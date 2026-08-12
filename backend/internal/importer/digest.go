package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func Digest(plan ImportPlan) string {
	copy := plan
	copy.PlanDigest = ""
	copy.Blockers = nil
	encoded, err := json.Marshal(copy)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func digestSnapshot(snapshot Snapshot) string {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func canonicalLegacyDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func plannedCustomerSourceDigest(customer PlannedCustomer) string {
	return canonicalLegacyDigest(LegacyCustomer{
		SourceKey:                 customer.SourceKey,
		Login:                     customer.DisplayLogin,
		LoginKeyHMAC:              customer.LoginKeyHMAC,
		UUIDHMAC:                  customer.UUIDHMAC,
		SubIDHMAC:                 customer.SubIDHMAC,
		TokenHMAC:                 customer.TokenHMAC,
		CredentialFingerprintHMAC: customer.CredentialFingerprintHMAC,
		IdentitySecretRef:         customer.IdentitySecretRef,
		ProtocolTags:              append([]string(nil), customer.ProtocolTags...),
		NodeIDs:                   append([]string(nil), customer.NodeIDs...),
		ExpiresAtUnix:             customer.ExpiresAtUnix,
		Generation:                customer.Generation,
		Status:                    customer.Status,
	})
}

func digestBatch(operations []ApplyOperation) string {
	encoded, err := json.Marshal(operations)
	if err != nil {
		return ""
	}
	return sha256Hex(encoded)
}

func deterministicID(namespace, entity, sourceKey string) string {
	return sha256Hex([]byte(namespace + "\x00" + entity + "\x00" + sourceKey))
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}

func validCanonicalSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
