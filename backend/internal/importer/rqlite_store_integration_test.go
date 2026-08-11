//go:build rqlite_integration

package importer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRQLiteApplyStoreWritesCanonicalRowsAndDurableReceipt(t *testing.T) {
	db, err := rqlite.New(rqlite.Config{
		Endpoints: []string{
			"http://127.0.0.1:4401",
			"http://127.0.0.1:4403",
			"http://127.0.0.1:4405",
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("rqlite.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	plan, report := Plan(decodeFixture(t, "orders-pending-credited.json"), testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	selected := make([]ApplyOperation, 0, 2)
	for _, operation := range operations {
		if operation.Entity == "customer" ||
			(operation.Entity == "order" && operation.Key == "legacy-order-credited") {
			selected = append(selected, operation)
		}
	}
	batch := ApplyBatch{
		RunID: "importer-integration-run-v1", PlanDigest: plan.PlanDigest, Index: 0,
		Digest: digestBatch(selected), Operations: selected,
	}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	progress, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: batch.RunID, SnapshotKind: "full", SourceDigest: plan.SourceDigest,
		PlanDigest: plan.PlanDigest, BatchCount: 1,
	})
	if err != nil {
		t.Fatalf("BeginOrResume: %v", err)
	}
	if !progress.New && !progress.Completed && len(progress.AppliedBatchDigests) == 0 {
		t.Fatal("run was neither created nor resumed")
	}
	receipt, err := store.CommitBatch(ctx, batch)
	if err != nil {
		t.Fatalf("CommitBatch: %v", err)
	}
	if receipt.Digest != batch.Digest {
		t.Fatalf("receipt = %#v", receipt)
	}
	target, err := store.InspectTarget(ctx)
	if err != nil {
		t.Fatalf("InspectTarget: %v", err)
	}
	if target.Empty || target.BusinessDigest == "" {
		t.Fatalf("target = %#v", target)
	}
	if err := store.Complete(ctx, ApplyCompletion{
		RunID: batch.RunID, SourceDigest: plan.SourceDigest,
		PlanDigest: plan.PlanDigest, TargetDigest: target.BusinessDigest,
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	results, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT display_login FROM customers WHERE customer_id=?`, Args: []any{plan.Customers[0].InternalID}},
		rqlite.Statement{SQL: `SELECT payment_code FROM orders WHERE order_id=?`, Args: []any{plan.Orders[0].InternalID}},
		rqlite.Statement{SQL: `SELECT batch_digest,status FROM import_batches WHERE import_run_id=? AND batch_index=0`, Args: []any{batch.RunID}},
		rqlite.Statement{SQL: `SELECT target_sha256,status FROM import_runs WHERE import_run_id=?`, Args: []any{batch.RunID}},
	)
	if err != nil {
		t.Fatalf("verify canonical rows: %v", err)
	}
	if len(results) != 4 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 ||
		len(results[2].Rows) != 1 || len(results[3].Rows) != 1 {
		t.Fatalf("verification results = %#v", results)
	}
	if results[0].Rows[0]["display_login"] != "OrderOwner" ||
		results[1].Rows[0]["payment_code"] != "MCRD1" ||
		results[2].Rows[0]["batch_digest"] != batch.Digest ||
		results[2].Rows[0]["status"] != "applied" ||
		results[3].Rows[0]["target_sha256"] != target.BusinessDigest ||
		results[3].Rows[0]["status"] != "applied" {
		t.Fatalf("canonical verification mismatch: %#v", results)
	}
	if strings.Contains(target.BusinessDigest, "OrderOwner") {
		t.Fatal("business digest contains plaintext row data")
	}
}
