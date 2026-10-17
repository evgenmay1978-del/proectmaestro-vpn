package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const panelPrivateSubscriptionMarker = "https://private.example/sub/customer-secret"

type panelSecretLeakBusiness struct {
	panelPermissionBusiness
}

func (business *panelSecretLeakBusiness) ListCustomers(context.Context, CustomerFilter) ([]CustomerView, error) {
	return []CustomerView{{Login: "alice", SubURL: panelPrivateSubscriptionMarker}}, nil
}

func (business *panelSecretLeakBusiness) CustomerByLogin(context.Context, string) (CustomerView, error) {
	return CustomerView{Login: "alice", SubURL: panelPrivateSubscriptionMarker}, nil
}

func (business *panelSecretLeakBusiness) CustomerUsage(context.Context, string) (CustomerUsageView, error) {
	return CustomerUsageView{Login: "alice"}, nil
}

func (business *panelSecretLeakBusiness) ProvisionCustomer(context.Context, ProvisionCustomerCommand) (CustomerView, error) {
	return CustomerView{Login: "alice", SubURL: panelPrivateSubscriptionMarker}, nil
}

func (business *panelSecretLeakBusiness) ListOrders(context.Context, OrderFilter) ([]OrderView, error) {
	return []OrderView{{OrderID: "order-1", Status: "pending", SubURL: panelPrivateSubscriptionMarker}}, nil
}

func (business *panelSecretLeakBusiness) ConfirmPayment(context.Context, ConfirmPaymentCommand) (ConfirmPaymentResult, error) {
	return ConfirmPaymentResult{
		Order:    OrderView{OrderID: "order-1", Status: "paid", SubURL: panelPrivateSubscriptionMarker},
		Customer: CustomerView{Login: "alice", SubURL: panelPrivateSubscriptionMarker},
	}, nil
}

func (business *panelSecretLeakBusiness) CancelOrder(context.Context, CancelOrderCommand) (OrderView, error) {
	return OrderView{OrderID: "order-1", Status: "canceled", SubURL: panelPrivateSubscriptionMarker}, nil
}

func TestControlPlanePanelNeverExposesPrivateSubscriptionURLs(t *testing.T) {
	handler := NewControlPlane(&panelSecretLeakBusiness{}, Config{
		PanelPath: "/mp/", PanelPasswordHash: "configured",
	}).Handler()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "panel html", method: http.MethodGet, path: "/mp/"},
		{name: "customers", method: http.MethodGet, path: "/mp/api/customers"},
		{name: "customer detail", method: http.MethodGet, path: "/mp/api/customer?login=alice"},
		{name: "provision", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"provision","login":"alice","days":30}`},
		{name: "orders", method: http.MethodGet, path: "/mp/api/orders"},
		{name: "confirm", method: http.MethodPost, path: "/mp/api/order/confirm", body: `{"order_id":"order-1"}`},
		{name: "cancel", method: http.MethodPost, path: "/mp/api/order/cancel", body: `{"order_id":"order-1"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
			if test.method == http.MethodPost {
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-CSRF", "panel-csrf")
				request.Header.Set("Idempotency-Key", "test-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			body := strings.ToLower(recorder.Body.String())
			if strings.Contains(body, strings.ToLower(panelPrivateSubscriptionMarker)) || strings.Contains(body, "private.example") {
				t.Fatalf("private subscription URL leaked: %s", recorder.Body.String())
			}
			if strings.Contains(body, `"sub_url"`) {
				t.Fatalf("private subscription field leaked: %s", recorder.Body.String())
			}
			if test.path == "/mp/" && strings.Contains(body, "копировать sub") {
				t.Fatalf("panel HTML exposes a private subscription control: %s", recorder.Body.String())
			}
		})
	}
}
