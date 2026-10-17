package shadowbilling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/whitelistfixture"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

type commercialControlPlaneClock struct{ now time.Time }

func (clock commercialControlPlaneClock) Now() time.Time { return clock.now }

type commercialControlPlaneIDs struct{ next int }

func (ids *commercialControlPlaneIDs) NewID(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s-commercial-%04d", prefix, ids.next), nil
}

type commercialRequestObserver struct {
	rqlite.RQLite
	lastErr error
}

type commercialFailOnceDebiter struct {
	delegate CommercialDebiter
	err      error
}

func (debiter *commercialFailOnceDebiter) DebitCommercialInterval(
	ctx context.Context,
	debit whitelistmetering.CommercialDebit,
) error {
	if debiter.err != nil {
		err := debiter.err
		debiter.err = nil
		return err
	}
	return debiter.delegate.DebitCommercialInterval(ctx, debit)
}

func (observer *commercialRequestObserver) Request(
	ctx context.Context,
	level rqlite.Consistency,
	transaction bool,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	results, err := observer.RQLite.Request(ctx, level, transaction, statements...)
	observer.lastErr = err
	return results, err
}

type commercialDebitRecorder struct {
	db       rqlite.RQLite
	attempts []whitelistmetering.CommercialDebit
	accepted map[string]whitelistmetering.CommercialDebit
	failNext error
}

func (recorder *commercialDebitRecorder) DebitCommercialInterval(
	ctx context.Context,
	request whitelistmetering.CommercialDebit,
) error {
	recorder.attempts = append(recorder.attempts, request)
	if recorder.failNext != nil {
		err := recorder.failNext
		recorder.failNext = nil
		return err
	}
	if recorder.accepted == nil {
		recorder.accepted = make(map[string]whitelistmetering.CommercialDebit)
	}
	key := request.MeterEpoch + "\x00" + request.IntervalID + "\x00" + request.Basis
	if stored, ok := recorder.accepted[key]; ok {
		if stored != request {
			return errors.New("commercial debit replay conflict")
		}
		return nil
	}
	recorder.accepted[key] = request
	if recorder.db != nil {
		receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(request.MeterEpoch, request.IntervalID)
		if err != nil {
			return err
		}
		requestHash, err := whitelistmetering.CommercialDebitReceiptHash(request)
		if err != nil {
			return err
		}
		_, err = recorder.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
			SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,
status,response_json,created_at_unix,applied_at_unix)
VALUES(?,?,?,?,?,?,?,'applied','{}',2000000,2000000)`,
			Args: []any{
				whitelistmetering.CommercialDebitReceiptScope,
				whitelistmetering.CommercialDebitReceiptCommand,
				receiptKey, requestHash, request.EntitlementID,
				"accepted", "test-debit-" + request.IntervalID,
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func TestCommercialDebitPersistsExactSourceBeforeIdempotentDebit(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-debit")
	recorder := &commercialDebitRecorder{db: db}

	baseline := commercialMeteringEvent(policy, source, "commercial-baseline", 1, 1, 100, 200, 2_000_000)
	result, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder)
	if err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	if result.Decision.Interval != nil || len(recorder.attempts) != 0 {
		t.Fatalf("baseline decision=%#v debit attempts=%#v", result.Decision, recorder.attempts)
	}

	delta := commercialMeteringEvent(policy, source, "commercial-delta", 1, 2, 120, 230, 2_000_001)
	wantBinding, err := BindCommercialMeteringSource(delta, policy)
	if err != nil {
		t.Fatalf("bind expected commercial source: %v", err)
	}
	first, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder)
	if err != nil {
		t.Fatalf("commercial delta: %v", err)
	}
	if first.Decision.Interval == nil || first.Decision.Interval.BillableBytes != 50 {
		t.Fatalf("commercial interval=%#v", first.Decision.Interval)
	}
	wantDebit := whitelistmetering.CommercialDebit{
		EntitlementID: policy.EntitlementID(), BillingPeriodID: policy.BillingPeriodID(),
		MeterEpoch: delta.MeterEpoch, IntervalID: delta.EventID,
		Basis: string(BasisUplinkPlusDownlink), IntervalEndUnix: delta.SampledAtUnix,
		SourceSHA256: wantBinding.SourceSHA256,
	}
	if len(recorder.attempts) != 1 || recorder.attempts[0] != wantDebit || len(recorder.accepted) != 1 {
		t.Fatalf("debit attempts=%#v accepted=%#v, want %#v", recorder.attempts, recorder.accepted, wantDebit)
	}
	wantReceiptKey, err := whitelistmetering.CommercialDebitReceiptKey(wantDebit.MeterEpoch, wantDebit.IntervalID)
	if err != nil {
		t.Fatalf("commercial receipt key: %v", err)
	}
	wantRequestHash, err := whitelistmetering.CommercialDebitReceiptHash(wantDebit)
	if err != nil {
		t.Fatalf("commercial receipt hash: %v", err)
	}
	outbox, err := db.QueryLinearizable(context.Background(), rqlite.Statement{SQL: `SELECT
event_id,entitlement_id,billing_period_id,meter_epoch,basis,interval_end_unix,
source_sha256,receipt_key,request_hash
FROM whitelist_commercial_debit_outbox WHERE event_id=?`, Args: []any{delta.EventID}})
	if err != nil || len(outbox) != 1 || len(outbox[0].Rows) != 1 {
		t.Fatalf("commercial outbox=%#v err=%v", outbox, err)
	}
	row := outbox[0].Rows[0]
	if row["entitlement_id"] != wantDebit.EntitlementID ||
		row["billing_period_id"] != wantDebit.BillingPeriodID ||
		row["meter_epoch"] != wantDebit.MeterEpoch || row["basis"] != wantDebit.Basis ||
		row["source_sha256"] != wantDebit.SourceSHA256 ||
		row["receipt_key"] != wantReceiptKey || row["request_hash"] != wantRequestHash {
		t.Fatalf("commercial outbox row=%#v", row)
	}

	replayed, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder)
	if err != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("commercial replay=%#v err=%v, want %#v", replayed, err, first)
	}
	if len(recorder.attempts) != 1 || len(recorder.accepted) != 1 {
		t.Fatalf("replay attempts=%d accepted=%d", len(recorder.attempts), len(recorder.accepted))
	}

	rows, err := db.QueryLinearizable(context.Background(), rqlite.Statement{SQL: `SELECT
event_id,entitlement_id,billing_period_id,origin_id,exit_id,meter_epoch,
route_xray_identity,counter_generation,sample_sequence,basis,sampled_at_unix,source_sha256
FROM whitelist_commercial_metering_sources ORDER BY event_id`})
	if err != nil || len(rows) != 1 || len(rows[0].Rows) != 2 {
		t.Fatalf("commercial source rows=%#v err=%v", rows, err)
	}
	deltaRow := rows[0].Rows[1]
	if deltaRow["event_id"] != delta.EventID || deltaRow["source_sha256"] != wantBinding.SourceSHA256 ||
		deltaRow["route_xray_identity"] != source.RouteXrayIdentity || deltaRow["basis"] != string(BasisUplinkPlusDownlink) {
		t.Fatalf("commercial source row=%#v", deltaRow)
	}
}

func TestCommercialDebitUsesRealControlPlaneAdapterAndExactReceipt(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	const accountID = "commercial-real-controlplane"
	shadowStore, policy, source := newCommercialMeteringFixture(t, db, accountID)
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
		Args: []any{policy.EntitlementID(), policy.BillingPeriodID(), clock.Now().Unix()},
	})
	accountDigest := sha256.Sum256([]byte(accountID))
	sourceOrderID := "order-" + hex.EncodeToString(accountDigest[:])[:16]
	if _, err := service.CreditWhiteListPurchasedBytes(
		context.Background(), clock.Now().Unix(), controlplane.CreditWhiteListPurchasedBytesCommand{
			EntitlementID: policy.EntitlementID(), PeriodID: policy.BillingPeriodID(),
			SourceOrderID: sourceOrderID, Bytes: 100,
		},
	); err != nil {
		t.Fatalf("credit commercial bytes: %v; request error: %v", err, controlDB.lastErr)
	}

	baseline := commercialMeteringEvent(policy, source, "real-adapter-baseline", 1, 1, 100, 200, 2_000_000)
	if _, err := shadowStore.ApplyCommercialOrdered(context.Background(), baseline, policy, service); err != nil {
		t.Fatalf("real adapter baseline: %v", err)
	}
	delta := commercialMeteringEvent(policy, source, "real-adapter-delta", 1, 2, 120, 230, 2_000_001)
	binding, err := BindCommercialMeteringSource(delta, policy)
	if err != nil {
		t.Fatalf("bind real adapter delta: %v", err)
	}
	if _, err := shadowStore.ApplyCommercialOrdered(context.Background(), delta, policy, service); err != nil {
		t.Fatalf("real adapter delta: %v", err)
	}

	debit := commercialDebitFromBinding(binding)
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(debit.MeterEpoch, debit.IntervalID)
	if err != nil {
		t.Fatalf("receipt key: %v", err)
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(debit)
	if err != nil {
		t.Fatalf("receipt hash: %v", err)
	}
	results, err := db.QueryLinearizable(context.Background(),
		rqlite.Statement{
			SQL: `SELECT purchased_remaining_bytes,lifetime_consumed_bytes,version
FROM whitelist_balance_projections WHERE entitlement_id=?`,
			Args: []any{policy.EntitlementID()},
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
		t.Fatalf("real adapter durable result=%#v err=%v", results, err)
	}
	projection := results[0].Rows[0]
	purchased, purchasedErr := rowUint(projection, "purchased_remaining_bytes")
	consumed, consumedErr := rowUint(projection, "lifetime_consumed_bytes")
	version, versionErr := rowUint(projection, "version")
	if purchasedErr != nil || consumedErr != nil || versionErr != nil ||
		purchased != 50 || consumed != 50 || version != 3 {
		t.Fatalf("real adapter projection=%#v", projection)
	}
	receipt := results[1].Rows[0]
	if receipt["request_hash"] != requestHash || receipt["resource_id"] != policy.EntitlementID() ||
		receipt["status"] != "applied" {
		t.Fatalf("real adapter receipt=%#v", receipt)
	}

	debitUnavailable := errors.New("real adapter unavailable")
	failOnce := &commercialFailOnceDebiter{delegate: service, err: debitUnavailable}
	pending := commercialMeteringEvent(policy, source, "real-adapter-pending", 1, 3, 130, 240, 2_000_002)
	if _, err := shadowStore.ApplyCommercialOrdered(context.Background(), pending, policy, failOnce); !errors.Is(err, debitUnavailable) {
		t.Fatalf("commit pending real adapter interval error=%v", err)
	}
	clock.now = time.Unix(2_100_001, 0)
	_, err = service.ScheduleWhiteListPeriod(context.Background(), clock.Now().Unix(), controlplane.ScheduleWhiteListPeriodCommand{
		EntitlementID: policy.EntitlementID(),
		Period: whitelistbalance.Period{
			ID: "period-2", Ordinal: 1, StartsAtUnix: 2_100_000, EndsAtUnix: 2_200_000,
			IncludedGrantBytes: 0, AccessOrderID: "ordinary-order-2",
		},
	})
	if !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("schedule while debit pending error=%v, want ErrUnavailable", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_debit_outbox", policy.EntitlementID()); got != 2 {
		t.Fatalf("commercial outbox rows before drain=%d, want 2", got)
	}
	if err := shadowStore.DrainCommercialDebits(context.Background(), policy.EntitlementID(), service); err != nil {
		t.Fatalf("drain real adapter interval after blocked rollover: %v", err)
	}
	afterDrain, err := db.QueryLinearizable(context.Background(), rqlite.Statement{
		SQL: `SELECT current_period_id,purchased_remaining_bytes,lifetime_consumed_bytes,version
FROM whitelist_balance_projections WHERE entitlement_id=?`,
		Args: []any{policy.EntitlementID()},
	})
	if err != nil || len(afterDrain) != 1 || len(afterDrain[0].Rows) != 1 {
		t.Fatalf("real adapter projection after drain=%#v err=%v", afterDrain, err)
	}
	finalProjection := afterDrain[0].Rows[0]
	finalPurchased, finalPurchasedErr := rowUint(finalProjection, "purchased_remaining_bytes")
	finalConsumed, finalConsumedErr := rowUint(finalProjection, "lifetime_consumed_bytes")
	finalVersion, finalVersionErr := rowUint(finalProjection, "version")
	currentPeriod, currentPeriodOK := finalProjection["current_period_id"]
	if finalPurchasedErr != nil || finalConsumedErr != nil || finalVersionErr != nil ||
		finalPurchased != 30 || finalConsumed != 70 || finalVersion != 4 ||
		!currentPeriodOK || currentPeriod != nil {
		t.Fatalf("real adapter final projection=%#v", finalProjection)
	}
}

func TestCommercialDebitRecoversAfterCallbackFailureWithoutDuplicateMetering(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-recovery")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "recovery-baseline", 1, 1, 0, 0, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}

	debitUnavailable := errors.New("debit unavailable")
	recorder.failNext = debitUnavailable
	delta := commercialMeteringEvent(policy, source, "recovery-delta", 1, 2, 20, 30, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder); !errors.Is(err, debitUnavailable) {
		t.Fatalf("first callback error=%v, want debit unavailable", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 2 {
		t.Fatalf("metering event count after callback failure=%d, want 2", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", policy.EntitlementID()); got != 2 {
		t.Fatalf("commercial source count after callback failure=%d, want 2", got)
	}

	next := commercialMeteringEvent(policy, source, "recovery-next", 1, 3, 30, 50, 2_000_002)
	if _, err := store.ApplyCommercialOrdered(context.Background(), next, policy, recorder); err != nil {
		t.Fatalf("next commercial interval with pending recovery: %v", err)
	}
	if len(recorder.accepted) != 2 || len(recorder.attempts) != 3 {
		t.Fatalf("retry attempts=%d accepted=%d", len(recorder.attempts), len(recorder.accepted))
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 3 {
		t.Fatalf("metering event count after recovery=%d, want 3", got)
	}
}

func TestCommercialDebitRejectsChangedSourceOnEventReplay(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-conflict")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "conflict-baseline", 1, 1, 0, 0, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	delta := commercialMeteringEvent(policy, source, "conflict-delta", 1, 2, 1, 1, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder); err != nil {
		t.Fatalf("commercial delta: %v", err)
	}

	changed := delta
	changed.Source.ExitID = "ru"
	changed.Source.RouteXrayIdentity = "wl:" + policy.EntitlementID() + ":ru"
	if _, err := store.ApplyCommercialOrdered(context.Background(), changed, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("changed source replay error=%v, want ErrEventIDConflict", err)
	}
	if len(recorder.attempts) != 1 {
		t.Fatalf("conflicting replay reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitRejectsTimestampRegressionAtomically(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-time-regression")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "time-baseline", 1, 1, 10, 10, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	regressed := commercialMeteringEvent(policy, source, "time-regressed", 1, 2, 20, 20, 1_999_999)
	if _, err := store.ApplyCommercialOrdered(context.Background(), regressed, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("timestamp regression error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 1 {
		t.Fatalf("metering events after timestamp regression=%d, want 1", got)
	}
	if len(recorder.attempts) != 0 {
		t.Fatalf("timestamp regression reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitPersistsOlderLateSampleWithoutChargingIt(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-late-audit")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "late-baseline", 1, 1, 10, 10, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	newer := commercialMeteringEvent(policy, source, "late-newer", 1, 3, 30, 30, 2_000_002)
	if _, err := store.ApplyCommercialOrdered(context.Background(), newer, policy, recorder); err != nil {
		t.Fatalf("commercial newer interval: %v", err)
	}
	late := commercialMeteringEvent(policy, source, "late-audit", 1, 2, 20, 20, 2_000_001)
	result, err := store.ApplyCommercialOrdered(context.Background(), late, policy, recorder)
	if err != nil {
		t.Fatalf("commercial late audit: %v", err)
	}
	if result.Decision.Diagnostic != DiagnosticLateSample || result.Decision.Interval != nil {
		t.Fatalf("late decision=%#v, want diagnostic-only", result.Decision)
	}
	replayed, err := store.ApplyCommercialOrdered(context.Background(), late, policy, recorder)
	if err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("late replay=%#v err=%v, want %#v", replayed, err, result)
	}
	duplicate := late
	duplicate.EventID = "late-audit-duplicate"
	if _, err := store.ApplyCommercialOrdered(context.Background(), duplicate, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("late physical source replay error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 3 {
		t.Fatalf("metering events after late audit=%d, want 3", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", policy.EntitlementID()); got != 3 {
		t.Fatalf("commercial sources after late audit=%d, want 3", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_debit_outbox", policy.EntitlementID()); got != 1 {
		t.Fatalf("commercial outbox rows after late audit=%d, want 1", got)
	}
	if len(recorder.attempts) != 1 {
		t.Fatalf("late audit reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitLateSampleRetriesAfterPendingDebitRecovery(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-late-recovery")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "late-recovery-baseline", 1, 1, 10, 10, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}

	debitUnavailable := errors.New("debit unavailable")
	recorder.failNext = debitUnavailable
	newer := commercialMeteringEvent(policy, source, "late-recovery-newer", 1, 3, 30, 30, 2_000_002)
	if _, err := store.ApplyCommercialOrdered(context.Background(), newer, policy, recorder); !errors.Is(err, debitUnavailable) {
		t.Fatalf("newer callback error=%v, want debit unavailable", err)
	}

	recorder.failNext = debitUnavailable
	late := commercialMeteringEvent(policy, source, "late-recovery-audit", 1, 2, 20, 20, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), late, policy, recorder); !errors.Is(err, debitUnavailable) {
		t.Fatalf("late pre-drain error=%v, want debit unavailable", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 2 {
		t.Fatalf("metering events before late retry=%d, want 2", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", policy.EntitlementID()); got != 2 {
		t.Fatalf("commercial sources before late retry=%d, want 2", got)
	}

	result, err := store.ApplyCommercialOrdered(context.Background(), late, policy, recorder)
	if err != nil {
		t.Fatalf("late retry after pending recovery: %v", err)
	}
	if result.Decision.Diagnostic != DiagnosticLateSample || result.Decision.Interval != nil {
		t.Fatalf("late retry decision=%#v, want diagnostic-only", result.Decision)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 3 {
		t.Fatalf("metering events after late retry=%d, want 3", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", policy.EntitlementID()); got != 3 {
		t.Fatalf("commercial sources after late retry=%d, want 3", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_debit_outbox", policy.EntitlementID()); got != 1 {
		t.Fatalf("commercial outbox rows after late retry=%d, want 1", got)
	}
	if len(recorder.attempts) != 3 || len(recorder.accepted) != 1 {
		t.Fatalf("pending recovery attempts=%d accepted=%d", len(recorder.attempts), len(recorder.accepted))
	}
}

func TestCommercialDebitRejectsLateSequenceWithFutureTimestampAtomically(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-late-future")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "late-future-baseline", 1, 1, 10, 10, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	newer := commercialMeteringEvent(policy, source, "late-future-newer", 1, 3, 30, 30, 2_000_002)
	if _, err := store.ApplyCommercialOrdered(context.Background(), newer, policy, recorder); err != nil {
		t.Fatalf("commercial newer interval: %v", err)
	}

	lateFuture := commercialMeteringEvent(policy, source, "late-future-audit", 1, 2, 20, 20, 2_000_100)
	if _, err := store.ApplyCommercialOrdered(context.Background(), lateFuture, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("late sequence with future timestamp error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 2 {
		t.Fatalf("metering events after rejected future late sample=%d, want 2", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", policy.EntitlementID()); got != 2 {
		t.Fatalf("commercial sources after rejected future late sample=%d, want 2", got)
	}

	next := commercialMeteringEvent(policy, source, "late-future-next", 1, 4, 40, 40, 2_000_003)
	if _, err := store.ApplyCommercialOrdered(context.Background(), next, policy, recorder); err != nil {
		t.Fatalf("normal sample after rejected future late sample: %v", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 3 {
		t.Fatalf("metering events after normal sample=%d, want 3", got)
	}
	if len(recorder.attempts) != 2 || len(recorder.accepted) != 2 {
		t.Fatalf("future late sample reached debiter: attempts=%d accepted=%d", len(recorder.attempts), len(recorder.accepted))
	}
}

func TestCommercialDebitRejectsOldPeriodAfterBalanceAdvancedAtomically(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-period-advanced")
	recorder := &commercialDebitRecorder{db: db}
	db.must(t, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix)
VALUES(?,?,'0','0','0','0',1,0,0,2000000)`,
		Args: []any{policy.EntitlementID(), policy.BillingPeriodID()},
	})
	baseline := commercialMeteringEvent(policy, source, "period-baseline", 1, 1, 10, 10, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	db.must(t, rqlite.Statement{
		SQL:  `UPDATE whitelist_balance_projections SET current_period_id=NULL,version=2 WHERE entitlement_id=?`,
		Args: []any{policy.EntitlementID()},
	})
	oldPeriod := commercialMeteringEvent(policy, source, "period-old-source", 1, 2, 20, 20, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), oldPeriod, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("old-period source error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 1 {
		t.Fatalf("metering events after balance advance=%d, want 1", got)
	}
	if len(recorder.attempts) != 0 {
		t.Fatalf("old-period source reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitRejectsRouteReuseWithinEpochAtomically(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-epoch-route")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "route-baseline", 1, 1, 10, 10, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	otherRoute := source
	otherRoute.ExitID = "ru"
	otherRoute.RouteXrayIdentity = "wl:" + policy.EntitlementID() + ":ru"
	changed := commercialMeteringEvent(policy, otherRoute, "route-changed", 1, 2, 20, 20, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), changed, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("route reuse error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 1 {
		t.Fatalf("metering events after route reuse=%d, want 1", got)
	}
	if len(recorder.attempts) != 0 {
		t.Fatalf("route reuse reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitRejectsOutOfPeriodSourceAtomically(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-boundary")
	recorder := &commercialDebitRecorder{db: db}
	outOfPeriod := commercialMeteringEvent(policy, source, "outside-period", 1, 1, 1, 1, 2_100_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), outOfPeriod, policy, recorder); err == nil {
		t.Fatal("commercial source outside billing period was accepted")
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 0 {
		t.Fatalf("metering events after rollback=%d, want 0", got)
	}
	if got := commercialMeteringCount(t, db, "whitelist_commercial_metering_sources", policy.EntitlementID()); got != 0 {
		t.Fatalf("commercial sources after rollback=%d, want 0", got)
	}
	if len(recorder.attempts) != 0 {
		t.Fatalf("rejected source reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitDrainRecoversCommittedPendingInterval(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-drain")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "drain-baseline", 1, 1, 0, 0, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	recorder.failNext = errors.New("debit unavailable")
	delta := commercialMeteringEvent(policy, source, "drain-delta", 1, 2, 2, 3, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder); err == nil {
		t.Fatal("commercial callback failure was not returned")
	}
	if err := store.DrainCommercialDebits(
		context.Background(), policy.EntitlementID(), recorder,
	); err != nil {
		t.Fatalf("drain committed commercial debit: %v", err)
	}
	if len(recorder.attempts) != 2 || len(recorder.accepted) != 1 {
		t.Fatalf("drain attempts=%d accepted=%d", len(recorder.attempts), len(recorder.accepted))
	}
}

func TestCommercialDebitDrainRejectsConflictingReceiptHash(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-receipt-conflict")
	recorder := &commercialDebitRecorder{db: db, failNext: errors.New("debit unavailable")}
	baseline := commercialMeteringEvent(policy, source, "receipt-baseline", 1, 1, 0, 0, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	delta := commercialMeteringEvent(policy, source, "receipt-delta", 1, 2, 1, 1, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder); err == nil {
		t.Fatal("commercial callback failure was not returned")
	}
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(delta.MeterEpoch, delta.EventID)
	if err != nil {
		t.Fatalf("receipt key: %v", err)
	}
	db.must(t, rqlite.Statement{
		SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,
status,response_json,created_at_unix,applied_at_unix)
VALUES(?,?,?,?,?,?,?,'applied','{}',2000000,2000000)`,
		Args: []any{
			whitelistmetering.CommercialDebitReceiptScope,
			whitelistmetering.CommercialDebitReceiptCommand,
			receiptKey, strings.Repeat("a", 64), policy.EntitlementID(),
			"accepted", "conflicting-receipt-operation",
		},
	})
	if err := store.DrainCommercialDebits(context.Background(), policy.EntitlementID(), recorder); !errors.Is(err, ErrDurableStateInvalid) {
		t.Fatalf("conflicting receipt drain error=%v, want ErrDurableStateInvalid", err)
	}
	if len(recorder.attempts) != 1 {
		t.Fatalf("conflicting receipt reached debiter again: %#v", recorder.attempts)
	}
}

func TestCommercialDebitReceiptPreventsRepeatedZeroByteDrain(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-zero")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "zero-baseline", 1, 1, 10, 20, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	zero := commercialMeteringEvent(policy, source, "zero-interval", 1, 2, 10, 20, 2_000_001)
	result, err := store.ApplyCommercialOrdered(context.Background(), zero, policy, recorder)
	if err != nil {
		t.Fatalf("zero-byte commercial interval: %v", err)
	}
	if result.Decision.Interval == nil || result.Decision.Interval.BillableBytes != 0 {
		t.Fatalf("zero-byte interval=%#v", result.Decision.Interval)
	}
	next := commercialMeteringEvent(policy, source, "zero-next", 1, 3, 11, 21, 2_000_002)
	if _, err := store.ApplyCommercialOrdered(context.Background(), next, policy, recorder); err != nil {
		t.Fatalf("commercial interval after zero-byte receipt: %v", err)
	}
	if len(recorder.attempts) != 2 || len(recorder.accepted) != 2 {
		t.Fatalf("zero-byte receipt attempts=%d accepted=%d", len(recorder.attempts), len(recorder.accepted))
	}
}

func TestCommercialDebitRejectsMismatchedRegisteredEpochProvenance(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-epoch-conflict")
	recorder := &commercialDebitRecorder{db: db}
	event := commercialMeteringEvent(policy, source, "epoch-conflict", 1, 1, 1, 1, 2_000_000)
	event.Source.CounterSourceID += "-wrong"
	if _, err := store.ApplyCommercialOrdered(context.Background(), event, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("epoch provenance error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 0 {
		t.Fatalf("metering events after epoch conflict=%d, want 0", got)
	}
	if len(recorder.attempts) != 0 {
		t.Fatalf("epoch conflict reached debiter: %#v", recorder.attempts)
	}
}

func TestCommercialDebitClassifiesSamePhysicalSampleUnderAnotherEvent(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-source-replay")
	recorder := &commercialDebitRecorder{db: db}
	baseline := commercialMeteringEvent(policy, source, "source-replay-baseline", 1, 1, 0, 0, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), baseline, policy, recorder); err != nil {
		t.Fatalf("commercial baseline: %v", err)
	}
	delta := commercialMeteringEvent(policy, source, "source-replay-delta", 1, 2, 1, 1, 2_000_001)
	if _, err := store.ApplyCommercialOrdered(context.Background(), delta, policy, recorder); err != nil {
		t.Fatalf("commercial delta: %v", err)
	}
	duplicate := delta
	duplicate.EventID = "source-replay-other-event"
	if _, err := store.ApplyCommercialOrdered(context.Background(), duplicate, policy, recorder); !errors.Is(err, ErrEventIDConflict) {
		t.Fatalf("physical sample replay error=%v, want ErrEventIDConflict", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 2 {
		t.Fatalf("metering event count after source replay=%d, want 2", got)
	}
	if len(recorder.attempts) != 1 {
		t.Fatalf("source replay reached debiter: %#v", recorder.attempts)
	}
}

type commercialQueryFailure struct {
	rqlite.RQLite
	failAt int
	calls  int
	err    error
}

func (db *commercialQueryFailure) QueryLinearizable(
	ctx context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	db.calls++
	if db.calls == db.failAt {
		return nil, db.err
	}
	return db.RQLite.QueryLinearizable(ctx, statements...)
}

func TestCommercialDebitPreservesVerificationCancellationAfterCommit(t *testing.T) {
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	_, policy, source := newCommercialMeteringFixture(t, db, "commercial-query-cancel")
	failingDB := &commercialQueryFailure{RQLite: db, failAt: 4, err: context.Canceled}
	store, err := NewDurableStore(failingDB)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	recorder := &commercialDebitRecorder{db: db}
	event := commercialMeteringEvent(policy, source, "query-cancel-baseline", 1, 1, 0, 0, 2_000_000)
	if _, err := store.ApplyCommercialOrdered(context.Background(), event, policy, recorder); !errors.Is(err, context.Canceled) {
		t.Fatalf("verification error=%v, want context.Canceled", err)
	}
	if got := commercialMeteringCount(t, db, "whitelist_metering_events", policy.EntitlementID()); got != 1 {
		t.Fatalf("committed metering event count=%d, want 1", got)
	}
	if len(recorder.attempts) != 0 {
		t.Fatalf("unverified source reached debiter: %#v", recorder.attempts)
	}
}

func newCommercialMeteringFixture(
	t *testing.T,
	db *meteringSQLite,
	accountID string,
	periodIDs ...string,
) (*DurableStore, Policy, CommercialMeterSource) {
	t.Helper()
	periodID := "period-1"
	if len(periodIDs) > 1 {
		t.Fatal("commercial fixture accepts at most one explicit period ID")
	}
	if len(periodIDs) == 1 {
		periodID = periodIDs[0]
	}
	entitlement := whitelistfixture.MustPersisted(t, accountID)
	var err error
	entitlement, err = entitlement.Activate("profile-a", "preset-a", "release-a", controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatalf("activate entitlement: %v", err)
	}
	policy, err := NewPolicy(entitlement, PolicySpec{
		BillingPeriodID: periodID, Unit: UnitGBDecimal, Basis: BasisUplinkPlusDownlink,
		IncludedBytes: 0, SoftLimitBytes: 1_000, HardLimitBytes: 2_000, GraceBytes: 100,
		Prices: PriceOptions{Global: &Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 100}},
	})
	if err != nil {
		t.Fatalf("new commercial policy: %v", err)
	}
	accountDigest := sha256.Sum256([]byte(accountID))
	digest := hex.EncodeToString(accountDigest[:])
	orderID := "order-" + digest[:16]
	originID := "origin-" + digest[:12]
	epochID := "epoch-" + digest[:12]
	source := CommercialMeterSource{
		OriginID: originID, ExitID: "nl", CounterSourceID: originID + "-handler-api",
		XrayProcessBootID: "boot-" + digest[:16], ResetSequence: 0,
		RouteXrayIdentity: "wl:" + policy.EntitlementID() + ":nl",
	}
	db.must(t,
		rqlite.Statement{
			SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,'active',3000000,1,1900000,1900000)`,
			Args: []any{accountID, accountID, digest},
		},
		rqlite.Statement{
			SQL:  `INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES(?,?,1900000)`,
			Args: []any{policy.EntitlementID(), accountID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,1900000,1986400,
'confirmed','applied','confirmed',1900100,3000000,1,?)`,
			Args: []any{orderID, "COMM-" + digest[:8], "commercial-metering", digest, accountID, orderID + "-operation"},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
VALUES(?,?,0,1900000,2100000,0,?,1900100)`,
			Args: []any{policy.BillingPeriodID(), policy.EntitlementID(), orderID},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_meter_epochs(
meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix)
VALUES(?,?,?,?,0,1999000)`,
			Args: []any{epochID, source.OriginID, source.CounterSourceID, source.XrayProcessBootID},
		},
	)
	store, err := NewDurableStore(db)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	return store, policy, source
}

func commercialMeteringEvent(
	policy Policy,
	source CommercialMeterSource,
	eventID string,
	generation, sequence, uplink, downlink uint64,
	sampledAtUnix int64,
) CommercialOrderedUsageEvent {
	return CommercialOrderedUsageEvent{
		OrderedUsageEvent: OrderedUsageEvent{
			UsageEvent: UsageEvent{
				EventID: eventID, InstanceID: source.OriginID,
				MeterEpoch:   strings.Replace(source.OriginID, "origin-", "epoch-", 1),
				XrayIdentity: "wl:" + policy.EntitlementID(),
				UplinkBytes:  uplink, DownlinkBytes: downlink,
			},
			CounterGeneration: generation, SampleSequence: sequence,
		},
		Source: source, SampledAtUnix: sampledAtUnix,
	}
}

func commercialMeteringCount(t *testing.T, db *meteringSQLite, table, entitlementID string) int {
	t.Helper()
	results, err := db.QueryLinearizable(context.Background(), rqlite.Statement{
		SQL: fmt.Sprintf("SELECT event_id FROM %s WHERE entitlement_id=?", table), Args: []any{entitlementID},
	})
	if err != nil || len(results) != 1 {
		t.Fatalf("count %s: results=%#v err=%v", table, results, err)
	}
	return len(results[0].Rows)
}
