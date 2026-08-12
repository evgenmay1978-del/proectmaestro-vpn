#!/usr/bin/env python3
"""Fail-closed repository policy for the apply-agent plaintext boundary."""

from __future__ import annotations

import re
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = (
    ROOT / ".github" / "workflows" / "ha-control-plane.yml",
    ROOT / ".github" / "workflows" / "ha-dr-restore-drill.yml",
)
GO_ROOTS = (
    ROOT / "backend" / "internal" / "applyagent",
    ROOT / "backend" / "internal" / "controlplane",
)
FORBIDDEN_DRIVER_SIGNATURE = re.compile(
    r"\b(?P<method>Inspect|Prepare)\s*\(\s*context\.Context\s*,\s*DesiredSnapshot\s*\)"
)
SENSITIVE_SINK = re.compile(
    r"\b(?:fmt\.(?:Errorf|Fprint|Fprintf|Fprintln|Print|Printf|Println|Sprint|Sprintf|Sprintln)"
    r"|log\.(?:Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln|Print|Printf|Println)"
    r"|slog\.(?:Debug|DebugContext|Error|ErrorContext|Info|InfoContext|Log|LogAttrs|Warn|WarnContext)"
    r"|json\.(?:Marshal|MarshalIndent))\s*\(|\.\s*Encode\s*\("
)
BODY_REFERENCE = re.compile(r"(?:\.\s*Body\b|\bMaterializedEntry\s*\{[^}]*\bBody\s*:)", re.DOTALL)
WORKFLOW_SNIPPETS = (
    "python ops/ha/test-agent-payload-policy.py",
    "find internal/applyagent internal/controlplane -type f -name '*.go' -print0",
    "LC_ALL=C sort -z",
    'gofmt -w "${guarded_go_files[@]}"',
    "go test ./internal/applyagent ./internal/controlplane",
)


def _call_arguments(text: str, open_paren: int) -> str:
    depth = 0
    index = open_paren
    state = "code"
    while index < len(text):
        char = text[index]
        next_char = text[index + 1] if index + 1 < len(text) else ""
        if state == "line-comment":
            if char == "\n":
                state = "code"
        elif state == "block-comment":
            if char == "*" and next_char == "/":
                state = "code"
                index += 1
        elif state == "quoted":
            if char == "\\":
                index += 1
            elif char == '"':
                state = "code"
        elif state == "rune":
            if char == "\\":
                index += 1
            elif char == "'":
                state = "code"
        elif state == "raw":
            if char == "`":
                state = "code"
        elif char == "/" and next_char == "/":
            state = "line-comment"
            index += 1
        elif char == "/" and next_char == "*":
            state = "block-comment"
            index += 1
        elif char == '"':
            state = "quoted"
        elif char == "'":
            state = "rune"
        elif char == "`":
            state = "raw"
        elif char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
            if depth == 0:
                return text[open_paren + 1 : index]
        index += 1
    return text[open_paren + 1 :]


def scan_go_file(path: Path, text: str, *, production: bool) -> list[str]:
    violations: list[str] = []
    for match in FORBIDDEN_DRIVER_SIGNATURE.finditer(text):
        violations.append(f"legacy-driver-{match.group('method').lower()}")
    if production:
        for match in SENSITIVE_SINK.finditer(text):
            arguments = _call_arguments(text, text.find("(", match.start()))
            if BODY_REFERENCE.search(arguments):
                violations.append("materialized-body-sink")
    return violations


def scan_repository(root: Path = ROOT) -> list[str]:
    violations: list[str] = []
    for relative_root in (
        Path("backend/internal/applyagent"),
        Path("backend/internal/controlplane"),
    ):
        source_root = root / relative_root
        if not source_root.is_dir():
            violations.append(f"missing-source-root:{relative_root.as_posix()}")
            continue
        for path in sorted(source_root.rglob("*.go")):
            relative = path.relative_to(root).as_posix()
            production = relative_root.name == "applyagent" and not path.name.endswith("_test.go")
            for code in scan_go_file(path, path.read_text(encoding="utf-8"), production=production):
                violations.append(f"{relative}:{code}")
    return violations


def assert_synthetic_forbidden_fixture() -> None:
    fixture = """package applyagent

import (
    "context"
    "encoding/json"
    "log"
)

type forbiddenDriver interface {
    Inspect(context.Context, DesiredSnapshot) (AppliedState, error)
    Prepare(context.Context, DesiredSnapshot) (PreparedChange, error)
}

func leakMaterializedBody(entry MaterializedEntry) {
    log.Printf("synthetic body: %s", entry.Body)
    _, _ = json.Marshal(entry.Body)
}
"""
    with tempfile.TemporaryDirectory(prefix="maestro-agent-policy-") as directory:
        path = Path(directory) / "forbidden.go"
        path.write_text(fixture, encoding="utf-8")
        found = scan_go_file(path, fixture, production=True)
    required = {
        "legacy-driver-inspect",
        "legacy-driver-prepare",
        "materialized-body-sink",
    }
    if not required.issubset(set(found)):
        raise AssertionError("synthetic forbidden fixture was not fully rejected")


def assert_workflow_contract(root: Path = ROOT) -> None:
    for workflow in (
        root / ".github" / "workflows" / "ha-control-plane.yml",
        root / ".github" / "workflows" / "ha-dr-restore-drill.yml",
    ):
        if not workflow.is_file():
            raise AssertionError(f"missing workflow: {workflow.name}")
        text = workflow.read_text(encoding="utf-8")
        for snippet in WORKFLOW_SNIPPETS:
            if snippet not in text:
                raise AssertionError(f"{workflow.name} lacks deterministic agent boundary gate")


def main() -> int:
    assert_synthetic_forbidden_fixture()
    violations = scan_repository()
    if violations:
        raise AssertionError("agent payload policy violations: " + ", ".join(violations))
    assert_workflow_contract()
    print("Agent payload boundary policy passed")
    return 0
