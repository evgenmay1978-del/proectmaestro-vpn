package controlplane

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	// SchemaVersion is the newest immutable control-plane migration.
	SchemaVersion = 2
	voterCount    = 3

	migrationDelimiter = "-- maestro:statement"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var expectedSchemaTables = []string{
	"active_order_guards",
	"audit_events",
	"backup_watermarks",
	"cluster_job_leases",
	"cluster_restore_state",
	"cluster_settings",
	"credentials",
	"customers",
	"desired_node_state",
	"desired_protocol_tags",
	"devices",
	"external_actions",
	"health_write_canary",
	"idempotency_requests",
	"import_batches",
	"import_delete_receipts",
	"import_runs",
	"imported_entity_state",
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

type migration struct {
	Version    int
	Path       string
	Data       []byte
	Checksum   string
	Statements []rqlite.Statement
}

type migrationIdentity struct {
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
}

// SchemaIdentity is the exact immutable migration identity verified on the
// target cluster.
type SchemaIdentity struct {
	Version  int
	Checksum string
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

// Apply validates the committed prefix and applies each missing migration once.
// A failed write is accepted only if a strong read confirms its exact receipt.
func (m *Migrator) Apply(ctx context.Context) error {
	if m == nil || m.db == nil {
		return errors.New("controlplane: migration database is required")
	}
	if err := m.verifyForeignKeys(ctx); err != nil {
		return err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	exists, err := m.schemaMigrationTableExists(ctx)
	if err != nil {
		return err
	}
	applied := []migrationIdentity{}
	if exists {
		applied, err = m.readMigrationIdentities(ctx)
		if err != nil {
			return err
		}
	}
	if err := verifyMigrationPrefix(applied, migrations); err != nil {
		return err
	}
	for index := len(applied); index < len(migrations); index++ {
		item := migrations[index]
		statements := append([]rqlite.Statement(nil), item.Statements...)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO schema_migrations(version,checksum,applied_at_unix)
				VALUES(?,?,?)`,
			Args: []any{item.Version, item.Checksum, m.now().Unix()},
		})
		if _, requestErr := m.db.Request(ctx, rqlite.Linearizable, true, statements...); requestErr != nil {
			exact, readErr := m.migrationRecordedExactly(ctx, item)
			if readErr == nil && exact {
				continue
			}
			return fmt.Errorf("controlplane: apply schema migration %d: %w", item.Version, requestErr)
		}
	}
	return m.Verify(ctx)
}

// Verify fails closed unless every voter has foreign keys enabled, the stored
// checksum is exact, the table set is complete, and SQLite reports no FK
// violations.
func (m *Migrator) Verify(ctx context.Context) error {
	_, err := m.VerifyIdentity(ctx)
	return err
}

// VerifyIdentity performs the same read-only verification and returns the
// canonical identity of the complete ordered chain.
func (m *Migrator) VerifyIdentity(ctx context.Context) (SchemaIdentity, error) {
	if m == nil || m.db == nil {
		return SchemaIdentity{}, errors.New("controlplane: migration database is required")
	}
	if err := m.verifyForeignKeys(ctx); err != nil {
		return SchemaIdentity{}, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return SchemaIdentity{}, err
	}
	results, err := m.db.QueryStrong(ctx,
		rqlite.Statement{SQL: "SELECT version,checksum FROM schema_migrations ORDER BY version"},
		rqlite.Statement{SQL: `SELECT name FROM sqlite_master
			WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`},
		rqlite.Statement{SQL: "PRAGMA foreign_key_check"},
	)
	if err != nil {
		return SchemaIdentity{}, fmt.Errorf("controlplane: verify schema: %w", err)
	}
	if len(results) != 3 {
		return SchemaIdentity{}, errors.New("controlplane: schema verification result count is invalid")
	}
	stored, err := identitiesFromRows(results[0].Rows)
	if err != nil || len(stored) != len(migrations) {
		return SchemaIdentity{}, errors.New("controlplane: schema migration chain is invalid")
	}
	if err := verifyMigrationPrefix(stored, migrations); err != nil {
		return SchemaIdentity{}, err
	}
	if err := verifyTableSet(results[1]); err != nil {
		return SchemaIdentity{}, err
	}
	if len(results[2].Rows) != 0 {
		return SchemaIdentity{}, errors.New("controlplane: foreign key violations detected")
	}
	checksum, err := combinedMigrationChecksum(migrations)
	if err != nil {
		return SchemaIdentity{}, err
	}
	return SchemaIdentity{Version: SchemaVersion, Checksum: checksum}, nil
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

func (m *Migrator) readMigrationIdentities(ctx context.Context) ([]migrationIdentity, error) {
	results, err := m.db.QueryStrong(ctx, rqlite.Statement{
		SQL: "SELECT version,checksum FROM schema_migrations ORDER BY version",
	})
	if err != nil || len(results) != 1 {
		return nil, errors.New("controlplane: inspect migration chain")
	}
	return identitiesFromRows(results[0].Rows)
}

func (m *Migrator) migrationRecordedExactly(ctx context.Context, item migration) (bool, error) {
	results, err := m.db.QueryStrong(ctx, rqlite.Statement{
		SQL: "SELECT version,checksum FROM schema_migrations WHERE version=?",
		Args: []any{item.Version},
	})
	if err != nil || len(results) != 1 {
		return false, err
	}
	identities, err := identitiesFromRows(results[0].Rows)
	return err == nil && len(identities) == 1 &&
		identities[0].Version == item.Version &&
		identities[0].Checksum == item.Checksum, err
}

func loadMigrations() ([]migration, error) {
	specs := []struct {
		version int
		path    string
	}{
		{version: 1, path: "migrations/0001_control_plane.sql"},
		{version: 2, path: "migrations/0002_restore_epoch.sql"},
	}
	migrations := make([]migration, 0, len(specs))
	for _, spec := range specs {
		data, err := migrationFiles.ReadFile(spec.path)
		if err != nil {
			return nil, errors.New("controlplane: embedded migration is unavailable")
		}
		sum := sha256.Sum256(data)
		statements := splitMigrationStatements(data)
		if len(statements) == 0 {
			return nil, errors.New("controlplane: embedded migration has no statements")
		}
		migrations = append(migrations, migration{
			Version: spec.version, Path: spec.path, Data: data,
			Checksum: hex.EncodeToString(sum[:]), Statements: statements,
		})
	}
	return migrations, nil
}

func loadMigration() ([]byte, string, []rqlite.Statement, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return nil, "", nil, err
	}
	var data []byte
	var statements []rqlite.Statement
	for _, item := range migrations {
		data = append(data, item.Data...)
		statements = append(statements, item.Statements...)
	}
	checksum, err := combinedMigrationChecksum(migrations)
	return data, checksum, statements, err
}

func splitMigrationStatements(data []byte) []rqlite.Statement {
	parts := strings.Split(string(data), migrationDelimiter)
	statements := make([]rqlite.Statement, 0, len(parts))
	for _, part := range parts {
		sql := strings.TrimSpace(part)
		if sql == "" || strings.HasPrefix(sql, "-- MaestroVPN HA") {
			continue
		}
		statements = append(statements, rqlite.Statement{SQL: sql})
	}
	return statements
}

func identitiesFromRows(rows []map[string]any) ([]migrationIdentity, error) {
	identities := make([]migrationIdentity, 0, len(rows))
	for _, row := range rows {
		version, ok := restoreInteger(row["version"])
		checksum, checksumOK := row["checksum"].(string)
		if !ok || version <= 0 || !checksumOK || !canonicalRestoreHex(checksum) {
			return nil, errors.New("controlplane: schema migration row is invalid")
		}
		identities = append(identities, migrationIdentity{
			Version: int(version), Checksum: checksum,
		})
	}
	return identities, nil
}

func verifyMigrationPrefix(stored []migrationIdentity, migrations []migration) error {
	if len(stored) > len(migrations) {
		return errors.New("controlplane: unknown schema migration")
	}
	for index, identity := range stored {
		item := migrations[index]
		if identity.Version != index+1 || item.Version != index+1 {
			return errors.New("controlplane: schema migration gap")
		}
		if identity.Checksum != item.Checksum {
			return errors.New("controlplane: schema migration checksum mismatch")
		}
	}
	return nil
}

func combinedMigrationChecksum(migrations []migration) (string, error) {
	identities := make([]migrationIdentity, 0, len(migrations))
	for _, item := range migrations {
		identities = append(identities, migrationIdentity{
			Version: item.Version, Checksum: item.Checksum,
		})
	}
	canonical, err := json.Marshal(identities)
	if err != nil {
		return "", errors.New("controlplane: encode migration identity")
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
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
