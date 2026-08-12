//go:build rqlite_integration

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	productionIdentityMarker = "PRODUCTION-IDENTITY-RAW-MARKER-7Q9"
	productionTrialMarker    = "PRODUCTION-TRIAL-SALT-RAW-MARKER-4X2"
)

type productionProofFiles struct {
	snapshot   string
	target     string
	keys       string
	salt       string
	signer     string
	receipt    string
	report     string
	plan       importer.ImportPlan
	publicKey ed25519.PublicKey
}

func TestPrepareProductionImportSchemaMTLS(t *testing.T) {
	if os.Getenv("MAESTRO_IMPORT_SCHEMA_PREP") != "1" {
		t.Skip("dedicated mTLS schema preparation is disabled")
	}
	root := productionMTLSRoot(t)
	db := productionMTLSDB(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	migrator := controlplane.NewMigrator(db)
	if err := migrator.Apply(ctx); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := migrator.VerifyIdentity(ctx); err != nil {
		t.Fatalf("verify schema identity: %v", err)
	}
}

func TestProductionImportFactoryBinaryProof(t *testing.T) {
	binary := os.Getenv("MAESTRO_IMPORT_BINARY")
	if binary == "" {
		t.Skip("production importer binary proof is disabled")
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatal("production importer binary is unavailable or unsafe")
	}

	root := productionMTLSRoot(t)
	db := productionMTLSDB(t, root)
	store, err := importer.NewRQLiteApplyStore(db, time.Now)
	if err != nil {
		t.Fatalf("new proof store: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	files := prepareProductionProofFiles(t, root)
	var captured [][]byte

	dryDirectory := t.TempDir()
	dryReport := filepath.Join(dryDirectory, "dry-report.json")
	dryArgs := []string{
		"--snapshot", files.snapshot,
		"--report", dryReport,
		"--mode", "dry-run",
		"--rqlite-config", filepath.Join(dryDirectory, "missing-target"),
		"--key-file", filepath.Join(dryDirectory, "missing-keys"),
		"--legacy-trial-salt-file", filepath.Join(dryDirectory, "missing-salt"),
		"--receipt-signing-key-file", filepath.Join(dryDirectory, "missing-signer"),
		"--receipt", filepath.Join(dryDirectory, "must-not-exist"),
	}
	output, code := runProductionBinary(ctx, binary, dryArgs)
	captured = append(captured, output, mustReadProofFile(t, dryReport))
	if code != exitClean {
		t.Fatalf("dry-run exit=%d output=%q", code, output)
	}
	if _, err := os.Stat(filepath.Join(dryDirectory, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("dry-run touched receipt destination: %v", err)
	}

	assertEmpty := func(label string) {
		t.Helper()
		state, err := store.InspectTarget(ctx)
		if err != nil {
			t.Fatalf("%s inspect target: %v", label, err)
		}
		if !state.Empty {
			t.Fatalf("%s mutated business target", label)
		}
	}
	assertRejected := func(label, target, keys, salt string) {
		t.Helper()
		directory := t.TempDir()
		report := filepath.Join(directory, "report.json")
		receipt := filepath.Join(directory, "receipt.json")
		args := productionApplyArgs(files, target, keys, salt, report, receipt, "rejected-"+label)
		output, code := runProductionBinary(ctx, binary, args)
		captured = append(captured, output, mustReadProofFile(t, report))
		if code != exitInputSystem {
			t.Fatalf("%s exit=%d output=%q", label, code, output)
		}
		if _, err := os.Stat(receipt); !os.IsNotExist(err) {
			t.Fatalf("%s created receipt: %v", label, err)
		}
		assertEmpty(label)
	}

	wrongKeys := writeProofJSON(t, "wrong-keys.json", proofKeyBundle(bytes.Repeat([]byte{0x52}, 32)))
	wrongSalt := writeProofBytes(t, "wrong-salt", []byte("different-trial-salt"))
	wrongClientTarget := writeProofJSON(t, "wrong-client-target.json", proofTargetConfig(
		root,
		filepath.Join(root, "tls", "server.crt"),
		filepath.Join(root, "tls", "server.key"),
		"https",
	))
	httpTarget := writeProofJSON(t, "http-target.json", proofTargetConfig(
		root,
		filepath.Join(root, "tls", "client.crt"),
		filepath.Join(root, "tls", "client.key"),
		"http",
	))
	assertRejected("wrong-hmac", files.target, wrongKeys, files.salt)
	assertRejected("wrong-salt", files.target, files.keys, wrongSalt)
	assertRejected("wrong-client", wrongClientTarget, files.keys, files.salt)
	assertRejected("http-target", httpTarget, files.keys, files.salt)

	args := productionApplyArgs(
		files,
		files.target,
		files.keys,
		files.salt,
		files.report,
		files.receipt,
		"production-binary-proof-v1",
	)
	output, code = runProductionBinary(ctx, binary, args)
	captured = append(captured, output, mustReadProofFile(t, files.report))
	if code != exitClean {
		t.Fatalf("valid apply exit=%d output=%q", code, output)
	}
	receiptBytes := mustReadProofFile(t, files.receipt)
	captured = append(captured, receiptBytes)
	receipt, err := importer.VerifyImportReceipt(receiptBytes, files.publicKey)
	if err != nil {
		t.Fatalf("verify signed receipt: %v", err)
	}
	if receipt.RunID != "production-binary-proof-v1" ||
		receipt.SourceDigest != files.plan.SourceDigest ||
		receipt.PlanDigest != files.plan.PlanDigest {
		t.Fatalf("receipt does not identify approved plan: %#v", receipt)
	}
	evidenceBefore, err := store.ReadAppliedRunEvidence(ctx, receipt.RunID)
	if err != nil {
		t.Fatalf("read applied evidence: %v", err)
	}
	if evidenceBefore.TargetDigest != receipt.TargetDigest ||
		evidenceBefore.BatchReceiptDigest != receipt.BatchReceiptDigest ||
		evidenceBefore.BatchCount != receipt.BatchCount {
		t.Fatalf("receipt/evidence mismatch: %#v / %#v", receipt, evidenceBefore)
	}

	shapes := importer.ShadowURLShapes{
		Maestro: "maestro://import/{opaque-token}",
		Karing:  "https://proof.invalid/sub/{opaque-token}",
	}
	legacyShadow, err := importer.ShadowFromPlan(files.plan, shapes)
	if err != nil {
		t.Fatalf("legacy shadow: %v", err)
	}
	candidateShadow, err := importer.ShadowFromCandidate(ctx, store, files.plan.SourceDigest, shapes)
	if err != nil {
		t.Fatalf("candidate shadow: %v", err)
	}
	if !reflect.DeepEqual(candidateShadow, legacyShadow) {
		t.Fatalf("shadow parity mismatch\nlegacy=%#v\ncandidate=%#v", legacyShadow, candidateShadow)
	}

	secondOutput, secondCode := runProductionBinary(ctx, binary, args)
	captured = append(captured, secondOutput)
	if secondCode != exitClean {
		t.Fatalf("resume exit=%d output=%q", secondCode, secondOutput)
	}
	secondReceipt := mustReadProofFile(t, files.receipt)
	if !bytes.Equal(secondReceipt, receiptBytes) {
		t.Fatal("idempotent resume changed exact receipt bytes")
	}
	evidenceAfter, err := store.ReadAppliedRunEvidence(ctx, receipt.RunID)
	if err != nil {
		t.Fatalf("read resumed evidence: %v", err)
	}
	if evidenceAfter != evidenceBefore {
		t.Fatalf("resume changed applied evidence: before=%#v after=%#v", evidenceBefore, evidenceAfter)
	}

	combined := bytes.Join(captured, []byte("\n"))
	for _, marker := range []string{productionIdentityMarker, productionTrialMarker} {
		if bytes.Contains(combined, []byte(marker)) {
			t.Fatalf("production binary proof leaked a raw marker")
		}
	}
}

func productionMTLSRoot(t *testing.T) string {
	t.Helper()
	base := os.Getenv("RUNNER_TEMP")
	if base == "" {
		t.Fatal("RUNNER_TEMP is required")
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("resolve runner temp: %v", err)
	}
	marker := filepath.Join(resolvedBase, "maestro-rqlite-ci-root")
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("safe rqlite marker is missing")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatal("rqlite marker is not one canonical path")
	}
	root, err := filepath.EvalSymlinks(lines[0])
	if err != nil || root != lines[0] || filepath.Dir(root) != resolvedBase ||
		!strings.HasPrefix(filepath.Base(root), "maestro-rqlite-ci.") {
		t.Fatal("rqlite root escaped runner temp")
	}
	mode, err := os.ReadFile(filepath.Join(root, "mode"))
	if err != nil || string(mode) != "mtls\n" {
		t.Fatal("rqlite cluster is not in mTLS mode")
	}
	return root
}

func productionMTLSDB(t *testing.T, root string) rqlite.RQLite {
	t.Helper()
	db, err := rqlite.New(rqlite.Config{
		Endpoints: []string{
			"https://127.0.0.1:4401",
			"https://127.0.0.1:4403",
			"https://127.0.0.1:4405",
		},
		CAFile:           filepath.Join(root, "tls", "ca.crt"),
		CertFile:         filepath.Join(root, "tls", "client.crt"),
		KeyFile:          filepath.Join(root, "tls", "client.key"),
		Timeout:          10 * time.Second,
		MaxResponseBytes: 8 << 20,
		MaxBackupBytes:   4 << 30,
	})
	if err != nil {
		t.Fatalf("new mTLS rqlite client: %v", err)
	}
	return db
}

func prepareProductionProofFiles(t *testing.T, root string) productionProofFiles {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", "production-full-v2.json"))
	if err != nil {
		t.Fatalf("read production fixture: %v", err)
	}
	snapshot, err := importer.DecodeSnapshot(fixture)
	if err != nil {
		t.Fatalf("decode production fixture: %v", err)
	}
	encryptionKey := bytes.Repeat([]byte{0x41}, 32)
	hmacKey := bytes.Repeat([]byte{0x51}, 32)
	trialSalt := []byte(productionTrialMarker)
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: encryptionKey}, hmacKey)
	if err != nil {
		t.Fatalf("new proof secret box: %v", err)
	}
	plaintext := []byte(productionIdentityMarker)
	envelope, err := box.Seal(controlplane.SecretScope{
		OwnerType: "customer",
		OwnerID:   "s1:customer:production-binary-1",
		Field:     "identity",
		Kind:      "customer-identity",
	}, plaintext)
	if err != nil {
		t.Fatalf("seal production identity: %v", err)
	}
	snapshot.ClusterHMACKeySHA256 = proofSHA256(hmacKey)
	snapshot.LegacyTrialSaltSHA256 = proofSHA256(trialSalt)
	snapshot.EncryptedSecrets[0].KeyVersion = envelope.KeyVersion
	snapshot.EncryptedSecrets[0].NonceB64 = base64.StdEncoding.EncodeToString(envelope.Nonce)
	snapshot.EncryptedSecrets[0].CiphertextB64 = base64.StdEncoding.EncodeToString(envelope.Ciphertext)
	snapshot.EncryptedSecrets[0].SHA256 = proofSHA256(plaintext)
	plan, report := importer.Plan(snapshot, defaultPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("production fixture blockers: %#v", report.Blockers)
	}

	snapshotPath := writeProofJSON(t, "production-full-v2.json", snapshot)
	targetPath := writeProofJSON(t, "target.json", proofTargetConfig(
		root,
		filepath.Join(root, "tls", "client.crt"),
		filepath.Join(root, "tls", "client.key"),
		"https",
	))
	keyPath := writeProofJSON(t, "keys.json", proofKeyBundle(hmacKey))
	saltPath := writeProofBytes(t, "trial-salt", trialSalt)
	seed := bytes.Repeat([]byte{0x61}, ed25519.SeedSize)
	signerPath := writeProofJSON(t, "receipt-key.json", map[string]any{
		"schema_version": 1,
		"seed_b64":       base64.StdEncoding.EncodeToString(seed),
	})
	directory := t.TempDir()
	return productionProofFiles{
		snapshot:   snapshotPath,
		target:     targetPath,
		keys:       keyPath,
		salt:       saltPath,
		signer:     signerPath,
		receipt:    filepath.Join(directory, "receipt.json"),
		report:     filepath.Join(directory, "report.json"),
		plan:       plan,
		publicKey: ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey),
	}
}

func proofTargetConfig(root, certificate, key, scheme string) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"voters": []any{
			map[string]any{"node_id": "S2", "url": scheme + "://127.0.0.1:4401"},
			map[string]any{"node_id": "S3", "url": scheme + "://127.0.0.1:4403"},
			map[string]any{"node_id": "S4", "url": scheme + "://127.0.0.1:4405"},
		},
		"ca_file":         filepath.Join(root, "tls", "ca.crt"),
		"cert_file":       certificate,
		"key_file":        key,
		"timeout_seconds": 10,
	}
}

func proofKeyBundle(hmacKey []byte) map[string]any {
	return map[string]any{
		"schema_version":      1,
		"current_key_version": 1,
		"encryption_keys": []any{
			map[string]any{
				"version": 1,
				"key_b64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 32)),
			},
		},
		"hmac_key_b64": base64.StdEncoding.EncodeToString(hmacKey),
	}
}

func productionApplyArgs(
	files productionProofFiles,
	target, keys, salt, report, receipt, runID string,
) []string {
	return []string{
		"--snapshot", files.snapshot,
		"--report", report,
		"--mode", "apply",
		"--expected-plan-digest", files.plan.PlanDigest,
		"--rqlite-config", target,
		"--key-file", keys,
		"--legacy-trial-salt-file", salt,
		"--receipt-signing-key-file", files.signer,
		"--receipt", receipt,
		"--run-id", runID,
		"--batch-size", "2",
	}
}

func runProductionBinary(ctx context.Context, binary string, args []string) ([]byte, int) {
	command := exec.CommandContext(ctx, binary, args...)
	output, err := command.CombinedOutput()
	if err == nil {
		return output, 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return output, exitError.ExitCode()
	}
	return append(output, []byte("\nsubprocess unavailable")...), -1
}

func writeProofJSON(t *testing.T, name string, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return writeProofBytes(t, name, encoded)
}

func writeProofBytes(t *testing.T, name string, value []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustReadProofFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read proof artifact: %v", err)
	}
	return data
}

func proofSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
