package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/canary"
)

const (
	exitOK        = 0
	exitUsage     = 2
	exitOperation = 4
	exitInternal  = 70

	maxRequestBytes     int64 = 64 << 10
	maxArchiveBytes     int64 = 64 << 20
	maxXrayBinaryBytes  int64 = 64 << 20
	maxDiagnosticBytes  int64 = 1 << 20
	maxVLESSOutputBytes       = 64 << 10
	maxArchiveEntries         = 128

	exactServiceName = "maestro-xray-cdn.service"

	pinnedArchiveSHA256 = "8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40"
	pinnedBinarySHA256  = "64d46afb80adea1bf97a0d467e83f4a9ac1ebd0995891e84bca3f1a1d1affb1d"

	secretPathPrefix = "/static/main/video/segment.ts/"
	randomPathBytes  = 24
)

type reasoned interface{ ReasonCode() string }

type reasonError struct{ code string }

func (err reasonError) Error() string      { return "canary operation failed" }
func (err reasonError) ReasonCode() string { return err.code }

type commandOperator interface {
	Prepare(context.Context, string, string) (canary.Stage, error)
	Activate(context.Context, string) (canary.Stage, error)
	Rollback(context.Context, string) (canary.Stage, error)
	Status(context.Context) (canary.Stage, error)
}

type prepareStore interface {
	Prepare(context.Context, canary.Snapshot, []byte, canary.Artifacts, canary.ConfigTester) (canary.Stage, error)
}

type binaryInvoker interface {
	Invoke(context.Context, string, []string, int) ([]byte, error)
}

type prepareDependencies struct {
	readProtected         func(string, int64) ([]byte, error)
	expectedArchiveSHA256 string
	expectedBinarySHA256  string
	stageExecutable       func([]byte) (string, func(), error)
	invoker               binaryInvoker
	newUUID               func() (string, error)
	newPath               func() (string, error)
	store                 prepareStore
	tester                canary.ConfigTester
}

type parsedCommand struct {
	name        string
	requestFile string
	archiveFile string
	runtimeID   string
}

type uniqueStringFlag struct {
	value string
	seen  bool
}

func (value *uniqueStringFlag) Set(input string) error {
	if value.seen {
		return errors.New("duplicate flag")
	}
	value.value = input
	value.seen = true
	return nil
}
func (value *uniqueStringFlag) String() string { return value.value }

type successOutput struct {
	Code               string       `json:"code"`
	State              canary.State `json:"state,omitempty"`
	RuntimeID          string       `json:"runtime_id,omitempty"`
	SnapshotSHA256     string       `json:"snapshot_sha256,omitempty"`
	XraySHA256         string       `json:"xray_sha256,omitempty"`
	ServerConfigSHA256 string       `json:"server_config_sha256,omitempty"`
	UnitSHA256         string       `json:"unit_sha256,omitempty"`
}

type vlessPair struct {
	server string
	client string
}

type protectedFileMetadata struct {
	regular bool
	uid     uint32
	gid     uint32
	mode    uint32
	links   uint64
	size    int64
}

var (
	commandOperatorFactory = newPlatformOperator
	vlessFieldPattern      = regexp.MustCompile(`^"(decryption|encryption)"[ \t]*:[ \t]*"([^"\\]+)"[ \t]*,?[ \t]*$`)
)

func main() { os.Exit(safeRun(os.Args[1:], os.Stdout, os.Stderr)) }

func safeRun(args []string, stdout, stderr io.Writer) (code int) {
	defer func() {
		if recover() != nil {
			code = writeFailure(stderr, "internal_failure", exitInternal)
		}
	}()
	return run(args, stdout, stderr)
}

func run(args []string, stdout, stderr io.Writer) int {
	command, ok := parseCommand(args)
	if !ok {
		return writeFailure(stderr, "arguments_invalid", exitUsage)
	}
	operator, err := commandOperatorFactory()
	if err != nil {
		return writeFailure(stderr, safeReason(err), exitOperation)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch command.name {
	case "prepare":
		stage, prepareErr := operator.Prepare(ctx, command.requestFile, command.archiveFile)
		if prepareErr != nil {
			if validStage(stage, canary.StatePrepared) {
				if code := writeSuccess(stdout, stageOutput("canary_prepare_recovery_required", stage)); code != exitOK {
					return writeFailure(stderr, "output_failed", code)
				}
			}
			return writeFailure(stderr, safeReason(prepareErr), exitOperation)
		}
		if !validStage(stage, canary.StatePrepared) {
			return writeFailure(stderr, "lifecycle_state_invalid", exitOperation)
		}
		return writeSuccess(stdout, stageOutput("canary_prepare_succeeded", stage))
	case "activate":
		stage, activateErr := operator.Activate(ctx, command.runtimeID)
		if activateErr != nil {
			return writeFailure(stderr, safeReason(activateErr), exitOperation)
		}
		if !validStage(stage, canary.StateCanaryActive) || stage.RuntimeID != command.runtimeID {
			return writeFailure(stderr, "lifecycle_state_invalid", exitOperation)
		}
		return writeSuccess(stdout, stageOutput("canary_activate_succeeded", stage))
	case "rollback":
		stage, rollbackErr := operator.Rollback(ctx, command.runtimeID)
		if rollbackErr != nil {
			return writeFailure(stderr, safeReason(rollbackErr), exitOperation)
		}
		if stage.State != canary.StateAbsent || stage.RuntimeID != "" {
			return writeFailure(stderr, "lifecycle_state_invalid", exitOperation)
		}
		return writeSuccess(stdout, stageOutput("canary_rollback_after_external_restore_succeeded", stage))
	case "status":
		stage, statusErr := operator.Status(ctx)
		if statusErr != nil {
			return writeFailure(stderr, safeReason(statusErr), exitOperation)
		}
		code, valid := statusCode(stage)
		if !valid {
			return writeFailure(stderr, "lifecycle_state_invalid", exitOperation)
		}
		return writeSuccess(stdout, stageOutput(code, stage))
	default:
		return writeFailure(stderr, "arguments_invalid", exitUsage)
	}
}

func parseCommand(args []string) (parsedCommand, bool) {
	if len(args) == 0 {
		return parsedCommand{}, false
	}
	result := parsedCommand{name: args[0]}
	flags := flag.NewFlagSet(result.name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var request, archive, runtimeID uniqueStringFlag
	switch result.name {
	case "prepare":
		flags.Var(&request, "request-file", "protected request")
		flags.Var(&archive, "xray-archive", "protected Xray archive")
	case "activate", "rollback":
		flags.Var(&runtimeID, "runtime-id", "prepared runtime identifier")
	case "status":
	default:
		return parsedCommand{}, false
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return parsedCommand{}, false
	}
	switch result.name {
	case "prepare":
		if !request.seen || !archive.seen || !safeAbsolutePath(request.value) || !safeAbsolutePath(archive.value) {
			return parsedCommand{}, false
		}
		result.requestFile, result.archiveFile = request.value, archive.value
	case "activate", "rollback":
		if !runtimeID.seen || !safeRuntimeID(runtimeID.value) {
			return parsedCommand{}, false
		}
		result.runtimeID = runtimeID.value
	case "status":
		if request.seen || archive.seen || runtimeID.seen {
			return parsedCommand{}, false
		}
	}
	return result, true
}

func safeAbsolutePath(value string) bool {
	if value == "" || len(value) > 4096 || value != strings.TrimSpace(value) || !utf8.ValidString(value) || !filepath.IsAbs(value) {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return filepath.Clean(value) == value
}

func safeRuntimeID(value string) bool {
	if len(value) != 34 || !strings.HasPrefix(value, "r-") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func validStage(stage canary.Stage, expected canary.State) bool {
	return stage.State == expected && safeRuntimeID(stage.RuntimeID) && validDigest(stage.SnapshotSHA256) &&
		validDigest(stage.XraySHA256) && validDigest(stage.ServerConfigSHA256) && validDigest(stage.UnitSHA256)
}

func statusCode(stage canary.Stage) (string, bool) {
	switch stage.State {
	case canary.StateAbsent:
		return "canary_status_absent", stage.RuntimeID == "" && stage.SnapshotSHA256 == "" && stage.XraySHA256 == "" && stage.ServerConfigSHA256 == "" && stage.UnitSHA256 == ""
	case canary.StatePrepared:
		return "canary_status_prepared", validStage(stage, canary.StatePrepared)
	case canary.StateRollbackRequired:
		return "canary_status_rollback_required", validStage(stage, canary.StateRollbackRequired)
	case canary.StateCanaryActive:
		return "canary_status_canary_active", validStage(stage, canary.StateCanaryActive)
	default:
		return "", false
	}
}

func stageOutput(code string, stage canary.Stage) successOutput {
	return successOutput{
		Code: code, State: stage.State, RuntimeID: stage.RuntimeID,
		SnapshotSHA256: stage.SnapshotSHA256, XraySHA256: stage.XraySHA256,
		ServerConfigSHA256: stage.ServerConfigSHA256, UnitSHA256: stage.UnitSHA256,
	}
}

func writeSuccess(stdout io.Writer, output successOutput) int {
	if err := json.NewEncoder(stdout).Encode(output); err != nil {
		return exitInternal
	}
	return exitOK
}

func writeFailure(stderr io.Writer, code string, exitCode int) int {
	if !safeReasonCode(code) {
		code = "operation_failed"
	}
	if _, err := fmt.Fprintf(stderr, "canary_failed code=%s\n", code); err != nil {
		return exitInternal
	}
	return exitCode
}

func safeReason(err error) string {
	var value reasoned
	if errors.As(err, &value) && safeReasonCode(value.ReasonCode()) {
		return value.ReasonCode()
	}
	return "operation_failed"
}

func safeReasonCode(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, current := range value[1:] {
		if (current < 'a' || current > 'z') && (current < '0' || current > '9') && current != '_' {
			return false
		}
	}
	return true
}

func prepareCanary(ctx context.Context, deps prepareDependencies, requestPath, archivePath string) (canary.Stage, error) {
	if deps.readProtected == nil || deps.stageExecutable == nil || deps.invoker == nil || deps.newUUID == nil || deps.newPath == nil || deps.store == nil ||
		!validDigest(deps.expectedArchiveSHA256) || !validDigest(deps.expectedBinarySHA256) {
		return canary.Stage{}, reasonError{"prepare_dependencies_invalid"}
	}
	requestRaw, err := deps.readProtected(requestPath, maxRequestBytes)
	if err != nil {
		return canary.Stage{}, reasonError{"request_file_invalid"}
	}
	request, err := canary.ParseRequest(requestRaw)
	if err != nil {
		return canary.Stage{}, reasonError{"request_invalid"}
	}
	archiveRaw, err := deps.readProtected(archivePath, maxArchiveBytes)
	if err != nil {
		return canary.Stage{}, reasonError{"archive_file_invalid"}
	}
	if !digestMatches(archiveRaw, deps.expectedArchiveSHA256) {
		return canary.Stage{}, reasonError{"archive_digest_mismatch"}
	}
	binary, err := extractPinnedXray(archiveRaw, maxXrayBinaryBytes)
	if err != nil {
		return canary.Stage{}, reasonError{"archive_invalid"}
	}
	if !digestMatches(binary, deps.expectedBinarySHA256) {
		return canary.Stage{}, reasonError{"binary_digest_mismatch"}
	}
	binaryPath, cleanup, err := deps.stageExecutable(binary)
	if err != nil || cleanup == nil {
		return canary.Stage{}, reasonError{"temporary_binary_failed"}
	}
	defer cleanup()
	output, err := deps.invoker.Invoke(ctx, binaryPath, []string{"vlessenc"}, maxVLESSOutputBytes)
	if err != nil {
		return canary.Stage{}, reasonError{"vlessenc_failed"}
	}
	pair, err := parseVLESSOutput(output)
	if err != nil {
		return canary.Stage{}, reasonError{"vlessenc_output_invalid"}
	}
	clientID, err := deps.newUUID()
	if err != nil || !validUUIDv4(clientID) {
		return canary.Stage{}, reasonError{"uuid_generation_failed"}
	}
	secretPath, err := deps.newPath()
	if err != nil || !validGeneratedPath(secretPath) {
		return canary.Stage{}, reasonError{"path_generation_failed"}
	}
	material := canary.Material{
		ClientID: clientID, ClientEmail: "canary-" + strings.ReplaceAll(clientID, "-", "") + "@maestro.invalid",
		ServerDecryption: pair.server, ClientEncryption: pair.client, SecretPath: secretPath,
	}
	material.PairTranscriptSHA256 = materialTranscript(material)
	snapshot, err := canary.NewSnapshot(request, material)
	if err != nil {
		return canary.Stage{}, reasonError{"generated_material_invalid"}
	}
	artifacts, err := snapshot.Materialize()
	if err != nil {
		return canary.Stage{}, reasonError{"materialization_failed"}
	}
	stage, err := deps.store.Prepare(ctx, snapshot, binary, artifacts, deps.tester)
	if err != nil {
		if validStage(stage, canary.StatePrepared) {
			return stage, reasonError{"prepare_failed"}
		}
		return canary.Stage{}, reasonError{"prepare_failed"}
	}
	if !validStage(stage, canary.StatePrepared) {
		return canary.Stage{}, reasonError{"lifecycle_state_invalid"}
	}
	return stage, nil
}

func extractPinnedXray(raw []byte, maximum int64) ([]byte, error) {
	if len(raw) == 0 || maximum <= 0 || int64(len(raw)) > maxArchiveBytes {
		return nil, reasonError{"archive_invalid"}
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(reader.File) == 0 || len(reader.File) > maxArchiveEntries {
		return nil, reasonError{"archive_invalid"}
	}
	seen := make(map[string]struct{}, len(reader.File))
	var selected *zip.File
	for _, file := range reader.File {
		name, isDirectory, valid := canonicalArchiveName(file.Name)
		if !valid {
			return nil, reasonError{"archive_invalid"}
		}
		collisionKey := strings.ToLower(name)
		if _, exists := seen[collisionKey]; exists {
			return nil, reasonError{"archive_invalid"}
		}
		seen[collisionKey] = struct{}{}
		mode := file.Mode()
		if isDirectory {
			if !mode.IsDir() {
				return nil, reasonError{"archive_invalid"}
			}
			continue
		}
		if !mode.IsRegular() || file.UncompressedSize64 > uint64(maximum) {
			return nil, reasonError{"archive_invalid"}
		}
		if name == "xray" {
			if selected != nil || mode.Perm()&0o111 == 0 || mode.Perm()&0o022 != 0 {
				return nil, reasonError{"archive_invalid"}
			}
			selected = file
			continue
		}
		if mode.Perm()&0o111 != 0 {
			return nil, reasonError{"archive_invalid"}
		}
	}
	if selected == nil || selected.UncompressedSize64 == 0 || selected.UncompressedSize64 > uint64(maximum) {
		return nil, reasonError{"archive_invalid"}
	}
	stream, err := selected.Open()
	if err != nil {
		return nil, reasonError{"archive_invalid"}
	}
	defer stream.Close()
	binary, err := io.ReadAll(io.LimitReader(stream, maximum+1))
	if err != nil || len(binary) == 0 || int64(len(binary)) > maximum || uint64(len(binary)) != selected.UncompressedSize64 {
		return nil, reasonError{"archive_invalid"}
	}
	return append([]byte(nil), binary...), nil
}

func canonicalArchiveName(raw string) (string, bool, bool) {
	if raw == "" || !utf8.ValidString(raw) || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "\\\x00\r\n") {
		return "", false, false
	}
	for _, current := range raw {
		if unicode.IsControl(current) {
			return "", false, false
		}
	}
	isDirectory := strings.HasSuffix(raw, "/")
	name := strings.TrimSuffix(raw, "/")
	if name == "" || name == "." || pathpkg.Clean(name) != name || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
		return "", false, false
	}
	return name, isDirectory, true
}

func parseVLESSOutput(raw []byte) (vlessPair, error) {
	if len(raw) == 0 || len(raw) > maxVLESSOutputBytes || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return vlessPair{}, reasonError{"vlessenc_output_invalid"}
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if strings.ContainsRune(text, '\r') {
		return vlessPair{}, reasonError{"vlessenc_output_invalid"}
	}
	const header = "Authentication: ML-KEM-768, Post-Quantum"
	active, headers := false, 0
	decryption, encryption := make([]string, 0, 1), make([]string, 0, 1)
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "Authentication:") {
			active = line == header
			if active {
				headers++
			}
			continue
		}
		if !active {
			continue
		}
		match := vlessFieldPattern.FindStringSubmatch(line)
		if len(match) == 3 {
			if match[1] == "decryption" {
				decryption = append(decryption, match[2])
			} else {
				encryption = append(encryption, match[2])
			}
			continue
		}
		if strings.Contains(line, `"decryption"`) || strings.Contains(line, `"encryption"`) {
			return vlessPair{}, reasonError{"vlessenc_output_invalid"}
		}
	}
	if headers != 1 || len(decryption) != 1 || len(encryption) != 1 {
		return vlessPair{}, reasonError{"vlessenc_output_invalid"}
	}
	return vlessPair{server: decryption[0], client: encryption[0]}, nil
}

func randomUUIDv4(reader io.Reader) (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", reasonError{"uuid_generation_failed"}
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func randomSecretPath(reader io.Reader) (string, error) {
	raw := make([]byte, randomPathBytes)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", reasonError{"path_generation_failed"}
	}
	return secretPathPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func validUUIDv4(value string) bool {
	if len(value) != 36 || value[14] != '4' || !strings.ContainsRune("89ab", rune(value[19])) || value != strings.ToLower(value) {
		return false
	}
	for index, current := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if current != '-' {
				return false
			}
			continue
		}
		if !(current >= '0' && current <= '9' || current >= 'a' && current <= 'f') {
			return false
		}
	}
	return true
}

func validGeneratedPath(value string) bool {
	token := strings.TrimPrefix(value, secretPathPrefix)
	if token == value || len(token) != base64.RawURLEncoding.EncodedLen(randomPathBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == randomPathBytes && base64.RawURLEncoding.EncodeToString(decoded) == token
}

func materialTranscript(material canary.Material) string {
	raw, _ := json.Marshal(struct {
		ClientID         string `json:"client_id"`
		ClientEmail      string `json:"client_email"`
		ServerDecryption string `json:"server_decryption"`
		ClientEncryption string `json:"client_encryption"`
		SecretPath       string `json:"secret_path"`
	}{material.ClientID, material.ClientEmail, material.ServerDecryption, material.ClientEncryption, material.SecretPath})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validProtectedFileMetadata(metadata protectedFileMetadata, maximum int64) bool {
	return metadata.regular && metadata.uid == 0 && metadata.gid == 0 && metadata.mode == 0o600 && metadata.links == 1 && metadata.size > 0 && metadata.size <= maximum
}

func systemctlServiceArgs(operation, service string) ([]string, error) {
	if service != exactServiceName || (operation != "is-active" && operation != "start" && operation != "stop") {
		return nil, reasonError{"service_command_invalid"}
	}
	args := []string{operation}
	if operation == "is-active" {
		args = append(args, "--quiet")
	}
	return append(args, service), nil
}

func systemctlReloadArgs() []string { return []string{"daemon-reload"} }

func newDiagnosticHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return reasonError{"diagnostic_redirect_refused"}
		},
	}
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func verifyRestoredDiagnosticOrigin(ctx context.Context, client httpDoer, probeURL, expectedSHA256 string, maximum int64) error {
	parsed, err := url.ParseRequestURI(probeURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || !validDigest(expectedSHA256) || maximum <= 0 || maximum > maxDiagnosticBytes {
		return reasonError{"diagnostic_request_invalid"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return reasonError{"diagnostic_request_invalid"}
	}
	response, err := client.Do(request)
	if err != nil || response == nil || response.Body == nil {
		return reasonError{"diagnostic_verify_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return reasonError{"diagnostic_status_invalid"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum || !digestMatches(body, expectedSHA256) {
		return reasonError{"diagnostic_response_invalid"}
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func digestMatches(raw []byte, expected string) bool {
	if !validDigest(expected) {
		return false
	}
	actual := sha256.Sum256(raw)
	decoded, _ := hex.DecodeString(expected)
	return subtle.ConstantTimeCompare(actual[:], decoded) == 1
}

func productionPrepareDependencies(store prepareStore, tester canary.ConfigTester, reader func(string, int64) ([]byte, error), stager func([]byte) (string, func(), error), invoker binaryInvoker) prepareDependencies {
	return prepareDependencies{
		readProtected: reader, expectedArchiveSHA256: pinnedArchiveSHA256, expectedBinarySHA256: pinnedBinarySHA256,
		stageExecutable: stager, invoker: invoker,
		newUUID: func() (string, error) { return randomUUIDv4(rand.Reader) },
		newPath: func() (string, error) { return randomSecretPath(rand.Reader) },
		store:   store, tester: tester,
	}
}
