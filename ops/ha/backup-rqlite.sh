#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"

verification_stage="A00"

fail() {
  if [[ "${MAESTRO_BACKUP_DIAGNOSTICS:-}" == "stage-v1" ]]; then
    case "$verification_stage" in
      A00|W10|W20|W30|W40|P10|P20|P30|P40|P50|P60|P70|P80|P90|P99) ;;
      *) verification_stage="X00" ;;
    esac
    printf 'backup-rqlite: verification failed [stage=%s]\n' "$verification_stage" >&2
  else
    printf 'backup-rqlite: verification failed\n' >&2
  fi
  exit 1
}

drill=0
worker=0
cluster_input=""
image_input=""
keys_input=""
output_input=""
verify_script_input=""
signer=""
recipient=""
manifest_version=""
backup_id=""
attempt_sequence=""
captured_generation=""
restore_epoch=""
lease_fence=""
object_key=""
cluster_seen=0
image_seen=0
keys_seen=0
output_seen=0
verify_script_seen=0
signer_seen=0
recipient_seen=0
manifest_version_seen=0
backup_id_seen=0
attempt_sequence_seen=0
captured_generation_seen=0
restore_epoch_seen=0
lease_fence_seen=0
object_key_seen=0
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --drill) drill=$((drill + 1)); shift ;;
    --worker) worker=$((worker + 1)); shift ;;
    --cluster-root) [[ "$#" -ge 2 ]] || fail; cluster_seen=$((cluster_seen + 1)); cluster_input="$2"; shift 2 ;;
    --image) [[ "$#" -ge 2 ]] || fail; image_seen=$((image_seen + 1)); image_input="$2"; shift 2 ;;
    --keys) [[ "$#" -ge 2 ]] || fail; keys_seen=$((keys_seen + 1)); keys_input="$2"; shift 2 ;;
    --output) [[ "$#" -ge 2 ]] || fail; output_seen=$((output_seen + 1)); output_input="$2"; shift 2 ;;
    --verify-script) [[ "$#" -ge 2 ]] || fail; verify_script_seen=$((verify_script_seen + 1)); verify_script_input="$2"; shift 2 ;;
    --signer) [[ "$#" -ge 2 ]] || fail; signer_seen=$((signer_seen + 1)); signer="$2"; shift 2 ;;
    --recipient) [[ "$#" -ge 2 ]] || fail; recipient_seen=$((recipient_seen + 1)); recipient="$2"; shift 2 ;;
    --manifest-version) [[ "$#" -ge 2 ]] || fail; manifest_version_seen=$((manifest_version_seen + 1)); manifest_version="$2"; shift 2 ;;
    --backup-id) [[ "$#" -ge 2 ]] || fail; backup_id_seen=$((backup_id_seen + 1)); backup_id="$2"; shift 2 ;;
    --attempt-sequence) [[ "$#" -ge 2 ]] || fail; attempt_sequence_seen=$((attempt_sequence_seen + 1)); attempt_sequence="$2"; shift 2 ;;
    --captured-generation) [[ "$#" -ge 2 ]] || fail; captured_generation_seen=$((captured_generation_seen + 1)); captured_generation="$2"; shift 2 ;;
    --restore-epoch) [[ "$#" -ge 2 ]] || fail; restore_epoch_seen=$((restore_epoch_seen + 1)); restore_epoch="$2"; shift 2 ;;
    --lease-fence) [[ "$#" -ge 2 ]] || fail; lease_fence_seen=$((lease_fence_seen + 1)); lease_fence="$2"; shift 2 ;;
    --object-key) [[ "$#" -ge 2 ]] || fail; object_key_seen=$((object_key_seen + 1)); object_key="$2"; shift 2 ;;
    *) fail ;;
  esac
done

[[ "$drill" -eq 1 && "$worker" -eq 0 || "$drill" -eq 0 && "$worker" -eq 1 ]] || fail
[[ "$keys_seen" -ge 1 && "$output_seen" -ge 1 && "$signer_seen" -ge 1 && "$recipient_seen" -ge 1 &&
  -n "$keys_input" && -n "$output_input" && "$signer" =~ ^[A-F0-9]{40}$ &&
  "$recipient" =~ ^[A-F0-9]{40}$ ]] || fail

binding_option_count=$((manifest_version_seen + backup_id_seen + attempt_sequence_seen + captured_generation_seen + lease_fence_seen + object_key_seen))
expected_object_tail="g-${captured_generation}/a-${attempt_sequence}-${backup_id}.tar.gpg"
valid_v2_binding=0
if [[ "$binding_option_count" -eq 6 &&
  "$manifest_version_seen" -eq 1 && "$manifest_version" == "2" &&
  "$backup_id_seen" -eq 1 && "$backup_id" =~ ^[a-f0-9]{32}$ &&
  "$attempt_sequence_seen" -eq 1 && "$attempt_sequence" =~ ^[1-9][0-9]*$ &&
  "$captured_generation_seen" -eq 1 && "$captured_generation" =~ ^(0|[1-9][0-9]*)$ &&
  "$lease_fence_seen" -eq 1 && "$lease_fence" =~ ^[1-9][0-9]*$ &&
  "$object_key_seen" -eq 1 && ${#object_key} -le 1024 &&
  "$object_key" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ &&
  "/$object_key/" != *"//"* &&
  "/$object_key/" != *"/./"* &&
  "/$object_key/" != *"/../"* &&
  ( "$object_key" == "$expected_object_tail" || "$object_key" == */"$expected_object_tail" ) ]]; then
  valid_v2_binding=1
fi

if [[ "$drill" -eq 1 ]]; then
  [[ "$cluster_seen" -ge 1 && -n "$cluster_input" && "$image_seen" -eq 0 &&
    "$verify_script_seen" -eq 0 && "$restore_epoch_seen" -eq 0 ]] || fail
  if [[ "$binding_option_count" -eq 0 ]]; then
    manifest_version=1
  else
    [[ "$valid_v2_binding" -eq 1 ]] || fail
  fi
else
  [[ "$cluster_seen" -eq 0 && "$image_seen" -eq 1 && -n "$image_input" &&
    "$keys_seen" -eq 1 && "$output_seen" -eq 1 && "$verify_script_seen" -eq 1 &&
    -n "$verify_script_input" && "$signer_seen" -eq 1 && "$recipient_seen" -eq 1 &&
    "$restore_epoch_seen" -eq 1 && "$restore_epoch" =~ ^[1-9][0-9]*$ &&
    "$captured_generation" =~ ^[1-9][0-9]*$ && "$valid_v2_binding" -eq 1 ]] || fail
fi
umask 077
runner=""
cluster=""
work=""
description=""
verify_command=()
python_command=()
gpg_command=()
verify_gpg_executable=""
image_fd=""

cleanup() {
  local status="$?" resolved
  trap - EXIT
  if [[ -n "$description" && -n "$cluster" && -f "$description" && ! -L "$description" ]]; then
    case "$description" in
      "$cluster"/backup-description.*.json) rm -f -- "$description" ;;
    esac
  fi
  if [[ -n "$work" && -n "$runner" && -d "$work" && ! -L "$work" ]]; then
    if [[ "$worker" -eq 1 ]]; then
      case "$work" in
        "$runner"/maestro-rqlite-backup.*) rm -rf -- "$work" ;;
      esac
    else
      resolved="$(realpath -e -- "$work" 2>/dev/null || true)"
      case "$resolved" in
        "$runner"/maestro-rqlite-backup.*) rm -rf -- "$resolved" ;;
      esac
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

remove_worker_partial() {
  local target="$1"
  case "$target" in "$work"/*) ;; *) fail ;; esac
  if [[ -e "$target" || -L "$target" ]]; then
    [[ -f "$target" && ! -L "$target" ]] || fail
    chmod 0600 -- "$target" 2>/dev/null || true
    rm -f -- "$target"
  fi
}

assert_worker_gpg_home_identity() {
  [[ "$worker" -eq 1 && -n "$gpg_home" && -n "$gpg_home_fd" && -n "$gpg_home_identity" ]] || fail
  local canonical_identity descriptor_identity
  canonical_identity="$(stat -Lc '%d:%i:%u:%g:%a' "$gpg_home")" || fail
  descriptor_identity="$(stat -Lc '%d:%i:%u:%g:%a' "$gpg_home_fd")" || fail
  [[ "$canonical_identity" == "$gpg_home_identity" &&
    "$descriptor_identity" == "$gpg_home_identity" ]] || fail
}

run_worker_gpg_output() {
  local target="$1"
  shift
  [[ "$worker" -eq 1 && -n "$work" ]] || fail
  case "$target" in "$work"/*) ;; *) fail ;; esac
  [[ ! -e "$target" && ! -L "$target" ]] || fail
  assert_worker_gpg_home_identity
  if ! (set -o noclobber; "$@" >"$target") 2>/dev/null; then
    remove_worker_partial "$target"
    fail
  fi
  assert_worker_gpg_home_identity
  [[ -f "$target" && ! -L "$target" &&
    "$(stat -c '%u' "$target")" == "$current_uid" &&
    "$(stat -c '%g' "$target")" == "$current_gid" &&
    "$(stat -c '%h' "$target")" == "1" ]] || fail
  chmod 0600 -- "$target" || fail
}

for command_name in tar realpath mktemp stat install ln date id; do
  command -v "$command_name" >/dev/null 2>&1 || fail
done
current_uid="$(id -u)"
current_gid="$(id -g)"
if [[ "$drill" -eq 1 ]]; then
  command -v curl >/dev/null 2>&1 || fail
  for command_name in gpg tar python3; do
    command -v "$command_name" >/dev/null 2>&1 || fail
  done
  [[ -f "$HARNESS" && ! -L "$HARNESS" ]] || fail

runner="$(realpath -e -- "${RUNNER_TEMP:-}")" || fail
[[ -d "$runner" && ! -L "$runner" ]] || fail
cluster="$(realpath -e -- "$cluster_input")" || fail
[[ "$cluster_input" == "$cluster" && -d "$cluster" && ! -L "$cluster" ]] || fail
case "$cluster" in "$runner"/maestro-rqlite-ci.*) ;; *) fail ;; esac
[[ "$(stat -c '%a' "$cluster")" == "700" ]] || fail
marker="$runner/maestro-rqlite-ci-root"
[[ -f "$marker" && ! -L "$marker" && "$(<"$marker")" == "$cluster" ]] || fail
[[ -f "$cluster/mode" && ! -L "$cluster/mode" && "$(<"$cluster/mode")" == "mtls" ]] || fail

keys="$(realpath -e -- "$keys_input")" || fail
[[ "$keys" == "$keys_input" && -f "$keys" && ! -L "$keys" ]] || fail
case "$keys" in "$runner"/*) ;; *) fail ;; esac
[[ "$(stat -c '%a' "$keys")" == "600" ]] || fail

output_parent="$(realpath -e -- "$(dirname -- "$output_input")")" || fail
output="$output_parent/$(basename -- "$output_input")"
[[ "$output" == "$output_input" && "$output" == *.tar.gpg ]] || fail
case "$output" in "$runner"/*) ;; *) fail ;; esac
[[ ! -e "$output" && ! -L "$output" ]] || fail
[[ "$(stat -c '%a' "$output_parent")" == "700" ]] || fail

gpg_home="$(realpath -e -- "${GNUPGHOME:-}")" || fail
[[ -d "$gpg_home" && ! -L "$gpg_home" && "$(stat -c '%a' "$gpg_home")" == "700" ]] || fail
case "$gpg_home" in "$runner"/*) ;; *) fail ;; esac
commit_sha="${GITHUB_SHA:-${MAESTRO_DR_COMMIT_SHA:-}}"
run_id="${GITHUB_RUN_ID:-${MAESTRO_DR_RUN_ID:-}}"
[[ "$commit_sha" =~ ^[a-f0-9]{40}$ && "$run_id" =~ ^[1-9][0-9]*$ ]] || fail

work="$(mktemp -d "$runner/maestro-rqlite-backup.XXXXXX")" || fail
work="$(realpath -e -- "$work")" || fail
[[ -d "$work" && ! -L "$work" && "$(stat -c '%a' "$work")" == "700" ]] || fail
description="$cluster/backup-description.$$.json"
bash "$HARNESS" describe-mtls --output "$description" >/dev/null

mapfile -t mtls < <(python3 - "$description" "$cluster" <<'PY'
import json
import pathlib
import sys

config_path = pathlib.Path(sys.argv[1])
root = pathlib.Path(sys.argv[2])
data = json.loads(config_path.read_text(encoding="utf-8"))
if set(data) != {"format_version", "nodes", "ca", "client_cert", "client_key"}:
    raise SystemExit(1)
if data["format_version"] != 1:
    raise SystemExit(1)
expected = [
    {"node_id": "S2", "endpoint": "https://127.0.0.1:4401"},
    {"node_id": "S3", "endpoint": "https://127.0.0.1:4403"},
    {"node_id": "S4", "endpoint": "https://127.0.0.1:4405"},
]
if data["nodes"] != expected:
    raise SystemExit(1)
paths = []
for key in ("ca", "client_cert", "client_key"):
    value = data[key]
    if not isinstance(value, str) or value.startswith("/") or ".." in pathlib.PurePosixPath(value).parts:
        raise SystemExit(1)
    path = (root / value).resolve(strict=True)
    if root not in path.parents or not path.is_file() or path.is_symlink():
        raise SystemExit(1)
    paths.append(path)
print(expected[0]["endpoint"])
for path in paths:
    print(path)
PY
) || fail
[[ "${#mtls[@]}" -eq 4 ]] || fail
endpoint="${mtls[0]}"
ca="${mtls[1]}"
client_cert="${mtls[2]}"
client_key="${mtls[3]}"

image="$work/control-plane.sqlite3"
curl_args=(
  --fail
  --silent
  --show-error
  --proto '=https'
  --proto-redir '=https'
  --max-redirs 0
  --connect-timeout 5
  --max-time 60
  --cacert "$ca"
  --cert "$client_cert"
  --key "$client_key"
  --output "$image"
)
curl "${curl_args[@]}" "$endpoint/db/backup?fmt=delete" >/dev/null 2>&1 || fail
chmod 0600 "$image"
python3 - "$image" <<'PY' || fail
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
size = path.stat().st_size
if size <= 0 or size > 536_870_912:
    raise SystemExit(1)
with path.open("rb") as handle:
    if handle.read(16) != b"SQLite format 3\x00":
        raise SystemExit(1)
PY
  python_command=(python3)
  gpg_command=(gpg --homedir "$gpg_home")
  verify_command=(python3 -m ops.ha.verify_backup)
  verify_gpg_executable=gpg
else
verification_stage="W10"
  image_source="$image_input"
  runtime="/proc/self/fd/3"
  [[ "$0" == "/proc/self/fd/4" ]] || fail
  [[ "$image_input" == "$runtime/task-$backup_id/control-plane.sqlite3" ]] || fail
  [[ "$keys_input" == "/proc/self/fd/6" ]] || fail
  [[ "$verify_script_input" == "/proc/self/fd/5" ]] || fail
  [[ "$output_input" == "$runtime/task-$backup_id/backup.bundle" ]] || fail
  [[ -d "$runtime" && -f "$0" && -x "$0" &&
    "$(stat -Lc '%a' "$runtime")" == "700" &&
    "$(stat -Lc '%u' "$runtime")" == "$current_uid" &&
    "$(stat -Lc '%g' "$runtime")" == "$current_gid" ]] || fail

  runtime_resolved="$(realpath -e -- "$runtime")" || fail
  task_input_dir="$runtime/task-$backup_id"
  task_resolved="$(realpath -e -- "$task_input_dir")" || fail
  [[ "$task_resolved" == "$runtime_resolved/task-$backup_id" ]] || fail
  [[ -d "$task_input_dir" && ! -L "$task_input_dir" &&
    "$(stat -Lc '%a' "$task_input_dir")" == "700" &&
    "$(stat -Lc '%u' "$task_input_dir")" == "$current_uid" &&
    "$(stat -Lc '%g' "$task_input_dir")" == "$current_gid" ]] || fail
  task_identity="$(stat -Lc '%d:%i:%u:%g:%a' "$task_input_dir")" || fail
  exec {task_fd}<"$task_input_dir" || fail
  task_dir="/proc/self/fd/$task_fd"
  [[ "$task_identity" == "$(stat -Lc '%d:%i:%u:%g:%a' "$task_dir")" ]] || fail

  image_input_identity="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_source")" || fail
  image_source="$task_dir/control-plane.sqlite3"
  [[ -f "$image_source" && ! -L "$image_source" &&
    "$image_input_identity" == "$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_source")" ]] || fail
  owner_marker="$task_dir/.maestro-backup-owner"
  [[ -f "$owner_marker" && ! -L "$owner_marker" &&
    "$(stat -Lc '%a' "$owner_marker")" == "600" &&
    "$(stat -Lc '%u' "$owner_marker")" == "$current_uid" &&
    "$(stat -Lc '%g' "$owner_marker")" == "$current_gid" &&
    "$(stat -Lc '%h' "$owner_marker")" == "1" &&
    "$(stat -Lc '%s' "$owner_marker")" == "33" &&
    "$(<"$owner_marker")" == "$backup_id" ]] || fail
  shopt -s nullglob dotglob
  task_entries=("$task_dir"/*)
  shopt -u nullglob dotglob
  [[ "${#task_entries[@]}" -eq 2 &&
    "${task_entries[0]}" == "$owner_marker" &&
    "${task_entries[1]}" == "$image_source" ]] || fail
  [[ "$(stat -Lc '%a' "$image_source")" == "600" &&
    "$(stat -Lc '%u' "$image_source")" == "$current_uid" &&
    "$(stat -Lc '%g' "$image_source")" == "$current_gid" &&
    "$(stat -Lc '%h' "$image_source")" == "1" &&
    "$task_identity" == "$(stat -Lc '%d:%i:%u:%g:%a' "$task_input_dir")" &&
    "$task_identity" == "$(stat -Lc '%d:%i:%u:%g:%a' "$task_dir")" &&
    "$image_input_identity" == "$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_input")" ]] || fail

  keys="$keys_input"
  [[ -f "$keys" &&
    "$(stat -Lc '%a' "$keys")" == "600" &&
    "$(stat -Lc '%u' "$keys")" == "$current_uid" &&
    "$(stat -Lc '%g' "$keys")" == "$current_gid" &&
    "$(stat -Lc '%h' "$keys")" == "1" ]] || fail

  verify_script="$verify_script_input"
  [[ -f "$verify_script" &&
    "$(stat -Lc '%u' "$verify_script")" == "$current_uid" &&
    "$(stat -Lc '%g' "$verify_script")" == "$current_gid" &&
    "$(stat -Lc '%h' "$verify_script")" == "1" ]] || fail
  verify_mode="$(stat -Lc '%a' "$verify_script")"
  [[ "$verify_mode" =~ ^[0-7]{3,4}$ && $((8#$verify_mode & 8#22)) -eq 0 ]] || fail

  output_parent="$task_dir"
  output="$task_dir/backup.bundle"
  [[ ! -e "$output" && ! -L "$output" ]] || fail

verification_stage="W20"
  worker_gpg="${MAESTRO_BACKUP_GPG:-}"
  worker_python="${MAESTRO_BACKUP_PYTHON:-}"
  [[ "$worker_gpg" == "/proc/self/fd/8" && "$worker_python" == "/proc/self/fd/9" ]] || fail
  [[ -f "$worker_gpg" && -x "$worker_gpg" &&
    -f "$worker_python" && -x "$worker_python" ]] || fail
  gpg_home_input="${GNUPGHOME:-}"
  gpg_home="$(realpath -e -- "$gpg_home_input")" || fail
  [[ "$gpg_home" == "$gpg_home_input" && -d "$gpg_home" && ! -L "$gpg_home" &&
    "$(stat -Lc '%a' "$gpg_home")" == "700" &&
    "$(stat -Lc '%u' "$gpg_home")" == "$current_uid" &&
    "$(stat -Lc '%g' "$gpg_home")" == "$current_gid" ]] || fail
  gpg_home_fd="${MAESTRO_BACKUP_GPG_HOME_FD:-}"
  [[ "$gpg_home_fd" == "/proc/self/fd/7" && -d "$gpg_home_fd" &&
    "$(stat -Lc '%a' "$gpg_home_fd")" == "700" &&
    "$(stat -Lc '%u' "$gpg_home_fd")" == "$current_uid" &&
    "$(stat -Lc '%g' "$gpg_home_fd")" == "$current_gid" ]] || fail
  gpg_home_identity="$(stat -Lc '%d:%i:%u:%g:%a' "$gpg_home_fd")" || fail
  assert_worker_gpg_home_identity
  python_command=("$worker_python")
  gpg_command=("$worker_gpg" --no-options --no-auto-key-retrieve --homedir "$gpg_home")
  verify_gpg_executable="$worker_gpg"
  commit_sha="${MAESTRO_BACKUP_COMMIT_SHA:-}"
  run_id="${MAESTRO_BACKUP_RUN_ID:-}"
  [[ "$commit_sha" =~ ^[a-f0-9]{40}$ && "$run_id" =~ ^[1-9][0-9]*$ ]] || fail

verification_stage="W30"
  runner="$task_dir"
  work="$(mktemp -d "$runner/maestro-rqlite-backup.XXXXXX")" || fail
  case "$work" in "$runner"/maestro-rqlite-backup.*) ;; *) fail ;; esac
  [[ -d "$work" && ! -L "$work" && "$(stat -c '%a' "$work")" == "700" ]] || fail

verification_stage="W40"
  exec {image_fd}<"$image_source" || fail
  image_fd_path="/proc/self/fd/$image_fd"
  image_identity="$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_source")" || fail
  [[ "$image_identity" == "$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_fd_path")" ]] || fail
  image="$work/control-plane.sqlite3"
  install -m 0600 -- "$image_fd_path" "$image" || fail
  [[ "$image_identity" == "$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_source")" &&
    "$image_identity" == "$(stat -Lc '%d:%i:%u:%g:%a:%h:%s:%Y:%Z' "$image_fd_path")" ]] || fail
  exec {image_fd}<&-
  image_fd=""
  "${python_command[@]}" - "$image" <<'PY' || fail
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
size = path.stat().st_size
if size < 16 or size > 536_870_912:
    raise SystemExit(1)
with path.open("rb") as handle:
    if handle.read(16) != b"SQLite format 3\x00":
        raise SystemExit(1)
PY
  verify_command=("$worker_python" "$verify_script")
fi

verification_stage="P10"
install -m 0600 -- "$keys" "$work/application-keys.json" || fail
created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
metadata="$work/metadata.json"
("${python_command[@]}" - "$metadata" "$commit_sha" "$run_id" "$created_at" "$signer" "$recipient" \
  "$manifest_version" "$backup_id" "$attempt_sequence" "$captured_generation" "$lease_fence" "$object_key" <<'PY'
import json
import os
import sys

if len(sys.argv) != 13:
    raise SystemExit(1)
payload = {
    "format_version": int(sys.argv[7]),
    "repository_commit_sha": sys.argv[2],
    "workflow_run_id": int(sys.argv[3]),
    "rqlite_version": "10.1.0",
    "created_at_utc": sys.argv[4],
    "signing_key_fingerprint": sys.argv[5],
    "recipient_key_fingerprint": sys.argv[6],
    "nodes": ["S2", "S3", "S4"],
}
if payload["format_version"] == 2:
    payload.update(
        {
            "backup_id": sys.argv[8],
            "attempt_sequence": int(sys.argv[9]),
            "captured_generation": int(sys.argv[10]),
            "lease_fence": int(sys.argv[11]),
            "object_key": sys.argv[12],
        }
    )
encoded = json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n"
descriptor = os.open(sys.argv[1], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as output:
    output.write(encoded)
PY
) 2>/dev/null || fail
chmod 0600 "$metadata"

verification_stage="P20"
manifest="$work/manifest.json"
(set -o noclobber; "${verify_command[@]}" build   --image "$image" --keys "$work/application-keys.json" --metadata "$metadata"   >"$manifest") 2>/dev/null || fail
chmod 0600 "$manifest"

verification_stage="P30"
signature="$work/manifest.sig"
gpg_sign=(
  "${gpg_command[@]}"
  --batch
  --no-tty
  --pinentry-mode loopback
  --passphrase ''
  --local-user "$signer"
)
if [[ "$worker" -eq 1 ]]; then
  gpg_sign+=(--output -)
else
  gpg_sign+=(--output "$signature")
fi
gpg_sign+=(--detach-sign "$manifest")
if [[ "$worker" -eq 1 ]]; then
  run_worker_gpg_output "$signature" "${gpg_sign[@]}"
else
  "${gpg_sign[@]}" >/dev/null 2>&1 || fail
fi
chmod 0600 "$signature"

verification_stage="P40"
archive="$work/backup.tar"
tar_args=(
  tar
  --sort=name
  --mtime=@0
  --owner=0
  --group=0
  --numeric-owner
  --format=ustar
  -cf "$archive"
  -C "$work"
  application-keys.json
  control-plane.sqlite3
  manifest.json
  manifest.sig
)
"${tar_args[@]}" >/dev/null 2>&1 || fail
chmod 0600 "$archive"

verification_stage="P50"
encrypted="$work/backup.tar.gpg"
gpg_encrypt=(
  "${gpg_command[@]}"
  --batch
  --no-tty
  --trust-model always
  --recipient "$recipient"
)
if [[ "$worker" -eq 1 ]]; then
  gpg_encrypt+=(--output -)
else
  gpg_encrypt+=(--output "$encrypted")
fi
gpg_encrypt+=(--encrypt "$archive")
if [[ "$worker" -eq 1 ]]; then
  run_worker_gpg_output "$encrypted" "${gpg_encrypt[@]}"
else
  "${gpg_encrypt[@]}" >/dev/null 2>&1 || fail
fi
chmod 0600 "$encrypted"

verification_stage="P60"
verify="$work/verify"
mkdir -m 0700 -- "$verify"
decrypted="$work/decrypted.tar"
gpg_decrypt=(
  "${gpg_command[@]}"
  --batch
  --no-tty
)
if [[ "$worker" -eq 1 ]]; then
  gpg_decrypt+=(--output -)
else
  gpg_decrypt+=(--output "$decrypted")
fi
gpg_decrypt+=(--decrypt "$encrypted")
if [[ "$worker" -eq 1 ]]; then
  run_worker_gpg_output "$decrypted" "${gpg_decrypt[@]}"
else
  "${gpg_decrypt[@]}" >/dev/null 2>&1 || fail
fi
chmod 0600 "$decrypted"
verification_stage="P70"
"${python_command[@]}" - "$decrypted" "$verify" <<'PY' || fail
import os
import pathlib
import shutil
import sys
import tarfile

archive = pathlib.Path(sys.argv[1])
target = pathlib.Path(sys.argv[2])
expected = {
    "application-keys.json",
    "control-plane.sqlite3",
    "manifest.json",
    "manifest.sig",
}
limits = {
    "application-keys.json": 1_048_576,
    "control-plane.sqlite3": 536_870_912,
    "manifest.json": 1_048_576,
    "manifest.sig": 1_048_576,
}
with tarfile.open(archive, "r:") as bundle:
    members = bundle.getmembers()
    names = [member.name for member in members]
    if len(names) != len(expected) or set(names) != expected:
        raise SystemExit(1)
    for member in members:
        if not member.isfile() or pathlib.PurePosixPath(member.name).name != member.name:
            raise SystemExit(1)
        if member.size < 0 or member.size > limits[member.name]:
            raise SystemExit(1)
        source = bundle.extractfile(member)
        if source is None:
            raise SystemExit(1)
        destination = target / member.name
        descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with source, os.fdopen(descriptor, "wb") as output:
            shutil.copyfileobj(source, output, 1024 * 1024)
PY

verification_stage="P80"
result="$work/verify-result.json"
assert_worker_gpg_home_identity
"${verify_command[@]}" verify   --directory "$verify" --signer "$signer" --gpg-home "$gpg_home"   --gpg-executable "$verify_gpg_executable"   >"$result" 2>/dev/null || fail
assert_worker_gpg_home_identity
verification_stage="P90"
"${python_command[@]}" - "$result" "$metadata" "$manifest" "$worker" "$restore_epoch" <<'PY' || fail
import json
import pathlib
import sys


def strict_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate")
        value[key] = item
    return value


result = json.loads(
    pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"),
    object_pairs_hook=strict_object,
)
metadata = json.loads(
    pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"),
    object_pairs_hook=strict_object,
)
manifest = json.loads(
    pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"),
    object_pairs_hook=strict_object,
)
if metadata["format_version"] == 1:
    expected = {
        "binding_status": "legacy-unbound",
        "format_version": 1,
        "rpo_eligible": False,
        "status": "verified",
    }
else:
    source = manifest.get("source")
    if not isinstance(source, dict):
        raise SystemExit(1)
    expected = {
        "backup_id": metadata["backup_id"],
        "attempt_sequence": metadata["attempt_sequence"],
        "binding_status": "signed-attempt",
        "captured_generation": metadata["captured_generation"],
        "dirty_generation": source.get("dirty_generation"),
        "format_version": 2,
        "lease_fence": metadata["lease_fence"],
        "object_key": metadata["object_key"],
        "restore_epoch": source.get("restore_epoch"),
        "rpo_eligible": False,
        "status": "verified",
    }
    if sys.argv[4] == "1" and expected["restore_epoch"] != int(sys.argv[5]):
        raise SystemExit(1)
if result != expected:
    raise SystemExit(1)
PY

verification_stage="P99"
[[ "$(stat -c '%d' "$work")" == "$(stat -c '%d' "$output_parent")" ]] || fail
ln -- "$encrypted" "$output" || fail
rm -f -- "$encrypted"
[[ -f "$output" && ! -L "$output" && "$(stat -c '%a' "$output")" == "600" ]] || fail
printf 'encrypted backup verified\n'
