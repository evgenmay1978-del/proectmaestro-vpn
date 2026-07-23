#!/usr/bin/env python3
"""Rebuild the phone eye scene and aligned blink-state resources.

The three source frames are owner-supplied.  Frame geometry is kept in the
original 1349 x 1536 coordinate space so the eyelids never move the medallion
or plaque.  The lower part of the existing 853 x 1844 phone scene is retained
for the revolver menu.

This script intentionally does not synthesize anatomy.  The committed
``mobile_eye_sclera``, ``mobile_eye_iris`` and ``mobile_eye_catchlight`` files
are deterministic reconstructions documented in
``docs/design/mobile-eye-natural/asset_metadata.json``.
"""

from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "docs/design/mobile-eye-natural/source"
DRAWABLE = ROOT / "app/src/main/res/drawable-nodpi"

CANVAS = (1349, 1536)
SCENE_SIZE = (853, 1844)
HERO_TOP = 48
STATE_CROP = (230, 745, 1120, 1380)
STATE_MASK_ELLIPSE = (300, 810, 1049, 1310)
STATE_MASK_FEATHER = 18
ALIGNMENT = {
    "open": (0, 0),
    "squint": (-8, -15),
    "closed": (-12, -15),
}


def normalized(path: Path) -> Image.Image:
    image = Image.open(path).convert("RGB")
    if image.height != CANVAS[1]:
        width = round(image.width * CANVAS[1] / image.height)
        image = image.resize((width, CANVAS[1]), Image.Resampling.LANCZOS)

    if image.width > CANVAS[0]:
        left = (image.width - CANVAS[0]) // 2
        image = image.crop((left, 0, left + CANVAS[0], CANVAS[1]))
    elif image.width < CANVAS[0]:
        image = image.resize(CANVAS, Image.Resampling.LANCZOS)
    return image


def aligned(image: Image.Image, dx: int, dy: int) -> Image.Image:
    result = Image.new("RGB", CANVAS)
    result.paste(image, (dx, dy))
    return result


def state_mask() -> Image.Image:
    mask = Image.new("L", CANVAS, 0)
    ImageDraw.Draw(mask).ellipse(STATE_MASK_ELLIPSE, fill=255)
    return mask.filter(ImageFilter.GaussianBlur(STATE_MASK_FEATHER))


def save_state_resources(frames: dict[str, Image.Image]) -> None:
    mask = state_mask().crop(STATE_CROP)
    for state, frame in frames.items():
        layer = frame.crop(STATE_CROP).convert("RGBA")
        layer.putalpha(mask)
        layer.save(
            DRAWABLE / f"mobile_eye_{state}.webp",
            format="WEBP",
            lossless=True,
            method=6,
        )


def save_scene(open_frame: Image.Image) -> None:
    lower_base = Image.open(
        SOURCE / "mobile_home_scene_lower_base.webp",
    ).convert("RGB")
    if lower_base.size != SCENE_SIZE:
        raise ValueError(
            f"Unexpected lower scene size {lower_base.size}; expected {SCENE_SIZE}",
        )

    hero_height = round(CANVAS[1] * SCENE_SIZE[0] / CANVAS[0])
    hero = open_frame.resize(
        (SCENE_SIZE[0], hero_height),
        Image.Resampling.LANCZOS,
    )
    old_underlay = lower_base.crop(
        (0, HERO_TOP, SCENE_SIZE[0], HERO_TOP + hero_height),
    )

    # A short top blend retains the carved outer cap.  The longer bottom blend
    # joins two compatible wood fields below the medallion, where menu rows
    # start covering the centre.
    alpha = Image.new("L", hero.size, 255)
    alpha_pixels = alpha.load()
    for y in range(24):
        value = round(255 * y / 23)
        for x in range(hero.width):
            alpha_pixels[x, y] = value
    for index, y in enumerate(range(hero.height - 80, hero.height)):
        value = round(255 * (79 - index) / 79)
        for x in range(hero.width):
            alpha_pixels[x, y] = value

    blended = Image.composite(hero, old_underlay, alpha)
    scene = lower_base.copy()
    scene.paste(blended, (0, HERO_TOP))
    scene.save(
        DRAWABLE / "mobile_home_scene.webp",
        format="WEBP",
        lossless=True,
        method=6,
    )


def validate_runtime_anatomy() -> None:
    expected = {
        "mobile_eye_sclera.webp": (660, 280),
        "mobile_eye_iris.webp": (292, 292),
        "mobile_eye_catchlight.webp": (90, 90),
    }
    for name, size in expected.items():
        path = DRAWABLE / name
        image = Image.open(path)
        if image.size != size or image.mode not in {"RGBA", "LA"}:
            raise ValueError(
                f"{name}: expected alpha image {size}, got {image.mode} {image.size}",
            )


def main() -> None:
    raw = {
        state: normalized(SOURCE / f"{state}.png")
        for state in ("open", "squint", "closed")
    }
    frames = {
        state: aligned(raw[state], *ALIGNMENT[state])
        for state in raw
    }
    save_scene(frames["open"])
    save_state_resources(frames)
    validate_runtime_anatomy()
    print("Rebuilt mobile eye scene and open/squint/closed state resources.")


if __name__ == "__main__":
    main()
