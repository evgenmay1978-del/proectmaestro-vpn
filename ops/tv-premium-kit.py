#!/root/vpn_bot/venv/bin/python3
"""
ТВ premium-кит v3 (2026-07-12): кнопки правой панели ТВ-хоума из МАТЕРИАЛА ЭТАЛОНА
/root/button.png (кнопка «Закрыть» owner, 1891×832) — вместо плоских кропов эскиза,
которые owner забраковал («просто картинка, ни объёма ни глубины»).

Принцип «ничего не растягивать»: торцы-лозы и gem-медальоны идут NATIVE-кропами
(только даунскейл), рельсы — ТАЙЛОМ вдоль оси, кора — native-тайл с mirror-blend.
Объём запекается в ассет офлайн (PIL blur разрешён здесь, на устройстве — нет):
внешняя мягкая тень + внутренняя тень «вдавленности» + тёплый нижний блик.

Выход: app/src/main/res/drawable-nodpi/tvp_*.webp (lossless) в SCALE× арт-размера
слота С ТЕМИ ЖЕ полями M=14 арт-px, что у старых кропов → TvEskizSpec top/h не меняются.
Плюс: divider орнамент секций + QR-безель с ПРОЗРАЧНЫМ проёмом (урок qr-frame).
Сим: ops/tv-premium-sim.py смотрит результат на фоне ДО Kotlin.
"""
from PIL import Image, ImageDraw, ImageFilter, ImageOps
import numpy as np
import os, json

ET = '/root/button.png'
OUT = os.path.join(os.path.dirname(__file__), '..', 'app/src/main/res/drawable-nodpi')
SCRATCH = os.environ.get('TVP_SCRATCH', '/tmp/tvp')
os.makedirs(SCRATCH, exist_ok=True)

SCALE = 2.0          # ассеты в 2× арт-пикселя (1672-арт → рендер под ~3344 сцену)
M = 14               # поле кропа вокруг рамки, арт-px (как у старых tv_ek_*)

# ── замеры эталона (2026-07-12, профили яркости) ──
RAIL_TOP = (158, 202)     # y-диапазон верхней рельсы
RAIL_BOT = (563, 617)     # y-диапазон нижней рельсы
RAIL_CLEAN_X = (566, 786) # чистый участок рельсы (без лоз/медальона)
CAP_L = (24, 566)         # x левого торца-лозы (весь узор)
CAP_Y = (96, 700)         # y торца с выступами лоз над/под рельсами
GEM_TOP_BOX = (796, 84, 1096, 276)
GEM_BOT_BOX = (796, 536, 1096, 764)
BARK_STRIP = (560, 204, 1500, 298)  # чистая кора БЕЗ текста «Закрыть» (полоса под верхней рельсой)
FRAME_RADIUS = 120        # радиус скругления рамки эталона (внешний контур)

def _lum(a):
    return a[..., :3].mean(axis=2)

def load_etalon():
    im = Image.open(ET).convert('RGB')
    return im, np.array(im).astype(np.float32)

def smoothstep(x, lo, hi):
    t = np.clip((x - lo) / (hi - lo), 0, 1)
    return t * t * (3 - 2 * t)

def extract_cap(arr, side='left', hollow=False):
    """Торец рамки с лозами. Внутри контура рамки — opaque (или, при [hollow],
    только приграничное кольцо + золотые завитки — чтобы CTA-интерьер бара
    просвечивал под торцом); снаружи альфа по яркости (золото лоз остаётся,
    тёмный фон эталона уходит)."""
    H, W = arr.shape[:2]
    x0, x1 = CAP_L
    y0, y1 = CAP_Y
    if side == 'right':
        x0, x1 = W - CAP_L[1], W - CAP_L[0]
    crop = arr[y0:y1, x0:x1].copy()
    h, w = crop.shape[:2]
    # контур рамки в координатах кропа: rounded-rect (внешняя граница рельс)
    left_edge = 28 - x0 if side == 'left' else -400  # у правого торца скругление справа
    right_edge = w + 400 if side == 'left' else (W - 28) - x0
    box = [left_edge, RAIL_TOP[0] - y0, right_edge, RAIL_BOT[1] - y0]
    mask_im = Image.new('L', (w, h), 0)
    ImageDraw.Draw(mask_im).rounded_rectangle(box, radius=FRAME_RADIUS, fill=255)
    inside = np.array(mask_im).astype(np.float32) / 255.0
    lum = _lum(crop)
    if hollow:
        # внутренняя «дыра»: глубже 40px от рамки интерьер прозрачен, золото лоз остаётся
        hole_im = Image.new('L', (w, h), 0)
        ImageDraw.Draw(hole_im).rounded_rectangle(
            [box[0] + 46, box[1] + 46, box[2] - 46, box[3] - 46],
            radius=max(8, FRAME_RADIUS - 40), fill=255)
        hole = np.array(hole_im).astype(np.float32) / 255.0
        ring = inside * (1 - hole)
        gold_in = smoothstep(lum, 52, 120) * hole
        inside = np.maximum(ring, gold_in)
    # зона «около рамки» (dilate ~56px): лозы живут тут; дальше — гасим (иначе
    # тёмно-зелёный underglow эталона тянется мутными прямоугольниками)
    near_im = Image.new('L', (w, h), 0)
    ImageDraw.Draw(near_im).rounded_rectangle(
        [box[0] - 56, box[1] - 56, box[2] + 56, box[3] + 56], radius=FRAME_RADIUS + 56, fill=255)
    near = np.array(near_im).astype(np.float32) / 255.0
    outside_a = smoothstep(lum, 26, 84) * near
    alpha = np.maximum(inside, outside_a)
    out = np.dstack([crop, (alpha * 255).astype(np.uint8)])
    return Image.fromarray(out.astype(np.uint8), 'RGBA')

def extract_gem(arr, which='top'):
    """Медальон-камень: альфа по яркости (лежит на чёрном/рельсе)."""
    b = GEM_TOP_BOX if which == 'top' else GEM_BOT_BOX
    crop = arr[b[1]:b[3], b[0]:b[2]].copy()
    lum = _lum(crop)
    # порог как у лоз: мягкий зелёный underglow эталона под нижним медальоном в альфу не пускаем
    alpha = smoothstep(lum, 26, 84)
    # центр камня тёмно-зелёный, но внутри контура медальона — заполнить дыры морфологией
    a8 = (alpha * 255).astype(np.uint8)
    am = Image.fromarray(a8).filter(ImageFilter.MaxFilter(9)).filter(ImageFilter.MinFilter(9))
    a_solid = np.maximum(a8, np.array(am))
    out = np.dstack([crop, a_solid])
    return Image.fromarray(out.astype(np.uint8), 'RGBA')

def extract_rail_tiles(im):
    """Чистые горизонтальные куски рельс (верх/низ) + бесшовность mirror-blend по x."""
    def prep(y0, y1):
        t = im.crop((RAIL_CLEAN_X[0], y0, RAIL_CLEAN_X[1], y1))
        return seamless_x(t)
    return prep(*RAIL_TOP), prep(*RAIL_BOT)

def seamless_x(tile, blend=30):
    """Мягкий шов по x: правый край плавно перетекает в левый."""
    a = np.array(tile).astype(np.float32)
    h, w = a.shape[:2]
    fl = a[:, ::-1]
    t = np.linspace(0, 1, blend)[None, :, None]
    a[:, -blend:] = a[:, -blend:] * (1 - t) + fl[:, -blend:] * t
    return Image.fromarray(a.astype(np.uint8))

def extract_bark(im):
    """Кора интерьеров = готовый бесшовный тайл carved_wood_tile.webp (нарезан из этого же
    эталона в ops/tv-frame-kit.py, native-масштаб, mirror-blend уже сделан)."""
    return Image.open(os.path.join(OUT, 'carved_wood_tile.webp')).convert('RGB')

def vertical_rail_profile(arr):
    """Цветовой профиль рельсы поперёк (для отрисовки вертикальных рельс синтезом):
    средний цвет по чистому x-участку для каждого y верхней рельсы."""
    x0, x1 = RAIL_CLEAN_X
    y0, y1 = RAIL_TOP
    return arr[y0:y1, x0:x1].mean(axis=1)  # (rail_h, 3)

# ───────────────────────────── сборка ─────────────────────────────

def build_bar(w_art, h_art, interior='bark', gems=False, pill=False,
              elems=None, cap_scale=1.0, tag=''):
    """Панель-кнопка: канва (w_art+2M)×(h_art+2M) в SCALE×. Рамка = рельсы-тайл (гориз.)
    + синтез-рельса по дуге торцов из профиля; торцы-лозы native поверх; кора/CTA-интерьер;
    объём: наружная тень, внутренняя тень, нижний тёплый блик."""
    capL, capR, capL_h, capR_h, gem_t, gem_b, rail_t, rail_b, bark, prof = elems
    W = int(round((w_art + 2 * M) * SCALE))
    Hc = int(round((h_art + 2 * M) * SCALE))
    fx0, fy0 = int(M * SCALE), int(M * SCALE)          # рамка внутри канвы
    fx1, fy1 = W - fx0, Hc - fy0
    fw, fh = fx1 - fx0, fy1 - fy0

    # ЕДИНЫЙ скейл материала: пропорция эталона — высота рамки слота / высота рамки эталона.
    # Тогда рельсы в торцах-лозах ложатся ровно на рельсы бара (без рассинхрона толщин).
    rail_native = RAIL_TOP[1] - RAIL_TOP[0]            # 44
    et_frame_h = RAIL_BOT[1] - RAIL_TOP[0]             # 459
    k = fh / et_frame_h * cap_scale
    # у низких широких баров k мал — не даём рельсе стать ниткой; у высоких плит капим
    k = max(0.30, min(0.62, k))
    rail_h_px = int(rail_native * k)
    radius = int((fh / 2) if pill else max(24, FRAME_RADIUS * k))

    canvas = Image.new('RGBA', (W, Hc), (0, 0, 0, 0))

    # 1) интерьер
    inner = Image.new('RGBA', (fw, fh), (0, 0, 0, 0))
    mask = Image.new('L', (fw, fh), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, fw - 1, fh - 1], radius=radius, fill=255)
    if interior == 'bark':
        bw, bh = bark.size
        tiled = Image.new('RGB', (fw, fh))
        for ty in range(0, fh, bh):
            for tx in range(0, fw, bw):
                tiled.paste(bark, (tx, ty))
        inner.paste(tiled, (0, 0), mask)
    else:  # cta: тёмно-зелёный градиент + сдержанное салатовое свечение у нижней кромки (эскиз)
        top = np.array([8, 11, 3], float); bot = np.array([19, 30, 10], float)
        g = np.linspace(0, 1, fh)[:, None, None]
        base = (top[None, None] * (1 - g) + bot[None, None] * g)
        glow = np.zeros((fh, fw, 3), float)
        yy = np.linspace(0, 1, fh)[:, None]
        xx = np.abs(np.linspace(-1, 1, fw))[None, :]
        gl = np.clip((yy - 0.50) / 0.50, 0, 1) ** 1.8 * np.clip(1.05 - xx * 0.50, 0, 1)
        glow[..., 0] = 38 * gl; glow[..., 1] = 76 * gl; glow[..., 2] = 14 * gl
        arr = np.clip(base + glow, 0, 255).astype(np.uint8)
        inner.paste(Image.fromarray(arr, 'RGB'), (0, 0), mask)
    # внутренняя тень (вдавленность): тёмный контур-градиент от рамки внутрь
    sh = Image.new('L', (fw, fh), 0)
    ds = ImageDraw.Draw(sh)
    ds.rounded_rectangle([0, 0, fw - 1, fh - 1], radius=radius, outline=255,
                         width=max(10, int(fh * 0.10)))
    sh = sh.filter(ImageFilter.GaussianBlur(max(6, int(fh * 0.07))))
    sh = Image.composite(sh, Image.new('L', (fw, fh), 0), mask)
    inner = Image.alpha_composite(inner, Image.merge('RGBA', (*Image.new('RGB', (fw, fh), (4, 2, 1)).split(), sh.point(lambda v: int(v * 0.78)))))
    # верхняя внутренняя тень (свет сверху → козырёк тени)
    tga = np.zeros((fh, fw), np.uint8)
    depth = max(10, int(fh * 0.30))
    tga[:depth] = (np.linspace(150, 0, depth)[:, None] * np.ones((1, fw))).astype(np.uint8)
    tg = Image.composite(Image.fromarray(tga), Image.new('L', (fw, fh), 0), mask)
    inner = Image.alpha_composite(inner, Image.merge('RGBA', (*Image.new('RGB', (fw, fh), (4, 2, 1)).split(), tg)))
    # нижний тёплый блик (отражение бронзы)
    bg = np.zeros((fh, fw), np.uint8)
    bd = max(4, int(fh * 0.10))
    bg[-bd:] = (np.linspace(0, 46, bd)[:, None] * np.ones((1, fw))).astype(np.uint8)
    bgm = Image.composite(Image.fromarray(bg), Image.new('L', (fw, fh), 0), mask)
    inner = Image.alpha_composite(inner, Image.merge('RGBA', (*Image.new('RGB', (fw, fh), (210, 160, 80)).split(), bgm)))

    # 2) наружная тень ПОД плитой: ambient вокруг + направленная вниз
    shadow = Image.new('L', (W, Hc), 0)
    dsh = ImageDraw.Draw(shadow)
    dsh.rounded_rectangle([fx0 - int(1 * SCALE), fy0, fx1 + int(1 * SCALE), fy1 + int(2 * SCALE)],
                          radius=radius, fill=110)
    dsh.rounded_rectangle([fx0, fy0 + int(4 * SCALE), fx1, fy1 + int(7 * SCALE)],
                          radius=radius, fill=220)
    shadow = shadow.filter(ImageFilter.GaussianBlur(int(6 * SCALE)))
    canvas = Image.alpha_composite(canvas, Image.merge('RGBA', (*Image.new('RGB', (W, Hc), (0, 0, 0)).split(), shadow)))

    canvas.paste(inner, (fx0, fy0), inner)

    # 3) рамка: горизонтальные рельсы тайлом + вертикальные/дуги синтезом из профиля
    rail_h = max(6, rail_h_px)
    def tile_rail(rail_img, y_dst):
        rw = max(24, int(rail_img.size[0] * k))
        r = rail_img.resize((rw, rail_h), Image.LANCZOS).convert('RGBA')
        x0r = fx0 + int(radius * 0.92)
        endx = fx1 - int(radius * 0.92)
        strip_w = endx - x0r
        if strip_w <= 0:
            return
        band = Image.new('RGBA', (strip_w, rail_h))
        x = 0
        while x < strip_w:
            wseg = min(rw, strip_w - x)
            band.paste(r.crop((0, 0, wseg, rail_h)), (x, 0))
            x += wseg
        # фейд концов ленты → плавный переход в синтез-дугу (без «лесенки» яркости)
        ba = np.array(band)
        fw_ = min(26, strip_w // 4)
        ramp = np.linspace(0, 1, fw_)
        ba[:, :fw_, 3] = (ba[:, :fw_, 3] * ramp[None, :]).astype(np.uint8)
        ba[:, -fw_:, 3] = (ba[:, -fw_:, 3] * ramp[::-1][None, :]).astype(np.uint8)
        band = Image.fromarray(ba, 'RGBA')
        canvas.paste(band, (x0r, y_dst), band)
    # синтез rounded-rect рамки из профиля (антиалиас через суперсэмпл ×3)
    prof_img = np.clip(prof, 0, 255).astype(np.uint8)  # (44,3) поперёк рельсы
    ss = 3
    fr = Image.new('RGBA', (fw * ss, fh * ss), (0, 0, 0, 0))
    dfr = ImageDraw.Draw(fr)
    for i in range(rail_h * ss):
        c = prof_img[min(rail_native - 1, int(i / (k * ss)))]
        dfr.rounded_rectangle(
            [i, i, fw * ss - 1 - i, fh * ss - 1 - i],
            radius=max(2, radius * ss - i), outline=tuple(c) + (255,), width=1)
    fr = fr.resize((fw, fh), Image.LANCZOS)
    canvas.paste(fr, (fx0, fy0), fr)
    tile_rail(rail_t, fy0)
    tile_rail(rail_b, fy1 - rail_h)

    # 4) торцы-лозы native (только даунскейл) — на широких барах; квадратным плитам
    #    лозы не по размеру (перекрывают всю плиту) → чистая рамка + финиалы (ниже)
    wide = (fw / max(1, fh)) > 3.0
    if not pill and wide:
        # торец масштабируется тем же k → рельсы внутри торца совпадают с рельсами бара;
        # CTA-барам — hollow-торцы (зелень проходит под лозами, без стыка кора/зелень)
        srcL, srcR = (capL, capR) if interior == 'bark' else (capL_h, capR_h)
        ch = int((CAP_Y[1] - CAP_Y[0]) * k)
        cw = int((CAP_L[1] - CAP_L[0]) * k)
        cl = srcL.resize((cw, ch), Image.LANCZOS)
        cr = srcR.resize((cw, ch), Image.LANCZOS)
        # вертикальная посадка: верх рельсы торца (RAIL_TOP[0]−CAP_Y[0])*k → на fy0
        cy = fy0 - int((RAIL_TOP[0] - CAP_Y[0]) * k)
        canvas.paste(cl, (fx0 - int(6 * k), cy), cl)
        canvas.paste(cr, (fx1 - cw + int(6 * k), cy), cr)

    # 5) gem-медальоны (верх/низ по центру) для парадных баров; выступ за рамку
    #    клампится полем канвы (медальон чуть съезжает внутрь, но не режется)
    if gems == 'mini':
        gk = k * 0.5
        pairs = (('top', gem_t),)
    elif gems:
        gk = k * 1.15
        pairs = (('top', gem_t), ('bot', gem_b))
    else:
        pairs = ()
    for which, gim in pairs:
        gw = int((GEM_TOP_BOX[2] - GEM_TOP_BOX[0]) * gk)
        gh = int((GEM_TOP_BOX[3] - GEM_TOP_BOX[1]) * gk)
        g = gim.resize((gw, gh), Image.LANCZOS)
        gx = fx0 + fw // 2 - gw // 2
        if which == 'top':
            gy = max(int(2 * SCALE), fy0 + rail_h // 2 - gh // 2 - int(1 * SCALE))
        else:
            gy = min(Hc - gh - int(2 * SCALE), fy1 - rail_h // 2 - gh // 2 + int(1 * SCALE))
        canvas.paste(g, (gx, gy), g)

    return canvas

def build_logo_panel(elems):
    """Лого-панель «MaestroVPN»: premium-рама эталона (торцы-лозы + gem'ы) + РОДНАЯ надпись
    из эскиза (пиксели tv_bg_off, identity сохранена). Канва накрывает старую фигурную раму
    целиком (bbox x89..663, y21..309 арт) → на фоне её не остаётся.
    Возврат: (img, top_art, x0_art, w_art, h_art) — посадка для Kotlin/сима."""
    x0_art, top_art = 84, 16
    frame_w, frame_h = 556, 268          # рамка; канва = +2M полей
    img = build_bar(frame_w, frame_h, interior='bark', gems=True, elems=elems, tag='logo')
    # wordmark из эскиза: зона букв (арт 150..610 × 118..232), альфа по яркости золота
    bg = np.array(Image.open(os.path.join(OUT, 'tv_bg_off.webp')).convert('RGB')).astype(np.float32)
    wm = bg[118:232, 150:610]
    lum = _lum(wm)
    a = smoothstep(lum, 30, 85)
    wm_img = Image.fromarray(np.dstack([wm, a * 255]).astype(np.uint8), 'RGBA')
    # эскиз-арт → канва 2×: апскейл + лёгкий unsharp, чтобы буквы не мылились рядом с native-рамой
    wm2 = wm_img.resize((wm_img.width * 2, wm_img.height * 2), Image.LANCZOS)
    wm2 = wm2.filter(ImageFilter.UnsharpMask(radius=2, percent=70, threshold=2))
    W, Hc = img.size
    img.paste(wm2, (W // 2 - wm2.width // 2, Hc // 2 - wm2.height // 2), wm2)

    # Заплатка под панелью: старая фигурная рама светилась зелёным ВНИЗ (хвост x125..618,
    # y312..404) — канва накрывает раму только до y312. Гасим самый заметный след дровами
    # из чистого донора эскиза (x88..160), НО только до y342: с y347 начинается видимое
    # кольцо медальона (RING_OFF_CY−RVIS) — его не трогаем, ниже зелень читается как glow глаза.
    patch_y0, patch_y1 = 300, 342      # от-под рельсы (298) до верха видимого кольца (347−5)
    px0, px1 = 104, 640
    donor = bg[patch_y0:patch_y1, 88:160]              # чистое дерево той же высоты
    dw = donor.shape[1]
    cols = []
    need = px1 - px0
    i = 0
    while sum(c.shape[1] for c in cols) < need:
        cols.append(donor[:, ::-1] if i % 2 else donor)  # чередуем зеркало — не видно повторов
        i += 1
    patch = np.concatenate(cols, axis=1)[:, :need]
    # швы между блоками мягчим лёгким горизонтальным блюром стыков
    pimg = Image.fromarray(patch.astype(np.uint8), 'RGB').resize((need * 2, (patch_y1 - patch_y0) * 2), Image.LANCZOS)
    pa = np.full((pimg.height, pimg.width), 255, np.float32)
    f = 12  # feather краёв, px канвы
    ramp = np.linspace(0, 1, f)
    pa[:, :f] *= ramp[None, :]
    pa[:, -f:] *= ramp[::-1][None, :]
    pa[:f, :] *= ramp[:, None]
    pa[-f:, :] *= ramp[::-1][:, None]
    pimg = Image.merge('RGBA', (*pimg.split(), Image.fromarray(pa.astype(np.uint8))))
    # расширяем канву вниз: СНАЧАЛА заплатка, ПОВЕРХ — рама (её мягкая тень ложится на дерево)
    new_h_art = (patch_y1 + 4) - top_art
    big = Image.new('RGBA', (W, int(new_h_art * SCALE)), (0, 0, 0, 0))
    big.paste(pimg, (int((px0 - x0_art) * SCALE), int((patch_y0 - top_art) * SCALE)), pimg)
    frame_layer = Image.new('RGBA', big.size, (0, 0, 0, 0))
    frame_layer.paste(img, (0, 0))
    big = Image.alpha_composite(big, frame_layer)
    return big, top_art, x0_art, frame_w + 2 * M, new_h_art

def build_divider(w_art, h_art, elems):
    """Орнамент-разделитель секции: рельса-тайл с gem-финиалом слева и растворением справа."""
    capL, capR, capL_h, capR_h, gem_t, gem_b, rail_t, rail_b, bark, prof = elems
    W = int(w_art * SCALE); Hc = int(h_art * SCALE)
    canvas = Image.new('RGBA', (W, Hc), (0, 0, 0, 0))
    k = 0.58
    rail_h = max(8, int((RAIL_TOP[1] - RAIL_TOP[0]) * k))
    r = rail_t.resize((max(24, int(rail_t.size[0] * k)), rail_h), Image.LANCZOS)
    y = Hc // 2 - rail_h // 2
    x = int(6 * SCALE)
    while x < W:
        seg = min(r.size[0], W - x)
        canvas.paste(r.crop((0, 0, seg, rail_h)), (x, y))
        x += seg
    # растворение рельсы вправо (градиент альфы)
    a = np.array(canvas)
    fade = np.ones(W, np.float32)
    f0 = int(W * 0.72)
    fade[f0:] = np.linspace(1, 0, W - f0)
    a[..., 3] = (a[..., 3].astype(np.float32) * fade[None, :]).astype(np.uint8)
    canvas = Image.fromarray(a, 'RGBA')
    # gem-финиал слева
    gk = k * 0.85
    gw = int((GEM_TOP_BOX[2] - GEM_TOP_BOX[0]) * gk)
    gh = int((GEM_TOP_BOX[3] - GEM_TOP_BOX[1]) * gk)
    g = gem_t.resize((gw, gh), Image.LANCZOS)
    canvas.paste(g, (0, Hc // 2 - gh // 2), g)
    return canvas

def build_qr_bezel(size_art, elems):
    """Квадратный QR-безель с ПРОЗРАЧНЫМ проёмом (урок qr-decorative-frame-lesson):
    рамка из профиля рельсы + углы-листики; проём = чистая альфа-дыра."""
    capL, capR, capL_h, capR_h, gem_t, gem_b, rail_t, rail_b, bark, prof = elems
    Spx = int(size_art * SCALE)
    ss = 2
    rail_native = RAIL_TOP[1] - RAIL_TOP[0]
    bez = int(26 * SCALE)          # толщина безеля
    radius = int(30 * SCALE)
    prof_img = np.clip(prof, 0, 255).astype(np.uint8)
    fr = Image.new('RGBA', (Spx * ss, Spx * ss), (0, 0, 0, 0))
    dfr = ImageDraw.Draw(fr)
    for i in range(bez * ss):
        c = prof_img[min(rail_native - 1, int(i * rail_native / (bez * ss)))]
        dfr.rounded_rectangle([i, i, Spx * ss - 1 - i, Spx * ss - 1 - i],
                              radius=max(2, radius * ss - i), outline=tuple(c) + (255,), width=1)
    fr = fr.resize((Spx, Spx), Image.LANCZOS)
    # gem-медальоны по центрам сторон (мелкие)
    gk = 0.44
    gw = int((GEM_TOP_BOX[2] - GEM_TOP_BOX[0]) * gk)
    gh = int((GEM_TOP_BOX[3] - GEM_TOP_BOX[1]) * gk)
    for which, gim in (('top', gem_t), ('bot', gem_b)):
        g = gim.resize((gw, gh), Image.LANCZOS)
        gx = Spx // 2 - gw // 2
        gy = (bez // 2 - gh // 2) if which == 'top' else (Spx - bez // 2 - gh // 2)
        fr.paste(g, (gx, gy), g)
    return fr

# ───────────────────────────── main ─────────────────────────────
if __name__ == '__main__':
    im, arr = load_etalon()
    capL = extract_cap(arr, 'left')
    capR = extract_cap(arr, 'right')
    capL_h = extract_cap(arr, 'left', hollow=True)
    capR_h = extract_cap(arr, 'right', hollow=True)
    gem_t = extract_gem(arr, 'top')
    gem_b = extract_gem(arr, 'bot')
    rail_t, rail_b = extract_rail_tiles(im)
    bark = extract_bark(im)
    prof = vertical_rail_profile(arr)
    elems = (capL, capR, capL_h, capR_h, gem_t, gem_b, rail_t, rail_b, bark, prof)

    for name, img in (('capL', capL), ('capR', capR), ('gem_t', gem_t),
                      ('rail_t', rail_t), ('bark', bark)):
        img.save(f'{SCRATCH}/elem_{name}.png')

    # Слоты (арт-размеры РАМКИ, без полей): из TvEskizSpec: h кропа − 2M
    slots = {
        'tvp_buy':    dict(w=894, h=121 - 2 * M, interior='cta',  gems=True),
        'tvp_tile':   dict(w=287, h=185 - 2 * M, interior='bark', gems='mini', cap_scale=0.85),
        'tvp_update': dict(w=894, h=116 - 2 * M, interior='bark', gems=False),
        'tvp_phone':  dict(w=894, h=128 - 2 * M, interior='cta',  gems=False, pill=True, cap_scale=1.3),
        'tvp_chip':   dict(w=287, h=152 - 2 * M, interior='bark', gems=False, cap_scale=0.8),
    }
    meta = {}
    for name, cfg in slots.items():
        img = build_bar(cfg['w'], cfg['h'], interior=cfg['interior'], gems=cfg['gems'],
                        pill=cfg.get('pill', False), elems=elems,
                        cap_scale=cfg.get('cap_scale', 1.0), tag=name)
        img.save(f'{SCRATCH}/{name}.png')
        meta[name] = dict(w_art=cfg['w'] + 2 * M, h_art=cfg['h'] + 2 * M, scale=SCALE)
        print(name, img.size)

    div = build_divider(560, 24, elems)
    div.save(f'{SCRATCH}/tvp_divider.png')
    qr = build_qr_bezel(340, elems)
    qr.save(f'{SCRATCH}/tvp_qr_bezel.png')
    logo, lt, lx, lw, lh = build_logo_panel(elems)
    logo.save(f'{SCRATCH}/tvp_logo.png')
    meta['tvp_logo'] = dict(top_art=lt, x0_art=lx, w_art=lw, h_art=lh, scale=SCALE)
    print('tvp_logo', logo.size, 'at art', lx, lt)
    json.dump(meta, open(f'{SCRATCH}/tvp_meta.json', 'w'), indent=1)
    print('done →', SCRATCH)
