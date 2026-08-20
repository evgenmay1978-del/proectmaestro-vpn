package subgen

import (
	"errors"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

// OrdinarySubscription is the existing subscription identity and serialized
// output. Rendering white-list access never replaces either field.
type OrdinarySubscription struct {
	Identity string `json:"identity"`
	Output   string `json:"output"`
}

// WhiteListNode is an additive public node descriptor. It intentionally has no
// origin route or data-plane address fields.
type WhiteListNode struct {
	Protocol              string `json:"protocol"`
	Network               string `json:"network"`
	Address               string `json:"address"`
	Port                  int    `json:"port"`
	TLS                   bool   `json:"tls"`
	ServerName            string `json:"server_name"`
	Host                  string `json:"host"`
	Path                  string `json:"path"`
	Mode                  string `json:"mode"`
	UplinkHTTPMethod      string `json:"uplink_http_method"`
	UplinkDataPlacement   string `json:"uplink_data_placement"`
	EdgeID                string `json:"edge_id"`
	TransportProfileID    string `json:"transport_profile_id"`
	CompatibilityPresetID string `json:"compatibility_preset_id"`
	TransportReleaseID    string `json:"transport_release_id"`
}

// WhiteListSubscriptionResult is the additive seam consumed by future API and
// Android tasks. WhiteListNodes is always an array, including when access is OFF.
type WhiteListSubscriptionResult struct {
	Ordinary       OrdinarySubscription `json:"ordinary"`
	WhiteListNodes []WhiteListNode      `json:"white_list_nodes"`
}

// RenderWhiteListSubscription preserves the ordinary subscription verbatim and
// adds deterministic public CDN node descriptors only for ACTIVE entitlement.
func RenderWhiteListSubscription(
	ordinary OrdinarySubscription,
	entitlement controlplane.WhiteListEntitlement,
	profile controlplane.TransportProfile,
	preset controlplane.CompatibilityPreset,
	release controlplane.TransportRelease,
) (WhiteListSubscriptionResult, error) {
	result := WhiteListSubscriptionResult{
		Ordinary:       ordinary,
		WhiteListNodes: make([]WhiteListNode, 0),
	}
	if !entitlement.Active() {
		return result, nil
	}
	if strings.TrimSpace(ordinary.Identity) == "" ||
		strings.TrimSpace(profile.ID) == "" ||
		strings.TrimSpace(profile.PublicHost) == "" ||
		!strings.HasPrefix(profile.SecretPath, "/") ||
		profile.CompatibilityPresetID != preset.ID || preset.Version <= 0 ||
		entitlement.TransportProfileID() != profile.ID ||
		entitlement.CompatibilityPresetID() != preset.ID ||
		entitlement.TransportReleaseID() != release.ID() ||
		release.TransportProfileID() != profile.ID ||
		release.CompatibilityPresetID() != preset.ID ||
		release.State() != controlplane.TransportReleasePublished {
		return WhiteListSubscriptionResult{}, errors.New("subgen: incompatible active white-list configuration")
	}

	edges := release.ApprovedEdges()
	if len(edges) == 0 {
		return WhiteListSubscriptionResult{}, errors.New("subgen: active release has no approved edges")
	}
	for _, edge := range edges {
		if edge.TransportProfileID != profile.ID {
			return WhiteListSubscriptionResult{}, errors.New("subgen: approved edge profile mismatch")
		}
		result.WhiteListNodes = append(result.WhiteListNodes, WhiteListNode{
			Protocol:              "vless",
			Network:               "xhttp",
			Address:               edge.Address,
			Port:                  443,
			TLS:                   true,
			ServerName:            profile.PublicHost,
			Host:                  profile.PublicHost,
			Path:                  profile.SecretPath,
			Mode:                  "packet-up",
			UplinkHTTPMethod:      "GET",
			UplinkDataPlacement:   "body",
			EdgeID:                edge.ID,
			TransportProfileID:    profile.ID,
			CompatibilityPresetID: preset.ID,
			TransportReleaseID:    release.ID(),
		})
	}
	return result, nil
}
