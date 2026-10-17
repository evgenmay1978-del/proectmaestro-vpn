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
	controllerPort  = 18443
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
	ReleaseID          string
	ConfigDigest       string
	ActiveOriginIPs    []string
	ControllerSourceIP string
	Routes             []Route
	runtimeConfigFile  string
	credentialDigests  map[string][sha256.Size]byte
}

type RuntimeConfigSource struct {
	XrayConfigFile           string
	ActiveOriginsFile        string
	ControllerSourceIPFile   string
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
	Firewall(context.Context) (FirewallState, error)
	ProbeRelay(context.Context, Route) error
}

type FirewallState struct {
	ActiveOriginIPs     []string
	ControllerSourceIPs []string
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

// ValidateRuntimeBinding reads protected runtime files only: no firewall command
// or relay connection is needed when serving a counter snapshot.
func (checker *Checker) ValidateRuntimeBinding(releaseID, configDigest string) error {
	if checker == nil || releaseID != checker.config.ReleaseID || configDigest != checker.config.ConfigDigest {
		return errors.New("relay preflight: runtime binding mismatch")
	}
	return checker.verifyRuntimeBindings()
}

func (checker *Checker) Validate(ctx context.Context, releaseID, configDigest, bootID, exitID string) error {
	if checker == nil || releaseID != checker.config.ReleaseID || configDigest != checker.config.ConfigDigest ||
		!safeIdentifier(bootID) || !supportedExit(exitID) {
		return errors.New("relay preflight: readiness binding mismatch")
	}
	selected := make([]Route, 0, 1)
	for _, route := range checker.config.Routes {
		if route.ExitID == exitID {
			selected = append(selected, route)
		}
	}
	if len(selected) != 1 {
		return errors.New("relay preflight: readiness binding mismatch")
	}
	attestation, err := checker.check(ctx, bootID, selected)
	if err != nil {
		return err
	}
	if !contains(attestation.HealthyExitIDs, exitID) || !checker.now().Before(attestation.ExpiresAt) {
		return errors.New("relay preflight: required exit is not healthy")
	}
	return nil
}

func (checker *Checker) Check(ctx context.Context, bootID string) (Attestation, error) {
	if checker == nil {
		return Attestation{}, errors.New("relay preflight: invalid process identity")
	}
	return checker.check(ctx, bootID, checker.config.Routes)
}

func (checker *Checker) check(ctx context.Context, bootID string, routes []Route) (Attestation, error) {
	if checker == nil || !safeIdentifier(bootID) {
		return Attestation{}, errors.New("relay preflight: invalid process identity")
	}
	if err := checker.verifyRuntimeBindings(); err != nil {
		return Attestation{}, err
	}
	firewall, err := checker.system.Firewall(ctx)
	if err != nil {
		return Attestation{}, errors.New("relay preflight: source firewall unavailable")
	}
	actualOrigins, err := normalizeIPs(firewall.ActiveOriginIPs)
	if err != nil || !equalStrings(actualOrigins, checker.config.ActiveOriginIPs) {
		return Attestation{}, errors.New("relay preflight: source firewall mismatch")
	}
	controllerSources, err := normalizeIPs(firewall.ControllerSourceIPs)
	if err != nil || !equalStrings(controllerSources, []string{checker.config.ControllerSourceIP}) {
		return Attestation{}, errors.New("relay preflight: controller source firewall mismatch")
	}
	healthy := make([]string, 0, len(routes))
	for _, route := range routes {
		if err := checker.system.ProbeRelay(ctx, route); err != nil {
			continue
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

func (system liveSystem) Firewall(ctx context.Context) (FirewallState, error) {
	command := exec.CommandContext(ctx, system.nftBinary, "-j", "list", "table", "inet", "maestro_xray_cdn")
	output, err := command.Output()
	if err != nil || len(output) == 0 || len(output) > maxFirewallData {
		return FirewallState{}, errors.New("relay preflight: nft query failed")
	}
	return ParseNFTSourceFirewall(output)
}

func (liveSystem) ProbeRelay(ctx context.Context, route Route) error {
	return probeRelay(ctx, route)
}

func LoadConfig(source RuntimeConfigSource, releaseID, configDigest string) (Config, error) {
	for _, path := range []string{source.XrayConfigFile, source.ActiveOriginsFile, source.ControllerSourceIPFile, source.RelayCADirectory, source.RelayCredentialDirectory} {
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
	controllerBytes, err := readRegularFile(source.ControllerSourceIPFile, maxConfigBytes, false)
	if err != nil {
		return Config{}, errors.New("relay preflight: controller source unavailable")
	}
	var controllerSource string
	if json.Unmarshal(controllerBytes, &controllerSource) != nil {
		return Config{}, errors.New("relay preflight: controller source invalid")
	}
	controllerSources, err := normalizeIPs([]string{controllerSource})
	if err != nil {
		return Config{}, errors.New("relay preflight: controller source invalid")
	}
	config := Config{
		ReleaseID: releaseID, ConfigDigest: configDigest, ActiveOriginIPs: origins,
		ControllerSourceIP: controllerSources[0], Routes: routes,
		runtimeConfigFile: source.XrayConfigFile, credentialDigests: credentialDigests,
	}
	if !validConfig(config) {
		return Config{}, errors.New("relay preflight: runtime contract invalid")
	}
	return config, nil
}

func ParseNFTSourceFirewall(raw []byte) (FirewallState, error) {
	if len(raw) == 0 || len(raw) > maxFirewallData {
		return FirewallState{}, errors.New("relay preflight: invalid nft data")
	}
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		return FirewallState{}, errors.New("relay preflight: invalid nft data")
	}
	entries, ok := document["nftables"].([]any)
	if !ok {
		return FirewallState{}, errors.New("relay preflight: invalid nft data")
	}
	var origins, controllerSources []string
	chainCount, originSetCount, controllerSetCount := 0, 0, 0
	type portPolicy struct {
		allowCount int
		dropCount  int
		allowIndex int
		dropIndex  int
		lastRule   int
	}
	policies := map[int]*portPolicy{
		relayPort:      {allowIndex: -1, dropIndex: -1, lastRule: -1},
		controllerPort: {allowIndex: -1, dropIndex: -1, lastRule: -1},
	}
	for index, entryValue := range entries {
		entry, ok := entryValue.(map[string]any)
		if !ok {
			continue
		}
		if chain, ok := entry["chain"].(map[string]any); ok && stringValue(chain, "family") == "inet" &&
			stringValue(chain, "table") == "maestro_xray_cdn" {
			if stringValue(chain, "name") != "input" {
				return FirewallState{}, errors.New("relay preflight: unexpected firewall chain")
			}
			chainCount++
			priority, priorityOK := chain["prio"].(float64)
			if chainCount != 1 || stringValue(chain, "type") != "filter" || stringValue(chain, "hook") != "input" ||
				!priorityOK || int(priority) != 0 || stringValue(chain, "policy") != "accept" {
				return FirewallState{}, errors.New("relay preflight: invalid hooked input chain")
			}
		}
		if set, ok := entry["set"].(map[string]any); ok && stringValue(set, "family") == "inet" &&
			stringValue(set, "table") == "maestro_xray_cdn" && stringValue(set, "type") == "ipv4_addr" {
			elements, ok := set["elem"].([]any)
			if !ok {
				return FirewallState{}, errors.New("relay preflight: invalid source set")
			}
			values := make([]string, 0, len(elements))
			for _, element := range elements {
				value, ok := element.(string)
				if !ok {
					return FirewallState{}, errors.New("relay preflight: invalid source set")
				}
				values = append(values, value)
			}
			switch stringValue(set, "name") {
			case "active_origins_18084":
				originSetCount++
				origins = values
			case "controller_source_18443":
				controllerSetCount++
				controllerSources = values
			}
		}
		rule, ok := entry["rule"].(map[string]any)
		if !ok || stringValue(rule, "family") != "inet" || stringValue(rule, "table") != "maestro_xray_cdn" {
			continue
		}
		if stringValue(rule, "chain") != "input" {
			return FirewallState{}, errors.New("relay preflight: unexpected firewall rule chain")
		}
		expressions, ok := rule["expr"].([]any)
		if !ok {
			return FirewallState{}, errors.New("relay preflight: invalid firewall rule")
		}
		port, verdict, valid := classifyManagedFirewallRule(expressions)
		if !valid {
			return FirewallState{}, errors.New("relay preflight: unexpected firewall rule")
		}
		policy, supportedPort := policies[port]
		if !supportedPort {
			return FirewallState{}, errors.New("relay preflight: unexpected firewall port")
		}
		policy.lastRule = index
		if verdict == "accept" {
			policy.allowCount++
			policy.allowIndex = index
		} else {
			policy.dropCount++
			policy.dropIndex = index
		}
	}
	if chainCount != 1 || originSetCount != 1 || controllerSetCount != 1 {
		return FirewallState{}, errors.New("relay preflight: incomplete source firewall")
	}
	for _, policy := range policies {
		if policy.allowCount != 1 || policy.dropCount != 1 || policy.allowIndex < 0 ||
			policy.dropIndex <= policy.allowIndex || policy.dropIndex != policy.lastRule {
			return FirewallState{}, errors.New("relay preflight: incomplete source firewall")
		}
	}
	origins, err := normalizeIPs(origins)
	if err != nil {
		return FirewallState{}, errors.New("relay preflight: invalid source set")
	}
	controllerSources, err = normalizeIPs(controllerSources)
	if err != nil || len(controllerSources) != 1 {
		return FirewallState{}, errors.New("relay preflight: invalid controller source set")
	}
	return FirewallState{ActiveOriginIPs: origins, ControllerSourceIPs: controllerSources}, nil
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
	controllerSources, err := normalizeIPs([]string{config.ControllerSourceIP})
	if err != nil || controllerSources[0] != config.ControllerSourceIP {
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

func classifyManagedFirewallRule(expressions []any) (int, string, bool) {
	if len(expressions) == 2 {
		port, portOK := exactDPortMatch(expressions[0])
		if !portOK || !exactVerdict(expressions[1], "drop") {
			return 0, "", false
		}
		return port, "drop", true
	}
	if len(expressions) != 3 {
		return 0, "", false
	}
	port, portOK := exactDPortMatch(expressions[0])
	expectedSet, supportedPort := map[int]string{
		relayPort:      "active_origins_18084",
		controllerPort: "controller_source_18443",
	}[port]
	if !portOK || !supportedPort || !exactSourceSetMatch(expressions[1], expectedSet) || !exactVerdict(expressions[2], "accept") {
		return 0, "", false
	}
	return port, "accept", true
}

func exactDPortMatch(expression any) (int, bool) {
	match, ok := exactMatch(expression)
	if !ok {
		return 0, false
	}
	payload, ok := exactPayload(match["left"], "tcp", "dport")
	if !ok || payload == nil {
		return 0, false
	}
	right, ok := match["right"].(float64)
	port := int(right)
	if !ok || right != float64(port) {
		return 0, false
	}
	return port, true
}

func exactSourceSetMatch(expression any, setName string) bool {
	match, ok := exactMatch(expression)
	if !ok {
		return false
	}
	if _, ok := exactPayload(match["left"], "ip", "saddr"); !ok {
		return false
	}
	switch right := match["right"].(type) {
	case string:
		return right == "@"+setName
	case map[string]any:
		return len(right) == 1 && stringValue(right, "set") == setName
	default:
		return false
	}
}

func exactMatch(expression any) (map[string]any, bool) {
	statement, ok := expression.(map[string]any)
	if !ok || len(statement) != 1 {
		return nil, false
	}
	match, ok := statement["match"].(map[string]any)
	return match, ok && len(match) == 3 && stringValue(match, "op") == "=="
}

func exactPayload(value any, protocol, field string) (map[string]any, bool) {
	left, ok := value.(map[string]any)
	if !ok || len(left) != 1 {
		return nil, false
	}
	payload, ok := left["payload"].(map[string]any)
	return payload, ok && len(payload) == 2 && stringValue(payload, "protocol") == protocol && stringValue(payload, "field") == field
}

func exactVerdict(expression any, verdict string) bool {
	statement, ok := expression.(map[string]any)
	if !ok || len(statement) != 1 {
		return false
	}
	value, present := statement[verdict]
	return present && value == nil
}
