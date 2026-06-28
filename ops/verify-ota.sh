#!/bin/sh
# Verify the OTA delivery chain end-to-end: RU Yandex mirror ↔ panel-for-fleet ↔ the 107 waypoint.
# Read-only by default; --sync first triggers the mirror upload service (after a fresh release).
#
# ⛔ The 107 waypoint is MANDATORY (AWG-engine checkpoint): a device on <107 MUST be offered 1.0.107
# first. This script fails loudly if that breaks. See [[update-delivery-ru]] in memory.
#
# Usage:
#   ops/verify-ota.sh           # verify the live chain
#   ops/verify-ota.sh --sync    # sync the mirror from the latest GitHub release first, then verify
set -u

MIRROR=https://storage.yandexcloud.net/maestro-apk/update.json
PANEL=https://wapmixx.ru:8911/update/update.json
ver() { curl -s --max-time 12 "$1" 2>/dev/null | jq -r '.version_name // .versionName // "?"'; }

if [ "${1:-}" = "--sync" ]; then
  echo "→ syncing mirror from latest GitHub release…"
  systemctl start maestro-update-mirror.service 2>/dev/null
  sleep 15
fi

MV=$(ver "$MIRROR")
PF=$(curl -s --max-time 10 -A 'SFA/1.0.109 (109; sing-box)' "$PANEL" 2>/dev/null | jq -r '.version_name // .versionName // "?"')
WP=$(curl -s --max-time 10 -A 'SFA/1.0.105 (105; sing-box)' "$PANEL" 2>/dev/null | jq -r '.version_name // .versionName // "?"')

echo "  mirror update.json  : $MV"
echo "  panel → fleet (vc109): $PF"
echo "  panel → <107 (vc105) : $WP   (must be 1.0.107)"
if [ "$WP" = "1.0.107" ]; then
  echo "  ✅ 107 waypoint INTACT"
else
  echo "  ⚠️⚠️ 107 WAYPOINT BROKEN — check env MAESTRO_UPDATE_WAYPOINTS=107 + /var/lib/maestro/update/update-107.json + the 1.0.107 APK on the mirror"
  exit 1
fi
