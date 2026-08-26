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

for token in   '--drill'   '--worker'   '--image'   '--verify-script'   '--restore-epoch'   'RUNNER_TEMP'   'realpath'   'umask 077'   'trap '   'mktemp -d'   'fmt=delete'   "--proto '=https'"   "--proto-redir '=https'"   '--max-redirs 0'   '--connect-timeout'   '--max-time'   '--cacert'   '--cert'   '--key'   'SQLite format 3'   '--detach-sign'   '--encrypt'   '--sort=name'   '--mtime=@0'   '--owner=0'   '--group=0'   '--numeric-owner'   'ops.ha.verify_backup'   'noclobber'
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
captured_generation=1
lease_fence=17
object_key="private/cluster-a/g-${captured_generation}/a-42-${backup_id}.tar.gpg"
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

worker_generation=103
worker_epoch=7
worker_task="$sandbox/task-$backup_id"
mkdir -m 0700 -- "$worker_task"
printf '%s\n' "$backup_id" >"$worker_task/.maestro-backup-owner"
chmod 0600 "$worker_task/.maestro-backup-owner"
worker_image="$worker_task/control-plane.sqlite3"
python3 - "$worker_image" "$worker_generation" "$worker_epoch" <<'PY'
import os
import pathlib
import sqlite3
import sys

path = pathlib.Path(sys.argv[1])
generation = int(sys.argv[2])
epoch = int(sys.argv[3])
db = sqlite3.connect(path)
db.executescript(
    """
    PRAGMA foreign_keys=ON;
    CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, checksum TEXT NOT NULL);
    CREATE TABLE cluster_restore_state(
      singleton_id INTEGER PRIMARY KEY, cluster_id TEXT NOT NULL,
      restore_epoch INTEGER NOT NULL, restored_from_backup_sha256 TEXT,
      activated INTEGER NOT NULL, created_at_unix INTEGER NOT NULL,
      activated_at_unix INTEGER
    );
    CREATE TABLE backup_rpo_state(
      singleton_id INTEGER PRIMARY KEY, restore_epoch INTEGER NOT NULL,
      dirty_generation INTEGER NOT NULL
    );
    CREATE TABLE import_runs(import_run_id TEXT PRIMARY KEY, completed_at_unix INTEGER);
    CREATE TABLE import_batches(import_run_id TEXT, batch_index INTEGER, applied_at_unix INTEGER);
    CREATE TABLE backup_watermarks(
      backup_id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL,
      backup_sha256 TEXT NOT NULL, destination TEXT NOT NULL,
      status TEXT NOT NULL, created_at_unix INTEGER NOT NULL,
      verified_at_unix INTEGER
    );
    """
)
db.execute("INSERT INTO schema_migrations VALUES(1, ?)", ("a" * 64,))
db.execute(
    "INSERT INTO cluster_restore_state VALUES(1, ?, ?, NULL, 1, 100, 100)",
    ("c" * 64, epoch),
)
db.execute("INSERT INTO backup_rpo_state VALUES(1, ?, ?)", (epoch, generation))
db.execute("INSERT INTO import_runs VALUES('worker-run', 200)")
db.execute("INSERT INTO import_batches VALUES('worker-run', 3, 201)")
db.execute(
    "INSERT INTO backup_watermarks VALUES('worker-backup', 1, ?, 'worker', 'verified', 202, 204)",
    ("d" * 64,),
)
db.commit()
db.close()
os.chmod(path, 0o600)
PY

worker_creator="$sandbox/backup-rqlite-worker"
install -m 0700 -- "$CREATOR" "$worker_creator"
[[ -f "$worker_creator" && ! -L "$worker_creator" &&
  "$(stat -c '%a' "$worker_creator")" == "700" &&
  "$(stat -c '%u' "$worker_creator")" == "$(id -u)" &&
  "$(stat -c '%g' "$worker_creator")" == "$(id -g)" &&
  "$(stat -c '%h' "$worker_creator")" == "1" ]] ||
  fail "worker creator install fixture is unsafe"
worker_commit="$(git -C "$ROOT" rev-parse HEAD)"
run_worker() {
  local task="$1" id="$2" epoch="$3"
  local gpg_binary python_binary
  local key="private/cluster-a/g-${worker_generation}/a-${attempt_sequence}-${id}.tar.gpg"
  [[ "$task" == "$sandbox/task-$id" ]] || fail "worker task is outside pinned runtime"
  gpg_binary="$(realpath -e -- "$(command -v gpg)")" || fail "gpg executable is unavailable"
  python_binary="$(realpath -e -- "$(command -v python3)")" || fail "python3 executable is unavailable"
  (
    exec 3<"$sandbox"
    exec 4<"$worker_creator"
    exec 5<"$ROOT/ops/ha/verify_backup.py"
    exec 6<"$keys"
    exec 7<"$gpg_home"
    exec 8<"$gpg_binary"
    exec 9<"$python_binary"
    env -u RUNNER_TEMP \
      MAESTRO_BACKUP_DIAGNOSTICS=stage-v1 \
      GNUPGHOME="$gpg_home" \
      MAESTRO_BACKUP_GPG_HOME_FD=/proc/self/fd/7 \
      MAESTRO_BACKUP_GPG=/proc/self/fd/8 \
      MAESTRO_BACKUP_PYTHON=/proc/self/fd/9 \
      MAESTRO_BACKUP_COMMIT_SHA="$worker_commit" \
      MAESTRO_BACKUP_RUN_ID=654321 \
      "/proc/self/fd/4" --worker \
        --image "/proc/self/fd/3/task-$id/control-plane.sqlite3" \
        --keys /proc/self/fd/6 --output "/proc/self/fd/3/task-$id/backup.bundle" \
        --signer "${fingerprints[0]}" --recipient "${fingerprints[1]}" \
        --manifest-version 2 --backup-id "$id" \
        --attempt-sequence "$attempt_sequence" \
        --captured-generation "$worker_generation" \
        --restore-epoch "$epoch" --lease-fence "$lease_fence" \
        --object-key "$key" --verify-script /proc/self/fd/5
  )
}

run_worker "$worker_task" "$backup_id" "$worker_epoch" >/dev/null
worker_output="$worker_task/backup.bundle"
[[ -f "$worker_output" && ! -L "$worker_output" &&
  "$(stat -c '%a' "$worker_output")" == "600" &&
  "$(stat -c '%h' "$worker_output")" == "1" ]] ||
  fail "worker candidate is not a pinned mode-0600 regular file"
mapfile -t worker_names < <(find "$worker_task" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)
expected_worker_names=(.maestro-backup-owner backup.bundle control-plane.sqlite3)
[[ "${worker_names[*]}" == "${expected_worker_names[*]}" ]] ||
  fail "worker left unexpected task members"
worker_before="$(sha256sum "$worker_output" | awk '{print $1}')"
if run_worker "$worker_task" "$backup_id" "$worker_epoch" >/dev/null 2>&1; then
  fail "worker overwrote an existing candidate"
fi
worker_after="$(sha256sum "$worker_output" | awk '{print $1}')"
[[ "$worker_before" == "$worker_after" ]] || fail "worker changed existing candidate"

worker_observed="$sandbox/worker-observed.tar"
gpg --homedir "$gpg_home" --batch --no-tty --output "$worker_observed" \
  --decrypt "$worker_output" >/dev/null 2>&1 || fail "worker candidate is not decryptable"
worker_manifest="$sandbox/worker-manifest.json"
tar -xOf "$worker_observed" manifest.json >"$worker_manifest"
python3 - "$worker_manifest" "$backup_id" "$attempt_sequence" "$worker_generation" \
  "$worker_epoch" "$lease_fence" <<'PY' || fail "worker manifest binding is invalid"
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
expected = {
    "backup_id": sys.argv[2],
    "attempt_sequence": int(sys.argv[3]),
    "captured_generation": int(sys.argv[4]),
    "lease_fence": int(sys.argv[6]),
}
if manifest.get("format_version") != 2:
    raise SystemExit(1)
for key, value in expected.items():
    if manifest.get(key) != value:
        raise SystemExit(1)
source = manifest.get("source")
if source.get("dirty_generation") != int(sys.argv[4]) or source.get("restore_epoch") != int(sys.argv[5]):
    raise SystemExit(1)
PY
rm -f -- "$worker_manifest" "$worker_observed"

wrong_epoch_id="1123456789abcdef0123456789abcdef"
wrong_epoch_task="$sandbox/task-$wrong_epoch_id"
mkdir -m 0700 -- "$wrong_epoch_task"
printf '%s\n' "$wrong_epoch_id" >"$wrong_epoch_task/.maestro-backup-owner"
chmod 0600 "$wrong_epoch_task/.maestro-backup-owner"
cp -- "$worker_image" "$wrong_epoch_task/control-plane.sqlite3"
chmod 0600 "$wrong_epoch_task/control-plane.sqlite3"
if run_worker "$wrong_epoch_task" "$wrong_epoch_id" 8 >/dev/null 2>&1; then
  fail "worker accepted a restore epoch that differs from the signed image"
fi
[[ ! -e "$wrong_epoch_task/backup.bundle" ]] || fail "wrong epoch published a candidate"

hardlink_id="2123456789abcdef0123456789abcdef"
hardlink_task="$sandbox/task-$hardlink_id"
mkdir -m 0700 -- "$hardlink_task"
printf '%s\n' "$hardlink_id" >"$hardlink_task/.maestro-backup-owner"
chmod 0600 "$hardlink_task/.maestro-backup-owner"
ln -- "$worker_image" "$hardlink_task/control-plane.sqlite3"
if run_worker "$hardlink_task" "$hardlink_id" "$worker_epoch" >/dev/null 2>&1; then
  fail "worker accepted a hard-linked image"
fi
[[ ! -e "$hardlink_task/backup.bundle" ]] || fail "hard-linked image published a candidate"

symlink_id="3123456789abcdef0123456789abcdef"
symlink_task="$sandbox/task-$symlink_id"
mkdir -m 0700 -- "$symlink_task"
printf '%s\n' "$symlink_id" >"$symlink_task/.maestro-backup-owner"
chmod 0600 "$symlink_task/.maestro-backup-owner"
ln -s -- "$worker_image" "$symlink_task/control-plane.sqlite3"
if run_worker "$symlink_task" "$symlink_id" "$worker_epoch" >/dev/null 2>&1; then
  fail "worker accepted a symlink image"
fi
[[ ! -e "$symlink_task/backup.bundle" ]] || fail "symlink image published a candidate"

if env -u RUNNER_TEMP GNUPGHOME="$gpg_home" \
  MAESTRO_BACKUP_COMMIT_SHA="$worker_commit" MAESTRO_BACKUP_RUN_ID=654321 \
  bash "$CREATOR" --worker --drill >/dev/null 2>&1; then
  fail "worker accepted both mutually exclusive modes"
fi
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
  --object-key "private/cluster-a/g-$((captured_generation + 1))/a-42-${backup_id}.tar.gpg" >/dev/null 2>&1
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
