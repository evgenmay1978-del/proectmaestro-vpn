package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const defaultConfigTemplate = `{"log":{"access":"none","error":"/var/log/maestro-xray-cdn/error.log","loglevel":"warning"},"api":{"tag":"api","services":["StatsService"]},"inbounds":[{"listen":"0.0.0.0","port":18081,"protocol":"vless","settings":{"clients":[],"decryption":"<RUNTIME_SERVER_DECRYPTION>"},"streamSettings":{"network":"xhttp","xhttpSettings":{"host":"<RUNTIME_PUBLIC_HOST>","path":"<RUNTIME_SECRET_PATH>","mode":"packet-up"}},"tag":"maestro-cdn-in"},{"listen":"127.0.0.1","port":18082,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"},"streamSettings":{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":"/etc/maestro-xray-cdn/api-mtls/server.crt","keyFile":"/etc/maestro-xray-cdn/api-mtls/server.key"},{"certificateFile":"/etc/maestro-xray-cdn/api-mtls/client-ca.crt","usage":"verify"}],"verifyPeerCertInNames":["maestro-metering-client"]}},"tag":"api"}],"outbounds":[{"protocol":"freedom","tag":"direct"}],"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"}]},"policy":{"system":{"statsInboundUplink":true,"statsInboundDownlink":true}},"stats":{}}`

const defaultSystemdTemplate = `[Unit]
Description=MaestroVPN isolated Xray CDN sidecar (maestro-xray-cdn.service)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=maestro-xray-cdn
Group=maestro-xray-cdn
WorkingDirectory=/opt/maestro-xray-cdn/current
RuntimeDirectory=maestro-xray-cdn
RuntimeDirectoryMode=0750
LogsDirectory=maestro-xray-cdn
LogsDirectoryMode=0750
ExecStartPre=/opt/maestro-xray-cdn/current/xray run -test -config /run/maestro-xray-cdn/config.json
ExecStart=/opt/maestro-xray-cdn/current/xray run -config /run/maestro-xray-cdn/config.json
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadOnlyPaths=/etc/maestro-xray-cdn/api-mtls
ReadWritePaths=/run/maestro-xray-cdn /var/log/maestro-xray-cdn

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
	Clients    []struct{} `json:"clients"`
	Decryption string     `json:"decryption"`
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
	Certificates          []xrayTLSCertificate `json:"certificates"`
	VerifyPeerCertInNames []string             `json:"verifyPeerCertInNames"`
}

type xrayTLSCertificate struct {
	CertificateFile string `json:"certificateFile"`
	KeyFile         string `json:"keyFile,omitempty"`
	Usage           string `json:"usage,omitempty"`
}

type xrayOutbound struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
}

type xrayRouting struct {
	Rules []xrayRoutingRule `json:"rules"`
}
type xrayRoutingRule struct {
	Type        string   `json:"type"`
	InboundTags []string `json:"inboundTag"`
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
	ServerDecryption string `json:"server_decryption"`
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
		!equalStrings(config.API.Services, []string{"StatsService"}) || len(config.Inbounds) != 2 ||
		len(config.Outbounds) != 1 || len(config.Routing.Rules) != 1 {
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
	outbound := config.Outbounds[0]
	rule := config.Routing.Rules[0]
	if outbound.Protocol != "freedom" || outbound.Tag != "direct" || rule.Type != "field" ||
		!equalStrings(rule.InboundTags, []string{"api"}) || rule.OutboundTag != "api" ||
		!config.Policy.System.StatsInboundUplink || !config.Policy.System.StatsInboundDownlink ||
		bytes.Contains(raw, []byte("18080")) || bytes.Contains(raw, []byte("HandlerService")) {
		return invalid("config_policy_invalid")
	}
	return nil
}

func RuntimeMaterialSHA256(material RuntimeMaterial) (string, error) {
	if !safeRuntimeValue(material.ServerDecryption) {
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
		config.API.Tag != "api" || !equalStrings(config.API.Services, []string{"StatsService"}) || len(config.Inbounds) != 2 ||
		len(config.Outbounds) != 1 || len(config.Routing.Rules) != 1 {
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
	runtimeSHA, err := RuntimeMaterialSHA256(RuntimeMaterial{ServerDecryption: publicSettings.Decryption})
	if err != nil || !equalDigest(runtimeSHA, committedRuntimeSHA) {
		return invalid("runtime_material_mismatch")
	}
	apiInbound := config.Inbounds[1]
	var apiSettings xrayAPIInboundSettings
	if decodeCanonicalJSON(apiInbound.Settings, &apiSettings) != nil || apiInbound.Listen != "127.0.0.1" || apiInbound.Port != StatsAPIPort ||
		apiInbound.Protocol != "dokodemo-door" || apiInbound.Tag != "api" || apiSettings.Address != "127.0.0.1" || !validAPIMTLS(apiInbound.StreamSettings) {
		return invalid("runtime_metering_boundary_invalid")
	}
	outbound := config.Outbounds[0]
	rule := config.Routing.Rules[0]
	if outbound.Protocol != "freedom" || outbound.Tag != "direct" || rule.Type != "field" ||
		!equalStrings(rule.InboundTags, []string{"api"}) || rule.OutboundTag != "api" ||
		!config.Policy.System.StatsInboundUplink || !config.Policy.System.StatsInboundDownlink ||
		bytes.Contains(raw, []byte("18080")) || bytes.Contains(raw, []byte("HandlerService")) {
		return invalid("runtime_policy_invalid")
	}
	return nil
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
		!equalStrings(stream.TLSSettings.VerifyPeerCertInNames, []string{"maestro-metering-client"}) {
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
