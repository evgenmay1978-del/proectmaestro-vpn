package controlplane

import (
	"context"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
	"testing"
)

func TestWhiteListFinalAuthorizationCannotBeManufacturedByZeroValue(t *testing.T) {
	var authorization WhiteListFinalReceiptAuthorization
	if authorization.Verified() || authorization.Unused() || authorization.EventID() != "" {
		t.Fatal("zero authorization trusted")
	}
	db, service := newCustomerIntegritySQLite(t)
	_ = db
	for _, proof := range []sidecaragentclient.ManagedFinalReceipt{{}, {ReceiptID: testDigest("a"), ProofSHA256: testDigest("b")}} {
		if _, err := service.AuthorizeWhiteListFinalReceipt(context.Background(), "s4", proof); err == nil {
			t.Fatal("malformed final proof accepted")
		}
	}
}
