#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
RESTORE="$ROOT/ops/ha/restore-rqlite.sh"
API="$ROOT/ops/ha/restore_api.py"
VERIFIER="$ROOT/ops/ha/verify_backup.py"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"
BACKUP="$ROOT/ops/ha/backup-rqlite.sh"
IDENTITY="$ROOT/ops/ha/tests/create-synthetic-dr-identity.sh"

fail() {
  printf 'restore-rqlite contract failed: %s\n' "$*" >&2
  exit 1
}

for required in "$VERIFIER" "$HARNESS" "$BACKUP" "$IDENTITY"; do
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
cleanup() {
  bash "$HARNESS" stop >/dev/null 2>&1 || true
  if [[ -d "$sandbox" ]]; then
    resolved="$(realpath -e "$sandbox")"
    case "$resolved" in
      "$parent"/maestro-restore-contract.*) rm -rf -- "$resolved" ;;
    esac
  fi
}
trap cleanup EXIT
chmod 0700 "$sandbox"
export RUNNER_TEMP="$sandbox"

if bash "$RESTORE" --cluster-root "$sandbox" --bundle "$sandbox/missing.tar.gpg" \
  --signer AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA --gpg-home "$sandbox/missing" >/dev/null 2>&1
then
  fail "restore accepted invocation without --drill"
fi

keys="$sandbox/application-keys.json"
printf '%s\n' '{"format_version":1,"keys":[]}' >"$keys"
chmod 0600 "$keys"
gpg_home="$sandbox/gnupg"
identity="$sandbox/dr-identity.json"
bash "$IDENTITY" --gpg-home "$gpg_home" --output "$identity" >/dev/null
mapfile -t fingerprints < <(python3 - "$identity" <<'PY'
import json
import pathlib
import sys

data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
print(data["signer_fingerprint"])
print(data["recipient_fingerprint"])
PY
)
[[ "${#fingerprints[@]}" -eq 2 ]] || fail "identity output is invalid"
export GNUPGHOME="$gpg_home"
export MAESTRO_DR_COMMIT_SHA="$(git -C "$ROOT" rev-parse HEAD)"
export MAESTRO_DR_RUN_ID=123456

import_binary="$sandbox/maestro-import"
(
  cd "$ROOT/backend"
  go build -o "$import_binary" ./cmd/maestro-import
)
chmod 0700 "$import_binary"
metadata="$sandbox/dr-metadata.json"

bash "$HARNESS" start-mtls >/dev/null
source_status="$(bash "$HARNESS" status)"
source_root="$(awk -F= '$1 == "root" { print $2 }' <<<"$source_status")"
source_root="$(realpath -e "$source_root")"
(
  cd "$ROOT/backend"
  MAESTRO_DR_PROOF_PHASE=source MAESTRO_DR_METADATA="$metadata" \
    MAESTRO_IMPORT_BINARY="$import_binary" \
    go test -tags=rqlite_integration ./cmd/maestro-import \
      -run '^TestPrepareSyntheticDRSource$' -count=1
)
bundle="$sandbox/source-backup.tar.gpg"
bash "$BACKUP" --drill --cluster-root "$source_root" --keys "$keys" \
  --output "$bundle" --signer "${fingerprints[0]}" \
  --recipient "${fingerprints[1]}" >/dev/null
bash "$HARNESS" stop >/dev/null

bash "$HARNESS" start-mtls >/dev/null
target_status="$(bash "$HARNESS" status)"
target_root="$(awk -F= '$1 == "root" { print $2 }' <<<"$target_status")"
target_root="$(realpath -e "$target_root")"
[[ "$target_root" != "$source_root" ]] || fail "restore target reused source cluster"
bash "$RESTORE" --drill --cluster-root "$target_root" --bundle "$bundle" \
  --signer "${fingerprints[0]}" --gpg-home "$gpg_home" >/dev/null
(
  cd "$ROOT/backend"
  MAESTRO_DR_PROOF_PHASE=restored MAESTRO_DR_METADATA="$metadata" \
    go test -tags=rqlite_integration ./internal/controlplane \
      -run '^TestAdvanceRestoredEpochAndFence$' -count=1
  MAESTRO_DR_PROOF_PHASE=restored MAESTRO_DR_METADATA="$metadata" \
    go test -tags=rqlite_integration ./internal/importer \
      -run '^TestVerifyRestoredBusinessParity$' -count=1
)
[[ -f "$target_root/restore-attempt" && ! -L "$target_root/restore-attempt" ]] ||
  fail "restore attempt marker is missing"
if bash "$RESTORE" --drill --cluster-root "$target_root" --bundle "$bundle" \
  --signer "${fingerprints[0]}" --gpg-home "$gpg_home" >/dev/null 2>&1
then
  fail "restore accepted a non-empty or prior-attempt cluster"
fi
bash "$HARNESS" stop >/dev/null
printf 'restore-rqlite contract passed\n'
