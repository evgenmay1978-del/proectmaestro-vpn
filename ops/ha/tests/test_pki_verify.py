from __future__ import annotations

import base64
import copy
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from ops.ha import pki_verify as pki
from ops.ha.pki_verify import (
    OpenSSLResult,
    PKIVerificationError,
    canonical_bytes,
    validate_evidence,
    validate_profile,
    verify_directory,
)


SERVER_AUTH = "1.3.6.1.5.5.7.3.1"
CLIENT_AUTH = "1.3.6.1.5.5.7.3.2"
PROFILE_SCHEMA = "maestro-ha-pki-profile-v1"
EVIDENCE_SCHEMA = "maestro-ha-pki-evidence-v1"
ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "ops" / "ha" / "pki-verify.py"
EXAMPLE = ROOT / "deploy" / "ha" / "pki-profile.json.example"
GITATTRIBUTES = ROOT / ".gitattributes"
IS_LINUX = os.name == "posix" and sys.platform.startswith("linux")

ROLE_MATRIX = {
    "bot-gateway": {
        "s2-bot-gateway-server": ("server", (SERVER_AUTH,)),
        "s2-telegram-bot-primary-client": ("client", (CLIENT_AUTH,)),
        "s2-telegram-bot-secondary-client": ("client", (CLIENT_AUTH,)),
        "s3-bot-gateway-server": ("server", (SERVER_AUTH,)),
        "s3-telegram-bot-primary-client": ("client", (CLIENT_AUTH,)),
        "s3-telegram-bot-secondary-client": ("client", (CLIENT_AUTH,)),
        "s4-bot-gateway-server": ("server", (SERVER_AUTH,)),
        "s4-telegram-bot-primary-client": ("client", (CLIENT_AUTH,)),
        "s4-telegram-bot-secondary-client": ("client", (CLIENT_AUTH,)),
    },
    "dispatcher": {
        "s2-controlplane-dispatcher-client": ("client", (CLIENT_AUTH,)),
        "s3-controlplane-dispatcher-client": ("client", (CLIENT_AUTH,)),
        "s4-controlplane-dispatcher-client": ("client", (CLIENT_AUTH,)),
    },
    "github-probe": {
        "github-workflow-probe-client": ("client", (CLIENT_AUTH,)),
    },
    "lease-verifier": {
        "s1-agent-lease-client": ("client", (CLIENT_AUTH,)),
        "s2-agent-lease-client": ("client", (CLIENT_AUTH,)),
        "s2-lease-verifier-server": ("server", (SERVER_AUTH,)),
        "s3-agent-lease-client": ("client", (CLIENT_AUTH,)),
        "s3-lease-verifier-server": ("server", (SERVER_AUTH,)),
        "s4-agent-lease-client": ("client", (CLIENT_AUTH,)),
        "s4-lease-verifier-server": ("server", (SERVER_AUTH,)),
    },
    "node-status": {
        "s1-agent-status-server": ("server", (SERVER_AUTH,)),
        "s2-agent-status-server": ("server", (SERVER_AUTH,)),
        "s3-agent-status-server": ("server", (SERVER_AUTH,)),
        "s4-agent-status-server": ("server", (SERVER_AUTH,)),
    },
    "rqlite-http": {
        "importer-rqlite-client": ("client", (CLIENT_AUTH,)),
        "s2-backup-rqlite-client": ("client", (CLIENT_AUTH,)),
        "s2-http-server": ("server", (SERVER_AUTH,)),
        "s2-panel-rqlite-client": ("client", (CLIENT_AUTH,)),
        "s3-backup-rqlite-client": ("client", (CLIENT_AUTH,)),
        "s3-http-server": ("server", (SERVER_AUTH,)),
        "s3-panel-rqlite-client": ("client", (CLIENT_AUTH,)),
        "s4-backup-rqlite-client": ("client", (CLIENT_AUTH,)),
        "s4-http-server": ("server", (SERVER_AUTH,)),
        "s4-panel-rqlite-client": ("client", (CLIENT_AUTH,)),
    },
    "rqlite-raft": {
        "s2-raft-peer": ("peer", (SERVER_AUTH, CLIENT_AUTH)),
        "s3-raft-peer": ("peer", (SERVER_AUTH, CLIENT_AUTH)),
        "s4-raft-peer": ("peer", (SERVER_AUTH, CLIENT_AUTH)),
    },
}

FIXED_DNS_SANS = {
    "github-workflow-probe-client": ("workflow-probe",),
    "s2-controlplane-dispatcher-client": ("controlplane-dispatcher",),
    "s2-telegram-bot-primary-client": ("bot-primary",),
    "s2-telegram-bot-secondary-client": ("bot-secondary",),
    "s3-controlplane-dispatcher-client": ("controlplane-dispatcher",),
    "s3-telegram-bot-primary-client": ("bot-primary",),
    "s3-telegram-bot-secondary-client": ("bot-secondary",),
    "s4-controlplane-dispatcher-client": ("controlplane-dispatcher",),
    "s4-telegram-bot-primary-client": ("bot-primary",),
    "s4-telegram-bot-secondary-client": ("bot-secondary",),
    "s2-http-server": ("s2-rqlite-http.internal",),
    "s2-raft-peer": ("s2-rqlite-raft.internal",),
    "s3-http-server": ("s3-rqlite-http.internal",),
    "s3-raft-peer": ("s3-rqlite-raft.internal",),
    "s4-http-server": ("s4-rqlite-http.internal",),
    "s4-raft-peer": ("s4-rqlite-raft.internal",),
}


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


def complete_profile() -> dict[str, object]:
    domains: list[dict[str, object]] = []
    for domain_name in sorted(ROLE_MATRIX):
        certificates: list[dict[str, object]] = []
        for role in sorted(ROLE_MATRIX[domain_name]):
            purpose, eku = ROLE_MATRIX[domain_name][role]
            fixed_dns = FIXED_DNS_SANS.get(role)
            dns_sans = (
                list(fixed_dns)
                if fixed_dns is not None
                else ([role + ".invalid"] if purpose in {"server", "peer"} else [])
            )
            certificates.append(
                {
                    "certificate": role + ".pem",
                    "dns_sans": dns_sans,
                    "eku_oids": list(eku),
                    "ip_sans": [],
                    "purpose": purpose,
                    "role": role,
                    "uri_sans": [],
                }
            )
        domains.append(
            {
                "ca_certificate": domain_name + "-ca.pem",
                "certificates": certificates,
                "name": domain_name,
            }
        )
    return {
        "deployment_authorized": False,
        "evaluation_time": "2026-08-30T00:00:00Z",
        "minimum_remaining_seconds": 2592000,
        "release_readiness": "NO_GO",
        "schema": PROFILE_SCHEMA,
        "trust_domains": domains,
    }


def complete_evidence(profile: dict[str, object] | None = None) -> dict[str, object]:
    profile_value = complete_profile() if profile is None else profile
    domains: list[dict[str, object]] = []
    for domain in profile_value["trust_domains"]:
        domain_name = domain["name"]
        certificates = [
            {
                "certificate_fingerprint_sha256": hashlib.sha256(
                    ("leaf:" + entry["role"]).encode("ascii")
                ).hexdigest(),
                "certificate_serial_hex": format(index + 2, "x"),
                "certificate_spki_sha256": hashlib.sha256(
                    ("spki:leaf:" + entry["role"]).encode("ascii")
                ).hexdigest(),
                "not_after": "2027-08-30T00:00:00Z",
                "role": entry["role"],
            }
            for index, entry in enumerate(domain["certificates"])
        ]
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
        "evaluation_time": profile_value["evaluation_time"],
        "openssl_version": "OpenSSL 3.0.13 30 Jan 2024",
        "profile_sha256": hashlib.sha256(canonical(profile_value)).hexdigest(),
        "release_readiness": "NO_GO",
        "schema": EVIDENCE_SCHEMA,
        "trust_domains": domains,
    }


def find_entry(profile: dict[str, object], domain_name: str, role: str) -> dict[str, object]:
    domain = next(item for item in profile["trust_domains"] if item["name"] == domain_name)
    return next(item for item in domain["certificates"] if item["role"] == role)


class RejectingRunner:
    def __init__(self) -> None:
        self.calls: list[tuple[str, ...]] = []

    def __call__(self, argv: tuple[str, ...], **_kwargs: object) -> object:
        self.calls.append(argv)
        raise AssertionError("OpenSSL must not run for an invalid bundle")


class FakeRunner:
    def __init__(self, result: object) -> None:
        self.result = result
        self.calls: list[tuple[tuple[str, ...], dict[str, object]]] = []

    def __call__(self, argv: tuple[str, ...], **kwargs: object) -> object:
        self.calls.append((argv, kwargs))
        if isinstance(self.result, BaseException):
            raise self.result
        return self.result


def certificate_facts(**changes: object) -> pki._CertificateFacts:
    evaluation = datetime(2026, 8, 30, tzinfo=timezone.utc)
    values: dict[str, object] = {
        "fingerprint": "1" * 64,
        "serial_hex": "1",
        "spki_sha256": "a" * 64,
        "issuer": b"root-name",
        "subject": b"root-name",
        "not_before": evaluation - timedelta(days=1),
        "not_after": evaluation + timedelta(days=365),
        "signature_oid": "1.2.840.10045.4.3.2",
        "key_algorithm": "EC",
        "key_bits": 256,
        "is_ca": True,
        "path_length": 0,
        "key_usage": frozenset({5, 6}),
        "ski": b"r" * 20,
        "aki": b"r" * 20,
        "eku_oids": None,
        "dns_sans": (),
        "ip_sans": (),
        "uri_sans": (),
    }
    values.update(changes)
    return pki._CertificateFacts(**values)


def leaf_facts(
    root_facts: pki._CertificateFacts,
    entry: dict[str, object],
    **changes: object,
) -> pki._CertificateFacts:
    values: dict[str, object] = {
        "fingerprint": "2" * 64,
        "serial_hex": "2",
        "spki_sha256": "b" * 64,
        "issuer": root_facts.subject,
        "subject": b"leaf-name",
        "not_before": root_facts.not_before,
        "not_after": root_facts.not_after,
        "signature_oid": "1.2.840.10045.4.3.2",
        "key_algorithm": "EC",
        "key_bits": 256,
        "is_ca": False,
        "path_length": None,
        "key_usage": frozenset({0}),
        "ski": b"l" * 20,
        "aki": root_facts.ski,
        "eku_oids": tuple(entry["eku_oids"]),
        "dns_sans": tuple(entry["dns_sans"]),
        "ip_sans": tuple(entry["ip_sans"]),
        "uri_sans": tuple(entry["uri_sans"]),
    }
    values.update(changes)
    return pki._CertificateFacts(**values)


def der_length(length: int) -> bytes:
    if length < 128:
        return bytes((length,))
    encoded = length.to_bytes((length.bit_length() + 7) // 8, "big")
    return bytes((0x80 | len(encoded),)) + encoded


def der(tag: int, content: bytes) -> bytes:
    return bytes((tag,)) + der_length(len(content)) + content


def der_integer(content: bytes) -> bytes:
    return der(0x02, content)


def rsa_spki(bits: int, exponent: int = 65537, *, include_null: bool = True) -> bytes:
    modulus = b"\x00\x80" + b"\x00" * (bits // 8 - 1)
    exponent_bytes = exponent.to_bytes((exponent.bit_length() + 7) // 8, "big")
    rsa_key = der(0x30, der_integer(modulus) + der_integer(exponent_bytes))
    rsa_oid = bytes.fromhex("06092a864886f70d010101")
    algorithm = der(0x30, rsa_oid + (b"\x05\x00" if include_null else b""))
    return der(0x30, algorithm + der(0x03, b"\x00" + rsa_key))


def ec_spki(curve_oid: bytes, point: bytes) -> bytes:
    ec_public_key_oid = bytes.fromhex("06072a8648ce3d0201")
    algorithm = der(0x30, ec_public_key_oid + curve_oid)
    return der(0x30, algorithm + der(0x03, b"\x00" + point))


def base128(value: int) -> bytes:
    if value < 0:
        raise ValueError("negative OID component")
    encoded = [value & 0x7F]
    value >>= 7
    while value:
        encoded.append(0x80 | (value & 0x7F))
        value >>= 7
    return bytes(reversed(encoded))


def oid_der(value: str) -> bytes:
    parts = [int(item) for item in value.split(".")]
    if len(parts) < 2 or parts[0] not in {0, 1, 2}:
        raise ValueError("invalid OID")
    content = base128(parts[0] * 40 + parts[1])
    content += b"".join(base128(item) for item in parts[2:])
    return der(0x06, content)


def name_der(common_name: str) -> bytes:
    attribute = der(
        0x30,
        oid_der("2.5.4.3") + der(0x0C, common_name.encode("ascii")),
    )
    return der(0x30, der(0x31, attribute))


def algorithm_der(oid: str, parameters: bytes | None = None) -> bytes:
    return der(0x30, oid_der(oid) + (b"" if parameters is None else parameters))


EC_SHA256_ALGORITHM = algorithm_der("1.2.840.10045.4.3.2")
EC_SHA384_ALGORITHM = algorithm_der("1.2.840.10045.4.3.3")
RSA_SHA256_ALGORITHM = algorithm_der("1.2.840.113549.1.1.11", b"\x05\x00")
RSA_SHA384_ALGORITHM = algorithm_der("1.2.840.113549.1.1.12", b"\x05\x00")


def extension_der(
    oid: str,
    value: bytes,
    *,
    critical: bool = False,
    critical_encoding: bytes | None = None,
) -> bytes:
    if critical_encoding is not None:
        critical_field = critical_encoding
    elif critical:
        critical_field = der(0x01, b"\xff")
    else:
        critical_field = b""
    return der(0x30, oid_der(oid) + critical_field + der(0x04, value))


def key_usage_value(indices: set[int]) -> bytes:
    if not indices:
        return der(0x03, b"\x00")
    bit_count = max(indices) + 1
    payload = bytearray((bit_count + 7) // 8)
    for index in indices:
        payload[index // 8] |= 0x80 >> (index % 8)
    unused = len(payload) * 8 - bit_count
    return der(0x03, bytes((unused,)) + bytes(payload))


def root_extensions(
    *,
    ski: bytes = b"r" * 20,
    basic: bytes | None = None,
    key_usage: bytes | None = None,
    extras: tuple[bytes, ...] = (),
) -> list[bytes]:
    basic_value = (
        der(0x30, der(0x01, b"\xff") + der_integer(b"\x00"))
        if basic is None
        else basic
    )
    usage_value = key_usage_value({5, 6}) if key_usage is None else key_usage
    return [
        extension_der("2.5.29.19", basic_value, critical=True),
        extension_der("2.5.29.15", usage_value, critical=True),
        extension_der("2.5.29.14", der(0x04, ski)),
        extension_der("2.5.29.35", der(0x30, der(0x80, ski))),
        *extras,
    ]


def leaf_extensions(
    *,
    aki: bytes = b"r" * 20,
    ski: bytes = b"l" * 20,
    eku_oids: tuple[str, ...] = (SERVER_AUTH,),
    dns_sans: tuple[str, ...] = ("leaf.invalid",),
    extras: tuple[bytes, ...] = (),
) -> list[bytes]:
    result = [
        extension_der("2.5.29.19", der(0x30, b""), critical=True),
        extension_der("2.5.29.15", key_usage_value({0}), critical=True),
        extension_der("2.5.29.14", der(0x04, ski)),
        extension_der("2.5.29.35", der(0x30, der(0x80, aki))),
        extension_der(
            "2.5.29.37",
            der(0x30, b"".join(oid_der(item) for item in eku_oids)),
        ),
    ]
    if dns_sans:
        result.append(
            extension_der(
                "2.5.29.17",
                der(
                    0x30,
                    b"".join(
                        der(0x82, item.encode("ascii")) for item in dns_sans
                    ),
                ),
            )
        )
    result.extend(extras)
    return result


def synthetic_certificate_der(
    *,
    extensions: list[bytes] | None = None,
    issuer: str = "root",
    subject: str = "root",
    serial_content: bytes = b"\x01",
    not_before: tuple[int, bytes] = (0x17, b"260829000000Z"),
    not_after: tuple[int, bytes] = (0x17, b"270829000000Z"),
    inner_algorithm: bytes = EC_SHA256_ALGORITHM,
    outer_algorithm: bytes | None = None,
    public_key: bytes | None = None,
) -> bytes:
    if extensions is None:
        extensions = root_extensions()
    if outer_algorithm is None:
        outer_algorithm = inner_algorithm
    if public_key is None:
        public_key = ec_spki(
            bytes.fromhex("06082a8648ce3d030107"),
            b"\x04" + b"\x01" * 64,
        )
    validity = der(
        0x30,
        der(not_before[0], not_before[1]) + der(not_after[0], not_after[1]),
    )
    tbs = der(
        0x30,
        der(0xA0, der_integer(b"\x02"))
        + der_integer(serial_content)
        + inner_algorithm
        + name_der(issuer)
        + validity
        + name_der(subject)
        + public_key
        + der(0xA3, der(0x30, b"".join(extensions))),
    )
    return der(0x30, tbs + outer_algorithm + der(0x03, b"\x00\x01"))


def synthetic_public_key(identity: str) -> bytes:
    return ec_spki(
        bytes.fromhex("06082a8648ce3d030107"),
        b"\x04" + hashlib.sha512(identity.encode("ascii")).digest(),
    )


def fixed_verify_argv(
    ca_fd: int = 10,
    leaf_fd: int = 11,
    purpose: str = "sslserver",
) -> tuple[str, ...]:
    return (
        "/usr/bin/openssl",
        "verify",
        "-trusted",
        f"/proc/self/fd/{ca_fd}",
        "-no-CAfile",
        "-no-CApath",
        "-no-CAstore",
        "-x509_strict",
        "-check_ss_sig",
        "-auth_level",
        "2",
        "-verify_depth",
        "0",
        "-attime",
        "1788048000",
        "-purpose",
        purpose,
        f"/proc/self/fd/{leaf_fd}",
    )


class FakePipe:
    def __init__(self, descriptor: int) -> None:
        self.descriptor = descriptor
        self.closed = False

    def fileno(self) -> int:
        return self.descriptor

    def close(self) -> None:
        self.closed = True


class FakeProcess:
    def __init__(self, *, wait_results: list[object] | None = None) -> None:
        self.stdout = FakePipe(101)
        self.stderr = FakePipe(102)
        self.wait_results = [0] if wait_results is None else list(wait_results)
        self.wait_timeouts: list[float] = []
        self.poll_calls = 0
        self.kill_calls = 0

    def poll(self) -> None:
        self.poll_calls += 1
        return None

    def kill(self) -> None:
        self.kill_calls += 1

    def wait(self, timeout: float) -> int:
        self.wait_timeouts.append(timeout)
        value = self.wait_results.pop(0) if self.wait_results else 0
        if isinstance(value, BaseException):
            raise value
        return int(value)


class FakeSelectorKey:
    def __init__(self, fileobj: FakePipe, data: str) -> None:
        self.fileobj = fileobj
        self.data = data


class FakeSelector:
    def __init__(self, *, timeout_immediately: bool = False) -> None:
        self.mapping: dict[int, FakeSelectorKey] = {}
        self.timeout_immediately = timeout_immediately
        self.closed = False

    def register(self, stream: FakePipe, _events: int, data: str) -> None:
        self.mapping[stream.fileno()] = FakeSelectorKey(stream, data)

    def unregister(self, stream: FakePipe) -> None:
        self.mapping.pop(stream.fileno())

    def get_map(self) -> dict[int, FakeSelectorKey]:
        return self.mapping

    def select(self, _timeout: float) -> list[tuple[FakeSelectorKey, int]]:
        if self.timeout_immediately:
            return []
        return [(key, 1) for key in list(self.mapping.values())]

    def close(self) -> None:
        self.closed = True


def pem_certificate(value: bytes) -> bytes:
    encoded = base64.b64encode(value).decode("ascii")
    lines = [encoded[index : index + 64] for index in range(0, len(encoded), 64)]
    return (
        "-----BEGIN CERTIFICATE-----\n"
        + "\n".join(lines)
        + "\n-----END CERTIFICATE-----\n"
    ).encode("ascii")


def positive_integer_content(value: int) -> bytes:
    encoded = value.to_bytes((value.bit_length() + 7) // 8, "big")
    return (b"\x00" if encoded[0] & 0x80 else b"") + encoded


def write_synthetic_bundle(
    root: Path,
    profile: dict[str, object],
    *,
    serial_overrides: dict[str, int] | None = None,
    spki_overrides: dict[str, bytes] | None = None,
) -> None:
    root.mkdir(mode=0o700, exist_ok=True)
    profile_path = root / "pki-profile.json"
    profile_path.write_bytes(canonical(profile))
    profile_path.chmod(0o600)
    serial_overrides = {} if serial_overrides is None else serial_overrides
    spki_overrides = {} if spki_overrides is None else spki_overrides
    serial = 1
    for domain in profile["trust_domains"]:
        domain_name = domain["name"]
        root_name = domain_name + "-root"
        ca_member = domain["ca_certificate"]
        root_ski = hashlib.sha256(
            ("root:" + domain_name).encode("ascii")
        ).digest()[:20]
        root_der = synthetic_certificate_der(
            extensions=root_extensions(ski=root_ski),
            issuer=root_name,
            subject=root_name,
            serial_content=positive_integer_content(
                serial_overrides.get(ca_member, serial)
            ),
            public_key=spki_overrides.get(
                ca_member, synthetic_public_key("root:" + domain_name)
            ),
        )
        serial += 1
        ca_path = root / ca_member
        ca_path.write_bytes(pem_certificate(root_der))
        ca_path.chmod(0o600)
        for entry in domain["certificates"]:
            leaf_member = entry["certificate"]
            leaf_ski = hashlib.sha256(
                ("leaf:" + entry["role"]).encode("ascii")
            ).digest()[:20]
            leaf_der = synthetic_certificate_der(
                extensions=leaf_extensions(
                    aki=root_ski,
                    ski=leaf_ski,
                    eku_oids=tuple(entry["eku_oids"]),
                    dns_sans=tuple(entry["dns_sans"]),
                ),
                issuer=root_name,
                subject=entry["role"],
                serial_content=positive_integer_content(
                    serial_overrides.get(leaf_member, serial)
                ),
                public_key=spki_overrides.get(
                    leaf_member,
                    synthetic_public_key("leaf:" + entry["role"]),
                ),
            )
            serial += 1
            leaf_path = root / leaf_member
            leaf_path.write_bytes(pem_certificate(leaf_der))
            leaf_path.chmod(0o600)


class PKIProfileTests(unittest.TestCase):
    def test_checked_in_example_is_exact_generated_canonical_contract(self) -> None:
        expected = canonical(complete_profile()).decode("utf-8")
        with EXAMPLE.open("r", encoding="utf-8", newline=None) as handle:
            text = handle.read()
        self.assertFalse(text.startswith("\ufeff"))
        self.assertEqual(text, expected)
        normalized = text.encode("utf-8")
        self.assertEqual(pki.example_profile(), complete_profile())
        self.assertEqual(validate_profile(normalized), complete_profile())
        self.assertTrue(text.endswith("\n"))
        self.assertEqual(text.count('"name":'), 7)
        self.assertEqual(text.count('"role":'), 37)

    def test_checked_in_example_has_an_exact_lf_attribute(self) -> None:
        raw = GITATTRIBUTES.read_bytes()
        self.assertFalse(raw.startswith(b"\xef\xbb\xbf"))
        attributes = raw.decode("utf-8").splitlines()
        exact = "/deploy/ha/pki-profile.json.example text eol=lf"
        profile_rules = [
            line
            for line in attributes
            if line.startswith("/deploy/ha/pki-profile.json.example ")
        ]
        self.assertEqual(profile_rules, [exact])

    def test_accepts_one_canonical_complete_profile(self) -> None:
        profile = complete_profile()
        validated = validate_profile(canonical(profile))
        self.assertEqual(validated, profile)
        self.assertEqual(len(validated["trust_domains"]), 7)
        self.assertEqual(
            sum(len(item["certificates"]) for item in validated["trust_domains"]),
            37,
        )
        self.assertEqual(canonical_bytes(validated), canonical(profile))

    def test_rejects_duplicate_noncanonical_and_wrong_top_level_contract(self) -> None:
        profile = complete_profile()
        raw = canonical(profile)
        duplicated = raw.replace(
            b'{"deployment_authorized":false,',
            b'{"deployment_authorized":false,"deployment_authorized":false,',
            1,
        )
        variants: list[tuple[str, bytes]] = [
            ("duplicate", duplicated),
            ("pretty", json.dumps(profile, indent=2).encode("utf-8")),
            ("missing newline", raw[:-1]),
            ("trailing newline", raw + b"\n"),
        ]
        missing = copy.deepcopy(profile)
        missing.pop("schema")
        variants.append(("missing key", canonical(missing)))
        extra = copy.deepcopy(profile)
        extra["unknown"] = "value"
        variants.append(("extra key", canonical(extra)))
        for label, value in variants:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(value)

    def test_rejects_duplicate_keys_at_every_nested_contract_level(self) -> None:
        raw = canonical(complete_profile())
        variants = {
            "profile": raw.replace(
                b'"schema":"maestro-ha-pki-profile-v1",',
                b'"schema":"maestro-ha-pki-profile-v1",'
                b'"schema":"maestro-ha-pki-profile-v1",',
                1,
            ),
            "domain": raw.replace(
                b'"name":"bot-gateway"}',
                b'"name":"bot-gateway","name":"bot-gateway"}',
                1,
            ),
            "certificate": raw.replace(
                b'"purpose":"server","role":"s2-bot-gateway-server"',
                b'"purpose":"server","role":"s2-bot-gateway-server",'
                b'"role":"s2-bot-gateway-server"',
                1,
            ),
        }
        for label, value in variants.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(value)

    def test_rejects_missing_extra_renamed_or_misclassified_roles(self) -> None:
        variants: list[tuple[str, dict[str, object]]] = []

        missing_domain = complete_profile()
        missing_domain["trust_domains"].pop()
        variants.append(("missing domain", missing_domain))

        extra_domain = complete_profile()
        duplicate_domain = copy.deepcopy(extra_domain["trust_domains"][0])
        duplicate_domain["name"] = "unknown-domain"
        extra_domain["trust_domains"].insert(0, duplicate_domain)
        variants.append(("extra domain", extra_domain))

        missing_role = complete_profile()
        missing_role["trust_domains"][0]["certificates"].pop()
        variants.append(("missing role", missing_role))

        renamed_role = complete_profile()
        renamed_role["trust_domains"][0]["certificates"][0]["role"] = "renamed-role"
        variants.append(("renamed role", renamed_role))

        wrong_purpose = complete_profile()
        find_entry(wrong_purpose, "rqlite-raft", "s2-raft-peer")["purpose"] = "server"
        variants.append(("wrong purpose", wrong_purpose))

        wrong_eku = complete_profile()
        find_entry(wrong_eku, "rqlite-http", "s2-http-server")["eku_oids"] = [
            CLIENT_AUTH
        ]
        variants.append(("wrong EKU", wrong_eku))

        duplicate_role = complete_profile()
        duplicate_role["trust_domains"][0]["certificates"][1] = copy.deepcopy(
            duplicate_role["trust_domains"][0]["certificates"][0]
        )
        variants.append(("duplicate role", duplicate_role))

        moved_role = complete_profile()
        source = moved_role["trust_domains"][0]["certificates"].pop(0)
        moved_role["trust_domains"][1]["certificates"].append(source)
        moved_role["trust_domains"][1]["certificates"].sort(
            key=lambda item: item["role"]
        )
        variants.append(("role moved across domains", moved_role))

        extra_role = complete_profile()
        injected = copy.deepcopy(extra_role["trust_domains"][0]["certificates"][0])
        injected["role"] = "unknown-role"
        injected["certificate"] = "unknown-role.pem"
        extra_role["trust_domains"][0]["certificates"].insert(0, injected)
        variants.append(("extra role", extra_role))

        extra_certificate_key = complete_profile()
        extra_certificate_key["trust_domains"][0]["certificates"][0][
            "private_key"
        ] = "must-never-be-accepted.pem"
        variants.append(("extra nested certificate key", extra_certificate_key))

        extra_domain_key = complete_profile()
        extra_domain_key["trust_domains"][0]["issuer"] = "unexpected"
        variants.append(("extra nested domain key", extra_domain_key))

        for label, value in variants:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(canonical(value))

    def test_rejects_fixed_identity_san_drift_and_unsafe_paths(self) -> None:
        variants: list[tuple[str, dict[str, object]]] = []

        missing_san = complete_profile()
        find_entry(
            missing_san, "dispatcher", "s2-controlplane-dispatcher-client"
        )["dns_sans"] = []
        variants.append(("missing fixed SAN", missing_san))

        extra_san = complete_profile()
        find_entry(
            extra_san, "bot-gateway", "s2-telegram-bot-primary-client"
        )["dns_sans"] = ["bot-primary", "extra.invalid"]
        variants.append(("extra fixed SAN", extra_san))

        wrong_type = complete_profile()
        entry = find_entry(
            wrong_type, "github-probe", "github-workflow-probe-client"
        )
        entry["dns_sans"] = []
        entry["uri_sans"] = ["spiffe://workflow-probe"]
        variants.append(("wrong SAN type", wrong_type))

        nested_path = complete_profile()
        find_entry(nested_path, "rqlite-http", "s2-http-server")[
            "certificate"
        ] = "../s2-http-server.pem"
        variants.append(("traversal", nested_path))

        reused_path = complete_profile()
        find_entry(reused_path, "rqlite-http", "s3-http-server")[
            "certificate"
        ] = "s2-http-server.pem"
        variants.append(("reused path", reused_path))

        renamed_fixed_san = complete_profile()
        find_entry(
            renamed_fixed_san,
            "dispatcher",
            "s3-controlplane-dispatcher-client",
        )["dns_sans"] = ["renamed-dispatcher"]
        variants.append(("renamed fixed SAN", renamed_fixed_san))

        renamed_ca = complete_profile()
        renamed_ca["trust_domains"][0]["ca_certificate"] = "renamed-ca.pem"
        variants.append(("renamed CA member", renamed_ca))

        absolute_path = complete_profile()
        find_entry(absolute_path, "rqlite-http", "s2-http-server")[
            "certificate"
        ] = "C:\\secret.pem"
        variants.append(("absolute path", absolute_path))

        for label, value in variants:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(canonical(value))

    def test_rejects_every_rqlite_fixed_identity_san_drift(self) -> None:
        roles = {
            "rqlite-http": (
                "s2-http-server",
                "s3-http-server",
                "s4-http-server",
            ),
            "rqlite-raft": (
                "s2-raft-peer",
                "s3-raft-peer",
                "s4-raft-peer",
            ),
        }
        for domain, domain_roles in roles.items():
            for role in domain_roles:
                for field, value in (
                    ("dns_sans", ["wrong.invalid"]),
                    ("ip_sans", ["127.0.0.1"]),
                    ("uri_sans", ["spiffe://wrong"]),
                ):
                    with self.subTest(domain=domain, role=role, field=field):
                        profile = complete_profile()
                        find_entry(profile, domain, role)[field] = value
                        with self.assertRaisesRegex(
                            PKIVerificationError,
                            "^pki-verify:invalid-input$",
                        ):
                            validate_profile(canonical(profile))

    def test_rejects_unsorted_or_duplicate_profile_lists(self) -> None:
        variants: list[tuple[str, dict[str, object]]] = []

        domains = complete_profile()
        domains["trust_domains"].reverse()
        variants.append(("domain order", domains))

        roles = complete_profile()
        roles["trust_domains"][0]["certificates"].reverse()
        variants.append(("role order", roles))

        sans = complete_profile()
        find_entry(sans, "rqlite-http", "s2-http-server")["dns_sans"] = [
            "z.invalid",
            "a.invalid",
        ]
        variants.append(("SAN order", sans))

        duplicates = complete_profile()
        find_entry(duplicates, "rqlite-http", "s2-http-server")["dns_sans"] = [
            "same.invalid",
            "same.invalid",
        ]
        variants.append(("duplicate SAN", duplicates))

        for label, value in variants:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(canonical(value))

    def test_rejects_bad_readiness_time_and_lifetime_values(self) -> None:
        invalid = (
            ("readiness", "release_readiness", "GO"),
            ("authorization", "deployment_authorized", True),
            ("offset time", "evaluation_time", "2026-08-30T03:00:00+03:00"),
            ("fractional time", "evaluation_time", "2026-08-30T00:00:00.1Z"),
            ("zero lifetime", "minimum_remaining_seconds", 0),
            ("boolean lifetime", "minimum_remaining_seconds", True),
        )
        for label, key, value in invalid:
            profile = complete_profile()
            profile[key] = value
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(canonical(profile))

    def test_rejects_invalid_json_scalars_encoding_and_limits(self) -> None:
        variants = {
            "not bytes": "{}\n",
            "empty": b"",
            "invalid UTF-8": b"{\xff}\n",
            "NaN": b'{"value":NaN}\n',
            "oversize": b" " * 262145,
            "array top-level": b"[]\n",
        }
        for label, value in variants.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    validate_profile(value)  # type: ignore[arg-type]


class PKIEvidenceTests(unittest.TestCase):
    def test_accepts_exact_canonical_redacted_evidence(self) -> None:
        evidence = complete_evidence()
        self.assertEqual(validate_evidence(canonical(evidence)), evidence)
        encoded = canonical_bytes(evidence)
        self.assertNotIn(b".pem", encoded)
        self.assertNotIn(b"PRIVATE", encoded)
        self.assertNotIn(str(Path.cwd()).encode("utf-8"), encoded)

    def test_rejects_duplicate_spki_globally_and_serial_within_issuer(self) -> None:
        variants: list[tuple[str, dict[str, object]]] = []

        leaf_reuses_ca_spki = complete_evidence()
        leaf_reuses_ca_spki["trust_domains"][0]["certificates"][0][
            "certificate_spki_sha256"
        ] = leaf_reuses_ca_spki["trust_domains"][0]["ca_spki_sha256"]
        variants.append(("leaf reuses CA SPKI", leaf_reuses_ca_spki))

        cross_domain_spki = complete_evidence()
        cross_domain_spki["trust_domains"][1]["certificates"][0][
            "certificate_spki_sha256"
        ] = cross_domain_spki["trust_domains"][0]["certificates"][0][
            "certificate_spki_sha256"
        ]
        variants.append(("cross-domain leaf SPKI", cross_domain_spki))

        leaf_reuses_ca_serial = complete_evidence()
        leaf_reuses_ca_serial["trust_domains"][0]["certificates"][0][
            "certificate_serial_hex"
        ] = leaf_reuses_ca_serial["trust_domains"][0]["ca_serial_hex"]
        variants.append(("leaf reuses issuer CA serial", leaf_reuses_ca_serial))

        duplicate_leaf_serial = complete_evidence()
        duplicate_leaf_serial["trust_domains"][0]["certificates"][1][
            "certificate_serial_hex"
        ] = duplicate_leaf_serial["trust_domains"][0]["certificates"][0][
            "certificate_serial_hex"
        ]
        variants.append(("same-issuer leaf serial", duplicate_leaf_serial))

        for label, evidence in variants:
            with self.subTest(label=label), self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:invalid-evidence$"
            ):
                validate_evidence(canonical(evidence))

    def test_allows_same_serial_in_different_trust_domains(self) -> None:
        evidence = complete_evidence()
        first = evidence["trust_domains"][0]["certificates"][0]
        second = evidence["trust_domains"][1]["certificates"][0]
        second["certificate_serial_hex"] = first["certificate_serial_hex"]
        self.assertEqual(validate_evidence(canonical(evidence)), evidence)

    def test_rejects_noncanonical_or_der_oversize_evidence_serials(self) -> None:
        invalid_serials = (
            "0",
            "00",
            "01",
            "A",
            "g1",
            "f" * 40,
            "1" * 41,
        )
        for serial in invalid_serials:
            with self.subTest(serial=serial):
                evidence = complete_evidence()
                evidence["trust_domains"][0]["certificates"][0][
                    "certificate_serial_hex"
                ] = serial
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-evidence$"
                ):
                    validate_evidence(canonical(evidence))

    def test_rejects_noncanonical_missing_extra_or_matrix_drift(self) -> None:
        variants: list[tuple[str, bytes]] = []
        evidence = complete_evidence()
        variants.append(("pretty", json.dumps(evidence, indent=2).encode("utf-8")))

        missing = complete_evidence()
        missing.pop("profile_sha256")
        variants.append(("missing", canonical(missing)))

        extra = complete_evidence()
        extra["certificate"] = "secret.pem"
        variants.append(("extra", canonical(extra)))

        missing_role = complete_evidence()
        missing_role["trust_domains"][0]["certificates"].pop()
        variants.append(("role drift", canonical(missing_role)))

        bad_digest = complete_evidence()
        bad_digest["profile_sha256"] = "A" * 64
        variants.append(("digest", canonical(bad_digest)))

        bad_version = complete_evidence()
        bad_version["openssl_version"] = "LibreSSL 3.8.2"
        variants.append(("version", canonical(bad_version)))

        for label, raw in variants:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-evidence$"
                ):
                    validate_evidence(raw)

    def test_rejects_duplicate_fingerprints_and_expired_ca_or_leaf(self) -> None:
        variants: list[tuple[str, dict[str, object]]] = []

        duplicate_ca = complete_evidence()
        duplicate_ca["trust_domains"][1]["ca_fingerprint_sha256"] = (
            duplicate_ca["trust_domains"][0]["ca_fingerprint_sha256"]
        )
        variants.append(("duplicate CA fingerprint", duplicate_ca))

        leaf_reuses_ca = complete_evidence()
        leaf_reuses_ca["trust_domains"][0]["certificates"][0][
            "certificate_fingerprint_sha256"
        ] = leaf_reuses_ca["trust_domains"][0]["ca_fingerprint_sha256"]
        variants.append(("leaf reuses CA fingerprint", leaf_reuses_ca))

        duplicate_leaf = complete_evidence()
        duplicate_leaf["trust_domains"][0]["certificates"][1][
            "certificate_fingerprint_sha256"
        ] = duplicate_leaf["trust_domains"][0]["certificates"][0][
            "certificate_fingerprint_sha256"
        ]
        variants.append(("duplicate leaf fingerprint", duplicate_leaf))

        expired_ca = complete_evidence()
        expired_ca["trust_domains"][0]["ca_not_after"] = (
            expired_ca["evaluation_time"]
        )
        variants.append(("CA expires at evaluation", expired_ca))

        expired_leaf = complete_evidence()
        expired_leaf["trust_domains"][0]["certificates"][0]["not_after"] = (
            expired_leaf["evaluation_time"]
        )
        variants.append(("leaf expires at evaluation", expired_leaf))

        missing_ca_expiry = complete_evidence()
        missing_ca_expiry["trust_domains"][0].pop("ca_not_after")
        variants.append(("missing CA expiry", missing_ca_expiry))

        for label, value in variants:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-evidence$"
                ):
                    validate_evidence(canonical(value))

    def test_evidence_is_strictly_redacted_and_canonicalizer_fails_closed(self) -> None:
        evidence = complete_evidence()
        raw = canonical_bytes(evidence)
        forbidden = (
            b"-----BEGIN",
            b"PRIVATE KEY",
            b".pem",
            b"/proc/self/fd",
            b"subject",
            b"issuer",
            b"stderr",
            b"stdout",
        )
        for marker in forbidden:
            with self.subTest(marker=marker):
                self.assertNotIn(marker, raw)
        self.assertEqual(raw, canonical(evidence))

        cyclic: list[object] = []
        cyclic.append(cyclic)
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-evidence$"
        ):
            canonical_bytes(cyclic)


class PKIOpenSSLContractTests(unittest.TestCase):
    def test_accepts_only_strict_official_openssl_three_version_lines(self) -> None:
        accepted = (
            b"OpenSSL 3.0.13 30 Jan 2024\n",
            b"OpenSSL 3.4.1 11 Feb 2025\r\n",
            b"OpenSSL 3.0.13 30 Jan 2024 "
            b"(Library: OpenSSL 3.0.13 30 Jan 2024)\n",
        )
        for output in accepted:
            runner = FakeRunner(OpenSSLResult(0, output, b""))
            with self.subTest(output=output):
                version = pki._openssl_version(runner)
                self.assertEqual(version, output.rstrip(b"\r\n").decode("ascii"))
                self.assertEqual(len(runner.calls), 1)
                argv, kwargs = runner.calls[0]
                self.assertEqual(argv, ("/usr/bin/openssl", "version", "-v"))
                self.assertEqual(kwargs["pass_fds"], ())
                self.assertEqual(kwargs["timeout_seconds"], 8)
                self.assertEqual(kwargs["output_limit"], 65536)

    def test_rejects_forks_suffixes_multiline_and_malformed_versions(self) -> None:
        rejected = (
            b"OpenSSL 2.0.0 1 Jan 2024\n",
            b"OpenSSL 4.0.0 1 Jan 2026\n",
            b"LibreSSL 3.8.2\n",
            b"OpenSSL 3.0.0 1 Jan 2024 BoringSSL\n",
            b"OpenSSL 3.0.0\n",
            b"OpenSSL 3.0.0 1 Foo 2024\n",
            b"OpenSSL 3.0.0 1 Jan 2024\nsecond line\n",
            b"OpenSSL 3.0.0 1 Jan 2024\rgarbage\n",
            b"OpenSSL 3.0.0 1 Jan 2024\x00\n",
            b"\xff\n",
        )
        for output in rejected:
            with self.subTest(output=output):
                runner = FakeRunner(OpenSSLResult(0, output, b""))
                with self.assertRaisesRegex(
                    PKIVerificationError,
                    "^pki-verify:(unsupported-openssl|invalid-openssl-output)$",
                ):
                    pki._openssl_version(runner)

    def test_maps_runner_failures_to_fixed_redacted_errors(self) -> None:
        version_failures = (
            (
                "nonzero version",
                OpenSSLResult(1, b"OpenSSL 3.0.13 30 Jan 2024\n", b""),
                "unsupported-openssl",
            ),
            (
                "stderr",
                OpenSSLResult(0, b"OpenSSL 3.0.13 30 Jan 2024\n", b"warning"),
                "invalid-openssl-output",
            ),
        )
        for label, result, code in version_failures:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:" + code + "$"
                ):
                    pki._openssl_version(FakeRunner(result))

        cases: list[tuple[str, object, str]] = [
            ("missing executable", FileNotFoundError("secret"), "unsupported-openssl"),
            (
                "timeout",
                subprocess.TimeoutExpired(("secret",), 8),
                "openssl-timeout",
            ),
            ("output cap", pki._OutputLimitError("secret"), "invalid-openssl-output"),
            ("os failure", OSError("secret"), "openssl-failed"),
            ("bad result type", object(), "invalid-openssl-output"),
            (
                "oversize stdout",
                OpenSSLResult(0, b"x" * 65537, b""),
                "invalid-openssl-output",
            ),
            (
                "oversize stderr",
                OpenSSLResult(0, b"", b"x" * 65537),
                "invalid-openssl-output",
            ),
        ]
        argv = ("/usr/bin/openssl", "version", "-v")
        for label, result, code in cases:
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:" + code + "$"
                ) as raised:
                    pki._invoke_openssl(FakeRunner(result), argv, ())
                self.assertNotIn("secret", str(raised.exception))

    def test_allows_only_the_fixed_version_and_verify_argv_shapes(self) -> None:
        version = ("/usr/bin/openssl", "version", "-v")
        verify = (
            "/usr/bin/openssl",
            "verify",
            "-trusted",
            "/proc/self/fd/10",
            "-no-CAfile",
            "-no-CApath",
            "-no-CAstore",
            "-x509_strict",
            "-check_ss_sig",
            "-auth_level",
            "2",
            "-verify_depth",
            "0",
            "-attime",
            "1788048000",
            "-purpose",
            "sslserver",
            "/proc/self/fd/11",
        )
        self.assertTrue(pki._allowed_openssl_argv(version, ()))
        self.assertTrue(pki._allowed_openssl_argv(verify, (10, 11)))
        mutations = (
            (verify[:-1], (10, 11)),
            (verify + ("extra",), (10, 11)),
            (verify[:14] + ("not-a-time",) + verify[15:], (10, 11)),
            (verify[:16] + ("any",) + verify[17:], (10, 11)),
            (verify[:4] + ("-CAfile",) + verify[5:], (10, 11)),
            (verify[:3] + ("",) + verify[4:], (10, 11)),
            (verify[:3] + ("bad\x00path",) + verify[4:], (10, 11)),
            (("openssl",) + verify[1:], (10, 11)),
            (verify[:3] + ("/tmp/ca.pem",) + verify[4:], (10, 11)),
            (verify[:17] + ("relative-leaf.pem",), (10, 11)),
            (verify, (10, 12)),
            (verify, (9, 10, 11)),
            (verify, (11, 10)),
            (verify, (10, 10)),
            (verify, (1, 10, 11)),
            (version, (10,)),
        )
        for argv, pass_fds in mutations:
            with self.subTest(argv=argv, pass_fds=pass_fds):
                self.assertFalse(pki._allowed_openssl_argv(argv, pass_fds))

    def test_process_stop_is_bounded_when_kill_and_wait_never_complete(self) -> None:
        class StubbornProcess:
            def __init__(self) -> None:
                self.poll_calls = 0
                self.kill_calls = 0
                self.wait_calls = 0

            def poll(self) -> None:
                self.poll_calls += 1
                return None

            def kill(self) -> None:
                self.kill_calls += 1

            def wait(self, timeout: int) -> None:
                self.wait_calls += 1
                self.assert_timeout = timeout
                raise subprocess.TimeoutExpired(("redacted",), timeout)

        process = StubbornProcess()
        pki._stop_process(process)  # type: ignore[arg-type]
        self.assertEqual(process.poll_calls, 2)
        self.assertEqual(process.kill_calls, 2)
        self.assertEqual(process.wait_calls, 2)
        self.assertEqual(process.assert_timeout, 1)

    def test_default_runner_uses_fixed_popen_contract_and_bounded_interleaved_pipes(self) -> None:
        argv = fixed_verify_argv()
        process = FakeProcess()
        selector = FakeSelector()
        chunks = {
            101: [b"ab", b"cd", b""],
            102: [b"1", b"234", b""],
        }
        read_sizes: list[tuple[int, int]] = []

        def fake_read(descriptor: int, size: int) -> bytes:
            read_sizes.append((descriptor, size))
            chunk = chunks[descriptor].pop(0)
            self.assertLessEqual(len(chunk), size)
            return chunk

        with mock.patch.object(pki, "_linux_runtime", return_value=True), mock.patch.object(
            pki.subprocess, "Popen", return_value=process
        ) as popen, mock.patch.object(
            pki.selectors, "DefaultSelector", return_value=selector
        ), mock.patch.object(pki.os, "read", side_effect=fake_read):
            result = pki.default_openssl_runner(
                argv,
                pass_fds=(10, 11),
                timeout_seconds=8,
                output_limit=4,
            )

        self.assertEqual(result, OpenSSLResult(0, b"abcd", b"1234"))
        popen.assert_called_once_with(
            list(argv),
            shell=False,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            close_fds=True,
            pass_fds=(10, 11),
            env={
                "PATH": "/usr/bin:/bin",
                "LC_ALL": "C",
                "LANG": "C",
                "TZ": "UTC",
                "OPENSSL_CONF": os.devnull,
            },
            bufsize=0,
        )
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)
        self.assertTrue(selector.closed)
        self.assertEqual(process.kill_calls, 0)
        self.assertEqual(len(process.wait_timeouts), 1)
        self.assertGreater(process.wait_timeouts[0], 0)
        self.assertLessEqual(process.wait_timeouts[0], 8)
        self.assertEqual([descriptor for descriptor, _ in read_sizes].count(101), 3)
        self.assertEqual([descriptor for descriptor, _ in read_sizes].count(102), 3)
        self.assertEqual(read_sizes[-2:], [(101, 1), (102, 1)])

    def test_default_runner_kills_and_reaps_at_output_limit_plus_one(self) -> None:
        argv = fixed_verify_argv()
        process = FakeProcess()
        selector = FakeSelector()
        chunks = {101: [b"abcde"], 102: [b""]}

        def fake_read(descriptor: int, size: int) -> bytes:
            chunk = chunks[descriptor].pop(0)
            self.assertLessEqual(len(chunk), size)
            return chunk

        with mock.patch.object(pki, "_linux_runtime", return_value=True), mock.patch.object(
            pki.subprocess, "Popen", return_value=process
        ), mock.patch.object(
            pki.selectors, "DefaultSelector", return_value=selector
        ), mock.patch.object(pki.os, "read", side_effect=fake_read):
            with self.assertRaises(pki._OutputLimitError):
                pki.default_openssl_runner(
                    argv,
                    pass_fds=(10, 11),
                    output_limit=4,
                )
        self.assertEqual(process.kill_calls, 1)
        self.assertEqual(len(process.wait_timeouts), 1)
        self.assertEqual(process.wait_timeouts[0], 1)
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)
        self.assertTrue(selector.closed)

    def test_default_runner_timeout_kills_reaps_and_closes_both_pipes(self) -> None:
        argv = fixed_verify_argv()
        process = FakeProcess()
        selector = FakeSelector(timeout_immediately=True)
        with mock.patch.object(pki, "_linux_runtime", return_value=True), mock.patch.object(
            pki.subprocess, "Popen", return_value=process
        ), mock.patch.object(
            pki.selectors, "DefaultSelector", return_value=selector
        ), mock.patch.object(pki.os, "read") as read:
            with self.assertRaises(subprocess.TimeoutExpired):
                pki.default_openssl_runner(
                    argv,
                    pass_fds=(10, 11),
                    timeout_seconds=1,
                    output_limit=4,
                )
        read.assert_not_called()
        self.assertEqual(process.kill_calls, 1)
        self.assertEqual(process.wait_timeouts, [1])
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)
        self.assertTrue(selector.closed)

    def test_verify_leaf_chain_accepts_only_one_exact_success_line(self) -> None:
        ca = object()
        leaf = object()
        expected_argv = fixed_verify_argv()
        for stdout in (
            b"/proc/self/fd/11: OK\n",
            b"/proc/self/fd/11: OK\r\n",
        ):
            runner = FakeRunner(OpenSSLResult(0, stdout, b""))
            with self.subTest(stdout=stdout), mock.patch.object(
                pki,
                "_descriptor_reference",
                side_effect=(
                    ("/proc/self/fd/10", (10,)),
                    ("/proc/self/fd/11", (11,)),
                ),
            ):
                pki._verify_leaf_chain(
                    runner,
                    object(),
                    ca,
                    leaf,
                    1788048000,
                    "sslserver",
                )
            self.assertEqual(len(runner.calls), 1)
            argv, kwargs = runner.calls[0]
            self.assertEqual(argv, expected_argv)
            self.assertEqual(kwargs["pass_fds"], (10, 11))

    def test_verify_leaf_chain_rejects_nonzero_stderr_and_malformed_stdout(self) -> None:
        cases = (
            (OpenSSLResult(1, b"", b"secret"), "openssl-failed"),
            (
                OpenSSLResult(0, b"/proc/self/fd/11: OK\n", b"warning"),
                "invalid-openssl-output",
            ),
            (OpenSSLResult(0, b"/proc/self/fd/11: OK", b""), "invalid-openssl-output"),
            (OpenSSLResult(0, b"leaf.pem: OK\n", b""), "invalid-openssl-output"),
            (
                OpenSSLResult(
                    0,
                    b"/proc/self/fd/11: OK\n/proc/self/fd/11: OK\n",
                    b"",
                ),
                "invalid-openssl-output",
            ),
        )
        for result, code in cases:
            with self.subTest(result=result), mock.patch.object(
                pki,
                "_descriptor_reference",
                side_effect=(
                    ("/proc/self/fd/10", (10,)),
                    ("/proc/self/fd/11", (11,)),
                ),
            ):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:" + code + "$"
                ):
                    pki._verify_leaf_chain(
                        FakeRunner(result),
                        object(),
                        object(),
                        object(),
                        1788048000,
                        "sslserver",
                    )

    @unittest.skipUnless(os.name == "nt", "Windows fail-closed boundary")
    def test_default_runner_fails_closed_outside_linux(self) -> None:
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:unsupported-openssl$"
        ):
            pki.default_openssl_runner(("/usr/bin/openssl", "version", "-v"))

    def test_default_runner_rejects_limits_and_fd_drift_before_process_start(self) -> None:
        verify = (
            "/usr/bin/openssl",
            "verify",
            "-trusted",
            "/proc/self/fd/10",
            "-no-CAfile",
            "-no-CApath",
            "-no-CAstore",
            "-x509_strict",
            "-check_ss_sig",
            "-auth_level",
            "2",
            "-verify_depth",
            "0",
            "-attime",
            "1788048000",
            "-purpose",
            "sslclient",
            "/proc/self/fd/11",
        )
        cases = (
            (("/usr/bin/openssl", "version", "-v"), (3,), 8, 65536),
            (verify, (10, 12), 8, 65536),
            (verify, (10, 11), 0, 65536),
            (verify, (10, 11), 9, 65536),
            (verify, (10, 11), True, 65536),
            (verify, (10, 11), 8, 0),
            (verify, (10, 11), 8, 65537),
            (verify, (10, 11), 8, True),
        )
        with mock.patch.object(pki, "_linux_runtime", return_value=True), mock.patch.object(
            pki.subprocess, "Popen"
        ) as popen:
            for argv, pass_fds, timeout_seconds, output_limit in cases:
                with self.subTest(
                    pass_fds=pass_fds,
                    timeout_seconds=timeout_seconds,
                    output_limit=output_limit,
                ):
                    with self.assertRaisesRegex(
                        PKIVerificationError,
                        "^pki-verify:unsupported-openssl$",
                    ):
                        pki.default_openssl_runner(
                            argv,
                            pass_fds=pass_fds,
                            timeout_seconds=timeout_seconds,
                            output_limit=output_limit,
                        )
            popen.assert_not_called()


class PKICertificatePolicyTests(unittest.TestCase):
    evaluation = datetime(2026, 8, 30, tzinfo=timezone.utc)
    minimum = 30 * 24 * 60 * 60

    def test_accepts_valid_root_and_leaf_facts(self) -> None:
        root_facts = certificate_facts()
        entry = find_entry(
            complete_profile(), "rqlite-http", "s2-http-server"
        )
        leaf = leaf_facts(root_facts, entry)
        self.assertIsNone(
            pki._validate_root(root_facts, self.evaluation, self.minimum)
        )
        self.assertIsNone(
            pki._validate_leaf(
                leaf, root_facts, entry, self.evaluation, self.minimum
            )
        )

    def test_rejects_every_root_policy_drift(self) -> None:
        cases = {
            "future": {"not_before": self.evaluation + timedelta(seconds=1)},
            "expired": {"not_after": self.evaluation},
            "remaining lifetime": {
                "not_after": self.evaluation + timedelta(seconds=self.minimum - 1)
            },
            "not self-issued": {"issuer": b"other-name"},
            "not CA": {"is_ca": False},
            "missing path length": {"path_length": None},
            "wrong path length": {"path_length": 1},
            "wrong key usage": {"key_usage": frozenset({5})},
            "AKI differs from SKI": {"aki": b"a" * 20},
            "root EKU": {"eku_oids": (SERVER_AUTH,)},
            "root DNS SAN": {"dns_sans": ("root.invalid",)},
            "root IP SAN": {"ip_sans": ("127.0.0.1",)},
            "root URI SAN": {"uri_sans": ("spiffe://root",)},
            "signature/key mismatch": {"signature_oid": "1.2.840.113549.1.1.11"},
        }
        for label, changes in cases.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-certificate$"
                ):
                    pki._validate_root(
                        certificate_facts(**changes), self.evaluation, self.minimum
                    )

    def test_rejects_every_leaf_policy_drift(self) -> None:
        root_facts = certificate_facts()
        entry = find_entry(
            complete_profile(), "rqlite-http", "s2-http-server"
        )
        cases = {
            "future": {"not_before": self.evaluation + timedelta(seconds=1)},
            "expired": {"not_after": self.evaluation},
            "remaining lifetime": {
                "not_after": self.evaluation + timedelta(seconds=self.minimum - 1)
            },
            "issuer": {"issuer": b"wrong-root"},
            "CA leaf": {"is_ca": True},
            "path length": {"path_length": 0},
            "key usage": {"key_usage": frozenset({0, 1})},
            "AKI": {"aki": b"a" * 20},
            "missing EKU": {"eku_oids": None},
            "wrong EKU": {"eku_oids": (CLIENT_AUTH,)},
            "any EKU": {"eku_oids": ("2.5.29.37.0",)},
            "DNS SAN": {"dns_sans": ("other.invalid",)},
            "IP SAN": {"ip_sans": ("127.0.0.1",)},
            "URI SAN": {"uri_sans": ("spiffe://leaf",)},
            "signature/root mismatch": {
                "signature_oid": "1.2.840.113549.1.1.11"
            },
        }
        for label, changes in cases.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-certificate$"
                ):
                    pki._validate_leaf(
                        leaf_facts(root_facts, entry, **changes),
                        root_facts,
                        entry,
                        self.evaluation,
                        self.minimum,
                    )

    def test_public_key_parser_accepts_only_approved_rsa_and_ec_keys(self) -> None:
        self.assertEqual(pki._public_key(pki._one(rsa_spki(3072), 0x30)), ("RSA", 3072))
        self.assertEqual(pki._public_key(pki._one(rsa_spki(4096), 0x30)), ("RSA", 4096))
        p256_oid = bytes.fromhex("06082a8648ce3d030107")
        p384_oid = bytes.fromhex("06052b81040022")
        self.assertEqual(
            pki._public_key(
                pki._one(ec_spki(p256_oid, b"\x04" + b"\x01" * 64), 0x30)
            ),
            ("EC", 256),
        )
        self.assertEqual(
            pki._public_key(
                pki._one(ec_spki(p384_oid, b"\x04" + b"\x01" * 96), 0x30)
            ),
            ("EC", 384),
        )

        rejected = (
            rsa_spki(2048),
            rsa_spki(3072, exponent=3),
            rsa_spki(3072, include_null=False),
            ec_spki(p256_oid, b"\x02" + b"\x01" * 64),
            ec_spki(p256_oid, b"\x04" + b"\x01" * 63),
            ec_spki(bytes.fromhex("06032b656e"), b"\x04" + b"\x01" * 64),
        )
        for value in rejected:
            with self.subTest(value_length=len(value)):
                with self.assertRaises(pki._DERError):
                    pki._public_key(pki._one(value, 0x30))


class PKISyntheticDERCertificateTests(unittest.TestCase):
    evaluation = datetime(2026, 8, 30, tzinfo=timezone.utc)
    minimum = 30 * 24 * 60 * 60

    def assert_der_rejected(self, value: bytes) -> None:
        with self.assertRaises(pki._DERError):
            pki._parse_certificate(value)

    def test_standard_library_baseline_is_parsed_and_policy_validated(self) -> None:
        public_key = synthetic_public_key("baseline-parser-spki")
        encoded = synthetic_certificate_der(public_key=public_key)
        facts = pki._parse_certificate(encoded)
        self.assertEqual(facts.fingerprint, hashlib.sha256(encoded).hexdigest())
        self.assertEqual(
            facts.spki_sha256,
            hashlib.sha256(public_key).hexdigest(),
        )
        self.assertEqual(facts.serial_hex, "1")
        self.assertEqual(facts.issuer, facts.subject)
        self.assertEqual(facts.signature_oid, "1.2.840.10045.4.3.2")
        self.assertEqual((facts.key_algorithm, facts.key_bits), ("EC", 256))
        self.assertTrue(facts.is_ca)
        self.assertEqual(facts.path_length, 0)
        self.assertEqual(facts.key_usage, frozenset({5, 6}))
        self.assertEqual(facts.ski, b"r" * 20)
        self.assertEqual(facts.aki, b"r" * 20)
        self.assertIsNone(facts.eku_oids)
        self.assertIsNone(
            pki._validate_root(facts, self.evaluation, self.minimum)
        )

    def test_parser_preserves_minimal_lowercase_serial_and_exact_spki(self) -> None:
        public_key = synthetic_public_key("exact-spki-identity")
        encoded = synthetic_certificate_der(
            serial_content=b"\x00\x80",
            public_key=public_key,
        )
        facts = pki._parse_certificate(encoded)
        self.assertEqual(facts.serial_hex, "80")
        self.assertEqual(
            facts.spki_sha256,
            hashlib.sha256(public_key).hexdigest(),
        )

    def test_positive_rsa_and_p384_certificates_pass_parser_and_policy(self) -> None:
        p384_spki = ec_spki(
            bytes.fromhex("06052b81040022"),
            b"\x04" + b"\x02" * 96,
        )
        cases = (
            (
                "RSA-3072-SHA256",
                rsa_spki(3072),
                RSA_SHA256_ALGORITHM,
                "1.2.840.113549.1.1.11",
                "RSA",
                3072,
            ),
            (
                "RSA-4096-SHA384",
                rsa_spki(4096),
                RSA_SHA384_ALGORITHM,
                "1.2.840.113549.1.1.12",
                "RSA",
                4096,
            ),
            (
                "EC-P384-SHA384",
                p384_spki,
                EC_SHA384_ALGORITHM,
                "1.2.840.10045.4.3.3",
                "EC",
                384,
            ),
        )
        entry = {
            "dns_sans": ["leaf.invalid"],
            "eku_oids": [SERVER_AUTH],
            "ip_sans": [],
            "uri_sans": [],
        }
        for label, public_key, algorithm, signature_oid, key_type, key_bits in cases:
            with self.subTest(label=label):
                root_der = synthetic_certificate_der(
                    extensions=root_extensions(),
                    issuer="root",
                    subject="root",
                    inner_algorithm=algorithm,
                    public_key=public_key,
                )
                root = pki._parse_certificate(root_der)
                self.assertEqual(root.signature_oid, signature_oid)
                self.assertEqual((root.key_algorithm, root.key_bits), (key_type, key_bits))
                self.assertIsNone(
                    pki._validate_root(root, self.evaluation, self.minimum)
                )

                leaf_der = synthetic_certificate_der(
                    extensions=leaf_extensions(),
                    issuer="root",
                    subject="leaf",
                    serial_content=b"\x02",
                    inner_algorithm=algorithm,
                    public_key=public_key,
                )
                leaf = pki._parse_certificate(leaf_der)
                self.assertEqual(leaf.signature_oid, signature_oid)
                self.assertEqual((leaf.key_algorithm, leaf.key_bits), (key_type, key_bits))
                self.assertIsNone(
                    pki._validate_leaf(
                        leaf,
                        root,
                        entry,
                        self.evaluation,
                        self.minimum,
                    )
                )

    def test_rejects_malformed_and_noncanonical_der_envelopes(self) -> None:
        valid = synthetic_certificate_der()
        variants = (
            b"",
            b"\x30\x80\x00\x00",
            valid[:-1],
            valid + b"\x00",
            b"\x31" + valid[1:],
            b"x" * 65537,
            der(0x30, b""),
        )
        for value in variants:
            with self.subTest(length=len(value), prefix=value[:4]):
                self.assert_der_rejected(value)

    def test_basic_constraints_criticality_encoding_and_pathlen(self) -> None:
        valid = root_extensions()
        basic_value = der(0x30, der(0x01, b"\xff") + der_integer(b"\x00"))
        invalid_extensions = (
            [extension_der("2.5.29.19", basic_value), *valid[1:]],
            [
                extension_der(
                    "2.5.29.19",
                    basic_value,
                    critical_encoding=der(0x01, b"\x00"),
                ),
                *valid[1:],
            ],
            [
                extension_der(
                    "2.5.29.19",
                    der(0x30, der(0x01, b"\x00")),
                    critical=True,
                ),
                *valid[1:],
            ],
            [
                extension_der(
                    "2.5.29.19",
                    der(0x30, der_integer(b"\x00")),
                    critical=True,
                ),
                *valid[1:],
            ],
            [
                extension_der(
                    "2.5.29.19",
                    der(
                        0x30,
                        der(0x01, b"\xff") + der_integer(b"\x00\x01"),
                    ),
                    critical=True,
                ),
                *valid[1:],
            ],
        )
        for extensions in invalid_extensions:
            with self.subTest(first=extensions[0]):
                self.assert_der_rejected(
                    synthetic_certificate_der(extensions=extensions)
                )

        for label, basic in (
            ("missing pathlen", der(0x30, der(0x01, b"\xff"))),
            (
                "pathlen one",
                der(0x30, der(0x01, b"\xff") + der_integer(b"\x01")),
            ),
        ):
            extensions = root_extensions(basic=basic)
            facts = pki._parse_certificate(
                synthetic_certificate_der(extensions=extensions)
            )
            with self.subTest(label=label), self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:invalid-certificate$"
            ):
                pki._validate_root(facts, self.evaluation, self.minimum)

    def test_key_usage_must_be_critical_canonical_and_exact(self) -> None:
        valid = root_extensions()
        invalid_values = (
            der(0x03, b"\x00"),
            der(0x03, b"\x08\x80"),
            der(0x03, b"\x07\x81"),
            der(0x03, b"\x00\x80"),
            der(0x04, b"\x01\x06"),
        )
        variants = [[valid[0], extension_der("2.5.29.15", key_usage_value({5, 6})), *valid[2:]]]
        variants.extend(
            [
                valid[0],
                extension_der("2.5.29.15", value, critical=True),
                *valid[2:],
            ]
            for value in invalid_values
        )
        for extensions in variants:
            with self.subTest(extension=extensions[1]):
                self.assert_der_rejected(
                    synthetic_certificate_der(extensions=extensions)
                )

        wrong_usage = root_extensions(key_usage=key_usage_value({5}))
        facts = pki._parse_certificate(
            synthetic_certificate_der(extensions=wrong_usage)
        )
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-certificate$"
        ):
            pki._validate_root(facts, self.evaluation, self.minimum)

    def test_eku_rejects_duplicates_order_and_critical_encoding(self) -> None:
        invalid_lists = (
            (SERVER_AUTH, SERVER_AUTH),
            (CLIENT_AUTH, SERVER_AUTH),
        )
        for eku_oids in invalid_lists:
            with self.subTest(eku_oids=eku_oids):
                self.assert_der_rejected(
                    synthetic_certificate_der(
                        extensions=leaf_extensions(eku_oids=eku_oids),
                        issuer="root",
                        subject="leaf",
                    )
                )

        extensions = leaf_extensions()
        critical_eku = extension_der(
            "2.5.29.37",
            der(0x30, oid_der(SERVER_AUTH)),
            critical=True,
        )
        extensions[4] = critical_eku
        self.assert_der_rejected(
            synthetic_certificate_der(
                extensions=extensions, issuer="root", subject="leaf"
            )
        )

    def test_rejects_unknown_critical_and_duplicate_extensions(self) -> None:
        unknown_value = der(0x05, b"")
        invalid = (
            root_extensions(
                extras=(
                    extension_der(
                        "1.2.3.4", unknown_value, critical=True
                    ),
                )
            ),
            root_extensions(
                extras=(
                    extension_der("2.5.29.14", der(0x04, b"x" * 20)),
                )
            ),
            root_extensions(
                extras=(
                    extension_der(
                        "2.5.29.19",
                        der(
                            0x30,
                            der(0x01, b"\xff") + der_integer(b"\x00"),
                        ),
                        critical=True,
                    ),
                )
            ),
        )
        for extensions in invalid:
            with self.subTest(extra=extensions[-1]):
                self.assert_der_rejected(
                    synthetic_certificate_der(extensions=extensions)
                )

        accepted = root_extensions(
            extras=(extension_der("1.2.3.4", unknown_value),)
        )
        self.assertTrue(
            pki._parse_certificate(
                synthetic_certificate_der(extensions=accepted)
            ).is_ca
        )

    def test_serial_is_positive_minimal_and_at_most_twenty_octets(self) -> None:
        invalid = (
            b"\x00",
            b"\x80",
            b"\x00\x01",
            b"\x01" * 21,
        )
        for serial in invalid:
            with self.subTest(serial=serial):
                self.assert_der_rejected(
                    synthetic_certificate_der(serial_content=serial)
                )
        positive_twenty = b"\x01" * 20
        self.assertTrue(
            pki._parse_certificate(
                synthetic_certificate_der(serial_content=positive_twenty)
            ).is_ca
        )

    def test_rfc5280_time_tags_and_2049_2050_boundary(self) -> None:
        valid = (
            ((0x17, b"500101000000Z"), (0x17, b"491231235959Z")),
            ((0x18, b"19490101000000Z"), (0x18, b"19491231235959Z")),
            ((0x18, b"20500101000000Z"), (0x18, b"20510101000000Z")),
        )
        for not_before, not_after in valid:
            with self.subTest(not_before=not_before, not_after=not_after):
                facts = pki._parse_certificate(
                    synthetic_certificate_der(
                        not_before=not_before, not_after=not_after
                    )
                )
                self.assertLess(facts.not_before, facts.not_after)

        invalid = (
            ((0x18, b"20490101000000Z"), (0x18, b"20491231235959Z")),
            ((0x17, b"2608290000Z"), (0x17, b"270829000000Z")),
            ((0x17, b"260829000000+0000"), (0x17, b"270829000000Z")),
            ((0x18, b"20500101000000.0Z"), (0x18, b"20510101000000Z")),
        )
        for not_before, not_after in invalid:
            with self.subTest(not_before=not_before):
                self.assert_der_rejected(
                    synthetic_certificate_der(
                        not_before=not_before, not_after=not_after
                    )
                )

    def test_signature_algorithms_are_known_exact_and_inner_outer_identical(self) -> None:
        self.assert_der_rejected(
            synthetic_certificate_der(outer_algorithm=EC_SHA384_ALGORITHM)
        )

        rsa_facts = pki._parse_certificate(
            synthetic_certificate_der(inner_algorithm=RSA_SHA256_ALGORITHM)
        )
        self.assertEqual(rsa_facts.signature_oid, "1.2.840.113549.1.1.11")

        rsa_oid = "1.2.840.113549.1.1.11"
        invalid_algorithms = (
            algorithm_der(rsa_oid),
            algorithm_der(rsa_oid, der_integer(b"\x00")),
            algorithm_der("1.2.3.4"),
        )
        for algorithm in invalid_algorithms:
            with self.subTest(algorithm=algorithm):
                self.assert_der_rejected(
                    synthetic_certificate_der(inner_algorithm=algorithm)
                )

    def test_only_direct_leaf_to_pinned_root_is_accepted(self) -> None:
        root = pki._parse_certificate(synthetic_certificate_der())
        entry = {
            "dns_sans": ["leaf.invalid"],
            "eku_oids": [SERVER_AUTH],
            "ip_sans": [],
            "uri_sans": [],
        }
        leaf = pki._parse_certificate(
            synthetic_certificate_der(
                extensions=leaf_extensions(),
                issuer="root",
                subject="leaf",
            )
        )
        self.assertIsNone(
            pki._validate_leaf(leaf, root, entry, self.evaluation, self.minimum)
        )

        intermediate = pki._parse_certificate(
            synthetic_certificate_der(
                extensions=root_extensions(ski=b"i" * 20),
                issuer="root",
                subject="intermediate",
            )
        )
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-certificate$"
        ):
            pki._validate_root(
                intermediate, self.evaluation, self.minimum
            )
        indirect_leaf = pki._parse_certificate(
            synthetic_certificate_der(
                extensions=leaf_extensions(aki=b"i" * 20),
                issuer="intermediate",
                subject="indirect-leaf",
            )
        )
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-certificate$"
        ):
            pki._validate_leaf(
                indirect_leaf,
                root,
                entry,
                self.evaluation,
                self.minimum,
            )


class PKIBundleBoundaryTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name) / "bundle"
        self.root.mkdir(mode=0o700)
        self.profile = complete_profile()
        (self.root / "pki-profile.json").write_bytes(canonical(self.profile))
        for domain in self.profile["trust_domains"]:
            (self.root / domain["ca_certificate"]).write_bytes(
                b"-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n"
            )
            for certificate in domain["certificates"]:
                (self.root / certificate["certificate"]).write_bytes(
                    b"-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n"
                )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def test_rejects_private_key_marker_before_openssl(self) -> None:
        first = self.profile["trust_domains"][0]["certificates"][0]["certificate"]
        (self.root / first).write_bytes(
            b"-----BEGIN PRIVATE KEY-----\nsynthetic\n-----END PRIVATE KEY-----\n"
        )
        runner = RejectingRunner()
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-members$"
        ):
            verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_rejects_unexpected_member_before_openssl(self) -> None:
        (self.root / "unexpected.env").write_text("synthetic", encoding="utf-8")
        runner = RejectingRunner()
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-members$"
        ):
            verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_rejects_root_directory_symlink_before_opening_members(self) -> None:
        link = Path(self.temporary.name) / "bundle-link"
        runner = RejectingRunner()
        try:
            link.symlink_to(self.root, target_is_directory=True)
        except OSError:
            metadata = os.lstat(self.root)
            values = list(metadata)
            values[0] = pki.stat.S_IFLNK | 0o777
            symlink_metadata = os.stat_result(values)
            with mock.patch.object(pki.os, "lstat", return_value=symlink_metadata):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-input$"
                ):
                    verify_directory(link, runner=runner)
        else:
            with self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:invalid-input$"
            ):
                verify_directory(link, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_detects_member_and_root_replacement_races(self) -> None:
        member_path = self.root / self.profile["trust_domains"][0][
            "ca_certificate"
        ]
        descriptor = os.open(member_path, os.O_RDONLY)
        try:
            metadata = os.fstat(descriptor)
            pinned = pki._PinnedMember(
                member_path.name,
                descriptor,
                metadata,
                member_path.read_bytes(),
            )
            replacement = Path(self.temporary.name) / "replacement-member"
            replacement.write_bytes(b"replacement-race")
            changed = os.stat(replacement)
            with mock.patch.object(pki.os, "fstat", return_value=changed):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-members$"
                ):
                    pki._ensure_member_stable(pinned)
        finally:
            os.close(descriptor)

        pinned_root = pki._open_root(self.root)
        try:
            replacement_root = Path(self.temporary.name) / "replacement-root"
            replacement_root.mkdir()
            changed_root = os.stat(replacement_root)
            with mock.patch.object(pki.os, "lstat", return_value=changed_root):
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-members$"
                ):
                    pki._ensure_root_stable(pinned_root)
        finally:
            pinned_root.close()

    def test_rejects_duplicate_certificate_fingerprint_under_valid_names(self) -> None:
        write_synthetic_bundle(self.root, self.profile)
        first = self.root / find_entry(
            self.profile,
            "bot-gateway",
            "s2-telegram-bot-primary-client",
        )["certificate"]
        duplicate = self.root / find_entry(
            self.profile,
            "bot-gateway",
            "s3-telegram-bot-primary-client",
        )["certificate"]
        duplicate.write_bytes(first.read_bytes())
        duplicate.chmod(0o600)
        runner = RejectingRunner()
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-certificate$"
        ):
            verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_rejects_duplicate_spki_with_different_certificates_before_openssl(self) -> None:
        first_member = find_entry(
            self.profile,
            "bot-gateway",
            "s2-telegram-bot-primary-client",
        )["certificate"]
        second_member = find_entry(
            self.profile,
            "bot-gateway",
            "s3-telegram-bot-primary-client",
        )["certificate"]
        reused_spki = synthetic_public_key("deliberately-reused-spki")
        write_synthetic_bundle(
            self.root,
            self.profile,
            spki_overrides={
                first_member: reused_spki,
                second_member: reused_spki,
            },
        )
        self.assertNotEqual(
            (self.root / first_member).read_bytes(),
            (self.root / second_member).read_bytes(),
        )
        runner = RejectingRunner()
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-certificate$"
        ):
            verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_rejects_duplicate_serial_within_issuer_before_openssl(self) -> None:
        first_member = find_entry(
            self.profile,
            "bot-gateway",
            "s2-telegram-bot-primary-client",
        )["certificate"]
        second_member = find_entry(
            self.profile,
            "bot-gateway",
            "s3-telegram-bot-primary-client",
        )["certificate"]
        write_synthetic_bundle(
            self.root,
            self.profile,
            serial_overrides={first_member: 900, second_member: 900},
        )
        self.assertNotEqual(
            (self.root / first_member).read_bytes(),
            (self.root / second_member).read_bytes(),
        )
        runner = RejectingRunner()
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-certificate$"
        ):
            verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_allows_same_serial_across_different_issuers(self) -> None:
        first_member = find_entry(
            self.profile,
            "bot-gateway",
            "s2-telegram-bot-primary-client",
        )["certificate"]
        second_member = find_entry(
            self.profile,
            "dispatcher",
            "s2-controlplane-dispatcher-client",
        )["certificate"]
        write_synthetic_bundle(
            self.root,
            self.profile,
            serial_overrides={first_member: 901, second_member: 901},
        )
        with mock.patch.object(
            pki, "_linux_runtime", return_value=True
        ), mock.patch.object(
            pki,
            "_openssl_version",
            return_value="OpenSSL 3.0.13 30 Jan 2024",
        ), mock.patch.object(pki, "_verify_leaf_chain"):
            evidence = verify_directory(self.root, runner=RejectingRunner())
        self.assertEqual(validate_evidence(canonical_bytes(evidence)), evidence)

    def test_peer_second_purpose_failure_stops_schedule_fail_closed(self) -> None:
        write_synthetic_bundle(self.root, self.profile)
        calls: list[tuple[str, str]] = []

        def verify_chain(
            _runner: object,
            _root: object,
            _ca_member: object,
            leaf_member: pki._PinnedMember,
            _evaluation_epoch: int,
            purpose: str,
        ) -> None:
            calls.append((leaf_member.name, purpose))
            if leaf_member.name == "s2-raft-peer.pem" and purpose == "sslclient":
                raise PKIVerificationError("pki-verify:openssl-failed")

        runner = RejectingRunner()
        with mock.patch.object(
            pki, "_linux_runtime", return_value=True
        ), mock.patch.object(
            pki,
            "_openssl_version",
            return_value="OpenSSL 3.0.13 30 Jan 2024",
        ), mock.patch.object(
            pki, "_verify_leaf_chain", side_effect=verify_chain
        ):
            with self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:openssl-failed$"
            ):
                verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])
        peer_calls = [item for item in calls if "raft-peer" in item[0]]
        self.assertEqual(
            peer_calls,
            [
                ("s2-raft-peer.pem", "sslserver"),
                ("s2-raft-peer.pem", "sslclient"),
            ],
        )

    def test_rejects_hardlinked_certificate_before_openssl(self) -> None:
        source = self.root / self.profile["trust_domains"][0]["ca_certificate"]
        linked = self.root / self.profile["trust_domains"][1]["ca_certificate"]
        linked.unlink()
        try:
            os.link(source, linked)
        except OSError as error:
            self.skipTest("hardlinks unavailable: " + error.__class__.__name__)
        runner = RejectingRunner()
        with self.assertRaisesRegex(
            PKIVerificationError, "^pki-verify:invalid-members$"
        ):
            verify_directory(self.root, runner=runner)
        self.assertEqual(runner.calls, [])

    def test_rejects_symlink_directory_empty_and_oversize_members(self) -> None:
        first = self.profile["trust_domains"][0]["certificates"][0][
            "certificate"
        ]
        target = self.root / first
        original = target.read_bytes()

        variants = ("directory", "empty", "oversize", "symlink")
        for label in variants:
            with self.subTest(label=label):
                if target.is_symlink() or target.is_file():
                    target.unlink()
                elif target.is_dir():
                    target.rmdir()
                if label == "directory":
                    target.mkdir()
                elif label == "empty":
                    target.write_bytes(b"")
                elif label == "oversize":
                    target.write_bytes(b"x" * 131073)
                else:
                    symlink_target = self.root / self.profile["trust_domains"][0][
                        "ca_certificate"
                    ]
                    try:
                        target.symlink_to(symlink_target)
                    except OSError as error:
                        target.write_bytes(original)
                        self.skipTest(
                            "symlinks unavailable: " + error.__class__.__name__
                        )
                runner = RejectingRunner()
                with self.assertRaisesRegex(
                    PKIVerificationError, "^pki-verify:invalid-members$"
                ):
                    verify_directory(self.root, runner=runner)
                self.assertEqual(runner.calls, [])
                if target.is_symlink() or target.is_file():
                    target.unlink()
                elif target.is_dir():
                    target.rmdir()
                target.write_bytes(original)

@unittest.skipUnless(IS_LINUX, "real OpenSSL integration runs only on Linux")
class PKILinuxOpenSSLIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        super().setUpClass()
        cls.openssl = Path("/usr/bin/openssl")
        if not cls.openssl.is_file():
            raise AssertionError("Linux contract requires /usr/bin/openssl")
        cls.temporary = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.temporary.cleanup)
        cls.workspace = Path(cls.temporary.name)
        cls.bundle = cls.workspace / "bundle"
        cls.secrets = cls.workspace / "secrets"
        cls.work = cls.workspace / "work"
        cls.bundle.mkdir(mode=0o700)
        cls.secrets.mkdir(mode=0o700)
        cls.work.mkdir(mode=0o700)
        cls.profile = complete_profile()
        cls._generate_complete_bundle()
        evaluation = datetime.now(timezone.utc).replace(microsecond=0)
        cls.profile["evaluation_time"] = evaluation.strftime("%Y-%m-%dT%H:%M:%SZ")
        profile_path = cls.bundle / "pki-profile.json"
        profile_path.write_bytes(canonical(cls.profile))
        profile_path.chmod(0o600)

    @classmethod
    def _run(cls, *arguments: str) -> None:
        result = subprocess.run(
            [str(cls.openssl), *arguments],
            cwd=cls.work,
            check=False,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            timeout=20,
        )
        if result.returncode != 0:
            command = " ".join(arguments[:2])
            raise AssertionError(
                "synthetic OpenSSL setup failed: "
                + command
                + " rc="
                + str(result.returncode)
            )

    @classmethod
    def _write_config(cls, name: str, lines: list[str]) -> Path:
        path = cls.work / name
        path.write_text("\n".join(lines) + "\n", encoding="ascii")
        path.chmod(0o600)
        return path

    @classmethod
    def _generate_complete_bundle(cls) -> None:
        serial = 1000
        for domain in cls.profile["trust_domains"]:
            domain_name = domain["name"]
            ca_key = cls.secrets / (domain_name + "-ca.key")
            ca_csr = cls.work / (domain_name + "-ca.csr")
            ca_certificate = cls.bundle / domain["ca_certificate"]
            ca_config = cls._write_config(
                domain_name + "-ca.cnf",
                [
                    "[v3_ca]",
                    "basicConstraints=critical,CA:true,pathlen:0",
                    "keyUsage=critical,keyCertSign,cRLSign",
                    "subjectKeyIdentifier=hash",
                    "authorityKeyIdentifier=keyid:always",
                ],
            )
            cls._run(
                "genpkey",
                "-algorithm",
                "EC",
                "-pkeyopt",
                "ec_paramgen_curve:P-256",
                "-out",
                str(ca_key),
            )
            ca_key.chmod(0o600)
            cls._run(
                "req",
                "-new",
                "-key",
                str(ca_key),
                "-subj",
                "/CN=" + domain_name + "-ca",
                "-out",
                str(ca_csr),
            )
            cls._run(
                "x509",
                "-req",
                "-in",
                str(ca_csr),
                "-signkey",
                str(ca_key),
                "-set_serial",
                str(serial),
                "-days",
                "400",
                "-sha256",
                "-extfile",
                str(ca_config),
                "-extensions",
                "v3_ca",
                "-out",
                str(ca_certificate),
            )
            serial += 1
            ca_certificate.chmod(0o600)

            for entry in domain["certificates"]:
                role = entry["role"]
                leaf_key = cls.secrets / (role + ".key")
                leaf_csr = cls.work / (role + ".csr")
                leaf_certificate = cls.bundle / entry["certificate"]
                eku = ",".join(
                    "serverAuth" if oid == SERVER_AUTH else "clientAuth"
                    for oid in entry["eku_oids"]
                )
                config_lines = [
                    "[v3_leaf]",
                    "basicConstraints=critical,CA:false",
                    "keyUsage=critical,digitalSignature",
                    "subjectKeyIdentifier=hash",
                    "authorityKeyIdentifier=keyid:always",
                    "extendedKeyUsage=" + eku,
                ]
                if entry["dns_sans"] or entry["ip_sans"] or entry["uri_sans"]:
                    config_lines.append("subjectAltName=@alt_names")
                    config_lines.append("[alt_names]")
                    for index, value in enumerate(entry["dns_sans"], start=1):
                        config_lines.append(f"DNS.{index}={value}")
                    for index, value in enumerate(entry["ip_sans"], start=1):
                        config_lines.append(f"IP.{index}={value}")
                    for index, value in enumerate(entry["uri_sans"], start=1):
                        config_lines.append(f"URI.{index}={value}")
                leaf_config = cls._write_config(role + ".cnf", config_lines)
                cls._run(
                    "genpkey",
                    "-algorithm",
                    "EC",
                    "-pkeyopt",
                    "ec_paramgen_curve:P-256",
                    "-out",
                    str(leaf_key),
                )
                leaf_key.chmod(0o600)
                cls._run(
                    "req",
                    "-new",
                    "-key",
                    str(leaf_key),
                    "-subj",
                    "/CN=" + role,
                    "-out",
                    str(leaf_csr),
                )
                cls._run(
                    "x509",
                    "-req",
                    "-in",
                    str(leaf_csr),
                    "-CA",
                    str(ca_certificate),
                    "-CAkey",
                    str(ca_key),
                    "-set_serial",
                    str(serial),
                    "-days",
                    "400",
                    "-sha256",
                    "-extfile",
                    str(leaf_config),
                    "-extensions",
                    "v3_leaf",
                    "-out",
                    str(leaf_certificate),
                )
                serial += 1
                leaf_certificate.chmod(0o600)

    def test_complete_7_domain_37_role_bundle_and_fixed_verify_schedule(self) -> None:
        calls: list[tuple[tuple[str, ...], tuple[int, ...]]] = []

        def recording_runner(
            argv: tuple[str, ...],
            *,
            pass_fds: tuple[int, ...],
            timeout_seconds: int,
            output_limit: int,
        ) -> OpenSSLResult:
            calls.append((argv, pass_fds))
            return pki.default_openssl_runner(
                argv,
                pass_fds=pass_fds,
                timeout_seconds=timeout_seconds,
                output_limit=output_limit,
            )

        evidence = verify_directory(self.bundle, runner=recording_runner)
        self.assertEqual(validate_evidence(canonical_bytes(evidence)), evidence)
        self.assertEqual(len(evidence["trust_domains"]), 7)
        self.assertEqual(
            sum(len(domain["certificates"]) for domain in evidence["trust_domains"]),
            37,
        )
        self.assertEqual(len(calls), 41)
        self.assertEqual(calls[0], (("/usr/bin/openssl", "version", "-v"), ()))
        verify_calls = calls[1:]
        self.assertTrue(
            all(
                pki._allowed_openssl_argv(argv, pass_fds)
                for argv, pass_fds in verify_calls
            )
        )
        self.assertEqual(
            sum(argv[16] == "sslserver" for argv, _ in verify_calls), 16
        )
        self.assertEqual(
            sum(argv[16] == "sslclient" for argv, _ in verify_calls), 24
        )
        for argv, pass_fds in verify_calls:
            self.assertEqual(len(pass_fds), 2)
            self.assertEqual(tuple(sorted(set(pass_fds))), pass_fds)
            self.assertEqual(
                {argv[3], argv[17]},
                {f"/proc/self/fd/{descriptor}" for descriptor in pass_fds},
            )
        encoded = canonical_bytes(evidence)
        for marker in (
            b".pem",
            b"PRIVATE",
            b"/proc/self/fd",
            str(self.workspace).encode("utf-8"),
        ):
            self.assertNotIn(marker, encoded)
        self.assertEqual(
            sorted(path.name for path in self.bundle.iterdir()),
            sorted(
                ["pki-profile.json"]
                + [
                    domain["ca_certificate"]
                    for domain in self.profile["trust_domains"]
                ]
                + [
                    entry["certificate"]
                    for domain in self.profile["trust_domains"]
                    for entry in domain["certificates"]
                ]
            ),
        )
        self.assertFalse(any(self.bundle.glob("*.key")))

    def test_real_openssl_version_is_killed_at_output_limit_plus_one(self) -> None:
        with self.assertRaises(pki._OutputLimitError):
            pki.default_openssl_runner(
                ("/usr/bin/openssl", "version", "-v"),
                output_limit=1,
            )

    def test_profile_san_drift_is_rejected_before_openssl(self) -> None:
        profile_path = self.bundle / "pki-profile.json"
        original = profile_path.read_bytes()
        mutated = copy.deepcopy(self.profile)
        find_entry(mutated, "rqlite-http", "s2-http-server")["dns_sans"] = [
            "wrong.invalid"
        ]
        try:
            profile_path.write_bytes(canonical(mutated))
            profile_path.chmod(0o600)
            runner = RejectingRunner()
            with self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:invalid-input$"
            ):
                verify_directory(self.bundle, runner=runner)
            self.assertEqual(runner.calls, [])
        finally:
            profile_path.write_bytes(original)
            profile_path.chmod(0o600)

    def test_rejects_group_writable_member_and_nonprivate_root(self) -> None:
        first = self.bundle / self.profile["trust_domains"][0]["ca_certificate"]
        try:
            first.chmod(0o620)
            with self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:invalid-members$"
            ):
                verify_directory(self.bundle, runner=RejectingRunner())
        finally:
            first.chmod(0o600)
        try:
            self.bundle.chmod(0o750)
            with self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:invalid-input$"
            ):
                verify_directory(self.bundle, runner=RejectingRunner())
        finally:
            self.bundle.chmod(0o700)

    def test_corrupted_leaf_signature_is_rejected_by_openssl(self) -> None:
        first = self.profile["trust_domains"][0]["certificates"][0][
            "certificate"
        ]
        path = self.bundle / first
        original = path.read_bytes()
        certificate_der = bytearray(pki._decode_certificate(original))
        certificate_der[-1] ^= 1
        encoded = base64.b64encode(certificate_der).decode("ascii")
        lines = [encoded[index : index + 64] for index in range(0, len(encoded), 64)]
        corrupted = (
            "-----BEGIN CERTIFICATE-----\n"
            + "\n".join(lines)
            + "\n-----END CERTIFICATE-----\n"
        ).encode("ascii")
        calls: list[tuple[str, ...]] = []

        def recording_runner(
            argv: tuple[str, ...],
            *,
            pass_fds: tuple[int, ...],
            timeout_seconds: int,
            output_limit: int,
        ) -> OpenSSLResult:
            calls.append(argv)
            return pki.default_openssl_runner(
                argv,
                pass_fds=pass_fds,
                timeout_seconds=timeout_seconds,
                output_limit=output_limit,
            )

        try:
            path.write_bytes(corrupted)
            path.chmod(0o600)
            with self.assertRaisesRegex(
                PKIVerificationError, "^pki-verify:openssl-failed$"
            ):
                verify_directory(self.bundle, runner=recording_runner)
            self.assertEqual(calls[0], ("/usr/bin/openssl", "version", "-v"))
            self.assertEqual(len(calls), 2)
        finally:
            path.write_bytes(original)
            path.chmod(0o600)


class PKICLITests(unittest.TestCase):
    def run_cli(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(SCRIPT), *arguments],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            timeout=8,
        )

    def test_cli_help_is_exact_and_side_effect_free(self) -> None:
        result = self.run_cli("--help")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "usage: pki-verify.py --root DIRECTORY\n")
        self.assertEqual(result.stderr, "")

    def test_cli_rejects_every_invalid_argument_shape_with_one_fixed_error(self) -> None:
        variants = (
            (),
            ("--root",),
            ("--unknown", "value"),
            ("--root", "one", "extra"),
            ("--help", "extra"),
        )
        for arguments in variants:
            with self.subTest(arguments=arguments):
                result = self.run_cli(*arguments)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, "")
                self.assertEqual(result.stderr, "pki-verify:invalid-input\n")

    def test_cli_emits_one_fixed_error_for_missing_bundle(self) -> None:
        result = self.run_cli("--root", str(ROOT / "missing-pki-bundle"))
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr, "pki-verify:invalid-input\n")


if __name__ == "__main__":
    unittest.main()
