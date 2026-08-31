"""Pure canonical S4 network-change inventory parsing and evaluation."""

import json
import re
from collections.abc import Mapping
from datetime import datetime, timedelta, timezone


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
