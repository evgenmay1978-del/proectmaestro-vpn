package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestRenderControlPlaneSubscriptionUsesFrozenGenerator(t *testing.T) {
	t.Parallel()

	customer := controlplane.BusinessCustomer{
		Customer: controlplane.Customer{
			Access: controlplane.CustomerAccess{Credentials: map[string]string{
				"vless":    "customer-vless-uuid",
				"hysteria2": "customer-hy2-password",
				"naive":    "customer-naive-password",
				"anytls":   "customer-anytls-password",
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
	body := string(document)
	for _, want := range []string{
		`"type":"vless"`, `"server":"vless.example.test"`, `"uuid":"customer-vless-uuid"`,
		`"type":"hysteria2"`, `"password":"customer-hy2-password"`,
		`"type":"naive"`, `"password":"customer-naive-password"`,
		`"type":"anytls"`, `"password":"customer-anytls-password"`,
		`"route"`, `"dns"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("subscription does not contain %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"credentials"`) {
		t.Fatalf("subscription leaked control-plane metadata instead of a client document: %s", body)
	}
}
