#!/bin/sh
# Strict S1 probe for per-login olcRTC exits on S3.
set -eu
umask 077

OLC_LIB="${MAESTRO_OLCRTC_LIB:-/usr/local/libexec/maestro-olcrtc-ssh-config.sh}"
[ -r "$OLC_LIB" ] || { echo "olcRTC SSH helper is unavailable" >&2; exit 1; }
# shellcheck disable=SC1090
. "$OLC_LIB"

raw=$(olc_ssh "set -eu; : '# maestro-phase=health'; for f in /opt/olcrtc/rooms/*.yaml; do [ -f \"\$f\" ] || continue; lg=\$(basename \"\$f\" .yaml); case \"\$lg\" in *[!A-Za-z0-9._-]*|'') continue;; esac; unit=\"olcrtc-srv@\$lg\"; active=\$(systemctl is-active \"\$unit\" 2>/dev/null || true); [ -n \"\$active\" ] || active=unknown; start=\$(systemctl show -p ExecMainStartTimestamp --value \"\$unit\" 2>/dev/null || true); joined=0; if [ -n \"\$start\" ] && journalctl -u \"\$unit\" --since \"\$start\" --no-pager 2>/dev/null | grep -qE 'Link connected|KCP started'; then joined=1; fi; printf '%s %s %s\n' \"\$lg\" \"\$active\" \"\$joined\"; done" 2>/dev/null) || raw=""

RAW_FILE=$(mktemp "${TMPDIR:-/tmp}/maestro-olc-health.XXXXXX")
trap 'rm -f "$RAW_FILE"' EXIT HUP INT TERM
printf '%s\n' "$raw" > "$RAW_FILE"
CHECKED=$(date -u +%s)
export CHECKED
python3 - "$OLC_HEALTH_FILE" "$RAW_FILE" <<'PY'
import json
import os
import re
import sys
import tempfile

destination = os.path.abspath(sys.argv[1])
raw_path = sys.argv[2]
directory = os.path.dirname(destination)
os.makedirs(directory, mode=0o700, exist_ok=True)
exits = {}
with open(raw_path, encoding="utf-8") as source:
    for line in source:
        fields = line.split()
        if len(fields) != 3:
            continue
        login, active, joined_raw = fields
        if re.fullmatch(r"[A-Za-z0-9._-]+", login) is None:
            continue
        joined = joined_raw == "1"
        exits[login] = {
            "active": active,
            "joined": joined,
            "healthy": active == "active" and joined,
        }
payload = {"checked": int(os.environ["CHECKED"]), "exits": exits}
fd, temporary = tempfile.mkstemp(prefix=".olcrtc-health.", dir=directory)
try:
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(payload, stream, separators=(",", ":"))
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, destination)
    directory_fd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
