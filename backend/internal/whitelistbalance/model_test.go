package whitelistbalance

import (
	"errors"
	"reflect"
	"testing"
)

func TestScheduleFirstZeroGrantHasNoImplicitCreditOrJournal(t *testing.T) {
	state := mustNewState(t)
	before := cloneTestState(state)
	req := SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 0, "access-order-1"),
	}

	transition := mustSchedule(t, state, req)
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("SchedulePeriod mutated its input: got %#v want %#v", state, before)
	}
	if transition.Replayed {
		t.Fatal("new schedule was reported as replayed")
	}
	projection := mustProjection(t, transition.State)
	if projection.Version != 1 || projection.CurrentPeriodID != "period-1" {
		t.Fatalf("unexpected initial projection: %#v", projection)
	}
	if projection.IncludedRemainingBytes != 0 || projection.PurchasedRemainingBytes != 0 {
		t.Fatalf("zero-grant period credited bytes: %#v", projection)
	}
	if projection.IncludedRemainingBytes == 2*GBDecimal {
		t.Fatal("ordinary accounting period received the retired implicit 2 GB grant")
	}
	if len(transition.Journal) != 0 {
		t.Fatalf("zero grant created journal intents: %#v", transition.Journal)
	}
}

func TestExplicitGrantCreatesOneJournalEntryAndOutstandingBalance(t *testing.T) {
	state := mustNewState(t)
	transition := mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-explicit",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 2*GBDecimal, "access-order-1"),
	})

	projection := mustProjection(t, transition.State)
	if projection.IncludedRemainingBytes != 2*GBDecimal {
		t.Fatalf("included remaining = %d, want %d", projection.IncludedRemainingBytes, 2*GBDecimal)
	}
	if got := transition.State.IncludedOutstandingBytes["period-1"]; got != 2*GBDecimal {
		t.Fatalf("journal-derived outstanding = %d, want %d", got, 2*GBDecimal)
	}
	if len(transition.Journal) != 1 {
		t.Fatalf("journal intents = %d, want 1", len(transition.Journal))
	}
	entry := transition.Journal[0]
	if entry.Kind != EntryIncludedGrant || entry.PeriodID != "period-1" || entry.SourceOrderID != "access-order-1" {
		t.Fatalf("unexpected included binding: %#v", entry)
	}
	if entry.IncludedDeltaBytes != 2*GBDecimal || entry.PurchasedDeltaBytes != 0 || entry.ConsumedDeltaBytes != 0 || entry.UncoveredDeltaBytes != 0 {
		t.Fatalf("unexpected included deltas: %#v", entry)
	}
}

func TestEarlyRenewalQueuesWithoutResettingCurrentQuota(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 100, "access-order-1"),
	}).State
	state = mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-1",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            40,
		PrimaryActive:    true,
	}).State

	second := mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     160,
		Period:      testPeriod("period-2", 2, 200, 300, 25, "access-order-2"),
	})
	if len(second.Journal) != 1 {
		t.Fatalf("future explicit grant journal intents = %d, want 1", len(second.Journal))
	}
	futureGrant := second.Journal[0]
	if futureGrant.Kind != EntryIncludedGrant || futureGrant.PeriodID != "period-2" || futureGrant.SourceOrderID != "access-order-2" || futureGrant.IncludedDeltaBytes != 25 {
		t.Fatalf("future explicit grant is not reconstructible from journal: %#v", futureGrant)
	}
	third := mustSchedule(t, second.State, SchedulePeriodRequest{
		OperationID: "schedule-3",
		NowUnix:     170,
		Period:      testPeriod("period-3", 3, 300, 400, 0, "access-order-3"),
	})

	projection := mustProjection(t, third.State)
	if projection.CurrentPeriodID != "period-1" || projection.IncludedRemainingBytes != 60 {
		t.Fatalf("early renewal reset current quota: %#v", projection)
	}
	if len(third.State.Periods) != 3 {
		t.Fatalf("queued periods = %d, want 3", len(third.State.Periods))
	}
	if projection.Version != 4 {
		t.Fatalf("projection version = %d, want 4", projection.Version)
	}
	if got := third.State.IncludedOutstandingBytes["period-2"]; got != 25 {
		t.Fatalf("future explicit grant outstanding = %d, want 25", got)
	}
}

func TestScheduleValidatesQueueIdentityOrdinalAndOverlap(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-zero-ordinal",
		NowUnix:     100,
		Period:      testPeriod("period-0", 0, 100, 200, 0, "access-order-0"),
	}).State
	before := cloneTestState(state)

	tests := []struct {
		name        string
		operationID string
		period      Period
	}{
		{name: "overlap", operationID: "invalid-overlap", period: testPeriod("period-1", 1, 199, 300, 0, "access-order-1")},
		{name: "ordinal gap", operationID: "invalid-gap", period: testPeriod("period-2", 2, 200, 300, 0, "access-order-1")},
		{name: "duplicate period id", operationID: "invalid-period-id", period: testPeriod("period-0", 1, 200, 300, 0, "access-order-1")},
		{name: "duplicate ordinal", operationID: "invalid-ordinal", period: testPeriod("period-1", 0, 200, 300, 0, "access-order-1")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := SchedulePeriod(state, SchedulePeriodRequest{
				OperationID: test.operationID,
				NowUnix:     150,
				Period:      test.period,
			}, nil)
			if !errors.Is(err, ErrPeriodConflict) {
				t.Fatalf("SchedulePeriod error = %v, want ErrPeriodConflict", err)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("invalid queue mutation changed state")
			}
		})
	}
}

func TestAdvanceUsesHalfOpenBoundaryAndExpiresOnlyUnusedIncluded(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 100, "access-order-1"),
	}).State
	state = mustCredit(t, state, CreditPurchasedRequest{
		OperationID:   "credit-1",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-1",
		NowUnix:       120,
		Bytes:         70,
		PrimaryActive: true,
	}).State
	state = mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-1",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            40,
		PrimaryActive:    true,
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     160,
		Period:      testPeriod("period-2", 2, 200, 300, 25, "access-order-2"),
	}).State
	before := cloneTestState(state)

	advanced, err := AdvanceAt(state, 200)
	if err != nil {
		t.Fatalf("AdvanceAt: %v", err)
	}
	projection := mustProjection(t, advanced.State)
	if projection.CurrentPeriodID != "period-2" || projection.IncludedRemainingBytes != 25 {
		t.Fatalf("half-open boundary did not activate period-2: %#v", projection)
	}
	if projection.PurchasedRemainingBytes != 70 {
		t.Fatalf("purchased bytes did not persist across rollover: %d", projection.PurchasedRemainingBytes)
	}
	if got := advanced.State.IncludedOutstandingBytes["period-1"]; got != 0 {
		t.Fatalf("expired period outstanding = %d, want 0", got)
	}
	if got := advanced.State.IncludedOutstandingBytes["period-2"]; got != 25 {
		t.Fatalf("new period outstanding = %d, want 25", got)
	}
	if len(advanced.Journal) != 1 {
		t.Fatalf("rollover journal intents = %d, want 1", len(advanced.Journal))
	}
	adjustment := advanced.Journal[0]
	if adjustment.Kind != EntryAdjustment || adjustment.PeriodID != "period-1" || adjustment.SourceOrderID != "access-order-1" || adjustment.IntervalID != "" || adjustment.IncludedDeltaBytes != -60 || adjustment.PurchasedDeltaBytes != 0 || adjustment.ConsumedDeltaBytes != 0 || adjustment.UncoveredDeltaBytes != 0 {
		t.Fatalf("unexpected rollover adjustment: %#v", adjustment)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("AdvanceAt mutated its input")
	}
}

func TestAdvanceSkipsMultipleQueuedPeriodsExactlyOnce(t *testing.T) {
	state := mustNewState(t)
	for _, period := range []Period{
		testPeriod("period-1", 1, 100, 200, 10, "access-order-1"),
		testPeriod("period-2", 2, 200, 300, 20, "access-order-2"),
		testPeriod("period-3", 3, 300, 400, 30, "access-order-3"),
	} {
		state = mustSchedule(t, state, SchedulePeriodRequest{
			OperationID: "schedule-" + period.ID,
			NowUnix:     120,
			Period:      period,
		}).State
	}

	advanced, err := AdvanceAt(state, 400)
	if err != nil {
		t.Fatalf("AdvanceAt: %v", err)
	}
	projection := mustProjection(t, advanced.State)
	if projection.CurrentPeriodID != "" || projection.IncludedRemainingBytes != 0 {
		t.Fatalf("advance past queue left active included bytes: %#v", projection)
	}
	if len(advanced.Journal) != 3 {
		t.Fatalf("expiry intents = %d, want 3", len(advanced.Journal))
	}
	for _, periodID := range []string{"period-1", "period-2", "period-3"} {
		if got := advanced.State.IncludedOutstandingBytes[periodID]; got != 0 {
			t.Fatalf("%s outstanding = %d, want 0", periodID, got)
		}
	}

	repeated, err := AdvanceAt(advanced.State, 500)
	if err != nil {
		t.Fatalf("AdvanceAt repeat: %v", err)
	}
	if len(repeated.Journal) != 0 {
		t.Fatalf("repeated advance duplicated expiry intents: %#v", repeated.Journal)
	}
	if got, want := mustProjection(t, repeated.State).Version, projection.Version; got != want {
		t.Fatalf("no-op advance version = %d, want %d", got, want)
	}
}

func TestAdvanceNeverEmitsZeroValuedAdjustment(t *testing.T) {
	t.Run("zero grant", func(t *testing.T) {
		state := mustSchedule(t, mustNewState(t), SchedulePeriodRequest{
			OperationID: "schedule-zero",
			NowUnix:     100,
			Period:      testPeriod("period-1", 1, 100, 200, 0, "access-order-1"),
		}).State
		advanced, err := AdvanceAt(state, 200)
		if err != nil {
			t.Fatalf("AdvanceAt: %v", err)
		}
		if len(advanced.Journal) != 0 {
			t.Fatalf("zero grant emitted adjustment: %#v", advanced.Journal)
		}
	})

	t.Run("fully consumed grant", func(t *testing.T) {
		state := mustBalanceState(t, 10, 0)
		state = mustApply(t, state, ApplyUsageRequest{
			OperationID:      "usage-all-included",
			PeriodID:         "period-1",
			MeterEpoch:       "epoch-1",
			IntervalID:       "interval-all-included",
			AppliedAtUnix:    150,
			IntervalEndUnix:  150,
			FreshThroughUnix: 150,
			Bytes:            10,
			PrimaryActive:    true,
		}).State
		advanced, err := AdvanceAt(state, 200)
		if err != nil {
			t.Fatalf("AdvanceAt: %v", err)
		}
		if len(advanced.Journal) != 0 {
			t.Fatalf("fully consumed grant emitted adjustment: %#v", advanced.Journal)
		}
	})
}

func TestPurchasedPersistsAcrossRolloverAndPrimaryExpiry(t *testing.T) {
	state := mustBalanceState(t, 0, 70)
	advanced, err := AdvanceAt(state, 200)
	if err != nil {
		t.Fatalf("AdvanceAt: %v", err)
	}
	snapshot, err := Snapshot(advanced.State, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Projection.PurchasedRemainingBytes != 70 || snapshot.AvailableBytes != 70 {
		t.Fatalf("expired primary discarded purchased balance: %#v", snapshot)
	}
	if snapshot.UsableBytes != 0 || !snapshot.Frozen {
		t.Fatalf("expired primary balance was not frozen: %#v", snapshot)
	}
}

func TestCreditRequiresExactActivePeriod(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 0, "access-order-1"),
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 0, "access-order-2"),
	}).State
	before := cloneTestState(state)

	_, err := CreditPurchased(state, CreditPurchasedRequest{
		OperationID:   "credit-wrong-period",
		PeriodID:      "period-2",
		SourceOrderID: "topup-order-1",
		NowUnix:       150,
		Bytes:         10,
		PrimaryActive: true,
	}, nil)
	if !errors.Is(err, ErrPeriodConflict) {
		t.Fatalf("wrong-period credit error = %v, want ErrPeriodConflict", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("wrong-period credit mutated state")
	}

	transition := mustCredit(t, state, CreditPurchasedRequest{
		OperationID:   "credit-1",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-1",
		NowUnix:       150,
		Bytes:         10,
		PrimaryActive: true,
	})
	if len(transition.Journal) != 1 || transition.Journal[0].Kind != EntryPurchasedCredit || transition.Journal[0].PeriodID != "period-1" {
		t.Fatalf("unexpected purchased journal intent: %#v", transition.Journal)
	}
}

func TestCreditAtBoundaryRequiresNewPeriodAndActivePrimary(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 0, "access-order-1"),
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 0, "access-order-2"),
	}).State
	before := cloneTestState(state)

	_, err := CreditPurchased(state, CreditPurchasedRequest{
		OperationID:   "credit-old-boundary",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-old",
		NowUnix:       200,
		Bytes:         10,
		PrimaryActive: true,
	}, nil)
	if !errors.Is(err, ErrPeriodConflict) {
		t.Fatalf("old-period boundary credit error = %v, want ErrPeriodConflict", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("old-period boundary rejection advanced or mutated state")
	}

	credited := mustCredit(t, state, CreditPurchasedRequest{
		OperationID:   "credit-new-boundary",
		PeriodID:      "period-2",
		SourceOrderID: "topup-order-new",
		NowUnix:       200,
		Bytes:         10,
		PrimaryActive: true,
	})
	if len(credited.Journal) != 1 {
		t.Fatalf("boundary credit journal intents = %d, want 1", len(credited.Journal))
	}
	creditEntry := credited.Journal[0]
	if mustProjection(t, credited.State).CurrentPeriodID != "period-2" || creditEntry.Kind != EntryPurchasedCredit || creditEntry.PeriodID != "period-2" || creditEntry.SourceOrderID != "topup-order-new" || creditEntry.IntervalID != "" || creditEntry.IncludedDeltaBytes != 0 || creditEntry.PurchasedDeltaBytes != 10 || creditEntry.ConsumedDeltaBytes != 0 || creditEntry.UncoveredDeltaBytes != 0 {
		t.Fatalf("boundary credit did not bind new period: %#v", credited)
	}

	_, err = CreditPurchased(state, CreditPurchasedRequest{
		OperationID:   "credit-inactive",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-inactive",
		NowUnix:       150,
		Bytes:         10,
		PrimaryActive: false,
	}, nil)
	if !errors.Is(err, ErrPrimaryInactive) {
		t.Fatalf("inactive-primary credit error = %v, want ErrPrimaryInactive", err)
	}
}

func TestApplyUsageDebitsOldPeriodBeforeBoundaryRollover(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 100, "access-order-1"),
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 25, "access-order-2"),
	}).State

	transition := mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-boundary",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    200,
		IntervalEndUnix:  200,
		FreshThroughUnix: 200,
		Bytes:            40,
		PrimaryActive:    true,
	})
	if transition.Result.Allocation != (UsageAllocation{IncludedBytes: 40}) {
		t.Fatalf("boundary allocation = %#v, want old-period included 40", transition.Result.Allocation)
	}
	projection := mustProjection(t, transition.State)
	if projection.CurrentPeriodID != "period-2" || projection.IncludedRemainingBytes != 25 || projection.LifetimeConsumedBytes != 40 {
		t.Fatalf("unexpected boundary projection: %#v", projection)
	}
	if len(transition.Journal) != 2 {
		t.Fatalf("boundary journal intents = %d, want usage plus expiry", len(transition.Journal))
	}
	consumed := findJournalKind(t, transition.Journal, EntryConsumed)
	if consumed.PeriodID != "period-1" || consumed.IntervalID != "interval-1" || consumed.SourceOrderID != "" || consumed.IncludedDeltaBytes != -40 || consumed.PurchasedDeltaBytes != 0 || consumed.ConsumedDeltaBytes != 40 || consumed.UncoveredDeltaBytes != 0 {
		t.Fatalf("unexpected boundary consumption: %#v", consumed)
	}
	adjustment := findJournalKind(t, transition.Journal, EntryAdjustment)
	if adjustment.PeriodID != "period-1" || adjustment.SourceOrderID != "access-order-1" || adjustment.IncludedDeltaBytes != -60 {
		t.Fatalf("unexpected boundary expiry: %#v", adjustment)
	}
	if got := transition.State.IncludedOutstandingBytes["period-1"]; got != 0 {
		t.Fatalf("old-period outstanding = %d, want 0", got)
	}
	if got := transition.State.IncludedOutstandingBytes["period-2"]; got != 25 {
		t.Fatalf("next-period outstanding = %d, want 25", got)
	}
}

func TestApplyUsageRejectsWrongOrCrossedPeriodAtomically(t *testing.T) {
	state := mustBalanceState(t, 100, 0)
	before := cloneTestState(state)
	base := ApplyUsageRequest{
		OperationID:      "usage-invalid",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    201,
		IntervalEndUnix:  201,
		FreshThroughUnix: 201,
		Bytes:            10,
		PrimaryActive:    true,
	}
	_, err := ApplyUsage(state, base, nil)
	if !errors.Is(err, ErrPeriodConflict) {
		t.Fatalf("cross-period usage error = %v, want ErrPeriodConflict", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("cross-period rejection mutated state")
	}

	base.OperationID = "usage-wrong-period"
	base.PeriodID = "period-unknown"
	base.AppliedAtUnix = 150
	base.IntervalEndUnix = 150
	base.FreshThroughUnix = 150
	_, err = ApplyUsage(state, base, nil)
	if !errors.Is(err, ErrPeriodConflict) {
		t.Fatalf("wrong-period usage error = %v, want ErrPeriodConflict", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("wrong-period rejection mutated state")
	}
}

func TestApplyUsageAcceptsReverseCrossOriginSamplesAndKeepsFreshnessMaximum(t *testing.T) {
	state := mustBalanceState(t, 0, 100)
	laterSample := ApplyUsageRequest{
		OperationID:      "usage-origin-s4",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-origin-s4",
		IntervalID:       "interval-origin-s4",
		AppliedAtUnix:    180,
		IntervalEndUnix:  180,
		FreshThroughUnix: 180,
		Bytes:            10,
		PrimaryActive:    true,
	}
	later := mustApply(t, state, laterSample)
	earlierSample := ApplyUsageRequest{
		OperationID:      "usage-origin-s2",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-origin-s2",
		IntervalID:       "interval-origin-s2",
		AppliedAtUnix:    181,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            20,
		PrimaryActive:    true,
	}
	earlier := mustApply(t, later.State, earlierSample)
	projection := mustProjection(t, earlier.State)
	if projection.PurchasedRemainingBytes != 70 || projection.LifetimeConsumedBytes != 30 ||
		projection.FreshThroughUnix != 180 {
		t.Fatalf("reverse-origin projection = %#v", projection)
	}
	if len(later.Journal) != 1 || later.Journal[0].MeterEpoch != laterSample.MeterEpoch ||
		later.Journal[0].IntervalID != laterSample.IntervalID || len(earlier.Journal) != 1 ||
		earlier.Journal[0].MeterEpoch != earlierSample.MeterEpoch ||
		earlier.Journal[0].IntervalID != earlierSample.IntervalID {
		t.Fatalf("reverse-origin journals later=%#v earlier=%#v", later.Journal, earlier.Journal)
	}

	beforeReplay := cloneTestState(earlier.State)
	replayed, err := ApplyUsage(earlier.State, earlierSample, &earlier.Record)
	if err != nil {
		t.Fatalf("ApplyUsage replay: %v", err)
	}
	if !replayed.Replayed || len(replayed.Journal) != 0 || !reflect.DeepEqual(replayed.State, beforeReplay) {
		t.Fatalf("reverse-origin replay was not a no-op: %#v", replayed)
	}
}

func TestApplyUsageRejectsFreshnessBeyondBoundPeriodAndKeepsPending(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 20, "access-order-1"),
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 0, "access-order-2"),
	}).State
	state.Projection.Pending = true
	before := cloneTestState(state)

	_, err := ApplyUsage(state, ApplyUsageRequest{
		OperationID:      "usage-cross-freshness",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    250,
		IntervalEndUnix:  200,
		FreshThroughUnix: 250,
		Bytes:            10,
		PrimaryActive:    true,
	}, nil)
	if !errors.Is(err, ErrPeriodConflict) {
		t.Fatalf("cross-period freshness error = %v, want ErrPeriodConflict", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("cross-period freshness rejection mutated state")
	}

	accepted := mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-boundary-pending",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-boundary-pending",
		AppliedAtUnix:    250,
		IntervalEndUnix:  200,
		FreshThroughUnix: 200,
		Bytes:            10,
		PrimaryActive:    true,
	})
	if !mustProjection(t, accepted.State).Pending {
		t.Fatal("ordinary usage transition cleared pending without explicit verified resolution")
	}
}

func TestStateRequiresOutstandingEntryForEveryPeriod(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 0, "access-order-1"),
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 10, "access-order-2"),
	}).State
	delete(state.IncludedOutstandingBytes, "period-2")
	before := cloneTestState(state)

	_, err := AdvanceAt(state, 200)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing outstanding entry error = %v, want ErrInvalid", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("invalid reconstructed state was mutated")
	}
}

func TestIncludedFirstThenPurchasedThenUncovered(t *testing.T) {
	state := mustBalanceState(t, 70, 50)
	transition := mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-1",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            140,
		PrimaryActive:    true,
	})

	want := UsageAllocation{IncludedBytes: 70, PurchasedBytes: 50, UncoveredBytes: 20}
	if transition.Result.Allocation != want {
		t.Fatalf("allocation = %#v, want %#v", transition.Result.Allocation, want)
	}
	projection := mustProjection(t, transition.State)
	if projection.IncludedRemainingBytes != 0 || projection.PurchasedRemainingBytes != 0 || projection.LifetimeConsumedBytes != 140 || projection.UncoveredBytes != 20 {
		t.Fatalf("unexpected projection: %#v", projection)
	}
	entry := findJournalKind(t, transition.Journal, EntryConsumed)
	if entry.PeriodID != "period-1" || entry.IntervalID != "interval-1" || entry.SourceOrderID != "" || entry.IncludedDeltaBytes != -70 || entry.PurchasedDeltaBytes != -50 || entry.ConsumedDeltaBytes != 140 || entry.UncoveredDeltaBytes != 20 {
		t.Fatalf("unexpected consumed deltas: %#v", entry)
	}
}

func TestPositiveUsageFromTrueZeroBalanceIsEntirelyUncovered(t *testing.T) {
	state := mustBalanceState(t, 0, 0)
	transition := mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-zero-balance",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-zero-balance",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            25,
		PrimaryActive:    true,
	})
	projection := mustProjection(t, transition.State)
	if projection.IncludedRemainingBytes != 0 || projection.PurchasedRemainingBytes != 0 || projection.LifetimeConsumedBytes != 25 || projection.UncoveredBytes != 25 {
		t.Fatalf("zero-balance projection = %#v", projection)
	}
	if transition.Result.Allocation != (UsageAllocation{UncoveredBytes: 25}) {
		t.Fatalf("zero-balance allocation = %#v", transition.Result.Allocation)
	}
	if len(transition.Journal) != 1 || transition.Journal[0].Kind != EntryUncovered || transition.Journal[0].IntervalID != "interval-zero-balance" || transition.Journal[0].ConsumedDeltaBytes != 25 || transition.Journal[0].UncoveredDeltaBytes != 25 {
		t.Fatalf("zero-balance journal = %#v", transition.Journal)
	}
}

func TestInactivePrimaryFreezesBalancesAndRecordsAllUsageUncovered(t *testing.T) {
	state := mustBalanceState(t, 30, 20)
	transition := mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-frozen",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            25,
		PrimaryActive:    false,
	})

	projection := mustProjection(t, transition.State)
	if projection.IncludedRemainingBytes != 30 || projection.PurchasedRemainingBytes != 20 {
		t.Fatalf("inactive primary spent frozen balance: %#v", projection)
	}
	if projection.LifetimeConsumedBytes != 25 || projection.UncoveredBytes != 25 {
		t.Fatalf("inactive usage accounting = %#v", projection)
	}
	if transition.Result.Allocation != (UsageAllocation{UncoveredBytes: 25}) {
		t.Fatalf("inactive allocation = %#v", transition.Result.Allocation)
	}
	if len(transition.Journal) != 1 || transition.Journal[0].Kind != EntryUncovered || transition.Journal[0].PeriodID != "period-1" || transition.Journal[0].IntervalID != "interval-1" || transition.Journal[0].SourceOrderID != "" || transition.Journal[0].IncludedDeltaBytes != 0 || transition.Journal[0].PurchasedDeltaBytes != 0 || transition.Journal[0].ConsumedDeltaBytes != 25 || transition.Journal[0].UncoveredDeltaBytes != 25 {
		t.Fatalf("inactive usage journal = %#v", transition.Journal)
	}
	if got := transition.State.IncludedOutstandingBytes["period-1"]; got != 30 {
		t.Fatalf("inactive usage changed included outstanding to %d, want 30", got)
	}
}

func TestZeroUsageCreatesNoIllegalJournalEntry(t *testing.T) {
	state := mustBalanceState(t, 30, 20)
	versionBefore := mustProjection(t, state).Version
	transition := mustApply(t, state, ApplyUsageRequest{
		OperationID:      "usage-zero",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-zero",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            0,
		PrimaryActive:    true,
	})
	if len(transition.Journal) != 0 {
		t.Fatalf("zero usage created illegal journal intent: %#v", transition.Journal)
	}
	projection := mustProjection(t, transition.State)
	if projection.IncludedRemainingBytes != 30 || projection.PurchasedRemainingBytes != 20 || projection.LifetimeConsumedBytes != 0 || projection.UncoveredBytes != 0 {
		t.Fatalf("zero usage changed balances: %#v", projection)
	}
	if projection.FreshThroughUnix != 150 {
		t.Fatalf("zero usage did not advance freshness: %#v", projection)
	}
	if projection.Version != versionBefore+1 {
		t.Fatalf("zero usage freshness version = %d, want %d", projection.Version, versionBefore+1)
	}
	replayed, err := ApplyUsage(transition.State, ApplyUsageRequest{
		OperationID:      "usage-zero",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-zero",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            0,
		PrimaryActive:    true,
	}, &transition.Record)
	if err != nil {
		t.Fatalf("zero usage replay: %v", err)
	}
	if !replayed.Replayed || len(replayed.Journal) != 0 || mustProjection(t, replayed.State).Version != projection.Version {
		t.Fatalf("zero usage replay was not a no-op: %#v", replayed)
	}
}

func TestReplayReturnsStoredResultWithoutRevertingNewerState(t *testing.T) {
	state := mustNewState(t)
	req := SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 10, "access-order-1"),
	}
	first := mustSchedule(t, state, req)
	newer := mustSchedule(t, first.State, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 0, "access-order-2"),
	}).State
	before := cloneTestState(newer)

	replay, err := SchedulePeriod(newer, req, &first.Record)
	if err != nil {
		t.Fatalf("SchedulePeriod replay: %v", err)
	}
	if !replay.Replayed || len(replay.Journal) != 0 {
		t.Fatalf("unexpected replay flags/journal: %#v", replay)
	}
	if !reflect.DeepEqual(replay.State, before) {
		t.Fatalf("replay reverted newer state: got %#v want %#v", replay.State, before)
	}
	if !reflect.DeepEqual(replay.Result, first.Record.Result) {
		t.Fatalf("replay result = %#v, want stored %#v", replay.Result, first.Record.Result)
	}
}

func TestSameOperationChangedCanonicalFieldConflicts(t *testing.T) {
	state := mustNewState(t)
	req := SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 10, "access-order-1"),
	}
	first := mustSchedule(t, state, req)
	tests := []struct {
		name   string
		mutate func(*SchedulePeriodRequest)
	}{
		{name: "operation id", mutate: func(value *SchedulePeriodRequest) { value.OperationID = "schedule-other" }},
		{name: "now", mutate: func(value *SchedulePeriodRequest) { value.NowUnix++ }},
		{name: "period id", mutate: func(value *SchedulePeriodRequest) { value.Period.ID = "period-other" }},
		{name: "ordinal", mutate: func(value *SchedulePeriodRequest) { value.Period.Ordinal++ }},
		{name: "start", mutate: func(value *SchedulePeriodRequest) { value.Period.StartsAtUnix++ }},
		{name: "end", mutate: func(value *SchedulePeriodRequest) { value.Period.EndsAtUnix++ }},
		{name: "grant", mutate: func(value *SchedulePeriodRequest) { value.Period.IncludedGrantBytes++ }},
		{name: "access order", mutate: func(value *SchedulePeriodRequest) { value.Period.AccessOrderID = "access-order-other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := cloneTestState(first.State)
			changed := req
			test.mutate(&changed)
			_, err := SchedulePeriod(first.State, changed, &first.Record)
			if !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("changed %s error = %v, want ErrOperationConflict", test.name, err)
			}
			if !reflect.DeepEqual(first.State, before) {
				t.Fatalf("changed %s mutated state", test.name)
			}
		})
	}

	other, err := NewState("wl-ent-2")
	if err != nil {
		t.Fatalf("NewState other entitlement: %v", err)
	}
	otherBefore := cloneTestState(other)
	_, err = SchedulePeriod(other, req, &first.Record)
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("cross-entitlement replay error = %v, want ErrOperationConflict", err)
	}
	if !reflect.DeepEqual(other, otherBefore) {
		t.Fatal("cross-entitlement replay mutated state")
	}
}

func TestCreditReplayBindsEveryCanonicalFieldAndCommandKind(t *testing.T) {
	state := mustBalanceState(t, 0, 0)
	req := CreditPurchasedRequest{
		OperationID:   "credit-canonical",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-1",
		NowUnix:       150,
		Bytes:         10,
		PrimaryActive: true,
	}
	first := mustCredit(t, state, req)

	tests := []struct {
		name   string
		mutate func(*CreditPurchasedRequest)
	}{
		{name: "period", mutate: func(value *CreditPurchasedRequest) { value.PeriodID = "period-other" }},
		{name: "source order", mutate: func(value *CreditPurchasedRequest) { value.SourceOrderID = "topup-order-2" }},
		{name: "time", mutate: func(value *CreditPurchasedRequest) { value.NowUnix++ }},
		{name: "bytes", mutate: func(value *CreditPurchasedRequest) { value.Bytes++ }},
		{name: "primary decision", mutate: func(value *CreditPurchasedRequest) { value.PrimaryActive = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := cloneTestState(first.State)
			changed := req
			test.mutate(&changed)
			_, err := CreditPurchased(first.State, changed, &first.Record)
			if !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("changed %s error = %v, want ErrOperationConflict", test.name, err)
			}
			if !reflect.DeepEqual(first.State, before) {
				t.Fatalf("changed %s mutated state", test.name)
			}
		})
	}

	beforeCrossCommand := cloneTestState(first.State)
	_, err := ApplyUsage(first.State, ApplyUsageRequest{
		OperationID:      req.OperationID,
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            1,
		PrimaryActive:    true,
	}, &first.Record)
	if !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("cross-command replay error = %v, want ErrOperationConflict", err)
	}
	if !reflect.DeepEqual(first.State, beforeCrossCommand) {
		t.Fatal("cross-command replay mutated state")
	}
}

func TestUsageReplayBindsEveryCanonicalFieldAndSurvivesRollover(t *testing.T) {
	state := mustNewState(t)
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 50, "access-order-1"),
	}).State
	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 0, "access-order-2"),
	}).State
	req := ApplyUsageRequest{
		OperationID:      "usage-canonical",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-1",
		IntervalID:       "interval-1",
		AppliedAtUnix:    200,
		IntervalEndUnix:  200,
		FreshThroughUnix: 200,
		Bytes:            10,
		PrimaryActive:    true,
	}
	first := mustApply(t, state, req)
	if mustProjection(t, first.State).CurrentPeriodID != "period-2" {
		t.Fatal("test precondition: usage did not roll to period-2")
	}
	newerBefore := cloneTestState(first.State)

	replayed, err := ApplyUsage(first.State, req, &first.Record)
	if err != nil {
		t.Fatalf("ApplyUsage replay: %v", err)
	}
	if !replayed.Replayed || len(replayed.Journal) != 0 || !reflect.DeepEqual(replayed.State, newerBefore) {
		t.Fatalf("usage replay after rollover was not a no-op: %#v", replayed)
	}

	tests := []struct {
		name   string
		mutate func(*ApplyUsageRequest)
	}{
		{name: "period", mutate: func(value *ApplyUsageRequest) { value.PeriodID = "period-2" }},
		{name: "meter epoch", mutate: func(value *ApplyUsageRequest) { value.MeterEpoch = "epoch-2" }},
		{name: "interval", mutate: func(value *ApplyUsageRequest) { value.IntervalID = "interval-2" }},
		{name: "applied time", mutate: func(value *ApplyUsageRequest) { value.AppliedAtUnix++ }},
		{name: "interval end", mutate: func(value *ApplyUsageRequest) { value.IntervalEndUnix-- }},
		{name: "fresh through", mutate: func(value *ApplyUsageRequest) { value.FreshThroughUnix-- }},
		{name: "bytes", mutate: func(value *ApplyUsageRequest) { value.Bytes++ }},
		{name: "primary decision", mutate: func(value *ApplyUsageRequest) { value.PrimaryActive = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := cloneTestState(first.State)
			changed := req
			test.mutate(&changed)
			_, err := ApplyUsage(first.State, changed, &first.Record)
			if !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("changed %s error = %v, want ErrOperationConflict", test.name, err)
			}
			if !reflect.DeepEqual(first.State, before) {
				t.Fatalf("changed %s mutated state", test.name)
			}
		})
	}
}

func TestOverflowCasesLeaveInputUnchanged(t *testing.T) {
	state := mustNewState(t)
	before := cloneTestState(state)
	_, err := SchedulePeriod(state, SchedulePeriodRequest{
		OperationID: "schedule-too-large",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, MaxExclusive, "access-order-1"),
	}, nil)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("max-exclusive grant error = %v, want ErrOverflow", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("rejected grant mutated state")
	}

	state = mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 0, "access-order-1"),
	}).State
	state = mustCredit(t, state, CreditPurchasedRequest{
		OperationID:   "credit-near-max",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-1",
		NowUnix:       120,
		Bytes:         MaxExclusive - 1,
		PrimaryActive: true,
	}).State
	before = cloneTestState(state)
	_, err = CreditPurchased(state, CreditPurchasedRequest{
		OperationID:   "credit-overflow",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-2",
		NowUnix:       130,
		Bytes:         1,
		PrimaryActive: true,
	}, nil)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("checked-add credit error = %v, want ErrOverflow", err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("overflowing credit mutated state")
	}

	queued := mustNewState(t)
	queued = mustSchedule(t, queued, SchedulePeriodRequest{
		OperationID: "schedule-current",
		NowUnix:     100,
		Period:      testPeriod("period-current", 1, 100, 200, 0, "access-order-current"),
	}).State
	queued = mustSchedule(t, queued, SchedulePeriodRequest{
		OperationID: "schedule-future",
		NowUnix:     120,
		Period:      testPeriod("period-future", 2, 200, 300, 10, "access-order-future"),
	}).State
	queuedBefore := cloneTestState(queued)
	_, err = CreditPurchased(queued, CreditPurchasedRequest{
		OperationID:   "credit-future-overflow",
		PeriodID:      "period-current",
		SourceOrderID: "topup-order-future-overflow",
		NowUnix:       150,
		Bytes:         MaxExclusive - 5,
		PrimaryActive: true,
	}, nil)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("future-grant credit error = %v, want ErrOverflow", err)
	}
	if !reflect.DeepEqual(queued, queuedBefore) {
		t.Fatal("future-grant overflow mutated state")
	}

	lifetime := mustBalanceState(t, 0, 0)
	lifetime.Projection.LifetimeConsumedBytes = MaxExclusive - 1
	lifetimeBefore := cloneTestState(lifetime)
	_, err = ApplyUsage(lifetime, ApplyUsageRequest{
		OperationID:      "usage-lifetime-overflow",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-lifetime",
		IntervalID:       "interval-lifetime",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            1,
		PrimaryActive:    true,
	}, nil)
	if !errors.Is(err, ErrOverflow) || !reflect.DeepEqual(lifetime, lifetimeBefore) {
		t.Fatalf("lifetime overflow was not atomic: err=%v state=%#v", err, lifetime)
	}

	uncovered := mustBalanceState(t, 0, 0)
	uncovered.Projection.UncoveredBytes = MaxExclusive - 1
	uncoveredBefore := cloneTestState(uncovered)
	_, err = ApplyUsage(uncovered, ApplyUsageRequest{
		OperationID:      "usage-uncovered-overflow",
		PeriodID:         "period-1",
		MeterEpoch:       "epoch-uncovered",
		IntervalID:       "interval-uncovered",
		AppliedAtUnix:    150,
		IntervalEndUnix:  150,
		FreshThroughUnix: 150,
		Bytes:            1,
		PrimaryActive:    false,
	}, nil)
	if !errors.Is(err, ErrOverflow) || !reflect.DeepEqual(uncovered, uncoveredBefore) {
		t.Fatalf("uncovered overflow was not atomic: err=%v state=%#v", err, uncovered)
	}

	versioned := mustBalanceState(t, 0, 0)
	versioned.Projection.Version = MaxExclusive - 1
	versionedBefore := cloneTestState(versioned)
	_, err = SchedulePeriod(versioned, SchedulePeriodRequest{
		OperationID: "schedule-version-overflow",
		NowUnix:     150,
		Period:      testPeriod("period-2", 2, 200, 300, 0, "access-order-2"),
	}, nil)
	if !errors.Is(err, ErrOverflow) || !reflect.DeepEqual(versioned, versionedBefore) {
		t.Fatalf("version overflow was not atomic: err=%v state=%#v", err, versioned)
	}
}

func TestStateAndTransitionDoNotAliasSlicesOrOutstandingMap(t *testing.T) {
	state := mustNewState(t)
	first := mustSchedule(t, state, SchedulePeriodRequest{
		OperationID: "schedule-1",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, 10, "access-order-1"),
	})
	originalPeriod := first.State.Periods[0]
	originalOutstanding := first.State.IncludedOutstandingBytes["period-1"]
	originalProjection := *first.State.Projection

	second := mustSchedule(t, first.State, SchedulePeriodRequest{
		OperationID: "schedule-2",
		NowUnix:     120,
		Period:      testPeriod("period-2", 2, 200, 300, 20, "access-order-2"),
	})
	second.State.Periods[0].ID = "mutated"
	second.State.IncludedOutstandingBytes["period-1"] = 999
	second.State.Projection.CurrentPeriodID = "mutated"
	second.Journal = append(second.Journal, JournalIntent{Kind: EntryUncovered})

	if first.State.Periods[0] != originalPeriod {
		t.Fatalf("transition aliased periods: got %#v want %#v", first.State.Periods[0], originalPeriod)
	}
	if got := first.State.IncludedOutstandingBytes["period-1"]; got != originalOutstanding {
		t.Fatalf("transition aliased outstanding map: got %d want %d", got, originalOutstanding)
	}
	if *first.State.Projection != originalProjection {
		t.Fatalf("transition aliased projection: got %#v want %#v", *first.State.Projection, originalProjection)
	}
}

func mustNewState(t *testing.T) State {
	t.Helper()
	state, err := NewState("wl-ent-1")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return state
}

func testPeriod(id string, ordinal, startsAt, endsAt, included int64, orderID string) Period {
	return Period{
		ID:                 id,
		Ordinal:            ordinal,
		StartsAtUnix:       startsAt,
		EndsAtUnix:         endsAt,
		IncludedGrantBytes: included,
		AccessOrderID:      orderID,
	}
}

func mustSchedule(t *testing.T, state State, req SchedulePeriodRequest) Transition {
	t.Helper()
	transition, err := SchedulePeriod(state, req, nil)
	if err != nil {
		t.Fatalf("SchedulePeriod: %v", err)
	}
	return transition
}

func mustCredit(t *testing.T, state State, req CreditPurchasedRequest) Transition {
	t.Helper()
	transition, err := CreditPurchased(state, req, nil)
	if err != nil {
		t.Fatalf("CreditPurchased: %v", err)
	}
	return transition
}

func mustApply(t *testing.T, state State, req ApplyUsageRequest) Transition {
	t.Helper()
	transition, err := ApplyUsage(state, req, nil)
	if err != nil {
		t.Fatalf("ApplyUsage: %v", err)
	}
	return transition
}

func mustBalanceState(t *testing.T, included, purchased int64) State {
	t.Helper()
	state := mustSchedule(t, mustNewState(t), SchedulePeriodRequest{
		OperationID: "schedule-balance",
		NowUnix:     100,
		Period:      testPeriod("period-1", 1, 100, 200, included, "access-order-1"),
	}).State
	if purchased == 0 {
		return state
	}
	return mustCredit(t, state, CreditPurchasedRequest{
		OperationID:   "credit-balance",
		PeriodID:      "period-1",
		SourceOrderID: "topup-order-1",
		NowUnix:       120,
		Bytes:         purchased,
		PrimaryActive: true,
	}).State
}

func mustProjection(t *testing.T, state State) BalanceProjection {
	t.Helper()
	if state.Projection == nil {
		t.Fatal("state projection is nil")
	}
	return *state.Projection
}

func findJournalKind(t *testing.T, journal []JournalIntent, kind EntryKind) JournalIntent {
	t.Helper()
	for _, entry := range journal {
		if entry.Kind == kind {
			return entry
		}
	}
	t.Fatalf("journal kind %q not found in %#v", kind, journal)
	return JournalIntent{}
}

func cloneTestState(state State) State {
	cloned := state
	cloned.Periods = append([]Period(nil), state.Periods...)
	if state.Projection != nil {
		projection := *state.Projection
		cloned.Projection = &projection
	}
	cloned.IncludedOutstandingBytes = make(map[string]int64, len(state.IncludedOutstandingBytes))
	for periodID, value := range state.IncludedOutstandingBytes {
		cloned.IncludedOutstandingBytes[periodID] = value
	}
	return cloned
}
