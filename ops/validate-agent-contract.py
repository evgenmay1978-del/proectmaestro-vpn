#!/usr/bin/env python3
"""Fail closed when the MaestroVPN handoff/agent contract loses key safeguards."""

from __future__ import annotations

import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]

REQUIRED: dict[str, tuple[str, ...]] = {
    "AGENTS.md": (
        "CONTEXT_HANDOFF.md",
        "SERVER_RUNBOOK.md",
        ".agents/skills/project-master/SKILL.md",
        "ops/maestro-repetition-guard.py",
    ),
    ".agents/skills/project-master/SKILL.md": (
        "systemctl show",
        "ExecStart",
        "WorkingDirectory",
        "MAESTRO_STORE",
        "SQLite backup API",
        "x-ui.service",
        "3x-ui",
        "S-ui",
        "getWebhookInfo",
        "getUpdates",
        "one poller",
        "mode 600",
        "$LASTEXITCODE",
        "rqlite",
    ),
    "SERVER_RUNBOOK.md": (
        "2026-08-11",
        "S1",
        "S2",
        "S3",
        "S4",
        "maestro-panel.service",
        "vpn_bot.service",
        "x-ui.service",
        "WDTT",
        "olcRTC",
        "not high availability",
        "read-only",
    ),
    "BACKLOG.md": (
        "WDTT",
        "1.0.153",
        "1.0.154",
        "eyelashes",
        "protocol arc",
        "rqlite",
        "idempotency",
        "Telegram",
    ),
    "CONTEXT_HANDOFF.md": (
        "## 0S. LIVE",
        "SERVER_RUNBOOK.md",
        "BACKLOG.md",
        "not high availability",
    ),
}

SECRET_PATTERNS = (
    re.compile(r"-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----"),
    re.compile(r"\b\d{8,12}:[A-Za-z0-9_-]{30,}\b"),
    re.compile(
        r"(?im)^\s*(?:BOT_TOKEN|PASSWORD|PANEL_PASSWORD|SSH_PASSWORD)\s*=\s*(?!<|REDACTED)\S+"
    ),
)

ALLOWED_FRONTMATTER_KEYS = {"name", "description", "license", "allowed-tools", "metadata"}


def validate_skill_frontmatter(text: str, failures: list[str]) -> None:
    """Validate this project's flat skill frontmatter without optional PyYAML."""
    lines = text.splitlines()
    if not lines or lines[0] != "---":
        failures.append("project-master SKILL.md: no YAML frontmatter")
        return
    try:
        closing = lines.index("---", 1)
    except ValueError:
        failures.append("project-master SKILL.md: unterminated YAML frontmatter")
        return

    frontmatter: dict[str, str] = {}
    for line in lines[1:closing]:
        if not line.strip():
            continue
        if ":" not in line or line[:1].isspace():
            failures.append(f"project-master SKILL.md: unsupported frontmatter line {line!r}")
            continue
        key, value = line.split(":", 1)
        frontmatter[key.strip()] = value.strip()

    unexpected = set(frontmatter) - ALLOWED_FRONTMATTER_KEYS
    if unexpected:
        failures.append(f"project-master SKILL.md: unexpected frontmatter keys {sorted(unexpected)}")
    name = frontmatter.get("name", "")
    description = frontmatter.get("description", "")
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name) or len(name) > 64:
        failures.append("project-master SKILL.md: invalid name")
    if not description or len(description) > 1024 or "<" in description or ">" in description:
        failures.append("project-master SKILL.md: invalid description")


def main() -> int:
    failures: list[str] = []
    loaded: dict[str, str] = {}

    for relative, needles in REQUIRED.items():
        path = ROOT / relative
        if not path.is_file():
            failures.append(f"missing file: {relative}")
            continue
        text = path.read_text(encoding="utf-8")
        loaded[relative] = text
        for needle in needles:
            if needle not in text:
                failures.append(f"{relative}: missing contract text {needle!r}")

    for relative, text in loaded.items():
        for pattern in SECRET_PATTERNS:
            if pattern.search(text):
                failures.append(f"{relative}: possible committed secret ({pattern.pattern})")

    skill_text = loaded.get(".agents/skills/project-master/SKILL.md")
    if skill_text is not None:
        validate_skill_frontmatter(skill_text, failures)

    if failures:
        print("AGENT CONTRACT: FAIL")
        for failure in failures:
            print(f"- {failure}")
        return 1

    print("AGENT CONTRACT: PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
