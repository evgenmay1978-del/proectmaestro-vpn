package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
)

func TestLegacyOrphanHistoryResponseOmitsInventedCustomerAndOperation(t *testing.T) {
	result := ConfirmPaymentResult{Order: OrderView{OrderID: "ord_original", Code: "Moriginal", RUB: 300, Status: "paid", SubURL: "https://subscription.example.test/sub/original"}, LegacyHistory: true}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(encoded, &document) != nil {
		t.Fatal("invalid response")
	}
	if _, exists := document["customer"]; exists {
		t.Fatal("orphan was given a fake customer")
	}
	if _, exists := document["operation"]; exists {
		t.Fatal("history was given a new operation")
	}
	if len(document) != 1 {
		t.Fatal("history response invented fields")
	}
	result.LegacyHistory = false
	encoded, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(encoded, &document) != nil || document["customer"] == nil || document["operation"] == nil {
		t.Fatal("ordinary confirmation response changed")
	}
}

func TestLegacyOrderPublicViewKeepsRawIdentityAndHistoricalTerms(t *testing.T) {
	business := &ServiceBusiness{cfg: ServiceBusinessConfig{SubBaseURL: "https://subscription.example.test", SBPPhone: "synthetic-payment-phone"}}
	original := legacyorder.Order{ID: "ord_original", Code: "Moriginal", Tariff: "1m", Days: 30, Rub: 300, Login: "OriginalCase", Status: "paid", SubToken: "original-token", CreatedAt: time.Unix(1000000, 123456789).UTC()}
	view := business.nativeLegacyOrderView(controlplane.LegacyOrderView{Order: original, HistoricalGrant: true})
	if view.OrderID != original.ID || view.Code != original.Code || view.RUB != 300 || view.Tariff != "1m" || view.Days != 30 || view.Status != "paid" || view.SubURL != "https://subscription.example.test/sub/original-token" || view.ResultGeneration != 0 || view.ProvisioningState != "" {
		t.Fatal("history public polling changed source values or invented readiness")
	}
	original.Status, original.SubToken = "pending", ""
	view = business.nativeLegacyOrderView(controlplane.LegacyOrderView{Order: original})
	if view.OrderID != original.ID || view.Status != "pending" || view.SubURL != "" {
		t.Fatal("old pending order became expired or paid")
	}
}
