#!/usr/bin/env python3
"""Сравнение вариантов посадки живого глаза в бронзовое кольцо — смотреть ГЛАЗАМИ до правки Kotlin.

ЗАЧЕМ ОТДЕЛЬНЫЙ СКРИПТ, а не ops/phone-screen-sim.py:
  ⛔ УСТАРЕЛО: `mobile_home_scene.webp` удалён (c15b4e3), phone-screen-sim.py собирает Home из
  4D-атласа. Ниже речь о прежнем запечённом глазе — оставлено как история. Живой слой с его
  реальным рантайм-масштабом он не рисует вовсе. Я на этом обжёгся 31.07.2026: показал владельцу
  симуляцию как доказательство размера глаза, хотя она про размер глаза не говорит НИЧЕГО.
  Здесь слой строится по тем же числам, что и Kotlin (LivingEyeLayerGeometry.kt).

Варианты:
  A — прежний маппинг (как было до PR #72): глаз крупный, но зелень вылезает на бронзу. Это был баг.
  B — как сейчас в проде: весь слой ужат в бронзовый отступ → глаз меньше на 30.8% по площади.
  C — предложение владельца (31.07.2026): прежний масштаб + КРУГОВОЙ КЛИП по бронзе. Глаз прежнего
      размера, лишняя зелень уходит ПОД кольцо, а не тащит за собой зрачок и радужку.

Usage: python3 ops/eye-fit-preview.py   → build/eye-fit-preview/eye-fit.png
"""
import pathlib
from PIL import Image, ImageDraw, ImageFont

ROOT = pathlib.Path(__file__).resolve().parent.parent
RES = ROOT / "app/src/main/res/drawable-nodpi"
OUT = ROOT / "build/eye-fit-preview"

# ── числа один-в-один из LivingEyeLayerGeometry.kt (сверять при правке Kotlin!) ──
STATE_X, STATE_Y = 230.0, 745.0
STATE_W, STATE_H = 890.0, 635.0
ORIGIN_X, ORIGIN_Y = 268.8, 637.3
VIRTUAL_SIZE = 822.5
BRONZE_INSET_FRACTION = 26.0 / 520.0

CANVAS = 520          # медальон в дизайн-единицах
VIEW = 900            # во столько раз крупнее рисуем предпросмотр
K = VIEW / CANVAS


def layer_rect(medallion: float, fit_into_bronze: bool):
    """Прямоугольник слоя на канве медальона. fit_into_bronze=True воспроизводит текущий прод."""
    virtual = medallion / VIRTUAL_SIZE
    left = (STATE_X - ORIGIN_X) * virtual
    top = (STATE_Y - ORIGIN_Y) * virtual
    right = (STATE_X + STATE_W - ORIGIN_X) * virtual
    bottom = (STATE_Y + STATE_H - ORIGIN_Y) * virtual
    if not fit_into_bronze:
        return left, top, right, bottom, virtual
    inset = medallion * BRONZE_INSET_FRACTION
    scale = (medallion - inset * 2) / (right - left)
    cx, cy = medallion / 2, medallion / 2
    w, h = (right - left) * scale, (bottom - top) * scale
    return cx - w / 2, cy - h / 2, cx + w / 2, cy + h / 2, virtual * scale


def render(fit_into_bronze: bool, clip_to_bronze: bool, title: str, note: str):
    eye = Image.open(RES / "mobile_eye_open.webp").convert("RGBA")
    tile = Image.new("RGBA", (VIEW, VIEW), (18, 14, 10, 255))

    l, t, r, b, scale = layer_rect(CANVAS, fit_into_bronze)
    lay = eye.resize((max(1, round((r - l) * K)), max(1, round((b - t) * K))), Image.LANCZOS)
    layer = Image.new("RGBA", (VIEW, VIEW), (0, 0, 0, 0))
    layer.alpha_composite(lay, (round(l * K), round(t * K)))

    inset = VIEW * BRONZE_INSET_FRACTION
    if clip_to_bronze:
        # Ровно то, что просил владелец: лишнее прячем ПОД кольцо, а не ужимаем глаз.
        mask = Image.new("L", (VIEW, VIEW), 0)
        ImageDraw.Draw(mask).ellipse([inset, inset, VIEW - inset, VIEW - inset], fill=255)
        layer.putalpha(Image.composite(layer.getchannel("A"), Image.new("L", (VIEW, VIEW), 0), mask))
    tile.alpha_composite(layer)

    d = ImageDraw.Draw(tile)
    d.ellipse([inset, inset, VIEW - inset, VIEW - inset], outline=(205, 140, 60, 255), width=6)
    d.ellipse([0, 0, VIEW - 1, VIEW - 1], outline=(120, 80, 35, 200), width=3)
    return tile, f"{title}   scale={scale:.4f}", note


def font(px):
    for p in ("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
              "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"):
        if pathlib.Path(p).exists():
            return ImageFont.truetype(p, px)
    return ImageFont.load_default()


def main():
    variants = [
        render(False, False, "A — как было до PR #72",
               "глаз крупный, но зелень лезет на бронзу (это и был баг)"),
        render(True, False, "B — как СЕЙЧАС в проде",
               "вписан весь слой целиком → глаз меньше на 30.8% площади"),
        render(False, True, "C — предложение владельца",
               "прежний размер + клип по бронзе: лишнее уходит ПОД кольцо"),
    ]
    pad, head, foot = 40, 110, 90
    W = pad + (VIEW + pad) * len(variants)
    H = head + VIEW + foot
    sheet = Image.new("RGB", (W, H), (12, 10, 8))
    d = ImageDraw.Draw(sheet)
    d.text((pad, 34), "Посадка живого глаза в бронзовое кольцо — по числам из LivingEyeLayerGeometry.kt",
           font=font(30), fill=(232, 205, 150))
    for i, (tile, title, note) in enumerate(variants):
        x = pad + (VIEW + pad) * i
        sheet.paste(tile.convert("RGB"), (x, head))
        d.text((x, head - 34), title, font=font(26), fill=(240, 220, 170))
        d.text((x, head + VIEW + 16), note, font=font(21), fill=(190, 175, 150))
    d.text((pad, H - 38), "Бронзовая линия = граница кольца (inset 26/520). Арт mobile_eye_open.webp — подлинный.",
           font=font(20), fill=(150, 140, 125))
    OUT.mkdir(parents=True, exist_ok=True)
    p = OUT / "eye-fit.png"
    sheet.save(p)
    print(f"OK {p} ({sheet.width}x{sheet.height})")


if __name__ == "__main__":
    main()
