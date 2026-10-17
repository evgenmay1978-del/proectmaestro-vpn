import contextlib
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from ops.ha.tests.test_inventory_hardening import EXPECTED_INVALID, contract_sources


PINNED_DIRECTORY_SUPPORTED = (
    hasattr(os, "O_DIRECTORY")
    and os.open in os.supports_dir_fd
    and os.stat in os.supports_dir_fd
    and os.stat in os.supports_follow_symlinks
    and os.listdir in os.supports_fd
)


class InventoryIdentityRaceTest(unittest.TestCase):
    def write_source(self, path: Path, kind: str, enabled: bool) -> None:
        document = {"enabled": enabled, "format_version": 1, "kind": kind}
        path.write_text(
            json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )

    def make_fixture(self, directory: Path, *, legacy: bool, ha: bool) -> None:
        directory.mkdir()
        self.write_source(directory / "legacy-backup.json", "legacy", legacy)
        self.write_source(directory / "ha-backup.json", "ha", ha)

    def run_embedded(
        self,
        fixture_root: Path,
        output: Path,
        *patches: mock._patch,
    ) -> tuple[int, str]:
        _, embedded = contract_sources()
        stderr = io.StringIO()
        with contextlib.ExitStack() as stack:
            stack.enter_context(
                mock.patch.object(
                    sys,
                    "argv",
                    ["inventory", str(fixture_root), str(output)],
                )
            )
            stack.enter_context(contextlib.redirect_stderr(stderr))
            for patcher in patches:
                stack.enter_context(patcher)
            with self.assertRaises(SystemExit) as stopped:
                exec(
                    compile(embedded, "inventory.sh", "exec"),
                    {"__name__": "__inventory_race__"},
                )
        return int(stopped.exception.code), stderr.getvalue()

    def assert_generic_blocker(self, code: int, stderr: str, output: Path) -> None:
        self.assertEqual(code, 2)
        self.assertEqual(stderr, "")
        self.assertEqual(output.read_bytes(), EXPECTED_INVALID)

    def patched_open_support(self, original_open, open_mock) -> set:
        supported = set(os.supports_dir_fd)
        if original_open in supported:
            supported.remove(original_open)
            supported.add(open_mock)
        return supported

    def test_source_replacement_between_stat_and_open_is_blocked(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            source = fixture / "legacy-backup.json"
            self.make_fixture(fixture, legacy=True, ha=False)
            original_open = os.open
            swapped = False
            source_dir_fd_seen = False

            def swapping_open(path, flags, mode=0o777, *, dir_fd=None):
                nonlocal swapped, source_dir_fd_seen
                path_text = os.fspath(path)
                is_source = Path(path_text).name == source.name
                is_read_only = not flags & (os.O_CREAT | os.O_WRONLY | os.O_RDWR)
                if not swapped and is_source and is_read_only:
                    swapped = True
                    source_dir_fd_seen = dir_fd is not None
                    source.unlink()
                    self.write_source(source, "legacy", False)
                if dir_fd is None:
                    return original_open(path, flags, mode)
                return original_open(path, flags, mode, dir_fd=dir_fd)

            open_mock = mock.Mock(side_effect=swapping_open)
            code, stderr = self.run_embedded(
                fixture,
                output,
                mock.patch.object(os, "open", open_mock),
                mock.patch.object(
                    os,
                    "supports_dir_fd",
                    self.patched_open_support(original_open, open_mock),
                ),
            )

            self.assertTrue(swapped)
            if PINNED_DIRECTORY_SUPPORTED:
                self.assertTrue(source_dir_fd_seen)
            self.assert_generic_blocker(code, stderr, output)

    @unittest.skipUnless(PINNED_DIRECTORY_SUPPORTED, "POSIX dirfd contract")
    def test_root_replacement_before_pin_or_identity_recheck_is_blocked(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            original_fixture = root / "original-fixture"
            output = root / "report.json"
            self.make_fixture(fixture, legacy=True, ha=False)
            original_open = os.open
            swapped = False
            root_directory_open_seen = False

            def replace_root() -> None:
                nonlocal swapped
                if swapped:
                    return
                swapped = True
                fixture.rename(original_fixture)
                self.make_fixture(fixture, legacy=False, ha=True)

            def swapping_open(path, flags, mode=0o777, *, dir_fd=None):
                nonlocal root_directory_open_seen
                if (
                    not swapped
                    and os.fspath(path) == os.fspath(fixture)
                    and flags & getattr(os, "O_DIRECTORY", 0)
                ):
                    root_directory_open_seen = True
                    replace_root()
                if dir_fd is None:
                    return original_open(path, flags, mode)
                return original_open(path, flags, mode, dir_fd=dir_fd)


            open_mock = mock.Mock(side_effect=swapping_open)
            code, stderr = self.run_embedded(
                fixture,
                output,
                mock.patch.object(os, "open", open_mock),
                mock.patch.object(
                    os,
                    "supports_dir_fd",
                    self.patched_open_support(original_open, open_mock),
                ),
            )

            self.assertTrue(swapped)
            if PINNED_DIRECTORY_SUPPORTED:
                self.assertTrue(root_directory_open_seen)
            self.assert_generic_blocker(code, stderr, output)

    def test_forced_fallback_root_replacement_is_blocked(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            original_fixture = root / "original-fixture"
            output = root / "report.json"
            self.make_fixture(fixture, legacy=True, ha=False)
            original_listdir = os.listdir
            swapped = False

            def swapping_listdir(path):
                nonlocal swapped
                if not swapped and os.fspath(path) == os.fspath(fixture):
                    swapped = True
                    fixture.rename(original_fixture)
                    self.make_fixture(fixture, legacy=False, ha=True)
                return original_listdir(path)

            code, stderr = self.run_embedded(
                fixture,
                output,
                mock.patch.object(os, "supports_dir_fd", set()),
                mock.patch.object(os, "listdir", side_effect=swapping_listdir),
            )

            self.assertTrue(swapped)
            self.assert_generic_blocker(code, stderr, output)


if __name__ == "__main__":
    unittest.main()
