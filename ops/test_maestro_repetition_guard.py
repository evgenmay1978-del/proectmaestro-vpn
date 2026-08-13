import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("maestro-repetition-guard.py")


class RepetitionGuardCliTest(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.ledger = Path(self.temp_dir.name) / "guard.json"

    def run_guard(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT), "--ledger", str(self.ledger), *args],
            capture_output=True,
            text=True,
            encoding="utf-8",
            check=False,
        )

    def test_first_attempt_is_allowed_without_writing_state(self):
        result = self.run_guard(
            "check", "--action", "s1-key-login", "--family", "openssh-key-probe"
        )

        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("ALLOW", result.stdout)
        self.assertFalse(self.ledger.exists())

    def test_failure_blocks_same_and_alternate_attempts(self):
        failure = self.run_guard(
            "fail",
            "--action",
            "s1-key-login",
            "--family",
            "puttygen-gui",
            "--reason-code",
            "hidden-interactive-prompt",
        )
        same = self.run_guard(
            "check", "--action", "s1-key-login", "--family", "puttygen-gui"
        )
        alternate = self.run_guard(
            "check", "--action", "s1-key-login", "--family", "plink-direct"
        )

        self.assertEqual(0, failure.returncode, failure.stderr)
        self.assertEqual(42, same.returncode)
        self.assertEqual(42, alternate.returncode)
        self.assertIn("BLOCK", same.stdout)
        self.assertIn("BLOCK", alternate.stdout)

    def test_correction_requires_a_different_family_and_root_cause(self):
        self.run_guard(
            "fail",
            "--action",
            "s1-key-login",
            "--family",
            "puttygen-gui",
            "--reason-code",
            "hidden-interactive-prompt",
        )
        unchanged = self.run_guard(
            "correct",
            "--action",
            "s1-key-login",
            "--old-family",
            "puttygen-gui",
            "--new-family",
            "puttygen-gui",
            "--root-cause-code",
            "gui-needs-tty",
        )
        corrected = self.run_guard(
            "correct",
            "--action",
            "s1-key-login",
            "--old-family",
            "puttygen-gui",
            "--new-family",
            "openssh-key-probe",
            "--root-cause-code",
            "gui-needs-tty",
        )
        old_check = self.run_guard(
            "check", "--action", "s1-key-login", "--family", "puttygen-gui"
        )
        new_check = self.run_guard(
            "check", "--action", "s1-key-login", "--family", "openssh-key-probe"
        )

        self.assertEqual(42, unchanged.returncode)
        self.assertEqual(0, corrected.returncode, corrected.stderr)
        self.assertEqual(42, old_check.returncode)
        self.assertEqual(0, new_check.returncode, new_check.stderr)

    def test_ledger_never_stores_raw_command_family(self):
        raw_family = "command-containing-sensitive-material"
        self.run_guard(
            "fail",
            "--action",
            "server-probe",
            "--family",
            raw_family,
            "--reason-code",
            "transport-failed",
        )

        raw_ledger = self.ledger.read_text(encoding="utf-8")
        state = json.loads(raw_ledger)
        self.assertNotIn(raw_family, raw_ledger)
        self.assertRegex(
            state["actions"]["server-probe"]["blocked_family_sha256"],
            r"^[0-9a-f]{64}$",
        )

    def test_failed_corrected_attempt_becomes_a_new_block(self):
        self.run_guard(
            "fail",
            "--action",
            "skill-validation",
            "--family",
            "system-python",
            "--reason-code",
            "missing-dependency",
        )
        self.run_guard(
            "correct",
            "--action",
            "skill-validation",
            "--old-family",
            "system-python",
            "--new-family",
            "isolated-dependency",
            "--root-cause-code",
            "dependency-not-bundled",
        )
        second_failure = self.run_guard(
            "fail",
            "--action",
            "skill-validation",
            "--family",
            "isolated-dependency",
            "--reason-code",
            "proxy-tls-failed",
        )
        blocked = self.run_guard(
            "check",
            "--action",
            "skill-validation",
            "--family",
            "isolated-dependency",
        )
        state = json.loads(self.ledger.read_text(encoding="utf-8"))
        entry = state["actions"]["skill-validation"]

        self.assertEqual(0, second_failure.returncode, second_failure.stderr)
        self.assertEqual(42, blocked.returncode)
        self.assertEqual("blocked", entry["status"])
        self.assertEqual("proxy-tls-failed", entry["reason_code"])
        self.assertNotIn("root_cause_code", entry)
        self.assertEqual(3, len(entry["history"]))

    def test_success_releases_the_action_for_future_work(self):
        self.run_guard(
            "fail",
            "--action",
            "s1-key-login",
            "--family",
            "puttygen-gui",
            "--reason-code",
            "hidden-interactive-prompt",
        )
        self.run_guard(
            "correct",
            "--action",
            "s1-key-login",
            "--old-family",
            "puttygen-gui",
            "--new-family",
            "openssh-key-probe",
            "--root-cause-code",
            "gui-needs-tty",
        )
        success = self.run_guard(
            "success",
            "--action",
            "s1-key-login",
            "--family",
            "openssh-key-probe",
            "--evidence-code",
            "key-only-login-confirmed",
        )
        future = self.run_guard(
            "check", "--action", "s1-key-login", "--family", "future-safe-probe"
        )

        self.assertEqual(0, success.returncode, success.stderr)
        self.assertEqual(0, future.returncode, future.stderr)


if __name__ == "__main__":
    unittest.main()
