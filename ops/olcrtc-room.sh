#!/bin/sh
# Swap the olcRTC carrier (Yandex Telemost) room EVERYWHERE from ONE command — run on S1:
#
#   ops/olcrtc-room.sh "https://telemost.yandex.ru/j/<new-id>"
#
# A Telemost room can expire; when it does, create a fresh one in the Yandex UI and paste its
# URL here. This updates BOTH consumers from the single source of truth:
#   1. the panel's GLOBAL olcRTC config (POST /admin/olcrtc/room) → every olcRTC client (the
#      gated login) picks up the new room on its next /sub/<tok>/info poll (≤15 min), automatically;
#   2. the S3 srv's server.yaml + restart → the tunnel exit rejoins the new room.
# The shared key/provider/transport are unchanged (only the room expires). First-time activation
# (setting the key + enabling) is a one-off `POST /admin/olcrtc` with the full config, not this.
#
# ⛔ Reads MAESTRO_ADMIN_TOKEN from /etc/maestro-panel.env. SSHes root@S3 to update the srv.
set -eu

ROOM="${1:-}"
[ -n "$ROOM" ] || { echo "usage: $0 <telemost-room-url>"; exit 1; }
case "$ROOM" in
  https://* | http://*) ;;
  *) echo "room must be an http(s) URL"; exit 1 ;;
esac

PANEL=http://127.0.0.1:8910
S3=46.30.42.151
# Read the token by PARSING the env file (it's a systemd EnvironmentFile — KEY=VALUE — not
# always valid shell, so `.`-sourcing it can fail). Strip surrounding quotes if any.
TOKEN=$(grep -E '^MAESTRO_ADMIN_TOKEN=' /etc/maestro-panel.env | head -1 | cut -d= -f2- | sed 's/^"//;s/"$//')
[ -n "$TOKEN" ] || { echo "MAESTRO_ADMIN_TOKEN not in /etc/maestro-panel.env"; exit 1; }

echo "→ 1/3 panel global olcRTC config (clients update via /info)"
HTTP=$(curl -s -o /tmp/olc-room.out -w '%{http_code}' -X POST "$PANEL/admin/olcrtc/room" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d "{\"room\":\"$ROOM\"}")
[ "$HTTP" = 200 ] || { echo "  ❌ panel update failed (HTTP $HTTP)"; exit 1; }
# Redact the hex key before printing (this output is relayed to the owner's Telegram).
echo "  ✅ panel updated: $(sed 's/"key":"[0-9a-fA-F]*"/"key":"<redacted>"/' /tmp/olc-room.out)"

echo "→ 2/3 S3 srv server.yaml + restart"
ssh -o StrictHostKeyChecking=no "root@$S3" "
  set -e
  test -f /opt/olcrtc/server.yaml
  sed -i 's#^  id: .*#  id: \"$ROOM\"#' /opt/olcrtc/server.yaml
  grep -q '$ROOM' /opt/olcrtc/server.yaml || { echo 'sed missed the room line'; exit 1; }
  systemctl restart olcrtc-srv
  sleep 8
  systemctl is-active olcrtc-srv
"

echo "→ 3/3 verify S3 rejoined the room"
ssh -o StrictHostKeyChecking=no "root@$S3" "journalctl -u olcrtc-srv -n 12 --no-pager | grep -E 'KCP started|Link connected|peers count' || echo '  (no join line yet — check journalctl -u olcrtc-srv)'"
echo "✅ room swapped. Clients pick it up on next /info poll (≤15 min); re-select olcRTC on the device to use the new room immediately."
