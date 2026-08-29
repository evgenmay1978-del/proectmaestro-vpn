package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type publicIdempotencyBusiness struct {
	*dispatchBusiness
	keys            map[string][]string
	derivations     map[string]string
	derivationCalls map[string]int
	orderIDs        []string
}

func newPublicIdempotencyBusiness() *publicIdempotencyBusiness {
	return &publicIdempotencyBusiness{
		dispatchBusiness: &dispatchBusiness{},
		keys:             make(map[string][]string),
		derivations:      make(map[string]string),
		derivationCalls:  make(map[string]int),
	}
}

func (business *publicIdempotencyBusiness) record(path, key string) {
	business.keys[path] = append(business.keys[path], key)
}

func (business *publicIdempotencyBusiness) LegacyPublicIdempotencyKey(route string, values ...string) (string, error) {
	business.derivationCalls[route]++
	identity := fmt.Sprintf("%q", append([]string{route}, values...))
	if key := business.derivations[identity]; key != "" {
		return key, nil
	}
	key := fmt.Sprintf("legacy-public-v1:%064x", len(business.derivations)+1)
	business.derivations[identity] = key
	return key, nil
}

func (business *publicIdempotencyBusiness) CreateOrder(_ context.Context, command CreateOrderCommand) (OrderView, error) {
	business.record("/order", command.IdempotencyKey)
	orderID := fmt.Sprintf("ord-%d", len(business.orderIDs)+1)
	business.orderIDs = append(business.orderIDs, orderID)
	return OrderView{OrderID: orderID, Status: "pending"}, nil
}

func (business *publicIdempotencyBusiness) MarkPaymentClaimed(_ context.Context, command ClaimPaymentCommand) (OrderView, error) {
	business.record("/order/paid-claim", command.IdempotencyKey)
	return OrderView{OrderID: command.OrderID, Status: "pending"}, nil
}

func (business *publicIdempotencyBusiness) RedeemTrial(_ context.Context, command RedeemTrialCommand) (CustomerView, error) {
	business.record("/trial", command.IdempotencyKey)
	return CustomerView{Login: command.Login, Active: true}, nil
}

func (business *publicIdempotencyBusiness) TouchDevice(_ context.Context, command TouchDeviceCommand) (DeviceDecision, error) {
	business.record("/claim", command.IdempotencyKey)
	return DeviceDecision{Allowed: true}, nil
}

func TestControlPlaneInstalledAppKeylessMutationsKeepLegacyReplaySemantics(t *testing.T) {
	business := newPublicIdempotencyBusiness()
	handler := NewControlPlane(business, Config{}).Handler()
	tests := []struct {
		name, path, body, differentBody string
		stableIdentity                  bool
	}{
		{name: "claim", path: "/claim", body: `{"code":"alice","device":"device-1"}`, differentBody: `{"code":"alice","device":"device-2"}`, stableIdentity: true},
		{name: "trial", path: "/trial", body: `{"nick":"alice","anchor":"anchor-1","device":"device-1"}`, differentBody: `{"nick":"alice","anchor":"anchor-2","device":"device-1"}`, stableIdentity: true},
		{name: "create order", path: "/order", body: `{"tariff":"month"}`},
		{name: "claim payment", path: "/order/paid-claim", body: `{"order_id":"ord-1"}`, differentBody: `{"order_id":"ord-2"}`, stableIdentity: true},
	}

	stableKeys := make(map[string]struct{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for attempt := 0; attempt < 2; attempt++ {
				request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("attempt %d status=%d body=%q", attempt+1, response.Code, response.Body.String())
				}
			}

			keys := business.keys[test.path]
			if len(keys) != 2 {
				t.Fatalf("forwarded keys=%q, want two calls", keys)
			}
			if !test.stableIdentity {
				if keys[0] != "" || keys[1] != "" {
					t.Fatalf("anonymous order keys=%q, want empty keys for new-intent semantics", keys)
				}
				if len(business.orderIDs) != 2 || business.orderIDs[0] == business.orderIDs[1] {
					t.Fatalf("anonymous order IDs=%q, want two distinct intents", business.orderIDs)
				}
				if business.derivationCalls[test.path] != 0 {
					t.Fatalf("anonymous order invoked stable key deriver %d times", business.derivationCalls[test.path])
				}
				return
			}
			if keys[0] == "" || keys[0] != keys[1] {
				t.Fatalf("stable keyless replay keys=%q, want one deterministic non-empty key", keys)
			}
			if !strings.HasPrefix(keys[0], "legacy-public-v1:") {
				t.Fatalf("derived key=%q, want versioned compatibility namespace", keys[0])
			}
			for _, rawIdentity := range []string{"alice", "anchor-1", "device-1", "ord-1"} {
				if strings.Contains(keys[0], rawIdentity) {
					t.Fatalf("derived key %q leaks raw identity %q", keys[0], rawIdentity)
				}
			}
			if _, duplicate := stableKeys[keys[0]]; duplicate {
				t.Fatalf("derived key %q is not route/domain separated", keys[0])
			}
			stableKeys[keys[0]] = struct{}{}

			differentRequest := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.differentBody))
			differentResponse := httptest.NewRecorder()
			handler.ServeHTTP(differentResponse, differentRequest)
			if differentResponse.Code != http.StatusOK {
				t.Fatalf("different identity status=%d body=%q", differentResponse.Code, differentResponse.Body.String())
			}
			keys = business.keys[test.path]
			if len(keys) != 3 || keys[2] == keys[0] {
				t.Fatalf("different identity keys=%q, want distinct derived key", keys)
			}
			if business.derivationCalls[test.path] != 3 {
				t.Fatalf("key deriver calls=%d, want 3", business.derivationCalls[test.path])
			}
		})
	}
}

func TestControlPlanePublicMutationsPreserveExplicitIdempotencyKeys(t *testing.T) {
	business := newPublicIdempotencyBusiness()
	handler := NewControlPlane(business, Config{}).Handler()
	const explicitKey = "  client-key:/!?  "
	for _, test := range []struct {
		path, body string
	}{
		{path: "/claim", body: `{"code":"alice","device":"device-1"}`},
		{path: "/trial", body: `{"nick":"alice","anchor":"anchor-1","device":"device-1"}`},
		{path: "/order", body: `{"tariff":"month"}`},
		{path: "/order/paid-claim", body: `{"order_id":"ord-1"}`},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Idempotency-Key", explicitKey)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%q", test.path, response.Code, response.Body.String())
		}
		keys := business.keys[test.path]
		if len(keys) != 1 || keys[0] != explicitKey {
			t.Fatalf("%s forwarded keys=%q, want explicit key unchanged", test.path, keys)
		}
	}
	if len(business.derivationCalls) != 0 {
		t.Fatalf("explicit keys unexpectedly invoked compatibility deriver: %#v", business.derivationCalls)
	}
}

func TestControlPlaneStableKeylessMutationFailsClosedWithoutDeriver(t *testing.T) {
	business := &dispatchBusiness{}
	handler := NewControlPlane(business, Config{}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/trial", strings.NewReader(`{"nick":"alice","anchor":"anchor-1","device":"device-1"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing deriver status=%d body=%q, want 503", response.Code, response.Body.String())
	}
	if containsDispatchCall(business.calls, "redeem_trial") {
		t.Fatal("missing deriver reached trial mutation")
	}
}

func TestControlPlanePublicCompatibilityDoesNotRelaxAuthenticatedWrites(t *testing.T) {
	business := newPublicIdempotencyBusiness()
	handler := NewControlPlane(business, Config{
		AdminToken:        "admin-secret",
		PanelPath:         "/mp/",
		PanelPasswordHash: "configured",
	}).Handler()

	admin := httptest.NewRequest(http.MethodPost, "/admin/provision", strings.NewReader(`{"login":"alice","days":30}`))
	admin.Header.Set("Authorization", "Bearer admin-secret")
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, admin)
	if adminResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("keyless admin status=%d body=%q", adminResponse.Code, adminResponse.Body.String())
	}

	panel := httptest.NewRequest(http.MethodPost, "/mp/api/action", strings.NewReader(`{"action":"disable","login":"alice"}`))
	panel.AddCookie(&http.Cookie{Name: "mp_session", Value: "panel-session"})
	panel.Header.Set("X-CSRF", "panel-csrf")
	panelResponse := httptest.NewRecorder()
	handler.ServeHTTP(panelResponse, panel)
	if panelResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("keyless panel status=%d body=%q", panelResponse.Code, panelResponse.Body.String())
	}
}
