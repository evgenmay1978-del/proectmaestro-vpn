#!/usr/bin/env python3
"""Build and verify the immutable commercial sidecar bundle manifest."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
from typing import Any


SCHEMA = "maestro-xray-cdn-commercial-bundle-v1"
XRAY_VERSION = "26.5.9"
XRAY_ARCHIVE_SHA256 = "f56c106b7c0159ad386bccd340faa5bbf55fd5c15821ec9e63e6a6ba11d3d1c7"
MANIFEST_NAME = "manifest.json"
EXECUTABLE_MEMBERS = {
    "bin/maestro-xray-cdn-agent",
    "bin/maestro-xray-cdn-commercial-operator",
    "bin/xray",
}
MEMBERS = (
    "bin/maestro-xray-cdn-agent",
    "bin/maestro-xray-cdn-commercial-operator",
    "bin/xray",
    "lib/commercial_bundle.py",
    "rollback.json",
    "systemd/maestro-xray-cdn-commercial-agent.service",
    "systemd/maestro-xray-cdn-commercial.service",
    "sysusers/maestro-xray-cdn-commercial.conf",
    "templates/config.json.tmpl",
)
_SHA_RE = re.compile(r"^[0-9a-f]{64}$")
_COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")


class ManifestError(RuntimeError):
    pass


def _fail(code: str) -> None:
    raise ManifestError(code)


def _canonical(value: Any) -> bytes:
    return (json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True) + "\n").encode("utf-8")


def _sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def _member_metadata(root: Path, relative: str) -> dict[str, Any]:
    path = root / relative
    try:
        metadata = path.lstat()
    except OSError:
        _fail("bundle_member_missing")
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or metadata.st_size <= 0:
        _fail("bundle_member_invalid")
    expected_mode = 0o755 if relative in EXECUTABLE_MEMBERS else 0o644
    if os.name == "posix" and stat.S_IMODE(metadata.st_mode) != expected_mode:
        _fail("bundle_member_mode_invalid")
    try:
        raw = path.read_bytes()
    except OSError:
        _fail("bundle_member_invalid")
    return {"path": relative, "sha256": _sha256(raw), "size_bytes": len(raw), "mode": format(expected_mode, "04o")}


def _exact_members(root: Path, *, manifest_expected: bool) -> None:
    if not root.is_absolute() or root.is_symlink() or not root.is_dir():
        _fail("bundle_root_invalid")
    actual = {
        entry.relative_to(root).as_posix()
        for entry in root.rglob("*")
        if entry.is_file() or entry.is_symlink()
    }
    expected = set(MEMBERS)
    if manifest_expected:
        expected.add(MANIFEST_NAME)
    if actual != expected:
        _fail("bundle_member_set_invalid")


def create_manifest(
    root: Path | str,
    *,
    source_commit: str,
    xray_version: str,
    xray_archive_sha256: str,
) -> dict[str, Any]:
    bundle_root = Path(root).resolve(strict=True)
    if not _COMMIT_RE.fullmatch(source_commit) or xray_version != XRAY_VERSION or xray_archive_sha256 != XRAY_ARCHIVE_SHA256:
        _fail("bundle_identity_invalid")
    _exact_members(bundle_root, manifest_expected=False)
    members = [_member_metadata(bundle_root, relative) for relative in MEMBERS]
    manifest = {
        "arch": "amd64",
        "deployment_scope": "commercial_yandex_cdn_sidecar_only",
        "members": members,
        "os": "linux",
        "profiles": {
            "s4-commercial": {"agent_port": 18443, "api_port": 28082, "proxy_target": "http://127.0.0.1:28081", "relay_port": 18084, "xhttp_port": 28081},
            "standard": {"agent_port": 18443, "api_port": 18082, "proxy_target": "http://127.0.0.1:18081", "relay_port": 18084, "xhttp_port": 18081},
        },
        "rollback": {
            "active_pointer": "/opt/maestro-xray-cdn-commercial/current",
            "last_known_good_record": "/var/lib/maestro-xray-cdn-commercial/operator-state.json",
            "unit_names": ["maestro-xray-cdn-commercial.service", "maestro-xray-cdn-commercial-agent.service"],
        },
        "schema": SCHEMA,
        "source_commit": source_commit,
        "xray_archive_sha256": xray_archive_sha256,
        "xray_version": xray_version,
    }
    output = bundle_root / MANIFEST_NAME
    if output.exists() or output.is_symlink():
        _fail("bundle_manifest_exists")
    output.write_bytes(_canonical(manifest))
    output.chmod(0o644)
    verify_manifest(bundle_root)
    return manifest


def verify_manifest(root: Path | str) -> dict[str, Any]:
    bundle_root = Path(root).resolve(strict=True)
    _exact_members(bundle_root, manifest_expected=True)
    manifest_path = bundle_root / MANIFEST_NAME
    metadata = manifest_path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or (os.name == "posix" and stat.S_IMODE(metadata.st_mode) != 0o644):
        _fail("bundle_manifest_invalid")
    raw = manifest_path.read_bytes()
    try:
        manifest = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError):
        _fail("bundle_manifest_invalid")
    if _canonical(manifest) != raw:
        _fail("bundle_manifest_noncanonical")
    expected_keys = {"arch", "deployment_scope", "members", "os", "profiles", "rollback", "schema", "source_commit", "xray_archive_sha256", "xray_version"}
    if set(manifest) != expected_keys or manifest["schema"] != SCHEMA or manifest["os"] != "linux" or manifest["arch"] != "amd64":
        _fail("bundle_manifest_invalid")
    if manifest["deployment_scope"] != "commercial_yandex_cdn_sidecar_only" or not _COMMIT_RE.fullmatch(manifest.get("source_commit", "")):
        _fail("bundle_manifest_invalid")
    if manifest["xray_version"] != XRAY_VERSION or manifest["xray_archive_sha256"] != XRAY_ARCHIVE_SHA256:
        _fail("bundle_manifest_invalid")
    if manifest.get("profiles") != {
        "s4-commercial": {"agent_port": 18443, "api_port": 28082, "proxy_target": "http://127.0.0.1:28081", "relay_port": 18084, "xhttp_port": 28081},
        "standard": {"agent_port": 18443, "api_port": 18082, "proxy_target": "http://127.0.0.1:18081", "relay_port": 18084, "xhttp_port": 18081},
    }:
        _fail("bundle_manifest_invalid")
    if manifest.get("rollback") != {
        "active_pointer": "/opt/maestro-xray-cdn-commercial/current",
        "last_known_good_record": "/var/lib/maestro-xray-cdn-commercial/operator-state.json",
        "unit_names": ["maestro-xray-cdn-commercial.service", "maestro-xray-cdn-commercial-agent.service"],
    }:
        _fail("bundle_manifest_invalid")
    members = manifest.get("members")
    if not isinstance(members, list) or [item.get("path") for item in members if isinstance(item, dict)] != list(MEMBERS):
        _fail("bundle_manifest_invalid")
    for item, relative in zip(members, MEMBERS):
        if set(item) != {"mode", "path", "sha256", "size_bytes"} or not _SHA_RE.fullmatch(item.get("sha256", "")):
            _fail("bundle_manifest_invalid")
        actual = _member_metadata(bundle_root, relative)
        if item != actual:
            _fail("bundle_member_digest_mismatch")
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    create = subparsers.add_parser("create")
    create.add_argument("--bundle", required=True)
    create.add_argument("--source-commit", required=True)
    create.add_argument("--xray-version", required=True)
    create.add_argument("--xray-archive-sha256", required=True)
    verify = subparsers.add_parser("verify")
    verify.add_argument("--bundle", required=True)
    args = parser.parse_args()
    try:
        if args.command == "create":
            result = create_manifest(args.bundle, source_commit=args.source_commit, xray_version=args.xray_version, xray_archive_sha256=args.xray_archive_sha256)
        else:
            result = verify_manifest(args.bundle)
    except (ManifestError, OSError) as error:
        print(str(error), file=os.sys.stderr)
        return 4
    print(json.dumps({"schema": result["schema"], "source_commit": result["source_commit"]}, separators=(",", ":"), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
