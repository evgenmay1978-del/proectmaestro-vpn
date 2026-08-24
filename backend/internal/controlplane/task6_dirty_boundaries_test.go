package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func task6DesiredEvidence() map[string]any {
	return map[string]any{
		"generation": int64(5), "desired_sha256": testDesiredSHA,
		"operation_id": "operation-1", "tombstone": int64(0),
		"event_kind": "customer_desired", "payload_sha256": testDesiredSHA,
	}
}

func TestTask6UpsertDesiredUsesStrictMutationOutboxDirtyAndExactEvidence(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"generation": int64(5), "desired_sha256": testDesiredSHA}}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{Rows: []map[string]any{{"dirty_generation": int64(2)}}},
		rqlite.Result{Rows: []map[string]any{task6DesiredEvidence()}},
	)}}
	service, _ := testService(t, db)
	if err := service.UpsertDesired(context.Background(), desiredFixture(5, testDesiredSHA)); err != nil {
		t.Fatalf("UpsertDesired: %v", err)
	}
	if len(db.requestCalls) != 1 || len(db.requestCalls[0].statements) != 4 {
		t.Fatalf("desired statements=%#v, want strict-CAS/outbox/dirty/evidence", db.requestCalls)
	}
	statements := db.requestCalls[0].statements
	upsert := strings.ToLower(statements[0].SQL)
	if !strings.Contains(upsert, "excluded.generation > desired_node_state.generation") ||
		strings.Contains(upsert, "excluded.generation = desired_node_state.generation") {
		t.Fatalf("desired CAS is not strict: %s", upsert)
	}
	outbox := strings.ToLower(statements[1].SQL)
	if !containsAll(outbox, "insert into outbox_events", "changes()=1") ||
		strings.Contains(outbox, "insert or ignore") || strings.Contains(outbox, "do nothing") {
		t.Fatalf("outbox boundary is not exact: %s", outbox)
	}
	dirty := strings.ToLower(statements[2].SQL)
	if !containsAll(dirty, "update backup_rpo_state", "changes() > 0", "phase = 'dirty'") {
		t.Fatalf("desired dirty boundary is missing: %s", dirty)
	}
	evidence := strings.ToLower(statements[3].SQL)
	for _, required := range []string{"select", "desired_node_state", "outbox_events", "operation_id", "event_kind", "payload_sha256"} {
		if !strings.Contains(evidence, required) {
			t.Fatalf("desired evidence lacks %q: %s", required, evidence)
		}
	}
}

func TestTask6UpsertDesiredExactReplaySucceedsWithoutMutation(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		rqlite.Result{Rows: []map[string]any{task6DesiredEvidence()}},
	)}}
	service, _ := testService(t, db)
	if err := service.UpsertDesired(context.Background(), desiredFixture(5, testDesiredSHA)); err != nil {
		t.Fatalf("exact desired replay: %v", err)
	}
}

func TestTask6RecordApplyReceiptBumpsOnlyAfterNewReceipt(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"receipt_id": "receipt-task6"}}},
		rqlite.Result{Rows: []map[string]any{{"dirty_generation": int64(2)}}},
		rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	receipt := ApplyReceipt{
		ReceiptID: "receipt-task6", CustomerID: "customer-1", NodeID: "s2", ServiceName: "xui",
		OperationID: "operation-1", HolderID: "agent", Generation: 8,
		ClusterEpoch: 6, NodeIncarnation: 2, LeaseFence: 10,
		DesiredSHA256: testDesiredSHA, ObservedSHA256: testObservedSHA,
	}
	if err := service.RecordApplyReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("RecordApplyReceipt: %v", err)
	}
	statements := db.requestCalls[0].statements
	if len(statements) != 5 || !strings.Contains(strings.ToLower(statements[1].SQL), "update backup_rpo_state") {
		t.Fatalf("receipt dirty ordering=%#v", statements)
	}
	for _, index := range []int{2, 3} {
		if !strings.Contains(strings.ToLower(statements[index].SQL), "status<>'applied'") {
			t.Fatalf("receipt replay statement %d is not a no-op: %s", index, statements[index].SQL)
		}
	}
}

func TestTask6PurgeTombstoneBumpsAfterDeleteCAS(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{RowsAffected: 1}, rqlite.Result{Rows: []map[string]any{{"dirty_generation": int64(2)}}},
	)}}
	service, _ := testService(t, db)
	err := service.PurgeTombstone(context.Background(), TombstonePurgeCommand{
		TombstoneID: "tombstone-task6", CustomerID: "customer-task6",
	})
	statements := db.requestCalls[0].statements
	if len(statements) != 2 || !strings.Contains(strings.ToLower(statements[1].SQL), "update backup_rpo_state") {
		t.Fatalf("purge dirty ordering=%#v", statements)
	}
	if err != nil {
		t.Fatalf("PurgeTombstone: %v", err)
	}
}

func TestTask6CreateCustomerTombstoneGatesBoundaryAndBumpsOnce(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"generation": int64(9)}}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 4}, rqlite.Result{RowsAffected: 4},
		rqlite.Result{RowsAffected: 4},
		rqlite.Result{Rows: []map[string]any{{"dirty_generation": int64(2)}}},
		rqlite.Result{},
		rqlite.Result{Rows: []map[string]any{{
			"generation": int64(9), "reason": "owner-delete",
			"customer_status": "deleted", "target_count": int64(4),
		}}},
	)}}
	service, _ := testService(t, db)
	_, err := service.CreateCustomerTombstone(context.Background(), CustomerTombstoneCommand{
		TombstoneID: "tombstone-task6", CustomerID: "customer-task6", Generation: 9, Reason: "owner-delete",
	})
	statements := db.requestCalls[0].statements
	if len(statements) != 8 {
		t.Fatalf("tombstone statement count=%d, want 8", len(statements))
	}
	customer := strings.ToLower(statements[0].SQL)
	if !containsAll(customer, "status='deleted'", "not exists", "returning generation") {
		t.Fatalf("customer tombstone CAS is incomplete: %s", customer)
	}
	insert := strings.ToLower(statements[1].SQL)
	if !containsAll(insert, "insert into tombstones", "changes()=1", "on conflict do nothing") {
		t.Fatalf("tombstone boundary is not CAS-gated: %s", insert)
	}
	for index, required := range []string{"changes()=1", "changes()>0", "changes()>0"} {
		if !strings.Contains(strings.ToLower(statements[index+2].SQL), required) {
			t.Fatalf("tombstone downstream statement %d lacks %q: %s", index+2, required, statements[index+2].SQL)
		}
	}
	if !strings.Contains(strings.ToLower(statements[5].SQL), "update backup_rpo_state") {
		t.Fatalf("tombstone dirty statement=%s", statements[5].SQL)
	}
	abort := strings.ToLower(statements[6].SQL)
	if !containsAll(abort, "insert into backup_rpo_state", "select 0", "tombstone_targets", "desired_node_state", "outbox_events") {
		t.Fatalf("tombstone abort postcondition is incomplete: %s", abort)
	}
	if err != nil {
		t.Fatalf("CreateCustomerTombstone: %v", err)
	}
}

func TestTask6PermanentRetirementGatesAuditThenBumps(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"retired": int64(1)}}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{Rows: []map[string]any{{"dirty_generation": int64(2)}}},
		rqlite.Result{RowsAffected: 3},
	)}}
	service, _ := testService(t, db)
	_, err := service.PermanentlyRetireNodeService(context.Background(), PermanentRetirementCommand{
		NodeID: "s1", ServiceName: "xui", ExpectedIncarnation: 4, Actor: "owner", Reason: "provider-retired",
	})
	statements := db.requestCalls[0].statements
	if len(statements) != 4 {
		t.Fatalf("retirement statement count=%d, want 4", len(statements))
	}
	audit := strings.ToLower(statements[1].SQL)
	if !containsAll(audit, "insert into audit_events", "changes()=1") || strings.Contains(audit, "do nothing") {
		t.Fatalf("retirement audit is not CAS-gated: %s", audit)
	}
	if !strings.Contains(strings.ToLower(statements[2].SQL), "update backup_rpo_state") {
		t.Fatalf("retirement dirty statement=%s", statements[2].SQL)
	}
	if err != nil {
		t.Fatalf("PermanentlyRetireNodeService: %v", err)
	}
}

func TestTask6AdvanceAfterRestoreSupersedesAttemptAndRebindsDirtySingleton(t *testing.T) {
	backupSHA := strings.Repeat("a", 64)
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{restoreStateRow(8, false, backupSHA)}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 1, Rows: []map[string]any{{
			"restore_epoch":         int64(8),
			"dirty_generation":      int64(1),
			"verified_generation":   int64(0),
			"last_attempt_sequence": int64(0),
			"phase":                 "dirty",
		}}},
		rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1},
		rqlite.Result{},
	)}}
	state, err := NewRestoreEpochStore(db).AdvanceAfterRestore(context.Background(), 7, backupSHA)
	if err != nil {
		t.Fatalf("AdvanceAfterRestore: %v", err)
	}
	if state.RestoreEpoch != 8 || state.Activated {
		t.Fatalf("state=%#v", state)
	}
	statements := db.requestCalls[0].statements
	if len(statements) != 7 {
		t.Fatalf("restore statement count=%d, want 7", len(statements))
	}
	attempt := strings.ToLower(statements[1].SQL)
	if !containsAll(attempt, "update backup_rpo_attempts", "phase='superseded'", "pending", "applying", "applied", "unknown") {
		t.Fatalf("restore attempt supersession=%s", attempt)
	}
	backup := strings.ToLower(statements[2].SQL)
	for _, required := range []string{
		"update backup_rpo_state", "restore_epoch=?", "dirty_generation=1", "verified_generation=0",
		"last_attempt_sequence=0", "phase='dirty'", "verified_object_version=null",
	} {
		if !strings.Contains(backup, required) {
			t.Fatalf("restore backup reset lacks %q: %s", required, backup)
		}
	}
	poller := strings.ToLower(statements[5].SQL)
	if !containsAll(poller, "update telegram_pollers", "lease_fence=lease_fence+1", "node_id is not null", "lease_token is not null", "lease_expires_at_unix<>0") {
		t.Fatalf("restore poller replay boundary=%s", poller)
	}
}

func TestTask6RestoreActivationDoesNotDirtyBusinessData(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{restoreStateRow(12, true, strings.Repeat("e", 64))}},
	)}}
	if _, err := NewRestoreEpochStore(db).Activate(context.Background(), 12); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if sql := strings.ToLower(statementsText(db.requestCalls[0].statements)); strings.Contains(sql, "backup_rpo_state") {
		t.Fatalf("activation dirtied backup state: %s", sql)
	}
}
