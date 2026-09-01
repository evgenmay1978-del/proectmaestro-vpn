package api

import (
	"encoding/json"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestRenderControlPlaneSubscriptionUsesFrozenGenerator(t *testing.T) {
	t.Parallel()

	customer := controlplane.BusinessCustomer{
		Customer: controlplane.Customer{
			Access: controlplane.CustomerAccess{Credentials: map[string]string{
				"vless":     "customer-vless-uuid",
				"hysteria2": "customer-hy2-password",
				"naive":     "customer-naive-password",
				"anytls":    "customer-anytls-password",
			}},
		},
		Login: "alice",
	}
	topology := subgen.Customer{
		VLESS:  &subgen.VLESSCreds{Server: "vless.example.test", Port: 443, Flow: "xtls-rprx-vision", SNI: "cdn.example.test", PublicKey: "reality-public-key", ShortID: "0123456789abcdef", Fingerprint: "chrome"},
		Hy2:    &subgen.Hy2Creds{Server: "hy2.example.test", Port: 8443, SNI: "hy2.example.test"},
		Naive:  &subgen.NaiveCreds{Server: "naive.example.test", Port: 443, SNI: "naive.example.test"},
		AnyTLS: &subgen.AnyTLSCreds{Server: "anytls.example.test", Port: 443, SNI: "anytls.example.test"},
	}

	document, contentType, err := renderControlPlaneSubscription(customer, topology, subscriptionRenderOptions{})
	if err != nil {
		t.Fatalf("render subscription: %v", err)
	}
	if contentType != "application/json" {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
	if !json.Valid(document) {
		t.Fatalf("subscription is not JSON: %s", document)
	}
	var config struct {
		DNS       json.RawMessage `json:"dns"`
		Route     json.RawMessage `json:"route"`
		Outbounds []struct {
			Type     string `json:"type"`
			Server   string `json:"server"`
			UUID     string `json:"uuid"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"outbounds"`
		Credentials json.RawMessage `json:"credentials"`
	}
	if err := json.Unmarshal(document, &config); err != nil {
		t.Fatalf("decode subscription: %v", err)
	}
	if len(config.DNS) == 0 || len(config.Route) == 0 {
		t.Fatalf("subscription lacks DNS or route configuration: %s", document)
	}
	if len(config.Credentials) != 0 {
		t.Fatalf("subscription leaked control-plane metadata instead of a client document: %s", document)
	}
	want := map[string]struct{ server, uuid, username, password string }{
		"vless":     {server: "vless.example.test", uuid: "customer-vless-uuid"},
		"hysteria2": {server: "hy2.example.test", password: "alice:customer-hy2-password"},
		"naive":     {server: "naive.example.test", username: "alice", password: "customer-naive-password"},
		"anytls":    {server: "anytls.example.test", password: "customer-anytls-password"},
	}
	for _, outbound := range config.Outbounds {
		expected, ok := want[outbound.Type]
		if !ok {
			continue
		}
		if outbound.Server != expected.server || outbound.UUID != expected.uuid || outbound.Username != expected.username || outbound.Password != expected.password {
			t.Errorf("%s outbound = %#v, want server=%q uuid=%q username=%q password=%q", outbound.Type, outbound, expected.server, expected.uuid, expected.username, expected.password)
		}
		delete(want, outbound.Type)
	}
	if len(want) != 0 {
		t.Fatalf("subscription lacks configured protocol outbounds %v: %s", want, document)
	}
}
