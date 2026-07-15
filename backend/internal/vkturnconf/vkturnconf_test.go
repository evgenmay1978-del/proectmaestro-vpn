package vkturnconf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func validConfig() Config {
	clients := make(map[string]Client)
	for _, login := range allowedLogins {
		clients[login] = Client{Password: "secret-" + login, WG: subgen.VKTurnCreds{
			PeerPublicKey: testKey,
			PrivateKey:    testKey, LocalAddress: "10.80.0.2/32",
		}}
	}
	return Config{Enabled: true, MinVersionCode: 200, Server: "wdtt.example:443", VKHashes: []string{"hash-one"}, Clients: clients}
}

func writeConfig(t *testing.T, cfg any) string {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "vkturn.json")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenValidAndClientFor(t *testing.T) {
	cfg, err := Open(writeConfig(t, validConfig()))
	if err != nil {
		t.Fatal(err)
	}
	if client, ok := cfg.ClientFor("wapmix"); !ok || client.Password != "secret-wapmix" {
		t.Fatalf("ClientFor(wapmix) = %#v, %v", client, ok)
	}
	if _, ok := cfg.ClientFor("WAPMIX"); ok {
		t.Fatal("case-insensitive login escaped allowlist")
	}
}

func TestOpenEmptyPathIsDisabled(t *testing.T) {
	cfg, err := Open("")
	if err != nil || cfg != nil {
		t.Fatalf("Open(empty) = %#v, %v", cfg, err)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"disabled partial", func(c *Config) { c.Enabled = false; c.Server = "" }},
		{"no minimum", func(c *Config) { c.MinVersionCode = 0 }},
		{"no hashes", func(c *Config) { c.VKHashes = nil }},
		{"missing login", func(c *Config) { delete(c.Clients, "wapmix2") }},
		{"extra login", func(c *Config) { c.Clients["other"] = c.Clients["wapmix"] }},
		{"empty password", func(c *Config) { v := c.Clients["wapmix"]; v.Password = ""; c.Clients["wapmix"] = v }},
		{"bad key", func(c *Config) { v := c.Clients["wapmix"]; v.WG.PrivateKey = "bad"; c.Clients["wapmix"] = v }},
		{"bad cidr", func(c *Config) { v := c.Clients["wapmix"]; v.WG.LocalAddress = "bad"; c.Clients["wapmix"] = v }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if _, err := Open(writeConfig(t, cfg)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestOpenRejectsUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vkturn.json")
	if err := os.WriteFile(p, []byte(`{"enabled":true,"unknown":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("unknown field accepted")
	}
}
