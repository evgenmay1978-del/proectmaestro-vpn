package backuprpo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type manifestRuntimeFixture struct {
	task        *manifestTaskFixture
	err         error
	prepare     int
	input       []byte
	expectation ManifestExpectation
	limits      ManifestVerifierLimits
}

func (runtime *manifestRuntimeFixture) Prepare(
	reader io.Reader,
	expectation ManifestExpectation,
	limits ManifestVerifierLimits,
) (manifestVerificationTask, error) {
	runtime.prepare++
	runtime.expectation = expectation
	runtime.limits = limits
	if reader != nil {
		runtime.input, _ = io.ReadAll(reader)
	}
	if runtime.err != nil {
		return nil, runtime.err
	}
	return runtime.task, nil
}

type manifestTaskFixture struct {
	order      *[]string
	input      *bytes.Reader
	archive    bytes.Buffer
	result     bytes.Buffer
	sealErr    error
	extractErr error
	readErr    error
	targetPath string
	targetFile *os.File
	aborted    bool
}

func (task *manifestTaskFixture) EncryptedReader() io.ReadSeeker {
	*task.order = append(*task.order, "encrypted")
	return task.input
}

func (task *manifestTaskFixture) ArchiveWriter() io.Writer {
	*task.order = append(*task.order, "archive")
	return &task.archive
}

func (task *manifestTaskFixture) SealArchive() error {
	*task.order = append(*task.order, "seal")
	return task.sealErr
}

func (task *manifestTaskFixture) Extract() error {
	*task.order = append(*task.order, "extract")
	return task.extractErr
}

func (task *manifestTaskFixture) DirectoryPath() string {
	return "/run/maestro-backup/verify-id/payload"
}

func (task *manifestTaskFixture) VerificationTarget() (string, []*os.File) {
	if task.targetPath == "" || task.targetFile == nil {
		return "", nil
	}
	return task.targetPath, []*os.File{task.targetFile}
}

func (task *manifestTaskFixture) ResultWriter() io.Writer {
	*task.order = append(*task.order, "result")
	return &task.result
}

func (task *manifestTaskFixture) ReadResult() ([]byte, error) {
	*task.order = append(*task.order, "read")
	if task.readErr != nil {
		return nil, task.readErr
	}
	return append([]byte(nil), task.result.Bytes()...), nil
}

func (task *manifestTaskFixture) Abort() {
	*task.order = append(*task.order, "abort")
	task.aborted = true
}

type manifestCommandFixture struct {
	order      *[]string
	resultJSON string
	calls      []CommandSpec
	failPath   string
}

func (commands *manifestCommandFixture) Run(_ context.Context, spec CommandSpec) error {
	commands.calls = append(commands.calls, spec)
	switch spec.Path {
	case "/usr/bin/gpg":
		*commands.order = append(*commands.order, "gpg")
		if commands.failPath == spec.Path {
			return errors.New("secret stderr from gpg")
		}
		if spec.Stdin == nil || spec.Stdout == nil {
			return errors.New("missing pinned streams")
		}
		payload, err := io.ReadAll(spec.Stdin)
		if err != nil || string(payload) != "encrypted-readback" {
			return errors.New("wrong encrypted stream")
		}
		_, err = spec.Stdout.Write([]byte("plain-tar"))
		return err
	case "/usr/bin/python3":
		*commands.order = append(*commands.order, "python")
		if commands.failPath == spec.Path {
			return errors.New("secret stderr from python")
		}
		if spec.Stdin != nil || spec.Stdout == nil {
			return errors.New("wrong verifier streams")
		}
		_, err := io.WriteString(spec.Stdout, commands.resultJSON)
		return err
	default:
		return errors.New("unexpected executable")
	}
}

func manifestVerifierConfigFixture() ManifestVerifierConfig {
	return ManifestVerifierConfig{
		RuntimeDir:        "/run/maestro-backup",
		Prefix:            "backup-rpo",
		VerifyScriptPath:  "/opt/maestro/verify_backup.py",
		GPGPath:           "/usr/bin/gpg",
		PythonPath:        "/usr/bin/python3",
		GPGHome:           "/var/lib/maestro/gnupg",
		SignerFingerprint: strings.Repeat("A", 40),
		CommandTimeout:    30 * time.Second,
		MaxBundleBytes:    128 << 20,
		MaxArchiveBytes:   256 << 20,
		MaxExtractedBytes: 192 << 20,
	}
}

func manifestExpectationFixture(t *testing.T) ManifestExpectation {
	t.Helper()
	version, err := NewVersionID("immutable-version-1")
	if err != nil {
		t.Fatalf("NewVersionID: %v", err)
	}
	return ManifestExpectation{
		Key:       "backup-rpo/g-7/a-9-" + bundleTestBackupID + ".tar.gpg",
		VersionID: version,
		Metadata: ObjectMetadata{
			SHA256:             strings.Repeat("b", 64),
			SizeBytes:          18,
			CapturedGeneration: 7,
			RestoreEpoch:       3,
			AttemptSequence:    9,
			BackupID:           bundleTestBackupID,
			ManifestVersion:    2,
			LeaseFence:         4,
		},
		RestoreEpoch: 3,
	}
}

func manifestResultFixture() string {
	return `{"attempt_sequence":9,"backup_id":"` + bundleTestBackupID + `","binding_status":"signed-attempt","captured_generation":7,"dirty_generation":7,"format_version":2,"lease_fence":4,"object_key":"backup-rpo/g-7/a-9-` + bundleTestBackupID + `.tar.gpg","restore_epoch":3,"rpo_eligible":false,"status":"verified"}`
}

func manifestVerifierFixture(t *testing.T) (*ShellManifestVerifier, *manifestRuntimeFixture, *manifestTaskFixture, *manifestCommandFixture, *[]string) {
	t.Helper()
	order := []string{"prepare"}
	targetFile, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open target directory: %v", err)
	}
	t.Cleanup(func() { _ = targetFile.Close() })
	task := &manifestTaskFixture{
		order:      &order,
		input:      bytes.NewReader([]byte("encrypted-readback")),
		targetPath: "/proc/self/fd/3",
		targetFile: targetFile,
	}
	runtime := &manifestRuntimeFixture{task: task}
	commands := &manifestCommandFixture{order: &order, resultJSON: manifestResultFixture()}
	verifier, err := newShellManifestVerifier(manifestVerifierConfigFixture(), runtime, commands)
	if err != nil {
		t.Fatalf("newShellManifestVerifier: %v", err)
	}
	return verifier, runtime, task, commands, &order
}

func TestShellManifestVerifierUsesPinnedStreamsAndRequiresExactSignedBinding(t *testing.T) {
	verifier, runtime, task, commands, order := manifestVerifierFixture(t)
	expectation := manifestExpectationFixture(t)

	err := verifier.VerifyAuthenticatedManifest(
		context.Background(),
		bytes.NewBufferString("downloaded-ciphertext"),
		expectation,
	)
	if err != nil {
		t.Fatalf("VerifyAuthenticatedManifest: %v", err)
	}
	if runtime.prepare != 1 || string(runtime.input) != "downloaded-ciphertext" || runtime.expectation != expectation {
		t.Fatalf("runtime=%#v", runtime)
	}
	wantLimits := ManifestVerifierLimits{MaxBundleBytes: 128 << 20, MaxArchiveBytes: 256 << 20, MaxExtractedBytes: 192 << 20}
	if runtime.limits != wantLimits {
		t.Fatalf("limits=%#v", runtime.limits)
	}
	if got := strings.Join(*order, ","); got != "prepare,encrypted,archive,gpg,seal,extract,result,python,read,abort" {
		t.Fatalf("order=%s", got)
	}
	if string(task.archive.Bytes()) != "plain-tar" || !task.aborted || len(commands.calls) != 2 {
		t.Fatalf("archive=%q aborted=%v calls=%d", task.archive.Bytes(), task.aborted, len(commands.calls))
	}
	gpg := commands.calls[0]
	if gpg.Path != "/usr/bin/gpg" || strings.Join(gpg.Args, " ") != "--no-options --homedir /var/lib/maestro/gnupg --batch --no-tty --no-auto-key-retrieve --output - --decrypt" ||
		strings.Join(gpg.Env, ",") != "PATH=/usr/bin:/bin,LANG=C,GNUPGHOME=/var/lib/maestro/gnupg" || gpg.Timeout != 30*time.Second {
		t.Fatalf("gpg spec=%#v", gpg)
	}
	python := commands.calls[1]
	if python.Path != "/usr/bin/python3" || strings.Join(python.Args, " ") != "/opt/maestro/verify_backup.py verify --directory /proc/self/fd/3 --signer "+strings.Repeat("A", 40)+" --gpg-home /var/lib/maestro/gnupg --gpg-executable /usr/bin/gpg" ||
		strings.Join(python.Env, ",") != "PATH=/usr/bin:/bin,LANG=C,GNUPGHOME=/var/lib/maestro/gnupg" || python.Timeout != 30*time.Second ||
		len(python.ExtraFiles) != 1 || python.ExtraFiles[0] != task.targetFile {
		t.Fatalf("python spec=%#v", python)
	}
}

func TestPinnedShellManifestVerifierPropagatesCommandFilesToBothStages(t *testing.T) {
	runtimeDir := &os.File{}
	verifyScript := &os.File{}
	gpg := &os.File{}
	python := &os.File{}
	gpgHome := &os.File{}
	payload := &os.File{}
	verifier := &ShellManifestVerifier{
		config:          manifestVerifierConfigFixture(),
		decryptLeadFile: runtimeDir,
		commandFiles:    []*os.File{verifyScript, gpg, python, gpgHome},
	}

	assertFiles := func(name string, got, want []*os.File) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s files = %v, want %v", name, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("%s file[%d] = %p, want %p", name, index, got[index], want[index])
			}
		}
	}

	decrypt := verifier.decryptSpec(bytes.NewReader(nil), io.Discard)
	assertFiles("decrypt", decrypt.ExtraFiles, []*os.File{
		runtimeDir, verifyScript, gpg, python, gpgHome,
	})
	verify := verifier.verifySpec("/proc/self/fd/3", []*os.File{payload}, io.Discard)
	assertFiles("verify", verify.ExtraFiles, []*os.File{
		payload, verifyScript, gpg, python, gpgHome,
	})
}

func TestShellManifestVerifierRejectsEveryResultMismatchDuplicateAndTrailingJSON(t *testing.T) {
	valid := manifestResultFixture()
	tests := map[string]string{
		"backup id":        strings.Replace(valid, bundleTestBackupID, strings.Repeat("c", 32), 1),
		"attempt":          strings.Replace(valid, `"attempt_sequence":9`, `"attempt_sequence":10`, 1),
		"generation":       strings.Replace(valid, `"captured_generation":7`, `"captured_generation":8`, 1),
		"dirty generation": strings.Replace(valid, `"dirty_generation":7`, `"dirty_generation":8`, 1),
		"restore epoch":    strings.Replace(valid, `"restore_epoch":3`, `"restore_epoch":4`, 1),
		"format":           strings.Replace(valid, `"format_version":2`, `"format_version":1`, 1),
		"fence":            strings.Replace(valid, `"lease_fence":4`, `"lease_fence":5`, 1),
		"key":              strings.Replace(valid, `backup-rpo/g-7/`, `foreign/g-7/`, 1),
		"binding":          strings.Replace(valid, `signed-attempt`, `legacy-unbound`, 1),
		"eligible":         strings.Replace(valid, `"rpo_eligible":false`, `"rpo_eligible":true`, 1),
		"status":           strings.Replace(valid, `"status":"verified"`, `"status":"bad"`, 1),
		"extra":            strings.TrimSuffix(valid, "}") + `,"extra":1}`,
		"missing":          strings.Replace(valid, `,"dirty_generation":7`, "", 1),
		"duplicate":        strings.TrimSuffix(valid, "}") + `,"status":"verified"}`,
		"trailing":         valid + `{}`,
	}
	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			verifier, _, task, commands, _ := manifestVerifierFixture(t)
			commands.resultJSON = result
			err := verifier.VerifyAuthenticatedManifest(context.Background(), strings.NewReader("cipher"), manifestExpectationFixture(t))
			if !errors.Is(err, ErrManifestInvalid) || err.Error() != ErrManifestInvalid.Error() || !task.aborted {
				t.Fatalf("error=%q aborted=%v", err, task.aborted)
			}
		})
	}
}

func TestShellManifestVerifierFailsClosedRedactedAcrossRuntimeAndProcesses(t *testing.T) {
	tests := []struct {
		name string
		wire func(*manifestRuntimeFixture, *manifestTaskFixture, *manifestCommandFixture)
	}{
		{"prepare", func(runtime *manifestRuntimeFixture, _ *manifestTaskFixture, _ *manifestCommandFixture) {
			runtime.err = errors.New("secret path")
		}},
		{"gpg", func(_ *manifestRuntimeFixture, _ *manifestTaskFixture, commands *manifestCommandFixture) {
			commands.failPath = "/usr/bin/gpg"
		}},
		{"seal", func(_ *manifestRuntimeFixture, task *manifestTaskFixture, _ *manifestCommandFixture) {
			task.sealErr = errors.New("secret archive")
		}},
		{"extract", func(_ *manifestRuntimeFixture, task *manifestTaskFixture, _ *manifestCommandFixture) {
			task.extractErr = errors.New("secret member")
		}},
		{"python", func(_ *manifestRuntimeFixture, _ *manifestTaskFixture, commands *manifestCommandFixture) {
			commands.failPath = "/usr/bin/python3"
		}},
		{"read", func(_ *manifestRuntimeFixture, task *manifestTaskFixture, _ *manifestCommandFixture) {
			task.readErr = errors.New("secret result")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier, runtime, task, commands, _ := manifestVerifierFixture(t)
			test.wire(runtime, task, commands)
			err := verifier.VerifyAuthenticatedManifest(context.Background(), strings.NewReader("cipher"), manifestExpectationFixture(t))
			if !errors.Is(err, ErrManifestInvalid) || err.Error() != ErrManifestInvalid.Error() {
				t.Fatalf("unredacted error=%q", err)
			}
			if test.name != "prepare" && !task.aborted {
				t.Fatal("prepared task was not aborted")
			}
		})
	}
}

func TestShellManifestVerifierRejectsUnsafeConfigAndUnboundExpectation(t *testing.T) {
	order := []string{}
	runtime := &manifestRuntimeFixture{task: &manifestTaskFixture{order: &order, input: bytes.NewReader(nil)}}
	commands := &manifestCommandFixture{order: &order}
	cases := []func(*ManifestVerifierConfig){
		func(value *ManifestVerifierConfig) { value.RuntimeDir = "relative" },
		func(value *ManifestVerifierConfig) { value.Prefix = "../bad" },
		func(value *ManifestVerifierConfig) { value.VerifyScriptPath = "verify.py" },
		func(value *ManifestVerifierConfig) { value.GPGPath = "/usr/bin/../bin/gpg" },
		func(value *ManifestVerifierConfig) { value.PythonPath = "python3" },
		func(value *ManifestVerifierConfig) { value.GPGHome = "/" },
		func(value *ManifestVerifierConfig) { value.SignerFingerprint = strings.Repeat("a", 40) },
		func(value *ManifestVerifierConfig) { value.CommandTimeout = time.Millisecond },
		func(value *ManifestVerifierConfig) { value.MaxBundleBytes = 0 },
		func(value *ManifestVerifierConfig) { value.MaxArchiveBytes = MaxObjectBytes + 1 },
		func(value *ManifestVerifierConfig) { value.MaxExtractedBytes = 0 },
	}
	for index, mutate := range cases {
		config := manifestVerifierConfigFixture()
		mutate(&config)
		got, err := newShellManifestVerifier(config, runtime, commands)
		if got != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case=%d verifier=%v error=%v", index, got, err)
		}
	}

	verifier, err := newShellManifestVerifier(manifestVerifierConfigFixture(), runtime, commands)
	if err != nil {
		t.Fatalf("valid constructor: %v", err)
	}
	expectation := manifestExpectationFixture(t)
	expectation.Key = "foreign/g-7/a-9-" + bundleTestBackupID + ".tar.gpg"
	if verifyErr := verifier.VerifyAuthenticatedManifest(context.Background(), strings.NewReader("cipher"), expectation); !errors.Is(verifyErr, ErrManifestInvalid) || runtime.prepare != 0 {
		t.Fatalf("error=%v prepare=%d", verifyErr, runtime.prepare)
	}
}
