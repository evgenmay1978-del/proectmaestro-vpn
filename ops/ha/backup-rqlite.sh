#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"

fail() {
  printf 'backup-rqlite: verification failed\n' >&2
  exit 1
}

drill=0
cluster_input=""
keys_input=""
output_input=""
signer=""
recipient=""
manifest_version=""
backup_id=""
attempt_sequence=""
captured_generation=""
lease_fence=""
object_key=""
manifest_version_seen=0
backup_id_seen=0
attempt_sequence_seen=0
captured_generation_seen=0
lease_fence_seen=0
object_key_seen=0
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --drill) drill=1; shift ;;
    --cluster-root) [[ "$#" -ge 2 ]] || fail; cluster_input="$2"; shift 2 ;;
    --keys) [[ "$#" -ge 2 ]] || fail; keys_input="$2"; shift 2 ;;
    --output) [[ "$#" -ge 2 ]] || fail; output_input="$2"; shift 2 ;;
    --signer) [[ "$#" -ge 2 ]] || fail; signer="$2"; shift 2 ;;
    --recipient) [[ "$#" -ge 2 ]] || fail; recipient="$2"; shift 2 ;;
    --manifest-version) [[ "$#" -ge 2 && "$manifest_version_seen" -eq 0 ]] || fail; manifest_version_seen=1; manifest_version="$2"; shift 2 ;;
    --backup-id) [[ "$#" -ge 2 && "$backup_id_seen" -eq 0 ]] || fail; backup_id_seen=1; backup_id="$2"; shift 2 ;;
    --attempt-sequence) [[ "$#" -ge 2 && "$attempt_sequence_seen" -eq 0 ]] || fail; attempt_sequence_seen=1; attempt_sequence="$2"; shift 2 ;;
    --captured-generation) [[ "$#" -ge 2 && "$captured_generation_seen" -eq 0 ]] || fail; captured_generation_seen=1; captured_generation="$2"; shift 2 ;;
    --lease-fence) [[ "$#" -ge 2 && "$lease_fence_seen" -eq 0 ]] || fail; lease_fence_seen=1; lease_fence="$2"; shift 2 ;;
    --object-key) [[ "$#" -ge 2 && "$object_key_seen" -eq 0 ]] || fail; object_key_seen=1; object_key="$2"; shift 2 ;;
    *) fail ;;
  esac
done
[[ "$drill" -eq 1 && -n "$cluster_input" && -n "$keys_input" &&
  -n "$output_input" && "$signer" =~ ^[A-F0-9]{40}$ &&
  "$recipient" =~ ^[A-F0-9]{40}$ ]] || fail

binding_option_count=$((manifest_version_seen + backup_id_seen + attempt_sequence_seen + captured_generation_seen + lease_fence_seen + object_key_seen))
expected_object_tail="g-${captured_generation}/a-${attempt_sequence}-${backup_id}.tar.gpg"
if [[ "$binding_option_count" -eq 0 ]]; then
  manifest_version=1
elif [[ "$binding_option_count" -eq 6 && "$manifest_version" == "2" &&
  "$backup_id" =~ ^[a-f0-9]{32}$ &&
  "$attempt_sequence" =~ ^[1-9][0-9]*$ &&
  "$captured_generation" =~ ^(0|[1-9][0-9]*)$ &&
  "$lease_fence" =~ ^[1-9][0-9]*$ &&
  ${#object_key} -le 1024 &&
  "$object_key" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ &&
  "/$object_key/" != *"//"* &&
  "/$object_key/" != *"/./"* &&
  "/$object_key/" != *"/../"* ]]; then
  [[ "$object_key" == "$expected_object_tail" ||
    "$object_key" == */"$expected_object_tail" ]] || fail
  :
else
  fail
fi
umask 077
runner=""
cluster=""
work=""
description=""

cleanup() {
  local status="$?" resolved
  trap - EXIT
  if [[ -n "$description" && -n "$cluster" && -f "$description" && ! -L "$description" ]]; then
    case "$description" in
      "$cluster"/backup-description.*.json) rm -f -- "$description" ;;
    esac
  fi
  if [[ -n "$work" && -n "$runner" && -d "$work" && ! -L "$work" ]]; then
    resolved="$(realpath -e -- "$work" 2>/dev/null || true)"
    case "$resolved" in
      "$runner"/maestro-rqlite-backup.*) rm -rf -- "$resolved" ;;
    esac
  fi
  exit "$status"
}
trap cleanup EXIT

for command_name in curl gpg tar python3 realpath mktemp stat install ln date; do
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

install -m 0600 -- "$keys" "$work/application-keys.json" || fail
created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
metadata="$work/metadata.json"
(python3 - "$metadata" "$commit_sha" "$run_id" "$created_at" "$signer" "$recipient" \
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

manifest="$work/manifest.json"
(set -o noclobber; python3 -m ops.ha.verify_backup build   --image "$image" --keys "$work/application-keys.json" --metadata "$metadata"   >"$manifest") 2>/dev/null || fail
chmod 0600 "$manifest"

signature="$work/manifest.sig"
gpg_sign=(
  gpg
  --homedir "$gpg_home"
  --batch
  --no-tty
  --pinentry-mode loopback
  --passphrase ''
  --local-user "$signer"
  --output "$signature"
  --detach-sign
  "$manifest"
)
"${gpg_sign[@]}" >/dev/null 2>&1 || fail
chmod 0600 "$signature"

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

encrypted="$work/backup.tar.gpg"
gpg_encrypt=(
  gpg
  --homedir "$gpg_home"
  --batch
  --no-tty
  --trust-model always
  --recipient "$recipient"
  --output "$encrypted"
  --encrypt
  "$archive"
)
"${gpg_encrypt[@]}" >/dev/null 2>&1 || fail
chmod 0600 "$encrypted"

verify="$work/verify"
mkdir -m 0700 -- "$verify"
decrypted="$work/decrypted.tar"
gpg --homedir "$gpg_home" --batch --no-tty --output "$decrypted"   --decrypt "$encrypted" >/dev/null 2>&1 || fail
chmod 0600 "$decrypted"
python3 - "$decrypted" "$verify" <<'PY' || fail
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

result="$work/verify-result.json"
python3 -m ops.ha.verify_backup verify   --directory "$verify" --signer "$signer" --gpg-home "$gpg_home"   >"$result" 2>/dev/null || fail
python3 - "$result" "$metadata" <<'PY' || fail
import json
import pathlib
import sys

result = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
metadata = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
if metadata["format_version"] == 1:
    expected = {
        "binding_status": "legacy-unbound",
        "format_version": 1,
        "rpo_eligible": False,
        "status": "verified",
    }
else:
    expected = {
        "backup_id": metadata["backup_id"],
        "attempt_sequence": metadata["attempt_sequence"],
        "binding_status": "signed-attempt",
        "captured_generation": metadata["captured_generation"],
        "format_version": 2,
        "lease_fence": metadata["lease_fence"],
        "object_key": metadata["object_key"],
        "rpo_eligible": False,
        "status": "verified",
    }
if result != expected:
    raise SystemExit(1)
PY

[[ "$(stat -c '%d' "$work")" == "$(stat -c '%d' "$output_parent")" ]] || fail
ln -- "$encrypted" "$output" || fail
rm -f -- "$encrypted"
[[ -f "$output" && ! -L "$output" && "$(stat -c '%a' "$output")" == "600" ]] || fail
printf 'encrypted backup verified\n'
