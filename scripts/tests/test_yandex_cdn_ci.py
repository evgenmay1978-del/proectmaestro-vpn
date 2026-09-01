from __future__ import annotations

import ast
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "yandex-cdn-release.yml"
GRADLE = REPO_ROOT / "app" / "build.gradle.kts"
BASH_RELEASE_VALIDATOR = REPO_ROOT / "ops" / "validate-yandex-cdn-release.sh"
POWERSHELL_RELEASE_VALIDATOR = REPO_ROOT / "ops" / "validate-yandex-cdn-release.ps1"
ROOT_CANARY_STEP = "Test exact-SHA root-only Linux canary contracts"
FAKE_GO_SOURCE = """from __future__ import annotations

import json
import os
import sys
from pathlib import Path

arguments = sys.argv[1:]
with Path(os.environ["FAKE_GO_LOG"]).open("a", encoding="utf-8") as handle:
    handle.write(json.dumps(arguments) + "\\n")
command = arguments[0] if arguments else ""
if command == "test":
    raise SystemExit(int(os.environ.get("FAKE_GO_TEST_EXIT", "0")))
if command == "run":
    raise SystemExit(int(os.environ.get("FAKE_GO_RUN_EXIT", "0")))
raise SystemExit(97)
"""

EXPECTED_JOBS = {
    "format-unit",
    "race-vet",
    "offline-replay",
    "rqlite-purge",
    "android-test-apk",
}
GO_PACKAGES = (
    "./internal/controlplane",
    "./internal/subgen",
    "./internal/shadowbilling",
    "./internal/whitelistapi/v1",
    "./internal/release",
    "./internal/whitelistready",
    "./internal/canary",
    "./cmd/maestro-release-validate",
    "./cmd/maestro-whitelist-ready",
    "./cmd/maestro-xray-cdn-canary",
    "./internal/testsupport/whitelistfixture",
)
GOFMT_SCOPE = (
    "internal/controlplane",
    "internal/subgen",
    "internal/shadowbilling",
    "internal/whitelistapi/v1",
    "internal/release",
    "internal/whitelistready",
    "internal/canary",
    "internal/testsupport/whitelistfixture",
    "cmd/maestro-release-validate",
    "cmd/maestro-whitelist-ready",
    "cmd/maestro-xray-cdn-canary",
    "internal/backuprpo",
    "cmd/maestro-backup-worker",
)
LEGACY_GO_FORMAT_DEBT = (
    "internal/controlplane/customers_test.go",
    "internal/controlplane/desired_payload.go",
    "internal/controlplane/migrations_test.go",
    "internal/controlplane/models.go",
    "internal/controlplane/outbox.go",
    "internal/controlplane/outbox_test.go",
    "internal/controlplane/restore_epoch.go",
    "internal/controlplane/restore_epoch_integration_test.go",
    "internal/controlplane/restore_epoch_test.go",
    "internal/controlplane/service.go",
)
CANONICAL_TASK7_FORMAT_FILES = (
    "internal/controlplane/migrations.go",
    "internal/controlplane/migrations_ordered_test.go",
    "internal/controlplane/schema_constraints_test.go",
)
WRAPPERS = (
    "yandex-get-body.sh",
    "yandex-active-stream.sh",
    "yandex-idle-cutoff.sh",
    "yandex-literal-edge.sh",
    "xray-counter-reset.sh",
    "billing-idempotency.sh",
    "duplicate-event-replay.sh",
    "subscription-escaping.sh",
    "edge-rotation.sh",
)
REQUIRED_PATH_FILTERS = (
    ".github/workflows/yandex-cdn-release.yml",
    "app/build.gradle.kts",
    "version.properties",
    "backend/go.mod",
    "backend/go.sum",
    "backend/cmd/maestro-release-validate/**",
    "backend/cmd/maestro-whitelist-ready/**",
    "backend/cmd/maestro-backup-worker/**",
    "backend/cmd/maestro-xray-cdn-canary/**",
    "backend/internal/backuprpo/**",
    "backend/internal/canary/**",
    "backend/internal/controlplane/**",
    "backend/internal/release/**",
    "backend/internal/shadowbilling/**",
    "backend/internal/subgen/**",
    "backend/internal/testsupport/whitelistfixture/**",
    "backend/internal/whitelistapi/**",
    "backend/internal/whitelistready/**",
    "deploy/ha/**",
    "deploy/maestro-backup.service",
    "deploy/maestro-backup.timer",
    "deploy/maestro-backup-onchange.path",
    "deploy/maestro-backup.sh",
    "docs/yandex-cdn-whitelist/**",
    "docs/superpowers/plans/2026-08-22-yandex-white-list-integration-readiness.md",
    "ops/ha/ci-rqlite-cluster.sh",
    "ops/ha/test-backup-systemd-policy.py",
    "ops/ha/tests/test_backup_systemd_policy.py",
    "ops/ha/tests/test_dr_workflow_policy_contract.py",
    "ops/ha/test-ci-rqlite-cluster.sh",
    "scripts/repro/**",
    "scripts/tests/test_yandex_cdn_*.py",
    "scripts/validate_yandex_cdn_docs.py",
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


def workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def gradle_text() -> str:
    return GRADLE.read_text(encoding="utf-8")


def is_active(raw: str) -> bool:
    stripped = raw.strip()
    return bool(stripped) and not stripped.startswith("#")


def active_step_text(source: str) -> str:
    return "\n".join(raw for raw in source.splitlines() if is_active(raw))


def trigger_paths(source: str, event: str) -> tuple[str, ...]:
    lines = source.splitlines()
    marker = f"  {event}:"
    indexes = [
        index for index, raw in enumerate(lines) if is_active(raw) and raw == marker
    ]
    if len(indexes) != 1:
        return ()
    start = indexes[0] + 1
    end = len(lines)
    for index in range(start, len(lines)):
        raw = lines[index]
        if is_active(raw) and len(raw) - len(raw.lstrip(" ")) <= 2:
            end = index
            break
    path_indexes = [
        index
        for index in range(start, end)
        if is_active(lines[index]) and lines[index] == "    paths:"
    ]
    if len(path_indexes) != 1:
        return ()
    sequence_start = path_indexes[0] + 1
    sequence_end = end
    for index in range(sequence_start, end):
        raw = lines[index]
        if is_active(raw) and len(raw) - len(raw.lstrip(" ")) <= 4:
            sequence_end = index
            break
    values = []
    for raw in lines[sequence_start:sequence_end]:
        if not is_active(raw):
            continue
        match = re.fullmatch(r"      - '([^']+)'", raw)
        if match is None:
            return ()
        values.append(match.group(1))
    if not values or len(values) != len(set(values)):
        return ()
    return tuple(values)


def top_level_permissions(source: str) -> dict[str, str]:
    lines = source.splitlines()
    indexes = [
        index
        for index, raw in enumerate(lines)
        if is_active(raw) and raw.startswith("permissions:")
    ]
    if len(indexes) != 1 or lines[indexes[0]] != "permissions:":
        return {}
    result: dict[str, str] = {}
    for raw in lines[indexes[0] + 1 :]:
        if not is_active(raw):
            continue
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation == 0:
            break
        match = re.fullmatch(r"  ([a-z][a-z0-9-]*):\s*(\S.*)", raw)
        if match is None or match.group(1) in result:
            return {}
        result[match.group(1)] = match.group(2)
    return result


def job_source(source: str, name: str) -> str:
    marker = f"  {name}:\n"
    if marker not in source:
        return ""
    tail = source.split(marker, 1)[1]
    next_job = re.search(r"(?m)^  [a-z0-9][a-z0-9-]*:\n", tail)
    block = tail[: next_job.start()] if next_job else tail
    return "\n".join(raw for raw in block.splitlines() if is_active(raw))


def named_step_source(source: str, name: str) -> str:
    matches = list(re.finditer(rf"(?m)^      - name: {re.escape(name)}\s*$", source))
    if len(matches) != 1:
        raise AssertionError(f"workflow must define one active named step: {name}")
    tail = source[matches[0].end() :]
    next_step = re.search(r"(?m)^      - (?:name:|uses:)", tail)
    return tail[: next_step.start()] if next_step is not None else tail


def step_run_lines(source: str, name: str) -> tuple[str, ...]:
    step = named_step_source(source, name)
    lines = step.splitlines()
    matches = []
    for index, raw in enumerate(lines):
        if not is_active(raw):
            continue
        match = re.fullmatch(r"        run:\s*(\S.*)", raw)
        if match is not None:
            matches.append((index, match.group(1)))
    if len(matches) != 1:
        raise AssertionError(f"step must define one active run command: {name}")
    index, value = matches[0]
    if value not in {"|", "|-", "|+"}:
        if value.startswith(("|", ">")):
            raise AssertionError(f"step uses unsupported run scalar: {name}")
        return (value,)
    body: list[str] = []
    for raw in lines[index + 1 :]:
        if not raw.strip():
            continue
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation <= 8:
            break
        if indentation < 10:
            raise AssertionError(f"step has malformed run indentation: {name}")
        body.append(raw[10:])
    if not body:
        raise AssertionError(f"step has empty run body: {name}")
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
            raise AssertionError(
                f"{label} contains a non-canonical or duplicate direct key"
            )
        result[match.group(1)] = match.group(2) or ""
    return result


def nested_block(
    lines: tuple[str, ...], parent_indent: int, key: str, label: str
) -> tuple[str, ...]:
    marker = f"{' ' * parent_indent}{key}:"
    indexes = [index for index, raw in enumerate(lines) if raw == marker]
    if len(indexes) != 1:
        raise AssertionError(f"{label} must define one exact {key} block")
    result: list[str] = []
    for raw in lines[indexes[0] + 1 :]:
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation <= parent_indent:
            break
        result.append(raw)
    if not result:
        raise AssertionError(f"{label} has an empty {key} block")
    return tuple(result)


def assert_step_metadata(
    source: str, name: str, expected: dict[str, str]
) -> tuple[str, ...]:
    lines = structural_yaml_lines(named_step_source(source, name))
    actual = direct_mapping(lines, 8, f"step {name}")
    if actual != expected:
        raise AssertionError(f"step metadata differs from the exact allowlist: {name}")
    return lines


def structural_yaml_lines(source: str) -> tuple[str, ...]:
    result: list[str] = []
    block_indent: int | None = None
    for raw in source.splitlines():
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


def assert_read_only_permissions(source: str) -> None:
    lines = structural_yaml_lines(source)
    for raw in lines:
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation in {0, 4} and re.fullmatch(
            r"\s*[a-z][a-z0-9-]*:(?:\s.*)?", raw
        ) is None:
            raise AssertionError("workflow has a non-canonical root or job key")

    indexes = [
        index
        for index, raw in enumerate(lines)
        if re.match(r"^\s*permissions\s*:", raw)
    ]
    if not indexes:
        raise AssertionError("workflow has no permissions policy")
    root_maps: list[dict[str, str]] = []
    job_maps: dict[str, dict[str, str]] = {}
    for index in indexes:
        raw = lines[index]
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation not in {0, 4} or raw.strip() != "permissions:":
            raise AssertionError("workflow has non-block or misplaced permissions")
        mapping: dict[str, str] = {}
        for child in lines[index + 1 :]:
            child_indent = len(child) - len(child.lstrip(" "))
            if child_indent <= indentation:
                break
            if child_indent != indentation + 2:
                raise AssertionError("permissions contain a nested or malformed value")
            match = re.fullmatch(
                r"\s*([a-z][a-z0-9-]*):\s+(read|none)", child
            )
            if match is None or match.group(1) in mapping:
                raise AssertionError("permissions contain an invalid active value")
            if match.group(1) not in READ_ONLY_PERMISSION_KEYS:
                raise AssertionError("permissions contain an unknown key")
            mapping[match.group(1)] = match.group(2)
        if not mapping:
            raise AssertionError("permissions mapping is empty")
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
            raise AssertionError("job permissions are outside one unique job")
        job_maps[match.group(1)] = mapping
    if root_maps != [{"contents": "read"}]:
        raise AssertionError("workflow root permissions must be contents: read only")


# Deliberately seal exact workflow text so unmodeled steps and run-body changes
# cannot bypass the readable semantic allowlists below.
EXPECTED_WORKFLOW_SHA256 = (
    "bdf965479dafb321c988ae46828483b6df07ba9a65731b073f75bf0f0d8e1893"
)


def raw_block_scalar(
    source: str, name: str, key: str, parent_indent: int
) -> tuple[str, ...]:
    lines = named_step_source(source, name).splitlines()
    marker = f"{' ' * parent_indent}{key}: |"
    indexes = [index for index, raw in enumerate(lines) if raw == marker]
    if len(indexes) != 1:
        raise AssertionError(f"step {name} must define one exact {key} block")
    body: list[str] = []
    for raw in lines[indexes[0] + 1 :]:
        indentation = len(raw) - len(raw.lstrip(" "))
        if indentation <= parent_indent:
            break
        body.append(raw)
    if not body:
        raise AssertionError(f"step {name} has an empty {key} block")
    return tuple(body)


def assert_execution_metadata_policy(source: str) -> None:
    actual_sha256 = hashlib.sha256(source.encode("utf-8")).hexdigest()
    if actual_sha256 != EXPECTED_WORKFLOW_SHA256:
        raise AssertionError("workflow differs from the exact reviewed source")
    if raw_block_scalar(
        source, "Upload Task 7 APK artifact only", "path", 10
    ) != (
        "            ${{ steps.apk.outputs.apk_path }}",
        "            ${{ runner.temp }}/task7-apk.sha256",
    ):
        raise AssertionError("artifact upload paths are not exact")
    top = direct_mapping(structural_yaml_lines(source), 0, "workflow")
    if top != {
        "name": "Yandex CDN isolated release checks",
        "on": "",
        "permissions": "",
        "jobs": "",
    }:
        raise AssertionError("workflow metadata differs from the exact allowlist")

    expected_jobs = {
        "format-unit": {
            "runs-on": "ubuntu-latest",
            "timeout-minutes": "20",
            "steps": "",
        },
        "race-vet": {
            "needs": "format-unit",
            "runs-on": "ubuntu-latest",
            "timeout-minutes": "25",
            "steps": "",
        },
        "offline-replay": {
            "needs": "format-unit",
            "runs-on": "ubuntu-latest",
            "timeout-minutes": "15",
            "steps": "",
        },
        "rqlite-purge": {
            "needs": "format-unit",
            "runs-on": "ubuntu-latest",
            "timeout-minutes": "20",
            "steps": "",
        },
        "android-test-apk": {
            "needs": "",
            "runs-on": "ubuntu-latest",
            "timeout-minutes": "60",
            "permissions": "",
            "steps": "",
        },
    }
    job_lines: dict[str, tuple[str, ...]] = {}
    for job_name, expected in expected_jobs.items():
        lines = structural_yaml_lines(job_source(source, job_name))
        job_lines[job_name] = lines
        actual = direct_mapping(lines, 4, f"job {job_name}")
        if actual != expected:
            raise AssertionError(
                f"job metadata differs from the exact allowlist: {job_name}"
            )

    android_lines = job_lines["android-test-apk"]
    if nested_block(android_lines, 4, "needs", "android-test-apk") != (
        "      - format-unit",
        "      - race-vet",
        "      - offline-replay",
        "      - rqlite-purge",
    ):
        raise AssertionError("android-test-apk needs list is not exact")
    if nested_block(android_lines, 4, "permissions", "android-test-apk") != (
        "      contents: read",
        "      actions: read",
    ):
        raise AssertionError("android-test-apk permissions are not exact")

    offline_lines = assert_step_metadata(
        source,
        "Replay all nine offline fixture suites",
        {"env": "", "run": "|"},
    )
    if nested_block(
        offline_lines, 8, "env", "offline replay step"
    ) != (
        '          GOPROXY: "off"',
        '          GOSUMDB: "off"',
    ):
        raise AssertionError("offline replay environment is not exact")

    assert_step_metadata(
        source,
        "Build separately versioned Task 7 APK",
        {"run": "|"},
    )
    assert_step_metadata(
        source,
        "Verify Task 7 APK metadata and signer",
        {"id": "apk", "run": "|"},
    )
    upload_lines = assert_step_metadata(
        source,
        "Upload Task 7 APK artifact only",
        {
            "uses": (
                "actions/upload-artifact@"
                "ea165f8d65b6e75b540449e92b4886f43607fa02 # v4"
            ),
            "with": "",
        },
    )
    if nested_block(upload_lines, 8, "with", "artifact upload step") != (
        "          name: maestrovpn-task7-test-${{ github.sha }}",
        "          path: |",
        "          if-no-files-found: error",
        "          retention-days: 7",
    ):
        raise AssertionError("artifact upload metadata is not exact")


def payload_guard(node: ast.stmt, key: str, expected: str) -> bool:
    if not isinstance(node, ast.If) or not isinstance(node.test, ast.Compare):
        return False
    test = node.test
    if len(test.ops) != 1 or not isinstance(test.ops[0], ast.NotEq):
        return False
    if len(test.comparators) != 1:
        return False
    left = test.left
    if not (
        isinstance(left, ast.Subscript)
        and isinstance(left.value, ast.Name)
        and left.value.id == "payload"
        and isinstance(left.slice, ast.Constant)
        and left.slice.value == key
        and isinstance(test.comparators[0], ast.Constant)
        and test.comparators[0].value == expected
    ):
        return False
    return any(
        isinstance(statement, ast.Raise)
        and isinstance(statement.exc, ast.Call)
        and isinstance(statement.exc.func, ast.Name)
        and statement.exc.func.id == "SystemExit"
        for statement in node.body
    )


def assert_offline_replay_policy(source: str) -> None:
    assert_execution_metadata_policy(source)
    expected = (
        "set -euo pipefail",
        "scripts=(",
        "  scripts/repro/_run-white-list-suite.sh",
        "  scripts/repro/yandex-get-body.sh",
        "  scripts/repro/yandex-active-stream.sh",
        "  scripts/repro/yandex-idle-cutoff.sh",
        "  scripts/repro/yandex-literal-edge.sh",
        "  scripts/repro/xray-counter-reset.sh",
        "  scripts/repro/billing-idempotency.sh",
        "  scripts/repro/duplicate-event-replay.sh",
        "  scripts/repro/subscription-escaping.sh",
        "  scripts/repro/edge-rotation.sh",
        ")",
        'for script in "${scripts[@]}"; do',
        '  bash -n "$script"',
        "done",
        'for script in "${scripts[@]:1}"; do',
        '  output="$("$script")"',
        "  printf '%s\\n' \"$output\"",
        "  printf '%s' \"$output\" | python -c '",
        "import json",
        "import sys",
        "payload = json.loads(sys.stdin.read())",
        'if payload["harness_status"] != "PASS":',
        '    raise SystemExit("harness_status_not_pass")',
        'if payload["release_readiness"] != "NO_GO":',
        '    raise SystemExit("release_readiness_not_no_go")',
        "'",
        "done",
    )
    lines = step_run_lines(source, "Replay all nine offline fixture suites")
    if lines != expected:
        raise AssertionError("offline replay step differs from the exact fail-closed body")
    start = "  printf '%s' \"$output\" | python -c '"
    starts = [index for index, line in enumerate(lines) if line == start]
    if len(starts) != 1:
        raise AssertionError("offline replay must execute one exact Python JSON guard")
    start_index = starts[0]
    try:
        end_index = lines.index("'", start_index + 1)
    except ValueError as error:
        raise AssertionError("offline replay Python guard is not terminated") from error
    program = ast.parse("\n".join(lines[start_index + 1 : end_index]))
    payload_assignments = [
        index
        for index, node in enumerate(program.body)
        if isinstance(node, ast.Assign)
        and any(isinstance(target, ast.Name) and target.id == "payload" for target in node.targets)
        and isinstance(node.value, ast.Call)
        and isinstance(node.value.func, ast.Attribute)
        and isinstance(node.value.func.value, ast.Name)
        and node.value.func.value.id == "json"
        and node.value.func.attr == "loads"
    ]
    guards = {
        ("harness_status", "PASS"): [
            index
            for index, node in enumerate(program.body)
            if payload_guard(node, "harness_status", "PASS")
        ],
        ("release_readiness", "NO_GO"): [
            index
            for index, node in enumerate(program.body)
            if payload_guard(node, "release_readiness", "NO_GO")
        ],
    }
    if len(payload_assignments) != 1 or any(len(value) != 1 for value in guards.values()):
        raise AssertionError("offline replay lacks exact parsed PASS/NO_GO guards")
    guard_indexes = [value[0] for value in guards.values()]
    if payload_assignments[0] >= min(guard_indexes):
        raise AssertionError("offline replay guards run before JSON payload parsing")


def split_heredocs(
    lines: tuple[str, ...],
) -> tuple[tuple[str, ...], dict[str, tuple[str, ...]]]:
    shell: list[str] = []
    bodies: dict[str, tuple[str, ...]] = {}
    index = 0
    while index < len(lines):
        line = lines[index]
        match = re.search(r"<<'([A-Z][A-Z0-9_]*)'$", line)
        shell.append(line)
        if match is None:
            index += 1
            continue
        marker = match.group(1)
        end = index + 1
        while end < len(lines) and lines[end] != marker:
            end += 1
        if end >= len(lines) or line in bodies:
            raise AssertionError("unterminated or duplicate heredoc")
        bodies[line] = tuple(lines[index + 1 : end])
        index = end + 1
    return tuple(shell), bodies


def signer_guard(node: ast.stmt) -> bool:
    if not isinstance(node, ast.If) or not isinstance(node.test, ast.Compare):
        return False
    test = node.test
    return bool(
        len(test.ops) == 1
        and isinstance(test.ops[0], ast.NotIn)
        and isinstance(test.left, ast.Constant)
        and test.left.value == "CN=Android Debug"
        and len(test.comparators) == 1
        and isinstance(test.comparators[0], ast.Name)
        and test.comparators[0].id == "report"
        and any(
            isinstance(statement, ast.Raise)
            and isinstance(statement.exc, ast.Call)
            and isinstance(statement.exc.func, ast.Name)
            and statement.exc.func.id == "SystemExit"
            for statement in node.body
        )
    )


def assert_android_artifact_policy(source: str) -> None:
    assert_execution_metadata_policy(source)
    slash = chr(92)
    expected_build = (
        "set -euo pipefail",
        "chmod +x gradlew",
        "./gradlew " + slash,
        "  :app:assembleOtherDebug " + slash,
        "  -PmaestroTask7TestVersionName=1.0.158-task7-test " + slash,
        "  -PmaestroTask7TestVersionCode=1015800 " + slash,
        "  --stacktrace " + slash,
        "  --no-daemon",
    )
    if step_run_lines(source, "Build separately versioned Task 7 APK") != expected_build:
        raise AssertionError("Task 7 APK build command is not exact")

    verify_lines = step_run_lines(source, "Verify Task 7 APK metadata and signer")
    expected_verify = (
        "set -euo pipefail",
        "mapfile -t apks < <(find app/build/outputs/apk/other/debug "
        "-maxdepth 1 -type f -name '*.apk' -print)",
        'if [ "${#apks[@]}" -ne 1 ]; then',
        '  echo "::error::Expected exactly one Task 7 APK." >&2',
        "  exit 1",
        "fi",
        'apk="${apks[0]}"',
        'test "$(apkanalyzer manifest application-id "$apk")" = \'com.maestrovpn.tv\'',
        'test "$(apkanalyzer manifest version-name "$apk")" = '
        "'1.0.158-task7-test'",
        'test "$(apkanalyzer manifest version-code "$apk")" = \'1015800\'',
        'test "$(apkanalyzer manifest debuggable "$apk")" = \'false\'',
        'apksigner="${ANDROID_SDK_ROOT}/build-tools/36.0.0/apksigner"',
        'test -x "$apksigner"',
        'signer_report="${RUNNER_TEMP}/task7-apk-signer.txt"',
        '"$apksigner" verify --verbose --print-certs "$apk" | tee "$signer_report"',
        "python - \"$signer_report\" <<'PY'",
        "import pathlib",
        "import sys",
        'report = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")',
        'if "CN=Android Debug" not in report:',
        '    raise SystemExit("task7_apk_not_debug_signed")',
        "PY",
        'sha256sum "$apk" | tee "${RUNNER_TEMP}/task7-apk.sha256"',
        'echo "apk_path=${apk}" >> "$GITHUB_OUTPUT"',
    )
    if verify_lines != expected_verify:
        raise AssertionError(
            "Task 7 APK verification differs from the exact fail-closed body"
        )

    shell, heredocs = split_heredocs(verify_lines)
    heredoc_start = "python - \"$signer_report\" <<'PY'"
    required_shell = (
        "set -euo pipefail",
        'test "$(apkanalyzer manifest application-id "$apk")" = \'com.maestrovpn.tv\'',
        'test "$(apkanalyzer manifest version-name "$apk")" = \'1.0.158-task7-test\'',
        'test "$(apkanalyzer manifest version-code "$apk")" = \'1015800\'',
        'test "$(apkanalyzer manifest debuggable "$apk")" = \'false\'',
        'test -x "$apksigner"',
        '"$apksigner" verify --verbose --print-certs "$apk" | tee "$signer_report"',
        heredoc_start,
        'sha256sum "$apk" | tee "${RUNNER_TEMP}/task7-apk.sha256"',
        'echo "apk_path=${apk}" >> "$GITHUB_OUTPUT"',
    )
    positions = []
    for command in required_shell:
        matches = [index for index, line in enumerate(shell) if line == command]
        if len(matches) != 1:
            raise AssertionError(f"Task 7 APK verification misses exact command: {command}")
        positions.append(matches[0])
    if positions != sorted(positions):
        raise AssertionError("Task 7 APK verification commands are out of order")
    if set(heredocs) != {heredoc_start}:
        raise AssertionError("Task 7 signer verification must use one exact heredoc")
    signer_program = ast.parse("\n".join(heredocs[heredoc_start]))
    if sum(signer_guard(node) for node in signer_program.body) != 1:
        raise AssertionError("Task 7 signer report lacks an executed debug-signer guard")


def bash_array(source: str, name: str) -> tuple[str, ...]:
    lines = [line.strip() for line in source.splitlines()]
    try:
        start = lines.index(f"{name}=(")
    except ValueError:
        return ()
    values: list[str] = []
    for line in lines[start + 1 :]:
        if line == ")":
            return tuple(values)
        if line:
            values.append(line)
    return ()


def powershell_array(source: str, name: str) -> tuple[str, ...]:
    lines = [line.strip() for line in source.splitlines()]
    try:
        start = lines.index(f"${name} = @(")
    except ValueError:
        return ()
    values: list[str] = []
    for line in lines[start + 1 :]:
        if line == ")":
            return tuple(values)
        match = re.fullmatch(r"'([^']+)'", line)
        if match is None:
            return ()
        values.append(match.group(1))
    return ()


def write_fake_go(root: Path) -> Path:
    if os.name == "nt":
        driver = root / "fake_go.py"
        driver.write_text(FAKE_GO_SOURCE, encoding="utf-8")
        shim = root / "fake-go.cmd"
        shim.write_text(
            f'@echo off\r\n"{sys.executable}" "{driver}" %*\r\n',
            encoding="utf-8",
        )
        return shim
    shim = root / "fake-go"
    shim.write_text(f"#!{sys.executable}\n{FAKE_GO_SOURCE}", encoding="utf-8")
    shim.chmod(0o755)
    return shim


def fake_go_calls(log_path: Path) -> list[list[str]]:
    return [
        json.loads(line)
        for line in log_path.read_text(encoding="utf-8").splitlines()
    ]


def continued_command_arguments(
    lines: tuple[str, ...], command: str
) -> tuple[str, ...]:
    try:
        start = lines.index(f"{command} \\")
    except ValueError:
        return ()
    values: list[str] = []
    for line in lines[start + 1 :]:
        value = line.strip()
        continued = value.endswith("\\")
        if continued:
            value = value[:-1].rstrip()
        if not value:
            return ()
        values.append(value)
        if not continued:
            return tuple(values)
    return ()


class WorkflowSafetyContractTest(unittest.TestCase):
    def test_triggers_cover_every_task7_input_on_push_and_pull_request(self) -> None:
        source = workflow_text()
        pull_request = trigger_paths(source, "pull_request")
        push = trigger_paths(source, "push")
        for path in REQUIRED_PATH_FILTERS:
            with self.subTest(path=path):
                self.assertIn(path, pull_request)
                self.assertIn(path, push)

    def test_commented_task10_path_is_not_an_active_trigger(self) -> None:
        source = workflow_text()
        active = "      - 'backend/internal/backuprpo/**'"
        commented = "      # - 'backend/internal/backuprpo/**'"
        mutated = source.replace(active, commented, 1)
        self.assertNotEqual(source, mutated)
        self.assertNotIn(
            "backend/internal/backuprpo/**",
            trigger_paths(mutated, "pull_request"),
        )

    def test_top_level_permissions_reject_write_all_even_with_run_text(self) -> None:
        source = workflow_text()
        mutated = source.replace(
            "permissions:\n  contents: read\n",
            "permissions: write-all\n",
            1,
        )
        mutated = mutated.replace(
            "jobs:\n",
            "jobs:\n  # permissions:\n  #   contents: read\n",
            1,
        )
        self.assertNotEqual({"contents": "read"}, top_level_permissions(mutated))

    def test_commented_no_go_assertion_is_not_active(self) -> None:
        source = job_source(workflow_text(), "offline-replay")
        active = 'if payload["release_readiness"] != "NO_GO":'
        commented = '# if payload["release_readiness"] != "NO_GO":'
        mutated = source.replace(active, commented, 1)
        self.assertNotEqual(source, mutated)
        self.assertNotIn(active, active_step_text(mutated))

    def test_job_write_all_is_rejected(self) -> None:
        source = workflow_text()
        mutated = source.replace(
            "    runs-on: ubuntu-latest\n",
            "    runs-on: ubuntu-latest\n    permissions: write-all\n",
            1,
        )
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_read_only_permissions(mutated)

    def test_job_inline_write_map_is_rejected(self) -> None:
        source = workflow_text()
        mutated = source.replace(
            "    runs-on: ubuntu-latest\n",
            "    runs-on: ubuntu-latest\n"
            "    permissions: {contents: write}\n",
            1,
        )
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_read_only_permissions(mutated)

    def test_permissions_text_inside_run_block_is_not_yaml(self) -> None:
        source = workflow_text()
        mutated = source.replace(
            "          set -euo pipefail\n",
            "          set -euo pipefail\n"
            "          printf '%s\\n' 'permissions: write-all' >/dev/null\n",
            1,
        )
        self.assertNotEqual(source, mutated)
        assert_read_only_permissions(mutated)

    def test_quoted_job_permissions_are_rejected(self) -> None:
        source = workflow_text()
        variants = (
            '"permissions": write-all',
            "'permissions': write-all",
            '"\\u0070ermissions": write-all',
        )
        for variant in variants:
            with self.subTest(variant=variant):
                mutated = source.replace(
                    "    runs-on: ubuntu-latest\n",
                    f"    runs-on: ubuntu-latest\n    {variant}\n",
                    1,
                )
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    assert_read_only_permissions(mutated)

    def test_quoted_permissions_text_inside_run_is_ignored(self) -> None:
        source = workflow_text()
        mutated = source.replace(
            "          set -euo pipefail\n",
            "          set -euo pipefail\n"
            "          printf '%s\\n' '\"permissions\": write-all' >/dev/null\n",
            1,
        )
        self.assertNotEqual(source, mutated)
        assert_read_only_permissions(mutated)

    def test_exit_before_no_go_guard_is_rejected(self) -> None:
        source = workflow_text()
        active = "            printf '%s' \"$output\" | python -c '"
        mutated = source.replace(active, "          exit 0\n" + active, 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_offline_replay_policy(mutated)

    def test_outer_heredoc_cannot_prove_no_go_guard(self) -> None:
        source = workflow_text()
        active = "            printf '%s' \"$output\" | python -c '"
        mutated = source.replace(active, "          cat <<'DECOY'\n" + active, 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_offline_replay_policy(mutated)

    def test_early_success_exit_cannot_prove_no_go_guard(self) -> None:
        source = workflow_text()
        active = "          payload = json.loads(sys.stdin.read())\n"
        mutated = source.replace(
            active,
            "          raise SystemExit(0)\n" + active,
            1,
        )
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_offline_replay_policy(mutated)

    def test_exit_before_artifact_verification_is_rejected(self) -> None:
        source = workflow_text()
        active = (
            '          test "$(apkanalyzer manifest application-id "$apk")" '
            "= 'com.maestrovpn.tv'"
        )
        mutated = source.replace(active, "          exit 0\n" + active, 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_android_artifact_policy(mutated)

    def test_false_branch_cannot_prove_artifact_verification(self) -> None:
        source = workflow_text()
        start = (
            '          test "$(apkanalyzer manifest application-id "$apk")" '
            "= 'com.maestrovpn.tv'"
        )
        end = "          PY\n          sha256sum"
        mutated = source.replace(start, "          if false; then\n" + start, 1)
        mutated = mutated.replace(end, "          PY\n          fi\n          sha256sum", 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_android_artifact_policy(mutated)

    def test_early_success_exit_cannot_prove_debug_signer_guard(self) -> None:
        source = workflow_text()
        active = (
            '          report = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")\n'
        )
        mutated = source.replace(
            active,
            "          raise SystemExit(0)\n" + active,
            1,
        )
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_android_artifact_policy(mutated)


    def test_no_go_string_without_executed_guard_is_rejected(self) -> None:
        source = workflow_text()
        active = (
            '          if payload["release_readiness"] != "NO_GO":\n'
            '              raise SystemExit("release_readiness_not_no_go")'
        )
        decoy = (
            """          no_go_decoy = 'payload["release_readiness"] != "NO_GO"'
          raise_decoy = 'release_readiness_not_no_go'"""
        )
        mutated = source.replace(active, decoy, 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_offline_replay_policy(mutated)

    def test_artifact_command_decoy_is_rejected(self) -> None:
        source = workflow_text()
        active = '          sha256sum "$apk" | tee "${RUNNER_TEMP}/task7-apk.sha256"'
        decoy = """          echo 'sha256sum "$apk" | tee \
"${RUNNER_TEMP}/task7-apk.sha256"'"""
        mutated = source.replace(active, decoy, 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_android_artifact_policy(mutated)

    def test_workflow_has_only_read_permissions_and_no_mutation_channel(self) -> None:
        assert_read_only_permissions(workflow_text())
        self.assertEqual({"contents": "read"}, top_level_permissions(workflow_text()))
        source = active_step_text(workflow_text())
        forbidden = (
            r"(?mi)^\s*[a-z-]+:\s*write\s*$",
            r"\$\{\{\s*secrets\.",
            r"(?mi)^\s*environment\s*:",
            r"\bSIGNING_KEYSTORE_B64\b",
            r"\bLOCAL_PROPERTIES\b",
            r"\bKEYSTORE_PASS\b",
            r"release\.keystore",
            r"\bgh\s+release\b",
            r"softprops/action-gh-release",
            r"\baws\s+(?:s3|s3api)\b",
            r"(?m)^\s*yc(?:\.exe)?\s+",
            r"\bs3cmd\b",
            r"\brclone\b",
            r"\b(?:ssh|scp|systemctl|iptables|nft|ufw)\b",
            r"\bdocker\s+push\b",
            r"https?://",
            r"\bsed\b[^\n]*version\.properties",
        )
        for pattern in forbidden:
            with self.subTest(pattern=pattern):
                self.assertIsNone(re.search(pattern, source), pattern)

    def test_every_action_reference_is_pinned_to_a_commit(self) -> None:
        references = re.findall(
            r"(?m)^\s+- uses:\s+([^\s#]+)", active_step_text(workflow_text())
        )
        self.assertTrue(references)
        for reference in references:
            with self.subTest(reference=reference):
                self.assertRegex(reference, r"^[^@]+@[0-9a-f]{40}$")


class WorkflowGateContractTest(unittest.TestCase):
    def test_workflow_has_exactly_the_five_isolated_jobs(self) -> None:
        jobs = workflow_text().split("jobs:\n", 1)[1]
        names = set(re.findall(r"(?m)^  ([a-z0-9][a-z0-9-]*):\n", jobs))
        self.assertEqual(EXPECTED_JOBS, names)

    def test_format_unit_job_runs_go_python_docs_and_diff_checks(self) -> None:
        source = job_source(workflow_text(), "format-unit")
        self.assertIn("gofmt -l", source)
        self.assertIn("comm -23", source)
        self.assertIn("Unexpected non-canonical Go files outside accepted legacy debt", source)
        self.assertCountEqual(GOFMT_SCOPE, bash_array(source, "gofmt_scope"))
        self.assertCountEqual(
            LEGACY_GO_FORMAT_DEBT,
            bash_array(source, "legacy_debt"),
        )
        legacy_debt = set(bash_array(source, "legacy_debt"))
        for path in CANONICAL_TASK7_FORMAT_FILES:
            with self.subTest(canonical_path=path):
                self.assertNotIn(path, legacy_debt)
        self.assertEqual(
            GO_PACKAGES,
            continued_command_arguments(
                step_run_lines(workflow_text(), "Test Task 7 Go packages"),
                "go test -count=1",
            ),
        )
        for module in (
            "scripts.tests.test_yandex_cdn_docs",
            "scripts.tests.test_yandex_cdn_repro",
            "scripts.tests.test_yandex_cdn_ci",
        ):
            self.assertIn(module, source)
        self.assertIn("python scripts/validate_yandex_cdn_docs.py", source)
        self.assertIn("git diff --check", source)

    def test_race_vet_job_is_separate_and_covers_every_go_package(self) -> None:
        source = job_source(workflow_text(), "race-vet")
        self.assertIn("needs: format-unit", source)
        self.assertEqual(
            GO_PACKAGES,
            continued_command_arguments(
                step_run_lines(workflow_text(), "Race-test Task 7 Go packages"),
                "go test -count=1 -race",
            ),
        )
        self.assertEqual(
            GO_PACKAGES,
            continued_command_arguments(
                step_run_lines(workflow_text(), "Vet Task 7 Go packages"),
                "go vet",
            ),
        )

    def test_release_wrappers_check_the_exact_go_package_list(self) -> None:
        bash = BASH_RELEASE_VALIDATOR.read_text(encoding="utf-8")
        powershell = POWERSHELL_RELEASE_VALIDATOR.read_text(encoding="utf-8")
        self.assertEqual(GO_PACKAGES, bash_array(bash, "validation_packages"))
        self.assertEqual(
            GO_PACKAGES,
            powershell_array(powershell, "validationPackages"),
        )
        bash_check = '"$go_binary" test -count=1 "${validation_packages[@]}"'
        self.assertEqual(1, bash.count(bash_check))
        self.assertLess(bash.index(bash_check), bash.index('exec "$go_binary" run'))
        powershell_check = "& $goBinary @testArgs"
        self.assertIn("$testArgs = @('test', '-count=1') + $validationPackages", powershell)
        self.assertEqual(1, powershell.count(powershell_check))
        self.assertLess(
            powershell.index(powershell_check),
            powershell.index("& $goBinary @validatorArgs"),
        )

    def assert_release_wrapper_behavior(self, command: list[str]) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fake_go = write_fake_go(root)
            log_path = root / "fake-go.jsonl"
            release_dir = root / "release"
            evidence_trust = root / "evidence-trust.pem"
            wrapper_command = [
                *command,
                "--release-dir",
                str(release_dir),
                "--evidence-trust",
                str(evidence_trust),
                "--go-binary",
                str(fake_go),
            ]
            environment = os.environ.copy()
            environment["FAKE_GO_LOG"] = str(log_path)
            success = subprocess.run(
                wrapper_command,
                cwd=REPO_ROOT,
                env=environment,
                capture_output=True,
                text=True,
                timeout=30,
                check=False,
            )
            self.assertEqual(0, success.returncode, success.stdout + success.stderr)
            expected_test = ["test", "-count=1", *GO_PACKAGES]
            expected_run = [
                "run",
                "./cmd/maestro-release-validate",
                "--release-dir",
                str(release_dir),
                "--evidence-trust",
                str(evidence_trust),
            ]
            self.assertEqual([expected_test, expected_run], fake_go_calls(log_path))

            log_path.unlink()
            environment["FAKE_GO_TEST_EXIT"] = "23"
            failure = subprocess.run(
                wrapper_command,
                cwd=REPO_ROOT,
                env=environment,
                capture_output=True,
                text=True,
                timeout=30,
                check=False,
            )
            self.assertNotEqual(0, failure.returncode)
            self.assertIn(
                "release_validation_failed code=go_tests_failed",
                failure.stderr,
            )
            self.assertEqual([expected_test], fake_go_calls(log_path))

    def test_bash_release_wrapper_executes_exact_checks_and_fails_closed(self) -> None:
        if os.name == "nt":
            self.skipTest("Bash wrapper behavior runs on the Linux CI runner")
        bash = shutil.which("bash")
        if bash is None:
            self.skipTest("bash is unavailable")
        self.assert_release_wrapper_behavior([bash, str(BASH_RELEASE_VALIDATOR)])

    def test_powershell_release_wrapper_executes_exact_checks_and_fails_closed(
        self,
    ) -> None:
        powershell = shutil.which("pwsh") or shutil.which("powershell")
        if powershell is None:
            self.skipTest("PowerShell is unavailable")
        self.assert_release_wrapper_behavior(
            [
                powershell,
                "-NoLogo",
                "-NoProfile",
                "-File",
                str(POWERSHELL_RELEASE_VALIDATOR),
            ]
        )

    def test_root_only_linux_canary_tests_are_exact_sha_and_sudo_gated(self) -> None:
        source = workflow_text()
        assert_step_metadata(
            source,
            ROOT_CANARY_STEP,
            {"working-directory": "backend", "run": "|"},
        )
        self.assertEqual(
            (
                "set -euo pipefail",
                'test "$(git rev-parse HEAD)" = "$GITHUB_SHA"',
                'go_binary="$(command -v go)"',
                'go_cache="$(go env GOCACHE)"',
                'go_mod_cache="$(go env GOMODCACHE)"',
                "sudo --non-interactive env \\",
                '  GOCACHE="$go_cache" \\',
                '  GOMODCACHE="$go_mod_cache" \\',
                '  "$go_binary" test -count=1 -buildvcs=false -json \\',
                "    -run '^(TestLinuxProtectedReaderAndTemporaryExecutable|TestLinuxCredentialExecutionClearsSupplementaryGroups)$' \\",
                "    ./cmd/maestro-xray-cdn-canary | python -c '",
                "import json",
                "import sys",
                "expected = {",
                '    "TestLinuxProtectedReaderAndTemporaryExecutable",',
                '    "TestLinuxCredentialExecutionClearsSupplementaryGroups",',
                "}",
                "passes = {name: 0 for name in expected}",
                "skips = {name: 0 for name in expected}",
                "for raw in sys.stdin:",
                "    event = json.loads(raw)",
                '    name = event.get("Test")',
                "    if name not in expected:",
                "        continue",
                '    if event.get("Action") == "pass":',
                "        passes[name] += 1",
                '    if event.get("Action") == "skip":',
                "        skips[name] += 1",
                "if passes != {name: 1 for name in expected} or any(skips.values()):",
                '    raise SystemExit(f"root tests not proven: passes={passes!r} skips={skips!r}")',
                "'",
            ),
            step_run_lines(source, ROOT_CANARY_STEP),
        )

    def test_offline_replay_runs_exact_wrappers_and_parses_pass_no_go_json(self) -> None:
        source = job_source(workflow_text(), "offline-replay")
        self.assertIn("needs: format-unit", source)
        prime = source.find("- name: Prime exact Go module cache")
        replay = source.find("- name: Replay all nine offline fixture suites")
        self.assertGreaterEqual(prime, 0)
        self.assertGreaterEqual(replay, 0)
        self.assertLess(prime, replay)
        prime_source = source[prime:replay]
        replay_source = source[replay:]
        job_header = source[:prime]
        self.assertNotIn("GOPROXY", job_header)
        self.assertNotIn("GOSUMDB", job_header)
        self.assertIn("go mod download", prime_source)
        self.assertIn("go mod verify", prime_source)
        self.assertNotIn("GOPROXY", prime_source)
        self.assertNotIn("GOSUMDB", prime_source)
        self.assertNotIn('"off"', prime_source)
        self.assertIn("GOPROXY: \"off\"", replay_source)
        self.assertIn("GOSUMDB: \"off\"", replay_source)
        self.assertIn("set -euo pipefail", source)
        self.assertIn("scripts/repro/_run-white-list-suite.sh", source)
        self.assertIn("bash -n", source)
        for wrapper in WRAPPERS:
            with self.subTest(wrapper=wrapper):
                self.assertEqual(1, source.count(wrapper))
        assert_offline_replay_policy(workflow_text())
        self.assertIsNone(re.search(r"\b(?:curl|wget|gh)\b", source))

    def test_rqlite_purge_is_targeted_isolated_and_always_cleaned_up(self) -> None:
        source = job_source(workflow_text(), "rqlite-purge")
        self.assertTrue(source, "missing rqlite-purge job")
        self.assertIn("needs: format-unit", source)
        self.assertIn("bash ops/ha/test-ci-rqlite-cluster.sh", source)
        self.assertIn("bash ops/ha/ci-rqlite-cluster.sh start", source)
        self.assertIn("-tags=rqlite_integration", source)
        self.assertIn("./internal/controlplane", source)
        self.assertIn("^TestWhiteListIdentityDoesNotBlockTombstonePurge$", source)
        self.assertNotIn("./...", source)
        cleanup = source.find("if: ${{ always() }}")
        stop = source.find("bash ops/ha/ci-rqlite-cluster.sh stop")
        self.assertGreaterEqual(cleanup, 0)
        self.assertGreaterEqual(stop, 0)
        self.assertLess(cleanup, stop)

    def test_android_build_waits_for_every_backend_and_replay_gate(self) -> None:
        source = job_source(workflow_text(), "android-test-apk")
        for dependency in ("format-unit", "race-vet", "offline-replay", "rqlite-purge"):
            self.assertIn(f"- {dependency}", source)
        self.assertIn("contents: read", source)
        self.assertIn("actions: read", source)

    def test_android_build_is_single_attempt_test_only_and_candidate_bound(self) -> None:
        assert_android_artifact_policy(workflow_text())
        source = job_source(workflow_text(), "android-test-apk")
        self.assertEqual(1, source.count("./gradlew"))
        self.assertNotRegex(source, r"\b(?:until|retry|sleep)\b")
        self.assertIn(":app:assembleOtherDebug", source)
        self.assertIn("-PmaestroTask7TestVersionName=1.0.158-task7-test", source)
        self.assertIn("-PmaestroTask7TestVersionCode=1015800", source)
        self.assertIn("com.maestrovpn.tv", source)
        self.assertIn("apkanalyzer", source)
        self.assertIn(
            'apksigner="${ANDROID_SDK_ROOT}/build-tools/36.0.0/apksigner"', source
        )
        self.assertIn('test -x "$apksigner"', source)
        self.assertIn(
            '"$apksigner" verify --verbose --print-certs "$apk"', source
        )
        self.assertIsNone(re.search(r"(?m)^[ ]*apksigner(?:[ ]|$)", source))
        self.assertEqual(
            1,
            source.count(
                "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02"
            ),
        )
        self.assertIn("github.sha", source)
        self.assertIn("if-no-files-found: error", source)
        self.assertNotIn("continue-on-error", source)

    def test_android_build_uses_the_exact_libbox_artifact_and_both_hashes(self) -> None:
        source = job_source(workflow_text(), "android-test-apk")
        for literal in (
            "31758418005",
            "9203846250",
            "baf1ae39b845601ef5291c92c9638261f4a9dfd7725d04ffd856431d48d42b37",
            "c70ce917b331fd333d52b8e3c01eba2c2a343497f896fdf62c30436490a88f05",
        ):
            self.assertIn(literal, source)
        self.assertNotRegex(source, r"(?i)latest\s+successful|gh\s+run\s+list")
        self.assertIn("workflow_run", source)
        self.assertIn("expired", source)


class GradleTask7VersionContractTest(unittest.TestCase):
    def test_override_allows_generic_defaults_and_enforces_task7_identity(self) -> None:
        source = gradle_text()
        source_defaults = (REPO_ROOT / "version.properties").read_text(encoding="utf-8")
        self.assertIn("VERSION_NAME=0.1.0", source_defaults)
        self.assertIn("VERSION_CODE=1", source_defaults)
        self.assertIn('gradleProperty("maestroTask7TestVersionName")', source)
        self.assertIn('gradleProperty("maestroTask7TestVersionCode")', source)
        self.assertIn("task7ProductionBaselineVersionCode = 157", source)
        self.assertIn('task7ProductionBaselineVersionName = "1.0.157"', source)
        self.assertIn("toIntOrNull()", source)
        self.assertIn("length in 1..64", source)
        self.assertIn("-task7-test", source)
        self.assertNotIn("productionVersionName == task7ProductionBaselineVersionName", source)
        self.assertNotIn("productionVersionCode == task7ProductionBaselineVersionCode", source)
        self.assertIn('task7TestVersionName == "1.0.158-task7-test"', source)
        self.assertIn("task7TestVersionCodeValue == 1015800", source)
        self.assertIn("task7TestVersionName != productionVersionName", source)
        self.assertIn("task7TestVersionName != task7ProductionBaselineVersionName", source)
        self.assertIn("task7TestVersionCodeValue > productionVersionCode", source)
        self.assertIn("task7TestVersionCodeValue > task7ProductionBaselineVersionCode", source)
        self.assertIn("task7TestVersionNameProperty ?: productionVersionName", source)
        self.assertIn("task7TestVersionCode ?: productionVersionCode", source)

    def test_task7_override_fails_closed_when_release_signing_is_configured(self) -> None:
        source = gradle_text()
        self.assertIn("releaseSigningConfigured", source)
        self.assertIn("task7TestOverrideActive", source)
        self.assertIn("require(!releaseSigningConfigured)", source)
        self.assertIn("Task 7 test APK must not use release signing", source)


    def test_required_step_execution_metadata_is_fail_closed(self) -> None:
        source = workflow_text()
        cases = (
            ("Replay all nine offline fixture suites", assert_offline_replay_policy),
            ("Build separately versioned Task 7 APK", assert_android_artifact_policy),
            ("Verify Task 7 APK metadata and signer", assert_android_artifact_policy),
            ("Upload Task 7 APK artifact only", assert_android_artifact_policy),
        )
        variants = (
            "        if: ${{ false }}\n",
            "        continue-on-error: true\n",
            "        shell: /bin/true {0}\n",
        )
        for step_name, validator in cases:
            marker = f"      - name: {step_name}\n"
            for variant in variants:
                with self.subTest(step=step_name, variant=variant.strip()):
                    mutated = source.replace(marker, marker + variant, 1)
                    self.assertNotEqual(source, mutated)
                    with self.assertRaises(AssertionError):
                        validator(mutated)

    def test_current_execution_metadata_policy_passes(self) -> None:
        policy = globals().get("assert_execution_metadata_policy")
        self.assertIsNotNone(policy)
        policy(workflow_text())

    def test_required_job_execution_metadata_is_fail_closed(self) -> None:
        source = workflow_text()
        policy = globals().get("assert_execution_metadata_policy")
        self.assertIsNotNone(policy)
        variants = (
            "    if: ${{ false }}\n",
            "    continue-on-error: true\n",
            "    env:\n      BASH_ENV: /tmp/noop\n",
            "    defaults:\n      run:\n        shell: /bin/true {0}\n",
        )
        for job_name in (
            "format-unit",
            "race-vet",
            "offline-replay",
            "rqlite-purge",
            "android-test-apk",
        ):
            marker = f"  {job_name}:\n"
            head, tail = source.split(marker, 1)
            for variant in variants:
                with self.subTest(job=job_name, variant=variant.strip()):
                    mutated_tail = tail.replace(
                        "    runs-on: ubuntu-latest\n",
                        "    runs-on: ubuntu-latest\n" + variant,
                        1,
                    )
                    mutated = head + marker + mutated_tail
                    self.assertNotEqual(source, mutated)
                    with self.assertRaises(AssertionError):
                        policy(mutated)

    def test_workflow_defaults_run_shell_is_rejected(self) -> None:
        source = workflow_text()
        policy = globals().get("assert_execution_metadata_policy")
        self.assertIsNotNone(policy)
        mutated = source.replace(
            "jobs:\n",
            "defaults:\n  run:\n    shell: /bin/true {0}\njobs:\n",
            1,
        )
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            policy(mutated)

    def test_alias_job_permissions_key_is_rejected(self) -> None:
        source = workflow_text()
        first_line = source.splitlines()[0] + "\n"
        mutated = source.replace(
            first_line, "name: &perm permissions\n", 1
        ).replace(
            "    runs-on: ubuntu-latest\n",
            "    runs-on: ubuntu-latest\n    *perm: write-all\n",
            1,
        )
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_read_only_permissions(mutated)


    def test_rqlite_proof_metadata_is_fail_closed(self) -> None:
        source = workflow_text()
        marker = "      - name: Prove white-list identity permits tombstone purge\n"
        variants = (
            "        if: ${{ false }}\n",
            "        continue-on-error: true\n",
            "        shell: /bin/true {0}\n",
        )
        for variant in variants:
            with self.subTest(variant=variant.strip()):
                mutated = source.replace(marker, marker + variant, 1)
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    assert_execution_metadata_policy(mutated)

    def test_extra_preproof_environment_step_is_rejected(self) -> None:
        source = workflow_text()
        marker = "      - name: Replay all nine offline fixture suites\n"
        addition = (
            "      - name: Poison execution environment\n"
            "        run: echo 'BASH_ENV=/tmp/noop' >> \"$GITHUB_ENV\"\n\n"
        )
        mutated = source.replace(marker, addition + marker, 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_offline_replay_policy(mutated)

    def test_upload_requires_apk_and_checksum_paths(self) -> None:
        source = workflow_text()
        active = "            ${{ steps.apk.outputs.apk_path }}\n"
        mutated = source.replace(active, "", 1)
        self.assertNotEqual(source, mutated)
        with self.assertRaises(AssertionError):
            assert_android_artifact_policy(mutated)


if __name__ == "__main__":
    unittest.main()
