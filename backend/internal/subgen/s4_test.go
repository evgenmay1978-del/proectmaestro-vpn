package subgen

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func s4Creds() *VLESSCreds {
	return &VLESSCreds{
		Server: "89.125.19.95", Port: 443, UUID: "uuid-4", Flow: "xtls-rprx-vision",
		SNI: "www.philips.com", PublicKey: "pbk4", ShortID: "f4ed", Fingerprint: "firefox",
	}
}

// The 4th node must reach BOTH client families: the app reads the sing-box JSON, while
// iPhone customers on Karing import the base64 share-links. A node added to only one of
// them is invisible to half the customers — this test pins both.
func TestS4ReachesAppAndKaring(t *testing.T) {
	c := sampleCustomer()
	c.VLESS4 = s4Creds()

	// --- app path: sing-box JSON ---
	raw, err := GenerateSingbox(c)
	if err != nil {
		t.Fatalf("GenerateSingbox: %v", err)
	}
	var cfg struct {
		Outbounds []struct {
			Type      string   `json:"type"`
			Tag       string   `json:"tag"`
			Server    string   `json:"server"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found, auto, sel bool
	for _, o := range cfg.Outbounds {
		if o.Tag == tagVLESS4 {
			found = true
			if o.Server != "89.125.19.95" {
				t.Errorf("s4 outbound points at %q, want 89.125.19.95", o.Server)
			}
		}
		if o.Type == "urltest" {
			auto = contains(o.Outbounds, tagVLESS4)
		}
		if o.Type == "selector" {
			sel = contains(o.Outbounds, tagVLESS4)
		}
	}
	if !found {
		t.Error("app path: no vless-s4 outbound in the sing-box config")
	}
	if !auto {
		t.Error("app path: vless-s4 missing from the urltest \"auto\" pool")
	}
	if !sel {
		t.Error("app path: vless-s4 missing from the selector")
	}

	// --- iPhone/Karing path: base64 share-links ---
	dec, err := base64.StdEncoding.DecodeString(ShareLinks(c))
	if err != nil {
		t.Fatalf("share links are not valid base64: %v", err)
	}
	links := strings.Split(strings.TrimSpace(string(dec)), "\n")
	var s4link string
	for _, l := range links {
		if strings.Contains(l, "89.125.19.95") {
			s4link = l
		}
	}
	if s4link == "" {
		t.Fatal("karing path: no S4 link — iPhone customers would never see the new node")
	}
	for _, want := range []string{"vless://", "sni=www.philips.com", "pbk=pbk4", "fp=firefox"} {
		if !strings.Contains(s4link, want) {
			t.Errorf("karing path: S4 link missing %q\nlink: %s", want, s4link)
		}
	}
}

// An un-configured S4 must leave the subscription BYTE-FOR-BYTE unchanged. This is the
// guarantee behind the staged rollout: deploying the S4-capable panel before any customer
// is provisioned on S4 cannot alter what a single existing customer receives.
func TestS4AbsentChangesNothing(t *testing.T) {
	c := sampleCustomer()

	before, err := GenerateSingbox(c)
	if err != nil {
		t.Fatalf("GenerateSingbox: %v", err)
	}
	beforeLinks := ShareLinks(c)

	// Same customer, S4 explicitly nil (the un-provisioned state).
	c.VLESS4 = nil
	after, err := GenerateSingbox(c)
	if err != nil {
		t.Fatalf("GenerateSingbox: %v", err)
	}

	if string(before) != string(after) {
		t.Error("sing-box config changed while S4 is absent — staged rollout is not safe")
	}
	if beforeLinks != ShareLinks(c) {
		t.Error("share links changed while S4 is absent — staged rollout is not safe")
	}
	if strings.Contains(string(after), tagVLESS4) {
		t.Errorf("config mentions %s even though the customer has no S4", tagVLESS4)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
