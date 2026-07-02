#!/bin/sh
# olcrtc-s1-test.sh — FEEDBACK LOOP for the olcRTC client patch.
#
# Builds the patched olcRTC client from /root/build/olcrtc, runs it on S1 against
# the LIVE Telemost room, and reports: does it CONNECT, does the SOCKS5 open, which
# media pair got SELECTED (udp vs tcp relay + IP), and does a real curl tunnel out.
#
# ⛔ MANDATORY before shipping ANY olcRTC canary to the owner. canary1 shipped
# WITHOUT this loop and broke olcRTC everywhere (a Yandex-CIDR SetIPFilter dropped
# every local interface IP -> zero candidate bases -> "no candidate pairs"). One
# 60s local run would have caught it. Feedback loop > guessing.
#
# NOTE: S1 is in NL (unfiltered), so this proves FUNCTIONALITY (connects + tunnels),
# NOT the RU {Yandex,VK,MAX} whitelist. TCP-relay never forms here (TCP-TURN to
# Yandex times out from foreign IPs) — the tcp path is only verifiable on a RU
# device. What this DOES guarantee: the patch didn't break candidate gathering.
#
# Usage: sh ops/olcrtc-s1-test.sh
set -eu
CLONE=${OLCRTC_CLONE:-/root/build/olcrtc}
CFG=/var/lib/maestro/olcrtc.json
PORT=8812
UNIT=olcrtc-s1-test
TMP=$(mktemp -d)
trap 'systemctl stop "$UNIT" 2>/dev/null || true; systemctl reset-failed "$UNIT" 2>/dev/null || true; rm -rf "$TMP"' EXIT

[ -f "$CFG" ] || { echo "no $CFG (olcRTC global config) — is this S1?" >&2; exit 1; }

echo "→ building patched client from $CLONE"
( cd "$CLONE" && go build -o "$TMP/olcrtc" ./cmd/olcrtc )
echo "  built $(stat -c%s "$TMP/olcrtc") bytes"

# client.yaml from the panel's global room/key (never printed)
python3 - "$CFG" "$TMP" "$PORT" <<'PY'
import json, os, sys
cfg, tmp, port = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.load(open(cfg))
os.makedirs(f"{tmp}/data", exist_ok=True)
open(f"{tmp}/client.yaml", "w").write(f"""mode: cnc
auth: {{provider: telemost}}
room: {{id: "{d.get('room','')}"}}
crypto: {{key: "{d.get('key','')}"}}
net: {{transport: vp8channel, dns: "8.8.8.8:53"}}
socks: {{host: "127.0.0.1", port: {port}}}
liveness: {{interval: 10s, timeout: 5s, failures: 3}}
vp8: {{fps: 30, batch_size: 64}}
data: {tmp}/data
""")
PY

echo "→ starting client (systemd-run — plain bg dies on S1)"
systemctl reset-failed "$UNIT" 2>/dev/null || true
# --since START pins journalctl to THIS run only — the unit name is reused across
# runs, so without it grep matches a PREVIOUS run's logs and can show a stale PASS.
START=$(date '+%Y-%m-%d %H:%M:%S')
systemd-run --unit="$UNIT" --property=Type=simple "$TMP/olcrtc" "$TMP/client.yaml" >/dev/null

i=0
while [ "$i" -lt 22 ]; do
  journalctl -u "$UNIT" --since "$START" --no-pager 2>/dev/null | grep -qiE "SOCKS5 server listening|SELECTED media pair|no candidate pairs" && break
  i=$((i + 1)); sleep 3
done
L=$(journalctl -u "$UNIT" --since "$START" --no-pager 2>/dev/null)

echo
echo "=== SELECTED pair + relay-pin mode ==="
echo "$L" | grep -iE "SELECTED media pair|forcing TURN-over-TCP-443|no transport=tcp TURN" | tail -3 || true
echo "  (note: a TURN relay candidate ALWAYS shows 'relay/udp' — that's the relay↔SFU"
echo "   leg; the client↔TURN leg is checked below by the actual socket protocol.)"
echo "=== ACTUAL media transport to Yandex (definitive — TCP:443 = shaping-resistant · UDP = shaped in RU) ==="
PID=$(systemctl show "$UNIT" -p MainPID --value 2>/dev/null)
TCP443=$(ss -tnp 2>/dev/null | grep ESTAB | grep ':443' | grep "pid=$PID," | grep -oE '(77\.88\.|37\.9\.|5\.255\.|213\.180\.|93\.158\.)[0-9.]+:443' | sort -u)
UDPY=$(ss -unp 2>/dev/null | grep "pid=$PID," | grep -oE '(77\.88\.|37\.9\.|5\.255\.)[0-9.]+:[0-9]+' | sort -u | head -3)
[ -n "$TCP443" ] && echo "  ✅ TCP-443 to Yandex: $(echo "$TCP443" | tr '\n' ' ')" || echo "  ⚠️ no TCP-443 to Yandex"
[ -n "$UDPY" ]   && echo "  ⚠️ UDP to Yandex (shaped path): $(echo "$UDPY" | tr '\n' ' ')" || echo "  ✅ no UDP to Yandex (pure TCP path)"
echo "=== connection state ==="
echo "$L" | grep -iE "SOCKS5 server listening|connection state: Connected|no candidate pairs" | tail -3 || true
echo "=== traffic test (expect the S3 exit IP; TCP-relay needs ~30-60s warm-up) ==="
sleep 12
timeout 95 curl -s --max-time 90 --socks5-hostname "127.0.0.1:$PORT" https://icanhazip.com 2>&1 | head -1 || echo "  (curl timeout/fail)"
echo
echo "→ done (unit + temp auto-cleaned)"
