package subgen

import (
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
		Hy2:   &Hy2Creds{Server: "wapmix.duckdns.org", Port: 8443, User: "cust1", Pass: "pw", SNI: "wapmix.duckdns.org", Insecure: true},
		Naive: &NaiveCreds{Server: "wapmixx.ru", Port: 443, Username: "mtv_cust1", Password: "np", SNI: "naive.example"},
		Mieru: &MieruCreds{Server: "85.137.166.237", Port: 2027, Username: "cust1", Password: "mp", Transport: "TCP", HelperSOCKS: 11082},
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
		tagMieru: "socks", tagAuto: "urltest", tagPick: "selector", "direct": "direct",
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
