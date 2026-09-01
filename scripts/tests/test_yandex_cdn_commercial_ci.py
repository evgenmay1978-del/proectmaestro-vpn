from __future__ import annotations

import re
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
PLAN = REPO_ROOT / "docs" / "superpowers" / "plans" / "2026-09-01-maestrovpn-whitelist-commercial-delivery.md"
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "yandex-cdn-release.yml"
WRAPPERS = (
    REPO_ROOT / "ops" / "validate-yandex-cdn-release.sh",
    REPO_ROOT / "ops" / "validate-yandex-cdn-release.ps1",
)

VERDICTS = (
    "PUBLISHABLE",
    "NO_ENTITLEMENT",
    "PRIMARY_EXPIRED",
    "NO_BALANCE",
    "PROJECTION_STALE",
    "PROJECTION_PENDING",
    "RELEASE_MISMATCH",
    "SIDECAR_UNAVAILABLE",
)
REQUIRED_WORKFLOW_PATHS = (
    "backend/internal/api/**",
    "backend/internal/controlplane/**",
    "backend/internal/subgen/**",
    "backend/internal/whitelistbalance/**",
    "sidecar-agent/**",
    "deploy/vpn_bot_maestro_*.py",
    "docs/superpowers/plans/2026-09-01-maestrovpn-whitelist-commercial-delivery.md",
)
REQUIRED_GO_PACKAGES = (
    "./internal/api",
    "./internal/controlplane",
    "./internal/subgen",
    "./internal/whitelistbalance",
)
REQUIRED_PYTHON_PATH = "deploy/vpn_bot_maestro_*.py"
ANDROID_TEST_APK_GATE = "android-test-apk"


class CommercialDeliveryContractPolicyTest(unittest.TestCase):
    def test_plan_freezes_commercial_contract_inventory(self) -> None:
        plan = PLAN.read_text(encoding="utf-8")

        self.assertIn("type WhiteListPublicationVerdict string", plan)
        for verdict in VERDICTS:
            self.assertIn(f'"{verdict}"', plan)
        self.assertIn("const GBDecimal int64 = 1_000_000_000", plan)
        self.assertIn("whitelist_billing_periods", plan)
        self.assertIn("whitelist_balance_entries", plan)
        self.assertIn("whitelist_balance_projections", plan)
        for product_row in (
            "`wl-gb-5-v1` | `WHITELIST_BYTES` | 10000 | 5000000000",
            "`wl-gb-20-v1` | `WHITELIST_BYTES` | 30000 | 20000000000",
            "`wl-gb-50-v1` | `WHITELIST_BYTES` | 60000 | 50000000000",
            "`wl-gb-100-v1` | `WHITELIST_BYTES` | 100000 | 100000000000",
        ):
            self.assertIn(product_row, plan)
        self.assertIn("DesiredGeneration", plan)
        self.assertIn("readiness receipt containing", plan)
        for receipt_field in (
            "origin_id",
            "xray_process_boot_id",
            "config_digest",
            "desired_generation",
            "managed_user_set_digest",
            "applied_at",
            "expires_at",
        ):
            self.assertIn(receipt_field, plan)
        for task, path in (
            (3, "backend/internal/api/controlplane_whitelist_publication_test.go"),
            (5, "backend/internal/whitelistbalance/model_test.go"),
            (7, "backend/internal/controlplane/whitelist_topup_orders_test.go"),
            (11, "backend/internal/controlplane/whitelist_sidecar_receipt_test.go"),
        ):
            self.assertRegex(
                plan,
                re.compile(rf"## Task {task}:.*?{re.escape(path)}", re.DOTALL),
            )

    def test_release_ci_watches_commercial_paths_and_keeps_android_gate(self) -> None:
        workflow = WORKFLOW.read_text(encoding="utf-8")
        for event in ("pull_request", "push"):
            match = re.search(
                rf"  {event}:\n    paths:\n(?P<paths>(?:      - .+\n)+)", workflow
            )
            self.assertIsNotNone(match, event)
            paths = match.group("paths") if match else ""
            for path in REQUIRED_WORKFLOW_PATHS:
                self.assertIn(f"      - '{path}'", paths)
        self.assertIn(ANDROID_TEST_APK_GATE, workflow)

    def test_release_wrappers_cover_commercial_go_and_python_scope(self) -> None:
        for wrapper in WRAPPERS:
            source = wrapper.read_text(encoding="utf-8")
            for package in REQUIRED_GO_PACKAGES:
                self.assertIn(package, source, wrapper.name)
            self.assertIn("vpn_bot_maestro_*.py", source, wrapper.name)


if __name__ == "__main__":
    unittest.main()
