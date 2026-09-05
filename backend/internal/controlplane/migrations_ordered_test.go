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
	requireExactV15MigrationChain(t, migrations)
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
	requireExactV6MigrationChain(t, migrations)
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
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v1 prefix: %v", err)
	}
	if len(db.requestCalls) != 16 {
		t.Fatalf("requests=%d, want v2-v17 transactions", len(db.requestCalls))
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
	v6 := db.requestCalls[4]
	if v6.level != rqlite.Linearizable || !v6.transaction {
		t.Fatalf("v6 request=%#v", v6)
	}
	v6SQL := strings.ToLower(statementsText(v6.statements))
	if !strings.Contains(v6SQL, "alter table cluster_settings") ||
		!strings.Contains(v6SQL, "last_mutation_token") {
		t.Fatalf("v6 migration transaction=%s", v6SQL)
	}
	v6Last := v6.statements[len(v6.statements)-1]
	if len(v6Last.Args) != 3 || fmt.Sprint(v6Last.Args[0]) != "6" ||
		fmt.Sprint(v6Last.Args[1]) != migrations[5].Checksum {
		t.Fatalf("v6 migration receipt=%#v", v6Last)
	}
	requireV7MigrationRequest(t, db.requestCalls[5], migrations[6])
	requireV8MigrationRequest(t, db.requestCalls[6], migrations[7])
	requireV9MigrationRequest(t, db.requestCalls[7], migrations[8])
	requireV10MigrationRequest(t, db.requestCalls[8], migrations[9])
	requireV11MigrationRequest(t, db.requestCalls[9], migrations[10])
	requireV12MigrationRequest(t, db.requestCalls[10], migrations[11])
	requireV13MigrationRequest(t, db.requestCalls[11], migrations[12])
	requireV14MigrationRequest(t, db.requestCalls[12], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[13], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[14], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[15], migrations[16])
}

func TestOrderedMigrationsUpgradeExactV4PrefixAppliesV5ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV6MigrationChain(t, migrations)
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
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v4 prefix: %v", err)
	}
	if len(db.requestCalls) != 13 {
		t.Fatalf("requests=%d, want v5 through v17", len(db.requestCalls))
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
	v5Last := v5.statements[len(v5.statements)-1]
	if len(v5Last.Args) != 3 || fmt.Sprint(v5Last.Args[0]) != "5" ||
		fmt.Sprint(v5Last.Args[1]) != migrations[4].Checksum {
		t.Fatalf("v5 receipt=%#v", v5Last)
	}

	v6 := db.requestCalls[1]
	if v6.level != rqlite.Linearizable || !v6.transaction {
		t.Fatalf("v6 request=%#v", v6)
	}
	v6SQL := strings.ToLower(statementsText(v6.statements))
	if !strings.Contains(v6SQL, "alter table cluster_settings") ||
		!strings.Contains(v6SQL, "last_mutation_token") {
		t.Fatalf("v6 migration transaction=%s", v6SQL)
	}
	v6Last := v6.statements[len(v6.statements)-1]
	if len(v6Last.Args) != 3 || fmt.Sprint(v6Last.Args[0]) != "6" ||
		fmt.Sprint(v6Last.Args[1]) != migrations[5].Checksum {
		t.Fatalf("v6 receipt=%#v", v6Last)
	}
	requireV7MigrationRequest(t, db.requestCalls[2], migrations[6])
	requireV8MigrationRequest(t, db.requestCalls[3], migrations[7])
	requireV9MigrationRequest(t, db.requestCalls[4], migrations[8])
	requireV10MigrationRequest(t, db.requestCalls[5], migrations[9])
	requireV11MigrationRequest(t, db.requestCalls[6], migrations[10])
	requireV12MigrationRequest(t, db.requestCalls[7], migrations[11])
	requireV13MigrationRequest(t, db.requestCalls[8], migrations[12])
	requireV14MigrationRequest(t, db.requestCalls[9], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[10], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[11], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[12], migrations[16])
}

func TestOrderedMigrationsExactV5PrefixAppliesV6ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV6MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:5])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v5 prefix: %v", err)
	}
	if len(db.requestCalls) != 12 {
		t.Fatalf("exact v5 prefix performed %d migration transaction(s), want twelve", len(db.requestCalls))
	}
	v6 := db.requestCalls[0]
	v6SQL := strings.ToLower(statementsText(v6.statements))
	if !strings.Contains(v6SQL, "alter table cluster_settings") ||
		!strings.Contains(v6SQL, "last_mutation_token") {
		t.Fatalf("v6 migration transaction=%s", v6SQL)
	}
	last := v6.statements[len(v6.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "6" ||
		fmt.Sprint(last.Args[1]) != migrations[5].Checksum {
		t.Fatalf("v6 receipt=%#v", last)
	}
	requireV7MigrationRequest(t, db.requestCalls[1], migrations[6])
	requireV8MigrationRequest(t, db.requestCalls[2], migrations[7])
	requireV9MigrationRequest(t, db.requestCalls[3], migrations[8])
	requireV10MigrationRequest(t, db.requestCalls[4], migrations[9])
	requireV11MigrationRequest(t, db.requestCalls[5], migrations[10])
	requireV12MigrationRequest(t, db.requestCalls[6], migrations[11])
	requireV13MigrationRequest(t, db.requestCalls[7], migrations[12])
	requireV14MigrationRequest(t, db.requestCalls[8], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[9], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[10], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[11], migrations[16])
}

func TestOrderedMigrationsExactV10PrefixAppliesV11ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV15MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:10])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v10 prefix: %v", err)
	}
	if len(db.requestCalls) != 7 {
		t.Fatalf("exact v10 prefix performed %d migration transaction(s), want v11 through v17", len(db.requestCalls))
	}
	requireV11MigrationRequest(t, db.requestCalls[0], migrations[10])
	requireV12MigrationRequest(t, db.requestCalls[1], migrations[11])
	requireV13MigrationRequest(t, db.requestCalls[2], migrations[12])
	requireV14MigrationRequest(t, db.requestCalls[3], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[4], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[5], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[6], migrations[16])
}

func TestOrderedMigrationsExactV11PrefixAppliesV12ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV15MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:11])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v11 prefix: %v", err)
	}
	if len(db.requestCalls) != 6 {
		t.Fatalf("exact v11 prefix performed %d migration transaction(s), want v12 through v17", len(db.requestCalls))
	}
	requireV12MigrationRequest(t, db.requestCalls[0], migrations[11])
	requireV13MigrationRequest(t, db.requestCalls[1], migrations[12])
	requireV14MigrationRequest(t, db.requestCalls[2], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[3], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[4], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[5], migrations[16])
}

func TestOrderedMigrationsExactV12PrefixAppliesV13ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV15MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:12])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v12 prefix: %v", err)
	}
	if len(db.requestCalls) != 5 {
		t.Fatalf("exact v12 prefix performed %d migration transaction(s), want v13 through v17", len(db.requestCalls))
	}
	requireV13MigrationRequest(t, db.requestCalls[0], migrations[12])
	requireV14MigrationRequest(t, db.requestCalls[1], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[2], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[3], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[4], migrations[16])
}

func TestOrderedMigrationsExactV13PrefixAppliesV14ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV15MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:13])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v13 prefix: %v", err)
	}
	if len(db.requestCalls) != 4 {
		t.Fatalf("exact v13 prefix performed %d migration transaction(s), want v14 through v17", len(db.requestCalls))
	}
	requireV14MigrationRequest(t, db.requestCalls[0], migrations[13])
	requireV15MigrationRequest(t, db.requestCalls[1], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[2], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[3], migrations[16])
}

func TestOrderedMigrationsExactV14PrefixAppliesV15ThroughV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV15MigrationChain(t, migrations)
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:14])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript(), resultsScript(), resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v14 prefix: %v", err)
	}
	if len(db.requestCalls) != 3 {
		t.Fatalf("exact v14 prefix performed %d migration transaction(s), want v15 through v17", len(db.requestCalls))
	}
	requireV15MigrationRequest(t, db.requestCalls[0], migrations[14])
	requireV16MigrationRequest(t, db.requestCalls[1], migrations[15])
	requireV17MigrationRequest(t, db.requestCalls[2], migrations[16])
}

func TestOrderedMigrationsExactV16PrefixAppliesOnlyV17(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	requireExactV15MigrationChain(t, migrations)
	if migrations[15].Path != "migrations/0016_external_action_not_sent_retry.sql" ||
		migrations[16].Path != "migrations/0017_whitelist_metering_observations.sql" {
		t.Fatal("v16-v17 migration paths changed")
	}
	db := &recordingRQLite{
		strong: []scriptedResult{
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"name": "schema_migrations"}),
			rowsScript(migrationIdentityRows(migrations[:16])...),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			rowsScript(map[string]any{"foreign_keys": int64(1)}),
			resultsScript(
				rqlite.Result{Rows: migrationIdentityRows(migrations)},
				rqlite.Result{Rows: expectedTableRows()},
				rqlite.Result{},
			),
			rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
			rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
		},
		requests: []scriptedResult{resultsScript()},
	}
	if err := NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("Apply v16 prefix: %v", err)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("exact v16 prefix performed %d migration transaction(s), want only v17", len(db.requestCalls))
	}
	requireV17MigrationRequest(t, db.requestCalls[0], migrations[16])
}

func TestVerifyIdentityRejectsMissingOrChangedCriticalWhiteListTrigger(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	validRows := expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])
	tests := []struct {
		name string
		rows []map[string]any
	}{
		{name: "missing", rows: validRows[:len(validRows)-1]},
		{name: "unexpected", rows: append(
			append([]map[string]any(nil), validRows...),
			map[string]any{
				"name": "arbitrary_v14_guard",
				"sql":  "CREATE TRIGGER arbitrary_v14_guard AFTER INSERT ON whitelist_topup_orders BEGIN SELECT 1; END",
			},
		)},
		{name: "changed", rows: func() []map[string]any {
			rows := make([]map[string]any, len(validRows))
			for index, row := range validRows {
				rows[index] = map[string]any{"name": row["name"], "sql": row["sql"]}
			}
			rows[0]["sql"] = "CREATE TRIGGER whitelist_commercial_metering_sources_exact_binding BEFORE INSERT ON whitelist_commercial_metering_sources BEGIN SELECT 1; END"
			return rows
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{strong: []scriptedResult{
				rowsScript(map[string]any{"foreign_keys": int64(1)}),
				rowsScript(map[string]any{"foreign_keys": int64(1)}),
				rowsScript(map[string]any{"foreign_keys": int64(1)}),
				resultsScript(
					rqlite.Result{Rows: migrationIdentityRows(migrations)},
					rqlite.Result{Rows: expectedTableRows()},
					rqlite.Result{},
				),
				rowsScript(test.rows...),
			}}
			if _, err := NewMigrator(db).VerifyIdentity(context.Background()); err == nil {
				t.Fatal("VerifyIdentity accepted critical white-list trigger drift")
			}
			if len(db.requestCalls) != 0 {
				t.Fatalf("VerifyIdentity performed %d mutation(s)", len(db.requestCalls))
			}
		})
	}
}

func TestVerifyIdentityRejectsMissingOrChangedCriticalWhiteListTopUpTable(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	validRows := expectedWhiteListTopUpTableRows(t, migrations[13])
	cloneRows := func() []map[string]any {
		rows := make([]map[string]any, len(validRows))
		for index, row := range validRows {
			rows[index] = map[string]any{"name": row["name"], "sql": row["sql"]}
		}
		return rows
	}
	withoutTable := func(name string) []map[string]any {
		rows := make([]map[string]any, 0, len(validRows)-1)
		for _, row := range validRows {
			if row["name"] != name {
				rows = append(rows, row)
			}
		}
		return rows
	}
	changedRows := cloneRows()
	for _, row := range changedRows {
		if row["name"] == "whitelist_topup_results" {
			row["sql"] = "CREATE TABLE whitelist_topup_results (order_id TEXT PRIMARY KEY NOT NULL)"
		}
	}
	tests := []struct {
		name string
		rows []map[string]any
	}{
		{name: "missing", rows: withoutTable("whitelist_topup_results")},
		{name: "changed", rows: changedRows},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{strong: []scriptedResult{
				rowsScript(map[string]any{"foreign_keys": int64(1)}),
				rowsScript(map[string]any{"foreign_keys": int64(1)}),
				rowsScript(map[string]any{"foreign_keys": int64(1)}),
				resultsScript(
					rqlite.Result{Rows: migrationIdentityRows(migrations)},
					rqlite.Result{Rows: expectedTableRows()},
					rqlite.Result{},
				),
				rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
				rowsScript(test.rows...),
			}}
			if _, err := NewMigrator(db).VerifyIdentity(context.Background()); err == nil {
				t.Fatal("VerifyIdentity accepted critical white-list table drift")
			}
			if len(db.requestCalls) != 0 {
				t.Fatalf("VerifyIdentity performed %d mutation(s)", len(db.requestCalls))
			}
		})
	}
}

func TestVerifyIdentityQueriesEveryCommercialTableOwnedTrigger(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	db := &recordingRQLite{strong: []scriptedResult{
		rowsScript(map[string]any{"foreign_keys": int64(1)}),
		rowsScript(map[string]any{"foreign_keys": int64(1)}),
		rowsScript(map[string]any{"foreign_keys": int64(1)}),
		resultsScript(
			rqlite.Result{Rows: migrationIdentityRows(migrations)},
			rqlite.Result{Rows: expectedTableRows()},
			rqlite.Result{},
		),
		rowsScript(expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])...),
		rowsScript(expectedWhiteListTopUpTableRows(t, migrations[13])...),
	}}
	if _, err := NewMigrator(db).VerifyIdentity(context.Background()); err != nil {
		t.Fatalf("VerifyIdentity exact schema: %v", err)
	}
	if len(db.strongCalls) < 2 || len(db.strongCalls[len(db.strongCalls)-2].statements) != 1 {
		t.Fatalf("unexpected strong query calls: %#v", db.strongCalls)
	}
	query := schemaSQLIdentity(db.strongCalls[len(db.strongCalls)-2].statements[0].SQL)
	for _, table := range []string{
		"whitelist_commercial_metering_sources",
		"whitelist_commercial_debit_outbox",
		"whitelist_gb_products",
		"whitelist_topup_orders",
		"whitelist_topup_payment_claims",
		"whitelist_publication_controls",
		"whitelist_topup_results",
		"whitelist_renewal_intents",
	} {
		if !strings.Contains(query, "'"+table+"'") {
			t.Fatalf("trigger identity query does not select table-owned triggers for %q: %s", table, query)
		}
	}
	for _, name := range []string{
		"whitelist_publication_controls_default_new",
		"whitelist_topup_orders_block_legacy_decision",
		"whitelist_topup_orders_payment_transition",
		"whitelist_topup_idempotency_applied_guard",
	} {
		if !strings.Contains(query, "'"+name+"'") {
			t.Fatalf("trigger identity query does not select cross-table guard %q: %s", name, query)
		}
	}
	if strings.Contains(query, "name like") {
		t.Fatalf("trigger identity query still relies on name prefixes: %s", query)
	}
}

func TestExpectedCriticalWhiteListTriggersIncludeTopUpGuards(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	want, err := expectedWhiteListCommercialMeteringTriggers(migrations[11], migrations[12], migrations[13])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"whitelist_topup_orders_exact_terms",
		"whitelist_publication_controls_purchase_owner",
		"whitelist_topup_results_exact_binding",
		"whitelist_topup_orders_block_legacy_decision",
		"whitelist_topup_orders_payment_transition",
		"whitelist_topup_idempotency_applied_guard",
	} {
		if _, ok := want[name]; !ok {
			t.Fatalf("critical v14 trigger %q is not identity-verified", name)
		}
	}
}

func requireExactV9MigrationChain(t *testing.T, migrations []migration) {
	requireExactV15MigrationChain(t, migrations)
}

func requireExactV15MigrationChain(t *testing.T, migrations []migration) {
	t.Helper()
	if SchemaVersion != 17 || len(migrations) != 17 {
		t.Fatalf(
			"migration chain is not exactly v1-v17: SchemaVersion=%d count=%d",
			SchemaVersion,
			len(migrations),
		)
	}
	for index, item := range migrations {
		if item.Version != index+1 {
			t.Fatalf("migration chain version[%d]=%d, want %d", index, item.Version, index+1)
		}
	}
}

func requireExactV6MigrationChain(t *testing.T, migrations []migration) {
	t.Helper()
	if len(migrations) < 6 {
		t.Fatalf("migration chain has %d entries, want at least v1-v6", len(migrations))
	}
	for index, item := range migrations[:6] {
		if item.Version != index+1 {
			t.Fatalf("v1-v6 prefix version[%d]=%d, want %d", index, item.Version, index+1)
		}
	}
	if migrations[5].Path != "migrations/0006_setting_mutation_token.sql" {
		t.Fatalf("migration 6 path=%q", migrations[5].Path)
	}
}

func requireV7MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v7 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	if !strings.Contains(sql, "task7_idempotency_claim_guard") ||
		!strings.Contains(sql, "task7_expiry_operation_fence") {
		t.Fatalf("v7 migration transaction=%s", sql)
	}
	statementIndex := func(needle string) int {
		for index, statement := range call.statements {
			if strings.Contains(strings.ToLower(statement.SQL), needle) {
				return index
			}
		}
		return -1
	}
	dropGuard := statementIndex("drop trigger idempotency_applied_resource")
	renamePayments := statementIndex("alter table payments rename to payments_v1")
	dropOldPayments := statementIndex("drop table payments_v1")
	recreateGuard := statementIndex("create trigger idempotency_applied_resource")
	if dropGuard < 0 || renamePayments < 0 || dropOldPayments < 0 || recreateGuard < 0 ||
		!(dropGuard < renamePayments && dropOldPayments < recreateGuard) {
		t.Fatalf("v7 does not transactionally preserve the durable payment replay guard: %s", sql)
	}
	ordersTable := ""
	for _, statement := range call.statements {
		candidate := strings.ToLower(statement.SQL)
		if strings.HasPrefix(strings.TrimSpace(candidate), "create table orders (") {
			ordersTable = candidate
			break
		}
	}
	if ordersTable == "" {
		t.Fatal("v7 orders table definition missing")
	}
	for _, legacyState := range []string{"'pending'", "'claimed'", "'rejected'", "'applied'"} {
		if !strings.Contains(ordersTable, legacyState) {
			t.Fatalf("v7 orders table dropped legacy state %s: %s", legacyState, ordersTable)
		}
	}
	for _, task7State := range []string{"'created'", "'payment_claimed'", "'canceled'", "'expired'", "'ready'", "'degraded'"} {
		if !strings.Contains(ordersTable, task7State) {
			t.Fatalf("v7 orders table missing Task 7 state %s: %s", task7State, ordersTable)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "7" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v7 receipt=%#v", last)
	}
}

func requireV8MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v8 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"alter table external_actions add column replaces_action_id",
		"alter table external_actions add column attempt_worker_id",
		"alter table external_actions add column attempt_lease_token",
		"alter table external_actions add column attempt_lease_fence",
		"external_actions_attempt_owner_valid_insert",
		"external_actions_attempt_owner_set_once",
		"external_actions_one_replacement",
		"external_actions_binding_immutable",
		"external_actions_replacement_valid_insert",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v8 migration transaction is missing %q: %s", required, sql)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "8" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v8 receipt=%#v", last)
	}
}

func requireV9MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v9 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"alter table rate_limit_buckets",
		"add column expires_at_unix",
		"update rate_limit_buckets",
		"create index idx_rate_limit_buckets_expiry",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v9 migration transaction is missing %q: %s", required, sql)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "9" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v9 receipt=%#v", last)
	}
}

func requireV10MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v10 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_metering_periods",
		"create table whitelist_metering_checkpoints",
		"create table whitelist_metering_events",
		"create table whitelist_metering_intervals",
		"create table whitelist_metering_projections",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v10 migration transaction is missing %q: %s", required, sql)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "10" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v10 receipt=%#v", last)
	}
}

func requireV11MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v11 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_meter_epochs",
		"create table whitelist_billing_periods",
		"create table whitelist_balance_entries",
		"create table whitelist_balance_projections",
		"create table whitelist_usage_applications",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v11 migration transaction is missing %q: %s", required, sql)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "11" || fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v11 receipt=%#v", last)
	}
}

func requireV12MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v12 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_commercial_metering_sources",
		"whitelist_commercial_metering_sources_exact_binding",
		"whitelist_commercial_metering_sources_immutable_update",
		"whitelist_commercial_metering_sources_immutable_delete",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v12 migration transaction is missing %q: %s", required, sql)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "12" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v12 receipt=%#v", last)
	}
}

func requireV13MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v13 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_commercial_debit_outbox",
		"whitelist_commercial_debit_outbox_exact_binding",
		"idx_whitelist_commercial_debit_outbox_order",
		"idx_whitelist_commercial_metering_sources_entitlement_time",
		"whitelist_commercial_debit_outbox_immutable_update",
		"whitelist_commercial_debit_outbox_immutable_delete",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v13 migration transaction is missing %q: %s", required, sql)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "13" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v13 receipt=%#v", last)
	}
}

func requireV14MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v14 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_gb_products",
		"create table whitelist_topup_orders",
		"create table whitelist_topup_payment_claims",
		"create table whitelist_publication_controls",
		"create table whitelist_topup_results",
		"create table whitelist_renewal_intents",
		"whitelist_renewal_intents_exact_binding",
		"whitelist_renewal_intents_applied_binding",
		"whitelist_topup_idempotency_applied_guard",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v14 migration transaction is missing %q", required)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "14" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v14 receipt=%#v", last)
	}
}

func requireV15MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v15 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_sidecar_exits",
		"create table whitelist_sidecar_origins",
		"create table whitelist_route_credentials",
		"create table whitelist_sidecar_desired",
		"create table whitelist_sidecar_receipts",
		"whitelist_sidecar_desired_monotonic_generation",
		"whitelist_sidecar_receipts_exact_desired",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v15 migration transaction is missing %q", required)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "15" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v15 receipt=%#v", last)
	}
}

func requireV16MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v16 request=%#v", call)
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"drop trigger external_actions_attempt_owner_set_once",
		"create trigger external_actions_attempt_owner_set_once",
		"old.status = 'applying' and new.status = 'pending'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v16 migration transaction is missing %q", required)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "16" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v16 receipt=%#v", last)
	}
}

func requireV17MigrationRequest(t *testing.T, call recordedCall, migration migration) {
	t.Helper()
	if call.level != rqlite.Linearizable || !call.transaction {
		t.Fatalf("v17 request=%#v", call)
	}
	if len(call.statements) != len(migration.Statements)+1 {
		t.Fatalf("v17 statement count=%d, want migration and one receipt", len(call.statements))
	}
	for index, statement := range migration.Statements {
		if call.statements[index].SQL != statement.SQL || len(call.statements[index].Args) != 0 {
			t.Fatalf("v17 statement %d differs from its immutable migration", index)
		}
	}
	sql := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"create table whitelist_metering_origin_observations",
		"create table whitelist_first_use_admissions",
		"create trigger whitelist_metering_origin_observations_exact_insert",
		"create trigger whitelist_metering_origin_observations_exact_update",
		"create trigger whitelist_metering_origin_observations_monotonic",
		"create trigger whitelist_first_use_admissions_exact_insert",
		"create trigger whitelist_first_use_admissions_immutable_binding",
		"create trigger whitelist_first_use_admissions_monotonic",
		"create trigger whitelist_first_use_admissions_immutable_delete",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v17 migration transaction is missing %q", required)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "17" ||
		fmt.Sprint(last.Args[1]) != migration.Checksum {
		t.Fatalf("v17 receipt=%#v", last)
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

func expectedWhiteListCommercialMeteringTriggerRows(
	t *testing.T,
	migrations ...migration,
) []map[string]any {
	t.Helper()
	want, err := expectedWhiteListCommercialMeteringTriggers(migrations...)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 0, len(whiteListCommercialMeteringTriggerNames))
	for _, name := range whiteListCommercialMeteringTriggerNames {
		rows = append(rows, map[string]any{"name": name, "sql": want[name]})
	}
	return rows
}

func expectedWhiteListTopUpTableRows(t *testing.T, item migration) []map[string]any {
	t.Helper()
	want, err := expectedWhiteListTopUpTables(item)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 0, len(whiteListTopUpTableNames))
	for _, name := range whiteListTopUpTableNames {
		rows = append(rows, map[string]any{"name": name, "sql": want[name]})
	}
	return rows
}
