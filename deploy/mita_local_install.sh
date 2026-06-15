#!/usr/bin/env bash
# Additive install of the Mieru server (mita) — run DIRECTLY ON SERVER 2
# (85.137.166.237 / s1602883), as root. No SSH, no repo needed:
#
#   curl -fsSL https://raw.githubusercontent.com/evgenmay1978-del/proectmaestro-vpn/main/deploy/mita_local_install.sh | bash
#
# SAFE BY DESIGN — must not disturb the live services on this box:
#   - naive  (Caddy forward_proxy, :443)   ~14 paying users
#   - hysteria-server (:8443/udp)
# mita binds its OWN free port (TCP+UDP 2027) and runs as its own systemd unit.
# The script ABORTS if caddy or hysteria-server is not active before it starts,
# if the chosen port is already in use, and re-verifies both services after.
# Idempotent: re-running upgrades/re-applies without duplicating anything.
set -euo pipefail

MITA_VERSION="${MITA_VERSION:-3.33.0}"
PORT="${MITA_PORT:-2027}"               # TCP+UDP; MUST differ from 443 and 8443
BUSER="${BOOTSTRAP_USER:-mtv_bootstrap}"

[ "$(id -u)" = "0" ] || { echo "run as root" >&2; exit 1; }
if [ "$PORT" = "443" ] || [ "$PORT" = "8443" ]; then
  echo "refusing: port $PORT collides with naive/hysteria" >&2; exit 1
fi

echo "---- PRECHECK (must not break naive/hysteria) ----"
caddy_active=$(systemctl is-active caddy || true)
hy2_active=$(systemctl is-active hysteria-server || true)
echo "caddy=$caddy_active hysteria-server=$hy2_active"
[ "$caddy_active" = "active" ] || { echo "ABORT: caddy (naive) not active — refusing to touch this box"; exit 1; }
[ "$hy2_active"  = "active" ] || { echo "ABORT: hysteria-server not active — refusing to touch this box"; exit 1; }
if ss -lntuH 2>/dev/null | awk '{print $5}' | grep -qE "[:.]${PORT}\$"; then
  echo "ABORT: port $PORT already in use:"; ss -lntu | grep ":${PORT} " || true; exit 1
fi

echo "---- INSTALL mita $MITA_VERSION (idempotent) ----"
ARCH=$(dpkg --print-architecture)        # amd64 / arm64
have=$(dpkg-query -W -f='${Version}' mita 2>/dev/null || true)
if [ "$have" = "$MITA_VERSION" ]; then
  echo "mita $MITA_VERSION already installed — skipping download"
else
  TMP=$(mktemp -d)
  url="https://github.com/enfein/mieru/releases/download/v${MITA_VERSION}/mita_${MITA_VERSION}_${ARCH}.deb"
  echo "downloading $url"
  curl -fLS -o "$TMP/mita.deb" "$url"
  # Wait out any background apt / unattended-upgrade holding the dpkg lock —
  # never force-remove the lock (can corrupt the package DB).
  for i in $(seq 1 120); do
    fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/cache/apt/archives/lock >/dev/null 2>&1 || break
    [ "$i" = 1 ] && echo "dpkg is locked by another apt process — waiting for it to finish…"
    sleep 5
  done
  dpkg -i "$TMP/mita.deb" || { echo "retrying dpkg in 10s…"; sleep 10; dpkg -i "$TMP/mita.deb"; }
  rm -rf "$TMP"
fi
systemctl enable mita >/dev/null 2>&1 || true

echo "---- APPLY config (TCP+UDP :$PORT, one bootstrap user) ----"
BPASS=$(head -c18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c16)
mkdir -p /etc/mita
cat > /etc/mita/maestro_config.json <<CFG
{
  "portBindings": [
    { "port": $PORT, "protocol": "TCP" },
    { "port": $PORT, "protocol": "UDP" }
  ],
  "users": [
    { "name": "$BUSER", "password": "$BPASS" }
  ],
  "loggingLevel": "INFO",
  "mtu": 1400
}
CFG
mita apply config /etc/mita/maestro_config.json
mita start || true
sleep 2

echo "---- POSTCHECK ----"
echo "mita: $(mita status 2>/dev/null || echo '??')"
caddy_after=$(systemctl is-active caddy || true)
hy2_after=$(systemctl is-active hysteria-server || true)
echo "AFTER: caddy=$caddy_after hysteria-server=$hy2_after"
[ "$caddy_after" = "active" ] || { echo "WARNING: caddy state changed!"; exit 1; }
[ "$hy2_after"  = "active" ] || { echo "WARNING: hysteria state changed!"; exit 1; }
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow "$PORT"/tcp >/dev/null 2>&1 || true
  ufw allow "$PORT"/udp >/dev/null 2>&1 || true
  echo "ufw: allowed $PORT tcp+udp"
fi
echo
echo "==> DONE: mita on :$PORT (tcp+udp); naive + hysteria untouched."
echo "==> Now redeploy the panel ON SERVER 1: bash /root/maestrovpn-tv/deploy/install.sh"
