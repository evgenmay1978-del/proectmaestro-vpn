package subgen

import (
	"encoding/json"
	"errors"
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
		stream := xrayJSONFullStreamSettings{
			Network:  node.Network,
			Security: node.Security,
			TLSSettings: xrayJSONFullTLSSettings{
				ServerName: node.ServerName,
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
		}
		configs = append(configs, xrayJSONFullConfig{
			Remarks: node.Label,
			Log:     xrayJSONLog{LogLevel: "warning"},
			Inbounds: []xrayJSONFullInbound{
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
			},
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
	Network       string                   `json:"network"`
	Security      string                   `json:"security"`
	TLSSettings   xrayJSONFullTLSSettings  `json:"tlsSettings"`
	XHTTPSettings xrayJSONXHTTPSettings    `json:"xhttpSettings"`
}

type xrayJSONFullTLSSettings struct {
	ServerName string `json:"serverName"`
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
