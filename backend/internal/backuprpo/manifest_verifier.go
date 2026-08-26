package backuprpo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type ManifestVerifierConfig struct {
	RuntimeDir        string
	Prefix            string
	VerifyScriptPath  string
	GPGPath           string
	PythonPath        string
	GPGHome           string
	SignerFingerprint string
	CommandTimeout    time.Duration
	MaxBundleBytes    int64
	MaxArchiveBytes   int64
	MaxExtractedBytes int64
}

type ManifestVerifierLimits struct {
	MaxBundleBytes    int64
	MaxArchiveBytes   int64
	MaxExtractedBytes int64
}

type manifestVerificationTask interface {
	EncryptedReader() io.ReadSeeker
	ArchiveWriter() io.Writer
	SealArchive() error
	Extract() error
	DirectoryPath() string
	VerificationTarget() (string, []*os.File)
	ResultWriter() io.Writer
	ReadResult() ([]byte, error)
	Abort()
}

type manifestVerificationRuntime interface {
	Prepare(io.Reader, ManifestExpectation, ManifestVerifierLimits) (manifestVerificationTask, error)
}

type ShellManifestVerifier struct {
	config          ManifestVerifierConfig
	runtime         manifestVerificationRuntime
	commands        commandRunner
	decryptLeadFile *os.File
	commandFiles    []*os.File
}

var _ AuthenticatedManifestVerifier = (*ShellManifestVerifier)(nil)

func NewShellManifestVerifier(config ManifestVerifierConfig) (*ShellManifestVerifier, error) {
	if !validManifestVerifierConfig(config) {
		return nil, ErrInvalidConfig
	}
	runtime, err := newSecureManifestVerificationRuntime(config.RuntimeDir)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	return newShellManifestVerifier(config, runtime, osCommandRunner{})
}

func newShellManifestVerifier(
	config ManifestVerifierConfig,
	runtime manifestVerificationRuntime,
	commands commandRunner,
) (*ShellManifestVerifier, error) {
	if !validManifestVerifierConfig(config) || runtime == nil || commands == nil {
		return nil, ErrInvalidConfig
	}
	return &ShellManifestVerifier{config: config, runtime: runtime, commands: commands}, nil
}

func (verifier *ShellManifestVerifier) VerifyAuthenticatedManifest(
	ctx context.Context,
	reader io.Reader,
	expectation ManifestExpectation,
) error {
	if verifier == nil || reader == nil || !validManifestExpectationForPrefix(verifier.config.Prefix, expectation) {
		return ErrManifestInvalid
	}
	limits := ManifestVerifierLimits{
		MaxBundleBytes:    verifier.config.MaxBundleBytes,
		MaxArchiveBytes:   verifier.config.MaxArchiveBytes,
		MaxExtractedBytes: verifier.config.MaxExtractedBytes,
	}
	task, err := verifier.runtime.Prepare(reader, expectation, limits)
	if err != nil || task == nil {
		return ErrManifestInvalid
	}
	defer task.Abort()

	encrypted := task.EncryptedReader()
	archive := task.ArchiveWriter()
	if encrypted == nil || archive == nil {
		return ErrManifestInvalid
	}
	if _, err := encrypted.Seek(0, io.SeekStart); err != nil {
		return ErrManifestInvalid
	}
	if err := verifier.commands.Run(ctx, verifier.decryptSpec(encrypted, archive)); err != nil {
		return ErrManifestInvalid
	}
	if err := task.SealArchive(); err != nil {
		return ErrManifestInvalid
	}
	if err := task.Extract(); err != nil {
		return ErrManifestInvalid
	}
	directory, extraFiles := task.VerificationTarget()
	if directory != "/proc/self/fd/3" || len(extraFiles) != 1 || extraFiles[0] == nil {
		return ErrManifestInvalid
	}
	result := task.ResultWriter()
	if result == nil {
		return ErrManifestInvalid
	}
	if err := verifier.commands.Run(ctx, verifier.verifySpec(directory, extraFiles, result)); err != nil {
		return ErrManifestInvalid
	}
	raw, err := task.ReadResult()
	if err != nil || !exactManifestVerificationResult(raw, expectation) {
		return ErrManifestInvalid
	}
	return nil
}

func (verifier *ShellManifestVerifier) decryptSpec(input io.Reader, output io.Writer) CommandSpec {
	extraFiles := make([]*os.File, 0, 1+len(verifier.commandFiles))
	if verifier.decryptLeadFile != nil || len(verifier.commandFiles) != 0 {
		extraFiles = append(extraFiles, verifier.decryptLeadFile)
		extraFiles = append(extraFiles, verifier.commandFiles...)
	}
	return CommandSpec{
		Path: verifier.config.GPGPath,
		Args: []string{
			"--no-options",
			"--homedir", verifier.config.GPGHome,
			"--batch",
			"--no-tty",
			"--no-auto-key-retrieve",
			"--output", "-",
			"--decrypt",
		},
		Env:        verifier.commandEnvironment(),
		ExtraFiles: extraFiles,
		Stdin:      input,
		Stdout:     output,
		Timeout:    verifier.config.CommandTimeout,
	}
}

func (verifier *ShellManifestVerifier) verifySpec(directory string, extraFiles []*os.File, output io.Writer) CommandSpec {
	commandFiles := append([]*os.File(nil), extraFiles...)
	commandFiles = append(commandFiles, verifier.commandFiles...)
	return CommandSpec{
		Path: verifier.config.PythonPath,
		Args: []string{
			verifier.config.VerifyScriptPath,
			"verify",
			"--directory", directory,
			"--signer", verifier.config.SignerFingerprint,
			"--gpg-home", verifier.config.GPGHome,
			"--gpg-executable", verifier.config.GPGPath,
		},
		Env:        verifier.commandEnvironment(),
		ExtraFiles: commandFiles,
		Stdout:     output,
		Timeout:    verifier.config.CommandTimeout,
	}
}

func (verifier *ShellManifestVerifier) commandEnvironment() []string {
	return []string{
		"PATH=/usr/bin:/bin",
		"LANG=C",
		"GNUPGHOME=" + verifier.config.GPGHome,
	}
}

func validManifestVerifierConfig(config ManifestVerifierConfig) bool {
	if !validPOSIXAbsolute(config.RuntimeDir) || config.RuntimeDir == "/" ||
		!validPOSIXAbsolute(config.VerifyScriptPath) ||
		!validPOSIXAbsolute(config.GPGPath) ||
		!validPOSIXAbsolute(config.PythonPath) ||
		!validPOSIXAbsolute(config.GPGHome) || config.GPGHome == "/" ||
		!canonicalUpperHex(config.SignerFingerprint, 40) ||
		config.CommandTimeout < time.Second || config.CommandTimeout > time.Hour ||
		config.MaxBundleBytes <= 0 || config.MaxBundleBytes > MaxObjectBytes ||
		config.MaxArchiveBytes <= 0 || config.MaxArchiveBytes > MaxObjectBytes ||
		config.MaxExtractedBytes <= 0 || config.MaxExtractedBytes > MaxObjectBytes {
		return false
	}
	_, err := BuildObjectKeyWithPrefix(config.Prefix, 1, 1, strings.Repeat("a", 32))
	return err == nil
}

func validManifestExpectationForPrefix(prefix string, expectation ManifestExpectation) bool {
	return isValidObjectMetadata(expectation.Metadata) &&
		expectation.RestoreEpoch == expectation.Metadata.RestoreEpoch &&
		validBoundKey(prefix, expectation.Key, expectation.Metadata) &&
		validVersionID(expectation.VersionID.String())
}

func exactManifestVerificationResult(raw []byte, expectation ManifestExpectation) bool {
	fields, ok := decodeStrictFlatJSONObject(raw)
	if !ok {
		return false
	}
	exactKeys := []string{
		"attempt_sequence",
		"backup_id",
		"binding_status",
		"captured_generation",
		"dirty_generation",
		"format_version",
		"lease_fence",
		"object_key",
		"restore_epoch",
		"rpo_eligible",
		"status",
	}
	if len(fields) != len(exactKeys) {
		return false
	}
	for _, key := range exactKeys {
		if _, exists := fields[key]; !exists {
			return false
		}
	}
	backupID, backupOK := strictJSONString(fields["backup_id"])
	binding, bindingOK := strictJSONString(fields["binding_status"])
	objectKey, keyOK := strictJSONString(fields["object_key"])
	status, statusOK := strictJSONString(fields["status"])
	attempt, attemptOK := strictJSONInt64(fields["attempt_sequence"])
	captured, capturedOK := strictJSONInt64(fields["captured_generation"])
	dirty, dirtyOK := strictJSONInt64(fields["dirty_generation"])
	format, formatOK := strictJSONInt64(fields["format_version"])
	fence, fenceOK := strictJSONInt64(fields["lease_fence"])
	restoreEpoch, epochOK := strictJSONInt64(fields["restore_epoch"])
	eligible, eligibleOK := strictJSONBool(fields["rpo_eligible"])
	return backupOK && backupID == expectation.Metadata.BackupID &&
		bindingOK && binding == "signed-attempt" &&
		keyOK && objectKey == expectation.Key &&
		statusOK && status == "verified" &&
		attemptOK && attempt == expectation.Metadata.AttemptSequence &&
		capturedOK && captured == expectation.Metadata.CapturedGeneration &&
		dirtyOK && dirty == expectation.Metadata.CapturedGeneration &&
		formatOK && format == 2 &&
		fenceOK && fence == expectation.Metadata.LeaseFence &&
		epochOK && restoreEpoch == expectation.RestoreEpoch &&
		eligibleOK && !eligible
}

func decodeStrictFlatJSONObject(raw []byte) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || len(raw) > int(maximumCommandOutputBytes) || !bytes.Equal(raw, bytes.TrimSpace(raw)) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, false
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		key, stringKey := keyToken.(string)
		if keyErr != nil || !stringKey {
			return nil, false
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	return fields, true
}

func strictJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func strictJSONInt64(raw json.RawMessage) (int64, bool) {
	text := string(raw)
	value, err := strconv.ParseInt(text, 10, 64)
	return value, err == nil && strconv.FormatInt(value, 10) == text
}

func strictJSONBool(raw json.RawMessage) (bool, bool) {
	switch string(raw) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}
