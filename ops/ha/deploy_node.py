"""Deterministic, offline-only MaestroVPN HA node deployment planner."""

from __future__ import annotations

import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
from typing import Any, Sequence, TextIO

from ops.ha import build_manifest, pki_verify


class DeployPlanError(ValueError):
    """A fixed, redacted deploy-plan contract failure."""


_ERRORS = {
    "input": "deploy-node:invalid-input",
    "inventory": "deploy-node:invalid-inventory",
    "transport": "deploy-node:invalid-transport",
    "artifact": "deploy-node:invalid-artifact",
    "pki": "deploy-node:invalid-pki",
    "templates": "deploy-node:invalid-templates",
}
_INVENTORY_SCHEMA = "maestro-ha-node-inventory-v1"
_TRANSPORT_SCHEMA = "maestro-ha-artifact-transport-evidence-v1"
_PLAN_SCHEMA = "maestro-ha-deploy-plan-v1"
_PANEL_ARTIFACT_COMMIT_SHA = "f577c67ad229fe89278430d35a3ec65f6ce454e5"
_TEMPLATE_SOURCE_COMMIT_SHA = "8289ce78be8dcb2c00829d6b9781d4b52a18cb73"
_ARTIFACT_MEMBER_NAMES = ("maestro-panel", "manifest.json")
_TEMPLATE_NAMES = (
    "maestro-panel.env.example",
    "maestro-panel.service",
    "rqlite-s2.env.example",
    "rqlite-s3.env.example",
    "rqlite-s4.env.example",
    "rqlited@.service",
)
_TRUSTED_TEMPLATE_FACTS = {
    "maestro-panel.env.example": (
        "2aea7a7ae8983b57c8ea0d51d13ae58e3562f782b3a38c4e8fd663257b2e68ad",
        656,
    ),
    "maestro-panel.service": (
        "0a38c7f5bdceb9ad576dc9a52e20b4b6712d527137634de6bb14e59de05368f9",
        1386,
    ),
    "rqlite-s2.env.example": (
        "1f1ce1324360755da5b87efe0e838f4bd1ecee044308bff2f41b8f02f7d6465c",
        656,
    ),
    "rqlite-s3.env.example": (
        "e01c8dbb6e67c8da5e5049337bb6b54e695b965000902b562ec2cfcf079a36d0",
        656,
    ),
    "rqlite-s4.env.example": (
        "8a6e6df069e4c1455ce1dda1186782564d6babc44dff87c5daa1ea4dda621214",
        656,
    ),
    "rqlited@.service": (
        "98979f28b9fe7727f30e8a733c7d9de9f998e59b242d09ad190f5c2bac368364",
        1749,
    ),
}
_INVENTORY_KEYS = {
    "artifact",
    "logical_addresses",
    "node_id",
    "role",
    "schema",
    "target",
    "templates",
}
_ARTIFACT_IDENTITY_KEYS = {
    "archive_sha256",
    "artifact_id",
    "artifact_name",
    "commit_sha",
    "ref",
    "repository",
    "workflow_run_attempt",
    "workflow_run_id",
}
_TRANSPORT_KEYS = _ARTIFACT_IDENTITY_KEYS | {"members", "schema"}
_LOGICAL_ADDRESS_KEYS = {"rqlite_http", "rqlite_raft"}
_MAX_JSON_BYTES = 262144
_MAX_TEMPLATE_BYTES = 1048576
_MAX_MANIFEST_BYTES = 16384
_MAX_POSITIVE_INTEGER = 2**63 - 1
_MIN_PKI_REMAINING_SECONDS = 2592000
_COMMIT_RE = re.compile(r"[0-9a-f]{40}\Z")
_DIGEST_RE = re.compile(r"[0-9a-f]{64}\Z")
_REPOSITORY_PART_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,99}\Z")
_REF_BODY_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._/-]{0,239}\Z")
_PULL_REF_RE = re.compile(r"refs/pull/[1-9][0-9]{0,9}/(?:head|merge)\Z")
_TIMESTAMP_RE = re.compile(
    r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\Z"
)
_FIXED_BLOCKERS = (
    "artifact-attestation-required",
    "deployment-not-authorized",
    "node-identity-not-verified",
    "pki-private-material-not-provisioned",
    "rqlite-membership-not-provisioned",
    "runtime-smoke-not-verified",
    "service-activation-not-authorized",
    "template-rendering-not-implemented",
)


def _fail(code: str) -> None:
    raise DeployPlanError(_ERRORS[code])


def canonical_bytes(value: object) -> bytes:
    try:
        encoded = json.dumps(
            value,
            sort_keys=True,
            separators=(",", ":"),
            ensure_ascii=True,
            allow_nan=False,
        )
    except (TypeError, ValueError):
        _fail("input")
    return (encoded + "\n").encode("ascii")


def _strict_object(code: str):
    def decode(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, item in pairs:
            if key in value:
                _fail(code)
            value[key] = item
        return value

    return decode


def _reject_constant(code: str):
    def reject(_value: str) -> None:
        _fail(code)

    return reject


def _coerce_path(value: Path | str, *, code: str) -> Path:
    try:
        raw = os.fspath(value)
        if not isinstance(raw, str) or not raw or "\x00" in raw:
            _fail(code)
        return Path(os.path.abspath(raw))
    except DeployPlanError:
        raise
    except (OSError, TypeError, ValueError):
        _fail(code)


def _fingerprint(value: os.stat_result) -> tuple[int, int, int, int, int, int, int]:
    return (
        int(value.st_dev),
        int(value.st_ino),
        int(value.st_mode),
        int(value.st_nlink),
        int(value.st_size),
        int(value.st_mtime_ns),
        int(value.st_ctime_ns),
    )


def _read_regular_file(
    value: Path | str,
    *,
    limit: int,
    code: str,
    allow_empty: bool = False,
) -> bytes:
    path = _coerce_path(value, code=code)
    descriptor = -1
    try:
        before = os.lstat(path)
        if (
            stat.S_ISLNK(before.st_mode)
            or not stat.S_ISREG(before.st_mode)
            or before.st_nlink != 1
            or before.st_size < (0 if allow_empty else 1)
            or before.st_size > limit
        ):
            _fail(code)
        flags = os.O_RDONLY
        for option in ("O_NOFOLLOW", "O_CLOEXEC", "O_BINARY", "O_NOINHERIT"):
            flags |= int(getattr(os, option, 0))
        descriptor = os.open(path, flags)
        opened = os.fstat(descriptor)
        if _fingerprint(opened) != _fingerprint(before):
            _fail(code)
        chunks: list[bytes] = []
        remaining = limit + 1
        while remaining > 0:
            chunk = os.read(descriptor, min(65536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        payload = b"".join(chunks)
        after_descriptor = os.fstat(descriptor)
        after_path = os.lstat(path)
        if (
            len(payload) != before.st_size
            or len(payload) > limit
            or (not allow_empty and not payload)
            or _fingerprint(after_descriptor) != _fingerprint(before)
            or _fingerprint(after_path) != _fingerprint(before)
        ):
            _fail(code)
        return payload
    except DeployPlanError:
        raise
    except (OSError, TypeError, ValueError):
        _fail(code)
    finally:
        if descriptor >= 0:
            try:
                os.close(descriptor)
            except OSError:
                pass


def _strict_json(raw: bytes, *, code: str) -> dict[str, Any]:
    if not raw or len(raw) > _MAX_JSON_BYTES:
        _fail(code)
    try:
        value = json.loads(
            raw.decode("utf-8", errors="strict"),
            object_pairs_hook=_strict_object(code),
            parse_constant=_reject_constant(code),
        )
    except DeployPlanError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError, TypeError, ValueError):
        _fail(code)
    if type(value) is not dict or canonical_bytes(value) != raw:
        _fail(code)
    return value


def _valid_positive_integer(value: object) -> bool:
    return type(value) is int and 0 < value <= _MAX_POSITIVE_INTEGER


def _valid_repository(value: object) -> bool:
    if not isinstance(value, str) or len(value) > 200:
        return False
    try:
        value.encode("ascii")
    except UnicodeEncodeError:
        return False
    parts = value.split("/")
    return (
        len(parts) == 2
        and all(part not in {".", ".."} for part in parts)
        and all(_REPOSITORY_PART_RE.fullmatch(part) is not None for part in parts)
    )


def _valid_ref(value: object) -> bool:
    if not isinstance(value, str) or len(value) > 255:
        return False
    try:
        value.encode("ascii")
    except UnicodeEncodeError:
        return False
    if _PULL_REF_RE.fullmatch(value) is not None:
        return True
    prefix = next(
        (item for item in ("refs/heads/", "refs/tags/") if value.startswith(item)),
        None,
    )
    if prefix is None:
        return False
    body = value[len(prefix) :]
    return (
        _REF_BODY_RE.fullmatch(body) is not None
        and all(part not in {"", ".", ".."} for part in body.split("/"))
    )


def _validate_artifact_identity(value: object, *, code: str) -> dict[str, object]:
    if type(value) is not dict or set(value) != _ARTIFACT_IDENTITY_KEYS:
        _fail(code)
    commit_sha = value["commit_sha"]
    if not (
        _valid_repository(value["repository"])
        and _valid_ref(value["ref"])
        and isinstance(commit_sha, str)
        and _COMMIT_RE.fullmatch(commit_sha) is not None
        and commit_sha == _PANEL_ARTIFACT_COMMIT_SHA
        and _valid_positive_integer(value["workflow_run_id"])
        and _valid_positive_integer(value["workflow_run_attempt"])
        and _valid_positive_integer(value["artifact_id"])
        and value["artifact_name"] == f"maestro-panel-{commit_sha}"
        and isinstance(value["archive_sha256"], str)
        and _DIGEST_RE.fullmatch(value["archive_sha256"]) is not None
    ):
        _fail(code)
    return value


def _validate_inventory(value: dict[str, Any]) -> dict[str, object]:
    if set(value) != _INVENTORY_KEYS:
        _fail("inventory")
    node_id = value["node_id"]
    if node_id not in {"s2", "s3", "s4"}:
        _fail("inventory")
    logical_addresses = value["logical_addresses"]
    templates = value["templates"]
    expected_templates = [
        "maestro-panel.env.example",
        "maestro-panel.service",
        f"rqlite-{node_id}.env.example",
        "rqlited@.service",
    ]
    if not (
        value["schema"] == _INVENTORY_SCHEMA
        and value["role"] == "rqlite-voter"
        and value["target"] == "linux/amd64"
        and type(logical_addresses) is dict
        and set(logical_addresses) == _LOGICAL_ADDRESS_KEYS
        and logical_addresses["rqlite_http"] == f"rqlite-http-{node_id}"
        and logical_addresses["rqlite_raft"] == f"rqlite-raft-{node_id}"
        and templates == expected_templates
    ):
        _fail("inventory")
    _validate_artifact_identity(value["artifact"], code="inventory")
    return value


def _validate_transport(value: dict[str, Any]) -> dict[str, object]:
    if set(value) != _TRANSPORT_KEYS or value.get("schema") != _TRANSPORT_SCHEMA:
        _fail("transport")
    identity = {key: value[key] for key in _ARTIFACT_IDENTITY_KEYS}
    _validate_artifact_identity(identity, code="transport")
    if value["members"] != list(_ARTIFACT_MEMBER_NAMES):
        _fail("transport")
    return identity


def _parse_timestamp(value: object) -> datetime:
    if not isinstance(value, str) or _TIMESTAMP_RE.fullmatch(value) is None:
        _fail("pki")
    try:
        parsed = datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(
            tzinfo=timezone.utc
        )
    except ValueError:
        _fail("pki")
    return parsed


def _validate_pki_evidence(raw: bytes, *, node_id: str) -> tuple[dict[str, object], list[str]]:
    try:
        evidence = pki_verify.validate_evidence(raw)
    except pki_verify.PKIVerificationError:
        _fail("pki")
    evaluation = _parse_timestamp(evidence["evaluation_time"])
    minimum = evaluation + timedelta(seconds=_MIN_PKI_REMAINING_SECONDS)
    roles: set[str] = set()
    for domain in evidence["trust_domains"]:
        if _parse_timestamp(domain["ca_not_after"]) < minimum:
            _fail("pki")
        for certificate in domain["certificates"]:
            if _parse_timestamp(certificate["not_after"]) < minimum:
                _fail("pki")
            roles.add(certificate["role"])
    required_roles = sorted(
        (
            f"{node_id}-http-server",
            f"{node_id}-panel-rqlite-client",
            f"{node_id}-raft-peer",
        )
    )
    if not all(role in roles for role in required_roles):
        _fail("pki")
    return evidence, required_roles


def _template_root_fingerprint(value: os.stat_result) -> tuple[int, int, int, int, int]:
    return (
        int(value.st_dev),
        int(value.st_ino),
        int(value.st_mode),
        int(value.st_mtime_ns),
        int(value.st_ctime_ns),
    )


def _read_templates(value: Path | str) -> dict[str, bytes]:
    root = _coerce_path(value, code="templates")
    try:
        before = os.lstat(root)
        if stat.S_ISLNK(before.st_mode) or not stat.S_ISDIR(before.st_mode):
            _fail("templates")
        if sorted(os.listdir(root)) != list(_TEMPLATE_NAMES):
            _fail("templates")
        payloads = {
            name: _read_regular_file(
                root / name,
                limit=_MAX_TEMPLATE_BYTES,
                code="templates",
            )
            for name in _TEMPLATE_NAMES
        }
        for name, payload in payloads.items():
            expected_digest, expected_size = _TRUSTED_TEMPLATE_FACTS[name]
            if (
                len(payload) != expected_size
                or hashlib.sha256(payload).hexdigest() != expected_digest
            ):
                _fail("templates")
        after = os.lstat(root)
        if (
            sorted(os.listdir(root)) != list(_TEMPLATE_NAMES)
            or _template_root_fingerprint(after) != _template_root_fingerprint(before)
        ):
            _fail("templates")
        return payloads
    except DeployPlanError:
        raise
    except (OSError, TypeError, ValueError):
        _fail("templates")


def _digest_item(name: str, payload: bytes) -> dict[str, object]:
    return {
        "name": name,
        "sha256": hashlib.sha256(payload).hexdigest(),
        "size_bytes": len(payload),
    }


def _desired_files(
    *,
    node_id: str,
    commit_sha: str,
    binary_sha256: str,
    binary_size_bytes: int,
    manifest_sha256: str,
    manifest_size_bytes: int,
    templates: dict[str, bytes],
) -> list[dict[str, object]]:
    entries = [
        {
            "destination": f"/opt/maestro/ha/releases/{commit_sha}/maestro-panel",
            "group": "root",
            "mode": "0755",
            "owner": "root",
            "sha256": binary_sha256,
            "size_bytes": binary_size_bytes,
            "source": "artifact/maestro-panel",
        },
        {
            "destination": f"/opt/maestro/ha/releases/{commit_sha}/manifest.json",
            "group": "root",
            "mode": "0644",
            "owner": "root",
            "sha256": manifest_sha256,
            "size_bytes": manifest_size_bytes,
            "source": "artifact/manifest.json",
        },
    ]
    selections = (
        (
            "maestro-panel.env.example",
            "/etc/maestro/ha/maestro-panel.env",
            "root",
            "maestro-panel",
            "0640",
        ),
        (
            "maestro-panel.service",
            "/etc/systemd/system/maestro-panel.service",
            "root",
            "root",
            "0644",
        ),
        (
            f"rqlite-{node_id}.env.example",
            f"/etc/maestro/ha/rqlite-{node_id}.env",
            "root",
            "maestro-rqlite",
            "0640",
        ),
        (
            "rqlited@.service",
            "/etc/systemd/system/rqlited@.service",
            "root",
            "root",
            "0644",
        ),
    )
    for source, destination, owner, group, mode in selections:
        payload = templates[source]
        entries.append(
            {
                "destination": destination,
                "group": group,
                "mode": mode,
                "owner": owner,
                "sha256": hashlib.sha256(payload).hexdigest(),
                "size_bytes": len(payload),
                "source": f"template/{source}",
            }
        )
    return sorted(entries, key=lambda item: item["destination"])


def create_plan(
    *,
    inventory_path: Path | str,
    transport_evidence_path: Path | str,
    artifact_root: Path | str,
    manifest_path: Path | str,
    pki_evidence_path: Path | str,
    templates_root: Path | str,
) -> dict[str, object]:
    inventory_raw = _read_regular_file(
        inventory_path,
        limit=_MAX_JSON_BYTES,
        code="inventory",
    )
    inventory = _validate_inventory(_strict_json(inventory_raw, code="inventory"))
    transport_raw = _read_regular_file(
        transport_evidence_path,
        limit=_MAX_JSON_BYTES,
        code="transport",
    )
    transport = _validate_transport(_strict_json(transport_raw, code="transport"))
    if inventory["artifact"] != transport:
        _fail("transport")

    try:
        first_artifact = build_manifest.verify_manifest(
            artifact_root,
            manifest_path,
            expected_repository=transport["repository"],
            expected_ref=transport["ref"],
            expected_commit_sha=transport["commit_sha"],
            expected_workflow_run_id=transport["workflow_run_id"],
            expected_workflow_run_attempt=transport["workflow_run_attempt"],
        )
    except build_manifest.ManifestError:
        _fail("artifact")
    manifest_raw = _read_regular_file(
        manifest_path,
        limit=_MAX_MANIFEST_BYTES,
        code="artifact",
    )

    pki_raw = _read_regular_file(
        pki_evidence_path,
        limit=_MAX_JSON_BYTES,
        code="pki",
    )
    evidence, required_roles = _validate_pki_evidence(
        pki_raw,
        node_id=inventory["node_id"],
    )
    templates = _read_templates(templates_root)

    try:
        final_artifact = build_manifest.verify_manifest(
            artifact_root,
            manifest_path,
            expected_repository=transport["repository"],
            expected_ref=transport["ref"],
            expected_commit_sha=transport["commit_sha"],
            expected_workflow_run_id=transport["workflow_run_id"],
            expected_workflow_run_attempt=transport["workflow_run_attempt"],
        )
    except build_manifest.ManifestError:
        _fail("artifact")
    final_manifest_raw = _read_regular_file(
        manifest_path,
        limit=_MAX_MANIFEST_BYTES,
        code="artifact",
    )
    if first_artifact != final_artifact or manifest_raw != final_manifest_raw:
        _fail("artifact")

    binary_sha256 = final_artifact["artifact_sha256"]
    binary_size_bytes = final_artifact["artifact_size_bytes"]
    manifest_sha256 = hashlib.sha256(manifest_raw).hexdigest()
    manifest_size_bytes = len(manifest_raw)
    commit_sha = transport["commit_sha"]
    return {
        "artifact": {
            "archive_sha256": transport["archive_sha256"],
            "artifact_id": transport["artifact_id"],
            "artifact_name": transport["artifact_name"],
            "binary_sha256": binary_sha256,
            "binary_size_bytes": binary_size_bytes,
            "head_commit_sha": commit_sha,
            "manifest_sha256": manifest_sha256,
            "manifest_size_bytes": manifest_size_bytes,
            "ref": transport["ref"],
            "repository": transport["repository"],
            "workflow_run_attempt": transport["workflow_run_attempt"],
            "workflow_run_id": transport["workflow_run_id"],
        },
        "authorized": False,
        "blockers": list(_FIXED_BLOCKERS),
        "files": _desired_files(
            node_id=inventory["node_id"],
            commit_sha=commit_sha,
            binary_sha256=binary_sha256,
            binary_size_bytes=binary_size_bytes,
            manifest_sha256=manifest_sha256,
            manifest_size_bytes=manifest_size_bytes,
            templates=templates,
        ),
        "node_id": inventory["node_id"],
        "node_inventory_sha256": hashlib.sha256(inventory_raw).hexdigest(),
        "pki": {
            "evaluation_time": evidence["evaluation_time"],
            "evidence_sha256": hashlib.sha256(pki_raw).hexdigest(),
            "openssl_version": evidence["openssl_version"],
            "profile_sha256": evidence["profile_sha256"],
            "required_roles": required_roles,
        },
        "release_readiness": "NO_GO",
        "schema": _PLAN_SCHEMA,
        "template_digests": [
            _digest_item(name, templates[name]) for name in _TEMPLATE_NAMES
        ],
        "template_source_commit_sha": _TEMPLATE_SOURCE_COMMIT_SHA,
    }


class _RedactedArgumentParser(argparse.ArgumentParser):
    def error(self, _message: str) -> None:
        _fail("input")


def _parser() -> _RedactedArgumentParser:
    parser = _RedactedArgumentParser(add_help=False, allow_abbrev=False)
    parser.add_argument("command", choices=("plan",))
    parser.add_argument("--inventory", required=True)
    parser.add_argument("--transport-evidence", required=True)
    parser.add_argument("--artifact-root", required=True)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--pki-evidence", required=True)
    parser.add_argument("--templates-root", required=True)
    return parser


def main(
    argv: Sequence[str] | None = None,
    *,
    stdout: TextIO | None = None,
    stderr: TextIO | None = None,
) -> int:
    output = sys.stdout if stdout is None else stdout
    errors = sys.stderr if stderr is None else stderr
    arguments = list(sys.argv[1:] if argv is None else argv)
    if not arguments or arguments[0] != "plan":
        errors.write(_ERRORS["input"] + "\n")
        return 2
    try:
        parsed = _parser().parse_args(arguments)
        plan = create_plan(
            inventory_path=parsed.inventory,
            transport_evidence_path=parsed.transport_evidence,
            artifact_root=parsed.artifact_root,
            manifest_path=parsed.manifest,
            pki_evidence_path=parsed.pki_evidence,
            templates_root=parsed.templates_root,
        )
    except DeployPlanError as error:
        errors.write(str(error) + "\n")
        return 2
    output.write(canonical_bytes(plan).decode("ascii"))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
