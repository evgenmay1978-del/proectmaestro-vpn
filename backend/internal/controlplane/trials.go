package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func (s *Service) RedeemTrial(ctx context.Context, command RedeemTrialCommand) (Customer, error) {
	if strings.TrimSpace(command.Anchor) == "" {
		return Customer{}, errors.New("controlplane: invalid trial identity")
	}
	if strings.TrimSpace(command.DRMIdentity) == "" {
		command.DRMIdentity = ""
		parts := strings.Split(command.Anchor, "|")
		if len(parts) >= 2 {
			device := strings.TrimSpace(parts[1])
			if _, err := hex.DecodeString(device); len(device) >= 16 && err == nil {
				command.DRMIdentity = device
			}
		}
	}
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "trial.redeem", login: command.Login, idempotency: command.IdempotencyKey,
		days: command.Days, status: "active", allowCreate: true,
		trialAnchor: command.Anchor, trialDevice: command.DRMIdentity,
	})
}

type trialMutationIdentity struct {
	anchorHMAC, deviceHMAC             string
	legacyAnchorHMAC, legacyDeviceHMAC string
	legacySecretID                     string
}

func (s *Service) trialIdentityForMutation(ctx context.Context, anchor, device string) (*trialMutationIdentity, error) {
	identity := &trialMutationIdentity{anchorHMAC: s.store.secrets.LookupHMAC("trial-anchor", []byte(anchor))}
	if device != "" {
		identity.deviceHMAC = s.store.secrets.LookupHMAC("trial-device", []byte(device))
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT secret_id,key_version,CAST(secret_envelope AS TEXT) AS secret_envelope,secret_sha256
FROM imported_secrets
WHERE owner_type='trial_lookup' AND owner_source_key='legacy' AND field='salt' AND kind='hmac-key'`,
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) > 1 {
		return nil, ErrUnavailable
	}
	if len(results[0].Rows) == 0 {
		return identity, nil
	}
	row := results[0].Rows[0]
	secretID, idOK := rowString(row, "secret_id")
	keyVersion, versionOK := rowInt64(row, "key_version")
	encoded, envelopeOK := rowString(row, "secret_envelope")
	digest, digestOK := rowString(row, "secret_sha256")
	if !idOK || secretID == "" || !versionOK || !envelopeOK || !digestOK {
		return nil, ErrUnavailable
	}
	var imported struct {
		KeyVersion int    `json:"key_version"`
		Nonce      []byte `json:"nonce_b64"`
		Ciphertext []byte `json:"ciphertext_b64"`
	}
	if json.Unmarshal([]byte(encoded), &imported) != nil || int64(imported.KeyVersion) != keyVersion {
		return nil, ErrUnavailable
	}
	salt, err := s.store.secrets.Open(SecretScope{OwnerType: "trial_lookup", OwnerID: "legacy", Field: "salt", Kind: "hmac-key"}, Envelope{
		KeyVersion: imported.KeyVersion, Nonce: imported.Nonce, Ciphertext: imported.Ciphertext,
	})
	if err != nil {
		return nil, ErrUnavailable
	}
	defer clear(salt)
	actualDigest := sha256.Sum256(salt)
	if len(salt) == 0 || hex.EncodeToString(actualDigest[:]) != digest {
		return nil, ErrUnavailable
	}
	identity.legacySecretID = secretID
	identity.legacyAnchorHMAC = legacyTrialHMAC(salt, anchor)
	if device != "" {
		identity.legacyDeviceHMAC = legacyTrialHMAC(salt, "drm:"+device)
	}
	return identity, nil
}

func legacyTrialHMAC(salt []byte, value string) string {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (identity *trialMutationIdentity) eligibility() (string, []any) {
	// Imported lookup secrets are immutable. Reject an unknown lookup secret
	// introduced after the read, so a concurrent import cannot bypass this gate.
	return `NOT EXISTS (SELECT 1 FROM trial_redemptions WHERE trial_code_hmac=? OR device_key_hmac=?)
AND NOT EXISTS (SELECT 1 FROM imported_trial_identities WHERE used=1 AND
(current_hmac IN (?,?) OR legacy_anchor_hmac IN (?,?) OR lookup_secret_id<>?))`,
		[]any{identity.anchorHMAC, identity.deviceHMAC, identity.anchorHMAC, identity.deviceHMAC,
			identity.legacyAnchorHMAC, identity.legacyDeviceHMAC, identity.legacySecretID}
}
