package subgen

import (
	"encoding/json"
	"strings"
	"testing"
)

func nativeNodeFixture() WhiteListNode {
	node := xhttpLinkNode()
	node.TransportProfileID = "profile-1"
	node.TransportReleaseID = "release-1"
	node.CompatibilityPresetID = "preset-1"
	node.Label = "Maestro CDN Netherlands"
	return node
}

func TestNativeWhiteListProfileHasOnlyFixedTypedFields(t *testing.T) {
	node := nativeNodeFixture()
	profiles, err := NativeWhiteListProfiles([]WhiteListNode{node})
	if err != nil || len(profiles) != 1 {
		t.Fatalf("native render failed: %v", err)
	}
	profile := profiles[0]
	if len(profile.RouteID) != 64 || profile.Address != node.Address || profile.ServerName != node.ServerName ||
		profile.Host != node.Host || profile.Port != 443 || profile.ClientID != node.ClientID || profile.Encryption != node.Encryption {
		t.Fatal("native transport or route identity mismatch")
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	keys := []string{"route_id", "label", "transport_profile_id", "transport_release_id", "compatibility_preset_id",
		"address", "port", "server_name", "host", "path", "client_id", "encryption"}
	if len(fields) != len(keys) {
		t.Fatal("unexpected native profile fields")
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing typed field %s", key)
		}
	}
	node.Label = "Maestro CDN NL"
	renamed, err := NativeWhiteListProfiles([]WhiteListNode{node})
	if err != nil || renamed[0].RouteID != profile.RouteID {
		t.Fatal("cosmetic label changed transport identity")
	}
	node.TransportReleaseID = "release-2"
	changed, err := NativeWhiteListProfiles([]WhiteListNode{node})
	if err != nil || changed[0].RouteID == profile.RouteID {
		t.Fatal("release drift did not change route identity")
	}
}

func TestNativeWhiteListProfilesRejectWholeInvalidBatch(t *testing.T) {
	for name, mutate := range map[string]func(*WhiteListNode){
		"wrong-alpn":         func(n *WhiteListNode) { n.ALPN = []string{"http/1.1"} },
		"wrong-host":         func(n *WhiteListNode) { n.Host = "other.example.invalid" },
		"arbitrary-extra":    func(n *WhiteListNode) { n.Extra = "arbitrary" },
		"plaintext":          func(n *WhiteListNode) { n.TLS = false },
		"private-ip":         func(n *WhiteListNode) { n.Address = "127.0.0.1" },
		"missing-release":    func(n *WhiteListNode) { n.TransportReleaseID = "" },
		"invalid-provenance": func(n *WhiteListNode) { n.CompatibilityPresetID = "bad\npreset" },
		"invalid-material":   func(n *WhiteListNode) { n.Encryption = "none" },
		"unsafe-label":       func(n *WhiteListNode) { n.Label = "bad\nlabel" },
		"internal-label":     func(n *WhiteListNode) { n.Label = "CDN release-1" },
	} {
		t.Run(name, func(t *testing.T) {
			good, bad := nativeNodeFixture(), nativeNodeFixture()
			bad.ClientID = "22222222-2222-4222-8222-222222222222"
			bad.Label = "Maestro CDN France"
			mutate(&bad)
			if result, err := NativeWhiteListProfiles([]WhiteListNode{good, bad}); err == nil || result != nil {
				t.Fatal("invalid batch leaked partial material")
			}
		})
	}
	node := nativeNodeFixture()
	for _, nodes := range [][]WhiteListNode{nil, {}, {node, node}, make([]WhiteListNode, 17)} {
		if result, err := NativeWhiteListProfiles(nodes); err == nil || result != nil {
			t.Fatal("invalid batch accepted")
		}
	}
	node.Address = "11.22.33.44"
	profiles, err := NativeWhiteListProfiles([]WhiteListNode{node})
	if err != nil || profiles[0].ServerName != "cdn.example.invalid" || !strings.EqualFold(profiles[0].Host, node.Host) {
		t.Fatal("approved literal changed Host/SNI")
	}
}
