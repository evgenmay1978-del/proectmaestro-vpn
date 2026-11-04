#!/usr/bin/env python3
"""Give the phone plates the single golden hairline from the owner's reference (05.11.2026).

The reference (m01670, "Вот так должно быть") rims every wooden plate with ONE warm gold line and a
dark seam just inside it: no vines, no double rails, wood stays the hero. tools/build-plain-phone-
plates.py had removed all framing on 04.11, so the plates now look flat next to that reference.

We repaint only the outermost 4 pixels of the alpha contour, so nine-patch markers, plate size and
the wood grain are untouched. Depth order from the plate edge inward:
    d0  existing dark rim (kept)
    d1  dark seam   (38, 22, 9)
    d2  gold        (vertical ramp 240,196,116 -> 176,126,62)
    d3  gold
    d4  shaded gold (0.45x) = the inner dark amber seam
"""
import sys
from PIL import Image, ImageFilter

RES = "app/src/main/res/drawable-nodpi"
BORDER = 1  # nine-patch marker ring


def shift(mask, px):
    return mask.filter(ImageFilter.MinFilter(px * 2 + 1))


def add_frame(path):
    img = Image.open(path).convert("RGBA")
    w, h = img.size
    inner = img.crop((BORDER, BORDER, w - BORDER, h - BORDER))
    iw, ih = inner.size

    # pad so erosion sees the plate edge as an edge (MinFilter clamps at the bitmap border)
    pad = 6
    canvas = Image.new("L", (iw + pad * 2, ih + pad * 2), 0)
    canvas.paste(inner.split()[3], (pad, pad))
    m = canvas.point(lambda v: 255 if v > 96 else 0)
    rings = [m]
    for _ in range(4):
        rings.append(shift(rings[-1], 1))

    src = inner.load()
    cols = [(38, 22, 9), None, None, None]  # d1..d4, gold ramp filled per row
    for y in range(ih):
        t = y / max(1, ih - 1)
        gold = tuple(int(c0 + (c1 - c0) * t) for c0, c1 in zip((240, 196, 116), (176, 126, 62)))
        row = [cols[0], gold, gold, tuple(int(c * 0.45) for c in gold)]
        for x in range(iw):
            px, py = x + pad, y + pad
            if not rings[0].getpixel((px, py)):
                continue
            d = 0
            for k in range(1, 5):
                if not rings[k].getpixel((px, py)):
                    d = k
                    break
            if d == 0:
                continue
            r, g, b = row[d - 1]
            a = src[x, y][3]
            src[x, y] = (r, g, b, a)

    out = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    out.paste(inner, (BORDER, BORDER))
    # keep the original marker ring intact
    m0 = Image.open(path).convert("RGBA").load()
    po = out.load()
    for x in range(w):
        for y in (0, h - 1):
            po[x, y] = m0[x, y]
    for y in range(h):
        for x in (0, w - 1):
            po[x, y] = m0[x, y]
    out.save(path)
    print("framed", path, out.size)


if __name__ == "__main__":
    base = sys.argv[1] if len(sys.argv) > 1 else RES
    for name in ("mobile_phone_panel.9.png", "mobile_phone_bar.9.png", "mobile_phone_selected.9.png"):
        add_frame(base + "/" + name)
