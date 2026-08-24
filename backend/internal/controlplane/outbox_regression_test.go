package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestDesiredPayloadHashMustMatchEnvelope(t *testing.T) {
	db := &recordingRQLite{}
	service, _ := testService(t, db)
	desired := desiredFixture(3, testObservedSHA)

	if err := service.UpsertDesired(context.Background(), desired); err == nil {
		t.Fatal("mismatched payload hash was accepted")
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("mismatched payload reached database: %#v", db.requestCalls)
	}
}

func TestSameGenerationSameHashDoesNotRewriteOperation(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{},
		rqlite.Result{RowsAffected: 0},
		rqlite.Result{},
		rqlite.Result{Rows: []map[string]any{task6DesiredEvidence()}},
	)}}
	service, _ := testService(t, db)
	if err := service.UpsertDesired(context.Background(), desiredFixture(5, testDesiredSHA)); err != nil {
		t.Fatalf("same generation/hash retry: %v", err)
	}
	sql := strings.ToLower(db.requestCalls[0].statements[0].SQL)
	if !strings.Contains(sql, "where excluded.generation > desired_node_state.generation") ||
		strings.Contains(sql, "or excluded.generation = desired_node_state.generation") {
		t.Fatalf("same-generation retry is not a strict write no-op: %s", sql)
	}
}

func TestDuplicateReceiptIsExactHashNoOp(t *testing.T) {
	db := &recordingRQLite{
		requests: []scriptedResult{resultsScript(
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		)},
		linear: []scriptedResult{rowsScript(map[string]any{
			"receipt_id": "receipt-1", "customer_id": "customer-1",
			"node_id": "s2", "service_name": "xui", "operation_id": "operation-1",
			"generation": int64(8), "cluster_epoch": int64(6),
			"node_incarnation": int64(2), "lease_fence": int64(10),
			"desired_sha256": testDesiredSHA, "observed_sha256": testObservedSHA,
		})},
	}
	service, _ := testService(t, db)
	err := service.RecordApplyReceipt(context.Background(), ApplyReceipt{
		ReceiptID: "receipt-1", CustomerID: "customer-1", NodeID: "s2", ServiceName: "xui",
		OperationID: "operation-1", HolderID: "agent", Generation: 8,
		ClusterEpoch: 6, NodeIncarnation: 2, LeaseFence: 10,
		DesiredSHA256: testDesiredSHA, ObservedSHA256: testObservedSHA,
	})
	if err != nil {
		t.Fatalf("exact duplicate receipt retry: %v", err)
	}
	if len(db.linearCalls) != 1 {
		t.Fatalf("duplicate receipt exact-read calls=%d, want 1", len(db.linearCalls))
	}
}

func TestAppliedTombstoneReceiptAcknowledgesRequiredTarget(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"receipt_id": "receipt-1"}}},
		rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1},
	)}}
	service, _ := testService(t, db)
	err := service.RecordApplyReceipt(context.Background(), ApplyReceipt{
		ReceiptID: "receipt-1", CustomerID: "customer-1", NodeID: "s2", ServiceName: "xui",
		OperationID: "operation-1", HolderID: "agent", Generation: 8,
		ClusterEpoch: 6, NodeIncarnation: 2, LeaseFence: 10,
		DesiredSHA256: testDesiredSHA, ObservedSHA256: testObservedSHA,
	})
	if err != nil {
		t.Fatalf("RecordApplyReceipt: %v", err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{"update tombstone_targets", "tombstone=1", "applied_at_unix=unixepoch()"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("receipt does not acknowledge tombstone target %q: %s", required, sql)
		}
	}
}

func TestTombstoneRetentionStartsAfterLastRequiredAck(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{RowsAffected: 0}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	err := service.PurgeTombstone(context.Background(), TombstonePurgeCommand{
		TombstoneID: "tombstone-1", CustomerID: "customer-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("PurgeTombstone error=%v, want ErrConflict", err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	if !strings.Contains(sql, "having max(tt.applied_at_unix) <= unixepoch()-7776000") {
		t.Fatalf("retention is not measured from last required ack: %s", sql)
	}
}
