from __future__ import annotations

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("mobile-4d-assets.py")


def load_pipeline():
    module_name = "maestro_mobile_4d_assets_under_test"
    spec = importlib.util.spec_from_file_location(module_name, SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    spec.loader.exec_module(module)
    return module


class Mobile4DAssetInstallSafetyTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.pipeline = load_pipeline()

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="mobile-4d-install-test-")
        self.root = Path(self.temporary.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.target = self.pipeline.asset_output_root(self.repo)
        self.target.mkdir(parents=True)
        self.manifest = self.pipeline.manifest_output_path(self.repo)
        self.manifest.parent.mkdir(parents=True)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def staging(self, name: str, files: dict[str, bytes]) -> Path:
        staging = self.root / name
        staging.mkdir()
        for filename, contents in files.items():
            (staging / filename).write_bytes(contents)
        return staging

    def test_manifest_package_ancestor_symlink_is_rejected_before_write(self) -> None:
        redirected = self.root / "redirected-package"
        redirected.mkdir()
        self.manifest.parent.rmdir()
        self.manifest.parent.symlink_to(redirected, target_is_directory=True)
        staging = self.staging("staging", {"atlas.webp": b"atlas"})

        with self.assertRaisesRegex(ValueError, "manifest.*redirect"):
            self.pipeline.install_outputs(self.repo, staging, "new manifest")

        self.assertFalse((redirected / self.manifest.name).exists())
        self.assertEqual([], list(self.target.iterdir()))

    def test_manifest_destination_symlink_is_rejected_before_write(self) -> None:
        redirected = self.root / "redirected-manifest.kt"
        redirected.write_text("outside manifest", encoding="utf-8")
        self.manifest.symlink_to(redirected)
        staging = self.staging("staging", {"atlas.webp": b"atlas"})

        with self.assertRaisesRegex(ValueError, "manifest.*redirect"):
            self.pipeline.install_outputs(self.repo, staging, "new manifest")

        self.assertEqual(
            "outside manifest",
            redirected.read_text(encoding="utf-8"),
        )
        self.assertEqual([], list(self.target.iterdir()))

    def test_keyboard_interrupt_rolls_back_assets_and_manifest(self) -> None:
        (self.target / "old.webp").write_bytes(b"old atlas")
        self.manifest.write_text("old manifest", encoding="utf-8")
        staging = self.staging(
            "staging",
            {"new-a.webp": b"new a", "new-b.webp": b"new b"},
        )
        real_replace = self.pipeline.os.replace

        def interrupt_during_install(source, destination):
            if (
                Path(source).name == "new-b.webp"
                and Path(destination).parent == self.target
            ):
                raise KeyboardInterrupt("injected interrupt")
            return real_replace(source, destination)

        with mock.patch.object(
            self.pipeline.os,
            "replace",
            side_effect=interrupt_during_install,
        ):
            with self.assertRaisesRegex(KeyboardInterrupt, "injected interrupt"):
                self.pipeline.install_outputs(self.repo, staging, "new manifest")

        self.assertEqual({"old.webp"}, {path.name for path in self.target.iterdir()})
        self.assertEqual(b"old atlas", (self.target / "old.webp").read_bytes())
        self.assertEqual("old manifest", self.manifest.read_text(encoding="utf-8"))

    def test_next_install_recovers_durable_interrupted_transaction_first(self) -> None:
        (self.target / "old.webp").write_bytes(b"old atlas")
        self.manifest.write_text("old manifest", encoding="utf-8")
        interrupted_staging = self.staging(
            "interrupted-staging",
            {"new-a.webp": b"new a", "new-b.webp": b"new b"},
        )
        child_code = r'''
import importlib.util
import os
import sys
from pathlib import Path

script, repo, staging = map(Path, sys.argv[1:])
name = "maestro_mobile_4d_assets_interrupted_child"
spec = importlib.util.spec_from_file_location(name, script)
module = importlib.util.module_from_spec(spec)
sys.modules[name] = module
spec.loader.exec_module(module)
target = module.asset_output_root(repo)
real_replace = module.os.replace

def terminate_after_first_new_asset(source, destination):
    result = real_replace(source, destination)
    if Path(source).name == "new-a.webp" and Path(destination).parent == target:
        os._exit(91)
    return result

module.os.replace = terminate_after_first_new_asset
module.install_outputs(repo, staging, "interrupted manifest")
'''
        interrupted = subprocess.run(
            [
                sys.executable,
                "-c",
                child_code,
                str(SCRIPT_PATH),
                str(self.repo),
                str(interrupted_staging),
            ],
            check=False,
        )
        self.assertEqual(91, interrupted.returncode)

        final_staging = self.staging("final-staging", {"final.webp": b"final"})
        real_copyfile = self.pipeline.shutil.copyfile
        recovery_observed = False

        def assert_recovered_before_copy(source, destination):
            nonlocal recovery_observed
            if Path(source).parent == final_staging and not recovery_observed:
                self.assertTrue(
                    (self.target / "old.webp").is_file(),
                    "interrupted transaction was not recovered before copying",
                )
                self.assertEqual(b"old atlas", (self.target / "old.webp").read_bytes())
                self.assertEqual(
                    "old manifest",
                    self.manifest.read_text(encoding="utf-8"),
                )
                recovery_observed = True
            return real_copyfile(source, destination)

        with mock.patch.object(
            self.pipeline.shutil,
            "copyfile",
            side_effect=assert_recovered_before_copy,
        ):
            self.pipeline.install_outputs(self.repo, final_staging, "final manifest")

        self.assertTrue(recovery_observed)
        self.assertEqual({"final.webp"}, {path.name for path in self.target.iterdir()})
        self.assertEqual(b"final", (self.target / "final.webp").read_bytes())
        self.assertEqual("final manifest", self.manifest.read_text(encoding="utf-8"))

    def test_rgba_comparison_ignores_rgb_only_beneath_zero_alpha(self) -> None:
        expected = self.pipeline.Image.new("RGBA", (2, 1))
        actual = self.pipeline.Image.new("RGBA", (2, 1))
        try:
            expected.putdata([(10, 20, 30, 0), (1, 2, 3, 255)])
            actual.putdata([(200, 150, 100, 0), (1, 2, 3, 255)])

            self.assertTrue(self.pipeline.visible_rgba_equal(expected, actual))
        finally:
            actual.close()
            expected.close()

    def test_rgba_comparison_rejects_visible_rgb_difference(self) -> None:
        expected = self.pipeline.Image.new("RGBA", (1, 1), (1, 2, 3, 255))
        actual = self.pipeline.Image.new("RGBA", (1, 1), (1, 2, 4, 255))
        try:
            self.assertFalse(self.pipeline.visible_rgba_equal(expected, actual))
        finally:
            actual.close()
            expected.close()

    def test_rgba_comparison_rejects_alpha_difference(self) -> None:
        expected = self.pipeline.Image.new("RGBA", (1, 1), (1, 2, 3, 255))
        actual = self.pipeline.Image.new("RGBA", (1, 1), (1, 2, 3, 254))
        try:
            self.assertFalse(self.pipeline.visible_rgba_equal(expected, actual))
        finally:
            actual.close()
            expected.close()


if __name__ == "__main__":
    unittest.main()
