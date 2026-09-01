package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/mlkem"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/canary"
)

type fakeCommandOperator struct {
	prepareStage  canary.Stage
	activateStage canary.Stage
	rollbackStage canary.Stage
	statusStage   canary.Stage
	err           error
	prepareCalls  int
	activateID    string
	rollbackID    string
}

func (operator *fakeCommandOperator) Prepare(context.Context, string, string) (canary.Stage, error) {
	operator.prepareCalls++
	return operator.prepareStage, operator.err
}
func (operator *fakeCommandOperator) Activate(_ context.Context, runtimeID string) (canary.Stage, error) {
	operator.activateID = runtimeID
	return operator.activateStage, operator.err
}
func (operator *fakeCommandOperator) Rollback(_ context.Context, runtimeID string) (canary.Stage, error) {
	operator.rollbackID = runtimeID
	return operator.rollbackStage, operator.err
}
func (operator *fakeCommandOperator) Status(context.Context) (canary.Stage, error) {
	return operator.statusStage, operator.err
}

type testReasonError struct{ code, secret string }

func (err testReasonError) Error() string      { return err.secret }
func (err testReasonError) ReasonCode() string { return err.code }

func TestRunRejectsMalformedCommandsWithoutEcho(t *testing.T) {
	secret := "do-not-echo-secret-path"
	tests := [][]string{
		nil,
		{"prepare"},
		{"prepare", "--request-file", secret},
		{"prepare", "--xray-archive", secret},
		{"prepare", "--request-file", "one", "--request-file", secret, "--xray-archive", "archive"},
		{"prepare", "--request-file", "request", "--xray-archive", "one", "--xray-archive", secret},
		{"prepare", "--request-file", "request", "--xray-archive", "archive", secret},
		{"activate"},
		{"activate", "--runtime-id", "r-00112233445566778899aabbccddeeff", "--runtime-id", secret},
		{"activate", "--runtime-id", secret},
		{"rollback", "--runtime-id", "R-00112233445566778899AABBCCDDEEFF"},
		{"status", secret},
		{"unknown", secret},
	}
	for index, args := range tests {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 || stderr.String() != "canary_failed code=arguments_invalid\n" || strings.Contains(stderr.String(), secret) {
				t.Fatalf("unsafe output stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunDispatchesCommandsAndPrintsReasonCodesOnly(t *testing.T) {
	const runtimeID = "r-00112233445566778899aabbccddeeff"
	requestPath := filepath.Join(t.TempDir(), "request.json")
	archivePath := filepath.Join(t.TempDir(), "Xray-linux-64.zip")
	prepared := testStage(runtimeID, canary.StatePrepared)
	active := testStage(runtimeID, canary.StateCanaryActive)
	absent := canary.Stage{State: canary.StateAbsent}
	tests := []struct {
		args  []string
		stage canary.Stage
		want  string
	}{
		{[]string{"prepare", "--request-file", requestPath, "--xray-archive", archivePath}, prepared, expectedStageOutput("canary_prepare_succeeded", prepared)},
		{[]string{"activate", "--runtime-id", runtimeID}, active, expectedStageOutput("canary_activate_succeeded", active)},
		{[]string{"rollback", "--runtime-id", runtimeID}, absent, "{\"code\":\"canary_rollback_after_external_restore_succeeded\",\"state\":\"ABSENT\"}\n"},
		{[]string{"status"}, absent, "{\"code\":\"canary_status_absent\",\"state\":\"ABSENT\"}\n"},
		{[]string{"status"}, prepared, expectedStageOutput("canary_status_prepared", prepared)},
		{[]string{"status"}, testStage(runtimeID, canary.StateRollbackRequired), expectedStageOutput("canary_status_rollback_required", testStage(runtimeID, canary.StateRollbackRequired))},
		{[]string{"status"}, testStage(runtimeID, canary.StateCanaryActive), expectedStageOutput("canary_status_canary_active", testStage(runtimeID, canary.StateCanaryActive))},
	}
	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			operator := &fakeCommandOperator{prepareStage: test.stage, activateStage: test.stage, rollbackStage: test.stage, statusStage: test.stage}
			withOperatorFactory(t, func() (commandOperator, error) { return operator, nil })
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != exitOK || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, secretMarker := range []string{"vless://", "mlkem", "/static/main", "request.json", "Xray-linux"} {
				if strings.Contains(stdout.String(), secretMarker) {
					t.Fatalf("sensitive field leaked: %q", stdout.String())
				}
			}
		})
	}
}

func TestRunPrintsRecoveryReceiptWhenPreparePersistedBeforeFailure(t *testing.T) {
	const runtimeID = "r-00112233445566778899aabbccddeeff"
	stage := testStage(runtimeID, canary.StatePrepared)
	operator := &fakeCommandOperator{
		prepareStage: stage,
		err:          testReasonError{code: "prepare_effective_state_unverified", secret: "secret-runtime-path"},
	}
	withOperatorFactory(t, func() (commandOperator, error) { return operator, nil })
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"prepare",
		"--request-file", filepath.Join(t.TempDir(), "request.json"),
		"--xray-archive", filepath.Join(t.TempDir(), "Xray-linux-64.zip"),
	}, &stdout, &stderr)
	if code != exitOperation {
		t.Fatalf("exit=%d", code)
	}
	want := expectedStageOutput("canary_prepare_recovery_required", stage)
	if stdout.String() != want || stderr.String() != "canary_failed code=prepare_effective_state_unverified\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "secret-runtime-path") {
		t.Fatal("secret failure detail leaked")
	}
}

func TestRunRedactsErrorsAndFailsClosedOnLifecycleState(t *testing.T) {
	secret := "secret-client-uri-and-path"
	operator := &fakeCommandOperator{err: testReasonError{code: "prepare_failed", secret: secret}}
	withOperatorFactory(t, func() (commandOperator, error) { return operator, nil })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"prepare", "--request-file", filepath.Join(t.TempDir(), "request"), "--xray-archive", filepath.Join(t.TempDir(), "archive")}, &stdout, &stderr); code != exitOperation {
		t.Fatalf("exit=%d", code)
	}
	if stdout.Len() != 0 || stderr.String() != "canary_failed code=prepare_failed\n" || strings.Contains(stderr.String(), secret) {
		t.Fatalf("unsafe output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	operator.err = nil
	operator.prepareStage = testStage("r-00112233445566778899aabbccddeeff", canary.StateCanaryActive)
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"prepare", "--request-file", filepath.Join(t.TempDir(), "request"), "--xray-archive", filepath.Join(t.TempDir(), "archive")}, &stdout, &stderr); code != exitOperation || stderr.String() != "canary_failed code=lifecycle_state_invalid\n" {
		t.Fatalf("prepare accepted wrong state: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	operator.statusStage = canary.Stage{State: "UNKNOWN"}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"status"}, &stdout, &stderr); code != exitOperation || stderr.String() != "canary_failed code=lifecycle_state_invalid\n" {
		t.Fatalf("status accepted unknown state: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExtractPinnedXrayAcceptsOfficialShapeAndRejectsUnsafeZIPs(t *testing.T) {
	binary := []byte("verified-xray-binary")
	valid := testZIP(t,
		zipEntry{name: "xray", mode: 0o755, raw: binary},
		zipEntry{name: "README.md", mode: 0o644, raw: []byte("official readme")},
		zipEntry{name: "geoip.dat", mode: 0o644, raw: []byte("official data")},
	)
	got, err := extractPinnedXray(valid, int64(len(binary)))
	if err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("valid official shape rejected: got=%q err=%v", got, err)
	}

	cases := []struct {
		name string
		raw  []byte
	}{
		{"traversal", testZIP(t, zipEntry{name: "../xray", mode: 0o755, raw: binary})},
		{"absolute", testZIP(t, zipEntry{name: "/xray", mode: 0o755, raw: binary})},
		{"duplicate", testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary}, zipEntry{name: "xray", mode: 0o755, raw: binary})},
		{"case collision", testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary}, zipEntry{name: "XRAY", mode: 0o644, raw: []byte("collision")})},
		{"nested", testZIP(t, zipEntry{name: "bin/xray", mode: 0o755, raw: binary})},
		{"missing", testZIP(t, zipEntry{name: "README.md", mode: 0o644, raw: []byte("readme")})},
		{"non executable xray", testZIP(t, zipEntry{name: "xray", mode: 0o644, raw: binary})},
		{"executable extra", testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary}, zipEntry{name: "helper", mode: 0o755, raw: []byte("extra")})},
		{"special extra", testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary}, zipEntry{name: "link", mode: os.ModeSymlink | 0o777, raw: []byte("xray")})},
		{"oversize binary", testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: append(binary, 'x')})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := extractPinnedXray(test.raw, int64(len(binary))); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestParseVLESSOutputSelectsExactlyOneMLKEM768Block(t *testing.T) {
	pair := validVLESSPair(t, 0x42)
	output := "Xray vlessenc\n" +
		"Authentication: X25519, not Post-Quantum\n" +
		"{\n  \"decryption\": \"x25519-server\",\n  \"encryption\": \"x25519-client\"\n}\n\n" +
		"Authentication: ML-KEM-768, Post-Quantum\n" +
		"{\n  \"decryption\": \"" + pair.server + "\",\n  \"encryption\": \"" + pair.client + "\"\n}\n"
	got, err := parseVLESSOutput([]byte(output))
	if err != nil || got != pair {
		t.Fatalf("pair=%#v err=%v", got, err)
	}
	for _, malformed := range []string{
		"Authentication: X25519, not Post-Quantum\n\"decryption\": \"x\"\n\"encryption\": \"y\"\n",
		output + output,
		"Authentication: ML-KEM-768, Post-Quantum\n\"decryption\": \"" + pair.server + "\"\n",
		"Authentication: ML-KEM-768, Post-Quantum\n\"decryption\": \"" + pair.server + "\"\n\"decryption\": \"" + pair.server + "\"\n\"encryption\": \"" + pair.client + "\"\n",
		"Authentication: ML-KEM-768, Post-Quantum\n\"decryption\" = \"" + pair.server + "\"\n\"encryption\": \"" + pair.client + "\"\n",
		strings.Repeat("x", maxVLESSOutputBytes+1),
	} {
		if _, err := parseVLESSOutput([]byte(malformed)); err == nil {
			t.Fatal("malformed or ambiguous vlessenc output accepted")
		}
	}
}

type fakePrepareStore struct {
	calls     int
	snapshot  canary.Snapshot
	binary    []byte
	artifacts canary.Artifacts
	stage     canary.Stage
	err       error
}

func (store *fakePrepareStore) Prepare(_ context.Context, snapshot canary.Snapshot, binary []byte, artifacts canary.Artifacts, _ canary.ConfigTester) (canary.Stage, error) {
	store.calls++
	store.snapshot = snapshot
	store.binary = append([]byte(nil), binary...)
	store.artifacts = artifacts
	return store.stage, store.err
}

type fakeInvoker struct {
	calls int
	path  string
	args  []string
	out   []byte
	err   error
}

func (invoker *fakeInvoker) Invoke(_ context.Context, path string, args []string, _ int) ([]byte, error) {
	invoker.calls++
	invoker.path = path
	invoker.args = append([]string(nil), args...)
	return append([]byte(nil), invoker.out...), invoker.err
}

func TestPrepareVerifiesDigestsBeforeDirectNoShellInvocation(t *testing.T) {
	binary := []byte("pinned xray")
	archive := testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary})
	requestRaw := testRequestRaw(t)
	pair := validVLESSPair(t, 0x61)
	invoker := &fakeInvoker{out: vlessOutput(pair)}
	store := &fakePrepareStore{stage: testStage("r-00112233445566778899aabbccddeeff", canary.StatePrepared)}
	staged := false
	cleaned := false
	deps := prepareDependencies{
		readProtected: func(path string, _ int64) ([]byte, error) {
			if path == "request" {
				return requestRaw, nil
			}
			return archive, nil
		},
		expectedArchiveSHA256: digestForTest(archive),
		expectedBinarySHA256:  digestForTest(binary),
		stageExecutable: func(raw []byte) (string, func(), error) {
			staged = true
			if !bytes.Equal(raw, binary) {
				t.Fatal("wrong binary staged")
			}
			return "/verified/direct/xray", func() { cleaned = true }, nil
		},
		invoker: invoker,
		newUUID: func() (string, error) { return "123e4567-e89b-42d3-a456-426614174000", nil },
		newPath: func() (string, error) { return "/static/main/video/segment.ts/abcdefghijklmnopqrstuvwxyz012345", nil },
		store:   store,
	}
	stage, err := prepareCanary(context.Background(), deps, "request", "archive")
	if err != nil || stage.State != canary.StatePrepared || store.calls != 1 || !staged || !cleaned {
		t.Fatalf("stage=%#v err=%v store.calls=%d staged=%v cleaned=%v", stage, err, store.calls, staged, cleaned)
	}
	if invoker.calls != 1 || invoker.path != "/verified/direct/xray" || !reflect.DeepEqual(invoker.args, []string{"vlessenc"}) {
		t.Fatalf("invocation path=%q args=%q calls=%d", invoker.path, invoker.args, invoker.calls)
	}
	if store.snapshot.Material.ClientID == "" || len(store.artifacts.ServerConfig()) == 0 || !bytes.Equal(store.binary, binary) {
		t.Fatal("prepared material was not passed to Store.Prepare")
	}

	for _, mutate := range []func(*prepareDependencies){
		func(value *prepareDependencies) { value.expectedArchiveSHA256 = strings.Repeat("0", 64) },
		func(value *prepareDependencies) { value.expectedBinarySHA256 = strings.Repeat("0", 64) },
	} {
		copyDeps := deps
		copyInvoker := new(fakeInvoker)
		copyStore := new(fakePrepareStore)
		copyDeps.invoker = copyInvoker
		copyDeps.store = copyStore
		copyDeps.stageExecutable = func([]byte) (string, func(), error) {
			t.Fatal("process-stage reached before digest rejection")
			return "", nil, nil
		}
		mutate(&copyDeps)
		if _, err := prepareCanary(context.Background(), copyDeps, "request", "archive"); err == nil {
			t.Fatal("digest mismatch accepted")
		}
		if copyInvoker.calls != 0 || copyStore.calls != 0 {
			t.Fatal("process or store reached before digest rejection")
		}
	}
}

func TestPreparePreservesValidStageWhenStoreReturnsError(t *testing.T) {
	binary := []byte("pinned xray")
	archive := testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary})
	stage := testStage("r-00112233445566778899aabbccddeeff", canary.StatePrepared)
	store := &fakePrepareStore{stage: stage, err: errors.New("state fsync result ambiguous")}
	deps := prepareDependencies{
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
		invoker: &fakeInvoker{out: vlessOutput(validVLESSPair(t, 0x72))},
		newUUID: func() (string, error) {
			return "123e4567-e89b-42d3-a456-426614174000", nil
		},
		newPath: func() (string, error) {
			return "/static/main/video/segment.ts/abcdefghijklmnopqrstuvwxyz012345", nil
		},
		store: store,
	}
	got, err := prepareCanary(context.Background(), deps, "request", "archive")
	if got != stage || safeReason(err) != "prepare_failed" {
		t.Fatalf("stage=%#v err=%v", got, err)
	}
}

func TestPrepareRejectsInvalidGeneratedUUIDPathAndMixedPair(t *testing.T) {
	binary := []byte("pinned xray")
	archive := testZIP(t, zipEntry{name: "xray", mode: 0o755, raw: binary})
	pair := validVLESSPair(t, 0x32)
	base := prepareDependencies{
		readProtected: func(path string, _ int64) ([]byte, error) {
			if path == "request" {
				return testRequestRaw(t), nil
			}
			return archive, nil
		},
		expectedArchiveSHA256: digestForTest(archive), expectedBinarySHA256: digestForTest(binary),
		stageExecutable: func([]byte) (string, func(), error) { return "/verified/xray", func() {}, nil },
		invoker:         &fakeInvoker{out: vlessOutput(pair)},
		newUUID:         func() (string, error) { return "123e4567-e89b-42d3-a456-426614174000", nil },
		newPath:         func() (string, error) { return "/static/main/video/segment.ts/abcdefghijklmnopqrstuvwxyz012345", nil },
		store:           &fakePrepareStore{stage: testStage("r-00112233445566778899aabbccddeeff", canary.StatePrepared)},
	}
	cases := []func(*prepareDependencies){
		func(value *prepareDependencies) {
			value.newUUID = func() (string, error) { return "123e4567-e89b-12d3-a456-426614174000", nil }
		},
		func(value *prepareDependencies) {
			value.newUUID = func() (string, error) { return "", errors.New("secret rng error") }
		},
		func(value *prepareDependencies) {
			value.newPath = func() (string, error) { return "/unsafe/../path", nil }
		},
		func(value *prepareDependencies) {
			value.newPath = func() (string, error) { return "", errors.New("secret rng error") }
		},
		func(value *prepareDependencies) {
			other := validVLESSPair(t, 0x33)
			value.invoker = &fakeInvoker{out: vlessOutput(vlessPair{server: pair.server, client: other.client})}
		},
	}
	for _, mutate := range cases {
		deps := base
		store := &fakePrepareStore{stage: testStage("r-00112233445566778899aabbccddeeff", canary.StatePrepared)}
		deps.store = store
		mutate(&deps)
		if _, err := prepareCanary(context.Background(), deps, "request", "archive"); err == nil || store.calls != 0 {
			t.Fatalf("invalid generated material accepted: err=%v calls=%d", err, store.calls)
		}
	}
}

func TestRandomUUIDAndPathUseSuppliedEntropy(t *testing.T) {
	entropy := append(bytes.Repeat([]byte{0x11}, 16), bytes.Repeat([]byte{0x22}, randomPathBytes)...)
	reader := bytes.NewReader(entropy)
	uuid, err := randomUUIDv4(reader)
	if err != nil || uuid != "11111111-1111-4111-9111-111111111111" {
		t.Fatalf("uuid=%q err=%v", uuid, err)
	}
	path, err := randomSecretPath(reader)
	if err != nil || !strings.HasPrefix(path, secretPathPrefix) || !validGeneratedPath(path) {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if _, err := randomUUIDv4(bytes.NewReader(nil)); err == nil {
		t.Fatal("short entropy accepted")
	}
}

func TestProtectedFileMetadataAndExactServiceArguments(t *testing.T) {
	valid := protectedFileMetadata{regular: true, uid: 0, gid: 0, mode: 0o600, links: 1, size: 10}
	if !validProtectedFileMetadata(valid, 20) {
		t.Fatal("valid protected file rejected")
	}
	for _, mutate := range []func(*protectedFileMetadata){
		func(value *protectedFileMetadata) { value.regular = false },
		func(value *protectedFileMetadata) { value.uid = 1000 },
		func(value *protectedFileMetadata) { value.gid = 1000 },
		func(value *protectedFileMetadata) { value.mode = 0o640 },
		func(value *protectedFileMetadata) { value.mode = 0o4600 },
		func(value *protectedFileMetadata) { value.mode = 0o2600 },
		func(value *protectedFileMetadata) { value.mode = 0o1600 },
		func(value *protectedFileMetadata) { value.links = 2 },
		func(value *protectedFileMetadata) { value.size = 0 },
		func(value *protectedFileMetadata) { value.size = 21 },
	} {
		copy := valid
		mutate(&copy)
		if validProtectedFileMetadata(copy, 20) {
			t.Fatalf("unsafe metadata accepted: %#v", copy)
		}
	}
	for _, operation := range []string{"is-active", "start", "stop"} {
		args, err := systemctlServiceArgs(operation, exactServiceName)
		if err != nil || len(args) == 0 || args[len(args)-1] != exactServiceName {
			t.Fatalf("operation=%q args=%q err=%v", operation, args, err)
		}
		if _, err := systemctlServiceArgs(operation, "x-ui.service"); err == nil {
			t.Fatal("unrelated service accepted")
		}
	}
	if got := systemctlReloadArgs(); !reflect.DeepEqual(got, []string{"daemon-reload"}) {
		t.Fatalf("reload args=%q", got)
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (function httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDiagnosticVerifierPinsHTTPSStatusBodyCapHashAndRedirects(t *testing.T) {
	body := []byte("diagnostic origin body")
	wantDigest := digestForTest(body)
	makeDoer := func(status int, raw []byte) httpDoerFunc {
		return func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet || request.URL.String() != "https://cdn.example.invalid/health" {
				t.Fatalf("request=%s %s", request.Method, request.URL)
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		}
	}
	if err := verifyRestoredDiagnosticOrigin(context.Background(), makeDoer(http.StatusOK, body), "https://cdn.example.invalid/health", wantDigest, int64(len(body))); err != nil {
		t.Fatalf("valid diagnostic response rejected: %v", err)
	}
	for _, test := range []struct {
		url, digest string
		status      int
		body        []byte
		limit       int64
	}{
		{"http://cdn.example.invalid/health", wantDigest, http.StatusOK, body, int64(len(body))},
		{"https://cdn.example.invalid/health", wantDigest, http.StatusFound, body, int64(len(body))},
		{"https://cdn.example.invalid/health", strings.Repeat("0", 64), http.StatusOK, body, int64(len(body))},
		{"https://cdn.example.invalid/health", wantDigest, http.StatusOK, append(body, 'x'), int64(len(body))},
	} {
		if err := verifyRestoredDiagnosticOrigin(context.Background(), makeDoer(test.status, test.body), test.url, test.digest, test.limit); err == nil {
			t.Fatalf("unsafe diagnostic response accepted: %#v", test)
		}
	}
	client := newDiagnosticHTTPClient()
	redirect, _ := http.NewRequest(http.MethodGet, "https://other.example.invalid/", nil)
	previous, _ := http.NewRequest(http.MethodGet, "https://cdn.example.invalid/health", nil)
	if client.CheckRedirect == nil || client.CheckRedirect(redirect, []*http.Request{previous}) == nil {
		t.Fatal("redirects are not rejected")
	}
}

func TestUnsupportedPlatformFailsClosed(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("unsupported runner is compiled on non-Linux platforms")
	}
	withOperatorFactory(t, newPlatformOperator)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status"}, &stdout, &stderr); code != exitOperation || stdout.Len() != 0 || stderr.String() != "canary_failed code=unsupported_platform\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func withOperatorFactory(t *testing.T, factory func() (commandOperator, error)) {
	t.Helper()
	previous := commandOperatorFactory
	commandOperatorFactory = factory
	t.Cleanup(func() { commandOperatorFactory = previous })
}

type zipEntry struct {
	name string
	mode os.FileMode
	raw  []byte
}

func testZIP(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func validVLESSPair(t *testing.T, seedByte byte) vlessPair {
	t.Helper()
	seed := bytes.Repeat([]byte{seedByte}, 64)
	key, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		t.Fatal(err)
	}
	return vlessPair{
		server: "mlkem768x25519plus.native.600s." + base64.RawURLEncoding.EncodeToString(seed),
		client: "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(key.EncapsulationKey().Bytes()),
	}
}

func vlessOutput(pair vlessPair) []byte {
	return []byte("Authentication: ML-KEM-768, Post-Quantum\n{\n\"decryption\": \"" + pair.server + "\",\n\"encryption\": \"" + pair.client + "\"\n}\n")
}

func testRequestRaw(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(canary.Request{
		SchemaVersion: 1, PublicHost: "cdn.example.invalid",
		DiagnosticProbeURL:       "https://cdn.example.invalid/health",
		DiagnosticResponseSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func digestForTest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func testStage(runtimeID string, state canary.State) canary.Stage {
	return canary.Stage{
		RuntimeID: runtimeID, State: state,
		SnapshotSHA256: strings.Repeat("a", 64), XraySHA256: strings.Repeat("b", 64),
		ServerConfigSHA256: strings.Repeat("c", 64), UnitSHA256: strings.Repeat("d", 64),
	}
}

func expectedStageOutput(code string, stage canary.Stage) string {
	raw, _ := json.Marshal(map[string]string{
		"code": code, "state": string(stage.State), "runtime_id": stage.RuntimeID,
		"snapshot_sha256": stage.SnapshotSHA256, "xray_sha256": stage.XraySHA256,
		"server_config_sha256": stage.ServerConfigSHA256, "unit_sha256": stage.UnitSHA256,
	})
	// Production uses a struct for deterministic field order; normalize here to that order.
	_ = raw
	return "{\"code\":\"" + code + "\",\"state\":\"" + string(stage.State) + "\",\"runtime_id\":\"" + stage.RuntimeID +
		"\",\"snapshot_sha256\":\"" + stage.SnapshotSHA256 + "\",\"xray_sha256\":\"" + stage.XraySHA256 +
		"\",\"server_config_sha256\":\"" + stage.ServerConfigSHA256 + "\",\"unit_sha256\":\"" + stage.UnitSHA256 + "\"}\n"
}

var _ io.Reader = (*bytes.Reader)(nil)
