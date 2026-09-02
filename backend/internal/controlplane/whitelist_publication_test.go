package controlplane

import (
	"errors"
	"reflect"
	"testing"
)

func TestDeriveWhiteListPublicationIntentEmitsOnlyOnUsabilityCrossing(t *testing.T) {
	const entitlementID = "wl-ent-publication-transition"
	publishable := EvaluateWhiteListPublication(validWhiteListPublicationFacts())

	intent, changed, err := DeriveWhiteListPublicationIntent(entitlementID, false, publishable)
	if err != nil || !changed || intent != (WhiteListPublicationIntent{
		EntitlementID: entitlementID,
		Action:        WhiteListPublicationEnable,
	}) {
		t.Fatalf("enable intent=%#v changed=%v err=%v", intent, changed, err)
	}
	if repeated, repeatedChanged, repeatErr := DeriveWhiteListPublicationIntent(
		entitlementID, true, publishable,
	); repeatErr != nil || repeatedChanged || repeated != (WhiteListPublicationIntent{}) {
		t.Fatalf("repeated enable intent=%#v changed=%v err=%v", repeated, repeatedChanged, repeatErr)
	}

	closed := closedWhiteListPublication(WhiteListPublicationNoBalance)
	intent, changed, err = DeriveWhiteListPublicationIntent(entitlementID, true, closed)
	if err != nil || !changed || intent != (WhiteListPublicationIntent{
		EntitlementID: entitlementID,
		Action:        WhiteListPublicationRevoke,
	}) {
		t.Fatalf("revoke intent=%#v changed=%v err=%v", intent, changed, err)
	}
	if repeated, repeatedChanged, repeatErr := DeriveWhiteListPublicationIntent(
		entitlementID, false, closed,
	); repeatErr != nil || repeatedChanged || repeated != (WhiteListPublicationIntent{}) {
		t.Fatalf("repeated revoke intent=%#v changed=%v err=%v", repeated, repeatedChanged, repeatErr)
	}
}

func TestDeriveWhiteListPublicationIntentKeepsDefaultOffAndHonorsExplicitActivation(t *testing.T) {
	const entitlementID = "wl-ent-publication-activation"
	base := validWhiteListPublicationFacts()

	disabled := base
	disabled.ActivationSource = WhiteListActivationDisabled
	disabledBefore := disabled
	decision := EvaluateWhiteListPublication(disabled)
	intent, changed, err := DeriveWhiteListPublicationIntent(entitlementID, false, decision)
	if err != nil || changed || intent != (WhiteListPublicationIntent{}) ||
		decision.Verdict != WhiteListPublicationNoEntitlement {
		t.Fatalf("default-off decision=%#v intent=%#v changed=%v err=%v", decision, intent, changed, err)
	}
	if !reflect.DeepEqual(disabled, disabledBefore) {
		t.Fatal("default-off evaluation mutated balance or publication facts")
	}

	for _, source := range []WhiteListActivationSource{
		WhiteListActivationConfirmedGBPurchase,
		WhiteListActivationAdminEnable,
	} {
		facts := base
		facts.ActivationSource = source
		factsBefore := facts
		decision = EvaluateWhiteListPublication(facts)
		intent, changed, err = DeriveWhiteListPublicationIntent(entitlementID, false, decision)
		if err != nil || !changed || intent.Action != WhiteListPublicationEnable {
			t.Fatalf("source=%q decision=%#v intent=%#v changed=%v err=%v", source, decision, intent, changed, err)
		}
		if !reflect.DeepEqual(facts, factsBefore) {
			t.Fatalf("source=%q evaluation mutated balance or publication facts", source)
		}
	}

	disabled = base
	disabled.ActivationSource = WhiteListActivationDisabled
	disabledBefore = disabled
	decision = EvaluateWhiteListPublication(disabled)
	intent, changed, err = DeriveWhiteListPublicationIntent(entitlementID, true, decision)
	if err != nil || !changed || intent.Action != WhiteListPublicationRevoke {
		t.Fatalf("admin disable decision=%#v intent=%#v changed=%v err=%v", decision, intent, changed, err)
	}
	if !reflect.DeepEqual(disabled, disabledBefore) || disabled.AvailableBytes != base.AvailableBytes {
		t.Fatal("admin disable changed purchased balance facts")
	}
}

func TestDeriveWhiteListPublicationIntentRevokesEveryClosedVerdict(t *testing.T) {
	for _, verdict := range []WhiteListPublicationVerdict{
		WhiteListPublicationNoEntitlement,
		WhiteListPublicationPrimaryExpired,
		WhiteListPublicationProjectionPending,
		WhiteListPublicationProjectionStale,
		WhiteListPublicationNoBalance,
		WhiteListPublicationReleaseMismatch,
		WhiteListPublicationSidecarUnavailable,
	} {
		intent, changed, err := DeriveWhiteListPublicationIntent(
			"wl-ent-publication-closed", true, closedWhiteListPublication(verdict),
		)
		if err != nil || !changed || intent.Action != WhiteListPublicationRevoke {
			t.Fatalf("verdict=%q intent=%#v changed=%v err=%v", verdict, intent, changed, err)
		}
	}
}

func TestDeriveWhiteListPublicationIntentRejectsMissingEntitlement(t *testing.T) {
	valid := EvaluateWhiteListPublication(validWhiteListPublicationFacts())
	intent, changed, err := DeriveWhiteListPublicationIntent("", false, valid)
	if !errors.Is(err, ErrConflict) || changed || intent != (WhiteListPublicationIntent{}) {
		t.Fatalf("intent=%#v changed=%v err=%v, want ErrConflict", intent, changed, err)
	}
}

func TestDeriveWhiteListPublicationIntentTreatsMalformedDecisionAsClosed(t *testing.T) {
	tests := []struct {
		name     string
		decision WhiteListPublicationDecision
	}{
		{name: "unknown verdict", decision: WhiteListPublicationDecision{Verdict: "UNKNOWN"}},
		{name: "publishable metadata missing", decision: WhiteListPublicationDecision{Verdict: WhiteListPublicationPublishable}},
		{name: "closed metadata retained", decision: WhiteListPublicationDecision{Verdict: WhiteListPublicationNoBalance, ProjectionVersion: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent, changed, err := DeriveWhiteListPublicationIntent("wl-ent-invalid", true, test.decision)
			if err != nil || !changed || intent != (WhiteListPublicationIntent{
				EntitlementID: "wl-ent-invalid",
				Action:        WhiteListPublicationRevoke,
			}) {
				t.Fatalf("published intent=%#v changed=%v err=%v, want fail-closed revoke", intent, changed, err)
			}

			intent, changed, err = DeriveWhiteListPublicationIntent("wl-ent-invalid", false, test.decision)
			if err != nil || changed || intent != (WhiteListPublicationIntent{}) {
				t.Fatalf("unpublished intent=%#v changed=%v err=%v, want closed no-op", intent, changed, err)
			}
		})
	}
}
