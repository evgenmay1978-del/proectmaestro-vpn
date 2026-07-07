#!/usr/bin/env bash
# Show the owner an image on his phone with ZERO re-derivation.
# Converts each image (webp/png/jpg) to a mobile-friendly JPG, uploads it to the
# public Yandex preview/ area, prints the public URL. Inline chat delivery is broken
# on his mobile client, so a plain public JPG URL is the reliable channel.
#
# Usage:  ops/preview.sh <img> [<img> ...]
# Output: one https://storage.yandexcloud.net/maestro-apk/preview/<name>.jpg per file.
# Cleanup later:  ops/preview.sh --clean   (removes everything under preview/)
set -euo pipefail

PY=/root/vpn_bot/venv/bin/python3
YC=(--profile yc --endpoint-url https://storage.yandexcloud.net)
BUCKET=maestro-apk
BASE="https://storage.yandexcloud.net/${BUCKET}/preview"

if [ "${1:-}" = "--clean" ]; then
  aws "${YC[@]}" s3 rm "s3://${BUCKET}/preview/" --recursive >/dev/null 2>&1 || true
  echo "preview/ cleared"; exit 0
fi
[ $# -ge 1 ] || { echo "usage: ops/preview.sh <img> [<img> ...]" >&2; exit 2; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
for f in "$@"; do
  [ -f "$f" ] || { echo "SKIP (missing): $f" >&2; continue; }
  name="$(basename "${f%.*}").jpg"
  "$PY" - "$f" "$TMP/$name" <<'PYEOF'
import sys
from PIL import Image
im = Image.open(sys.argv[1]).convert("RGB")
if im.height > 1400:
    im = im.resize((round(im.width*1400/im.height), 1400), Image.LANCZOS)
im.save(sys.argv[2], "JPEG", quality=88, optimize=True)
PYEOF
  aws "${YC[@]}" s3 cp "$TMP/$name" "s3://${BUCKET}/preview/$name" \
      --acl public-read --content-type image/jpeg --cache-control no-cache >/dev/null
  echo "${BASE}/${name}"
done
