package subgen

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type xraySubscriptionDocument []xraySubscriptionConfig

type xraySubscriptionConfig struct {
	Remarks   string                `json:"remarks"`
	Log       xraySubscriptionLog   `json:"log"`
	Inbounds  []xraySubscriptionIn  `json:"inbounds"`
	Outbounds []xraySubscriptionOut `json:"outbounds"`
	Routing   xraySubscriptionRoute `json:"routing"`
}

type xraySubscriptionLog struct {
	LogLevel string `json:"loglevel"`
}

type xraySubscriptionIn struct {
	Tag      string                 `json:"tag"`
	Listen   string                 `json:"listen"`
	Port     int                    `json:"port"`
	Protocol string                 `json:"protocol"`
	Settings xraySubscriptionInOpts `json:"settings"`
}

type xraySubscriptionInOpts struct {
	Auth string `json:"auth"`
	UDP  bool   `json:"udp"`
}

type xraySubscriptionOut struct {
	Tag            string                     `json:"tag"`
	Protocol       string                     `json:"protocol"`
	Settings       xraySubscriptionOutOpts    `json:"settings"`
	StreamSettings xraySubscriptionStreamOpts `json:"streamSettings"`
}

type xraySubscriptionOutOpts struct {
	VNext []xraySubscriptionVNext `json:"vnext"`
}

type xraySubscriptionVNext struct {
	Address string                 `json:"address"`
	Port    int                    `json:"port"`
	Users   []xraySubscriptionUser `json:"users"`
}

type xraySubscriptionUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
}

type xraySubscriptionStreamOpts struct {
	Network       string                   `json:"network"`
	Security      string                   `json:"security"`
	TLSSettings   xraySubscriptionTLSOpts  `json:"tlsSettings"`
	XHTTPSettings xraySubscriptionXHTTPOps `json:"xhttpSettings"`
}

type xraySubscriptionTLSOpts struct {
	ServerName  string   `json:"serverName"`
	ALPN        []string `json:"alpn"`
	Fingerprint string   `json:"fingerprint"`
}

type xraySubscriptionXHTTPOps struct {
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

type xraySubscriptionRoute struct {
	Rules []xraySubscriptionRule `json:"rules"`
}

type xraySubscriptionRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
}

func decodeXrayJSONSubscription(t *testing.T, rendered []byte) xraySubscriptionDocument {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, rendered); err != nil {
		t.Fatalf("compact rendered subscription: %v", err)
	}
	if !bytes.Equal(rendered, compact.Bytes()) {
		t.Fatalf("renderer returned non-compact JSON: %q", rendered)
	}
	var document xraySubscriptionDocument
	if err := json.Unmarshal(rendered, &document); err != nil {
		t.Fatalf("decode rendered subscription: %v", err)
	}
	return document
}

// Catches the production mutation that omits a required public Xray field or changes its fixed value.
func TestWhiteListXrayJSONSubscriptionGoldenStructure(t *testing.T) {
	node := xhttpLinkNode()
	rendered, err := WhiteListXrayJSONSubscription(node, "RU")
	if err != nil {
		t.Fatalf("WhiteListXrayJSONSubscription: %v", err)
	}
	document := decodeXrayJSONSubscription(t, rendered)
	if len(document) != 1 {
		t.Fatalf("config count=%d, want 1", len(document))
	}
	config := document[0]
	const label = "🇷🇺 RU · MaestroVPN"
	if config.Remarks != label || config.Log.LogLevel != "warning" {
		t.Fatalf("remarks/log=%q/%q, want %q/warning", config.Remarks, config.Log.LogLevel, label)
	}
	if len(config.Inbounds) != 1 {
		t.Fatalf("inbounds=%d, want 1", len(config.Inbounds))
	}
	inbound := config.Inbounds[0]
	if inbound.Tag != "socks-in" || inbound.Listen != "127.0.0.1" || inbound.Port != 10808 || inbound.Protocol != "socks" || inbound.Settings.Auth != "noauth" || inbound.Settings.UDP {
		t.Fatalf("unexpected inbound: %#v", inbound)
	}
	if len(config.Outbounds) != 1 {
		t.Fatalf("outbounds=%d, want 1", len(config.Outbounds))
	}
	outbound := config.Outbounds[0]
	if outbound.Tag != label || outbound.Protocol != "vless" || len(outbound.Settings.VNext) != 1 {
		t.Fatalf("unexpected outbound: %#v", outbound)
	}
	vnext := outbound.Settings.VNext[0]
	if vnext.Address != "cdn.example.invalid" || vnext.Port != 443 || len(vnext.Users) != 1 || vnext.Users[0].ID != "11111111-1111-4111-8111-111111111111" || vnext.Users[0].Encryption != node.Encryption {
		t.Fatalf("unexpected vnext: %#v", vnext)
	}
	stream := outbound.StreamSettings
	if stream.Network != "xhttp" || stream.Security != "tls" || stream.TLSSettings.ServerName != "cdn.example.invalid" || len(stream.TLSSettings.ALPN) != 1 || stream.TLSSettings.ALPN[0] != "h2" || stream.TLSSettings.Fingerprint != "firefox" {
		t.Fatalf("unexpected stream/TLS: %#v", stream)
	}
	if got, want := stream.XHTTPSettings, (xraySubscriptionXHTTPOps{Host: "cdn.example.invalid", Path: "/static/main/video/segment.ts/opaque", Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body", SessionIDPlacement: "query", SessionIDKey: "auth", SessionIDLength: 16, SeqPlacement: "query", SeqKey: "chunk_id"}); got != want {
		t.Fatalf("xhttp=%#v, want %#v", got, want)
	}
	if len(config.Routing.Rules) != 1 {
		t.Fatalf("routing rules=%d, want 1", len(config.Routing.Rules))
	}
	rule := config.Routing.Rules[0]
	if rule.Type != "field" || len(rule.InboundTag) != 1 || rule.InboundTag[0] != "socks-in" || rule.OutboundTag != label {
		t.Fatalf("unexpected routing rule: %#v", rule)
	}
}

// Catches the production mutation that changes Host or SNI when dialing a literal IPv4 edge.
func TestWhiteListXrayJSONSubscriptionLiteralIPv4KeepsDomainHostAndSNI(t *testing.T) {
	node := xhttpLinkNode()
	node.Address = "11.22.33.44"
	rendered, err := WhiteListXrayJSONSubscription(node, "US")
	if err != nil {
		t.Fatalf("WhiteListXrayJSONSubscription: %v", err)
	}
	stream := decodeXrayJSONSubscription(t, rendered)[0].Outbounds[0].StreamSettings
	vnext := decodeXrayJSONSubscription(t, rendered)[0].Outbounds[0].Settings.VNext[0]
	if vnext.Address != "11.22.33.44" || stream.XHTTPSettings.Host != "cdn.example.invalid" || stream.TLSSettings.ServerName != "cdn.example.invalid" {
		t.Fatalf("literal IPv4 changed dial/Host/SNI: %#v %#v", vnext, stream)
	}
}

// Catches the production mutation that fails to retain a valid hostname dial address.
func TestWhiteListXrayJSONSubscriptionHostnameDial(t *testing.T) {
	node := xhttpLinkNode()
	node.Address = "edge.example.invalid"
	rendered, err := WhiteListXrayJSONSubscription(node, "DE")
	if err != nil {
		t.Fatalf("WhiteListXrayJSONSubscription: %v", err)
	}
	if got := decodeXrayJSONSubscription(t, rendered)[0].Outbounds[0].Settings.VNext[0].Address; got != "edge.example.invalid" {
		t.Fatalf("hostname dial address=%q", got)
	}
}

// Catches the production mutation that accepts malformed public country metadata or an invalid node.
func TestWhiteListXrayJSONSubscriptionRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		name    string
		country string
		mutate  func(*WhiteListNode)
	}{
		{name: "lowercase country", country: "ru"},
		{name: "one letter country", country: "R"},
		{name: "three letter country", country: "RUS"},
		{name: "non ASCII country", country: "ЯA"},
		{name: "invalid node", country: "RU", mutate: func(node *WhiteListNode) { node.Mode = "stream-up" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := xhttpLinkNode()
			if test.mutate != nil {
				test.mutate(&node)
			}
			if rendered, err := WhiteListXrayJSONSubscription(node, test.country); err == nil || rendered != nil {
				t.Fatalf("accepted invalid input: rendered=%q err=%v", rendered, err)
			}
		})
	}
}

// Catches the production mutation that serializes internal node labels or release/profile identifiers.
func TestWhiteListXrayJSONSubscriptionDoesNotLeakInternalNodeMetadata(t *testing.T) {
	node := xhttpLinkNode()
	node.Label = "internal label"
	node.EdgeID = "edge-internal-id"
	node.TransportProfileID = "profile-internal-id"
	node.CompatibilityPresetID = "preset-internal-id"
	node.TransportReleaseID = "release-internal-id"
	rendered, err := WhiteListXrayJSONSubscription(node, "FR")
	if err != nil {
		t.Fatalf("WhiteListXrayJSONSubscription: %v", err)
	}
	for _, secret := range []string{"internal label", "edge-internal-id", "profile-internal-id", "preset-internal-id", "release-internal-id", "\"dns\"", "direct", "block", "0.0.0.0"} {
		if strings.Contains(string(rendered), secret) {
			t.Fatalf("renderer leaked internal metadata %q", secret)
		}
	}
}

// Catches the production mutation that regresses the existing share-link bytes.
func TestWhiteListXrayJSONSubscriptionDoesNotChangeShareLinkBytes(t *testing.T) {
	link, err := WhiteListShareLink(xhttpLinkNode())
	if err != nil {
		t.Fatalf("WhiteListShareLink: %v", err)
	}
	want := "vless://11111111-1111-4111-8111-111111111111@cdn.example.invalid:443?alpn=h2&encryption=mlkem768x25519plus.native.0rtt." + strings.Repeat("Wlpa", 394) + "Wlo&extra=%7B%22sessionIDPlacement%22%3A%22query%22%2C%22sessionIDKey%22%3A%22auth%22%2C%22sessionIDLength%22%3A16%2C%22seqPlacement%22%3A%22query%22%2C%22seqKey%22%3A%22chunk_id%22%2C%22uplinkHTTPMethod%22%3A%22GET%22%2C%22uplinkDataPlacement%22%3A%22body%22%7D&fp=firefox&host=cdn.example.invalid&mode=packet-up&path=%2Fstatic%2Fmain%2Fvideo%2Fsegment.ts%2Fopaque&security=tls&sni=cdn.example.invalid&type=xhttp#MaestroVPN%20Yandex%20CDN"
	if link != want {
		t.Fatalf("existing link bytes changed:\n got %q\nwant %q", link, want)
	}
}
