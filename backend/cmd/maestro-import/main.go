package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

const (
	exitClean          = 0
	exitBlockers       = 2
	exitInputSystem    = 3
	maxSnapshotSize    = 64 << 20
	importCommandLimit = 30 * time.Minute
)

type applyRuntimeFactory func(context.Context, applyRuntimeConfig) (*applyRuntime, error)

var mainApplyRuntimeFactory applyRuntimeFactory = productionApplyRuntimeFactory

type cliOptions struct {
	SnapshotPath          string
	ParentSnapshotPath    string
	ReportPath            string
	Mode                  string
	ExpectedPlanDigest    string
	AppliedParentDigest   string
	KeyFile               string
	LegacyTrialSaltFile   string
	RQLiteConfigFile      string
	ReceiptPath           string
	ReceiptSigningKeyFile string
	RunID                 string
	BatchSize             int
}

func main() {
	os.Exit(run(
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		mainApplyRuntimeFactory,
	))
}

func run(args []string, stdout, stderr io.Writer, factory applyRuntimeFactory) int {
	if len(args) > 0 {
		switch args[0] {
		case "normalize":
			return runNormalize(args[1:], stdout, stderr)
		case "capture-xui":
			return runCaptureXUI(args[1:], stdout, stderr)
		}
	}
	options, err := parseOptions(args, stderr)
	if err != nil {
		writeError(stderr, "invalid arguments")
		return exitInputSystem
	}

	snapshotBytes, err := readBounded(options.SnapshotPath, maxSnapshotSize)
	if err != nil {
		writeError(stderr, "snapshot is unavailable")
		return exitInputSystem
	}
	snapshot, err := importer.DecodeSnapshot(snapshotBytes)
	zero(snapshotBytes)
	if err != nil {
		writeError(stderr, "snapshot is invalid")
		return exitInputSystem
	}
	if options.Mode == "apply" {
		if _, preparation := snapshot.SourceHashes["scope:"+importer.LegacyCustomerPreparationScope]; preparation {
			writeError(stderr, "customer preparation snapshot is not a complete cutover input")
			return exitInputSystem
		}
	}

	planOptions := defaultPlanOptions()
	planOptions.AppliedParentDigest = options.AppliedParentDigest
	if snapshot.SnapshotKind == "delta" {
		if options.ParentSnapshotPath == "" || options.AppliedParentDigest == "" {
			writeError(stderr, "delta parent is required")
			return exitInputSystem
		}
		parentBytes, readErr := readBounded(options.ParentSnapshotPath, maxSnapshotSize)
		if readErr != nil {
			writeError(stderr, "parent snapshot is unavailable")
			return exitInputSystem
		}
		parent, decodeErr := importer.DecodeSnapshot(parentBytes)
		zero(parentBytes)
		if decodeErr != nil || parent.SnapshotKind != "full" {
			writeError(stderr, "parent snapshot is invalid")
			return exitInputSystem
		}
		planOptions.ParentSnapshot = &parent
	}

	plan, report := importer.Plan(snapshot, planOptions)
	if err := writeReport(options.ReportPath, report); err != nil {
		writeError(stderr, "report cannot be written")
		return exitInputSystem
	}
	if len(report.Blockers) != 0 {
		_, _ = fmt.Fprintln(stdout, "import plan blocked; see explicit report")
		return exitBlockers
	}
	if options.Mode == "dry-run" {
		_, _ = fmt.Fprintln(stdout, "import plan clean; no writes performed")
		return exitClean
	}

	if !validDigest(options.ExpectedPlanDigest) || options.ExpectedPlanDigest != plan.PlanDigest {
		writeError(stderr, "expected plan digest does not match")
		return exitInputSystem
	}
	protection := importer.ProtectionFromSnapshot(snapshot, planOptions.ParentSnapshot)
	if !validApplyOptions(options, protection.HasTrials) {
		writeError(stderr, "apply requires run id and protected inputs")
		return exitInputSystem
	}
	if err := preflightApplyFiles(options, protection.HasTrials); err != nil {
		writeError(stderr, "protected apply input is invalid")
		return exitInputSystem
	}
	if factory == nil {
		writeError(stderr, "apply runtime is not configured")
		return exitInputSystem
	}

	commandContext, cancel := context.WithTimeout(context.Background(), importCommandLimit)
	defer cancel()
	runtime, err := factory(commandContext, applyRuntimeConfig{
		TargetConfigFile:    options.RQLiteConfigFile,
		KeyBundleFile:       options.KeyFile,
		LegacyTrialSaltFile: options.LegacyTrialSaltFile,
		ReceiptSigningFile:  options.ReceiptSigningKeyFile,
		Protection:          protection,
	})
	if err != nil || runtime == nil {
		writeError(stderr, "apply runtime is unavailable")
		return exitInputSystem
	}
	defer zero(runtime.Signer)
	if runtime.Store == nil {
		writeError(stderr, "apply runtime is unavailable")
		return exitInputSystem
	}

	result, err := importer.Apply(commandContext, runtime.Store, plan, importer.ApplyOptions{
		RunID: options.RunID, BatchSize: options.BatchSize,
	})
	if err != nil {
		writeError(stderr, "apply failed")
		return exitInputSystem
	}
	evidence, err := runtime.Store.ReadAppliedRunEvidence(commandContext, options.RunID)
	if err != nil || !evidenceMatchesPlan(evidence, plan, result, options.RunID) {
		writeError(stderr, "completed run evidence does not match")
		return exitInputSystem
	}
	receipt, _, err := importer.SignImportReceipt(
		evidence,
		runtime.Schema,
		runtime.TargetConfigSHA256,
		runtime.Signer,
	)
	if err != nil || receipt.SignerKeyID != runtime.SignerKeyID {
		writeError(stderr, "receipt cannot be signed")
		return exitInputSystem
	}
	if err := writeReceiptAtomic(options.ReceiptPath, receipt); err != nil {
		writeError(stderr, "receipt cannot be persisted")
		return exitInputSystem
	}

	_, _ = fmt.Fprintln(stdout, "import apply completed; signed receipt persisted")
	return exitClean
}

func validApplyOptions(options cliOptions, hasTrials bool) bool {
	if strings.TrimSpace(options.RunID) == "" ||
		strings.TrimSpace(options.KeyFile) == "" ||
		strings.TrimSpace(options.RQLiteConfigFile) == "" ||
		strings.TrimSpace(options.ReceiptPath) == "" ||
		strings.TrimSpace(options.ReceiptSigningKeyFile) == "" {
		return false
	}
	if hasTrials {
		return strings.TrimSpace(options.LegacyTrialSaltFile) != ""
	}
	return strings.TrimSpace(options.LegacyTrialSaltFile) == ""
}

func preflightApplyFiles(options cliOptions, hasTrials bool) error {
	paths := []string{
		options.RQLiteConfigFile,
		options.KeyFile,
		options.ReceiptSigningKeyFile,
	}
	if hasTrials {
		paths = append(paths, options.LegacyTrialSaltFile)
	}
	for _, path := range paths {
		data, err := readProtected(path)
		if err != nil {
			return err
		}
		zero(data)
	}
	return nil
}

func evidenceMatchesPlan(
	evidence importer.AppliedRunEvidence,
	plan importer.ImportPlan,
	result importer.ApplyResult,
	runID string,
) bool {
	return evidence.RunID == runID &&
		evidence.SnapshotKind == plan.SnapshotKind &&
		evidence.SourceDigest == plan.SourceDigest &&
		evidence.PlanDigest == plan.PlanDigest &&
		evidence.ParentDigest == plan.ParentSourceDigest &&
		evidence.TargetDigest == result.TargetDigest &&
		evidence.BatchCount == result.AppliedBatches
}

func defaultPlanOptions() importer.PlanOptions {
	return importer.PlanOptions{
		Namespace:             "maestro-legacy-v1",
		SupportedBotSchemas:   []string{"bot-schema-v1"},
		SupportedProtocolTags: []string{"vless", "hysteria2", "anytls", "naive", "awg", "wdtt", "olcrtc"},
		SupportedNodeIDs:      []string{"S1", "S2", "S3", "S4"},
	}
}

func parseOptions(args []string, stderr io.Writer) (cliOptions, error) {
	var options cliOptions
	flags := flag.NewFlagSet("maestro-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.SnapshotPath, "snapshot", "", "explicit normalized snapshot path")
	flags.StringVar(&options.ParentSnapshotPath, "parent-snapshot", "", "explicit full parent snapshot path for delta")
	flags.StringVar(&options.ReportPath, "report", "", "explicit immutable report path")
	flags.StringVar(&options.Mode, "mode", "", "dry-run or apply")
	flags.StringVar(&options.ExpectedPlanDigest, "expected-plan-digest", "", "exact approved plan digest")
	flags.StringVar(&options.AppliedParentDigest, "applied-parent-digest", "", "exact applied parent source digest")
	flags.StringVar(&options.KeyFile, "key-file", "", "protected versioned key-bundle path")
	flags.StringVar(&options.LegacyTrialSaltFile, "legacy-trial-salt-file", "", "protected legacy trial salt path")
	flags.StringVar(&options.RQLiteConfigFile, "rqlite-config", "", "protected rqlite mTLS target configuration path")
	flags.StringVar(&options.ReceiptPath, "receipt", "", "exclusive signed receipt destination")
	flags.StringVar(&options.ReceiptSigningKeyFile, "receipt-signing-key-file", "", "protected Ed25519 receipt signing key path")
	flags.StringVar(&options.RunID, "run-id", "", "stable import run id")
	flags.IntVar(&options.BatchSize, "batch-size", 100, "deterministic operations per batch")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return cliOptions{}, errors.New("invalid command line")
	}
	if strings.TrimSpace(options.SnapshotPath) == "" || strings.TrimSpace(options.ReportPath) == "" ||
		(options.Mode != "dry-run" && options.Mode != "apply") || options.BatchSize <= 0 {
		return cliOptions{}, errors.New("required command line option is missing")
	}
	return options, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := &io.LimitedReader{R: file, N: limit + 1}
	data, err := io.ReadAll(reader)
	if err != nil || int64(len(data)) > limit {
		zero(data)
		return nil, errors.New("bounded read failed")
	}
	return data, nil
}

func readProtected(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, errors.New("invalid protected file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("protected file permissions are too broad")
	}
	data, err := readBounded(path, 1<<20)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		zero(data)
		return nil, errors.New("protected file is empty")
	}
	return data, nil
}

func writeReport(path string, report importer.Report) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".maestro-import-report-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(encoded); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func zero(data []byte) {
	for index := range data {
		data[index] = 0
	}
}

func writeError(stderr io.Writer, message string) {
	_, _ = fmt.Fprintln(stderr, "maestro-import:", message)
}
