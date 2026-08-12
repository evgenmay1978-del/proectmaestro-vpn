package main

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

func receiptFileFixture(t *testing.T) (importer.ImportReceipt, []byte) {
	t.Helper()
	receipt, encoded, err := importer.SignImportReceipt(importer.AppliedRunEvidence{
		RunID:              "receipt-file-run",
		SnapshotKind:       "full",
		SourceDigest:       strings.Repeat("1", 64),
		PlanDigest:         strings.Repeat("2", 64),
		TargetDigest:       strings.Repeat("3", 64),
		BatchCount:         1,
		BatchReceiptDigest: strings.Repeat("4", 64),
		CompletedAtUnix:    1_786_000_000,
	}, controlplane.SchemaIdentity{
		Version: 1, Checksum: strings.Repeat("5", 64),
	}, strings.Repeat("6", 64), ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x71}, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	return receipt, encoded
}

func TestWriteReceiptAtomicUses0600RenameAndDirectorySync(t *testing.T) {
	receipt, encoded := receiptFileFixture(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "receipt.json")
	if err := writeReceiptAtomic(path, receipt); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encoded) {
		t.Fatalf("receipt bytes differ\ngot:  %s\nwant: %s", got, encoded)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("receipt mode=%#o, want 0600", info.Mode().Perm())
		}
	}
	temps, err := filepath.Glob(filepath.Join(directory, ".maestro-import-receipt-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary receipt files=%v error=%v", temps, err)
	}
	if err := writeReceiptAtomic(path, receipt); err != nil {
		t.Fatalf("exact existing receipt is not idempotent: %v", err)
	}
}

func TestWriteReceiptAtomicRejectsConflictingExistingBytesAndSymlink(t *testing.T) {
	receipt, _ := receiptFileFixture(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "receipt.json")
	conflict := []byte("{}")
	if err := os.WriteFile(path, conflict, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeReceiptAtomic(path, receipt); err == nil {
		t.Fatal("conflicting receipt was accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, conflict) {
		t.Fatalf("conflicting receipt changed: %q / %v", got, err)
	}

	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "receipt-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := writeReceiptAtomic(link, receipt); err == nil {
		t.Fatal("receipt symlink destination was accepted")
	}
}
