#!/bin/sh
# Deploy maestro-panel (the Go backend on S1): build → vet → backup → install → restart →
# verify the panel + existing public endpoints recovered; ROLL BACK the binary if not.
#
# ⛔ HARD RULE #1 — live paying clients: this verifies /healthz + /order/tariffs come back 200
# and the service is active; on ANY failure it restores the previous binary and restarts.
#
# Usage:
#   ops/deploy-panel.sh            # build + deploy + verify (+ rollback on failure)
#   ops/deploy-panel.sh --dry-run  # build + vet ONLY (no install, no restart) — safe pre-check
set -eu

REPO=/root/maestrovpn-tv/backend
BIN=/usr/local/bin/maestro-panel
PANEL=http://127.0.0.1:8910
DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1

cd "$REPO"
echo "→ go build ./cmd/maestro-panel"
go build -o /tmp/maestro-panel-new ./cmd/maestro-panel
echo "  ✅ built ($(stat -c%s /tmp/maestro-panel-new) bytes)"
echo "→ go vet (informational)"; go vet ./... 2>&1 | head -5 || true

if [ "$DRY" = 1 ]; then
  echo "  (dry-run) not installing/restarting"; rm -f /tmp/maestro-panel-new; exit 0
fi

BK="/root/maestro-panel.bin.bak-$(date +%s)"
cp -p "$BIN" "$BK"
mv /tmp/maestro-panel-new "$BIN"
systemctl restart maestro-panel
sleep 3

ACT=$(systemctl is-active maestro-panel)
HZ=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$PANEL/healthz" || echo 000)
TF=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$PANEL/order/tariffs" || echo 000)
echo "  panel=$ACT healthz=$HZ tariffs=$TF"

if [ "$ACT" = active ] && [ "$HZ" = 200 ] && [ "$TF" = 200 ]; then
  echo "  ✅ deployed OK (prev binary backed up: $BK)"
else
  echo "  ❌ unhealthy after deploy — ROLLING BACK to $BK"
  mv "$BK" "$BIN"; systemctl restart maestro-panel; sleep 2
  echo "  rolled back: panel=$(systemctl is-active maestro-panel) healthz=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$PANEL/healthz" || echo 000)"
  exit 1
fi
