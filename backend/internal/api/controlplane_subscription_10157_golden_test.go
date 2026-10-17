package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestControlPlaneSubscription10157BareGoldenDoesNotAugment(t *testing.T) {
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	source := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
	business := newSubscriptionReviewBusiness(&now, source)
	handler := NewControlPlane(business, Config{EnforceDeviceLimit: true}).Handler()
	options := subscriptionReviewOptions(subscriptionEndpointBase)
	expectedBody, expectedType, err := renderControlPlaneSubscription(source.state.Customer, business.cfg.SubscriptionTopology, options)
	if err != nil {
		t.Fatalf("render ordinary expectation: %v", err)
	}
	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/sub/review-token?device=review-device", nil)
		req.Header.Set("User-Agent", subscriptionReviewOptions(subscriptionEndpointBase).UserAgent)
		handler.ServeHTTP(response, req)
		return response
	}
	fresh := request()
	freshSnapshotCalls := source.snapshotCall
	source.snapshotErr = controlplane.ErrUnavailable
	cached := request()
	if fresh.Code != http.StatusOK || cached.Code != fresh.Code || fresh.Header().Get("Content-Type") != expectedType || cached.Header().Get("Content-Type") != fresh.Header().Get("Content-Type") || string(fresh.Body.Bytes()) != string(expectedBody) || string(cached.Body.Bytes()) != string(expectedBody) {
		t.Fatalf("fresh/cached golden mismatch: fresh=%d %q %q cached=%d %q %q", fresh.Code, fresh.Header().Get("Content-Type"), fresh.Body.Bytes(), cached.Code, cached.Header().Get("Content-Type"), cached.Body.Bytes())
	}
	if source.snapshotCall != freshSnapshotCalls+1 {
		t.Fatalf("outage snapshot calls=%d, want one failed source probe before cache LKG after %d fresh calls", source.snapshotCall, freshSnapshotCalls)
	}
	for _, response := range []*httptest.ResponseRecorder{fresh, cached} {
		for _, header := range []string{"Content-Encoding", "Etag", "Content-Length"} {
			if got := response.Header().Get(header); got != "" {
				t.Fatalf("%s=%q, want absent", header, got)
			}
		}
		for _, forbidden := range []string{"Maestro CDN", "wl:", "mlkem", "cdn"} {
			if strings.Contains(strings.ToLower(response.Body.String()), strings.ToLower(forbidden)) {
				t.Fatalf("bare response leaked %q: %q", forbidden, response.Body.Bytes())
			}
		}
	}
	if source.claimCalls != 1 {
		t.Fatalf("claim calls=%d, want one admission followed by cache LKG", source.claimCalls)
	}
}
