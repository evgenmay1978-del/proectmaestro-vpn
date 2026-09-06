package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type legacyXUIAbsenceSlot struct {
	record                                   LegacyXUIAbsentBinding
	id, ownerKey, encoded, digest, canonical string
	version                                  int64
	present                                  bool
}

type legacyNodeSecret struct {
	SecretID       string `json:"secret_id"`
	OwnerType      string `json:"owner_type"`
	OwnerSourceKey string `json:"owner_source_key"`
	Field          string `json:"field"`
	Kind           string `json:"kind"`
	KeyVersion     int    `json:"key_version"`
	NonceB64       string `json:"nonce_b64"`
	CiphertextB64  string `json:"ciphertext_b64"`
	SHA256         string `json:"sha256"`
}

func legacyNodeDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func decodeLegacyNodeJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) legacyXUIAbsence(ctx context.Context, customerID, nodeID string) (legacyXUIAbsenceSlot, error) {
	slot := legacyXUIAbsenceSlot{id: LegacyXUIAbsenceID(customerID, nodeID), ownerKey: LegacyXUIAbsenceOwnerKey(customerID, nodeID)}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT i.secret_id,i.owner_type,i.owner_source_key,i.field,i.kind,i.key_version,
CAST(i.secret_envelope AS TEXT) AS secret_envelope,i.secret_sha256,
e.source_key,e.target_id,e.canonical_sha256,e.lifecycle
FROM (SELECT ? AS expected_id) expected
LEFT JOIN imported_secrets i ON i.secret_id=expected.expected_id OR (i.owner_type=? AND i.owner_source_key=?)
LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=expected.expected_id LIMIT 2`, Args: []any{slot.id, LegacyXUIAbsenceOwner, slot.ownerKey}})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return slot, ErrUnavailable
	}
	row := results[0].Rows[0]
	if row["secret_id"] == nil && row["source_key"] == nil {
		return slot, nil
	}
	for key, want := range map[string]string{"secret_id": slot.id, "owner_type": LegacyXUIAbsenceOwner, "owner_source_key": slot.ownerKey, "field": "observed_absent", "kind": LegacyXUIAbsenceKind, "source_key": slot.id, "target_id": slot.id, "lifecycle": "active"} {
		got, ok := rowString(row, key)
		if !ok || got != want {
			return slot, ErrUnavailable
		}
	}
	var ok bool
	slot.encoded, ok = rowString(row, "secret_envelope")
	if !ok || len(slot.encoded) > 65536 {
		return slot, ErrUnavailable
	}
	slot.digest, ok = rowString(row, "secret_sha256")
	if !ok || !legacyBindingSHA(slot.digest) {
		return slot, ErrUnavailable
	}
	slot.canonical, ok = rowString(row, "canonical_sha256")
	if !ok || legacyNodeDigest([]byte(slot.encoded)) != slot.canonical {
		return slot, ErrUnavailable
	}
	slot.version, ok = rowInt64(row, "key_version")
	if !ok || slot.version <= 0 {
		return slot, ErrUnavailable
	}
	var secret legacyNodeSecret
	if decodeLegacyNodeJSON([]byte(slot.encoded), &secret) != nil || secret.SecretID != slot.id || secret.OwnerType != LegacyXUIAbsenceOwner ||
		secret.OwnerSourceKey != slot.ownerKey || secret.Field != "observed_absent" || secret.Kind != LegacyXUIAbsenceKind || secret.SHA256 != slot.digest || int64(secret.KeyVersion) != slot.version {
		return slot, ErrUnavailable
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(secret.NonceB64)
	if err != nil || base64.StdEncoding.EncodeToString(nonce) != secret.NonceB64 {
		return slot, ErrUnavailable
	}
	cipher, err := base64.StdEncoding.Strict().DecodeString(secret.CiphertextB64)
	if err != nil || base64.StdEncoding.EncodeToString(cipher) != secret.CiphertextB64 {
		return slot, ErrUnavailable
	}
	plain, err := s.store.secrets.Open(SecretScope{OwnerType: LegacyXUIAbsenceOwner, OwnerID: slot.ownerKey, Field: "observed_absent", Kind: LegacyXUIAbsenceKind}, Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
	if err != nil {
		return slot, ErrUnavailable
	}
	defer wipeDesiredPayloadBytes(plain)
	if legacyNodeDigest(plain) != slot.digest || decodeLegacyNodeJSON(plain, &slot.record) != nil || !slot.record.Valid() || slot.record.CustomerID != customerID || slot.record.NodeID != nodeID {
		return slot, ErrUnavailable
	}
	slot.present = true
	return slot, nil
}

// The SQL guard binds the checked immutable slot inside the eventual write. A
// newly installed absence between the read and write cannot authorize XUI work.
func (slot legacyXUIAbsenceSlot) guard() (string, []any) {
	if !slot.present {
		return `NOT EXISTS(SELECT 1 FROM imported_secrets WHERE secret_id=? OR (owner_type=? AND owner_source_key=?)) AND NOT EXISTS(SELECT 1 FROM imported_entity_state WHERE entity_kind='encrypted_secret' AND source_key=?)`, []any{slot.id, LegacyXUIAbsenceOwner, slot.ownerKey, slot.id}
	}
	return `EXISTS(SELECT 1 FROM imported_secrets i JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id WHERE i.secret_id=? AND i.owner_type=? AND i.owner_source_key=? AND i.field='observed_absent' AND i.kind=? AND i.key_version=? AND CAST(i.secret_envelope AS TEXT)=? AND i.secret_sha256=? AND e.target_id=i.secret_id AND e.canonical_sha256=? AND e.lifecycle='active')`, []any{slot.id, LegacyXUIAbsenceOwner, slot.ownerKey, LegacyXUIAbsenceKind, slot.version, slot.encoded, slot.digest, slot.canonical}
}

func (s *Service) appendLegacyXUIAbsence(ctx context.Context, customerID, nodeID string, payload map[string]any) error {
	delete(payload, LegacyXUIAbsenceMarker)
	slot, err := s.legacyXUIAbsence(ctx, customerID, nodeID)
	if err != nil {
		return err
	}
	if slot.present {
		payload[LegacyXUIAbsenceMarker] = slot.record
	}
	return nil
}

func (s *Service) legacyAbsentPayloadAllowed(slot legacyXUIAbsenceSlot, scope DesiredPayloadScope, envelope Envelope, digest string) bool {
	if !slot.present {
		return true
	}
	scope.PayloadKind = LegacyXUIPayloadKind
	if _, err := s.store.secrets.OpenDesiredPayload(scope, envelope, digest); err == nil {
		return false
	}
	for _, kind := range []string{"customer-active", "customer-revoked"} {
		scope.PayloadKind = kind
		document, err := s.store.secrets.OpenDesiredPayload(scope, envelope, digest)
		if err != nil {
			continue
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(document.Body, &body) != nil {
			return false
		}
		var record LegacyXUIAbsentBinding
		if decodeLegacyNodeJSON(body[LegacyXUIAbsenceMarker], &record) != nil {
			return false
		}
		got, _ := json.Marshal(record)
		want, _ := json.Marshal(slot.record)
		return bytes.Equal(got, want)
	}
	// PayloadKind is part of the AEAD AAD, along with this complete tuple.
	// Other protocol kinds keep their existing materializer authentication;
	// a ciphertext rejected above cannot later authenticate as XUI for this
	// same tuple. This branch does not grant it a new authentication claim.
	return true
}

func (s *Service) legacyXUIReceiptGuard(ctx context.Context, receipt ApplyReceipt) (string, []any, error) {
	slot, err := s.legacyXUIAbsence(ctx, receipt.CustomerID, receipt.NodeID)
	if err != nil {
		return "", nil, err
	}
	if slot.present {
		rows, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT CAST(desired_envelope AS TEXT) AS desired_envelope,tombstone FROM desired_node_state WHERE customer_id=? AND node_id=? AND service_name=? AND generation=? AND desired_sha256=? AND operation_id=?`, Args: []any{receipt.CustomerID, receipt.NodeID, receipt.ServiceName, receipt.Generation, receipt.DesiredSHA256, receipt.OperationID}})
		row, ok := firstRow(rows)
		if err != nil || !ok {
			return "", nil, ErrUnavailable
		}
		encoded, eOK := rowString(row, "desired_envelope")
		tombstone, tOK := rowInt64(row, "tombstone")
		var envelope Envelope
		if !eOK || !tOK || (tombstone != 0 && tombstone != 1) || decodeLegacyNodeJSON([]byte(encoded), &envelope) != nil ||
			!s.legacyAbsentPayloadAllowed(slot, DesiredPayloadScope{CustomerID: receipt.CustomerID, NodeID: receipt.NodeID, ServiceID: receipt.ServiceName, OperationID: receipt.OperationID, Generation: receipt.Generation, Tombstone: tombstone == 1}, envelope, receipt.DesiredSHA256) {
			return "", nil, ErrForbidden
		}
	}
	guard, args := slot.guard()
	return guard, args, nil
}

func (s *Service) legacyXUIReconcileGuard(ctx context.Context, command ReconcileNodeCommand) (string, []any, error) {
	// Only bindings with a durable absence slot need decryption. Other customers
	// remain selectable, including when one customer on this node is quarantined.
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT d.customer_id,d.generation,d.operation_id,d.tombstone,d.desired_sha256,CAST(d.desired_envelope AS TEXT) AS desired_envelope
FROM desired_node_state d WHERE d.node_id=? AND d.service_name=? AND d.operation_id IS NOT NULL
AND (?='' OR d.customer_id=?) AND (EXISTS(SELECT 1 FROM imported_secrets i WHERE i.secret_id='legacy-xui-absence-v1:'||d.customer_id||':'||d.node_id OR (i.owner_type=? AND i.owner_source_key=d.customer_id||':'||d.node_id||':xui')) OR EXISTS(SELECT 1 FROM imported_entity_state e WHERE e.entity_kind='encrypted_secret' AND e.source_key='legacy-xui-absence-v1:'||d.customer_id||':'||d.node_id)) LIMIT 4097`, Args: []any{command.NodeID, command.ServiceName, command.CustomerID, command.CustomerID, LegacyXUIAbsenceOwner}})
	if err != nil || len(results) != 1 || len(results[0].Rows) > 4096 {
		return "", nil, ErrUnavailable
	}
	guard := `((NOT EXISTS(SELECT 1 FROM imported_secrets q WHERE q.secret_id='legacy-xui-absence-v1:'||d.customer_id||':'||d.node_id OR (q.owner_type='legacy_node_binding' AND q.owner_source_key=d.customer_id||':'||d.node_id||':xui')) AND NOT EXISTS(SELECT 1 FROM imported_entity_state e WHERE e.entity_kind='encrypted_secret' AND e.source_key='legacy-xui-absence-v1:'||d.customer_id||':'||d.node_id))`
	var args []any
	for _, row := range results[0].Rows {
		customer, cOK := rowString(row, "customer_id")
		operation, oOK := rowString(row, "operation_id")
		digest, hOK := rowString(row, "desired_sha256")
		encoded, eOK := rowString(row, "desired_envelope")
		generation, gOK := rowInt64(row, "generation")
		tombstone, tOK := rowInt64(row, "tombstone")
		if !cOK || !oOK || !hOK || !eOK || !gOK || !tOK || (tombstone != 0 && tombstone != 1) {
			return "", nil, ErrUnavailable
		}
		slot, err := s.legacyXUIAbsence(ctx, customer, command.NodeID)
		if err != nil || !slot.present {
			return "", nil, ErrUnavailable
		}
		var envelope Envelope
		if decodeLegacyNodeJSON([]byte(encoded), &envelope) != nil || !s.legacyAbsentPayloadAllowed(slot, DesiredPayloadScope{CustomerID: customer, NodeID: command.NodeID, ServiceID: command.ServiceName, OperationID: operation, Generation: generation, Tombstone: tombstone == 1}, envelope, digest) {
			continue
		}
		slotGuard, slotArgs := slot.guard()
		guard += ` OR (d.customer_id=? AND d.desired_sha256=? AND ` + slotGuard + `)`
		args = append(args, customer, digest)
		args = append(args, slotArgs...)
	}
	return guard + `)`, args, nil
}
