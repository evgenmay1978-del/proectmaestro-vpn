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
		!rqliteIntegerEquals(securityResults[0].Rows[0]["generation"], 2) ||
		securityResults[1].Rows[0]["secret_sha256"] != strings.Repeat("3", 64) ||
		!rqliteIntegerEquals(securityResults[1].Rows[0]["key_version"], 1) ||
		securityResults[2].Rows[0]["login_key_hmac"] != strings.Repeat("1", 64) ||
		securityResults[2].Rows[0]["status"] != "active" ||
		securityResults[3].Rows[0]["role_name"] != "owner" ||
		securityResults[4].Rows[0]["verifier_sha256"] != strings.Repeat("2", 64) ||
		!rqliteIntegerEquals(securityResults[4].Rows[0]["active"], 1) {
		t.Fatalf("security verification mismatch: %#v", securityResults)
	}
}

func TestRQLiteApplyStoreWritesBotRoutePollStateAndPendingCallback(t *testing.T) {
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

	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	snapshot.BotPollStates = []LegacyBotPollState{{
		BotIdentityHMAC: snapshot.BotBindings[0].BotIdentityHMAC,
		CurrentTokenFingerprintHMAC: snapshot.BotBindings[0].TokenFingerprintHMAC,
		CredentialVersion: snapshot.BotBindings[0].CredentialVersion,
		NextUpdateID: 42,
		CapturedFence: 11,
	}}
	snapshot.PendingCallbacks = []LegacyCallback{{
		BotIdentityHMAC: snapshot.BotBindings[0].BotIdentityHMAC,
		TokenFingerprintHMAC: snapshot.BotBindings[0].TokenFingerprintHMAC,
		CredentialVersion: snapshot.BotBindings[0].CredentialVersion,
		CallbackHMAC: strings.Repeat("4", 64),
		OrderID: "legacy-order-callback",
		Action: "confirm",
		State: "pending",
	}}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	batch := ApplyBatch{
		RunID: "importer-integration-bot-poll-state-v1", PlanDigest: plan.PlanDigest, Index: 0,
		Digest: digestBatch(operations), Operations: operations,
	}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: batch.RunID, SnapshotKind: "full", SourceDigest: plan.SourceDigest,
		PlanDigest: plan.PlanDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("BeginOrResume: %v", err)
	}
	if _, err := store.CommitBatch(ctx, batch); err != nil {
		t.Fatalf("CommitBatch bot state: %v", err)
	}

	results, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT token_fingerprint_hmac,credential_version
FROM telegram_bot_routes WHERE bot_identity_hmac=?`, Args: []any{snapshot.BotBindings[0].BotIdentityHMAC}},
		rqlite.Statement{SQL: `SELECT offset_value,lease_fence,node_id,lease_token,lease_expires_at_unix
FROM telegram_pollers WHERE bot_identity_hmac=?`, Args: []any{snapshot.BotBindings[0].BotIdentityHMAC}},
		rqlite.Statement{SQL: `SELECT order_id,action,state FROM telegram_imported_callbacks
WHERE callback_hmac=?`, Args: []any{snapshot.PendingCallbacks[0].CallbackHMAC}},
	)
	if err != nil {
		t.Fatalf("verify bot state: %v", err)
	}
	if len(results) != 3 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		t.Fatalf("bot state results = %#v", results)
	}
	if results[0].Rows[0]["token_fingerprint_hmac"] != snapshot.BotBindings[0].TokenFingerprintHMAC ||
		!rqliteIntegerEquals(results[0].Rows[0]["credential_version"], 1) ||
		!rqliteIntegerEquals(results[1].Rows[0]["offset_value"], 42) ||
		!rqliteIntegerEquals(results[1].Rows[0]["lease_fence"], 11) ||
		results[1].Rows[0]["node_id"] != nil || results[1].Rows[0]["lease_token"] != nil ||
		!rqliteIntegerEquals(results[1].Rows[0]["lease_expires_at_unix"], 0) ||
		results[2].Rows[0]["order_id"] != "legacy-order-callback" ||
		results[2].Rows[0]["action"] != "confirm" ||
		results[2].Rows[0]["state"] != "pending" {
		t.Fatalf("bot state verification mismatch: %#v", results)
	}

	mismatchSnapshot := decodeFixture(t, "bot-bindings-v1.json")
	mismatchSnapshot.BotPollStates = []LegacyBotPollState{{
		BotIdentityHMAC: mismatchSnapshot.BotBindings[0].BotIdentityHMAC,
		CurrentTokenFingerprintHMAC: strings.Repeat("3", 64),
		CredentialVersion: mismatchSnapshot.BotBindings[0].CredentialVersion,
		NextUpdateID: 43,
		CapturedFence: 12,
	}}
	mismatchPlan, mismatchReport := Plan(mismatchSnapshot, testPlanOptions())
	if len(mismatchReport.Blockers) != 0 {
		t.Fatalf("unexpected mismatch blockers: %#v", mismatchReport.Blockers)
	}
	mismatchOperations, err := planOperations(mismatchPlan)
	if err != nil {
		t.Fatalf("mismatch planOperations: %v", err)
	}
	selected := mismatchOperations[:0]
	for _, operation := range mismatchOperations {
		if operation.Entity == "bot_poll_state" {
			selected = append(selected, operation)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("mismatch poll operations = %#v", mismatchOperations)
	}
	mismatchBatch := ApplyBatch{
		RunID: "importer-integration-bot-poll-mismatch-v1", PlanDigest: mismatchPlan.PlanDigest, Index: 0,
		Digest: digestBatch(selected), Operations: selected,
	}
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: mismatchBatch.RunID, SnapshotKind: "full", SourceDigest: mismatchPlan.SourceDigest,
		PlanDigest: mismatchPlan.PlanDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("mismatch BeginOrResume: %v", err)
	}
	if _, err := store.CommitBatch(ctx, mismatchBatch); err == nil {
		t.Fatal("CommitBatch accepted poll state for a different credential route")
	}
	unchanged, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT offset_value,lease_fence FROM telegram_pollers WHERE bot_identity_hmac=?`, Args: []any{snapshot.BotBindings[0].BotIdentityHMAC}},
		rqlite.Statement{SQL: `SELECT status FROM import_batches WHERE import_run_id=?`, Args: []any{mismatchBatch.RunID}},
	)
	if err != nil || len(unchanged) != 2 || len(unchanged[0].Rows) != 1 || len(unchanged[1].Rows) != 0 ||
		!rqliteIntegerEquals(unchanged[0].Rows[0]["offset_value"], 42) ||
		!rqliteIntegerEquals(unchanged[0].Rows[0]["lease_fence"], 11) {
		t.Fatalf("mismatched poll state was not rolled back atomically: %#v, %v", unchanged, err)
	}

	callbackMismatchSnapshot := decodeFixture(t, "bot-bindings-v1.json")
	callbackMismatchSnapshot.PendingCallbacks = []LegacyCallback{{
		BotIdentityHMAC: callbackMismatchSnapshot.BotBindings[0].BotIdentityHMAC,
		TokenFingerprintHMAC: strings.Repeat("3", 64),
		CredentialVersion: callbackMismatchSnapshot.BotBindings[0].CredentialVersion,
		CallbackHMAC: snapshot.PendingCallbacks[0].CallbackHMAC,
		OrderID: "legacy-order-callback",
		Action: "confirm",
		State: "in_flight",
	}}
	callbackMismatchPlan, callbackMismatchReport := Plan(callbackMismatchSnapshot, testPlanOptions())
	if len(callbackMismatchReport.Blockers) != 0 {
		t.Fatalf("unexpected callback mismatch blockers: %#v", callbackMismatchReport.Blockers)
	}
	callbackMismatchOperations, err := planOperations(callbackMismatchPlan)
	if err != nil {
		t.Fatalf("callback mismatch planOperations: %v", err)
	}
	callbackSelected := callbackMismatchOperations[:0]
	for _, operation := range callbackMismatchOperations {
		if operation.Entity == "pending_callback" {
			callbackSelected = append(callbackSelected, operation)
		}
	}
	if len(callbackSelected) != 1 {
		t.Fatalf("mismatch callback operations = %#v", callbackMismatchOperations)
	}
	callbackMismatchBatch := ApplyBatch{
		RunID: "importer-integration-bot-callback-mismatch-v1",
		PlanDigest: callbackMismatchPlan.PlanDigest, Index: 0,
		Digest: digestBatch(callbackSelected), Operations: callbackSelected,
	}
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: callbackMismatchBatch.RunID, SnapshotKind: "full",
		SourceDigest: callbackMismatchPlan.SourceDigest,
		PlanDigest: callbackMismatchPlan.PlanDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("callback mismatch BeginOrResume: %v", err)
	}
	if _, err := store.CommitBatch(ctx, callbackMismatchBatch); err == nil {
		t.Fatal("CommitBatch accepted callback for a different credential route")
	}
	callbackUnchanged, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT state FROM telegram_imported_callbacks WHERE callback_hmac=?`, Args: []any{snapshot.PendingCallbacks[0].CallbackHMAC}},
		rqlite.Statement{SQL: `SELECT status FROM import_batches WHERE import_run_id=?`, Args: []any{callbackMismatchBatch.RunID}},
	)
	if err != nil || len(callbackUnchanged) != 2 || len(callbackUnchanged[0].Rows) != 1 ||
		len(callbackUnchanged[1].Rows) != 0 || callbackUnchanged[0].Rows[0]["state"] != "pending" {
		t.Fatalf("mismatched callback was not rolled back atomically: %#v, %v", callbackUnchanged, err)
	}
}

func rqliteIntegerEquals(value any, want int64) bool {
	got, ok := applyRowInt(value)
	return ok && got == want
}
