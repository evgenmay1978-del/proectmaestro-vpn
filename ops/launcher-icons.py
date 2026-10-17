#!/usr/bin/env python3
"""launcher-icons.py — иконки приложения MaestroVPN из мастера логотипа (MV-вензель 1280×1280).

Что генерирует (все плотности mdpi…xxxhdpi):
  • ic_launcher_foreground.png — вензель на ПРОЗРАЧНОМ, вписан в safe zone adaptive-иконки
    (круг ⌀66dp из 108dp) с запасом: ни одна маска лаунчера (круг, капля, сквиркл) не режет засечки.
  • ic_launcher_monochrome.png — белый силуэт той же геометрии для «тематических» иконок Android 13+.
  • ic_launcher.png / ic_launcher_round.png — legacy (Android < 8 и лаунчеры без adaptive):
    скруглённый квадрат / круг цвета фона с вензелем внутри, без обрезки.
  • ic_stat_fox.png, ic_qs_spider.png — чёткий белый силуэт для шторки и плитки быстрых настроек
    (система красит их одним цветом по альфе — полупрозрачная серая заливка давала «кашу»).

TV-баннер (tv_banner) не трогается. Требует Pillow, numpy, scipy.
Запуск из корня репозитория:  python3 ops/launcher-icons.py
"""
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw
from scipy import ndimage

ROOT = Path(__file__).resolve().parents[1]
MASTER = ROOT / "docs/design/maestrovpn-mv-logo-reference-2026-07-13.jpg"
RES = ROOT / "app/src/main/res"
BACKGROUND = (0x0A, 0x0A, 0x0A)  # = @color/ic_launcher_background

DENSITIES = {"mdpi": 1, "hdpi": 1.5, "xhdpi": 2, "xxhdpi": 3, "xxxhdpi": 4}

# Доли от стороны холста. Safe zone adaptive-иконки — круг радиуса 33/108 = 0.3056.
FOREGROUND_RADIUS = 0.29    # самая дальняя точка вензеля от центра (запас ~5% до края safe zone 0.3056)
LEGACY_RADIUS = 0.40        # legacy-иконка: вензель внутри круга/скруглённого квадрата
STATUS_RADIUS = 0.47        # силуэт 24dp: почти весь холст (Android сам даёт поля)


def load_master():
    rgb = np.asarray(Image.open(MASTER).convert("RGB")).astype(np.float32)
    lum = rgb.mean(axis=2)
    shape = lum > 22
    shape = ndimage.binary_fill_holes(shape)
    shape = ndimage.binary_closing(shape, iterations=3)
    labels, count = ndimage.label(shape)
    if count > 1:  # убрать мелкий шум JPEG вокруг вензеля
        sizes = ndimage.sum(shape, labels, range(1, count + 1))
        shape = labels == (int(np.argmax(sizes)) + 1)
    alpha = np.clip(ndimage.gaussian_filter(shape.astype(np.float32), 1.2), 0, 1)
    # Сплошной силуэт: внешний контур вензеля сам читается как «M»; вырезанный жёлоб V на 24dp
    # превращался в шум.
    solid = ndimage.binary_fill_holes(ndimage.binary_closing(shape, iterations=10))
    solid = solid | shape
    silhouette = np.clip(ndimage.gaussian_filter(solid.astype(np.float32), 1.2), 0, 1)
    color = Image.fromarray(np.dstack([rgb, alpha * 255]).astype(np.uint8), "RGBA")
    white = np.full(rgb.shape, 255, np.float32)
    mono = Image.fromarray(np.dstack([white, silhouette * 255]).astype(np.uint8), "RGBA")
    return color, mono, shape


def radial_fit(img, shape, canvas, radius_frac):
    """Масштабирует так, чтобы самая дальняя непрозрачная точка (от центра bbox) легла на
    radius_frac·canvas, и ставит по центру прозрачного холста canvas×canvas."""
    ys, xs = np.nonzero(shape)
    x0, x1, y0, y1 = xs.min(), xs.max() + 1, ys.min(), ys.max() + 1
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    reach = float(np.sqrt(((xs - cx) ** 2 + (ys - cy) ** 2).max()))
    scale = radius_frac * canvas / reach
    crop = img.crop((x0, y0, x1, y1))
    w, h = max(1, round(crop.width * scale)), max(1, round(crop.height * scale))
    resized = crop.resize((w, h), Image.LANCZOS)
    out = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    out.alpha_composite(resized, ((canvas - w) // 2, (canvas - h) // 2))
    return out


def legacy(color, shape, size, round_shape):
    ss = size * 4  # суперсэмплинг для гладкой кромки
    plate = Image.new("L", (ss, ss), 0)
    draw = ImageDraw.Draw(plate)
    if round_shape:
        draw.ellipse((0, 0, ss - 1, ss - 1), fill=255)
    else:
        draw.rounded_rectangle((0, 0, ss - 1, ss - 1), radius=int(ss * 0.18), fill=255)
    base = Image.new("RGBA", (ss, ss), BACKGROUND + (255,))
    base.putalpha(plate)
    base.alpha_composite(radial_fit(color, shape, ss, LEGACY_RADIUS))
    return base.resize((size, size), Image.LANCZOS)


def save(img, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    img.save(path, optimize=True)


def main():
    color, mono, shape = load_master()
    count = 0
    for density, factor in DENSITIES.items():
        mip = RES / f"mipmap-{density}"
        fg = round(108 * factor)
        save(radial_fit(color, shape, fg, FOREGROUND_RADIUS), mip / "ic_launcher_foreground.png")
        save(radial_fit(mono, shape, fg, FOREGROUND_RADIUS), mip / "ic_launcher_monochrome.png")
        icon = round(48 * factor)
        save(legacy(color, shape, icon, round_shape=False), mip / "ic_launcher.png")
        save(legacy(color, shape, icon, round_shape=True), mip / "ic_launcher_round.png")
        status = round(24 * factor)
        sil = radial_fit(mono, shape, status, STATUS_RADIUS)
        for name in ("ic_stat_fox", "ic_qs_spider"):
            save(sil, RES / f"drawable-{density}" / f"{name}.png")
        count += 6
    print(f"OK: {count} файлов из {MASTER.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
