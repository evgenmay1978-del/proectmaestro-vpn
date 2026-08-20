package controlplane_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestWhiteListEntitlementDefaultsDisabled(t *testing.T) {
	var zero controlplane.WhiteListEntitlement
	if zero.State() != controlplane.EntitlementDisabled {
		t.Fatalf("zero-value state = %q, want %q", zero.State(), controlplane.EntitlementDisabled)
	}
	if zero.Active() {
		t.Fatal("zero-value entitlement is active; white-list access must default OFF")
	}

	entitlement, err := controlplane.NewWhiteListEntitlement("account-alpha")
	if err != nil {
		t.Fatalf("NewWhiteListEntitlement: %v", err)
	}
	if entitlement.State() != controlplane.EntitlementDisabled || entitlement.Active() {
		t.Fatalf("new entitlement state = %q active=%v, want disabled", entitlement.State(), entitlement.Active())
	}
}

func TestWhiteListEntitlementActivationPinsAdditiveReferences(t *testing.T) {
	disabled, err := controlplane.NewWhiteListEntitlement("account-alpha")
	if err != nil {
		t.Fatalf("NewWhiteListEntitlement: %v", err)
	}
	active, err := disabled.Activate("profile-a", "preset-a", "release-a")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if disabled.Active() {
		t.Fatal("Activate mutated the disabled value")
	}
	if !active.Active() || active.State() != controlplane.EntitlementActive {
		t.Fatalf("active state = %q active=%v", active.State(), active.Active())
	}
	if active.AccountID() != "account-alpha" || active.TransportProfileID() != "profile-a" ||
		active.CompatibilityPresetID() != "preset-a" || active.TransportReleaseID() != "release-a" {
		t.Fatalf("activation did not pin account/profile/preset/release: %#v", active)
	}
}

func TestTransportReleaseIsImmutableAndCanonical(t *testing.T) {
	edges := []controlplane.ApprovedEdge{
		{ID: "edge-b", TransportProfileID: "profile-a", Address: "203.0.113.12", ApprovedAt: time.Unix(20, 0), EvidenceRef: "evidence-b"},
		{ID: "edge-a", TransportProfileID: "profile-a", Address: "203.0.113.11", ApprovedAt: time.Unix(10, 0), EvidenceRef: "evidence-a"},
	}
	release, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID:                    "release-a",
		TransportProfileID:    "profile-a",
		CompatibilityPresetID: "preset-a",
		State:                 controlplane.TransportReleasePublished,
		ApprovedEdges:         edges,
	})
	if err != nil {
		t.Fatalf("NewTransportRelease: %v", err)
	}

	edges[0].Address = "198.51.100.99"
	firstRead := release.ApprovedEdges()
	wantAddresses := []string{"203.0.113.11", "203.0.113.12"}
	gotAddresses := []string{firstRead[0].Address, firstRead[1].Address}
	if !reflect.DeepEqual(gotAddresses, wantAddresses) {
		t.Fatalf("canonical release addresses = %v, want %v", gotAddresses, wantAddresses)
	}

	firstRead[0].Address = "198.51.100.100"
	secondRead := release.ApprovedEdges()
	if secondRead[0].Address != "203.0.113.11" {
		t.Fatalf("release exposed mutable edge slice: %q", secondRead[0].Address)
	}
	if release.ID() != "release-a" || release.TransportProfileID() != "profile-a" ||
		release.CompatibilityPresetID() != "preset-a" || release.State() != controlplane.TransportReleasePublished {
		t.Fatalf("release identity changed: id=%q profile=%q preset=%q state=%q", release.ID(), release.TransportProfileID(), release.CompatibilityPresetID(), release.State())
	}
}

func TestEdgeCandidateApprovalPreservesCandidateIdentity(t *testing.T) {
	candidate := controlplane.EdgeCandidate{ID: "edge-a", TransportProfileID: "profile-a", Address: "203.0.113.11"}
	approved, err := candidate.Approve(time.Unix(10, 0), "evidence-a")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.ID != candidate.ID || approved.TransportProfileID != candidate.TransportProfileID || approved.Address != candidate.Address {
		t.Fatalf("approved edge lost candidate identity: %#v", approved)
	}
	if approved.ApprovedAt != time.Unix(10, 0) || approved.EvidenceRef != "evidence-a" {
		t.Fatalf("approval evidence not retained: %#v", approved)
	}
}
