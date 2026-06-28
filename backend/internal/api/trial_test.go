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

func postTrial(t *testing.T, url, nick, anchor string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nick": nick, "anchor": anchor, "device": "dev-" + nick})
	resp, err := http.Post(url+"/trial", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /trial: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// TestTrialAntiAbuse proves the core anti-abuse: one trial per device ANCHOR (survives a
// reinstall/new-nick), one per nick, and a fresh device+nick still works.
func TestTrialAntiAbuse(t *testing.T) {
	srv := newTrialServer(t)
	defer srv.Close()

	// 1. first trial on a device → 200
	if got := postTrial(t, srv.URL, "vasya", "androidA|drmA|Pixel"); got != http.StatusOK {
		t.Fatalf("first trial = %d, want 200", got)
	}
	// 2. SAME anchor, different nick (the reinstaller case) → 403 device already trialed
	if got := postTrial(t, srv.URL, "petya", "androidA|drmA|Pixel"); got != http.StatusForbidden {
		t.Fatalf("same-anchor trial = %d, want 403", got)
	}
	// 3. NEW anchor but a nick already used → 409
	if got := postTrial(t, srv.URL, "vasya", "androidB|drmB|Sony"); got != http.StatusConflict {
		t.Fatalf("reused-nick trial = %d, want 409", got)
	}
	// 4. fresh device + fresh nick → 200
	if got := postTrial(t, srv.URL, "kolya", "androidC|drmC|TCL"); got != http.StatusOK {
		t.Fatalf("fresh trial = %d, want 200", got)
	}
	// 5. no device anchor → 400 (can't enforce anti-abuse without it)
	if got := postTrial(t, srv.URL, "misha", ""); got != http.StatusBadRequest {
		t.Fatalf("no-anchor trial = %d, want 400", got)
	}
	// 6. malformed nick → 400
	if got := postTrial(t, srv.URL, "bad nick!", "androidD|x|y"); got != http.StatusBadRequest {
		t.Fatalf("bad-nick trial = %d, want 400", got)
	}
}
