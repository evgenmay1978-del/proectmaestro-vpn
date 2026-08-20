package subgen

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func whiteListFixture(t *testing.T) (controlplane.WhiteListEntitlement, controlplane.TransportProfile, controlplane.CompatibilityPreset, controlplane.TransportRelease) {
	t.Helper()
	entitlement, err := controlplane.NewWhiteListEntitlement("account-alpha")
	if err != nil {
		t.Fatalf("NewWhiteListEntitlement: %v", err)
	}
	entitlement, err = entitlement.Activate("profile-a", "preset-a", "release-a")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	profile := controlplane.TransportProfile{
		ID: "profile-a", PublicHost: "cdn.example.invalid", SecretPath: "/static/test/segment.ts/opaque",
		OriginRouteID: "origin-route-a", CompatibilityPresetID: "preset-a",
	}
	preset := controlplane.CompatibilityPreset{
		ID: "preset-a", Version: 1, ProtectionLevel: "advanced",
		Capabilities: []string{"vless-encryption", "xhttp-get-body"},
	}
	release, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID:                    "release-a",
		TransportProfileID:    "profile-a",
		CompatibilityPresetID: "preset-a",
		State:                 controlplane.TransportReleasePublished,
		ApprovedEdges: []controlplane.ApprovedEdge{
			{ID: "edge-b", TransportProfileID: "profile-a", Address: "203.0.113.12", ApprovedAt: time.Unix(20, 0), EvidenceRef: "evidence-b"},
			{ID: "edge-a", TransportProfileID: "profile-a", Address: "203.0.113.11", ApprovedAt: time.Unix(10, 0), EvidenceRef: "evidence-a"},
		},
	})
	if err != nil {
		t.Fatalf("NewTransportRelease: %v", err)
	}
	return entitlement, profile, preset, release
}

func TestRenderWhiteListSubscriptionLeavesOrdinaryOutputByteExactWhenDisabled(t *testing.T) {
	entitlement, err := controlplane.NewWhiteListEntitlement("account-alpha")
	if err != nil {
		t.Fatalf("NewWhiteListEntitlement: %v", err)
	}
	ordinary := OrdinarySubscription{Identity: "ordinary-subscription-alpha", Output: "opaque\nordinary\noutput\n"}

	result, err := RenderWhiteListSubscription(ordinary, entitlement, controlplane.TransportProfile{}, controlplane.CompatibilityPreset{}, controlplane.TransportRelease{})
	if err != nil {
		t.Fatalf("RenderWhiteListSubscription(disabled): %v", err)
	}
	if result.Ordinary != ordinary {
		t.Fatalf("ordinary subscription changed: got %#v want %#v", result.Ordinary, ordinary)
	}
	if len(result.WhiteListNodes) != 0 {
		t.Fatalf("disabled entitlement rendered %d CDN nodes", len(result.WhiteListNodes))
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	want := `{"ordinary":{"identity":"ordinary-subscription-alpha","output":"opaque\nordinary\noutput\n"},"white_list_nodes":[]}`
	if string(raw) != want {
		t.Fatalf("disabled API contract = %s, want %s", raw, want)
	}
}

func TestRenderWhiteListSubscriptionAddsOnlyDeterministicApprovedNodesWhenActive(t *testing.T) {
	entitlement, profile, preset, release := whiteListFixture(t)
	ordinary := OrdinarySubscription{Identity: "ordinary-subscription-alpha", Output: "opaque-existing-output"}

	result, err := RenderWhiteListSubscription(ordinary, entitlement, profile, preset, release)
	if err != nil {
		t.Fatalf("RenderWhiteListSubscription(active): %v", err)
	}
	if result.Ordinary != ordinary {
		t.Fatalf("active rendering replaced ordinary subscription: got %#v want %#v", result.Ordinary, ordinary)
	}
	if len(result.WhiteListNodes) != 2 {
		t.Fatalf("active node count = %d, want 2", len(result.WhiteListNodes))
	}
	gotAddresses := []string{result.WhiteListNodes[0].Address, result.WhiteListNodes[1].Address}
	if !reflect.DeepEqual(gotAddresses, []string{"203.0.113.11", "203.0.113.12"}) {
		t.Fatalf("node order = %v, want canonical address order", gotAddresses)
	}
	for _, node := range result.WhiteListNodes {
		if node.Protocol != "vless" || node.Network != "xhttp" || node.Port != 443 || !node.TLS ||
			node.ServerName != profile.PublicHost || node.Host != profile.PublicHost || node.Path != profile.SecretPath ||
			node.Mode != "packet-up" || node.UplinkHTTPMethod != "GET" || node.UplinkDataPlacement != "body" ||
			node.TransportProfileID != profile.ID || node.CompatibilityPresetID != preset.ID || node.TransportReleaseID != release.ID() {
			t.Fatalf("unexpected CDN node contract: %#v", node)
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal active result: %v", err)
	}
	if containsJSONKey(raw, "origin_route_id") || containsJSONKey(raw, "data_plane_instance_id") {
		t.Fatalf("public subscription contract exposed origin routing: %s", raw)
	}
}

func TestRenderWhiteListSubscriptionRejectsMismatchedActiveRelease(t *testing.T) {
	entitlement, profile, preset, release := whiteListFixture(t)
	profile.ID = "profile-other"
	_, err := RenderWhiteListSubscription(OrdinarySubscription{Identity: "ordinary", Output: "payload"}, entitlement, profile, preset, release)
	if err == nil {
		t.Fatal("active entitlement accepted a release/profile mismatch")
	}
}

func containsJSONKey(raw []byte, key string) bool {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	for k, v := range value {
		if k == key {
			return true
		}
		if nested, ok := json.Marshal(v); ok == nil && containsJSONKey(nested, key) {
			return true
		}
	}
	return false
}
