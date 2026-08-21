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

bash "$CREATOR" --drill   --cluster-root "$cluster_root" --keys "$keys" --output "$output"   --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" >/dev/null
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
rm -f -- "$observed"
bash "$HARNESS" stop >/dev/null
printf 'backup-rqlite contract passed\n'
