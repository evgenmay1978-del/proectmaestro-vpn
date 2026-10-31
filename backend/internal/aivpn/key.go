// Package aivpn is the panel side of the AIVPN transport: the client connection
// key format (aivpn://<base64url(JSON)>) and the abstraction that decides where
// a subscriber's key comes from and who may receive it.
//
// AIVPN is NOT a sing-box outbound. Its client (libaivpn, Rust) runs inside the
// app's own VpnService, so these credentials never belong in the sing-box JSON
// built by internal/subgen — they ride to the app over GET /sub/<token>/info and
// GET /aivpn/<token>, behind the gates in internal/api.
package aivpn

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Prefix is the URI scheme of an AIVPN connection key. The Rust client accepts
// the bare base64url payload too, and so does Parse.
const Prefix = "aivpn://"

// NetConfig mirrors AIVPN's ClientNetworkConfig
// (crates/aivpn-common/src/network_config.rs; the serde field names are the ones
// below). Values come from the server: the client MUST use mdh_len for mask
// header generation and parsing, so they are relayed verbatim.
type NetConfig struct {
	ClientIP      string `json:"client_ip"`
	ServerVPNIP   string `json:"server_vpn_ip"`
	PrefixLen     int    `json:"prefix_len"`
	MTU           int    `json:"mtu,omitempty"`
	MDHLen        int    `json:"mdh_len,omitempty"`
	KeepaliveSecs int    `json:"keepalive_secs,omitempty"`
	IPv6Address   string `json:"ipv6_address,omitempty"`
	IPv6PrefixLen int    `json:"ipv6_prefix_len,omitempty"`
}

// Key is the decoded payload of an AIVPN connection key. Field names are the
// wire names the Rust client reads: s (server address), k (server public key),
// p (PSK), i (client VPN IP — a legacy mirror of n.client_ip), n (per-client
// network config) and the optional sk/mop parity fields (server signing key and
// mask-operator key).
type Key struct {
	Server    string     `json:"s"`
	ServerKey string     `json:"k"`
	PSK       string     `json:"p"`
	ClientIP  string     `json:"i"`
	Net       *NetConfig `json:"n,omitempty"`
	Signing   string     `json:"sk,omitempty"`
	MaskOp    string     `json:"mop,omitempty"`
}

// Build validates k and encodes it as an aivpn:// connection key. This is the
// issuance primitive: the panel relays keys the AIVPN server produced (see
// Source), while tooling and tests form keys from parts with this function.
func Build(k Key) (string, error) {
	if err := k.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(k)
	if err != nil {
		return "", fmt.Errorf("aivpn: encode key: %w", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Parse decodes a connection key (prefixed or bare) and validates its payload.
// Error messages never echo key material: a key carries the PSK, so it must not
// reach a log line or an HTTP body through an error string.
func Parse(raw string) (Key, error) {
	payload := strings.TrimPrefix(strings.TrimSpace(raw), Prefix)
	if payload == "" {
		return Key{}, errors.New("aivpn: empty connection key")
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		// A hand-copied key may still carry padding: the standard decoder
		// accepts it, the raw one does not.
		b, err = base64.URLEncoding.DecodeString(payload)
		if err != nil {
			return Key{}, errors.New("aivpn: connection key is not base64url")
		}
	}
	var k Key
	if err := json.Unmarshal(b, &k); err != nil {
		return Key{}, errors.New("aivpn: connection key payload is not valid JSON")
	}
	if err := k.Validate(); err != nil {
		return Key{}, err
	}
	return k, nil
}

// Validate checks the payload against what the Rust client accepts: a host:port
// server address, a 32-byte server public key and a 32-byte PSK (both base64
// standard), and at least one client IP with sane network parameters. Unknown
// JSON fields are tolerated on purpose — the protocol has optional fields the
// panel does not model, and rejecting them would break forward compatibility.
func (k Key) Validate() error {
	if err := validateHostPort(k.Server); err != nil {
		return fmt.Errorf("aivpn: key server address: %w", err)
	}
	if err := validateBase64Len("server public key", k.ServerKey, 32); err != nil {
		return err
	}
	if err := validateBase64Len("PSK", k.PSK, 32); err != nil {
		return err
	}
	if k.Signing != "" {
		if _, err := decodeStdBase64(k.Signing); err != nil {
			return errors.New("aivpn: key signing key (sk) is not base64")
		}
	}
	if k.MaskOp != "" {
		if _, err := decodeStdBase64(k.MaskOp); err != nil {
			return errors.New("aivpn: key mask-operator key (mop) is not base64")
		}
	}
	if k.Net == nil {
		return validateIPv4("client ip", k.ClientIP)
	}
	return k.Net.Validate(k.ClientIP)
}

// Validate checks one network-config block. A block that is present must carry
// both IPs and a prefix length: the Rust struct has no serde default for them,
// so a partially filled block would be rejected client-side and the client would
// silently fall back to legacy defaults — not something we want to ship.
func (n *NetConfig) Validate(clientIP string) error {
	if err := validateIPv4("n.client_ip", n.ClientIP); err != nil {
		return err
	}
	if clientIP != "" && clientIP != n.ClientIP {
		return fmt.Errorf("aivpn: key client IP mismatch (i=%q, n.client_ip=%q)", clientIP, n.ClientIP)
	}
	if err := validateIPv4("n.server_vpn_ip", n.ServerVPNIP); err != nil {
		return err
	}
	if n.PrefixLen < 1 || n.PrefixLen > 32 {
		return fmt.Errorf("aivpn: key n.prefix_len must be 1..32, got %d", n.PrefixLen)
	}
	if n.MTU != 0 && (n.MTU < 500 || n.MTU > 65535) {
		return fmt.Errorf("aivpn: key n.mtu must be 0 or 500..65535, got %d", n.MTU)
	}
	if n.MDHLen < 0 || n.MDHLen > 255 {
		return fmt.Errorf("aivpn: key n.mdh_len must be 0..255, got %d", n.MDHLen)
	}
	if n.KeepaliveSecs < 0 || n.KeepaliveSecs > 255 {
		return fmt.Errorf("aivpn: key n.keepalive_secs must be 0..255, got %d", n.KeepaliveSecs)
	}
	if n.IPv6Address != "" {
		ip := net.ParseIP(n.IPv6Address)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("aivpn: key n.ipv6_address is not an IPv6 address: %q", n.IPv6Address)
		}
		if n.IPv6PrefixLen < 0 || n.IPv6PrefixLen > 128 {
			return fmt.Errorf("aivpn: key n.ipv6_prefix_len must be 0..128, got %d", n.IPv6PrefixLen)
		}
	}
	return nil
}

// String implements fmt.Stringer so a Key can never be dumped — together with
// its PSK — into a log line by accident.
func (k Key) String() string {
	return fmt.Sprintf("aivpn.Key{server:%s client:%s}", k.Server, k.ClientIP)
}

// validateHostPort requires the host:port shape the client dials.
func validateHostPort(s string) error {
	if s == "" {
		return errors.New("missing")
	}
	if strings.IndexFunc(s, func(r rune) bool { return r <= ' ' || r == '/' }) >= 0 {
		return errors.New("must be host:port without spaces or slashes")
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return errors.New("must be host:port")
	}
	if strings.TrimSpace(host) == "" {
		return errors.New("empty host")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

// validateIPv4 accepts only IPv4 literals: AIVPN v1 assigns IPv4 tunnel
// addresses (ClientNetworkConfig.client_ip is an Ipv4Addr).
func validateIPv4(label, s string) error {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("aivpn: key %s is not an IPv4 address: %q", label, s)
	}
	return nil
}

// validateBase64Len checks that v is base64 standard (padded or not) decoding to
// exactly want bytes — the shape the client's decode_base64_key enforces.
func validateBase64Len(label, v string, want int) error {
	b, err := decodeStdBase64(v)
	if err != nil {
		return fmt.Errorf("aivpn: key %s is not base64", label)
	}
	if len(b) != want {
		return fmt.Errorf("aivpn: key %s must decode to %d bytes, got %d", label, want, len(b))
	}
	return nil
}

// decodeStdBase64 accepts base64 standard with or without padding.
func decodeStdBase64(v string) ([]byte, error) {
	if v == "" {
		return nil, errors.New("empty")
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(v)
}
