import pathlib
import re
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "backup-rqlite.sh"
FULL_E2E = pathlib.Path(__file__).resolve().parents[1] / "test-backup-rqlite.sh"


class BackupRqliteShellSecurityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.source = SCRIPT.read_text(encoding="utf-8")
        cls.worker_start = cls.source.index("else\n  image_source=")
        cls.common_start = cls.source.index(
            '\ninstall -m 0600 -- "$keys"', cls.worker_start
        )
        cls.worker = cls.source[cls.worker_start : cls.common_start]
        cls.common = cls.source[cls.common_start :]
        cls.drill = cls.source[
            cls.source.index('if [[ "$drill" -eq 1 ]]; then') : cls.worker_start
        ]
        cls.full_e2e_source = FULL_E2E.read_text(encoding="utf-8")
        full_worker_start = cls.full_e2e_source.index("run_worker() {")
        full_worker_end = cls.full_e2e_source.index(
            "\n}\n\nrun_worker ", full_worker_start
        )
        cls.full_worker_e2e = cls.full_e2e_source[full_worker_start:full_worker_end]

    def test_worker_uses_exact_pinned_python_and_gpg_descriptors(self):
        self.assertIn('worker_gpg="${MAESTRO_BACKUP_GPG:-}"', self.worker)
        self.assertIn('worker_python="${MAESTRO_BACKUP_PYTHON:-}"', self.worker)
        self.assertIn(
            '[[ "$worker_gpg" == "/proc/self/fd/8" && '
            '"$worker_python" == "/proc/self/fd/9" ]] || fail',
            self.worker,
        )
        self.assertIn('python_command=("$worker_python")', self.worker)
        self.assertIn(
            'verify_command=("$worker_python" "$verify_script")', self.worker
        )
        self.assertIn('verify_gpg_executable="$worker_gpg"', self.worker)
        self.assertIn(
            '--gpg-executable "$verify_gpg_executable"', self.common
        )
        self.assertNotIn("python3", self.worker)

    def test_worker_preserves_exact_fd3_through_fd9_contract(self):
        for token in (
            'image_source="$image_input"',
            'runtime="/proc/self/fd/3"',
            '[[ "$0" == "/proc/self/fd/4" ]] || fail',
            '[[ "$image_input" == "$runtime/task-$backup_id/control-plane.sqlite3" ]] || fail',
            '[[ "$keys_input" == "/proc/self/fd/6" ]] || fail',
            '[[ "$verify_script_input" == "/proc/self/fd/5" ]] || fail',
            '[[ "$output_input" == "$runtime/task-$backup_id/backup.bundle" ]] || fail',
            'exec {task_fd}<"$task_input_dir" || fail',
            'task_dir="/proc/self/fd/$task_fd"',
            'image_source="$task_dir/control-plane.sqlite3"',
            'output="$task_dir/backup.bundle"',
        ):
            self.assertIn(token, self.worker)
        for operational in (
            "image_source",
            "keys",
            "verify_script",
            "output_parent",
            "output",
        ):
            self.assertIsNone(
                re.search(rf'(?m)^\s*{operational}="\$\(realpath\b', self.worker),
                f"{operational} must retain its pinned descriptor path",
            )

    def test_worker_validates_descriptor_identity_without_using_resolved_paths(self):
        self.assertIn(
            'runtime_resolved="$(realpath -e -- "$runtime")" || fail', self.worker
        )
        self.assertIn(
            'task_resolved="$(realpath -e -- "$task_input_dir")" || fail',
            self.worker,
        )
        self.assertIn(
            '[[ "$task_resolved" == "$runtime_resolved/task-$backup_id" ]] || fail',
            self.worker,
        )
        self.assertIn('[[ "$task_identity" == "$(stat -Lc ', self.worker)
        self.assertIn('"$task_dir")" ]] || fail', self.worker)

    def test_full_worker_e2e_invokes_actual_creator_with_fd3_through_fd9(self):
        for token in (
            'exec 3<"$sandbox"',
            'exec 4<"$worker_creator"',
            'exec 5<"$ROOT/ops/ha/verify_backup.py"',
            'exec 6<"$keys"',
            'exec 7<"$gpg_home"',
            'exec 8<"$gpg_binary"',
            'exec 9<"$python_binary"',
            'GNUPGHOME=/proc/self/fd/7',
            'MAESTRO_BACKUP_GPG=/proc/self/fd/8',
            'MAESTRO_BACKUP_PYTHON=/proc/self/fd/9',
            '"/proc/self/fd/4" --worker',
            '--image "/proc/self/fd/3/task-$id/control-plane.sqlite3"',
            '--keys /proc/self/fd/6',
            '--output "/proc/self/fd/3/task-$id/backup.bundle"',
            '--verify-script /proc/self/fd/5',
        ):
            self.assertIn(token, self.full_worker_e2e)
        self.assertNotIn('bash "$CREATOR" --worker', self.full_worker_e2e)
        self.assertIn(
            'worker_creator="$sandbox/backup-rqlite-worker"', self.full_e2e_source
        )
        self.assertIn(
            'install -m 0700 -- "$CREATOR" "$worker_creator"',
            self.full_e2e_source,
        )

    def test_worker_gpg_is_offline_and_all_common_calls_are_indirect(self):
        self.assertIn(
            'gpg_command=("$worker_gpg" --no-options --no-auto-key-retrieve '
            '--homedir "$gpg_home")',
            self.worker,
        )
        self.assertNotIn("python3", self.common)
        self.assertIsNone(re.search(r"(?m)^\s*gpg(?:\s|$)", self.common))
        self.assertGreaterEqual(self.source.count("--output -"), 3)
        self.assertIn("run_worker_gpg_output", self.source)
        self.assertIn('rm -f -- "$target"', self.source)
        self.assertIn("set -o noclobber", self.source)

    def test_drill_keeps_path_commands_and_original_tool_argv(self):
        self.assertIn("gpg tar python3", self.source)
        self.assertIn("verify_command=(python3 -m ops.ha.verify_backup)", self.drill)
        self.assertIn("python_command=(python3)", self.drill)
        self.assertIn('gpg_command=(gpg --homedir "$gpg_home")', self.drill)


if __name__ == "__main__":
    unittest.main()
