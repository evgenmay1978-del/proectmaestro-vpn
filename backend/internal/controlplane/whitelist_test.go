package controlplane_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/whitelistfixture"
)

func validProfile() controlplane.TransportProfile {
	return controlplane.TransportProfile{
		ID:                    "profile-a",
		PublicHost:            "cdn.example.invalid",
		SecretPath:            "/static/test/segment.ts/opaque",
		OriginRouteID:         "origin-route-a",
		CompatibilityPresetID: "preset-a",
	}
}

func validPreset() controlplane.CompatibilityPreset {
	return controlplane.CompatibilityPreset{
		ID: "preset-a", Version: 1, Kind: "MAESTRO_ADVANCED", ProtectionLevel: "advanced",
		Capabilities: []string{"vless-encryption", "xhttp-get-body"},
		CoreRange:    "xray>=26.7.28", ClientRanges: []string{"maestrovpn>=154"}, FixtureRefs: []string{"fixture-a"},
		Protocol: "vless", Network: "xhttp", Port: 443, TLS: true,
		Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ALPN: []string{"h2"}, Fingerprint: "firefox", ExtraJSON: `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id"}`, LabelPrefix: "БС/Yandex", DomainFallback: true,
	}
}

func validCredential() controlplane.WhiteListCredential {
	return controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	}
}

func validApprovedEdges() []controlplane.ApprovedEdge {
	return []controlplane.ApprovedEdge{
		{ID: "edge-b", TransportProfileID: "profile-a", Address: "1.1.1.11", ApprovedAt: time.Unix(20, 0), EvidenceRef: "evidence-b"},
		{ID: "edge-a", TransportProfileID: "profile-a", Address: "8.8.8.12", ApprovedAt: time.Unix(10, 0), EvidenceRef: "evidence-a"},
	}
}

func TestWhiteListEntitlementDefaultsDisabled(t *testing.T) {
	var zero controlplane.WhiteListEntitlement
	if zero.State() != controlplane.EntitlementDisabled || zero.Active() {
		t.Fatalf("zero-value entitlement granted access: state=%q active=%v", zero.State(), zero.Active())
	}
	entitlement := whitelistfixture.MustPersisted(t, "account-alpha")
	if entitlement.State() != controlplane.EntitlementDisabled || entitlement.Active() {
		t.Fatalf("new entitlement state=%q active=%v, want disabled", entitlement.State(), entitlement.Active())
	}
}

func TestWhiteListEntitlementRepresentsEveryExplicitStateAndRejectsUnknown(t *testing.T) {
	disabled := whitelistfixture.MustPersisted(t, "account-alpha")
	seed, err := disabled.Activate("profile-a", "preset-a", "release-a", validCredential())
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	states := []controlplane.EntitlementState{
		controlplane.EntitlementDisabled,
		controlplane.EntitlementProvisioning,
		controlplane.EntitlementActive,
		controlplane.EntitlementGrace,
		controlplane.EntitlementSuspended,
		controlplane.EntitlementError,
		controlplane.EntitlementExpired,
	}
	for _, state := range states {
		got, err := seed.WithState(state)
		if err != nil {
			t.Fatalf("WithState(%q): %v", state, err)
		}
		if got.State() != state || got.Active() != (state == controlplane.EntitlementActive) {
			t.Errorf("WithState(%q): state=%q active=%v", state, got.State(), got.Active())
		}
		if got.AccountID() != "account-alpha" || got.TransportProfileID() != "profile-a" ||
			got.CompatibilityPresetID() != "preset-a" || got.TransportReleaseID() != "release-a" || got.Credential() != validCredential() {
			t.Errorf("WithState(%q) discarded pinned references: %#v", state, got)
		}
	}
	if _, err := seed.WithState(controlplane.EntitlementState("UNKNOWN")); err == nil {
		t.Fatal("WithState accepted an unknown lifecycle state")
	}
}

func TestActivateRejectsUnverifiedClientEncryptionMaterial(t *testing.T) {
	disabled := whitelistfixture.MustPersisted(t, "account-alpha")
	tests := []struct {
		name   string
		mutate func(*controlplane.WhiteListCredential)
	}{
		{name: "nil uuid", mutate: func(value *controlplane.WhiteListCredential) { value.ClientID = "00000000-0000-0000-0000-000000000000" }},
		{name: "none", mutate: func(value *controlplane.WhiteListCredential) { value.ClientEncryption = "none" }},
		{name: "opaque legacy", mutate: func(value *controlplane.WhiteListCredential) { value.ClientEncryption = "opaque-legacy-token" }},
		{name: "server material", mutate: func(value *controlplane.WhiteListCredential) {
			value.ClientEncryption = "mlkem768x25519plus.native.0rtt.server-decryption-material"
		}},
		{name: "server role", mutate: func(value *controlplane.WhiteListCredential) { value.ClientEncryptionRole = "SERVER" }},
		{name: "missing proof", mutate: func(value *controlplane.WhiteListCredential) { value.ClientEncryptionProofRef = "" }},
		{name: "bad prefix", mutate: func(value *controlplane.WhiteListCredential) {
			value.ClientEncryption = "legacy.native.0rtt.abcdefghijklmnop"
		}},
		{name: "short material", mutate: func(value *controlplane.WhiteListCredential) {
			value.ClientEncryption = "mlkem768x25519plus.native.0rtt.short"
		}},
		{name: "illegal material character", mutate: func(value *controlplane.WhiteListCredential) {
			value.ClientEncryption = "mlkem768x25519plus.native.0rtt.invalid/material-value"
		}},
		{name: "material proof mismatch", mutate: func(value *controlplane.WhiteListCredential) {
			value.ClientEncryption = "mlkem768x25519plus.native.0rtt.different-client-material"
		}},
		{name: "forged proof", mutate: func(value *controlplane.WhiteListCredential) {
			value.ClientEncryptionProofRef = "xray-vlessenc-client-v1:sha256:0000000000000000000000000000000000000000000000000000000000000000"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credential := validCredential()
			test.mutate(&credential)
			if _, err := disabled.Activate("profile-a", "preset-a", "release-a", credential); err == nil {
				t.Fatalf("Activate accepted invalid credential: %#v", credential)
			}
		})
	}
}

func TestTransportReleaseRejectsEmptyOrIncompleteAdvancedMetadata(t *testing.T) {
	for _, extra := range []string{"{}", `{"sessionIDPlacement":"query"}`} {
		preset := validPreset()
		preset.ExtraJSON = extra
		if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
			ID: "release-a", Profile: validProfile(), Preset: preset,
			State: controlplane.TransportReleasePublished, ApprovedEdges: validApprovedEdges(),
		}); err == nil {
			t.Errorf("release accepted incomplete advanced metadata: %s", extra)
		}
	}
}

func TestTransportReleaseFreezesProfilePresetAndCanonicalEdges(t *testing.T) {
	profile := validProfile()
	preset := validPreset()
	edges := validApprovedEdges()
	release, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: "release-a", Profile: profile, Preset: preset,
		State: controlplane.TransportReleasePublished, ApprovedEdges: edges,
	})
	if err != nil {
		t.Fatalf("NewTransportRelease: %v", err)
	}

	profile.PublicHost = "mutated.example.invalid"
	preset.Capabilities[0] = "mutated"
	edges[0].Address = "9.9.9.99"

	frozenProfile := release.Profile()
	frozenPreset := release.Preset()
	frozenEdges := release.ApprovedEdges()
	if frozenProfile.PublicHost != "cdn.example.invalid" || frozenPreset.Capabilities[0] != "vless-encryption" {
		t.Fatalf("release did not freeze profile/preset: profile=%#v preset=%#v", frozenProfile, frozenPreset)
	}
	gotAddresses := []string{frozenEdges[0].Address, frozenEdges[1].Address}
	wantAddresses := []string{"8.8.8.12", "1.1.1.11"}
	if !reflect.DeepEqual(gotAddresses, wantAddresses) {
		t.Fatalf("edge order=%v, want ID-canonical %v", gotAddresses, wantAddresses)
	}

	frozenPreset.Capabilities[0] = "caller-mutated"
	frozenEdges[0].Address = "9.9.9.100"
	if release.Preset().Capabilities[0] != "vless-encryption" || release.ApprovedEdges()[0].Address != "8.8.8.12" {
		t.Fatal("release getters exposed mutable slices")
	}
}

func TestTransportReleaseRejectsDuplicateAddressAndMalformedPublicMaterial(t *testing.T) {
	profile := validProfile()
	preset := validPreset()
	edges := validApprovedEdges()
	edges[1].Address = edges[0].Address
	if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: "release-a", Profile: profile, Preset: preset,
		State: controlplane.TransportReleasePublished, ApprovedEdges: edges,
	}); err == nil {
		t.Fatal("release accepted duplicate edge address")
	}

	profile.PublicHost = "https://internal.example.invalid/path"
	if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: "release-a", Profile: profile, Preset: preset,
		State: controlplane.TransportReleasePublished, ApprovedEdges: validApprovedEdges(),
	}); err == nil {
		t.Fatal("release accepted malformed public host")
	}
}

func TestTransportReleaseRejectsPresetMixingAndUnsafePublicMaterial(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*controlplane.TransportProfile, *controlplane.CompatibilityPreset, *[]controlplane.ApprovedEdge)
	}{
		{name: "mixed preset capabilities", mutate: func(_ *controlplane.TransportProfile, preset *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			preset.Capabilities = []string{"xhttp-get-body"}
		}},
		{name: "downgraded protection", mutate: func(_ *controlplane.TransportProfile, preset *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			preset.ProtectionLevel = "compatibility"
		}},
		{name: "numeric public host", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.PublicHost = "203.0.113.7"
		}},
		{name: "private edge", mutate: func(_ *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, edges *[]controlplane.ApprovedEdge) {
			(*edges)[0].Address = "10.0.0.7"
		}},
		{name: "loopback edge", mutate: func(_ *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, edges *[]controlplane.ApprovedEdge) {
			(*edges)[0].Address = "127.0.0.1"
		}},
		{name: "space in path", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/bad path"
		}},
		{name: "invalid path escape", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%ZZ"
		}},
		{name: "escaped backslash", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%5Cpath"
		}},
		{name: "encoded dot traversal", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%2e%2e/admin"
		}},
		{name: "encoded separator", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%2Fadmin"
		}},
		{name: "double encoded traversal", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%252e%252e/admin"
		}},
		{name: "double encoded separator", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%252Fadmin"
		}},
		{name: "invalid utf8", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/%FF"
		}},
		{name: "raw invalid utf8", mutate: func(profile *controlplane.TransportProfile, _ *controlplane.CompatibilityPreset, _ *[]controlplane.ApprovedEdge) {
			profile.SecretPath = "/static/\xff"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := validProfile()
			preset := validPreset()
			edges := validApprovedEdges()
			test.mutate(&profile, &preset, &edges)
			if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
				ID: "release-a", Profile: profile, Preset: preset,
				State: controlplane.TransportReleasePublished, ApprovedEdges: edges,
			}); err == nil {
				t.Fatal("release accepted mixed preset or unsafe public material")
			}
		})
	}
}
func TestTransportReleaseRejectsReservedEdgeRanges(t *testing.T) {
	addresses := []string{
		"100.64.0.1",
		"192.0.0.9",
		"192.0.2.1",
		"192.88.99.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
	}
	for _, address := range addresses {
		t.Run(address, func(t *testing.T) {
			edges := validApprovedEdges()
			edges[0].Address = address
			if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
				ID: "release-a", Profile: validProfile(), Preset: validPreset(),
				State: controlplane.TransportReleasePublished, ApprovedEdges: edges,
			}); err == nil {
				t.Fatalf("release accepted reserved edge address %q", address)
			}
		})
	}
}

func TestEdgeCandidateApprovalPreservesCandidateIdentity(t *testing.T) {
	candidate := controlplane.EdgeCandidate{ID: "edge-a", TransportProfileID: "profile-a", Address: "1.1.1.11"}
	approved, err := candidate.Approve(time.Unix(10, 0), "evidence-a")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.ID != candidate.ID || approved.TransportProfileID != candidate.TransportProfileID || approved.Address != candidate.Address ||
		approved.ApprovedAt != time.Unix(10, 0) || approved.EvidenceRef != "evidence-a" {
		t.Fatalf("approved edge lost identity/evidence: %#v", approved)
	}
}
