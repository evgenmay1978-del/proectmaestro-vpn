#!/usr/bin/env bash
# Additive install of the Mieru server (mita) on server 2 (85.137.166.237).
#
# SAFE BY DESIGN — must never disturb the live services on that box:
#   - naive  (Caddy forward_proxy, :443)        ~14 paying users
#   - hysteria-server (:8443/udp)               app-owned hy2
# mita binds its OWN free port (TCP+UDP 2027) and runs as its own systemd unit.
# The script ABORTS if caddy or hysteria-server is not active before it starts,
# if the chosen port is already in use, and re-verifies both services after.
#
# Idempotent: re-running upgrades/re-applies without duplicating anything.
# Run ON server 1 (this host); it SSHes to server 2 with the stored root password.
#
#   bash deploy/s2_install_mita.sh
#
set -euo pipefail

# ---- config (server-2 access mirrors deploy/install.sh) ----------------------
ENV_FILE=/etc/maestro-panel.env
S2_PASS_FILE=/root/.ssh/.s2pass
S2_HOST="${S2_HOST:-85.137.166.237}"
S2_USER="${S2_USER:-root}"
S2_SSH_PORT="${S2_SSH_PORT:-22}"

MITA_VERSION="${MITA_VERSION:-3.33.0}"
MITA_PORT="${MITA_PORT:-2027}"           # TCP+UDP; MUST differ from 443 and 8443
BOOTSTRAP_USER="${BOOTSTRAP_USER:-mtv_bootstrap}"

# Pull S2 password from env file or the hidden pass file (never printed).
if [ -f "$ENV_FILE" ]; then
  # shellcheck disable=SC1090
  S2_PASSWORD="$(grep -oP '^S2_PASSWORD=\K.*' "$ENV_FILE" 2>/dev/null || true)"
fi
if [ -z "${S2_PASSWORD:-}" ] && [ -f "$S2_PASS_FILE" ]; then
  S2_PASSWORD="$(cat "$S2_PASS_FILE")"
fi
if [ -z "${S2_PASSWORD:-}" ]; then
  read -rsp "server-2 root password: " S2_PASSWORD; echo
fi

command -v sshpass >/dev/null || { echo "sshpass not installed on this host" >&2; exit 1; }
if [ "$MITA_PORT" = "443" ] || [ "$MITA_PORT" = "8443" ]; then
  echo "refusing: MITA_PORT $MITA_PORT collides with naive/hysteria" >&2; exit 1
fi

s2() { sshpass -p "$S2_PASSWORD" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 \
        -p "$S2_SSH_PORT" "$S2_USER@$S2_HOST" "$@"; }

# A bootstrap password so the service has a valid initial user; real per-customer
# users are added later by maestro-panel (server2.SyncMieruUsers). Generated on
# server 2 so it never lands in logs here.
echo "==> connecting to server 2 ($S2_HOST) and running additive mita install…"

REMOTE_SCRIPT=$(cat <<REMOTE
set -euo pipefail
PORT=$MITA_PORT
VER=$MITA_VERSION
BUSER=$BOOTSTRAP_USER

echo "---- PRECHECK (must not break naive/hysteria) ----"
caddy_active=\$(systemctl is-active caddy || true)
hy2_active=\$(systemctl is-active hysteria-server || true)
echo "caddy=\$caddy_active hysteria-server=\$hy2_active"
[ "\$caddy_active" = "active" ]  || { echo "ABORT: caddy (naive) not active — refusing to touch this box"; exit 1; }
[ "\$hy2_active"  = "active" ]   || { echo "ABORT: hysteria-server not active — refusing to touch this box"; exit 1; }
# chosen port must be free (tcp+udp), and we never reuse 443/8443
if ss -lntuH 2>/dev/null | awk '{print \$5}' | grep -qE "[:.]\${PORT}\$"; then
  echo "ABORT: port \$PORT already in use on server 2"; ss -lntu | grep ":\${PORT} " || true; exit 1
fi

echo "---- INSTALL mita \$VER (idempotent) ----"
ARCH=\$(dpkg --print-architecture)   # amd64 / arm64
have=\$(dpkg-query -W -f='\${Version}' mita 2>/dev/null || true)
if [ "\$have" = "\$VER" ]; then
  echo "mita \$VER already installed — skipping download"
else
  TMP=\$(mktemp -d)
  url="https://github.com/enfein/mieru/releases/download/v\${VER}/mita_\${VER}_\${ARCH}.deb"
  echo "downloading \$url"
  curl -fLS -o "\$TMP/mita.deb" "\$url"
  dpkg -i "\$TMP/mita.deb"
  rm -rf "\$TMP"
fi
systemctl enable mita >/dev/null 2>&1 || true
systemctl status mita --no-pager 2>/dev/null | grep -E 'Loaded|Active' || true

echo "---- APPLY config (TCP+UDP :\$PORT, one bootstrap user) ----"
# generate a bootstrap password locally on server 2; print only its presence
BPASS=\$(head -c18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c16)
mkdir -p /etc/mita
cat > /etc/mita/maestro_config.json <<CFG
{
  "portBindings": [
    { "port": \$PORT, "protocol": "TCP" },
    { "port": \$PORT, "protocol": "UDP" }
  ],
  "users": [
    { "name": "\$BUSER", "password": "\$BPASS" }
  ],
  "loggingLevel": "INFO",
  "mtu": 1400
}
CFG
mita apply config /etc/mita/maestro_config.json
mita start || true
sleep 2

echo "---- POSTCHECK ----"
mita_status=\$(mita status 2>/dev/null || true)
echo "mita: \$mita_status"
caddy_after=\$(systemctl is-active caddy || true)
hy2_after=\$(systemctl is-active hysteria-server || true)
echo "AFTER: caddy=\$caddy_after hysteria-server=\$hy2_after"
[ "\$caddy_after" = "active" ] || { echo "WARNING: caddy state changed!"; exit 1; }
[ "\$hy2_after"  = "active" ] || { echo "WARNING: hysteria state changed!"; exit 1; }
# firewall: allow the new port additively (no-op if ufw inactive)
if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  ufw allow \$PORT/tcp >/dev/null 2>&1 || true
  ufw allow \$PORT/udp >/dev/null 2>&1 || true
  echo "ufw: allowed \$PORT tcp+udp"
fi
echo "==> mita listening on :\$PORT (tcp+udp); naive+hysteria untouched. Bootstrap user: \$BUSER"
echo "==> client params: mita describe config"
REMOTE
)

s2 "bash -s" <<<"$REMOTE_SCRIPT"

echo
echo "Done. Next: maestro-panel provisions per-customer mita users over SSH"
echo "(server2.SyncMieruUsers) — wire MITA_PORT=$MITA_PORT into /etc/maestro-panel.env."
