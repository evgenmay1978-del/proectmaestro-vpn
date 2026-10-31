package aivpn

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const (
	testServer   = "203.0.113.10:443"
	testClientIP = "10.77.0.2"
	testServerIP = "10.77.0.1"
)

// b64 returns a base64 string of n copies of b (stand-in for a 32-byte key).
func b64(n int, b byte) string {
	raw := make([]byte, n)
	for i := range raw {
		raw[i] = b
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// testKey builds a fully populated, valid connection key.
func testKey(t *testing.T) string {
	t.Helper()
	key, err := Build(Key{
		Server:    testServer,
		ServerKey: b64(32, 1),
		PSK:       b64(32, 2),
		ClientIP:  testClientIP,
		Net: &NetConfig{
			ClientIP:      testClientIP,
			ServerVPNIP:   testServerIP,
			PrefixLen:     24,
			MTU:           1280,
			MDHLen:        20,
			KeepaliveSecs: 8,
		},
	})
	if err != nil {
		t.Fatalf("build test key: %v", err)
	}
	return key
}

func TestBuildParseRoundTrip(t *testing.T) {
	key := testKey(t)
	if !strings.HasPrefix(key, Prefix) {
		t.Fatalf("key %q lacks %q prefix", key, Prefix)
	}
	got, err := Parse(key)
	if err != nil {
		t.Fatalf("parse own key: %v", err)
	}
	if got.Server != testServer || got.ClientIP != testClientIP {
		t.Fatalf("unexpected server/client: %+v", got)
	}
	if got.Net == nil || got.Net.ServerVPNIP != testServerIP || got.Net.PrefixLen != 24 || got.Net.MDHLen != 20 {
		t.Fatalf("unexpected net config: %+v", got.Net)
	}
	// Re-encoding a parsed key must be stable: the panel caches and relays the
	// exact server string, round-tripping proves the payload is complete.
	again, err := Build(got)
	if err != nil {
		t.Fatalf("rebuild parsed key: %v", err)
	}
	if again != key {
		t.Fatalf("round trip changed the key")
	}
}

func TestParseAcceptsBareAndPadded(t *testing.T) {
	key := testKey(t)
	bare := strings.TrimPrefix(key, Prefix)
	for name, in := range map[string]string{
		"bare":     bare,
		"spaces":   "  " + key + " ",
		"padded":   addPadding(bare),
		"prefixed": key,
	} {
		if _, err := Parse(in); err != nil {
			t.Fatalf("%s form rejected: %v", name, err)
		}
	}
}

// addPadding appends '=' so the raw base64url payload is decodable by the
// standard URL decoder too (a hand-copied key may look like that).
func addPadding(s string) string {
	if m := len(s) % 4; m != 0 {
		return s + strings.Repeat("=", 4-m)
	}
	return s
}

func TestParseRejectsBrokenKeys(t *testing.T) {
	valid := testKey(t)
	payload := strings.TrimPrefix(valid, Prefix)
	cases := map[string]string{
		"empty":        "",
		"prefix only":  Prefix,
		"not base64":   Prefix + "!!!not-base64!!!",
		"not json":     Prefix + base64.RawURLEncoding.EncodeToString([]byte("nope")),
		"short pubkey": encode(t, map[string]any{"s": testServer, "k": b64(16, 1), "p": b64(32, 2), "i": testClientIP}),
		"short psk":    encode(t, map[string]any{"s": testServer, "k": b64(32, 1), "p": b64(8, 2), "i": testClientIP}),
		"bad server":   encode(t, map[string]any{"s": "no-port", "k": b64(32, 1), "p": b64(32, 2), "i": testClientIP}),
		"bad port":     encode(t, map[string]any{"s": "203.0.113.10:0", "k": b64(32, 1), "p": b64(32, 2), "i": testClientIP}),
		"no client ip": encode(t, map[string]any{"s": testServer, "k": b64(32, 1), "p": b64(32, 2)}),
		"bad prefix": encode(t, map[string]any{"s": testServer, "k": b64(32, 1), "p": b64(32, 2), "i": testClientIP,
			"n": map[string]any{"client_ip": testClientIP, "server_vpn_ip": testServerIP, "prefix_len": 40}}),
		"ip mismatch": encode(t, map[string]any{"s": testServer, "k": b64(32, 1), "p": b64(32, 2), "i": "10.77.0.9",
			"n": map[string]any{"client_ip": testClientIP, "server_vpn_ip": testServerIP, "prefix_len": 24}}),
		"bad ipv6": encode(t, map[string]any{"s": testServer, "k": b64(32, 1), "p": b64(32, 2), "i": testClientIP,
			"n": map[string]any{"client_ip": testClientIP, "server_vpn_ip": testServerIP, "prefix_len": 24, "ipv6_address": "not-an-ip"}}),
	}
	for name, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
	if _, err := Parse(payload); err != nil {
		// Sanity: the bare payload itself must stay valid.
		t.Fatalf("bare payload rejected: %v", err)
	}
}

func TestBuildRejectsInvalid(t *testing.T) {
	cases := map[string]Key{
		"no server":     {ServerKey: b64(32, 1), PSK: b64(32, 2), ClientIP: testClientIP},
		"no pubkey":     {Server: testServer, PSK: b64(32, 2), ClientIP: testClientIP},
		"no psk":        {Server: testServer, ServerKey: b64(32, 1), ClientIP: testClientIP},
		"no client ip":  {Server: testServer, ServerKey: b64(32, 1), PSK: b64(32, 2)},
		"bad signing":   {Server: testServer, ServerKey: b64(32, 1), PSK: b64(32, 2), ClientIP: testClientIP, Signing: "?!"},
		"mtu too small": {Server: testServer, ServerKey: b64(32, 1), PSK: b64(32, 2), ClientIP: testClientIP, Net: &NetConfig{ClientIP: testClientIP, ServerVPNIP: testServerIP, PrefixLen: 24, MTU: 100}},
		"mdh too big":   {Server: testServer, ServerKey: b64(32, 1), PSK: b64(32, 2), ClientIP: testClientIP, Net: &NetConfig{ClientIP: testClientIP, ServerVPNIP: testServerIP, PrefixLen: 24, MDHLen: 300}},
		"no prefix":     {Server: testServer, ServerKey: b64(32, 1), PSK: b64(32, 2), ClientIP: testClientIP, Net: &NetConfig{ClientIP: testClientIP, ServerVPNIP: testServerIP}},
		"no server vpn": {Server: testServer, ServerKey: b64(32, 1), PSK: b64(32, 2), ClientIP: testClientIP, Net: &NetConfig{ClientIP: testClientIP, PrefixLen: 24}},
	}
	for name, k := range cases {
		if _, err := Build(k); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestParseToleratesUnknownFields(t *testing.T) {
	// Forward compatibility: the protocol may add fields the panel does not
	// model (bd/bu today). Dropping such a key would be a regression.
	in := encode(t, map[string]any{
		"s": testServer, "k": b64(32, 1), "p": b64(32, 2), "i": testClientIP,
		"bd": []any{map[string]any{"host": "x"}},
		"bu": []string{"udp://[2001:db8::1]:443"},
	})
	k, err := Parse(in)
	if err != nil {
		t.Fatalf("key with unknown fields rejected: %v", err)
	}
	if k.Server != testServer {
		t.Fatalf("server lost: %+v", k)
	}
}

func TestKeyStringRedactsSecrets(t *testing.T) {
	key := testKey(t)
	parsed, err := Parse(key)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := parsed.String()
	if strings.Contains(s, parsed.PSK) || strings.Contains(s, parsed.ServerKey) {
		t.Fatalf("String() leaks key material: %s", s)
	}
	if !strings.Contains(s, testServer) {
		t.Fatalf("String() should name the server: %s", s)
	}
}

func TestJSONWireNames(t *testing.T) {
	key := testKey(t)
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, Prefix))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, want := range []string{"s", "k", "p", "i", "n"} {
		if _, ok := m[want]; !ok {
			t.Fatalf("payload lacks wire field %q: %s", want, raw)
		}
	}
	if _, ok := m["sk"]; ok {
		t.Fatalf("empty sk must be omitted, got %s", raw)
	}
	var n map[string]any
	if err := json.Unmarshal(m["n"], &n); err != nil {
		t.Fatalf("unmarshal n: %v", err)
	}
	for _, want := range []string{"client_ip", "server_vpn_ip", "prefix_len", "mtu", "mdh_len", "keepalive_secs"} {
		if _, ok := n[want]; !ok {
			t.Fatalf("net config lacks %q: %v", want, n)
		}
	}
}

// encode marshals m as base64url JSON behind the aivpn:// prefix.
func encode(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(b)
}
