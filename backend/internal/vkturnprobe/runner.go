// Package vkturnprobe executes the pinned WDTT provider-only probe without
// placing a room hash in argv, the environment, logs or returned errors.
package vkturnprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	probePrefix        = "__WDTT_PROBE__|"
	defaultTimeout     = 50 * time.Second
	defaultOutputLimit = 4096
)

var hashPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,160}$`)

var safeStages = map[string]struct{}{
	"STARTING":         {},
	"DNS":              {},
	"TLS":              {},
	"VK_AUTH":          {},
	"CAPTCHA_REQUIRED": {},
	"TURN_ALLOCATED":   {},
	"DTLS":             {},
	"WRAP":             {},
	"WIREGUARD":        {},
	"READY":            {},
	"FAILED":           {},
	"STOPPED":          {},
}

var safeCodes = map[string]struct{}{
	"OK":                   {},
	"INPUT_INVALID":        {},
	"TLS_TRUST_FAILED":     {},
	"VK_AUTH_FAILED":       {},
	"VK_CALL_UNAVAILABLE":  {},
	"CAPTCHA_REQUIRED":     {},
	"TURN_ALLOCATE_FAILED": {},
	"DTLS_FAILED":          {},
	"WRAP_FAILED":          {},
	"WIREGUARD_FAILED":     {},
	"PROVIDER_UNAVAILABLE": {},
	"INTERNAL":             {},
	"PROBE_UNAVAILABLE":    {},
	"PROBE_TIMEOUT":        {},
	"PROBE_OUTPUT_INVALID": {},
	"PROBE_EXEC_FAILED":    {},
	"PROBE_INPUT_INVALID":  {},
}

// Result is the complete public probe result. It intentionally has no detail or
// error field: callers receive only fixed, validated stage and code values.
type Result struct {
	OK    bool   `json:"ok"`
	Stage string `json:"stage"`
	Code  string `json:"code"`
}

// Runner invokes the pinned Linux WDTT probe. Args exists only for deterministic
// helper-process tests; production supplies Path and receives the fixed probe flag.
type Runner struct {
	Path      string
	Args      []string
	Timeout   time.Duration
	MaxOutput int
}

type candidateProbe func(context.Context, string, int) (Result, bool)

// New returns a production runner. An empty path remains a valid fail-closed
// configuration and yields PROBE_UNAVAILABLE without starting a process.
func New(path string) *Runner {
	return &Runner{
		Path:      strings.TrimSpace(path),
		Timeout:   defaultTimeout,
		MaxOutput: defaultOutputLimit,
	}
}

// Probe checks up to four candidates in order and succeeds when any candidate
// obtains a usable provider relay. Every child receives its hash only through a
// single JSON document on stdin.
func (r Runner) Probe(ctx context.Context, hashes []string) Result {
	if strings.TrimSpace(r.Path) == "" {
		return failure("STARTING", "PROBE_UNAVAILABLE")
	}
	if len(hashes) < 1 || len(hashes) > 4 {
		return failure("STARTING", "PROBE_INPUT_INVALID")
	}
	for _, hash := range hashes {
		if !hashPattern.MatchString(hash) {
			return failure("STARTING", "PROBE_INPUT_INVALID")
		}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxOutput := r.MaxOutput
	if maxOutput <= 0 {
		maxOutput = defaultOutputLimit
	}
	return probeCandidates(ctx, hashes, timeout, maxOutput, r.probeOne)
}

func probeCandidates(ctx context.Context, hashes []string, timeout time.Duration, maxOutput int, probe candidateProbe) Result {
	last := failure("FAILED", "PROVIDER_UNAVAILABLE")
	for _, hash := range hashes {
		if ctx.Err() != nil {
			return failure("STARTING", "PROBE_TIMEOUT")
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		result, providerResult := probe(probeCtx, hash, maxOutput)
		candidateTimedOut := probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		if ctx.Err() != nil {
			return failure("STARTING", "PROBE_TIMEOUT")
		}
		if !providerResult {
			if candidateTimedOut && result.Code == "PROBE_TIMEOUT" {
				last = result
				continue
			}
			return result
		}
		if result.OK {
			return result
		}
		last = result
	}
	return last
}

func (r Runner) probeOne(ctx context.Context, hash string, maxOutput int) (Result, bool) {
	document, err := json.Marshal(struct {
		VKHash string `json:"vk_hash"`
	}{VKHash: hash})
	if err != nil {
		return failure("STARTING", "PROBE_INPUT_INVALID"), false
	}
	document = append(document, '\n')
	args := append(append([]string(nil), r.Args...), "--provider-probe-stdin")
	cmd := exec.CommandContext(ctx, r.Path, args...)
	cmd.Stdin = bytes.NewReader(document)
	stdout := newCappedBuffer(maxOutput)
	stderr := newCappedBuffer(maxOutput)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return failure("STARTING", "PROBE_TIMEOUT"), false
	}
	if stdout.Truncated() || stderr.Truncated() {
		return failure("STARTING", "PROBE_OUTPUT_INVALID"), false
	}
	stdoutText := stdout.String()
	stderrText := stderr.String()
	if strings.Contains(stdoutText, hash) || strings.Contains(stderrText, hash) {
		return failure("STARTING", "PROBE_OUTPUT_INVALID"), false
	}
	if runErr != nil {
		return failure("STARTING", "PROBE_EXEC_FAILED"), false
	}
	result, ok := parseResult(stdoutText)
	if !ok {
		return failure("STARTING", "PROBE_OUTPUT_INVALID"), false
	}
	return result, true
}

func parseResult(raw string) (Result, bool) {
	if !strings.HasSuffix(raw, "\n") || strings.Count(raw, "\n") != 1 || strings.Contains(raw, "\r") {
		return Result{}, false
	}
	line := strings.TrimSuffix(raw, "\n")
	if !strings.HasPrefix(line, probePrefix) {
		return Result{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, probePrefix)))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Result{}, false
	}
	return Sanitize(result)
}

// Sanitize rejects any result outside the fixed public contract. It is also
// used at the API boundary so an alternate Prober cannot inject arbitrary text.
func Sanitize(result Result) (Result, bool) {
	if _, ok := safeStages[result.Stage]; !ok {
		return Result{}, false
	}
	if _, ok := safeCodes[result.Code]; !ok {
		return Result{}, false
	}
	if result.OK != (result.Stage == "TURN_ALLOCATED" && result.Code == "OK") {
		return Result{}, false
	}
	return result, true
}

func failure(stage, code string) Result {
	return Result{OK: false, Stage: stage, Code: code}
}

type cappedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		part := p
		if len(part) > remaining {
			part = part[:remaining]
		}
		_, _ = b.buffer.Write(part)
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *cappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
