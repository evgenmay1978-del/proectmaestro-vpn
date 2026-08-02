from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

from PIL import Image, ImageDraw


MODULE_PATH = Path(__file__).with_name("mobile-eye-natural-assets.py")
SPEC = importlib.util.spec_from_file_location("mobile_eye_natural_assets", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Cannot load {MODULE_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class MobileEyeNaturalAssetsTest(unittest.TestCase):
    def test_rgb_source_is_rejected_instead_of_getting_a_backing_mask(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "open.png"
            Image.new("RGB", MODULE.CANVAS, (20, 30, 20)).save(source)

            with self.assertRaisesRegex(ValueError, "RGBA eyelid cutout required"):
                MODULE.normalized(source)

    def test_candidate_outside_approved_contour_is_rejected(self) -> None:
        approved = Image.new("L", (12, 8), 0)
        ImageDraw.Draw(approved).rectangle((3, 2, 8, 5), fill=255)
        candidate = Image.new("RGBA", approved.size, (0, 0, 0, 0))
        candidate.putalpha(approved.copy())
        candidate.putpixel((1, 1), (10, 20, 10, 255))

        try:
            expected_sha256 = MODULE.alpha_support_sha256(approved)
            with self.assertRaisesRegex(ValueError, "immutable approved contour"):
                MODULE.validate_state_cutout(
                    "open",
                    candidate,
                    expected_sha256,
                    approved.size,
                )
        finally:
            approved.close()
            candidate.close()

    def test_candidate_inside_approved_contour_is_accepted(self) -> None:
        approved = Image.new("L", (12, 8), 0)
        ImageDraw.Draw(approved).ellipse((2, 1, 9, 6), fill=255)
        candidate = Image.new("RGBA", approved.size, (20, 30, 20, 0))
        candidate.putalpha(approved.copy())

        try:
            expected_sha256 = MODULE.alpha_support_sha256(approved)
            MODULE.validate_state_cutout(
                "open",
                candidate,
                expected_sha256,
                approved.size,
            )
        finally:
            approved.close()
            candidate.close()

    def test_empty_cutout_is_rejected(self) -> None:
        approved = Image.new("L", (12, 8), 255)
        candidate = Image.new("RGBA", approved.size, (0, 0, 0, 0))

        try:
            expected_sha256 = MODULE.alpha_support_sha256(approved)
            with self.assertRaisesRegex(ValueError, "eyelid cutout is empty"):
                MODULE.validate_state_cutout(
                    "closed",
                    candidate,
                    expected_sha256,
                    approved.size,
                )
        finally:
            approved.close()
            candidate.close()

    def test_committed_runtime_contours_match_immutable_approvals(self) -> None:
        for state, expected_sha256 in MODULE.APPROVED_ALPHA_SUPPORT_SHA256.items():
            with self.subTest(state=state):
                path = MODULE.DRAWABLE / f"mobile_eye_{state}.webp"
                with Image.open(path) as source:
                    candidate = source.convert("RGBA")
                try:
                    MODULE.validate_state_cutout(state, candidate, expected_sha256)
                finally:
                    candidate.close()


if __name__ == "__main__":
    unittest.main()
