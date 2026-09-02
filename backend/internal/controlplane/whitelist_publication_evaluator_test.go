package controlplane

import "testing"

func validWhiteListPublicationFacts() WhiteListPublicationFacts {
	return WhiteListPublicationFacts{
		NowUnix:                 2_100_000_000,
		ActivationSource:        WhiteListActivationConfirmedGBPurchase,
		ActivationEntitlementID: "wl-ent-11111111111111111111111111111111",
		EntitlementID:           "wl-ent-11111111111111111111111111111111",
		EntitlementState:        EntitlementActive,
		PrimaryStatus:           "active",
		PrimaryExpiresAtUnix:    2_100_000_100,
		ProjectionVersion:       7,
		AvailableBytes:          1_000_000_000,
		ObservedThroughUnix:     2_099_999_998,
		ReleaseBindingExact:     true,
		CredentialUsable:        true,
		DesiredGeneration:       9,
		ReceiptSetReady:         true,
		ReceiptsFreshUntilUnix:  2_100_000_010,
		ApprovedNodeCount:       2,
	}
}

func TestEvaluateWhiteListPublicationAllowsOnlyPurchasedOrAdminEnabledBalance(t *testing.T) {
	for _, source := range []WhiteListActivationSource{
		WhiteListActivationConfirmedGBPurchase,
		WhiteListActivationAdminEnable,
	} {
		facts := validWhiteListPublicationFacts()
		facts.ActivationSource = source
		decision := EvaluateWhiteListPublication(facts)
		if decision.Verdict != WhiteListPublicationPublishable ||
			decision.ProjectionVersion != facts.ProjectionVersion ||
			decision.DesiredGeneration != facts.DesiredGeneration ||
			decision.FreshUntilUnix != facts.ObservedThroughUnix+5 {
			t.Fatalf("source=%q decision=%#v", source, decision)
		}
	}

	for _, source := range []WhiteListActivationSource{
		WhiteListActivationDisabled,
		WhiteListActivationSource("PAYMENT_CLAIMED"),
		WhiteListActivationSource("GENERIC_PURCHASE"),
	} {
		facts := validWhiteListPublicationFacts()
		facts.ActivationSource = source
		decision := EvaluateWhiteListPublication(facts)
		if decision != (WhiteListPublicationDecision{Verdict: WhiteListPublicationNoEntitlement}) {
			t.Fatalf("source=%q decision=%#v, want closed", source, decision)
		}
	}
}

func TestEvaluateWhiteListPublicationUsesStrictFailClosedOrder(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*WhiteListPublicationFacts)
		verdict WhiteListPublicationVerdict
	}{
		{"activation entitlement mismatch", func(f *WhiteListPublicationFacts) { f.ActivationEntitlementID += "-other" }, WhiteListPublicationNoEntitlement},
		{"inactive entitlement", func(f *WhiteListPublicationFacts) { f.EntitlementState = EntitlementSuspended }, WhiteListPublicationNoEntitlement},
		{"primary inactive", func(f *WhiteListPublicationFacts) { f.PrimaryStatus = "suspended" }, WhiteListPublicationPrimaryExpired},
		{"primary expiry boundary", func(f *WhiteListPublicationFacts) { f.PrimaryExpiresAtUnix = f.NowUnix }, WhiteListPublicationPrimaryExpired},
		{"projection missing", func(f *WhiteListPublicationFacts) { f.ProjectionVersion = 0 }, WhiteListPublicationProjectionPending},
		{"projection pending", func(f *WhiteListPublicationFacts) { f.ProjectionPending = true }, WhiteListPublicationProjectionPending},
		{"projection freshness boundary", func(f *WhiteListPublicationFacts) { f.ObservedThroughUnix = f.NowUnix - 5 }, WhiteListPublicationProjectionStale},
		{"no balance", func(f *WhiteListPublicationFacts) { f.AvailableBytes = 0 }, WhiteListPublicationNoBalance},
		{"release mismatch", func(f *WhiteListPublicationFacts) { f.ReleaseBindingExact = false }, WhiteListPublicationReleaseMismatch},
		{"credential unusable", func(f *WhiteListPublicationFacts) { f.CredentialUsable = false }, WhiteListPublicationSidecarUnavailable},
		{"generation missing", func(f *WhiteListPublicationFacts) { f.DesiredGeneration = 0 }, WhiteListPublicationSidecarUnavailable},
		{"receipts missing", func(f *WhiteListPublicationFacts) { f.ReceiptSetReady = false }, WhiteListPublicationSidecarUnavailable},
		{"receipts expiry boundary", func(f *WhiteListPublicationFacts) { f.ReceiptsFreshUntilUnix = f.NowUnix }, WhiteListPublicationSidecarUnavailable},
		{"approved nodes missing", func(f *WhiteListPublicationFacts) { f.ApprovedNodeCount = 0 }, WhiteListPublicationSidecarUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := validWhiteListPublicationFacts()
			test.mutate(&facts)
			decision := EvaluateWhiteListPublication(facts)
			if decision != (WhiteListPublicationDecision{Verdict: test.verdict}) {
				t.Fatalf("decision=%#v, want closed verdict %q and zero metadata", decision, test.verdict)
			}
		})
	}
}

func TestEvaluateWhiteListPublicationPrecedenceAndFreshnessMinimum(t *testing.T) {
	facts := validWhiteListPublicationFacts()
	facts.ActivationSource = WhiteListActivationDisabled
	facts.PrimaryStatus = "suspended"
	facts.ProjectionPending = true
	facts.AvailableBytes = 0
	facts.ReleaseBindingExact = false
	facts.ReceiptSetReady = false
	if got := EvaluateWhiteListPublication(facts).Verdict; got != WhiteListPublicationNoEntitlement {
		t.Fatalf("precedence verdict=%q", got)
	}

	facts = validWhiteListPublicationFacts()
	facts.PrimaryExpiresAtUnix = facts.NowUnix + 1
	facts.ReceiptsFreshUntilUnix = facts.NowUnix + 2
	facts.ObservedThroughUnix = facts.NowUnix - 1
	decision := EvaluateWhiteListPublication(facts)
	if decision.Verdict != WhiteListPublicationPublishable || decision.FreshUntilUnix != facts.NowUnix+1 {
		t.Fatalf("minimum freshness decision=%#v", decision)
	}
}
