"""Offline fail-closed PKI verifier for the MaestroVPN HA control plane."""

from __future__ import annotations

import base64
from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import selectors
import stat
import subprocess
import sys
import time
from typing import Any, Callable, Protocol, Sequence


class PKIVerificationError(ValueError):
    """A fixed, redacted PKI verification failure."""


_ERRORS = {
    "input": "pki-verify:invalid-input",
    "members": "pki-verify:invalid-members",
    "certificate": "pki-verify:invalid-certificate",
    "unsupported": "pki-verify:unsupported-openssl",
    "openssl": "pki-verify:openssl-failed",
    "timeout": "pki-verify:openssl-timeout",
    "output": "pki-verify:invalid-openssl-output",
    "evidence": "pki-verify:invalid-evidence",
}
_PROFILE_SCHEMA = "maestro-ha-pki-profile-v1"
_EVIDENCE_SCHEMA = "maestro-ha-pki-evidence-v1"
_PROFILE_NAME = "pki-profile.json"
_SERVER_AUTH = "1.3.6.1.5.5.7.3.1"
_CLIENT_AUTH = "1.3.6.1.5.5.7.3.2"
_ANY_EKU = "2.5.29.37.0"
_MAX_PROFILE_BYTES = 262144
_MAX_CERTIFICATE_BYTES = 131072
_MAX_DER_BYTES = 65536
_MAX_OPENSSL_OUTPUT = 65536
_OPENSSL_TIMEOUT_SECONDS = 8
_MAX_POSITIVE_INTEGER = 2**63 - 1
_DIGEST_RE = re.compile(r"[0-9a-f]{64}\Z")
_SERIAL_HEX_RE = re.compile(r"[1-9a-f][0-9a-f]{0,39}\Z")
_MEMBER_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,126}\Z")
_TIMESTAMP_RE = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\Z")
_OPENSSL_VERSION_RE = re.compile(
    r"OpenSSL 3\.[0-9]+\.[0-9]+[a-z]? "
    r"[0-9]{1,2} (?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) "
    r"[0-9]{4}(?: \(Library: OpenSSL 3\.[0-9]+\.[0-9]+[a-z]? "
    r"[0-9]{1,2} (?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) "
    r"[0-9]{4}\))?\Z"
)
_DNS_LABEL_RE = re.compile(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\Z")
_URI_RE = re.compile(r"[a-z][a-z0-9+.-]*:[\x21-\x7e]{1,500}\Z")
_PRIVATE_MARKERS = (
    b"-----BEGIN PRIVATE KEY-----",
    b"-----BEGIN ENCRYPTED PRIVATE KEY-----",
    b"-----BEGIN RSA PRIVATE KEY-----",
    b"-----BEGIN EC PRIVATE KEY-----",
    b"-----BEGIN DSA PRIVATE KEY-----",
    b"-----BEGIN OPENSSH PRIVATE KEY-----",
)

_ROLE_MATRIX: dict[str, dict[str, tuple[str, tuple[str, ...]]]] = {
    "bot-gateway": {
        "s2-bot-gateway-server": ("server", (_SERVER_AUTH,)),
        "s2-telegram-bot-primary-client": ("client", (_CLIENT_AUTH,)),
        "s2-telegram-bot-secondary-client": ("client", (_CLIENT_AUTH,)),
        "s3-bot-gateway-server": ("server", (_SERVER_AUTH,)),
        "s3-telegram-bot-primary-client": ("client", (_CLIENT_AUTH,)),
        "s3-telegram-bot-secondary-client": ("client", (_CLIENT_AUTH,)),
        "s4-bot-gateway-server": ("server", (_SERVER_AUTH,)),
        "s4-telegram-bot-primary-client": ("client", (_CLIENT_AUTH,)),
        "s4-telegram-bot-secondary-client": ("client", (_CLIENT_AUTH,)),
    },
    "dispatcher": {
        "s2-controlplane-dispatcher-client": ("client", (_CLIENT_AUTH,)),
        "s3-controlplane-dispatcher-client": ("client", (_CLIENT_AUTH,)),
        "s4-controlplane-dispatcher-client": ("client", (_CLIENT_AUTH,)),
    },
    "github-probe": {
        "github-workflow-probe-client": ("client", (_CLIENT_AUTH,)),
    },
    "lease-verifier": {
        "s1-agent-lease-client": ("client", (_CLIENT_AUTH,)),
        "s2-agent-lease-client": ("client", (_CLIENT_AUTH,)),
        "s2-lease-verifier-server": ("server", (_SERVER_AUTH,)),
        "s3-agent-lease-client": ("client", (_CLIENT_AUTH,)),
        "s3-lease-verifier-server": ("server", (_SERVER_AUTH,)),
        "s4-agent-lease-client": ("client", (_CLIENT_AUTH,)),
        "s4-lease-verifier-server": ("server", (_SERVER_AUTH,)),
    },
    "node-status": {
        "s1-agent-status-server": ("server", (_SERVER_AUTH,)),
        "s2-agent-status-server": ("server", (_SERVER_AUTH,)),
        "s3-agent-status-server": ("server", (_SERVER_AUTH,)),
        "s4-agent-status-server": ("server", (_SERVER_AUTH,)),
    },
    "rqlite-http": {
        "importer-rqlite-client": ("client", (_CLIENT_AUTH,)),
        "s2-backup-rqlite-client": ("client", (_CLIENT_AUTH,)),
        "s2-http-server": ("server", (_SERVER_AUTH,)),
        "s2-panel-rqlite-client": ("client", (_CLIENT_AUTH,)),
        "s3-backup-rqlite-client": ("client", (_CLIENT_AUTH,)),
        "s3-http-server": ("server", (_SERVER_AUTH,)),
        "s3-panel-rqlite-client": ("client", (_CLIENT_AUTH,)),
        "s4-backup-rqlite-client": ("client", (_CLIENT_AUTH,)),
        "s4-http-server": ("server", (_SERVER_AUTH,)),
        "s4-panel-rqlite-client": ("client", (_CLIENT_AUTH,)),
    },
    "rqlite-raft": {
        "s2-raft-peer": ("peer", (_SERVER_AUTH, _CLIENT_AUTH)),
        "s3-raft-peer": ("peer", (_SERVER_AUTH, _CLIENT_AUTH)),
        "s4-raft-peer": ("peer", (_SERVER_AUTH, _CLIENT_AUTH)),
    },
}
_FIXED_DNS_SANS = {
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
_PROFILE_KEYS = {
    "deployment_authorized",
    "evaluation_time",
    "minimum_remaining_seconds",
    "release_readiness",
    "schema",
    "trust_domains",
}
_DOMAIN_KEYS = {"ca_certificate", "certificates", "name"}
_CERTIFICATE_KEYS = {
    "certificate",
    "dns_sans",
    "eku_oids",
    "ip_sans",
    "purpose",
    "role",
    "uri_sans",
}
_EVIDENCE_KEYS = {
    "blockers",
    "deployment_authorized",
    "evaluation_time",
    "openssl_version",
    "profile_sha256",
    "release_readiness",
    "schema",
    "trust_domains",
}
_EVIDENCE_DOMAIN_KEYS = {
    "ca_fingerprint_sha256",
    "ca_not_after",
    "ca_serial_hex",
    "ca_spki_sha256",
    "certificates",
    "name",
}
_EVIDENCE_CERTIFICATE_KEYS = {
    "certificate_fingerprint_sha256",
    "certificate_serial_hex",
    "certificate_spki_sha256",
    "not_after",
    "role",
}

_OID_BASIC_CONSTRAINTS = "2.5.29.19"
_OID_KEY_USAGE = "2.5.29.15"
_OID_SUBJECT_KEY_IDENTIFIER = "2.5.29.14"
_OID_AUTHORITY_KEY_IDENTIFIER = "2.5.29.35"
_OID_EXTENDED_KEY_USAGE = "2.5.29.37"
_OID_SUBJECT_ALT_NAME = "2.5.29.17"
_KNOWN_CRITICAL_EXTENSIONS = {
    _OID_BASIC_CONSTRAINTS,
    _OID_KEY_USAGE,
    _OID_SUBJECT_KEY_IDENTIFIER,
    _OID_AUTHORITY_KEY_IDENTIFIER,
    _OID_EXTENDED_KEY_USAGE,
    _OID_SUBJECT_ALT_NAME,
}
_OID_RSA_PUBLIC_KEY = "1.2.840.113549.1.1.1"
_OID_EC_PUBLIC_KEY = "1.2.840.10045.2.1"
_OID_P256 = "1.2.840.10045.3.1.7"
_OID_P384 = "1.3.132.0.34"
_SIGNATURE_ALGORITHMS = {
    "1.2.840.113549.1.1.11": "RSA",
    "1.2.840.113549.1.1.12": "RSA",
    "1.2.840.10045.4.3.2": "EC",
    "1.2.840.10045.4.3.3": "EC",
}


def _fail(code: str) -> None:
    raise PKIVerificationError(_ERRORS[code])


def _canonical(value: object, *, error_code: str) -> bytes:
    try:
        encoded = json.dumps(
            value,
            sort_keys=True,
            separators=(",", ":"),
            ensure_ascii=True,
            allow_nan=False,
        )
    except (TypeError, ValueError, OverflowError, RecursionError):
        _fail(error_code)
    return (encoded + "\n").encode("utf-8")


def canonical_bytes(value: object) -> bytes:
    return _canonical(value, error_code="evidence")


def _strict_json(raw: bytes, *, error_code: str, limit: int) -> object:
    if not isinstance(raw, bytes) or not raw or len(raw) > limit:
        _fail(error_code)

    def object_hook(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, item in pairs:
            if key in value:
                _fail(error_code)
            value[key] = item
        return value

    def reject_constant(_value: str) -> None:
        _fail(error_code)

    try:
        decoded = raw.decode("utf-8", errors="strict")
        value = json.loads(
            decoded,
            object_pairs_hook=object_hook,
            parse_constant=reject_constant,
        )
    except (
        UnicodeDecodeError,
        json.JSONDecodeError,
        TypeError,
        ValueError,
        OverflowError,
        RecursionError,
    ):
        _fail(error_code)
    if _canonical(value, error_code=error_code) != raw:
        _fail(error_code)
    return value


def _timestamp(value: object, *, error_code: str) -> datetime:
    if not isinstance(value, str) or _TIMESTAMP_RE.fullmatch(value) is None:
        _fail(error_code)
    try:
        parsed = datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(
            tzinfo=timezone.utc
        )
    except ValueError:
        _fail(error_code)
    if parsed.strftime("%Y-%m-%dT%H:%M:%SZ") != value:
        _fail(error_code)
    return parsed


def _timestamp_text(value: datetime) -> str:
    return value.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _member_name(value: object, expected: str | None = None) -> str:
    if (
        not isinstance(value, str)
        or _MEMBER_RE.fullmatch(value) is None
        or value in {".", "..", _PROFILE_NAME}
        or "/" in value
        or "\\" in value
    ):
        _fail("input")
    if expected is not None and value != expected:
        _fail("input")
    return value


def _sorted_strings(value: object, validator: Callable[[str], bool]) -> list[str]:
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        _fail("input")
    if value != sorted(value) or len(value) != len(set(value)):
        _fail("input")
    if not all(validator(item) for item in value):
        _fail("input")
    return value


def _valid_dns(value: str) -> bool:
    if not value or len(value) > 253 or value.endswith("."):
        return False
    try:
        value.encode("ascii")
    except UnicodeEncodeError:
        return False
    return all(
        _DNS_LABEL_RE.fullmatch(label) is not None for label in value.split(".")
    )


def _valid_ip(value: str) -> bool:
    try:
        return str(ipaddress.ip_address(value)) == value
    except ValueError:
        return False


def _valid_uri(value: str) -> bool:
    try:
        value.encode("ascii")
    except UnicodeEncodeError:
        return False
    return _URI_RE.fullmatch(value) is not None


def validate_profile(raw: bytes) -> dict[str, object]:
    value = _strict_json(raw, error_code="input", limit=_MAX_PROFILE_BYTES)
    if not isinstance(value, dict) or set(value) != _PROFILE_KEYS:
        _fail("input")
    if (
        value["schema"] != _PROFILE_SCHEMA
        or value["release_readiness"] != "NO_GO"
        or value["deployment_authorized"] is not False
    ):
        _fail("input")
    _timestamp(value["evaluation_time"], error_code="input")
    minimum = value["minimum_remaining_seconds"]
    if type(minimum) is not int or not 0 < minimum <= _MAX_POSITIVE_INTEGER:
        _fail("input")

    domains = value["trust_domains"]
    if not isinstance(domains, list):
        _fail("input")
    expected_domains = sorted(_ROLE_MATRIX)
    if [
        item.get("name") if isinstance(item, dict) else None for item in domains
    ] != expected_domains:
        _fail("input")

    names = {_PROFILE_NAME}
    for domain in domains:
        if not isinstance(domain, dict) or set(domain) != _DOMAIN_KEYS:
            _fail("input")
        domain_name = domain["name"]
        if domain_name not in _ROLE_MATRIX:
            _fail("input")
        ca_name = _member_name(
            domain["ca_certificate"], expected=domain_name + "-ca.pem"
        )
        if ca_name in names:
            _fail("input")
        names.add(ca_name)

        certificates = domain["certificates"]
        if not isinstance(certificates, list):
            _fail("input")
        expected_roles = sorted(_ROLE_MATRIX[domain_name])
        actual_roles = [
            entry.get("role") if isinstance(entry, dict) else None
            for entry in certificates
        ]
        if actual_roles != expected_roles:
            _fail("input")
        for entry in certificates:
            if not isinstance(entry, dict) or set(entry) != _CERTIFICATE_KEYS:
                _fail("input")
            role = entry["role"]
            expected_purpose, expected_eku = _ROLE_MATRIX[domain_name][role]
            if entry["purpose"] != expected_purpose:
                _fail("input")
            certificate_name = _member_name(
                entry["certificate"], expected=role + ".pem"
            )
            if certificate_name in names:
                _fail("input")
            names.add(certificate_name)
            dns_sans = _sorted_strings(entry["dns_sans"], _valid_dns)
            ip_sans = _sorted_strings(entry["ip_sans"], _valid_ip)
            uri_sans = _sorted_strings(entry["uri_sans"], _valid_uri)
            eku_oids = _sorted_strings(
                entry["eku_oids"],
                lambda item: item in {_SERVER_AUTH, _CLIENT_AUTH},
            )
            if tuple(eku_oids) != expected_eku or _ANY_EKU in eku_oids:
                _fail("input")
            if role in _FIXED_DNS_SANS and (
                tuple(dns_sans) != _FIXED_DNS_SANS[role]
                or ip_sans
                or uri_sans
            ):
                _fail("input")
    if len(names) != 45:
        _fail("input")
    return value


def example_profile() -> dict[str, object]:
    domains: list[dict[str, object]] = []
    for domain_name in sorted(_ROLE_MATRIX):
        certificates: list[dict[str, object]] = []
        for role in sorted(_ROLE_MATRIX[domain_name]):
            purpose, eku = _ROLE_MATRIX[domain_name][role]
            fixed_dns = _FIXED_DNS_SANS.get(role)
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
        "schema": _PROFILE_SCHEMA,
        "trust_domains": domains,
    }


def _valid_openssl_version(value: object) -> bool:
    return (
        isinstance(value, str)
        and _OPENSSL_VERSION_RE.fullmatch(value) is not None
    )


def _valid_serial_hex(value: object) -> bool:
    if not isinstance(value, str) or _SERIAL_HEX_RE.fullmatch(value) is None:
        return False
    value_octets = (len(value) + 1) // 2
    sign_pad_octets = int(
        len(value) % 2 == 0 and int(value[0], 16) >= 8
    )
    return value_octets + sign_pad_octets <= 20


def validate_evidence(raw: bytes) -> dict[str, object]:
    value = _strict_json(raw, error_code="evidence", limit=_MAX_PROFILE_BYTES)
    if not isinstance(value, dict) or set(value) != _EVIDENCE_KEYS:
        _fail("evidence")
    profile_digest = value["profile_sha256"]
    if (
        value["schema"] != _EVIDENCE_SCHEMA
        or value["release_readiness"] != "NO_GO"
        or value["deployment_authorized"] is not False
        or value["blockers"] != ["deployment-not-authorized"]
        or not isinstance(profile_digest, str)
        or _DIGEST_RE.fullmatch(profile_digest) is None
    ):
        _fail("evidence")
    if not _valid_openssl_version(value["openssl_version"]):
        _fail("evidence")
    evaluation = _timestamp(value["evaluation_time"], error_code="evidence")
    domains = value["trust_domains"]
    if not isinstance(domains, list):
        _fail("evidence")
    if [
        item.get("name") if isinstance(item, dict) else None for item in domains
    ] != sorted(_ROLE_MATRIX):
        _fail("evidence")

    fingerprints: set[str] = set()
    spki_digests: set[str] = set()
    for domain in domains:
        if not isinstance(domain, dict) or set(domain) != _EVIDENCE_DOMAIN_KEYS:
            _fail("evidence")
        domain_name = domain["name"]
        ca_fingerprint = domain["ca_fingerprint_sha256"]
        ca_spki = domain["ca_spki_sha256"]
        ca_serial = domain["ca_serial_hex"]
        if (
            not isinstance(ca_fingerprint, str)
            or _DIGEST_RE.fullmatch(ca_fingerprint) is None
            or ca_fingerprint in fingerprints
            or not isinstance(ca_spki, str)
            or _DIGEST_RE.fullmatch(ca_spki) is None
            or ca_spki in spki_digests
            or not _valid_serial_hex(ca_serial)
        ):
            _fail("evidence")
        fingerprints.add(ca_fingerprint)
        spki_digests.add(ca_spki)
        serials = {ca_serial}
        if _timestamp(domain["ca_not_after"], error_code="evidence") <= evaluation:
            _fail("evidence")
        certificates = domain["certificates"]
        if not isinstance(certificates, list):
            _fail("evidence")
        if [
            item.get("role") if isinstance(item, dict) else None
            for item in certificates
        ] != sorted(_ROLE_MATRIX[domain_name]):
            _fail("evidence")
        for certificate in certificates:
            if (
                not isinstance(certificate, dict)
                or set(certificate) != _EVIDENCE_CERTIFICATE_KEYS
            ):
                _fail("evidence")
            fingerprint = certificate["certificate_fingerprint_sha256"]
            spki = certificate["certificate_spki_sha256"]
            serial = certificate["certificate_serial_hex"]
            if (
                not isinstance(fingerprint, str)
                or _DIGEST_RE.fullmatch(fingerprint) is None
                or fingerprint in fingerprints
                or not isinstance(spki, str)
                or _DIGEST_RE.fullmatch(spki) is None
                or spki in spki_digests
                or not _valid_serial_hex(serial)
                or serial in serials
            ):
                _fail("evidence")
            fingerprints.add(fingerprint)
            spki_digests.add(spki)
            serials.add(serial)
            if (
                _timestamp(certificate["not_after"], error_code="evidence")
                <= evaluation
            ):
                _fail("evidence")
    if len(fingerprints) != 44 or len(spki_digests) != 44:
        _fail("evidence")
    return value

@dataclass(frozen=True)
class OpenSSLResult:
    returncode: int
    stdout: bytes
    stderr: bytes


class OpenSSLRunner(Protocol):
    def __call__(
        self,
        argv: tuple[str, ...],
        *,
        pass_fds: tuple[int, ...],
        timeout_seconds: int,
        output_limit: int,
    ) -> OpenSSLResult: ...


class _OutputLimitError(RuntimeError):
    pass


def _openssl_program() -> str:
    return "/usr/bin/openssl"


def _linux_runtime() -> bool:
    return (
        os.name == "posix"
        and sys.platform.startswith("linux")
        and os.path.isdir("/proc/self/fd")
    )


def _allowed_openssl_argv(
    argv: tuple[str, ...], pass_fds: tuple[int, ...]
) -> bool:
    if (
        not isinstance(argv, tuple)
        or not all(isinstance(item, str) for item in argv)
        or not isinstance(pass_fds, tuple)
        or not all(type(item) is int and item >= 3 for item in pass_fds)
        or tuple(sorted(set(pass_fds))) != pass_fds
    ):
        return False
    program = _openssl_program()
    if argv == (program, "version", "-v"):
        return pass_fds == ()
    if len(argv) != 18 or argv[0:2] != (program, "verify"):
        return False
    ca_match = re.fullmatch(r"/proc/self/fd/([1-9][0-9]*)", argv[3])
    leaf_match = re.fullmatch(r"/proc/self/fd/([1-9][0-9]*)", argv[17])
    if ca_match is None or leaf_match is None:
        return False
    ca_fd = int(ca_match.group(1))
    leaf_fd = int(leaf_match.group(1))
    if ca_fd < 3 or leaf_fd < 3 or ca_fd == leaf_fd:
        return False
    return (
        argv[2] == "-trusted"
        and argv[4:14]
        == (
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
        )
        and argv[14].isdigit()
        and argv[15] == "-purpose"
        and argv[16] in {"sslserver", "sslclient"}
        and pass_fds == tuple(sorted((ca_fd, leaf_fd)))
    )


def _stop_process(process: subprocess.Popen[bytes]) -> None:
    for _attempt in range(2):
        if process.poll() is not None:
            return
        try:
            process.kill()
        except OSError:
            pass
        try:
            process.wait(timeout=1)
            return
        except (subprocess.TimeoutExpired, OSError):
            continue


def default_openssl_runner(
    argv: tuple[str, ...],
    *,
    pass_fds: tuple[int, ...] = (),
    timeout_seconds: int = _OPENSSL_TIMEOUT_SECONDS,
    output_limit: int = _MAX_OPENSSL_OUTPUT,
) -> OpenSSLResult:
    if (
        not _linux_runtime()
        or not _allowed_openssl_argv(argv, pass_fds)
        or type(timeout_seconds) is not int
        or not 0 < timeout_seconds <= _OPENSSL_TIMEOUT_SECONDS
        or type(output_limit) is not int
        or not 0 < output_limit <= _MAX_OPENSSL_OUTPUT
    ):
        _fail("unsupported")
    environment = {
        "PATH": "/usr/bin:/bin",
        "LC_ALL": "C",
        "LANG": "C",
        "TZ": "UTC",
        "OPENSSL_CONF": os.devnull,
    }
    process = subprocess.Popen(
        list(argv),
        shell=False,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        close_fds=True,
        pass_fds=pass_fds,
        env=environment,
        bufsize=0,
    )
    if process.stdout is None or process.stderr is None:
        _stop_process(process)
        _fail("openssl")
    selector = selectors.DefaultSelector()
    streams = {
        "stdout": (process.stdout, bytearray()),
        "stderr": (process.stderr, bytearray()),
    }
    try:
        for name, (stream, _buffer) in streams.items():
            selector.register(stream, selectors.EVENT_READ, name)
        deadline = time.monotonic() + timeout_seconds
        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise subprocess.TimeoutExpired(argv, timeout_seconds)
            events = selector.select(remaining)
            if not events:
                raise subprocess.TimeoutExpired(argv, timeout_seconds)
            for key, _mask in events:
                stream, buffer = streams[key.data]
                chunk = os.read(
                    stream.fileno(),
                    min(65536, output_limit + 1 - len(buffer)),
                )
                if not chunk:
                    selector.unregister(stream)
                    continue
                buffer.extend(chunk)
                if len(buffer) > output_limit:
                    raise _OutputLimitError
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise subprocess.TimeoutExpired(argv, timeout_seconds)
        returncode = process.wait(timeout=remaining)
        return OpenSSLResult(
            returncode=returncode,
            stdout=bytes(streams["stdout"][1]),
            stderr=bytes(streams["stderr"][1]),
        )
    except BaseException:
        _stop_process(process)
        raise
    finally:
        selector.close()
        process.stdout.close()
        process.stderr.close()

@dataclass(frozen=True)
class _TLV:
    tag: int
    content: bytes
    encoded: bytes


class _DERError(ValueError):
    pass


def _read_tlv(data: bytes, offset: int) -> tuple[_TLV, int]:
    start = offset
    if offset >= len(data):
        raise _DERError
    tag = data[offset]
    offset += 1
    if tag & 0x1F == 0x1F or offset >= len(data):
        raise _DERError
    first_length = data[offset]
    offset += 1
    if first_length == 0x80:
        raise _DERError
    if first_length & 0x80:
        count = first_length & 0x7F
        if count == 0 or count > 4 or offset + count > len(data):
            raise _DERError
        encoded_length = data[offset : offset + count]
        if encoded_length[0] == 0:
            raise _DERError
        length = int.from_bytes(encoded_length, "big")
        if length < 128:
            raise _DERError
        offset += count
    else:
        length = first_length
    end = offset + length
    if end > len(data):
        raise _DERError
    return _TLV(tag, data[offset:end], data[start:end]), end


def _items(data: bytes) -> list[_TLV]:
    values: list[_TLV] = []
    offset = 0
    while offset < len(data):
        value, offset = _read_tlv(data, offset)
        values.append(value)
    if offset != len(data):
        raise _DERError
    return values


def _one(data: bytes, tag: int) -> _TLV:
    value, offset = _read_tlv(data, 0)
    if offset != len(data) or value.tag != tag:
        raise _DERError
    return value


def _oid(value: _TLV) -> str:
    if value.tag != 0x06 or not value.content:
        raise _DERError
    subidentifiers: list[int] = []
    current = 0
    starting = True
    for byte in value.content:
        if starting and byte == 0x80:
            raise _DERError
        starting = False
        current = (current << 7) | (byte & 0x7F)
        if current > 2**63 - 1:
            raise _DERError
        if not byte & 0x80:
            subidentifiers.append(current)
            current = 0
            starting = True
    if not starting or not subidentifiers:
        raise _DERError
    first_value = subidentifiers[0]
    if first_value < 40:
        first, second = 0, first_value
    elif first_value < 80:
        first, second = 1, first_value - 40
    else:
        first, second = 2, first_value - 80
    return ".".join(
        str(item) for item in (first, second, *subidentifiers[1:])
    )


def _positive_integer(
    value: _TLV, *, max_content: int | None = None
) -> int:
    content = value.content
    if value.tag != 0x02 or not content or content[0] & 0x80:
        raise _DERError
    if len(content) > 1 and content[0] == 0 and not content[1] & 0x80:
        raise _DERError
    if max_content is not None and len(content) > max_content:
        raise _DERError
    result = int.from_bytes(content, "big")
    if result <= 0:
        raise _DERError
    return result


def _nonnegative_integer(value: _TLV) -> int:
    if (
        value.tag != 0x02
        or not value.content
        or value.content[0] & 0x80
        or (
            len(value.content) > 1
            and value.content[0] == 0
            and not value.content[1] & 0x80
        )
    ):
        raise _DERError
    return int.from_bytes(value.content, "big")


def _boolean(value: _TLV) -> bool:
    if (
        value.tag != 0x01
        or len(value.content) != 1
        or value.content[0] not in {0, 0xFF}
    ):
        raise _DERError
    return value.content[0] == 0xFF


def _bit_indices(value: _TLV) -> frozenset[int]:
    if value.tag != 0x03 or len(value.content) < 2:
        raise _DERError
    unused = value.content[0]
    payload = value.content[1:]
    if unused > 7 or payload[-1] & ((1 << unused) - 1):
        raise _DERError
    bit_count = len(payload) * 8 - unused
    indices = {
        index
        for index in range(bit_count)
        if payload[index // 8] & (0x80 >> (index % 8))
    }
    if not indices or max(indices) != bit_count - 1:
        raise _DERError
    return frozenset(indices)


def _algorithm(value: _TLV, *, kind: str) -> tuple[str, bytes | None]:
    if value.tag != 0x30:
        raise _DERError
    values = _items(value.content)
    if len(values) not in {1, 2}:
        raise _DERError
    algorithm_oid = _oid(values[0])
    parameters = values[1].encoded if len(values) == 2 else None
    if kind == "signature":
        if algorithm_oid not in _SIGNATURE_ALGORITHMS:
            raise _DERError
        if _SIGNATURE_ALGORITHMS[algorithm_oid] == "RSA":
            if parameters != b"\x05\x00":
                raise _DERError
        elif parameters is not None:
            raise _DERError
    return algorithm_oid, parameters


def _asn1_time(value: _TLV) -> datetime:
    try:
        text = value.content.decode("ascii")
        if value.tag == 0x17 and re.fullmatch(r"[0-9]{12}Z", text):
            year = int(text[:2])
            year += 2000 if year < 50 else 1900
            expanded = f"{year:04d}" + text[2:]
        elif value.tag == 0x18 and re.fullmatch(r"[0-9]{14}Z", text):
            expanded = text
        else:
            raise _DERError
        result = datetime.strptime(expanded, "%Y%m%d%H%M%SZ").replace(
            tzinfo=timezone.utc
        )
    except (UnicodeDecodeError, ValueError):
        raise _DERError
    if (
        1950 <= result.year <= 2049
        and value.tag != 0x17
    ) or (
        not 1950 <= result.year <= 2049
        and value.tag != 0x18
    ):
        raise _DERError
    return result


@dataclass(frozen=True)
class _Extension:
    critical: bool
    value: bytes


@dataclass(frozen=True)
class _CertificateFacts:
    fingerprint: str
    serial_hex: str
    spki_sha256: str
    issuer: bytes
    subject: bytes
    not_before: datetime
    not_after: datetime
    signature_oid: str
    key_algorithm: str
    key_bits: int
    is_ca: bool
    path_length: int | None
    key_usage: frozenset[int]
    ski: bytes
    aki: bytes
    eku_oids: tuple[str, ...] | None
    dns_sans: tuple[str, ...]
    ip_sans: tuple[str, ...]
    uri_sans: tuple[str, ...]

def _extensions(value: _TLV) -> dict[str, _Extension]:
    if value.tag != 0xA3:
        raise _DERError
    sequence = _one(value.content, 0x30)
    result: dict[str, _Extension] = {}
    for item in _items(sequence.content):
        if item.tag != 0x30:
            raise _DERError
        parts = _items(item.content)
        if len(parts) not in {2, 3}:
            raise _DERError
        extension_oid = _oid(parts[0])
        index = 1
        critical = False
        if len(parts) == 3:
            critical = _boolean(parts[1])
            if not critical:
                raise _DERError
            index = 2
        if parts[index].tag != 0x04 or extension_oid in result:
            raise _DERError
        if critical and extension_oid not in _KNOWN_CRITICAL_EXTENSIONS:
            raise _DERError
        result[extension_oid] = _Extension(critical, parts[index].content)
    return result


def _basic_constraints(
    extension: _Extension,
) -> tuple[bool, int | None]:
    values = _items(_one(extension.value, 0x30).content)
    if not values:
        return False, None
    if values[0].tag != 0x01 or not _boolean(values[0]):
        raise _DERError
    if len(values) == 1:
        return True, None
    if len(values) != 2:
        raise _DERError
    return True, _nonnegative_integer(values[1])


def _key_identifier(
    extension: _Extension, *, authority: bool
) -> bytes:
    if authority:
        values = _items(_one(extension.value, 0x30).content)
        if len(values) != 1 or values[0].tag != 0x80:
            raise _DERError
        identifier = values[0].content
    else:
        identifier = _one(extension.value, 0x04).content
    if not 16 <= len(identifier) <= 64:
        raise _DERError
    return identifier


def _eku(extension: _Extension) -> tuple[str, ...]:
    values = tuple(
        _oid(item) for item in _items(_one(extension.value, 0x30).content)
    )
    if (
        not values
        or len(values) != len(set(values))
        or tuple(sorted(values)) != values
    ):
        raise _DERError
    return values


def _sans(
    extension: _Extension | None,
) -> tuple[tuple[str, ...], tuple[str, ...], tuple[str, ...]]:
    if extension is None:
        return (), (), ()
    dns: list[str] = []
    ips: list[str] = []
    uris: list[str] = []
    values = _items(_one(extension.value, 0x30).content)
    if not values:
        raise _DERError
    for item in values:
        try:
            if item.tag == 0x82:
                text = item.content.decode("ascii")
                if not _valid_dns(text):
                    raise _DERError
                dns.append(text)
            elif item.tag == 0x87:
                ips.append(str(ipaddress.ip_address(item.content)))
            elif item.tag == 0x86:
                text = item.content.decode("ascii")
                if not _valid_uri(text):
                    raise _DERError
                uris.append(text)
            else:
                raise _DERError
        except (UnicodeDecodeError, ValueError):
            raise _DERError
    if (
        len(dns) != len(set(dns))
        or len(ips) != len(set(ips))
        or len(uris) != len(set(uris))
    ):
        raise _DERError
    return tuple(sorted(dns)), tuple(sorted(ips)), tuple(sorted(uris))


def _public_key(value: _TLV) -> tuple[str, int]:
    if value.tag != 0x30:
        raise _DERError
    parts = _items(value.content)
    if len(parts) != 2 or parts[0].tag != 0x30 or parts[1].tag != 0x03:
        raise _DERError
    algorithm_parts = _items(parts[0].content)
    if len(algorithm_parts) not in {1, 2}:
        raise _DERError
    algorithm_oid = _oid(algorithm_parts[0])
    bit_string = parts[1].content
    if len(bit_string) < 2 or bit_string[0] != 0:
        raise _DERError
    key_data = bit_string[1:]
    if algorithm_oid == _OID_RSA_PUBLIC_KEY:
        if (
            len(algorithm_parts) != 2
            or algorithm_parts[1].encoded != b"\x05\x00"
        ):
            raise _DERError
        rsa_parts = _items(_one(key_data, 0x30).content)
        if len(rsa_parts) != 2:
            raise _DERError
        modulus = _positive_integer(rsa_parts[0])
        exponent = _positive_integer(rsa_parts[1])
        bits = modulus.bit_length()
        if bits not in {3072, 4096} or exponent != 65537:
            raise _DERError
        return "RSA", bits
    if algorithm_oid == _OID_EC_PUBLIC_KEY:
        if len(algorithm_parts) != 2 or algorithm_parts[1].tag != 0x06:
            raise _DERError
        curve = _oid(algorithm_parts[1])
        expected = {
            _OID_P256: (256, 65),
            _OID_P384: (384, 97),
        }.get(curve)
        if (
            expected is None
            or len(key_data) != expected[1]
            or key_data[0] != 0x04
        ):
            raise _DERError
        return "EC", expected[0]
    raise _DERError


def _parse_certificate(der: bytes) -> _CertificateFacts:
    if not isinstance(der, bytes) or not der or len(der) > _MAX_DER_BYTES:
        raise _DERError
    certificate = _one(der, 0x30)
    outer = _items(certificate.content)
    if (
        len(outer) != 3
        or outer[0].tag != 0x30
        or outer[1].tag != 0x30
        or outer[2].tag != 0x03
    ):
        raise _DERError
    if len(outer[2].content) < 2 or outer[2].content[0] != 0:
        raise _DERError
    signature_oid, _ = _algorithm(outer[1], kind="signature")

    tbs = _items(outer[0].content)
    if len(tbs) < 8 or tbs[0].tag != 0xA0:
        raise _DERError
    version = _one(tbs[0].content, 0x02)
    if _nonnegative_integer(version) != 2:
        raise _DERError
    serial = _positive_integer(tbs[1], max_content=20)
    tbs_signature_oid, _ = _algorithm(tbs[2], kind="signature")
    if (
        tbs[2].encoded != outer[1].encoded
        or tbs_signature_oid != signature_oid
    ):
        raise _DERError
    if tbs[3].tag != 0x30 or not tbs[3].content:
        raise _DERError
    issuer = tbs[3].encoded
    if tbs[4].tag != 0x30:
        raise _DERError
    validity = _items(tbs[4].content)
    if len(validity) != 2:
        raise _DERError
    not_before = _asn1_time(validity[0])
    not_after = _asn1_time(validity[1])
    if not not_before < not_after:
        raise _DERError
    if tbs[5].tag != 0x30 or not tbs[5].content:
        raise _DERError
    subject = tbs[5].encoded
    key_algorithm, key_bits = _public_key(tbs[6])
    spki_sha256 = hashlib.sha256(tbs[6].encoded).hexdigest()
    if len(tbs) != 8 or tbs[7].tag != 0xA3:
        raise _DERError
    extensions = _extensions(tbs[7])
    required = {
        _OID_BASIC_CONSTRAINTS,
        _OID_KEY_USAGE,
        _OID_SUBJECT_KEY_IDENTIFIER,
        _OID_AUTHORITY_KEY_IDENTIFIER,
    }
    if not required.issubset(extensions):
        raise _DERError

    basic = extensions[_OID_BASIC_CONSTRAINTS]
    key_usage = extensions[_OID_KEY_USAGE]
    ski_extension = extensions[_OID_SUBJECT_KEY_IDENTIFIER]
    aki_extension = extensions[_OID_AUTHORITY_KEY_IDENTIFIER]
    if (
        not basic.critical
        or not key_usage.critical
        or ski_extension.critical
        or aki_extension.critical
    ):
        raise _DERError
    is_ca, path_length = _basic_constraints(basic)
    usage = _bit_indices(_one(key_usage.value, 0x03))
    ski = _key_identifier(ski_extension, authority=False)
    aki = _key_identifier(aki_extension, authority=True)

    eku_extension = extensions.get(_OID_EXTENDED_KEY_USAGE)
    if eku_extension is not None and eku_extension.critical:
        raise _DERError
    eku_oids = _eku(eku_extension) if eku_extension is not None else None
    san_extension = extensions.get(_OID_SUBJECT_ALT_NAME)
    if san_extension is not None and san_extension.critical:
        raise _DERError
    dns_sans, ip_sans, uri_sans = _sans(san_extension)

    return _CertificateFacts(
        fingerprint=hashlib.sha256(der).hexdigest(),
        serial_hex=format(serial, "x"),
        spki_sha256=spki_sha256,
        issuer=issuer,
        subject=subject,
        not_before=not_before,
        not_after=not_after,
        signature_oid=signature_oid,
        key_algorithm=key_algorithm,
        key_bits=key_bits,
        is_ca=is_ca,
        path_length=path_length,
        key_usage=usage,
        ski=ski,
        aki=aki,
        eku_oids=eku_oids,
        dns_sans=dns_sans,
        ip_sans=ip_sans,
        uri_sans=uri_sans,
    )


def _decode_certificate(raw: bytes) -> bytes:
    if any(marker in raw for marker in _PRIVATE_MARKERS):
        _fail("members")
    try:
        lines = raw.decode("ascii").splitlines()
    except UnicodeDecodeError:
        _fail("certificate")
    if (
        len(lines) < 3
        or lines[0] != "-----BEGIN CERTIFICATE-----"
        or lines[-1] != "-----END CERTIFICATE-----"
        or any(not line or len(line) > 76 for line in lines[1:-1])
    ):
        _fail("certificate")
    try:
        encoded = "".join(lines[1:-1]).encode("ascii")
        der = base64.b64decode(encoded, validate=True)
    except (UnicodeEncodeError, ValueError):
        _fail("certificate")
    if not der or len(der) > _MAX_DER_BYTES:
        _fail("certificate")
    return der


def _validate_time(
    facts: _CertificateFacts,
    evaluation: datetime,
    minimum_remaining_seconds: int,
) -> None:
    if (
        facts.not_before > evaluation
        or evaluation >= facts.not_after
        or (
            facts.not_after - evaluation
        ).total_seconds() < minimum_remaining_seconds
    ):
        _fail("certificate")


def _validate_root(
    facts: _CertificateFacts,
    evaluation: datetime,
    minimum_remaining_seconds: int,
) -> None:
    _validate_time(facts, evaluation, minimum_remaining_seconds)
    if (
        facts.issuer != facts.subject
        or not facts.is_ca
        or facts.path_length != 0
        or facts.key_usage != frozenset({5, 6})
        or facts.ski != facts.aki
        or facts.eku_oids is not None
        or facts.dns_sans
        or facts.ip_sans
        or facts.uri_sans
        or _SIGNATURE_ALGORITHMS[facts.signature_oid]
        != facts.key_algorithm
    ):
        _fail("certificate")


def _validate_leaf(
    facts: _CertificateFacts,
    root: _CertificateFacts,
    entry: dict[str, object],
    evaluation: datetime,
    minimum_remaining_seconds: int,
) -> None:
    _validate_time(facts, evaluation, minimum_remaining_seconds)
    if (
        facts.issuer != root.subject
        or facts.is_ca
        or facts.path_length is not None
        or facts.key_usage != frozenset({0})
        or facts.aki != root.ski
        or facts.eku_oids != tuple(entry["eku_oids"])
        or _ANY_EKU in (facts.eku_oids or ())
        or facts.dns_sans != tuple(entry["dns_sans"])
        or facts.ip_sans != tuple(entry["ip_sans"])
        or facts.uri_sans != tuple(entry["uri_sans"])
        or _SIGNATURE_ALGORITHMS[facts.signature_oid]
        != root.key_algorithm
    ):
        _fail("certificate")

_Fingerprint = tuple[int, int, int, int, int, int, int]


def _stat_fingerprint(value: os.stat_result) -> _Fingerprint:
    return (
        value.st_dev,
        value.st_ino,
        value.st_mode,
        value.st_nlink,
        value.st_size,
        int(getattr(value, "st_mtime_ns", value.st_mtime * 1_000_000_000)),
        int(getattr(value, "st_ctime_ns", value.st_ctime * 1_000_000_000)),
    )


@dataclass
class _PinnedMember:
    name: str
    descriptor: int
    metadata: os.stat_result
    data: bytes

    def close(self) -> None:
        if self.descriptor >= 0:
            os.close(self.descriptor)
            self.descriptor = -1


@dataclass
class _PinnedRoot:
    path: Path
    descriptor: int | None
    metadata: os.stat_result

    def close(self) -> None:
        if self.descriptor is not None:
            os.close(self.descriptor)
            self.descriptor = None


def _coerce_root(value: Path | str) -> Path:
    try:
        raw = os.fspath(value)
        if not isinstance(raw, str) or not raw or "\x00" in raw:
            _fail("input")
        return Path(os.path.abspath(raw))
    except (OSError, TypeError, ValueError):
        _fail("input")


def _open_root(value: Path | str) -> _PinnedRoot:
    path = _coerce_root(value)
    try:
        metadata = os.lstat(path)
    except OSError:
        _fail("input")
    if stat.S_ISLNK(metadata.st_mode) or not stat.S_ISDIR(metadata.st_mode):
        _fail("input")
    if os.name == "posix" and (
        stat.S_IMODE(metadata.st_mode) != 0o700
        or metadata.st_uid != os.geteuid()
    ):
        _fail("input")
    descriptor: int | None = None
    if hasattr(os, "O_DIRECTORY"):
        flags = os.O_RDONLY | int(getattr(os, "O_DIRECTORY", 0))
        flags |= int(getattr(os, "O_NOFOLLOW", 0))
        flags |= int(getattr(os, "O_CLOEXEC", 0))
        try:
            descriptor = os.open(path, flags)
            opened = os.fstat(descriptor)
        except OSError:
            if descriptor is not None:
                os.close(descriptor)
            _fail("input")
        if _stat_fingerprint(opened)[:3] != _stat_fingerprint(metadata)[:3]:
            os.close(descriptor)
            _fail("input")
        metadata = opened
    return _PinnedRoot(path, descriptor, metadata)


def _root_members(root: _PinnedRoot) -> list[str]:
    try:
        values = (
            os.listdir(root.descriptor)
            if (
                root.descriptor is not None
                and os.listdir in getattr(os, "supports_fd", set())
            )
            else os.listdir(root.path)
        )
    except OSError:
        _fail("members")
    if not all(isinstance(item, str) for item in values):
        _fail("members")
    return values


def _ensure_root_stable(root: _PinnedRoot) -> None:
    try:
        current = os.lstat(root.path)
        if root.descriptor is not None:
            opened = os.fstat(root.descriptor)
            if (
                _stat_fingerprint(opened)[:3]
                != _stat_fingerprint(root.metadata)[:3]
            ):
                _fail("members")
    except OSError:
        _fail("members")
    if (
        stat.S_ISLNK(current.st_mode)
        or not stat.S_ISDIR(current.st_mode)
        or _stat_fingerprint(current)[:3]
        != _stat_fingerprint(root.metadata)[:3]
    ):
        _fail("members")


def _member_stat(root: _PinnedRoot, name: str) -> os.stat_result:
    try:
        if (
            root.descriptor is not None
            and os.stat in getattr(os, "supports_dir_fd", set())
            and os.stat in getattr(os, "supports_follow_symlinks", set())
        ):
            return os.stat(
                name,
                dir_fd=root.descriptor,
                follow_symlinks=False,
            )
        return os.lstat(root.path / name)
    except OSError:
        _fail("members")


def _open_member(
    root: _PinnedRoot,
    name: str,
    limit: int,
) -> _PinnedMember:
    before = _member_stat(root, name)
    if (
        stat.S_ISLNK(before.st_mode)
        or not stat.S_ISREG(before.st_mode)
        or before.st_nlink != 1
        or not 0 < before.st_size <= limit
    ):
        _fail("members")
    if os.name == "posix" and (
        before.st_uid != os.geteuid()
        or stat.S_IMODE(before.st_mode) & 0o133
    ):
        _fail("members")
    flags = os.O_RDONLY | int(getattr(os, "O_NOFOLLOW", 0))
    flags |= int(getattr(os, "O_CLOEXEC", 0))
    flags |= int(getattr(os, "O_BINARY", 0))
    descriptor: int | None = None
    try:
        if (
            root.descriptor is not None
            and os.open in getattr(os, "supports_dir_fd", set())
        ):
            descriptor = os.open(name, flags, dir_fd=root.descriptor)
        else:
            descriptor = os.open(root.path / name, flags)
        opened = os.fstat(descriptor)
        if _stat_fingerprint(opened) != _stat_fingerprint(before):
            os.close(descriptor)
            _fail("members")
        chunks: list[bytes] = []
        remaining = limit + 1
        while remaining:
            chunk = os.read(descriptor, min(65536, remaining))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        data = b"".join(chunks)
        after = os.fstat(descriptor)
        if (
            len(data) != before.st_size
            or len(data) > limit
            or _stat_fingerprint(after) != _stat_fingerprint(opened)
        ):
            os.close(descriptor)
            _fail("members")
        os.lseek(descriptor, 0, os.SEEK_SET)
        return _PinnedMember(name, descriptor, opened, data)
    except PKIVerificationError:
        raise
    except OSError:
        if descriptor is not None:
            os.close(descriptor)
        _fail("members")


def _ensure_member_stable(member: _PinnedMember) -> None:
    try:
        current = os.fstat(member.descriptor)
    except OSError:
        _fail("members")
    if _stat_fingerprint(current) != _stat_fingerprint(member.metadata):
        _fail("members")


def _invoke_openssl(
    runner: OpenSSLRunner,
    argv: tuple[str, ...],
    pass_fds: tuple[int, ...],
) -> OpenSSLResult:
    try:
        result = runner(
            argv,
            pass_fds=pass_fds,
            timeout_seconds=_OPENSSL_TIMEOUT_SECONDS,
            output_limit=_MAX_OPENSSL_OUTPUT,
        )
    except subprocess.TimeoutExpired:
        _fail("timeout")
    except FileNotFoundError:
        _fail("unsupported")
    except _OutputLimitError:
        _fail("output")
    except PKIVerificationError:
        raise
    except (OSError, TypeError, ValueError):
        _fail("openssl")
    if (
        not isinstance(result, OpenSSLResult)
        or type(result.returncode) is not int
        or not isinstance(result.stdout, bytes)
        or not isinstance(result.stderr, bytes)
        or len(result.stdout) > _MAX_OPENSSL_OUTPUT
        or len(result.stderr) > _MAX_OPENSSL_OUTPUT
    ):
        _fail("output")
    return result


def _openssl_version(runner: OpenSSLRunner) -> str:
    result = _invoke_openssl(
        runner,
        (_openssl_program(), "version", "-v"),
        (),
    )
    if result.returncode != 0:
        _fail("unsupported")
    if result.stderr:
        _fail("output")
    try:
        text = result.stdout.decode("ascii")
    except UnicodeDecodeError:
        _fail("output")
    if text.endswith("\r\n"):
        text = text[:-2]
    elif text.endswith("\n"):
        text = text[:-1]
    if (
        "\r" in text
        or "\n" in text
        or not _valid_openssl_version(text)
    ):
        _fail("unsupported")
    return text


def _descriptor_reference(
    root: _PinnedRoot,
    member: _PinnedMember,
) -> tuple[str, tuple[int, ...]]:
    del root
    if not _linux_runtime():
        _fail("unsupported")
    reference = f"/proc/self/fd/{member.descriptor}"
    if not os.path.exists(reference):
        _fail("openssl")
    return reference, (member.descriptor,)


def _verify_leaf_chain(
    runner: OpenSSLRunner,
    root: _PinnedRoot,
    ca_member: _PinnedMember,
    leaf_member: _PinnedMember,
    evaluation_epoch: int,
    purpose: str,
) -> None:
    ca_reference, ca_fds = _descriptor_reference(root, ca_member)
    leaf_reference, leaf_fds = _descriptor_reference(root, leaf_member)
    pass_fds = tuple(sorted(set(ca_fds + leaf_fds)))
    argv = (
        _openssl_program(),
        "verify",
        "-trusted",
        ca_reference,
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
        str(evaluation_epoch),
        "-purpose",
        purpose,
        leaf_reference,
    )
    result = _invoke_openssl(runner, argv, pass_fds)
    if result.returncode != 0:
        _fail("openssl")
    expected = (leaf_reference + ": OK\n").encode("ascii")
    if (
        result.stderr
        or result.stdout.replace(b"\r\n", b"\n") != expected
    ):
        _fail("output")

def verify_directory(
    root_value: Path | str,
    *,
    runner: OpenSSLRunner = default_openssl_runner,
) -> dict[str, object]:
    root = _open_root(root_value)
    members: list[_PinnedMember] = []
    try:
        initial_names = _root_members(root)
        if _PROFILE_NAME not in initial_names or len(initial_names) > 45:
            _fail("members")
        profile_member = _open_member(
            root,
            _PROFILE_NAME,
            _MAX_PROFILE_BYTES,
        )
        members.append(profile_member)
        profile = validate_profile(profile_member.data)

        expected_names = {_PROFILE_NAME}
        for domain in profile["trust_domains"]:
            expected_names.add(domain["ca_certificate"])
            expected_names.update(
                entry["certificate"]
                for entry in domain["certificates"]
            )
        if (
            len(initial_names) != len(expected_names)
            or set(initial_names) != expected_names
        ):
            _fail("members")

        by_name = {_PROFILE_NAME: profile_member}
        for name in sorted(expected_names - {_PROFILE_NAME}):
            member = _open_member(
                root,
                name,
                _MAX_CERTIFICATE_BYTES,
            )
            members.append(member)
            by_name[name] = member
        if any(
            marker in member.data
            for member in members
            for marker in _PRIVATE_MARKERS
        ):
            _fail("members")
        _ensure_root_stable(root)

        evaluation = _timestamp(
            profile["evaluation_time"],
            error_code="input",
        )
        minimum = profile["minimum_remaining_seconds"]
        facts_by_name: dict[str, _CertificateFacts] = {}
        try:
            for name in sorted(expected_names - {_PROFILE_NAME}):
                facts_by_name[name] = _parse_certificate(
                    _decode_certificate(by_name[name].data)
                )
        except _DERError:
            _fail("certificate")

        fingerprints: set[str] = set()
        spki_digests: set[str] = set()
        domain_facts: list[
            tuple[
                dict[str, object],
                _CertificateFacts,
                list[tuple[dict[str, object], _CertificateFacts]],
            ]
        ] = []
        for domain in profile["trust_domains"]:
            root_facts = facts_by_name[domain["ca_certificate"]]
            _validate_root(root_facts, evaluation, minimum)
            if (
                root_facts.fingerprint in fingerprints
                or root_facts.spki_sha256 in spki_digests
            ):
                _fail("certificate")
            fingerprints.add(root_facts.fingerprint)
            spki_digests.add(root_facts.spki_sha256)
            serials = {root_facts.serial_hex}
            leaf_facts: list[
                tuple[dict[str, object], _CertificateFacts]
            ] = []
            for entry in domain["certificates"]:
                facts = facts_by_name[entry["certificate"]]
                _validate_leaf(
                    facts,
                    root_facts,
                    entry,
                    evaluation,
                    minimum,
                )
                if (
                    facts.fingerprint in fingerprints
                    or facts.spki_sha256 in spki_digests
                    or facts.serial_hex in serials
                ):
                    _fail("certificate")
                fingerprints.add(facts.fingerprint)
                spki_digests.add(facts.spki_sha256)
                serials.add(facts.serial_hex)
                leaf_facts.append((entry, facts))
            domain_facts.append((domain, root_facts, leaf_facts))
        if len(fingerprints) != 44 or len(spki_digests) != 44:
            _fail("certificate")
        if not _linux_runtime():
            _fail("unsupported")

        openssl_version = _openssl_version(runner)
        evaluation_epoch = int(evaluation.timestamp())
        for domain, _root_facts, leaf_facts in domain_facts:
            ca_member = by_name[domain["ca_certificate"]]
            for entry, _facts in leaf_facts:
                leaf_member = by_name[entry["certificate"]]
                if entry["purpose"] == "peer":
                    purposes = ("sslserver", "sslclient")
                elif entry["purpose"] == "server":
                    purposes = ("sslserver",)
                else:
                    purposes = ("sslclient",)
                for purpose in purposes:
                    _verify_leaf_chain(
                        runner,
                        root,
                        ca_member,
                        leaf_member,
                        evaluation_epoch,
                        purpose,
                    )

        for member in members:
            _ensure_member_stable(member)
        _ensure_root_stable(root)
        if set(_root_members(root)) != expected_names:
            _fail("members")

        evidence_domains: list[dict[str, object]] = []
        for domain, root_facts, leaf_facts in domain_facts:
            evidence_domains.append(
                {
                    "ca_fingerprint_sha256": root_facts.fingerprint,
                    "ca_not_after": _timestamp_text(
                        root_facts.not_after
                    ),
                    "ca_serial_hex": root_facts.serial_hex,
                    "ca_spki_sha256": root_facts.spki_sha256,
                    "certificates": [
                        {
                            "certificate_fingerprint_sha256": (
                                facts.fingerprint
                            ),
                            "certificate_serial_hex": facts.serial_hex,
                            "certificate_spki_sha256": facts.spki_sha256,
                            "not_after": _timestamp_text(
                                facts.not_after
                            ),
                            "role": entry["role"],
                        }
                        for entry, facts in leaf_facts
                    ],
                    "name": domain["name"],
                }
            )
        evidence = {
            "blockers": ["deployment-not-authorized"],
            "deployment_authorized": False,
            "evaluation_time": profile["evaluation_time"],
            "openssl_version": openssl_version,
            "profile_sha256": hashlib.sha256(
                profile_member.data
            ).hexdigest(),
            "release_readiness": "NO_GO",
            "schema": _EVIDENCE_SCHEMA,
            "trust_domains": evidence_domains,
        }
        validate_evidence(canonical_bytes(evidence))
        return evidence
    finally:
        for member in reversed(members):
            member.close()
        root.close()


def main(argv: Sequence[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    if arguments == ["--help"]:
        print("usage: pki-verify.py --root DIRECTORY")
        return 0
    try:
        if len(arguments) != 2 or arguments[0] != "--root":
            _fail("input")
        evidence = verify_directory(arguments[1])
        sys.stdout.buffer.write(canonical_bytes(evidence))
        return 0
    except PKIVerificationError as error:
        print(str(error), file=sys.stderr)
        return 2
    except Exception:
        print(_ERRORS["input"], file=sys.stderr)
        return 2


__all__ = [
    "OpenSSLResult",
    "PKIVerificationError",
    "canonical_bytes",
    "default_openssl_runner",
    "example_profile",
    "main",
    "validate_evidence",
    "validate_profile",
    "verify_directory",
]


if __name__ == "__main__":
    raise SystemExit(main())
