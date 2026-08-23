import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from ops.ha import backup_worker as worker


@unittest.skipUnless(os.name == "posix", "POSIX dirfd security contract")
class BackupWorkerIdentityRaceTests(unittest.TestCase):
    def make_root(self, base: Path, name: str) -> Path:
        root = base / name
        root.mkdir(mode=0o700)
        root.chmod(0o700)
        return root

    def make_bundle(self, root: Path, backup_id: str, payload: bytes) -> tuple[Path, Path]:
        task = worker.create_task_directory(root, backup_id)
        bundle = task / worker.BUNDLE_NAME
        bundle.write_bytes(payload)
        bundle.chmod(0o600)
        return task, bundle

    def test_validated_bundle_stays_bound_to_original_inode_after_path_replacement(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self.make_root(Path(temporary), "runtime")
            task, bundle = self.make_bundle(root, "backup-pinned", b"original")
            pinned = worker.validate_private_bundle(task)
            try:
                bundle.rename(task / "replaced-bundle")
                bundle.write_bytes(b"replacement")
                bundle.chmod(0o600)
                self.assertEqual(pinned.read(), b"original")
                self.assertNotEqual(os.fstat(pinned.fileno()).st_ino, bundle.stat().st_ino)
            finally:
                pinned.close()

    def test_cleanup_uses_only_directory_relative_unlink_and_rmdir(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self.make_root(Path(temporary), "runtime")
            task = worker.create_task_directory(root, "backup-stale")
            os.utime(task, (1_000, 1_000))
            original_unlink = os.unlink
            original_rmdir = os.rmdir
            calls = []

            def tracked_unlink(path, *args, **kwargs):
                calls.append(("unlink", path, kwargs.get("dir_fd")))
                return original_unlink(path, *args, **kwargs)

            def tracked_rmdir(path, *args, **kwargs):
                calls.append(("rmdir", path, kwargs.get("dir_fd")))
                return original_rmdir(path, *args, **kwargs)

            with mock.patch.object(worker.os, "unlink", side_effect=tracked_unlink), mock.patch.object(
                worker.os,
                "rmdir",
                side_effect=tracked_rmdir,
            ):
                removed = worker.cleanup_stale_task_directories(
                    root,
                    active_backup_ids=frozenset(),
                    now_unix=10_000,
                    stale_after_seconds=3_600,
                )

            self.assertEqual(removed, ("backup-stale",))
            self.assertTrue(calls)
            for operation, path, directory_fd in calls:
                with self.subTest(operation=operation, path=path):
                    self.assertIsInstance(path, str)
                    self.assertEqual(Path(path).name, path)
                    self.assertIsInstance(directory_fd, int)

    def test_cleanup_failure_keeps_marker_last_and_is_resumable(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self.make_root(Path(temporary), "runtime")
            task, _bundle = self.make_bundle(root, "backup-resume", b"sensitive")
            marker = task / worker.OWNER_MARKER
            os.utime(task, (1_000, 1_000))
            original_unlink = os.unlink
            failed = False

            def fail_bundle_once(path, *args, **kwargs):
                nonlocal failed
                if not failed and path == worker.BUNDLE_NAME:
                    failed = True
                    raise OSError("synthetic cleanup failure")
                return original_unlink(path, *args, **kwargs)

            with mock.patch.object(worker.os, "unlink", side_effect=fail_bundle_once):
                removed = worker.cleanup_stale_task_directories(
                    root,
                    active_backup_ids=frozenset(),
                    now_unix=10_000,
                    stale_after_seconds=3_600,
                )
            self.assertEqual(removed, ())
            self.assertTrue(marker.exists())

            removed = worker.cleanup_stale_task_directories(
                root,
                active_backup_ids=frozenset(),
                now_unix=10_000,
                stale_after_seconds=3_600,
            )
            self.assertEqual(removed, ("backup-resume",))
            self.assertFalse(task.exists())

    def test_root_path_replacement_cannot_redirect_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            root = self.make_root(base, "runtime")
            old_task = worker.create_task_directory(root, "backup-old")
            os.utime(old_task, (1_000, 1_000))

            replacement = self.make_root(base, "replacement")
            new_task, new_bundle = self.make_bundle(
                replacement,
                "backup-new",
                b"must-survive",
            )
            os.utime(new_task, (1_000, 1_000))
            parked = base / "parked"
            original_listdir = os.listdir
            swapped = False

            def swapping_listdir(path):
                nonlocal swapped
                if isinstance(path, int) and not swapped:
                    swapped = True
                    root.rename(parked)
                    replacement.rename(root)
                return original_listdir(path)

            with mock.patch.object(worker.os, "listdir", side_effect=swapping_listdir):
                removed = worker.cleanup_stale_task_directories(
                    root,
                    active_backup_ids=frozenset(),
                    now_unix=10_000,
                    stale_after_seconds=3_600,
                )

            self.assertEqual(removed, ("backup-old",))
            self.assertFalse((parked / "task-backup-old").exists())
            self.assertEqual(
                (root / "task-backup-new" / worker.BUNDLE_NAME).read_bytes(),
                b"must-survive",
            )
            self.assertTrue(new_bundle.name == worker.BUNDLE_NAME)


if __name__ == "__main__":
    unittest.main()
