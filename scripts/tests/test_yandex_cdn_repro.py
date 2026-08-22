from __future__ import annotations

import functools
import os
import re
import shutil
import subprocess
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
REPRO_DIR = REPO_ROOT / "scripts" / "repro"
HELPER = REPRO_DIR / "_run-white-list-suite.sh"
WRAPPERS = {
    "yandex-get-body.sh": "yandex_get_body",
    "yandex-active-stream.sh": "yandex_active_stream",
    "yandex-idle-cutoff.sh": "yandex_idle_cutoff",
    "yandex-literal-edge.sh": "yandex_literal_edge",
    "xray-counter-reset.sh": "xray_counter_reset",
    "billing-idempotency.sh": "billing_idempotency",
    "duplicate-event-replay.sh": "duplicate_event_replay",
    "subscription-escaping.sh": "subscription_escaping",
    "edge-rotation.sh": "edge_rotation",
}
ALL_SHELL_FILES = [HELPER, *(REPRO_DIR / name for name in WRAPPERS)]
FORBIDDEN = re.compile(
    r"(?:https?://|\bcurl\b|\bwget\b|\bssh\b|\bsocat\b|\bnc\b|\beval\b|"
    r"\bsystemctl\b|\bservice\b|\bufw\b|\biptables\b|\bnft\b|\bendpoint\b|"
    r"\btoken\b|\bsecret\b|/dev/tcp|openssl\s+s_client|\btelnet\b)",
    re.IGNORECASE,
)


@functools.lru_cache(maxsize=1)
def usable_bash() -> str | None:
    bash = shutil.which("bash")
    if bash is None:
        return None
    try:
        probe = subprocess.run(
            [bash, "--version"],
            check=False,
            capture_output=True,
            timeout=10,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if probe.returncode != 0:
        return None
    return bash


class ReproductionWrappersTest(unittest.TestCase):
    def test_public_wrappers_are_closed_offline_entrypoints(self) -> None:
        for filename, suite in WRAPPERS.items():
            with self.subTest(filename=filename):
                path = REPRO_DIR / filename
                self.assertTrue(path.is_file(), f"missing wrapper: {path}")
                source = path.read_text(encoding="utf-8")
                self.assertTrue(source.startswith("#!/usr/bin/env bash\nset -euo pipefail\n"))
                self.assertIn('if [ "$#" -ne 0 ]; then', source)
                self.assertIn("SCRIPT_DIR=", source)
                self.assertIn('"$SCRIPT_DIR/_run-white-list-suite.sh"', source)
                self.assertIn(f'"{suite}"', source)
                self.assertIsNone(FORBIDDEN.search(source))

    def test_helper_has_an_exact_suite_allowlist_and_no_network_primitive(self) -> None:
        self.assertTrue(HELPER.is_file(), f"missing helper: {HELPER}")
        source = HELPER.read_text(encoding="utf-8")
        self.assertTrue(source.startswith("#!/usr/bin/env bash\nset -euo pipefail\n"))
        self.assertIn('if [ "$#" -ne 1 ]; then', source)
        self.assertIn('case "$suite" in', source)
        self.assertIn("GOPROXY=off", source)
        self.assertIn("mktemp", source)
        self.assertIn("trap", source)
        self.assertIn("maestro-whitelist-ready", source)
        self.assertIn("acceptance-catalog.v1.json", source)
        self.assertIn("acceptance-evidence.v1.json", source)
        self.assertIn("client-compatibility-matrix.v1.json", source)
        for suite in WRAPPERS.values():
            self.assertRegex(source, rf"(?m)^\s*{re.escape(suite)}\)$")
        self.assertIsNone(FORBIDDEN.search(source))

    def test_shell_files_have_executable_git_modes(self) -> None:
        for path in ALL_SHELL_FILES:
            relative = path.relative_to(REPO_ROOT).as_posix()
            completed = subprocess.run(
                ["git", "ls-files", "--stage", "--", relative],
                cwd=REPO_ROOT,
                check=False,
                capture_output=True,
                text=True,
            )
            with self.subTest(path=relative):
                self.assertEqual(0, completed.returncode, completed.stderr)
                self.assertTrue(completed.stdout, f"{relative} is not tracked")
                self.assertEqual("100755", completed.stdout.split()[0])

    def test_shell_sources_parse_when_bash_is_available(self) -> None:
        bash = usable_bash()
        if bash is None:
            self.skipTest("bash is not installed")
        for path in ALL_SHELL_FILES:
            with self.subTest(path=path.name):
                completed = subprocess.run(
                    [bash, "-n", str(path)],
                    cwd=REPO_ROOT,
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(0, completed.returncode, completed.stderr)

    def test_public_wrappers_reject_arguments_when_bash_is_available(self) -> None:
        bash = usable_bash()
        if bash is None:
            self.skipTest("bash is not installed")
        for filename in WRAPPERS:
            completed = subprocess.run(
                [bash, str(REPRO_DIR / filename), "unexpected"],
                cwd=REPO_ROOT,
                check=False,
                capture_output=True,
                text=True,
            )
            with self.subTest(filename=filename):
                self.assertEqual(2, completed.returncode)
                self.assertEqual('{"error":"arguments_invalid"}\n', completed.stderr)
                self.assertEqual("", completed.stdout)

    def test_public_wrappers_emit_exact_pass_and_no_go_when_tools_are_available(self) -> None:
        bash = usable_bash()
        if bash is None or shutil.which("go") is None:
            self.skipTest("usable bash and Go are required for semantic replay")
        environment = os.environ.copy()
        environment["GOPROXY"] = "off"
        for filename, suite in WRAPPERS.items():
            completed = subprocess.run(
                [bash, str(REPRO_DIR / filename)],
                cwd=REPO_ROOT,
                check=False,
                capture_output=True,
                text=True,
                timeout=120,
                env=environment,
            )
            expected = (
                '{"harness_status":"PASS","release_readiness":"NO_GO",'
                f'"evidence_class":"FIXTURE_REPLAY","selected_suite":"{suite}"}}\n'
            )
            with self.subTest(filename=filename):
                self.assertEqual(0, completed.returncode, completed.stderr)
                self.assertEqual(expected, completed.stdout)
                self.assertEqual("", completed.stderr)


if __name__ == "__main__":
    unittest.main()
