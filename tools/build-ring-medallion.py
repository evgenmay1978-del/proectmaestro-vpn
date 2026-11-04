
import math, random
from PIL import Image, ImageDraw, ImageFilter, ImageChops

S = 1024
C = S // 2
R = 372          # outer radius of the disc (matches app geometry 0.7266 * 512)

def lerp(a, b, t): return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))

def leather(size=S):
    random.seed(7)
    base = Image.new("RGB", (size, size), (26, 18, 12))
    px = base.load()
    # fine grain noise
    for y in range(size):
        for x in range(size):
            n = random.randint(-7, 7)
            r, g, b = px[x, y]
            px[x, y] = (max(0, min(255, r + n)), max(0, min(255, g + n)), max(0, min(255, b + int(n * 0.75))))
    # quilted diamond pattern (subtle)
    q = Image.new("L", (size, size), 0)
    d = ImageDraw.Draw(q)
    step = 78
    for k in range(-size, size * 2, step):
        d.line([(k, 0), (k + size, size)], fill=26, width=2)
        d.line([(k + size, 0), (k, size)], fill=26, width=2)
    q = q.filter(ImageFilter.GaussianBlur(1.2))
    base = ImageChops.add(base, Image.merge("RGB", (q, q, q)))
    return base

def radial_shade(img, inner=(1.0, 1.02, 1.0), outer=(0.28, 0.24, 0.22)):
    mask = Image.new("L", (S, S), 0)
    d = ImageDraw.Draw(mask)
    for i in range(120, 0, -1):
        t = i / 120.0
        col = int(255 * (1 - t) ** 1.3)
        d.ellipse([C - R * t, C - R * t, C + R * t, C + R * t], fill=col)
    mask = mask.filter(ImageFilter.GaussianBlur(18))
    dark = Image.new("RGB", (S, S), (0, 0, 0))
    lum = Image.new("RGB", (S, S), (38, 26, 17))
    # multiply-blend a vignette
    out = img.copy()
    vig = Image.new("L", (S, S), 0)
    dv = ImageDraw.Draw(vig)
    for rr in range(R, 0, -1):
        v = 255 - int(150 * ((R - rr) / R) ** 2)
        dv.ellipse([C - rr, C - rr, C + rr, C + rr], fill=v)
    vig = vig.filter(ImageFilter.GaussianBlur(26))
    out = Image.composite(img, Image.new("RGB", (S, S), (12, 8, 5)), vig)
    return out

def ring_mask(outer, inner):
    m = Image.new("L", (S, S), 0)
    d = ImageDraw.Draw(m)
    d.ellipse([C - outer, C - outer, C + outer, C + outer], fill=255)
    d.ellipse([C - inner, C - inner, C + inner, C + inner], fill=0)
    return m

def gold_gradient(warm=(250, 226, 164), mid=(206, 160, 84), deep=(120, 84, 36)):
    g = Image.new("RGB", (S, S))
    px = g.load()
    for y in range(S):
        for x in range(S):
            dx = (x - C) / R
            dy = (y - C) / R
            t = max(0.0, min(1.0, (0.5 - 0.5 * dy) * 0.75 + 0.5 - 0.5 * math.hypot(dx, dy) * 0.25))
            # angular highlight: light from upper-left
            ang = math.atan2(dy, dx)
            hi = 0.5 + 0.5 * math.cos(ang + math.pi * 0.75)
            t = min(1.0, t * 0.7 + hi * 0.45)
            if t < 0.5:
                col = lerp(deep, mid, t * 2)
            else:
                col = lerp(mid, warm, (t - 0.5) * 2)
            px[x, y] = col
    return g

def emerald(size=44):
    im = Image.new("RGBA", (size * 3, size * 3), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    k = size * 3 // 2
    # gold bezel: diamond
    d.polygon([(k, k - size), (k + size, k), (k, k + size), (k - size, k)], fill=(214, 172, 96, 255))
    d.polygon([(k, k - size + 3), (k + size - 3, k), (k, k + size - 3), (k - size + 3, k)], fill=(122, 88, 40, 255))
    # gem
    g = size - 9
    pts = [(k, k - g), (k + g, k), (k, k + g), (k - g, k)]
    d.polygon(pts, fill=(24, 132, 72, 255))
    # facets
    d.polygon([(k, k - g), (k + g // 2, k - g // 4), (k, k), (k - g // 2, k - g // 4)], fill=(86, 226, 140, 255))
    d.polygon([(k, k - g), (k - g // 2, k - g // 4), (k, k), (k + g // 3, k - g // 3)], fill=(150, 246, 190, 255))
    d.polygon([(k - g, k), (k - g // 2, k - g // 4), (k, k), (k - g // 2, k + g // 3)], fill=(46, 176, 104, 255))
    d.polygon([(k, k + g), (k + g // 2, k + g // 3), (k, k), (k - g // 2, k + g // 3)], fill=(14, 96, 52, 255))
    d.polygon([(k + g, k), (k + g // 2, k + g // 3), (k, k), (k + g // 2, k - g // 4)], fill=(30, 148, 84, 255))
    return im

def build(on=True):
    tex = leather()
    # clip leather to disc
    disc = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    mask = Image.new("L", (S, S), 0)
    ImageDraw.Draw(mask).ellipse([C - R + 2, C - R + 2, C + R - 2, C + R - 2], fill=255)
    tex = radial_shade(tex)
    disc.paste(tex, (0, 0), mask)

    # dark gap + gold band + inner hairline
    gold = gold_gradient()
    if not on:
        gold = Image.eval(gold, lambda v: int(v * 0.62))
    band = ring_mask(R - 3, R - 27)
    disc.paste(gold, (0, 0), band)
    hair = ring_mask(R - 33, R - 44)
    hair_gold = gold_gradient(warm=(226, 196, 132), mid=(168, 126, 62), deep=(96, 66, 28))
    if not on:
        hair_gold = Image.eval(hair_gold, lambda v: int(v * 0.6))
    disc.paste(hair_gold, (0, 0), hair)
    # inner bevel shading inside the leather
    bev = Image.new("L", (S, S), 0)
    db = ImageDraw.Draw(bev)
    for i in range(14):
        v = int(90 * (1 - i / 14.0))
        db.ellipse([C - (R - 45) + i, C - (R - 45) + i, C + (R - 45) - i, C + (R - 45) - i], outline=v, width=1)
    bev = bev.filter(ImageFilter.GaussianBlur(6))
    dark = Image.new("RGBA", (S, S), (8, 5, 3, 255))
    disc = Image.composite(dark, disc, bev)

    # emerald cabochons at 4 cardinal points
    for ang in (0, 90, 180, 270):
        rad = math.radians(ang - 90)
        cx = C + int(math.cos(rad) * (R - 15))
        cy = C + int(math.sin(rad) * (R - 15))
        gem = emerald(30 if on else 30)
        if not on:
            gem = Image.eval(gem.convert("RGB"), lambda v: int(v * 0.62)).convert("RGBA")
        disc.alpha_composite(gem, (cx - 45, cy - 45))
    return disc

for name, on in (("mobile_medallion_disc_on.png", True), ("mobile_medallion_disc_off.png", False)):
    img = build(on)
    img.save("/root/maestro/work/main-sync/app/src/main/res/drawable-nodpi/" + name)
    img.save("/root/maestro/work/tmp/ui/medallion_" + ("on" if on else "off") + ".png")
print("ok")

# preview: composite disc + M sprite on wood
wood = Image.open("/root/maestro/work/main-sync/app/src/main/res/drawable-nodpi/phone_home_wood.png").convert("RGB")
w, h = wood.size
canvas = wood.crop((0, 0, 720, min(h, 720))).resize((720, 720))
disc = Image.open("/root/maestro/work/tmp/ui/medallion_on.png")
scale = 412 / 1024.0
d2 = disc.resize((412, 412), Image.LANCZOS)
canvas.paste(d2, (154, 154), d2)
m = Image.open("/root/maestro/work/main-sync/app/src/main/res/drawable-nodpi/mobile_medallion_m_idle.webp").convert("RGBA")
mw = int(412 * 0.75); mh = int(412 * 0.53125)
m = m.resize((mw, mh), Image.LANCZOS)
canvas.paste(m, (154 + int(412 * 0.125), 154 + int(412 * 0.2734375)), m)
canvas.save("/root/maestro/work/tmp/ui/medallion_preview.png")
print("preview ok")
