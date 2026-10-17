package subgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// WhiteListNativeProfile is schema 1's fixed VLESS/XHTTP client material. It
// contains no arbitrary Xray configuration, Origin address, or local listener.
type WhiteListNativeProfile struct {
	RouteID               string `json:"route_id"`
	Label                 string `json:"label"`
	TransportProfileID    string `json:"transport_profile_id"`
	TransportReleaseID    string `json:"transport_release_id"`
	CompatibilityPresetID string `json:"compatibility_preset_id"`
	Address               string `json:"address"`
	Port                  int    `json:"port"`
	ServerName            string `json:"server_name"`
	Host                  string `json:"host"`
	Path                  string `json:"path"`
	ClientID              string `json:"client_id"`
	Encryption            string `json:"encryption"`
}

// NativeWhiteListProfiles validates the entire already-authorized publication.
// It is a renderer, not entitlement, admission, receipt, or freshness authority.
func NativeWhiteListProfiles(nodes []WhiteListNode) ([]WhiteListNativeProfile, error) {
	if len(nodes) == 0 || len(nodes) > 16 {
		return nil, errInvalidWhiteListNode
	}
	profiles := make([]WhiteListNativeProfile, 0, len(nodes))
	labels, clients, routes := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for _, node := range nodes {
		if _, err := validatedWhiteListNodeExtra(node); err != nil {
			return nil, err
		}
		if !validWhiteListBatchLabel(node.Label, node) || labels[node.Label] || clients[node.ClientID] ||
			!nativeWhiteListProvenanceID(node.TransportProfileID) || !nativeWhiteListProvenanceID(node.TransportReleaseID) ||
			!nativeWhiteListProvenanceID(node.CompatibilityPresetID) {
			return nil, errInvalidWhiteListNode
		}
		profile := WhiteListNativeProfile{
			TransportProfileID: node.TransportProfileID, TransportReleaseID: node.TransportReleaseID,
			CompatibilityPresetID: node.CompatibilityPresetID, Address: node.Address, Port: node.Port,
			ServerName: node.ServerName, Host: node.Host, Path: node.Path,
			ClientID: node.ClientID, Encryption: node.Encryption,
		}
		// A cosmetic label change does not change transport identity. The hash
		// includes every transport/provenance field but no account/token identity.
		encoded, err := json.Marshal(profile)
		if err != nil {
			return nil, errInvalidWhiteListNode
		}
		digest := sha256.Sum256(encoded)
		profile.RouteID = hex.EncodeToString(digest[:])
		profile.Label = node.Label
		if routes[profile.RouteID] {
			return nil, errInvalidWhiteListNode
		}
		labels[node.Label], clients[node.ClientID], routes[profile.RouteID] = true, true, true
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func nativeWhiteListProvenanceID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i, char := range []byte(value) {
		alphanumeric := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		if !alphanumeric && (i == 0 || (char != '.' && char != '_' && char != ':' && char != '-')) {
			return false
		}
	}
	return true
}
