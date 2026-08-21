package release

import (
	"sort"
	"time"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type canonicalTransport struct {
	ReleaseID string                  `json:"release_id"`
	Profile   canonicalProfile        `json:"profile"`
	Preset    canonicalPreset         `json:"preset"`
	Edges     []canonicalApprovedEdge `json:"approved_edges"`
}

type canonicalProfile struct {
	ID                    string `json:"id"`
	PublicHost            string `json:"public_host"`
	SecretPath            string `json:"secret_path"`
	OriginRouteID         string `json:"origin_route_id"`
	CompatibilityPresetID string `json:"compatibility_preset_id"`
}

type canonicalPreset struct {
	ID                  string   `json:"id"`
	Version             int      `json:"version"`
	Kind                string   `json:"kind"`
	ProtectionLevel     string   `json:"protection_level"`
	Capabilities        []string `json:"capabilities"`
	CoreRange           string   `json:"core_range"`
	ClientRanges        []string `json:"client_ranges"`
	FixtureRefs         []string `json:"fixture_refs"`
	Protocol            string   `json:"protocol"`
	Network             string   `json:"network"`
	Port                int      `json:"port"`
	TLS                 bool     `json:"tls"`
	Mode                string   `json:"mode"`
	UplinkHTTPMethod    string   `json:"uplink_http_method"`
	UplinkDataPlacement string   `json:"uplink_data_placement"`
	ALPN                []string `json:"alpn"`
	Fingerprint         string   `json:"fingerprint"`
	ExtraJSON           string   `json:"extra_json"`
	LabelPrefix         string   `json:"label_prefix"`
	DomainFallback      bool     `json:"domain_fallback"`
}

type canonicalApprovedEdge struct {
	ID                 string `json:"id"`
	TransportProfileID string `json:"transport_profile_id"`
	Address            string `json:"address"`
	ApprovedAt         string `json:"approved_at"`
	EvidenceRef        string `json:"evidence_ref"`
}

type candidateBinding struct {
	SchemaVersion         int    `json:"schema_version"`
	ReleaseID             string `json:"release_id"`
	Generation            uint64 `json:"generation"`
	TransportProfileID    string `json:"transport_profile_id"`
	CompatibilityPresetID string `json:"compatibility_preset_id"`
	TransportSHA256       string `json:"transport_sha256"`
	RuntimeMaterialSHA256 string `json:"runtime_material_sha256"`
	EvidenceTrustSHA256   string `json:"evidence_trust_sha256"`
	XrayVersion           string `json:"xray_version"`
	XrayCommit            string `json:"xray_commit"`
	XraySource            string `json:"xray_source"`
	XrayBinarySHA256      string `json:"xray_binary_sha256"`
	ConfigSHA256          string `json:"config_sha256"`
	SystemdSHA256         string `json:"systemd_sha256"`
	RollbackSHA256        string `json:"rollback_sha256"`
}

func TransportSHA256(transport controlplane.TransportRelease) (string, error) {
	if !transportStringsUTF8(transport) {
		return "", invalid("transport_binding_invalid")
	}
	profile := transport.Profile()
	preset := transport.Preset()
	edges := transport.ApprovedEdges()
	if _, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: transport.ID(), Profile: profile, Preset: preset,
		State: transport.State(), ApprovedEdges: edges,
	}); err != nil {
		return "", invalid("transport_binding_invalid")
	}
	canonicalEdges := make([]canonicalApprovedEdge, 0, len(edges))
	for _, edge := range edges {
		canonicalEdges = append(canonicalEdges, canonicalApprovedEdge{
			ID: edge.ID, TransportProfileID: edge.TransportProfileID,
			Address: edge.Address, ApprovedAt: edge.ApprovedAt.UTC().Format(time.RFC3339Nano),
			EvidenceRef: edge.EvidenceRef,
		})
	}
	sort.Slice(canonicalEdges, func(i, j int) bool {
		if canonicalEdges[i].ID == canonicalEdges[j].ID {
			return canonicalEdges[i].Address < canonicalEdges[j].Address
		}
		return canonicalEdges[i].ID < canonicalEdges[j].ID
	})
	value := canonicalTransport{
		ReleaseID: transport.ID(),
		Profile: canonicalProfile{
			ID: profile.ID, PublicHost: profile.PublicHost, SecretPath: profile.SecretPath,
			OriginRouteID: profile.OriginRouteID, CompatibilityPresetID: profile.CompatibilityPresetID,
		},
		Preset: canonicalPreset{
			ID: preset.ID, Version: preset.Version, Kind: preset.Kind,
			ProtectionLevel: preset.ProtectionLevel,
			Capabilities:    append([]string(nil), preset.Capabilities...),
			CoreRange:       preset.CoreRange, ClientRanges: append([]string(nil), preset.ClientRanges...),
			FixtureRefs: append([]string(nil), preset.FixtureRefs...), Protocol: preset.Protocol,
			Network: preset.Network, Port: preset.Port, TLS: preset.TLS, Mode: preset.Mode,
			UplinkHTTPMethod: preset.UplinkHTTPMethod, UplinkDataPlacement: preset.UplinkDataPlacement,
			ALPN: append([]string(nil), preset.ALPN...), Fingerprint: preset.Fingerprint,
			ExtraJSON: preset.ExtraJSON, LabelPrefix: preset.LabelPrefix,
			DomainFallback: preset.DomainFallback,
		},
		Edges: canonicalEdges,
	}
	raw, err := marshalCanonical(value)
	if err != nil {
		return "", invalid("transport_binding_encode")
	}
	return digestBytes(raw), nil
}

func transportStringsUTF8(transport controlplane.TransportRelease) bool {
	profile := transport.Profile()
	preset := transport.Preset()
	values := []string{
		transport.ID(),
		profile.ID, profile.PublicHost, profile.SecretPath, profile.OriginRouteID, profile.CompatibilityPresetID,
		preset.ID, preset.Kind, preset.ProtectionLevel, preset.CoreRange, preset.Protocol, preset.Network,
		preset.Mode, preset.UplinkHTTPMethod, preset.UplinkDataPlacement, preset.Fingerprint,
		preset.ExtraJSON, preset.LabelPrefix,
	}
	values = append(values, preset.Capabilities...)
	values = append(values, preset.ClientRanges...)
	values = append(values, preset.FixtureRefs...)
	values = append(values, preset.ALPN...)
	for _, edge := range transport.ApprovedEdges() {
		values = append(values, edge.ID, edge.TransportProfileID, edge.Address, edge.EvidenceRef)
	}
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

func CandidateSHA256(spec CandidateSpec) (string, error) {
	if err := validateCandidateInputs(spec); err != nil {
		return "", err
	}
	transportSHA, err := TransportSHA256(spec.Transport)
	if err != nil {
		return "", err
	}
	trustSHA, err := spec.EvidenceTrust.SHA256()
	if err != nil {
		return "", err
	}
	return candidateBindingSHA256(candidateBinding{
		SchemaVersion: SchemaVersion, ReleaseID: spec.Transport.ID(), Generation: spec.Generation,
		TransportProfileID:    spec.Transport.TransportProfileID(),
		CompatibilityPresetID: spec.Transport.CompatibilityPresetID(),
		TransportSHA256:       transportSHA, RuntimeMaterialSHA256: spec.RuntimeMaterialSHA256,
		EvidenceTrustSHA256: trustSHA, XrayVersion: spec.XrayVersion,
		XrayCommit: spec.XrayCommit, XraySource: spec.XraySource,
		XrayBinarySHA256: spec.XrayBinarySHA256, ConfigSHA256: digestBytes(spec.ConfigJSON),
		SystemdSHA256: digestBytes(spec.SystemdUnit), RollbackSHA256: digestBytes(spec.RollbackJSON),
	})
}

func candidateSHA256FromManifestArtifacts(manifest Manifest, artifacts map[string][]byte) (string, error) {
	config, configOK := artifacts["config.json"]
	systemd, systemdOK := artifacts["maestro-xray-cdn.service"]
	rollback, rollbackOK := artifacts["rollback.json"]
	xray, xrayOK := artifacts["xray"]
	if !configOK || !systemdOK || !rollbackOK || !xrayOK {
		return "", invalid("candidate_binding_artifact_missing")
	}
	return candidateBindingSHA256(candidateBinding{
		SchemaVersion: manifest.SchemaVersion, ReleaseID: manifest.ReleaseID,
		Generation: manifest.Generation, TransportProfileID: manifest.TransportProfileID,
		CompatibilityPresetID: manifest.CompatibilityPresetID,
		TransportSHA256:       manifest.TransportSHA256,
		RuntimeMaterialSHA256: manifest.RuntimeMaterialSHA256,
		EvidenceTrustSHA256:   manifest.EvidenceTrustSHA256,
		XrayVersion:           manifest.XrayVersion, XrayCommit: manifest.XrayCommit,
		XraySource: manifest.XraySource, XrayBinarySHA256: digestBytes(xray),
		ConfigSHA256: digestBytes(config), SystemdSHA256: digestBytes(systemd),
		RollbackSHA256: digestBytes(rollback),
	})
}

func candidateBindingSHA256(binding candidateBinding) (string, error) {
	raw, err := marshalCanonical(binding)
	if err != nil {
		return "", invalid("candidate_binding_encode")
	}
	return digestBytes(raw), nil
}
