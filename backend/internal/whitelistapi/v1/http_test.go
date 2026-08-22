package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

type fixtureReader struct {
	fixtures  Fixtures
	calls     []string
	ledgerReq PageRequest
	auditReq  PageRequest
	err       error
}

func (reader *fixtureReader) Entitlement(_ context.Context, accountID string) (Entitlement, error) {
	reader.calls = append(reader.calls, "entitlement:"+accountID)
	return reader.fixtures.Entitlement, reader.err
}

func (reader *fixtureReader) Health(_ context.Context, accountID string) (Health, error) {
	reader.calls = append(reader.calls, "health:"+accountID)
	return reader.fixtures.Health, reader.err
}

func (reader *fixtureReader) Usage(_ context.Context, accountID string) (Usage, error) {
	reader.calls = append(reader.calls, "usage:"+accountID)
	return reader.fixtures.Usage, reader.err
}

func (reader *fixtureReader) Ledger(_ context.Context, accountID string, page PageRequest) (Page[LedgerEntry], error) {
	reader.calls = append(reader.calls, "ledger:"+accountID)
	reader.ledgerReq = page
	return reader.fixtures.Ledger, reader.err
}

func (reader *fixtureReader) Audit(_ context.Context, accountID string, page PageRequest) (Page[AuditRecord], error) {
	reader.calls = append(reader.calls, "audit:"+accountID)
	reader.auditReq = page
	return reader.fixtures.Audit, reader.err
}

func TestHandlerServesAuthenticatedVersionedReadContracts(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	reader := &fixtureReader{fixtures: validFixtures(now)}
	handler, err := NewHandler(Config{BearerToken: testToken, Reader: reader})
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{"entitlement", "health", "usage", "ledger?limit=25&cursor=next_1", "audit"}
	for _, suffix := range paths {
		req := privateRequest(http.MethodGet, BasePath+"/accounts/acct_1/"+suffix, testToken)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, body=%q", suffix, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET %s Cache-Control=%q", suffix, got)
		}
		var envelope struct {
			APIVersion string          `json:"api_version"`
			Data       json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil || envelope.APIVersion != Version || len(envelope.Data) == 0 {
			t.Fatalf("GET %s invalid envelope: version=%q data=%s err=%v", suffix, envelope.APIVersion, envelope.Data, err)
		}
		body := strings.ToLower(res.Body.String())
		for _, forbidden := range []string{"origin_ip", "subscription_uri", "client_encryption", "xray_identity"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("GET %s leaked forbidden field %q: %s", suffix, forbidden, body)
			}
		}
	}

	if reader.ledgerReq != (PageRequest{Limit: 25, Cursor: "next_1"}) {
		t.Fatalf("ledger page request = %#v", reader.ledgerReq)
	}
	if reader.auditReq != (PageRequest{Limit: DefaultPageSize}) {
		t.Fatalf("audit default page request = %#v", reader.auditReq)
	}
}

func TestHandlerFailsClosedForNetworkAndBearerAuth(t *testing.T) {
	reader := &fixtureReader{fixtures: validFixtures(time.Now().UTC())}
	if _, err := NewHandler(Config{Reader: reader}); err == nil {
		t.Fatal("empty bearer token accepted")
	}
	if _, err := NewHandler(Config{BearerToken: "short", Reader: reader}); err == nil {
		t.Fatal("short bearer token accepted")
	}
	handler, err := NewHandler(Config{BearerToken: testToken, Reader: reader})
	if err != nil {
		t.Fatal(err)
	}
	path := BasePath + "/accounts/acct_1/usage"

	remote := privateRequest(http.MethodGet, path, testToken)
	remote.RemoteAddr = "198.51.100.7:443"
	remote.Header.Set("X-Forwarded-For", "127.0.0.1")
	assertStatus(t, handler, remote, http.StatusNotFound)
	assertStatus(t, handler, privateRequest(http.MethodGet, path, ""), http.StatusUnauthorized)
	assertStatus(t, handler, privateRequest(http.MethodGet, path, strings.Repeat("x", len(testToken))), http.StatusUnauthorized)
	if len(reader.calls) != 0 {
		t.Fatalf("unauthorized request reached provider: %v", reader.calls)
	}
}

func TestHandlerValidatesMethodPathAndPaginationBeforeProvider(t *testing.T) {
	reader := &fixtureReader{fixtures: validFixtures(time.Now().UTC())}
	handler, err := NewHandler(Config{BearerToken: testToken, Reader: reader})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, BasePath + "/accounts/acct_1/usage", http.StatusMethodNotAllowed},
		{http.MethodGet, BasePath + "/accounts/acct_1/control", http.StatusNotFound},
		{http.MethodGet, BasePath + "/accounts/acct%2F2/usage", http.StatusNotFound},
		{http.MethodGet, BasePath + "/accounts/acct_1/usage?cursor=x", http.StatusBadRequest},
		{http.MethodGet, BasePath + "/accounts/acct_1/ledger?limit=101", http.StatusBadRequest},
		{http.MethodGet, BasePath + "/accounts/acct_1/audit?unknown=x", http.StatusBadRequest},
	} {
		assertStatus(t, handler, privateRequest(tc.method, tc.path, testToken), tc.want)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("invalid request reached provider: %v", reader.calls)
	}
}

func TestHandlerRejectsCrossAccountOrInvalidProviderDataWithoutPartialJSON(t *testing.T) {
	fixtures := validFixtures(time.Now().UTC())
	fixtures.Entitlement.AccountID = "acct_2"
	reader := &fixtureReader{fixtures: fixtures}
	handler, err := NewHandler(Config{BearerToken: testToken, Reader: reader})
	if err != nil {
		t.Fatal(err)
	}
	req := privateRequest(http.MethodGet, BasePath+"/accounts/acct_1/entitlement", testToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("invalid provider data status=%d body=%q", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "acct_2") || strings.Contains(res.Body.String(), "data") {
		t.Fatalf("provider data leaked in error: %q", res.Body.String())
	}

	reader.err = errors.New("secret origin failure")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || strings.Contains(res.Body.String(), "secret origin") {
		t.Fatalf("provider error leaked: status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestHandlerRejectsProviderPagesBeyondRequestedLimit(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	for _, resource := range []string{"ledger", "audit"} {
		t.Run(resource, func(t *testing.T) {
			fixtures := validFixtures(now)
			switch resource {
			case "ledger":
				second := fixtures.Ledger.Items[0]
				second.ID = "ledger_2"
				second.EventID = "event_2"
				fixtures.Ledger.Items = append(fixtures.Ledger.Items, second)
			case "audit":
				second := fixtures.Audit.Items[0]
				second.ID = "audit_2"
				fixtures.Audit.Items = append(fixtures.Audit.Items, second)
			}

			reader := &fixtureReader{fixtures: fixtures}
			handler, err := NewHandler(Config{BearerToken: testToken, Reader: reader})
			if err != nil {
				t.Fatal(err)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, privateRequest(http.MethodGet, BasePath+"/accounts/acct_1/"+resource+"?limit=1", testToken))
			if res.Code != http.StatusBadGateway {
				t.Fatalf("provider page beyond requested limit status=%d body=%q", res.Code, res.Body.String())
			}
			if strings.Contains(res.Body.String(), "_2") || strings.Contains(res.Body.String(), "\"data\"") {
				t.Fatalf("provider page leaked in error: %q", res.Body.String())
			}
		})
	}
}

func privateRequest(method, target, token string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:43123"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func assertStatus(t *testing.T, handler http.Handler, req *http.Request, want int) {
	t.Helper()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != want {
		t.Fatalf("%s %s = %d, want %d; body=%q", req.Method, req.URL.String(), res.Code, want, res.Body.String())
	}
}

func validFixtures(now time.Time) Fixtures {
	amount := ExactAmount{Numerator: "125", Denominator: 2, Currency: "RUB"}
	return Fixtures{
		Entitlement: Entitlement{
			ID: "ent_1", AccountID: "acct_1", State: EntitlementActive,
			TransportProfileID: "profile_1", CompatibilityPresetID: "preset_1",
			TransportReleaseID: "release_1", BillingEnabled: true, UpdatedAt: now,
		},
		Health: Health{
			AccountID: "acct_1", Status: HealthHealthy, CollectorStatus: HealthHealthy,
			XrayStatus: HealthHealthy, DataPlaneReleaseID: "release_1", Fresh: true, ObservedAt: now,
		},
		Usage: Usage{
			AccountID: "acct_1", EntitlementID: "ent_1", BillingPeriodID: "period_1",
			Unit: UnitGBDecimal, Basis: BasisDownlinkOnly, MeasuredBytes: 1000,
			IncludedBytes: 100, BillableBytes: 900, RemainingIncludedBytes: 0,
			SoftLimitBytes: 2000, HardLimitBytes: 3000, GraceBytes: 100,
			AccruedAmount: amount, UpdatedAt: now,
		},
		Ledger: Page[LedgerEntry]{Items: []LedgerEntry{{
			ID: "ledger_1", EventID: "event_1", AccountID: "acct_1", EntitlementID: "ent_1",
			BillingPeriodID: "period_1", BillableBytes: 900, Unit: UnitGBDecimal,
			Basis: BasisDownlinkOnly, PriceSource: PriceTariff, Amount: amount, OccurredAt: now,
		}}, NextCursor: "ledger_next"},
		Audit: Page[AuditRecord]{Items: []AuditRecord{{
			ID: "audit_1", AccountID: "acct_1", ActorID: "admin_1",
			Action: "ENTITLEMENT_ENABLED", Reason: "owner request", OccurredAt: now,
			Changes: []AuditChange{{Field: "state", OldValue: "DISABLED", NewValue: "ACTIVE"}},
		}}},
	}
}
