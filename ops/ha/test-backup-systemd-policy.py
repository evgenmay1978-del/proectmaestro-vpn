#!/usr/bin/env python3
"""Fail-closed offline policy for mutually exclusive backup unit templates."""

from __future__ import annotations

import json
import os
import stat
import sys
from pathlib import Path
from typing import Mapping


ROOT = Path(__file__).resolve().parents[2]
HA_SERVICE = Path("deploy/ha/maestro-backup.service")
HA_TIMER = Path("deploy/ha/maestro-backup.timer")
LEGACY_PATHS = (
    Path("deploy/maestro-backup.service"),
    Path("deploy/maestro-backup.timer"),
    Path("deploy/maestro-backup-onchange.path"),
)
LEGACY_NAMES = frozenset(path.name for path in LEGACY_PATHS)
HA_NAMES = frozenset({"maestro-ha-backup.service", "maestro-ha-backup.timer"})
MODE_FILE = "/etc/maestro/control-plane-mode.env"
CONFIG_FILE = "/etc/maestro/backup-worker.json"
STATE_DIR = "/var/lib/maestro-backup"
MAX_CUTOVER_EVIDENCE_BYTES = 16 << 10
FAILURE = "backup-systemd-policy:invalid"
UNSAFE_CUTOVER = "backup-systemd-policy:unsafe-cutover"


def fail(message: str = FAILURE) -> None:
    raise AssertionError(message)


def parse_unit(text: str) -> dict[str, dict[str, list[str]]]:
    sections: dict[str, dict[str, list[str]]] = {}
    section = ""
    for raw in text.splitlines():
        if raw.rstrip().endswith("\\"):
            fail()
        line = raw.strip()
        if not line or line.startswith(("#", ";")):
            continue
        if line.startswith("[") and line.endswith("]"):
            section = line[1:-1]
            if not section or section in sections:
                fail()
            sections[section] = {}
            continue
        key, separator, value = line.partition("=")
        if not separator or not section or not key or key.strip() != key:
            fail()
        sections[section].setdefault(key, []).append(value)
    return sections


def one(unit: Mapping[str, Mapping[str, list[str]]], section: str, key: str) -> str:
    values = unit.get(section, {}).get(key, [])
    if len(values) != 1:
        fail()
    return values[0]


def exact_tokens(
    unit: Mapping[str, Mapping[str, list[str]]],
    section: str,
    key: str,
    expected: frozenset[str],
) -> None:
    values = unit.get(section, {}).get(key, [])
    if len(values) != 1 or frozenset(values[0].split()) != expected:
        fail()


def read_regular(root: Path, relative: Path) -> str:
    path = root / relative
    if not path.is_file() or path.is_symlink():
        fail()
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeError):
        fail()
    raise AssertionError("unreachable")


def installation_targets() -> dict[str, str]:
    return {
        HA_SERVICE.as_posix(): (
            "/etc/systemd/system/maestro-ha-backup.service"
        ),
        HA_TIMER.as_posix(): "/etc/systemd/system/maestro-ha-backup.timer",
    }


def validate_ha_service(text: str) -> None:
    if not text.startswith(
        "# Repository template: install with the logical name maestro-ha-backup.service.\n"
    ):
        fail()
    unit = parse_unit(text)
    if frozenset(unit) != frozenset({"Unit", "Service"}):
        fail()
    exact_tokens(
        unit,
        "Unit",
        "After",
        LEGACY_NAMES | frozenset({"network-online.target"}),
    )
    exact_tokens(unit, "Unit", "Conflicts", LEGACY_NAMES)
    exact_tokens(unit, "Unit", "Wants", frozenset({"network-online.target"}))
    if one(unit, "Unit", "ConditionPathExists") != CONFIG_FILE:
        fail()
    if one(unit, "Unit", "StartLimitIntervalSec") != "1h":
        fail()
    if one(unit, "Unit", "StartLimitBurst") != "6":
        fail()

    expected = {
        "Type": "oneshot",
        "User": "maestro-backup",
        "Group": "maestro-backup",
        "UMask": "0077",
        "RuntimeDirectory": "maestro-backup",
        "RuntimeDirectoryMode": "0700",
        "StateDirectory": "maestro-backup",
        "StateDirectoryMode": "0700",
        "WorkingDirectory": STATE_DIR,
        "ExecStart": (
            "/usr/local/bin/maestro-backup-worker --config " + CONFIG_FILE
        ),
        "TimeoutStartSec": "15min",
        "TimeoutStopSec": "30s",
        "Restart": "no",
        "NoNewPrivileges": "yes",
        "PrivateTmp": "yes",
        "PrivateDevices": "yes",
        "ProtectSystem": "strict",
        "ProtectHome": "yes",
        "ProtectKernelTunables": "yes",
        "ProtectKernelModules": "yes",
        "ProtectKernelLogs": "yes",
        "ProtectControlGroups": "yes",
        "ProtectClock": "yes",
        "ProtectHostname": "yes",
        "ProtectProc": "invisible",
        "ProcSubset": "pid",
        "RestrictNamespaces": "yes",
        "RestrictRealtime": "yes",
        "RestrictSUIDSGID": "yes",
        "LockPersonality": "yes",
        "MemoryDenyWriteExecute": "yes",
        "RemoveIPC": "yes",
        "CapabilityBoundingSet": "",
        "AmbientCapabilities": "",
        "SystemCallArchitectures": "native",
        "RestrictAddressFamilies": "AF_UNIX AF_INET AF_INET6",
        "ReadWritePaths": STATE_DIR,
        "ReadOnlyPaths": CONFIG_FILE,
        "KeyringMode": "private",
        "TasksMax": "64",
        "LimitNOFILE": "1024",
    }
    service = unit["Service"]
    if frozenset(service) != frozenset(expected):
        fail()
    for key, value in expected.items():
        if one(unit, "Service", key) != value:
            fail()
    exec_start = expected["ExecStart"]
    if any(token in exec_start for token in ("$", "`", ";", "&&", "||")):
        fail()


def validate_ha_timer(text: str) -> None:
    if not text.startswith(
        "# Repository template: install with the logical name maestro-ha-backup.timer.\n"
    ):
        fail()
    unit = parse_unit(text)
    if frozenset(unit) != frozenset({"Unit", "Timer", "Install"}):
        fail()
    exact_tokens(unit, "Unit", "After", LEGACY_NAMES)
    exact_tokens(unit, "Unit", "Conflicts", LEGACY_NAMES)
    if one(unit, "Unit", "ConditionPathExists") != CONFIG_FILE:
        fail()
    expected = {
        "OnBootSec": "5min",
        "OnUnitActiveSec": "5min",
        "AccuracySec": "30s",
        "RandomizedDelaySec": "30s",
        "Persistent": "true",
        "Unit": "maestro-ha-backup.service",
    }
    if frozenset(unit["Timer"]) != frozenset(expected):
        fail()
    for key, value in expected.items():
        if one(unit, "Timer", key) != value:
            fail()
    if unit["Install"] != {"WantedBy": ["timers.target"]}:
        fail()


def validate_legacy_unit(text: str, name: str) -> None:
    unit = parse_unit(text)
    exact_tokens(unit, "Unit", "Before", HA_NAMES)
    exact_tokens(unit, "Unit", "Conflicts", HA_NAMES)
    if one(unit, "Unit", "ConditionPathExists") != MODE_FILE:
        fail()
    if name == "maestro-backup.service":
        if one(unit, "Service", "EnvironmentFile") != MODE_FILE:
            fail()
        if one(unit, "Service", "ExecStart") != "/usr/local/bin/maestro-backup.sh":
            fail()


def executable_lines(text: str) -> list[str]:
    return [
        line.strip()
        for line in text.splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    ]


def validate_legacy_script(text: str) -> None:
    prefix = [
        "set -euo pipefail",
        'case "${MAESTRO_CONTROL_PLANE_MODE:-}" in',
        "rqlite)",
        "printf '%s\\n' 'maestro-backup: disabled in rqlite mode'",
        "exit 0",
        ";;",
        "legacy)",
        ";;",
        "*)",
        "printf '%s\\n' 'maestro-backup: invalid control-plane mode' >&2",
        "exit 64",
        ";;",
        "esac",
    ]
    lines = executable_lines(text)
    if lines[: len(prefix)] != prefix:
        fail()
    remainder = "\n".join(lines[len(prefix) :])
    required_after_guard = (
        "AWS=",
        "BUCKET=",
        "mktemp",
        "/var/lib/maestro",
        "/etc/x-ui",
        "ssh ",
        "tar ",
        "gpg ",
        " s3 ",
    )
    if any(marker not in remainder for marker in required_after_guard):
        fail()
    if "set -x" in text or "MAESTRO_CONTROL_PLANE_MODE=" in remainder:
        fail()


def require_safe_cutover(states: Mapping[str, Mapping[str, object]]) -> None:
    if frozenset(states) != LEGACY_NAMES:
        raise AssertionError(UNSAFE_CUTOVER)
    for name in LEGACY_NAMES:
        state = states.get(name)
        if state is None or frozenset(state) != frozenset(
            {"active", "enabled", "masked"}
        ):
            raise AssertionError(UNSAFE_CUTOVER)
        if (
            state["active"] is not False
            or state["enabled"] is not False
            or state["masked"] is not True
        ):
            raise AssertionError(UNSAFE_CUTOVER)


def validate_cutover_evidence(evidence: Mapping[str, object]) -> None:
    if not isinstance(evidence, Mapping) or frozenset(evidence) != frozenset(
        {"version", "control_plane_mode", "ha_enable_requested", "legacy_units"}
    ):
        raise AssertionError(UNSAFE_CUTOVER)
    version = evidence["version"]
    if isinstance(version, bool) or version != 1:
        raise AssertionError(UNSAFE_CUTOVER)
    if evidence["control_plane_mode"] != "rqlite":
        raise AssertionError(UNSAFE_CUTOVER)
    if evidence["ha_enable_requested"] is not True:
        raise AssertionError(UNSAFE_CUTOVER)
    states = evidence["legacy_units"]
    if not isinstance(states, Mapping):
        raise AssertionError(UNSAFE_CUTOVER)
    require_safe_cutover(states)


def reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate key")
        result[key] = value
    return result


_open_cutover_evidence = os.open


def read_cutover_evidence(path_text: str) -> Mapping[str, object]:
    path = Path(path_text)
    descriptor: int | None = None
    try:
        if os.name == "posix":
            flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW
        else:
            if path.is_symlink():
                raise AssertionError(UNSAFE_CUTOVER)
            flags = os.O_RDONLY | getattr(os, "O_NOINHERIT", 0)

        descriptor = _open_cutover_evidence(path, flags)
        metadata = os.fstat(descriptor)
        if (
            not stat.S_ISREG(metadata.st_mode)
            or metadata.st_nlink != 1
            or metadata.st_size <= 0
            or metadata.st_size > MAX_CUTOVER_EVIDENCE_BYTES
        ):
            raise AssertionError(UNSAFE_CUTOVER)
        if os.name == "posix" and (
            metadata.st_uid != os.geteuid() or stat.S_IMODE(metadata.st_mode) != 0o600
        ):
            raise AssertionError(UNSAFE_CUTOVER)

        chunks: list[bytes] = []
        remaining = MAX_CUTOVER_EVIDENCE_BYTES + 1
        while remaining:
            chunk = os.read(descriptor, min(65536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        raw = b"".join(chunks)
        if len(raw) != metadata.st_size:
            raise AssertionError(UNSAFE_CUTOVER)
        decoded = raw.decode("utf-8")
        evidence = json.loads(decoded, object_pairs_hook=reject_duplicate_keys)
    except AssertionError:
        raise
    except (OSError, UnicodeError, ValueError, TypeError, json.JSONDecodeError):
        raise AssertionError(UNSAFE_CUTOVER) from None
    finally:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError:
                raise AssertionError(UNSAFE_CUTOVER) from None
    if not isinstance(evidence, Mapping):
        raise AssertionError(UNSAFE_CUTOVER)
    return evidence


def validate_repository(root: Path = ROOT) -> None:
    ha_service = read_regular(root, HA_SERVICE)
    ha_timer = read_regular(root, HA_TIMER)
    validate_ha_service(ha_service)
    validate_ha_timer(ha_timer)
    for relative in LEGACY_PATHS:
        validate_legacy_unit(read_regular(root, relative), relative.name)
    validate_legacy_script(read_regular(root, Path("deploy/maestro-backup.sh")))

    targets = installation_targets()
    if frozenset(targets) != frozenset(
        {HA_SERVICE.as_posix(), HA_TIMER.as_posix()}
    ):
        fail()
    target_paths = tuple(Path(value) for value in targets.values())
    if (
        len(frozenset(targets.values())) != len(targets)
        or frozenset(path.name for path in target_paths) != HA_NAMES
        or any(path.parent.as_posix() != "/etc/systemd/system" for path in target_paths)
        or not LEGACY_NAMES.isdisjoint(path.name for path in target_paths)
    ):
        fail()
    if any(Path(source).name == Path(target).name for source, target in targets.items()):
        fail()

    combined = "\n".join(
        [ha_service, ha_timer]
        + [read_regular(root, path) for path in LEGACY_PATHS]
    ).lower()
    for forbidden in (
        "system" + "ctl",
        "daemon-" + "reload",
        "/etc/systemd/" + "system",
        "preset-all",
    ):
        if forbidden in combined:
            fail()


def main(arguments: list[str] | None = None) -> int:
    args = sys.argv[1:] if arguments is None else arguments
    validate_repository()
    if not args:
        print("backup systemd policy passed")
        return 0
    if len(args) == 2 and args[0] == "--cutover-evidence":
        validate_cutover_evidence(read_cutover_evidence(args[1]))
        print("backup cutover evidence passed")
        return 0
    fail()
    raise AssertionError("unreachable")


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError:
        message = "invalid repository policy"
        if sys.argv[1:2] == ["--cutover-evidence"]:
            message = "invalid cutover evidence"
        print(f"backup systemd policy failed: {message}", file=sys.stderr)
        raise SystemExit(1)
