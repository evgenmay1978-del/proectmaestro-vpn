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
SB="${SINGBOX:-/usr/local/bin/sing-box}"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
for pair in \
  "rule-set-geosite/geosite-ru-available-only-inside.srs|geosite-ru-available-only-inside.srs" \
  "rule-set-geosite/geosite-ru-blocked.srs|geosite-ru-blocked.srs" \
  "rule-set-geoip/geoip-ru.srs|geoip-ru.srs"; do
  src="${pair%%|*}"; name="${pair##*|}"
  curl -fsSL --max-time 60 "$SRC/$src" -o "$tmp/$name" || { echo "rules: fetch $name failed, keeping mirror" >&2; continue; }
  [ -s "$tmp/$name" ] || continue
  # ⛔ Непустой файл ≠ валидный: раздатчик может отдать HTML-ошибку с кодом 200, и такой
  # "srs" уедет на зеркало, а у КАЖДОГО клиента sing-box откажется стартовать (правила
  # качаются до туннеля). Поэтому пропускаем через парсер и заливаем только то, что
  # реально разбирается. Нет бинаря sing-box — не гадаем, а отказываемся от заливки.
  if [ -x "$SB" ]; then
    "$SB" rule-set decompile "$tmp/$name" -o "$tmp/$name.json" >/dev/null 2>&1 \
      || { echo "rules: $name FAILED to parse — keeping mirror" >&2; continue; }
  else
    echo "rules: no sing-box at $SB — refusing to publish $name unverified" >&2; continue
  fi
  "$AWS" --profile "$PROF" --endpoint-url "$EP" s3 cp "$tmp/$name" "$B/$name" \
    --acl public-read --content-type application/octet-stream >/dev/null 2>&1 \
    && echo "rules: refreshed $name ($(stat -c%s "$tmp/$name") b)"
done
