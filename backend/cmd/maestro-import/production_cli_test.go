package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

const task6TargetDigest = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

type task6Store struct {
	run              importer.ApplyRun
	completed        bool
	batches          map[int]string
	commitCalls      int
	evidenceOverride *importer.AppliedRunEvidence
}

func (s *task6Store) InspectTarget(context.Context) (importer.TargetState, error) {
	return importer.TargetState{Empty: !s.completed, BusinessDigest: task6TargetDigest}, nil
}

func (s *task6Store) BeginOrResume(_ context.Context, run importer.ApplyRun) (importer.RunProgress, error) {
	if s.run.RunID != "" && s.run != run {
		return importer.RunProgress{}, errors.New("conflicting run")
	}
	s.run = run
	if s.batches == nil {
		s.batches = make(map[int]string)
	}
	copyBatches := make(map[int]string, len(s.batches))
	for index, digest := range s.batches {
		copyBatches[index] = digest
	}
	return importer.RunProgress{
		AppliedBatchDigests: copyBatches,
		Completed:           s.completed,
		TargetDigest:        task6TargetDigest,
	}, nil
}

func (s *task6Store) CommitBatch(_ context.Context, batch importer.ApplyBatch) (importer.BatchReceipt, error) {
	if digest, exists := s.batches[batch.Index]; exists {
		return importer.BatchReceipt{Index: batch.Index, Digest: digest, AlreadyApplied: true}, nil
	}
	s.commitCalls++
	s.batches[batch.Index] = batch.Digest
	return importer.BatchReceipt{Index: batch.Index, Digest: batch.Digest}, nil
}

func (s *task6Store) Complete(_ context.Context, completion importer.ApplyCompletion) error {
	if completion.RunID != s.run.RunID || completion.PlanDigest != s.run.PlanDigest ||
		completion.SourceDigest != s.run.SourceDigest || completion.TargetDigest != task6TargetDigest {
		return errors.New("invalid completion")
	}
	s.completed = true
	return nil
}

func (s *task6Store) ReadAppliedRunEvidence(_ context.Context, runID string) (importer.AppliedRunEvidence, error) {
	if s.evidenceOverride != nil {
		return *s.evidenceOverride, nil
	}
	if !s.completed || runID != s.run.RunID {
		return importer.AppliedRunEvidence{}, errors.New("run is not completed")
	}
	type batchEvidence struct {
		Index  int    `json:"index"`
		Digest string `json:"digest"`
	}
	batches := make([]batchEvidence, s.run.BatchCount)
	for index := range batches {
		digest, exists := s.batches[index]
		if !exists {
			return importer.AppliedRunEvidence{}, errors.New("missing batch")
		}
		batches[index] = batchEvidence{Index: index, Digest: digest}
	}
	encoded, err := json.Marshal(batches)
	if err != nil {
		return importer.AppliedRunEvidence{}, err
	}
	sum := sha256.Sum256(encoded)
	return importer.AppliedRunEvidence{
		RunID:              s.run.RunID,
		SnapshotKind:       s.run.SnapshotKind,
		SourceDigest:       s.run.SourceDigest,
		PlanDigest:         s.run.PlanDigest,
		ParentDigest:       s.run.ParentDigest,
		TargetDigest:       task6TargetDigest,
		BatchCount:         s.run.BatchCount,
		BatchReceiptDigest: hex.EncodeToString(sum[:]),
		CompletedAtUnix:    1_786_000_000,
	}, nil
}

func task6Protected(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("synthetic-protected-input"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func task6Factory(store *task6Store) applyRuntimeFactory {
	return func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		signer := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x61}, ed25519.SeedSize))
		keyID := sha256.Sum256(signer.Public().(ed25519.PublicKey))
		return &applyRuntime{
			Store:              store,
			Schema:             controlplane.SchemaIdentity{Version: 1, Checksum: strings.Repeat("a", 64)},
			TargetConfigSHA256: strings.Repeat("b", 64),
			Signer:             signer,
			SignerKeyID:        hex.EncodeToString(keyID[:]),
		}, nil
	}
}

func task6Command(t *testing.T, store *task6Store) ([]string, string, importer.ImportPlan, applyRuntimeFactory) {
	t.Helper()
	snapshotPath := commandFixture(t, "customers-valid.json")
	snapshotBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := importer.DecodeSnapshot(snapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	plan, report := importer.Plan(snapshot, defaultPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("fixture blockers: %#v", report.Blockers)
	}
	directory := t.TempDir()
	receiptPath := filepath.Join(directory, "receipt.json")
	return []string{
		"--snapshot", snapshotPath,
		"--report", filepath.Join(directory, "report.json"),
		"--mode", "apply",
		"--expected-plan-digest", plan.PlanDigest,
		"--key-file", task6Protected(t, directory, "keys.json"),
		"--rqlite-config", task6Protected(t, directory, "rqlite.json"),
		"--receipt-signing-key-file", task6Protected(t, directory, "receipt-key.json"),
		"--receipt", receiptPath,
		"--run-id", "synthetic-run",
		"--batch-size", "1",
	}, receiptPath, plan, task6Factory(store)
}

func task6ReplaceOption(args []string, name, value string) []string {
	result := append([]string(nil), args...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return result
}

func TestMainPassesNonNilProductionFactory(t *testing.T) {
	if mainApplyRuntimeFactory == nil {
		t.Fatal("main production runtime factory is nil")
	}
}

func TestRunDryRunNeverOpensApplyInputsOrFactory(t *testing.T) {
	snapshot := commandFixture(t, "customers-valid.json")
	directory := t.TempDir()
	called := false
	factory := func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		called = true
		panic("dry-run crossed apply boundary")
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshot,
		"--report", filepath.Join(directory, "report.json"),
		"--mode", "dry-run",
		"--key-file", filepath.Join(directory, "missing-keys"),
		"--rqlite-config", filepath.Join(directory, "missing-target"),
		"--receipt-signing-key-file", filepath.Join(directory, "missing-signer"),
		"--receipt", filepath.Join(directory, "must-not-exist"),
	}, &stdout, &stderr, factory)
	if code != exitClean || called {
		t.Fatalf("run exit=%d factory_called=%v stderr=%q", code, called, stderr.String())
	}
}

func TestRunApplyRequiresTargetReceiptAndSigningPaths(t *testing.T) {
	snapshot := commandFixture(t, "customers-valid.json")
	called := false
	factory := func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		called = true
		return nil, errors.New("unexpected")
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshot,
		"--report", filepath.Join(t.TempDir(), "report.json"),
		"--mode", "apply",
	}, &stdout, &stderr, factory)
	if code != exitInputSystem || called {
		t.Fatalf("run exit=%d factory_called=%v", code, called)
	}
}

func TestRunApplyValidatesLocalInputsBeforeRQLite(t *testing.T) {
	store := &task6Store{}
	args, _, _, factory := task6Command(t, store)
	var keyPath string
	for index := range args {
		if args[index] == "--key-file" {
			keyPath = args[index+1]
		}
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	called := false
	guarded := func(ctx context.Context, config applyRuntimeConfig) (*applyRuntime, error) {
		called = true
		return factory(ctx, config)
	}
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, guarded)
	if code != exitInputSystem || called {
		t.Fatalf("run exit=%d factory_called=%v", code, called)
	}
}

func TestRunSignsOnlyExactReReadCompletedRun(t *testing.T) {
	store := &task6Store{}
	args, receiptPath, _, factory := task6Command(t, store)
	store.evidenceOverride = &importer.AppliedRunEvidence{
		RunID:              "different-run",
		SnapshotKind:       "full",
		SourceDigest:       strings.Repeat("1", 64),
		PlanDigest:         strings.Repeat("2", 64),
		TargetDigest:       task6TargetDigest,
		BatchReceiptDigest: strings.Repeat("3", 64),
		CompletedAtUnix:    1,
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, factory); code != exitInputSystem {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(receiptPath); !os.IsNotExist(err) {
		t.Fatalf("receipt exists for mismatched evidence: %v", err)
	}
}

func TestRunReceiptWriteFailureResumesWithoutSecondBatchMutation(t *testing.T) {
	store := &task6Store{}
	args, receiptPath, _, factory := task6Command(t, store)
	args = task6ReplaceOption(args, "--receipt", filepath.Dir(receiptPath))
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, factory); code != exitInputSystem {
		t.Fatalf("first run exit=%d stderr=%q", code, stderr.String())
	}
	firstCommitCalls := store.commitCalls
	if firstCommitCalls == 0 {
		t.Fatal("first run did not commit any batch")
	}
	stdout.Reset()
	stderr.Reset()
	args = task6ReplaceOption(args, "--receipt", receiptPath)
	if code := run(args, &stdout, &stderr, factory); code != exitClean {
		t.Fatalf("resume exit=%d stderr=%q", code, stderr.String())
	}
	if store.commitCalls != firstCommitCalls {
		t.Fatalf("resume committed batches again: before=%d after=%d", firstCommitCalls, store.commitCalls)
	}
}

func TestRunConflictingExistingReceiptFailsClosed(t *testing.T) {
	store := &task6Store{}
	args, receiptPath, _, factory := task6Command(t, store)
	conflict := []byte("{}")
	if err := os.WriteFile(receiptPath, conflict, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, factory); code != exitInputSystem {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	got, err := os.ReadFile(receiptPath)
	if err != nil || !bytes.Equal(got, conflict) {
		t.Fatalf("conflicting receipt changed: %q / %v", got, err)
	}
}
