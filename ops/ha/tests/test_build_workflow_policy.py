from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
import inspect
import io
from pathlib import Path
import re
import tempfile
import unittest
from unittest import mock

from ops.ha import build_workflow_policy as POLICY
from ops.ha.build_workflow_policy import WorkflowPolicyError, validate_workflow


CHECKOUT_SHA = "11bd71901bbe5b1630ceea73d27597364c9af683"
SETUP_GO_SHA = "40f1582b2485089dde7abd97c1529aa768e1baff"
UPLOAD_SHA = "ea165f8d65b6e75b540449e92b4886f43607fa02"
BRANCH = "codex/yandex-cdn-whitelist-task3-sync"

VALID_WORKFLOW = f"""name: HA immutable panel artifact

on:
  push:
    branches:
      - {BRANCH}
  pull_request:
    branches:
      - {BRANCH}
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: ha-build-${{{{ github.workflow }}}}-${{{{ github.ref }}}}
  cancel-in-progress: false

jobs:
  build-panel-artifact:
    name: Build immutable panel artifact
    runs-on: ubuntu-24.04
    timeout-minutes: 45
    permissions:
      contents: read
    steps:
      - name: Checkout
        uses: actions/checkout@{CHECKOUT_SHA}
        with:
          persist-credentials: false

      - name: Test build workflow policy
        env:
          LC_ALL: C
        run: |
          set -euo pipefail
          python -m unittest ops.ha.tests.test_build_manifest ops.ha.tests.test_build_workflow_policy -v
          python ops/ha/build_workflow_policy.py

      - name: Set up Go
        uses: actions/setup-go@{SETUP_GO_SHA}
        with:
          go-version-file: backend/go.mod
          cache-dependency-path: backend/go.sum

      - name: Test backend
        working-directory: backend
        env:
          LC_ALL: C
        run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 ./...

      - name: Race-test backend
        working-directory: backend
        env:
          LC_ALL: C
        run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 -race ./...

      - name: Vet backend
        working-directory: backend
        env:
          LC_ALL: C
        run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go vet ./...

      - name: Test isolated rqlite harness
        env:
          LC_ALL: C
        run: bash ops/ha/test-ci-rqlite-cluster.sh

      - name: Start isolated rqlite cluster
        env:
          LC_ALL: C
        run: bash ops/ha/ci-rqlite-cluster.sh start

      - name: Test rqlite integration
        working-directory: backend
        env:
          LC_ALL: C
        run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 -tags=rqlite_integration ./...

      - name: Stop isolated rqlite cluster
        if: ${{{{ always() && hashFiles('ops/ha/ci-rqlite-cluster.sh') != '' }}}}
        env:
          LC_ALL: C
        run: bash ops/ha/ci-rqlite-cluster.sh stop

      - name: Build reproducible panel
        working-directory: backend
        env:
          LC_ALL: C
        run: |
          set -euo pipefail
          umask 077
          build_a="$RUNNER_TEMP/maestro-build-a"
          build_b="$RUNNER_TEMP/maestro-build-b"
          test ! -e "$build_a"
          test ! -e "$build_b"
          mkdir -m 700 -- "$build_a" "$build_b"
          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \\
            -trimpath -buildvcs=true \\
            -ldflags "-s -w -X github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api.BuildCommit=$GITHUB_SHA" \\
            -o "$build_a/maestro-panel" ./cmd/maestro-panel
          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \\
            -trimpath -buildvcs=true \\
            -ldflags "-s -w -X github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api.BuildCommit=$GITHUB_SHA" \\
            -o "$build_b/maestro-panel" ./cmd/maestro-panel
          cmp -- "$build_a/maestro-panel" "$build_b/maestro-panel"
          go version -m "$build_a/maestro-panel" > "$RUNNER_TEMP/maestro-panel.buildinfo"
          python - "$GITHUB_SHA" "$RUNNER_TEMP/maestro-panel.buildinfo" <<'PY'
          from pathlib import Path
          import sys

          revision = sys.argv[1]
          lines = Path(sys.argv[2]).read_text(encoding="utf-8").splitlines()
          settings = []
          for raw in lines:
              fields = raw.split("\\t")
              if len(fields) != 3 or fields[1] != "build":
                  continue
              key, separator, value = fields[2].partition("=")
              if not separator or any(existing == key for existing, _ in settings):
                  raise SystemExit("invalid duplicate build setting")
              settings.append((key, value))
          expected = (
              ("GOOS", "linux"),
              ("GOARCH", "amd64"),
              ("vcs.revision", revision),
              ("vcs.modified", "false"),
          )
          for key, wanted in expected:
              actual = [value for candidate, value in settings if candidate == key]
              if actual != [wanted]:
                  raise SystemExit("build identity mismatch")
          PY
          test ! -e "$GITHUB_WORKSPACE/dist"
          mkdir -m 700 -- "$GITHUB_WORKSPACE/dist"
          mv -- "$build_a/maestro-panel" "$GITHUB_WORKSPACE/dist/maestro-panel"

      - name: Create and verify manifest
        env:
          LC_ALL: C
        run: |
          set -euo pipefail
          go_version="$(go env GOVERSION)"
          test "$go_version" = "go1.25.0"
          python ops/ha/build_manifest.py create \\
            --artifact-root dist \\
            --output dist/manifest.json \\
            --repository "$GITHUB_REPOSITORY" \\
            --ref "$GITHUB_REF" \\
            --commit-sha "$GITHUB_SHA" \\
            --workflow-run-id "$GITHUB_RUN_ID" \\
            --workflow-run-attempt "$GITHUB_RUN_ATTEMPT" \\
            --go-version "$go_version"
          python ops/ha/build_manifest.py verify \\
            --artifact-root dist \\
            --manifest dist/manifest.json \\
            --expected-repository "$GITHUB_REPOSITORY" \\
            --expected-ref "$GITHUB_REF" \\
            --expected-commit-sha "$GITHUB_SHA" \\
            --expected-workflow-run-id "$GITHUB_RUN_ID" \\
            --expected-workflow-run-attempt "$GITHUB_RUN_ATTEMPT" > /dev/null

      - name: Assert exact artifact membership
        env:
          LC_ALL: C
        run: |
          set -euo pipefail
          python - <<'PY'
          import os
          import stat

          entries = list(os.scandir("dist"))
          expected_names = ["maestro-panel", "manifest.json"]
          if sorted(entry.name for entry in entries) != expected_names:
              raise SystemExit("artifact membership mismatch")
          for entry in entries:
              metadata = entry.stat(follow_symlinks=False)
              if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1:
                  raise SystemExit("artifact member is not a single-link regular file")
          PY

      - name: Upload immutable panel artifact
        if: github.event_name != 'pull_request'
        uses: actions/upload-artifact@{UPLOAD_SHA}
        with:
          name: maestro-panel-${{{{ github.sha }}}}
          path: |
            dist/maestro-panel
            dist/manifest.json
          if-no-files-found: error
          compression-level: 0
          overwrite: false
          include-hidden-files: false
"""

CLEANUP_STEP = """      - name: Stop isolated rqlite cluster
        if: ${{ always() && hashFiles('ops/ha/ci-rqlite-cluster.sh') != '' }}
        env:
          LC_ALL: C
        run: bash ops/ha/ci-rqlite-cluster.sh stop

"""
UPLOAD_STEP = f"""      - name: Upload immutable panel artifact
        if: github.event_name != 'pull_request'
        uses: actions/upload-artifact@{UPLOAD_SHA}
        with:
          name: maestro-panel-${{{{ github.sha }}}}
          path: |
            dist/maestro-panel
            dist/manifest.json
          if-no-files-found: error
          compression-level: 0
          overwrite: false
          include-hidden-files: false
"""


class BuildWorkflowPolicyTests(unittest.TestCase):
    maxDiff = None

    def mutate(self, old: str, new: str, source: str = VALID_WORKFLOW) -> str:
        self.assertEqual(1, source.count(old), f"non-unique mutation anchor: {old!r}")
        changed = source.replace(old, new, 1)
        self.assertNotEqual(source, changed)
        return changed

    def rejected(self, text: object, code: str) -> None:
        expected = rf"\A{re.escape('ha-build-workflow-policy:' + code)}\Z"
        with self.assertRaisesRegex(WorkflowPolicyError, expected):
            validate_workflow(text)  # type: ignore[arg-type]

    def test_safe_fixture_passes(self) -> None:
        self.assertIsNone(validate_workflow(VALID_WORKFLOW))

    def test_repository_workflow_passes_policy(self) -> None:
        source = POLICY.WORKFLOW.read_text(encoding="ascii")

        self.assertIsNone(validate_workflow(source))

    def test_has_no_third_party_yaml_dependency(self) -> None:
        source = inspect.getsource(POLICY)
        self.assertIsNone(
            re.search(r"(?m)^\s*(?:from\s+(?:yaml|ruamel)|import\s+(?:yaml|ruamel))\b", source)
        )

    def test_rejects_required_security_mutations(self) -> None:
        root_permissions = "permissions:\n  contents: read\n"
        job_permissions = "    permissions:\n      contents: read\n"
        cases = (
            (f"actions/checkout@{CHECKOUT_SHA}", "actions/checkout@v4", "action-pin"),
            (f"actions/checkout@{CHECKOUT_SHA}", "actions/checkout@" + "0" * 40, "action-pin"),
            (f"actions/setup-go@{SETUP_GO_SHA}", "actions/cache@" + "1" * 40, "action-boundary"),
            (f"actions/upload-artifact@{UPLOAD_SHA}", "actions/upload-artifact@v4", "action-pin"),
            (root_permissions, "permissions: write-all\n", "permissions-boundary"),
            (job_permissions, "    permissions:\n      contents: write\n", "permissions-boundary"),
            ("    timeout-minutes: 45\n", "", "timeout-boundary"),
            ("    timeout-minutes: 45\n", "    timeout-minutes: 61\n", "timeout-boundary"),
            ("  cancel-in-progress: false\n", "  cancel-in-progress: true\n", "concurrency-boundary"),
            ("  pull_request:\n", "  pull_request_target:\n", "trigger-boundary"),
            ("    runs-on: ubuntu-24.04\n", "    runs-on: self-hosted\n", "runner-boundary"),
            ("    timeout-minutes: 45\n", "    timeout-minutes: 45\n    environment: production\n", "job-boundary"),
            ("          persist-credentials: false\n", "          persist-credentials: true\n", "action-boundary"),
            ("          go-version-file: backend/go.mod\n", "          go-version-file: go.mod\n", "action-boundary"),
            ("        if: github.event_name != 'pull_request'\n", "        if: always()\n", "upload-boundary"),
            ("            dist/maestro-panel\n            dist/manifest.json\n", "            dist/**\n", "upload-boundary"),
            ("            dist/manifest.json\n", "            dist/manifest.json\n            dist/debug.log\n", "upload-boundary"),
            ("          include-hidden-files: false\n", "          include-hidden-files: true\n", "upload-boundary"),
            ("          overwrite: false\n", "          overwrite: true\n", "upload-boundary"),
            ("          compression-level: 0\n", "          compression-level: 6\n", "upload-boundary"),
            ("          if-no-files-found: error\n", "          if-no-files-found: warn\n", "upload-boundary"),
        )
        for old, new, code in cases:
            with self.subTest(code=code, replacement=new):
                self.rejected(self.mutate(old, new), code)

    def test_rejects_wrong_push_branch(self) -> None:
        old = f"  push:\n    branches:\n      - {BRANCH}\n"
        new = "  push:\n    branches:\n      - main\n"
        self.rejected(self.mutate(old, new), "trigger-boundary")

    def test_rejects_second_job(self) -> None:
        unsafe = VALID_WORKFLOW + (
            "  deploy-production:\n"
            "    runs-on: ubuntu-24.04\n"
            "    timeout-minutes: 5\n"
            "    steps:\n"
            "      - name: Deploy\n"
            "        run: true\n"
        )
        self.rejected(unsafe, "job-boundary")

    def test_rejects_second_upload(self) -> None:
        extra = UPLOAD_STEP.replace("Upload immutable", "Upload extra", 1)
        self.rejected(self.mutate(UPLOAD_STEP, extra + "\n" + UPLOAD_STEP), "upload-boundary")

    def test_rejects_forbidden_commands_and_capabilities(self) -> None:
        anchor = "          python ops/ha/build_workflow_policy.py\n"
        cases = (
            ("          curl -fsS https://203.0.113.10/healthz\n", "network-boundary"),
            ("          wget https://example.invalid/bootstrap\n", "network-boundary"),
            ("          ssh deploy@example.invalid true\n", "network-boundary"),
            ("          scp artifact host:/tmp\n", "network-boundary"),
            ("          rsync artifact host:/tmp\n", "network-boundary"),
            ("          systemctl restart maestro-panel\n", "command-boundary"),
            ("          service nginx reload\n", "command-boundary"),
            ("          sudo ufw allow 443/tcp\n", "command-boundary"),
            ("          bash ops/ha/deploy-node.sh apply\n", "command-boundary"),
            ("          printf '%s' '${{ secrets.DEPLOY_TOKEN }}'\n", "command-boundary"),
        )
        for addition, code in cases:
            with self.subTest(command=addition.strip()):
                self.rejected(self.mutate(anchor, anchor + addition), code)

    def test_rejects_cleanup_mutations(self) -> None:
        cases = (
            (CLEANUP_STEP, ""),
            ("        if: ${{ always() && hashFiles('ops/ha/ci-rqlite-cluster.sh') != '' }}\n", "        if: success()\n"),
            ("        run: bash ops/ha/ci-rqlite-cluster.sh stop\n", "        run: true\n"),
        )
        for old, new in cases:
            with self.subTest(replacement=new):
                self.rejected(self.mutate(old, new), "cleanup-boundary")

    def test_rejects_missing_or_decoy_self_policy(self) -> None:
        cli = "          python ops/ha/build_workflow_policy.py\n"
        suite = "          python -m unittest ops.ha.tests.test_build_manifest ops.ha.tests.test_build_workflow_policy -v\n"
        cases = (
            (cli, ""),
            (cli, "          # python ops/ha/build_workflow_policy.py\n"),
            (cli, "          printf '%s\\n' 'python ops/ha/build_workflow_policy.py' >/dev/null\n"),
            (suite, ""),
        )
        for old, new in cases:
            with self.subTest(replacement=new):
                self.rejected(self.mutate(old, new), "self-policy")

    def test_rejects_unreviewed_run_step_even_when_payload_is_obfuscated(self) -> None:
        addition = (
            "      - name: Unreviewed local helper\n"
            "        run: python -c \"m=__import__('urllib'+'.request'); "
            "m.urlopen('ht'+'tps:'+'//example.invalid')\"\n\n"
        )
        unsafe = self.mutate(
            "      - name: Upload immutable panel artifact\n",
            addition + "      - name: Upload immutable panel artifact\n",
        )
        self.rejected(unsafe, "step-boundary")

    def without_step(self, name: str, next_name: str, source: str = VALID_WORKFLOW) -> str:
        start = f"      - name: {name}\n"
        end = f"      - name: {next_name}\n"
        self.assertEqual(1, source.count(start), f"missing step start: {name}")
        self.assertEqual(1, source.count(end), f"missing next step: {next_name}")
        start_index = source.index(start)
        end_index = source.index(end, start_index + len(start))
        self.assertGreater(end_index, start_index)
        return source[:start_index] + source[end_index:]

    def test_rejects_missing_mandatory_task3_gates(self) -> None:
        cases = (
            ("Test backend", "Race-test backend", "backend-boundary"),
            ("Race-test backend", "Vet backend", "backend-boundary"),
            ("Vet backend", "Test isolated rqlite harness", "backend-boundary"),
            ("Test isolated rqlite harness", "Start isolated rqlite cluster", "step-boundary"),
            ("Start isolated rqlite cluster", "Test rqlite integration", "step-boundary"),
            ("Test rqlite integration", "Stop isolated rqlite cluster", "step-boundary"),
            ("Build reproducible panel", "Create and verify manifest", "build-boundary"),
            ("Create and verify manifest", "Assert exact artifact membership", "manifest-boundary"),
            ("Assert exact artifact membership", "Upload immutable panel artifact", "membership-boundary"),
        )
        for name, next_name, code in cases:
            with self.subTest(step=name):
                self.rejected(self.without_step(name, next_name), code)

    def test_rejects_task3_gate_substitutions(self) -> None:
        cases = (
            (
                "run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 ./...",
                "run: go test -count=1 ./...",
                "backend-boundary",
            ),
            (
                "run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go test -count=1 -race ./...",
                "run: env -u MAESTRO_S2_PASS go test -count=1 -race ./...",
                "backend-boundary",
            ),
            (
                "run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS go vet ./...",
                "run: go vet ./...",
                "backend-boundary",
            ),
            (
                '            -o "$build_b/maestro-panel" ./cmd/maestro-panel',
                '            -tags task3wrong -o "$build_b/maestro-panel" ./cmd/maestro-panel',
                "build-boundary",
            ),
            (
                '          cmp -- "$build_a/maestro-panel" "$build_b/maestro-panel"\n',
                "          true # reproducibility comparison removed\n",
                "reproducibility-boundary",
            ),
            (
                '          go version -m "$build_a/maestro-panel" > "$RUNNER_TEMP/maestro-panel.buildinfo"\n',
                "          true # build metadata proof removed\n",
                "metadata-boundary",
            ),
            (
                '              ("vcs.modified", "false"),',
                '              ("vcs.modified", "true"),',
                "metadata-boundary",
            ),
            (
                "python ops/ha/build_manifest.py create",
                "python ops/ha/build_manifest.py inspect",
                "manifest-boundary",
            ),
            (
                "python ops/ha/build_manifest.py verify",
                "python ops/ha/build_manifest.py inspect",
                "manifest-boundary",
            ),
            (
                '          expected_names = ["maestro-panel", "manifest.json"]',
                '          expected_names = ["maestro-panel"]',
                "membership-boundary",
            ),
        )
        for old, new, code in cases:
            with self.subTest(code=code, replacement=new):
                self.rejected(self.mutate(old, new), code)

    def test_rejects_wrong_step_working_directories(self) -> None:
        backend_steps = (
            ("Test backend", "backend-boundary"),
            ("Race-test backend", "backend-boundary"),
            ("Vet backend", "backend-boundary"),
            ("Test rqlite integration", "step-boundary"),
            ("Build reproducible panel", "build-boundary"),
        )
        for name, code in backend_steps:
            old = f"      - name: {name}\n        working-directory: backend\n"
            new = f"      - name: {name}\n"
            with self.subTest(step=name, mode="missing-backend"):
                self.rejected(self.mutate(old, new), code)

        root_steps = (
            ("Test build workflow policy", "step-boundary"),
            ("Test isolated rqlite harness", "step-boundary"),
            ("Start isolated rqlite cluster", "step-boundary"),
            ("Create and verify manifest", "manifest-boundary"),
            ("Assert exact artifact membership", "membership-boundary"),
        )
        for name, code in root_steps:
            old = f"      - name: {name}\n        env:\n"
            new = f"      - name: {name}\n        working-directory: backend\n        env:\n"
            with self.subTest(step=name, mode="unexpected-backend"):
                self.rejected(self.mutate(old, new), code)

        old_cleanup = "      - name: Stop isolated rqlite cluster\n        if:"
        new_cleanup = "      - name: Stop isolated rqlite cluster\n        working-directory: backend\n        if:"
        self.rejected(self.mutate(old_cleanup, new_cleanup), "cleanup-boundary")

    def test_rejects_bare_non_loopback_ip(self) -> None:
        anchor = "          python ops/ha/build_workflow_policy.py\n"
        addition = "          printf '%s\\n' 203.0.113.10 >/dev/null\n"
        self.rejected(self.mutate(anchor, anchor + addition), "network-boundary")

    def test_rejects_additional_action_and_upload_option_mutations(self) -> None:
        cases = (
            (f"actions/setup-go@{SETUP_GO_SHA}", "actions/setup-go@" + "0" * 40, "action-pin"),
            ("          persist-credentials: false\n", "", "action-boundary"),
            ("          cache-dependency-path: backend/go.sum\n", "          cache-dependency-path: go.sum\n", "action-boundary"),
            ("          name: maestro-panel-${{ github.sha }}\n", "          name: maestro-panel-latest\n", "upload-boundary"),
            ("            dist/manifest.json\n", "            dist/manifest.json\n            dist/manifest.json\n", "upload-boundary"),
            (
                "            dist/maestro-panel\n            dist/manifest.json\n",
                "            dist/manifest.json\n            dist/maestro-panel\n",
                "upload-boundary",
            ),
        )
        for old, new, code in cases:
            with self.subTest(code=code, replacement=new):
                self.rejected(self.mutate(old, new), code)

    def test_comment_in_run_body_is_part_of_active_seal(self) -> None:
        anchor = "          python ops/ha/build_workflow_policy.py\n"
        unsafe = self.mutate(
            anchor,
            "          # permissions: write-all\n"
            "          # uses: actions/upload-artifact@v4\n"
            "          # deploy-production:\n" + anchor,
        )
        self.rejected(unsafe, "step-boundary")

    def test_comment_forbidden_command_in_run_body_is_sealed(self) -> None:
        anchor = "          python ops/ha/build_workflow_policy.py\n"
        unsafe = self.mutate(anchor, "          # curl https://203.0.113.10\n" + anchor)
        self.rejected(unsafe, "step-boundary")

    def test_comment_after_shell_continuation_is_sealed(self) -> None:
        anchor = (
            '          mkdir -m 700 -- "$build_a" "$build_b"\n'
            "          CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \\\n"
        )
        unsafe = self.mutate(
            anchor,
            anchor + "          # continuation semantics changed\n",
        )
        self.rejected(unsafe, "step-boundary")

    def test_encoded_newline_comment_in_python_heredoc_is_sealed(self) -> None:
        anchor = "          from pathlib import Path\n"
        unsafe = self.mutate(
            anchor,
            "          # coding: utf-7\n"
            "          #+AAo-raise SystemExit('unexpected')\n"
            + anchor,
        )
        self.rejected(unsafe, "step-boundary")

    def test_rejects_step_and_yaml_bypasses(self) -> None:
        cases = (
            (
                "      - name: Test build workflow policy\n"
                "        env:\n"
                "          LC_ALL: C\n"
                "        run: |\n",
                "      - name: Test build workflow policy\n"
                "        env:\n"
                "          LC_ALL: C\n"
                "        run: >\n",
                "step-boundary",
            ),
            ("      - name: Test build workflow policy\n", "      - name: Test build workflow policy\n        continue-on-error: true\n", "step-boundary"),
            ("      - name: Test build workflow policy\n", "      - name: Test build workflow policy\n        shell: /bin/true {{0}}\n", "step-boundary"),
            ("jobs:\n", "defaults:\n  run:\n    shell: /bin/true {{0}}\njobs:\n", "invalid-structure"),
            (root_permissions := "permissions:\n  contents: read\n", "\"permissions\": write-all\n", "invalid-structure"),
        )
        for old, new, code in cases:
            with self.subTest(code=code, replacement=new):
                self.rejected(self.mutate(old, new), code)

    def test_rejects_invalid_input(self) -> None:
        for value in (None, "", VALID_WORKFLOW.replace("\n", "\r\n", 1), VALID_WORKFLOW + "\t"):
            with self.subTest(value=repr(value)[:40]):
                self.rejected(value, "invalid-input")

    def run_main(self, payload: bytes | None) -> tuple[int, str, str]:
        with tempfile.TemporaryDirectory(prefix="ha-build-policy-") as directory:
            workflow = Path(directory) / "ha-build.yml"
            if payload is not None:
                workflow.write_bytes(payload)
            stdout = io.StringIO()
            stderr = io.StringIO()
            with mock.patch.object(POLICY, "WORKFLOW", workflow), redirect_stdout(stdout), redirect_stderr(stderr):
                result = POLICY.main()
            return result, stdout.getvalue(), stderr.getvalue()

    def test_cli_contract(self) -> None:
        result, stdout, stderr = self.run_main(VALID_WORKFLOW.encode("utf-8"))
        self.assertEqual((0, "HA build workflow policy passed\n", ""), (result, stdout, stderr))

        result, stdout, stderr = self.run_main(b"\xffSECRET")
        self.assertEqual((1, "", "ha-build-workflow-policy:source-unavailable\n"), (result, stdout, stderr))

        result, stdout, stderr = self.run_main(None)
        self.assertEqual((1, "", "ha-build-workflow-policy:source-unavailable\n"), (result, stdout, stderr))

        unsafe = self.mutate("permissions:\n  contents: read\n", "permissions: write-all # OWNER_SECRET\n")
        result, stdout, stderr = self.run_main(unsafe.encode("utf-8"))
        self.assertEqual((1, "", "ha-build-workflow-policy:permissions-boundary\n"), (result, stdout, stderr))
        self.assertNotIn("OWNER_SECRET", stderr)


    def run_main_with_source_object(self, source: object) -> tuple[int, str, str]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with (
            mock.patch.object(POLICY, "WORKFLOW", source),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            result = POLICY.main()
        return result, stdout.getvalue(), stderr.getvalue()

    def test_cli_rejects_symlink_source(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ha-build-policy-symlink-") as directory:
            root = Path(directory)
            target = root / "target.yml"
            workflow = root / "ha-build.yml"
            target.write_bytes(VALID_WORKFLOW.encode("ascii"))
            try:
                workflow.symlink_to(target)
            except (NotImplementedError, OSError) as error:
                self.skipTest(f"symlink unavailable: {type(error).__name__}")
            observed = self.run_main_with_source_object(workflow)
        self.assertEqual(
            (1, "", "ha-build-workflow-policy:source-unavailable\n"),
            observed,
        )

    def test_cli_rejects_path_replacement_between_lstat_and_open(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ha-build-policy-replace-") as directory:
            root = Path(directory)
            workflow = root / "ha-build.yml"
            replacement = root / "replacement.yml"
            workflow.write_bytes(VALID_WORKFLOW.encode("ascii"))
            replacement.write_bytes(VALID_WORKFLOW.encode("ascii"))

            class ReplacingSource:
                def __init__(self) -> None:
                    self.swapped = False

                def __fspath__(self) -> str:
                    return str(workflow)

                def lstat(self):
                    if self.swapped:
                        return workflow.lstat()
                    before = workflow.lstat()
                    replacement.replace(workflow)
                    self.swapped = True
                    return before

                def read_bytes(self) -> bytes:
                    return workflow.read_bytes()

            observed = self.run_main_with_source_object(ReplacingSource())
        self.assertEqual(
            (1, "", "ha-build-workflow-policy:source-unavailable\n"),
            observed,
        )

    def test_cli_rejects_size_growth_between_lstat_and_open(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ha-build-policy-growth-") as directory:
            workflow = Path(directory) / "ha-build.yml"
            workflow.write_bytes(VALID_WORKFLOW.encode("ascii"))

            class GrowingSource:
                def __init__(self) -> None:
                    self.grown = False

                def __fspath__(self) -> str:
                    return str(workflow)

                def lstat(self):
                    if self.grown:
                        return workflow.lstat()
                    before = workflow.lstat()
                    with workflow.open("ab") as stream:
                        stream.write(b"# descriptor-race-growth\n")
                    self.grown = True
                    return before

                def read_bytes(self) -> bytes:
                    return workflow.read_bytes()

            observed = self.run_main_with_source_object(GrowingSource())
        self.assertEqual(
            (1, "", "ha-build-workflow-policy:source-unavailable\n"),
            observed,
        )

    def test_cli_redacts_unexpected_runtime_error(self) -> None:
        marker = "OWNER_SECRET_RUNTIME_MARKER"
        with tempfile.TemporaryDirectory(prefix="ha-build-policy-runtime-") as directory:
            workflow = Path(directory) / "ha-build.yml"
            workflow.write_bytes(VALID_WORKFLOW.encode("ascii"))
            stdout = io.StringIO()
            stderr = io.StringIO()
            with (
                mock.patch.object(POLICY, "WORKFLOW", workflow),
                mock.patch.object(
                    POLICY,
                    "validate_workflow",
                    side_effect=RuntimeError(marker),
                ),
                redirect_stdout(stdout),
                redirect_stderr(stderr),
            ):
                result = POLICY.main()
        self.assertEqual(1, result)
        self.assertEqual("", stdout.getvalue())
        self.assertEqual(
            "ha-build-workflow-policy:internal-failure\n",
            stderr.getvalue(),
        )
        self.assertNotIn(marker, stderr.getvalue())
if __name__ == "__main__":
    unittest.main()
