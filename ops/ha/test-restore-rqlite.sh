#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
RESTORE="$ROOT/ops/ha/restore-rqlite.sh"
API="$ROOT/ops/ha/restore_api.py"
VERIFIER="$ROOT/ops/ha/verify_backup.py"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"

fail() {
  printf 'restore-rqlite contract failed: %s\n' "$*" >&2
  exit 1
}

for required in "$VERIFIER" "$HARNESS"; do
  [[ -f "$required" && ! -L "$required" ]] ||
    fail "missing ${required#"$ROOT/"}"
done
[[ -f "$API" && ! -L "$API" ]] ||
  fail "missing ops/ha/restore_api.py (expected RED)"
[[ -f "$RESTORE" && ! -L "$RESTORE" ]] ||
  fail "missing ops/ha/restore-rqlite.sh (expected RED)"

bash -n "$RESTORE"
if grep -Eq '^[[:space:]]*set[[:space:]]+-[^#]*x|^[[:space:]]*set[[:space:]]+-o[[:space:]]+xtrace' "$RESTORE"; then
  fail "xtrace is forbidden"
fi
if grep -Eq 'curl[^|]*\|[[:space:]]*(sh|bash)|(^|[^[:alnum:]_])(ssh|scp|rsync)([^[:alnum:]_]|$)' "$RESTORE"; then
  fail "remote shell or download-and-execute is forbidden"
fi

for token in \
  '--drill' \
  'RUNNER_TEMP' \
  'realpath' \
  'umask 077' \
  'trap ' \
  'mktemp -d' \
  'ops.ha.verify_backup' \
  'inspect_empty' \
  'load_sqlite' \
  'restore-attempt' \
  'noclobber' \
  'SQLite format 3' \
  'chmod 0600'
do
  grep -qF -- "$token" "$RESTORE" ||
    fail "restore orchestrator lacks required token: $token"
done

verify_line="$(grep -nF 'ops.ha.verify_backup verify' "$RESTORE" | head -n1 | cut -d: -f1)"
inspect_line="$(grep -nF 'inspect_empty' "$RESTORE" | head -n1 | cut -d: -f1)"
marker_line="$(grep -nF 'restore-attempt' "$RESTORE" | head -n1 | cut -d: -f1)"
load_line="$(grep -nF 'load_sqlite' "$RESTORE" | head -n1 | cut -d: -f1)"
[[ -n "$verify_line" && -n "$inspect_line" && -n "$marker_line" && -n "$load_line" ]] ||
  fail "restore order markers are incomplete"
(( verify_line < inspect_line && inspect_line < marker_line && marker_line < load_line )) ||
  fail "restore order is not verify -> inspect -> marker -> load"
[[ "$(grep -cF 'load_sqlite' "$RESTORE")" -eq 1 ]] ||
  fail "restore orchestrator may call load_sqlite more than once"

parent="$(realpath -e "${RUNNER_TEMP:-/tmp}")"
sandbox="$(mktemp -d "$parent/maestro-restore-contract.XXXXXX")"
sandbox="$(realpath -e "$sandbox")"
case "$sandbox" in
  "$parent"/maestro-restore-contract.*) ;;
  *) fail "test sandbox escaped runner temp" ;;
esac
trap 'case "$sandbox" in "$parent"/maestro-restore-contract.*) rm -rf -- "$sandbox";; esac' EXIT
chmod 0700 "$sandbox"
export RUNNER_TEMP="$sandbox"

if bash "$RESTORE" --cluster-root "$sandbox" --bundle "$sandbox/missing.tar.gpg" \
  --signer AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA --gpg-home "$sandbox/missing" >/dev/null 2>&1
then
  fail "restore accepted invocation without --drill"
fi
printf 'restore-rqlite contract passed\n'
