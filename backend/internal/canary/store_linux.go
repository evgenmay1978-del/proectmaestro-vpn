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

	"golang.org/x/sys/unix"
)

const (
	serviceName       = "maestro-xray-cdn.service"
	serviceAccount    = "maestro-xray-cdn"
	maxXrayBinarySize = 256 << 20
	maxStateBytes     = 4096
)

type storeConfig struct {
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
	config.rootUID = config.serviceUID
	config.rootGID = config.serviceGID
	if config.binarySHA256 == "" {
		config.binarySHA256 = pinnedBinarySHA256
	}
	if config.runtimeID == nil {
		config.runtimeID = generateRuntimeID
	}
	return newStore(config)
}

func newStore(config storeConfig) (*Store, error) {
	for _, path := range []string{config.optRoot, config.runRoot, config.stateRoot, config.evidenceRoot, config.unitPath} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, invalid("store_path_invalid")
		}
	}
	if !validDigest(config.binarySHA256) || config.runtimeID == nil {
		return nil, invalid("store_config_invalid")
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
	current, exists, err := impl.loadState()
	if err != nil {
		return Stage{}, err
	}
	if exists && current.State != StateAbsent {
		return Stage{}, invalid("state_not_absent")
	}
	runtimeID, err := impl.config.runtimeID()
	if err != nil || !safeRuntimeID(runtimeID) {
		return Stage{}, invalid("runtime_id_invalid")
	}
	paths := impl.paths(runtimeID)
	if err := impl.preflightPrepare(paths, exists); err != nil {
		return Stage{}, err
	}

	var files []createdFile
	var dirs []string
	defer func() {
		if resultErr != nil {
			impl.cleanupCreated(files, dirs)
		}
	}()
	if err := impl.prepareDirectories(paths, &dirs); err != nil {
		return Stage{}, err
	}

	canonicalSnapshot := snapshot.CanonicalJSON()
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
		{"xray", paths.xray, xray, 0o550, impl.config.serviceUID, impl.config.serviceGID},
		{"config", paths.config, artifacts.ServerConfig(), 0o640, impl.config.serviceUID, impl.config.serviceGID},
	}
	for _, write := range writes {
		if err := impl.atomicNoReplace(write.label, write.path, write.raw, write.mode, write.uid, write.gid); err != nil {
			return Stage{}, err
		}
		files = append(files, createdFile{write.path, digest(write.raw), write.mode, write.uid, write.gid})
	}
	if err := impl.setDirMode(paths.runtimeDir, 0o550, impl.config.serviceUID, impl.config.serviceGID); err != nil {
		return Stage{}, err
	}
	if err := tester.Test(ctx, paths.xray, paths.config, impl.config.serviceUID, impl.config.serviceGID); err != nil {
		return Stage{}, invalid("config_test_failed")
	}
	unit := impl.unit(runtimeID)
	if err := impl.atomicNoReplace("unit", paths.unit, unit, 0o644, impl.config.rootUID, impl.config.rootGID); err != nil {
		return Stage{}, err
	}
	files = append(files, createdFile{paths.unit, digest(unit), 0o644, impl.config.rootUID, impl.config.rootGID})

	stage = Stage{runtimeID, StatePrepared, snapshot.SHA256(), digest(xray), digest(artifacts.ServerConfig()), digest(unit)}
	record := recordFromStage(stage)
	if err := impl.writeState(record, exists); err != nil {
		// A replace may have completed before a directory-fsync error was
		// reported. If the intended record is now durably readable, retain its
		// exact stage so recovery can roll it back instead of orphaning state.
		if installed, present, loadErr := impl.loadState(); loadErr == nil && present && installed == record {
			files = nil
			dirs = nil
		}
		return Stage{}, err
	}
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

func (s *Store) Activate(ctx context.Context, runtimeID string, controller ServiceController) error {
	impl, err := s.linuxImpl()
	if err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil || controller == nil || !safeRuntimeID(runtimeID) {
		return invalid("activate_input_invalid")
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
	active, err := controller.IsActive(ctx, serviceName)
	if err != nil || active {
		return invalid("service_inactive_unproven")
	}
	record.State = StateRollbackRequired
	if err := impl.writeState(record, true); err != nil {
		return err
	}
	if err := controller.Start(ctx, serviceName); err != nil {
		return invalid("service_start_ambiguous")
	}
	active, err = controller.IsActive(ctx, serviceName)
	if err != nil || !active {
		return invalid("service_active_unproven")
	}
	record.State = StateCanaryActive
	if err := impl.writeState(record, true); err != nil {
		return err
	}
	return nil
}

func (s *Store) RollbackToAbsence(ctx context.Context, runtimeID string, controller ServiceController, origin DiagnosticOrigin) error {
	impl, err := s.linuxImpl()
	if err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil || controller == nil || origin == nil || !safeRuntimeID(runtimeID) {
		return invalid("rollback_input_invalid")
	}
	record, exists, err := impl.loadState()
	if err != nil {
		return err
	}
	if !exists || record.RuntimeID != runtimeID {
		return invalid("rollback_state_invalid")
	}
	if record.State == StateAbsent {
		return nil
	}
	if record.State != StatePrepared && record.State != StateRollbackRequired && record.State != StateCanaryActive {
		return invalid("rollback_state_invalid")
	}
	if err := impl.verifyStage(record, true); err != nil {
		return err
	}
	snapshot, err := impl.loadProtectedSnapshot(record)
	if err != nil {
		return err
	}
	if err := controller.Stop(ctx, serviceName); err != nil {
		return invalid("service_stop_failed")
	}
	active, err := controller.IsActive(ctx, serviceName)
	if err != nil || active {
		return invalid("service_inactive_unproven")
	}
	if err := origin.RestoreAndVerify(ctx, snapshot.Request.PublicHost, snapshot.Request.DiagnosticProbeURL); err != nil {
		return invalid("diagnostic_restore_failed")
	}
	paths := impl.paths(runtimeID)
	if err := impl.removeMatching(paths.config, record.ServerConfigSHA256, 0o640, impl.config.serviceUID, impl.config.serviceGID, true); err != nil {
		return err
	}
	if err := impl.removeMatching(paths.unit, record.UnitSHA256, 0o644, impl.config.rootUID, impl.config.rootGID, true); err != nil {
		return err
	}
	if err := controller.Reload(ctx); err != nil {
		return invalid("service_reload_failed")
	}
	record.State = StateAbsent
	return impl.writeState(record, true)
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
	}
}

func (s *storeImpl) preflightPrepare(paths stagePaths, stateExists bool) error {
	for _, path := range []string{paths.runtimeDir, paths.xray, paths.config, paths.evidenceDir, paths.snapshot, paths.direct, paths.cdn, paths.uri, paths.unit} {
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

func (s *storeImpl) prepareDirectories(paths stagePaths, created *[]string) error {
	managed := []struct {
		path string
		mode uint32
		uid  uint32
		gid  uint32
	}{
		{s.config.optRoot, 0o750, s.config.serviceUID, s.config.serviceGID},
		{paths.runtimeRoot, 0o750, s.config.serviceUID, s.config.serviceGID},
		{s.config.runRoot, 0o750, s.config.serviceUID, s.config.serviceGID},
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
		{paths.runtimeDir, 0o750, s.config.serviceUID, s.config.serviceGID},
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
	if raw, err := s.readVerified(paths.xray, 0o550, s.config.serviceUID, s.config.serviceGID, maxXrayBinarySize); err != nil || digest(raw) != record.XraySHA256 {
		return invalid("runtime_verification_failed")
	}
	if raw, err := s.readVerified(paths.snapshot, 0o600, s.config.rootUID, s.config.rootGID, maxJSONBytes); err != nil || digest(raw) != record.SnapshotSHA256 {
		return invalid("snapshot_verification_failed")
	}
	checks := []struct {
		path   string
		mode   uint32
		uid    uint32
		gid    uint32
		limit  int
		digest string
	}{
		{paths.config, 0o640, s.config.serviceUID, s.config.serviceGID, maxJSONBytes, record.ServerConfigSHA256},
		{paths.unit, 0o644, s.config.rootUID, s.config.rootGID, maxJSONBytes, record.UnitSHA256},
	}
	for _, check := range checks {
		raw, err := s.readVerified(check.path, check.mode, check.uid, check.gid, check.limit)
		if rollback && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || digest(raw) != check.digest {
			return invalid("runnable_verification_failed")
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

func (s *storeImpl) atomicNoReplace(label, path string, raw []byte, mode, uid, gid uint32) error {
	return s.atomicWrite(label, path, raw, mode, uid, gid, false)
}

func (s *storeImpl) atomicReplace(label, path string, raw []byte, mode, uid, gid uint32) error {
	return s.atomicWrite(label, path, raw, mode, uid, gid, true)
}

func (s *storeImpl) atomicWrite(label, path string, raw []byte, mode, uid, gid uint32, replace bool) error {
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
	temp, err := randomTempName(base)
	if err != nil {
		return err
	}
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
		if installed && !replace {
			_ = unix.Unlinkat(parent, base, 0)
			_ = unix.Fsync(parent)
		}
	}()
	if err := s.hook(label + ":temp_create"); err != nil {
		return err
	}
	if err := unix.Fchown(fd, int(uid), int(gid)); err != nil {
		return invalid("file_owner_failed")
	}
	if err := unix.Fchmod(fd, mode); err != nil {
		return invalid("file_mode_failed")
	}
	if err := writeAll(fd, raw); err != nil {
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

func (s *storeImpl) cleanupCreated(files []createdFile, dirs []string) {
	for index := len(files) - 1; index >= 0; index-- {
		file := files[index]
		_ = s.removeMatching(file.path, file.digest, file.mode, file.uid, file.gid, true)
	}
	for index := len(dirs) - 1; index >= 0; index-- {
		_ = removeEmptyDirectory(dirs[index])
	}
}

func removeEmptyDirectory(path string) error {
	parent, base, err := openParent(path)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if err := unix.Unlinkat(parent, base, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return unix.Fsync(parent)
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

func randomTempName(base string) (string, error) {
	raw := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", invalid("temp_name_failed")
	}
	return "." + base + ".tmp-" + hex.EncodeToString(raw), nil
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
