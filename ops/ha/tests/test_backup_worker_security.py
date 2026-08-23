import ast
import os
import tempfile
import unittest
from unittest import mock
from pathlib import Path

from ops.ha import backup_worker as worker


class BackupWorkerSecurityContractTests(unittest.TestCase):
    def test_runtime_api_is_present_on_every_platform(self):
        for name in (
            "create_task_directory",
            "validate_private_bundle",
            "cleanup_stale_task_directories",
            "PinnedBundle",
        ):
            with self.subTest(name=name):
                self.assertTrue(hasattr(worker, name), name)
                self.assertTrue(callable(getattr(worker, name, None)), name)

    def test_create_collision_preserves_existing_owned_task(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "runtime"
            root.mkdir(mode=0o700)
            if os.name != "nt":
                root.chmod(0o700)
            task = worker.create_task_directory(root, "backup-collision")
            marker = task / worker.OWNER_MARKER
            marker_before = marker.read_bytes()
            bundle = task / worker.BUNDLE_NAME
            bundle.write_bytes(b"existing-bundle-marker")
            if os.name != "nt":
                bundle.chmod(0o600)

            with self.assertRaisesRegex(
                worker.BackupWorkerError,
                "^backup-worker:unsafe-runtime$",
            ):
                worker.create_task_directory(root, "backup-collision")

            self.assertEqual(marker.read_bytes(), marker_before)
            self.assertEqual(bundle.read_bytes(), b"existing-bundle-marker")
            self.assertTrue(task.is_dir())

    def test_cleanup_removes_exact_stale_crash_intermediates(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "runtime"
            root.mkdir(mode=0o700)
            if os.name != "nt":
                root.chmod(0o700)
            task = worker.create_task_directory(root, "backup-crashed")
            for name in worker.INTERMEDIATE_FILES:
                path = task / name
                path.write_bytes((name + "\n").encode("ascii"))
                if os.name != "nt":
                    path.chmod(0o600)
            verify = task / worker.VERIFY_DIR_NAME
            verify.mkdir(mode=0o700)
            if os.name != "nt":
                verify.chmod(0o700)
            for name in worker.VERIFY_MEMBER_FILES:
                path = verify / name
                path.write_bytes((name + "\n").encode("ascii"))
                if os.name != "nt":
                    path.chmod(0o600)
            os.utime(task, (1_000, 1_000))

            removed = worker.cleanup_stale_task_directories(
                root,
                active_backup_ids=frozenset(),
                now_unix=10_000,
                stale_after_seconds=3_600,
            )
            self.assertEqual(removed, ("backup-crashed",))
            self.assertFalse(task.exists())

    def test_fallback_cleanup_failure_preserves_owner_marker_and_retries(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "runtime"
            root.mkdir(mode=0o700)
            if os.name != "nt":
                root.chmod(0o700)
            task = worker.create_task_directory(root, "backup-resume")
            marker = task / worker.OWNER_MARKER
            bundle = task / worker.BUNDLE_NAME
            bundle.write_bytes(b"sensitive")
            if os.name != "nt":
                bundle.chmod(0o600)
            os.utime(task, (1_000, 1_000))
            original_unlink = Path.unlink
            failed = False

            def fail_bundle_once(path, *args, **kwargs):
                nonlocal failed
                if not failed and path.name == worker.BUNDLE_NAME:
                    failed = True
                    raise OSError("synthetic cleanup failure")
                return original_unlink(path, *args, **kwargs)

            with mock.patch.object(
                worker,
                "_pinned_directory_supported",
                return_value=False,
            ), mock.patch.object(Path, "unlink", new=fail_bundle_once):
                removed = worker.cleanup_stale_task_directories(
                    root,
                    active_backup_ids=frozenset(),
                    now_unix=10_000,
                    stale_after_seconds=3_600,
                )
            self.assertEqual(removed, ())
            self.assertTrue(marker.exists())

            with mock.patch.object(worker, "_pinned_directory_supported", return_value=False):
                removed = worker.cleanup_stale_task_directories(
                    root,
                    active_backup_ids=frozenset(),
                    now_unix=10_000,
                    stale_after_seconds=3_600,
                )
            self.assertEqual(removed, ("backup-resume",))
            self.assertFalse(task.exists())

    def test_cutover_accepts_only_ha_enabled_with_legacy_disabled(self):
        worker.require_ha_backup_exclusive(legacy_enabled=False, ha_enabled=True)
        for legacy_enabled, ha_enabled in ((False, False), (True, False), (True, True)):
            with self.subTest(legacy=legacy_enabled, ha=ha_enabled):
                with self.assertRaisesRegex(
                    worker.BackupWorkerError,
                    "^backup-worker:unsafe-cutover$",
                ):
                    worker.require_ha_backup_exclusive(
                        legacy_enabled=legacy_enabled,
                        ha_enabled=ha_enabled,
                    )

    def test_module_is_offline_and_public_errors_are_fixed(self):
        tree = ast.parse(Path(worker.__file__).read_text(encoding="utf-8"))
        forbidden = {
            "subprocess",
            "socket",
            "requests",
            "urllib",
            "http",
            "ftplib",
            "paramiko",
        }
        imported = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imported.update(alias.name.split(".")[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                imported.add(node.module.split(".")[0])
        self.assertFalse(imported & forbidden)
        self.assertEqual(
            worker.PUBLIC_ERRORS,
            frozenset(
                {
                    "backup-worker:invalid-state",
                    "backup-worker:invalid-transition",
                    "backup-worker:stale-lease",
                    "backup-worker:verification-mismatch",
                    "backup-worker:unsafe-runtime",
                    "backup-worker:unsafe-cutover",
                }
            ),
        )


if __name__ == "__main__":
    unittest.main()
