//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/canary"
	"golang.org/x/sys/unix"
)

func TestLinuxMetadataPreservesSpecialModeBits(t *testing.T) {
	for _, mode := range []uint32{0o4600, 0o2600, 0o1600} {
		metadata := metadataFromStat(unix.Stat_t{
			Mode:  unix.S_IFREG | mode,
			Uid:   0,
			Gid:   0,
			Nlink: 1,
			Size:  1,
		})
		if metadata.mode != mode || validProtectedFileMetadata(metadata, 4096) {
			t.Fatalf("special mode accepted or lost: input=%#o metadata=%#v", mode, metadata)
		}
	}
}

func TestLinuxProtectedReaderAndTemporaryExecutable(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root ownership boundary requires root")
	}
	root := t.TempDir()
	input := filepath.Join(root, "request.json")
	want := []byte(`{"schema_version":1}`)
	if err := os.WriteFile(input, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(input, 0, 0); err != nil {
		t.Fatal(err)
	}
	got, err := readRootOwned0600(input, 4096)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("read=%q err=%v", got, err)
	}

	symlink := filepath.Join(root, "request-link.json")
	if err := os.Symlink(input, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readRootOwned0600(symlink, 4096); err == nil {
		t.Fatal("symlink accepted")
	}
	hardlink := filepath.Join(root, "request-hardlink.json")
	if err := os.Link(input, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readRootOwned0600(input, 4096); err == nil {
		t.Fatal("hardlinked file accepted")
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(input, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := readRootOwned0600(input, 4096); err == nil {
		t.Fatal("wrong mode accepted")
	}
	t.Run("setuid fixture", func(t *testing.T) {
		if err := os.Chmod(input, 0o4600); err != nil {
			t.Fatal(err)
		}
		var special unix.Stat_t
		if err := unix.Stat(input, &special); err != nil {
			t.Fatal(err)
		}
		if special.Mode&0o7000 != 0o4000 {
			t.Skipf("filesystem stripped setuid fixture: mode=%#o", special.Mode)
		}
		if _, err := readRootOwned0600(input, 4096); err == nil {
			t.Fatal("setuid protected file accepted")
		}
	})
	if err := os.Chmod(input, 0o600); err != nil || os.Chown(input, 0, 1) != nil {
		t.Fatal("cannot create wrong-group fixture")
	}
	if _, err := readRootOwned0600(input, 4096); err == nil {
		t.Fatal("wrong group accepted")
	}

	binary := []byte("test executable bytes")
	binaryDigest := digestForTest(binary)
	path, cleanup, err := stageTemporaryExecutable(root, binary, binaryDigest)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 || stat.Uid != 0 || stat.Gid != 0 || stat.Nlink != 1 {
		t.Fatalf("unsafe temporary executable metadata: mode=%v stat=%#v", info.Mode(), stat)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, binary) {
		t.Fatalf("temporary binary=%q err=%v", raw, err)
	}
	parent := filepath.Dir(path)
	cleanup()
	cleanup()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary binary remains: %v", err)
	}
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory remains: %v", err)
	}
}

func TestLinuxSystemdInspectionParserAndEffectiveState(t *testing.T) {
	const runtimeID = "r-00112233445566778899aabbccddeeff"
	loaded := loadedSystemdShow(runtimeID)
	inspection, err := parseServiceInspection([]byte(loaded))
	if err != nil || !inspectionMatchesStage(inspection, runtimeID) {
		t.Fatalf("inspection=%#v err=%v", inspection, err)
	}
	prepared := testStage(runtimeID, canary.StatePrepared)
	active := testStage(runtimeID, canary.StateCanaryActive)
	recovery := testStage(runtimeID, canary.StateRollbackRequired)
	if err := reconcileEffectiveState(prepared, inspection, false); err != nil {
		t.Fatal(err)
	}
	if err := reconcileEffectiveState(prepared, inspection, true); err == nil {
		t.Fatal("active PREPARED accepted")
	}
	if err := reconcileEffectiveState(active, inspection, true); err != nil {
		t.Fatal(err)
	}
	if err := reconcileEffectiveState(active, inspection, false); err == nil {
		t.Fatal("inactive CANARY_ACTIVE accepted")
	}
	if err := reconcileEffectiveState(recovery, inspection, false); err != nil {
		t.Fatal(err)
	}
	if err := reconcileEffectiveState(recovery, inspection, true); err != nil {
		t.Fatal(err)
	}
	drift := inspection
	drift.DropInPaths = []string{"/unsafe/drop-in.conf"}
	if err := reconcileEffectiveState(recovery, drift, false); err == nil {
		t.Fatal("drop-in drift accepted")
	}

	missing, err := parseServiceInspection([]byte(missingSystemdShow()))
	if err != nil || !missingInspection(missing) {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}
	for _, malformed := range []string{
		"", loaded + "LoadState=loaded\n", strings.Replace(loaded, "Group=maestro-xray-cdn\n", "", 1),
		strings.Replace(loaded, "ExecStart=", "Unknown=", 1),
		strings.Replace(loaded, "argv[]=", "argv[]=bad argv[]=", 1),
		strings.Replace(loaded, "DropInPaths=", "DropInPaths=/unsafe/drop-in.conf", 1),
	} {
		parsed, parseErr := parseServiceInspection([]byte(malformed))
		if parseErr == nil && inspectionMatchesStage(parsed, runtimeID) {
			t.Fatalf("malformed or drifted inspection accepted: %#v", parsed)
		}
	}
}

func TestLinuxSystemdControllerPinsCommandsAndExitSemantics(t *testing.T) {
	const runtimeID = "r-00112233445566778899aabbccddeeff"
	type call struct {
		path string
		args []string
	}
	var calls []call
	results := []processResult{
		{output: []byte(missingSystemdShow()), exitCode: 4, err: errors.New("not found")},
		{exitCode: 4, err: errors.New("not found")},
		{exitCode: 4, err: errors.New("not found")},
		{output: []byte(loadedSystemdShow(runtimeID)), exitCode: 0},
		{exitCode: 3, err: errors.New("inactive")},
		{exitCode: 2, err: errors.New("unknown")},
	}
	controller := &linuxServiceController{execute: func(_ context.Context, path string, args []string, credential *syscall.Credential, _ int) processResult {
		if credential != nil {
			t.Fatal("systemctl received credentials override")
		}
		calls = append(calls, call{path: path, args: append([]string(nil), args...)})
		result := results[0]
		results = results[1:]
		return result
	}}
	inspection, err := controller.Inspect(context.Background(), exactServiceName)
	if err != nil || !missingInspection(inspection) {
		t.Fatalf("missing inspection=%#v err=%v", inspection, err)
	}
	if active, err := controller.IsActive(context.Background(), exactServiceName); err != nil || active {
		t.Fatalf("proven missing active=%v err=%v", active, err)
	}
	if _, err := controller.IsActive(context.Background(), exactServiceName); err == nil {
		t.Fatal("standalone exit 4 accepted without missing inspection proof")
	}
	inspection, err = controller.Inspect(context.Background(), exactServiceName)
	if err != nil || !inspectionMatchesStage(inspection, runtimeID) {
		t.Fatalf("loaded inspection=%#v err=%v", inspection, err)
	}
	if active, err := controller.IsActive(context.Background(), exactServiceName); err != nil || active {
		t.Fatalf("inactive loaded unit active=%v err=%v", active, err)
	}
	if _, err := controller.IsActive(context.Background(), exactServiceName); err == nil {
		t.Fatal("unknown is-active exit accepted")
	}
	if len(results) != 0 {
		t.Fatalf("unused fake results: %d", len(results))
	}
	if len(calls) != 6 {
		t.Fatalf("calls=%#v", calls)
	}
	for _, current := range calls {
		if current.path != systemctlPath || strings.Contains(strings.Join(current.args, " "), "sh -c") || current.args[len(current.args)-1] != exactServiceName {
			t.Fatalf("unsafe systemctl call: %#v", current)
		}
	}
	if _, err := controller.Inspect(context.Background(), "x-ui.service"); err == nil {
		t.Fatal("unrelated service accepted")
	}
}

func TestLinuxPreparePreservesPersistedStageWhenEffectiveVerificationFails(t *testing.T) {
	binary := []byte("pinned xray")
	archive := testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary})
	stage := testStage("r-00112233445566778899aabbccddeeff", canary.StatePrepared)
	store := &fakePrepareStore{stage: stage}
	controller := &linuxServiceController{execute: func(_ context.Context, path string, args []string, _ *syscall.Credential, _ int) processResult {
		if path != systemctlPath || !reflectStrings(args, []string{"daemon-reload"}) {
			t.Fatalf("unexpected reload command path=%q args=%q", path, args)
		}
		return processResult{exitCode: 1, err: errors.New("reload failed")}
	}}
	operator := &linuxOperator{
		store:      &canary.Store{},
		controller: controller,
		prepare: prepareDependencies{
			readProtected: func(path string, _ int64) ([]byte, error) {
				if path == "request" {
					return testRequestRaw(t), nil
				}
				return archive, nil
			},
			expectedArchiveSHA256: digestForTest(archive),
			expectedBinarySHA256:  digestForTest(binary),
			stageExecutable: func([]byte) (string, func(), error) {
				return "/verified/xray", func() {}, nil
			},
			invoker: &fakeInvoker{out: vlessOutput(validVLESSPair(t, 0x71))},
			newUUID: func() (string, error) {
				return "123e4567-e89b-42d3-a456-426614174000", nil
			},
			newPath: func() (string, error) {
				return "/static/main/video/segment.ts/abcdefghijklmnopqrstuvwxyz012345", nil
			},
			store: store,
		},
	}
	got, err := operator.Prepare(context.Background(), "request", "archive")
	if got != stage || safeReason(err) != "prepare_effective_state_unverified" {
		t.Fatalf("stage=%#v err=%v", got, err)
	}

	store.err = errors.New("state fsync result ambiguous")
	controller.execute = func(_ context.Context, _ string, _ []string, _ *syscall.Credential, _ int) processResult {
		t.Fatal("systemd reached after Store.Prepare ambiguity")
		return processResult{}
	}
	got, err = operator.Prepare(context.Background(), "request", "archive")
	if got != stage || safeReason(err) != "prepare_failed" {
		t.Fatalf("persisted stage=%#v err=%v", got, err)
	}
}

func TestLinuxStopAcceptsOnlyProvenMissingAfterFailedStop(t *testing.T) {
	const runtimeID = "r-00112233445566778899aabbccddeeff"
	tests := []struct {
		name    string
		results []processResult
		wantOK  bool
	}{
		{
			name: "missing and inactive with exact exit four",
			results: []processResult{
				{exitCode: 5, err: errors.New("stop failed")},
				{output: []byte(missingSystemdShow()), exitCode: 4, err: errors.New("not found")},
				{exitCode: 4, err: errors.New("not found")},
				{exitCode: 4, err: errors.New("not found")},
				{exitCode: 4, err: errors.New("not found")},
			},
			wantOK: true,
		},
		{
			name: "loaded unit",
			results: []processResult{
				{exitCode: 5, err: errors.New("stop failed")},
				{output: []byte(loadedSystemdShow(runtimeID)), exitCode: 0},
			},
		},
		{
			name: "missing but wrong inactive exit",
			results: []processResult{
				{exitCode: 5, err: errors.New("stop failed")},
				{output: []byte(missingSystemdShow()), exitCode: 4, err: errors.New("not found")},
				{exitCode: 3, err: errors.New("inactive")},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results := append([]processResult(nil), test.results...)
			controller := &linuxServiceController{execute: func(_ context.Context, path string, args []string, _ *syscall.Credential, _ int) processResult {
				if path != systemctlPath || len(results) == 0 || args[len(args)-1] != exactServiceName {
					t.Fatalf("unexpected systemctl call path=%q args=%q", path, args)
				}
				result := results[0]
				results = results[1:]
				return result
			}}
			err := controller.Stop(context.Background(), exactServiceName)
			if (err == nil) != test.wantOK {
				t.Fatalf("err=%v remaining=%d", err, len(results))
			}
			if test.wantOK {
				if active, activeErr := controller.IsActive(context.Background(), exactServiceName); activeErr != nil || active {
					t.Fatalf("post-stop inactive proof active=%v err=%v", active, activeErr)
				}
				if _, activeErr := controller.IsActive(context.Background(), exactServiceName); activeErr == nil {
					t.Fatal("post-stop missing proof was reusable")
				}
			}
			if len(results) != 0 {
				t.Fatalf("unused results=%d", len(results))
			}
		})
	}
}

func TestLinuxConfigTesterClearsGroupsAndUsesDirectXray(t *testing.T) {
	var gotPath string
	var gotArgs []string
	var gotCredential *syscall.Credential
	controller := &linuxServiceController{execute: func(_ context.Context, path string, args []string, credential *syscall.Credential, _ int) processResult {
		gotPath = path
		gotArgs = append([]string(nil), args...)
		gotCredential = credential
		return processResult{exitCode: 0}
	}}
	tester := linuxConfigTester{controller: controller}
	if err := tester.Test(context.Background(), "/opt/maestro-xray-cdn/runtime/r-test/xray", "/run/maestro-xray-cdn/config.json", 1234, 2345); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/opt/maestro-xray-cdn/runtime/r-test/xray" || !reflectStrings(gotArgs, []string{"run", "-test", "-config", "/run/maestro-xray-cdn/config.json"}) {
		t.Fatalf("path=%q args=%q", gotPath, gotArgs)
	}
	if gotCredential == nil || gotCredential.Uid != 1234 || gotCredential.Gid != 2345 || gotCredential.NoSetGroups || len(gotCredential.Groups) != 0 {
		t.Fatalf("credential=%#v", gotCredential)
	}
}

func TestLinuxCredentialExecutionClearsSupplementaryGroups(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("credential transition requires root")
	}
	result := executeBounded(context.Background(), "/proc/self/exe", []string{"-test.run=^TestLinuxHelperProcess$", "--", "identity"}, sidecarCredential(65534, 65534), 4096)
	if result.err != nil || result.exitCode != 0 || result.overflow {
		t.Fatalf("helper result=%#v", result)
	}
	var identity struct {
		UID    int   `json:"uid"`
		GID    int   `json:"gid"`
		Groups []int `json:"groups"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(result.output), &identity); err != nil {
		t.Fatalf("identity output=%q err=%v", result.output, err)
	}
	if identity.UID != 65534 || identity.GID != 65534 || len(identity.Groups) != 0 {
		t.Fatalf("identity=%#v", identity)
	}
}

func TestLinuxBoundedExecutionCapsOutputAndKillsProcessGroup(t *testing.T) {
	result := executeBounded(context.Background(), "/proc/self/exe", []string{"-test.run=^TestLinuxHelperProcess$", "--", "emit"}, nil, 64)
	if !result.overflow || len(result.output) != 64 {
		t.Fatalf("bounded result=%#v", result)
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	started := time.Now()
	result = executeBounded(ctx, "/proc/self/exe", []string{"-test.run=^TestLinuxHelperProcess$", "--", "group-parent", pidFile}, nil, 4096)
	if result.err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout result=%#v elapsed=%s", result, time.Since(started))
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if processGoneOrZombie(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived process-group cancellation", pid)
}

func processGoneOrZombie(pid int) bool {
	if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
		return true
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	fields := strings.Fields(string(raw))
	return len(fields) >= 3 && fields[2] == "Z"
}

func TestLinuxHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "identity":
		groups, err := os.Getgroups()
		if err != nil {
			os.Exit(91)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"uid": os.Geteuid(), "gid": os.Getegid(), "groups": groups})
		os.Exit(0)
	case "emit":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("o"), 128))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), 128))
		os.Exit(0)
	case "group-parent":
		if separator+2 >= len(os.Args) {
			os.Exit(92)
		}
		command := exec.Command("/proc/self/exe", "-test.run=^TestLinuxHelperProcess$", "--", "group-child")
		if err := command.Start(); err != nil {
			os.Exit(93)
		}
		if err := os.WriteFile(os.Args[separator+2], []byte(fmt.Sprint(command.Process.Pid)), 0o600); err != nil {
			os.Exit(94)
		}
		_ = command.Wait()
		os.Exit(0)
	case "group-child":
		for {
			time.Sleep(time.Second)
		}
	}
}

func loadedSystemdShow(runtimeID string) string {
	executable := filepath.Join("/opt/maestro-xray-cdn/runtime", runtimeID, "xray")
	return "LoadState=loaded\n" +
		"UnitFileState=disabled\n" +
		"FragmentPath=/etc/systemd/system/" + exactServiceName + "\n" +
		"DropInPaths=\n" +
		"User=maestro-xray-cdn\n" +
		"Group=maestro-xray-cdn\n" +
		"ExecStart={ path=" + executable + " ; argv[]=" + executable + " run -config /run/maestro-xray-cdn/config.json ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\n"
}

func missingSystemdShow() string {
	return "LoadState=not-found\nUnitFileState=\nFragmentPath=\nDropInPaths=\nUser=\nGroup=\nExecStart=\n"
}
