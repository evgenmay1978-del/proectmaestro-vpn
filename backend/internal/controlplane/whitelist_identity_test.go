package controlplane_test

import (
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/whitelistfixture"
)

func task7Credential() controlplane.WhiteListCredential {
	return controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	}
}

func task7Entitlement(t *testing.T, accountID string) controlplane.WhiteListEntitlement {
	t.Helper()
	return whitelistfixture.MustPersisted(t, accountID)
}

func TestWhiteListEntitlementIdentityIsRandomAndOpaque(t *testing.T) {
	first := task7Entitlement(t, "account-a")
	second := task7Entitlement(t, "account-a")
	other := task7Entitlement(t, "account-b")

	if first.EntitlementID() == "" {
		t.Fatal("entitlement id must not be empty")
	}
	if first.EntitlementID() == second.EntitlementID() {
		t.Fatalf("separate entitlements reused an id: %q", first.EntitlementID())
	}
	if first.EntitlementID() == other.EntitlementID() {
		t.Fatalf("different accounts produced the same entitlement id: %q", first.EntitlementID())
	}
	if strings.Contains(first.EntitlementID(), "account-a") {
		t.Fatalf("entitlement id exposes the raw account id: %q", first.EntitlementID())
	}
}

func TestWhiteListEntitlementXrayIdentityFollowsCredentialAcrossLifecycle(t *testing.T) {
	disabled := task7Entitlement(t, "account-a")
	stableID := disabled.EntitlementID()
	if identity, ok := disabled.XrayIdentity(); ok || identity != "" {
		t.Fatalf("inactive entitlement returned an Xray identity: identity=%q ok=%v", identity, ok)
	}

	active, err := disabled.Activate("profile-a", "preset-a", "release-a", task7Credential())
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	wantIdentity := "wl:" + stableID
	if identity, ok := active.XrayIdentity(); !ok || identity != wantIdentity {
		t.Fatalf("active Xray identity=%q ok=%v, want %q true", identity, ok, wantIdentity)
	}
	if active.EntitlementID() != stableID {
		t.Fatalf("activation changed entitlement id: %q != %q", active.EntitlementID(), stableID)
	}

	suspended, err := active.WithState(controlplane.EntitlementSuspended)
	if err != nil {
		t.Fatalf("WithState(SUSPENDED): %v", err)
	}
	if identity, ok := suspended.XrayIdentity(); !ok || identity != wantIdentity || suspended.EntitlementID() != stableID {
		t.Fatalf("suspension changed identities: entitlement=%q xray=%q ok=%v", suspended.EntitlementID(), identity, ok)
	}

	rotatedCredential := task7Credential()
	rotatedCredential.ClientID = "22222222-2222-4222-8222-222222222222"
	rotated, err := suspended.Activate("profile-a", "preset-a", "release-b", rotatedCredential)
	if err != nil {
		t.Fatalf("Activate(release-b): %v", err)
	}
	if identity, ok := rotated.XrayIdentity(); !ok || identity != wantIdentity || rotated.EntitlementID() != stableID {
		t.Fatalf("release rotation changed identities: entitlement=%q xray=%q ok=%v", rotated.EntitlementID(), identity, ok)
	}
}

func TestWhiteListEntitlementsWithDuplicateCredentialsHaveDistinctXrayIdentities(t *testing.T) {
	first, err := task7Entitlement(t, "account-a").Activate("profile-a", "preset-a", "release-a", task7Credential())
	if err != nil {
		t.Fatalf("activate first: %v", err)
	}
	second, err := task7Entitlement(t, "account-b").Activate("profile-a", "preset-a", "release-a", task7Credential())
	if err != nil {
		t.Fatalf("activate second: %v", err)
	}
	firstIdentity, firstOK := first.XrayIdentity()
	secondIdentity, secondOK := second.XrayIdentity()
	if !firstOK || !secondOK {
		t.Fatalf("missing identities: first=%v second=%v", firstOK, secondOK)
	}
	if firstIdentity == secondIdentity {
		t.Fatalf("duplicate client credentials aliased Xray identity %q", firstIdentity)
	}
}
