package shadowbilling_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/shadowbilling"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/whitelistfixture"
)

func boundEntitlement(t *testing.T) controlplane.WhiteListEntitlement {
	t.Helper()
	entitlement := whitelistfixture.MustPersisted(t, "account-a")
	entitlement, err := entitlement.Activate("profile-a", "preset-a", "release-a", controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return entitlement
}

func boundPolicy(t *testing.T, entitlement controlplane.WhiteListEntitlement) shadowbilling.Policy {
	t.Helper()
	policy, err := shadowbilling.NewPolicy(entitlement, shadowbilling.PolicySpec{
		BillingPeriodID: "period-1",
		Unit:            shadowbilling.UnitGBDecimal,
		Basis:           shadowbilling.BasisUplinkPlusDownlink,
		IncludedBytes:   100,
		SoftLimitBytes:  150,
		HardLimitBytes:  200,
		GraceBytes:      10,
		Prices: shadowbilling.PriceOptions{Global: &shadowbilling.Price{
			Mode: shadowbilling.PricePaid, Currency: "RUB", MinorUnitsPerUnit: 25000,
		}},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func TestPolicyCanOnlyUseControlPlaneWhiteListIdentity(t *testing.T) {
	entitlement := boundEntitlement(t)
	identity, ok := entitlement.XrayIdentity()
	if !ok {
		t.Fatal("active entitlement has no Xray identity")
	}
	policy := boundPolicy(t, entitlement)
	if _, exported := reflect.TypeOf(policy).FieldByName("ExpectedXrayIdentity"); exported {
		t.Fatal("policy exposes caller-controlled expected Xray identity")
	}
	state := shadowbilling.NewState()
	wrong := shadowbilling.UsageEvent{EventID: "event-1", InstanceID: "s2", MeterEpoch: "epoch-1", XrayIdentity: "ordinary:existing-vpn"}
	got, decision, err := shadowbilling.Apply(state, wrong, policy)
	if !errors.Is(err, shadowbilling.ErrIdentityMismatch) || !reflect.DeepEqual(got, state) || decision != (shadowbilling.Decision{}) {
		t.Fatalf("ordinary identity rejection: state=%#v decision=%#v err=%v", got, decision, err)
	}
	valid := wrong
	valid.XrayIdentity = identity
	got, decision, err = shadowbilling.Apply(got, valid, policy)
	if err != nil || decision.Replay || decision.Diagnostic != shadowbilling.DiagnosticEpochStarted || len(got.LedgerEntries()) != 0 {
		t.Fatalf("valid bound baseline: decision=%#v ledger=%d err=%v", decision, len(got.LedgerEntries()), err)
	}
}
