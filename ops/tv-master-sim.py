#!/usr/bin/env python3
"""Deterministic 1920x1080 visual smoke test for the TV master screen."""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
RES = ROOT / "app/src/main/res/drawable-nodpi"
OUT = ROOT / "build/tv-master-sim"
M = 28


def font(size, bold=False):
    name = "DejaVuSans-Bold.ttf" if bold else "DejaVuSans.ttf"
    return ImageFont.truetype(f"/usr/share/fonts/truetype/dejavu/{name}", size)


def center(draw, box, text, fill, size, bold=True):
    f = font(size, bold)
    x0, y0, x1, y1 = box
    b = draw.textbbox((0, 0), text, font=f)
    draw.text(((x0 + x1 - b[2]) / 2, (y0 + y1 - b[3]) / 2 - 2), text, font=f, fill=fill)


def panel(im, name, x, y, w, h):
    asset = Image.open(RES / f"{name}.webp").convert("RGBA")
    assert asset.size == (w + 2 * M, h + 2 * M), (name, asset.size)
    im.alpha_composite(asset, (x - M, y - M))


def render(connected):
    bg = "tvm_bg_on.webp" if connected else "tvm_bg_off.webp"
    im = Image.open(RES / bg).convert("RGBA")
    d = ImageDraw.Draw(im)
    panels = [
        ("tvm_cta", 856, 76, 868, 100), ("tvm_sq", 1752, 76, 100, 100),
        *[("tvm_tile", 856 + i * 341, 208, 317, 142) for i in range(3)],
        ("tvm_bar2", 856, 374, 486, 86), ("tvm_bar2", 1366, 374, 486, 86),
        ("tvm_phone", 856, 532, 996, 84),
        *[("tvm_msg", 856 + i * 341, 672, 317, 72) for i in range(3)],
        *[("tvm_chip_sel" if i == 0 else "tvm_chip", 856 + (i % 4) * 254, 818 + (i // 4) * 96, 236, 80) for i in range(8)],
        ("tvm_account", 112, 734, 626, 130),
    ]
    for args in panels:
        panel(im, *args)
    center(d, (856, 76, 1724, 176), "Купить подписку", "#cde37b", 34)
    for i, text in enumerate(("Ввести код", "Приложения VPN", "Поделиться")):
        center(d, (856 + i * 341, 208, 1173 + i * 341, 350), text, "#ededE9", 27)
    center(d, (856, 374, 1342, 460), "Обновить приложение", "#ededE9", 25)
    center(d, (1366, 374, 1852, 460), "Проверить соединение", "#ededE9", 25)
    d.text((862, 474), "КОНТАКТЫ", font=font(34, True), fill="#e1bb73")
    center(d, (856, 532, 1852, 616), "8 977 811-65-64", "#ededE9", 40)
    for i, text in enumerate(("Telegram", "WhatsApp", "МАКС")):
        center(d, (856 + i * 341, 672, 1173 + i * 341, 744), text, "#ededE9", 27)
    d.text((862, 758), "ПРОТОКОЛ", font=font(34, True), fill="#e1bb73")
    for i, text in enumerate(("Авто", "Hysteria2", "VLESS", "Naive", "AnyTLS", "OpenVPN", "WireGuard", "OLC RTC")):
        x, y = 856 + (i % 4) * 254, 818 + (i // 4) * 96
        center(d, (x, y, x + 236, y + 80), text, "#ff953e" if i == 0 else "#ededE9", 21)
    d.text((218, 746), "Аккаунт", font=font(18, True), fill="#b7b9b5")
    d.text((218, 770), "wapmix", font=font(31, True), fill="#ededE9")
    d.text((218, 814), "Осталось 27 дней", font=font(23, True), fill="#3ee07a")
    state = "ПОДКЛЮЧЕНО" if connected else "ОТКЛЮЧЕНО"
    color = "#3ee07a" if connected else "#ff4040"
    center(d, (122, 892, 659, 948), state, color, 40)
    center(d, (122, 946, 659, 986), "Подключён: Авто" if connected else "Отключён: Авто", "#f08a35", 25)
    OUT.mkdir(parents=True, exist_ok=True)
    path = OUT / ("on.png" if connected else "off.png")
    im.convert("RGB").save(path, quality=95)
    print(path)


if __name__ == "__main__":
    render(False)
    render(True)
