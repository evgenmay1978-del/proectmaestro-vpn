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
	"path/filepath"
	"strings"
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
	active      bool
	isActiveErr error
	isActive    func(int) (bool, error)
	startErr    error
	stopErr     error
	reloadErr   error
	reloads     int
	starts      int
	stops       int
	activeCalls int
	beforeStart func()
}

func (f *fakeController) Reload(context.Context) error { f.reloads++; return f.reloadErr }
func (f *fakeController) IsActive(context.Context, string) (bool, error) {
	f.activeCalls++
	if f.isActive != nil {
		return f.isActive(f.activeCalls)
	}
	return f.active, f.isActiveErr
}
func (f *fakeController) Start(context.Context, string) error {
	if f.beforeStart != nil {
		f.beforeStart()
	}
	f.starts++
	if f.startErr == nil {
		f.active = true
	}
	return f.startErr
}
func (f *fakeController) Stop(context.Context, string) error {
	f.stops++
	if f.stopErr == nil {
		f.active = false
	}
	return f.stopErr
}

type fakeOrigin struct {
	calls int
	host  string
	url   string
	err   error
}

func (f *fakeOrigin) RestoreAndVerify(_ context.Context, host, url string) error {
	f.calls++
	f.host, f.url = host, url
	return f.err
}

func testStore(t *testing.T, binary []byte, hook func(string) error) (*Store, storeConfig) {
	t.Helper()
	root := t.TempDir()
	digest := sha256.Sum256(binary)
	config := storeConfig{
		optRoot:      filepath.Join(root, "opt", "maestro-xray-cdn"),
		runRoot:      filepath.Join(root, "run", "maestro-xray-cdn"),
		stateRoot:    filepath.Join(root, "var", "lib", "maestro-xray-cdn-canary"),
		evidenceRoot: filepath.Join(root, "root", ".maestro-xray-cdn-canary"),
		unitPath:     filepath.Join(root, "etc", "systemd", "system", serviceName),
		serviceUID:   uint32(os.Getuid()),
		serviceGID:   uint32(os.Getgid()),
		binarySHA256: hex.EncodeToString(digest[:]),
		runtimeID:    func() (string, error) { return testRuntimeID, nil },
		stepHook:     hook,
	}
	store, err := newStoreForTest(config)
	if err != nil {
		t.Fatalf("newStoreForTest: %v", err)
	}
	return store, config
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

	assertMetadata(t, config.optRoot, 0o750, config.serviceUID, config.serviceGID, false)
	assertMetadata(t, filepath.Join(config.optRoot, "runtime"), 0o750, config.serviceUID, config.serviceGID, false)
	assertMetadata(t, runtimeDir, 0o550, config.serviceUID, config.serviceGID, false)
	assertMetadata(t, xrayPath, 0o550, config.serviceUID, config.serviceGID, true)
	assertMetadata(t, config.runRoot, 0o750, config.serviceUID, config.serviceGID, false)
	assertMetadata(t, configPath, 0o640, config.serviceUID, config.serviceGID, true)
	assertMetadata(t, config.stateRoot, 0o700, config.serviceUID, config.serviceGID, false)
	assertMetadata(t, filepath.Join(config.stateRoot, "state.json"), 0o600, config.serviceUID, config.serviceGID, true)
	evidenceDir := filepath.Join(config.evidenceRoot, testRuntimeID)
	assertMetadata(t, config.evidenceRoot, 0o700, config.serviceUID, config.serviceGID, false)
	assertMetadata(t, evidenceDir, 0o700, config.serviceUID, config.serviceGID, false)
	for _, name := range []string{"snapshot.json", "client-direct.json", "client-cdn.json", "client-uri.txt"} {
		assertMetadata(t, filepath.Join(evidenceDir, name), 0o600, config.serviceUID, config.serviceGID, true)
	}
	assertMetadata(t, config.unitPath, 0o644, config.serviceUID, config.serviceGID, true)

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
	assertState(t, config, StateCanaryActive)
	if err := store.Activate(context.Background(), testRuntimeID, controller); err == nil {
		t.Fatal("double Activate succeeded")
	}
	origin := new(fakeOrigin)
	if err := store.RollbackToAbsence(context.Background(), testRuntimeID, controller, origin); err != nil {
		t.Fatalf("RollbackToAbsence: %v", err)
	}
	if origin.calls != 1 || origin.host != snapshot.Request.PublicHost || origin.url != snapshot.Request.DiagnosticProbeURL {
		t.Fatalf("unexpected origin restoration: %#v", origin)
	}
	if controller.stops != 1 || controller.reloads != 2 || controller.active {
		t.Fatalf("unexpected rollback calls: %#v", controller)
	}
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
		if controller.stops != 0 || controller.reloads != 0 || origin.calls != 0 {
			t.Fatal("rollback side effects occurred before digest validation")
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, victim) {
			t.Fatalf("rollback removed or modified victim: %q, %v", got, err)
		}
	})
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
