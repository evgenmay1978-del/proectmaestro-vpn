#!/usr/bin/env python3
"""tv-mobile-kit — ТВ-ассеты v4 из МАТЕРИАЛА МОБИЛЬНОЙ версии (заказ owner 2026-07-12:
«универсальный материал, своя ТВ-раскладка, один экран без скролла, старьё под снос»).

Источники — ТОЛЬКО из репо (воспроизводимо):
  home_backdrop.webp / home_backdrop_connected.webp  853×1844 — герой (лого+подвес+медальон),
      дерево, периметр-рама с плющом (углы/рейки), статусная зона
  frame_bar.9.png   1042×348 — рама с изумрудами (бары)
  frame_button.9.png 270×162 — чистый золотой безель (плитки/чипы)
  frame_qr.webp      760×760 — золотая рама QR телефона (Поделиться)

Выход (drawable-nodpi, lossless webp):
  tvm_bg_off / tvm_bg_on   1920×1080 — цельный фон: дерево + рама + ГЕРОЙ (сфера/глаз) + свет+зерно
  tvm_wood_bg              1920×1080 — то же БЕЗ героя (глобальный фон второстепенных экранов)
  tvm_cta/sq/tile/bar2/phone/msg/chip/chip_sel/account/trial — панели правой зоны + аккаунт/триал
      (канва = inner + поля M=28 с запечённой тенью; НИЧЕГО не растягивается: концы native-scaled,
       рельса тайлом с фейд-стыками, gem-кластер по центру, интерьер = телефонное дерево + объём)
  tvm_wood_tile  ~360×240 — бесшовный тайл телефонного дерева (ImageShader carvedSurface)
  tvm_qr_bezel   760×760  — рама QR с ПРОЗРАЧНЫМ проёмом ±308 (урок qr-decorative-frame-lesson)

Геометрия ТВ-хоума (арт 1920×1080, INNER-прямоугольники, M=28) — ДОЛЖНА совпадать с TvEskizSpec:
  hero crop(54,52,800,1018) ×0.86 @ (104,26) → орб (422, 600.5) r≈176
  CTA (856,76,868,100) + GEAR (1752,76,100,100) | TILES 3×(317×142) y208 шаг341
  BAR2 2×(486×86) y374 x856/1366 | PHONE (856,532,996,84) | MSG 3×(317×72) y672
  CHIPS 4×2 (236×80) x шаг254 y818 шаг96 | ACCOUNT/TRIAL (112,964,626,66)
Запуск: /root/vpn_bot/venv/bin/python3 ops/tv-mobile-kit.py
"""
import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageOps

RES = "/root/maestrovpn-tv/app/src/main/res/drawable-nodpi"
W, H = 1920, 1080
M = 28  # поля панель-канвы под запечённую тень

PB = Image.open(f"{RES}/home_backdrop.webp").convert("RGB")
PBO = Image.open(f"{RES}/home_backdrop_connected.webp").convert("RGB")
FB = Image.open(f"{RES}/frame_bar.9.png").convert("RGBA").crop((1, 1, 1041, 347))    # срез 9p-маркеров
FBT = Image.open(f"{RES}/frame_button.9.png").convert("RGBA").crop((1, 1, 269, 161))
FQR = Image.open(f"{RES}/frame_qr.webp").convert("RGBA")

SALAD = (205, 227, 123)


def feather_paste(dst, src, xy, feather=24, sides="ltrb"):
    w, h = src.size
    m = np.ones((h, w), np.float32)
    ramp = np.linspace(0.0, 1.0, feather, dtype=np.float32)
    if "l" in sides: m[:, :feather] *= ramp[None, :]
    if "r" in sides: m[:, -feather:] *= ramp[::-1][None, :]
    if "t" in sides: m[:feather, :] *= ramp[:, None]
    if "b" in sides: m[-feather:, :] *= ramp[::-1][:, None]
    dst.paste(src, xy, Image.fromarray((m * 255).astype(np.uint8), "L"))


def tile_wood(patch, w, h, overlap=30):
    pw, ph = patch.size
    out = Image.new("RGB", (w, h))
    y = ry = 0
    while y < h:
        x = rx = 0
        while x < w:
            t = patch
            if rx % 2: t = ImageOps.mirror(t)
            if ry % 2: t = ImageOps.flip(t)
            if x == 0 and y == 0:
                out.paste(t, (0, 0))
            else:
                sides = ("l" if x > 0 else "") + ("t" if y > 0 else "")
                feather_paste(out, t, (x, y), overlap, sides or "l")
            x += pw - overlap; rx += 1
        y += ph - overlap; ry += 1
    return out


def rrect_mask(w, h, rad, inset=0):
    m = Image.new("L", (w, h), 0)
    ImageDraw.Draw(m).rounded_rectangle([inset, inset, w - 1 - inset, h - 1 - inset], radius=rad, fill=255)
    return m


WOOD_PATCH = PB.crop((70, 1100, 790, 1540))


def recompose_frame(src, tw, th, rail_src_x, gem=None):
    """Рама tw×th БЕЗ растяжки: целиком масштабируем к th (только вниз), концы native-scaled,
    середину добираем ТАЙЛОМ чистого слайса рельсы с фейд-стыками, gem-кластер по центру."""
    sw, sh = src.size
    k = th / sh
    scaled = src.resize((max(1, round(sw * k)), th), Image.LANCZOS)
    ssw = scaled.width
    out = Image.new("RGBA", (tw, th), (0, 0, 0, 0))
    half = min(ssw // 2, tw // 2)
    left = scaled.crop((0, 0, half, th))
    right = scaled.crop((ssw - (tw - half if tw - half < ssw else half), 0, ssw, th))
    out.alpha_composite(left, (0, 0))
    out.alpha_composite(right, (tw - right.width, 0))
    gap0, gap1 = left.width - 8, tw - right.width + 8
    if gap1 > gap0:
        a, b = int(rail_src_x[0] * k), int(rail_src_x[1] * k)
        seg = scaled.crop((a, 0, b, th)); segw = seg.width
        x = gap0
        while x < gap1:
            piece = seg if (x - gap0) // max(segw - 12, 1) % 2 == 0 else ImageOps.mirror(seg)
            pw = min(segw, gap1 - x + 12)
            fp = piece.crop((0, 0, pw, th))
            tmp = Image.new("RGBA", (tw, th), (0, 0, 0, 0)); tmp.paste(fp, (x, 0))
            if x > gap0:
                a_np = np.array(tmp.split()[3], np.float32)
                f_np = np.zeros((th, tw), np.float32); f_np[:, x:x + pw] = 255
                f_np[:, x:x + 12] = np.linspace(0, 255, 12, dtype=np.float32)[None, :]
                tmp.putalpha(Image.fromarray(np.minimum(a_np, f_np).astype(np.uint8)))
            out.alpha_composite(tmp)
            x += segw - 12
    if gem:
        g0, g1 = int(gem[0] * k), int(gem[1] * k)
        for half_name in ("top", "bot"):
            gsrc = scaled.crop((g0, 0, g1, th))
            arr = np.array(gsrc.split()[3])
            if half_name == "top": arr[th // 2:, :] = 0
            else: arr[: th // 2, :] = 0
            gsrc.putalpha(Image.fromarray(arr))
            out.alpha_composite(gsrc, ((tw - gsrc.width) // 2, 0))
    return out


def build_panel(w, h, kind="wood", src="bar"):
    """Панель inner w×h на канве (w+2M)×(h+2M): тень + рама + интерьер + объём (всё запечено)."""
    if src == "bar":
        fr = recompose_frame(FB, w, h, rail_src_x=(618, 798), gem=(431, 611))
        thick = max(10, int(54 * h / 346)); rad = int(90 * h / 346)
    else:
        fr = recompose_frame(FBT, w, h, rail_src_x=(95, 175))
        thick = max(8, int(24 * h / 160)); rad = int(34 * h / 160)
    cw, ch = w + 2 * M, h + 2 * M
    canvas = Image.new("RGBA", (cw, ch), (0, 0, 0, 0))
    # тень (запечённая: на устройстве blur запрещён)
    sh = Image.new("RGBA", (cw, ch), (0, 0, 0, 0))
    ImageDraw.Draw(sh).rounded_rectangle([M - 2, M - 2 + 7, M + w + 2, M + h + 2 + 7], radius=rad + 4, fill=(0, 0, 0, 110))
    canvas.alpha_composite(sh.filter(ImageFilter.GaussianBlur(16)))
    # интерьер
    inner = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    im = rrect_mask(w, h, max(4, rad - thick // 2), inset=thick)
    if kind == "cta":
        g = np.zeros((h, w, 3), np.float32)
        tcol = np.array([20, 58, 28], np.float32); bcol = np.array([34, 92, 42], np.float32)
        for yy in range(h):
            g[yy, :, :] = tcol + (bcol - tcol) * (yy / max(h - 1, 1))
        xxg, yyg = np.mgrid[0:h, 0:w]
        glow = np.exp(-(((yyg - w / 2) ** 2) / (2 * (w * 0.32) ** 2) + ((xxg - h * 0.6) ** 2) / (2 * (h * 0.8) ** 2))) * 34
        g += glow[..., None] * np.array([0.5, 1.0, 0.55])
        base = Image.fromarray(np.clip(g, 0, 255).astype(np.uint8), "RGB")
    elif kind == "ember":
        wood = tile_wood(WOOD_PATCH, w, h)
        arr = np.array(wood, np.float32)
        emb = np.array([18, 52, 24], np.float32)
        xxg, yyg = np.mgrid[0:h, 0:w]
        gl = np.exp(-(((yyg - w / 2) ** 2) / (2 * (w * 0.36) ** 2) + ((xxg - h / 2) ** 2) / (2 * (h * 0.9) ** 2)))
        arr = arr * 0.66 + emb[None, None, :] * (0.55 + 0.45 * gl)[..., None] * 1.35
        base = Image.fromarray(np.clip(arr, 0, 255).astype(np.uint8), "RGB")
    elif kind == "sel":  # выбранный протокол — тёплый ОРАНЖ (видео: «Авто»)
        wood = tile_wood(WOOD_PATCH, w, h)
        arr = np.array(wood, np.float32) * 0.62
        ov = Image.new("RGBA", (w, h), (0, 0, 0, 0))
        ImageDraw.Draw(ov).rounded_rectangle([6, 6, w - 6, h - 6], radius=14, fill=(232, 116, 34, 96))
        ov = ov.filter(ImageFilter.GaussianBlur(7))
        base = Image.fromarray(arr.astype(np.uint8), "RGB")
        base.paste(Image.alpha_composite(base.convert("RGBA"), ov).convert("RGB"), (0, 0))
    else:
        wood = tile_wood(WOOD_PATCH, w, h)
        base = Image.fromarray((np.array(wood, np.float32) * 0.74).astype(np.uint8), "RGB")
    inner.paste(base, (0, 0), im)
    vol = Image.new("RGBA", (w, h), (0, 0, 0, 0)); dv = ImageDraw.Draw(vol)
    dv.rectangle([0, 0, w, int(h * 0.34)], fill=(0, 0, 0, 70))
    lick = (SALAD + (95,)) if kind in ("cta",) else (255, 196, 120, 34)
    dv.rectangle([0, int(h * 0.80), w, h], fill=lick)
    vol = vol.filter(ImageFilter.GaussianBlur(max(6, h // 12)))
    vol.putalpha(Image.composite(vol.split()[3], Image.new("L", (w, h), 0), im))
    inner.alpha_composite(vol)
    canvas.alpha_composite(inner, (M, M))
    canvas.alpha_composite(fr, (M, M))
    if kind == "sel":  # оранжевый контур выбора поверх рамы
        ImageDraw.Draw(canvas).rounded_rectangle(
            [M + 3, M + 3, M + w - 3, M + h - 3], radius=15, outline=(255, 150, 66, 220), width=3)
    return canvas


def build_bg(with_hero, hero_src=None):
    base = tile_wood(WOOD_PATCH, W, H)

    def bright(im, k):
        return Image.fromarray(np.clip(np.array(im, np.float32) * k, 0, 255).astype(np.uint8), "RGB")

    cs = 150
    feather_paste(base, bright(PB.crop((0, 0, cs, cs)), 1.14), (0, 0), 18, "rb")
    feather_paste(base, bright(PB.crop((853 - cs, 0, 853, cs)), 1.14), (W - cs, 0), 18, "lb")
    feather_paste(base, bright(PB.crop((0, 1844 - cs, cs, 1844)), 1.14), (0, H - cs), 18, "rt")
    feather_paste(base, bright(PB.crop((853 - cs, 1844 - cs, 853, 1844)), 1.14), (W - cs, H - cs), 18, "lt")
    feather_paste(base, bright(PB.crop((4, 400, 48, 400 + H - 2 * cs)), 1.16), (4, cs), 14, "r")
    feather_paste(base, bright(PB.crop((853 - 48, 400, 853 - 4, 400 + H - 2 * cs)), 1.16), (W - 48, cs), 14, "l")
    seg_t = bright(PB.crop((170, 5, 750, 41)), 1.30)
    seg_b = bright(PB.crop((170, 1801, 750, 1837)), 1.30)
    x = cs
    while x < W - cs:
        wseg = min(seg_t.width, W - cs - x)
        feather_paste(base, seg_t.crop((0, 0, wseg, 36)), (x, 4), 14, "b" + ("l" if x > cs else ""))
        feather_paste(base, seg_b.crop((0, 0, wseg, 36)), (x, H - 40), 14, "t" + ("l" if x > cs else ""))
        x += seg_t.width - 16

    if with_hero:
        hero = hero_src.crop((54, 52, 800, 1018))
        hero = hero.resize((round(hero.width * 0.86), round(hero.height * 0.86)), Image.LANCZOS)
        feather_paste(base, hero, (104, 26), 30, "ltrb")

    yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
    key = np.exp(-(((xx - 440) ** 2) / (2 * 700 ** 2) + ((yy - 260) ** 2) / (2 * 560 ** 2))) * 0.14
    edge = np.minimum.reduce([xx, W - 1 - xx]) / (W * 0.5)
    edgey = np.minimum.reduce([yy, H - 1 - yy]) / (H * 0.5)
    vig = (1 - np.clip(edge * 3.2, 0, 1)) * 0.16 + (1 - np.clip(edgey * 3.2, 0, 1)) * 0.12
    arr = np.array(base, np.float32) * (1.0 + key - vig)[..., None]
    noise = np.random.default_rng(7).normal(0, 1.7, (H, W, 1)).astype(np.float32)
    return Image.fromarray(np.clip(arr + noise, 0, 255).astype(np.uint8), "RGB")


def build_wood_tile():
    """Бесшовный тайл телефонного дерева (mirror-blend краёв → TileMode.Repeated без швов)."""
    t = PB.crop((250, 1150, 610, 1390))  # 360×240 чистых досок
    w, h = t.size
    arr = np.array(t, np.float32)
    f = 26
    ramp = np.linspace(0, 1, f, dtype=np.float32)[None, :, None]
    arr[:, :f] = arr[:, :f] * ramp + arr[:, -f:][:, ::-1] * (1 - ramp)
    rampv = np.linspace(0, 1, f, dtype=np.float32)[:, None, None]
    arr[:f, :] = arr[:f, :] * rampv + arr[-f:, :][::-1] * (1 - rampv)
    return Image.fromarray(arr.astype(np.uint8), "RGB")


def build_qr_bezel():
    """frame_qr с ПРОЗРАЧНЫМ проёмом ±308 от центра (золото начинается на ±317):
    белая QR-карточка рисуется ПОД рамой (урок qr-decorative-frame-lesson)."""
    q = FQR.copy()
    a = np.array(q.split()[3])
    c = 380; ap = 308
    a[c - ap:c + ap, c - ap:c + ap] = 0
    q.putalpha(Image.fromarray(a))
    return q


def save(im, name):
    im.save(f"{RES}/{name}.webp", lossless=True)
    import os
    print(f"{name}.webp {im.size[0]}×{im.size[1]} {os.path.getsize(f'{RES}/{name}.webp')//1024}KB")


if __name__ == "__main__":
    save(build_bg(True, PB), "tvm_bg_off")
    save(build_bg(True, PBO), "tvm_bg_on")
    save(build_bg(False), "tvm_wood_bg")
    save(build_panel(868, 100, "cta", "bar"), "tvm_cta")
    save(build_panel(100, 100, "wood", "btn"), "tvm_sq")
    save(build_panel(317, 142, "wood", "btn"), "tvm_tile")
    save(build_panel(486, 86, "wood", "bar"), "tvm_bar2")
    save(build_panel(996, 84, "ember", "bar"), "tvm_phone")
    save(build_panel(317, 72, "wood", "btn"), "tvm_msg")
    save(build_panel(236, 80, "wood", "btn"), "tvm_chip")
    save(build_panel(236, 80, "sel", "btn"), "tvm_chip_sel")
    save(build_panel(626, 66, "wood", "bar"), "tvm_account")
    save(build_panel(626, 66, "cta", "bar"), "tvm_trial")
    save(build_wood_tile(), "tvm_wood_tile")
    save(build_qr_bezel(), "tvm_qr_bezel")
    print("done")
