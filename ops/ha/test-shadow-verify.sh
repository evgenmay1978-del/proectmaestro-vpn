#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERIFY="$ROOT_DIR/ops/ha/shadow-verify.sh"
FIXTURES="$ROOT_DIR/ops/ha/testdata/shadow"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TMP_DIR"' EXIT

fail() {
  printf 'shadow verifier contract: %s\n' "$1" >&2
  exit 1
}

printf '%s' 'synthetic-run-local-salt' >"$TMP_DIR/salt"
chmod 600 "$TMP_DIR/salt"

if ! bash -n "$VERIFY"; then
  fail 'syntax check failed'
fi
if grep -Eiq '(^|[^a-z])(ssh|curl|wget|nc|netcat)([^a-z]|$)' "$VERIFY"; then
  fail 'network-capable command found'
fi

matching_output="$TMP_DIR/matching.json"
if ! "$VERIFY" \
  --legacy "$FIXTURES/legacy.json" \
  --candidate "$FIXTURES/matching.json" \
  --salt-file "$TMP_DIR/salt" >"$matching_output"; then
  fail 'matching exports were rejected'
fi
python3 - "$matching_output" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    report = json.load(handle)
assert report == {"differences": [], "status": "match"}, report
PY

mismatch_output="$TMP_DIR/mismatch.json"
set +e
"$VERIFY" \
  --legacy "$FIXTURES/legacy.json" \
  --candidate "$FIXTURES/mismatch.json" \
  --salt-file "$TMP_DIR/salt" >"$mismatch_output"
status=$?
set -e
if [[ "$status" -ne 2 ]]; then
  fail "mismatch exit was $status, want 2"
fi
python3 - "$mismatch_output" <<'PY'
import json, re, sys
raw_identity = "1" * 64
with open(sys.argv[1], encoding="utf-8") as handle:
    encoded = handle.read()
    report = json.loads(encoded)
assert report["status"] == "mismatch", report
assert report["differences"], report
assert raw_identity not in encoded, encoded
assert any(re.fullmatch(r"[0-9a-f]{64}", item.get("subject", "")) for item in report["differences"]), report
PY

if "$VERIFY" --legacy "$FIXTURES/legacy.json" --candidate "$FIXTURES/matching.json" >/dev/null 2>&1; then
  fail 'missing explicit salt file was accepted'
fi

printf 'shadow verifier contract: PASS\n'
