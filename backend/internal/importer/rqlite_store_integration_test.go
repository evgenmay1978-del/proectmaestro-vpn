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

	snapshot := decodeFixture(t, "orders-pending-credited.json")
	snapshot.Orders[0].PaymentCode = "MCRD-RQLITE-E2E"
	plan, report := Plan(snapshot, testPlanOptions())
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
		results[1].Rows[0]["payment_code"] != "MCRD-RQLITE-E2E" ||
		results[2].Rows[0]["batch_digest"] != batch.Digest ||
		results[2].Rows[0]["status"] != "applied" ||
		results[3].Rows[0]["target_sha256"] != target.BusinessDigest ||
		results[3].Rows[0]["status"] != "applied" {
		t.Fatalf("canonical verification mismatch: %#v", results)
	}
	if strings.Contains(target.BusinessDigest, "OrderOwner") {
		t.Fatal("business digest contains plaintext row data")
	}

	securityPlan, securityReport := Plan(decodeFixture(t, "settings-principals-v1.json"), testPlanOptions())
	if len(securityReport.Blockers) != 0 {
		t.Fatalf("unexpected security blockers: %#v", securityReport.Blockers)
	}
	securityOperations, err := planOperations(securityPlan)
	if err != nil {
		t.Fatalf("security planOperations: %v", err)
	}
	securityBatch := ApplyBatch{
		RunID: "importer-integration-security-run-v1", PlanDigest: securityPlan.PlanDigest, Index: 0,
		Digest: digestBatch(securityOperations), Operations: securityOperations,
	}
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: securityBatch.RunID, SnapshotKind: "full", SourceDigest: securityPlan.SourceDigest,
		PlanDigest: securityPlan.PlanDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("security BeginOrResume: %v", err)
	}
	if _, err := store.CommitBatch(ctx, securityBatch); err != nil {
		t.Fatalf("security CommitBatch: %v", err)
	}
	securityTarget, err := store.InspectTarget(ctx)
	if err != nil {
		t.Fatalf("security InspectTarget: %v", err)
	}
	if err := store.Complete(ctx, ApplyCompletion{
		RunID: securityBatch.RunID, SourceDigest: securityPlan.SourceDigest,
		PlanDigest: securityPlan.PlanDigest, TargetDigest: securityTarget.BusinessDigest,
	}); err != nil {
		t.Fatalf("security Complete: %v", err)
	}

	securityResults, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT public_value_json,generation FROM cluster_settings WHERE setting_key='telegram'`},
		rqlite.Statement{SQL: `SELECT secret_sha256,key_version FROM setting_secrets WHERE setting_key='telegram'`},
		rqlite.Statement{SQL: `SELECT login_key_hmac,status FROM principals WHERE principal_id=?`, Args: []any{securityPlan.Principals[0].InternalID}},
		rqlite.Statement{SQL: `SELECT role_name FROM principal_roles WHERE principal_id=?`, Args: []any{securityPlan.Principals[0].InternalID}},
		rqlite.Statement{SQL: `SELECT verifier_sha256,active FROM principal_credentials WHERE principal_id=?`, Args: []any{securityPlan.Principals[0].InternalID}},
	)
	if err != nil {
		t.Fatalf("verify security rows: %v", err)
	}
	for index, result := range securityResults {
		if len(result.Rows) != 1 {
			t.Fatalf("security result %d = %#v", index, result)
		}
	}
	if securityResults[0].Rows[0]["public_value_json"] != `{"enabled":true}` ||
		securityResults[0].Rows[0]["generation"] != float64(2) ||
		securityResults[1].Rows[0]["secret_sha256"] != strings.Repeat("3", 64) ||
		securityResults[1].Rows[0]["key_version"] != float64(1) ||
		securityResults[2].Rows[0]["login_key_hmac"] != strings.Repeat("1", 64) ||
		securityResults[2].Rows[0]["status"] != "active" ||
		securityResults[3].Rows[0]["role_name"] != "owner" ||
		securityResults[4].Rows[0]["verifier_sha256"] != strings.Repeat("2", 64) ||
		securityResults[4].Rows[0]["active"] != float64(1) {
		t.Fatalf("security verification mismatch: %#v", securityResults)
	}
}
