package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const defaultConfigTemplate = `{"log":{"access":"none","error":"/var/log/maestro-xray-cdn/error.log","loglevel":"warning"},"api":{"tag":"api","services":["StatsService","HandlerService"]},"inbounds":[{"listen":"0.0.0.0","port":18081,"protocol":"vless","settings":{"clients":[],"decryption":"<RUNTIME_SERVER_DECRYPTION>"},"streamSettings":{"network":"xhttp","xhttpSettings":{"host":"<RUNTIME_PUBLIC_HOST>","path":"<RUNTIME_SECRET_PATH>","mode":"packet-up"}},"tag":"maestro-cdn-in"},{"listen":"127.0.0.1","port":18082,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"},"streamSettings":{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":"/etc/maestro-xray-cdn/api-mtls/server.crt","keyFile":"/etc/maestro-xray-cdn/api-mtls/server.key"},{"certificateFile":"/etc/maestro-xray-cdn/api-mtls/client-ca.crt","usage":"verify"}],"verifyPeerCertInNames":["maestro-metering-client","maestro-sidecar-agent"]}},"tag":"api"},{"listen":"0.0.0.0","port":18084,"protocol":"vless","settings":{"clients":[{"id":"<RUNTIME_EXIT_S1_CREDENTIAL>","email":"relay:exit-s1"},{"id":"<RUNTIME_EXIT_S2_CREDENTIAL>","email":"relay:exit-s2"},{"id":"<RUNTIME_EXIT_S3_CREDENTIAL>","email":"relay:exit-s3"},{"id":"<RUNTIME_EXIT_S4_CREDENTIAL>","email":"relay:exit-s4"}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"certificates":[{"certificateFile":"/etc/maestro-xray-cdn/relay-tls/server.crt","keyFile":"/etc/maestro-xray-cdn/relay-tls/server.key"}],"alpn":["h2"]}},"tag":"maestro-cdn-exit-in"}],"outbounds":[{"protocol":"freedom","tag":"direct"},{"protocol":"blackhole","tag":"block"},{"protocol":"vless","settings":{"vnext":[{"address":"<RUNTIME_EXIT_S1_ADDRESS>","port":18084,"users":[{"id":"<RUNTIME_EXIT_S1_CREDENTIAL>","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"<RUNTIME_EXIT_S1_SERVER_NAME>","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s1"},{"protocol":"vless","settings":{"vnext":[{"address":"<RUNTIME_EXIT_S2_ADDRESS>","port":18084,"users":[{"id":"<RUNTIME_EXIT_S2_CREDENTIAL>","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"<RUNTIME_EXIT_S2_SERVER_NAME>","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s2"},{"protocol":"vless","settings":{"vnext":[{"address":"<RUNTIME_EXIT_S3_ADDRESS>","port":18084,"users":[{"id":"<RUNTIME_EXIT_S3_CREDENTIAL>","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"<RUNTIME_EXIT_S3_SERVER_NAME>","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s3"},{"protocol":"vless","settings":{"vnext":[{"address":"<RUNTIME_EXIT_S4_ADDRESS>","port":18084,"users":[{"id":"<RUNTIME_EXIT_S4_CREDENTIAL>","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"<RUNTIME_EXIT_S4_SERVER_NAME>","allowInsecure":false,"alpn":["h2"]}},"tag":"exit-s4"}],"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"},{"type":"field","inboundTag":["maestro-cdn-exit-in"],"outboundTag":"direct"},{"type":"field","inboundTag":["maestro-cdn-in"],"user":["regexp:^wl:[^:]+:exit-s1$"],"outboundTag":"exit-s1"},{"type":"field","inboundTag":["maestro-cdn-in"],"user":["regexp:^wl:[^:]+:exit-s2$"],"outboundTag":"exit-s2"},{"type":"field","inboundTag":["maestro-cdn-in"],"user":["regexp:^wl:[^:]+:exit-s3$"],"outboundTag":"exit-s3"},{"type":"field","inboundTag":["maestro-cdn-in"],"user":["regexp:^wl:[^:]+:exit-s4$"],"outboundTag":"exit-s4"},{"type":"field","inboundTag":["maestro-cdn-in"],"outboundTag":"block"}]},"policy":{"system":{"statsInboundUplink":true,"statsInboundDownlink":true}},"stats":{}}`

const defaultSystemdTemplate = `[Unit]
Description=MaestroVPN isolated Xray CDN sidecar (maestro-xray-cdn.service)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=maestro-xray-cdn
Group=maestro-xray-cdn
WorkingDirectory=/opt/maestro-xray-cdn/current
LogsDirectory=maestro-xray-cdn
LogsDirectoryMode=0750
RuntimeDirectory=maestro-xray-cdn-pid
RuntimeDirectoryMode=0755
ExecStartPre=/opt/maestro-xray-cdn/current/xray run -test -config /run/maestro-xray-cdn/config.json
ExecStart=/opt/maestro-xray-cdn/current/xray run -config /run/maestro-xray-cdn/config.json
ExecStartPost=/opt/maestro-xray-cdn-agent/current/maestro-xray-cdn-agent write-xray-pid /run/maestro-xray-cdn-pid/xray.pid ${MAINPID}
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadOnlyPaths=/etc/maestro-xray-cdn/api-mtls /etc/maestro-xray-cdn/relay-tls /run/maestro-xray-cdn /run/maestro-xray-cdn/config.json
ReadWritePaths=/var/log/maestro-xray-cdn

[Install]
WantedBy=multi-user.target
`

const defaultRollbackTemplate = `{"schema_version":1,"fallback_probe_port":18080,"fallback_service":"maestro-cdn-probe.service","active_pointer":"/opt/maestro-xray-cdn/current"}`

func DefaultConfigTemplate() []byte   { return []byte(defaultConfigTemplate) }
func DefaultSystemdTemplate() []byte  { return []byte(defaultSystemdTemplate) }
func DefaultRollbackTemplate() []byte { return []byte(defaultRollbackTemplate) }

type xrayConfig struct {
	Log       xrayLog        `json:"log"`
	API       xrayAPI        `json:"api"`
	Inbounds  []xrayInbound  `json:"inbounds"`
	Outbounds []xrayOutbound `json:"outbounds"`
	Routing   xrayRouting    `json:"routing"`
	Policy    xrayPolicy     `json:"policy"`
	Stats     struct{}       `json:"stats"`
}

type xrayLog struct {
	Access   string `json:"access"`
	Error    string `json:"error"`
	LogLevel string `json:"loglevel"`
}

type xrayAPI struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

type xrayInbound struct {
	Listen         string              `json:"listen"`
	Port           int                 `json:"port"`
	Protocol       string              `json:"protocol"`
	Settings       json.RawMessage     `json:"settings"`
	StreamSettings *xrayStreamSettings `json:"streamSettings,omitempty"`
	Tag            string              `json:"tag"`
}

type xrayVLESSSettings struct {
	Clients    []xrayVLESSClient `json:"clients"`
	Decryption string            `json:"decryption"`
}

type xrayVLESSClient struct {
	ID         string `json:"id"`
	Email      string `json:"email,omitempty"`
	Encryption string `json:"encryption,omitempty"`
}

type xrayAPIInboundSettings struct {
	Address string `json:"address"`
}

type xrayStreamSettings struct {
	Network       string             `json:"network,omitempty"`
	Security      string             `json:"security,omitempty"`
	XHTTPSettings *xrayXHTTPSettings `json:"xhttpSettings,omitempty"`
	TLSSettings   *xrayTLSSettings   `json:"tlsSettings,omitempty"`
}

type xrayXHTTPSettings struct {
	Host                string `json:"host"`
	Path                string `json:"path"`
	Mode                string `json:"mode"`
	UplinkHTTPMethod    string `json:"uplinkHTTPMethod,omitempty"`
	UplinkDataPlacement string `json:"uplinkDataPlacement,omitempty"`
	SessionIDPlacement  string `json:"sessionIDPlacement,omitempty"`
	SessionIDKey        string `json:"sessionIDKey,omitempty"`
	SessionIDLength     int    `json:"sessionIDLength,omitempty"`
	SeqPlacement        string `json:"seqPlacement,omitempty"`
	SeqKey              string `json:"seqKey,omitempty"`
}

type xrayTLSSettings struct {
	Certificates          []xrayTLSCertificate `json:"certificates,omitempty"`
	VerifyPeerCertInNames []string             `json:"verifyPeerCertInNames,omitempty"`
	ServerName            string               `json:"serverName,omitempty"`
	AllowInsecure         *bool                `json:"allowInsecure,omitempty"`
	ALPN                  []string             `json:"alpn,omitempty"`
}

type xrayTLSCertificate struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile,omitempty"`
	Usage           string `json:"usage,omitempty"`
}

type xrayOutbound struct {
	Protocol       string              `json:"protocol"`
	Settings       json.RawMessage     `json:"settings,omitempty"`
	StreamSettings *xrayStreamSettings `json:"streamSettings,omitempty"`
	Tag            string              `json:"tag"`
}

type xrayOutboundSettings struct {
	VNext []xrayVNext `json:"vnext"`
}

type xrayVNext struct {
	Address string            `json:"address"`
	Port    int               `json:"port"`
	Users   []xrayVLESSClient `json:"users"`
}

type xrayRouting struct {
	Rules []xrayRoutingRule `json:"rules"`
}
type xrayRoutingRule struct {
	Type        string   `json:"type"`
	InboundTags []string `json:"inboundTag,omitempty"`
	Users       []string `json:"user,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}
type xrayPolicy struct {
	System xrayPolicySystem `json:"system"`
}
type xrayPolicySystem struct {
	StatsInboundUplink   bool `json:"statsInboundUplink"`
	StatsInboundDownlink bool `json:"statsInboundDownlink"`
}
type rollbackTemplate struct {
	SchemaVersion     int    `json:"schema_version"`
	FallbackProbePort int    `json:"fallback_probe_port"`
	FallbackService   string `json:"fallback_service"`
	ActivePointer     string `json:"active_pointer"`
}

type RuntimeMaterial struct {
	ServerDecryption string               `json:"server_decryption"`
	LocalExitID      string               `json:"local_exit_id,omitempty"`
	RelayRoutes      []RelayRouteMaterial `json:"relay_routes,omitempty"`
}

type RelayRouteMaterial struct {
	ExitID     string `json:"exit_id"`
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
	Credential string `json:"credential"`
}

type runtimeMaterialCommitment struct {
	SchemaVersion int             `json:"schema_version"`
	Purpose       string          `json:"purpose"`
	Material      RuntimeMaterial `json:"material"`
}

type xhttpPresetMetadata struct {
	SessionIDPlacement string `json:"sessionIDPlacement"`
	SessionIDKey       string `json:"sessionIDKey"`
	SessionIDLength    int    `json:"sessionIDLength"`
	SeqPlacement       string `json:"seqPlacement"`
	SeqKey             string `json:"seqKey"`
}

func ValidateConfigTemplate(raw []byte) error {
	if len(raw) == 0 || len(raw) > 1<<20 || !utf8.Valid(raw) || containsForbiddenSecretSyntax(raw) {
		return invalid("config_template_bytes_invalid")
	}
	var config xrayConfig
	if err := decodeCanonicalJSON(raw, &config); err != nil {
		return err
	}
	if config.Log.Access != "none" || config.Log.Error != "/var/log/maestro-xray-cdn/error.log" ||
		config.Log.LogLevel != "warning" || config.API.Tag != "api" ||
		!equalStrings(config.API.Services, []string{"StatsService", "HandlerService"}) || len(config.Inbounds) != 3 ||
		len(config.Outbounds) != 6 || len(config.Routing.Rules) != 7 {
		return invalid("config_boundary_invalid")
	}
	publicInbound := config.Inbounds[0]
	var publicSettings xrayVLESSSettings
	if decodeCanonicalJSON(publicInbound.Settings, &publicSettings) != nil || publicSettings.Clients == nil ||
		publicInbound.Listen != "0.0.0.0" || publicInbound.Port != SidecarPort || publicInbound.Protocol != "vless" ||
		publicInbound.Tag != "maestro-cdn-in" || len(publicSettings.Clients) != 0 ||
		publicSettings.Decryption != "<RUNTIME_SERVER_DECRYPTION>" || publicInbound.StreamSettings == nil ||
		publicInbound.StreamSettings.Network != "xhttp" || publicInbound.StreamSettings.Security != "" ||
		publicInbound.StreamSettings.XHTTPSettings == nil || publicInbound.StreamSettings.TLSSettings != nil ||
		publicInbound.StreamSettings.XHTTPSettings.Host != "<RUNTIME_PUBLIC_HOST>" ||
		publicInbound.StreamSettings.XHTTPSettings.Path != "<RUNTIME_SECRET_PATH>" ||
		publicInbound.StreamSettings.XHTTPSettings.Mode != "packet-up" ||
		!templateXHTTPFieldsEmpty(*publicInbound.StreamSettings.XHTTPSettings) {
		return invalid("config_public_inbound_invalid")
	}
	apiInbound := config.Inbounds[1]
	var apiSettings xrayAPIInboundSettings
	if decodeCanonicalJSON(apiInbound.Settings, &apiSettings) != nil || apiInbound.Listen != "127.0.0.1" ||
		apiInbound.Port != StatsAPIPort || apiInbound.Protocol != "dokodemo-door" || apiInbound.Tag != "api" ||
		apiSettings.Address != "127.0.0.1" || !validAPIMTLS(apiInbound.StreamSettings) {
		return invalid("config_metering_boundary_invalid")
	}
	if !validRelayTemplateInbound(config.Inbounds[2]) || !validRelayTemplateOutbounds(config.Outbounds) ||
		!validRelayRouting(config.Routing.Rules) ||
		!config.Policy.System.StatsInboundUplink || !config.Policy.System.StatsInboundDownlink ||
		bytes.Contains(raw, []byte("18080")) {
		return invalid("config_policy_invalid")
	}
	return nil
}

func RuntimeMaterialSHA256(material RuntimeMaterial) (string, error) {
	if !safeRuntimeValue(material.ServerDecryption) || !validRelayRuntimeMaterial(material) {
		return "", invalid("runtime_material_invalid")
	}
	raw, err := marshalCanonical(runtimeMaterialCommitment{
		SchemaVersion: 1,
		Purpose:       "maestro-xray-cdn-server-decryption",
		Material:      material,
	})
	if err != nil {
		return "", invalid("runtime_material_encode")
	}
	return digestBytes(raw), nil
}

func materializeRuntimeConfig(template []byte, transport controlplane.TransportRelease, committedTransportSHA, committedRuntimeSHA string, material RuntimeMaterial) ([]byte, error) {
	transportSHA, err := TransportSHA256(transport)
	if err != nil || !equalDigest(transportSHA, committedTransportSHA) {
		return nil, invalid("runtime_transport_mismatch")
	}
	runtimeSHA, err := RuntimeMaterialSHA256(material)
	if err != nil || !equalDigest(runtimeSHA, committedRuntimeSHA) {
		return nil, invalid("runtime_material_invalid")
	}
	if err := ValidateConfigTemplate(template); err != nil {
		return nil, err
	}
	var config xrayConfig
	if err := decodeCanonicalJSON(template, &config); err != nil {
		return nil, err
	}
	var settings xrayVLESSSettings
	if err := decodeCanonicalJSON(config.Inbounds[0].Settings, &settings); err != nil {
		return nil, err
	}
	settings.Decryption = material.ServerDecryption
	settingsJSON, err := marshalCanonical(settings)
	if err != nil {
		return nil, invalid("runtime_material_encode")
	}
	config.Inbounds[0].Settings = settingsJSON
	xhttp, err := runtimeXHTTPSettings(transport)
	if err != nil {
		return nil, err
	}
	config.Inbounds[0].StreamSettings.XHTTPSettings = &xhttp
	if err := materializeRelayRuntime(&config, material); err != nil {
		return nil, err
	}
	raw, err := marshalCanonical(config)
	if err != nil || bytes.Contains(bytes.ToLower(raw), []byte("<runtime_")) {
		return nil, invalid("runtime_config_invalid")
	}
	if err := validateRuntimeConfig(raw, transport, committedTransportSHA, committedRuntimeSHA); err != nil {
		return nil, err
	}
	return raw, nil
}

func validateRuntimeConfig(raw []byte, transport controlplane.TransportRelease, committedTransportSHA, committedRuntimeSHA string) error {
	transportSHA, err := TransportSHA256(transport)
	if err != nil || !equalDigest(transportSHA, committedTransportSHA) || len(raw) == 0 || len(raw) > 1<<20 ||
		!utf8.Valid(raw) || containsForbiddenSecretSyntax(raw) || bytes.Contains(bytes.ToLower(raw), []byte("<runtime_")) {
		return invalid("runtime_config_invalid")
	}
	var config xrayConfig
	if err := decodeCanonicalJSON(raw, &config); err != nil {
		return err
	}
	if config.Log.Access != "none" || config.Log.Error != "/var/log/maestro-xray-cdn/error.log" || config.Log.LogLevel != "warning" ||
		config.API.Tag != "api" || !equalStrings(config.API.Services, []string{"StatsService", "HandlerService"}) || len(config.Inbounds) != 3 ||
		len(config.Outbounds) != 6 || len(config.Routing.Rules) != 7 {
		return invalid("runtime_config_boundary_invalid")
	}
	preset := transport.Preset()
	expectedXHTTP, err := runtimeXHTTPSettings(transport)
	if err != nil {
		return err
	}
	publicInbound := config.Inbounds[0]
	var publicSettings xrayVLESSSettings
	if decodeCanonicalJSON(publicInbound.Settings, &publicSettings) != nil || publicSettings.Clients == nil || len(publicSettings.Clients) != 0 ||
		!safeRuntimeValue(publicSettings.Decryption) || publicInbound.Listen != "0.0.0.0" || publicInbound.Port != SidecarPort ||
		publicInbound.Protocol != preset.Protocol || publicInbound.Tag != "maestro-cdn-in" || publicInbound.StreamSettings == nil ||
		publicInbound.StreamSettings.Network != preset.Network || publicInbound.StreamSettings.Security != "" ||
		publicInbound.StreamSettings.XHTTPSettings == nil || publicInbound.StreamSettings.TLSSettings != nil ||
		*publicInbound.StreamSettings.XHTTPSettings != expectedXHTTP {
		return invalid("runtime_public_inbound_invalid")
	}
	apiInbound := config.Inbounds[1]
	var apiSettings xrayAPIInboundSettings
	if decodeCanonicalJSON(apiInbound.Settings, &apiSettings) != nil || apiInbound.Listen != "127.0.0.1" || apiInbound.Port != StatsAPIPort ||
		apiInbound.Protocol != "dokodemo-door" || apiInbound.Tag != "api" || apiSettings.Address != "127.0.0.1" || !validAPIMTLS(apiInbound.StreamSettings) {
		return invalid("runtime_metering_boundary_invalid")
	}
	runtimeMaterial, err := relayRuntimeMaterial(config, publicSettings.Decryption)
	if err != nil {
		return err
	}
	runtimeSHA, err := RuntimeMaterialSHA256(runtimeMaterial)
	if err != nil || !equalDigest(runtimeSHA, committedRuntimeSHA) {
		return invalid("runtime_material_mismatch")
	}
	if !validRelayRouting(config.Routing.Rules) ||
		!config.Policy.System.StatsInboundUplink || !config.Policy.System.StatsInboundDownlink ||
		bytes.Contains(raw, []byte("18080")) {
		return invalid("runtime_policy_invalid")
	}
	return nil
}

var relayCredentialPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func relayExitIDs() []string {
	return []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"}
}

func relayTemplateRoutes() []RelayRouteMaterial {
	routes := make([]RelayRouteMaterial, 0, 4)
	for _, exitID := range relayExitIDs() {
		placeholder := strings.ToUpper(strings.ReplaceAll(exitID, "-", "_"))
		routes = append(routes, RelayRouteMaterial{
			ExitID: exitID, Address: "<RUNTIME_" + placeholder + "_ADDRESS>",
			ServerName: "<RUNTIME_" + placeholder + "_SERVER_NAME>",
			Credential: "<RUNTIME_" + placeholder + "_CREDENTIAL>",
		})
	}
	return routes
}

func validRelayTemplateInbound(inbound xrayInbound) bool {
	if !validRelayInboundBoundary(inbound) {
		return false
	}
	var settings xrayVLESSSettings
	if decodeCanonicalJSON(inbound.Settings, &settings) != nil || settings.Decryption != "none" || len(settings.Clients) != 4 {
		return false
	}
	for index, route := range relayTemplateRoutes() {
		client := settings.Clients[index]
		if client.ID != route.Credential || client.Email != "relay:"+route.ExitID || client.Encryption != "" {
			return false
		}
	}
	return true
}

func validRelayInboundBoundary(inbound xrayInbound) bool {
	if inbound.Listen != "0.0.0.0" || inbound.Port != 18084 || inbound.Protocol != "vless" ||
		inbound.Tag != "maestro-cdn-exit-in" || inbound.StreamSettings == nil {
		return false
	}
	stream := inbound.StreamSettings
	if stream.Network != "tcp" || stream.Security != "tls" || stream.XHTTPSettings != nil || stream.TLSSettings == nil {
		return false
	}
	tlsSettings := stream.TLSSettings
	if len(tlsSettings.Certificates) != 1 || len(tlsSettings.VerifyPeerCertInNames) != 0 || tlsSettings.ServerName != "" ||
		tlsSettings.AllowInsecure != nil || !equalStrings(tlsSettings.ALPN, []string{"h2"}) {
		return false
	}
	certificate := tlsSettings.Certificates[0]
	return certificate.CertificateFile == "/etc/maestro-xray-cdn/relay-tls/server.crt" &&
		certificate.KeyFile == "/etc/maestro-xray-cdn/relay-tls/server.key" && certificate.Usage == ""
}

func validRelayTemplateOutbounds(outbounds []xrayOutbound) bool {
	if !validFixedOutbounds(outbounds) {
		return false
	}
	for index, route := range relayTemplateRoutes() {
		if !validRelayOutbound(outbounds[index+2], route) {
			return false
		}
	}
	return true
}

func validFixedOutbounds(outbounds []xrayOutbound) bool {
	return len(outbounds) == 6 && outbounds[0].Protocol == "freedom" && outbounds[0].Tag == "direct" &&
		len(outbounds[0].Settings) == 0 && outbounds[0].StreamSettings == nil &&
		outbounds[1].Protocol == "blackhole" && outbounds[1].Tag == "block" &&
		len(outbounds[1].Settings) == 0 && outbounds[1].StreamSettings == nil
}

func validRelayOutbound(outbound xrayOutbound, expected RelayRouteMaterial) bool {
	actual, ok := relayRouteFromOutbound(outbound, expected.ExitID)
	return ok && actual == expected
}

func relayRouteFromOutbound(outbound xrayOutbound, exitID string) (RelayRouteMaterial, bool) {
	if outbound.Protocol != "vless" || outbound.Tag != exitID || outbound.StreamSettings == nil {
		return RelayRouteMaterial{}, false
	}
	var settings xrayOutboundSettings
	if decodeCanonicalJSON(outbound.Settings, &settings) != nil || len(settings.VNext) != 1 ||
		len(settings.VNext[0].Users) != 1 || settings.VNext[0].Port != 18084 {
		return RelayRouteMaterial{}, false
	}
	user := settings.VNext[0].Users[0]
	tlsSettings := outbound.StreamSettings.TLSSettings
	if outbound.StreamSettings.Network != "tcp" || outbound.StreamSettings.Security != "tls" ||
		outbound.StreamSettings.XHTTPSettings != nil || tlsSettings == nil || len(tlsSettings.Certificates) != 0 ||
		len(tlsSettings.VerifyPeerCertInNames) != 0 || tlsSettings.AllowInsecure == nil || *tlsSettings.AllowInsecure ||
		!equalStrings(tlsSettings.ALPN, []string{"h2"}) || user.Email != "" || user.Encryption != "none" {
		return RelayRouteMaterial{}, false
	}
	return RelayRouteMaterial{
		ExitID: exitID, Address: settings.VNext[0].Address,
		ServerName: tlsSettings.ServerName, Credential: user.ID,
	}, true
}

func validRelayRouting(rules []xrayRoutingRule) bool {
	if len(rules) != 7 || !validRoute(rules[0], []string{"api"}, nil, "api") ||
		!validRoute(rules[1], []string{"maestro-cdn-exit-in"}, nil, "direct") {
		return false
	}
	for index, exitID := range relayExitIDs() {
		if !validRoute(rules[index+2], []string{"maestro-cdn-in"}, []string{"regexp:^wl:[^:]+:" + exitID + "$"}, exitID) {
			return false
		}
	}
	return validRoute(rules[6], []string{"maestro-cdn-in"}, nil, "block")
}

func validRoute(rule xrayRoutingRule, inboundTags, users []string, outboundTag string) bool {
	return rule.Type == "field" && equalStrings(rule.InboundTags, inboundTags) &&
		equalStrings(rule.Users, users) && rule.OutboundTag == outboundTag
}

func validRelayRuntimeMaterial(material RuntimeMaterial) bool {
	if len(material.RelayRoutes) != 4 || !supportedRelayExit(material.LocalExitID) {
		return false
	}
	loopbacks := 0
	for index, expectedExit := range relayExitIDs() {
		route := material.RelayRoutes[index]
		address := net.ParseIP(route.Address)
		if route.ExitID != expectedExit || address == nil || (!address.IsLoopback() && !address.IsGlobalUnicast()) ||
			!validRelayServerName(route.ServerName) || !relayCredentialPattern.MatchString(route.Credential) {
			return false
		}
		if address.IsLoopback() {
			loopbacks++
			if route.ExitID != material.LocalExitID || route.Address != "127.0.0.1" {
				return false
			}
		}
	}
	return loopbacks == 1
}

func supportedRelayExit(exitID string) bool {
	for _, expected := range relayExitIDs() {
		if exitID == expected {
			return true
		}
	}
	return false
}

func validRelayServerName(value string) bool {
	return safeHost(value) && strings.Contains(value, ".") && net.ParseIP(value) == nil &&
		!strings.HasSuffix(strings.ToLower(value), ".invalid")
}

func materializeRelayRuntime(config *xrayConfig, material RuntimeMaterial) error {
	if config == nil || !validRelayRuntimeMaterial(material) {
		return invalid("runtime_relay_material_invalid")
	}
	routes := material.RelayRoutes
	clients := make([]xrayVLESSClient, 0, 1)
	for _, route := range routes {
		if route.ExitID == material.LocalExitID {
			clients = append(clients, xrayVLESSClient{ID: route.Credential, Email: "relay:" + route.ExitID})
		}
	}
	var inboundSettings xrayVLESSSettings
	if decodeCanonicalJSON(config.Inbounds[2].Settings, &inboundSettings) != nil {
		return invalid("runtime_relay_inbound_invalid")
	}
	inboundSettings.Clients = clients
	encodedInbound, err := marshalCanonical(inboundSettings)
	if err != nil {
		return invalid("runtime_relay_material_encode")
	}
	config.Inbounds[2].Settings = encodedInbound
	for index, route := range routes {
		settings := xrayOutboundSettings{VNext: []xrayVNext{{
			Address: route.Address, Port: 18084,
			Users: []xrayVLESSClient{{ID: route.Credential, Encryption: "none"}},
		}}}
		encoded, err := marshalCanonical(settings)
		if err != nil {
			return invalid("runtime_relay_material_encode")
		}
		config.Outbounds[index+2].Settings = encoded
		config.Outbounds[index+2].StreamSettings.TLSSettings.ServerName = route.ServerName
	}
	return nil
}

func relayRuntimeMaterial(config xrayConfig, serverDecryption string) (RuntimeMaterial, error) {
	if !validRelayInboundBoundary(config.Inbounds[2]) || !validFixedOutbounds(config.Outbounds) {
		return RuntimeMaterial{}, invalid("runtime_relay_boundary_invalid")
	}
	var inboundSettings xrayVLESSSettings
	if decodeCanonicalJSON(config.Inbounds[2].Settings, &inboundSettings) != nil || inboundSettings.Decryption != "none" {
		return RuntimeMaterial{}, invalid("runtime_relay_inbound_invalid")
	}
	routes := make([]RelayRouteMaterial, 0, 4)
	for index, exitID := range relayExitIDs() {
		route, ok := relayRouteFromOutbound(config.Outbounds[index+2], exitID)
		if !ok {
			return RuntimeMaterial{}, invalid("runtime_relay_outbound_invalid")
		}
		routes = append(routes, route)
	}
	if len(inboundSettings.Clients) != 1 {
		return RuntimeMaterial{}, invalid("runtime_relay_credential_invalid")
	}
	localExitID := ""
	localRoute := RelayRouteMaterial{}
	for _, route := range routes {
		if address := net.ParseIP(route.Address); address != nil && address.IsLoopback() {
			localExitID = route.ExitID
			localRoute = route
		}
	}
	client := inboundSettings.Clients[0]
	if client.ID != localRoute.Credential || client.Email != "relay:"+localRoute.ExitID || client.Encryption != "" {
		return RuntimeMaterial{}, invalid("runtime_relay_credential_invalid")
	}
	material := RuntimeMaterial{ServerDecryption: serverDecryption, LocalExitID: localExitID, RelayRoutes: routes}
	if !validRelayRuntimeMaterial(material) {
		return RuntimeMaterial{}, invalid("runtime_relay_material_invalid")
	}
	return material, nil
}

func templateXHTTPFieldsEmpty(settings xrayXHTTPSettings) bool {
	return settings.UplinkHTTPMethod == "" && settings.UplinkDataPlacement == "" &&
		settings.SessionIDPlacement == "" && settings.SessionIDKey == "" && settings.SessionIDLength == 0 &&
		settings.SeqPlacement == "" && settings.SeqKey == ""
}

func runtimeXHTTPSettings(transport controlplane.TransportRelease) (xrayXHTTPSettings, error) {
	preset := transport.Preset()
	var metadata xhttpPresetMetadata
	if decodeCanonicalJSON([]byte(preset.ExtraJSON), &metadata) != nil ||
		preset.UplinkHTTPMethod != "GET" || preset.UplinkDataPlacement != "body" ||
		metadata.SessionIDPlacement != "query" || metadata.SessionIDKey != "auth" || metadata.SessionIDLength != 16 ||
		metadata.SeqPlacement != "query" || metadata.SeqKey != "chunk_id" {
		return xrayXHTTPSettings{}, invalid("runtime_preset_invalid")
	}
	profile := transport.Profile()
	return xrayXHTTPSettings{
		Host: profile.PublicHost, Path: profile.SecretPath, Mode: preset.Mode,
		UplinkHTTPMethod: preset.UplinkHTTPMethod, UplinkDataPlacement: preset.UplinkDataPlacement,
		SessionIDPlacement: metadata.SessionIDPlacement, SessionIDKey: metadata.SessionIDKey,
		SessionIDLength: metadata.SessionIDLength, SeqPlacement: metadata.SeqPlacement, SeqKey: metadata.SeqKey,
	}, nil
}

func ValidateSystemdTemplate(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || containsForbiddenSecretSyntax(raw) ||
		!bytes.Equal(raw, []byte(defaultSystemdTemplate)) || bytes.Contains(raw, []byte("18080")) ||
		bytes.Contains(raw, []byte("/current/config.json")) || !bytes.Contains(raw, []byte(RuntimeConfigPath)) {
		return invalid("systemd_template_invalid")
	}
	return nil
}

func ValidateRollbackTemplate(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || containsForbiddenSecretSyntax(raw) {
		return invalid("rollback_template_bytes_invalid")
	}
	var rollback rollbackTemplate
	if err := decodeCanonicalJSON(raw, &rollback); err != nil {
		return err
	}
	if rollback.SchemaVersion != 1 || rollback.FallbackProbePort != FallbackProbePort ||
		rollback.FallbackService != "maestro-cdn-probe.service" || rollback.ActivePointer != "/opt/maestro-xray-cdn/current" {
		return invalid("rollback_template_invalid")
	}
	return nil
}

func decodeCanonicalJSON(raw []byte, destination any) error {
	if !utf8.Valid(raw) {
		return invalid("json_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid("json_invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("json_trailing_data")
	}
	canonical, err := marshalCanonical(destination)
	if err != nil {
		return invalid("json_encode")
	}
	if !bytes.Equal(canonical, raw) {
		return invalid("json_not_canonical")
	}
	return nil
}
func validAPIMTLS(stream *xrayStreamSettings) bool {
	if stream == nil || stream.Network != "" || stream.Security != "tls" || stream.XHTTPSettings != nil ||
		stream.TLSSettings == nil || len(stream.TLSSettings.Certificates) != 2 ||
		!equalStrings(stream.TLSSettings.VerifyPeerCertInNames, []string{"maestro-metering-client", "maestro-sidecar-agent"}) ||
		stream.TLSSettings.ServerName != "" || stream.TLSSettings.AllowInsecure != nil || len(stream.TLSSettings.ALPN) != 0 {
		return false
	}
	server := stream.TLSSettings.Certificates[0]
	clientCA := stream.TLSSettings.Certificates[1]
	return server.CertificateFile == "/etc/maestro-xray-cdn/api-mtls/server.crt" &&
		server.KeyFile == "/etc/maestro-xray-cdn/api-mtls/server.key" && server.Usage == "" &&
		clientCA.CertificateFile == "/etc/maestro-xray-cdn/api-mtls/client-ca.crt" &&
		clientCA.KeyFile == "" && clientCA.Usage == "verify"
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

func safeHost(value string) bool {
	return value != "" && len(value) <= 253 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, " /\\\t\r\n") && !strings.Contains(value, "<RUNTIME_")
}

func safeSecretPath(value string) bool {
	return strings.HasPrefix(value, "/") && len(value) <= 2048 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\t\r\n") && !strings.Contains(value, "<RUNTIME_")
}

func safeRuntimeValue(value string) bool {
	lower := strings.ToLower(value)
	if !utf8.ValidString(value) || value == "" || len(value) > 4096 ||
		value != strings.TrimSpace(value) || strings.Contains(lower, "<runtime_") {
		return false
	}
	normalized := strings.Map(func(current rune) rune {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			return unicode.ToLower(current)
		}
		return -1
	}, value)
	switch normalized {
	case "none", "placeholder", "changeme", "replaceme", "todo", "tbd", "yourserverdecryption":
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) || current == 0x7f {
			return false
		}
	}
	return true
}
func containsForbiddenSecretSyntax(raw []byte) bool {
	value := strings.ToLower(string(raw))
	for _, marker := range []string{
		"-----begin ", "private key-----", "vless://", "password=", "passwd=", "api_token=", "api_key=",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
