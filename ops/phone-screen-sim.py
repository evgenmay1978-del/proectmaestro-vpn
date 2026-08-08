#!/usr/bin/env python3
"""Deterministic visual smoke test for the PHONE screens (390x844 dp @2x).

–¢–µ–ª–µ—Ñ–æ–Ω–Ω—ã–π –±–ª–∏–∑–Ω–µ—Ü ops/tv-master-sim.py. –°—É—â–µ—Å—Ç–≤—É–µ—Ç –ø–æ—Ç–æ–º—É, —á—Ç–æ –ø—Ä–∞–≤–∏–ª–æ –ø—Ä–æ–µ–∫—Ç–∞
—Ç—Ä–µ–±—É–µ—Ç —Å–º–æ—Ç—Ä–µ—Ç—å UI –ì–õ–ê–ó–ê–ú–ò –î–û –ö–û–î–ê (CLAUDE.md, –ø.3 ¬´UI = –ø–∏–∫—Å–µ–ª—å-–≤-–ø–∏–∫—Å–µ–ª—å¬ª), –∞
–ª–æ–∫–∞–ª—å–Ω–æ–≥–æ Android SDK –Ω–∞ S1 –Ω–µ—Ç –∏ —ç–º—É–ª—è—Ç–æ—Ä–∞ —Ç–æ–∂–µ: —Å–æ–±—Ä–∞—Ç—å APK –º–æ–∂–Ω–æ —Ç–æ–ª—å–∫–æ –≤ CI,
~13 –º–∏–Ω—É—Ç –∑–∞ –ø—Ä–æ–≥–æ–Ω, –∏ –≤—ã–≥—Ä—É–∑–∫–∞ APK –µ—â—ë –∏ –ø–∞–¥–∞–µ—Ç –ø—Ä–∏ –ø–µ—Ä–µ–ø–æ–ª–Ω–µ–Ω–Ω–æ–π –∫–≤–æ—Ç–µ –∞—Ä—Ç–µ—Ñ–∞–∫—Ç–æ–≤.
–ü–æ—ç—Ç–æ–º—É —ç–∫—Ä–∞–Ω –≤–æ—Å–ø—Ä–æ–∏–∑–≤–æ–¥–∏—Ç—Å—è –∏–∑ –ü–û–î–õ–ò–ù–ù–´–• –∞—Å—Å–µ—Ç–æ–≤ —Ä–µ–ø–æ–∑–∏—Ç–æ—Ä–∏—è –∏ —á–∏—Å–µ–ª –∏–∑ Kotlin.

–ß—Ç–æ –±–µ—Ä—ë—Ç—Å—è –∏–∑ —Ä–µ–ø–æ (–≤–æ—Å–ø—Ä–æ–∏–∑–≤–æ–¥–∏–º–æ, –Ω–∏—á–µ–≥–æ –Ω–µ –≤—ã–¥—É–º–∞–Ω–æ):
  mobile_4d/atlas_c_*.webp      ‚Äî —Ü–µ–Ω—Ç—Ä–∞–ª—å–Ω–æ–µ –æ—Å–≤–µ—â–µ–Ω–∏–µ –ø—è—Ç–∏—Å–ª–æ–π–Ω–æ–π 4D-—Å—Ü–µ–Ω—ã
  mobile_eye_open.webp          ‚Äî –µ–¥–∏–Ω—ã–π —Å–ª–æ–π –≥–ª–∞–∑–∞ –¥–ª—è –≤—Å–µ—Ö —Ñ–∞–∑ –º–æ—Ä–≥–∞–Ω–∏—è
  mobile_surface.webp            ‚Äî —Ñ–æ–Ω –≤–Ω—É—Ç—Ä–µ–Ω–Ω–∏—Ö —ç–∫—Ä–∞–Ω–æ–≤
  frame_button.9.png / frame_bar.9.png / frame_panel.9.png ‚Äî nine-patch —Ä–∞–º—ã
  font/playfair_display.ttf      ‚Äî —Ç–∏—Ç—É–ª—å–Ω—ã–π —à—Ä–∏—Ñ—Ç

–ì–µ–æ–º–µ—Ç—Ä–∏—è ‚Äî –∏–∑ TvHomeScreen.kt (ContentScale.Crop –¥–ª—è 853x1844, —Ü–µ–Ω—Ç—Ä –º–µ–¥–∞–ª—å–æ–Ω–∞
430,711, —Ä–∞–¥–∏—É—Å 260, windowTop = —Ü–µ–Ω—Ç—Ä+—Ä–∞–¥–∏—É—Å+12, windowBottom = 7% –≤—ã—Å–æ—Ç—ã),
—Ü–≤–µ—Ç–∞ ‚Äî –∏–∑ premium/MobilePremiumTokens.kt –∏ theme/Color.kt.

‚õî –õ–û–í–£–®–ö–ò (–Ω–µ ¬´—É–ª—É—á—à–∞—Ç—å¬ª –±–µ–∑–¥—É–º–Ω–æ):
 1. –í Liberation Sans –ù–ï–¢ –≥–ª–∏—Ñ–æ–≤ ‚ÇΩ –∏ ‚ö† ‚Äî —Ä–∏—Å—É–µ—Ç—Å—è .notdef-–∫–≤–∞–¥—Ä–∞—Ç (–ø—Ä–æ–≤–µ—Ä–µ–Ω–æ
    fontTools cmap). –°—Ç—Ä–æ–∫–∏ —Å –Ω–∏–º–∏ –∏–¥—É—Ç DejaVu. –Ø—Ä–ª—ã–∫–∏ –ø–ª–∏—Ç–æ–∫ –Ω–∞–º–µ—Ä–µ–Ω–Ω–æ –æ—Å—Ç–∞–≤–ª–µ–Ω—ã
    –Ω–∞ Liberation: –æ–Ω –ø–æ —à–∏—Ä–∏–Ω–µ –±–ª–∏–∑–æ–∫ –∫ Roboto, –ø–æ—ç—Ç–æ–º—É –æ–±—Ä–µ–∑–∫–∞ –Ω–∞ —Å–∏–º—É–ª—è—Ü–∏–∏ –Ω–µ
    –ø—Ä–µ—É–≤–µ–ª–∏—á–µ–Ω–∞ –ø—Ä–æ—Ç–∏–≤ –Ω–∞—Å—Ç–æ—è—â–µ–≥–æ —É—Å—Ç—Ä–æ–π—Å—Ç–≤–∞.
 2. clip_txt(..., ellipsis=False) –≤–æ—Å–ø—Ä–æ–∏–∑–≤–æ–¥–∏—Ç TextOverflow.Clip ‚Äî –¥–µ—Ñ–æ–ª—Ç Compose,
    –∫–æ–≥–¥–∞ —É Text –∑–∞–¥–∞–Ω maxLines –∏ –ù–ï –∑–∞–¥–∞–Ω overflow. –≠—Ç–æ –æ–±—Ä—É–±–∞–Ω–∏–µ –ø–æ—Å—Ä–µ–¥–∏ –≥–ª–∏—Ñ–∞,
    –±–µ–∑ ¬´...¬ª. –ï—Å–ª–∏ –ø–æ–ø—Ä–∞–≤–∏—Ç—å –Ω–∞ ellipsis=True ¬´—á—Ç–æ–±—ã –∫—Ä–∞—Å–∏–≤–µ–µ¬ª ‚Äî —Å–∏–º—É–ª—è—Ü–∏—è –Ω–∞—á–Ω—ë—Ç
    –≤—Ä–∞—Ç—å –∏ –ø–µ—Ä–µ—Å—Ç–∞–Ω–µ—Ç –ª–æ–≤–∏—Ç—å –∏–º–µ–Ω–Ω–æ —ç—Ç–æ—Ç –∫–ª–∞—Å—Å –¥–µ—Ñ–µ–∫—Ç–æ–≤.
 3. –≠—Ç–æ –ù–ï —Å–∫—Ä–∏–Ω—à–æ—Ç. –õ–∏—Å—Ç —Ñ–∞–∑ –≥–ª–∞–∑–∞ –¥–µ—Ç–µ—Ä–º–∏–Ω–∏—Ä–æ–≤–∞–Ω–Ω–æ –ø–æ–∫–∞–∑—ã–≤–∞–µ—Ç —Ñ–æ—Ä–º—É –º–æ—Ä–≥–∞–Ω–∏—è,
    –Ω–æ –Ω–µ –≤–æ—Å–ø—Ä–æ–∏–∑–≤–æ–¥–∏—Ç —Å–ª–µ–∂–µ–Ω–∏–µ –∑–∞ –ø–∞–ª—å—Ü–µ–º; –±–∞—Ä–∞–±–∞–Ω –Ω–µ –∫—Ä—É—Ç–∏—Ç—Å—è, —Å—É–º–º—ã/QR —É—Å–ª–æ–≤–Ω—ã–µ.
    –î–ª—è –¥–æ–∫–∞–∑–∞—Ç–µ–ª—å—Å—Ç–≤ –ø–æ–≤–µ–¥–µ–Ω–∏—è ‚Äî CI –∏
    —É—Å—Ç—Ä–æ–π—Å—Ç–≤–æ, —Å–∏–º—É–ª—è—Ü–∏—è —Ç–æ–ª—å–∫–æ –¥–ª—è –≥–ª–∞–∑.

–ò—Å–ø–æ–ª—å–∑–æ–≤–∞–Ω–∏–µ:  ops/phone-screen-sim.py   ->  build/phone-screen-sim/phone-screens.png
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
S = 2  # —Å—É–ø–µ—Ä-—Å–µ–º–ø–ª–∏–Ω–≥: —Ä–∞–±–æ—Ç–∞–µ–º –≤ 2x –æ—Ç dp
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
# ‚õî –õ–û–í–£–®–ö–ê: –≤ Liberation Sans –ù–ï–¢ –≥–ª–∏—Ñ–æ–≤ ‚ÇΩ –∏ ‚ö† (–ø—Ä–æ–≤–µ—Ä–µ–Ω–æ fontTools cmap) ‚Äî —Ä–∏—Å—É–µ—Ç—Å—è
# .notdef-–∫–≤–∞–¥—Ä–∞—Ç. –î–ª—è —Å—Ç—Ä–æ–∫ —Å –Ω–∏–º–∏ –±–µ—Ä—ë–º DejaVu. –Ø—Ä–ª—ã–∫–∏ –ø–ª–∏—Ç–æ–∫ –æ—Å—Ç–∞–≤–ª–µ–Ω—ã –Ω–∞ Liberation:
# –æ–Ω –ø–æ —à–∏—Ä–∏–Ω–µ –±–ª–∏–∑–æ–∫ –∫ Roboto, –ø–æ—ç—Ç–æ–º—É –æ–±—Ä–µ–∑–∫–∞ –Ω–∞ –º–∞–∫–µ—Ç–µ –Ω–µ –ø—Ä–µ—É–≤–µ–ª–∏—á–µ–Ω–∞ –ø—Ä–æ—Ç–∏–≤ —Ä–µ–∞–ª—å–Ω–æ—Å—Ç–∏.

def F(path, sp): return ImageFont.truetype(path, round(sp * S))

# ‚îÄ‚îÄ –ø–∞–ª–∏—Ç—Ä–∞ MobilePremiumTokens.kt + theme/Color.kt
WALNUT=(18,13,9); LEATHER=(29,21,16); GOLD=(224,189,112); GOLDM=(168,135,74)
EMER=(41,201,101); RUBY=(201,86,77); TXT=(241,232,208); TXTM=(189,181,167)
NEONG=(70,224,90); ORANGE=(240,121,42); STATERED=(255,64,64)

def nine(src, w, h, sl=None):
    """9-slice –º–∞—Å—à—Ç–∞–±–∏—Ä–æ–≤–∞–Ω–∏–µ nine-patch –±–µ–∑ —Å–ª—É–∂–µ–±–Ω–æ–π 1px —Ä–∞–º–∫–∏."""
    im = src.crop((1, 1, src.width - 1, src.height - 1)).convert('RGBA')
    sl = sl or max(8, min(im.width, im.height) // 3)
    sl = min(sl, im.width // 2 - 1, im.height // 2 - 1)
    w, h = max(w, sl * 2 + 1), max(h, sl * 2 + 1)
    out = Image.new('RGBA', (w, h))
    W, H = im.width, im.height
    box = lambda a, b, c, d: im.crop((a, b, c, d))
    cw, ch = w - 2 * sl, h - 2 * sl
    # —Ü–µ–Ω—Ç—Ä –∏ –∫—Ä–∞—è
    out.paste(box(sl, sl, W - sl, H - sl).resize((cw, ch), Image.BILINEAR), (sl, sl))
    out.paste(box(sl, 0, W - sl, sl).resize((cw, sl), Image.BILINEAR), (sl, 0))
    out.paste(box(sl, H - sl, W - sl, H).resize((cw, sl), Image.BILINEAR), (sl, h - sl))
    out.paste(box(0, sl, sl, H - sl).resize((sl, ch), Image.BILINEAR), (0, sl))
    out.paste(box(W - sl, sl, W, H - sl).resize((sl, ch), Image.BILINEAR), (w - sl, sl))
    # —É–≥–ª—ã
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
    # 83 = 77 –ø—Ä–µ–∂–Ω–∏—Ö + 6 —Ñ—Ä–∞–≥–º–µ–Ω—Ç–æ–≤ —Å–ª–æ—è `arc`, –¥–æ–±–∞–≤–ª–µ–Ω–Ω–æ–≥–æ 2026-08-01. –ß–∏—Å–ª–æ —Å–≤–µ—Ä—è–µ—Ç—Å—è
    # –Ω–∞–º–µ—Ä–µ–Ω–Ω–æ: –º–æ–ª—á–∞–ª–∏–≤–æ–µ —Ä–∞—Å—Ö–æ–∂–¥–µ–Ω–∏–µ –º–∞–Ω–∏—Ñ–µ—Å—Ç–∞ –∏ —Å–∏–º—É–ª—è—Ü–∏–∏ = —Å–∏–º—É–ª—è—Ü–∏—è –Ω–∞—á–Ω—ë—Ç –≤—Ä–∞—Ç—å.
    # 84 = 83 + —Ñ—Ä–∞–≥–º–µ–Ω—Ç —Å–ª–æ—è `console`, –ø–æ–¥–∫–ª—é—á—ë–Ω–Ω–æ–≥–æ 01.08.
    # 90 = 84 + —Ñ—Ä–∞–≥–º–µ–Ω—Ç—ã —Å–ª–æ—è `contacts`, –ø–æ–¥–∫–ª—é—á—ë–Ω–Ω–æ–≥–æ 01.08.
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
    """–í–µ—Ä—Ç–∏–∫–∞–ª—å–Ω—ã–π –≥—Ä–∞–¥–∏–µ–Ω—Ç –ø–æ —Å—Ç–æ–ø–∞–º [(pos, (r,g,b), alpha)] ‚Äî –∫–∞–∫ Brush.verticalGradient."""
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
    """–†–∏—Å—É–µ—Ç —Ç–µ–∫—Å—Ç, –æ–±—Ä–µ–∑–∞—è –ø–æ maxw. ellipsis=False = TextOverflow.Clip (–¥–µ—Ñ–æ–ª—Ç Compose):
    –æ–±—Ä—É–±–∞–Ω–∏–µ –ø–æ—Å—Ä–µ–¥–∏ –≥–ª–∏—Ñ–∞, –±–µ–∑ ¬´‚Ä¶¬ª. –î–ª—è —è—Ä–ª—ã–∫–æ–≤ –ø–ª–∏—Ç–æ–∫ –∏—Å–ø–æ–ª—å–∑—É–µ—Ç—Å—è autosize_txt
    (–≤ –∫–æ–¥–µ autoSize), clip_txt –æ—Å—Ç–∞—ë—Ç—Å—è –¥–ª—è –º–µ—Å—Ç, –≥–¥–µ Compose –¥–µ–π—Å—Ç–≤–∏—Ç–µ–ª—å–Ω–æ –∫–ª–∏–ø—É–µ—Ç."""
    tmp = Image.new('RGBA', (max(1, round(font.getlength(s)) + 8), font.size * 2))
    ImageDraw.Draw(tmp).text((0, 0), s, font=font, fill=fill)
    if ellipsis and tmp.width > maxw:
        while s and font.getlength(s + '‚Ä¶') > maxw: s = s[:-1]
        tmp = Image.new('RGBA', (maxw, font.size * 2))
        ImageDraw.Draw(tmp).text((0, 0), s + '‚Ä¶', font=font, fill=fill)
    tmp = tmp.crop((0, 0, min(tmp.width, maxw), tmp.height))
    layer.alpha_composite(tmp, (round(x), round(y)))

def autosize_txt(layer, x, y, s, fill, maxw, path, min_sp, max_sp, step_sp=0.5, bold=True):
    """–í–æ—Å–ø—Ä–æ–∏–∑–≤–æ–¥–∏—Ç BasicText + TextAutoSize.StepBased: —É–º–µ–Ω—å—à–∞–µ—Ç –∫–µ–≥–ª—å —Å max_sp –¥–æ
    min_sp —à–∞–≥–æ–º step_sp, –ø–æ–∫–∞ —Å—Ç—Ä–æ–∫–∞ –Ω–µ –≤–ª–µ–∑–µ—Ç –≤ maxw. –ò–º–µ–Ω–Ω–æ —Ç–∞–∫ —Ç–µ–ø–µ—Ä—å —É—Å—Ç—Ä–æ–µ–Ω—ã
    —è—Ä–ª—ã–∫–∏ —Å–µ–∫—Ç–æ—Ä–æ–≤ –∏ –ø–ª–∏—Ç–æ–∫ (PhoneHomeControlDeck.kt, ProtocolSector/HomeTile) ‚Äî –ø—Ä–∏—ë–º
    –≤–∑—è—Ç –∏–∑ component/NeonGlass.kt:228. –ï—Å–ª–∏ —Å—Ç—Ä–æ–∫–∞ –Ω–µ –≤–ª–µ–∑–∞–µ—Ç –¥–∞–∂–µ –Ω–∞ min_sp, Compose
    –æ—Å—Ç–∞–≤–ª—è–µ—Ç min_sp –∏ –æ–±—Ä–µ–∑–∞–µ—Ç ‚Äî –∑–¥–µ—Å—å —Ç–æ –∂–µ —Å–∞–º–æ–µ."""
    sp = max_sp
    while sp > min_sp:
        if F(path, sp).getlength(s) <= maxw:
            break
        sp -= step_sp
    clip_txt(layer, x, y, s, F(path, sp), fill, maxw, ellipsis=False)
    return sp

# ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ‚îÄ –∏–∫–æ–Ω–∫–∏ (–ø—Ä–æ—Å—Ç—ã–µ –≤–µ–∫—Ç–æ—Ä–Ω—ã–µ –∑–∞–≥–æ—Ç–æ–≤–∫–∏ –ø–æ–¥ Material)
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
    d.ellipse(◊N˝Ú⁄$z{-ÆÈ‹j◊ù¢f˜"∆&V¬ñ‚≤}	Ì-≠Ω-¬-›çm2Ì˝Ω-≤u”†–¢&Ç“S¢3≤'r“7r“CB¢0–¢"“ñ÷vRÊÊWrÇu$t$r¬Ü'r¬&Çí¬É¬¬¬íì≤"Ê«Üˆ6ˆ◊˜6óFRÜÊñÊRÑ%D‚¬'r¬&Çíê–¢GáBÑñ÷vTG&r‰G&rÜ"í¬Ü'rÚ"¬Ü&Ç“#¢2íÚ"í¬∆&V¬¬bÖ4Â4"¬bí¬EÖB¬Ê6Ü˜#“v÷rê–¢ñÊÊW"Ê«Üˆ6ˆ◊˜6óFRÜ"¬á&˜VÊBÇÜ7r“'ríÚ"í¬&˜VÊBáíííì≤í≥“&Ç≤B¢0–¢GáBÜñG"¬Ü7rÚ"¬íí¬}	≠ÌB}≠}ç=≠mç-R"ÌÌù]›çÇ¢˝]]-ÌM2ì¢r¬eˆÇ¬EÖB¬Ê6Ü˜#“v÷rê–¢í≥“#"¢0–¢GáBÜñG"¬Ü7rÚ"¬íí¬t#$32r¬bÖƒí¬#bí¬T‘U"¬Ê6Ü˜#“v÷rì≤í≥“3Ç¢0–¢GáBÜñG"¬Ü7rÚ"¬íí¬}	çΩÇ-=}›=‚˝‚
		Û¢≥rìsrÉ”cR”cBr¬eˆÇ¬EÖB¬Ê6Ü˜#“v÷rì≤í≥“3¢0–¢&Ç“S¢3≤'r“#C¢0–¢"“ñ÷vRÊÊWrÇu$t$r¬Ü'r¬&Çí¬É¬¬¬íì≤"Ê«Üˆ6ˆ◊˜6óFRÜÊñÊRÑ%D‚¬'r¬&Çíê–¢GáBÑñ÷vTG&r‰G&rÜ"í¬Ü'rÚ"¬Ü&Ç“#¢2íÚ"í¬}
ÚÌ˝Ω-ç≤r¬bÖ4Â4"¬bí¬EÖB¬Ê6Ü˜#“v÷rê–¢ñÊÊW"Ê«Üˆ6ˆ◊˜6óFRÜ"¬á&˜VÊBÇÜ7r“'ríÚ"í¬&˜VÊBáíííì≤í≥“&Ç≤B¢0–¢eˆ““bÖ4Â2¬"„Rê–¢f˜"∆ñÊRñ‚≤}	˝ÌΩR›m-çÚ}˝-≠=ùM"-ΩM]ΩÕm2‚r¿–¢}	˝ÌM˝ç≠≠-ç-ç=]-Ú˝ÌΩR˝ÌM--]mM]›çÚ(	Br¬}Ì--Õ-R›≠“Ì-≠Ω-Ω¬‚u”†–¢GáBÜñG"¬Ü7rÚ"¬íí¬∆ñÊR¬eˆ“¬EÖD“¬Ê6Ü˜#“v÷rì≤í≥“Ç¢0–¢ÇÊ«Üˆ6ˆ◊˜6óFRÜñÊÊW"¬á&˜VÊBáBí¬&˜VÊBáíííê–¢&WGW&‚Ä–†–¢2)Y)Y)Y)Y)Y)Y)Y)Y)Y
›	≠
		“3¢Ìçç≠≠-ç-mçÇÑ6∆ñ’67&VV‚≤÷ˆ&ñ∆U&V÷óV‘W'&˜"í)Y)Y)Y)Y)Y)Y)Y)Y)Y –¶FVb67&VVÂˆW'"Çì†–¢Ç“6˜fW"Übw∑7G"Ö$U2ó“ˆ÷ˆ&ñ∆U˜7W&f6RÁvV'r¬r¬ÇíÊ6ˆÁfW'BÇu$t$rê–¢ÇÊ«Üˆ6ˆ◊˜6óFRÑñ÷vRÊÊWrÇu$t$r¬Ör¬Çí¬É¬¬¬Sííê–¢B“ñ÷vTG&r‰G&ráÇê–¢B“Ç¢0–¢ñ5ˆ&6≤ÜB¬B¬#"¢2¬#b¢2¬tÙƒBê–¢GáBÜB¬áB≤3Ç¢2¬Ç¢2í¬}	≠-ç-mçÚ˝ÌM˝ç≠Çr¬bÖƒí¬#Bí¬tÙƒBê–¢2˝ÌΩR--ÌM÷ˆ&ñ∆U&V÷óV’FWáDfñV∆BÜg&÷Uˆ&"í(	B›çmR]}›Ì=‚Ì›Õ]›-m]›∞–¢í“B¢3≤fÇ“SB¢0–¢f∆B“ñ÷vRÊÊWrÇu$t$r¬Ör“"¢B¬fÇí¬É¬¬¬íê–¢f∆BÊ«Üˆ6ˆ◊˜6óFRÜÊñÊRÑ$"¬r“"¢B¬fÇíê–¢GáBÑñ÷vTG&r‰G&rÜf∆Bí¬É#"¢2¬ÜfÇ“#¢2íÚ"í¬}	≠ÌBçΩÇΩÌ=ç“r¬bÖ4Â2¬bí¬EÖD“ê–¢ÇÊ«Üˆ6ˆ◊˜6óFRÜf∆B¬á&˜VÊBáBí¬&˜VÊBáíííê–¢í≥“fÇ≤b¢0–¢2˝›]Ω¬Ìçç≠Ä–¢ÖˆÇ“3¢0–¢‚“ñ÷vRÊÊWrÇu$t$r¬Ör“"¢B¬ÖˆÇí¬É¬¬¬íê–¢‚Ê«Üˆ6ˆ◊˜6óFRÜÊñÊRÖ‰T¬¬r“"¢B¬ÖˆÇíê–¢B“ñ÷vTG&r‰G&rá‚ì≤7r“‚ÁvñGFÄ–¢óí“C¢0–¢ñ5ˆW'"áB¬Ü7r“S"¢2íÚ"¬óí¬S"¢2¬%T%íì≤óí≥“S"¢2≤Ç¢0–¢GáBáB¬Ü7rÚ"¬óíí¬}	Ìçç≠¢	≠ÌB›R›ùM]“r¬bÖ4Â2¬rí¬%T%í¬Ê6Ü˜#“v÷rê–¢óí≥“#b¢2≤Ç¢0–¢&Ç“S¢3≤'r“7r“CB¢0–¢"“ñ÷vRÊÊWrÇu$t$r¬Ü'r¬&Çí¬É¬¬¬íì≤"Ê«Üˆ6ˆ◊˜6óFRÜÊñÊRÑ%D‚¬'r¬&Çíê–¢GáBÑñ÷vTG&r‰G&rÜ"í¬Ü'rÚ"¬Ü&Ç“#¢2íÚ"í¬}	˝Ì--Ìç-¬r¬bÖ4Â4"¬bí¬EÖB¬Ê6Ü˜#“v÷rê–¢‚Ê«Üˆ6ˆ◊˜6óFRÜ"¬á&˜VÊBÇÜ7r“'ríÚ"í¬&˜VÊBÜóíííê–¢ÇÊ«Üˆ6ˆ◊˜6óFRá‚¬á&˜VÊBáBí¬&˜VÊBáíííê–¢&WGW&‚Ç¬í≤#¢0–†–¢2)Y)Y)Y)Y)Y)Y)Y)Y)YÌ≠Ωç-Ì")Y)Y)Y)Y)Y)Y)Y)Y)Y –¶FVb&˜VÊFVBÜñ“¬&Bì†–¢““ñ÷vRÊÊWrÇt¬r¬ñ“Á6ó¶R¬ê–¢ñ÷vTG&r‰G&rÜ“íÁ&˜VÊFVE˜&V7FÊv∆RÇÉ¬¬ñ“ÁvñGFÇ“¬ñ“ÊÜVñváB“í¬&FóW3◊&B¬fñ∆√”#SRê–¢Ú“ñ“Ê6˜íÇì≤ÚÁWF«ÜÜ“ì≤&WGW&‚–†–•5DDU2“Çv6ˆÊÊV7FVBr¬v6ˆÊÊV7FñÊrr¬vFó66ˆÊÊV7FVBrê–•5DDUı%R“≤v6ˆÊÊV7FVBs¢}	˝	Ì	M	≠	Ω
Ì
}	]	›	‚(	B=ΩrÌ-≠Ω"r¿–¢v6ˆÊÊV7FñÊrs¢}	˝	Ì	M	≠	Ω
Ì
}	]	›	ç	R(	B=Ωr˝ÌΩ=Ì-≠Ω"r¿–¢vFó66ˆÊÊV7FVBs¢}	Ì
-	≠	Ω
Ì
}	]	›	‚(	B=Ωr˝ÌΩ›Ì-Õ‚}≠Ω"w––†–¶Üˆ÷W2“∑––•45$Ùƒ≈ı$ÙÙeÙE“cB„ –•45$ÙƒƒTB“7G"ÑıUDDï"Úv˜vÊW"÷Üˆ÷R÷6ˆÊÊV7FVB◊67&ˆ∆∆VBÁÊrrê–¶ñbÊ˜BUîUıÑ4U5ÙÙ‰≈ì†–¢f˜"7Bñ‚5DDU3†–¢ñ““67&VVÂˆÜˆ÷Rá7Bê–¢Üˆ÷W5∑7E““ñ––¢ñ“Ê6ˆÁfW'BÇu$t"ríÁ6fRÑıUDDï"Úbv˜vÊW"÷Üˆ÷R◊∑7G“ÁÊrr¬u‰rr¬˜Fñ÷ó¶S’G'VRê–¢Üˆ÷U˜67&ˆ∆∆VB“67&VVÂˆÜˆ÷RÇv6ˆÊÊV7FVBr¬45$Ùƒ≈ı$ÙÙeÙEê–¢Üˆ÷U˜67&ˆ∆∆VBÊ6ˆÁfW'BÇu$t"ríÁ6fRÖ45$ÙƒƒTB¬u‰rr¬˜Fñ÷ó¶S’G'VRê–¶V«6S†–¢Üˆ÷U˜67&ˆ∆∆VB“ÊˆÊP–†–¢2)H)HM]-]Õç›çÌ-››Ωí6∆˜6R◊W˝˝-ÇMs¢ÌM›ÕÌ}ç≠¬ÌMç“˜V‚÷WñR¬Mç›Õç}]≠Úù]Ω¿–§UîUıÑ4U2“É„¬„#R¬„R¬„sR¬„ê–§UîUıÑ4UÙ4$B“3c –§UîUıÑ4UÙt“`–§UîUıÑ4UÙ‘$tî‚“#@–§UîUıÑ4UÙÑTDU"“É`–¶WñU˜Ü6U˜6ÜVWB“ñ÷vRÊÊWrÄ–¢u$t"r¿–¢ÑUîUıÑ4UÙ‘$tî‚¢"≤UîUıÑ4UÙ4$B¢∆V‚ÑUîUıÑ4U2ê–¢≤UîUıÑ4UÙt¢Ü∆V‚ÑUîUıÑ4U2í“í¬UîUıÑ4UÙÑTDU"≤UîUıÑ4UÙ4$B≤Sbí¿–¢É2¬í¬bí¿–¢ê–¶WñU˜Ü6UˆG&r“ñ÷vTG&r‰G&rÜWñU˜Ü6U˜6ÜVWBê–ßGáBÜWñU˜Ü6UˆG&r¬ÜWñU˜Ü6U˜6ÜVWBÁvñGFÇÚ"¬Çí¿–¢}	=Ωr≤ÕÌ}ç≠(	B]Mç›ÚÕ≠ÕÌ=›çÚr¬bÖƒí¬Çí¬tÙƒB¬Ê6Ü˜#“v÷rê–ßGáBÜWñU˜Ü6UˆG&r¬ÜWñU˜Ü6U˜6ÜVWBÁvñGFÇÚ"¬Sí¿–¢v÷ˆ&ñ∆UˆWñUˆ˜V‚≠Ωç˝=]-Úù]ΩÕ„≤˝ÇÜ6S”Ì-Ì-Ú}Ì-ÚÕÌ}ç≠Ç-Ì›≠çíç}=Õ=M›ΩíçÌ"‚r¿–¢bÖ4Â2¬í¬EÖD“¬Ê6Ü˜#“v÷rê–¶WñU˜Ü6Uˆ&6R“Üˆ÷U˜66VÊRÖr¬Ç¬„íÊ6ˆÁfW'BÇu$t$rê–¶7Ç¬7í¬Ú¬Ú¬Ú“WñUˆ&˜ÇÇê–¶WñU˜Ü6UˆÜ∆eˆ7&˜“&˜VÊBÉÉ¢2ê–¶WñU˜Ü6Uˆ7&˜ˆ&˜Ç“Ä–¢&˜VÊBÜ7Ç¢2í“WñU˜Ü6UˆÜ∆eˆ7&˜¿–¢&˜VÊBÜ7í¢2í“WñU˜Ü6UˆÜ∆eˆ7&˜¿–¢&˜VÊBÜ7Ç¢2í≤WñU˜Ü6UˆÜ∆eˆ7&˜¿–¢&˜VÊBÜ7í¢2í≤WñU˜Ü6UˆÜ∆eˆ7&˜¿–¢ê–¶f˜"ñÊFWÇ¬Ü6Rñ‚VÁV÷W&FRÑUîUıÑ4U2ì†–¢WñUˆ∆ñW"¬6V“¬W'GW&R¬WñU˜Ç¬WñU˜í“ˆ∆ófñÊuˆWñUˆ6ˆ◊ˆÊVÁG2áÜ6Rê–¢6ˆ÷&ñÊVB“ñ÷vRÊÊWrÇu$t$r¬WñUˆ∆ñW"Á6ó¶R¬É¬¬¬íê–¢6ˆ÷&ñÊVBÊ«Üˆ6ˆ◊˜6óFRÜWñUˆ∆ñW"ê–¢6ˆ÷&ñÊVBÊ«Üˆ6ˆ◊˜6óFRá6V“ê–†–¢6V’ˆ÷6≤“6V“ÊvWF6ÜÊÊV¬ÇtríÁˆñÁBÜ∆÷&F«Ü¢#SRñb«ÜV«6Rê–¢∆∆˜vVEˆ÷6≤“ñ÷vT6Ü˜2Ê∆ñváFW"ÜW'GW&R¬6V’ˆ÷6≤ê–¢˜WG6ñFUˆ÷6≤“ñ÷vT6Ü˜2ÊñÁfW'BÜ∆∆˜vVEˆ÷6≤ê–¢76W'Bñ÷vT6Ü˜2Ê◊V«Fó«íÜ6ˆ÷&ñÊVBÊvWF6ÜÊÊV¬Çtrí¬˜WG6ñFUˆ÷6≤íÊvWF&&˜ÇÇíó2ÊˆÊR¬¿–¢bwÜ6S◊∑Ü6W”¢WñR˜fW&∆í∆V∂VB˜WG6ñFRW'GW&R˜6V“p–¢ñbÜ6R”“„†–¢76W'BWñUˆ∆ñW"ÊvWF6ÜÊÊV¬ÇtríÊvWF&&˜ÇÇíó2ÊˆÊR¬¿–¢v6∆˜6VBÜ6R◊W7BÜfR¶W&Ú˜V‚÷WñR«Üp–¢76W'Bñ÷vT6Ü˜2ÊFñffW&VÊ6RÜ6ˆ÷&ñÊVBÊvWF6ÜÊÊV¬Çtrí¬6V“ÊvWF6ÜÊÊV¬ÇtrííÊvWF&&˜ÇÇíó2ÊˆÊR¬¿–¢v6∆˜6VBÜ6R◊W7B6ˆÁFñ‚ˆÊ«íFÜRV÷W&∆B6V“˜fW&∆íp–†–¢g&÷R“WñU˜Ü6Uˆ&6RÊ6˜íÇê–¢g&÷RÊ«Üˆ6ˆ◊˜6óFRÜ6ˆ÷&ñÊVB¬ÜWñU˜Ç¬WñU˜ííê–¢&Vf˜&R“WñU˜Ü6Uˆ&6RÊ7&˜ÇÜWñU˜Ç¬WñU˜í¬WñU˜Ç≤6ˆ÷&ñÊVBÁvñGFÇ¬WñU˜í≤6ˆ÷&ñÊVBÊÜVñváBíê–¢gFW"“g&÷RÊ7&˜ÇÜWñU˜Ç¬WñU˜í¬WñU˜Ç≤6ˆ÷&ñÊVBÁvñGFÇ¬WñU˜í≤6ˆ÷&ñÊVBÊÜVñváBíê–¢6ˆ∆˜W%ˆFñfb“ñ÷vT6Ü˜2ÊFñffW&VÊ6RÜ&Vf˜&RÊ6ˆÁfW'BÇu$t"rí¬gFW"Ê6ˆÁfW'BÇu$t"ríê–¢76W'B∆¬Ññ÷vT6Ü˜2Ê◊V«Fó«íÜ6ÜÊÊV¬¬˜WG6ñFUˆ÷6≤íÊvWF&&˜ÇÇíó2ÊˆÊP–¢f˜"6ÜÊÊV¬ñ‚6ˆ∆˜W%ˆFñfbÁ7∆óBÇíí¬¿–¢bwÜ6S◊∑Ü6W”¢&6R÷˜6ñ26ÜÊvVB˜WG6ñFRW'GW&R˜6V“p–†–¢7&˜“g&÷RÊ7&˜ÜWñU˜Ü6Uˆ7&˜ˆ&˜ÇíÊ6ˆÁfW'BÇu$t"ríÁ&W6ó¶RÄ–¢ÑUîUıÑ4UÙ4$B¬UîUıÑ4UÙ4$Bí¬ñ÷vRÂ&W6◊∆ñÊr‰ƒ‰5§ı2ê–¢Ç“UîUıÑ4UÙ‘$tî‚≤ñÊFWÇ¢ÑUîUıÑ4UÙ4$B≤UîUıÑ4UÙtê–¢WñU˜Ü6UˆG&rÁ&V7FÊv∆RÇáÇ“"¬UîUıÑ4UÙÑTDU"“"¿–¢Ç≤UîUıÑ4UÙ4$B≤¬UîUıÑ4UÙÑTDU"≤UîUıÑ4UÙ4$B≤í¿–¢fñ∆√“ÉsR¬Sr¬3Bíê–¢WñU˜Ü6U˜6ÜVWBÁ7FRÜ7&˜¬áÇ¬UîUıÑ4UÙÑTDU"íê–¢GáBÜWñU˜Ü6UˆG&r¬áÇ≤UîUıÑ4UÙ4$BÚ"¬UîUıÑ4UÙÑTDU"≤UîUıÑ4UÙ4$B≤bí¿–¢bwÜ6R∑Ü6S¢„&g“r¬bÖ4Â4"¬"í¬tÙƒB¬Ê6Ü˜#“v÷rê–¢7&˜Ê6∆˜6RÇì≤&Vf˜&RÊ6∆˜6RÇì≤gFW"Ê6∆˜6RÇì≤g&÷RÊ6∆˜6RÇì≤6ˆ∆˜W%ˆFñfbÊ6∆˜6RÇê–¢∆∆˜vVEˆ÷6≤Ê6∆˜6RÇì≤˜WG6ñFUˆ÷6≤Ê6∆˜6RÇì≤6V’ˆ÷6≤Ê6∆˜6RÇê–¢6ˆ÷&ñÊVBÊ6∆˜6RÇì≤WñUˆ∆ñW"Ê6∆˜6RÇì≤6V“Ê6∆˜6RÇì≤W'GW&RÊ6∆˜6RÇê–¶WñU˜Ü6Uˆ&6RÊ6∆˜6RÇê–§UîUıÑ4Uı4ÑTUB“7G"ÑıUDDï"Úv˜vÊW"÷WñR÷&∆ñÊ≤◊Ü6W2ÁÊrrê–¶WñU˜Ü6U˜6ÜVWBÁ6fRÑUîUıÑ4Uı4ÑTUB¬u‰rr¬˜Fñ÷ó¶S’G'VRê–§UîUıÑ4Uı4ÑTUEı“7G"ÑıUDDï"Úv˜vÊW"÷WñR÷&∆ñÊ≤◊Ü6W2◊Êßrrê–¶WñU˜Ü6U˜˜vñGFÇ“c –¶WñU˜Ü6U˜“WñU˜Ü6U˜6ÜVWBÁ&W6ó¶RÄ–¢ÜWñU˜Ü6U˜˜vñGFÇ¬&˜VÊBÜWñU˜Ü6U˜6ÜVWBÊÜVñváB¢WñU˜Ü6U˜˜vñGFÇÚWñU˜Ü6U˜6ÜVWBÁvñGFÇíí¿–¢ñ÷vRÂ&W6◊∆ñÊr‰ƒ‰5§ı2¿–¢ê–¶WñU˜Ü6U˜Á6fRÑUîUıÑ4Uı4ÑTUEı¬t•Trr¬V∆óGì”SR¬˜Fñ÷ó¶S’G'VRê–¶WñU˜Ü6U˜Ê6∆˜6RÇê–¶WñU˜Ü6U˜6ÜVWBÊ6∆˜6RÇê–¶ñbUîUıÑ4U5ÙÙ‰≈ì†–¢&ñÁBÇtÙ≤r¬UîUıÑ4Uı4ÑTUB¬bw∂˜2ÁFÇÊvWG6ó¶RÑUîUıÑ4Uı4ÑTUBíÛ#C¢„g“¥"rê–¢&ñÁBÇtÙ≤r¬UîUıÑ4Uı4ÑTUEı¬bw∂˜2ÁFÇÊvWG6ó¶RÑUîUıÑ4Uı4ÑTUEıíÛ#C¢„g“¥"rê–¢&ó6R7ó7FV‘WÜóBÉê–†–¢2)H)HMÌ≠-›]›çÛ¢›-ΩÌ“-ΩM]ΩÕmΩ]-¬çÕ=Ω˝mçÚ-Ì=‚mR-ÕÌ˝Ì-˝- –ß&Vb“ñ÷vRÊ˜V‚Ö$TeÙ•ríÊ6ˆÁfW'BÇu$t"ríÁ&W6ó¶RÇÖr¬Çí¬ñ÷vR‰ƒ‰5§ı2ê–§4t“Cb¢3≤4‘$r“C¢3≤5Dı“cÇ¢3≤44“ìb¢0–¶6%˜r“4‘$r¢"≤r¢"≤4t –¶6%ˆÇ“5Dı≤Ç≤44 –¶&ˆ&B“ñ÷vRÊÊWrÇu$t"r¬Ü6%˜r¬6%ˆÇí¬É2¬í¬bíê–¶&B“ñ÷vTG&r‰G&rÜ&ˆ&Bê–ßGáBÜ&B¬Ü6%˜rÚ"¬3B¢2í¬t÷W7G&ıe‚Üˆ÷R(	B›-ΩÌ“-ΩM]ΩÕm˝Ì-ç"çÕ=Ω˝mçÇr¿–¢bÖƒí¬3í¬tÙƒB¬Ê6Ü˜#“v÷rê–¶f˜"í¬∆ñÊRñ‚VÁV÷W&FRÖ∞–¢}
Ω]-(	BB÷˜vÊW"◊6V∆V7FVB÷Üˆ÷R”##b”r”3Êßr¬˝ç-]M››Ωí¢3ì9sÉCB‚
˝-(	BçÕ=Ω˝mçÚ˝‚}çΩ¬r¿–¢uÜˆÊTÜˆ÷U&VfW&VÊ6T∆ñ˜WBÊ∑BÇÜˆÊTÜˆ÷T6ˆÁG&ˆƒFV6≤Ê∑B›	˝	Ì	M	Ω	ç	›	›
Ω
R]-R]˝Ì}ç-ÌçÚçm]›-ΩÕ›Ωír¿–¢}-]"DB›-ΩçrÇ&V∆ñVb›ΩÌ"¬∆ñfó"í‚
›-‚	›	R≠ç›çÌ#¢=Ωr--ç}]“¬›≠ΩÌ›Ç˝ΩΩ≠›]"‚r¿–¢}
]}ÕM==ÇÇ≠Ì›ÌΩÇ(	B›-Ì˝ùçí"çr-Ω≤≠ÌBç=]"-ÌΩÕ≠‚˝ÌM˝çÇ¬ç≠Ì›≠ÇÇ-ΩÌ‚u“ì†–¢GáBÜ&B¬Ü6%˜rÚ"¬ÉÉ≤í¢íí¢2í¬∆ñÊR¬bÖ4Â2¬2í¬EÖD“¬Ê6Ü˜#“v÷rê–¶f˜"Ç¬ñ“¬6ñ‚ÇÑ4‘$r¬&Vb¬}
›-ΩÌ“-ΩM]ΩÕmrí¿–¢Ñ4‘$r≤r≤4t¬Üˆ÷W5≤v6ˆÊÊV7FVBu“Ê6ˆÁfW'BÇu$t"rí¬}
çÕ=Ω˝mçÚ	˝	Ì	M	≠	Ω
Ì
}	]	›	‚ríì†–¢6&B“&˜VÊFVBÜñ“¬3Ç¢2ê–¢&BÁ&˜VÊFVE˜&V7FÊv∆RÇáÇ“2¢2¬5Dı“2¢2¬Ç≤r≤2¢2¬5Dı≤Ç≤2¢2í¿–¢&FóW3”C¢2¬fñ∆√“ÉSÇ¬CB¬#Çíê–¢&ˆ&BÁ7FRÜ6&B¬á&˜VÊBáÇí¬&˜VÊBÑ5Dıíí¬6&Bê–¢GáBÜ&B¬áÇ≤rÚ"¬5Dı≤Ç≤#B¢2í¬6¬bÖƒí¬#í¬tÙƒB¬Ê6Ü˜#“v÷rê–¶&ˆ&B“&ˆ&BÁ&W6ó¶RÇÜ6%˜rÚÚ"¬6%ˆÇÚÚ"í¬ñ÷vR‰ƒ‰5§ı2ê–§$Ù$B“7G"ÑıUDDï"Úv˜vÊW"÷Üˆ÷R÷6ˆ◊&ó6ˆ‚ÁÊrrê–¶&ˆ&BÁ6fRÑ$Ù$B¬u‰rr¬˜Fñ÷ó¶S’G'VRê–§$Ù$EÙ•r“7G"ÑıUDDï"Úv˜vÊW"÷Üˆ÷R÷6ˆ◊&ó6ˆ‚◊Êßrrê–¶&ˆ&E˜“&ˆ&BÁ&W6ó¶RÇÉCS¬&˜VÊBÜ&ˆ&BÊÜVñváB¢CSÚ&ˆ&BÁvñGFÇíí¬ñ÷vR‰ƒ‰5§ı2ê–¶&ˆ&E˜Á6fRÑ$Ù$EÙ•r¬t•Trr¬V∆óGì”C"¬˜Fñ÷ó¶S’G'VRê–¶&ˆ&E˜Ê6∆˜6RÇê–†–¢2)H)HMÌ≠}-]ΩÕ--‚Ìù]=‚67&ˆ∆¬÷˜vÊW#¢-]RÌMç›≠Ì"¬&V∆ñVbÇ˝ÌM˝çÇ]M="-Õ]-P–•4t“Cb¢3≤4‘$r“C¢3≤5Dı“C"¢3≤44“É"¢0–ß67&ˆ∆≈˜r“4‘$r¢"≤r¢"≤4t –ß67&ˆ∆≈ˆÇ“5Dı≤Ç≤44 –ß67&ˆ∆≈ˆ&ˆ&B“ñ÷vRÊÊWrÇu$t"r¬á67&ˆ∆≈˜r¬67&ˆ∆≈ˆÇí¬É2¬í¬bíê–ß6&B“ñ÷vTG&r‰G&rá67&ˆ∆≈ˆ&ˆ&Bê–ßGáBá6&B¬á67&ˆ∆≈˜rÚ"¬3¢2í¬tÜˆ÷R(	BfóÜVBÜW&ÚÇ]Mç›Ωí≠ÌΩ≤›çm›]íM]≠Çr¿–¢bÖƒí¬#Çí¬tÙƒB¬Ê6Ü˜#“v÷rê–¶f˜"í¬∆ñÊRñ‚VÁV÷W&FRÖ∞–¢}
Ω]-(	B›}Ω‚‚
˝-(	B≥cBG¢ΩÌ=Ì-çÚ¬≠ÌΩÕm‚Ç=ΩrÌ-Ì-Ú›Õ]-S≤r¿–¢v&2Ú6ˆÁF7G2Ú6ˆÁ6ˆ∆RÇ-RçR˝ÌM˝çÇÕ]ùÌ-Ú›ÌM›‚}›}]›çRÇ≠Ωç˝=Ì-Ú˝ÌB=]Ì]¬‚u“ì†–¢GáBá6&B¬á67&ˆ∆≈˜rÚ"¬És"≤í¢íí¢2í¬∆ñÊR¬bÖ4Â2¬2í¬EÖD“¬Ê6Ü˜#“v÷rê–¶f˜"Ç¬ñ“¬6ñ‚ÇÖ4‘$r¬Üˆ÷W5≤v6ˆÊÊV7FVBu“Ê6ˆÁfW'BÇu$t"rí¬}	›}Ω‚M]≠Çrí¿–¢Ö4‘$r≤r≤4t¬Üˆ÷U˜67&ˆ∆∆VBÊ6ˆÁfW'BÇu$t"rí¬}	M]≠˝Ì≠=}]››cBGríì†–¢6&B“&˜VÊFVBÜñ“¬3Ç¢2ê–¢6&BÁ&˜VÊFVE˜&V7FÊv∆RÇáÇ“2¢2¬5Dı“2¢2¬Ç≤r≤2¢2¬5Dı≤Ç≤2¢2í¿–¢&FóW3”C¢2¬fñ∆√“ÉSÇ¬CB¬#Çíê–¢67&ˆ∆≈ˆ&ˆ&BÁ7FRÜ6&B¬á&˜VÊBáÇí¬&˜VÊBÖ5Dıíí¬6&Bê–¢GáBá6&B¬áÇ≤rÚ"¬5Dı≤Ç≤#B¢2í¬6¬bÖƒí¬#í¬tÙƒB¬Ê6Ü˜#“v÷rê–ß67&ˆ∆≈ˆ&ˆ&B“67&ˆ∆≈ˆ&ˆ&BÁ&W6ó¶RÇá67&ˆ∆≈˜rÚÚ"¬67&ˆ∆≈ˆÇÚÚ"í¬ñ÷vR‰ƒ‰5§ı2ê–•45$Ùƒ≈Ù$Ù$B“7G"ÑıUDDï"Úv˜vÊW"÷Üˆ÷R◊67&ˆ∆¬◊&ˆˆbÁÊrrê–ß67&ˆ∆≈ˆ&ˆ&BÁ6fRÖ45$Ùƒ≈Ù$Ù$B¬u‰rr¬˜Fñ÷ó¶S’G'VRê–•45$Ùƒ≈Ù$Ù$EÙ•r“7G"ÑıUDDï"Úv˜vÊW"÷Üˆ÷R◊67&ˆ∆¬◊&ˆˆb◊Êßrrê–ß67&ˆ∆≈ˆ&ˆ&E˜“67&ˆ∆≈ˆ&ˆ&BÁ&W6ó¶RÇÉCS¬&˜VÊBá67&ˆ∆≈ˆ&ˆ&BÊÜVñváB¢CSÚ67&ˆ∆≈ˆ&ˆ&BÁvñGFÇíí¬ñ÷vR‰ƒ‰5§ı2ê–ß67&ˆ∆≈ˆ&ˆ&E˜Á6fRÖ45$Ùƒ≈Ù$Ù$EÙ•r¬t•Trr¬V∆óGì”C"¬˜Fñ÷ó¶S’G'VRê–ß67&ˆ∆≈ˆ&ˆ&E˜Ê6∆˜6RÇê–†–†–¢2)H)HΩç"-RÌ-Ì˝›çí=Ω} –§t“Cb¢3≤‘$r“C¢3≤DıÇ“S¢3≤4Ç“Ç¢0–ß6ÜVWE˜r“‘$r¢"≤r¢2≤t¢ –ß6ÜVWEˆÇ“DıÇ≤Ç≤4Ç≤3¢0–ß6ÜVWB“ñ÷vRÊÊWrÇu$t"r¬á6ÜVWE˜r¬6ÜVWEˆÇí¬É2¬í¬bíê–¶v∆˜r“ñ÷vRÊÊWrÇu$t"r¬á6ÜVWE˜r¬6ÜVWEˆÇí¬É2¬í¬bíê–§ñ÷vTG&r‰G&rÜv∆˜ríÊV∆∆ó6RÇá6ÜVWE˜r¢„R¬◊6ÜVWEˆÇ¢„3R¬6ÜVWE˜r¢„ÉR¬6ÜVWEˆÇ¢„CRí¿–¢fñ∆√“É3Ç¬#r¬Çíê–ß6ÜVWB“ñ÷vRÊ&∆VÊBá6ÜVWB¬v∆˜rÊfñ«FW"Ññ÷vTfñ«FW"‰vW76ñ‰&«W"Écíí¬„ÉRê–ß6B“ñ÷vTG&r‰G&rá6ÜVWBê–ßGáBá6B¬á6ÜVWE˜rÚ"¬3B¢2í¬t÷W7G&ıe‚(	BÜˆ÷R¬-ÇÌ-Ì˝›çÚ=Ω}ççÕ=Ω˝mçÚír¿–¢bÖƒí¬3í¬tÙƒB¬Ê6Ü˜#“v÷rê–¶f˜"í¬∆ñÊRñ‚VÁV÷W&FRÖ∞–¢}	›]˝ÌM-çm›≥¢ΩÌ=Ì-çÚ¬Õ]MΩÕÌ“¬ÕÌ}ç≠Çmç-Ìí=Ωr‚	›çmRçM="--=Ç≠-ç-›Ωí˝Ì-Ì≠Ì≤¬r¿–¢}-]Ω]MÌ“¬FV∆Vw&“Ú	Õ		≠
ÚvÜG4¬M==˝Ì-Ì≠ÌΩÌ"¬˝Ì≠=˝≠Ç›çm›˝Ú≠Ì›ÌΩ¬‚r¿–¢}	›çm›˝ÚM]≠çÕ]]"ÌMç“67&ˆ∆¬÷˜vÊW"MΩÚ]ΩÕ]M¬˝Ωç-Ì¢¬ç≠Ì›Ì¢Ç-]≠-‚
-Ì=‚›¬r¿–¢}››˝¬›≠ΩÌ›˝MÌ"Ç=Mç]›-›ÌíÕ≠ÇÌΩÕçR›]"‚u“ì†–¢GáBá6B¬á6ÜVWE˜rÚ"¬ÉsÇ≤í¢íí¢2í¬∆ñÊR¬bÖ4Â2¬2í¬EÖD“¬Ê6Ü˜#“v÷rê–ßá2“¥‘$r¬‘$r≤r≤t¬‘$r≤Ör≤tí¢%––¶f˜"Ç¬7Bñ‚¶óáá2¬5DDU2ì†–¢ñ““Üˆ÷W5∑7E“Ê6ˆÁfW'BÇu$t"rê–¢6&B“&˜VÊFVBÜñ“¬3Ç¢2ê–¢6BÁ&˜VÊFVE˜&V7FÊv∆RÇáÇ“2¢2¬DıÇ“2¢2¬Ç≤r≤2¢2¬DıÇ≤Ç≤2¢2í¿–¢&FóW3”C¢2¬fñ∆√“ÉSÇ¬CB¬#Çíê–¢6ÜVWBÁ7FRÜ6&B¬á&˜VÊBáÇí¬&˜VÊBÖDıÇíí¬6&Bê–¢GáBá6B¬áÇ≤rÚ"¬DıÇ≤Ç≤#b¢2í¬5DDUı%U∑7E“¬bÖƒí¬Çí¬tÙƒB¬Ê6Ü˜#“v÷rê–ß6ÜVWB“6ÜVWBÁ&W6ó¶RÇá6ÜVWE˜rÚÚ"¬6ÜVWEˆÇÚÚ"í¬ñ÷vR‰ƒ‰5§ı2ê–ß6ÜVWBÁ6fRÑıUB¬u‰rr¬˜Fñ÷ó¶S’G'VRê–†–¶f˜"Ê÷Rñ‚¥ıUB¬$Ù$B¬45$ÙƒƒTB¬45$Ùƒ≈Ù$Ù$B¬UîUıÑ4Uı4ÑTUE“≤∞–¢7G"ÑıUDDï"Úbv˜vÊW"÷Üˆ÷R◊∑7G“ÁÊrríf˜"7Bñ‚5DDU5”†–¢&ñÁBÇtÙ≤r¬Ê÷R¬bw∂˜2ÁFÇÊvWG6ó¶RÜÊ÷RíÛ#C¢„g“¥"rê–