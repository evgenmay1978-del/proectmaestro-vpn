//go:build rqlite_integration

package controlplane

import (
	"context"
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
