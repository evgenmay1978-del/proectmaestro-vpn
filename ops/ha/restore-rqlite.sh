#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HARNESS="$ROOT/ops/ha/ci-rqlite-cluster.sh"

fail() {
  printf 'restore-rqlite: verification failed\n' >&2
  exit 1
}

drill=0
cluster_input=""
bundle_input=""
signer=""
gpg_input=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --drill) drill=1; shift ;;
    --cluster-root) [[ "$#" -ge 2 ]] || fail; cluster_input="$2"; shift 2 ;;
    --bundle) [[ "$#" -ge 2 ]] || fail; bundle_input="$2"; shift 2 ;;
    --signer) [[ "$#" -ge 2 ]] || fail; signer="$2"; shift 2 ;;
    --gpg-home) [[ "$#" -ge 2 ]] || fail; gpg_input="$2"; shift 2 ;;
    *) fail ;;
  esac
done
[[ "$drill" -eq 1 && -n "$cluster_input" && -n "$bundle_input" &&
  -n "$gpg_input" && "$signer" =~ ^[A-F0-9]{40}$ ]] || fail

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
      "$cluster"/restore-description.*.json) rm -f -- "$description" ;;
    esac
  fi
  if [[ -n "$work" && -n "$runner" && -d "$work" && ! -L "$work" ]]; then
    resolved="$(realpath -e -- "$work" 2>/dev/null || true)"
    case "$resolved" in
      "$runner"/maestro-rqlite-restore.*) rm -rf -- "$resolved" ;;
    esac
  fi
  exit "$status"
}
trap cleanup EXIT

for command_name in gpg tar python3 realpath mktemp stat; do
  command -v "$command_name" >/dev/null 2>&1 || fail
done
[[ -f "$HARNESS" && ! -L "$HARNESS" ]] || fail
runner="$(realpath -e -- "${RUNNER_TEMP:-}")" || fail
[[ -d "$runner" && ! -L "$runner" ]] || fail
cluster="$(realpath -e -- "$cluster_input")" || fail
[[ "$cluster" == "$cluster_input" && -d "$cluster" && ! -L "$cluster" ]] || fail
case "$cluster" in "$runner"/maestro-rqlite-ci.*) ;; *) fail ;; esac
[[ "$(stat -c '%a' "$cluster")" == "700" ]] || fail
marker="$runner/maestro-rqlite-ci-root"
[[ -f "$marker" && ! -L "$marker" && "$(<"$marker")" == "$cluster" ]] || fail
[[ -f "$cluster/mode" && ! -L "$cluster/mode" && "$(<"$cluster/mode")" == "mtls" ]] || fail

bundle="$(realpath -e -- "$bundle_input")" || fail
[[ "$bundle" == "$bundle_input" && -f "$bundle" && ! -L "$bundle" ]] || fail
case "$bundle" in "$runner"/*.tar.gpg|"$runner"/*/*.tar.gpg) ;; *) fail ;; esac
[[ "$(stat -c '%a' "$bundle")" == "600" ]] || fail
bundle_size="$(stat -c '%s' "$bundle")"
[[ "$bundle_size" -gt 0 && "$bundle_size" -le 600000000 ]] || fail

gpg_home="$(realpath -e -- "$gpg_input")" || fail
[[ "$gpg_home" == "$gpg_input" && -d "$gpg_home" && ! -L "$gpg_home" ]] || fail
case "$gpg_home" in "$runner"/*) ;; *) fail ;; esac
[[ "$(stat -c '%a' "$gpg_home")" == "700" ]] || fail

work="$(mktemp -d "$runner/maestro-rqlite-restore.XXXXXX")" || fail
work="$(realpath -e -- "$work")" || fail
[[ -d "$work" && ! -L "$work" && "$(stat -c '%a' "$work")" == "700" ]] || fail
archive="$work/decrypted.tar"
gpg --homedir "$gpg_home" --batch --no-tty --output "$archive" \
  --decrypt "$bundle" >/dev/null 2>&1 || fail
chmod 0600 "$archive"
extract="$work/bundle"
mkdir -m 0700 -- "$extract"
python3 - "$archive" "$extract" <<'PY' || fail
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

verify_result="$work/verify-result.json"
python3 -m ops.ha.verify_backup verify \
  --directory "$extract" --signer "$signer" --gpg-home "$gpg_home" \
  >"$verify_result" 2>/dev/null || fail
[[ "$(<"$verify_result")" == '{"format_version":1,"status":"verified"}' ]] || fail

image="$extract/control-plane.sqlite3"
[[ -f "$image" && ! -L "$image" && "$(stat -c '%a' "$image")" == "600" ]] || fail
python3 - "$image" <<'PY' || fail
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
if path.stat().st_size <= 16 or path.stat().st_size > 536_870_912:
    raise SystemExit(1)
with path.open("rb") as handle:
    if handle.read(16) != b"SQLite format 3\x00":
        raise SystemExit(1)
PY

description="$cluster/restore-description.$$.json"
bash "$HARNESS" describe-mtls --output "$description" >/dev/null
config="$work/restore-config.json"
python3 - "$description" "$cluster" "$config" <<'PY' || fail
import json
import os
import pathlib
import sys

source = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
root = pathlib.Path(sys.argv[2])
target = pathlib.Path(sys.argv[3])
if set(source) != {"format_version", "nodes", "ca", "client_cert", "client_key"}:
    raise SystemExit(1)
payload = {"format_version": source["format_version"], "nodes": source["nodes"]}
for key in ("ca", "client_cert", "client_key"):
    value = source[key]
    if not isinstance(value, str) or value.startswith("/") or ".." in pathlib.PurePosixPath(value).parts:
        raise SystemExit(1)
    path = (root / value).resolve(strict=True)
    if root not in path.parents or not path.is_file() or path.is_symlink():
        raise SystemExit(1)
    payload[key] = str(path)
encoded = json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n"
descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as output:
    output.write(encoded)
PY
chmod 0600 "$config"

empty="$(
  python3 - "$config" <<'PY'
import json
import pathlib
import sys
from ops.ha.restore_api import inspect_empty

config = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
print("empty" if inspect_empty(config) else "nonempty")
PY
)" || fail
[[ "$empty" == "empty" ]] || fail

attempt="$cluster/restore-attempt"
(set -o noclobber; printf '%s\n' "started" >"$attempt") 2>/dev/null || fail
chmod 0600 "$attempt"

python3 - "$config" "$image" "$extract/manifest.json" <<'PY' || fail
import json
import pathlib
import sys
from ops.ha.restore_api import inspect_restored, load_sqlite; load_sqlite(json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")), pathlib.Path(sys.argv[2]))

config = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
manifest = json.loads(pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"))
inspect_restored(config, manifest)
PY

printf 'fresh mTLS restore verified\n'
