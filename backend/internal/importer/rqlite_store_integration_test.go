//go:build rqlite_integration

package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		rqlite.Statement{SQL: `
			SELECT node_id,service_name,generation,desired_sha256,status
			FROM desired_node_state WHERE customer_id=?
			ORDER BY node_id,service_name
		`, Args: []any{plan.Customers[0].InternalID}},
		rqlite.Statement{SQL: `
			SELECT node_id,service_name,protocol_tag
			FROM desired_protocol_tags WHERE customer_id=?
			ORDER BY node_id,service_name,protocol_tag
		`, Args: []any{plan.Customers[0].InternalID}},
	)
	if err != nil {
		t.Fatalf("verify canonical rows: %v", err)
	}
	if len(results) != 6 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 ||
		len(results[2].Rows) != 1 || len(results[3].Rows) != 1 || len(results[4].Rows) != 4 ||
		len(results[5].Rows) != len(plan.Customers[0].NodeIDs)*len(plan.Customers[0].ProtocolTags) {
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
	wantNodes := make(map[string]struct{}, len(plan.Customers[0].NodeIDs))
	wantTuples := make(map[string]struct{}, len(plan.Customers[0].NodeIDs)*len(plan.Customers[0].ProtocolTags))
	for _, nodeID := range plan.Customers[0].NodeIDs {
		wantNodes[nodeID] = struct{}{}
		for _, protocolTag := range plan.Customers[0].ProtocolTags {
			wantTuples[nodeID+"\x00"+protocolTag] = struct{}{}
		}
	}
	for _, row := range results[4].Rows {
		nodeID, _ := row["node_id"].(string)
		if _, exists := wantNodes[nodeID]; !exists || row["service_name"] != "maestro-core" ||
			fmt.Sprint(row["generation"]) != fmt.Sprint(plan.Customers[0].Generation) || row["desired_sha256"] != plan.EncryptedSecrets[0].SHA256 ||
			row["status"] != "pending" {
			t.Fatalf("unexpected desired node row: %#v", row)
		}
		delete(wantNodes, nodeID)
	}
	for _, row := range results[5].Rows {
		nodeID, _ := row["node_id"].(string)
		protocolTag, _ := row["protocol_tag"].(string)
		key := nodeID + "\x00" + protocolTag
		if _, exists := wantTuples[key]; !exists || row["service_name"] != "maestro-core" {
			t.Fatalf("unexpected desired protocol row: %#v", row)
		}
		delete(wantTuples, key)
	}
	if len(wantNodes) != 0 || len(wantTuples) != 0 {
		t.Fatalf("missing desired topology rows: nodes=%v tuples=%v", wantNodes, wantTuples)
	}
	if strings.Contains(target.BusinessDigest, "OrderOwner") {
		t.Fatal("business digest contains plaintext row data")
	}

	securitySnapshot := decodeFixture(t, "settings-principals-v1.json")
	securitySnapshot.EncryptedSecrets = append(securitySnapshot.EncryptedSecrets, standaloneEncryptedSecret())
	securityPlan, securityReport := Plan(securitySnapshot, testPlanOptions())
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
		rqlite.Statement{SQL: `SELECT owner_type,owner_source_key,field,kind,key_version,secret_sha256 FROM imported_secrets WHERE secret_id=?`, Args: []any{standaloneEncryptedSecret().SecretID}},
	)
	if err != nil {
		t.Fatalf("verify security rows: %v", err)
	}
	if len(securityResults) != 6 {
		t.Fatalf("security result count = %d, want 6", len(securityResults))
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
		t.Fatalf("owner-bound security verification mismatch: %#v", securityResults)
	}
	if securityResults[5].Rows[0]["owner_type"] != "legacy_service" ||
		securityResults[5].Rows[0]["owner_source_key"] != "s3-wb" ||
		securityResults[5].Rows[0]["field"] != "token" || securityResults[5].Rows[0]["kind"] != "bearer" ||
		!rqliteIntegerEquals(securityResults[5].Rows[0]["key_version"], 1) ||
		securityResults[5].Rows[0]["secret_sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("standalone secret verification mismatch: %#v", securityResults[5])
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
	oldFingerprint := snapshot.BotBindings[0].TokenFingerprintHMAC
	snapshot.BotBindings[0].TokenFingerprintHMAC = strings.Repeat("5", 64)
	snapshot.BotBindings[0].CredentialVersion = 2
	snapshot.BotCredentialRotations = []LegacyBotCredentialRotation{{
		BotIdentityHMAC: snapshot.BotBindings[0].BotIdentityHMAC,
		OldTokenFingerprintHMAC: oldFingerprint,
		NewTokenFingerprintHMAC: snapshot.BotBindings[0].TokenFingerprintHMAC,
		OldCredentialVersion: 1,
		NewCredentialVersion: 2,
		AuditDigest: strings.Repeat("6", 64),
	}}
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
		rqlite.Statement{SQL: `SELECT old_token_fingerprint_hmac,new_token_fingerprint_hmac,
old_credential_version,new_credential_version FROM telegram_bot_credential_rotations
WHERE audit_digest=?`, Args: []any{snapshot.BotCredentialRotations[0].AuditDigest}},
	)
	if err != nil {
		t.Fatalf("verify bot state: %v", err)
	}
	if len(results) != 4 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 || len(results[2].Rows) != 1 || len(results[3].Rows) != 1 {
		t.Fatalf("bot state results = %#v", results)
	}
	if results[0].Rows[0]["token_fingerprint_hmac"] != snapshot.BotBindings[0].TokenFingerprintHMAC ||
		!rqliteIntegerEquals(results[0].Rows[0]["credential_version"], 2) ||
		!rqliteIntegerEquals(results[1].Rows[0]["offset_value"], 42) ||
		!rqliteIntegerEquals(results[1].Rows[0]["lease_fence"], 11) ||
		results[1].Rows[0]["node_id"] != nil || results[1].Rows[0]["lease_token"] != nil ||
		!rqliteIntegerEquals(results[1].Rows[0]["lease_expires_at_unix"], 0) ||
		results[2].Rows[0]["order_id"] != "legacy-order-callback" ||
		results[2].Rows[0]["action"] != "confirm" ||
		results[2].Rows[0]["state"] != "pending" {
		t.Fatalf("bot state or callback verification mismatch: %#v", results)
	}
	if results[3].Rows[0]["old_token_fingerprint_hmac"] != oldFingerprint ||
		results[3].Rows[0]["new_token_fingerprint_hmac"] != snapshot.BotBindings[0].TokenFingerprintHMAC ||
		!rqliteIntegerEquals(results[3].Rows[0]["old_credential_version"], 1) ||
		!rqliteIntegerEquals(results[3].Rows[0]["new_credential_version"], 2) {
		t.Fatalf("bot rotation verification mismatch: %#v", results[3])
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

func TestRQLiteApplyStoreWritesProtectedLegacyTrialIdentity(t *testing.T) {
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

	trial := legacyTrialFixture()
	trial.SourceKey = "integration-legacy-trial-v1"
	trial.LegacyAnchorHMAC = strings.Repeat("9", 64)
	trial.CurrentHMAC = strings.Repeat("a", 64)
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	snapshot.Trials = []LegacyTrial{trial}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected trial blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	selected := make([]ApplyOperation, 0, 1)
	for _, operation := range operations {
		if operation.Entity == "trial" {
			selected = append(selected, operation)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("trial operations = %#v", selected)
	}
	batch := ApplyBatch{
		RunID: "importer-integration-protected-trial-v1", PlanDigest: plan.PlanDigest, Index: 0,
		Digest: digestBatch(selected), Operations: selected,
	}
	protection := protectedTrialImportFixture()
	store, err := NewRQLiteApplyStoreWithTrialProtection(
		db, func() time.Time { return time.Unix(1_500_000, 0) }, protection,
	)
	if err != nil {
		t.Fatalf("NewRQLiteApplyStoreWithTrialProtection: %v", err)
	}
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: batch.RunID, SnapshotKind: "full", SourceDigest: plan.SourceDigest,
		PlanDigest: plan.PlanDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("trial BeginOrResume: %v", err)
	}
	receipt, err := store.CommitBatch(ctx, batch)
	if err != nil {
		t.Fatalf("trial CommitBatch: %v", err)
	}
	if receipt.Digest != batch.Digest {
		t.Fatalf("trial receipt = %#v", receipt)
	}

	results, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT owner_type,owner_source_key,field,kind,key_version,secret_sha256
FROM imported_secrets WHERE secret_id='legacy-trial-salt-v1'`},
		rqlite.Statement{SQL: `SELECT legacy_anchor_hmac,current_hmac,used,expires_at_unix,lookup_secret_id
FROM imported_trial_identities WHERE source_key=?`, Args: []any{trial.SourceKey}},
		rqlite.Statement{SQL: `SELECT batch_digest,status FROM import_batches
WHERE import_run_id=? AND batch_index=0`, Args: []any{batch.RunID}},
	)
	if err != nil {
		t.Fatalf("verify protected trial rows: %v", err)
	}
	if len(results) != 3 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		t.Fatalf("protected trial results = %#v", results)
	}
	if results[0].Rows[0]["owner_type"] != "trial_lookup" ||
		results[0].Rows[0]["owner_source_key"] != "legacy" ||
		results[0].Rows[0]["field"] != "salt" || results[0].Rows[0]["kind"] != "hmac-key" ||
		!rqliteIntegerEquals(results[0].Rows[0]["key_version"], 1) ||
		results[0].Rows[0]["secret_sha256"] != protection.SaltSHA256 ||
		results[1].Rows[0]["legacy_anchor_hmac"] != trial.LegacyAnchorHMAC ||
		results[1].Rows[0]["current_hmac"] != trial.CurrentHMAC ||
		!rqliteIntegerEquals(results[1].Rows[0]["used"], 0) ||
		!rqliteIntegerEquals(results[1].Rows[0]["expires_at_unix"], trial.ExpiresAtUnix) ||
		results[1].Rows[0]["lookup_secret_id"] != "legacy-trial-salt-v1" ||
		results[2].Rows[0]["batch_digest"] != batch.Digest || results[2].Rows[0]["status"] != "applied" {
		t.Fatalf("protected trial verification mismatch: %#v", results)
	}
}

func TestRQLiteApplyStoreCustomerDeleteRollsBackWrongProofThenCommits(t *testing.T) {
	db, err := rqlite.New(rqlite.Config{
		Endpoints: []string{"http://127.0.0.1:4401", "http://127.0.0.1:4403", "http://127.0.0.1:4405"},
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
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_600_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixtureFromSnapshot(t, base, testPlanOptions())
	commitIntegrationPlan(t, ctx, store, basePlan, "importer-delete-base-run-v1")
	delta := preparedDelta(t, base, basePlan)
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest
	deltaPlan, report := Plan(delta, options)
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected delta blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(deltaPlan)
	if err != nil {
		t.Fatalf("delta planOperations: %v", err)
	}
	validBatch := ApplyBatch{RunID: "importer-delete-delta-run-v1", PlanDigest: deltaPlan.PlanDigest, Index: 0, Operations: operations}
	validBatch.Digest = digestBatch(validBatch.Operations)
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: validBatch.RunID, SnapshotKind: "delta", SourceDigest: deltaPlan.SourceDigest,
		PlanDigest: deltaPlan.PlanDigest, ParentDigest: deltaPlan.ParentSourceDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("delta BeginOrResume: %v", err)
	}
	wrongBatch := validBatch
	wrongBatch.Operations = append([]ApplyOperation(nil), validBatch.Operations...)
	for index := range wrongBatch.Operations {
		operation := &wrongBatch.Operations[index]
		if !operation.Tombstone || operation.Entity != "customer" {
			continue
		}
		var deletion PlannedDelete
		if err := decodeCanonicalOperation(operation.CanonicalJSON, &deletion); err != nil {
			t.Fatalf("decode customer deletion: %v", err)
		}
		deletion.ExpectedPriorDigest = strings.Repeat("f", 64)
		operation.CanonicalJSON, err = json.Marshal(deletion)
		if err != nil {
			t.Fatalf("encode wrong customer deletion: %v", err)
		}
	}
	wrongBatch.Digest = digestBatch(wrongBatch.Operations)
	if _, err := store.CommitBatch(ctx, wrongBatch); err == nil {
		t.Fatal("CommitBatch accepted a wrong prior customer digest")
	}
	betaID := deterministicID(options.Namespace, "customer", "customer-beta")
	unchanged, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT status,generation FROM customers WHERE customer_id=?`, Args: []any{betaID}},
		rqlite.Statement{SQL: `SELECT lifecycle FROM imported_entity_state WHERE entity_kind='customer' AND source_key='customer-beta'`},
		rqlite.Statement{SQL: `SELECT count(*) AS count FROM tombstones WHERE customer_id=?`, Args: []any{betaID}},
		rqlite.Statement{SQL: `SELECT count(*) AS count FROM tombstone_targets tt JOIN tombstones t ON t.tombstone_id=tt.tombstone_id WHERE t.customer_id=?`, Args: []any{betaID}},
		rqlite.Statement{SQL: `SELECT count(*) AS count FROM import_batches WHERE import_run_id=?`, Args: []any{validBatch.RunID}},
	)
	if err != nil || len(unchanged) != 5 || len(unchanged[0].Rows) != 1 || len(unchanged[1].Rows) != 1 ||
		unchanged[0].Rows[0]["status"] != "active" || !rqliteIntegerEquals(unchanged[0].Rows[0]["generation"], 1) ||
		unchanged[1].Rows[0]["lifecycle"] != "active" || !rqliteIntegerEquals(unchanged[2].Rows[0]["count"], 0) ||
		!rqliteIntegerEquals(unchanged[3].Rows[0]["count"], 0) || !rqliteIntegerEquals(unchanged[4].Rows[0]["count"], 0) {
		t.Fatalf("wrong digest was not rolled back atomically: %#v, %v", unchanged, err)
	}
	if _, err := store.CommitBatch(ctx, validBatch); err != nil {
		t.Fatalf("CommitBatch valid customer delete: %v", err)
	}
	wantEnvelope, err := json.Marshal(base.EncryptedSecrets[1])
	if err != nil {
		t.Fatalf("encode expected protected envelope: %v", err)
	}
	deleted, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT status,generation FROM customers WHERE customer_id=?`, Args: []any{betaID}},
		rqlite.Statement{SQL: `SELECT lifecycle FROM imported_entity_state WHERE entity_kind='customer' AND source_key='customer-beta'`},
		rqlite.Statement{SQL: `SELECT lifecycle FROM imported_entity_state WHERE entity_kind='encrypted_secret' AND source_key='secret-beta'`},
		rqlite.Statement{SQL: `SELECT count(*) AS count FROM tombstone_targets WHERE tombstone_id=?`, Args: []any{deltaPlan.Deletes[0].TombstoneID}},
		rqlite.Statement{SQL: `SELECT enabled,secret_envelope FROM credentials WHERE customer_id=?`, Args: []any{betaID}},
		rqlite.Statement{SQL: `SELECT revoked,token_envelope FROM subscription_tokens WHERE customer_id=?`, Args: []any{betaID}},
		rqlite.Statement{SQL: `SELECT tombstone_id FROM import_delete_receipts WHERE entity_kind='encrypted_secret' AND source_key='secret-beta'`},
	)
	if err != nil || len(deleted) != 7 || len(deleted[0].Rows) != 1 || len(deleted[1].Rows) != 1 ||
		len(deleted[2].Rows) != 1 || len(deleted[3].Rows) != 1 || len(deleted[4].Rows) != 1 ||
		len(deleted[5].Rows) != 1 || len(deleted[6].Rows) != 1 ||
		deleted[0].Rows[0]["status"] != "deleted" || !rqliteIntegerEquals(deleted[0].Rows[0]["generation"], 2) ||
		deleted[1].Rows[0]["lifecycle"] != "deleted" || deleted[2].Rows[0]["lifecycle"] != "deleted" ||
		!rqliteIntegerEquals(deleted[3].Rows[0]["count"], 4) || !rqliteIntegerEquals(deleted[4].Rows[0]["enabled"], 0) ||
		deleted[4].Rows[0]["secret_envelope"] != string(wantEnvelope) || !rqliteIntegerEquals(deleted[5].Rows[0]["revoked"], 1) ||
		deleted[5].Rows[0]["token_envelope"] != string(wantEnvelope) || deleted[6].Rows[0]["tombstone_id"] != nil {
		t.Fatalf("valid delete verification mismatch: %#v, %v", deleted, err)
	}
}

func commitIntegrationPlan(t *testing.T, ctx context.Context, store *RQLiteApplyStore, plan ImportPlan, runID string) TargetState {
	t.Helper()
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	batch := ApplyBatch{RunID: runID, PlanDigest: plan.PlanDigest, Index: 0, Operations: operations}
	batch.Digest = digestBatch(batch.Operations)
	if _, err := store.BeginOrResume(ctx, ApplyRun{
		RunID: runID, SnapshotKind: plan.SnapshotKind, SourceDigest: plan.SourceDigest,
		PlanDigest: plan.PlanDigest, ParentDigest: plan.ParentSourceDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("BeginOrResume %s: %v", runID, err)
	}
	if _, err := store.CommitBatch(ctx, batch); err != nil {
		t.Fatalf("CommitBatch %s: %v", runID, err)
	}
	target, err := store.InspectTarget(ctx)
	if err != nil {
		t.Fatalf("InspectTarget %s: %v", runID, err)
	}
	if err := store.Complete(ctx, ApplyCompletion{RunID: runID, SourceDigest: plan.SourceDigest, PlanDigest: plan.PlanDigest, TargetDigest: target.BusinessDigest}); err != nil {
		t.Fatalf("Complete %s: %v", runID, err)
	}
	return target
}

func TestRQLiteImportDeleteDigestPhase(t *testing.T) {
	phase := os.Getenv("MAESTRO_IMPORT_DIGEST_PHASE")
	proofPath := os.Getenv("MAESTRO_IMPORT_DIGEST_PROOF")
	if phase == "" || proofPath == "" {
		t.Skip("dedicated parity phase")
	}
	db, err := rqlite.New(rqlite.Config{
		Endpoints: []string{"http://127.0.0.1:4401", "http://127.0.0.1:4403", "http://127.0.0.1:4405"},
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
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_700_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}

	switch phase {
	case "delta":
		base := decodeFixture(t, "full-then-delta/base-full.json")
		basePlan := plannedFixtureFromSnapshot(t, base, testPlanOptions())
		commitIntegrationPlan(t, ctx, store, basePlan, "importer-parity-base-run-v1")
		delta := preparedDelta(t, base, basePlan)
		options := testPlanOptions()
		options.ParentSnapshot = &base
		options.AppliedParentDigest = basePlan.SourceDigest
		deltaPlan, report := Plan(delta, options)
		if len(report.Blockers) != 0 {
			t.Fatalf("unexpected parity delta blockers: %#v", report.Blockers)
		}
		target := commitIntegrationPlan(t, ctx, store, deltaPlan, "importer-parity-delta-run-v1")
		betaID := deterministicID(options.Namespace, "customer", "customer-beta")
		wantEnvelope, err := json.Marshal(base.EncryptedSecrets[1])
		if err != nil {
			t.Fatalf("encode parity protected envelope: %v", err)
		}
		results, err := db.QueryLinearizable(ctx,
			rqlite.Statement{SQL: `SELECT status FROM customers WHERE customer_id=?`, Args: []any{betaID}},
			rqlite.Statement{SQL: `SELECT count(*) AS count FROM tombstone_targets WHERE tombstone_id=?`, Args: []any{deltaPlan.Deletes[0].TombstoneID}},
			rqlite.Statement{SQL: `SELECT secret_envelope FROM credentials WHERE customer_id=?`, Args: []any{betaID}},
		)
		if err != nil || len(results) != 3 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 ||
			len(results[2].Rows) != 1 || results[0].Rows[0]["status"] != "deleted" ||
			!rqliteIntegerEquals(results[1].Rows[0]["count"], 4) ||
			results[2].Rows[0]["secret_envelope"] != string(wantEnvelope) {
			t.Fatalf("delta parity proof mismatch: %#v, %v", results, err)
		}
		if !validCanonicalSHA256(target.BusinessDigest) {
			t.Fatalf("delta business digest = %q", target.BusinessDigest)
		}
		if err := os.WriteFile(proofPath, []byte(target.BusinessDigest), 0o600); err != nil {
			t.Fatalf("write digest proof: %v", err)
		}
	case "fresh":
		finalPlan := plannedFixture(t, "full-then-delta/final-full.json", testPlanOptions())
		target := commitIntegrationPlan(t, ctx, store, finalPlan, "importer-parity-fresh-run-v1")
		proofBytes, err := os.ReadFile(proofPath)
		if err != nil {
			t.Fatalf("read digest proof: %v", err)
		}
		proof := string(proofBytes)
		if !validCanonicalSHA256(proof) || target.BusinessDigest != proof {
			t.Fatalf("fresh digest %q does not match delta proof %q", target.BusinessDigest, proof)
		}
	default:
		t.Fatalf("unsupported digest phase %q", phase)
	}
}

func rqliteIntegerEquals(value any, want int64) bool {
	got, ok := applyRowInt(value)
	return ok && got == want
}
