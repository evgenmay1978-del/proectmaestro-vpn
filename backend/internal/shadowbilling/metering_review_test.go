package shadowbilling

import (
	"math"
	"reflect"
	"testing"
)

// This catches a production change that wraps a two-direction Xray delta and
// silently undercharges it; the error must leave the input state untouched.
func TestApplyRejectsUnrepresentableTwoDirectionDeltaAtomically(t *testing.T) {
	p := paidPolicy()
	state, _, err := Apply(NewState(), event(p, "baseline", "epoch", 0, 0), p)
	if err != nil {
		t.Fatal(err)
	}
	before := state.clone()
	got, _, err := Apply(state, event(p, "overflow", "epoch", math.MaxUint64, 1), p)
	if err == nil || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(state, before) {
		t.Fatalf("overflow result=%#v err=%v", got, err)
	}
}

// This catches a production change that wraps an entitlement-period total when
// independent meters contribute more than uint64 bytes in aggregate.
func TestApplyRejectsUnrepresentablePeriodTotalAtomically(t *testing.T) {
	p := paidPolicy()
	p.Basis = BasisDownlinkOnly
	p.IncludedBytes = 0
	state, _, err := Apply(NewState(), event(p, "a0", "epoch-a", 0, 0), p)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Apply(state, event(p, "a1", "epoch-a", 0, math.MaxUint64), p)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Apply(state, UsageEvent{EventID: "b0", InstanceID: "s2", MeterEpoch: "epoch-b", XrayIdentity: "wl:" + p.EntitlementID(), UplinkBytes: 0, DownlinkBytes: 0}, p)
	if err != nil {
		t.Fatal(err)
	}
	before := state.clone()
	got, _, err := Apply(state, UsageEvent{EventID: "b1", InstanceID: "s2", MeterEpoch: "epoch-b", XrayIdentity: "wl:" + p.EntitlementID(), UplinkBytes: 0, DownlinkBytes: 1}, p)
	if err == nil || !reflect.DeepEqual(got, before) {
		t.Fatalf("period overflow result=%#v err=%v", got, err)
	}
}

// This catches a production change that lets hard+grace wrap and suspends an
// entitlement below the representable effective threshold.
func TestApplyComparesHardLimitAndGraceWithoutOverflow(t *testing.T) {
	p := paidPolicy()
	p.Basis = BasisDownlinkOnly
	p.HardLimitBytes = math.MaxUint64
	p.GraceBytes = 1
	state, _, err := Apply(NewState(), event(p, "base", "epoch", 0, 0), p)
	if err != nil {
		t.Fatal(err)
	}
	_, d, err := Apply(state, event(p, "one", "epoch", 0, 1), p)
	if err != nil || d.Suspension.Recommended {
		t.Fatalf("hard+grace result=%#v err=%v", d, err)
	}
}

// This catches a production change that joins arbitrary opaque tuple members
// with a delimiter and aliases distinct meter or billing-period scopes.
func TestApplyKeepsEmbeddedNULTuplesDistinct(t *testing.T) {
	p := paidPolicy()
	p.Basis = BasisDownlinkOnly
	p.IncludedBytes = 100
	state := NewState()
	first := UsageEvent{EventID: "m1", InstanceID: "a\x00b", MeterEpoch: "c", XrayIdentity: "wl:" + p.EntitlementID()}
	second := UsageEvent{EventID: "m2", InstanceID: "a", MeterEpoch: "b\x00c", XrayIdentity: "wl:" + p.EntitlementID()}
	var d Decision
	var err error
	state, d, err = Apply(state, first, p)
	if err != nil || d.Interval != nil {
		t.Fatalf("first meter=%#v %v", d, err)
	}
	_, d, err = Apply(state, second, p)
	if err != nil || d.Interval != nil {
		t.Fatalf("NUL meter alias=%#v %v", d, err)
	}
	state, _, _ = Apply(NewState(), event(p, "p0", "epoch-p", 0, 0), p)
	state, _, err = Apply(state, event(p, "p1", "epoch-p", 0, 100), p)
	if err != nil {
		t.Fatal(err)
	}
	before := state.clone()
	p2 := p
	p2.billingPeriodID = "period\x001"
	got, d, err := Apply(state, event(p2, "q0", "epoch-q", 0, 0), p2)
	if err == nil || !reflect.DeepEqual(got, before) || d != (Decision{}) {
		t.Fatalf("NUL policy binding result=%#v decision=%#v err=%v", got, d, err)
	}
}
