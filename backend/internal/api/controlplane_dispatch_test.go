package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestControlPlaneRoutesHaveNoUnavailablePlaceholder(t *testing.T) {
	source, err := os.ReadFile("controlplane_port.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "controlPlaneUnavailable") {
		t.Fatal("rqlite runtime still exposes the generic unavailable placeholder")
	}
}

func TestControlPlaneDispatchesEveryRequiredRouteFamily(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{
		AdminToken:        "admin-secret",
		PanelPath:         "/mp/",
		PanelPasswordHash: "configured",
		UpdateDir:         "configured",
	}).Handler()
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	tests := []struct {
		name, method, path, body, wantCall string
		admin, panelWrite                  bool
	}{
		{"subscription", http.MethodGet, "/sub/token", "", "subscription_snapshot", false, false},
		{"subscription info", http.MethodGet, "/sub/token/info", "", "subscription_snapshot", false, false},
		{"subscription helpers", http.MethodGet, "/sub/token/helpers", "", "subscription_snapshot", false, false},
		{"claim", http.MethodPost, "/claim", `{"code":"alice","device":"device"}`, "touch_device", false, false},
		{"tariffs", http.MethodGet, "/order/tariffs", "", "tariffs", false, false},
		{"create order", http.MethodPost, "/order", `{"tariff":"month"}`, "create_order", false, false},
		{"get order", http.MethodGet, "/order/ord", "", "order_by_id", false, false},
		{"claim payment", http.MethodPost, "/order/paid-claim", `{"order_id":"ord"}`, "mark_payment_claimed", false, false},
		{"trial", http.MethodPost, "/trial", `{"nick":"alice","anchor":"anchor","device":"device"}`, "redeem_trial", false, false},
		{"approved ota", http.MethodGet, "/update/update.json", "", "approved_ota", false, false},
		{"admin provision", http.MethodPost, "/admin/provision", `{"login":"alice","days":30}`, "provision", true, false},
		{"admin extend", http.MethodPost, "/admin/extend", `{"login":"alice","days":30}`, "extend", true, false},
		{"admin renew", http.MethodPost, "/admin/renew", `{"login":"alice","days":30}`, "renew", true, false},
		{"admin set expiry", http.MethodPost, "/admin/set-expiry", `{"login":"alice","expires":"` + expires + `"}`, "set_expiry", true, false},
		{"admin reset", http.MethodPost, "/admin/reset-devices", `{"login":"alice"}`, "reset_devices", true, false},
		{"admin customer", http.MethodGet, "/admin/customer?login=alice", "", "customer_by_login", true, false},
		{"backfill anytls", http.MethodPost, "/admin/backfill-anytls", `{}`, "reconcile:anytls", true, false},
		{"backfill s3", http.MethodPost, "/admin/backfill-s3", `{}`, "reconcile:s3", true, false},
		{"backfill s4", http.MethodPost, "/admin/backfill-s4", `{}`, "reconcile:s4", true, false},
		{"migrate anytls", http.MethodPost, "/admin/migrate-anytls-s2", `{}`, "migrate_endpoint", true, false},
		{"confirm order", http.MethodPost, "/admin/order/confirm", `{"order_id":"ord"}`, "confirm_payment", true, false},
		{"cancel order", http.MethodPost, "/admin/order/cancel", `{"order_id":"ord"}`, "cancel_order", true, false},
		{"admin olcrtc", http.MethodGet, "/admin/olcrtc", "", "olcrtc_state", true, false},
		{"admin olcrtc room", http.MethodPost, "/admin/olcrtc/room", `{"login":"alice","room":"https://room","provider":"telemost"}`, "olcrtc_room", true, false},
		{"panel customers", http.MethodGet, "/mp/api/customers", "", "list_customers", false, false},
		{"panel customer", http.MethodGet, "/mp/api/customer?login=alice", "", "customer_usage", false, false},
		{"panel stats", http.MethodGet, "/mp/api/stats", "", "customer_stats", false, false},
		{"panel olcrtc", http.MethodGet, "/mp/api/olcrtc", "", "wbtoken_status", false, false},
		{"panel olcrtc room", http.MethodPost, "/mp/api/olcrtc/room", `{"login":"alice","room":"https://room","provider":"telemost"}`, "olcrtc_room", false, true},
		{"panel olcrtc grant", http.MethodPost, "/mp/api/olcrtc/login", `{"login":"alice","action":"add"}`, "olcrtc_grant", false, true},
		{"panel wb token", http.MethodPost, "/mp/api/olcrtc/wbtoken", `{"token":"header.payload.signature"}`, "set_wbtoken", false, true},
		{"panel wb room", http.MethodPost, "/mp/api/olcrtc/wbroom", `{"login":"alice"}`, "request_wbroom", false, true},
		{"panel vkturn", http.MethodGet, "/mp/api/vkturn", "", "vkturn_state", false, false},
		{"panel vkturn update", http.MethodPost, "/mp/api/vkturn", `{"server":"vk.example"}`, "update_vkturn", false, true},
		{"panel vkturn enabled", http.MethodPost, "/mp/api/vkturn/enabled", `{"enabled":true}`, "set_vkturn_enabled", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			business.calls = nil
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.method == http.MethodPost {
				req.Header.Set("Idempotency-Key", "idem-"+strings.ReplaceAll(test.name, " ", "-"))
			}
			if test.admin {
				req.Header.Set("Authorization", "Bearer admin-secret")
			}
			if strings.HasPrefix(test.path, "/mp/api/") {
				req.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
				if test.panelWrite {
					req.Header.Set("X-CSRF", "panel-csrf")
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code == http.StatusServiceUnavailable {
				t.Fatalf("%s %s returned placeholder 503", test.method, test.path)
			}
			if !containsDispatchCall(business.calls, test.wantCall) {
				t.Fatalf("calls = %v, want %q", business.calls, test.wantCall)
			}
		})
	}
}

func TestControlPlanePanelWBRoomPropagatesReplacementKey(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/mp/api/olcrtc/wbroom", strings.NewReader(
		`{"login":" Alice ","action_key":"wb-action-key-1","replaces_action_key":" wb-action-key-0 "}`,
	))
	req.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
	req.Header.Set("X-CSRF", "panel-csrf")
	req.Header.Set("Idempotency-Key", "wb-idempotency-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	want := RequestWBRoomCommand{
		Login: " Alice ", ActionKey: "wb-action-key-1", ReplacesActionKey: " wb-action-key-0 ", IdempotencyKey: "wb-idempotency-1",
	}
	if business.wbRoomCommand != want {
		t.Fatalf("command = %#v, want %#v", business.wbRoomCommand, want)
	}
}

func TestControlPlanePanelWBRoomPreservesActionKeyHeaderFallback(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/mp/api/olcrtc/wbroom", strings.NewReader(`{"login":"alice"}`))
	req.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
	req.Header.Set("X-CSRF", "panel-csrf")
	req.Header.Set("Idempotency-Key", "wb-header-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if business.wbRoomCommand.ActionKey != "wb-header-key" || business.wbRoomCommand.ReplacesActionKey != "" {
		t.Fatalf("command = %#v", business.wbRoomCommand)
	}
}

func TestControlPlanePanelActionsDispatchCanonicalCommands(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	tests := []struct{ action, body, want string }{
		{"provision", `{"action":"provision","login":"alice","days":30}`, "provision"},
		{"extend", `{"action":"extend","login":"alice","days":30}`, "extend"},
		{"renew", `{"action":"renew","login":"alice","days":30}`, "renew"},
		{"set_expiry", `{"action":"set_expiry","login":"alice","expires":"2030-01-02T03:04:05Z"}`, "set_expiry"},
		{"reset_devices", `{"action":"reset_devices","login":"alice"}`, "reset_devices"},
		{"disable", `{"action":"disable","login":"alice"}`, "disable"},
		{"enable", `{"action":"enable","login":"alice"}`, "enable"},
		{"delete", `{"action":"delete","login":"alice"}`, "delete"},
		{"delete_expired", `{"action":"delete_expired"}`, "expiry_sweep"},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			business.calls = nil
			req := httptest.NewRequest(http.MethodPost, "/mp/api/action", strings.NewReader(test.body))
			req.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
			req.Header.Set("X-CSRF", "panel-csrf")
			req.Header.Set("Idempotency-Key", "action-"+test.action)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code == http.StatusServiceUnavailable {
				t.Fatalf("action %s returned placeholder 503", test.action)
			}
			if !containsDispatchCall(business.calls, test.want) {
				t.Fatalf("calls = %v, want %q", business.calls, test.want)
			}
		})
	}
}

func TestControlPlaneAuthAndIdempotencyBoundaries(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{
		AdminToken:        "admin-secret",
		PanelPath:         "/mp/",
		PanelPasswordHash: "configured",
	}).Handler()
	tests := []struct {
		name, path, body string
		auth, csrf, idem bool
		want             int
	}{
		{"admin auth", "/admin/provision", `{"login":"alice","days":30}`, false, false, true, http.StatusUnauthorized},
		{"admin idempotency", "/admin/provision", `{"login":"alice","days":30}`, true, false, false, http.StatusPreconditionRequired},
		{"panel csrf", "/mp/api/action", `{"action":"disable","login":"alice"}`, false, false, true, http.StatusForbidden},
		{"panel idempotency", "/mp/api/action", `{"action":"disable","login":"alice"}`, false, true, false, http.StatusPreconditionRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			if test.auth {
				req.Header.Set("Authorization", "Bearer admin-secret")
			}
			if strings.HasPrefix(test.path, "/mp/") {
				req.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
			}
			if test.csrf {
				req.Header.Set("X-CSRF", "panel-csrf")
			}
			if test.idem {
				req.Header.Set("Idempotency-Key", "boundary")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}
