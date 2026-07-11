#!/usr/bin/env python3
"""FINAL TV-eskiz pipeline: uniform row cells + full-width bars, inpainted bg,
then a PIL simulation of the EXACT Compose scroll-Column so the rebuilt layout
can be eyeballed against the mockup before any Kotlin is written.

Mockups: /root/vpnon.png, /root/vpnoff.png (1672x941). Outputs: scratch, then repo.
"""
import json
import numpy as np
from PIL import Image

OUT = "/tmp/claude-0/-root-maestrovpn-tv/9854678b-8886-44a4-9be1-8d50e7a667ea/scratchpad/tveskiz"
ART = {"on": "/root/vpnon.png", "off": "/root/vpnoff.png"}
W, H = 1672, 941
M = 14                        # crop margin (feathered)
FEATHER = 12
PANEL_X0, PANEL_X1 = 706, 1600     # full-width bar crop x-span
COL_X0, COL_X1 = 712, 1593         # 3-col row span
GUT = 10                           # gutter between the 3 cells
TOP_PAD = 43                       # art-y of the first bar's crop-top (buy)

# button vertical extents (art-y of the frame) per state
ROWS = {
 "off": {
    "buy":    (706, 57, 1600, 150),
    "code3":  (192, 349),   # shared row extents for code/apps/share
    "update": (706, 379, 1600, 467),
    "kontakty": (706, 508, 1600, 566),
    "phone":  (706, 559, 1600, 659),
    "hint":   (706, 690, 1600, 744),
    "tg3":    (744, 868),
 },
 "on": {
    "buy":    (706, 60, 1600, 159),
    "code3":  (191, 348),
    "update": (706, 380, 1600, 479),
    "kontakty": (706, 512, 1600, 566),
    "phone":  (706, 572, 1600, 673),
    "hint":   (706, 700, 1600, 744),
    "tg3":    (755, 874),
 },
}
# gaps (art px) between successive stack elements — from the mockup
STACK = ["buy", "row_code", "update", "kontakty", "phone", "hint", "row_tg"]

RING = {"off": (364, 556, 250), "on": (376, 596, 272)}
# OFF status glyphs (art-space), measured
STATUS = {"cx": 373, "gray_cy": 813, "gray_cap": 21, "orange_cy": 857,
          "orange_cap": 22, "dot_cx": 284, "dot_cy": 813, "dot_r": 11}
# clean-wood sample rows for inpaint (glow-free)
FIELD_ROWS = {
    "on":  [(32, 54), (166, 186), (352, 376), (492, 516), (546, 558), (680, 696), (878, 908)],
    "off": [(32, 54), (160, 180), (357, 371), (477, 495), (668, 682), (880, 900)],
}
GRAIN = (32, 54)


def feather(w, h, f=FEATHER):
    a = np.ones((h, w), np.float32)
    r = np.linspace(0, 1, f, endpoint=False)
    a[:f] *= r[:, None]; a[-f:] *= r[::-1][:, None]
    a[:, :f] *= r[None, :]; a[:, -f:] *= r[::-1][None, :]
    return (a * 255).astype(np.uint8)


def smooth_row(strip, k=61):
    m = strip.astype(np.float32).mean(axis=0)
    pad = np.pad(m, ((k // 2, k // 2), (0, 0)), mode="edge")
    return np.stack([np.convolve(pad[:, c], np.ones(k) / k, "valid") for c in range(3)], 1)


def inpaint_panel(a, rows, seed0=1):
    ys = np.array([(y0 + y1) / 2 for y0, y1 in rows], float)
    profs = [smooth_row(a[y0:y1, PANEL_X0:PANEL_X1]) for y0, y1 in rows]
    g = a[GRAIN[0]:GRAIN[1], PANEL_X0:PANEL_X1].astype(np.float32)
    hp = g - smooth_row(g)[None]
    gh, gw, _ = hp.shape
    x0, x1 = PANEL_X0, PANEL_X1
    fill = np.empty((H, x1 - x0, 3), np.float32)
    for y in range(H):
        if y <= ys[0]: p = profs[0]
        elif y >= ys[-1]: p = profs[-1]
        else:
            j = int(np.searchsorted(ys, y)) - 1
            t = (y - ys[j]) / (ys[j + 1] - ys[j]); p = profs[j] * (1 - t) + profs[j + 1] * t
        band = y // gh
        row = hp[y % gh] if band % 2 == 0 else hp[gh - 1 - y % gh]
        fill[y] = p + 0.9 * np.roll(row, (band * 353) % gw, axis=0)
    # paint whole panel column, feather top/bottom into frame
    out = a.copy().astype(np.float32)
    al = np.ones(H, np.float32)
    al[:30] = np.linspace(0, 1, 30); al[-30:] = np.linspace(1, 0, 30)
    out[:, x0:x1] = fill * al[:, None, None] + out[:, x0:x1] * (1 - al[:, None, None])
    return np.clip(out, 0, 255).astype(np.uint8)


def save_crop(src, box, name):
    x0, y0, x1, y1 = [int(v) for v in box]
    x0 = max(0, x0); y0 = max(0, y0); x1 = min(W, x1); y1 = min(H, y1)
    tile = np.asarray(src)[y0:y1, x0:x1]
    rgba = np.dstack([tile, feather(x1 - x0, y1 - y0)])
    Image.fromarray(rgba, "RGBA").save(f"{OUT}/{name}.png")
    return (x1 - x0, y1 - y0)


spec = {"art_w": W, "art_h": H, "M": M, "panel_x": [PANEL_X0, PANEL_X1],
        "col_x": [COL_X0, COL_X1], "gut": GUT, "top_pad": TOP_PAD,
        "ring": RING, "status": STATUS, "stack": STACK, "sizes": {}, "cell": {}}

report = {}
for state, path in ART.items():
    src = Image.open(path).convert("RGB")
    a = np.asarray(src).copy()
    R = ROWS[state]

    # full-width bars
    for k in ["buy", "update", "kontakty", "phone", "hint"]:
        x0, y0, x1, y1 = R[k]
        w, h = save_crop(src, (x0, y0 - M, x1, y1 + M), f"tv_ek_{k}_{state}")
        spec["sizes"].setdefault(k, {})[state] = [w, h]
    # 3-col rows
    cellw = (COL_X1 - COL_X0 - 2 * GUT) / 3.0
    for rowk, keys in (("code3", ["code", "apps", "share"]), ("tg3", ["tg", "wa", "max"])):
        y0, y1 = R[rowk]
        for i, key in enumerate(keys):
            cx0 = COL_X0 + i * (cellw + GUT)
            w, h = save_crop(src, (cx0, y0 - M, cx0 + cellw, y1 + M), f"tv_ek_{key}_{state}")
            spec["sizes"].setdefault(key, {})[state] = [w, h]
        spec["cell"].setdefault(rowk, {})[state] = [int(cellw + 2 * M) if False else int(cellw), int(y1 - y0 + 2 * M)]

    # inpaint bg
    bg_arr = inpaint_panel(a, FIELD_ROWS[state])
    # also clear OFF status text (left half) so the LIVE text renders clean
    if state == "off":
        for rx0, ry0, rx1, ry1 in ([215, 792, 540, 878],):
            Ls = bg_arr[ry0:ry1, rx0 - 20:rx0 - 4].astype(np.float32).mean(1)
            Rs = bg_arr[ry0:ry1, rx1 + 4:rx1 + 20].astype(np.float32).mean(1)
            t = np.linspace(0, 1, rx1 - rx0)[None, :, None]
            bg_arr[ry0:ry1, rx0:rx1] = (Ls[:, None] * (1 - t) + Rs[:, None] * t).astype(np.uint8)
    bg = Image.fromarray(bg_arr)
    bg.save(f"{OUT}/tv_bg_{state}.png")
    # LOSSLESS: герой-фон всего ТВ (дерево/металл/глаз) — lossy-webp съедал зерно
    # текстуры, на 4K читалось «мылом» (владелец 2026-07-11). ~1.4-1.6MB — осознанно.
    bg.save(f"{OUT}/tv_bg_{state}.webp", lossless=True, method=6)

# ─────────────────────────────────────────────────────────────────────────────
# SIMULATE the Compose scroll-Column in PIL (state=off) at 1:1 art scale.
# Layout rules == the Kotlin I'm about to write.  Compare against the mockup.
# ─────────────────────────────────────────────────────────────────────────────
def simulate(state):
    bg = Image.open(f"{OUT}/tv_bg_{state}.png").convert("RGBA")
    canvas = bg.copy()
    Wp = PANEL_X1 - PANEL_X0
    cellw = (COL_X1 - COL_X0 - 2 * GUT) / 3.0
    # element (top_art_y used to derive spacers) from mockup frames+M
    R = ROWS[state]
    tops = {"buy": R["buy"][1] - M, "row_code": R["code3"][0] - M,
            "update": R["update"][1] - M, "kontakty": R["kontakty"][1] - M,
            "phone": R["phone"][1] - M, "hint": R["hint"][1] - M,
            "row_tg": R["tg3"][0] - M}
    def cro(name):
        return Image.open(f"{OUT}/tv_ek_{name}_{state}.png")
    y = TOP_PAD
    for i, el in enumerate(STACK):
        if el.startswith("row_"):
            keys = ["code", "apps", "share"] if el == "row_code" else ["tg", "wa", "max"]
            h = cro(keys[0]).height
            for j, k in enumerate(keys):
                cx = PANEL_X0 + int((Wp - 3 * cellw - 2 * GUT) / 2) + int(j * (cellw + GUT))
                canvas.alpha_composite(cro(k), (cx, int(y)))
            y += h
        else:
            im = cro(el)
            cx = PANEL_X0 + (Wp - im.width) // 2
            canvas.alpha_composite(im, (cx, int(y)))
            y += im.height
        # spacer to next element's mockup top
        if i + 1 < len(STACK):
            nxt = STACK[i + 1]
            gap = tops[nxt] - (y)  # remaining to reach next top
            y += max(0, gap)
    canvas.convert("RGB").save(f"{OUT}/sim_{state}.png")
    # diff vs mockup on the right panel only
    src = np.asarray(Image.open(ART[state]).convert("RGB")).astype(int)
    sim = np.asarray(canvas.convert("RGB")).astype(int)
    d = np.abs(sim - src).max(axis=2)
    panel = d[30:912, PANEL_X0:PANEL_X1]
    return {"panel_le8_pct": round(100 * float((panel <= 8).mean()), 2),
            "panel_le24_pct": round(100 * float((panel <= 24).mean()), 2),
            "stack_bottom_y": int(y)}

for st in ["off", "on"]:
    report[st] = simulate(st)

json.dump(spec, open(f"{OUT}/tv_eskiz_spec.json", "w"), indent=1)
print(json.dumps(report, indent=1))
