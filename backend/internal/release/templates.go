package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
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
	Host string `json:"host"`
	Path string `json:"path"`
	Mode string `json:"mode"`
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
		publicInbound.StreamSettings.XHTTPSettings.Mode != "packet-up" {
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

func materializeRuntimeConfig(template []byte, material map[string]string) ([]byte, error) {
	if len(material) != 3 || !safeHost(material["public_host"]) || !safeSecretPath(material["secret_path"]) ||
		!safeRuntimeValue(material["server_decryption"]) {
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
	settings.Decryption = material["server_decryption"]
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, invalid("runtime_material_encode")
	}
	config.Inbounds[0].Settings = settingsJSON
	config.Inbounds[0].StreamSettings.XHTTPSettings.Host = material["public_host"]
	config.Inbounds[0].StreamSettings.XHTTPSettings.Path = material["secret_path"]
	raw, err := json.Marshal(config)
	if err != nil || bytes.Contains(raw, []byte("<RUNTIME_")) {
		return nil, invalid("runtime_config_invalid")
	}
	return raw, nil
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid("json_invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("json_trailing_data")
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(destination); err != nil {
		return invalid("json_encode")
	}
	if !bytes.Equal(bytes.TrimSuffix(canonical.Bytes(), []byte{'\n'}), raw) {
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
	return value != "" && len(value) <= 4096 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\x00\r\n") && !strings.Contains(value, "<RUNTIME_")
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
