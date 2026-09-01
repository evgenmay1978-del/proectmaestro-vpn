package subgen

import (
	"encoding/json"
	"errors"
)

const maxWhiteListXrayJSONSubscriptionBytes = 64 << 10

var errInvalidWhiteListCountryCode = errors.New("invalid whitelist country code")

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
