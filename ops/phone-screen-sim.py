#!/usr/bin/env python3
"""Deterministic visual smoke test for the PHONE screens (390x844 dp @2x).

Телефонный близнец ops/tv-master-sim.py. Существует потому, что правило проекта
требует смотреть UI ГЛАЗАМИ ДО КОДА (CLAUDE.md, п.3 «UI = пиксель-в-пиксель»), а
локального Android SDK на S1 нет и эмулятора тоже: собрать APK можно только в CI,
~13 минут за прогон, и выгрузка APK ещё и падает при переполненной квоте артефактов.
Поэтому экран воспроизводится из ПОДЛИННЫХ ассетов репозитория и чисел из Kotlin.

Что берётся из репо (воспроизводимо, ничего не выдумано):
  mobile_4d/atlas_c_*.webp      — центральное освещение пятислойной 4D-сцены
  mobile_eye_open/squint/closed  — live anatomy plus textured dynamic lid coverage
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
 3. Это НЕ скриншот. Лист фаз глаза детерминированно показывает форму моргания,
    но не воспроизводит слежение за пальцем; барабан не крутится, суммы/QR условные.
    Для доказательств поведения — CI и
    устройство, симуляция только для глаз.

Использование:  ops/phone-screen-sim.py   ->  build/phone-screen-sim/phone-screens.png
"""
import os
import re
import sys
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont, ImageFilter
from PIL import ImageChops

ROOT = Path(__file__).resolve().parents[1]
RES = ROOT / 'app/src/main/res/drawable-nodpi'
ASSETS = ROOT / 'app/src/main/assets'
MOBILE_4D_MANIFEST = ROOT / 'app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/Mobile4DGeneratedAssets.kt'
OUTDIR = ROOT / 'build/phone-screen-sim'
OUTDIR.mkdir(parents=True, exist_ok=True)
OUT = str(OUTDIR / 'phone-screens.png')
S = 2  # супер-семплинг: работаем в 2x от dp
EYE_PHASES_ONLY = '--eye-phases-only' in sys.argv

PLAY = str(ROOT / 'app/src/main/res/font/playfair_display.ttf')

def existing_font(*candidates):
    """Keep S1 output unchanged, but allow the same smoke test to run on Windows Codex."""
    for candidate in candidates:
        if candidate and Path(candidate).is_file():
            return str(candidate)
    raise OSError(f'none of the required fonts exists: {candidates!r}')


windows_fonts = Path(os.environ.get('WINDIR', 'C:/Windows')) / 'Fonts'
SANS = existing_font(
    '/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf',
    windows_fonts / 'arial.ttf',
)
SANSB = existing_font(
    '/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf',
    windows_fonts / 'arialbd.ttf',
)
DEJA = existing_font(
    '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',
    windows_fonts / 'segoeui.ttf',
)
DEJAB = existing_font(
    '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf',
    windows_fonts / 'segoeuib.ttf',
)
MASTER_4D_SIZE = (2160, 4670)
FRAGMENT_RE = re.compile(
    r'Mobile4DAssetFragment\("[^"]+", "([^"]+)", (\d+), \d+, \d+, '
    r'Mobile4DAssetLight\.Centre, (\d+), "([^"]+)", '
    r'Mobile4DAssetRect\((\d+), (\d+), (\d+), (\d+)\), '
    r'Mobile4DAssetRect\((\d+), (\d+), (\d+), (\d+)\), \d+\),'
)
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

def centre_4d_scene():
    """Reconstruct the committed centre-light scene from generated atlas geometry."""
    fragments = []
    for match in FRAGMENT_RE.finditer(MOBILE_4D_MANIFEST.read_text(encoding='utf-8')):
        layer, z_order, page_index, page_path, *coords = match.groups()
        fragments.append((int(z_order), int(page_index), page_path, layer, *map(int, coords)))
    # 83 = 77 прежних + 6 фрагментов слоя `arc`, добавленного 2026-08-01. Число сверяется
    # намеренно: молчаливое расхождение манифеста и симуляции = симуляция начнёт врать.
    # 84 = 83 + фрагмент слоя `console`, подключённого 01.08.
    # 90 = 84 + фрагменты слоя `contacts`, подключённого 01.08.
    if len(fragments) != 90:
        raise ValueError(f'Expected 90 centre-light 4D fragments, found {len(fragments)}')

    scene = Image.new('RGBA', MASTER_4D_SIZE, (0, 0, 0, 0))
    current_path = None
    atlas = None
    for fragment in fragments:
        _, _, page_path, _, sx, sy, sw, sh, dx, dy, dw, dh = fragment
        if (sw, sh) != (dw, dh):
            raise ValueError(f'Atlas/scene rectangle mismatch in {page_path}')
        if page_path != current_path:
            if atlas is not None:
                atlas.close()
            atlas_path = ASSETS / page_path
            if not atlas_path.is_file():
                raise FileNotFoundError(f'Missing committed 4D atlas: {atlas_path}')
            atlas = Image.open(atlas_path).convert('RGBA')
            current_path = page_path
        if sx + sw > atlas.width or sy + sh > atlas.height:
            raise ValueError(f'Fragment outside atlas bounds in {page_path}')
        if dx + dw > MASTER_4D_SIZE[0] or dy + dh > MASTER_4D_SIZE[1]:
            raise ValueError(f'Fragment outside 4D master canvas in {page_path}')
        scene.alpha_composite(atlas.crop((sx, sy, sx + sw, sy + sh)), (dx, dy))
    if atlas is not None:
        atlas.close()
    return scene

def home_4d_viewport(w, h):
    scene = centre_4d_scene()
    k = max(w / scene.width, h / scene.height)
    scaled = scene.resize((round(scene.width * k), round(scene.height * k)), Image.Resampling.LANCZOS)
    scene.close()
    viewport = scaled.crop(((scaled.width - w) // 2, (scaled.height - h) // 2,
                            (scaled.width - w) // 2 + w, (scaled.height - h) // 2 + h))
    scaled.close()
    return viewport

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
    ярлыки секторов и плиток (PhoneHomeControlDeck.kt, ProtocolSector/HomeTile) — приём
    взят из component/NeonGlass.kt:228. Если строка не влезает даже на min_sp, Compose
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

# ═════════ дополнительные иконки под новую деку ═════════
def ic_phone(d, x, y, s, c):
    # ⛔ Здесь были два квадратика и дуга «трубки», которые на 22 dp рисовались как зелёная
    # точка и зелёная закорючка рядом с номером — владелец справедливо спросил, что это.
    # В приложении в этом месте стоит Material `Icons.Filled.Call`; рисуем узнаваемую трубку.
    w = max(2, round(s * .16))
    d.line((x+s*.20, y+s*.16, x+s*.34, y+s*.34), fill=c, width=w)
    d.line((x+s*.34, y+s*.34, x+s*.26, y+s*.50), fill=c, width=w)
    d.line((x+s*.26, y+s*.50, x+s*.50, y+s*.74), fill=c, width=w)
    d.line((x+s*.50, y+s*.74, x+s*.66, y+s*.66), fill=c, width=w)
    d.line((x+s*.66, y+s*.66, x+s*.84, y+s*.80), fill=c, width=w)
    d.line((x+s*.84, y+s*.80, x+s*.66, y+s*.94), fill=c, width=w)
    d.line((x+s*.66, y+s*.94, x+s*.20, y+s*.16), fill=None, width=1)
def ic_send(d, x, y, s, c):
    d.polygon([(x,y+s*.5),(x+s,y),(x+s*.42,y+s),(x+s*.34,y+s*.66)], fill=c)
def ic_forum(d, x, y, s, c):
    d.rounded_rectangle((x,y+s*.1,x+s*.74,y+s*.66), radius=s*.12, outline=c, width=max(2,round(s*.09)))
    d.rounded_rectangle((x+s*.26,y+s*.36,x+s,y+s*.92), radius=s*.12, fill=c)
def ic_chat(d, x, y, s, c):
    d.rounded_rectangle((x,y+s*.1,x+s,y+s*.76), radius=s*.14, fill=c)
    d.polygon([(x+s*.2,y+s*.72),(x+s*.46,y+s*.72),(x+s*.2,y+s)], fill=c)
def ic_auto(d, x, y, s, c):
    w = max(2, round(s*.12))
    d.arc((x+s*.08,y+s*.08,x+s*.92,y+s*.92), 40, 320, fill=c, width=w)
    d.polygon([(x+s*.62,y),(x+s*.98,y+s*.2),(x+s*.6,y+s*.34)], fill=c)
def ic_shield(d, x, y, s, c):
    d.polygon([(x+s*.5,y),(x+s*.96,y+s*.2),(x+s*.86,y+s*.72),(x+s*.5,y+s),
               (x+s*.14,y+s*.72),(x+s*.04,y+s*.2)], fill=c)
def ic_hub(d, x, y, s, c):
    circle(d, x+s*.5, y+s*.5, s*.17, c)
    for a in (0, 60, 120, 180, 240, 300):
        import math
        rad = math.radians(a)
        px, py = x+s*.5+math.cos(rad)*s*.36, y+s*.5+math.sin(rad)*s*.36
        d.line((x+s*.5, y+s*.5, px, py), fill=c, width=max(2, round(s*.07)))
        circle(d, px, py, s*.1, c)
def ic_video(d, x, y, s, c):
    d.rounded_rectangle((x,y+s*.24,x+s*.68,y+s*.78), radius=s*.1, fill=c)
    d.polygon([(x+s*.72,y+s*.44),(x+s,y+s*.26),(x+s,y+s*.76),(x+s*.72,y+s*.58)], fill=c)
def ic_gear(d, x, y, s, c):
    import math
    w = max(2, round(s*.13))
    d.ellipse((x+s*.14,y+s*.14,x+s*.86,y+s*.86), outline=c, width=w)
    d.ellipse((x+s*.38,y+s*.38,x+s*.62,y+s*.62), outline=c, width=max(2, round(s*.1)))
    for a in range(0, 360, 45):
        rad = math.radians(a)
        d.line((x+s*.5+math.cos(rad)*s*.34, y+s*.5+math.sin(rad)*s*.34,
                x+s*.5+math.cos(rad)*s*.5, y+s*.5+math.sin(rad)*s*.5), fill=c, width=w)
def ic_smartphone(d, x, y, s, c):
    d.rounded_rectangle((x+s*.24,y,x+s*.76,y+s), radius=s*.1, outline=c, width=max(2,round(s*.1)))
    d.line((x+s*.42,y+s*.86,x+s*.58,y+s*.86), fill=c, width=max(2,round(s*.08)))

# ═════════ ЭКРАН 1: главный — дека по эталону владельца ═════════
# ⛔ Числа НЕ выдуманы: 1:1 из PhoneHomeReferenceLayout.kt (границы, измеренные по
# design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg) и из констант
# PhoneHomeControlDeck.kt. Меняешь Kotlin — меняй здесь, иначе симуляция начнёт врать.
W, H = 390 * S, 844 * S
REF_JPG = ROOT / 'design/mobile-4d-references/08-owner-installed-test-home-2026-08-08.jpg'

HERO_TRANSLATION_Y = -58.0          # PhoneHomeReferenceLayout.HeroTranslationY
DECK_TOP = 434.0                    # PhoneHomeReferenceLayout.DeckTop
MOBILE_HOME_LOWER_DECK_SHIFT_DP = 25.0  # Mobile4DHome.kt
B = {                               # PhoneHomeReferenceLayout: границы эталона в dp
    'title':        (69.0,  54.0, 323.0,  88.0),
    'medallion':    (26.0, 104.0, 364.0, 413.0),
    'status':       (128.0, 434.0, 265.0, 456.0),
    'activeProtocol': (126.0, 456.0, 266.0, 476.0),
    'phone':        (81.0, 478.0, 310.0, 516.0),
    'contacts':     (34.0, 511.0, 356.0, 594.0),
    'protocolArc':  (0.0,  595.0, 390.0, 730.0),
    'buy':          (81.0, 724.0, 309.0, 769.0),
    'bottomConsole': (8.0, 760.0, 382.0, 864.0),
}
CONTACT_GAP = 10.0                  # PhoneHomeControlDeck.CONTACT_GAP
# Ячейки резного веера: центры заданы владельцем, провис снят с самого home_arc_c.png.
# ⛔ Не путать с силуэтом дуги — он провисает на 39.8 dp, ячейки только на ~12.
# ⛔ Замер, а не номинал: интерьеры ячеек 40…47 dp, коробка 52 выезжала на разделитель.
ARC_SECTOR_CENTERS = [37.8, 88.3, 141.7, 195.6, 248.2, 301.4, 352.4]
ARC_CELL_WIDTHS = [40.1, 47.0, 45.7, 47.3, 45.7, 46.4, 40.7]
ARC_CELL_TOPS = [644.7, 628.6, 618.7, 627.0, 618.1, 627.9, 644.4]
ARC_CELL_HEIGHTS = [67.2, 77.2, 83.1, 73.6, 83.7, 77.7, 67.4]
CONSOLE_LEFT = (32.0, 782.4, 139.0, 842.4)
CONSOLE_DIAL = (160.0, 774.7, 228.0, 848.0)
CONSOLE_RIGHT = (250.0, 782.6, 357.0, 842.4)
SELECTION_BAR_WIDTH = 22.0
TITLE_SP = 38          # Mobile4DHome.TITLE_MAX_FONT_SIZE
TITLE_TRACK = 3.5      # Mobile4DHome.TITLE_LETTER_SPACING
TITLE_FACE_TOP, TITLE_FACE_BOT = (228,214,180), (140,122,85)   # Mobile4DHome.TITLE_FACE
TITLE_CARVE = (36,26,16)      # Mobile4DHome.TITLE_CARVE
TITLE_RIM = (255,240,200)     # Mobile4DHome.TITLE_RIM (alpha 0x66)
TITLE_CARVE_DEPTH, TITLE_RIM_LIFT = 1.6, 0.9

# Сектора дуги: тег → (подпись эталона, иконка). WDTT (`vk-turn`) на эталоне не нарисован,
# но на телефоне это живой протокол (прячет его только ТВ), поэтому сектор ему выделен.
ARC_PROTOCOLS = [
    ('auto', 'АВТО', ic_auto),
    ('vless', 'VLESS', ic_shield),
    ('hysteria2', 'HYSTERIA2', ic_bolt),
    ('anytls', 'ANYTLS', ic_lock),
    ('naive', 'NAIVE\nPROXY', ic_hub),
    ('vk-turn', 'WDTT', ic_video),
    ('olcrtc', 'WEBRTC', ic_globe),
]

def centre_4d_fragments():
    """Читает тот же манифест, что runtime, и возвращает фрагменты в z-порядке."""
    fragments = []
    for match in FRAGMENT_RE.finditer(MOBILE_4D_MANIFEST.read_text(encoding='utf-8')):
        layer, z_order, page_index, page_path, *coords = match.groups()
        fragments.append((int(z_order), int(page_index), page_path, layer, *map(int, coords)))
    # 83 = 77 прежних + 6 фрагментов слоя `arc`, добавленного 2026-08-01. Число сверяется
    # намеренно: молчаливое расхождение манифеста и симуляции = симуляция начнёт врать.
    # 84 = 83 + фрагмент слоя `console`, подключённого 01.08.
    # 90 = 84 + фрагменты слоя `contacts`, подключённого 01.08.
    if len(fragments) != 90:
        raise ValueError(f'Expected 90 centre-light 4D fragments, found {len(fragments)}')
    return sorted(fragments)

def centre_4d_layer(fragments, layer_name):
    """Реконструирует ровно один relief-слой, не сливая fixed и scrolling ownership."""
    master = Image.new('RGBA', MASTER_4D_SIZE, (0, 0, 0, 0))
    atlases = {}
    for fragment in (item for item in fragments if item[3] == layer_name):
        _, _, page_path, layer, sx, sy, sw, sh, dx, dy, dw, dh = fragment
        if (sw, sh) != (dw, dh):
            raise ValueError(f'Atlas/scene rectangle mismatch in {page_path}')
        atlas = atlases.get(page_path)
        if atlas is None:
            atlas_path = ASSETS / page_path
            if not atlas_path.is_file():
                raise FileNotFoundError(f'Missing committed 4D atlas: {atlas_path}')
            atlas = Image.open(atlas_path).convert('RGBA')
            atlases[page_path] = atlas
        crop = atlas.crop((sx, sy, sx + sw, sy + sh))
        master.alpha_composite(crop, (dx, dy))
        crop.close()
    for atlas in atlases.values():
        atlas.close()
    return master

def _fit(master, w, h):
    k = max(w / master.width, h / master.height)
    scaled = master.resize((round(master.width * k), round(master.height * k)), Image.Resampling.LANCZOS)
    left, top = (scaled.width - w) // 2, (scaled.height - h) // 2
    out = scaled.crop((left, top, left + w, top + h))
    scaled.close()
    return out

def home_scene(w, h, deck_scroll_dp=0.0):
    """Ровно runtime ownership: fixed hero сверху, три relief-слоя движутся с декой."""
    fragments = centre_4d_fragments()
    layer_order = []
    for _, _, _, layer, *_ in fragments:
        if layer not in layer_order:
            layer_order.append(layer)
    expected = ['wood', 'console', 'contacts', 'frame', 'cartouche', 'vines', 'arc', 'ring']
    if layer_order != expected:
        raise ValueError(f'Unexpected 4D layer order: {layer_order!r}')

    scene = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    deck_layers = {'console', 'contacts', 'arc'}
    for layer in layer_order:
        master = centre_4d_layer(fragments, layer)
        fitted = _fit(master, w, h)
        master.close()
        dy = 0
        if layer == 'ring':
            dy = round(HERO_TRANSLATION_Y * S)
        elif layer in deck_layers:
            dy = round((MOBILE_HOME_LOWER_DECK_SHIFT_DP - deck_scroll_dp) * S)

        positioned = Image.new('RGBA', (w, h), (0, 0, 0, 0))
        positioned.alpha_composite(fitted, (0, dy))
        fitted.close()
        if layer in deck_layers:
            clip_y = round(DECK_TOP * S)
            visible = positioned.crop((0, clip_y, w, h))
            scene.alpha_composite(visible, (0, clip_y))
            visible.close()
        else:
            scene.alpha_composite(positioned)
        positioned.close()
    return scene

LIVING_EYE_BRONZE_INSET_FRACTION = 26.0 / 520.0   # LivingEyeLayerGeometry.kt
LIVING_EYE_STATE_W, LIVING_EYE_STATE_H = 890.0, 635.0
LIVING_EYE_VIRTUAL_SIZE = 822.5
LIVING_EYE_ANATOMY_SCALE = 1.10
LIVING_EYE_OFFSET_X_FRACTION = 3.5 / 238.0
LIVING_EYE_OFFSET_Y_FRACTION = 7.0 / 238.0
LIVING_EYE_SEAM_FROM_UPPER = 0.70
LIVING_EYE_CONTACT_SHADOW_FRACTION = 3.0 / 520.0
LIVING_EYE_CONTACT_SHADOW_ALPHA = 0.18

def eye_box():
    """Коробка живого глаза = medallion* из Mobile4DSceneModel.kt + общий сдвиг героя.

    ⛔ ЛОВУШКА: это НЕ `B['medallion']`. Границы эталона описывают ВНЕШНЕЕ бронзовое
    кольцо (338 dp), а LivingEyeMedallion вписывает ассет 890×635 в квадрат
    2*radius (238 dp) минус бронзовый инсет. Если взять эталонные — глаз вылезет
    из кольца на 42%."""
    sc = max(390.0 / MASTER_4D_SIZE[0], 844.0 / MASTER_4D_SIZE[1])
    tx = (390.0 - MASTER_4D_SIZE[0] * sc) / 2
    ty = (844.0 - MASTER_4D_SIZE[1] * sc) / 2
    cx = 1080.0 * sc + tx
    cy = 1751.0 * sc + ty + HERO_TRANSLATION_Y
    size = min((MASTER_4D_SIZE[0] * 260 / 853) * sc, (MASTER_4D_SIZE[1] * 260 / 1844) * sc) * 2
    state_w = size * LIVING_EYE_STATE_W / LIVING_EYE_VIRTUAL_SIZE * LIVING_EYE_ANATOMY_SCALE
    state_h = size * LIVING_EYE_STATE_H / LIVING_EYE_VIRTUAL_SIZE * LIVING_EYE_ANATOMY_SCALE
    return cx, cy, size, state_w, state_h
APERTURE_UPPER = [
    (388,1083),(405,1061),(430,1037),(460,1014),(500,993),(540,978),
    (580,968),(620,961),(660,957),(700,957),(740,962),(780,973),
    (820,990),(860,1011),(900,1036),(932,1061),(957,1083),
]
APERTURE_LOWER = [
    (388,1083),(420,1104),(460,1123),(500,1139),(540,1152),(580,1162),
    (620,1170),(660,1174),(700,1172),(740,1167),(780,1159),(820,1148),
    (860,1133),(900,1115),(932,1098),(957,1083),
]

def _interpolate_contour_y(points, x):
    """Linear Y at X for a monotonic contour; both contours use their union of X values."""
    if x <= points[0][0]:
        return float(points[0][1])
    if x >= points[-1][0]:
        return float(points[-1][1])
    for (x0, y0), (x1, y1) in zip(points, points[1:]):
        if x0 <= x <= x1:
            if x1 == x0:
                return float(y1)
            t = (x - x0) / (x1 - x0)
            return y0 + (y1 - y0) * t
    raise AssertionError(f'contour does not cover x={x}')


APERTURE_SAMPLES = tuple(
    (x, _interpolate_contour_y(APERTURE_UPPER, x),
     _interpolate_contour_y(APERTURE_LOWER, x))
    for x in sorted({p[0] for p in APERTURE_UPPER} | {p[0] for p in APERTURE_LOWER})
)


def _living_eye_contours(phase, state_left, state_top, state_px):
    """Upper/lower aperture contours converging on a 70/30 shared seam."""
    phase = max(0.0, min(1.0, float(phase)))
    upper, lower = [], []
    for x, source_upper_y, source_lower_y in APERTURE_SAMPLES:
        seam_y = source_upper_y + (source_lower_y - source_upper_y) * LIVING_EYE_SEAM_FROM_UPPER
        current_upper_y = source_upper_y + (seam_y - source_upper_y) * phase
        current_lower_y = source_lower_y + (seam_y - source_lower_y) * phase
        px = state_left + (x - 230.0) / LIVING_EYE_STATE_W * state_px[0]
        upper.append((px, state_top + (current_upper_y - 745.0) / LIVING_EYE_STATE_H * state_px[1]))
        lower.append((px, state_top + (current_lower_y - 745.0) / LIVING_EYE_STATE_H * state_px[1]))
    return upper, lower


def _living_eye_components(phase):
    """Live anatomy plus opaque squint/closed textures clipped by moving 70/30 lids."""
    phase = max(0.0, min(1.0, float(phase)))
    cx, cy, size, state_w, state_h = eye_box()
    canvas_px = round(size * S)
    state_px = (round(state_w * S), round(state_h * S))
    state_left = round(
        (canvas_px - state_px[0]) / 2 + canvas_px * LIVING_EYE_OFFSET_X_FRACTION)
    state_top = round(
        (canvas_px - state_px[1]) / 2 + canvas_px * LIVING_EYE_OFFSET_Y_FRACTION)
    owner_state_px = (
        round(size * S * (LIVING_EYE_STATE_W / LIVING_EYE_VIRTUAL_SIZE)
              * LIVING_EYE_ANATOMY_SCALE),
        round(size * S * (LIVING_EYE_STATE_H / LIVING_EYE_VIRTUAL_SIZE)
              * LIVING_EYE_ANATOMY_SCALE),
    )
    owner_state_origin = (
        round((canvas_px - owner_state_px[0]) / 2
              + canvas_px * LIVING_EYE_OFFSET_X_FRACTION),
        round((canvas_px - owner_state_px[1]) / 2
              + canvas_px * LIVING_EYE_OFFSET_Y_FRACTION),
    )
    assert state_px == owner_state_px, (
        'simulator eye geometry is stale: state_px must mirror the owner-approved '
        f'uniform anatomy scale; expected={owner_state_px}, actual={state_px}'
    )
    assert (state_left, state_top) == owner_state_origin, (
        'simulator eye geometry is stale: state origin must mirror the owner-approved '
        f'offset; expected={owner_state_origin}, actual={(state_left, state_top)}'
    )

    open_upper, open_lower = _living_eye_contours(0.0, state_left, state_top, state_px)
    upper, lower = _living_eye_contours(phase, state_left, state_top, state_px)
    open_aperture = Image.new('L', (canvas_px, canvas_px), 0)
    ImageDraw.Draw(open_aperture).polygon(
        open_upper + list(reversed(open_lower)), fill=255)

    aperture = Image.new('L', (canvas_px, canvas_px), 0)
    if phase < 0.999:
        ImageDraw.Draw(aperture).polygon(upper + list(reversed(lower)), fill=255)

    eye_layer = Image.new('RGBA', (canvas_px, canvas_px), (0, 0, 0, 0))
    if phase < 0.999:
        source = Image.open(RES / 'mobile_eye_open.webp').convert('RGBA').resize(
            state_px, Image.Resampling.LANCZOS)
        eye_layer.alpha_composite(source, (state_left, state_top))
        source.close()
        eye_layer.putalpha(ImageChops.multiply(eye_layer.getchannel('A'), aperture))

    seam_width = max(1, round(canvas_px * LIVING_EYE_CONTACT_SHADOW_FRACTION))
    coverage = Image.new('L', (canvas_px, canvas_px), 0)
    if phase >= 0.999:
        coverage = open_aperture.copy()
    elif phase > 0.0:
        eroded_aperture = aperture.filter(ImageFilter.MinFilter(seam_width * 2 + 1))
        coverage = ImageChops.subtract(open_aperture, eroded_aperture)
        eroded_aperture.close()
    if coverage.getbbox() is not None:
        lids = Image.new('RGBA', eye_layer.size, (0, 0, 0, 0))
        for texture_name in ('mobile_eye_squint.webp', 'mobile_eye_closed.webp'):
            source = Image.open(RES / texture_name).convert('RGBA').resize(
                state_px, Image.Resampling.LANCZOS)
            lids.alpha_composite(source, (state_left, state_top))
            source.close()
        lids.putalpha(ImageChops.multiply(lids.getchannel('A'), coverage))
        eye_layer.alpha_composite(lids)
        lids.close()
    coverage.close()

    seam = Image.new('RGBA', eye_layer.size, (0, 0, 0, 0))
    seam_draw = ImageDraw.Draw(seam, 'RGBA')
    seam_rgb = (6, 20, 9)  # LivingEyeMedallion.kt EYE_CONTACT_SHADOW = #061409
    upper_colour = (*seam_rgb, round(255 * LIVING_EYE_CONTACT_SHADOW_ALPHA))
    lower_alpha = round(255 * LIVING_EYE_CONTACT_SHADOW_ALPHA * (1.0 - phase))
    seam_draw.line(upper, fill=upper_colour, width=seam_width, joint='curve')
    if lower_alpha > 0:
        seam_draw.line(lower, fill=(*seam_rgb, lower_alpha), width=seam_width, joint='curve')

    x = round(cx * S - canvas_px / 2)
    y = round(cy * S - canvas_px / 2)
    return eye_layer, seam, aperture, open_aperture, x, y


def living_eye_phase_layer(phase):
    eye_layer, seam, aperture, open_aperture, x, y = _living_eye_components(phase)
    canvas = Image.new('RGBA', eye_layer.size, (0, 0, 0, 0))
    canvas.alpha_composite(eye_layer)
    canvas.alpha_composite(seam)
    eye_layer.close()
    seam.close()
    aperture.close()
    open_aperture.close()
    return canvas, x, y


def living_eye_layer(state):
    """Static state sampled from the same one-layer aperture animation used at runtime."""
    phase = {'connected': 0.0, 'connecting': 0.5, 'disconnected': 1.0}[state]
    return living_eye_phase_layer(phase)


def tile(layer, x, y, w, h, label, icf, *, selected=False, locked=False,
         icon_sp=22, label_min=8, label_max=12, gap=6, bar=False):
    """Резная плитка: nine-patch frame_button + иконка сверху + подпись снизу.
    Это ровно HomeTile/ProtocolSector из PhoneHomeControlDeck.kt."""
    w, h = round(w), round(h)
    cell = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    cell.alpha_composite(nine(BTN, w, h))
    cd = ImageDraw.Draw(cell)
    icon = round(icon_sp * S)
    bar_h = round(5 * S) if bar else 0
    block = icon + round(gap * S) + round(label_max * S) + bar_h
    iy = (h - block) / 2
    tint = EMER if selected else GOLD
    icf(cd, (w - icon) / 2, iy, icon, tint)
    ly = iy + icon + gap * S
    col = TXT
    if bar:
        col = TXT if selected else GOLDM
    lines = label.split('\n')
    sp = label_max
    for line in lines:
        sp = min(sp, autosize_probe(line, w - 8 * S, SANSB, label_min, label_max))
    for i, line in enumerate(lines):
        clip_txt_centered(cell, w / 2, ly + i * sp * 1.15 * S, line, F(SANSB, sp), col, round(w - 8 * S))
    if bar and selected:
        by = ly + len(lines) * sp * 1.15 * S + 3 * S
        bw = SELECTION_BAR_WIDTH * S
        cd.rounded_rectangle(((w - bw) / 2, by, (w + bw) / 2, by + 2 * S), radius=S, fill=EMER)
    if locked:
        cell.putalpha(cell.getchannel('A').point(lambda a: round(a * .72)))
    layer.alpha_composite(cell, (round(x), round(y)))

def autosize_probe(s, maxw, path, min_sp, max_sp, step_sp=0.5):
    """Кегль, при котором строка влезает в maxw — TextAutoSize.StepBased."""
    sp = max_sp
    while sp > min_sp:
        if F(path, sp).getlength(s) <= maxw:
            break
        sp -= step_sp
    return sp

def clip_txt_centered(layer, cx, y, s, font, fill, maxw):
    clip_txt(layer, cx - min(font.getlength(s), maxw) / 2, y, s, font, fill, maxw, ellipsis=False)

def autosize_centered(layer, cx, y, s, fill, maxw, path, min_sp, max_sp, step_sp=0.5):
    """autosize_txt, но по центру — TextAlign.Center у BasicText в деке."""
    sp = autosize_probe(s, maxw, path, min_sp, max_sp, step_sp)
    clip_txt_centered(layer, cx, y, s, F(path, sp), fill, round(maxw))
    return sp

def pill(layer, x, y, w, h, label, icf, icon_tint):
    """MobilePremiumButton: рама frame_button, иконка + подпись в строку по центру."""
    w, h = round(w), round(h)
    cell = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    cell.alpha_composite(nine(BTN, w, h))
    cd = ImageDraw.Draw(cell)
    f = F(SANSB, 16)
    icon = round(22 * S)
    total = f.getlength(label) + icon + 10 * S
    bx = (w - total) / 2
    icf(cd, bx, (h - icon) / 2, icon, icon_tint)
    txt(cd, (bx + icon + 10 * S, (h - 20 * S) / 2), label, f, TXT)
    layer.alpha_composite(cell, (round(x), round(y)))

def screen_home(state='connected', deck_scroll_dp=0.0):
    """state: connected | connecting | disconnected."""
    ph = home_scene(W, H, deck_scroll_dp).convert('RGBA')

    # ── глаз и титул fixed: они не принадлежат scroll-owner нижней деки.
    eye, eye_x, eye_y = living_eye_layer(state)
    ph.alpha_composite(eye, (eye_x, eye_y))
    eye.close()


    lay = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(lay)

    # ── титул: кодовый Playfair по измеренным границам эталона
    tl, tt, tr, tb = B['title']
    # Объёмный титул: прорезь снизу, светлая кромка сверху, градиент по грани — те же три
    # слоя, что и в Mobile4DHome. PIL не умеет letterSpacing, поэтому шаг задаём вручную.
    tf = F(PLAY, TITLE_SP)
    text = 'MaestroVPN'
    adv = [tf.getlength(ch) for ch in text]
    total = sum(adv) + TITLE_TRACK * S * (len(text) - 1)
    tx0 = ((tl + tr) / 2) * S - total / 2
    ty0 = ((tt + tb) / 2) * S

    def draw_title(layer_d, dy, fill):
        x = tx0
        for ch, w in zip(text, adv):
            layer_d.text((x, ty0 + dy), ch, font=tf, fill=fill, anchor='lm')
            x += w + TITLE_TRACK * S

    draw_title(d, TITLE_CARVE_DEPTH * S, TITLE_CARVE)
    rim = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    draw_title(ImageDraw.Draw(rim), -TITLE_RIM_LIFT * S, TITLE_RIM + (0x66,))
    lay.alpha_composite(rim); rim.close()
    face_mask = Image.new('L', (W, H), 0)
    draw_title(ImageDraw.Draw(face_mask), 0, 255)
    bb = face_mask.getbbox()
    if bb:
        grad = Image.new('RGB', (1, max(1, bb[3] - bb[1])))
        gp = grad.load()
        for i in range(grad.height):
            t = i / max(1, grad.height - 1)
            gp[0, i] = tuple(round(a + (b - a) * t) for a, b in zip(TITLE_FACE_TOP, TITLE_FACE_BOT))
        face = Image.new('RGBA', (W, H), (0, 0, 0, 0))
        face.paste(grad.resize((bb[2] - bb[0], bb[3] - bb[1])), (bb[0], bb[1]))
        face.putalpha(face_mask)
        lay.alpha_composite(face); face.close()
    ph.alpha_composite(lay)
    lay.close()

    # Всё ниже deckTop — один scroll-owner. Его текст, hit-area и резной relief
    # двигаются на одинаковое число dp и клипуются под неподвижным героем.
    lay = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(lay)


    # ── статус + активный протокол (PhoneStatusRow, вне маски — маски больше нет вовсе)
    label = {'connected': 'ПОДКЛЮЧЕНО', 'connecting': 'ПОДКЛЮЧЕНИЕ',
             'disconnected': 'ОТКЛЮЧЕНО'}[state]
    # Промежуточное состояние — свой цвет, не цвет отказа (см. PhoneHomeControlDeck).
    col = {'connected': NEONG, 'connecting': ORANGE, 'disconnected': STATERED}[state]
    # ⛔ Полосы статуса и протокола стоят по СВОИМ границам эталона (363–386 и 386–406).
    # У прежнего PhoneStatusRow была своя вертикаль (padding 6 + spacer 8), и строка
    # протокола уезжала на 398–418 — под телефонную пилюлю, начинающуюся с 402.
    f_st, f_pr = F(SANSB, 16), F(SANSB, 14)
    sl, st_, sr, sb = B['status']
    tw = f_st.getlength(label)
    dot_x = (W - (tw + 11 * S + 9 * S)) / 2
    scy = (st_ + sb) / 2 * S
    circle(d, dot_x + 5.5 * S, scy, 5.5 * S, col)
    txt(d, (dot_x + 20 * S, scy), label, f_st, col, anchor='lm')
    proto = {
        'connected': 'Подключён: VLESS',
        'connecting': 'Подключение: VLESS',
        'disconnected': 'Отключён: VLESS',
    }[state]
    _, apt, _, apb = B['activeProtocol']
    txt(d, (W / 2, (apt + apb) / 2 * S), proto, f_pr, ORANGE, anchor='mm')

    # ── телефон поддержки (орнамент 38 dp, цель нажатия 48 dp)
    pl, pt, pr_, pb = B['phone']
    ph_h = pb - pt
    pill(lay, pl * S, pt * S, (pr_ - pl) * S, ph_h * S,
         '8 977 811-65-64', ic_phone, EMER)


    # ── три контакта: резьбу даёт слой `contacts` атласа, плитки идут без своей рамки,
    # границы плит — замер по home_contacts_c.png (ряд НЕ делится на три равные части).
    CONTACT_PLATES = [(34.1, 134.5), (144.8, 245.2), (255.5, 355.7)]  # по альфе слоя
    CONTACT_TOP, CONTACT_BOTTOM = 521.0, 585.0
    CONTACT_ICONS = ['contact_telegram', 'contact_max', 'contact_whatsapp']
    for (px0, px1), name, icon_name in zip(CONTACT_PLATES,
                                           ['Telegram', 'МАКС', 'WhatsApp'], CONTACT_ICONS):
        pw, ph_ = px1 - px0, CONTACT_BOTTOM - CONTACT_TOP
        cell = Image.new('RGBA', (round(pw * S), round(ph_ * S)), (0, 0, 0, 0))
        # ⛔ Фирменные иконки из кита владельца, а не Material-глифы: те давали плоский
        # треугольник вместо самолётика и два квадратика вместо WhatsApp.
        ic = Image.open(RES / f'{icon_name}.webp').convert('RGBA')
        isz = round(26 * S)
        ic = ic.resize((isz, isz), Image.LANCZOS)
        block = isz + round(6 * S) + round(10.5 * S)
        iy = round((cell.height - block) / 2)
        cell.alpha_composite(ic, ((cell.width - isz) // 2, iy))
        ic.close()
        autosize_centered(cell, cell.width / 2, iy + isz + 6 * S, name, TXT,
                          cell.width - 8 * S, SANSB, 8, 10.5)
        lay.alpha_composite(cell, (round(px0 * S), round(CONTACT_TOP * S)))

    # ── сектора протоколов: резьбу рисует АРТ (слой `arc` атласа), сюда идут только
    # подпись, иконка и отметка выбора. Своей рамки у сектора больше нет — два канта
    # друг на друге и были «двойным хозяином» зоны.
    for i, (tag, name, icf) in enumerate(ARC_PROTOCOLS):
        cx, cw = ARC_SECTOR_CENTERS[i], ARC_CELL_WIDTHS[i]
        y, ch = ARC_CELL_TOPS[i], ARC_CELL_HEIGHTS[i]
        x = cx - cw / 2
        sel = tag == 'vless'
        cell = Image.new('RGBA', (round(cw * S), round(ch * S)), (0, 0, 0, 0))
        cd = ImageDraw.Draw(cell)
        tint = EMER if sel else GOLD
        icon = round(20 * S)
        # содержимое центрируется в ИЗМЕРЕННОМ интерьере, а не прижимается к верху коробки
        block = icon + round(4 * S) + round(11 * S) + (round(5 * S) if sel else 0)
        iy = (cell.height - block) / 2
        icf(cd, (cell.width - icon) / 2, iy, icon, tint)
        sp = autosize_centered(cell, cell.width / 2, iy + icon + 4 * S, name,
                               TXT if sel else GOLDM, cell.width - 6 * S, SANSB, 7, 11)
        if sel:
            by = iy + icon + 4 * S + sp * 1.15 * S + 3 * S
            bw = SELECTION_BAR_WIDTH * S
            cd.rounded_rectangle(((cell.width - bw) / 2, by, (cell.width + bw) / 2, by + 2 * S),
                                 radius=S, fill=EMER)
        if tag == 'olcrtc':
            cell.putalpha(cell.getchannel('A').point(lambda a: round(a * .72)))
        lay.alpha_composite(cell, (round(x * S), round(y * S)))

    # ── купить подписку
    bl, bt, br, bb = B['buy']
    bh = bb - bt
    pill(lay, bl * S, bt * S, (br - bl) * S, bh * S,
         'Купить подписку', ic_cart, GOLD)

    # ── нижняя консоль
    ll, lt, lr, lb = CONSOLE_LEFT
    tile(lay, ll * S, lt * S, (lr - ll) * S, (lb - lt) * S,
         'Ввести логин', ic_person, icon_sp=22, label_min=8, label_max=12)
    rl, rt, rr, rb = CONSOLE_RIGHT
    tile(lay, rl * S, rt * S, (rr - rl) * S, (rb - rt) * S,
         'Подключить\nтелефон', ic_smartphone, icon_sp=22, label_min=8, label_max=12)
    dial_left, dial_top, dial_right, dial_bottom = CONSOLE_DIAL
    dial = min(dial_right - dial_left, dial_bottom - dial_top)
    dx = dial_left + (dial_right - dial_left - dial) / 2
    dy = dial_top + (dial_bottom - dial_top - dial) / 2
    dl = Image.new('RGBA', (round(dial * S), round(dial * S)), (0, 0, 0, 0))
    dd = ImageDraw.Draw(dl, 'RGBA')
    dd.ellipse((0, 0, dial * S - 1, dial * S - 1), fill=LEATHER + (255,),
               outline=GOLDM, width=round(2 * S))
    icon = round(24 * S)
    block = icon + 4 * S + 12 * S
    iy = (dial * S - block) / 2
    ic_gear(dd, (dial * S - icon) / 2, iy, icon, GOLD)
    autosize_centered(dl, dial * S / 2, iy + icon + 4 * S, 'Тест сети', TXT,
                      dial * S - 16 * S, SANSB, 8, 12)
    lay.alpha_composite(dl, (round(dx * S), round(dy * S)))
    dl.close()

    moved = Image.new('RGBA', (W, H), (0, 0, 0, 0))
    moved.alpha_composite(lay, (0, -round(deck_scroll_dp * S)))
    clip_y = round(DECK_TOP * S)
    visible = moved.crop((0, clip_y, W, H))
    ph.alpha_composite(visible, (0, clip_y))
    visible.close()
    moved.close()
    lay.close()
    return ph


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

# ═════════ сборка листов ═════════
def rounded(im, rad):
    m = Image.new('L', im.size, 0)
    ImageDraw.Draw(m).rounded_rectangle((0, 0, im.width - 1, im.height - 1), radius=rad, fill=255)
    o = im.copy(); o.putalpha(m); return o

STATES = ('connected', 'connecting', 'disconnected')
STATE_RU = {'connected': 'ПОДКЛЮЧЕНО — глаз открыт',
            'connecting': 'ПОДКЛЮЧЕНИЕ — глаз полуоткрыт',
            'disconnected': 'ОТКЛЮЧЕНО — глаз полностью закрыт'}

homes = {}
SCROLL_PROOF_DP = 64.0
SCROLLED = str(OUTDIR / 'owner-home-connected-scrolled.png')
if not EYE_PHASES_ONLY:
    for st in STATES:
        im = screen_home(st)
        homes[st] = im
        im.convert('RGB').save(OUTDIR / f'owner-home-{st}.png', 'PNG', optimize=True)
    home_scrolled = screen_home('connected', SCROLL_PROOF_DP)
    home_scrolled.convert('RGB').save(SCROLLED, 'PNG', optimize=True)
else:
    home_scrolled = None

# ── детерминированный close-up пяти фаз: live anatomy + opaque textured lids
EYE_PHASES = (0.0, 0.25, 0.5, 0.75, 1.0)
EYE_PHASE_CARD = 360
EYE_PHASE_GAP = 16
EYE_PHASE_MARGIN = 24
EYE_PHASE_HEADER = 86
eye_phase_sheet = Image.new(
    'RGB',
    (EYE_PHASE_MARGIN * 2 + EYE_PHASE_CARD * len(EYE_PHASES)
     + EYE_PHASE_GAP * (len(EYE_PHASES) - 1), EYE_PHASE_HEADER + EYE_PHASE_CARD + 56),
    (13, 9, 6),
)
eye_phase_draw = ImageDraw.Draw(eye_phase_sheet)
txt(eye_phase_draw, (eye_phase_sheet.width / 2, 18),
    'Живой глаз — непрозрачные динамические веки 70/30', F(PLAY, 18), GOLD, anchor='ma')
txt(eye_phase_draw, (eye_phase_sheet.width / 2, 50),
    'Open-анатомия остаётся живой; squint/closed дают только фактуру движущихся век.',
    F(SANS, 10), TXTM, anchor='ma')
eye_phase_base = home_scene(W, H, 0.0).convert('RGBA')
cx, cy, _, _, _ = eye_box()
eye_phase_half_crop = round(180 * S)
eye_phase_crop_box = (
    round(cx * S) - eye_phase_half_crop,
    round(cy * S) - eye_phase_half_crop,
    round(cx * S) + eye_phase_half_crop,
    round(cy * S) + eye_phase_half_crop,
)
for index, phase in enumerate(EYE_PHASES):
    eye_layer, seam, aperture, open_aperture, eye_x, eye_y = _living_eye_components(phase)
    combined = Image.new('RGBA', eye_layer.size, (0, 0, 0, 0))
    combined.alpha_composite(eye_layer)
    combined.alpha_composite(seam)

    seam_mask = seam.getchannel('A').point(lambda alpha: 255 if alpha else 0)
    allowed_mask = ImageChops.lighter(open_aperture, seam_mask)
    outside_mask = ImageChops.invert(allowed_mask)
    assert ImageChops.multiply(combined.getchannel('A'), outside_mask).getbbox() is None, \
        f'phase={phase}: eye overlay leaked outside original aperture/seam'
    if phase == 1.0:
        assert aperture.getbbox() is None, 'closed phase must disable the open anatomy aperture'
        required = open_aperture.filter(ImageFilter.MinFilter(3))
        opaque = combined.getchannel('A').point(lambda alpha: 255 if alpha >= 250 else 0)
        uncovered = ImageChops.subtract(required, opaque)
        assert uncovered.getbbox() is None, (
            'closed phase must opaquely cover the original open aperture; '
            f'uncovered={uncovered.getbbox()}')
        required.close(); opaque.close(); uncovered.close()

    frame = eye_phase_base.copy()
    frame.alpha_composite(combined, (eye_x, eye_y))
    before = eye_phase_base.crop((eye_x, eye_y, eye_x + combined.width, eye_y + combined.height))
    after = frame.crop((eye_x, eye_y, eye_x + combined.width, eye_y + combined.height))
    colour_diff = ImageChops.difference(before.convert('RGB'), after.convert('RGB'))
    assert all(ImageChops.multiply(channel, outside_mask).getbbox() is None
               for channel in colour_diff.split()), \
        f'phase={phase}: base eye-surround changed outside original aperture/seam'

    crop = frame.crop(eye_phase_crop_box).convert('RGB').resize(
        (EYE_PHASE_CARD, EYE_PHASE_CARD), Image.Resampling.LANCZOS)
    x = EYE_PHASE_MARGIN + index * (EYE_PHASE_CARD + EYE_PHASE_GAP)
    eye_phase_draw.rectangle((x - 2, EYE_PHASE_HEADER - 2,
                              x + EYE_PHASE_CARD + 1, EYE_PHASE_HEADER + EYE_PHASE_CARD + 1),
                             fill=(75, 57, 34))
    eye_phase_sheet.paste(crop, (x, EYE_PHASE_HEADER))
    txt(eye_phase_draw, (x + EYE_PHASE_CARD / 2, EYE_PHASE_HEADER + EYE_PHASE_CARD + 16),
        f'phase {phase:.2f}', F(SANSB, 12), GOLD, anchor='ma')
    crop.close(); before.close(); after.close(); frame.close(); colour_diff.close()
    allowed_mask.close(); outside_mask.close(); seam_mask.close()
    combined.close(); eye_layer.close(); seam.close(); aperture.close(); open_aperture.close()
eye_phase_base.close()
EYE_PHASE_SHEET = str(OUTDIR / 'owner-eye-blink-phases.png')
eye_phase_sheet.save(EYE_PHASE_SHEET, 'PNG', optimize=True)
EYE_PHASE_SHEET_QA = str(OUTDIR / 'owner-eye-blink-phases-qa.jpg')
eye_phase_qa_width = 600
eye_phase_qa = eye_phase_sheet.resize(
    (eye_phase_qa_width, round(eye_phase_sheet.height * eye_phase_qa_width / eye_phase_sheet.width)),
    Image.Resampling.LANCZOS,
)
eye_phase_qa.save(EYE_PHASE_SHEET_QA, 'JPEG', quality=55, optimize=True)
eye_phase_qa.close()
eye_phase_sheet.close()
if EYE_PHASES_ONLY:
    print('OK', EYE_PHASE_SHEET, f'{os.path.getsize(EYE_PHASE_SHEET)/1024:.0f} KB')
    print('OK', EYE_PHASE_SHEET_QA, f'{os.path.getsize(EYE_PHASE_SHEET_QA)/1024:.0f} KB')
    raise SystemExit(0)

# ── доска сравнения: эталон владельца слева, симуляция того же вьюпорта справа
ref = Image.open(REF_JPG).convert('RGB').resize((W, H), Image.LANCZOS)
CGAP = 46 * S; CMARG = 40 * S; CTOP = 168 * S; CCAP = 96 * S
cb_w = CMARG * 2 + W * 2 + CGAP
cb_h = CTOP + H + CCAP
board = Image.new('RGB', (cb_w, cb_h), (13, 9, 6))
bd = ImageDraw.Draw(board)
txt(bd, (cb_w / 2, 34 * S), 'MaestroVPN Home — эталон владельца против симуляции',
    F(PLAY, 30), GOLD, anchor='ma')
for i, line in enumerate([
        'Слева — установленный Home 08-owner-installed-test-home-2026-08-08.jpg. Справа — симуляция по числам',
        'PhoneHomeReferenceLayout.kt и PhoneHomeControlDeck.kt на ПОДЛИННЫХ ассетах репозитория (центральный',
        'свет 4D-атласа из 8 relief-слоёв, Playfair). Кадр фиксирует фазу; Android сохраняет blink/gaze/touch.',
        'Резьба дуги и консоли — настоящий арт из атласа; код рисует только подписи, иконки и выбор.']):
    txt(bd, (cb_w / 2, (80 + i * 19) * S), line, F(SANS, 13), TXTM, anchor='ma')
for x, im, cap in ((CMARG, ref, 'Эталон владельца'),
                   (CMARG + W + CGAP, homes['connected'].convert('RGB'), 'Симуляция ПОДКЛЮЧЕНО')):
    card = rounded(im, 38 * S)
    bd.rounded_rectangle((x - 3 * S, CTOP - 3 * S, x + W + 3 * S, CTOP + H + 3 * S),
                         radius=41 * S, fill=(58, 44, 28))
    board.paste(card, (round(x), round(CTOP)), card)
    txt(bd, (x + W / 2, CTOP + H + 24 * S), cap, F(PLAY, 20), GOLD, anchor='ma')
board = board.resize((cb_w // 2, cb_h // 2), Image.LANCZOS)
BOARD = str(OUTDIR / 'owner-home-comparison.png')
board.save(BOARD, 'PNG', optimize=True)
BOARD_JPG = str(OUTDIR / 'owner-home-comparison-qa.jpg')
board_qa = board.resize((450, round(board.height * 450 / board.width)), Image.LANCZOS)
board_qa.save(BOARD_JPG, 'JPEG', quality=42, optimize=True)
board_qa.close()

# ── доказательство общего scroll-owner: верх одинаков, relief и подписи едут вместе
SGAP = 46 * S; SMARG = 40 * S; STOP = 142 * S; SCAP = 82 * S
scroll_w = SMARG * 2 + W * 2 + SGAP
scroll_h = STOP + H + SCAP
scroll_board = Image.new('RGB', (scroll_w, scroll_h), (13, 9, 6))
sbd = ImageDraw.Draw(scroll_board)
txt(sbd, (scroll_w / 2, 30 * S), 'Home — fixed hero и единый скролл нижней деки',
    F(PLAY, 28), GOLD, anchor='ma')
for i, line in enumerate([
        'Слева — начало. Справа — +64 dp: логотип, кольцо и глаз остаются на месте;',
        'arc / contacts / console и все их подписи смещаются на одно значение и клипуются под героем.']):
    txt(sbd, (scroll_w / 2, (72 + i * 19) * S), line, F(SANS, 13), TXTM, anchor='ma')
for x, im, cap in ((SMARG, homes['connected'].convert('RGB'), 'Начало деки'),
                   (SMARG + W + SGAP, home_scrolled.convert('RGB'), 'Дека прокручена на 64 dp')):
    card = rounded(im, 38 * S)
    sbd.rounded_rectangle((x - 3 * S, STOP - 3 * S, x + W + 3 * S, STOP + H + 3 * S),
                          radius=41 * S, fill=(58, 44, 28))
    scroll_board.paste(card, (round(x), round(STOP)), card)
    txt(sbd, (x + W / 2, STOP + H + 24 * S), cap, F(PLAY, 20), GOLD, anchor='ma')
scroll_board = scroll_board.resize((scroll_w // 2, scroll_h // 2), Image.LANCZOS)
SCROLL_BOARD = str(OUTDIR / 'owner-home-scroll-proof.png')
scroll_board.save(SCROLL_BOARD, 'PNG', optimize=True)
SCROLL_BOARD_JPG = str(OUTDIR / 'owner-home-scroll-proof-qa.jpg')
scroll_board_qa = scroll_board.resize((450, round(scroll_board.height * 450 / scroll_board.width)), Image.LANCZOS)
scroll_board_qa.save(SCROLL_BOARD_JPG, 'JPEG', quality=42, optimize=True)
scroll_board_qa.close()


# ── лист трёх состояний глаза
GAP = 46 * S; MARG = 40 * S; TOPH = 150 * S; CAPH = 118 * S
sheet_w = MARG * 2 + W * 3 + GAP * 2
sheet_h = TOPH + H + CAPH + 30 * S
sheet = Image.new('RGB', (sheet_w, sheet_h), (13, 9, 6))
glow = Image.new('RGB', (sheet_w, sheet_h), (13, 9, 6))
ImageDraw.Draw(glow).ellipse((sheet_w * .15, -sheet_h * .35, sheet_w * .85, sheet_h * .45),
                             fill=(38, 27, 18))
sheet = Image.blend(sheet, glow.filter(ImageFilter.GaussianBlur(160)), .85)
sd = ImageDraw.Draw(sheet)
txt(sd, (sheet_w / 2, 34 * S), 'MaestroVPN — Home, три состояния глаза (симуляция)',
    F(PLAY, 30), GOLD, anchor='ma')
for i, line in enumerate([
        'Неподвижны: логотип, медальон, eye-surround и живой глаз. Ниже идут статус и активный протокол,',
        'телефон, Telegram / МАКС / WhatsApp, дуга протоколов, покупка и нижняя консоль.',
        'Нижняя дека имеет один scroll-owner для рельефа, плиток, иконок и текста. Старого барабана,',
        'снэпа, наклона рядов и градиентной маски больше нет.']):
    txt(sd, (sheet_w / 2, (78 + i * 19) * S), line, F(SANS, 13), TXTM, anchor='ma')
xs = [MARG, MARG + W + GAP, MARG + (W + GAP) * 2]
for x, st in zip(xs, STATES):
    im = homes[st].convert('RGB')
    card = rounded(im, 38 * S)
    sd.rounded_rectangle((x - 3 * S, TOPH - 3 * S, x + W + 3 * S, TOPH + H + 3 * S),
                         radius=41 * S, fill=(58, 44, 28))
    sheet.paste(card, (round(x), round(TOPH)), card)
    txt(sd, (x + W / 2, TOPH + H + 26 * S), STATE_RU[st], F(PLAY, 18), GOLD, anchor='ma')
sheet = sheet.resize((sheet_w // 2, sheet_h // 2), Image.LANCZOS)
sheet.save(OUT, 'PNG', optimize=True)
OUT_QA = str(OUTDIR / 'phone-screens-qa.jpg')
sheet_qa = sheet.resize((900, round(sheet.height * 900 / sheet.width)), Image.LANCZOS)
sheet_qa.save(OUT_QA, 'JPEG', quality=60, optimize=True)
sheet_qa.close()

for name in [OUT, OUT_QA, BOARD, BOARD_JPG, SCROLLED, SCROLL_BOARD, EYE_PHASE_SHEET,
             EYE_PHASE_SHEET_QA] + [
        str(OUTDIR / f'owner-home-{st}.png') for st in STATES]:
    print('OK', name, f'{os.path.getsize(name)/1024:.0f} KB')
