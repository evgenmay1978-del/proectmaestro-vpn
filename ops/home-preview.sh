#!/usr/bin/env bash
# Regenerate the connected/disconnected home-screen comparison from the CURRENT baked
# backdrops, measure how much of the socket the central element fills, upload all three
# images to Yandex, print the URLs. One command, 0-token repeat — use every time the
# owner wants to SEE the two VPN states after a backdrop tweak.
#
# Usage:  ops/home-preview.sh
set -euo pipefail
cd "$(dirname "$0")/.."
PY=/root/vpn_bot/venv/bin/python3
OFF=app/src/main/res/drawable-nodpi/home_backdrop.webp
ON=app/src/main/res/drawable-nodpi/home_backdrop_connected.webp
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

"$PY" - "$OFF" "$ON" "$TMP/compare.jpg" <<'PYEOF'
import sys
import numpy as np
from PIL import Image, ImageDraw, ImageFont
from scipy import ndimage
off_p, on_p, out = sys.argv[1], sys.argv[2], sys.argv[3]

# socket geometry (shared frame): centre + inner-ring radius
CX, CY, RSOCK = 428.0, 711.0, 231.0

def central_fill(path, connected):
    a = np.asarray(Image.open(path).convert("RGB")).astype(int)
    R, G, B = a[..., 0], a[..., 1], a[..., 2]
    if connected:  # eye: bright sclera OR saturated green iris
        m = ((R > 150) & (G > 150) & (B > 140)) | ((G >= R + 10) & (G > 90))
    else:          # emerald: cyan-green glass
        m = (G >= R + 8) & (G > 60)
    band = np.zeros(a.shape[:2], bool); band[420:1010, 120:740] = True
    m &= band
    lbl, n = ndimage.label(m)
    if n == 0: return 0.0
    big = 1 + int(np.argmax(ndimage.sum(np.ones_like(lbl), lbl, range(1, n + 1))))
    ys, xs = np.where(lbl == big)
    diam = ((xs.max() - xs.min()) + (ys.max() - ys.min())) / 2
    return diam / (2 * RSOCK) * 100

off_pct = central_fill(off_p, False)
on_pct  = central_fill(on_p, True)
print(f"MEASURE  disconnected(emerald) {off_pct:.0f}%   connected(eye) {on_pct:.0f}%   diff {on_pct-off_pct:+.0f}%")

# side-by-side
H = 900
def sc(p):
    im = Image.open(p).convert("RGB")
    return im.resize((round(im.width*H/im.height), H), Image.LANCZOS)
dis, con = sc(off_p), sc(on_p)
w = dis.width; gap = 48; pad = 32; labelh = 64
canvas = Image.new("RGB", (pad*2+w*2+gap, pad+labelh+H+pad), (14, 12, 10))
d = ImageDraw.Draw(canvas)
def font(sz):
    for p in ["/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
              "/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf"]:
        try: return ImageFont.truetype(p, sz)
        except Exception: pass
    return ImageFont.load_default()
f = font(34)
y = pad + labelh
canvas.paste(dis, (pad, y)); canvas.paste(con, (pad+w+gap, y))
def ctext(cx, ty, t, fill):
    bb = d.textbbox((0, 0), t, font=f); d.text((cx-(bb[2]-bb[0])/2, ty), t, font=f, fill=fill)
ctext(pad+w/2, pad+6, f"ОТКЛЮЧЁН · {off_pct:.0f}%", (200, 200, 205))
ctext(pad+w+gap+w/2, pad+6, f"ПОДКЛЮЧЁН · {on_pct:.0f}%", (90, 220, 150))
canvas.save(out, "JPEG", quality=88)
PYEOF

bash ops/preview.sh "$TMP/compare.jpg" "$OFF" "$ON"
