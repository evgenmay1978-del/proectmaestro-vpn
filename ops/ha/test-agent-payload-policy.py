#!/usr/bin/env python3
"""Executable assertion for the apply-agent payload boundary policy."""

from __future__ import annotations

import sys

from agent_payload_policy import main


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as error:
        print(f"Agent payload boundary policy failed: {error}", file=sys.stderr)
        raise SystemExit(1)
