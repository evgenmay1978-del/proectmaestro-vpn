#!/usr/bin/env bash
# TLS-expose maestro-panel's customer endpoints on server 1, reusing the existing
# Let's Encrypt wapmixx.ru cert. nginx listens on :PORT and proxies ONLY /sub +
# /claim (+ /healthz) to 127.0.0.1:8910 — /admin stays localhost-only. Run as root.
set -euo pipefail

PORT=8911
CERT=/etc/letsencrypt/live/wapmixx.ru/fullchain.pem
KEY=/etc/letsencrypt/live/wapmixx.ru/privkey.pem
[ -f "$CERT" ] && [ -f "$KEY" ] || { echo "ERROR: wapmixx.ru cert not found at $CERT" >&2; exit 1; }

cat > /etc/nginx/conf.d/maestro-panel.conf <<EOF
server {
    listen $PORT ssl;
    http2 on;
    server_name wapmixx.ru;
    ssl_certificate $CERT;
    ssl_certificate_key $KEY;

    # Only the customer-facing endpoints are public. /admin is NEVER proxied
    # (it stays reachable only on 127.0.0.1:8910).
    location /sub/    { proxy_pass http://127.0.0.1:8910; }
    location = /claim { proxy_pass http://127.0.0.1:8910; }
    location = /healthz { proxy_pass http://127.0.0.1:8910; }
    location / { return 404; }
}
EOF

nginx -t
ufw allow ${PORT}/tcp comment 'MaestroVPN TV sub/claim' || true
systemctl reload nginx

# Point the sub URLs the backend hands out at the public TLS endpoint.
sed -i "s#^MAESTRO_SUB_BASE=.*#MAESTRO_SUB_BASE=https://wapmixx.ru:${PORT}#" /etc/maestro-panel.env
systemctl restart maestro-panel
sleep 1

echo "public healthz: $(curl -sk https://wapmixx.ru:${PORT}/healthz)"
echo "MAESTRO_SUB_BASE -> https://wapmixx.ru:${PORT}  (the app's BACKEND_URL must match)"
