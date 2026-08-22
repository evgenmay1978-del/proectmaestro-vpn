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
	if SchemaVersion != 4 || len(migrations) != 4 ||
		migrations[0].Version != 1 || migrations[1].Version != 2 ||
		migrations[2].Version != 3 || migrations[3].Version != 4 {
		t.Fatalf("migrations=%#v", migrations)
	}
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
	tableRows := make([]map[string]any, 0, len(expectedSchemaTables))
	for _, name := range expectedSchemaTables {
		tableRows = append(tableRows, map[string]any{"name": name})
	}
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(map[string]any{
				"version": json.Number("1"), "checksum": migrations[0].Checksum,
			}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: []map[string]any{
					{"version": json.Number("1"), "checksum": migrations[0].Checksum},
					{"version": json.Number("2"), "checksum": migrations[1].Checksum},
					{"version": json.Number("3"), "checksum": migrations[2].Checksum},
					{"version": json.Number("4"), "checksum": migrations[3].Checksum},
				}},
				rqlite.Result{Rows: tableRows},
				rqlite.Result{},
			),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v1 prefix: %v", err)
	}
	if len(db.requestCalls) != 3 {
		t.Fatalf("requests=%d, want v2-v4 transactions", len(db.requestCalls))
	}
	call := db.requestCalls[0]
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	if strings.Contains(sql, "create table customers") ||
		!strings.Contains(sql, "create table cluster_restore_state") {
		t.Fatalf("migration transaction=%s", sql)
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "2" ||
		fmt.Sprint(last.Args[1]) != migrations[1].Checksum {
		t.Fatalf("migration receipt=%#v", last)
	}
	v3 := db.requestCalls[1]
	if v3.level != rqlite.Linearizable || !v3.transaction {
		t.Fatalf("v3 request=%#v", v3)
	}
	v3SQL := strings.ToLower(statementsText(v3.statements))
	if !strings.Contains(v3SQL, "alter table node_leases") || !strings.Contains(v3SQL, "lease_fence") {
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
}
