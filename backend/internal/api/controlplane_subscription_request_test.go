package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// This exercises the shipped HTTP adapter and ServiceBusiness. Only the
// canonical database read is replaced; format selection and rendering are real.
func TestControlPlaneSubscriptionPreservesFrozenRequestSemantics(t *testing.T) {
	oldMinimum, oldFakeIPOff := awgMinVC, dnsFakeIPOff
	awgMinVC = 107
	t.Cleanup(func() { awgMinVC, dnsFakeIPOff = oldMinimum, oldFakeIPOff })
	for _, tc := range []struct {
		name, query, userAgent       string
		links, naive, awg, fakeIPOff bool
	}{
		{name: "installed_mobile", query: "?platform=mobile", userAgent: "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)", naive: true, awg: true},
		{name: "installed_tv", query: "?platform=tv", userAgent: "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)", naive: true, awg: true},
		{name: "older_sfa", userAgent: "SFA/1.0.106 (106; sing-box 1.14; language ru_RU)", naive: true},
		{name: "stock_core", userAgent: "curl/8.0"},
		{name: "plain_karing", userAgent: "Karing/1.0"},
		{name: "malformed_sfa", userAgent: "SFA/1.0.157"},
		{name: "karing_links", query: "?app=karing", userAgent: "Karing/1.0", links: true, naive: true},
		{name: "format_links", query: "?format=links", userAgent: "curl/8.0", links: true, naive: true},
		{name: "case_sensitive_app", query: "?app=Karing", userAgent: "Karing/1.0"},
		{name: "fakeip_kill_switch", userAgent: "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)", naive: true, awg: true, fakeIPOff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dnsFakeIPOff = tc.fakeIPOff
			topology := subscriptionRequestTopology()
			before, err := json.Marshal(topology)
			if err != nil {
				t.Fatal(err)
			}
			customer := controlplane.BusinessCustomer{
				Customer: controlplane.Customer{
					Status: "active", ExpiresAtUnix: time.Now().Add(24 * time.Hour).Unix(), Generation: 7,
					Access: controlplane.CustomerAccess{Credentials: map[string]string{
						"vless": "customer-uuid", "hysteria2": "hy2-password", "naive": "naive-password", "anytls": "anytls-password",
					}},
				}, Login: "fixture-login",
			}
			accountWG := &subgen.WGCreds{Server: "awg.example.test", Port: 443, PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), LocalAddress: "10.10.8.2/32"}
			wgCredential, err := controlplane.EncodeWGCredentialIdentity(accountWG)
			if err != nil {
				t.Fatal(err)
			}
			customer.Access.Credentials["awg"] = wgCredential
			business := NewServiceBusiness(nil, ServiceBusinessConfig{SubscriptionTopology: topology})
			business.subscriptions = subscriptionRequestSource{customer: customer}
			handler := NewControlPlane(business, Config{}).Handler()
			request := httptest.NewRequest(http.MethodGet, "/sub/fixture-token"+tc.query, nil)
			request.Header.Set("User-Agent", tc.userAgent)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d", response.Code)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}

			// Hand-specified legacy input: all nodes reuse the customer's UUID,
			// never the stale UUID supplied in the shared topology fixture.
			wantCustomer := subgen.Customer{
				Name: "fixture-login", DNSFakeIP: !tc.fakeIPOff,
				VLESS:  &subgen.VLESSCreds{Server: "s1.example.test", Port: 443, UUID: "customer-uuid", SNI: "s1.example.test", PublicKey: "s1-public", ShortID: "1111", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
				Hy2:    &subgen.Hy2Creds{Server: "s2.example.test", Port: 8443, User: "fixture-login", Pass: "hy2-password", SNI: "s2.example.test", Insecure: true},
				AnyTLS: &subgen.AnyTLSCreds{Server: "anytls.example.test", Port: 8443, Password: "anytls-password", SNI: "anytls.example.test", Insecure: true},
				VLESS3: &subgen.VLESSCreds{Server: "s3.example.test", Port: 443, UUID: "customer-uuid", SNI: "s3.example.test", PublicKey: "s3-public", ShortID: "3333", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
				VLESS4: &subgen.VLESSCreds{Server: "s4.example.test", Port: 443, UUID: "customer-uuid", SNI: "s4.example.test", PublicKey: "s4-public", ShortID: "4444", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
			}
			if tc.naive {
				wantCustomer.Naive = &subgen.NaiveCreds{Server: "naive.example.test", Port: 443, Username: "fixture-login", Password: "naive-password", SNI: "naive.example.test"}
			}
			if tc.awg {
				wantCustomer.WG = accountWG
			}
			var want []byte
			wantContentType := "application/json"
			if tc.links {
				want = []byte(subgen.ShareLinks(wantCustomer))
				wantContentType = "text/plain; charset=utf-8"
			} else {
				want, err = subgen.GenerateSingbox(wantCustomer)
				if err != nil {
					t.Fatal(err)
				}
			}
			if response.Header().Get("Content-Type") != wantContentType {
				t.Errorf("content type=%q, want %q", response.Header().Get("Content-Type"), wantContentType)
			}
			if !bytes.Equal(response.Body.Bytes(), want) {
				t.Error("HTTP subscription differs from frozen legacy client document")
			}
			after, err := json.Marshal(topology)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("request mutated shared topology")
			}
		})
	}
}

func TestControlPlaneSubscriptionMissingCredentialsDoNotPublishTopologyIdentity(t *testing.T) {
	topology := subscriptionRequestTopology()
	topology.WG = nil
	customer := controlplane.BusinessCustomer{Login: "fixture-login", Customer: controlplane.Customer{
		Access: controlplane.CustomerAccess{Credentials: map[string]string{"hysteria2": "hy2-password"}},
	}}
	document, _, err := renderControlPlaneSubscription(customer, topology, subscriptionRenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Outbounds []struct {
			Tag  string `json:"tag"`
			UUID string `json:"uuid"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(document, &got); err != nil {
		t.Fatal(err)
	}
	for _, outbound := range got.Outbounds {
		if outbound.UUID != "" {
			t.Fatalf("missing customer credential still published UUID at tag %q", outbound.Tag)
		}
	}
}

type subscriptionRequestSource struct{ customer controlplane.BusinessCustomer }

func (source subscriptionRequestSource) BusinessCustomerByToken(_ context.Context, token string) (controlplane.BusinessCustomer, error) {
	if token != "fixture-token" {
		return controlplane.BusinessCustomer{}, fmt.Errorf("unexpected fixture token")
	}
	return source.customer, nil
}

func subscriptionRequestTopology() subgen.Customer {
	return subgen.Customer{
		VLESS:  &subgen.VLESSCreds{Server: "s1.example.test", Port: 443, UUID: "stale-topology-uuid", SNI: "s1.example.test", PublicKey: "s1-public", ShortID: "1111", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
		Hy2:    &subgen.Hy2Creds{Server: "s2.example.test", Port: 8443, SNI: "s2.example.test", Insecure: true},
		Naive:  &subgen.NaiveCreds{Server: "naive.example.test", Port: 443, SNI: "naive.example.test"},
		AnyTLS: &subgen.AnyTLSCreds{Server: "anytls.example.test", Port: 8443, SNI: "anytls.example.test", Insecure: true},
		VLESS3: &subgen.VLESSCreds{Server: "s3.example.test", Port: 443, UUID: "stale-topology-uuid", SNI: "s3.example.test", PublicKey: "s3-public", ShortID: "3333", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
		VLESS4: &subgen.VLESSCreds{Server: "s4.example.test", Port: 443, UUID: "stale-topology-uuid", SNI: "s4.example.test", PublicKey: "s4-public", ShortID: "4444", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
		WG:     &subgen.WGCreds{Server: "awg.example.test", Port: 443, PeerPublicKey: "peer-public", PrivateKey: "fixture-private", LocalAddress: "10.10.8.2/32"},
	}
}
