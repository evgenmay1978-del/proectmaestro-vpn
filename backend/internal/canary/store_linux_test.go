//go:build linux

package canary

import (
	"bytes"
	"context"
	"crypto/mlkem"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

const testRuntimeID = "r-0123456789abcdef0123456789abcdef"

type recordingTester struct {
	calls      int
	binaryPath string
	configPath string
	uid        uint32
	gid        uint32
	err        error
}

func (f *recordingTester) Test(_ context.Context, binaryPath, configPath string, uid, gid uint32) error {
	f.calls++
	f.binaryPath, f.configPath, f.uid, f.gid = binaryPath, configPath, uid, gid
	return f.err
}

type fakeController struct {
	mu           sync.Mutex
	active       bool
	isActiveErr  error
	isActive     func(int) (bool, error)
	startErr     error
	stopErr      error
	reloadErr    error
	reloads      int
	starts       int
	stops        int
	activeCalls  int
	beforeStart  func()
	serviceNames []string
	events       []string
	wrongService bool
}

func (f *fakeController) Reload(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "reload")
	f.reloads++
	return f.reloadErr
}
func (f *fakeController) IsActive(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serviceNames = append(f.serviceNames, name)
	f.events = append(f.events, "is-active")
	if name != serviceName {
		f.wrongService = true
		return false, errors.New("wrong service")
	}
	f.activeCalls++
	if f.isActive != nil {
		return f.isActive(f.activeCalls)
	}
	return f.active, f.isActiveErr
}
func (f *fakeController) Start(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serviceNames = append(f.serviceNames, name)
	f.events = append(f.events, "start")
	if name != serviceName {
		f.wrongService = true
		return errors.New("wrong service")
	}
	if f.beforeStart != nil {
		f.beforeStart()
	}
	f.starts++
	if f.startErr == nil {
		f.active = true
	}
	return f.startErr
}
func (f *fakeController) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serviceNames = append(f.serviceNames, name)
	f.events = append(f.events, "stop")
	if name != serviceName {
		f.wrongService = true
		return errors.New("wrong service")
	}
	f.stops++
	if f.stopErr == nil {
		f.active = false
	}
	return f.stopErr
}

type fakeOrigin struct {
	calls          int
	probeURL       string
	responseSHA256 string
	err            error
}

func (f *fakeOrigin) RestoreAndVerify(_ context.Context, probeURL, responseSHA256 string) error {
	f.calls++
	f.probeURL, f.responseSHA256 = probeURL, responseSHA256
	return f.err
}

func testStore(t *testing.T, binary []byte, hook func(string) error) (*Store, storeConfig) {
	t.Helper()
	return testStoreAtRoot(t, t.TempDir(), binary, testRuntimeID, hook)
}

func testStoreAtRoot(t *testing.T, root string, binary []byte, runtimeID string, hook func(string) error) (*Store, storeConfig) {
	t.Helper()
	digest := sha256.Sum256(binary)
	rootUID := uint32(os.Getuid())
	serviceUID := rootUID + 1
	if rootUID == ^uint32(0) {
		serviceUID = rootUID - 1
	}
	config := storeConfig{
		anchorRoot:   root,
		optRoot:      filepath.Join(root, "opt", "maestro-xray-cdn"),
		runRoot:      filepath.Join(root, "run", "maestro-xray-cdn"),
		stateRoot:    filepath.Join(root, "var", "lib", "maestro-xray-cdn-canary"),
		evidenceRoot: filepath.Join(root, "root", ".maestro-xray-cdn-canary"),
		unitPath:     filepath.Join(root, "etc", "systemd", "system", serviceName),
		serviceUID:   serviceUID,
		serviceGID:   uint32(os.Getgid()),
		rootUID:      rootUID,
		rootGID:      uint32(os.Getgid()),
		binarySHA256: hex.EncodeToString(digest[:]),
		runtimeID:    func() (string, error) { return runtimeID, nil },
		stepHook:     hook,
	}
	store, err := newStoreForTest(config)
	if err != nil {
		t.Fatalf("newStoreForTest: %v", err)
	}
	return store, config
}

func TestStoreCrashHelper(t *testing.T) {
	point := os.Getenv("MAESTRO_CANARY_CRASH_POINT")
	root := os.Getenv("MAESTRO_CANARY_CRASH_ROOT")
	if point == "" || root == "" {
		t.Skip("subprocess helper")
	}
	snapshot, artifacts, binary := testStoreInputs(t)
	store, _ := testStoreAtRoot(t, root, binary, testRuntimeID, func(step string) error {
		if step == point {
			os.Exit(86)
		}
		return nil
	})
	_, _ = store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester))
	t.Fatal("Prepare returned without simulated crash")
}

func testStoreInputs(t *testing.T) (Snapshot, Artifacts, []byte) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x42}, 64)
	key, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		t.Fatal(err)
	}
	material := Material{
		ClientID:         "123e4567-e89b-12d3-a456-426614174000",
		ClientEmail:      "canary@example.invalid",
		ServerDecryption: "mlkem768x25519plus.native.600s." + base64.RawURLEncoding.EncodeToString(seed),
		ClientEncryption: "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(key.EncapsulationKey().Bytes()),
		SecretPath:       "/xhttp-canary",
	}
	pair, err := json.Marshal(struct {
		ClientID         string `json:"client_id"`
		ClientEmail      string `json:"client_email"`
		ServerDecryption string `json:"server_decryption"`
		ClientEncryption string `json:"client_encryption"`
		SecretPath       string `json:"secret_path"`
	}{material.ClientID, material.ClientEmail, material.ServerDecryption, material.ClientEncryption, material.SecretPath})
	if err != nil {
		t.Fatal(err)
	}
	pairDigest := sha256.Sum256(pair)
	material.PairTranscriptSHA256 = hex.EncodeToString(pairDigest[:])
	snapshot, err := NewSnapshot(Request{
		SchemaVersion:            1,
		PublicHost:               "cdn.example.invalid",
		DiagnosticProbeURL:       "https://cdn.example.invalid/health",
		DiagnosticResponseSHA256: strings.Repeat("a", 64),
	}, material)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	artifacts, err := snapshot.Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return snapshot, artifacts, []byte("synthetic pinned xray binary")
}

func TestStorePrepareCreatesProtectedStage(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	store, config := testStore(t, binary, nil)
	tester := new(recordingTester)

	stage, err := store.Prepare(context.Background(), snapshot, binary, artifacts, tester)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if stage.RuntimeID != testRuntimeID || stage.State != StatePrepared || stage.SnapshotSHA256 != snapshot.SHA256() {
		t.Fatalf("unexpected stage: %#v", stage)
	}
	runtimeDir := filepath.Join(config.optRoot, "runtime", testRuntimeID)
	xrayPath := filepath.Join(runtimeDir, "xray")
	configPath := filepath.Join(config.runRoot, "config.json")
	unitPath := config.unitPath
	if stage.XraySHA256 != digestBytes(binary) || stage.ServerConfigSHA256 != digestBytes(artifacts.ServerConfig()) {
		t.Fatalf("stage data digests do not match actual inputs: %#v", stage)
	}
	if tester.calls != 1 || tester.binaryPath != xrayPath || tester.configPath != configPath || tester.uid != config.serviceUID || tester.gid != config.serviceGID {
		t.Fatalf("tester observed (%d, %q, %q, %d, %d)", tester.calls, tester.binaryPath, tester.configPath, tester.uid, tester.gid)
	}

	assertMetadata(t, config.optRoot, 0o750, config.rootUID, config.serviceGID, false)
	assertMetadata(t, filepath.Join(config.optRoot, "runtime"), 0o750, config.rootUID, config.serviceGID, false)
	assertMetadata(t, runtimeDir, 0o550, config.rootUID, config.serviceGID, false)
	assertMetadata(t, xrayPath, 0o550, config.rootUID, config.serviceGID, true)
	assertMetadata(t, config.runRoot, 0o750, config.rootUID, config.serviceGID, false)
	assertMetadata(t, configPath, 0o640, config.rootUID, config.serviceGID, true)
	assertMetadata(t, config.stateRoot, 0o700, config.rootUID, config.rootGID, false)
	assertMetadata(t, filepath.Join(config.stateRoot, "state.json"), 0o600, config.rootUID, config.rootGID, true)
	evidenceDir := filepath.Join(config.evidenceRoot, testRuntimeID)
	assertMetadata(t, config.evidenceRoot, 0o700, config.rootUID, config.rootGID, false)
	assertMetadata(t, evidenceDir, 0o700, config.rootUID, config.rootGID, false)
	for _, name := range []string{"snapshot.json", "client-direct.json", "client-cdn.json", "client-uri.txt"} {
		assertMetadata(t, filepath.Join(evidenceDir, name), 0o600, config.rootUID, config.rootGID, true)
	}
	assertMetadata(t, config.unitPath, 0o644, config.rootUID, config.rootGID, true)

	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	if stage.UnitSHA256 != digestBytes(unit) {
		t.Fatalf("unit digest = %q, want actual %q", stage.UnitSHA256, digestBytes(unit))
	}
	for _, want := range []string{
		"User=maestro-xray-cdn", "Group=maestro-xray-cdn", "UMask=0077", "NoNewPrivileges=true",
		"PrivateTmp=true", "ProtectSystem=strict", "ProtectHome=true", "ReadWritePaths=/var/log/maestro-xray-cdn",
		"ExecStartPre=" + xrayPath + " run -test -config " + configPath,
		"ExecStart=" + xrayPath + " run -config " + configPath,
	} {
		if !bytes.Contains(unit, []byte(want)) {
			t.Errorf("unit missing %q", want)
		}
	}
	if bytes.Contains(unit, []byte("x-ui")) || bytes.Contains(unit, []byte(snapshot.Request.DiagnosticProbeURL)) || bytes.Contains(unit, []byte(snapshot.Material.ClientID)) {
		t.Fatal("unit contains ordinary listener path or secret evidence")
	}
}

func TestStoreStatusReturnsAuthoritativelyVerifiedStage(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	store, config := testStore(t, binary, nil)
	absent, err := store.Status(context.Background())
	if err != nil || absent.State != StateAbsent || absent.RuntimeID != "" {
		t.Fatalf("initial Status = (%#v, %v)", absent, err)
	}
	if _, err := os.Lstat(config.stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initial Status mutated pristine state root: %v", err)
	}
	prepared, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester))
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(context.Background())
	if err != nil || status != prepared {
		t.Fatalf("prepared Status = (%#v, %v), want %#v", status, err, prepared)
	}
	path := filepath.Join(config.evidenceRoot, testRuntimeID, "client-direct.json")
	if err := os.WriteFile(path, []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Status(context.Background()); err == nil {
		t.Fatal("Status accepted retained artifact drift")
	}
}

func TestStoreLifecycleAndRollback(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	store, config := testStore(t, binary, nil)
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
		t.Fatal("double Prepare succeeded")
	}
	controller := new(fakeController)
	controller.beforeStart = func() { assertState(t, config, StateRollbackRequired) }
	if err := store.Activate(context.Background(), testRuntimeID, controller); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if controller.reloads != 1 || controller.starts != 1 || !controller.active {
		t.Fatalf("unexpected activation calls: %#v", controller)
	}
	assertExactServiceNames(t, controller)
	assertState(t, config, StateCanaryActive)
	if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
		t.Fatal("double Activate succeeded")
	}
	origin := new(fakeOrigin)
	if err := store.RollbackToAbsence(context.Background(), testRuntimeID, controller, origin); err != nil {
		t.Fatalf("RollbackToAbsence: %v", err)
	}
	if origin.calls != 1 || origin.probeURL != snapshot.Request.DiagnosticProbeURL || origin.responseSHA256 != snapshot.Request.DiagnosticResponseSHA256 {
		t.Fatalf("unexpected origin restoration: %#v", origin)
	}
	if controller.stops != 1 || controller.reloads != 2 || controller.active {
		t.Fatalf("unexpected rollback calls: %#v", controller)
	}
	assertExactServiceNames(t, controller)
	assertState(t, config, StateAbsent)
	for _, path := range []string{filepath.Join(config.runRoot, "config.json"), config.unitPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runnable path still exists: %s (%v)", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(config.optRoot, "runtime", testRuntimeID, "xray"),
		filepath.Join(config.evidenceRoot, testRuntimeID, "snapshot.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("immutable evidence missing: %s: %v", path, err)
		}
	}
	if err := store.RollbackToAbsence(context.Background(), testRuntimeID, controller, origin); err != nil {
		t.Fatalf("idempotent rollback: %v", err)
	}
	if origin.calls != 1 || controller.stops != 1 {
		t.Fatal("idempotent rollback repeated side effects")
	}
}

func TestStoreRejectsSubstitutionBeforeExecution(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	t.Run("wrong binary digest", func(t *testing.T) {
		store, _ := testStore(t, binary, nil)
		tester := new(recordingTester)
		if _, err := store.Prepare(context.Background(), snapshot, append(binary, '!'), artifacts, tester); err == nil {
			t.Fatal("wrong binary digest accepted")
		}
		if tester.calls != 0 {
			t.Fatal("tester called before digest rejection")
		}
	})
	t.Run("substituted artifact", func(t *testing.T) {
		store, _ := testStore(t, binary, nil)
		substituted := artifacts
		substituted.serverConfig = append(substituted.ServerConfig(), ' ')
		tester := new(recordingTester)
		if _, err := store.Prepare(context.Background(), snapshot, binary, substituted, tester); err == nil {
			t.Fatal("substituted artifact accepted")
		}
		if tester.calls != 0 {
			t.Fatal("tester called before artifact rejection")
		}
	})
}

func TestStoreRefusesUnsafePathsWithoutVictimModification(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	t.Run("symlink parent", func(t *testing.T) {
		store, config := testStore(t, binary, nil)
		victim := t.TempDir()
		marker := filepath.Join(victim, "marker")
		if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(config.optRoot), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, config.optRoot); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
			t.Fatal("symlink parent accepted")
		}
		if got, _ := os.ReadFile(marker); string(got) != "unchanged" {
			t.Fatalf("victim modified: %q", got)
		}
	})
	t.Run("preexisting unit", func(t *testing.T) {
		store, config := testStore(t, binary, nil)
		if err := os.MkdirAll(filepath.Dir(config.unitPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.unitPath, []byte("victim"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
			t.Fatal("preexisting unit accepted")
		}
		if got, _ := os.ReadFile(config.unitPath); string(got) != "victim" {
			t.Fatalf("preexisting target modified: %q", got)
		}
	})
}

func TestStoreActivationFailsClosed(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	cases := []struct {
		name       string
		controller *fakeController
	}{
		{"already active", &fakeController{active: true}},
		{"unknown active state", &fakeController{isActiveErr: errors.New("unknown")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, config := testStore(t, binary, nil)
			if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
				t.Fatal(err)
			}
			if err := store.Activate(context.Background(), testRuntimeID, tc.controller); err == nil {
				t.Fatal("Activate succeeded")
			}
			if tc.controller.starts != 0 {
				t.Fatal("Start called after fail-closed check")
			}
			assertState(t, config, StatePrepared)
		})
	}

	t.Run("ambiguous start remains rollback required", func(t *testing.T) {
		store, config := testStore(t, binary, nil)
		if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
			t.Fatal(err)
		}
		controller := &fakeController{startErr: errors.New("ambiguous")}
		if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
			t.Fatal("ambiguous Start succeeded")
		}
		assertState(t, config, StateRollbackRequired)
	})

	t.Run("post-start active check failure remains rollback required", func(t *testing.T) {
		store, config := testStore(t, binary, nil)
		if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
			t.Fatal(err)
		}
		controller := &fakeController{isActive: func(call int) (bool, error) {
			if call == 1 {
				return false, nil
			}
			return false, errors.New("post-start unknown")
		}}
		controller.beforeStart = func() { assertState(t, config, StateRollbackRequired) }
		if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
			t.Fatal("Activate succeeded through post-start status failure")
		}
		if controller.starts != 1 || controller.activeCalls != 2 {
			t.Fatalf("unexpected controller calls: %#v", controller)
		}
		assertState(t, config, StateRollbackRequired)
	})
}

func TestStoreAtomicOrderingAndFailureCleanup(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	var steps []string
	store, config := testStore(t, binary, func(step string) error {
		steps = append(steps, step)
		return nil
	})
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
		t.Fatal(err)
	}
	assertOrdered(t, steps, "config:temp_create", "config:file_fsync", "config:rename_noreplace", "config:dir_fsync", "unit:rename_noreplace", "state:dir_fsync")

	t.Run("directory fsync failure leaves no runnable layer", func(t *testing.T) {
		failed := false
		store, config = testStore(t, binary, func(step string) error {
			if step == "config:dir_fsync" && !failed {
				failed = true
				return errors.New("injected")
			}
			return nil
		})
		if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
			t.Fatal("Prepare succeeded through fsync failure")
		}
		for _, path := range []string{filepath.Join(config.runRoot, "config.json"), config.unitPath, filepath.Join(config.stateRoot, "state.json")} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial runnable state remains: %s (%v)", path, err)
			}
		}
	})
}

func TestStorePrepareReturnsRecoveryStageAfterStateFsyncAmbiguity(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	failed := false
	store, config := testStore(t, binary, func(step string) error {
		if step == "state:dir_fsync" && !failed {
			failed = true
			return errors.New("injected")
		}
		return nil
	})
	stage, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester))
	if err == nil {
		t.Fatal("Prepare succeeded through state fsync ambiguity")
	}
	if stage.RuntimeID != testRuntimeID || stage.State != StatePrepared || stage.XraySHA256 != digestBytes(binary) {
		t.Fatalf("recovery stage not populated: %#v", stage)
	}
	assertState(t, config, StatePrepared)
	for _, path := range []string{
		filepath.Join(config.optRoot, "runtime", testRuntimeID, "xray"),
		filepath.Join(config.runRoot, "config.json"),
		config.unitPath,
	} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("recovery artifact missing: %s: %v", path, statErr)
		}
	}
}

func TestStoreRecoversInterruptedPrepareAcrossProcessCrash(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	for _, point := range []string{"config:dir_fsync", "unit:dir_fsync", "state:temp_create", "state:before_file_fsync", "state:before_rename"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			runCrashHelper(t, root, point)
			recoveryRuntimeID := "r-fedcba9876543210fedcba9876543210"
			store, config := testStoreAtRoot(t, root, binary, recoveryRuntimeID, nil)
			stage, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester))
			if err != nil {
				t.Fatalf("recovery Prepare: %v", err)
			}
			if stage.RuntimeID != recoveryRuntimeID || stage.State != StatePrepared {
				t.Fatalf("unexpected recovery stage: %#v", stage)
			}
			if _, err := os.Lstat(filepath.Join(config.stateRoot, "prepare-intent.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prepare intent remains after recovery: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(config.optRoot, "runtime", testRuntimeID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("interrupted runtime remains after recovery: %v", err)
			}
			entries, err := os.ReadDir(config.stateRoot)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".state.json.tmp") {
					t.Fatalf("stale state temporary remains after recovery: %s", entry.Name())
				}
			}
		})
	}
}

func TestStorePreservesIntentWhenRecoveryCleanupFails(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	root := t.TempDir()
	runCrashHelper(t, root, "unit:dir_fsync")
	recoveryRuntimeID := "r-fedcba9876543210fedcba9876543210"
	store, config := testStoreAtRoot(t, root, binary, recoveryRuntimeID, func(step string) error {
		if step == "recovery:before_remove_config" {
			return errors.New("injected cleanup failure")
		}
		return nil
	})
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
		t.Fatal("Prepare succeeded through recovery cleanup failure")
	}
	if _, err := os.Stat(filepath.Join(config.stateRoot, "prepare-intent.json")); err != nil {
		t.Fatalf("recoverable intent was lost: %v", err)
	}
	store, _ = testStoreAtRoot(t, root, binary, recoveryRuntimeID, nil)
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
		t.Fatalf("second recovery Prepare: %v", err)
	}
}

func TestStoreSerializesLifecycleAcrossInstances(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	first, config := testStore(t, binary, nil)
	if _, err := first.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
		t.Fatal(err)
	}
	second, err := newStoreForTest(config)
	if err != nil {
		t.Fatal(err)
	}
	controller := new(fakeController)
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var wait sync.WaitGroup
	for _, store := range []*Store{first, second} {
		wait.Add(1)
		go func(store *Store) {
			defer wait.Done()
			<-start
			errorsOut <- store.Activate(context.Background(), testRuntimeID, controller)
		}(store)
	}
	close(start)
	wait.Wait()
	close(errorsOut)
	successes := 0
	for err := range errorsOut {
		if err == nil {
			successes++
		}
	}
	if successes != 1 || controller.starts != 1 {
		t.Fatalf("serialized Activate successes=%d starts=%d", successes, controller.starts)
	}
	if err := second.RollbackToAbsence(context.Background(), testRuntimeID, controller, new(fakeOrigin)); err != nil {
		t.Fatal(err)
	}
	assertState(t, config, StateAbsent)
	if controller.starts != 1 {
		t.Fatal("stale activation ran after rollback")
	}
	assertExactServiceNames(t, controller)
}

func TestStoreParentSwapRaceDoesNotReachVictim(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	victim := t.TempDir()
	marker := filepath.Join(victim, "marker")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	store, config := testStore(t, binary, func(step string) error {
		if step == "config:before_open" && !swapped {
			swapped = true
			if err := os.Rename(configRunRootFromHook, configRunRootFromHook+".safe"); err != nil {
				return err
			}
			return os.Symlink(victim, configRunRootFromHook)
		}
		return nil
	})
	configRunRootFromHook = config.runRoot
	t.Cleanup(func() { configRunRootFromHook = "" })
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
		t.Fatal("Prepare succeeded through parent swap")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "unchanged" {
		t.Fatalf("parent swap victim modified: %q, %v", got, err)
	}
}

var configRunRootFromHook string

func runCrashHelper(t *testing.T, root, point string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestStoreCrashHelper$")
	command.Env = append(os.Environ(), "MAESTRO_CANARY_CRASH_ROOT="+root, "MAESTRO_CANARY_CRASH_POINT="+point)
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 {
		t.Fatalf("crash helper at %s = %v", point, err)
	}
}

func TestStoreMetadataSubstitutionFailsClosed(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	store, config := testStore(t, binary, nil)
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
		t.Fatal(err)
	}
	unitAlias := config.unitPath + ".alias"
	if err := os.Link(config.unitPath, unitAlias); err != nil {
		t.Fatal(err)
	}
	controller := new(fakeController)
	if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
		t.Fatal("Activate accepted hardlinked unit")
	}
	if controller.reloads != 0 || controller.starts != 0 {
		t.Fatal("controller called before metadata validation")
	}
}

func TestStoreDigestSubstitutionFailsClosed(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	for _, tc := range []struct {
		name string
		path func(storeConfig) string
	}{
		{"runtime", func(config storeConfig) string {
			return filepath.Join(config.optRoot, "runtime", testRuntimeID, "xray")
		}},
		{"config", func(config storeConfig) string { return filepath.Join(config.runRoot, "config.json") }},
		{"unit", func(config storeConfig) string { return config.unitPath }},
	} {
		t.Run("activate "+tc.name, func(t *testing.T) {
			store, config := testStore(t, binary, nil)
			if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
				t.Fatal(err)
			}
			path := tc.path(config)
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("substituted victim"), 0o600); err != nil {
				t.Fatal(err)
			}
			controller := new(fakeController)
			if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
				t.Fatal("Activate accepted digest substitution")
			}
			if controller.reloads != 0 || controller.starts != 0 {
				t.Fatal("controller called before digest validation")
			}
		})
	}

	t.Run("rollback preserves substituted config victim", func(t *testing.T) {
		store, config := testStore(t, binary, nil)
		if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(config.runRoot, "config.json")
		victim := []byte("substituted victim")
		if err := os.WriteFile(path, victim, 0o640); err != nil {
			t.Fatal(err)
		}
		controller := new(fakeController)
		origin := new(fakeOrigin)
		if err := store.RollbackToAbsence(context.Background(), testRuntimeID, controller, origin); err == nil {
			t.Fatal("RollbackToAbsence accepted substituted config")
		}
		if controller.stops != 1 || controller.activeCalls != 1 || controller.reloads != 0 || origin.calls != 0 {
			t.Fatal("rollback did not stop before digest validation")
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, victim) {
			t.Fatalf("rollback removed or modified victim: %q, %v", got, err)
		}
	})
}

func TestStoreDoesNotTrustStateDigests(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	for _, tc := range []struct {
		name       string
		path       func(storeConfig) string
		update     func(*stateRecord, string)
		needsChmod bool
	}{
		{"pinned runtime", func(config storeConfig) string {
			return filepath.Join(config.optRoot, "runtime", testRuntimeID, "xray")
		}, func(record *stateRecord, value string) { record.XraySHA256 = value }, true},
		{"rematerialized server config", func(config storeConfig) string { return filepath.Join(config.runRoot, "config.json") }, func(record *stateRecord, value string) { record.ServerConfigSHA256 = value }, false},
		{"regenerated unit", func(config storeConfig) string { return config.unitPath }, func(record *stateRecord, value string) { record.UnitSHA256 = value }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, config := testStore(t, binary, nil)
			if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
				t.Fatal(err)
			}
			path := tc.path(config)
			if tc.needsChmod {
				if err := os.Chmod(path, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			victim := []byte("attacker-controlled replacement")
			if err := os.WriteFile(path, victim, 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.needsChmod {
				if err := os.Chmod(path, 0o550); err != nil {
					t.Fatal(err)
				}
			}
			mutateState(t, config, func(record *stateRecord) { tc.update(record, digestBytes(victim)) })
			controller := new(fakeController)
			if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
				t.Fatal("Activate trusted attacker-controlled state digest")
			}
			if controller.reloads != 0 || controller.starts != 0 {
				t.Fatal("controller called before authoritative revalidation")
			}
		})
	}
}

func TestStoreVerifiesEveryRetainedArtifact(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	for _, name := range []string{"client-direct.json", "client-cdn.json", "client-uri.txt"} {
		t.Run(name, func(t *testing.T) {
			store, config := testStore(t, binary, nil)
			if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(config.evidenceRoot, testRuntimeID, name)
			if err := os.WriteFile(path, []byte("retained artifact drift"), 0o600); err != nil {
				t.Fatal(err)
			}
			controller := new(fakeController)
			if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
				t.Fatal("Activate accepted retained artifact drift")
			}
			if controller.reloads != 0 {
				t.Fatal("controller called before retained artifact validation")
			}
		})
	}
}

func TestStoreRollbackStopsBeforeDriftValidation(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	for _, tc := range []struct {
		name       string
		path       func(storeConfig) string
		needsChmod bool
	}{
		{"runtime", func(config storeConfig) string {
			return filepath.Join(config.optRoot, "runtime", testRuntimeID, "xray")
		}, true},
		{"config", func(config storeConfig) string { return filepath.Join(config.runRoot, "config.json") }, false},
		{"unit", func(config storeConfig) string { return config.unitPath }, false},
		{"retained client", func(config storeConfig) string {
			return filepath.Join(config.evidenceRoot, testRuntimeID, "client-cdn.json")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, config := testStore(t, binary, nil)
			if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err != nil {
				t.Fatal(err)
			}
			path := tc.path(config)
			if tc.needsChmod {
				if err := os.Chmod(path, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			victim := []byte("substituted victim")
			if err := os.WriteFile(path, victim, 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.needsChmod {
				if err := os.Chmod(path, 0o550); err != nil {
					t.Fatal(err)
				}
			}
			controller := &fakeController{active: true}
			origin := new(fakeOrigin)
			if err := store.RollbackToAbsence(context.Background(), testRuntimeID, controller, origin); err == nil {
				t.Fatal("RollbackToAbsence accepted drift")
			}
			if controller.stops != 1 || controller.active || controller.activeCalls != 1 {
				t.Fatalf("rollback did not stop and prove inactive first: %#v", controller)
			}
			assertOrdered(t, controller.events, "stop", "is-active")
			assertExactServiceNames(t, controller)
			if origin.calls != 0 {
				t.Fatal("origin restoration ran after artifact drift")
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, victim) {
				t.Fatalf("rollback removed or modified drift victim: %q, %v", got, err)
			}
		})
	}
}

func TestStoreRejectsUnsafeManagedAncestor(t *testing.T) {
	snapshot, artifacts, binary := testStoreInputs(t)
	store, config := testStore(t, binary, nil)
	unsafe := filepath.Dir(config.optRoot)
	if err := os.MkdirAll(unsafe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prepare(context.Background(), snapshot, binary, artifacts, new(recordingTester)); err == nil {
		t.Fatal("Prepare accepted group/world-writable managed ancestor")
	}
	if _, err := os.Lstat(config.optRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe ancestor was traversed or modified: %v", err)
	}
}

func assertMetadata(t *testing.T, path string, mode os.FileMode, uid, gid uint32, oneLink bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if info.Mode().Perm() != mode {
		t.Errorf("%s mode = %04o, want %04o", path, info.Mode().Perm(), mode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s has unexpected stat type %T", path, info.Sys())
	}
	if stat.Uid != uid || stat.Gid != gid {
		t.Errorf("%s owner = %d:%d, want %d:%d", path, stat.Uid, stat.Gid, uid, gid)
	}
	if oneLink && stat.Nlink != 1 {
		t.Errorf("%s nlink = %d, want 1", path, stat.Nlink)
	}
}

func assertState(t *testing.T, config storeConfig, want State) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(config.stateRoot, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record stateRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.SchemaVersion != 1 || record.State != want || record.RuntimeID != testRuntimeID {
		t.Fatalf("state = %#v, want %s for %s", record, want, testRuntimeID)
	}
	if bytes.Contains(raw, []byte("client_id")) || bytes.Contains(raw, []byte("diagnostic")) || bytes.Contains(raw, []byte("https://")) {
		t.Fatalf("state leaked protected fields: %s", raw)
	}
}

func mutateState(t *testing.T, config storeConfig, mutate func(*stateRecord)) {
	t.Helper()
	path := filepath.Join(config.stateRoot, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record stateRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	mutate(&record)
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertExactServiceNames(t *testing.T, controller *fakeController) {
	t.Helper()
	if controller.wrongService {
		t.Fatalf("controller received wrong service name: %#v", controller.serviceNames)
	}
	for _, name := range controller.serviceNames {
		if name != "maestro-xray-cdn.service" {
			t.Fatalf("service name = %q, want maestro-xray-cdn.service", name)
		}
	}
}

func assertOrdered(t *testing.T, got []string, want ...string) {
	t.Helper()
	position := -1
	for _, expected := range want {
		found := -1
		for index := position + 1; index < len(got); index++ {
			if got[index] == expected {
				found = index
				break
			}
		}
		if found < 0 {
			t.Fatalf("step %q not found after %d in %#v", expected, position, got)
		}
		position = found
	}
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
