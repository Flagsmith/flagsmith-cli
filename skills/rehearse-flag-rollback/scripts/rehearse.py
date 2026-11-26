#!/usr/bin/env python3
"""Compare application rollback policies in a disposable Flagsmith environment."""

import argparse
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import sys
import time
from urllib.parse import urlsplit


class Inconclusive(Exception):
    pass


def origin(value):
    try:
        url = urlsplit(value)
        port = url.port
    except ValueError:
        raise argparse.ArgumentTypeError("invalid API origin") from None
    if url.hostname and url.path in ("", "/") and not (
        url.username is not None or url.password is not None or port == 0 or "?" in value or "#" in value
    ):
        if url.scheme == "https":
            return value.rstrip("/")
        try:
            local = ipaddress.ip_address(url.hostname).is_loopback
        except ValueError:
            local = url.hostname == "localhost"
        if local and url.scheme == "http":
            return value.rstrip("/")
    raise argparse.ArgumentTypeError("use an HTTPS origin or local loopback HTTP origin")


def label(value):
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}", value):
        raise argparse.ArgumentTypeError("expected a short identifier")
    return value


def execute(args, env, phase, *, payload=None, timeout=30, read=True):
    try:
        result = subprocess.run(args, env=env, input=json.dumps(payload), text=True,
                                capture_output=True, timeout=timeout, check=True)
        if read:
            return json.loads(result.stdout)
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        # Raw CLI diagnostics and application output can contain credentials or artifacts.
        raise Inconclusive(f"{phase}: {type(exc).__name__}") from None


def rehearsal(opts):
    name = "rehearsal-" + secrets.token_hex(10)
    report = {"sandbox": name, "plans": {}, "cleanup": "not-created"}
    admin = {k: v for k, v in os.environ.items()
             if not k.upper().startswith("FLAGSMITH_ENVIRONMENT_KEY")}
    base = ["flagsmith", "--no-input", "--api-url", opts.api_url, "--sdk-api-url",
            opts.sdk_api_url, "--project", str(opts.project)]
    key = None

    def cli(*args, query=None, phase, environment=None, timeout=30):
        target = environment or key
        command = base + (["--environment", target] if target else []) + list(args)
        if query:
            command += ["--jq", query]
        return execute(command, admin, phase, read=bool(query), timeout=timeout)

    def switch(enabled):
        action = "enable" if enabled else "disable"
        cli("flag", action, opts.flag, "--yes", phase=f"flag {action}")
        if cli("flag", "get", opts.flag, query=".enabled", phase="admin readback") is not enabled:
            raise Inconclusive("unexpected environment default")
        audience = ["--identity", opts.identity] if opts.identity else []
        for trait in opts.trait:
            audience.extend(["--trait", trait])
        deadline = time.monotonic() + opts.convergence_timeout
        delay = 0.5
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            state = cli("evaluate", opts.flag, *audience, query=".enabled", phase="SDK evaluation",
                        timeout=min(30, remaining))
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            if state is enabled:
                return
            time.sleep(min(delay, remaining))
            delay = min(delay * 2, 5)
        raise Inconclusive("SDK assignment did not converge for this audience")

    def probe(phase, plan=None, snapshot=None):
        # Pass an explicit environment to the trusted probe. The CLI admin credential
        # and unrelated CI secrets are not needed by the application under test.
        env = {k: os.environ[k] for k in ("PATH", "LANG", "SYSTEMROOT") if k in os.environ}
        env.update(REHEARSAL_SDK_URL=opts.sdk_api_url, REHEARSAL_SDK_KEY=key,
                   REHEARSAL_FLAG=opts.flag)
        return execute([opts.probe, phase], env, f"probe {phase}", timeout=opts.probe_timeout,
                       payload={"plan": plan, "snapshot": snapshot, "identity": opts.identity,
                                "traits": opts.trait})

    def valid_environment(env):
        return (isinstance(env, dict) and type(env.get("project")) is int
                and env["project"] == opts.project and type(env.get("id")) is int
                and env["id"] > 0 and isinstance(env.get("api_key"), str)
                and bool(env["api_key"]) and not env["api_key"].startswith("-"))

    fields = "{id,name,project,api_key}"
    try:
        source = cli("environment", "get", opts.source, query=fields, phase="source lookup")
        if not valid_environment(source):
            raise Inconclusive("source environment identity unverified")
        if opts.identity:
            # Older Edge indexes and projects migrating between stores may omit overrides.
            ready = cli("project", "get", str(opts.project), phase="check identity storage",
                        query="(.enable_dynamo_db == false and .use_edge_identities == false) or "
                              "(.enable_dynamo_db == true and .use_edge_identities == true and "
                              ".show_edge_identity_overrides_for_feature == true)")
            if ready is not True:
                raise Inconclusive("source identity storage cannot verify all overrides")
            empty = cli("flag", "list", "--feature", opts.flag, "--identities",
                        environment=source["api_key"], phase="check source identity overrides",
                        query='type == "array" and length == 0')
            if empty is not True:
                raise Inconclusive("source identity overrides cannot be reproduced by cloning")
        report["cleanup"] = "inspect-by-name"
        print(f"Temporary environment: {name} (project {opts.project})", file=sys.stderr, flush=True)
        clone = cli("environment", "clone", source["api_key"], name, query=fields, phase="clone")
        if not (valid_environment(clone) and clone.get("name") == name
                and clone["id"] != source["id"] and clone["api_key"] != source["api_key"]):
            raise Inconclusive("clone identity unverified")
        key = clone["api_key"]
        switch(opts.before == "on")
        snapshot = probe("capture")
        if snapshot is None:
            raise Inconclusive("missing capture")
        switch(opts.before != "on")
        for plan in opts.plan:
            observed = probe("replay", plan, snapshot)
            if (not isinstance(observed, dict) or set(observed) != set(opts.check)
                    or any(value is not None and type(value) is not bool for value in observed.values())):
                raise Inconclusive(f"invalid observations for {plan}")
            report["plans"][plan] = observed
    except (Inconclusive, KeyboardInterrupt) as exc:
        report["error"] = str(exc) or "interrupted"
    finally:
        if key:
            try:
                cli("environment", "delete", key, "--yes", phase="delete clone")
                ids = cli("environment", "list", query="map(.id)", phase="verify deletion")
                if (not isinstance(ids, list) or any(type(i) is not int for i in ids)
                        or clone["id"] in ids or source["id"] not in ids):
                    raise Inconclusive("clone deletion unverified")
                report["cleanup"] = "verified"
            except (Inconclusive, KeyboardInterrupt):
                report["cleanup"] = "inspect-by-name"
    checks = [value for results in report["plans"].values() for value in results.values()]
    if "error" in report or report["cleanup"] != "verified" or None in checks:
        return report, 2
    return report, 0 if all(checks) else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api-url", required=True, type=origin)
    parser.add_argument("--sdk-api-url", required=True, type=origin)
    parser.add_argument("--project", required=True, type=int)
    parser.add_argument("--source", required=True)
    parser.add_argument("--flag", required=True, type=label)
    parser.add_argument("--before", choices=["on", "off"], default="on")
    parser.add_argument("--identity")
    parser.add_argument("--trait", action="append", default=[])
    parser.add_argument("--probe", required=True, type=lambda s: str(Path(s).resolve()))
    parser.add_argument("--probe-timeout", type=int, default=30)
    parser.add_argument("--convergence-timeout", type=int, default=90, metavar="SECONDS")
    parser.add_argument("--plan", type=label, action="append", required=True)
    parser.add_argument("--check", type=label, action="append", required=True)
    opts = parser.parse_args()
    if opts.source.startswith("-") or not opts.source.strip() or opts.project < 1:
        parser.error("supply a project ID and source environment name or key")
    if opts.probe_timeout <= 0 or not os.access(opts.probe, os.X_OK) or not Path(opts.probe).is_file():
        parser.error("supply an executable probe and positive timeout")
    if len(set(opts.plan)) != len(opts.plan) or len(set(opts.check)) != len(opts.check):
        parser.error("duplicate plan or check")
    if opts.convergence_timeout <= 0:
        parser.error("supply a positive SDK convergence timeout")
    if opts.identity is not None and not opts.identity.strip():
        parser.error("supply a non-empty synthetic identity")
    if opts.trait and not opts.identity:
        parser.error("traits require an identity")

    def interrupt(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupt)
    report, code = rehearsal(opts)
    report["verdict"] = ("pass", "fail", "inconclusive")[code]
    print(json.dumps(report, separators=(",", ":")))
    return code


if __name__ == "__main__":
    sys.exit(main())
