package api

import (
	"net/http"
	"reflect"
	"testing"
)

func TestCommercialAdminRoutesRequireAuthenticationWithoutDispatch(t *testing.T) {
	business := newCommercialBusinessFake()
	handler := NewControlPlane(business, Config{AdminToken: "test-admin-token"}).Handler()
	for _, route := range []struct {
		path string
		body string
	}{
		{path: "/admin/order/topup-order-1/confirm", body: `{}`},
		{path: "/admin/order/topup-order-1/reject", body: `{}`},
		{path: "/admin/accounts/account-a/whitelist-publication", body: `{"enabled":true}`},
	} {
		response := commercialRequest(t, handler, http.MethodPost, route.path, route.body, "", "admin-key")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s status = %d, want %d; body=%s", route.path, response.Code, http.StatusUnauthorized, response.Body.String())
		}
	}
	if len(business.bindingOrderIDs) != 0 || len(business.confirmCommercialCalls) != 0 || len(business.rejectCommercialCalls) != 0 || len(business.publicationCalls) != 0 {
		t.Fatalf("unauthenticated admin routes dispatched: bindings=%#v confirm=%#v reject=%#v publication=%#v", business.bindingOrderIDs, business.confirmCommercialCalls, business.rejectCommercialCalls, business.publicationCalls)
	}
}

func TestCommercialAdminConfirmDispatchesByPersistedFamilyAndReplays(t *testing.T) {
	business := newCommercialBusinessFake()
	business.bindings["access-looking-topup"] = CommercialOrderBindingView{
		OrderID: "access-looking-topup", Family: "WHITELIST_TOP_UP", AccountID: commercialAccountA,
	}
	business.orders["access-looking-topup"] = CommercialOrderView{
		OrderID: "access-looking-topup", AccountID: commercialAccountA,
		ProductID: "wl-gb-20-v1", AmountMinor: 30_000, Currency: "RUB",
		Bytes: 20_000_000_000, PaymentState: "confirmed",
	}
	handler := NewControlPlane(business, Config{AdminToken: "test-admin-token"}).Handler()
	first := commercialRequest(t, handler, http.MethodPost, "/admin/order/access-looking-topup/confirm", `{}`, "test-admin-token", "confirm-key-1")
	second := commercialRequest(t, handler, http.MethodPost, "/admin/order/access-looking-topup/confirm", `{}`, "test-admin-token", "confirm-key-1")

	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("duplicate top-up confirm = (%d, %q), (%d, %q); want identical 200 replay", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	want := CommercialOrderDecisionCommand{OrderID: "access-looking-topup", Actor: "admin", IdempotencyKey: "confirm-key-1"}
	if !reflect.DeepEqual(business.confirmCommercialCalls, []CommercialOrderDecisionCommand{want, want}) {
		t.Fatalf("top-up confirm calls = %#v, want two exact replay commands", business.confirmCommercialCalls)
	}
	if len(business.confirmMutationKeys) != 1 {
		t.Fatalf("durable confirm mutation keys = %d, want 1", len(business.confirmMutationKeys))
	}
	if len(business.legacyConfirmCalls) != 0 {
		t.Fatalf("persisted top-up family reached legacy confirm: %#v", business.legacyConfirmCalls)
	}
}

func TestCommercialAdminConfirmDispatchesAccessFamilyDespiteWhitelistLookingID(t *testing.T) {
	business := newCommercialBusinessFake()
	business.bindings["wl-gb-looking-access"] = CommercialOrderBindingView{
		OrderID: "wl-gb-looking-access", Family: "ACCESS", AccountID: commercialAccountA,
	}
	business.legacyConfirmResult = ConfirmPaymentResult{Order: OrderView{OrderID: "wl-gb-looking-access", Status: "paid"}}
	response := commercialRequest(t, NewControlPlane(business, Config{AdminToken: "test-admin-token"}).Handler(), http.MethodPost, "/admin/order/wl-gb-looking-access/confirm", `{}`, "test-admin-token", "legacy-confirm-key")

	if response.Code != http.StatusOK {
		t.Fatalf("access confirm status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	want := ConfirmPaymentCommand{OrderID: "wl-gb-looking-access", Actor: "admin", IdempotencyKey: "legacy-confirm-key"}
	if !reflect.DeepEqual(business.legacyConfirmCalls, []ConfirmPaymentCommand{want}) {
		t.Fatalf("legacy confirm calls = %#v, want %#v", business.legacyConfirmCalls, []ConfirmPaymentCommand{want})
	}
	if len(business.confirmCommercialCalls) != 0 {
		t.Fatalf("persisted access family reached top-up confirm: %#v", business.confirmCommercialCalls)
	}
}

func TestCommercialAdminRejectDispatchesBothPersistedFamilies(t *testing.T) {
	business := newCommercialBusinessFake()
	business.bindings["ordinary-looking-topup"] = CommercialOrderBindingView{
		OrderID: "ordinary-looking-topup", Family: "WHITELIST_TOP_UP", AccountID: commercialAccountA,
	}
	business.bindings["wl-looking-access"] = CommercialOrderBindingView{
		OrderID: "wl-looking-access", Family: "ACCESS", AccountID: commercialAccountA,
	}
	business.orders["ordinary-looking-topup"] = CommercialOrderView{OrderID: "ordinary-looking-topup", AccountID: commercialAccountA, ProductID: "wl-gb-50-v1", PaymentState: "rejected"}
	business.legacyCancelResult = OrderView{OrderID: "wl-looking-access", Status: "canceled"}
	handler := NewControlPlane(business, Config{AdminToken: "test-admin-token"}).Handler()
	topup := commercialRequest(t, handler, http.MethodPost, "/admin/order/ordinary-looking-topup/reject", `{}`, "test-admin-token", "reject-topup-key")
	access := commercialRequest(t, handler, http.MethodPost, "/admin/order/wl-looking-access/reject", `{}`, "test-admin-token", "reject-access-key")

	if topup.Code != http.StatusOK || access.Code != http.StatusOK {
		t.Fatalf("reject statuses = top-up %d, access %d; want both 200", topup.Code, access.Code)
	}
	wantTopUp := CommercialOrderDecisionCommand{OrderID: "ordinary-looking-topup", Actor: "admin", IdempotencyKey: "reject-topup-key"}
	if !reflect.DeepEqual(business.rejectCommercialCalls, []CommercialOrderDecisionCommand{wantTopUp}) {
		t.Fatalf("top-up reject calls = %#v, want %#v", business.rejectCommercialCalls, []CommercialOrderDecisionCommand{wantTopUp})
	}
	wantAccess := CancelOrderCommand{OrderID: "wl-looking-access", Actor: "admin", IdempotencyKey: "reject-access-key"}
	if !reflect.DeepEqual(business.legacyCancelCalls, []CancelOrderCommand{wantAccess}) {
		t.Fatalf("legacy reject calls = %#v, want %#v", business.legacyCancelCalls, []CancelOrderCommand{wantAccess})
	}
}

func TestCommercialAdminPublicationEnableAndDisableAreIdempotentAuditedAndIsolated(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enable", false: "disable"}[enabled], func(t *testing.T) {
			business := newCommercialBusinessFake()
			beforeBalance := business.balance
			handler := NewControlPlane(business, Config{AdminToken: "test-admin-token"}).Handler()
			body := `{"enabled":false}`
			if enabled {
				body = `{"enabled":true}`
			}
			first := commercialRequest(t, handler, http.MethodPost, "/admin/accounts/account-a/whitelist-publication", body, "test-admin-token", "publication-key-1")
			second := commercialRequest(t, handler, http.MethodPost, "/admin/accounts/account-a/whitelist-publication", body, "test-admin-token", "publication-key-1")

			if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
				t.Fatalf("duplicate publication = (%d, %q), (%d, %q); want identical 200 replay", first.Code, first.Body.String(), second.Code, second.Body.String())
			}
			want := CommercialPublicationCommand{AccountID: commercialAccountA, Enabled: enabled, Actor: "admin", IdempotencyKey: "publication-key-1"}
			if !reflect.DeepEqual(business.publicationCalls, []CommercialPublicationCommand{want, want}) {
				t.Fatalf("publication calls = %#v, want two exact replay commands", business.publicationCalls)
			}
			if len(business.publicationMutationKeys) != 1 {
				t.Fatalf("publication mutation keys = %d, want 1", len(business.publicationMutationKeys))
			}
			got := decodeCommercialJSON[CommercialPublicationView](t, first)
			if got.Enabled != enabled || got.OperationID == "" || got.AuditID == "" || got.Version != 7 {
				t.Fatalf("publication result = %#v, want enabled=%t with operation and audit evidence", got, enabled)
			}
			if !reflect.DeepEqual(business.balance, beforeBalance) {
				t.Fatalf("publication changed balance: before=%#v after=%#v", beforeBalance, business.balance)
			}
			if business.legacyEnableCalls != 0 || business.legacyDisableCalls != 0 {
				t.Fatalf("publication changed ordinary access: enable calls=%d disable calls=%d", business.legacyEnableCalls, business.legacyDisableCalls)
			}
		})
	}
}

func TestCommercialAPIKeepsCurrentLegacyAdminCallbacks(t *testing.T) {
	business := newCommercialBusinessFake()
	handler := NewControlPlane(business, Config{AdminToken: "test-admin-token"}).Handler()
	confirm := commercialRequest(t, handler, http.MethodPost, "/admin/order/confirm", `{"order_id":"legacy-order-1"}`, "test-admin-token", "current-confirm-key")
	cancel := commercialRequest(t, handler, http.MethodPost, "/admin/order/cancel", `{"order_id":"legacy-order-1"}`, "test-admin-token", "current-cancel-key")

	if confirm.Code != http.StatusOK || cancel.Code != http.StatusOK {
		t.Fatalf("legacy admin callback statuses = confirm %d, cancel %d; want both 200", confirm.Code, cancel.Code)
	}
	wantConfirm := ConfirmPaymentCommand{OrderID: "legacy-order-1", Actor: "admin", IdempotencyKey: "current-confirm-key"}
	wantCancel := CancelOrderCommand{OrderID: "legacy-order-1", Actor: "admin", IdempotencyKey: "current-cancel-key"}
	if !reflect.DeepEqual(business.legacyConfirmCalls, []ConfirmPaymentCommand{wantConfirm}) {
		t.Fatalf("current confirm callback calls = %#v, want %#v", business.legacyConfirmCalls, []ConfirmPaymentCommand{wantConfirm})
	}
	if !reflect.DeepEqual(business.legacyCancelCalls, []CancelOrderCommand{wantCancel}) {
		t.Fatalf("current cancel callback calls = %#v, want %#v", business.legacyCancelCalls, []CancelOrderCommand{wantCancel})
	}
}
