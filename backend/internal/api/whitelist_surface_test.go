package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func TestWhiteListControlAndStatsContractsAreNotMountedPublicly(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := New(st, nil, nil, nil, Config{AdminToken: "0123456789abcdef0123456789abcdef"}).Handler()
	paths := []string{
		"/internal/white-list/v1/accounts/acct_1/entitlement",
		"/internal/white-list/v1/accounts/acct_1/health",
		"/internal/white-list/v1/accounts/acct_1/usage",
		"/internal/white-list/v1/accounts/acct_1/ledger",
		"/internal/white-list/v1/accounts/acct_1/audit",
		"/api/white-list/v1/accounts/acct_1/usage",
		"/admin/white-list/v1/control",
		"/stats/white-list",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d, want 404", method, path, res.Code)
			}
		}
	}
}
