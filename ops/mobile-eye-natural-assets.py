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

import hashlib
from pathlib import Path

from PIL import Image


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "docs/design/mobile-eye-natural/source"
DRAWABLE = ROOT / "app/src/main/res/drawable-nodpi"

CANVAS = (1349, 1536)
STATE_CROP = (230, 745, 1120, 1380)
STATE_SIZE = (STATE_CROP[2] - STATE_CROP[0], STATE_CROP[3] - STATE_CROP[1])
APPROVED_ALPHA_SUPPORT_SHA256 = {
    "open": "8b4f87a263706752d6ad044923c77179458f32e9b46446cfe15671a9ee6f7bc9",
    "squint": "b172931f6ccf6deaf4b0bb8098d0abc6f0ddbc04b80efd58286abdec7d766e28",
    "closed": "2b297389fda3d7e66cfd32c5f5d2f011a54565d91fab4aa54b757f09e83a541b",
}
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


def alpha_support_sha256(alpha: Image.Image) -> str:
    if alpha.mode != "L":
        raise ValueError(f"expected L alpha channel, got {alpha.mode}")
    support = bytes(1 if value else 0 for value in alpha.getdata())
    return hashlib.sha256(support).hexdigest()


def validate_state_cutout(
    state: str,
    candidate: Image.Image,
    expected_sha256: str,
    expected_size: tuple[int, int] = STATE_SIZE,
) -> None:
    if candidate.mode != "RGBA" or candidate.size != expected_size:
        raise ValueError(
            f"{state}: expected RGBA {expected_size}, got {candidate.mode} {candidate.size}",
        )
    alpha = candidate.getchannel("A")
    try:
        if alpha.getbbox() is None:
            raise ValueError(f"{state}: eyelid cutout is empty")
        actual_sha256 = alpha_support_sha256(alpha)
        if actual_sha256 != expected_sha256:
            raise ValueError(
                f"{state}: alpha support differs from immutable approved contour",
            )
    finally:
        alpha.close()


def save_state_resources(frames: dict[str, Image.Image]) -> None:
    layers: dict[str, Image.Image] = {}
    try:
        for state, frame in frames.items():
            layer = frame.crop(STATE_CROP).convert("RGBA")
            expected_sha256 = APPROVED_ALPHA_SUPPORT_SHA256.get(state)
            if expected_sha256 is None:
                layer.close()
                raise ValueError(f"{state}: immutable approved contour is missing")
            validate_state_cutout(state, layer, expected_sha256)
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
