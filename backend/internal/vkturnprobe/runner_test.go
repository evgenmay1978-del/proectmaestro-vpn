package vkturnprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const probeTestHash = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"

func TestProbeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WDTT_PROBE_HELPER") != "1" {
		return
	}
	input, _ := io.ReadAll(os.Stdin)
	if capture := os.Getenv("WDTT_PROBE_HELPER_CAPTURE"); capture != "" {
		_ = os.WriteFile(capture, input, 0o600)
	}
	var request struct {
		VKHash string `json:"vk_hash"`
	}
	_ = json.Unmarshal(input, &request)
	switch os.Getenv("WDTT_PROBE_HELPER_MODE") {
	case "success":
		fmt.Print("__WDTT_PROBE__|{\"ok\":true,\"stage\":\"TURN_ALLOCATED\",\"code\":\"OK\"}\n")
	case "provider-failure":
		fmt.Print("__WDTT_PROBE__|{\"ok\":false,\"stage\":\"TLS\",\"code\":\"TLS_TRUST_FAILED\"}\n")
	case "mixed":
		if request.VKHash == "secondSafeHash" {
			fmt.Print("__WDTT_PROBE__|{\"ok\":true,\"stage\":\"TURN_ALLOCATED\",\"code\":\"OK\"}\n")
		} else {
			fmt.Print("__WDTT_PROBE__|{\"ok\":false,\"stage\":\"VK_AUTH\",\"code\":\"VK_CALL_UNAVAILABLE\"}\n")
		}
	case "malformed":
		fmt.Print("not-a-probe-result\n")
	case "extra":
		fmt.Print("__WDTT_PROBE__|{\"ok\":true,\"stage\":\"TURN_ALLOCATED\",\"code\":\"OK\"}\nextra\n")
	case "nonzero":
		fmt.Fprint(os.Stderr, "provider-body-secret")
		os.Exit(9)
	case "secret-echo":
		fmt.Printf("__WDTT_PROBE__|{\"ok\":false,\"stage\":\"FAILED\",\"code\":\"PROVIDER_UNAVAILABLE\"}\n%s", request.VKHash)
	case "oversize":
		fmt.Print(strings.Repeat("x", 8192))
	case "sleep":
		time.Sleep(5 * time.Second)
	default:
		os.Exit(11)
	}
	os.Exit(0)
}

func helperRunner(t *testing.T, mode string) Runner {
	t.Helper()
	t.Setenv("GO_WANT_WDTT_PROBE_HELPER", "1")
	t.Setenv("WDTT_PROBE_HELPER_MODE", mode)
	return Runner{
		Path:      os.Args[0],
		Args:      []string{"-test.run=TestProbeHelperProcess", "--"},
		Timeout:   2 * time.Second,
		MaxOutput: 1024,
	}
}

func TestRunnerSendsExactStdinAndParsesSafeSuccess(t *testing.T) {
	runner := helperRunner(t, "success")
	capture := filepath.Join(t.TempDir(), "stdin.json")
	t.Setenv("WDTT_PROBE_HELPER_CAPTURE", capture)

	got := runner.Probe(context.Background(), []string{probeTestHash})
	want := Result{OK: true, Stage: "TURN_ALLOCATED", Code: "OK"}
	if got != want {
		t.Fatalf("Probe() = %#v, want %#v", got, want)
	}
	b, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"vk_hash":"`+probeTestHash+`"}`+"\n" {
		t.Fatalf("stdin document mismatch: %q", b)
	}
}

func TestRunnerReturnsOnlyValidatedProviderResult(t *testing.T) {
	runner := helperRunner(t, "provider-failure")
	got := runner.Probe(context.Background(), []string{probeTestHash})
	want := Result{OK: false, Stage: "TLS", Code: "TLS_TRUST_FAILED"}
	if got != want {
		t.Fatalf("Probe() = %#v, want %#v", got, want)
	}
}

func TestRunnerTriesBoundedCandidatesUntilOneWorks(t *testing.T) {
	runner := helperRunner(t, "mixed")
	got := runner.Probe(context.Background(), []string{"firstSafeHash", "secondSafeHash"})
	if !got.OK || got.Stage != "TURN_ALLOCATED" || got.Code != "OK" {
		t.Fatalf("Probe() = %#v", got)
	}
}

func TestRunnerGivesEachCandidateItsOwnTimeout(t *testing.T) {
	var calls []string
	probe := func(ctx context.Context, hash string, _ int) (Result, bool) {
		calls = append(calls, hash)
		if hash == "firstSafeHash" {
			<-ctx.Done()
			if ctx.Err() != context.DeadlineExceeded {
				t.Fatalf("first candidate context error = %v", ctx.Err())
			}
			return failure("STARTING", "PROBE_TIMEOUT"), false
		}
		return Result{OK: true, Stage: "TURN_ALLOCATED", Code: "OK"}, true
	}

	got := probeCandidates(context.Background(), []string{"firstSafeHash", "secondSafeHash"}, 10*time.Millisecond, 1024, probe)
	if !got.OK || got.Stage != "TURN_ALLOCATED" || got.Code != "OK" {
		t.Fatalf("Probe() after first candidate timeout = %#v", got)
	}
	if len(calls) != 2 || calls[0] != "firstSafeHash" || calls[1] != "secondSafeHash" {
		t.Fatalf("candidate calls = %#v", calls)
	}
}

func TestRunnerFailsClosedAtEveryProcessBoundary(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		mutate   func(*Runner)
		wantCode string
	}{
		{name: "timeout", mode: "sleep", mutate: func(r *Runner) { r.Timeout = 50 * time.Millisecond }, wantCode: "PROBE_TIMEOUT"},
		{name: "output limit", mode: "oversize", mutate: func(r *Runner) { r.MaxOutput = 128 }, wantCode: "PROBE_OUTPUT_INVALID"},
		{name: "malformed", mode: "malformed", wantCode: "PROBE_OUTPUT_INVALID"},
		{name: "extra output", mode: "extra", wantCode: "PROBE_OUTPUT_INVALID"},
		{name: "nonzero exit", mode: "nonzero", wantCode: "PROBE_EXEC_FAILED"},
		{name: "secret echo", mode: "secret-echo", wantCode: "PROBE_OUTPUT_INVALID"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := helperRunner(t, tc.mode)
			if tc.mutate != nil {
				tc.mutate(&runner)
			}
			got := runner.Probe(context.Background(), []string{probeTestHash})
			if got.OK || got.Code != tc.wantCode {
				t.Fatalf("Probe() = %#v, want failure code %q", got, tc.wantCode)
			}
			encoded := fmt.Sprintf("%#v", got)
			if strings.Contains(encoded, probeTestHash) || strings.Contains(encoded, "provider-body-secret") {
				t.Fatalf("safe result leaked helper input/output: %s", encoded)
			}
		})
	}
}

func TestRunnerRejectsMissingBinaryAndInvalidCandidateSet(t *testing.T) {
	if got := (Runner{}).Probe(context.Background(), []string{probeTestHash}); got.OK || got.Code != "PROBE_UNAVAILABLE" {
		t.Fatalf("missing binary result = %#v", got)
	}
	runner := helperRunner(t, "success")
	for _, hashes := range [][]string{nil, {}, {"a", "b", "c", "d", "e"}} {
		if got := runner.Probe(context.Background(), hashes); got.OK || got.Code != "PROBE_INPUT_INVALID" {
			t.Fatalf("invalid candidates result = %#v", got)
		}
	}
}

func TestSanitizeRejectsInvalidStageCodePairs(t *testing.T) {
	invalid := []Result{
		{OK: false, Stage: "FAILED", Code: "OK"},
		{OK: false, Stage: "TLS", Code: "VK_AUTH_FAILED"},
		{OK: false, Stage: "TURN_ALLOCATED", Code: "PROVIDER_UNAVAILABLE"},
		{OK: false, Stage: "READY", Code: "INTERNAL"},
	}
	for _, result := range invalid {
		if safe, ok := Sanitize(result); ok {
			t.Fatalf("Sanitize(%#v) accepted impossible pair %#v", result, safe)
		}
	}
	valid := []Result{
		{OK: true, Stage: "TURN_ALLOCATED", Code: "OK"},
		{OK: false, Stage: "TLS", Code: "TLS_TRUST_FAILED"},
		{OK: false, Stage: "VK_AUTH", Code: "VK_CALL_UNAVAILABLE"},
		{OK: false, Stage: "STARTING", Code: "PROBE_TIMEOUT"},
	}
	for _, result := range valid {
		if safe, ok := Sanitize(result); !ok || safe != result {
			t.Fatalf("Sanitize(%#v) rejected valid pair", result)
		}
	}
}
