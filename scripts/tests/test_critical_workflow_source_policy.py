from __future__ import annotations

import ast
import base64
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.parse


REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = (
    REPO_ROOT / ".github" / "workflows" / "critical-workflow-source-policy.yml"
)
CODEOWNERS = REPO_ROOT / ".github" / "CODEOWNERS"

EXPECTED_ALLOWED = {
    ".github/workflows/ha-control-plane.yml": {
        "db5da2254acdadcd11095748fb07848627aa492a5d0094638e62e1602444cfe7"
    },
    ".github/workflows/ha-dr-restore-drill.yml": {
        "0982b58c98ad8dc56cd6a68fbb19554e4ec3c8a9f237a3860770e225adcfdef9"
    },
    ".github/workflows/yandex-cdn-release.yml": {
        "54b906858b483dac81cc6e50e0c3c2657d5de5b3d51bd676716bf100dfd24d33"
    },
}

EXPECTED_CODEOWNERS = (
    "/.github/CODEOWNERS @evgenmay1978-del",
    "/.github/workflows/critical-workflow-source-policy.yml @evgenmay1978-del",
    "/.github/workflows/ha-control-plane.yml @evgenmay1978-del",
    "/.github/workflows/ha-dr-restore-drill.yml @evgenmay1978-del",
    "/.github/workflows/yandex-cdn-release.yml @evgenmay1978-del",
)


def workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def embedded_python(source: str) -> str:
    marker = "        run: |\n"
    if source.count(marker) != 1:
        raise AssertionError("guard workflow must define one exact inline run block")
    tail = source.split(marker, 1)[1]
    body: list[str] = []
    for raw in tail.splitlines():
        if raw == "":
            body.append("")
            continue
        if not raw.startswith("          "):
            break
        body.append(raw[10:])
    if not body:
        raise AssertionError("guard workflow inline Python body is empty")
    return "\n".join(body) + "\n"


def assigned_literal(body: str, name: str) -> object:
    tree = ast.parse(body)
    matches = [
        node
        for node in tree.body
        if isinstance(node, ast.Assign)
        and len(node.targets) == 1
        and isinstance(node.targets[0], ast.Name)
        and node.targets[0].id == name
    ]
    if len(matches) != 1:
        raise AssertionError(f"inline policy must assign {name} exactly once")
    return ast.literal_eval(matches[0].value)


class FakeResponse:
    def __init__(self, body: bytes) -> None:
        self.status = 200
        self._body = body

    def __enter__(self) -> "FakeResponse":
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self, limit: int) -> bytes:
        return self._body[:limit]


def run_inline_policy(
    *,
    first_mode: str = "100644",
    first_blob_mismatch: bool = False,
) -> None:
    body = embedded_python(workflow_text())
    paths = tuple(EXPECTED_ALLOWED)
    contents = {path: (REPO_ROOT / path).read_bytes() for path in paths}
    blob_ids = {
        path: f"{index:040x}"
        for index, path in enumerate(paths, start=1)
    }
    tree_entries = [
        {
            "path": path,
            "mode": first_mode if index == 0 else "100644",
            "type": "blob",
            "sha": blob_ids[path],
        }
        for index, path in enumerate(paths)
    ]
    head_sha = "a" * 40
    tree_sha = "b" * 40
    event = {
        "pull_request": {
            "head": {
                "sha": head_sha,
                "repo": {"full_name": "evgenmay1978-del/proectmaestro-vpn"},
            }
        }
    }

    def fake_urlopen(request: object, timeout: int) -> FakeResponse:
        if timeout != 20:
            raise AssertionError("policy API timeout changed")
        url = getattr(request, "full_url")
        if "/git/commits/" in url:
            payload = {"sha": head_sha, "tree": {"sha": tree_sha}}
        elif "/git/trees/" in url:
            payload = {
                "sha": tree_sha, "truncated": False, "tree": tree_entries
            }
        else:
            marker = "/contents/"
            if marker not in url:
                raise AssertionError(f"unexpected policy API URL: {url}")
            path = urllib.parse.unquote(
                urllib.parse.urlsplit(url).path.split(marker, 1)[1]
            )
            raw = contents[path]
            blob_sha = blob_ids[path]
            if first_blob_mismatch and path == paths[0]:
                blob_sha = "f" * 40
            payload = {
                "type": "file",
                "encoding": "base64",
                "path": path,
                "sha": blob_sha,
                "size": len(raw),
                "content": base64.b64encode(raw).decode("ascii"),
            }
        return FakeResponse(json.dumps(payload).encode("utf-8"))

    with tempfile.TemporaryDirectory() as directory:
        event_path = Path(directory) / "event.json"
        event_path.write_text(json.dumps(event), encoding="utf-8")
        environment = {
            "GITHUB_EVENT_PATH": str(event_path),
            "GH_TOKEN": "test-token",
            "GITHUB_API_URL": "https://api.github.com",
        }
        with (
            mock.patch.dict(os.environ, environment, clear=False),
            mock.patch("urllib.request.urlopen", side_effect=fake_urlopen),
            mock.patch("builtins.print"),
        ):
            exec(compile(body, "<critical-workflow-policy>", "exec"), {})


class CriticalWorkflowSourcePolicyTest(unittest.TestCase):
    def test_trigger_is_base_owned_and_never_path_filtered(self) -> None:
        source = workflow_text()
        header = source.split("permissions:\n", 1)[0]
        self.assertEqual(
            header,
            "name: Critical workflow source policy\n"
            "\n"
            "on:\n"
            "  pull_request_target:\n"
            "    types:\n"
            "      - opened\n"
            "      - reopened\n"
            "      - synchronize\n"
            "      - ready_for_review\n"
            "\n",
        )
        self.assertNotIn("paths:", header)
        self.assertNotIn("pull_request:\n", header)

    def test_permissions_and_required_check_identity_are_exact(self) -> None:
        source = workflow_text()
        self.assertIn("permissions:\n  contents: read\n\njobs:\n", source)
        self.assertNotIn("write-all", source)
        self.assertNotIn(": write", source)
        self.assertIn(
            "jobs:\n"
            "  critical-workflow-source-policy:\n"
            "    name: critical-workflow-source-policy\n"
            "    runs-on: ubuntu-24.04\n"
            "    timeout-minutes: 5\n",
            source,
        )

    def test_guard_executes_only_base_owned_inline_python(self) -> None:
        source = workflow_text()
        body = embedded_python(source)
        self.assertNotIn("actions/checkout@", source)
        self.assertNotIn("subprocess", body)
        self.assertNotIn("os.system", body)
        self.assertNotIn("eval(", body)
        self.assertNotIn("exec(", body)
        self.assertIn("        shell: python\n", source)
        self.assertIn("GH_TOKEN: ${{ github.token }}", source)
        self.assertIn("GITHUB_API_URL: ${{ github.api_url }}", source)

    def test_allowlist_is_exact_and_sha256_only(self) -> None:
        body = embedded_python(workflow_text())
        self.assertEqual(assigned_literal(body, "ALLOWED"), EXPECTED_ALLOWED)
        for allowed in EXPECTED_ALLOWED.values():
            for digest in allowed:
                self.assertEqual(len(digest), 64)
                self.assertTrue(all(character in "0123456789abcdef" for character in digest))

    def test_head_identity_api_payload_and_bytes_fail_closed(self) -> None:
        body = embedded_python(workflow_text())
        required = (
            'event["pull_request"]["head"]',
            'head["repo"]["full_name"]',
            're.fullmatch(r"[0-9a-f]{40}", head_sha)',
            're.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", full_name)',
            'urllib.parse.quote(owner, safe="")',
            'urllib.parse.quote(repository, safe="")',
            'urllib.parse.quote(path, safe="/")',
            'payload.get("type") != "file"',
            'payload.get("encoding") != "base64"',
            "MAX_FILE_BYTES = 256 * 1024",
            "base64.b64decode(compact_content, validate=True)",
            'decoded.decode("utf-8", errors="strict")',
            'text.replace("\\r\\n", "\\n").replace("\\r", "\\n")',
            'hashlib.sha256(normalized.encode("utf-8")).hexdigest()',
        )
        for fragment in required:
            with self.subTest(fragment=fragment):
                self.assertIn(fragment, body)

    def test_api_request_is_bounded_authenticated_and_never_logs_content(self) -> None:
        body = embedded_python(workflow_text())
        self.assertIn('"Authorization": f"Bearer {token}"', body)
        self.assertIn("urllib.request.urlopen(request, timeout=20)", body)
        self.assertIn("response.read(MAX_RESPONSE_BYTES + 1)", body)
        self.assertNotIn("print(content", body)
        self.assertNotIn("print(decoded", body)
        self.assertNotIn("print(text", body)

    def test_codeowners_locks_policy_and_all_critical_workflows(self) -> None:
        active = tuple(
            raw.strip()
            for raw in CODEOWNERS.read_text(encoding="utf-8").splitlines()
            if raw.strip() and not raw.lstrip().startswith("#")
        )
        self.assertEqual(active, EXPECTED_CODEOWNERS)


    def test_git_tree_requires_regular_100644_blobs(self) -> None:
        body = embedded_python(workflow_text())
        required = (
            'f"/git/commits/{head_sha}"',
            'commit_payload.get("sha") != head_sha',
            'tree_sha = commit_payload.get("tree", {}).get("sha")',
            'f"/git/trees/{tree_sha}?recursive=1"',
            'tree_payload.get("sha") != tree_sha',
            'tree_payload.get("truncated") is not False',
            'entry.get("type") != "blob"',
            'entry.get("mode") != "100644"',
            'payload.get("sha") != expected_blob_sha',
        )
        for fragment in required:
            with self.subTest(fragment=fragment):
                self.assertIn(fragment, body)

    def test_inline_policy_accepts_reviewed_regular_blobs(self) -> None:
        run_inline_policy()

    def test_dereferenced_symlink_file_response_is_rejected(self) -> None:
        with self.assertRaises(SystemExit):
            run_inline_policy(first_mode="120000")

    def test_contents_blob_must_match_tree_blob(self) -> None:
        with self.assertRaises(SystemExit):
            run_inline_policy(first_blob_mismatch=True)


if __name__ == "__main__":
    unittest.main()
