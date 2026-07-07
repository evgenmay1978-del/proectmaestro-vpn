#!/usr/bin/env bash
# Transplant the ideal central element from an owner REFERENCE screenshot into a backdrop's
# socket — the crisp method the owner asked for ("сделай как изумруд вырезал"). Aligns by the
# socket circle (green interior / glow ring), scales to fill, feather-composites the reference's
# own pixels in (no invention, no auto-rescale-crescent). Preview by default; --apply writes.
#
#   ops/socket-transplant.sh <reference-image> <target-backdrop.webp> [--apply] [--center X,Y]
# --center X,Y  = pin the alignment point in the REFERENCE (e.g. the pupil) so the eye looks
#                 STRAIGHT. Without it, an eye is aligned by its glow-ring centre, which can sit
#                 off the pupil and make the gaze look crooked (learned 2026-07-07). For a
#                 symmetric emerald ball, omit it. Pupil ≈ darkest navy blob inside the iris.
# Socket geometry of this frame is fixed: centre (428,711), inner radius 231px.
set -euo pipefail
cd "$(dirname "$0")/.."
PY=/root/vpn_bot/venv/bin/python3
BKDIR=/root/.claude/maestro-asset-backups

REF=${1:?"usage: ops/socket-transplant.sh <reference-image> <target-backdrop.webp> [--apply] [--center X,Y]"}
TGT=${2:?"target backdrop .webp"}
APPLY=0; CENTER=""
shift 2 || true
while [ $# -gt 0 ]; do case "$1" in --apply) APPLY=1;; --center) CENTER="${2:-}"; shift;; esac; shift; done
[ -f "$REF" ] && [ -f "$TGT" ] || { echo "missing file" >&2; exit 2; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
if [ "$APPLY" = 1 ]; then
  OUT="$TGT"; mkdir -p "$BKDIR"
  BK="$BKDIR/$(basename "${TGT%.*}").bak-$(date +%Y%m%d-%H%M%S).webp"; cp "$TGT" "$BK"; echo "backup: $BK"
else
  OUT="$TMP/$(basename "${TGT%.*}")_transplant.png"
fi

"$PY" - "$REF" "$TGT" "$OUT" "$CENTER" <<'PYEOF'
import sys, numpy as np
from PIL import Image
from scipy import ndimage
ref_p, tgt_p, out = sys.argv[1], sys.argv[2], sys.argv[3]
center = sys.argv[4] if len(sys.argv) > 4 else ""
CX, CY, RSOCK = 428.0, 711.0, 231.0

ref = Image.open(ref_p).convert("RGB")
a = np.asarray(ref).astype(int); R, G, B = a[..., 0], a[..., 1], a[..., 2]
# socket circle in the reference: bright green glow ring at the inner edge (gives the SCALE)
glow = (G > 140) & (G > R + 35) & (G > B + 25)
ys, xs = np.where(glow)
if len(xs) < 200:  # fallback: green glass / non-wood interior enclosing circle
    interior = (G >= R + 8) & (G > 60)
    if interior.sum() < 200:
        interior = ~((R > G + 10) & (R > B + 20))
    lbl, n = ndimage.label(interior)
    big = 1 + int(np.argmax(ndimage.sum(np.ones_like(lbl), lbl, range(1, n + 1))))
    ys, xs = np.where(lbl == big)
gcx, gcy = (xs.min()+xs.max())/2, (ys.min()+ys.max())/2
Rr = ((xs.max()-xs.min()) + (ys.max()-ys.min())) / 4
# ALIGNMENT point: --center X,Y (e.g. pupil, for a straight gaze) overrides the glow centre
if center:
    rcx, rcy = [float(v) for v in center.split(",")]
else:
    rcx, rcy = gcx, gcy

bg = Image.open(tgt_p).convert("RGB")
s = RSOCK / Rr
ref_s = ref.resize((round(ref.width*s), round(ref.height*s)), Image.LANCZOS)
layer = Image.new("RGB", bg.size); layer.paste(ref_s, (round(CX - rcx*s), round(CY - rcy*s)))
yy, xx = np.mgrid[0:bg.height, 0:bg.width]
dist = np.sqrt((xx-CX)**2 + (yy-CY)**2)
al = np.clip((RSOCK*0.995 - dist)/6.0 + 0.5, 0, 1)
res = Image.composite(layer, bg, Image.fromarray((al*255).astype("uint8")))
if out.lower().endswith(".webp"):
    res.save(out, "WEBP", quality=93, method=6)
else:
    res.save(out, "PNG")
print(f"TRANSPLANT: ref socket R={Rr:.0f} (glow pts={len(xs)}) -> backdrop R={RSOCK:.0f}  scale {s:.3f}  wrote {out}")
PYEOF

bash ops/preview.sh "$OUT"
