#!/bin/sh
# Shared strict S1 -> S3 SSH and local-secret boundary for olcRTC operations.
set -eu

OLC_ENV_FILE="${MAESTRO_OLCRTC_ENV_FILE:-/etc/maestro-olcrtc.env}"

olc_secure_file() {
	path="$1"
	label="$2"
	[ -f "$path" ] || { echo "$label is unavailable" >&2; return 1; }
	command -v stat >/dev/null 2>&1 || { echo "cannot verify $label metadata" >&2; return 1; }
	uid=$(stat -c '%u' "$path" 2>/dev/null || stat -f '%u' "$path" 2>/dev/null || echo "")
	mode=$(stat -c '%a' "$path" 2>/dev/null || stat -f '%Lp' "$path" 2>/dev/null || echo "")
	euid=$(id -u)
	case "$uid" in ""|*[!0-9]*) echo "cannot verify $label owner" >&2; return 1;; esac
	case "$mode" in ""|*[!0-7]*) echo "cannot verify $label permissions" >&2; return 1;; esac
	[ "$euid" -eq 0 ] || { echo "$label requires root" >&2; return 1; }
	[ "$uid" -eq 0 ] || { echo "$label owner is unsafe" >&2; return 1; }
	permissions=$((0$mode))
	[ $((permissions & 077)) -eq 0 ] || { echo "$label permissions are unsafe" >&2; return 1; }
}

olc_secure_file "$OLC_ENV_FILE" "olcRTC SSH configuration"
# shellcheck disable=SC1090
. "$OLC_ENV_FILE"

: "${S3_HOST:?S3_HOST is required}"
: "${S3_USER:?S3_USER is required}"
: "${S3_IDENTITY_FILE:?S3_IDENTITY_FILE is required}"
: "${S3_KNOWN_HOSTS_FILE:?S3_KNOWN_HOSTS_FILE is required}"
: "${PANEL_URL:?PANEL_URL is required}"
: "${PANEL_ENV_FILE:?PANEL_ENV_FILE is required}"
: "${WB_TOKEN_FILE:?WB_TOKEN_FILE is required}"
: "${OLC_HEALTH_FILE:?OLC_HEALTH_FILE is required}"

case "$S3_HOST" in *[!A-Za-z0-9._:-]*|"") echo "invalid S3_HOST" >&2; exit 1;; esac
case "$S3_USER" in *[!A-Za-z0-9._-]*|"") echo "invalid S3_USER" >&2; exit 1;; esac

python3 - "$PANEL_URL" <<'PY'
import sys
from urllib.parse import urlsplit
url = urlsplit(sys.argv[1])
if (
    url.scheme != "http"
    or url.hostname not in {"127.0.0.1", "localhost"}
    or url.username is not None
    or url.password is not None
    or url.query
    or url.fragment
    or url.path not in {"", "/"}
    or url.port is None
):
    raise SystemExit("PANEL_URL must be plain loopback HTTP with an explicit port")
PY

olc_secure_file "$S3_IDENTITY_FILE" "S3 identity file"
olc_secure_file "$S3_KNOWN_HOSTS_FILE" "S3 known-hosts file"
olc_secure_file "$PANEL_ENV_FILE" "panel environment file"

SSH_TARGET="$S3_USER@$S3_HOST"

olc_ssh() {
	ssh \
		-i "$S3_IDENTITY_FILE" \
		-o BatchMode=yes \
		-o IdentitiesOnly=yes \
		-o StrictHostKeyChecking=yes \
		-o "UserKnownHostsFile=$S3_KNOWN_HOSTS_FILE" \
		-o ConnectTimeout=8 \
		"$SSH_TARGET" "$@"
}
