package controlplane

import (
	"strings"
	"testing"
)

func TestMigrationWhiteListCommercialDebitOutboxIsV13AndAppendOnly(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 13 || len(migrations) != 13 {
		t.Fatalf("schema chain = version %d/%d, want exact v13", SchemaVersion, len(migrations))
	}
	if migrations[11].Version != 12 || migrations[11].Path != "migrations/0012_whitelist_commercial_metering_sources.sql" {
		t.Fatalf("immutable v12 migration moved: %#v", migrations[11])
	}
	latest := migrations[12]
	if latest.Version != 13 || latest.Path != "migrations/0013_whitelist_commercial_debit_outbox.sql" {
		t.Fatalf("latest migration=%#v", latest)
	}
	sql := strings.ToLower(string(latest.Data))
	for _, required := range []string{
		"create table whitelist_commercial_debit_outbox",
		"event_id text primary key not null",
		"basis text not null check(basis = 'uplink_plus_downlink')",
		"typeof(interval_end_unix) = 'integer'",
		"source_sha256 text not null",
		"receipt_key text not null unique",
		"request_hash text not null",
		"foreign key(event_id)",
		"references whitelist_commercial_metering_sources(event_id)",
		"on delete restrict",
		"whitelist_commercial_debit_outbox_exact_binding",
		"join whitelist_metering_intervals as interval",
		"join whitelist_metering_periods as policy",
		"source.entitlement_id = new.entitlement_id",
		"source.billing_period_id = new.billing_period_id",
		"source.meter_epoch = new.meter_epoch",
		"source.basis = new.basis",
		"source.sampled_at_unix = new.interval_end_unix",
		"source.source_sha256 = new.source_sha256",
		"source.counter_generation = '1'",
		"policy.basis = 'uplink_plus_downlink'",
		"policy.included_bytes = '0'",
		"create index idx_whitelist_commercial_debit_outbox_order",
		"on whitelist_commercial_debit_outbox(entitlement_id, interval_end_unix, event_id)",
		"create index idx_whitelist_commercial_metering_sources_entitlement_time",
		"on whitelist_commercial_metering_sources(entitlement_id, sampled_at_unix, event_id)",
		"before update on whitelist_commercial_debit_outbox",
		"before delete on whitelist_commercial_debit_outbox",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v13 missing %q", required)
		}
	}
	if strings.Contains(sql, "alter table") || strings.Contains(sql, "drop table") {
		t.Fatal("v13 is not additive")
	}
}
