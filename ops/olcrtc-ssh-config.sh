#!/bin/sh
# Shared strict S1 -> S3 SSH boundary for olcRTC operations.
# Production configuration is root-owned /etc/maestro-olcrtc.env.
set -eu

OLC_ENV_FILE="${MAESTRO_OLCRTC_ENV_FILE:-/etc/maestro-olcrtc.env}"
[ -r "$OLC_ENV_FILE" ] || { echo "olcRTC SSH configuration is unavailable" >&2; exit 1; }

# The file is administrator-controlled shell syntax. Refuse group/world-writable input.
if command -v stat >/dev/null 2>&1; then
	mode=$(stat -c '%a' "$OLC_ENV_FILE" 2>/dev/null || stat -f '%Lp' "$OLC_ENV_FILE" 2>/dev/null || echo "")
	case "$mode" in
		""|*[!0-7]*) echo "cannot verify olcRTC SSH configuration permissions" >&2; exit 1 ;;
	esac
	permissions=$((0$mode))
	if [ $((permissions & 022)) -ne 0 ]; then
		echo "olcRTC SSH configuration permissions are unsafe" >&2
		exit 1
	fi
fi

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
case "$PANEL_URL" in http://127.0.0.1:*|http://localhost:*) ;; *) echo "PANEL_URL must be loopback HTTP" >&2; exit 1;; esac

[ -r "$S3_IDENTITY_FILE" ] || { echo "S3 identity file is unavailable" >&2; exit 1; }
[ -r "$S3_KNOWN_HOSTS_FILE" ] || { echo "S3 known-hosts file is unavailable" >&2; exit 1; }
[ -r "$PANEL_ENV_FILE" ] || { echo "panel environment file is unavailable" >&2; exit 1; }

SSH_TARGET="$S3_USER@$S3_HOST"

olc_ssh() {
	ssh 		-i "$S3_IDENTITY_FILE" 		-o BatchMode=yes 		-o IdentitiesOnly=yes 		-o StrictHostKeyChecking=yes 		-o "UserKnownHostsFile=$S3_KNOWN_HOSTS_FILE" 		-o ConnectTimeout=8 		"$SSH_TARGET" "$@"
}

olc_panel_token() {
	sed -n 's/^MAESTRO_ADMIN_TOKEN=//p' "$PANEL_ENV_FILE" |
		head -n 1 |
		sed 's/^"//;s/"$//'
}
