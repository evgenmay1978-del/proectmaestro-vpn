#!/usr/bin/env python3
"""Fail-closed repository policy for the apply-agent plaintext boundary."""

from __future__ import annotations

import re
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FORBIDDEN_DRIVER_SIGNATURE = re.compile(
    r"\b(?P<method>Inspect|Prepare)\s*\([^)]*\bDesiredSnapshot\b[^)]*\)", re.DOTALL
)
SENSITIVE_SINK = re.compile(
    r"(?:\b(?:Errorf|Fprint|Fprintf|Fprintln|Print|Printf|Println|Sprint|Sprintf|Sprintln"
    r"|Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln|Debug|DebugContext|Error|ErrorContext"
    r"|Info|InfoContext|Log|LogAttrs|Warn|WarnContext|Marshal|MarshalIndent)"
    r"|\.\s*Encode)\s*\("
)
WORKFLOW_SNIPPETS = (
    "python ops/ha/test-agent-payload-policy.py",
    "find internal/applyagent internal/controlplane -type f -name '*.go' -print0",
    "LC_ALL=C sort -z",
    "noncanonical_go_files",
    "tr '\\n' '\\0'",
    "cmp - <(printf '%s\\0'",
    'gofmt -w "${guarded_go_files[@]}"',
    "go test ./internal/applyagent ./internal/controlplane",
)


def _code_only(text: str) -> str:
    output = list(text)
    index = 0
    state = "code"
    while index < len(text):
        char = text[index]
        next_char = text[index + 1] if index + 1 < len(text) else ""
        if state == "code":
            if char == "/" and next_char == "/":
                output[index] = output[index + 1] = " "
                state = "line-comment"
                index += 1
            elif char == "/" and next_char == "*":
                output[index] = output[index + 1] = " "
                state = "block-comment"
                index += 1
            elif char in ('"', "'", "`"):
                output[index] = " "
                state = {"\"": "quoted", "'": "rune", "`": "raw"}[char]
        elif state == "line-comment":
            if char == "\n":
                state = "code"
            else:
                output[index] = " "
        elif state == "block-comment":
            output[index] = " " if char != "\n" else "\n"
            if char == "*" and next_char == "/":
                output[index + 1] = " "
                state = "code"
                index += 1
        elif state in ("quoted", "rune"):
            output[index] = " " if char != "\n" else "\n"
            if char == "\\":
                if index + 1 < len(text):
                    output[index + 1] = " " if text[index + 1] != "\n" else "\n"
                    index += 1
            elif (state == "quoted" and char == '"') or (state == "rune" and char == "'"):
                state = "code"
        elif state == "raw":
            output[index] = " " if char != "\n" else "\n"
            if char == "`":
                state = "code"
        index += 1
    return "".join(output)


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


def _body_tainted_names(code: str) -> set[str]:
    tainted: set[str] = set()
    statements = re.split(r"[;\n]", code)
    changed = True
    while changed:
        changed = False
        for statement in statements:
            assignment = re.search(r"(?:^|\s)([A-Za-z_]\w*)\s*(?::=|=)\s*(.+)$", statement)
            if assignment is None:
                continue
            name, value = assignment.groups()
            carries_body = (
                re.search(r"\.\s*Body\b|\bMaterialized(?:Entry|Snapshot)\b", value) is not None
            ) or any(
                re.search(rf"\b{re.escape(source)}\b", value) for source in tainted
            )
            if carries_body and name not in tainted:
                tainted.add(name)
                changed = True
    return tainted


def scan_go_file(path: Path, text: str, *, production: bool) -> list[str]:
    del path
    code = _code_only(text)
    violations: list[str] = []
    for match in FORBIDDEN_DRIVER_SIGNATURE.finditer(code):
        violations.append(f"legacy-driver-{match.group('method').lower()}")
    if production:
        tainted = _body_tainted_names(code)
        for match in SENSITIVE_SINK.finditer(code):
            open_paren = code.find("(", match.start())
            arguments = _call_arguments(code, open_paren)
            direct_body = re.search(
                r"\.\s*Body\b|\bMaterialized(?:Entry|Snapshot)\s*\{", arguments
            ) is not None
            aliased_body = any(re.search(rf"\b{re.escape(name)}\b", arguments) for name in tainted)
            if direct_body or aliased_body:
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
    secret := entry.Body
    alias := secret
    log.Printf("synthetic body: %s", alias)
    _, _ = json.Marshal(secret)
    snapshot := MaterializedSnapshot{}
    _, _ = json.Marshal(snapshot)
}
"""
    permitted = "// log.Printf(\"synthetic: %s\", entry.Body)\nvar note = `json.Marshal(entry.Body)`\n"
    with tempfile.TemporaryDirectory(prefix="maestro-agent-policy-") as directory:
        path = Path(directory) / "forbidden.go"
        path.write_text(fixture, encoding="utf-8")
        found = scan_go_file(path, fixture, production=True)
        permitted_found = scan_go_file(path, permitted, production=True)
    required = {
        "legacy-driver-inspect",
        "legacy-driver-prepare",
        "materialized-body-sink",
    }
    if not required.issubset(set(found)):
        raise AssertionError("synthetic forbidden fixture was not fully rejected")
    if permitted_found:
        raise AssertionError("comments or string literals triggered the payload policy")


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
