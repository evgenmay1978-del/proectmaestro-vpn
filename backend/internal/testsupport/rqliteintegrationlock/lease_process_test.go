//go:build rqlite_integration

package rqliteintegrationlock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const helperProcessEnvironment = "MAESTRO_TASK6_LOCK_HELPER"

func TestRenewResolvesCommittedUnknownByExactHolderTokenAndExpiry(t *testing.T) {
	identity := Identity{JobName: JobName, HolderID: "renew-owner", LeaseToken: "renew-token"}
	db := &scriptedRQLite{
		requestResults: [][]rqlite.Result{
			{{Rows: []map[string]any{leaseRow(identity, 100, 130, 100)}}},
			nil,
		},
		requestErrors: []error{
			nil,
			&rqlite.TransportError{Operation: "request response", UnknownOutcome: true, Err: io.ErrUnexpectedEOF},
		},
		queryResults: [][]rqlite.Result{{{Rows: []map[string]any{leaseRow(identity, 100, 160, 111)}}}},
		queryErrors:  []error{nil},
	}
	lease, err := Acquire(context.Background(), db, Options{
		HolderID: identity.HolderID, LeaseToken: identity.LeaseToken,
		TTL: 30 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lease.Renew(context.Background()); err != nil {
		t.Fatalf("resolve committed-unknown renewal: %v", err)
	}
	if len(db.queries) != 1 || db.queries[0].SQL != leaseAtExpirySQL {
		t.Fatalf("renewal resolver queries=%#v, want exact-expiry query", db.queries)
	}
	wantArgs := []any{identity.JobName, identity.HolderID, identity.LeaseToken, int64(160)}
	if fmt.Sprint(db.queries[0].Args) != fmt.Sprint(wantArgs) {
		t.Fatalf("renewal resolver args=%#v, want %#v", db.queries[0].Args, wantArgs)
	}
}

func TestRunFailsClosedForConfiguredEndpointAndOnlyBypassesWhenAbsent(t *testing.T) {
	runCalled := false
	code := Run(RunOptions{
		HolderID: "no-live-endpoints",
		Migrate:  func(context.Context, rqlite.RQLite) error { return nil },
		Run: func(identity Identity) int {
			runCalled = true
			if identity.Valid() {
				t.Fatalf("absent endpoints returned live identity %#v", identity)
			}
			return 0
		},
	})
	if code != 0 || !runCalled {
		t.Fatalf("absent endpoints Run=(code=%d, called=%t), want explicit non-live bypass", code, runCalled)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := closed.URL
	closed.Close()
	runCalled = false
	code = Run(RunOptions{
		HolderID: "configured-live-endpoint",
		Migrate:  func(context.Context, rqlite.RQLite) error { return nil },
		Run: func(Identity) int {
			runCalled = true
			return 0
		},
		Endpoints: []string{endpoint},
		timing:    helperTiming(),
	})
	if code != 2 || runCalled {
		t.Fatalf("unreachable configured endpoint Run=(code=%d, called=%t), want fail closed", code, runCalled)
	}
}

func TestExactSequentialDRSelectionRejectsBroadEnvironmentBypass(t *testing.T) {
	accepted := []Selection{
		{Package: "importer", RunPattern: "^TestVerifyRestoredBusinessParity$", DRProofPhase: "restored"},
		{Package: "controlplane", RunPattern: "^TestAdvanceRestoredEpochAndFence$", DRProofPhase: "restored", DRFencePhase: "advance"},
		{Package: "controlplane", RunPattern: "^TestAdvanceRestoredEpochAndFence$", DRProofPhase: "restored", DRFencePhase: "activate"},
		{Package: "controlplane", RunPattern: "^TestRestoredQuorumBoundaries$", DRProofPhase: "restored", DRQuorumPhase: "one-loss"},
		{Package: "controlplane", RunPattern: "^TestRestoredQuorumBoundaries$", DRProofPhase: "restored", DRQuorumPhase: "two-loss"},
	}
	for _, selection := range accepted {
		if !IsExactSequentialDRSelection(selection) {
			t.Errorf("exact sequential DR selection rejected: %#v", selection)
		}
	}
	rejected := []Selection{
		{Package: "importer", DRProofPhase: "restored"},
		{Package: "importer", RunPattern: ".*", DRProofPhase: "restored"},
		{Package: "importer", RunPattern: "^TestVerifyRestoredBusinessParity$", DRProofPhase: "restored", DRFencePhase: "advance"},
		{Package: "controlplane", RunPattern: "^TestAdvanceRestoredEpochAndFence$|^TestOther$", DRProofPhase: "restored", DRFencePhase: "advance"},
		{Package: "controlplane", RunPattern: "^TestAdvanceRestoredEpochAndFence$", DRProofPhase: "restored"},
		{Package: "controlplane", RunPattern: "^TestRestoredQuorumBoundaries$", DRProofPhase: "restored", DRFencePhase: "advance", DRQuorumPhase: "one-loss"},
	}
	for _, selection := range rejected {
		if IsExactSequentialDRSelection(selection) {
			t.Errorf("broad or inconsistent DR selection accepted: %#v", selection)
		}
	}
	if !IsExactNonLiveSelection("importer", "^TestTask6IntegrationCleanupRestoresOnlyExactOwnedPostconditionSQLite$") ||
		!IsExactNonLiveSelection("controlplane", "^$") ||
		IsExactNonLiveSelection("importer", ".*") {
		t.Fatal("exact non-live selection policy is broader or narrower than intended")
	}
}

func TestRunCoordinatesSeparateProcessesAcrossMultipleTTLs(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	ownerA := startHelper(t, coordinator.URL(), "package-a", "normal", 7*time.Second)
	enteredA := waitHelperEvent(t, ownerA, "enter", 4*time.Second)
	ownerB := startHelper(t, coordinator.URL(), "package-b", "normal", 250*time.Millisecond)

	select {
	case event := <-ownerB.events:
		t.Fatalf("contender entered or exited during active multi-TTL lease: %#v", event)
	case <-time.After(4 * time.Second):
	}
	exitedA := waitHelperEvent(t, ownerA, "exit", 5*time.Second)
	enteredB := waitHelperEvent(t, ownerB, "enter", 5*time.Second)
	if enteredB.at.Before(exitedA.at) {
		t.Fatalf("contender entered at %s before owner callback ended at %s", enteredB.at, exitedA.at)
	}
	if exitedA.at.Sub(enteredA.at) < 2*helperTiming().TTL {
		t.Fatalf("owner held lease for %s, want at least two TTLs", exitedA.at.Sub(enteredA.at))
	}
	if code := waitHelperExit(t, ownerA, 4*time.Second); code != 0 {
		t.Fatalf("owner exit=%d: %s", code, ownerA.diagnostic())
	}
	if code := waitHelperExit(t, ownerB, 4*time.Second); code != 0 {
		t.Fatalf("contender exit=%d: %s", code, ownerB.diagnostic())
	}
	stats := coordinator.Stats()
	if stats.renewSuccesses < 2 {
		t.Fatalf("successful renewals=%d, want multi-TTL renewal", stats.renewSuccesses)
	}
}

func TestRunRecoversTransientAndCommittedUnknownRenewal(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	coordinator.FailNextRenewTransiently()
	coordinator.CommitNextRenewWithoutResponse()
	owner := startHelper(t, coordinator.URL(), "renew-recovery", "normal", 4*time.Second)
	_ = waitHelperEvent(t, owner, "enter", 4*time.Second)
	if code := waitHelperExit(t, owner, 8*time.Second); code != 0 {
		t.Fatalf("renew recovery process exit=%d: %s", code, owner.diagnostic())
	}
	stats := coordinator.Stats()
	if stats.transientRenewFailures != 1 || stats.committedUnknownRenewals != 1 ||
		stats.renewSuccesses < 1 || stats.exactExpiryQueries < 2 {
		t.Fatalf("renew recovery stats=%#v", stats)
	}
}

func TestRunFailStopsBeforeExpiryWhenRenewalCannotBeProven(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	owner := startHelper(t, coordinator.URL(), "unproven-owner", "normal", 10*time.Second)
	_ = waitHelperEvent(t, owner, "enter", 4*time.Second)
	expiresAt := coordinator.CurrentExpiry()
	coordinator.SetOutage(true)
	if code := waitHelperExit(t, owner, 5*time.Second); code != 2 {
		t.Fatalf("unproven renewal process exit=%d, want fail-stop 2: %s", code, owner.diagnostic())
	}
	if !strings.Contains(owner.diagnostic(), "HTTP 503") {
		t.Fatalf("fail-stop diagnostic lost last renewal error: %s", owner.diagnostic())
	}
	if !time.Now().Before(expiresAt) {
		t.Fatalf("fail-stop happened at/after lease expiry %s", expiresAt)
	}
}

func TestRunFailStopsHangingRenewBeforeDatabaseExpiryAndTakeover(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	coordinator.HangRenewRequests()
	owner := startHelper(t, coordinator.URL(), "hanging-renew-owner", "long-renew-timeout", 10*time.Second)
	_ = waitHelperEvent(t, owner, "enter", 4*time.Second)
	expiresAt := coordinator.CurrentExpiry()
	coordinator.WaitForHangingRenewal(t, 2*time.Second)

	replacement := startHelper(t, coordinator.URL(), "hanging-renew-replacement", "normal", 100*time.Millisecond)
	ownerResult := waitHelperResult(t, owner, 7*time.Second)
	if code := helperExitCode(t, ownerResult); code != 2 {
		t.Fatalf("hanging-renew owner exit=%d, want fail-stop 2: %s", code, owner.diagnostic())
	}
	ownerStoppedAt := ownerResult.finishedAt
	enteredReplacement := waitHelperEvent(t, replacement, "enter", 7*time.Second)
	if !ownerStoppedAt.Before(expiresAt) {
		t.Fatalf("hanging-renew owner stopped at %s, want strictly before database expiry %s", ownerStoppedAt, expiresAt)
	}
	if !ownerStoppedAt.Before(enteredReplacement.at) {
		t.Fatalf("replacement entered at %s before old owner stopped at %s", enteredReplacement.at, ownerStoppedAt)
	}
	if enteredReplacement.at.Before(expiresAt) {
		t.Fatalf("replacement entered at %s before database expiry %s", enteredReplacement.at, expiresAt)
	}
	if code := waitHelperExit(t, replacement, 4*time.Second); code != 0 {
		t.Fatalf("hanging-renew replacement exit=%d: %s", code, replacement.diagnostic())
	}
}

func TestRunFailStopsImmediatelyWhenLeaseOwnershipIsLost(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	owner := startHelper(t, coordinator.URL(), "lost-owner", "normal", 10*time.Second)
	_ = waitHelperEvent(t, owner, "enter", 4*time.Second)
	stolenAt := time.Now()
	coordinator.StealLease()
	if code := waitHelperExit(t, owner, 3*time.Second); code != 2 {
		t.Fatalf("lost-owner process exit=%d, want fail-stop 2: %s", code, owner.diagnostic())
	}
	if time.Since(stolenAt) >= helperTiming().TTL {
		t.Fatalf("lost ownership was not fail-stopped before a TTL elapsed")
	}
}

func TestCrashedProcessLeaseExpiresAndAllowsTakeover(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	crashed := startHelper(t, coordinator.URL(), "crashed-owner", "crash", 150*time.Millisecond)
	_ = waitHelperEvent(t, crashed, "enter", 4*time.Second)
	expiresAt := coordinator.CurrentExpiry()
	if code := waitHelperExit(t, crashed, 3*time.Second); code != 17 {
		t.Fatalf("crash helper exit=%d, want 17: %s", code, crashed.diagnostic())
	}
	replacement := startHelper(t, coordinator.URL(), "replacement-owner", "normal", 200*time.Millisecond)
	entered := waitHelperEvent(t, replacement, "enter", 7*time.Second)
	if entered.at.Before(expiresAt) {
		t.Fatalf("replacement entered at %s before crashed lease expiry %s", entered.at, expiresAt)
	}
	if code := waitHelperExit(t, replacement, 4*time.Second); code != 0 {
		t.Fatalf("replacement exit=%d: %s", code, replacement.diagnostic())
	}
}

func TestConfiguredEndpointsIdentifyIndependentClusters(t *testing.T) {
	clusterA := newFakeCoordinator(t)
	clusterB := newFakeCoordinator(t)
	ownerA := startHelper(t, clusterA.URL(), "same-package", "normal", 2500*time.Millisecond)
	ownerB := startHelper(t, clusterB.URL(), "same-package", "normal", 2500*time.Millisecond)
	enteredA := waitHelperEvent(t, ownerA, "enter", 4*time.Second)
	enteredB := waitHelperEvent(t, ownerB, "enter", 4*time.Second)
	delta := enteredA.at.Sub(enteredB.at)
	if delta < 0 {
		delta = -delta
	}
	if delta >= 2*time.Second {
		t.Fatalf("independent clusters serialized by endpoint identity: entry delta=%s", delta)
	}
	if code := waitHelperExit(t, ownerA, 5*time.Second); code != 0 {
		t.Fatalf("cluster A exit=%d: %s", code, ownerA.diagnostic())
	}
	if code := waitHelperExit(t, ownerB, 5*time.Second); code != 0 {
		t.Fatalf("cluster B exit=%d: %s", code, ownerB.diagnostic())
	}
}

func TestHelperProcessResultSynchronizesConcurrentObservers(t *testing.T) {
	coordinator := newFakeCoordinator(t)
	process := startHelper(t, coordinator.URL(), "result-observers", "normal", 100*time.Millisecond)
	_ = waitHelperEvent(t, process, "enter", 4*time.Second)

	type observation struct {
		result helperProcessResult
		ok     bool
	}
	observations := make(chan observation, 2)
	for range 2 {
		go func() {
			result, ok := process.waitResult(4 * time.Second)
			observations <- observation{result: result, ok: ok}
		}()
	}
	first := <-observations
	second := <-observations
	if !first.ok || !second.ok {
		t.Fatal("concurrent helper result observers timed out")
	}
	if helperExitCode(t, first.result) != 0 || helperExitCode(t, second.result) != 0 {
		t.Fatalf("concurrent helper observers saw non-zero exit: first=%v second=%v", first.result.waitErr, second.result.waitErr)
	}
	if first.result.finishedAt != second.result.finishedAt || first.result.stderr != second.result.stderr {
		t.Fatalf("concurrent helper observers saw different immutable results: first=%#v second=%#v", first.result, second.result)
	}
}

func TestRQLiteIntegrationLeaseProcessHelper(t *testing.T) {
	if os.Getenv(helperProcessEnvironment) != "1" {
		return
	}
	endpoint := os.Getenv("MAESTRO_TASK6_LOCK_ENDPOINT")
	holder := os.Getenv("MAESTRO_TASK6_LOCK_HOLDER")
	mode := os.Getenv("MAESTRO_TASK6_LOCK_MODE")
	hold, err := time.ParseDuration(os.Getenv("MAESTRO_TASK6_LOCK_HOLD"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse helper hold duration: %v\n", err)
		os.Exit(19)
	}
	timing := helperTiming()
	if mode == "long-renew-timeout" {
		timing.RenewTimeout = 5 * time.Second
		timing.SafetyMargin = 500 * time.Millisecond
	}
	code := Run(RunOptions{
		HolderID:  holder,
		Endpoints: []string{endpoint},
		Migrate:   func(context.Context, rqlite.RQLite) error { return nil },
		Run: func(Identity) int {
			emitHelperEvent("enter", holder)
			time.Sleep(hold)
			if mode == "crash" {
				os.Exit(17)
			}
			emitHelperEvent("exit", holder)
			return 0
		},
		timing: timing,
	})
	os.Exit(code)
}

func helperTiming() runTiming {
	return runTiming{
		TTL: 3 * time.Second, PollInterval: 50 * time.Millisecond,
		ProbeTimeout: time.Second, MigrateTimeout: time.Second, AcquireTimeout: 12 * time.Second,
		RenewInterval: 200 * time.Millisecond, RenewRetryInterval: 50 * time.Millisecond,
		RenewTimeout: 250 * time.Millisecond, SafetyMargin: 200 * time.Millisecond,
		ReleaseTimeout: time.Second,
	}
}

func emitHelperEvent(kind, holder string) {
	fmt.Printf("TASK6_EVENT %s %s %d\n", kind, holder, time.Now().UnixNano())
}

type helperEvent struct {
	kind   string
	holder string
	at     time.Time
}

type helperProcessResult struct {
	waitErr    error
	stdoutErr  error
	stderrErr  error
	stderr     string
	finishedAt time.Time
}

type helperStreamResult struct {
	text string
	err  error
}

type helperProcess struct {
	events   chan helperEvent
	finished chan struct{}
	result   helperProcessResult
}

func startHelper(t *testing.T, endpoint, holder, mode string, hold time.Duration) *helperProcess {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestRQLiteIntegrationLeaseProcessHelper$", "-test.count=1")
	environment := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, helperProcessEnvironment+"=") &&
			!strings.HasPrefix(entry, "MAESTRO_TASK6_LOCK_ENDPOINT=") &&
			!strings.HasPrefix(entry, "MAESTRO_TASK6_LOCK_HOLDER=") &&
			!strings.HasPrefix(entry, "MAESTRO_TASK6_LOCK_MODE=") &&
			!strings.HasPrefix(entry, "MAESTRO_TASK6_LOCK_HOLD=") {
			environment = append(environment, entry)
		}
	}
	command.Env = append(environment,
		helperProcessEnvironment+"=1",
		"MAESTRO_TASK6_LOCK_ENDPOINT="+endpoint,
		"MAESTRO_TASK6_LOCK_HOLDER="+holder,
		"MAESTRO_TASK6_LOCK_MODE="+mode,
		"MAESTRO_TASK6_LOCK_HOLD="+hold.String(),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout: %v", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatalf("helper stderr: %v", err)
	}
	process := &helperProcess{events: make(chan helperEvent, 8), finished: make(chan struct{})}
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-process.finished:
			return
		default:
		}
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-process.finished:
		case <-time.After(2 * time.Second):
			t.Errorf("helper did not finish after cleanup kill: %s", process.diagnostic())
		}
	})

	stdoutDrained := make(chan error, 1)
	go func() {
		defer close(process.events)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) != 4 || fields[0] != "TASK6_EVENT" {
				continue
			}
			nanoseconds, parseErr := strconv.ParseInt(fields[3], 10, 64)
			if parseErr != nil {
				continue
			}
			process.events <- helperEvent{kind: fields[1], holder: fields[2], at: time.Unix(0, nanoseconds)}
		}
		stdoutDrained <- scanner.Err()
	}()
	stderrDrained := make(chan helperStreamResult, 1)
	go func() {
		var buffer bytes.Buffer
		_, copyErr := io.Copy(&buffer, stderr)
		stderrDrained <- helperStreamResult{text: buffer.String(), err: copyErr}
	}()
	go func() {
		stdoutErr := <-stdoutDrained
		stderrResult := <-stderrDrained
		process.result = helperProcessResult{
			waitErr: command.Wait(), stdoutErr: stdoutErr,
			stderrErr: stderrResult.err, stderr: stderrResult.text, finishedAt: time.Now(),
		}
		close(process.finished)
	}()
	return process
}

func (process *helperProcess) waitResult(timeout time.Duration) (helperProcessResult, bool) {
	if timeout <= 0 {
		select {
		case <-process.finished:
			return process.result, true
		default:
			return helperProcessResult{}, false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-process.finished:
		return process.result, true
	case <-timer.C:
		return helperProcessResult{}, false
	}
}

func (process *helperProcess) diagnostic() string {
	result, ok := process.waitResult(0)
	if !ok {
		return "helper still running"
	}
	parts := make([]string, 0, 4)
	if stderr := strings.TrimSpace(result.stderr); stderr != "" {
		parts = append(parts, stderr)
	}
	if result.stdoutErr != nil {
		parts = append(parts, fmt.Sprintf("stdout: %v", result.stdoutErr))
	}
	if result.stderrErr != nil {
		parts = append(parts, fmt.Sprintf("stderr: %v", result.stderrErr))
	}
	if result.waitErr != nil {
		parts = append(parts, fmt.Sprintf("wait: %v", result.waitErr))
	}
	if len(parts) == 0 {
		return "no helper diagnostics"
	}
	return strings.Join(parts, "; ")
}

func waitHelperEvent(t *testing.T, process *helperProcess, kind string, timeout time.Duration) helperEvent {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-process.events:
			if !ok {
				t.Fatalf("helper exited before %s event: %s", kind, process.diagnostic())
			}
			if event.kind == kind {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for helper %s event: %s", kind, process.diagnostic())
		}
	}
}

func waitHelperResult(t *testing.T, process *helperProcess, timeout time.Duration) helperProcessResult {
	t.Helper()
	result, ok := process.waitResult(timeout)
	if !ok {
		t.Fatalf("timed out waiting for helper exit: %s", process.diagnostic())
	}
	if result.stdoutErr != nil || result.stderrErr != nil {
		t.Fatalf("drain helper output: stdout=%v stderr=%v", result.stdoutErr, result.stderrErr)
	}
	return result
}

func helperExitCode(t *testing.T, result helperProcessResult) int {
	t.Helper()
	if result.waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(result.waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	t.Fatalf("wait helper: %v", result.waitErr)
	return -1
}

func waitHelperExit(t *testing.T, process *helperProcess, timeout time.Duration) int {
	t.Helper()
	return helperExitCode(t, waitHelperResult(t, process, timeout))
}

type fakeLease struct {
	jobName    string
	holderID   string
	leaseToken string
	acquiredAt int64
	expiresAt  int64
}

type fakeCoordinatorStats struct {
	renewSuccesses           int
	transientRenewFailures   int
	committedUnknownRenewals int
	exactExpiryQueries       int
}

type fakeCoordinator struct {
	mu sync.Mutex

	server *httptest.Server
	lease  *fakeLease
	outage bool

	failNextRenew           bool
	commitNextRenewUnknown  bool
	hangRenew               bool
	hangingRenewStarted     chan struct{}
	hangingRenewStartedOnce sync.Once
	stats                   fakeCoordinatorStats
}

func newFakeCoordinator(t *testing.T) *fakeCoordinator {
	t.Helper()
	coordinator := &fakeCoordinator{hangingRenewStarted: make(chan struct{})}
	coordinator.server = httptest.NewServer(http.HandlerFunc(coordinator.serveHTTP))
	t.Cleanup(coordinator.server.Close)
	return coordinator
}

func (coordinator *fakeCoordinator) HangRenewRequests() {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.hangRenew = true
}

func (coordinator *fakeCoordinator) WaitForHangingRenewal(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-coordinator.hangingRenewStarted:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for hanging renewal request")
	}
}
func (coordinator *fakeCoordinator) URL() string { return coordinator.server.URL }

func (coordinator *fakeCoordinator) FailNextRenewTransiently() {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.failNextRenew = true
}

func (coordinator *fakeCoordinator) CommitNextRenewWithoutResponse() {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.commitNextRenewUnknown = true
}

func (coordinator *fakeCoordinator) SetOutage(outage bool) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.outage = outage
}

func (coordinator *fakeCoordinator) StealLease() {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	now := time.Now().Unix()
	coordinator.lease = &fakeLease{
		jobName: JobName, holderID: "intruder", leaseToken: "intruder-token",
		acquiredAt: now, expiresAt: now + 30,
	}
}

func (coordinator *fakeCoordinator) CurrentExpiry() time.Time {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.lease == nil {
		return time.Time{}
	}
	return time.Unix(coordinator.lease.expiresAt, 0)
}

func (coordinator *fakeCoordinator) Stats() fakeCoordinatorStats {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.stats
}

func (coordinator *fakeCoordinator) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/db/request" {
		http.Error(writer, "unexpected request", http.StatusNotFound)
		return
	}
	decoder := json.NewDecoder(request.Body)
	decoder.UseNumber()
	var statements [][]any
	if err := decoder.Decode(&statements); err != nil || len(statements) != 1 || len(statements[0]) == 0 {
		http.Error(writer, "malformed statement", http.StatusBadRequest)
		return
	}
	sql, ok := statements[0][0].(string)
	if !ok {
		http.Error(writer, "malformed SQL", http.StatusBadRequest)
		return
	}
	args := statements[0][1:]
	if sql == "SELECT 1 AS ready" {
		writeFakeResults(writer, []map[string]any{{"ready": int64(1)}})
		return
	}

	coordinator.mu.Lock()
	if coordinator.outage {
		coordinator.mu.Unlock()
		http.Error(writer, "coordinator unavailable", http.StatusServiceUnavailable)
		return
	}
	now := time.Now().Unix()
	var rows []map[string]any
	closeWithoutResponse := false
	switch sql {
	case acquireSQL:
		if len(args) != 4 {
			coordinator.mu.Unlock()
			http.Error(writer, "bad acquire args", http.StatusBadRequest)
			return
		}
		ttl := fakeInteger(args[3])
		if coordinator.lease == nil || coordinator.lease.expiresAt <= now {
			coordinator.lease = &fakeLease{
				jobName: fakeString(args[0]), holderID: fakeString(args[1]), leaseToken: fakeString(args[2]),
				acquiredAt: now, expiresAt: now + ttl,
			}
		}
		if coordinator.lease.matches(fakeString(args[0]), fakeString(args[1]), fakeString(args[2])) {
			rows = []map[string]any{coordinator.lease.row(now)}
		}
	case renewSQL:
		if coordinator.hangRenew {
			coordinator.hangingRenewStartedOnce.Do(func() { close(coordinator.hangingRenewStarted) })
			coordinator.mu.Unlock()
			<-request.Context().Done()
			return
		}
		if coordinator.failNextRenew {
			coordinator.failNextRenew = false
			coordinator.stats.transientRenewFailures++
			coordinator.mu.Unlock()
			http.Error(writer, "transient renewal failure", http.StatusServiceUnavailable)
			return
		}
		if len(args) != 6 {
			coordinator.mu.Unlock()
			http.Error(writer, "bad renew args", http.StatusBadRequest)
			return
		}
		ttl := fakeInteger(args[0])
		expectedExpiry := fakeInteger(args[4])
		renewWindow := fakeInteger(args[5])
		if coordinator.lease != nil &&
			coordinator.lease.matches(fakeString(args[1]), fakeString(args[2]), fakeString(args[3])) &&
			coordinator.lease.expiresAt == expectedExpiry && coordinator.lease.expiresAt > now &&
			coordinator.lease.expiresAt-now <= renewWindow {
			coordinator.lease.expiresAt = expectedExpiry + ttl
			coordinator.stats.renewSuccesses++
			rows = []map[string]any{coordinator.lease.row(now)}
			if coordinator.commitNextRenewUnknown {
				coordinator.commitNextRenewUnknown = false
				coordinator.stats.committedUnknownRenewals++
				closeWithoutResponse = true
			}
		}
	case leaseAtExpirySQL:
		coordinator.stats.exactExpiryQueries++
		if len(args) == 4 && coordinator.lease != nil && coordinator.lease.expiresAt > now &&
			coordinator.lease.matches(fakeString(args[0]), fakeString(args[1]), fakeString(args[2])) &&
			coordinator.lease.expiresAt == fakeInteger(args[3]) {
			rows = []map[string]any{coordinator.lease.row(now)}
		}
	case currentSQL:
		if len(args) == 3 && coordinator.lease != nil && coordinator.lease.expiresAt > now &&
			coordinator.lease.matches(fakeString(args[0]), fakeString(args[1]), fakeString(args[2])) {
			rows = []map[string]any{coordinator.lease.row(now)}
		}
	case releaseSQL:
		if len(args) == 3 && coordinator.lease != nil &&
			coordinator.lease.matches(fakeString(args[0]), fakeString(args[1]), fakeString(args[2])) {
			rows = []map[string]any{{
				"job_name": coordinator.lease.jobName, "holder_id": coordinator.lease.holderID,
				"lease_token": coordinator.lease.leaseToken,
			}}
			coordinator.lease = nil
		}
	default:
		coordinator.mu.Unlock()
		http.Error(writer, "unexpected SQL", http.StatusBadRequest)
		return
	}
	coordinator.mu.Unlock()
	if closeWithoutResponse {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		connection, _, err := hijacker.Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return
	}
	writeFakeResults(writer, rows)
}

func (lease *fakeLease) matches(jobName, holderID, leaseToken string) bool {
	return lease.jobName == jobName && lease.holderID == holderID && lease.leaseToken == leaseToken
}

func (lease *fakeLease) row(databaseNow int64) map[string]any {
	return map[string]any{
		"job_name": lease.jobName, "holder_id": lease.holderID, "lease_token": lease.leaseToken,
		"acquired_at_unix": lease.acquiredAt, "expires_at_unix": lease.expiresAt,
		"database_now_unix": databaseNow,
	}
}

func writeFakeResults(writer http.ResponseWriter, rows []map[string]any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"results": []map[string]any{{"rows": rows}},
	})
}

func fakeString(value any) string {
	result, _ := value.(string)
	return result
}

func fakeInteger(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		result, _ := typed.Int64()
		return result
	case float64:
		return int64(typed)
	case int64:
		return typed
	}
	return 0
}
