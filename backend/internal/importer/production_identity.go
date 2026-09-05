package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

var errInvalidProductionIdentity = errors.New("unsupported or inconsistent protected legacy customer identity")

// ProductionCustomerIdentity is the plaintext contract inside a v2 snapshot's
// customer/identity/customer-identity envelope. Customer retains the entire
// original legacy record. SubID/NodeSubIDs come from existing XUI identities.
// Generation is the declared migration revision (initial 1, changed delta +1),
// not a historical accounting generation: neither is in legacy customer JSON.
//
// LookupHMAC domains are customer-login (canonical login), subscription-token,
// customer-uuid, subscription-id, and customer-credentials. The last hashes the
// JSON encoding of the protocol -> raw credential map, with sorted JSON keys.
// Independently provisioned VLESS3/4 UUIDs still block migration. Existing WG
// tuples are retained in a separate typed credential; absent VLESS stays absent.
type ProductionCustomerIdentity struct {
	SchemaVersion int                  `json:"schema_version"`
	Customer      legacystore.Customer `json:"customer"`
	SubID         string               `json:"sub_id"`
	Generation    int64                `json:"generation"`
	NodeSubIDs    map[string]string    `json:"node_sub_ids,omitempty"`
}

// ProductionCustomerProtection retains validated digests, lookup HMACs and
// canonical customer metadata, without decrypted credentials or raw device keys.
// Callers cannot construct an enabled production adapter without validation.
type ProductionCustomerProtection struct {
	box             *controlplane.SecretBox
	sourceDigest    string
	rows            map[string]string
	secrets         map[string]string
	snapshotKind    string
	parentDigest    string
	capturedAt      time.Time
	priorCustomers  map[string]LegacyCustomer
	deviceKeys      map[string]map[string]bool
	priorDeviceKeys map[string]map[string]bool
	identityDigests map[string]string
}

func ValidateProductionCustomerIdentities(protection SnapshotProtection, box *controlplane.SecretBox) (*ProductionCustomerProtection, error) {
	validated, err := validateProductionCustomerRows(protection, box)
	if err != nil {
		return nil, err
	}
	validated.snapshotKind, validated.parentDigest, validated.capturedAt = protection.SnapshotKind, protection.ParentSourceDigest, protection.CapturedAt
	validated.priorCustomers = make(map[string]LegacyCustomer)
	if protection.SnapshotKind == "delta" {
		if protection.Parent == nil || !validCanonicalSHA256(protection.ParentSourceDigest) ||
			protection.Parent.SourceDigest != protection.ParentSourceDigest || protection.Parent.ClusterHMACKeySHA256 != protection.ClusterHMACKeySHA256 {
			return nil, errInvalidProductionIdentity
		}
		parent, err := validateProductionCustomerRows(*protection.Parent, box)
		if err != nil {
			return nil, errInvalidProductionIdentity
		}
		validated.priorDeviceKeys = parent.deviceKeys
		for _, row := range protection.Parent.Customers {
			row.ProtocolTags = append([]string(nil), row.ProtocolTags...)
			row.NodeIDs = append([]string(nil), row.NodeIDs...)
			validated.priorCustomers[row.SourceKey] = row
		}
		for _, row := range protection.Customers {
			if prior, exists := validated.priorCustomers[row.SourceKey]; exists {
				if row.Generation < prior.Generation || (row.Generation == prior.Generation &&
					(validated.rows[row.SourceKey] != parent.rows[row.SourceKey] || validated.identityDigests[row.SourceKey] != parent.identityDigests[row.SourceKey])) {
					return nil, errInvalidProductionIdentity
				}
			}
		}
	} else if protection.ParentSourceDigest != "" || protection.Parent != nil {
		return nil, errInvalidProductionIdentity
	}
	return validated, nil
}

func validateProductionCustomerRows(protection SnapshotProtection, box *controlplane.SecretBox) (*ProductionCustomerProtection, error) {
	if box == nil || !validCanonicalSHA256(protection.SourceDigest) {
		return nil, errInvalidProductionIdentity
	}
	if len(protection.Customers) > 0 && ((protection.SnapshotKind != "full" && protection.SnapshotKind != "delta") || protection.CapturedAt.IsZero() || protection.CapturedAt.Unix() <= 0) {
		return nil, errInvalidProductionIdentity
	}
	validated := &ProductionCustomerProtection{box: box, sourceDigest: protection.SourceDigest,
		rows: make(map[string]string), secrets: make(map[string]string), deviceKeys: make(map[string]map[string]bool), identityDigests: make(map[string]string)}
	secrets := make(map[string]LegacyEncryptedSecret, len(protection.EncryptedSecrets))
	for _, secret := range protection.EncryptedSecrets {
		if _, duplicate := secrets[secret.SecretID]; duplicate {
			return nil, errInvalidProductionIdentity
		}
		secrets[secret.SecretID] = secret
	}
	for _, customer := range protection.Customers {
		secret, found := secrets[customer.IdentitySecretRef]
		if !found || customer.SourceKey == "" || validated.rows[customer.SourceKey] != "" ||
			validated.secrets[secret.SecretID] != "" {
			return nil, errInvalidProductionIdentity
		}
		identity, err := openProductionIdentity(box, customer.SourceKey, secret)
		if err != nil || validateProductionIdentity(box, customer, identity) != nil {
			return nil, errInvalidProductionIdentity
		}
		keys := make(map[string]bool, len(identity.Customer.Devices))
		for raw := range identity.Customer.Devices {
			key := box.LookupHMAC("device-identity", []byte(raw))
			if keys[key] {
				return nil, errInvalidProductionIdentity
			}
			keys[key] = true
		}
		validated.deviceKeys[customer.SourceKey] = keys
		validated.identityDigests[customer.SourceKey] = secret.SHA256
		customer.ProtocolTags = append([]string(nil), customer.ProtocolTags...)
		customer.NodeIDs = append([]string(nil), customer.NodeIDs...)
		sort.Strings(customer.ProtocolTags)
		sort.Strings(customer.NodeIDs)
		validated.rows[customer.SourceKey] = canonicalLegacyDigest(customer)
		validated.secrets[secret.SecretID] = canonicalLegacyDigest(secret)
	}
	return validated, nil
}

func NewProductionRQLiteApplyStore(db rqlite.RQLite, now func() time.Time, customers *ProductionCustomerProtection, trials *TrialImportProtection) (*RQLiteApplyStore, error) {
	if customers == nil || customers.box == nil || !validCanonicalSHA256(customers.sourceDigest) {
		return nil, errInvalidProductionIdentity
	}
	var store *RQLiteApplyStore
	var err error
	if trials == nil {
		store, err = NewRQLiteApplyStore(db, now)
	} else {
		store, err = NewRQLiteApplyStoreWithTrialProtection(db, now, *trials)
	}
	if err != nil {
		return nil, err
	}
	store.customerProtection = customers
	return store, nil
}

func openProductionIdentity(box *controlplane.SecretBox, sourceKey string, secret LegacyEncryptedSecret) (ProductionCustomerIdentity, error) {
	var identity ProductionCustomerIdentity
	if secret.OwnerType != "customer" || secret.OwnerSourceKey != sourceKey ||
		secret.Field != "identity" || secret.Kind != "customer-identity" {
		return identity, errInvalidProductionIdentity
	}
	nonce, nonceOK := decodeCanonicalBase64(secret.NonceB64)
	ciphertext, ciphertextOK := decodeCanonicalBase64(secret.CiphertextB64)
	if !nonceOK || !ciphertextOK || len(ciphertext) > 1<<20 {
		return identity, errInvalidProductionIdentity
	}
	plaintext, err := box.Open(controlplane.SecretScope{OwnerType: secret.OwnerType,
		OwnerID: sourceKey, Field: secret.Field, Kind: secret.Kind},
		controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		return identity, errInvalidProductionIdentity
	}
	defer zeroBytes(plaintext)
	if sha256Hex(plaintext) != secret.SHA256 || decodeCanonicalOperation(plaintext, &identity) != nil || identity.SchemaVersion != 1 {
		return ProductionCustomerIdentity{}, errInvalidProductionIdentity
	}
	return identity, nil
}

func productionCredentials(identity ProductionCustomerIdentity) (map[string]string, error) {
	customer := identity.Customer
	if customer.VLESS == nil && (customer.VLESS3 != nil || customer.VLESS4 != nil) {
		return nil, errInvalidProductionIdentity
	}
	credentials := map[string]string{}
	if customer.VLESS != nil {
		if customer.VLESS.UUID == "" || (customer.VLESS3 != nil && customer.VLESS3.UUID != customer.VLESS.UUID) ||
			(customer.VLESS4 != nil && customer.VLESS4.UUID != customer.VLESS.UUID) {
			return nil, errInvalidProductionIdentity
		}
		credentials["vless"] = customer.VLESS.UUID
	}
	if customer.WG != nil {
		raw, err := controlplane.EncodeWGCredentialIdentity(customer.WG)
		if err != nil {
			return nil, errInvalidProductionIdentity
		}
		credentials["awg"] = raw
	}
	if customer.Hy2 != nil {
		if customer.Hy2.User != customer.Login {
			return nil, errInvalidProductionIdentity
		}
		credentials["hysteria2"] = customer.Hy2.Pass
	}
	if customer.Naive != nil {
		if customer.Naive.Username == "" || len(customer.Naive.Username) > 4096 || strings.ContainsRune(customer.Naive.Username, 0) {
			return nil, errInvalidProductionIdentity
		}
		credentials["naive"] = customer.Naive.Password
	}
	if customer.AnyTLS != nil {
		credentials["anytls"] = customer.AnyTLS.Password
	}
	if len(credentials) == 0 {
		return nil, errInvalidProductionIdentity
	}
	for _, raw := range credentials {
		if raw == "" || len(raw) > 4096 || strings.ContainsRune(raw, 0) {
			return nil, errInvalidProductionIdentity
		}
	}
	return credentials, nil
}

func validateProductionIdentity(box *controlplane.SecretBox, row LegacyCustomer, identity ProductionCustomerIdentity) error {
	customer := identity.Customer
	if identity.NodeSubIDs != nil {
		expected := legacyVLESSNodes(customer)
		if len(identity.NodeSubIDs) != len(expected) || identity.NodeSubIDs["S1"] != identity.SubID {
			return errInvalidProductionIdentity
		}
		for node, subID := range identity.NodeSubIDs {
			if _, exists := expected[node]; !exists || subID == "" || len(subID) > 4096 || strings.ContainsRune(subID, 0) {
				return errInvalidProductionIdentity
			}
			found := false
			for _, rowNode := range row.NodeIDs {
				if rowNode == node {
					found = true
				}
			}
			if !found {
				return errInvalidProductionIdentity
			}
		}
	}
	login, err := controlplane.CanonicalLoginKey(customer.Login)
	credentials, credentialErr := productionCredentials(identity)
	if err != nil || credentialErr != nil || customer.Login != row.Login ||
		customer.SubToken == "" || len(customer.SubToken) > 4096 || strings.ContainsRune(customer.SubToken, 0) ||
		len(identity.SubID) > 4096 || strings.ContainsRune(identity.SubID, 0) ||
		identity.Generation <= 0 || identity.Generation != row.Generation ||
		customer.Expires.Unix() != row.ExpiresAtUnix ||
		(row.Status != "active" && row.Status != "suspended") || customer.Disabled != (row.Status == "suspended") {
		return errInvalidProductionIdentity
	}
	uuidHMAC, subIDHMAC := "", ""
	if customer.VLESS == nil {
		if identity.SubID != "" || len(identity.NodeSubIDs) != 0 {
			return errInvalidProductionIdentity
		}
	} else {
		if identity.SubID == "" {
			return errInvalidProductionIdentity
		}
		uuidHMAC = box.LookupHMAC("customer-uuid", []byte(customer.VLESS.UUID))
		subIDHMAC = box.LookupHMAC("subscription-id", []byte(identity.SubID))
	}
	for rawDevice, lastSeen := range customer.Devices {
		if rawDevice == "" || len(rawDevice) > 4096 || strings.ContainsRune(rawDevice, 0) || lastSeen.Unix() < 0 || lastSeen.Unix() > 253402300799 {
			return errInvalidProductionIdentity
		}
	}
	fingerprint, err := json.Marshal(credentials)
	if err != nil {
		return errInvalidProductionIdentity
	}
	defer zeroBytes(fingerprint)
	if box.LookupHMAC("customer-login", []byte(login)) != row.LoginKeyHMAC ||
		box.LookupHMAC("subscription-token", []byte(customer.SubToken)) != row.TokenHMAC ||
		uuidHMAC != row.UUIDHMAC || subIDHMAC != row.SubIDHMAC ||
		box.LookupHMAC("customer-credentials", fingerprint) != row.CredentialFingerprintHMAC {
		return errInvalidProductionIdentity
	}
	seen := make(map[string]bool)
	for _, protocol := range row.ProtocolTags {
		if seen[protocol] || (credentials[protocol] == "" && protocol != "wdtt" && protocol != "olcrtc") {
			return errInvalidProductionIdentity
		}
		seen[protocol] = true
	}
	for protocol := range credentials {
		if !seen[protocol] {
			return errInvalidProductionIdentity
		}
	}
	return nil
}

type productionSealedValue struct {
	envelope string
	digest   string
}

func (protection *ProductionCustomerProtection) sealValue(customerID, field, kind, raw string) (productionSealedValue, error) {
	envelope, err := protection.box.Seal(controlplane.SecretScope{
		OwnerType: "customer", OwnerID: customerID, Field: field, Kind: kind}, []byte(raw))
	if err != nil {
		return productionSealedValue{}, errInvalidProductionIdentity
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return productionSealedValue{}, errInvalidProductionIdentity
	}
	// rqlite transports []byte SQL arguments as this same base64 JSON text.
	return productionSealedValue{base64.StdEncoding.EncodeToString(encoded), sha256Hex([]byte(raw))}, nil
}

func (s *RQLiteApplyStore) productionCustomerValues(customer PlannedCustomer, secret LegacyEncryptedSecret) (productionSealedValue, map[string]productionSealedValue, error) {
	protection := s.customerProtection
	if protection.rows[customer.SourceKey] != plannedCustomerSourceDigest(customer) ||
		protection.secrets[secret.SecretID] != canonicalLegacyDigest(secret) {
		return productionSealedValue{}, nil, errInvalidProductionIdentity
	}
	identity, err := openProductionIdentity(protection.box, customer.SourceKey, secret)
	if err != nil {
		return productionSealedValue{}, nil, err
	}
	rawCredentials, err := productionCredentials(identity)
	if err != nil {
		return productionSealedValue{}, nil, err
	}
	token, err := protection.sealValue(customer.InternalID, "token", "subscription", identity.Customer.SubToken)
	if err != nil {
		return productionSealedValue{}, nil, err
	}
	credentials := make(map[string]productionSealedValue, len(rawCredentials))
	for protocol, raw := range rawCredentials {
		if protocol == "naive" {
			var encoded []byte
			var digest string
			encoded, digest, err = controlplane.SealNaiveCredentialIdentity(protection.box, customer.InternalID, identity.Customer.Naive.Username, raw)
			credentials[protocol] = productionSealedValue{base64.StdEncoding.EncodeToString(encoded), digest}
		} else if protocol == "awg" {
			var encoded []byte
			var digest string
			encoded, digest, err = controlplane.SealWGCredentialIdentity(protection.box, customer.InternalID, raw)
			credentials[protocol] = productionSealedValue{base64.StdEncoding.EncodeToString(encoded), digest}
		} else {
			credentials[protocol], err = protection.sealValue(customer.InternalID, "credential", protocol, raw)
		}
		if err != nil {
			return productionSealedValue{}, nil, err
		}
	}
	return token, credentials, nil
}

func (s *RQLiteApplyStore) productionCustomerKeyVersions(ctx context.Context) ([]int, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT secret_envelope AS envelope,'access' AS encoding FROM credentials
UNION ALL SELECT token_envelope AS envelope,'access' AS encoding FROM subscription_tokens
UNION ALL SELECT desired_envelope AS envelope,'identity' AS encoding FROM desired_node_state WHERE service_name='maestro-core'`})
	if err != nil || len(results) != 1 {
		return nil, errInvalidProductionIdentity
	}
	var versions []int
	for _, row := range results[0].Rows {
		encoded, ok := row["envelope"].(string)
		if row["encoding"] == "identity" {
			var identity LegacyEncryptedSecret
			if ok && decodeCanonicalOperation([]byte(encoded), &identity) == nil && identity.KeyVersion > 0 &&
				identity.OwnerType == "customer" && identity.Field == "identity" && identity.Kind == "customer-identity" {
				versions = append(versions, identity.KeyVersion)
				continue
			}
			// Ordinary mutations use the existing base64 Envelope encoding. Both
			// formats retain their own exact key version after the import.
		}
		raw, decoded := decodeCanonicalBase64(encoded)
		var envelope controlplane.Envelope
		if !ok || !decoded || json.Unmarshal(raw, &envelope) != nil || envelope.KeyVersion <= 0 {
			return nil, errInvalidProductionIdentity
		}
		versions = append(versions, envelope.KeyVersion)
	}
	return versions, nil
}

func (s *RQLiteApplyStore) verifyProductionParentReceipt(ctx context.Context, run ApplyRun) error {
	if s.customerProtection.snapshotKind != "delta" {
		return nil
	}
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT plan_sha256,target_sha256,batch_count,
(SELECT COUNT(*) FROM import_batches b WHERE b.import_run_id=r.import_run_id AND b.status='applied') AS applied_batches
FROM import_runs r WHERE source_sha256=? AND status='applied'`, Args: []any{run.ParentDigest}})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return errInvalidProductionIdentity
	}
	row := results[0].Rows[0]
	plan, planOK := row["plan_sha256"].(string)
	target, targetOK := row["target_sha256"].(string)
	count, countOK := applyRowInt(row["batch_count"])
	applied, appliedOK := applyRowInt(row["applied_batches"])
	if !planOK || !targetOK || !validCanonicalSHA256(plan) || !validCanonicalSHA256(target) || !countOK || !appliedOK || count < 0 || count != applied {
		return errInvalidProductionIdentity
	}
	return nil
}

func (s *RQLiteApplyStore) productionDeviceStatements(batch ApplyBatch, customer PlannedCustomer, secret LegacyEncryptedSecret) ([]rqlite.Statement, error) {
	identity, err := openProductionIdentity(s.customerProtection.box, customer.SourceKey, secret)
	if err != nil {
		return nil, err
	}
	devices := make(map[string]int64, len(identity.Customer.Devices))
	keys := make([]string, 0, len(identity.Customer.Devices))
	for raw, seen := range identity.Customer.Devices {
		hmac := s.customerProtection.box.LookupHMAC("device-identity", []byte(raw))
		if _, duplicate := devices[hmac]; duplicate {
			return nil, errInvalidProductionIdentity
		}
		devices[hmac] = seen.Unix()
		keys = append(keys, hmac)
	}
	sort.Strings(keys)
	gate := batchGateArgs(batch)
	var removed []string
	for key := range s.customerProtection.priorDeviceKeys[customer.SourceKey] {
		if _, exists := devices[key]; !exists {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	var statements []rqlite.Statement
	if len(removed) > 0 {
		args := []any{customer.InternalID, s.customerProtection.capturedAt.Unix()}
		for _, key := range removed {
			args = append(args, key)
		}
		statements = append(statements, rqlite.Statement{SQL: `UPDATE devices SET revoked=1
WHERE customer_id=? AND last_seen_at_unix<? AND device_key_hmac IN (` + sqlPlaceholders(len(removed)) + `) AND ` + batchWriteGate, Args: append(args, gate...)})
	}
	for _, key := range keys {
		deviceID := sha256Hex([]byte("import-device\x00" + customer.InternalID + "\x00" + key))
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix)
SELECT ?,?,?,'maestro',?,0,? WHERE ` + batchWriteGate + `
ON CONFLICT(customer_id,device_key_hmac) DO UPDATE SET
last_seen_at_unix=MAX(COALESCE(devices.last_seen_at_unix,0),excluded.last_seen_at_unix),
revoked=CASE WHEN devices.last_seen_at_unix>=? THEN devices.revoked ELSE 0 END`,
			Args: append(append([]any{deviceID, customer.InternalID, key, devices[key], s.now().Unix()}, gate...), s.customerProtection.capturedAt.Unix())})
	}
	return statements, nil
}
