package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	testDesiredSHA  = "f5f8197e669855d4ced22399d9e922b4c9b66ba020c63f121a57df4d693e7015"
	testObservedSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func desiredFixture(generation int64, digest string) DesiredState {
	return DesiredState{
		CustomerID: "customer-1", NodeID: "s2", ServiceName: "xui",
		OperationID: "operation-1", EventKind: "customer_desired",
		Generation: generation, Payload: Envelope{KeyVersion: 1, Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext")},
		PayloadSHA256: digest,
	}
}

// Break caught: removing the generation guard would let an old retry replace
// a newer absolute expiry and credential snapshot.
func TestDesiredGenerationNeverMovesBackward(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)

	err := service.UpsertDesired(context.Background(), desiredFixture(4, testDesiredSHA))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpsertDesired stale generation error=%v, want ErrConflict", err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("desired write calls=%#v", db.requestCalls)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if !strings.Contains(sql, "excluded.generation > desired_node_state.generation") {
		t.Fatalf("desired transaction lacks monotonic generation guard: %s", sql)
	}
}

// Break caught: accepting the same generation with different encrypted bytes
// would make two agents apply different truth under one generation number.
func TestSameGenerationDifferentPayloadHashConflicts(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)

	err := service.UpsertDesired(context.Background(), desiredFixture(5, testObservedSHA))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("same-generation hash conflict error=%v, want ErrConflict", err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if !strings.Contains(sql, "excluded.desired_sha256 = desired_node_state.desired_sha256") {
		t.Fatalf("desired transaction lacks same-generation hash equality: %s", sql)
	}
}

// Break caught: resetting or reusing a fence during holder handoff would let a
// stale process commit after a replacement agent acquired the service.
func TestLeaseFenceMonotonicallyIncreases(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{rowsScript(map[string]any{
		"node_id": "s2", "service_name": "xui", "holder_id": "agent-new",
		"cluster_epoch": int64(7), "node_incarnation": int64(3),
		"lease_fence": int64(12), "expires_at_unix": int64(2_000_090),
	})}}
	service, _ := testService(t, db)

	lease, err := service.AcquireNodeLease(context.Background(), LeaseRequest{
		NodeID: "s2", ServiceName: "xui", HolderID: "agent-new",
	})
	if err != nil {
		t.Fatalf("AcquireNodeLease: %v", err)
	}
	if lease.LeaseFence != 12 || lease.ClusterEpoch != 7 || lease.NodeIncarnation != 3 {
		t.Fatalf("lease=%#v", lease)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{"unixepoch()", "lease_fence + 1", "returning"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("lease transaction lacks %q: %s", required, sql)
		}
	}
}

// Break caught: a receipt from an expired/stale epoch, incarnation or fence
// must not mark desired state or its outbox event applied.
func TestStaleLeaseCannotRecordReceipt(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	err := service.RecordApplyReceipt(context.Background(), ApplyReceipt{
		ReceiptID: "receipt-1", CustomerID: "customer-1", NodeID: "s2", ServiceName: "xui",
		OperationID: "operation-1", HolderID: "agent-old", Generation: 8,
		ClusterEpoch: 6, NodeIncarnation: 2, LeaseFence: 10,
		DesiredSHA256: testDesiredSHA, ObservedSHA256: testObservedSHA,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("RecordApplyReceipt stale fence error=%v, want ErrConflict", err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{"cluster_epoch", "node_incarnation", "lease_fence", "expires_at_unix > unixepoch()"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("receipt transaction lacks %q: %s", required, sql)
		}
	}
	if strings.Contains(sql, "update customers") || strings.Contains(sql, "expires_at_unix =") {
		t.Fatalf("node receipt mutates business expiry: %s", sql)
	}
}

// Break caught: losing an outbox insert after desired state commits must be
// repairable without reading expiry or credentials from a VPN node.
func TestReconcileRepairsMissingOutboxFromDesiredSnapshot(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{RowsAffected: 1},
	)}}
	service, _ := testService(t, db)
	repaired, err := service.ReconcileNode(context.Background(), ReconcileNodeCommand{
		NodeID: "s2", ServiceName: "xui",
	})
	if err != nil || repaired != 1 {
		t.Fatalf("ReconcileNode repaired=%d err=%v", repaired, err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if !strings.Contains(sql, "insert into outbox_events") ||
		!strings.Contains(sql, "from desired_node_state") ||
		strings.Contains(sql, "from node_apply_receipts") {
		t.Fatalf("reconcile is not driven only by desired truth: %s", sql)
	}
}

// Break caught: treating a fenced/down required node as optional would purge
// credentials before that node has acknowledged the tombstone.
func TestFencedOrDownRequiredNodeBlocksTombstonePurge(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{RowsAffected: 0},
	)}}
	service, _ := testService(t, db)
	err := service.PurgeTombstone(context.Background(), TombstonePurgeCommand{
		TombstoneID: "tombstone-1", CustomerID: "customer-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("PurgeTombstone with required target error=%v, want ErrConflict", err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{"desired_target = 1", "retired = 0", "status <> 'applied'", "unixepoch()"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("tombstone purge lacks %q: %s", required, sql)
		}
	}
}
