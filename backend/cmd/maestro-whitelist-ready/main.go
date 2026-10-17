package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistready"
)

const (
	exitOK         = 0
	exitUsage      = 2
	exitInput      = 3
	exitValidation = 4
	exitInternal   = 70
)

var (
	readLocalFile  = readBoundedRegularFile
	validateBundle = whitelistready.Validate
	replayBundle   = whitelistready.Replay
)

type command struct {
	name     string
	suite    string
	catalog  string
	evidence string
	matrix   string
}

type successOutput struct {
	HarnessStatus    string                       `json:"harness_status"`
	ReleaseReadiness string                       `json:"release_readiness"`
	EvidenceClass    whitelistready.EvidenceClass `json:"evidence_class"`
	SelectedSuite    string                       `json:"selected_suite,omitempty"`
}

type errorOutput struct {
	Error string `json:"error"`
}

type reasoned interface{ ReasonCode() string }

func main() { os.Exit(safeRun(os.Args[1:], os.Stdout, os.Stderr)) }

func safeRun(args []string, stdout, stderr io.Writer) (code int) {
	defer func() {
		if recover() != nil {
			code = writeError(stderr, exitInternal, "internal_failure")
		}
	}()
	return run(args, stdout, stderr)
}

func run(args []string, stdout, stderr io.Writer) int {
	parsed, ok := parseCommand(args)
	if !ok {
		return writeError(stderr, exitUsage, "arguments_invalid")
	}
	catalog, err := readLocalFile(parsed.catalog)
	if err != nil {
		return writeError(stderr, exitInput, "input_read_failed")
	}
	evidence, err := readLocalFile(parsed.evidence)
	if err != nil {
		return writeError(stderr, exitInput, "input_read_failed")
	}
	matrix, err := readLocalFile(parsed.matrix)
	if err != nil {
		return writeError(stderr, exitInput, "input_read_failed")
	}
	var assessment whitelistready.Assessment
	if parsed.name == "validate" {
		assessment, err = validateBundle(catalog, evidence, matrix)
	} else {
		assessment, err = replayBundle(parsed.suite, catalog, evidence, matrix)
	}
	if err != nil {
		return writeError(stderr, exitValidation, safeReason(err))
	}
	if assessment.HarnessStatus != whitelistready.HarnessPass || assessment.ReleaseReadiness != whitelistready.ReleaseNoGo ||
		assessment.EvidenceClass != whitelistready.EvidenceFixtureReplay || (parsed.name == "replay" && assessment.SelectedSuite != parsed.suite) {
		return writeError(stderr, exitInternal, "internal_failure")
	}
	output := successOutput{HarnessStatus: assessment.HarnessStatus, ReleaseReadiness: assessment.ReleaseReadiness, EvidenceClass: assessment.EvidenceClass, SelectedSuite: assessment.SelectedSuite}
	if err := json.NewEncoder(stdout).Encode(output); err != nil {
		return exitInternal
	}
	return exitOK
}

func parseCommand(args []string) (command, bool) {
	if len(args) == 7 && args[0] == "validate" && args[1] == "--catalog" && args[3] == "--evidence" && args[5] == "--matrix" {
		result := command{name: args[0], catalog: args[2], evidence: args[4], matrix: args[6]}
		return result, safeLocalPath(result.catalog) && safeLocalPath(result.evidence) && safeLocalPath(result.matrix)
	}
	if len(args) == 9 && args[0] == "replay" && args[1] == "--suite" && args[3] == "--catalog" && args[5] == "--evidence" && args[7] == "--matrix" {
		result := command{name: args[0], suite: args[2], catalog: args[4], evidence: args[6], matrix: args[8]}
		return result, requiredSuite(result.suite) && safeLocalPath(result.catalog) && safeLocalPath(result.evidence) && safeLocalPath(result.matrix)
	}
	return command{}, false
}

func safeLocalPath(value string) bool {
	portable := strings.ReplaceAll(value, `\`, "/")
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		strings.Contains(portable, "://") || !filepath.IsLocal(portable) || filepath.VolumeName(value) != "" || hasWindowsDrivePrefix(value) {
		return false
	}
	for _, component := range strings.Split(portable, "/") {
		if component == ".." {
			return false
		}
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	drive := value[0]
	return (drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')
}

func requiredSuite(value string) bool {
	for _, suite := range whitelistready.RequiredSuites() {
		if value == suite {
			return true
		}
	}
	return false
}

func readBoundedRegularFile(path string) ([]byte, error) {
	if !safeLocalPath(path) || pathHasLink(path) {
		return nil, errors.New("input unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("input unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > whitelistready.MaxDocumentBytes {
		return nil, errors.New("input unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, whitelistready.MaxDocumentBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > whitelistready.MaxDocumentBytes {
		return nil, errors.New("input unavailable")
	}
	return raw, nil
}

func pathHasLink(path string) bool {
	clean := filepath.Clean(path)
	current := "."
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func safeReason(err error) string {
	var value reasoned
	if errors.As(err, &value) && safeReasonCode(value.ReasonCode()) {
		return value.ReasonCode()
	}
	return "validation_failed"
}

func safeReasonCode(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, current := range value[1:] {
		if (current < 'a' || current > 'z') && (current < '0' || current > '9') && current != '_' {
			return false
		}
	}
	return true
}

func writeError(stderr io.Writer, exitCode int, reason string) int {
	if !safeReasonCode(reason) {
		reason = "internal_failure"
	}
	if err := json.NewEncoder(stderr).Encode(errorOutput{Error: reason}); err != nil {
		return exitInternal
	}
	return exitCode
}
