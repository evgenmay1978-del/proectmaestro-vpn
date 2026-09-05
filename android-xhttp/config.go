package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxPayloadBytes = 16 << 10

var errInput = errors.New("invalid_input")
var canonicalUUID = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
var dnsLabel = regexp.MustCompile("^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")

// Transport contains only data selected from a fresh server publication. The
// Android caller resolves Address on its current underlying Network first.
// No inbound, route, DNS, socket option, log path or arbitrary Xray JSON is accepted.
type transport struct {
	Schema     int    `json:"schema"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	ServerName string `json:"server_name"`
	Host       string `json:"host"`
	Path       string `json:"path"`
	ClientID   string `json:"client_id"`
	Encryption string `json:"encryption"`
	SocksPort  int    `json:"socks_port"`
	SocksUser  string `json:"socks_user"`
	SocksPass  string `json:"socks_pass"`
}

func parseTransport(raw []byte) (transport, error) {
	var value transport
	if len(raw) == 0 || len(raw) > maxPayloadBytes || !utf8.Valid(raw) {
		return value, errInput
	}
	// All fields are scalar. Reject duplicate keys before decoding the strict DTO.
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return value, errInput
	}
	seen := make(map[string]bool)
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return value, errInput
		}
		seen[key] = true
		var field json.RawMessage
		if d.Decode(&field) != nil || bytes.Equal(field, []byte("null")) {
			return value, errInput
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return value, errInput
	}
	if _, err = d.Token(); err != io.EOF {
		return value, errInput
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || len(seen) != 11 || value.validate() != nil {
		return transport{}, errInput
	}
	return value, nil
}

func (v transport) validate() error {
	ip, err := netip.ParseAddr(v.Address)
	if err != nil || !publicIPv4(ip) || ip.String() != v.Address ||
		v.Schema != 1 || v.Port != 443 || v.Host != v.ServerName ||
		!validDNSName(v.ServerName) || !canonicalUUID.MatchString(v.ClientID) ||
		v.SocksPort < 1024 || v.SocksPort > 65535 ||
		!localCredential(v.SocksUser) || !localCredential(v.SocksPass) ||
		v.SocksUser == v.SocksPass {
		return errInput
	}
	const prefix = "mlkem768x25519plus.native.0rtt."
	if !strings.HasPrefix(v.Encryption, prefix) {
		return errInput
	}
	material := strings.TrimPrefix(v.Encryption, prefix)
	decoded, err := base64.RawURLEncoding.DecodeString(material)
	if err != nil || len(decoded) != 1184 || base64.RawURLEncoding.EncodeToString(decoded) != material {
		return errInput
	}
	// Preserve the literal server path, without allowing query/fragment/userinfo.
	path, err := url.ParseRequestURI(v.Path)
	if err != nil || len(v.Path) < 2 || len(v.Path) > 1024 ||
		!strings.HasPrefix(v.Path, "/") || strings.HasPrefix(v.Path, "//") ||
		path.IsAbs() || path.Host != "" || path.RawQuery != "" || path.Fragment != "" ||
		strings.ContainsAny(v.Path, "?#\\") || strings.Contains(v.Path, "/../") ||
		strings.HasSuffix(v.Path, "/..") {
		return errInput
	}
	for _, ch := range v.Path {
		if ch < 0x21 || ch > 0x7e {
			return errInput
		}
	}
	return nil
}

func publicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return false
	}
	b := ip.As4()
	switch {
	case b[0] == 0, b[0] >= 224:
		return false
	case b[0] == 100 && b[1] >= 64 && b[1] <= 127:
		return false
	case b[0] == 169 && b[1] == 254:
		return false
	case b[0] == 192 && b[1] == 0 && (b[2] == 0 || b[2] == 2):
		return false
	case b[0] == 192 && b[1] == 88 && b[2] == 99:
		return false
	case b[0] == 198 && (b[1] == 18 || b[1] == 19 || (b[1] == 51 && b[2] == 100)):
		return false
	case b[0] == 203 && b[1] == 0 && b[2] == 113:
		return false
	default:
		return true
	}
}

func validDNSName(name string) bool {
	if len(name) == 0 || len(name) > 253 || name != strings.ToLower(name) {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if !dnsLabel.MatchString(part) {
			return false
		}
	}
	return true
}

func localCredential(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) >= 24 && len(decoded) <= 64 &&
		base64.RawURLEncoding.EncodeToString(decoded) == value
}

func (v transport) config(sessionID int64) ([]byte, error) {
	// Mark is an internal generation cookie consumed by protectedDialer. That
	// dialer NEVER applies SO_MARK. Old XHTTP cached clients retain their old
	// cookie and cannot borrow a newer account's socket protector.
	return json.Marshal(map[string]any{
		"log": map[string]any{"access": "none", "error": "none", "loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "maestro-cdn-socks", "listen": "127.0.0.1", "port": v.SocksPort,
			"protocol": "socks",
			"settings": map[string]any{
				// Xray's UDP filter trusts the peer IP after one association.
				// Loopback IP is shared by other apps, so only authenticated TCP is enabled.
				"auth": "password", "udp": false, "ip": "127.0.0.1",
				"accounts": []any{map[string]any{"user": v.SocksUser, "pass": v.SocksPass}},
			},
		}},
		"outbounds": []any{map[string]any{
			"tag": "maestro-cdn", "protocol": "vless",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": v.Address, "port": v.Port,
					"users": []any{map[string]any{"id": v.ClientID, "encryption": v.Encryption}},
				}},
			},
			"streamSettings": map[string]any{
				"network": "xhttp", "security": "tls",
				"sockopt": map[string]any{"mark": sessionID},
				"tlsSettings": map[string]any{
					"serverName": v.ServerName, "alpn": []string{"h2"}, "fingerprint": "firefox",
				},
				"xhttpSettings": map[string]any{
					"host": v.Host, "path": v.Path, "mode": "packet-up",
					"uplinkHTTPMethod": "GET", "uplinkDataPlacement": "body",
					"sessionIDPlacement": "query", "sessionIDKey": "auth", "sessionIDLength": 16,
					"seqPlacement": "query", "seqKey": "chunk_id",
				},
			},
		}},
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules": []any{map[string]any{
				"type": "field", "inboundTag": []string{"maestro-cdn-socks"}, "outboundTag": "maestro-cdn",
			}},
		},
	})
}
