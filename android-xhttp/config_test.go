package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func fixtureTransport() transport {
	return transport{
		Schema: 1, Address: "8.8.8.8", Port: 443,
		ServerName: "cdn.example.com", Host: "cdn.example.com", Path: "/fixture-path",
		ClientID:   "12345678-1234-4123-8123-123456789abc",
		Encryption: "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(make([]byte, 1184)),
		SocksPort:  31080,
		SocksUser:  base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 24)),
		SocksPass:  base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 24)),
	}
}

func fixtureJSON(t *testing.T, v transport) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTransportStrictInput(t *testing.T) {
	valid := fixtureJSON(t, fixtureTransport())
	if _, err := parseTransport(valid); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"unknown":         bytes.Replace(valid, []byte("{"), []byte("{\"freedom\":true,"), 1),
		"duplicate":       bytes.Replace(valid, []byte("{"), []byte("{\"schema\":1,"), 1),
		"second_document": append(append([]byte{}, valid...), []byte("{}")...),
		"array":           []byte("[]"),
		"null":            bytes.Replace(valid, []byte("\"schema\":1"), []byte("\"schema\":null"), 1),
		"oversize":        bytes.Repeat([]byte(" "), maxPayloadBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTransport(raw); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestTransportRejectsUnsafeFields(t *testing.T) {
	cases := map[string]func(*transport){
		"schema":                 func(v *transport) { v.Schema = 2 },
		"hostname_upstream":      func(v *transport) { v.Address = "cdn.example.com" },
		"private_upstream":       func(v *transport) { v.Address = "10.1.1.1" },
		"documentation_upstream": func(v *transport) { v.Address = "192.0.2.1" },
		"carrier_nat":            func(v *transport) { v.Address = "100.64.1.1" },
		"ipv6":                   func(v *transport) { v.Address = "2001:4860:4860::8888" },
		"port":                   func(v *transport) { v.Port = 80 },
		"host_mismatch":          func(v *transport) { v.Host = "other.example.com" },
		"ip_sni":                 func(v *transport) { v.ServerName = "8.8.8.8"; v.Host = v.ServerName },
		"path_query":             func(v *transport) { v.Path = "/path?query=1" },
		"path_fragment":          func(v *transport) { v.Path = "/path#fragment" },
		"path_absolute":          func(v *transport) { v.Path = "https://cdn.example.com/path" },
		"path_traversal":         func(v *transport) { v.Path = "/path/../secret" },
		"uuid":                   func(v *transport) { v.ClientID = "invalid" },
		"encryption":             func(v *transport) { v.Encryption = "none" },
		"socks_port":             func(v *transport) { v.SocksPort = 80 },
		"weak_socks_auth":        func(v *transport) { v.SocksPass = "password" },
		"same_socks_auth":        func(v *transport) { v.SocksPass = v.SocksUser },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			value := fixtureTransport()
			edit(&value)
			if _, err := parseTransport(fixtureJSON(t, value)); err == nil {
				t.Fatal("accepted invalid transport")
			}
		})
	}
}

func TestGeneratedConfigHasOnlyAuthenticatedLoopbackAndXHTTP(t *testing.T) {
	v := fixtureTransport()
	raw, err := v.config(7)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["dns"]; ok {
		t.Fatal("unexpected native DNS")
	}
	for _, forbidden := range []string{"freedom", "blackhole", "urltest", "auto", "\"allowInsecure\":true"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("unexpected config capability")
		}
	}
	var inbounds []struct {
		Listen   string
		Protocol string
		Settings struct {
			Auth     string
			UDP      bool
			Accounts []struct {
				User string
				Pass string
			}
		}
	}
	if json.Unmarshal(config["inbounds"], &inbounds) != nil ||
		len(inbounds) != 1 || inbounds[0].Listen != "127.0.0.1" ||
		inbounds[0].Protocol != "socks" || inbounds[0].Settings.Auth != "password" ||
		inbounds[0].Settings.UDP || len(inbounds[0].Settings.Accounts) != 1 ||
		inbounds[0].Settings.Accounts[0].Pass != v.SocksPass {
		t.Fatal("invalid TCP-only SOCKS boundary")
	}
	var outbounds []struct {
		Protocol       string
		StreamSettings struct {
			Network     string
			Security    string
			Sockopt     struct{ Mark int }
			TLSSettings struct {
				ServerName  string
				ALPN        []string
				Fingerprint string
			}
			XHTTPSettings struct {
				Host                string
				Path                string
				Mode                string
				UplinkHTTPMethod    string
				UplinkDataPlacement string
			}
		}
	}
	if json.Unmarshal(config["outbounds"], &outbounds) != nil || len(outbounds) != 1 {
		t.Fatal("invalid outbounds")
	}
	o := outbounds[0]
	if o.Protocol != "vless" || o.StreamSettings.Network != "xhttp" ||
		o.StreamSettings.Security != "tls" || o.StreamSettings.Sockopt.Mark != 7 ||
		o.StreamSettings.TLSSettings.ServerName != v.ServerName ||
		o.StreamSettings.TLSSettings.Fingerprint != "firefox" ||
		len(o.StreamSettings.TLSSettings.ALPN) != 1 || o.StreamSettings.TLSSettings.ALPN[0] != "h2" ||
		o.StreamSettings.XHTTPSettings.Host != v.Host || o.StreamSettings.XHTTPSettings.Path != v.Path ||
		o.StreamSettings.XHTTPSettings.Mode != "packet-up" || o.StreamSettings.XHTTPSettings.UplinkHTTPMethod != "GET" ||
		o.StreamSettings.XHTTPSettings.UplinkDataPlacement != "body" {
		t.Fatal("transport contract drift")
	}
}
