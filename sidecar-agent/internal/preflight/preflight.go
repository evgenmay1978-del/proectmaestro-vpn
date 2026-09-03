package preflight

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	stdnet "net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	xraynet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/proxy/vless"
	vlessencoding "github.com/xtls/xray-core/proxy/vless/encoding"
)

const (
	relayPort       = 18084
	healthPort      = 18444
	attestationTTL  = 30 * time.Second
	maxConfigBytes  = 1 << 20
	maxFirewallData = 1 << 20
)

var (
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	credentialPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	serverNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
)

type Route struct {
	ExitID         string
	Address        string
	Port           int
	ServerName     string
	CAFile         string
	CredentialFile string
}

type Config struct {
	ReleaseID         string
	ConfigDigest      string
	ActiveOriginIPs   []string
	Routes            []Route
	runtimeConfigFile string
	credentialDigests map[string][sha256.Size]byte
}

type RuntimeConfigSource struct {
	XrayConfigFile           string
	ActiveOriginsFile        string
	RelayCADirectory         string
	RelayCredentialDirectory string
}

type Attestation struct {
	ReleaseID             string    `json:"release_id"`
	ConfigDigest          string    `json:"config_digest"`
	XrayProcessBootID     string    `json:"xray_process_boot_id"`
	ActiveOriginSetDigest string    `json:"active_origin_set_digest"`
	HealthyExitIDs        []string  `json:"healthy_exit_ids"`
	CheckedAt             time.Time `json:"checked_at"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type System interface {
	FirewallOrigins(context.Context) ([]string, error)
	ProbeRelay(context.Context, Route) error
}

type Checker struct {
	config Config
	system System
	now    func() time.Time
}

func NewChecker(config Config, system System, now func() time.Time) (*Checker, error) {
	if system == nil || !validConfig(config) {
		return nil, errors.New("relay preflight: invalid configuration")
	}
	if now == nil {
		now = time.Now
	}
	return &Checker{config: config, system: system, now: now}, nil
}

func (checker *Checker) Validate(ctx context.Context, releaseID, configDigest, bootID, exitID string) error {
	if checker == nil || releaseID != checker.config.ReleaseID || configDigest != checker.config.ConfigDigest ||
		!safeIdentifier(bootID) || !supportedExit(exitID) {
		return errors.New("relay preflight: readiness binding mismatch")
	}
	attestation, err := checker.Check(ctx, bootID)
	if err != nil {
		return err
	}
	if !contains(attestation.HealthyExitIDs, exitID) || !checker.now().Before(attestation.ExpiresAt) {
		return errors.New("relay preflight: required exit is not healthy")
	}
	return nil
}

func (checker *Checker) Check(ctx context.Context, bootID string) (Attestation, error) {
	if checker == nil || !safeIdentifier(bootID) {
		return Attestation{}, errors.New("relay preflight: invalid process identity")
	}
	if err := checker.verifyRuntimeBindings(); err != nil {
		return Attestation{}, err
	}
	actualOrigins, err := checker.system.FirewallOrigins(ctx)
	if err != nil {
		return Attestation{}, errors.New("relay preflight: source firewall unavailable")
	}
	actualOrigins, err = normalizeIPs(actualOrigins)
	if err != nil || !equalStrings(actualOrigins, checker.config.ActiveOriginIPs) {
		return Attestation{}, errors.New("relay preflight: source firewall mismatch")
	}
	healthy := make([]string, 0, len(checker.config.Routes))
	for _, route := range checker.config.Routes {
		if err := checker.system.ProbeRelay(ctx, route); err != nil {
			return Attestation{}, errors.New("relay preflight: exact exit health failed")
		}
		healthy = append(healthy, route.ExitID)
	}
	checkedAt := checker.now().UTC()
	originDigest := sha256.Sum256([]byte(strings.Join(actualOrigins, "\x00")))
	return Attestation{
		ReleaseID: checker.config.ReleaseID, ConfigDigest: checker.config.ConfigDigest,
		XrayProcessBootID: bootID, ActiveOriginSetDigest: hex.EncodeToString(originDigest[:]),
		HealthyExitIDs: healthy, CheckedAt: checkedAt, ExpiresAt: checkedAt.Add(attestationTTL),
	}, nil
}

type liveSystem struct {
	nftBinary string
}

func NewLiveSystem(nftBinary string) (System, error) {
	if !filepath.IsAbs(nftBinary) {
		return nil, errors.New("relay preflight: invalid nft executable")
	}
	return liveSystem{nftBinary: nftBinary}, nil
}

func (system liveSystem) FirewallOrigins(ctx context.Context) ([]string, error) {
	command := exec.CommandContext(ctx, system.nftBinary, "-j", "list", "table", "inet", "maestro_xray_cdn")
	output, err := command.Output()
	if err != nil || len(output) == 0 || len(output) > maxFirewallData {
		return nil, errors.New("relay preflight: nft query failed")
	}
	return ParseNFTSourceFirewall(output)
}

func (liveSystem) ProbeRelay(ctx context.Context, route Route) error {
	return probeRelay(ctx, route)
}

func LoadConfig(source RuntimeConfigSource, releaseID, configDigest string) (Config, error) {
	for _, path := range []string{source.XrayConfigFile, source.ActiveOriginsFile, source.RelayCADirectory, source.RelayCredentialDirectory} {
		if !absolutePath(path) {
			return Config{}, errors.New("relay preflight: invalid runtime source")
		}
	}
	raw, err := readRegularFile(source.XrayConfigFile, maxConfigBytes, false)
	if err != nil || !digestPattern.MatchString(configDigest) {
		return Config{}, errors.New("relay preflight: runtime config unavailable")
	}
	digest := sha256.Sum256(raw)
	expectedDigest, _ := hex.DecodeString(configDigest)
	if subtle.ConstantTimeCompare(digest[:], expectedDigest) != 1 {
		return Config{}, errors.New("relay preflight: runtime config digest mismatch")
	}
	var document runtimeConfig
	if json.Unmarshal(raw, &document) != nil || len(document.Outbounds) != 6 ||
		document.Outbounds[0].Protocol != "freedom" || document.Outbounds[0].Tag != "direct" ||
		document.Outbounds[1].Protocol != "blackhole" || document.Outbounds[1].Tag != "block" {
		return Config{}, errors.New("relay preflight: runtime route configuration invalid")
	}
	routes := make([]Route, 0, 4)
	credentialDigests := make(map[string][sha256.Size]byte, 4)
	for index, exitID := range exitIDs() {
		outbound := document.Outbounds[index+2]
		if outbound.Protocol != "vless" || outbound.Tag != exitID || len(outbound.Settings.VNext) != 1 ||
			len(outbound.Settings.VNext[0].Users) != 1 || outbound.Settings.VNext[0].Port != relayPort ||
			outbound.Settings.VNext[0].Users[0].Encryption != "none" || outbound.StreamSettings.Network != "tcp" ||
			outbound.StreamSettings.Security != "tls" || outbound.StreamSettings.TLSSettings == nil ||
			outbound.StreamSettings.TLSSettings.AllowInsecure == nil || *outbound.StreamSettings.TLSSettings.AllowInsecure ||
			!equalStrings(outbound.StreamSettings.TLSSettings.ALPN, []string{"h2"}) {
			return Config{}, errors.New("relay preflight: runtime route configuration invalid")
		}
		credentialFile := filepath.Join(source.RelayCredentialDirectory, exitID+".credential")
		credential, err := readCredential(credentialFile)
		if err != nil || !credentialsEqual(credential, outbound.Settings.VNext[0].Users[0].ID) {
			return Config{}, errors.New("relay preflight: protected relay credential mismatch")
		}
		credentialDigests[exitID] = sha256.Sum256([]byte(credential))
		caFile := filepath.Join(source.RelayCADirectory, exitID+".crt")
		if _, err := readRegularFile(caFile, maxConfigBytes, false); err != nil {
			return Config{}, errors.New("relay preflight: relay CA unavailable")
		}
		routes = append(routes, Route{
			ExitID: exitID, Address: outbound.Settings.VNext[0].Address, Port: relayPort,
			ServerName: outbound.StreamSettings.TLSSettings.ServerName,
			CAFile:     caFile, CredentialFile: credentialFile,
		})
	}
	originBytes, err := readRegularFile(source.ActiveOriginsFile, maxConfigBytes, false)
	if err != nil {
		return Config{}, errors.New("relay preflight: active Origin set unavailable")
	}
	var origins []string
	if json.Unmarshal(originBytes, &origins) != nil {
		return Config{}, errors.New("relay preflight: active Origin set invalid")
	}
	origins, err = normalizeIPs(origins)
	if err != nil {
		return Config{}, errors.New("relay preflight: active Origin set invalid")
	}
	config := Config{
		ReleaseID: releaseID, ConfigDigest: configDigest, ActiveOriginIPs: origins, Routes: routes,
		runtimeConfigFile: source.XrayConfigFile, credentialDigests: credentialDigests,
	}
	if !validConfig(config) {
		return Config{}, errors.New("relay preflight: runtime contract invalid")
	}
	return config, nil
}

func ParseNFTSourceFirewall(raw []byte) ([]string, error) {
	if len(raw) == 0 || len(raw) > maxFirewallData {
		return nil, errors.New("relay preflight: invalid nft data")
	}
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		return nil, errors.New("relay preflight: invalid nft data")
	}
	entries, ok := document["nftables"].([]any)
	if !ok {
		return nil, errors.New("relay preflight: invalid nft data")
	}
	var origins []string
	setCount, allowCount, dropCount := 0, 0, 0
	allowIndex, dropIndex, lastPortRule := -1, -1, -1
	for index, entryValue := range entries {
		entry, ok := entryValue.(map[string]any)
		if !ok {
			continue
		}
		if set, ok := entry["set"].(map[string]any); ok && stringValue(set, "family") == "inet" &&
			stringValue(set, "table") == "maestro_xray_cdn" && stringValue(set, "name") == "active_origins_18084" &&
			stringValue(set, "type") == "ipv4_addr" {
			setCount++
			if setCount != 1 {
				return nil, errors.New("relay preflight: duplicate source set")
			}
			elements, ok := set["elem"].([]any)
			if !ok {
				return nil, errors.New("relay preflight: invalid source set")
			}
			origins = origins[:0]
			for _, element := range elements {
				value, ok := element.(string)
				if !ok {
					return nil, errors.New("relay preflight: invalid source set")
				}
				origins = append(origins, value)
			}
		}
		rule, ok := entry["rule"].(map[string]any)
		if !ok || stringValue(rule, "family") != "inet" || stringValue(rule, "table") != "maestro_xray_cdn" ||
			stringValue(rule, "chain") != "input" {
			continue
		}
		expressions, ok := rule["expr"].([]any)
		if !ok {
			continue
		}
		hasAccept := hasVerdict(expressions, "accept")
		hasDrop := hasVerdict(expressions, "drop")
		hasDPort := matchesDPort(expressions)
		hasSourceSet := matchesSourceSet(expressions)
		if hasAccept && (!hasDPort || !hasSourceSet || hasDrop) {
			return nil, errors.New("relay preflight: unsafe accept rule")
		}
		if !hasDPort {
			continue
		}
		lastPortRule = index
		if hasAccept && hasSourceSet && !hasDrop {
			allowCount++
			allowIndex = index
		}
		if hasDrop && !hasAccept {
			dropCount++
			dropIndex = index
		}
	}
	if len(origins) == 0 || setCount != 1 || allowCount != 1 || dropCount != 1 ||
		allowIndex < 0 || dropIndex <= allowIndex || dropIndex != lastPortRule {
		return nil, errors.New("relay preflight: incomplete source firewall")
	}
	origins, err := normalizeIPs(origins)
	if err != nil {
		return nil, errors.New("relay preflight: invalid source set")
	}
	return origins, nil
}

func probeRelay(ctx context.Context, route Route) error {
	caBytes, err := readRegularFile(route.CAFile, maxConfigBytes, false)
	if err != nil {
		return errors.New("relay preflight: relay CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return errors.New("relay preflight: relay CA invalid")
	}
	credential, err := readCredential(route.CredentialFile)
	if err != nil {
		return err
	}
	account, err := (&vless.Account{Id: credential, Encryption: "none"}).AsAccount()
	if err != nil {
		return errors.New("relay preflight: protected relay credential invalid")
	}
	probeContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rawConnection, err := (&stdnet.Dialer{}).DialContext(probeContext, "tcp", stdnet.JoinHostPort(route.Address, strconv.Itoa(route.Port)))
	if err != nil {
		return errors.New("relay preflight: relay connection failed")
	}
	connection := tls.Client(rawConnection, &tls.Config{
		MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, ServerName: route.ServerName,
		RootCAs: roots, NextProtos: []string{"h2"},
	})
	defer connection.Close()
	if deadline, ok := probeContext.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := connection.HandshakeContext(probeContext); err != nil || connection.ConnectionState().NegotiatedProtocol != "h2" {
		return errors.New("relay preflight: relay TLS identity or ALPN failed")
	}
	request := &protocol.RequestHeader{
		Version: vlessencoding.Version,
		User:    &protocol.MemoryUser{Email: "relay:" + route.ExitID, Account: account},
		Command: protocol.RequestCommandTCP, Address: xraynet.ParseAddress("127.0.0.1"), Port: xraynet.Port(healthPort),
	}
	if err := vlessencoding.EncodeRequestHeader(connection, request, &vlessencoding.Addons{}); err != nil {
		return errors.New("relay preflight: VLESS request failed")
	}
	if _, err := io.WriteString(connection, "GET /healthz HTTP/1.1\r\nHost: maestro-relay-health\r\nConnection: close\r\n\r\n"); err != nil {
		return errors.New("relay preflight: health request failed")
	}
	if _, err := vlessencoding.DecodeResponseHeader(connection, request); err != nil {
		return errors.New("relay preflight: VLESS authentication failed")
	}
	httpRequest, _ := http.NewRequestWithContext(probeContext, http.MethodGet, "http://maestro-relay-health/healthz", nil)
	response, err := http.ReadResponse(bufio.NewReader(connection), httpRequest)
	if err != nil {
		return errors.New("relay preflight: health response invalid")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("X-Maestro-Relay-Health") != "exact" {
		return errors.New("relay preflight: exact exit health rejected")
	}
	return nil
}

type runtimeConfig struct {
	Outbounds []runtimeOutbound `json:"outbounds"`
}

type runtimeOutbound struct {
	Protocol       string                `json:"protocol"`
	Tag            string                `json:"tag"`
	Settings       runtimeSettings       `json:"settings"`
	StreamSettings runtimeStreamSettings `json:"streamSettings"`
}

type runtimeSettings struct {
	VNext []runtimeVNext `json:"vnext"`
}

type runtimeVNext struct {
	Address string        `json:"address"`
	Port    int           `json:"port"`
	Users   []runtimeUser `json:"users"`
}

type runtimeUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
}

type runtimeStreamSettings struct {
	Network     string              `json:"network"`
	Security    string              `json:"security"`
	TLSSettings *runtimeTLSSettings `json:"tlsSettings"`
}

type runtimeTLSSettings struct {
	ServerName    string   `json:"serverName"`
	AllowInsecure *bool    `json:"allowInsecure"`
	ALPN          []string `json:"alpn"`
}

func validConfig(config Config) bool {
	if !safeIdentifier(config.ReleaseID) || !digestPattern.MatchString(config.ConfigDigest) || len(config.Routes) != 4 {
		return false
	}
	origins, err := normalizeIPs(config.ActiveOriginIPs)
	if err != nil || !equalStrings(origins, config.ActiveOriginIPs) {
		return false
	}
	for index, exitID := range exitIDs() {
		route := config.Routes[index]
		if route.ExitID != exitID || stdnet.ParseIP(route.Address) == nil || route.Port != relayPort ||
			!serverNamePattern.MatchString(route.ServerName) || stdnet.ParseIP(route.ServerName) != nil ||
			!absolutePath(route.CAFile) || !absolutePath(route.CredentialFile) {
			return false
		}
	}
	return true
}

func (checker *Checker) verifyRuntimeBindings() error {
	if checker.config.runtimeConfigFile == "" {
		return nil
	}
	raw, err := readRegularFile(checker.config.runtimeConfigFile, maxConfigBytes, false)
	if err != nil {
		return errors.New("relay preflight: runtime config unavailable")
	}
	actualConfigDigest := sha256.Sum256(raw)
	expectedConfigDigest, _ := hex.DecodeString(checker.config.ConfigDigest)
	if subtle.ConstantTimeCompare(actualConfigDigest[:], expectedConfigDigest) != 1 {
		return errors.New("relay preflight: runtime config changed")
	}
	for _, route := range checker.config.Routes {
		credential, err := readCredential(route.CredentialFile)
		expected, ok := checker.config.credentialDigests[route.ExitID]
		actual := sha256.Sum256([]byte(credential))
		if err != nil || !ok || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			return errors.New("relay preflight: protected relay credential changed")
		}
	}
	return nil
}

func readCredential(path string) (string, error) {
	raw, err := readRegularFile(path, 256, true)
	if err != nil {
		return "", errors.New("relay preflight: protected relay credential unavailable")
	}
	credential := strings.TrimSpace(string(raw))
	if !credentialPattern.MatchString(credential) {
		return "", errors.New("relay preflight: protected relay credential invalid")
	}
	return credential, nil
}

func readRegularFile(path string, maximum int64, private bool) ([]byte, error) {
	if !absolutePath(path) {
		return nil, errors.New("relay preflight: invalid file path")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum ||
		(private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("relay preflight: protected file unavailable")
	}
	return os.ReadFile(path)
}

func absolutePath(path string) bool {
	return filepath.IsAbs(path) || (strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\x00\r\n"))
}

func normalizeIPs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 64 {
		return nil, errors.New("relay preflight: invalid IP set")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		ip := stdnet.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return nil, errors.New("relay preflight: invalid IP set")
		}
		normalized := ip.To4().String()
		if _, duplicate := seen[normalized]; duplicate {
			return nil, errors.New("relay preflight: duplicate IP")
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result, nil
}

func exitIDs() []string {
	return []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"}
}

func supportedExit(value string) bool {
	return contains(exitIDs(), value)
}

func safeIdentifier(value string) bool {
	return value != "" && len(value) <= 128 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\x00\r\n\t /\\")
}

func credentialsEqual(left, right string) bool {
	return credentialPattern.MatchString(left) && credentialPattern.MatchString(right) &&
		subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func equalStrings(left, right []string) bool {
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

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func matchesDPort(expressions []any) bool {
	for _, expression := range expressions {
		statement, ok := expression.(map[string]any)
		if !ok {
			continue
		}
		match, ok := statement["match"].(map[string]any)
		if !ok || stringValue(match, "op") != "==" {
			continue
		}
		left, ok := match["left"].(map[string]any)
		if !ok {
			continue
		}
		payload, ok := left["payload"].(map[string]any)
		right, rightOK := match["right"].(float64)
		if ok && rightOK && stringValue(payload, "protocol") == "tcp" && stringValue(payload, "field") == "dport" && int(right) == relayPort {
			return true
		}
	}
	return false
}

func matchesSourceSet(expressions []any) bool {
	for _, expression := range expressions {
		statement, ok := expression.(map[string]any)
		if !ok {
			continue
		}
		match, ok := statement["match"].(map[string]any)
		if !ok || stringValue(match, "op") != "==" {
			continue
		}
		left, ok := match["left"].(map[string]any)
		if !ok {
			continue
		}
		payload, ok := left["payload"].(map[string]any)
		if !ok || stringValue(payload, "protocol") != "ip" || stringValue(payload, "field") != "saddr" {
			continue
		}
		switch right := match["right"].(type) {
		case map[string]any:
			if stringValue(right, "set") == "active_origins_18084" {
				return true
			}
		case string:
			if right == "@active_origins_18084" {
				return true
			}
		}
	}
	return false
}

func hasVerdict(expressions []any, verdict string) bool {
	for _, expression := range expressions {
		statement, ok := expression.(map[string]any)
		if !ok {
			continue
		}
		if _, present := statement[verdict]; present {
			return true
		}
	}
	return false
}
