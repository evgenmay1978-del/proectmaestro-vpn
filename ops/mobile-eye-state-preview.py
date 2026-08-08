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
REFERENCE_PATH = ROOT / "design/mobile-4d-references/04-owner-selected-home-2026-07-31.jpg"
MATERIAL_PATH = ROOT / "design/mobile-asset-redraw/materials/mobile_eye_surround_c.png"
OPEN_EYE_PATH = ROOT / "app/src/main/res/drawable-nodpi/mobile_eye_open.webp"

DP_SIZE = (390, 844)
STATES = ("disconnected", "connected")
MATERIAL_BOUNDS_DP = (78.5, 142.0, 311.5, 375.0)
STATE_BOUNDS_DP = (56.8716, 164.4053, 340.1284, 366.5043)
APERTURE_LEFT = 107.158
APERTURE_RIGHT = 288.251
SEAM_END_Y = 271.979
SEAM_MID_Y = 280.222
APERTURE_TOP = 231.878
APERTURE_BOTTOM = 300.942
GLOW_INNER_EDGE = 0.82
GLOW_MAX_ALPHA = 0.22 * 0.7


def _scaled(value: float, scale: int) -> int:
    return round(value * scale)


def _font(size: int) -> ImageFont.FreeTypeFont | ImageFont.ImageFont:
    for candidate in (
        "C:/Windows/Fonts/arialbd.ttf",
        "C:/Windows/Fonts/arial.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
    ):
        try:
            return ImageFont.truetype(candidate, size=size)
        except OSError:
            pass
    return ImageFont.load_default()


def load_reference(scale: int = 1) -> Image.Image:
    """Return the owner reference registered to the 390x844 dp viewport."""
    with Image.open(REFERENCE_PATH) as source:
        return source.convert("RGB").resize(
            (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)),
            Image.Resampling.LANCZOS,
        )


def eye_state_bounds_dp() -> tuple[float, float, float, float]:
    """Owner-approved 1.10 uniform fit with its (+3.5,+7) dp shift applied."""
    return STATE_BOUNDS_DP


def _seam_points() -> list[tuple[float, float]]:
    # 19 points match the lightweight contact contour used by the preview.
    xs = [APERTURE_LEFT + (APERTURE_RIGHT - APERTURE_LEFT) * index / 18 for index in range(19)]
    xs[9] = 193.726
    points: list[tuple[float, float]] = []
    for index, x in enumerate(xs):
        t = index / 18
        y = SEAM_END_Y + (SEAM_MID_Y - SEAM_END_Y) * (1.0 - (2.0 * t - 1.0) ** 2)
        points.append((x, y))
    points[9] = (193.726, SEAM_MID_Y)
    return points


def aperture_contours_dp(closure: float) -> tuple[list[tuple[float, float]], list[tuple[float, float]]]:
    """Return the shared 70/30 aperture contours for a blink closure [0, 1]."""
    closure = max(0.0, min(1.0, closure))
    seam = _seam_points()
    upper: list[tuple[float, float]] = []
    lower: list[tuple[float, float]] = []
    for index, (x, seam_y) in enumerate(seam):
        t = index / 18
        lobe = 1.0 - (2.0 * t - 1.0) ** 2
        upper_open = seam_y - (SEAM_MID_Y - APERTURE_TOP) * lobe
        lower_open = seam_y + (APERTURE_BOTTOM - SEAM_MID_Y) * lobe
        upper.append((x, seam_y * closure + upper_open * (1.0 - closure)))
        lower.append((x, seam_y * closure + lower_open * (1.0 - closure)))
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


def allowed_change_mask(scale: int = 1) -> Image.Image:
    """The hero replacement and status rows are the only mutable reference pixels."""
    mask = _material_mask(scale)
    draw = ImageDraw.Draw(mask)
    # The material replacement owns its complete registered box; anatomy/glow overhang it.
    draw.rectangle(tuple(_scaled(value, scale) for value in MATERIAL_BOUNDS_DP), fill=255)
    draw.rectangle(tuple(_scaled(value, scale) for value in (55, 150, 343, 370)), fill=255)
    draw.rectangle((_scaled(58, scale), _scaled(360, scale), _scaled(332, scale), _scaled(408, scale)), fill=255)
    return mask


def render_living_eye_layers(closure: float, scale: int = 2) -> tuple[Image.Image, Image.Image, Image.Image, Image.Image]:
    """Build anatomy, contact seam, aperture and annular glow as independent layers."""
    size = (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale))
    eye = Image.new("RGBA", size, (0, 0, 0, 0))
    seam = Image.new("RGBA", size, (0, 0, 0, 0))
    aperture = Image.new("L", size, 0)
    glow = Image.new("RGBA", size, (0, 0, 0, 0))
    if closure >= 1.0:
        points = [(_scaled(x, scale), _scaled(y, scale)) for x, y in _seam_points()]
        ImageDraw.Draw(seam).line(points, fill=(6, 20, 9, round(255 * 0.18)), width=max(1, _scaled(1.25, scale)), joint="curve")
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
    seam_draw.line([(_scaled(x, scale), _scaled(y, scale)) for x, y in upper], fill=(6, 20, 9, 35), width=max(1, _scaled(0.65, scale)))
    seam_draw.line([(_scaled(x, scale), _scaled(y, scale)) for x, y in lower], fill=(6, 20, 9, 35), width=max(1, _scaled(0.65, scale)))

    centre = (_scaled(195.0, scale), _scaled(258.45, scale))
    outer = _scaled(107.0, scale)
    glow_draw = ImageDraw.Draw(glow)
    for band in range(18):
        t = band / 17
        radius = int(outer * (1.0 - t * (1.0 - GLOW_INNER_EDGE)))
        alpha = round(255 * GLOW_MAX_ALPHA * (1.0 - t) ** 2)
        glow_draw.ellipse(
            (centre[0] - radius, centre[1] - radius, centre[0] + radius, centre[1] + radius),
            outline=(46, 190, 108, alpha),
            width=max(1, _scaled(1.25, scale)),
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


def _draw_disconnected_status(canvas: Image.Image, scale: int) -> None:
    draw = ImageDraw.Draw(canvas)
    box = (_scaled(58, scale), _scaled(360, scale), _scaled(332, scale), _scaled(408, scale))
    draw.rectangle(box, fill=(5, 17, 10, 255))
    status_font = _font(_scaled(16, scale))
    protocol_font = _font(_scaled(14, scale))
    red = (207, 70, 70, 255)
    draw.ellipse((_scaled(117, scale), _scaled(368, scale), _scaled(128, scale), _scaled(379, scale)), fill=red)
    _center_text(draw, _scaled(202, scale), _scaled(374, scale), "ОТКЛЮЧЕНО", status_font, red)
    _center_text(draw, _scaled(195, scale), _scaled(397, scale), "Отключён: VLESS", protocol_font, (232, 163, 93, 255))


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
    else:
        _draw_disconnected_status(canvas, scale)
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
