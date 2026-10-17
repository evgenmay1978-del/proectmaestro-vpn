import json
import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "ops" / "ha" / "inventory.sh"


def find_usable_bash() -> str | None:
    candidate = shutil.which("bash")
    if candidate is None:
        return None
    try:
        probe = subprocess.run(
            [candidate, "--version"],
            text=True,
            capture_output=True,
            timeout=5,
            check=False,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if probe.returncode != 0 or "GNU bash" not in probe.stdout:
        return None
    return candidate


BASH = find_usable_bash()

EXPECTED_INVALID = (
    b'{"backup_implementation":"unknown","blocker_codes":'
    b'["fixture_input_invalid"],"evidence_class":"FIXTURE",'
    b'"format_version":1,"release_readiness":"NO_GO"}\n'
)


@unittest.skipIf(BASH is None, "usable GNU Bash is required for the inventory contract")
class InventoryFixtureContractTest(unittest.TestCase):
    def make_fixture(self, directory: Path, *, legacy: bool, ha: bool) -> None:
        directory.mkdir()
        self.write_source(directory / "legacy-backup.json", "legacy", legacy)
        self.write_source(directory / "ha-backup.json", "ha", ha)

    def write_source(self, path: Path, kind: str, enabled: bool) -> None:
        value = {"enabled": enabled, "format_version": 1, "kind": kind}
        path.write_text(
            json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )

    def run_inventory(
        self,
        fixture_root: Path | None,
        output: Path | None,
        *,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        args = [BASH, str(SCRIPT)]
        if fixture_root is not None:
            args.extend(["--fixture-root", str(fixture_root)])
        if output is not None:
            args.extend(["--output", str(output)])
        return subprocess.run(
            args,
            cwd=ROOT,
            env=env,
            text=True,
            capture_output=True,
            timeout=10,
            check=False,
        )

    def test_requires_explicit_fixture_root_and_output(self) -> None:
        result = self.run_inventory(None, None)

        self.assertEqual(result.returncode, 3)
        self.assertEqual(
            result.stderr,
            "inventory: explicit fixture root and output are required\n",
        )
        self.assertEqual(result.stdout, "")

    def test_refuses_to_replace_an_existing_output(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            self.make_fixture(fixture, legacy=True, ha=False)
            output.write_text("keep\n", encoding="utf-8")

            result = self.run_inventory(fixture, output)

            self.assertEqual(result.returncode, 3)
            self.assertEqual(
                result.stderr,
                "inventory: output must be a new regular file\n",
            )
            self.assertEqual(output.read_text(encoding="utf-8"), "keep\n")

    def test_exactly_one_enabled_implementation_is_canonical_and_no_go(self) -> None:
        for selected in ("legacy", "ha"):
            with self.subTest(selected=selected), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = root / "fixture"
                first = root / "first.json"
                second = root / "second.json"
                self.make_fixture(
                    fixture,
                    legacy=selected == "legacy",
                    ha=selected == "ha",
                )
                expected = (
                    '{"backup_implementation":"'
                    + selected
                    + '","blocker_codes":[],"evidence_class":"FIXTURE",'
                    '"format_version":1,"release_readiness":"NO_GO"}\n'
                ).encode("ascii")

                first_result = self.run_inventory(fixture, first)
                second_result = self.run_inventory(fixture, second)

                self.assertEqual(first_result.returncode, 0)
                self.assertEqual(second_result.returncode, 0)
                self.assertEqual(first_result.stdout, "")
                self.assertEqual(first_result.stderr, "")
                self.assertEqual(first.read_bytes(), expected)
                self.assertEqual(second.read_bytes(), expected)
                self.assertNotIn(str(root).encode(), expected)
                if os.name != "nt":
                    self.assertEqual(stat.S_IMODE(first.stat().st_mode), 0o600)

    def test_zero_or_two_enabled_implementations_are_blocked(self) -> None:
        cases = (
            (
                False,
                False,
                "none",
                "backup_implementation_missing",
            ),
            (
                True,
                True,
                "conflict",
                "backup_implementation_conflict",
            ),
        )
        for legacy, ha, selected, blocker in cases:
            with self.subTest(blocker=blocker), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = root / "fixture"
                output = root / "report.json"
                self.make_fixture(fixture, legacy=legacy, ha=ha)
                expected = (
                    '{"backup_implementation":"'
                    + selected
                    + '","blocker_codes":["'
                    + blocker
                    + '"],"evidence_class":"FIXTURE","format_version":1,'
                    '"release_readiness":"NO_GO"}\n'
                ).encode("ascii")

                result = self.run_inventory(fixture, output)

                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, "")
                self.assertEqual(result.stderr, "")
                self.assertEqual(output.read_bytes(), expected)

    def test_missing_extra_oversized_or_malformed_sources_are_generic_blockers(self) -> None:
        def missing(fixture: Path) -> None:
            (fixture / "ha-backup.json").unlink()

        def extra(fixture: Path) -> None:
            (fixture / "unexpected.json").write_text("{}\n", encoding="utf-8")

        def oversized(fixture: Path) -> None:
            (fixture / "ha-backup.json").write_bytes(b"x" * 4097)

        def malformed(fixture: Path) -> None:
            (fixture / "legacy-backup.json").write_text(
                '{"enabled":true,"format_version":1,"kind":"legacy",'
                '"token":"fixture-secret-value"}\n',
                encoding="utf-8",
            )

        for name, mutate in (
            ("missing", missing),
            ("extra", extra),
            ("oversized", oversized),
            ("malformed", malformed),
        ):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = root / "fixture"
                output = root / "report.json"
                self.make_fixture(fixture, legacy=True, ha=False)
                mutate(fixture)

                result = self.run_inventory(fixture, output)

                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, "")
                self.assertEqual(result.stderr, "")
                self.assertEqual(output.read_bytes(), EXPECTED_INVALID)
                self.assertNotIn(b"fixture-secret-value", output.read_bytes())
                self.assertNotIn(str(root).encode(), output.read_bytes())

    def test_symlinked_source_is_a_generic_blocker(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            target = root / "target.json"
            self.make_fixture(fixture, legacy=True, ha=False)
            self.write_source(target, "ha", False)
            (fixture / "ha-backup.json").unlink()
            try:
                os.symlink(target, fixture / "ha-backup.json")
            except OSError as error:
                self.skipTest(f"symlinks unavailable: {error}")

            result = self.run_inventory(fixture, output)

            self.assertEqual(result.returncode, 2)
            self.assertEqual(result.stderr, "")
            self.assertEqual(output.read_bytes(), EXPECTED_INVALID)

    def test_fixture_mode_does_not_invoke_live_or_network_tools(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            fake_bin = root / "fake-bin"
            marker = root / "live-tool-called"
            self.make_fixture(fixture, legacy=False, ha=True)
            fake_bin.mkdir()
            for name in ("systemctl", "ssh", "curl", "wget", "nc", "socat", "aws"):
                command = fake_bin / name
                command.write_text(
                    "#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$INVENTORY_SENTINEL\"\nexit 91\n",
                    encoding="utf-8",
                )
                command.chmod(0o700)
            environment = os.environ.copy()
            environment["PATH"] = str(fake_bin) + os.pathsep + environment.get("PATH", "")
            environment["INVENTORY_SENTINEL"] = str(marker)

            result = self.run_inventory(fixture, output, env=environment)

            self.assertEqual(result.returncode, 0)
            self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
