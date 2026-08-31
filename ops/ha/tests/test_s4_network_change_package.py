import copy
import json
import unittest
from datetime import datetime, timedelta, timezone

from ops.ha.s4_network_change_package import (
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
    def assert_invalid(self, raw: bytes) -> None:
        with self.assertRaisesRegex(
            S4ChangePackageError,
            r"^s4-network-change-package:[a-z0-9-]+$",
        ) as raised:
            parse_inventory(raw, evaluation_time=EVALUATION_TIME)
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

        self.assert_invalid(reordered)
        self.assert_invalid(spaced)

    def test_duplicate_top_level_key_cannot_change_audited_inventory_meaning(self) -> None:
        raw = canonical(valid_inventory()).rstrip(b"\n")
        duplicate = raw[:-1] + b',"schema":"fixture-secret-duplicate"}\n'

        self.assert_invalid(duplicate)

    def test_floats_and_json_constants_cannot_enter_a_boolean_inventory(self) -> None:
        float_inventory = valid_inventory()
        float_inventory["networkd"] = dict(float_inventory["networkd"], active=1.0)
        self.assert_invalid(canonical(float_inventory))
        self.assert_invalid(canonical(valid_inventory()).replace(b"true", b"NaN", 1))
        self.assert_invalid(canonical(valid_inventory()).replace(b"true", b"Infinity", 1))

    def test_unknown_keys_at_each_level_cannot_expand_the_inventory_contract(self) -> None:
        paths = (None, "networkd", "ifupdown", "health", "console")
        for path in paths:
            with self.subTest(path=path):
                inventory = copy.deepcopy(valid_inventory())
                if path is None:
                    inventory["unexpected"] = True
                else:
                    inventory[path]["unexpected"] = True
                self.assert_invalid(canonical(inventory))

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
                self.assert_invalid(canonical(inventory))

    def test_wrong_scalar_and_container_types_cannot_be_coerced(self) -> None:
        cases = (
            ("schema", 7),
            ("captured_at_utc", None),
            ("networkd", []),
            ("health", "not-an-object"),
        )
        for key, replacement in cases:
            with self.subTest(key=key):
                inventory = valid_inventory()
                inventory[key] = replacement
                self.assert_invalid(canonical(inventory))

    def test_integer_zero_or_one_cannot_be_coerced_to_boolean_inventory_facts(self) -> None:
        for replacement in (0, 1):
            with self.subTest(replacement=replacement):
                inventory = valid_inventory()
                inventory["health"]["vpn_units_healthy"] = replacement
                self.assert_invalid(canonical(inventory))

    def test_non_ascii_or_escaped_ambiguity_cannot_be_accepted(self) -> None:
        non_ascii = canonical(valid_inventory()).replace(b'"s4"', '"s４"'.encode("utf-8"))
        escaped = canonical(valid_inventory()).replace(b'"s4"', b'"s\\u0034"')

        self.assert_invalid(non_ascii)
        self.assert_invalid(escaped)

    def test_trusted_time_accepts_only_the_closed_open_freshness_window(self) -> None:
        raw = canonical(valid_inventory())
        captured = datetime(2026, 8, 31, 11, 50, 0, tzinfo=timezone.utc)
        expires = datetime(2026, 8, 31, 12, 5, 0, tzinfo=timezone.utc)

        self.assertEqual(parse_inventory(raw, evaluation_time=captured), valid_inventory())
        self.assertEqual(
            parse_inventory(raw, evaluation_time=expires - timedelta(seconds=1)),
            valid_inventory(),
        )
        self.assert_invalid_with_time(raw, captured - timedelta(seconds=1))
        self.assert_invalid_with_time(raw, expires)

    def test_expiry_window_cannot_exceed_fifteen_minutes(self) -> None:
        inventory = valid_inventory()
        inventory["expires_at_utc"] = "2026-08-31T12:05:01Z"

        self.assert_invalid(canonical(inventory))

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
                self.assert_invalid(canonical(inventory))

    def assert_invalid_with_time(self, raw: bytes, evaluation_time: datetime) -> None:
        with self.assertRaisesRegex(
            S4ChangePackageError,
            r"^s4-network-change-package:[a-z0-9-]+$",
        ):
            parse_inventory(raw, evaluation_time=evaluation_time)


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
