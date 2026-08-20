package controlplane

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// EntitlementState is the independent lifecycle of white-list access. It is
// intentionally separate from the ordinary account status.
type EntitlementState string

const (
	EntitlementDisabled     EntitlementState = "DISABLED"
	EntitlementProvisioning EntitlementState = "PROVISIONING"
	EntitlementActive       EntitlementState = "ACTIVE"
	EntitlementGrace        EntitlementState = "GRACE"
	EntitlementSuspended    EntitlementState = "SUSPENDED"
	EntitlementError        EntitlementState = "ERROR"
	EntitlementExpired      EntitlementState = "EXPIRED"
)

// TransportProfile is the public white-list transport shape. OriginRouteID is
// an internal reference and must never be copied into a customer subscription.
type TransportProfile struct {
	ID                    string
	PublicHost            string
	SecretPath            string
	OriginRouteID         string
	CompatibilityPresetID string
}

// CompatibilityPreset pins a versioned set of client/core capabilities.
type CompatibilityPreset struct {
	ID              string
	Version         int
	ProtectionLevel string
	Capabilities    []string
}

// OriginRoute is an opaque control-plane route to one isolated data plane.
// The actual origin address remains outside subscription rendering.
type OriginRoute struct {
	ID                  string
	DataPlaneInstanceID string
}

// EdgeCandidate is discovered evidence that has not yet been approved for a
// customer subscription.
type EdgeCandidate struct {
	ID                 string
	TransportProfileID string
	Address            string
}

// ApprovedEdge is an edge candidate with auditable approval evidence.
type ApprovedEdge struct {
	ID                 string
	TransportProfileID string
	Address            string
	ApprovedAt         time.Time
	EvidenceRef        string
}

// Approve returns a new approved value and leaves the candidate unchanged.
func (candidate EdgeCandidate) Approve(approvedAt time.Time, evidenceRef string) (ApprovedEdge, error) {
	if blank(candidate.ID) || blank(candidate.TransportProfileID) || blank(candidate.Address) || approvedAt.IsZero() || blank(evidenceRef) {
		return ApprovedEdge{}, errors.New("controlplane: incomplete edge approval")
	}
	return ApprovedEdge{
		ID:                 candidate.ID,
		TransportProfileID: candidate.TransportProfileID,
		Address:            candidate.Address,
		ApprovedAt:         approvedAt,
		EvidenceRef:        evidenceRef,
	}, nil
}

// TransportReleaseState is the immutable release lifecycle marker.
type TransportReleaseState string

const (
	TransportReleaseDraft     TransportReleaseState = "DRAFT"
	TransportReleaseCandidate TransportReleaseState = "CANDIDATE"
	TransportReleasePublished TransportReleaseState = "PUBLISHED"
	TransportReleaseRetired   TransportReleaseState = "RETIRED"
)

// TransportReleaseSpec is copied by NewTransportRelease. Mutating the spec or
// its edge slice after construction cannot change the release.
type TransportReleaseSpec struct {
	ID                    string
	TransportProfileID    string
	CompatibilityPresetID string
	State                 TransportReleaseState
	ApprovedEdges         []ApprovedEdge
}

// TransportRelease is an immutable, canonical snapshot used by renderers.
type TransportRelease struct {
	id                    string
	transportProfileID    string
	compatibilityPresetID string
	state                 TransportReleaseState
	approvedEdges         []ApprovedEdge
}

func NewTransportRelease(spec TransportReleaseSpec) (TransportRelease, error) {
	if blank(spec.ID) || blank(spec.TransportProfileID) || blank(spec.CompatibilityPresetID) || !validTransportReleaseState(spec.State) {
		return TransportRelease{}, errors.New("controlplane: invalid transport release")
	}
	if spec.State == TransportReleasePublished && len(spec.ApprovedEdges) == 0 {
		return TransportRelease{}, errors.New("controlplane: published release has no approved edges")
	}

	edges := append([]ApprovedEdge(nil), spec.ApprovedEdges...)
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if blank(edge.ID) || blank(edge.Address) || edge.TransportProfileID != spec.TransportProfileID || edge.ApprovedAt.IsZero() || blank(edge.EvidenceRef) {
			return TransportRelease{}, errors.New("controlplane: invalid approved edge")
		}
		if _, exists := seen[edge.ID]; exists {
			return TransportRelease{}, errors.New("controlplane: duplicate approved edge")
		}
		seen[edge.ID] = struct{}{}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].ID == edges[j].ID {
			return edges[i].Address < edges[j].Address
		}
		return edges[i].ID < edges[j].ID
	})

	return TransportRelease{
		id:                    spec.ID,
		transportProfileID:    spec.TransportProfileID,
		compatibilityPresetID: spec.CompatibilityPresetID,
		state:                 spec.State,
		approvedEdges:         edges,
	}, nil
}

func (release TransportRelease) ID() string { return release.id }

func (release TransportRelease) TransportProfileID() string { return release.transportProfileID }

func (release TransportRelease) CompatibilityPresetID() string {
	return release.compatibilityPresetID
}

func (release TransportRelease) State() TransportReleaseState { return release.state }

// ApprovedEdges returns a defensive copy of the canonical edge order.
func (release TransportRelease) ApprovedEdges() []ApprovedEdge {
	return append([]ApprovedEdge(nil), release.approvedEdges...)
}

// WhiteListEntitlement is the per-account additive right. Its zero value is a
// safe disabled value, so omitted records cannot accidentally grant access.
type WhiteListEntitlement struct {
	accountID             string
	transportProfileID    string
	compatibilityPresetID string
	transportReleaseID    string
	state                 EntitlementState
}

func NewWhiteListEntitlement(accountID string) (WhiteListEntitlement, error) {
	if blank(accountID) {
		return WhiteListEntitlement{}, errors.New("controlplane: account id is required")
	}
	return WhiteListEntitlement{accountID: accountID, state: EntitlementDisabled}, nil
}

// Activate returns a new entitlement pinned to one profile, preset and
// immutable release. It does not mutate the disabled value.
func (entitlement WhiteListEntitlement) Activate(profileID, presetID, releaseID string) (WhiteListEntitlement, error) {
	if blank(entitlement.accountID) || blank(profileID) || blank(presetID) || blank(releaseID) {
		return WhiteListEntitlement{}, errors.New("controlplane: incomplete entitlement activation")
	}
	entitlement.transportProfileID = profileID
	entitlement.compatibilityPresetID = presetID
	entitlement.transportReleaseID = releaseID
	entitlement.state = EntitlementActive
	return entitlement, nil
}

// WithState returns a new entitlement in an explicit lifecycle state while
// preserving its pinned release references. ACTIVE requires a complete pin.
func (entitlement WhiteListEntitlement) WithState(state EntitlementState) (WhiteListEntitlement, error) {
	if blank(entitlement.accountID) || !validEntitlementState(state) {
		return WhiteListEntitlement{}, errors.New("controlplane: invalid entitlement state")
	}
	if state == EntitlementActive && (blank(entitlement.transportProfileID) || blank(entitlement.compatibilityPresetID) || blank(entitlement.transportReleaseID)) {
		return WhiteListEntitlement{}, errors.New("controlplane: active entitlement has incomplete release pin")
	}
	entitlement.state = state
	return entitlement, nil
}

func (entitlement WhiteListEntitlement) State() EntitlementState {
	if entitlement.state == "" {
		return EntitlementDisabled
	}
	return entitlement.state
}

func (entitlement WhiteListEntitlement) Active() bool {
	return entitlement.State() == EntitlementActive
}

func (entitlement WhiteListEntitlement) AccountID() string { return entitlement.accountID }

func (entitlement WhiteListEntitlement) TransportProfileID() string {
	return entitlement.transportProfileID
}

func (entitlement WhiteListEntitlement) CompatibilityPresetID() string {
	return entitlement.compatibilityPresetID
}

func (entitlement WhiteListEntitlement) TransportReleaseID() string {
	return entitlement.transportReleaseID
}

func validEntitlementState(state EntitlementState) bool {
	switch state {
	case EntitlementDisabled, EntitlementProvisioning, EntitlementActive, EntitlementGrace, EntitlementSuspended, EntitlementError, EntitlementExpired:
		return true
	default:
		return false
	}
}

func validTransportReleaseState(state TransportReleaseState) bool {
	switch state {
	case TransportReleaseDraft, TransportReleaseCandidate, TransportReleasePublished, TransportReleaseRetired:
		return true
	default:
		return false
	}
}

func blank(value string) bool { return strings.TrimSpace(value) == "" }
