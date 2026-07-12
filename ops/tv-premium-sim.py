#!/root/vpn_bot/venv/bin/python3
"""
PIL-сим ТВ-хоума с premium-панелями v3 (правило проекта: смотреть глазами ДО Kotlin).
Первый экран 1920×1080: фон tv_bg_off + новые tvp_* на позициях TvEskizSpec +
тексты/иконки как их положит Compose. Плюс сим share-диалога (скрим 0.85 + новый QR-безель).
"""
from PIL import Image, ImageDraw, ImageFont
import os, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RES = os.path.join(ROOT, 'app/src/main/res/drawable-nodpi')
S = os.environ.get('TVP_SCRATCH', '/tmp/tvp')

ART_W, ART_H = 1672.0, 941.0
PANEL_X0, PANEL_X1 = 706.0, 1600.0
COL_X0, COL_X1, GUT = 712.0, 1593.0, 10.0
CELL_W = (COL_X1 - COL_X0 - 2 * GUT) / 3.0
M = 14

W, H = 1920, 1080
s = W / ART_W  # 1.148

PLAY = os.path.join(ROOT, 'app/src/main/res/font/playfair_display.ttf')
SANS_B = '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf'
SANS = '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf'

GOLD = (232, 200, 119)
SALAD = (196, 224, 110)
SILVER = (208, 210, 214)

def font(path, size):
    return ImageFont.truetype(path, size)

def text_center(dr, xy, txt, f, fill, stroke=None, sw=0):
    bbox = dr.textbbox((0, 0), txt, font=f)
    tw, th = bbox[2] - bbox[0], bbox[3] - bbox[1]
    x = xy[0] - tw / 2 - bbox[0]
    y = xy[1] - th / 2 - bbox[1]
    if stroke:
        dr.text((x, y), txt, font=f, fill=fill, stroke_width=sw, stroke_fill=stroke)
    else:
        dr.text((x, y), txt, font=f, fill=fill)

def paste_slot(bg, asset_path, top_art, h_art, x0_art=PANEL_X0, x1_art=PANEL_X1):
    """Кладёт ассет (канва с полями M) на слот: top_art = верх кропа (уже с полем)."""
    im = Image.open(asset_path).convert('RGBA')
    dst_w = int(round((x1_art - x0_art + 2 * M) * s))
    dst_h = int(round(h_art * s))
    im = im.resize((dst_w, dst_h), Image.LANCZOS)
    x = int(round((x0_art - M) * s))
    y = int(round(top_art * s))
    bg.paste(im, (x, y), im)
    return x, y, dst_w, dst_h

def sim_home():
    bg = Image.open(os.path.join(RES, 'tv_bg_off.webp')).convert('RGB').resize((W, H), Image.LANCZOS)
    bg = bg.convert('RGBA')
    dr = ImageDraw.Draw(bg)

    # BUY 43..164 (арт, кроп с полями)
    x, y, w, h = paste_slot(bg, f'{S}/tvp_buy.png', 43, 121)
    fbuy = font(SANS_B, int(34 * s))
    text_center(dr, (W * (PANEL_X0 + PANEL_X1) / 2 / ART_W, (43 + 121 / 2) * s), '🛒  Купить подписку', fbuy, SALAD)

    # ROW_CODE: 3 тайла 178..363
    ftile = font(SANS_B, int(24 * s))
    labels = ['Ввести код', 'Приложения VPN', 'Поделиться']
    for i, lab in enumerate(labels):
        cx0 = COL_X0 + i * (CELL_W + GUT)
        paste_slot(bg, f'{S}/tvp_tile.png', 178, 185, cx0, cx0 + CELL_W)
        ccx = (cx0 + CELL_W / 2) * s
        ccy = (178 + 185 / 2) * s
        # иконка-плейсхолдер (зелёный глиф-кружок как на эскизе)
        r = 22 * s
        dr.ellipse([ccx - r, ccy - 34 * s - r, ccx + r, ccy - 34 * s + r], outline=(120, 220, 90), width=int(4 * s))
        text_center(dr, (ccx, ccy + 34 * s), lab, ftile, (238, 238, 234))

    # UPDATE 365..481
    paste_slot(bg, f'{S}/tvp_update.png', 365, 116)
    text_center(dr, (W * (PANEL_X0 + PANEL_X1) / 2 / ART_W, (365 + 116 / 2) * s), '⬇  Обновить приложение', font(SANS_B, int(30 * s)), (232, 232, 228))

    # KONTAKTY 494..580: заголовок Playfair-gold + divider
    fz = font(PLAY, int(34 * s))
    zx = PANEL_X0 * s
    zy = (494 + 86 / 2) * s
    dr.text((zx, zy - 20 * s), 'КОНТАКТЫ', font=fz, fill=GOLD)
    div = Image.open(f'{S}/tvp_divider.png').convert('RGBA')
    dw = int(560 * s * 0.62); dh = int(24 * s)
    div2 = div.resize((dw, dh), Image.LANCZOS)
    bg.paste(div2, (int(zx + 250 * s), int(zy - dh / 2 + 2 * s)), div2)

    # PHONE 545..673 pill
    paste_slot(bg, f'{S}/tvp_phone.png', 545, 128)
    text_center(dr, (W * (PANEL_X0 + PANEL_X1) / 2 / ART_W, (545 + 128 / 2) * s), '📞  8 977 811-65-64', font(SANS_B, int(40 * s)), SALAD)

    # HINT 676..758
    text_center(dr, (W * (PANEL_X0 + PANEL_X1) / 2 / ART_W, (676 + 82 / 2) * s),
                'Если я не ответил на звонок — обязательно напишите в любом из мессенджеров 👇',
                font(SANS, int(17 * s)), SILVER)

    # ROW_TG 730..882
    fchip = font(SANS_B, int(24 * s))
    for i, lab in enumerate(['Telegram', 'WhatsApp', 'МАКС']):
        cx0 = COL_X0 + i * (CELL_W + GUT)
        paste_slot(bg, f'{S}/tvp_chip.png', 730, 152, cx0, cx0 + CELL_W)
        ccx = (cx0 + CELL_W / 2) * s
        ccy = (730 + 152 / 2) * s
        text_center(dr, (ccx + 20 * s, ccy), lab, fchip, (240, 240, 236))
        rr = 14 * s
        icx = ccx - 78 * s
        dr.ellipse([icx - rr, ccy - rr, icx + rr, ccy + rr],
                   fill=[(42, 171, 238), (37, 211, 102), (39, 135, 245)][i])

    # янтарный фокус на «Приложения VPN» (пример выделения)
    fx0 = (COL_X0 + 1 * (CELL_W + GUT)) * s
    fy0 = (178 + M) * s
    fw = CELL_W * s
    fh = (185 - 2 * M) * s
    for k, (col, wd) in enumerate([((255, 206, 140, 255), 3), ((255, 173, 92, 160), 6)]):
        dr.rounded_rectangle([fx0 + 8 * s + k * 5, fy0 + 8 * s + k * 5, fx0 + fw - 8 * s - k * 5, fy0 + fh - 8 * s - k * 5],
                             radius=int(26 * s) - k * 4, outline=col, width=int(wd * s / 2))

    out = bg.convert('RGB')
    out.save(f'{S}/sim_home.png')
    print('sim_home saved')

def sim_share():
    """Share-диалог: скрим 0.85 + carved-панель + новый QR-безель поверх белой карточки ЗА ним."""
    base = Image.open(f'{S}/sim_home.png').convert('RGBA')
    scrim = Image.new('RGBA', base.size, (0, 0, 0, int(255 * 0.85)))
    base = Image.alpha_composite(base, scrim)
    dr = ImageDraw.Draw(base)
    # панель диалога 460dp кап ≈ 700px на 1080p при density 1.5… для сима: 640×720
    pw, ph = 640, 760
    px, py = (W - pw) // 2, (H - ph) // 2
    bark = Image.open(os.path.join(RES, 'carved_wood_tile.webp')).convert('RGB')
    panel = Image.new('RGB', (pw, ph))
    for ty in range(0, ph, bark.size[1]):
        for tx in range(0, pw, bark.size[0]):
            panel.paste(bark, (tx, ty))
    # лёгкое затемнение + бронзовая рамка (как carvedSurface)
    dark = Image.new('L', (pw, ph), int(255 * 0.16))
    panel = Image.composite(Image.new('RGB', (pw, ph), (0, 0, 0)), panel, dark)
    base.paste(panel, (px, py))
    dpr = ImageDraw.Draw(base)
    for inset, colw in [(1, ((5, 3, 2), 4)), (5, ((200, 154, 80), 4)), (10, ((8, 5, 3), 2))]:
        dpr.rounded_rectangle([px + inset, py + inset, px + pw - inset, py + ph - inset],
                              radius=24 - inset, outline=colw[0], width=colw[1])
    text_center(dpr, (px + pw / 2, py + 44), 'Поделиться подпиской', font(PLAY, 34), GOLD)
    text_center(dpr, (px + pw / 2, py + 78), '◆', font(SANS, 16), GOLD)
    # сегменты Android/iPhone
    seg_w, seg_h = pw - 100, 62
    sx, sy = px + 50, py + 104
    dpr.rounded_rectangle([sx, sy, sx + seg_w, sy + seg_h], radius=14, fill=(26, 18, 10), outline=(200, 154, 80), width=2)
    dpr.rounded_rectangle([sx + 4, sy + 4, sx + seg_w // 2 - 2, sy + seg_h - 4], radius=10,
                          fill=(30, 46, 20), outline=(232, 200, 119), width=2)
    text_center(dpr, (sx + seg_w // 4, sy + seg_h / 2), 'Android', font(SANS_B, 24), (255, 255, 255))
    text_center(dpr, (sx + 3 * seg_w // 4, sy + seg_h / 2), 'iPhone', font(SANS_B, 24), (170, 140, 90))
    text_center(dpr, (px + pw / 2, sy + seg_h + 40), 'Android: отсканируй в MaestroVPN —', font(SANS, 21), SILVER)
    text_center(dpr, (px + pw / 2, sy + seg_h + 68), 'подключатся все 4 протокола.', font(SANS, 21), SILVER)

    # QR-композит ПО УРОКУ: белая карточка ЗА безелем, безель поверх
    qr_zone = 380
    qx, qy = px + (pw - qr_zone) // 2, sy + seg_h + 100
    bez = Image.open(f'{S}/tvp_qr_bezel.png').convert('RGBA').resize((qr_zone, qr_zone), Image.LANCZOS)
    # белая карточка: чуть больше проёма (проём = qr_zone - 2*bez_th; bez_th≈40*380/680)
    bez_th = int(20 * 2 * (qr_zone / 680))  # толщина безеля в пикселях сима
    card = qr_zone - 2 * bez_th + 8  # заправка 4px под безель с каждой стороны
    cx0 = qx + (qr_zone - card) // 2
    dpr.rounded_rectangle([cx0, qy + (qr_zone - card) // 2, cx0 + card, qy + (qr_zone - card) // 2 + card],
                          radius=10, fill=(255, 255, 255))
    # псевдо-QR
    import random
    random.seed(7)
    qsz = card - 36
    q0x, q0y = cx0 + 18, qy + (qr_zone - card) // 2 + 18
    cell = qsz // 29
    for r in range(29):
        for c in range(29):
            if random.random() < 0.45:
                dpr.rectangle([q0x + c * cell, q0y + r * cell, q0x + c * cell + cell - 1, q0y + r * cell + cell - 1], fill=(0, 0, 0))
    base.paste(bez, (qx, qy), bez)

    base.convert('RGB').save(f'{S}/sim_share.png')
    print('sim_share saved')

if __name__ == '__main__':
    sim_home()
    sim_share()
