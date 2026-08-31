import ast
import copy
import hashlib
import ipaddress
import io
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import socket
from types import SimpleNamespace
import unittest
from datetime import datetime, timedelta, timezone
from unittest import mock

from ops.ha import s4_network_change_package
from ops.ha.s4_network_change_package import (
    BLOCKER_ORDER,
    BLOCKER_RULES,
    S4ChangePackageError,
    canonical_bytes,
    evaluate_inventory,
    parse_inventory,
)


CAPTURED_AT = "2026-08-31T11:50:00Z"
EXPIRES_AT = "2026-08-31T12:05:00Z"
EVALUATION_TIME = datetime(2026, 8, 31, 12, 0, 0, tzinfo=timezone.utc)


def valid_inventory() -> dict[str, object]:
    return {
        "schema": "maestro-ha-s4-network-inventory-v1",
        "captured_at_utc": CAPTURED_AT,
        "expires_at_utc": EXPIRES_AT,
        "node_id": "s4",
        "evidence_class": "PRODUCTION_READ_ONLY",
        "source_review_completed": True,
        "networkd": {
            "active": True,
            "enabled": True,
            "owns_primary_interface": True,
            "owns_default_route": True,
        },
        "ifupdown": {
            "enabled": True,
            "declares_primary_interface": True,
            "declares_default_route": True,
            "ifup_unit_failed": True,
            "networking_unit_failed": True,
        },
        "health": {
            "management_reachable": True,
            "vpn_units_healthy": True,
            "expected_vpn_listeners_present": True,
            "default_route_present": True,
        },
        "console": {
            "independent_access_confirmed": True,
            "recovery_procedure_reviewed": True,
            "second_operator_available": True,
        },
    }


def canonical(value: object) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=True,
        allow_nan=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("ascii") + b"\n"


class S4InventoryContractTests(unittest.TestCase):
    def assert_invalid(self, raw: bytes, code: str) -> None:
        with self.assertRaises(S4ChangePackageError) as raised:
            parse_inventory(raw, evaluation_time=EVALUATION_TIME)
        self.assertEqual(str(raised.exception), f"s4-network-change-package:{code}")
        self.assertNotIn("fixture-secret", str(raised.exception))

    def test_canonical_bytes_emits_hand_checked_ascii_json_with_one_terminal_lf(self) -> None:
        self.assertEqual(
            canonical_bytes({"z": True, "a": [1, "x"]}),
            b'{"a":[1,"x"],"z":true}\n',
        )

    def test_canonical_happy_path_preserves_the_audited_inventory(self) -> None:
        raw = canonical(valid_inventory())

        parsed = parse_inventory(raw, evaluation_time=EVALUATION_TIME)

        self.assertEqual(parsed, valid_inventory())
        self.assertEqual(raw, canonical(parsed))
        self.assertEqual(parsed["captured_at_utc"], CAPTURED_AT)
        self.assertEqual(parsed["expires_at_utc"], EXPIRES_AT)

    def test_noncanonical_whitespace_or_key_order_cannot_be_accepted_as_inventory(self) -> None:
        inventory = valid_inventory()
        reordered = json.dumps(inventory, separators=(",", ":")).encode("ascii") + b"\n"
        spaced = canonical(inventory).replace(b":", b": ", 1)

        self.assert_invalid(reordered, "inventory-canonical")
        self.assert_invalid(spaced, "inventory-canonical")

    def test_duplicate_top_level_key_cannot_change_audited_inventory_meaning(self) -> None:
        raw = canonical(valid_inventory()).rstrip(b"\n")
        duplicate = raw[:-1] + b',"schema":"fixture-secret-duplicate"}\n'

        self.assert_invalid(duplicate, "inventory-duplicate-key")

    def test_floats_and_json_constants_cannot_enter_a_boolean_inventory(self) -> None:
        float_inventory = valid_inventory()
        float_inventory["networkd"] = dict(float_inventory["networkd"], active=1.0)
        self.assert_invalid(canonical(float_inventory), "inventory-float")
        self.assert_invalid(
            canonical(valid_inventory()).replace(b"true", b"NaN", 1),
            "inventory-json-constant",
        )
        self.assert_invalid(
            canonical(valid_inventory()).replace(b"true", b"Infinity", 1),
            "inventory-json-constant",
        )

    def test_numeric_overflow_exponent_is_a_redacted_float_error(self) -> None:
        overflow = canonical(valid_inventory()).replace(b"true", b"1e999", 1)

        self.assert_invalid(overflow, "inventory-float")

    def test_fixed_inventory_values_cannot_drift(self) -> None:
        cases = (
            ("schema", "maestro-ha-s4-network-inventory-v2", "inventory-schema"),
            ("node_id", "s5", "inventory-node"),
            ("evidence_class", "PRODUCTION_WRITE", "inventory-evidence-class"),
        )
        for field, replacement, code in cases:
            with self.subTest(field=field):
                inventory = valid_inventory()
                inventory[field] = replacement
                self.assert_invalid(canonical(inventory), code)

    def test_unknown_keys_at_each_level_cannot_expand_the_inventory_contract(self) -> None:
        paths = (None, "networkd", "ifupdown", "health", "console")
        for path in paths:
            with self.subTest(path=path):
                inventory = copy.deepcopy(valid_inventory())
                if path is None:
                    inventory["unexpected"] = True
                else:
                    inventory[path]["unexpected"] = True
                self.assert_invalid(canonical(inventory), "inventory-keys")

    def test_missing_keys_at_each_level_cannot_be_interpreted_as_safe(self) -> None:
        removals = (
            (None, "schema"),
            ("networkd", "active"),
            ("ifupdown", "enabled"),
            ("health", "management_reachable"),
            ("console", "independent_access_confirmed"),
        )
        for path, key in removals:
            with self.subTest(path=path, key=key):
                inventory = copy.deepcopy(valid_inventory())
                target = inventory if path is None else inventory[path]
                del target[key]
                self.assert_invalid(canonical(inventory), "inventory-keys")

    def test_wrong_scalar_and_container_types_cannot_be_coerced(self) -> None:
        cases = (
            ("schema", 7, "inventory-string"),
            ("captured_at_utc", None, "inventory-string"),
            ("networkd", [], "inventory-object"),
            ("health", "not-an-object", "inventory-object"),
        )
        for key, replacement, code in cases:
            with self.subTest(key=key):
                inventory = valid_inventory()
                inventory[key] = replacement
                self.assert_invalid(canonical(inventory), code)

    def test_integer_zero_or_one_cannot_be_coerced_to_boolean_inventory_facts(self) -> None:
        for replacement in (0, 1):
            with self.subTest(replacement=replacement):
                inventory = valid_inventory()
                inventory["health"]["vpn_units_healthy"] = replacement
                self.assert_invalid(canonical(inventory), "inventory-boolean")

    def test_non_ascii_or_escaped_ambiguity_cannot_be_accepted(self) -> None:
        non_ascii = canonical(valid_inventory()).replace(b'"s4"', '"s４"'.encode("utf-8"))
        escaped = canonical(valid_inventory()).replace(b'"s4"', b'"s\\u0034"')

        self.assert_invalid(non_ascii, "inventory-json")
        self.assert_invalid(escaped, "inventory-canonical")

    def test_trusted_time_accepts_only_the_closed_open_freshness_window(self) -> None:
        raw = canonical(valid_inventory())
        captured = datetime(2026, 8, 31, 11, 50, 0, tzinfo=timezone.utc)
        expires = datetime(2026, 8, 31, 12, 5, 0, tzinfo=timezone.utc)

        self.assertEqual(parse_inventory(raw, evaluation_time=captured), valid_inventory())
        self.assertEqual(
            parse_inventory(raw, evaluation_time=expires - timedelta(seconds=1)),
            valid_inventory(),
        )
        self.assert_invalid_with_time(raw, captured - timedelta(seconds=1), "inventory-freshness")
        self.assert_invalid_with_time(raw, expires, "inventory-freshness")

    def test_expiry_window_cannot_exceed_fifteen_minutes(self) -> None:
        inventory = valid_inventory()
        inventory["expires_at_utc"] = "2026-08-31T12:05:01Z"

        self.assert_invalid(canonical(inventory), "inventory-freshness-window")

    def test_timestamps_must_be_utc_seconds_in_the_fixed_format(self) -> None:
        for field, value in (
            ("captured_at_utc", "2026-08-31T11:50:00+00:00"),
            ("expires_at_utc", "2026-08-31T12:05Z"),
            ("captured_at_utc", "2026-08-31T11:50:00.000Z"),
            ("expires_at_utc", "not-a-timestamp"),
        ):
            with self.subTest(field=field, value=value):
                inventory = valid_inventory()
                inventory[field] = value
                self.assert_invalid(canonical(inventory), "inventory-timestamp")

    def assert_invalid_with_time(
        self, raw: bytes, evaluation_time: datetime, code: str
    ) -> None:
        with self.assertRaises(S4ChangePackageError) as raised:
            parse_inventory(raw, evaluation_time=evaluation_time)
        self.assertEqual(str(raised.exception), f"s4-network-change-package:{code}")


class S4EvaluationTests(unittest.TestCase):
    INVENTORY_SHA256 = "a" * 64

    def parsed_inventory(self) -> dict[str, object]:
        return parse_inventory(canonical(valid_inventory()), evaluation_time=EVALUATION_TIME)

    def test_complete_evidence_emits_the_exact_non_mutating_change_package(self) -> None:
        inventory = self.parsed_inventory()
        original = copy.deepcopy(inventory)

        package = evaluate_inventory(inventory, inventory_sha256=self.INVENTORY_SHA256)

        self.assertEqual(inventory, original)
        self.assertEqual(
            package,
            {
                "apply_supported": False,
                "blockers": [],
                "change_scope": "REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY",
                "change_step_ids": [
                    "backup_ifupdown_state",
                    "remove_ifupdown_primary_declaration",
                    "disable_ifupdown_boot_ownership",
                    "preserve_systemd_networkd",
                ],
                "conflicting_manager": "ifupdown",
                "inventory_captured_at_utc": CAPTURED_AT,
                "inventory_expires_at_utc": EXPIRES_AT,
                "inventory_sha256": self.INVENTORY_SHA256,
                "mutation_authorized": False,
                "precheck_ids": [
                    "inventory_reviewed",
                    "networkd_working_owner",
                    "ifupdown_conflict_confirmed",
                    "management_vpn_health_green",
                    "console_recovery_ready",
                ],
                "rollback_ids": [
                    "restore_ifupdown_primary_declaration",
                    "restore_ifupdown_unit_state",
                    "repeat_s4_health_validation",
                ],
                "schema": "maestro-ha-s4-network-change-package-v1",
                "selected_owner": "systemd-networkd",
                "status": "EVIDENCE_COMPLETE",
                "stop_gate_ids": [
                    "trusted_utc_expired",
                    "console_unavailable",
                    "inventory_drift",
                    "unexpected_network_owner",
                    "prechange_health_degraded",
                    "unexpected_command_result",
                    "route_or_listener_loss",
                    "fresh_management_session_failed",
                ],
                "validation_ids": [
                    "single_primary_network_owner",
                    "networkd_active_enabled",
                    "default_route_preserved",
                    "fresh_management_session_established",
                    "vpn_units_listeners_preserved",
                    "no_new_failed_units",
                ],
            },
        )

    def test_each_inventory_fact_has_its_fixed_blocker_code(self) -> None:
        cases = (
            (("source_review_completed",), "source_review_incomplete"),
            (("networkd", "active"), "networkd_inactive"),
            (("networkd", "enabled"), "networkd_disabled"),
            (("networkd", "owns_primary_interface"), "networkd_not_primary_owner"),
            (("networkd", "owns_default_route"), "networkd_not_default_route_owner"),
            (("ifupdown", "enabled"), "ifupdown_disabled"),
            (("ifupdown", "declares_primary_interface"), "ifupdown_primary_declaration_absent"),
            (("ifupdown", "declares_default_route"), "ifupdown_default_route_declaration_absent"),
            (("ifupdown", "ifup_unit_failed"), "ifup_unit_state_drift"),
            (("ifupdown", "networking_unit_failed"), "networking_unit_state_drift"),
            (("health", "management_reachable"), "management_unreachable"),
            (("health", "vpn_units_healthy"), "vpn_units_unhealthy"),
            (("health", "expected_vpn_listeners_present"), "vpn_listeners_missing"),
            (("health", "default_route_present"), "default_route_missing"),
            (("console", "independent_access_confirmed"), "console_access_unconfirmed"),
            (("console", "recovery_procedure_reviewed"), "recovery_procedure_unreviewed"),
            (("console", "second_operator_available"), "second_operator_unavailable"),
        )
        for path, blocker in cases:
            with self.subTest(blocker=blocker):
                inventory = self.parsed_inventory()
                target = inventory
                for key in path[:-1]:
                    target = target[key]
                target[path[-1]] = False

                package = evaluate_inventory(inventory, inventory_sha256=self.INVENTORY_SHA256)

                self.assertEqual(package["blockers"], [blocker])
                self.assertEqual(package["status"], "BLOCKED")
                self.assertIs(package["apply_supported"], False)
                self.assertIs(package["mutation_authorized"], False)

    def test_multiple_blockers_follow_contract_order_not_input_construction_order(self) -> None:
        inventory = self.parsed_inventory()
        inventory["console"]["second_operator_available"] = False
        inventory["networkd"]["active"] = False
        inventory["health"]["default_route_present"] = False

        package = evaluate_inventory(inventory, inventory_sha256=self.INVENTORY_SHA256)

        self.assertEqual(
            package["blockers"],
            ["networkd_inactive", "default_route_missing", "second_operator_unavailable"],
        )

    def test_all_blockers_use_the_complete_frozen_literal_order(self) -> None:
        inventory = self.parsed_inventory()
        expected = [
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
        ]
        for _, path, _ in BLOCKER_RULES:
            target = inventory
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = False

        package = evaluate_inventory(inventory, inventory_sha256=self.INVENTORY_SHA256)

        self.assertEqual(list(BLOCKER_ORDER), expected)
        self.assertEqual(package["blockers"], expected)

    def test_package_binds_only_the_caller_digest_and_allowed_inventory_timestamps(self) -> None:
        package = evaluate_inventory(self.parsed_inventory(), inventory_sha256=self.INVENTORY_SHA256)
        encoded = canonical(package).decode("ascii")

        self.assertEqual(package["inventory_sha256"], self.INVENTORY_SHA256)
        self.assertEqual(
            set(package),
            {
                "apply_supported", "blockers", "change_scope", "change_step_ids",
                "conflicting_manager", "inventory_captured_at_utc",
                "inventory_expires_at_utc", "inventory_sha256", "mutation_authorized",
                "precheck_ids", "rollback_ids", "schema", "selected_owner", "status",
                "stop_gate_ids", "validation_ids",
            },
        )
        for forbidden in (
            "evaluation_time", "node_id", "evidence_class", "password", "token",
            "ssh", "endpoint", "production", "command_output",
        ):
            self.assertNotIn(forbidden, encoded)

    def test_non_lowercase_or_wrong_length_digest_cannot_label_the_package(self) -> None:
        for digest in ("A" * 64, "a" * 63, "g" * 64):
            with self.subTest(digest=digest):
                with self.assertRaisesRegex(
                    S4ChangePackageError,
                    r"^s4-network-change-package:inventory-digest$",
                ):
                    evaluate_inventory(self.parsed_inventory(), inventory_sha256=digest)


class S4BoundaryContractTests(unittest.TestCase):
    """Observable package-boundary behavior; POSIX race checks run in Linux CI."""

    def test_required_boundary_functions_are_public(self) -> None:
        for name in ("read_inventory", "publish_change_package", "run", "main"):
            with self.subTest(name=name):
                self.assertTrue(callable(getattr(s4_network_change_package, name, None)))

    def _require_boundary(self, name: str) -> None:
        if not callable(getattr(s4_network_change_package, name, None)):
            self.fail(f"missing boundary function: {name}")

    def test_non_posix_cli_fails_before_any_filesystem_access(self) -> None:
        self._require_boundary("run")
        stdout = io.StringIO()
        stderr = io.StringIO()
        denied = mock.Mock(side_effect=AssertionError("filesystem"))
        fake_os = SimpleNamespace(name="nt", lstat=denied, open=denied, read=denied)
        with tempfile.TemporaryDirectory() as temporary:
            with (
                mock.patch.object(s4_network_change_package, "os", fake_os, create=True),
                mock.patch.object(s4_network_change_package, "read_inventory", side_effect=AssertionError("read")),
                mock.patch.object(s4_network_change_package, "publish_change_package", side_effect=AssertionError("publish")),
            ):
                candidate = Path(temporary) / "must-not-exist"
                code = s4_network_change_package.run(
                    [
                        "package", "--inventory", "fixture-secret", "--evaluation-time",
                        "2026-08-31T12:00:00Z", "--output", str(candidate),
                    ],
                    stdout,
                    stderr,
                )
        self.assertEqual(code, 3)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(stderr.getvalue(), "s4-network-change-package:unsupported-platform\n")
        denied.assert_not_called()
        self.assertFalse(candidate.exists())

    def test_invalid_command_is_rejected_before_inventory_is_opened(self) -> None:
        self._require_boundary("run")
        stderr = io.StringIO()
        with mock.patch.object(
            s4_network_change_package, "read_inventory", side_effect=AssertionError("opened"), create=True
        ):
            code = s4_network_change_package.run(["matrix"], io.StringIO(), stderr)
        self.assertEqual(code, 3)
        self.assertEqual(stderr.getvalue(), "s4-network-change-package:input\n")

    def test_exact_cli_publishes_only_canonical_package_and_keeps_stdout_empty(self) -> None:
        published: list[tuple[str, bytes]] = []
        stdout = io.StringIO()
        stderr = io.StringIO()
        raw = canonical(valid_inventory())
        with (
            mock.patch.object(s4_network_change_package.os, "name", "posix"),
            mock.patch.object(
                s4_network_change_package,
                "read_inventory",
                return_value=(valid_inventory(), hashlib.sha256(raw).hexdigest()),
            ),
            mock.patch.object(
                s4_network_change_package,
                "publish_change_package",
                side_effect=lambda path, encoded: published.append((str(path), encoded)),
            ),
        ):
            code = s4_network_change_package.run(
                [
                    "package", "--inventory", "fixture-inventory", "--evaluation-time",
                    "2026-08-31T12:00:00Z", "--output", "fixture-output",
                ],
                stdout,
                stderr,
            )
        self.assertEqual(code, 0)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(stderr.getvalue(), "")
        self.assertEqual(published[0][0], "fixture-output")
        self.assertEqual(json.loads(published[0][1]), evaluate_inventory(
            valid_inventory(), inventory_sha256=hashlib.sha256(raw).hexdigest()
        ))

    def test_cli_preflight_rejects_every_non_exact_shape_before_opening_input(self) -> None:
        invalid = (
            [], ["matrix"], ["apply"], ["repair"], ["disable"], ["restart"], ["rollback"],
            ["p"], ["pkg"], ["packages"], ["package="], ["--package"],
            ["package"], ["packages"], ["package", "--help"],
            ["package", "--inventory", "x", "--evaluation-time", "t", "--output", "y", "extra"],
            ["package", "--inventory", "x", "--inventory", "t", "--output", "y"],
            ["package", "--tooling-sha", "x", "--evaluation-time", "t", "--output", "y"],
            ["package", "--inventory", "-x", "--evaluation-time", "t", "--output", "y"],
            ["package", "--inventory", "x", "--evaluation-time", "-t", "--output", "y"],
            ["package", "--inventory", "x", "--evaluation-time", "t", "--output", "-y"],
            ["package", "--inventory", "x", "--inventory", "x", "--evaluation-time", "t", "--output", "y"],
            ["package", "--inventory", "x", "--evaluation-time", "t", "--evaluation-time", "t", "--output", "y"],
            ["package", "--inventory", "x", "--evaluation-time", "t", "--output", "y", "--output", "y"],
        )
        for argv in invalid:
            with self.subTest(argv=argv):
                stderr = io.StringIO()
                with mock.patch.object(
                    s4_network_change_package, "read_inventory", side_effect=AssertionError("opened")
                ):
                    code = s4_network_change_package.run(argv, io.StringIO(), stderr)
                self.assertEqual(code, 3)
                self.assertEqual(stderr.getvalue(), "s4-network-change-package:input\n")

    def test_programmatic_argv_iteration_and_non_strings_are_redacted_input_errors(self) -> None:
        class ExplodingArgv:
            def __iter__(self) -> object:
                raise RuntimeError("fixture-secret")

        for argv in (["package", "--inventory", 1, "--evaluation-time", "t", "--output", "x"], ExplodingArgv()):
            with self.subTest(argv=type(argv).__name__):
                stderr = io.StringIO()
                code = s4_network_change_package.run(argv, io.StringIO(), stderr)  # type: ignore[arg-type]
                self.assertEqual(code, 3)
                self.assertEqual(stderr.getvalue(), "s4-network-change-package:input\n")

    def test_arbitrary_package_error_text_is_not_echoed(self) -> None:
        stderr = io.StringIO()
        with mock.patch.object(
            s4_network_change_package,
            "read_inventory",
            side_effect=S4ChangePackageError("fixture-secret-path-and-message"),
        ), mock.patch.object(s4_network_change_package.os, "name", "posix"):
            code = s4_network_change_package.run(
                [
                    "package", "--inventory", "inventory", "--evaluation-time",
                    "2026-08-31T12:00:00Z", "--output", "output",
                ],
                io.StringIO(),
                stderr,
            )
        self.assertEqual(code, 3)
        self.assertEqual(stderr.getvalue(), "s4-network-change-package:system\n")

    def test_wrapper_bootstrap_does_not_resolve_its_path(self) -> None:
        wrapper = Path(__file__).parents[1] / "s4-network-change-package.py"
        source = wrapper.read_text(encoding="utf-8")
        self.assertNotIn("Path(__file__).resolve", source)

    def test_wrapper_rejects_forbidden_commands_without_stdout_or_output(self) -> None:
        wrapper = Path(__file__).parents[1] / "s4-network-change-package.py"
        forbidden = ("matrix", "apply", "repair", "disable", "restart", "rollback")
        for command in forbidden:
            with self.subTest(command=command):
                completed = subprocess.run(
                    [sys.executable, str(wrapper), command], check=False, capture_output=True, text=True
                )
                self.assertEqual(completed.returncode, 3)
                self.assertEqual(completed.stdout, "")
                self.assertEqual(completed.stderr, "s4-network-change-package:input\n")

    def test_wrapper_rejects_every_invalid_shape_without_creating_output_or_leaking_help_env(self) -> None:
        wrapper = Path(__file__).parents[1] / "s4-network-change-package.py"
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "candidate"
            invalid = (
                ["pkg"],
                ["package", "--inventory", "x", "--evaluation-time", "t", "--output", str(output), "extra"],
                ["package", "--inventory", "x", "--tooling-sha", "sha", "--output", str(output)],
                ["package", "--inventory", "-x", "--evaluation-time", "t", "--output", str(output)],
            )
            for argv in invalid:
                with self.subTest(argv=argv):
                    completed = subprocess.run([sys.executable, str(wrapper), *argv], check=False, capture_output=True, text=True)
                    self.assertEqual((completed.returncode, completed.stdout, completed.stderr), (3, "", "s4-network-change-package:input\n"))
                    self.assertFalse(output.exists())
            environment = os.environ.copy()
            environment["S4_FIXTURE_SECRET"] = "fixture-secret-path"
            completed = subprocess.run([sys.executable, str(wrapper), "--help"], check=False, capture_output=True, text=True, env=environment)
            self.assertEqual(completed.returncode, 0)
            self.assertNotIn(environment["S4_FIXTURE_SECRET"], completed.stdout + completed.stderr)

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_descriptor_pinned_inventory_returns_sha256(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "inventory.json"
            raw = canonical(valid_inventory())
            path.write_bytes(raw)
            os.chmod(path, 0o600)

            inventory, digest = s4_network_change_package.read_inventory(
                path, evaluation_time=EVALUATION_TIME
            )

        self.assertEqual(inventory, valid_inventory())
        self.assertEqual(digest, hashlib.sha256(raw).hexdigest())

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_descriptor_pinned_inventory_rejects_unsafe_file_kinds_and_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            cases: list[Path] = []
            directory = root / "directory"
            directory.mkdir()
            os.chmod(directory, 0o600)
            cases.append(directory)
            empty = root / "empty"
            empty.write_bytes(b"")
            os.chmod(empty, 0o600)
            cases.append(empty)
            oversized = root / "oversized"
            oversized.write_bytes(b"x" * 16_385)
            os.chmod(oversized, 0o600)
            cases.append(oversized)
            for mode in (0o640, 0o604, 0o606, 0o660, 0o666, 0o700):
                unsafe = root / f"mode-{mode:o}"
                unsafe.write_bytes(canonical(valid_inventory()))
                os.chmod(unsafe, mode)
                cases.append(unsafe)
            private = root / "private"
            private.write_bytes(canonical(valid_inventory()))
            os.chmod(private, 0o600)
            linked = root / "linked"
            os.link(private, linked)
            cases.append(linked)
            symlink = root / "symlink"
            symlink.symlink_to(private)
            cases.append(symlink)
            fifo = root / "fifo"
            os.mkfifo(fifo, 0o600)
            cases.append(fifo)
            sock = socket.socket(socket.AF_UNIX)
            socket_path = root / "socket"
            sock.bind(str(socket_path))
            cases.append(socket_path)

            for path in cases:
                with self.subTest(path=path.name), self.assertRaisesRegex(
                    S4ChangePackageError, r"^s4-network-change-package:inventory$"
                ):
                    s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)
            sock.close()

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_descriptor_reader_has_exact_bound_and_nonblocking_open(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "inventory.json"
            path.write_bytes(b"x" * 16_384)
            os.chmod(path, 0o600)
            real_open = s4_network_change_package.os.open
            opened_flags: list[int] = []
            with mock.patch.object(s4_network_change_package.os, "open", side_effect=lambda *args, **kwargs: (opened_flags.append(args[1]), real_open(*args, **kwargs))[1]), mock.patch.object(s4_network_change_package, "parse_inventory", return_value={"fixture": True}) as parser:
                inventory, digest = s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)
            self.assertEqual(inventory, {"fixture": True})
            self.assertEqual(digest, hashlib.sha256(b"x" * 16_384).hexdigest())
            self.assertTrue(opened_flags[0] & getattr(os, "O_NONBLOCK", 0))
            parser.assert_called_once()
            path.write_bytes(b"x" * 16_385)
            os.chmod(path, 0o600)
            with mock.patch.object(s4_network_change_package, "parse_inventory") as parser:
                with self.assertRaisesRegex(S4ChangePackageError, r":inventory$"):
                    s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)
            parser.assert_not_called()

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_descriptor_pinned_inventory_rejects_recheck_drift(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "inventory.json"
            path.write_bytes(canonical(valid_inventory()))
            os.chmod(path, 0o600)
            original = s4_network_change_package._fingerprint
            fingerprints = [original(os.lstat(path))] * 3 + [(1, 2, 3, 4, 5, 6, 7, 8)]
            with mock.patch.object(s4_network_change_package, "_fingerprint", side_effect=fingerprints):
                with self.assertRaisesRegex(
                    S4ChangePackageError, r"^s4-network-change-package:inventory$"
                ):
                    s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_publish_creates_private_single_link_regular_file(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            os.chmod(root, 0o700)
            output = root / "package.json"
            encoded = canonical({"schema": "fixture"})

            s4_network_change_package.publish_change_package(output, encoded)

            info = os.lstat(output)
            self.assertTrue(stat.S_ISREG(info.st_mode))
            self.assertEqual(stat.S_IMODE(info.st_mode), 0o600)
            self.assertEqual(info.st_uid, os.geteuid())
            self.assertEqual(info.st_nlink, 1)
            self.assertEqual(output.read_bytes(), encoded)

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_publish_never_overwrites_an_existing_final_or_temp_name(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            os.chmod(root, 0o700)
            output = root / "package.json"
            output.write_bytes(b"preexisting")
            os.chmod(output, 0o600)
            with self.assertRaisesRegex(S4ChangePackageError, r":exists$"):
                s4_network_change_package.publish_change_package(output, b"new")
            self.assertEqual(output.read_bytes(), b"preexisting")

            output.unlink()
            temporary_name = ".s4-network-change-" + "a" * 24 + ".tmp"
            collision = root / temporary_name
            collision.write_bytes(b"other-invocation")
            os.chmod(collision, 0o600)
            with mock.patch.object(s4_network_change_package.secrets, "token_hex", return_value="a" * 24):
                with self.assertRaisesRegex(S4ChangePackageError, r":output$"):
                    s4_network_change_package.publish_change_package(output, b"new")
            self.assertFalse(output.exists())
            self.assertEqual(collision.read_bytes(), b"other-invocation")

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_publish_rolls_back_only_its_own_final_on_write_or_link_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            os.chmod(root, 0o700)
            output = root / "package.json"
            with mock.patch.object(s4_network_change_package.os, "write", return_value=0):
                with self.assertRaisesRegex(S4ChangePackageError, r":output$"):
                    s4_network_change_package.publish_change_package(output, b"new")
            self.assertFalse(output.exists())
            self.assertEqual(list(root.iterdir()), [])

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_publish_rolls_back_a_final_created_before_link_reports_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            os.chmod(root, 0o700)
            output = root / "package.json"
            real_link = s4_network_change_package.os.link

            def link_then_fail(*args: object, **kwargs: object) -> None:
                real_link(*args, **kwargs)
                raise OSError("fixture post-link failure")

            with mock.patch.object(s4_network_change_package.os, "link", side_effect=link_then_fail):
                with self.assertRaisesRegex(S4ChangePackageError, r":output$"):
                    s4_network_change_package.publish_change_package(output, b"new")

            self.assertFalse(output.exists())
            self.assertEqual(list(root.iterdir()), [])

            with mock.patch.object(s4_network_change_package.os, "link", side_effect=OSError("fixture")):
                with self.assertRaisesRegex(S4ChangePackageError, r":output$"):
                    s4_network_change_package.publish_change_package(output, b"new")
            self.assertFalse(output.exists())
            self.assertEqual(list(root.iterdir()), [])

    def test_wrapper_has_no_stdout_for_a_redacted_help_request(self) -> None:
        self._require_boundary("main")
        wrapper = Path(__file__).parents[1] / "s4-network-change-package.py"
        completed = subprocess.run(
            [sys.executable, str(wrapper), "--help"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(completed.returncode, 0)
        self.assertEqual(completed.stderr, "")
        self.assertIn("package", completed.stdout)
        self.assertNotIn("fixture-secret", completed.stdout)


class S4SecureInputTests(unittest.TestCase):
    """Focused POSIX proof for the descriptor-pinned inventory boundary."""

    def _private_file(self, root: Path, name: str = "inventory.json") -> Path:
        path = root / name
        path.write_bytes(canonical(valid_inventory()))
        os.chmod(path, 0o600)
        return path

    def _assert_rejected_without_parser(self, path: Path) -> None:
        with mock.patch.object(s4_network_change_package, "parse_inventory") as parser:
            with self.assertRaisesRegex(S4ChangePackageError, r"^s4-network-change-package:inventory$"):
                s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)
        parser.assert_not_called()

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_full_fstat_proxy_rejects_wrong_invoking_uid_before_parser(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = self._private_file(Path(temporary))
            real_fstat = os.fstat

            def wrong_uid(descriptor: int) -> object:
                info = real_fstat(descriptor)
                return SimpleNamespace(
                    st_dev=info.st_dev, st_ino=info.st_ino, st_mode=info.st_mode,
                    st_uid=info.st_uid + 1, st_nlink=info.st_nlink, st_size=info.st_size,
                    st_mtime=info.st_mtime, st_ctime=info.st_ctime,
                    st_mtime_ns=info.st_mtime_ns, st_ctime_ns=info.st_ctime_ns,
                )

            with mock.patch.object(s4_network_change_package.os, "fstat", side_effect=wrong_uid):
                self._assert_rejected_without_parser(path)

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_exact_private_mode_matrix_accepts_only_0600(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = self._private_file(Path(temporary))
            info = os.lstat(path)
            for mode in range(0o1000):
                candidate = SimpleNamespace(
                    st_mode=stat.S_IFREG | mode, st_nlink=1, st_uid=os.geteuid(),
                    st_size=1, st_dev=info.st_dev, st_ino=info.st_ino,
                    st_mtime=info.st_mtime, st_ctime=info.st_ctime,
                    st_mtime_ns=info.st_mtime_ns, st_ctime_ns=info.st_ctime_ns,
                )
                with self.subTest(mode=oct(mode)):
                    if mode == 0o600:
                        s4_network_change_package._require_private_regular(
                            candidate, code="inventory", maximum=1
                        )
                    else:
                        with self.assertRaisesRegex(S4ChangePackageError, r":inventory$"):
                            s4_network_change_package._require_private_regular(
                                candidate, code="inventory", maximum=1
                            )

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_canonical_inventory_with_a_trailing_byte_is_rejected_as_invalid_json(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = self._private_file(Path(temporary))
            path.write_bytes(canonical(valid_inventory()) + b"x")
            os.chmod(path, 0o600)
            with self.assertRaisesRegex(S4ChangePackageError, r"^s4-network-change-package:inventory-json$"):
                s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_concrete_path_replacement_truncation_and_metadata_drift_reject_before_parser(self) -> None:
        for drift in ("replacement", "truncation", "metadata"):
            with self.subTest(drift=drift), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                path = self._private_file(root)
                replacement = self._private_file(root, "replacement.json")
                original_open = os.open
                original_read = os.read
                changed = False

                def open_then_replace(*args: object, **kwargs: object) -> int:
                    nonlocal changed
                    descriptor = original_open(*args, **kwargs)
                    if drift == "replacement" and not changed and os.fspath(args[0]) == os.fspath(path):
                        os.replace(replacement, path)
                        changed = True
                    return descriptor

                def read_then_drift(descriptor: int, size: int) -> bytes:
                    nonlocal changed
                    value = original_read(descriptor, size)
                    if drift == "truncation" and not changed:
                        with open(path, "r+b") as handle:
                            handle.truncate(1)
                        changed = True
                    if drift == "metadata" and not changed:
                        os.chmod(path, 0o400)
                        changed = True
                    return value

                patches = [mock.patch.object(s4_network_change_package.os, "open", side_effect=open_then_replace)]
                if drift != "replacement":
                    patches.append(mock.patch.object(s4_network_change_package.os, "read", side_effect=read_then_drift))
                with patches[0]:
                    if len(patches) == 2:
                        with patches[1]:
                            self._assert_rejected_without_parser(path)
                    else:
                        self._assert_rejected_without_parser(path)
                self.assertTrue(changed)

    @unittest.skipUnless(os.name == "posix", "POSIX descriptor boundary")
    def test_fifo_open_is_nonblocking_when_path_check_is_raced(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fifo = root / "inventory-fifo"
            os.mkfifo(fifo, 0o600)
            regular = self._private_file(root, "regular")
            real_lstat = os.lstat
            real_open = os.open
            flags_seen: list[int] = []

            def regular_before_fifo(path: object, *args: object, **kwargs: object) -> os.stat_result:
                if os.fspath(path) == os.fspath(fifo):
                    return real_lstat(regular)
                return real_lstat(path, *args, **kwargs)

            def observe_open(path: object, flags: int, *args: object, **kwargs: object) -> int:
                flags_seen.append(flags)
                return real_open(path, flags, *args, **kwargs)

            with (
                mock.patch.object(s4_network_change_package.os, "lstat", side_effect=regular_before_fifo),
                mock.patch.object(s4_network_change_package.os, "open", side_effect=observe_open),
            ):
                self._assert_rejected_without_parser(fifo)
            self.assertTrue(flags_seen[0] & getattr(os, "O_NONBLOCK", 0))


class S4SecureOutputTests(unittest.TestCase):
    """Focused POSIX proof for the no-clobber publication boundary."""

    def _root(self) -> tempfile.TemporaryDirectory[str]:
        temporary = tempfile.TemporaryDirectory()
        os.chmod(temporary.name, 0o700)
        return temporary

    def _assert_output_error(self, output: Path, encoded: bytes = b"package\n") -> None:
        with self.assertRaisesRegex(S4ChangePackageError, r"^s4-network-change-package:(?:output|output-exists|exists)$"):
            s4_network_change_package.publish_change_package(output, encoded)

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_unsafe_parent_symlink_wrong_uid_wrong_mode_and_replacement_fail_closed(self) -> None:
        with self._root() as temporary:
            root = Path(temporary)
            target = root / "target"
            target.mkdir()
            os.chmod(target, 0o700)
            link = root / "link"
            link.symlink_to(target, target_is_directory=True)
            self._assert_output_error(link / "package")
            for mode in (0o000, 0o600, 0o701, 0o755):
                os.chmod(target, mode)
                with self.subTest(mode=oct(mode)):
                    self._assert_output_error(target / "package")
            os.chmod(target, 0o700)
            real_lstat = os.lstat
            def wrong_uid(path: object, *args: object, **kwargs: object) -> object:
                info = real_lstat(path, *args, **kwargs)
                if os.fspath(path) == os.fspath(target):
                    return SimpleNamespace(
                        st_dev=info.st_dev, st_ino=info.st_ino, st_mode=info.st_mode,
                        st_uid=info.st_uid + 1, st_nlink=info.st_nlink, st_size=info.st_size,
                        st_mtime=info.st_mtime, st_ctime=info.st_ctime,
                        st_mtime_ns=info.st_mtime_ns, st_ctime_ns=info.st_ctime_ns,
                    )
                return info
            with mock.patch.object(s4_network_change_package.os, "lstat", side_effect=wrong_uid):
                self._assert_output_error(target / "package")
            replacement = root / "replacement"
            replacement.mkdir()
            os.chmod(replacement, 0o700)
            original_open = os.open
            changed = False
            def open_then_replace(path: object, flags: int, *args: object, **kwargs: object) -> int:
                nonlocal changed
                descriptor = original_open(path, flags, *args, **kwargs)
                if os.fspath(path) == os.fspath(target) and not changed:
                    os.rmdir(target)
                    os.rename(replacement, target)
                    changed = True
                return descriptor
            with mock.patch.object(s4_network_change_package.os, "open", side_effect=open_then_replace):
                self._assert_output_error(target / "package")
            self.assertTrue(changed)

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_final_regular_symlink_hardlink_and_prelink_substitution_are_never_clobbered(self) -> None:
        for kind in ("regular", "symlink", "hardlink", "substitution"):
            with self.subTest(kind=kind), self._root() as temporary:
                root = Path(temporary)
                output = root / "package"
                foreign = root / "foreign"
                foreign.write_bytes(b"foreign\n")
                os.chmod(foreign, 0o600)
                if kind == "regular":
                    output.write_bytes(b"foreign\n")
                    os.chmod(output, 0o600)
                elif kind == "symlink":
                    output.symlink_to(foreign)
                elif kind == "hardlink":
                    os.link(foreign, output)
                else:
                    original_open = os.open
                    injected = False
                    def open_then_inject(path: object, flags: int, *args: object, **kwargs: object) -> int:
                        nonlocal injected
                        descriptor = original_open(path, flags, *args, **kwargs)
                        if os.fspath(path).startswith(".s4-network-change-") and not injected:
                            output.write_bytes(b"foreign\n")
                            os.chmod(output, 0o600)
                            injected = True
                        return descriptor
                    patch = mock.patch.object(s4_network_change_package.os, "open", side_effect=open_then_inject)
                    with patch:
                        self._assert_output_error(output)
                    self.assertTrue(injected)
                    self.assertEqual(output.read_bytes(), b"foreign\n")
                    continue
                self._assert_output_error(output)
                self.assertEqual(output.read_bytes(), b"foreign\n")

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_temp_collision_and_substitution_preserve_foreign_inode(self) -> None:
        with self._root() as temporary:
            root = Path(temporary)
            output = root / "package"
            temp_name = ".s4-network-change-" + "a" * 24 + ".tmp"
            temp = root / temp_name
            temp.write_bytes(b"foreign-temp\n")
            os.chmod(temp, 0o600)
            with mock.patch.object(s4_network_change_package.secrets, "token_hex", return_value="a" * 24):
                self._assert_output_error(output)
            self.assertEqual(temp.read_bytes(), b"foreign-temp\n")
        with self._root() as temporary:
            root = Path(temporary)
            output = root / "package"
            temp_name = ".s4-network-change-" + "b" * 24 + ".tmp"
            temp = root / temp_name
            original_open = os.open
            injected = False
            def open_then_substitute(path: object, flags: int, *args: object, **kwargs: object) -> int:
                nonlocal injected
                descriptor = original_open(path, flags, *args, **kwargs)
                if os.fspath(path) == temp_name and not injected:
                    os.unlink(temp)
                    temp.write_bytes(b"foreign-temp\n")
                    os.chmod(temp, 0o600)
                    injected = True
                return descriptor
            with (
                mock.patch.object(s4_network_change_package.secrets, "token_hex", return_value="b" * 24),
                mock.patch.object(s4_network_change_package.os, "open", side_effect=open_then_substitute),
            ):
                self._assert_output_error(output)
            self.assertTrue(injected)
            self.assertEqual(temp.read_bytes(), b"foreign-temp\n")
            self.assertFalse(output.exists())

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_role_based_write_temp_fsync_and_directory_fsync_failures_roll_back_owned_final(self) -> None:
        for failure in ("write", "temp-fsync", "directory-fsync"):
            with self.subTest(failure=failure), self._root() as temporary:
                root = Path(temporary)
                output = root / "package"
                real_fsync = os.fsync
                real_fstat = os.fstat
                real_unlink = os.unlink
                failed = False
                directory_fsync_roles: list[str] = []
                events: list[str] = []
                def fail_role(descriptor: int) -> None:
                    nonlocal failed
                    is_directory = stat.S_ISDIR(real_fstat(descriptor).st_mode)
                    if failure == "directory-fsync" and is_directory:
                        role = "publication" if not failed else "rollback"
                        directory_fsync_roles.append(role)
                        events.append(f"directory-fsync:{role}")
                    if failure == "temp-fsync" and not is_directory and not failed:
                        failed = True
                        raise OSError("synthetic")
                    if failure == "directory-fsync" and is_directory and not failed:
                        failed = True
                        raise OSError("synthetic")
                    real_fsync(descriptor)
                def observe_unlink(name: object, *args: object, **kwargs: object) -> None:
                    if failure == "directory-fsync" and os.fspath(name) == output.name:
                        events.append("rollback-final-unlink")
                    real_unlink(name, *args, **kwargs)
                patches = [mock.patch.object(s4_network_change_package.os, "fsync", side_effect=fail_role)]
                if failure == "directory-fsync":
                    patches.append(mock.patch.object(s4_network_change_package.os, "unlink", side_effect=observe_unlink))
                if failure == "write":
                    patches.insert(0, mock.patch.object(s4_network_change_package.os, "write", return_value=0))
                with patches[0]:
                    if len(patches) == 3:
                        with patches[1], patches[2]:
                            self._assert_output_error(output)
                    elif len(patches) == 2:
                        with patches[1]:
                            self._assert_output_error(output)
                    else:
                        self._assert_output_error(output)
                if failure != "write":
                    self.assertTrue(failed)
                if failure == "directory-fsync":
                    self.assertGreaterEqual(len(directory_fsync_roles), 2)
                    self.assertEqual(directory_fsync_roles[:2], ["publication", "rollback"])
                    self.assertLess(
                        events.index("directory-fsync:publication"),
                        events.index("rollback-final-unlink"),
                    )
                    self.assertLess(
                        events.index("rollback-final-unlink"),
                        events.index("directory-fsync:rollback"),
                    )
                self.assertFalse(output.exists())
                self.assertEqual(list(root.iterdir()), [])

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_link_fileexists_races_and_post_link_recheck_preserve_foreign_final(self) -> None:
        for mode in ("ordinary-race", "foreign-then-raise", "post-link-recheck"):
            with self.subTest(mode=mode), self._root() as temporary:
                root = Path(temporary)
                output = root / "package"
                original_link = os.link
                foreign = b"foreign-final\n"
                def link_race(source: object, destination: object, *args: object, **kwargs: object) -> None:
                    if mode == "post-link-recheck":
                        original_link(source, destination, *args, **kwargs)
                        output.unlink()
                        output.write_bytes(foreign)
                        os.chmod(output, 0o600)
                        return None
                    output.write_bytes(foreign)
                    os.chmod(output, 0o600)
                    raise FileExistsError("synthetic")
                with mock.patch.object(s4_network_change_package.os, "link", side_effect=link_race):
                    self._assert_output_error(output)
                self.assertEqual(output.read_bytes(), foreign)
                self.assertFalse(any(entry.name.startswith(".s4-network-change-") for entry in root.iterdir()))

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_ambiguous_link_failure_and_owned_rollback_leave_no_final(self) -> None:
        with self._root() as temporary:
            root = Path(temporary)
            output = root / "package"
            original_link = os.link
            def link_then_raise(*args: object, **kwargs: object) -> None:
                original_link(*args, **kwargs)
                raise OSError("synthetic")
            with mock.patch.object(s4_network_change_package.os, "link", side_effect=link_then_raise):
                self._assert_output_error(output)
            self.assertFalse(output.exists())
            self.assertEqual(list(root.iterdir()), [])

    @unittest.skipUnless(os.name == "posix", "POSIX output boundary")
    def test_success_has_exact_bytes_private_uid_regular_single_link(self) -> None:
        with self._root() as temporary:
            output = Path(temporary) / "package"
            encoded = canonical({"status": "fixture"})
            s4_network_change_package.publish_change_package(output, encoded)
            info = os.lstat(output)
            self.assertTrue(stat.S_ISREG(info.st_mode))
            self.assertEqual((info.st_uid, stat.S_IMODE(info.st_mode), info.st_nlink), (os.geteuid(), 0o600, 1))
            self.assertEqual(output.read_bytes(), encoded)


class S4CliBoundaryTests(unittest.TestCase):
    """Exact command grammar proof, separately for direct and wrapper calls."""

    @staticmethod
    def _invalid_argv(output: str) -> tuple[list[str], ...]:
        forbidden = ("matrix", "apply", "repair", "disable", "restart", "rollback")
        aliases = ("p", "pkg", "packages", "package=", "--package")
        shapes: list[list[str]] = [[], ["package"], ["packages"], ["package", "--help"]]
        shapes.extend([[name] for name in forbidden + aliases])
        for option in ("--unknown", "--tooling-sha", "--inventory-extra", "--evaluation_time", "--out"):
            shapes.append(["package", option, "value", "--inventory", "inventory", "--evaluation-time", "2026-08-31T12:00:00Z", "--output", output])
        valid = ["package", "--inventory", "inventory", "--evaluation-time", "2026-08-31T12:00:00Z", "--output", output]
        for position in (2, 4, 6):
            option_value = list(valid)
            option_value[position] = "--option-like"
            shapes.append(option_value)
        for option, value in (("--inventory", "inventory"), ("--evaluation-time", "2026-08-31T12:00:00Z"), ("--output", output)):
            shapes.append(valid + [option, value])
        shapes.extend((
            valid + ["extra"],
            ["package", "--inventory", "inventory", "--inventory", "again", "--evaluation-time", "2026-08-31T12:00:00Z", "--output", output],
            ["package", "--inventory", "inventory", "--evaluation-time", "2026-08-31T12:00:00Z", "--evaluation-time", "2026-08-31T12:01:00Z", "--output", output],
            ["package", "--inventory", "inventory", "--evaluation-time", "2026-08-31T12:00:00Z", "--output", output, "--output", output + ".two"],
        ))
        return tuple(shapes)

    def test_direct_invalid_matrix_never_reads_or_publishes_and_is_redacted(self) -> None:
        for argv in self._invalid_argv("candidate"):
            with self.subTest(argv=argv):
                stdout = io.StringIO()
                stderr = io.StringIO()
                read_sentinel = mock.Mock(side_effect=AssertionError("read sentinel"))
                publish_sentinel = mock.Mock(side_effect=AssertionError("publish sentinel"))
                with (
                    mock.patch.object(s4_network_change_package, "read_inventory", read_sentinel),
                    mock.patch.object(s4_network_change_package, "publish_change_package", publish_sentinel),
                ):
                    code = s4_network_change_package.run(argv, stdout, stderr)
                self.assertEqual((code, stdout.getvalue(), stderr.getvalue()), (3, "", "s4-network-change-package:input\n"))
                read_sentinel.assert_not_called()
                publish_sentinel.assert_not_called()

    def test_direct_invalid_evaluation_times_fail_before_read_or_publication(self) -> None:
        invalid_times = (
            "2026-08-31T12:00:00+00:00",
            "2026-08-31T12:00:00.000Z",
            "2026-02-30T12:00:00Z",
            "2026-08-31T12:00:00z",
            "2026-08-31T12:00Z",
        )
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "must-not-exist"
            for evaluation_time in invalid_times:
                with self.subTest(evaluation_time=evaluation_time):
                    stdout = io.StringIO()
                    stderr = io.StringIO()
                    read_sentinel = mock.Mock(side_effect=AssertionError("read sentinel"))
                    publish_sentinel = mock.Mock(side_effect=AssertionError("publish sentinel"))
                    with (
                        mock.patch.object(s4_network_change_package, "read_inventory", read_sentinel),
                        mock.patch.object(s4_network_change_package, "publish_change_package", publish_sentinel),
                    ):
                        code = s4_network_change_package.run(
                            [
                                "package", "--inventory", "valid-inventory",
                                "--evaluation-time", evaluation_time,
                                "--output", str(output),
                            ],
                            stdout,
                            stderr,
                        )
                    self.assertEqual((code, stdout.getvalue(), stderr.getvalue()), (3, "", "s4-network-change-package:input\n"))
                    read_sentinel.assert_not_called()
                    publish_sentinel.assert_not_called()
                    self.assertFalse(output.exists())

    def test_wrapper_invalid_matrix_never_creates_candidate_and_help_does_not_leak_poisoned_environment(self) -> None:
        wrapper = Path(__file__).parents[1] / "s4-network-change-package.py"
        environment = os.environ.copy()
        environment["S4_POISONED_ENV"] = "fixture-secret-environment"
        environment["PATH"] = "fixture-secret-path"
        with tempfile.TemporaryDirectory() as temporary:
            for argv in self._invalid_argv(str(Path(temporary) / "candidate")):
                with self.subTest(argv=argv):
                    output = Path(temporary) / "candidate"
                    completed = subprocess.run([sys.executable, str(wrapper), *argv], check=False, capture_output=True, text=True, env=environment)
                    self.assertEqual((completed.returncode, completed.stdout, completed.stderr), (3, "", "s4-network-change-package:input\n"))
                    self.assertFalse(output.exists())
            help_result = subprocess.run([sys.executable, str(wrapper), "--help"], check=False, capture_output=True, text=True, env=environment)
        self.assertEqual((help_result.returncode, help_result.stderr), (0, ""))
        self.assertNotIn("fixture-secret", help_result.stdout + help_result.stderr)

    @unittest.skipUnless(os.name == "posix", "wrapper publication requires POSIX")
    def test_wrapper_subprocess_emits_complete_blocked_and_error_contracts(self) -> None:
        wrapper = Path(__file__).parents[1] / "s4-network-change-package.py"
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            os.chmod(root, 0o700)
            for name, source_review, expected_code, expected_status in (
                ("complete", True, 0, "EVIDENCE_COMPLETE"),
                ("blocked", False, 2, "BLOCKED"),
            ):
                inventory = valid_inventory()
                inventory["source_review_completed"] = source_review
                input_path = root / f"{name}.json"
                output_path = root / f"{name}.out"
                input_path.write_bytes(canonical(inventory))
                os.chmod(input_path, 0o600)
                completed = subprocess.run(
                    [sys.executable, str(wrapper), "package", "--inventory", str(input_path), "--evaluation-time", "2026-08-31T12:00:00Z", "--output", str(output_path)],
                    check=False, capture_output=True, text=True,
                )
                self.assertEqual((completed.returncode, completed.stdout, completed.stderr), (expected_code, "", ""))
                self.assertEqual(json.loads(output_path.read_bytes())["status"], expected_status)
            invalid = root / "invalid.json"
            invalid.write_bytes(b"not-json")
            os.chmod(invalid, 0o600)
            no_final = root / "no-final"
            completed = subprocess.run(
                [sys.executable, str(wrapper), "package", "--inventory", str(invalid), "--evaluation-time", "2026-08-31T12:00:00Z", "--output", str(no_final)],
                check=False, capture_output=True, text=True,
            )
            self.assertEqual((completed.returncode, completed.stdout, completed.stderr), (3, "", "s4-network-change-package:inventory-json\n"))
            self.assertFalse(no_final.exists())


class S4CapabilityDenylistTests(unittest.TestCase):
    """The package boundary has no network, shell, or remote-control capability."""

    ROOT = Path(__file__).parents[3]
    ACTIVE_SOURCES = (
        ROOT / "ops" / "ha" / "s4_network_change_package.py",
        ROOT / "ops" / "ha" / "s4-network-change-package.py",
    )
    FORBIDDEN_IMPORT_ROOTS = frozenset(
        {
            "aiohttp",
            "asyncssh",
            "fabric",
            "ftplib",
            "http",
            "httpx",
            "imaplib",
            "importlib",
            "multiprocessing",
            "nntplib",
            "paramiko",
            "pexpect",
            "poplib",
            "requests",
            "smtplib",
            "socket",
            "socketserver",
            "subprocess",
            "telnetlib",
            "urllib",
            "websocket",
            "websockets",
            "xmlrpc",
        }
    )
    ALLOWED_LOCAL_MODULES = frozenset({"ops.ha.s4_network_change_package"})
    FORBIDDEN_CALLS = frozenset(
        {
            "compile",
            "eval",
            "exec",
            "__import__",
            "asyncio.create_subprocess_exec",
            "asyncio.create_subprocess_shell",
            "importlib.import_module",
            "os.execv",
            "os.execve",
            "os.execvp",
            "os.execvpe",
            "os.fork",
            "os.forkpty",
            "os.kill",
            "os.killpg",
            "os.popen",
            "os.posix_spawn",
            "os.posix_spawnp",
            "os.spawnl",
            "os.spawnle",
            "os.spawnlp",
            "os.spawnlpe",
            "os.spawnv",
            "os.spawnve",
            "os.spawnvp",
            "os.spawnvpe",
            "os.startfile",
            "os.system",
        }
    )
    FORBIDDEN_OS_MEMBERS = frozenset(
        name.partition(".")[2]
        for name in FORBIDDEN_CALLS
        if name.startswith("os.")
    )

    @staticmethod
    def _dotted_name(node: ast.AST) -> str | None:
        parts: list[str] = []
        while isinstance(node, ast.Attribute):
            parts.append(node.attr)
            node = node.value
        if not isinstance(node, ast.Name):
            return None
        parts.append(node.id)
        return ".".join(reversed(parts))

    @staticmethod
    def _resolve_import_alias(name: str, aliases: dict[str, str]) -> str:
        root, separator, suffix = name.partition(".")
        resolved_root = aliases.get(root, root)
        return resolved_root + (separator + suffix if separator else "")

    @classmethod
    def _forbidden_capabilities(cls, source: str) -> tuple[str, ...]:
        tree = ast.parse(source)
        findings: set[str] = set()
        aliases: dict[str, str] = {}
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    local_name = alias.asname or alias.name.partition(".")[0]
                    aliases[local_name] = alias.name if alias.asname else local_name
            elif isinstance(node, ast.ImportFrom) and node.module:
                for alias in node.names:
                    if alias.name == "*":
                        continue
                    local_name = alias.asname or alias.name
                    aliases[local_name] = f"{node.module}.{alias.name}"
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    root = alias.name.partition(".")[0]
                    if root in cls.FORBIDDEN_IMPORT_ROOTS:
                        findings.add(f"import:{root}")
                    if (
                        alias.name.startswith("ops.ha.")
                        and alias.name not in cls.ALLOWED_LOCAL_MODULES
                    ):
                        findings.add("import:local-mutator")
            elif isinstance(node, ast.ImportFrom):
                root = (node.module or "").partition(".")[0]
                if root in cls.FORBIDDEN_IMPORT_ROOTS:
                    findings.add(f"import:{root}")
                if (
                    node.module is not None
                    and node.module.startswith("ops.ha")
                    and node.module not in cls.ALLOWED_LOCAL_MODULES
                ):
                    findings.add("import:local-mutator")
                if root == "os":
                    for alias in node.names:
                        if alias.name in cls.FORBIDDEN_OS_MEMBERS:
                            findings.add(f"import:os.{alias.name}")
            elif isinstance(node, ast.Call):
                name = cls._dotted_name(node.func)
                if name is None:
                    continue
                name = cls._resolve_import_alias(name, aliases)
                root = name.partition(".")[0]
                if root in cls.FORBIDDEN_IMPORT_ROOTS or name in cls.FORBIDDEN_CALLS:
                    findings.add(f"call:{name}")
                if (
                    name == "getattr"
                    and len(node.args) >= 2
                    and isinstance(node.args[1], ast.Constant)
                    and isinstance(node.args[1].value, str)
                ):
                    base_name = cls._dotted_name(node.args[0])
                    if base_name is not None:
                        resolved_base = cls._resolve_import_alias(base_name, aliases)
                        candidate = f"{resolved_base}.{node.args[1].value}"
                        if candidate in cls.FORBIDDEN_CALLS:
                            findings.add(f"call:getattr-{candidate.replace('.', '-')}")
        return tuple(sorted(findings))

    def test_active_core_and_wrapper_have_no_forbidden_capability(self) -> None:
        for path in self.ACTIVE_SOURCES:
            with self.subTest(path=path.name):
                self.assertTrue(path.is_file(), "active S4 source is missing")
                self.assertEqual(
                    (),
                    self._forbidden_capabilities(path.read_text(encoding="utf-8")),
                    "active S4 source gained a forbidden capability",
                )

    def test_capability_denylist_fixture_matrix_is_non_vacuous(self) -> None:
        fixtures = (
            "import socket\nsocket.create_connection(('fixture.invalid', 1))\n",
            "from urllib import request\nrequest.urlopen('fixture')\n",
            "import subprocess\nsubprocess.run(['fixture'])\n",
            "import os\nos.system('fixture')\n",
            "from os import system\nsystem('fixture')\n",
            "import os\ngetattr(os, 'system')('fixture')\n",
            "__import__('socket')\n",
            "exec('fixture')\n",
        )
        for source in fixtures:
            with self.subTest(source=source.splitlines()[0]):
                self.assertTrue(self._forbidden_capabilities(source))

    def test_capability_denylist_resolves_forbidden_import_aliases(self) -> None:
        fixtures = (
            (
                "import os as runner\nrunner.system('fixture')\n",
                "call:os.system",
            ),
            (
                "from os import system as runner\nrunner('fixture')\n",
                "call:os.system",
            ),
            (
                "import asyncio as loop\nloop.create_subprocess_exec('fixture')\n",
                "call:asyncio.create_subprocess_exec",
            ),
            (
                "from asyncio import create_subprocess_shell as launch\n"
                "launch('fixture')\n",
                "call:asyncio.create_subprocess_shell",
            ),
        )
        for source, expected in fixtures:
            with self.subTest(source=source.splitlines()[0]):
                self.assertIn(expected, self._forbidden_capabilities(source))

    def test_capability_denylist_rejects_local_mutators_and_network_clients(self) -> None:
        fixtures = (
            "from ops.ha import deploy_node\ndeploy_node.main()\n",
            "import ftplib\nftplib.FTP('fixture')\n",
            "import smtplib\nsmtplib.SMTP('fixture')\n",
        )
        for source in fixtures:
            with self.subTest(source=source.splitlines()[0]):
                self.assertTrue(self._forbidden_capabilities(source))


class S4SensitiveLiteralTests(unittest.TestCase):
    """Active S4 policy files contain no credential or live-production material."""

    ROOT = Path(__file__).parents[3]
    ACTIVE_FILES = (
        ROOT / "ops" / "ha" / "s4_network_change_package.py",
        ROOT / "ops" / "ha" / "s4-network-change-package.py",
        ROOT / ".github" / "workflows" / "ha-s4-network-change-package.yml",
        ROOT / "docs" / "operations" / "runbook-ha-s4-network-repair.md",
        ROOT
        / "docs"
        / "superpowers"
        / "specs"
        / "2026-08-31-maestrovpn-s4-production-authorization-amendment.md",
    )
    ALLOWED_CLEANUP = 'rm -rf -- "$s4_ci_tmp"'

    @staticmethod
    def _python_slice_spans(text: str) -> tuple[tuple[int, int], ...]:
        try:
            tree = ast.parse(text)
        except SyntaxError:
            return ()

        lines = text.splitlines(keepends=True)
        line_starts: list[int] = []
        cursor = 0
        for line in lines:
            line_starts.append(cursor)
            cursor += len(line)

        def absolute_offset(line_number: int, utf8_column: int) -> int:
            line = lines[line_number - 1]
            prefix = line.encode("utf-8")[:utf8_column].decode("utf-8")
            return line_starts[line_number - 1] + len(prefix)

        spans = []
        for node in ast.walk(tree):
            if not isinstance(node, ast.Slice) or node.end_lineno is None:
                continue
            spans.append(
                (
                    absolute_offset(node.lineno, node.col_offset),
                    absolute_offset(node.end_lineno, node.end_col_offset),
                )
            )
        return tuple(spans)

    @classmethod
    def _ipv6_literals(cls, text: str) -> tuple[str, ...]:
        candidate_pattern = re.compile(
            r"(?<![0-9A-Za-z:])[0-9A-Fa-f:]*:[0-9A-Fa-f:]+(?![0-9A-Za-z:])"
        )
        python_slice_spans = cls._python_slice_spans(text)
        values = []
        for match in candidate_pattern.finditer(text):
            candidate = match.group(0)
            if any(
                start <= match.start() and match.end() <= end
                for start, end in python_slice_spans
            ):
                continue
            try:
                ipaddress.IPv6Address(candidate)
            except ipaddress.AddressValueError:
                continue
            values.append(candidate)
        return tuple(values)

    def test_ipv6_literal_scanner_only_exempts_real_python_slices(self) -> None:
        self.assertEqual((), self._ipv6_literals("options = argv[1::2]\n"))
        for text in ("endpoint = '1::2'\n", "[1::2]:443"):
            with self.subTest(text=text):
                self.assertIn("1::2", self._ipv6_literals(text))

    def test_sensitive_literal_scanner_rejects_quoted_credentials_and_urls(self) -> None:
        fixtures = (
            ('{"token":"opaque-secret"}', "credential-assignment"),
            ("endpoint='https://prod.example.com/api'", "hostname"),
            ("endpoint='https://prod.example.com:443/api'", "host-port"),
        )
        for text, expected in fixtures:
            with self.subTest(text=text):
                self.assertIn(expected, self._sensitive_kinds(text))

    @classmethod
    def _sensitive_kinds(cls, text: str) -> tuple[str, ...]:
        kinds: set[str] = set()
        lowered = text.casefold()
        if "github_pat_" in lowered:
            kinds.add("github-token")
        if "bearer " in lowered:
            kinds.add("bearer-token")
        if re.search(r"-----BEGIN [^-\r\n]*PRIVATE KEY-----", text, flags=re.IGNORECASE):
            kinds.add("private-key")
        if re.search(
            r"(?i)(?<![\w-])(?:api[_-]?key|token|password)[\"']?\s*(?:=|:)\s*[\"']?\S",
            text,
        ):
            kinds.add("credential-assignment")

        for candidate in re.findall(r"(?<![0-9])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![0-9])", text):
            octets = candidate.split(".")
            if all(0 <= int(octet) <= 255 for octet in octets):
                kinds.add("ipv4")
                break
        if cls._ipv6_literals(text):
            kinds.add("ipv6")
        if re.search(
            r"(?i)(?<![A-Za-z0-9.-])(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+"
            r"(?:com|net|org|ru|online|tech|cloud|io|dev|app|xyz|site|me|cc|su)(?![\w.-])",
            text,
        ):
            kinds.add("hostname")
        if re.search(r"(?i)(?<![A-Za-z0-9.-])[A-Za-z][A-Za-z0-9.-]*:[0-9]{1,5}(?![0-9])", text):
            kinds.add("host-port")
        if re.search(r"(?i)\b(?:customer|subscriber)[_-]?(?:id|uuid|data)\b", text):
            kinds.add("customer-data")
        if re.search(
            r"(?i)(?<![A-Za-z0-9._-])/(?:etc|var|usr|root|home|opt|run|srv|lib|boot|snap|tmp)(?:/[^\s`'\"<>]+)+",
            text,
        ) or re.search(
            r"(?i)\b[A-Z]:\\(?:Users|Windows|ProgramData|Program Files|etc)(?:\\[^\s`'\"<>]+)+",
            text,
        ):
            kinds.add("production-path")

        command_text = "\n".join(
            "" if line.strip() == cls.ALLOWED_CLEANUP else line
            for line in text.splitlines()
        )
        mutation_patterns = (
            r"(?i)\bsystemctl\b[^\r\n]{0,80}\b(?:start|stop|restart|reload|enable|disable|mask|unmask)\b",
            r"(?i)\bservice\s+\S+\s+(?:start|stop|restart|reload)\b",
            r"(?i)\bip\s+(?:addr(?:ess)?|link|route)\s+(?:add|del(?:ete)?|replace|flush|set)\b",
            r"(?i)\bnetworkctl\s+(?:reload|reconfigure)\b",
            r"(?i)\bnetplan\s+apply\b",
            r"(?i)\b(?:iptables|firewall-cmd)\s+\S+",
            r"(?i)\bnft\s+(?:add|delete|flush|insert|replace)\b",
            r"(?i)\bufw\s+(?:allow|deny|delete|enable|disable|reload|reset)\b",
            r"(?i)\brm\s+-rf\b",
        )
        if any(re.search(pattern, command_text) for pattern in mutation_patterns):
            kinds.add("mutation-command")
        return tuple(sorted(kinds))

    def test_active_files_exist_and_have_no_sensitive_literal(self) -> None:
        for path in self.ACTIVE_FILES:
            with self.subTest(path=path.as_posix()):
                self.assertTrue(path.is_file(), "active S4 policy file is missing")
                self.assertEqual(
                    (),
                    self._sensitive_kinds(path.read_text(encoding="utf-8")),
                    "active S4 policy file contains sensitive or live material",
                )

    def test_sensitive_literal_fixture_matrix_is_non_vacuous(self) -> None:
        fixtures = (
            "github_pat_fixture",
            "Bearer fixture",
            "-----BEGIN PRIVATE KEY-----",
            "password=<SECRET>",
            "synthetic endpoint 192.0.2.1",
            "synthetic endpoint 2001:db8::1",
            "synthetic.example.com",
            "synthetic-host:443",
            "customer_uuid=opaque",
            "/tmp/synthetic-command-sheet",
            "systemctl restart synthetic-unit",
            "rm -rf synthetic-root",
        )
        for fixture in fixtures:
            with self.subTest(fixture=fixture):
                self.assertTrue(self._sensitive_kinds(fixture))

    def test_reviewed_public_and_semantic_literals_remain_allowed(self) -> None:
        safe_values = (
            "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
            "maestro-ha-s4-network-inventory-v1",
            "REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY",
            "Android/TV remains immutable at 1.0.157.",
            "evaluation-time 2026-08-31T12:00:00Z",
            self.ALLOWED_CLEANUP,
        )
        for value in safe_values:
            with self.subTest(value=value):
                self.assertEqual((), self._sensitive_kinds(value))


class S4RunbookContractTests(unittest.TestCase):
    """Safety contract for the reviewed, semantic-only S4 operator runbook."""

    RUNBOOK = (
        Path(__file__).parents[3]
        / "docs"
        / "operations"
        / "runbook-ha-s4-network-repair.md"
    )

    def setUp(self) -> None:
        self.text = (
            self.RUNBOOK.read_text(encoding="utf-8")
            if self.RUNBOOK.is_file()
            else ""
        )
        self.normalized_text = " ".join(self.text.split())

    def _semantic_ids(self, name: str) -> tuple[str, ...]:
        marker = f"### `{name}`"
        self.assertIn(marker, self.text)
        start = self.text.index(marker) + len(marker)
        end = self.text.find("\n### ", start)
        if end == -1:
            end = self.text.find("\n## ", start)
        if end == -1:
            end = len(self.text)
        values = []
        for line in self.text[start:end].splitlines():
            if line.startswith("- `") and line.endswith("`"):
                values.append(line[3:-1])
        return tuple(values)

    def _section(self, heading: str) -> str:
        self.assertIn(heading, self.text)
        start = self.text.index(heading) + len(heading)
        end = self.text.find("\n## ", start)
        if end == -1:
            end = len(self.text)
        return " ".join(self.text[start:end].split())

    @staticmethod
    def _ipv6_literals(text: str) -> tuple[str, ...]:
        candidate_pattern = re.compile(
            r"(?<![0-9A-Za-z:])[0-9A-Fa-f:]*:[0-9A-Fa-f:]+(?![0-9A-Za-z:])"
        )
        values = []
        for match in candidate_pattern.finditer(text):
            candidate = match.group(0)
            try:
                ipaddress.IPv6Address(candidate)
            except ipaddress.AddressValueError:
                continue
            values.append(candidate)
        return tuple(values)

    @classmethod
    def _live_material_kinds(cls, text: str) -> tuple[str, ...]:
        kinds = set()
        if re.search(
            r"-----BEGIN [^-\r\n]*PRIVATE KEY-----", text, flags=re.IGNORECASE
        ):
            kinds.add("private-key")
        if re.search(
            r"(?i)(?<![\w-])(?:api[_-]?key|token|password)\s*(?:=|:)\s*\S",
            text,
        ):
            kinds.add("credential-assignment")
        mutation_patterns = (
            r"(?i)\bsystemctl\b[^\r\n]{0,80}\b(?:start|stop|restart|reload|enable|disable|mask|unmask)\b",
            r"(?i)\bservice\s+\S+\s+(?:start|stop|restart|reload)\b",
            r"(?i)\bip\s+(?:addr(?:ess)?|link|route)\s+(?:add|del(?:ete)?|replace|flush|set)\b",
            r"(?i)\bnetworkctl\s+(?:reload|reconfigure)\b",
            r"(?i)\bnetplan\s+apply\b",
            r"(?i)\bfirewall-cmd\s+\S+",
            r"(?i)\biptables\s+\S+",
            r"(?i)\bnft\s+(?:add|delete|flush|insert|replace)\b",
            r"(?i)\bufw\s+(?:allow|deny|delete|enable|disable|reload|reset)\b",
        )
        if any(re.search(pattern, text) for pattern in mutation_patterns):
            kinds.add("mutation-command")
        unix_path = re.search(
            r"(?i)(?<![A-Za-z0-9._-])/(?:etc|var|usr|root|home|opt|run|srv|lib|boot|snap|tmp)(?:/[^\s`'\"<>]+)+",
            text,
        )
        windows_path = re.search(
            r"(?i)\b[A-Z]:\\(?:Users|Windows|ProgramData|Program Files|etc)(?:\\[^\s`'\"<>]+)+",
            text,
        )
        if unix_path or windows_path:
            kinds.add("production-path")
        if cls._ipv6_literals(text):
            kinds.add("ipv6")
        return tuple(sorted(kinds))

    def test_runbook_exists_and_has_the_required_ordered_operator_gates(self) -> None:
        self.assertTrue(self.RUNBOOK.is_file(), "S4 operator runbook is missing")
        headings = (
            "## Repository authority and Task 6 gate",
            "## Evidence capture",
            "## Package generation",
            "## Package review",
            "## Gate 1 — independent trusted UTC and declaration",
            "## Gate 2 — independent trusted UTC before execution",
            "## Semantic change scope",
            "## Validation",
            "## Rollback",
            "## Evidence recording",
        )
        positions = []
        for heading in headings:
            self.assertIn(heading, self.text)
            positions.append(self.text.index(heading))
        self.assertEqual(positions, sorted(positions))

    def test_no_go_target_scope_command_and_exit_contract_are_explicit(self) -> None:
        required = (
            "PRODUCTION NO-GO",
            "`EVIDENCE_COMPLETE` package",
            "trusted UTC",
            "console recovery",
            "fresh S4 read-only preflight",
            "target: `s4`",
            "selected owner: `systemd-networkd`",
            "scope: `REMOVE_CONFLICTING_IFUPDOWN_PRIMARY_OWNERSHIP_ONLY`",
            "python ops/ha/s4-network-change-package.py package --inventory PATH --evaluation-time 2026-08-31T12:00:00Z --output PATH",
            "`0`: publishes a canonical `EVIDENCE_COMPLETE` package",
            "`2`: publishes a canonical `BLOCKED` package",
            "`3`: invalid, stale, unsafe, or system input; no final output is created",
            "`apply_supported: false`",
            "`mutation_authorized: false`",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, self.normalized_text)

    def test_no_go_requires_repository_authority_and_every_task6_gate(self) -> None:
        section = self._section("## Repository authority and Task 6 gate")
        required = (
            "S4 repository implementation is complete",
            "durable handoff is complete",
            "scoped local verification is GREEN",
            "canonical branch is pushed and its remote SHA equals the local SHA",
            "exact-SHA GitHub CI is GREEN for that SHA",
            "detached exact-SHA docs, manifest, and diff verification is GREEN",
            "dedicated S4 workflow and every required canonical-branch workflow are GREEN for that exact SHA",
            "independent review reports `0 Critical / 0 Important / 0 Minor`",
            "fresh bounded S4 raw capture was reviewed before canonical inventory derivation",
            "fresh unchanged inventory and exact package digest were reviewed",
            "newly generated `EVIDENCE_COMPLETE` package was reviewed",
            "every Task 6 package and stop gate is GREEN",
            "rollback is executable",
            "this standalone runbook does not permit Gate 1, declaration, Gate 2, or semantic execution",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, section)

    def test_all_semantic_id_sets_are_exact_and_ordered(self) -> None:
        expected = {
            "precheck_ids": (
                "inventory_reviewed",
                "networkd_working_owner",
                "ifupdown_conflict_confirmed",
                "management_vpn_health_green",
                "console_recovery_ready",
            ),
            "change_step_ids": (
                "backup_ifupdown_state",
                "remove_ifupdown_primary_declaration",
                "disable_ifupdown_boot_ownership",
                "preserve_systemd_networkd",
            ),
            "stop_gate_ids": (
                "trusted_utc_expired",
                "console_unavailable",
                "inventory_drift",
                "unexpected_network_owner",
                "prechange_health_degraded",
                "unexpected_command_result",
                "route_or_listener_loss",
                "fresh_management_session_failed",
            ),
            "validation_ids": (
                "single_primary_network_owner",
                "networkd_active_enabled",
                "default_route_preserved",
                "fresh_management_session_established",
                "vpn_units_listeners_preserved",
                "no_new_failed_units",
            ),
            "rollback_ids": (
                "restore_ifupdown_primary_declaration",
                "restore_ifupdown_unit_state",
                "repeat_s4_health_validation",
            ),
        }
        for name, identifiers in expected.items():
            with self.subTest(name=name):
                self.assertEqual(self._semantic_ids(name), identifiers)

    def test_raw_capture_and_digest_evidence_cannot_be_promoted_to_authority(self) -> None:
        required = (
            "protected bounded raw capture outside Git",
            "operator or owner reviews the raw capture before canonical inventory is derived",
            "`source_review_completed: true` may be set only after that review",
            "Raw capture bytes remain outside Git, package output, and ordinary reports.",
            "`inventory_sha256` is integrity evidence, not a secrecy mechanism.",
            "Do not invoke `build_manifest`, `pki_verify`, `deploy_node`, or `verify_backup` against digest-only evidence.",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, self.normalized_text)

    def test_two_utc_gates_and_declaration_envelope_are_independent(self) -> None:
        required = (
            "first independent trusted-UTC comparison immediately before the declaration activates standing authorization",
            "second independent trusted-UTC comparison immediately before execution",
            "fresh unchanged inventory",
            "no concurrent work",
            "independent second operator",
            "protected affected-file and unit-state backups",
            "before-state health capture",
            "exact S4 target",
            "package digest",
            "named operator",
            "bounded UTC window",
            "expected impact",
            "verified preconditions",
            "protected rollback-sheet identity",
            "all stop gates",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, self.normalized_text)

    def test_declaration_binds_backup_health_and_immediate_rollback_evidence(self) -> None:
        section = self._section(
            "## Gate 1 — independent trusted UTC and declaration"
        )
        required = (
            "protected affected-file backup identity",
            "protected unit-state backup identity",
            "before-state management, default-route, VPN-unit, VPN-listener, and failed-unit health evidence",
            "immediate rollback path from the protected rollback sheet",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, section)

    def test_gate2_utc_failure_terminates_window_and_requires_full_refresh(self) -> None:
        section = self._section(
            "## Gate 2 — independent trusted UTC before execution"
        )
        required = (
            "An expired, uncertain, unavailable, or mismatched second comparison terminates the maintenance window.",
            "A later attempt requires a new protected bounded raw capture",
            "a new operator or owner review of those raw bytes",
            "a fresh canonical inventory",
            "a newly generated and reviewed package",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, section)

    def test_change_validation_and_rollback_preserve_the_working_owner(self) -> None:
        required = (
            "backup and restore only the conflicting ifupdown declaration and unit state",
            "preserve active and enabled `systemd-networkd` ownership",
            "preserve the default route, management access, VPN units, and VPN listeners",
            "fresh independent management session before the original session is closed",
            "Immediate rollback",
            "owner drift",
            "route or listener loss",
            "unhealthy VPN units",
            "console loss",
            "unexpected command result",
            "failed fresh management session",
            "`ifup_unit_failed: true` and `networking_unit_failed: true` are reviewed conflict evidence",
            "management reachability, default-route presence, VPN-unit health, listener presence, and `no_new_failed_units`",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, self.normalized_text)

    def test_scope_exclusions_and_immutable_android_baseline_are_complete(self) -> None:
        exclusions = (
            "S1-S3",
            "DNS/CDN",
            "bots",
            "payments",
            "customer data",
            "VPN/firewall/listeners",
            "install",
            "restart",
            "reload",
            "reboot",
            "release",
            "signing",
            "OTA",
            "matrix",
            "PKI",
            "rqlite",
            "shadow traffic",
            "final cutover",
            "OLCRTC",
            "WDTT",
            "backups outside this narrow rollback",
        )
        self.assertIn("S4 only", self.normalized_text)
        self.assertIn("Android/TV remains immutable at `1.0.157`", self.normalized_text)
        for exclusion in exclusions:
            with self.subTest(exclusion=exclusion):
                self.assertIn(exclusion, self.normalized_text)

    def test_amendment_does_not_bypass_gates_or_embed_execution_authority(self) -> None:
        required = (
            "dated authority amendment removes only the additional chat-reply pause after every gate is GREEN and the full declaration is emitted",
            "does not bypass a gate",
            "does not embed execution authority in the package",
            "does not expand the scope",
        )
        for value in required:
            with self.subTest(value=value):
                self.assertIn(value, self.normalized_text)

    def test_runbook_has_no_affirmative_apply_or_readiness_claim(self) -> None:
        lowered = self.text.casefold()
        forbidden_affirmative_phrases = (
            "automatically apply",
            "automatic apply",
            "auto-apply",
            "apply automatically",
            "production ready",
            "production-ready",
            "production readiness confirmed",
            "ready for customer traffic",
            "customer traffic is ready",
            "customer traffic readiness confirmed",
            "permission to mutate",
            "authorized to mutate",
            "mutation is authorized",
            "approved for mutation",
            "approved to mutate production",
            "cleared to mutate production",
            "mutation is approved",
            "execution is approved",
            "mutation authority is granted",
            "apply_supported: true",
            "mutation_authorized: true",
        )
        for phrase in forbidden_affirmative_phrases:
            with self.subTest(phrase=phrase):
                self.assertNotIn(phrase, lowered)

    def test_checked_in_runbook_contains_no_live_command_sheet_material(self) -> None:
        forbidden_literals = (
            "systemctl start",
            "systemctl stop",
            "systemctl restart",
            "systemctl reload",
            "systemctl enable",
            "systemctl disable",
            "systemctl mask",
            "systemctl unmask",
            "networkctl reload",
            "networkctl reconfigure",
            "netplan apply",
            "ifdown ",
            "ifup ",
            "ip link set",
            "ip route add",
            "ip route delete",
            "ip route replace",
            "sed -i",
            "chmod ",
            "chown ",
            "rm -",
            "mv /",
            "cp /",
            "tee /",
            "sudo ",
            "ssh ",
            "curl ",
            "http://",
            "https://",
            "/etc/",
            "/var/",
            "/usr/",
            "/root/",
            "/home/",
            "/opt/",
            "/run/",
            "/srv/",
            "/lib/",
            "/boot/",
            "/snap/",
            "~/.ssh/",
            ":\\users\\",
            "BEGIN PRIVATE KEY",
            "Bearer ",
            "password=",
            "token=",
        )
        lowered = self.text.casefold()
        for literal in forbidden_literals:
            with self.subTest(literal=literal):
                self.assertNotIn(literal.casefold(), lowered)
        self.assertEqual(
            (),
            self._live_material_kinds(self.text),
            "runbook must contain semantic identifiers only",
        )
        self.assertIsNone(
            re.search(r"(?<![0-9A-Fa-f:])(?:\d{1,3}\.){3}\d{1,3}(?![0-9])", self.text),
            "runbook must not contain a live IPv4 literal",
        )
        self.assertEqual(
            (),
            self._ipv6_literals(self.text),
            "runbook must not contain a live IPv6 literal",
        )
        self.assertIsNone(
            re.search(
                r"(?<![\w./-])(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}(?![\w.-])",
                self.text,
            ),
            "runbook must not contain a live FQDN",
        )
        self.assertIsNone(
            re.search(
                r"(?<![\w./-])[A-Za-z][A-Za-z0-9.-]*:[0-9]{1,5}(?![0-9])",
                self.text,
            ),
            "runbook must not contain a live host:port endpoint",
        )
        self.assertIsNone(
            re.search(
                r"(?i)\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b",
                self.text,
            ),
            "runbook must not contain a UUID",
        )

    def test_compressed_ipv6_is_detected_without_utc_or_version_false_positives(self) -> None:
        self.assertEqual(
            ("2001:db8::1",),
            self._ipv6_literals("synthetic endpoint 2001:db8::1"),
        )
        for safe_text in (
            "2026-08-31T12:00:00Z",
            "UTC 12:00:00Z",
            "Android/TV 1.0.157",
        ):
            with self.subTest(safe_text=safe_text):
                self.assertEqual((), self._ipv6_literals(safe_text))

    def test_live_material_fixture_matrix_is_non_vacuous_and_precise(self) -> None:
        fixtures = (
            ("ipv6", "synthetic endpoint 2001:db8::1"),
            ("ipv6", "synthetic endpoint [2001:db8::1]:443"),
            ("private-key", "-----BEGIN PRIVATE KEY-----"),
            ("private-key", "-----BEGIN RSA PRIVATE KEY-----"),
            ("private-key", "-----BEGIN EC PRIVATE KEY-----"),
            ("credential-assignment", "api_key=<SECRET>"),
            ("credential-assignment", "token = <SECRET>"),
            ("credential-assignment", "password: <SECRET>"),
            ("mutation-command", "systemctl --now restart synthetic-unit"),
            ("mutation-command", "service synthetic-unit restart"),
            ("mutation-command", "ip addr flush dev synthetic0"),
            ("production-path", "/tmp/synthetic-command-sheet"),
        )
        for expected_kind, fixture in fixtures:
            with self.subTest(expected_kind=expected_kind, fixture=fixture):
                self.assertIn(expected_kind, self._live_material_kinds(fixture))

        for safe_text in (
            "The package is not approved for mutation.",
            "Android/TV remains immutable at 1.0.157.",
            "evaluation-time 2026-08-31T12:00:00Z",
            "--inventory PATH --output PATH",
        ):
            with self.subTest(safe_text=safe_text):
                self.assertEqual((), self._live_material_kinds(safe_text))
