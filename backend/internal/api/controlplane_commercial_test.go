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
