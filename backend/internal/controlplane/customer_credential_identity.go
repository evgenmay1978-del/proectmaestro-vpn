package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
)

// The explicit outer marker selects a different authenticated scope. Removing
// or changing it cannot reinterpret a typed identity as a scalar password.
type naiveCredentialEnvelope struct {
	Envelope
	IdentityVersion int `json:"credential_identity_version"`
}

type naiveCredentialIdentity struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func validCredentialIdentityPart(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.ContainsRune(value, 0)
}

// SealNaiveCredentialIdentity retains the actual legacy proxy username without
// adding a transport protocol or changing existing scalar credential envelopes.
func SealNaiveCredentialIdentity(box *SecretBox, customerID, username, password string) ([]byte, string, error) {
	if box == nil || !validCredentialIdentityPart(username) || !validCredentialIdentityPart(password) {
		return nil, "", ErrInvalidState
	}
	plain, err := json.Marshal(naiveCredentialIdentity{Username: username, Password: password})
	if err != nil {
		return nil, "", ErrInvalidState
	}
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
	}()
	envelope, err := box.Seal(SecretScope{OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: "naive-identity-v1"}, plain)
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

func (s *Service) openCustomerCredential(row map[string]any, customerID, protocol string) (string, string, error) {
	encoded, ok := rowString(row, "secret_envelope")
	if !ok || len(encoded) > 128<<10 {
		return "", "", ErrUnavailable
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", ErrUnavailable
	}
	var marker map[string]json.RawMessage
	if json.Unmarshal(raw, &marker) != nil || marker == nil {
		return "", "", ErrUnavailable
	}
	if _, typed := marker["credential_identity_version"]; !typed {
		if protocol == "awg" {
			return "", "", ErrUnavailable
		}
		password, err := s.openCustomerSecret(row, "secret_envelope", customerID, "credential", protocol)
		return password, "", err
	}
	var envelope naiveCredentialEnvelope
	if (protocol != "naive" && protocol != "awg") || decodeCredentialIdentityJSON(raw, &envelope) != nil || envelope.IdentityVersion != 1 {
		return "", "", ErrUnavailable
	}
	plain, err := s.store.secrets.Open(SecretScope{OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: protocol + "-identity-v1"}, envelope.Envelope)
	if err != nil {
		return "", "", ErrUnavailable
	}
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
	}()
	if protocol == "awg" {
		if _, err := DecodeWGCredentialIdentity(string(plain)); err != nil {
			return "", "", ErrUnavailable
		}
		return string(plain), "", nil
	}
	var identity naiveCredentialIdentity
	if decodeCredentialIdentityJSON(plain, &identity) != nil ||
		!validCredentialIdentityPart(identity.Username) || !validCredentialIdentityPart(identity.Password) {
		return "", "", ErrUnavailable
	}
	return identity.Password, identity.Username, nil
}

func decodeCredentialIdentityJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidState
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalidState
	}
	return nil
}
