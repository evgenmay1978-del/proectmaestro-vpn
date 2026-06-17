package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// fakeProv implements Provisioner over the real store (so /sub works after).
type fakeProv struct{ st *store.Store }

func (f *fakeProv) Provision(login string, dur time.Duration) (*store.Customer, error) {
	c := &store.Customer{Login: login, SubToken: "tok-" + login, Expires: time.Now().Add(dur),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"}}
	return c, f.st.Put(c)
}

func (f *fakeProv) Extend(login string, dur time.Duration) (*store.Customer, error) {
	return f.st.Extend(login, dur)
}

func (f *fakeProv) SetExpiry(login string, t time.Time) (*store.Customer, error) {
	return f.st.SetExpiry(login, t)
}

func (f *fakeProv) ActivateExisting(login string) (*store.Customer, error) {
	return nil, store.ErrNotFound
}

func adminServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, _ := store.Open(filepath.Join(t.TempDir(), "s.json"))
	srv := New(st, &fakeProv{st: st}, nil, Config{AdminToken: "sek", SubBaseURL: "https://wapmixx.ru:8910"})
	return httptest.NewServer(srv.Handler()), st
}

func TestAdminProvisionThenSubWorks(t *testing.T) {
	srv, _ := adminServer(t)
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"login": "dave", "days": 30})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/provision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sek")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("provision status = %d", resp.StatusCode)
	}
	var out customerResp
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.SubURL != "https://wapmixx.ru:8910/sub/tok-dave" || !out.Active {
		t.Fatalf("bad provision response: %+v", out)
	}

	// the issued sub URL must now serve a config
	r2, _ := http.Get(srv.URL + "/sub/tok-dave")
	defer func() { _ = r2.Body.Close() }()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("sub after provision = %d, want 200", r2.StatusCode)
	}
}

// TestAdminRenew: the cross-channel /admin/renew endpoint extends an existing account,
// and 404s for a login that is neither in the store nor backfillable from a bot panel.
func TestAdminRenew(t *testing.T) {
	srv, st := adminServer(t)
	defer srv.Close()
	post := func(path, body string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer sek")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("/admin/provision", `{"login":"alice","days":30}`); code != http.StatusOK {
		t.Fatalf("seed provision = %d", code)
	}
	before, _ := st.ByLogin("alice")
	if code := post("/admin/renew", `{"login":"alice","days":30}`); code != http.StatusOK {
		t.Fatalf("renew existing = %d, want 200", code)
	}
	after, _ := st.ByLogin("alice")
	if !after.Expires.After(before.Expires) {
		t.Fatalf("renew did not extend: before %v after %v", before.Expires, after.Expires)
	}
	// not in store and fake ActivateExisting returns ErrNotFound → 404
	if code := post("/admin/renew", `{"login":"ghost","days":30}`); code != http.StatusNotFound {
		t.Fatalf("renew unknown = %d, want 404", code)
	}
}

func TestAdminRequiresToken(t *testing.T) {
	srv, _ := adminServer(t)
	defer srv.Close()
	body, _ := json.Marshal(map[string]any{"login": "x", "days": 1})
	resp, _ := http.Post(srv.URL+"/admin/provision", "application/json", bytes.NewReader(body))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token provision = %d, want 401", resp.StatusCode)
	}
}
