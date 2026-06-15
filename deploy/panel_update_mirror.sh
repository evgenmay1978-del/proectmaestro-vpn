#!/usr/bin/env bash
# panel_update_mirror.sh — keep the panel-hosted OTA channel current.
#
# The panel (server 1) is a datacenter VPS that CAN reach GitHub; the user's RU
# devices CANNOT (github.com / objects.githubusercontent.com throttled in RU) but
# they DO reach the panel. So the panel mirrors the latest GitHub release APK into
# MAESTRO_UPDATE_DIR and regenerates update.json, which the app's PanelUpdateChecker
# reads. Run from a systemd timer every ~15 min. Idempotent: only downloads when
# the published version differs from what is already mirrored. Atomic swap.
set -euo pipefail

REPO="${OTA_REPO:-evgenmay1978-del/proectmaestro-vpn}"
DIR="${MAESTRO_UPDATE_DIR:-/var/lib/maestro/update}"
API="https://api.github.com/repos/${REPO}/releases/latest"

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

# Atomic manifest swap.
mtmp="$DIR/.update.json.tmp"
printf '{"version_code": %s, "version_name": "%s", "apk_url": "/update/%s", "size": %s, "sha256": "%s", "notes": "Автообновление %s."}\n' \
  "${vc:-0}" "$vn" "$apk_name" "$sz" "$sha" "$vn" > "$mtmp"
mv -f "$mtmp" "$DIR/update.json"

# Prune older mirrored APKs (keep the current one).
find "$DIR" -maxdepth 1 -name 'MaestroVPN-TV-*-debug.apk' ! -name "$apk_name" -delete 2>/dev/null || true

echo "mirror: published $vn (code ${vc:-0}, ${sz} bytes) to $DIR"
