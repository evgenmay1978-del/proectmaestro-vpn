#!/usr/bin/env python3
"""Fail-closed policy checks for the repository-only DR workflow."""

from __future__ import annotations

import hashlib
import re
import sys
from pathlib import Path

from agent_payload_policy import main as assert_agent_payload_policy

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "ha-dr-restore-drill.yml"
CONTROL_WORKFLOW = ROOT / ".github" / "workflows" / "ha-control-plane.yml"

REQUIRED_STEPS = (
    "Check Go formatting and shell syntax",
    "Test backend",
    "Race-test backend",
    "Vet backend",
    "Test DR workflow policy",
    "Test durable backup adapter policy",
    "Test authenticated backup verification and tamper matrix",
    "Test fresh restore fencing parity and quorum",
    "Stop isolated rqlite cluster",
)

TASK10_PATHS = (
    "backend/internal/backuprpo/**",
    "backend/cmd/maestro-backup-worker/**",
    "deploy/ha/**",
    "deploy/maestro-backup.service",
    "deploy/maestro-backup.timer",
    "deploy/maestro-backup-onchange.path",
    "deploy/maestro-backup.sh",
    "ops/ha/test-backup-systemd-policy.py",
    "ops/ha/tests/test_backup_systemd_policy.py",
    "ops/ha/tests/test_dr_workflow_policy_contract.py",
)
TASK10_FORMAT_SCOPES = (
    "internal/backuprpo/*.go",
    "cmd/maestro-backup-worker/*.go",
)
TASK10_POLICY_COMMANDS = (
    "python -m unittest ops.ha.tests.test_backup_systemd_policy "
    "ops.ha.tests.test_dr_workflow_policy_contract",
    "python ops/ha/test-backup-systemd-policy.py",
    "bash -n ops/ha/backup-rqlite.sh deploy/maestro-backup.sh",
)
READ_ONLY_PERMISSION_KEYS = {
    "actions",
    "attestations",
    "checks",
    "contents",
    "deployments",
    "discussions",
    "id-token",
    "issues",
    "models",
    "packages",
    "pages",
    "pull-requests",
    "security-events",
    "statuses",
}


def fail(message: str) -> None:
    raise AssertionError(message)


def materialized_agent_allowlist(text: str) -> tuple[str, ...]:
    marker = "- name: Check materialized agent boundary"
    start = text.find(marker)
    if start < 0:
        fail("workflow lacks the materialized agent boundary gate")
    end = text.find("\n      - name:", start + len(marker))
    if end < 0:
        fail("materialized agent boundary gate is not bounded by a next step")

    paths = []
    for line in text[start:end].splitlines():
        match = re.match(
            r"\s*(internal/(?:applyagent|controlplane)/[^\s)]+\.go)",
            line,
        )
        if match is not None:
            paths.append(match.group(1))
    if not paths:
        fail("materialized agent boundary gate has no explicit allowlist")
    return tuple(paths)


def is_active(raw: str) -> bool:
    stripped = raw.strip()
    return bool(stripped) and not stripped.startswith("#")


def active_text(text: str) -> str:
    return "\n".join(raw.strip() for raw in text.splitlines() if is_active(raw))


def active_yaml_text(text: str) -> str:
    return "\n".join(raw for raw in text.splitlines() if is_active(raw))


def trigger_sequence(text: str, event: str, key: str) -> tuple[str, ...]:
    lines = text.splitlines()
    event_marker = f"  {event}:"
    event_indexes = [
        index for index, raw in enumerate(lines) if is_active(raw) and raw == event_marker
    ]
    if len(event_indexes) != 1:
        fail(f"workflow must define one active {event} trigger")
    start = event_indexes[0] + 1
    end = len(lines)
    for index in range(start, len(lines)):
        raw = lines[index]
        if is_active(raw) and len(raw) - len(raw.lstrip(" ")) <= 2:
            end = index
            break

    key_marker = f"    {key}:"
    key_indexes = [
        index
        for index in range(start, end)
        if is_active(lines[index]) and lines[index] == key_marker
    ]
    if len(key_indexes) != 1:
        fail(f"{event} must define one active {key} sequence")
    sequence_start = key_indexes[0] + 1
    sequence_end = end
    for index in range(sequence_start, end):
        raw = lines[index]
        if is_active(raw) and len(raw) - len(raw.lstrip(" ")) <= 4:
            sequence_end = index
            break

    values: list[str] = []
    for raw in lines[sequence_start:sequence_end]:
        if not is_active(raw):
            continue
        match = re.fullmatch(
            r"      - (?:'([^']+)'|([A-Za-z0-9_.][A-Za-z0-9_./*?-]*))",
            raw,
        )
        if match is None:
            fail(f"{event}.{key} contains a malformed active entry")
        values.append(match.group(1) or match.group(2))
    if not values or len(values) != len(set(values)):
        fail(f"{event}.{key} must be non-empty and unique")
    return tuple(values)


def named_step(text: str, name: str) -> str:
    matches = list(re.finditer(rf"(?m)^      - name: {re.escape(name)}\s*$", text))
    if len(matches) != 1:
        fail(f"workflow must define one active named step: {name}")
    match = matches[0]
    tail = text[match.end() :]
    next_step = re.search(r"(?m)^      - (?:name:|uses:)", tail)
    return tail[: next_step.start()] if next_step is not None else tail


def step_run_lines(text: str, name: str) -> tuple[str, ...]:
    step = named_step(text, name)
    lines = step.splitlines()
    matches = []
    for index, raw in enumerate(lines):
        if not is_active(raw):
            continue
        match = re.fullmatch(r"        run:\s*(\S.*)", raw)
        if match is not None:
            matches.append((index, match.group(1)))
    if len(matches) != 1:
        fail(f"workflow step must define one active run command: {name}")
    index, value = matches[0]
    if value not in {"|", "|-", "|+"}:
        if value.startswith(("|", ">")):
            fail(f"workflow step uses an unsupported run scalar: {name}")
        return (value,)

    body: list[str] = []
    for raw in lines[index + 1 :]:
        if not raw.strip():
            continue
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation <= 8:
            break
        if indentation < 10:
            fail(f"workflow step has malformed run indentation: {name}")
        body.append(raw[10:])
    if not body:
        fail(f"workflow step has an empty run body: {name}")
    return tuple(body)


def direct_mapping(
    lines: tuple[str, ...], indentation: int, label: str
) -> dict[str, str]:
    result: dict[str, str] = {}
    prefix = " " * indentation
    pattern = re.compile(
        rf"{re.escape(prefix)}([a-z][a-z0-9-]*):(?:\s*(.*))?"
    )
    for raw in lines:
        current = len(raw) - len(raw.lstrip(" "))
        if current != indentation:
            continue
        match = pattern.fullmatch(raw)
        if match is None or match.group(1) in result:
            fail(f"{label} contains a non-canonical or duplicate direct key")
        result[match.group(1)] = match.group(2) or ""
    return result


def job_source(text: str, name: str) -> str:
    matches = list(re.finditer(rf"(?m)^  {re.escape(name)}:\s*$", text))
    if len(matches) != 1:
        fail(f"workflow must define one active job: {name}")
    tail = text[matches[0].end() :]
    next_job = re.search(r"(?m)^  [a-z0-9][a-z0-9-]*:\s*$", tail)
    return tail[: next_job.start()] if next_job is not None else tail


def assert_step_metadata(
    text: str, name: str, expected: dict[str, str], label: str
) -> None:
    lines = structural_yaml_lines(named_step(text, name))
    actual = direct_mapping(lines, 8, f"{label} step {name}")
    if actual != expected:
        fail(f"{label} step metadata differs from the exact allowlist: {name}")


def expected_format_run_lines(label: str) -> tuple[str, ...]:
    slash = chr(92)
    common = (
        "gofmt -d " + slash,
        "  cmd/maestro-import/production_integration_test.go " + slash,
        "  internal/rqlite/*.go " + slash,
        "  internal/backuprpo/*.go " + slash,
        "  cmd/maestro-backup-worker/*.go |",
    )
    if label == "control":
        return (
            "set -euo pipefail",
            *common,
            "  tee /tmp/ha-gofmt.diff",
            "test ! -s /tmp/ha-gofmt.diff",
        )
    if label == "DR":
        return (
            "set -euo pipefail",
            "cd backend",
            *common,
            "  tee /tmp/ha-dr-gofmt.diff",
            "test ! -s /tmp/ha-dr-gofmt.diff",
            "cd ..",
            "bash -n ops/ha/backup-rqlite.sh ops/ha/restore-rqlite.sh",
            "bash -n ops/ha/ci-rqlite-cluster.sh ops/ha/test-ci-rqlite-cluster.sh",
            "bash -n ops/ha/test-backup-rqlite.sh ops/ha/test-restore-rqlite.sh",
        )
    fail(f"unknown workflow label: {label}")


def assert_workflow_seal(text: str, label: str) -> None:
    # Deliberately seal exact workflow text so any unmodeled step or run-body
    # change fails closed and requires an explicit policy review.
    expected_workflow_sha256 = {
        "control": "48e5d5d1a3f03990309959dede222bffd9b3ef00af1c00d9c3c9e3e162b8f64d",
        "DR": "5af1d6797e7e135dada0c3bb12c6f25f6ec64d8c70e4cd1a5460a20f838f3ce6",
    }
    expected = expected_workflow_sha256.get(label)
    actual = hashlib.sha256(text.encode("utf-8")).hexdigest()
    if expected is None or actual != expected:
        fail(f"{label} workflow differs from the exact reviewed source")


def assert_task10_contract(text: str, label: str, format_step_name: str) -> None:
    assert_workflow_seal(text, label)
    pull_request = set(trigger_sequence(text, "pull_request", "paths"))
    push = set(trigger_sequence(text, "push", "paths"))
    for path in TASK10_PATHS:
        if path not in pull_request or path not in push:
            fail(f"{label} workflow misses Task 10 path filter: {path}")

    backend_run = (
        "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
        "go test -count=1 ./..."
    )
    race_run = (
        "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
        "go test -count=1 -race ./..."
    )
    expected_step_metadata = {
        format_step_name: (
            {"working-directory": "backend", "run": "|"}
            if label == "control"
            else {"run": "|"}
        ),
        "Test durable backup adapter policy": {"run": "|"},
        "Test backend": {"working-directory": "backend", "run": backend_run},
        "Race-test backend": {"working-directory": "backend", "run": race_run},
        "Vet backend": {"working-directory": "backend", "run": "go vet ./..."},
    }
    if label == "control":
        expected_step_metadata["Test HA Python contracts"] = {"run": "|"}
    else:
        expected_step_metadata[
            "Test authenticated backup verification and tamper matrix"
        ] = {"run": "|"}
        expected_step_metadata["Test fresh restore fencing parity and quorum"] = {
            "run": "|"
        }
    for step_name, expected_metadata in expected_step_metadata.items():
        assert_step_metadata(text, step_name, expected_metadata, label)

    if step_run_lines(text, format_step_name) != expected_format_run_lines(label):
        fail(f"{label} Task 10 formatting gate differs from the fail-closed policy")

    policy_source = active_yaml_text(named_step(text, "Test durable backup adapter policy"))
    if "continue-on-error" in policy_source:
        fail(f"{label} durable backup policy may not continue on error")
    if step_run_lines(text, "Test durable backup adapter policy") != (
        "set -euo pipefail",
        *TASK10_POLICY_COMMANDS,
    ):
        fail(f"{label} durable backup policy commands are not exact and ordered")

    required_gate_commands = {
        "Test backend": (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 ./..."
        ),
        "Race-test backend": (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 -race ./..."
        ),
        "Vet backend": "go vet ./...",
    }
    for gate, command in required_gate_commands.items():
        if step_run_lines(text, gate) != (command,):
            fail(f"{label} workflow gate is not the exact command: {gate}")

    slash = chr(92)
    if label == "control":
        expected_proof_lines = (
            "set -euo pipefail",
            "python -m unittest " + slash,
            "  ops.ha.tests.test_verify_backup ops.ha.tests.test_restore_api " + slash,
            "  ops.ha.tests.test_backup_worker " + slash,
            "  ops.ha.tests.test_backup_worker_security " + slash,
            "  ops.ha.tests.test_backup_rqlite_shell_security " + slash,
            "  ops.ha.tests.test_backup_worker_identity_races " + slash,
            "  ops.ha.tests.test_inventory " + slash,
            "  ops.ha.tests.test_inventory_hardening " + slash,
            "  ops.ha.tests.test_inventory_identity_races",
            "python ops/ha/test-dr-workflow-policy.py",
            "bash ops/ha/test-backup-rqlite.sh",
            "bash ops/ha/test-restore-rqlite.sh",
        )
        if step_run_lines(text, "Test HA Python contracts") != expected_proof_lines:
            fail("control workflow proof step differs from the exact fail-closed body")
    else:
        expected_backup_lines = (
            "set -euo pipefail",
            "python -m unittest " + slash,
            "  ops.ha.tests.test_verify_backup " + slash,
            "  ops.ha.tests.test_restore_api " + slash,
            "  ops.ha.tests.test_backup_worker " + slash,
            "  ops.ha.tests.test_backup_worker_security " + slash,
            "  ops.ha.tests.test_backup_rqlite_shell_security " + slash,
            "  ops.ha.tests.test_backup_worker_identity_races",
            "bash ops/ha/test-backup-rqlite.sh",
        )
        if (
            step_run_lines(
                text, "Test authenticated backup verification and tamper matrix"
            )
            != expected_backup_lines
        ):
            fail("DR workflow backup proof step differs from the exact fail-closed body")
        if step_run_lines(text, "Test fresh restore fencing parity and quorum") != (
            "set -euo pipefail",
            "bash ops/ha/test-restore-rqlite.sh",
        ):
            fail("DR workflow lacks exact fresh restore proof")


def structural_yaml_lines(text: str) -> tuple[str, ...]:
    result: list[str] = []
    block_indent: int | None = None
    for raw in text.splitlines():
        if block_indent is not None:
            if not raw.strip():
                continue
            indentation = len(raw) - len(raw.lstrip(" "))
            if indentation > block_indent:
                continue
            block_indent = None
        if not is_active(raw):
            continue
        result.append(raw)
        if re.search(r":\s*[|>][+-]?\s*(?:#.*)?$", raw):
            block_indent = len(raw) - len(raw.lstrip(" "))
    return tuple(result)


def assert_read_only_permissions(text: str, label: str) -> None:
    lines = structural_yaml_lines(text)
    for raw in lines:
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation in {0, 4} and re.fullmatch(
            r"\s*[a-z][a-z0-9-]*:(?:\s.*)?", raw
        ) is None:
            fail(f"{label} workflow has a non-canonical root or job key")

    indexes = [
        index
        for index, raw in enumerate(lines)
        if re.match(r"^\s*permissions\s*:", raw)
    ]
    if not indexes:
        fail(f"{label} workflow has no permissions policy")
    root_maps: list[dict[str, str]] = []
    job_maps: dict[str, dict[str, str]] = {}
    for index in indexes:
        raw = lines[index]
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation not in {0, 4} or raw.strip() != "permissions:":
            fail(f"{label} workflow has non-block or misplaced permissions")
        mapping: dict[str, str] = {}
        for child in lines[index + 1 :]:
            child_indent = len(child) - len(child.lstrip(" "))
            if child_indent <= indentation:
                break
            if child_indent != indentation + 2:
                fail(f"{label} permissions contain a nested or malformed value")
            match = re.fullmatch(
                r"\s*([a-z][a-z0-9-]*):\s+(read|none)", child
            )
            if match is None or match.group(1) in mapping:
                fail(f"{label} permissions contain an invalid active value")
            if match.group(1) not in READ_ONLY_PERMISSION_KEYS:
                fail(f"{label} permissions contain an unknown key")
            mapping[match.group(1)] = match.group(2)
        if not mapping:
            fail(f"{label} permissions mapping is empty")
        if indentation == 0:
            root_maps.append(mapping)
            continue
        parent = None
        for candidate in reversed(lines[:index]):
            candidate_indent = len(candidate) - len(candidate.lstrip(" "))
            if candidate_indent <= 2:
                parent = candidate
                break
        match = re.fullmatch(r"  ([a-z][a-z0-9-]*):", parent or "")
        if match is None or match.group(1) in job_maps:
            fail(f"{label} job permissions are outside one unique job")
        job_maps[match.group(1)] = mapping
    if root_maps != [{"contents": "read"}]:
        fail(f"{label} workflow permissions must be contents: read only")


def top_level_mapping(text: str, key: str) -> dict[str, str]:
    lines = text.splitlines()
    markers = [
        index
        for index, raw in enumerate(lines)
        if is_active(raw) and raw.startswith(f"{key}:")
    ]
    if len(markers) != 1 or lines[markers[0]] != f"{key}:":
        fail(f"workflow must define one block-form top-level {key} mapping")
    result: dict[str, str] = {}
    for raw in lines[markers[0] + 1 :]:
        if not is_active(raw):
            continue
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation == 0:
            break
        match = re.fullmatch(r"  ([a-z][a-z0-9-]*):\s*(\S.*)", raw)
        if match is None or match.group(1) in result:
            fail(f"top-level {key} mapping contains an invalid active entry")
        result[match.group(1)] = match.group(2)
    if not result:
        fail(f"top-level {key} mapping is empty")
    return result


def assert_workflow_safety(text: str, label: str) -> None:
    assert_workflow_seal(text, label)
    assert_read_only_permissions(text, label)
    expected_top = {
        "name": (
            "HA control-plane checks"
            if label == "control"
            else "HA DR restore drill"
        ),
        "on": "",
        "permissions": "",
        "concurrency": "",
        "jobs": "",
    }
    top = direct_mapping(structural_yaml_lines(text), 0, f"{label} workflow")
    if top != expected_top:
        fail(f"{label} top-level workflow metadata differs from the exact allowlist")
    job_name = (
        "go-and-rqlite"
        if label == "control"
        else "authenticated-empty-cluster-restore"
    )
    expected_job = {
        "name": (
            "Go and isolated rqlite"
            if label == "control"
            else "Authenticated empty-cluster restore"
        ),
        "runs-on": "ubuntu-24.04",
        "timeout-minutes": "35" if label == "control" else "60",
        "steps": "",
    }
    job = direct_mapping(
        structural_yaml_lines(job_source(text, job_name)),
        4,
        f"{label} job {job_name}",
    )
    if job != expected_job:
        fail(f"{label} job metadata differs from the exact allowlist")
    active_yaml = active_yaml_text(text)
    if not re.search(r"(?m)^    runs-on: ubuntu-24\.04$", active_yaml):
        fail(f"{label} workflow must use ubuntu-24.04")
    timeouts = [
        int(value)
        for value in re.findall(r"(?m)^    timeout-minutes:\s*([0-9]+)\s*$", active_yaml)
    ]
    if len(timeouts) != 1 or not 1 <= timeouts[0] <= 60:
        fail(f"{label} workflow needs one bounded timeout of at most 60 minutes")
    concurrency = top_level_mapping(text, "concurrency")
    if set(concurrency) != {"group", "cancel-in-progress"} or concurrency[
        "cancel-in-progress"
    ] != "false":
        fail(f"{label} workflow concurrency must preserve an in-progress proof")
    uses = re.findall(
        r"(?m)^\s*-?\s*uses:\s*[^@\s]+@([^\s#]+)",
        "\n".join(structural_yaml_lines(text)),
    )
    if not uses or any(re.fullmatch(r"[0-9a-f]{40}", ref) is None for ref in uses):
        fail(f"{label} workflow actions must be pinned to full commits")
    lowered = active_yaml.lower()
    for forbidden in ("environment:", "secrets.", "upload-artifact", "self-hosted"):
        if forbidden in lowered:
            fail(f"{label} workflow has forbidden capability: {forbidden}")


def main() -> int:
    if not WORKFLOW.is_file():
        fail("dedicated DR workflow is absent")
    if not CONTROL_WORKFLOW.is_file():
        fail("HA control-plane workflow is absent")
    text = WORKFLOW.read_text(encoding="utf-8")
    control_text = CONTROL_WORKFLOW.read_text(encoding="utf-8")

    if materialized_agent_allowlist(text) != materialized_agent_allowlist(control_text):
        fail("materialized agent allowlist diverges from HA control-plane workflow")

    assert_task10_contract(text, "DR", "Check Go formatting and shell syntax")
    assert_task10_contract(control_text, "control", "Check Go formatting")
    if "codex/yandex-cdn-whitelist-task3-sync" not in trigger_sequence(
        text, "push", "branches"
    ):
        fail("DR workflow does not run on the canonical branch")
    assert_workflow_safety(text, "DR")
    assert_workflow_safety(control_text, "control")

    positions = []
    for name in REQUIRED_STEPS:
        marker = f"- name: {name}"
        position = text.find(marker)
        if position < 0:
            fail(f"missing named DR gate: {name}")
        positions.append(position)
    if positions != sorted(positions):
        fail("DR gates are out of order")

    cleanup = active_yaml_text(named_step(text, "Stop isolated rqlite cluster"))
    if "        if: ${{ always() && hashFiles('ops/ha/ci-rqlite-cluster.sh') != '' }}" not in cleanup:
        fail("DR workflow lacks unconditional bounded cluster cleanup")
    if step_run_lines(text, "Stop isolated rqlite cluster") != (
        "bash ops/ha/ci-rqlite-cluster.sh stop",
    ):
        fail("DR workflow cleanup command is not exact")

    if step_run_lines(text, "Test DR workflow policy") != (
        "python ops/ha/test-dr-workflow-policy.py",
    ):
        fail("workflow does not execute its own policy test")
    assert_agent_payload_policy()
    print("DR workflow policy passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as error:
        print(f"DR workflow policy failed: {error}", file=sys.stderr)
        raise SystemExit(1)
