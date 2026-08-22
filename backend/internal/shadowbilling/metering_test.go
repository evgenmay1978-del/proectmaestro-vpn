package shadowbilling

import "testing"

func paidPolicy() Policy {
	return Policy{
		AccountID: "account-a", EntitlementID: "cdn-a", TransportID: "profile-a", BillingPeriodID: "period-1",
		Unit: UnitGBDecimal, Basis: BasisUplinkPlusDownlink, IncludedBytes: 100,
		SoftLimitBytes: 150, HardLimitBytes: 200, GraceBytes: 10,
		Prices: PriceOptions{Global: &Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 25000}},
	}
}

func event(id, epoch string, up, down uint64) UsageEvent {
	return UsageEvent{EventID: id, InstanceID: "s2-xray-a", MeterEpoch: epoch, XrayIdentity: "wl:opaque-a", UplinkBytes: up, DownlinkBytes: down}
}

// This catches a production change that charges a baseline, a replay, or the
// pre-reset counter range twice.
func TestApplyUsesPositiveCumulativeDeltasAndDeduplicatesEvents(t *testing.T) {
	state := NewState()
	policy := paidPolicy()
	var decision Decision
	var err error
	state, decision, err = Apply(state, event("event-1", "epoch-1", 10, 90), policy)
	if err != nil || decision.Interval != nil || decision.Diagnostic != DiagnosticEpochStarted {
		t.Fatalf("first sample = %#v, %v; want baseline epoch start", decision, err)
	}
	state, decision, err = Apply(state, event("event-2", "epoch-1", 30, 180), policy)
	if err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if decision.Interval == nil || decision.Interval.UplinkBytes != 20 || decision.Interval.DownlinkBytes != 90 || decision.Interval.BillableBytes != 10 {
		t.Fatalf("second interval = %#v; want uplink=20 downlink=90 billable=10", decision.Interval)
	}
	if decision.Ledger == nil || decision.Ledger.CalculatedAmount.Numerator != "1" || decision.Ledger.CalculatedAmount.Denominator != 4000 {
		t.Fatalf("second ledger = %#v; want exact 1/4000 RUB minor units", decision.Ledger)
	}
	state, decision, err = Apply(state, event("event-2", "epoch-1", 30, 180), policy)
	if err != nil || !decision.Replay || decision.Interval != nil || len(state.LedgerEntries()) != 1 {
		t.Fatalf("replay = %#v, entries=%d, err=%v; want no duplicate", decision, len(state.LedgerEntries()), err)
	}
	state, decision, err = Apply(state, event("event-3", "epoch-1", 5, 8), policy)
	if err != nil || decision.Interval != nil || decision.Diagnostic != DiagnosticCounterReset || len(state.LedgerEntries()) != 1 {
		t.Fatalf("counter reset = %#v, entries=%d, err=%v; want safe rebaseline", decision, len(state.LedgerEntries()), err)
	}
	state, decision, err = Apply(state, event("event-4", "epoch-2", 1, 2), policy)
	if err != nil || decision.Interval != nil || decision.Diagnostic != DiagnosticEpochStarted || len(state.LedgerEntries()) != 1 {
		t.Fatalf("new epoch = %#v, entries=%d, err=%v; want safe baseline", decision, len(state.LedgerEntries()), err)
	}
}

// This catches a production change that reapplies included bytes after a
// restart-like state handoff, or that suspends anything beyond the entitlement.
func TestApplyConsumesIncludedOnceAndOnlyRecommendsWhiteListSuspension(t *testing.T) {
	state := NewState()
	policy := paidPolicy()
	var decision Decision
	var err error
	state, _, err = Apply(state, event("event-1", "epoch-1", 0, 0), policy)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	state, decision, err = Apply(state, event("event-2", "epoch-1", 0, 160), policy)
	if err != nil || decision.Interval == nil || decision.Interval.BillableBytes != 60 || !decision.SoftLimitReached || decision.Suspension.Recommended {
		t.Fatalf("included/soft interval = %#v, decision=%#v, err=%v", decision.Interval, decision, err)
	}
	state, decision, err = Apply(state, event("event-3", "epoch-1", 0, 220), policy)
	if err != nil || decision.Interval == nil || decision.Interval.BillableBytes != 60 || !decision.Suspension.Recommended || decision.Suspension.EntitlementID != "cdn-a" || decision.Suspension.Reason != SuspensionHardLimit {
		t.Fatalf("hard-limit interval = %#v, decision=%#v, err=%v", decision.Interval, decision, err)
	}
	if !state.WhiteListSuspended("cdn-a") || len(state.LedgerEntries()) != 2 {
		t.Fatalf("suspension state=%t ledger=%d; want entitlement-only suspension with immutable history", state.WhiteListSuspended("cdn-a"), len(state.LedgerEntries()))
	}
}

// This catches a production change that selects a lower-precedence price or
// silently interprets an absent paid price as free.
func TestResolvePriceUsesDocumentedPrecedenceAndRequiresExplicitFreeMode(t *testing.T) {
	individual := Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 900}
	tariff := Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 800}
	profile := Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 700}
	global := Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 600}
	resolved, err := ResolvePrice(PriceOptions{Individual: &individual, Tariff: &tariff, Profile: &profile, Global: &global})
	if err != nil || resolved.Source != PriceIndividual || resolved.Price.MinorUnitsPerUnit != 900 {
		t.Fatalf("resolved = %#v, %v; want individual 900", resolved, err)
	}
	if _, err := ResolvePrice(PriceOptions{}); err != ErrMissingPaidPrice {
		t.Fatalf("missing paid price error = %v, want %v", err, ErrMissingPaidPrice)
	}
	free, err := ResolvePrice(PriceOptions{Global: &Price{Mode: PriceFree}})
	if err != nil || free.Source != PriceGlobal || free.Price.Mode != PriceFree {
		t.Fatalf("explicit free = %#v, %v", free, err)
	}
}

// This catches a production change that rewrites an old interval when pricing
// changes. Exact amount fields are decimal/integer rational values, never float.
func TestApplySnapshotsTariffPerInterval(t *testing.T) {
	state := NewState()
	policy := paidPolicy()
	policy.IncludedBytes = 0
	policy.HardLimitBytes = 0
	var err error
	state, _, err = Apply(state, event("event-1", "epoch-1", 0, 0), policy)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	state, _, err = Apply(state, event("event-2", "epoch-1", 0, 1000), policy)
	if err != nil {
		t.Fatalf("first interval: %v", err)
	}
	policy.Prices.Global = &Price{Mode: PricePaid, Currency: "RUB", MinorUnitsPerUnit: 40000}
	state, _, err = Apply(state, event("event-3", "epoch-1", 0, 2000), policy)
	if err != nil {
		t.Fatalf("second interval: %v", err)
	}
	entries := state.LedgerEntries()
	if len(entries) != 2 || entries[0].Snapshot.Price.Price.MinorUnitsPerUnit != 25000 || entries[1].Snapshot.Price.Price.MinorUnitsPerUnit != 40000 {
		t.Fatalf("snapshots = %#v; want 25000 then 40000", entries)
	}
}
