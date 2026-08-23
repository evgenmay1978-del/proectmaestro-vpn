#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
CREATOR="$ROOT/ops/ha/backup-rqlite.sh"
IDENTITY="$ROOT/ops/ha/tests/create-synthetic-dr-identity.sh"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"
HARNESS_TEST="$ROOT/ops/ha/test-ci-rqlite-cluster.sh"

fail() {
  printf 'backup-rqlite contract failed: %s\n' "$*" >&2
  exit 1
}

for required in "$CREATOR" "$IDENTITY" "$HARNESS" "$HARNESS_TEST"; do
  [[ -f "$required" && ! -L "$required" ]] ||
    fail "missing ${required#"$ROOT/"} (expected RED)"
done
bash -n "$CREATOR" "$IDENTITY" "$HARNESS" "$HARNESS_TEST"

if grep -Eq '^[[:space:]]*set[[:space:]]+-[^#]*x|^[[:space:]]*set[[:space:]]+-o[[:space:]]+xtrace' "$CREATOR" "$IDENTITY"; then
  fail "xtrace is forbidden"
fi
if grep -Eq 'curl[^|]*\|[[:space:]]*(sh|bash)|(^|[^[:alnum:]_])(ssh|scp|rsync)([^[:alnum:]_]|$)' "$CREATOR"; then
  fail "remote shell or download-and-execute is forbidden"
fi

for token in   '--drill'   'RUNNER_TEMP'   'realpath'   'umask 077'   'trap '   'mktemp -d'   'fmt=delete'   "--proto '=https'"   "--proto-redir '=https'"   '--max-redirs 0'   '--connect-timeout'   '--max-time'   '--cacert'   '--cert'   '--key'   'SQLite format 3'   '--detach-sign'   '--encrypt'   '--sort=name'   '--mtime=@0'   '--owner=0'   '--group=0'   '--numeric-owner'   'ops.ha.verify_backup'   'noclobber'
do
  grep -qF -- "$token" "$CREATOR" || fail "creator lacks required token: $token"
done
for token in \
  '--manifest-version' \
  '--backup-id' \
  '--attempt-sequence' \
  '--captured-generation' \
  '--lease-fence' \
  '--object-key'
do
  grep -qF -- "$token" "$CREATOR" || fail "creator lacks v2 binding option: $token"
done

if grep -qF -- '--location' "$CREATOR"; then
  fail "backup download must not follow redirects"
fi
grep -qF 'describe-mtls)' "$HARNESS" || fail "describe-mtls command is missing"
grep -qF 'describe-mtls --output FILE' "$HARNESS" || fail "describe-mtls usage is missing"
grep -qF 'chmod 0600' "$HARNESS" || fail "describe-mtls output mode contract is missing"
grep -qF 'set -o noclobber' "$HARNESS" || fail "describe-mtls no-clobber contract is missing"
for endpoint in   'https://127.0.0.1:4401'   'https://127.0.0.1:4403'   'https://127.0.0.1:4405'
do
  grep -qF "$endpoint" "$HARNESS" || fail "describe-mtls endpoint missing: $endpoint"
done
for relative in 'tls/ca.crt' 'tls/client.crt' 'tls/client.key'; do
  grep -qF "$relative" "$HARNESS" || fail "describe-mtls relative TLS path missing: $relative"
done
grep -qF 'describe-mtls' "$HARNESS_TEST" || fail "harness test lacks describe-mtls coverage"

parent="$(realpath -e "${RUNNER_TEMP:-/tmp}")"
sandbox="$(mktemp -d "$parent/maestro-backup-contract.XXXXXX")"
sandbox="$(realpath -e "$sandbox")"
case "$sandbox" in
  "$parent"/maestro-backup-contract.*) ;;
  *) fail "test sandbox escaped runner temp" ;;
esac
cleanup() {
  bash "$HARNESS" stop >/dev/null 2>&1 || true
  if [[ -d "$sandbox" ]]; then
    resolved="$(realpath -e "$sandbox")"
    case "$resolved" in
      "$parent"/maestro-backup-contract.*) rm -rf -- "$resolved" ;;
    esac
  fi
}
trap cleanup EXIT
chmod 0700 "$sandbox"
export RUNNER_TEMP="$sandbox"

keys="$sandbox/application-keys.json"
printf '%s\n' '{"format_version":1,"keys":[]}' >"$keys"
chmod 0600 "$keys"
backup_id="0123456789abcdef0123456789abcdef"
attempt_sequence=42
captured_generation=103
lease_fence=17
object_key="private/cluster-a/g-103/a-42-${backup_id}.tar.gpg"
v2_args=(
  --manifest-version 2
  --backup-id "$backup_id"
  --attempt-sequence "$attempt_sequence"
  --captured-generation "$captured_generation"
  --lease-fence "$lease_fence"
  --object-key "$object_key"
)
output="$sandbox/backup.tar.gpg"

if bash "$CREATOR"   --cluster-root "$sandbox" --keys "$keys" --output "$output"   --signer AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA   --recipient BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB >/dev/null 2>&1
then
  fail "creator accepted invocation without --drill"
fi
[[ ! -e "$output" ]] || fail "failed invocation published an output"

if bash "$CREATOR" --drill   --cluster-root "$sandbox" --keys "$keys" --output "$output"   --signer bad --recipient BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB >/dev/null 2>&1
then
  fail "creator accepted malformed signer fingerprint"
fi
[[ ! -e "$output" ]] || fail "malformed invocation published an output"


bash "$HARNESS" start-mtls
status="$(bash "$HARNESS" status)"
cluster_root="$(awk -F= '$1 == "root" { print $2 }' <<<"$status")"
[[ -n "$cluster_root" ]] || fail "mTLS cluster root is missing"
cluster_root="$(realpath -e "$cluster_root")"
case "$cluster_root" in
  "$sandbox"/maestro-rqlite-ci.*) ;;
  *) fail "positive cluster escaped test sandbox" ;;
esac

(
  cd "$ROOT/backend"
  MAESTRO_IMPORT_SCHEMA_PREP=1     go test -tags=rqlite_integration ./cmd/maestro-import       -run '^TestPrepareProductionImportSchemaMTLS$' -count=1
) >/dev/null

gpg_home="$sandbox/gnupg"
identity="$sandbox/dr-identity.json"
bash "$IDENTITY" --gpg-home "$gpg_home" --output "$identity" >/dev/null
mapfile -t fingerprints < <(python3 - "$identity" <<'PY'
import json
import pathlib
import sys

data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if set(data) != {"format_version", "recipient_fingerprint", "signer_fingerprint"}:
    raise SystemExit(1)
if data["format_version"] != 1:
    raise SystemExit(1)
print(data["signer_fingerprint"])
print(data["recipient_fingerprint"])
PY
)
[[ "${#fingerprints[@]}" -eq 2 ]] || fail "identity output is invalid"
export GNUPGHOME="$gpg_home"
export MAESTRO_DR_COMMIT_SHA="$(git -C "$ROOT" rev-parse HEAD)"
export MAESTRO_DR_RUN_ID=123456

empty_binding_output="$sandbox/empty-binding.tar.gpg"
if bash "$CREATOR" --drill   --cluster-root "$cluster_root" --keys "$keys" --output "$empty_binding_output"   --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" \
  --manifest-version "" >/dev/null 2>&1
then
  fail "an explicitly supplied empty v2 option must not fall back to legacy v1"
fi
duplicate_binding_output="$sandbox/duplicate-binding.tar.gpg"
if bash "$CREATOR" --drill   --cluster-root "$cluster_root" --keys "$keys" --output "$duplicate_binding_output"   --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" \
  "${v2_args[@]}" --manifest-version 2 >/dev/null 2>&1
then
  fail "duplicate v2 binding options must be rejected"
fi
mismatched_key_output="$sandbox/mismatched-key.tar.gpg"
if bash "$CREATOR" --drill   --cluster-root "$cluster_root" --keys "$keys" --output "$mismatched_key_output"   --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" \
  --manifest-version 2 \
  --backup-id "$backup_id" \
  --attempt-sequence "$attempt_sequence" \
  --captured-generation "$captured_generation" \
  --lease-fence "$lease_fence" \
  --object-key "private/cluster-a/g-104/a-42-${backup_id}.tar.gpg" >/dev/null 2>&1
then
  fail "object key must bind the exact generation, sequence, and backup id"
fi

bash "$CREATOR" --drill   --cluster-root "$cluster_root" --keys "$keys" --output "$output"   --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" \
  "${v2_args[@]}" >/dev/null
[[ -f "$output" && ! -L "$output" ]] || fail "encrypted output is missing"
[[ "$(stat -c '%a' "$output")" == "600" ]] || fail "encrypted output is not mode 0600"
before="$(sha256sum "$output" | awk '{print $1}')"
if bash "$CREATOR" --drill   --cluster-root "$cluster_root" --keys "$keys" --output "$output"   --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" >/dev/null 2>&1
then
  fail "creator overwrote an existing output"
fi
after="$(sha256sum "$output" | awk '{print $1}')"
[[ "$before" == "$after" ]] || fail "existing output changed after rejected invocation"

observed="$sandbox/observed.tar"
gpg --homedir "$gpg_home" --batch --no-tty --output "$observed"   --decrypt "$output" >/dev/null 2>&1 || fail "recipient could not decrypt output"
mapfile -t observed_names < <(tar -tf "$observed")
expected_names=(
  application-keys.json
  control-plane.sqlite3
  manifest.json
  manifest.sig
)
[[ "${#observed_names[@]}" -eq "${#expected_names[@]}" ]] ||
  fail "encrypted archive member count is invalid"
for index in "${!expected_names[@]}"; do
  [[ "${observed_names[$index]}" == "${expected_names[$index]}" ]] ||
    fail "encrypted archive member order is invalid"
done
observed_manifest="$sandbox/observed-manifest.json"
tar -xOf "$observed" manifest.json >"$observed_manifest"
python3 - "$observed_manifest" "$backup_id" "$attempt_sequence" \
  "$captured_generation" "$lease_fence" "$object_key" <<'PY' || fail "v2 manifest binding is invalid"
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
expected = {
    "backup_id": sys.argv[2],
    "attempt_sequence": int(sys.argv[3]),
    "captured_generation": int(sys.argv[4]),
    "lease_fence": int(sys.argv[5]),
    "object_key": sys.argv[6],
}
if manifest.get("format_version") != 2:
    raise SystemExit(1)
for key, value in expected.items():
    if manifest.get(key) != value:
        raise SystemExit(1)
PY
rm -f -- "$observed_manifest"
rm -f -- "$observed"
bash "$HARNESS" stop >/dev/null
printf 'backup-rqlite contract passed\n'
