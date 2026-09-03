package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReceiptExpiresAndRefreshRecoversAfterProcessRestart(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	bootID := "boot-a"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed", "wl:one:exit-s1")
	reconciler, store := testReconciler(t, handler, &now, &bootID)
	desired := testDesired(t, 7, "release-12", strings.Repeat("a", 64), "wl:one:exit-s1")
	first, err := reconciler.Apply(context.Background(), desired)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	now = now.Add(31 * time.Second)
	if first.ReadyAt(now) {
		t.Fatal("expired receipt remained ready")
	}
	refreshed, err := reconciler.Refresh(context.Background())
	if err != nil || !refreshed.ReadyAt(now) || refreshed.XrayProcessBootID != bootID {
		t.Fatalf("Refresh receipt=%#v err=%v", refreshed, err)
	}

	bootID = "boot-b"
	now = now.Add(10 * time.Second)
	if err := store.InvalidateReceiptsExceptBoot(bootID); err != nil {
		t.Fatalf("InvalidateReceiptsExceptBoot: %v", err)
	}
	if _, err := store.LoadReceipt(desired.ActionKey()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("prior-process receipt survived invalidation: %v", err)
	}
	recovered, err := reconciler.Recover(context.Background())
	if err != nil || recovered.XrayProcessBootID != bootID || !recovered.ReadyAt(now) {
		t.Fatalf("Recover receipt=%#v err=%v", recovered, err)
	}
}

func TestFileStoreUsesPrivateAtomicFilesAndBoundedReceiptRetention(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, 2)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for generation := int64(1); generation <= 3; generation++ {
		desired := testDesired(t, generation, "release-12", strings.Repeat("a", 64), "wl:one:exit-s1")
		if err := store.SaveDesired(desired); err != nil {
			t.Fatalf("SaveDesired(%d): %v", generation, err)
		}
		receipt := Receipt{
			ActionKey: desired.ActionKey(), OriginID: desired.OriginID, ReleaseID: desired.ReleaseID,
			XrayProcessBootID: "boot-a", ConfigDigest: desired.ConfigDigest,
			DesiredGeneration: desired.Generation, ManagedUserSetDigest: desired.ManagedUserSetDigest,
			AppliedAt: time.Unix(generation, 0).UTC(), ExpiresAt: time.Unix(generation+30, 0).UTC(),
		}
		if err := store.SaveReceipt(receipt); err != nil {
			t.Fatalf("SaveReceipt(%d): %v", generation, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	receiptFiles := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "receipt-") {
			receiptFiles++
		}
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("Info(%s): %v", entry.Name(), err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", entry.Name(), info.Mode().Perm())
		}
	}
	if receiptFiles != 2 {
		t.Fatalf("receipt files = %d, want 2", receiptFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, currentDesiredFile)); err != nil {
		t.Fatalf("current desired missing: %v", err)
	}
}
