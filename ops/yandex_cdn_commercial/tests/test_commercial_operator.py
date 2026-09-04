import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

from ops.yandex_cdn_commercial import bundle
from ops.yandex_cdn_commercial.operator import CommercialOperator, OperationError, profile_contract


FULL_SHA = "467e40af33fe7ba6f5a7957adfcbf0d9e72a2d71"
XRAY_ARCHIVE_SHA = "f56c106b7c0159ad386bccd340faa5bbf55fd5c15821ec9e63e6a6ba11d3d1c7"


class FakeSystem:
    def __init__(self) -> None:
        self.commands: list[tuple[str, ...]] = []
        self.fail_start = False

    def run(self, *command: str) -> None:
        self.commands.append(command)
        if self.fail_start and command[:2] == ("systemctl", "start"):
            raise OperationError("service_start_failed")


class CommercialOperatorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.root = self.base / "root"
        self.bundle_dir = self.base / "bundle"
        self.runtime = self.base / "runtime.json"
        self.certs = self.base / "certs"
        self.system = FakeSystem()
        self._write_bundle()
        self._write_runtime()
        self._write_certificates()

    def _write_bundle(self) -> None:
        members = {
            "bin/xray": b"xray-v26.5.9\n",
            "bin/maestro-xray-cdn-agent": b"agent\n",
            "bin/maestro-xray-cdn-commercial-operator": b"operator\n",
            "lib/commercial_bundle.py": b"bundle-library\n",
            "templates/config.json.tmpl": (
                b'{"log":{"access":"none","error":"/var/log/maestro-xray-cdn-commercial/error.log","loglevel":"warning"},'
                b'"api":{"tag":"api","services":["StatsService","HandlerService"]},'
                b'"inbounds":[{"listen":"0.0.0.0","port":<PROFILE_XHTTP_PORT>,"protocol":"vless",'
                b'"settings":{"clients":[],"decryption":"<RUNTIME_SERVER_DECRYPTION>"},'
                b'"streamSettings":{"network":"xhttp","xhttpSettings":{"host":"<RUNTIME_PUBLIC_HOST>",'
                b'"path":"<RUNTIME_SECRET_PATH>","mode":"packet-up"}},"tag":"maestro-cdn-in"},'
                b'{"listen":"127.0.0.1","port":<PROFILE_API_PORT>,"protocol":"dokodemo-door",'
                b'"settings":{"address":"127.0.0.1"},"tag":"api"}],"outbounds":[]}'
            ),
            "systemd/maestro-xray-cdn-commercial.service": b"[Unit]\nDescription=commercial xray\n",
            "systemd/maestro-xray-cdn-commercial-agent.service": b"[Unit]\nDescription=commercial agent\n",
            "sysusers/maestro-xray-cdn-commercial.conf": b"g maestro-xray-cdn -\n",
            "rollback.json": b'{"schema":1,"pointer":"/opt/maestro-xray-cdn-commercial/current"}\n',
        }
        for relative, payload in members.items():
            target = self.bundle_dir / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(payload)
            target.chmod(0o755 if relative.startswith("bin/") else 0o644)
        bundle.create_manifest(
            self.bundle_dir,
            source_commit=FULL_SHA,
            xray_version="26.5.9",
            xray_archive_sha256=XRAY_ARCHIVE_SHA,
        )

    def _write_runtime(self, *, public_host: str = "cdn.example.test") -> None:
        material = {
            "server_decryption": "none",
            "public_host": public_host,
            "secret_path": "/static/main/video/segment.ts/test",
            "active_origin_ips": ["192.0.2.10"],
            "controller_source_ip": "192.0.2.20",
            "managed_credentials": {
                "wl:test:exit-s1": "00000000-0000-4000-8000-000000000051",
            },
            "relay_routes": [
                {
                    "exit_id": f"exit-s{index}",
                    "address": f"192.0.2.{30 + index}",
                    "server_name": f"exit-s{index}.example.test",
                    "credential": f"00000000-0000-4000-8000-00000000004{index}",
                }
                for index in range(1, 5)
            ],
        }
        self.runtime.write_text(json.dumps(material), encoding="utf-8")
        self.runtime.chmod(0o600)

    def _write_certificates(self) -> None:
        required = (
            "api-mtls/server.crt",
            "api-mtls/server.key",
            "api-mtls/client-ca.crt",
            "api-mtls/sidecar-agent.crt",
            "api-mtls/sidecar-agent.key",
            "api-mtls/server-ca.crt",
            "relay-tls/server.crt",
            "relay-tls/server.key",
            "agent-server/server.crt",
            "agent-server/server.key",
            "controller-ca/client-ca.crt",
            "relay-ca/exit-s1.crt",
            "relay-ca/exit-s2.crt",
            "relay-ca/exit-s3.crt",
            "relay-ca/exit-s4.crt",
        )
        for relative in required:
            target = self.certs / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("fixture\n", encoding="utf-8")
            target.chmod(0o600)

    def _operator(self, profile: str = "standard") -> CommercialOperator:
        test_owner = (
            (os.getuid(), os.getgid())
            if hasattr(os, "getuid") and hasattr(os, "getgid")
            else (1001, 1001)
        )
        return CommercialOperator(
            root=self.root,
            bundle_dir=self.bundle_dir,
            profile=profile,
            runtime_material=self.runtime,
            certificate_source=self.certs,
            command_runner=self.system.run,
            owner_resolver=lambda _name: test_owner,
        )

    def test_manifest_tamper_refused_before_mutation(self) -> None:
        (self.bundle_dir / "bin/xray").write_bytes(b"tampered\n")
        with self.assertRaisesRegex(OperationError, "bundle_member_digest_mismatch"):
            self._operator().apply()
        self.assertFalse((self.root / "opt/maestro-xray-cdn-commercial").exists())
        self.assertEqual(self.system.commands, [])

    def test_s4_apply_preserves_ordinary_and_private_canary(self) -> None:
        ordinary = self.root / "usr/local/x-ui/bin/xray-linux-amd64"
        private_unit = self.root / "etc/systemd/system/maestro-xray-cdn.service"
        ordinary.parent.mkdir(parents=True)
        private_unit.parent.mkdir(parents=True)
        ordinary.write_bytes(b"ordinary-sentinel")
        private_unit.write_bytes(b"private-canary-sentinel")

        self._operator("s4-commercial").apply()

        self.assertEqual(ordinary.read_bytes(), b"ordinary-sentinel")
        self.assertEqual(private_unit.read_bytes(), b"private-canary-sentinel")
        flattened = "\n".join(" ".join(command) for command in self.system.commands)
        self.assertNotIn("maestro-xray-cdn.service", flattened)
        self.assertNotIn("/usr/local/x-ui", flattened)

    def test_apply_atomically_switches_current_pointer(self) -> None:
        result = self._operator().apply()
        current = self.root / "opt/maestro-xray-cdn-commercial/current"
        self.assertTrue(current.is_symlink())
        self.assertEqual(Path(os.readlink(current)).name, result["release_id"])
        self.assertEqual(result["status"], "ACTIVE")
        credential_name = hashlib.sha256(b"wl:test:exit-s1").hexdigest() + ".credential"
        credential = current.resolve() / "runtime/credentials" / credential_name
        self.assertEqual(credential.read_text(encoding="utf-8"), "00000000-0000-4000-8000-000000000051\n")

    def test_failed_start_restores_previous_pointer(self) -> None:
        first = self._operator().apply()
        self._write_runtime(public_host="cdn-next.example.test")
        self.system.fail_start = True

        with self.assertRaisesRegex(OperationError, "service_start_failed"):
            self._operator().apply()

        current = self.root / "opt/maestro-xray-cdn-commercial/current"
        self.assertEqual(Path(os.readlink(current)).name, first["release_id"])

    def test_explicit_rollback_restores_last_known_good(self) -> None:
        first = self._operator().apply()
        self._write_runtime(public_host="cdn-next.example.test")
        second = self._operator().apply()
        self.assertNotEqual(first["release_id"], second["release_id"])

        result = self._operator().rollback()

        self.assertEqual(result["release_id"], first["release_id"])
        self.assertEqual(result["status"], "ACTIVE")

    def test_status_reports_digest_drift_truthfully(self) -> None:
        applied = self._operator().apply()
        release = self.root / "opt/maestro-xray-cdn-commercial/releases" / applied["release_id"]
        (release / "config.json").write_text("{}\n", encoding="utf-8")

        status = self._operator().status()

        self.assertEqual(status["status"], "DRIFT")
        self.assertNotEqual(status["expected_config_sha256"], status["actual_config_sha256"])

    def test_standard_and_s4_profiles_are_isolated(self) -> None:
        standard = profile_contract("standard")
        s4 = profile_contract("s4-commercial")
        self.assertEqual((standard.xhttp_port, standard.api_port, standard.relay_port, standard.agent_port), (18081, 18082, 18084, 18443))
        self.assertEqual((s4.xhttp_port, s4.api_port, s4.relay_port, s4.agent_port), (28081, 28082, 18084, 18443))
        self.assertEqual(s4.proxy_target, "http://127.0.0.1:28081")
        self.assertEqual(s4.xray_unit, "maestro-xray-cdn-commercial.service")
        self.assertEqual(s4.agent_unit, "maestro-xray-cdn-commercial-agent.service")


if __name__ == "__main__":
    unittest.main()
