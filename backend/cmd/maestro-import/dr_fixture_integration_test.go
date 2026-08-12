//go:build rqlite_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

type drSourceMetadata struct {
	FormatVersion       int    `json:"format_version"`
	SourceEpoch         int64  `json:"source_epoch"`
	SchemaVersion       int    `json:"schema_version"`
	SchemaChecksum      string `json:"schema_checksum"`
	RunID               string `json:"run_id"`
	SourceDigest        string `json:"source_digest"`
	PlanDigest          string `json:"plan_digest"`
	TargetDigest        string `json:"target_digest"`
	BatchCount          int    `json:"batch_count"`
	BatchReceiptDigest  string `json:"batch_receipt_digest"`
	ReceiptSHA256       string `json:"receipt_sha256"`
	ShadowSHA256        string `json:"shadow_sha256"`
}

func TestPrepareSyntheticDRSource(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "source" {
		t.Skip("dedicated DR source proof is disabled")
	}
	binary := os.Getenv("MAESTRO_IMPORT_BINARY")
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatal("exact importer binary is unavailable")
	}
	metadataPath := safeDRMetadataOutput(t)
	root := productionMTLSRoot(t)
	db := productionMTLSDB(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	migrator := controlplane.NewMigrator(db)
	if err := migrator.Apply(ctx); err != nil {
		t.Fatalf("apply source schema: %v", err)
	}
	identity, err := migrator.VerifyIdentity(ctx)
	if err != nil {
		t.Fatalf("verify source schema: %v", err)
	}
	store, err := importer.NewRQLiteApplyStore(db, time.Now)
	if err != nil {
		t.Fatalf("new source store: %v", err)
	}
	files := prepareProductionProofFiles(t, root)
	const runID = "dr-source-proof-v1"
	output, code := runProductionBinary(ctx, binary, productionApplyArgs(
		files, files.target, files.keys, files.salt, files.report, files.receipt, runID,
	))
	if code != exitClean {
		t.Fatalf("source apply exit=%d output=%q", code, output)
	}
	receiptBytes := mustReadProofFile(t, files.receipt)
	receipt, err := importer.VerifyImportReceipt(receiptBytes, files.publicKey)
	if err != nil {
		t.Fatalf("verify source receipt: %v", err)
	}
	evidence, err := store.ReadAppliedRunEvidence(ctx, runID)
	if err != nil {
		t.Fatalf("read source evidence: %v", err)
	}
	target, err := store.InspectTarget(ctx)
	if err != nil {
		t.Fatalf("inspect source target: %v", err)
	}
	if target.BusinessDigest != receipt.TargetDigest || evidence.TargetDigest != receipt.TargetDigest ||
		evidence.BatchReceiptDigest != receipt.BatchReceiptDigest || evidence.BatchCount != receipt.BatchCount {
		t.Fatal("source receipt, evidence and business digest differ")
	}
	shapes := importer.ShadowURLShapes{
		Maestro: "maestro://import/{opaque-token}",
		Karing:  "https://proof.invalid/sub/{opaque-token}",
	}
	shadow, err := importer.ShadowFromCandidate(ctx, store, receipt.SourceDigest, shapes)
	if err != nil {
		t.Fatalf("source shadow: %v", err)
	}
	shadowBytes, err := importer.EncodeShadowExport(shadow)
	if err != nil {
		t.Fatalf("encode source shadow: %v", err)
	}
	state, err := controlplane.NewRestoreEpochStore(db).Current(ctx)
	if err != nil || !state.Activated || state.RestoreEpoch <= 0 {
		t.Fatalf("source restore state: %#v, %v", state, err)
	}
	metadata := drSourceMetadata{
		FormatVersion: 1, SourceEpoch: state.RestoreEpoch,
		SchemaVersion: identity.Version, SchemaChecksum: identity.Checksum,
		RunID: runID, SourceDigest: receipt.SourceDigest, PlanDigest: receipt.PlanDigest,
		TargetDigest: receipt.TargetDigest, BatchCount: receipt.BatchCount,
		BatchReceiptDigest: receipt.BatchReceiptDigest,
		ReceiptSHA256: sha256HexDR(receiptBytes), ShadowSHA256: sha256HexDR(shadowBytes),
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(productionIdentityMarker)) ||
		bytes.Contains(encoded, []byte(productionTrialMarker)) {
		t.Fatal("redacted DR metadata contains a raw marker")
	}
	file, err := os.OpenFile(metadataPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create DR metadata: %v", err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		t.Fatalf("write DR metadata: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync DR metadata: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close DR metadata: %v", err)
	}
}

func safeDRMetadataOutput(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	if err != nil || !filepath.IsAbs(base) {
		t.Fatal("RUNNER_TEMP is unavailable")
	}
	path := os.Getenv("MAESTRO_DR_METADATA")
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || parent != base || filepath.Base(path) != "dr-source-metadata.json" {
		t.Fatal("DR metadata path is outside the runner root")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("DR metadata output already exists or is unsafe")
	}
	return path
}

func sha256HexDR(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
