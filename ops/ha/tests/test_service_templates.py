from __future__ import annotations

import ipaddress
import json
import re
import shlex
import unittest
from pathlib import Path
from urllib.parse import urlsplit


ROOT = Path(__file__).resolve().parents[3]
DEPLOY = ROOT / "deploy" / "ha"
PANEL_SOURCE_SHA = "f577c67ad229fe89278430d35a3ec65f6ce454e5"
RQLITED = "/opt/maestro/ha/rqlite/10.1.0/rqlited"
PANEL = f"/opt/maestro/ha/releases/{PANEL_SOURCE_SHA}/maestro-panel"

RQLITE_ENV_KEYS = {
    "RQLITE_HTTP_ADDR",
    "RQLITE_HTTP_ADV_ADDR",
    "RQLITE_HTTP_CA_CERT",
    "RQLITE_HTTP_CERT",
    "RQLITE_HTTP_KEY",
    "RQLITE_RAFT_ADDR",
    "RQLITE_RAFT_ADV_ADDR",
    "RQLITE_RAFT_CA_CERT",
    "RQLITE_RAFT_CERT",
    "RQLITE_RAFT_KEY",
}
PANEL_ENV_KEYS = {
    "MAESTRO_LISTEN",
    "MAESTRO_CONTROL_PLANE",
    "MAESTRO_RQLITE_ENDPOINTS",
    "MAESTRO_RQLITE_CA_FILE",
    "MAESTRO_RQLITE_CERT_FILE",
    "MAESTRO_RQLITE_KEY_FILE",
    "MAESTRO_RQLITE_KEY_BUNDLE_FILE",
    "MAESTRO_REPORT_DIR",
}
STRING_FLAGS = {
    "-node-id",
    "-http-addr",
    "-http-adv-addr",
    "-http-ca-cert",
    "-http-cert",
    "-http-key",
    "-raft-addr",
    "-raft-adv-addr",
    "-node-ca-cert",
    "-node-cert",
    "-node-key",
}
BOOL_FLAGS = {"-fk", "-http-verify-client", "-node-verify-client"}


class UnitFile:
    def __init__(self, path: Path) -> None:
        self.path = path
        raw = _bounded_text(path)
        self.sections: dict[str, dict[str, list[str]]] = {}
        section: str | None = None
        for line_number, raw_line in enumerate(raw.splitlines(), 1):
            line = raw_line.strip()
            if not line or line.startswith(("#", ";")):
                continue
            if line.startswith("[") and line.endswith("]"):
                section = line[1:-1]
                if not re.fullmatch(r"[A-Za-z][A-Za-z0-9]*", section):
                    raise AssertionError(f"invalid section at line {line_number}")
                if section in self.sections:
                    raise AssertionError(f"duplicate section {section}")
                self.sections[section] = {}
                continue
            if section is None or "=" not in line:
                raise AssertionError(f"invalid directive at line {line_number}")
            key, value = line.split("=", 1)
            if not re.fullmatch(r"[A-Za-z][A-Za-z0-9]*", key):
                raise AssertionError(f"invalid key at line {line_number}")
            self.sections[section].setdefault(key, []).append(value)

    def one(self, section: str, key: str) -> str:
        values = self.sections.get(section, {}).get(key)
        if values is None or len(values) != 1:
            raise AssertionError(f"{self.path.name}: expected one {section}.{key}")
        return values[0]


def _bounded_text(path: Path, *, universal_newlines: bool = False) -> str:
    if not path.is_file():
        raise AssertionError(f"required regular file missing: {path.relative_to(ROOT)}")
    raw = path.read_bytes()
    if not raw or len(raw) > 64 * 1024:
        raise AssertionError(f"invalid template size: {path.name}")
    text = raw.decode("utf-8", "strict")
    if text.startswith("\ufeff") or "\x00" in text:
        raise AssertionError(f"invalid template encoding: {path.name}")
    if universal_newlines:
        normalized = text.replace("\r\n", "\n")
        if "\r" in normalized:
            raise AssertionError(f"invalid template encoding: {path.name}")
        return normalized
    if "\r" in text:
        raise AssertionError(f"invalid template encoding: {path.name}")
    return text


def _env(path: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    for line_number, raw_line in enumerate(_bounded_text(path).splitlines(), 1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise AssertionError(f"invalid env line {line_number} in {path.name}")
        key, value = line.split("=", 1)
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or not value:
            raise AssertionError(f"invalid env assignment at line {line_number}")
        if key in result:
            raise AssertionError(f"duplicate env key {key}")
        if value != value.strip() or any(character.isspace() for character in value):
            raise AssertionError(f"unquoted whitespace in {key}")
        result[key] = value
    return result


def _expanded_exec(unit: UnitFile, environment: dict[str, str]) -> list[str]:
    tokens = shlex.split(unit.one("Service", "ExecStart"), posix=True)
    expanded: list[str] = []
    for token in tokens:
        match = re.fullmatch(r"\$[{]([A-Z][A-Z0-9_]*)[}]", token)
        if match:
            key = match.group(1)
            if key not in environment:
                raise AssertionError(f"ExecStart references unknown environment key {key}")
            expanded.append(environment[key])
        else:
            if "$" in token:
                raise AssertionError("partial or unbraced environment expansion")
            expanded.append(token)
    return expanded


def _rqlite_arguments(argv: list[str]) -> tuple[dict[str, str], set[str], str]:
    if not argv or argv[0] != RQLITED:
        raise AssertionError("rqlited binary is not the pinned 10.1.0 path")
    strings: dict[str, str] = {}
    booleans: set[str] = set()
    index = 1
    while index < len(argv):
        token = argv[index]
        if token in BOOL_FLAGS:
            if token in booleans:
                raise AssertionError(f"duplicate boolean flag {token}")
            booleans.add(token)
            index += 1
            continue
        if token in STRING_FLAGS:
            if token in strings or index + 1 >= len(argv):
                raise AssertionError(f"invalid string flag {token}")
            strings[token] = argv[index + 1]
            index += 2
            continue
        if token.startswith("-"):
            raise AssertionError(f"unknown rqlited flag {token}")
        if index != len(argv) - 1:
            raise AssertionError("rqlited data directory is not the final argument")
        return strings, booleans, token
    raise AssertionError("rqlited data directory is missing")


def _assert_hardened(test: unittest.TestCase, unit: UnitFile, user: str) -> None:
    test.assertEqual(set(unit.sections), {"Unit", "Service"})
    expected = {
        "Type": "simple",
        "User": user,
        "Group": user,
        "UMask": "0077",
        "Restart": "on-failure",
        "RestartSec": "10s",
        "TimeoutStartSec": "60s",
        "TimeoutStopSec": "30s",
        "NoNewPrivileges": "yes",
        "PrivateTmp": "yes",
        "PrivateDevices": "yes",
        "ProtectSystem": "strict",
        "ProtectHome": "yes",
        "ProtectKernelTunables": "yes",
        "ProtectKernelModules": "yes",
        "ProtectKernelLogs": "yes",
        "ProtectControlGroups": "yes",
        "ProtectClock": "yes",
        "ProtectHostname": "yes",
        "LockPersonality": "yes",
        "RestrictRealtime": "yes",
        "RestrictSUIDSGID": "yes",
        "RemoveIPC": "yes",
        "RestrictNamespaces": "yes",
        "ProtectProc": "invisible",
        "ProcSubset": "pid",
        "SystemCallArchitectures": "native",
        "CapabilityBoundingSet": "",
        "AmbientCapabilities": "",
        "RestrictAddressFamilies": "AF_UNIX AF_INET AF_INET6",
    }
    for key, value in expected.items():
        test.assertEqual(unit.one("Service", key), value, key)
    for key, value in {
        "After": "network-online.target",
        "Wants": "network-online.target",
        "StartLimitIntervalSec": "60s",
        "StartLimitBurst": "3",
    }.items():
        test.assertEqual(unit.one("Unit", key), value, key)
    expected_unit_keys = {
        "Description",
        "After",
        "Wants",
        "StartLimitIntervalSec",
        "StartLimitBurst",
        "ConditionFileIsExecutable",
        "ConditionFileNotEmpty",
        "ConditionPathIsDirectory",
    }
    expected_service_keys = set(expected) | {
        "Type",
        "EnvironmentFile",
        "WorkingDirectory",
        "ExecStart",
        "ReadOnlyPaths",
        "ReadWritePaths",
        "LimitNOFILE",
        "TasksMax",
    }
    test.assertEqual(set(unit.sections["Unit"]), expected_unit_keys)
    test.assertEqual(set(unit.sections["Service"]), expected_service_keys)
    for key, values in unit.sections["Unit"].items():
        if key != "ConditionFileNotEmpty":
            test.assertEqual(len(values), 1, key)
    for key, values in unit.sections["Service"].items():
        test.assertEqual(len(values), 1, key)
    test.assertNotIn("Install", unit.sections)


class ServiceTemplateTests(unittest.TestCase):
    def test_rqlited_template_uses_only_pinned_v1010_flag_surface(self) -> None:
        unit = UnitFile(DEPLOY / "rqlited@.service")
        self.assertEqual(unit.one("Service", "EnvironmentFile"), "/etc/maestro/ha/rqlite-%i.env")
        for node in ("s2", "s3", "s4"):
            environment = _env(DEPLOY / f"rqlite-{node}.env.example")
            strings, booleans, data_dir = _rqlite_arguments(_expanded_exec(unit, environment))
            self.assertEqual(set(strings), STRING_FLAGS)
            self.assertEqual(booleans, BOOL_FLAGS)
            self.assertEqual(strings["-node-id"], "%i")
            self.assertEqual(data_dir, "/var/lib/maestro/rqlite/%i")
            self.assertNotIn("-node-verify-server-name", strings)
            self.assertNotIn("0.0.0.0", " ".join(strings.values()))
            self.assertNotIn("[::]", " ".join(strings.values()))

    def test_rqlite_envs_have_stable_ids_and_separate_http_raft_trust(self) -> None:
        all_http_certificates: set[str] = set()
        all_raft_certificates: set[str] = set()
        for node in ("s2", "s3", "s4"):
            environment = _env(DEPLOY / f"rqlite-{node}.env.example")
            self.assertEqual(set(environment), RQLITE_ENV_KEYS)
            self.assertEqual(environment["RQLITE_HTTP_ADDR"], f"__{node.upper()}_PRIVATE_IP__:4001")
            self.assertEqual(environment["RQLITE_HTTP_ADV_ADDR"], f"{node}-rqlite-http.internal:4001")
            self.assertEqual(environment["RQLITE_RAFT_ADDR"], f"__{node.upper()}_PRIVATE_IP__:4002")
            self.assertEqual(environment["RQLITE_RAFT_ADV_ADDR"], f"{node}-rqlite-raft.internal:4002")
            self.assertEqual(environment["RQLITE_HTTP_CA_CERT"], "/etc/maestro/ha/pki/rqlite-http/ca.pem")
            self.assertEqual(environment["RQLITE_RAFT_CA_CERT"], "/etc/maestro/ha/pki/rqlite-raft/ca.pem")
            self.assertEqual(
                environment["RQLITE_HTTP_CERT"],
                f"/etc/maestro/ha/pki/rqlite-http/{node}-http-server.pem",
            )
            self.assertEqual(
                environment["RQLITE_HTTP_KEY"],
                f"/etc/maestro/ha/secrets/rqlite-http/{node}-http-server.key",
            )
            self.assertEqual(
                environment["RQLITE_RAFT_CERT"],
                f"/etc/maestro/ha/pki/rqlite-raft/{node}-raft-peer.pem",
            )
            self.assertEqual(
                environment["RQLITE_RAFT_KEY"],
                f"/etc/maestro/ha/secrets/rqlite-raft/{node}-raft-peer.key",
            )
            self.assertNotIn("RQLITE_NODE_ID", environment)
            self.assertNotIn("RQLITE_DATA_DIR", environment)
            self.assertNotIn("RQLITE_RAFT_SERVER_NAME", environment)
            all_http_certificates.add(environment["RQLITE_HTTP_CERT"])
            all_raft_certificates.add(environment["RQLITE_RAFT_CERT"])
            self.assertNotEqual(environment["RQLITE_HTTP_CA_CERT"], environment["RQLITE_RAFT_CA_CERT"])
            self.assertNotEqual(environment["RQLITE_HTTP_CERT"], environment["RQLITE_RAFT_CERT"])
            self.assertNotEqual(environment["RQLITE_HTTP_KEY"], environment["RQLITE_RAFT_KEY"])
        self.assertEqual(len(all_http_certificates), 3)
        self.assertEqual(len(all_raft_certificates), 3)

    def test_panel_template_selects_real_rqlite_runtime_and_immutable_binary(self) -> None:
        unit = UnitFile(DEPLOY / "maestro-panel.service")
        self.assertEqual(unit.one("Service", "EnvironmentFile"), "/etc/maestro/ha/maestro-panel.env")
        self.assertEqual(shlex.split(unit.one("Service", "ExecStart"), posix=True), [PANEL])
        environment = _env(DEPLOY / "maestro-panel.env.example")
        self.assertEqual(set(environment), PANEL_ENV_KEYS)
        self.assertEqual(environment["MAESTRO_CONTROL_PLANE"], "rqlite")
        self.assertEqual(environment["MAESTRO_LISTEN"], "127.0.0.1:8910")
        self.assertEqual(environment["MAESTRO_RQLITE_CA_FILE"], "/etc/maestro/ha/pki/rqlite-http/ca.pem")
        self.assertEqual(
            environment["MAESTRO_RQLITE_CERT_FILE"],
            "/etc/maestro/ha/pki/rqlite-http/__NODE_ID__-panel-rqlite-client.pem",
        )
        self.assertEqual(
            environment["MAESTRO_RQLITE_KEY_FILE"],
            "/etc/maestro/ha/secrets/rqlite-http/__NODE_ID__-panel-rqlite-client.key",
        )
        self.assertEqual(
            environment["MAESTRO_RQLITE_KEY_BUNDLE_FILE"],
            "/etc/maestro/ha/secrets/control-plane/key-bundle.json",
        )
        self.assertEqual(
            environment["MAESTRO_REPORT_DIR"],
            "/var/lib/maestro/panel/reports",
        )
        host, port = environment["MAESTRO_LISTEN"].rsplit(":", 1)
        self.assertTrue(ipaddress.ip_address(host).is_loopback)
        self.assertEqual(port, "8910")

    def test_panel_endpoints_match_all_advertised_http_addresses(self) -> None:
        panel = _env(DEPLOY / "maestro-panel.env.example")
        endpoints = panel["MAESTRO_RQLITE_ENDPOINTS"].split(",")
        self.assertEqual(len(endpoints), 3)
        self.assertEqual(len(set(endpoints)), 3)
        advertised = {
            _env(DEPLOY / f"rqlite-{node}.env.example")["RQLITE_HTTP_ADV_ADDR"]
            for node in ("s2", "s3", "s4")
        }
        parsed_addresses: set[str] = set()
        for endpoint in endpoints:
            parsed = urlsplit(endpoint)
            self.assertEqual(parsed.scheme, "https")
            self.assertIsNone(parsed.username)
            self.assertIsNone(parsed.password)
            self.assertEqual(parsed.path, "")
            self.assertEqual(parsed.query, "")
            self.assertEqual(parsed.fragment, "")
            self.assertEqual(parsed.port, 4001)
            parsed_addresses.add(parsed.netloc)
        self.assertEqual(parsed_addresses, advertised)

    def test_service_tls_names_match_offline_pki_profile(self) -> None:
        profile = json.loads(
            _bounded_text(
                DEPLOY / "pki-profile.json.example", universal_newlines=True
            )
        )
        domains = {domain["name"]: domain for domain in profile["trust_domains"]}
        self.assertIn("rqlite-http", domains)
        self.assertIn("rqlite-raft", domains)
        http_roles = {
            entry["role"]: entry
            for entry in domains["rqlite-http"]["certificates"]
        }
        raft_roles = {
            entry["role"]: entry
            for entry in domains["rqlite-raft"]["certificates"]
        }
        panel_endpoints = {
            urlsplit(endpoint).hostname: endpoint
            for endpoint in _env(DEPLOY / "maestro-panel.env.example")[
                "MAESTRO_RQLITE_ENDPOINTS"
            ].split(",")
        }
        for node in ("s2", "s3", "s4"):
            environment = _env(DEPLOY / f"rqlite-{node}.env.example")
            http_hostname = environment["RQLITE_HTTP_ADV_ADDR"].rsplit(":", 1)[0]
            self.assertIn(http_hostname, panel_endpoints)
            http_entry = http_roles[f"{node}-http-server"]
            self.assertEqual(http_entry["dns_sans"], [http_hostname])
            self.assertEqual(
                Path(http_entry["certificate"]).name,
                Path(environment["RQLITE_HTTP_CERT"]).name,
            )

            self.assertEqual(
                environment["RQLITE_RAFT_ADV_ADDR"],
                f"{node}-rqlite-raft.internal:4002",
            )
            raft_hostname = environment["RQLITE_RAFT_ADV_ADDR"].rsplit(":", 1)[0]
            raft_entry = raft_roles[f"{node}-raft-peer"]
            self.assertEqual(raft_entry["dns_sans"], [raft_hostname])
            self.assertEqual(
                Path(raft_entry["certificate"]).name,
                Path(environment["RQLITE_RAFT_CERT"]).name,
            )

    def test_rqlite_template_refuses_unprovisioned_cluster_state(self) -> None:
        unit = UnitFile(DEPLOY / "rqlited@.service")
        self.assertEqual(
            unit.one("Unit", "ConditionPathIsDirectory"),
            "/var/lib/maestro/rqlite/%i",
        )
        self.assertEqual(
            unit.sections["Unit"].get("ConditionFileNotEmpty"),
            [
                "/etc/maestro/ha/rqlite-%i.env",
                "/var/lib/maestro/rqlite/%i/raft.db",
            ],
        )
        self.assertNotIn("ExecStartPre", unit.sections.get("Service", {}))

    def test_units_use_valid_fail_closed_file_conditions(self) -> None:
        rqlite = UnitFile(DEPLOY / "rqlited@.service")
        panel = UnitFile(DEPLOY / "maestro-panel.service")
        self.assertEqual(
            rqlite.one("Unit", "ConditionFileIsExecutable"),
            RQLITED,
        )
        self.assertEqual(
            panel.one("Unit", "ConditionFileIsExecutable"),
            PANEL,
        )
        self.assertEqual(
            panel.one("Unit", "ConditionFileNotEmpty"),
            "/etc/maestro/ha/maestro-panel.env",
        )
        for unit in (rqlite, panel):
            self.assertNotIn("ConditionPathIsExecutable", unit.sections["Unit"])
            self.assertNotIn("ConditionPathIsRegular", unit.sections["Unit"])

    def test_units_are_hardened_dedicated_and_create_nothing(self) -> None:
        rqlite = UnitFile(DEPLOY / "rqlited@.service")
        panel = UnitFile(DEPLOY / "maestro-panel.service")
        _assert_hardened(self, rqlite, "maestro-rqlite")
        _assert_hardened(self, panel, "maestro-panel")
        self.assertEqual(
            rqlite.one("Service", "ReadOnlyPaths"),
            "/opt/maestro/ha/rqlite/10.1.0 /etc/maestro/ha/pki /etc/maestro/ha/secrets",
        )
        self.assertEqual(rqlite.one("Service", "ReadWritePaths"), "/var/lib/maestro/rqlite/%i")
        self.assertEqual(rqlite.one("Service", "WorkingDirectory"), "/var/lib/maestro/rqlite/%i")
        self.assertEqual(rqlite.one("Service", "LimitNOFILE"), "65536")
        self.assertEqual(rqlite.one("Service", "TasksMax"), "1024")
        self.assertEqual(
            panel.one("Service", "ReadOnlyPaths"),
            f"/opt/maestro/ha/releases/{PANEL_SOURCE_SHA} /etc/maestro/ha/pki /etc/maestro/ha/secrets",
        )
        self.assertEqual(panel.one("Service", "ReadWritePaths"), "/var/lib/maestro/panel")
        self.assertEqual(panel.one("Service", "WorkingDirectory"), "/var/lib/maestro/panel")
        self.assertEqual(panel.one("Service", "LimitNOFILE"), "65536")
        self.assertEqual(panel.one("Service", "TasksMax"), "512")

    def test_templates_have_no_shell_bootstrap_secrets_or_frozen_protocols(self) -> None:
        paths = [
            DEPLOY / "rqlited@.service",
            DEPLOY / "rqlite-s2.env.example",
            DEPLOY / "rqlite-s3.env.example",
            DEPLOY / "rqlite-s4.env.example",
            DEPLOY / "maestro-panel.service",
            DEPLOY / "maestro-panel.env.example",
        ]
        combined = "\n".join(_bounded_text(path) for path in paths)
        lowered = combined.lower()
        for forbidden in (
            "[install]",
            "/bin/sh",
            "/bin/bash",
            " sh -c",
            " bash -c",
            "$(",
            "`",
            "&&",
            "||",
            "-join",
            "-bootstrap",
            "-node-no-verify",
            "-node-verify-server-name",
            "-raft-cert",
            "-raft-key",
            "-raft-ca",
            "0.0.0.0",
            "[::]",
            "olcrtc",
            "wdtt",
        ):
            self.assertNotIn(forbidden, lowered, forbidden)
        self.assertNotRegex(combined, r"(?im)^(?:.*(?:TOKEN|PASSWORD|PASSPHRASE|PRIVATE_KEY).*)=")
        self.assertNotIn("BEGIN PRIVATE KEY", combined)
        self.assertNotIn("BEGIN RSA PRIVATE KEY", combined)


if __name__ == "__main__":
    unittest.main()
