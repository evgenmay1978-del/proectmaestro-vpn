"""Fail-closed canonical S4 network-change package boundary."""

import argparse
import hashlib
import json
import os
import re
import secrets
import stat
from collections.abc import Mapping
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Sequence, TextIO


INVENTORY_SCHEMA = "maestro-ha-s4-network-inventory-v1"
OUTPUT_SCHEMA = "maestro-ha-s4-network-change-package-v1"
CHANGE_SCOPE = "REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY"

INVENTORY_KEYS = {
    "schema",
    "captured_at_utc",
    "expires_at_utc",
    "node_id",
    "evidence_class",
    "source_review_completed",
    "networkd",
    "ifupdown",
    "health",
    "console",
}
NETWORKD_KEYS = {
    "active",
    "enabled",
    "owns_primary_interface",
    "owns_default_route",
}
IFUPDOWN_KEYS = {
    "enabled",
    "declares_primary_interface",
    "declares_default_route",
    "ifup_unit_failed",
    "networking_unit_failed",
}
HEALTH_KEYS = {
    "management_reachable",
    "vpn_units_healthy",
    "expected_vpn_listeners_present",
    "default_route_present",
}
CONSOLE_KEYS = {
    "independent_access_confirmed",
    "recovery_procedure_reviewed",
    "second_operator_available",
}

BLOCKER_ORDER = (
    "source_review_incomplete",
    "networkd_inactive",
    "networkd_disabled",
    "networkd_not_primary_owner",
    "networkd_not_default_route_owner",
    "ifupdown_disabled",
    "ifupdown_primary_declaration_absent",
    "ifupdown_default_route_declaration_absent",
    "ifup_unit_state_drift",
    "networking_unit_state_drift",
    "management_unreachable",
    "vpn_units_unhealthy",
    "vpn_listeners_missing",
    "default_route_missing",
    "console_access_unconfirmed",
    "recovery_procedure_unreviewed",
    "second_operator_unavailable",
)
PRECHECK_IDS = (
    "inventory_reviewed",
    "networkd_working_owner",
    "ifupdown_conflict_confirmed",
    "management_vpn_health_green",
    "console_recovery_ready",
)
CHANGE_STEP_IDS = (
    "backup_ifupdown_state",
    "remove_ifupdown_primary_declaration",
    "disable_ifupdown_boot_ownership",
    "preserve_systemd_networkd",
)
STOP_GATE_IDS = (
    "trusted_utc_expired",
    "console_unavailable",
    "inventory_drift",
    "unexpected_network_owner",
    "prechange_health_degraded",
    "unexpected_command_result",
    "route_or_listener_loss",
    "fresh_management_session_failed",
)
VALIDATION_IDS = (
    "single_primary_network_owner",
    "networkd_active_enabled",
    "default_route_preserved",
    "fresh_management_session_established",
    "vpn_units_listeners_preserved",
    "no_new_failed_units",
)
ROLLBACK_IDS = (
    "restore_ifupdown_primary_declaration",
    "restore_ifupdown_unit_state",
    "repeat_s4_health_validation",
)
BLOCKER_RULES = (
    ("source_review_incomplete", ("source_review_completed",), False),
    ("networkd_inactive", ("networkd", "active"), False),
    ("networkd_disabled", ("networkd", "enabled"), False),
    ("networkd_not_primary_owner", ("networkd", "owns_primary_interface"), False),
    ("networkd_not_default_route_owner", ("networkd", "owns_default_route"), False),
    ("ifupdown_disabled", ("ifupdown", "enabled"), False),
    ("ifupdown_primary_declaration_absent", ("ifupdown", "declares_primary_interface"), False),
    ("ifupdown_default_route_declaration_absent", ("ifupdown", "declares_default_route"), False),
    ("ifup_unit_state_drift", ("ifupdown", "ifup_unit_failed"), False),
    ("networking_unit_state_drift", ("ifupdown", "networking_unit_failed"), False),
    ("management_unreachable", ("health", "management_reachable"), False),
    ("vpn_units_unhealthy", ("health", "vpn_units_healthy"), False),
    ("vpn_listeners_missing", ("health", "expected_vpn_listeners_present"), False),
    ("default_route_missing", ("health", "default_route_present"), False),
    ("console_access_unconfirmed", ("console", "independent_access_confirmed"), False),
    ("recovery_procedure_unreviewed", ("console", "recovery_procedure_reviewed"), False),
    ("second_operator_unavailable", ("console", "second_operator_available"), False),
)
_UTC_SECONDS = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
MAX_INVENTORY_BYTES = 16_384


class S4ChangePackageError(ValueError):
    """An invalid S4 package input represented by a stable redacted code."""


def _fail(code: str) -> None:
    raise S4ChangePackageError(f"s4-network-change-package:{code}")


def canonical_bytes(value: object) -> bytes:
    return (
        json.dumps(
            value,
            ensure_ascii=True,
            allow_nan=False,
            sort_keys=True,
            separators=(",", ":"),
        ).encode("ascii")
        + b"\n"
    )


def _object_without_duplicates(pairs: list[tuple[str, object]]) -> dict[str, object]:
    value: dict[str, object] = {}
    for key, item in pairs:
        if key in value:
            _fail("inventory-duplicate-key")
        value[key] = item
    return value


def _reject_json_constant(_: str) -> None:
    _fail("inventory-json-constant")


def _reject_json_float(_: str) -> None:
    _fail("inventory-float")


def _require_exact_keys(value: object, keys: set[str]) -> dict[str, object]:
    if type(value) is not dict:
        _fail("inventory-object")
    if set(value) != keys:
        _fail("inventory-keys")
    return value


def _parse_utc_seconds(value: object) -> datetime:
    if type(value) is not str or _UTC_SECONDS.fullmatch(value) is None:
        _fail("inventory-timestamp")
    try:
        return datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(
            tzinfo=timezone.utc
        )
    except ValueError:
        _fail("inventory-timestamp")


def _require_evaluation_time(value: datetime) -> datetime:
    if type(value) is not datetime or value.tzinfo is None:
        _fail("evaluation-time")
    if value.utcoffset() != timedelta(0):
        _fail("evaluation-time")
    return value


def parse_inventory(raw: bytes, *, evaluation_time: datetime) -> dict[str, object]:
    """Parse one strict, canonical, fresh S4 inventory without side effects."""
    if type(raw) is not bytes:
        _fail("inventory-bytes")
    try:
        decoded = raw.decode("ascii")
        value = json.loads(
            decoded,
            object_pairs_hook=_object_without_duplicates,
            parse_constant=_reject_json_constant,
            parse_float=_reject_json_float,
        )
    except S4ChangePackageError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError):
        _fail("inventory-json")
    inventory = _require_exact_keys(value, INVENTORY_KEYS)
    if raw != canonical_bytes(inventory):
        _fail("inventory-canonical")
    for field in ("schema", "captured_at_utc", "expires_at_utc", "node_id", "evidence_class"):
        if type(inventory[field]) is not str:
            _fail("inventory-string")
    if inventory["schema"] != INVENTORY_SCHEMA:
        _fail("inventory-schema")
    if inventory["node_id"] != "s4":
        _fail("inventory-node")
    if inventory["evidence_class"] != "PRODUCTION_READ_ONLY":
        _fail("inventory-evidence-class")
    if type(inventory["source_review_completed"]) is not bool:
        _fail("inventory-boolean")
    for field, keys in (
        ("networkd", NETWORKD_KEYS),
        ("ifupdown", IFUPDOWN_KEYS),
        ("health", HEALTH_KEYS),
        ("console", CONSOLE_KEYS),
    ):
        nested = _require_exact_keys(inventory[field], keys)
        if any(type(nested[key]) is not bool for key in keys):
            _fail("inventory-boolean")
    captured_at = _parse_utc_seconds(inventory["captured_at_utc"])
    expires_at = _parse_utc_seconds(inventory["expires_at_utc"])
    trusted_time = _require_evaluation_time(evaluation_time)
    if expires_at - captured_at > timedelta(minutes=15):
        _fail("inventory-freshness-window")
    if not captured_at <= trusted_time < expires_at:
        _fail("inventory-freshness")
    return inventory


def _nested_bool(inventory: Mapping[str, object], path: tuple[str, ...]) -> bool:
    value: object = inventory
    for key in path:
        if not isinstance(value, Mapping) or key not in value:
            _fail("inventory-shape")
        value = value[key]
    if type(value) is not bool:
        _fail("inventory-boolean")
    return value


def evaluate_inventory(
    inventory: Mapping[str, object],
    *,
    inventory_sha256: str,
) -> dict[str, object]:
    """Create the deterministic, non-mutating S4 evidence package."""
    if type(inventory_sha256) is not str or re.fullmatch(r"[0-9a-f]{64}", inventory_sha256) is None:
        _fail("inventory-digest")
    blockers = [
        code
        for code, path, blocked_value in BLOCKER_RULES
        if _nested_bool(inventory, path) is blocked_value
    ]
    return {
        "apply_supported": False,
        "blockers": blockers,
        "change_scope": CHANGE_SCOPE,
        "change_step_ids": list(CHANGE_STEP_IDS),
        "conflicting_manager": "ifupdown",
        "inventory_captured_at_utc": inventory["captured_at_utc"],
        "inventory_expires_at_utc": inventory["expires_at_utc"],
        "inventory_sha256": inventory_sha256,
        "mutation_authorized": False,
        "precheck_ids": list(PRECHECK_IDS),
        "rollback_ids": list(ROLLBACK_IDS),
        "schema": OUTPUT_SCHEMA,
        "selected_owner": "systemd-networkd",
        "status": "BLOCKED" if blockers else "EVIDENCE_COMPLETE",
        "stop_gate_ids": list(STOP_GATE_IDS),
        "validation_ids": list(VALIDATION_IDS),
    }


_Fingerprint = tuple[int, int, int, int, int, int, int, int]


def _fingerprint(info: os.stat_result) -> _Fingerprint:
    return (
        int(info.st_dev),
        int(info.st_ino),
        int(info.st_mode),
        int(info.st_uid),
        int(info.st_nlink),
        int(info.st_size),
        int(getattr(info, "st_mtime_ns", int(info.st_mtime * 1_000_000_000))),
        int(getattr(info, "st_ctime_ns", int(info.st_ctime * 1_000_000_000))),
    )


def _identity(info: os.stat_result) -> tuple[int, int, int, int]:
    return (int(info.st_dev), int(info.st_ino), int(info.st_mode), int(info.st_uid))


def _require_posix() -> None:
    if os.name != "posix":
        _fail("unsupported-platform")


def _require_private_regular(info: os.stat_result, *, code: str, maximum: int) -> None:
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_nlink != 1
        or info.st_uid != os.geteuid()
        or stat.S_IMODE(info.st_mode) != 0o600
        or info.st_size < 1
        or info.st_size > maximum
    ):
        _fail(code)


def _read_inventory_bytes(path: Path | str) -> bytes:
    _require_posix()
    descriptor: int | None = None
    try:
        source = Path(path)
        before = os.lstat(source)
        _require_private_regular(before, code="inventory", maximum=MAX_INVENTORY_BYTES)
        flags = os.O_RDONLY
        for option in ("O_NOFOLLOW", "O_CLOEXEC"):
            flags |= int(getattr(os, option, 0))
        descriptor = os.open(source, flags)
        opened = os.fstat(descriptor)
        _require_private_regular(opened, code="inventory", maximum=MAX_INVENTORY_BYTES)
        if _fingerprint(opened) != _fingerprint(before):
            _fail("inventory")
        chunks: list[bytes] = []
        remaining = MAX_INVENTORY_BYTES + 1
        while remaining:
            chunk = os.read(descriptor, min(65_536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        raw = b"".join(chunks)
        after_descriptor = os.fstat(descriptor)
        after_path = os.lstat(source)
        if (
            len(raw) > MAX_INVENTORY_BYTES
            or len(raw) != before.st_size
            or _fingerprint(after_descriptor) != _fingerprint(before)
            or _fingerprint(after_path) != _fingerprint(before)
        ):
            _fail("inventory")
        return raw
    except S4ChangePackageError:
        raise
    except (OSError, TypeError, ValueError):
        _fail("inventory")
    finally:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError:
                pass


def read_inventory(
    path: Path | str,
    *,
    evaluation_time: datetime,
) -> tuple[dict[str, object], str]:
    """Read a descriptor-pinned canonical inventory and bind its raw digest."""
    raw = _read_inventory_bytes(path)
    try:
        inventory = parse_inventory(raw, evaluation_time=evaluation_time)
    except S4ChangePackageError:
        raise
    return inventory, hashlib.sha256(raw).hexdigest()


class _PinnedOutputDirectory:
    def __init__(self, path: Path, descriptor: int, metadata: os.stat_result) -> None:
        self.path = path
        self.descriptor = descriptor
        self.metadata = metadata

    def close(self) -> None:
        os.close(self.descriptor)


def _require_private_directory(info: os.stat_result) -> None:
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.geteuid()
        or stat.S_IMODE(info.st_mode) != 0o700
    ):
        _fail("output")


def _open_output_directory(path: Path) -> _PinnedOutputDirectory:
    descriptor: int | None = None
    try:
        before = os.lstat(path)
        if stat.S_ISLNK(before.st_mode):
            _fail("output")
        _require_private_directory(before)
        flags = os.O_RDONLY
        for option in ("O_DIRECTORY", "O_NOFOLLOW", "O_CLOEXEC"):
            flags |= int(getattr(os, option, 0))
        descriptor = os.open(path, flags)
        opened = os.fstat(descriptor)
        _require_private_directory(opened)
        if _fingerprint(opened) != _fingerprint(before):
            _fail("output")
        return _PinnedOutputDirectory(path, descriptor, opened)
    except S4ChangePackageError:
        if descriptor is not None:
            os.close(descriptor)
        raise
    except (OSError, TypeError, ValueError):
        if descriptor is not None:
            os.close(descriptor)
        _fail("output")


def _ensure_output_directory(root: _PinnedOutputDirectory) -> None:
    try:
        current = os.lstat(root.path)
        pinned = os.fstat(root.descriptor)
    except OSError:
        _fail("output")
    if stat.S_ISLNK(current.st_mode):
        _fail("output")
    _require_private_directory(current)
    if _identity(current) != _identity(root.metadata) or _identity(pinned) != _identity(root.metadata):
        _fail("output")


def _lstat_at(root: _PinnedOutputDirectory, name: str) -> os.stat_result | None:
    try:
        return os.stat(name, dir_fd=root.descriptor, follow_symlinks=False)
    except FileNotFoundError:
        return None
    except (OSError, TypeError):
        _fail("output")


def _unlink_at(root: _PinnedOutputDirectory, name: str) -> None:
    os.unlink(name, dir_fd=root.descriptor)


def _safe_unlink_at(root: _PinnedOutputDirectory, name: str, owned: _Fingerprint | None) -> bool:
    if owned is None:
        return False
    try:
        current = _lstat_at(root, name)
        if current is None or _fingerprint(current) != owned:
            return False
        _ensure_output_directory(root)
        current = _lstat_at(root, name)
        if current is None or _fingerprint(current) != owned:
            return False
        _unlink_at(root, name)
        return True
    except (OSError, S4ChangePackageError):
        return False


def _safe_unlink_temporary(
    root: _PinnedOutputDirectory, name: str, owned: tuple[int, int, int] | None
) -> bool:
    if owned is None:
        return False
    try:
        current = _lstat_at(root, name)
        if current is None or (current.st_dev, current.st_ino, stat.S_IFMT(current.st_mode)) != owned:
            return False
        _ensure_output_directory(root)
        current = _lstat_at(root, name)
        if current is None or (current.st_dev, current.st_ino, stat.S_IFMT(current.st_mode)) != owned:
            return False
        _unlink_at(root, name)
        return True
    except (OSError, S4ChangePackageError):
        return False


def _output_temp_flags() -> int:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    for option in ("O_NOFOLLOW", "O_CLOEXEC"):
        flags |= int(getattr(os, option, 0))
    return flags


def publish_change_package(output: Path | str, encoded: bytes) -> None:
    """Publish one private canonical package without overwriting another inode."""
    _require_posix()
    if type(encoded) is not bytes or not encoded:
        _fail("output")
    try:
        target = Path(output)
        if target.name in ("", ".", "..") or target.parent == target:
            _fail("output")
    except (TypeError, ValueError):
        _fail("output")
    root = _open_output_directory(target.parent)
    temporary_name = ".s4-network-change-" + secrets.token_hex(12) + ".tmp"
    descriptor: int | None = None
    temporary: tuple[int, int, int] | None = None
    published: _Fingerprint | None = None
    try:
        _ensure_output_directory(root)
        if _lstat_at(root, target.name) is not None:
            _fail("exists")
        descriptor = os.open(temporary_name, _output_temp_flags(), 0o600, dir_fd=root.descriptor)
        os.fchmod(descriptor, 0o600)
        opened_temporary = os.fstat(descriptor)
        temporary = (
            opened_temporary.st_dev,
            opened_temporary.st_ino,
            stat.S_IFMT(opened_temporary.st_mode),
        )
        view = memoryview(encoded)
        written = 0
        while written < len(view):
            count = os.write(descriptor, view[written:])
            if count <= 0:
                _fail("output")
            written += count
        os.fsync(descriptor)
        temporary_info = os.fstat(descriptor)
        _require_private_regular(temporary_info, code="output", maximum=len(encoded))
        if temporary_info.st_size != len(encoded):
            _fail("output")
        os.link(
            temporary_name,
            target.name,
            src_dir_fd=root.descriptor,
            dst_dir_fd=root.descriptor,
            follow_symlinks=False,
        )
        linked = os.fstat(descriptor)
        published = _fingerprint(linked)
        final_path = _lstat_at(root, target.name)
        if (
            final_path is None
            or linked.st_nlink != 2
            or _fingerprint(linked) != _fingerprint(final_path)
            or linked.st_size != len(encoded)
        ):
            _fail("output")
        current_temporary = _lstat_at(root, temporary_name)
        if current_temporary is None or _fingerprint(current_temporary) != _fingerprint(linked):
            _fail("output")
        _unlink_at(root, temporary_name)
        temporary = None
        final = os.fstat(descriptor)
        final_path = _lstat_at(root, target.name)
        _require_private_regular(final, code="output", maximum=len(encoded))
        if (
            final.st_size != len(encoded)
            or final_path is None
            or _fingerprint(final) != _fingerprint(final_path)
        ):
            _fail("output")
        published = _fingerprint(final)
        _ensure_output_directory(root)
        os.fsync(root.descriptor)
    except S4ChangePackageError:
        candidate = published
        if candidate is None and descriptor is not None:
            try:
                candidate = _fingerprint(os.fstat(descriptor))
            except OSError:
                pass
        _safe_unlink_at(root, target.name, candidate)
        raise
    except (OSError, TypeError, ValueError):
        candidate = published
        if candidate is None and descriptor is not None:
            try:
                candidate = _fingerprint(os.fstat(descriptor))
            except OSError:
                pass
        _safe_unlink_at(root, target.name, candidate)
        _fail("output")
    finally:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError:
                pass
        _safe_unlink_temporary(root, temporary_name, temporary)
        try:
            os.fsync(root.descriptor)
        except OSError:
            pass
        try:
            root.close()
        except OSError:
            pass


class _RedactedArgumentParser(argparse.ArgumentParser):
    def error(self, _message: str) -> None:
        _fail("input")

    def exit(self, _status: int = 0, _message: str | None = None) -> None:
        _fail("input")


def _write_help(stdout: TextIO) -> None:
    stdout.write("usage: s4-network-change-package.py package --inventory PATH --evaluation-time UTC --output PATH\n")


def _preflight(argv: Sequence[str]) -> tuple[str, str, str] | None:
    if list(argv) in (["--help"], ["-h"]):
        return None
    if len(argv) != 7 or argv[0] != "package":
        _fail("input")
    options = argv[1::2]
    values = argv[2::2]
    expected = {"--inventory", "--evaluation-time", "--output"}
    if set(options) != expected or len(set(options)) != 3:
        _fail("input")
    if any(not value or value.startswith("-") for value in values):
        _fail("input")
    pairs = dict(zip(options, values, strict=True))
    return pairs["--inventory"], pairs["--evaluation-time"], pairs["--output"]


def _parse_cli_utc(value: str) -> datetime:
    try:
        return _parse_utc_seconds(value)
    except S4ChangePackageError:
        _fail("input")


def run(argv: Sequence[str] | None, stdout: TextIO, stderr: TextIO) -> int:
    """Run the exact, redacted offline package command without stdout data."""
    arguments = list(argv) if argv is not None else []
    try:
        preflight = _preflight(arguments)
        if preflight is None:
            _write_help(stdout)
            return 0
        inventory_path, evaluation_time_text, output_path = preflight
        _require_posix()
        parser = _RedactedArgumentParser(add_help=False)
        parser.add_argument("command")
        parser.add_argument("--inventory", required=True)
        parser.add_argument("--evaluation-time", required=True)
        parser.add_argument("--output", required=True)
        parsed = parser.parse_args(
            ["package", "--inventory", inventory_path, "--evaluation-time", evaluation_time_text, "--output", output_path]
        )
        evaluation_time = _parse_cli_utc(parsed.evaluation_time)
        inventory, digest = read_inventory(parsed.inventory, evaluation_time=evaluation_time)
        package = evaluate_inventory(inventory, inventory_sha256=digest)
        publish_change_package(parsed.output, canonical_bytes(package))
        return 0 if package["status"] == "EVIDENCE_COMPLETE" else 2
    except S4ChangePackageError as error:
        stderr.write(str(error) + "\n")
        return 3
    except Exception:
        stderr.write("s4-network-change-package:output\n")
        return 3


def main(argv: Sequence[str] | None = None) -> int:
    import sys

    return run(sys.argv[1:] if argv is None else argv, sys.stdout, sys.stderr)
