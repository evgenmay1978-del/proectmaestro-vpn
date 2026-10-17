package importer

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const legacyRuntimeCurrentCapture = "legacy:runtime:current-capture-v1"
const legacyRuntimeCurrentOTA = "legacy:runtime:current-ota-v1"

func hasNativeRuntimeSource(hashes map[string]string) bool {
	for _, key := range []string{"legacy:settings:runtime-converted-v1", "legacy:principals:runtime-converted-v1", legacyRuntimeCurrentCapture, legacyRuntimeCurrentOTA} {
		if _, exists := hashes[key]; exists {
			return true
		}
	}
	return false
}

func openRuntimeSource(box *controlplane.SecretBox, secret LegacyEncryptedSecret) ([]byte, error) {
	nonce, nOK := decodeCanonicalBase64(secret.NonceB64)
	cipher, cOK := decodeCanonicalBase64(secret.CiphertextB64)
	if box == nil || !nOK || !cOK || secret.KeyVersion <= 0 {
		return nil, ErrLegacyRuntimeDomains
	}
	raw, err := box.Open(controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
	if err != nil || sha256Hex(raw) != secret.SHA256 {
		zeroBytes(raw)
		return nil, ErrLegacyRuntimeDomains
	}
	return raw, nil
}

func runtimeSecretMap(secrets []LegacyEncryptedSecret) (map[string]LegacyEncryptedSecret, error) {
	result := map[string]LegacyEncryptedSecret{}
	for _, secret := range secrets {
		if _, exists := result[secret.SecretID]; exists {
			return nil, ErrLegacyRuntimeDomains
		}
		result[secret.SecretID] = secret
	}
	return result, nil
}

func runtimeCapsuleSource(secrets map[string]LegacyEncryptedSecret, sha string, box *controlplane.SecretBox) (LegacyRuntimeCapsule, error) {
	var capsule LegacyRuntimeCapsule
	secret, exists := secrets["legacy-runtime-source-v1:"+sha]
	if !exists || !validCanonicalSHA256(sha) || secret.OwnerType != "legacy_runtime_source" || secret.OwnerSourceKey != sha || secret.Field != "raw_capsule" || secret.Kind != "runtime-domains-v1" || secret.SHA256 != sha {
		return capsule, ErrLegacyRuntimeDomains
	}
	raw, err := openRuntimeSource(box, secret)
	if err != nil {
		return capsule, err
	}
	defer zeroBytes(raw)
	if runtimeDomainDecode(raw, &capsule) != nil {
		return capsule, ErrLegacyRuntimeDomains
	}
	return capsule, nil
}

func runtimeCapsuleBound(capsule LegacyRuntimeCapsule, protection SnapshotProtection) bool {
	p := capsule.ProcessBefore
	return capsule.SchemaVersion == 1 && p == capsule.ProcessAfter && p.PID > 0 && p.StartTicks > 0 &&
		validCanonicalSHA256(p.ExecutableSHA256) && validCanonicalSHA256(p.EnvironmentSHA256) &&
		validCanonicalSHA256(p.CustomersSHA256) && p.CustomersSHA256 == protection.SourceHashes["customers"] &&
		capsule.CustomerCount == len(protection.Customers) && capsule.CapturedAt.Unix() > 0 && !capsule.CompletedAt.Before(capsule.CapturedAt)
}

// Only the live process/capture and customer data may advance here. A changed
// native configuration needs an explicit conflict resolution, not a lossy merge.
func runtimeCapsulesUnchanged(old, current LegacyRuntimeCapsule) bool {
	return old.ProcessBefore.ExecutableSHA256 == current.ProcessBefore.ExecutableSHA256 &&
		old.ProcessBefore.EnvironmentSHA256 == current.ProcessBefore.EnvironmentSHA256 &&
		reflect.DeepEqual(old.Environment, current.Environment) && reflect.DeepEqual(old.Files, current.Files) &&
		!current.CapturedAt.Before(old.CapturedAt)
}

func normalizeLegacyRuntimeDelta(snapshot *Snapshot, customersRaw []byte, source *LegacyRuntimeSource, box *controlplane.SecretBox, now time.Time, maxAge time.Duration, parent *Snapshot) error {
	if parent == nil || parent.SnapshotKind != "full" || snapshot.SnapshotKind != "delta" || snapshot.ParentSourceDigest != digestSnapshot(*parent) || !hasNativeRuntimeSource(parent.SourceHashes) {
		return ErrLegacyRuntimeDomains
	}
	parentProof, err := validateNativeRuntimeProof(ProtectionFromSnapshot(*parent), box)
	if err != nil || len(parentProof) == 0 {
		return ErrLegacyRuntimeDomains
	}
	var capsule LegacyRuntimeCapsule
	if runtimeDomainDecode(source.RawCapsule, &capsule) != nil || !runtimeCapsuleBound(capsule, ProtectionFromSnapshot(*snapshot)) ||
		sha256Hex(customersRaw) != capsule.ProcessBefore.CustomersSHA256 || now.IsZero() || maxAge <= 0 ||
		capsule.CompletedAt.After(now) || now.Sub(capsule.CapturedAt) > maxAge {
		return ErrLegacyRuntimeDomains
	}
	// The normalizer already authenticates customer identities. Check the raw
	// capture's exact count here as well; no old-customer bytes stand in for it.
	customers, err := DecodeLegacyCustomers(customersRaw)
	if err != nil || len(customers) != capsule.CustomerCount {
		return ErrLegacyRuntimeDomains
	}
	currentSHA := sha256Hex(source.RawCapsule)
	for _, domain := range []string{"settings", "principals"} {
		if snapshot.SourceHashes["legacy:"+domain+":present-unconverted"] != currentSHA {
			return ErrLegacyRuntimeDomains
		}
	}
	secrets, err := runtimeSecretMap(snapshot.EncryptedSecrets)
	if err != nil {
		return err
	}
	for _, secret := range parent.EncryptedSecrets {
		if _, retained := parentProof[secret.SecretID]; !retained {
			continue
		}
		if old, exists := secrets[secret.SecretID]; exists && old != secret {
			return ErrLegacyRuntimeDomains
		}
		secrets[secret.SecretID] = secret
	}
	sealArchive := func(id, owner, field, kind string, raw []byte) error {
		sha := sha256Hex(raw)
		if old, exists := secrets[id]; exists {
			plain, err := openRuntimeSource(box, old)
			defer zeroBytes(plain)
			if err != nil || old.OwnerType != owner || old.OwnerSourceKey != sha || old.Field != field || old.Kind != kind || old.SHA256 != sha || !reflect.DeepEqual(plain, raw) {
				return ErrLegacyRuntimeDomains
			}
			return nil
		}
		envelope, err := box.Seal(controlplane.SecretScope{OwnerType: owner, OwnerID: sha, Field: field, Kind: kind}, raw)
		if err != nil {
			return ErrLegacyRuntimeDomains
		}
		secrets[id] = LegacyEncryptedSecret{SecretID: id, OwnerType: owner, OwnerSourceKey: sha, Field: field, Kind: kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha}
		return nil
	}
	if err := sealArchive("legacy-runtime-source-v1:"+currentSHA, "legacy_runtime_source", "raw_capsule", "runtime-domains-v1", source.RawCapsule); err != nil {
		return err
	}
	for _, row := range parent.Settings {
		if !reservedRuntimeSecretID(row.SecretRef) {
			continue
		}
		for _, existing := range snapshot.Settings {
			if existing.Key == row.Key {
				return ErrLegacyRuntimeDomains
			}
		}
		snapshot.Settings = append(snapshot.Settings, cloneRuntimeSettings([]LegacySetting{row})[0])
	}
	for _, row := range parent.Principals {
		if !reservedRuntimeSecretID(row.CredentialSecretRef) {
			continue
		}
		for _, existing := range snapshot.Principals {
			if existing.SourceKey == row.SourceKey || existing.LoginKeyHMAC == row.LoginKeyHMAC {
				return ErrLegacyRuntimeDomains
			}
		}
		snapshot.Principals = append(snapshot.Principals, clonePrincipals([]LegacyPrincipal{row})[0])
	}
	for _, domain := range []string{"settings", "principals"} {
		delete(snapshot.SourceHashes, "legacy:"+domain+":present-unconverted")
		snapshot.SourceHashes["legacy:"+domain+":runtime-converted-v1"] = parent.SourceHashes["legacy:"+domain+":runtime-converted-v1"]
	}
	snapshot.SourceHashes[legacyRuntimeCurrentCapture] = currentSHA
	oldOTA, hadOTA := parent.SourceHashes["legacy:ota:absent-v1"]
	if hadOTA != (len(source.RawOTAAbsence) != 0) {
		return ErrLegacyRuntimeDomains
	}
	if hadOTA {
		check := &LegacyRuntimeDomains{capsuleSHA: currentSHA}
		if err := check.appendOTAAbsence(source.RawOTAAbsence, capsule, box, false, time.Time{}, 0); err != nil {
			return err
		}
		var evidence LegacyRuntimeOTAAbsence
		if runtimeDomainDecode(source.RawOTAAbsence, &evidence) != nil || evidence.ObservedAt.After(now) || now.Sub(evidence.ObservedAt) > maxAge {
			return ErrLegacyRuntimeDomains
		}
		newOTA := sha256Hex(source.RawOTAAbsence)
		if newOTA != oldOTA {
			if err := sealArchive("legacy-runtime-ota-source-v1:"+newOTA, "legacy_runtime_ota_source", "raw_absence", "ota-absence-v1", source.RawOTAAbsence); err != nil {
				return err
			}
		}
		snapshot.SourceHashes["legacy:ota:absent-v1"] = oldOTA
		snapshot.SourceHashes[legacyRuntimeCurrentOTA] = newOTA
	}
	snapshot.EncryptedSecrets = nil
	for _, secret := range secrets {
		snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, secret)
	}
	_, err = validateNativeRuntimeDeltaProof(ProtectionFromSnapshot(*snapshot, parent), box)
	return err
}

func validateNativeRuntimeDeltaProof(protection SnapshotProtection, box *controlplane.SecretBox) (map[string]string, error) {
	parent := protection.Parent
	if box == nil || protection.SnapshotKind != "delta" || parent == nil || parent.SnapshotKind != "full" || !validCanonicalSHA256(protection.ParentSourceDigest) || parent.SourceDigest != protection.ParentSourceDigest || parent.ClusterHMACKeySHA256 != protection.ClusterHMACKeySHA256 || !protection.CapturedAt.After(parent.CapturedAt) {
		return nil, ErrLegacyRuntimeDomains
	}
	parentProof, err := validateNativeRuntimeProof(*parent, box)
	if err != nil || len(parentProof) == 0 {
		return nil, ErrLegacyRuntimeDomains
	}
	for _, domain := range []string{"settings", "principals"} {
		if protection.SourceHashes["legacy:"+domain+":runtime-converted-v1"] != parent.SourceHashes["legacy:"+domain+":runtime-converted-v1"] {
			return nil, ErrLegacyRuntimeDomains
		}
		for _, state := range []string{"absent", "present-unconverted"} {
			if _, exists := protection.SourceHashes["legacy:"+domain+":"+state]; exists {
				return nil, ErrLegacyRuntimeDomains
			}
		}
	}
	secrets, err := runtimeSecretMap(protection.EncryptedSecrets)
	if err != nil {
		return nil, err
	}
	oldSecrets, err := runtimeSecretMap(parent.EncryptedSecrets)
	if err != nil {
		return nil, err
	}
	proof := map[string]string{}
	for id, digest := range parentProof {
		if secrets[id] != oldSecrets[id] {
			return nil, ErrLegacyRuntimeDomains
		}
		proof[id] = digest
	}
	old, err := runtimeCapsuleSource(oldSecrets, parent.SourceHashes["legacy:settings:runtime-converted-v1"], box)
	if err != nil {
		return nil, err
	}
	currentSHA := protection.SourceHashes[legacyRuntimeCurrentCapture]
	current, err := runtimeCapsuleSource(secrets, currentSHA, box)
	if err != nil || !runtimeCapsuleBound(current, protection) || !runtimeCapsulesUnchanged(old, current) {
		return nil, ErrLegacyRuntimeDomains
	}
	currentSource := secrets["legacy-runtime-source-v1:"+currentSHA]
	proof[currentSource.SecretID] = canonicalLegacyDigest(currentSource)
	for _, customer := range protection.Customers {
		identity, err := openProductionIdentity(box, customer.SourceKey, secrets[customer.IdentitySecretRef])
		if err != nil || validateProductionIdentity(box, customer, identity) != nil {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	// Validate the unchanged files against the current customer set, while the
	// original member's whole-row SHA remains historical evidence of the full.
	derived, err := deriveLegacyRuntimeDomains(nil, current, Snapshot{Customers: protection.Customers}, box, false)
	if err != nil {
		return nil, err
	}
	settings := map[string]LegacySetting{}
	for _, row := range protection.Settings {
		if _, exists := settings[row.Key]; exists {
			return nil, ErrLegacyRuntimeDomains
		}
		settings[row.Key] = row
	}
	for _, prior := range parent.Settings {
		if !reservedRuntimeSecretID(prior.SecretRef) {
			continue
		}
		if canonicalLegacyDigest(settings[prior.Key]) != canonicalLegacyDigest(prior) {
			return nil, ErrLegacyRuntimeDomains
		}
		delete(settings, prior.Key)
		for _, candidate := range derived.settings {
			if candidate.Key != prior.Key {
				continue
			}
			if len(candidate.Members) != len(prior.Members) {
				return nil, ErrLegacyRuntimeDomains
			}
			for i, member := range candidate.Members {
				member.CustomerSHA256 = prior.Members[i].CustomerSHA256
				if member != prior.Members[i] {
					return nil, ErrLegacyRuntimeDomains
				}
			}
		}
	}
	for _, row := range settings {
		if len(row.Members) != 0 || reservedRuntimeSecretID(row.SecretRef) {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	principals := map[string]LegacyPrincipal{}
	for _, row := range protection.Principals {
		if _, exists := principals[row.SourceKey]; exists {
			return nil, ErrLegacyRuntimeDomains
		}
		principals[row.SourceKey] = row
	}
	for _, prior := range parent.Principals {
		if !reservedRuntimeSecretID(prior.CredentialSecretRef) {
			continue
		}
		if canonicalLegacyDigest(principals[prior.SourceKey]) != canonicalLegacyDigest(prior) {
			return nil, ErrLegacyRuntimeDomains
		}
		delete(principals, prior.SourceKey)
	}
	for _, row := range principals {
		if reservedRuntimeSecretID(row.CredentialSecretRef) {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	oldOTA, hadOTA := parent.SourceHashes["legacy:ota:absent-v1"]
	if ota, exists := protection.SourceHashes["legacy:ota:absent-v1"]; exists != hadOTA || ota != oldOTA {
		return nil, ErrLegacyRuntimeDomains
	}
	newOTA, hasCurrentOTA := protection.SourceHashes[legacyRuntimeCurrentOTA]
	if hasCurrentOTA != hadOTA {
		return nil, ErrLegacyRuntimeDomains
	}
	if hadOTA {
		if !validCanonicalSHA256(newOTA) {
			return nil, ErrLegacyRuntimeDomains
		}
		secret := secrets["legacy-runtime-ota-source-v1:"+newOTA]
		if newOTA == oldOTA {
			secret = secrets["runtime-setting-v1:ota:"+oldOTA]
		} else if secret.OwnerType != "legacy_runtime_ota_source" || secret.OwnerSourceKey != newOTA || secret.Field != "raw_absence" || secret.Kind != "ota-absence-v1" || secret.SHA256 != newOTA {
			return nil, ErrLegacyRuntimeDomains
		}
		raw, err := openRuntimeSource(box, secret)
		if err != nil {
			return nil, err
		}
		defer zeroBytes(raw)
		check := &LegacyRuntimeDomains{capsuleSHA: currentSHA}
		if check.appendOTAAbsence(raw, current, box, false, time.Time{}, 0) != nil {
			return nil, ErrLegacyRuntimeDomains
		}
		oldRaw, err := openRuntimeSource(box, oldSecrets["runtime-setting-v1:ota:"+oldOTA])
		if err != nil {
			return nil, err
		}
		defer zeroBytes(oldRaw)
		var before, after LegacyRuntimeOTAAbsence
		if runtimeDomainDecode(oldRaw, &before) != nil || runtimeDomainDecode(raw, &after) != nil || before.Directory != after.Directory || after.ObservedAt.Before(before.ObservedAt) {
			return nil, ErrLegacyRuntimeDomains
		}
		proof[secret.SecretID] = canonicalLegacyDigest(secret)
	}
	for id, secret := range secrets {
		if reservedRuntimeSecret(secret) && proof[id] != canonicalLegacyDigest(secret) {
			return nil, ErrLegacyRuntimeDomains
		}
	}
	return proof, nil
}

// Structural planning accepts historical customer-row digests only for the
// exact immutable setting inherited from the supplied, digest-bound parent.
func inheritedRuntimeSetting(snapshot Snapshot, setting LegacySetting, options PlanOptions) bool {
	parent := options.ParentSnapshot
	if snapshot.SnapshotKind != "delta" || parent == nil || !hasNativeRuntimeSource(parent.SourceHashes) || !validCanonicalSHA256(snapshot.SourceHashes[legacyRuntimeCurrentCapture]) || snapshot.ParentSourceDigest != digestSnapshot(*parent) || options.AppliedParentDigest != snapshot.ParentSourceDigest || !reservedRuntimeSecretID(setting.SecretRef) {
		return false
	}
	for _, row := range parent.Settings {
		if row.Key == setting.Key {
			return canonicalLegacyDigest(row) == canonicalLegacyDigest(setting)
		}
	}
	return false
}

func (s *RQLiteApplyStore) preserveRuntimeSetting(batch ApplyBatch, setting LegacySetting, secret LegacyEncryptedSecret) ([]rqlite.Statement, error) {
	p := s.customerProtection
	if p == nil || p.snapshotKind != "delta" || p.runtimeSecrets[secret.SecretID] != canonicalLegacyDigest(secret) || p.settingRows[setting.Key] != canonicalLegacyDigest(setting) || setting.SecretRef != secret.SecretID {
		return nil, ErrLegacyRuntimeDomains
	}
	raw, err := json.Marshal(secret)
	if err != nil {
		return nil, err
	}
	guard := `EXISTS(SELECT 1 FROM imported_secrets i JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id AND e.target_id=i.secret_id AND e.lifecycle='active' WHERE i.secret_id=? AND i.owner_type='setting' AND i.owner_source_key=? AND i.field='secret' AND i.kind=? AND i.key_version=? AND i.secret_envelope=? AND i.secret_sha256=? AND e.canonical_sha256=?) AND EXISTS(SELECT 1 FROM cluster_settings c JOIN setting_secrets s ON s.setting_key=c.setting_key WHERE c.setting_key=? AND c.generation>=? AND s.key_version>0 AND length(s.secret_sha256)=64 AND length(s.secret_envelope)>0)`
	args := []any{secret.SecretID, setting.Key, setting.Key, secret.KeyVersion, string(raw), secret.SHA256, canonicalLegacyDigest(secret), setting.Key, setting.Generation}
	for _, member := range setting.Members {
		rowSHA := p.rows[member.CustomerSourceKey]
		if !validCanonicalSHA256(rowSHA) {
			return nil, ErrLegacyRuntimeDomains
		}
		guard += ` AND EXISTS(SELECT 1 FROM customers c JOIN imported_entity_state e ON e.entity_kind='customer' AND e.target_id=c.customer_id AND e.lifecycle='active' WHERE c.customer_id=? AND c.login_key_hmac=? AND c.display_login=? AND e.source_key=? AND e.canonical_sha256=?)`
		args = append(args, member.CustomerID, member.LoginHMAC, member.Login, member.CustomerSourceKey, rowSHA)
	}
	return []rqlite.Statement{{SQL: `SELECT CASE WHEN NOT (` + batchWriteGate + `) OR (` + guard + `) THEN 1 ELSE json('native-runtime-delta-target-conflict') END`, Args: append(batchGateArgs(batch), args...)}}, nil
}

func (s *RQLiteApplyStore) preserveRuntimePrincipal(batch ApplyBatch, principal PlannedPrincipal, secret LegacyEncryptedSecret) ([]rqlite.Statement, error) {
	p := s.customerProtection
	if p == nil || p.snapshotKind != "delta" || p.runtimeSecrets[secret.SecretID] != canonicalLegacyDigest(secret) || !strings.HasPrefix(secret.SecretID, "runtime-panel-password-v1:") {
		return nil, ErrLegacyRuntimeDomains
	}
	return []rqlite.Statement{{SQL: `SELECT CASE WHEN NOT (` + batchWriteGate + `) OR EXISTS(SELECT 1 FROM principals p WHERE p.principal_id=? AND p.login_key_hmac=? AND EXISTS(SELECT 1 FROM principal_credentials c WHERE c.principal_id=p.principal_id AND c.credential_type='password' AND length(c.verifier_envelope)>0 AND length(c.verifier_sha256)=64)) THEN 1 ELSE json('native-runtime-delta-principal-conflict') END`, Args: append(batchGateArgs(batch), principal.InternalID, principal.LoginKeyHMAC)}}, nil
}
