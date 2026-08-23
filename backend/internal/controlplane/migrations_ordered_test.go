package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestOrderedMigrationsExposeExactChain(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	requireExactV5MigrationChain(t, migrations)
	identity, err := combinedMigrationChecksum(migrations)
	if err != nil || len(identity) != 64 {
		t.Fatalf("combined identity=%q err=%v", identity, err)
	}
}

func TestApplyUpgradesExactV1PrefixWithoutReapplyingV1(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV5MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(map[string]any{"version": json.Number("1"), "checksum": migrations[0].Checksum}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v1 prefix: %v", err)
	}
	if len(db.requestCalls) != 4 {
		t.Fatalf("requests=%d, want v2-v5 transactions", len(db.requestCalls))
	}

	v2 := db.requestCalls[0]
	if v2.level != rqlite.Linearizable || !v2.transaction {
		t.Fatalf("v2 request=%#v", v2)
	}
	v2SQL := strings.ToLower(statementsText(v2.statements))
	if strings.Contains(v2SQL, "create table customers") ||
		!strings.Contains(v2SQL, "create table cluster_restore_state") {
		t.Fatalf("v2 migration transaction=%s", v2SQL)
	}
	v2Last := v2.statements[len(v2.statements)-1]
	if len(v2Last.Args) != 3 || fmt.Sprint(v2Last.Args[0]) != "2" ||
		fmt.Sprint(v2Last.Args[1]) != migrations[1].Checksum {
		t.Fatalf("v2 migration receipt=%#v", v2Last)
	}

	v3 := db.requestCalls[1]
	if v3.level != rqlite.Linearizable || !v3.transaction {
		t.Fatalf("v3 request=%#v", v3)
	}
	v3SQL := strings.ToLower(statementsText(v3.statements))
	if !strings.Contains(v3SQL, "alter table node_leases") ||
		!strings.Contains(v3SQL, "lease_fence") {
		t.Fatalf("v3 migration transaction=%s", v3SQL)
	}
	v3Last := v3.statements[len(v3.statements)-1]
	if len(v3Last.Args) != 3 || fmt.Sprint(v3Last.Args[0]) != "3" ||
		fmt.Sprint(v3Last.Args[1]) != migrations[2].Checksum {
		t.Fatalf("v3 migration receipt=%#v", v3Last)
	}

	v4 := db.requestCalls[2]
	if v4.level != rqlite.Linearizable || !v4.transaction {
		t.Fatalf("v4 request=%#v", v4)
	}
	v4SQL := strings.ToLower(statementsText(v4.statements))
	if !strings.Contains(v4SQL, "create table whitelist_entitlement_identities") ||
		!strings.Contains(v4SQL, "customer_id text not null unique") {
		t.Fatalf("v4 migration transaction=%s", v4SQL)
	}
	v4Last := v4.statements[len(v4.statements)-1]
	if len(v4Last.Args) != 3 || fmt.Sprint(v4Last.Args[0]) != "4" ||
		fmt.Sprint(v4Last.Args[1]) != migrations[3].Checksum {
		t.Fatalf("v4 migration receipt=%#v", v4Last)
	}

	v5 := db.requestCalls[3]
	if v5.level != rqlite.Linearizable || !v5.transaction {
		t.Fatalf("v5 request=%#v", v5)
	}
	v5SQL := strings.ToLower(statementsText(v5.statements))
	for _, required := range []string{
		"create table backup_rpo_state",
		"create table backup_rpo_attempts",
		"alter table cluster_job_leases",
	} {
		if !strings.Contains(v5SQL, required) {
			t.Fatalf("v5 migration transaction is missing %q: %s", required, v5SQL)
		}
	}
	v5Last := v5.statements[len(v5.statements)-1]
	if len(v5Last.Args) != 3 || fmt.Sprint(v5Last.Args[0]) != "5" ||
		fmt.Sprint(v5Last.Args[1]) != migrations[4].Checksum {
		t.Fatalf("v5 migration receipt=%#v", v5Last)
	}
}

func TestOrderedMigrationsUpgradeExactV4PrefixAppliesOnlyV5(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV5MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:4])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
		},
		requests: []scriptedResult{resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v4 prefix: %v", err)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("requests=%d, want only v5", len(db.requestCalls))
	}
	v5 := db.requestCalls[0]
	if v5.level != rqlite.Linearizable || !v5.transaction {
		t.Fatalf("v5 request=%#v", v5)
	}
	v5SQL := strings.ToLower(statementsText(v5.statements))
	for _, forbidden := range []string{
		"create table customers",
		"create table cluster_restore_state",
		"create table whitelist_entitlement_identities",
	} {
		if strings.Contains(v5SQL, forbidden) {
			t.Fatalf("v4 prefix reapplied %q: %s", forbidden, v5SQL)
		}
	}
	for _, required := range []string{
		"create table backup_rpo_state",
		"create table backup_rpo_attempts",
		"alter table cluster_job_leases",
	} {
		if !strings.Contains(v5SQL, required) {
			t.Fatalf("v5 migration transaction is missing %q: %s", required, v5SQL)
		}
	}
	last := v5.statements[len(v5.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "5" ||
		fmt.Sprint(last.Args[1]) != migrations[4].Checksum {
		t.Fatalf("v5 receipt=%#v", last)
	}
}

func TestOrderedMigrationsExactV5PrefixIsNoOp(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV5MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations)...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
		},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v5 prefix: %v", err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("exact v5 prefix performed %d migration transaction(s)", len(db.requestCalls))
	}
}

func requireExactV5MigrationChain(t *testing.T, migrations []migration) {
	t.Helper()
	if SchemaVersion != 5 || len(migrations) != 5 ||
		migrations[0].Version != 1 || migrations[1].Version != 2 ||
		migrations[2].Version != 3 || migrations[3].Version != 4 ||
		migrations[4].Version != 5 {
		t.Fatalf(
			"migration v5 is absent or chain is not exactly v1-v5: SchemaVersion=%d migrations=%#v",
			SchemaVersion,
			migrations,
		)
	}
}

func migrationIdentityRows(migrations []migration) []map[string]any {
	rows := make([]map[string]any, 0, len(migrations))
	for _, item := range migrations {
		rows = append(rows, map[string]any{
			"version":  json.Number(fmt.Sprint(item.Version)),
			"checksum": item.Checksum,
		})
	}
	return rows
}

func expectedTableRows() []map[string]any {
	rows := make([]map[string]any, 0, len(expectedSchemaTables))
	for _, name := range expectedSchemaTables {
		rows = append(rows, map[string]any{"name": name})
	}
	return rows
}
