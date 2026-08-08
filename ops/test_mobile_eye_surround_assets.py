from __future__ import annotations

import hashlib
import importlib.util
import unittest
from pathlib import Path

from PIL import Image


MODULE_NAME = "mobile_eye_surround_assets"
MODULE_PATH = Path(__file__).with_name("mobile-eye-surround-assets.py")
if not MODULE_PATH.is_file():
    raise ModuleNotFoundError(f"No module named '{MODULE_NAME}'")
SPEC = importlib.util.spec_from_file_location(MODULE_NAME, MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Cannot load {MODULE_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


CENTER = (48, 54)
RADIUS = 24
FEATHER = 6


def synthetic_base() -> Image.Image:
    image = Image.new("RGBA", (96, 144))
    image.putdata(
        [
            (
                (x * 3 + y) % 256,
                (x + y * 2) % 256,
                (x * 2 + y * 3) % 256,
                (x * 5 + y * 7) % 256,
            )
            for y in range(image.height)
            for x in range(image.width)
        ],
    )
    return image


def synthetic_master() -> Image.Image:
    image = Image.new("RGB", (41, 37))
    image.putdata(
        [
            (
                72 + (x * 4 + y) % 112,
                45 + (x + y * 3) % 96,
                28 + (x * 2 + y * 5) % 80,
            )
            for y in range(image.height)
            for x in range(image.width)
        ],
    )
    return image


def circular_digest(
    image: Image.Image,
    center: tuple[int, int],
    radius: int,
    *,
    outside: bool,
) -> str:
    rgba = image.convert("RGBA")
    pixels = rgba.load()
    cx, cy = center
    selected = bytearray()
    for y in range(rgba.height):
        for x in range(rgba.width):
            in_circle = (x - cx) ** 2 + (y - cy) ** 2 <= radius ** 2
            if in_circle != outside:
                selected.extend(pixels[x, y])
    return hashlib.sha256(selected).hexdigest()


def outside_digest(image: Image.Image) -> str:
    return circular_digest(image, CENTER, RADIUS, outside=True)


def core_digest(image: Image.Image) -> str:
    return circular_digest(image, CENTER, RADIUS - FEATHER, outside=False)


def alpha_support(image: Image.Image) -> bytes:
    return bytes(value > 0 for value in image.convert("RGBA").getchannel("A").getdata())


class MobileEyeSurroundAssetsTest(unittest.TestCase):
    def render(self, light: str = "c", base: Image.Image | None = None) -> Image.Image:
        return MODULE.replace_ring_material(
            base or synthetic_base(),
            synthetic_master(),
            light,
            center=CENTER,
            radius=RADIUS,
            feather=FEATHER,
        )

    def test_replacement_mask_has_a_full_core_and_local_feather(self) -> None:
        mask = MODULE.replacement_mask(radius=RADIUS, feather=FEATHER)

        self.assertEqual(mask.mode, "L")
        self.assertEqual(mask.size, (RADIUS * 2 + 1, RADIUS * 2 + 1))
        self.assertEqual(mask.getpixel((RADIUS, RADIUS)), 255)
        self.assertEqual(mask.getpixel((0, 0)), 0)
        self.assertTrue(0 < mask.getpixel((RADIUS * 2, RADIUS)) < 255)

    def test_replacement_preserves_alpha_and_pixels_outside_circle(self) -> None:
        base = synthetic_base()
        result = self.render(base=base)

        self.assertEqual(result.getchannel("A").tobytes(), base.getchannel("A").tobytes())
        self.assertEqual(outside_digest(result), outside_digest(base))
        self.assertNotEqual(core_digest(result), core_digest(base))

    def test_lights_share_alpha_support_but_left_and_right_rgb_differ(self) -> None:
        left = self.render("l")
        center = self.render("c")
        right = self.render("r")

        self.assertEqual(alpha_support(left), alpha_support(center))
        self.assertEqual(alpha_support(center), alpha_support(right))
        self.assertNotEqual(left.convert("RGB").tobytes(), right.convert("RGB").tobytes())

    def test_replacement_is_idempotent_with_feather(self) -> None:
        first = self.render("r")
        second = self.render("r", base=first)

        self.assertEqual(second.tobytes(), first.tobytes())


class MobileEyeSurroundAssetsIntegrationTest(unittest.TestCase):
    def test_tracked_kit_and_source_match_rendered_expectations(self) -> None:
        expected = MODULE.render_expected_outputs()

        self.assertEqual(
            set(expected),
            {
                directory / f"home_ring_{light}.png"
                for directory in (MODULE.KIT, MODULE.SOURCE)
                for light in MODULE.LIGHTS
            },
        )
        for path, rendered in expected.items():
            with self.subTest(path=path), Image.open(path) as tracked:
                self.assertEqual(tracked.convert("RGBA").tobytes(), rendered.tobytes())


if __name__ == "__main__":
    unittest.main()
