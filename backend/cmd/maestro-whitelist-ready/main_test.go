package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistready"
)

func TestRunAcceptsOnlyClosedCommandGrammar(t *testing.T) {
	stubSuccessfulInputs(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "empty"},
		{name: "help", args: []string{"--help"}},
		{name: "version", args: []string{"--version"}},
		{name: "missing matrix", args: []string{"validate", "--catalog", "c", "--evidence", "e"}},
		{name: "reordered flags", args: []string{"validate", "--evidence", "e", "--catalog", "c", "--matrix", "m"}},
		{name: "equals form", args: []string{"validate", "--catalog=c", "--evidence", "e", "--matrix", "m"}},
		{name: "duplicate", args: []string{"validate", "--catalog", "c", "--catalog", "x", "--evidence", "e", "--matrix", "m"}},
		{name: "extra", args: []string{"validate", "--catalog", "c", "--evidence", "e", "--matrix", "m", "extra"}},
		{name: "unknown command", args: []string{"live", "--catalog", "c", "--evidence", "e", "--matrix", "m"}},
		{name: "replay missing suite", args: []string{"replay", "--catalog", "c", "--evidence", "e", "--matrix", "m"}},
		{name: "replay wildcard", args: []string{"replay", "--suite", "*", "--catalog", "c", "--evidence", "e", "--matrix", "m"}},
		{name: "url path", args: []string{"validate", "--catalog", "https://secret.invalid", "--evidence", "e", "--matrix", "m"}},
		{name: "control path", args: []string{"validate", "--catalog", "secret\npath", "--evidence", "e", "--matrix", "m"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(test.args, &stdout, &stderr)
			if code != exitUsage || stdout.Len() != 0 || stderr.String() != "{\"error\":\"arguments_invalid\"}\n" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "secret") || strings.Contains(stderr.String(), "https") {
				t.Fatalf("stderr echoed rejected input: %q", stderr.String())
			}
		})
	}
}

func TestRunValidateEmitsDeterministicNoGo(t *testing.T) {
	stubSuccessfulInputs(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--catalog", "catalog.json", "--evidence", "evidence.json", "--matrix", "matrix.json"}, &stdout, &stderr)
	const expected = "{\"harness_status\":\"PASS\",\"release_readiness\":\"NO_GO\",\"evidence_class\":\"FIXTURE_REPLAY\"}\n"
	if code != exitOK || stdout.String() != expected || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReplayForwardsExactSuite(t *testing.T) {
	previousReplay := replayBundle
	previousRead := readLocalFile
	t.Cleanup(func() { replayBundle, readLocalFile = previousReplay, previousRead })
	readLocalFile = func(path string) ([]byte, error) { return []byte(path), nil }
	var gotSuite string
	var gotInputs [][]byte
	replayBundle = func(suite string, catalog, evidence, matrix []byte) (whitelistready.Assessment, error) {
		gotSuite = suite
		gotInputs = [][]byte{catalog, evidence, matrix}
		return whitelistready.Assessment{HarnessStatus: whitelistready.HarnessPass, ReleaseReadiness: whitelistready.ReleaseNoGo, EvidenceClass: whitelistready.EvidenceFixtureReplay, SelectedSuite: suite}, nil
	}
	var stdout, stderr bytes.Buffer
	args := []string{"replay", "--suite", "edge_rotation", "--catalog", "catalog.json", "--evidence", "evidence.json", "--matrix", "matrix.json"}
	code := run(args, &stdout, &stderr)
	const expected = "{\"harness_status\":\"PASS\",\"release_readiness\":\"NO_GO\",\"evidence_class\":\"FIXTURE_REPLAY\",\"selected_suite\":\"edge_rotation\"}\n"
	if code != exitOK || stdout.String() != expected || stderr.Len() != 0 || gotSuite != "edge_rotation" ||
		!reflect.DeepEqual(gotInputs, [][]byte{[]byte("catalog.json"), []byte("evidence.json"), []byte("matrix.json")}) {
		t.Fatalf("exit=%d stdout=%q stderr=%q suite=%q inputs=%q", code, stdout.String(), stderr.String(), gotSuite, gotInputs)
	}
}

func TestRunNeverEchoesReadOrValidationFailures(t *testing.T) {
	previousValidate := validateBundle
	previousRead := readLocalFile
	t.Cleanup(func() { validateBundle, readLocalFile = previousValidate, previousRead })

	t.Run("read", func(t *testing.T) {
		readLocalFile = func(string) ([]byte, error) { return nil, errors.New("secret-path must-not-echo") }
		var stdout, stderr bytes.Buffer
		code := run([]string{"validate", "--catalog", "catalog.json", "--evidence", "evidence.json", "--matrix", "matrix.json"}, &stdout, &stderr)
		if code != exitInput || stdout.Len() != 0 || stderr.String() != "{\"error\":\"input_read_failed\"}\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("validate", func(t *testing.T) {
		readLocalFile = func(string) ([]byte, error) { return []byte("secret fixture"), nil }
		validateBundle = func(_, _, _ []byte) (whitelistready.Assessment, error) {
			return whitelistready.Assessment{}, fakeReasonError{}
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"validate", "--catalog", "catalog.json", "--evidence", "evidence.json", "--matrix", "matrix.json"}, &stdout, &stderr)
		if code != exitValidation || stdout.Len() != 0 || stderr.String() != "{\"error\":\"fixture_invalid\"}\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

type fakeReasonError struct{}

func (fakeReasonError) Error() string      { return "secret raw input must-not-echo" }
func (fakeReasonError) ReasonCode() string { return "fixture_invalid" }

func stubSuccessfulInputs(t *testing.T) {
	t.Helper()
	previousValidate := validateBundle
	previousReplay := replayBundle
	previousRead := readLocalFile
	t.Cleanup(func() { validateBundle, replayBundle, readLocalFile = previousValidate, previousReplay, previousRead })
	readLocalFile = func(string) ([]byte, error) { return []byte("fixture"), nil }
	validateBundle = func(_, _, _ []byte) (whitelistready.Assessment, error) {
		return whitelistready.Assessment{HarnessStatus: whitelistready.HarnessPass, ReleaseReadiness: whitelistready.ReleaseNoGo, EvidenceClass: whitelistready.EvidenceFixtureReplay}, nil
	}
	replayBundle = func(suite string, _, _, _ []byte) (whitelistready.Assessment, error) {
		return whitelistready.Assessment{HarnessStatus: whitelistready.HarnessPass, ReleaseReadiness: whitelistready.ReleaseNoGo, EvidenceClass: whitelistready.EvidenceFixtureReplay, SelectedSuite: suite}, nil
	}
}
