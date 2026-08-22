package shadowbilling

import (
	"errors"
	"reflect"
	"testing"
)

func orderedEvent(policy Policy, id string, generation, sequence, down uint64) OrderedUsageEvent {
	return OrderedUsageEvent{
		UsageEvent:        event(policy, id, "epoch-ordered", 0, down),
		CounterGeneration: generation,
		SampleSequence:    sequence,
	}
}

func TestApplyOrderedIgnoresLateSampleWithoutRebasingCounter(t *testing.T) {
	policy := paidPolicy()
	policy.Basis = BasisDownlinkOnly
	policy.IncludedBytes = 0
	policy.SoftLimitBytes = 0
	policy.HardLimitBytes = 0
	policy.GraceBytes = 0

	state, decision, err := ApplyOrdered(NewState(), orderedEvent(policy, "baseline", 1, 1, 0), policy)
	if err != nil || decision.Diagnostic != DiagnosticEpochStarted || decision.Interval != nil {
		t.Fatalf("baseline=%#v err=%v", decision, err)
	}
	state, decision, err = ApplyOrdered(state, orderedEvent(policy, "accepted-100", 1, 3, 100), policy)
	if err != nil || decision.Interval == nil || decision.Interval.BillableBytes != 100 {
		t.Fatalf("accepted 100=%#v err=%v", decision, err)
	}

	beforeLate := state.clone()
	state, decision, err = ApplyOrdered(state, orderedEvent(policy, "late-50", 1, 2, 50), policy)
	if err != nil || decision.Diagnostic != DiagnosticLateSample || decision.Interval != nil || decision.Ledger != nil {
		t.Fatalf("late 50=%#v err=%v", decision, err)
	}
	if !reflect.DeepEqual(state, beforeLate) || len(state.LedgerEntries()) != 1 {
		t.Fatalf("late sample mutated state: before=%#v after=%#v", beforeLate, state)
	}

	state, decision, err = ApplyOrdered(state, orderedEvent(policy, "fresh-110", 1, 4, 110), policy)
	if err != nil || decision.Interval == nil || decision.Interval.DownlinkBytes != 10 || decision.Interval.BillableBytes != 10 || len(state.LedgerEntries()) != 2 {
		t.Fatalf("fresh 110=%#v ledger=%d err=%v; want only 10 billed", decision, len(state.LedgerEntries()), err)
	}
	state, decision, err = ApplyOrdered(state, orderedEvent(policy, "fresh-110", 1, 4, 110), policy)
	if err != nil || !decision.Replay || decision.Interval != nil || len(state.LedgerEntries()) != 2 {
		t.Fatalf("ordered replay=%#v ledger=%d err=%v", decision, len(state.LedgerEntries()), err)
	}
}

func TestApplyOrderedRequiresExplicitGenerationForCounterReset(t *testing.T) {
	policy := paidPolicy()
	policy.Basis = BasisDownlinkOnly
	policy.IncludedBytes = 0
	policy.SoftLimitBytes = 0
	policy.HardLimitBytes = 0
	policy.GraceBytes = 0

	state, _, err := ApplyOrdered(NewState(), orderedEvent(policy, "baseline", 1, 1, 0), policy)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	state, _, err = ApplyOrdered(state, orderedEvent(policy, "accepted-100", 1, 2, 100), policy)
	if err != nil {
		t.Fatalf("accepted 100: %v", err)
	}
	beforeRegression := state.clone()
	got, decision, err := ApplyOrdered(state, orderedEvent(policy, "unmarked-reset", 1, 3, 50), policy)
	if !errors.Is(err, ErrResetGenerationRequired) || decision != (Decision{}) || !reflect.DeepEqual(got, beforeRegression) {
		t.Fatalf("unmarked reset: state=%#v decision=%#v err=%v", got, decision, err)
	}

	state, decision, err = ApplyOrdered(state, orderedEvent(policy, "marked-reset", 2, 1, 50), policy)
	if err != nil || decision.Diagnostic != DiagnosticCounterReset || decision.Interval != nil || len(state.LedgerEntries()) != 1 {
		t.Fatalf("marked reset=%#v ledger=%d err=%v", decision, len(state.LedgerEntries()), err)
	}
	state, decision, err = ApplyOrdered(state, orderedEvent(policy, "post-reset-60", 2, 2, 60), policy)
	if err != nil || decision.Interval == nil || decision.Interval.BillableBytes != 10 || len(state.LedgerEntries()) != 2 {
		t.Fatalf("post reset=%#v ledger=%d err=%v", decision, len(state.LedgerEntries()), err)
	}
}

func requireEventIDConflict(t *testing.T, before State, eventID string, apply func(State) (State, Decision, error)) {
	t.Helper()
	snapshot := before.clone()
	got, decision, err := apply(before)
	var conflict *EventIDConflictError
	if !errors.Is(err, ErrEventIDConflict) || !errors.As(err, &conflict) || conflict.EventID != eventID {
		t.Fatalf("conflict error=%T %v; want typed conflict for %q", err, err, eventID)
	}
	if decision != (Decision{}) || !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("conflict mutated state: before=%#v after=%#v decision=%#v", snapshot, got, decision)
	}
}

func TestApplyEventIDBindsCanonicalPayloadAndContext(t *testing.T) {
	policy := paidPolicy()
	policy.Basis = BasisDownlinkOnly
	base := orderedEvent(policy, "bound-event", 1, 1, 100)
	state, decision, err := ApplyOrdered(NewState(), base, policy)
	if err != nil || decision.Replay || decision.Diagnostic != DiagnosticEpochStarted {
		t.Fatalf("initial event=%#v err=%v", decision, err)
	}
	state, decision, err = ApplyOrdered(state, base, policy)
	if err != nil || !decision.Replay || decision.Interval != nil || decision.Ledger != nil {
		t.Fatalf("identical retry=%#v err=%v", decision, err)
	}

	changedCounter := base
	changedCounter.DownlinkBytes++
	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, changedCounter, policy)
	})

	changedOrder := base
	changedOrder.SampleSequence++
	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, changedOrder, policy)
	})

	changedBasis := policy
	changedBasis.Basis = BasisUplinkPlusDownlink
	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, base, changedBasis)
	})

	changedIncluded := policy
	changedIncluded.IncludedBytes++
	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, base, changedIncluded)
	})

	changedPrice := policy
	price := *policy.Prices.Global
	price.MinorUnitsPerUnit++
	changedPrice.Prices.Global = &price
	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, base, changedPrice)
	})

	otherPolicy := paidPolicy()
	otherPolicy.Basis = BasisDownlinkOnly
	otherEvent := orderedEvent(otherPolicy, base.EventID, base.CounterGeneration, base.SampleSequence, base.DownlinkBytes)
	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, otherEvent, otherPolicy)
	})

	requireEventIDConflict(t, state, base.EventID, func(input State) (State, Decision, error) {
		return Apply(input, base.UsageEvent, policy)
	})

	legacyEvent := event(policy, "legacy-bound-event", "epoch-ordered", 0, 100)
	legacyState, legacyDecision, err := Apply(NewState(), legacyEvent, policy)
	if err != nil || legacyDecision.Replay || legacyDecision.Diagnostic != DiagnosticEpochStarted {
		t.Fatalf("initial legacy event=%#v err=%v", legacyDecision, err)
	}
	orderedAfterLegacy := OrderedUsageEvent{UsageEvent: legacyEvent, CounterGeneration: 1, SampleSequence: 1}
	requireEventIDConflict(t, legacyState, legacyEvent.EventID, func(input State) (State, Decision, error) {
		return ApplyOrdered(input, orderedAfterLegacy, policy)
	})
}
