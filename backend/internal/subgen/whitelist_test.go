package subgen

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func reviewedCredential() controlplane.WhiteListCredential {
	return controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	}
}

func reviewedFixture(t *testing.T) (controlplane.WhiteListEntitlement, controlplane.TransportRelease) {
	t.Helper()
	entitlement, err := controlplane.NewWhiteListEntitlement("account-alpha")
	if err != nil {
		t.Fatalf("NewWhiteListEntitlement: %v", err)
	}
	entitlement, err = entitlement.Activate("profile-a", "preset-a", "release-a", reviewedCredential())
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	profile := controlplane.TransportProfile{
		ID: "profile-a", PublicHost: "cdn.example.invalid", SecretPath: "/static/test/segment.ts/opaque",
		OriginRouteID: "origin-route-a", CompatibilityPresetID: "preset-a",
	}
	preset := controlplane.CompatibilityPreset{
		ID: "preset-a", Version: 1, Kind: "MAESTRO_ADVANCED", ProtectionLevel: "advanced",
		Capabilities: []string{"vless-encryption", "xhttp-get-body"},
		CoreRange:    "xray>=26.7.28", ClientRanges: []string{"maestrovpn>=154"}, FixtureRefs: []string{"fixture-a"},
		Protocol: "vless", Network: "xhttp", Port: 443, TLS: true,
		Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ALPN: []string{"h2"}, Fingerprint: "firefox", ExtraJSON: `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id"}`, LabelPrefix: "БС/Yandex", DomainFallback: true,
	}
	release, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: "release-a", Profile: profile, Preset: preset,
		State: controlplane.TransportReleasePublished,
		ApprovedEdges: []controlplane.ApprovedEdge{
			{ID: "edge-b", TransportProfileID: "profile-a", Address: "1.1.1.11", ApprovedAt: time.Unix(20, 0), EvidenceRef: "evidence-b"},
			{ID: "edge-a", TransportProfileID: "profile-a", Address: "8.8.8.12", ApprovedAt: time.Unix(10, 0), EvidenceRef: "evidence-a"},
		},
	})
	if err != nil {
		t.Fatalf("NewTransportRelease: %v", err)
	}
	return entitlement, release
}

func TestRenderWhiteListSubscriptionLeavesOrdinaryOutputByteExactWhenDisabled(t *testing.T) {
	entitlement, err := controlplane.NewWhiteListEntitlement("account-alpha")
	if err != nil {
		t.Fatalf("NewWhiteListEntitlement: %v", err)
	}
	ordinary := OrdinarySubscription{AccountID: "account-alpha", Identity: "ordinary-subscription-alpha", Output: "opaque\nordinary\noutput\n"}
	result := RenderWhiteListSubscription(ordinary, entitlement, controlplane.TransportRelease{})
	if result.Ordinary != ordinary || len(result.WhiteListNodes) != 0 || result.Diagnostic != nil {
		t.Fatalf("disabled rendering changed ordinary access: %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	want := `{"ordinary":{"identity":"ordinary-subscription-alpha","output":"opaque\nordinary\noutput\n"},"white_list_nodes":[]}`
	if string(raw) != want {
		t.Fatalf("disabled API contract=%s, want %s", raw, want)
	}
}

func TestRenderWhiteListSubscriptionLeavesOrdinaryForEveryNonActiveState(t *testing.T) {
	active, release := reviewedFixture(t)
	ordinary := OrdinarySubscription{AccountID: "account-alpha", Identity: "ordinary", Output: "payload"}
	states := []controlplane.EntitlementState{
		controlplane.EntitlementDisabled,
		controlplane.EntitlementProvisioning,
		controlplane.EntitlementGrace,
		controlplane.EntitlementSuspended,
		controlplane.EntitlementError,
		controlplane.EntitlementExpired,
	}
	for _, state := range states {
		entitlement, err := active.WithState(state)
		if err != nil {
			t.Fatalf("WithState(%q): %v", state, err)
		}
		result := RenderWhiteListSubscription(ordinary, entitlement, release)
		if result.Ordinary != ordinary || len(result.WhiteListNodes) != 0 || result.Diagnostic != nil {
			t.Errorf("state %q changed ordinary access: %#v", state, result)
		}
	}
}

func TestRenderWhiteListSubscriptionUsesFrozenReleaseAndDeterministicApprovedEdges(t *testing.T) {
	entitlement, release := reviewedFixture(t)
	ordinary := OrdinarySubscription{AccountID: "account-alpha", Identity: "ordinary-subscription-alpha", Output: "opaque-existing-output"}
	result := RenderWhiteListSubscription(ordinary, entitlement, release)
	if result.Ordinary != ordinary || result.Diagnostic != nil {
		t.Fatalf("active rendering changed ordinary subscription: %#v", result)
	}
	if len(result.WhiteListNodes) != 3 {
		t.Fatalf("active node count=%d, want two edges plus domain fallback", len(result.WhiteListNodes))
	}
	gotAddresses := []string{result.WhiteListNodes[0].Address, result.WhiteListNodes[1].Address}
	if !reflect.DeepEqual(gotAddresses, []string{"8.8.8.12", "1.1.1.11"}) {
		t.Fatalf("node order=%v, want edge-ID canonical order", gotAddresses)
	}
	for _, node := range result.WhiteListNodes {
		if node.Protocol != "vless" || node.Network != "xhttp" || node.Port != 443 || !node.TLS ||
			node.ServerName != "cdn.example.invalid" || node.Host != "cdn.example.invalid" || node.Path != "/static/test/segment.ts/opaque" ||
			node.Mode != "packet-up" || node.UplinkHTTPMethod != "GET" || node.UplinkDataPlacement != "body" ||
			node.TransportProfileID != "profile-a" || node.CompatibilityPresetID != "preset-a" || node.TransportReleaseID != "release-a" {
			t.Fatalf("unexpected frozen CDN node contract: %#v", node)
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal active result: %v", err)
	}
	if containsJSONKey(raw, "account_id") || containsJSONKey(raw, "origin_route_id") || containsJSONKey(raw, "data_plane_instance_id") {
		t.Fatalf("public subscription contract exposed internal routing/ownership: %s", raw)
	}
}

func TestRenderedActiveNodesContainCompletePublicContractAndDomainFallback(t *testing.T) {
	entitlement, release := reviewedFixture(t)
	ordinary := OrdinarySubscription{AccountID: "account-alpha", Identity: "ordinary-subscription-alpha", Output: "opaque-existing-output"}
	result := RenderWhiteListSubscription(ordinary, entitlement, release)
	if len(result.WhiteListNodes) != 3 {
		t.Errorf("active node count=%d, want two approved edges plus domain fallback", len(result.WhiteListNodes))
	}
	for index, node := range result.WhiteListNodes {
		raw, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("marshal node: %v", err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("unmarshal node: %v", err)
		}
		for _, key := range []string{"client_id", "encryption", "security", "alpn", "fingerprint", "extra", "label", "domain_fallback"} {
			if _, ok := fields[key]; !ok {
				t.Errorf("node %d missing required public field %q: %s", index, key, raw)
			}
		}
		if got, _ := fields["client_id"].(string); got != "11111111-1111-4111-8111-111111111111" {
			t.Errorf("node %d client_id=%q", index, got)
		}
		if got, _ := fields["encryption"].(string); got != "mlkem768x25519plus.native.0rtt.test-client-material" {
			t.Errorf("node %d encryption=%q", index, got)
		}
		if got, _ := fields["security"].(string); got != "tls" {
			t.Errorf("node %d security=%q", index, got)
		}
		if got, _ := fields["fingerprint"].(string); got != "firefox" {
			t.Errorf("node %d fingerprint=%q", index, got)
		}
		if got, _ := fields["label"].(string); !strings.HasPrefix(got, "БС/Yandex ") {
			t.Errorf("node %d label=%q", index, got)
		}
		alpn, _ := fields["alpn"].([]any)
		if len(alpn) != 1 || alpn[0] != "h2" {
			t.Errorf("node %d alpn=%#v", index, fields["alpn"])
		}
		encodedExtra, _ := fields["extra"].(string)
		extra, err := url.QueryUnescape(encodedExtra)
		if err != nil || !json.Valid([]byte(extra)) {
			t.Errorf("node %d extra is not URL-encoded JSON: %q", index, encodedExtra)
		}
	}
	if len(result.WhiteListNodes) >= 3 {
		fallback := result.WhiteListNodes[2]
		raw, _ := json.Marshal(fallback)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if fallback.Address != "cdn.example.invalid" || fields["domain_fallback"] != true {
			t.Errorf("domain fallback=%#v", fallback)
		}
	}
}

func TestRenderWhiteListSubscriptionRejectsCrossAccountWithoutDroppingOrdinary(t *testing.T) {
	entitlement, release := reviewedFixture(t)
	ordinary := OrdinarySubscription{AccountID: "account-other", Identity: "ordinary-other", Output: "ordinary-payload"}
	result := RenderWhiteListSubscription(ordinary, entitlement, release)
	if result.Ordinary != ordinary || len(result.WhiteListNodes) != 0 {
		t.Fatalf("cross-account failure dropped/changed ordinary output: %#v", result)
	}
	if result.Diagnostic == nil || result.Diagnostic.Code != DiagnosticAccountMismatch {
		t.Fatalf("cross-account diagnostic=%#v", result.Diagnostic)
	}
}

func TestRenderWhiteListSubscriptionRejectsEmptyOrdinaryOutputBeforeAddingNodes(t *testing.T) {
	entitlement, release := reviewedFixture(t)
	for _, output := range []string{"", " \t\n"} {
		ordinary := OrdinarySubscription{AccountID: "account-alpha", Identity: "ordinary", Output: output}
		result := RenderWhiteListSubscription(ordinary, entitlement, release)
		if result.Ordinary != ordinary || len(result.WhiteListNodes) != 0 {
			t.Fatalf("invalid ordinary output published additive nodes: %#v", result)
		}
		if result.Diagnostic == nil || string(result.Diagnostic.Code) != "INVALID_ORDINARY" {
			t.Fatalf("invalid ordinary diagnostic=%#v", result.Diagnostic)
		}
	}
}

func TestRenderWhiteListSubscriptionReleaseMismatchFailsOnlyAdditiveNodes(t *testing.T) {
	entitlement, release := reviewedFixture(t)
	entitlement, err := entitlement.WithState(controlplane.EntitlementSuspended)
	if err != nil {
		t.Fatalf("WithState: %v", err)
	}
	entitlement, err = entitlement.Activate("profile-a", "preset-a", "release-other", entitlement.Credential())
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	ordinary := OrdinarySubscription{AccountID: "account-alpha", Identity: "ordinary", Output: "ordinary-payload"}
	result := RenderWhiteListSubscription(ordinary, entitlement, release)
	if result.Ordinary != ordinary || len(result.WhiteListNodes) != 0 {
		t.Fatalf("release mismatch dropped/changed ordinary output: %#v", result)
	}
	if result.Diagnostic == nil || result.Diagnostic.Code != DiagnosticReleaseMismatch {
		t.Fatalf("release mismatch diagnostic=%#v", result.Diagnostic)
	}
}

func containsJSONKey(raw []byte, key string) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	var walk func(any) bool
	walk = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for candidate, nested := range typed {
				if candidate == key || walk(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range typed {
				if walk(nested) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}
