package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// deviceServer seeds one active customer and returns a server with the device cap ON.
func deviceServer(t *testing.T, login string) (*httptest.Server, *store.Store, string) {
	t.Helper()
	st, _ := store.Open(filepath.Join(t.TempDir(), "s.json"))
	tok := "tok-" + login
	if err := st.Put(&store.Customer{
		Login: login, SubToken: tok, Expires: time.Now().Add(720 * time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := New(st, &fakeProv{st: st}, nil, Config{
		AdminToken: "sek", SubBaseURL: "https://x", EnforceDeviceLimit: true,
	})
	return httptest.NewServer(srv.Handler()), st, tok
}

func subStatus(t *testing.T, base, tok, dev string) int {
	t.Helper()
	url := base + "/sub/" + tok
	if dev != "" {
		url += "?device=" + dev
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET sub: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestDeviceLimitBlocksSixth: 5 distinct devices succeed, the 6th is refused, and a device
// already registered keeps working (idempotent — re-polls don't consume slots).
func TestDeviceLimitBlocksSixth(t *testing.T) {
	srv, _, tok := deviceServer(t, "dave")
	defer srv.Close()
	for i := 1; i <= 5; i++ {
		if code := subStatus(t, srv.URL, tok, fmt.Sprintf("dev-%d", i)); code != http.StatusOK {
			t.Fatalf("device %d = %d, want 200", i, code)
		}
	}
	if code := subStatus(t, srv.URL, tok, "dev-6"); code != http.StatusForbidden {
		t.Fatalf("6th device = %d, want 403", code)
	}
	// a known device still works, and re-polling it doesn't free/consume a slot
	if code := subStatus(t, srv.URL, tok, "dev-3"); code != http.StatusOK {
		t.Fatalf("known device = %d, want 200", code)
	}
	if code := subStatus(t, srv.URL, tok, "dev-7"); code != http.StatusForbidden {
		t.Fatalf("another new device = %d, want 403", code)
	}
}

// TestDeviceLimitNoIDPassThrough: a client that sends no device id (Karing, bot sub) is
// never blocked — those are capped natively by 3x-ui limitIp, not here.
func TestDeviceLimitNoIDPassThrough(t *testing.T) {
	srv, _, tok := deviceServer(t, "dave")
	defer srv.Close()
	for i := 0; i < 8; i++ {
		if code := subStatus(t, srv.URL, tok, ""); code != http.StatusOK {
			t.Fatalf("no-id poll %d = %d, want 200", i, code)
		}
	}
}

// TestDeviceLimitExemptUnlimited: the owner admin login is uncapped.
func TestDeviceLimitExemptUnlimited(t *testing.T) {
	srv, _, tok := deviceServer(t, "wapmix")
	defer srv.Close()
	for i := 1; i <= 8; i++ {
		if code := subStatus(t, srv.URL, tok, fmt.Sprintf("dev-%d", i)); code != http.StatusOK {
			t.Fatalf("exempt device %d = %d, want 200", i, code)
		}
	}
}

// TestSubInfoNotDeviceCapped: /info (login + days) is served even for a blocked device, so a
// customer at the cap can still see their account status in the app.
func TestSubInfoNotDeviceCapped(t *testing.T) {
	srv, _, tok := deviceServer(t, "dave")
	defer srv.Close()
	for i := 1; i <= 5; i++ {
		_ = subStatus(t, srv.URL, tok, fmt.Sprintf("dev-%d", i))
	}
	resp, err := http.Get(srv.URL + "/sub/" + tok + "/info?device=dev-6")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/info for blocked device = %d, want 200", resp.StatusCode)
	}
}

// TestResetDevicesClears: the admin reset-devices endpoint frees a maxed-out account.
func TestResetDevicesClears(t *testing.T) {
	srv, _, tok := deviceServer(t, "dave")
	defer srv.Close()
	for i := 1; i <= 5; i++ {
		_ = subStatus(t, srv.URL, tok, fmt.Sprintf("dev-%d", i))
	}
	if code := subStatus(t, srv.URL, tok, "dev-6"); code != http.StatusForbidden {
		t.Fatalf("6th before reset = %d, want 403", code)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/reset-devices",
		bytes.NewReader([]byte(`{"login":"dave"}`)))
	req.Header.Set("Authorization", "Bearer sek")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset = %d, want 200", resp.StatusCode)
	}
	if code := subStatus(t, srv.URL, tok, "dev-6"); code != http.StatusOK {
		t.Fatalf("6th after reset = %d, want 200", code)
	}
}

// TestDeviceLimitOffPassThrough: with EnforceDeviceLimit unset, nothing is blocked.
func TestDeviceLimitOffPassThrough(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "s.json"))
	_ = st.Put(&store.Customer{Login: "dave", SubToken: "tok-dave", Expires: time.Now().Add(720 * time.Hour),
		VLESS: &subgen.VLESSCreds{Server: "s", Port: 443, UUID: "u"}})
	srv := httptest.NewServer(New(st, &fakeProv{st: st}, nil, Config{AdminToken: "sek"}).Handler())
	defer srv.Close()
	for i := 1; i <= 8; i++ {
		if code := subStatus(t, srv.URL, "tok-dave", fmt.Sprintf("dev-%d", i)); code != http.StatusOK {
			t.Fatalf("cap-off device %d = %d, want 200", i, code)
		}
	}
}
