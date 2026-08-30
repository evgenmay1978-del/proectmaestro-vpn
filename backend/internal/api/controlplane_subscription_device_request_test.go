package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type requestAwareSubscriptionBusiness struct {
	Business
	snapshot SubscriptionSnapshot
	calls    []subscriptionRenderOptions
}

func (business *requestAwareSubscriptionBusiness) SubscriptionSnapshot(context.Context, string) (SubscriptionSnapshot, error) {
	return business.snapshot, nil
}

func (business *requestAwareSubscriptionBusiness) subscriptionSnapshotForRequest(_ context.Context, _ string, options subscriptionRenderOptions) (SubscriptionSnapshot, error) {
	business.calls = append(business.calls, options)
	return business.snapshot, nil
}

func TestControlPlaneSubscriptionRequestAwareSourceCoversAllEndpointKinds(t *testing.T) {
	business := &requestAwareSubscriptionBusiness{snapshot: SubscriptionSnapshot{
		Customer: CustomerView{Login: "alice", Active: true, Expires: time.Now().Add(time.Hour)},
		Document: []byte(`{}`),
	}}
	handler := NewControlPlane(business, Config{EnforceDeviceLimit: true}).Handler()

	for _, path := range []string{
		"/sub/subscription-token?device=device-1",
		"/sub/subscription-token/helpers?device=device-1",
		"/sub/subscription-token/info?device=device-1",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%q, want %d", path, response.Code, response.Body.String(), http.StatusOK)
		}
	}

	if len(business.calls) != 3 {
		t.Fatalf("request-aware calls=%d, want one coherent request per base/helpers/info endpoint", len(business.calls))
	}
}
