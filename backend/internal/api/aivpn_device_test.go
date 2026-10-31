package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/aivpn"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// recordingAivpn is a Source that records every lookup, so a test can prove which
// (login, device) pair reached the source and which requests never got that far.
type recordingAivpn struct {
	mu      sync.Mutex
	calls   []string
	keys    map[string]string // device id -> key; the empty device is the legacy key
	allowed map[string]bool
	fail    error
}

func newRecordingAivpn(logins ...string) *recordingAivpn {
	r := &recordingAivpn{keys: map[string]string{}, allowed: map[string]bool{}}
	for _, login := range logins {
		r.allowed[login] = true
	}
	return r
}

func (r *recordingAivpn) Enabled() bool       { return true }
func (r *recordingAivpn) MinVersionCode() int { return 108 }
func (r *recordingAivpn) Allowed(login string) bool {
	return r.allowed[login]
}

func (r *recordingAivpn) lookup(login, device string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, login+"|"+device)
	r.mu.Unlock()
	if r.fail != nil {
		return "", r.fail
	}
	key, ok := r.keys[device]
	if !ok || key == "" {
		return "", aivpn.ErrNoClient
	}
	return key, nil
}

func (r *recordingAivpn) Key(_ context.Context, login string) (string, error) {
	return r.lookup(login, "")
}

func (r *recordingAivpn) KeyForDevice(_ context.Context, login, device string) (string, error) {
	if device == "" {
		return r.Key(context.Background(), login)
	}
	return r.lookup(login, device)
}

func (r *recordingAivpn) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// deviceKey builds a valid key whose PSK is 32 copies of psk, so every device's
// credential is distinguishable in the response body.
func deviceKey(t *testing.T, psk byte) string {
	t.Helper()
	key, err := aivpn.Build(aivpn.Key{
		Server:    "203.0.113.10:443",
		ServerKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		PSK:       base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{psk}, 32)),
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

// decodeAivpnKey reads the single field of the /aivpn JSON answer.
func decodeAivpnKey(t *testing.T, body string) string {
	t.Helper()
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode /aivpn body %q: %v", body, err)
	}
	return out.Key
}

// TestAivpnDeviceScoped: a request that names a device receives THAT device's
// credential; no device (and a malformed device, which /sub also treats as none)
// receives the legacy login-wide key. The X-Device-Id header scopes the same way.
func TestAivpnDeviceScoped(t *testing.T) {
	st, _, _ := aivpnFixture(t)
	src := newRecordingAivpn("alice")
	legacy, key1, key2 := deviceKey(t, 0x33), deviceKey(t, 0x11), deviceKey(t, 0x22)
	src.keys[""], src.keys["dev-1"], src.keys["dev-2"] = legacy, key1, key2
	srv := httptest.NewServer(New(st, nil, nil, nil, Config{AIVPN: src}).Handler())
	defer srv.Close()

	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"device 1", "?device=dev-1", key1},
		{"device 2", "?device=dev-2", key2},
		{"no device is the legacy key", "", legacy},
		{"malformed device is no device", "?device=bad%20id", legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := getWithUA(t, srv.URL+"/aivpn/tok-alice"+tc.query, aivpnUA)
			if status != http.StatusOK {
				t.Fatalf("status %d, want 200 (body %q)", status, body)
			}
			if got := decodeAivpnKey(t, body); got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
		})
	}

	// The header is the app's second way of naming its device: same scoping.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/aivpn/tok-alice", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("User-Agent", aivpnUA)
	req.Header.Set("X-Device-Id", "dev-2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with X-Device-Id: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("X-Device-Id status %d, read err %v", resp.StatusCode, err)
	}
	if got := decodeAivpnKey(t, string(body)); got != key2 {
		t.Errorf("X-Device-Id key = %q, want %q", got, key2)
	}

	// Every request consulted the source exactly once, with the device it named.
	want := []string{"alice|dev-1", "alice|dev-2", "alice|", "alice|", "alice|dev-2"}
	if got := src.seen(); !reflect.DeepEqual(got, want) {
		t.Fatalf("source calls = %v, want %v", got, want)
	}
}

// TestAivpnDeviceGates: the gates run BEFORE the source is consulted, including
// on the device path — an old build or a non-whitelisted login cannot make the
// panel create an AIVPN client or burn a device slot.
func TestAivpnDeviceGates(t *testing.T) {
	st, _, _ := aivpnFixture(t)
	src := newRecordingAivpn("alice")
	src.keys["dev-1"] = deviceKey(t, 0x11)
	srv := httptest.NewServer(New(st, nil, nil, nil, Config{AIVPN: src}).Handler())
	defer srv.Close()

	cases := []struct {
		name   string
		path   string
		ua     string
		status int
	}{
		{"eligible device request", "/aivpn/tok-alice?device=dev-1", aivpnUA, http.StatusOK},
		{"curl has no versionCode", "/aivpn/tok-alice?device=dev-1", "curl/8.5.0", http.StatusForbidden},
		{"version too old", "/aivpn/tok-alice?device=dev-1", "SFA/1.0.107 (107; sing-box 1.14.0-alpha.31; language ru_RU)", http.StatusForbidden},
		{"login not whitelisted", "/aivpn/tok-bob?device=dev-1", aivpnUA, http.StatusForbidden},
		{"expired subscription", "/aivpn/tok-carol?device=dev-1", aivpnUA, http.StatusPaymentRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := getWithUA(t, srv.URL+tc.path, tc.ua)
			if status != tc.status {
				t.Errorf("status %d, want %d (body %q)", status, tc.status, body)
			}
			if tc.status != http.StatusOK && strings.Contains(body, "aivpn://") {
				t.Errorf("a rejected device request leaked a key: %q", body)
			}
		})
	}
	if got := src.seen(); !reflect.DeepEqual(got, []string{"alice|dev-1"}) {
		t.Fatalf("source calls = %v, want only the eligible lookup", got)
	}
}

// TestAivpnDeviceLimit: /aivpn enforces the same per-account cap as /sub — the
// sixth new device gets /sub's own 403 "device limit reached", /info still serves
// the account status without the credential, and the refused device never reaches
// the source.
func TestAivpnDeviceLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	tok := "tok-dave"
	if err := st.Put(&store.Customer{
		Login: "dave", SubToken: tok, Expires: time.Now().Add(720 * time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	src := newRecordingAivpn("dave")
	for i := 1; i <= 6; i++ {
		src.keys[fmt.Sprintf("dev-%d", i)] = deviceKey(t, byte(i))
	}
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, nil, nil, Config{
		AdminToken: "sek", SubBaseURL: "https://x", EnforceDeviceLimit: true, AIVPN: src,
	}).Handler())
	defer srv.Close()

	for i := 1; i <= 5; i++ {
		status, body, _ := getWithUA(t, fmt.Sprintf("%s/aivpn/%s?device=dev-%d", srv.URL, tok, i), aivpnUA)
		if status != http.StatusOK {
			t.Fatalf("device %d: status %d, want 200 (body %q)", i, status, body)
		}
	}

	status, body, _ := getWithUA(t, srv.URL+"/aivpn/"+tok+"?device=dev-6", aivpnUA)
	if status != http.StatusForbidden {
		t.Fatalf("6th device: status %d, want 403 (body %q)", status, body)
	}
	if msg := strings.TrimSpace(body); msg != "device limit reached" {
		t.Fatalf("6th device body = %q, want /sub's own message", body)
	}
	// /sub refuses the same device with the same status: one shared cap.
	if code := subStatus(t, srv.URL, tok, "dev-6"); code != http.StatusForbidden {
		t.Fatalf("/sub for the same device = %d, want 403", code)
	}
	// /info is never device-capped: the customer still sees the account, minus the key.
	status, infoBody, _ := getWithUA(t, srv.URL+"/sub/"+tok+"/info?device=dev-6", aivpnUA)
	if status != http.StatusOK {
		t.Fatalf("/info status %d, want 200 (body %q)", status, infoBody)
	}
	if strings.Contains(infoBody, "aivpn") {
		t.Errorf("/info leaked AIVPN for a capped device: %q", infoBody)
	}
	// A cap refusal is not a lookup: the source saw exactly the five admitted devices.
	if got := src.seen(); len(got) != 5 {
		t.Fatalf("source calls = %v, want the five admitted devices", got)
	}
	// A device admitted earlier keeps working at the cap.
	status, body, _ = getWithUA(t, srv.URL+"/aivpn/"+tok+"?device=dev-3", aivpnUA)
	if status != http.StatusOK {
		t.Fatalf("known device at the cap: status %d, want 200 (body %q)", status, body)
	}
}
