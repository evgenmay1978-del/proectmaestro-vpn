from __future__ import annotations

import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "sidecar-agent.yml"
SERVICE = ROOT / "deploy" / "maestro-xray-cdn-agent.service"
ENVIRONMENT = ROOT / "deploy" / "maestro-xray-cdn-agent.env.example"


class SidecarAgentWorkflowPolicyTest(unittest.TestCase):
    def test_workflow_is_read_only_pinned_and_covers_exact_task12_gates(self) -> None:
        source = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("permissions:\n  contents: read\n", source)
        self.assertNotIn("contents: write", source)
        self.assertNotIn("pull_request_target", source)
        self.assertNotIn("secrets.", source)
        self.assertIn("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683", source)
        self.assertIn("actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff", source)
        for gate in (
            "python -m unittest scripts.tests.test_sidecar_agent_ci -v",
            "test -z \"$(gofmt -l .)\"",
            "go test -count=1 ./...",
            "go test -race -count=1 ./...",
            "go vet ./...",
            "go test -count=1 ./internal/release",
        ):
            self.assertIn(gate, source)

    def test_workflow_triggers_on_every_task12_surface(self) -> None:
        source = WORKFLOW.read_text(encoding="utf-8")
        for path in (
            "sidecar-agent/**",
            "backend/internal/release/templates.go",
            "backend/internal/release/handler_service_test.go",
            "deploy/maestro-xray-cdn-agent.service",
            "deploy/maestro-xray-cdn-agent.env.example",
            "scripts/tests/test_sidecar_agent_ci.py",
        ):
            self.assertGreaterEqual(source.count(path), 2, path)

    def test_systemd_unit_is_dedicated_sandboxed_and_has_no_shell(self) -> None:
        source = SERVICE.read_text(encoding="utf-8")
        for directive in (
            "User=maestro-xray-cdn-agent",
            "Group=maestro-xray-cdn-agent",
            "StateDirectory=maestro-xray-cdn-agent/receipts",
            "StateDirectoryMode=0700",
            "NoNewPrivileges=true",
            "PrivateDevices=true",
            "ProtectSystem=strict",
            "ReadOnlyPaths=/etc/maestro-xray-cdn-agent /etc/maestro-xray-cdn/api-mtls /var/lib/maestro-xray-cdn-agent/credentials",
            "ReadWritePaths=/var/lib/maestro-xray-cdn-agent/receipts",
        ):
            self.assertIn(directive, source)
        self.assertIn("ExecStart=/opt/maestro-xray-cdn-agent/current/maestro-xray-cdn-agent", source)
        self.assertNotIn("/bin/sh", source)
        self.assertNotIn("/bin/bash", source)

    def test_deployment_contract_wires_live_relay_preflight(self) -> None:
        service = SERVICE.read_text(encoding="utf-8")
        environment = ENVIRONMENT.read_text(encoding="utf-8")
        for directive in (
            "AmbientCapabilities=CAP_NET_ADMIN",
            "CapabilityBoundingSet=CAP_NET_ADMIN",
            "/etc/maestro-xray-cdn-agent/active-origin-ips.json",
            "/etc/maestro-xray-cdn-agent/controller-source-ip.json",
            "/etc/maestro-xray-cdn-agent/relay-ca",
            "/var/lib/maestro-xray-cdn-agent/relay-credentials",
            "/run/maestro-xray-cdn/config.json",
            "/run/maestro-xray-cdn-pid/xray.pid",
        ):
            self.assertIn(directive, service)
        for setting in (
            "MAESTRO_ACTIVE_ORIGIN_IPS_FILE=/etc/maestro-xray-cdn-agent/active-origin-ips.json",
            "MAESTRO_CONTROLLER_SOURCE_IP_FILE=/etc/maestro-xray-cdn-agent/controller-source-ip.json",
            "MAESTRO_RELAY_CA_DIRECTORY=/etc/maestro-xray-cdn-agent/relay-ca",
            "MAESTRO_RELAY_CREDENTIAL_DIRECTORY=/var/lib/maestro-xray-cdn-agent/relay-credentials",
            "MAESTRO_XRAY_CONFIG_FILE=/run/maestro-xray-cdn/config.json",
            "MAESTRO_NFT_BINARY=/usr/sbin/nft",
            "MAESTRO_XRAY_PID_FILE=/run/maestro-xray-cdn-pid/xray.pid",
        ):
            self.assertIn(setting, environment)
        self.assertNotIn("credential=", environment.lower())

        self.assertIn("PartOf=maestro-xray-cdn.service", service)
        self.assertIn("WantedBy=multi-user.target maestro-xray-cdn.service", service)


if __name__ == "__main__":
    unittest.main()
