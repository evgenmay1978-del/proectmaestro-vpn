package whitelistready

import "testing"

func TestValidateRequiresExplicitNotRunProductionGates(t *testing.T) {
	catalog, evidence, matrix := validModels(t)
	evidence.ProductionGates = []ProductionGate{
		{ID: "revocation_orchestration", VerificationState: VerificationNotRun, EvidenceClass: EvidenceSchemaOnly},
		{ID: "subscription_cache_invalidation", VerificationState: VerificationNotRun, EvidenceClass: EvidenceSchemaOnly},
		{ID: "real_balance_non_mutation", VerificationState: VerificationNotRun, EvidenceClass: EvidenceSchemaOnly},
	}
	if _, err := Validate(marshalJSON(t, catalog), marshalJSON(t, evidence), marshalJSON(t, matrix)); err != nil {
		t.Fatalf("valid production gates rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*EvidenceBundle)
		code   string
	}{
		{
			name: "missing",
			mutate: func(bundle *EvidenceBundle) {
				bundle.ProductionGates = bundle.ProductionGates[1:]
			},
			code: CodeProductionGateSetInvalid,
		},
		{
			name: "duplicate",
			mutate: func(bundle *EvidenceBundle) {
				bundle.ProductionGates[1].ID = bundle.ProductionGates[0].ID
			},
			code: CodeDuplicateID,
		},
		{
			name: "false green",
			mutate: func(bundle *EvidenceBundle) {
				bundle.ProductionGates[0].VerificationState = VerificationPassed
			},
			code: CodeProductionGateInvalid,
		},
		{
			name: "fixture evidence cannot satisfy production gate",
			mutate: func(bundle *EvidenceBundle) {
				bundle.ProductionGates[0].EvidenceClass = EvidenceFixtureReplay
			},
			code: CodeProductionGateInvalid,
		},
		{
			name: "evidence ref forbidden while not run",
			mutate: func(bundle *EvidenceBundle) {
				ref := "fixture://not-production"
				bundle.ProductionGates[0].EvidenceRef = &ref
			},
			code: CodeProductionGateInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneJSON(t, evidence)
			test.mutate(&candidate)
			_, err := Validate(marshalJSON(t, catalog), marshalJSON(t, candidate), marshalJSON(t, matrix))
			assertReasonCode(t, err, test.code)
		})
	}
}

func TestSubscriptionFixtureCasesCannotClaimProductionRevocationOrCache(t *testing.T) {
	cases := RequiredCaseIDs("subscription_escaping")
	for _, forbidden := range []string{"revocation", "cache-invalidation"} {
		for _, caseID := range cases {
			if caseID == forbidden {
				t.Fatalf("fixture case %q falsely names a production gate", forbidden)
			}
		}
	}
	for _, required := range []string{"fixture-inactive-state-render", "fixture-state-rerender"} {
		found := false
		for _, caseID := range cases {
			found = found || caseID == required
		}
		if !found {
			t.Fatalf("missing explicitly fixture-only case %q", required)
		}
	}
}
