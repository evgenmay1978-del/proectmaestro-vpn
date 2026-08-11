#!/usr/bin/env python3
"""Fail-closed guard against repeating an unexplained MaestroVPN operation.

The ledger stores only semantic identifiers, safe reason/evidence codes, and
SHA-256 fingerprints. Never pass a command, credential, token, URL, customer
identifier, or other sensitive value as an argument.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import stat
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


EXIT_BLOCKED = 42
EXIT_CORRUPT = 43
SCHEMA_VERSION = 1
MAX_HISTORY = 100
SAFE_ID = re.compile(r"^[a-z0-9][a-z0-9._-]{0,79}$")


class LedgerError(RuntimeError):
    pass


def safe_id(value: str) -> str:
    if not SAFE_ID.fullmatch(value):
        raise argparse.ArgumentTypeError(
            "use a lowercase semantic id (letters, digits, dot, dash, underscore; max 80)"
        )
    return value


def fingerprint(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def now_utc() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


def empty_state() -> dict[str, Any]:
    return {"schema_version": SCHEMA_VERSION, "actions": {}}


def load_state(path: Path) -> dict[str, Any]:
    if not path.exists():
        return empty_state()
    if path.is_symlink():
        raise LedgerError("ledger must not be a symbolic link")
    try:
        state = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise LedgerError("ledger is unreadable or invalid JSON") from exc
    if (
        not isinstance(state, dict)
        or state.get("schema_version") != SCHEMA_VERSION
        or not isinstance(state.get("actions"), dict)
    ):
        raise LedgerError("ledger schema is invalid")
    return state


def write_state(path: Path, state: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists() and path.is_symlink():
        raise LedgerError("ledger must not be a symbolic link")
    payload = json.dumps(state, ensure_ascii=True, indent=2, sort_keys=True) + "\n"
    descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent), text=True
    )
    temporary_path = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        if os.name != "nt":
            os.chmod(temporary_path, stat.S_IRUSR | stat.S_IWUSR)
        os.replace(temporary_path, path)
    except OSError as exc:
        raise LedgerError("ledger could not be written atomically") from exc
    finally:
        try:
            temporary_path.unlink(missing_ok=True)
        except OSError:
            pass


def append_history(entry: dict[str, Any], event: dict[str, str]) -> None:
    history = entry.setdefault("history", [])
    if not isinstance(history, list):
        raise LedgerError("action history is invalid")
    history.append({"at": now_utc(), **event})
    if len(history) > MAX_HISTORY:
        del history[:-MAX_HISTORY]


def current_entry(state: dict[str, Any], action: str) -> dict[str, Any] | None:
    entry = state["actions"].get(action)
    if entry is not None and not isinstance(entry, dict):
        raise LedgerError("action entry is invalid")
    return entry


def check(state: dict[str, Any], action: str, family: str) -> int:
    entry = current_entry(state, action)
    if entry is None or entry.get("status") == "success":
        print(f"ALLOW action={action}")
        return 0
    if entry.get("status") == "corrected" and entry.get(
        "allowed_family_sha256"
    ) == fingerprint(family):
        print(f"ALLOW_CORRECTED action={action}")
        return 0
    print(f"BLOCK action={action} reason=unresolved-failure")
    return EXIT_BLOCKED


def fail(
    path: Path, state: dict[str, Any], action: str, family: str, reason_code: str
) -> int:
    entry = current_entry(state, action)
    family_hash = fingerprint(family)
    if entry is not None and entry.get("status") == "blocked":
        print(f"BLOCK action={action} reason=existing-unresolved-failure")
        return EXIT_BLOCKED
    if entry is not None and entry.get("status") == "corrected":
        if entry.get("allowed_family_sha256") != family_hash:
            print(f"BLOCK action={action} reason=unapproved-family")
            return EXIT_BLOCKED
        entry["status"] = "blocked"
        entry["blocked_family_sha256"] = family_hash
        entry["reason_code"] = reason_code
        entry["updated_at"] = now_utc()
        entry.pop("allowed_family_sha256", None)
        entry.pop("root_cause_code", None)
        append_history(entry, {"event": "failure", "reason_code": reason_code})
    else:
        entry = {
            "status": "blocked",
            "blocked_family_sha256": family_hash,
            "reason_code": reason_code,
            "updated_at": now_utc(),
            "history": [],
        }
        append_history(entry, {"event": "failure", "reason_code": reason_code})
        state["actions"][action] = entry
    write_state(path, state)
    print(f"RECORDED_FAILURE action={action}")
    return 0


def correct(
    path: Path,
    state: dict[str, Any],
    action: str,
    old_family: str,
    new_family: str,
    root_cause_code: str,
) -> int:
    entry = current_entry(state, action)
    old_hash = fingerprint(old_family)
    new_hash = fingerprint(new_family)
    if (
        entry is None
        or entry.get("status") != "blocked"
        or entry.get("blocked_family_sha256") != old_hash
        or old_hash == new_hash
    ):
        print(f"BLOCK action={action} reason=invalid-correction")
        return EXIT_BLOCKED
    entry["status"] = "corrected"
    entry["allowed_family_sha256"] = new_hash
    entry["root_cause_code"] = root_cause_code
    entry["updated_at"] = now_utc()
    append_history(
        entry,
        {"event": "correction", "root_cause_code": root_cause_code},
    )
    write_state(path, state)
    print(f"RECORDED_CORRECTION action={action}")
    return 0


def success(
    path: Path,
    state: dict[str, Any],
    action: str,
    family: str,
    evidence_code: str,
) -> int:
    entry = current_entry(state, action)
    family_hash = fingerprint(family)
    if entry is not None and entry.get("status") == "blocked":
        print(f"BLOCK action={action} reason=correction-required")
        return EXIT_BLOCKED
    if (
        entry is not None
        and entry.get("status") == "corrected"
        and entry.get("allowed_family_sha256") != family_hash
    ):
        print(f"BLOCK action={action} reason=unapproved-family")
        return EXIT_BLOCKED
    if entry is None:
        entry = {"history": []}
        state["actions"][action] = entry
    entry["status"] = "success"
    entry["successful_family_sha256"] = family_hash
    entry["evidence_code"] = evidence_code
    entry["updated_at"] = now_utc()
    entry.pop("allowed_family_sha256", None)
    append_history(entry, {"event": "success", "evidence_code": evidence_code})
    write_state(path, state)
    print(f"RECORDED_SUCCESS action={action}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--ledger",
        type=Path,
        default=Path.cwd() / ".maestro-state" / "repetition-guard.json",
        help="local untracked ledger path",
    )
    commands = parser.add_subparsers(dest="command", required=True)

    check_parser = commands.add_parser("check")
    check_parser.add_argument("--action", required=True, type=safe_id)
    check_parser.add_argument("--family", required=True, type=safe_id)

    fail_parser = commands.add_parser("fail")
    fail_parser.add_argument("--action", required=True, type=safe_id)
    fail_parser.add_argument("--family", required=True, type=safe_id)
    fail_parser.add_argument("--reason-code", required=True, type=safe_id)

    correct_parser = commands.add_parser("correct")
    correct_parser.add_argument("--action", required=True, type=safe_id)
    correct_parser.add_argument("--old-family", required=True, type=safe_id)
    correct_parser.add_argument("--new-family", required=True, type=safe_id)
    correct_parser.add_argument("--root-cause-code", required=True, type=safe_id)

    success_parser = commands.add_parser("success")
    success_parser.add_argument("--action", required=True, type=safe_id)
    success_parser.add_argument("--family", required=True, type=safe_id)
    success_parser.add_argument("--evidence-code", required=True, type=safe_id)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        state = load_state(args.ledger)
        if args.command == "check":
            return check(state, args.action, args.family)
        if args.command == "fail":
            return fail(args.ledger, state, args.action, args.family, args.reason_code)
        if args.command == "correct":
            return correct(
                args.ledger,
                state,
                args.action,
                args.old_family,
                args.new_family,
                args.root_cause_code,
            )
        if args.command == "success":
            return success(
                args.ledger,
                state,
                args.action,
                args.family,
                args.evidence_code,
            )
    except LedgerError as exc:
        print(f"BLOCK reason=ledger-error detail={exc}", file=sys.stderr)
        return EXIT_CORRUPT
    raise AssertionError("unreachable command")


if __name__ == "__main__":
    raise SystemExit(main())
