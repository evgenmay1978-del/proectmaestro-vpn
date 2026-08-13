#!/bin/sh
# Transactionally activate a global or per-login olcRTC exit on S3, then publish it to the panel.
set -eu
umask 077

usage() {
	echo "usage: $0 <login> <room> [telemost|wbstream] [newkey]" >&2
	echo "       $0 <telemost-room-url>" >&2
	exit 1
}

LOGIN=""
ROOM=""
PROVIDER=telemost
NEWKEY=0
if [ "$#" -eq 1 ]; then
	ROOM="$1"
elif [ "$#" -ge 2 ]; then
	LOGIN="$1"
	ROOM="$2"
	shift 2
	for arg in "$@"; do
		case "$arg" in
			telemost|wbstream) PROVIDER="$arg" ;;
			newkey) NEWKEY=1 ;;
			*) usage ;;
		esac
	done
else
	usage
fi

case "$LOGIN" in *[!A-Za-z0-9._-]*) echo "invalid login" >&2; exit 1;; esac
LOGIN="$LOGIN" ROOM="$ROOM" PROVIDER="$PROVIDER" python3 - <<'PY'
import os, re
from urllib.parse import urlsplit
login = os.environ["LOGIN"]
room = os.environ["ROOM"]
provider = os.environ["PROVIDER"]
if not room or any(ord(ch) < 32 for ch in room):
    raise SystemExit("invalid room")
if provider == "wbstream":
    if not login or re.fullmatch(r"[A-Za-z0-9._~-]+", room) is None:
        raise SystemExit("invalid wbstream room")
else:
    url = urlsplit(room)
    if url.scheme not in {"http", "https"} or not url.hostname or url.username or url.password:
        raise SystemExit("invalid Telemost room")
PY

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

if [ -n "$LOGIN" ]; then
	REMOTE_NAME="$LOGIN"
	REMOTE_DEST="/opt/olcrtc/rooms/$LOGIN.yaml"
	REMOTE_UNIT="olcrtc-srv@$LOGIN"
else
	REMOTE_NAME=global
	REMOTE_DEST=/opt/olcrtc/server.yaml
	REMOTE_UNIT=olcrtc-srv
fi
REMOTE_LOCK="/run/maestro-olcrtc-$REMOTE_NAME.lock"

remote_rollback() {
	olc_ssh "set -eu; : '# maestro-phase=rollback'; lock='$REMOTE_LOCK'; dest='$REMOTE_DEST'; unit='$REMOTE_UNIT'; [ -d \"\$lock\" ] || exit 0; [ -f \"\$lock/owner\" ] || exit 0; owner=\$(cat \"\$lock/owner\"); [ \"\$owner\" = '$TXN' ] || exit 0; if [ -f \"\$lock/previous-exists\" ] && [ -f \"\$lock/previous.yaml\" ]; then mv -f \"\$lock/previous.yaml\" \"\$dest\"; elif [ -f \"\$lock/new-dest\" ]; then rm -f \"\$dest\"; fi; active=inactive; [ ! -f \"\$lock/previous-active\" ] || active=\$(cat \"\$lock/previous-active\"); enabled=unknown; [ ! -f \"\$lock/previous-enabled\" ] || enabled=\$(cat \"\$lock/previous-enabled\"); case \"\$enabled\" in enabled|enabled-runtime|linked|linked-runtime|alias) systemctl enable \"\$unit\" >/dev/null ;; masked|masked-runtime) systemctl mask \"\$unit\" >/dev/null ;; disabled) systemctl disable \"\$unit\" >/dev/null ;; esac; if [ \"\$active\" = active ]; then systemctl restart \"\$unit\"; else systemctl stop \"\$unit\" >/dev/null 2>&1 || true; fi; rm -rf \"\$lock\""
}

cleanup() {
	code=$?
	trap - EXIT HUP INT TERM
	if [ "$STAGED" = 1 ]; then remote_rollback >/dev/null 2>&1 || true; fi
	rm -rf "$WORK"
	exit "$code"
}
trap cleanup EXIT HUP INT TERM

if [ -n "$LOGIN" ]; then
	olc_ssh "set -eu; : '# maestro-phase=preflight'; test -d /opt/olcrtc/rooms; test -x /opt/olcrtc/olcrtc; systemctl cat 'olcrtc-srv@.service' >/dev/null"
else
	olc_ssh "set -eu; : '# maestro-phase=preflight'; test -f '/opt/olcrtc/server.yaml'; test -x /opt/olcrtc/olcrtc; systemctl cat 'olcrtc-srv.service' >/dev/null"
fi

python3 - "$CURLCFG" "$PANEL_ENV_FILE" <<'PY'
import os, re, sys
target, source = sys.argv[1:3]
token = ""
with open(source, encoding="utf-8") as stream:
    for line in stream:
        match = re.fullmatch(r'MAESTRO_ADMIN_TOKEN=(?:"([^"\r\n]*)"|([^\r\n]*))\r?\n?', line)
        if match:
            token = match.group(1) if match.group(1) is not None else match.group(2)
            break
if not token or any(ord(ch) < 33 for ch in token):
    raise SystemExit("panel credential is unavailable")
with open(target, "w", encoding="utf-8") as output:
    output.write('header = "Authorization: Bearer ' + token.replace("\\", "\\\\").replace('"', '\\"') + '"\n')
os.chmod(target, 0o600)
PY
curl -sS --fail --config "$CURLCFG" "$PANEL_URL/admin/olcrtc" > "$CURRENT"

KEY=$(LOGIN="$LOGIN" python3 - "$CURRENT" <<'PY'
import json, os, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    data = json.load(stream)
if not isinstance(data, dict):
    raise SystemExit("invalid panel state")
login = os.environ["LOGIN"]
if login:
    rooms = data.get("rooms", {})
    if rooms is None:
        rooms = {}
    if not isinstance(rooms, dict):
        raise SystemExit("invalid rooms state")
    item = rooms.get(login)
    if item is None:
        key = ""
    elif isinstance(item, dict) and isinstance(item.get("key", ""), str):
        key = item.get("key", "")
    else:
        raise SystemExit("invalid room state")
else:
    key = data.get("key", "")
    if not isinstance(key, str):
        raise SystemExit("invalid global key state")
print(key)
PY
)
if [ -n "$LOGIN" ] && { [ "$NEWKEY" = 1 ] || [ -z "$KEY" ]; }; then KEY=$(openssl rand -hex 32); fi
[ -n "$LOGIN" ] || [ -n "$KEY" ] || { echo "global olcRTC key is missing" >&2; exit 1; }
case "$KEY" in *[!0-9a-fA-F]*|"") echo "invalid room key" >&2; exit 1;; esac
[ "${#KEY}" -eq 64 ] || { echo "invalid room key length" >&2; exit 1; }

WBTOK=""
if [ "$PROVIDER" = wbstream ]; then
	olc_secure_file "$WB_TOKEN_FILE" "wbstream credential"
	WBTOK=$(tr -d '\r\n' < "$WB_TOKEN_FILE")
	case "$WBTOK" in *[!A-Za-z0-9._~-]*|"") echo "wbstream credential format is invalid" >&2; exit 1;; esac
fi

if [ -n "$LOGIN" ]; then
	LOGIN="$LOGIN" ROOM="$ROOM" KEY="$KEY" PROVIDER="$PROVIDER" WBTOK="$WBTOK" python3 - "$YAML" <<'PY'
import json, os, sys
q = json.dumps
provider = os.environ["PROVIDER"]
lines = ["mode: srv", "auth:", "  provider: " + q(provider)]
if provider == "wbstream":
    lines.append("  token: " + q(os.environ["WBTOK"]))
lines += [
    "room:", "  id: " + q(os.environ["ROOM"]),
    "crypto:", "  key: " + q(os.environ["KEY"]),
    "net:", '  transport: "vp8channel"', '  dns: "8.8.8.8:53"',
    "liveness:", "  interval: 10s", "  timeout: 5s", "  failures: 3",
    "socks:", '  host: "127.0.0.1"', "  port: 8808",
    "vp8:", "  fps: 30", "  batch_size: 64",
    "data: /opt/olcrtc/data",
]
with open(sys.argv[1], "w", encoding="utf-8") as output:
    output.write("\n".join(lines) + "\n")
    output.flush()
    os.fsync(output.fileno())
os.chmod(sys.argv[1], 0o600)
PY
fi

COMMON_STAGE="lock='$REMOTE_LOCK'; dest='$REMOTE_DEST'; unit='$REMOTE_UNIT'; owned=0; stage_cleanup() { code=\$?; trap - EXIT HUP INT TERM; if [ \"\$owned\" = 1 ]; then if [ -f \"\$lock/previous-exists\" ] && [ -f \"\$lock/previous.yaml\" ]; then mv -f \"\$lock/previous.yaml\" \"\$dest\"; elif [ -f \"\$lock/new-dest\" ]; then rm -f \"\$dest\"; fi; active=inactive; [ ! -f \"\$lock/previous-active\" ] || active=\$(cat \"\$lock/previous-active\"); enabled=unknown; [ ! -f \"\$lock/previous-enabled\" ] || enabled=\$(cat \"\$lock/previous-enabled\"); case \"\$enabled\" in enabled|enabled-runtime|linked|linked-runtime|alias) systemctl enable \"\$unit\" >/dev/null ;; masked|masked-runtime) systemctl mask \"\$unit\" >/dev/null ;; disabled) systemctl disable \"\$unit\" >/dev/null ;; esac; if [ \"\$active\" = active ]; then systemctl restart \"\$unit\" >/dev/null 2>&1 || true; else systemctl stop \"\$unit\" >/dev/null 2>&1 || true; fi; rm -rf \"\$lock\"; fi; exit \"\$code\"; }; trap stage_cleanup EXIT HUP INT TERM; mkdir \"\$lock\"; owned=1; printf '%s\n' '$TXN' > \"\$lock/owner\"; systemctl is-active \"\$unit\" > \"\$lock/previous-active\" 2>/dev/null || printf 'inactive\n' > \"\$lock/previous-active\"; systemctl is-enabled \"\$unit\" > \"\$lock/previous-enabled\" 2>/dev/null || printf 'disabled\n' > \"\$lock/previous-enabled\"; if [ -f \"\$dest\" ]; then cp -p \"\$dest\" \"\$lock/previous.yaml.tmp\"; sync \"\$lock/previous.yaml.tmp\"; mv -f \"\$lock/previous.yaml.tmp\" \"\$lock/previous.yaml\"; : > \"\$lock/previous-exists\"; else : > \"\$lock/new-dest\"; fi; umask 077"

if [ -n "$LOGIN" ]; then
	olc_ssh "set -eu; : '# maestro-phase=stage'; $COMMON_STAGE; cat > \"\$lock/candidate.yaml\"; grep -q '^mode: srv$' \"\$lock/candidate.yaml\"; grep -q '^crypto:$' \"\$lock/candidate.yaml\"; mv -f \"\$lock/candidate.yaml\" \"\$dest\"; trap - EXIT HUP INT TERM" < "$YAML"
else
	printf '%s\n' "$ROOM" | olc_ssh "set -eu; : '# maestro-phase=stage'; $COMMON_STAGE; cat > \"\$lock/room\"; printf '%s' 'aW1wb3J0IGpzb24sIG9zLCByZSwgc3lzCnNvdXJjZSwgcm9vbV9wYXRoLCB0YXJnZXQgPSBzeXMuYXJndlsxOjRdCnJvb20gPSBvcGVuKHJvb21fcGF0aCwgZW5jb2Rpbmc9InV0Zi04IikucmVhZCgpLnJzdHJpcCgiXG4iKQppZiBub3Qgcm9vbSBvciBhbnkob3JkKGNoKSA8IDMyIGZvciBjaCBpbiByb29tKToKICAgIHJhaXNlIFN5c3RlbUV4aXQoImludmFsaWQgZ2xvYmFsIHJvb20iKQpsaW5lcyA9IG9wZW4oc291cmNlLCBlbmNvZGluZz0idXRmLTgiKS5yZWFkKCkuc3BsaXRsaW5lcygpCmZvdW5kID0gMAppbl9yb29tID0gRmFsc2UKZm9yIGluZGV4LCBsaW5lIGluIGVudW1lcmF0ZShsaW5lcyk6CiAgICBpZiBsaW5lID09ICJyb29tOiI6CiAgICAgICAgaW5fcm9vbSA9IFRydWUKICAgICAgICBjb250aW51ZQogICAgaWYgaW5fcm9vbSBhbmQgcmUubWF0Y2gociJeICBpZDoiLCBsaW5lKToKICAgICAgICBsaW5lc1tpbmRleF0gPSAiICBpZDogIiArIGpzb24uZHVtcHMocm9vbSkKICAgICAgICBmb3VuZCArPSAxCiAgICAgICAgaW5fcm9vbSA9IEZhbHNlCiAgICBlbGlmIGluX3Jvb20gYW5kIGxpbmUgYW5kIG5vdCBsaW5lLnN0YXJ0c3dpdGgoIiAiKToKICAgICAgICBpbl9yb29tID0gRmFsc2UKaWYgZm91bmQgIT0gMToKICAgIHJhaXNlIFN5c3RlbUV4aXQoImdsb2JhbCByb29tLmlkIG5vdCBmb3VuZCBleGFjdGx5IG9uY2UiKQp3aXRoIG9wZW4odGFyZ2V0LCAidyIsIGVuY29kaW5nPSJ1dGYtOCIpIGFzIG91dHB1dDoKICAgIG91dHB1dC53cml0ZSgiXG4iLmpvaW4obGluZXMpICsgIlxuIikKICAgIG91dHB1dC5mbHVzaCgpCiAgICBvcy5mc3luYyhvdXRwdXQuZmlsZW5vKCkpCg==' | base64 -d | python3 - \"\$dest\" \"\$lock/room\" \"\$lock/candidate.yaml\"; mv -f \"\$lock/candidate.yaml\" \"\$dest\"; trap - EXIT HUP INT TERM"
fi
STAGED=1

olc_ssh "set -eu; : '# maestro-phase=restart'; lock='$REMOTE_LOCK'; [ -f \"\$lock/owner\" ]; owner=\$(cat \"\$lock/owner\"); [ \"\$owner\" = '$TXN' ]; systemctl enable '$REMOTE_UNIT' >/dev/null; systemctl is-enabled --quiet '$REMOTE_UNIT'; systemctl restart '$REMOTE_UNIT'"
olc_ssh "set -eu; : '# maestro-phase=verify'; lock='$REMOTE_LOCK'; [ -f \"\$lock/owner\" ]; owner=\$(cat \"\$lock/owner\"); [ \"\$owner\" = '$TXN' ]; systemctl is-active --quiet '$REMOTE_UNIT'; start=\$(systemctl show -p ExecMainStartTimestamp --value '$REMOTE_UNIT'); test -n \"\$start\"; i=0; while [ \"\$i\" -lt 12 ]; do if journalctl -u '$REMOTE_UNIT' --since \"\$start\" --no-pager | grep -qE 'Link connected|KCP started'; then exit 0; fi; i=\$((i + 1)); sleep 5; done; exit 1"

LOGIN="$LOGIN" ROOM="$ROOM" KEY="$KEY" PROVIDER="$PROVIDER" python3 - "$PAYLOAD" <<'PY'
import json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as output:
    json.dump({
        "login": os.environ["LOGIN"],
        "room": os.environ["ROOM"],
        "key": os.environ["KEY"],
        "provider": os.environ["PROVIDER"],
    }, output, separators=(",", ":"))
    output.flush()
    os.fsync(output.fileno())
os.chmod(sys.argv[1], 0o600)
PY
HTTP=$(curl -sS --config "$CURLCFG" -o "$OUT" -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data-binary "@$PAYLOAD" "$PANEL_URL/admin/olcrtc/room")
[ "$HTTP" = 200 ] || { echo "panel rejected verified olcRTC room" >&2; exit 1; }

# Publication is the point of no return: never roll S3 back after the panel accepted the same state.
STAGED=0
if ! olc_ssh "set -eu; : '# maestro-phase=commit'; lock='$REMOTE_LOCK'; [ -f \"\$lock/owner\" ]; owner=\$(cat \"\$lock/owner\"); [ \"\$owner\" = '$TXN' ]; rm -rf \"\$lock\""; then
	echo "olcRTC state published; remote lock cleanup is pending" >&2
fi
echo "olcRTC room activated and published"
