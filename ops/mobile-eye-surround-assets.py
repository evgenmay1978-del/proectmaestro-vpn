#!/usr/bin/env python3
"""Rebuild the three home ring eye-surround material variants."""

from __future__ import annotations

import argparse
import hashlib
import math
import sys
from pathlib import Path

from PIL import Image, ImageEnhance, ImageOps, ImageDraw


ROOT = Path(__file__).resolve().parents[1]
MATERIALS = ROOT / "design" / "mobile-asset-redraw" / "materials"
KIT = ROOT / "design" / "mobile-asset-redraw" / "kit"
SOURCE = ROOT / "design" / "mobile-asset-redraw" / "source"
MASTER = MATERIALS / "mobile_eye_surround_c.png"

MASTER_SIZE = (2160, 4670)
MATERIAL_CENTER = (1080, 1751)
MATERIAL_RADIUS = 644
MATERIAL_FEATHER = 6
MATERIAL_GUARD_RADIUS = 650
LIGHTS = ("l", "c", "r")

MASTER_MATERIAL_SIZE = (1254, 1254)
MASTER_MATERIAL_SHA256 = (
    "bcd108a3c53a1f18b1d1a984c56105e315bc86a581c4fe05efc5b76f2b0eeeac"
)
EXPECTED_RING_OUTSIDE_SHA256 = {
    "l": "9917a32afe8f1532c001e39c39c4fd37eabe13f68d589b0f8fc68e82453f4b03",
    "c": "9a708bd0527be69304776c37b9a7e26a15c54dc8c2084b427b52fa44450ccf43",
    "r": "f6beeabaff79fced29109a2e8f00272fdaae3aa27d946d75a600d0bca254d53e",
}


def replacement_mask(radius: int = 644, feather: int = 6) -> Image.Image:
    """Return a local circular mask whose feather lies wholly inside the circle."""
    if radius < 1:
        raise ValueError("radius must be positive")
    if feather < 0 or feather > radius:
        raise ValueError("feather must be between zero and radius")

    diameter = radius * 2 + 1
    inner = radius - feather
    values = bytearray(diameter * diameter)
    for y in range(diameter):
        dy = y - radius
        for x in range(diameter):
            distance = math.hypot(x - radius, dy)
            if distance <= inner or feather == 0 and distance <= radius:
                value = 255
            elif distance <= radius:
                value = round(255 * (radius + 1 - distance) / (feather + 1))
            else:
                value = 0
            values[y * diameter + x] = value
    return Image.frombytes("L", (diameter, diameter), bytes(values))


def directional_material(
    master: Image.Image,
    light: str,
    size: tuple[int, int],
) -> Image.Image:
    """Resize one master and apply only a mirrored horizontal brightness field."""
    if light not in LIGHTS:
        raise ValueError(f"unknown light {light!r}; expected one of {LIGHTS}")
    if size[0] < 1 or size[1] < 1:
        raise ValueError("material size must be positive")

    resized = master.convert("RGB").resize(size, Image.Resampling.LANCZOS)
    if light == "c":
        return resized

    low = ImageEnhance.Brightness(resized).enhance(0.88)
    high = ImageEnhance.Brightness(resized).enhance(1.10)
    if size[0] == 1:
        gradient = Image.new("L", size, 128)
    else:
        ramp = Image.linear_gradient("L").rotate(90, expand=True).resize(
            size,
            Image.Resampling.BILINEAR,
        )
        gradient = ImageOps.mirror(ramp) if light == "l" else ramp
    return Image.composite(high, low, gradient)


def _hard_circle_mask(radius: int) -> Image.Image:
    diameter = radius * 2 + 1
    radius_squared = radius * radius
    return Image.frombytes(
        "L",
        (diameter, diameter),
        bytes(
            255 if (x - radius) ** 2 + (y - radius) ** 2 <= radius_squared else 0
            for y in range(diameter)
            for x in range(diameter)
        ),
    )


def _canonical_backdrop(
    base_rgb: Image.Image,
    center: tuple[int, int],
    radius: int,
) -> Image.Image:
    """Project every local pixel to unchanged RGB at the same angle on r=radius+1."""
    cx, cy = center
    sample_radius = radius + 1
    diameter = radius * 2 + 1
    source = base_rgb.load()
    pixels: list[tuple[int, int, int]] = []
    for y in range(diameter):
        dy = y - radius
        for x in range(diameter):
            dx = x - radius
            distance = math.hypot(dx, dy)
            if distance == 0:
                sample_x, sample_y = cx + sample_radius, cy
            else:
                sample_x = round(cx + dx * sample_radius / distance)
                sample_y = round(cy + dy * sample_radius / distance)
                while (sample_x - cx) ** 2 + (sample_y - cy) ** 2 <= radius ** 2:
                    sample_x += 1 if dx > 0 else -1 if dx < 0 else 0
                    sample_y += 1 if dy > 0 else -1 if dy < 0 else 0
            if not (0 <= sample_x < base_rgb.width and 0 <= sample_y < base_rgb.height):
                raise ValueError("replacement circle is too close to the image edge")
            pixels.append(source[sample_x, sample_y])

    backdrop = Image.new("RGB", (diameter, diameter))
    backdrop.putdata(pixels)
    return backdrop


def replace_ring_material(
    base: Image.Image,
    material: Image.Image,
    light: str,
    *,
    center: tuple[int, int] = MATERIAL_CENTER,
    radius: int = MATERIAL_RADIUS,
    feather: int = MATERIAL_FEATHER,
) -> Image.Image:
    """Replace ring RGB inside the circle while preserving alpha and outside bytes."""
    if light not in LIGHTS:
        raise ValueError(f"unknown light {light!r}; expected one of {LIGHTS}")
    if radius < 1:
        raise ValueError("radius must be positive")
    cx, cy = center
    if (
        cx - radius < 0
        or cy - radius < 0
        or cx + radius >= base.width
        or cy + radius >= base.height
    ):
        raise ValueError("replacement circle does not fit inside the base image")

    rgba = base.convert("RGBA")
    alpha = rgba.getchannel("A")
    base_rgb = rgba.convert("RGB")
    diameter = radius * 2 + 1
    local_size = (diameter, diameter)
    local_material = directional_material(material, light, local_size)
    backdrop = _canonical_backdrop(base_rgb, center, radius)
    feathered = Image.composite(
        local_material,
        backdrop,
        replacement_mask(radius, feather),
    )
    box = (cx - radius, cy - radius, cx + radius + 1, cy + radius + 1)
    result_rgb = base_rgb.copy()
    result_rgb.paste(feathered, box, _hard_circle_mask(radius))
    red, green, blue = result_rgb.split()
    result = Image.merge("RGBA", (red, green, blue, alpha))
    result.info.clear()
    return result


def outside_circle_digest(
    image: Image.Image,
    center: tuple[int, int] = MATERIAL_CENTER,
    guard_radius: int = MATERIAL_GUARD_RADIUS,
) -> str:
    """Match the Task 1 guard digest: zero the protected circle, hash the rest."""
    rgba = image.convert("RGBA")
    mask = Image.new("L", rgba.size, 0)
    cx, cy = center
    ImageDraw.Draw(mask).ellipse(
        (cx - guard_radius, cy - guard_radius, cx + guard_radius, cy + guard_radius),
        fill=255,
    )
    outside = Image.composite(Image.new("RGBA", rgba.size, (0, 0, 0, 0)), rgba, mask)
    return hashlib.sha256(outside.tobytes()).hexdigest()


def _load_master() -> Image.Image:
    actual_sha256 = hashlib.sha256(MASTER.read_bytes()).hexdigest()
    if actual_sha256 != MASTER_MATERIAL_SHA256:
        raise ValueError(
            f"{MASTER.name}: expected SHA-256 {MASTER_MATERIAL_SHA256}, got {actual_sha256}",
        )
    with Image.open(MASTER) as source:
        if source.size != MASTER_MATERIAL_SIZE or source.mode != "RGB":
            raise ValueError(
                f"{MASTER.name}: expected RGB {MASTER_MATERIAL_SIZE}, "
                f"got {source.mode} {source.size}",
            )
        return source.copy()


def render_expected_outputs() -> dict[Path, Image.Image]:
    """Render all six decoded RGBA expectations without writing tracked files."""
    material = _load_master()
    outputs: dict[Path, Image.Image] = {}
    for light in LIGHTS:
        base_path = KIT / f"home_ring_{light}.png"
        with Image.open(base_path) as source:
            if source.size != MASTER_SIZE or source.mode != "RGBA":
                raise ValueError(
                    f"{base_path.name}: expected RGBA {MASTER_SIZE}, "
                    f"got {source.mode} {source.size}",
                )
            base = source.copy()
        rendered = replace_ring_material(base, material, light)
        actual_outside = outside_circle_digest(rendered)
        expected_outside = EXPECTED_RING_OUTSIDE_SHA256[light]
        if actual_outside != expected_outside:
            raise ValueError(
                f"{base_path.name}: outside material guard changed; "
                f"expected {expected_outside}, got {actual_outside}",
            )
        outputs[base_path] = rendered
        outputs[SOURCE / base_path.name] = rendered.copy()
    return outputs


def write_outputs(outputs: dict[Path, Image.Image]) -> None:
    for path, image in outputs.items():
        image.save(path, format="PNG")


def check_outputs(outputs: dict[Path, Image.Image]) -> int:
    mismatches: list[Path] = []
    for path, expected in outputs.items():
        if not path.is_file():
            mismatches.append(path)
            continue
        with Image.open(path) as tracked:
            if (
                tracked.size != expected.size
                or tracked.convert("RGBA").tobytes() != expected.tobytes()
            ):
                mismatches.append(path)
    if mismatches:
        for path in mismatches:
            print(f"MISMATCH {path.relative_to(ROOT)}")
        return 1
    print("PASS eye-surround assets match tracked RGBA outputs")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="compare decoded tracked RGBA outputs without writing files",
    )
    args = parser.parse_args()
    outputs = render_expected_outputs()
    if args.check:
        return check_outputs(outputs)
    write_outputs(outputs)
    print("Rebuilt home ring eye-surround assets in kit and source.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
