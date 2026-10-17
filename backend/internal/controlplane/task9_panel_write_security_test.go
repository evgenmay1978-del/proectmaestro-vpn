package controlplane_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceBusinessPanelWriteGuardAndStorageStatusSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	owner := seedF9PanelPrincipal(t, fixture, "owner-1", "owner")
	handler := f9PanelHandler(fixture)

	t.Run("bad csrf on write is forbidden", func(t *testing.T) {
		request := httptest.NewRequest(
			http.MethodPost,
			"/mp/api/action",
			strings.NewReader("{\"action\":\"reset_devices\",\"login\":\""+fixture.customerID+"\"}"),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF", "wrong-csrf")
		request.Header.Set("Idempotency-Key", "write-security-bad-csrf")
		request.AddCookie(&http.Cookie{Name: "mp_session", Value: owner.Cookie.Value})
		request.AddCookie(&http.Cookie{Name: "mp_csrf", Value: owner.CSRFToken})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, body = %s, want 403", response.Code, response.Body.String())
		}
	})

	t.Run("cluster storage outage is unavailable", func(t *testing.T) {
		fixture.database.setUnavailable(true)
		response := f9PanelGET(t, handler, "/mp/api/customers", owner)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body = %s, want 503", response.Code, response.Body.String())
		}
	})
}
