package controlplane

import (
	"errors"
	"net"
	"path"
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

// CompatibilityPreset is the versioned client/core and wire contract frozen
// into every immutable transport release.
type CompatibilityPreset struct {
	ID                  string
	Version             int
	Kind                string
	ProtectionLevel     string
	Capabilities        []string
	CoreRange           string
	ClientRanges        []string
	FixtureRefs         []string
	Protocol            string
	Network             string
	Port                int
	TLS                 bool
	Mode                string
	UplinkHTTPMethod    string
	UplinkDataPlacement string
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
	if blank(candidate.ID) || blank(candidate.TransportProfileID) || !validEdgeAddress(candidate.Address) || approvedAt.IsZero() || blank(evidenceRef) {
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

// TransportReleaseSpec is copied by NewTransportRelease. Mutating any caller
// owned profile, preset or edge slice after construction cannot change release
// output.
type TransportReleaseSpec struct {
	ID            string
	Profile       TransportProfile
	Preset        CompatibilityPreset
	State         TransportReleaseState
	ApprovedEdges []ApprovedEdge
}

// TransportRelease is an immutable, canonical snapshot used by renderers.
type TransportRelease struct {
	id            string
	profile       TransportProfile
	preset        CompatibilityPreset
	state         TransportReleaseState
	approvedEdges []ApprovedEdge
}

func NewTransportRelease(spec TransportReleaseSpec) (TransportRelease, error) {
	if blank(spec.ID) || !validTransportReleaseState(spec.State) || !validProfile(spec.Profile) || !validPreset(spec.Preset) || spec.Profile.CompatibilityPresetID != spec.Preset.ID {
		return TransportRelease{}, errors.New("controlplane: invalid transport release")
	}
	if spec.State == TransportReleasePublished && len(spec.ApprovedEdges) == 0 {
		return TransportRelease{}, errors.New("controlplane: published release has no approved edges")
	}

	edges := append([]ApprovedEdge(nil), spec.ApprovedEdges...)
	seenIDs := make(map[string]struct{}, len(edges))
	seenAddresses := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if blank(edge.ID) || !validEdgeAddress(edge.Address) || edge.TransportProfileID != spec.Profile.ID || edge.ApprovedAt.IsZero() || blank(edge.EvidenceRef) {
			return TransportRelease{}, errors.New("controlplane: invalid approved edge")
		}
		if _, exists := seenIDs[edge.ID]; exists {
			return TransportRelease{}, errors.New("controlplane: duplicate approved edge id")
		}
		if _, exists := seenAddresses[edge.Address]; exists {
			return TransportRelease{}, errors.New("controlplane: duplicate approved edge address")
		}
		seenIDs[edge.ID] = struct{}{}
		seenAddresses[edge.Address] = struct{}{}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].ID == edges[j].ID {
			return edges[i].Address < edges[j].Address
		}
		return edges[i].ID < edges[j].ID
	})

	return TransportRelease{
		id:            spec.ID,
		profile:       spec.Profile,
		preset:        clonePreset(spec.Preset),
		state:         spec.State,
		approvedEdges: edges,
	}, nil
}

func (release TransportRelease) ID() string { return release.id }

func (release TransportRelease) TransportProfileID() string { return release.profile.ID }

func (release TransportRelease) CompatibilityPresetID() string { return release.preset.ID }

func (release TransportRelease) State() TransportReleaseState { return release.state }

// Profile returns the immutable public profile snapshot.
func (release TransportRelease) Profile() TransportProfile { return release.profile }

// Preset returns a defensive copy of the immutable preset snapshot.
func (release TransportRelease) Preset() CompatibilityPreset { return clonePreset(release.preset) }

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

func clonePreset(preset CompatibilityPreset) CompatibilityPreset {
	preset.Capabilities = append([]string(nil), preset.Capabilities...)
	preset.ClientRanges = append([]string(nil), preset.ClientRanges...)
	preset.FixtureRefs = append([]string(nil), preset.FixtureRefs...)
	return preset
}

func validProfile(profile TransportProfile) bool {
	return !blank(profile.ID) && validPublicHost(profile.PublicHost) && validSecretPath(profile.SecretPath) &&
		!blank(profile.OriginRouteID) && !blank(profile.CompatibilityPresetID)
}

func validPreset(preset CompatibilityPreset) bool {
	return !blank(preset.ID) && preset.Version > 0 && !blank(preset.Kind) && !blank(preset.ProtectionLevel) &&
		allNonBlank(preset.Capabilities) && !blank(preset.CoreRange) && allNonBlank(preset.ClientRanges) && allNonBlank(preset.FixtureRefs) &&
		preset.Protocol == "vless" && preset.Network == "xhttp" && preset.Port == 443 && preset.TLS &&
		preset.Mode == "packet-up" && preset.UplinkHTTPMethod == "GET" && preset.UplinkDataPlacement == "body"
}

func validPublicHost(host string) bool {
	if host != strings.TrimSpace(host) || len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, ":/?#") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func validSecretPath(value string) bool {
	return len(value) > 1 && strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "?#") && path.Clean(value) == value
}

func validEdgeAddress(value string) bool {
	parsed := net.ParseIP(value)
	return parsed != nil && parsed.To4() != nil && parsed.String() == value
}

func allNonBlank(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if blank(value) {
			return false
		}
	}
	return true
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
