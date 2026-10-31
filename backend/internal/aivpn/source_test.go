package aivpn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStaticConfig(t *testing.T, cfg Config) string {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "aivpn.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func validConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Enabled:        true,
		MinVersionCode: 108,
		Logins:         []string{"alice"},
		Keys:           map[string]string{"alice": testKey(t)},
	}
}

func TestOpenStaticOff(t *testing.T) {
	for name, path := range map[string]string{
		"empty path": "",
		"missing":    filepath.Join(t.TempDir(), "absent.json"),
	} {
		s, err := OpenStatic(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s != nil {
			t.Fatalf("%s: expected nil source, got %+v", name, s)
		}
	}

	cfg := validConfig(t)
	cfg.Enabled = false
	s, err := OpenStatic(writeStaticConfig(t, cfg))
	if err != nil {
		t.Fatalf("disabled config: %v", err)
	}
	if s != nil {
		t.Fatal("disabled config must yield a nil source")
	}
}

func TestStaticServesWhitelistedKeys(t *testing.T) {
	cfg := validConfig(t)
	key := cfg.Keys["alice"]
	s, err := OpenStatic(writeStaticConfig(t, cfg))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !s.Enabled() || s.MinVersionCode() != 108 {
		t.Fatalf("unexpected source state: enabled=%v minVC=%d", s.Enabled(), s.MinVersionCode())
	}
	if !s.Allowed("alice") || s.Allowed("bob") {
		t.Fatalf("whitelist mismatch: alice=%v bob=%v", s.Allowed("alice"), s.Allowed("bob"))
	}
	got, err := s.Key(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if got != key {
		t.Fatal("Key did not return the configured key")
	}
	if _, err := s.Key(context.Background(), "bob"); !errors.Is(err, ErrNoClient) {
		t.Fatalf("unknown login err = %v, want ErrNoClient", err)
	}
}

func TestOpenStaticRejectsBrokenConfig(t *testing.T) {
	key := testKey(t)
	cases := map[string]Config{
		"no min vc":         {Enabled: true, Logins: []string{"alice"}, Keys: map[string]string{"alice": key}},
		"no keys":           {Enabled: true, MinVersionCode: 108, Logins: []string{"alice"}},
		"no logins":         {Enabled: true, MinVersionCode: 108, Keys: map[string]string{"alice": key}},
		"login without key": {Enabled: true, MinVersionCode: 108, Logins: []string{"alice", "bob"}, Keys: map[string]string{"alice": key}},
		"bad login":         {Enabled: true, MinVersionCode: 108, Logins: []string{"alice smith"}, Keys: map[string]string{"alice smith": key}},
		"bad key":           {Enabled: true, MinVersionCode: 108, Logins: []string{"alice"}, Keys: map[string]string{"alice": "aivpn://garbage"}},
		"duplicate login":   {Enabled: true, MinVersionCode: 108, Logins: []string{"alice", "alice"}, Keys: map[string]string{"alice": key}},
	}
	for name, cfg := range cases {
		if _, err := OpenStatic(writeStaticConfig(t, cfg)); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}

	// Invalid JSON must abort too (not fall back to OFF).
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := OpenStatic(path); err == nil {
		t.Fatal("invalid JSON: expected an error")
	}
}

func TestOpenStaticKeyErrorMentionsNoSecret(t *testing.T) {
	cfg := validConfig(t)
	cfg.Keys["alice"] = "aivpn://" + strings.TrimPrefix(cfg.Keys["alice"], Prefix)[:10]
	_, err := OpenStatic(writeStaticConfig(t, cfg))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "aivpn://") {
		t.Fatalf("error echoes key material: %s", err)
	}
}

func envFrom(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestOpenOSOff(t *testing.T) {
	s, err := OpenOS(envFrom(nil))
	if err != nil {
		t.Fatalf("OpenOS: %v", err)
	}
	if s != nil {
		t.Fatalf("empty environment must mean OFF, got %+v", s)
	}
	// A static file path that does not exist is still OFF.
	s, err = OpenOS(envFrom(map[string]string{EnvFile: filepath.Join(t.TempDir(), "absent.json")}))
	if err != nil || s != nil {
		t.Fatalf("missing static file: source=%v err=%v, want nil/nil", s, err)
	}
}

func TestOpenOSStatic(t *testing.T) {
	cfg := validConfig(t)
	env := map[string]string{EnvFile: writeStaticConfig(t, cfg)}
	s, err := OpenOS(envFrom(env))
	if err != nil {
		t.Fatalf("OpenOS: %v", err)
	}
	if s == nil || !s.Enabled() || s.MinVersionCode() != 108 || !s.Allowed("alice") {
		t.Fatalf("unexpected source: %+v", s)
	}
	if _, ok := s.(*Static); !ok {
		t.Fatalf("expected *Static, got %T", s)
	}
}

func TestOpenOSServerModes(t *testing.T) {
	base := map[string]string{
		EnvSource:     "http",
		EnvLogins:     "alice, bob",
		EnvMinVC:      "108",
		EnvSocket:     "/run/aivpn/api.sock",
		EnvCLI:        "/usr/local/bin/aivpn-server",
		EnvCLIConfig:  "/etc/aivpn/server.json",
		EnvServerAddr: testServer,
	}
	env := func() map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		return out
	}

	s, err := OpenOS(envFrom(env()))
	if err != nil {
		t.Fatalf("http mode: %v", err)
	}
	srv, ok := s.(*Server)
	if !ok {
		t.Fatalf("http mode: expected *Server, got %T", s)
	}
	if !srv.Allowed("alice") || !srv.Allowed("bob") || srv.Allowed("carol") {
		t.Fatal("http mode: whitelist not parsed")
	}
	if srv.MinVersionCode() != 108 {
		t.Fatalf("http mode: min versionCode = %d", srv.MinVersionCode())
	}

	e := env()
	e[EnvSource] = "cli"
	e[EnvSocket] = ""
	s, err = OpenOS(envFrom(e))
	if err != nil {
		t.Fatalf("cli mode: %v", err)
	}
	if _, ok := s.(*Server); !ok {
		t.Fatalf("cli mode: expected *Server, got %T", s)
	}

	// Unknown source must be a configuration error, not a silent OFF.
	e = env()
	e[EnvSource] = "smoke-signals"
	if _, err := OpenOS(envFrom(e)); err == nil {
		t.Fatal("unknown source: expected an error")
	}

	// Server modes require an explicit whitelist and versionCode.
	e = env()
	delete(e, EnvLogins)
	if _, err := OpenOS(envFrom(e)); err == nil {
		t.Fatal("missing logins: expected an error")
	}
	e = env()
	delete(e, EnvMinVC)
	if _, err := OpenOS(envFrom(e)); err == nil {
		t.Fatal("missing min versionCode: expected an error")
	}
}

func TestAtoi(t *testing.T) {
	cases := map[string]int{"": 0, "abc": 0, "12x": 0, "0": 0, "108": 108, " 107 ": 107}
	for in, want := range cases {
		if got := atoi(in); got != want {
			t.Fatalf("atoi(%q) = %d, want %d", in, got, want)
		}
	}
}
