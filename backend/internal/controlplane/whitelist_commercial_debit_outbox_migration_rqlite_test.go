//go:build rqlite_integration

package controlplane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

type commercialDebitOutboxSourceFixture struct {
	EventID         string
	MeterEpoch      string
	IntervalEndUnix int64
	SourceSHA256    string
}

func TestMigrationWhiteListCommercialDebitOutboxEnforcesExactImmutableRows(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	first := seedCommercialDebitOutboxSource(t, fixture, true, "0", "first", 1, 2, fixture.NowUnix-10)
	second := seedCommercialDebitOutboxSource(t, fixture, false, "0", "second", 1, 3, fixture.NowUnix-9)
	receiptKey := strings.Repeat("a", 64)
	requestHash := strings.Repeat("b", 64)

	task7Request(t, fixture.DB, commercialDebitOutboxStatement(
		fixture, first, receiptKey, requestHash,
	))
	if got := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_commercial_debit_outbox WHERE event_id=? AND receipt_key=? AND request_hash=?",
		first.EventID, receiptKey, requestHash); got != 1 {
		t.Fatalf("commercial debit outbox count=%d, want 1", got)
	}

	mustRequestFail(t, task7Context(t), fixture.DB, rqlite.Statement{
		SQL:  "UPDATE whitelist_commercial_debit_outbox SET interval_end_unix=interval_end_unix+1 WHERE event_id=?",
		Args: []any{first.EventID},
	})
	mustRequestFail(t, task7Context(t), fixture.DB, rqlite.Statement{
		SQL:  "DELETE FROM whitelist_commercial_debit_outbox WHERE event_id=?",
		Args: []any{first.EventID},
	})

	wrongTime := second
	wrongTime.IntervalEndUnix++
	mustRequestFail(t, task7Context(t), fixture.DB, commercialDebitOutboxStatement(
		fixture, wrongTime, strings.Repeat("c", 64), strings.Repeat("d", 64),
	))
	mustRequestFail(t, task7Context(t), fixture.DB, commercialDebitOutboxStatement(
		fixture, second, receiptKey, strings.Repeat("d", 64),
	))
	mustRequestFail(t, task7Context(t), fixture.DB, commercialDebitOutboxStatement(
		fixture, second, strings.Repeat("E", 64), strings.Repeat("d", 64),
	))
	mustRequestFail(t, task7Context(t), fixture.DB, commercialDebitOutboxStatement(
		fixture, second, strings.Repeat("c", 64), strings.Repeat("F", 64),
	))
	task7Request(t, fixture.DB, commercialDebitOutboxStatement(
		fixture, second, strings.Repeat("c", 64), strings.Repeat("d", 64),
	))

	rows := commercialDebitOutboxRows(t, fixture.DB, rqlite.Statement{SQL: `SELECT event_id
FROM whitelist_commercial_debit_outbox
WHERE entitlement_id=? ORDER BY interval_end_unix,event_id`, Args: []any{fixture.EntitlementID}})
	if len(rows) != 2 || fmt.Sprint(rows[0]["event_id"]) != first.EventID ||
		fmt.Sprint(rows[1]["event_id"]) != second.EventID {
		t.Fatalf("commercial debit outbox order=%#v", rows)
	}
	indexRows := commercialDebitOutboxRows(t, fixture.DB, rqlite.Statement{
		SQL: "PRAGMA index_info('idx_whitelist_commercial_debit_outbox_order')",
	})
	if len(indexRows) != 3 || fmt.Sprint(indexRows[0]["name"]) != "entitlement_id" ||
		fmt.Sprint(indexRows[1]["name"]) != "interval_end_unix" ||
		fmt.Sprint(indexRows[2]["name"]) != "event_id" {
		t.Fatalf("commercial debit outbox ordering index=%#v", indexRows)
	}
	sourceIndexRows := commercialDebitOutboxRows(t, fixture.DB, rqlite.Statement{
		SQL: "PRAGMA index_info('idx_whitelist_commercial_metering_sources_entitlement_time')",
	})
	if len(sourceIndexRows) != 3 || fmt.Sprint(sourceIndexRows[0]["name"]) != "entitlement_id" ||
		fmt.Sprint(sourceIndexRows[1]["name"]) != "sampled_at_unix" ||
		fmt.Sprint(sourceIndexRows[2]["name"]) != "event_id" {
		t.Fatalf("commercial source timestamp index=%#v", sourceIndexRows)
	}
	planRows := commercialDebitOutboxRows(t, fixture.DB, rqlite.Statement{
		SQL: `EXPLAIN QUERY PLAN SELECT 1
FROM whitelist_commercial_metering_sources
WHERE entitlement_id=? AND sampled_at_unix>?`,
		Args: []any{fixture.EntitlementID, fixture.NowUnix - 100},
	})
	usesTimestampIndex := false
	for _, row := range planRows {
		if strings.Contains(
			strings.ToLower(fmt.Sprint(row["detail"])),
			"idx_whitelist_commercial_metering_sources_entitlement_time",
		) {
			usesTimestampIndex = true
		}
	}
	if !usesTimestampIndex {
		t.Fatalf("commercial source timestamp query plan=%#v", planRows)
	}
}

func TestMigrationWhiteListCommercialDebitOutboxRejectsIncludedPolicy(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	source := seedCommercialDebitOutboxSource(t, fixture, true, "1", "included", 1, 2, fixture.NowUnix-10)
	mustRequestFail(t, task7Context(t), fixture.DB, commercialDebitOutboxStatement(
		fixture, source, strings.Repeat("a", 64), strings.Repeat("b", 64),
	))
	if got := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_commercial_debit_outbox WHERE event_id=?", source.EventID); got != 0 {
		t.Fatalf("included commercial policy left %d outbox row(s)", got)
	}
}

func TestMigrationWhiteListCommercialDebitOutboxRejectsNonInitialGeneration(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	source := seedCommercialDebitOutboxSource(t, fixture, true, "0", "generation", 2, 2, fixture.NowUnix-10)
	mustRequestFail(t, task7Context(t), fixture.DB, commercialDebitOutboxStatement(
		fixture, source, strings.Repeat("a", 64), strings.Repeat("b", 64),
	))
}

func seedCommercialDebitOutboxSource(
	t *testing.T,
	fixture whiteListBalanceRQLiteFixture,
	seedPolicy bool,
	includedBytes, suffix string,
	generation uint64,
	sequence uint64,
	sampledAtUnix int64,
) commercialDebitOutboxSourceFixture {
	t.Helper()
	originID := "origin_" + task7Name(t, "commercial-debit-origin")
	meterEpoch := "epoch_" + task7Name(t, "commercial-debit-epoch")
	baseIdentity := "wl:" + fixture.EntitlementID
	exitID := "nl"
	routeIdentity := baseIdentity + ":" + exitID
	counterSourceID := originID + "-xray"
	processBootID := "boot-" + task7Name(t, "commercial-debit-boot")
	eventID := "interval_" + task7Name(t, "commercial-debit-"+suffix)
	uplinkBytes := uint64(20 + sequence)
	downlinkBytes := uint64(30 + sequence)
	digest, err := whitelistmetering.SourceSHA256(whitelistmetering.SourceDigestInput{
		AccountID: fixture.CustomerID, EntitlementID: fixture.EntitlementID,
		TransportID: "yandex-cdn", BillingPeriodID: fixture.PeriodID,
		Basis: "UPLINK_PLUS_DOWNLINK", BaseXrayIdentity: baseIdentity,
		RouteXrayIdentity: routeIdentity, OriginID: originID, ExitID: exitID,
		CounterSourceID: counterSourceID, XrayProcessBootID: processBootID,
		MeterEpoch: meterEpoch, CounterGeneration: generation, SampleSequence: sequence,
		UplinkBytes: uplinkBytes, DownlinkBytes: downlinkBytes, SampledAtUnix: sampledAtUnix,
	})
	if err != nil {
		t.Fatalf("commercial debit source digest: %v", err)
	}
	statements := make([]rqlite.Statement, 0, 5)
	if seedPolicy {
		statements = append(statements,
			rqlite.Statement{
				SQL: `INSERT INTO whitelist_metering_periods(
entitlement_id,billing_period_id,account_id,transport_id,xray_identity,unit,basis,
included_bytes,soft_limit_bytes,hard_limit_bytes,grace_bytes,price_mode,price_source,
currency,minor_units_per_unit,policy_sha256,created_at_unix)
VALUES(?,?,?,?,?,'GB_DECIMAL','UPLINK_PLUS_DOWNLINK',?,'0','0','0',
'PAID','GLOBAL','RUB','100',?,?)`,
				Args: []any{
					fixture.EntitlementID, fixture.PeriodID, fixture.CustomerID, "yandex-cdn",
					baseIdentity, includedBytes, whiteListDigest("policy:" + eventID), fixture.NowUnix - 100,
				},
			},
			rqlite.Statement{
				SQL: `INSERT INTO whitelist_meter_epochs(
meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix)
VALUES(?,?,?,?,0,?)`,
				Args: []any{meterEpoch, originID, counterSourceID, processBootID, fixture.NowUnix - 100},
			},
		)
	}
	statements = append(statements,
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_events(
event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
diagnostic,has_interval,result_json,created_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,?,?,'',1,'{}',?)`,
			Args: []any{
				eventID, fixture.EntitlementID, fixture.PeriodID, originID, meterEpoch,
				baseIdentity, fmt.Sprint(generation), fmt.Sprint(sequence), fmt.Sprint(uplinkBytes), fmt.Sprint(downlinkBytes),
				whiteListDigest("payload:" + eventID), sampledAtUnix,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_intervals(
event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
amount_numerator,amount_denominator,currency,created_at_unix)
VALUES(?,?,?,?,?,'1000000000','RUB',?)`,
			Args: []any{
				eventID, fmt.Sprint(uplinkBytes), fmt.Sprint(downlinkBytes),
				fmt.Sprint(uplinkBytes + downlinkBytes), fmt.Sprint((uplinkBytes + downlinkBytes) * 100),
				sampledAtUnix,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_commercial_metering_sources(
event_id,entitlement_id,billing_period_id,origin_id,exit_id,meter_epoch,
route_xray_identity,counter_generation,sample_sequence,basis,sampled_at_unix,source_sha256)
VALUES(?,?,?,?,?,?,?,?,?,'UPLINK_PLUS_DOWNLINK',?,?)`,
			Args: []any{
				eventID, fixture.EntitlementID, fixture.PeriodID, originID, exitID,
				meterEpoch, routeIdentity, fmt.Sprint(generation), fmt.Sprint(sequence), sampledAtUnix, digest,
			},
		},
	)
	task7Request(t, fixture.DB, statements...)
	return commercialDebitOutboxSourceFixture{
		EventID: eventID, MeterEpoch: meterEpoch,
		IntervalEndUnix: sampledAtUnix, SourceSHA256: digest,
	}
}

func commercialDebitOutboxStatement(
	fixture whiteListBalanceRQLiteFixture,
	source commercialDebitOutboxSourceFixture,
	receiptKey, requestHash string,
) rqlite.Statement {
	return rqlite.Statement{
		SQL: `INSERT INTO whitelist_commercial_debit_outbox(
event_id,entitlement_id,billing_period_id,meter_epoch,basis,interval_end_unix,
source_sha256,receipt_key,request_hash,created_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
		Args: []any{
			source.EventID, fixture.EntitlementID, fixture.PeriodID, source.MeterEpoch,
			"UPLINK_PLUS_DOWNLINK", source.IntervalEndUnix, source.SourceSHA256,
			receiptKey, requestHash, fixture.NowUnix,
		},
	}
}

func commercialDebitOutboxRows(
	t *testing.T,
	db rqlite.RQLite,
	statement rqlite.Statement,
) []map[string]any {
	t.Helper()
	results, err := db.QueryLinearizable(task7Context(t), statement)
	if err != nil {
		t.Fatalf("commercial debit outbox query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("commercial debit outbox query returned %d results, want 1", len(results))
	}
	return results[0].Rows
}
