#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf '%s\n' "release_validation_failed code=$1" >&2
  exit 1
}

release_dir=''
evidence_trust=''
go_binary=''
seen_release=0
seen_trust=0
seen_go=0
validation_packages=(
  ./internal/api
  ./internal/controlplane
  ./internal/subgen
  ./internal/whitelistbalance
  ./internal/shadowbilling
  ./internal/whitelistapi/v1
  ./internal/release
  ./internal/whitelistready
  ./internal/canary
  ./cmd/maestro-release-validate
  ./cmd/maestro-whitelist-ready
  ./cmd/maestro-xray-cdn-canary
  ./internal/testsupport/whitelistfixture
)
shopt -s nullglob
commercial_python_sources=(../deploy/vpn_bot_maestro_*.py)

while (($# > 0)); do
  case "$1" in
    --release-dir)
      (($# >= 2)) || fail arguments_invalid
      ((seen_release == 0)) || fail arguments_invalid
      release_dir=$2
      seen_release=1
      shift 2
      ;;
    --evidence-trust)
      (($# >= 2)) || fail arguments_invalid
      ((seen_trust == 0)) || fail arguments_invalid
      evidence_trust=$2
      seen_trust=1
      shift 2
      ;;
    --go-binary)
      (($# >= 2)) || fail arguments_invalid
      ((seen_go == 0)) || fail arguments_invalid
      go_binary=$2
      seen_go=1
      shift 2
      ;;
    *)
      fail arguments_invalid
      ;;
  esac
done

[[ $seen_release == 1 && $seen_trust == 1 && -n $release_dir && -n $evidence_trust ]] || fail arguments_invalid

caller_dir=$PWD
[[ $release_dir == /* ]] || release_dir="$caller_dir/$release_dir"
[[ $evidence_trust == /* ]] || evidence_trust="$caller_dir/$evidence_trust"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P) || fail wrapper_failed
repo_root=$(cd -- "$script_dir/.." && pwd -P) || fail wrapper_failed

if [[ -z $go_binary ]]; then
  go_binary=$(command -v go) || fail go_not_found
elif [[ $go_binary == */* ]]; then
  [[ $go_binary == /* ]] || go_binary="$caller_dir/$go_binary"
  [[ -x $go_binary ]] || fail go_not_found
else
  [[ $go_binary != -* ]] || fail arguments_invalid
  go_binary=$(command -v "$go_binary") || fail go_not_found
fi

cd -- "$repo_root/backend" || fail wrapper_failed
existing_packages=()
for package in "${validation_packages[@]}"; do
  [[ -d ${package#./} ]] && existing_packages+=("$package")
done
"$go_binary" test -count=1 "${existing_packages[@]}" || fail go_tests_failed
if ((${#commercial_python_sources[@]})); then
  python3 -X utf8 -m py_compile "${commercial_python_sources[@]}" || fail python_compile_failed
fi
exec "$go_binary" run ./cmd/maestro-release-validate \
  --release-dir "$release_dir" \
  --evidence-trust "$evidence_trust"
