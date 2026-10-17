//go:build rqlite_integration

package controlplane

import (
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func TestMigrationWhiteListCommercialMeteringSourceEnforcesExactBinding(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	const (
		originID        = "origin-s2"
		exitID          = "exit-nl"
		counterSourceID = "xray-api-origin-s2-exit-nl"
		processBootID   = "boot-a"
		meterEpoch      = "origin-s2-exit-nl-boot-a-reset-0"
	)
	baseIdentity := "wl:" + fixture.EntitlementID
	routeIdentity := baseIdentity + ":" + exitID

	sourceDigest := func(eventID, origin, route string, sampledAt int64, sequence uint64) string {
		t.Helper()
		digest, err := whitelistmetering.SourceSHA256(whitelistmetering.SourceDigestInput{
			AccountID: fixture.CustomerID, EntitlementID: fixture.EntitlementID,
			TransportID: "yandex-cdn", BillingPeriodID: fixture.PeriodID,
			Basis: "UPLINK_PLUS_DOWNLINK", BaseXrayIdentity: baseIdentity,
			RouteXrayIdentity: route, OriginID: origin, ExitID: exitID,
			CounterSourceID: counterSourceID, XrayProcessBootID: processBootID,
			ResetSequence: 0, MeterEpoch: meterEpoch, CounterGeneration: 1,
			SampleSequence: sequence, UplinkBytes: 30, DownlinkBytes: 70,
			SampledAtUnix: sampledAt,
		})
		if err != nil {
			t.Fatalf("source digest for %s: %v", eventID, err)
		}
		return digest
	}
	eventStatement := func(eventID, origin string, sequence uint64) rqlite.Statement {
		return rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_events(
event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
diagnostic,has_interval,result_json,created_at_unix)
VALUES(?,?,?,?,?,?,'1',?,'30','70',?,'',1,'{}',?)`,
			Args: []any{
				eventID, fixture.EntitlementID, fixture.PeriodID, origin,
				meterEpoch, baseIdentity, sequence, whiteListDigest(eventID), fixture.NowUnix,
			},
		}
	}
	intervalStatement := func(eventID string) rqlite.Statement {
		return rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_intervals(
event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
amount_numerator,amount_denominator,currency,created_at_unix)
VALUES(?,'30','70','0','0','1','RUB',?)`,
			Args: []any{eventID, fixture.NowUnix},
		}
	}
	sourceStatement := func(eventID, origin, route, digest string, sampledAt int64, sequence uint64) rqlite.Statement {
		return rqlite.Statement{
			SQL: `INSERT INTO whitelist_commercial_metering_sources(
event_id,entitlement_id,billing_period_id,origin_id,exit_id,meter_epoch,
route_xray_identity,counter_generation,sample_sequence,basis,sampled_at_unix,source_sha256)
VALUES(?,?,?,?,?,?,?,'1',?,'UPLINK_PLUS_DOWNLINK',?,?)`,
			Args: []any{
				eventID, fixture.EntitlementID, fixture.PeriodID, origin,
				exitID, meterEpoch, route, sequence, sampledAt, digest,
			},
		}
	}

	const eventID = "commercial-source-event-1"
	digest := sourceDigest(eventID, originID, routeIdentity, fixture.NowUnix, 2)
	task7Request(t, fixture.DB,
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_periods(
entitlement_id,billing_period_id,account_id,transport_id,xray_identity,unit,basis,
included_bytes,soft_limit_bytes,hard_limit_bytes,grace_bytes,price_mode,price_source,
currency,minor_units_per_unit,policy_sha256,created_at_unix)
VALUES(?,?,?,?,?,'GB_DECIMAL','UPLINK_PLUS_DOWNLINK','0','0','0','0',
'PAID','GLOBAL','RUB','100',?,?)`,
			Args: []any{
				fixture.EntitlementID, fixture.PeriodID, fixture.CustomerID,
				"yandex-cdn", baseIdentity, strings.Repeat("1", 64), fixture.NowUnix,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_meter_epochs(
meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix)
VALUES(?,?,?,?,0,?)`,
			Args: []any{meterEpoch, originID, counterSourceID, processBootID, fixture.NowUnix},
		},
		eventStatement(eventID, originID, 2),
		intervalStatement(eventID),
		sourceStatement(eventID, originID, routeIdentity, digest, fixture.NowUnix, 2),
	)
	if got := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_commercial_metering_sources WHERE event_id=? AND source_sha256=?",
		eventID, digest); got != 1 {
		t.Fatalf("commercial source count=%d, want 1", got)
	}

	mustRequestFail(t, task7Context(t), fixture.DB, rqlite.Statement{
		SQL:  "UPDATE whitelist_commercial_metering_sources SET sampled_at_unix=sampled_at_unix+1 WHERE event_id=?",
		Args: []any{eventID},
	})
	mustRequestFail(t, task7Context(t), fixture.DB, rqlite.Statement{
		SQL:  "DELETE FROM whitelist_commercial_metering_sources WHERE event_id=?",
		Args: []any{eventID},
	})

	const wrongOriginEvent = "commercial-source-wrong-origin"
	wrongOriginDigest := sourceDigest(wrongOriginEvent, "origin-s3", routeIdentity, fixture.NowUnix+1, 3)
	if _, err := fixture.DB.Request(task7Context(t), rqlite.Linearizable, true,
		eventStatement(wrongOriginEvent, originID, 3),
		intervalStatement(wrongOriginEvent),
		sourceStatement(wrongOriginEvent, "origin-s3", routeIdentity, wrongOriginDigest, fixture.NowUnix+1, 3),
	); err == nil {
		t.Fatal("commercial source accepted origin/epoch mismatch")
	}
	if got := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_metering_events WHERE event_id=?", wrongOriginEvent); got != 0 {
		t.Fatalf("failed source transaction left %d event row(s)", got)
	}

	const duplicatePhysicalEvent = "commercial-source-duplicate-physical"
	differentClaimedDigest := strings.Repeat("f", 64)
	if differentClaimedDigest == digest {
		t.Fatal("test digest collision")
	}
	if _, err := fixture.DB.Request(task7Context(t), rqlite.Linearizable, true,
		eventStatement(duplicatePhysicalEvent, originID, 2),
		intervalStatement(duplicatePhysicalEvent),
		sourceStatement(duplicatePhysicalEvent, originID, routeIdentity, differentClaimedDigest, fixture.NowUnix, 2),
	); err == nil {
		t.Fatal("same physical sample was accepted under a second event ID")
	}
	if got := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_metering_events WHERE event_id=?", duplicatePhysicalEvent); got != 0 {
		t.Fatalf("duplicate physical source left %d event row(s)", got)
	}

	results, err := fixture.DB.QueryLinearizable(task7Context(t), rqlite.Statement{SQL: "PRAGMA foreign_key_check"})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 0 {
		t.Fatalf("foreign_key_check results=%#v err=%v", results, err)
	}
}
