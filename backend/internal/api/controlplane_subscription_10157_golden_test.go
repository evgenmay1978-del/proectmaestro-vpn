package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlPlaneSubscription10157BareGoldenDoesNotAugment(t *testing.T) {
	body := []byte("vless://ordinary-10157")
	handler := NewControlPlane(subscriptionEndpointBusiness{snapshot: SubscriptionSnapshot{
		Customer: CustomerView{Login: "alice", Active: true, Expires: time.Now().Add(time.Hour)},
		Document: body,
	}}, Config{}).Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/subscription-token", nil).WithContext(context.Background()))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" || string(response.Body.Bytes()) != string(body) {
		t.Fatalf("bare golden status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.Bytes())
	}
	for _, header := range []string{"Content-Encoding", "Etag", "Content-Length"} {
		if got := response.Header().Get(header); got != "" {
			t.Fatalf("%s=%q, want absent", header, got)
		}
	}
	for _, forbidden := range []string{"Maestro CDN", "wl:", "cdn", "credential"} {
		if strings.Contains(string(response.Body.Bytes()), forbidden) {
			t.Fatalf("bare response leaked %q: %q", forbidden, response.Body.Bytes())
		}
	}
}
