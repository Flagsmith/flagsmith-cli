#!/usr/bin/env python3
"""Build PyPI wheels around the binaries from GoReleaser.

The binary is shipped in the wheel's .data/scripts/ directory,
which every installer drops straight onto PATH.
This should make `uv tool install flagsmith-cli` (and pip, and pipx) hand
you a working `flagsmith` without a Python shim in the way.

Reads dist/artifacts.json + dist/metadata.json, writes wheels to
dist/pypi/.

    python3 packaging/pypi/build_wheels.py --dist dist
"""

from __future__ import annotations

import argparse
import base64
import csv
import hashlib
import io
import json
import re
import sys
import zipfile
from dataclasses import dataclass
from pathlib import Path

PACKAGE = "flagsmith-cli"
# Wheel filenames and .dist-info/.data directories use the escaped name.
PACKAGE_ESCAPED = PACKAGE.replace("-", "_")
BINARY = "flagsmith"
SUMMARY = "The Flagsmith command-line interface"
HOMEPAGE = "https://github.com/Flagsmith/flagsmith-cli"
REQUIRES_PYTHON = ">=3.8"

# GoReleaser target -> wheel platform tag(s). A wheel may claim several
# platforms; the compressed tag set is joined with "." in the filename.
# CGO is disabled, so the Linux binaries are static and run on musl too.
#
# A macOS tag is a minimum: macosx_12_0 installs on 12 and newer. Keep it at
# the floor of the Go version in go.mod, which the release job builds with --
# claiming more than the binary supports means installing onto a macOS that
# cannot run it. Go 1.26 needs macOS 12; 1.27 moves to 13. See
# https://go.dev/wiki/MinimumRequirements
PLATFORM_TAGS: dict[tuple[str, str], list[str]] = {
    ("darwin", "amd64"): ["macosx_12_0_x86_64"],
    ("darwin", "arm64"): ["macosx_12_0_arm64"],
    ("linux", "amd64"): ["manylinux2014_x86_64", "musllinux_1_1_x86_64"],
    ("linux", "arm64"): ["manylinux2014_aarch64", "musllinux_1_1_aarch64"],
    ("windows", "amd64"): ["win_amd64"],
    ("windows", "arm64"): ["win_arm64"],
}

PRERELEASE = {"alpha": "a", "beta": "b", "rc": "rc"}


def pep440_version(tag: str) -> str:
    """v2.0.0-beta.3 -> 2.0.0b3. Anything unexpected is a hard error."""
    match = re.fullmatch(r"v?(\d+\.\d+\.\d+)(?:-([a-z]+)\.?(\d+))?", tag)
    if not match:
        raise SystemExit(f"cannot convert tag {tag!r} to a PEP 440 version")
    release, kind, number = match.groups()
    if kind is None:
        return release
    if kind not in PRERELEASE:
        raise SystemExit(f"unknown prerelease segment {kind!r} in tag {tag!r}")
    return f"{release}{PRERELEASE[kind]}{number}"


def metadata(version: str, readme: str) -> str:
    return (
        "Metadata-Version: 2.4\n"
        f"Name: {PACKAGE}\n"
        f"Version: {version}\n"
        f"Summary: {SUMMARY}\n"
        f"Project-URL: Homepage, {HOMEPAGE}\n"
        f"Project-URL: Source, {HOMEPAGE}\n"
        f"Project-URL: Issues, {HOMEPAGE}/issues\n"
        "License-Expression: MIT\n"
        "License-File: LICENSE\n"
        "Keywords: cli,feature-flags,flagsmith\n"
        "Classifier: Development Status :: 4 - Beta\n"
        "Classifier: Environment :: Console\n"
        "Classifier: Intended Audience :: Developers\n"
        "Classifier: Programming Language :: Go\n"
        "Classifier: Topic :: Software Development\n"
        f"Requires-Python: {REQUIRES_PYTHON}\n"
        "Description-Content-Type: text/markdown\n"
        "\n"
        f"{readme}"
    )


@dataclass
class Record:
    entries: list[tuple[str, str, int]]

    def add(self, name: str, data: bytes) -> None:
        digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest())
        self.entries.append((name, f"sha256={digest.rstrip(b'=').decode()}", len(data)))

    def render(self, record_name: str) -> bytes:
        out = io.StringIO()
        writer = csv.writer(out, lineterminator="\n")
        writer.writerows(self.entries)
        writer.writerow([record_name, "", ""])
        return out.getvalue().encode()


def build_wheel(
    *,
    binary: Path,
    goos: str,
    tags: list[str],
    version: str,
    readme: str,
    license_text: str,
    out_dir: Path,
) -> Path:
    dist_info = f"{PACKAGE_ESCAPED}-{version}.dist-info"
    data_scripts = f"{PACKAGE_ESCAPED}-{version}.data/scripts"
    script_name = f"{BINARY}.exe" if goos == "windows" else BINARY
    tag = ".".join(tags)

    files: list[tuple[str, bytes, int]] = [
        (f"{dist_info}/METADATA", metadata(version, readme).encode(), 0o644),
        (
            f"{dist_info}/WHEEL",
            (
                "Wheel-Version: 1.0\n"
                f"Generator: {Path(__file__).name}\n"
                "Root-Is-Purelib: false\n"
                + "".join(f"Tag: py3-none-{t}\n" for t in tags)
            ).encode(),
            0o644,
        ),
        (f"{dist_info}/licenses/LICENSE", license_text.encode(), 0o644),
        (f"{data_scripts}/{script_name}", binary.read_bytes(), 0o755),
    ]

    record = Record(entries=[])
    path = out_dir / f"{PACKAGE_ESCAPED}-{version}-py3-none-{tag}.whl"
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as wheel:
        for name, data, mode in files:
            record.add(name, data)
            # Fixed timestamp: same inputs, same wheel, byte for byte.
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.external_attr = (mode << 16) | 0o100000
            info.compress_type = zipfile.ZIP_DEFLATED
            wheel.writestr(info, data)
        record_name = f"{dist_info}/RECORD"
        info = zipfile.ZipInfo(record_name, date_time=(1980, 1, 1, 0, 0, 0))
        info.external_attr = (0o644 << 16) | 0o100000
        info.compress_type = zipfile.ZIP_DEFLATED
        wheel.writestr(info, record.render(record_name))
    return path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--dist", type=Path, default=Path("dist"), help="GoReleaser dist directory"
    )
    parser.add_argument(
        "--out", type=Path, default=None, help="output directory (default: <dist>/pypi)"
    )
    parser.add_argument(
        "--version",
        default=None,
        help="override the version (default: tag from metadata.json)",
    )
    args = parser.parse_args()

    repo = Path(__file__).resolve().parents[2]
    artifacts = json.loads((args.dist / "artifacts.json").read_text())
    meta = json.loads((args.dist / "metadata.json").read_text())
    version = args.version or pep440_version(meta["tag"])

    out_dir = args.out or args.dist / "pypi"
    out_dir.mkdir(parents=True, exist_ok=True)
    readme = (repo / "README.md").read_text()
    license_text = (repo / "LICENSE").read_text()

    built = 0
    for artifact in artifacts:
        if artifact.get("type") != "Binary":
            continue
        target = (artifact["goos"], artifact["goarch"])
        tags = PLATFORM_TAGS.get(target)
        if tags is None:
            print(
                f"skipping {target[0]}/{target[1]}: no wheel platform tag",
                file=sys.stderr,
            )
            continue
        path = build_wheel(
            binary=Path(artifact["path"]),
            goos=target[0],
            tags=tags,
            version=version,
            readme=readme,
            license_text=license_text,
            out_dir=out_dir,
        )
        print(f"{target[0]}/{target[1]} -> {path}")
        built += 1

    if not built:
        raise SystemExit("no binaries found in artifacts.json")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
