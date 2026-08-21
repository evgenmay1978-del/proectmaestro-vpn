#!/usr/bin/env python3
"""Fail-closed repository policy for the apply-agent plaintext boundary."""

from __future__ import annotations

import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ANALYZER = ROOT / "ops" / "ha" / "agent_payload_policy_ast.go"
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


def run_analyzer(*arguments: str, root: Path = ROOT) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["go", "run", str(ANALYZER), *arguments],
        cwd=root,
        text=True,
        capture_output=True,
        check=False,
    )


def assert_synthetic_forbidden_fixture() -> None:
    fixture = """package applyagent

import (
    "bytes"
    "context"
    enc "encoding/json"
    "fmt"
    logger "log"
    slog "log/slog"
)

type forbiddenDriver interface {
    Inspect(ctx context.Context, desired DesiredSnapshot) (AppliedState, error)
    Prepare(ctx context.Context, desired DesiredSnapshot) (PreparedChange, error)
}

func reveal(entry MaterializedEntry) any { return entry.Body }
func relay(entry MaterializedEntry) any { return reveal(entry) }

func leakMaterializedBody(entry MaterializedEntry, snapshot MaterializedSnapshot) {
    secret := entry.Body
    alias := secret
    logger.Printf("synthetic body: %s", alias)
    _ = fmt.Errorf("synthetic body: %s", alias)
    _, _ = enc.Marshal(entry)
    _, _ = enc.Marshal(snapshot)
    holder := struct{ Secret any }{}
    holder.Secret = entry.Body
    logger.Printf("synthetic holder: %v", holder.Secret)
    logger.Printf("synthetic helper: %v", relay(entry))
    var buffer bytes.Buffer
    encoder := enc.NewEncoder(&buffer)
    _ = encoder.Encode(snapshot)
    _ = enc.NewEncoder(&buffer).Encode(entry.Body)
    logger.New(&buffer, "", 0).Printf("synthetic logger: %v", entry.Body)
    receiver := logger.New(&buffer, "", 0)
    receiver.Print(entry.Body)
    handler := slog.NewTextHandler(&buffer, nil)
    slog.New(handler).Info("synthetic slog", "body", entry.Body)
    slogReceiver := slog.New(handler)
    slogReceiver.Error("synthetic slog", "snapshot", snapshot)
}

type unrelated struct{}
func (unrelated) Error(any) {}
func permitted(entry MaterializedEntry, err error) {
    unrelated{}.Error(entry.Body)
    _ = fmt.Errorf("ordinary error: %w", err)
}
"""
    permitted = """package applyagent
// logger.Printf("synthetic: %s", entry.Body)
var note = `enc.Marshal(entry.Body)`
"""
    with tempfile.TemporaryDirectory(prefix="maestro-agent-policy-") as directory:
        path = Path(directory) / "forbidden.go"
        path.write_text(fixture, encoding="utf-8")
        found = run_analyzer("--file", str(path), "--production")
        permitted_path = Path(directory) / "permitted.go"
        permitted_path.write_text(permitted, encoding="utf-8")
        permitted_found = run_analyzer("--file", str(permitted_path), "--production")
    required = ("legacy-driver-inspect", "legacy-driver-prepare", "materialized-body-sink")
    if found.returncode != 1 or not all(item in found.stdout for item in required):
        raise AssertionError("synthetic forbidden fixture was not fully rejected")
    if permitted_found.returncode != 0:
        raise AssertionError("permitted syntax triggered the payload policy")


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
    result = run_analyzer("--root", str(ROOT))
    if result.returncode != 0:
        detail = result.stdout.strip() or result.stderr.strip() or "analyzer execution failed"
        raise AssertionError("agent payload policy violations: " + detail)
    assert_workflow_contract()
    print("Agent payload boundary policy passed")
    return 0
