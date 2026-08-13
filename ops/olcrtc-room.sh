#!/bin/sh
# Transactionally activate one per-login olcRTC exit on S3, then publish it to the panel.
set -eu
umask 077

usage() {
	echo "usage: $0 <login> <room> [telemost|wbstream] [newkey]" >&2
	exit 1
}

[ "$#" -ge 2 ] || usage
LOGIN="$1"
ROOM="$2"
shift 2
PROVIDER=telemost
NEWKEY=0
for arg in "$@"; do
	case "$arg" in
		telemost|wbstream) PROVIDER="$arg" ;;
		newkey) NEWKEY=1 ;;
		*) usage ;;
	esac
done

case "$LOGIN" in *[!A-Za-z0-9._-]*|"") echo "invalid login" >&2; exit 1;; esac
if [ "$PROVIDER" = wbstream ]; then
	case "$ROOM" in *[!A-Za-z0-9._~-]*|"") echo "invalid wbstream room" >&2; exit 1;; esac
else
	case "$ROOM" in http://*|https://*) ;;
		*) echo "invalid Telemost room" >&2; exit 1 ;;
	esac
	case "$ROOM" in *'
'*|*'
'*) echo "invalid room" >&2; exit 1;; esac
fi

OLC_LIB="${MAESTRO_OLCRTC_LIB:-/usr/local/libexec/maestro-olcrtc-ssh-config.sh}"
[ -r "$OLC_LIB" ] || { echo "olcRTC SSH helper is unavailable" >&2; exit 1; }
# shellcheck disable=SC1090
. "$OLC_LIB"

TMP_ROOT="${TMPDIR:-/tmp}"
WORK=$(mktemp -d "$TMP_ROOT/maestro-olcrtc.XXXXXX")
CURRENT="$WORK/current.json"
YAML="$WORK/room.yaml"
PAYLOAD="$WORK/payload.json"
CURLCFG="$WORK/curl.cfg"
OUT="$WORK/panel.out"
TXN=$(openssl rand -hex 8)
case "$TXN" in *[!0-9a-f]*|"") echo "cannot create transaction id" >&2; exit 1;; esac
STAGED=0

remote_rollback() {
	olc_ssh "set -eu; : '# maestro-phase=rollback'; lock='/run/maestro-olcrtc-$LOGIN-$TXN'; dest='/opt/olcrtc/rooms/$LOGIN.yaml'; if [ -f \"\$lock/previous.yaml\" ]; then mv -f \"\$lock/previous.yaml\" \"\$dest\"; else rm -f \"\$dest\"; fi; prev=inactive; [ ! -f \"\$lock/previous-active\" ] || prev=\$(cat \"\$lock/previous-active\"); if [ \"\$prev\" = active ]; then systemctl restart 'olcrtc-srv@$LOGIN'; else systemctl stop 'olcrtc-srv@$LOGIN' >/dev/null 2>&1 || true; fi; rm -rf \"\$lock\""
}

cleanup() {
	code=$?
	trap - EXIT HUP INT TERM
	if [ "$STAGED" = 1 ]; then remote_rollback >/dev/null 2>&1 || true; fi
	rm -rf "$WORK"
	exit "$code"
}
trap cleanup EXIT HUP INT TERM

# Prove the remote boundary before reading or minting credentials.
olc_ssh "set -eu; : '# maestro-phase=preflight'; test -d /opt/olcrtc; test -d /opt/olcrtc/rooms; test -x /opt/olcrtc/olcrtc; systemctl cat 'olcrtc-srv@.service' >/dev/null"

TOKEN=$(olc_panel_token)
[ -n "$TOKEN" ] || { echo "panel credential is unavailable" >&2; exit 1; }
case "$TOKEN" in *'
'*|*'
'*|*'"'*) echo "panel credential format is invalid" >&2; exit 1;; esac
python3 - "$CURLCFG" "$TOKEN" <<'PY'
import os, sys
path, token = sys.argv[1], sys.argv[2]
with open(path, "w", encoding="utf-8") as f:
    f.write('header = "Authorization: Bearer ' + token + '"\n')
os.chmod(path, 0o600)
PY
curl -sS --fail --config "$CURLCFG" "$PANEL_URL/admin/olcrtc" > "$CURRENT"

KEY=$(LOGIN="$LOGIN" python3 - "$CURRENT" <<'PY'
import json, os, sys
try:
    with open(sys.argv[1], encoding="utf-8") as f:
        data = json.load(f)
    print(((data.get("rooms") or {}).get(os.environ["LOGIN"]) or {}).get("key", ""))
except Exception:
    print("")
PY
)
if [ "$NEWKEY" = 1 ] || [ -z "$KEY" ]; then KEY=$(openssl rand -hex 32); fi
case "$KEY" in *[!0-9a-fA-F]*|"") echo "invalid room key" >&2; exit 1;; esac
[ "${#KEY}" -eq 64 ] || { echo "invalid room key length" >&2; exit 1; }

WBTOK=""
if [ "$PROVIDER" = wbstream ]; then
	[ -r "$WB_TOKEN_FILE" ] || { echo "wbstream credential is unavailable" >&2; exit 1; }
	WBTOK=$(tr -d '\r\n' < "$WB_TOKEN_FILE")
	case "$WBTOK" in *[!A-Za-z0-9._~-]*|"") echo "wbstream credential format is invalid" >&2; exit 1;; esac
fi

LOGIN="$LOGIN" ROOM="$ROOM" KEY="$KEY" PROVIDER="$PROVIDER" WBTOK="$WBTOK" python3 - "$YAML" <<'PY'
import json, os, stat, sys
q = json.dumps
provider = os.environ["PROVIDER"]
lines = [
    "mode: srv",
    "auth:",
    "  provider: " + q(provider),
]
if provider == "wbstream":
    lines.append("  token: " + q(os.environ["WBTOK"]))
lines += [
    "room:",
    "  id: " + q(os.environ["ROOM"]),
    "crypto:",
    "  key: " + q(os.environ["KEY"]),
    "net:",
    '  transport: "vp8channel"',
    '  dns: "8.8.8.8:53"',
    "liveness:",
    "  interval: 10s",
    "  timeout: 5s",
    "  failures: 3",
    "socks:",
    '  host: "127.0.0.1"',
    "  port: 8808",
    "vp8:",
    "  fps: 30",
    "  batch_size: 64",
    "data: /opt/olcrtc/data",
]
path = sys.argv[1]
with open(path, "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + "\n")
os.chmod(path, 0o600)
PY

olc_ssh "set -eu; : '# maestro-phase=stage'; lock='/run/maestro-olcrtc-$LOGIN-$TXN'; dest='/opt/olcrtc/rooms/$LOGIN.yaml'; mkdir \"\$lock\"; if [ -f \"\$dest\" ]; then cp -p \"\$dest\" \"\$lock/previous.yaml\"; fi; systemctl is-active 'olcrtc-srv@$LOGIN' > \"\$lock/previous-active\" 2>/dev/null || printf 'inactive\n' > \"\$lock/previous-active\"; umask 077; cat > \"\$lock/candidate.yaml\"; grep -q '^mode: srv$' \"\$lock/candidate.yaml\"; grep -q '^crypto:$' \"\$lock/candidate.yaml\"; mv -f \"\$lock/candidate.yaml\" \"\$dest\"" < "$YAML"
STAGED=1
olc_ssh "set -eu; : '# maestro-phase=restart'; test -d '/run/maestro-olcrtc-$LOGIN-$TXN'; systemctl enable 'olcrtc-srv@$LOGIN' >/dev/null 2>&1 || true; systemctl restart 'olcrtc-srv@$LOGIN'"
olc_ssh "set -eu; : '# maestro-phase=verify'; test -d '/run/maestro-olcrtc-$LOGIN-$TXN'; systemctl is-active --quiet 'olcrtc-srv@$LOGIN'; start=\$(systemctl show -p ExecMainStartTimestamp --value 'olcrtc-srv@$LOGIN'); test -n \"\$start\"; journalctl -u 'olcrtc-srv@$LOGIN' --since \"\$start\" --no-pager | grep -qE 'Link connected|KCP started'"

LOGIN="$LOGIN" ROOM="$ROOM" KEY="$KEY" PROVIDER="$PROVIDER" python3 - "$PAYLOAD" <<'PY'
import json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump({
        "login": os.environ["LOGIN"],
        "room": os.environ["ROOM"],
        "key": os.environ["KEY"],
        "provider": os.environ["PROVIDER"],
    }, f, separators=(",", ":"))
os.chmod(sys.argv[1], 0o600)
PY
HTTP=$(curl -sS --config "$CURLCFG" -o "$OUT" -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data-binary "@$PAYLOAD" "$PANEL_URL/admin/olcrtc/room")
[ "$HTTP" = 200 ] || { echo "panel rejected verified olcRTC room" >&2; exit 1; }

olc_ssh "set -eu; : '# maestro-phase=commit'; rm -rf '/run/maestro-olcrtc-$LOGIN-$TXN'"
STAGED=0
echo "olcRTC room activated and published"
