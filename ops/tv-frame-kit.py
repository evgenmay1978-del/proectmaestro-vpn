#!/usr/bin/env python3
"""
tv-frame-kit — премиум-ассеты ТВ из эталонов owner'а (жалоба 2026-07-11:
9-patch рамки 270..1042px растягивались до 6× на панель → пиксельная каша).

Принцип: НИКОГДА не растягивать растр.
  • carved_wood_tile.webp — бесшовная плитка коры из эталона /root/button.png
    (native 1:1; в Compose — ShaderBrush(ImageShader, TileMode.Repeated));
  • tv_wood_bg.webp — честный 1920×1080 lossless фон из native-досок oak_bg
    + виньетка + зерно (зерно разбивает banding 8-bit ТВ-панелей).
Бронзовые рамки НЕ ассет — рисуются в Compose градиент-штрихами (CarvedKit.kt),
разрешение-независимы. Цвета сняты пипеткой с эскиза vpnoff.png.

Запуск: /root/.venvs/imgtools/bin/python3 ops/tv-frame-kit.py
Пишет в app/src/main/res/drawable-nodpi/ (git покажет диф).
"""
import argparse
import os
import numpy as np
from PIL import Image

SRC_BUTTON = '/root/button.png'      # эталон-кнопка owner 1891×832 (2026-07-05)
REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_OUT = os.path.join(REPO, 'app/src/main/res/drawable-nodpi')
OAK = os.path.join(DEFAULT_OUT, 'oak_bg.webp')   # родные тёмные доски (телефонный фон)


def mirror_blend(arr: np.ndarray, f: int, axis: int) -> np.ndarray:
    """Бесшовность края: зеркальное сведение первых/последних f строк/столбцов."""
    a = arr.astype(np.float32)
    if axis == 1:
        r = np.linspace(0, 1, f)[None, :, None]
        a[:, :f] = a[:, :f] * r + a[:, -f:][:, ::-1] * (1 - r)
    else:
        r = np.linspace(0, 1, f)[:, None, None]
        a[:f, :] = a[:f, :] * r + a[-f:, :][::-1, :] * (1 - r)
    return a


def cut_bark_tile() -> Image.Image:
    """Чистая кора интерьера эталона (без текста «Закрыть», рамок, завитков и листьев):
    x 360..660, y 262..458 по сетке button_grid — native 300×196."""
    src = Image.open(SRC_BUTTON).convert('RGB')
    t = np.asarray(src.crop((360, 262, 660, 458)))
    t = mirror_blend(t, 36, axis=1)
    t = mirror_blend(t, 26, axis=0)
    return Image.fromarray(np.clip(t, 0, 255).astype(np.uint8))


def build_wood_bg(w: int = 1920, h: int = 1080) -> Image.Image:
    """1080p-фон: native-доски (oak_bg, чистая зона без нижнего орнамента).
    Вертикаль кроется ЗЕРКАЛОМ (720 + отражение) — шов математически непрерывен;
    горизонталь — mirror-blend краёв + шахматный флип столбцов."""
    oak = Image.open(OAK).convert('RGB')
    tile = np.asarray(oak.crop((0, 20, 560, 740)))       # 560×720 чистых досок
    tile = mirror_blend(tile, 40, axis=1)                # швы X (границы досок — почти невидимы)
    tile = np.clip(tile, 0, 255).astype(np.uint8)
    th, tw = tile.shape[:2]
    nx = w // tw + 2
    strips = []
    for i in range(nx):
        col = tile[:, ::-1] if i % 2 else tile           # шахматный флип прячет периодичность
        full = np.concatenate([col, col[::-1, :]], axis=0)  # 1440: низ = отражение (бесшовно)
        strips.append(full)
    a = np.concatenate(strips, axis=1)[:h, :w].astype(np.float32)
    yy, xx = np.mgrid[0:h, 0:w]
    d = np.sqrt(((xx - w / 2) / (w * 0.75)) ** 2 + ((yy - h * 0.42) / (h * 0.8)) ** 2)
    a *= np.clip(1.04 - 0.42 * d ** 2, 0.5, 1.04)[..., None]
    a *= 0.82                                            # общее притемнение под контент
    a += np.random.default_rng(3).normal(0, 2.2, a.shape)
    return Image.fromarray(np.clip(a, 0, 255).astype(np.uint8))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--out', default=DEFAULT_OUT)
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    cut_bark_tile().save(os.path.join(args.out, 'carved_wood_tile.webp'), lossless=True, method=6)
    build_wood_bg().save(os.path.join(args.out, 'tv_wood_bg.webp'), lossless=True, method=6)

    for f in ['carved_wood_tile.webp', 'tv_wood_bg.webp']:
        p = os.path.join(args.out, f)
        print(f, Image.open(p).size, os.path.getsize(p) // 1024, 'KB')


if __name__ == '__main__':
    main()
