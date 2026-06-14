package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return httptest.NewServer(New(st, nil, nil, Config{}).Handler()), st
}

func TestSubActiveCustomer(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()
	_ = st.Put(&store.Customer{
		Login: "alice", SubToken: "tok-alice", Expires: time.Now().Add(24 * time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "wapmixx.ru", Port: 443, UUID: "u"},
		Hy2:   &subgen.Hy2Creds{Server: "wapmix.duckdns.org", Port: 8443, User: "alice", Pass: "p", Insecure: true},
	})

	resp, err := http.Get(srv.URL + "/sub/tok-alice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var cfg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("response not valid sing-box JSON: %v", err)
	}
	if _, ok := cfg["outbounds"]; !ok {
		t.Fatalf("config missing outbounds: %v", cfg)
	}
}

func TestSubExpired(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()
	_ = st.Put(&store.Customer{
		Login: "bob", SubToken: "tok-bob", Expires: time.Now().Add(-time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"},
	})
	resp, _ := http.Get(srv.URL + "/sub/tok-bob")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("expired status = %d, want 402", resp.StatusCode)
	}
}

func TestSubUnknownToken(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/sub/nope")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token status = %d, want 404", resp.StatusCode)
	}
}

func TestClaimReturnsSubURL(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()
	_ = st.Put(&store.Customer{
		Login: "MAESTRO-7F3K", SubToken: "tok-x", Expires: time.Now().Add(24 * time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"},
		Hy2:   &subgen.Hy2Creds{Server: "s2", Port: 8443, User: "u", Pass: "p", Insecure: true},
	})
	resp, err := http.Post(srv.URL+"/claim", "application/json", strings.NewReader(`{"code":"MAESTRO-7F3K"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		SubURL string `json:"sub_url"`
		Active bool   `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasSuffix(got.SubURL, "/sub/tok-x") {
		t.Fatalf("sub_url = %q, want suffix /sub/tok-x", got.SubURL)
	}
	if !got.Active {
		t.Fatalf("want active customer")
	}
}

func TestClaimUnknownCode(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	resp, _ := http.Post(srv.URL+"/claim", "application/json", strings.NewReader(`{"code":"nope"}`))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown claim status = %d, want 404", resp.StatusCode)
	}
}

func TestClaimRejectsGET(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/claim")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /claim status = %d, want 405", resp.StatusCode)
	}
}

func TestRenewExtendsThenServes(t *testing.T) {
	srv, st := newTestServer(t)
	defer srv.Close()
	_ = st.Put(&store.Customer{Login: "carol", SubToken: "tok-c", Expires: time.Now().Add(-time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"}})
	if _, err := st.Extend("carol", 30*24*time.Hour); err != nil {
		t.Fatalf("Extend: %v", err)
	}
	resp, _ := http.Get(srv.URL + "/sub/tok-c")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after renew status = %d, want 200", resp.StatusCode)
	}
}
