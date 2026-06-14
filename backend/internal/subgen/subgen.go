// Package subgen builds the per-customer sing-box config the MaestroVPN TV app
// consumes (the "combined subscription"). It emits all four protocols as
// sing-box outbounds:
//
//   - VLESS/Reality (3x-ui)         — native sing-box outbound
//   - Hysteria2                     — native sing-box outbound
//   - Naive (Caddy forward_proxy)   — socks outbound to a local naive helper
//   - Mieru                         — socks outbound to a local mieru helper
//
// plus a `selector` (manual protocol switch) and a `urltest` (auto failover),
// a `tun` inbound, and a route block that supports per-app split tunnelling.
// The app runs the naive/mieru client binaries as local SOCKS helpers on the
// HelperSOCKS ports referenced here.
package subgen

import (
	"encoding/json"
	"fmt"
)

// VLESSCreds is a 3x-ui VLESS+Reality client.
type VLESSCreds struct {
	Server      string
	Port        int
	UUID        string
	Flow        string // e.g. "xtls-rprx-vision"
	SNI         string
	PublicKey   string // Reality public key
	ShortID     string
	Fingerprint string // uTLS fingerprint, e.g. "chrome"
}

// Hy2Creds is a Hysteria2 user.
type Hy2Creds struct {
	Server   string
	Port     int
	User     string
	Pass     string
	SNI      string
	Insecure bool // self-signed cert → true (or pin via PinSHA256)
}

// SOCKSCreds describes a protocol carried by a local helper process exposing a
// SOCKS5 port (Naive, Mieru). The app launches the helper; sing-box dials it.
type SOCKSCreds struct {
	HelperSOCKS int // 127.0.0.1:<port> the app's helper listens on
}

// Customer holds whichever protocols are provisioned for one subscription.
// Any nil field is skipped.
type Customer struct {
	Name  string
	VLESS *VLESSCreds
	Hy2   *Hy2Creds
	Naive *SOCKSCreds
	Mieru *SOCKSCreds
}

const (
	tagVLESS = "vless"
	tagHy2   = "hysteria2"
	tagNaive = "naive"
	tagMieru = "mieru"
	tagAuto  = "auto"   // urltest
	tagPick  = "select" // selector (default outbound the tun routes to)
)

// GenerateSingbox renders the customer's sing-box configuration as JSON.
func GenerateSingbox(c Customer) ([]byte, error) {
	outbounds := []map[string]any{}
	var protoTags []string

	if c.VLESS != nil {
		outbounds = append(outbounds, vlessOutbound(c.VLESS))
		protoTags = append(protoTags, tagVLESS)
	}
	if c.Hy2 != nil {
		outbounds = append(outbounds, hy2Outbound(c.Hy2))
		protoTags = append(protoTags, tagHy2)
	}
	if c.Naive != nil {
		outbounds = append(outbounds, socksOutbound(tagNaive, c.Naive.HelperSOCKS))
		protoTags = append(protoTags, tagNaive)
	}
	if c.Mieru != nil {
		outbounds = append(outbounds, socksOutbound(tagMieru, c.Mieru.HelperSOCKS))
		protoTags = append(protoTags, tagMieru)
	}
	if len(protoTags) == 0 {
		return nil, fmt.Errorf("subgen: customer %q has no protocols", c.Name)
	}

	// auto = urltest over the real protocols; select = manual switch (default auto).
	outbounds = append(outbounds,
		map[string]any{
			"type": "urltest", "tag": tagAuto, "outbounds": protoTags,
			"url": "https://www.gstatic.com/generate_204", "interval": "3m", "tolerance": 100,
		},
		map[string]any{
			"type": "selector", "tag": tagPick,
			"outbounds": append([]string{tagAuto}, protoTags...), "default": tagAuto,
		},
		map[string]any{"type": "direct", "tag": "direct"},
	)

	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{
			"servers": []map[string]any{{"tag": "google", "address": "tls://8.8.8.8"}},
		},
		"inbounds": []map[string]any{{
			"type": "tun", "tag": "tun-in",
			"address":                  []string{"172.19.0.1/30"},
			"auto_route":               true,
			"strict_route":             true,
			"stack":                    "system",
			"endpoint_independent_nat": true,
		}},
		"outbounds": outbounds,
		"route": map[string]any{
			// final → the selector, so the app's protocol pick (or auto) routes all
			// tun traffic. Per-app split tunnelling is applied by the app via the
			// platform allow/disallow package list on the tun inbound.
			"final":                 tagPick,
			"auto_detect_interface": true,
			"rules": []map[string]any{
				{"action": "sniff"},
				{"protocol": "dns", "action": "hijack-dns"},
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func vlessOutbound(v *VLESSCreds) map[string]any {
	o := map[string]any{
		"type": "vless", "tag": tagVLESS,
		"server": v.Server, "server_port": v.Port,
		"uuid": v.UUID, "flow": v.Flow,
		"tls": map[string]any{
			"enabled":     true,
			"server_name": v.SNI,
			"utls":        map[string]any{"enabled": true, "fingerprint": fallback(v.Fingerprint, "chrome")},
			"reality": map[string]any{
				"enabled":    true,
				"public_key": v.PublicKey,
				"short_id":   v.ShortID,
			},
		},
	}
	return o
}

func hy2Outbound(h *Hy2Creds) map[string]any {
	return map[string]any{
		"type": "hysteria2", "tag": tagHy2,
		"server": h.Server, "server_port": h.Port,
		"password": h.User + ":" + h.Pass, // userpass auth: "<user>:<pass>"
		"tls": map[string]any{
			"enabled":     true,
			"server_name": h.SNI,
			"insecure":    h.Insecure,
		},
	}
}

func socksOutbound(tag string, port int) map[string]any {
	return map[string]any{
		"type": "socks", "tag": tag,
		"server": "127.0.0.1", "server_port": port, "version": "5",
	}
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
