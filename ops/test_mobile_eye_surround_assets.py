from __future__ import annotations

import contextlib
import hashlib
import importlib.util
import io
import statistics
import tempfile
import unittest
import zlib
from pathlib import Path
from unittest import mock

import PIL
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
    @classmethod
    def setUpClass(cls) -> None:
        with Image.open(MODULE.MASTER) as source:
            cls.master = source.convert("RGB")

        size = 1254
        center = size / 2
        radius_squared = (0.49 * size) ** 2

        def region(
            bounds: tuple[float, float, float, float],
        ) -> list[tuple[int, int, int]]:
            left, top, right, bottom = bounds
            pixels = cls.master.load()
            return [
                pixels[x, y]
                for y in range(size)
                for x in range(size)
                if left <= x < right
                and top <= y < bottom
                and (x - center) ** 2 + (y - center) ** 2 <= radius_squared
            ]

        aperture = region((154, 484, 1129, 855))
        upper = region((0.25 * size, 0.28 * size, 0.75 * size, 0.49 * size))
        lower = region((0.25 * size, 0.62 * size, 0.75 * size, 0.82 * size))
        cls.aperture_green_minus_red = sum(green - red for red, green, _ in aperture) / len(aperture)
        cls.upper_green_minus_red = sum(green - red for red, green, _ in upper) / len(upper)

        def median_luma(pixels: list[tuple[int, int, int]]) -> float:
            return statistics.median(
                0.2126 * red + 0.7152 * green + 0.0722 * blue
                for red, green, blue in pixels
            )

        cls.upper_lower_luma_delta = median_luma(upper) - median_luma(lower)

    def test_tracked_master_keeps_dense_emerald_lids_across_the_aperture(self) -> None:
        self.assertGreaterEqual(
            self.aperture_green_minus_red,
            14.0,
            "regression: the sealed aperture lost its dense dark-emerald lid material",
        )

    def test_tracked_master_keeps_the_brighter_raised_upper_lid_relief(self) -> None:
        self.assertGreaterEqual(
            self.upper_green_minus_red,
            20.0,
            "regression: the raised upper lid lost its emerald relief",
        )

    def test_tracked_master_keeps_upper_lid_brighter_than_the_broad_lower_lid(self) -> None:
        self.assertGreaterEqual(
            self.upper_lower_luma_delta,
            8.0,
            "regression: the upper and lower lids collapsed into a flat or hollow socket",
        )

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


    def test_archived_owner_reference_rebuilds_the_tracked_master(self) -> None:
        expected = MODULE.render_expected_master()
        with Image.open(MODULE.MASTER) as source:
            tracked = source.convert("RGB")
        self.assertEqual(tracked.size, expected.size)
        self.assertEqual(tracked.mode, expected.mode)
        self.assertEqual(tracked.tobytes(), expected.tobytes())

    def test_master_mismatch_reports_encoding_pixels_and_runtime_versions(self) -> None:
        expected = Image.new("RGB", MODULE.MASTER_MATERIAL_SIZE, (8, 31, 19))
        expected_buffer = io.BytesIO()
        expected.save(expected_buffer, format="PNG", optimize=False, compress_level=9)
        expected_encoded = expected_buffer.getvalue()
        expected_pixels = expected.tobytes()

        with tempfile.TemporaryDirectory(dir=MODULE.ROOT) as temporary_directory:
            tracked_path = Path(temporary_directory) / "diagnostic-master.png"
            expected.save(tracked_path, format="PNG", optimize=False, compress_level=0)
            tracked_encoded = tracked_path.read_bytes()
            output = io.StringIO()
            with mock.patch.object(MODULE, "MASTER", tracked_path), contextlib.redirect_stdout(output):
                result = MODULE.check_master(expected)

        diagnostic = output.getvalue()
        self.assertEqual(result, 1)
        self.assertIn("encoded_matches=False pixels_match=True", diagnostic)
        self.assertIn(
            f"tracked_encoded_sha256={hashlib.sha256(tracked_encoded).hexdigest()}",
            diagnostic,
        )
        self.assertIn(
            f"expected_encoded_sha256={hashlib.sha256(expected_encoded).hexdigest()}",
            diagnostic,
        )
        expected_pixel_sha256 = hashlib.sha256(expected_pixels).hexdigest()
        self.assertIn(f"tracked_rgb_sha256={expected_pixel_sha256}", diagnostic)
        self.assertIn(f"expected_rgb_sha256={expected_pixel_sha256}", diagnostic)
        self.assertIn(f"pillow={PIL.__version__}", diagnostic)
        self.assertIn(f"zlib_compile={zlib.ZLIB_VERSION}", diagnostic)
        self.assertIn(f"zlib_runtime={zlib.ZLIB_RUNTIME_VERSION}", diagnostic)


if __name__ == "__main__":
    unittest.main()
