package importer

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

var errInvalidSnapshotProtection = errors.New("invalid snapshot protection")

type SnapshotProtection struct {
	SourceDigest          string
	ClusterHMACKeySHA256  string
	LegacyTrialSaltSHA256 string
	HasTrials             bool
	EncryptedSecrets      []LegacyEncryptedSecret
	Customers             []LegacyCustomer
}

func ProtectionFromSnapshot(snapshot Snapshot) SnapshotProtection {
	customers := append([]LegacyCustomer(nil), snapshot.Customers...)
	for index := range customers {
		customers[index].ProtocolTags = append([]string(nil), customers[index].ProtocolTags...)
		customers[index].NodeIDs = append([]string(nil), customers[index].NodeIDs...)
	}
	return SnapshotProtection{
		SourceDigest:          digestSnapshot(snapshot),
		ClusterHMACKeySHA256:  snapshot.ClusterHMACKeySHA256,
		LegacyTrialSaltSHA256: snapshot.LegacyTrialSaltSHA256,
		HasTrials:             len(snapshot.Trials) > 0,
		EncryptedSecrets:      append([]LegacyEncryptedSecret(nil), snapshot.EncryptedSecrets...),
		Customers:             customers,
	}
}

func ValidateSnapshotProtection(
	protection SnapshotProtection,
	box *controlplane.SecretBox,
	rawHMACKey []byte,
	rawTrialSalt []byte,
) (*TrialImportProtection, error) {
	if box == nil ||
		!validCanonicalSHA256(protection.ClusterHMACKeySHA256) ||
		sha256Hex(rawHMACKey) != protection.ClusterHMACKeySHA256 {
		return nil, errInvalidSnapshotProtection
	}
	for _, secret := range protection.EncryptedSecrets {
		nonce, ok := decodeCanonicalBase64(secret.NonceB64)
		if !ok {
			return nil, errInvalidSnapshotProtection
		}
		ciphertext, ok := decodeCanonicalBase64(secret.CiphertextB64)
		if !ok {
			return nil, errInvalidSnapshotProtection
		}
		plaintext, err := box.Open(controlplane.SecretScope{
			OwnerType: secret.OwnerType,
			OwnerID:   secret.OwnerSourceKey,
			Field:     secret.Field,
			Kind:      secret.Kind,
		}, controlplane.Envelope{
			KeyVersion: secret.KeyVersion,
			Nonce:      nonce,
			Ciphertext: ciphertext,
		})
		if err != nil {
			return nil, errInvalidSnapshotProtection
		}
		plaintextSHA256 := sha256Hex(plaintext)
		zeroBytes(plaintext)
		if !validCanonicalSHA256(secret.SHA256) || plaintextSHA256 != secret.SHA256 {
			return nil, errInvalidSnapshotProtection
		}
	}
	if !protection.HasTrials {
		if protection.LegacyTrialSaltSHA256 != "" || len(rawTrialSalt) != 0 {
			return nil, errInvalidSnapshotProtection
		}
		return nil, nil
	}
	if !validCanonicalSHA256(protection.LegacyTrialSaltSHA256) ||
		sha256Hex(rawTrialSalt) != protection.LegacyTrialSaltSHA256 {
		return nil, errInvalidSnapshotProtection
	}
	envelope, err := box.Seal(controlplane.SecretScope{
		OwnerType: "trial_lookup",
		OwnerID:   "legacy",
		Field:     "salt",
		Kind:      "hmac-key",
	}, rawTrialSalt)
	if err != nil {
		return nil, errInvalidSnapshotProtection
	}
	encoded, err := json.Marshal(struct {
		KeyVersion    int    `json:"key_version"`
		NonceB64      string `json:"nonce_b64"`
		CiphertextB64 string `json:"ciphertext_b64"`
	}{
		KeyVersion:    envelope.KeyVersion,
		NonceB64:      base64.StdEncoding.EncodeToString(envelope.Nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext),
	})
	if err != nil {
		return nil, errInvalidSnapshotProtection
	}
	return &TrialImportProtection{
		KeyVersion:            envelope.KeyVersion,
		EncryptedSaltEnvelope: string(encoded),
		SaltSHA256:            protection.LegacyTrialSaltSHA256,
	}, nil
}

func decodeCanonicalBase64(value string) ([]byte, bool) {
	if value == "" {
		return nil, false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, false
	}
	return decoded, true
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
