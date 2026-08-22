package v1

import (
	"testing"
	"time"
)

func TestExactAmountUsesCanonicalIntegerWireFormat(t *testing.T) {
	valid := ExactAmount{Numerator: "125", Denominator: 2, Currency: "RUB"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid amount rejected: %v", err)
	}

	for name, amount := range map[string]ExactAmount{
		"float numerator":  {Numerator: "1.25", Denominator: 1, Currency: "RUB"},
		"negative":         {Numerator: "-1", Denominator: 1, Currency: "RUB"},
		"leading zero":     {Numerator: "01", Denominator: 1, Currency: "RUB"},
		"zero denominator": {Numerator: "1", Denominator: 0, Currency: "RUB"},
		"not reduced":      {Numerator: "10", Denominator: 2, Currency: "RUB"},
		"lower currency":   {Numerator: "1", Denominator: 1, Currency: "rub"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := amount.Validate(); err == nil {
				t.Fatalf("invalid amount accepted: %#v", amount)
			}
		})
	}
}

func TestDTOValidationEnforcesAccountScopeAndBounds(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	fixtures := validFixtures(now)
	if err := fixtures.ValidateForAccount("acct_1"); err != nil {
		t.Fatalf("valid fixtures rejected: %v", err)
	}

	wrongAccount := fixtures
	wrongAccount.Ledger.Items[0].AccountID = "acct_2"
	if err := wrongAccount.ValidateForAccount("acct_1"); err == nil {
		t.Fatal("cross-account ledger row accepted")
	}

	tooMany := fixtures
	tooMany.Audit.Items = make([]AuditRecord, MaxPageSize+1)
	if err := tooMany.ValidateForAccount("acct_1"); err == nil {
		t.Fatal("oversized audit page accepted")
	}

	secretField := fixtures
	secretField.Audit.Items[0].Changes[0].Field = "origin_ip"
	if err := secretField.ValidateForAccount("acct_1"); err == nil {
		t.Fatal("non-panel audit field accepted")
	}
}
