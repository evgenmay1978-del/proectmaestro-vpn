# Deploy maestro-panel (server 1, 194.48.141.106)

Production step — run as root on server 1. This brings `/sub/<token>` and `/claim`
online so the TV app can provision + auto-update.

## Quick path (everything pre-filled)
```bash
bash /root/maestrovpn-tv/deploy/install.sh
```
Reads the 3x-ui Bearer token (from `/root/vpn_bot/.env`) + server-2 password (from
`/root/.ssh/.s2pass`) server-side, writes `/etc/maestro-panel.env` with the discovered
Reality params (SNI `www.intel.com`, pbk/sid from inbound :443), builds the binary,
installs the systemd unit, starts it, and prints the admin token. Then do **step 4 (TLS)**.
The manual steps below are the same, broken out.

## 1. Build the binary
```bash
cd /root/maestrovpn-tv/backend
/usr/local/go/bin/go build -o /usr/local/bin/maestro-panel ./cmd/maestro-panel
```

## 2. Configure
```bash
cp /root/maestrovpn-tv/deploy/maestro-panel.env.example /etc/maestro-panel.env
chmod 600 /etc/maestro-panel.env
# edit /etc/maestro-panel.env and fill the __SET_ME__ / __..._ values:
#   MAESTRO_ADMIN_TOKEN  ->  openssl rand -hex 24
#   XUI_TOKEN            ->  grep PANEL_API_TOKEN /root/vpn_bot/.env
#   S2_PASSWORD          ->  the server-2 root password
#   VLESS_SNI/PBK/SID    ->  from the 3x-ui :443 inbound (panel → inbound → Reality settings)
```

## 3. Install + start
```bash
cp /root/maestrovpn-tv/deploy/maestro-panel.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now maestro-panel
systemctl status maestro-panel --no-pager
curl -s http://127.0.0.1:8910/healthz   # -> ok
```

## 4. TLS exposure (required — MAESTRO_SUB_BASE must be HTTPS-reachable by the TV)
The panel listens on `127.0.0.1:8910` only. Expose it over TLS using the existing
`wapmixx.ru` certificate, then set `MAESTRO_SUB_BASE` to that public URL:
- **Reverse proxy (recommended):** nginx/caddy `https://wapmixx.ru:8910` → `127.0.0.1:8910`.
- Keep `/admin/*` NOT publicly exposed (it's token-guarded, but bind admin to localhost
  or allowlist) — only `/sub/` and `/claim` need to be public.

## 5. Provision a customer + hand the code to the customer
```bash
TOK=$(grep MAESTRO_ADMIN_TOKEN /etc/maestro-panel.env | cut -d= -f2)
curl -s -X POST http://127.0.0.1:8910/admin/provision \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"login":"MAESTRO-TEST","days":30}'
# In the app: "Ввести код подписки" → MAESTRO-TEST → /claim → sub URL → auto-updating profile.
```

Note: provisioning calls 3x-ui (Bearer) + server-2 (SSH) — verify both reachable from
server 1 first. Existing 3x-ui/naive customers are never touched (the app uses its own
inbound clients + the `mtv_` naive prefix).
