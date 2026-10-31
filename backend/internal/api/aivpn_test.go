package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/aivpn"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// aivpnUA is what the app sends once it ships AIVPN: appVersionCode parses the
// number in parentheses, so 108 here.
const aivpnUA = "SFA/1.0.108 (108; sing-box 1.14.0-alpha.31; language ru_RU)"

func testAivpnKey(t *testing.T) string {
	t.Helper()
	key, err := aivpn.Build(aivpn.Key{
		Server:    "203.0.113.10:443", // TEST-NET-3, never a real node
		ServerKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		PSK:       base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
		ClientIP:  "10.77.0.2",
		Net: &aivpn.NetConfig{
			ClientIP:      "10.77.0.2",
			ServerVPNIP:   "10.77.0.1",
			PrefixLen:     24,
			MTU:           1280,
			MDHLen:        20,
			KeepaliveSecs: 8,
		},
	})
	if err != nil {
		t.Fatalf("aivpn.Build: %v", err)
	}
	return key
}

// staticAivpnSource builds a real aivpn.Static source (file-backed pilot mode)
// whitelisting logins with the same key. minVC is the shipped app versionCode.
func staticAivpnSource(t *testing.T, logins []string, minVC int) *aivpn.Static {
	t.Helper()
	key := testAivpnKey(t)
	cfg := aivpn.Config{Enabled: true, MinVersionCode: minVC, Logins: logins, Keys: map[string]string{}}
	for _, l := range logins {
		cfg.Keys[l] = key
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal aivpn config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "aivpn.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write aivpn config: %v", err)
	}
	src, err := aivpn.OpenStatic(path)
	if err != nil {
		t.Fatalf("aivpn.OpenStatic: %v", err)
	}
	if src == nil {
		t.Fatal("aivpn.OpenStatic returned nil for an enabled config")
	}
	return src
}

// aivpnFixture is the shared setup: a store with an eligible login (alice), a
// non-whitelisted one (bob) and an expired one (carol), and a server whose
// AIVPN source is on. Returns the store so callers can build their own server
// with a different Config where needed.
func aivpnFixture(t *testing.T) (*store.Store, *aivpn.Static, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	src := staticAivpnSource(t, []string{"alice"}, 108)
	key := testAivpnKey(t)
	for _, c := range []*store.Customer{
		{Login: "alice", SubToken: "tok-alice", Expires: time.Now().Add(24 * time.Hour)},
		{Login: "bob", SubToken: "tok-bob", Expires: time.Now().Add(24 * time.Hour)},
		{Login: "carol", SubToken: "tok-carol", Expires: time.Now().Add(-time.Hour)},
	} {
		c.VLESS = &subgen.VLESSCreds{Server: "wapmixx.ru", Port: 443, UUID: "u"}
		if err := st.Put(c); err != nil {
			t.Fatalf("store.Put(%s): %v", c.Login, err)
		}
	}
	return st, src, key
}

func getWithUA(t *testing.T, url, ua string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest %s: %v", url, err)
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", url, err)
	}
	return resp.StatusCode, string(b), resp.Header
}

// TestAivpnEndpointGates: GET /aivpn/<token> hands out the key ONLY for an active,
// whitelisted login of our app at a new enough versionCode — and answers with the
// same opaque 403 for every other gate failure so a token holder cannot probe the
// configuration.
func TestAivpnEndpointGates(t *testing.T) {
	st, src, key := aivpnFixture(t)
	srv := httptest.NewServer(New(st, nil, nil, nil, Config{AIVPN: src}).Handler())
	defer srv.Close()

	status, body, hdr := getWithUA(t, srv.URL+"/aivpn/tok-alice", aivpnUA)
	if status != http.StatusOK {
		t.Fatalf("eligible: status %d, want 200 (body %q)", status, body)
	}
	if got := hdr.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := hdr.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode /aivpn body %q: %v", body, err)
	}
	if out.Key != key {
		t.Errorf("served key %q, want the configured key %q", out.Key, key)
	}
	if _, err := aivpn.Parse(out.Key); err != nil {
		t.Errorf("served key does not parse back: %v", err)
	}

	tests := []struct {
		name   string
		path   string
		ua     string
		status int
	}{
		{"curl has no versionCode", "/aivpn/tok-alice", "curl/8.5.0", http.StatusForbidden},
		{"version too old", "/aivpn/tok-alice", "SFA/1.0.107 (107; sing-box 1.14.0-alpha.31; language ru_RU)", http.StatusForbidden},
		{"login not whitelisted", "/aivpn/tok-bob", aivpnUA, http.StatusForbidden},
		{"expired subscription", "/aivpn/tok-carol", aivpnUA, http.StatusPaymentRequired},
		{"unknown token", "/aivpn/tok-nope", aivpnUA, http.StatusNotFound},
		{"empty token", "/aivpn/", aivpnUA, http.StatusNotFound},
		{"sub-path", "/aivpn/tok-alice/extra", aivpnUA, http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := getWithUA(t, srv.URL+tc.path, tc.ua)
			if status != tc.status {
				t.Errorf("status %d, want %d (body %q)", status, tc.status, body)
			}
			if strings.Contains(body, "aivpn://") {
				t.Errorf("a rejected request leaked a key: %q", body)
			}
		})
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/aivpn/tok-alice", nil)
	if err != nil {
		t.Fatalf("NewRequest POST: %v", err)
	}
	req.Header.Set("User-Agent", aivpnUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /aivpn: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want GET", got)
	}
}

// TestAivpnRouteAbsentWhenOff: the default Config (nil source) must behave exactly
// like before the feature: no /aivpn route and no AIVPN hint in /info.
func TestAivpnRouteAbsentWhenOff(t *testing.T) {
	st, _, key := aivpnFixture(t)
	srv := httptest.NewServer(New(st, nil, nil, nil, Config{}).Handler())
	defer srv.Close()

	status, body, _ := getWithUA(t, srv.URL+"/aivpn/tok-alice", aivpnUA)
	if status != http.StatusNotFound {
		t.Errorf("/aivpn with feature off: status %d, want 404 (body %q)", status, body)
	}
	status, body, _ = getWithUA(t, srv.URL+"/sub/tok-alice/info", aivpnUA)
	if status != http.StatusOK {
		t.Fatalf("/info status %d, want 200", status)
	}
	if strings.Contains(body, "aivpn") || strings.Contains(body, key) {
		t.Errorf("/info leaked AIVPN while the feature is off: %q", body)
	}
}

// TestAivpnInfoFeatureFlag: /info advertises the AIVPN key only when the gates pass,
// and then as features.aivpn=true plus the key itself; everyone else's /info stays
// byte-for-byte free of the feature.
func TestAivpnInfoFeatureFlag(t *testing.T) {
	st, src, key := aivpnFixture(t)
	srv := httptest.NewServer(New(st, nil, nil, nil, Config{AIVPN: src}).Handler())
	defer srv.Close()

	status, body, _ := getWithUA(t, srv.URL+"/sub/tok-alice/info", aivpnUA)
	if status != http.StatusOK {
		t.Fatalf("/info status %d, want 200 (body %q)", status, body)
	}
	var info struct {
		Features map[string]any `json:"features"`
		AIVPN    struct {
			Key string `json:"key"`
		} `json:"aivpn"`
	}
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("decode /info %q: %v", body, err)
	}
	if v, ok := info.Features["aivpn"]; !ok || v != true {
		t.Errorf("features.aivpn = %#v (present %v), want true; body %q", v, ok, body)
	}
	if info.AIVPN.Key != key {
		t.Errorf("info aivpn.key = %q, want %q", info.AIVPN.Key, key)
	}

	for _, tc := range []struct {
		name string
		path string
		ua   string
	}{
		{"old app", "/sub/tok-alice/info", "SFA/1.0.107 (107; sing-box 1.14.0-alpha.31; language ru_RU)"},
		{"non-app client", "/sub/tok-alice/info", "curl/8.5.0"},
		{"not whitelisted", "/sub/tok-bob/info", aivpnUA},
		{"expired", "/sub/tok-carol/info", aivpnUA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := getWithUA(t, srv.URL+tc.path, tc.ua)
			if status != http.StatusOK {
				t.Fatalf("/info status %d, want 200 (body %q)", status, body)
			}
			if strings.Contains(body, "aivpn") || strings.Contains(body, key) {
				t.Errorf("/info exposed AIVPN to %s: %q", tc.name, body)
			}
			// Nobody else in this fixture has vk_turn either, so the whole
			// features object must be absent — same bytes as before the change.
			var m map[string]any
			if err := json.Unmarshal([]byte(body), &m); err != nil {
				t.Fatalf("decode /info %q: %v", body, err)
			}
			if _, ok := m["features"]; ok {
				t.Errorf("empty features object still emitted: %q", body)
			}
		})
	}
}

// TestAivpnSubProfileStaysSingbox: the aivpn:// key is NOT a sing-box field, so the
// /sub profile must never carry it (a libbox would hard-fail the whole config).
func TestAivpnSubProfileStaysSingbox(t *testing.T) {
	st, src, key := aivpnFixture(t)
	srv := httptest.NewServer(New(st, nil, nil, nil, Config{AIVPN: src}).Handler())
	defer srv.Close()

	status, body, _ := getWithUA(t, srv.URL+"/sub/tok-alice", aivpnUA)
	if status != http.StatusOK {
		t.Fatalf("/sub status %d, want 200 (body %q)", status, body)
	}
	if strings.Contains(body, key) || strings.Contains(body, "aivpn") {
		t.Error("/sub sing-box profile contains the AIVPN key")
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatalf("/sub is not JSON: %v", err)
	}
}

// failingAivpn is a Source whose Key always fails — the shape a misconfigured or
// unreachable AIVPN server takes. It must degrade to "no key", not to a 500.
type failingAivpn struct {
	err   error
	empty bool
}

func (failingAivpn) Enabled() bool       { return true }
func (failingAivpn) MinVersionCode() int { return 108 }
func (failingAivpn) Allowed(string) bool { return true }
func (f failingAivpn) Key(context.Context, string) (string, error) {
	if f.empty {
		return "", nil
	}
	return "", f.err
}

func TestAivpnSourceFailureDegrades(t *testing.T) {
	st, _, _ := aivpnFixture(t)
	for _, src := range []failingAivpn{
		{err: errors.New("aivpn: AIVPN server issued an unusable key: boom")},
		{empty: true},
	} {
		srv := httptest.NewServer(New(st, nil, nil, nil, Config{AIVPN: src}).Handler())

		status, body, _ := getWithUA(t, srv.URL+"/aivpn/tok-alice", aivpnUA)
		if status != http.StatusForbidden {
			t.Errorf("source failure: status %d, want 403 (body %q)", status, body)
		}
		status, body, _ = getWithUA(t, srv.URL+"/sub/tok-alice/info", aivpnUA)
		if status != http.StatusOK {
			t.Fatalf("/info status %d, want 200 (body %q)", status, body)
		}
		if strings.Contains(body, "aivpn") {
			t.Errorf("source failure still advertised AIVPN: %q", body)
		}
		srv.Close()
	}
}
