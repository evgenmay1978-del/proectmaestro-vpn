from __future__ import annotations

import copy
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from ops.ha import build_manifest as manifest_module
from ops.ha.build_manifest import ManifestError, build_manifest, verify_manifest


REPOSITORY = "evgenmay1978-del/proectmaestro-vpn"
REF = "refs/heads/codex/yandex-cdn-whitelist-task3-sync"
FULL_SHA = "1" * 40
OTHER_SHA = "2" * 40
GO_VERSION = "go1.25.0"
SCHEMA = "maestro-ha-build-manifest-v1"
MAX_ARTIFACT_BYTES = 268435456
ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "ops" / "ha" / "build_manifest.py"


def canonical(value: object) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")


def write_amd64_elf(
    path: Path,
    *,
    elf_class: int = 2,
    endian: int = 1,
    elf_version: int = 1,
    machine: int = 0x3E,
    executable: bool = True,
) -> bytes:
    header = bytearray(64)
    header[:4] = b"\x7fELF"
    header[4] = elf_class
    header[5] = endian
    header[6] = elf_version
    header[16:18] = (2).to_bytes(2, "little")
    header[18:20] = machine.to_bytes(2, "little")
    header[20:24] = (1).to_bytes(4, "little")
    header[32:40] = (64).to_bytes(8, "little")
    header[52:54] = (64).to_bytes(2, "little")
    header[54:56] = (56).to_bytes(2, "little")
    header[56:58] = (1).to_bytes(2, "little")

    segment = b"maestro-panel-synthetic-fixture"
    program = bytearray(56)
    program[0:4] = (1).to_bytes(4, "little")
    program[4:8] = (5).to_bytes(4, "little")
    program[8:16] = (120).to_bytes(8, "little")
    program[16:24] = (0x400078).to_bytes(8, "little")
    program[24:32] = (0x400078).to_bytes(8, "little")
    program[32:40] = len(segment).to_bytes(8, "little")
    program[40:48] = len(segment).to_bytes(8, "little")
    program[48:56] = (0x1000).to_bytes(8, "little")
    payload = bytes(header) + bytes(program) + segment
    path.write_bytes(payload)
    path.chmod(0o755 if executable else 0o600)
    return payload


class BuildManifestTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.base = Path(self.temporary.name)
        self.root = self.base / "artifact"
        self.root.mkdir(mode=0o700)
        self.panel = self.root / "maestro-panel"
        self.manifest_path = self.root / "manifest.json"

    def tearDown(self) -> None:
        self.temporary.cleanup()

    @staticmethod
    def _identity(**overrides: object) -> dict[str, object]:
        values: dict[str, object] = {
            "repository": REPOSITORY,
            "ref": REF,
            "commit_sha": FULL_SHA,
            "workflow_run_id": 123,
            "workflow_run_attempt": 2,
            "go_version": GO_VERSION,
        }
        values.update(overrides)
        return values

    @staticmethod
    def _expected(**overrides: object) -> dict[str, object]:
        values: dict[str, object] = {
            "expected_repository": REPOSITORY,
            "expected_ref": REF,
            "expected_commit_sha": FULL_SHA,
            "expected_workflow_run_id": 123,
            "expected_workflow_run_attempt": 2,
        }
        values.update(overrides)
        return values

    def _build(self, **overrides: object) -> dict[str, object]:
        return build_manifest(self.root, **self._identity(**overrides))

    def _write_manifest(self, manifest: object, *, raw: bytes | None = None) -> None:
        self.manifest_path.write_bytes(canonical(manifest) if raw is None else raw)
        self.manifest_path.chmod(0o600)

    def _bundle(self) -> dict[str, object]:
        write_amd64_elf(self.panel)
        manifest = self._build()
        self._write_manifest(manifest)
        return manifest

    def _verify(self, **overrides: object) -> dict[str, object]:
        return verify_manifest(
            self.root,
            self.manifest_path,
            **self._expected(**overrides),
        )

    @staticmethod
    def _run_cli(arguments: list[str], *, timeout: int = 8) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT), *arguments],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=timeout,
        )

    def _create_arguments(
        self,
        *,
        output: Path | None = None,
        repository: str = REPOSITORY,
    ) -> list[str]:
        return [
            "create",
            "--artifact-root",
            str(self.root),
            "--output",
            str(output or self.manifest_path),
            "--repository",
            repository,
            "--ref",
            REF,
            "--commit-sha",
            FULL_SHA,
            "--workflow-run-id",
            "123",
            "--workflow-run-attempt",
            "2",
            "--go-version",
            GO_VERSION,
        ]

    def _verify_arguments(self) -> list[str]:
        return [
            "verify",
            "--artifact-root",
            str(self.root),
            "--manifest",
            str(self.manifest_path),
            "--expected-repository",
            REPOSITORY,
            "--expected-ref",
            REF,
            "--expected-commit-sha",
            FULL_SHA,
            "--expected-workflow-run-id",
            "123",
            "--expected-workflow-run-attempt",
            "2",
        ]

    def test_round_trip_binds_literal_digest_size_identity_and_no_go_status(self) -> None:
        payload = write_amd64_elf(self.panel)
        manifest = self._build()
        digest = hashlib.sha256(payload).hexdigest()

        self.assertEqual(
            set(manifest),
            {
                "artifacts",
                "commit_sha",
                "deployment_authorized",
                "go_version",
                "ref",
                "release_readiness",
                "repository",
                "schema",
                "workflow_run_attempt",
                "workflow_run_id",
            },
        )
        self.assertEqual(
            manifest["artifacts"],
            [
                {
                    "arch": "amd64",
                    "name": "maestro-panel",
                    "os": "linux",
                    "path": "maestro-panel",
                    "sha256": digest,
                    "size_bytes": len(payload),
                }
            ],
        )
        self.assertEqual(manifest["commit_sha"], FULL_SHA)
        self.assertEqual(manifest["go_version"], GO_VERSION)
        self.assertEqual(manifest["release_readiness"], "NO_GO")
        self.assertIs(manifest["deployment_authorized"], False)
        self.assertNotIn(str(self.base), canonical(manifest).decode("utf-8"))

        self._write_manifest(manifest)
        self.assertEqual(
            self._verify(),
            {
                "artifact_sha256": digest,
                "artifact_size_bytes": len(payload),
                "deployment_authorized": False,
                "release_readiness": "NO_GO",
                "schema": SCHEMA,
            },
        )

    def test_accepts_safe_branch_tag_and_pull_request_refs(self) -> None:
        write_amd64_elf(self.panel)
        for safe_ref in (
            REF,
            "refs/tags/backend-v1.2.3",
            "refs/pull/123/merge",
            "refs/pull/123/head",
        ):
            with self.subTest(ref=safe_ref):
                self.assertEqual(self._build(ref=safe_ref)["ref"], safe_ref)

    def test_rejects_identity_values_that_break_exact_build_provenance(self) -> None:
        write_amd64_elf(self.panel)
        invalid = (
            ("short commit", {"commit_sha": "1" * 39}),
            ("uppercase commit", {"commit_sha": "A" * 40}),
            ("nonhex commit", {"commit_sha": "g" * 40}),
            ("repository traversal", {"repository": "../repo"}),
            ("repository URL", {"repository": "https://example.invalid/repo"}),
            ("repository control byte", {"repository": "owner/repo\nother"}),
            ("ref traversal", {"ref": "refs/heads/../main"}),
            ("ref control byte", {"ref": "refs/heads/main\nother"}),
            ("unsupported Go line", {"go_version": "go1.24.9"}),
            ("Go suffix injection", {"go_version": "go1.25.0 linux/amd64"}),
            ("zero run", {"workflow_run_id": 0}),
            ("boolean run", {"workflow_run_id": True}),
            ("oversize run", {"workflow_run_id": 2**63}),
            ("zero attempt", {"workflow_run_attempt": 0}),
            ("boolean attempt", {"workflow_run_attempt": False}),
        )
        for label, overrides in invalid:
            with self.subTest(label=label):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
                    self._build(**overrides)

    def test_accepts_only_exact_go_1_25_0_and_positive_integer_ids(self) -> None:
        write_amd64_elf(self.panel)
        manifest = self._build(workflow_run_id=2**63 - 1, workflow_run_attempt=1)
        self.assertEqual(manifest["go_version"], GO_VERSION)
        self.assertEqual(manifest["workflow_run_id"], 2**63 - 1)

        for value in ("go1.25.1", "go1.25.12", "go1.25", "go1.25.0+metadata"):
            with self.subTest(go_version=value):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
                    self._build(go_version=value)


    def test_create_rejects_missing_or_extra_artifact_member(self) -> None:
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-members$"):
            self._build()
        write_amd64_elf(self.panel)
        (self.root / "unexpected.env").write_text("synthetic", encoding="utf-8")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-members$"):
            self._build()

    def test_create_rejects_artifact_root_that_is_not_a_real_directory(self) -> None:
        not_directory = self.base / "not-directory"
        not_directory.write_bytes(b"synthetic")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
            build_manifest(not_directory, **self._identity())

    @unittest.skipUnless(os.name == "posix", "private mode contract requires POSIX")
    def test_create_rejects_nonprivate_artifact_root(self) -> None:
        write_amd64_elf(self.panel)
        for mode in (0o750, 0o707, 0o1700):
            with self.subTest(mode=oct(mode)):
                self.root.chmod(mode)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
                    self._build()
        self.root.chmod(0o700)

    def test_create_rejects_symlinked_artifact_root(self) -> None:
        real_root = self.base / "real-root"
        real_root.mkdir()
        write_amd64_elf(real_root / "maestro-panel")
        linked_root = self.base / "linked-root"
        try:
            linked_root.symlink_to(real_root, target_is_directory=True)
        except OSError as error:
            self.skipTest(f"symlink unavailable: {error.__class__.__name__}")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
            build_manifest(linked_root, **self._identity())

    def test_create_rejects_symlink_hardlink_and_directory_sources(self) -> None:
        outside = self.base / "outside-panel"
        write_amd64_elf(outside)

        try:
            self.panel.symlink_to(outside)
        except OSError as error:
            self.skipTest(f"symlink unavailable: {error.__class__.__name__}")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()
        self.panel.unlink()

        os.link(outside, self.panel)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()
        self.panel.unlink()

        self.panel.mkdir()
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()

    @unittest.skipUnless(os.name == "posix", "Unix mode bits are a source-build contract")
    def test_create_rejects_source_binary_without_executable_mode(self) -> None:
        write_amd64_elf(self.panel, executable=False)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()

    def test_create_rejects_zero_and_oversize_binary_before_hashing(self) -> None:
        self.panel.write_bytes(b"")
        self.panel.chmod(0o755)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()

        write_amd64_elf(self.panel)
        with self.panel.open("r+b") as handle:
            handle.truncate(MAX_ARTIFACT_BYTES + 1)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()

    def test_create_rejects_wrong_elf_magic_class_endian_version_machine_or_length(self) -> None:
        cases = (
            ("magic", {"elf_class": 2}, b"NOPE"),
            ("class", {"elf_class": 1}, None),
            ("endian", {"endian": 2}, None),
            ("version", {"elf_version": 0}, None),
            ("machine", {"machine": 0xB7}, None),
        )
        for label, parameters, replacement_magic in cases:
            with self.subTest(label=label):
                payload = bytearray(write_amd64_elf(self.panel, **parameters))
                if replacement_magic is not None:
                    payload[:4] = replacement_magic
                    self.panel.write_bytes(payload)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                    self._build()
        self.panel.write_bytes(b"\x7fELF" + b"\x00" * 20)
        self.panel.chmod(0o755)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._build()

    def test_create_rejects_invalid_elf_header_and_program_table_contract(self) -> None:
        mutations = (
            ("object type", 16, 2, 1),
            ("object version", 20, 4, 0),
            ("program table overlaps header", 32, 8, 0),
            ("header size", 52, 2, 63),
            ("program entry size", 54, 2, 55),
            ("program count zero", 56, 2, 0),
            ("program table beyond eof", 56, 2, 2),
            ("missing load segment", 64, 4, 2),
            ("load offset beyond eof", 72, 8, 2**32),
            ("empty load segment", 96, 8, 0),
            ("load size beyond eof", 96, 8, 2**32),
            ("load size exceeds memory", 104, 8, 0),
        )
        for label, offset, width, value in mutations:
            with self.subTest(label=label):
                payload = bytearray(write_amd64_elf(self.panel))
                payload[offset : offset + width] = value.to_bytes(width, "little")
                self.panel.write_bytes(payload)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                    self._build()

    @unittest.skipUnless(hasattr(os, "mkfifo") and os.name == "posix", "FIFO unavailable")
    def test_fifo_source_fails_closed_without_blocking(self) -> None:
        os.mkfifo(self.panel, mode=0o700)
        completed = self._run_cli(self._create_arguments(), timeout=3)
        self.assertEqual(completed.returncode, 1)
        self.assertEqual(completed.stdout, "")
        self.assertEqual(completed.stderr, "build-manifest:invalid-artifact\n")

    @unittest.skipUnless(hasattr(socket, "AF_UNIX") and os.name == "posix", "Unix sockets unavailable")
    def test_create_rejects_unix_socket_source(self) -> None:
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            listener.bind(str(self.panel))
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                self._build()
        finally:
            listener.close()

    def test_verify_accepts_transport_stripped_executable_bit(self) -> None:
        self._bundle()
        self.panel.chmod(0o600)
        self.assertEqual(self._verify()["release_readiness"], "NO_GO")

    def test_verify_rejects_missing_or_extra_bundle_member(self) -> None:
        self._bundle()
        self.manifest_path.unlink()
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-members$"):
            self._verify()

        self._bundle()
        (self.root / "customer-export.json").write_text("synthetic", encoding="utf-8")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-members$"):
            self._verify()

    def test_verify_rejects_symlinked_or_hardlinked_binary(self) -> None:
        self._bundle()
        outside = self.base / "transported-panel"
        self.panel.replace(outside)
        try:
            self.panel.symlink_to(outside)
        except OSError as error:
            self.skipTest(f"symlink unavailable: {error.__class__.__name__}")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._verify()
        self.panel.unlink()
        os.link(outside, self.panel)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
            self._verify()

    def test_verify_rejects_symlinked_or_hardlinked_manifest(self) -> None:
        self._bundle()
        outside = self.base / "transported-manifest.json"
        self.manifest_path.replace(outside)
        try:
            self.manifest_path.symlink_to(outside)
        except OSError as error:
            self.skipTest(f"symlink unavailable: {error.__class__.__name__}")
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
            self._verify()
        self.manifest_path.unlink()
        os.link(outside, self.manifest_path)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
            self._verify()

    def test_verify_rejects_manifest_path_outside_artifact_root(self) -> None:
        self._bundle()
        outside = self.base / "outside-manifest.json"
        outside.write_bytes(self.manifest_path.read_bytes())
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
            verify_manifest(self.root, outside, **self._expected())

    def test_verify_rejects_unknown_missing_or_wrongly_typed_fields(self) -> None:
        original = self._bundle()
        mutations = []

        extra = copy.deepcopy(original)
        extra["unexpected"] = True
        mutations.append(("extra top-level", extra))

        missing = copy.deepcopy(original)
        del missing["schema"]
        mutations.append(("missing top-level", missing))

        extra_artifact = copy.deepcopy(original)
        extra_artifact["artifacts"][0]["unexpected"] = True
        mutations.append(("extra artifact", extra_artifact))

        missing_artifact = copy.deepcopy(original)
        del missing_artifact["artifacts"][0]["arch"]
        mutations.append(("missing artifact", missing_artifact))

        boolean_size = copy.deepcopy(original)
        boolean_size["artifacts"][0]["size_bytes"] = True
        mutations.append(("boolean size", boolean_size))

        boolean_run = copy.deepcopy(original)
        boolean_run["workflow_run_id"] = True
        mutations.append(("boolean run", boolean_run))

        for label, mutated in mutations:
            with self.subTest(label=label):
                self._write_manifest(mutated)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
                    self._verify()

    def test_verify_rejects_duplicate_keys_at_top_level_and_nested_level(self) -> None:
        manifest = self._bundle()
        encoded = canonical(manifest)
        duplicate_top = encoded.replace(
            b'"schema":"maestro-ha-build-manifest-v1"',
            b'"schema":"maestro-ha-build-manifest-v1","schema":"maestro-ha-build-manifest-v1"',
            1,
        )
        self._write_manifest(manifest, raw=duplicate_top)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
            self._verify()

        duplicate_nested = encoded.replace(
            b'"arch":"amd64"',
            b'"arch":"amd64","arch":"amd64"',
            1,
        )
        self._write_manifest(manifest, raw=duplicate_nested)
        with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
            self._verify()

    def test_verify_rejects_noncanonical_invalid_utf8_or_oversize_manifest(self) -> None:
        manifest = self._bundle()
        variants = (
            json.dumps(manifest, indent=2).encode("utf-8") + b"\n",
            canonical(manifest).replace(b"\n", b"\r\n"),
            b" " + canonical(manifest),
            canonical(manifest).rstrip(b"\n"),
            b"\xff\xfe\n",
            b"{" + b" " * 65536 + b"}\n",
        )
        for index, raw in enumerate(variants):
            with self.subTest(index=index):
                self._write_manifest(manifest, raw=raw)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
                    self._verify()

    def test_verify_rejects_unsafe_artifact_path_and_static_contract_drift(self) -> None:
        original = self._bundle()
        mutations = (
            ("traversal", "path", "../maestro-panel"),
            ("absolute", "path", "/tmp/maestro-panel"),
            ("subdirectory", "path", "bin/maestro-panel"),
            ("wrong name", "name", "panel"),
            ("wrong os", "os", "windows"),
            ("wrong arch", "arch", "arm64"),
        )
        for label, key, value in mutations:
            with self.subTest(label=label):
                mutated = copy.deepcopy(original)
                mutated["artifacts"][0][key] = value
                self._write_manifest(mutated)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
                    self._verify()

    def test_verify_rejects_digest_size_and_no_go_contract_drift(self) -> None:
        original = self._bundle()
        mutations = []
        wrong_digest = copy.deepcopy(original)
        wrong_digest["artifacts"][0]["sha256"] = "f" * 64
        mutations.append(wrong_digest)
        wrong_size = copy.deepcopy(original)
        wrong_size["artifacts"][0]["size_bytes"] += 1
        mutations.append(wrong_size)
        wrong_schema = copy.deepcopy(original)
        wrong_schema["schema"] = "maestro-ha-build-manifest-v2"
        mutations.append(wrong_schema)
        wrong_go = copy.deepcopy(original)
        wrong_go["go_version"] = "go1.25.12"
        mutations.append(wrong_go)
        go_status = copy.deepcopy(original)
        go_status["release_readiness"] = "GO"
        mutations.append(go_status)
        authorized = copy.deepcopy(original)
        authorized["deployment_authorized"] = True
        mutations.append(authorized)

        for mutated in mutations:
            with self.subTest(mutated=mutated):
                self._write_manifest(mutated)
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
                    self._verify()

    def test_verify_rejects_expected_repository_ref_commit_or_run_mismatch(self) -> None:
        self._bundle()
        mismatches = (
            {"expected_repository": "other-owner/other-repo"},
            {"expected_ref": "refs/heads/other-safe-branch"},
            {"expected_commit_sha": OTHER_SHA},
            {"expected_workflow_run_id": 124},
            {"expected_workflow_run_attempt": 3},
        )
        for mismatch in mismatches:
            with self.subTest(mismatch=mismatch):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:identity-mismatch$"):
                    self._verify(**mismatch)

    @unittest.skipUnless(
        os.name == "posix" and os.open in getattr(os, "supports_dir_fd", set()),
        "descriptor race proof requires POSIX dir_fd",
    )
    def test_verify_rejects_artifact_path_replaced_after_descriptor_open(self) -> None:
        self._bundle()
        replacement = self.base / "replacement-panel"
        write_amd64_elf(replacement)
        replacement.write_bytes(replacement.read_bytes() + b"changed")
        original_open = os.open
        replaced = False

        def open_then_replace(path: object, flags: int, mode: int = 0o777, *, dir_fd: int | None = None) -> int:
            nonlocal replaced
            options = {} if dir_fd is None else {"dir_fd": dir_fd}
            descriptor = original_open(path, flags, mode, **options)
            if os.path.basename(os.fsdecode(path)) == "maestro-panel" and not replaced:
                replaced = True
                os.replace(replacement, self.panel)
            return descriptor

        with mock.patch.object(manifest_module.os, "open", side_effect=open_then_replace):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                self._verify()
        self.assertTrue(replaced)

    @unittest.skipUnless(
        os.name == "posix" and os.open in getattr(os, "supports_dir_fd", set()),
        "descriptor race proof requires POSIX dir_fd",
    )
    def test_verify_rejects_manifest_path_replaced_after_descriptor_open(self) -> None:
        manifest = self._bundle()
        replacement = self.base / "replacement-manifest.json"
        mutated = copy.deepcopy(manifest)
        mutated["deployment_authorized"] = True
        replacement.write_bytes(canonical(mutated))
        original_open = os.open
        replaced = False

        def open_then_replace(path: object, flags: int, mode: int = 0o777, *, dir_fd: int | None = None) -> int:
            nonlocal replaced
            options = {} if dir_fd is None else {"dir_fd": dir_fd}
            descriptor = original_open(path, flags, mode, **options)
            if os.path.basename(os.fsdecode(path)) == "manifest.json" and not replaced:
                replaced = True
                os.replace(replacement, self.manifest_path)
            return descriptor

        with mock.patch.object(manifest_module.os, "open", side_effect=open_then_replace):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
                self._verify()
        self.assertTrue(replaced)

    @unittest.skipUnless(
        os.name == "posix" and os.open in getattr(os, "supports_dir_fd", set()),
        "descriptor race proof requires POSIX dir_fd",
    )
    def test_verify_rejects_member_injected_after_artifact_open(self) -> None:
        self._bundle()
        original_open = os.open
        injected = False

        def open_then_inject(path: object, flags: int, mode: int = 0o777, *, dir_fd: int | None = None) -> int:
            nonlocal injected
            options = {} if dir_fd is None else {"dir_fd": dir_fd}
            descriptor = original_open(path, flags, mode, **options)
            if os.path.basename(os.fsdecode(path)) == "maestro-panel" and not injected:
                injected = True
                (self.root / "late-secret.env").write_text("synthetic", encoding="utf-8")
            return descriptor

        with mock.patch.object(manifest_module.os, "open", side_effect=open_then_inject):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-members$"):
                self._verify()
        self.assertTrue(injected)

    def test_verify_rejects_manifest_replaced_while_artifact_is_hashed(self) -> None:
        self._bundle()
        replacement = self.base / "late-manifest.json"
        replacement.write_bytes(self.manifest_path.read_bytes())
        replacement.chmod(0o600)
        original_hash = manifest_module._hash_artifact
        replaced = False

        def replace_then_hash(*args: object, **kwargs: object) -> object:
            nonlocal replaced
            os.replace(replacement, self.manifest_path)
            replaced = True
            return original_hash(*args, **kwargs)

        with mock.patch.object(manifest_module, "_hash_artifact", side_effect=replace_then_hash):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-manifest$"):
                self._verify()
        self.assertTrue(replaced)

    def test_verify_rejects_artifact_replaced_after_hash_before_final_success(self) -> None:
        self._bundle()
        replacement = self.base / "late-panel"
        write_amd64_elf(replacement)
        original_hash = manifest_module._hash_artifact
        replaced = False

        def hash_then_replace(*args: object, **kwargs: object) -> object:
            nonlocal replaced
            result = original_hash(*args, **kwargs)
            os.replace(replacement, self.panel)
            replaced = True
            return result

        with mock.patch.object(manifest_module, "_hash_artifact", side_effect=hash_then_replace):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                self._verify()
        self.assertTrue(replaced)

    def test_cli_create_and_verify_are_canonical_bounded_and_redacted(self) -> None:
        payload = write_amd64_elf(self.panel)
        created = self._run_cli(self._create_arguments())
        self.assertEqual(created.returncode, 0, created.stderr)
        self.assertEqual(created.stdout, "")
        self.assertEqual(created.stderr, "")
        parsed = json.loads(self.manifest_path.read_bytes())
        self.assertEqual(self.manifest_path.read_bytes(), canonical(parsed))
        if os.name == "posix":
            self.assertEqual(stat.S_IMODE(self.manifest_path.stat().st_mode), 0o600)

        verified = self._run_cli(self._verify_arguments())
        self.assertEqual(verified.returncode, 0, verified.stderr)
        self.assertEqual(verified.stderr, "")
        result = {
            "artifact_sha256": hashlib.sha256(payload).hexdigest(),
            "artifact_size_bytes": len(payload),
            "deployment_authorized": False,
            "release_readiness": "NO_GO",
            "schema": SCHEMA,
        }
        self.assertEqual(verified.stdout.encode("utf-8"), canonical(result))
        self.assertLessEqual(len(verified.stdout.encode("utf-8")), 512)
        self.assertNotIn(str(self.base), verified.stdout)
        self.assertNotIn(REPOSITORY, verified.stdout)
        self.assertNotIn(REF, verified.stdout)

    def test_cli_create_refuses_existing_manifest_without_overwrite_or_temp_debris(self) -> None:
        write_amd64_elf(self.panel)
        foreign = b"foreign-manifest-canary"
        self.manifest_path.write_bytes(foreign)
        completed = self._run_cli(self._create_arguments())
        self.assertEqual(completed.returncode, 1)
        self.assertEqual(completed.stdout, "")
        self.assertEqual(completed.stderr, "build-manifest:output-exists\n")
        self.assertEqual(self.manifest_path.read_bytes(), foreign)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel", "manifest.json"})

    def test_cli_create_rejects_output_outside_root_without_writing(self) -> None:
        write_amd64_elf(self.panel)
        outside = self.base / "outside.json"
        completed = self._run_cli(self._create_arguments(output=outside))
        self.assertEqual(completed.returncode, 1)
        self.assertEqual(completed.stdout, "")
        self.assertEqual(completed.stderr, "build-manifest:invalid-input\n")
        self.assertFalse(outside.exists())

    def test_cli_failure_never_echoes_user_path_or_identity_canary(self) -> None:
        write_amd64_elf(self.panel)
        canary = "https://secret-canary.invalid/private/repository"
        completed = self._run_cli(self._create_arguments(repository=canary))
        combined = completed.stdout + completed.stderr
        self.assertEqual(completed.returncode, 1)
        self.assertEqual(completed.stdout, "")
        self.assertEqual(completed.stderr, "build-manifest:invalid-input\n")
        self.assertNotIn(canary, combined)
        self.assertNotIn(str(self.base), combined)

    def test_publish_rolls_back_owned_manifest_after_final_member_check_failure(self) -> None:
        write_amd64_elf(self.panel)
        encoded = canonical(self._build())
        original_check = manifest_module._check_members
        failed = False

        def fail_final_check(root: object, expected: set[str]) -> None:
            nonlocal failed
            if expected == {"maestro-panel", "manifest.json"} and not failed:
                failed = True
                raise ManifestError("build-manifest:invalid-members")
            original_check(root, expected)

        with mock.patch.object(manifest_module, "_check_members", side_effect=fail_final_check):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-members$"):
                manifest_module._publish_manifest(self.root, self.manifest_path, encoded)
        self.assertTrue(failed)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel"})

    def test_publish_rolls_back_owned_manifest_when_link_reports_ambiguous_failure(self) -> None:
        write_amd64_elf(self.panel)
        encoded = canonical(self._build())
        original_link = os.link
        linked = False

        def link_then_raise(source: object, destination: object, *args: object, **kwargs: object) -> None:
            nonlocal linked
            original_link(source, destination, *args, **kwargs)
            linked = True
            raise OSError("synthetic ambiguous link result")

        link_mock = mock.Mock(side_effect=link_then_raise)
        supported = set(getattr(os, "supports_dir_fd", set())) | {link_mock}
        with mock.patch.object(manifest_module.os, "link", link_mock):
            with mock.patch.object(manifest_module.os, "supports_dir_fd", supported):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:output-failed$"):
                    manifest_module._publish_manifest(self.root, self.manifest_path, encoded)
        self.assertTrue(linked)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel"})

    def test_publish_ambiguous_link_failure_preserves_foreign_replacement(self) -> None:
        write_amd64_elf(self.panel)
        encoded = canonical(self._build())
        original_link = os.link
        foreign = b"foreign-manifest-after-link\n"

        def replace_then_raise(source: object, destination: object, *args: object, **kwargs: object) -> None:
            original_link(source, destination, *args, **kwargs)
            self.manifest_path.unlink()
            self.manifest_path.write_bytes(foreign)
            raise OSError("synthetic ambiguous link result")

        link_mock = mock.Mock(side_effect=replace_then_raise)
        supported = set(getattr(os, "supports_dir_fd", set())) | {link_mock}
        with mock.patch.object(manifest_module.os, "link", link_mock):
            with mock.patch.object(manifest_module.os, "supports_dir_fd", supported):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:output-failed$"):
                    manifest_module._publish_manifest(self.root, self.manifest_path, encoded)
        self.assertEqual(self.manifest_path.read_bytes(), foreign)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel", "manifest.json"})

    def test_publish_link_race_preserves_foreign_manifest_and_cleans_temp(self) -> None:
        write_amd64_elf(self.panel)
        encoded = canonical(self._build())
        foreign = b"foreign-manifest-race\n"

        def competitor_wins(_source: object, destination: object, *args: object, **kwargs: object) -> None:
            self.manifest_path.write_bytes(foreign)
            raise FileExistsError("synthetic competitor")

        link_mock = mock.Mock(side_effect=competitor_wins)
        supported = set(getattr(os, "supports_dir_fd", set())) | {link_mock}
        with mock.patch.object(manifest_module.os, "link", link_mock):
            with mock.patch.object(manifest_module.os, "supports_dir_fd", supported):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:output-exists$"):
                    manifest_module._publish_manifest(self.root, self.manifest_path, encoded)
        self.assertEqual(self.manifest_path.read_bytes(), foreign)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel", "manifest.json"})

    def test_cli_create_rolls_back_owned_manifest_after_post_publish_verify_failure(self) -> None:
        write_amd64_elf(self.panel)
        with tempfile.TemporaryFile() as error_output:
            fake_stderr = mock.Mock(buffer=error_output)
            with mock.patch.object(manifest_module.sys, "stderr", fake_stderr):
                with mock.patch.object(
                    manifest_module,
                    "verify_manifest",
                    side_effect=ManifestError("build-manifest:invalid-manifest"),
                ):
                    result = manifest_module.main(self._create_arguments())
            error_output.seek(0)
            self.assertEqual(error_output.read(), b"build-manifest:invalid-manifest\n")
        self.assertEqual(result, 1)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel"})

    def test_cli_create_rollback_preserves_foreign_manifest_replacement(self) -> None:
        write_amd64_elf(self.panel)
        foreign = b"foreign-post-publish-manifest\n"

        def replace_then_fail(*_args: object, **_kwargs: object) -> None:
            self.manifest_path.unlink()
            self.manifest_path.write_bytes(foreign)
            raise ManifestError("build-manifest:invalid-manifest")

        with tempfile.TemporaryFile() as error_output:
            fake_stderr = mock.Mock(buffer=error_output)
            with mock.patch.object(manifest_module.sys, "stderr", fake_stderr):
                with mock.patch.object(manifest_module, "verify_manifest", side_effect=replace_then_fail):
                    result = manifest_module.main(self._create_arguments())
        self.assertEqual(result, 1)
        self.assertEqual(self.manifest_path.read_bytes(), foreign)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel", "manifest.json"})

    def test_publish_rolls_back_owned_manifest_after_temp_unlink_failure(self) -> None:
        write_amd64_elf(self.panel)
        encoded = canonical(self._build())
        original_unlink = manifest_module._unlink_name
        failed = False

        def fail_temp_once(root: object, name: str) -> None:
            nonlocal failed
            if name.startswith(".manifest-") and not failed:
                failed = True
                raise OSError("synthetic temp unlink failure")
            original_unlink(root, name)

        with mock.patch.object(manifest_module, "_unlink_name", side_effect=fail_temp_once):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:output-failed$"):
                manifest_module._publish_manifest(self.root, self.manifest_path, encoded)
        self.assertTrue(failed)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel"})

    @unittest.skipUnless(os.name == "posix", "directory fsync contract requires POSIX")
    def test_publish_rolls_back_owned_manifest_after_directory_fsync_failure(self) -> None:
        write_amd64_elf(self.panel)
        encoded = canonical(self._build())
        original_fsync = os.fsync
        failed = False

        def fail_directory_once(descriptor: int) -> None:
            nonlocal failed
            if stat.S_ISDIR(os.fstat(descriptor).st_mode) and not failed:
                failed = True
                raise OSError("synthetic directory fsync failure")
            original_fsync(descriptor)

        with mock.patch.object(manifest_module.os, "fsync", side_effect=fail_directory_once):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:output-failed$"):
                manifest_module._publish_manifest(self.root, self.manifest_path, encoded)
        self.assertTrue(failed)
        self.assertEqual({entry.name for entry in self.root.iterdir()}, {"maestro-panel"})

    @unittest.skipUnless(
        os.name == "posix" and os.open in getattr(os, "supports_dir_fd", set()),
        "pinned-root replacement proof requires POSIX dir_fd",
    )
    def test_verify_rejects_artifact_root_replaced_during_verification(self) -> None:
        self._bundle()
        moved_root = self.base / "moved-artifact-root"
        original_hash = manifest_module._hash_artifact
        replaced = False

        def replace_root_then_hash(*args: object, **kwargs: object) -> object:
            nonlocal replaced
            self.root.rename(moved_root)
            self.root.mkdir(mode=0o700)
            replaced = True
            return original_hash(*args, **kwargs)

        with mock.patch.object(manifest_module, "_hash_artifact", side_effect=replace_root_then_hash):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
                self._verify()
        self.assertTrue(replaced)

    @unittest.skipUnless(os.name == "posix", "same-inode mutation proof requires POSIX")
    def test_verify_rejects_same_inode_artifact_mutation_during_hashing(self) -> None:
        self._bundle()
        original_validate = manifest_module._validate_elf
        mutated = False

        def validate_then_mutate(descriptor: int, size: int) -> None:
            nonlocal mutated
            original_validate(descriptor, size)
            with self.panel.open("r+b") as handle:
                handle.seek(-1, os.SEEK_END)
                current = handle.read(1)
                handle.seek(-1, os.SEEK_END)
                handle.write(b"X" if current != b"X" else b"Y")
                handle.flush()
                os.fsync(handle.fileno())
            mutated = True

        with mock.patch.object(manifest_module, "_validate_elf", side_effect=validate_then_mutate):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                self._verify()
        self.assertTrue(mutated)

    @unittest.skipUnless(os.name == "posix" and hasattr(os, "link"), "link drift proof requires POSIX")
    def test_verify_rejects_artifact_link_count_drift_during_hashing(self) -> None:
        self._bundle()
        extra_link = self.base / "artifact-hardlink"
        original_validate = manifest_module._validate_elf
        linked = False

        def validate_then_link(descriptor: int, size: int) -> None:
            nonlocal linked
            original_validate(descriptor, size)
            os.link(self.panel, extra_link)
            linked = True

        with mock.patch.object(manifest_module, "_validate_elf", side_effect=validate_then_link):
            with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-artifact$"):
                self._verify()
        self.assertTrue(linked)

    @unittest.skipUnless(os.name == "posix", "root mode drift proof requires POSIX")
    def test_verify_rejects_artifact_root_mode_drift(self) -> None:
        self._bundle()
        original_hash = manifest_module._hash_artifact
        changed = False

        def change_mode_then_hash(*args: object, **kwargs: object) -> object:
            nonlocal changed
            self.root.chmod(0o750)
            changed = True
            return original_hash(*args, **kwargs)

        try:
            with mock.patch.object(manifest_module, "_hash_artifact", side_effect=change_mode_then_hash):
                with self.assertRaisesRegex(ManifestError, "^build-manifest:invalid-input$"):
                    self._verify()
        finally:
            self.root.chmod(0o700)
        self.assertTrue(changed)


if __name__ == "__main__":
    unittest.main()
