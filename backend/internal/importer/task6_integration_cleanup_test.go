//go:build rqlite_integration

package importer

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type task6BackupRPOState struct {
	RestoreEpoch            int64
	DirtyGeneration         int64
	VerifiedGeneration      int64
	VerifiedBackupID        any
	VerifiedObjectKey       any
	VerifiedObjectSHA256    any
	VerifiedObjectVersion   any
	VerifiedSizeBytes       any
	VerifiedManifestVersion any
	VerifiedAtUnix          any
	LastAttemptSequence     int64
	Phase                   string
	UpdatedAtUnix           int64
}

type task6ImportRunReceipt struct {
	RunID        string
	SourceDigest string
	PlanDigest   string
	Status       string
}

type task6BackupRPOCleanupExpectation struct {
	DirtyGenerationDelta int64
	UpdatedAtUnix        int64
	Receipt              task6ImportRunReceipt
}

type task6IntegrationBackupCleanup struct {
	t           *testing.T
	db          rqlite.RQLite
	baseline    task6BackupRPOState
	owner       task6BackupRPOLockIdentity
	expectation *task6BackupRPOCleanupExpectation
}

func task6CaptureIntegrationBackupCleanup(t *testing.T, db rqlite.RQLite) *task6IntegrationBackupCleanup {
	t.Helper()
	owner := task6BackupRPOIntegrationLeaseIdentity
	if !owner.Valid() {
		t.Fatal("rqlite integration backup cleanup lacks an active package lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	baseline, err := task6ReadIntegrationBackupState(ctx, db)
	cancel()
	if err != nil {
		t.Fatalf("capture backup RPO cleanup baseline: %v", err)
	}
	cleanup := &task6IntegrationBackupCleanup{t: t, db: db, baseline: baseline, owner: owner}
	t.Cleanup(cleanup.run)
	return cleanup
}

func (cleanup *task6IntegrationBackupCleanup) Expect(expectation task6BackupRPOCleanupExpectation) {
	cleanup.t.Helper()
	if cleanup.expectation != nil || expectation.DirtyGenerationDelta <= 0 || expectation.UpdatedAtUnix <= 0 ||
		strings.TrimSpace(expectation.Receipt.RunID) != expectation.Receipt.RunID || expectation.Receipt.RunID == "" ||
		!validCanonicalSHA256(expectation.Receipt.SourceDigest) ||
		!validCanonicalSHA256(expectation.Receipt.PlanDigest) ||
		(expectation.Receipt.Status != "applying" && expectation.Receipt.Status != "applied") {
		cleanup.t.Fatal("invalid backup RPO cleanup ownership expectation")
	}
	copy := expectation
	cleanup.expectation = &copy
}

func (cleanup *task6IntegrationBackupCleanup) run() {
	cleanup.t.Helper()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if cleanup.expectation == nil {
		current, err := task6ReadIntegrationBackupState(cleanupCtx, cleanup.db)
		if err != nil {
			cleanup.t.Errorf("verify untouched backup RPO baseline: %v", err)
			return
		}
		if current != cleanup.baseline {
			cleanup.t.Errorf("backup RPO state changed before cleanup ownership was established: %s", current.summary())
		}
		return
	}
	statement := task6IntegrationBackupCleanupStatement(cleanup.baseline, *cleanup.expectation, cleanup.owner)
	results, requestErr := cleanup.db.Request(cleanupCtx, rqlite.Linearizable, true, statement)
	if requestErr == nil && len(results) == 1 && len(results[0].Rows) == 1 {
		restored, err := task6BackupRPOStateFromRow(results[0].Rows[0])
		if err == nil && restored == cleanup.baseline {
			return
		}
		cleanup.t.Errorf("backup RPO cleanup returned malformed baseline: %v", err)
		return
	}
	current, readErr := task6ReadIntegrationBackupState(cleanupCtx, cleanup.db)
	if readErr == nil && current == cleanup.baseline {
		return
	}
	if readErr != nil {
		cleanup.t.Errorf("backup RPO cleanup failed without resolvable state: request=%v read=%v", requestErr, readErr)
		return
	}
	cleanup.t.Errorf(
		"backup RPO cleanup refused non-owned postcondition: request=%v results=%d current=%s",
		requestErr, len(results), current.summary(),
	)
}

func task6IntegrationBackupCleanupStatement(
	baseline task6BackupRPOState,
	expectation task6BackupRPOCleanupExpectation,
	owner task6BackupRPOLockIdentity,
) rqlite.Statement {
	expected := baseline
	expected.DirtyGeneration += expectation.DirtyGenerationDelta
	expected.Phase = "dirty"
	expected.UpdatedAtUnix = expectation.UpdatedAtUnix
	targetGate := "AND target_sha256 IS NULL AND completed_at_unix IS NULL"
	if expectation.Receipt.Status == "applied" {
		targetGate = "AND target_sha256 IS NOT NULL AND completed_at_unix IS NOT NULL"
	} else if expectation.Receipt.Status != "applying" {
		targetGate = "AND 0"
	}
	return rqlite.Statement{SQL: `UPDATE backup_rpo_state
SET restore_epoch=?,dirty_generation=?,verified_generation=?,
verified_backup_id=?,verified_object_key=?,verified_object_sha256=?,verified_object_version=?,
verified_size_bytes=?,verified_manifest_version=?,verified_at_unix=?,
last_attempt_sequence=?,phase=?,updated_at_unix=?
WHERE singleton_id=1
AND restore_epoch=? AND dirty_generation=? AND verified_generation=?
AND verified_backup_id IS ? AND verified_object_key IS ? AND verified_object_sha256 IS ?
AND verified_object_version IS ? AND verified_size_bytes IS ? AND verified_manifest_version IS ?
AND verified_at_unix IS ? AND last_attempt_sequence=? AND phase=? AND updated_at_unix=?
AND EXISTS(SELECT 1 FROM cluster_restore_state
    WHERE singleton_id=1 AND activated=1 AND restore_epoch=backup_rpo_state.restore_epoch)
AND EXISTS(SELECT 1 FROM cluster_job_leases
    WHERE job_name=? AND holder_id=? AND lease_token=? AND expires_at_unix>unixepoch())
AND EXISTS(SELECT 1 FROM import_runs
    WHERE import_run_id=? AND source_sha256=? AND plan_sha256=? AND status=? ` + targetGate + `)
RETURNING restore_epoch,dirty_generation,verified_generation,
verified_backup_id,verified_object_key,verified_object_sha256,verified_object_version,
verified_size_bytes,verified_manifest_version,verified_at_unix,
last_attempt_sequence,phase,updated_at_unix`, Args: append(
		append(task6BackupRPOStateArgs(baseline), task6BackupRPOStateArgs(expected)...),
		owner.JobName, owner.HolderID, owner.LeaseToken,
		expectation.Receipt.RunID, expectation.Receipt.SourceDigest,
		expectation.Receipt.PlanDigest, expectation.Receipt.Status,
	)}
}

func task6BackupRPOStateArgs(state task6BackupRPOState) []any {
	return []any{
		state.RestoreEpoch, state.DirtyGeneration, state.VerifiedGeneration,
		state.VerifiedBackupID, state.VerifiedObjectKey, state.VerifiedObjectSHA256,
		state.VerifiedObjectVersion, state.VerifiedSizeBytes, state.VerifiedManifestVersion,
		state.VerifiedAtUnix, state.LastAttemptSequence, state.Phase, state.UpdatedAtUnix,
	}
}

func task6ReadIntegrationBackupState(
	ctx context.Context,
	db rqlite.RQLite,
) (task6BackupRPOState, error) {
	results, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT
restore_epoch,dirty_generation,verified_generation,
verified_backup_id,verified_object_key,verified_object_sha256,verified_object_version,
verified_size_bytes,verified_manifest_version,verified_at_unix,
last_attempt_sequence,phase,updated_at_unix
FROM backup_rpo_state WHERE singleton_id=1`})
	if err != nil {
		return task6BackupRPOState{}, err
	}
	if len(results) != 1 || len(results[0].Rows) != 1 {
		return task6BackupRPOState{}, fmt.Errorf("backup RPO singleton result is malformed")
	}
	return task6BackupRPOStateFromRow(results[0].Rows[0])
}

func task6BackupRPOStateFromRow(row map[string]any) (task6BackupRPOState, error) {
	var state task6BackupRPOState
	var ok bool
	state.RestoreEpoch, ok = applyRowInt(row["restore_epoch"])
	if !ok || state.RestoreEpoch <= 0 {
		return state, fmt.Errorf("backup RPO restore epoch is malformed")
	}
	state.DirtyGeneration, ok = applyRowInt(row["dirty_generation"])
	if !ok || state.DirtyGeneration < 0 {
		return state, fmt.Errorf("backup RPO dirty generation is malformed")
	}
	state.VerifiedGeneration, ok = applyRowInt(row["verified_generation"])
	if !ok || state.VerifiedGeneration < 0 || state.VerifiedGeneration > state.DirtyGeneration {
		return state, fmt.Errorf("backup RPO verified generation is malformed")
	}
	for column, target := range map[string]*any{
		"verified_backup_id":      &state.VerifiedBackupID,
		"verified_object_key":     &state.VerifiedObjectKey,
		"verified_object_sha256":  &state.VerifiedObjectSHA256,
		"verified_object_version": &state.VerifiedObjectVersion,
	} {
		value, exists := row[column]
		if !exists || (value != nil && task6StringValue(value) == "") {
			return state, fmt.Errorf("backup RPO %s is malformed", column)
		}
		*target = value
	}
	for column, target := range map[string]*any{
		"verified_size_bytes":       &state.VerifiedSizeBytes,
		"verified_manifest_version": &state.VerifiedManifestVersion,
		"verified_at_unix":          &state.VerifiedAtUnix,
	} {
		value, exists := row[column]
		if !exists {
			return state, fmt.Errorf("backup RPO %s is absent", column)
		}
		if value != nil {
			integer, integerOK := applyRowInt(value)
			if !integerOK {
				return state, fmt.Errorf("backup RPO %s is malformed", column)
			}
			value = integer
		}
		*target = value
	}
	state.LastAttemptSequence, ok = applyRowInt(row["last_attempt_sequence"])
	if !ok || state.LastAttemptSequence < 0 {
		return state, fmt.Errorf("backup RPO last attempt is malformed")
	}
	state.Phase, ok = row["phase"].(string)
	if !ok || (state.Phase != "dirty" && state.Phase != "verified") {
		return state, fmt.Errorf("backup RPO phase is malformed")
	}
	state.UpdatedAtUnix, ok = applyRowInt(row["updated_at_unix"])
	if !ok || state.UpdatedAtUnix <= 0 {
		return state, fmt.Errorf("backup RPO update time is malformed")
	}
	return state, nil
}

func task6StringValue(value any) string {
	stringValue, _ := value.(string)
	return stringValue
}

func (state task6BackupRPOState) summary() string {
	return fmt.Sprintf(
		"epoch=%d dirty=%d verified=%d attempt=%d phase=%s updated=%d",
		state.RestoreEpoch, state.DirtyGeneration, state.VerifiedGeneration,
		state.LastAttemptSequence, state.Phase, state.UpdatedAtUnix,
	)
}
