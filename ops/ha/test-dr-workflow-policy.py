#!/usr/bin/env python3
"""Fail-closed policy checks for the repository-only DR workflow."""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "ha-dr-restore-drill.yml"

REQUIRED_STEPS = (
    "Check Go formatting and shell syntax",
    "Test backend",
    "Race-test backend",
    "Vet backend",
    "Test DR workflow policy",
    "Test authenticated backup verification and tamper matrix",
    "Test fresh restore fencing parity and quorum",
    "Stop isolated rqlite cluster",
)


def fail(message: str) -> None:
    raise AssertionError(message)


def main() -> int:
    if not WORKFLOW.is_file():
        fail("dedicated DR workflow is absent")
    text = WORKFLOW.read_text(encoding="utf-8")
    lowered = text.lower()

    if not re.search(r"(?m)^permissions:\s*\n\s{2}contents:\s*read\s*$", text):
        fail("workflow permissions must be contents: read only")
    if "runs-on: ubuntu-24.04" not in text:
        fail("workflow must use ubuntu-24.04")
    if not re.search(r"(?m)^\s+timeout-minutes:\s*(?:[1-9]|[1-5][0-9]|60)\s*$", text):
        fail("workflow job needs a bounded timeout of at most 60 minutes")
    if not re.search(
        r"(?ms)^concurrency:\s*\n\s+group:\s*[^\n]+\n\s+cancel-in-progress:\s*false\s*$",
        text,
    ):
        fail("workflow concurrency must preserve an in-progress DR proof")

    uses = re.findall(r"(?m)^\s*-?\s*uses:\s*[^@\s]+@([^\s#]+)", text)
    if not uses or any(re.fullmatch(r"[0-9a-f]{40}", ref) is None for ref in uses):
        fail("every action must be pinned to a full 40-hex commit")
    for forbidden in ("environment:", "secrets.", "upload-artifact", "self-hosted"):
        if forbidden in lowered:
            fail(f"forbidden workflow capability: {forbidden}")

    positions = []
    for name in REQUIRED_STEPS:
        marker = f"- name: {name}"
        position = text.find(marker)
        if position < 0:
            fail(f"missing named DR gate: {name}")
        positions.append(position)
    if positions != sorted(positions):
        fail("DR gates are out of order")

    cleanup = re.search(
        r"(?ms)- name: Stop isolated rqlite cluster\s*\n"
        r"\s+if:\s*\$\{\{\s*always\(\).*?\}\}\s*\n"
        r"\s+run:\s*bash ops/ha/ci-rqlite-cluster\.sh stop",
        text,
    )
    if cleanup is None:
        fail("DR workflow lacks unconditional bounded cluster cleanup")

    if "python ops/ha/test-dr-workflow-policy.py" not in text:
        fail("workflow does not execute its own policy test")
    if "bash ops/ha/test-backup-rqlite.sh" not in text:
        fail("workflow does not execute authenticated backup proof")
    if "bash ops/ha/test-restore-rqlite.sh" not in text:
        fail("workflow does not execute fresh restore/fencing proof")
    print("DR workflow policy passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as error:
        print(f"DR workflow policy failed: {error}", file=sys.stderr)
        raise SystemExit(1)
