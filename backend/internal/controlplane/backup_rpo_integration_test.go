//go:build rqlite_integration

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestBackupRPOLeaseIntegrationTakeoverBoundaryAndNodeHandoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := mustIntegrationRQLite(t)
	state := prepareBackupRPOIntegration(t, ctx, db)

	firstRequest := integrationBackupRPOLeaseRequest(state, "node-s2", "lease-token-integration-a", 0)
	first, err := NewBackupRPOStore(db).AcquireLease(ctx, firstRequest)
	if err != nil || first.LeaseFence != 1 {
		t.Fatalf("first lease=%#v error=%v", first, err)
	}
	renewed, err := NewBackupRPOStore(db).RenewLease(ctx, BackupRPOLeaseRequest{
		HolderID: first.HolderID, LeaseToken: first.LeaseToken, RestoreEpoch: first.RestoreEpoch,
		ExpectedFence: first.LeaseFence, TTLSeconds: 60, Capability: first.Capability,
	})
	if err != nil || renewed.LeaseFence != first.LeaseFence {
		t.Fatalf("renewed lease=%#v error=%v", renewed, err)
	}

	_, err = db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE cluster_job_leases
		SET acquired_at_unix=unixepoch()-1,expires_at_unix=unixepoch()
		WHERE job_name='backup-rpo' AND lease_fence=?`, Args: []any{first.LeaseFence}})
	if err != nil {
		t.Fatalf("expire lease at DB-time boundary: %v", err)
	}

	handoffRequest := integrationBackupRPOLeaseRequest(state, "node-s3", "lease-token-integration-b", first.LeaseFence)
	handoff, err := NewBackupRPOStore(db).AcquireLease(ctx, handoffRequest)
	if err != nil {
		t.Fatalf("handoff AcquireLease: %v", err)
	}
	if handoff.HolderID != "node-s3" || handoff.LeaseFence != first.LeaseFence+1 {
		t.Fatalf("handoff=%#v", handoff)
	}
}

func TestBackupRPOLeaseIntegrationCapabilityExpiryAndRestoreActivation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := mustIntegrationRQLite(t)
	state := prepareBackupRPOIntegration(t, ctx, db)

	expiredCapability := integrationBackupRPOLeaseRequest(state, "node-s2", "lease-token-expired-capability", 0)
	expiredCapability.Capability.ExpiresAtUnix = state.DatabaseNowUnix
	if _, err := NewBackupRPOStore(db).AcquireLease(ctx, expiredCapability); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired capability error=%v, want ErrConflict", err)
	}

	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE cluster_restore_state SET activated=0,activated_at_unix=NULL
		WHERE singleton_id=1 AND restore_epoch=?`, Args: []any{state.RestoreEpoch}}); err != nil {
		t.Fatalf("deactivate restore epoch: %v", err)
	}
	liveCapability := integrationBackupRPOLeaseRequest(state, "node-s2", "lease-token-inactive-restore", 0)
	if _, err := NewBackupRPOStore(db).AcquireLease(ctx, liveCapability); !errors.Is(err, ErrConflict) {
		t.Fatalf("inactive restore error=%v, want ErrConflict", err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE cluster_restore_state SET activated=1,activated_at_unix=unixepoch()
		WHERE singleton_id=1 AND restore_epoch=? AND activated=0`, Args: []any{state.RestoreEpoch}}); err != nil {
		t.Fatalf("reactivate restore epoch: %v", err)
	}
	active, err := NewBackupRPOStore(db).AcquireLease(ctx, liveCapability)
	if err != nil || active.RestoreEpoch != state.RestoreEpoch || active.LeaseFence != 1 {
		t.Fatalf("active lease=%#v error=%v", active, err)
	}
}

func TestBackupRPOLeaseIntegrationUnknownOutcomeUsesOneEvidenceRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	delegate := mustIntegrationRQLite(t)
	state := prepareBackupRPOIntegration(t, ctx, delegate)
	db := &committedUnknownRQLite{delegate: delegate}
	request := integrationBackupRPOLeaseRequest(state, "node-s4", "lease-token-unknown-outcome", 0)

	lease, err := NewBackupRPOStore(db).AcquireLease(ctx, request)
	if err != nil || lease.HolderID != request.HolderID || lease.LeaseFence != 1 {
		t.Fatalf("lease=%#v error=%v", lease, err)
	}
	if db.requestCalls.Load() != 1 || db.linearCalls.Load() != 1 {
		t.Fatalf("requests=%d reads=%d, want 1/1", db.requestCalls.Load(), db.linearCalls.Load())
	}
}

func TestBackupRPOAttemptIntegrationRestartUnknownConcurrentDirtyAndOneActive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := mustIntegrationRQLite(t)
	state := prepareBackupRPOIntegration(t, ctx, db)
	leaseRequest := integrationBackupRPOLeaseRequest(state, "node-s2", "attempt-lifecycle-lease", 0)
	lease, err := NewBackupRPOStore(db).AcquireLease(ctx, leaseRequest)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	identity := integrationBackupRPOAttemptIdentity(state, lease)
	if _, err := NewBackupRPOStore(db).RegisterAttempt(ctx, identity); err != nil {
		t.Fatalf("RegisterAttempt: %v", err)
	}
	second := identity
	second.AttemptSequence++
	second.BackupID = fmt.Sprintf("%032x", second.AttemptSequence)
	second.ObjectKey = fmt.Sprintf("backups/g-%d/a-%d.tar.gpg", second.CapturedGeneration, second.AttemptSequence)
	if _, err := NewBackupRPOStore(db).RegisterAttempt(ctx, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second active attempt error=%v, want ErrConflict", err)
	}

	unknownDB := &committedUnknownRQLite{delegate: db}
	started, err := NewBackupRPOStore(unknownDB).MarkUploadStarted(ctx, identity)
	if err != nil || started.Phase != BackupRPOAttemptApplying {
		t.Fatalf("committed-unknown start=%#v error=%v", started, err)
	}
	if unknownDB.requestCalls.Load() != 1 || unknownDB.linearCalls.Load() != 1 {
		t.Fatalf("unknown start requests=%d reads=%d", unknownDB.requestCalls.Load(), unknownDB.linearCalls.Load())
	}
	if _, err := NewBackupRPOStore(db).RecordUploadOutcome(ctx, BackupRPOUploadOutcome{
		Identity: identity, Unknown: true,
	}); err != nil {
		t.Fatalf("RecordUploadOutcome unknown: %v", err)
	}
	if _, err := NewBackupRPOStore(db).MarkUploadStarted(ctx, identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown attempt upload restart error=%v, want ErrConflict", err)
	}
	if _, err := NewBackupRPOStore(db).RecordUploadOutcome(ctx, BackupRPOUploadOutcome{
		Identity: identity, VersionID: mustIntegrationBackupRPOVersionID(t, "version-integration-9"),
	}); err != nil {
		t.Fatalf("RecordUploadOutcome reconciled: %v", err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE backup_rpo_state SET dirty_generation=dirty_generation+1,phase='dirty',updated_at_unix=unixepoch()
		WHERE singleton_id=1 AND restore_epoch=?`, Args: []any{identity.RestoreEpoch}}); err != nil {
		t.Fatalf("concurrent dirty bump: %v", err)
	}
	proof := integrationBackupRPOVerification(t, identity, "version-integration-9")
	if _, err := NewBackupRPOStore(db).AcknowledgeVerified(ctx, proof); err != nil {
		t.Fatalf("AcknowledgeVerified: %v", err)
	}
	current, err := NewBackupRPOStore(db).Current(ctx)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.VerifiedGeneration != identity.CapturedGeneration ||
		current.DirtyGeneration != identity.CapturedGeneration+1 || current.Phase != BackupRPOPhaseDirty {
		t.Fatalf("concurrent state=%#v", current)
	}
}

func TestBackupRPOAttemptIntegrationNewerFenceSupersedesStaleAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := mustIntegrationRQLite(t)
	state := prepareBackupRPOIntegration(t, ctx, db)
	firstRequest := integrationBackupRPOLeaseRequest(state, "node-s2", "stale-attempt-lease", 0)
	first, err := NewBackupRPOStore(db).AcquireLease(ctx, firstRequest)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	identity := integrationBackupRPOAttemptIdentity(state, first)
	if _, err := NewBackupRPOStore(db).RegisterAttempt(ctx, identity); err != nil {
		t.Fatalf("RegisterAttempt: %v", err)
	}
	if _, err := NewBackupRPOStore(db).MarkUploadStarted(ctx, identity); err != nil {
		t.Fatalf("MarkUploadStarted: %v", err)
	}
	if _, err := NewBackupRPOStore(db).RecordUploadOutcome(ctx, BackupRPOUploadOutcome{
		Identity: identity, VersionID: mustIntegrationBackupRPOVersionID(t, "version-stale-applied"),
	}); err != nil {
		t.Fatalf("RecordUploadOutcome: %v", err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE cluster_job_leases SET acquired_at_unix=unixepoch()-1,expires_at_unix=unixepoch()
		WHERE job_name='backup-rpo' AND lease_fence=?`, Args: []any{first.LeaseFence}}); err != nil {
		t.Fatalf("expire stale lease: %v", err)
	}
	currentRequest := integrationBackupRPOLeaseRequest(
		state, "node-s3", "newer-attempt-lease", first.LeaseFence,
	)
	current, err := NewBackupRPOStore(db).AcquireLease(ctx, currentRequest)
	if err != nil {
		t.Fatalf("newer AcquireLease: %v", err)
	}
	currentRequest.ExpectedFence = current.LeaseFence
	attempt, err := NewBackupRPOStore(db).SupersedeStaleAttempt(ctx, BackupRPOSupersedeRequest{
		Identity: identity, CurrentLease: currentRequest,
	})
	if err != nil || attempt.Phase != BackupRPOAttemptSuperseded ||
		attempt.FailureCode != BackupRPOFailureStaleFence {
		t.Fatalf("superseded=%#v error=%v", attempt, err)
	}
}

func integrationBackupRPOAttemptIdentity(state BackupRPOState, lease BackupRPOLease) BackupRPOAttemptIdentity {
	sequence := state.LastAttemptSequence + 1
	return BackupRPOAttemptIdentity{
		HolderID: lease.HolderID, LeaseToken: lease.LeaseToken,
		RestoreEpoch: lease.RestoreEpoch, LeaseFence: lease.LeaseFence,
		Capability: lease.Capability, CapturedGeneration: state.DirtyGeneration,
		AttemptSequence: sequence, BackupID: fmt.Sprintf("%032x", sequence),
		ObjectKey:    fmt.Sprintf("backups/g-%d/a-%d.tar.gpg", state.DirtyGeneration, sequence),
		ObjectSHA256: testBackupRPOObjectDigest, ObjectSizeBytes: 4096,
		ManifestVersion: 2, AdapterContractVersion: BackupRPOAdapterYandexS3V1,
	}
}

func mustIntegrationBackupRPOVersionID(t *testing.T, value string) BackupRPOVersionID {
	t.Helper()
	versionID, err := NewBackupRPOVersionID(value)
	if err != nil {
		t.Fatalf("NewBackupRPOVersionID: %v", err)
	}
	return versionID
}

func integrationBackupRPOVerification(t *testing.T, identity BackupRPOAttemptIdentity, version string) BackupRPOVerification {
	t.Helper()
	return BackupRPOVerification{
		Identity: identity, VersionID: mustIntegrationBackupRPOVersionID(t, version), FullReadback: true,
		ReadbackSHA256: identity.ObjectSHA256, ReadbackSizeBytes: identity.ObjectSizeBytes,
		ManifestAuthenticated: true, ManifestVersion: identity.ManifestVersion,
		ManifestBackupID:           identity.BackupID,
		ManifestCapturedGeneration: identity.CapturedGeneration,
		ManifestObjectKey:          identity.ObjectKey, ManifestObjectSHA256: identity.ObjectSHA256,
		ManifestObjectSizeBytes: identity.ObjectSizeBytes,
	}
}

func prepareBackupRPOIntegration(t *testing.T, ctx context.Context, db rqlite.RQLite) BackupRPOState {
	t.Helper()
	if err := NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("Apply migrations: %v", err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: "DELETE FROM cluster_job_leases WHERE job_name='backup-rpo'"},
		rqlite.Statement{SQL: `UPDATE cluster_restore_state
			SET activated=1,activated_at_unix=COALESCE(activated_at_unix,unixepoch())
			WHERE singleton_id=1`},
		rqlite.Statement{SQL: `UPDATE backup_rpo_state
			SET restore_epoch=(SELECT restore_epoch FROM cluster_restore_state WHERE singleton_id=1),
				dirty_generation=CASE
					WHEN dirty_generation=verified_generation THEN dirty_generation+1
					ELSE dirty_generation
				END,
				phase='dirty',updated_at_unix=unixepoch()
			WHERE singleton_id=1`},
	); err != nil {
		t.Fatalf("prepare backup RPO state: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.Request(cleanupCtx, rqlite.Linearizable, true,
			rqlite.Statement{SQL: "DELETE FROM cluster_job_leases WHERE job_name='backup-rpo'"},
			rqlite.Statement{SQL: `UPDATE cluster_restore_state
				SET activated=1,activated_at_unix=COALESCE(activated_at_unix,unixepoch())
				WHERE singleton_id=1`},
		); err != nil {
			t.Errorf("cleanup backup RPO integration state: %v", err)
		}
	})
	state, err := NewBackupRPOStore(db).Current(ctx)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	return state
}

func integrationBackupRPOLeaseRequest(
	state BackupRPOState,
	holderID string,
	leaseToken string,
	expectedFence int64,
) BackupRPOLeaseRequest {
	return BackupRPOLeaseRequest{
		HolderID: holderID, LeaseToken: leaseToken, RestoreEpoch: state.RestoreEpoch,
		ExpectedFence: expectedFence, TTLSeconds: 60,
		Capability: BackupRPOCapability{
			Generation: 1, EvidenceSHA256: testBackupRPOCapabilityDigest,
			ExpiresAtUnix: state.DatabaseNowUnix + 600,
		},
	}
}

type committedUnknownRQLite struct {
	delegate     rqlite.RQLite
	requestCalls atomic.Int64
	linearCalls  atomic.Int64
}

func (db *committedUnknownRQLite) Request(
	ctx context.Context,
	level rqlite.Consistency,
	transaction bool,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	db.requestCalls.Add(1)
	if _, err := db.delegate.Request(ctx, level, transaction, statements...); err != nil {
		return nil, err
	}
	return nil, &rqlite.TransportError{
		Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic committed ambiguity"),
	}
}

func (db *committedUnknownRQLite) QueryLinearizable(
	ctx context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	db.linearCalls.Add(1)
	return db.delegate.QueryLinearizable(ctx, statements...)
}

func (db *committedUnknownRQLite) QueryStrong(
	ctx context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	return db.delegate.QueryStrong(ctx, statements...)
}

func (db *committedUnknownRQLite) Backup(ctx context.Context, writer io.Writer) error {
	return db.delegate.Backup(ctx, writer)
}
