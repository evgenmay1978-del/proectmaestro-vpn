package controlplane

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	// SchemaVersion is the newest immutable control-plane migration.
	SchemaVersion = 1
	voterCount    = 3

	migrationDelimiter = "-- maestro:statement"
)

//go:embed migrations/0001_control_plane.sql
var migrationFiles embed.FS

var expectedSchemaTables = []string{
	"active_order_guards",
	"audit_events",
	"backup_watermarks",
	"cluster_job_leases",
	"cluster_settings",
	"credentials",
	"customers",
	"desired_node_state",
	"devices",
	"external_actions",
	"health_write_canary",
	"idempotency_requests",
	"import_batches",
	"import_runs",
	"imported_secrets",
	"imported_trial_identities",
	"node_apply_receipts",
	"node_leases",
	"node_services",
	"nodes",
	"operation_batches",
	"operations",
	"orders",
	"outbox_events",
	"payments",
	"principal_credentials",
	"principal_roles",
	"principals",
	"rate_limit_buckets",
	"schema_migrations",
	"setting_members",
	"setting_secrets",
	"subscription_tokens",
	"tariff_versions",
	"telegram_bindings",
	"telegram_bot_credential_rotations",
	"telegram_bot_routes",
	"telegram_callbacks",
	"telegram_delivery_outbox",
	"telegram_inbox",
	"telegram_imported_callbacks",
	"telegram_pollers",
	"tombstone_targets",
	"tombstones",
	"trial_redemptions",
	"web_sessions",
}

// Migrator applies and verifies the immutable HA control-plane schema.
type Migrator struct {
	db  rqlite.RQLite
	now func() time.Time
}

// NewMigrator returns a migration runner using one rqlite cluster interface.
func NewMigrator(db rqlite.RQLite) *Migrator {
	return &Migrator{db: db, now: time.Now}
}

// Apply installs migration 0001 atomically, or verifies an already installed
// copy. A failed or unknown write outcome is resolved only by strong reads.
func (m *Migrator) Apply(ctx context.Context) error {
	if m == nil || m.db == nil {
		return errors.New("controlplane: migration database is required")
	}
	if err := m.verifyForeignKeys(ctx); err != nil {
		return err
	}

	exists, err := m.schemaMigrationTableExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return m.Verify(ctx)
	}

	sqlBytes, checksum, statements, err := loadMigration()
	if err != nil {
		return err
	}
	_ = sqlBytes
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO schema_migrations(version,checksum,applied_at_unix)
			VALUES(?,?,?)`,
		Args: []any{SchemaVersion, checksum, m.now().Unix()},
	})

	if _, err := m.db.Request(ctx, rqlite.Linearizable, true, statements...); err != nil {
		if verifyErr := m.Verify(ctx); verifyErr == nil {
			return nil
		}
		return fmt.Errorf("controlplane: apply schema migration: %w", err)
	}
	return m.Verify(ctx)
}

// Verify fails closed unless every voter has foreign keys enabled, the stored
// checksum is exact, the table set is complete, and SQLite reports no FK
// violations.
func (m *Migrator) Verify(ctx context.Context) error {
	if m == nil || m.db == nil {
		return errors.New("controlplane: migration database is required")
	}
	if err := m.verifyForeignKeys(ctx); err != nil {
		return err
	}
	_, checksum, _, err := loadMigration()
	if err != nil {
		return err
	}

	results, err := m.db.QueryStrong(ctx,
		rqlite.Statement{SQL: "SELECT version,checksum FROM schema_migrations ORDER BY version"},
		rqlite.Statement{SQL: `SELECT name FROM sqlite_master
			WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`},
		rqlite.Statement{SQL: "PRAGMA foreign_key_check"},
	)
	if err != nil {
		return fmt.Errorf("controlplane: verify schema: %w", err)
	}
	if len(results) != 3 {
		return errors.New("controlplane: schema verification result count is invalid")
	}
	if len(results[0].Rows) != 1 || fmt.Sprint(results[0].Rows[0]["version"]) != fmt.Sprint(SchemaVersion) {
		return errors.New("controlplane: schema migration version is invalid")
	}
	storedChecksum, ok := results[0].Rows[0]["checksum"].(string)
	if !ok || storedChecksum != checksum {
		return errors.New("controlplane: schema migration checksum mismatch")
	}
	if err := verifyTableSet(results[1]); err != nil {
		return err
	}
	if len(results[2].Rows) != 0 {
		return errors.New("controlplane: foreign key violations detected")
	}
	return nil
}

func (m *Migrator) verifyForeignKeys(ctx context.Context) error {
	for voter := 0; voter < voterCount; voter++ {
		results, err := m.db.QueryStrong(ctx, rqlite.Statement{SQL: "PRAGMA foreign_keys"})
		if err != nil {
			return fmt.Errorf("controlplane: verify voter foreign keys: %w", err)
		}
		if len(results) != 1 || len(results[0].Rows) != 1 ||
			fmt.Sprint(results[0].Rows[0]["foreign_keys"]) != "1" {
			return errors.New("controlplane: foreign keys are disabled on a voter")
		}
	}
	return nil
}

func (m *Migrator) schemaMigrationTableExists(ctx context.Context) (bool, error) {
	results, err := m.db.QueryStrong(ctx, rqlite.Statement{SQL: `
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='schema_migrations'
	`})
	if err != nil {
		return false, fmt.Errorf("controlplane: inspect migration table: %w", err)
	}
	if len(results) != 1 {
		return false, errors.New("controlplane: migration table query is invalid")
	}
	return len(results[0].Rows) == 1, nil
}

func loadMigration() ([]byte, string, []rqlite.Statement, error) {
	data, err := migrationFiles.ReadFile("migrations/0001_control_plane.sql")
	if err != nil {
		return nil, "", nil, errors.New("controlplane: embedded migration is unavailable")
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])

	parts := strings.Split(string(data), migrationDelimiter)
	statements := make([]rqlite.Statement, 0, len(parts))
	for _, part := range parts {
		sql := strings.TrimSpace(part)
		if sql == "" || strings.HasPrefix(sql, "-- MaestroVPN HA schema") {
			continue
		}
		statements = append(statements, rqlite.Statement{SQL: sql})
	}
	if len(statements) == 0 {
		return nil, "", nil, errors.New("controlplane: embedded migration has no statements")
	}
	return data, checksum, statements, nil
}

func verifyTableSet(result rqlite.Result) error {
	got := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		name, ok := row["name"].(string)
		if !ok || name == "" {
			return errors.New("controlplane: schema table row is invalid")
		}
		got = append(got, name)
	}
	want := append([]string(nil), expectedSchemaTables...)
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		return errors.New("controlplane: schema table set mismatch")
	}
	return nil
}
