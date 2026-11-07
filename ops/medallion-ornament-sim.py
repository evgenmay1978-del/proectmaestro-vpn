#!/usr/bin/env python3
"""PIL-симулятор декоративного слоя вокруг медальона (MedallionOrnament.kt, 07.11.2026).

Повторяет числа из MedallionOrnamentGeometry.kt / MedallionOrnament.kt и PhoneDashboard.kt
(размер героя, бокс медальона, подпись статуса) и рисует ACTIVE/INACTIVE поверх реальных
ассетов: phone_home_wood, рамы из mobile_approved_atlas, mobile_medallion_disc_on/off,
mobile_medallion_m_idle. Плюс проверки: симметрия, отступ от диска/камней, клип над подписью.

    python3 -I ops/medallion-ornament-sim.py [out_dir]   # по умолчанию build/medallion-ornament-sim

Это симуляция по числам из Kotlin, НЕ снимок устройства.
"""
import math
import os
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFont

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RES = os.path.join(ROOT, "app/src/main/res")
OUT = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "build/medallion-ornament-sim")

# --- MedallionOrnamentGeometry.kt ---
CLEAR = 0.81
GUIDE_REACH = 1.26
SPIKE_REACH = 0.99
RAY_START = CLEAR + 0.02
RAY_REACH = 0.62
RAY_MIN_LEN = 0.05
RAY_COUNT = 96
RAY_AXIS_GAP = 10.0
RAY_PATTERN = [1, 0.42, 0.7, 0.5, 0.85, 0.46, 1, 0.46, 0.85, 0.5, 0.7, 0.42]

# --- ApprovedMobileArtwork.kt ---
ASPECT = 720 / 712
DIAM = 570 / 712
LEFT = 72 / 712
TOP = 5 / 720


def layout(width, height, ml, mt, ms, bottom_limit, margin):
    r = ms / 2
    cx, cy = ml + r, mt + r
    half = min(cx, width - cx)
    vhalf = max(0.0, min(cy, min(height, bottom_limit) - cy))
    clip = (cx - half, cy - vhalf, cx + half, cy + vhalf)
    rays = []
    for k in range(RAY_COUNT):
        deg = k * 360.0 / RAY_COUNT
        if abs(((deg + 45) % 90) - 45) < RAY_AXIS_GAP:
            continue
        p = RAY_PATTERN[k % len(RAY_PATTERN)]
        a = math.radians(deg)
        dx, dy = math.cos(a), math.sin(a)
        tx = (clip[2] - cx) / dx if dx > 1e-6 else (clip[0] - cx) / dx if dx < -1e-6 else 1e9
        ty = (clip[3] - cy) / dy if dy > 1e-6 else (clip[1] - cy) / dy if dy < -1e-6 else 1e9
        s = RAY_START * r
        e = min((RAY_START + RAY_REACH * p) * r, min(tx, ty) - margin)
        if e - s < RAY_MIN_LEN * r:
            continue
        rays.append((cx + dx * s, cy + dy * s, cx + dx * e, cy + dy * e, p))
    return dict(cx=cx, cy=cy, r=r, clip=clip, g0=CLEAR * r, g1=min(GUIDE_REACH * r, half - margin),
                s0=CLEAR * r, top=min(SPIKE_REACH * r, vhalf - margin),
                bot=min(SPIKE_REACH * r, vhalf - margin), rays=rays)


# --- палитры MedallionOrnament.kt ---
def C(h):
    return np.array([(h >> 16) & 255, (h >> 8) & 255, h & 255], float)


GOLD = [C(0xFFE6A0), C(0xD3A447), C(0x8C601E), C(0x4A3010)]
BRONZE = [C(0x5B4630), C(0x443321), C(0x2E2216), C(0x1C140B)]
EM_ON = [C(0x8CFFC0), C(0x17C25A), C(0x06572A)]
EM_OFF = [C(0x123522), C(0x0B2516), C(0x05140C)]
EM_GLOW, RAY_GOLD, RAY_BRONZE = C(0x2EF27A), C(0xFFD877), C(0x6B5434)


def lerp(a, b, t):
    return a + (b - a) * t


class Canvas:
    """RGB float-буфер с альфа-композитингом примитивов по маске и цветовой функции."""

    def __init__(self, img, clip):
        self.a = np.asarray(img.convert("RGB"), float).copy()
        h, w = self.a.shape[:2]
        self.Y, self.X = np.mgrid[0:h, 0:w].astype(float) + 0.5
        x0, y0, x1, y1 = clip
        self.clip = ((self.X >= x0) & (self.X <= x1) & (self.Y >= y0) & (self.Y <= y1)).astype(float)

    def comp(self, mask, rgb, alpha):
        al = (mask * alpha * self.clip)[..., None]
        self.a = self.a * (1 - al) + rgb * al

    def poly_mask(self, pts):
        m = Image.new("L", (self.a.shape[1], self.a.shape[0]), 0)
        ImageDraw.Draw(m).polygon([tuple(p) for p in pts], fill=255)
        return np.asarray(m, float) / 255

    def stroke_mask(self, pts, width):
        m = Image.new("L", (self.a.shape[1], self.a.shape[0]), 0)
        ImageDraw.Draw(m).line([tuple(p) for p in pts] + [tuple(pts[0])], fill=255, width=max(1, round(width)))
        return np.asarray(m, float) / 255

    def ramp(self, t, stops):
        t = np.clip(t, 0, 1)
        out = np.zeros(t.shape + (3,))
        for (p0, c0), (p1, c1) in zip(stops, stops[1:]):
            sel = (t >= p0) & (t <= p1)
            k = ((t - p0) / max(p1 - p0, 1e-6))[..., None]
            out[sel] = (c0 + (c1 - c0) * k)[sel]
        return out


def metal_stops(m):
    hi, mid, low, edge = m
    return [(0, edge), (0.22, hi), (0.5, mid), (0.82, low), (1, edge)]


def draw_jewel(cv, cx, cy, hw, hh, m, em, glow, level):
    if glow > 0.001:
        gr = max(hw, hh) * 1.8
        d = np.hypot(cv.X - cx, cv.Y - cy) / gr
        cv.comp((d < 1).astype(float), EM_GLOW, 0.30 * glow * np.clip(1 - d, 0, 1))
    rh = lambda x, y, a, b: [(x, y - b), (x + a, y), (x, y + b), (x - a, y)]
    fm = cv.poly_mask(rh(cx, cy, hw, hh))
    cv.comp(fm, cv.ramp((cv.Y - (cy - hh)) / (2 * hh), metal_stops(m)), 1)
    cv.comp(cv.stroke_mask(rh(cx, cy, hw, hh), max(1, hw * 0.06)), m[3], 1)
    iw, ih = hw * 0.58, hh * 0.58
    sm = cv.poly_mask(rh(cx, cy, iw, ih))
    t = ((cv.X - (cx - iw)) + (cv.Y - (cy - ih))) / (2 * (iw + ih))
    cv.comp(sm, cv.ramp(t, [(0, em[0]), (0.5, em[1]), (1, em[2])]), 1)
    if level > 0.001:
        cv.comp(cv.poly_mask(rh(cx - iw * 0.28, cy - ih * 0.30, iw * 0.26, ih * 0.22)), C(0xFFFFFF), 0.55 * level)


def render(ornament_on, level, breath, img, L, density):
    cv = Canvas(img, L["clip"])
    cx, cy, r = L["cx"], L["cy"], L["r"]
    m = [lerp(b, g, level) for b, g in zip(BRONZE, GOLD)]
    em = [lerp(o, n, level) for o, n in zip(EM_OFF, EM_ON)]
    glow = level * breath
    if ornament_on:
        if glow > 0.001:  # ореол
            x0, y0, x1, y1 = L["clip"]
            reach = min(1.12 * r, cy - y0, y1 - cy, x1 - cx)
            inner = 0.74 * r
            if reach > inner + 0.08 * r:
                peak = min(0.82 * r, (inner + reach) / 2)
                d = np.hypot(cv.X - cx, cv.Y - cy)
                a = np.interp(d, [inner, peak, reach], [0, 0.15 * glow, 0], left=0, right=0)
                cv.comp(np.ones_like(d), RAY_GOLD, a)
        # лучи
        col = lerp(RAY_BRONZE, RAY_GOLD, level)
        w = max(1, r * 0.0055)
        for x0, y0, x1, y1, p in L["rays"]:
            al = 0.045 + ((0.30 + 0.35 * p) * breath - 0.045) * level
            vx, vy = x1 - x0, y1 - y0
            ln = math.hypot(vx, vy)
            tt = ((cv.X - x0) * vx + (cv.Y - y0) * vy) / (ln * ln)
            dist = np.abs((cv.X - x0) * vy - (cv.Y - y0) * vx) / ln
            mk = np.clip(w / 2 + 0.5 - dist, 0, 1) * ((tt >= 0) & (tt <= 1))
            cv.comp(mk, col, al * np.clip(1 - tt, 0, 1))
        # направляющие
        for side in (-1, 1):
            u0, ue = L["g0"], L["g1"]
            if ue - u0 < 0.18 * r:
                continue
            X = lambda u: cx + side * u
            for dy in (-0.052 * r, 0.052 * r):
                xa, xb = X(u0 + 0.02 * r), X(u0 + (ue - u0) * 0.9)
                tt = np.abs(cv.X - X(u0)) / abs(xb - X(u0))
                mk = (np.abs(cv.Y - (cy + dy)) <= max(1, r * 0.008) / 2 + 0.5) & (cv.X >= min(xa, xb)) & (cv.X <= max(xa, xb))
                cv.comp(mk.astype(float), m[1], np.clip(1 - tt, 0, 1))
            t0, t1 = 0.030 * r, 0.020 * r
            ub = max(min(u0 + 0.12 * r, ue - 0.14 * r), u0 + 0.09 * r)
            bar = [(X(u0), cy - t0), (X(ub), cy - t1), (X(ue), cy), (X(ub), cy + t1), (X(u0), cy + t0)]
            cv.comp(cv.poly_mask(bar), cv.ramp((cv.Y - (cy - t0)) / (2 * t0), metal_stops(m)), 1)
            ch, cw = 0.048 * r, 0.030 * r
            xl = min(X(u0), X(u0 + cw))
            cv.comp(cv.poly_mask([(xl, cy - ch), (xl + cw, cy - ch), (xl + cw, cy + ch), (xl, cy + ch)]),
                    cv.ramp((cv.Y - (cy - ch)) / (2 * ch), metal_stops(m)), 1)
            draw_jewel(cv, X(ub), cy, 0.072 * r, 0.085 * r, m, em, glow, level)
            if ue - ub > 0.16 * r:
                draw_jewel(cv, X(ub + (ue - ub) * 0.55), cy, 0.040 * r, 0.048 * r, m, em, glow, level)
        # наконечники
        for d, ve in ((-1, L["top"]), (1, L["bot"])):
            v0 = L["s0"]
            if ve - v0 < 0.06 * r:
                continue
            Yf = lambda v: cy + d * v
            cw, ch = 0.068 * r, 0.026 * r
            yt = min(Yf(v0), Yf(v0 + ch))
            cv.comp(cv.poly_mask([(cx - cw, yt), (cx + cw, yt), (cx + cw, yt + ch), (cx - cw, yt + ch)]),
                    cv.ramp((cv.X - (cx - cw)) / (2 * cw), metal_stops(m)), 1)
            jv, jh = v0 + ch + 0.050 * r, 0.050 * r
            bs = jv + jh
            if ve > bs + 0.04 * r:
                bw = 0.030 * r
                cv.comp(cv.poly_mask([(cx - bw, Yf(bs - 0.01 * r)), (cx, Yf(ve)), (cx + bw, Yf(bs - 0.01 * r))]),
                        cv.ramp((cv.X - (cx - bw)) / (2 * bw), metal_stops(m)), 1)
                for s in (-1, 1):
                    cv.comp(cv.poly_mask([(cx + s * 0.020 * r, Yf(bs)), (cx + s * 0.090 * r, Yf(bs + 0.055 * r)),
                                          (cx + s * 0.030 * r, Yf(bs + 0.030 * r))]), m[1], 1)
            if ve >= jv + jh:
                draw_jewel(cv, cx, Yf(jv), 0.042 * r, jh, m, em, glow, level)
    return Image.fromarray(np.clip(cv.a, 0, 255).astype(np.uint8))


def checks(L, hero_h, caption_top):
    cx, cy, r = L["cx"], L["cy"], L["r"]
    x0, y0, x1, y1 = L["clip"]
    gem = 402 / 512 * r
    errs = []
    if abs((cx - x0) - (x1 - cx)) > 0.01:
        errs.append("clip asymmetric")
    if L["g0"] <= gem or L["s0"] <= gem:
        errs.append("ornament touches gem plates")
    if y1 > caption_top + 0.01:
        errs.append("clip below caption")
    for ray in L["rays"]:
        if math.hypot(ray[0] - cx, ray[1] - cy) <= gem:
            errs.append("ray inside disc")
        for x, y in ((ray[0], ray[1]), (ray[2], ray[3])):
            if not (x0 - 0.01 <= x <= x1 + 0.01 and y0 - 0.01 <= y <= y1 + 0.01):
                errs.append("ray outside clip")
    if cy + L["bot"] > y1 + 0.01:
        errs.append("bottom spike over caption")
    mir = {(round(2 * cx - a, 1), round(b, 1), round(2 * cx - c, 1), round(d, 1)) for a, b, c, d, _ in L["rays"]}
    own = {(round(a, 1), round(b, 1), round(c, 1), round(d, 1)) for a, b, c, d, _ in L["rays"]}
    flip = {(round(a, 1), round(2 * cy - b, 1), round(c, 1), round(2 * cy - d, 1)) for a, b, c, d, _ in L["rays"]}
    if len(own ^ flip) > 0:
        errs.append(f"rays not top/bottom symmetric ({len(own ^ flip)})")
    if abs(L["top"] - L["bot"]) > 1e-6:
        errs.append("spikes differ")
    if len(own ^ mir) > 0:
        errs.append(f"rays not mirror-symmetric ({len(own ^ mir)})")
    return errs


def screen(name, w_dp, h_dp, density, connected):
    W, H = round(w_dp * density), round(h_dp * density)
    wood = Image.open(os.path.join(RES, "drawable-nodpi/phone_home_wood.png")).convert("RGB")
    s = max(W / wood.width, H / wood.height)
    wood = wood.resize((round(wood.width * s), round(wood.height * s)))
    bg = wood.crop(((wood.width - W) // 2, (wood.height - H) // 2, (wood.width - W) // 2 + W, (wood.height - H) // 2 + H))
    atlas = Image.open(os.path.join(RES, "drawable-nodpi/mobile_approved_atlas.png")).convert("RGB")
    side = min(W * 72 / 852, 42 * density)
    bg.paste(atlas.crop((0, 0, 72, 1628)).resize((round(side), H)), (0, 0))
    bg.paste(atlas.crop((780, 0, 852, 1628)).resize((round(side), H)), (W - round(side), 0))

    # PhoneDashboard: side = поля колонки; героя считаем по формуле экрана.
    pad = 18 * density  # приблизительно (точное значение не влияет на симметрию/клип)
    max_w = W - 2 * pad
    hero_w = min(max_w, min(max(H - 460 * density, 220 * density), 360 * density) / ASPECT)
    hero_h = hero_w * ASPECT
    hx = (W - hero_w) / 2
    hy = 62 * density + 7 * density + 52 * density + 7 * density + 64 * density + 7 * density  # бренд+аккаунт+кошельки
    draw = ImageDraw.Draw(bg)
    for i, (t, ww) in enumerate((("АККАУНТ", 1), ("VPN / CDN", 1))):
        y = hy - (64 + 7 + 52 + 7) * density + i * (52 + 7) * density
        draw.rounded_rectangle((pad, y, W - pad, y + (52 if i == 0 else 64) * density - 2), 8 * density,
                               outline=(150, 110, 50), width=2)

    caption_h = (8 + 28 + 3 + 16 + 8) * density
    hero = bg.crop((round(hx), round(hy), round(hx + hero_w), round(hy + hero_h)))
    ml, mt, ms = hero_w * LEFT, hero_h * TOP, hero_w * DIAM
    L = layout(hero.width, hero.height, ml, mt, ms, hero.height - 4 * density - caption_h, 1.5 * density)
    errs = checks(L, hero.height, hero.height - caption_h)
    level = 1.0 if connected else 0.0
    hero = render(True, level, 1.0, hero, L, density)

    disc = Image.open(os.path.join(RES, "drawable-nodpi/mobile_medallion_disc_%s.png" % ("on" if connected else "off"))).convert("RGBA")
    disc = disc.resize((round(ms), round(ms)), Image.LANCZOS)
    hero = hero.convert("RGBA")
    hero.alpha_composite(disc, (round(ml), round(mt)))
    m_idle = Image.open(os.path.join(RES, "drawable-nodpi/mobile_medallion_m_idle.webp")).convert("RGBA")
    fw, fh = ms * 0.75 * 0.86, ms * 0.53125 * 0.86
    fl = ml + ms / 2 + (ms * 0.125 - ms / 2) * 0.86
    ft = mt + ms / 2 + (ms * 0.2734375 - ms / 2) * 0.86
    hero.alpha_composite(m_idle.resize((round(fw), round(fh)), Image.LANCZOS), (round(fl), round(ft)))
    if connected:  # MonogramGlow: изумрудная кромка с разрывами на камнях
        hd = ImageDraw.Draw(hero)
        R = ms / 2
        st = R * 13 / 285
        rad = R * 192 / 285 + st / 2
        bx = (ml + R - rad, mt + R - rad, ml + R + rad, mt + R + rad)
        for q in range(4):
            hd.arc(bx, 90 * q + 10, 90 * q + 80, fill=(23, 209, 65), width=round(st))
    bg = bg.convert("RGBA")
    bg.alpha_composite(hero, (round(hx), round(hy)))
    font = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", round(23 * density))
    small = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", round(12 * density))
    d = ImageDraw.Draw(bg)
    ty = hy + hero_h - caption_h + 8 * density
    t1 = "Подключено" if connected else "Отключено"
    t2 = "Нажмите на медальон, чтобы отключить" if connected else "Нажмите на медальон для подключения"
    d.text((W / 2, ty), t1, font=font, anchor="mt", fill=(46, 230, 110) if connected else (205, 60, 60))
    d.text((W / 2, ty + 31 * density), t2, font=small, anchor="mt", fill=(235, 220, 190))
    out = os.path.join(OUT, f"{name}-{'active' if connected else 'inactive'}.png")
    crop = bg.crop((0, round(hy - 140 * density), W, round(hy + hero_h + 30 * density))).convert("RGB")
    crop.save(out)
    return out, errs, dict(hero_w_dp=round(hero_w / density, 1), r_dp=round(L["r"] / density, 1),
                           rays=len(L["rays"]), bottom_spike=L["bot"] >= L["s0"] + 0.06 * L["r"])


def main():
    os.makedirs(OUT, exist_ok=True)
    ok = True
    for name, w, h in (("small-360x640", 360, 640), ("mid-390x844", 390, 844), ("large-412x915", 412, 915)):
        for conn in (True, False):
            out, errs, info = screen(name, w, h, 2.0, conn)
            print(out, info, "OK" if not errs else "FAIL " + "; ".join(sorted(set(errs))))
            ok &= not errs
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
