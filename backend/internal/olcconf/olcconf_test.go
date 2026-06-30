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
