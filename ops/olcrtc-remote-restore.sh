#!/bin/sh
# Restore one staged olcRTC unit without discarding recovery evidence on partial failure.
set -u
umask 077

if [ "$#" -ne 4 ]; then
	echo "invalid olcRTC restore request" >&2
	exit 1
fi

lock="$1"
dest="$2"
unit="$3"
txn="$4"

case "$lock" in /*) ;; *) echo "invalid olcRTC restore lock" >&2; exit 1;; esac
case "$dest" in /*) ;; *) echo "invalid olcRTC restore destination" >&2; exit 1;; esac
[ "$lock" != "$dest" ] || { echo "invalid olcRTC restore paths" >&2; exit 1; }
case "$unit" in
	olcrtc-srv) ;;
	olcrtc-srv@*)
		instance="${unit#olcrtc-srv@}"
		case "$instance" in *[!A-Za-z0-9._-]*|"") echo "invalid olcRTC restore unit" >&2; exit 1;; esac
		;;
	*) echo "invalid olcRTC restore unit" >&2; exit 1 ;;
esac
case "$txn" in *[!0-9a-f]*|"") echo "invalid olcRTC restore transaction" >&2; exit 1;; esac
[ "${#txn}" -eq 16 ] || { echo "invalid olcRTC restore transaction" >&2; exit 1; }

[ -d "$lock" ] || exit 0
[ -f "$lock/owner" ] || { echo "olcRTC recovery owner is unavailable" >&2; exit 1; }
owner=$(cat "$lock/owner" 2>/dev/null) || { echo "olcRTC recovery owner is unreadable" >&2; exit 1; }
[ "$owner" = "$txn" ] || { echo "olcRTC recovery owner mismatch" >&2; exit 1; }

restore_ok=1
if [ -f "$lock/previous-exists" ] && [ -f "$lock/previous.yaml" ]; then
	tmp="$lock/restore.yaml.tmp"
	if cp -p "$lock/previous.yaml" "$tmp" &&
		sync "$tmp" &&
		mv -f "$tmp" "$dest" &&
		cmp -s "$lock/previous.yaml" "$dest"; then
		:
	else
		restore_ok=0
	fi
elif [ -f "$lock/new-dest" ]; then
	if rm -f "$dest" && [ ! -e "$dest" ]; then
		:
	else
		restore_ok=0
	fi
else
	restore_ok=0
fi

enabled=unknown
if [ -f "$lock/previous-enabled" ]; then
	enabled=$(cat "$lock/previous-enabled" 2>/dev/null) || enabled=unknown
fi
case "$enabled" in
	enabled|enabled-runtime|linked|linked-runtime|alias)
		if systemctl enable "$unit" >/dev/null 2>&1; then
			actual=$(systemctl is-enabled "$unit" 2>/dev/null || true)
			case "$actual" in enabled|enabled-runtime|linked|linked-runtime|alias) ;; *) restore_ok=0;; esac
		else
			restore_ok=0
		fi
		;;
	masked|masked-runtime)
		if systemctl mask "$unit" >/dev/null 2>&1; then
			actual=$(systemctl is-enabled "$unit" 2>/dev/null || true)
			case "$actual" in masked|masked-runtime) ;; *) restore_ok=0;; esac
		else
			restore_ok=0
		fi
		;;
	disabled)
		if systemctl disable "$unit" >/dev/null 2>&1; then
			actual=$(systemctl is-enabled "$unit" 2>/dev/null || true)
			[ "$actual" = disabled ] || restore_ok=0
		else
			restore_ok=0
		fi
		;;
	*) restore_ok=0 ;;
esac

active=unknown
if [ -f "$lock/previous-active" ]; then
	active=$(cat "$lock/previous-active" 2>/dev/null) || active=unknown
fi
case "$active" in
	active)
		if systemctl restart "$unit" >/dev/null 2>&1 &&
			systemctl is-active --quiet "$unit"; then
			:
		else
			restore_ok=0
		fi
		;;
	inactive)
		if systemctl stop "$unit" >/dev/null 2>&1 &&
			! systemctl is-active --quiet "$unit"; then
			:
		else
			restore_ok=0
		fi
		;;
	*) restore_ok=0 ;;
esac

if [ "$restore_ok" = 1 ]; then
	if rm -rf "$lock"; then
		exit 0
	fi
fi

echo "olcRTC rollback incomplete; recovery lock retained" >&2
exit 1
