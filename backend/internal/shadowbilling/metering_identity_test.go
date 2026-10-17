package shadowbilling

import (
	"errors"
	"reflect"
	"testing"
)

func TestApplyRejectsUnexpectedXrayIdentityWithoutStateMutation(t *testing.T) {
	policy := paidPolicy()
	state := NewState()
	bad := event(policy, "same-event", "epoch-1", 10, 90)
	bad.XrayIdentity = "ordinary:existing-vpn"

	got, decision, err := Apply(state, bad, policy)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("mismatched identity error=%v, want %v", err, ErrIdentityMismatch)
	}
	if !reflect.DeepEqual(got, state) || decision != (Decision{}) {
		t.Fatalf("mismatched identity mutated state or decision: state=%#v decision=%#v", got, decision)
	}

	next, decision, err := Apply(got, event(policy, "same-event", "epoch-1", 10, 90), policy)
	if err != nil || decision.Replay || decision.Diagnostic != DiagnosticEpochStarted || len(next.LedgerEntries()) != 0 {
		t.Fatalf("rejected event contaminated valid baseline: decision=%#v ledger=%d err=%v", decision, len(next.LedgerEntries()), err)
	}
}

func TestApplyRequiresPolicyIdentityBinding(t *testing.T) {
	policy := Policy{}
	state := NewState()

	got, decision, err := Apply(state, event(policy, "event-1", "epoch-1", 0, 0), policy)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unbound policy error=%v, want %v", err, ErrInvalidInput)
	}
	if !reflect.DeepEqual(got, state) || decision != (Decision{}) {
		t.Fatalf("unbound policy mutated state or decision: state=%#v decision=%#v", got, decision)
	}
}
