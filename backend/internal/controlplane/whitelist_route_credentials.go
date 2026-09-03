package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// WhiteListRouteCredential is the immutable, exit-bound credential used by one
// commercial white-list entitlement. Payload is always protected at rest.
type WhiteListRouteCredential struct {
	EntitlementID string
	ExitID        string
	ManagedEmail  string
	Payload       Envelope
}

// WhiteListRouteCredentialScope binds a protected credential to exactly one
// entitlement/exit pair so ciphertext cannot be transplanted between routes.
func WhiteListRouteCredentialScope(entitlementID, exitID string) SecretScope {
	return SecretScope{
		OwnerType: "whitelist-route-credential",
		OwnerID:   entitlementID + "\x00" + exitID,
		Field:     "credential",
		Kind:      "xray-route",
	}
}

// NewWhiteListRouteCredential protects a route credential before it can enter
// a persistence payload.
func NewWhiteListRouteCredential(
	box *SecretBox, entitlementID, exitID string, plaintext []byte,
) (WhiteListRouteCredential, error) {
	if box == nil || entitlementID == "" || exitID == "" || len(plaintext) == 0 {
		return WhiteListRouteCredential{}, errors.New("controlplane: invalid white-list route credential")
	}
	payload, err := box.Seal(WhiteListRouteCredentialScope(entitlementID, exitID), plaintext)
	if err != nil {
		return WhiteListRouteCredential{}, err
	}
	return WhiteListRouteCredential{
		EntitlementID: entitlementID,
		ExitID:        exitID,
		ManagedEmail:  whiteListManagedEmail(entitlementID, exitID),
		Payload:       payload,
	}, nil
}

// CanonicalIdentity deliberately excludes ciphertext and plaintext. It is safe
// to use as a stable non-secret route identity.
func (credential WhiteListRouteCredential) CanonicalIdentity() string {
	return credential.EntitlementID + "\x00" + credential.ExitID + "\x00" + credential.ManagedEmail
}

// StoreWhiteListRouteCredential inserts an immutable credential or accepts an
// exact replay after a linearizable read-back.
func (s *Service) StoreWhiteListRouteCredential(ctx context.Context, credential WhiteListRouteCredential) error {
	if s == nil || s.store == nil || credential.EntitlementID == "" || credential.ExitID == "" ||
		credential.ManagedEmail != whiteListManagedEmail(credential.EntitlementID, credential.ExitID) {
		return errors.New("controlplane: invalid white-list route credential")
	}
	payload, err := json.Marshal(credential.Payload)
	if err != nil {
		return errors.New("controlplane: encode white-list route credential")
	}
	now := s.clock.Now().Unix()
	insert := rqlite.Statement{SQL: `INSERT OR IGNORE INTO whitelist_route_credentials(
entitlement_id,exit_id,managed_email,credential_envelope,created_at_unix) VALUES(?,?,?,?,?)`, Args: []any{
		credential.EntitlementID, credential.ExitID, credential.ManagedEmail, payload, now,
	}}
	read := whiteListRouteCredentialRead(credential.EntitlementID, credential.ExitID)
	results, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, insert, read)
	if requestErr == nil && whiteListRouteCredentialMatches(results, credential, payload) {
		return nil
	}
	results, err = s.store.db.QueryLinearizable(ctx, read)
	if err != nil {
		return ErrUnavailable
	}
	if whiteListRouteCredentialMatches(results, credential, payload) {
		return nil
	}
	if requestErr == nil {
		return ErrConflict
	}
	return ErrUnavailable
}

func whiteListRouteCredentialRead(entitlementID, exitID string) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT entitlement_id,exit_id,managed_email,credential_envelope
FROM whitelist_route_credentials WHERE entitlement_id=? AND exit_id=?`, Args: []any{entitlementID, exitID}}
}

func whiteListRouteCredentialMatches(
	results []rqlite.Result, credential WhiteListRouteCredential, payload []byte,
) bool {
	row, ok := firstRow(results)
	if !ok {
		return false
	}
	entitlementID, entitlementOK := rowString(row, "entitlement_id")
	exitID, exitOK := rowString(row, "exit_id")
	managedEmail, emailOK := rowString(row, "managed_email")
	storedPayload, payloadOK := whiteListRowBytes(row, "credential_envelope")
	return entitlementOK && exitOK && emailOK && payloadOK &&
		entitlementID == credential.EntitlementID && exitID == credential.ExitID &&
		managedEmail == credential.ManagedEmail && string(storedPayload) == string(payload)
}

func whiteListRowBytes(row map[string]any, key string) ([]byte, bool) {
	value, ok := row[key]
	if !ok || value == nil {
		return nil, false
	}
	switch actual := value.(type) {
	case []byte:
		return append([]byte(nil), actual...), true
	case string:
		decoded, err := base64.StdEncoding.DecodeString(actual)
		if err == nil {
			return decoded, true
		}
		return []byte(actual), true
	default:
		return nil, false
	}
}

func whiteListManagedEmail(entitlementID, exitID string) string {
	return fmt.Sprintf("wl:%s:%s", entitlementID, exitID)
}
