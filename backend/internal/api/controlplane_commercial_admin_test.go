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
