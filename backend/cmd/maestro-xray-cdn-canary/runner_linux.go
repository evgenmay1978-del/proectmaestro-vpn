//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/canary"
	"golang.org/x/sys/unix"
)

const (
	systemctlPath        = "/usr/bin/systemctl"
	maximumProcessOutput = 64 << 10
	processWaitDelay     = 250 * time.Millisecond
)

type linuxOperator struct {
	store      *canary.Store
	prepare    prepareDependencies
	controller *linuxServiceController
	client     httpDoer
}

func newPlatformOperator() (commandOperator, error) {
	store, err := canary.NewStore()
	if err != nil {
		return nil, reasonError{"platform_unavailable"}
	}
	controller := newLinuxServiceController()
	tester := linuxConfigTester{controller: controller}
	return &linuxOperator{
		store:      store,
		prepare:    productionPrepareDependencies(store, tester, readRootOwned0600, stageTemporaryXray, linuxBinaryInvoker{}),
		controller: controller,
		client:     newDiagnosticHTTPClient(),
	}, nil
}

func (operator *linuxOperator) Prepare(ctx context.Context, requestPath, archivePath string) (canary.Stage, error) {
	if operator == nil || operator.store == nil || operator.controller == nil {
		return canary.Stage{}, reasonError{"prepare_failed"}
	}
	stage, err := prepareCanary(ctx, operator.prepare, requestPath, archivePath)
	if err != nil {
		if validStage(stage, canary.StatePrepared) {
			return stage, err
		}
		return canary.Stage{}, err
	}
	// Loading the newly written static unit is not activation. It is required so
	// PREPARED can authenticate the effective fragment while proving it inactive.
	if err := operator.controller.Reload(ctx); err != nil {
		return stage, reasonError{"prepare_effective_state_unverified"}
	}
	verified, err := operator.authoritativeStatus(ctx)
	if err != nil || verified != stage || verified.State != canary.StatePrepared {
		return stage, reasonError{"prepare_effective_state_unverified"}
	}
	return verified, nil
}

func (operator *linuxOperator) Activate(ctx context.Context, runtimeID string) (canary.Stage, error) {
	if operator == nil || operator.store == nil || operator.controller == nil || !safeRuntimeID(runtimeID) {
		return canary.Stage{}, reasonError{"activate_failed"}
	}
	if err := operator.store.Activate(ctx, runtimeID, operator.controller); err != nil {
		return canary.Stage{}, reasonError{"activate_failed"}
	}
	stage, err := operator.authoritativeStatus(ctx)
	if err != nil || stage.RuntimeID != runtimeID || stage.State != canary.StateCanaryActive {
		return canary.Stage{}, reasonError{"activate_state_unverified"}
	}
	return stage, nil
}

func (operator *linuxOperator) Rollback(ctx context.Context, runtimeID string) (canary.Stage, error) {
	if operator == nil || operator.store == nil || operator.controller == nil || operator.client == nil || !safeRuntimeID(runtimeID) {
		return canary.Stage{}, reasonError{"rollback_failed"}
	}
	verifier := linuxDiagnosticRestorationVerifier{client: operator.client}
	if err := operator.store.RollbackToAbsence(ctx, runtimeID, operator.controller, verifier); err != nil {
		return canary.Stage{}, reasonError{"rollback_or_external_restore_verification_failed"}
	}
	stage, err := operator.authoritativeStatus(ctx)
	if err != nil || stage.State != canary.StateAbsent {
		return canary.Stage{}, reasonError{"rollback_state_unverified"}
	}
	return stage, nil
}

func (operator *linuxOperator) Status(ctx context.Context) (canary.Stage, error) {
	stage, err := operator.authoritativeStatus(ctx)
	if err != nil {
		return canary.Stage{}, reasonError{"status_failed"}
	}
	return stage, nil
}

func (operator *linuxOperator) authoritativeStatus(ctx context.Context) (canary.Stage, error) {
	if operator == nil || operator.store == nil || operator.controller == nil {
		return canary.Stage{}, reasonError{"status_failed"}
	}
	stage, err := operator.store.Status(ctx, operator.controller)
	if err != nil {
		return canary.Stage{}, reasonError{"status_failed"}
	}
	if stage.State == canary.StateAbsent {
		return stage, nil
	}
	if !validStage(stage, stage.State) {
		return canary.Stage{}, reasonError{"status_failed"}
	}
	inspection, err := operator.controller.Inspect(ctx, exactServiceName)
	if err != nil || !inspectionMatchesStage(inspection, stage.RuntimeID) {
		return canary.Stage{}, reasonError{"service_effective_state_invalid"}
	}
	active, err := operator.controller.IsActive(ctx, exactServiceName)
	if err != nil {
		return canary.Stage{}, reasonError{"service_active_state_unknown"}
	}
	if err := reconcileEffectiveState(stage, inspection, active); err != nil {
		return canary.Stage{}, err
	}
	return stage, nil
}

func reconcileEffectiveState(stage canary.Stage, inspection canary.ServiceInspection, active bool) error {
	if !validStage(stage, stage.State) || !inspectionMatchesStage(inspection, stage.RuntimeID) {
		return reasonError{"service_effective_state_invalid"}
	}
	switch stage.State {
	case canary.StatePrepared:
		if active {
			return reasonError{"prepared_service_active"}
		}
	case canary.StateCanaryActive:
		if !active {
			return reasonError{"active_service_inactive"}
		}
	case canary.StateRollbackRequired:
		// Both active and inactive are recoverable. The durable state still
		// requires rollback, but the exact effective unit must remain authenticated.
	default:
		return reasonError{"lifecycle_state_invalid"}
	}
	return nil
}

func inspectionMatchesStage(inspection canary.ServiceInspection, runtimeID string) bool {
	if !safeRuntimeID(runtimeID) {
		return false
	}
	wantExec := []string{
		filepath.Join("/opt/maestro-xray-cdn/runtime", runtimeID, "xray"),
		"run", "-config", "/run/maestro-xray-cdn/config.json",
	}
	return inspection.LoadState == "loaded" && inspection.UnitFileState == "disabled" &&
		inspection.FragmentPath == "/etc/systemd/system/"+exactServiceName && len(inspection.DropInPaths) == 0 &&
		inspection.User == "maestro-xray-cdn" && inspection.Group == "maestro-xray-cdn" && reflectStrings(inspection.ExecStart, wantExec)
}

type linuxDiagnosticRestorationVerifier struct{ client httpDoer }

func (verifier linuxDiagnosticRestorationVerifier) VerifyRestored(ctx context.Context, diagnosticProbeURL, diagnosticResponseSHA256 string) error {
	if verifier.client == nil {
		return reasonError{"diagnostic_restore_not_verified"}
	}
	return verifyRestoredDiagnosticOrigin(ctx, verifier.client, diagnosticProbeURL, diagnosticResponseSHA256, maxDiagnosticBytes)
}

func readRootOwned0600(path string, maximum int64) ([]byte, error) {
	if !safeAbsolutePath(path) || maximum <= 0 {
		return nil, reasonError{"input_invalid"}
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	})
	if err != nil {
		return nil, reasonError{"input_invalid"}
	}
	file := os.NewFile(uintptr(fd), "protected-input")
	if file == nil {
		_ = unix.Close(fd)
		return nil, reasonError{"input_invalid"}
	}
	defer file.Close()

	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil || !validProtectedFileMetadata(metadataFromStat(before), maximum) {
		return nil, reasonError{"input_invalid"}
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maximum || int64(len(raw)) != before.Size {
		return nil, reasonError{"input_invalid"}
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil || !samePinnedFile(before, after) || !validProtectedFileMetadata(metadataFromStat(after), maximum) {
		return nil, reasonError{"input_invalid"}
	}
	return append([]byte(nil), raw...), nil
}

func metadataFromStat(stat unix.Stat_t) protectedFileMetadata {
	return protectedFileMetadata{
		regular: stat.Mode&unix.S_IFMT == unix.S_IFREG,
		uid:     stat.Uid,
		gid:     stat.Gid,
		mode:    stat.Mode & 0o7777,
		links:   uint64(stat.Nlink),
		size:    stat.Size,
	}
}

func samePinnedFile(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode && left.Uid == right.Uid && left.Gid == right.Gid &&
		left.Nlink == right.Nlink && left.Size == right.Size
}

func stageTemporaryXray(binary []byte) (string, func(), error) {
	return stageTemporaryExecutable("/run", binary, pinnedBinarySHA256)
}

func stageTemporaryExecutable(parent string, binary []byte, expectedSHA256 string) (string, func(), error) {
	if !safeAbsolutePath(parent) || !digestMatches(binary, expectedSHA256) {
		return "", nil, reasonError{"binary_digest_mismatch"}
	}
	directory, err := os.MkdirTemp(parent, ".maestro-xray-cdn-prepare-")
	if err != nil || !safeAbsolutePath(directory) {
		return "", nil, reasonError{"temporary_binary_failed"}
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = os.Remove(filepath.Join(directory, "xray"))
			_ = os.Remove(directory)
		})
	}
	fail := func() (string, func(), error) {
		cleanup()
		return "", nil, reasonError{"temporary_binary_failed"}
	}
	if err := os.Chmod(directory, 0o700); err != nil || os.Chown(directory, 0, 0) != nil {
		return fail()
	}
	path := filepath.Join(directory, "xray")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return fail()
	}
	writeErr := writeExactAndSync(file, binary)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || os.Chmod(path, 0o700) != nil || os.Chown(path, 0, 0) != nil {
		return fail()
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fail()
	}
	opened := os.NewFile(uintptr(fd), "temporary-xray")
	if opened == nil {
		_ = unix.Close(fd)
		return fail()
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(fd, &stat)
	written, readErr := io.ReadAll(io.LimitReader(opened, int64(len(binary))+1))
	closeReadErr := opened.Close()
	if statErr != nil || readErr != nil || closeReadErr != nil || !digestMatches(written, expectedSHA256) ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o700 || stat.Uid != 0 || stat.Gid != 0 || stat.Nlink != 1 || stat.Size != int64(len(binary)) {
		return fail()
	}
	return path, cleanup, nil
}

func writeExactAndSync(file *os.File, raw []byte) error {
	for len(raw) > 0 {
		written, err := file.Write(raw)
		if err != nil || written <= 0 {
			return reasonError{"temporary_binary_failed"}
		}
		raw = raw[written:]
	}
	return file.Sync()
}

type commandExecutor func(context.Context, string, []string, *syscall.Credential, int) processResult

type linuxBinaryInvoker struct{}

func (linuxBinaryInvoker) Invoke(ctx context.Context, path string, args []string, maximum int) ([]byte, error) {
	if !safeAbsolutePath(path) || !reflectStrings(args, []string{"vlessenc"}) || maximum <= 0 || maximum > maximumProcessOutput {
		return nil, reasonError{"vlessenc_failed"}
	}
	result := executeBounded(ctx, path, args, nil, maximum)
	if result.err != nil || result.exitCode != 0 || result.overflow {
		return nil, reasonError{"vlessenc_failed"}
	}
	return result.output, nil
}

type linuxConfigTester struct{ controller *linuxServiceController }

func (tester linuxConfigTester) Inspect(ctx context.Context, service string) (canary.ServiceInspection, error) {
	if tester.controller == nil {
		return canary.ServiceInspection{}, reasonError{"service_inspection_failed"}
	}
	return tester.controller.Inspect(ctx, service)
}
func (tester linuxConfigTester) IsActive(ctx context.Context, service string) (bool, error) {
	if tester.controller == nil {
		return false, reasonError{"service_status_failed"}
	}
	return tester.controller.IsActive(ctx, service)
}
func (tester linuxConfigTester) Test(ctx context.Context, binaryPath, configPath string, uid, gid uint32) error {
	if tester.controller == nil || !safeAbsolutePath(binaryPath) || !safeAbsolutePath(configPath) || uid == 0 || gid == 0 {
		return reasonError{"config_test_failed"}
	}
	credential := sidecarCredential(uid, gid)
	result := tester.controller.run(ctx, binaryPath, []string{"run", "-test", "-config", configPath}, credential, 4096)
	if result.err != nil || result.exitCode != 0 || result.overflow {
		return reasonError{"config_test_failed"}
	}
	return nil
}

func sidecarCredential(uid, gid uint32) *syscall.Credential {
	return &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}, NoSetGroups: false}
}

type linuxServiceController struct {
	mu                   sync.Mutex
	execute              commandExecutor
	missingInspectionFor string
}

func newLinuxServiceController() *linuxServiceController {
	return &linuxServiceController{execute: executeBounded}
}

func (controller *linuxServiceController) run(ctx context.Context, path string, args []string, credential *syscall.Credential, maximum int) processResult {
	if controller == nil || controller.execute == nil {
		return processResult{exitCode: -1, err: reasonError{"process_invalid"}}
	}
	return controller.execute(ctx, path, args, credential, maximum)
}

func (controller *linuxServiceController) Inspect(ctx context.Context, service string) (canary.ServiceInspection, error) {
	args, err := systemctlShowArgs(service)
	if err != nil {
		return canary.ServiceInspection{}, reasonError{"service_inspection_failed"}
	}
	result := controller.run(ctx, systemctlPath, args, nil, 16<<10)
	if result.overflow {
		controller.setMissingProof("")
		return canary.ServiceInspection{}, reasonError{"service_inspection_failed"}
	}
	inspection, parseErr := parseServiceInspection(result.output)
	missing := parseErr == nil && missingInspection(inspection)
	missingExit := result.exitCode == 4 && missing
	if parseErr != nil || (!missingExit && (result.err != nil || result.exitCode != 0)) {
		controller.setMissingProof("")
		return canary.ServiceInspection{}, reasonError{"service_inspection_failed"}
	}
	if missing {
		controller.setMissingProof(service)
	} else {
		controller.setMissingProof("")
	}
	return inspection, nil
}

func (controller *linuxServiceController) IsActive(ctx context.Context, service string) (bool, error) {
	args, err := systemctlServiceArgs("is-active", service)
	if err != nil {
		return false, reasonError{"service_status_failed"}
	}
	result := controller.run(ctx, systemctlPath, args, nil, 4096)
	if result.overflow {
		controller.setMissingProof("")
		return false, reasonError{"service_status_failed"}
	}
	if result.err == nil && result.exitCode == 0 {
		controller.setMissingProof("")
		return true, nil
	}
	if result.exitCode == 3 {
		controller.setMissingProof("")
		return false, nil
	}
	if result.exitCode == 4 && controller.consumeMissingProof(service) {
		return false, nil
	}
	controller.setMissingProof("")
	return false, reasonError{"service_status_failed"}
}

func (controller *linuxServiceController) Reload(ctx context.Context) error {
	controller.setMissingProof("")
	result := controller.run(ctx, systemctlPath, systemctlReloadArgs(), nil, 4096)
	if result.err != nil || result.exitCode != 0 || result.overflow {
		return reasonError{"systemctl_reload_failed"}
	}
	return nil
}

func (controller *linuxServiceController) Start(ctx context.Context, service string) error {
	return controller.runServiceMutation(ctx, "start", service)
}
func (controller *linuxServiceController) Stop(ctx context.Context, service string) error {
	args, err := systemctlServiceArgs("stop", service)
	if err != nil {
		return reasonError{"service_command_failed"}
	}
	controller.setMissingProof("")
	result := controller.run(ctx, systemctlPath, args, nil, 4096)
	if result.err == nil && result.exitCode == 0 && !result.overflow {
		return nil
	}
	if result.overflow {
		return reasonError{"service_command_failed"}
	}
	inspection, inspectErr := controller.Inspect(ctx, service)
	if inspectErr != nil || !missingInspection(inspection) {
		controller.setMissingProof("")
		return reasonError{"service_command_failed"}
	}
	activeArgs, activeArgsErr := systemctlServiceArgs("is-active", service)
	if activeArgsErr != nil {
		controller.setMissingProof("")
		return reasonError{"service_command_failed"}
	}
	activeResult := controller.run(ctx, systemctlPath, activeArgs, nil, 4096)
	if activeResult.overflow || activeResult.exitCode != 4 || !controller.consumeMissingProof(service) {
		controller.setMissingProof("")
		return reasonError{"service_command_failed"}
	}
	// RollbackToAbsence performs one independent IsActive proof immediately
	// after Stop. Authorize exactly that next exit-4 check for the same unit.
	controller.setMissingProof(service)
	return nil
}
func (controller *linuxServiceController) runServiceMutation(ctx context.Context, operation, service string) error {
	args, err := systemctlServiceArgs(operation, service)
	if err != nil {
		return reasonError{"service_command_failed"}
	}
	controller.setMissingProof("")
	result := controller.run(ctx, systemctlPath, args, nil, 4096)
	if result.err != nil || result.exitCode != 0 || result.overflow {
		return reasonError{"service_command_failed"}
	}
	return nil
}

func (controller *linuxServiceController) setMissingProof(service string) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.missingInspectionFor = service
}
func (controller *linuxServiceController) consumeMissingProof(service string) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	matched := controller.missingInspectionFor == service && service == exactServiceName
	controller.missingInspectionFor = ""
	return matched
}

func systemctlShowArgs(service string) ([]string, error) {
	if service != exactServiceName {
		return nil, reasonError{"service_command_invalid"}
	}
	return []string{
		"show", "--no-pager",
		"--property=LoadState", "--property=UnitFileState", "--property=FragmentPath", "--property=DropInPaths",
		"--property=User", "--property=Group", "--property=ExecStart",
		service,
	}, nil
}

func parseServiceInspection(raw []byte) (canary.ServiceInspection, error) {
	if len(raw) == 0 || len(raw) > 16<<10 || !utf8.Valid(raw) || strings.ContainsAny(string(raw), "\x00\r") {
		return canary.ServiceInspection{}, reasonError{"service_inspection_invalid"}
	}
	wanted := map[string]bool{
		"LoadState": false, "UnitFileState": false, "FragmentPath": false, "DropInPaths": false,
		"User": false, "Group": false, "ExecStart": false,
	}
	values := make(map[string]string, len(wanted))
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, found := strings.Cut(line, "=")
		seen, expected := wanted[key]
		if !found || !expected || seen || strings.ContainsAny(value, "\n\x00") {
			return canary.ServiceInspection{}, reasonError{"service_inspection_invalid"}
		}
		wanted[key] = true
		values[key] = value
	}
	for _, seen := range wanted {
		if !seen {
			return canary.ServiceInspection{}, reasonError{"service_inspection_invalid"}
		}
	}
	inspection := canary.ServiceInspection{
		LoadState: values["LoadState"], UnitFileState: values["UnitFileState"], FragmentPath: values["FragmentPath"],
		User: values["User"], Group: values["Group"],
	}
	if values["DropInPaths"] != "" {
		inspection.DropInPaths = strings.Fields(values["DropInPaths"])
		if len(inspection.DropInPaths) == 0 {
			return canary.ServiceInspection{}, reasonError{"service_inspection_invalid"}
		}
	}
	if values["ExecStart"] != "" {
		var err error
		inspection.ExecStart, err = parseSystemdExecStart(values["ExecStart"])
		if err != nil {
			return canary.ServiceInspection{}, err
		}
	}
	if inspection.LoadState == "not-found" && inspection.UnitFileState == "" {
		inspection.UnitFileState = "not-found"
	}
	return inspection, nil
}

func parseSystemdExecStart(raw string) ([]string, error) {
	if strings.Count(raw, "argv[]=") != 1 || strings.Count(raw, "path=") != 1 || !strings.HasPrefix(raw, "{ ") || !strings.HasSuffix(raw, " }") {
		return nil, reasonError{"service_inspection_invalid"}
	}
	pathStart := strings.Index(raw, "path=") + len("path=")
	pathEnd := strings.Index(raw[pathStart:], " ;")
	argvStart := strings.Index(raw, "argv[]=") + len("argv[]=")
	argvEnd := strings.Index(raw[argvStart:], " ;")
	if pathEnd <= 0 || argvEnd <= 0 {
		return nil, reasonError{"service_inspection_invalid"}
	}
	executable := raw[pathStart : pathStart+pathEnd]
	argvText := raw[argvStart : argvStart+argvEnd]
	if strings.ContainsAny(executable+argvText, "'\"\\\r\n\x00") {
		return nil, reasonError{"service_inspection_invalid"}
	}
	args := strings.Fields(argvText)
	if len(args) != 4 || args[0] != executable {
		return nil, reasonError{"service_inspection_invalid"}
	}
	return args, nil
}

func missingInspection(inspection canary.ServiceInspection) bool {
	return inspection.LoadState == "not-found" && inspection.UnitFileState == "not-found" && inspection.FragmentPath == "" &&
		len(inspection.DropInPaths) == 0 && inspection.User == "" && inspection.Group == "" && len(inspection.ExecStart) == 0
}

type processResult struct {
	output   []byte
	exitCode int
	overflow bool
	err      error
}

func executeBounded(parent context.Context, path string, args []string, credential *syscall.Credential, maximum int) processResult {
	if maximum <= 0 || maximum > maximumProcessOutput || !safeAbsolutePath(path) {
		return processResult{exitCode: -1, err: reasonError{"process_invalid"}}
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	command.Stdin = strings.NewReader("")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: credential}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		return command.Process.Kill()
	}
	command.WaitDelay = processWaitDelay
	collector := &boundedCollector{remaining: maximum}
	command.Stdout = collector
	command.Stderr = collector
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
	}
	return processResult{output: collector.bytes(), exitCode: exitCode, overflow: collector.overflowed(), err: err}
}

type boundedCollector struct {
	mu        sync.Mutex
	buffer    []byte
	remaining int
	overflow  bool
}

func (collector *boundedCollector) Write(raw []byte) (int, error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(raw) > collector.remaining {
		collector.buffer = append(collector.buffer, raw[:collector.remaining]...)
		collector.remaining = 0
		collector.overflow = true
		return len(raw), nil
	}
	collector.buffer = append(collector.buffer, raw...)
	collector.remaining -= len(raw)
	return len(raw), nil
}
func (collector *boundedCollector) bytes() []byte {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return append([]byte(nil), collector.buffer...)
}

func (collector *boundedCollector) overflowed() bool {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.overflow
}

func reflectStrings(left, right []string) bool {
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
