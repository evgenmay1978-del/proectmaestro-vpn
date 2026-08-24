package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	testBackupRPOCapabilityDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testBackupRPOObjectDigest     = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestBackupRPODirtyGenerationStatementIsTransactionLocalAndRestoreBound(t *testing.T) {
	statement := backupRPODirtyGenerationStatement(2_000_000)
	sql := strings.ToLower(statement.SQL)
	for _, required := range []string{
		"update backup_rpo_state", "dirty_generation", "dirty_generation + 1",
		"changes() > 0", "cluster_restore_state", "activated = 1", "restore_epoch",
		"returning dirty_generation",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("dirty-generation statement lacks %q: %s", required, statement.SQL)
		}
	}
	if len(statement.Args) != 1 || statement.Args[0] != int64(2_000_000) {
		t.Fatalf("dirty-generation args = %#v", statement.Args)
	}
}

func assertBackupDirtyImmediatelyAfter(t *testing.T, statements []rqlite.Statement, authoritativeIndex int) {
	t.Helper()
	if authoritativeIndex < 0 || authoritativeIndex+1 >= len(statements) {
		t.Fatalf("authoritative statement %d has no following dirty statement: %#v", authoritativeIndex, statements)
	}
	dirtyCount := 0
	for index, statement := range statements {
		sql := strings.ToLower(statement.SQL)
		if strings.Contains(sql, "update backup_rpo_state") && strings.Contains(sql, "dirty_generation") {
			dirtyCount++
			if index != authoritativeIndex+1 {
				t.Fatalf("dirty statement index = %d, want %d", index, authoritativeIndex+1)
			}
			if !strings.Contains(sql, "changes() > 0") {
				t.Fatalf("dirty statement is not gated by preceding mutation: %s", statement.SQL)
			}
		}
	}
	if dirtyCount != 1 {
		t.Fatalf("dirty statement count = %d, want 1", dirtyCount)
	}
}

func validBackupRPOAttemptIdentity() BackupRPOAttemptIdentity {
	return BackupRPOAttemptIdentity{
		HolderID: "node-s2", LeaseToken: "lease-token-a", RestoreEpoch: 7,
		LeaseFence: 3, Capability: BackupRPOCapability{
			Generation: 5, EvidenceSHA256: testBackupRPOCapabilityDigest,
			ExpiresAtUnix: 2_000_120,
		},
		CapturedGeneration: 9, AttemptSequence: 5,
		BackupID:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ObjectKey: "backups/g-9/a-5.tar.gpg", ObjectSHA256: testBackupRPOObjectDigest,
		ObjectSizeBytes: 4096, ManifestVersion: 2,
		AdapterContractVersion: BackupRPOAdapterYandexS3V1,
	}
}

func mustBackupRPOVersionID(value string) BackupRPOVersionID {
	versionID, err := NewBackupRPOVersionID(value)
	if err != nil {
		panic(err)
	}
	return versionID
}

func backupRPOAttemptRow(identity BackupRPOAttemptIdentity, phase string, version, failure any) map[string]any {
	return map[string]any{
		"restore_epoch": identity.RestoreEpoch, "attempt_sequence": identity.AttemptSequence,
		"phase": phase, "backup_id": identity.BackupID,
		"captured_generation": identity.CapturedGeneration,
		"object_key":          identity.ObjectKey, "object_sha256": identity.ObjectSHA256,
		"object_version": version, "object_size_bytes": identity.ObjectSizeBytes,
		"manifest_version":           identity.ManifestVersion,
		"adapter_contract_version":   identity.AdapterContractVersion,
		"capability_generation":      identity.Capability.Generation,
		"capability_evidence_sha256": identity.Capability.EvidenceSHA256,
		"capability_expires_at_unix": identity.Capability.ExpiresAtUnix,
		"lease_holder_id":            identity.HolderID, "lease_token": identity.LeaseToken,
		"lease_fence": identity.LeaseFence, "failure_code": failure,
		"created_at_unix": int64(2_000_000), "updated_at_unix": int64(2_000_001),
		"database_now_unix": int64(2_000_002),
	}
}

func validBackupRPOVerification() BackupRPOVerification {
	identity := validBackupRPOAttemptIdentity()
	return BackupRPOVerification{
		Identity: identity, VersionID: mustBackupRPOVersionID("version-9"), FullReadback: true,
		ReadbackSHA256: identity.ObjectSHA256, ReadbackSizeBytes: identity.ObjectSizeBytes,
		ManifestAuthenticated: true, ManifestVersion: identity.ManifestVersion,
		ManifestBackupID:           identity.BackupID,
		ManifestCapturedGeneration: identity.CapturedGeneration,
		ManifestObjectKey:          identity.ObjectKey, ManifestObjectSHA256: identity.ObjectSHA256,
		ManifestObjectSizeBytes: identity.ObjectSizeBytes,
	}
}

func TestBackupRPORegisterAttemptBurnsExactSequenceAndInsertsAtomically(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"last_attempt_sequence": identity.AttemptSequence}}},
		rqlite.Result{Rows: []map[string]any{backupRPOAttemptRow(identity, BackupRPOAttemptPending, nil, nil)}},
	)}}
	attempt, err := NewBackupRPOStore(db).RegisterAttempt(context.Background(), identity)
	if err != nil || attempt.Phase != BackupRPOAttemptPending || attempt.Identity != identity {
		t.Fatalf("attempt=%#v error=%v", attempt, err)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 0 {
		t.Fatalf("requests=%d reads=%d", len(db.requestCalls), len(db.linearCalls))
	}
	call := db.requestCalls[0]
	if call.level != rqlite.Linearizable || !call.transaction || len(call.statements) != 2 {
		t.Fatalf("call=%#v", call)
	}
	assertBackupRPORegistrationSQL(t, call, identity, []string{
		"update backup_rpo_state", "last_attempt_sequence=?", "last_attempt_sequence=?-1",
		"dirty_generation=?", "phase in ('pending','applying','applied','unknown')",
		"insert into backup_rpo_attempts", "changes()=1", "'pending'", "returning",
	})
}

func TestBackupRPORegisterAttemptRejectsOneActiveAttemptAndResolvesUnknownExactly(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	conflict := &recordingRQLite{requests: []scriptedResult{resultsScript(rqlite.Result{}, rqlite.Result{})}}
	if _, err := NewBackupRPOStore(conflict).RegisterAttempt(context.Background(), identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("active attempt error=%v, want ErrConflict", err)
	}
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic ambiguity"),
		}}},
		linear: []scriptedResult{rowsScript(backupRPOAttemptRow(identity, BackupRPOAttemptPending, nil, nil))},
	}
	attempt, err := NewBackupRPOStore(db).RegisterAttempt(context.Background(), identity)
	if err != nil || attempt.Phase != BackupRPOAttemptPending {
		t.Fatalf("attempt=%#v error=%v", attempt, err)
	}
	assertOneExactBackupRPOAttemptRead(t, db, identity, BackupRPOAttemptPending)
	evidenceSQL := strings.ToLower(db.linearCalls[0].statements[0].SQL)
	if !strings.Contains(evidenceSQL, "backup_rpo_state") ||
		!strings.Contains(evidenceSQL, "last_attempt_sequence>=a.attempt_sequence") {
		t.Fatalf("registration evidence lacks burned sequence proof: %s", evidenceSQL)
	}
}

func TestBackupRPOMarkUploadStartedIsOneWayAndExactlyBound(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	db := &recordingRQLite{requests: []scriptedResult{rowsScript(
		backupRPOAttemptRow(identity, BackupRPOAttemptApplying, nil, nil),
	)}}
	attempt, err := NewBackupRPOStore(db).MarkUploadStarted(context.Background(), identity)
	if err != nil || attempt.Phase != BackupRPOAttemptApplying {
		t.Fatalf("attempt=%#v error=%v", attempt, err)
	}
	assertBackupRPOAttemptSQL(t, db.requestCalls[0], identity, []string{
		"set phase='applying'", "phase='pending'", "expires_at_unix>unixepoch()",
	})
	conflictDB := &recordingRQLite{requests: []scriptedResult{rowsScript()}}
	if _, err := NewBackupRPOStore(conflictDB).MarkUploadStarted(context.Background(), identity); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated upload start error=%v, want ErrConflict", err)
	}
}

func TestBackupRPORecordUploadOutcomePersistsAppliedOrUnknownWithoutReplay(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	for _, test := range []struct {
		name, phase, version, oldPhase string
		unknown                        bool
	}{
		{name: "applied", phase: BackupRPOAttemptApplied, version: "version-9", oldPhase: "phase in ('applying','unknown')"},
		{name: "unknown", phase: BackupRPOAttemptUnknown, unknown: true, oldPhase: "phase='applying'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rowVersion any
			versionID := BackupRPOVersionID{}
			if test.version != "" {
				rowVersion = test.version
				versionID = mustBackupRPOVersionID(test.version)
			}
			db := &recordingRQLite{requests: []scriptedResult{rowsScript(
				backupRPOAttemptRow(identity, test.phase, rowVersion, nil),
			)}}
			attempt, err := NewBackupRPOStore(db).RecordUploadOutcome(context.Background(), BackupRPOUploadOutcome{
				Identity: identity, VersionID: versionID, Unknown: test.unknown,
			})
			if err != nil || attempt.Phase != test.phase {
				t.Fatalf("attempt=%#v error=%v", attempt, err)
			}
			assertBackupRPOAttemptSQL(t, db.requestCalls[0], identity, []string{test.oldPhase, "updated_at_unix=unixepoch()"})
		})
	}
}

func TestBackupRPOVersionIDAcceptsOpaqueLowerHexAndRejectsETagShapes(t *testing.T) {
	opaque := "0123456789abcdef0123456789abcdef"
	versionID, err := NewBackupRPOVersionID(opaque)
	if err != nil || versionID.String() != opaque {
		t.Fatalf("versionID=%q error=%v", versionID.String(), err)
	}
	for _, value := range []string{
		"0123456789ABCDEF0123456789ABCDEF",
		"\"0123456789abcdef0123456789abcdef\"",
		"0123456789abcdef0123456789abcdef-2",
		"\"0123456789ABCDEF0123456789ABCDEF-12\"",
		"", "latest", "null", "none",
	} {
		if _, err := NewBackupRPOVersionID(value); err == nil ||
			err.Error() != "controlplane: backup RPO attempt request is invalid" {
			t.Fatalf("NewBackupRPOVersionID(%q) error=%v", value, err)
		}
	}
}

func TestBackupRPORecordUploadOutcomeRejectsAmbiguousVersionProvenance(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	for _, outcome := range []BackupRPOUploadOutcome{
		{Identity: identity},
		{Identity: identity, Unknown: true, VersionID: mustBackupRPOVersionID("version-9")},
		{Identity: identity, VersionID: BackupRPOVersionID{value: "latest"}},
	} {
		db := &recordingRQLite{}
		_, err := NewBackupRPOStore(db).RecordUploadOutcome(context.Background(), outcome)
		if err == nil || err.Error() != "controlplane: backup RPO attempt request is invalid" || len(db.requestCalls) != 0 {
			t.Fatalf("outcome=%#v error=%v requests=%d", outcome, err, len(db.requestCalls))
		}
	}
}

func TestBackupRPOAcknowledgeVerifiedUsesExactVersionReadbackAndManifestProof(t *testing.T) {
	proof := validBackupRPOVerification()
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{
			"verified_generation": proof.Identity.CapturedGeneration,
			"dirty_generation":    proof.Identity.CapturedGeneration + 1, "phase": BackupRPOPhaseDirty,
		}}},
		rqlite.Result{Rows: []map[string]any{backupRPOAttemptRow(
			proof.Identity, BackupRPOAttemptVerified, proof.VersionID.String(), nil,
		)}},
	)}}
	attempt, err := NewBackupRPOStore(db).AcknowledgeVerified(context.Background(), proof)
	if err != nil || attempt.Phase != BackupRPOAttemptVerified {
		t.Fatalf("attempt=%#v error=%v", attempt, err)
	}
	call := db.requestCalls[0]
	if !call.transaction || len(call.statements) != 2 {
		t.Fatalf("call=%#v", call)
	}
	assertBackupRPOAttemptSQL(t, call, proof.Identity, []string{
		"verified_generation=?", "verified_backup_id=?", "verified_object_key=?",
		"verified_object_sha256=?", "verified_object_version=?", "verified_size_bytes=?",
		"verified_manifest_version=?", "verified_at_unix=unixepoch()", "dirty_generation>=?",
		"case when dirty_generation=? then 'verified' else 'dirty' end", "phase='applied'",
		"set phase='verified'", "changes()=1", "expires_at_unix>unixepoch()",
	})
}

func TestBackupRPOAcknowledgeVerifiedRejectsMissingOrMismatchedProof(t *testing.T) {
	valid := validBackupRPOVerification()
	for _, mutate := range []func(*BackupRPOVerification){
		func(v *BackupRPOVerification) { v.FullReadback = false },
		func(v *BackupRPOVerification) { v.ManifestAuthenticated = false },
		func(v *BackupRPOVerification) { v.VersionID = BackupRPOVersionID{} },
		func(v *BackupRPOVerification) { v.ReadbackSHA256 = "0123456789abcdef0123456789abcdef" },
		func(v *BackupRPOVerification) { v.ReadbackSizeBytes++ },
		func(v *BackupRPOVerification) { v.ManifestVersion = 1 },
		func(v *BackupRPOVerification) { v.ManifestBackupID = strings.Repeat("a", 32) },
		func(v *BackupRPOVerification) { v.ManifestCapturedGeneration++ },
		func(v *BackupRPOVerification) { v.ManifestObjectKey += ".other" },
		func(v *BackupRPOVerification) { v.ManifestObjectSHA256 = strings.Repeat("e", 64) },
		func(v *BackupRPOVerification) { v.ManifestObjectSizeBytes++ },
	} {
		proof := valid
		mutate(&proof)
		db := &recordingRQLite{}
		_, err := NewBackupRPOStore(db).AcknowledgeVerified(context.Background(), proof)
		if err == nil || err.Error() != "controlplane: backup RPO attempt request is invalid" || len(db.requestCalls) != 0 {
			t.Fatalf("proof=%#v error=%v requests=%d", proof, err, len(db.requestCalls))
		}
	}
}

func TestBackupRPOSupersedeStaleAttemptRequiresExactNewerLease(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	current := validBackupRPOLeaseRequest()
	current.HolderID, current.LeaseToken = "node-s3", "lease-token-new"
	current.ExpectedFence = identity.LeaseFence + 1
	db := &recordingRQLite{requests: []scriptedResult{rowsScript(
		backupRPOAttemptRow(identity, BackupRPOAttemptSuperseded, nil, BackupRPOFailureStaleFence),
	)}}
	attempt, err := NewBackupRPOStore(db).SupersedeStaleAttempt(context.Background(), BackupRPOSupersedeRequest{
		Identity: identity, CurrentLease: current,
	})
	if err != nil || attempt.Phase != BackupRPOAttemptSuperseded || attempt.FailureCode != BackupRPOFailureStaleFence {
		t.Fatalf("attempt=%#v error=%v", attempt, err)
	}
	assertBackupRPOAttemptSQL(t, db.requestCalls[0], identity, []string{
		"set phase='superseded'", "failure_code='stale-fence'",
		"phase in ('pending','applying','applied','unknown')", "lease_fence>?",
		"job_name='backup-rpo'", "holder_id=?", "lease_token=?", "expires_at_unix>unixepoch()",
	})
	current.ExpectedFence = identity.LeaseFence
	invalidDB := &recordingRQLite{}
	_, err = NewBackupRPOStore(invalidDB).SupersedeStaleAttempt(context.Background(), BackupRPOSupersedeRequest{
		Identity: identity, CurrentLease: current,
	})
	if err == nil || err.Error() != "controlplane: backup RPO attempt request is invalid" || len(invalidDB.requestCalls) != 0 {
		t.Fatalf("same-fence error=%v requests=%d", err, len(invalidDB.requestCalls))
	}
}

func TestBackupRPOSupersedeStaleAttemptPreservesAppliedVersionKnownAndUnknown(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	current := validBackupRPOLeaseRequest()
	current.HolderID, current.LeaseToken = "node-s3", "lease-token-new"
	current.ExpectedFence = identity.LeaseFence + 1
	appliedVersion := "version-9"
	row := backupRPOAttemptRow(
		identity, BackupRPOAttemptSuperseded, appliedVersion, BackupRPOFailureStaleFence,
	)

	t.Run("known outcome", func(t *testing.T) {
		db := &recordingRQLite{requests: []scriptedResult{rowsScript(row)}}
		attempt, err := NewBackupRPOStore(db).SupersedeStaleAttempt(
			context.Background(),
			BackupRPOSupersedeRequest{Identity: identity, CurrentLease: current},
		)
		if err != nil || attempt.ObjectVersion != appliedVersion {
			t.Fatalf("attempt=%#v error=%v", attempt, err)
		}
	})

	t.Run("committed unknown outcome", func(t *testing.T) {
		db := &recordingRQLite{
			requests: []scriptedResult{{err: &rqlite.TransportError{
				Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic ambiguity"),
			}}},
			linear: []scriptedResult{rowsScript(row)},
		}
		attempt, err := NewBackupRPOStore(db).SupersedeStaleAttempt(
			context.Background(),
			BackupRPOSupersedeRequest{Identity: identity, CurrentLease: current},
		)
		if err != nil || attempt.ObjectVersion != appliedVersion {
			t.Fatalf("attempt=%#v error=%v", attempt, err)
		}
		if len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
			t.Fatalf("requests=%d reads=%d, want 1/1", len(db.requestCalls), len(db.linearCalls))
		}
		evidenceSQL := strings.ToLower(db.linearCalls[0].statements[0].SQL)
		if strings.Contains(evidenceSQL, "object_version is null") {
			t.Fatalf("supersede evidence incorrectly requires NULL version: %s", evidenceSQL)
		}
	})
}

func TestBackupRPOAttemptTransitionUnknownOutcomeReadsOnceAndMalformedRowsFailClosed(t *testing.T) {
	identity := validBackupRPOAttemptIdentity()
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic ambiguity"),
		}}},
		linear: []scriptedResult{rowsScript(backupRPOAttemptRow(identity, BackupRPOAttemptApplying, nil, nil))},
	}
	attempt, err := NewBackupRPOStore(db).MarkUploadStarted(context.Background(), identity)
	if err != nil || attempt.Phase != BackupRPOAttemptApplying {
		t.Fatalf("attempt=%#v error=%v", attempt, err)
	}
	assertOneExactBackupRPOAttemptRead(t, db, identity, BackupRPOAttemptApplying)

	malformed := backupRPOAttemptRow(identity, BackupRPOAttemptApplying, nil, nil)
	malformed["attempt_sequence"] = float64(identity.AttemptSequence)
	malformedDB := &recordingRQLite{requests: []scriptedResult{rowsScript(malformed)}}
	_, err = NewBackupRPOStore(malformedDB).MarkUploadStarted(context.Background(), identity)
	if err == nil || err.Error() != "controlplane: backup RPO attempt unavailable" {
		t.Fatalf("malformed error=%v", err)
	}
}

func assertBackupRPORegistrationSQL(t *testing.T, call recordedCall, identity BackupRPOAttemptIdentity, fragments []string) {
	t.Helper()
	sql, args := "", ""
	for _, statement := range call.statements {
		sql += " " + strings.ToLower(statement.SQL)
		args += " " + fmt.Sprint(statement.Args)
	}
	for _, fragment := range append([]string{
		"restore_epoch", "attempt_sequence", "captured_generation", "backup_id",
		"object_key", "object_sha256", "object_size_bytes", "manifest_version",
		"adapter_contract_version", "capability_generation", "capability_evidence_sha256",
		"capability_expires_at_unix", "lease_holder_id", "lease_token", "lease_fence",
	}, fragments...) {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("registration SQL lacks %q: %s", fragment, sql)
		}
	}
	for _, value := range []any{
		identity.HolderID, identity.LeaseToken, identity.RestoreEpoch, identity.LeaseFence,
		identity.Capability.Generation, identity.Capability.EvidenceSHA256, identity.Capability.ExpiresAtUnix,
		identity.CapturedGeneration, identity.AttemptSequence, identity.BackupID,
		identity.ObjectKey, identity.ObjectSHA256, identity.ObjectSizeBytes,
		identity.ManifestVersion, identity.AdapterContractVersion,
	} {
		if !strings.Contains(args, fmt.Sprint(value)) {
			t.Fatalf("registration args %s lack %v", args, value)
		}
	}
}

func assertBackupRPOAttemptSQL(t *testing.T, call recordedCall, identity BackupRPOAttemptIdentity, fragments []string) {
	t.Helper()
	sql, args := "", ""
	for _, statement := range call.statements {
		sql += " " + strings.ToLower(statement.SQL)
		args += " " + fmt.Sprint(statement.Args)
	}
	for _, fragment := range append([]string{
		"restore_epoch=?", "attempt_sequence=?", "captured_generation=?", "backup_id=?",
		"object_key=?", "object_sha256=?", "object_size_bytes=?", "manifest_version=?",
		"adapter_contract_version=?", "capability_generation=?", "capability_evidence_sha256=?",
		"capability_expires_at_unix=?", "lease_holder_id=?", "lease_token=?", "lease_fence=?",
	}, fragments...) {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("attempt SQL lacks %q: %s", fragment, sql)
		}
	}
	for _, value := range []any{
		identity.HolderID, identity.LeaseToken, identity.RestoreEpoch, identity.LeaseFence,
		identity.Capability.Generation, identity.Capability.EvidenceSHA256, identity.Capability.ExpiresAtUnix,
		identity.CapturedGeneration, identity.AttemptSequence, identity.BackupID,
		identity.ObjectKey, identity.ObjectSHA256, identity.ObjectSizeBytes,
		identity.ManifestVersion, identity.AdapterContractVersion,
	} {
		if !strings.Contains(args, fmt.Sprint(value)) {
			t.Fatalf("attempt args %s lack %v", args, value)
		}
	}
}

func assertOneExactBackupRPOAttemptRead(t *testing.T, db *recordingRQLite, identity BackupRPOAttemptIdentity, phase string) {
	t.Helper()
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
		t.Fatalf("requests=%d reads=%d, want 1/1", len(db.requestCalls), len(db.linearCalls))
	}
	call := db.linearCalls[0]
	if len(call.statements) != 1 {
		t.Fatalf("evidence statements=%d", len(call.statements))
	}
	assertBackupRPOAttemptSQL(t, call, identity, []string{"from backup_rpo_attempts", "phase=?"})
	if !strings.Contains(fmt.Sprint(call.statements[0].Args), phase) {
		t.Fatalf("evidence args %v lack phase %q", call.statements[0].Args, phase)
	}
}

func validBackupRPOStateRow() map[string]any {
	return map[string]any{
		"restore_epoch":                    int64(7),
		"dirty_generation":                 int64(9),
		"verified_generation":              int64(8),
		"verified_backup_id":               "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"verified_object_key":              "backups/g-8/a-1.tar.gpg",
		"verified_object_sha256":           testBackupRPOObjectDigest,
		"verified_object_version":          "version-8",
		"verified_size_bytes":              int64(4096),
		"verified_manifest_version":        int64(2),
		"verified_at_unix":                 int64(1_999_900),
		"last_attempt_sequence":            int64(4),
		"phase":                            "dirty",
		"updated_at_unix":                  int64(1_999_950),
		"database_now_unix":                int64(2_000_000),
		"lease_job_name":                   "backup-rpo",
		"lease_holder_id":                  "node-s2",
		"lease_token":                      "lease-token-a",
		"lease_acquired_at_unix":           int64(1_999_980),
		"lease_expires_at_unix":            int64(2_000_060),
		"lease_restore_epoch":              int64(7),
		"lease_fence":                      int64(3),
		"lease_capability_generation":      int64(5),
		"lease_capability_evidence_sha256": testBackupRPOCapabilityDigest,
		"lease_capability_expires_at_unix": int64(2_000_120),
	}
}

func validBackupRPOLeaseRequest() BackupRPOLeaseRequest {
	return BackupRPOLeaseRequest{
		HolderID:      "node-s2",
		LeaseToken:    "lease-token-a",
		RestoreEpoch:  7,
		ExpectedFence: 2,
		TTLSeconds:    60,
		Capability: BackupRPOCapability{
			Generation:     5,
			EvidenceSHA256: testBackupRPOCapabilityDigest,
			ExpiresAtUnix:  2_000_120,
		},
	}
}

func backupRPOLeaseRow(request BackupRPOLeaseRequest, fence, acquiredAt, expiresAt, databaseNow int64) map[string]any {
	return map[string]any{
		"job_name":                   "backup-rpo",
		"holder_id":                  request.HolderID,
		"lease_token":                request.LeaseToken,
		"acquired_at_unix":           acquiredAt,
		"expires_at_unix":            expiresAt,
		"restore_epoch":              request.RestoreEpoch,
		"lease_fence":                fence,
		"capability_generation":      request.Capability.Generation,
		"capability_evidence_sha256": request.Capability.EvidenceSHA256,
		"capability_expires_at_unix": request.Capability.ExpiresAtUnix,
		"database_now_unix":          databaseNow,
	}
}

func TestBackupRPOCurrentReturnsExactActiveRestoreStateAndLease(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(validBackupRPOStateRow())}}
	state, err := NewBackupRPOStore(db).Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if state.RestoreEpoch != 7 || state.DirtyGeneration != 9 || state.VerifiedGeneration != 8 ||
		state.LastAttemptSequence != 4 || state.Phase != BackupRPOPhaseDirty ||
		state.UpdatedAtUnix != 1_999_950 || state.DatabaseNowUnix != 2_000_000 {
		t.Fatalf("state=%#v", state)
	}
	if state.Verified == nil || state.Verified.BackupID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		state.Verified.ObjectKey != "backups/g-8/a-1.tar.gpg" ||
		state.Verified.ObjectSHA256 != testBackupRPOObjectDigest ||
		state.Verified.ObjectVersion != "version-8" || state.Verified.SizeBytes != 4096 ||
		state.Verified.ManifestVersion != 2 || state.Verified.VerifiedAtUnix != 1_999_900 {
		t.Fatalf("verified=%#v", state.Verified)
	}
	if state.Lease == nil || state.Lease.JobName != "backup-rpo" || state.Lease.HolderID != "node-s2" ||
		state.Lease.LeaseToken != "lease-token-a" || state.Lease.RestoreEpoch != 7 ||
		state.Lease.LeaseFence != 3 || !state.Lease.Live ||
		state.Lease.Capability.Generation != 5 ||
		state.Lease.Capability.EvidenceSHA256 != testBackupRPOCapabilityDigest {
		t.Fatalf("lease=%#v", state.Lease)
	}
	if len(db.linearCalls) != 1 || len(db.requestCalls) != 0 {
		t.Fatalf("linear=%d requests=%d", len(db.linearCalls), len(db.requestCalls))
	}
	sql := strings.ToLower(db.linearCalls[0].statements[0].SQL)
	for _, required := range []string{
		"backup_rpo_state", "cluster_restore_state", "activated=1", "restore_epoch",
		"cluster_job_leases", "backup-rpo", "unixepoch()",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("Current query lacks %q: %s", required, sql)
		}
	}
}

func TestBackupRPOCurrentRejectsEveryMalformedNumericEncoding(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value any
	}{
		{name: "missing", key: "dirty_generation", value: nil},
		{name: "float", key: "dirty_generation", value: float64(9)},
		{name: "fractional-json-number", key: "dirty_generation", value: json.Number("9.0")},
		{name: "leading-zero-string", key: "dirty_generation", value: "09"},
		{name: "unsigned", key: "dirty_generation", value: uint64(9)},
		{name: "negative", key: "dirty_generation", value: int64(-1)},
		{name: "zero-restore-epoch", key: "restore_epoch", value: int64(0)},
		{name: "zero-database-time", key: "database_now_unix", value: int64(0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := validBackupRPOStateRow()
			if test.value == nil {
				delete(row, test.key)
			} else {
				row[test.key] = test.value
			}
			db := &recordingRQLite{linear: []scriptedResult{rowsScript(row)}}
			_, err := NewBackupRPOStore(db).Current(context.Background())
			if err == nil || err.Error() != "controlplane: backup RPO state unavailable" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestBackupRPOCurrentFailsClosedWithoutOneCompleteActiveState(t *testing.T) {
	tests := []struct {
		name string
		rows []map[string]any
	}{
		{name: "inactive-or-mismatched-restore"},
		{name: "duplicate", rows: []map[string]any{validBackupRPOStateRow(), validBackupRPOStateRow()}},
		{name: "invalid-phase", rows: []map[string]any{func() map[string]any {
			row := validBackupRPOStateRow()
			row["phase"] = "unknown"
			return row
		}()}},
		{name: "partial-verified-identity", rows: []map[string]any{func() map[string]any {
			row := validBackupRPOStateRow()
			row["verified_object_version"] = nil
			return row
		}()}},
		{name: "lease-restore-mismatch", rows: []map[string]any{func() map[string]any {
			row := validBackupRPOStateRow()
			row["lease_restore_epoch"] = int64(6)
			return row
		}()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{linear: []scriptedResult{rowsScript(test.rows...)}}
			_, err := NewBackupRPOStore(db).Current(context.Background())
			if err == nil || err.Error() != "controlplane: backup RPO state unavailable" {
				t.Fatalf("error=%v", err)
			}
		})
	}

	db := &recordingRQLite{linear: []scriptedResult{{err: errors.New("raw backend object holder token")}}}
	_, err := NewBackupRPOStore(db).Current(context.Background())
	if err == nil || err.Error() != "controlplane: backup RPO state unavailable" ||
		strings.Contains(err.Error(), "raw backend") {
		t.Fatalf("error=%v", err)
	}
}

func TestBackupRPOAcquireLeaseUsesOneFencedDBTimeTransaction(t *testing.T) {
	tests := []struct {
		name          string
		expectedFence int64
		returnedFence int64
	}{
		{name: "first-owner", expectedFence: 0, returnedFence: 1},
		{name: "expired-holder-takeover", expectedFence: 7, returnedFence: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validBackupRPOLeaseRequest()
			request.ExpectedFence = test.expectedFence
			db := &recordingRQLite{requests: []scriptedResult{rowsScript(
				backupRPOLeaseRow(request, test.returnedFence, 2_000_000, 2_000_060, 2_000_000),
			)}}
			lease, err := NewBackupRPOStore(db).AcquireLease(context.Background(), request)
			if err != nil {
				t.Fatalf("AcquireLease: %v", err)
			}
			if lease.JobName != "backup-rpo" || lease.LeaseFence != test.returnedFence ||
				lease.HolderID != request.HolderID || lease.LeaseToken != request.LeaseToken || !lease.Live {
				t.Fatalf("lease=%#v", lease)
			}
			if len(db.requestCalls) != 1 || len(db.linearCalls) != 0 {
				t.Fatalf("requests=%d reads=%d", len(db.requestCalls), len(db.linearCalls))
			}
			call := db.requestCalls[0]
			if call.level != rqlite.Linearizable || !call.transaction || len(call.statements) != 1 {
				t.Fatalf("call=%#v", call)
			}
			sql := strings.ToLower(call.statements[0].SQL)
			for _, required := range []string{
				"insert into cluster_job_leases", "'backup-rpo'", "unixepoch()",
				"cluster_restore_state", "activated=1", "backup_rpo_state",
				"restore_epoch", "capability_expires_at_unix", "expires_at_unix<=unixepoch()",
				"lease_fence + 1", "returning",
			} {
				if !strings.Contains(sql, required) {
					t.Fatalf("AcquireLease SQL lacks %q: %s", required, sql)
				}
			}
		})
	}
}

func TestBackupRPOAcquireLeaseConflictsFailClosed(t *testing.T) {
	for _, name := range []string{
		"live-different-holder", "stale-restore-epoch", "stale-capability", "stale-fence",
	} {
		t.Run(name, func(t *testing.T) {
			db := &recordingRQLite{requests: []scriptedResult{rowsScript()}}
			_, err := NewBackupRPOStore(db).AcquireLease(context.Background(), validBackupRPOLeaseRequest())
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("error=%v, want ErrConflict", err)
			}
			if len(db.requestCalls) != 1 || len(db.linearCalls) != 0 {
				t.Fatalf("requests=%d reads=%d", len(db.requestCalls), len(db.linearCalls))
			}
		})
	}
}

func TestBackupRPOAcquireLeaseResolvesUnknownOutcomeByOneExactRead(t *testing.T) {
	request := validBackupRPOLeaseRequest()
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("raw transport identity"),
		}}},
		linear: []scriptedResult{rowsScript(
			backupRPOLeaseRow(request, 3, 2_000_000, 2_000_060, 2_000_001),
		)},
	}
	lease, err := NewBackupRPOStore(db).AcquireLease(context.Background(), request)
	if err != nil || lease.LeaseFence != 3 {
		t.Fatalf("lease=%#v error=%v", lease, err)
	}
	assertOneExactBackupRPOEvidenceRead(t, db, request, 3)
}

func TestBackupRPOAcquireLeaseUnknownMismatchStaysRedactedAndUnresolved(t *testing.T) {
	request := validBackupRPOLeaseRequest()
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("raw backend holder token"),
		}}},
		linear: []scriptedResult{rowsScript()},
	}
	_, err := NewBackupRPOStore(db).AcquireLease(context.Background(), request)
	if err == nil || err.Error() != "controlplane: backup RPO lease outcome is unresolved" ||
		strings.Contains(err.Error(), request.HolderID) || strings.Contains(err.Error(), request.LeaseToken) ||
		strings.Contains(err.Error(), "raw backend") {
		t.Fatalf("error=%v", err)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
		t.Fatalf("requests=%d reads=%d", len(db.requestCalls), len(db.linearCalls))
	}
}

func TestBackupRPORenewLeasePreservesFenceAndExactIdentity(t *testing.T) {
	request := validBackupRPOLeaseRequest()
	request.ExpectedFence = 4
	db := &recordingRQLite{requests: []scriptedResult{rowsScript(
		backupRPOLeaseRow(request, 4, 1_999_900, 2_000_060, 2_000_000),
	)}}
	lease, err := NewBackupRPOStore(db).RenewLease(context.Background(), request)
	if err != nil || lease.LeaseFence != 4 {
		t.Fatalf("lease=%#v error=%v", lease, err)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 0 {
		t.Fatalf("requests=%d reads=%d", len(db.requestCalls), len(db.linearCalls))
	}
	sql := strings.ToLower(db.requestCalls[0].statements[0].SQL)
	for _, required := range []string{
		"update cluster_job_leases", "set expires_at_unix=unixepoch()+?",
		"job_name='backup-rpo'", "holder_id=?", "lease_token=?", "restore_epoch=?",
		"lease_fence=?", "capability_generation=?", "capability_evidence_sha256=?",
		"expires_at_unix>unixepoch()", "capability_expires_at_unix>=unixepoch()+?",
		"cluster_restore_state", "activated=1", "backup_rpo_state", "returning",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("RenewLease SQL lacks %q: %s", required, sql)
		}
	}
	setClause := strings.SplitN(strings.SplitN(sql, " set ", 2)[1], " where ", 2)[0]
	if strings.Contains(setClause, "lease_fence") || strings.Contains(setClause, "holder_id") ||
		strings.Contains(setClause, "lease_token") {
		t.Fatalf("renewal changed fenced identity: %s", setClause)
	}
}

func TestBackupRPORenewLeaseRejectsExpiredOrStaleIdentity(t *testing.T) {
	for _, name := range []string{
		"expired-at-boundary", "different-holder", "stale-token", "stale-epoch",
		"stale-fence", "stale-capability", "capability-expired",
	} {
		t.Run(name, func(t *testing.T) {
			db := &recordingRQLite{requests: []scriptedResult{rowsScript()}}
			_, err := NewBackupRPOStore(db).RenewLease(context.Background(), validBackupRPOLeaseRequest())
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("error=%v, want ErrConflict", err)
			}
		})
	}
}

func TestBackupRPORenewLeaseResolvesUnknownOutcomeByOneExactRead(t *testing.T) {
	request := validBackupRPOLeaseRequest()
	request.ExpectedFence = 6
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic ambiguity"),
		}}},
		linear: []scriptedResult{rowsScript(
			backupRPOLeaseRow(request, 6, 1_999_900, 2_000_060, 2_000_001),
		)},
	}
	lease, err := NewBackupRPOStore(db).RenewLease(context.Background(), request)
	if err != nil || lease.LeaseFence != 6 {
		t.Fatalf("lease=%#v error=%v", lease, err)
	}
	assertOneExactBackupRPOEvidenceRead(t, db, request, 6)
}

func TestBackupRPOLeaseRejectsInvalidInputAndMalformedResultWithoutLeak(t *testing.T) {
	invalid := validBackupRPOLeaseRequest()
	invalid.Capability.EvidenceSHA256 = "object-secret"
	for _, method := range []struct {
		name string
		call func(*BackupRPOStore) (BackupRPOLease, error)
	}{
		{name: "acquire", call: func(store *BackupRPOStore) (BackupRPOLease, error) {
			return store.AcquireLease(context.Background(), invalid)
		}},
		{name: "renew", call: func(store *BackupRPOStore) (BackupRPOLease, error) {
			return store.RenewLease(context.Background(), invalid)
		}},
	} {
		t.Run(method.name, func(t *testing.T) {
			db := &recordingRQLite{}
			_, err := method.call(NewBackupRPOStore(db))
			if err == nil || err.Error() != "controlplane: backup RPO lease request is invalid" ||
				strings.Contains(err.Error(), "object-secret") || len(db.requestCalls) != 0 || len(db.linearCalls) != 0 {
				t.Fatalf("error=%v requests=%d reads=%d", err, len(db.requestCalls), len(db.linearCalls))
			}
		})
	}

	request := validBackupRPOLeaseRequest()
	badRow := backupRPOLeaseRow(request, 3, 2_000_000, 2_000_060, 2_000_000)
	badRow["lease_fence"] = float64(3)
	db := &recordingRQLite{requests: []scriptedResult{rowsScript(badRow)}}
	_, err := NewBackupRPOStore(db).AcquireLease(context.Background(), request)
	if err == nil || err.Error() != "controlplane: backup RPO lease unavailable" {
		t.Fatalf("error=%v", err)
	}
}

func TestBackupRPOLeaseKnownTransportErrorIsFixedAndRedacted(t *testing.T) {
	request := validBackupRPOLeaseRequest()
	db := &recordingRQLite{requests: []scriptedResult{{err: fmt.Errorf(
		"sql object=%s holder=%s token=%s", testBackupRPOObjectDigest, request.HolderID, request.LeaseToken,
	)}}}
	_, err := NewBackupRPOStore(db).AcquireLease(context.Background(), request)
	if err == nil || err.Error() != "controlplane: backup RPO lease unavailable" ||
		strings.Contains(err.Error(), request.HolderID) || strings.Contains(err.Error(), request.LeaseToken) ||
		strings.Contains(err.Error(), testBackupRPOObjectDigest) {
		t.Fatalf("error=%v", err)
	}
}

func assertOneExactBackupRPOEvidenceRead(
	t *testing.T,
	db *recordingRQLite,
	request BackupRPOLeaseRequest,
	fence int64,
) {
	t.Helper()
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
		t.Fatalf("requests=%d reads=%d, want 1/1", len(db.requestCalls), len(db.linearCalls))
	}
	call := db.linearCalls[0]
	if len(call.statements) != 1 {
		t.Fatalf("evidence statements=%d", len(call.statements))
	}
	sql := strings.ToLower(call.statements[0].SQL)
	for _, required := range []string{
		"cluster_job_leases", "job_name='backup-rpo'", "holder_id=?", "lease_token=?",
		"restore_epoch=?", "lease_fence=?", "capability_generation=?",
		"capability_evidence_sha256=?", "capability_expires_at_unix=?",
		"expires_at_unix>unixepoch()", "capability_expires_at_unix>unixepoch()",
		"cluster_restore_state", "activated=1", "backup_rpo_state", "unixepoch()",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("evidence read lacks %q: %s", required, sql)
		}
	}
	args := fmt.Sprint(call.statements[0].Args)
	for _, expected := range []string{
		request.HolderID, request.LeaseToken, fmt.Sprint(request.RestoreEpoch), fmt.Sprint(fence),
		fmt.Sprint(request.Capability.Generation), request.Capability.EvidenceSHA256,
		fmt.Sprint(request.Capability.ExpiresAtUnix),
	} {
		if !strings.Contains(args, expected) {
			t.Fatalf("evidence args %s lack %q", args, expected)
		}
	}
}
