package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"golang.org/x/crypto/bcrypt"
)

// TestVKTurnRedactedNeverLeaksSecrets is the security-critical assertion for the
// panel editor: the browser-facing view must expose only presence booleans for the
// per-login password and WG private key, never their values — while still returning
// the non-secret fields (server, hashes, peer public key, tunnel address).
func TestVKTurnRedactedNeverLeaksSecrets(t *testing.T) {
	vkStore := vkturnconf.NewInMemory()
	if err := vkStore.Set(validVKTurnConfig()); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	s := New(nil, nil, nil, nil, Config{VKTurn: vkStore})

	r := s.vkTurnRedacted()
	if r["configured"] != true || r["enabled"] != true {
		t.Fatalf("redacted top-level wrong: %#v", r)
	}
	clients, ok := r["clients"].(map[string]any)
	if !ok {
		t.Fatalf("clients missing: %#v", r)
	}
	for _, login := range []string{"wapmix", "wapmixx", "wapmix2"} {
		cl, ok := clients[login].(map[string]any)
		if !ok {
			t.Fatalf("client %q missing: %#v", login, clients)
		}
		if _, present := cl["password"]; present {
			t.Errorf("client %q: plaintext password leaked into redacted view", login)
		}
		if cl["password_set"] != true {
			t.Errorf("client %q: password_set should be true", login)
		}
		wg, ok := cl["wg"].(map[string]any)
		if !ok {
			t.Fatalf("client %q wg missing", login)
		}
		if _, present := wg["private_key"]; present {
			t.Errorf("client %q: WG private_key leaked into redacted view", login)
		}
		if wg["private_key_set"] != true {
			t.Errorf("client %q: private_key_set should be true", login)
		}
		if v, _ := wg["peer_public_key"].(string); v == "" {
			t.Errorf("client %q: peer_public_key (non-secret) should be shown", login)
		}
		if v, _ := wg["local_address"].(string); v == "" {
			t.Errorf("client %q: local_address should be shown", login)
		}
	}
}

// TestVKTurnRedactedUnconfigured reports configured=false (feature off) without panicking
// when the store has no config yet.
func TestVKTurnRedactedUnconfigured(t *testing.T) {
	s := New(nil, nil, nil, nil, Config{VKTurn: vkturnconf.NewInMemory()})
	r := s.vkTurnRedacted()
	if r["configured"] != false {
		t.Fatalf("unconfigured store should report configured=false: %#v", r)
	}
	if _, present := r["clients"]; present {
		t.Fatal("no clients should be exposed when unconfigured")
	}
}

// TestApplyVKTurnEditBlankKeepsSecrets unit-tests the merge: a blank/absent secret keeps
// the existing value; a provided non-secret field replaces it; first-time setup on nil.
func TestApplyVKTurnEditBlankKeepsSecrets(t *testing.T) {
	cur := validVKTurnConfig() // secrets set, server "wdtt.example:443"
	sp := func(s string) *string { return &s }
	var req vkTurnSaveReq
	req.Server = sp("newhost:56000")
	req.VKHashes = []string{"fresh"}
	req.Clients = map[string]vkTurnClientReq{}
	for _, l := range []string{"wapmix", "wapmixx", "wapmix2"} {
		c := vkTurnClientReq{Password: sp("")} // blank → keep
		c.WG.PrivateKey = sp("")               // blank → keep
		req.Clients[l] = c
	}
	got := applyVKTurnEdit(cur, req)
	if got.Server != "newhost:56000" || got.VKHashes[0] != "fresh" {
		t.Fatalf("non-secret fields not applied: %+v", got)
	}
	if got.Clients["wapmix"].Password != "pass-wapmix" {
		t.Fatalf("blank password overwrote the kept secret: %q", got.Clients["wapmix"].Password)
	}
	if got.Clients["wapmix"].WG.PrivateKey != vkTurnTestKey {
		t.Fatalf("blank private_key overwrote the kept secret")
	}
	// First-time setup: nil cur must not panic and must produce a Clients map.
	fresh := applyVKTurnEdit(nil, vkTurnSaveReq{Server: sp("h:1")})
	if fresh == nil || fresh.Server != "h:1" || fresh.Clients == nil {
		t.Fatalf("first-time merge wrong: %+v", fresh)
	}
}

// --- panel session helpers (login → cookie + CSRF, then authenticated POST) ---

func newPanelVKTurnServer(t *testing.T, cfg *VKTurnConfig) (*httptest.Server, *vkturnconf.Store) {
	t.Helper()
	vkStore := vkturnconf.NewInMemory()
	if cfg != nil {
		if err := vkStore.Set(cfg); err != nil {
			t.Fatalf("seed store: %v", err)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("testpw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil, nil, Config{PanelPath: "/mp/", PanelPasswordHash: string(hash), VKTurn: vkStore})
	return httptest.NewServer(s.Handler()), vkStore
}

func panelLogin(t *testing.T, srv *httptest.Server) (cookie, csrf string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": "testpw"})
	resp, err := http.Post(srv.URL+"/mp/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("panel login status = %d", resp.StatusCode)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "mp_session" {
			cookie = c.Value
		}
	}
	if cookie == "" || out.CSRF == "" {
		t.Fatalf("login missing session/csrf: cookie=%q csrf=%q", cookie, out.CSRF)
	}
	return cookie, out.CSRF
}

func panelPost(t *testing.T, srv *httptest.Server, path, cookie, csrf string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mp/"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF", csrf)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "mp_session", Value: cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPanelVKTurnSaveMergeToggleAndCSRF(t *testing.T) {
	srv, vkStore := newPanelVKTurnServer(t, validVKTurnConfig()) // enabled, secrets set
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	// Save with BLANK secrets + changed server/hash/min-version → secrets kept, fields applied.
	clients := map[string]any{}
	addrs := map[string]string{"wapmix": "10.77.0.2/32", "wapmixx": "10.77.0.3/32", "wapmix2": "10.77.0.4/32"}
	for l, a := range addrs {
		clients[l] = map[string]any{"password": "", "wg": map[string]any{
			"private_key": "", "peer_public_key": vkTurnTestKey, "local_address": a,
		}}
	}
	resp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{
		"min_version_code": 90200, "server": "newhost:56000", "vk_hashes": []string{"fresh-hash"}, "clients": clients,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("save status = %d: %s", resp.StatusCode, b)
	}
	_ = resp.Body.Close()
	got := vkStore.Get()
	if got.Server != "newhost:56000" || got.VKHashes[0] != "fresh-hash" || got.MinVersionCode != 90200 {
		t.Fatalf("save did not apply public fields: %+v", got)
	}
	if got.Clients["wapmix"].Password != "pass-wapmix" || got.Clients["wapmix"].WG.PrivateKey != vkTurnTestKey {
		t.Fatalf("blank secret overwrote kept value: %+v", got.Clients["wapmix"])
	}
	if !got.Enabled {
		t.Fatal("save must NOT flip enabled (no enabled field sent)")
	}

	// Master switch OFF.
	resp2 := panelPost(t, srv, "api/vkturn/enabled", cookie, csrf, map[string]any{"enabled": false})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("toggle status = %d", resp2.StatusCode)
	}
	_ = resp2.Body.Close()
	if vkStore.Get().Enabled {
		t.Fatal("toggle did not disable the transport")
	}

	// Missing CSRF on a write → 403 (proves the mutating routes are CSRF-guarded).
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mp/api/vkturn/enabled", strings.NewReader(`{"enabled":true}`))
	req.AddCookie(&http.Cookie{Name: "mp_session", Value: cookie})
	r3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d", r3.StatusCode)
	}
}

func TestPanelVKTurnRejectsPartialAndGuardsUnconfiguredToggle(t *testing.T) {
	srv, _ := newPanelVKTurnServer(t, nil) // unconfigured (in-memory, empty)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	// A partial first-time save fails validation → 400 (fail-closed, nothing persisted).
	resp := panelPost(t, srv, "api/vkturn", cookie, csrf, map[string]any{"server": "h:1"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("partial save should be 400, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Enabling before a full config exists → 400 with an actionable message.
	resp2 := panelPost(t, srv, "api/vkturn/enabled", cookie, csrf, map[string]any{"enabled": true})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("toggle on unconfigured should be 400, got %d", resp2.StatusCode)
	}
	_ = resp2.Body.Close()

	// Unauthenticated GET → 401 (session required even to read the redacted view).
	r3, err := http.Get(srv.URL + "/mp/api/vkturn")
	if err != nil {
		t.Fatal(err)
	}
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET should be 401, got %d", r3.StatusCode)
	}
}
