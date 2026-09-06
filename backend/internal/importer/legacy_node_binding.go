package importer

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func validLegacyNodeCapture(binding LegacyNodeCapture, capture LegacyXUICapture, now time.Time, maxAge time.Duration) bool {
	if binding.ObservedAbsent == nil {
		return binding.SubID != "" && len(binding.SubID) <= 4096 && !strings.ContainsRune(binding.SubID, 0)
	}
	evidence := binding.ObservedAbsent
	return binding.SubID == "" && evidence.Valid() && evidence.CustomersSHA256 == capture.CustomersSHA256 &&
		!evidence.ObservedAt.Before(capture.CapturedAt) && !evidence.ObservedAt.After(capture.CompletedAt) &&
		!evidence.ObservedAt.After(now) && now.Sub(evidence.ObservedAt) <= maxAge
}

// ValidateLegacyAbsentCaptureBinding is also used by the HTTPS capture command
// when it is explicitly supplied with protected, independently obtained proof.
func ValidateLegacyAbsentCaptureBinding(binding LegacyNodeCapture, sourceSHA string, now time.Time, maxAge time.Duration) bool {
	return binding.ObservedAbsent != nil && binding.Login != "" && binding.UUID != "" && binding.Server != "" &&
		(binding.NodeID == "S1" || binding.NodeID == "S3" || binding.NodeID == "S4") &&
		validLegacyNodeCapture(binding, LegacyXUICapture{CustomersSHA256: sourceSHA, CompletedAt: now}, now, maxAge)
}

func nodeAbsenceRecord(row LegacyCustomer, identity ProductionCustomerIdentity, node string) controlplane.LegacyXUIAbsentBinding {
	creds := legacyVLESSNodes(identity.Customer)[node]
	return controlplane.LegacyXUIAbsentBinding{SchemaVersion: 1,
		CustomerID: deterministicID("maestro-legacy-v1", "customer", row.SourceKey), CustomerSourceKey: row.SourceKey,
		NodeID: node, Server: creds.server, LoginKeyHMAC: row.LoginKeyHMAC, UUIDHMAC: row.UUIDHMAC,
		Observation: identity.ObservedAbsent[node]}
}

func reservedNodeAbsence(secret LegacyEncryptedSecret) bool {
	return strings.HasPrefix(secret.SecretID, controlplane.LegacyXUIAbsenceKind+":") ||
		secret.OwnerType == controlplane.LegacyXUIAbsenceOwner || secret.Kind == controlplane.LegacyXUIAbsenceKind
}

func openNodeAbsence(box *controlplane.SecretBox, secret LegacyEncryptedSecret) (controlplane.LegacyXUIAbsentBinding, error) {
	var record controlplane.LegacyXUIAbsentBinding
	if box == nil || secret.OwnerType != controlplane.LegacyXUIAbsenceOwner ||
		secret.Field != "observed_absent" || secret.Kind != controlplane.LegacyXUIAbsenceKind {
		return record, errInvalidProductionIdentity
	}
	nonce, nOK := decodeCanonicalBase64(secret.NonceB64)
	cipher, cOK := decodeCanonicalBase64(secret.CiphertextB64)
	if !nOK || !cOK {
		return record, errInvalidProductionIdentity
	}
	plain, err := box.Open(controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
	if err != nil {
		return record, errInvalidProductionIdentity
	}
	defer zeroBytes(plain)
	if sha256Hex(plain) != secret.SHA256 || decodeCanonicalOperation(plain, &record) != nil || !record.Valid() ||
		secret.SecretID != controlplane.LegacyXUIAbsenceID(record.CustomerID, record.NodeID) || secret.OwnerSourceKey != controlplane.LegacyXUIAbsenceOwnerKey(record.CustomerID, record.NodeID) {
		return controlplane.LegacyXUIAbsentBinding{}, errInvalidProductionIdentity
	}
	return record, nil
}

func appendLegacyNodeAbsences(snapshot *Snapshot, row LegacyCustomer, identity ProductionCustomerIdentity, box *controlplane.SecretBox, parent *Snapshot) error {
	nodes := make([]string, 0, len(identity.ObservedAbsent))
	for node := range identity.ObservedAbsent {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		record := nodeAbsenceRecord(row, identity, node)
		if !record.Valid() {
			return errInvalidProductionIdentity
		}
		id := controlplane.LegacyXUIAbsenceID(record.CustomerID, node)
		secret := LegacyEncryptedSecret{}
		if parent != nil {
			for _, prior := range parent.EncryptedSecrets {
				if prior.SecretID == id {
					secret = prior
					break
				}
			}
		}
		if secret.SecretID != "" {
			prior, err := openNodeAbsence(box, secret)
			if err != nil || canonicalLegacyDigest(prior) != canonicalLegacyDigest(record) {
				return errInvalidProductionIdentity
			}
		} else {
			plain, err := json.Marshal(record)
			if err != nil {
				return errInvalidProductionIdentity
			}
			scope := controlplane.SecretScope{OwnerType: controlplane.LegacyXUIAbsenceOwner, OwnerID: controlplane.LegacyXUIAbsenceOwnerKey(record.CustomerID, node), Field: "observed_absent", Kind: controlplane.LegacyXUIAbsenceKind}
			envelope, err := box.Seal(scope, plain)
			digest := sha256Hex(plain)
			zeroBytes(plain)
			if err != nil {
				return errInvalidProductionIdentity
			}
			secret = LegacyEncryptedSecret{SecretID: id, OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion,
				NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: digest}
		}
		snapshot.SourceHashes[id] = secret.SHA256
		snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, secret)
	}
	return nil
}

func validateProductionNodeAbsences(protection SnapshotProtection, validated *ProductionCustomerProtection, secrets map[string]LegacyEncryptedSecret) error {
	validated.nodeAbsences = map[string]LegacyEncryptedSecret{}
	parentSecrets := map[string]LegacyEncryptedSecret{}
	if protection.Parent != nil {
		for _, secret := range protection.Parent.EncryptedSecrets {
			if reservedNodeAbsence(secret) {
				parentSecrets[secret.SecretID] = secret
			}
		}
	}
	for _, row := range protection.Customers {
		identity, err := openProductionIdentity(validated.box, row.SourceKey, secrets[row.IdentitySecretRef])
		if err != nil {
			return errInvalidProductionIdentity
		}
		for node, evidence := range identity.ObservedAbsent {
			expected := nodeAbsenceRecord(row, identity, node)
			id := controlplane.LegacyXUIAbsenceID(expected.CustomerID, node)
			secret, exists := secrets[id]
			if !exists {
				return errInvalidProductionIdentity
			}
			record, err := openNodeAbsence(validated.box, secret)
			if err != nil || canonicalLegacyDigest(record) != canonicalLegacyDigest(expected) || protection.SourceHashes[id] != secret.SHA256 {
				return errInvalidProductionIdentity
			}
			prior, retained := parentSecrets[id]
			if retained {
				if prior != secret {
					return errInvalidProductionIdentity
				}
			} else if evidence.CustomersSHA256 != protection.SourceHashes["customers"] || evidence.ObservedAt.After(protection.CapturedAt.Add(30*time.Minute)) {
				return errInvalidProductionIdentity
			}
			validated.nodeAbsences[id] = secret
		}
	}
	for id, secret := range secrets {
		if reservedNodeAbsence(secret) {
			if expected, ok := validated.nodeAbsences[id]; !ok || expected != secret {
				return errInvalidProductionIdentity
			}
		}
	}
	for id := range parentSecrets {
		if _, ok := validated.nodeAbsences[id]; !ok {
			return errInvalidProductionIdentity
		}
	}
	return nil
}

// This is part of the SAME customer transaction, before any desired rows are
// published. A later standalone evidence operation is an exact immutable replay.
func (s *RQLiteApplyStore) productionNodeAbsenceStatements(batch ApplyBatch, customer PlannedCustomer) ([]rqlite.Statement, error) {
	if s.customerProtection == nil {
		return nil, nil
	}
	ids := make([]string, 0)
	for id, secret := range s.customerProtection.nodeAbsences {
		record, err := openNodeAbsence(s.customerProtection.box, secret)
		if err != nil {
			return nil, err
		}
		if record.CustomerID == customer.InternalID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var statements []rqlite.Statement
	for _, id := range ids {
		secret := s.customerProtection.nodeAbsences[id]
		raw, err := json.Marshal(secret)
		if err != nil {
			return nil, errInvalidProductionIdentity
		}
		next, err := s.encryptedSecretStatements(batch, ApplyOperation{Entity: "encrypted_secret", Key: id, CanonicalJSON: raw})
		if err != nil {
			return nil, err
		}
		statements = append(statements, next...)
	}
	return statements, nil
}
