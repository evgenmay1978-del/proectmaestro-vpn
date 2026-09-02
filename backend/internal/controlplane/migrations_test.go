//go:build rqlite_integration

package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestMigrationsApplyIdempotentlyAndVerifySchema(t *testing.T) {
	db := mustIntegrationRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator := NewMigrator(db)
	if err := migrator.Apply(ctx); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := migrator.Apply(ctx); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if err := migrator.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	results, err := db.QueryStrong(ctx, rqlite.Statement{SQL: `
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`})
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("table query results = %d, want 1", len(results))
	}

	gotTables := make([]string, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		name, ok := row["name"].(string)
		if !ok || name == "" {
			t.Fatalf("malformed sqlite_master row: %#v", row)
		}
		gotTables = append(gotTables, name)
	}
	wantTables := []string{
		"schema_migrations", "cluster_restore_state", "backup_rpo_state", "backup_rpo_attempts", "customers", "whitelist_entitlement_identities",
		"whitelist_meter_epochs", "whitelist_billing_periods", "whitelist_commercial_metering_sources", "whitelist_commercial_debit_outbox", "whitelist_balance_entries", "whitelist_balance_projections", "whitelist_usage_applications", "whitelist_gb_products", "whitelist_topup_orders", "whitelist_topup_payment_claims", "whitelist_publication_controls", "whitelist_topup_results", "whitelist_renewal_intents",
		"whitelist_metering_periods", "whitelist_metering_checkpoints", "whitelist_metering_events", "whitelist_metering_intervals", "whitelist_metering_projections",
		"credentials", "subscription_tokens", "devices",
		"tariff_versions", "orders", "active_order_guards", "payments", "trial_redemptions",
		"idempotency_requests", "nodes", "node_services", "desired_node_state", "desired_protocol_tags", "outbox_events",
		"node_leases", "cluster_job_leases", "node_apply_receipts", "tombstones", "tombstone_targets",
		"telegram_bot_routes", "telegram_bot_credential_rotations", "telegram_pollers", "telegram_inbox", "telegram_callbacks",
		"telegram_delivery_outbox", "telegram_bindings", "telegram_imported_callbacks", "external_actions", "operations",
		"operation_batches", "cluster_settings", "setting_members", "setting_secrets",
		"principals", "principal_roles", "principal_credentials", "web_sessions",
		"rate_limit_buckets", "import_runs", "import_batches", "imported_secrets", "backup_watermarks",
		"imported_trial_identities", "imported_entity_state", "import_delete_receipts",
		"audit_events", "health_write_canary",
	}
	sort.Strings(wantTables)
	if fmt.Sprint(gotTables) != fmt.Sprint(wantTables) {
		t.Fatalf("tables = %v, want %v", gotTables, wantTables)
	}

	fkResults, err := db.QueryStrong(ctx, rqlite.Statement{SQL: "PRAGMA foreign_key_check"})
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if len(fkResults) != 1 || len(fkResults[0].Rows) != 0 {
		t.Fatalf("foreign_key_check rows = %#v, want none", fkResults)
	}

	checksumResults, err := db.QueryStrong(ctx, rqlite.Statement{
		SQL:  "SELECT checksum FROM schema_migrations WHERE version = ?",
		Args: []any{SchemaVersion},
	})
	if err != nil {
		t.Fatalf("query migration checksum: %v", err)
	}
	if len(checksumResults) != 1 || len(checksumResults[0].Rows) != 1 {
		t.Fatalf("checksum rows = %#v, want one", checksumResults)
	}
	originalChecksum, ok := checksumResults[0].Rows[0]["checksum"].(string)
	if !ok || len(originalChecksum) != 64 {
		t.Fatalf("stored checksum is malformed")
	}

	changedChecksum := strings.Repeat("0", 64)
	if changedChecksum == originalChecksum {
		changedChecksum = strings.Repeat("1", 64)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL:  "UPDATE schema_migrations SET checksum = ? WHERE version = ?",
		Args: []any{changedChecksum, SchemaVersion},
	}); err != nil {
		t.Fatalf("replace migration checksum: %v", err)
	}
	if err := migrator.Verify(ctx); err == nil {
		t.Fatal("Verify accepted a changed migration checksum")
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL:  "UPDATE schema_migrations SET checksum = ? WHERE version = ?",
		Args: []any{originalChecksum, SchemaVersion},
	}); err != nil {
		t.Fatalf("restore migration checksum: %v", err)
	}
	if err := migrator.Verify(ctx); err != nil {
		t.Fatalf("Verify after checksum restore: %v", err)
	}
}

func TestTask7MigrationPreservesDurablePaymentReplayGuard(t *testing.T) {
	db := mustIntegrationRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	const (
		customerID = "task7-migration-sentinel-customer"
		orderID    = "task7-migration-sentinel-order"
		paymentID  = "task7-migration-sentinel-payment"
	)
	loginDigest := sha256.Sum256([]byte(customerID))
	loginKeyHMAC := hex.EncodeToString(loginDigest[:])
	if loginKeyHMAC == strings.Repeat("a", 64) {
		t.Fatal("migration sentinel collides with schema constraint fixture")
	}
	_, err := db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT INTO customers(
customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,'active',2000000,1,1000000,1000000)`, Args: []any{customerID, customerID, loginKeyHMAC}},
		rqlite.Statement{SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,?,?, ?,?,'tariff_1m_v1',40000,'RUB',30,1000000,1086400,'confirmed',
'ready','confirmed',1000100,2000000,1,?)`, Args: []any{orderID, "T7MIG", "migration", strings.Repeat("b", 64), customerID, "task7-migration-sentinel-order-op"}},
		rqlite.Statement{SQL: `INSERT INTO payments(
payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
VALUES(?,?,'migration',NULL,NULL,40000,'RUB',1000100)`, Args: []any{paymentID, orderID}},
		rqlite.Statement{SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,
response_json,created_at_unix,applied_at_unix)
VALUES('migration','legacy-payment','sentinel',?,?, 'payment_confirmed',?,'applied','{}',1000100,1000100)`,
			Args: []any{strings.Repeat("c", 64), paymentID, "task7-migration-sentinel-replay-op"}},
	)
	if err != nil {
		t.Fatalf("insert durable replay sentinel: %v", err)
	}
	if err := NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("replay Apply: %v", err)
	}
	results, err := db.QueryStrong(ctx,
		rqlite.Statement{SQL: `SELECT sql FROM sqlite_master
WHERE type='trigger' AND name='idempotency_applied_resource'`},
		rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM idempotency_requests
WHERE scope='migration' AND command_type='legacy-payment' AND idempotency_key='sentinel'`},
	)
	if err != nil || len(results) != 2 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 {
		t.Fatalf("query replay guard/sentinel: results=%#v err=%v", results, err)
	}
	triggerSQL, ok := results[0].Rows[0]["sql"].(string)
	if !ok || !strings.Contains(triggerSQL, "FROM payments WHERE") || strings.Contains(triggerSQL, "payments_v1") {
		t.Fatalf("durable replay trigger targets wrong table: %q", triggerSQL)
	}
	count, ok := rowInt64(results[1].Rows[0], "n")
	if !ok || count != 1 {
		t.Fatalf("durable replay sentinel count=%#v", results[1].Rows[0]["n"])
	}
}

func mustIntegrationRQLite(t *testing.T) rqlite.RQLite {
	t.Helper()
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
	return db
}

func TestVerifyIdentityReturnsExactCommittedVersionAndChecksum(t *testing.T) {
	db := mustIntegrationRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrator := NewMigrator(db)
	if err := migrator.Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	identity, err := migrator.VerifyIdentity(ctx)
	if err != nil {
		t.Fatalf("VerifyIdentity: %v", err)
	}
	_, checksum, _, err := loadMigration()
	if err != nil {
		t.Fatalf("loadMigration: %v", err)
	}
	if identity.Version != SchemaVersion || identity.Checksum != checksum || len(identity.Checksum) != 64 {
		t.Fatalf("identity=%#v", identity)
	}
}

func TestVerifyIdentityRejectsChangedChecksumWithoutApplying(t *testing.T) {
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: []map[string]any{{
					"version":  int64(SchemaVersion),
					"checksum": strings.Repeat("0", 64),
				}}},
				rqlite.Result{Rows: nil},
				rqlite.Result{Rows: nil},
			),
		},
	}
	if _, err := NewMigrator(db).VerifyIdentity(context.Background()); err == nil {
		t.Fatal("VerifyIdentity accepted a changed checksum")
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("VerifyIdentity performed %d mutation(s)", len(db.requestCalls))
	}
}
