package controlplane

import (
	"fmt"
	"sync/atomic"
	"testing"
)

var whiteListTestIdentitySequence atomic.Uint64

// NewWhiteListEntitlement exists only in the controlplane test build. The
// production package deliberately has no constructor that can bypass Store.
func NewWhiteListEntitlement(accountID string) (WhiteListEntitlement, error) {
	entitlementID := fmt.Sprintf("wl-ent-%032x", whiteListTestIdentitySequence.Add(1))
	return whiteListEntitlementFromPersistedIdentity(accountID, entitlementID)
}

func TestWhiteListEntitlementRejectsValidButUnpersistedIdentity(t *testing.T) {
	entitlement := WhiteListEntitlement{
		accountID:     "account-a",
		entitlementID: "wl-ent-00000000000000000000000000000001",
		state:         EntitlementDisabled,
	}
	_, err := entitlement.Activate("profile-a", "preset-a", "release-a", WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err == nil {
		t.Fatal("valid-looking but unpersisted entitlement was activated")
	}
	if entitlement.EntitlementID() != "" {
		t.Fatalf("unpersisted entitlement exposed identity %q", entitlement.EntitlementID())
	}
	if identity, ok := entitlement.XrayIdentity(); ok || identity != "" {
		t.Fatalf("unpersisted entitlement exposed Xray identity=%q ok=%v", identity, ok)
	}
}
