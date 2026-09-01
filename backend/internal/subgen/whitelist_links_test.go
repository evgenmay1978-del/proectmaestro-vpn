package subgen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func xhttpLinkNode() WhiteListNode {
	material := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 1184))
	extra := `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id","uplinkHTTPMethod":"GET","uplinkDataPlacement":"body"}`
	return WhiteListNode{
		Protocol: "vless", Network: "xhttp", Address: "cdn.example.invalid", Port: 443,
		TLS: true, ServerName: "cdn.example.invalid", Host: "cdn.example.invalid",
		Path: "/static/main/video/segment.ts/opaque", Mode: "packet-up",
		UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ClientID: "11111111-1111-4111-8111-111111111111",
		Encryption: "mlkem768x25519plus.native.0rtt." + material,
		Security: "tls", ALPN: []string{"h2"}, Fingerprint: "firefox",
		Extra: url.QueryEscape(extra), Label: "БС/Yandex fallback", DomainFallback: true,
	}
}

func TestWhiteListShareLinkMatchesCanonicalXrayXHTTPContract(t *testing.T) {
	node := xhttpLinkNode()
	link, err := WhiteListShareLink(node)
	if err != nil {
		t.Fatalf("WhiteListShareLink: %v", err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	if parsed.Scheme != "vless" || parsed.User.Username() != node.ClientID || parsed.Host != "cdn.example.invalid:443" {
		t.Fatalf("authority=%q user=%q scheme=%q", parsed.Host, parsed.User.Username(), parsed.Scheme)
	}
	query := parsed.Query()
	want := map[string]string{
		"encryption": node.Encryption,
		"security": "tls",
		"type": "xhttp",
		"host": node.Host,
		"sni": node.ServerName,
		"path": node.Path,
		"mode": "packet-up",
	}
	for key, value := range want {
		if query.Get(key) != value {
			t.Errorf("query[%q]=%q, want %q", key, query.Get(key), value)
		}
	}
	decodedExtra, err := url.QueryUnescape(node.Extra)
	if err != nil {
		t.Fatalf("fixture extra: %v", err)
	}
	if query.Get("extra") != decodedExtra || !json.Valid([]byte(query.Get("extra"))) {
		t.Fatalf("extra was not encoded exactly once: %q", query.Get("extra"))
	}
	if strings.Contains(parsed.RawQuery, "%257B") {
		t.Fatalf("extra is double encoded: %s", parsed.RawQuery)
	}
}

func TestAppendWhiteListShareLinkPreservesOrdinaryDecodedPrefix(t *testing.T) {
	ordinary := []byte("vless://ordinary-one\nhysteria2://ordinary-two")
	encoded := base64.StdEncoding.EncodeToString(ordinary)
	node := xhttpLinkNode()
	link, err := WhiteListShareLink(node)
	if err != nil {
		t.Fatal(err)
	}
	augmented, err := AppendWhiteListShareLink(encoded, node)
	if err != nil {
		t.Fatalf("AppendWhiteListShareLink: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(augmented)
	if err != nil {
		t.Fatalf("decode augmented: %v", err)
	}
	want := append(append([]byte(nil), ordinary...), []byte("\n"+link)...)
	if !bytes.Equal(decoded, want) {
		t.Fatalf("decoded augmented document changed ordinary prefix:\n got %q\nwant %q", decoded, want)
	}
	if repeated, err := AppendWhiteListShareLink(augmented, node); err != nil || repeated != augmented {
		t.Fatalf("duplicate append = %q, %v; want byte-exact unchanged", repeated, err)
	}
}

func TestWhiteListShareLinkRejectsMalformedOrDoubleEncodedInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WhiteListNode)
	}{
		{name: "mismatched host", mutate: func(node *WhiteListNode) { node.Host = "other.example.invalid" }},
		{name: "wrong method", mutate: func(node *WhiteListNode) { node.UplinkHTTPMethod = "POST" }},
		{name: "wrong alpn", mutate: func(node *WhiteListNode) { node.ALPN = []string{"http/1.1"} }},
		{name: "double encoded extra", mutate: func(node *WhiteListNode) { node.Extra = url.QueryEscape(node.Extra) }},
		{name: "invalid extra", mutate: func(node *WhiteListNode) { node.Extra = url.QueryEscape(`{"unknown":true}`) }},
		{name: "short encryption", mutate: func(node *WhiteListNode) { node.Encryption = "mlkem768x25519plus.native.0rtt.short" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := xhttpLinkNode()
			test.mutate(&node)
			if link, err := WhiteListShareLink(node); err == nil || link != "" {
				t.Fatalf("WhiteListShareLink accepted invalid node: link=%q err=%v", link, err)
			}
		})
	}
}

func TestAppendWhiteListShareLinkRejectsInvalidOrUnboundedOrdinaryDocument(t *testing.T) {
	node := xhttpLinkNode()
	for _, encoded := range []string{
		"not-base64",
		base64.StdEncoding.EncodeToString(nil),
		base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'a'}, (1<<20)+1)),
		base64.StdEncoding.EncodeToString([]byte("vless://ordinary\n")),
	} {
		if augmented, err := AppendWhiteListShareLink(encoded, node); err == nil || augmented != "" {
			t.Fatalf("AppendWhiteListShareLink accepted invalid ordinary document: len=%d err=%v", len(encoded), err)
		}
	}
}
