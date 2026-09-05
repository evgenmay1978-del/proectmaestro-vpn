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

    def test_open_aperture_matches_original_master_registered_bounds(self) -> None:
        upper, lower = MODULE.aperture_contours_dp(closure=0.0)
        xs = [point[0] for point in upper + lower]
        ys = [point[1] for point in upper + lower]
        bounds = (min(xs), min(ys), max(xs), max(ys))

        # Frozen 390x844 dp projection of the original 1254px green master:
        # corners x=25/1229, upper y=444 and lower y=809. These independent
        # reference values must not be calculated from MODULE's contours/fit.
        expected = (83.252, 224.486, 306.748, 292.240)
        for value, target in zip(bounds, expected):
            self.assertAlmostEqual(value, target, delta=0.05)
        self.assertAlmostEqual(bounds[2] - bounds[0], 223.496, delta=0.05)
        self.assertAlmostEqual(bounds[3] - bounds[1], 67.754, delta=0.05)

    def test_full_closure_uses_original_master_fold_and_fixed_corners(self) -> None:
        upper, lower = MODULE.aperture_contours_dp(closure=1.0)

        self.assertEqual(len(upper), len(lower))
        for upper_point, lower_point in zip(upper, lower):
            self.assertAlmostEqual(upper_point[0], lower_point[0], delta=0.0001)
            self.assertAlmostEqual(upper_point[1], lower_point[1], delta=0.0001)

        # Independently frozen master-fold samples, not a 70/30 blend of OPEN:
        # master x: 25,100,200,300,400,500,627,750,850,950,1050,1150,1229
        # master y: 635,660,693,718,737,746,749,740,727,707,685,658,635
        # The same registration is fixed in LivingEyeLayerGeometry.kt.
        expected_points = [
            (83.252, 259.941),
            (97.174, 264.581),
            (115.737, 270.707),
            (134.300, 275.348),
            (152.863, 278.875),
            (171.425, 280.545),
            (195.000, 281.102),
            (217.833, 279.431),
            (236.395, 277.018),
            (254.958, 273.306),
            (273.521, 269.222),
            (292.084, 264.210),
            (306.748, 259.941),
        ]
        self.assertEqual(len(upper), len(expected_points))
        for actual, expected in zip(upper, expected_points):
            self.assertAlmostEqual(actual[0], expected[0], delta=0.05)
            self.assertAlmostEqual(actual[1], expected[1], delta=0.05)

        for closure in (0.0, 0.5, 1.0):
            phase_upper, phase_lower = MODULE.aperture_contours_dp(closure=closure)
            for contour in (phase_upper, phase_lower):
                for index in (0, -1):
                    self.assertEqual(contour[index], upper[index])
                    self.assertAlmostEqual(
                        contour[index][0], expected_points[index][0], delta=0.05
                    )
                    self.assertAlmostEqual(
                        contour[index][1], expected_points[index][1], delta=0.05
                    )

    def test_full_closure_reveals_the_registered_emerald_surround(self) -> None:
        scale = 2
        material_only = MODULE._replace_material(
            MODULE.load_reference(scale),
            scale,
        ).convert("RGB")
        disconnected = MODULE.render_home("disconnected", scale=scale)
        eye, seam, aperture, glow = MODULE.render_living_eye_layers(
            closure=1.0,
            scale=scale,
        )
        combined = Image.new("RGBA", eye.size, (0, 0, 0, 0))
        combined.alpha_composite(eye)
        combined.alpha_composite(seam)

        required = MODULE._contour_mask(scale=scale, closure=0.0).filter(
            ImageFilter.MinFilter(3)
        )
        difference = ImageChops.difference(material_only, disconnected)
        # The green master remains untouched; only the newly approved lashes cover its fold.
        lash_support = MODULE.render_eyelashes(1.0, scale).getchannel("A").point(
            lambda value: 255 if value else 0
        )
        required = ImageChops.multiply(required, ImageChops.invert(lash_support))

        self.assertIsNone(aperture.getbbox())
        self.assertIsNone(glow.getchannel("A").getbbox())
        self.assertIsNone(
            combined.getchannel("A").getbbox(),
            "full closure must add no anatomy, legacy lid texture, or doubled "
            f"contact seam over the registered surround: overlay={combined.getchannel('A').getbbox()}",
        )
        for channel in difference.split():
            self.assertIsNone(
                ImageChops.multiply(channel, required).getbbox(),
                "full closure must be pixel-identical to the registered "
                "emerald surround outside the explicit eyelash overlay",
            )

    def test_lashes_are_irregular_sparse_below_and_attached_during_blink(self) -> None:
        upper_specs = MODULE._lash_specs(True)
        lower_specs = MODULE._lash_specs(False)
        self.assertEqual(len(upper_specs), 29)
        self.assertEqual(len(lower_specs), 12)
        self.assertLess(max(spec[1] for spec in lower_specs), max(spec[1] for spec in upper_specs))
        self.assertGreater(len({round(b[0] - a[0], 4) for a, b in zip(upper_specs, upper_specs[1:])}), 5)
        self.assertGreater(len({spec[1] for spec in upper_specs}), 10)
        for closure in (0.0, 0.5, 1.0):
            upper, lower = MODULE.aperture_contours_dp(closure)
            curves = MODULE.lash_curves_dp(closure)
            self.assertEqual(curves, MODULE.lash_curves_dp(closure))
            for root, control1, control2, tip, width, alpha, is_upper in curves:
                lid = upper if is_upper else lower
                self.assertGreater(root[0], lid[0][0])
                self.assertLess(root[0], lid[-1][0])
                self.assertAlmostEqual(root[1], MODULE._interpolate_contour_y(tuple(lid), root[0]))
                self.assertGreater(width, 0)
                self.assertGreater(alpha, 0)
                self.assertLessEqual(alpha, 1)
                # The control points depart from a straight spoke and end in a zero-width tip.
                cross = ((control2[0] - root[0]) * (tip[1] - root[1])
                         - (control2[1] - root[1]) * (tip[0] - root[0]))
                self.assertNotAlmostEqual(cross, 0, delta=0.000001)
            self.assertIsNotNone(MODULE.render_eyelashes(closure).getchannel("A").getbbox())

    def test_connected_glow_fades_to_transparent_at_socket_boundary(self) -> None:
        scale = 2
        _eye, _seam, _aperture, glow = MODULE.render_living_eye_layers(
            closure=0.0,
            scale=scale,
        )
        center_x, center_y, medallion = MODULE._current_medallion_dp()
        center = (round(center_x * scale), round(center_y * scale))
        outer_radius = round(medallion * 0.45 * scale)

        self.assertEqual(
            glow.getpixel((center[0] + outer_radius, center[1]))[3],
            0,
            "the connected glow must fade out before the circle ends, "
            "not leave an opaque cut-off rim",
        )
        self.assertGreater(
            glow.getchannel("A").getextrema()[1],
            0,
            "fading the edge must retain the connection glow",
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
