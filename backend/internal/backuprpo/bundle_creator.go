package backuprpo

import (
	"context"
	"encoding/hex"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type backupClient interface {
	Backup(context.Context, io.Writer) error
}

type BackupSource interface {
	Capture(context.Context, io.Writer) error
}

type RQLiteBackupSource struct {
	client backupClient
}

func NewRQLiteBackupSource(client backupClient) (*RQLiteBackupSource, error) {
	if client == nil {
		return nil, ErrInvalidConfig
	}
	return &RQLiteBackupSource{client: client}, nil
}

func (source *RQLiteBackupSource) Capture(ctx context.Context, writer io.Writer) error {
	if source == nil || source.client == nil || writer == nil {
		return ErrBackupSource
	}
	if err := source.client.Backup(ctx, writer); err != nil {
		return ErrBackupSource
	}
	return nil
}

type ShellBundleCreatorConfig struct {
	RuntimeDir           string
	Prefix               string
	ScriptPath           string
	VerifyScriptPath     string
	KeysPath             string
	GPGHome              string
	GPGPath              string
	PythonPath           string
	SignerFingerprint    string
	RecipientFingerprint string
	RepositoryCommitSHA  string
	BuildRunID           int64
	CommandTimeout       time.Duration
	MaxImageBytes        int64
	MaxBundleBytes       int64
}

type CommandSpec struct {
	Path       string
	Args       []string
	Env        []string
	ExtraFiles []*os.File
	Stdin      io.Reader
	Stdout     io.Writer
	Timeout    time.Duration
}

type preparedTask interface {
	ImageWriter() io.Writer
	ImagePath() string
	OutputPath() string
	SealImage(int64) error
	RemoveImage() error
	Abort()
}

type bundleRuntime interface {
	Prepare(BundleRequest) (preparedTask, error)
	Pin(BundleRequest, int64) (Bundle, error)
}

type commandRunner interface {
	Run(context.Context, CommandSpec) error
}

type ShellBundleCreator struct {
	config       ShellBundleCreatorConfig
	source       BackupSource
	runtime      bundleRuntime
	commands     commandRunner
	commandFiles []*os.File
}

func NewShellBundleCreator(config ShellBundleCreatorConfig, source BackupSource) (*ShellBundleCreator, error) {
	runtime, err := newSecureBundleRuntime(config.RuntimeDir)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return newShellBundleCreator(config, source, runtime, osCommandRunner{})
}

func newShellBundleCreator(
	config ShellBundleCreatorConfig,
	source BackupSource,
	runtime bundleRuntime,
	commands commandRunner,
) (*ShellBundleCreator, error) {
	if !validShellBundleCreatorConfig(config) || source == nil || runtime == nil || commands == nil {
		return nil, ErrInvalidConfig
	}
	return &ShellBundleCreator{config: config, source: source, runtime: runtime, commands: commands}, nil
}

func (creator *ShellBundleCreator) Create(ctx context.Context, request BundleRequest) (Bundle, error) {
	if creator == nil || !validBundleRequestForPrefix(request, creator.config.Prefix) {
		return nil, ErrUnsafeRuntime
	}
	task, err := creator.runtime.Prepare(request)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	success := false
	defer func() {
		if !success {
			task.Abort()
		}
	}()
	if err := creator.source.Capture(ctx, task.ImageWriter()); err != nil {
		return nil, ErrBackupSource
	}
	if err := task.SealImage(creator.config.MaxImageBytes); err != nil {
		return nil, ErrUnsafeRuntime
	}
	if err := creator.commands.Run(ctx, creator.commandSpec(task, request)); err != nil {
		return nil, ErrCommandFailed
	}
	if err := task.RemoveImage(); err != nil {
		return nil, ErrUnsafeRuntime
	}
	bundle, err := creator.runtime.Pin(request, creator.config.MaxBundleBytes)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	success = true
	return bundle, nil
}

func (creator *ShellBundleCreator) OpenExisting(_ context.Context, request BundleRequest) (Bundle, error) {
	if creator == nil || !validBundleRequestForPrefix(request, creator.config.Prefix) {
		return nil, ErrUnsafeRuntime
	}
	bundle, err := creator.runtime.Pin(request, creator.config.MaxBundleBytes)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	return bundle, nil
}

func (creator *ShellBundleCreator) commandSpec(task preparedTask, request BundleRequest) CommandSpec {
	return CommandSpec{
		Path: creator.config.ScriptPath,
		Args: []string{
			"--worker",
			"--image", task.ImagePath(),
			"--keys", creator.config.KeysPath,
			"--output", task.OutputPath(),
			"--signer", creator.config.SignerFingerprint,
			"--recipient", creator.config.RecipientFingerprint,
			"--manifest-version", "2",
			"--backup-id", request.BackupID,
			"--attempt-sequence", strconv.FormatInt(request.AttemptSequence, 10),
			"--captured-generation", strconv.FormatInt(request.CapturedGeneration, 10),
			"--restore-epoch", strconv.FormatInt(request.RestoreEpoch, 10),
			"--lease-fence", strconv.FormatInt(request.LeaseFence, 10),
			"--object-key", request.ObjectKey,
			"--verify-script", creator.config.VerifyScriptPath,
		},
		Env: []string{
			"PATH=/usr/bin:/bin",
			"LANG=C",
			"GNUPGHOME=" + creator.config.GPGHome,
			"MAESTRO_BACKUP_GPG=" + creator.config.GPGPath,
			"MAESTRO_BACKUP_PYTHON=" + creator.config.PythonPath,
			"MAESTRO_BACKUP_COMMIT_SHA=" + creator.config.RepositoryCommitSHA,
			"MAESTRO_BACKUP_RUN_ID=" + strconv.FormatInt(creator.config.BuildRunID, 10),
		},
		ExtraFiles: append([]*os.File(nil), creator.commandFiles...),
		Timeout:    creator.config.CommandTimeout,
	}
}

func validShellBundleCreatorConfig(config ShellBundleCreatorConfig) bool {
	if !validPOSIXAbsolute(config.RuntimeDir) ||
		config.RuntimeDir == "/" ||
		!validPOSIXAbsolute(config.ScriptPath) ||
		!validPOSIXAbsolute(config.VerifyScriptPath) ||
		!validPOSIXAbsolute(config.KeysPath) ||
		!validPOSIXAbsolute(config.GPGHome) ||
		!validPOSIXAbsolute(config.GPGPath) ||
		!validPOSIXAbsolute(config.PythonPath) ||
		!canonicalUpperHex(config.SignerFingerprint, 40) ||
		!canonicalUpperHex(config.RecipientFingerprint, 40) ||
		!canonicalLowerHex(config.RepositoryCommitSHA, 40) ||
		config.BuildRunID <= 0 ||
		config.CommandTimeout < time.Second ||
		config.CommandTimeout > time.Hour ||
		config.MaxImageBytes <= 0 ||
		config.MaxImageBytes > MaxObjectBytes ||
		config.MaxBundleBytes <= 0 ||
		config.MaxBundleBytes > MaxObjectBytes {
		return false
	}
	_, err := BuildObjectKeyWithPrefix(config.Prefix, 1, 1, strings.Repeat("a", 32))
	return err == nil
}

func validBundleRequestForPrefix(request BundleRequest, prefix string) bool {
	if request.RestoreEpoch <= 0 ||
		request.CapturedGeneration <= 0 ||
		request.AttemptSequence <= 0 ||
		request.LeaseFence <= 0 ||
		!canonicalLowerHex(request.BackupID, 32) {
		return false
	}
	expected, err := BuildObjectKeyWithPrefix(
		prefix,
		request.CapturedGeneration,
		request.AttemptSequence,
		request.BackupID,
	)
	return err == nil && request.ObjectKey == expected
}

func validPOSIXAbsolute(value string) bool {
	return value != "" &&
		len(value) <= 4096 &&
		value[0] == '/' &&
		!strings.ContainsRune(value, 0) &&
		path.IsAbs(value) &&
		path.Clean(value) == value
}

func canonicalUpperHex(value string, length int) bool {
	if len(value) != length || strings.ToUpper(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == length
}
