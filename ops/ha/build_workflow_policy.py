#!/usr/bin/env python3
"""Fail-closed policy for the repository-only HA panel build workflow."""

from __future__ import annotations

from dataclasses import dataclass
import hashlib
import os
from pathlib import Path
import re
import stat
import sys
from typing import TypeAlias


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "ha-build.yml"

POLICY_PREFIX = "ha-build-workflow-policy:"
MAX_SOURCE_BYTES = 65_536
CANONICAL_BRANCH = "codex/yandex-cdn-whitelist-task3-sync"
CHECKOUT_SHA = "11bd71901bbe5b1630ceea73d27597364c9af683"
SETUP_GO_SHA = "40f1582b2485089dde7abd97c1529aa768e1baff"
UPLOAD_SHA = "ea165f8d65b6e75b540449e92b4886f43607fa02"
APPROVED_ACTIVE_SHA256 = (
    "d3ed6d03dd4866571a7b8594286fb706ef94cd1c99300971d9c2e6fc9223b1a9"
)

_BLOCK_RE = re.compile(r"[|>](?:[+-]?[1-9]?|[1-9]?[+-]?)?")
_STEP_NAME_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9 ./_()-]{0,95}")
_FULL_SHA_RE = re.compile(r"[0-9a-f]{40}")
_IPV4_RE = re.compile(r"(?<![0-9])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![0-9])")


class WorkflowPolicyError(ValueError):
    """A redacted workflow-policy failure."""


class _SubsetError(ValueError):
    pass


@dataclass(frozen=True)
class _Block:
    style: str
    lines: tuple[str, ...]


_Node: TypeAlias = str | _Block | dict[str, "_Node"] | list["_Node"]


def _fail(code: str) -> None:
    raise WorkflowPolicyError(POLICY_PREFIX + code)


class _SubsetParser:
    """Parse only the small canonical YAML subset used by this workflow."""

    def __init__(self, text: str) -> None:
        self.lines = text.splitlines()

    @staticmethod
    def _indent(raw: str) -> int:
        return len(raw) - len(raw.lstrip(" "))

    def _next_active(self, index: int) -> int:
        while index < len(self.lines):
            stripped = self.lines[index].lstrip(" ")
            if stripped and not stripped.startswith("#"):
                break
            index += 1
        return index

    @staticmethod
    def _pair(content: str) -> tuple[str, str]:
        match = re.fullmatch(r"([A-Za-z_][A-Za-z0-9_-]*):(.*)", content)
        if match is None:
            raise _SubsetError
        rest = match.group(2)
        if not rest:
            return match.group(1), ""
        if not rest.startswith(" ") or rest.startswith("  "):
            raise _SubsetError
        return match.group(1), rest[1:]

    def parse(self) -> dict[str, _Node]:
        start = self._next_active(0)
        if start >= len(self.lines) or self._indent(self.lines[start]) != 0:
            raise _SubsetError
        node, end = self._node(start, 0)
        if not isinstance(node, dict) or self._next_active(end) != len(self.lines):
            raise _SubsetError
        return node

    def _node(self, index: int, indentation: int) -> tuple[_Node, int]:
        index = self._next_active(index)
        if index >= len(self.lines) or self._indent(self.lines[index]) != indentation:
            raise _SubsetError
        if self.lines[index][indentation:].startswith("- "):
            return self._sequence(index, indentation)
        return self._mapping(index, indentation)

    def _value(
        self, index: int, key_indent: int, value: str
    ) -> tuple[_Node, int]:
        if _BLOCK_RE.fullmatch(value):
            return self._block(index, key_indent, value)
        if value:
            return value, index
        child = self._next_active(index)
        if child >= len(self.lines) or self._indent(self.lines[child]) <= key_indent:
            return {}, index
        if self._indent(self.lines[child]) != key_indent + 2:
            raise _SubsetError
        return self._node(child, key_indent + 2)

    def _block(
        self, index: int, key_indent: int, style: str
    ) -> tuple[_Block, int]:
        content_indent = key_indent + 2
        body: list[str] = []
        saw_content = False
        while index < len(self.lines):
            raw = self.lines[index]
            if not raw:
                if saw_content:
                    body.append("")
                index += 1
                continue
            indentation = self._indent(raw)
            if indentation <= key_indent:
                break
            if indentation < content_indent:
                raise _SubsetError
            body.append(raw[content_indent:])
            saw_content = True
            index += 1
        while body and body[-1] == "":
            body.pop()
        if not body:
            raise _SubsetError
        return _Block(style, tuple(body)), index

    def _mapping(
        self,
        index: int,
        indentation: int,
        initial: dict[str, _Node] | None = None,
    ) -> tuple[dict[str, _Node], int]:
        result = {} if initial is None else initial
        while True:
            current = self._next_active(index)
            if current >= len(self.lines):
                return result, current
            raw = self.lines[current]
            current_indent = self._indent(raw)
            if current_indent < indentation:
                return result, current
            if current_indent != indentation or raw[indentation:].startswith("- "):
                if current_indent == indentation:
                    raise _SubsetError
                return result, current
            key, value = self._pair(raw[indentation:])
            if key in result:
                raise _SubsetError
            node, index = self._value(current + 1, indentation, value)
            result[key] = node

    def _sequence(self, index: int, indentation: int) -> tuple[list[_Node], int]:
        result: list[_Node] = []
        while True:
            current = self._next_active(index)
            if current >= len(self.lines):
                return result, current
            raw = self.lines[current]
            current_indent = self._indent(raw)
            if current_indent < indentation:
                return result, current
            if current_indent != indentation or not raw[indentation:].startswith("- "):
                raise _SubsetError
            payload = raw[indentation + 2 :]
            if not payload:
                child = self._next_active(current + 1)
                if child >= len(self.lines) or self._indent(self.lines[child]) != indentation + 2:
                    raise _SubsetError
                node, index = self._node(child, indentation + 2)
                result.append(node)
                continue
            try:
                key, value = self._pair(payload)
            except _SubsetError:
                next_index = self._next_active(current + 1)
                if next_index < len(self.lines) and self._indent(self.lines[next_index]) > indentation:
                    raise _SubsetError
                result.append(payload)
                index = current + 1
                continue
            item: dict[str, _Node] = {}
            first, next_index = self._value(current + 1, indentation + 2, value)
            item[key] = first
            item, index = self._mapping(next_index, indentation + 2, item)
            result.append(item)


def _mapping(node: _Node, code: str) -> dict[str, _Node]:
    if not isinstance(node, dict):
        _fail(code)
    return node


def _sequence(node: _Node, code: str) -> list[_Node]:
    if not isinstance(node, list):
        _fail(code)
    return node


def _scalar(node: _Node, code: str) -> str:
    if not isinstance(node, str):
        _fail(code)
    return node


def _exact_keys(mapping: dict[str, _Node], expected: set[str], code: str) -> None:
    if set(mapping) != expected:
        _fail(code)


def _validate_source(text: object) -> str:
    if not isinstance(text, str) or not text or not text.endswith("\n"):
        _fail("invalid-input")
    try:
        encoded = text.encode("ascii")
    except UnicodeEncodeError:
        _fail("invalid-input")
    if len(encoded) > MAX_SOURCE_BYTES or any(char in text for char in ("\0", "\r", "\t")):
        _fail("invalid-input")
    for raw in text.splitlines():
        if len(raw) > 2_048 or raw.rstrip(" ") != raw:
            _fail("invalid-input")
        if raw and (len(raw) - len(raw.lstrip(" "))) % 2:
            _fail("invalid-input")
    return text


def _active_source_sha256(text: str) -> str:
    active_lines: list[str] = []
    block_indent: int | None = None
    for raw in text.splitlines():
        stripped = raw.lstrip(" ")
        indent = len(raw) - len(stripped)
        if block_indent is not None:
            if not stripped or indent > block_indent:
                active_lines.append(raw)
                continue
            block_indent = None

        if not stripped or stripped.startswith("#"):
            continue
        active_lines.append(raw)
        _, separator, value = stripped.partition(":")
        if separator and _BLOCK_RE.fullmatch(value.strip()):
            block_indent = indent

    active = "\n".join(active_lines) + "\n"
    return hashlib.sha256(active.encode("ascii")).hexdigest()


def _validate_triggers(node: _Node) -> None:
    triggers = _mapping(node, "trigger-boundary")
    _exact_keys(triggers, {"push", "pull_request", "workflow_dispatch"}, "trigger-boundary")
    for event in ("push", "pull_request"):
        event_map = _mapping(triggers[event], "trigger-boundary")
        _exact_keys(event_map, {"branches"}, "trigger-boundary")
        branches = _sequence(event_map["branches"], "trigger-boundary")
        if branches != [CANONICAL_BRANCH]:
            _fail("trigger-boundary")
    if triggers["workflow_dispatch"] != {}:
        _fail("trigger-boundary")


def _validate_permissions(node: _Node) -> None:
    permissions = _mapping(node, "permissions-boundary")
    if permissions != {"contents": "read"}:
        _fail("permissions-boundary")


def _active_run_lines(node: _Node) -> tuple[str, ...]:
    if isinstance(node, str):
        candidates = (node,)
    elif isinstance(node, _Block):
        if node.style != "|":
            _fail("step-boundary")
        candidates = node.lines
    else:
        _fail("step-boundary")
    return tuple(
        line
        for line in candidates
        if line.strip() and not line.lstrip().startswith("#")
    )


def _scan_commands(lines: tuple[str, ...]) -> None:
    network_words = re.compile(r"(?i)(?:^|[^A-Za-z0-9_])(curl|wget|ssh|scp|rsync|sftp|nc|ncat|socat|telnet)(?:$|[^A-Za-z0-9_])")
    command_words = re.compile(r"(?i)(?:^|[^A-Za-z0-9_])(systemctl|service|sudo|ufw|nft|iptables|firewall-cmd|kubectl)(?:$|[^A-Za-z0-9_])")
    safe_nonexecuting_lines = {
        "bash -n ops/ha/deploy-node.sh",
        "python -m py_compile ops/ha/pki_verify.py ops/ha/pki-verify.py ops/ha/deploy_node.py",
        'systemd-analyze --root="$verify_root" verify rqlited@s2.service rqlited@s3.service rqlited@s4.service maestro-panel.service',
    }
    for line in lines:
        if line in safe_nonexecuting_lines:
            continue
        lowered = line.lower()
        if network_words.search(line) or "http://" in lowered or "https://" in lowered:
            _fail("network-boundary")
        for value in _IPV4_RE.findall(line):
            if value != "127.0.0.1":
                _fail("network-boundary")
        if (
            command_words.search(line)
            or "secrets." in lowered
            or "deploy-node" in lowered
            or re.search(r"(?i)(?:^|[ /])deploy(?:[ /_.-]|$)", line)
        ):
            _fail("command-boundary")


def _validate_action(step: dict[str, _Node]) -> tuple[str, str]:
    allowed_keys = {"name", "uses", "with"}
    if "if" in step:
        allowed_keys.add("if")
    _exact_keys(step, allowed_keys, "step-boundary")
    name = _scalar(step["name"], "step-boundary")
    uses = _scalar(step["uses"], "action-boundary")
    action, separator, revision = uses.rpartition("@")
    pins = {
        "actions/checkout": CHECKOUT_SHA,
        "actions/setup-go": SETUP_GO_SHA,
        "actions/upload-artifact": UPLOAD_SHA,
    }
    if action not in pins or not separator:
        _fail("action-boundary")
    if _FULL_SHA_RE.fullmatch(revision) is None or revision != pins[action]:
        _fail("action-pin")
    options = _mapping(step["with"], "action-boundary")
    if action == "actions/checkout":
        if options != {"persist-credentials": "false"} or "if" in step:
            _fail("action-boundary")
    elif action == "actions/setup-go":
        if options != {
            "go-version-file": "backend/go.mod",
            "cache-dependency-path": "backend/go.sum",
        } or "if" in step:
            _fail("action-boundary")
    else:
        if name != "Upload immutable panel artifact":
            _fail("upload-boundary")
        if step.get("if") != "github.event_name != 'pull_request'":
            _fail("upload-boundary")
        expected_scalar = {
            "name": "maestro-panel-${{ github.sha }}",
            "if-no-files-found": "error",
            "compression-level": "0",
            "overwrite": "false",
            "include-hidden-files": "false",
        }
        if set(options) != set(expected_scalar) | {"path"}:
            _fail("upload-boundary")
        for key, value in expected_scalar.items():
            if options.get(key) != value:
                _fail("upload-boundary")
        path = options["path"]
        if not isinstance(path, _Block) or path.style != "|" or path.lines != (
            "dist/maestro-panel",
            "dist/manifest.json",
        ):
            _fail("upload-boundary")
    return action, name


def _required_run(
    runs: dict[str, tuple[dict[str, _Node], tuple[str, ...], int]],
    name: str,
    expected: tuple[str, ...],
    code: str,
) -> int:
    entry = runs.get(name)
    if entry is None:
        _fail(code)
    step, lines, index = entry
    if lines != expected:
        _fail(code)
    return index


def _validate_steps(node: _Node) -> None:
    raw_steps = _sequence(node, "job-boundary")
    steps: list[dict[str, _Node]] = []
    names: set[str] = set()
    actions: list[tuple[str, str, int]] = []
    runs: dict[str, tuple[dict[str, _Node], tuple[str, ...], int]] = {}
    all_commands: list[str] = []
    for index, raw_step in enumerate(raw_steps):
        step = _mapping(raw_step, "step-boundary")
        name = _scalar(step.get("name", {}), "step-boundary")
        if _STEP_NAME_RE.fullmatch(name) is None or name in names:
            _fail("step-boundary")
        names.add(name)
        steps.append(step)
        if "uses" in step:
            if "run" in step:
                _fail("step-boundary")
            action, action_name = _validate_action(step)
            actions.append((action, action_name, index))
            continue
        allowed = {"name", "env", "run"}
        if "working-directory" in step:
            allowed.add("working-directory")
        if "if" in step:
            allowed.add("if")
        _exact_keys(step, allowed, "step-boundary")
        lines = _active_run_lines(step["run"])
        runs[name] = (step, lines, index)
        all_commands.extend(lines)

    reviewed_run_names = {
        "Test build workflow policy",
        "Test offline PKI service and deploy contracts",
        "Test panel service runtime contract",
        "Test backend",
        "Race-test backend",
        "Vet backend",
        "Test isolated rqlite harness",
        "Start isolated rqlite cluster",
        "Verify pinned rqlite v10.1.0 service flags",
        "Test rqlite integration",
        "Stop isolated rqlite cluster",
        "Verify inert service units",
        "Build reproducible panel",
        "Create and verify manifest",
        "Assert exact artifact membership",
    }
    if not set(runs).issubset(reviewed_run_names):
        _fail("step-boundary")

    required_rqlite_lines = {
        "set -euo pipefail",
        'marker="$RUNNER_TEMP/maestro-rqlite-ci-root"',
        'runner_root="$(realpath -e -- "$RUNNER_TEMP")"',
        'cluster_root="$(realpath -e -- "$(cat -- "$marker")")"',
        'binary="$cluster_root/bin/rqlited"',
        'help_file="$RUNNER_TEMP/rqlited-v10.1.0-help.txt"',
        "trap 'rm -f -- \"$help_file\"' EXIT",
        '"$binary" -h >"$help_file" 2>&1',
        'text = Path(sys.argv[1]).read_text(encoding="utf-8")',
        '    "node-id", "fk", "http-addr", "http-adv-addr",',
        '    "http-ca-cert", "http-cert", "http-key", "http-verify-client",',
        '    "raft-addr", "raft-adv-addr", "node-ca-cert", "node-cert",',
        '    "node-key", "node-verify-client",',
        '        raise SystemExit("rqlite flag surface mismatch")',
    }
    rqlite_candidate = runs.get("Verify pinned rqlite v10.1.0 service flags")
    if rqlite_candidate is None or not required_rqlite_lines.issubset(rqlite_candidate[1]):
        _fail("rqlite-flags-boundary")

    _scan_commands(tuple(all_commands))

    checkout = [item for item in actions if item[0] == "actions/checkout"]
    setup_go = [item for item in actions if item[0] == "actions/setup-go"]
    uploads = [item for item in actions if item[0] == "actions/upload-artifact"]
    if len(checkout) != 1 or len(setup_go) != 1:
        _fail("action-boundary")
    if len(uploads) != 1:
        _fail("upload-boundary")
    if checkout[0][2] != 0 or setup_go[0][2] != 2 or uploads[0][2] != len(steps) - 1:
        _fail("action-boundary")
    if checkout[0][1] != "Checkout" or setup_go[0][1] != "Set up Go":
        _fail("action-boundary")

    exact_env = {"LC_ALL": "C"}

    self_index = _required_run(
        runs,
        "Test build workflow policy",
        (
            "set -euo pipefail",
            "python -m unittest ops.ha.tests.test_build_manifest ops.ha.tests.test_build_workflow_policy -v",
            "python ops/ha/build_workflow_policy.py",
        ),
        "self-policy",
    )
    self_step = runs["Test build workflow policy"][0]
    if set(self_step) != {"name", "env", "run"} or self_step.get("env") != exact_env:
        _fail("step-boundary")

    offline_index = _required_run(
        runs,
        "Test offline PKI service and deploy contracts",
        (
            "set -euo pipefail",
            "python -c \"from ops.ha import pki_verify as p; print('pki-openssl-version=' + p._openssl_version(p.default_openssl_runner))\"",
            "python -m unittest ops.ha.tests.test_pki_verify ops.ha.tests.test_service_templates ops.ha.tests.test_deploy_node -v",
            "python -m py_compile ops/ha/pki_verify.py ops/ha/pki-verify.py ops/ha/deploy_node.py",
            "bash -n ops/ha/deploy-node.sh",
        ),
        "offline-contract-boundary",
    )
    offline_step = runs["Test offline PKI service and deploy contracts"][0]
    if (
        set(offline_step) != {"name", "env", "run"}
        or offline_step.get("env")
        != {"LC_ALL": "C", "TMPDIR": "${{ runner.temp }}"}
    ):
        _fail("offline-contract-boundary")

    runtime_index = _required_run(
        runs,
        "Test panel service runtime contract",
        (
            "go test ./cmd/maestro-panel -run '^TestHAServiceTemplateRuntimeContract$' -count=1",
        ),
        "runtime-contract-boundary",
    )
    runtime_step = runs["Test panel service runtime contract"][0]
    if (
        set(runtime_step) != {"name", "working-directory", "env", "run"}
        or runtime_step.get("working-directory") != "backend"
        or runtime_step.get("env") != exact_env
    ):
        _fail("runtime-contract-boundary")

    backend_index = _required_run(
        runs,
        "Test backend",
        ("env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 ./...",),
        "backend-boundary",
    )
    race_index = _required_run(
        runs,
        "Race-test backend",
        (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 -race ./...",
        ),
        "backend-boundary",
    )
    vet_index = _required_run(
        runs,
        "Vet backend",
        ("env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go vet ./...",),
        "backend-boundary",
    )
    for backend_name in ("Test backend", "Race-test backend", "Vet backend"):
        backend_step = runs[backend_name][0]
        if (
            set(backend_step) != {"name", "working-directory", "env", "run"}
            or backend_step.get("working-directory") != "backend"
            or backend_step.get("env") != exact_env
        ):
            _fail("backend-boundary")

    harness_index = _required_run(
        runs,
        "Test isolated rqlite harness",
        ("bash ops/ha/test-ci-rqlite-cluster.sh",),
        "step-boundary",
    )
    harness_step = runs["Test isolated rqlite harness"][0]
    if set(harness_step) != {"name", "env", "run"} or harness_step.get("env") != exact_env:
        _fail("step-boundary")
    start_index = _required_run(
        runs,
        "Start isolated rqlite cluster",
        ("bash ops/ha/ci-rqlite-cluster.sh start",),
        "step-boundary",
    )
    start_step = runs["Start isolated rqlite cluster"][0]
    if set(start_step) != {"name", "env", "run"} or start_step.get("env") != exact_env:
        _fail("step-boundary")
    rqlite_flags_index = _required_run(
        runs,
        "Verify pinned rqlite v10.1.0 service flags",
        runs.get("Verify pinned rqlite v10.1.0 service flags", ({}, (), -1))[1],
        "rqlite-flags-boundary",
    )
    rqlite_step, rqlite_lines, _ = runs["Verify pinned rqlite v10.1.0 service flags"]
    if (
        set(rqlite_step) != {"name", "env", "run"}
        or rqlite_step.get("env") != exact_env
    ):
        _fail("rqlite-flags-boundary")
    if not required_rqlite_lines.issubset(rqlite_lines):
        _fail("rqlite-flags-boundary")
    integration_index = _required_run(
        runs,
        "Test rqlite integration",
        (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 -tags=rqlite_integration ./...",
        ),
        "step-boundary",
    )
    integration = runs["Test rqlite integration"][0]
    if (
        set(integration) != {"name", "working-directory", "env", "run"}
        or integration.get("working-directory") != "backend"
        or integration.get("env") != exact_env
    ):
        _fail("step-boundary")
    cleanup_index = _required_run(
        runs,
        "Stop isolated rqlite cluster",
        ("bash ops/ha/ci-rqlite-cluster.sh stop",),
        "cleanup-boundary",
    )
    cleanup = runs["Stop isolated rqlite cluster"][0]
    if (
        set(cleanup) != {"name", "if", "env", "run"}
        or cleanup.get("if")
        != "${{ always() && hashFiles('ops/ha/ci-rqlite-cluster.sh') != '' }}"
        or cleanup.get("env") != exact_env
    ):
        _fail("cleanup-boundary")

    systemd_index = _required_run(
        runs,
        "Verify inert service units",
        runs.get("Verify inert service units", ({}, (), -1))[1],
        "systemd-boundary",
    )
    systemd_step, systemd_lines, _ = runs["Verify inert service units"]
    if (
        set(systemd_step) != {"name", "env", "run"}
        or systemd_step.get("env")
        != {
            "LC_ALL": "C",
            "TEMPLATE_ROOT": "deploy/ha",
            "UNIT_SUFFIX": "service",
        }
    ):
        _fail("systemd-boundary")
    required_systemd_lines = {
        "set -euo pipefail",
        'verify_root="$RUNNER_TEMP/maestro-systemd-verify"',
        'rqlite_unit="rqlited@.$UNIT_SUFFIX"',
        'panel_unit="maestro-panel.$UNIT_SUFFIX"',
        "trap 'rm -rf -- \"$verify_root\"' EXIT",
        'install -d -m 0755 -- "$verify_root/etc/systemd/system" "$verify_root/etc/maestro/ha" "$verify_root/etc/maestro/ha/pki" "$verify_root/etc/maestro/ha/secrets"',
        'install -m 0644 -- "$TEMPLATE_ROOT/$rqlite_unit" "$verify_root/etc/systemd/system/$rqlite_unit"',
        'install -m 0644 -- "$TEMPLATE_ROOT/$panel_unit" "$verify_root/etc/systemd/system/$panel_unit"',
        'install -m 0644 -- "$TEMPLATE_ROOT/maestro-panel.env.example" "$verify_root/etc/maestro/ha/maestro-panel.env"',
        'install -m 0755 -- /bin/true "$verify_root/opt/maestro/ha/rqlite/10.1.0/rqlited"',
        'install -m 0755 -- /bin/true "$verify_root/opt/maestro/ha/releases/f577c67ad229fe89278430d35a3ec65f6ce454e5/maestro-panel"',
        "for target in basic network-online shutdown sysinit; do",
        '  printf \'%s\\n\' \'[Unit]\' "Description=synthetic $target target" \'DefaultDependencies=no\' >"$verify_root/etc/systemd/system/$target.target"',
        'systemd-analyze --root="$verify_root" verify rqlited@s2.service rqlited@s3.service rqlited@s4.service maestro-panel.service',
    }
    if not required_systemd_lines.issubset(systemd_lines):
        _fail("systemd-boundary")

    build_index = _required_run(
        runs,
        "Build reproducible panel",
        runs.get("Build reproducible panel", ({}, (), -1))[1],
        "build-boundary",
    )
    build_step, build_lines, _ = runs["Build reproducible panel"]
    if (
        set(build_step) != {"name", "working-directory", "env", "run"}
        or build_step.get("working-directory") != "backend"
        or build_step.get("env") != exact_env
    ):
        _fail("build-boundary")
    if (
        build_lines.count("CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \\") != 2
        or '  -o "$build_a/maestro-panel" ./cmd/maestro-panel' not in build_lines
        or '  -o "$build_b/maestro-panel" ./cmd/maestro-panel' not in build_lines
    ):
        _fail("build-boundary")
    if 'cmp -- "$build_a/maestro-panel" "$build_b/maestro-panel"' not in build_lines:
        _fail("reproducibility-boundary")
    if (
        'go version -m "$build_a/maestro-panel" > "$RUNNER_TEMP/maestro-panel.buildinfo"'
        not in build_lines
        or '    ("vcs.modified", "false"),' not in build_lines
    ):
        _fail("metadata-boundary")

    manifest_index = _required_run(
        runs,
        "Create and verify manifest",
        runs.get("Create and verify manifest", ({}, (), -1))[1],
        "manifest-boundary",
    )
    manifest_step, manifest_lines, _ = runs["Create and verify manifest"]
    if set(manifest_step) != {"name", "env", "run"} or manifest_step.get("env") != exact_env:
        _fail("manifest-boundary")
    if (
        "python ops/ha/build_manifest.py create \\" not in manifest_lines
        or "python ops/ha/build_manifest.py verify \\" not in manifest_lines
    ):
        _fail("manifest-boundary")

    membership_index = _required_run(
        runs,
        "Assert exact artifact membership",
        runs.get("Assert exact artifact membership", ({}, (), -1))[1],
        "membership-boundary",
    )
    membership_step, membership_lines, _ = runs["Assert exact artifact membership"]
    if set(membership_step) != {"name", "env", "run"} or membership_step.get("env") != exact_env:
        _fail("membership-boundary")
    if 'expected_names = ["maestro-panel", "manifest.json"]' not in membership_lines:
        _fail("membership-boundary")
    for name, (step, lines, _) in runs.items():
        if name != "Stop isolated rqlite cluster" and "if" in step:
            _fail("step-boundary")
        if any("ci-rqlite-cluster.sh" in line for line in lines) and name not in {
            "Test isolated rqlite harness",
            "Start isolated rqlite cluster",
            "Stop isolated rqlite cluster",
        }:
            _fail("step-boundary")
    if not (
        self_index
        < setup_go[0][2]
        < offline_index
        < runtime_index
        < backend_index
        < race_index
        < vet_index
        < harness_index
        < start_index
        < rqlite_flags_index
        < integration_index
        < cleanup_index
        < systemd_index
        < build_index
        < manifest_index
        < membership_index
        < uploads[0][2]
    ):
        _fail("step-boundary")
    expected_names = (
        "Checkout",
        "Test build workflow policy",
        "Set up Go",
        "Test offline PKI service and deploy contracts",
        "Test panel service runtime contract",
        "Test backend",
        "Race-test backend",
        "Vet backend",
        "Test isolated rqlite harness",
        "Start isolated rqlite cluster",
        "Verify pinned rqlite v10.1.0 service flags",
        "Test rqlite integration",
        "Stop isolated rqlite cluster",
        "Verify inert service units",
        "Build reproducible panel",
        "Create and verify manifest",
        "Assert exact artifact membership",
        "Upload immutable panel artifact",
    )
    if tuple(_scalar(step["name"], "step-boundary") for step in steps) != expected_names:
        _fail("step-boundary")


def validate_workflow(text: str) -> None:
    """Validate active security behavior of ``ha-build.yml``."""

    source = _validate_source(text)
    try:
        document = _SubsetParser(source).parse()
    except _SubsetError:
        _fail("invalid-structure")

    _exact_keys(
        document,
        {"name", "on", "permissions", "concurrency", "jobs"},
        "invalid-structure",
    )
    if document["name"] != "HA immutable panel artifact":
        _fail("invalid-structure")
    _validate_triggers(document["on"])
    _validate_permissions(document["permissions"])

    concurrency = _mapping(document["concurrency"], "concurrency-boundary")
    if concurrency != {
        "group": "ha-build-${{ github.workflow }}-${{ github.ref }}",
        "cancel-in-progress": "false",
    }:
        _fail("concurrency-boundary")

    jobs = _mapping(document["jobs"], "job-boundary")
    if set(jobs) != {"build-panel-artifact"}:
        _fail("job-boundary")
    job = _mapping(jobs["build-panel-artifact"], "job-boundary")
    if "timeout-minutes" not in job:
        _fail("timeout-boundary")
    _exact_keys(
        job,
        {"name", "runs-on", "timeout-minutes", "permissions", "steps"},
        "job-boundary",
    )
    if job["name"] != "Build immutable panel artifact":
        _fail("job-boundary")
    if job["runs-on"] != "ubuntu-24.04":
        _fail("runner-boundary")
    if job["timeout-minutes"] != "45":
        _fail("timeout-boundary")
    _validate_permissions(job["permissions"])
    _validate_steps(job["steps"])
    if _active_source_sha256(source) != APPROVED_ACTIVE_SHA256:
        _fail("step-boundary")


def _metadata_fingerprint(metadata: os.stat_result) -> tuple[int, ...]:
    return (
        metadata.st_dev,
        metadata.st_ino,
        metadata.st_mode,
        metadata.st_nlink,
        metadata.st_size,
        metadata.st_mtime_ns,
        metadata.st_ctime_ns,
    )


def _read_workflow_source() -> str:
    before = WORKFLOW.lstat()
    if (
        not stat.S_ISREG(before.st_mode)
        or before.st_nlink != 1
        or not 0 < before.st_size <= MAX_SOURCE_BYTES
    ):
        raise OSError
    flags = os.O_RDONLY
    for option in ("O_CLOEXEC", "O_NOFOLLOW", "O_NONBLOCK", "O_BINARY"):
        flags |= getattr(os, option, 0)
    descriptor = os.open(os.fspath(WORKFLOW), flags)
    try:
        opened = os.fstat(descriptor)
        if (
            not stat.S_ISREG(opened.st_mode)
            or opened.st_nlink != 1
            or _metadata_fingerprint(opened) != _metadata_fingerprint(before)
        ):
            raise OSError
        chunks: list[bytes] = []
        remaining = MAX_SOURCE_BYTES + 1
        while remaining:
            chunk = os.read(descriptor, min(65_536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        payload = b"".join(chunks)
        after = os.fstat(descriptor)
        if (
            len(payload) != opened.st_size
            or len(payload) > MAX_SOURCE_BYTES
            or _metadata_fingerprint(after) != _metadata_fingerprint(opened)
        ):
            raise OSError
        path_after = os.lstat(os.fspath(WORKFLOW))
        if _metadata_fingerprint(path_after) != _metadata_fingerprint(opened):
            raise OSError
    finally:
        os.close(descriptor)
    return payload.decode("utf-8", errors="strict")


def main() -> int:
    try:
        source = _read_workflow_source()
    except (OSError, UnicodeError):
        print(POLICY_PREFIX + "source-unavailable", file=sys.stderr)
        return 1
    except Exception:
        print(POLICY_PREFIX + "internal-failure", file=sys.stderr)
        return 1
    try:
        validate_workflow(source)
    except WorkflowPolicyError as error:
        print(str(error), file=sys.stderr)
        return 1
    except Exception:
        print(POLICY_PREFIX + "internal-failure", file=sys.stderr)
        return 1
    print("HA build workflow policy passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
