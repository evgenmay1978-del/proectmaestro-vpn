package applyagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStateStoreRoundTripAndRestrictiveMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "marker.json")
	store, err := NewFileStateStore(path)
	if err != nil {
		t.Fatalf("NewFileStateStore: %v", err)
	}
	want := StateMarker{SnapshotSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entries: map[string]EntryMarker{
		"customer-a": {Generation: 4, PayloadSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}}
	if err := store.Store(context.Background(), want); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SnapshotSHA256 != want.SnapshotSHA256 || got.Entries["customer-a"] != want.Entries["customer-a"] {
		t.Fatalf("round trip=%#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("marker permissions=%#o, want no group/other access", info.Mode().Perm())
	}
}

func TestFileStateStoreRejectsCorruptOrNonCanonicalMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker.json")
	if err := os.WriteFile(path, []byte(`{"snapshot_sha256":"bad","entries":{}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store, err := NewFileStateStore(path)
	if err != nil {
		t.Fatalf("NewFileStateStore: %v", err)
	}
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("corrupt marker was accepted")
	}
}

func TestFileStateStoreMissingMarkerIsEmptyState(t *testing.T) {
	store, err := NewFileStateStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("NewFileStateStore: %v", err)
	}
	marker, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load missing marker: %v", err)
	}
	if marker.SnapshotSHA256 != "" || len(marker.Entries) != 0 {
		t.Fatalf("missing marker=%#v, want empty", marker)
	}
}
