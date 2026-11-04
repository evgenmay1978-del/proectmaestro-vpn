#!/usr/bin/env python3
"""Rebuild the phone plates as plain wood: no ornament rails, no vines.

Owner request (04.11.2026): "вокруг кнопки узор нужно убрать, оставить текстуру дерева,
подогнать так, чтобы смотрелось всё единым дизайном".

The plate outline keeps the original nine-patch markers and canvas size; only the
interior artwork changes: the wood grain of the approved plate is re-used, the gold
rails/vines/gems are replaced by a soft rounded recess.
"""
import sys
from PIL import Image, ImageDraw, ImageFilter

RES = "app/src/main/res/drawable-nodpi"

PLATES = {
    # name: (wood sample box inside the original plate, corner radius in content px)
    "mobile_phone_panel.9.png": ((80, 26, 630, 104), 12),
    "mobile_phone_bar.9.png": ((230, 48, 598, 148), 14),
}


def shade(size, radius):
    """Rounded recess shading: transparent corners, dark rim, soft top light."""
    w, h = size
    mask = Image.new("L", (w, h), 0)
    d = ImageDraw.Draw(mask)
    d.rounded_rectangle((0, 0, w - 1, h - 1), radius=radius, fill=255)
    mask = mask.filter(ImageFilter.GaussianBlur(0.6))

    overlay = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    od = ImageDraw.Draw(overlay)
    # soft inner shadow along the bottom and the sides
    od.rectangle((0, h - 7, w - 1, h - 1), fill=(0, 0, 0, 60))
    od.rectangle((0, 0, 3, h - 1), fill=(0, 0, 0, 45))
    od.rectangle((w - 4, 0, w - 1, h - 1), fill=(0, 0, 0, 45))
    overlay = overlay.filter(ImageFilter.GaussianBlur(3.0))
    # crisp rim
    rim = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    rd = ImageDraw.Draw(rim)
    rd.rounded_rectangle((0, 0, w - 1, h - 1), radius=radius, outline=(0, 0, 0, 120), width=1)
    # a whisper of light on the top edge keeps the plate readable on dark wood
    light = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    ld = ImageDraw.Draw(light)
    ld.rounded_rectangle((1, 1, w - 2, h - 2), radius=radius, outline=(255, 226, 176, 26), width=1)
    return mask, overlay, rim, light


def rebuild(path, sample_box, radius):
    src = Image.open(path).convert("RGBA")
    w, h = src.size
    cw, ch = w - 2, h - 2
    wood = src.crop(sample_box).resize((cw, ch), Image.LANCZOS)
    mask, overlay, rim, light = shade((cw, ch), radius)
    content = Image.new("RGBA", (cw, ch), (0, 0, 0, 0))
    content.paste(wood, (0, 0))
    content = Image.alpha_composite(content, overlay)
    content = Image.alpha_composite(content, rim)
    content = Image.alpha_composite(content, light)
    content.putalpha(mask)

    out = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    out.paste(content, (1, 1))
    # nine-patch markers survive untouched
    out.paste(src.crop((0, 0, w, 1)), (0, 0))
    out.paste(src.crop((0, h - 1, w, h)), (0, h - 1))
    out.paste(src.crop((0, 0, 1, h)), (0, 0))
    out.paste(src.crop((w - 1, 0, w, h)), (w - 1, 0))
    out.save(path)
    print("rebuilt", path, out.size)


if __name__ == "__main__":
    for name, (box, radius) in PLATES.items():
        rebuild(sys.argv[1] + "/" + name if len(sys.argv) > 1 else RES + "/" + name, box, radius)
