import ast
import contextlib
import io
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "ops" / "ha" / "inventory.sh"
HEREDOC_MARKER = '"$python_bin" - "$fixture_root" "$output" <<\'PY\'\n'
EXPECTED_INVALID = (
    b'{"backup_implementation":"unknown","blocker_codes":'
    b'["fixture_input_invalid"],"evidence_class":"FIXTURE",'
    b'"format_version":1,"release_readiness":"NO_GO"}\n'
)
EXPECTED_WRAPPER = r"""#!/usr/bin/env bash
set -euo pipefail

umask 077

usage_error() {
  printf '%s\n' 'inventory: explicit fixture root and output are required' >&2
  exit 3
}

fixture_root=''
output=''

while (( $# > 0 )); do
  case "$1" in
    --fixture-root)
      if (( $# < 2 )) || [[ -n "$fixture_root" ]]; then
        usage_error
      fi
      fixture_root=$2
      shift 2
      ;;
    --output)
      if (( $# < 2 )) || [[ -n "$output" ]]; then
        usage_error
      fi
      output=$2
      shift 2
      ;;
    *)
      usage_error
      ;;
  esac
done

if [[ -z "$fixture_root" || -z "$output" ]]; then
  usage_error
fi

if command -v python3 >/dev/null 2>&1; then
  python_bin=python3
elif command -v python >/dev/null 2>&1; then
  python_bin=python
else
  printf '%s\n' 'inventory: Python 3 is required' >&2
  exit 3
fi

"""


def contract_sources() -> tuple[str, str]:
    source = SCRIPT.read_text(encoding="utf-8")
    wrapper, marker, remainder = source.partition(HEREDOC_MARKER)
    if marker != HEREDOC_MARKER:
        raise AssertionError("inventory Python heredoc marker is not exact")
    embedded, terminator, tail = remainder.rpartition("\nPY\n")
    if terminator != "\nPY\n" or tail:
        raise AssertionError("inventory Python heredoc terminator is not exact")
    return wrapper, embedded + "\n"


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


class EmbeddedInventoryContractTest(unittest.TestCase):
    def make_fixture(self, directory: Path) -> None:
        directory.mkdir()
        for kind, enabled in (("legacy", True), ("ha", False)):
            document = {"enabled": enabled, "format_version": 1, "kind": kind}
            (directory / f"{kind}-backup.json").write_text(
                json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n",
                encoding="utf-8",
            )

    def run_embedded(
        self,
        fixture_root: Path,
        output: Path,
        *,
        patches: tuple[mock._patch, ...] = (),
    ) -> tuple[int, str]:
        _, embedded = contract_sources()
        stderr = io.StringIO()
        namespace: dict[str, object] = {"__name__": "__inventory_contract__"}
        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.object(sys, "argv", ["inventory", str(fixture_root), str(output)]))
            stack.enter_context(contextlib.redirect_stderr(stderr))
            for patcher in patches:
                stack.enter_context(patcher)
            with self.assertRaises(SystemExit) as stopped:
                exec(compile(embedded, str(SCRIPT), "exec"), namespace)
        return int(stopped.exception.code), stderr.getvalue()

    def assert_generic_blocker(self, fixture_root: Path, output: Path) -> None:
        code, stderr = self.run_embedded(fixture_root, output)
        self.assertEqual(code, 2)
        self.assertEqual(stderr, "")
        self.assertEqual(output.read_bytes(), EXPECTED_INVALID)

    def test_shell_wrapper_and_embedded_imports_are_exactly_offline(self) -> None:
        wrapper, embedded = contract_sources()
        self.assertEqual(wrapper, EXPECTED_WRAPPER)
        tree = ast.parse(embedded)
        imports = {
            alias.name
            for node in ast.walk(tree)
            if isinstance(node, ast.Import)
            for alias in node.names
        }
        self.assertEqual(imports, {"json", "os", "stat", "sys"})
        self.assertFalse(
            any(isinstance(node, ast.ImportFrom) for node in ast.walk(tree))
        )
        dangerous_os_calls = [
            node.func.attr
            for node in ast.walk(tree)
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Attribute)
            and isinstance(node.func.value, ast.Name)
            and node.func.value.id == "os"
            and (
                node.func.attr in {"system", "popen"}
                or node.func.attr.startswith(("exec", "spawn"))
            )
        ]
        self.assertEqual(dangerous_os_calls, [])
        lowered = embedded.lower()
        for forbidden in (
            "subprocess",
            "socket",
            "urllib",
            "http.client",
            "requests",
            "systemctl",
            "journalctl",
            "curl",
            "wget",
            "ssh",
            "socat",
            "nslookup",
            "iptables",
        ):
            self.assertNotIn(forbidden, lowered)

    def test_strict_invalid_fixture_corpus_is_redacted(self) -> None:
        invalid_payloads = {
            "duplicate-key": b'{"enabled":true,"enabled":false,"format_version":1,"kind":"legacy"}\n',
            "invalid-utf8": b"\xff",
            "trailing-json": b'{"enabled":true,"format_version":1,"kind":"legacy"}{}',
            "non-object": b"[]\n",
            "wrong-enabled": b'{"enabled":1,"format_version":1,"kind":"legacy"}\n',
            "wrong-version": b'{"enabled":true,"format_version":2,"kind":"legacy"}\n',
            "boolean-version": b'{"enabled":true,"format_version":true,"kind":"legacy"}\n',
            "wrong-kind": b'{"enabled":true,"format_version":1,"kind":"ha"}\n',
            "zero-byte": b"",
            "deeply-nested": b"[" * 1100 + b"0" + b"]" * 1100,
        }
        for name, payload in invalid_payloads.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = root / "fixture"
                output = root / "report.json"
                self.make_fixture(fixture)
                (fixture / "legacy-backup.json").write_bytes(payload)
                self.assert_generic_blocker(fixture, output)

    def test_non_directory_and_symlinked_fixture_roots_are_blocked(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            regular_root = root / "regular-root"
            output = root / "regular-report.json"
            regular_root.write_text("not a directory\n", encoding="utf-8")
            self.assert_generic_blocker(regular_root, output)

            fixture = root / "fixture"
            linked_root = root / "linked-root"
            linked_output = root / "linked-report.json"
            self.make_fixture(fixture)
            try:
                os.symlink(fixture, linked_root, target_is_directory=True)
            except OSError as error:
                self.skipTest(f"directory symlinks unavailable: {error}")
            self.assert_generic_blocker(linked_root, linked_output)

    def test_non_regular_and_symlinked_sources_are_blocked(self) -> None:
        for source_kind in ("directory", "symlink"):
            with self.subTest(source_kind=source_kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                fixture = root / "fixture"
                output = root / "report.json"
                self.make_fixture(fixture)
                source = fixture / "ha-backup.json"
                source.unlink()
                if source_kind == "directory":
                    source.mkdir()
                else:
                    target = root / "target.json"
                    target.write_text(
                        '{"enabled":false,"format_version":1,"kind":"ha"}\n',
                        encoding="utf-8",
                    )
                    try:
                        os.symlink(target, source)
                    except OSError as error:
                        self.skipTest(f"file symlinks unavailable: {error}")
                self.assert_generic_blocker(fixture, output)

    def test_existing_output_symlink_and_target_are_preserved(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            target = root / "foreign.json"
            self.make_fixture(fixture)
            target.write_bytes(b"foreign\n")
            try:
                os.symlink(target, output)
            except OSError as error:
                self.skipTest(f"file symlinks unavailable: {error}")

            code, stderr = self.run_embedded(fixture, output)

            self.assertEqual(code, 3)
            self.assertEqual(stderr, "inventory: output must be a new regular file\n")
            self.assertTrue(output.is_symlink())
            self.assertEqual(target.read_bytes(), b"foreign\n")

    def test_racing_foreign_output_is_never_unlinked(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            self.make_fixture(fixture)
            original_open = os.open
            original_write = os.write
            original_close = os.close

            def racing_open(path, flags, mode=0o777):
                if os.fspath(path) == os.fspath(output) and flags & os.O_EXCL:
                    descriptor = original_open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | getattr(os, "O_BINARY", 0), 0o600)
                    original_write(descriptor, b"competitor\n")
                    original_close(descriptor)
                    raise FileExistsError("simulated O_EXCL race")
                return original_open(path, flags, mode)

            code, stderr = self.run_embedded(
                fixture,
                output,
                patches=(mock.patch.object(os, "open", side_effect=racing_open),),
            )

            self.assertEqual(code, 3)
            self.assertEqual(stderr, "inventory: output must be a new regular file\n")
            self.assertEqual(output.read_bytes(), b"competitor\n")

    def test_failed_owned_output_is_left_for_safe_manual_cleanup(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            self.make_fixture(fixture)
            original_open = os.open
            original_write = os.write
            output_descriptors: set[int] = set()

            def tracking_open(path, flags, mode=0o777):
                descriptor = original_open(path, flags, mode)
                if os.fspath(path) == os.fspath(output) and flags & os.O_EXCL:
                    output_descriptors.add(descriptor)
                return descriptor

            def failing_write(descriptor, data):
                if descriptor in output_descriptors:
                    raise OSError("simulated write failure")
                return original_write(descriptor, data)

            code, stderr = self.run_embedded(
                fixture,
                output,
                patches=(
                    mock.patch.object(os, "open", side_effect=tracking_open),
                    mock.patch.object(os, "write", side_effect=failing_write),
                ),
            )

            self.assertEqual(code, 3)
            self.assertEqual(stderr, "inventory: output must be a new regular file\n")
            self.assertTrue(os.path.lexists(output))
            self.assertEqual(output.read_bytes(), b"")
            if os.name != "nt":
                self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o600)

    @unittest.skipIf(os.name == "nt", "replacing an open inode is a Unix contract")
    def test_replacement_inode_is_preserved_after_owned_write_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = root / "fixture"
            output = root / "report.json"
            self.make_fixture(fixture)
            original_open = os.open
            original_write = os.write
            original_close = os.close
            output_descriptors: set[int] = set()

            def tracking_open(path, flags, mode=0o777):
                descriptor = original_open(path, flags, mode)
                if os.fspath(path) == os.fspath(output) and flags & os.O_EXCL:
                    output_descriptors.add(descriptor)
                return descriptor

            def replacing_write(descriptor, data):
                if descriptor in output_descriptors:
                    os.unlink(output)
                    replacement = original_open(output, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                    original_write(replacement, b"replacement\n")
                    original_close(replacement)
                    raise OSError("simulated replacement race")
                return original_write(descriptor, data)

            code, stderr = self.run_embedded(
                fixture,
                output,
                patches=(
                    mock.patch.object(os, "open", side_effect=tracking_open),
                    mock.patch.object(os, "write", side_effect=replacing_write),
                ),
            )

            self.assertEqual(code, 3)
            self.assertEqual(stderr, "inventory: output must be a new regular file\n")
            self.assertEqual(output.read_bytes(), b"replacement\n")


@unittest.skipIf(BASH is None, "usable GNU Bash is required for argv parsing tests")
class InventoryArgumentContractTest(unittest.TestCase):
    def test_invalid_argument_forms_fail_before_io(self) -> None:
        cases = (
            (),
            ("--unknown",),
            ("positional",),
            ("--fixture-root",),
            ("--output",),
            ("--fixture-root", "fixture", "--fixture-root", "again", "--output", "report"),
            ("--fixture-root", "fixture", "--output", "report", "--output", "again"),
            ("--fixture-root", "fixture", "--output"),
            ("--fixture-root", "fixture", "--output", "report", "extra"),
        )
        for arguments in cases:
            with self.subTest(arguments=arguments), tempfile.TemporaryDirectory() as temporary:
                result = subprocess.run(
                    [BASH, str(SCRIPT), *arguments],
                    cwd=temporary,
                    text=True,
                    capture_output=True,
                    timeout=10,
                    check=False,
                )
                self.assertEqual(result.returncode, 3)
                self.assertEqual(result.stdout, "")
                self.assertEqual(
                    result.stderr,
                    "inventory: explicit fixture root and output are required\n",
                )
                self.assertEqual(list(Path(temporary).iterdir()), [])


if __name__ == "__main__":
    unittest.main()
