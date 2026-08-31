import copy
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
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
        stderr = io.StringIO()
        fake_os = mock.Mock()
        fake_os.name = "nt"
        with (
            mock.patch.object(s4_network_change_package, "os", fake_os, create=True),
        ):
            code = s4_network_change_package.run(
                [
                    "package", "--inventory", "fixture-secret", "--evaluation-time",
                    "2026-08-31T12:00:00Z", "--output", "fixture-output",
                ],
                io.StringIO(),
                stderr,
            )
        self.assertEqual(code, 3)
        self.assertEqual(stderr.getvalue(), "s4-network-change-package:unsupported-platform\n")

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
            [], ["apply"], ["package"],
            ["package", "--inventory", "x", "--evaluation-time", "t", "--output", "y", "extra"],
            ["package", "--inventory", "x", "--inventory", "t", "--output", "y"],
            ["package", "--tooling-sha", "x", "--evaluation-time", "t", "--output", "y"],
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
            private = root / "private"
            private.write_bytes(canonical(valid_inventory()))
            os.chmod(private, 0o640)
            cases.append(private)
            linked = root / "linked"
            os.link(private, linked)
            os.chmod(private, 0o600)
            cases.append(linked)
            symlink = root / "symlink"
            symlink.symlink_to(private)
            cases.append(symlink)
            fifo = root / "fifo"
            os.mkfifo(fifo, 0o600)
            cases.append(fifo)

            for path in cases:
                with self.subTest(path=path.name), self.assertRaisesRegex(
                    S4ChangePackageError, r"^s4-network-change-package:inventory$"
                ):
                    s4_network_change_package.read_inventory(path, evaluation_time=EVALUATION_TIME)

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
