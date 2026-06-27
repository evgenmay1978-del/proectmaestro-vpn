// Package subgen builds the per-customer sing-box config the MaestroVPN TV app
// consumes (the "combined subscription"). It emits all four protocols as
// sing-box outbounds:
//
//   - VLESS/Reality (3x-ui)         — native sing-box outbound
//   - Hysteria2                     — native sing-box outbound
//   - Naive (Caddy forward_proxy)   — native sing-box `naive` outbound
//   - AnyTLS                        — native sing-box outbound
//
// plus a `selector` (manual protocol switch) and a `urltest` (auto failover),
// a `tun` inbound, and a route block that supports per-app split tunnelling.
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

// NaiveCreds is a NaiveProxy (Caddy forward_proxy) user. sing-box has a NATIVE
// `naive` outbound, so no on-device helper is needed.
type NaiveCreds struct {
	Server   string
	Port     int
	Username string
	Password string
	SNI      string
}

// AnyTLSCreds is an AnyTLS user. sing-box has a NATIVE `anytls` outbound (no helper
// needed); the protocol wraps traffic in standard TLS + a padding scheme to defeat the
// TLS-in-TLS fingerprint DPI uses against TLS proxies.
type AnyTLSCreds struct {
	Server   string
	Port     int
	Password string
	SNI      string
	Insecure bool // self-signed cert → true
}

// Customer holds whichever protocols are provisioned for one subscription.
// Any nil field is skipped. VLESS3 (VLESS-Reality on the 3rd node) is kept as its own
// field/tag because each outbound has a fixed tag — a second VLESS endpoint needs a
// distinct tag, not a second "vless".
type Customer struct {
	Name   string
	VLESS  *VLESSCreds
	Hy2    *Hy2Creds
	Naive  *NaiveCreds
	AnyTLS *AnyTLSCreds
	VLESS3 *VLESSCreds // VLESS-Reality on the 3rd node (S3)
	WG     *WGCreds    // AmneziaWG (S3) — gated on the with_awg libbox; ⛔ nil for ALL real customers
}

// WGCreds is one AmneziaWG client (the official amneziawg-go engine, endpoint type "awg").
// Per-customer fields only; the shared obfuscation block (awg* consts) is global and MUST
// byte-match the S3 server awg0.conf [Interface] (/root/.s3wg). ⛔ Only the `with_awg` libbox
// parses an awg endpoint; the PLAIN shipping fleet libbox HARD-FAILS the whole config — so WG
// must stay nil for every real customer until the with_awg libbox is the OTA'd fleet engine
// (canary devices only). See [[amneziawg-app-support]].
type WGCreds struct {
	Server        string // peer (S3) address, e.g. "46.30.42.151"
	Port          int    // peer (S3) UDP port, e.g. 443
	PeerPublicKey string // S3 server public key (base64)
	PrivateKey    string // THIS customer's client private key (base64)
	LocalAddress  string // this customer's tunnel IP, e.g. "10.10.8.2/32"
}

const (
	tagVLESS  = "vless"
	tagVLESS3 = "vless-s3" // VLESS-Reality on the 3rd node
	tagHy2    = "hysteria2"
	tagNaive  = "naive"
	tagAnyTLS = "anytls"
	tagWG     = "awg"    // AmneziaWG endpoint on the 3rd node (S3); official amneziawg-go schema
	tagAuto   = "auto"   // urltest
	tagPick   = "select" // selector (default outbound the tun routes to)
)

// AmneziaWG shared obfuscation block — the SINGLE source of truth for the client side.
// MUST byte-match the S3 server awg0.conf [Interface] (/root/.s3wg); params are NOT
// negotiated on the wire, so any drift breaks every AWG client at once. h1-h4 are STRINGS
// in the official `awg` endpoint schema. i1 (the 2.0 decoy) is CLIENT-ONLY — intentionally
// absent from the server [Interface].
const (
	awgJc   = 4
	awgJmin = 40
	awgJmax = 70
	awgS1   = 86
	awgS2   = 148
	awgH1   = "1148714657"
	awgH2   = "1730977510"
	awgH3   = "1957464804"
	awgH4   = "1310196031"
	awgI1   = `<b 0xc000000001081c3d9a2b><r 64><t>`
)

// RU-direct rule-sets. So the user never has to toggle the VPN, Russian
// services (gosuslugi, banks, mos.ru, Yandex/VK/marketplaces…) route DIRECT,
// bypassing the tunnel. Source: runetfreedom/russia-v2ray-rules-dat `release`
// branch — native sing-box .srs, rebuilt every 6h. `ru-available-only-inside`
// is the domestic-only domain list (NOT a blanket .ru TLD, NOT the proxy-side
// censorship list). geoip-ru is a secondary catch for raw-IP RU apps with no
// SNI. Served DIRECT from our RU-domestic mirror (Yandex Object Storage) and
// fetched with download_detour=direct at startup: GitHub raw is throttled in RU,
// and the old download_detour=select fetched THROUGH the tunnel — which fails
// before the tunnel is up ("Создание службы … context canceled"). An S1 timer
// (maestro-rules-mirror) refreshes the mirror from runetfreedom (rebuilt ~6h).
const (
	ruDomainsURL = "https://storage.yandexcloud.net/maestro-apk/rules/geosite-ru-available-only-inside.srs"
	ruIPURL      = "https://storage.yandexcloud.net/maestro-apk/rules/geoip-ru.srs"

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
	"google.com",     // gemini.google.com, aistudio.google.com, accounts.google.com…
	"googleapis.com", // generativelanguage.googleapis.com = the Gemini API
	"gstatic.com",
	"googleusercontent.com",
	"ggpht.com",
	"googlevideo.com",
	"withgoogle.com",
	"google.dev", // ai.google.dev
	"youtube.com",
	"youtu.be",
	"ytimg.com",
}

// GenerateSingbox renders the customer's sing-box configuration as JSON.
func GenerateSingbox(c Customer) ([]byte, error) {
	outbounds := []map[string]any{}
	var protoTags []string

	if c.VLESS != nil {
		outbounds = append(outbounds, vlessOutbound(c.VLESS, tagVLESS))
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
	if c.AnyTLS != nil {
		outbounds = append(outbounds, anytlsOutbound(c.AnyTLS))
		protoTags = append(protoTags, tagAnyTLS)
	}
	if c.VLESS3 != nil {
		outbounds = append(outbounds, vlessOutbound(c.VLESS3, tagVLESS3))
		protoTags = append(protoTags, tagVLESS3)
	}
	// AmneziaWG goes in the top-level "endpoints" array (NOT outbounds). ⛔ Only the
	// with_awg libbox parses it; the PLAIN fleet libbox HARD-FAILS the whole config → c.WG
	// stays nil for every real customer (canary devices only). Joins protoTags so it enrolls
	// in urltest("auto")/selector("select") like the other protocols.
	var endpoints []map[string]any
	if c.WG != nil {
		endpoints = append(endpoints, awgEndpoint(c.WG))
		protoTags = append(protoTags, tagWG)
	}
	if len(protoTags) == 0 {
		return nil, fmt.Errorf("subgen: customer %q has no protocols", c.Name)
	}

	// auto = urltest over the real protocols; select = manual switch (default auto).
	// NB: urltest health probes dial each outbound's DialContext DIRECTLY and bypass the
	// route block, so listing gstatic in forceProxyDomains does NOT loop the probe (verified).
	// interrupt_exist_connections=true is the key failover knob: without it, after Auto
	// re-picks a healthy leaf (or the user manually switches), already-ESTABLISHED flows stay
	// pinned to the OLD/dead outbound until they close — only new connections move. With it,
	// sing-box migrates live traffic onto the new pick instantly. interval 3m (2026-06-23,
	// was 1m) balances Auto-failover latency against probe wakeups: each tick dials EVERY leaf
	// (4 TLS handshakes), which on weak 1GB Android-TV boxes (Sony/TCL) blocks CPU deep-sleep
	// and burns thermal headroom. 3m cuts that ~67% vs 1m; interrupt_exist_connections keeps
	// live flows moving when it does re-pick, so the only cost is slightly slower detection of
	// a silently-dead leaf on "Авто" — the tunnel never breaks.
	outbounds = append(outbounds,
		map[string]any{
			"type": "urltest", "tag": tagAuto, "outbounds": protoTags,
			"url": "https://www.gstatic.com/generate_204", "interval": "3m", "tolerance": 100,
			"interrupt_exist_connections": true,
		},
		map[string]any{
			"type": "selector", "tag": tagPick,
			"outbounds": append([]string{tagAuto}, protoTags...), "default": tagAuto,
			"interrupt_exist_connections": true,
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
			// Remote .srs rule-sets from our RU-domestic mirror, fetched DIRECT
			// (download_detour=direct) at startup — NOT through the tunnel: the
			// selector isn't connected yet during service init, so a proxied fetch
			// fails ("context canceled") and the whole service refuses to start.
			// format:binary is mandatory for .srs. download_detour is deprecated in
			// sing-box 1.14 / removed in 1.16 → swap for http_client.detour on bump.
			"rule_set": []map[string]any{
				{
					"type": "remote", "tag": tagRUDomains, "format": "binary",
					"url":             ruDomainsURL,
					"download_detour": "direct",
					"update_interval": "24h",
				},
				{
					"type": "remote", "tag": tagRUIP, "format": "binary",
					"url":             ruIPURL,
					"download_detour": "direct",
					"update_interval": "24h",
				},
			},
			"rules": routeRules,
		},
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// awgEndpoint builds the OFFICIAL AmneziaWG endpoint (sing-box 1.14 "endpoints" entry, type
// "awg", amneziawg-go v0.2.15). h1-4 are STRINGS in this schema. Per-customer key + tunnel
// address + the S3 peer; shared awg* obfuscation block (must byte-match S3 awg0.conf; i1
// client-only). allowed_ips must be present+non-empty; mtu 1280 fits the junk train.
func awgEndpoint(w *WGCreds) map[string]any {
	return map[string]any{
		"type": "awg", "tag": tagWG,
		"address":     []string{w.LocalAddress},
		"private_key": w.PrivateKey,
		"mtu":         1280,
		"jc":          awgJc, "jmin": awgJmin, "jmax": awgJmax, "s1": awgS1, "s2": awgS2,
		"h1": awgH1, "h2": awgH2, "h3": awgH3, "h4": awgH4, "i1": awgI1,
		"peers": []map[string]any{{
			"address": w.Server, "port": w.Port, "public_key": w.PeerPublicKey,
			"allowed_ips":                   []string{"0.0.0.0/0", "::/0"},
			"persistent_keepalive_interval": 25,
		}},
	}
}

func vlessOutbound(v *VLESSCreds, tag string) map[string]any {
	o := map[string]any{
		"type": "vless", "tag": tag,
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

func anytlsOutbound(a *AnyTLSCreds) map[string]any {
	return map[string]any{
		"type": "anytls", "tag": tagAnyTLS,
		"server": a.Server, "server_port": a.Port,
		"password": a.Password,
		"tls":      map[string]any{"enabled": true, "server_name": a.SNI, "insecure": a.Insecure},
	}
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
