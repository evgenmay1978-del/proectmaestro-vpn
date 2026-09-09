package subgen

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const maxWhiteListXrayJSONSubscriptionBytes = 64 << 10

var errInvalidWhiteListCountryCode = errors.New("invalid whitelist country code")

const whiteListXrayJSONOutboundTag = "maestro-xhttp-cdn"

// WhiteListXrayJSONSubscription renders one bounded, native-Xray JSON profile.
func WhiteListXrayJSONSubscription(node WhiteListNode, countryCode string) ([]byte, error) {
	extra, err := validatedWhiteListNodeExtra(node)
	if err != nil {
		return nil, err
	}
	label, err := whiteListXrayJSONLabel(countryCode)
	if err != nil {
		return nil, err
	}
	var xhttp xrayJSONXHTTPSettings
	if err := json.Unmarshal([]byte(extra), &xhttp); err != nil {
		return nil, errInvalidWhiteListNode
	}

	rendered, err := json.Marshal([]xrayJSONConfig{{
		Remarks: label,
		Log:     xrayJSONLog{LogLevel: "warning"},
		Inbounds: []xrayJSONInbound{{
			Tag:      "socks-in",
			Listen:   "127.0.0.1",
			Port:     10808,
			Protocol: "socks",
			Settings: xrayJSONSOCKSSettings{Auth: "noauth", UDP: false},
		}},
		Outbounds: []xrayJSONOutbound{{
			Tag:      label,
			Protocol: "vless",
			Settings: xrayJSONVLESSSettings{VNext: []xrayJSONVNext{{
				Address: node.Address,
				Port:    node.Port,
				Users: []xrayJSONVLESSUser{{
					ID:         node.ClientID,
					Encryption: node.Encryption,
				}},
			}}},
			StreamSettings: xrayJSONStreamSettings{
				Network:  node.Network,
				Security: node.Security,
				TLSSettings: xrayJSONTLSSettings{
					ServerName:  node.ServerName,
					ALPN:        append([]string(nil), node.ALPN...),
					Fingerprint: node.Fingerprint,
				},
				XHTTPSettings: xrayJSONXHTTPSettings{
					Host:                node.Host,
					Path:                node.Path,
					Mode:                node.Mode,
					UplinkHTTPMethod:    xhttp.UplinkHTTPMethod,
					UplinkDataPlacement: xhttp.UplinkDataPlacement,
					SessionIDPlacement:  xhttp.SessionIDPlacement,
					SessionIDKey:        xhttp.SessionIDKey,
					SessionIDLength:     xhttp.SessionIDLength,
					SeqPlacement:        xhttp.SeqPlacement,
					SeqKey:              xhttp.SeqKey,
				},
			},
		}},
		Routing: xrayJSONRouting{Rules: []xrayJSONRoutingRule{{
			Type:        "field",
			InboundTag:  []string{"socks-in"},
			OutboundTag: label,
		}}},
	}})
	if err != nil {
		return nil, errInvalidWhiteListNode
	}
	if len(rendered) > maxWhiteListXrayJSONSubscriptionBytes {
		return nil, errWhiteListSubscriptionTooLarge
	}
	return rendered, nil
}

// WhiteListXrayJSONSubscriptions renders the paid CDN nodes as independent
// full Xray configurations. This is the representation used by Incy/Happ;
// each array element is imported as one selectable server.
func WhiteListXrayJSONSubscriptions(nodes []WhiteListNode) ([]byte, error) {
	if len(nodes) == 0 || len(nodes) > 16 {
		return nil, errInvalidWhiteListNode
	}
	configs, err := whiteListXrayJSONConfigs(nodes)
	if err != nil {
		return nil, err
	}
	return marshalWhiteListXrayJSONConfigs(configs)
}

// WhiteListCombinedXrayJSONSubscription keeps every ordinary VLESS/Reality
// profile from the already accepted legacy subscription and appends the paid
// CDN profiles. Protocols that are not Xray full-config outbounds remain in the
// unchanged universal links representation.
func WhiteListCombinedXrayJSONSubscription(ordinary string, nodes []WhiteListNode) ([]byte, error) {
	ordinaryConfigs, err := ordinaryVLESSXrayJSONConfigs(ordinary)
	if err != nil {
		return nil, err
	}
	cdnConfigs, err := whiteListXrayJSONConfigs(nodes)
	if err != nil {
		return nil, err
	}
	configs := append(ordinaryConfigs, cdnConfigs...)
	if len(configs) == 0 || len(configs) > 32 {
		return nil, errInvalidOrdinarySubscription
	}
	labels := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		if _, exists := labels[config.Remarks]; exists {
			return nil, errInvalidOrdinarySubscription
		}
		labels[config.Remarks] = struct{}{}
	}
	return marshalWhiteListXrayJSONConfigs(configs)
}

func whiteListXrayJSONConfigs(nodes []WhiteListNode) ([]xrayJSONFullConfig, error) {
	if len(nodes) > 16 {
		return nil, errInvalidWhiteListNode
	}
	configs := make([]xrayJSONFullConfig, 0, len(nodes))
	labels := make(map[string]struct{}, len(nodes))
	clientIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if _, exists := labels[node.Label]; exists {
			return nil, errInvalidWhiteListNode
		}
		if _, exists := clientIDs[node.ClientID]; exists {
			return nil, errInvalidWhiteListNode
		}
		if !validWhiteListBatchLabel(node.Label, node) {
			return nil, errInvalidWhiteListNode
		}
		extra, err := validatedWhiteListNodeExtra(node)
		if err != nil {
			return nil, err
		}
		var xhttp xrayJSONXHTTPSettings
		if err := json.Unmarshal([]byte(extra), &xhttp); err != nil {
			return nil, errInvalidWhiteListNode
		}
		tlsSettings := xrayJSONFullTLSSettings{
			ServerName:  node.ServerName,
			ALPN:        append([]string(nil), node.ALPN...),
			Fingerprint: node.Fingerprint,
		}
		xhttpSettings := xrayJSONXHTTPSettings{
			Host:                node.Host,
			Path:                node.Path,
			Mode:                node.Mode,
			UplinkHTTPMethod:    xhttp.UplinkHTTPMethod,
			UplinkDataPlacement: xhttp.UplinkDataPlacement,
			SessionIDPlacement:  xhttp.SessionIDPlacement,
			SessionIDKey:        xhttp.SessionIDKey,
			SessionIDLength:     xhttp.SessionIDLength,
			SeqPlacement:        xhttp.SeqPlacement,
			SeqKey:              xhttp.SeqKey,
		}
		stream := xrayJSONFullStreamSettings{
			Network:       node.Network,
			Security:      node.Security,
			TLSSettings:   &tlsSettings,
			XHTTPSettings: &xhttpSettings,
		}
		configs = append(configs, xrayJSONFullConfig{
			Remarks:  node.Label,
			Log:      xrayJSONLog{LogLevel: "warning"},
			Inbounds: xrayJSONClientInbounds(),
			Outbounds: []xrayJSONFullOutbound{
				{
					Tag: whiteListXrayJSONOutboundTag, Protocol: "vless",
					Settings: xrayJSONVLESSSettings{VNext: []xrayJSONVNext{{
						Address: node.Address,
						Port:    node.Port,
						Users: []xrayJSONVLESSUser{{
							ID: node.ClientID, Encryption: node.Encryption,
						}},
					}}},
					StreamSettings: &stream,
				},
				{Tag: "direct", Protocol: "freedom"},
				{Tag: "block-quic", Protocol: "blackhole"},
			},
			Routing: xrayJSONFullRouting{
				DomainStrategy: "AsIs",
				Rules: []xrayJSONFullRoutingRule{
					{Type: "field", Network: "udp", Port: "443", OutboundTag: "block-quic"},
					{Type: "field", Network: "tcp,udp", OutboundTag: whiteListXrayJSONOutboundTag},
				},
			},
		})
		labels[node.Label] = struct{}{}
		clientIDs[node.ClientID] = struct{}{}
	}
	return configs, nil
}

func ordinaryVLESSXrayJSONConfigs(encoded string) ([]xrayJSONFullConfig, error) {
	decoded, err := decodeOrdinaryWhiteListSubscription(encoded)
	if err != nil {
		return nil, err
	}
	configs := make([]xrayJSONFullConfig, 0, 3)
	labels := make(map[string]struct{})
	identities := make(map[string]struct{})
	for _, line := range strings.Split(string(decoded), "\n") {
		if !strings.HasPrefix(strings.ToLower(line), "vless://") {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil || !strings.EqualFold(parsed.Scheme, "vless") || parsed.User == nil || parsed.Path != "" {
			return nil, errInvalidOrdinarySubscription
		}
		clientID := parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword || !validCanonicalUUID(clientID) {
			return nil, errInvalidOrdinarySubscription
		}
		address := parsed.Hostname()
		port, portErr := strconv.Atoi(parsed.Port())
		query := parsed.Query()
		if portErr != nil || port <= 0 || port > 65535 || !validWhiteListDialAddress(address) ||
			!singleXrayQueryValues(query, "encryption", "security", "sni", "fp", "pbk", "sid", "type", "flow") ||
			query.Get("encryption") != "none" || query.Get("security") != "reality" || query.Get("type") != "tcp" ||
			!validWhiteListServerName(query.Get("sni")) || query.Get("fp") == "" || query.Get("pbk") == "" || query.Get("sid") == "" ||
			len(query.Get("fp")) > 64 || len(query.Get("pbk")) > 1024 || len(query.Get("sid")) > 64 ||
			(query.Get("flow") != "" && query.Get("flow") != "xtls-rprx-vision") ||
			!validWhiteListPublicLabelSyntax(parsed.Fragment) {
			return nil, errInvalidOrdinarySubscription
		}
		identity := strings.ToLower(address) + ":" + strconv.Itoa(port)
		if _, exists := labels[parsed.Fragment]; exists {
			return nil, errInvalidOrdinarySubscription
		}
		if _, exists := identities[identity]; exists {
			return nil, errInvalidOrdinarySubscription
		}
		reality := xrayJSONRealitySettings{
			ServerName: query.Get("sni"), Fingerprint: query.Get("fp"),
			PublicKey: query.Get("pbk"), ShortID: query.Get("sid"),
		}
		stream := xrayJSONFullStreamSettings{Network: "tcp", Security: "reality", RealitySettings: &reality}
		configs = append(configs, xrayJSONFullConfig{
			Remarks:  parsed.Fragment,
			Log:      xrayJSONLog{LogLevel: "warning"},
			Inbounds: xrayJSONClientInbounds(),
			Outbounds: []xrayJSONFullOutbound{
				{
					Tag: "maestro-vless", Protocol: "vless",
					Settings: xrayJSONVLESSSettings{VNext: []xrayJSONVNext{{
						Address: address, Port: port,
						Users: []xrayJSONVLESSUser{{ID: clientID, Encryption: "none", Flow: query.Get("flow")}},
					}}},
					StreamSettings: &stream,
				},
				{Tag: "direct", Protocol: "freedom"},
				{Tag: "block-quic", Protocol: "blackhole"},
			},
			Routing: xrayJSONFullRouting{
				DomainStrategy: "AsIs",
				Rules: []xrayJSONFullRoutingRule{
					{Type: "field", Network: "udp", Port: "443", OutboundTag: "block-quic"},
					{Type: "field", Network: "tcp,udp", OutboundTag: "maestro-vless"},
				},
			},
		})
		labels[parsed.Fragment] = struct{}{}
		identities[identity] = struct{}{}
	}
	if len(configs) == 0 || len(configs) > 16 {
		return nil, errInvalidOrdinarySubscription
	}
	return configs, nil
}

func singleXrayQueryValues(values url.Values, allowed ...string) bool {
	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowedKeys[key]; !ok || len(entries) != 1 {
			return false
		}
	}
	return true
}

func xrayJSONClientInbounds() []xrayJSONFullInbound {
	return []xrayJSONFullInbound{
		{
			Tag: "socks", Protocol: "socks", Listen: "127.0.0.1", Port: 10808,
			Settings: xrayJSONSOCKSSettings{Auth: "noauth", UDP: true},
			Sniffing: &xrayJSONSniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
		},
		{
			Tag: "http", Protocol: "http", Listen: "127.0.0.1", Port: 10809,
			Settings: xrayJSONHTTPSettings{AllowTransparent: false},
			Sniffing: &xrayJSONSniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
		},
	}
}

func marshalWhiteListXrayJSONConfigs(configs []xrayJSONFullConfig) ([]byte, error) {
	rendered, err := json.Marshal(configs)
	if err != nil {
		return nil, errInvalidWhiteListNode
	}
	if len(rendered) > maxWhiteListXrayJSONSubscriptionBytes {
		return nil, errWhiteListSubscriptionTooLarge
	}
	return rendered, nil
}

func whiteListXrayJSONLabel(countryCode string) (string, error) {
	if len(countryCode) != 2 || countryCode[0] < 'A' || countryCode[0] > 'Z' || countryCode[1] < 'A' || countryCode[1] > 'Z' {
		return "", errInvalidWhiteListCountryCode
	}
	return string([]rune{0x1F1E6 + rune(countryCode[0]-'A'), 0x1F1E6 + rune(countryCode[1]-'A')}) + " " + countryCode + " · MaestroVPN", nil
}

type xrayJSONConfig struct {
	Remarks   string             `json:"remarks"`
	Log       xrayJSONLog        `json:"log"`
	Inbounds  []xrayJSONInbound  `json:"inbounds"`
	Outbounds []xrayJSONOutbound `json:"outbounds"`
	Routing   xrayJSONRouting    `json:"routing"`
}

type xrayJSONLog struct {
	LogLevel string `json:"loglevel"`
}

type xrayJSONInbound struct {
	Tag      string                `json:"tag"`
	Listen   string                `json:"listen"`
	Port     int                   `json:"port"`
	Protocol string                `json:"protocol"`
	Settings xrayJSONSOCKSSettings `json:"settings"`
}

type xrayJSONSOCKSSettings struct {
	Auth string `json:"auth"`
	UDP  bool   `json:"udp"`
}

type xrayJSONOutbound struct {
	Tag            string                 `json:"tag"`
	Protocol       string                 `json:"protocol"`
	Settings       xrayJSONVLESSSettings  `json:"settings"`
	StreamSettings xrayJSONStreamSettings `json:"streamSettings"`
}

type xrayJSONVLESSSettings struct {
	VNext []xrayJSONVNext `json:"vnext"`
}

type xrayJSONVNext struct {
	Address string              `json:"address"`
	Port    int                 `json:"port"`
	Users   []xrayJSONVLESSUser `json:"users"`
}

type xrayJSONVLESSUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow,omitempty"`
}

type xrayJSONStreamSettings struct {
	Network       string                `json:"network"`
	Security      string                `json:"security"`
	TLSSettings   xrayJSONTLSSettings   `json:"tlsSettings"`
	XHTTPSettings xrayJSONXHTTPSettings `json:"xhttpSettings"`
}

type xrayJSONTLSSettings struct {
	ServerName  string   `json:"serverName"`
	ALPN        []string `json:"alpn"`
	Fingerprint string   `json:"fingerprint"`
}

type xrayJSONXHTTPSettings struct {
	Host                string `json:"host"`
	Path                string `json:"path"`
	Mode                string `json:"mode"`
	UplinkHTTPMethod    string `json:"uplinkHTTPMethod"`
	UplinkDataPlacement string `json:"uplinkDataPlacement"`
	SessionIDPlacement  string `json:"sessionIDPlacement"`
	SessionIDKey        string `json:"sessionIDKey"`
	SessionIDLength     int    `json:"sessionIDLength"`
	SeqPlacement        string `json:"seqPlacement"`
	SeqKey              string `json:"seqKey"`
	Extra               *xrayJSONXHTTPExtra `json:"extra,omitempty"`
}

type xrayJSONXHTTPExtra struct {
	NoGRPCHeader          bool             `json:"noGRPCHeader"`
	NoSSEHeader           bool             `json:"noSSEHeader"`
	SCMaxBufferedPosts    int              `json:"scMaxBufferedPosts"`
	SCMaxEachPostBytes    int              `json:"scMaxEachPostBytes"`
	SCMinPostsIntervalMS  string           `json:"scMinPostsIntervalMs"`
	SeqKey                string           `json:"seqKey"`
	SeqPlacement          string           `json:"seqPlacement"`
	ServerMaxHeaderBytes  int              `json:"serverMaxHeaderBytes"`
	SessionIDKey          string           `json:"sessionIDKey"`
	SessionIDLength       string           `json:"sessionIDLength"`
	SessionIDPlacement    string           `json:"sessionIDPlacement"`
	SessionKey            string           `json:"sessionKey"`
	SessionPlacement      string           `json:"sessionPlacement"`
	UplinkDataPlacement     string            `json:"uplinkDataPlacement"`
	UplinkHTTPMethod      string           `json:"uplinkHTTPMethod"`
	XPaddingBytes         string           `json:"xPaddingBytes"`
	XPaddingHeader        string           `json:"xPaddingHeader"`
	XPaddingKey           string           `json:"xPaddingKey"`
	XPaddingMethod        string           `json:"xPaddingMethod"`
	XPaddingObfsMode      bool             `json:"xPaddingObfsMode"`
	XPaddingPlacement     string           `json:"xPaddingPlacement"`
	XMux                   xrayJSONXHTTPXMux `json:"xmux"`
}

type xrayJSONXHTTPXMux struct {
	CMaxReuseTimes   string `json:"cMaxReuseTimes"`
	HKeepAlivePeriod int    `json:"hKeepAlivePeriod"`
	HMaxRequestTimes string `json:"hMaxRequestTimes"`
	HMaxReusableSecs string `json:"hMaxReusableSecs"`
	MaxConnections   string `json:"maxConnections"`
}

func compatibleXHTTPExtra(source xrayJSONXHTTPSettings) *xrayJSONXHTTPExtra {
	return &xrayJSONXHTTPExtra{
		NoGRPCHeader: true, NoSSEHeader: true,
		SCMaxBufferedPosts: 100, SCMaxEachPostBytes: 3000000, SCMinPostsIntervalMS: "30",
		SeqKey: source.SeqKey, SeqPlacement: source.SeqPlacement, ServerMaxHeaderBytes: 32768,
		SessionIDKey: source.SessionIDKey, SessionIDLength: "16-32", SessionIDPlacement: source.SessionIDPlacement,
		SessionKey: source.SessionIDKey, SessionPlacement: source.SessionIDPlacement,
		UplinkDataPlacement: source.UplinkDataPlacement, UplinkHTTPMethod: source.UplinkHTTPMethod,
		XPaddingBytes: "50-150", XPaddingHeader: "X-Padding", XPaddingKey: "x_padding",
		XPaddingMethod: "tokenish", XPaddingObfsMode: true, XPaddingPlacement: "header",
		XMux: xrayJSONXHTTPXMux{
			CMaxReuseTimes: "0", HKeepAlivePeriod: 0, HMaxRequestTimes: "0",
			HMaxReusableSecs: "0", MaxConnections: "1",
		},
	}
}

type xrayJSONRouting struct {
	Rules []xrayJSONRoutingRule `json:"rules"`
}

type xrayJSONRoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
}

type xrayJSONFullConfig struct {
	Remarks   string                 `json:"remarks"`
	Log       xrayJSONLog            `json:"log"`
	Inbounds  []xrayJSONFullInbound  `json:"inbounds"`
	Outbounds []xrayJSONFullOutbound `json:"outbounds"`
	Routing   xrayJSONFullRouting    `json:"routing"`
}

type xrayJSONFullInbound struct {
	Tag      string            `json:"tag"`
	Protocol string            `json:"protocol"`
	Listen   string            `json:"listen"`
	Port     int               `json:"port"`
	Settings any               `json:"settings"`
	Sniffing *xrayJSONSniffing `json:"sniffing,omitempty"`
}

type xrayJSONHTTPSettings struct {
	AllowTransparent bool `json:"allowTransparent"`
}

type xrayJSONSniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride"`
}

type xrayJSONFullOutbound struct {
	Tag            string                      `json:"tag"`
	Protocol       string                      `json:"protocol"`
	Settings       any                         `json:"settings,omitempty"`
	StreamSettings *xrayJSONFullStreamSettings `json:"streamSettings,omitempty"`
}

type xrayJSONFullStreamSettings struct {
	Network         string                   `json:"network"`
	Security        string                   `json:"security"`
	TLSSettings     *xrayJSONFullTLSSettings `json:"tlsSettings,omitempty"`
	XHTTPSettings   *xrayJSONXHTTPSettings   `json:"xhttpSettings,omitempty"`
	RealitySettings *xrayJSONRealitySettings `json:"realitySettings,omitempty"`
}

type xrayJSONFullTLSSettings struct {
	ServerName  string   `json:"serverName"`
	ALPN        []string `json:"alpn"`
	Fingerprint string   `json:"fingerprint"`
}

type xrayJSONRealitySettings struct {
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
}

type xrayJSONFullRouting struct {
	DomainStrategy string                    `json:"domainStrategy"`
	Rules          []xrayJSONFullRoutingRule `json:"rules"`
}

type xrayJSONFullRoutingRule struct {
	Type        string `json:"type"`
	Network     string `json:"network,omitempty"`
	Port        string `json:"port,omitempty"`
	OutboundTag string `json:"outboundTag"`
}
