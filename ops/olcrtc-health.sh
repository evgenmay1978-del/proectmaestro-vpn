#!/bin/sh
# Probe each per-login olcRTC exit on S3 and write a health snapshot the web panel reads, so a
# DEAD exit shows red instead of the panel silently emitting creds that point at a room no srv is
# in. "Healthy" is stricter than is-active: the unit must be active AND have JOINED its Telemost
# room since its last (re)start (a "Link connected"/"KCP started" line) — so a running-but-can't-
# join srv (bad/expired room) is red, not green. Run by maestro-olcrtc-health.timer (~60s) on S1.
set -eu
S3="${S3_HOST:-46.30.42.151}"
OUT="${OLC_HEALTH_FILE:-/var/lib/maestro/olcrtc-health.json}"

# One SSH round-trip: emit "<login> <active> <joined 0|1>" per per-login srv (rooms/*.yaml on S3).
raw=$(ssh -o StrictHostKeyChecking=no -o ConnectTimeout=8 "root@$S3" '
  for f in /opt/olcrtc/rooms/*.yaml; do
    [ -f "$f" ] || continue
    lg=$(basename "$f" .yaml)
    unit="olcrtc-srv@$lg"
    active=$(systemctl is-active "$unit" 2>/dev/null); [ -n "$active" ] || active=unknown
    start=$(systemctl show -p ExecMainStartTimestamp --value "$unit" 2>/dev/null)
    joined=0
    if [ -n "$start" ] && journalctl -u "$unit" --since "$start" --no-pager 2>/dev/null | grep -qE "Link connected|KCP started"; then
      joined=1
    fi
    echo "$lg $active $joined"
  done
' 2>/dev/null) || raw=""

# Build the JSON on S1 (robust) + stamp the check time. An SSH failure → empty {} (panel shows
# "unknown" rather than stale green). Keys: login → {active, joined, healthy}.
printf '%s' "$raw" | TS="$(date -u +%s)" python3 -c '
import json, os, sys
out = {}
for line in sys.stdin:
    p = line.split()
    if len(p) != 3:
        continue
    lg, active, joined = p[0], p[1], p[2] == "1"
    out[lg] = {"active": active, "joined": joined, "healthy": active == "active" and joined}
print(json.dumps({"checked": int(os.environ["TS"]), "exits": out}))
' > "$OUT.tmp" && mv "$OUT.tmp" "$OUT"
