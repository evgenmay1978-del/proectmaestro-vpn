package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type panelPermissionBusiness struct {
	dispatchBusiness
	authorize     []AuthorizeCommand
	confirmResult ConfirmPaymentResult
}

func (b *panelPermissionBusiness) Authorize(_ context.Context, command AuthorizeCommand) (PrincipalView, error) {
	b.authorize = append(b.authorize, command)
	b.called("authorize")
	return PrincipalView{ID: "owner", Permissions: []string{
		"customer.read",
		"customer.provision",
		"payment.decide",
		"settings.critical",
	}}, nil
}

func (b *panelPermissionBusiness) ListOrders(context.Context, OrderFilter) ([]OrderView, error) {
	b.called("list_orders")
	return []OrderView{{OrderID: "ord", Status: "pending"}}, nil
}

func (b *panelPermissionBusiness) ConfirmPayment(context.Context, ConfirmPaymentCommand) (ConfirmPaymentResult, error) {
	b.called("confirm_payment")
	return b.confirmResult, nil
}

func (b *panelPermissionBusiness) ClusterStatus(context.Context) (ClusterStatusView, error) {
	b.called("cluster_status")
	return ClusterStatusView{Ready: true, Quorum: true}, nil
}

func (b *panelPermissionBusiness) RecentAudit(context.Context, AuditFilter) ([]AuditView, error) {
	b.called("recent_audit")
	return []AuditView{}, nil
}

func TestControlPlanePanelLoginSetsHardenedSessionAndCSRFCookies(t *testing.T) {
	handler := NewControlPlane(&dispatchBusiness{}, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/mp/api/login", strings.NewReader(`{"password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	cookies := map[string]*http.Cookie{}
	for _, cookie := range response.Result().Cookies() {
		cookies[cookie.Name] = cookie
	}
	session := cookies[controlPlanePanelCookie]
	csrf := cookies[controlPlanePanelCSRFCookie]
	if session == nil || csrf == nil {
		t.Fatalf("cookies=%v, want session and csrf cookies", cookies)
	}
	for name, cookie := range map[string]*http.Cookie{
		controlPlanePanelCookie:     session,
		controlPlanePanelCSRFCookie: csrf,
	} {
		if cookie.Path != "/mp/" || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
			t.Errorf("cookie %s=%+v, want Path=/mp/ Secure SameSite=Strict", name, cookie)
		}
	}
	if !session.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if csrf.HttpOnly {
		t.Error("csrf cookie must remain readable by the panel SPA")
	}
}

func TestControlPlanePanelPermissionsMatchActionRisk(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		wantPermission string
	}{
		{name: "orders", method: http.MethodGet, path: "/mp/api/orders", wantPermission: "payment.decide"},
		{name: "confirm", method: http.MethodPost, path: "/mp/api/order/confirm", body: `{"order_id":"ord"}`, wantPermission: "payment.decide"},
		{name: "cancel", method: http.MethodPost, path: "/mp/api/order/cancel", body: `{"order_id":"ord"}`, wantPermission: "payment.decide"},
		{name: "cluster", method: http.MethodGet, path: "/mp/api/cluster-status", wantPermission: "settings.critical"},
		{name: "audit", method: http.MethodGet, path: "/mp/api/audit?limit=10", wantPermission: "settings.critical"},
		{name: "provision", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"provision","login":"alice","days":30}`, wantPermission: "customer.provision"},
		{name: "extend", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"extend","login":"alice","days":30}`, wantPermission: "customer.provision"},
		{name: "renew", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"renew","login":"alice","days":30}`, wantPermission: "customer.provision"},
		{name: "set expiry", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"set_expiry","login":"alice","expires":"2026-09-30T00:00:00Z"}`, wantPermission: "customer.provision"},
		{name: "reset devices", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"reset_devices","login":"alice"}`, wantPermission: "customer.provision"},
		{name: "disable", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"disable","login":"alice"}`, wantPermission: "settings.critical"},
		{name: "enable", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"enable","login":"alice"}`, wantPermission: "settings.critical"},
		{name: "delete", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"delete","login":"alice"}`, wantPermission: "settings.critical"},
		{name: "delete expired", method: http.MethodPost, path: "/mp/api/action", body: `{"action":"delete_expired"}`, wantPermission: "settings.critical"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			business := &panelPermissionBusiness{}
			handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
			if test.method == http.MethodPost {
				request.Header.Set("X-CSRF", "panel-csrf")
				request.Header.Set("Idempotency-Key", "permission-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if len(business.authorize) != 1 || business.authorize[0].Permission != test.wantPermission {
				t.Fatalf("authorize=%v, want exactly one %q check", business.authorize, test.wantPermission)
			}
		})
	}
}

func TestControlPlanePanelConfirmRedactsPrivateSubscriptionURLs(t *testing.T) {
	business := &panelPermissionBusiness{confirmResult: ConfirmPaymentResult{
		Order: OrderView{OrderID: "ord", Status: "paid", SubURL: "https://private.test/sub/order-secret"},
		Customer: CustomerView{
			Login:  "alice",
			SubURL: "https://private.test/sub/customer-secret",
		},
	}}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/mp/api/order/confirm", strings.NewReader(`{"order_id":"ord"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF", "panel-csrf")
	request.Header.Set("Idempotency-Key", "confirm-redaction-1")
	request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, secret := range []string{"private.test", "order-secret", "customer-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaks %q: %s", secret, body)
		}
	}
}
