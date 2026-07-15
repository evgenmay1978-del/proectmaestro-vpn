package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
)

const vkTurnTestUA = "SFA/1.0.200 (200; sing-box test; language ru_RU)"
const vkTurnTestKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func validVKTurnConfig() *VKTurnConfig {
	clients := make(map[string]vkturnconf.Client)
	for i, login := range []string{"wapmix", "wapmixx", "wapmix2"} {
		clients[login] = vkturnconf.Client{Password: "pass-" + login, WG: subgen.VKTurnCreds{
			PrivateKey: vkTurnTestKey, PeerPublicKey: vkTurnTestKey,
			LocalAddress: "10.77.0." + string(rune('2'+i)) + "/32",
		}}
	}
	return &VKTurnConfig{Enabled: true, MinVersionCode: 200, Server: "wdtt.example:443", VKHashes: []string{"vk-hash"}, Clients: clients}
}

func newVKTurnTestServer(t *testing.T, cfg *VKTurnConfig, customers ...*store.Customer) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, c := range customers {
		if err := st.Put(c); err != nil {
			t.Fatalf("store.Put(%s): %v", c.Login, err)
		}
	}
	return httptest.NewServer(New(st, nil, nil, nil, Config{VKTurn: cfg}).Handler())
}

func vkTurnCustomer(login, token string) *store.Customer {
	return &store.Customer{
		Login: login, SubToken: token, Expires: time.Now().Add(24 * time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "example.test", Port: 443, UUID: "u"},
	}
}

func getVKTurnInfo(t *testing.T, srv *httptest.Server, path, ua string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", ua)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasVKTurnFeature(out map[string]any) bool {
	features, ok := out["features"].(map[string]any)
	return ok && features["vk_turn"] == true
}

func TestVKTurnInfoAllowsExactlyThreeMobileLogins(t *testing.T) {
	cfg := validVKTurnConfig()
	logins := []string{"wapmix", "wapmixx", "wapmix2", "WAPMIX", "wapmix3", "stranger"}
	customers := make([]*store.Customer, 0, len(logins))
	for i, login := range logins {
		customers = append(customers, vkTurnCustomer(login, "tok-"+string(rune('a'+i))))
	}
	srv := newVKTurnTestServer(t, cfg, customers...)
	defer srv.Close()

	for i, login := range logins {
		got := hasVKTurnFeature(getVKTurnInfo(t, srv, "/sub/tok-"+string(rune('a'+i))+"/info?platform=mobile", vkTurnTestUA))
		want := login == "wapmix" || login == "wapmixx" || login == "wapmix2"
		if got != want {
			t.Errorf("login %q vk_turn = %v, want %v", login, got, want)
		}
	}
}

func TestVKTurnInfoIncludesCompleteChildContract(t *testing.T) {
	srv := newVKTurnTestServer(t, validVKTurnConfig(), vkTurnCustomer("wapmix", "tok-owner"))
	defer srv.Close()
	out := getVKTurnInfo(t, srv, "/sub/tok-owner/info?platform=mobile", vkTurnTestUA)
	child, ok := out["vk_turn"].(map[string]any)
	if !ok {
		t.Fatalf("vk_turn child params missing: %#v", out)
	}
	for _, key := range []string{"server", "vk_hashes", "password", "workers", "fingerprint", "client_ids", "obfs_mode"} {
		if _, exists := child[key]; !exists {
			t.Errorf("vk_turn.%s missing: %#v", key, child)
		}
	}
	if child["workers"] != float64(vkturnconf.DefaultWorkers) || child["fingerprint"] != vkturnconf.DefaultFingerprint {
		t.Errorf("unexpected child defaults: %#v", child)
	}
}

func TestVKTurnInfoFailsClosedForPlatformVersionAndConfig(t *testing.T) {
	customer := vkTurnCustomer("wapmix", "tok-owner")
	tests := []struct {
		name string
		cfg  *VKTurnConfig
		path string
		ua   string
	}{
		{name: "nil config", path: "/sub/tok-owner/info?platform=mobile", ua: vkTurnTestUA},
		{name: "disabled", cfg: func() *VKTurnConfig { c := validVKTurnConfig(); c.Enabled = false; return c }(), path: "/sub/tok-owner/info?platform=mobile", ua: vkTurnTestUA},
		{name: "no minimum", cfg: func() *VKTurnConfig { c := validVKTurnConfig(); c.MinVersionCode = 0; return c }(), path: "/sub/tok-owner/info?platform=mobile", ua: vkTurnTestUA},
		{name: "tv", cfg: validVKTurnConfig(), path: "/sub/tok-owner/info?platform=tv", ua: vkTurnTestUA},
		{name: "unknown platform", cfg: validVKTurnConfig(), path: "/sub/tok-owner/info?platform=android", ua: vkTurnTestUA},
		{name: "missing platform", cfg: validVKTurnConfig(), path: "/sub/tok-owner/info", ua: vkTurnTestUA},
		{name: "old app", cfg: validVKTurnConfig(), path: "/sub/tok-owner/info?platform=mobile", ua: "SFA/1.0.199 (199; sing-box test; language ru_RU)"},
		{name: "non SFA", cfg: validVKTurnConfig(), path: "/sub/tok-owner/info?platform=mobile", ua: "curl/8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newVKTurnTestServer(t, tt.cfg, customer)
			defer srv.Close()
			if hasVKTurnFeature(getVKTurnInfo(t, srv, tt.path, tt.ua)) {
				t.Fatal("vk_turn capability leaked through a closed gate")
			}
		})
	}
}

func TestVKTurnSubEmitsTransportOnlyForEligibleMobile(t *testing.T) {
	srv := newVKTurnTestServer(t,
		validVKTurnConfig(),
		vkTurnCustomer("wapmix", "tok-owner"),
	)
	defer srv.Close()

	get := func(path string) []byte {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("User-Agent", vkTurnTestUA)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	plain := get("/sub/tok-owner")
	mobile := get("/sub/tok-owner?platform=mobile")
	if string(plain) == string(mobile) || !strings.Contains(string(mobile), `"vk-turn"`) {
		t.Fatal("eligible mobile subscription missing vk-turn transport")
	}
	if strings.Contains(string(plain), `"vk-turn"`) {
		t.Fatal("request without mobile platform received vk-turn transport")
	}
}
