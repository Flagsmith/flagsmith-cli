import argparse
import contextlib
import io
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import unittest
from unittest.mock import patch

SKILL = Path(__file__).resolve().parents[2] / "skills" / "rehearse-flag-rollback"
sys.path.insert(0, str(SKILL / "scripts"))
import rehearse


class RehearsalTests(unittest.TestCase):
    def setUp(self):
        self.opts = argparse.Namespace(
            api_url="https://admin.example", sdk_api_url="https://sdk.example",
            project=4, source="Staging", flag="reader", probe="probe", before="on",
            identity=None, trait=[], plan=["first", "second"], check=["balance"],
            probe_timeout=2, convergence_timeout=1,
        )
        self.source = {"id": 1, "name": "Staging", "project": 4, "api_key": "src"}
        self.calls = []
        self.evaluation_timeouts = []
        self.responses = {}
        self.snapshot = {"balance": 17}

    def invoke(self, args, env, phase, *, payload=None, **kwargs):
        self.calls.append(phase)
        if phase == "SDK evaluation":
            self.evaluation_timeouts.append(kwargs.get("timeout"))
        if phase.startswith("probe "):
            self.assertNotIn("FLAGSMITH_API_KEY", env)
            self.assertEqual(env["REHEARSAL_SDK_KEY"], "copy")
            self.assertEqual(payload["identity"], self.opts.identity)
            self.assertEqual(payload["traits"], self.opts.trait)
        else:
            self.assertNotIn("FLAGSMITH_ENVIRONMENT_KEY", env)
            if phase not in ("source lookup", "clone", "check identity storage", "check source identity overrides"):
                self.assertEqual(args[args.index("--environment") + 1], "copy")

        if phase in self.responses:
            result = self.responses[phase]
            if isinstance(result, BaseException):
                raise result
            return result
        if phase == "source lookup":
            return self.source
        if phase == "check identity storage":
            return True
        if phase == "check source identity overrides":
            self.assertIn("--identities", args)
            self.assertEqual(args[args.index("--feature") + 1], self.opts.flag)
            self.assertEqual(args[args.index("--environment") + 1], "src")
            return True
        if phase == "clone":
            self.assertEqual(args[args.index("clone") + 1], "src")
            return dict(self.source, id=2, name=args[args.index("clone") + 2], api_key="copy")
        if phase in ("flag enable", "flag disable"):
            self.enabled = phase == "flag enable"
        elif phase in ("SDK evaluation", "admin readback"):
            return self.enabled
        elif phase == "probe capture":
            return self.snapshot
        elif phase == "probe replay":
            self.assertEqual(payload["snapshot"], self.snapshot)
            return {"balance": payload["plan"] == "second"}
        elif phase == "delete clone":
            self.assertEqual(args[args.index("delete") + 1], "copy")
        elif phase == "verify deletion":
            return [1]
        else:
            self.fail(phase)

    def run_rehearsal(self):
        with patch.object(rehearse, "execute", self.invoke), contextlib.redirect_stderr(io.StringIO()):
            return rehearse.rehearsal(self.opts)

    def test_mixed_plans_replay_one_capture(self):
        # Given
        with patch.dict(os.environ, FLAGSMITH_ENVIRONMENT_KEY="other", FLAGSMITH_API_KEY="admin"):
            # When
            report, code = self.run_rehearsal()

        # Then
        self.assertEqual(code, 1)
        self.assertEqual(report["plans"], {"first": {"balance": False}, "second": {"balance": True}})
        self.assertEqual(report["cleanup"], "verified")
        self.assertEqual(self.calls.count("probe capture"), 1)

    def test_reverse_change_with_synthetic_identity(self):
        # Given
        self.opts.before = "off"
        self.opts.identity = "reader"
        self.opts.trait = ["region=test"]
        self.opts.plan = ["second"]

        # When
        _, code = self.run_rehearsal()

        # Then
        self.assertEqual(code, 0)
        self.assertLess(self.calls.index("flag disable"), self.calls.index("flag enable"))

    def test_identity_overrides_stop_before_clone(self):
        # Given
        self.opts.identity = "reader"
        self.responses["check source identity overrides"] = False

        # When
        _, code = self.run_rehearsal()

        # Then
        self.assertEqual(code, 2)
        self.assertNotIn("clone", self.calls)

    def test_unavailable_identity_storage_stops_before_clone(self):
        # Given
        self.opts.identity = "reader"
        for unknown in (False, None):
            with self.subTest(storage=unknown):
                self.responses["check identity storage"] = unknown

                # When
                _, code = self.run_rehearsal()

                # Then
                self.assertEqual(code, 2)
                self.assertNotIn("check source identity overrides", self.calls)
                self.assertNotIn("clone", self.calls)

    def test_origins_reject_trailing_query_and_fragment_delimiters(self):
        # Given
        for value in ("https://api.example?", "https://api.example/#"):
            with self.subTest(value=value):
                # When
                with self.assertRaises(argparse.ArgumentTypeError) as error:
                    rehearse.origin(value)

                # Then
                self.assertIn("origin", str(error.exception))

    def test_invalid_source_or_clone_identity_prevents_writes(self):
        # Given
        for phase, response in (("source lookup", dict(self.source, id=None)), ("clone", self.source)):
            with self.subTest(phase=phase):
                self.responses = {phase: response}
                self.calls.clear()

                # When
                _, code = self.run_rehearsal()

                # Then
                self.assertEqual(code, 2)
                self.assertNotIn("flag enable", self.calls)
                self.assertNotIn("delete clone", self.calls)

    def test_missing_or_malformed_observations(self):
        # Given
        for observed in ({"balance": None}, {}, {"balance": 1}, {"unrequested": True}):
            with self.subTest(observed=observed):
                self.responses["probe replay"] = observed

                # When
                report, code = self.run_rehearsal()

                # Then
                self.assertEqual((code, report["cleanup"]), (2, "verified"))

    def test_interrupted_capture_and_failed_readback_clean_up(self):
        # Given
        for phase, response in (("probe capture", KeyboardInterrupt()), ("admin readback", None),
                                ("probe replay", rehearse.Inconclusive("probe timeout"))):
            with self.subTest(phase=phase):
                self.responses = {phase: response}

                # When
                report, code = self.run_rehearsal()

                # Then
                self.assertEqual((code, report["cleanup"]), (2, "verified"))

    def test_sdk_polls_only_within_remaining_deadline(self):
        # Given
        self.responses["SDK evaluation"] = False
        # One second total. The second poll begins with a quarter-second left.
        with patch.object(rehearse.time, "monotonic", side_effect=[0, 0.125, 0.25, 0.75, 0.875, 1]), \
                patch.object(rehearse.time, "sleep") as sleep:
            # When
            report, code = self.run_rehearsal()

        # Then
        self.assertEqual((code, report["cleanup"]), (2, "verified"))
        self.assertNotIn("probe capture", self.calls)
        self.assertEqual(self.evaluation_timeouts, [0.875, 0.25])
        self.assertEqual([call.args[0] for call in sleep.call_args_list], [0.5, 0.125])

    def test_late_sdk_success_is_inconclusive(self):
        # Given
        self.responses["SDK evaluation"] = True
        with patch.object(rehearse.time, "monotonic", side_effect=[0, 0.25, 1.125]):
            # When
            report, code = self.run_rehearsal()

        # Then
        self.assertEqual((code, report["cleanup"]), (2, "verified"))
        self.assertNotIn("probe capture", self.calls)

    def test_unverified_cleanup_overrides_pass(self):
        # Given
        self.opts.plan = ["second"]
        for ids in ([1, 2], ["2"], [], None):
            with self.subTest(ids=ids):
                self.responses["verify deletion"] = ids

                # When
                report, code = self.run_rehearsal()

                # Then
                self.assertEqual((code, report["cleanup"]), (2, "inspect-by-name"))

    def test_command_errors_redact_arguments_and_raw_output(self):
        # Given
        command = [sys.executable, "-c", "print('private data')"]

        # When
        with self.assertRaises(rehearse.Inconclusive) as error:
            rehearse.execute(command, {}, "probe")

        # Then
        self.assertEqual(str(error.exception), "probe: JSONDecodeError")

    def test_anonymous_queue_example_rejects_targeting(self):
        # Given
        request = {"identity": "reader", "traits": []}

        # When
        run = subprocess.run(
            [sys.executable, str(SKILL / "examples" / "queue_probe.py"), "capture"],
            input=json.dumps(request), capture_output=True, text=True,
        )

        # Then
        self.assertNotEqual(run.returncode, 0)
        self.assertIn("anonymous", run.stderr)


class QueueExampleTests(unittest.TestCase):
    def test_versioned_job_survives_only_with_reader_available(self):
        # Given
        class SDK(BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path != "/api/v1/flags/" or self.headers.get("X-Environment-Key") != "copy":
                    self.send_error(404)
                    return
                self.send_response(200)
                self.end_headers()
                flags = [{"feature": {"name": "reader"}, "enabled": self.server.enabled}]
                self.wfile.write(json.dumps(flags).encode())

            def log_message(self, *args):
                pass

        with HTTPServer(("127.0.0.1", 0), SDK) as server:
            worker = threading.Thread(target=server.serve_forever)
            worker.start()
            env = {**os.environ, "REHEARSAL_SDK_URL": f"http://127.0.0.1:{server.server_port}",
                   "REHEARSAL_SDK_KEY": "copy", "REHEARSAL_FLAG": "reader"}

            def probe(phase, plan=None, snapshot=None):
                result = subprocess.run(
                    [sys.executable, str(SKILL / "examples" / "queue_probe.py"), phase],
                    input=json.dumps(dict(identity=None, traits=[], plan=plan, snapshot=snapshot)),
                    env=env, capture_output=True, check=True, text=True, timeout=5,
                )
                return json.loads(result.stdout)

            try:
                server.enabled = True
                # When
                snapshot = probe("capture")
                server.enabled = False
                outcomes = {plan: probe("replay", plan, snapshot) for plan in ("immediate", "drain")}

                # Then
                for plan, observed in outcomes.items():
                    with self.subTest(plan=plan):
                        self.assertEqual(observed,
                                         {"fresh_legacy_job": True, "inflight_delivered_once": plan == "drain"})
            finally:
                server.shutdown()
                worker.join()
