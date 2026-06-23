#!/usr/bin/env bash
# rules_mirror.sh — refresh the RU-direct .srs rule-sets on our Yandex mirror from
# runetfreedom (rebuilt ~6h). The app downloads them DIRECT (RU-domestic) at startup
# (download_detour=direct), so they MUST stay reachable + fresh: a proxied fetch from
# GitHub fails before the tunnel is up ("Создание службы … context canceled") and the
# whole sing-box service refuses to start. Installed to /usr/local/bin and run by
# /etc/cron.d/maestro-rules every 6h.
set -euo pipefail
AWS="${YC_AWS:-/root/.local/bin/aws}"; PROF="${YC_PROFILE:-yc}"
EP="https://storage.yandexcloud.net"; B="s3://maestro-apk/rules"
SRC="https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
for pair in \
  "rule-set-geosite/geosite-ru-available-only-inside.srs|geosite-ru-available-only-inside.srs" \
  "rule-set-geoip/geoip-ru.srs|geoip-ru.srs"; do
  src="${pair%%|*}"; name="${pair##*|}"
  curl -fsSL --max-time 60 "$SRC/$src" -o "$tmp/$name" || { echo "rules: fetch $name failed, keeping mirror" >&2; continue; }
  [ -s "$tmp/$name" ] || continue
  "$AWS" --profile "$PROF" --endpoint-url "$EP" s3 cp "$tmp/$name" "$B/$name" \
    --acl public-read --content-type application/octet-stream >/dev/null 2>&1 \
    && echo "rules: refreshed $name ($(stat -c%s "$tmp/$name") b)"
done
