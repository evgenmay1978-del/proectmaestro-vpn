package subgen

import (
	"bytes"
	"encoding/json"
	"testing"
)

func sampleCustomer() Customer {
	return Customer{
		Name: "cust1",
		VLESS: &VLESSCreds{
			Server: "wapmixx.ru", Port: 443, UUID: "uuid-1", Flow: "xtls-rprx-vision",
			SNI: "yahoo.com", PublicKey: "pubkey", ShortID: "ab12",
		},
		Hy2:    &Hy2Creds{Server: "wapmix.duckdns.org", Port: 8443, User: "cust1", Pass: "pw", SNI: "wapmix.duckdns.org", Insecure: true},
		Naive:  &NaiveCreds{Server: "wapmixx.ru", Port: 443, Username: "mtv_cust1", Password: "np", SNI: "naive.example"},
		AnyTLS: &AnyTLSCreds{Server: "wapmixx.ru", Port: 8444, Password: "atpw", SNI: "wapmixx.ru", Insecure: true},
	}
}

func TestGenerateSingboxAllProtocols(t *testing.T) {
	raw, err := GenerateSingbox(sampleCustomer())
	if err != nil {
		t.Fatalf("GenerateSingbox: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	outs, _ := cfg["outbounds"].([]any)
	tags := map[string]string{} // tag -> type
	for _, o := range outs {
		m := o.(map[string]any)
		tags[m["tag"].(string)] = m["type"].(string)
	}
	want := map[string]string{
		tagVLESS: "vless", tagHy2: "hysteria2", tagNaive: "naive",
		tagAnyTLS: "anytls", tagAuto: "urltest", tagPick: "selector", "direct": "direct",
	}
	for tag, typ := range want {
		if tags[tag] != typ {
			t.Errorf("outbound %q = %q, want type %q", tag, tags[tag], typ)
		}
	}

	// tun inbound + route final must wire to the selector.
	ins, _ := cfg["inbounds"].([]any)
	if len(ins) != 1 || ins[0].(map[string]any)["type"] != "tun" {
		t.Fatalf("expected a single tun inbound, got %v", ins)
	}
	// GUARD: the tun MUST use the kernel ("system") netstack, never "gvisor". gvisor is a
	// userspace TCP/IP stack (per-connection ring buffers + extra goroutines + packet copies)
	// — the one config change that genuinely OOM-kills a 1GB Android-TV box. Upstream sing-box
	// samples often default to gvisor, so fail loudly if a future edit/copy-paste flips it.
	if ins[0].(map[string]any)["stack"] != "system" {
		t.Fatalf("tun.stack = %v, want \"system\" (gvisor would OOM weak 1GB TVs)", ins[0].(map[string]any)["stack"])
	}
	if route, _ := cfg["route"].(map[string]any); route["final"] != tagPick {
		t.Fatalf("route.final = %v, want %q", route["final"], tagPick)
	}

	// DNS must use the sing-box 1.12+ server format (type+server), NOT the legacy
	// {address:"tls://8.8.8.8"} that libbox 1.14 rejects with a decode error.
	dnsSrv := cfg["dns"].(map[string]any)["servers"].([]any)[0].(map[string]any)
	if dnsSrv["type"] != "tls" || dnsSrv["server"] != "8.8.8.8" || dnsSrv["address"] != nil {
		t.Fatalf("dns server not in new format: %v", dnsSrv)
	}

	// hysteria2 password must be user:pass (userpass auth).
	for _, o := range outs {
		if m := o.(map[string]any); m["tag"] == tagHy2 {
			if m["password"] != "cust1:pw" {
				t.Fatalf("hy2 password = %v, want cust1:pw", m["password"])
			}
		}
	}

	// RU-direct split routing: the two .srs rule-sets must be declared, both
	// fetched DIRECT (download_detour=direct) from our RU-domestic mirror — NOT
	// through the selector, which isn't connected during service init (a proxied
	// fetch fails "context canceled" and the service won't start). A route rule
	// must send them to the direct outbound.
	route := cfg["route"].(map[string]any)
	rsTags := map[string]bool{}
	for _, rs := range route["rule_set"].([]any) {
		m := rs.(map[string]any)
		rsTags[m["tag"].(string)] = true
		if m["download_detour"] != "direct" {
			t.Errorf("rule_set %v download_detour = %v, want %q", m["tag"], m["download_detour"], "direct")
		}
		if m["format"] != "binary" {
			t.Errorf("rule_set %v format = %v, want binary", m["tag"], m["format"])
		}
	}
	if !rsTags[tagRUDomains] || !rsTags[tagRUIP] {
		t.Fatalf("missing RU rule-sets, got %v", rsTags)
	}
	var ruDirect bool
	for _, r := range route["rules"].([]any) {
		m := r.(map[string]any)
		if m["outbound"] == "direct" && m["rule_set"] != nil {
			ruDirect = true
		}
	}
	if !ruDirect {
		t.Fatal("no route rule sends the RU rule-sets to the direct outbound")
	}

	// The RU DOMAIN set must resolve via the local (direct) resolver, so the
	// lookup isn't proxied and geoip-ru matches the real RU IP.
	dnsRules, _ := cfg["dns"].(map[string]any)["rules"].([]any)
	var ruDNS bool
	for _, r := range dnsRules {
		m := r.(map[string]any)
		if m["server"] == "local" && m["rule_set"] != nil {
			ruDNS = true
		}
	}
	if !ruDNS {
		t.Fatal("no dns rule routes the RU domain set to the local resolver")
	}

	// Persistent cache so the fetched rule-sets survive restarts offline.
	if exp, _ := cfg["experimental"].(map[string]any); exp["cache_file"] == nil {
		t.Fatal("experimental.cache_file missing (RU rule-sets won't persist)")
	}
}

func TestGenerateSingboxPartial(t *testing.T) {
	// VLESS-only customer still produces a valid config with auto/select.
	raw, err := GenerateSingbox(Customer{Name: "v", VLESS: &VLESSCreds{Server: "s", Port: 443, UUID: "u"}})
	if err != nil {
		t.Fatalf("GenerateSingbox(vless-only): %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestGenerateSingboxNoProtocols(t *testing.T) {
	if _, err := GenerateSingbox(Customer{Name: "empty"}); err == nil {
		t.Fatal("expected error for a customer with no protocols")
	}
}

// TestGenerateSingboxOLCRTC: when OLC creds are present, the config gets a SOCKS5 outbound to
// the local olcRTC listener, exposed in the SELECTOR but kept OUT of the urltest "auto" pool,
// plus a DIRECT route rule for the carrier hosts (anti-loop). Absent OLC → none of it.
func TestGenerateSingboxOLCRTC(t *testing.T) {
	c := sampleCustomer()
	c.OLC = &OLCRTCCreds{Provider: "telemost", Room: "https://telemost.yandex.ru/j/123", Key: "ab", Transport: "vp8channel"}
	raw, err := GenerateSingbox(c)
	if err != nil {
		t.Fatalf("GenerateSingbox(+olc): %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// 1) socks outbound tag "olcrtc" → 127.0.0.1:8808, version 5.
	var socks map[string]any
	for _, o := range cfg["outbounds"].([]any) {
		if m := o.(map[string]any); m["tag"] == tagOLC {
			socks = m
		}
	}
	if socks == nil {
		t.Fatal("no olcrtc socks outbound emitted")
	}
	if socks["type"] != "socks" || socks["server"] != olcrtcSocksHost || int(socks["server_port"].(float64)) != olcrtcSocksPort {
		t.Fatalf("olcrtc outbound wrong: %v", socks)
	}

	// 2) in the selector, NOT in auto.
	var inSelector, inAuto bool
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		switch m["tag"] {
		case tagPick:
			for _, t := range m["outbounds"].([]any) {
				if t == tagOLC {
					inSelector = true
				}
			}
		case tagAuto:
			for _, t := range m["outbounds"].([]any) {
				if t == tagOLC {
					inAuto = true
				}
			}
		}
	}
	if !inSelector {
		t.Error("olcrtc missing from the selector")
	}
	if inAuto {
		t.Error("olcrtc must NOT be in the urltest auto pool (slow manual fallback)")
	}

	// 3) DIRECT route rules for the carrier hosts (anti-loop): a domain rule AND a static
	// ip_cidr rule (so raw-IP media/STUN don't depend on the remote geoip-ru being loaded).
	var carrierDomainDirect, carrierCIDRDirect bool
	for _, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if m["outbound"] != "direct" {
			continue
		}
		if m["domain_suffix"] != nil {
			for _, d := range m["domain_suffix"].([]any) {
				if d == "yandex.ru" {
					carrierDomainDirect = true
				}
			}
		}
		if m["ip_cidr"] != nil {
			for _, d := range m["ip_cidr"].([]any) {
				if d == "77.88.0.0/18" {
					carrierCIDRDirect = true
				}
			}
		}
	}
	if !carrierDomainDirect {
		t.Error("no DIRECT domain rule for the olcRTC carrier hosts")
	}
	if !carrierCIDRDirect {
		t.Error("no static DIRECT ip_cidr rule for the olcRTC carrier ranges (geoip-ru cold-start loop guard)")
	}

	// 4) absent OLC → nothing olcrtc anywhere.
	raw2, _ := GenerateSingbox(sampleCustomer())
	if bytes.Contains(raw2, []byte(tagOLC)) {
		t.Error("olcrtc leaked into a config without OLC creds")
	}
}

func TestGenerateSingboxVKTurn(t *testing.T) {
	c := sampleCustomer()
	c.VKTurn = &VKTurnCreds{
		PrivateKey: "client-private", PeerPublicKey: "server-public", LocalAddress: "10.77.0.2/32",
	}
	raw, err := GenerateSingbox(c)
	if err != nil {
		t.Fatalf("GenerateSingbox(+vk-turn): %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	var vk, selector, auto map[string]any
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		switch m["tag"] {
		case tagVKTurn:
			vk = m
		case tagPick:
			selector = m
		case tagAuto:
			auto = m
		}
	}
	if vk == nil {
		t.Fatal("vk-turn outbound missing")
	}
	if vk["type"] != "wireguard" || vk["server"] != vkTurnRelayHost || int(vk["server_port"].(float64)) != vkTurnRelayPort {
		t.Fatalf("vk-turn relay wrong: %v", vk)
	}
	if vk["private_key"] != "client-private" || vk["peer_public_key"] != "server-public" || int(vk["mtu"].(float64)) != 1280 {
		t.Fatalf("vk-turn WireGuard identity wrong: %v", vk)
	}
	addresses := vk["local_address"].([]any)
	if len(addresses) != 1 || addresses[0] != "10.77.0.2/32" {
		t.Fatalf("vk-turn local_address = %v", addresses)
	}

	contains := func(o map[string]any, tag string) bool {
		for _, value := range o["outbounds"].([]any) {
			if value == tag {
				return true
			}
		}
		return false
	}
	if !contains(selector, tagVKTurn) {
		t.Error("vk-turn missing from selector")
	}
	if contains(auto, tagVKTurn) {
		t.Error("vk-turn must be manual-only, not in urltest")
	}

	var vkDomainDirect, okcdnDomainDirect, vkCIDRDirect, dnsCIDRDirect bool
	for _, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if m["outbound"] != "direct" {
			continue
		}
		if domains, ok := m["domain_suffix"].([]any); ok {
			for _, domain := range domains {
				switch domain {
				case "vk.com":
					vkDomainDirect = true
				case "okcdn.ru":
					okcdnDomainDirect = true
				}
			}
		}
		if cidrs, ok := m["ip_cidr"].([]any); ok {
			for _, cidr := range cidrs {
				switch cidr {
				case "87.240.128.0/18":
					vkCIDRDirect = true
				case "77.88.8.8/32":
					dnsCIDRDirect = true
				}
			}
		}
	}
	if !vkDomainDirect || !okcdnDomainDirect || !vkCIDRDirect || !dnsCIDRDirect {
		t.Fatalf("vk carrier direct rules missing: vk_domain=%v okcdn_domain=%v vk_cidr=%v dns_cidr=%v",
			vkDomainDirect, okcdnDomainDirect, vkCIDRDirect, dnsCIDRDirect)
	}

	rawWithoutVK, err := GenerateSingbox(sampleCustomer())
	if err != nil {
		t.Fatalf("GenerateSingbox(without vk-turn): %v", err)
	}
	if bytes.Contains(rawWithoutVK, []byte(tagVKTurn)) || bytes.Contains(rawWithoutVK, []byte("vk-calls.com")) {
		t.Error("vk-turn data leaked into config without VKTurn creds")
	}
}
