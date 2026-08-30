package controlplane_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestServiceBusinessPanelReloadUsesCSRFCookieSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	session := seedF9PanelPrincipal(t, fixture, "f9-owner", "owner")
	handler := f9PanelHandler(fixture)

	response := f9PanelGET(t, handler, "/mp/api/me", session)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var body struct {
		LoggedIn    bool     `json:"logged_in"`
		CSRF        string   `json:"csrf"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.LoggedIn || body.CSRF != session.CSRFToken {
		t.Fatalf("body=%+v, want logged-in session with exact csrf", body)
	}
	for _, permission := range []string{"customer.read", "customer.provision", "payment.decide", "settings.critical"} {
		if !containsF9String(body.Permissions, permission) {
			t.Errorf("permissions=%v missing %q", body.Permissions, permission)
		}
	}
	customers := f9PanelGET(t, handler, "/mp/api/customers", session)
	if customers.Code != http.StatusOK {
		t.Fatalf("protected GET after reload status=%d body=%q", customers.Code, customers.Body.String())
	}
}

func TestServiceBusinessPanelLegacyDTOsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	session := seedF9PanelPrincipal(t, fixture, "f9-owner", "owner")
	lastSeen := fixture.startedAt.Add(-5 * time.Minute).Unix()
	fixture.sqlite.must(t,
		rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix)
VALUES(?,?,?,?,?,0,?)`, Args: []any{"f9-device-active", fixture.customerID, fixture.box.LookupHMAC("f9-device", []byte("active")), "android", lastSeen, fixture.startedAt.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix)
VALUES(?,?,?,?,?,1,?)`, Args: []any{"f9-device-revoked", fixture.customerID, fixture.box.LookupHMAC("f9-device", []byte("revoked")), "android", lastSeen + 60, fixture.startedAt.Unix()}},
	)
	handler := f9PanelHandler(fixture)

	customersResponse := f9PanelGET(t, handler, "/mp/api/customers", session)
	if customersResponse.Code != http.StatusOK {
		t.Fatalf("customers status=%d body=%q", customersResponse.Code, customersResponse.Body.String())
	}
	var customers struct {
		Customers []struct {
			Login    string     `json:"login"`
			DaysLeft int        `json:"days_left"`
			Devices  int        `json:"devices"`
			LastSeen *time.Time `json:"last_seen"`
		} `json:"customers"`
	}
	if err := json.Unmarshal(customersResponse.Body.Bytes(), &customers); err != nil {
		t.Fatal(err)
	}
	if len(customers.Customers) != 1 {
		t.Fatalf("customers=%+v", customers.Customers)
	}
	customer := customers.Customers[0]
	if customer.DaysLeft <= 0 || customer.Devices != 1 || customer.LastSeen == nil || customer.LastSeen.Unix() != lastSeen {
		t.Fatalf("customer=%+v, want positive days, one active device and max active last_seen", customer)
	}

	detailResponse := f9PanelGET(t, handler, "/mp/api/customer?login="+url.QueryEscape(fixture.customerID), session)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%q", detailResponse.Code, detailResponse.Body.String())
	}
	var detail struct {
		DeviceIDs   map[string]string `json:"device_ids"`
		DeviceLimit int               `json:"device_limit"`
		Traffic     int64             `json:"traffic_bytes"`
	}
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.DeviceIDs) != 1 || detail.DeviceIDs["f9-device-active"] == "" || detail.DeviceLimit != 2 || detail.Traffic != 0 {
		t.Fatalf("detail=%+v, want one active device, limit 2 and canonical zero traffic", detail)
	}

	statsResponse := f9PanelGET(t, handler, "/mp/api/stats", session)
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%q", statsResponse.Code, statsResponse.Body.String())
	}
	var stats struct {
		Total   int `json:"total"`
		Devices int `json:"devices"`
	}
	if err := json.Unmarshal(statsResponse.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Devices != 1 {
		t.Fatalf("stats=%+v, want total=1 devices=1", stats)
	}
}

func TestServiceBusinessPanelRBACSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	owner := seedF9PanelPrincipal(t, fixture, "f9-owner", "owner")
	admin := seedF9PanelPrincipal(t, fixture, "f9-admin", "admin")
	handler := f9PanelHandler(fixture)

	if response := f9PanelGET(t, handler, "/mp/api/customers", admin); response.Code != http.StatusOK {
		t.Fatalf("admin read status=%d body=%q", response.Code, response.Body.String())
	}
	if response := f9PanelGET(t, handler, "/mp/api/orders", admin); response.Code != http.StatusForbidden {
		t.Fatalf("admin owner-route status=%d body=%q, want 403", response.Code, response.Body.String())
	}
	if response := f9PanelGET(t, handler, "/mp/api/orders", owner); response.Code != http.StatusOK {
		t.Fatalf("owner orders status=%d body=%q", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/mp/api/orders", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing session status=%d body=%q, want 401", response.Code, response.Body.String())
	}
}

func seedF9PanelPrincipal(t *testing.T, fixture *f5SubscriptionFixture, principalID, role string) controlplane.SessionResult {
	t.Helper()
	fixture.sqlite.must(t,
		rqlite.Statement{SQL: `INSERT INTO principals(principal_id,login_key_hmac,status,revocation_epoch,created_at_unix)
VALUES(?,?,'active',0,?)`, Args: []any{principalID, fixture.box.LookupHMAC("f9-principal", []byte(principalID)), fixture.startedAt.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO principal_roles(principal_id,role_name,granted_at_unix) VALUES(?,?,?)`, Args: []any{principalID, role, fixture.startedAt.Unix()}},
	)
	session, err := fixture.service.CreateSession(context.Background(), principalID)
	if err != nil {
		t.Fatalf("create %s session: %v", role, err)
	}
	return session
}

func f9PanelHandler(fixture *f5SubscriptionFixture) http.Handler {
	business := api.NewServiceBusiness(fixture.service, fixture.config)
	return api.NewControlPlane(business, api.Config{
		PanelPath: "/mp/", PanelPasswordHash: "configured", EnforceDeviceLimit: true,
	}).Handler()
}

func f9PanelGET(t *testing.T, handler http.Handler, path string, session controlplane.SessionResult) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: "mp_session", Value: session.Cookie.Value})
	request.AddCookie(&http.Cookie{Name: "mp_csrf", Value: session.CSRFToken})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func containsF9String(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
