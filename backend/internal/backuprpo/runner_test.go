package backuprpo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const (
	runnerBackupIDFixture = "0123456789abcdef0123456789abcdef"
	testVersion           = "version-0001"
)

type sequenceIDs struct {
	values []string
}

func (source *sequenceIDs) NewID() (string, error) {
	if len(source.values) == 0 {
		return "", errors.New("ids exhausted")
	}
	value := source.values[0]
	source.values = source.values[1:]
	return value, nil
}

type memoryCapabilityGate struct {
	calls *[]string
	err   error
}

func (gate *memoryCapabilityGate) Check(context.Context) error {
	*gate.calls = append(*gate.calls, "capability")
	return gate.err
}

type memoryCycleStore struct {
	now               int64
	state             controlplane.BackupRPOState
	attempt           *controlplane.BackupRPOAttempt
	calls             *[]string
	markStartedErr    error
	registerInvisible bool
	bumpAfterRegister bool
}

func (store *memoryCycleStore) add(call string) {
	*store.calls = append(*store.calls, call)
}

func (store *memoryCycleStore) Current(context.Context) (controlplane.BackupRPOState, error) {
	store.add("current")
	state := store.state
	state.DatabaseNowUnix = store.now
	return state, nil
}

func (store *memoryCycleStore) CurrentCycle(context.Context) (controlplane.BackupRPOCycle, error) {
	store.add("cycle")
	state := store.state
	state.DatabaseNowUnix = store.now
	var attempt *controlplane.BackupRPOAttempt
	if store.attempt != nil {
		copy := *store.attempt
		copy.DatabaseNowUnix = store.now
		attempt = &copy
	}
	return controlplane.BackupRPOCycle{State: state, ActiveAttempt: attempt}, nil
}

func (store *memoryCycleStore) AcquireLease(_ context.Context, request controlplane.BackupRPOLeaseRequest) (controlplane.BackupRPOLease, error) {
	store.add("acquire")
	lease := controlplane.BackupRPOLease{
		JobName: controlplane.BackupRPOJobName, HolderID: request.HolderID,
		LeaseToken: request.LeaseToken, AcquiredAtUnix: store.now,
		ExpiresAtUnix: store.now + request.TTLSeconds, RestoreEpoch: request.RestoreEpoch,
		LeaseFence: request.ExpectedFence + 1, Capability: request.Capability, Live: true,
	}
	store.state.Lease = &lease
	return lease, nil
}

func (store *memoryCycleStore) RenewLease(_ context.Context, request controlplane.BackupRPOLeaseRequest) (controlplane.BackupRPOLease, error) {
	store.add("renew")
	lease := controlplane.BackupRPOLease{
		JobName: controlplane.BackupRPOJobName, HolderID: request.HolderID,
		LeaseToken: request.LeaseToken, AcquiredAtUnix: store.now - 1,
		ExpiresAtUnix: store.now + request.TTLSeconds, RestoreEpoch: request.RestoreEpoch,
		LeaseFence: request.ExpectedFence, Capability: request.Capability, Live: true,
	}
	store.state.Lease = &lease
	return lease, nil
}

func (store *memoryCycleStore) RegisterAttempt(_ context.Context, identity controlplane.BackupRPOAttemptIdentity) (controlplane.BackupRPOAttempt, error) {
	store.add("register")
	attempt := controlplane.BackupRPOAttempt{Identity: identity, Phase: controlplane.BackupRPOAttemptPending, CreatedAtUnix: store.now, UpdatedAtUnix: store.now, DatabaseNowUnix: store.now}
	store.state.LastAttemptSequence = identity.AttemptSequence
	if store.bumpAfterRegister {
		store.state.DirtyGeneration++
	}
	if !store.registerInvisible {
		store.attempt = &attempt
	}
	return attempt, nil
}

func (store *memoryCycleStore) MarkUploadStarted(_ context.Context, identity controlplane.BackupRPOAttemptIdentity) (controlplane.BackupRPOAttempt, error) {
	store.add("mark-upload-started")
	if store.markStartedErr != nil {
		return controlplane.BackupRPOAttempt{}, store.markStartedErr
	}
	attempt := controlplane.BackupRPOAttempt{Identity: identity, Phase: controlplane.BackupRPOAttemptApplying, CreatedAtUnix: store.now, UpdatedAtUnix: store.now, DatabaseNowUnix: store.now}
	store.attempt = &attempt
	return attempt, nil
}

func (store *memoryCycleStore) RecordUploadOutcome(_ context.Context, outcome controlplane.BackupRPOUploadOutcome) (controlplane.BackupRPOAttempt, error) {
	phase := controlplane.BackupRPOAttemptApplied
	version := outcome.VersionID.String()
	if outcome.Unknown {
		phase = controlplane.BackupRPOAttemptUnknown
		store.add("record-unknown")
	} else {
		store.add("record-applied")
	}
	attempt := controlplane.BackupRPOAttempt{Identity: outcome.Identity, Phase: phase, ObjectVersion: version, CreatedAtUnix: store.now, UpdatedAtUnix: store.now, DatabaseNowUnix: store.now}
	store.attempt = &attempt
	return attempt, nil
}

func (store *memoryCycleStore) AcknowledgeVerified(_ context.Context, proof controlplane.BackupRPOVerification) (controlplane.BackupRPOAttempt, error) {
	store.add("ack")
	if proof.ManifestRestoreEpoch != proof.Identity.RestoreEpoch || proof.ManifestCapturedGeneration != proof.Identity.CapturedGeneration {
		return controlplane.BackupRPOAttempt{}, errors.New("bad proof")
	}
	attempt := controlplane.BackupRPOAttempt{Identity: proof.Identity, Phase: controlplane.BackupRPOAttemptVerified, ObjectVersion: proof.VersionID.String(), CreatedAtUnix: store.now, UpdatedAtUnix: store.now, DatabaseNowUnix: store.now}
	store.state.VerifiedGeneration = proof.Identity.CapturedGeneration
	if store.state.DirtyGeneration == proof.Identity.CapturedGeneration {
		store.state.Phase = controlplane.BackupRPOPhaseVerified
	} else {
		store.state.Phase = controlplane.BackupRPOPhaseDirty
	}
	store.attempt = nil
	return attempt, nil
}

func (store *memoryCycleStore) SupersedeStaleAttempt(_ context.Context, request controlplane.BackupRPOSupersedeRequest) (controlplane.BackupRPOAttempt, error) {
	store.add("supersede")
	attempt := controlplane.BackupRPOAttempt{Identity: request.Identity, Phase: controlplane.BackupRPOAttemptSuperseded, FailureCode: controlplane.BackupRPOFailureStaleFence, CreatedAtUnix: store.now, UpdatedAtUnix: store.now, DatabaseNowUnix: store.now}
	store.attempt = nil
	return attempt, nil
}

type memoryObjectStore struct {
	calls        *[]string
	checkErr     error
	putErr       error
	reconcileErr error
	getErr       error
	putCalls     int
	reconcile    int
	getCalls     int
}

func (store *memoryObjectStore) CheckVersioning(context.Context) error {
	*store.calls = append(*store.calls, "capability")
	return store.checkErr
}

func (store *memoryObjectStore) PutImmutable(_ context.Context, request PutRequest) (VersionID, error) {
	*store.calls = append(*store.calls, "put")
	store.putCalls++
	payload, err := io.ReadAll(request.Body)
	if err != nil || int64(len(payload)) != request.Metadata.SizeBytes {
		return VersionID{}, ErrInvalidRequest
	}
	if store.putErr != nil {
		return VersionID{}, store.putErr
	}
	return mustObjectVersion(testVersion), nil
}

func (store *memoryObjectStore) GetExact(_ context.Context, request ExactObjectRequest) (Readback, error) {
	*store.calls = append(*store.calls, "get")
	store.getCalls++
	if store.getErr != nil {
		return Readback{}, store.getErr
	}
	return Readback{VersionID: request.VersionID, SHA256: request.Metadata.SHA256, SizeBytes: request.Metadata.SizeBytes, RestoreEpoch: request.Metadata.RestoreEpoch, ManifestAuthenticated: true}, nil
}

func (store *memoryObjectStore) ReconcileUnknownPut(_ context.Context, request ReconcileRequest) (VersionID, error) {
	*store.calls = append(*store.calls, "reconcile")
	store.reconcile++
	if store.reconcileErr != nil {
		return VersionID{}, store.reconcileErr
	}
	return mustObjectVersion(testVersion), nil
}

type byteBundle struct {
	*bytes.Reader
	closed bool
}

func (bundle *byteBundle) Close() error {
	bundle.closed = true
	return nil
}

type memoryBundles struct {
	calls      *[]string
	payload    []byte
	createErr  error
	openErr    error
	createCall int
	openCall   int
}

func (bundles *memoryBundles) Create(_ context.Context, _ BundleRequest) (Bundle, error) {
	*bundles.calls = append(*bundles.calls, "create")
	bundles.createCall++
	if bundles.createErr != nil {
		return nil, bundles.createErr
	}
	return &byteBundle{Reader: bytes.NewReader(bundles.payload)}, nil
}

func (bundles *memoryBundles) OpenExisting(_ context.Context, _ BundleRequest) (Bundle, error) {
	*bundles.calls = append(*bundles.calls, "open-existing")
	bundles.openCall++
	if bundles.openErr != nil {
		return nil, bundles.openErr
	}
	return &byteBundle{Reader: bytes.NewReader(bundles.payload)}, nil
}

func mustObjectVersion(value string) VersionID {
	version, err := NewVersionID(value)
	if err != nil {
		panic(err)
	}
	return version
}

func runnerFixture(t *testing.T, state controlplane.BackupRPOState) (*Runner, *memoryCycleStore, *memoryObjectStore, *memoryBundles, *[]string) {
	t.Helper()
	calls := []string{}
	store := &memoryCycleStore{now: 1_700_000_000, state: state, calls: &calls}
	objects := &memoryObjectStore{calls: &calls}
	bundles := &memoryBundles{calls: &calls, payload: []byte("authenticated-encrypted-bundle")}
	runner := &Runner{
		Store: store, Objects: objects, Bundles: bundles,
		Capabilities: &memoryCapabilityGate{calls: &calls},
		IDs:          &sequenceIDs{values: []string{"lease-token", runnerBackupIDFixture, "fedcba9876543210fedcba9876543210", "11111111111111111111111111111111"}},
		Now:          func() time.Time { return time.Unix(store.now, 0) },
		Config: RunnerConfig{
			HolderID: "worker-s2", Prefix: "backup-rpo", LeaseTTL: 60 * time.Second,
			CapabilityTTL: 120 * time.Second, Deadline: time.Second, MaxTransitions: 32,
			CapabilityGeneration: 1, CapabilityEvidenceSHA256: strings.Repeat("a", 64),
			CapabilityIssuedAtUnix: store.now, CapabilityExpiresAtUnix: store.now + 120,
			MaxBundleBytes: 1 << 20,
		},
	}
	return runner, store, objects, bundles, &calls
}

func dirtyState() controlplane.BackupRPOState {
	return controlplane.BackupRPOState{RestoreEpoch: 4, DirtyGeneration: 2, VerifiedGeneration: 1, LastAttemptSequence: 0, Phase: controlplane.BackupRPOPhaseDirty, UpdatedAtUnix: 1_699_999_999, DatabaseNowUnix: 1_700_000_000}
}

func seedAttempt(store *memoryCycleStore, phase string, fence int64, token string) {
	payload := storePayload(store)
	digest := sha256.Sum256(payload)
	identity := controlplane.BackupRPOAttemptIdentity{
		HolderID: "worker-s2", LeaseToken: token, RestoreEpoch: store.state.RestoreEpoch,
		LeaseFence: fence, Capability: controlplane.BackupRPOCapability{Generation: 1, EvidenceSHA256: strings.Repeat("a", 64), ExpiresAtUnix: store.now + 120},
		CapturedGeneration: store.state.DirtyGeneration, AttemptSequence: 1,
		BackupID: runnerBackupIDFixture, ObjectKey: "backup-rpo/g-2/a-1-" + runnerBackupIDFixture + ".tar.gpg",
		ObjectSHA256: hex.EncodeToString(digest[:]), ObjectSizeBytes: int64(len(payload)), ManifestVersion: 2,
		AdapterContractVersion: controlplane.BackupRPOAdapterYandexS3V1,
	}
	attempt := &controlplane.BackupRPOAttempt{Identity: identity, Phase: phase, CreatedAtUnix: store.now - 1, UpdatedAtUnix: store.now - 1, DatabaseNowUnix: store.now}
	if phase == controlplane.BackupRPOAttemptApplied || phase == controlplane.BackupRPOAttemptVerified {
		attempt.ObjectVersion = testVersion
	}
	store.attempt = attempt
	store.state.LastAttemptSequence = 1
}

func storePayload(_ *memoryCycleStore) []byte { return []byte("authenticated-encrypted-bundle") }

func liveLease(store *memoryCycleStore, fence int64, token string) {
	store.state.Lease = &controlplane.BackupRPOLease{
		JobName: controlplane.BackupRPOJobName, HolderID: "worker-s2", LeaseToken: token,
		AcquiredAtUnix: store.now - 30, ExpiresAtUnix: store.now + 30,
		RestoreEpoch: store.state.RestoreEpoch, LeaseFence: fence,
		Capability: controlplane.BackupRPOCapability{Generation: 1, EvidenceSHA256: strings.Repeat("a", 64), ExpiresAtUnix: store.now + 120}, Live: true,
	}
}

func TestRunnerCleanCycleIsNoopAfterCapabilityAndFencedLease(t *testing.T) {
	state := dirtyState()
	state.VerifiedGeneration = state.DirtyGeneration
	state.Phase = controlplane.BackupRPOPhaseVerified
	runner, _, objects, bundles, calls := runnerFixture(t, state)

	result := runner.Run(context.Background())

	if result.Code != ResultNoop {
		t.Fatalf("code = %q, want %q", result.Code, ResultNoop)
	}
	if objects.putCalls != 0 || objects.getCalls != 0 || bundles.createCall != 0 {
		t.Fatalf("clean cycle mutated external state: put=%d get=%d create=%d", objects.putCalls, objects.getCalls, bundles.createCall)
	}
	if got := strings.Join(*calls, ","); got != "capability,current,acquire,cycle" {
		t.Fatalf("calls = %s", got)
	}
}

func TestRunnerDirtyCycleCreatesRegistersUploadsReadsExactAndAcknowledges(t *testing.T) {
	runner, store, objects, bundles, calls := runnerFixture(t, dirtyState())

	result := runner.Run(context.Background())

	if result.Code != ResultVerified {
		t.Fatalf("code = %q, want %q", result.Code, ResultVerified)
	}
	if objects.putCalls != 1 || objects.getCalls != 1 || objects.reconcile != 0 || bundles.createCall != 1 {
		t.Fatalf("external calls put=%d get=%d reconcile=%d create=%d", objects.putCalls, objects.getCalls, objects.reconcile, bundles.createCall)
	}
	if store.state.VerifiedGeneration != 2 || store.state.Phase != controlplane.BackupRPOPhaseVerified {
		t.Fatalf("state = %#v", store.state)
	}
	got := strings.Join(*calls, ",")
	want := "capability,current,acquire,cycle,create,register,cycle,mark-upload-started,put,record-applied,cycle,get,ack"
	if got != want {
		t.Fatalf("calls = %s\nwant  = %s", got, want)
	}
}

func TestRunnerUploadUnknownPerformsExactlyOnePutThenOnlyReconciles(t *testing.T) {
	runner, _, objects, _, calls := runnerFixture(t, dirtyState())
	objects.putErr = ErrPutOutcomeUnknown

	result := runner.Run(context.Background())

	if result.Code != ResultVerified {
		t.Fatalf("code = %q, want %q", result.Code, ResultVerified)
	}
	if objects.putCalls != 1 || objects.reconcile != 1 {
		t.Fatalf("put=%d reconcile=%d", objects.putCalls, objects.reconcile)
	}
	got := strings.Join(*calls, ",")
	if !strings.Contains(got, "put,record-unknown,cycle,reconcile,record-applied,cycle,get,ack") {
		t.Fatalf("unknown outcome was not durably reconciled: %s", got)
	}
}

func TestRunnerApplyingResumeNeverPutsOrRecaptures(t *testing.T) {
	runner, store, objects, bundles, _ := runnerFixture(t, dirtyState())
	liveLease(store, 1, "lease-token")
	seedAttempt(store, controlplane.BackupRPOAttemptApplying, 1, "lease-token")

	result := runner.Run(context.Background())

	if result.Code != ResultVerified {
		t.Fatalf("code = %q, want %q", result.Code, ResultVerified)
	}
	if objects.putCalls != 0 || objects.reconcile != 1 || bundles.createCall != 0 || bundles.openCall != 0 {
		t.Fatalf("resume calls put=%d reconcile=%d create=%d open=%d", objects.putCalls, objects.reconcile, bundles.createCall, bundles.openCall)
	}
}

func TestRunnerAppliedResumeUsesExactReadbackWithoutBundle(t *testing.T) {
	runner, store, objects, bundles, _ := runnerFixture(t, dirtyState())
	liveLease(store, 1, "lease-token")
	seedAttempt(store, controlplane.BackupRPOAttemptApplied, 1, "lease-token")

	result := runner.Run(context.Background())

	if result.Code != ResultVerified || objects.getCalls != 1 || objects.putCalls != 0 || bundles.createCall != 0 || bundles.openCall != 0 {
		t.Fatalf("result=%q get=%d put=%d create=%d open=%d", result.Code, objects.getCalls, objects.putCalls, bundles.createCall, bundles.openCall)
	}
}

func TestRunnerOneShotRestartReusesLiveSameHolderLeaseWithoutFreshToken(t *testing.T) {
	tests := []struct {
		name          string
		phase         string
		wantPut       int
		wantReconcile int
		wantGet       int
		wantOpen      int
	}{
		{name: "pending", phase: controlplane.BackupRPOAttemptPending, wantPut: 1, wantGet: 1, wantOpen: 1},
		{name: "applying", phase: controlplane.BackupRPOAttemptApplying, wantReconcile: 1, wantGet: 1},
		{name: "applied", phase: controlplane.BackupRPOAttemptApplied, wantGet: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, store, objects, bundles, calls := runnerFixture(t, dirtyState())
			liveLease(store, 7, "durable-lease-token")
			seedAttempt(store, test.phase, 7, "durable-lease-token")
			runner.IDs = &sequenceIDs{}

			result := runner.Run(context.Background())

			if result.Code != ResultVerified {
				t.Fatalf("code = %q, want %q; calls=%s", result.Code, ResultVerified, strings.Join(*calls, ","))
			}
			if objects.putCalls != test.wantPut || objects.reconcile != test.wantReconcile ||
				objects.getCalls != test.wantGet || bundles.createCall != 0 || bundles.openCall != test.wantOpen {
				t.Fatalf("put=%d reconcile=%d get=%d create=%d open=%d", objects.putCalls, objects.reconcile, objects.getCalls, bundles.createCall, bundles.openCall)
			}
			if got := strings.Join(*calls, ","); !strings.Contains(got, "capability,current,renew,cycle") {
				t.Fatalf("same-holder restart did not renew the durable lease: %s", got)
			}
		})
	}
}

func TestRunnerOneShotRestartRejectsForeignLiveLeaseWithoutMintingIdentity(t *testing.T) {
	runner, store, objects, bundles, calls := runnerFixture(t, dirtyState())
	liveLease(store, 7, "foreign-lease-token")
	store.state.Lease.HolderID = "backup-worker-foreign"
	runner.IDs = &sequenceIDs{}

	result := runner.Run(context.Background())

	if result.Code != ResultStaleLease {
		t.Fatalf("code = %q, want %q; calls=%s", result.Code, ResultStaleLease, strings.Join(*calls, ","))
	}
	if objects.putCalls != 0 || objects.getCalls != 0 || bundles.createCall != 0 || bundles.openCall != 0 {
		t.Fatalf("foreign lease reached external work: put=%d get=%d create=%d open=%d", objects.putCalls, objects.getCalls, bundles.createCall, bundles.openCall)
	}
	if got := strings.Join(*calls, ","); got != "capability,current" {
		t.Fatalf("calls = %s, want capability,current", got)
	}
}

func TestRunnerPendingMissingPinnedBundleFailsClosedWithoutRecaptureOrPut(t *testing.T) {
	runner, store, objects, bundles, _ := runnerFixture(t, dirtyState())
	liveLease(store, 1, "lease-token")
	seedAttempt(store, controlplane.BackupRPOAttemptPending, 1, "lease-token")
	bundles.openErr = ErrUnsafeRuntime

	result := runner.Run(context.Background())

	if result.Code != ResultUnsafeRuntime {
		t.Fatalf("code = %q, want %q", result.Code, ResultUnsafeRuntime)
	}
	if objects.putCalls != 0 || bundles.createCall != 0 || bundles.openCall != 1 || store.attempt == nil {
		t.Fatalf("unsafe pending resume changed attempt: put=%d create=%d open=%d attempt=%#v", objects.putCalls, bundles.createCall, bundles.openCall, store.attempt)
	}
}

func TestRunnerMarkStartedFailureCannotReachPut(t *testing.T) {
	runner, store, objects, _, _ := runnerFixture(t, dirtyState())
	store.markStartedErr = errors.New("unknown database outcome")

	result := runner.Run(context.Background())

	if result.Code != ResultInvalidTransition || objects.putCalls != 0 {
		t.Fatalf("code=%q put=%d", result.Code, objects.putCalls)
	}
}

func TestRunnerUnresolvedUnknownRemainsDirtyAndNeverPuts(t *testing.T) {
	runner, store, objects, bundles, _ := runnerFixture(t, dirtyState())
	liveLease(store, 1, "lease-token")
	seedAttempt(store, controlplane.BackupRPOAttemptUnknown, 1, "lease-token")
	objects.reconcileErr = ErrReconcileUnresolved

	result := runner.Run(context.Background())

	if result.Code != ResultVerificationMismatch || objects.putCalls != 0 || bundles.createCall != 0 || store.attempt == nil || store.state.VerifiedGeneration != 1 {
		t.Fatalf("result=%q put=%d create=%d attempt=%#v verified=%d", result.Code, objects.putCalls, bundles.createCall, store.attempt, store.state.VerifiedGeneration)
	}
}

func TestRunnerNewFenceSupersedesBeforeCreatingAnotherAttempt(t *testing.T) {
	runner, store, objects, _, calls := runnerFixture(t, dirtyState())
	seedAttempt(store, controlplane.BackupRPOAttemptUnknown, 1, "old-token")
	store.state.Lease = &controlplane.BackupRPOLease{JobName: controlplane.BackupRPOJobName, HolderID: "old-worker", LeaseToken: "old-token", AcquiredAtUnix: store.now - 120, ExpiresAtUnix: store.now - 1, RestoreEpoch: 4, LeaseFence: 1, Capability: controlplane.BackupRPOCapability{Generation: 1, EvidenceSHA256: strings.Repeat("b", 64), ExpiresAtUnix: store.now - 1}, Live: false}

	result := runner.Run(context.Background())

	if result.Code != ResultVerified || objects.putCalls != 1 {
		t.Fatalf("result=%q put=%d", result.Code, objects.putCalls)
	}
	got := strings.Join(*calls, ",")
	if !strings.Contains(got, "acquire,cycle,supersede,cycle,create,register") {
		t.Fatalf("stale attempt not superseded first: %s", got)
	}
}

func TestRunnerConcurrentDirtyBumpAcknowledgesOnlyCapturedGeneration(t *testing.T) {
	runner, store, _, _, _ := runnerFixture(t, dirtyState())
	store.bumpAfterRegister = true

	result := runner.Run(context.Background())

	if result.Code != ResultVerified || store.state.VerifiedGeneration != 2 || store.state.DirtyGeneration != 3 || store.state.Phase != controlplane.BackupRPOPhaseDirty {
		t.Fatalf("result=%q state=%#v", result.Code, store.state)
	}
}

func TestRunnerCapabilityLossHappensBeforeLeaseOrCapture(t *testing.T) {
	runner, _, objects, bundles, calls := runnerFixture(t, dirtyState())
	runner.Capabilities.(*memoryCapabilityGate).err = ErrVersioningRequired

	result := runner.Run(context.Background())

	if result.Code != ResultCapabilityUnavailable || objects.putCalls != 0 || bundles.createCall != 0 || strings.Join(*calls, ",") != "capability" {
		t.Fatalf("result=%q create=%d calls=%s", result.Code, bundles.createCall, strings.Join(*calls, ","))
	}
}

func TestRunnerKeepsExternallyIssuedCapabilityExpiryWithoutRestamping(t *testing.T) {
	state := dirtyState()
	state.VerifiedGeneration = state.DirtyGeneration
	state.Phase = controlplane.BackupRPOPhaseVerified
	runner, store, _, _, _ := runnerFixture(t, state)
	wantExpiry := store.now + 90
	runner.Config.CapabilityExpiresAtUnix = wantExpiry

	result := runner.Run(context.Background())

	if result.Code != ResultNoop || store.state.Lease == nil {
		t.Fatalf("result=%q lease=%#v", result.Code, store.state.Lease)
	}
	if got := store.state.Lease.Capability.ExpiresAtUnix; got != wantExpiry {
		t.Fatalf("capability expiry = %d, want externally issued %d", got, wantExpiry)
	}
}

func TestRunnerRejectsStaleOrFutureCapabilityBeforeReadinessAndLease(t *testing.T) {
	tests := map[string]func(*RunnerConfig, int64){
		"future issued":         func(config *RunnerConfig, now int64) { config.CapabilityIssuedAtUnix = now + 1 },
		"insufficient validity": func(config *RunnerConfig, now int64) { config.CapabilityExpiresAtUnix = now + 59 },
		"duration exceeds ttl": func(config *RunnerConfig, now int64) {
			config.CapabilityIssuedAtUnix = now - 1
			config.CapabilityExpiresAtUnix = now + 120
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			runner, store, _, bundles, calls := runnerFixture(t, dirtyState())
			mutate(&runner.Config, store.now)
			result := runner.Run(context.Background())
			if result.Code != ResultCapabilityUnavailable || bundles.createCall != 0 || len(*calls) != 0 {
				t.Fatalf("result=%q create=%d calls=%s", result.Code, bundles.createCall, strings.Join(*calls, ","))
			}
		})
	}
}

func TestRunnerTerminalAttemptIsNoop(t *testing.T) {
	runner, store, objects, bundles, _ := runnerFixture(t, dirtyState())
	liveLease(store, 1, "lease-token")
	seedAttempt(store, controlplane.BackupRPOAttemptVerified, 1, "lease-token")

	result := runner.Run(context.Background())

	if result.Code != ResultNoop || objects.putCalls != 0 || objects.getCalls != 0 || bundles.createCall != 0 {
		t.Fatalf("result=%q put=%d get=%d create=%d", result.Code, objects.putCalls, objects.getCalls, bundles.createCall)
	}
}

func TestRunnerBoundsDurableTransitionLoop(t *testing.T) {
	runner, store, objects, _, _ := runnerFixture(t, dirtyState())
	store.registerInvisible = true
	runner.Config.MaxTransitions = 2

	result := runner.Run(context.Background())

	if result.Code != ResultTransitionLimit || objects.putCalls != 0 {
		t.Fatalf("result=%q put=%d", result.Code, objects.putCalls)
	}
}

func TestPublicContractMatchesPythonWorker(t *testing.T) {
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Skip("python unavailable")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	script := `
import json
from ops.ha.backup_worker import AttemptPhase, PUBLIC_ERRORS, decide
from ops.ha.tests.test_backup_worker import applying_state, bound_state, lease, ready_capabilities, state

ready = ready_capabilities()
rows = {
    "no-attempt-clean": decide(bound_state(generation=3), lease=lease(), capabilities=ready, db_now_unix=2_000).action.value,
    "no-attempt-dirty": decide(state(dirty=5, verified=3), lease=lease(), capabilities=ready, db_now_unix=2_000).action.value,
    "pending": decide(applying_state(phase=AttemptPhase.PENDING), lease=lease(), capabilities=ready, db_now_unix=2_003).action.value,
    "applying": decide(applying_state(phase=AttemptPhase.APPLYING), lease=lease(), capabilities=ready, db_now_unix=2_003).action.value,
    "applied": decide(applying_state(phase=AttemptPhase.APPLIED), lease=lease(), capabilities=ready, db_now_unix=2_003).action.value,
    "unknown": decide(applying_state(phase=AttemptPhase.UNKNOWN), lease=lease(), capabilities=ready, db_now_unix=2_003).action.value,
    "newer-fence": decide(applying_state(phase=AttemptPhase.UNKNOWN), lease=lease(fence=12), capabilities=ready, db_now_unix=2_003).action.value,
    "capability-unavailable": decide(state(dirty=5, verified=3), lease=lease(), capabilities=ready_capabilities(object_put=False), db_now_unix=2_000).action.value,
    "lease-unavailable": decide(state(dirty=5, verified=3), lease=None, capabilities=ready, db_now_unix=2_000).action.value,
}
print(json.dumps({
    "phases": sorted(item.value for item in AttemptPhase),
    "failure_codes": sorted(PUBLIC_ERRORS),
    "transitions": rows,
}, sort_keys=True))
`
	command := exec.Command(python, "-c", script)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("python contract: %v", err)
	}
	var pythonContract ContractMatrix
	if err := json.Unmarshal(output, &pythonContract); err != nil {
		t.Fatalf("decode python contract: %v", err)
	}
	wantTransitions := map[string]string{
		"no-attempt-clean":       "wait",
		"no-attempt-dirty":       "create",
		"pending":                "upload",
		"applying":               "verify",
		"applied":                "verify",
		"unknown":                "verify",
		"newer-fence":            "supersede",
		"capability-unavailable": "blocked",
		"lease-unavailable":      "blocked",
	}
	if !reflect.DeepEqual(pythonContract.Transitions, wantTransitions) {
		t.Fatalf("python transition matrix = %#v, want %#v", pythonContract.Transitions, wantTransitions)
	}
	goContract := PublicContract()
	sort.Strings(goContract.Phases)
	sort.Strings(goContract.FailureCodes)
	if strings.Join(goContract.Phases, ",") != strings.Join(pythonContract.Phases, ",") ||
		strings.Join(goContract.FailureCodes, ",") != strings.Join(pythonContract.FailureCodes, ",") ||
		!reflect.DeepEqual(goContract.Transitions, pythonContract.Transitions) {
		t.Fatalf("contract mismatch: go=%#v python=%#v", goContract, pythonContract)
	}
}

func TestPublicTransitionMatrixMatchesRunnerActions(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Runner, *memoryCycleStore)
	}{
		{
			name: "no-attempt-clean",
			setup: func(_ *Runner, store *memoryCycleStore) {
				store.state.VerifiedGeneration = store.state.DirtyGeneration
				store.state.Phase = controlplane.BackupRPOPhaseVerified
			},
		},
		{name: "no-attempt-dirty", setup: func(_ *Runner, _ *memoryCycleStore) {}},
		{
			name: "pending",
			setup: func(_ *Runner, store *memoryCycleStore) {
				liveLease(store, 11, "lease-token")
				seedAttempt(store, controlplane.BackupRPOAttemptPending, 11, "lease-token")
			},
		},
		{
			name: "applying",
			setup: func(_ *Runner, store *memoryCycleStore) {
				liveLease(store, 11, "lease-token")
				seedAttempt(store, controlplane.BackupRPOAttemptApplying, 11, "lease-token")
			},
		},
		{
			name: "applied",
			setup: func(_ *Runner, store *memoryCycleStore) {
				liveLease(store, 11, "lease-token")
				seedAttempt(store, controlplane.BackupRPOAttemptApplied, 11, "lease-token")
			},
		},
		{
			name: "unknown",
			setup: func(_ *Runner, store *memoryCycleStore) {
				liveLease(store, 11, "lease-token")
				seedAttempt(store, controlplane.BackupRPOAttemptUnknown, 11, "lease-token")
			},
		},
		{
			name: "newer-fence",
			setup: func(_ *Runner, store *memoryCycleStore) {
				liveLease(store, 12, "lease-token")
				seedAttempt(store, controlplane.BackupRPOAttemptUnknown, 11, "old-token")
			},
		},
		{
			name: "capability-unavailable",
			setup: func(runner *Runner, _ *memoryCycleStore) {
				runner.Config.CapabilityEvidenceSHA256 = ""
			},
		},
		{
			name: "lease-unavailable",
			setup: func(_ *Runner, store *memoryCycleStore) {
				liveLease(store, 11, "different-token")
				store.state.Lease.HolderID = "worker-other"
			},
		},
	}
	contract := PublicContract()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, store, _, _, calls := runnerFixture(t, dirtyState())
			runner.Config.MaxTransitions = 1
			test.setup(runner, store)
			got := runnerActionForContract(runner.Run(context.Background()), *calls)
			if want := contract.Transitions[test.name]; got != want {
				t.Fatalf("runner action = %q, want %q; calls=%v", got, want, *calls)
			}
		})
	}
}

func runnerActionForContract(result Result, calls []string) string {
	for _, candidate := range []struct{ call, action string }{
		{"supersede", "supersede"}, {"create", "create"}, {"put", "upload"},
		{"reconcile", "verify"}, {"get", "verify"},
	} {
		for _, call := range calls {
			if call == candidate.call {
				return candidate.action
			}
		}
	}
	if result.Code == ResultCapabilityUnavailable || result.Code == ResultStaleLease {
		return "blocked"
	}
	return "wait"
}
