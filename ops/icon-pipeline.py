#!/usr/bin/env python3
"""icon-pipeline.py <source_icon.png> — нарезает бренд-иконку MaestroVPN во ВСЕ ресурсы:
  • launcher (ic_launcher / ic_launcher_round / ic_launcher_foreground) во всех mipmap-плотностях
  • Android-TV баннер (drawable-xhdpi/tv_banner.png, 640×360)
  • системные силуэты (ic_stat_fox трей + ic_qs_spider шторка) — БЕЛЫЙ силуэт на прозрачном
    (status-bar/QS-tile Android красит одним цветом → нужен именно силуэт, не цветной арт).
Источник = квадратная бренд-иконка (VM-вензель на почти-чёрном). Идемпотентен, бэкапит заменяемое.
Запуск: /root/.venvs/imgtools/bin/python3 ops/icon-pipeline.py /root/vm_icon.png
"""
import sys, os, shutil, time
from PIL import Image, ImageDraw, ImageFilter
import numpy as np

if len(sys.argv) < 2 or not os.path.isfile(sys.argv[1]):
    sys.exit("usage: icon-pipeline.py <source_icon.png>  (нужен реальный файл иконки)")
SRC = sys.argv[1]
RES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "app", "src", "main", "res")
RES = os.path.normpath(RES)
BAK = "/root/.claude/maestro-asset-backups"
os.makedirs(BAK, exist_ok=True)
STAMP = os.environ.get("ICON_STAMP", "icon")  # передаём снаружи (Date.now недоступен внутри)

LAUNCH = {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}
FG = {"mdpi": 108, "hdpi": 162, "xhdpi": 216, "xxhdpi": 324, "xxxhdpi": 432}
SIL = {"mdpi": 24, "hdpi": 36, "xhdpi": 48, "xxhdpi": 72, "xxxhdpi": 96}

src = Image.open(SRC).convert("RGBA")
S = min(src.size)
# квадратим по центру
src = src.crop(((src.width - S)//2, (src.height - S)//2, (src.width + S)//2, (src.height + S)//2))

def bak(path):
    if os.path.isfile(path):
        shutil.copy2(path, os.path.join(BAK, f"{os.path.basename(path)}.bak-{STAMP}"))

def save(img, path):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    bak(path); img.save(path)

def round_mask(size, radius_frac=0.5):
    m = Image.new("L", (size, size), 0)
    d = ImageDraw.Draw(m)
    d.ellipse((0, 0, size-1, size-1), fill=255)
    return m

# ── VM-силуэт: маска = яркие (не-чёрные) пиксели вензеля ──
def vm_silhouette():
    a = np.asarray(src.convert("RGB")).astype(float)
    lum = a.mean(2)
    mask = np.clip((lum - 30) / 40, 0, 1)  # >30 яркость → вензель; мягкий край
    # обрезаем внешнюю чёрную рамку (берём bbox содержимого)
    ys, xs = np.where(mask > 0.3)
    if len(xs):
        pad = int(S*0.02)
        x0, x1 = max(0, xs.min()-pad), min(S, xs.max()+pad)
        y0, y1 = max(0, ys.min()-pad), min(S, ys.max()+pad)
    else:
        x0, y0, x1, y1 = 0, 0, S, S
    sil = np.zeros((S, S, 4), np.uint8)
    sil[..., :3] = 255
    sil[..., 3] = (mask*255).astype(np.uint8)
    return Image.fromarray(sil).crop((x0, y0, x1, y1)), (x0, y0, x1, y1)

sil_img, bbox = vm_silhouette()
content = src.crop(bbox)  # цветной вензель c чёрной рамкой (для TV-баннера на чёрном — там ок)

# Цветной вензель на ПРОЗРАЧНОМ (для adaptive foreground): заполненная маска формы, чтобы
# фон adaptive-иконки лёг бесшовно (без паразитного тёмного квадрата от чёрной рамки арта).
def content_on_transparent():
    a = np.asarray(src.convert("RGB")).astype(float)
    lum = a.mean(2)
    binm = lum > 22
    try:
        from scipy import ndimage
        binm = ndimage.binary_fill_holes(binm)                 # залить внутренние фасет-«дыры»
        binm = ndimage.binary_closing(binm, iterations=3)      # сомкнуть тонкие разрывы
        alpha = ndimage.gaussian_filter(binm.astype(float), 1.5)
    except Exception:
        from PIL import ImageFilter as _IF
        mimg = Image.fromarray((binm * 255).astype(np.uint8)).filter(_IF.MaxFilter(9)).filter(_IF.GaussianBlur(2))
        alpha = np.asarray(mimg).astype(float) / 255
    rgba = np.dstack([a, np.clip(alpha, 0, 1) * 255]).astype(np.uint8)
    img = Image.fromarray(rgba, "RGBA")
    ys, xs = np.where(np.clip(alpha, 0, 1) > 0.3)
    pad = int(S * 0.02)
    return img.crop((max(0, xs.min()-pad), max(0, ys.min()-pad),
                     min(S, xs.max()+pad), min(S, ys.max()+pad)))

content_t = content_on_transparent()

def fit_center(img, canvas, frac):
    """вписать img в квадрат canvas на прозрачном, масштаб = frac от canvas, по центру."""
    target = int(canvas * frac)
    w, h = img.size
    sc = target / max(w, h)
    r = img.resize((max(1, int(w*sc)), max(1, int(h*sc))), Image.LANCZOS)
    out = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    out.paste(r, ((canvas - r.width)//2, (canvas - r.height)//2), r)
    return out

n = 0
# ── 1) LAUNCHER (полноцветный арт) + round ──
for d, sz in LAUNCH.items():
    full = src.resize((sz, sz), Image.LANCZOS)
    save(full, f"{RES}/mipmap-{d}/ic_launcher.png"); n += 1
    rnd = full.copy(); rnd.putalpha(round_mask(sz))
    save(rnd, f"{RES}/mipmap-{d}/ic_launcher_round.png"); n += 1
# ── 2) ADAPTIVE FOREGROUND (вензель на ПРОЗРАЧНОМ в safe-zone; фон = #0A0A0A из XML) ──
for d, sz in FG.items():
    save(fit_center(content_t, sz, 0.66), f"{RES}/mipmap-{d}/ic_launcher_foreground.png"); n += 1
# ── 3) TV BANNER (Android TV + Google TV): иконка владельца КАК ЕСТЬ — по центру, на её же
#     чёрном фоне, крупно на всю высоту (без обрезки и без выдуманных коллажей). 3 плотности. ──
BANNERS = {"hdpi": (480, 270), "xhdpi": (640, 360), "xxhdpi": (960, 540)}
for d, (bw, bh) in BANNERS.items():
    ban = Image.new("RGBA", (bw, bh), (0, 0, 0, 255))
    vm = src.resize((bh, bh), Image.LANCZOS)   # его квадратная иконка, высота = высота баннера
    ban.paste(vm, ((bw - bh)//2, 0), vm)
    save(ban, f"{RES}/drawable-{d}/tv_banner.png"); n += 1
# ── 4) СИЛУЭТЫ трея + шторки (белый VM на прозрачном, вписан 92%) ──
for name in ("ic_stat_fox", "ic_qs_spider"):
    for d, sz in SIL.items():
        save(fit_center(sil_img, sz, 0.92), f"{RES}/drawable-{d}/{name}.png"); n += 1

print(f"OK: сгенерировано {n} ресурсов из {SRC}")
print(f"    launcher×{len(LAUNCH)} +round +fg  · tv_banner 640×360 · силуэты ic_stat_fox+ic_qs_spider×{len(SIL)}")
print(f"    бэкапы: {BAK}/*.bak-{STAMP}")
