package shadowbilling

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestCommercialProducerResumesAcceptedCursorAndPendingDebitsAfterRestart(t *testing.T) {
	ctx := context.Background()
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, policy, source := newCommercialMeteringFixture(t, db, "commercial-producer-restart")

	initial, err := store.EnsureCommercialProducerCursor(ctx, source, 1_999_000)
	if err != nil || initial.NextSampleSequence != 1 {
		t.Fatalf("initial cursor=%#v err=%v", initial, err)
	}
	if _, err := store.EnsureCommercialProducerCursor(ctx, initial.Source, 1_999_001); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("returned bound source replay error=%v, want ErrInvalidInput", err)
	}
	baseline := commercialMeteringEvent(
		policy, initial.Source, "producer-baseline", 1, initial.NextSampleSequence, 100, 200, 2_000_000,
	)
	baseline.MeterEpoch = initial.MeterEpoch
	if _, err := store.ApplyCommercialOrdered(ctx, baseline, policy, &commercialDebitRecorder{db: db}); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}

	restarted, err := NewDurableStore(db)
	if err != nil {
		t.Fatalf("restart durable store: %v", err)
	}
	resumed, err := restarted.EnsureCommercialProducerCursor(ctx, source, 2_000_001)
	if err != nil || resumed.MeterEpoch != initial.MeterEpoch || resumed.NextSampleSequence != 2 {
		t.Fatalf("resumed cursor=%#v err=%v, want epoch=%q sequence=2", resumed, err, initial.MeterEpoch)
	}
	delta := commercialMeteringEvent(
		policy, resumed.Source, "producer-delta", 1, resumed.NextSampleSequence, 130, 240, 2_000_001,
	)
	delta.MeterEpoch = resumed.MeterEpoch
	if _, err := restarted.ApplyCommercialOrdered(ctx, delta, policy, &commercialFailOnceDebiter{
		err: errors.New("synthetic debit outage"),
	}); err == nil {
		t.Fatal("commercial interval unexpectedly lost its pending debit")
	}

	restartedAgain, err := NewDurableStore(db)
	if err != nil {
		t.Fatalf("second restart durable store: %v", err)
	}
	afterPending, err := restartedAgain.EnsureCommercialProducerCursor(ctx, source, 2_000_002)
	if err != nil || afterPending.MeterEpoch != initial.MeterEpoch || afterPending.NextSampleSequence != 3 {
		t.Fatalf("cursor after pending debit=%#v err=%v, want epoch=%q sequence=3", afterPending, err, initial.MeterEpoch)
	}
	pending, err := restartedAgain.PendingCommercialDebitEntitlementIDs(ctx)
	if err != nil || !reflect.DeepEqual(pending, []string{policy.EntitlementID()}) {
		t.Fatalf("pending entitlements=%#v err=%v", pending, err)
	}

	resetSource := source
	resetSource.ResetSequence++
	reset, err := restartedAgain.EnsureCommercialProducerCursor(ctx, resetSource, 2_000_002)
	if err != nil || reset.MeterEpoch == initial.MeterEpoch || reset.NextSampleSequence != 1 {
		t.Fatalf("reset cursor=%#v err=%v", reset, err)
	}
	replayed, err := restartedAgain.EnsureCommercialProducerCursor(ctx, resetSource, 2_000_003)
	if err != nil || !reflect.DeepEqual(replayed, reset) {
		t.Fatalf("epoch replay=%#v err=%v, want %#v", replayed, err, reset)
	}
}

func TestCommercialProducerBindsSharedStatsServicePerRoute(t *testing.T) {
	ctx := context.Background()
	db := newMeteringSQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply control-plane migrations: %v", err)
	}
	store, firstPolicy, sharedSource := newCommercialMeteringFixture(t, db, "commercial-producer-shared-a")
	_, secondPolicy, secondSource := newCommercialMeteringFixture(t, db, "commercial-producer-shared-b", "shared-period-b")
	secondSource.OriginID = sharedSource.OriginID
	secondSource.ExitID = sharedSource.ExitID
	secondSource.CounterSourceID = sharedSource.CounterSourceID
	secondSource.XrayProcessBootID = sharedSource.XrayProcessBootID

	first, err := store.EnsureCommercialProducerCursor(ctx, sharedSource, 1_999_000)
	if err != nil {
		t.Fatalf("first cursor: %v", err)
	}
	second, err := store.EnsureCommercialProducerCursor(ctx, secondSource, 1_999_000)
	if err != nil {
		t.Fatalf("second cursor: %v", err)
	}
	if first.Source.CounterSourceID == sharedSource.CounterSourceID ||
		second.Source.CounterSourceID == secondSource.CounterSourceID ||
		first.Source.CounterSourceID == second.Source.CounterSourceID ||
		first.MeterEpoch == second.MeterEpoch {
		t.Fatalf("route bindings first=%#v second=%#v", first, second)
	}

	for index, item := range []struct {
		policy Policy
		cursor CommercialProducerCursor
	}{
		{policy: firstPolicy, cursor: first},
		{policy: secondPolicy, cursor: second},
	} {
		event := commercialMeteringEvent(
			item.policy, item.cursor.Source, "shared-source-"+uintText(uint64(index+1)),
			1, item.cursor.NextSampleSequence, 100, 200, 2_000_000,
		)
		event.MeterEpoch = item.cursor.MeterEpoch
		if _, err := store.ApplyCommercialOrdered(ctx, event, item.policy, &commercialDebitRecorder{db: db}); err != nil {
			t.Fatalf("apply route %d: %v", index+1, err)
		}
	}
}
