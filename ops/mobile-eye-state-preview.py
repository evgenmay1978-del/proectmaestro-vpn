#!/usr/bin/env python3
"""Small deterministic preview of the approved phone Home eye states.

This is an art-review aid, not a runtime renderer or an Android screenshot.
Only approved owner references, the registered surround, and live anatomy are read.
"""

from __future__ import annotations

import argparse
import re
from functools import lru_cache
from pathlib import Path
from typing import Iterable

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont


ROOT = Path(__file__).resolve().parents[1]
REFERENCE_PATH = ROOT / "design/mobile-4d-references/10-owner-installed-home-2026-09-01.jpg"
MATERIAL_PATH = ROOT / "design/mobile-asset-redraw/materials/mobile_eye_surround_c.png"
ANATOMY_DIR = ROOT / "app/src/main/res/drawable-nodpi"
GEOMETRY_PATH = ROOT / "app/src/main/java/com/maestrovpn/tv/compose/screen/tvhome/LivingEyeLayerGeometry.kt"

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
APERTURE_UPPER = (
    (312.889, 1045.174), (356.632, 1024.760), (414.957, 995.015),
    (473.282, 967.602), (531.607, 948.355), (589.931, 937.856),
    (664.004, 933.774), (735.744, 939.606), (794.068, 954.187),
    (852.393, 974.018), (910.718, 997.931), (969.043, 1025.344),
    (1015.119, 1045.174),
)
APERTURE_LOWER = (
    (312.889, 1045.174), (356.632, 1060.338), (414.957, 1086.001),
    (473.282, 1109.331), (531.607, 1127.412), (589.931, 1140.243),
    (664.004, 1146.659), (735.744, 1141.410), (794.068, 1129.745),
    (852.393, 1111.664), (910.718, 1088.334), (969.043, 1063.255),
    (1015.119, 1045.174),
)
CLOSED_SEAM = (
    (312.889, 1045.174), (356.632, 1059.755), (414.957, 1079.002),
    (473.282, 1093.584), (531.607, 1104.665), (589.931, 1109.915),
    (664.004, 1111.664), (735.744, 1106.415), (794.068, 1098.833),
    (852.393, 1087.168), (910.718, 1074.336), (969.043, 1058.589),
    (1015.119, 1045.174),
)
GLOW_INNER_EDGE = 0.82
GLOW_OUTER_FADE_START = 0.98
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


def _interpolate_contour_y(points: tuple[tuple[float, float], ...], x: float) -> float:
    for (x0, y0), (x1, y1) in zip(points, points[1:]):
        if x0 <= x <= x1:
            return y0 + (y1 - y0) * (x - x0) / (x1 - x0)
    if x == points[-1][0]:
        return float(points[-1][1])
    raise ValueError(f"source contour does not cover x={x}")


def aperture_contours_dp(closure: float) -> tuple[list[tuple[float, float]], list[tuple[float, float]]]:
    """Map production contours onto the unchanged green master's existing closed fold."""
    closure = max(0.0, min(1.0, closure))
    left, top, width, height, _medallion, _center_x, _center_y = _state_geometry_dp()
    upper, lower = [], []
    for source_x in sorted({x for x, _y in APERTURE_UPPER} | {x for x, _y in APERTURE_LOWER}):
        source_upper = _interpolate_contour_y(APERTURE_UPPER, source_x)
        source_lower = _interpolate_contour_y(APERTURE_LOWER, source_x)
        seam = _interpolate_contour_y(CLOSED_SEAM, source_x)
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


@lru_cache(maxsize=2)
def _lash_specs(upper: bool) -> tuple[tuple[float, ...], ...]:
    """Use the actual runtime's authored follicles, not a second artistic parameter set."""
    name = "UPPER" if upper else "LOWER"
    source = GEOMETRY_PATH.read_text(encoding="utf-8")
    block = re.search(rf"LIVING_EYE_{name}_LASHES = listOf\((.*?)\n\)", source, re.S)
    if block is None:
        raise ValueError(f"Runtime {name.lower()} lash specifications are absent")
    specs = tuple(tuple(float(value.strip().removesuffix("f")) for value in row.split(","))
                  for row in re.findall(r"LivingEyeLashSpec\(([^)]+)\)", block.group(1)))
    if not specs or any(len(spec) != 5 for spec in specs):
        raise ValueError("Runtime lash specification shape changed")
    return specs


def lash_curves_dp(closure: float) -> list[tuple]:
    """Same cubic centreline and lid-root projection as livingEyeLashes."""
    phase = max(0.0, min(1.0, closure))
    upper_lid, lower_lid = aperture_contours_dp(phase)
    source_scale = _state_geometry_dp()[2] / LIVING_EYE_STATE_W
    curves = []
    for upper, lid in ((True, upper_lid), (False, lower_lid)):
        for fraction, length, sweep, width, alpha in _lash_specs(upper):
            x = lid[0][0] + (lid[-1][0] - lid[0][0]) * fraction
            y = _interpolate_contour_y(tuple(lid), x)
            length *= source_scale
            fan = (fraction - 0.46) * 1.85
            dx = length * (fan + sweep * 0.45)
            curl = length * sweep * 1.2
            dy = length * (-1 + 1.65 * phase if upper else 1)
            root_projection = length * (0.72 - 0.32 * phase if upper else -0.45)
            curves.append(((x, y), (x + dx * 0.38 - curl * 0.25, y + root_projection),
                           (x + dx * 0.78 + curl, y + dy * 0.88), (x + dx + curl * 0.20, y + dy * 0.68),
                           width * source_scale, min(1.0, alpha + 0.22) * (1 if upper else 1 - 0.7 * phase), upper))
    return curves


def render_eyelashes(closure: float, scale: int = 2) -> Image.Image:
    """Antialiased tapered ribbons, clipped only by the same bronze socket as runtime."""
    center_x, center_y, medallion = _current_medallion_dp()
    radius = medallion * (0.5 - 26.0 / 520.0)
    origin = (int((center_x - radius - 2) * scale), int((center_y - radius - 2) * scale))
    side = round((radius * 2 + 4) * scale)
    supersample = 3  # Supersample only the small socket, not the full phone screenshot.
    lashes = Image.new("RGBA", (side * supersample, side * supersample))

    def cubic(points: tuple, t: float) -> tuple[float, float]:
        weights = ((1 - t) ** 3, 3 * (1 - t) ** 2 * t, 3 * (1 - t) * t ** 2, t ** 3)
        return tuple(sum(weight * point[axis] for weight, point in zip(weights, points))
                     for axis in (0, 1))

    for root, control1, control2, tip, width, alpha, upper in lash_curves_dp(closure):
        half = width / 2
        edges = []
        for sign in (-1, 1):
            points = ((root[0] + sign * half, root[1]),
                      (control1[0] + sign * half * 0.88, control1[1]),
                      (control2[0] + sign * half * 0.50, control2[1]), tip)
            edges.append([cubic(points, index / 16) for index in range(17)])
        polygon = [((x * scale - origin[0]) * supersample, (y * scale - origin[1]) * supersample)
                   for x, y in edges[0] + list(reversed(edges[1]))]
        stroke = Image.new("RGBA", lashes.size)
        stroke_draw = ImageDraw.Draw(stroke)
        stroke_draw.polygon(polygon, fill=(8, 12, 6, round(alpha * 255)))
        ridge_points = [cubic((root, control1, control2, tip), 0.18 + index * 0.06)
                        for index in range(9)]
        ridge_edges = []
        for sign in (-1, 1):
            ridge_edges.append([
                ((x + sign * width * 0.09 * (index / 4 if index <= 4 else (8 - index) / 4))
                 * scale - origin[0], y * scale - origin[1])
                for index, (x, y) in enumerate(ridge_points)
            ])
        ridge_polygon = [(x * supersample, y * supersample)
                         for x, y in ridge_edges[0] + list(reversed(ridge_edges[1]))]
        # The ridge lies entirely inside the shaft; premix source-over here so ImageDraw's
        # replacement semantics preserve the opaque dark hair without another full-size layer.
        ridge_alpha = alpha * 0.64
        combined_alpha = ridge_alpha + alpha * (1 - ridge_alpha)
        ridge_colour = tuple(round((ridge * ridge_alpha + base * alpha * (1 - ridge_alpha))
                                   / combined_alpha)
                             for ridge, base in zip((82, 57, 29), (8, 12, 6)))
        stroke_draw.polygon(ridge_polygon, fill=ridge_colour + (round(combined_alpha * 255),))
        lashes.alpha_composite(stroke)

    bronze = Image.new("L", lashes.size)
    ImageDraw.Draw(bronze).ellipse(
        tuple((value * scale - origin[axis]) * supersample for value, axis in
              ((center_x - radius, 0), (center_y - radius, 1),
               (center_x + radius, 0), (center_y + radius, 1))), fill=255)
    lashes.putalpha(ImageChops.multiply(lashes.getchannel("A"), bronze))
    layer = Image.new("RGBA", (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale)))
    layer.alpha_composite(lashes.resize((side, side), Image.Resampling.LANCZOS), origin)
    return layer


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


def _draw_lid_occlusion(eye: Image.Image, aperture: Image.Image, closure: float, scale: int) -> None:
    """Match runtime cast shadow over the complete anatomy, not just the sclera sprite."""
    upper, lower = aperture_contours_dp(closure)
    shadow = Image.new("RGBA", eye.size)
    draw = ImageDraw.Draw(shadow)

    def band(start: float, end: float) -> list[tuple[int, int]]:
        def points(fraction: float) -> list[tuple[int, int]]:
            return [(_scaled(u[0], scale), _scaled(u[1] + (l[1] - u[1]) * fraction, scale))
                    for u, l in zip(upper, lower)]
        return points(start) + list(reversed(points(end)))

    for index in range(24):
        remaining = 1 - (index + 0.5) / 24
        draw.polygon(band(index / 24 * 0.30, (index + 1) / 24 * 0.30),
                     fill=(3, 9, 5, round(255 * 0.82 * remaining * remaining)))
    for index in range(12):
        remaining = 1 - (index + 0.5) / 12
        draw.polygon(band(1 - (index + 1) / 12 * 0.08, 1 - index / 12 * 0.08),
                     fill=(3, 9, 5, round(255 * 0.42 * remaining * remaining)))
    shadow.putalpha(ImageChops.multiply(shadow.getchannel("A"), aperture))
    eye.alpha_composite(shadow)
    wet = Image.new("RGBA", eye.size)
    ImageDraw.Draw(wet).line(
        [(_scaled(u[0], scale), _scaled(l[1] - (l[1] - u[1]) * 0.014, scale))
         for u, l in zip(upper, lower)],
        fill=(135, 147, 128, 42), width=max(1, round(scale * 0.45)),
    )
    wet.putalpha(ImageChops.multiply(wet.getchannel("A"), aperture))
    eye.alpha_composite(wet)


def render_living_eye_layers(closure: float, scale: int = 2) -> tuple[Image.Image, Image.Image, Image.Image, Image.Image]:
    """Build live anatomy, transient aperture seam, aperture and annular glow."""
    phase = max(0.0, min(1.0, closure))
    size = (_scaled(DP_SIZE[0], scale), _scaled(DP_SIZE[1], scale))
    eye = Image.new("RGBA", size, (0, 0, 0, 0))
    seam = Image.new("RGBA", size, (0, 0, 0, 0))
    aperture = Image.new("L", size, 0)
    glow = Image.new("RGBA", size, (0, 0, 0, 0))
    left, top, right, bottom = eye_state_bounds_dp()
    factor = (right - left) / LIVING_EYE_STATE_W * scale

    def position(x: float, y: float) -> tuple[int, int]:
        return (_scaled(left, scale) + round((x - 230.0) * factor),
                _scaled(top, scale) + round((y - 745.0) * factor))

    def draw_layer(name: str, x: float, y: float, width: float, height: float) -> None:
        with Image.open(ANATOMY_DIR / name) as source:
            sprite = source.convert("RGBA").resize(
                (round(width * factor), round(height * factor)), Image.Resampling.LANCZOS
            )
        eye.alpha_composite(sprite, position(x, y))

    _left, _top, _width, _height, medallion, center_x, center_y = _state_geometry_dp()
    bronze = Image.new("L", size, 0)
    bronze_radius = medallion * (0.5 - 26.0 / 520.0) * scale
    ImageDraw.Draw(bronze).ellipse(
        (center_x * scale - bronze_radius, center_y * scale - bronze_radius,
         center_x * scale + bronze_radius, center_y * scale + bronze_radius), fill=255
    )

    if phase < 0.999:
        aperture = ImageChops.multiply(_contour_mask(scale, phase), bronze)
        draw_layer("mobile_eye_sclera.webp", 300, 900, 740, 300)
        draw_layer("mobile_eye_iris.webp", 535, 900, 292, 292)
        # Match the neutral runtime pupil shader instead of baking a flat circle in the preview.
        pupil = position(681, 1045)
        radius = 54 * factor
        draw = ImageDraw.Draw(eye)
        draw.ellipse((pupil[0] - radius - 3 * factor, pupil[1] - radius - 3 * factor,
                      pupil[0] + radius + 3 * factor, pupil[1] + radius + 3 * factor),
                     fill=(10, 36, 20, 255))
        for r in range(round(radius), 0, -1):
            fraction = r / radius
            if fraction < 0.5:
                t = fraction * 2
                colour = (round(t), round(1 + 2 * t), round(2 * t), 255)
            else:
                t = (fraction - 0.5) * 2
                colour = (round(1 + 6 * t), round(3 + 18 * t), round(2 + 10 * t), 255)
            draw.ellipse((pupil[0] - r, pupil[1] - r, pupil[0] + r, pupil[1] + r), fill=colour)
        draw_layer("mobile_eye_catchlight.webp", 635, 945, 90, 90)
        _draw_lid_occlusion(eye, aperture, phase, scale)
        eye.putalpha(ImageChops.multiply(eye.getchannel("A"), aperture))

    upper, lower = aperture_contours_dp(phase)
    seam_draw = ImageDraw.Draw(seam)
    seam_width = max(1, round(_state_geometry_dp()[4] * scale * 3.0 / 520.0))
    seam_alpha = round(255 * 0.18 * (1.0 - phase))
    if seam_alpha > 0:
        seam_fill = (6, 20, 9, seam_alpha)
        seam_draw.line(
            [(_scaled(x, scale), _scaled(y, scale)) for x, y in upper],
            fill=seam_fill,
            width=seam_width,
            joint="curve",
        )
        seam_draw.line(
            [(_scaled(x, scale), _scaled(y, scale)) for x, y in lower],
            fill=seam_fill,
            width=seam_width,
            joint="curve",
        )

    seam.putalpha(ImageChops.multiply(seam.getchannel("A"), bronze))

    if phase < 0.999:
        _left, _top, _width, _height, medallion, center_x, center_y = _state_geometry_dp()
        centre = (_scaled(center_x, scale), _scaled(center_y, scale))
        outer = _scaled(medallion * (0.5 - 26.0 / 520.0), scale)
        glow_draw = ImageDraw.Draw(glow)
        midpoint = (GLOW_INNER_EDGE + 1.0) / 2.0
        fade_start_alpha = 0.25 + 0.75 * (GLOW_OUTER_FADE_START - midpoint) / (1.0 - midpoint)
        for band in range(18):
            t = band / 17
            fraction = 1.0 - t * (1.0 - GLOW_INNER_EDGE)
            radius = int(outer * fraction)
            # Match the runtime radial stops, including the transparent outer edge.
            if fraction > GLOW_OUTER_FADE_START:
                strength = fade_start_alpha * (1.0 - fraction) / (1.0 - GLOW_OUTER_FADE_START)
            elif fraction > midpoint:
                strength = 0.25 + 0.75 * (fraction - midpoint) / (1.0 - midpoint)
            else:
                strength = 0.25 * (fraction - GLOW_INNER_EDGE) / (midpoint - GLOW_INNER_EDGE)
            alpha = round(255 * GLOW_MAX_ALPHA * strength)
            glow_draw.ellipse(
                (centre[0] - radius, centre[1] - radius, centre[0] + radius, centre[1] + radius),
                outline=(46, 190, 108, alpha),
                width=seam_width,
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
    canvas.alpha_composite(render_eyelashes(closure, scale))
    if state == "connected":
        canvas.alpha_composite(glow)
        _draw_connected_status(canvas, load_reference(scale), scale)
    return canvas.convert("RGB")


def _comparison(scale: int) -> Image.Image:
    disconnected = render_home("disconnected", scale)
    connected = render_home("connected", scale)
    pad, header = _scaled(10, scale), _scaled(61, scale)
    board = Image.new("RGB", (disconnected.width * 2 + pad * 3, disconnected.height + header + pad * 2), (9, 20, 13))
    board.paste(disconnected, (pad, header + pad))
    board.paste(connected, (disconnected.width + pad * 2, header + pad))
    draw = ImageDraw.Draw(board)
    font = _font(_scaled(14, scale))
    title = _font(_scaled(12, scale))
    _center_text(draw, board.width // 2, _scaled(13, scale), "ПРЕДПРОСМОТР — НЕ СНИМОК ПРИЛОЖЕНИЯ", font, (207, 185, 130, 255))
    _center_text(draw, board.width // 2, _scaled(29, scale), "Основа: ваш экран от 01.09.2026", _font(_scaled(9, scale)), (207, 185, 130, 255))
    _center_text(draw, pad + disconnected.width // 2, _scaled(46, scale), "ОТКЛЮЧЕНО", title, (224, 104, 104, 255))
    _center_text(draw, disconnected.width + pad * 2 + connected.width // 2, _scaled(46, scale), "ПОДКЛЮЧЕНО", title, (91, 205, 133, 255))
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
