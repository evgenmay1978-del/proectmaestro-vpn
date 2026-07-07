#!/usr/bin/env bash
# Fit the central medallion element (emerald / eye) INTO the socket at a target fill %,
# re-centred — the repeated "сделай как на эскизе / увеличь шар / отцентруй" operation.
# NON-DESTRUCTIVE by default: writes a temp preview + uploads to Yandex + prints the URL,
# so the owner sees the result before anything touches the shipping asset. --apply commits.
#
# Usage:
#   ops/socket-fit.sh <backdrop.webp> <emerald|eye> [pct=100]           # preview only
#   ops/socket-fit.sh <backdrop.webp> <emerald|eye> [pct=100] --apply   # back up + write in place
# Socket geometry (this frame): centre (428,711), inner radius 231px.
set -euo pipefail
cd "$(dirname "$0")/.."
PY=/root/vpn_bot/venv/bin/python3

SRC=${1:?"usage: ops/socket-fit.sh <backdrop.webp> <emerald|eye> [pct=100] [--apply]"}
KIND=${2:?"kind = emerald | eye"}
PCT=100; APPLY=0
shift 2 || true
for a in "$@"; do case "$a" in --apply) APPLY=1;; [0-9]*) PCT="$a";; esac; done
[ -f "$SRC" ] || { echo "no such file: $SRC" >&2; exit 2; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
if [ "$APPLY" = 1 ]; then OUT="$SRC"; else OUT="$TMP/$(basename "${SRC%.*}")_fit${PCT}.png"; fi

if [ "$APPLY" = 1 ]; then
  BKDIR=/root/.claude/maestro-asset-backups; mkdir -p "$BKDIR"
  BK="$BKDIR/$(basename "${SRC%.*}").bak-$(date +%Y%m%d-%H%M%S).webp"
  cp "$SRC" "$BK"; echo "backup: $BK"
fi

"$PY" - "$SRC" "$KIND" "$PCT" "$OUT" <<'PYEOF'
import sys
import numpy as np
from PIL import Image
from scipy import ndimage
src, kind, pct, out = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
CX, CY, RSOCK = 428.0, 711.0, 231.0

im = Image.open(src).convert("RGB")
a = np.asarray(im).astype(int)
R, G, B = a[..., 0], a[..., 1], a[..., 2]
if kind == "eye":
    m = ((R > 150) & (G > 150) & (B > 140)) | ((G >= R + 10) & (G > 90))
else:  # emerald glass
    m = (G >= R + 8) & (G > 60)
band = np.zeros(a.shape[:2], bool); band[420:1010, 120:740] = True
m &= band
lbl, n = ndimage.label(m)
if n == 0:
    sys.exit("could not detect central element")
big = 1 + int(np.argmax(ndimage.sum(np.ones_like(lbl), lbl, range(1, n + 1))))
ys, xs = np.where(lbl == big)
ccx, ccy = (xs.min() + xs.max()) / 2, (ys.min() + ys.max()) / 2
d0 = ((xs.max() - xs.min()) + (ys.max() - ys.min())) / 2
d1 = pct / 100.0 * 2 * RSOCK
scale = d1 / d0

crop_r = int(RSOCK * 1.25)
disc = im.crop((int(ccx-crop_r), int(ccy-crop_r), int(ccx+crop_r), int(ccy+crop_r)))
disc_s = disc.resize((max(1, int(disc.width*scale)), max(1, int(disc.height*scale))), Image.LANCZOS)
layer = im.copy()
layer.paste(disc_s, (int(CX - crop_r*scale), int(CY - crop_r*scale)))

yy, xx = np.mgrid[0:im.height, 0:im.width]
dist = np.sqrt((xx-CX)**2 + (yy-CY)**2)
alpha = np.clip((RSOCK*0.99 - dist)/7.0 + 0.5, 0, 1)
mask = Image.fromarray((alpha*255).astype("uint8"))
res = Image.composite(layer, im, mask)

if out.lower().endswith(".webp"):
    res.save(out, "WEBP", quality=93, method=6)
else:
    res.save(out, "PNG")
print(f"REFIT {kind}: {d0/(2*RSOCK)*100:.0f}% -> {pct:.0f}%  (scale {scale:.2f})  "
      f"centre ({ccx:.0f},{ccy:.0f}) -> ({CX:.0f},{CY:.0f})  wrote {out}")
PYEOF

bash ops/preview.sh "$OUT"
