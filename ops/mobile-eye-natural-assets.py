#!/usr/bin/env python3
"""Rebuild aligned phone blink-state resources.

The three source frames are owner-supplied. Frame geometry is kept in the
original 1349 x 1536 coordinate space so the eyelids stay registered. Sources
must already contain the approved transparent eyelid cutout; this script never
constructs a backing mask.

This script intentionally does not synthesize anatomy. The committed
``mobile_eye_sclera``, ``mobile_eye_iris`` and ``mobile_eye_catchlight`` files
are deterministic reconstructions documented in
``docs/design/mobile-eye-natural/asset_metadata.json``.
"""

from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageChops, ImageOps


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "docs/design/mobile-eye-natural/source"
DRAWABLE = ROOT / "app/src/main/res/drawable-nodpi"

CANVAS = (1349, 1536)
STATE_CROP = (230, 745, 1120, 1380)
ALIGNMENT = {
    "open": (0, 0),
    "squint": (-8, -15),
    "closed": (-12, -15),
}


def normalized(path: Path) -> Image.Image:
    source = Image.open(path)
    if "A" not in source.getbands():
        source.close()
        raise ValueError(
            f"{path.name}: RGBA eyelid cutout required; refusing legacy backing mask",
        )
    image = source.convert("RGBA")
    source.close()
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
    result = Image.new("RGBA", CANVAS, (0, 0, 0, 0))
    result.alpha_composite(image, (dx, dy))
    return result


def validate_state_cutout(
    state: str,
    candidate: Image.Image,
    approved_alpha: Image.Image,
) -> None:
    if candidate.mode != "RGBA" or candidate.size != approved_alpha.size:
        raise ValueError(
            f"{state}: expected RGBA {approved_alpha.size}, got {candidate.mode} {candidate.size}",
        )
    alpha = candidate.getchannel("A")
    if alpha.getbbox() is None:
        alpha.close()
        raise ValueError(f"{state}: eyelid cutout is empty")
    approved_support = approved_alpha.point(lambda value: 255 if value else 0)
    outside = ImageChops.multiply(alpha, ImageOps.invert(approved_support))
    try:
        if outside.getbbox() is not None:
            raise ValueError(
                f"{state}: non-transparent pixels outside approved eyelid contour",
            )
    finally:
        alpha.close()
        approved_support.close()
        outside.close()


def save_state_resources(frames: dict[str, Image.Image]) -> None:
    layers: dict[str, Image.Image] = {}
    try:
        for state, frame in frames.items():
            layer = frame.crop(STATE_CROP).convert("RGBA")
            approved_path = DRAWABLE / f"mobile_eye_{state}.webp"
            if not approved_path.exists():
                layer.close()
                raise ValueError(f"{state}: approved runtime cutout is missing")
            with Image.open(approved_path) as approved:
                approved_alpha = approved.convert("RGBA").getchannel("A")
            try:
                validate_state_cutout(state, layer, approved_alpha)
            finally:
                approved_alpha.close()
            layers[state] = layer

        for state, layer in layers.items():
            layer.save(
                DRAWABLE / f"mobile_eye_{state}.webp",
                format="WEBP",
                lossless=True,
                method=6,
            )
    finally:
        for layer in layers.values():
            layer.close()


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
    save_state_resources(frames)
    validate_runtime_anatomy()
    print("Rebuilt open/squint/closed mobile eye state resources.")


if __name__ == "__main__":
    main()
