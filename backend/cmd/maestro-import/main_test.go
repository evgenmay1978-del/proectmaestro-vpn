package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

type cliApplyStore struct {
	run       importer.ApplyRun
	completed bool
}

func (s *cliApplyStore) InspectTarget(context.Context) (importer.TargetState, error) {
	return importer.TargetState{Empty: true, BusinessDigest: "synthetic-target-digest"}, nil
}

func (s *cliApplyStore) BeginOrResume(_ context.Context, run importer.ApplyRun) (importer.RunProgress, error) {
	s.run = run
	return importer.RunProgress{AppliedBatchDigests: make(map[int]string)}, nil
}

func (s *cliApplyStore) CommitBatch(_ context.Context, batch importer.ApplyBatch) (importer.BatchReceipt, error) {
	return importer.BatchReceipt{Index: batch.Index, Digest: batch.Digest}, nil
}

func (s *cliApplyStore) Complete(_ context.Context, completion importer.ApplyCompletion) error {
	s.completed = true
	return nil
}

func commandFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "importer", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), filepath.Base(name))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestRunRequiresExplicitSnapshotReportAndMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, nil); code != exitInputSystem {
		t.Fatalf("run exit = %d, want %d", code, exitInputSystem)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunDryRunWritesImmutableRedactedReport(t *testing.T) {
	snapshot := commandFixture(t, "customers-valid.json")
	reportPath := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshot,
		"--report", reportPath,
		"--mode", "dry-run",
	}, &stdout, &stderr, nil)
	if code != exitClean {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report importer.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.PlanDigest == "" || report.SourceDigest == "" || len(report.Blockers) != 0 {
		t.Fatalf("report = %#v", report)
	}
	if bytes.Contains(data, []byte("c3ludGhldGljLWNpcGhlcnRleHQ=")) {
		t.Fatalf("report leaked encrypted payload bytes: %s", data)
	}
}

func TestRunReturnsBlockerExitAndStillWritesReport(t *testing.T) {
	snapshot := commandFixture(t, "collisions.json")
	reportPath := filepath.Join(t.TempDir(), "blockers.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshot,
		"--report", reportPath,
		"--mode", "dry-run",
	}, &stdout, &stderr, nil)
	if code != exitBlockers {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report importer.Report
	if err := json.Unmarshal(data, &report); err != nil || len(report.Blockers) == 0 {
		t.Fatalf("blocker report/error = %#v / %v", report, err)
	}
}

func TestRunApplyRequiresExpectedDigestAndProtectedFiles(t *testing.T) {
	snapshot := commandFixture(t, "customers-valid.json")
	reportPath := filepath.Join(t.TempDir(), "report.json")
	called := false
	factory := func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		called = true
		return &applyRuntime{}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshot,
		"--report", reportPath,
		"--mode", "apply",
	}, &stdout, &stderr, factory)
	if code != exitInputSystem || called {
		t.Fatalf("run exit=%d factory_called=%v", code, called)
	}
}

func TestRunApplyRejectsExpectedPlanDigestDriftBeforeStore(t *testing.T) {
	snapshot := commandFixture(t, "customers-valid.json")
	temp := t.TempDir()
	keyPath := filepath.Join(temp, "key")
	saltPath := filepath.Join(temp, "legacy-salt")
	for _, path := range []string{keyPath, saltPath} {
		if err := os.WriteFile(path, []byte("synthetic-protected-input"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	factory := func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		called = true
		return &applyRuntime{}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshot,
		"--report", filepath.Join(temp, "report.json"),
		"--mode", "apply",
		"--expected-plan-digest", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"--key-file", keyPath,
		"--legacy-trial-salt-file", saltPath,
		"--run-id", "synthetic-run",
	}, &stdout, &stderr, factory)
	if code != exitInputSystem || called {
		t.Fatalf("run exit=%d factory_called=%v", code, called)
	}
}

func TestRunApplyCannotUseIncompleteRuntime(t *testing.T) {
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
	temp := t.TempDir()
	factory := func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		return &applyRuntime{}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--snapshot", snapshotPath,
		"--report", filepath.Join(temp, "report.json"),
		"--mode", "apply",
		"--expected-plan-digest", plan.PlanDigest,
		"--key-file", protectedFixtureForLegacyTest(t, temp, "keys"),
		"--rqlite-config", protectedFixtureForLegacyTest(t, temp, "target"),
		"--receipt-signing-key-file", protectedFixtureForLegacyTest(t, temp, "signer"),
		"--receipt", filepath.Join(temp, "receipt.json"),
		"--run-id", "synthetic-run",
	}, &stdout, &stderr, factory)
	if code != exitInputSystem {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
}

func protectedFixtureForLegacyTest(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("synthetic-protected-input"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
