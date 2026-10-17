#!/bin/sh
# Assign / swap an olcRTC carrier (Yandex Telemost) room from ONE command — run on S1.
#
#   ops/olcrtc-room.sh <login> "https://telemost.yandex.ru/j/<id>"   # PER-LOGIN room + exit (isolation)
#   ops/olcrtc-room.sh "https://telemost.yandex.ru/j/<id>"           # GLOBAL fallback room (back-compat)
#
# PER-LOGIN (the family-isolation path): each family login (wapmix/wapmixx/wapmix2/…) gets its OWN
# Telemost room + its OWN exit on S3, so two members never share a room+srv → no cross-latch. This:
#   1. reuses that login's existing shared key (or mints a fresh 64-hex one the FIRST time) so a
#      room-only swap never desyncs the srv;
#   2. POSTs the login's {room,key} to the panel (POST /admin/olcrtc/room) → that login's /sub +
#      /info serve its OWN room on the next poll (≤15 min);
#   3. writes /opt/olcrtc/rooms/<login>.yaml on S3 and (re)starts olcrtc-srv@<login> → that login's
#      exit joins the room. The GLOBAL olcrtc-srv is left untouched as the fallback for logins with
#      no dedicated room.
#
# A Telemost room can expire; when it does, create a fresh one in the Yandex UI and re-run this with
# the SAME login and the new URL — the key is preserved, only the room moves.
#
# ⛔ Reads MAESTRO_ADMIN_TOKEN from /etc/maestro-panel.env. SSHes root@S3 to update that login's srv.
set -eu

usage() {
	echo "usage: $0 <login> <room> [telemost|wbstream] [newkey]   # per-login (isolation)"
	echo "       $0 <telemost-room-url>                           # global fallback room"
	echo "   telemost/jitsi room = http(s) URL ; wbstream room = bare id (UUID)"
	exit 1
}

LOGIN=""
ROOM=""
NEWKEY=0
PROVIDER=telemost
# Positional: <login> <room> [provider] [newkey]  (or <room> for the global telemost swap).
# provider ∈ {telemost, wbstream}; newkey forces a fresh per-login key.
if [ $# -eq 1 ]; then
	ROOM="$1"
else
	LOGIN="$1"; ROOM="$2"; shift 2
	for a in "$@"; do
		case "$a" in
			telemost|wbstream) PROVIDER="$a" ;;
			newkey) NEWKEY=1 ;;
			*) usage ;;
		esac
	done
fi
[ -n "$ROOM" ] || usage
# Room shape per carrier: wbstream = bare room id (UUID); everything else = http(s) URL.
if [ "$PROVIDER" = wbstream ]; then
	case "$ROOM" in
		*[!A-Za-z0-9._~-]* | "") echo "wbstream room must be a bare id (A-Za-z0-9._~-)"; exit 1 ;;
	esac
	[ -n "$LOGIN" ] || { echo "wbstream is per-login only (needs a <login>)"; exit 1; }
else
	case "$ROOM" in
		https://* | http://*) ;;
		*) echo "room must be an http(s) URL"; exit 1 ;;
	esac
fi
# Guard the login against shell/YAML/systemd-instance injection (same charset the panel accepts).
if [ -n "$LOGIN" ]; then
	case "$LOGIN" in
		*[!A-Za-z0-9._-]*) echo "login has illegal chars (allowed: A-Za-z0-9._-)"; exit 1 ;;
	esac
fi

PANEL=http://127.0.0.1:8910
S3=46.30.42.151
# Parse the token from the systemd EnvironmentFile (KEY=VALUE — not always valid shell to source).
TOKEN=$(grep -E '^MAESTRO_ADMIN_TOKEN=' /etc/maestro-panel.env | head -1 | cut -d= -f2- | sed 's/^"//;s/"$//')
[ -n "$TOKEN" ] || { echo "MAESTRO_ADMIN_TOKEN not in /etc/maestro-panel.env"; exit 1; }

# ── GLOBAL room swap (no login) — the original one-room behaviour ─────────────────────────────────
if [ -z "$LOGIN" ]; then
	echo "→ 1/3 panel GLOBAL olcRTC room (fallback logins update via /info)"
	HTTP=$(curl -s -o /tmp/olc-room.out -w '%{http_code}' -X POST "$PANEL/admin/olcrtc/room" \
		-H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "{\"room\":\"$ROOM\"}")
	[ "$HTTP" = 200 ] || { echo "  ❌ panel update failed (HTTP $HTTP)"; exit 1; }
	echo "  ✅ panel updated: $(sed 's/"key":"[0-9a-fA-F]*"/"key":"<redacted>"/g' /tmp/olc-room.out)"

	echo "→ 2/3 S3 GLOBAL srv server.yaml + restart"
	ssh -o StrictHostKeyChecking=no "root@$S3" "
		set -e
		test -f /opt/olcrtc/server.yaml
		sed -i 's#^  id: .*#  id: \"$ROOM\"#' /opt/olcrtc/server.yaml
		grep -q '$ROOM' /opt/olcrtc/server.yaml || { echo 'sed missed the room line'; exit 1; }
		systemctl restart olcrtc-srv
		sleep 8
		systemctl is-active olcrtc-srv
	"
	echo "→ 3/3 verify S3 rejoined"
	ssh -o StrictHostKeyChecking=no "root@$S3" "journalctl -u olcrtc-srv -n 12 --no-pager | grep -E 'KCP started|Link connected|peers count' || echo '  (no join line yet — check journalctl -u olcrtc-srv)'"
	echo "✅ global room swapped. Clients pick it up on next /info poll (≤15 min)."
	exit 0
fi

# ── PER-LOGIN room+exit (family isolation) ────────────────────────────────────────────────────────
echo "→ 1/4 resolve $LOGIN's key (its OWN per-login key; mint one the first time / on 'newkey')"
curl -s -o /tmp/olc-get.out -H "Authorization: Bearer $TOKEN" "$PANEL/admin/olcrtc" >/dev/null || true
# Read ONLY this login's dedicated key (NOT the global one) so every family login is isolated by
# BOTH a distinct room AND a distinct key — the global key/room stay the fallback for wapmix/others.
KEY=$(python3 -c "import json
try:
    d=json.load(open('/tmp/olc-get.out'))
    print(((d.get('rooms') or {}).get('$LOGIN') or {}).get('key',''))
except Exception:
    print('')")
if [ "$NEWKEY" = 1 ] || [ -z "$KEY" ]; then
	KEY=$(openssl rand -hex 32)
	echo "  minted a fresh 64-hex key for $LOGIN"
else
	echo "  reusing $LOGIN's existing per-login key"
fi
# Validate KEY is 64 hex (defence-in-depth before it lands in YAML on S3).
case "$KEY" in
	*[!0-9a-fA-F]* | "") echo "  ❌ bad key"; exit 1 ;;
esac
[ "${#KEY}" -eq 64 ] || { echo "  ❌ key must be 64 hex chars (got ${#KEY})"; exit 1; }

echo "→ 2/4 panel per-login room for $LOGIN (provider=$PROVIDER; its /sub + /info serve THIS room)"
HTTP=$(curl -s -o /tmp/olc-room.out -w '%{http_code}' -X POST "$PANEL/admin/olcrtc/room" \
	-H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
	-d "{\"login\":\"$LOGIN\",\"room\":\"$ROOM\",\"key\":\"$KEY\",\"provider\":\"$PROVIDER\"}")
[ "$HTTP" = 200 ] || { echo "  ❌ panel update failed (HTTP $HTTP)"; exit 1; }
echo "  ✅ panel updated for $LOGIN (key redacted)"

# Build the auth block for the S3 srv. wbstream must join AS THE ACCOUNT (else the room dies when
# no owner is present) → embed the account token; telemost/jitsi join anonymously.
if [ "$PROVIDER" = wbstream ]; then
	WBTOK=$(cat /var/lib/maestro/wb.token 2>/dev/null | tr -d '\r\n')
	[ -n "$WBTOK" ] || { echo "  ❌ no wbstream account token at /var/lib/maestro/wb.token — set it in the panel first"; exit 1; }
	AUTHBLOCK="auth:
  provider: wbstream
  token: \"$WBTOK\""
else
	AUTHBLOCK="auth:
  provider: $PROVIDER"
fi

echo "→ 3/4 S3 exit olcrtc-srv@$LOGIN → rooms/$LOGIN.yaml + (re)start"
ssh -o StrictHostKeyChecking=no "root@$S3" "
	set -e
	mkdir -p /opt/olcrtc/rooms /opt/olcrtc/data
	umask 077
	cat > /opt/olcrtc/rooms/$LOGIN.yaml <<YAML
mode: srv
$AUTHBLOCK
room:
  id: \"$ROOM\"
crypto:
  key: \"$KEY\"
net:
  transport: vp8channel
  dns: \"8.8.8.8:53\"
liveness:
  interval: 10s
  timeout: 5s
  failures: 3
socks:
  host: \"127.0.0.1\"
  port: 8808
vp8:
  fps: 30
  batch_size: 64
data: /opt/olcrtc/data
YAML
	grep -q '$ROOM' /opt/olcrtc/rooms/$LOGIN.yaml || { echo 'room not written'; exit 1; }
	systemctl enable olcrtc-srv@$LOGIN >/dev/null 2>&1 || true
	systemctl restart olcrtc-srv@$LOGIN
	sleep 8
	systemctl is-active olcrtc-srv@$LOGIN
"

echo "→ 4/4 verify $LOGIN's exit joined its room"
ssh -o StrictHostKeyChecking=no "root@$S3" "journalctl -u olcrtc-srv@$LOGIN -n 15 --no-pager | grep -E 'KCP started|Link connected|peers count' || echo '  (no join line yet — check journalctl -u olcrtc-srv@$LOGIN)'"
echo "✅ $LOGIN isolated on its own room + exit. $LOGIN's device picks it up on next /info poll (≤15 min); re-select olcRTC to switch immediately."
