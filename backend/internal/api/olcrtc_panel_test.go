package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func newPanelOlcRoomServer(t *testing.T, script string) *httptest.Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("testpw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	olc, err := olcconf.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := olc.Set(olcconf.Config{
		Enabled:   true,
		Room:      "https://telemost.yandex.ru/j/global",
		Key:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Provider:  "telemost",
		Transport: "vp8channel",
	}); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil, nil, Config{
		PanelPath:         "/mp/",
		PanelPasswordHash: string(hash),
		OLC:               olc,
		OlcrtcRoomScript:  script,
		OlcHealthFile:     "",
		OlcWBTokenFile:    "",
	})
	return httptest.NewServer(s.Handler())
}

func writeOlcArgRecorder(t *testing.T) (script, argsPath string) {
	t.Helper()
	dir := t.TempDir()
	argsPath = filepath.Join(dir, "args.txt")
	script = filepath.Join(dir, "record-olc-args.sh")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$#\" > '" + argsPath + "'\n" +
		"printf '%s\\n' \"$@\" >> '" + argsPath + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, argsPath
}

func TestPanelOlcGlobalTelemostRoomScriptGetsOnlyRoomArg(t *testing.T) {
	script, argsPath := writeOlcArgRecorder(t)
	srv := newPanelOlcRoomServer(t, script)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	resp := panelPost(t, srv, "api/olcrtc/room", cookie, csrf, map[string]any{
		"login":    "",
		"room":     "https://telemost.yandex.ru/j/fresh",
		"provider": "telemost",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("global telemost room status = %d body=%s", resp.StatusCode, b)
	}
	got, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "1\nhttps://telemost.yandex.ru/j/fresh\n"
	if string(got) != want {
		t.Fatalf("global room script args:\n got %q\nwant %q", string(got), want)
	}
}

func TestPanelOlcGlobalWbstreamRoomIsRejectedBeforeScript(t *testing.T) {
	script, argsPath := writeOlcArgRecorder(t)
	srv := newPanelOlcRoomServer(t, script)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	resp := panelPost(t, srv, "api/olcrtc/room", cookie, csrf, map[string]any{
		"login":    "",
		"room":     "wb-room-12345",
		"provider": "wbstream",
	})
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("global wbstream status = %d body=%s", resp.StatusCode, b)
	}
	if !bytes.Contains(bytes.ToLower(b), []byte("login")) {
		t.Fatalf("global wbstream rejection should mention login: %s", b)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatalf("wbstream global request reached script; args file err=%v", err)
	}
}

func TestPanelOlcPerLoginRoomStillPassesProviderToScript(t *testing.T) {
	script, argsPath := writeOlcArgRecorder(t)
	srv := newPanelOlcRoomServer(t, script)
	defer srv.Close()
	cookie, csrf := panelLogin(t, srv)

	resp := panelPost(t, srv, "api/olcrtc/room", cookie, csrf, map[string]any{
		"login":    "wapmix",
		"room":     "https://telemost.yandex.ru/j/family",
		"provider": "telemost",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("per-login room status = %d body=%s", resp.StatusCode, b)
	}
	got, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{"3", "wapmix", "https://telemost.yandex.ru/j/family", "telemost"}
	if strings.TrimSpace(string(got)) != strings.Join(wantLines, "\n") {
		t.Fatalf("per-login script args:\n got %q\nwant %q", string(got), strings.Join(wantLines, "\n")+"\n")
	}
}

func TestCreateWBStreamRoomRegistersFreshGuestBeforeCreate(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/auth/api/v1/auth/user/guest-register":
			if r.Method != http.MethodPost {
				t.Fatalf("guest register method = %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("guest register Authorization = %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"displayName":"MaestroVPN Panel","device":{"deviceName":"Linux","deviceType":"PARTICIPANT_DEVICE_TYPE_WEB_DESKTOP"}}`
			if string(body) != want {
				t.Fatalf("guest register body = %q, want %q", body, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"accessToken":"fresh-access"}`)
		case "/api-room/api/v2/room":
			if r.Method != http.MethodPost {
				t.Fatalf("room create method = %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer fresh-access" {
				t.Fatalf("room create Authorization = %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"roomType":"ROOM_TYPE_ALL_ON_SCREEN","roomPrivacy":"ROOM_PRIVACY_FREE"}`
			if string(body) != want {
				t.Fatalf("room create body = %q, want %q", body, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"roomId":"fresh-room"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	room, err := createWBStreamRoomWithClient(context.Background(), upstream.Client(), upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	if room != "fresh-room" {
		t.Fatalf("room = %q", room)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}
