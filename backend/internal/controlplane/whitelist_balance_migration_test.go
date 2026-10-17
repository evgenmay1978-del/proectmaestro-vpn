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
	if SchemaVersion < 11 || len(migrations) < 11 {
		t.Fatalf("schema chain = version %d/%d, want immutable v11 prefix", SchemaVersion, len(migrations))
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
		{"migrations/0001_control_plane.sql", "48d4ea2c209dd53f51fe0b507a0ece15b52098aa4072ad994fed69ec60403056"},
		{"migrations/0002_restore_epoch.sql", "8d485e12b9b4c749f0e930746dfeb0b67f05368bdc73d075f70b6cf0006eded7"},
		{"migrations/0003_outbox_fencing.sql", "f27c1682f6f437f99710e5380970c577c90e3374deea347bf2383358591da9bd"},
		{"migrations/0004_whitelist_entitlement_identity.sql", "707ede55a373432b4dc72dc81e41bb41052c754552ff848577e828522e6de82f"},
		{"migrations/0005_backup_rpo.sql", "17adc3309bb73d79d7702a16585cd619cdd602643c714e5a3192addb2ff0205c"},
		{"migrations/0006_setting_mutation_token.sql", "c76ae24756f5a165727e78c6ab2c6e52d662c8f9833d6429622302201b0a790b"},
		{"migrations/0007_orders_exactly_once.sql", "4c985d9c54dd4ede88b4fd2020e314a7164456bea41c83f6ad9b3db455c2388e"},
		{"migrations/0008_external_action_binding.sql", "703bcabf3b8c8b5e4ac712e9f7cbc4b29b1a799cc114b67aa0926bd3f7de1f81"},
		{"migrations/0009_rate_limit_retention.sql", "01a6568d9558293bc1000cac6947b378bdc6c40f73b43ad56d6d251a0e4e202d"},
		{"migrations/0010_whitelist_metering.sql", "b958ddee59e5ac0fe854627ee46de137a8f4f86f81933a08c8d3a59ed875df64"},
	}
	for index, expected := range want {
		if migrations[index].Path != expected.path || migrations[index].Checksum != expected.checksum {
			t.Fatalf("migration %d path=%q checksum=%q, want path=%q checksum=%q", index+1, migrations[index].Path, migrations[index].Checksum, expected.path, expected.checksum)
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
