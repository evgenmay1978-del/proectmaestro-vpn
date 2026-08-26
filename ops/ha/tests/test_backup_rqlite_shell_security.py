import pathlib
import re
import unittest


SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "backup-rqlite.sh"


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
        self.assertNotIn("python3", self.worker)

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
