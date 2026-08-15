import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
BUNDLE = ROOT / "third_party" / "wdtt"
SERIES = BUNDLE / "series"
MANIFEST = BUNDLE / "SHA256SUMS"
WORKFLOW = ROOT / ".github" / "workflows" / "wdtt-bin.yml"
PIN_PATTERN = re.compile(r"^[0-9a-f]{40}$")
DIGEST_PATTERN = re.compile(r"^([0-9a-f]{64})  ([^\\]+)$")
EXPECTED_RED_SERIES = (
    "patches/0001-provider-bridge-tests.patch",
    "patches/0002-provider-bridge-runtime.patch",
)
EXPECTED_MANIFEST_PATHS = {
    "certs/cacert.pem",
    "patches/0001-provider-bridge-tests.patch",
    "patches/0002-provider-bridge-runtime.patch",
    "series",
}
FORBIDDEN_TLS_BYPASSES = (
    "InsecureSkipVerify",
    "VKTURN_INSECURE_TLS",
    "handler.proceed()",
)


def read_properties() -> dict[str, str]:
    properties: dict[str, str] = {}
    for raw_line in (ROOT / "version.properties").read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        properties[key.strip()] = value.strip()
    return properties


def read_series() -> tuple[str, ...]:
    entries = tuple(
        line.strip()
        for line in SERIES.read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    )
    for entry in entries:
        path = PurePosixPath(entry)
        if path.is_absolute() or ".." in path.parts or path.suffix != ".patch":
            raise AssertionError(f"unsafe patch path: {entry}")
    return entries


def read_manifest() -> dict[str, str]:
    result: dict[str, str] = {}
    for line in MANIFEST.read_text(encoding="utf-8").splitlines():
        match = DIGEST_PATTERN.fullmatch(line)
        if match is None:
            raise AssertionError(f"invalid SHA256SUMS line: {line!r}")
        digest, relative = match.groups()
        if relative in result:
            raise AssertionError(f"duplicate SHA256SUMS entry: {relative}")
        path = PurePosixPath(relative)
        if path.is_absolute() or ".." in path.parts:
            raise AssertionError(f"unsafe SHA256SUMS path: {relative}")
        result[relative] = digest
    return result


def reviewed_sha256(path: Path) -> str:
    canonical = path.read_bytes().replace(b"\r\n", b"\n")
    return hashlib.sha256(canonical).hexdigest()


class WdttPatchsetTest(unittest.TestCase):
    def test_exact_pin_and_ordered_unique_series(self) -> None:
        pin = read_properties().get("WDTT_REF", "")
        self.assertRegex(pin, PIN_PATTERN)
        entries = read_series()
        self.assertEqual(entries, EXPECTED_RED_SERIES)
        self.assertEqual(len(entries), len(set(entries)))

    def test_manifest_matches_every_reviewed_input(self) -> None:
        manifest = read_manifest()
        self.assertEqual(set(manifest), EXPECTED_MANIFEST_PATHS)
        for relative, expected in manifest.items():
            path = BUNDLE / PurePosixPath(relative)
            self.assertTrue(path.is_file(), relative)
            actual = reviewed_sha256(path)
            self.assertEqual(actual, expected, relative)

    def test_patchset_and_workflow_forbid_tls_bypasses(self) -> None:
        reviewed_text = WORKFLOW.read_text(encoding="utf-8")
        for entry in read_series():
            reviewed_text += (BUNDLE / PurePosixPath(entry)).read_text(encoding="utf-8")
        for forbidden in FORBIDDEN_TLS_BYPASSES:
            self.assertNotIn(forbidden, reviewed_text)

    def test_exact_base_accepts_each_patch_once(self) -> None:
        source_value = os.environ.get("WDTT_UPSTREAM_DIR")
        self.assertIsNotNone(source_value, "WDTT_UPSTREAM_DIR is required")
        source = Path(source_value).resolve()
        pin = read_properties()["WDTT_REF"]
        head = subprocess.run(
            ["git", "-C", str(source), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
        self.assertEqual(head, pin)

        with tempfile.TemporaryDirectory(prefix="wdtt-patchset-") as temporary:
            temporary_path = Path(temporary)
            archive = temporary_path / "upstream.tar"
            checkout = temporary_path / "checkout"
            checkout.mkdir()
            subprocess.run(
                [
                    "git",
                    "-C",
                    str(source),
                    "archive",
                    "--format=tar",
                    f"--output={archive}",
                    pin,
                ],
                check=True,
            )
            subprocess.run(
                ["tar", "-xf", str(archive), "-C", str(checkout)],
                check=True,
            )
            subprocess.run(["git", "-C", str(checkout), "init", "--quiet"], check=True)
            for entry in read_series():
                patch = (BUNDLE / PurePosixPath(entry)).resolve()
                reviewed_patch = temporary_path / PurePosixPath(entry).name
                reviewed_patch.write_bytes(patch.read_bytes().replace(b"\r\n", b"\n"))
                subprocess.run(
                    ["git", "-C", str(checkout), "apply", "--check", str(reviewed_patch)],
                    check=True,
                )
                subprocess.run(
                    ["git", "-C", str(checkout), "apply", str(reviewed_patch)],
                    check=True,
                )
                duplicate = subprocess.run(
                    ["git", "-C", str(checkout), "apply", "--check", str(reviewed_patch)],
                    capture_output=True,
                    text=True,
                )
                self.assertNotEqual(duplicate.returncode, 0, entry)


if __name__ == "__main__":
    unittest.main()
