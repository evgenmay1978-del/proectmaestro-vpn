import ast
import os
import stat
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path

from ops.ha import backup_worker as worker


HEX_A = "a" * 64
HEX_B = "b" * 64
BACKUP_A = "a" * 32
BACKUP_B = "b" * 32


def bound_object_key(backup_id, generation, sequence, prefix="private/cluster-a"):
    return f"{prefix}/g-{generation}/a-{sequence}-{backup_id}.tar.gpg"


OBJECT_A = bound_object_key(BACKUP_A, 5, 1)
OBJECT_B = bound_object_key(BACKUP_B, 6, 2)


def ready_capabilities(**overrides):
    values = {
        "backup_api": True,
        "object_put": True,
        "object_get": True,
        "signing_subkey": True,
        "encryption_recipient": True,
        "decrypt_verify": True,
    }
    values.update(overrides)
    return worker.BackupCapabilities(**values)


def lease(*, epoch=7, fence=11, holder="worker-s2", token="lease-token", expires=10_000):
    return worker.Lease(
        cluster_epoch=epoch,
        fence=fence,
        holder_id=holder,
        token=token,
        expires_at_unix=expires,
        capability_generation=3,
        capability_evidence_sha256=HEX_A,
        capability_expires_at_unix=expires,
    )


def state(*, dirty=5, verified=3, verified_at=1_000, attempt=None):
    digest = HEX_A if verified_at is not None else None
    return worker.BackupState(
        cluster_epoch=7,
        dirty_generation=dirty,
        verified_generation=verified,
        verified_at_unix=verified_at,
        verified_object_digest=digest,
        attempt=attempt,
    )


def bound_state(*, generation=3, verified_at=1_000):
    return worker.BackupState(
        cluster_epoch=7,
        dirty_generation=generation,
        verified_generation=generation,
        verified_at_unix=verified_at,
        verified_object_digest=HEX_A,
        last_attempt_sequence=1,
        verified_attempt_sequence=1,
        verified_object_key=bound_object_key(BACKUP_A, generation, 1),
        verified_object_version_id="version-base",
        verified_backup_id=BACKUP_A,
        verified_manifest_version=2,
    )


def prepared(*, generation=5, backup_id=BACKUP_A, object_key=None, digest=HEX_B, sequence=1):
    if object_key is None:
        object_key = bound_object_key(backup_id, generation, sequence)
    return worker.PreparedBackup(
        backup_id=backup_id,
        object_key=object_key,
        object_digest=digest,
        captured_generation=generation,
        attempt_sequence=sequence,
    )


def applying_state(*, dirty=5, generation=5, phase=worker.AttemptPhase.APPLYING):
    current = state(dirty=dirty)
    current = worker.prepare_attempt(
        current,
        lease=lease(),
        prepared=prepared(generation=generation),
        db_now_unix=2_000,
    )
    if phase is worker.AttemptPhase.PENDING:
        return current
    current = worker.mark_upload_started(
        current,
        lease=lease(),
        backup_id=BACKUP_A,
        db_now_unix=2_001,
    )
    if phase is worker.AttemptPhase.APPLYING:
        return current
    outcome = (
        worker.UploadOutcome.APPLIED
        if phase is worker.AttemptPhase.APPLIED
        else worker.UploadOutcome.UNKNOWN
    )
    receipt = {}
    if outcome is worker.UploadOutcome.APPLIED:
        receipt = {
            "object_key": bound_object_key(BACKUP_A, generation, 1),
            "object_version_id": "version-a",
        }
    return worker.record_upload_outcome(
        current,
        lease=lease(),
        backup_id=BACKUP_A,
        outcome=outcome,
        db_now_unix=2_002,
        **receipt,
    )


class BackupWorkerStateMachineTests(unittest.TestCase):
    def test_dirty_burst_coalesces_into_one_active_attempt(self):
        current = state(dirty=9, verified=3)
        decision = worker.decide(
            current,
            lease=lease(),
            capabilities=ready_capabilities(),
            db_now_unix=2_000,
        )
        self.assertEqual(decision.action, worker.WorkerAction.CREATE)

        active = worker.prepare_attempt(
            current,
            lease=lease(),
            prepared=prepared(generation=9),
            db_now_unix=2_000,
        )
        self.assertEqual(active.attempt.prepared.captured_generation, 9)
        self.assertEqual(
            worker.decide(
                active,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=2_001,
            ).action,
            worker.WorkerAction.UPLOAD,
        )
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:invalid-transition$"):
            worker.prepare_attempt(
                active,
                lease=lease(),
                prepared=prepared(generation=9, backup_id=BACKUP_B),
                db_now_unix=2_001,
            )

    def test_poll_recovers_lost_wakeup_from_durable_watermark(self):
        decision = worker.decide(
            state(dirty=4, verified=3),
            lease=lease(),
            capabilities=ready_capabilities(),
            db_now_unix=2_000,
        )
        self.assertEqual(decision, worker.Decision(worker.WorkerAction.CREATE, "dirty"))

    def test_crash_before_upload_resumes_pending_attempt_without_second_capture(self):
        current = applying_state(phase=worker.AttemptPhase.PENDING)
        decision = worker.decide(
            current,
            lease=lease(),
            capabilities=ready_capabilities(),
            db_now_unix=2_010,
        )
        self.assertEqual(decision.action, worker.WorkerAction.UPLOAD)
        self.assertEqual(current.attempt.prepared.backup_id, BACKUP_A)

    def test_unknown_upload_result_requires_readback_and_never_reputs(self):
        current = applying_state(phase=worker.AttemptPhase.UNKNOWN)
        self.assertEqual(
            worker.decide(
                current,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=2_010,
            ).action,
            worker.WorkerAction.VERIFY,
        )
        self.assertEqual(current.verified_generation, 3)
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.VERIFIED,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                object_digest=HEX_B,
                captured_generation=5,
                attempt_sequence=1,
                object_version_id="version-a",
                manifest_version=2,
                lease_fence=11,
            ),
            db_now_unix=2_011,
        )
        self.assertEqual(verified.verified_generation, 5)
        self.assertEqual(verified.verified_object_digest, HEX_B)
        self.assertEqual(verified.verified_object_version_id, "version-a")
        self.assertEqual(verified.verified_attempt_sequence, 1)
        self.assertIsNone(verified.attempt)

    def test_crash_after_upload_before_verification_resumes_readback_only(self):
        for phase in (worker.AttemptPhase.APPLYING, worker.AttemptPhase.APPLIED):
            with self.subTest(phase=phase):
                current = applying_state(phase=phase)
                self.assertEqual(
                    worker.decide(
                        current,
                        lease=lease(),
                        capabilities=ready_capabilities(),
                        db_now_unix=2_010,
                    ).action,
                    worker.WorkerAction.VERIFY,
                )

    def test_concurrent_write_after_capture_remains_dirty_after_ack(self):
        current = applying_state(dirty=5, generation=5, phase=worker.AttemptPhase.APPLIED)
        current = replace(current, dirty_generation=8)
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.VERIFIED,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                object_digest=HEX_B,
                captured_generation=5,
                attempt_sequence=1,
                object_version_id="version-a",
                manifest_version=2,
                lease_fence=11,
            ),
            db_now_unix=2_020,
        )
        self.assertEqual((verified.dirty_generation, verified.verified_generation), (8, 5))
        self.assertEqual(
            worker.decide(
                verified,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=2_021,
            ).action,
            worker.WorkerAction.CREATE,
        )

    def test_stale_lease_cannot_transition_or_ack_after_handoff(self):
        current = applying_state(phase=worker.AttemptPhase.APPLIED)
        newer = lease(fence=12, holder="worker-s3", token="new-token")
        self.assertEqual(
            worker.decide(
                current,
                lease=newer,
                capabilities=ready_capabilities(),
                db_now_unix=2_010,
            ).action,
            worker.WorkerAction.SUPERSEDE,
        )
        result = worker.ReadbackResult(
            status=worker.ReadbackStatus.VERIFIED,
            backup_id=BACKUP_A,
            object_key=OBJECT_A,
            object_digest=HEX_B,
            captured_generation=5,
            attempt_sequence=1,
            object_version_id="version-a",
            manifest_version=2,
            lease_fence=11,
        )
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:stale-lease$"):
            worker.record_readback(current, lease=newer, result=result, db_now_unix=2_010)
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:stale-lease$"):
            worker.record_readback(current, lease=lease(expires=2_010), result=result, db_now_unix=2_010)

    def test_newer_fence_supersedes_without_advancing_verified_generation(self):
        current = applying_state(phase=worker.AttemptPhase.UNKNOWN)
        superseded = worker.supersede_stale_attempt(
            current,
            lease=lease(fence=12, holder="worker-s3", token="new-token"),
            db_now_unix=2_010,
        )
        self.assertIsNone(superseded.attempt)
        self.assertEqual(superseded.verified_generation, 3)
        self.assertEqual(superseded.lease_fence, 12)

        stale = lease(fence=11)
        self.assertEqual(
            worker.decide(
                superseded,
                lease=stale,
                capabilities=ready_capabilities(),
                db_now_unix=2_011,
            ),
            worker.Decision(worker.WorkerAction.BLOCKED, "lease"),
        )
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:stale-lease$"):
            worker.prepare_attempt(
                superseded,
                lease=stale,
                prepared=prepared(sequence=2),
                db_now_unix=2_011,
            )

    def test_missing_any_capability_blocks_due_work(self):
        fields = (
            "backup_api",
            "object_put",
            "object_get",
            "signing_subkey",
            "encryption_recipient",
            "decrypt_verify",
        )
        for field in fields:
            with self.subTest(field=field):
                capabilities = ready_capabilities(**{field: False})
                self.assertFalse(worker.can_acquire_lease(capabilities))
                decision = worker.decide(
                    state(),
                    lease=lease(),
                    capabilities=capabilities,
                    db_now_unix=2_000,
                )
                self.assertEqual(decision, worker.Decision(worker.WorkerAction.BLOCKED, "capability"))

    def test_capability_generation_is_bound_to_every_attempt_transition(self):
        current = applying_state(phase=worker.AttemptPhase.PENDING)
        revoked = replace(current, capability_generation=4)
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:stale-lease$"):
            worker.mark_upload_started(
                revoked,
                lease=lease(),
                backup_id=BACKUP_A,
                db_now_unix=2_010,
            )
        expired = lease(expires=2_010)
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:stale-lease$"):
            worker.mark_upload_started(current, lease=expired, backup_id=BACKUP_A, db_now_unix=2_010)

    def test_hourly_force_and_verified_age_red_boundaries_are_exact(self):
        clean = bound_state(generation=3, verified_at=1_000)
        before = worker.decide(
            clean,
            lease=lease(),
            capabilities=ready_capabilities(),
            db_now_unix=4_599,
        )
        boundary = worker.decide(
            clean,
            lease=lease(),
            capabilities=ready_capabilities(),
            db_now_unix=4_600,
        )
        self.assertEqual(before.action, worker.WorkerAction.WAIT)
        self.assertEqual(boundary, worker.Decision(worker.WorkerAction.CREATE, "hourly"))
        self.assertEqual(
            worker.backup_health(clean, db_now_unix=4_600),
            worker.BackupHealth.OK,
        )
        self.assertEqual(
            worker.backup_health(clean, db_now_unix=4_601),
            worker.BackupHealth.RED_TOO_OLD,
        )
        never = state(dirty=1, verified=0, verified_at=None)
        self.assertEqual(
            worker.backup_health(never, db_now_unix=1),
            worker.BackupHealth.RED_NEVER_VERIFIED,
        )

        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:invalid-state$"):
            worker.decide(
                clean,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=4_601,
                force_after_seconds=3_601,
            )
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:invalid-state$"):
            worker.backup_health(
                clean,
                db_now_unix=4_601,
                max_verified_age_seconds=3_601,
            )

    def test_verified_readback_requires_signed_v2_attempt_binding(self):
        current = applying_state(phase=worker.AttemptPhase.APPLIED)
        valid = worker.ReadbackResult(
            status=worker.ReadbackStatus.VERIFIED,
            backup_id=BACKUP_A,
            object_key=OBJECT_A,
            object_digest=HEX_B,
            captured_generation=5,
            attempt_sequence=1,
            object_version_id="version-a",
            manifest_version=2,
            lease_fence=11,
        )
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=valid,
            db_now_unix=2_010,
        )
        self.assertEqual(verified.verified_generation, 5)
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:invalid-state$",
        ):
            replace(valid, manifest_version=1)
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:verification-mismatch$",
        ):
            worker.record_readback(
                current,
                lease=lease(),
                result=replace(valid, lease_fence=12),
                db_now_unix=2_010,
            )
        for version in ("", " latest", "latest", "null", "none", "version\ncontrol"):
            with self.subTest(version=version):
                with self.assertRaisesRegex(
                    worker.BackupWorkerError,
                    "^backup-worker:invalid-state$",
                ):
                    replace(valid, object_version_id=version)

        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:invalid-state$",
        ):
            worker.ReadbackResult(
                status=worker.ReadbackStatus.MISSING,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                attempt_sequence=1,
                manifest_version=2,
                lease_fence=11,
            )

    def test_readback_must_match_id_key_digest_and_captured_generation(self):
        current = applying_state(phase=worker.AttemptPhase.APPLIED)
        base = worker.ReadbackResult(
            status=worker.ReadbackStatus.VERIFIED,
            backup_id=BACKUP_A,
            object_key=OBJECT_A,
            object_digest=HEX_B,
            captured_generation=5,
            attempt_sequence=1,
            object_version_id="version-a",
            manifest_version=2,
            lease_fence=11,
        )
        invalid = (
            replace(base, backup_id=BACKUP_B),
            replace(base, object_key=OBJECT_B),
            replace(base, object_digest=HEX_A),
            replace(base, captured_generation=4),
            replace(base, attempt_sequence=2),
            replace(base, status=worker.ReadbackStatus.INVALID),
        )
        for result in invalid:
            with self.subTest(result=result):
                with self.assertRaisesRegex(
                    worker.BackupWorkerError,
                    "^backup-worker:verification-mismatch$",
                ):
                    worker.record_readback(
                        current,
                        lease=lease(),
                        result=result,
                        db_now_unix=2_010,
                    )
                self.assertEqual(current.verified_generation, 3)

    def test_only_one_active_attempt_is_permitted(self):
        current = applying_state(phase=worker.AttemptPhase.PENDING)
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:invalid-transition$"):
            worker.prepare_attempt(
                current,
                lease=lease(),
                prepared=prepared(backup_id=BACKUP_B),
                db_now_unix=2_010,
            )

    def test_attempt_sequence_is_burned_and_old_receipt_cannot_be_recreated(self):
        current = applying_state(phase=worker.AttemptPhase.APPLIED)
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.VERIFIED,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                object_digest=HEX_B,
                captured_generation=5,
                attempt_sequence=1,
                object_version_id="version-a",
                manifest_version=2,
                lease_fence=11,
            ),
            db_now_unix=2_010,
        )
        with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:invalid-transition$"):
            worker.prepare_attempt(
                verified, lease=lease(), prepared=prepared(sequence=1), db_now_unix=2_011
            )
        self.assertEqual(verified.last_attempt_sequence, 1)

    def test_proven_not_sent_clears_attempt_but_unknown_does_not(self):
        applying = applying_state(phase=worker.AttemptPhase.APPLYING)
        unknown = worker.record_upload_outcome(
            applying,
            lease=lease(),
            backup_id=BACKUP_A,
            outcome=worker.UploadOutcome.UNKNOWN,
            db_now_unix=2_010,
        )
        self.assertEqual(unknown.attempt.phase, worker.AttemptPhase.UNKNOWN)
        not_sent = worker.record_upload_outcome(
            applying,
            lease=lease(),
            backup_id=BACKUP_A,
            outcome=worker.UploadOutcome.NOT_SENT,
            db_now_unix=2_010,
        )
        self.assertIsNone(not_sent.attempt)
        self.assertEqual(not_sent.verified_generation, 3)

    def test_missing_remote_object_retries_with_a_new_attempt(self):
        current = applying_state(phase=worker.AttemptPhase.UNKNOWN)
        missing = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.MISSING,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                attempt_sequence=1,
            ),
            db_now_unix=2_010,
        )
        self.assertIsNone(missing.attempt)
        self.assertEqual(
            worker.decide(
                missing,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=2_011,
            ).action,
            worker.WorkerAction.CREATE,
        )

    def test_new_attempt_cannot_reuse_previous_verified_backup_identity(self):
        current = applying_state(phase=worker.AttemptPhase.APPLIED)
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.VERIFIED,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                object_digest=HEX_B,
                captured_generation=5,
                attempt_sequence=1,
                object_version_id="version-a",
                manifest_version=2,
                lease_fence=11,
            ),
            db_now_unix=2_010,
        )
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:invalid-transition$",
        ):
            worker.prepare_attempt(
                replace(verified, dirty_generation=6),
                lease=lease(),
                prepared=prepared(
                    generation=6,
                    backup_id=BACKUP_A,
                    object_key=bound_object_key(BACKUP_A, 6, 2),
                    digest=HEX_A,
                    sequence=2,
                ),
                db_now_unix=2_011,
            )

    def test_applied_upload_receipt_rejects_different_readback_version(self):
        current = applying_state(phase=worker.AttemptPhase.APPLIED)
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.VERIFIED,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                object_digest=HEX_B,
                captured_generation=5,
                attempt_sequence=1,
                object_version_id="version-a",
                manifest_version=2,
                lease_fence=11,
            ),
            db_now_unix=2_010,
        )
        second = worker.prepare_attempt(
            replace(verified, dirty_generation=6),
            lease=lease(),
            prepared=prepared(
                generation=6,
                backup_id=BACKUP_B,
                object_key=OBJECT_B,
                digest=HEX_A,
                sequence=2,
            ),
            db_now_unix=2_011,
        )
        second = worker.mark_upload_started(
            second,
            lease=lease(),
            backup_id=BACKUP_B,
            db_now_unix=2_012,
        )
        second = worker.record_upload_outcome(
            second,
            lease=lease(),
            backup_id=BACKUP_B,
            outcome=worker.UploadOutcome.APPLIED,
            db_now_unix=2_013,
            object_key=OBJECT_B,
            object_version_id="version-a",
        )
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:verification-mismatch$",
        ):
            worker.record_readback(
                second,
                lease=lease(),
                result=worker.ReadbackResult(
                    status=worker.ReadbackStatus.VERIFIED,
                    backup_id=BACKUP_B,
                    object_key=OBJECT_B,
                    object_digest=HEX_A,
                    captured_generation=6,
                    attempt_sequence=2,
                    object_version_id="version-b",
                    manifest_version=2,
                    lease_fence=11,
                ),
                db_now_unix=2_014,
            )

    def test_manifest_identity_and_object_key_contract_are_shared_with_verifier(self):
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:invalid-state$",
        ):
            worker.PreparedBackup(
                backup_id="not-random",
                object_key="private/cluster-a/g-5/a-1-not-random.tar.gpg",
                object_digest=HEX_A,
                captured_generation=5,
                attempt_sequence=1,
            )
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:invalid-state$",
        ):
            worker.PreparedBackup(
                backup_id="c" * 32,
                object_key="private/cluster-a/unbound-object.tar.gpg",
                object_digest=HEX_A,
                captured_generation=5,
                attempt_sequence=1,
            )

    def test_legacy_unbound_verification_never_makes_rpo_green(self):
        legacy = state(dirty=3, verified=3, verified_at=1_000)
        self.assertEqual(
            worker.decide(
                legacy,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=1_001,
            ),
            worker.Decision(worker.WorkerAction.CREATE, "unbound"),
        )
        self.assertEqual(
            worker.backup_health(legacy, db_now_unix=1_001),
            worker.BackupHealth.RED_NEVER_VERIFIED,
        )

    def test_never_verified_zero_generation_is_distinct_and_backupable(self):
        never = worker.BackupState(7, 0, 0, None, None)
        self.assertEqual(
            worker.decide(
                never,
                lease=lease(),
                capabilities=ready_capabilities(),
                db_now_unix=1,
            ),
            worker.Decision(worker.WorkerAction.CREATE, "never"),
        )
        zero_id = "c" * 32
        active = worker.prepare_attempt(
            never,
            lease=lease(),
            prepared=worker.PreparedBackup(
                backup_id=zero_id,
                object_key=bound_object_key(zero_id, 0, 1),
                object_digest=HEX_A,
                captured_generation=0,
                attempt_sequence=1,
            ),
            db_now_unix=1,
        )
        self.assertEqual(active.attempt.prepared.captured_generation, 0)

    def test_applied_upload_receipt_is_required_and_exact_version_is_bound(self):
        applying = applying_state(phase=worker.AttemptPhase.APPLYING)
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:verification-mismatch$",
        ):
            worker.record_upload_outcome(
                applying,
                lease=lease(),
                backup_id=BACKUP_A,
                outcome=worker.UploadOutcome.APPLIED,
                db_now_unix=2_010,
            )
        applied = worker.record_upload_outcome(
            applying,
            lease=lease(),
            backup_id=BACKUP_A,
            outcome=worker.UploadOutcome.APPLIED,
            object_key=bound_object_key(BACKUP_A, 5, 1),
            object_version_id="version-a",
            db_now_unix=2_010,
        )
        self.assertEqual(applied.attempt.uploaded_object_version_id, "version-a")
        with self.assertRaisesRegex(
            worker.BackupWorkerError,
            "^backup-worker:verification-mismatch$",
        ):
            worker.record_readback(
                applied,
                lease=lease(),
                result=worker.ReadbackResult(
                    status=worker.ReadbackStatus.VERIFIED,
                    backup_id=BACKUP_A,
                    object_key=OBJECT_A,
                    object_digest=HEX_B,
                    captured_generation=5,
                    attempt_sequence=1,
                    object_version_id="version-b",
                    manifest_version=2,
                    lease_fence=11,
                ),
                db_now_unix=2_011,
            )

    def test_verified_state_persists_signed_identity_version(self):
        current = applying_state(phase=worker.AttemptPhase.UNKNOWN)
        verified = worker.record_readback(
            current,
            lease=lease(),
            result=worker.ReadbackResult(
                status=worker.ReadbackStatus.VERIFIED,
                backup_id=BACKUP_A,
                object_key=OBJECT_A,
                object_digest=HEX_B,
                captured_generation=5,
                attempt_sequence=1,
                object_version_id="version-a",
                manifest_version=2,
                lease_fence=11,
            ),
            db_now_unix=2_011,
        )
        self.assertEqual(verified.verified_backup_id, BACKUP_A)
        self.assertEqual(verified.verified_manifest_version, 2)

    def test_invalid_state_and_boolean_integers_fail_closed(self):
        for create in (
            lambda: state(dirty=2, verified=3),
            lambda: worker.BackupState(7, True, 0, None, None),
            lambda: worker.PreparedBackup(BACKUP_A, OBJECT_A, HEX_A, True),
            lambda: worker.Lease(7, True, "holder", "token", 9),
        ):
            with self.subTest(create=create):
                with self.assertRaisesRegex(worker.BackupWorkerError, "^backup-worker:invalid-state$"):
                    create()


@unittest.skipIf(os.name == "nt", "POSIX ownership and mode contract")
class BackupWorkerRuntimeTests(unittest.TestCase):
    def test_cleanup_removes_only_stale_owned_marked_inactive_task_directories(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "runtime"
            root.mkdir(mode=0o700)
            os.chmod(root, 0o700)
            stale = worker.create_task_directory(root, "backup-stale")
            active = worker.create_task_directory(root, "backup-active")
            young = worker.create_task_directory(root, "backup-young")
            foreign = root / "foreign"
            foreign.mkdir(mode=0o700)
            os.chmod(foreign, 0o700)
            symlink = root / "task-backup-link"
            symlink.symlink_to(stale, target_is_directory=True)
            os.utime(stale, (1_000, 1_000), follow_symlinks=False)
            os.utime(active, (1_000, 1_000), follow_symlinks=False)
            os.utime(young, (9_900, 9_900), follow_symlinks=False)

            removed = worker.cleanup_stale_task_directories(
                root,
                active_backup_ids=frozenset({"backup-active"}),
                now_unix=10_000,
                stale_after_seconds=3_600,
            )
            self.assertEqual(removed, ("backup-stale",))
            self.assertFalse(stale.exists())
            self.assertTrue(active.exists())
            self.assertTrue(young.exists())
            self.assertTrue(foreign.exists())
            self.assertTrue(symlink.is_symlink())

    def test_runtime_root_task_directory_and_bundle_permissions_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "runtime"
            root.mkdir(mode=0o700)
            os.chmod(root, 0o700)
            task = worker.create_task_directory(root, BACKUP_A)
            self.assertEqual(stat.S_IMODE(task.stat().st_mode), 0o700)
            marker = task / worker.OWNER_MARKER
            self.assertEqual(stat.S_IMODE(marker.stat().st_mode), 0o600)
            bundle = task / worker.BUNDLE_NAME
            bundle.write_bytes(b"synthetic")
            os.chmod(bundle, 0o600)
            with worker.validate_private_bundle(task) as pinned:
                self.assertEqual(pinned.path, bundle)
                self.assertEqual(pinned.read(), b"synthetic")
