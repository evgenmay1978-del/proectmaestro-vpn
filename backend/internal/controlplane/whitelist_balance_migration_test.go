package controlplane

import (
	"strings"
	"testing"
)

func TestMigrationWhiteListCommercialBalanceIsV11AndAppendOnly(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 11 || len(migrations) != 11 {
		t.Fatalf("schema chain = version %d/%d, want v11 with 11 entries", SchemaVersion, len(migrations))
	}
	if migrations[10].Version != 11 || migrations[10].Path != "migrations/0011_whitelist_commercial_balance.sql" {
		t.Fatalf("latest migration = %#v", migrations[10])
	}
	for _, required := range []string{
		"create table whitelist_meter_epochs",
		"create table whitelist_billing_periods",
		"create table whitelist_balance_entries",
		"create table whitelist_balance_projections",
		"create table whitelist_usage_applications",
		"before update on whitelist_billing_periods",
		"before delete on whitelist_billing_periods",
		"before update on whitelist_meter_epochs",
		"before delete on whitelist_meter_epochs",
		"before update on whitelist_balance_entries",
		"before delete on whitelist_balance_entries",
		"before update on whitelist_usage_applications",
		"before delete on whitelist_usage_applications",
		"unique(access_order_id, period_ordinal)",
		"unique(entitlement_id, period_id)",
		"unique(origin_id, counter_source_id, xray_process_boot_id, reset_sequence)",
		"create unique index whitelist_balance_entries_purchased_source_order_once",
		"where kind = 'purchased_credit'",
		"unique(meter_epoch, interval_id)",
		"unique(interval_id)",
		"foreign key(entitlement_id, current_period_id)",
		"foreign key(entry_id, entitlement_id, period_id, interval_id)",
		"foreign key(meter_epoch) references whitelist_meter_epochs(meter_epoch)",
		"source_order_id is not null and interval_id is null",
		"source_order_id is null and interval_id is not null",
		"whitelist_balance_entries_interval_matches_period",
		"whitelist_balance_entries_included_matches_period_order",
		"whitelist_usage_applications_meter_epoch_matches_interval",
		"create trigger whitelist_balance_projections_initial_version",
		"before delete on whitelist_balance_projections",
		"create unique index whitelist_balance_entries_included_source_period_once",
		"source_order.payment_state = 'confirmed'",
		"source_order.decision = 'confirmed'",
		"new.version <> old.version + 1",
	} {
		if !strings.Contains(strings.ToLower(string(migrations[10].Data)), required) {
			t.Fatalf("v11 missing %q", required)
		}
	}
}

func TestMigrationWhiteListCommercialBalanceChecksSQLiteIntegerStorage(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(migrations[10].Data))
	wantChecks := map[string]int{
		"typeof(reset_sequence) = 'integer'":            1,
		"typeof(created_at_unix) = 'integer'":           3,
		"typeof(period_ordinal) = 'integer'":            1,
		"typeof(starts_at_unix) = 'integer'":            1,
		"typeof(ends_at_unix) = 'integer'":              1,
		"typeof(included_grant_bytes) = 'integer'":      1,
		"typeof(included_delta_bytes) = 'integer'":      1,
		"typeof(purchased_delta_bytes) = 'integer'":     1,
		"typeof(consumed_delta_bytes) = 'integer'":      1,
		"typeof(uncovered_delta_bytes) = 'integer'":     1,
		"typeof(included_remaining_bytes) = 'integer'":  1,
		"typeof(purchased_remaining_bytes) = 'integer'": 1,
		"typeof(lifetime_consumed_bytes) = 'integer'":   1,
		"typeof(uncovered_bytes) = 'integer'":           1,
		"typeof(version) = 'integer'":                   1,
		"typeof(pending) = 'integer'":                   1,
		"typeof(fresh_through_unix) = 'integer'":        1,
		"typeof(updated_at_unix) = 'integer'":           1,
		"typeof(applied_at_unix) = 'integer'":           1,
	}
	for check, want := range wantChecks {
		if got := strings.Count(sql, check); got != want {
			t.Errorf("v11 storage check %q count = %d, want %d", check, got, want)
		}
	}
}

func TestWhiteListCommercialBalancePreservesExactV1ThroughV10Checksums(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ path, checksum string }{
		{"migrations/0001_control_plane.sql", "c5ed7cb0d13cf0f00def416f0a88cfe60a7d8388b0e76a6e6b5ce62c93ebb0b9"},
		{"migrations/0002_restore_epoch.sql", "ae8d8fdf9d43d04bf26895bfcf0b8c3a93d627a9be68aca8abf293264bcb3cf7"},
		{"migrations/0003_outbox_fencing.sql", "e7c7c70e6d14007ae2e1f5b7d4b1a25264eaabec3eefadbb9542c644bd98566f"},
		{"migrations/0004_whitelist_entitlement_identity.sql", "cda7555052ad6e908517704b672023ad2e645ac6d7077700769a93994824d6f1"},
		{"migrations/0005_backup_rpo.sql", "898cde623153b071848e34e5828becdbb27d75b4fb55220a22d33883ecbc5296"},
		{"migrations/0006_setting_mutation_token.sql", "d8514ee9bb83daa9a761e503681354b17897324b6cd98e340f770a75e1b5851e"},
		{"migrations/0007_orders_exactly_once.sql", "d2bdc09a09889a2a0a41928bb7e33423d4bf64ab712f072f37aa213df7460d66"},
		{"migrations/0008_external_action_binding.sql", "c80a0c7cf6d2441412d7a62cd389d59b2ba4ca80983975be0ecbdc08fdd09890"},
		{"migrations/0009_rate_limit_retention.sql", "8b3ba568d0addee453f4da4fd97bfc42a543a2343e173f0d6ba6c4843e06bc1a"},
		{"migrations/0010_whitelist_metering.sql", "d06dfbc96012079e8747f43a877cfdc65f87b6f1ebd6a69b4bdd02ad72b20210"},
	}
	for index, expected := range want {
		if migrations[index].Path != expected.path || migrations[index].Checksum != expected.checksum {
			t.Fatalf("migration %d = %#v, want path=%q checksum=%q", index+1, migrations[index], expected.path, expected.checksum)
		}
	}
}

func TestMigrationWhiteListCommercialBalanceUsesMaxExclusiveCanonicalCounters(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(migrations[10].Data))
	for _, column := range []string{"included_delta_bytes", "purchased_delta_bytes", "consumed_delta_bytes", "uncovered_delta_bytes"} {
		if !strings.Contains(sql, column+" integer") {
			t.Fatalf("v11 counter %q is not sqlite INTEGER", column)
		}
	}
	if !strings.Contains(sql, "9223372036854775806") {
		t.Fatal("v11 does not cap counters strictly below int64 max")
	}
	if strings.Contains(sql, "between 0 and 9223372036854775807") ||
		strings.Contains(sql, "between -9223372036854775807 and 9223372036854775807") {
		t.Fatal("v11 accepts an int64 max byte value")
	}
	if strings.Count(sql, "on delete restrict") < 8 {
		t.Fatalf("v11 has too few restrictive foreign keys: %d", strings.Count(sql, "on delete restrict"))
	}
}
