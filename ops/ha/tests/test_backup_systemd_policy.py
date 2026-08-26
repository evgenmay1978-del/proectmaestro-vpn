from __future__ import annotations

import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
POLICY = ROOT / "ops" / "ha" / "test-backup-systemd-policy.py"
LEGACY_SCRIPT = ROOT / "deploy" / "maestro-backup.sh"
HA_SERVICE = ROOT / "deploy" / "ha" / "maestro-backup.service"
HA_TIMER = ROOT / "deploy" / "ha" / "maestro-backup.timer"
LEGACY_UNITS = (
    ROOT / "deploy" / "maestro-backup.service",
    ROOT / "deploy" / "maestro-backup.timer",
    ROOT / "deploy" / "maestro-backup-onchange.path",
)
HA_UNIT_NAMES = frozenset({"maestro-ha-backup.service", "maestro-ha-backup.timer"})
LEGACY_UNIT_NAMES = frozenset(path.name for path in LEGACY_UNITS)


def parse_unit(path: Path) -> dict[str, dict[str, list[str]]]:
    sections: dict[str, dict[str, list[str]]] = {}
    section = ""
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith(("#", ";")):
            continue
        if line.startswith("[") and line.endswith("]"):
            section = line[1:-1]
            sections.setdefault(section, {})
            continue
        key, separator, value = line.partition("=")
        if not separator or not section:
            raise AssertionError(f"invalid unit line in {path.name}: {line}")
        sections[section].setdefault(key, []).append(value)
    return sections


def tokens(unit: dict[str, dict[str, list[str]]], section: str, key: str) -> set[str]:
    return {
        token
        for value in unit.get(section, {}).get(key, [])
        for token in value.split()
    }


def single(unit: dict[str, dict[str, list[str]]], section: str, key: str) -> str:
    values = unit.get(section, {}).get(key, [])
    if len(values) != 1:
        raise AssertionError(f"{section}.{key} must occur exactly once: {values}")
    return values[0]


def load_policy():
    if not POLICY.is_file():
        raise AssertionError("backup systemd policy script is absent")
    spec = importlib.util.spec_from_file_location("backup_systemd_policy", POLICY)
    if spec is None or spec.loader is None:
        raise AssertionError("backup systemd policy script cannot be loaded")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class BackupSystemdPolicyTests(unittest.TestCase):
    def test_ha_templates_exist(self):
        self.assertTrue(HA_SERVICE.is_file(), HA_SERVICE)
        self.assertTrue(HA_TIMER.is_file(), HA_TIMER)

    def test_bidirectional_conflicts_and_ordering_cover_every_pair(self):
        for path in (HA_SERVICE, HA_TIMER):
            with self.subTest(path=path):
                unit = parse_unit(path)
                self.assertTrue(LEGACY_UNIT_NAMES <= tokens(unit, "Unit", "Conflicts"))
                self.assertTrue(LEGACY_UNIT_NAMES <= tokens(unit, "Unit", "After"))

        for path in LEGACY_UNITS:
            with self.subTest(path=path):
                unit = parse_unit(path)
                self.assertTrue(HA_UNIT_NAMES <= tokens(unit, "Unit", "Conflicts"))
                self.assertTrue(HA_UNIT_NAMES <= tokens(unit, "Unit", "Before"))

    def test_ha_service_has_fixed_paths_and_strict_sandbox(self):
        unit = parse_unit(HA_SERVICE)
        expected = {
            "Type": "oneshot",
            "User": "maestro-backup",
            "Group": "maestro-backup",
            "UMask": "0077",
            "RuntimeDirectory": "maestro-backup",
            "RuntimeDirectoryMode": "0700",
            "StateDirectory": "maestro-backup",
            "StateDirectoryMode": "0700",
            "WorkingDirectory": "/var/lib/maestro-backup",
            "ExecStart": (
                "/usr/local/bin/maestro-backup-worker --config "
                "/etc/maestro/backup-worker.json"
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
            "SystemCallArchitectures": "native",
            "RestrictAddressFamilies": "AF_UNIX AF_INET AF_INET6",
            "ReadWritePaths": "/var/lib/maestro-backup",
            "ReadOnlyPaths": "/etc/maestro/backup-worker.json",
            "KeyringMode": "private",
            "TasksMax": "64",
            "LimitNOFILE": "1024",
        }
        for key, value in expected.items():
            with self.subTest(key=key):
                self.assertEqual(single(unit, "Service", key), value)
        self.assertEqual(single(unit, "Service", "CapabilityBoundingSet"), "")
        self.assertEqual(single(unit, "Service", "AmbientCapabilities"), "")
        self.assertEqual(
            single(unit, "Unit", "ConditionPathExists"),
            "/etc/maestro/backup-worker.json",
        )
        self.assertNotIn("Install", unit)

    def test_ha_service_persists_resume_bundle_across_oneshot_runs(self):
        unit = parse_unit(HA_SERVICE)
        self.assertEqual(
            single(unit, "Service", "StateDirectory"),
            "maestro-backup",
        )
        self.assertEqual(single(unit, "Service", "StateDirectoryMode"), "0700")
        self.assertEqual(
            single(unit, "Service", "WorkingDirectory"),
            "/var/lib/maestro-backup",
        )
        self.assertEqual(single(unit, "Service", "ReadWritePaths"), "/var/lib/maestro-backup")

    def test_ha_timer_is_bounded_and_only_explicitly_enableable(self):
        unit = parse_unit(HA_TIMER)
        self.assertEqual(single(unit, "Timer", "Unit"), "maestro-ha-backup.service")
        self.assertEqual(single(unit, "Timer", "OnBootSec"), "5min")
        self.assertEqual(single(unit, "Timer", "OnUnitActiveSec"), "5min")
        self.assertEqual(single(unit, "Timer", "AccuracySec"), "30s")
        self.assertEqual(single(unit, "Timer", "RandomizedDelaySec"), "30s")
        self.assertEqual(single(unit, "Timer", "Persistent"), "true")
        self.assertEqual(single(unit, "Install", "WantedBy"), "timers.target")
        self.assertNotIn("Also", unit.get("Install", {}))

    def test_legacy_service_requires_explicit_shared_mode_file(self):
        unit = parse_unit(LEGACY_UNITS[0])
        self.assertEqual(
            single(unit, "Unit", "ConditionPathExists"),
            "/etc/maestro/control-plane-mode.env",
        )
        self.assertEqual(
            single(unit, "Service", "EnvironmentFile"),
            "/etc/maestro/control-plane-mode.env",
        )

    def test_legacy_guard_is_first_and_precedes_every_side_effect(self):
        executable = [
            line.strip()
            for line in LEGACY_SCRIPT.read_text(encoding="utf-8").splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        ]
        expected_prefix = [
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
        self.assertEqual(executable[: len(expected_prefix)], expected_prefix)
        remainder = "\n".join(executable[len(expected_prefix) :])
        for marker in (
            "AWS=",
            "BUCKET=",
            "mktemp",
            "/var/lib/maestro",
            "/etc/x-ui",
            "ssh ",
            "tar ",
            "gpg ",
            " s3 ",
        ):
            with self.subTest(marker=marker):
                self.assertIn(marker, remainder)

    def run_legacy(self, mode: str | None) -> tuple[subprocess.CompletedProcess[str], list[str]]:
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("bash is unavailable")
        try:
            probe = subprocess.run(
                [bash, "-c", "printf maestro-bash-ok"],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=5,
                check=False,
            )
        except (OSError, subprocess.SubprocessError):
            self.skipTest("usable POSIX bash is unavailable")
        if probe.returncode != 0 or probe.stdout != "maestro-bash-ok":
            self.skipTest("usable POSIX bash is unavailable")
        with tempfile.TemporaryDirectory() as temporary:
            temporary_root = Path(temporary)
            command_root = temporary_root / "bin"
            work_root = temporary_root / "work"
            command_root.mkdir()
            work_root.mkdir()
            for command in (
                "mktemp",
                "date",
                "hostname",
                "ssh",
                "sqlite3",
                "install",
                "stat",
                "tar",
                "gpg",
            ):
                stub = command_root / command
                stub.write_text("#!/bin/sh\nexit 99\n", encoding="ascii")
                stub.chmod(0o700)
            environment = os.environ.copy()
            environment["PATH"] = str(command_root) + os.pathsep + environment["PATH"]
            environment["TMPDIR"] = str(work_root)
            environment["HOME"] = str(work_root)
            if mode is None:
                environment.pop("MAESTRO_CONTROL_PLANE_MODE", None)
            else:
                environment["MAESTRO_CONTROL_PLANE_MODE"] = mode
            result = subprocess.run(
                [bash, str(LEGACY_SCRIPT)],
                cwd=work_root,
                env=environment,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
                check=False,
            )
            survivors = sorted(path.name for path in work_root.iterdir())
        return result, survivors

    def test_rqlite_mode_exits_zero_without_any_side_effect(self):
        result, survivors = self.run_legacy("rqlite")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "maestro-backup: disabled in rqlite mode\n")
        self.assertEqual(result.stderr, "")
        self.assertEqual(survivors, [])

    def test_missing_or_unknown_mode_fails_before_any_side_effect(self):
        for mode in (None, "dual", "RQLITE", " rqlite"):
            with self.subTest(mode=mode):
                result, survivors = self.run_legacy(mode)
                self.assertEqual(result.returncode, 64)
                self.assertEqual(result.stdout, "")
                self.assertEqual(
                    result.stderr,
                    "maestro-backup: invalid control-plane mode\n",
                )
                self.assertEqual(survivors, [])

    def test_cutover_gate_requires_all_legacy_units_stopped_disabled_and_masked(self):
        policy = load_policy()
        safe = {
            name: {"active": False, "enabled": False, "masked": True}
            for name in LEGACY_UNIT_NAMES
        }
        policy.require_safe_cutover(safe)
        for name in LEGACY_UNIT_NAMES:
            for field, unsafe in (("active", True), ("enabled", True), ("masked", False)):
                mutated = {unit: values.copy() for unit, values in safe.items()}
                mutated[name][field] = unsafe
                with self.subTest(name=name, field=field):
                    with self.assertRaisesRegex(
                        AssertionError,
                        "^backup-systemd-policy:unsafe-cutover$",
                    ):
                        policy.require_safe_cutover(mutated)

    def test_installation_mapping_is_explicit_and_cannot_overwrite_legacy_units(self):
        policy = load_policy()
        expected = {
            "deploy/ha/maestro-backup.service": (
                "/etc/systemd/system/maestro-ha-backup.service"
            ),
            "deploy/ha/maestro-backup.timer": (
                "/etc/systemd/system/maestro-ha-backup.timer"
            ),
        }
        self.assertEqual(policy.installation_targets(), expected)
        self.assertEqual(len(set(expected.values())), len(expected))
        self.assertEqual(
            {Path(target).name for target in expected.values()},
            HA_UNIT_NAMES,
        )
        self.assertTrue(
            LEGACY_UNIT_NAMES.isdisjoint(
                {Path(target).name for target in expected.values()}
            )
        )

    def test_cutover_evidence_requires_rqlite_and_exact_safe_unit_state(self):
        policy = load_policy()
        states = {
            name: {"active": False, "enabled": False, "masked": True}
            for name in LEGACY_UNIT_NAMES
        }
        safe = {
            "version": 1,
            "control_plane_mode": "rqlite",
            "ha_enable_requested": True,
            "legacy_units": states,
        }
        policy.validate_cutover_evidence(safe)
        mutations = (
            {**safe, "control_plane_mode": "legacy"},
            {**safe, "ha_enable_requested": False},
            {**safe, "unexpected": True},
            {key: value for key, value in safe.items() if key != "legacy_units"},
        )
        for evidence in mutations:
            with self.subTest(evidence=evidence):
                with self.assertRaisesRegex(
                    AssertionError,
                    "^backup-systemd-policy:unsafe-cutover$",
                ):
                    policy.validate_cutover_evidence(evidence)

    def test_cutover_preflight_cli_is_executable_and_fail_closed(self):
        states = {
            name: {"active": False, "enabled": False, "masked": True}
            for name in LEGACY_UNIT_NAMES
        }
        with tempfile.TemporaryDirectory() as temporary:
            evidence_path = Path(temporary) / "cutover.json"
            evidence_path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "control_plane_mode": "rqlite",
                        "ha_enable_requested": True,
                        "legacy_units": states,
                    }
                ),
                encoding="utf-8",
            )
            evidence_path.chmod(0o600)
            result = subprocess.run(
                [sys.executable, str(POLICY), "--cutover-evidence", str(evidence_path)],
                cwd=ROOT,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, "backup cutover evidence passed\n")
            self.assertEqual(result.stderr, "")

            states["maestro-backup.service"]["active"] = True
            evidence_path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "control_plane_mode": "rqlite",
                        "ha_enable_requested": True,
                        "legacy_units": states,
                    }
                ),
                encoding="utf-8",
            )
            evidence_path.chmod(0o600)
            rejected = subprocess.run(
                [sys.executable, str(POLICY), "--cutover-evidence", str(evidence_path)],
                cwd=ROOT,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
                check=False,
            )
            self.assertEqual(rejected.returncode, 1)
            self.assertEqual(rejected.stdout, "")
            self.assertEqual(rejected.stderr, "backup systemd policy failed: invalid cutover evidence\n")

    def test_cutover_reader_uses_one_pinned_no_follow_descriptor(self):
        source = POLICY.read_text(encoding="utf-8")
        self.assertIn("_open_cutover_evidence", source)
        self.assertIn("os.O_NOFOLLOW", source)
        self.assertIn("os.O_CLOEXEC", source)
        self.assertIn("os.fstat(", source)
        self.assertIn("os.read(", source)
        self.assertNotIn("path.read_bytes()", source)

    @unittest.skipUnless(os.name == "posix", "descriptor replacement proof is POSIX-only")
    def test_cutover_reader_pins_original_file_across_path_replacement(self):
        policy = load_policy()
        states = {
            name: {"active": False, "enabled": False, "masked": True}
            for name in LEGACY_UNIT_NAMES
        }
        safe = {
            "version": 1,
            "control_plane_mode": "rqlite",
            "ha_enable_requested": True,
            "legacy_units": states,
        }
        unsafe_states = {name: values.copy() for name, values in states.items()}
        unsafe_states["maestro-backup.service"]["active"] = True
        unsafe = {
            "version": 1,
            "control_plane_mode": "rqlite",
            "ha_enable_requested": True,
            "legacy_units": unsafe_states,
        }

        with tempfile.TemporaryDirectory() as temporary:
            evidence_path = Path(temporary) / "cutover.json"
            replacement_path = Path(temporary) / "replacement.json"
            evidence_path.write_text(json.dumps(safe), encoding="utf-8")
            replacement_path.write_text(json.dumps(unsafe), encoding="utf-8")
            evidence_path.chmod(0o600)
            replacement_path.chmod(0o600)
            original_open = os.open

            def open_then_replace(path, flags, mode=0o777):
                descriptor = original_open(path, flags, mode)
                os.replace(replacement_path, evidence_path)
                return descriptor

            with mock.patch.object(
                policy,
                "_open_cutover_evidence",
                new=open_then_replace,
            ):
                observed = policy.read_cutover_evidence(str(evidence_path))

            policy.validate_cutover_evidence(observed)
            with self.assertRaisesRegex(
                AssertionError,
                "^backup-systemd-policy:unsafe-cutover$",
            ):
                policy.validate_cutover_evidence(
                    json.loads(evidence_path.read_text(encoding="utf-8"))
                )

    def test_repository_policy_cli_passes_offline(self):
        result = subprocess.run(
            [sys.executable, str(POLICY)],
            cwd=ROOT,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=10,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "backup systemd policy passed\n")
        self.assertEqual(result.stderr, "")


if __name__ == "__main__":
    unittest.main()
