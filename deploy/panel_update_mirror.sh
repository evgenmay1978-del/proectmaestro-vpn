#!/usr/bin/env bash
# panel_update_mirror.sh — keep the OTA channel current, served from a Russia-domestic
# mirror (Yandex Object Storage) so RU devices download updates fast/unthrottled.
#
# The panel (server 1, NL) CAN reach GitHub; RU devices CANNOT (github.com /
# objects.githubusercontent.com throttled in RU), and even the panel itself is a slow,
# resettable foreign hop for a ~90 MB APK. So this script: mirrors the latest GitHub
# release APK, UPLOADS it to Yandex Object Storage (RU-domestic), and writes update.json
# with apk_url pointing at the Yandex URL. The local panel copy stays as a fallback (and as
# the bots' latest.apk). Run from a systemd timer every ~15 min. Idempotent: only acts when
# the published version differs from what is already mirrored. Atomic manifest swap.
set -euo pipefail

REPO="${OTA_REPO:-evgenmay1978-del/proectmaestro-vpn}"
DIR="${MAESTRO_UPDATE_DIR:-/var/lib/maestro/update}"
API="https://api.github.com/repos/${REPO}/releases/latest"

# Russia-domestic mirror (Yandex Object Storage, S3-compatible). Credentials live in
# /root/.aws (profile YC_PROFILE), mode 0600. If the upload fails, fall back to the
# panel-hosted apk_url so updates keep working (just slower) rather than breaking.
YC_AWS="${YC_AWS:-/root/.local/bin/aws}"
YC_PROFILE="${YC_PROFILE:-yc}"
YC_ENDPOINT="${YC_ENDPOINT:-https://storage.yandexcloud.net}"
YC_BUCKET="${YC_BUCKET:-maestro-apk}"
YC_PUBLIC_BASE="${YC_PUBLIC_BASE:-https://storage.yandexcloud.net/${YC_BUCKET}}"

mkdir -p "$DIR"

rel="$(curl -fsSL --max-time 60 -H 'Accept: application/vnd.github+json' "$API")" || {
  echo "mirror: GitHub API unreachable, keeping existing channel" >&2; exit 0; }

vn="$(printf '%s' "$rel" | python3 -c 'import sys,json,re
d=json.load(sys.stdin); t=d.get("tag_name","")
print(re.sub(r"^tv-v","",t))')"
[ -n "$vn" ] || { echo "mirror: no tag_name in latest release" >&2; exit 0; }

# Already current?
cur=""
[ -f "$DIR/update.json" ] && cur="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("version_name",""))
except Exception: print("")' "$DIR/update.json")" || true
if [ "$cur" = "$vn" ]; then
  exit 0
fi

# Resolve assets: the non-play, non-legacy .apk + the version-metadata.json.
apk_url="$(printf '%s' "$rel" | python3 -c 'import sys,json
d=json.load(sys.stdin)
for a in d.get("assets",[]):
    n=a["name"]
    if n.endswith(".apk") and "play" not in n and "legacy-android-5" not in n:
        print(a["browser_download_url"]); break')"
vc="$(printf '%s' "$rel" | python3 -c 'import sys,json,urllib.request
d=json.load(sys.stdin); url=""
for a in d.get("assets",[]):
    if a["name"]=="version-metadata.json": url=a["browser_download_url"]
if url:
    try:
        m=json.load(urllib.request.urlopen(url,timeout=60)); print(m.get("version_code",0))
    except Exception: print(0)
else: print(0)')"
[ -n "$apk_url" ] || { echo "mirror: no apk asset in $vn" >&2; exit 0; }

apk_name="MaestroVPN-TV-${vn}-debug.apk"
tmp="$DIR/.${apk_name}.tmp"
curl -fsSL --max-time 600 "$apk_url" -o "$tmp" || { echo "mirror: apk download failed" >&2; rm -f "$tmp"; exit 0; }
sz="$(stat -c %s "$tmp")"
sha="$(sha256sum "$tmp" | cut -d' ' -f1)"
mv -f "$tmp" "$DIR/$apk_name"

# Upload to the Russia-domestic mirror (Yandex Object Storage). apk_path_url starts as the
# panel-hosted fallback; switch to the Yandex URL ONLY once the upload actually succeeds, so
# a transient S3 failure degrades to slow-but-working rather than a broken channel.
apk_path_url="/update/${apk_name}"
if "$YC_AWS" --profile "$YC_PROFILE" --endpoint-url "$YC_ENDPOINT" \
     s3 cp "$DIR/$apk_name" "s3://${YC_BUCKET}/${apk_name}" \
     --acl public-read --content-type application/vnd.android.package-archive >/dev/null 2>&1; then
  apk_path_url="${YC_PUBLIC_BASE}/${apk_name}"
  echo "mirror: uploaded $apk_name to Yandex Object Storage"
  # Prune older versioned APKs — but KEEP the current one AND any mandatory WAYPOINT APKs
  # (MAESTRO_UPDATE_WAYPOINTS from the panel env): a device behind a waypoint still downloads
  # that frozen version, so its APK must never be pruned (see api.go updateManifestFor).
  WAYPOINTS="$(grep -oP '^MAESTRO_UPDATE_WAYPOINTS=\K.*' /etc/maestro-panel.env 2>/dev/null || true)"
  keep="$apk_name"
  for wp in ${WAYPOINTS//,/ }; do
    [ -n "$wp" ] && keep="${keep}|MaestroVPN-TV-1.0.${wp}-debug.apk"
  done
  old_list="$("$YC_AWS" --profile "$YC_PROFILE" --endpoint-url "$YC_ENDPOINT" s3 ls "s3://${YC_BUCKET}/" 2>/dev/null \
    | awk '{print $4}' | grep -E '^MaestroVPN-TV-.*\.apk$' | grep -vE "^(${keep})$" || true)"
  for old in $old_list; do
    "$YC_AWS" --profile "$YC_PROFILE" --endpoint-url "$YC_ENDPOINT" s3 rm "s3://${YC_BUCKET}/${old}" >/dev/null 2>&1 || true
  done
else
  echo "mirror: Yandex upload failed — using panel-hosted apk_url fallback" >&2
fi

# Atomic manifest swap.
mtmp="$DIR/.update.json.tmp"
printf '{"version_code": %s, "version_name": "%s", "apk_url": "%s", "size": %s, "sha256": "%s", "notes": "Автообновление %s."}\n' \
  "${vc:-0}" "$vn" "$apk_path_url" "$sz" "$sha" "$vn" > "$mtmp"
mv -f "$mtmp" "$DIR/update.json"

# Freeze a per-version copy so the panel can serve it as a WAYPOINT manifest to devices that
# must step through this version before a newer one (api.go updateManifestFor). Its apk_url
# points at this version's Yandex APK, which the waypoint-aware prune above keeps alive.
cp -f "$DIR/update.json" "$DIR/update-${vc:-0}.json"

# HA: mirror the manifest to Yandex too — it is otherwise served ONLY by S1's nginx, so if S1
# dies the app can't even DISCOVER updates (the APK binary already survives on Yandex). This
# lets a future app build fall back to the public Yandex update.json. Best-effort; panel copy
# stays primary.
"$YC_AWS" --profile "$YC_PROFILE" --endpoint-url "$YC_ENDPOINT" \
  s3 cp "$DIR/update.json" "s3://${YC_BUCKET}/update.json" \
  --acl public-read --content-type application/json --cache-control no-cache >/dev/null 2>&1 \
  && echo "mirror: uploaded update.json manifest to Yandex" || true

# Stable "always latest" copy for the bots' download link (panel + Yandex mirror).
cp -f "$DIR/$apk_name" "$DIR/latest.apk"
if [ "$apk_path_url" != "/update/${apk_name}" ]; then
  "$YC_AWS" --profile "$YC_PROFILE" --endpoint-url "$YC_ENDPOINT" s3 cp "$DIR/latest.apk" "s3://${YC_BUCKET}/latest.apk" \
    --acl public-read --content-type application/vnd.android.package-archive >/dev/null 2>&1 || true
fi

# Prune older mirrored APKs locally (keep the current one + latest.apk).
find "$DIR" -maxdepth 1 -name 'MaestroVPN-TV-*-debug.apk' ! -name "$apk_name" -delete 2>/dev/null || true

echo "mirror: published $vn (code ${vc:-0}, ${sz} bytes) — apk_url=$apk_path_url"
