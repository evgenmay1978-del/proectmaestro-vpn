package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const legacyOrdersConvertedSource = "legacy:orders:present-converted-v1"
const legacyOrderAliasPrefix = "legacy-order-alias-v1:"

type LegacyOrderSource struct{ RawJSON []byte }

type nativeOrderAlias struct {
	Key, CustomerID, SecretID, SHA, SourceSHA, PriorSecretID string
	Revision, PriorRevision                                  int64
	Historical                                               bool
}
type nativeLegacyOrderProof struct {
	secrets map[string]LegacyEncryptedSecret
	aliases map[string]nativeOrderAlias
}

func reservedLegacyOrderSecret(secret LegacyEncryptedSecret) bool {
	return secret.Kind == controlplane.LegacyOrderRecordKind || secret.Kind == controlplane.LegacyOrderSourceKind || secret.OwnerType == "legacy_order" || secret.OwnerType == "legacy_order_source" || strings.HasPrefix(secret.SecretID, controlplane.LegacyOrderRecordKind+":") || strings.HasPrefix(secret.SecretID, controlplane.LegacyOrderSourceKind+":")
}
func hasConvertedOrderSource(hashes map[string]string) bool {
	_, ok := hashes[legacyOrdersConvertedSource]
	return ok
}

func openLegacyOrderSecret(box *controlplane.SecretBox, secret LegacyEncryptedSecret) ([]byte, error) {
	var scope controlplane.SecretScope
	switch secret.Kind {
	case controlplane.LegacyOrderSourceKind:
		scope = controlplane.LegacyOrderSourceScope(secret.SHA256)
		if secret.SecretID != controlplane.LegacyOrderSourceID(secret.SHA256) {
			return nil, errInvalidSnapshotProtection
		}
	case controlplane.LegacyOrderRecordKind:
		var err error
		scope, err = controlplane.LegacyOrderStoredRecordScope(secret.OwnerSourceKey, secret.SHA256, secret.Field)
		if err != nil {
			return nil, errInvalidSnapshotProtection
		}
		if !validCanonicalSHA256(secret.OwnerSourceKey) || secret.SecretID != controlplane.LegacyOrderRecordID(secret.OwnerSourceKey, secret.SHA256) {
			return nil, errInvalidSnapshotProtection
		}
	default:
		return nil, errInvalidSnapshotProtection
	}
	if box == nil || secret.OwnerType != scope.OwnerType || secret.OwnerSourceKey != scope.OwnerID || secret.Field != scope.Field || !validCanonicalSHA256(secret.SHA256) {
		return nil, errInvalidSnapshotProtection
	}
	nonce, nOK := decodeCanonicalBase64(secret.NonceB64)
	ciphertext, cOK := decodeCanonicalBase64(secret.CiphertextB64)
	if !nOK || !cOK {
		return nil, errInvalidSnapshotProtection
	}
	plain, err := box.Open(scope, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil || sha256Hex(plain) != secret.SHA256 {
		zeroBytes(plain)
		return nil, errInvalidSnapshotProtection
	}
	return plain, nil
}

func sealLegacyOrderSecret(box *controlplane.SecretBox, scope controlplane.SecretScope, id string, plain []byte) (LegacyEncryptedSecret, error) {
	envelope, err := box.Seal(scope, plain)
	if err != nil {
		return LegacyEncryptedSecret{}, errInvalidSnapshotProtection
	}
	return LegacyEncryptedSecret{SecretID: id, OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha256Hex(plain)}, nil
}

func legacyOrderRecordFor(row controlplane.LegacyOrderSourceRow, customers map[string]LegacyCustomer, sourceSHA, customerSHA string, box *controlplane.SecretBox) (controlplane.LegacyOrderRecord, error) {
	record := controlplane.LegacyOrderRecord{SchemaVersion: 1, OrderKeyHMAC: box.LookupHMAC(controlplane.LegacyOrderLookupDomain, []byte(row.Order.ID)), OrdersSHA256: sourceSHA, CustomersSHA256: customerSHA, SourceRow: append([]byte(nil), row.RawJSON...), Revision: 1}
	if customer, exists := customers[row.Order.Login]; exists {
		record.CustomerSourceKey, record.CustomerID = customer.SourceKey, deterministicID("maestro-legacy-v1", "customer", customer.SourceKey)
		record.CustomerLoginHMAC, record.CustomerTokenHMAC = customer.LoginKeyHMAC, customer.TokenHMAC
	}
	if _, err := controlplane.ValidateLegacyOrderRecord(box, record); err != nil {
		zeroBytes(record.SourceRow)
		return controlplane.LegacyOrderRecord{}, errInvalidSnapshotProtection
	}
	return record, nil
}

func validateNativeLegacyOrderProof(protection SnapshotProtection, box *controlplane.SecretBox) (*nativeLegacyOrderProof, error) {
	proof := &nativeLegacyOrderProof{secrets: map[string]LegacyEncryptedSecret{}, aliases: map[string]nativeOrderAlias{}}
	for _, secret := range protection.EncryptedSecrets {
		if reservedLegacyOrderSecret(secret) {
			if _, exists := proof.secrets[secret.SecretID]; exists {
				return nil, errInvalidSnapshotProtection
			}
			proof.secrets[secret.SecretID] = secret
		}
	}
	sourceSHA, converted := protection.SourceHashes[legacyOrdersConvertedSource]
	if !converted {
		if len(proof.secrets) != 0 {
			return nil, errInvalidSnapshotProtection
		}
		for key := range protection.SourceHashes {
			if strings.HasPrefix(key, legacyOrderAliasPrefix) {
				return nil, errInvalidSnapshotProtection
			}
		}
		return nil, nil
	}
	if !validCanonicalSHA256(sourceSHA) || len(protection.Orders) != 0 || protection.SourceHashes["legacy:orders:absent"] != "" || protection.SourceHashes["legacy:orders:present-unconverted"] != "" {
		return nil, errInvalidSnapshotProtection
	}
	parent := &nativeLegacyOrderProof{secrets: map[string]LegacyEncryptedSecret{}, aliases: map[string]nativeOrderAlias{}}
	if protection.Parent != nil {
		var err error
		parent, err = validateNativeLegacyOrderProof(*protection.Parent, box)
		if err != nil || parent == nil || protection.ParentSourceDigest != protection.Parent.SourceDigest {
			return nil, errInvalidSnapshotProtection
		}
	}
	for id, secret := range parent.secrets {
		if proof.secrets[id] != secret {
			return nil, errInvalidSnapshotProtection
		}
	}
	sourceRows := map[string][]controlplane.LegacyOrderSourceRow{}
	records := map[string]controlplane.LegacyOrderRecord{}
	defer func() {
		for _, record := range records {
			zeroBytes(record.SourceRow)
		}
	}()
	for id, secret := range proof.secrets {
		plain, err := openLegacyOrderSecret(box, secret)
		if err != nil {
			return nil, errInvalidSnapshotProtection
		}
		if secret.Kind == controlplane.LegacyOrderSourceKind {
			rows, err := controlplane.DecodeLegacyOrderSource(plain)
			zeroBytes(plain)
			if err != nil {
				return nil, errInvalidSnapshotProtection
			}
			sourceRows[secret.SHA256] = rows
		} else {
			var record controlplane.LegacyOrderRecord
			err = decodeCanonicalOperation(plain, &record)
			zeroBytes(plain)
			if err != nil {
				return nil, errInvalidSnapshotProtection
			}
			if _, err := controlplane.ValidateLegacyOrderRecord(box, record); err != nil {
				zeroBytes(record.SourceRow)
				return nil, errInvalidSnapshotProtection
			}
			records[id] = record
		}
	}
	current, ok := sourceRows[sourceSHA]
	if !ok {
		return nil, errInvalidSnapshotProtection
	}
	customers := map[string]LegacyCustomer{}
	for _, customer := range protection.Customers {
		if _, exists := customers[customer.Login]; exists {
			return nil, errInvalidSnapshotProtection
		}
		customers[customer.Login] = customer
	}
	for _, row := range current {
		candidate, err := legacyOrderRecordFor(row, customers, sourceSHA, protection.SourceHashes["customers"], box)
		if err != nil {
			return nil, errInvalidSnapshotProtection
		}
		sha := protection.SourceHashes[legacyOrderAliasPrefix+candidate.OrderKeyHMAC]
		id := controlplane.LegacyOrderRecordID(candidate.OrderKeyHMAC, sha)
		record, ok := records[id]
		if !ok {
			zeroBytes(candidate.SourceRow)
			return nil, errInvalidSnapshotProtection
		}
		prior, retained := parent.aliases[candidate.OrderKeyHMAC]
		if retained && prior.SecretID == id {
			candidate.OrdersSHA256, candidate.CustomersSHA256, candidate.Revision = record.OrdersSHA256, record.CustomersSHA256, record.Revision
		} else if retained {
			if prior.Revision == math.MaxInt64 {
				zeroBytes(candidate.SourceRow)
				return nil, errInvalidSnapshotProtection
			}
			candidate.Revision = prior.Revision + 1
			oldRecord, exists := records[prior.SecretID]
			oldRow, oldErr := controlplane.ValidateLegacyOrderRecord(box, oldRecord)
			if !exists || oldErr != nil || oldRecord.CustomerID != record.CustomerID || !controlplane.LegacyOrderTransitionAllowed(oldRow, row) {
				zeroBytes(candidate.SourceRow)
				return nil, errInvalidSnapshotProtection
			}
		}
		if canonicalLegacyDigest(candidate) != canonicalLegacyDigest(record) {
			zeroBytes(candidate.SourceRow)
			return nil, errInvalidSnapshotProtection
		}
		zeroBytes(candidate.SourceRow)
		found := false
		for _, original := range sourceRows[record.OrdersSHA256] {
			if bytes.Equal(original.RawJSON, record.SourceRow) {
				found = true
				break
			}
		}
		if !found {
			return nil, errInvalidSnapshotProtection
		}
		proof.aliases[record.OrderKeyHMAC] = nativeOrderAlias{Key: record.OrderKeyHMAC, CustomerID: record.CustomerID, SecretID: id, SHA: sha, SourceSHA: record.OrdersSHA256, Revision: record.Revision, Historical: row.Order.Status == "paid" || row.Order.Credited, PriorSecretID: prior.SecretID, PriorRevision: prior.Revision}
	}
	for key := range parent.aliases {
		if _, ok := proof.aliases[key]; !ok {
			return nil, errInvalidSnapshotProtection
		}
	}
	aliases := 0
	for key, sha := range protection.SourceHashes {
		if strings.HasPrefix(key, legacyOrderAliasPrefix) {
			aliases++
			if !validCanonicalSHA256(sha) || proof.aliases[strings.TrimPrefix(key, legacyOrderAliasPrefix)].SHA != sha {
				return nil, errInvalidSnapshotProtection
			}
		}
	}
	if aliases != len(current) {
		return nil, errInvalidSnapshotProtection
	}
	for id, secret := range proof.secrets {
		if _, old := parent.secrets[id]; old {
			continue
		}
		if secret.Kind == controlplane.LegacyOrderSourceKind {
			if secret.SHA256 != sourceSHA {
				return nil, errInvalidSnapshotProtection
			}
		} else if proof.aliases[secret.OwnerSourceKey].SecretID != id {
			return nil, errInvalidSnapshotProtection
		}
	}
	return proof, nil
}

func normalizeLegacyOrderSource(snapshot *Snapshot, input *LegacyOrderSource, box *controlplane.SecretBox, parentSnapshot *Snapshot) error {
	if input == nil {
		if parentSnapshot != nil && hasConvertedOrderSource(parentSnapshot.SourceHashes) {
			return ErrLegacyNormalize
		}
		return nil
	}
	sourceSHA := sha256Hex(input.RawJSON)
	if snapshot.SourceHashes["legacy:orders:present-unconverted"] != sourceSHA {
		return ErrLegacyNormalize
	}
	rows, err := controlplane.DecodeLegacyOrderSource(input.RawJSON)
	if err != nil {
		return ErrLegacyNormalize
	}
	parent := &nativeLegacyOrderProof{secrets: map[string]LegacyEncryptedSecret{}, aliases: map[string]nativeOrderAlias{}}
	if parentSnapshot != nil {
		parent, err = validateNativeLegacyOrderProof(ProtectionFromSnapshot(*parentSnapshot), box)
		if err != nil || parent == nil {
			return ErrLegacyNormalize
		}
	}
	secrets := map[string]LegacyEncryptedSecret{}
	for id, secret := range parent.secrets {
		secrets[id] = secret
	}
	sourceID := controlplane.LegacyOrderSourceID(sourceSHA)
	if _, exists := secrets[sourceID]; !exists {
		secret, err := sealLegacyOrderSecret(box, controlplane.LegacyOrderSourceScope(sourceSHA), sourceID, input.RawJSON)
		if err != nil {
			return ErrLegacyNormalize
		}
		secrets[sourceID] = secret
	}
	customers := map[string]LegacyCustomer{}
	for _, row := range snapshot.Customers {
		customers[row.Login] = row
	}
	seen := map[string]bool{}
	for _, row := range rows {
		record, err := legacyOrderRecordFor(row, customers, sourceSHA, snapshot.SourceHashes["customers"], box)
		if err != nil {
			return ErrLegacyNormalize
		}
		prior, exists := parent.aliases[record.OrderKeyHMAC]
		var secret LegacyEncryptedSecret
		if exists {
			plain, err := openLegacyOrderSecret(box, secrets[prior.SecretID])
			if err != nil {
				zeroBytes(record.SourceRow)
				return ErrLegacyNormalize
			}
			var priorRecord controlplane.LegacyOrderRecord
			err = decodeCanonicalOperation(plain, &priorRecord)
			zeroBytes(plain)
			if err != nil {
				zeroBytes(record.SourceRow)
				return ErrLegacyNormalize
			}
			comparable := record
			comparable.OrdersSHA256, comparable.CustomersSHA256, comparable.Revision = priorRecord.OrdersSHA256, priorRecord.CustomersSHA256, priorRecord.Revision
			if canonicalLegacyDigest(comparable) == canonicalLegacyDigest(priorRecord) {
				secret = secrets[prior.SecretID]
			} else {
				priorRow, priorErr := controlplane.ValidateLegacyOrderRecord(box, priorRecord)
				if prior.Revision == math.MaxInt64 || priorErr != nil || priorRecord.CustomerID != record.CustomerID || !controlplane.LegacyOrderTransitionAllowed(priorRow, row) {
					zeroBytes(priorRecord.SourceRow)
					zeroBytes(record.SourceRow)
					return ErrLegacyNormalize
				}
				record.Revision = prior.Revision + 1
			}
			zeroBytes(priorRecord.SourceRow)
		}
		if secret.SecretID == "" {
			plain, err := json.Marshal(record)
			if err != nil {
				zeroBytes(record.SourceRow)
				return ErrLegacyNormalize
			}
			recordSHA := sha256Hex(plain)
			secret, err = sealLegacyOrderSecret(box, controlplane.LegacyOrderRecordScope(record.OrderKeyHMAC, recordSHA), controlplane.LegacyOrderRecordID(record.OrderKeyHMAC, recordSHA), plain)
			zeroBytes(plain)
			if err != nil {
				zeroBytes(record.SourceRow)
				return ErrLegacyNormalize
			}
			secrets[secret.SecretID] = secret
		}
		snapshot.SourceHashes[legacyOrderAliasPrefix+record.OrderKeyHMAC] = secret.SHA256
		seen[record.OrderKeyHMAC] = true
		zeroBytes(record.SourceRow)
	}
	for key := range parent.aliases {
		if !seen[key] {
			return ErrLegacyNormalize
		}
	}
	delete(snapshot.SourceHashes, "legacy:orders:present-unconverted")
	snapshot.SourceHashes[legacyOrdersConvertedSource] = sourceSHA
	ids := make([]string, 0, len(secrets))
	for id := range secrets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, secrets[id])
	}
	return nil
}

func (s *RQLiteApplyStore) nativeLegacyOrderStatements(batch ApplyBatch, operation ApplyOperation, proof *nativeLegacyOrderProof) ([]rqlite.Statement, error) {
	if proof == nil {
		return nil, errInvalidSnapshotProtection
	}
	var secret LegacyEncryptedSecret
	if decodeCanonicalOperation(operation.CanonicalJSON, &secret) != nil || operation.Key != secret.SecretID || proof.secrets[secret.SecretID] != secret {
		return nil, errInvalidSnapshotProtection
	}
	alias, ok := proof.aliases[secret.OwnerSourceKey]
	if secret.Kind != controlplane.LegacyOrderRecordKind || !ok || alias.SecretID != secret.SecretID {
		return nil, nil
	}
	historical := 0
	if alias.Historical {
		historical = 1
	}
	var customer any
	if alias.CustomerID != "" {
		customer = alias.CustomerID
	}
	// Same-batch replay keeps the same source. A final delta uses the actual
	// authenticated parent, never incoming-1. A newer owner acceptance wins.
	validPrior := `imported_legacy_order_aliases.record_secret_id=excluded.record_secret_id`
	priorArgs := []any{}
	if alias.PriorSecretID != "" {
		validPrior += ` OR (imported_legacy_order_aliases.record_secret_id=? AND imported_legacy_order_aliases.source_revision=?)`
		priorArgs = []any{alias.PriorSecretID, alias.PriorRevision}
	}
	preserve := `(imported_legacy_order_aliases.accepted_order_id IS NOT NULL OR imported_legacy_order_aliases.cancelled_at_unix IS NOT NULL)`
	args := append([]any{alias.Key, alias.SecretID, alias.SHA, alias.SourceSHA, alias.Revision, customer, historical, s.now().Unix()}, batchGateArgs(batch)...)
	args = append(args, priorArgs...)
	return []rqlite.Statement{{SQL: `INSERT INTO imported_legacy_order_aliases(order_key_hmac,record_secret_id,record_sha256,source_sha256,source_revision,customer_id,historical_grant,imported_at_unix)
 SELECT ?,?,?,?,?,?,?,? WHERE ` + batchWriteGate + `
 ON CONFLICT(order_key_hmac) DO UPDATE SET
 source_revision=CASE WHEN ` + preserve + ` THEN imported_legacy_order_aliases.source_revision WHEN (` + validPrior + `) THEN excluded.source_revision ELSE -1 END,
 record_secret_id=CASE WHEN ` + preserve + ` THEN imported_legacy_order_aliases.record_secret_id ELSE excluded.record_secret_id END,
 record_sha256=CASE WHEN ` + preserve + ` THEN imported_legacy_order_aliases.record_sha256 ELSE excluded.record_sha256 END,
 source_sha256=CASE WHEN ` + preserve + ` THEN imported_legacy_order_aliases.source_sha256 ELSE excluded.source_sha256 END,
 historical_grant=CASE WHEN ` + preserve + ` THEN imported_legacy_order_aliases.historical_grant ELSE excluded.historical_grant END`, Args: args}}, nil
}
