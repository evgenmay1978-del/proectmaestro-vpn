package api

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestCommercialAccountAndClaimRoutesRequireAuthentication(t *testing.T) {
	business := newCommercialBusinessFake()
	handler := NewControlPlane(business, Config{}).Handler()
	for _, route := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "balance", method: http.MethodGet, path: "/account/whitelist-balance"},
		{name: "delivery", method: http.MethodPost, path: "/account/subscription-delivery", body: `{"client":"incy"}`},
		{name: "paid claim", method: http.MethodPost, path: "/order/topup-order-1/paid-claim", body: `{}`},
	} {
		t.Run(route.name, func(t *testing.T) {
			response := commercialRequest(t, handler, route.method, route.path, route.body, "", "unauthenticated-key")
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s status = %d, want %d; body=%s", route.path, response.Code, http.StatusUnauthorized, response.Body.String())
			}
		})
	}
	if len(business.customerTokens) != 0 || len(business.bindingOrderIDs) != 0 || len(business.deliveryCalls) != 0 || len(business.claimCommercialCalls) != 0 {
		t.Fatalf("unauthenticated routes reached business: tokens=%#v bindings=%#v delivery=%#v claims=%#v", business.customerTokens, business.bindingOrderIDs, business.deliveryCalls, business.claimCommercialCalls)
	}
}

func TestCommercialPaidClaimDeniesOrderOwnedByDifferentAccount(t *testing.T) {
	business := newCommercialBusinessFake()
	business.bindings["topup-order-b"] = CommercialOrderBindingView{
		OrderID: "topup-order-b", Family: "WHITELIST_TOP_UP", AccountID: commercialAccountB,
	}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodPost, "/order/topup-order-b/paid-claim", `{}`, commercialTokenA, "claim-key-b")

	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-account claim status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if !reflect.DeepEqual(business.bindingOrderIDs, []string{"topup-order-b"}) {
		t.Fatalf("claim binding lookups = %#v, want persisted order lookup", business.bindingOrderIDs)
	}
	if len(business.claimCommercialCalls) != 0 || len(business.legacyClaimCalls) != 0 {
		t.Fatalf("cross-account claim dispatched: commercial=%#v legacy=%#v", business.claimCommercialCalls, business.legacyClaimCalls)
	}
}

func TestCommercialPaidClaimDispatchesAccessFamilyThroughLegacyPort(t *testing.T) {
	business := newCommercialBusinessFake()
	business.bindings["wl-looking-access-order"] = CommercialOrderBindingView{
		OrderID: "wl-looking-access-order", Family: "ACCESS", AccountID: commercialAccountA,
	}
	business.legacyClaimResult = OrderView{OrderID: "wl-looking-access-order", Status: "payment_claimed"}
	response := commercialRequest(t, NewControlPlane(business, Config{}).Handler(), http.MethodPost, "/order/wl-looking-access-order/paid-claim", `{}`, commercialTokenA, "legacy-claim-key")

	if response.Code != http.StatusOK {
		t.Fatalf("access-family paid claim status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	want := ClaimPaymentCommand{OrderID: "wl-looking-access-order", IdempotencyKey: "legacy-claim-key"}
	if !reflect.DeepEqual(business.legacyClaimCalls, []ClaimPaymentCommand{want}) {
		t.Fatalf("legacy claim calls = %#v, want %#v", business.legacyClaimCalls, []ClaimPaymentCommand{want})
	}
	if len(business.claimCommercialCalls) != 0 {
		t.Fatalf("access-family claim reached top-up port: %#v", business.claimCommercialCalls)
	}
}

func TestCommercialOrderKeepsLegacyAccessRequestAndJSONUnchanged(t *testing.T) {
	business := newCommercialBusinessFake()
	handler := NewControlPlane(business, Config{}).Handler()
	response := commercialRequest(t, handler, http.MethodPost, "/order", `{"tariff":"month","sub_token":"existing-token","login":"alice"}`, "", "legacy-create-key")

	if response.Code != http.StatusOK {
		t.Fatalf("legacy POST /order status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	wantCommand := CreateOrderCommand{Tariff: "month", SubToken: "existing-token", Login: "alice", IdempotencyKey: "legacy-create-key"}
	if !reflect.DeepEqual(business.legacyCreateCalls, []CreateOrderCommand{wantCommand}) {
		t.Fatalf("legacy create calls = %#v, want %#v", business.legacyCreateCalls, []CreateOrderCommand{wantCommand})
	}
	if len(business.createCommercialCalls) != 0 {
		t.Fatalf("legacy access order reached commercial create port: %#v", business.createCommercialCalls)
	}
	wantJSON := `{"order_id":"legacy-order-1","code":"LEGACY","rub":400,"days":30,"tariff":"month","sbp_phone":"+70000000000","pay_url":"https://pay.example/order","status":"created"}` + "\n"
	if response.Body.String() != wantJSON {
		t.Fatalf("legacy order JSON = %q, want byte-compatible %q", response.Body.String(), wantJSON)
	}
}

func TestCommercialAPIKeepsLegacyTariffsRoute(t *testing.T) {
	business := newCommercialBusinessFake()
	response := commercialRequest(t, NewControlPlane(business, Config{SBPPhone: "+70000000000", PayURL: "https://pay.example/order"}).Handler(), http.MethodGet, "/order/tariffs", "", "", "")

	if response.Code != http.StatusOK {
		t.Fatalf("legacy tariffs status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if business.legacyTariffCalls != 1 {
		t.Fatalf("legacy tariff calls = %d, want 1", business.legacyTariffCalls)
	}
	got := decodeCommercialJSON[map[string]any](t, response)
	if _, hasProducts := got["products"]; hasProducts {
		t.Fatalf("legacy tariffs unexpectedly contains commercial products: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"id":"month"`) || !strings.Contains(response.Body.String(), `"rub":400`) {
		t.Fatalf("legacy tariffs JSON changed: %s", response.Body.String())
	}
}
