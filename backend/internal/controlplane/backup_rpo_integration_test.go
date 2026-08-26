//go:build rqlite_integration

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestBackupRPOIntegrationFailureMessageIsStageSpecificAndSafe(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(rqlite.Result{Rows: []map[string]any{{
				"last_attempt_sequence":   int64(7),
				"max_attempt_sequence":    int64(9),
				"expected_attempt_exists": int64(1),
			}}}),
		},
	}

	got := backupRPOIntegrationFailureMessage(
		context.Background(), db, "restart-unknown-concurrent-dirty", "register-attempt", &identity,
	)
	want := "case=restart-unknown-concurrent-dirty stage=register-attempt " +
		"last_sequence=7 max_sequence=9 expected_attempt=true"
	if got != want {
		t.Fatalf("failure message=%q, want %q", got, want)
	}
	if len(db.linearCalls) != 1 || len(db.linearCalls[0].statements) != 1 {
		t.Fatalf("linearizable fingerprint calls=%#v", db.linearCalls)
	}
	sql := db.linearCalls[0].statements[0].SQL
	for _, required := range []string{
		"last_attempt_sequence", "MAX(a.attempt_sequence)", "expected_attempt_exists",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("fingerprint SQL lacks %q: %s", required, sql)
		}
	}
	for _, secret := range []string{identity.LeaseToken, identity.ObjectKey, identity.ObjectSHA256} {
		if strings.Contains(got, secret) {
			t.Fatalf("failure message leaked attempt data: %q", got)
		}
	}
}

func TestBackupRPOIntegrationIsolationUsesUniqueEpochAndRestoresMigrationSeed(t *testing.T) {
	prepare := backupRPOIntegrationPrepareStatements()
	cleanup := backupRPOIntegrationCleanupStatements()
	if len(prepare) != 3 || len(cleanup) != 3 {
		t.Fatalf("prepare statements=%d cleanup statements=%d, want 3/3", len(prepare), len(cleanup))
	}
	if backupRPOIntegrationRestoreEpochFloor != 1_000_000 {
		t.Fatalf("restore epoch floor=%d, want 1000000", backupRPOIntegrationRestoreEpochFloor)
	}
	normalizeSQL := func(statements []rqlite.Statement) string {
		parts := make([]string, 0, len(statements))
		for _, statement := range statements {
			parts = append(parts, strings.Join(strings.Fields(strings.ToLower(statement.SQL)), " "))
		}
		return strings.Join(parts, "\n")
	}
	prepareSQL := normalizeSQL(prepare)
	for _, required := range []string{
		"delete from cluster_job_leases where job_name='backup-rpo'",
		"coalesce((select max(restore_epoch) from backup_rpo_attempts),0) >= ? then null",
		"select max(restore_epoch)+1 from backup_rpo_attempts",
		"dirty_generation=1", "verified_generation=0", "last_attempt_sequence=0", "phase='dirty'",
	} {
		if !strings.Contains(prepareSQL, required) {
			t.Fatalf("prepare SQL lacks %q: %s", required, prepareSQL)
		}
	}
	if got := fmt.Sprint(prepare[1].Args); got != "[9223372036854775807 1000000 1000000]" {
		t.Fatalf("guarded unique epoch args=%s", got)
	}
	cleanupSQL := normalizeSQL(cleanup)
	for _, required := range []string{
		"delete from cluster_job_leases where job_name='backup-rpo'",
		"restored_from_backup_sha256=null", "restore_epoch=?", "activated_at_unix=created_at_unix",
		"dirty_generation=1", "verified_generation=0", "verified_backup_id=null",
		"verified_object_key=null", "verified_object_sha256=null", "verified_object_version=null",
		"verified_size_bytes=null", "verified_manifest_version=null", "verified_at_unix=null",
		"last_attempt_sequence=0", "phase='dirty'",
	} {
		if !strings.Contains(cleanupSQL, required) {
			t.Fatalf("cleanup SQL lacks %q: %s", required, cleanupSQL)
		}
	}
	if got := fmt.Sprint(cleanup[1].Args); got != "[1]" {
		t.Fatalf("cleanup restore epoch args=%s", got)
	}
	if strings.Contains(cleanupSQL, "delete from backup_rpo_attempts") {
		t.Fatalf("cleanup bypasses append-only attempt evidence: %s", cleanupSQL)
	}
}

func TestBackupRPOIntegrationCommittedUnknownPreparationRestoresMigrationSeed(t *testing.T) {
	type seedState struct {
		restoreEpoch        int64
		dirtyGeneration     int64
		verifiedGeneration  int64
		lastAttemptSequence int64
		phase               string
	}
	seed := seedState{
		restoreEpoch: 1, dirtyGeneration: 1, verifiedGeneration: 0,
		lastAttemptSequence: 0, phase: "dirty",
	}
	current := seed
	var cleanups []func()
	var cleanupErrors []error
	requestNumber := 0
	db := &recordingRQLite{}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		requestNumber++
		switch requestNumber {
		case 1:
			if len(cleanups) != 1 {
				t.Errorf("cleanup registrations=%d before prepare request, want 1", len(cleanups))
			}
			current = seedState{
				restoreEpoch:    backupRPOIntegrationRestoreEpochFloor,
				dirtyGeneration: 1, verifiedGeneration: 0,
				lastAttemptSequence: 0, phase: "dirty",
			}
			return nil, &rqlite.TransportError{
				Operation: "request", UnknownOutcome: true,
				Err: errors.New("synthetic committed prepare ambiguity"),
			}
		case 2:
			want := backupRPOIntegrationCleanupStatements()
			if len(statements) != len(want) {
				t.Errorf("cleanup statements=%d, want %d", len(statements), len(want))
				return nil, nil
			}
			for i := range want {
				if statements[i].SQL != want[i].SQL || fmt.Sprint(statements[i].Args) != fmt.Sprint(want[i].Args) {
					t.Errorf("cleanup statement %d=%#v, want %#v", i, statements[i], want[i])
				}
			}
			current = seed
			return nil, nil
		default:
			t.Errorf("unexpected request %d", requestNumber)
			return nil, nil
		}
	}

	_, err := prepareBackupRPOIntegrationAfterMigrations(
		context.Background(), db,
		func(cleanup func()) { cleanups = append(cleanups, cleanup) },
		func(cleanupErr error) { cleanupErrors = append(cleanupErrors, cleanupErr) },
	)
	var transportErr *rqlite.TransportError
	if !errors.As(err, &transportErr) || !transportErr.UnknownOutcome {
		t.Fatalf("prepare error=%v, want committed unknown outcome", err)
	}
	if current.restoreEpoch != backupRPOIntegrationRestoreEpochFloor {
		t.Fatalf("committed prepare state=%#v", current)
	}
	if len(cleanups) != 1 {
		t.Fatalf("cleanup registrations=%d, want 1", len(cleanups))
	}
	cleanups[0]()
	if len(cleanupErrors) != 0 {
		t.Fatalf("cleanup errors=%v", cleanupErrors)
	}
	if current != seed {
		t.Fatalf("cleanup state=%#v, want exact seed %#v", current, seed)
	}
}

func TestBackupRPOIntegrationFailureTraceAnnotatesCleanupOnce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstStage  string
		secondStage string
		wantStage   string
	}{
		{name: "cleanup-only", firstStage: "cleanup", secondStage: "cleanup", wantStage: "cleanup"},
		{name: "body-and-cleanup", firstStage: "register-attempt", secondStage: "cleanup", wantStage: "register-attempt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &recordingRQLite{linear: []scriptedResult{
				resultsScript(rqlite.Result{Rows: []map[string]any{{
					"last_attempt_sequence": int64(0), "max_attempt_sequence": int64(0),
					"expected_attempt_exists": int64(0),
				}}}),
			}}
			var output strings.Builder
			trace := newBackupRPOIntegrationFailureTrace(t, db, "diagnostic-dedup")
			trace.annotationWriter = &output
			trace.annotateFailure(tc.firstStage, nil)
			trace.annotateFailure(tc.secondStage, nil)
			if got := strings.Count(output.String(), "::error title=Task 4 rqlite integration::"); got != 1 {
				t.Fatalf("annotations=%d output=%q, want 1", got, output.String())
			}
			if !strings.Contains(output.String(), "stage="+tc.wantStage+" ") {
				t.Fatalf("annotation=%q, want stage %q", output.String(), tc.wantStage)
			}
			if len(db.linearCalls) != 1 {
				t.Fatalf("fingerprint reads=%d, want 1", len(db.linearCalls))
			}
		})
	}
}

type backupRPOIntegrationFailureTrace struct {
	t                *testing.T
	db               rqlite.RQLite
	caseID           string
	stage            string
	identity         *BackupRPOAttemptIdentity
	annotationWriter io.Writer
	annotated        atomic.Bool
}

func newBackupRPOIntegrationFailureTrace(
	t *testing.T,
	db rqlite.RQLite,
	caseID string,
) *backupRPOIntegrationFailureTrace {
	t.Helper()
	return &backupRPOIntegrationFailureTrace{
		t: t, db: db, caseID: caseID, stage: "initialize", annotationWriter: os.Stderr,
	}
}

func (trace *backupRPOIntegrationFailureTrace) setStage(
	stage string,
	identity *BackupRPOAttemptIdentity,
) {
	trace.stage = stage
	trace.identity = identity
}

func (trace *backupRPOIntegrationFailureTrace) report() {
	if !trace.t.Failed() {
		return
	}
	trace.annotateFailure(trace.stage, trace.identity)
}

func (trace *backupRPOIntegrationFailureTrace) annotateFailure(
	stage string,
	identity *BackupRPOAttemptIdentity,
) {
	if !trace.annotated.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writer := trace.annotationWriter
	if writer == nil {
		writer = os.Stderr
	}
	fmt.Fprintf(writer, "::error title=Task 4 rqlite integration::%s\n",
		backupRPOIntegrationFailureMessage(ctx, trace.db, trace.caseID, stage, identity))
}

func backupRPOIntegrationFailureMessage(
	ctx context.Context,
	db rqlite.RQLite,
	caseID string,
	stage string,
	identity *BackupRPOAttemptIdentity,
) string {
	restoreEpoch := int64(-1)
	attemptSequence := int64(-1)
	expectedAttempt := "unset"
	if identity != nil {
		restoreEpoch = identity.RestoreEpoch
		attemptSequence = identity.AttemptSequence
		expectedAttempt = "unavailable"
	}
	lastSequence := "unavailable"
	maxSequence := "unavailable"
	results, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT
		b.last_attempt_sequence,
		COALESCE((
			SELECT MAX(a.attempt_sequence)
			FROM backup_rpo_attempts a
			WHERE a.restore_epoch=b.restore_epoch
		),0) AS max_attempt_sequence,
		CASE WHEN EXISTS (
			SELECT 1 FROM backup_rpo_attempts expected
			WHERE expected.restore_epoch=? AND expected.attempt_sequence=?
		) THEN 1 ELSE 0 END AS expected_attempt_exists
	FROM backup_rpo_state b
	WHERE b.singleton_id=1`, Args: []any{restoreEpoch, attemptSequence}})
	if err == nil && len(results) == 1 && len(results[0].Rows) == 1 {
		row := results[0].Rows[0]
		last, lastOK := strictBackupRPOIntegerAt(row, "last_attempt_sequence")
		maximum, maxOK := strictBackupRPOIntegerAt(row, "max_attempt_sequence")
		exists, existsOK := strictBackupRPOIntegerAt(row, "expected_attempt_exists")
		if lastOK && maxOK && existsOK && (exists == 0 || exists == 1) {
			lastSequence = fmt.Sprintf("%d", last)
			maxSequence = fmt.Sprintf("%d", maximum)
			if identity != nil {
				expectedAttempt = fmt.Sprintf("%t", exists == 1)
			}
		}
	}
	return fmt.Sprintf("case=%s stage=%s last_sequence=%s max_sequence=%s expected_attempt=%s",
		caseID, stage, lastSequence, maxSequence, expectedAttempt)
}

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
	trace := newBackupRPOIntegrationFailureTrace(t, db, "restart-unknown-concurrent-dirty")
	defer trace.report()
	trace.setStage("prepare", nil)
	state := prepareBackupRPOIntegration(t, ctx, db, trace)
	trace.setStage("acquire-lease", nil)
	leaseRequest := integrationBackupRPOLeaseRequest(state, "node-s2", "attempt-lifecycle-lease", 0)
	lease, err := NewBackupRPOStore(db).AcquireLease(ctx, leaseRequest)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	identity := integrationBackupRPOAttemptIdentity(state, lease)
	trace.setStage("register-attempt", &identity)
	if _, err := NewBackupRPOStore(db).RegisterAttempt(ctx, identity); err != nil {
		t.Fatalf("RegisterAttempt: %v", err)
	}
	second := identity
	second.AttemptSequence++
	second.BackupID = fmt.Sprintf("%032x", second.AttemptSequence)
	second.ObjectKey = fmt.Sprintf("backups/g-%d/a-%d-%s.tar.gpg", second.CapturedGeneration, second.AttemptSequence, second.BackupID)
	trace.setStage("reject-second-active", &second)
	if _, err := NewBackupRPOStore(db).RegisterAttempt(ctx, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second active attempt error=%v, want ErrConflict", err)
	}

	unknownDB := &committedUnknownRQLite{delegate: db}
	trace.setStage("mark-upload-started-unknown-outcome", &identity)
	started, err := NewBackupRPOStore(unknownDB).MarkUploadStarted(ctx, identity)
	if err != nil || started.Phase != BackupRPOAttemptApplying {
		t.Fatalf("committed-unknown start=%#v error=%v", started, err)
	}
	trace.setStage("verify-unknown-read-count", &identity)
	if unknownDB.requestCalls.Load() != 1 || unknownDB.linearCalls.Load() != 1 {
		t.Fatalf("unknown start requests=%d reads=%d", unknownDB.requestCalls.Load(), unknownDB.linearCalls.Load())
	}
	trace.setStage("record-unknown-upload", &identity)
	if _, err := NewBackupRPOStore(db).RecordUploadOutcome(ctx, BackupRPOUploadOutcome{
		Identity: identity, Unknown: true,
	}); err != nil {
		t.Fatalf("RecordUploadOutcome unknown: %v", err)
	}
	trace.setStage("reject-upload-restart", &identity)
	if _, err := NewBackupRPOStore(db).MarkUploadStarted(ctx, identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown attempt upload restart error=%v, want ErrConflict", err)
	}
	trace.setStage("reconcile-upload", &identity)
	if _, err := NewBackupRPOStore(db).RecordUploadOutcome(ctx, BackupRPOUploadOutcome{
		Identity: identity, VersionID: mustIntegrationBackupRPOVersionID(t, "version-integration-9"),
	}); err != nil {
		t.Fatalf("RecordUploadOutcome reconciled: %v", err)
	}
	trace.setStage("bump-concurrent-dirty", &identity)
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE backup_rpo_state SET dirty_generation=dirty_generation+1,phase='dirty',updated_at_unix=unixepoch()
		WHERE singleton_id=1 AND restore_epoch=?`, Args: []any{identity.RestoreEpoch}}); err != nil {
		t.Fatalf("concurrent dirty bump: %v", err)
	}
	trace.setStage("acknowledge-verified", &identity)
	proof := integrationBackupRPOVerification(t, identity, "version-integration-9")
	if _, err := NewBackupRPOStore(db).AcknowledgeVerified(ctx, proof); err != nil {
		t.Fatalf("AcknowledgeVerified: %v", err)
	}
	trace.setStage("read-current", &identity)
	current, err := NewBackupRPOStore(db).Current(ctx)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	trace.setStage("verify-concurrent-dirty", &identity)
	if current.VerifiedGeneration != identity.CapturedGeneration ||
		current.DirtyGeneration != identity.CapturedGeneration+1 || current.Phase != BackupRPOPhaseDirty {
		t.Fatalf("concurrent state=%#v", current)
	}
}

func TestBackupRPOAttemptIntegrationNewerFenceSupersedesStaleAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := mustIntegrationRQLite(t)
	trace := newBackupRPOIntegrationFailureTrace(t, db, "newer-fence-supersedes-stale")
	defer trace.report()
	trace.setStage("prepare", nil)
	state := prepareBackupRPOIntegration(t, ctx, db, trace)
	trace.setStage("acquire-first-lease", nil)
	firstRequest := integrationBackupRPOLeaseRequest(state, "node-s2", "stale-attempt-lease", 0)
	first, err := NewBackupRPOStore(db).AcquireLease(ctx, firstRequest)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	identity := integrationBackupRPOAttemptIdentity(state, first)
	trace.setStage("register-attempt", &identity)
	if _, err := NewBackupRPOStore(db).RegisterAttempt(ctx, identity); err != nil {
		t.Fatalf("RegisterAttempt: %v", err)
	}
	trace.setStage("mark-upload-started", &identity)
	if _, err := NewBackupRPOStore(db).MarkUploadStarted(ctx, identity); err != nil {
		t.Fatalf("MarkUploadStarted: %v", err)
	}
	trace.setStage("record-upload-outcome", &identity)
	if _, err := NewBackupRPOStore(db).RecordUploadOutcome(ctx, BackupRPOUploadOutcome{
		Identity: identity, VersionID: mustIntegrationBackupRPOVersionID(t, "version-stale-applied"),
	}); err != nil {
		t.Fatalf("RecordUploadOutcome: %v", err)
	}
	trace.setStage("expire-stale-lease", &identity)
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
		UPDATE cluster_job_leases SET acquired_at_unix=unixepoch()-1,expires_at_unix=unixepoch()
		WHERE job_name='backup-rpo' AND lease_fence=?`, Args: []any{first.LeaseFence}}); err != nil {
		t.Fatalf("expire stale lease: %v", err)
	}
	currentRequest := integrationBackupRPOLeaseRequest(
		state, "node-s3", "newer-attempt-lease", first.LeaseFence,
	)
	trace.setStage("acquire-newer-lease", &identity)
	current, err := NewBackupRPOStore(db).AcquireLease(ctx, currentRequest)
	if err != nil {
		t.Fatalf("newer AcquireLease: %v", err)
	}
	currentRequest.ExpectedFence = current.LeaseFence
	trace.setStage("supersede-stale-attempt", &identity)
	attempt, err := NewBackupRPOStore(db).SupersedeStaleAttempt(ctx, BackupRPOSupersedeRequest{
		Identity: identity, CurrentLease: currentRequest,
	})
	if err != nil {
		t.Fatalf("SupersedeStaleAttempt: %v", err)
	}
	trace.setStage("verify-superseded", &identity)
	if attempt.Phase != BackupRPOAttemptSuperseded ||
		attempt.FailureCode != BackupRPOFailureStaleFence {
		t.Fatalf("superseded=%#v", attempt)
	}
}

func integrationBackupRPOAttemptIdentity(state BackupRPOState, lease BackupRPOLease) BackupRPOAttemptIdentity {
	sequence := state.LastAttemptSequence + 1
	backupID := fmt.Sprintf("%032x", sequence)
	return BackupRPOAttemptIdentity{
		HolderID: lease.HolderID, LeaseToken: lease.LeaseToken,
		RestoreEpoch: lease.RestoreEpoch, LeaseFence: lease.LeaseFence,
		Capability: lease.Capability, CapturedGeneration: state.DirtyGeneration,
		AttemptSequence: sequence, BackupID: backupID,
		ObjectKey:    fmt.Sprintf("backups/g-%d/a-%d-%s.tar.gpg", state.DirtyGeneration, sequence, backupID),
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
		ManifestRestoreEpoch:       identity.RestoreEpoch,
		ManifestCapturedGeneration: identity.CapturedGeneration,
		ManifestObjectKey:          identity.ObjectKey, ManifestObjectSHA256: identity.ObjectSHA256,
		ManifestObjectSizeBytes: identity.ObjectSizeBytes,
	}
}

const (
	backupRPOIntegrationRestoreEpochFloor int64 = 1_000_000
	backupRPOIntegrationMaxRestoreEpoch   int64 = 1<<63 - 1
)

func backupRPOIntegrationSeedStateStatement() rqlite.Statement {
	return rqlite.Statement{SQL: `UPDATE backup_rpo_state
		SET restore_epoch=(SELECT restore_epoch FROM cluster_restore_state WHERE singleton_id=1),
			dirty_generation=1,
			verified_generation=0,
			verified_backup_id=NULL,
			verified_object_key=NULL,
			verified_object_sha256=NULL,
			verified_object_version=NULL,
			verified_size_bytes=NULL,
			verified_manifest_version=NULL,
			verified_at_unix=NULL,
			last_attempt_sequence=0,
			phase='dirty',
			updated_at_unix=unixepoch()
		WHERE singleton_id=1`}
}

func backupRPOIntegrationPrepareStatements() []rqlite.Statement {
	return []rqlite.Statement{
		{SQL: "DELETE FROM cluster_job_leases WHERE job_name='backup-rpo'"},
		{SQL: `UPDATE cluster_restore_state
			SET restore_epoch=CASE
				WHEN COALESCE((SELECT MAX(restore_epoch) FROM backup_rpo_attempts),0) >= ? THEN NULL
				ELSE MAX(
					?,
					COALESCE((SELECT MAX(restore_epoch)+1 FROM backup_rpo_attempts),?)
				)
			END,
				restored_from_backup_sha256=NULL,
				activated=1,
				activated_at_unix=COALESCE(activated_at_unix,unixepoch())
			WHERE singleton_id=1`, Args: []any{
			backupRPOIntegrationMaxRestoreEpoch,
			backupRPOIntegrationRestoreEpochFloor,
			backupRPOIntegrationRestoreEpochFloor,
		}},
		backupRPOIntegrationSeedStateStatement(),
	}
}

func backupRPOIntegrationCleanupStatements() []rqlite.Statement {
	return []rqlite.Statement{
		{SQL: "DELETE FROM cluster_job_leases WHERE job_name='backup-rpo'"},
		{SQL: `UPDATE cluster_restore_state
			SET restore_epoch=?,
				restored_from_backup_sha256=NULL,
				activated=1,
				activated_at_unix=created_at_unix
			WHERE singleton_id=1`, Args: []any{int64(1)}},
		backupRPOIntegrationSeedStateStatement(),
	}
}

func prepareBackupRPOIntegration(
	t *testing.T,
	ctx context.Context,
	db rqlite.RQLite,
	traces ...*backupRPOIntegrationFailureTrace,
) BackupRPOState {
	t.Helper()
	if err := NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("Apply migrations: %v", err)
	}
	cleanupTrace := newBackupRPOIntegrationFailureTrace(t, db, t.Name())
	if len(traces) == 1 && traces[0] != nil {
		cleanupTrace = traces[0]
	}
	state, err := prepareBackupRPOIntegrationAfterMigrations(
		ctx, db, t.Cleanup,
		func(cleanupErr error) {
			t.Errorf("cleanup backup RPO integration state: %v", cleanupErr)
			cleanupTrace.annotateFailure("cleanup", nil)
		},
	)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return state
}

func prepareBackupRPOIntegrationAfterMigrations(
	ctx context.Context,
	db rqlite.RQLite,
	registerCleanup func(func()),
	reportCleanupFailure func(error),
) (BackupRPOState, error) {
	registerCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.Request(
			cleanupCtx, rqlite.Linearizable, true, backupRPOIntegrationCleanupStatements()...,
		); err != nil {
			reportCleanupFailure(err)
		}
	})
	if _, err := db.Request(
		ctx, rqlite.Linearizable, true, backupRPOIntegrationPrepareStatements()...,
	); err != nil {
		return BackupRPOState{}, fmt.Errorf("prepare backup RPO state: %w", err)
	}
	state, err := NewBackupRPOStore(db).Current(ctx)
	if err != nil {
		return BackupRPOState{}, fmt.Errorf("Current: %w", err)
	}
	return state, nil
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
