package controlplane

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/netip"
)

// WGCredentialIdentity is the complete per-account WireGuard credential value.
// Subscription generation aliases this type so persistence has no dependency
// on the renderer, which already consumes control-plane entitlements.
type WGCredentialIdentity struct {
	Server        string
	Port          int
	PeerPublicKey string
	PrivateKey    string
	LocalAddress  string
}

// EncodeWGCredentialIdentity retains the complete per-customer tuple. It never
// derives keys, addresses, or peer settings from a shared topology.
func EncodeWGCredentialIdentity(identity *WGCredentialIdentity) (string, error) {
	if identity == nil || !validCredentialIdentityPart(identity.Server) || identity.Port < 1 || identity.Port > 65535 {
		return "", ErrInvalidState
	}
	if _, err := netip.ParsePrefix(identity.LocalAddress); err != nil {
		return "", ErrInvalidState
	}
	for _, key := range []string{identity.PeerPublicKey, identity.PrivateKey} {
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != key {
			return "", ErrInvalidState
		}
	}
	raw, err := json.Marshal(identity)
	if err != nil || len(raw) > 4096 {
		return "", ErrInvalidState
	}
	return string(raw), nil
}

func DecodeWGCredentialIdentity(raw string) (*WGCredentialIdentity, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, ErrInvalidState
	}
	var identity WGCredentialIdentity
	if decodeCredentialIdentityJSON([]byte(raw), &identity) != nil {
		return nil, ErrInvalidState
	}
	canonical, err := EncodeWGCredentialIdentity(&identity)
	if err != nil || canonical != raw {
		return nil, ErrInvalidState
	}
	return &identity, nil
}

func SealWGCredentialIdentity(box *SecretBox, customerID, raw string) ([]byte, string, error) {
	if box == nil {
		return nil, "", ErrInvalidState
	}
	if _, err := DecodeWGCredentialIdentity(raw); err != nil {
		return nil, "", ErrInvalidState
	}
	plain := []byte(raw)
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
	}()
	envelope, err := box.Seal(SecretScope{OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: "awg-identity-v1"}, plain)
	if err != nil {
		return nil, "", ErrInvalidState
	}
	digest := sha256.Sum256(plain)
	encoded, err := json.Marshal(naiveCredentialEnvelope{Envelope: envelope, IdentityVersion: 1})
	if err != nil {
		return nil, "", ErrInvalidState
	}
	return encoded, hex.EncodeToString(digest[:]), nil
}
