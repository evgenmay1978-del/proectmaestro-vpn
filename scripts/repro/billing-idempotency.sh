#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 0 ]; then
  printf '%s\n' '{"error":"arguments_invalid"}' >&2
  exit 2
fi
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/_run-white-list-suite.sh" "billing_idempotency"
