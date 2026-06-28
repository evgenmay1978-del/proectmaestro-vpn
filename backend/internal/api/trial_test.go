package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/promo"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func newTrialServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	pst, err := promo.Open(filepath.Join(t.TempDir(), "trials.json"), "test-salt")
	if err != nil {
		t.Fatalf("promo.Open: %v", err)
	}
	// TrialIPQuota=0 disables the velocity check (all test calls share 127.0.0.1).
	return httptest.NewServer(New(st, &fakeProv{st: st}, nil, pst, Config{TrialDays: 2, TrialIPQuota: 0}).Handler())
}

// postTrial returns (status, login). login is the provisioned/returned account, or "".
func postTrial(t *testing.T, url, nick, anchor string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nick": nick, "anchor": anchor, "device": "dev-" + nick})
	resp, err := http.Post(url+"/trial", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /trial: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var o struct {
		Login string `json:"login"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&o)
	return resp.StatusCode, o.Login
}

// TestTrialAntiAbuse proves: the trial is keyed on the nick (login "trial-<nick>"), one account per
// device anchor, a device coming back gets the SAME account (no proliferation), and a taken nick is
// rejected for a different device.
func TestTrialAntiAbuse(t *testing.T) {
	srv := newTrialServer(t)
	defer srv.Close()

	// 1. first trial on a device → account "trial-vasya"
	st1, login1 := postTrial(t, srv.URL, "vasya", "androidA|drmA|Pixel")
	if st1 != http.StatusOK || login1 != "trial-vasya" {
		t.Fatalf("first trial = %d/%q, want 200/trial-vasya", st1, login1)
	}
	// 2. SAME device (reinstall / any nick) → returns the SAME account, NOT a new one
	st2, login2 := postTrial(t, srv.URL, "petya", "androidA|drmA|Pixel")
	if st2 != http.StatusOK {
		t.Fatalf("re-entry = %d, want 200", st2)
	}
	if login2 != login1 {
		t.Fatalf("re-entry login = %q, want SAME %q (no proliferation)", login2, login1)
	}
	// 3. a DIFFERENT device tries an already-taken nick → 409
	st3, _ := postTrial(t, srv.URL, "vasya", "androidB|drmB|Sony")
	if st3 != http.StatusConflict {
		t.Fatalf("taken-nick = %d, want 409", st3)
	}
	// 4. fresh device + fresh nick → a new account "trial-kolya"
	st4, login4 := postTrial(t, srv.URL, "kolya", "androidC|drmC|TCL")
	if st4 != http.StatusOK || login4 != "trial-kolya" {
		t.Fatalf("fresh trial = %d/%q, want 200/trial-kolya", st4, login4)
	}
	// 5. no device anchor → 400 (can't enforce anti-abuse without it)
	st5, _ := postTrial(t, srv.URL, "misha", "")
	if st5 != http.StatusBadRequest {
		t.Fatalf("no-anchor = %d, want 400", st5)
	}
	// 6. malformed nick → 400
	st6, _ := postTrial(t, srv.URL, "bad nick!", "androidD|x|y")
	if st6 != http.StatusBadRequest {
		t.Fatalf("bad-nick = %d, want 400", st6)
	}
}
