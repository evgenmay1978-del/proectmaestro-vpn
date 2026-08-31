from __future__ import annotations

import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import urllib.request

from ops.ha import build_manifest, pki_verify


ROOT = Path(__file__).resolve().parents[3]
CANONICAL_ROOT = Path(pki_verify.__file__).resolve().parents[2]
SOURCE_TEMPLATES = CANONICAL_ROOT / "deploy" / "ha"
MODULE_PATH = ROOT / "ops" / "ha" / "deploy_node.py"
SPEC = importlib.util.spec_from_file_location("task15b_deploy_node", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("deploy-node-test:module-load-failed")
deploy_node = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = deploy_node
SPEC.loader.exec_module(deploy_node)


REPOSITORY = "evgenmay1978-del/proectmaestro-vpn"
REF = "refs/heads/codex/yandex-cdn-whitelist-task3-sync"
COMMIT_SHA = "f577c67ad229fe89278430d35a3ec65f6ce454e5"
TEMPLATE_SOURCE_SHA = "8289ce78be8dcb2c00829d6b9781d4b52a18cb73"
ARCHIVE_SHA = "2" * 64
RUN_ID = 33327019392
RUN_ATTEMPT = 1
ARTIFACT_ID = 9736614530
ARTIFACT_NAME = f"maestro-panel-{COMMIT_SHA}"
TEMPLATE_NAMES = (
    "maestro-panel.env.example",
    "maestro-panel.service",
    "rqlite-s2.env.example",
    "rqlite-s3.env.example",
    "rqlite-s4.env.example",
    "rqlited@.service",
)
FIXED_BLOCKERS = [
    "artifact-attestation-required",
    "deployment-not-authorized",
    "node-identity-not-verified",
    "pki-private-material-not-provisioned",
    "rqlite-membership-not-provisioned",
    "runtime-smoke-not-verified",
    "service-activation-not-authorized",
    "template-rendering-not-implemented",
]


def canonical(value: object) -> bytes:
    return (
        json.dumps(
            value,
            sort_keys=True,
            separators=(",", ":"),
            ensure_ascii=True,
            allow_nan=False,
        )
        + "\n"
    ).encode("utf-8")


def write_amd64_elf(path: Path) -> bytes:
    header = bytearray(64)
    header[:4] = b"\x7fELF"
    header[4] = 2
    header[5] = 1
    header[6] = 1
    header[16:18] = (2).to_bytes(2, "little")
    header[18:20] = (0x3E).to_bytes(2, "little")
    header[20:24] = (1).to_bytes(4, "little")
    header[32:40] = (64).to_bytes(8, "little")
    header[52:54] = (64).to_bytes(2, "little")
    header[54:56] = (56).to_bytes(2, "little")
    header[56:58] = (1).to_bytes(2, "little")

    segment = b"offline-deploy-node-synthetic-panel"
    program = bytearray(56)
    program[0:4] = (1).to_bytes(4, "little")
    program[4:8] = (5).to_bytes(4, "little")
    program[8:16] = (120).to_bytes(8, "little")
    program[16:24] = (0x400078).to_bytes(8, "little")
    program[24:32] = (0x400078).to_bytes(8, "little")
    program[32:40] = len(segment).to_bytes(8, "little")
    program[40:48] = len(segment).to_bytes(8, "little")
    program[48:56] = (0x1000).to_bytes(8, "little")
    payload = bytes(header) + bytes(program) + segment
    path.write_bytes(payload)
    path.chmod(0o755)
    return payload


def valid_pki_evidence() -> dict[str, object]:
    profile = pki_verify.example_profile()
    domains: list[dict[str, object]] = []
    for domain in profile["trust_domains"]:
        domain_name = domain["name"]
        certificates: list[dict[str, object]] = []
        for index, certificate in enumerate(domain["certificates"]):
            role = certificate["role"]
            certificates.append(
                {
                    "certificate_fingerprint_sha256": hashlib.sha256(
                        ("leaf:" + role).encode("ascii")
                    ).hexdigest(),
                    "certificate_serial_hex": format(index + 2, "x"),
                    "certificate_spki_sha256": hashlib.sha256(
                        ("spki:leaf:" + role).encode("ascii")
                    ).hexdigest(),
                    "not_after": "2027-08-30T00:00:00Z",
                    "role": role,
                }
            )
        domains.append(
            {
                "ca_fingerprint_sha256": hashlib.sha256(
                    ("ca:" + domain_name).encode("ascii")
                ).hexdigest(),
                "ca_not_after": "2027-08-30T00:00:00Z",
                "ca_serial_hex": "1",
                "ca_spki_sha256": hashlib.sha256(
                    ("spki:ca:" + domain_name).encode("ascii")
                ).hexdigest(),
                "certificates": certificates,
                "name": domain_name,
            }
        )
    return {
        "blockers": ["deployment-not-authorized"],
        "deployment_authorized": False,
        "evaluation_time": "2026-08-30T00:00:00Z",
        "openssl_version": "OpenSSL 3.0.13 30 Jan 2024",
        "profile_sha256": "f" * 64,
        "release_readiness": "NO_GO",
        "schema": "maestro-ha-pki-evidence-v1",
        "trust_domains": domains,
    }


class DeployNodePlanTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.base = Path(self.temporary.name)
        self.artifact_root = self.base / "artifact"
        self.artifact_root.mkdir(mode=0o700)
        self.panel = self.artifact_root / "maestro-panel"
        self.manifest_path = self.artifact_root / "manifest.json"
        self.templates_root = self.base / "templates"
        self.templates_root.mkdir(mode=0o700)
        self.inventory_path = self.base / "inventory.json"
        self.transport_path = self.base / "transport.json"
        self.pki_path = self.base / "pki-evidence.json"

        self.panel_bytes = write_amd64_elf(self.panel)
        self.manifest = build_manifest.build_manifest(
            self.artifact_root,
            repository=REPOSITORY,
            ref=REF,
            commit_sha=COMMIT_SHA,
            workflow_run_id=RUN_ID,
            workflow_run_attempt=RUN_ATTEMPT,
            go_version="go1.25.0",
        )
        self._write_json(self.manifest_path, self.manifest)
        for name in TEMPLATE_NAMES:
            payload = (SOURCE_TEMPLATES / name).read_bytes().replace(b"\r\n", b"\n")
            if b"\r" in payload or payload.startswith(b"\xef\xbb\xbf"):
                raise AssertionError(f"non-canonical source template: {name}")
            (self.templates_root / name).write_bytes(payload)

        self.inventory = self._inventory("s2")
        self.transport = self._transport()
        self.pki_evidence = valid_pki_evidence()
        self._write_inputs()

    def tearDown(self) -> None:
        self.temporary.cleanup()

    @staticmethod
    def _write_json(path: Path, value: object, *, raw: bytes | None = None) -> None:
        path.write_bytes(canonical(value) if raw is None else raw)
        path.chmod(0o600)

    @staticmethod
    def _artifact_identity() -> dict[str, object]:
        return {
            "archive_sha256": ARCHIVE_SHA,
            "artifact_id": ARTIFACT_ID,
            "artifact_name": ARTIFACT_NAME,
            "commit_sha": COMMIT_SHA,
            "ref": REF,
            "repository": REPOSITORY,
            "workflow_run_attempt": RUN_ATTEMPT,
            "workflow_run_id": RUN_ID,
        }

    @classmethod
    def _inventory(cls, node_id: str) -> dict[str, object]:
        return {
            "artifact": cls._artifact_identity(),
            "logical_addresses": {
                "rqlite_http": f"rqlite-http-{node_id}",
                "rqlite_raft": f"rqlite-raft-{node_id}",
            },
            "node_id": node_id,
            "role": "rqlite-voter",
            "schema": "maestro-ha-node-inventory-v1",
            "target": "linux/amd64",
            "templates": [
                "maestro-panel.env.example",
                "maestro-panel.service",
                f"rqlite-{node_id}.env.example",
                "rqlited@.service",
            ],
        }

    @classmethod
    def _transport(cls) -> dict[str, object]:
        value = cls._artifact_identity()
        value.update(
            {
                "members": ["maestro-panel", "manifest.json"],
                "schema": "maestro-ha-artifact-transport-evidence-v1",
            }
        )
        return value

    def _write_inputs(self) -> None:
        self._write_json(self.inventory_path, self.inventory)
        self._write_json(self.transport_path, self.transport)
        self._write_json(self.pki_path, self.pki_evidence)

    def _plan(self) -> dict[str, object]:
        return deploy_node.create_plan(
            inventory_path=self.inventory_path,
            transport_evidence_path=self.transport_path,
            artifact_root=self.artifact_root,
            manifest_path=self.manifest_path,
            pki_evidence_path=self.pki_path,
            templates_root=self.templates_root,
        )

    def _arguments(self) -> list[str]:
        return [
            "plan",
            "--inventory",
            str(self.inventory_path),
            "--transport-evidence",
            str(self.transport_path),
            "--artifact-root",
            str(self.artifact_root),
            "--manifest",
            str(self.manifest_path),
            "--pki-evidence",
            str(self.pki_path),
            "--templates-root",
            str(self.templates_root),
        ]

    def _assert_invalid(self) -> None:
        with self.assertRaisesRegex(
            deploy_node.DeployPlanError,
            r"^deploy-node:invalid-(?:input|inventory|transport|artifact|pki|templates)$",
        ):
            self._plan()

    def test_plan_is_canonical_deterministic_redacted_and_binds_every_input(self) -> None:
        first = self._plan()
        second = self._plan()
        self.assertEqual(first, second)
        self.assertEqual(
            set(first),
            {
                "artifact",
                "authorized",
                "blockers",
                "files",
                "node_id",
                "node_inventory_sha256",
                "pki",
                "release_readiness",
                "schema",
                "template_digests",
                "template_source_commit_sha",
            },
        )
        self.assertEqual(first["schema"], "maestro-ha-deploy-plan-v1")
        self.assertEqual(first["node_id"], "s2")
        self.assertIs(first["authorized"], False)
        self.assertEqual(first["release_readiness"], "NO_GO")
        self.assertEqual(first["blockers"], FIXED_BLOCKERS)
        self.assertEqual(first["template_source_commit_sha"], TEMPLATE_SOURCE_SHA)
        self.assertEqual(
            first["node_inventory_sha256"],
            hashlib.sha256(canonical(self.inventory)).hexdigest(),
        )

        artifact = first["artifact"]
        self.assertEqual(
            artifact,
            {
                "archive_sha256": ARCHIVE_SHA,
                "artifact_id": ARTIFACT_ID,
                "artifact_name": ARTIFACT_NAME,
                "binary_sha256": hashlib.sha256(self.panel_bytes).hexdigest(),
                "binary_size_bytes": len(self.panel_bytes),
                "head_commit_sha": COMMIT_SHA,
                "manifest_sha256": hashlib.sha256(canonical(self.manifest)).hexdigest(),
                "manifest_size_bytes": len(canonical(self.manifest)),
                "ref": REF,
                "repository": REPOSITORY,
                "workflow_run_attempt": RUN_ATTEMPT,
                "workflow_run_id": RUN_ID,
            },
        )
        self.assertEqual(
            first["pki"],
            {
                "evaluation_time": "2026-08-30T00:00:00Z",
                "evidence_sha256": hashlib.sha256(canonical(self.pki_evidence)).hexdigest(),
                "openssl_version": "OpenSSL 3.0.13 30 Jan 2024",
                "profile_sha256": "f" * 64,
                "required_roles": [
                    "s2-http-server",
                    "s2-panel-rqlite-client",
                    "s2-raft-peer",
                ],
            },
        )

        template_digests = first["template_digests"]
        self.assertEqual([item["name"] for item in template_digests], list(TEMPLATE_NAMES))
        for item in template_digests:
            payload = (self.templates_root / item["name"]).read_bytes()
            self.assertEqual(item["sha256"], hashlib.sha256(payload).hexdigest())
            self.assertEqual(item["size_bytes"], len(payload))

        files = first["files"]
        self.assertEqual(
            [item["destination"] for item in files],
            sorted(item["destination"] for item in files),
        )
        self.assertEqual(
            {(item["destination"], item["owner"], item["group"], item["mode"]) for item in files},
            {
                (
                    f"/opt/maestro/ha/releases/{COMMIT_SHA}/maestro-panel",
                    "root",
                    "root",
                    "0755",
                ),
                (
                    f"/opt/maestro/ha/releases/{COMMIT_SHA}/manifest.json",
                    "root",
                    "root",
                    "0644",
                ),
                ("/etc/maestro/ha/maestro-panel.env", "root", "maestro-panel", "0640"),
                ("/etc/maestro/ha/rqlite-s2.env", "root", "maestro-rqlite", "0640"),
                ("/etc/systemd/system/maestro-panel.service", "root", "root", "0644"),
                ("/etc/systemd/system/rqlited@.service", "root", "root", "0644"),
            },
        )

        encoded = deploy_node.canonical_bytes(first)
        self.assertEqual(encoded, canonical(first))
        text = encoded.decode("ascii")
        self.assertNotIn(str(self.base), text)
        self.assertNotIn("__S2_PRIVATE_IP__", text)
        self.assertNotIn("s2-rqlite-http.internal", text)
        self.assertNotIn("s2-rqlite-raft.internal", text)
        for forbidden in (
            "systemctl ",
            "chmod ",
            "chown ",
            "firewall-cmd ",
            "ufw ",
            "ssh ",
            "curl ",
            "olcrtc",
            "wdtt",
        ):
            self.assertNotIn(forbidden, text)

    def test_every_node_has_fixed_logical_ids_templates_destinations_and_pki_roles(self) -> None:
        for node_id in ("s2", "s3", "s4"):
            with self.subTest(node_id=node_id):
                self.inventory = self._inventory(node_id)
                self._write_inputs()
                plan = self._plan()
                self.assertEqual(plan["node_id"], node_id)
                self.assertEqual(
                    plan["pki"]["required_roles"],
                    [
                        f"{node_id}-http-server",
                        f"{node_id}-panel-rqlite-client",
                        f"{node_id}-raft-peer",
                    ],
                )
                destinations = {item["destination"] for item in plan["files"]}
                self.assertIn(f"/etc/maestro/ha/rqlite-{node_id}.env", destinations)

    def test_rejects_noncanonical_duplicate_or_unknown_inventory_and_wrong_fixed_fields(self) -> None:
        cases: list[bytes] = []
        cases.append(canonical(self.inventory).replace(b'"node_id":"s2"', b'"node_id":"s2","node_id":"s2"'))
        cases.append(json.dumps(self.inventory, indent=2).encode("utf-8"))
        for mutate in (
            lambda value: value.update({"unknown": True}),
            lambda value: value.update({"schema": "wrong"}),
            lambda value: value.update({"node_id": "s1"}),
            lambda value: value.update({"role": "leader"}),
            lambda value: value.update({"target": "linux/arm64"}),
            lambda value: value["logical_addresses"].update({"rqlite_http": "rqlite-http-s3"}),
            lambda value: value["logical_addresses"].update({"extra": "value"}),
            lambda value: value.update({"templates": value["templates"][:-1]}),
            lambda value: value.update({"templates": value["templates"] + ["extra.service"]}),
            lambda value: value["artifact"].update({"artifact_id": 0}),
            lambda value: value["artifact"].update({"artifact_name": "other"}),
            lambda value: value["artifact"].update({"archive_sha256": "A" * 64}),
        ):
            value = copy.deepcopy(self.inventory)
            mutate(value)
            cases.append(canonical(value))
        for raw in cases:
            with self.subTest(raw=hashlib.sha256(raw).hexdigest()[:12]):
                self._write_json(self.inventory_path, self.inventory, raw=raw)
                self._assert_invalid()

    def test_transport_evidence_is_strict_and_must_match_inventory_and_bundle_members(self) -> None:
        mutations = (
            lambda value: value.update({"extra": True}),
            lambda value: value.update({"schema": "wrong"}),
            lambda value: value.update({"artifact_id": ARTIFACT_ID + 1}),
            lambda value: value.update({"archive_sha256": "3" * 64}),
            lambda value: value.update({"members": ["manifest.json", "maestro-panel"]}),
            lambda value: value.update({"members": ["maestro-panel"]}),
            lambda value: value.update({"head_sha": COMMIT_SHA}),
        )
        for mutate in mutations:
            value = copy.deepcopy(self.transport)
            mutate(value)
            self._write_json(self.transport_path, value)
            with self.subTest(mutation=repr(mutate)):
                self._assert_invalid()

    def test_real_build_manifest_verifier_rejects_member_and_artifact_tampering(self) -> None:
        (self.artifact_root / "unexpected").write_bytes(b"unexpected")
        self._assert_invalid()
        (self.artifact_root / "unexpected").unlink()
        self.panel.write_bytes(self.panel_bytes + b"tampered")
        self._assert_invalid()

    def test_pki_evidence_must_be_canonical_valid_and_have_thirty_days_remaining(self) -> None:
        self.pki_evidence["trust_domains"][0]["ca_not_after"] = "2026-09-01T00:00:00Z"
        self._write_json(self.pki_path, self.pki_evidence)
        self._assert_invalid()

        self.pki_evidence = valid_pki_evidence()
        self.pki_evidence["unknown"] = True
        self._write_json(self.pki_path, self.pki_evidence)
        self._assert_invalid()

    def test_template_root_requires_exact_regular_single_link_members_and_stable_digests(self) -> None:
        extra = self.templates_root / "extra.conf"
        extra.write_bytes(b"extra")
        self._assert_invalid()
        extra.unlink()

        selected = self.templates_root / "rqlite-s2.env.example"
        selected.unlink()
        self._assert_invalid()

    def test_templates_and_artifact_are_pinned_to_independently_reviewed_commits(self) -> None:
        selected = self.templates_root / "maestro-panel.service"
        selected.write_bytes(selected.read_bytes() + b"# one-byte-drift\n")
        self._assert_invalid()

        selected.write_bytes(
            (SOURCE_TEMPLATES / selected.name)
            .read_bytes()
            .replace(b"\r\n", b"\n")
        )
        other_commit = "3" * 40
        other_identity = self._artifact_identity()
        other_identity["commit_sha"] = other_commit
        other_identity["artifact_name"] = f"maestro-panel-{other_commit}"
        self.inventory["artifact"] = copy.deepcopy(other_identity)
        self.transport = copy.deepcopy(other_identity)
        self.transport.update(
            {
                "members": ["maestro-panel", "manifest.json"],
                "schema": "maestro-ha-artifact-transport-evidence-v1",
            }
        )
        self.manifest_path.unlink()
        self.manifest = build_manifest.build_manifest(
            self.artifact_root,
            repository=REPOSITORY,
            ref=REF,
            commit_sha=other_commit,
            workflow_run_id=RUN_ID,
            workflow_run_attempt=RUN_ATTEMPT,
            go_version="go1.25.0",
        )
        self._write_json(self.manifest_path, self.manifest)
        self._write_inputs()
        self._assert_invalid()

    @unittest.skipUnless(hasattr(os, "link"), "hard links unavailable")
    def test_template_hardlink_is_rejected(self) -> None:
        selected = self.templates_root / "rqlite-s2.env.example"
        original = selected.read_bytes()
        selected.unlink()
        source = self.base / "hardlink-source"
        source.write_bytes(original)
        os.link(source, selected)
        self._assert_invalid()

    def test_planning_uses_no_process_network_or_filesystem_mutation_seam(self) -> None:
        before = sorted(
            (
                str(path.relative_to(self.base)),
                hashlib.sha256(path.read_bytes()).hexdigest(),
            )
            for path in self.base.rglob("*")
            if path.is_file()
        )
        forbidden = AssertionError("deploy-node-test:mutation-seam-used")
        with (
            mock.patch.object(subprocess, "Popen", side_effect=forbidden),
            mock.patch.object(subprocess, "run", side_effect=forbidden),
            mock.patch.object(socket, "socket", side_effect=forbidden),
            mock.patch.object(socket, "create_connection", side_effect=forbidden),
            mock.patch.object(socket, "getaddrinfo", side_effect=forbidden),
            mock.patch.object(urllib.request, "urlopen", side_effect=forbidden),
            mock.patch.object(os, "system", side_effect=forbidden),
            mock.patch.object(os, "replace", side_effect=forbidden),
            mock.patch.object(os, "rename", side_effect=forbidden),
            mock.patch.object(os, "remove", side_effect=forbidden),
            mock.patch.object(os, "unlink", side_effect=forbidden),
            mock.patch.object(os, "mkdir", side_effect=forbidden),
            mock.patch.object(os, "makedirs", side_effect=forbidden),
            mock.patch.object(Path, "write_bytes", side_effect=forbidden),
            mock.patch.object(Path, "write_text", side_effect=forbidden),
        ):
            self._plan()
        after = sorted(
            (
                str(path.relative_to(self.base)),
                hashlib.sha256(path.read_bytes()).hexdigest(),
            )
            for path in self.base.rglob("*")
            if path.is_file()
        )
        self.assertEqual(after, before)

    def test_cli_emits_only_canonical_stdout(self) -> None:
        stdout = io.StringIO()
        stderr = io.StringIO()
        result = deploy_node.main(self._arguments(), stdout=stdout, stderr=stderr)
        self.assertEqual(result, 0)
        self.assertEqual(stderr.getvalue(), "")
        parsed = json.loads(stdout.getvalue())
        self.assertEqual(stdout.getvalue().encode("ascii"), canonical(parsed))

    def test_cli_rejects_non_plan_commands_and_extra_positionals_before_any_read(self) -> None:
        for arguments in (["apply"], ["status"], [], ["plan", "extra"]):
            with self.subTest(arguments=arguments):
                stdout = io.StringIO()
                stderr = io.StringIO()
                with mock.patch.object(
                    deploy_node,
                    "_read_regular_file",
                    side_effect=AssertionError("deploy-node-test:read-before-command-reject"),
                ):
                    result = deploy_node.main(arguments, stdout=stdout, stderr=stderr)
                self.assertEqual(result, 2)
                self.assertEqual(stdout.getvalue(), "")
                self.assertEqual(stderr.getvalue(), "deploy-node:invalid-input\n")

if __name__ == "__main__":
    unittest.main()
