package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPanelActionMalformedRequestUsesWritePreflightBeforeDecode(t *testing.T) {
	business := &panelRatePaginationBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	request := httptest.NewRequest(http.MethodPost, "/mp/api/action", strings.NewReader("{"))
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("X-Real-IP", "198.51.100.44")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want auth preflight 401 before JSON decode", response.Code, response.Body.String())
	}
	if len(business.rateCalls) != 1 || business.rateCalls[0].Scope != "panel.write.ip" || business.rateCalls[0].Key != "198.51.100.44" {
		t.Fatalf("rate calls=%+v, want exactly one write IP preflight", business.rateCalls)
	}
	if len(business.authorize) != 0 {
		t.Fatalf("authorize=%+v, malformed request without session must not reach authorization", business.authorize)
	}
}

func TestControlPlanePanelCursorIsEncryptedTamperEvidentAndClusterStable(t *testing.T) {
	customers := []CustomerView{
		{CustomerID: "customer-a", Login: "alice"},
		{CustomerID: "customer-b", Login: "bob"},
	}
	cfg := Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}
	firstHandler := NewControlPlane(&panelRatePaginationBusiness{customers: customers}, cfg).Handler()
	first := panelPaginationGET(t, firstHandler, "/mp/api/customers?limit=1")
	var firstPage struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if first.Code != http.StatusOK || firstPage.NextCursor == "" {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}

	raw, err := base64.RawURLEncoding.DecodeString(firstPage.NextCursor)
	if err != nil || len(raw) < 2 {
		t.Fatalf("decode cursor: len=%d err=%v", len(raw), err)
	}
	if raw[0] != controlPlanePanelCursorVersion || json.Valid(raw) {
		t.Fatalf("cursor envelope version=%d json=%t, want versioned binary envelope", raw[0], json.Valid(raw))
	}
	plaintext := string(raw)
	for _, secret := range []string{"customer-a", "alice"} {
		if strings.Contains(plaintext, secret) {
			t.Fatalf("cursor leaks plaintext %q: %q", secret, plaintext)
		}
	}

	tamperedRaw := append([]byte(nil), raw...)
	tamperedRaw[len(tamperedRaw)-1] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(tamperedRaw)
	if response := panelPaginationGET(t, firstHandler, "/mp/api/customers?limit=1&cursor="+url.QueryEscape(tampered)); response.Code != http.StatusBadRequest {
		t.Fatalf("tampered cursor status=%d body=%s, want 400", response.Code, response.Body.String())
	}

	secondHandler := NewControlPlane(&panelRatePaginationBusiness{customers: customers}, cfg).Handler()
	second := panelPaginationGET(t, secondHandler, "/mp/api/customers?limit=1&cursor="+url.QueryEscape(firstPage.NextCursor))
	var secondPage struct {
		Customers []panelCustomerView `json:"customers"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPage); err != nil {
		t.Fatal(err)
	}
	if second.Code != http.StatusOK || len(secondPage.Customers) != 1 || secondPage.Customers[0].Login != "bob" {
		t.Fatalf("second HA node status=%d body=%s", second.Code, second.Body.String())
	}

	differentHandler := NewControlPlane(
		&panelRatePaginationBusiness{customers: customers},
		Config{PanelPath: "/mp/", PanelPasswordHash: "different"},
	).Handler()
	if response := panelPaginationGET(t, differentHandler, "/mp/api/customers?limit=1&cursor="+url.QueryEscape(firstPage.NextCursor)); response.Code != http.StatusBadRequest {
		t.Fatalf("different-key cursor status=%d body=%s, want 400", response.Code, response.Body.String())
	}
}

func TestPanelActionResetAndDeleteReturnTruthfulShapes(t *testing.T) {
	business := &panelPermissionBusiness{}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()

	reset := panelReviewAction(t, handler, `{"action":"reset_devices","login":"alice"}`, "reset-shape-1")
	var resetBody struct {
		OK       bool              `json:"ok"`
		Customer panelCustomerView `json:"customer"`
	}
	if err := json.Unmarshal(reset.Body.Bytes(), &resetBody); err != nil {
		t.Fatal(err)
	}
	if reset.Code != http.StatusOK || !resetBody.OK || resetBody.Customer.Login != "alice" {
		t.Fatalf("reset status=%d body=%s, want refreshed alice customer", reset.Code, reset.Body.String())
	}

	deleted := panelReviewAction(t, handler, `{"action":"delete","login":"alice"}`, "delete-shape-1")
	var deletedBody map[string]json.RawMessage
	if err := json.Unmarshal(deleted.Body.Bytes(), &deletedBody); err != nil {
		t.Fatal(err)
	}
	var deletedOK bool
	if err := json.Unmarshal(deletedBody["ok"], &deletedOK); err != nil {
		t.Fatal(err)
	}
	if deleted.Code != http.StatusOK || !deletedOK {
		t.Fatalf("delete status=%d body=%s, want ok=true", deleted.Code, deleted.Body.String())
	}
	if _, exists := deletedBody["customer"]; exists {
		t.Fatalf("delete returned a fabricated customer: %s", deleted.Body.String())
	}
}

func panelReviewAction(t *testing.T, handler http.Handler, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mp/api/action", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF", "panel-csrf")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
