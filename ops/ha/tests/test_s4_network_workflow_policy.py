from __future__ import annotations

import ipaddress
import re
from pathlib import Path
import unittest

from ops.ha import build_workflow_policy as BASE


ROOT = Path(__file__).parents[3]
WORKFLOW = ROOT / ".github" / "workflows" / "ha-s4-network-change-package.yml"
BRANCH = "codex/yandex-cdn-whitelist-task3-sync"
CHECKOUT_SHA = "11bd71901bbe5b1630ceea73d27597364c9af683"
APPROVED_ACTIVE_SOURCE_SHA256 = "80bb607a33156f05060f09acc7eac5a61fccaaf6572ab4223db6dfc8c73001b3"

EXPECTED_STEP_NAMES = (
    "Checkout repository",
    "Create private S4 runner temp",
    "Run S4 change-package tests",
    "Run S4 boundary and safety tests",
    "Compile S4 change-package sources",
    "Check S4 wrapper help",
    "Validate canonical documentation",
    "Check repository diff",
    "Clean private S4 runner temp",
)

EXPECTED_RUN_LINES = {
    "Create private S4 runner temp": (
        "set -euo pipefail",
        'test -n "$RUNNER_TEMP"',
        'case "$RUNNER_TEMP" in /*) ;; *) exit 1 ;; esac',
        's4_ci_tmp="$RUNNER_TEMP/maestro-s4-network-change-package"',
        'case "$s4_ci_tmp" in "$RUNNER_TEMP"/*) ;; *) exit 1 ;; esac',
        'install -d -m 700 "$s4_ci_tmp/pycache"',
        "printf 'PYTHONPYCACHEPREFIX=%s\\n' \"$s4_ci_tmp/pycache\" >> \"$GITHUB_ENV\"",
    ),
    "Run S4 change-package tests": (
        "set -euo pipefail",
        "python -m unittest \\",
        "  ops.ha.tests.test_s4_network_change_package \\",
        "  ops.ha.tests.test_s4_network_workflow_policy -v",
    ),
    "Run S4 boundary and safety tests": (
        "set -euo pipefail",
        "python -m unittest \\",
        "  ops.ha.tests.test_s4_network_change_package.S4CliBoundaryTests \\",
        "  ops.ha.tests.test_s4_network_change_package.S4CapabilityDenylistTests \\",
        "  ops.ha.tests.test_s4_network_change_package.S4SensitiveLiteralTests -v",
    ),
    "Compile S4 change-package sources": (
        "set -euo pipefail",
        "python -m py_compile \\",
        "  ops/ha/s4_network_change_package.py \\",
        "  ops/ha/s4-network-change-package.py",
    ),
    "Check S4 wrapper help": (
        "python ops/ha/s4-network-change-package.py --help",
    ),
    "Validate canonical documentation": (
        "set -euo pipefail",
        "python -m unittest scripts.tests.test_yandex_cdn_docs -v",
        "python scripts/validate_yandex_cdn_docs.py",
    ),
    "Check repository diff": ("git diff --check",),
    "Clean private S4 runner temp": (
        "set -euo pipefail",
        's4_ci_tmp="$RUNNER_TEMP/maestro-s4-network-change-package"',
        'case "$s4_ci_tmp" in "$RUNNER_TEMP"/*) ;; *) exit 1 ;; esac',
        'rm -rf -- "$s4_ci_tmp"',
    ),
}


class S4WorkflowPolicyError(ValueError):
    pass


def _fail(code: str) -> None:
    raise S4WorkflowPolicyError(f"ha-s4-workflow-policy:{code}")


def _mapping(node: BASE._Node, code: str) -> dict[str, BASE._Node]:
    if not isinstance(node, dict):
        _fail(code)
    return node


def _sequence(node: BASE._Node, code: str) -> list[BASE._Node]:
    if not isinstance(node, list):
        _fail(code)
    return node


def _scalar(node: BASE._Node, code: str) -> str:
    if not isinstance(node, str):
        _fail(code)
    return node


def _exact_keys(mapping: dict[str, BASE._Node], expected: set[str], code: str) -> None:
    if set(mapping) != expected:
        _fail(code)


def _run_lines(node: BASE._Node) -> tuple[str, ...]:
    if isinstance(node, str):
        values = (node,)
    elif isinstance(node, BASE._Block):
        if node.style != "|":
            _fail("step-boundary")
        values = node.lines
    else:
        _fail("step-boundary")
    return tuple(line for line in values if line.strip() and not line.lstrip().startswith("#"))


def _validate_triggers(node: BASE._Node) -> None:
    triggers = _mapping(node, "trigger-boundary")
    _exact_keys(triggers, {"push", "pull_request", "workflow_dispatch"}, "trigger-boundary")
    for event in ("push", "pull_request"):
        event_map = _mapping(triggers[event], "trigger-boundary")
        _exact_keys(event_map, {"branches"}, "trigger-boundary")
        if _sequence(event_map["branches"], "trigger-boundary") != [BRANCH]:
            _fail("trigger-boundary")
    if triggers["workflow_dispatch"] != {}:
        _fail("trigger-boundary")


def _validate_permissions(node: BASE._Node) -> None:
    if _mapping(node, "permissions-boundary") != {"contents": "read"}:
        _fail("permissions-boundary")


def _ipv6_literals(text: str) -> tuple[str, ...]:
    pattern = re.compile(
        r"(?<![0-9A-Za-z:])[0-9A-Fa-f:]*:[0-9A-Fa-f:]+(?![0-9A-Za-z:])"
    )
    values = []
    for match in pattern.finditer(text):
        candidate = match.group(0)
        try:
            ipaddress.IPv6Address(candidate)
        except ipaddress.AddressValueError:
            continue
        values.append(candidate)
    return tuple(values)


def _validate_workflow_source(text: str) -> None:
    try:
        source = BASE._validate_source(text)
        document = BASE._SubsetParser(source).parse()
    except (BASE.WorkflowPolicyError, BASE._SubsetError):
        _fail("invalid-structure")

    _exact_keys(document, {"name", "on", "permissions", "concurrency", "jobs"}, "top-level-boundary")
    if document["name"] != "HA S4 network change-package checks":
        _fail("top-level-boundary")
    _validate_triggers(document["on"])
    _validate_permissions(document["permissions"])

    if _mapping(document["concurrency"], "concurrency-boundary") != {
        "group": "ha-s4-network-change-package-${{ github.workflow }}-${{ github.ref }}",
        "cancel-in-progress": "false",
    }:
        _fail("concurrency-boundary")

    jobs = _mapping(document["jobs"], "job-boundary")
    _exact_keys(jobs, {"verify-s4-change-package"}, "job-boundary")
    job = _mapping(jobs["verify-s4-change-package"], "job-boundary")
    _exact_keys(job, {"name", "runs-on", "timeout-minutes", "permissions", "steps"}, "job-boundary")
    if job["name"] != "Verify inert S4 change package":
        _fail("job-boundary")
    if job["runs-on"] != "ubuntu-24.04":
        _fail("runner-boundary")
    if job["timeout-minutes"] != "15":
        _fail("timeout-boundary")
    _validate_permissions(job["permissions"])

    steps = _sequence(job["steps"], "step-boundary")
    if len(steps) != len(EXPECTED_STEP_NAMES):
        _fail("step-boundary")
    step_maps = [_mapping(step, "step-boundary") for step in steps]
    if tuple(_scalar(step.get("name"), "step-boundary") for step in step_maps) != EXPECTED_STEP_NAMES:
        _fail("step-boundary")

    checkout = step_maps[0]
    _exact_keys(checkout, {"name", "uses", "with"}, "checkout-boundary")
    if checkout["uses"] != f"actions/checkout@{CHECKOUT_SHA}":
        _fail("checkout-boundary")
    if _mapping(checkout["with"], "checkout-boundary") != {"persist-credentials": "false"}:
        _fail("checkout-boundary")

    for step in step_maps[1:]:
        name = _scalar(step["name"], "step-boundary")
        expected_keys = {"name", "run"}
        if name == "Clean private S4 runner temp":
            expected_keys.add("if")
            if step.get("if") != "always()":
                _fail("cleanup-boundary")
        _exact_keys(step, expected_keys, "step-boundary")
        if _run_lines(step["run"]) != EXPECTED_RUN_LINES[name]:
            _fail("command-boundary")

    lowered = source.casefold()
    allowed_semantic_hostnames = {
        "github.ref",
        "github.workflow",
        "s4-network-change-package.py",
    }
    hostname_literals = tuple(
        match.group(0)
        for match in re.finditer(
            r"(?i)(?<![A-Za-z0-9._-])"
            r"(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+"
            r"(?:[A-Za-z]|[A-Za-z](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9]))\.?"
            r"(?![A-Za-z0-9._-])",
            source,
        )
        if match.group(0).rstrip(".").casefold() not in allowed_semantic_hostnames
    )
    forbidden_literals = (
        "pull_request_target",
        "${{ secrets",
        "upload-artifact",
        "download-artifact",
        "environment:",
        "curl ",
        "wget ",
        "ssh ",
        "scp ",
        "rsync ",
        "systemctl ",
        "service ",
        "sudo ",
        "iptables ",
        "firewall-cmd ",
        "netplan apply",
    )
    if any(value in lowered for value in forbidden_literals):
        _fail("capability-boundary")
    if (
        "github_pat_" in lowered
        or "bearer " in lowered
        or re.search(r"-----BEGIN [^-\r\n]*PRIVATE KEY-----", source, flags=re.IGNORECASE)
        or re.search(
            r"(?i)(?<![\w-])(?:api[_-]?key|token|password)[\"']?\s*(?:=|:)\s*[\"']?\S",
            source,
        )
    ):
        _fail("sensitive-boundary")
    if (
        re.search(r"(?<![0-9])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![0-9])", source)
        or _ipv6_literals(source)
        or hostname_literals
        or re.search(
            r"(?i)(?<![A-Za-z0-9.-])[A-Za-z][A-Za-z0-9.-]*:[0-9]{1,5}(?![0-9])",
            source,
        )
    ):
        _fail("endpoint-boundary")
    if re.search(
        r"(?i)(?<![A-Za-z0-9._-])/(?:etc|var|usr|root|home|opt|run|srv|lib|boot|snap|tmp)(?:/[^\s`'\"<>]+)+",
        source,
    ):
        _fail("sensitive-boundary")
    cleanup_stripped = "\n".join(
        "" if line.strip() == 'rm -rf -- "$s4_ci_tmp"' else line
        for line in source.splitlines()
    )
    if re.search(r"(?i)\brm\s+-rf\b", cleanup_stripped):
        _fail("capability-boundary")
    for step in step_maps[1:]:
        if "uses" in step:
            _fail("action-boundary")
    if not re.fullmatch(r"[0-9a-f]{64}", APPROVED_ACTIVE_SOURCE_SHA256):
        _fail("digest-boundary")
    if BASE._active_source_sha256(source) != APPROVED_ACTIVE_SOURCE_SHA256:
        _fail("digest-boundary")


class S4NetworkWorkflowPolicyTests(unittest.TestCase):
    def setUp(self) -> None:
        self.source = WORKFLOW.read_text(encoding="utf-8") if WORKFLOW.is_file() else ""

    def assert_rejected(self, source: str, code: str) -> None:
        with self.assertRaises(S4WorkflowPolicyError) as raised:
            _validate_workflow_source(source)
        self.assertEqual(str(raised.exception), f"ha-s4-workflow-policy:{code}")

    def test_active_workflow_matches_the_exact_fail_closed_contract(self) -> None:
        self.assertTrue(WORKFLOW.is_file(), "S4 workflow is missing")
        _validate_workflow_source(self.source)

    def test_active_digest_ignores_only_comments_and_blank_lines(self) -> None:
        self.assertTrue(self.source)
        changed = "# comment-only fixture\n\n" + self.source.replace(
            "      - name: Check repository diff\n",
            "      # comment-only fixture\n      - name: Check repository diff\n",
            1,
        )
        self.assertEqual(
            BASE._active_source_sha256(self.source),
            BASE._active_source_sha256(changed),
        )
        _validate_workflow_source(changed)

    def test_active_content_mutations_fail_closed(self) -> None:
        mutations = (
            (
                self.source.replace("  workflow_dispatch:\n", "  pull_request_target:\n", 1),
                "trigger-boundary",
            ),
            (
                self.source.replace(BRANCH, "fixture-branch", 1),
                "trigger-boundary",
            ),
            (
                self.source.replace("      - name: Check repository diff\n", "", 1),
                "invalid-structure",
            ),
            (
                self.source.replace("git diff --check", "git diff --stat", 1),
                "command-boundary",
            ),
            (
                self.source.replace("      - name: Check repository diff\n", "      - name: Extra fixture step\n        run: true\n\n      - name: Check repository diff\n", 1),
                "step-boundary",
            ),
        )
        for changed, code in mutations:
            with self.subTest(code=code):
                self.assertNotEqual(changed, self.source)
                self.assert_rejected(changed, code)

    def test_forbidden_capability_literals_are_rejected(self) -> None:
        marker = "      - name: Check repository diff\n        run: git diff --check"
        fixtures = ("curl fixture.invalid", "ssh fixture", "systemctl restart fixture", "${{ secrets.FIXTURE }}")
        for fixture in fixtures:
            changed = self.source.replace(marker, marker + "\n        # " + fixture, 1)
            with self.subTest(fixture=fixture):
                self.assertNotEqual(changed, self.source)
                self.assert_rejected(changed, "capability-boundary")

    def test_sensitive_and_endpoint_literals_are_rejected(self) -> None:
        marker = "      - name: Check repository diff\n        run: git diff --check"
        fixtures = (
            ("github_pat_fixture", "sensitive-boundary"),
            ("Bearer fixture", "sensitive-boundary"),
            ("password=fixture", "sensitive-boundary"),
            ("192.0.2.1", "endpoint-boundary"),
            ("2001:db8::1", "endpoint-boundary"),
            ("synthetic.example.com", "endpoint-boundary"),
            ("endpoint=https://prod.example.com/api", "endpoint-boundary"),
            ("endpoint=https://prod.example.de/api", "endpoint-boundary"),
            ("endpoint=https://prod.example.com./api", "endpoint-boundary"),
            ("synthetic-host:443", "endpoint-boundary"),
            ('{"token":"opaque-secret"}', "sensitive-boundary"),
            ("/tmp/synthetic-command-sheet", "sensitive-boundary"),
            ("rm -rf synthetic-root", "capability-boundary"),
        )
        for fixture, code in fixtures:
            changed = self.source.replace(marker, marker + "\n        # " + fixture, 1)
            with self.subTest(fixture=fixture):
                self.assertNotEqual(changed, self.source)
                self.assert_rejected(changed, code)


if __name__ == "__main__":
    unittest.main()
