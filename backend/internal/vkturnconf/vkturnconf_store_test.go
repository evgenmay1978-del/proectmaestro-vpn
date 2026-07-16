package vkturnconf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func validStoreCfg() *Config {
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32-byte base64
	clients := map[string]Client{}
	for i, l := range []string{"wapmix", "wapmixx", "wapmix2"} {
		clients[l] = Client{Password: "pass-" + l, WG: subgen.VKTurnCreds{
			PrivateKey: key, PeerPublicKey: key, LocalAddress: "10.66.66." + string(rune('2'+i)) + "/32",
		}}
	}
	return &Config{Enabled: true, MinVersionCode: 90181, Server: "wdtt.example:56000", VKHashes: []string{"hash-a"}, Clients: clients}
}

func TestStoreSetPersistsAtomicallyAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vkturn.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != nil {
		t.Fatal("a fresh store over a missing file must be OFF (nil) until Set")
	}
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatalf("Set: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("persisted file missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("persisted file mode = %v, want 0600 (secrets)", fi.Mode().Perm())
	}
	// A fresh OpenStore re-parses+validates the persisted file — proving a real round trip.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := s2.Get()
	if got == nil || !got.Enabled || got.Server != "wdtt.example:56000" || len(got.Clients) != 3 {
		t.Fatalf("reloaded config wrong: %+v", got)
	}
}

func TestStoreGetIsIsolatedSnapshot(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	snap := s.Get()
	snap.Enabled = false
	snap.VKHashes[0] = "mutated"
	snap.Clients["wapmix"] = Client{}
	live := s.Get()
	if !live.Enabled || live.VKHashes[0] != "hash-a" || live.Clients["wapmix"].Password == "" {
		t.Fatal("mutating a Get() snapshot leaked into the live store")
	}
}

func TestStoreSetRejectsInvalidAndKeepsPrevious(t *testing.T) {
	s := NewInMemory()
	if err := s.Set(validStoreCfg()); err != nil {
		t.Fatal(err)
	}
	bad := validStoreCfg()
	bad.MinVersionCode = 0 // invalid → Validate must reject
	if err := s.Set(bad); err == nil {
		t.Fatal("Set accepted an invalid config")
	}
	if s.Get().MinVersionCode != 90181 {
		t.Fatal("a rejected edit clobbered the live config (must be fail-closed)")
	}
}

func TestOpenStoreEmptyPathIsNil(t *testing.T) {
	// An unset MAESTRO_VKTURN_FILE (empty path) must yield a nil store so the caller
	// treats WDTT as OFF and the panel guards can fire — never an editable, ephemeral
	// in-memory store that would silently drop config on restart.
	s, err := OpenStore("")
	if err != nil || s != nil {
		t.Fatalf(`OpenStore("") must return (nil, nil): store=%v err=%v`, s, err)
	}
}

func TestOpenStoreMissingIsOffExistingInvalidFailsClosed(t *testing.T) {
	dir := t.TempDir()
	// configured path, missing file → OFF, no error (panel populates it later)
	s, err := OpenStore(filepath.Join(dir, "absent.json"))
	if err != nil || s.Get() != nil {
		t.Fatalf("missing file should start OFF: err=%v cfg=%v", err, s.Get())
	}
	// existing but incomplete file → startup error (fail-closed)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(bad); err == nil {
		t.Fatal("an existing but invalid file must fail startup closed")
	}
}
