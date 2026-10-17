package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type subscriptionEndpointBusiness struct {
	Business
	snapshot SubscriptionSnapshot
}

func (b subscriptionEndpointBusiness) SubscriptionSnapshot(context.Context, string) (SubscriptionSnapshot, error) {
	return b.snapshot, nil
}

func TestControlPlaneSubscriptionInfoAndHelpersKeepLegacyContract(t *testing.T) {
	t.Parallel()
	expires := time.Now().Add(2 * time.Hour).UTC()
	handler := NewControlPlane(subscriptionEndpointBusiness{snapshot: SubscriptionSnapshot{
		Customer: CustomerView{Login: "alice", Expires: expires, Active: true},
		Document: []byte(`{"outbounds":[]}`),
	}}, Config{}).Handler()

	info := httptest.NewRecorder()
	handler.ServeHTTP(info, httptest.NewRequest(http.MethodGet, "/sub/subscription-token/info", nil))
	if info.Code != http.StatusOK {
		t.Fatalf("info status = %d, want %d", info.Code, http.StatusOK)
	}
	if info.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("info cache control = %q, want no-store", info.Header().Get("Cache-Control"))
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(info.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if len(got) != 4 || got["login"] == nil || got["expires"] == nil || got["days_left"] == nil || got["active"] == nil {
		t.Fatalf("info fields = %s, want exactly login/expires/days_left/active", info.Body.String())
	}
	var daysLeft int
	if err := json.Unmarshal(got["days_left"], &daysLeft); err != nil || daysLeft != 1 {
		t.Fatalf("days_left = %s, want 1", got["days_left"])
	}

	helpers := httptest.NewRecorder()
	handler.ServeHTTP(helpers, httptest.NewRequest(http.MethodGet, "/sub/subscription-token/helpers", nil))
	if helpers.Code != http.StatusOK {
		t.Fatalf("helpers status = %d, want %d", helpers.Code, http.StatusOK)
	}
	if helpers.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("helpers content type = %q, want application/json", helpers.Header().Get("Content-Type"))
	}
	if helpers.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("helpers cache control = %q, want no-store", helpers.Header().Get("Cache-Control"))
	}
	if helpers.Body.String() != "{}\n" {
		t.Fatalf("helpers body = %q, want {}", helpers.Body.String())
	}
}

func TestControlPlaneSubscriptionRejectsInactiveOrExpiredButKeepsInfo(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	tests := []struct {
		name     string
		customer CustomerView
		wantDays int
	}{
		{name: "inactive", customer: CustomerView{Login: "inactive", Expires: now.Add(2 * time.Hour), Active: false}, wantDays: 1},
		{name: "expired", customer: CustomerView{Login: "expired", Expires: now.Add(-time.Hour), Active: true}, wantDays: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewControlPlane(subscriptionEndpointBusiness{snapshot: SubscriptionSnapshot{
				Customer: test.customer,
				Document: []byte(`{"outbounds":[]}`),
			}}, Config{}).Handler()

			info := httptest.NewRecorder()
			handler.ServeHTTP(info, httptest.NewRequest(http.MethodGet, "/sub/subscription-token/info", nil))
			if info.Code != http.StatusOK {
				t.Fatalf("info status = %d, want %d", info.Code, http.StatusOK)
			}
			if info.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("info cache control = %q, want no-store", info.Header().Get("Cache-Control"))
			}
			var infoBody struct {
				DaysLeft int  `json:"days_left"`
				Active   bool `json:"active"`
			}
			if err := json.Unmarshal(info.Body.Bytes(), &infoBody); err != nil {
				t.Fatalf("decode info: %v", err)
			}
			if infoBody.Active || infoBody.DaysLeft != test.wantDays {
				t.Fatalf("info = %#v, want active=false days_left=%d", infoBody, test.wantDays)
			}

			for _, suffix := range []string{"", "/helpers"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/subscription-token"+suffix, nil))
				if response.Code != http.StatusPaymentRequired {
					t.Fatalf("%q status = %d, want %d", suffix, response.Code, http.StatusPaymentRequired)
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("%q cache control = %q, want no-store", suffix, response.Header().Get("Cache-Control"))
				}
			}
		})
	}
}
