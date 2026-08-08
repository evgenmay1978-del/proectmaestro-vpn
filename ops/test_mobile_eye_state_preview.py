from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

from PIL import Image, ImageChops


MODULE_NAME = "mobile_eye_state_preview"
MODULE_PATH = Path(__file__).with_name("mobile-eye-state-preview.py")
if not MODULE_PATH.is_file():
    raise ModuleNotFoundError(f"No module named '{MODULE_NAME}'")
SPEC = importlib.util.spec_from_file_location(MODULE_NAME, MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Cannot load {MODULE_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


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

    def test_full_closure_uses_the_70_30_seam(self) -> None:
        upper, lower = MODULE.aperture_contours_dp(closure=1.0)

        self.assertEqual(len(upper), len(lower))
        for upper_point, lower_point in zip(upper, lower):
            self.assertAlmostEqual(upper_point[0], lower_point[0], delta=0.0001)
            self.assertAlmostEqual(upper_point[1], lower_point[1], delta=0.0001)

        expected_points = {
            0: (107.158, 271.979),
            9: (193.726, 280.222),
            -1: (288.251, 271.979),
        }
        for index, expected in expected_points.items():
            self.assertAlmostEqual(upper[index][0], expected[0], delta=0.05)
            self.assertAlmostEqual(upper[index][1], expected[1], delta=0.05)

    def test_disconnected_overlay_contains_only_the_contact_seam(self) -> None:
        eye, seam, aperture, _ = MODULE.render_living_eye_layers(
            closure=1.0,
            scale=2,
        )
        combined = Image.new("RGBA", eye.size, (0, 0, 0, 0))
        combined.alpha_composite(eye)
        combined.alpha_composite(seam)

        self.assertIsNone(eye.getchannel("A").getbbox())
        self.assertIsNone(aperture.getbbox())
        self.assertIsNotNone(seam.getchannel("A").getbbox())
        self.assertEqual(
            combined.getchannel("A").tobytes(),
            seam.getchannel("A").tobytes(),
        )


class MobileEyeStatePreviewRenderTest(unittest.TestCase):
    def test_two_states_are_deterministic_and_do_not_touch_unowned_pixels(self) -> None:
        reference = MODULE.load_reference(scale=1)
        allowed = MODULE.allowed_change_mask(scale=1)
        outside = ImageChops.invert(allowed)

        for state in MODULE.STATES:
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

        material_box = tuple(round(value) for value in MODULE.MATERIAL_BOUNDS_DP)
        self.assertNotEqual(
            reference.crop(material_box).tobytes(),
            disconnected.crop(material_box).tobytes(),
        )

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
