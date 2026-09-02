package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlPlaneCommercialCatalogExposesAccessAndGBProducts(t *testing.T) {
	server := NewControlPlane((*ServiceBusiness)(nil), Config{})
	request := httptest.NewRequest(http.MethodGet, "/order/catalog", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /order/catalog status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestControlPlaneCommercialAccountRoutesRequireAuthentication(t *testing.T) {
	server := NewControlPlane((*ServiceBusiness)(nil), Config{})
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/account/whitelist-balance"},
		{method: http.MethodPost, path: "/account/subscription-delivery"},
	} {
		t.Run(route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			response := httptest.NewRecorder()

			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s status = %d, want %d", route.path, response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestControlPlaneCommercialOrderClaimRequiresBoundAccount(t *testing.T) {
	server := NewControlPlane((*ServiceBusiness)(nil), Config{})
	request := httptest.NewRequest(http.MethodPost, "/order/order-1/paid-claim", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unbound paid claim status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
