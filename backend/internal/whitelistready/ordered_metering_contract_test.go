package whitelistready

import "testing"

func TestOutOfOrderCaseRequiresAtomicLateSampleEvidence(t *testing.T) {
	facts := requiredFacts("out-of-order")
	if len(facts) != 3 {
		t.Fatalf("out-of-order facts=%#v; want three atomic ordering facts", facts)
	}
	if facts[0].Name != "late_sample_ignored" || facts[0].Kind != FactBoolean || facts[0].BooleanValue == nil || !*facts[0].BooleanValue {
		t.Fatalf("late-sample fact=%#v", facts[0])
	}
	if facts[1].Name != "ledger_unchanged" || facts[1].Kind != FactBoolean || facts[1].BooleanValue == nil || !*facts[1].BooleanValue {
		t.Fatalf("ledger fact=%#v", facts[1])
	}
	if facts[2].Name != "next_delta_bytes" || facts[2].Kind != FactInteger || facts[2].IntegerValue == nil || *facts[2].IntegerValue != 10 {
		t.Fatalf("next-delta fact=%#v", facts[2])
	}
}

func TestCounterResetCaseRequiresExplicitGenerationEvidence(t *testing.T) {
	facts := requiredFacts("counter-reset")
	if len(facts) != 5 {
		t.Fatalf("counter-reset facts=%#v; want five explicit generation facts", facts)
	}
	if facts[0].Name != "ledger_unchanged" || facts[0].Kind != FactBoolean || facts[0].BooleanValue == nil || !*facts[0].BooleanValue {
		t.Fatalf("ledger fact=%#v", facts[0])
	}
	if facts[1].Name != "next_delta_bytes" || facts[1].Kind != FactInteger || facts[1].IntegerValue == nil || *facts[1].IntegerValue != 15 {
		t.Fatalf("next-delta fact=%#v", facts[1])
	}
	if facts[2].Name != "reset_delta_bytes" || facts[2].Kind != FactInteger || facts[2].IntegerValue == nil || *facts[2].IntegerValue != 0 {
		t.Fatalf("reset-delta fact=%#v", facts[2])
	}
	if facts[3].Name != "reset_generation" || facts[3].Kind != FactInteger || facts[3].IntegerValue == nil || *facts[3].IntegerValue != 2 {
		t.Fatalf("reset-generation fact=%#v", facts[3])
	}
	if facts[4].Name != "same_generation_rejected" || facts[4].Kind != FactBoolean || facts[4].BooleanValue == nil || !*facts[4].BooleanValue {
		t.Fatalf("same-generation fact=%#v", facts[4])
	}
}
