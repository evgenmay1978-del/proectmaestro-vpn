package controlplane

import (
	"strings"
	"testing"
)

func TestMigrationWhiteListCommercialMeteringSourceRemainsV12AndAppendOnly(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 13 || len(migrations) != 13 {
		t.Fatalf("schema chain = version %d/%d, want exact v13 with retained v12", SchemaVersion, len(migrations))
	}
	if migrations[10].Checksum != "520db62664c161b2906058a567613aa30e99b5f6dcae5f872852a454100c1430" {
		t.Fatalf("immutable v11 checksum changed: %q", migrations[10].Checksum)
	}
	v12 := migrations[11]
	if v12.Version != 12 || v12.Path != "migrations/0012_whitelist_commercial_metering_sources.sql" {
		t.Fatalf("v12 migration=%#v", v12)
	}
	sql := strings.ToLower(string(v12.Data))
	for _, required := range []string{
		"create table whitelist_commercial_metering_sources",
		"event_id text primary key not null",
		"basis text not null check(basis = 'uplink_plus_downlink')",
		"typeof(sampled_at_unix) = 'integer'",
		"sampled_at_unix between 1 and 9223372036854775806",
		"source_sha256 text not null unique",
		"unique(meter_epoch, route_xray_identity, counter_generation, sample_sequence)",
		"route_xray_identity =",
		"'wl:' || entitlement_id || ':' || exit_id",
		"foreign key(event_id)",
		"references whitelist_metering_events(event_id) on delete restrict",
		"foreign key(meter_epoch)",
		"references whitelist_meter_epochs(meter_epoch) on delete restrict",
		"whitelist_commercial_metering_sources_exact_binding",
		"event.instance_id = new.origin_id",
		"event.xray_identity = 'wl:' || new.entitlement_id",
		"event.counter_generation = new.counter_generation",
		"event.sample_sequence = new.sample_sequence",
		"policy.basis = new.basis",
		"epoch.origin_id = new.origin_id",
		"before update on whitelist_commercial_metering_sources",
		"before delete on whitelist_commercial_metering_sources",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v12 missing %q", required)
		}
	}
	if strings.Contains(sql, "alter table") || strings.Contains(sql, "drop table") {
		t.Fatal("v12 is not additive")
	}
}
