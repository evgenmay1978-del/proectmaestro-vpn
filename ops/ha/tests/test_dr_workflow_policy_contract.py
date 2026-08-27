from __future__ import annotations

import importlib.util
import sys
import unittest
from unittest import mock
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]
POLICY_PATH = ROOT / "ops" / "ha" / "test-dr-workflow-policy.py"
sys.path.insert(0, str(POLICY_PATH.parent))
SPEC = importlib.util.spec_from_file_location("dr_workflow_policy", POLICY_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cannot load DR workflow policy")
POLICY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(POLICY)


class DRWorkflowPolicyContractTests(unittest.TestCase):
    def setUp(self) -> None:
        self.control = POLICY.CONTROL_WORKFLOW.read_text(encoding="utf-8")
        self.dr = POLICY.WORKFLOW.read_text(encoding="utf-8")

    def workflow_cases(self) -> tuple[tuple[str, str, str], ...]:
        return (
            ("control", self.control, "Check Go formatting"),
            ("DR", self.dr, "Check Go formatting and shell syntax"),
        )

    def test_current_workflows_pass_full_policy(self) -> None:
        with mock.patch.object(POLICY, "assert_agent_payload_policy", return_value=0):
            self.assertEqual(0, POLICY.main())

    def test_commented_task10_path_filter_is_rejected(self) -> None:
        active = "      - 'backend/internal/backuprpo/**'"
        commented = "      # - 'backend/internal/backuprpo/**'"
        mutated = self.control.replace(active, commented, 1)
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_task10_contract(mutated, "control", "Check Go formatting")

    def test_commented_policy_command_is_rejected(self) -> None:
        active = "          python ops/ha/test-backup-systemd-policy.py"
        commented = "          # python ops/ha/test-backup-systemd-policy.py"
        mutated = self.control.replace(active, commented, 1)
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_task10_contract(mutated, "control", "Check Go formatting")

    def test_named_backend_gate_requires_an_active_command(self) -> None:
        active = (
            "        run: env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 ./..."
        )
        commented = (
            "        run: |\n"
            "          # env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 ./..."
        )
        mutated = self.control.replace(active, commented, 1)
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_task10_contract(mutated, "control", "Check Go formatting")

    def test_common_safety_rejects_unpinned_control_action(self) -> None:
        self.assertTrue(hasattr(POLICY, "assert_workflow_safety"))
        mutated = self.control.replace(
            "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
            "actions/checkout@v4",
            1,
        )
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_workflow_safety(mutated, "control")

    def test_common_safety_rejects_write_all_permissions(self) -> None:
        self.assertTrue(hasattr(POLICY, "assert_workflow_safety"))
        mutated = self.control.replace(
            "permissions:\n  contents: read\n",
            "permissions: write-all\n",
            1,
        )
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_workflow_safety(mutated, "control")

    def test_inline_comment_cannot_prove_backend_gate(self) -> None:
        command = (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 ./..."
        )
        for label, source, format_step in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace(
                    f"        run: {command}",
                    f"        run: true # {command}",
                    1,
                )
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_task10_contract(mutated, label, format_step)

    def test_heredoc_cannot_prove_backend_gate(self) -> None:
        command = (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 ./..."
        )
        replacement = (
            "        run: |\n"
            "          cat <<'EOF'\n"
            f"          {command}\n"
            "          EOF"
        )
        for label, source, format_step in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace(f"        run: {command}", replacement, 1)
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_task10_contract(mutated, label, format_step)

    def test_dead_branch_cannot_prove_backend_gate(self) -> None:
        command = (
            "env -u MAESTRO_S2_PASS -u MAESTRO_HY2_PASS "
            "go test -count=1 ./..."
        )
        replacement = (
            "        run: |\n"
            "          if false; then\n"
            f"            {command}\n"
            "          fi"
        )
        for label, source, format_step in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace(f"        run: {command}", replacement, 1)
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_task10_contract(mutated, label, format_step)

    def test_exit_before_control_proofs_is_rejected(self) -> None:
        active = "          python ops/ha/test-dr-workflow-policy.py\n"
        mutated = self.control.replace(active, "          exit 0\n" + active, 1)
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_task10_contract(mutated, "control", "Check Go formatting")

    def test_exit_before_dr_backup_proof_is_rejected(self) -> None:
        active = "          bash ops/ha/test-backup-rqlite.sh\n"
        mutated = self.dr.replace(active, "          exit 0\n" + active, 1)
        self.assertNotEqual(self.dr, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_task10_contract(
                mutated, "DR", "Check Go formatting and shell syntax"
            )

    def test_unterminated_outer_heredoc_cannot_prove_control_scripts(self) -> None:
        active = "          python ops/ha/test-dr-workflow-policy.py\n"
        mutated = self.control.replace(active, "          cat <<'DECOY'\n" + active, 1)
        self.assertNotEqual(self.control, mutated)
        with self.assertRaises(AssertionError):
            POLICY.assert_task10_contract(mutated, "control", "Check Go formatting")

    def test_quoted_job_permissions_are_rejected(self) -> None:
        variants = (
            '"permissions": write-all',
            "'permissions': write-all",
            '"\\u0070ermissions": write-all',
        )
        for label, source, _ in self.workflow_cases():
            for variant in variants:
                with self.subTest(workflow=label, variant=variant):
                    mutated = source.replace(
                        "    runs-on: ubuntu-24.04\n",
                        f"    runs-on: ubuntu-24.04\n    {variant}\n",
                        1,
                    )
                    self.assertNotEqual(source, mutated)
                    with self.assertRaises(AssertionError):
                        POLICY.assert_workflow_safety(mutated, label)

    def test_quoted_permissions_text_inside_run_is_ignored(self) -> None:
        for label, source, _ in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace(
                    "          set -euo pipefail\n",
                    "          set -euo pipefail\n"
                    "          printf '%s\\n' '\"permissions\": write-all' >/dev/null\n",
                    1,
                )
                self.assertNotEqual(source, mutated)
                POLICY.assert_read_only_permissions(mutated, label)


    def test_job_write_all_is_rejected(self) -> None:
        for label, source, _ in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace(
                    "    runs-on: ubuntu-24.04\n",
                    "    runs-on: ubuntu-24.04\n    permissions: write-all\n",
                    1,
                )
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_workflow_safety(mutated, label)

    def test_job_inline_write_map_is_rejected(self) -> None:
        for label, source, _ in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace(
                    "    runs-on: ubuntu-24.04\n",
                    "    runs-on: ubuntu-24.04\n"
                    "    permissions: {contents: write}\n",
                    1,
                )
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_workflow_safety(mutated, label)


    def test_required_step_execution_metadata_is_fail_closed(self) -> None:
        cases = (
            ("control", self.control, "Check Go formatting", "Check Go formatting"),
            (
                "control",
                self.control,
                "Check Go formatting",
                "Test HA Python contracts",
            ),
            (
                "control",
                self.control,
                "Check Go formatting",
                "Test durable backup adapter policy",
            ),
            ("control", self.control, "Check Go formatting", "Test backend"),
            ("control", self.control, "Check Go formatting", "Race-test backend"),
            ("control", self.control, "Check Go formatting", "Vet backend"),
            (
                "DR",
                self.dr,
                "Check Go formatting and shell syntax",
                "Check Go formatting and shell syntax",
            ),
            (
                "DR",
                self.dr,
                "Check Go formatting and shell syntax",
                "Test durable backup adapter policy",
            ),
            (
                "DR",
                self.dr,
                "Check Go formatting and shell syntax",
                "Test authenticated backup verification and tamper matrix",
            ),
            (
                "DR",
                self.dr,
                "Check Go formatting and shell syntax",
                "Test fresh restore fencing parity and quorum",
            ),
            ("DR", self.dr, "Check Go formatting and shell syntax", "Test backend"),
            (
                "DR",
                self.dr,
                "Check Go formatting and shell syntax",
                "Race-test backend",
            ),
            ("DR", self.dr, "Check Go formatting and shell syntax", "Vet backend"),
        )
        variants = (
            "        if: ${{ false }}\n",
            "        continue-on-error: true\n",
            "        shell: /bin/true {0}\n",
        )
        for label, source, format_step, step_name in cases:
            marker = f"      - name: {step_name}\n"
            for variant in variants:
                with self.subTest(
                    workflow=label, step=step_name, variant=variant.strip()
                ):
                    mutated = source.replace(marker, marker + variant, 1)
                    self.assertNotEqual(source, mutated)
                    with self.assertRaises(AssertionError):
                        POLICY.assert_task10_contract(mutated, label, format_step)

    def test_required_job_execution_metadata_is_fail_closed(self) -> None:
        variants = (
            "    if: ${{ false }}\n",
            "    continue-on-error: true\n",
            "    env:\n      BASH_ENV: /tmp/noop\n",
            "    defaults:\n      run:\n        shell: /bin/true {0}\n",
        )
        for label, source, _ in self.workflow_cases():
            marker = "    runs-on: ubuntu-24.04\n"
            for variant in variants:
                with self.subTest(workflow=label, variant=variant.strip()):
                    mutated = source.replace(marker, marker + variant, 1)
                    self.assertNotEqual(source, mutated)
                    with self.assertRaises(AssertionError):
                        POLICY.assert_workflow_safety(mutated, label)

    def test_workflow_defaults_run_shell_is_rejected(self) -> None:
        addition = "defaults:\n  run:\n    shell: /bin/true {0}\n"
        for label, source, _ in self.workflow_cases():
            with self.subTest(workflow=label):
                mutated = source.replace("jobs:\n", addition + "jobs:\n", 1)
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_workflow_safety(mutated, label)

    def test_alias_job_permissions_key_is_rejected(self) -> None:
        for label, source, _ in self.workflow_cases():
            with self.subTest(workflow=label):
                first_line = source.splitlines()[0] + "\n"
                mutated = source.replace(
                    first_line, "name: &perm permissions\n", 1
                ).replace(
                    "    runs-on: ubuntu-24.04\n",
                    "    runs-on: ubuntu-24.04\n    *perm: write-all\n",
                    1,
                )
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_read_only_permissions(mutated, label)


    def test_control_rqlite_integration_metadata_is_fail_closed(self) -> None:
        marker = "      - name: Test rqlite integration\n"
        variants = (
            "        if: ${{ false }}\n",
            "        continue-on-error: true\n",
            "        shell: /bin/true {0}\n",
        )
        for variant in variants:
            with self.subTest(variant=variant.strip()):
                mutated = self.control.replace(marker, marker + variant, 1)
                self.assertNotEqual(self.control, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_task10_contract(
                        mutated, "control", "Check Go formatting"
                    )

    def test_extra_preproof_environment_step_is_rejected(self) -> None:
        addition = (
            "      - name: Poison execution environment\n"
            "        run: echo 'BASH_ENV=/tmp/noop' >> \"$GITHUB_ENV\"\n\n"
        )
        cases = (
            (
                "control",
                self.control,
                "Check Go formatting",
                "      - name: Test HA Python contracts\n",
            ),
            (
                "DR",
                self.dr,
                "Check Go formatting and shell syntax",
                "      - name: Test authenticated backup verification and tamper matrix\n",
            ),
        )
        for label, source, format_step, marker in cases:
            with self.subTest(workflow=label):
                mutated = source.replace(marker, addition + marker, 1)
                self.assertNotEqual(source, mutated)
                with self.assertRaises(AssertionError):
                    POLICY.assert_task10_contract(mutated, label, format_step)


if __name__ == "__main__":
    unittest.main()
