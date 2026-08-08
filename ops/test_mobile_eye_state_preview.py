from __future__ import annotations

import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path

from PIL import Image, ImageChops, ImageDraw, ImageFilter


MODULE_NAME = "mobile_eye_state_preview"
MODULE_PATH = Path(__file__).with_name("mobile-eye-state-preview.py")
if not MODULE_PATH.is_file():
    raise ModuleNotFoundError(f"No module named '{MODULE_NAME}'")
SPEC = importlib.util.spec_from_file_location(MODULE_NAME, MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Cannot load {MODULE_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)

ROOT = Path(__file__).resolve().parents[1]
CURRENT_INSTALLED_HOME = (
    ROOT
    / "design"
    / "mobile-4d-references"
    / "08-owner-installed-test-home-2026-08-08.jpg"
)
CURRENT_INSTALLED_HOME_SHA256 = (
    "9251457407f3aeee17b5281b32634e6c0d03e7fce3e9db12c16706444a9f800b"
)


class MobileEyeStatePreviewGeometryTest(unittest.TestCase):
    def test_owner_approved_uniform_transform_has_expected_state_bounds(self) -> None:
        actual = MODULE.eye_state_bounds_dp()
        expected = (56.8716, 164.4053, 340.1284, 366.5043)

        for value, target in zip(actual, expected):
            self.assertAlmostEqual(value, target, delta=0.002)

    def test_open_aperture_matches_owner_approved_viewport_bounds(self) -> None:
        upper, lower = MODULE.aperture_contours_dp(closure=0.0)
        xs = [point[0] for point in upper + lower]
        ys = [point[1] for point in upper + lower]
        bounds = (min(xs), min(ys), max(xs), max(ys))

        expected = (107.158, 231.878, 288.251, 300.942)
        for value, target in zip(bounds, expected):
            self.assertAlmostEqual(value, target, delta=0.05)
        self.assertAlmostEqual(bounds[2] - bounds[0], 181.093, delta=0.05)
        self.assertAlmostEqual(bounds[3] - bounds[1], 69.064, delta=0.05)

    def test_full_closure_uses_the_production_70_30_seam(self) -> None:
        upper, lower = MODULE.aperture_contours_dp(closure=1.0)

        self.assertEqual(len(upper), len(lower))
        for upper_point, lower_point in zip(upper, lower):
            self.assertAlmostEqual(upper_point[0], lower_point[0], delta=0.0001)
            self.assertAlmostEqual(upper_point[1], lower_point[1], delta=0.0001)

        expected_points = [
            (107.158, 271.979),
            (112.568, 272.364),
            (117.342, 273.182),
            (120.525, 273.324),
            (130.073, 274.303),
            (142.803, 275.862),
            (155.534, 277.326),
            (168.265, 278.599),
            (180.995, 279.713),
            (193.726, 280.222),
            (206.457, 279.777),
            (219.187, 279.140),
            (231.918, 278.408),
            (244.649, 277.581),
            (257.379, 276.244),
            (270.110, 274.621),
            (280.294, 273.221),
            (288.251, 271.979),
        ]
        self.assertEqual(len(upper), len(expected_points))
        for actual, expected in zip(upper, expected_points):
            self.assertAlmostEqual(actual[0], expected[0], delta=0.05)
            self.assertAlmostEqual(actual[1], expected[1], delta=0.05)

    def test_disconnected_dynamic_lids_opaquely_cover_the_open_aperture(self) -> None:
        eye, seam, aperture, glow = MODULE.render_living_eye_layers(
            closure=1.0,
            scale=2,
        )
        combined = Image.new("RGBA", eye.size, (0, 0, 0, 0))
        combined.alpha_composite(eye)
        combined.alpha_composite(seam)

        required = MODULE._contour_mask(scale=2, closure=0.0).filter(
            ImageFilter.MinFilter(3)
        )
        opaque = combined.getchannel("A").point(
            lambda alpha: 255 if alpha >= 250 else 0
        )
        uncovered = ImageChops.subtract(required, opaque)

        self.assertIsNone(aperture.getbbox())
        self.assertIsNone(glow.getchannel("A").getbbox())
        self.assertIsNone(
            uncovered.getbbox(),
            "full closure leaves the original open aperture without opaque "
            f"lid coverage: required={required.getbbox()}, "
            f"overlay={combined.getchannel('A').getbbox()}, "
            f"uncovered={uncovered.getbbox()}",
        )

    def test_connected_contact_seam_uses_runtime_alpha(self) -> None:
        _eye, seam, _aperture, _glow = MODULE.render_living_eye_layers(
            closure=0.0,
            scale=2,
        )
        alphas = [value for value in seam.getchannel("A").getdata() if value]

        self.assertTrue(alphas)
        self.assertEqual(max(alphas), round(255 * 0.18))


class MobileEyeStatePreviewRenderTest(unittest.TestCase):
    def load_current_installed_home(self, scale: int = 1) -> Image.Image:
        self.assertEqual(
            hashlib.sha256(CURRENT_INSTALLED_HOME.read_bytes()).hexdigest(),
            CURRENT_INSTALLED_HOME_SHA256,
        )
        with Image.open(CURRENT_INSTALLED_HOME) as source:
            self.assertEqual(source.size, (591, 1280))
            return source.convert("RGB").resize(
                (390 * scale, 844 * scale),
                Image.Resampling.LANCZOS,
            )

    def owner_allowed_change_mask(self, state: str, scale: int = 1) -> Image.Image:
        mask = Image.new("L", (390 * scale, 844 * scale), 0)
        draw = ImageDraw.Draw(mask)
        draw.ellipse(
            tuple(round(value * scale) for value in (78.5, 142.0, 311.5, 375.0)),
            fill=255,
        )
        if state == "connected":
            draw.rectangle(
                tuple(round(value * scale) for value in (96.0, 426.0, 294.0, 480.0)),
                fill=255,
            )
        return mask

    def test_load_reference_uses_the_current_installed_test_build(self) -> None:
        expected = self.load_current_installed_home(scale=1)

        self.assertEqual(MODULE.load_reference(scale=1).tobytes(), expected.tobytes())

    def test_two_states_are_deterministic_and_do_not_touch_unowned_pixels(self) -> None:
        reference = self.load_current_installed_home(scale=1)

        for state in MODULE.STATES:
            outside = ImageChops.invert(
                self.owner_allowed_change_mask(state, scale=1)
            )
            first = MODULE.render_home(state, scale=1)
            second = MODULE.render_home(state, scale=1)
            self.assertEqual(first.mode, "RGB")
            self.assertEqual(first.size, (390, 844))
            self.assertEqual(first.tobytes(), second.tobytes())

            difference = ImageChops.difference(reference, first)
            for channel in difference.split():
                self.assertIsNone(ImageChops.multiply(channel, outside).getbbox())

        connected = MODULE.render_home("connected", scale=1)
        disconnected = MODULE.render_home("disconnected", scale=1)
        self.assertNotEqual(connected.tobytes(), disconnected.tobytes())

        installed_status_box = (96, 426, 294, 480)
        self.assertEqual(
            disconnected.crop(installed_status_box).tobytes(),
            reference.crop(installed_status_box).tobytes(),
        )
        self.assertNotEqual(
            connected.crop(installed_status_box).tobytes(),
            reference.crop(installed_status_box).tobytes(),
        )

        material_box = tuple(round(value) for value in MODULE.MATERIAL_BOUNDS_DP)
        self.assertNotEqual(
            reference.crop(material_box).tobytes(),
            disconnected.crop(material_box).tobytes(),
        )

    def test_connected_status_does_not_carry_old_disconnected_ink(self) -> None:
        reference = Image.new("RGB", (390, 844), (32, 32, 32))
        old_ink_box = (104, 451, 116, 459)
        ImageDraw.Draw(reference).rectangle(old_ink_box, fill=(240, 20, 20))
        canvas = reference.convert("RGBA")

        MODULE._draw_connected_status(canvas, reference, scale=1)

        probe = canvas.convert("RGB").crop((100, 448, 120, 462))
        red_pixels = sum(
            1
            for red, green, blue in probe.getdata()
            if red >= 50 and red > green * 1.5 and red > blue * 1.5
        )
        self.assertEqual(red_pixels, 0)

    def test_write_and_check_are_reproducible_and_check_is_read_only(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output_dir = Path(temp_dir)
            paths = MODULE.write_outputs(output_dir, scale=1)

            self.assertEqual(
                {path.name for path in paths},
                {
                    "home-disconnected.png",
                    "home-connected.png",
                    "home-eye-states-comparison.png",
                },
            )
            before = {path: path.read_bytes() for path in paths}
            self.assertEqual(MODULE.check_outputs(output_dir, scale=1), 0)
            self.assertEqual(before, {path: path.read_bytes() for path in paths})

            target = output_dir / "home-disconnected.png"
            with Image.open(target) as source:
                corrupted = source.convert("RGB")
            corrupted.putpixel((0, 0), (255, 0, 255))
            corrupted.save(target, format="PNG", optimize=False)
            corrupted_bytes = target.read_bytes()

            self.assertEqual(MODULE.check_outputs(output_dir, scale=1), 1)
            self.assertEqual(target.read_bytes(), corrupted_bytes)


if __name__ == "__main__":
    unittest.main()
