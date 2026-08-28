package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type recordingBusiness struct {
	Business
	extendCalls int
	renewCalls  int
	mutations   int
}

func (b *recordingBusiness) ExtendCustomer(context.Context, ExtendCustomerCommand) (CustomerView, error) {
	b.extendCalls++
	b.mutations++
	return CustomerView{}, nil
}

func (b *recordingBusiness) RenewCustomer(context.Context, RenewCustomerCommand) (CustomerView, error) {
	b.renewCalls++
	b.mutations++
	return CustomerView{}, nil
}

func TestRQLiteRouteActionSnapshotIsComplete(t *testing.T) {
	business := &recordingBusiness{}
	handler := NewControlPlane(business, Config{
		AdminToken:        "sek",
		PanelPath:         "/mp/",
		PanelPasswordHash: "bootstrap-hash",
		UpdateDir:         "enabled",
		ReportDir:         "enabled",
	}).Handler()

	matcher, ok := handler.(interface {
		Handler(*http.Request) (http.Handler, string)
	})
	if !ok {
		t.Fatalf("control-plane handler %T does not expose ServeMux route matching", handler)
	}

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/sub/token"},
		{http.MethodGet, "/sub/token/info"},
		{http.MethodGet, "/sub/token/helpers"},
		{http.MethodPost, "/claim"},
		{http.MethodGet, "/order/tariffs"},
		{http.MethodPost, "/order"},
		{http.MethodGet, "/order/id"},
		{http.MethodPost, "/order/paid-claim"},
		{http.MethodPost, "/trial"},
		{http.MethodGet, "/update/update.json"},
		{http.MethodPost, "/report"},
		{http.MethodPost, "/admin/provision"},
		{http.MethodPost, "/admin/extend"},
		{http.MethodPost, "/admin/renew"},
		{http.MethodPost, "/admin/set-expiry"},
		{http.MethodPost, "/admin/reset-devices"},
		{http.MethodGet, "/admin/customer"},
		{http.MethodPost, "/admin/backfill-anytls"},
		{http.MethodPost, "/admin/backfill-s3"},
		{http.MethodPost, "/admin/backfill-s4"},
		{http.MethodPost, "/admin/bulk-import"},
		{http.MethodPost, "/admin/migrate-anytls-s2"},
		{http.MethodPost, "/admin/order/confirm"},
		{http.MethodPost, "/admin/order/cancel"},
		{http.MethodGet, "/admin/olcrtc"},
		{http.MethodPost, "/admin/olcrtc/room"},
		{http.MethodGet, "/mp/"},
		{http.MethodPost, "/mp/api/login"},
		{http.MethodPost, "/mp/api/logout"},
		{http.MethodGet, "/mp/api/me"},
		{http.MethodPost, "/mp/api/password"},
		{http.MethodGet, "/mp/api/customers"},
		{http.MethodGet, "/mp/api/customer"},
		{http.MethodGet, "/mp/api/stats"},
		{http.MethodPost, "/mp/api/action"},
		{http.MethodGet, "/mp/api/olcrtc"},
		{http.MethodPost, "/mp/api/olcrtc/room"},
		{http.MethodPost, "/mp/api/olcrtc/login"},
		{http.MethodPost, "/mp/api/olcrtc/wbtoken"},
		{http.MethodPost, "/mp/api/olcrtc/wbroom"},
		{http.MethodGet, "/mp/api/vkturn"},
		{http.MethodPost, "/mp/api/vkturn/enabled"},
	}
	for _, route := range routes {
		req := httptest.NewRequest(route.method, route.path, nil)
		_, pattern := matcher.Handler(req)
		if pattern == "" {
			t.Errorf("missing control-plane route %s %s", route.method, route.path)
		}
	}

	wantActions := []string{
		"delete", "delete_expired", "disable", "enable", "extend",
		"provision", "renew", "reset_devices", "set_expiry",
	}
	gotActions := controlPlanePanelActions()
	sort.Strings(gotActions)
	if !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("control-plane panel actions = %v, want %v", gotActions, wantActions)
	}
}

func TestExtendAndRenewRemainDistinct(t *testing.T) {
	business := &recordingBusiness{}
	handler := NewControlPlane(business, Config{AdminToken: "sek"}).Handler()

	if got := postControlPlaneAdmin(handler, "/admin/extend", `{"login":"alice","days":30}`); got != http.StatusOK {
		t.Fatalf("extend status = %d, want 200", got)
	}
	if got := postControlPlaneAdmin(handler, "/admin/renew", `{"login":"alice","days":30}`); got != http.StatusOK {
		t.Fatalf("renew status = %d, want 200", got)
	}
	if business.extendCalls != 1 || business.renewCalls != 1 {
		t.Fatalf("calls extend=%d renew=%d, want 1/1", business.extendCalls, business.renewCalls)
	}
}

func TestRQLiteBulkImportReturns410WithoutMutation(t *testing.T) {
	business := &recordingBusiness{}
	handler := NewControlPlane(business, Config{AdminToken: "sek"}).Handler()

	if got := postControlPlaneAdmin(handler, "/admin/bulk-import", `{"logins":["alice"]}`); got != http.StatusGone {
		t.Fatalf("bulk import status = %d, want 410", got)
	}
	if business.mutations != 0 {
		t.Fatalf("bulk import invoked %d business mutation(s), want 0", business.mutations)
	}
}

func postControlPlaneAdmin(handler http.Handler, path, body string) int {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sek")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-key-00000001")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}
