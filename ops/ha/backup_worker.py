"""Offline state-machine contract for the MaestroVPN HA backup worker.

This module deliberately contains no network, subprocess, credential, or
production wiring. Durable rqlite and object-storage adapters must apply these
transitions with their own fenced transactions; repository fixtures remain
NO_GO until that wiring and production evidence exist.
"""

from __future__ import annotations

import os
import re
import stat
from dataclasses import dataclass, field, replace
from enum import Enum
from pathlib import Path
from typing import Final


MAX_VERIFIED_AGE_SECONDS: Final = 3_600
OWNER_MARKER: Final = ".maestro-backup-owner"
BUNDLE_NAME: Final = "backup.bundle"
INTERMEDIATE_FILES = frozenset(
    {
        "application-keys.json",
        "control-plane.sqlite3",
        "manifest.json",
        "manifest.sig",
        "backup.tar",
        "backup.tar.gpg",
        "decrypted.tar",
    }
)
VERIFY_DIR_NAME: Final = "verify"
VERIFY_MEMBER_FILES = frozenset(
    {"application-keys.json", "control-plane.sqlite3", "manifest.json", "manifest.sig"}
)

_ERROR = {
    "state": "backup-worker:invalid-state",
    "transition": "backup-worker:invalid-transition",
    "lease": "backup-worker:stale-lease",
    "receipt": "backup-worker:verification-mismatch",
    "runtime": "backup-worker:unsafe-runtime",
    "cutover": "backup-worker:unsafe-cutover",
}
PUBLIC_ERRORS = frozenset(_ERROR.values())
_HEX64 = re.compile(r"[0-9a-f]{64}\Z")
_BACKUP_ID = re.compile(r"[0-9a-f]{32}\Z")
_RUNTIME_ID = re.compile(r"[a-z][a-z0-9-]{0,63}\Z")
_TASK_PREFIX: Final = "task-"


class BackupWorkerError(RuntimeError):
    """A fixed, redacted backup-worker contract failure."""


def _fail(kind: str) -> BackupWorkerError:
    return BackupWorkerError(_ERROR[kind])


def _integer(value: object, *, minimum: int = 0) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value >= minimum


def _text(value: object, *, maximum: int = 256) -> bool:
    return isinstance(value, str) and 0 < len(value) <= maximum and "\x00" not in value


def _object_version(value: object) -> bool:
    return (
        _text(value, maximum=512)
        and value == value.strip()
        and value.casefold() not in {"latest", "none", "null"}
        and all(0x21 <= ord(character) <= 0x7E for character in value)
    )


def _digest(value: object) -> bool:
    return isinstance(value, str) and _HEX64.fullmatch(value) is not None


def _backup_id(value: object) -> bool:
    return isinstance(value, str) and _BACKUP_ID.fullmatch(value) is not None


def _runtime_id(value: object) -> bool:
    return isinstance(value, str) and _RUNTIME_ID.fullmatch(value) is not None


def _object_key(value: object) -> bool:
    if (
        not isinstance(value, str)
        or not 0 < len(value) <= 1_024
        or value.startswith("/")
        or "\\" in value
        or not all(0x21 <= ord(character) <= 0x7E for character in value)
    ):
        return False
    return all(part not in {"", ".", ".."} for part in value.split("/"))


def _bound_object_key(
    value: object,
    *,
    backup_id: str,
    captured_generation: int,
    attempt_sequence: int,
) -> bool:
    tail = f"g-{captured_generation}/a-{attempt_sequence}-{backup_id}.tar.gpg"
    return _object_key(value) and (value == tail or value.endswith("/" + tail))


def _now(value: object) -> int:
    if not _integer(value):
        raise _fail("state")
    return value


class AttemptPhase(str, Enum):
    PENDING = "pending"
    APPLYING = "applying"
    APPLIED = "applied"
    UNKNOWN = "unknown"


class WorkerAction(str, Enum):
    WAIT = "wait"
    BLOCKED = "blocked"
    CREATE = "create"
    UPLOAD = "upload"
    VERIFY = "verify"
    SUPERSEDE = "supersede"


class UploadOutcome(str, Enum):
    APPLIED = "applied"
    UNKNOWN = "unknown"
    NOT_SENT = "not_sent"


class ReadbackStatus(str, Enum):
    VERIFIED = "verified"
    MISSING = "missing"
    INVALID = "invalid"


class BackupHealth(str, Enum):
    OK = "ok"
    RED_NEVER_VERIFIED = "red-never-verified"
    RED_TOO_OLD = "red-too-old"


@dataclass(frozen=True)
class BackupCapabilities:
    backup_api: bool
    object_put: bool
    object_get: bool
    signing_subkey: bool
    encryption_recipient: bool
    decrypt_verify: bool

    def __post_init__(self) -> None:
        if any(
            type(value) is not bool
            for value in (
                self.backup_api,
                self.object_put,
                self.object_get,
                self.signing_subkey,
                self.encryption_recipient,
                self.decrypt_verify,
            )
        ):
            raise _fail("state")

    @property
    def ready(self) -> bool:
        return all(
            (
                self.backup_api,
                self.object_put,
                self.object_get,
                self.signing_subkey,
                self.encryption_recipient,
                self.decrypt_verify,
            )
        )


@dataclass(frozen=True)
class Lease:
    cluster_epoch: int
    fence: int
    holder_id: str
    token: str = field(repr=False)
    expires_at_unix: int = 0
    capability_generation: int = 0
    capability_evidence_sha256: str = ""
    capability_expires_at_unix: int = 0

    def __post_init__(self) -> None:
        if (
            not _integer(self.cluster_epoch, minimum=1)
            or not _integer(self.fence, minimum=1)
            or not _text(self.holder_id)
            or not _text(self.token)
            or not _integer(self.expires_at_unix)
            or not _integer(self.capability_generation, minimum=1)
            or not _digest(self.capability_evidence_sha256)
            or not _integer(self.capability_expires_at_unix, minimum=1)
            or self.expires_at_unix > self.capability_expires_at_unix
        ):
            raise _fail("state")


@dataclass(frozen=True)
class PreparedBackup:
    backup_id: str
    object_key: str = field(repr=False)
    object_digest: str
    captured_generation: int
    attempt_sequence: int = 1

    def __post_init__(self) -> None:
        if (
            not _backup_id(self.backup_id)
            or not _digest(self.object_digest)
            or not _integer(self.captured_generation)
            or not _integer(self.attempt_sequence, minimum=1)
            or not _bound_object_key(
                self.object_key,
                backup_id=self.backup_id,
                captured_generation=self.captured_generation,
                attempt_sequence=self.attempt_sequence,
            )
        ):
            raise _fail("state")


@dataclass(frozen=True)
class BackupAttempt:
    prepared: PreparedBackup
    cluster_epoch: int
    lease_fence: int
    holder_id: str
    lease_token: str = field(repr=False)
    capability_generation: int = 0
    capability_evidence_sha256: str = ""
    phase: AttemptPhase = AttemptPhase.PENDING
    uploaded_object_version_id: str | None = field(default=None, repr=False)

    def __post_init__(self) -> None:
        if (
            not isinstance(self.prepared, PreparedBackup)
            or not _integer(self.cluster_epoch, minimum=1)
            or not _integer(self.lease_fence, minimum=1)
            or not _text(self.holder_id)
            or not _text(self.lease_token)
            or not _integer(self.capability_generation, minimum=1)
            or not _digest(self.capability_evidence_sha256)
            or not isinstance(self.phase, AttemptPhase)
            or (
                self.phase is AttemptPhase.APPLIED
                and not _object_version(self.uploaded_object_version_id)
            )
            or (self.phase is not AttemptPhase.APPLIED and self.uploaded_object_version_id is not None)
        ):
            raise _fail("state")


@dataclass(frozen=True)
class BackupState:
    cluster_epoch: int
    dirty_generation: int
    verified_generation: int
    verified_at_unix: int | None
    verified_object_digest: str | None
    attempt: BackupAttempt | None = None
    lease_fence: int = 0
    lease_holder_id: str | None = None
    lease_token: str | None = field(default=None, repr=False)
    capability_generation: int = 0
    capability_evidence_sha256: str | None = None
    last_attempt_sequence: int = 0
    verified_attempt_sequence: int = 0
    verified_object_key: str | None = field(default=None, repr=False)
    verified_object_version_id: str | None = field(default=None, repr=False)
    verified_backup_id: str | None = None
    verified_manifest_version: int | None = None

    def __post_init__(self) -> None:
        if (
            not _integer(self.cluster_epoch, minimum=1)
            or not _integer(self.dirty_generation)
            or not _integer(self.verified_generation)
            or self.verified_generation > self.dirty_generation
            or (self.attempt is not None and not isinstance(self.attempt, BackupAttempt))
            or not _integer(self.lease_fence)
            or not _integer(self.capability_generation)
            or not _integer(self.last_attempt_sequence)
            or not _integer(self.verified_attempt_sequence)
            or self.verified_attempt_sequence > self.last_attempt_sequence
        ):
            raise _fail("state")
        lease_fields_empty = (
            self.lease_holder_id is None
            and self.lease_token is None
            and self.capability_generation == 0
            and self.capability_evidence_sha256 is None
        )
        lease_fields_valid = (
            _text(self.lease_holder_id)
            and _text(self.lease_token)
            and _integer(self.capability_generation, minimum=1)
            and _digest(self.capability_evidence_sha256)
        )
        if (self.lease_fence == 0 and not lease_fields_empty) or (
            self.lease_fence > 0 and not lease_fields_valid
        ):
            raise _fail("state")
        if self.verified_at_unix is None or self.verified_object_digest is None:
            if (
                self.verified_at_unix is not None
                or self.verified_object_digest is not None
                or self.verified_generation != 0
            ):
                raise _fail("state")
        elif not _integer(self.verified_at_unix) or not _digest(self.verified_object_digest):
            raise _fail("state")
        if self.verified_attempt_sequence == 0:
            if any(
                value is not None
                for value in (
                    self.verified_object_key,
                    self.verified_object_version_id,
                    self.verified_backup_id,
                    self.verified_manifest_version,
                )
            ):
                raise _fail("state")
        elif (
            not _backup_id(self.verified_backup_id)
            or self.verified_manifest_version != 2
            or not _object_version(self.verified_object_version_id)
            or not _bound_object_key(
                self.verified_object_key,
                backup_id=self.verified_backup_id,
                captured_generation=self.verified_generation,
                attempt_sequence=self.verified_attempt_sequence,
            )
            or self.verified_at_unix is None
            or self.verified_object_digest is None
        ):
            raise _fail("state")
        if self.attempt is not None:
            generation = self.attempt.prepared.captured_generation
            if (
                generation < self.verified_generation
                or generation > self.dirty_generation
                or self.attempt.prepared.attempt_sequence != self.last_attempt_sequence
                or self.attempt.lease_fence != self.lease_fence
                or self.attempt.holder_id != self.lease_holder_id
                or self.attempt.lease_token != self.lease_token
                or self.attempt.capability_generation > self.capability_generation
                or (
                    self.attempt.capability_generation == self.capability_generation
                    and self.attempt.capability_evidence_sha256
                    != self.capability_evidence_sha256
                )
            ):
                raise _fail("state")


@dataclass(frozen=True)
class Decision:
    action: WorkerAction
    code: str

    def __post_init__(self) -> None:
        if not isinstance(self.action, WorkerAction) or self.code not in {
            "idle",
            "dirty",
            "hourly",
            "never",
            "unbound",
            "capability",
            "lease",
            "pending-upload",
            "readback",
            "new-fence",
        }:
            raise _fail("state")


@dataclass(frozen=True)
class ReadbackResult:
    status: ReadbackStatus
    backup_id: str
    object_key: str = field(repr=False)
    object_digest: str | None = None
    captured_generation: int | None = None
    attempt_sequence: int | None = None
    object_version_id: str | None = field(default=None, repr=False)

    manifest_version: int | None = None
    lease_fence: int | None = None
    def __post_init__(self) -> None:
        if (
            not isinstance(self.status, ReadbackStatus)
            or not _backup_id(self.backup_id)
            or not _text(self.object_key, maximum=1_024)
        ):
            raise _fail("state")
        if self.status is ReadbackStatus.VERIFIED:
            if (
                not _digest(self.object_digest)
                or not _integer(self.captured_generation)
                or not _integer(self.attempt_sequence, minimum=1)
                or not _object_version(self.object_version_id)
                or self.manifest_version != 2
                or not _integer(self.lease_fence, minimum=1)
            ):
                raise _fail("state")
        elif self.status is ReadbackStatus.MISSING:
            if (
                self.object_digest is not None
                or self.captured_generation is not None
                or not _integer(self.attempt_sequence, minimum=1)
                or self.object_version_id is not None
                or self.manifest_version is not None
                or self.lease_fence is not None
            ):
                raise _fail("state")


def can_acquire_lease(capabilities: BackupCapabilities) -> bool:
    if not isinstance(capabilities, BackupCapabilities):
        raise _fail("state")
    return capabilities.ready


def _live_for_state(state: BackupState, candidate: Lease, db_now_unix: int) -> bool:
    return (
        candidate.cluster_epoch == state.cluster_epoch
        and candidate.expires_at_unix > db_now_unix
        and candidate.capability_expires_at_unix > db_now_unix
        and candidate.fence >= state.lease_fence
        and candidate.capability_generation >= state.capability_generation
        and (
            candidate.fence > state.lease_fence
            or state.lease_fence == 0
            or (
                candidate.holder_id == state.lease_holder_id
                and candidate.token == state.lease_token
                and candidate.capability_generation == state.capability_generation
                and candidate.capability_evidence_sha256
                == state.capability_evidence_sha256
            )
        )
    )


def _newer_than_attempt(candidate: Lease, attempt: BackupAttempt) -> bool:
    return candidate.cluster_epoch > attempt.cluster_epoch or (
        candidate.cluster_epoch == attempt.cluster_epoch
        and candidate.fence > attempt.lease_fence
    )


def _bound_to_attempt(candidate: Lease, attempt: BackupAttempt) -> bool:
    return (
        candidate.cluster_epoch == attempt.cluster_epoch
        and candidate.fence == attempt.lease_fence
        and candidate.holder_id == attempt.holder_id
        and candidate.token == attempt.lease_token
        and candidate.capability_generation == attempt.capability_generation
        and candidate.capability_evidence_sha256
        == attempt.capability_evidence_sha256
    )


def _lease_state(candidate: Lease) -> dict[str, object]:
    return {
        "lease_fence": candidate.fence,
        "lease_holder_id": candidate.holder_id,
        "lease_token": candidate.token,
        "capability_generation": candidate.capability_generation,
        "capability_evidence_sha256": candidate.capability_evidence_sha256,
    }


def _require_live_lease(state: BackupState, candidate: Lease, db_now_unix: int) -> None:
    if not isinstance(candidate, Lease) or not _live_for_state(state, candidate, db_now_unix):
        raise _fail("lease")


def _require_attempt_lease(state: BackupState, candidate: Lease, db_now_unix: int) -> BackupAttempt:
    attempt = state.attempt
    if attempt is None:
        raise _fail("transition")
    _require_live_lease(state, candidate, db_now_unix)
    if not _bound_to_attempt(candidate, attempt):
        raise _fail("lease")
    return attempt


def _rpo_bound(state: BackupState) -> bool:
    return (
        state.verified_attempt_sequence > 0
        and state.verified_manifest_version == 2
        and state.verified_backup_id is not None
        and state.verified_object_key is not None
        and state.verified_object_version_id is not None
    )


def _due_code(
    state: BackupState,
    *,
    db_now_unix: int,
    force_after_seconds: int,
) -> str | None:
    if (
        not _integer(force_after_seconds, minimum=1)
        or force_after_seconds > MAX_VERIFIED_AGE_SECONDS
    ):
        raise _fail("state")
    if state.dirty_generation > state.verified_generation:
        return "dirty"
    if state.verified_at_unix is None:
        return "never"
    if not _rpo_bound(state):
        return "unbound"
    if db_now_unix < state.verified_at_unix:
        raise _fail("state")
    if db_now_unix - state.verified_at_unix >= force_after_seconds:
        return "hourly"
    return None


def decide(
    state: BackupState,
    *,
    lease: Lease | None,
    capabilities: BackupCapabilities,
    db_now_unix: int,
    force_after_seconds: int = MAX_VERIFIED_AGE_SECONDS,
) -> Decision:
    if not isinstance(state, BackupState) or not isinstance(capabilities, BackupCapabilities):
        raise _fail("state")
    now = _now(db_now_unix)
    if state.attempt is None:
        due = _due_code(
            state,
            db_now_unix=now,
            force_after_seconds=force_after_seconds,
        )
        if due is None:
            return Decision(WorkerAction.WAIT, "idle")
        if not capabilities.ready:
            return Decision(WorkerAction.BLOCKED, "capability")
        if lease is None or not isinstance(lease, Lease) or not _live_for_state(state, lease, now):
            return Decision(WorkerAction.BLOCKED, "lease")
        return Decision(WorkerAction.CREATE, due)

    if not capabilities.ready:
        return Decision(WorkerAction.BLOCKED, "capability")
    if lease is None or not isinstance(lease, Lease) or not _live_for_state(state, lease, now):
        return Decision(WorkerAction.BLOCKED, "lease")
    attempt = state.attempt
    if _newer_than_attempt(lease, attempt):
        return Decision(WorkerAction.SUPERSEDE, "new-fence")
    if not _bound_to_attempt(lease, attempt):
        return Decision(WorkerAction.BLOCKED, "lease")
    if attempt.phase is AttemptPhase.PENDING:
        return Decision(WorkerAction.UPLOAD, "pending-upload")
    return Decision(WorkerAction.VERIFY, "readback")


def prepare_attempt(
    state: BackupState,
    *,
    lease: Lease,
    prepared: PreparedBackup,
    db_now_unix: int,
) -> BackupState:
    if not isinstance(state, BackupState) or not isinstance(prepared, PreparedBackup):
        raise _fail("state")
    now = _now(db_now_unix)
    _require_live_lease(state, lease, now)
    if state.attempt is not None:
        raise _fail("transition")
    if (
        prepared.captured_generation < state.verified_generation
        or prepared.captured_generation > state.dirty_generation
        or prepared.attempt_sequence != state.last_attempt_sequence + 1
        or prepared.backup_id == state.verified_backup_id
        or prepared.object_key == state.verified_object_key
    ):
        raise _fail("transition")
    attempt = BackupAttempt(
        prepared=prepared,
        cluster_epoch=lease.cluster_epoch,
        lease_fence=lease.fence,
        holder_id=lease.holder_id,
        lease_token=lease.token,
        capability_generation=lease.capability_generation,
        capability_evidence_sha256=lease.capability_evidence_sha256,
        phase=AttemptPhase.PENDING,
    )
    return replace(
        state,
        attempt=attempt,
        last_attempt_sequence=prepared.attempt_sequence,
        **_lease_state(lease),
    )


def mark_upload_started(
    state: BackupState,
    *,
    lease: Lease,
    backup_id: str,
    db_now_unix: int,
) -> BackupState:
    if not isinstance(state, BackupState) or not _backup_id(backup_id):
        raise _fail("state")
    attempt = _require_attempt_lease(state, lease, _now(db_now_unix))
    if attempt.phase is not AttemptPhase.PENDING or attempt.prepared.backup_id != backup_id:
        raise _fail("transition")
    return replace(state, attempt=replace(attempt, phase=AttemptPhase.APPLYING))


def record_upload_outcome(
    state: BackupState,
    *,
    lease: Lease,
    backup_id: str,
    outcome: UploadOutcome,
    db_now_unix: int,
    object_key: str | None = None,
    object_version_id: str | None = None,
) -> BackupState:
    if (
        not isinstance(state, BackupState)
        or not _backup_id(backup_id)
        or not isinstance(outcome, UploadOutcome)
    ):
        raise _fail("state")
    attempt = _require_attempt_lease(state, lease, _now(db_now_unix))
    if attempt.phase is not AttemptPhase.APPLYING or attempt.prepared.backup_id != backup_id:
        raise _fail("transition")
    if outcome is UploadOutcome.APPLIED:
        if (
            object_key != attempt.prepared.object_key
            or not _object_version(object_version_id)
        ):
            raise _fail("receipt")
        return replace(
            state,
            attempt=replace(
                attempt,
                phase=AttemptPhase.APPLIED,
                uploaded_object_version_id=object_version_id,
            ),
        )
    if object_key is not None or object_version_id is not None:
        raise _fail("receipt")
    if outcome is UploadOutcome.NOT_SENT:
        return replace(state, attempt=None)
    return replace(state, attempt=replace(attempt, phase=AttemptPhase.UNKNOWN))


def record_readback(
    state: BackupState,
    *,
    lease: Lease,
    result: ReadbackResult,
    db_now_unix: int,
) -> BackupState:
    if not isinstance(state, BackupState) or not isinstance(result, ReadbackResult):
        raise _fail("state")
    now = _now(db_now_unix)
    attempt = _require_attempt_lease(state, lease, now)
    if attempt.phase not in {
        AttemptPhase.APPLYING,
        AttemptPhase.APPLIED,
        AttemptPhase.UNKNOWN,
    }:
        raise _fail("transition")
    expected = attempt.prepared
    if (
        result.backup_id != expected.backup_id
        or result.object_key != expected.object_key
        or result.attempt_sequence != expected.attempt_sequence
    ):
        raise _fail("receipt")
    if result.status is ReadbackStatus.MISSING:
        return replace(state, attempt=None)
    if result.status is not ReadbackStatus.VERIFIED:
        raise _fail("receipt")
    if (
        result.object_digest != expected.object_digest
        or result.captured_generation != expected.captured_generation
        or result.captured_generation < state.verified_generation
        or result.captured_generation > state.dirty_generation
        or result.manifest_version != 2
        or result.lease_fence != attempt.lease_fence
        or (
            attempt.uploaded_object_version_id is not None
            and result.object_version_id != attempt.uploaded_object_version_id
        )
    ):
        raise _fail("receipt")
    return replace(
        state,
        verified_generation=result.captured_generation,
        verified_at_unix=now,
        verified_object_digest=result.object_digest,
        verified_attempt_sequence=result.attempt_sequence,
        verified_object_key=result.object_key,
        verified_object_version_id=result.object_version_id,
        verified_backup_id=result.backup_id,
        verified_manifest_version=result.manifest_version,
        attempt=None,
    )


def supersede_stale_attempt(
    state: BackupState,
    *,
    lease: Lease,
    db_now_unix: int,
) -> BackupState:
    if not isinstance(state, BackupState):
        raise _fail("state")
    now = _now(db_now_unix)
    _require_live_lease(state, lease, now)
    if state.attempt is None:
        raise _fail("transition")
    if not _newer_than_attempt(lease, state.attempt):
        raise _fail("lease")
    return replace(state, attempt=None, **_lease_state(lease))


def backup_health(
    state: BackupState,
    *,
    db_now_unix: int,
    max_verified_age_seconds: int = MAX_VERIFIED_AGE_SECONDS,
) -> BackupHealth:
    if (
        not isinstance(state, BackupState)
        or not _integer(max_verified_age_seconds, minimum=1)
        or max_verified_age_seconds > MAX_VERIFIED_AGE_SECONDS
    ):
        raise _fail("state")
    now = _now(db_now_unix)
    if not _rpo_bound(state):
        return BackupHealth.RED_NEVER_VERIFIED
    if state.verified_at_unix is None:
        return BackupHealth.RED_NEVER_VERIFIED
    if now < state.verified_at_unix:
        raise _fail("state")
    if now - state.verified_at_unix > max_verified_age_seconds:
        return BackupHealth.RED_TOO_OLD
    return BackupHealth.OK


def require_ha_backup_exclusive(*, legacy_enabled: bool, ha_enabled: bool) -> None:
    if type(legacy_enabled) is not bool or type(ha_enabled) is not bool:
        raise _fail("cutover")
    if legacy_enabled or not ha_enabled:
        raise _fail("cutover")


def _private_mode(info: os.stat_result, mode: int) -> bool:
    return os.name == "nt" or stat.S_IMODE(info.st_mode) == mode


def _owned(info: os.stat_result) -> bool:
    return not hasattr(os, "getuid") or info.st_uid == os.getuid()


def _private_directory(path: Path) -> os.stat_result:
    try:
        info = path.lstat()
    except OSError as error:
        raise _fail("runtime") from error
    if (
        stat.S_ISLNK(info.st_mode)
        or not stat.S_ISDIR(info.st_mode)
        or not _private_mode(info, 0o700)
        or not _owned(info)
    ):
        raise _fail("runtime")
    return info


def _private_file(path: Path) -> os.stat_result:
    try:
        info = path.lstat()
    except OSError as error:
        raise _fail("runtime") from error
    if (
        stat.S_ISLNK(info.st_mode)
        or not stat.S_ISREG(info.st_mode)
        or not _private_mode(info, 0o600)
        or not _owned(info)
    ):
        raise _fail("runtime")
    return info


def _marker_bytes(backup_id: str) -> bytes:
    return (backup_id + "\n").encode("ascii")


def _read_marker(task_directory: Path, backup_id: str) -> None:
    marker = task_directory / OWNER_MARKER
    _private_file(marker)
    try:
        value = marker.read_bytes()
    except OSError as error:
        raise _fail("runtime") from error
    if value != _marker_bytes(backup_id):
        raise _fail("runtime")


class PinnedBundle:
    """An open descriptor pinned to the validated bundle inode."""

    def __init__(self, path: Path, descriptor: int):
        self.path = path
        self._descriptor = descriptor

    def fileno(self) -> int:
        if self._descriptor < 0:
            raise ValueError("pinned bundle is closed")
        return self._descriptor

    def read(self, size: int = -1) -> bytes:
        descriptor = self.fileno()
        info = os.fstat(descriptor)
        if info.st_size < 0 or info.st_size > 1_073_741_824:
            raise _fail("runtime")
        limit = info.st_size if size is None or size < 0 else min(size, info.st_size)
        os.lseek(descriptor, 0, os.SEEK_SET)
        chunks = []
        remaining = limit
        while remaining:
            chunk = os.read(descriptor, min(remaining, 1_048_576))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks)

    def close(self) -> None:
        if self._descriptor >= 0:
            descriptor = self._descriptor
            self._descriptor = -1
            os.close(descriptor)

    def __enter__(self) -> "PinnedBundle":
        self.fileno()
        return self

    def __exit__(self, _kind, _value, _traceback) -> None:
        self.close()

    def __del__(self) -> None:
        try:
            self.close()
        except OSError:
            pass


def _identity(info: os.stat_result) -> tuple[int, int, int, int, int]:
    return (
        info.st_dev,
        info.st_ino,
        info.st_mode,
        getattr(info, "st_uid", -1),
        getattr(info, "st_ctime_ns", int(info.st_ctime * 1_000_000_000)),
    )


def _stable_identity(info: os.stat_result) -> tuple[int, int, int, int]:
    return (
        info.st_dev,
        info.st_ino,
        stat.S_IFMT(info.st_mode),
        getattr(info, "st_uid", -1),
    )


def _pinned_directory_supported() -> bool:
    return (
        os.name == "posix"
        and hasattr(os, "O_DIRECTORY")
        and hasattr(os, "O_NOFOLLOW")
        and os.listdir in os.supports_fd
        and all(
            operation in os.supports_dir_fd
            for operation in (os.open, os.mkdir, os.stat, os.unlink, os.rmdir)
        )
    )


def _directory_flags() -> int:
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    if hasattr(os, "O_CLOEXEC"):
        flags |= os.O_CLOEXEC
    return flags


def _file_flags(*, writable: bool = False, exclusive: bool = False) -> int:
    flags = (os.O_RDWR if writable else os.O_RDONLY) | os.O_NOFOLLOW
    if exclusive:
        flags |= os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_CLOEXEC"):
        flags |= os.O_CLOEXEC
    if hasattr(os, "O_BINARY"):
        flags |= os.O_BINARY
    return flags


def _require_directory_info(info: os.stat_result) -> None:
    if (
        not stat.S_ISDIR(info.st_mode)
        or not _private_mode(info, 0o700)
        or not _owned(info)
    ):
        raise _fail("runtime")


def _require_file_info(info: os.stat_result) -> None:
    if (
        not stat.S_ISREG(info.st_mode)
        or not _private_mode(info, 0o600)
        or not _owned(info)
    ):
        raise _fail("runtime")


def _open_directory_path(path: Path) -> tuple[int, os.stat_result]:
    try:
        before = path.lstat()
        _require_directory_info(before)
        descriptor = os.open(path, _directory_flags())
        opened = os.fstat(descriptor)
        _require_directory_info(opened)
        if _identity(before) != _identity(opened):
            raise _fail("runtime")
        return descriptor, opened
    except (BackupWorkerError, OSError) as error:
        if "descriptor" in locals():
            os.close(descriptor)
        if isinstance(error, BackupWorkerError):
            raise
        raise _fail("runtime") from error


def _open_directory_at(parent_descriptor: int, name: str) -> tuple[int, os.stat_result]:
    try:
        before = os.stat(name, dir_fd=parent_descriptor, follow_symlinks=False)
        _require_directory_info(before)
        descriptor = os.open(name, _directory_flags(), dir_fd=parent_descriptor)
        opened = os.fstat(descriptor)
        _require_directory_info(opened)
        if _identity(before) != _identity(opened):
            raise _fail("runtime")
        return descriptor, opened
    except (BackupWorkerError, OSError) as error:
        if "descriptor" in locals():
            os.close(descriptor)
        if isinstance(error, BackupWorkerError):
            raise
        raise _fail("runtime") from error


def _open_file_at(parent_descriptor: int, name: str) -> tuple[int, os.stat_result]:
    try:
        before = os.stat(name, dir_fd=parent_descriptor, follow_symlinks=False)
        _require_file_info(before)
        descriptor = os.open(name, _file_flags(), dir_fd=parent_descriptor)
        opened = os.fstat(descriptor)
        _require_file_info(opened)
        if _identity(before) != _identity(opened):
            raise _fail("runtime")
        return descriptor, opened
    except (BackupWorkerError, OSError) as error:
        if "descriptor" in locals():
            os.close(descriptor)
        if isinstance(error, BackupWorkerError):
            raise
        raise _fail("runtime") from error


def _read_descriptor(descriptor: int, maximum: int) -> bytes:
    try:
        info = os.fstat(descriptor)
        if info.st_size < 0 or info.st_size > maximum:
            raise _fail("runtime")
        os.lseek(descriptor, 0, os.SEEK_SET)
        chunks = []
        remaining = info.st_size
        while remaining:
            chunk = os.read(descriptor, min(remaining, 65_536))
            if not chunk:
                raise _fail("runtime")
            chunks.append(chunk)
            remaining -= len(chunk)
        if os.read(descriptor, 1):
            raise _fail("runtime")
        return b"".join(chunks)
    except OSError as error:
        raise _fail("runtime") from error


def _entry_identity(parent_descriptor: int, name: str) -> tuple[int, int, int, int, int]:
    try:
        info = os.stat(name, dir_fd=parent_descriptor, follow_symlinks=False)
    except OSError as error:
        raise _fail("runtime") from error
    return _identity(info)


def _create_task_directory_posix(runtime_root: Path, backup_id: str) -> Path:
    root_descriptor, root_info = _open_directory_path(runtime_root)
    task_name = _TASK_PREFIX + backup_id
    task_descriptor = -1
    marker_descriptor = -1
    created = False
    marker_created = False
    try:
        os.mkdir(task_name, 0o700, dir_fd=root_descriptor)
        created = True
        task_descriptor, task_info = _open_directory_at(root_descriptor, task_name)
        marker_descriptor = os.open(
            OWNER_MARKER,
            _file_flags(writable=True, exclusive=True),
            0o600,
            dir_fd=task_descriptor,
        )
        marker_created = True
        payload = _marker_bytes(backup_id)
        written = 0
        while written < len(payload):
            count = os.write(marker_descriptor, payload[written:])
            if count <= 0:
                raise _fail("runtime")
            written += count
        os.fsync(marker_descriptor)
        os.lseek(marker_descriptor, 0, os.SEEK_SET)
        if _read_descriptor(marker_descriptor, 256) != payload:
            raise _fail("runtime")
        if _stable_identity(
            os.stat(task_name, dir_fd=root_descriptor, follow_symlinks=False)
        ) != _stable_identity(task_info):
            raise _fail("runtime")
        try:
            current_root = runtime_root.lstat()
        except OSError as error:
            raise _fail("runtime") from error
        if _stable_identity(current_root) != _stable_identity(root_info):
            raise _fail("runtime")
        os.fsync(task_descriptor)
        os.fsync(root_descriptor)
        return runtime_root / task_name
    except (BackupWorkerError, OSError) as error:
        if created:
            try:
                if marker_created:
                    os.unlink(OWNER_MARKER, dir_fd=task_descriptor)
                if task_descriptor >= 0:
                    os.close(task_descriptor)
                    task_descriptor = -1
                os.rmdir(task_name, dir_fd=root_descriptor)
            except OSError:
                pass
        if isinstance(error, BackupWorkerError):
            raise
        raise _fail("runtime") from error
    finally:
        if marker_descriptor >= 0:
            os.close(marker_descriptor)
        if task_descriptor >= 0:
            os.close(task_descriptor)
        os.close(root_descriptor)


def _open_private_bundle_posix(task: Path) -> PinnedBundle:
    task_descriptor, task_info = _open_directory_path(task)
    marker_descriptor = -1
    bundle_descriptor = -1
    try:
        backup_id = _task_id(task)
        names = set(os.listdir(task_descriptor))
        if names != {OWNER_MARKER, BUNDLE_NAME}:
            raise _fail("runtime")
        marker_descriptor, _ = _open_file_at(task_descriptor, OWNER_MARKER)
        if _read_descriptor(marker_descriptor, 256) != _marker_bytes(backup_id):
            raise _fail("runtime")
        bundle_descriptor, _ = _open_file_at(task_descriptor, BUNDLE_NAME)
        try:
            current_task = task.lstat()
        except OSError as error:
            raise _fail("runtime") from error
        if _identity(current_task) != _identity(task_info):
            raise _fail("runtime")
        pinned = PinnedBundle(task / BUNDLE_NAME, bundle_descriptor)
        bundle_descriptor = -1
        return pinned
    finally:
        if marker_descriptor >= 0:
            os.close(marker_descriptor)
        if bundle_descriptor >= 0:
            os.close(bundle_descriptor)
        os.close(task_descriptor)


def _cleanup_stale_posix(
    runtime_root: Path,
    *,
    active_backup_ids: frozenset[str],
    now_unix: int,
    stale_after_seconds: int,
) -> tuple[str, ...]:
    root_descriptor, _ = _open_directory_path(runtime_root)
    removed = []
    try:
        for task_name in sorted(os.listdir(root_descriptor)):
            if not task_name.startswith(_TASK_PREFIX):
                continue
            backup_id = task_name[len(_TASK_PREFIX) :]
            if not _runtime_id(backup_id) or backup_id in active_backup_ids:
                continue
            task_descriptor = -1
            verify_descriptor = -1
            try:
                task_descriptor, task_info = _open_directory_at(root_descriptor, task_name)
                if task_info.st_mtime > now_unix - stale_after_seconds:
                    continue
                names = set(os.listdir(task_descriptor))
                allowed = {OWNER_MARKER, BUNDLE_NAME, VERIFY_DIR_NAME} | INTERMEDIATE_FILES
                if OWNER_MARKER not in names or not names <= allowed:
                    raise _fail("runtime")

                top_identities = {}
                marker_value = None
                for name in sorted(names - {VERIFY_DIR_NAME}):
                    descriptor, info = _open_file_at(task_descriptor, name)
                    try:
                        if name == OWNER_MARKER:
                            marker_value = _read_descriptor(descriptor, 256)
                        top_identities[name] = _identity(info)
                    finally:
                        os.close(descriptor)
                if marker_value != _marker_bytes(backup_id):
                    raise _fail("runtime")

                verify_identity = None
                verify_identities = {}
                if VERIFY_DIR_NAME in names:
                    verify_descriptor, verify_info = _open_directory_at(
                        task_descriptor,
                        VERIFY_DIR_NAME,
                    )
                    verify_identity = _identity(verify_info)
                    verify_names = set(os.listdir(verify_descriptor))
                    if not verify_names <= VERIFY_MEMBER_FILES:
                        raise _fail("runtime")
                    for name in sorted(verify_names):
                        descriptor, info = _open_file_at(verify_descriptor, name)
                        os.close(descriptor)
                        verify_identities[name] = _identity(info)

                if any(
                    _entry_identity(task_descriptor, name) != identity
                    for name, identity in top_identities.items()
                ):
                    raise _fail("runtime")
                if verify_descriptor >= 0:
                    if _entry_identity(task_descriptor, VERIFY_DIR_NAME) != verify_identity:
                        raise _fail("runtime")
                    if any(
                        _entry_identity(verify_descriptor, name) != identity
                        for name, identity in verify_identities.items()
                    ):
                        raise _fail("runtime")

                if verify_descriptor >= 0:
                    for name in sorted(verify_identities):
                        os.unlink(name, dir_fd=verify_descriptor)
                    os.close(verify_descriptor)
                    verify_descriptor = -1
                    os.rmdir(VERIFY_DIR_NAME, dir_fd=task_descriptor)
                for name in sorted(top_identities):
                    if name != OWNER_MARKER:
                        os.unlink(name, dir_fd=task_descriptor)
                os.unlink(OWNER_MARKER, dir_fd=task_descriptor)
                if _stable_identity(
                    os.stat(task_name, dir_fd=root_descriptor, follow_symlinks=False)
                ) != _stable_identity(task_info):
                    raise _fail("runtime")
                os.close(task_descriptor)
                task_descriptor = -1
                os.rmdir(task_name, dir_fd=root_descriptor)
            except (BackupWorkerError, OSError):
                continue
            finally:
                if verify_descriptor >= 0:
                    os.close(verify_descriptor)
                if task_descriptor >= 0:
                    os.close(task_descriptor)
            removed.append(backup_id)
    finally:
        os.close(root_descriptor)
    return tuple(removed)


def create_task_directory(runtime_root: Path | str, backup_id: str) -> Path:
    if not _runtime_id(backup_id):
        raise _fail("runtime")
    root = Path(runtime_root)
    if _pinned_directory_supported():
        return _create_task_directory_posix(root, backup_id)
    _private_directory(root)
    task = root / (_TASK_PREFIX + backup_id)
    created = False
    marker_created = False
    try:
        task.mkdir(mode=0o700)
        created = True
        if os.name != "nt":
            task.chmod(0o700)
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        if hasattr(os, "O_NOFOLLOW"):
            flags |= os.O_NOFOLLOW
        if hasattr(os, "O_BINARY"):
            flags |= os.O_BINARY
        descriptor = os.open(task / OWNER_MARKER, flags, 0o600)
        marker_created = True
        try:
            payload = _marker_bytes(backup_id)
            written = 0
            while written < len(payload):
                count = os.write(descriptor, payload[written:])
                if count <= 0:
                    raise OSError("short owner marker write")
                written += count
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        if os.name != "nt":
            (task / OWNER_MARKER).chmod(0o600)
        _private_directory(task)
        _read_marker(task, backup_id)
        return task
    except (BackupWorkerError, OSError) as error:
        if created:
            try:
                marker = task / OWNER_MARKER
                if marker_created and marker.exists() and not marker.is_symlink():
                    marker.unlink()
                if task.exists() and not task.is_symlink():
                    task.rmdir()
            except OSError:
                pass
        if isinstance(error, BackupWorkerError):
            raise
        raise _fail("runtime") from error


def _task_id(task_directory: Path) -> str:
    name = task_directory.name
    if not name.startswith(_TASK_PREFIX):
        raise _fail("runtime")
    backup_id = name[len(_TASK_PREFIX) :]
    if not _runtime_id(backup_id):
        raise _fail("runtime")
    return backup_id


def validate_private_bundle(task_directory: Path | str) -> PinnedBundle:
    task = Path(task_directory)
    if _pinned_directory_supported():
        return _open_private_bundle_posix(task)
    _private_directory(task)
    backup_id = _task_id(task)
    _read_marker(task, backup_id)
    bundle = task / BUNDLE_NAME
    bundle_info = _private_file(bundle)
    try:
        names = {entry.name for entry in os.scandir(task)}
    except OSError as error:
        raise _fail("runtime") from error
    if names != {OWNER_MARKER, BUNDLE_NAME}:
        raise _fail("runtime")
    flags = os.O_RDONLY
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    if hasattr(os, "O_BINARY"):
        flags |= os.O_BINARY
    descriptor = -1
    try:
        descriptor = os.open(bundle, flags)
        if _identity(os.fstat(descriptor)) != _identity(bundle_info):
            raise _fail("runtime")
        pinned = PinnedBundle(bundle, descriptor)
        descriptor = -1
        return pinned
    except BackupWorkerError:
        raise
    except OSError as error:
        raise _fail("runtime") from error
    finally:
        if descriptor >= 0:
            os.close(descriptor)


def _cleanup_candidate(
    task: Path,
    backup_id: str,
) -> tuple[tuple[Path, ...], tuple[Path, ...]]:
    _private_directory(task)
    _read_marker(task, backup_id)
    try:
        entries = tuple(os.scandir(task))
    except OSError as error:
        raise _fail("runtime") from error
    allowed = {OWNER_MARKER, BUNDLE_NAME, VERIFY_DIR_NAME} | INTERMEDIATE_FILES
    if any(entry.name not in allowed for entry in entries):
        raise _fail("runtime")
    files = []
    directories = []
    for entry in entries:
        path = task / entry.name
        if entry.name != VERIFY_DIR_NAME:
            _private_file(path)
            files.append(path)
            continue
        _private_directory(path)
        try:
            verify_entries = tuple(os.scandir(path))
        except OSError as error:
            raise _fail("runtime") from error
        if any(item.name not in VERIFY_MEMBER_FILES for item in verify_entries):
            raise _fail("runtime")
        for item in verify_entries:
            member = path / item.name
            _private_file(member)
            files.append(member)
        directories.append(path)
    if OWNER_MARKER not in {path.name for path in files}:
        raise _fail("runtime")
    return tuple(files), tuple(directories)


def cleanup_stale_task_directories(
    runtime_root: Path | str,
    *,
    active_backup_ids: frozenset[str],
    now_unix: int,
    stale_after_seconds: int,
) -> tuple[str, ...]:
    root = Path(runtime_root)
    now = _now(now_unix)
    if (
        not isinstance(active_backup_ids, frozenset)
        or not _integer(stale_after_seconds, minimum=1)
        or any(not _runtime_id(value) for value in active_backup_ids)
    ):
        raise _fail("runtime")
    if _pinned_directory_supported():
        return _cleanup_stale_posix(
            root,
            active_backup_ids=active_backup_ids,
            now_unix=now,
            stale_after_seconds=stale_after_seconds,
        )
    _private_directory(root)
    try:
        entries = sorted(os.scandir(root), key=lambda item: item.name)
    except OSError as error:
        raise _fail("runtime") from error
    removed = []
    for entry in entries:
        if not entry.name.startswith(_TASK_PREFIX) or entry.is_symlink():
            continue
        backup_id = entry.name[len(_TASK_PREFIX) :]
        if not _runtime_id(backup_id) or backup_id in active_backup_ids:
            continue
        task = root / entry.name
        try:
            info = _private_directory(task)
            if info.st_mtime > now - stale_after_seconds:
                continue
            files, directories = _cleanup_candidate(task, backup_id)
            marker = task / OWNER_MARKER
            for child in files:
                if child != marker:
                    child.unlink()
            for directory in directories:
                directory.rmdir()
            marker.unlink()
            task.rmdir()
        except (BackupWorkerError, OSError):
            continue
        removed.append(backup_id)
    return tuple(removed)
