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

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
)

const (
	exitClean       = 0
	exitBlockers    = 2
	exitInputSystem = 3
	maxSnapshotSize = 64 << 20
)

type applyConfig struct {
	KeyFile             string
	LegacyTrialSaltFile string
}

type applyStoreFactory func(context.Context, applyConfig) (importer.ApplyStore, error)

type cliOptions struct {
	SnapshotPath        string
	ParentSnapshotPath  string
	ReportPath          string
	Mode                string
	ExpectedPlanDigest  string
	AppliedParentDigest string
	KeyFile             string
	LegacyTrialSaltFile string
	RunID               string
	BatchSize           int
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

func run(args []string, stdout, stderr io.Writer, factory applyStoreFactory) int {
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
	if options.RunID == "" || options.KeyFile == "" || options.LegacyTrialSaltFile == "" {
		writeError(stderr, "apply requires run id and protected inputs")
		return exitInputSystem
	}
	keyMaterial, err := readProtected(options.KeyFile)
	if err != nil {
		writeError(stderr, "protected key input is invalid")
		return exitInputSystem
	}
	zero(keyMaterial)
	legacySalt, err := readProtected(options.LegacyTrialSaltFile)
	if err != nil {
		writeError(stderr, "legacy trial salt input is invalid")
		return exitInputSystem
	}
	zero(legacySalt)
	if factory == nil {
		writeError(stderr, "apply store is not configured")
		return exitInputSystem
	}
	store, err := factory(context.Background(), applyConfig{
		KeyFile:             options.KeyFile,
		LegacyTrialSaltFile: options.LegacyTrialSaltFile,
	})
	if err != nil || store == nil {
		writeError(stderr, "apply store is unavailable")
		return exitInputSystem
	}
	if _, err := importer.Apply(context.Background(), store, plan, importer.ApplyOptions{
		RunID: options.RunID, BatchSize: options.BatchSize,
	}); err != nil {
		writeError(stderr, "apply failed")
		return exitInputSystem
	}
	_, _ = fmt.Fprintln(stdout, "import apply completed and digest verified")
	return exitClean
}

func defaultPlanOptions() importer.PlanOptions {
	return importer.PlanOptions{
		Namespace:           "maestro-legacy-v1",
		SupportedBotSchemas: []string{"bot-schema-v1"},
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
	flags.StringVar(&options.KeyFile, "key-file", "", "protected key file path")
	flags.StringVar(&options.LegacyTrialSaltFile, "legacy-trial-salt-file", "", "protected legacy trial salt file path")
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
	info, err := os.Stat(path)
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
