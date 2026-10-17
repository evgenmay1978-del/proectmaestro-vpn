#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'synthetic-dr-identity: invalid request\n' >&2
  exit 1
}

gpg_home=""
output=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --gpg-home)
      [[ "$#" -ge 2 ]] || fail
      gpg_home="$2"
      shift 2
      ;;
    --output)
      [[ "$#" -ge 2 ]] || fail
      output="$2"
      shift 2
      ;;
    *) fail ;;
  esac
done
[[ -n "$gpg_home" && -n "$output" ]] || fail

umask 077
runner="$(realpath -e -- "${RUNNER_TEMP:-/tmp}")" || fail
[[ -d "$runner" && ! -L "$runner" ]] || fail
[[ ! -e "$gpg_home" && ! -L "$gpg_home" ]] || fail
home_parent="$(realpath -e -- "$(dirname -- "$gpg_home")")" || fail
output_parent="$(realpath -e -- "$(dirname -- "$output")")" || fail
gpg_home="$home_parent/$(basename -- "$gpg_home")"
output="$output_parent/$(basename -- "$output")"
case "$gpg_home" in "$runner"/*) ;; *) fail ;; esac
case "$output" in "$runner"/*) ;; *) fail ;; esac
[[ ! -e "$output" && ! -L "$output" ]] || fail
command -v gpg >/dev/null 2>&1 || fail
command -v python3 >/dev/null 2>&1 || fail

mkdir -m 0700 -- "$gpg_home" || fail
gpg --homedir "$gpg_home" --batch --no-tty --pinentry-mode loopback   --passphrase '' --quick-gen-key   'MaestroVPN DR Signer <dr-signer@example.invalid>' rsa2048 sign 1d   >/dev/null 2>&1 || fail
gpg --homedir "$gpg_home" --batch --no-tty --pinentry-mode loopback   --passphrase '' --quick-gen-key   'MaestroVPN DR Recipient <dr-recipient@example.invalid>' rsa2048 encr 1d   >/dev/null 2>&1 || fail

signer="$(
  gpg --homedir "$gpg_home" --batch --no-tty --with-colons     --list-secret-keys 'dr-signer@example.invalid' 2>/dev/null |
    awk -F: '$1 == "fpr" { print $10; exit }'
)"
recipient="$(
  gpg --homedir "$gpg_home" --batch --no-tty --with-colons     --list-secret-keys 'dr-recipient@example.invalid' 2>/dev/null |
    awk -F: '$1 == "fpr" { print $10; exit }'
)"
[[ "$signer" =~ ^[A-F0-9]{40}$ && "$recipient" =~ ^[A-F0-9]{40}$ ]] || fail

(set -o noclobber; python3 - "$signer" "$recipient" <<'PY' >"$output"
import json
import sys

print(json.dumps(
    {
        "format_version": 1,
        "recipient_fingerprint": sys.argv[2],
        "signer_fingerprint": sys.argv[1],
    },
    sort_keys=True,
    separators=(",", ":"),
))
PY
) 2>/dev/null || fail
chmod 0600 "$output"
printf 'synthetic DR identity ready\n'
