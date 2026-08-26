package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	pathpkg "path"
	"strconv"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/backuprpo"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	configVersion        = 1
	credentialVersion    = 1
	capabilityVersion    = 1
	maxConfigBytes       = int64(64 << 10)
	maxCredentialBytes   = int64(16 << 10)
	maxCapabilityBytes   = int64(16 << 10)
	pinnedYandexEndpoint = "https://storage.yandexcloud.net"
	pinnedYandexRegion   = "ru-central1"
)

var (
	errConfig        = errors.New("backup-worker:config")
	errUnsafeRuntime = errors.New("backup-worker:unsafe-runtime")
	errOperational   = errors.New("backup-worker:operational")
)

type workerConfig struct {
	Version int `json:"version"`

	RQLiteEndpoints       []string `json:"rqlite_endpoints"`
	RQLiteCredentialsFile string   `json:"rqlite_credentials_file"`
	RQLiteCAFile          string   `json:"rqlite_ca_file"`
	RQLiteCertFile        string   `json:"rqlite_cert_file"`
	RQLiteKeyFile         string   `json:"rqlite_key_file"`

	YandexEndpoint        string `json:"yandex_endpoint"`
	YandexRegion          string `json:"yandex_region"`
	YandexBucket          string `json:"yandex_bucket"`
	YandexPrefix          string `json:"yandex_prefix"`
	YandexCredentialsFile string `json:"yandex_credentials_file"`

	RuntimeDir       string `json:"runtime_dir"`
	BackupScriptPath string `json:"backup_script_path"`
	VerifyScriptPath string `json:"verify_script_path"`
	KeysPath         string `json:"keys_path"`
	GPGPath          string `json:"gpg_path"`
	PythonPath       string `json:"python_path"`
	GPGHome          string `json:"gpg_home"`

	SignerFingerprint    string `json:"signer_fingerprint"`
	RecipientFingerprint string `json:"recipient_fingerprint"`
	RepositoryCommitSHA  string `json:"repository_commit_sha"`
	BuildRunID           int64  `json:"build_run_id"`

	CapabilityEvidenceFile string `json:"capability_evidence_file"`

	LeaseTTLSeconds       int64 `json:"lease_ttl_seconds"`
	CapabilityTTLSeconds  int64 `json:"capability_ttl_seconds"`
	DeadlineSeconds       int64 `json:"deadline_seconds"`
	CommandTimeoutSeconds int64 `json:"command_timeout_seconds"`
	MaxTransitions        int   `json:"max_transitions"`

	MaxResponseBytes  int64 `json:"max_response_bytes"`
	MaxBackupBytes    int64 `json:"max_backup_bytes"`
	MaxImageBytes     int64 `json:"max_image_bytes"`
	MaxBundleBytes    int64 `json:"max_bundle_bytes"`
	MaxArchiveBytes   int64 `json:"max_archive_bytes"`
	MaxExtractedBytes int64 `json:"max_extracted_bytes"`
}

type rqliteCredentials struct {
	Version  int    `json:"version"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type yandexCredentials struct {
	Version         int    `json:"version"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

type capabilityEvidence struct {
	Version    int    `json:"version"`
	Generation int64  `json:"generation"`
	SHA256     string `json:"sha256"`
}

type secureFileClass uint8

const (
	secureSecretFile secureFileClass = iota + 1
	securePublicFile
	secureExecutableFile
)

type oneShotWorker interface {
	Run(context.Context) backuprpo.Result
}

type runtimeDependencies struct {
	load  func(string) (workerConfig, error)
	build func(context.Context, workerConfig) (oneShotWorker, error)
}

type cryptoIdentitySource struct{}

func (cryptoIdentitySource) NewID() (string, error) {
	return randomHex(16)
}

func main() {
	dependencies := runtimeDependencies{
		load:  loadWorkerConfig,
		build: buildProductionWorker,
	}
	os.Exit(execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, dependencies))
}

func execute(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies runtimeDependencies,
) int {
	if len(args) != 2 || args[0] != "--config" || !validPOSIXPath(args[1], false) ||
		dependencies.load == nil || dependencies.build == nil {
		writeFixed(stderr, "backup-worker:config\n")
		return 2
	}
	config, err := dependencies.load(args[1])
	if err != nil {
		if errors.Is(err, errUnsafeRuntime) {
			writeFixed(stderr, "backup-worker:unsafe-runtime\n")
			return 3
		}
		writeFixed(stderr, "backup-worker:config\n")
		return 2
	}
	worker, err := dependencies.build(ctx, config)
	if err != nil || worker == nil {
		switch {
		case errors.Is(err, errConfig):
			writeFixed(stderr, "backup-worker:config\n")
			return 2
		case errors.Is(err, errUnsafeRuntime):
			writeFixed(stderr, "backup-worker:unsafe-runtime\n")
			return 3
		default:
			writeFixed(stderr, "backup-worker:operational\n")
			return 1
		}
	}
	result := worker.Run(ctx)
	switch result.Code {
	case backuprpo.ResultVerified, backuprpo.ResultNoop:
		writeFixed(stdout, "backup-worker:ok\n")
		return 0
	case backuprpo.ResultUnsafeRuntime:
		writeFixed(stderr, "backup-worker:unsafe-runtime\n")
		return 3
	default:
		writeFixed(stderr, "backup-worker:operational\n")
		return 1
	}
}

func writeFixed(writer io.Writer, message string) {
	if writer != nil {
		_, _ = io.WriteString(writer, message)
	}
}

func loadWorkerConfig(configPath string) (workerConfig, error) {
	if !validPOSIXPath(configPath, false) {
		return workerConfig{}, errConfig
	}
	raw, err := readSecureFile(configPath, secureSecretFile, maxConfigBytes)
	if err != nil {
		return workerConfig{}, errUnsafeRuntime
	}
	config, err := decodeWorkerConfig(bytes.NewReader(raw))
	if err != nil {
		return workerConfig{}, errConfig
	}
	return config, nil
}

func decodeWorkerConfig(reader io.Reader) (workerConfig, error) {
	var config workerConfig
	if err := decodeStrictJSON(reader, maxConfigBytes, &config); err != nil {
		return workerConfig{}, errConfig
	}
	if err := validateWorkerConfig(config); err != nil {
		return workerConfig{}, errConfig
	}
	return config, nil
}

func decodeStrictJSON(reader io.Reader, limit int64, destination any) error {
	if reader == nil || destination == nil || limit <= 0 {
		return errConfig
	}
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	raw, err := io.ReadAll(limited)
	if err != nil || int64(len(raw)) > limit || len(raw) == 0 {
		return errConfig
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return errConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errConfig
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errConfig
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return errConfig
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errConfig
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errConfig
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return errConfig
			}
			key, ok := keyToken.(string)
			if !ok {
				return errConfig
			}
			if _, duplicate := seen[key]; duplicate {
				return errConfig
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errConfig
		}
		return nil
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errConfig
		}
		return nil
	default:
		return errConfig
	}
}

func validateWorkerConfig(config workerConfig) error {
	if config.Version != configVersion ||
		!validRQLiteEndpoints(config.RQLiteEndpoints) ||
		config.YandexEndpoint != pinnedYandexEndpoint ||
		config.YandexRegion != pinnedYandexRegion ||
		!validBucket(config.YandexBucket) ||
		!validObjectPrefix(config.YandexPrefix) ||
		!validUpperHex(config.SignerFingerprint, 40) ||
		!validUpperHex(config.RecipientFingerprint, 40) ||
		!validLowerHex(config.RepositoryCommitSHA, 40) ||
		config.BuildRunID <= 0 ||
		!validSeconds(config.LeaseTTLSeconds) ||
		!validSeconds(config.CapabilityTTLSeconds) ||
		config.CapabilityTTLSeconds < config.LeaseTTLSeconds ||
		!validSeconds(config.DeadlineSeconds) ||
		!validSeconds(config.CommandTimeoutSeconds) ||
		config.CommandTimeoutSeconds > config.DeadlineSeconds ||
		config.DeadlineSeconds >= config.LeaseTTLSeconds ||
		config.MaxTransitions < 1 || config.MaxTransitions > 64 ||
		!validLimits(config) {
		return errConfig
	}
	filePaths := []string{
		config.RQLiteCredentialsFile,
		config.RQLiteCAFile,
		config.RQLiteCertFile,
		config.RQLiteKeyFile,
		config.YandexCredentialsFile,
		config.BackupScriptPath,
		config.VerifyScriptPath,
		config.KeysPath,
		config.GPGPath,
		config.PythonPath,
		config.CapabilityEvidenceFile,
	}
	for _, value := range filePaths {
		if !validPOSIXPath(value, false) {
			return errConfig
		}
	}
	if !validPOSIXPath(config.RuntimeDir, false) || !validPOSIXPath(config.GPGHome, false) {
		return errConfig
	}
	return nil
}

func validRQLiteEndpoints(endpoints []string) bool {
	if len(endpoints) < 1 || len(endpoints) > 9 {
		return false
	}
	seen := make(map[string]struct{}, len(endpoints))
	for _, raw := range endpoints {
		if raw == "" || strings.TrimSpace(raw) != raw {
			return false
		}
		parsed, err := url.ParseRequestURI(raw)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
			parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" ||
			parsed.Fragment != "" || parsed.Host == "" {
			return false
		}
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
			return false
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
		normalized := parsed.String()
		if _, duplicate := seen[normalized]; duplicate {
			return false
		}
		seen[normalized] = struct{}{}
	}
	return true
}

func validBucket(value string) bool {
	if len(value) < 3 || len(value) > 63 || net.ParseIP(value) != nil ||
		!asciiLowerDigit(value[0]) || !asciiLowerDigit(value[len(value)-1]) ||
		strings.Contains(value, "..") || strings.Contains(value, ".-") || strings.Contains(value, "-.") {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '.') {
			return false
		}
	}
	return true
}

func asciiLowerDigit(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9')
}

func validObjectPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > 512 || strings.TrimSpace(prefix) != prefix ||
		strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") ||
		strings.Contains(prefix, "//") || strings.Contains(prefix, "..") || strings.ContainsRune(prefix, 0) {
		return false
	}
	key, err := backuprpo.BuildObjectKeyWithPrefix(prefix, 1, 1, strings.Repeat("a", 32))
	return err == nil && strings.HasPrefix(key, prefix+"/")
}

func validPOSIXPath(value string, allowRoot bool) bool {
	if value == "" || len(value) > 4096 || value[0] != '/' || strings.ContainsRune(value, 0) ||
		!pathpkg.IsAbs(value) || pathpkg.Clean(value) != value {
		return false
	}
	return allowRoot || value != "/"
}

func validUpperHex(value string, length int) bool {
	if len(value) != length || strings.ToUpper(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == length
}

func validLowerHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == length
}

func validSeconds(value int64) bool {
	return value >= 1 && value <= int64(time.Hour/time.Second)
}

func validLimits(config workerConfig) bool {
	limits := []int64{
		config.MaxResponseBytes,
		config.MaxBackupBytes,
		config.MaxImageBytes,
		config.MaxBundleBytes,
		config.MaxArchiveBytes,
		config.MaxExtractedBytes,
	}
	for _, value := range limits {
		if value <= 0 || value > backuprpo.MaxObjectBytes {
			return false
		}
	}
	return config.MaxBackupBytes <= config.MaxImageBytes &&
		config.MaxBundleBytes <= config.MaxArchiveBytes &&
		config.MaxArchiveBytes <= config.MaxExtractedBytes
}

type pinnedWorkerInputs struct {
	runtimeDir   *os.File
	gpgHome      *os.File
	rqliteCA     *os.File
	rqliteCert   *os.File
	rqliteKey    *os.File
	backupScript *os.File
	verifyScript *os.File
	keys         *os.File
	gpg          *os.File
	python       *os.File
}

func (inputs *pinnedWorkerInputs) Close() {
	if inputs == nil {
		return
	}
	for _, file := range []*os.File{
		inputs.runtimeDir,
		inputs.gpgHome,
		inputs.rqliteCA,
		inputs.rqliteCert,
		inputs.rqliteKey,
		inputs.backupScript,
		inputs.verifyScript,
		inputs.keys,
		inputs.gpg,
		inputs.python,
	} {
		if file != nil {
			_ = file.Close()
		}
	}
}

type pinnedOneShotWorker struct {
	worker oneShotWorker
	inputs *pinnedWorkerInputs
}

func (worker *pinnedOneShotWorker) Run(ctx context.Context) backuprpo.Result {
	defer worker.inputs.Close()
	return worker.worker.Run(ctx)
}

func pinWorkerInputs(config workerConfig) (*pinnedWorkerInputs, error) {
	inputs := &pinnedWorkerInputs{}
	var err error
	inputs.runtimeDir, err = openPinnedSecureDirectory(config.RuntimeDir)
	if err != nil {
		inputs.Close()
		return nil, errUnsafeRuntime
	}
	inputs.gpgHome, err = openPinnedSecureDirectory(config.GPGHome)
	if err != nil {
		inputs.Close()
		return nil, errUnsafeRuntime
	}
	files := []struct {
		path   string
		class  secureFileClass
		target **os.File
	}{
		{config.RQLiteCAFile, securePublicFile, &inputs.rqliteCA},
		{config.RQLiteCertFile, securePublicFile, &inputs.rqliteCert},
		{config.RQLiteKeyFile, secureSecretFile, &inputs.rqliteKey},
		{config.BackupScriptPath, secureExecutableFile, &inputs.backupScript},
		{config.VerifyScriptPath, securePublicFile, &inputs.verifyScript},
		{config.KeysPath, secureSecretFile, &inputs.keys},
		{config.GPGPath, secureExecutableFile, &inputs.gpg},
		{config.PythonPath, secureExecutableFile, &inputs.python},
	}
	for _, item := range files {
		*item.target, err = openPinnedSecureFile(item.path, item.class)
		if err != nil {
			inputs.Close()
			return nil, errUnsafeRuntime
		}
	}
	return inputs, nil
}

func buildProductionWorker(ctx context.Context, config workerConfig) (oneShotWorker, error) {
	if err := validateWorkerConfig(config); err != nil {
		return nil, errConfig
	}
	inputs, err := pinWorkerInputs(config)
	if err != nil {
		return nil, errUnsafeRuntime
	}
	keepInputs := false
	defer func() {
		if !keepInputs {
			inputs.Close()
		}
	}()

	rqliteAuth, err := loadRQLiteCredentials(config.RQLiteCredentialsFile)
	if err != nil {
		return nil, err
	}
	yandexAuth, err := loadYandexCredentials(config.YandexCredentialsFile)
	if err != nil {
		return nil, err
	}
	capability, err := loadCapabilityEvidence(config.CapabilityEvidenceFile)
	if err != nil {
		return nil, err
	}
	client, err := rqlite.New(rqlite.Config{
		Endpoints:        append([]string(nil), config.RQLiteEndpoints...),
		Username:         rqliteAuth.Username,
		Password:         rqliteAuth.Password,
		CAFile:           pinnedSecurePath(inputs.rqliteCA),
		CertFile:         pinnedSecurePath(inputs.rqliteCert),
		KeyFile:          pinnedSecurePath(inputs.rqliteKey),
		Timeout:          time.Duration(config.DeadlineSeconds) * time.Second,
		MaxResponseBytes: config.MaxResponseBytes,
		MaxBackupBytes:   config.MaxBackupBytes,
	})
	if err != nil {
		return nil, errConfig
	}
	source, err := backuprpo.NewRQLiteBackupSource(client)
	if err != nil {
		return nil, errConfig
	}
	verifier, err := backuprpo.NewPinnedShellManifestVerifier(
		backuprpo.ManifestVerifierConfig{
			RuntimeDir:        config.RuntimeDir,
			Prefix:            config.YandexPrefix,
			VerifyScriptPath:  config.VerifyScriptPath,
			GPGPath:           config.GPGPath,
			PythonPath:        config.PythonPath,
			GPGHome:           config.GPGHome,
			SignerFingerprint: config.SignerFingerprint,
			CommandTimeout:    time.Duration(config.CommandTimeoutSeconds) * time.Second,
			MaxBundleBytes:    config.MaxBundleBytes,
			MaxArchiveBytes:   config.MaxArchiveBytes,
			MaxExtractedBytes: config.MaxExtractedBytes,
		},
		backuprpo.PinnedManifestInputs{
			RuntimeDir:   inputs.runtimeDir,
			VerifyScript: inputs.verifyScript,
			GPG:          inputs.gpg,
			Python:       inputs.python,
			GPGHome:      inputs.gpgHome,
		},
	)
	if err != nil {
		return nil, errUnsafeRuntime
	}
	objects, err := backuprpo.NewYandexS3(ctx, backuprpo.YandexS3Config{
		Endpoint:        config.YandexEndpoint,
		Region:          config.YandexRegion,
		Bucket:          config.YandexBucket,
		Prefix:          config.YandexPrefix,
		AccessKeyID:     yandexAuth.AccessKeyID,
		SecretAccessKey: yandexAuth.SecretAccessKey,
	}, verifier)
	if err != nil {
		return nil, errOperational
	}
	bundles, err := backuprpo.NewPinnedShellBundleCreator(
		backuprpo.ShellBundleCreatorConfig{
			RuntimeDir:           config.RuntimeDir,
			Prefix:               config.YandexPrefix,
			ScriptPath:           config.BackupScriptPath,
			VerifyScriptPath:     config.VerifyScriptPath,
			KeysPath:             config.KeysPath,
			GPGHome:              config.GPGHome,
			GPGPath:              config.GPGPath,
			PythonPath:           config.PythonPath,
			SignerFingerprint:    config.SignerFingerprint,
			RecipientFingerprint: config.RecipientFingerprint,
			RepositoryCommitSHA:  config.RepositoryCommitSHA,
			BuildRunID:           config.BuildRunID,
			CommandTimeout:       time.Duration(config.CommandTimeoutSeconds) * time.Second,
			MaxImageBytes:        config.MaxImageBytes,
			MaxBundleBytes:       config.MaxBundleBytes,
		},
		source,
		backuprpo.PinnedBundleInputs{
			RuntimeDir:   inputs.runtimeDir,
			Script:       inputs.backupScript,
			VerifyScript: inputs.verifyScript,
			Keys:         inputs.keys,
			GPGHome:      inputs.gpgHome,
			GPG:          inputs.gpg,
			Python:       inputs.python,
		},
	)
	if err != nil {
		return nil, errUnsafeRuntime
	}
	holderID, err := randomHex(16)
	if err != nil {
		return nil, errUnsafeRuntime
	}
	runner := &backuprpo.Runner{
		Store:   controlplane.NewBackupRPOStore(client),
		Objects: objects,
		Bundles: bundles,
		IDs:     cryptoIdentitySource{},
		Now:     time.Now,
		Config: backuprpo.RunnerConfig{
			HolderID:                 "backup-worker-" + holderID,
			Prefix:                   config.YandexPrefix,
			LeaseTTL:                 time.Duration(config.LeaseTTLSeconds) * time.Second,
			CapabilityTTL:            time.Duration(config.CapabilityTTLSeconds) * time.Second,
			Deadline:                 time.Duration(config.DeadlineSeconds) * time.Second,
			MaxTransitions:           config.MaxTransitions,
			CapabilityGeneration:     capability.Generation,
			CapabilityEvidenceSHA256: capability.SHA256,
			MaxBundleBytes:           config.MaxBundleBytes,
		},
	}
	keepInputs = true
	return &pinnedOneShotWorker{worker: runner, inputs: inputs}, nil
}

func verifyRuntimePaths(config workerConfig) error {
	inputs, err := pinWorkerInputs(config)
	if inputs != nil {
		inputs.Close()
	}
	return err
}

func loadRQLiteCredentials(filePath string) (rqliteCredentials, error) {
	raw, err := readSecureFile(filePath, secureSecretFile, maxCredentialBytes)
	if err != nil {
		return rqliteCredentials{}, errUnsafeRuntime
	}
	var credentials rqliteCredentials
	if err := decodeStrictJSON(bytes.NewReader(raw), maxCredentialBytes, &credentials); err != nil ||
		credentials.Version != credentialVersion ||
		!validSensitiveText(credentials.Username, 1, 256) ||
		!validSensitiveText(credentials.Password, 1, 4096) {
		return rqliteCredentials{}, errConfig
	}
	return credentials, nil
}

func loadYandexCredentials(filePath string) (yandexCredentials, error) {
	raw, err := readSecureFile(filePath, secureSecretFile, maxCredentialBytes)
	if err != nil {
		return yandexCredentials{}, errUnsafeRuntime
	}
	var credentials yandexCredentials
	if err := decodeStrictJSON(bytes.NewReader(raw), maxCredentialBytes, &credentials); err != nil ||
		credentials.Version != credentialVersion ||
		!validSensitiveText(credentials.AccessKeyID, 3, 256) ||
		!validSensitiveText(credentials.SecretAccessKey, 8, 4096) {
		return yandexCredentials{}, errConfig
	}
	return credentials, nil
}

func loadCapabilityEvidence(filePath string) (capabilityEvidence, error) {
	raw, err := readSecureFile(filePath, secureSecretFile, maxCapabilityBytes)
	if err != nil {
		return capabilityEvidence{}, errUnsafeRuntime
	}
	var evidence capabilityEvidence
	if err := decodeStrictJSON(bytes.NewReader(raw), maxCapabilityBytes, &evidence); err != nil ||
		evidence.Version != capabilityVersion || evidence.Generation <= 0 ||
		!validLowerHex(evidence.SHA256, 64) {
		return capabilityEvidence{}, errConfig
	}
	return evidence, nil
}

func validSensitiveText(value string, minimum int, maximum int) bool {
	if len(value) < minimum || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func randomHex(byteCount int) (string, error) {
	if byteCount < 1 || byteCount > 64 {
		return "", errUnsafeRuntime
	}
	buffer := make([]byte, byteCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", errUnsafeRuntime
	}
	return hex.EncodeToString(buffer), nil
}
