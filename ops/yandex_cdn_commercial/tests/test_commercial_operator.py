import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from ops.yandex_cdn_commercial import bundle
from ops.yandex_cdn_commercial.operator import CommercialOperator, OperationError, _default_runner, profile_contract


FULL_SHA = "467e40af33fe7ba6f5a7957adfcbf0d9e72a2d71"
SECOND_SHA = "1111111111111111111111111111111111111111"
XRAY_ARCHIVE_SHA = "f56c106b7c0159ad386bccd340faa5bbf55fd5c15821ec9e63e6a6ba11d3d1c7"


class FakeSystem:
    def __init__(self) -> None:
        self.commands: list[tuple[str, ...]] = []
        self.fail_start = False
        self.fail_health = False
        self.listeners_ready = True
        self.active_units: set[str] = set()
        self.process_ready = True
        self.process_targets: list[str] = []
        self.restart_failures: list[str] = []

    def run(self, *command: str) -> None:
        self.commands.append(command)
        if command[:1] == ("systemctl",) and len(command) > 1 and command[1] in {"start", "restart"} and self.restart_failures:
            raise OperationError(self.restart_failures.pop(0))
        if self.fail_start and command[:1] == ("systemctl",) and len(command) > 1 and command[1] in {"start", "restart"}:
            self.fail_start = False
            raise OperationError("service_start_failed")
        if command[:2] == ("systemctl", "start"):
            self.active_units.add(command[2])
        elif command[:2] == ("systemctl", "restart"):
            self.active_units.add(command[2])
        elif command[:2] == ("systemctl", "stop"):
            self.active_units.difference_update(command[2:])
        elif command[:3] == ("systemctl", "is-active", "--quiet"):
            if self.fail_health or command[3] not in self.active_units:
                raise OperationError("service_inactive")

    def listeners_bound(self, _ports: set[int]) -> bool:
        return (
            not self.fail_health
            and self.listeners_ready
            and {
                "maestro-xray-cdn-commercial.service",
                "maestro-xray-cdn-commercial-agent.service",
            }.issubset(self.active_units)
        )

    def processes_match(self, target: Path, _profile: object) -> bool:
        self.process_targets.append(target.name)
        return (
            self.process_ready
            and {
                "maestro-xray-cdn-commercial.service",
                "maestro-xray-cdn-commercial-agent.service",
            }.issubset(self.active_units)
        )


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

    def test_production_template_uses_verified_xhttp_recipe_and_private_relay_ca(self) -> None:
        template = (Path(__file__).parents[1] / "templates" / "config.json.tmpl").read_text(encoding="utf-8")
        for fragment in (
            '"sessionIDPlacement": "query"',
            '"sessionIDKey": "auth"',
            '"sessionIDLength": 16',
            '"seqPlacement": "query"',
            '"seqKey": "chunk_id"',
            '"uplinkHTTPMethod": "GET"',
            '"uplinkDataPlacement": "body"',
            '"disableSystemRoot": true',
            '"verifyPeerCertByName": "maestro-metering-client,maestro-sidecar-agent"',
        ):
            self.assertIn(fragment, template)
        self.assertNotIn("verifyPeerCertInNames", template)
        for index in range(1, 5):
            self.assertIn(
                f'"certificateFile": "/opt/maestro-xray-cdn-commercial/current/runtime/relay-ca/exit-s{index}.crt"',
                template,
            )

    def test_direct_outbound_allows_only_exact_loopback_relay_health_target(self) -> None:
        template = (Path(__file__).parents[1] / "templates" / "config.json.tmpl").read_text(encoding="utf-8")
        rendered = template.replace("<PROFILE_XHTTP_PORT>", "28081").replace("<PROFILE_API_PORT>", "28082")
        replacements = {
            "<RUNTIME_SERVER_DECRYPTION>": "none",
            "<RUNTIME_PUBLIC_HOST>": "cdn.example.test",
            "<RUNTIME_SECRET_PATH>": "/commercial/path",
        }
        for index in range(1, 5):
            replacements.update({
                f"<RUNTIME_EXIT_S{index}_ADDRESS>": f"192.0.2.{index}",
                f"<RUNTIME_EXIT_S{index}_CREDENTIAL>": f"00000000-0000-4000-8000-00000000004{index}",
                f"<RUNTIME_EXIT_S{index}_SERVER_NAME>": f"exit-s{index}.example.test",
            })
        for placeholder, value in replacements.items():
            rendered = rendered.replace(placeholder, value)
        document = json.loads(rendered)

        direct = next(outbound for outbound in document["outbounds"] if outbound["tag"] == "direct")
        self.assertEqual(direct, {
            "protocol": "freedom",
            "settings": {
                "finalRules": [{
                    "action": "allow",
                    "network": "tcp",
                    "ip": ["127.0.0.1/32"],
                    "port": 18444,
                }],
            },
            "tag": "direct",
        })

    def test_parser_diagnostics_require_explicit_synthetic_ci_opt_in(self) -> None:
        completed = subprocess.CompletedProcess(
            args=("xray", "run", "-test"),
            returncode=23,
            stdout="",
            stderr="synthetic-xray-parser-detail",
        )
        environment_key = "MAESTRO_SYNTHETIC_XRAY_DIAGNOSTICS"

        with mock.patch("ops.yandex_cdn_commercial.operator.subprocess.run", return_value=completed):
            with mock.patch.dict(os.environ, {}, clear=False):
                os.environ.pop(environment_key, None)
                hidden = io.StringIO()
                with contextlib.redirect_stderr(hidden), self.assertRaisesRegex(OperationError, "command_failed"):
                    _default_runner("xray", "run", "-test")
                self.assertEqual(hidden.getvalue(), "")

            with mock.patch.dict(os.environ, {environment_key: "1"}, clear=False):
                visible = io.StringIO()
                with contextlib.redirect_stderr(visible), self.assertRaisesRegex(OperationError, "command_failed"):
                    _default_runner("xray", "run", "-test")
                self.assertIn("synthetic-xray-parser-detail", visible.getvalue())

    def test_first_install_validation_uses_temporary_certificates_without_mutating_bundle(self) -> None:
        runtime_prefix = "/opt/maestro-xray-cdn-commercial/current/runtime/"
        production_log = "/var/log/maestro-xray-cdn-commercial/error.log"
        template_path = self.bundle_dir / "templates/config.json.tmpl"
        template = template_path.read_text(encoding="utf-8").replace(
            '"streamSettings":{"network":"xhttp",',
            '"streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"certificates":['
            '{"certificateFile":"' + runtime_prefix + 'api-mtls/server.crt","keyFile":"'
            + runtime_prefix
            + 'api-mtls/server.key"},{"certificateFile":"'
            + runtime_prefix
            + 'api-mtls/client-ca.crt","usage":"verify"}]},',
        )
        template_path.write_text(template, encoding="utf-8")
        (self.bundle_dir / "manifest.json").unlink()
        bundle.create_manifest(
            self.bundle_dir,
            source_commit=FULL_SHA,
            xray_version="26.5.9",
            xray_archive_sha256=XRAY_ARCHIVE_SHA,
        )

        failures: list[str] = []
        operator = self._operator("s4-commercial")
        production_config = operator._render_config(operator._material(), template_path.read_bytes())

        def first_install_runner(*command: str) -> None:
            config_path = Path(command[command.index("-config") + 1])
            validation_config = config_path.read_bytes()
            if runtime_prefix.encode("utf-8") in validation_config:
                failures.append("validation config still references the absent current/runtime tree")
            document = json.loads(validation_config)
            validation_log = document.get("log", {}).get("error")
            if validation_log == production_log:
                failures.append("validation config still references the absent production log directory")
            elif not isinstance(validation_log, str) or Path(validation_log).parent != config_path.parent:
                failures.append("validation config does not isolate the error log beside the temporary config")
            certificate_paths: list[str] = []

            def collect(value: object) -> None:
                if isinstance(value, dict):
                    certificate_file = value.get("certificateFile")
                    if isinstance(certificate_file, str):
                        certificate_paths.append(certificate_file)
                    for child in value.values():
                        collect(child)
                elif isinstance(value, list):
                    for child in value:
                        collect(child)

            collect(document)
            missing = [path for path in certificate_paths if not Path(path).is_file()]
            if missing:
                failures.append("validation certificate paths do not exist: " + ",".join(missing))

        operator.run = first_install_runner
        result = operator.plan()
        if result["config_sha256"] != hashlib.sha256(production_config).hexdigest():
            failures.append("production config digest changed during validation")
        if runtime_prefix.encode("utf-8") not in production_config:
            failures.append("production config no longer references current/runtime")
        if production_log.encode("utf-8") not in production_config:
            failures.append("production config no longer references the production error log")

        cli_root = self.base / "direct-cli"
        cli_bin = cli_root / "bin/maestro-xray-cdn-commercial-operator"
        cli_lib = cli_root / "lib/commercial_bundle.py"
        cli_bin.parent.mkdir(parents=True)
        cli_lib.parent.mkdir(parents=True)
        cli_bin.write_bytes((Path(__file__).parents[1] / "operator.py").read_bytes())
        cli_lib.write_bytes((Path(__file__).parents[1] / "bundle.py").read_bytes())
        environment = os.environ.copy()
        environment.pop("PYTHONDONTWRITEBYTECODE", None)
        environment.pop("PYTHONPYCACHEPREFIX", None)
        completed = subprocess.run(
            [sys.executable, str(cli_bin), "--help"],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if completed.returncode != 0:
            failures.append("direct CLI failed: " + completed.stderr.strip())
        if (cli_root / "lib/__pycache__").exists():
            failures.append("direct CLI wrote bytecode into the immutable bundle")

        self.assertEqual(failures, [])

    def test_relay_ca_directory_is_traversable_by_xray_and_agent_group(self) -> None:
        owners = {
            "root": (10, 20),
            "maestro-xray-cdn": (30, 40),
            "maestro-xray-cdn-agent": (50, 60),
        }
        operator = CommercialOperator(
            root=self.root,
            bundle_dir=self.bundle_dir,
            profile="s4-commercial",
            runtime_material=self.runtime,
            certificate_source=self.certs,
            command_runner=self.system.run,
            owner_resolver=owners.__getitem__,
        )
        prepared = operator._prepare()
        directories: dict[str, tuple[int, tuple[int, int]]] = {}
        files: dict[str, tuple[int, tuple[int, int] | None]] = {}

        def protect(path: Path, mode: int, owner: tuple[int, int]) -> None:
            path.mkdir(parents=True, exist_ok=True)
            path.chmod(mode)
            directories[path.relative_to(self.root).as_posix()] = (mode, owner)

        def write(path: Path, raw: bytes, mode: int, owner: tuple[int, int] | None = None) -> None:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(raw)
            path.chmod(mode)
            files[path.relative_to(self.root).as_posix()] = (mode, owner)

        with mock.patch.object(operator, "_protect_directory", side_effect=protect), mock.patch.object(
            operator, "_write", side_effect=write
        ):
            operator._stage_release(*prepared)

        relay_directory = next(value for path, value in directories.items() if path.endswith("/runtime/relay-ca"))
        relay_file = next(value for path, value in files.items() if path.endswith("/runtime/relay-ca/exit-s1.crt"))
        self.assertEqual(
            relay_directory,
            (0o750, (owners["root"][0], owners["maestro-xray-cdn"][1])),
        )
        self.assertEqual(
            relay_file,
            (0o640, owners["maestro-xray-cdn"]),
        )
        agent_unit = (Path(__file__).parents[1] / "systemd/maestro-xray-cdn-commercial-agent.service").read_text(
            encoding="utf-8"
        )
        self.assertIn("SupplementaryGroups=maestro-xray-cdn", agent_unit)

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

    def _rewrite_bundle_binaries(self) -> None:
        (self.bundle_dir / "manifest.json").unlink()
        (self.bundle_dir / "bin/xray").write_bytes(b"xray-v26.5.9-second\n")
        (self.bundle_dir / "bin/maestro-xray-cdn-agent").write_bytes(b"agent-second\n")
        bundle.create_manifest(
            self.bundle_dir,
            source_commit=SECOND_SHA,
            xray_version="26.5.9",
            xray_archive_sha256=XRAY_ARCHIVE_SHA,
        )

    def _operator(self, profile: str = "standard") -> CommercialOperator:
        test_owner = (
            (os.getuid(), os.getgid())
            if hasattr(os, "getuid") and hasattr(os, "getgid")
            else (1001, 1001)
        )
        operator = CommercialOperator(
            root=self.root,
            bundle_dir=self.bundle_dir,
            profile=profile,
            runtime_material=self.runtime,
            certificate_source=self.certs,
            command_runner=self.system.run,
            owner_resolver=lambda _name: test_owner,
        )
        operator.listener_probe = self.system.listeners_bound
        operator.process_probe = self.system.processes_match
        return operator

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

    def test_first_install_explicit_rollback_restores_absent(self) -> None:
        ordinary = self.root / "usr/local/x-ui/bin/xray-linux-amd64"
        private_unit = self.root / "etc/systemd/system/maestro-xray-cdn.service"
        ordinary.parent.mkdir(parents=True)
        private_unit.parent.mkdir(parents=True)
        ordinary.write_bytes(b"ordinary-sentinel")
        private_unit.write_bytes(b"private-canary-sentinel")

        self._operator("s4-commercial").apply()
        rollback_command_start = len(self.system.commands)
        result = self._operator("s4-commercial").rollback()

        current = self.root / "opt/maestro-xray-cdn-commercial/current"
        self.assertFalse(current.exists())
        self.assertFalse(current.is_symlink())
        self.assertEqual(result["status"], "ABSENT")
        self.assertEqual(ordinary.read_bytes(), b"ordinary-sentinel")
        self.assertEqual(private_unit.read_bytes(), b"private-canary-sentinel")
        rollback_commands = self.system.commands[rollback_command_start:]
        self.assertIn(
            (
                "systemctl",
                "stop",
                "maestro-xray-cdn-commercial-agent.service",
                "maestro-xray-cdn-commercial.service",
            ),
            rollback_commands,
        )
        self.assertIn(
            (
                "systemctl",
                "disable",
                "maestro-xray-cdn-commercial-agent.service",
                "maestro-xray-cdn-commercial.service",
            ),
            rollback_commands,
        )
        self.assertNotIn("maestro-xray-cdn.service", "\n".join(" ".join(row) for row in rollback_commands))

    def test_release_identity_binds_runtime_inputs_without_secret_plaintext(self) -> None:
        first = self._operator().apply()
        rotated_certificate = self.certs / "agent-server/server.crt"
        rotated_certificate.write_bytes(b"rotated-certificate\n")
        rotated_certificate.chmod(0o600)

        second = self._operator().apply()

        self.assertNotEqual(first["release_id"], second["release_id"])
        second_release = self.root / "opt/maestro-xray-cdn-commercial/releases" / second["release_id"]
        self.assertEqual(
            (second_release / "runtime/agent-server/server.crt").read_bytes(),
            b"rotated-certificate\n",
        )

        material = json.loads(self.runtime.read_text(encoding="utf-8"))
        material["active_origin_ips"] = ["192.0.2.11"]
        material["controller_source_ip"] = "192.0.2.21"
        material["managed_credentials"]["wl:test:exit-s1"] = "00000000-0000-4000-8000-000000000052"
        self.runtime.write_text(json.dumps(material), encoding="utf-8")
        self.runtime.chmod(0o600)

        third = self._operator().apply()

        self.assertNotEqual(second["release_id"], third["release_id"])
        third_release = self.root / "opt/maestro-xray-cdn-commercial/releases" / third["release_id"]
        credential_name = hashlib.sha256(b"wl:test:exit-s1").hexdigest() + ".credential"
        self.assertEqual(
            (third_release / "runtime/credentials" / credential_name).read_text(encoding="utf-8"),
            "00000000-0000-4000-8000-000000000052\n",
        )
        metadata_text = (third_release / "release.json").read_text(encoding="utf-8")
        metadata = json.loads(metadata_text)
        self.assertRegex(metadata["runtime_input_sha256"], r"^[0-9a-f]{64}$")
        self.assertIn("agent.env", metadata["release_inventory"])
        self.assertNotIn("rotated-certificate", metadata_text)
        self.assertNotIn("00000000-0000-4000-8000-000000000052", metadata_text)

    def test_apply_health_and_status_inventory_are_fail_closed(self) -> None:
        self.system.fail_health = True

        with self.assertRaisesRegex(OperationError, "post_apply_verification_failed"):
            self._operator().apply()

        current = self.root / "opt/maestro-xray-cdn-commercial/current"
        state = self.root / "var/lib/maestro-xray-cdn-commercial/operator-state.json"
        self.assertFalse(current.exists())
        self.assertFalse(current.is_symlink())
        self.assertFalse(state.exists())
        self.assertEqual(self.system.active_units, set())

        self.system.fail_health = False
        applied = self._operator().apply()
        release = self.root / "opt/maestro-xray-cdn-commercial/releases" / applied["release_id"]
        metadata = json.loads((release / "release.json").read_text(encoding="utf-8"))
        self.assertIn("runtime/agent-server/server.crt", metadata["release_inventory"])
        self.assertIn("agent.env", metadata["release_inventory"])
        self.assertIn(
            "/etc/systemd/system/maestro-xray-cdn-commercial.service",
            metadata["package_inventory"],
        )
        self.assertEqual(metadata["release_inventory"]["config.json"]["mode"], "0640")

        certificate = release / "runtime/agent-server/server.crt"
        original_certificate = certificate.read_bytes()
        certificate.write_bytes(b"tampered\n")
        self.assertEqual(self._operator().status()["status"], "DRIFT")
        certificate.write_bytes(original_certificate)
        certificate.chmod(0o640)

        environment = release / "agent.env"
        original_environment = environment.read_bytes()
        environment.write_bytes(b"tampered\n")
        self.assertEqual(self._operator().status()["status"], "DRIFT")
        environment.write_bytes(original_environment)
        environment.chmod(0o640)

        commercial_unit = self.root / "etc/systemd/system/maestro-xray-cdn-commercial.service"
        original_unit = commercial_unit.read_bytes()
        commercial_unit.write_bytes(b"tampered\n")
        self.assertEqual(self._operator().status()["status"], "DRIFT")
        commercial_unit.write_bytes(original_unit)
        commercial_unit.chmod(0o644)

        self.system.active_units.discard("maestro-xray-cdn-commercial.service")
        self.assertEqual(self._operator().status()["status"], "DRIFT")
        self.system.active_units.add("maestro-xray-cdn-commercial.service")
        self.system.listeners_ready = False
        self.assertEqual(self._operator().status()["status"], "DRIFT")

    def test_unknown_package_bytes_refused_and_failed_install_is_recoverable(self) -> None:
        commercial_unit = self.root / "etc/systemd/system/maestro-xray-cdn-commercial.service"
        commercial_unit.parent.mkdir(parents=True)
        commercial_unit.write_bytes(b"unknown-existing-unit\n")
        commercial_unit.chmod(0o644)

        with self.assertRaisesRegex(OperationError, "package_file_conflict"):
            self._operator().apply()

        self.assertEqual(commercial_unit.read_bytes(), b"unknown-existing-unit\n")
        self.assertFalse((self.root / "opt/maestro-xray-cdn-commercial/current").exists())

        commercial_unit.unlink()
        self.system.fail_start = True
        with self.assertRaisesRegex(OperationError, "service_start_failed"):
            self._operator().apply()

        self.assertFalse(commercial_unit.exists())
        self.assertFalse(
            (self.root / "etc/systemd/system/maestro-xray-cdn-commercial-agent.service").exists()
        )
        self.assertFalse(
            (self.root / "usr/lib/sysusers.d/maestro-xray-cdn-commercial.conf").exists()
        )

    def test_upgrade_restarts_units_and_requires_current_process_identity(self) -> None:
        self._operator().apply()
        self._write_runtime(public_host="cdn-next.example.test")
        command_start = len(self.system.commands)

        upgraded = self._operator().apply()

        upgrade_commands = self.system.commands[command_start:]
        self.assertIn(
            ("systemctl", "restart", "maestro-xray-cdn-commercial.service"),
            upgrade_commands,
        )
        self.assertIn(
            ("systemctl", "restart", "maestro-xray-cdn-commercial-agent.service"),
            upgrade_commands,
        )
        self.assertNotIn(
            ("systemctl", "start", "maestro-xray-cdn-commercial.service"),
            upgrade_commands,
        )
        self.assertEqual(self.system.process_targets[-1], upgraded["release_id"])

        self.system.process_ready = False
        self.assertEqual(self._operator().status()["status"], "DRIFT")

    def test_two_bundle_rollback_uses_release_local_binary_inventory(self) -> None:
        first = self._operator().apply()
        self._rewrite_bundle_binaries()
        self._write_runtime(public_host="cdn-next.example.test")
        second = self._operator().apply()
        self.assertNotEqual(first["release_id"], second["release_id"])

        rolled_back = self._operator().rollback()

        self.assertEqual(rolled_back["release_id"], first["release_id"])
        self.assertEqual(rolled_back["status"], "ACTIVE")

    def test_automatic_failback_proves_previous_or_reports_both_failures(self) -> None:
        first = self._operator().apply()
        self._write_runtime(public_host="cdn-next.example.test")
        process_proof_start = len(self.system.process_targets)
        self.system.restart_failures = ["primary_restart_failed"]

        with self.assertRaisesRegex(OperationError, "primary_restart_failed"):
            self._operator().apply()

        current = self.root / "opt/maestro-xray-cdn-commercial/current"
        self.assertEqual(Path(os.readlink(current)).name, first["release_id"])
        self.assertIn(first["release_id"], self.system.process_targets[process_proof_start:])
        recovery_commands = self.system.commands
        self.assertIn(
            ("systemctl", "restart", "maestro-xray-cdn-commercial.service"),
            recovery_commands,
        )
        self.assertIn(
            ("systemctl", "restart", "maestro-xray-cdn-commercial-agent.service"),
            recovery_commands,
        )

        self._write_runtime(public_host="cdn-third.example.test")
        self.system.process_ready = False
        with self.assertRaisesRegex(OperationError, "rollback_failed") as caught:
            self._operator().apply()

        failure = str(caught.exception)
        self.assertIn("primary=post_apply_verification_failed", failure)
        self.assertIn("rollback=rollback_health_failed", failure)
        self.assertEqual(Path(os.readlink(current)).name, first["release_id"])


if __name__ == "__main__":
    unittest.main()
