from __future__ import annotations

import re
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "yandex-cdn-release.yml"
GRADLE = REPO_ROOT / "app" / "build.gradle.kts"

EXPECTED_JOBS = {
    "format-unit",
    "race-vet",
    "offline-replay",
    "rqlite-purge",
    "android-test-apk",
}
GO_PACKAGES = (
    "./internal/controlplane",
    "./internal/subgen",
    "./internal/shadowbilling",
    "./internal/whitelistapi/v1",
    "./internal/release",
    "./internal/whitelistready",
    "./cmd/maestro-release-validate",
    "./cmd/maestro-whitelist-ready",
    "./internal/testsupport/whitelistfixture",
)
WRAPPERS = (
    "yandex-get-body.sh",
    "yandex-active-stream.sh",
    "yandex-idle-cutoff.sh",
    "yandex-literal-edge.sh",
    "xray-counter-reset.sh",
    "billing-idempotency.sh",
    "duplicate-event-replay.sh",
    "subscription-escaping.sh",
    "edge-rotation.sh",
)
REQUIRED_PATH_FILTERS = (
    ".github/workflows/yandex-cdn-release.yml",
    "app/build.gradle.kts",
    "version.properties",
    "backend/go.mod",
    "backend/go.sum",
    "backend/cmd/maestro-release-validate/**",
    "backend/cmd/maestro-whitelist-ready/**",
    "backend/internal/controlplane/**",
    "backend/internal/release/**",
    "backend/internal/shadowbilling/**",
    "backend/internal/subgen/**",
    "backend/internal/testsupport/whitelistfixture/**",
    "backend/internal/whitelistapi/**",
    "backend/internal/whitelistready/**",
    "docs/yandex-cdn-whitelist/**",
    "docs/superpowers/plans/2026-08-22-yandex-white-list-integration-readiness.md",
    "ops/ha/ci-rqlite-cluster.sh",
    "ops/ha/test-ci-rqlite-cluster.sh",
    "scripts/repro/**",
    "scripts/tests/test_yandex_cdn_*.py",
    "scripts/validate_yandex_cdn_docs.py",
)


def workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def gradle_text() -> str:
    return GRADLE.read_text(encoding="utf-8")


def job_source(source: str, name: str) -> str:
    marker = f"  {name}:\n"
    if marker not in source:
        return ""
    tail = source.split(marker, 1)[1]
    next_job = re.search(r"(?m)^  [a-z0-9][a-z0-9-]*:\n", tail)
    return tail[: next_job.start()] if next_job else tail


class WorkflowSafetyContractTest(unittest.TestCase):
    def test_triggers_cover_every_task7_input_on_push_and_pull_request(self) -> None:
        source = workflow_text()
        pull_request = source.split("  pull_request:\n", 1)[1].split("  push:\n", 1)[0]
        push = source.split("  push:\n", 1)[1].split("\npermissions:\n", 1)[0]
        for path in REQUIRED_PATH_FILTERS:
            with self.subTest(path=path):
                self.assertIn(path, pull_request)
                self.assertIn(path, push)

    def test_workflow_has_only_read_permissions_and_no_mutation_channel(self) -> None:
        source = workflow_text()
        self.assertIn("permissions:\n  contents: read\n", source)
        forbidden = (
            r"(?mi)^\s*[a-z-]+:\s*write\s*$",
            r"\$\{\{\s*secrets\.",
            r"(?mi)^\s*environment\s*:",
            r"\bSIGNING_KEYSTORE_B64\b",
            r"\bLOCAL_PROPERTIES\b",
            r"\bKEYSTORE_PASS\b",
            r"release\.keystore",
            r"\bgh\s+release\b",
            r"softprops/action-gh-release",
            r"\b(?:ssh|scp|systemctl|iptables|nft|ufw)\b",
            r"\bdocker\s+push\b",
            r"https?://",
            r"\bsed\b[^\n]*version\.properties",
        )
        for pattern in forbidden:
            with self.subTest(pattern=pattern):
                self.assertIsNone(re.search(pattern, source), pattern)

    def test_every_action_reference_is_pinned_to_a_commit(self) -> None:
        references = re.findall(r"(?m)^\s+- uses:\s+([^\s#]+)", workflow_text())
        self.assertTrue(references)
        for reference in references:
            with self.subTest(reference=reference):
                self.assertRegex(reference, r"^[^@]+@[0-9a-f]{40}$")


class WorkflowGateContractTest(unittest.TestCase):
    def test_workflow_has_exactly_the_five_isolated_jobs(self) -> None:
        jobs = workflow_text().split("jobs:\n", 1)[1]
        names = set(re.findall(r"(?m)^  ([a-z0-9][a-z0-9-]*):\n", jobs))
        self.assertEqual(EXPECTED_JOBS, names)

    def test_format_unit_job_runs_go_python_docs_and_diff_checks(self) -> None:
        source = job_source(workflow_text(), "format-unit")
        self.assertIn("gofmt -d", source)
        self.assertIn("go test -count=1", source)
        for package in GO_PACKAGES:
            self.assertIn(package, source)
        for module in (
            "scripts.tests.test_yandex_cdn_docs",
            "scripts.tests.test_yandex_cdn_repro",
            "scripts.tests.test_yandex_cdn_ci",
        ):
            self.assertIn(module, source)
        self.assertIn("python scripts/validate_yandex_cdn_docs.py", source)
        self.assertIn("git diff --check", source)

    def test_race_vet_job_is_separate_and_covers_every_go_package(self) -> None:
        source = job_source(workflow_text(), "race-vet")
        self.assertIn("needs: format-unit", source)
        self.assertIn("go test -count=1 -race", source)
        self.assertIn("go vet", source)
        for package in GO_PACKAGES:
            self.assertGreaterEqual(source.count(package), 2, package)

    def test_offline_replay_runs_exact_wrappers_and_parses_pass_no_go_json(self) -> None:
        source = job_source(workflow_text(), "offline-replay")
        self.assertIn("needs: format-unit", source)
        self.assertIn("GOPROXY: \"off\"", source)
        self.assertIn("set -euo pipefail", source)
        self.assertIn("scripts/repro/_run-white-list-suite.sh", source)
        self.assertIn("bash -n", source)
        for wrapper in WRAPPERS:
            with self.subTest(wrapper=wrapper):
                self.assertEqual(1, source.count(wrapper))
        self.assertIn("json.loads(sys.stdin.read())", source)
        self.assertIn('payload["harness_status"] != "PASS"', source)
        self.assertIn('payload["release_readiness"] != "NO_GO"', source)
        self.assertIsNone(re.search(r"\b(?:curl|wget|gh)\b", source))

    def test_rqlite_purge_is_targeted_isolated_and_always_cleaned_up(self) -> None:
        source = job_source(workflow_text(), "rqlite-purge")
        self.assertTrue(source, "missing rqlite-purge job")
        self.assertIn("needs: format-unit", source)
        self.assertIn("bash ops/ha/test-ci-rqlite-cluster.sh", source)
        self.assertIn("bash ops/ha/ci-rqlite-cluster.sh start", source)
        self.assertIn("-tags=rqlite_integration", source)
        self.assertIn("./internal/controlplane", source)
        self.assertIn("^TestWhiteListIdentityDoesNotBlockTombstonePurge$", source)
        self.assertNotIn("./...", source)
        cleanup = source.find("if: ${{ always() }}")
        stop = source.find("bash ops/ha/ci-rqlite-cluster.sh stop")
        self.assertGreaterEqual(cleanup, 0)
        self.assertGreaterEqual(stop, 0)
        self.assertLess(cleanup, stop)

    def test_android_build_waits_for_every_backend_and_replay_gate(self) -> None:
        source = job_source(workflow_text(), "android-test-apk")
        for dependency in ("format-unit", "race-vet", "offline-replay", "rqlite-purge"):
            self.assertIn(f"- {dependency}", source)
        self.assertIn("contents: read", source)
        self.assertIn("actions: read", source)

    def test_android_build_is_single_attempt_test_only_and_candidate_bound(self) -> None:
        source = job_source(workflow_text(), "android-test-apk")
        self.assertEqual(1, source.count("./gradlew"))
        self.assertNotRegex(source, r"\b(?:until|retry|sleep)\b")
        self.assertIn(":app:assembleOtherDebug", source)
        self.assertIn("-PmaestroTask7TestVersionName=1.0.158-task7-test", source)
        self.assertIn("-PmaestroTask7TestVersionCode=1015800", source)
        self.assertIn("com.maestrovpn.tv", source)
        self.assertIn("apkanalyzer", source)
        self.assertIn("apksigner", source)
        self.assertIn("Android Debug", source)
        self.assertIn("sha256sum", source)
        self.assertIn("actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02", source)
        self.assertIn("github.sha", source)
        self.assertNotIn("continue-on-error", source)

    def test_android_build_uses_the_exact_libbox_artifact_and_both_hashes(self) -> None:
        source = job_source(workflow_text(), "android-test-apk")
        for literal in (
            "31758418005",
            "9203846250",
            "baf1ae39b845601ef5291c92c9638261f4a9dfd7725d04ffd856431d48d42b37",
            "c70ce917b331fd333d52b8e3c01eba2c2a343497f896fdf62c30436490a88f05",
        ):
            self.assertIn(literal, source)
        self.assertNotRegex(source, r"(?i)latest\s+successful|gh\s+run\s+list")
        self.assertIn("workflow_run", source)
        self.assertIn("expired", source)


class GradleTask7VersionContractTest(unittest.TestCase):
    def test_override_allows_generic_defaults_and_enforces_task7_identity(self) -> None:
        source = gradle_text()
        source_defaults = (REPO_ROOT / "version.properties").read_text(encoding="utf-8")
        self.assertIn("VERSION_NAME=0.1.0", source_defaults)
        self.assertIn("VERSION_CODE=1", source_defaults)
        self.assertIn('gradleProperty("maestroTask7TestVersionName")', source)
        self.assertIn('gradleProperty("maestroTask7TestVersionCode")', source)
        self.assertIn("task7ProductionBaselineVersionCode = 157", source)
        self.assertIn('task7ProductionBaselineVersionName = "1.0.157"', source)
        self.assertIn("toIntOrNull()", source)
        self.assertIn("length in 1..64", source)
        self.assertIn("-task7-test", source)
        self.assertNotIn("productionVersionName == task7ProductionBaselineVersionName", source)
        self.assertNotIn("productionVersionCode == task7ProductionBaselineVersionCode", source)
        self.assertIn('task7TestVersionName == "1.0.158-task7-test"', source)
        self.assertIn("task7TestVersionCodeValue == 1015800", source)
        self.assertIn("task7TestVersionName != productionVersionName", source)
        self.assertIn("task7TestVersionName != task7ProductionBaselineVersionName", source)
        self.assertIn("task7TestVersionCodeValue > productionVersionCode", source)
        self.assertIn("task7TestVersionCodeValue > task7ProductionBaselineVersionCode", source)
        self.assertIn("task7TestVersionNameProperty ?: productionVersionName", source)
        self.assertIn("task7TestVersionCode ?: productionVersionCode", source)

    def test_task7_override_fails_closed_when_release_signing_is_configured(self) -> None:
        source = gradle_text()
        self.assertIn("releaseSigningConfigured", source)
        self.assertIn("task7TestOverrideActive", source)
        self.assertIn("require(!releaseSigningConfigured)", source)
        self.assertIn("Task 7 test APK must not use release signing", source)


if __name__ == "__main__":
    unittest.main()
