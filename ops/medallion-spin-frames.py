#!/usr/bin/env python3
"""Кадры вращения буквы «M» медальона и её статичный кадр (16.11.2026).

Пишет res/drawable-nodpi/mobile_medallion_m_idle.webp и mobile_medallion_m_spin_000..071.webp
(768×544, окно кадра MonogramSpin.kt не меняется) из исходника
design/medallion/m_idle_source-2026-10-05.webp — буквы, вырезанной из диска 05.10.2026.

Что исправлено против кадров 05.10 (owner 16.11.2026: «на букве косяки»):
  * толщина буквы — сплошная грань из плотных слоёв (≤0,5 px между слоями), а не 6–8 копий
    со ступеньками и «двойным контуром»; на ребре (90°/270°) — цельная пластина, не две полоски;
  * кадр 0° и кадр 180° совпадают со статикой пиксель в пиксель — старт и докрутка без скачка
    света и резкости (раньше кадры вращения были другим рендером: meanDiff 13,5);
  * силуэт симметричен: правая нижняя засечка была срезана по x=728 (там буква срасталась
    с орнаментом рамы) — дорисована зеркалом левой; мусорные точки над буквой убраны;
  * свет физичный: грани освещены по нормали контура, лицевая сторона темнеет на повороте,
    по металлу проходит один блик за пол-оборота; мягкая тень на коже диска.

    python3 -I ops/medallion-spin-frames.py [--preview DIR] [--frames 0,6,12]

Детерминированно: одинаковый исходник → одинаковые кадры. Рендер ×2 со сглаживанием вниз.
"""
import math
import os
import sys
from multiprocessing import Pool

import numpy as np
from PIL import Image, ImageFilter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RES = os.path.join(ROOT, "app/src/main/res/drawable-nodpi")
SOURCE = os.path.join(ROOT, "design/medallion/m_idle_source-2026-10-05.webp")

W, H = 768, 544            # окно кадра в системе ассета 1024: (128, 280) .. (896, 824)
AXIS_X = 384.0             # ось вращения = центр диска по x
EYE_Y = 232.0              # центр диска по y внутри окна (512 - 280)
SS = 2                     # суперсэмплинг
FRAMES = 72                # шаг 5°, полный оборот — 3,6 с (MonogramSpin.kt)
THICK = 34.0               # толщина буквы, px окна
FOCAL = 3.0 * W            # мягкая перспектива
SLICES = 2 * int(THICK * SS)  # ≤0,5 px между слоями в рендере ×2
LIGHT = np.array([-0.45, -0.55, 0.70]); LIGHT /= np.linalg.norm(LIGHT)
VIEW = np.array([0.0, 0.0, 1.0])
HALF = (LIGHT + VIEW) / np.linalg.norm(LIGHT + VIEW)
# нижние внешние углы ножек, где к букве был припаян орнамент рамы
CORNER_TOP = 380
CORNER_SIDE = 170
FEATHER = 16.0
EDGE_COS = 0.35            # ближе к ребру — префильтр сжатия (edge_prefilter)
# внешний край левой ножки внизу (x, y) в окне после сдвига оси: ниже 466 к ней прилип обломок
LEFT_EDGE = [(70.0, 466.0), (64.0, 478.0), (57.0, 492.0), (51.0, 504.0), (46.0, 514.0), (43.0, 522.0)]


def clean_and_mirror(src: Image.Image) -> np.ndarray:
    """RGBA float 0..1: без мусорных точек, ось симметрии ровно на AXIS_X, силуэт зеркальный."""
    a = np.asarray(src.convert("RGBA"), float) / 255.0
    alpha = a[..., 3]
    # 1) Мусор: связные куски альфы, не связанные с буквой (заливка от самого большого куска).
    occupied = alpha > 0.02
    keep = np.zeros_like(occupied)
    seeds = np.argwhere(alpha > 0.9)
    if len(seeds):
        sy, sx = seeds[len(seeds) // 2]
        stack = [(sy, sx)]
        keep[sy, sx] = True
        while stack:
            y, x = stack.pop()
            for dy, dx in ((1, 0), (-1, 0), (0, 1), (0, -1), (1, 1), (1, -1), (-1, 1), (-1, -1)):
                ny, nx = y + dy, x + dx
                if 0 <= ny < H and 0 <= nx < W and occupied[ny, nx] and not keep[ny, nx]:
                    keep[ny, nx] = True
                    stack.append((ny, nx))
    a[..., 3] = np.where(keep, alpha, 0.0)
    # 2) Ось симметрии силуэта (лучшее совпадение с зеркалом) → сдвиг на AXIS_X.
    m = a[..., 3] > 0.5
    best = (0.0, AXIS_X)
    for c2 in np.arange(2 * AXIS_X - 12, 2 * AXIS_X + 12.5, 0.5):
        mx = np.round(c2 - np.arange(W)).astype(int)
        ok = (mx >= 0) & (mx < W)
        mm = np.zeros_like(m)
        mm[:, ok] = m[:, mx[ok]]
        iou = (m & mm).sum() / max(1, (m | mm).sum())
        if iou > best[0]:
            best = (iou, c2 / 2)
    shift = AXIS_X - best[1]
    img = Image.fromarray((a * 255).round().astype(np.uint8), "RGBA")
    img = img.transform((W, H), Image.AFFINE, (1, 0, -shift, 0, 1, 0), resample=Image.BICUBIC)
    a = np.asarray(img, float) / 255.0
    # 3) Обломки рамы у нижних внешних углов ножек (owner 16.11.2026: «артефакты снизу буквы
    #    с обеих сторон в виде шипов»). Буква была припаяна к орнаменту рамы: слева снаружи
    #    золотой полосы ножки торчал тёмный ступенчатый клин с коричневой «шишкой», справа —
    #    диагональная планка рамы и такой же клин, срезанный краем окна (x=728).
    #    a) левый угол режем по внешнему краю золотой полосы (ломаная LEFT_EDGE, снята с исходника
    #       при увеличении ×6; кромка 1 px остаётся);
    xs = np.arange(W, dtype=float)[None, :]
    ys = np.arange(H, dtype=float)
    ey = np.array([p[1] for p in LEFT_EDGE], float)
    ex = np.array([p[0] for p in LEFT_EDGE], float)
    edge = np.interp(ys, ey, ex, left=-1e9, right=ex[-1])[:, None]
    below = (ys >= ey[0])[:, None]
    cut = np.clip(xs - edge + 0.5, 0, 1)
    a[..., 3] = np.where(below & (xs < W / 2), a[..., 3] * cut, a[..., 3])
    #    b) правый угол целиком — зеркало чистого левого, с мягким швом.
    def mirror(img):
        # x → 768 − x (ось 384): a[:, ::-1] даёт 767 − x — добираем 1 px
        return np.asarray(Image.fromarray((img[:, ::-1, :] * 255).round().astype(np.uint8), "RGBA").transform(
            (W, H), Image.AFFINE, (1, 0, -1.0, 0, 1, 0), resample=Image.BICUBIC), float) / 255.0
    fx = np.clip((xs - (W - CORNER_SIDE)) / FEATHER, 0, 1)
    fy = np.clip((ys[:, None] - CORNER_TOP) / FEATHER, 0, 1)
    take = (fx * fy)[..., None]
    a = a * (1 - take) + mirror(a) * take
    # 4) Симметрия силуэта для кадра 180°: зеркало лишь дополняет там, где своя альфа слабее;
    #    текстура остаётся своей. Обе стороны к этому моменту уже чистые.
    mirrored = mirror(a)
    own = (a[..., 3] >= mirrored[..., 3])[..., None]
    out = np.where(own, a, mirrored)
    return out, best[0], shift


def edge_prefilter(img, cos_theta):
    """Перед сильным сжатием по x (поворот у ребра) свернуть ряд так, как его видит взгляд вдоль x.

    PERSPECTIVE в PIL при сжатии в 1/|cos| раз берёт один столбец исходника на пиксель: у ребра
    это центр буквы, где на средней высоте пусто (вырез «V»), и пластина рвалась пополам.
    Покрытие — скользящий максимум альфы по окну 1/|cos|, цвет — среднее в премультиплицированной
    альфе по тому же окну.
    """
    c = abs(cos_theta)
    if c >= EDGE_COS:
        return img
    w = int(min(img.width, math.ceil(1.0 / max(c, 1e-3))))
    if w <= 2:
        return img
    a = np.asarray(img.convert("RGBa"), float)
    alpha = a[..., 3]
    # скользящий максимум по окну w (удвоением), затем центрируем
    mx = alpha.copy()
    k = 1
    while k < w:
        step = min(k, w - k)
        mx[:, :-step] = np.maximum(mx[:, :-step], mx[:, step:])
        k += step
    shift = (w - 1) // 2
    cov = np.zeros_like(mx)
    cov[:, shift:] = mx[:, :mx.shape[1] - shift]
    # среднее премультиплицированного цвета по тому же окну
    cs = np.cumsum(np.pad(a, ((0, 0), (1, 0), (0, 0))), axis=1)
    lo = np.clip(np.arange(a.shape[1]) - shift, 0, a.shape[1])
    hi = np.clip(np.arange(a.shape[1]) - shift + w, 0, a.shape[1])
    mean = (cs[:, hi, :] - cs[:, lo, :]) / np.maximum(hi - lo, 1)[None, :, None]
    # p / α = c / 255 для премультиплицированных значений 0..255 → цвет в долях 0..1
    rgb = np.where(mean[..., 3:4] > 1e-3, mean[..., :3] / np.maximum(mean[..., 3:4], 1e-3), 0)
    out = np.concatenate([rgb * cov[..., None], cov[..., None]], axis=2)
    return Image.fromarray(np.clip(out, 0, 255).round().astype(np.uint8), "RGBa")


def coeffs(dst, src):
    """Коэффициенты PIL PERSPECTIVE: выход (dst) → вход (src)."""
    m = []
    for (x, y), (u, v) in zip(dst, src):
        m.append([x, y, 1, 0, 0, 0, -u * x, -u * y])
        m.append([0, 0, 0, x, y, 1, -v * x, -v * y])
    b = np.array(src, float).reshape(8)
    return np.linalg.solve(np.array(m, float), b)


def project(x, y, z, theta):
    """Точка буквы (px окна) на экран: поворот вокруг вертикальной оси + перспектива."""
    c, s = math.cos(theta), math.sin(theta)
    xr = x - AXIS_X
    X = xr * c + z * s
    Z = -xr * s + z * c
    k = FOCAL / (FOCAL - Z) * (FOCAL - THICK / 2) / FOCAL
    return AXIS_X + X * k, EYE_Y + (y - EYE_Y) * k


def plane_transform(img_ss, z, theta, mirror=False):
    """Плоскость буквы на глубине z → экран (×SS). mirror: текстура задней грани читается прямо."""
    w, h = W * SS, H * SS
    corners = [(0, 0), (W, 0), (W, H), (0, H)]
    src = [(cx * SS, cy * SS) for cx, cy in corners]
    if mirror:
        src = [(w - u, v) for u, v in src]
    dst = [tuple(c * SS for c in project(cx, cy, z, theta)) for cx, cy in corners]
    # премультиплицированная альфа: без тёмной каймы от прозрачных пикселей по краю буквы
    return img_ss.transform((w, h), Image.PERSPECTIVE, coeffs(dst, src), resample=Image.BICUBIC).convert("RGBA")


def wall_layer(alpha_ss, normals, theta, rim=1.0):
    """Цвет грани толщины для этого угла: тёмное золото по нормали контура, блик по Блинну."""
    nx, ny = normals
    c, s = math.cos(theta), math.sin(theta)
    # нормаль грани (nx, ny, 0) в камере после поворота; видна та сторона, что смотрит на нас
    cx_, cy_, cz_ = nx * c, ny, -nx * s
    flip = np.where(cz_ < 0, -1.0, 1.0)
    cx_, cy_, cz_ = cx_ * flip, cy_ * flip, cz_ * flip
    diff = np.clip(cx_ * LIGHT[0] + cy_ * LIGHT[1] + cz_ * LIGHT[2], 0, 1)
    spec = np.clip(cx_ * HALF[0] + cy_ * HALF[1] + cz_ * HALF[2], 0, 1) ** 28
    hgt, wid = alpha_ss.shape
    t = (np.arange(hgt) / hgt)[:, None]
    base_hi = np.array([0.70, 0.48, 0.19])
    base_lo = np.array([0.30, 0.18, 0.06])
    base = base_hi * (1 - t[..., None]) + base_lo * t[..., None]
    rgb = base * (0.22 + 0.85 * diff[..., None]) + np.array([1.0, 0.90, 0.66]) * (0.55 * spec[..., None])
    rgb = rgb * rim
    rgb = np.clip(rgb, 0, 1)
    rgba = np.concatenate([rgb, alpha_ss[..., None]], axis=2)
    return Image.fromarray((rgba * 255).round().astype(np.uint8), "RGBA").convert("RGBa")


def face_light(theta, back):
    n = np.array([math.sin(theta), 0.0, math.cos(theta)])
    if back:
        n = -n
    ref = 0.35 + 0.65 * max(0.0, float(VIEW @ LIGHT))
    return float(np.clip((0.35 + 0.65 * max(0.0, float(n @ LIGHT))) / ref, 0.45, 1.10))


def glint(theta, back):
    """Один блик за пол-оборота: полоса света проходит по металлу слева направо (0 на анфасе)."""
    deg = math.degrees(theta) % 360
    local = (deg - 180) if back else (deg if deg < 180 else deg - 360)
    # local ∈ (-90, 90): 0 — анфас; блик идёт при уходе от анфаса и не трогает 0°/180°
    if not 6 <= abs(local) <= 80:
        return None
    u = (abs(local) - 6) / 74.0            # 0..1 по ходу поворота
    pos = -0.25 + 1.5 * u if local > 0 else 1.25 - 1.5 * u
    strength = math.sin(math.pi * u) * 0.55
    return pos, strength


_SHARED = {}


def _init(tex_ss_bytes, normals, alpha_ss):
    _SHARED["tex"] = Image.frombytes("RGBA", (W * SS, H * SS), tex_ss_bytes).convert("RGBa")
    _SHARED["normals"] = normals
    _SHARED["alpha"] = alpha_ss


def render_frame(k):
    tex_ss, normals, alpha_ss = _SHARED["tex"], _SHARED["normals"], _SHARED["alpha"]
    theta = 2 * math.pi * k / FRAMES
    if k % (FRAMES // 4) == 0 and k % (FRAMES // 2) != 0:
        theta += math.radians(0.6)        # ровно ребро даёт вырожденную проекцию
    c = math.cos(theta)
    back = c < 0
    wall = edge_prefilter(wall_layer(alpha_ss, normals, theta), c)
    # тёмная кромка у лица и у тыла: пластина читается металлом, а не жёлтой заливкой
    rim = edge_prefilter(wall_layer(alpha_ss, normals, theta, rim=0.45), c)
    canvas = Image.new("RGBA", (W * SS, H * SS), (0, 0, 0, 0))
    zs = np.linspace(-THICK / 2, THICK / 2, SLICES)
    if back:
        zs = zs[::-1]
    edge = max(2, SLICES // 16)
    for i, z in enumerate(zs[:-1]):
        layer = rim if (i < edge or i >= len(zs) - 1 - edge) else wall
        canvas.alpha_composite(plane_transform(layer, float(z), theta, mirror=back))
    # лицевая (или задняя) грань с текстурой исходника
    face_z = float(zs[-1])
    face = plane_transform(edge_prefilter(tex_ss, c), face_z, theta, mirror=back)
    f = np.asarray(face, float) / 255.0
    f[..., :3] *= face_light(theta, back)
    g = glint(theta, back)
    if g is not None:
        pos, strength = g
        yy, xx = np.mgrid[0:H * SS, 0:W * SS]
        # диагональная полоса 60° в координатах окна
        u = (xx / (W * SS)) + 0.35 * (yy / (H * SS)) - 0.175
        band = np.exp(-((u - pos) / 0.045) ** 2) * strength
        # блик ложится только на светлый полированный металл, тёмные впадины не засвечивает
        lum = (0.3 * f[..., 0] + 0.59 * f[..., 1] + 0.11 * f[..., 2])
        band = band * np.clip((lum - 0.18) / 0.45, 0, 1)
        f[..., :3] = 1 - (1 - f[..., :3]) * (1 - band[..., None] * np.array([1.0, 0.90, 0.62]))
    f = np.clip(f, 0, 1)
    canvas.alpha_composite(Image.fromarray((f * 255).round().astype(np.uint8), "RGBA"))
    return k, canvas


def finish(canvas_ss):
    """Тень на коже диска + сглаживание ×2 → 768×544."""
    a = canvas_ss.getchannel("A")
    shadow = a.filter(ImageFilter.GaussianBlur(9 * SS)).point(lambda v: int(v * 0.55))
    sh = Image.new("RGBA", canvas_ss.size, (14, 8, 3, 0))
    sh.putalpha(shadow)
    sh = sh.transform(sh.size, Image.AFFINE, (1, 0, -7 * SS, 0, 1, -11 * SS), resample=Image.BILINEAR)
    out = Image.new("RGBA", canvas_ss.size, (0, 0, 0, 0))
    out.alpha_composite(sh)
    out.alpha_composite(canvas_ss)
    return out.convert("RGBa").resize((W, H), Image.LANCZOS).convert("RGBA")


def main():
    args = sys.argv[1:]
    preview = None
    only = None
    if "--preview" in args:
        preview = args[args.index("--preview") + 1]
        os.makedirs(preview, exist_ok=True)
    if "--frames" in args:
        only = [int(v) for v in args[args.index("--frames") + 1].split(",")]

    tex, iou, shift = clean_and_mirror(Image.open(SOURCE))
    print("symmetry IoU %.4f, shift %.2f px" % (iou, shift))
    tex_img = Image.fromarray((tex * 255).round().astype(np.uint8), "RGBA")
    tex_ss = tex_img.convert("RGBa").resize((W * SS, H * SS), Image.LANCZOS).convert("RGBA")
    alpha_ss = np.asarray(tex_ss.getchannel("A"), float) / 255.0
    soft = np.asarray(tex_ss.getchannel("A").filter(ImageFilter.GaussianBlur(5 * SS)), float) / 255.0
    gy, gx = np.gradient(soft)
    norm = np.hypot(gx, gy) + 1e-6
    normals = (-gx / norm, -gy / norm)

    frames = only if only is not None else list(range(FRAMES))
    init = (tex_ss.tobytes(), (normals[0].astype(np.float32), normals[1].astype(np.float32)), alpha_ss.astype(np.float32))
    with Pool(min(4, os.cpu_count() or 1), initializer=_init, initargs=init) as pool:
        for k, canvas in pool.imap_unordered(render_frame, frames):
            img = finish(canvas)
            if preview:
                img.save(os.path.join(preview, "m_spin_%03d.png" % k))
            else:
                img.save(os.path.join(RES, "mobile_medallion_m_spin_%03d.webp" % k), "WEBP", quality=92, method=6)
                if k == 0:
                    img.save(os.path.join(RES, "mobile_medallion_m_idle.webp"), "WEBP", lossless=True, method=6)
            print("frame", k, flush=True)


if __name__ == "__main__":
    main()
