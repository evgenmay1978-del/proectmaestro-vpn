#!/usr/bin/env python3
"""Small deterministic preview of the approved phone Home eye states.

This is an art-review aid, not a runtime renderer or an Android screenshot.
Only the approved owner reference, surround master, and open-eye anatomy are read.
"""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Iterable

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont


ROOT = Path(__file__).resolve().parents[1]
REFERENCE_PATH = ROOT / "design/mobile-4d-references/08-owner-installed-test-home-2026-08-08.jpg"
MATERIAL_PATH = ROOT / "design/mobile-asset-redraw/materials/mobile_eye_surround_c.png"
OPEN_EYE_PATH = ROOT / "app/src/main/res/drawable-nodpi/mobile_eye_open.webp"
REPO_FONT_PATH = ROOT / "app/src/main/res/font/playfair_display.ttf"

DP_SIZE = (390, 844)
STATES = ("disconnected", "connected")
MATERIAL_BOUNDS_DP = (78.5, 142.0, 311.5, 375.0)
STATUS_PATCH_BOUNDS_DP = (96.0, 426.0, 294.0, 480.0)
MASTER_4D_SIZE = (2160.0, 4670.0)
HERO_TRANSLATION_Y = -58.0
LIVING_EYE_STATE_W, LIVING_EYE_STATE_H = (890.0, 635.0)
LIVING_EYE_VIRTUAL_SIZE = 822.5
LIVING_EYE_UNIFORM_SCALE = 1.10
LIVING_EYE_OFFSET_DP = (3.5, 7.0)
SEAM_FROM_UPPER = 0.70
APERTURE_UPPER = (
    (388, 1083), (405, 1061), (430, 1037), (460, 1014), (500, 993), (540, 978),
    (580, 968), (620, 961), (660, 957), (700, 957), (740, 962), (780, 973),
    (820, 990), (860, 1011), (900, 1036), (932, 1061), (957, 1083),
)
APERTURE_LOWER = (
    (388, 1083), (420, 1104), (460, 1123), (500, 1139), (540, 1152), (580, 1162),
    (620, 1170), (660, 1174), (700, 1172), (740, 1167), (780, 1159), (820, 1148),
    (860, 1133), (900, 1115), (932, 1098), (957, 1083),
)
GLOW_INNER_EDGE = 0.82
GLOW_MAX_ALPHA = 0.22 * 0.7


def _scaled(value: float, scale: int) -> int:
    return round(value * scale)


def _font(size: int) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(REPO_FONT_PATH, size=size)


def load_reference(scale: int = 1) -> Image.Image:
    """Return the installed owner Home screenshot registered to 390×844 dp."""
    with Image.open(REFERENCE_PATH) as source:
        return source.convert("RGB").resize(
            (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)),
            Image.Resampling.LANCZOS,
        )

def _current_medallion_dp() -> tuple[float, float, float]:
    scale = max(DP_SIZE[0] / MASTER_4D_SIZE[0], DP_SIZE[1] / MASTER_4D_SIZE[1])
    translate_x = (DP_SIZE[0] - MASTER_4D_SIZE[0] * scale) / 2
    translate_y = (DP_SIZE[1] - MASTER_4D_SIZE[1] * scale) / 2
    center_x = 1080.0 * scale + translate_x
    center_y = 1751.0 * scale + translate_y + HERO_TRANSLATION_Y
    size = min(
        (MASTER_4D_SIZE[0] * 260.0 / 853.0) * scale,
        (MASTER_4D_SIZE[1] * 260.0 / 1844.0) * scale,
    ) * 2.0
    return center_x, center_y, size


def _state_geometry_dp() -> tuple[float, float, float, float, float, float, float]:
    center_x, center_y, medallion = _current_medallion_dp()
    state_width = medallion * LIVING_EYE_STATE_W / LIVING_EYE_VIRTUAL_SIZE * LIVING_EYE_UNIFORM_SCALE
    state_height = medallion * LIVING_EYE_STATE_H / LIVING_EYE_VIRTUAL_SIZE * LIVING_EYE_UNIFORM_SCALE
    left = center_x - state_width / 2 + LIVING_EYE_OFFSET_DP[0]
    top = center_y - state_height / 2 + LIVING_EYE_OFFSET_DP[1]
    return left, top, state_width, state_height, medallion, center_x, center_y


def eye_state_bounds_dp() -> tuple[float, float, float, float]:
    """Derive the approved 1.10 uniform fit from the production master viewport."""
    left, top, width, height, _medallion, _center_x, _center_y = _state_geometry_dp()
    return left, top, left + width, top + height


def _interpolate_contour_y(points: tuple[tuple[int, int], ...], x: int) -> float:
    for (x0, y0), (x1, y1) in zip(points, points[1:]):
        if x0 <= x <= x1:
            return y0 + (y1 - y0) * (x - x0) / (x1 - x0)
    if x == points[-1][0]:
        return float(points[-1][1])
    raise ValueError(f"source contour does not cover x={x}")


def aperture_contours_dp(closure: float) -> tuple[list[tuple[float, float]], list[tuple[float, float]]]:
    """Map the production contour samples through the shared 70/30 blink seam."""
    closure = max(0.0, min(1.0, closure))
    left, top, width, height, _medallion, _center_x, _center_y = _state_geometry_dp()
    upper, lower = [], []
    for source_x in sorted({x for x, _y in APERTURE_UPPER} | {x for x, _y in APERTURE_LOWER}):
        source_upper = _interpolate_contour_y(APERTURE_UPPER, source_x)
        source_lower = _interpolate_contour_y(APERTURE_LOWER, source_x)
        seam = source_upper + (source_lower - source_upper) * SEAM_FROM_UPPER
        current_upper = source_upper + (seam - source_upper) * closure
        current_lower = source_lower + (seam - source_lower) * closure
        x = left + (source_x - 230.0) / LIVING_EYE_STATE_W * width
        upper.append((x, top + (current_upper - 745.0) / LIVING_EYE_STATE_H * height))
        lower.append((x, top + (current_lower - 745.0) / LIVING_EYE_STATE_H * height))
    return upper, lower
def _contour_mask(scale: int, closure: float) -> Image.Image:
    mask = Image.new("L", (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)), 0)
    upper, lower = aperture_contours_dp(closure)
    polygon = [(_scaled(x, scale), _scaled(y, scale)) for x, y in upper + list(reversed(lower))]
    ImageDraw.Draw(mask).polygon(polygon, fill=255)
    return mask


def _material_mask(scale: int) -> Image.Image:
    mask = Image.new("L", (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)), 0)
    left, top, right, bottom = MATERIAL_BOUNDS_DP
    ImageDraw.Draw(mask).ellipse(
        (_scaled(left, scale), _scaled(top, scale), _scaled(right, scale), _scaled(bottom, scale)),
        fill=255,
    )
    # The master is feathered only inward so no original mosaic pixels leak at the edge.
    blurred = mask.filter(ImageFilter.GaussianBlur(radius=max(1, _scaled(1.5, scale))))
    return ImageChops.darker(mask, blurred)


def _status_patch_mask(scale: int) -> Image.Image:
    mask = Image.new("L", (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)), 0)
    left, top, right, bottom = (_scaled(value, scale) for value in STATUS_PATCH_BOUNDS_DP)
    inset = max(2, _scaled(4.0, scale))
    ImageDraw.Draw(mask).rounded_rectangle(
        (left + inset, top + inset, right - inset, bottom - inset),
        radius=max(2, _scaled(8.0, scale)), fill=255,
    )
    feathered = mask.filter(ImageFilter.GaussianBlur(radius=max(1, _scaled(3.0, scale))))
    boundary = Image.new("L", mask.size, 0)
    ImageDraw.Draw(boundary).rectangle((left, top, right, bottom), fill=255)
    return ImageChops.multiply(feathered, boundary)


def allowed_change_mask(scale: int = 1) -> Image.Image:
    """Exact material circle plus the feathered connected-status patch support."""
    mask = Image.new("L", (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)), 0)
    left, top, right, bottom = MATERIAL_BOUNDS_DP
    ImageDraw.Draw(mask).ellipse(
        (_scaled(left, scale), _scaled(top, scale), _scaled(right, scale), _scaled(bottom, scale)), fill=255
    )
    patch_support = _status_patch_mask(scale).point(lambda value: 255 if value else 0)
    return ImageChops.lighter(mask, patch_support)
def render_living_eye_layers(closure: float, scale: int = 2) -> tuple[Image.Image, Image.Image, Image.Image, Image.Image]:
    """Build anatomy, contact seam, aperture and annular glow as independent layers."""
    size = (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale))
    eye = Image.new("RGBA", size, (0, 0, 0, 0))
    seam = Image.new("RGBA", size, (0, 0, 0, 0))
    aperture = Image.new("L", size, 0)
    glow = Image.new("RGBA", size, (0, 0, 0, 0))
    if closure >= 1.0:
        upper, _lower = aperture_contours_dp(closure=1.0)
        points = [(_scaled(x, scale), _scaled(y, scale)) for x, y in upper]
        ImageDraw.Draw(seam).line(points, fill=(6, 20, 9, round(255 * 0.18)), width=max(1, round(_state_geometry_dp()[4] * scale * 3.0 / 520.0)), joint="curve")
        return eye, seam, aperture, glow

    aperture = _contour_mask(scale, closure)
    left, top, right, bottom = eye_state_bounds_dp()
    with Image.open(OPEN_EYE_PATH) as source:
        anatomy = source.convert("RGBA").resize(
            (_scaled(right - left, scale), _scaled(bottom - top, scale)), Image.Resampling.LANCZOS
        )
    eye.alpha_composite(anatomy, (_scaled(left, scale), _scaled(top, scale)))
    eye.putalpha(ImageChops.multiply(eye.getchannel("A"), aperture))
    upper, lower = aperture_contours_dp(closure)
    seam_draw = ImageDraw.Draw(seam)
    seam_draw.line([(_scaled(x, scale), _scaled(y, scale)) for x, y in upper], fill=(6, 20, 9, round(255 * 0.18)), width=max(1, round(_state_geometry_dp()[4] * scale * 3.0 / 520.0)))
    seam_draw.line([(_scaled(x, scale), _scaled(y, scale)) for x, y in lower], fill=(6, 20, 9, round(255 * 0.18)), width=max(1, round(_state_geometry_dp()[4] * scale * 3.0 / 520.0)))

    _left, _top, _width, _height, medallion, center_x, center_y = _state_geometry_dp()
    centre = (_scaled(center_x, scale), _scaled(center_y, scale))
    outer = _scaled(medallion * (0.5 - 26.0 / 520.0), scale)
    glow_draw = ImageDraw.Draw(glow)
    for band in range(18):
        t = band / 17
        radius = int(outer * (1.0 - t * (1.0 - GLOW_INNER_EDGE)))
        alpha = round(255 * GLOW_MAX_ALPHA * (1.0 - t) ** 2)
        glow_draw.ellipse(
            (centre[0] - radius, centre[1] - radius, centre[0] + radius, centre[1] + radius),
            outline=(46, 190, 108, alpha),
            width=max(1, round(_state_geometry_dp()[4] * scale * 3.0 / 520.0)),
        )
    return eye, seam, aperture, glow


def _replace_material(reference: Image.Image, scale: int) -> Image.Image:
    canvas = reference.convert("RGBA")
    left, top, right, bottom = MATERIAL_BOUNDS_DP
    with Image.open(MATERIAL_PATH) as source:
        material = source.convert("RGB").resize(
            (_scaled(right - left, scale), _scaled(bottom - top, scale)), Image.Resampling.LANCZOS
        )
    layer = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    layer.paste(material, (_scaled(left, scale), _scaled(top, scale)))
    layer.putalpha(_material_mask(scale))
    canvas.alpha_composite(layer)
    return canvas


def _clean_status_background(reference: Image.Image) -> Image.Image:
    """Inpaint the disconnected red ink before making a soft local wood patch."""
    source = reference.convert("RGB")
    width, height = source.size
    pixels = list(source.getdata())

    def is_old_status_red(pixel: tuple[int, int, int]) -> bool:
        red, green, blue = pixel
        return red >= 50 and red > green * 1.5 and red > blue * 1.5

    clean_pixels = [pixel for pixel in pixels if not is_old_status_red(pixel)]
    fallback = tuple(sum(pixel[channel] for pixel in clean_pixels) // len(clean_pixels) for channel in range(3))
    result = pixels[:]
    for index, pixel in enumerate(pixels):
        if not is_old_status_red(pixel):
            continue
        x, y = index % width, index // width
        samples: list[tuple[int, int, int]] = []
        for radius in range(1, 17):
            x0, x1 = max(0, x - radius), min(width, x + radius + 1)
            y0, y1 = max(0, y - radius), min(height, y + radius + 1)
            samples = [
                pixels[yy * width + xx]
                for yy in range(y0, y1)
                for xx in range(x0, x1)
                if not is_old_status_red(pixels[yy * width + xx])
            ]
            if samples:
                break
        result[index] = (
            sum(sample[0] for sample in samples) // len(samples),
            sum(sample[1] for sample in samples) // len(samples),
            sum(sample[2] for sample in samples) // len(samples),
        ) if samples else fallback
    cleaned = Image.new("RGB", source.size)
    cleaned.putdata(result)
    return cleaned


def _draw_connected_status(canvas: Image.Image, reference: Image.Image, scale: int) -> None:
    """Replace OFF ink with an inpainted, feathered local wood patch and connected labels."""
    patch_mask = _status_patch_mask(scale)
    left, top, right, bottom = (_scaled(value, scale) for value in STATUS_PATCH_BOUNDS_DP)
    source_patch = _clean_status_background(reference.crop((left, top, right, bottom))).filter(
        ImageFilter.GaussianBlur(radius=max(1, _scaled(9.0, scale)))
    )
    patch = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    patch.paste(source_patch.convert("RGBA"), (left, top))
    draw = ImageDraw.Draw(patch)
    status_font = _font(_scaled(15, scale))
    protocol_font = _font(_scaled(13, scale))
    green = (46, 190, 108, 255)
    draw.ellipse((_scaled(113, scale), _scaled(436, scale), _scaled(123, scale), _scaled(446, scale)), fill=green)
    _center_text(draw, _scaled(204, scale), _scaled(442, scale), "ПОДКЛЮЧЕНО", status_font, green)
    _center_text(draw, _scaled(195, scale), _scaled(466, scale), "Подключён: VLESS", protocol_font, (232, 163, 93, 255))
    patch.putalpha(patch_mask)
    canvas.alpha_composite(patch)
def _center_text(draw: ImageDraw.ImageDraw, x: int, y: int, text: str, font: ImageFont.ImageFont, fill: tuple[int, int, int, int]) -> None:
    bbox = draw.textbbox((0, 0), text, font=font)
    draw.text((x - (bbox[2] - bbox[0]) / 2, y - (bbox[3] - bbox[1]) / 2), text, font=font, fill=fill)


def render_home(state: str, scale: int = 2) -> Image.Image:
    """Render one approved state; this preview deliberately does not touch runtime code."""
    if state not in STATES:
        raise ValueError(f"Unknown state {state!r}; expected one of {STATES}")
    canvas = _replace_material(load_reference(scale), scale)
    closure = 1.0 if state == "disconnected" else 0.0
    eye, seam, _aperture, glow = render_living_eye_layers(closure=closure, scale=scale)
    canvas.alpha_composite(eye)
    canvas.alpha_composite(seam)
    if state == "connected":
        canvas.alpha_composite(glow)
        _draw_connected_status(canvas, load_reference(scale), scale)
    return canvas.convert("RGB")


def _comparison(scale: int) -> Image.Image:
    disconnected = render_home("disconnected", scale)
    connected = render_home("connected", scale)
    pad, header = _scaled(10, scale), _scaled(46, scale)
    board = Image.new("RGB", (disconnected.width * 2 + pad * 3, disconnected.height + header + pad * 2), (9, 20, 13))
    board.paste(disconnected, (pad, header + pad))
    board.paste(connected, (disconnected.width + pad * 2, header + pad))
    draw = ImageDraw.Draw(board)
    font = _font(_scaled(14, scale))
    title = _font(_scaled(12, scale))
    _center_text(draw, board.width // 2, _scaled(13, scale), "SCRIPTED PREVIEW — НЕ runtime screenshot", font, (207, 185, 130, 255))
    _center_text(draw, pad + disconnected.width // 2, _scaled(31, scale), "ОТКЛЮЧЕНО", title, (224, 104, 104, 255))
    _center_text(draw, disconnected.width + pad * 2 + connected.width // 2, _scaled(31, scale), "ПОДКЛЮЧЕНО", title, (91, 205, 133, 255))
    return board


def _expected_outputs(scale: int) -> dict[str, Image.Image]:
    return {
        "home-disconnected.png": render_home("disconnected", scale),
        "home-connected.png": render_home("connected", scale),
        "home-eye-states-comparison.png": _comparison(scale),
    }


def write_outputs(output_dir: Path, scale: int = 2) -> list[Path]:
    output_dir.mkdir(parents=True, exist_ok=True)
    paths: list[Path] = []
    for name, image in _expected_outputs(scale).items():
        path = output_dir / name
        image.save(path, format="PNG", optimize=False)
        paths.append(path)
    return paths


def check_outputs(output_dir: Path, scale: int = 2) -> int:
    """Return non-zero for absent or stale outputs, without creating or updating them."""
    for name, expected in _expected_outputs(scale).items():
        path = output_dir / name
        if not path.is_file():
            return 1
        with Image.open(path) as actual:
            if actual.convert("RGB").tobytes() != expected.tobytes() or actual.size != expected.size:
                return 1
    return 0


def main(argv: Iterable[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, default=ROOT / "build/mobile-eye-state-preview")
    parser.add_argument("--scale", type=int, default=2)
    parser.add_argument("--check", action="store_true", help="verify existing outputs without writing")
    args = parser.parse_args(argv)
    if args.scale < 1:
        parser.error("--scale must be at least 1")
    if args.check:
        return check_outputs(args.output_dir, args.scale)
    write_outputs(args.output_dir, args.scale)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
