package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlPlaneCommercialPublicationRequiresAdminAuthentication(t *testing.T) {
	server := NewControlPlane((*ServiceBusiness)(nil), Config{AdminToken: "test-admin-token"})
	request := httptest.NewRequest(http.MethodPost, "/admin/accounts/account-1/whitelist-publication", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated publication status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestControlPlaneCommercialOrderCallbacksRequireAdminAuthentication(t *testing.T) {
	server := NewControlPlane((*ServiceBusiness)(nil), Config{AdminToken: "test-admin-token"})
	for _, path := range []string{
		"/admin/order/order-1/confirm",
		"/admin/order/order-1/reject",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			response := httptest.NewRecorder()

			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s status = %d, want %d", path, response.Code, http.StatusUnauthorized)
			}
		})
	}
}
