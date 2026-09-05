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
// original legacy record. SubID and Generation require authoritative source
// metadata: the legacy customer JSON does not contain either value.
//
// LookupHMAC domains are customer-login (canonical login), subscription-token,
// customer-uuid, subscription-id, and customer-credentials. The last hashes the
// JSON encoding of the protocol -> raw credential map, with sorted JSON keys.
// Devices, WG and independently provisioned VLESS3/4 UUIDs cannot currently be
// represented by the production reader and must block migration before writes.
type ProductionCustomerIdentity struct {
	SchemaVersion int                  `json:"schema_version"`
	Customer      legacystore.Customer `json:"customer"`
	SubID         string               `json:"sub_id"`
	Generation    int64                `json:"generation"`
}

// ProductionCustomerProtection contains validated digests and ciphertext only.
// Callers cannot construct an enabled production adapter without validation.
type ProductionCustomerProtection struct {
	box          *controlplane.SecretBox
	sourceDigest string
	rows         map[string]string
	secrets      map[string]string
}

func ValidateProductionCustomerIdentities(protection SnapshotProtection, box *controlplane.SecretBox) (*ProductionCustomerProtection, error) {
	if box == nil || !validCanonicalSHA256(protection.SourceDigest) {
		return nil, errInvalidProductionIdentity
	}
	validated := &ProductionCustomerProtection{box: box, sourceDigest: protection.SourceDigest,
		rows: make(map[string]string), secrets: make(map[string]string)}
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
	if customer.VLESS == nil || customer.VLESS.UUID == "" || customer.WG != nil || len(customer.Devices) != 0 ||
		(customer.VLESS3 != nil && customer.VLESS3.UUID != customer.VLESS.UUID) ||
		(customer.VLESS4 != nil && customer.VLESS4.UUID != customer.VLESS.UUID) {
		return nil, errInvalidProductionIdentity
	}
	credentials := map[string]string{"vless": customer.VLESS.UUID}
	if customer.Hy2 != nil {
		if customer.Hy2.User != customer.Login {
			return nil, errInvalidProductionIdentity
		}
		credentials["hysteria2"] = customer.Hy2.Pass
	}
	if customer.Naive != nil {
		if customer.Naive.Username != customer.Login {
			return nil, errInvalidProductionIdentity
		}
		credentials["naive"] = customer.Naive.Password
	}
	if customer.AnyTLS != nil {
		credentials["anytls"] = customer.AnyTLS.Password
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
	login, err := controlplane.CanonicalLoginKey(customer.Login)
	credentials, credentialErr := productionCredentials(identity)
	if err != nil || credentialErr != nil || customer.Login != row.Login ||
		customer.SubToken == "" || len(customer.SubToken) > 4096 || strings.ContainsRune(customer.SubToken, 0) ||
		identity.SubID == "" || len(identity.SubID) > 4096 || strings.ContainsRune(identity.SubID, 0) ||
		identity.Generation <= 0 || identity.Generation != row.Generation ||
		customer.Expires.Nanosecond() != 0 || customer.Expires.Unix() != row.ExpiresAtUnix ||
		(row.Status != "active" && row.Status != "disabled") || customer.Disabled != (row.Status == "disabled") {
		return errInvalidProductionIdentity
	}
	fingerprint, err := json.Marshal(credentials)
	if err != nil {
		return errInvalidProductionIdentity
	}
	defer zeroBytes(fingerprint)
	if box.LookupHMAC("customer-login", []byte(login)) != row.LoginKeyHMAC ||
		box.LookupHMAC("subscription-token", []byte(customer.SubToken)) != row.TokenHMAC ||
		box.LookupHMAC("customer-uuid", []byte(customer.VLESS.UUID)) != row.UUIDHMAC ||
		box.LookupHMAC("subscription-id", []byte(identity.SubID)) != row.SubIDHMAC ||
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
		credentials[protocol], err = protection.sealValue(customer.InternalID, "credential", protocol, raw)
		if err != nil {
			return productionSealedValue{}, nil, err
		}
	}
	return token, credentials, nil
}

func (s *RQLiteApplyStore) productionCustomerKeyVersions(ctx context.Context) ([]int, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT secret_envelope AS envelope FROM credentials
UNION ALL SELECT token_envelope AS envelope FROM subscription_tokens`})
	if err != nil || len(results) != 1 {
		return nil, errInvalidProductionIdentity
	}
	var versions []int
	for _, row := range results[0].Rows {
		encoded, ok := row["envelope"].(string)
		raw, decoded := decodeCanonicalBase64(encoded)
		var envelope controlplane.Envelope
		if !ok || !decoded || json.Unmarshal(raw, &envelope) != nil || envelope.KeyVersion <= 0 {
			return nil, errInvalidProductionIdentity
		}
		versions = append(versions, envelope.KeyVersion)
	}
	return versions, nil
}
