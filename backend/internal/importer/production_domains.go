package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"golang.org/x/crypto/bcrypt"
)

var errInvalidProductionDomain = errors.New("unsupported or inconsistent protected setting or principal")

type productionDomainValue struct {
	envelope   string
	digest     string
	keyVersion int
}

func validProductionDomainID(value string) bool {
	return value != "" && utf8.ValidString(value) && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\x00\r\n\t")
}

// Source AAD is checked before decryption. The planned typed normalizer contract
// is setting/<key>/secret/<key> and principal/<source>/password/bcrypt; a real
// legacy producer for these domains is still separate work. The declarations in
// testdata/settings-principals-v1.json also permit setting/telegram/token/bot-token
// and principal/<source>/verifier/password-verifier, only with valid authenticated
// ciphertext under that exact AAD. Synthetic fixture ciphertext is never trusted.
// Output always uses the existing runtime reader scope and encoding.
func openProductionDomainSecret(box *controlplane.SecretBox, secret LegacyEncryptedSecret, owner, source string) ([]byte, error) {
	if box == nil || !validProductionDomainID(secret.SecretID) || secret.OwnerType != owner || secret.OwnerSourceKey != source || secret.KeyVersion <= 0 || !validCanonicalSHA256(secret.SHA256) {
		return nil, errInvalidProductionDomain
	}
	validScope := false
	switch owner {
	case "setting":
		validScope = secret.Field == "secret" && secret.Kind == source || source == "telegram" && secret.Field == "token" && secret.Kind == "bot-token"
	case "principal":
		validScope = secret.Field == "password" && secret.Kind == "bcrypt" || secret.Field == "verifier" && secret.Kind == "password-verifier"
	}
	if !validScope {
		return nil, errInvalidProductionDomain
	}
	nonce, nonceOK := decodeCanonicalBase64(secret.NonceB64)
	ciphertext, cipherOK := decodeCanonicalBase64(secret.CiphertextB64)
	if !nonceOK || !cipherOK || len(ciphertext) > 1<<20 {
		return nil, errInvalidProductionDomain
	}
	plain, err := box.Open(controlplane.SecretScope{OwnerType: owner, OwnerID: source, Field: secret.Field, Kind: secret.Kind}, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		return nil, errInvalidProductionDomain
	}
	if sha256Hex(plain) != secret.SHA256 {
		zeroBytes(plain)
		return nil, errInvalidProductionDomain
	}
	if owner == "principal" {
		if _, err := bcrypt.Cost(plain); err != nil {
			zeroBytes(plain)
			return nil, errInvalidProductionDomain
		}
	}
	return plain, nil
}

func validateProductionDomains(snapshot SnapshotProtection, validated *ProductionCustomerProtection, secrets map[string]LegacyEncryptedSecret) error {
	validated.settingRows = make(map[string]string, len(snapshot.Settings))
	validated.principalRows = make(map[string]string, len(snapshot.Principals))
	if len(snapshot.Settings)+len(snapshot.Principals) > 0 && ((snapshot.SnapshotKind != "full" && snapshot.SnapshotKind != "delta") || snapshot.CapturedAt.Unix() <= 0) {
		return errInvalidProductionDomain
	}
	checkSecret := func(id, owner, source string) error {
		secret, ok := secrets[id]
		if !ok || validated.secrets[id] != "" {
			return errInvalidProductionDomain
		}
		plain, err := openProductionDomainSecret(validated.box, secret, owner, source)
		if err != nil {
			return err
		}
		zeroBytes(plain)
		validated.secrets[id] = canonicalLegacyDigest(secret)
		return nil
	}
	for _, setting := range snapshot.Settings {
		if !validProductionDomainID(setting.Key) || setting.Generation < 1 || len(setting.PublicValueJSON) > 1<<20 || !json.Valid(setting.PublicValueJSON) || validated.settingRows[setting.Key] != "" {
			return errInvalidProductionDomain
		}
		if setting.SecretRef != "" {
			if err := checkSecret(setting.SecretRef, "setting", setting.Key); err != nil {
				return err
			}
		}
		validated.settingRows[setting.Key] = canonicalLegacyDigest(setting)
	}
	logins := map[string]bool{}
	for _, principal := range snapshot.Principals {
		if !validProductionDomainID(principal.SourceKey) || !validCanonicalSHA256(principal.LoginKeyHMAC) || logins[principal.LoginKeyHMAC] || (principal.Status != "active" && principal.Status != "disabled") || validated.principalRows[principal.SourceKey] != "" || len(principal.Roles) == 0 {
			return errInvalidProductionDomain
		}
		roles := map[string]bool{}
		for _, role := range principal.Roles {
			if !validProductionDomainID(role) || roles[role] {
				return errInvalidProductionDomain
			}
			roles[role] = true
		}
		if err := checkSecret(principal.CredentialSecretRef, "principal", principal.SourceKey); err != nil {
			return err
		}
		logins[principal.LoginKeyHMAC] = true
		validated.principalRows[principal.SourceKey] = canonicalLegacyDigest(principal)
	}
	for _, secret := range secrets {
		if (secret.OwnerType == "setting" || secret.OwnerType == "principal") && validated.secrets[secret.SecretID] == "" {
			return errInvalidProductionDomain
		}
	}
	validated.domainsValidated = true
	return nil
}

func (protection *ProductionCustomerProtection) resealDomain(secret LegacyEncryptedSecret, owner, source, target, field, kind string) (productionDomainValue, error) {
	if protection == nil || !protection.domainsValidated || protection.secrets[secret.SecretID] != canonicalLegacyDigest(secret) || !validProductionDomainID(target) {
		return productionDomainValue{}, errInvalidProductionDomain
	}
	plain, err := openProductionDomainSecret(protection.box, secret, owner, source)
	if err != nil {
		return productionDomainValue{}, err
	}
	zeroBytes(plain)
	nonce, _ := decodeCanonicalBase64(secret.NonceB64)
	ciphertext, _ := decodeCanonicalBase64(secret.CiphertextB64)
	envelope, err := protection.box.Rebind(
		controlplane.SecretScope{OwnerType: owner, OwnerID: source, Field: secret.Field, Kind: secret.Kind},
		controlplane.SecretScope{OwnerType: owner, OwnerID: target, Field: field, Kind: kind},
		controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		return productionDomainValue{}, errInvalidProductionDomain
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return productionDomainValue{}, errInvalidProductionDomain
	}
	return productionDomainValue{envelope: base64.StdEncoding.EncodeToString(raw), digest: secret.SHA256, keyVersion: envelope.KeyVersion}, nil
}

func (s *RQLiteApplyStore) productionSettingValue(setting LegacySetting, secret *LegacyEncryptedSecret) (*productionDomainValue, error) {
	p := s.customerProtection
	if p == nil || !p.domainsValidated || p.settingRows[setting.Key] != canonicalLegacyDigest(setting) {
		return nil, errInvalidProductionDomain
	}
	if setting.SecretRef == "" {
		if secret != nil {
			return nil, errInvalidProductionDomain
		}
		return nil, nil
	}
	if secret == nil || secret.SecretID != setting.SecretRef {
		return nil, errInvalidProductionDomain
	}
	value, err := p.resealDomain(*secret, "setting", setting.Key, setting.Key, "secret", setting.Key)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *RQLiteApplyStore) productionPrincipalValue(principal PlannedPrincipal, secret LegacyEncryptedSecret) (productionDomainValue, error) {
	p := s.customerProtection
	row := LegacyPrincipal{SourceKey: principal.SourceKey, LoginKeyHMAC: principal.LoginKeyHMAC, Status: principal.Status, Roles: principal.Roles, CredentialSecretRef: principal.CredentialSecretRef}
	// The production CLI fixes this namespace. A caller-supplied SHA-shaped ID
	// is not authority to move an authenticated principal's credential or roles.
	if p == nil || !p.domainsValidated || p.principalRows[principal.SourceKey] != canonicalLegacyDigest(row) || principal.CredentialSecretRef != secret.SecretID || principal.InternalID != deterministicID("maestro-legacy-v1", "principal", principal.SourceKey) {
		return productionDomainValue{}, errInvalidProductionDomain
	}
	return p.resealDomain(secret, "principal", principal.SourceKey, principal.InternalID, "password", "bcrypt")
}

// Durable readers validate the actual runtime AAD and plaintext digest as well
// as the encoded version. No source version is substituted for stored evidence.
func (s *RQLiteApplyStore) productionDomainEnvelopeVersion(encoded string, scope controlplane.SecretScope, digest string, expectedVersion int) (int, error) {
	if s.customerProtection == nil || s.customerProtection.box == nil || !validCanonicalSHA256(digest) || len(encoded) > 2<<20 {
		return 0, errInvalidProductionDomain
	}
	raw, ok := decodeCanonicalBase64(encoded)
	var envelope controlplane.Envelope
	if !ok || decodeCanonicalOperation(raw, &envelope) != nil || envelope.KeyVersion <= 0 || (expectedVersion != 0 && envelope.KeyVersion != expectedVersion) {
		return 0, errInvalidProductionDomain
	}
	plain, err := s.customerProtection.box.Open(scope, envelope)
	if err != nil {
		return 0, errInvalidProductionDomain
	}
	defer zeroBytes(plain)
	if sha256Hex(plain) != digest {
		return 0, errInvalidProductionDomain
	}
	return envelope.KeyVersion, nil
}

func (s *RQLiteApplyStore) productionDomainKeyVersions(ctx context.Context) ([]int, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT 'setting' AS owner,setting_key AS owner_id,secret_envelope AS envelope,secret_sha256 AS digest,key_version FROM setting_secrets
UNION ALL SELECT 'principal' AS owner,principal_id AS owner_id,verifier_envelope AS envelope,verifier_sha256 AS digest,0 AS key_version FROM principal_credentials`})
	if err != nil || len(results) != 1 {
		return nil, errInvalidProductionDomain
	}
	var versions []int
	for _, row := range results[0].Rows {
		owner, ownerOK := row["owner"].(string)
		id, idOK := row["owner_id"].(string)
		encoded, encodedOK := row["envelope"].(string)
		digest, digestOK := row["digest"].(string)
		expected, versionOK := applyRowInt(row["key_version"])
		if !ownerOK || !idOK || !validProductionDomainID(id) || !encodedOK || !digestOK || !versionOK || expected < 0 || int64(int(expected)) != expected {
			return nil, errInvalidProductionDomain
		}
		scope := controlplane.SecretScope{OwnerType: owner, OwnerID: id}
		switch owner {
		case "setting":
			if expected <= 0 {
				return nil, errInvalidProductionDomain
			}
			scope.Field, scope.Kind = "secret", id
		case "principal":
			if expected != 0 {
				return nil, errInvalidProductionDomain
			}
			scope.Field, scope.Kind = "password", "bcrypt"
		default:
			return nil, errInvalidProductionDomain
		}
		version, err := s.productionDomainEnvelopeVersion(encoded, scope, digest, int(expected))
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, nil
}
