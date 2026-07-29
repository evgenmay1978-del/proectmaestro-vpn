#!/usr/bin/env python3
"""Deterministic visual smoke test for the PHONE screens (390x844 dp @2x).

Телефонный близнец ops/tv-master-sim.py. Существует потому, что правило проекта
требует смотреть UI ГЛАЗАМИ ДО КОДА (CLAUDE.md, п.3 «UI = пиксель-в-пиксель»), а
локального Android SDK на S1 нет и эмулятора тоже: собрать APK можно только в CI,
~13 минут за прогон, и выгрузка APK ещё и падает при переполненной квоте артефактов.
Поэтому экран воспроизводится из ПОДЛИННЫХ ассетов репозитория и чисел из Kotlin.

Что берётся из репо (воспроизводимо, ничего не выдумано):
  mobile_home_scene.webp 853x1844 — сцена с медальоном и живым глазом
  mobile_surface.webp            — фон внутренних экранов
  frame_button.9.png / frame_bar.9.png / frame_panel.9.png — nine-patch рамы
  font/playfair_display.ttf      — титульный шрифт

Геометрия — из TvHomeScreen.kt (ContentScale.Crop для 853x1844, центр медальона
430,711, радиус 260, windowTop = центр+радиус+12, windowBottom = 7% высоты),
цвета — из premium/MobilePremiumTokens.kt и theme/Color.kt.

⛔ ЛОВУШКИ (не «улучшать» бездумно):
 1. В Liberation Sans НЕТ глифов ₽ и ⚠ — рисуется .notdef-квадрат (проверено
    fontTools cmap). Строки с ними идут DejaVu. Ярлыки плиток намеренно оставлены
    на Liberation: он по ширине близок к Roboto, поэтому обрезка на симуляции не
    преувеличена против настоящего устройства.
 2. clip_txt(..., ellipsis=False) воспроизводит TextOverflow.Clip — дефолт Compose,
    когда у Text задан maxLines и НЕ задан overflow. Это обрубание посреди глифа,
    без «...». Если поправить на ellipsis=True «чтобы красивее» — симуляция начнёт
    врать и перестанет ловить именно этот класс дефектов.
 3. Это НЕ скриншот. Глаз статичен (в приложении моргает и следит за пальцем),
    барабан не крутится, суммы/QR условные. Для доказательств поведения — CI и
    устройство, симуляция только для глаз.

Использование:  ops/phone-screen-sim.py   ->  build/phone-screen-sim/phone-screens.png
"""
import os
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont, ImageFilter

ROOT = Path(__file__).resolve().parents[1]
RES = ROOT / 'app/src/main/res/drawable-nodpi'
OUTDIR = ROOT / 'build/phone-screen-sim'
OUTDIR.mkdir(parents=True, exist_ok=True)
OUT = str(OUTDIR / 'phone-screens.png')
S = 2  # супер-семплинг: работаем в 2x от dp

PLAY = str(ROOT / 'app/src/main/res/font/playfair_display.ttf')
SANS = '/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf'
SANSB = '/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf'
DEJA = '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf'
DEJAB = '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf'
# ⛔ ЛОВУШКА: в Liberation Sans НЕТ глифов ₽ и ⚠ (проверено fontTools cmap) — рисуется
# .notdef-квадрат. Для строк с ними берём DejaVu. Ярлыки плиток оставлены на Liberation:
# он по ширине близок к Roboto, поэтому обрезка на макете не преувеличена против реальности.

def F(path, sp): return ImageFont.truetype(path, round(sp * S))

# ── палитра MobilePremiumTokens.kt + theme/Color.kt
WALNUT=(18,13,9); LEATHER=(29,21,16); GOLD=(224,189,112); GOLDM=(168,135,74)
EMER=(41,201,101); RUBY=(201,86,77); TXT=(241,232,208); TXTM=(189,181,167)
NEONG=(70,224,90); ORANGE=(240,121,42); STATERED=(255,64,64)

def nine(src, w, h, sl=None):
    """9-slice масштабирование nine-patch без служебной 1px рамки."""
    im = src.crop((1, 1, src.width - 1, src.height - 1)).convert('RGBA')
    sl = sl or max(8, min(im.width, im.height) // 3)
    sl = min(sl, im.width // 2 - 1, im.height // 2 - 1)
    w, h = max(w, sl * 2 + 1), max(h, sl * 2 + 1)
    out = Image.new('RGBA', (w, h))
    W, H = im.width, im.height
    box = lambda a, b, c, d: im.crop((a, b, c, d))
    cw, ch = w - 2 * sl, h - 2 * sl
    # центр и края
    out.paste(box(sl, sl, W - sl, H - sl).resize((cw, ch), Image.BILINEAR), (sl, sl))
    out.paste(box(sl, 0, W - sl, sl).resize((cw, sl), Image.BILINEAR), (sl, 0))
    out.paste(box(sl, H - sl, W - sl, H).resize((cw, sl), Image.BILINEAR), (sl, h - sl))
    out.paste(box(0, sl, sl, H - sl).resize((sl, ch), Image.BILINEAR), (0, sl))
    out.paste(box(W - sl, sl, W, H - sl).resize((sl, ch), Image.BILINEAR), (w - sl, sl))
    # углы
    out.paste(box(0, 0, sl, sl), (0, 0)); out.paste(box(W - sl, 0, W, sl), (w - sl, 0))
    out.paste(box(0, H - sl, sl, H), (0, h - sl)); out.paste(box(W - sl, H - sl, W, H), (w - sl, h - sl))
    return out

BTN = Image.open(f'{str(RES)}/frame_button.9.png')
PANEL = Image.open(f'{str(RES)}/frame_panel.9.png')
BAR = Image.open(f'{str(RES)}/frame_bar.9.png')

def cover(path, w, h):
    im = Image.open(path).convert('RGB')
    k = max(w / im.width, h / im.height)
    im = im.resize((round(im.width * k), round(im.height * k)), Image.LANCZOS)
    return im.crop(((im.width - w) // 2, (im.height - h) // 2,
                    (im.width - w) // 2 + w, (im.height - h) // 2 + h))

def vgrad(w, h, stops):
    """Вертикальный градиент по стопам [(pos, (r,g,b), alpha)] — как Brush.verticalGradient."""
    g = Image.new('RGBA', (1, h))
    px = g.load()
    for y in range(h):
        t = y / max(1, h - 1)
        for i in range(len(stops) - 1):
            p0, c0, a0 = stops[i]; p1, c1, a1 = stops[i + 1]
            if p0 <= t <= p1:
                f = 0 if p1 == p0 else (t - p0) / (p1 - p0)
                px[0, y] = (round(c0[0] + (c1[0] - c0[0]) * f), round(c0[1] + (c1[1] - c0[1]) * f),
                            round(c0[2] + (c1[2] - c0[2]) * f), round(a0 + (a1 - a0) * f))
                break
    return g.resize((w, h), Image.BILINEAR)

def circle(d, cx, cy, r, fill):
    d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=fill)

def txt(d, xy, s, font, fill, anchor='la'):
    d.text(xy, s, font=font, fill=fill, anchor=anchor)

def clip_txt(layer, x, y, s, font, fill, maxw, ellipsis=False):
    """Рисует текст, обрезая по maxw. ellipsis=False = TextOverflow.Clip (дефолт Compose):
    обрубание посреди глифа, без «…». Для ярлыков плиток используется autosize_txt
    (в коде autoSize), clip_txt остаётся для мест, где Compose действительно клипует."""
    tmp = Image.new('RGBA', (max(1, round(font.getlength(s)) + 8), font.size * 2))
    ImageDraw.Draw(tmp).text((0, 0), s, font=font, fill=fill)
    if ellipsis and tmp.width > maxw:
        while s and font.getlength(s + '…') > maxw: s = s[:-1]
        tmp = Image.new('RGBA', (maxw, font.size * 2))
        ImageDraw.Draw(tmp).text((0, 0), s + '…', font=font, fill=fill)
    tmp = tmp.crop((0, 0, min(tmp.width, maxw), tmp.height))
    layer.alpha_composite(tmp, (round(x), round(y)))

def autosize_txt(layer, x, y, s, fill, maxw, path, min_sp, max_sp, step_sp=0.5, bold=True):
    """Воспроизводит BasicText + TextAutoSize.StepBased: уменьшает кегль с max_sp до
    min_sp шагом step_sp, пока строка не влезет в maxw. Именно так теперь устроены
    ярлыки плиток протоколов (PhoneRevolverMenu.kt, PremiumProtocolTile) — приём взят
    из component/NeonGlass.kt:228. Если строка не влезает даже на min_sp, Compose
    оставляет min_sp и обрезает — здесь то же самое."""
    sp = max_sp
    while sp > min_sp:
        if F(path, sp).getlength(s) <= maxw:
            break
        sp -= step_sp
    clip_txt(layer, x, y, s, F(path, sp), fill, maxw, ellipsis=False)
    return sp

# ─────────── иконки (простые векторные заготовки под Material)
def ic_bolt(d, x, y, s, c):
    d.polygon([(x+s*.55,y),(x+s*.2,y+s*.55),(x+s*.45,y+s*.55),(x+s*.38,y+s),
               (x+s*.8,y+s*.42),(x+s*.52,y+s*.42)], fill=c)
def ic_globe(d, x, y, s, c):
    d.ellipse((x,y,x+s,y+s), outline=c, width=max(2,round(s*.09)))
    d.ellipse((x+s*.28,y,x+s*.72,y+s), outline=c, width=max(2,round(s*.07)))
    d.line((x,y+s*.5,x+s,y+s*.5), fill=c, width=max(2,round(s*.07)))
def ic_lock(d, x, y, s, c):
    d.rounded_rectangle((x+s*.14,y+s*.42,x+s*.86,y+s), radius=s*.12, fill=c)
    d.arc((x+s*.26,y+s*.06,x+s*.74,y+s*.62), 180, 360, fill=c, width=max(2,round(s*.11)))
def ic_check(d, x, y, s, c):
    d.ellipse((x,y,x+s,y+s), fill=c)
    d.line([(x+s*.26,y+s*.52),(x+s*.44,y+s*.7),(x+s*.76,y+s*.32)],
           fill=(12,9,6), width=max(2,round(s*.12)), joint='curve')
def ic_radio(d, x, y, s, c):
    d.ellipse((x,y,x+s,y+s), outline=c, width=max(2,round(s*.1)))
def ic_cart(d, x, y, s, c):
    d.line((x,y+s*.12,x+s*.2,y+s*.12), fill=c, width=max(2,round(s*.1)))
    d.polygon([(x+s*.2,y+s*.12),(x+s,y+s*.28),(x+s*.8,y+s*.66),(x+s*.34,y+s*.66)], fill=c)
    circle(d, x+s*.4, y+s*.86, s*.1, c); circle(d, x+s*.78, y+s*.86, s*.1, c)
def ic_person(d, x, y, s, c):
    circle(d, x+s*.5, y+s*.3, s*.22, c)
    d.pieslice((x+s*.1,y+s*.5,x+s*.9,y+s*1.25), 180, 360, fill=c)
def ic_cal(d, x, y, s, c):
    d.rounded_rectangle((x,y+s*.12,x+s,y+s), radius=s*.1, outline=c, width=max(2,round(s*.08)))
    d.line((x,y+s*.36,x+s,y+s*.36), fill=c, width=max(2,round(s*.08)))
    d.line((x+s*.28,y,x+s*.28,y+s*.2), fill=c, width=max(2,round(s*.09)))
    d.line((x+s*.72,y,x+s*.72,y+s*.2), fill=c, width=max(2,round(s*.09)))
def ic_back(d, x, y, s, c):
    d.line((x+s*.1,y+s*.5,x+s*.92,y+s*.5), fill=c, width=max(2,round(s*.09)))
    d.line([(x+s*.42,y+s*.18),(x+s*.1,y+s*.5),(x+s*.42,y+s*.82)], fill=c,
           width=max(2,round(s*.09)), joint='curve')
def ic_err(d, x, y, s, c):
    d.ellipse((x,y,x+s,y+s), outline=c, width=max(3,round(s*.07)))
    d.line((x+s*.5,y+s*.24,x+s*.5,y+s*.58), fill=c, width=max(3,round(s*.09)))
    circle(d, x+s*.5, y+s*.72, s*.055, c)
def ic_search(d, x, y, s, c):
    d.ellipse((x+s*.06,y+s*.06,x+s*.7,y+s*.7), outline=c, width=max(2,round(s*.09)))
    d.line((x+s*.64,y+s*.64,x+s*.94,y+s*.94), fill=c, width=max(2,round(s*.11)))
def ic_qr(d, x, y, s, c):
    for (a,b) in [(0,0),(.62,0),(0,.62)]:
        d.rectangle((x+s*a,y+s*b,x+s*(a+.32),y+s*(b+.32)), outline=c, width=max(2,round(s*.08)))
    for (a,b) in [(.46,.46),(.62,.62),(.84,.5),(.5,.84)]:
        d.rectangle((x+s*a,y+s*b,x+s*(a+.1),y+s*(b+.1)), fill=c)

# ═════════ ЭКРАН 1: главный (револьвер) ═════════
W, H = 390 * S, 844 * S
def screen_home():
    ph = cover(f'{str(RES)}/mobile_home_scene.webp', W, H).convert('RGBA')
    # геометрия TvHomeScreen.kt:224-240 для 390x844dp
    sc = max(390 / 853, 844 / 1844)
    cy = ((844 - 1844 * sc) / 2 + 711 * sc); r = 260 * sc
    win_t = round((cy + r + 12) * S); win_b = round((844 - 844 * .070) * S)
    lay = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(lay)
    pad = 18 * S; x0, x1 = pad, W - pad

    # ── статус: ФИКСИРОВАННАЯ ШАПКА вне барабана и вне маски.
    # Column { PhoneStatusRow(); Box(weight(1f)) { LazyColumn + маска } } в
    # PhoneRevolverMenu. Рисуем на отдельном слое, который НЕ накрывается градиентом:
    # раньше статус был первым элементом списка и на скролле 0 тонул в затемнении.
    head = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    hd = ImageDraw.Draw(head)
    hy = win_t + 6 * S
    f_st = F(SANSB, 16); f_pr = F(SANSB, 14)
    tw = f_st.getlength('ОТКЛЮЧЕНО')
    dot_x = (W - (tw + 11 * S + 9 * S)) / 2
    circle(hd, dot_x + 5.5 * S, hy + 9 * S, 5.5 * S, STATERED)
    txt(hd, (dot_x + 20 * S, hy), 'ОТКЛЮЧЕНО', f_st, STATERED)
    hy += 22 * S + 8 * S
    txt(hd, (W / 2, hy), 'Отключён: VLESS  •  авто', f_pr, ORANGE, anchor='ma')
    hy += 20 * S
    drum_t = round(hy + 6 * S)          # верх прокручиваемой части

    y = drum_t + 8 * S                  # contentPadding top = 8.dp

    # ── ПРОТОКОЛ (PremiumMenuSectionLabel:514)
    f_sec = F(PLAY, 16)
    y += 6 * S
    txt(d, (W / 2, y), 'П Р О Т О К О Л', f_sec, GOLD, anchor='ma')
    y += 20 * S + 6 * S + 10 * S

    # ── плитки протоколов (PremiumProtocolTile:528-580)
    f_lbl = F(SANSB, 16); f_bdg = F(DEJA, 11)
    tile_h = 76 * S; gap = 10 * S
    tw_tile = (x1 - x0 - gap) // 2
    def tile(px, py, label, badge, icf, selected=False, locked=False):
        bg = (41,201,101,46) if selected else (29,21,16,158)
        fr = nine(BTN, tw_tile, tile_h)
        cell = Image.new('RGBA', (tw_tile, tile_h), bg)
        cell.alpha_composite(fr)
        cd = ImageDraw.Draw(cell)
        tint = EMER if selected else GOLDM
        icf(cd, 14 * S, (tile_h - 22 * S) / 2, 22 * S, tint)
        # текстовая колонка: padding 14dp рамки + иконка 22 + padding 10dp с двух сторон + трейлинг 22
        tx = 14 * S + 22 * S + 10 * S
        avail = tw_tile - tx - 10 * S - 22 * S - 14 * S
        lcol = TXT if selected else TXTM
        bcol = EMER if selected else GOLDM
        # ⚠ ДЕФЕКТ №1: maxLines=1 без overflow → Clip (обрубание, без «…»)
        # autoSize: 10–16sp ярлык, 7–11sp бейдж — как в PremiumProtocolTile после правки.
        autosize_txt(cell, tx, (tile_h - 40 * S) / 2, label, lcol, avail, SANSB, 10, 16)
        autosize_txt(cell, tx, (tile_h - 40 * S) / 2 + 21 * S, badge, bcol, avail, DEJA, 7, 11)
        (ic_check if selected else ic_radio)(cd, tw_tile - 14 * S - 22 * S,
                                            (tile_h - 22 * S) / 2, 22 * S, tint)
        if locked:
            cell.putalpha(cell.getchannel('A').point(lambda a: round(a * .72)))
        lay.alpha_composite(cell, (round(px), round(py)))
    tile(x0, y, 'Авто', 'Рекомендуется', ic_bolt)
    tile(x0 + tw_tile + gap, y, 'VLESS', 'Оптимальный', ic_globe, selected=True)
    y += tile_h + gap
    tile(x0, y, 'Hysteria2', 'Самый быстрый', ic_bolt)
    tile(x0 + tw_tile + gap, y, 'NaiveProxy', '⚠ нестабильный', ic_globe)
    row2_y = y
    y += tile_h + gap

    # ── кнопка триала (MobilePremiumButton, PhoneRevolverMenu.kt:319)
    bh = 50 * S
    fr = nine(BTN, x1 - x0, bh)
    cell = Image.new('RGBA', (x1 - x0, bh), (0, 0, 0, 0)); cell.alpha_composite(fr)
    cd = ImageDraw.Draw(cell)
    f_btn = F(SANSB, 16)
    lw = f_btn.getlength('Попробовать 2 дня бесплатно')
    bx = (cell.width - (lw + 22 * S + 10 * S)) / 2
    ic_bolt(cd, bx, (bh - 22 * S) / 2, 22 * S, EMER)
    txt(cd, (bx + 32 * S, (bh - 20 * S) / 2), 'Попробовать 2 дня бесплатно', f_btn, TXT)
    lay.alpha_composite(cell, (round(x0), round(y)))

    # окно барабана: обрезаем всё за его границами (clipToBounds в TvHomeScreen)
    win = lay.crop((0, drum_t, W, win_b))
    lay = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    lay.paste(win, (0, drum_t))
    # ── маска-градиент ПОВЕРХ списка (PhoneRevolverMenu.kt) — накрывает ТОЛЬКО
    # прокручиваемую часть; статусная шапка выше и остаётся в полную яркость.
    g = vgrad(W, win_b - drum_t, [(0.0, WALNUT, 224), (0.13, WALNUT, 0),
                                  (0.84, LEATHER, 0), (1.0, LEATHER, 235)])
    lay.alpha_composite(g, (0, drum_t))
    ph.alpha_composite(lay)
    ph.alpha_composite(head)
    return ph, row2_y, win_t

# ═════════ ЭКРАН 2: оплата (BuyScreen.PhonePaymentContent) ═════════
def fake_qr(px=216):
    import random
    rnd = random.Random(72); n = 29; cell = px // n
    im = Image.new('RGB', (n * cell, n * cell), 'white'); p = im.load()
    def sq(x, y, s, v):
        for i in range(s * cell):
            for j in range(s * cell): p[x * cell + i, y * cell + j] = v
    for yy in range(n):
        for xx in range(n):
            if rnd.random() < .45: sq(xx, yy, 1, (12, 12, 12))
    for (fx, fy) in [(0, 0), (n - 7, 0), (0, n - 7)]:
        sq(fx, fy, 7, (12, 12, 12)); sq(fx + 1, fy + 1, 5, (255,) * 3); sq(fx + 2, fy + 2, 3, (12, 12, 12))
    return im.resize((px, px), Image.NEAREST)

def screen_pay():
    ph = cover(f'{str(RES)}/mobile_surface.webp', W, H).convert('RGBA')
    ph.alpha_composite(Image.new('RGBA', (W, H), (0, 0, 0, 51)))
    d = ImageDraw.Draw(ph)
    pad = 18 * S
    # заголовок MobilePremiumScreen
    ic_back(d, pad, 22 * S, 26 * S, GOLD)
    txt(d, (pad + 38 * S, 20 * S), 'Покупка', F(PLAY, 26), GOLD)
    # панель
    py = 72 * S; ph_h = H - py - 26 * S
    pan = nine(PANEL, W - 2 * pad, ph_h)
    inner = Image.new('RGBA', (W - 2 * pad, ph_h), (0, 0, 0, 0))
    inner.alpha_composite(pan)
    idr = ImageDraw.Draw(inner); cw = inner.width
    y = 26 * S
    txt(idr, (cw / 2, y), 'Оплата', F(PLAY, 23), GOLD, anchor='ma'); y += 32 * S
    txt(idr, (cw / 2, y), 'Сумма: 450 ₽', F(DEJAB, 20), EMER, anchor='ma'); y += 34 * S
    # белая карточка + QR 216dp (mobilePremiumPaymentQrSize)
    q = 216 * S; card = q + 24 * S
    cx = (cw - card) / 2
    idr.rounded_rectangle((cx, y, cx + card, y + card), radius=16 * S, fill=(255, 255, 255))
    inner.paste(fake_qr(q), (round(cx + 12 * S), round(y + 12 * S)))
    y += card + 12 * S
    f_h = F(SANS, 13.5)
    txt(idr, (cw / 2, y), 'Отсканируйте телефоном — откроется оплата', f_h, TXT, anchor='ma')
    y += 19 * S
    txt(idr, (cw / 2, y), '(СБП или картой, из любого банка)', f_h, TXT, anchor='ma'); y += 26 * S
    for label in ['Открыть страницу оплаты']:
        bh = 50 * S; bw = cw - 44 * S
        b = Image.new('RGBA', (bw, bh), (0, 0, 0, 0)); b.alpha_composite(nine(BTN, bw, bh))
        txt(ImageDraw.Draw(b), (bw / 2, (bh - 20 * S) / 2), label, F(SANSB, 16), TXT, anchor='ma')
        inner.alpha_composite(b, (round((cw - bw) / 2), round(y))); y += bh + 14 * S
    txt(idr, (cw / 2, y), 'Код заказа (укажите в сообщении к переводу):', f_h, TXT, anchor='ma')
    y += 22 * S
    txt(idr, (cw / 2, y), 'A1B2C3', F(PLAY, 26), EMER, anchor='ma'); y += 38 * S
    txt(idr, (cw / 2, y), 'Или вручную по СБП: +7 977 811-65-64', f_h, TXT, anchor='ma'); y += 30 * S
    bh = 50 * S; bw = 240 * S
    b = Image.new('RGBA', (bw, bh), (0, 0, 0, 0)); b.alpha_composite(nine(BTN, bw, bh))
    txt(ImageDraw.Draw(b), (bw / 2, (bh - 20 * S) / 2), 'Я оплатил', F(SANSB, 16), TXT, anchor='ma')
    inner.alpha_composite(b, (round((cw - bw) / 2), round(y))); y += bh + 14 * S
    f_m = F(SANS, 12.5)
    for line in ['После нажатия заявка уйдёт владельцу.',
                 'Подписка активируется после подтверждения —', 'оставьте экран открытым.']:
        txt(idr, (cw / 2, y), line, f_m, TXTM, anchor='ma'); y += 18 * S
    ph.alpha_composite(inner, (round(pad), round(py)))
    return ph

# ═════════ ЭКРАН 3: ошибка активации (ClaimScreen + MobilePremiumError) ═════════
def screen_err():
    ph = cover(f'{str(RES)}/mobile_surface.webp', W, H).convert('RGBA')
    ph.alpha_composite(Image.new('RGBA', (W, H), (0, 0, 0, 51)))
    d = ImageDraw.Draw(ph)
    pad = 18 * S
    ic_back(d, pad, 22 * S, 26 * S, GOLD)
    txt(d, (pad + 38 * S, 18 * S), 'Активация подписки', F(PLAY, 24), GOLD)
    # поле ввода MobilePremiumTextField (frame_bar) — ниже резного орнамента сцены
    y = 104 * S; fh = 54 * S
    fld = Image.new('RGBA', (W - 2 * pad, fh), (0, 0, 0, 0))
    fld.alpha_composite(nine(BAR, W - 2 * pad, fh))
    txt(ImageDraw.Draw(fld), (22 * S, (fh - 20 * S) / 2), 'Код или логин', F(SANS, 16), TXTM)
    ph.alpha_composite(fld, (round(pad), round(y)))
    y += fh + 16 * S
    # панель ошибки
    ph_h = 300 * S
    pan = Image.new('RGBA', (W - 2 * pad, ph_h), (0, 0, 0, 0))
    pan.alpha_composite(nine(PANEL, W - 2 * pad, ph_h))
    pd = ImageDraw.Draw(pan); cw = pan.width
    iy = 40 * S
    ic_err(pd, (cw - 52 * S) / 2, iy, 52 * S, RUBY); iy += 52 * S + 18 * S
    txt(pd, (cw / 2, iy), 'Ошибка: Код не найден', F(SANS, 17), RUBY, anchor='ma')
    iy += 26 * S + 18 * S
    bh = 50 * S; bw = cw - 44 * S
    b = Image.new('RGBA', (bw, bh), (0, 0, 0, 0)); b.alpha_composite(nine(BTN, bw, bh))
    txt(ImageDraw.Draw(b), (bw / 2, (bh - 20 * S) / 2), 'Повторить', F(SANSB, 16), TXT, anchor='ma')
    pan.alpha_composite(b, (round((cw - bw) / 2), round(iy)))
    ph.alpha_composite(pan, (round(pad), round(y)))
    return ph, y + 200 * S

# ═════════ сборка листа ═════════
def rounded(im, rad):
    m = Image.new('L', im.size, 0)
    ImageDraw.Draw(m).rounded_rectangle((0, 0, im.width - 1, im.height - 1), radius=rad, fill=255)
    o = im.copy(); o.putalpha(m); return o

home, row2_y, win_t = screen_home()
pay = screen_pay()
err, err_btn_y = screen_err()

GAP = 46 * S; MARG = 40 * S; TOPH = 150 * S; CAPH = 118 * S
sheet_w = MARG * 2 + W * 3 + GAP * 2
sheet_h = TOPH + H + CAPH + 30 * S
sheet = Image.new('RGB', (sheet_w, sheet_h), (13, 9, 6))
sd = ImageDraw.Draw(sheet)
# фон-подсветка
glow = Image.new('RGB', (sheet_w, sheet_h), (13, 9, 6))
ImageDraw.Draw(glow).ellipse((sheet_w * .15, -sheet_h * .35, sheet_w * .85, sheet_h * .45),
                             fill=(38, 27, 18))
sheet = Image.blend(sheet, glow.filter(ImageFilter.GaussianBlur(160)), .85)
sd = ImageDraw.Draw(sheet)

txt(sd, (sheet_w / 2, 34 * S), 'MaestroVPN — экраны телефона (симуляция)', F(PLAY, 30), GOLD, anchor='ma')
sub = [
 'Симуляция по числам из Kotlin. Фон, резные рамки и шрифт Playfair — подлинные ассеты',
 'репозитория; раскладка, цвета, тексты и геометрия — из TvHomeScreen.kt / PhoneRevolverMenu.kt /',
 'BuyScreen.kt / MobilePremium*.kt. Это НЕ скриншот: глаз статичен (в приложении моргает и следит',
 'за пальцем), барабан не крутится, суммы и QR условные. Доказательства поведения — только CI и устройство.']
yy = 78 * S
for line in sub:
    txt(sd, (sheet_w / 2, yy), line, F(SANS, 13), TXTM, anchor='ma'); yy += 19 * S

xs = [MARG, MARG + W + GAP, MARG + (W + GAP) * 2]
for x, im in zip(xs, [home, pay, err]):
    card = rounded(im, 38 * S)
    sd.rounded_rectangle((x - 3 * S, TOPH - 3 * S, x + W + 3 * S, TOPH + H + 3 * S),
                         radius=41 * S, fill=(58, 44, 28))
    sheet.paste(card, (round(x), round(TOPH)), card)

caps = [
 ('Главный экран', ['Сцена с живым глазом — подлинный арт: глаз моргает,',
                    'зрачок следит за пальцем, тап = подключение.',
                    'Ниже барабан-«револьвер»: центральный ряд плоский,',
                    'дальние наклоняются и гаснут; снэп к центру + хаптик,',
                    'при TalkBack — плоский список без наклонов.']),
 ('Оплата', ['BuyScreen, телефонная ветка. Панель на резной раме,',
             'белое поле сканирования — платёжный инвариант,',
             'QR 216dp считается от ширины панели.',
             'Данные, колбэки и опрос статуса не менялись —',
             'изменилось только оформление.']),
 ('Ошибка активации', ['ClaimScreen + MobilePremiumError: рубиновая иконка,',
                       'сообщение и кнопка повтора на резной раме.',
                       'retryLabel задан литералом «Повторить» — раньше брался',
                       'R.string.menu_redo и на нерусских локалях читался',
                       '«Redo» (строка undo/redo из редактора конфигов).'])]
cy0 = TOPH + H + 26 * S
for x, (title, lines) in zip(xs, caps):
    txt(sd, (x + W / 2, cy0), title, F(PLAY, 19), GOLD, anchor='ma')
    ly = cy0 + 30 * S
    for l in lines:
        txt(sd, (x + W / 2, ly), l, F(SANS, 12), TXTM, anchor='ma'); ly += 17 * S

sheet = sheet.resize((sheet_w // 2, sheet_h // 2), Image.LANCZOS)
sheet.save(OUT, 'PNG', optimize=True)
print('OK', OUT, f'{os.path.getsize(OUT)/1024:.0f} KB', sheet.size)
