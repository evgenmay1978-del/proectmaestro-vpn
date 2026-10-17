package backuprpo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

const bundleTestBackupID = "abcdef0123456789abcdef0123456789"

type backupOnlyClient struct {
	calls int
	err   error
}

func (client *backupOnlyClient) Backup(_ context.Context, writer io.Writer) error {
	client.calls++
	if client.err != nil {
		return client.err
	}
	_, err := writer.Write([]byte("SQLite format 3\x00image"))
	return err
}

type recordingBackupSource struct {
	order *[]string
	err   error
}

func (source *recordingBackupSource) Capture(_ context.Context, writer io.Writer) error {
	*source.order = append(*source.order, "capture")
	if source.err != nil {
		return source.err
	}
	_, err := writer.Write([]byte("SQLite format 3\x00image"))
	return err
}

type preparedTaskFixture struct {
	order      *[]string
	image      bytes.Buffer
	imagePath  string
	outputPath string
	sealErr    error
	removeErr  error
	aborted    bool
}

func (task *preparedTaskFixture) ImageWriter() io.Writer { return &task.image }
func (task *preparedTaskFixture) ImagePath() string      { return task.imagePath }
func (task *preparedTaskFixture) OutputPath() string     { return task.outputPath }
func (task *preparedTaskFixture) SealImage(maximum int64) error {
	*task.order = append(*task.order, "seal")
	if int64(task.image.Len()) > maximum {
		return ErrUnsafeRuntime
	}
	return task.sealErr
}
func (task *preparedTaskFixture) RemoveImage() error {
	*task.order = append(*task.order, "remove-image")
	return task.removeErr
}
func (task *preparedTaskFixture) Abort() { task.aborted = true }

type runtimeFixture struct {
	order      *[]string
	prepared   *preparedTaskFixture
	pinned     Bundle
	prepareErr error
	pinErr     error
	removeErr  error
	prepare    int
	pin        int
	remove     int
	removedID  string
}

func (runtime *runtimeFixture) Prepare(_ BundleRequest) (preparedTask, error) {
	*runtime.order = append(*runtime.order, "prepare")
	runtime.prepare++
	if runtime.prepareErr != nil {
		return nil, runtime.prepareErr
	}
	return runtime.prepared, nil
}

func (runtime *runtimeFixture) Pin(_ BundleRequest, _ int64) (Bundle, error) {
	*runtime.order = append(*runtime.order, "pin")
	runtime.pin++
	if runtime.pinErr != nil {
		return nil, runtime.pinErr
	}
	return runtime.pinned, nil
}

func (runtime *runtimeFixture) RemoveExisting(backupID string) error {
	*runtime.order = append(*runtime.order, "remove-existing")
	runtime.remove++
	runtime.removedID = backupID
	return runtime.removeErr
}

type commandFixture struct {
	order *[]string
	spec  CommandSpec
	specs []CommandSpec
	err   error
}

func (command *commandFixture) Run(_ context.Context, spec CommandSpec) error {
	*command.order = append(*command.order, "command")
	command.spec = spec
	command.specs = append(command.specs, spec)
	joined := strings.Join(spec.Args, " ")
	if spec.Stdout != nil && strings.Contains(joined, "--list-secret-keys") {
		fingerprint := strings.Repeat("A", 40)
		if strings.Contains(joined, strings.Repeat("B", 40)) {
			fingerprint = strings.Repeat("B", 40)
		}
		_, _ = io.WriteString(spec.Stdout, "sec:u:2048:1:0000000000000000:0:0:::::::\nfpr:::::::::"+fingerprint+":\n")
	}
	if spec.Stdout != nil && strings.Contains(joined, "--list-keys") && !strings.Contains(joined, "--list-secret-keys") {
		_, _ = io.WriteString(spec.Stdout, "pub:u:2048:1:1111111111111111:0:0:::::::\nfpr:::::::::"+strings.Repeat("B", 40)+":\n")
	}
	return command.err
}

func creatorConfigFixture() ShellBundleCreatorConfig {
	return ShellBundleCreatorConfig{
		RuntimeDir:           "/run/maestro-backup",
		Prefix:               "backup-rpo",
		ScriptPath:           "/opt/maestro/backup-rqlite.sh",
		VerifyScriptPath:     "/opt/maestro/verify_backup.py",
		KeysPath:             "/etc/maestro/application-keys.json",
		GPGHome:              "/etc/maestro/gnupg",
		GPGPath:              "/usr/bin/gpg",
		PythonPath:           "/usr/bin/python3",
		SignerFingerprint:    strings.Repeat("A", 40),
		RecipientFingerprint: strings.Repeat("B", 40),
		RepositoryCommitSHA:  strings.Repeat("c", 40),
		BuildRunID:           17,
		CommandTimeout:       45 * time.Second,
		MaxImageBytes:        512 << 20,
		MaxBundleBytes:       768 << 20,
	}
}

func bundleRequestFixture() BundleRequest {
	return BundleRequest{
		RestoreEpoch:       3,
		CapturedGeneration: 7,
		AttemptSequence:    9,
		BackupID:           bundleTestBackupID,
		ObjectKey:          "backup-rpo/g-7/a-9-" + bundleTestBackupID + ".tar.gpg",
		LeaseFence:         4,
	}
}

func creatorFixture(t *testing.T) (*ShellBundleCreator, *preparedTaskFixture, *runtimeFixture, *commandFixture, *[]string) {
	t.Helper()
	order := []string{}
	prepared := &preparedTaskFixture{
		order:      &order,
		imagePath:  "/run/maestro-backup/task-" + bundleTestBackupID + "/control-plane.sqlite3",
		outputPath: "/run/maestro-backup/task-" + bundleTestBackupID + "/backup.bundle",
	}
	runtime := &runtimeFixture{
		order:    &order,
		prepared: prepared,
		pinned:   &byteBundle{Reader: bytes.NewReader([]byte("encrypted-bundle"))},
	}
	command := &commandFixture{order: &order}
	creator, err := newShellBundleCreator(
		creatorConfigFixture(),
		&recordingBackupSource{order: &order},
		runtime,
		command,
	)
	if err != nil {
		t.Fatalf("newShellBundleCreator: %v", err)
	}
	return creator, prepared, runtime, command, &order
}

func TestRQLiteBackupSourceDelegatesExactlyOnceAndRedactsFailure(t *testing.T) {
	client := &backupOnlyClient{}
	source, err := NewRQLiteBackupSource(client)
	if err != nil {
		t.Fatalf("NewRQLiteBackupSource: %v", err)
	}
	var output bytes.Buffer
	if err := source.Capture(context.Background(), &output); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if client.calls != 1 || !bytes.HasPrefix(output.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("calls=%d output=%q", client.calls, output.Bytes())
	}

	client.err = errors.New("https://user:secret@example.invalid")
	err = source.Capture(context.Background(), io.Discard)
	if !errors.Is(err, ErrBackupSource) || strings.Contains(err.Error(), "secret") || client.calls != 2 {
		t.Fatalf("redacted error=%q calls=%d", err, client.calls)
	}
}

func TestRQLiteBackupSourceReadinessUsesBackupEndpointWithoutCapturingImage(t *testing.T) {
	client := &backupOnlyClient{}
	source, err := NewRQLiteBackupSource(client)
	if err != nil {
		t.Fatalf("NewRQLiteBackupSource: %v", err)
	}
	if err := source.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("CheckReadiness: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("backup endpoint calls = %d, want 1", client.calls)
	}

	client.err = errors.New("backup TLS identity unavailable")
	if err := source.CheckReadiness(context.Background()); !errors.Is(err, ErrBackupSource) {
		t.Fatalf("error = %v, want ErrBackupSource", err)
	}
}

func TestShellBundleCreatorReadinessChecksPinnedSignerRecipientAndVerifierWithoutCapture(t *testing.T) {
	creator, _, runtime, command, order := creatorFixture(t)

	if err := creator.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("CheckReadiness: %v", err)
	}
	if runtime.prepare != 0 || runtime.pin != 0 {
		t.Fatalf("readiness touched bundle runtime: prepare=%d pin=%d", runtime.prepare, runtime.pin)
	}
	if got := strings.Join(*order, ","); got != "command,command,command,command" {
		t.Fatalf("order = %s", got)
	}
	if len(command.specs) != 4 {
		t.Fatalf("commands = %d, want 4", len(command.specs))
	}
	want := []string{
		"--list-secret-keys " + strings.Repeat("A", 40),
		"--list-keys " + strings.Repeat("B", 40),
		"--list-secret-keys " + strings.Repeat("B", 40),
		"/opt/maestro/verify_backup.py --help",
	}
	for index, token := range want {
		if got := strings.Join(command.specs[index].Args, " "); !strings.Contains(got, token) {
			t.Fatalf("command %d = %s, want token %s", index, got, token)
		}
	}
}

func TestShellBundleCreatorCapturesBeforeBoundedWorkerAndPinsCandidate(t *testing.T) {
	creator, prepared, runtime, command, order := creatorFixture(t)

	bundle, err := creator.Create(context.Background(), bundleRequestFixture())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer bundle.Close()

	if got := strings.Join(*order, ","); got != "prepare,capture,seal,command,remove-image,pin" {
		t.Fatalf("order=%s", got)
	}
	if prepared.aborted {
		t.Fatal("successful task was aborted")
	}
	if runtime.prepare != 1 || runtime.pin != 1 {
		t.Fatalf("prepare=%d pin=%d", runtime.prepare, runtime.pin)
	}
	wantArgs := strings.Join([]string{
		"--worker",
		"--image", prepared.imagePath,
		"--keys", "/etc/maestro/application-keys.json",
		"--output", prepared.outputPath,
		"--signer", strings.Repeat("A", 40),
		"--recipient", strings.Repeat("B", 40),
		"--manifest-version", "2",
		"--backup-id", bundleTestBackupID,
		"--attempt-sequence", "9",
		"--captured-generation", "7",
		"--restore-epoch", "3",
		"--lease-fence", "4",
		"--object-key", "backup-rpo/g-7/a-9-" + bundleTestBackupID + ".tar.gpg",
		"--verify-script", "/opt/maestro/verify_backup.py",
	}, "\x00")
	if command.spec.Path != "/opt/maestro/backup-rqlite.sh" ||
		strings.Join(command.spec.Args, "\x00") != wantArgs ||
		command.spec.Timeout != 45*time.Second {
		t.Fatalf("command spec=%#v", command.spec)
	}
	joinedEnv := strings.Join(command.spec.Env, "\n")
	for _, exact := range []string{
		"GNUPGHOME=/etc/maestro/gnupg",
		"MAESTRO_BACKUP_COMMIT_SHA=" + strings.Repeat("c", 40),
		"MAESTRO_BACKUP_RUN_ID=17",
	} {
		if !strings.Contains(joinedEnv, exact) {
			t.Fatalf("environment missing %q: %q", exact, joinedEnv)
		}
	}
}

func TestShellBundleCreatorOpenExistingNeverRecapturesOrRunsCommand(t *testing.T) {
	creator, _, runtime, command, order := creatorFixture(t)

	bundle, err := creator.OpenExisting(context.Background(), bundleRequestFixture())
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	defer bundle.Close()

	if got := strings.Join(*order, ","); got != "pin" || runtime.prepare != 0 || command.spec.Path != "" {
		t.Fatalf("order=%s prepare=%d command=%#v", got, runtime.prepare, command.spec)
	}
}

func TestShellBundleCreatorRemoveExistingValidatesDelegatesAndRedacts(t *testing.T) {
	creator, _, runtime, _, order := creatorFixture(t)
	backupID := strings.Repeat("a", 32)

	if err := creator.RemoveExisting(context.Background(), backupID); err != nil {
		t.Fatalf("RemoveExisting: %v", err)
	}
	if runtime.remove != 1 || runtime.removedID != backupID || strings.Join(*order, ",") != "remove-existing" {
		t.Fatalf("remove=%d id=%q order=%v", runtime.remove, runtime.removedID, *order)
	}

	for _, invalid := range []string{"", strings.Repeat("A", 32), "../" + backupID, backupID + "/x"} {
		if err := creator.RemoveExisting(context.Background(), invalid); !errors.Is(err, ErrUnsafeRuntime) {
			t.Fatalf("invalid %q: err=%v", invalid, err)
		}
	}
	if runtime.remove != 1 {
		t.Fatalf("invalid IDs delegated: remove=%d", runtime.remove)
	}

	runtime.removeErr = errors.New("secret path /runtime/task")
	err := creator.RemoveExisting(context.Background(), backupID)
	if !errors.Is(err, ErrUnsafeRuntime) {
		t.Fatalf("runtime error=%v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/runtime") {
		t.Fatalf("runtime error leaked dependency output: %q", err)
	}
}

func TestShellBundleCreatorFailsClosedAndNeverLeaksDependencyOutput(t *testing.T) {
	tests := []struct {
		name string
		wire func(*ShellBundleCreator, *preparedTaskFixture, *commandFixture)
		want error
	}{
		{
			name: "capture",
			wire: func(creator *ShellBundleCreator, _ *preparedTaskFixture, _ *commandFixture) {
				creator.source = &recordingBackupSource{order: creator.runtime.(*runtimeFixture).order, err: errors.New("credential=secret-value")}
			},
			want: ErrBackupSource,
		},
		{
			name: "command",
			wire: func(_ *ShellBundleCreator, _ *preparedTaskFixture, command *commandFixture) {
				command.err = errors.New("stderr secret-value")
			},
			want: ErrCommandFailed,
		},
		{
			name: "seal",
			wire: func(_ *ShellBundleCreator, prepared *preparedTaskFixture, _ *commandFixture) {
				prepared.sealErr = errors.New("path secret-value")
			},
			want: ErrUnsafeRuntime,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			creator, prepared, _, command, _ := creatorFixture(t)
			test.wire(creator, prepared, command)
			bundle, err := creator.Create(context.Background(), bundleRequestFixture())
			if bundle != nil || !errors.Is(err, test.want) || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("bundle=%v error=%q want=%q", bundle, err, test.want)
			}
			if !prepared.aborted {
				t.Fatal("failed task was not aborted")
			}
		})
	}
}

func TestShellBundleCreatorRejectsUnboundRequestAndUnsafeConfig(t *testing.T) {
	config := creatorConfigFixture()
	source := &recordingBackupSource{order: &[]string{}}
	runtime := &runtimeFixture{order: &[]string{}}
	command := &commandFixture{order: &[]string{}}

	badRequest := bundleRequestFixture()
	badRequest.ObjectKey = "foreign/g-7/a-9-" + bundleTestBackupID + ".tar.gpg"
	creator, err := newShellBundleCreator(config, source, runtime, command)
	if err != nil {
		t.Fatalf("valid constructor: %v", err)
	}
	if bundle, createErr := creator.Create(context.Background(), badRequest); bundle != nil || !errors.Is(createErr, ErrUnsafeRuntime) || runtime.prepare != 0 {
		t.Fatalf("bundle=%v error=%v prepare=%d", bundle, createErr, runtime.prepare)
	}

	cases := []func(*ShellBundleCreatorConfig){
		func(value *ShellBundleCreatorConfig) { value.RuntimeDir = "relative" },
		func(value *ShellBundleCreatorConfig) { value.SignerFingerprint = strings.Repeat("a", 40) },
		func(value *ShellBundleCreatorConfig) { value.CommandTimeout = 0 },
		func(value *ShellBundleCreatorConfig) { value.MaxBundleBytes = MaxObjectBytes + 1 },
		func(value *ShellBundleCreatorConfig) { value.RepositoryCommitSHA = "secret-inline" },
	}
	for index, mutate := range cases {
		value := creatorConfigFixture()
		mutate(&value)
		if got, configErr := newShellBundleCreator(value, source, runtime, command); got != nil || !errors.Is(configErr, ErrInvalidConfig) {
			t.Fatalf("case %d creator=%v error=%v", index, got, configErr)
		}
	}
}
