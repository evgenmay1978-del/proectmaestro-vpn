package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

const maxEvidenceTrustBytes = 1 << 20

type reasoned interface{ ReasonCode() string }

type reasonError struct{ code string }

func (err reasonError) Error() string      { return "release validation failed" }
func (err reasonError) ReasonCode() string { return err.code }

type uniqueStringFlag struct {
	value string
	seen  bool
}

func (value *uniqueStringFlag) Set(input string) error {
	if value.seen {
		return errors.New("duplicate flag")
	}
	value.value = input
	value.seen = true
	return nil
}

func (value *uniqueStringFlag) String() string { return value.value }

var validateReleaseDirectoryWithTrust = release.ValidateReleaseDirectoryWithTrust

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("maestro-release-validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var releaseDir uniqueStringFlag
	var evidenceTrustPath uniqueStringFlag
	flags.Var(&releaseDir, "release-dir", "candidate release directory")
	flags.Var(&evidenceTrustPath, "evidence-trust", "external evidence trust file")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		!releaseDir.seen || !evidenceTrustPath.seen ||
		!safePathArgument(releaseDir.value) || !safePathArgument(evidenceTrustPath.value) {
		return writeFailure(stderr, "arguments_invalid")
	}

	trust, err := readEvidenceTrust(evidenceTrustPath.value)
	if err != nil {
		return writeFailure(stderr, errorReasonCode(err))
	}
	if err := validateReleaseDirectoryWithTrust(releaseDir.value, trust); err != nil {
		return writeFailure(stderr, errorReasonCode(err))
	}
	fmt.Fprintln(stdout, "release_validation_passed")
	return 0
}

func readEvidenceTrust(path string) (release.EvidenceTrust, error) {
	file, err := os.Open(path)
	if err != nil {
		return release.EvidenceTrust{}, reasonError{code: "evidence_trust_read_failed"}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return release.EvidenceTrust{}, reasonError{code: "evidence_trust_read_failed"}
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxEvidenceTrustBytes {
		return release.EvidenceTrust{}, reasonError{code: "evidence_trust_invalid"}
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxEvidenceTrustBytes+1))
	if err != nil {
		return release.EvidenceTrust{}, reasonError{code: "evidence_trust_read_failed"}
	}
	if len(raw) == 0 || len(raw) > maxEvidenceTrustBytes {
		return release.EvidenceTrust{}, reasonError{code: "evidence_trust_invalid"}
	}
	return release.ParseEvidenceTrust(raw)
}

func safePathArgument(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func errorReasonCode(err error) string {
	var value reasoned
	if errors.As(err, &value) && safeReasonCode(value.ReasonCode()) {
		return value.ReasonCode()
	}
	return "validation_failed"
}

func safeReasonCode(code string) bool {
	if len(code) == 0 || len(code) > 64 || code[0] < 'a' || code[0] > 'z' {
		return false
	}
	for _, current := range code[1:] {
		if (current < 'a' || current > 'z') && (current < '0' || current > '9') && current != '_' {
			return false
		}
	}
	return true
}

func writeFailure(stderr io.Writer, code string) int {
	fmt.Fprintf(stderr, "release_validation_failed code=%s\n", code)
	return 1
}
