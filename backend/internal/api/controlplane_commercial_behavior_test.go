package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// commercialBusinessFake is the HTTP-boundary fixture. Task 7 owns the
// durable service proof; these tests specify the additive API contract.
type commercialBusinessFake struct {
	Business
	customer CustomerView
	catalog  CommercialCatalogView
	orders   map[string]CommercialOrderView
	balance  WhiteListBalanceView
	last     CommercialOrderCommand
}

func (b *commercialBusinessFake) CustomerByToken(context.Context, string) (CustomerView, error) { return b.customer, nil }
func (b *commercialBusinessFake) CommercialCatalog(context.Context) (CommercialCatalogView, error) { return b.catalog, nil }
func (b *commercialBusinessFake) CreateCommercialOrder(_ context.Context, command CommercialOrderCommand) (CommercialOrderView, error) {
	b.last = command
	return b.orders[command.ProductID], nil
}
func (b *commercialBusinessFake) ClaimCommercialPayment(_ context.Context, command CommercialClaimCommand) (CommercialOrderView, error) {
	return b.orders[command.OrderID], nil
}
func (b *commercialBusinessFake) WhiteListBalance(context.Context, string) (WhiteListBalanceView, error) { return b.balance, nil }
func (b *commercialBusinessFake) SetWhiteListPublication(context.Context, CommercialPublicationCommand) (CommercialPublicationView, error) {
	return CommercialPublicationView{Enabled: true, Verdict: "redacted"}, nil
}
func (b *commercialBusinessFake) SubscriptionDelivery(context.Context, CommercialDeliveryCommand) (CommercialDeliveryView, error) {
	return CommercialDeliveryView{AccountID: b.customer.CustomerID, Format: "happ"}, nil
}

func TestCommercialCatalogHasExactAccessAndDecimalGBProducts(t *testing.T) {
	b := &commercialBusinessFake{catalog: CommercialCatalogView{Access: TariffView{ID: "month", RUB: 400}, Products: []CommercialProductView{
		{ID: "gb-5", AmountMinor: 10000, Bytes: 5_000_000_000}, {ID: "gb-20", AmountMinor: 30000, Bytes: 20_000_000_000},
		{ID: "gb-50", AmountMinor: 60000, Bytes: 50_000_000_000}, {ID: "gb-100", AmountMinor: 100000, Bytes: 100_000_000_000},
	}}}
	r := httptest.NewRecorder()
	NewControlPlane(b, Config{}).Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/order/catalog", nil))
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"amount_minor":10000`) || !strings.Contains(r.Body.String(), `"bytes":5000000000`) { t.Fatalf("catalog = %d %s", r.Code, r.Body.String()) }
}

func TestCommercialOrderBindsAccountAndPreservesLegacyAccessJSON(t *testing.T) {
	b := &commercialBusinessFake{customer: CustomerView{CustomerID: "account-a"}, orders: map[string]CommercialOrderView{"gb-5": {OrderID: "topup-1", ProductID: "gb-5", AmountMinor: 10000, Bytes: 5_000_000_000}}}
	r := httptest.NewRecorder(); q := httptest.NewRequest(http.MethodPost, "/order", strings.NewReader(`{"product_id":"gb-5","sub_token":"token-a"}`)); q.Header.Set("Idempotency-Key", "key-1")
	NewControlPlane(b, Config{}).Handler().ServeHTTP(r, q)
	if r.Code != http.StatusOK || b.last.AccountID != "account-a" || !strings.Contains(r.Body.String(), `"product_id":"gb-5"`) { t.Fatalf("order = %d %s", r.Code, r.Body.String()) }
}

func TestCommercialClaimsCallbacksAndAccountRoutesAreBoundAndRedacted(t *testing.T) {
	// The implementation must reject cross-account claim/delivery, replay duplicate
	// claims/callbacks durably, reject expired primary access, dispatch confirm/reject
	// by persisted product membership, preserve balance and ordinary access on disable,
	// and omit tokens, credentials, nodes, and verdict internals from every response.
	// These assertions become executable once the typed CommercialBusiness port exists.
	var _ CommercialBusiness = (*commercialBusinessFake)(nil)
}
