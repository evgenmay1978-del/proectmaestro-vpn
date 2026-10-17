package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type panelCompatibilityBusiness struct {
	dispatchBusiness
	authorize []AuthorizeCommand
}

func (b *panelCompatibilityBusiness) Authorize(_ context.Context, command AuthorizeCommand) (PrincipalView, error) {
	b.authorize = append(b.authorize, command)
	b.called("authorize")
	return PrincipalView{ID: "owner", Permissions: []string{"settings.critical"}}, nil
}

func TestControlPlanePanelSPAAddsCSRFAndIdempotencyHeaders(t *testing.T) {
	required := []string{
		"if(CSRF)opts.headers['X-CSRF']=CSRF",
		"opts.headers['Idempotency-Key']",
		"crypto.randomUUID",
	}
	for _, fragment := range required {
		if !strings.Contains(panelHTML, fragment) {
			t.Errorf("panel request helper missing %q", fragment)
		}
	}
}

func TestControlPlanePanelMeKeepsLegacyReloadContract(t *testing.T) {
	handler := NewControlPlane(&dispatchBusiness{}, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/mp/api/me", nil)
	request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
	request.AddCookie(&http.Cookie{Name: "mp_csrf", Value: "panel-csrf"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["logged_in"] != true || body["csrf"] != "panel-csrf" {
		t.Fatalf("body=%v, want legacy logged_in/csrf reload contract", body)
	}
}

func TestControlPlanePanelLegacyCustomerDTOFields(t *testing.T) {
	typeOfCustomer := reflect.TypeOf(CustomerView{})
	for _, field := range []string{"DaysLeft", "Devices", "LastSeen"} {
		if _, ok := typeOfCustomer.FieldByName(field); !ok {
			t.Errorf("CustomerView missing %s", field)
		}
	}

	handler := NewControlPlane(&dispatchBusiness{}, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	tests := []struct {
		path string
		keys []string
	}{
		{path: "/mp/api/customers", keys: []string{"days_left", "devices"}},
		{path: "/mp/api/customer?login=alice", keys: []string{"device_ids", "device_limit", "traffic_bytes"}},
		{path: "/mp/api/stats", keys: []string{"expiring_7d", "devices"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, key := range test.keys {
				if !strings.Contains(body, `"`+key+`"`) {
					t.Errorf("body=%s missing %q", body, key)
				}
			}
		})
	}
}

func TestControlPlanePanelProvisionUsesDelegatedPermission(t *testing.T) {
	business := &panelCompatibilityBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/mp/api/action", strings.NewReader(`{"action":"provision","login":"alice","days":30}`))
	request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
	request.Header.Set("X-CSRF", "panel-csrf")
	request.Header.Set("Idempotency-Key", "panel-provision-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if len(business.authorize) != 1 || business.authorize[0].Permission != "customer.provision" {
		t.Fatalf("authorize=%v, want customer.provision", business.authorize)
	}
}

func TestControlPlanePanelRegistersOwnerOperationRoutes(t *testing.T) {
	handler := NewControlPlane(&dispatchBusiness{}, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	matcher := handler.(interface {
		Handler(*http.Request) (http.Handler, string)
	})
	for _, route := range []string{
		"/mp/api/orders",
		"/mp/api/order/confirm",
		"/mp/api/order/cancel",
		"/mp/api/cluster-status",
		"/mp/api/audit",
	} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		_, pattern := matcher.Handler(request)
		if pattern != route {
			t.Errorf("route %q matched %q", route, pattern)
		}
	}
}
