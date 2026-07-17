package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnprov"
	"golang.org/x/crypto/bcrypt"
)

// newPanelClientServer wires the FULL one-click chain: panel auth + customer
// store + vkturn store + a real provisioner over a temp canary dir with a fake
// docker. Returns everything the assertions need.
func newPanelClientServer(t *testing.T, withProv bool) (*httptest.Server, *vkturnconf.Store, *store.Store, string, *[]string) {
	t.Helper()
	vkStore := vkturnconf.NewInMemory()
	if err := vkStore.Set(validVKTurnConfig()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	seed := func(c *store.Customer) {
		if err := st.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	friend := vkTurnCustomer("friend", "tok-friend")
	friend.Devices = map[string]time.Time{
		"dev-uuid-1":       time.Now(),
		"d-abc123":         time.Now(),
		"bad id with sp!c": time.Now(), // fails deviceIDRe → must be filtered out
	}
	seed(friend)
	nodev := vkTurnCustomer("nodev", "tok-nodev")
	seed(nodev)
	expired := vkTurnCustomer("gone", "tok-gone")
	expired.Expires = time.Now().Add(-time.Hour)
	seed(expired)

	canaryDir := t.TempDir()
	db := map[string]any{
		"main_password": "m", "admin_id": "1", "bot_token": "b",
		"passwords": map[string]any{}, "devices": map[string]any{},
	}
	b, _ := json.MarshalIndent(db, "", "  ")
	if err := os.WriteFile(filepath.Join(canaryDir, "passwords.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canaryDir, "wg-keys.dat"), []byte(strings.Repeat(vkTurnTestKey+"\n", 4)), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	var prov *vkturnprov.Provisioner
	if withProv {
		prov = vkturnprov.Open(canaryDir, "", "")
		prov.SetExecHooks(func(name string, args ...string) (string, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if len(args) > 0 && args[0] == "inspect" {
				return "true 172.17.0.2", nil
			}
			return "", nil
		}, func(time.Duration) {})
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("testpw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil, nil, Config{PanelPath: "/mp/", PanelPasswordHash: string(hash), VKTurn: vkStore, WDTT: prov})
	return httptest.NewServer(s.Handler()), vkStore, st, canaryDir, &calls
}

func TestPanelVKTurnClientAddRemoveEndToEnd(t *testing.T) {
	srv, vkStore, _, canaryDir, calls := newPanelClientServer(t, true)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	// ADD: one click provisions the canary AND publishes the client half.
	resp := panelPost(t, srv, "api/vkturn/client", cookie, csrf, map[string]string{"login": "friend", "action": "add"})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("add status = %d: %s", resp.StatusCode, b)
	}
	_ = resp.Body.Close()

	cl, ok := vkStore.Get().Clients["friend"]
	if !ok {
		t.Fatal("friend not published to vkturn config")
	}
	if len(cl.Password) != 16 || cl.WG.PrivateKey == "" || cl.WG.PeerPublicKey != vkTurnTestKey || !strings.HasPrefix(cl.WG.LocalAddress, "10.66.66.") {
		t.Fatalf("published client half incomplete: %+v", cl)
	}
	raw, err := os.ReadFile(filepath.Join(canaryDir, "passwords.json"))
	if err != nil {
		t.Fatal(err)
	}
	var db struct {
		Passwords map[string]map[string]any `json:"passwords"`
		Devices   map[string]map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(raw, &db); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.Passwords[cl.Password]; !ok {
		t.Fatal("canary has no password entry for friend")
	}
	if db.Devices["dev-uuid-1"] == nil || db.Devices["d-abc123"] == nil {
		t.Fatalf("both valid installs must be pre-provisioned: %v", db.Devices)
	}
	if db.Devices["bad id with sp!c"] != nil {
		t.Fatal("device id failing deviceIDRe must not reach the canary")
	}
	if len(*calls) == 0 || !strings.Contains(strings.Join(*calls, ";"), "restart") {
		t.Fatal("canary container was not restarted")
	}

	// Duplicate add → 409; unknown login → 404; no installs → 409; expired → 409.
	for _, tc := range []struct {
		login string
		want  int
	}{{"friend", 409}, {"ghost", 404}, {"nodev", 409}, {"gone", 409}} {
		r := panelPost(t, srv, "api/vkturn/client", cookie, csrf, map[string]string{"login": tc.login, "action": "add"})
		if r.StatusCode != tc.want {
			b, _ := io.ReadAll(r.Body)
			t.Fatalf("add %q status = %d (want %d): %s", tc.login, r.StatusCode, tc.want, b)
		}
		_ = r.Body.Close()
	}

	// REMOVE: unpublishes and tears the canary side down.
	resp2 := panelPost(t, srv, "api/vkturn/client", cookie, csrf, map[string]string{"login": "friend", "action": "remove"})
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("remove status = %d: %s", resp2.StatusCode, b)
	}
	var out map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&out)
	_ = resp2.Body.Close()
	if _, warned := out["warning"]; warned {
		t.Fatalf("healthy remove must not warn: %v", out["warning"])
	}
	if _, still := vkStore.Get().Clients["friend"]; still {
		t.Fatal("friend still published after remove")
	}
	raw, _ = os.ReadFile(filepath.Join(canaryDir, "passwords.json"))
	if strings.Contains(string(raw), cl.Password) || strings.Contains(string(raw), "dev-uuid-1") {
		t.Fatal("canary entries survive removal")
	}

	// Removing a login that is not listed → 404.
	r := panelPost(t, srv, "api/vkturn/client", cookie, csrf, map[string]string{"login": "friend", "action": "remove"})
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("second remove status = %d", r.StatusCode)
	}
	_ = r.Body.Close()
}

func TestPanelVKTurnClientGuards(t *testing.T) {
	srv, _, _, _, _ := newPanelClientServer(t, true)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	// Mutations are CSRF-guarded like every other panel write.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mp/api/vkturn/client", strings.NewReader(`{"login":"friend","action":"add"}`))
	req.AddCookie(&http.Cookie{Name: "mp_session", Value: cookie})
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be 403, got %d", r.StatusCode)
	}

	// Bad action / blank login → 400.
	for _, body := range []map[string]string{{"login": "friend", "action": "explode"}, {"login": " ", "action": "add"}} {
		r2 := panelPost(t, srv, "api/vkturn/client", cookie, csrf, body)
		if r2.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %v status = %d", body, r2.StatusCode)
		}
		_ = r2.Body.Close()
	}
}

func TestPanelVKTurnClientOffWithoutProvisioner(t *testing.T) {
	srv, _, _, _, _ := newPanelClientServer(t, false)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)
	r := panelPost(t, srv, "api/vkturn/client", cookie, csrf, map[string]string{"login": "friend", "action": "add"})
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("without MAESTRO_WDTT_CANARY_DIR the endpoint must 400, got %d", r.StatusCode)
	}
	b, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if !strings.Contains(string(b), "MAESTRO_WDTT_CANARY_DIR") {
		t.Fatalf("error must name the missing env: %s", b)
	}
}
