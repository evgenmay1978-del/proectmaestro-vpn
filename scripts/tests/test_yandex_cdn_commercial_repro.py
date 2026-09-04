from __future__ import annotations

import functools
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
REPRO_DIR = REPO_ROOT / "scripts" / "repro"
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "yandex-cdn-release.yml"
WRAPPERS = (
    REPO_ROOT / "ops" / "validate-yandex-cdn-release.sh",
    REPO_ROOT / "ops" / "validate-yandex-cdn-release.ps1",
)
SCRIPTS = {
    "whitelist-commercial-balance.sh": {
        "workdir": "backend",
        "commands": (
            "test -mod=readonly -count=1 ./internal/whitelistbalance -run "
            "^(TestAdvanceUsesHalfOpenBoundaryAndExpiresOnlyUnusedIncluded|"
            "TestApplyUsageDebitsOldPeriodBeforeBoundaryRollover)$",
            "test -mod=readonly -count=1 ./internal/controlplane -run "
            "^(TestConfirmWhiteListTopUpPaymentCreditsOnceAndEnablesPublication|"
            "TestWhiteListTopUpConfirmationCommitsOnceAndReplaysAfterUnknownOutcome)$",
            "test -mod=readonly -count=1 ./internal/whitelistready -run "
            "^TestIntegrationFixtureCompositionShadowMeteringKeysResetReplay$",
        ),
        "stdout": (
            '{"fixture":"whitelist-commercial-balance","harness_status":"PASS",'
            '"proofs":5,"evidence_class":"OFFLINE_REPRO",'
            '"release_readiness":"NO_GO"}\n'
        ),
    },
    "whitelist-publication-cache.sh": {
        "workdir": "backend",
        "commands": (
            "test -mod=readonly -count=1 ./internal/api -run "
            "^(TestControlPlaneSubscription10157BareGoldenDoesNotAugment|"
            "TestTask3PublicationAfterOrdinaryCacheCannotResurrectClosedNode)$",
            "test -mod=readonly -count=1 ./internal/controlplane -run "
            "^(TestReconcileWhiteListSidecarGenerationCoversEveryActiveOriginBeforeReady|"
            "TestBuildWhiteListRouteMatrixUsesOnlyExitCountryMetadata|"
            "TestBuildWhiteListSidecarDesiredChangesOnlyManagedIdentityAndBumpsEveryOrigin|"
            "TestResolveWhiteListSidecarUnknownReadsExactReceiptWithoutWrite)$",
        ),
        "stdout": (
            '{"fixture":"whitelist-publication-cache","harness_status":"PASS",'
            '"proofs":6,"evidence_class":"OFFLINE_REPRO",'
            '"release_readiness":"NO_GO"}\n'
        ),
    },
    "whitelist-sidecar-reconcile.sh": {
        "workdir": "sidecar-agent",
        "commands": (
            "test -mod=readonly -count=1 ./cmd/maestro-xray-cdn-agent -run "
            "^TestWriteXrayPIDFileReplacesRestartIdentity$",
            "test -mod=readonly -count=1 ./internal/agent -run "
            "^(TestReconcileConvergesExactManagedSetAddsBeforeRemovalsAndPreservesStaticUsers|"
            "TestReceiptExpiresAndRefreshRecoversAfterProcessRestart)$",
        ),
        "stdout": (
            '{"fixture":"whitelist-sidecar-reconcile","harness_status":"PASS",'
            '"proofs":3,"evidence_class":"OFFLINE_REPRO",'
            '"release_readiness":"NO_GO"}\n'
        ),
    },
}


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
    return bash if probe.returncode == 0 else None


class CommercialReproTest(unittest.TestCase):
    def test_release_gates_wire_the_task14_proofs(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        for marker in (
            "scripts/repro/whitelist-commercial-balance.sh",
            "scripts/repro/whitelist-publication-cache.sh",
            "scripts/repro/whitelist-sidecar-reconcile.sh",
            "scripts.tests.test_yandex_cdn_commercial_ci",
            "scripts.tests.test_yandex_cdn_commercial_repro",
            "python -m unittest discover -s deploy/tests -p 'test_vpn_bot_maestro_*.py'",
            "scripts.tests.test_yandex_cdn_ci",
            "go-version-file: sidecar-agent/go.mod",
            "go test -race -count=1 ./...",
            "go vet ./...",
            "1.0.158-task7-test",
        ):
            self.assertIn(marker, workflow, marker)
        self.assertRegex(
            workflow,
            r"(?s)  commercial-sidecar-agent:.*?go-version-file: "
            r"sidecar-agent/go\.mod.*?whitelist-sidecar-reconcile\.sh.*?"
            r"go test -race -count=1 \./\.\.\..*?go vet \./\.\.\.",
        )

        for wrapper in WRAPPERS:
            source = wrapper.read_text(encoding="utf-8")
            self.assertIn("test_vpn_bot_maestro_*.py", source, wrapper.name)
            self.assertIn("unittest", source, wrapper.name)

    def test_required_repros_are_present(self) -> None:
        for filename in SCRIPTS:
            with self.subTest(filename=filename):
                self.assertTrue((REPRO_DIR / filename).is_file())

    def test_repros_execute_only_the_exact_offline_proofs(self) -> None:
        bash = usable_bash()
        if bash is None:
            self.skipTest("bash is not installed")

        with tempfile.TemporaryDirectory() as raw_temp:
            temp = Path(raw_temp)
            go_log = temp / "go.log"
            fake_go = temp / "go"
            fake_go.write_text(
                "#!/bin/sh\n"
                "printf '%s\\t%s\\t%s\\t%s\\n' \"$PWD\" \"$GOPROXY\" "
                "\"$GOSUMDB\" \"$*\" >> \"$MAESTRO_GO_LOG\"\n",
                encoding="utf-8",
                newline="\n",
            )
            fake_go.chmod(0o755)

            environment = os.environ.copy()
            environment["PATH"] = str(temp)
            environment["MAESTRO_GO_LOG"] = str(go_log)

            for filename, contract in SCRIPTS.items():
                go_log.write_text("", encoding="utf-8")
                completed = subprocess.run(
                    [bash, str(REPRO_DIR / filename)],
                    cwd=REPO_ROOT,
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=30,
                    env=environment,
                )
                with self.subTest(filename=filename):
                    self.assertEqual(0, completed.returncode, completed.stderr)
                    self.assertEqual(contract["stdout"], completed.stdout)
                    self.assertEqual("", completed.stderr)
                    rows = go_log.read_text(encoding="utf-8").splitlines()
                    self.assertEqual(len(contract["commands"]), len(rows))
                    for row, expected_command in zip(rows, contract["commands"]):
                        cwd, proxy, sumdb, command = row.split("\t", 3)
                        normalized_cwd = cwd.replace("\\", "/")
                        self.assertTrue(
                            normalized_cwd.endswith(f"/{contract['workdir']}")
                        )
                        self.assertEqual("off", proxy)
                        self.assertEqual("off", sumdb)
                        self.assertEqual(expected_command, command)

    def test_repros_reject_arguments_before_running_go(self) -> None:
        bash = usable_bash()
        if bash is None:
            self.skipTest("bash is not installed")
        for filename in SCRIPTS:
            completed = subprocess.run(
                [bash, str(REPRO_DIR / filename), "unexpected"],
                cwd=REPO_ROOT,
                check=False,
                capture_output=True,
                text=True,
            )
            with self.subTest(filename=filename):
                self.assertEqual(2, completed.returncode)
                self.assertEqual("", completed.stdout)
                self.assertEqual('{"error":"arguments_invalid"}\n', completed.stderr)


if __name__ == "__main__":
    unittest.main()
