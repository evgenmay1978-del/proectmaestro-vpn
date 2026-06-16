// Package subgen builds the per-customer sing-box config the MaestroVPN TV app
// consumes (the "combined subscription"). It emits all four protocols as
// sing-box outbounds:
//
//   - VLESS/Reality (3x-ui)         — native sing-box outbound
//   - Hysteria2                     — native sing-box outbound
//   - Naive (Caddy forward_proxy)   — native sing-box `naive` outbound
//   - Mieru                         — socks outbound to a local mieru helper
//
// plus a `selector` (manual protocol switch) and a `urltest` (auto failover),
// a `tun` inbound, and a route block that supports per-app split tunnelling.
// Mieru has no native sing-box outbound, so the app runs the mieru client as a
// local SOCKS helper on the HelperSOCKS port referenced here; Naive does NOT
// need a helper (sing-box dials it natively).
package subgen

import (
	"encoding/json"
	"fmt"
	"net"
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

// NaiveCreds is a NaiveProxy (Caddy forward_proxy) user. sing-box has a NATIVE
// `naive` outbound, so no on-device helper is needed.
type NaiveCreds struct {
	Server   string
	Port     int
	Username string
	Password string
	SNI      string
}

// MieruCreds is a Mieru user. sing-box has NO native mieru outbound, so the app
// runs the mieru client as a local SOCKS5 helper (on HelperSOCKS) that sing-box
// dials; the server fields below configure that helper.
type MieruCreds struct {
	Server      string
	Port        int
	Username    string
	Password    string
	Transport   string // "TCP" or "UDP"
	HelperSOCKS int    // 127.0.0.1:<port> the app's mieru helper listens on
}

// Customer holds whichever protocols are provisioned for one subscription.
// Any nil field is skipped.
type Customer struct {
	Name  string
	VLESS *VLESSCreds
	Hy2   *Hy2Creds
	Naive *NaiveCreds
	Mieru *MieruCreds
}

const (
	tagVLESS = "vless"
	tagHy2   = "hysteria2"
	tagNaive = "naive"
	tagMieru = "mieru"
	tagAuto  = "auto"   // urltest
	tagPick  = "select" // selector (default outbound the tun routes to)
)

// RU-direct rule-sets. So the user never has to toggle the VPN, Russian
// services (gosuslugi, banks, mos.ru, Yandex/VK/marketplaces…) route DIRECT,
// bypassing the tunnel. Source: runetfreedom/russia-v2ray-rules-dat `release`
// branch — native sing-box .srs, rebuilt every 6h. `ru-available-only-inside`
// is the domestic-only domain list (NOT a blanket .ru TLD, NOT the proxy-side
// censorship list). geoip-ru is a secondary catch for raw-IP RU apps with no
// SNI. Both fetched THROUGH the tunnel (download_detour=select) so the URL need
// not be reachable from a Russian ISP in the clear.
const (
	ruDomainsURL = "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geosite/geosite-ru-available-only-inside.srs"
	ruIPURL      = "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geoip/geoip-ru.srs"

	tagRUDomains = "ru-direct-domains"
	tagRUIP      = "ru-direct-ip"
)

// forceProxyDomains are FOREIGN services that must ALWAYS egress through the
// tunnel, never direct. Google serves much of its traffic from Google Global
// Cache nodes that sit on Russian ISP IP ranges — those IPs are in geoip-ru, so
// the RU-direct rule would send Google/Gemini out the RU exit and the service
// then sees a Russian location ("Gemini thinks we're in Russia"). Matching by
// domain (sniffed SNI, before IP) and routing to the selector overrides that.
var forceProxyDomains = []string{
	"google.com",            // gemini.google.com, aistudio.google.com, accounts.google.com…
	"googleapis.com",        // generativelanguage.googleapis.com = the Gemini API
	"gstatic.com",
	"googleusercontent.com",
	"ggpht.com",
	"googlevideo.com",
	"withgoogle.com",
	"google.dev",            // ai.google.dev
	"youtube.com",
	"youtu.be",
	"ytimg.com",
}

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
		outbounds = append(outbounds, naiveOutbound(c.Naive))
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

	// Base route rules. sniff first (gives IP-initiated conns an SNI), then
	// hijack-dns, then private IPs direct.
	routeRules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
		{"ip_is_private": true, "action": "route", "outbound": "direct"},
	}
	// Anti-loop for the Mieru helper. The bundled mieru client runs as a SEPARATE
	// OS process, so its TCP connection to the mita server is NOT one of sing-box's
	// own outbound sockets and is never protect()'d. With strict_route + the tun
	// capturing the app's own package, that connection re-enters the tun and gets
	// routed back to the local mieru socks outbound (final=select) — an infinite
	// loop, so ZERO packets ever reach mita (verified by on-server tcpdump). Pin
	// the mieru server endpoint to a DIRECT route so the helper's carrier traffic
	// egresses the real interface. Native VLESS/Hy2/Naive don't need this (sing-box
	// protect()s their fds, so they already bypass the tun). Placed above the
	// force-proxy / RU-direct rules so it short-circuits first.
	if c.Mieru != nil && c.Mieru.Server != "" {
		routeRules = append(routeRules, mieruDirectRule(c.Mieru.Server))
	}
	routeRules = append(routeRules,
		// Foreign services (Google/Gemini/YouTube) MUST go through the tunnel.
		// Placed ABOVE the RU-direct rules (belt-and-suspenders for the Google
		// Global Cache "Gemini thinks we're in Russia" case).
		map[string]any{
			"domain_suffix": forceProxyDomains,
			"action":        "route", "outbound": tagPick,
		},
		// RU domestic services → DIRECT, matched by DOMAIN (geosite) — no IP
		// resolution needed, so this is immune to DNS poisoning.
		map[string]any{
			"rule_set": []string{tagRUDomains},
			"action":   "route", "outbound": "direct",
		},
		// CRITICAL: resolve everything still unmatched (i.e. foreign domains) via the
		// CLEAN DoT resolver (8.8.8.8 over the tunnel) BEFORE the geoip-ru check below.
		// Otherwise the geoip-ru rule forces sing-box to resolve the domain via
		// default_domain_resolver=local (the Russian ISP resolver), which POISONS
		// blocked sites (instagram/x/facebook…) to a RU/blackhole IP → they match
		// geoip-ru → route DIRECT → stay blocked. (Symptom: "only YouTube works" —
		// because only forceProxyDomains escaped, being matched by domain above.)
		// With a clean resolve, geoip-ru sees the REAL foreign IP and lets them proxy.
		map[string]any{
			"action": "resolve", "server": "google",
		},
		// RU raw-IP apps (no SNI) → DIRECT. Now checks CLEAN IPs, so foreign blocked
		// sites (non-RU IPs) no longer fall through here.
		map[string]any{
			"rule_set": []string{tagRUIP},
			"action":   "route", "outbound": "direct",
		},
	)

	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		// Persist downloaded rule-sets (and DNS rdrc) so the RU-direct lists
		// survive restarts offline — a fresh install fetches once through the
		// tunnel, then matches RU traffic forever after (bbolt + ETag/304).
		"experimental": map[string]any{
			"cache_file": map[string]any{"enabled": true, "store_rdrc": true},
		},
		"dns": map[string]any{
			// sing-box 1.12+ DNS server format. "google" (DoT through the proxy)
			// resolves user traffic; "local" (the system resolver, direct) is used
			// only to bootstrap the proxy server domains (see default_domain_resolver).
			"servers": []map[string]any{
				{"type": "tls", "tag": "google", "server": "8.8.8.8", "detour": tagPick},
				{"type": "local", "tag": "local"},
			},
			"rules": []map[string]any{
				// RU service domains resolve via the system resolver (direct), so
				// the A/AAAA lookup isn't proxied and geoip-ru matches the REAL RU
				// IP — otherwise the lookup egresses the tunnel and RU traffic leaks
				// into the proxy. geoip can't match a domain pre-resolution, so only
				// the DOMAIN set goes here, never the IP set.
				{"rule_set": []string{tagRUDomains}, "action": "route", "server": "local"},
			},
			"final": "google",
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
			// Resolve the proxy SERVER domains (wapmixx.ru, wapmix.duckdns.org…) via
			// the system resolver — DIRECT, pre-tunnel — so connecting to the proxy
			// never loops through itself. This was why connect succeeded but no
			// traffic flowed (the proxy domain couldn't be resolved).
			"default_domain_resolver": "local",
			// Remote .srs rule-sets, fetched THROUGH the tunnel (download_detour =
			// the selector) so the GitHub URL need not be RU-reachable in the clear.
			// format:binary is mandatory for .srs. download_detour is deprecated in
			// sing-box 1.14 / removed in 1.16 → swap for http_client.detour on bump.
			"rule_set": []map[string]any{
				{
					"type": "remote", "tag": tagRUDomains, "format": "binary",
					"url":             ruDomainsURL,
					"download_detour": tagPick,
					"update_interval": "24h",
				},
				{
					"type": "remote", "tag": tagRUIP, "format": "binary",
					"url":             ruIPURL,
					"download_detour": tagPick,
					"update_interval": "24h",
				},
			},
			"rules": routeRules,
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

func naiveOutbound(n *NaiveCreds) map[string]any {
	return map[string]any{
		"type": "naive", "tag": tagNaive,
		"server": n.Server, "server_port": n.Port,
		"username": n.Username, "password": n.Password,
		"tls": map[string]any{"enabled": true, "server_name": n.SNI},
	}
}

func socksOutbound(tag string, port int) map[string]any {
	return map[string]any{
		"type": "socks", "tag": tag,
		"server": "127.0.0.1", "server_port": port, "version": "5",
	}
}

// mieruDirectRule pins the mieru server endpoint to a DIRECT route. The mieru
// helper is a separate OS process whose socket sing-box can't protect(), so its
// carrier connection to mita must bypass the tun or it loops back into the local
// mieru socks outbound. Mieru servers are configured by raw IP (no SNI on the
// wire), so match by ip_cidr; fall back to domain_suffix if ever a hostname.
func mieruDirectRule(server string) map[string]any {
	if ip := net.ParseIP(server); ip != nil {
		cidr := server + "/32"
		if ip.To4() == nil {
			cidr = server + "/128"
		}
		return map[string]any{"ip_cidr": []string{cidr}, "action": "route", "outbound": "direct"}
	}
	return map[string]any{"domain_suffix": []string{server}, "action": "route", "outbound": "direct"}
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
