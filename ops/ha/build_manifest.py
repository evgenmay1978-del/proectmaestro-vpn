"""Build and verify immutable MaestroVPN panel artifact manifests."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
import sys
from typing import Any, Sequence


class ManifestError(ValueError):
    """A fixed, redacted build-manifest contract failure."""


_ERRORS = {
    "input": "build-manifest:invalid-input",
    "members": "build-manifest:invalid-members",
    "artifact": "build-manifest:invalid-artifact",
    "manifest": "build-manifest:invalid-manifest",
    "identity": "build-manifest:identity-mismatch",
    "exists": "build-manifest:output-exists",
    "output": "build-manifest:output-failed",
}

_SCHEMA = "maestro-ha-build-manifest-v1"
_ARTIFACT_NAME = "maestro-panel"
_MANIFEST_NAME = "manifest.json"
_MAX_ARTIFACT_BYTES = 268435456
_MAX_MANIFEST_BYTES = 16384
_MAX_RUN_VALUE = 2**63 - 1
_READ_CHUNK = 1024 * 1024
_TOP_LEVEL_KEYS = {
    "artifacts",
    "commit_sha",
    "deployment_authorized",
    "go_version",
    "ref",
    "release_readiness",
    "repository",
    "schema",
    "workflow_run_attempt",
    "workflow_run_id",
}
_ARTIFACT_KEYS = {"arch", "name", "os", "path", "sha256", "size_bytes"}
_COMMIT_RE = re.compile(r"[0-9a-f]{40}\Z")
_DIGEST_RE = re.compile(r"[0-9a-f]{64}\Z")
_REPOSITORY_PART_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,99}\Z")
_GO_VERSION_RE = re.compile(r"go1\.25\.0\Z")
_REF_BODY_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._/-]{0,239}\Z")
_PULL_REF_RE = re.compile(r"refs/pull/[1-9][0-9]{0,9}/(?:head|merge)\Z")


def _fail(code: str) -> None:
    raise ManifestError(_ERRORS[code])


def _canonical(value: object) -> bytes:
    try:
        encoded = json.dumps(
            value,
            sort_keys=True,
            separators=(",", ":"),
            ensure_ascii=True,
            allow_nan=False,
        )
    except (TypeError, ValueError):
        _fail("manifest")
    return (encoded + "\n").encode("utf-8")


def _strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            _fail("manifest")
        value[key] = item
    return value


def _reject_json_constant(_value: str) -> None:
    _fail("manifest")


def _is_positive_integer(value: object) -> bool:
    return type(value) is int and 0 < value <= _MAX_RUN_VALUE


def _valid_repository(value: object) -> bool:
    if not isinstance(value, str) or len(value) > 200:
        return False
    try:
        value.encode("ascii")
    except UnicodeEncodeError:
        return False
    parts = value.split("/")
    return (
        len(parts) == 2
        and all(part not in {".", ".."} for part in parts)
        and all(_REPOSITORY_PART_RE.fullmatch(part) is not None for part in parts)
    )


def _valid_ref(value: object) -> bool:
    if not isinstance(value, str) or len(value) > 255:
        return False
    try:
        value.encode("ascii")
    except UnicodeEncodeError:
        return False
    if _PULL_REF_RE.fullmatch(value) is not None:
        return True
    prefix = next((item for item in ("refs/heads/", "refs/tags/") if value.startswith(item)), None)
    if prefix is None:
        return False
    body = value[len(prefix) :]
    if _REF_BODY_RE.fullmatch(body) is None:
        return False
    parts = body.split("/")
    return all(part not in {"", ".", ".."} for part in parts)


def _valid_commit(value: object) -> bool:
    return isinstance(value, str) and _COMMIT_RE.fullmatch(value) is not None


def _valid_go_version(value: object) -> bool:
    return isinstance(value, str) and _GO_VERSION_RE.fullmatch(value) is not None


def _validate_build_identity(
    repository: object,
    ref: object,
    commit_sha: object,
    workflow_run_id: object,
    workflow_run_attempt: object,
    go_version: object,
) -> None:
    if not (
        _valid_repository(repository)
        and _valid_ref(ref)
        and _valid_commit(commit_sha)
        and _is_positive_integer(workflow_run_id)
        and _is_positive_integer(workflow_run_attempt)
        and _valid_go_version(go_version)
    ):
        _fail("input")


def _validate_expected_identity(
    repository: object,
    ref: object,
    commit_sha: object,
    workflow_run_id: object,
    workflow_run_attempt: object,
) -> None:
    if not (
        _valid_repository(repository)
        and _valid_ref(ref)
        and _valid_commit(commit_sha)
        and _is_positive_integer(workflow_run_id)
        and _is_positive_integer(workflow_run_attempt)
    ):
        _fail("input")


def _coerce_path(value: Path | str) -> Path:
    try:
        raw = os.fspath(value)
        if not isinstance(raw, str) or not raw or "\x00" in raw:
            _fail("input")
        return Path(os.path.abspath(raw))
    except (OSError, TypeError, ValueError):
        _fail("input")


def _same_path(left: Path | str, right: Path | str) -> bool:
    try:
        return os.path.normcase(os.path.abspath(os.fspath(left))) == os.path.normcase(
            os.path.abspath(os.fspath(right))
        )
    except (OSError, TypeError, ValueError):
        return False


def _identity(info: os.stat_result) -> tuple[int, int, int]:
    return (info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode))


def _timestamp_ns(info: os.stat_result, name: str) -> int:
    nanosecond = getattr(info, name + "_ns", None)
    if nanosecond is not None:
        return int(nanosecond)
    return int(getattr(info, name) * 1_000_000_000)


_Fingerprint = tuple[int, int, int, int, int, int, int]


def _fingerprint(info: os.stat_result) -> _Fingerprint:
    return (
        info.st_dev,
        info.st_ino,
        info.st_mode,
        info.st_nlink,
        info.st_size,
        _timestamp_ns(info, "st_mtime"),
        _timestamp_ns(info, "st_ctime"),
    )


@dataclass
class _PinnedRoot:
    path: Path
    descriptor: int | None
    metadata: os.stat_result

    def close(self) -> None:
        if self.descriptor is not None:
            os.close(self.descriptor)
            self.descriptor = None


def _root_flags() -> int:
    flags = os.O_RDONLY
    for name in ("O_DIRECTORY", "O_NOFOLLOW", "O_CLOEXEC", "O_NONBLOCK", "O_BINARY", "O_NOINHERIT"):
        flags |= int(getattr(os, name, 0))
    return flags


def _member_flags() -> int:
    flags = os.O_RDONLY
    for name in ("O_NOFOLLOW", "O_CLOEXEC", "O_NONBLOCK", "O_BINARY", "O_NOINHERIT"):
        flags |= int(getattr(os, name, 0))
    return flags


def _open_root(value: Path | str) -> _PinnedRoot:
    path = _coerce_path(value)
    try:
        before = os.lstat(path)
    except OSError:
        _fail("input")
    if stat.S_ISLNK(before.st_mode) or not stat.S_ISDIR(before.st_mode):
        _fail("input")

    descriptor: int | None = None
    if hasattr(os, "O_DIRECTORY"):
        try:
            descriptor = os.open(path, _root_flags())
            opened = os.fstat(descriptor)
        except OSError:
            if descriptor is not None:
                os.close(descriptor)
            _fail("input")
        if not stat.S_ISDIR(opened.st_mode) or _identity(before) != _identity(opened):
            os.close(descriptor)
            _fail("input")
        before = opened
    if os.name == "posix" and (
        stat.S_IMODE(before.st_mode) != 0o700 or before.st_uid != os.geteuid()
    ):
        if descriptor is not None:
            os.close(descriptor)
        _fail("input")
    return _PinnedRoot(path=path, descriptor=descriptor, metadata=before)


def _ensure_root_stable(root: _PinnedRoot) -> None:
    try:
        current = os.lstat(root.path)
        if root.descriptor is not None:
            pinned = os.fstat(root.descriptor)
            if _identity(pinned) != _identity(root.metadata):
                _fail("input")
    except OSError:
        _fail("input")
    if stat.S_ISLNK(current.st_mode) or not stat.S_ISDIR(current.st_mode):
        _fail("input")
    if os.name == "posix" and (
        stat.S_IMODE(current.st_mode) != 0o700 or current.st_uid != os.geteuid()
    ):
        _fail("input")
    if _identity(current) != _identity(root.metadata):
        _fail("input")


def _list_members(root: _PinnedRoot, error_code: str = "members") -> list[str]:
    try:
        if root.descriptor is not None and os.listdir in getattr(os, "supports_fd", set()):
            values = os.listdir(root.descriptor)
        else:
            values = os.listdir(root.path)
    except OSError:
        _fail(error_code)
    if not all(isinstance(item, str) for item in values):
        _fail(error_code)
    return values


def _check_members(root: _PinnedRoot, expected: set[str]) -> None:
    values = _list_members(root)
    if len(values) != len(expected) or set(values) != expected:
        _fail("members")


def _stat_member(root: _PinnedRoot, name: str, error_code: str) -> os.stat_result:
    try:
        if (
            root.descriptor is not None
            and os.stat in getattr(os, "supports_dir_fd", set())
            and os.stat in getattr(os, "supports_follow_symlinks", set())
        ):
            return os.stat(name, dir_fd=root.descriptor, follow_symlinks=False)
        return os.lstat(root.path / name)
    except OSError:
        _fail(error_code)


def _open_member(root: _PinnedRoot, name: str, error_code: str) -> int:
    try:
        if root.descriptor is not None and os.open in getattr(os, "supports_dir_fd", set()):
            return os.open(name, _member_flags(), dir_fd=root.descriptor)
        return os.open(root.path / name, _member_flags())
    except OSError:
        _fail(error_code)


def _validate_mode(info: os.stat_result, *, manifest: bool, require_executable: bool, error_code: str) -> None:
    if os.name != "posix":
        return
    mode = stat.S_IMODE(info.st_mode)
    special = stat.S_ISUID | stat.S_ISGID | stat.S_ISVTX
    if mode & special or mode & 0o022:
        _fail(error_code)
    if require_executable and mode & 0o111 == 0:
        _fail(error_code)
    if manifest and mode & 0o111:
        _fail(error_code)


def _validate_member_metadata(
    info: os.stat_result,
    *,
    limit: int,
    manifest: bool,
    require_executable: bool,
    error_code: str,
) -> None:
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_nlink != 1
        or info.st_size <= 0
        or info.st_size > limit
    ):
        _fail(error_code)
    _validate_mode(
        info,
        manifest=manifest,
        require_executable=require_executable,
        error_code=error_code,
    )


def _open_validated_member(
    root: _PinnedRoot,
    name: str,
    *,
    limit: int,
    manifest: bool,
    require_executable: bool,
    error_code: str,
) -> tuple[int, os.stat_result]:
    before = _stat_member(root, name, error_code)
    _validate_member_metadata(
        before,
        limit=limit,
        manifest=manifest,
        require_executable=require_executable,
        error_code=error_code,
    )
    descriptor = _open_member(root, name, error_code)
    try:
        opened = os.fstat(descriptor)
        _validate_member_metadata(
            opened,
            limit=limit,
            manifest=manifest,
            require_executable=require_executable,
            error_code=error_code,
        )
        if _fingerprint(before) != _fingerprint(opened):
            _fail(error_code)
    except BaseException:
        os.close(descriptor)
        raise
    return descriptor, opened


def _recheck_member(
    root: _PinnedRoot,
    name: str,
    expected: os.stat_result,
    *,
    limit: int,
    manifest: bool,
    require_executable: bool,
    error_code: str,
) -> None:
    current = _stat_member(root, name, error_code)
    _validate_member_metadata(
        current,
        limit=limit,
        manifest=manifest,
        require_executable=require_executable,
        error_code=error_code,
    )
    if _fingerprint(current) != _fingerprint(expected):
        _fail(error_code)
    descriptor = _open_member(root, name, error_code)
    try:
        reopened = os.fstat(descriptor)
        if _fingerprint(reopened) != _fingerprint(expected):
            _fail(error_code)
    finally:
        os.close(descriptor)


def _read_at(descriptor: int, length: int, offset: int) -> bytes:
    if hasattr(os, "pread"):
        try:
            return os.pread(descriptor, length, offset)
        except OSError:
            _fail("artifact")
    try:
        current = os.lseek(descriptor, 0, os.SEEK_CUR)
        os.lseek(descriptor, offset, os.SEEK_SET)
        value = os.read(descriptor, length)
        os.lseek(descriptor, current, os.SEEK_SET)
        return value
    except OSError:
        _fail("artifact")


def _validate_elf(descriptor: int, size: int) -> None:
    if size < 64:
        _fail("artifact")
    header = _read_at(descriptor, 64, 0)
    if len(header) != 64:
        _fail("artifact")

    object_version = int.from_bytes(header[20:24], "little")
    program_offset = int.from_bytes(header[32:40], "little")
    header_size = int.from_bytes(header[52:54], "little")
    program_entry_size = int.from_bytes(header[54:56], "little")
    program_count = int.from_bytes(header[56:58], "little")
    if not (
        header[:4] == b"\x7fELF"
        and header[4] == 2
        and header[5] == 1
        and header[6] == 1
        and int.from_bytes(header[16:18], "little") in {2, 3}
        and int.from_bytes(header[18:20], "little") == 0x3E
        and object_version == 1
        and header_size == 64
        and program_entry_size == 56
        and 1 <= program_count <= 128
    ):
        _fail("artifact")

    table_size = program_entry_size * program_count
    if program_offset < header_size or table_size > size or program_offset > size - table_size:
        _fail("artifact")
    table = _read_at(descriptor, table_size, program_offset)
    if len(table) != table_size:
        _fail("artifact")

    has_nonempty_load = False
    for index in range(program_count):
        start = index * program_entry_size
        entry = table[start : start + program_entry_size]
        if int.from_bytes(entry[0:4], "little") != 1:
            continue
        segment_offset = int.from_bytes(entry[8:16], "little")
        virtual_address = int.from_bytes(entry[16:24], "little")
        file_size = int.from_bytes(entry[32:40], "little")
        memory_size = int.from_bytes(entry[40:48], "little")
        alignment = int.from_bytes(entry[48:56], "little")
        if (
            segment_offset > size
            or file_size > size - segment_offset
            or file_size > memory_size
            or (alignment not in {0, 1} and alignment & (alignment - 1) != 0)
            or (alignment > 1 and segment_offset % alignment != virtual_address % alignment)
        ):
            _fail("artifact")
        if file_size > 0:
            has_nonempty_load = True
    if not has_nonempty_load:
        _fail("artifact")


def _hash_artifact(root: _PinnedRoot, *, require_executable: bool) -> tuple[str, int, os.stat_result]:
    descriptor, opened = _open_validated_member(
        root,
        _ARTIFACT_NAME,
        limit=_MAX_ARTIFACT_BYTES,
        manifest=False,
        require_executable=require_executable,
        error_code="artifact",
    )
    try:
        _validate_elf(descriptor, opened.st_size)
        os.lseek(descriptor, 0, os.SEEK_SET)
        digest = hashlib.sha256()
        total = 0
        while True:
            block = os.read(descriptor, _READ_CHUNK)
            if not block:
                break
            total += len(block)
            if total > _MAX_ARTIFACT_BYTES:
                _fail("artifact")
            digest.update(block)
        final = os.fstat(descriptor)
        if total != opened.st_size or _fingerprint(final) != _fingerprint(opened):
            _fail("artifact")
    except ManifestError:
        raise
    except OSError:
        _fail("artifact")
    finally:
        os.close(descriptor)
    _recheck_member(
        root,
        _ARTIFACT_NAME,
        final,
        limit=_MAX_ARTIFACT_BYTES,
        manifest=False,
        require_executable=require_executable,
        error_code="artifact",
    )
    return digest.hexdigest(), total, final


def _read_manifest_bytes(root: _PinnedRoot) -> tuple[bytes, os.stat_result]:
    descriptor, opened = _open_validated_member(
        root,
        _MANIFEST_NAME,
        limit=_MAX_MANIFEST_BYTES,
        manifest=True,
        require_executable=False,
        error_code="manifest",
    )
    try:
        chunks: list[bytes] = []
        total = 0
        while True:
            block = os.read(descriptor, _MAX_MANIFEST_BYTES + 1 - total)
            if not block:
                break
            total += len(block)
            if total > _MAX_MANIFEST_BYTES:
                _fail("manifest")
            chunks.append(block)
        final = os.fstat(descriptor)
        if total != opened.st_size or _fingerprint(final) != _fingerprint(opened):
            _fail("manifest")
    except ManifestError:
        raise
    except OSError:
        _fail("manifest")
    finally:
        os.close(descriptor)
    _recheck_member(
        root,
        _MANIFEST_NAME,
        final,
        limit=_MAX_MANIFEST_BYTES,
        manifest=True,
        require_executable=False,
        error_code="manifest",
    )
    return b"".join(chunks), final


def _parse_manifest(raw: bytes) -> dict[str, Any]:
    if not raw or len(raw) > _MAX_MANIFEST_BYTES:
        _fail("manifest")
    try:
        decoded = raw.decode("utf-8", errors="strict")
        value = json.loads(
            decoded,
            object_pairs_hook=_strict_object,
            parse_constant=_reject_json_constant,
        )
    except ManifestError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError, TypeError, ValueError):
        _fail("manifest")
    if type(value) is not dict or raw != _canonical(value):
        _fail("manifest")
    return value


def _validate_manifest_shape(manifest: dict[str, Any]) -> dict[str, Any]:
    if set(manifest) != _TOP_LEVEL_KEYS:
        _fail("manifest")
    artifacts = manifest.get("artifacts")
    if type(artifacts) is not list or len(artifacts) != 1 or type(artifacts[0]) is not dict:
        _fail("manifest")
    artifact = artifacts[0]
    if set(artifact) != _ARTIFACT_KEYS:
        _fail("manifest")
    if not (
        manifest.get("schema") == _SCHEMA
        and _valid_repository(manifest.get("repository"))
        and _valid_ref(manifest.get("ref"))
        and _valid_commit(manifest.get("commit_sha"))
        and _valid_go_version(manifest.get("go_version"))
        and _is_positive_integer(manifest.get("workflow_run_id"))
        and _is_positive_integer(manifest.get("workflow_run_attempt"))
        and manifest.get("release_readiness") == "NO_GO"
        and manifest.get("deployment_authorized") is False
        and artifact.get("name") == _ARTIFACT_NAME
        and artifact.get("path") == _ARTIFACT_NAME
        and artifact.get("os") == "linux"
        and artifact.get("arch") == "amd64"
        and isinstance(artifact.get("sha256"), str)
        and _DIGEST_RE.fullmatch(artifact["sha256"]) is not None
        and type(artifact.get("size_bytes")) is int
        and 0 < artifact["size_bytes"] <= _MAX_ARTIFACT_BYTES
    ):
        _fail("manifest")
    return artifact


def build_manifest(
    artifact_root: Path | str,
    *,
    repository: str,
    ref: str,
    commit_sha: str,
    workflow_run_id: int,
    workflow_run_attempt: int,
    go_version: str,
) -> dict[str, object]:
    _validate_build_identity(
        repository,
        ref,
        commit_sha,
        workflow_run_id,
        workflow_run_attempt,
        go_version,
    )
    root = _open_root(artifact_root)
    try:
        _check_members(root, {_ARTIFACT_NAME})
        digest, size, artifact_info = _hash_artifact(root, require_executable=True)
        _check_members(root, {_ARTIFACT_NAME})
        _ensure_root_stable(root)
        _recheck_member(
            root,
            _ARTIFACT_NAME,
            artifact_info,
            limit=_MAX_ARTIFACT_BYTES,
            manifest=False,
            require_executable=True,
            error_code="artifact",
        )
        _ensure_root_stable(root)
    finally:
        root.close()
    return {
        "artifacts": [
            {
                "arch": "amd64",
                "name": _ARTIFACT_NAME,
                "os": "linux",
                "path": _ARTIFACT_NAME,
                "sha256": digest,
                "size_bytes": size,
            }
        ],
        "commit_sha": commit_sha,
        "deployment_authorized": False,
        "go_version": go_version,
        "ref": ref,
        "release_readiness": "NO_GO",
        "repository": repository,
        "schema": _SCHEMA,
        "workflow_run_attempt": workflow_run_attempt,
        "workflow_run_id": workflow_run_id,
    }


def verify_manifest(
    artifact_root: Path | str,
    manifest_path: Path | str,
    *,
    expected_repository: str,
    expected_ref: str,
    expected_commit_sha: str,
    expected_workflow_run_id: int,
    expected_workflow_run_attempt: int,
) -> dict[str, object]:
    _validate_expected_identity(
        expected_repository,
        expected_ref,
        expected_commit_sha,
        expected_workflow_run_id,
        expected_workflow_run_attempt,
    )
    root_path = _coerce_path(artifact_root)
    expected_manifest_path = root_path / _MANIFEST_NAME
    if not _same_path(manifest_path, expected_manifest_path):
        _fail("input")

    root = _open_root(root_path)
    try:
        _check_members(root, {_ARTIFACT_NAME, _MANIFEST_NAME})
        raw_manifest, manifest_info = _read_manifest_bytes(root)
        manifest = _parse_manifest(raw_manifest)
        artifact = _validate_manifest_shape(manifest)
        if (
            manifest["repository"] != expected_repository
            or manifest["ref"] != expected_ref
            or manifest["commit_sha"] != expected_commit_sha
            or manifest["workflow_run_id"] != expected_workflow_run_id
            or manifest["workflow_run_attempt"] != expected_workflow_run_attempt
        ):
            _fail("identity")
        digest, size, artifact_info = _hash_artifact(root, require_executable=False)
        if artifact["sha256"] != digest or artifact["size_bytes"] != size:
            _fail("manifest")
        _check_members(root, {_ARTIFACT_NAME, _MANIFEST_NAME})
        _ensure_root_stable(root)
        _recheck_member(
            root,
            _MANIFEST_NAME,
            manifest_info,
            limit=_MAX_MANIFEST_BYTES,
            manifest=True,
            require_executable=False,
            error_code="manifest",
        )
        _recheck_member(
            root,
            _ARTIFACT_NAME,
            artifact_info,
            limit=_MAX_ARTIFACT_BYTES,
            manifest=False,
            require_executable=False,
            error_code="artifact",
        )
        _ensure_root_stable(root)
    finally:
        root.close()
    return {
        "artifact_sha256": digest,
        "artifact_size_bytes": size,
        "deployment_authorized": False,
        "release_readiness": "NO_GO",
        "schema": _SCHEMA,
    }


def _open_temp(root: _PinnedRoot, name: str) -> int:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    for option in ("O_NOFOLLOW", "O_CLOEXEC", "O_BINARY", "O_NOINHERIT"):
        flags |= int(getattr(os, option, 0))
    try:
        if root.descriptor is not None and os.open in getattr(os, "supports_dir_fd", set()):
            return os.open(name, flags, 0o600, dir_fd=root.descriptor)
        return os.open(root.path / name, flags, 0o600)
    except OSError:
        _fail("output")


def _stat_temp(root: _PinnedRoot, name: str) -> os.stat_result | None:
    try:
        if (
            root.descriptor is not None
            and os.stat in getattr(os, "supports_dir_fd", set())
            and os.stat in getattr(os, "supports_follow_symlinks", set())
        ):
            return os.stat(name, dir_fd=root.descriptor, follow_symlinks=False)
        return os.lstat(root.path / name)
    except OSError:
        return None


def _unlink_name(root: _PinnedRoot, name: str) -> None:
    if root.descriptor is not None and os.unlink in getattr(os, "supports_dir_fd", set()):
        os.unlink(name, dir_fd=root.descriptor)
    else:
        os.unlink(root.path / name)


def _safe_unlink_temp(root: _PinnedRoot, name: str, owned: os.stat_result | None) -> None:
    if owned is None:
        return
    current = _stat_temp(root, name)
    if current is None or _identity(current) != _identity(owned):
        return
    try:
        _unlink_name(root, name)
    except OSError:
        return


def _safe_unlink_owned(root: _PinnedRoot, name: str, owned: _Fingerprint | None) -> bool:
    if owned is None:
        return False
    current = _stat_temp(root, name)
    if current is None or _fingerprint(current) != owned:
        return False
    try:
        _ensure_root_stable(root)
    except ManifestError:
        return False
    current = _stat_temp(root, name)
    # The pinned root is exact-mode 0700 and current-EUID owned.  Rollback is a
    # trusted single-writer operation because POSIX has no conditional unlink
    # primitive that atomically compares an inode fingerprint and removes it.
    if current is None or _fingerprint(current) != owned:
        return False
    try:
        _unlink_name(root, name)
    except OSError:
        return False
    return True


def _fsync_root_best_effort(root: _PinnedRoot) -> None:
    if root.descriptor is None:
        return
    try:
        os.fsync(root.descriptor)
    except OSError:
        return


def _rollback_published_manifest(
    artifact_root: Path | str,
    output: Path | str,
    owned: _Fingerprint,
) -> None:
    root: _PinnedRoot | None = None
    try:
        root_path = _coerce_path(artifact_root)
        if not _same_path(output, root_path / _MANIFEST_NAME):
            return
        root = _open_root(root_path)
        if _safe_unlink_owned(root, _MANIFEST_NAME, owned):
            _fsync_root_best_effort(root)
    except BaseException:
        return
    finally:
        if root is not None:
            try:
                root.close()
            except OSError:
                pass


def _link_no_clobber(root: _PinnedRoot, source: str, destination: str) -> None:
    try:
        if root.descriptor is not None and os.link in getattr(os, "supports_dir_fd", set()):
            os.link(
                source,
                destination,
                src_dir_fd=root.descriptor,
                dst_dir_fd=root.descriptor,
                follow_symlinks=False,
            )
        else:
            os.link(root.path / source, root.path / destination, follow_symlinks=False)
    except FileExistsError:
        _fail("exists")
    except (OSError, TypeError):
        _fail("output")


def _publish_manifest(artifact_root: Path | str, output: Path | str, encoded: bytes) -> _Fingerprint:
    root_path = _coerce_path(artifact_root)
    if not _same_path(output, root_path / _MANIFEST_NAME):
        _fail("input")
    try:
        if os.path.lexists(root_path / _MANIFEST_NAME):
            _fail("exists")
    except OSError:
        _fail("output")

    root = _open_root(root_path)
    temporary_name = ".manifest-" + secrets.token_hex(12) + ".tmp"
    temporary_info: os.stat_result | None = None
    descriptor: int | None = None
    owned: _Fingerprint | None = None
    try:
        current = _list_members(root)
        if _MANIFEST_NAME in current:
            _fail("exists")
        if len(current) != 1 or set(current) != {_ARTIFACT_NAME}:
            _fail("members")

        descriptor = _open_temp(root, temporary_name)
        if os.name == "posix":
            os.fchmod(descriptor, 0o600)
        view = memoryview(encoded)
        written = 0
        while written < len(view):
            count = os.write(descriptor, view[written:])
            if count <= 0:
                _fail("output")
            written += count
        os.fsync(descriptor)
        temporary_info = os.fstat(descriptor)
        _validate_member_metadata(
            temporary_info,
            limit=_MAX_MANIFEST_BYTES,
            manifest=True,
            require_executable=False,
            error_code="output",
        )
        if temporary_info.st_size != len(encoded):
            _fail("output")
        if os.name != "posix":
            os.close(descriptor)
            descriptor = None

        _link_no_clobber(root, temporary_name, _MANIFEST_NAME)
        linked_info = (
            os.fstat(descriptor) if descriptor is not None else _stat_temp(root, temporary_name)
        )
        linked_path = _stat_temp(root, _MANIFEST_NAME)
        if (
            linked_info is None
            or linked_path is None
            or not stat.S_ISREG(linked_info.st_mode)
            or linked_info.st_nlink != 2
            or linked_info.st_size != len(encoded)
            or _fingerprint(linked_path) != _fingerprint(linked_info)
        ):
            _fail("output")
        owned = _fingerprint(linked_info)
        try:
            _unlink_name(root, temporary_name)
        except OSError:
            _fail("output")
        temporary_info = None

        final_info = (
            os.fstat(descriptor) if descriptor is not None else _stat_temp(root, _MANIFEST_NAME)
        )
        if final_info is None:
            _fail("output")
        _validate_member_metadata(
            final_info,
            limit=_MAX_MANIFEST_BYTES,
            manifest=True,
            require_executable=False,
            error_code="output",
        )
        final_path = _stat_temp(root, _MANIFEST_NAME)
        if (
            final_path is None
            or final_info.st_size != len(encoded)
            or _fingerprint(final_path) != _fingerprint(final_info)
        ):
            _fail("output")
        owned = _fingerprint(final_info)
        if root.descriptor is not None:
            os.fsync(root.descriptor)
        _check_members(root, {_ARTIFACT_NAME, _MANIFEST_NAME})
        _ensure_root_stable(root)
        _recheck_member(
            root,
            _MANIFEST_NAME,
            final_info,
            limit=_MAX_MANIFEST_BYTES,
            manifest=True,
            require_executable=False,
            error_code="output",
        )
        _ensure_root_stable(root)
        if descriptor is not None:
            os.close(descriptor)
            descriptor = None
        return owned
    except BaseException as error:
        candidate = owned
        if descriptor is not None:
            try:
                candidate = _fingerprint(os.fstat(descriptor))
            except OSError:
                pass
        else:
            current_temp = _stat_temp(root, temporary_name)
            if current_temp is not None:
                candidate = _fingerprint(current_temp)
        if _safe_unlink_owned(root, _MANIFEST_NAME, candidate):
            _fsync_root_best_effort(root)
        if isinstance(error, ManifestError):
            raise
        if isinstance(error, Exception):
            _fail("output")
        raise
    finally:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except OSError:
                pass
        _safe_unlink_temp(root, temporary_name, temporary_info)
        _fsync_root_best_effort(root)
        try:
            root.close()
        except OSError:
            pass


class _RedactedArgumentParser(argparse.ArgumentParser):
    def error(self, _message: str) -> None:
        _fail("input")

    def exit(self, _status: int = 0, _message: str | None = None) -> None:
        _fail("input")


def _parser() -> _RedactedArgumentParser:
    parser = _RedactedArgumentParser(add_help=False)
    commands = parser.add_subparsers(dest="command", required=True, parser_class=_RedactedArgumentParser)

    create = commands.add_parser("create", add_help=False)
    create.add_argument("--artifact-root", required=True)
    create.add_argument("--output", required=True)
    create.add_argument("--repository", required=True)
    create.add_argument("--ref", required=True)
    create.add_argument("--commit-sha", required=True)
    create.add_argument("--workflow-run-id", required=True)
    create.add_argument("--workflow-run-attempt", required=True)
    create.add_argument("--go-version", required=True)

    verify = commands.add_parser("verify", add_help=False)
    verify.add_argument("--artifact-root", required=True)
    verify.add_argument("--manifest", required=True)
    verify.add_argument("--expected-repository", required=True)
    verify.add_argument("--expected-ref", required=True)
    verify.add_argument("--expected-commit-sha", required=True)
    verify.add_argument("--expected-workflow-run-id", required=True)
    verify.add_argument("--expected-workflow-run-attempt", required=True)
    return parser


def _parse_cli_integer(value: object) -> int:
    if not isinstance(value, str) or re.fullmatch(r"[1-9][0-9]{0,18}", value) is None:
        _fail("input")
    try:
        parsed = int(value, 10)
    except ValueError:
        _fail("input")
    if not _is_positive_integer(parsed):
        _fail("input")
    return parsed


def main(argv: Sequence[str] | None = None) -> int:
    try:
        args = _parser().parse_args(argv)
        if args.command == "create":
            run_id = _parse_cli_integer(args.workflow_run_id)
            run_attempt = _parse_cli_integer(args.workflow_run_attempt)
            output = _coerce_path(args.output)
            root = _coerce_path(args.artifact_root)
            if not _same_path(output, root / _MANIFEST_NAME):
                _fail("input")
            try:
                if os.path.lexists(output):
                    _fail("exists")
            except OSError:
                _fail("output")
            manifest = build_manifest(
                root,
                repository=args.repository,
                ref=args.ref,
                commit_sha=args.commit_sha,
                workflow_run_id=run_id,
                workflow_run_attempt=run_attempt,
                go_version=args.go_version,
            )
            owned = _publish_manifest(root, output, _canonical(manifest))
            try:
                verify_manifest(
                    root,
                    output,
                    expected_repository=args.repository,
                    expected_ref=args.ref,
                    expected_commit_sha=args.commit_sha,
                    expected_workflow_run_id=run_id,
                    expected_workflow_run_attempt=run_attempt,
                )
            except BaseException:
                _rollback_published_manifest(root, output, owned)
                raise
            return 0

        result = verify_manifest(
            args.artifact_root,
            args.manifest,
            expected_repository=args.expected_repository,
            expected_ref=args.expected_ref,
            expected_commit_sha=args.expected_commit_sha,
            expected_workflow_run_id=_parse_cli_integer(args.expected_workflow_run_id),
            expected_workflow_run_attempt=_parse_cli_integer(args.expected_workflow_run_attempt),
        )
        sys.stdout.buffer.write(_canonical(result))
        sys.stdout.buffer.flush()
        return 0
    except ManifestError as error:
        sys.stderr.buffer.write((str(error) + "\n").encode("ascii"))
        sys.stderr.buffer.flush()
        return 1
    except Exception:
        sys.stderr.buffer.write((_ERRORS["input"] + "\n").encode("ascii"))
        sys.stderr.buffer.flush()
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
