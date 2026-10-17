package shadowbilling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func TestCommercialBoundaryZeroByteReceiptAdvancesPeriodBeforeP2Source(t *testing.T) {
	ctx := context.Background()
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}

	const accountID = "commercial-boundary-receipt"
	shadowStore, period1Policy, source := newCommercialMeteringFixture(t, db, accountID)
	clock := &commercialControlPlaneClock{now: time.Unix(2_000_010, 0)}
	secretBox, err := controlplane.NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{1}, 32)},
		bytes.Repeat([]byte{2}, 32),
	)
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	controlDB := &commercialRequestObserver{RQLite: db}
	controlStore, err := controlplane.NewStore(controlDB, secretBox, clock)
	if err != nil {
		t.Fatalf("new control-plane store: %v", err)
	}
	service, err := controlplane.NewService(controlStore, &commercialControlPlaneIDs{}, clock)
	if err != nil {
		t.Fatalf("new control-plane service: %v", err)
	}

	db.must(t, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix)
VALUES(?,?,'0','0','0','0',1,0,0,?)`,
		Args: []any{period1Policy.EntitlementID(), period1Policy.BillingPeriodID(), clock.Now().Unix()},
	})
	accountDigest := sha256.Sum256([]byte(accountID))
	period2 := whitelistbalance.Period{
		ID: "period-2", Ordinal: 1, StartsAtUnix: 2_100_000, EndsAtUnix: 2_200_000,
		IncludedGrantBytes: 0, AccessOrderID: "order-" + hex.EncodeToString(accountDigest[:])[:16],
	}
	if _, err := service.ScheduleWhiteListPeriod(ctx, clock.Now().Unix(), controlplane.ScheduleWhiteListPeriodCommand{
		EntitlementID: period1Policy.EntitlementID(),
		Period:        period2,
	}); err != nil {
		t.Fatalf("pre-schedule period 2: %v; request error: %v", err, controlDB.lastErr)
	}

	baseline := commercialMeteringEvent(
		period1Policy, source, "boundary-period-1-baseline", 1, 1, 100, 200, 2_000_000,
	)
	if _, err := shadowStore.ApplyCommercialOrdered(ctx, baseline, period1Policy, service); err != nil {
		t.Fatalf("period 1 baseline: %v", err)
	}

	period2Policy := period1Policy
	period2Policy.billingPeriodID = period2.ID
	prematurePeriod2 := commercialMeteringEvent(
		period2Policy, source, "boundary-period-2-source", 1, 3, 100, 200, 2_100_001,
	)
	if _, err := shadowStore.ApplyCommercialOrdered(ctx, prematurePeriod2, period2Policy, service); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("premature period 2 source error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", period1Policy.EntitlementID()); got != 1 {
		t.Fatalf("metering events after premature period 2 source=%d, want 1", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", period1Policy.EntitlementID()); got != 1 {
		t.Fatalf("commercial sources after premature period 2 source=%d, want 1", got)
	}

	clock.now = time.Unix(period2.StartsAtUnix+1, 0)
	boundary := commercialMeteringEvent(
		period1Policy, source, "boundary-period-1-close", 1, 2, 100, 200, period2.StartsAtUnix,
	)
	boundaryResult, err := shadowStore.ApplyCommercialOrdered(ctx, boundary, period1Policy, service)
	if err != nil {
		t.Fatalf("zero-byte period 1 boundary interval: %v; request error: %v", err, controlDB.lastErr)
	}
	if boundaryResult.Decision.Interval == nil || boundaryResult.Decision.Interval.BillableBytes != 0 {
		t.Fatalf("period 1 boundary interval=%#v, want persisted zero-byte interval", boundaryResult.Decision.Interval)
	}

	boundaryBinding, err := BindCommercialMeteringSource(boundary, period1Policy)
	if err != nil {
		t.Fatalf("bind period 1 boundary source: %v", err)
	}
	boundaryDebit := commercialDebitFromBinding(boundaryBinding)
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(
		boundaryDebit.MeterEpoch, boundaryDebit.IntervalID,
	)
	if err != nil {
		t.Fatalf("period 1 boundary receipt key: %v", err)
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(boundaryDebit)
	if err != nil {
		t.Fatalf("period 1 boundary request hash: %v", err)
	}
	results, err := db.QueryLinearizable(ctx,
		rqlite.Statement{
			SQL:  `SELECT current_period_id FROM whitelist_balance_projections WHERE entitlement_id=?`,
			Args: []any{period1Policy.EntitlementID()},
		},
		rqlite.Statement{
			SQL: `SELECT request_hash,resource_id,status FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`,
			Args: []any{
				whitelistmetering.CommercialDebitReceiptScope,
				whitelistmetering.CommercialDebitReceiptCommand,
				receiptKey,
			},
		},
	)
	if err != nil || len(results) != 2 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 {
		t.Fatalf("period boundary durable state=%#v err=%v", results, err)
	}
	if got := results[0].Rows[0]["current_period_id"]; got != period2.ID {
		t.Fatalf("persisted current period after boundary receipt=%#v, want %q", got, period2.ID)
	}
	receipt := results[1].Rows[0]
	if receipt["request_hash"] != requestHash ||
		receipt["resource_id"] != period1Policy.EntitlementID() || receipt["status"] != "applied" {
		t.Fatalf("period 1 boundary receipt=%#v", receipt)
	}

	clock.now = time.Unix(period2.StartsAtUnix+2, 0)
	period2Result, err := shadowStore.ApplyCommercialOrdered(ctx, prematurePeriod2, period2Policy, service)
	if err != nil {
		t.Fatalf("same period 2 source after period 1 closure: %v; request error: %v", err, controlDB.lastErr)
	}
	if period2Result.Decision.Interval != nil {
		t.Fatalf("period 2 first source interval=%#v, want accepted baseline", period2Result.Decision.Interval)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", period1Policy.EntitlementID()); got != 3 {
		t.Fatalf("metering events after accepted period 2 source=%d, want 3", got)
	}
}

func TestCommercialBoundaryRestartDrainsCommittedZeroByteReceiptBeforeP2Source(t *testing.T) {
	ctx := context.Background()
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}

	const accountID = "commercial-boundary-restart"
	shadowStore, period1Policy, source := newCommercialMeteringFixture(t, db, accountID)
	clock := &commercialControlPlaneClock{now: time.Unix(2_000_010, 0)}
	secretBox, err := controlplane.NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{3}, 32)},
		bytes.Repeat([]byte{4}, 32),
	)
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	controlDB := &commercialRequestObserver{RQLite: db}
	controlStore, err := controlplane.NewStore(controlDB, secretBox, clock)
	if err != nil {
		t.Fatalf("new control-plane store: %v", err)
	}
	service, err := controlplane.NewService(controlStore, &commercialControlPlaneIDs{}, clock)
	if err != nil {
		t.Fatalf("new control-plane service: %v", err)
	}

	db.must(t, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix)
VALUES(?,?,'0','0','0','0',1,0,0,?)`,
		Args: []any{period1Policy.EntitlementID(), period1Policy.BillingPeriodID(), clock.Now().Unix()},
	})
	accountDigest := sha256.Sum256([]byte(accountID))
	period2 := whitelistbalance.Period{
		ID: "period-2-restart", Ordinal: 1, StartsAtUnix: 2_100_000, EndsAtUnix: 2_200_000,
		IncludedGrantBytes: 0, AccessOrderID: "order-" + hex.EncodeToString(accountDigest[:])[:16],
	}
	if _, err := service.ScheduleWhiteListPeriod(ctx, clock.Now().Unix(), controlplane.ScheduleWhiteListPeriodCommand{
		EntitlementID: period1Policy.EntitlementID(),
		Period:        period2,
	}); err != nil {
		t.Fatalf("pre-schedule period 2: %v; request error: %v", err, controlDB.lastErr)
	}

	baseline := commercialMeteringEvent(
		period1Policy, source, "boundary-restart-period-1-baseline", 1, 1, 100, 200, 2_000_000,
	)
	if _, err := shadowStore.ApplyCommercialOrdered(ctx, baseline, period1Policy, service); err != nil {
		t.Fatalf("period 1 baseline: %v", err)
	}

	clock.now = time.Unix(period2.StartsAtUnix+1, 0)
	boundary := commercialMeteringEvent(
		period1Policy, source, "boundary-restart-period-1-close", 1, 2, 100, 200, period2.StartsAtUnix,
	)
	callbackErr := errors.New("boundary zero-byte debit unavailable")
	failOnce := &commercialFailOnceDebiter{delegate: service, err: callbackErr}
	_, err = shadowStore.ApplyCommercialOrdered(ctx, boundary, period1Policy, failOnce)
	if !errors.Is(err, callbackErr) {
		t.Fatalf("zero-byte boundary callback error=%v, want %v", err, callbackErr)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", period1Policy.EntitlementID()); got != 2 {
		t.Fatalf("metering events after boundary callback failure=%d, want 2", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", period1Policy.EntitlementID()); got != 2 {
		t.Fatalf("commercial sources after boundary callback failure=%d, want 2", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_debit_outbox", period1Policy.EntitlementID()); got != 1 {
		t.Fatalf("commercial outbox rows after boundary callback failure=%d, want 1", got)
	}
	intervalRows, err := db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT billable_bytes FROM whitelist_metering_intervals WHERE event_id=?`,
		Args: []any{boundary.EventID},
	})
	if err != nil || len(intervalRows) != 1 || len(intervalRows[0].Rows) != 1 ||
		intervalRows[0].Rows[0]["billable_bytes"] != "0" {
		t.Fatalf("durable zero-byte boundary interval=%#v err=%v", intervalRows, err)
	}

	boundaryBinding, err := BindCommercialMeteringSource(boundary, period1Policy)
	if err != nil {
		t.Fatalf("bind period 1 boundary source: %v", err)
	}
	boundaryDebit := commercialDebitFromBinding(boundaryBinding)
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(
		boundaryDebit.MeterEpoch, boundaryDebit.IntervalID,
	)
	if err != nil {
		t.Fatalf("period 1 boundary receipt key: %v", err)
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(boundaryDebit)
	if err != nil {
		t.Fatalf("period 1 boundary request hash: %v", err)
	}

	restartedStore, err := NewDurableStore(db)
	if err != nil {
		t.Fatalf("restart durable store: %v", err)
	}
	if err := restartedStore.DrainCommercialDebits(ctx, period1Policy.EntitlementID(), service); err != nil {
		t.Fatalf("drain committed zero-byte boundary receipt after restart: %v; request error: %v", err, controlDB.lastErr)
	}

	results, err := db.QueryLinearizable(ctx,
		rqlite.Statement{
			SQL:  `SELECT current_period_id FROM whitelist_balance_projections WHERE entitlement_id=?`,
			Args: []any{period1Policy.EntitlementID()},
		},
		rqlite.Statement{
			SQL: `SELECT request_hash,resource_id,status FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`,
			Args: []any{
				whitelistmetering.CommercialDebitReceiptScope,
				whitelistmetering.CommercialDebitReceiptCommand,
				receiptKey,
			},
		},
	)
	if err != nil || len(results) != 2 || len(results[0].Rows) != 1 || len(results[1].Rows) != 1 {
		t.Fatalf("period boundary state after restart drain=%#v err=%v", results, err)
	}
	if got := results[0].Rows[0]["current_period_id"]; got != period2.ID {
		t.Fatalf("persisted current period after restart drain=%#v, want %q", got, period2.ID)
	}
	receipt := results[1].Rows[0]
	if receipt["request_hash"] != requestHash ||
		receipt["resource_id"] != period1Policy.EntitlementID() || receipt["status"] != "applied" {
		t.Fatalf("period 1 boundary receipt after restart=%#v", receipt)
	}

	period2Policy := period1Policy
	period2Policy.billingPeriodID = period2.ID
	clock.now = time.Unix(period2.StartsAtUnix+2, 0)
	period2Source := commercialMeteringEvent(
		period2Policy, source, "boundary-restart-period-2-source", 1, 3, 100, 200, period2.StartsAtUnix+1,
	)
	period2Result, err := restartedStore.ApplyCommercialOrdered(ctx, period2Source, period2Policy, service)
	if err != nil {
		t.Fatalf("period 2 source after restart drain: %v; request error: %v", err, controlDB.lastErr)
	}
	if period2Result.Decision.Interval != nil {
		t.Fatalf("period 2 first source after restart interval=%#v, want accepted baseline", period2Result.Decision.Interval)
	}
}
