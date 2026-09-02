package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func TestCommercialDebitAdapterLoadsExactImmutableSourceAndUsesServiceClock(t *testing.T) {
	const (
		entitlementID = "entitlement-commercial-1"
		periodID      = "period-commercial-1"
		meterEpoch    = "meter-epoch-commercial-1"
		intervalID    = "interval-commercial-1"
		sourceSHA256  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	period := whitelistbalance.Period{
		ID: periodID, Ordinal: 0, StartsAtUnix: 1_999_000, EndsAtUnix: 2_001_000,
		AccessOrderID: "ordinary-order-commercial-1",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: entitlementID, CurrentPeriodID: periodID,
		PurchasedRemainingBytes: 100, Version: 2, FreshThroughUnix: 1_999_900,
	}
	expected := whitelistbalance.OperationResult{
		PeriodID: periodID,
		Allocation: whitelistbalance.UsageAllocation{
			PurchasedBytes: 60,
		},
		Projection: whitelistbalance.BalanceProjection{
			EntitlementID: entitlementID, CurrentPeriodID: periodID,
			PurchasedRemainingBytes: 40, LifetimeConsumedBytes: 60,
			Version: 3, FreshThroughUnix: 1_999_999,
		},
	}
	debit := whitelistmetering.CommercialDebit{
		EntitlementID: entitlementID, BillingPeriodID: periodID,
		MeterEpoch: meterEpoch, IntervalID: intervalID, Basis: "UPLINK_PLUS_DOWNLINK",
		IntervalEndUnix: 1_999_999, SourceSHA256: sourceSHA256,
	}
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(debit.MeterEpoch, debit.IntervalID)
	if err != nil {
		t.Fatal(err)
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(debit)
	if err != nil {
		t.Fatal(err)
	}
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(),
		rowsScript(whiteListBalanceStateRow(entitlementID, "active", 3_000_000, period, projection, 0)),
		rowsScript(map[string]any{
			"entitlement_id": entitlementID, "period_id": periodID,
			"meter_epoch": meterEpoch, "interval_id": intervalID,
			"billable_bytes": "60", "basis": "UPLINK_PLUS_DOWNLINK",
			"interval_end_unix": int64(1_999_999), "source_sha256": sourceSHA256,
			"receipt_key": receiptKey, "request_hash": requestHash,
		}),
	}}
	db.requestFn = successfulWhiteListBalanceRequest(t, expected)
	service, _ := testService(t, db)

	err = service.DebitCommercialInterval(context.Background(), debit)
	if err != nil {
		t.Fatalf("DebitCommercialInterval: %v", err)
	}
	if len(db.linearCalls) != 3 {
		t.Fatalf("linearizable reads=%d, want 3", len(db.linearCalls))
	}
	loaderSQL := strings.ToLower(db.linearCalls[2].statements[0].SQL)
	for _, required := range []string{
		"join whitelist_commercial_metering_sources",
		"source.basis",
		"source.sampled_at_unix",
		"source.source_sha256",
	} {
		if !strings.Contains(loaderSQL, required) {
			t.Fatalf("commercial loader lacks %q: %s", required, loaderSQL)
		}
	}
	statements := assertWhiteListBalanceWrite(t, db,
		"whitelist_balance_entries", "whitelist_usage_applications", "whitelist_balance_projections",
	)
	if !whiteListBalanceStatementsHaveArgs(statements, meterEpoch, intervalID) {
		t.Fatalf("commercial debit did not use exact source and service clock: %#v", statements)
	}
	clockBound := false
	for _, statement := range statements {
		for _, arg := range statement.Args {
			if value, ok := arg.(int64); ok && value == 2_000_000 {
				clockBound = true
			}
		}
	}
	if !clockBound {
		t.Fatalf("commercial debit did not use service clock: %#v", statements)
	}
}

func TestCommercialDebitAdapterRejectsUnknownBasisOrSourceDigestBeforeDatabaseRead(t *testing.T) {
	service, _ := testService(t, &recordingRQLite{})
	valid := whitelistmetering.CommercialDebit{
		EntitlementID: "entitlement-commercial-1", BillingPeriodID: "period-commercial-1",
		MeterEpoch: "meter-epoch-commercial-1", IntervalID: "interval-commercial-1",
		Basis: "UPLINK_PLUS_DOWNLINK", IntervalEndUnix: 1_999_999,
		SourceSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for _, mutate := range []func(*whitelistmetering.CommercialDebit){
		func(request *whitelistmetering.CommercialDebit) { request.Basis = "DOWNLINK_ONLY" },
		func(request *whitelistmetering.CommercialDebit) { request.SourceSHA256 = "not-a-digest" },
	} {
		request := valid
		mutate(&request)
		if err := service.DebitCommercialInterval(context.Background(), request); err != ErrConflict {
			t.Fatalf("invalid commercial request error=%v, want ErrConflict", err)
		}
	}
}
