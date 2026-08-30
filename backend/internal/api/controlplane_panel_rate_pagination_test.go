package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type panelRatePaginationBusiness struct {
	panelPermissionBusiness
	denyScope         string
	rateCalls         []RateLimitCommand
	customers         []CustomerView
	orders            []OrderView
	events            []AuditView
	lastCustomerLimit int
}

func (business *panelRatePaginationBusiness) ConsumeRateLimit(_ context.Context, command RateLimitCommand) (RateLimitView, error) {
	business.rateCalls = append(business.rateCalls, command)
	if command.Scope == business.denyScope {
		return RateLimitView{Allowed: false, RetryAfterSeconds: 17}, nil
	}
	return RateLimitView{Allowed: true}, nil
}

func (business *panelRatePaginationBusiness) ListCustomers(_ context.Context, filter CustomerFilter) ([]CustomerView, error) {
	business.lastCustomerLimit = filter.Limit
	result := make([]CustomerView, 0, len(business.customers))
	for _, customer := range business.customers {
		if filter.AfterLogin != "" && (customer.Login < filter.AfterLogin ||
			(customer.Login == filter.AfterLogin && customer.CustomerID <= filter.AfterCustomerID)) {
			continue
		}
		result = append(result, customer)
		if filter.Limit > 0 && len(result) == filter.Limit {
			break
		}
	}
	return result, nil
}

func (business *panelRatePaginationBusiness) ListOrders(_ context.Context, filter OrderFilter) ([]OrderView, error) {
	result := make([]OrderView, 0, len(business.orders))
	for _, order := range business.orders {
		if filter.AfterCreatedAtUnix > 0 && (order.CreatedAtUnix > filter.AfterCreatedAtUnix ||
			(order.CreatedAtUnix == filter.AfterCreatedAtUnix && order.OrderID >= filter.AfterOrderID)) {
			continue
		}
		result = append(result, order)
		if filter.Limit > 0 && len(result) == filter.Limit {
			break
		}
	}
	return result, nil
}

func (business *panelRatePaginationBusiness) RecentAudit(_ context.Context, filter AuditFilter) ([]AuditView, error) {
	result := make([]AuditView, 0, len(business.events))
	for _, event := range business.events {
		unix := event.CreatedAt.Unix()
		if filter.AfterCreatedAtUnix > 0 && (unix > filter.AfterCreatedAtUnix ||
			(unix == filter.AfterCreatedAtUnix && event.ID >= filter.AfterID)) {
			continue
		}
		result = append(result, event)
		if filter.Limit > 0 && len(result) == filter.Limit {
			break
		}
	}
	return result, nil
}

func TestControlPlanePanelPaginationIsStableBoundedAndOpaque(t *testing.T) {
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	business := &panelRatePaginationBusiness{
		customers: []CustomerView{
			{CustomerID: "customer-a", Login: "alice"},
			{CustomerID: "customer-b", Login: "bob"},
			{CustomerID: "customer-c", Login: "charlie"},
		},
		orders: []OrderView{
			{OrderID: "order-c", Status: "pending", CreatedAtUnix: now.Unix()},
			{OrderID: "order-b", Status: "pending", CreatedAtUnix: now.Add(-time.Minute).Unix()},
			{OrderID: "order-a", Status: "pending", CreatedAtUnix: now.Add(-2 * time.Minute).Unix()},
		},
		events: []AuditView{
			{ID: "audit-c", Action: "c", CreatedAt: now},
			{ID: "audit-b", Action: "b", CreatedAt: now.Add(-time.Minute)},
			{ID: "audit-a", Action: "a", CreatedAt: now.Add(-2 * time.Minute)},
		},
	}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()

	t.Run("customers", func(t *testing.T) {
		first := panelPaginationGET(t, handler, "/mp/api/customers?limit=2")
		var page struct {
			Customers  []CustomerView `json:"customers"`
			NextCursor string         `json:"next_cursor"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if first.Code != http.StatusOK || len(page.Customers) != 2 || page.Customers[0].Login != "alice" || page.Customers[1].Login != "bob" || page.NextCursor == "" {
			t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
		}
		if strings.Contains(page.NextCursor, "bob") || strings.Contains(first.Body.String(), "customer-b") {
			t.Fatalf("cursor/internal ID is not opaque: %s", first.Body.String())
		}
		second := panelPaginationGET(t, handler, "/mp/api/customers?limit=2&cursor="+url.QueryEscape(page.NextCursor))
		page = struct {
			Customers  []CustomerView `json:"customers"`
			NextCursor string         `json:"next_cursor"`
		}{}
		if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if second.Code != http.StatusOK || len(page.Customers) != 1 || page.Customers[0].Login != "charlie" || page.NextCursor != "" {
			t.Fatalf("second page status=%d body=%s", second.Code, second.Body.String())
		}
		_ = panelPaginationGET(t, handler, "/mp/api/customers?limit=10000")
		if business.lastCustomerLimit <= 0 || business.lastCustomerLimit > 201 {
			t.Fatalf("backend page limit=%d, want bounded lookahead", business.lastCustomerLimit)
		}
	})

	t.Run("orders", func(t *testing.T) {
		first := panelPaginationGET(t, handler, "/mp/api/orders?limit=2")
		var page struct {
			Orders     []OrderView `json:"orders"`
			NextCursor string      `json:"next_cursor"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if first.Code != http.StatusOK || len(page.Orders) != 2 || page.NextCursor == "" {
			t.Fatalf("first orders page status=%d body=%s", first.Code, first.Body.String())
		}
		second := panelPaginationGET(t, handler, "/mp/api/orders?limit=2&cursor="+url.QueryEscape(page.NextCursor))
		page = struct {
			Orders     []OrderView `json:"orders"`
			NextCursor string      `json:"next_cursor"`
		}{}
		if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if second.Code != http.StatusOK || len(page.Orders) != 1 || page.Orders[0].OrderID != "order-a" || page.NextCursor != "" {
			t.Fatalf("second orders page status=%d body=%s", second.Code, second.Body.String())
		}
	})

	t.Run("audit", func(t *testing.T) {
		first := panelPaginationGET(t, handler, "/mp/api/audit?limit=2")
		var page struct {
			Events     []AuditView `json:"events"`
			NextCursor string      `json:"next_cursor"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if first.Code != http.StatusOK || len(page.Events) != 2 || page.NextCursor == "" {
			t.Fatalf("first audit page status=%d body=%s", first.Code, first.Body.String())
		}
		second := panelPaginationGET(t, handler, "/mp/api/audit?limit=2&cursor="+url.QueryEscape(page.NextCursor))
		page = struct {
			Events     []AuditView `json:"events"`
			NextCursor string      `json:"next_cursor"`
		}{}
		if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if second.Code != http.StatusOK || len(page.Events) != 1 || page.Events[0].ID != "audit-a" || page.NextCursor != "" {
			t.Fatalf("second audit page status=%d body=%s", second.Code, second.Body.String())
		}
	})

	if response := panelPaginationGET(t, handler, "/mp/api/customers?cursor=not-a-valid-cursor"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestControlPlanePanelUsesPerIPAndPerActorClusterRateLimits(t *testing.T) {
	loginBusiness := &panelRatePaginationBusiness{denyScope: "panel.login.ip"}
	loginHandler := NewControlPlane(loginBusiness, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	loginRequest := httptest.NewRequest(http.MethodPost, "/mp/api/login", strings.NewReader(`{"password":"secret"}`))
	loginRequest.RemoteAddr = "127.0.0.1:5000"
	loginRequest.Header.Set("X-Real-IP", "198.51.100.9")
	loginResponse := httptest.NewRecorder()
	loginHandler.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusTooManyRequests || loginResponse.Header().Get("Retry-After") != "17" {
		t.Fatalf("login status=%d retry=%q body=%s", loginResponse.Code, loginResponse.Header().Get("Retry-After"), loginResponse.Body.String())
	}
	if len(loginBusiness.rateCalls) != 1 || loginBusiness.rateCalls[0].Key != "198.51.100.9" {
		t.Fatalf("login rate calls=%+v, want trusted X-Real-IP", loginBusiness.rateCalls)
	}

	actorBusiness := &panelRatePaginationBusiness{denyScope: "panel.read.actor"}
	actorHandler := NewControlPlane(actorBusiness, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	actorResponse := panelPaginationGET(t, actorHandler, "/mp/api/customers")
	if actorResponse.Code != http.StatusTooManyRequests || actorResponse.Header().Get("Retry-After") != "17" {
		t.Fatalf("actor status=%d retry=%q body=%s", actorResponse.Code, actorResponse.Header().Get("Retry-After"), actorResponse.Body.String())
	}
	var sawIP, sawActor bool
	for _, call := range actorBusiness.rateCalls {
		sawIP = sawIP || call.Scope == "panel.read.ip"
		sawActor = sawActor || (call.Scope == "panel.read.actor" && call.Key == "owner")
	}
	if !sawIP || !sawActor {
		t.Fatalf("rate calls=%+v, want per-IP and per-actor checks", actorBusiness.rateCalls)
	}
}

func panelPaginationGET(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "127.0.0.1:5000"
	request.Header.Set("X-Real-IP", "198.51.100.10")
	request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
	request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
