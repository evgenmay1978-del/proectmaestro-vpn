package subgen

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

// OrdinarySubscription is the existing subscription identity and serialized
// output. AccountID binds the internal aggregate but is never serialized.
type OrdinarySubscription struct {
	AccountID string `json:"-"`
	Identity  string `json:"identity"`
	Output    string `json:"output"`
}

// WhiteListNode is an additive public node descriptor. It intentionally has no
// origin route or data-plane address fields.
type WhiteListNode struct {
	Protocol              string   `json:"protocol"`
	Network               string   `json:"network"`
	Address               string   `json:"address"`
	Port                  int      `json:"port"`
	TLS                   bool     `json:"tls"`
	ServerName            string   `json:"server_name"`
	Host                  string   `json:"host"`
	Path                  string   `json:"path"`
	Mode                  string   `json:"mode"`
	UplinkHTTPMethod      string   `json:"uplink_http_method"`
	UplinkDataPlacement   string   `json:"uplink_data_placement"`
	ClientID              string   `json:"client_id"`
	Encryption            string   `json:"encryption"`
	Security              string   `json:"security"`
	ALPN                  []string `json:"alpn"`
	Fingerprint           string   `json:"fingerprint"`
	Extra                 string   `json:"extra"`
	Label                 string   `json:"label"`
	DomainFallback        bool     `json:"domain_fallback"`
	EdgeID                string   `json:"edge_id"`
	TransportProfileID    string   `json:"transport_profile_id"`
	CompatibilityPresetID string   `json:"compatibility_preset_id"`
	TransportReleaseID    string   `json:"transport_release_id"`
}

// DiagnosticCode is a redacted additive-render failure category.
type DiagnosticCode string

const (
	DiagnosticInvalidOrdinary DiagnosticCode = "INVALID_ORDINARY"
	DiagnosticAccountMismatch DiagnosticCode = "ACCOUNT_MISMATCH"
	DiagnosticReleaseMismatch DiagnosticCode = "RELEASE_MISMATCH"
	DiagnosticInvalidRelease  DiagnosticCode = "INVALID_RELEASE"
)

// WhiteListDiagnostic reports only why CDN nodes were omitted. Ordinary output
// remains available and no internal addresses or secrets are included.
type WhiteListDiagnostic struct {
	Code DiagnosticCode `json:"code"`
}

// WhiteListSubscriptionResult is the additive seam consumed by future API and
// Android tasks. WhiteListNodes is always an array, including when access is OFF.
type WhiteListSubscriptionResult struct {
	Ordinary       OrdinarySubscription `json:"ordinary"`
	WhiteListNodes []WhiteListNode      `json:"white_list_nodes"`
	Diagnostic     *WhiteListDiagnostic `json:"white_list_error,omitempty"`
}

// RenderWhiteListSubscription cannot fail the ordinary subscription path. It
// adds CDN nodes only for an account-bound ACTIVE entitlement whose immutable
// release pin matches exactly.
func RenderWhiteListSubscription(
	ordinary OrdinarySubscription,
	entitlement controlplane.WhiteListEntitlement,
	release controlplane.TransportRelease,
) WhiteListSubscriptionResult {
	result := WhiteListSubscriptionResult{
		Ordinary:       ordinary,
		WhiteListNodes: make([]WhiteListNode, 0),
	}
	if !entitlement.Active() {
		return result
	}
	if strings.TrimSpace(ordinary.AccountID) == "" || strings.TrimSpace(ordinary.Identity) == "" || strings.TrimSpace(ordinary.Output) == "" {
		return withDiagnostic(result, DiagnosticInvalidOrdinary)
	}
	if ordinary.AccountID != entitlement.AccountID() {
		return withDiagnostic(result, DiagnosticAccountMismatch)
	}
	if entitlement.TransportProfileID() != release.TransportProfileID() ||
		entitlement.CompatibilityPresetID() != release.CompatibilityPresetID() ||
		entitlement.TransportReleaseID() != release.ID() {
		return withDiagnostic(result, DiagnosticReleaseMismatch)
	}
	if release.State() != controlplane.TransportReleasePublished {
		return withDiagnostic(result, DiagnosticInvalidRelease)
	}

	profile := release.Profile()
	preset := release.Preset()
	edges := release.ApprovedEdges()
	if len(edges) == 0 {
		return withDiagnostic(result, DiagnosticInvalidRelease)
	}
	credential := entitlement.Credential()
	for _, edge := range edges {
		if edge.TransportProfileID != profile.ID {
			result.WhiteListNodes = result.WhiteListNodes[:0]
			return withDiagnostic(result, DiagnosticInvalidRelease)
		}
		result.WhiteListNodes = append(result.WhiteListNodes, buildWhiteListNode(
			edge.Address, edge.ID, edge.ID, false, profile, preset, release.ID(), credential,
		))
	}
	if preset.DomainFallback {
		result.WhiteListNodes = append(result.WhiteListNodes, buildWhiteListNode(
			profile.PublicHost, "", "fallback", true, profile, preset, release.ID(), credential,
		))
	}
	return result
}

func buildWhiteListNode(
	address, edgeID, labelSuffix string,
	domainFallback bool,
	profile controlplane.TransportProfile,
	preset controlplane.CompatibilityPreset,
	releaseID string,
	credential controlplane.WhiteListCredential,
) WhiteListNode {
	return WhiteListNode{
		Protocol:              preset.Protocol,
		Network:               preset.Network,
		Address:               address,
		Port:                  preset.Port,
		TLS:                   preset.TLS,
		ServerName:            profile.PublicHost,
		Host:                  profile.PublicHost,
		Path:                  profile.SecretPath,
		Mode:                  preset.Mode,
		UplinkHTTPMethod:      preset.UplinkHTTPMethod,
		UplinkDataPlacement:   preset.UplinkDataPlacement,
		ClientID:              credential.ClientID,
		Encryption:            credential.ClientEncryption,
		Security:              "tls",
		ALPN:                  append([]string(nil), preset.ALPN...),
		Fingerprint:           preset.Fingerprint,
		Extra:                 url.QueryEscape(preset.ExtraJSON),
		Label:                 fmt.Sprintf("%s %s", preset.LabelPrefix, labelSuffix),
		DomainFallback:        domainFallback,
		EdgeID:                edgeID,
		TransportProfileID:    profile.ID,
		CompatibilityPresetID: preset.ID,
		TransportReleaseID:    releaseID,
	}
}

func withDiagnostic(result WhiteListSubscriptionResult, code DiagnosticCode) WhiteListSubscriptionResult {
	result.Diagnostic = &WhiteListDiagnostic{Code: code}
	return result
}
