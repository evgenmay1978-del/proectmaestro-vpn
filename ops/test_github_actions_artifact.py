from __future__ import annotations

import hashlib
import importlib.util
import io
import json
import subprocess
import tempfile
import unittest
import zipfile
from contextlib import redirect_stderr
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


MODULE_NAME = "github_actions_artifact"
MODULE_PATH = Path(__file__).with_name("github-actions-artifact.py")
if not MODULE_PATH.is_file():
    raise ModuleNotFoundError(f"No module named '{MODULE_NAME}'")
SPEC = importlib.util.spec_from_file_location(MODULE_NAME, MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"Cannot load {MODULE_PATH}")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


HEAD_SHA = "0123456789abcdef0123456789abcdef01234567"
REF = "codex/mobile-4d-deck"


class DispatchPolicyTest(unittest.TestCase):
    def test_only_staging_ref_and_declared_tasks_are_accepted(self) -> None:
        self.assertEqual(
            MODULE.artifact_name_for("android", REF, HEAD_SHA),
            "maestrovpn-tv-test-apk",
        )
        self.assertEqual(
            MODULE.artifact_name_for("mobile-eye-ring-assets", REF, HEAD_SHA),
            f"mobile-eye-ring-assets-{HEAD_SHA}",
        )
        self.assertEqual(
            MODULE.artifact_name_for("mobile-eye-runtime-assets", REF, HEAD_SHA),
            f"mobile-eye-runtime-assets-{HEAD_SHA}",
        )

        with self.assertRaisesRegex(MODULE.PolicyError, "task"):
            MODULE.artifact_name_for("release", REF, HEAD_SHA)
        with self.assertRaisesRegex(MODULE.PolicyError, "ref"):
            MODULE.artifact_name_for("android", "main", HEAD_SHA)
        with self.assertRaisesRegex(MODULE.PolicyError, "HEAD"):
            MODULE.artifact_name_for("android", REF, "abc")

    def test_api_policy_rejects_release_and_cross_repository_paths(self) -> None:
        allowed = (
            "/repos/evgenmay1978-del/proectmaestro-vpn/actions/"
            "workflows/android-test.yml/dispatches"
        )
        self.assertEqual(MODULE.validate_actions_api_path(allowed), allowed)

        for path in (
            "/repos/evgenmay1978-del/proectmaestro-vpn/releases",
            "/repos/other/repository/actions/runs",
            "/repos/evgenmay1978-del/proectmaestro-vpn/ota",
        ):
            with self.subTest(path=path):
                with self.assertRaises(MODULE.PolicyError):
                    MODULE.validate_actions_api_path(path)


class LocalRevisionTest(unittest.TestCase):
    def test_revision_is_clean_pushed_head_on_the_whitelisted_branch(self) -> None:
        outputs = {
            ("git", "rev-parse", "HEAD"): f"{HEAD_SHA}\n",
            ("git", "branch", "--show-current"): f"{REF}\n",
            ("git", "status", "--porcelain"): "",
            ("git", "ls-remote", "--heads", "origin", f"refs/heads/{REF}"): (
                f"{HEAD_SHA}\trefs/heads/{REF}\n"
            ),
        }

        def fake_run(command, **_kwargs):
            return SimpleNamespace(stdout=outputs[tuple(command)])

        revision = MODULE.read_local_revision(Path("repo"), run=fake_run)

        self.assertEqual(revision.ref, REF)
        self.assertEqual(revision.head_sha, HEAD_SHA)

    def test_revision_rejects_dirty_or_not_pushed_worktree(self) -> None:
        base = {
            ("git", "rev-parse", "HEAD"): f"{HEAD_SHA}\n",
            ("git", "branch", "--show-current"): f"{REF}\n",
            ("git", "status", "--porcelain"): " M local.py\n",
        }

        def dirty_run(command, **_kwargs):
            return SimpleNamespace(stdout=base[tuple(command)])

        with self.assertRaisesRegex(MODULE.PolicyError, "uncommitted"):
            MODULE.read_local_revision(Path("repo"), run=dirty_run)

        remote_sha = "f" * 40
        base[("git", "status", "--porcelain")] = ""
        base[("git", "ls-remote", "--heads", "origin", f"refs/heads/{REF}")] = (
            f"{remote_sha}\trefs/heads/{REF}\n"
        )
        with self.assertRaisesRegex(MODULE.PolicyError, "pushed"):
            MODULE.read_local_revision(Path("repo"), run=dirty_run)


class AuthenticationTest(unittest.TestCase):
    def test_environment_token_precedes_git_credential_fallback(self) -> None:
        def should_not_run(*_args, **_kwargs):
            raise AssertionError("git credential must not run")

        token = MODULE.read_github_token(
            {"GH_TOKEN": "gh-secret", "GITHUB_TOKEN": "github-secret"},
            run=should_not_run,
        )

        self.assertEqual(token, "gh-secret")

    def test_git_credential_fill_is_a_non_printing_fallback(self) -> None:
        def fake_run(command, **kwargs):
            self.assertEqual(command, ["git", "credential", "fill"])
            self.assertEqual(kwargs["input"], "protocol=https\nhost=github.com\n\n")
            return SimpleNamespace(
                stdout="protocol=https\nhost=github.com\nusername=owner\npassword=credential-secret\n"
            )

        token = MODULE.read_github_token({}, run=fake_run)

        self.assertEqual(token, "credential-secret")

    def test_sensitive_values_are_redacted_from_errors(self) -> None:
        rendered = MODULE.redact_sensitive(
            "request failed: Bearer token-secret; password=token-secret",
            ["token-secret"],
        )

        self.assertNotIn("token-secret", rendered)
        self.assertEqual(rendered.count("<REDACTED>"), 2)

    def test_cli_never_prints_unexpected_exception_details(self) -> None:
        stderr = io.StringIO()
        with mock.patch.object(
            MODULE,
            "run_command",
            side_effect=RuntimeError("credential-secret"),
        ), redirect_stderr(stderr):
            exit_code = MODULE.main(["--task", "android"])

        self.assertEqual(exit_code, 1)
        self.assertNotIn("credential-secret", stderr.getvalue())
        self.assertIn("details suppressed", stderr.getvalue())


class FakeResponse:
    def __init__(self, body: bytes = b"", status: int = 200) -> None:
        self._body = io.BytesIO(body)
        self.status = status

    def read(self, size: int = -1) -> bytes:
        return self._body.read(size)

    def __enter__(self):
        return self

    def __exit__(self, *_args) -> None:
        return None


class GitHubApiTest(unittest.TestCase):
    def test_dispatch_uses_only_the_fixed_workflow_and_input(self) -> None:
        requests = []

        def opener(request, timeout):
            requests.append((request, timeout))
            return FakeResponse(status=204)

        api = MODULE.GitHubActionsApi("token-secret", opener=opener)
        api.dispatch("android", REF)

        request, timeout = requests[0]
        self.assertEqual(request.method, "POST")
        self.assertEqual(timeout, MODULE.HTTP_TIMEOUT_SECONDS)
        self.assertEqual(
            request.full_url,
            "https://api.github.com/repos/evgenmay1978-del/proectmaestro-vpn/"
            "actions/workflows/android-test.yml/dispatches",
        )
        self.assertEqual(
            json.loads(request.data.decode("utf-8")),
            {"ref": REF, "inputs": {"task": "android"}},
        )

    def test_run_filter_requires_new_workflow_dispatch_exact_sha_and_branch(self) -> None:
        payload = {
            "workflow_runs": [
                {"id": 1, "event": "workflow_dispatch", "head_sha": HEAD_SHA, "head_branch": REF},
                {"id": 2, "event": "push", "head_sha": HEAD_SHA, "head_branch": REF},
                {"id": 3, "event": "workflow_dispatch", "head_sha": "f" * 40, "head_branch": REF},
                {"id": 4, "event": "workflow_dispatch", "head_sha": HEAD_SHA, "head_branch": "main"},
                {
                    "id": 5,
                    "event": "workflow_dispatch",
                    "head_sha": HEAD_SHA,
                    "head_branch": REF,
                    "status": "queued",
                    "conclusion": None,
                    "html_url": "https://github.com/example/actions/runs/5",
                },
            ]
        }

        run = MODULE.select_new_run(payload, HEAD_SHA, REF, previous_ids={1})

        self.assertEqual(run.run_id, 5)
        self.assertEqual(run.head_sha, HEAD_SHA)
        self.assertEqual(run.event, "workflow_dispatch")

    def test_artifact_selection_is_exact_and_unique(self) -> None:
        payload = {
            "artifacts": [
                {"id": 7, "name": "maestrovpn-tv-test-apk-old", "expired": False},
                {
                    "id": 8,
                    "name": "maestrovpn-tv-test-apk",
                    "expired": False,
                    "size_in_bytes": 123,
                },
            ]
        }

        artifact = MODULE.select_exact_artifact(payload, "maestrovpn-tv-test-apk")

        self.assertEqual(artifact.artifact_id, 8)
        self.assertEqual(artifact.name, "maestrovpn-tv-test-apk")
        with self.assertRaisesRegex(MODULE.GitHubError, "exact artifact"):
            MODULE.select_exact_artifact(payload, "maestrovpn-tv-test")

        payload["artifacts"].append(
            {"id": 9, "name": "maestrovpn-tv-test-apk", "expired": False}
        )
        with self.assertRaisesRegex(MODULE.GitHubError, "exact artifact"):
            MODULE.select_exact_artifact(payload, "maestrovpn-tv-test-apk")


class SafeExtractionTest(unittest.TestCase):
    @staticmethod
    def write_zip(path: Path, members: dict[str, bytes]) -> None:
        with zipfile.ZipFile(path, "w") as archive:
            for name, body in members.items():
                archive.writestr(name, body)

    def test_safe_extract_writes_only_relative_members_below_destination(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "artifact.zip"
            destination = root / "extracted"
            self.write_zip(archive, {"nested/result.txt": b"ok", "report.json": b"{}"})

            MODULE.safe_extract_zip(archive, destination)

            self.assertEqual((destination / "nested/result.txt").read_bytes(), b"ok")
            self.assertEqual((destination / "report.json").read_bytes(), b"{}")

    def test_safe_extract_rejects_absolute_parent_and_windows_traversal(self) -> None:
        malicious_names = (
            "../escape.txt",
            "/absolute.txt",
            "C:/windows.txt",
            "nested/../../escape.txt",
            "..\\escape.txt",
            "\\\\server\\share\\escape.txt",
            "NUL",
            "nested/CON.txt",
        )
        for index, name in enumerate(malicious_names):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / f"bad-{index}.zip"
                destination = root / "extracted"
                self.write_zip(archive, {name: b"bad"})

                with self.assertRaises(MODULE.ArtifactSafetyError):
                    MODULE.safe_extract_zip(archive, destination)

                self.assertFalse(destination.exists())
                self.assertFalse((root / "escape.txt").exists())


class ArtifactPersistenceTest(unittest.TestCase):
    def test_download_writes_zip_digest_safe_metadata_and_extracted_files(self) -> None:
        class FakeApi:
            def download_artifact(self, artifact_id: int, destination: Path) -> None:
                if artifact_id != 42:
                    raise AssertionError("wrong artifact")
                with zipfile.ZipFile(destination, "w") as archive:
                    archive.writestr("app/test.apk", b"apk")

        run = MODULE.RunRecord(
            run_id=77,
            event="workflow_dispatch",
            head_sha=HEAD_SHA,
            head_branch=REF,
            status="completed",
            conclusion="success",
            html_url="https://github.com/example/actions/runs/77",
        )
        artifact = MODULE.ArtifactRecord(
            artifact_id=42,
            name="maestrovpn-tv-test-apk",
            size_in_bytes=3,
        )
        revision = MODULE.LocalRevision(ref=REF, head_sha=HEAD_SHA)

        with tempfile.TemporaryDirectory() as temporary:
            result = MODULE.persist_artifact(
                FakeApi(),
                run,
                artifact,
                revision,
                "android",
                Path(temporary),
            )

            expected_digest = hashlib.sha256(result.zip_path.read_bytes()).hexdigest()
            self.assertEqual(result.zip_sha256, expected_digest)
            self.assertEqual(result.run_directory.name, "run-77")
            self.assertEqual((result.extracted_directory / "app/test.apk").read_bytes(), b"apk")
            metadata = json.loads(result.metadata_path.read_text(encoding="utf-8"))
            self.assertEqual(metadata["head_sha"], HEAD_SHA)
            self.assertEqual(metadata["workflow"], "android-test.yml")
            self.assertEqual(metadata["artifact"]["zip_sha256"], expected_digest)
            serialized = result.metadata_path.read_text(encoding="utf-8")
            self.assertNotIn("Authorization", serialized)
            self.assertNotIn("token", serialized.lower())


if __name__ == "__main__":
    unittest.main()
