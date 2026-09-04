#!/usr/bin/env python3
"""Bounded plan/apply/status/rollback operator for the commercial CDN sidecar."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from typing import Any, Callable

try:
    from . import bundle
except ImportError:  # pragma: no cover - bundled executable path
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "lib"))
    import commercial_bundle as bundle


BASE_PATH = "/opt/maestro-xray-cdn-commercial"
STATE_PATH = "/var/lib/maestro-xray-cdn-commercial/operator-state.json"
XRAY_UNIT = "maestro-xray-cdn-commercial.service"
AGENT_UNIT = "maestro-xray-cdn-commercial-agent.service"
UNIT_PATHS = {
    XRAY_UNIT: "systemd/maestro-xray-cdn-commercial.service",
    AGENT_UNIT: "systemd/maestro-xray-cdn-commercial-agent.service",
}
SYSUSERS_PATH = "sysusers/maestro-xray-cdn-commercial.conf"
ABSENT_TARGET = "ABSENT"
PACKAGE_PATHS = {
    "/usr/lib/sysusers.d/maestro-xray-cdn-commercial.conf": SYSUSERS_PATH,
    "/etc/systemd/system/maestro-xray-cdn-commercial.service": UNIT_PATHS[XRAY_UNIT],
    "/etc/systemd/system/maestro-xray-cdn-commercial-agent.service": UNIT_PATHS[AGENT_UNIT],
}
REQUIRED_CERTIFICATES = (
    "agent-server/server.crt",
    "agent-server/server.key",
    "api-mtls/client-ca.crt",
    "api-mtls/server-ca.crt",
    "api-mtls/server.crt",
    "api-mtls/server.key",
    "api-mtls/sidecar-agent.crt",
    "api-mtls/sidecar-agent.key",
    "controller-ca/client-ca.crt",
    "relay-ca/exit-s1.crt",
    "relay-ca/exit-s2.crt",
    "relay-ca/exit-s3.crt",
    "relay-ca/exit-s4.crt",
    "relay-tls/server.crt",
    "relay-tls/server.key",
)
_HOST_RE = re.compile(r"^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
_UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")


class OperationError(RuntimeError):
    pass


@dataclass(frozen=True)
class Profile:
    name: str
    xhttp_port: int
    api_port: int
    relay_port: int
    agent_port: int
    proxy_target: str
    xray_unit: str = XRAY_UNIT
    agent_unit: str = AGENT_UNIT


def profile_contract(name: str) -> Profile:
    if name == "standard":
        return Profile(name, 18081, 18082, 18084, 18443, "http://127.0.0.1:18081")
    if name == "s4-commercial":
        return Profile(name, 28081, 28082, 18084, 18443, "http://127.0.0.1:28081")
    raise OperationError("profile_invalid")


def _sha256(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def _canonical(value: Any) -> bytes:
    return (json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True) + "\n").encode("utf-8")


def _default_runner(*command: str) -> None:
    completed = subprocess.run(command, check=False, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if completed.returncode != 0:
        raise OperationError("command_failed")


class CommercialOperator:
    def __init__(
        self,
        *,
        root: Path,
        bundle_dir: Path,
        profile: str,
        runtime_material: Path | None = None,
        certificate_source: Path | None = None,
        command_runner: Callable[..., None] = _default_runner,
        owner_resolver: Callable[[str], tuple[int, int]] | None = None,
        listener_probe: Callable[[set[int]], bool] | None = None,
    ) -> None:
        self.root = Path(root).resolve()
        self.bundle_dir = Path(bundle_dir).resolve()
        self.profile = profile_contract(profile)
        self.runtime_material = Path(runtime_material).resolve() if runtime_material else None
        self.certificate_source = Path(certificate_source).resolve() if certificate_source else None
        self.run = command_runner
        self.owner_resolver = owner_resolver or self._resolve_owner
        self.listener_probe = listener_probe or self._default_listener_probe

    @staticmethod
    def _resolve_owner(name: str) -> tuple[int, int]:
        import pwd

        try:
            identity = pwd.getpwnam(name)
        except KeyError as error:
            raise OperationError("service_identity_missing") from error
        return identity.pw_uid, identity.pw_gid

    @staticmethod
    def _default_listener_probe(expected_ports: set[int]) -> bool:
        listening: set[int] = set()
        for table in (Path("/proc/net/tcp"), Path("/proc/net/tcp6")):
            try:
                rows = table.read_text(encoding="ascii").splitlines()[1:]
            except OSError:
                continue
            for row in rows:
                fields = row.split()
                if len(fields) < 4 or fields[3] != "0A":
                    continue
                try:
                    listening.add(int(fields[1].rsplit(":", 1)[1], 16))
                except (IndexError, ValueError):
                    continue
        return expected_ports.issubset(listening)

    def _rooted(self, absolute: str) -> Path:
        if not absolute.startswith("/") or ".." in Path(absolute).parts:
            raise OperationError("path_invalid")
        return self.root / absolute.lstrip("/")

    def _bundle(self) -> dict[str, Any]:
        try:
            return bundle.verify_manifest(self.bundle_dir)
        except (bundle.ManifestError, OSError) as error:
            raise OperationError(str(error)) from error

    @staticmethod
    def _read_protected(path: Path, *, limit: int = 1 << 20) -> bytes:
        try:
            metadata = path.lstat()
        except OSError as error:
            raise OperationError("runtime_input_unavailable") from error
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or metadata.st_size <= 0 or metadata.st_size > limit:
            raise OperationError("runtime_input_invalid")
        if os.name == "posix" and stat.S_IMODE(metadata.st_mode) & 0o077:
            raise OperationError("runtime_input_mode_invalid")
        return path.read_bytes()

    def _material(self) -> dict[str, Any]:
        if self.runtime_material is None:
            raise OperationError("runtime_material_required")
        try:
            material = json.loads(self._read_protected(self.runtime_material))
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise OperationError("runtime_material_invalid") from error
        required = {"active_origin_ips", "controller_source_ip", "managed_credentials", "public_host", "relay_routes", "secret_path", "server_decryption"}
        if not isinstance(material, dict) or set(material) != required:
            raise OperationError("runtime_material_invalid")
        if not isinstance(material["server_decryption"], str) or not material["server_decryption"] or len(material["server_decryption"]) > 4096:
            raise OperationError("runtime_material_invalid")
        if not isinstance(material["public_host"], str) or not _HOST_RE.fullmatch(material["public_host"]):
            raise OperationError("runtime_material_invalid")
        if not isinstance(material["secret_path"], str) or not material["secret_path"].startswith("/") or len(material["secret_path"]) > 1024:
            raise OperationError("runtime_material_invalid")
        origins = material["active_origin_ips"]
        if not isinstance(origins, list) or not origins or len(origins) > 32:
            raise OperationError("runtime_material_invalid")
        try:
            normalized = sorted({str(ipaddress.ip_address(value)) for value in origins})
            controller = str(ipaddress.ip_address(material["controller_source_ip"]))
        except (TypeError, ValueError) as error:
            raise OperationError("runtime_material_invalid") from error
        if normalized != origins or controller != material["controller_source_ip"]:
            raise OperationError("runtime_material_invalid")
        routes = material["relay_routes"]
        if not isinstance(routes, list) or len(routes) != 4:
            raise OperationError("runtime_material_invalid")
        for index, route in enumerate(routes, 1):
            if not isinstance(route, dict) or set(route) != {"address", "credential", "exit_id", "server_name"}:
                raise OperationError("runtime_material_invalid")
            if route["exit_id"] != f"exit-s{index}" or not _HOST_RE.fullmatch(route["server_name"]) or not _UUID_RE.fullmatch(route["credential"]):
                raise OperationError("runtime_material_invalid")
            try:
                ipaddress.ip_address(route["address"])
            except (TypeError, ValueError) as error:
                raise OperationError("runtime_material_invalid") from error
        credentials = material["managed_credentials"]
        if not isinstance(credentials, dict) or len(credentials) > 10000:
            raise OperationError("runtime_material_invalid")
        for email, credential in credentials.items():
            if not isinstance(email, str) or re.fullmatch(r"wl:[^:\s]{1,128}:exit-s[1-4]", email) is None or not isinstance(credential, str) or _UUID_RE.fullmatch(credential) is None:
                raise OperationError("runtime_material_invalid")
        return material

    def _certificate_bytes(self) -> dict[str, bytes]:
        if self.certificate_source is None or not self.certificate_source.is_dir() or self.certificate_source.is_symlink():
            raise OperationError("certificate_source_invalid")
        result: dict[str, bytes] = {}
        for relative in REQUIRED_CERTIFICATES:
            result[relative] = self._read_protected(self.certificate_source / relative)
        actual = {
            item.relative_to(self.certificate_source).as_posix()
            for item in self.certificate_source.rglob("*")
            if item.is_file() or item.is_symlink()
        }
        if actual != set(REQUIRED_CERTIFICATES):
            raise OperationError("certificate_source_invalid")
        return result

    @staticmethod
    def _json_fragment(value: str) -> str:
        return json.dumps(value, ensure_ascii=False)[1:-1]

    @staticmethod
    def _snapshot_members(bundle_dir: Path, manifest: dict[str, Any]) -> dict[str, bytes]:
        result: dict[str, bytes] = {}
        for item in manifest["members"]:
            raw = (bundle_dir / item["path"]).read_bytes()
            if len(raw) != item["size_bytes"] or _sha256(raw) != item["sha256"]:
                raise OperationError("bundle_member_digest_mismatch")
            result[item["path"]] = raw
        return result

    def _render_config(self, material: dict[str, Any], template: bytes) -> bytes:
        try:
            raw = template.decode("utf-8")
        except UnicodeDecodeError as error:
            raise OperationError("config_template_invalid") from error
        replacements = {
            "<PROFILE_XHTTP_PORT>": str(self.profile.xhttp_port),
            "<PROFILE_API_PORT>": str(self.profile.api_port),
            "<RUNTIME_SERVER_DECRYPTION>": self._json_fragment(material["server_decryption"]),
            "<RUNTIME_PUBLIC_HOST>": self._json_fragment(material["public_host"]),
            "<RUNTIME_SECRET_PATH>": self._json_fragment(material["secret_path"]),
        }
        for route in material["relay_routes"]:
            prefix = route["exit_id"].upper().replace("-", "_")
            replacements[f"<RUNTIME_{prefix}_ADDRESS>"] = self._json_fragment(route["address"])
            replacements[f"<RUNTIME_{prefix}_SERVER_NAME>"] = self._json_fragment(route["server_name"])
            replacements[f"<RUNTIME_{prefix}_CREDENTIAL>"] = self._json_fragment(route["credential"])
        for marker, value in replacements.items():
            raw = raw.replace(marker, value)
        if "<RUNTIME_" in raw or "<PROFILE_" in raw:
            raise OperationError("config_template_unresolved")
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError as error:
            raise OperationError("rendered_config_invalid") from error
        encoded = _canonical(parsed)
        if f'"port":{self.profile.xhttp_port}'.encode() not in encoded or f'"port":{self.profile.api_port}'.encode() not in encoded:
            raise OperationError("rendered_profile_mismatch")
        return encoded

    def _environment_bytes(self, release_id: str, config_sha: str) -> bytes:
        environment = {
            "MAESTRO_ACTIVE_ORIGIN_IPS_FILE": BASE_PATH + "/current/runtime/active-origin-ips.json",
            "MAESTRO_AGENT_SERVER_CERT": BASE_PATH + "/current/runtime/agent-server/server.crt",
            "MAESTRO_AGENT_SERVER_KEY": BASE_PATH + "/current/runtime/agent-server/server.key",
            "MAESTRO_CONFIG_DIGEST": config_sha,
            "MAESTRO_CONTROLLER_CA": BASE_PATH + "/current/runtime/controller-ca/client-ca.crt",
            "MAESTRO_CONTROLLER_SOURCE_IP_FILE": BASE_PATH + "/current/runtime/controller-source-ip.json",
            "MAESTRO_CREDENTIAL_DIRECTORY": BASE_PATH + "/current/runtime/credentials",
            "MAESTRO_RECEIPT_DIRECTORY": "/var/lib/maestro-xray-cdn-commercial-agent/receipts",
            "MAESTRO_RELAY_CA_DIRECTORY": BASE_PATH + "/current/runtime/relay-ca",
            "MAESTRO_RELAY_CREDENTIAL_DIRECTORY": BASE_PATH + "/current/runtime/relay-credentials",
            "MAESTRO_RELEASE_ID": release_id,
            "MAESTRO_XRAY_API_ADDRESS": f"127.0.0.1:{self.profile.api_port}",
            "MAESTRO_XRAY_API_CA": BASE_PATH + "/current/runtime/api-mtls/server-ca.crt",
            "MAESTRO_XRAY_API_SERVER_NAME": "maestro-xray-api",
            "MAESTRO_XRAY_CLIENT_CERT": BASE_PATH + "/current/runtime/api-mtls/sidecar-agent.crt",
            "MAESTRO_XRAY_CLIENT_KEY": BASE_PATH + "/current/runtime/api-mtls/sidecar-agent.key",
            "MAESTRO_XRAY_CONFIG_FILE": BASE_PATH + "/current/config.json",
            "MAESTRO_XRAY_PID_FILE": "/run/maestro-xray-cdn-commercial-pid/xray.pid",
        }
        return "".join(f"{key}={value}\n" for key, value in sorted(environment.items())).encode()

    @staticmethod
    def _fingerprint(raw: bytes, mode: int) -> dict[str, Any]:
        return {"mode": f"{mode:04o}", "sha256": _sha256(raw), "size_bytes": len(raw)}

    def _release_inventory(
        self,
        members: dict[str, bytes],
        material: dict[str, Any],
        certificates: dict[str, bytes],
        config: bytes,
        environment: bytes,
    ) -> dict[str, dict[str, Any]]:
        inventory = {
            "xray": self._fingerprint(members["bin/xray"], 0o755),
            "maestro-xray-cdn-agent": self._fingerprint(members["bin/maestro-xray-cdn-agent"], 0o755),
            "config.json": self._fingerprint(config, 0o640),
            "agent.env": self._fingerprint(environment, 0o640),
            "runtime/active-origin-ips.json": self._fingerprint(_canonical(material["active_origin_ips"]), 0o640),
            "runtime/controller-source-ip.json": self._fingerprint(_canonical(material["controller_source_ip"]), 0o640),
        }
        for relative, raw in certificates.items():
            mode = 0o600 if relative.endswith(".key") else 0o640
            inventory["runtime/" + relative] = self._fingerprint(raw, mode)
        for route in material["relay_routes"]:
            raw = (route["credential"] + "\n").encode()
            inventory[f"runtime/relay-credentials/{route['exit_id']}.credential"] = self._fingerprint(raw, 0o600)
        for email, credential in sorted(material["managed_credentials"].items()):
            credential_name = hashlib.sha256(email.encode("utf-8")).hexdigest() + ".credential"
            inventory["runtime/credentials/" + credential_name] = self._fingerprint((credential + "\n").encode(), 0o600)
        return dict(sorted(inventory.items()))

    @staticmethod
    def _package_inventory(members: dict[str, bytes]) -> dict[str, dict[str, Any]]:
        return {
            absolute: CommercialOperator._fingerprint(members[source], 0o644)
            for absolute, source in sorted(PACKAGE_PATHS.items())
        }

    def _runtime_input_digest(
        self,
        manifest: dict[str, Any],
        material: dict[str, Any],
        certificates: dict[str, bytes],
        config: bytes,
        config_sha: str,
    ) -> str:
        managed = {
            hashlib.sha256(email.encode("utf-8")).hexdigest() + ".credential": _sha256((credential + "\n").encode())
            for email, credential in sorted(material["managed_credentials"].items())
        }
        relay = {
            route["exit_id"] + ".credential": _sha256((route["credential"] + "\n").encode())
            for route in material["relay_routes"]
        }
        identity = {
            "active_origin_ips_sha256": _sha256(_canonical(material["active_origin_ips"])),
            "certificate_sha256": {relative: _sha256(raw) for relative, raw in sorted(certificates.items())},
            "config_sha256": config_sha,
            "controller_source_ip_sha256": _sha256(_canonical(material["controller_source_ip"])),
            "environment_template_sha256": _sha256(self._environment_bytes("<RELEASE_ID>", config_sha)),
            "managed_credential_sha256": managed,
            "profile": self.profile.name,
            "relay_credential_sha256": relay,
            "source_commit": manifest["source_commit"],
            "validated_config_bytes_sha256": _sha256(config),
        }
        return _sha256(_canonical(identity))

    def _prepare(self) -> tuple[dict[str, Any], dict[str, bytes], dict[str, Any], dict[str, bytes], bytes, str, str, str]:
        manifest = self._bundle()
        members = self._snapshot_members(self.bundle_dir, manifest)
        material = self._material()
        certificates = self._certificate_bytes()
        config = self._render_config(material, members["templates/config.json.tmpl"])
        config_sha = _sha256(config)
        runtime_input_sha = self._runtime_input_digest(manifest, material, certificates, config, config_sha)
        release_id = manifest["source_commit"][:12] + "-" + self.profile.name.replace("-", "") + "-" + runtime_input_sha[:16]
        with tempfile.TemporaryDirectory(prefix="maestro-commercial-config-") as directory:
            xray_path = Path(directory) / "xray"
            config_path = Path(directory) / "config.json"
            xray_path.write_bytes(members["bin/xray"])
            xray_path.chmod(0o700)
            config_path.write_bytes(config)
            config_path.chmod(0o600)
            self.run(str(xray_path), "run", "-test", "-config", str(config_path))
        return manifest, members, material, certificates, config, config_sha, runtime_input_sha, release_id

    def plan(self) -> dict[str, Any]:
        manifest, _members, _material, _certificates, _config, config_sha, runtime_input_sha, release_id = self._prepare()
        return {
            "action": "plan",
            "artifact_source_commit": manifest["source_commit"],
            "config_sha256": config_sha,
            "firewall_mutation": "NONE",
            "firewall_stop_gate": {
                "agent": f"allow TCP {self.profile.agent_port} only from the controller source, then drop all other TCP {self.profile.agent_port}",
                "relay": f"allow TCP {self.profile.relay_port} only from the complete active Origin set, then drop all other TCP {self.profile.relay_port}",
                "required_before_apply": True,
            },
            "profile": self.profile.name,
            "proxy_target": self.profile.proxy_target,
            "release_id": release_id,
            "runtime_input_sha256": runtime_input_sha,
            "units": [self.profile.xray_unit, self.profile.agent_unit],
        }

    @staticmethod
    def _write(path: Path, raw: bytes, mode: int, owner: tuple[int, int] | None = None) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        temporary = path.with_name("." + path.name + ".tmp-" + str(os.getpid()))
        with open(temporary, "xb") as output:
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, mode)
        if owner is not None and os.name == "posix":
            os.chown(temporary, owner[0], owner[1])
        os.replace(temporary, path)

    @staticmethod
    def _protect_directory(path: Path, mode: int, owner: tuple[int, int]) -> None:
        path.mkdir(parents=True, exist_ok=True)
        os.chmod(path, mode)
        if os.name == "posix":
            os.chown(path, owner[0], owner[1])

    @staticmethod
    def _switch(pointer: Path, target: Path | None) -> None:
        pointer.parent.mkdir(parents=True, exist_ok=True)
        temporary = pointer.with_name("." + pointer.name + ".tmp-" + str(os.getpid()))
        if temporary.exists() or temporary.is_symlink():
            temporary.unlink()
        if target is None:
            if pointer.exists() or pointer.is_symlink():
                pointer.unlink()
            return
        os.symlink(os.path.relpath(target, pointer.parent), temporary)
        if os.name != "posix" and (pointer.exists() or pointer.is_symlink()):
            pointer.unlink()
        os.replace(temporary, pointer)

    def _current_target(self) -> Path | None:
        pointer = self._rooted(BASE_PATH + "/current")
        if not pointer.is_symlink():
            return None
        target = (pointer.parent / os.readlink(pointer)).resolve()
        releases = self._rooted(BASE_PATH + "/releases").resolve()
        if target.parent != releases or not target.is_dir():
            raise OperationError("current_pointer_invalid")
        return target

    @staticmethod
    def _file_matches(path: Path, expected: dict[str, Any]) -> bool:
        try:
            metadata = path.lstat()
            raw = path.read_bytes()
        except OSError:
            return False
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1:
            return False
        if len(raw) != expected.get("size_bytes") or _sha256(raw) != expected.get("sha256"):
            return False
        if os.name == "posix" and stat.S_IMODE(metadata.st_mode) != int(str(expected.get("mode", "0")), 8):
            return False
        return True

    def _release_inventory_matches(self, release: Path, inventory: dict[str, Any]) -> bool:
        if not isinstance(inventory, dict):
            return False
        actual = {
            item.relative_to(release).as_posix()
            for item in release.rglob("*")
            if item.is_file() or item.is_symlink()
        }
        if actual != set(inventory) | {"release.json"}:
            return False
        try:
            release_metadata = (release / "release.json").lstat()
        except OSError:
            return False
        if not stat.S_ISREG(release_metadata.st_mode) or release_metadata.st_nlink != 1:
            return False
        if os.name == "posix" and stat.S_IMODE(release_metadata.st_mode) != 0o640:
            return False
        return all(self._file_matches(release / relative, expected) for relative, expected in inventory.items())

    def _package_inventory_matches(self, inventory: dict[str, Any]) -> bool:
        return isinstance(inventory, dict) and all(
            self._file_matches(self._rooted(absolute), expected)
            for absolute, expected in inventory.items()
        )

    def _stage_release(
        self,
        manifest: dict[str, Any],
        members: dict[str, bytes],
        material: dict[str, Any],
        certificates: dict[str, bytes],
        config: bytes,
        config_sha: str,
        runtime_input_sha: str,
        release_id: str,
    ) -> Path:
        releases = self._rooted(BASE_PATH + "/releases")
        releases.mkdir(parents=True, exist_ok=True)
        final = releases / release_id
        environment = self._environment_bytes(release_id, config_sha)
        release_metadata = {
            "agent_sha256": _sha256(members["bin/maestro-xray-cdn-agent"]),
            "config_sha256": config_sha,
            "package_inventory": self._package_inventory(members),
            "profile": self.profile.name,
            "release_inventory": self._release_inventory(members, material, certificates, config, environment),
            "release_id": release_id,
            "runtime_input_sha256": runtime_input_sha,
            "schema": "maestro-xray-cdn-commercial-release-v2",
            "source_commit": manifest["source_commit"],
            "xray_sha256": _sha256(members["bin/xray"]),
        }
        if final.exists() or final.is_symlink():
            try:
                metadata = json.loads((final / "release.json").read_bytes())
                matches = (
                    not final.is_symlink()
                    and final.is_dir()
                    and metadata == release_metadata
                    and self._release_inventory_matches(final, release_metadata["release_inventory"])
                )
            except (OSError, json.JSONDecodeError):
                matches = False
            if not matches:
                raise OperationError("release_id_collision")
            return final
        stage = releases / (".stage-" + release_id + "-" + str(os.getpid()))
        if stage.exists():
            shutil.rmtree(stage)
        stage.mkdir(mode=0o750)
        root_owner = self.owner_resolver("root")
        xray_owner = self.owner_resolver("maestro-xray-cdn")
        agent_owner = self.owner_resolver("maestro-xray-cdn-agent")
        self._protect_directory(stage, 0o750, (root_owner[0], xray_owner[1]))
        for relative in ("runtime", "runtime/api-mtls", "runtime/relay-tls"):
            self._protect_directory(stage / relative, 0o750, (root_owner[0], xray_owner[1]))
        for relative in ("runtime/agent-server", "runtime/controller-ca", "runtime/relay-ca"):
            self._protect_directory(stage / relative, 0o750, agent_owner)
        for relative in ("runtime/credentials", "runtime/relay-credentials"):
            self._protect_directory(stage / relative, 0o700, agent_owner)
        self._write(stage / "xray", members["bin/xray"], 0o755, root_owner)
        self._write(stage / "maestro-xray-cdn-agent", members["bin/maestro-xray-cdn-agent"], 0o755, root_owner)
        self._write(stage / "config.json", config, 0o640, (root_owner[0], xray_owner[1]))
        for relative, raw in certificates.items():
            owner = agent_owner if relative.startswith(("agent-server/", "controller-ca/")) or "sidecar-agent" in relative or relative.endswith("server-ca.crt") else xray_owner
            mode = 0o600 if relative.endswith(".key") else 0o640
            self._write(stage / "runtime" / relative, raw, mode, owner)
        self._write(stage / "runtime/active-origin-ips.json", _canonical(material["active_origin_ips"]), 0o640, agent_owner)
        self._write(stage / "runtime/controller-source-ip.json", _canonical(material["controller_source_ip"]), 0o640, agent_owner)
        for route in material["relay_routes"]:
            self._write(stage / "runtime/relay-credentials" / (route["exit_id"] + ".credential"), (route["credential"] + "\n").encode(), 0o600, agent_owner)
        for email, credential in sorted(material["managed_credentials"].items()):
            credential_name = hashlib.sha256(email.encode("utf-8")).hexdigest() + ".credential"
            self._write(stage / "runtime/credentials" / credential_name, (credential + "\n").encode(), 0o600, agent_owner)
        self._write(stage / "agent.env", environment, 0o640, (root_owner[0], agent_owner[1]))
        self._write(stage / "release.json", _canonical(release_metadata), 0o640, (root_owner[0], xray_owner[1]))
        if not self._release_inventory_matches(stage, release_metadata["release_inventory"]):
            raise OperationError("stage_inventory_mismatch")
        os.replace(stage, final)
        return final

    def _package_file_plan(self, members: dict[str, bytes]) -> tuple[dict[str, dict[str, Any]], list[Path]]:
        inventory = self._package_inventory(members)
        created: list[Path] = []
        for absolute, expected in inventory.items():
            destination = self._rooted(absolute)
            if destination.exists() or destination.is_symlink():
                if not self._file_matches(destination, expected):
                    raise OperationError("package_file_conflict")
            else:
                created.append(destination)
        return inventory, created

    def _install_package_files(self, members: dict[str, bytes], created: list[Path]) -> None:
        root_owner = self.owner_resolver("root")
        created_set = set(created)
        for absolute, source in PACKAGE_PATHS.items():
            destination = self._rooted(absolute)
            if destination in created_set:
                self._write(destination, members[source], 0o644, root_owner)
        sysusers = self._rooted("/usr/lib/sysusers.d/maestro-xray-cdn-commercial.conf")
        self.run("systemd-sysusers", str(sysusers))

    def _write_state(self, current: str, previous: str) -> None:
        state = {"current_release_id": current, "last_known_good_release_id": previous, "schema": 1}
        self._write(self._rooted(STATE_PATH), _canonical(state), 0o600, self.owner_resolver("root"))

    def _unit_activity(self) -> dict[str, bool]:
        activity: dict[str, bool] = {}
        for unit in (self.profile.xray_unit, self.profile.agent_unit):
            try:
                self.run("systemctl", "is-active", "--quiet", unit)
                activity[unit] = True
            except Exception:
                activity[unit] = False
        return activity

    def _listeners_are_bound(self) -> bool:
        expected = {
            self.profile.xhttp_port,
            self.profile.api_port,
            self.profile.relay_port,
            self.profile.agent_port,
        }
        try:
            return bool(self.listener_probe(expected))
        except Exception:
            return False

    def _status_for_target(self, target: Path) -> dict[str, Any]:
        try:
            metadata = json.loads((target / "release.json").read_bytes())
        except (OSError, json.JSONDecodeError) as error:
            raise OperationError("status_unavailable") from error
        manifest = self._bundle()
        members = self._snapshot_members(self.bundle_dir, manifest)
        expected_package_inventory = self._package_inventory(members)
        release_inventory = metadata.get("release_inventory")
        package_inventory = metadata.get("package_inventory")
        release_inventory_ok = self._release_inventory_matches(target, release_inventory)
        package_contract_ok = package_inventory == expected_package_inventory
        package_inventory_ok = package_contract_ok and self._package_inventory_matches(package_inventory)
        unit_activity = self._unit_activity()
        listeners_bound = self._listeners_are_bound()
        expected_config = metadata.get("config_sha256", "")
        try:
            actual_config = _sha256((target / "config.json").read_bytes())
        except OSError:
            actual_config = ""
        metadata_ok = (
            metadata.get("schema") == "maestro-xray-cdn-commercial-release-v2"
            and metadata.get("profile") == self.profile.name
            and metadata.get("release_id") == target.name
            and re.fullmatch(r"[0-9a-f]{64}", str(metadata.get("runtime_input_sha256", ""))) is not None
            and metadata.get("agent_sha256") == _sha256(members["bin/maestro-xray-cdn-agent"])
            and metadata.get("xray_sha256") == _sha256(members["bin/xray"])
            and actual_config == expected_config
        )
        active = (
            metadata_ok
            and release_inventory_ok
            and package_inventory_ok
            and all(unit_activity.values())
            and listeners_bound
        )
        return {
            "actual_config_sha256": actual_config,
            "checks": {
                "listeners_bound": listeners_bound,
                "metadata": metadata_ok,
                "package_inventory": package_inventory_ok,
                "release_inventory": release_inventory_ok,
                "units_active": all(unit_activity.values()),
            },
            "expected_config_sha256": expected_config,
            "profile": self.profile.name,
            "proxy_target": self.profile.proxy_target,
            "release_id": target.name,
            "runtime_input_sha256": metadata.get("runtime_input_sha256", ""),
            "source_commit": metadata.get("source_commit", ""),
            "status": "ACTIVE" if active else "DRIFT",
        }

    def apply(self) -> dict[str, Any]:
        manifest, members, material, certificates, config, config_sha, runtime_input_sha, release_id = self._prepare()
        previous = self._current_target()
        previous_id = previous.name if previous else ABSENT_TARGET
        created_package_files: list[Path] = []
        current = self._rooted(BASE_PATH + "/current")
        try:
            _package_inventory, created_package_files = self._package_file_plan(members)
            self._install_package_files(members, created_package_files)
            release = self._stage_release(
                manifest, members, material, certificates, config, config_sha, runtime_input_sha, release_id
            )
            self._switch(current, release)
            self.run("systemctl", "daemon-reload")
            self.run("systemctl", "enable", self.profile.xray_unit, self.profile.agent_unit)
            self.run("systemctl", "start", self.profile.xray_unit)
            self.run("systemctl", "start", self.profile.agent_unit)
            if self._status_for_target(release)["status"] != "ACTIVE":
                raise OperationError("post_apply_verification_failed")
            self._write_state(release_id, previous_id)
        except Exception as error:
            self._switch(current, previous)
            try:
                self.run("systemctl", "stop", self.profile.agent_unit, self.profile.xray_unit)
                if previous is not None:
                    self.run("systemctl", "start", self.profile.xray_unit)
                    self.run("systemctl", "start", self.profile.agent_unit)
                else:
                    self.run("systemctl", "disable", self.profile.agent_unit, self.profile.xray_unit)
                    for package_file in created_package_files:
                        if package_file.exists() or package_file.is_symlink():
                            package_file.unlink()
                self.run("systemctl", "daemon-reload")
            except Exception:
                pass
            if isinstance(error, OperationError):
                raise
            raise OperationError("apply_failed") from error
        return self.status()

    def status(self) -> dict[str, Any]:
        target = self._current_target()
        if target is None:
            unit_activity = self._unit_activity()
            listeners_bound = self._listeners_are_bound()
            absent = not any(unit_activity.values()) and not listeners_bound
            return {
                "checks": {
                    "listeners_absent": not listeners_bound,
                    "units_inactive": not any(unit_activity.values()),
                },
                "profile": self.profile.name,
                "proxy_target": self.profile.proxy_target,
                "status": "ABSENT" if absent else "DRIFT",
            }
        return self._status_for_target(target)

    def rollback(self) -> dict[str, Any]:
        self._bundle()
        state_path = self._rooted(STATE_PATH)
        try:
            state = json.loads(state_path.read_bytes())
        except (OSError, json.JSONDecodeError) as error:
            raise OperationError("rollback_state_unavailable") from error
        previous_id = state.get("last_known_good_release_id")
        current = self._current_target()
        if not isinstance(previous_id, str) or not previous_id:
            raise OperationError("rollback_target_unavailable")
        if previous_id == ABSENT_TARGET:
            try:
                self.run("systemctl", "stop", self.profile.agent_unit, self.profile.xray_unit)
                self.run("systemctl", "disable", self.profile.agent_unit, self.profile.xray_unit)
                self._switch(self._rooted(BASE_PATH + "/current"), None)
                self.run("systemctl", "daemon-reload")
            except Exception as error:
                if current is not None:
                    self._switch(self._rooted(BASE_PATH + "/current"), current)
                    try:
                        self.run("systemctl", "start", self.profile.xray_unit)
                        self.run("systemctl", "start", self.profile.agent_unit)
                    except Exception:
                        pass
                raise OperationError("rollback_absent_failed") from error
            self._write_state(ABSENT_TARGET, current.name if current else ABSENT_TARGET)
            return self.status()
        previous = self._rooted(BASE_PATH + "/releases/" + previous_id)
        if not previous.is_dir():
            raise OperationError("rollback_target_unavailable")
        self._switch(self._rooted(BASE_PATH + "/current"), previous)
        try:
            self.run("systemctl", "restart", self.profile.xray_unit)
            self.run("systemctl", "restart", self.profile.agent_unit)
            if self._status_for_target(previous)["status"] != "ACTIVE":
                raise OperationError("rollback_health_failed")
        except Exception as error:
            if current is not None:
                self._switch(self._rooted(BASE_PATH + "/current"), current)
                try:
                    self.run("systemctl", "restart", self.profile.xray_unit)
                    self.run("systemctl", "restart", self.profile.agent_unit)
                except Exception:
                    pass
            raise OperationError("rollback_start_failed") from error
        self._write_state(previous_id, current.name if current else ABSENT_TARGET)
        return self.status()


def _cli() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("plan", "apply", "status", "rollback"))
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--profile", choices=("standard", "s4-commercial"), required=True)
    parser.add_argument("--runtime-material")
    parser.add_argument("--certificate-source")
    args = parser.parse_args()
    if args.command in {"plan", "apply"} and (not args.runtime_material or not args.certificate_source):
        parser.error("plan/apply require --runtime-material and --certificate-source")
    if args.command in {"apply", "rollback"} and os.name == "posix" and os.geteuid() != 0:
        print('{"code":"root_required","status":"STOP"}', file=os.sys.stderr)
        return 4
    try:
        operator = CommercialOperator(
            root=Path("/"), bundle_dir=Path(args.bundle), profile=args.profile,
            runtime_material=Path(args.runtime_material) if args.runtime_material else None,
            certificate_source=Path(args.certificate_source) if args.certificate_source else None,
        )
        result = getattr(operator, args.command)()
    except (OperationError, OSError) as error:
        print(json.dumps({"code": str(error), "status": "STOP"}, separators=(",", ":"), sort_keys=True), file=os.sys.stderr)
        return 4
    print(json.dumps(result, separators=(",", ":"), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(_cli())
