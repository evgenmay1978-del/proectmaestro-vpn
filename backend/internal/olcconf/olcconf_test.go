package olcconf

import (
	"path/filepath"
	"testing"
)

func TestReadyGate(t *testing.T) {
	cases := []struct {
		c    Config
		want bool
	}{
		{Config{}, false},
		{Config{Enabled: true}, false},                     // no room/key
		{Config{Enabled: true, Room: "r"}, false},          // no key
		{Config{Enabled: true, Key: "k"}, false},           // no room
		{Config{Room: "r", Key: "k"}, false},               // not enabled
		{Config{Enabled: true, Room: "r", Key: "k"}, true}, // ready
	}
	for i, tc := range cases {
		if got := tc.c.Ready(); got != tc.want {
			t.Errorf("case %d: Ready()=%v want %v", i, got, tc.want)
		}
	}
}

func TestSetRoomPersistsAndAutoEnables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "olcrtc.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// No key yet → SetRoom must NOT auto-enable (Ready stays false).
	if err := s.SetRoom("https://telemost.yandex.ru/j/1"); err != nil {
		t.Fatalf("SetRoom: %v", err)
	}
	if s.Get().Ready() {
		t.Fatal("Ready() true with no key")
	}
	// Set the key → now SetRoom auto-enables and Ready holds.
	if err := s.Set(Config{Room: "https://telemost.yandex.ru/j/1", Key: "deadbeef"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.SetRoom("https://telemost.yandex.ru/j/2"); err != nil {
		t.Fatalf("SetRoom: %v", err)
	}
	got := s.Get()
	if !got.Ready() || got.Room != "https://telemost.yandex.ru/j/2" || got.Key != "deadbeef" {
		t.Fatalf("after SetRoom: %+v", got)
	}
	if got.Provider != "telemost" || got.Transport != "vp8channel" {
		t.Fatalf("defaults not applied: %+v", got)
	}

	// Reload from disk → same config persisted.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if g2 := s2.Get(); g2.Room != got.Room || g2.Key != got.Key || !g2.Enabled {
		t.Fatalf("persisted config mismatch: %+v", g2)
	}
}

func TestOpenMissingFileIsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Open(missing): %v", err)
	}
	if s.Get().Ready() {
		t.Fatal("missing file should be not-Ready")
	}
}

func TestRoomForPerLoginAndFallback(t *testing.T) {
	c := Config{
		Enabled: true,
		Room:    "https://telemost.yandex.ru/j/GLOBAL",
		Key:     "globalkey",
		Rooms: map[string]RoomKey{
			"wapmixx": {Room: "https://telemost.yandex.ru/j/B", Key: "keyB"},
			"partial": {Room: "https://telemost.yandex.ru/j/P"}, // no key → not usable
		},
	}
	// Dedicated login → its OWN room+key.
	if room, key, ok := c.RoomFor("wapmixx"); !ok || room != "https://telemost.yandex.ru/j/B" || key != "keyB" {
		t.Fatalf("wapmixx: got (%q,%q,%v)", room, key, ok)
	}
	if !c.Dedicated("wapmixx") {
		t.Fatal("wapmixx should be Dedicated")
	}
	// Login with no entry → global fallback.
	if room, key, ok := c.RoomFor("wapmix"); !ok || room != "https://telemost.yandex.ru/j/GLOBAL" || key != "globalkey" {
		t.Fatalf("wapmix fallback: got (%q,%q,%v)", room, key, ok)
	}
	if c.Dedicated("wapmix") {
		t.Fatal("wapmix should NOT be Dedicated (uses global)")
	}
	// Partial per-login entry (room but no key) → falls back to global, not left broken.
	if room, _, ok := c.RoomFor("partial"); !ok || room != "https://telemost.yandex.ru/j/GLOBAL" {
		t.Fatalf("partial should fall back to global: got (%q,%v)", room, ok)
	}
	// No global + no per-login → nothing to emit.
	empty := Config{Enabled: true, Rooms: map[string]RoomKey{"x": {Room: "r"}}}
	if _, _, ok := empty.RoomFor("y"); ok {
		t.Fatal("no usable room should give ok=false")
	}
	// A login that only has a dedicated room works even with NO global default.
	onlyPer := Config{Enabled: true, Rooms: map[string]RoomKey{"z": {Room: "https://telemost.yandex.ru/j/Z", Key: "kz"}}}
	if room, key, ok := onlyPer.RoomFor("z"); !ok || room != "https://telemost.yandex.ru/j/Z" || key != "kz" {
		t.Fatalf("dedicated-only: got (%q,%q,%v)", room, key, ok)
	}
}

func TestSetRoomForIsolationAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "olcrtc.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Assign wapmixx a dedicated room+key → auto-enables (no global default needed).
	if err := s.SetRoomFor("wapmixx", "https://telemost.yandex.ru/j/B", "keyB"); err != nil {
		t.Fatalf("SetRoomFor: %v", err)
	}
	if !s.Get().Enabled {
		t.Fatal("assigning a per-login room should auto-enable")
	}
	if room, key, ok := s.Get().RoomFor("wapmixx"); !ok || room != "https://telemost.yandex.ru/j/B" || key != "keyB" {
		t.Fatalf("wapmixx after set: (%q,%q,%v)", room, key, ok)
	}
	// Room-only swap keeps the existing key (must not desync the S3 srv).
	if err := s.SetRoomFor("wapmixx", "https://telemost.yandex.ru/j/B2", ""); err != nil {
		t.Fatalf("SetRoomFor room-only: %v", err)
	}
	if room, key, _ := s.Get().RoomFor("wapmixx"); room != "https://telemost.yandex.ru/j/B2" || key != "keyB" {
		t.Fatalf("room-only swap wiped key or missed room: (%q,%q)", room, key)
	}
	// Set a global default, then remove the per-login override → login reverts to global.
	if err := s.Set(mergeGlobal(s.Get(), "https://telemost.yandex.ru/j/G", "gk")); err != nil {
		t.Fatalf("Set global: %v", err)
	}
	if err := s.SetRoomFor("wapmixx", "", ""); err != nil {
		t.Fatalf("SetRoomFor remove: %v", err)
	}
	if s.Get().Dedicated("wapmixx") {
		t.Fatal("empty room should remove the per-login override")
	}
	if room, key, ok := s.Get().RoomFor("wapmixx"); !ok || room != "https://telemost.yandex.ru/j/G" || key != "gk" {
		t.Fatalf("after remove, wapmixx should use global: (%q,%q,%v)", room, key, ok)
	}
	// Persisted across reopen.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if room, _, _ := s2.Get().RoomFor("wapmixx"); room != "https://telemost.yandex.ru/j/G" {
		t.Fatalf("persisted per-login state mismatch: %q", room)
	}
}

// mergeGlobal returns c with the global room/key overridden (keeps Rooms + provider/transport).
func mergeGlobal(c Config, room, key string) Config {
	c.Room = room
	c.Key = key
	return c
}

func TestProviderFor(t *testing.T) {
	// Per-login override wins; falls back to the global provider; then to "telemost".
	c := Config{
		Provider: "telemost",
		Rooms: map[string]RoomKey{
			"maxguy":  {Room: "https://max.ru/call/abc", Key: "k1", Provider: "max"},
			"wbguy":   {Room: "room-id-123", Key: "k2", Provider: "wbstream"},
			"nomatch": {Room: "https://telemost.yandex.ru/j/1", Key: "k3"}, // no override → global
		},
	}
	if got := c.ProviderFor("maxguy"); got != "max" {
		t.Fatalf("maxguy provider = %q, want max", got)
	}
	if got := c.ProviderFor("wbguy"); got != "wbstream" {
		t.Fatalf("wbguy provider = %q, want wbstream", got)
	}
	if got := c.ProviderFor("nomatch"); got != "telemost" {
		t.Fatalf("nomatch provider = %q, want telemost (global)", got)
	}
	if got := (Config{}).ProviderFor("anyone"); got != "telemost" {
		t.Fatalf("empty-config provider = %q, want telemost (default)", got)
	}
}

// SetRoomProviderFor persists a "max" per-login override and keeps it on a room-only re-swap.
func TestSetMaxProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "olcrtc.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SetRoomProviderFor("maxguy", "https://max.ru/call/one", "abcd", "max"); err != nil {
		t.Fatalf("SetRoomProviderFor: %v", err)
	}
	if got := s.Get().ProviderFor("maxguy"); got != "max" {
		t.Fatalf("after set: provider = %q, want max", got)
	}
	// Room-only swap (empty provider) must KEEP the max carrier and the existing key.
	if err := s.SetRoomFor("maxguy", "https://max.ru/call/two", ""); err != nil {
		t.Fatalf("SetRoomFor room-only: %v", err)
	}
	g := s.Get()
	if got := g.ProviderFor("maxguy"); got != "max" {
		t.Fatalf("after room-only swap: provider = %q, want max (kept)", got)
	}
	if room, key, ok := g.RoomFor("maxguy"); !ok || room != "https://max.ru/call/two" || key != "abcd" {
		t.Fatalf("after room-only swap: room=%q key=%q ok=%v", room, key, ok)
	}
}

func TestAllowlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "olcrtc.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SetLogins([]string{"wapmix", "wapmix", "wapmixx"}); err != nil { // dedupes
		t.Fatalf("SetLogins: %v", err)
	}
	if g := s.Get(); len(g.Logins) != 2 || !g.Allowed("wapmix") || !g.Allowed("wapmixx") || g.Allowed("stranger") {
		t.Fatalf("after SetLogins: %+v", g.Logins)
	}
	if err := s.AddLogin("wapmix2"); err != nil {
		t.Fatalf("AddLogin: %v", err)
	}
	if err := s.AddLogin("wapmix"); err != nil { // no-op dup
		t.Fatalf("AddLogin dup: %v", err)
	}
	if g := s.Get(); len(g.Logins) != 3 || !g.Allowed("wapmix2") {
		t.Fatalf("after AddLogin: %+v", g.Logins)
	}
	// RemoveLogin drops the login AND its dedicated room.
	if err := s.SetRoomFor("wapmix2", "https://telemost.yandex.ru/j/Z", "kz"); err != nil {
		t.Fatalf("SetRoomFor: %v", err)
	}
	if err := s.RemoveLogin("wapmix2"); err != nil {
		t.Fatalf("RemoveLogin: %v", err)
	}
	if g := s.Get(); g.Allowed("wapmix2") || g.Dedicated("wapmix2") {
		t.Fatalf("wapmix2 still present after remove: %+v", g)
	}
	// Persisted across reopen.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if g := s2.Get(); !g.Allowed("wapmix") || g.Allowed("wapmix2") {
		t.Fatalf("persisted allowlist mismatch: %+v", g.Logins)
	}
}
