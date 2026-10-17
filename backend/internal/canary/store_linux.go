//go:build linux

package canary

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	serviceName       = "maestro-xray-cdn.service"
	serviceAccount    = "maestro-xray-cdn"
	maxXrayBinarySize = 256 << 20
	maxStateBytes     = 4096
)

type storeConfig struct {
	anchorRoot   string
	optRoot      string
	runRoot      string
	stateRoot    string
	evidenceRoot string
	unitPath     string
	serviceUID   uint32
	serviceGID   uint32
	rootUID      uint32
	rootGID      uint32
	binarySHA256 string
	runtimeID    func() (string, error)
	stepHook     func(string) error
}

type storeImpl struct {
	config storeConfig
}

type stateRecord struct {
	SchemaVersion      int    `json:"schema_version"`
	State              State  `json:"state"`
	RuntimeID          string `json:"runtime_id"`
	SnapshotSHA256     string `json:"snapshot_sha256"`
	XraySHA256         string `json:"xray_sha256"`
	ServerConfigSHA256 string `json:"server_config_sha256"`
	UnitSHA256         string `json:"unit_sha256"`
}

type prepareIntent struct {
	SchemaVersion      int    `json:"schema_version"`
	RuntimeID          string `json:"runtime_id"`
	SnapshotSHA256     string `json:"snapshot_sha256"`
	XraySHA256         string `json:"xray_sha256"`
	ServerConfigSHA256 string `json:"server_config_sha256"`
	DirectClientSHA256 string `json:"direct_client_sha256"`
	CDNClientSHA256    string `json:"cdn_client_sha256"`
	ClientURISHA256    string `json:"client_uri_sha256"`
	UnitSHA256         string `json:"unit_sha256"`
}

type createdFile struct {
	path   string
	digest string
	mode   uint32
	uid    uint32
	gid    uint32
}

func NewStore() (*Store, error) {
	if os.Geteuid() != 0 {
		return nil, invalid("store_requires_root")
	}
	account, err := user.Lookup(serviceAccount)
	if err != nil {
		return nil, invalid("service_identity_unavailable")
	}
	group, err := user.LookupGroup(serviceAccount)
	if err != nil {
		return nil, invalid("service_identity_unavailable")
	}
	uid, err := parseID(account.Uid)
	if err != nil {
		return nil, err
	}
	gid, err := parseID(group.Gid)
	if err != nil {
		return nil, err
	}
	return newStore(storeConfig{
		anchorRoot:   "/",
		optRoot:      "/opt/maestro-xray-cdn",
		runRoot:      "/run/maestro-xray-cdn",
		stateRoot:    "/var/lib/maestro-xray-cdn-canary",
		evidenceRoot: "/root/.maestro-xray-cdn-canary",
		unitPath:     "/etc/systemd/system/" + serviceName,
		serviceUID:   uid,
		serviceGID:   gid,
		rootUID:      0,
		rootGID:      0,
		binarySHA256: pinnedBinarySHA256,
		runtimeID:    generateRuntimeID,
	})
}

func newStoreForTest(config storeConfig) (*Store, error) {
	if config.binarySHA256 == "" {
		config.binarySHA256 = pinnedBinarySHA256
	}
	if config.runtimeID == nil {
		config.runtimeID = generateRuntimeID
	}
	return newStore(config)
}

func newStore(config storeConfig) (*Store, error) {
	if config.anchorRoot == "" || !filepath.IsAbs(config.anchorRoot) || filepath.Clean(config.anchorRoot) != config.anchorRoot {
		return nil, invalid("store_anchor_invalid")
	}
	for _, path := range []string{config.optRoot, config.runRoot, config.stateRoot, config.evidenceRoot, config.unitPath} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, invalid("store_path_invalid")
		}
		relative, err := filepath.Rel(config.anchorRoot, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, invalid("store_path_invalid")
		}
	}
	if !validDigest(config.binarySHA256) || config.runtimeID == nil {
		return nil, invalid("store_config_invalid")
	}
	if config.serviceUID == 0 || config.serviceGID == 0 || config.serviceUID == config.rootUID || config.serviceGID == config.rootGID {
		return nil, invalid("service_identity_invalid")
	}
	return &Store{impl: &storeImpl{config: config}}, nil
}

func parseID(raw string) (uint32, error) {
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, invalid("service_identity_invalid")
	}
	return uint32(value), nil
}

func generateRuntimeID() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", invalid("runtime_id_generation_failed")
	}
	return "r-" + hex.EncodeToString(raw), nil
}

func safeRuntimeID(runtimeID string) bool {
	if len(runtimeID) != 34 || !strings.HasPrefix(runtimeID, "r-") {
		return false
	}
	_, err := hex.DecodeString(runtimeID[2:])
	return err == nil && runtimeID == strings.ToLower(runtimeID)
}

func (s *Store) Prepare(ctx context.Context, snapshot Snapshot, xray []byte, artifacts Artifacts, tester ConfigTester) (stage Stage, resultErr error) {
	impl, err := s.linuxImpl()
	if err != nil {
		return Stage{}, err
	}
	if err := validatePrepareInputs(ctx, snapshot, xray, artifacts, tester, impl.config.binarySHA256); err != nil {
		return Stage{}, err
	}
	if err := impl.validateManagedAncestors(); err != nil {
		return Stage{}, err
	}
	if _, err := ensureDirectory(impl.config.stateRoot, 0o700, impl.config.rootUID, impl.config.rootGID, true); err != nil {
		return Stage{}, err
	}
	lock, err := impl.acquireLifecycleLock(ctx)
	if err != nil {
		return Stage{}, err
	}
	defer lock.release()
	if err := impl.recoverInterruptedPrepare(); err != nil {
		return Stage{}, err
	}
	current, exists, err := impl.loadState()
	if err != nil {
		return Stage{}, err
	}
	if exists && current.State != StateAbsent {
		return Stage{}, invalid("state_not_absent")
	}
	if err := impl.requireServiceAbsent(ctx, tester, "service_absence_unproven"); err != nil {
		return Stage{}, err
	}
	runtimeID, err := impl.config.runtimeID()
	if err != nil || !safeRuntimeID(runtimeID) {
		return Stage{}, invalid("runtime_id_invalid")
	}
	paths := impl.paths(runtimeID)
	if err := impl.preflightPrepare(paths, exists); err != nil {
		return Stage{}, err
	}

	canonicalSnapshot := snapshot.CanonicalJSON()
	unit := impl.unit(runtimeID)
	intent := prepareIntent{
		SchemaVersion:      1,
		RuntimeID:          runtimeID,
		SnapshotSHA256:     digest(canonicalSnapshot),
		XraySHA256:         digest(xray),
		ServerConfigSHA256: digest(artifacts.ServerConfig()),
		DirectClientSHA256: digest(artifacts.DirectClientConfig()),
		CDNClientSHA256:    digest(artifacts.CDNClientConfig()),
		ClientURISHA256:    digest(artifacts.ClientURI()),
		UnitSHA256:         digest(unit),
	}
	intentRaw, err := json.Marshal(intent)
	if err != nil {
		return Stage{}, invalid("intent_encode_failed")
	}
	var files []createdFile
	var dirs []string
	intentTracked := false
	defer func() {
		if resultErr != nil {
			if installed, present, loadErr := impl.loadState(); loadErr == nil && present && stage.RuntimeID != "" && installed == recordFromStage(stage) {
				return
			}
			cleanupErr := impl.cleanupCreated(files, dirs)
			for _, path := range []string{
				deterministicTempPath(paths.state),
				deterministicTempPath(paths.intent),
			} {
				if err := impl.cleanupTrackedTemp(path, 0o600, impl.config.rootUID, impl.config.rootGID); err != nil {
					cleanupErr = invalid("prepare_cleanup_failed")
				}
			}
			if cleanupErr != nil {
				resultErr = cleanupErr
				return
			}
			if intentTracked {
				if err := impl.removeMatching(paths.intent, digest(intentRaw), 0o600, impl.config.rootUID, impl.config.rootGID, true); err != nil {
					resultErr = err
				}
			}
		}
	}()
	intentTracked = true
	if err := impl.atomicNoReplace("intent", paths.intent, intentRaw, 0o600, impl.config.rootUID, impl.config.rootGID); err != nil {
		return Stage{}, err
	}
	if err := impl.prepareDirectories(paths, &dirs); err != nil {
		return Stage{}, err
	}

	writes := []struct {
		label string
		path  string
		raw   []byte
		mode  uint32
		uid   uint32
		gid   uint32
	}{
		{"snapshot", paths.snapshot, canonicalSnapshot, 0o600, impl.config.rootUID, impl.config.rootGID},
		{"client_direct", paths.direct, artifacts.DirectClientConfig(), 0o600, impl.config.rootUID, impl.config.rootGID},
		{"client_cdn", paths.cdn, artifacts.CDNClientConfig(), 0o600, impl.config.rootUID, impl.config.rootGID},
		{"client_uri", paths.uri, artifacts.ClientURI(), 0o600, impl.config.rootUID, impl.config.rootGID},
		{"xray", paths.xray, xray, 0o550, impl.config.rootUID, impl.config.serviceGID},
	}
	for _, write := range writes {
		files = append(files, createdFile{write.path, digest(write.raw), write.mode, write.uid, write.gid})
		if err := impl.atomicNoReplace(write.label, write.path, write.raw, write.mode, write.uid, write.gid); err != nil {
			return Stage{}, err
		}
	}
	if err := impl.setDirMode(paths.runtimeDir, 0o550, impl.config.rootUID, impl.config.serviceGID); err != nil {
		return Stage{}, err
	}
	files = append(files, createdFile{paths.config, digest(artifacts.ServerConfig()), 0o640, impl.config.rootUID, impl.config.serviceGID})
	if err := impl.atomicNoReplace("config", paths.config, artifacts.ServerConfig(), 0o640, impl.config.rootUID, impl.config.serviceGID); err != nil {
		return Stage{}, err
	}
	if err := tester.Test(ctx, paths.xray, paths.config, impl.config.serviceUID, impl.config.serviceGID); err != nil {
		return Stage{}, invalid("config_test_failed")
	}
	files = append(files, createdFile{paths.unit, digest(unit), 0o644, impl.config.rootUID, impl.config.rootGID})
	if err := impl.atomicNoReplace("unit", paths.unit, unit, 0o644, impl.config.rootUID, impl.config.rootGID); err != nil {
		return Stage{}, err
	}

	stage = Stage{runtimeID, StatePrepared, snapshot.SHA256(), digest(xray), digest(artifacts.ServerConfig()), digest(unit)}
	record := recordFromStage(stage)
	if err := impl.writeStateCAS(record, exists, current.State, current.RuntimeID); err != nil {
		// A replace may have completed before a directory-fsync error was
		// reported. If the intended record is now durably readable, retain its
		// exact stage so recovery can roll it back instead of orphaning state.
		if installed, present, loadErr := impl.loadState(); loadErr == nil && present && installed == record {
			files = nil
			dirs = nil
		}
		return stage, err
	}
	files = nil
	dirs = nil
	if err := impl.removeMatching(paths.intent, digest(intentRaw), 0o600, impl.config.rootUID, impl.config.rootGID, false); err != nil {
		return stage, err
	}
	intentTracked = false
	return stage, nil
}

func validatePrepareInputs(ctx context.Context, snapshot Snapshot, xray []byte, artifacts Artifacts, tester ConfigTester, expectedBinaryDigest string) error {
	if ctx == nil || ctx.Err() != nil {
		return invalid("context_invalid")
	}
	if tester == nil || len(xray) == 0 || len(xray) > maxXrayBinarySize {
		return invalid("prepare_input_invalid")
	}
	canonical := snapshot.CanonicalJSON()
	parsed, err := ParseSnapshot(canonical)
	if err != nil || !bytes.Equal(parsed.CanonicalJSON(), canonical) || parsed.SHA256() != snapshot.SHA256() {
		return invalid("snapshot_invalid")
	}
	recomputed, err := parsed.Materialize()
	if err != nil || !equalArtifacts(recomputed, artifacts) {
		return invalid("artifacts_invalid")
	}
	if digest(xray) != expectedBinaryDigest {
		return invalid("xray_digest_invalid")
	}
	return nil
}

func equalArtifacts(left, right Artifacts) bool {
	return bytes.Equal(left.ServerConfig(), right.ServerConfig()) &&
		bytes.Equal(left.DirectClientConfig(), right.DirectClientConfig()) &&
		bytes.Equal(left.CDNClientConfig(), right.CDNClientConfig()) &&
		bytes.Equal(left.ClientURI(), right.ClientURI()) &&
		bytes.Equal(left.Receipt(), right.Receipt())
}

func recordFromStage(stage Stage) stateRecord {
	return stateRecord{1, stage.State, stage.RuntimeID, stage.SnapshotSHA256, stage.XraySHA256, stage.ServerConfigSHA256, stage.UnitSHA256}
}

func stageFromRecord(record stateRecord) Stage {
	return Stage{record.RuntimeID, record.State, record.SnapshotSHA256, record.XraySHA256, record.ServerConfigSHA256, record.UnitSHA256}
}

func (s *Store) Status(ctx context.Context, inspector ServiceInspector) (Stage, error) {
	impl, err := s.linuxImpl()
	if err != nil {
		return Stage{}, err
	}
	if ctx == nil || ctx.Err() != nil || inspector == nil {
		return Stage{}, invalid("context_invalid")
	}
	if err := impl.validateManagedAncestors(); err != nil {
		return Stage{}, err
	}
	exists, err := securePathExists(impl.config.stateRoot)
	if err != nil {
		return Stage{}, err
	}
	if !exists {
		if err := impl.requireAbsentState(ctx, inspector); err != nil {
			return Stage{}, err
		}
		return Stage{State: StateAbsent}, nil
	}
	stateRoot, err := openDirSecure(impl.config.stateRoot)
	if err != nil {
		return Stage{}, err
	}
	if err := verifyDirFD(stateRoot, 0o700, impl.config.rootUID, impl.config.rootGID); err != nil {
		_ = unix.Close(stateRoot)
		return Stage{}, err
	}
	_ = unix.Close(stateRoot)
	lock, err := impl.acquireExistingLifecycleLock(ctx)
	if err != nil {
		return Stage{}, err
	}
	defer lock.release()
	if err := impl.recoverInterruptedPrepare(); err != nil {
		return Stage{}, err
	}
	record, exists, err := impl.loadState()
	if err != nil {
		return Stage{}, err
	}
	if !exists {
		if err := impl.requireAbsentState(ctx, inspector); err != nil {
			return Stage{}, err
		}
		return Stage{State: StateAbsent}, nil
	}
	if record.State == StateAbsent {
		if err := impl.requireAbsentState(ctx, inspector); err != nil {
			return Stage{}, err
		}
		return Stage{State: StateAbsent}, nil
	}
	if err := impl.verifyStage(record, false); err != nil {
		return Stage{}, err
	}
	return stageFromRecord(record), nil
}

func (s *Store) Activate(ctx context.Context, runtimeID string, controller ServiceController) error {
	impl, err := s.linuxImpl()
	if err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil || controller == nil || !safeRuntimeID(runtimeID) {
		return invalid("activate_input_invalid")
	}
	if err := impl.validateManagedAncestors(); err != nil {
		return err
	}
	lock, err := impl.acquireExistingLifecycleLock(ctx)
	if err != nil {
		return err
	}
	defer lock.release()
	if err := impl.recoverInterruptedPrepare(); err != nil {
		return err
	}
	record, exists, err := impl.loadState()
	if err != nil {
		return err
	}
	if !exists || record.State != StatePrepared || record.RuntimeID != runtimeID {
		return invalid("activate_state_invalid")
	}
	if err := impl.verifyStage(record, false); err != nil {
		return err
	}
	if err := controller.Reload(ctx); err != nil {
		return invalid("service_reload_failed")
	}
	if err := impl.authenticatePreparedService(ctx, runtimeID, controller); err != nil {
		return err
	}
	record.State = StateRollbackRequired
	if err := impl.writeStateCAS(record, true, StatePrepared, runtimeID); err != nil {
		return err
	}
	if err := controller.Start(ctx, serviceName); err != nil {
		return invalid("service_start_ambiguous")
	}
	active, err := controller.IsActive(ctx, serviceName)
	if err != nil || !active {
		return invalid("service_active_unproven")
	}
	record.State = StateCanaryActive
	if err := impl.writeStateCAS(record, true, StateRollbackRequired, runtimeID); err != nil {
		return err
	}
	return nil
}

func (s *Store) RollbackToAbsence(ctx context.Context, runtimeID string, controller ServiceController, verifier DiagnosticRestorationVerifier) error {
	impl, err := s.linuxImpl()
	if err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil || controller == nil || verifier == nil || !safeRuntimeID(runtimeID) {
		return invalid("rollback_input_invalid")
	}
	if err := impl.validateManagedAncestors(); err != nil {
		return err
	}
	lock, err := impl.acquireExistingLifecycleLock(ctx)
	if err != nil {
		return err
	}
	defer lock.release()
	record, exists, err := impl.loadState()
	stateLoadErr := err
	_, intentErr := securePathExists(filepath.Join(impl.config.stateRoot, "prepare-intent.json"))
	if err := controller.Stop(ctx, serviceName); err != nil {
		return invalid("service_stop_failed")
	}
	active, err := controller.IsActive(ctx, serviceName)
	if err != nil || active {
		return invalid("service_inactive_unproven")
	}
	if intentErr != nil {
		return intentErr
	}
	if err := impl.recoverInterruptedPrepare(); err != nil {
		return err
	}
	if stateLoadErr != nil {
		return stateLoadErr
	}
	record, exists, err = impl.loadState()
	if err != nil {
		return err
	}
	if !exists || record.RuntimeID != runtimeID {
		return invalid("rollback_state_invalid")
	}
	if record.State == StateAbsent {
		return impl.requireAbsentState(ctx, controller)
	}
	if record.State != StatePrepared && record.State != StateRollbackRequired && record.State != StateCanaryActive {
		return invalid("rollback_state_invalid")
	}
	if record.State != StateRollbackRequired {
		previousState := record.State
		record.State = StateRollbackRequired
		if err := impl.writeStateCAS(record, true, previousState, runtimeID); err != nil {
			return err
		}
	}
	snapshot, err := impl.loadProtectedSnapshot(record)
	if err != nil {
		return err
	}
	if err := verifier.VerifyRestored(ctx, snapshot.Request.DiagnosticProbeURL, snapshot.Request.DiagnosticResponseSHA256); err != nil {
		return invalid("diagnostic_restore_failed")
	}
	if err := impl.verifyStage(record, true); err != nil {
		return err
	}
	artifacts, err := snapshot.Materialize()
	if err != nil {
		return invalid("artifacts_invalid")
	}
	paths := impl.paths(runtimeID)
	if err := impl.removeMatching(paths.config, digest(artifacts.ServerConfig()), 0o640, impl.config.rootUID, impl.config.serviceGID, true); err != nil {
		return err
	}
	if err := impl.removeMatching(paths.unit, digest(impl.unit(runtimeID)), 0o644, impl.config.rootUID, impl.config.rootGID, true); err != nil {
		return err
	}
	if err := controller.Reload(ctx); err != nil {
		return invalid("service_reload_failed")
	}
	if err := impl.requireServiceAbsent(ctx, controller, "service_absence_unproven"); err != nil {
		return err
	}
	record.State = StateAbsent
	return impl.writeStateCAS(record, true, StateRollbackRequired, runtimeID)
}

func (s *Store) linuxImpl() (*storeImpl, error) {
	if s == nil || s.impl == nil {
		return nil, invalid("store_uninitialized")
	}
	return s.impl, nil
}

type stagePaths struct {
	runtimeRoot string
	runtimeDir  string
	xray        string
	config      string
	state       string
	evidenceDir string
	snapshot    string
	direct      string
	cdn         string
	uri         string
	unit        string
	intent      string
}

func (s *storeImpl) paths(runtimeID string) stagePaths {
	runtimeRoot := filepath.Join(s.config.optRoot, "runtime")
	runtimeDir := filepath.Join(runtimeRoot, runtimeID)
	evidenceDir := filepath.Join(s.config.evidenceRoot, runtimeID)
	return stagePaths{
		runtimeRoot: runtimeRoot,
		runtimeDir:  runtimeDir,
		xray:        filepath.Join(runtimeDir, "xray"),
		config:      filepath.Join(s.config.runRoot, "config.json"),
		state:       filepath.Join(s.config.stateRoot, "state.json"),
		evidenceDir: evidenceDir,
		snapshot:    filepath.Join(evidenceDir, "snapshot.json"),
		direct:      filepath.Join(evidenceDir, "client-direct.json"),
		cdn:         filepath.Join(evidenceDir, "client-cdn.json"),
		uri:         filepath.Join(evidenceDir, "client-uri.txt"),
		unit:        s.config.unitPath,
		intent:      filepath.Join(s.config.stateRoot, "prepare-intent.json"),
	}
}

func (s *storeImpl) preflightPrepare(paths stagePaths, stateExists bool) error {
	for _, path := range []string{paths.runtimeDir, paths.xray, paths.config, paths.evidenceDir, paths.snapshot, paths.direct, paths.cdn, paths.uri, paths.unit, paths.intent} {
		exists, err := securePathExists(path)
		if err != nil {
			return err
		}
		if exists {
			return invalid("target_exists")
		}
	}
	if stateExists {
		if _, err := s.readVerified(paths.state, 0o600, s.config.rootUID, s.config.rootGID, maxStateBytes); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeImpl) requireServiceAbsent(ctx context.Context, inspector ServiceInspector, reason string) error {
	if inspector == nil {
		return invalid(reason)
	}
	status, err := inspector.Inspect(ctx, serviceName)
	if err != nil || status.LoadState != "not-found" || status.UnitFileState != "not-found" || status.FragmentPath != "" || len(status.DropInPaths) != 0 || status.User != "" || status.Group != "" || len(status.ExecStart) != 0 {
		return invalid(reason)
	}
	active, err := inspector.IsActive(ctx, serviceName)
	if err != nil || active {
		return invalid(reason)
	}
	return nil
}

func (s *storeImpl) requireAbsentState(ctx context.Context, inspector ServiceInspector) error {
	paths := s.paths("r-00000000000000000000000000000000")
	for _, path := range []string{paths.config, paths.unit} {
		exists, err := securePathExists(path)
		if err != nil {
			return err
		}
		if exists {
			return invalid("absent_state_invalid")
		}
	}
	return s.requireServiceAbsent(ctx, inspector, "absent_state_invalid")
}

func (s *storeImpl) authenticatePreparedService(ctx context.Context, runtimeID string, inspector ServiceInspector) error {
	status, err := inspector.Inspect(ctx, serviceName)
	paths := s.paths(runtimeID)
	wantExecStart := []string{paths.xray, "run", "-config", paths.config}
	if err != nil || status.LoadState != "loaded" || status.UnitFileState != "disabled" || status.FragmentPath != s.config.unitPath || len(status.DropInPaths) != 0 || status.User != serviceAccount || status.Group != serviceAccount || !equalStringSlices(status.ExecStart, wantExecStart) {
		return invalid("service_effective_state_invalid")
	}
	active, err := inspector.IsActive(ctx, serviceName)
	if err != nil || active {
		return invalid("service_inactive_unproven")
	}
	return nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *storeImpl) prepareDirectories(paths stagePaths, created *[]string) error {
	managed := []struct {
		path string
		mode uint32
		uid  uint32
		gid  uint32
	}{
		{s.config.optRoot, 0o750, s.config.rootUID, s.config.serviceGID},
		{paths.runtimeRoot, 0o750, s.config.rootUID, s.config.serviceGID},
		{s.config.runRoot, 0o750, s.config.rootUID, s.config.serviceGID},
		{s.config.stateRoot, 0o700, s.config.rootUID, s.config.rootGID},
		{s.config.evidenceRoot, 0o700, s.config.rootUID, s.config.rootGID},
	}
	for _, directory := range managed {
		wasCreated, err := ensureDirectory(directory.path, directory.mode, directory.uid, directory.gid, true)
		if err != nil {
			return err
		}
		if wasCreated {
			*created = append(*created, directory.path)
		}
	}
	for _, directory := range []struct {
		path string
		mode uint32
		uid  uint32
		gid  uint32
	}{
		{paths.runtimeDir, 0o750, s.config.rootUID, s.config.serviceGID},
		{paths.evidenceDir, 0o700, s.config.rootUID, s.config.rootGID},
	} {
		wasCreated, err := ensureDirectory(directory.path, directory.mode, directory.uid, directory.gid, true)
		if err != nil {
			return err
		}
		if !wasCreated {
			return invalid("target_exists")
		}
		*created = append(*created, directory.path)
	}
	_, err := ensureDirectory(filepath.Dir(paths.unit), 0o700, s.config.rootUID, s.config.rootGID, false)
	return err
}

func (s *storeImpl) unit(runtimeID string) []byte {
	paths := s.paths(runtimeID)
	return []byte(fmt.Sprintf(`[Unit]
Description=MaestroVPN isolated Xray CDN first canary
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=maestro-xray-cdn
Group=maestro-xray-cdn
WorkingDirectory=%s
LogsDirectory=maestro-xray-cdn
LogsDirectoryMode=0750
ExecStartPre=%s run -test -config %s
ExecStart=%s run -config %s
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadOnlyPaths=%s %s %s
ReadWritePaths=/var/log/maestro-xray-cdn

[Install]
WantedBy=multi-user.target
`, paths.runtimeDir, paths.xray, paths.config, paths.xray, paths.config, paths.runtimeDir, s.config.runRoot, paths.config))
}

func (s *storeImpl) verifyStage(record stateRecord, rollback bool) error {
	paths := s.paths(record.RuntimeID)
	if err := s.verifyManagedDirectories(paths); err != nil {
		return err
	}
	xray, err := s.readVerified(paths.xray, 0o550, s.config.rootUID, s.config.serviceGID, maxXrayBinarySize)
	if err != nil || digest(xray) != s.config.binarySHA256 || record.XraySHA256 != s.config.binarySHA256 {
		return invalid("runtime_verification_failed")
	}
	snapshotRaw, err := s.readVerified(paths.snapshot, 0o600, s.config.rootUID, s.config.rootGID, maxJSONBytes)
	if err != nil || digest(snapshotRaw) != record.SnapshotSHA256 {
		return invalid("snapshot_verification_failed")
	}
	snapshot, err := ParseSnapshot(snapshotRaw)
	if err != nil || !bytes.Equal(snapshot.CanonicalJSON(), snapshotRaw) {
		return invalid("snapshot_verification_failed")
	}
	artifacts, err := snapshot.Materialize()
	if err != nil {
		return invalid("artifacts_invalid")
	}
	unit := s.unit(record.RuntimeID)
	if record.ServerConfigSHA256 != digest(artifacts.ServerConfig()) || record.UnitSHA256 != digest(unit) {
		return invalid("state_digest_mismatch")
	}
	checks := []struct {
		path  string
		mode  uint32
		uid   uint32
		gid   uint32
		limit int
		want  []byte
	}{
		{paths.config, 0o640, s.config.rootUID, s.config.serviceGID, maxJSONBytes, artifacts.ServerConfig()},
		{paths.unit, 0o644, s.config.rootUID, s.config.rootGID, maxJSONBytes, unit},
		{paths.direct, 0o600, s.config.rootUID, s.config.rootGID, maxJSONBytes, artifacts.DirectClientConfig()},
		{paths.cdn, 0o600, s.config.rootUID, s.config.rootGID, maxJSONBytes, artifacts.CDNClientConfig()},
		{paths.uri, 0o600, s.config.rootUID, s.config.rootGID, maxJSONBytes, artifacts.ClientURI()},
	}
	for index, check := range checks {
		raw, err := s.readVerified(check.path, check.mode, check.uid, check.gid, check.limit)
		if rollback && index < 2 && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !bytes.Equal(raw, check.want) {
			return invalid("runnable_verification_failed")
		}
	}
	return nil
}

func (s *storeImpl) verifyManagedDirectories(paths stagePaths) error {
	checks := []struct {
		path string
		mode uint32
		uid  uint32
		gid  uint32
	}{
		{s.config.optRoot, 0o750, s.config.rootUID, s.config.serviceGID},
		{paths.runtimeRoot, 0o750, s.config.rootUID, s.config.serviceGID},
		{paths.runtimeDir, 0o550, s.config.rootUID, s.config.serviceGID},
		{s.config.runRoot, 0o750, s.config.rootUID, s.config.serviceGID},
		{s.config.stateRoot, 0o700, s.config.rootUID, s.config.rootGID},
		{s.config.evidenceRoot, 0o700, s.config.rootUID, s.config.rootGID},
		{paths.evidenceDir, 0o700, s.config.rootUID, s.config.rootGID},
	}
	for _, check := range checks {
		fd, err := openDirSecure(check.path)
		if err != nil {
			return err
		}
		err = verifyDirFD(fd, check.mode, check.uid, check.gid)
		_ = unix.Close(fd)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *storeImpl) loadProtectedSnapshot(record stateRecord) (Snapshot, error) {
	paths := s.paths(record.RuntimeID)
	raw, err := s.readVerified(paths.snapshot, 0o600, s.config.rootUID, s.config.rootGID, maxJSONBytes)
	if err != nil || digest(raw) != record.SnapshotSHA256 {
		return Snapshot{}, invalid("snapshot_verification_failed")
	}
	snapshot, err := ParseSnapshot(raw)
	if err != nil || !bytes.Equal(snapshot.CanonicalJSON(), raw) {
		return Snapshot{}, invalid("snapshot_verification_failed")
	}
	return snapshot, nil
}

func (s *storeImpl) loadState() (stateRecord, bool, error) {
	path := filepath.Join(s.config.stateRoot, "state.json")
	raw, err := s.readVerified(path, 0o600, s.config.rootUID, s.config.rootGID, maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return stateRecord{}, false, nil
	}
	if err != nil {
		return stateRecord{}, false, err
	}
	var record stateRecord
	if err := decodeCanonical(raw, &record); err != nil || !validStateRecord(record) {
		return stateRecord{}, false, invalid("state_invalid")
	}
	return record, true, nil
}

func (s *storeImpl) recoverInterruptedPrepare() error {
	if err := s.cleanupStaleControlTemps(); err != nil {
		return err
	}
	path := filepath.Join(s.config.stateRoot, "prepare-intent.json")
	raw, err := s.readVerified(path, 0o600, s.config.rootUID, s.config.rootGID, maxStateBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var intent prepareIntent
	if err := decodeCanonical(raw, &intent); err != nil || !validPrepareIntent(intent) {
		return invalid("prepare_intent_invalid")
	}
	record, exists, err := s.loadState()
	if err != nil {
		return err
	}
	paths := s.paths(intent.RuntimeID)
	if exists && record.RuntimeID == intent.RuntimeID && record.State != StateAbsent {
		if record.SnapshotSHA256 != intent.SnapshotSHA256 || record.XraySHA256 != intent.XraySHA256 || record.ServerConfigSHA256 != intent.ServerConfigSHA256 || record.UnitSHA256 != intent.UnitSHA256 {
			return invalid("prepare_intent_state_mismatch")
		}
		if err := s.cleanupInterruptedTemps(paths); err != nil {
			return err
		}
		return s.removeMatching(paths.intent, digest(raw), 0o600, s.config.rootUID, s.config.rootGID, false)
	}
	if err := s.cleanupInterruptedTemps(paths); err != nil {
		return err
	}
	removals := []struct {
		label  string
		path   string
		digest string
		mode   uint32
		uid    uint32
		gid    uint32
	}{
		{"unit", paths.unit, intent.UnitSHA256, 0o644, s.config.rootUID, s.config.rootGID},
		{"config", paths.config, intent.ServerConfigSHA256, 0o640, s.config.rootUID, s.config.serviceGID},
		{"xray", paths.xray, intent.XraySHA256, 0o550, s.config.rootUID, s.config.serviceGID},
		{"client_uri", paths.uri, intent.ClientURISHA256, 0o600, s.config.rootUID, s.config.rootGID},
		{"client_cdn", paths.cdn, intent.CDNClientSHA256, 0o600, s.config.rootUID, s.config.rootGID},
		{"client_direct", paths.direct, intent.DirectClientSHA256, 0o600, s.config.rootUID, s.config.rootGID},
		{"snapshot", paths.snapshot, intent.SnapshotSHA256, 0o600, s.config.rootUID, s.config.rootGID},
	}
	for _, removal := range removals {
		if err := s.hook("recovery:before_remove_" + removal.label); err != nil {
			return err
		}
		if err := s.removeMatching(removal.path, removal.digest, removal.mode, removal.uid, removal.gid, true); err != nil {
			return err
		}
	}
	for _, directory := range []string{paths.runtimeDir, paths.evidenceDir} {
		if err := removeEmptyDirectoryIfExists(directory); err != nil {
			return invalid("recovery_cleanup_incomplete")
		}
	}
	return s.removeMatching(paths.intent, digest(raw), 0o600, s.config.rootUID, s.config.rootGID, false)
}

func (s *storeImpl) cleanupStaleControlTemps() error {
	for _, path := range []string{
		filepath.Join(s.config.stateRoot, ".state.json.tmp"),
		filepath.Join(s.config.stateRoot, ".prepare-intent.json.tmp"),
	} {
		if err := s.cleanupTrackedTemp(path, 0o600, s.config.rootUID, s.config.rootGID); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeImpl) cleanupInterruptedTemps(paths stagePaths) error {
	checks := []struct {
		path string
		mode uint32
		uid  uint32
		gid  uint32
	}{
		{deterministicTempPath(paths.snapshot), 0o600, s.config.rootUID, s.config.rootGID},
		{deterministicTempPath(paths.direct), 0o600, s.config.rootUID, s.config.rootGID},
		{deterministicTempPath(paths.cdn), 0o600, s.config.rootUID, s.config.rootGID},
		{deterministicTempPath(paths.uri), 0o600, s.config.rootUID, s.config.rootGID},
		{deterministicTempPath(paths.xray), 0o550, s.config.rootUID, s.config.serviceGID},
		{deterministicTempPath(paths.config), 0o640, s.config.rootUID, s.config.serviceGID},
		{deterministicTempPath(paths.unit), 0o644, s.config.rootUID, s.config.rootGID},
	}
	for _, check := range checks {
		if err := s.cleanupTrackedTemp(check.path, check.mode, check.uid, check.gid); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeImpl) cleanupTrackedTemp(path string, mode, uid, gid uint32) error {
	parent, base, err := openParent(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if err := verifySafeAncestorFD(parent, s.config.rootUID); err != nil {
		return err
	}
	fd, err := unix.Openat(parent, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		if err := unix.Fsync(parent); err != nil {
			return invalid("directory_fsync_failed")
		}
		return nil
	}
	if err != nil {
		return invalid("temp_open_failed")
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(fd, &stat)
	_ = unix.Close(fd)
	permissions := uint32(stat.Mode & 0o777)
	ownerOK := stat.Uid == uid && (stat.Gid == gid || stat.Gid == s.config.rootGID)
	modeOK := stat.Mode&0o7000 == 0 && permissions&^mode == 0
	if statErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || !ownerOK || !modeOK {
		return invalid("temp_metadata_invalid")
	}
	if err := unix.Unlinkat(parent, base, 0); err != nil {
		return invalid("temp_remove_failed")
	}
	if err := unix.Fsync(parent); err != nil {
		return invalid("directory_fsync_failed")
	}
	return nil
}

func validPrepareIntent(intent prepareIntent) bool {
	return intent.SchemaVersion == 1 && safeRuntimeID(intent.RuntimeID) &&
		validDigest(intent.SnapshotSHA256) && validDigest(intent.XraySHA256) &&
		validDigest(intent.ServerConfigSHA256) && validDigest(intent.DirectClientSHA256) &&
		validDigest(intent.CDNClientSHA256) && validDigest(intent.ClientURISHA256) &&
		validDigest(intent.UnitSHA256)
}

func validStateRecord(record stateRecord) bool {
	if record.SchemaVersion != 1 || !safeRuntimeID(record.RuntimeID) || !validDigest(record.SnapshotSHA256) || !validDigest(record.XraySHA256) || !validDigest(record.ServerConfigSHA256) || !validDigest(record.UnitSHA256) {
		return false
	}
	switch record.State {
	case StateAbsent, StatePrepared, StateRollbackRequired, StateCanaryActive:
		return true
	default:
		return false
	}
}

func (s *storeImpl) writeState(record stateRecord, replace bool) error {
	if !validStateRecord(record) {
		return invalid("state_invalid")
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return invalid("state_encode_failed")
	}
	path := filepath.Join(s.config.stateRoot, "state.json")
	if replace {
		return s.atomicReplace("state", path, raw, 0o600, s.config.rootUID, s.config.rootGID)
	}
	return s.atomicNoReplace("state", path, raw, 0o600, s.config.rootUID, s.config.rootGID)
}

func (s *storeImpl) writeStateCAS(record stateRecord, expectedExists bool, expectedState State, expectedRuntime string) error {
	current, exists, err := s.loadState()
	if err != nil {
		return err
	}
	if exists != expectedExists {
		return invalid("state_cas_failed")
	}
	if exists && (current.State != expectedState || current.RuntimeID != expectedRuntime) {
		return invalid("state_cas_failed")
	}
	return s.writeState(record, exists)
}

func (s *storeImpl) atomicNoReplace(label, path string, raw []byte, mode, uid, gid uint32) error {
	return s.atomicWrite(label, path, raw, mode, uid, gid, false)
}

func (s *storeImpl) atomicReplace(label, path string, raw []byte, mode, uid, gid uint32) error {
	return s.atomicWrite(label, path, raw, mode, uid, gid, true)
}

func (s *storeImpl) atomicWrite(label, path string, raw []byte, mode, uid, gid uint32, replace bool) error {
	if err := s.hook(label + ":before_open"); err != nil {
		return err
	}
	parent, base, err := openParent(path)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if replace {
		if _, err := readVerifiedAt(parent, base, mode, uid, gid, maxStateBytes); err != nil {
			return err
		}
	} else if exists, err := pathExistsAt(parent, base); err != nil || exists {
		if err != nil {
			return err
		}
		return invalid("target_exists")
	}
	temp := deterministicTempName(base)
	fd, err := unix.Openat(parent, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return invalid("temp_create_failed")
	}
	tempExists := true
	installed := false
	defer func() {
		unix.Close(fd)
		if tempExists {
			_ = unix.Unlinkat(parent, temp, 0)
		}
		if installed && !replace && label != "state" {
			_ = unix.Unlinkat(parent, base, 0)
			_ = unix.Fsync(parent)
		}
	}()
	if err := s.hook(label + ":temp_opened"); err != nil {
		return err
	}
	if err := unix.Fchown(fd, int(uid), int(gid)); err != nil {
		return invalid("file_owner_failed")
	}
	if err := unix.Fchmod(fd, mode); err != nil {
		return invalid("file_mode_failed")
	}
	if err := s.hook(label + ":temp_create"); err != nil {
		return err
	}
	if err := writeAll(fd, raw); err != nil {
		return err
	}
	if err := s.hook(label + ":before_file_fsync"); err != nil {
		return err
	}
	if err := unix.Fsync(fd); err != nil {
		return invalid("file_fsync_failed")
	}
	if err := s.hook(label + ":file_fsync"); err != nil {
		return err
	}
	if err := unix.Close(fd); err != nil {
		return invalid("file_close_failed")
	}
	fd = -1
	if err := s.hook(label + ":before_rename"); err != nil {
		return err
	}
	if replace {
		if _, err := readVerifiedAt(parent, base, mode, uid, gid, maxStateBytes); err != nil {
			return err
		}
		if err := unix.Renameat(parent, temp, parent, base); err != nil {
			return invalid("rename_failed")
		}
	} else if err := unix.Renameat2(parent, temp, parent, base, unix.RENAME_NOREPLACE); err != nil {
		return invalid("rename_failed")
	}
	tempExists = false
	installed = true
	renameStep := ":rename_noreplace"
	if replace {
		renameStep = ":rename_replace"
	}
	if err := s.hook(label + renameStep); err != nil {
		return err
	}
	if err := unix.Fsync(parent); err != nil {
		return invalid("directory_fsync_failed")
	}
	if err := s.hook(label + ":dir_fsync"); err != nil {
		return err
	}
	installed = false
	return nil
}

func (s *storeImpl) hook(step string) error {
	if s.config.stepHook == nil {
		return nil
	}
	if err := s.config.stepHook(step); err != nil {
		return invalid("fault_injected")
	}
	return nil
}

type lifecycleLock struct {
	fd int
}

func (s *storeImpl) acquireLifecycleLock(ctx context.Context) (*lifecycleLock, error) {
	return s.acquireLifecycleLockMode(ctx, true)
}

func (s *storeImpl) acquireExistingLifecycleLock(ctx context.Context) (*lifecycleLock, error) {
	return s.acquireLifecycleLockMode(ctx, false)
}

func (s *storeImpl) acquireLifecycleLockMode(ctx context.Context, create bool) (*lifecycleLock, error) {
	parent, err := openDirSecure(s.config.stateRoot)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	const name = "lifecycle.lock"
	fd, err := unix.Openat(parent, name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		if !create {
			return nil, invalid("lock_missing")
		}
		fd, err = unix.Openat(parent, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(err, unix.EEXIST) {
			fd, err = unix.Openat(parent, name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		} else if err == nil {
			if chownErr := unix.Fchown(fd, int(s.config.rootUID), int(s.config.rootGID)); chownErr != nil {
				unix.Close(fd)
				return nil, invalid("lock_owner_failed")
			}
			if chmodErr := unix.Fchmod(fd, 0o600); chmodErr != nil {
				unix.Close(fd)
				return nil, invalid("lock_mode_failed")
			}
			if fsyncErr := unix.Fsync(fd); fsyncErr != nil {
				unix.Close(fd)
				return nil, invalid("lock_fsync_failed")
			}
			if fsyncErr := unix.Fsync(parent); fsyncErr != nil {
				unix.Close(fd)
				return nil, invalid("directory_fsync_failed")
			}
		}
	}
	if err != nil {
		return nil, invalid("lock_open_failed")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != s.config.rootUID || stat.Gid != s.config.rootGID || stat.Mode&0o777 != 0o600 {
		unix.Close(fd)
		return nil, invalid("lock_metadata_invalid")
	}
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return &lifecycleLock{fd: fd}, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			unix.Close(fd)
			return nil, invalid("lock_failed")
		}
		select {
		case <-ctx.Done():
			unix.Close(fd)
			return nil, invalid("context_invalid")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (l *lifecycleLock) release() {
	if l == nil || l.fd < 0 {
		return
	}
	_ = unix.Flock(l.fd, unix.LOCK_UN)
	_ = unix.Close(l.fd)
	l.fd = -1
}

func (s *storeImpl) readVerified(path string, mode, uid, gid uint32, limit int) ([]byte, error) {
	parent, base, err := openParent(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	return readVerifiedAt(parent, base, mode, uid, gid, limit)
}

func readVerifiedAt(parent int, base string, mode, uid, gid uint32, limit int) ([]byte, error) {
	fd, err := unix.Openat(parent, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, invalid("file_open_failed")
	}
	file := os.NewFile(uintptr(fd), base)
	if file == nil {
		unix.Close(fd)
		return nil, invalid("file_open_failed")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Uid != uid || stat.Gid != gid || stat.Mode&0o777 != mode {
		return nil, invalid("file_metadata_invalid")
	}
	reader := io.LimitReader(file, int64(limit)+1)
	raw, err := io.ReadAll(reader)
	if err != nil || len(raw) > limit {
		return nil, invalid("file_read_failed")
	}
	return raw, nil
}

func (s *storeImpl) removeMatching(path, expectedDigest string, mode, uid, gid uint32, allowMissing bool) error {
	parent, base, err := openParent(path)
	if err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer unix.Close(parent)
	raw, err := readVerifiedAt(parent, base, mode, uid, gid, maxXrayBinarySize)
	if allowMissing && errors.Is(err, os.ErrNotExist) {
		if err := unix.Fsync(parent); err != nil {
			return invalid("directory_fsync_failed")
		}
		return nil
	}
	if err != nil || digest(raw) != expectedDigest {
		return invalid("remove_verification_failed")
	}
	if err := unix.Unlinkat(parent, base, 0); err != nil {
		return invalid("remove_failed")
	}
	if err := unix.Fsync(parent); err != nil {
		return invalid("directory_fsync_failed")
	}
	return nil
}

func (s *storeImpl) cleanupCreated(files []createdFile, dirs []string) error {
	clean := true
	for index := len(files) - 1; index >= 0; index-- {
		file := files[index]
		if err := s.removeMatching(file.path, file.digest, file.mode, file.uid, file.gid, true); err != nil {
			clean = false
		}
		if err := s.cleanupTrackedTemp(deterministicTempPath(file.path), file.mode, file.uid, file.gid); err != nil {
			clean = false
		}
	}
	if clean {
		for index := len(dirs) - 1; index >= 0; index-- {
			if err := removeEmptyDirectory(dirs[index]); err != nil {
				clean = false
			}
		}
	}
	if !clean {
		return invalid("prepare_cleanup_failed")
	}
	return nil
}

func removeEmptyDirectory(path string) error {
	parent, base, err := openParent(path)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if err := unix.Unlinkat(parent, base, unix.AT_REMOVEDIR); errors.Is(err, unix.ENOENT) {
		return unix.Fsync(parent)
	} else if err != nil {
		return err
	}
	return unix.Fsync(parent)
}

func removeEmptyDirectoryIfExists(path string) error {
	err := removeEmptyDirectory(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func (s *storeImpl) setDirMode(path string, mode, uid, gid uint32) error {
	fd, err := openDirSecure(path)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := verifyDirFD(fd, 0o750, uid, gid); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, mode); err != nil {
		return invalid("directory_mode_failed")
	}
	return unix.Fsync(fd)
}

func ensureDirectory(path string, mode, uid, gid uint32, strict bool) (bool, error) {
	components, err := absoluteComponents(path)
	if err != nil {
		return false, err
	}
	current, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, invalid("directory_open_failed")
	}
	defer unix.Close(current)
	createdFinal := false
	for index, component := range components {
		last := index == len(components)-1
		next, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) {
			createMode := uint32(0o700)
			if last {
				createMode = mode
			}
			if err := unix.Mkdirat(current, component, createMode); err != nil {
				return false, invalid("directory_create_failed")
			}
			next, openErr = unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if openErr != nil {
				return false, invalid("directory_open_failed")
			}
			if err := unix.Fchown(next, int(uid), int(gid)); err != nil {
				unix.Close(next)
				return false, invalid("directory_owner_failed")
			}
			if err := unix.Fchmod(next, createMode); err != nil {
				unix.Close(next)
				return false, invalid("directory_mode_failed")
			}
			if err := unix.Fsync(next); err != nil {
				unix.Close(next)
				return false, invalid("directory_fsync_failed")
			}
			if err := unix.Fsync(current); err != nil {
				unix.Close(next)
				return false, invalid("directory_fsync_failed")
			}
			if last {
				createdFinal = true
			}
		} else if openErr != nil {
			return false, invalid("directory_unsafe")
		}
		unix.Close(current)
		current = next
	}
	if strict {
		if err := verifyDirFD(current, mode, uid, gid); err != nil {
			return false, err
		}
	}
	return createdFinal, nil
}

func verifyDirFD(fd int, mode, uid, gid uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uid || stat.Gid != gid || stat.Mode&0o777 != mode {
		return invalid("directory_metadata_invalid")
	}
	return nil
}

func securePathExists(path string) (bool, error) {
	parent, base, err := openParent(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unix.Close(parent)
	return pathExistsAt(parent, base)
}

func pathExistsAt(parent int, base string) (bool, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(parent, base, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, invalid("target_check_failed")
	}
	return true, nil
}

func openParent(path string) (int, string, error) {
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || strings.ContainsRune(base, filepath.Separator) {
		return -1, "", invalid("store_path_invalid")
	}
	parent, err := openDirSecure(filepath.Dir(clean))
	return parent, base, err
}

func openDirSecure(path string) (int, error) {
	if filepath.Clean(path) == string(filepath.Separator) {
		fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return -1, invalid("directory_open_failed")
		}
		return fd, nil
	}
	components, err := absoluteComponents(path)
	if err != nil {
		return -1, err
	}
	current, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, invalid("directory_open_failed")
	}
	for _, component := range components {
		next, err := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(current)
		if errors.Is(err, unix.ENOENT) {
			return -1, os.ErrNotExist
		}
		if err != nil {
			return -1, invalid("directory_unsafe")
		}
		current = next
	}
	return current, nil
}

func absoluteComponents(path string) ([]string, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
		return nil, invalid("store_path_invalid")
	}
	components := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, invalid("store_path_invalid")
		}
	}
	return components, nil
}

func (s *storeImpl) validateManagedAncestors() error {
	for _, target := range []string{s.config.optRoot, s.config.runRoot, s.config.stateRoot, s.config.evidenceRoot, filepath.Dir(s.config.unitPath)} {
		if err := validateAnchoredChain(s.config.anchorRoot, target, s.config.rootUID); err != nil {
			return err
		}
	}
	return nil
}

func validateAnchoredChain(anchor, target string, owner uint32) error {
	relative, err := filepath.Rel(anchor, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return invalid("store_path_invalid")
	}
	current, err := openDirSecure(anchor)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(current) }()
	if err := verifySafeAncestorFD(current, owner); err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		next, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) {
			return nil
		}
		if openErr != nil {
			return invalid("directory_unsafe")
		}
		unix.Close(current)
		current = next
		if err := verifySafeAncestorFD(current, owner); err != nil {
			return err
		}
	}
	return nil
}

func verifySafeAncestorFD(fd int, owner uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != owner || stat.Mode&0o022 != 0 {
		return invalid("directory_unsafe")
	}
	return nil
}

func deterministicTempName(base string) string {
	return "." + base + ".tmp"
}

func deterministicTempPath(path string) string {
	return filepath.Join(filepath.Dir(path), deterministicTempName(filepath.Base(path)))
}

func writeAll(fd int, raw []byte) error {
	for len(raw) > 0 {
		written, err := unix.Write(fd, raw)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || written <= 0 {
			return invalid("file_write_failed")
		}
		raw = raw[written:]
	}
	return nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
