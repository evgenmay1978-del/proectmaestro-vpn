#!/usr/bin/env bash
# One-command deploy of maestro-panel on SERVER 1 (193.17.183.48 / ubuntu24). Run as root.
#
# No secrets live in this script: the 3x-ui Bearer token is read from the vpn_bot
# env, the server-2 root password from /root/.ssh/.s2pass (falls back to a hidden
# prompt). The Reality params below are client-facing (shipped in every client
# config), not secret.
set -euo pipefail

VPN_BOT_ENV=/root/vpn_bot/.env
S2_PASS_FILE=/root/.ssh/.s2pass
REPO=/root/maestrovpn-tv

XTOKEN=$(grep -oP '^PANEL_API_TOKEN=\K.*' "$VPN_BOT_ENV" || true)
XURL=$(grep -oP '^PANEL_URL=\K.*' "$VPN_BOT_ENV" || true)
[ -n "$XTOKEN" ] || { echo "ERROR: PANEL_API_TOKEN not found in $VPN_BOT_ENV" >&2; exit 1; }
[ -n "$XURL" ]   || { echo "ERROR: PANEL_URL not found in $VPN_BOT_ENV" >&2; exit 1; }
if [ -f "$S2_PASS_FILE" ]; then S2PASS=$(cat "$S2_PASS_FILE"); else read -rsp "server-2 root password: " S2PASS; echo; fi
# СБП number for in-app purchase (Тинькофф / Сбер), per the owner. Deliberately NOT hard-coded:
# this repository is public, and a payment number sitting in a public tree is free food for
# scrapers and payment fraud. Sources, in order: $MAESTRO_SBP_PHONE → whatever is already live in
# /etc/maestro-panel.env (the re-run case) → an interactive prompt. Empty is refused below: the
# panel would otherwise serve customers a purchase screen with nothing to pay to.
SBPPHONE="${MAESTRO_SBP_PHONE:-}"
DEVICE_LIMIT="${MAESTRO_DEVICE_LIMIT:-}"
# СБП pay link (T-Bank «Сбор денег» — cross-bank, no acquiring) shown as a scannable QR.
SBPPAYURL="https://tbank.ru/cf/AL6tPKPozJo"
# owner Telegram notify (reuse the vpn_bot's token + admin id; send-only, no poll conflict)
TGTOKEN=$(grep -oP '^BOT_TOKEN=\K.*' "$VPN_BOT_ENV" || true)
TGADMIN=$(grep -oP '^ADMIN_IDS?=\K[^,[:space:]]*' "$VPN_BOT_ENV" || true)
# preserve the admin token + (TLS) sub base + device-limit switch across re-runs
ADMIN=""
SUBBASE="https://wapmixx.ru:8911"
if [ -f /etc/maestro-panel.env ]; then
    ADMIN=$(grep -oP '^MAESTRO_ADMIN_TOKEN=\K.*' /etc/maestro-panel.env || true)
    EXIST_SUB=$(grep -oP '^MAESTRO_SUB_BASE=\K.*' /etc/maestro-panel.env || true)
    [ -n "$EXIST_SUB" ] && SUBBASE="$EXIST_SUB"
    EXIST_PAY=$(grep -oP '^MAESTRO_SBP_PAY_URL=\K.*' /etc/maestro-panel.env || true)
    [ -n "$EXIST_PAY" ] && SBPPAYURL="$EXIST_PAY"
    EXIST_PHONE=$(grep -oP '^MAESTRO_SBP_PHONE=\K.*' /etc/maestro-panel.env || true)
    [ -n "$EXIST_PHONE" ] && SBPPHONE="$EXIST_PHONE"
    EXIST_DEVICE_LIMIT=$(grep -oP '^MAESTRO_DEVICE_LIMIT=\K.*' /etc/maestro-panel.env || true)
    [ -n "$EXIST_DEVICE_LIMIT" ] && [ -z "$DEVICE_LIMIT" ] && DEVICE_LIMIT="$EXIST_DEVICE_LIMIT"
fi
[ -n "$DEVICE_LIMIT" ] || DEVICE_LIMIT=on
# Last resort on a FIRST install: ask. Refusing empty is the point — writing an empty
# MAESTRO_SBP_PHONE would silently ship a purchase screen customers cannot pay from.
if [ -z "$SBPPHONE" ]; then
    read -rp "СБП phone for in-app purchase (e.g. +79991234567): " SBPPHONE || true
fi
[ -n "$SBPPHONE" ] || { echo "ERROR: СБП phone is empty — set MAESTRO_SBP_PHONE or answer the prompt" >&2; exit 1; }
[ -n "$ADMIN" ] || ADMIN=$(openssl rand -hex 24)

install -d -m 700 /var/lib/maestro
umask 077
cat > /etc/maestro-panel.env <<EOF
MAESTRO_LISTEN=127.0.0.1:8910
MAESTRO_STORE=/var/lib/maestro/customers.json
MAESTRO_ORDER_STORE=/var/lib/maestro/orders.json
MAESTRO_ADMIN_TOKEN=$ADMIN
MAESTRO_SUB_BASE=$SUBBASE
MAESTRO_DEVICE_LIMIT=$DEVICE_LIMIT
MAESTRO_SBP_PHONE=$SBPPHONE
MAESTRO_SBP_PAY_URL=$SBPPAYURL
MAESTRO_TG_BOT_TOKEN=$TGTOKEN
MAESTRO_TG_ADMIN_ID=$TGADMIN
XUI_BASE_URL=$XURL
XUI_HOST=wapmixx.ru
XUI_TOKEN=$XTOKEN
XUI_INSECURE=1
XUI_INBOUND=2
S2_HOST=85.137.166.237
S2_USER=root
S2_PASSWORD=$S2PASS
S2_HY2_PORT=8443
# Naive (Caddy forward_proxy on server 2) — app users managed via the Caddyfile
# MTV-MANAGED block over SSH (mtv_ prefix; existing users untouched).
NAIVE_SERVER=wapmix.duckdns.org
NAIVE_PORT=443
NAIVE_SNI=wapmix.duckdns.org
VLESS_SERVER=wapmixx.ru
VLESS_PORT=443
VLESS_SNI=www.intel.com
VLESS_PBK=4vp-gUBILwT5wG3iAn4uBDTd-pvrvis9_Nc2R9rIpR8
VLESS_SID=b83d8ff4
VLESS_FLOW=xtls-rprx-vision
VLESS_FP=chrome
HY2_SERVER=wapmix.duckdns.org
HY2_SNI=wapmix.duckdns.org
HY2_INSECURE=1
EOF
chmod 600 /etc/maestro-panel.env
echo "[1/4] /etc/maestro-panel.env written (chmod 600)"

( cd "$REPO/backend" && /usr/local/go/bin/go build -o /usr/local/bin/maestro-panel ./cmd/maestro-panel )
echo "[2/4] binary -> /usr/local/bin/maestro-panel"

cp "$REPO/deploy/maestro-panel.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now maestro-panel
sleep 2
echo "[3/4] service: $(systemctl is-active maestro-panel)"
echo "[4/4] healthz: $(curl -s http://127.0.0.1:8910/healthz)"
journalctl -u maestro-panel -n 4 --no-pager | grep -iE 'provisioning|listening' || true

echo
echo "Admin token (use for POST /admin/provision):"
grep '^MAESTRO_ADMIN_TOKEN=' /etc/maestro-panel.env
echo "NEXT: TLS-expose 127.0.0.1:8910 as https://wapmixx.ru:8910 (DEPLOY.md step 4), then provision a customer + give them the code."
