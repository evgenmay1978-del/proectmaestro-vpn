package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlPlanePanelSessionRoutesDispatchBusinessPort(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	tests := []struct {
		name, method, path, body, want string
		csrf                           bool
	}{
		{"login", http.MethodPost, "/mp/api/login", `{"password":"secret"}`, "create_session", false},
		{"logout", http.MethodPost, "/mp/api/logout", `{}`, "revoke_sessions", true},
		{"me", http.MethodGet, "/mp/api/me", "", "authorize", false},
		{"password", http.MethodPost, "/mp/api/password", `{"current":"secret","new":"new-secret"}`, "change_password", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			business.calls = nil
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.name != "login" {
				req.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
			}
			if test.method == http.MethodPost {
				req.Header.Set("Idempotency-Key", "session-"+test.name)
			}
			if test.csrf {
				req.Header.Set("X-CSRF", "panel-csrf")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code == http.StatusServiceUnavailable {
				t.Fatalf("%s returned placeholder 503", test.path)
			}
			if !containsDispatchCall(business.calls, test.want) {
				t.Fatalf("calls = %v, want %q", business.calls, test.want)
			}
		})
	}
}
