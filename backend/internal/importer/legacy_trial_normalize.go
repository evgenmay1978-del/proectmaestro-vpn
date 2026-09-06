package importer

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const legacyTrialConvertedSource = "legacy:trials:present-converted-v1"
const legacyTrialEvidencePrefix = "legacy-trial-evidence-v1:"
const legacyTrialEvidenceOwner = "trial_evidence"

// LegacyTrialSource contains exact protected file and effective runtime salt
// bytes. The caller owns and clears these buffers after normalization.
type LegacyTrialSource struct{ RawJSON, Salt []byte }

type nativeTrialLedger struct {
	Anchors map[string]string
	DRM     map[string]string
	Audit   json.RawMessage
}

// An immutable private proof is created only after the raw source, salt,
// derived used rows and retained parent evidence have all authenticated.
type legacyTrialProof struct {
	sourceDigest, snapshotKind, parentDigest string
	evidence                                 map[string]LegacyEncryptedSecret
	trials                                   map[string]LegacyTrial
}

func decodeNativeTrialLedger(raw []byte) (nativeTrialLedger, error) {
	invalid := func() (nativeTrialLedger, error) { return nativeTrialLedger{}, errInvalidSnapshotProtection }
	if len(raw) == 0 || len(raw) > 64<<20 || !utf8.Valid(raw) || rejectLegacySurrogates(raw) != nil || rejectDuplicateLegacyJSON(raw) != nil {
		return invalid()
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil || document == nil {
		return invalid()
	}
	ledger := nativeTrialLedger{Anchors: map[string]string{}, DRM: map[string]string{}}
	for key, value := range document {
		switch key {
		case "redeemed_anchors", "redeemed_drm":
			var entries map[string]string
			if json.Unmarshal(value, &entries) != nil {
				return invalid()
			}
			for hash, login := range entries {
				if !validCanonicalSHA256(hash) || login == "" || len(login) > 4096 || strings.ContainsRune(login, 0) {
					return invalid()
				}
			}
			if key == "redeemed_anchors" {
				ledger.Anchors = entries
			} else {
				ledger.DRM = entries
			}
		case "audit":
			// The entire file is retained, including every audit field and byte.
			// Audit is not used to invent a current device identity or an expiry.
			var audit []json.RawMessage
			if json.Unmarshal(value, &audit) != nil {
				return invalid()
			}
			for _, item := range audit {
				var record map[string]json.RawMessage
				if json.Unmarshal(item, &record) != nil || record == nil {
					return invalid()
				}
			}
			ledger.Audit = append(json.RawMessage(nil), value...)
		default:
			return invalid()
		}
	}
	return ledger, nil
}

func nativeTrialRows(ledger nativeTrialLedger, saltSHA string) []LegacyTrial {
	var rows []LegacyTrial
	for _, group := range []struct {
		kind   string
		values map[string]string
	}{{"anchor", ledger.Anchors}, {"drm", ledger.DRM}} {
		for hash := range group.values {
			rows = append(rows, LegacyTrial{SourceKey: "s1:trial:" + group.kind + "-v1:" + saltSHA + ":" + hash, IdentityKind: "legacy-" + group.kind + "-v1", LegacyAnchorHMAC: hash, Used: true})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].SourceKey < rows[j].SourceKey })
	return rows
}

func trialLedgerContains(current, prior nativeTrialLedger) bool {
	for _, pair := range [][2]map[string]string{{current.Anchors, prior.Anchors}, {current.DRM, prior.DRM}} {
		for hash, login := range pair[1] {
			if pair[0][hash] != login {
				return false
			}
		}
	}
	return true
}

func reservedTrialEvidence(secret LegacyEncryptedSecret) bool {
	return strings.HasPrefix(secret.SecretID, legacyTrialEvidencePrefix) || secret.OwnerType == legacyTrialEvidenceOwner || secret.Kind == "legacy-promo-v1"
}

func validTrialEvidenceMetadata(secret LegacyEncryptedSecret) bool {
	return validCanonicalSHA256(secret.SHA256) && secret.SecretID == legacyTrialEvidencePrefix+secret.SHA256 &&
		secret.OwnerType == legacyTrialEvidenceOwner && secret.OwnerSourceKey == "s1:promo-v1:"+secret.SHA256 &&
		secret.Field == "source_json" && secret.Kind == "legacy-promo-v1" && secret.KeyVersion > 0
}

func openTrialEvidence(box *controlplane.SecretBox, secret LegacyEncryptedSecret) ([]byte, error) {
	if box == nil || !validTrialEvidenceMetadata(secret) {
		return nil, errInvalidSnapshotProtection
	}
	nonce, nOK := decodeCanonicalBase64(secret.NonceB64)
	cipher, cOK := decodeCanonicalBase64(secret.CiphertextB64)
	if !nOK || !cOK {
		return nil, errInvalidSnapshotProtection
	}
	raw, err := box.Open(controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
	if err != nil || sha256Hex(raw) != secret.SHA256 {
		zeroBytes(raw)
		return nil, errInvalidSnapshotProtection
	}
	return raw, nil
}

func hasConvertedTrialSource(hashes map[string]string) bool {
	_, ok := hashes[legacyTrialConvertedSource]
	return ok
}

func validateTrialSourceShape(hashes map[string]string, secrets []LegacyEncryptedSecret) bool {
	current, converted := hashes[legacyTrialConvertedSource]
	_, absent := hashes["legacy:trials:absent"]
	_, unconverted := hashes["legacy:trials:present-unconverted"]
	found := false
	for _, secret := range secrets {
		if !reservedTrialEvidence(secret) {
			continue
		}
		if !converted || !validTrialEvidenceMetadata(secret) {
			return false
		}
		if secret.SHA256 == current {
			found = true
		}
	}
	return !converted || (validCanonicalSHA256(current) && !absent && !unconverted && found)
}

func validateNativeTrialProof(protection SnapshotProtection, box *controlplane.SecretBox) (*legacyTrialProof, error) {
	if !validateTrialSourceShape(protection.SourceHashes, protection.EncryptedSecrets) {
		return nil, errInvalidSnapshotProtection
	}
	if !hasConvertedTrialSource(protection.SourceHashes) {
		return nil, nil
	}
	if !validCanonicalSHA256(protection.SourceDigest) || !validCanonicalSHA256(protection.LegacyTrialSaltSHA256) || (protection.SnapshotKind != "full" && protection.SnapshotKind != "delta") {
		return nil, errInvalidSnapshotProtection
	}
	proof := &legacyTrialProof{sourceDigest: protection.SourceDigest, snapshotKind: protection.SnapshotKind, parentDigest: protection.ParentSourceDigest, evidence: map[string]LegacyEncryptedSecret{}, trials: map[string]LegacyTrial{}}
	ledgers := map[string]nativeTrialLedger{}
	for _, secret := range protection.EncryptedSecrets {
		if !reservedTrialEvidence(secret) {
			continue
		}
		if _, exists := proof.evidence[secret.SecretID]; exists {
			return nil, errInvalidSnapshotProtection
		}
		raw, err := openTrialEvidence(box, secret)
		if err != nil {
			return nil, errInvalidSnapshotProtection
		}
		ledger, err := decodeNativeTrialLedger(raw)
		zeroBytes(raw)
		if err != nil {
			return nil, errInvalidSnapshotProtection
		}
		proof.evidence[secret.SecretID], ledgers[secret.SHA256] = secret, ledger
	}
	current := ledgers[protection.SourceHashes[legacyTrialConvertedSource]]
	for _, row := range nativeTrialRows(current, protection.LegacyTrialSaltSHA256) {
		proof.trials[row.SourceKey] = row
	}
	if len(proof.trials) != len(protection.Trials) {
		return nil, errInvalidSnapshotProtection
	}
	seenTrials := map[string]bool{}
	for _, row := range protection.Trials {
		if expected, ok := proof.trials[row.SourceKey]; !ok || row != expected || seenTrials[row.SourceKey] {
			return nil, errInvalidSnapshotProtection
		}
		seenTrials[row.SourceKey] = true
	}
	// Every retained source is a predecessor, never a disconnected substitute.
	for _, ledger := range ledgers {
		if !trialLedgerContains(current, ledger) {
			return nil, errInvalidSnapshotProtection
		}
	}
	if protection.SnapshotKind == "delta" {
		if protection.Parent == nil || protection.Parent.SnapshotKind != "full" || protection.Parent.SourceDigest != protection.ParentSourceDigest || protection.Parent.ClusterHMACKeySHA256 != protection.ClusterHMACKeySHA256 || protection.Parent.LegacyTrialSaltSHA256 != protection.LegacyTrialSaltSHA256 || !protection.CapturedAt.After(protection.Parent.CapturedAt) {
			return nil, errInvalidSnapshotProtection
		}
		parentProof, err := validateNativeTrialProof(*protection.Parent, box)
		if err != nil || parentProof == nil {
			return nil, errInvalidSnapshotProtection
		}
		for id, secret := range parentProof.evidence {
			if retained, ok := proof.evidence[id]; !ok || retained != secret {
				return nil, errInvalidSnapshotProtection
			}
		}
	} else if protection.Parent != nil || protection.ParentSourceDigest != "" {
		return nil, errInvalidSnapshotProtection
	}
	return proof, nil
}

func normalizeLegacyTrialSource(snapshot *Snapshot, input *LegacyTrialSource, box *controlplane.SecretBox, parent *Snapshot) error {
	if input == nil {
		if parent != nil && hasConvertedTrialSource(parent.SourceHashes) {
			return ErrLegacyNormalize
		}
		return nil
	}
	if len(input.Salt) == 0 || len(input.Salt) > 1<<20 || snapshot.SourceHashes["legacy:trials:present-unconverted"] != sha256Hex(input.RawJSON) {
		return ErrLegacyNormalize
	}
	ledger, err := decodeNativeTrialLedger(input.RawJSON)
	if err != nil {
		return ErrLegacyNormalize
	}
	saltSHA := sha256Hex(input.Salt)
	rawSHA := sha256Hex(input.RawJSON)
	evidence := map[string]LegacyEncryptedSecret{}
	if parent != nil {
		if !hasConvertedTrialSource(parent.SourceHashes) || parent.LegacyTrialSaltSHA256 != saltSHA {
			return ErrLegacyNormalize
		}
		for _, secret := range parent.EncryptedSecrets {
			if !reservedTrialEvidence(secret) {
				continue
			}
			raw, err := openTrialEvidence(box, secret)
			if err != nil {
				return ErrLegacyNormalize
			}
			prior, err := decodeNativeTrialLedger(raw)
			zeroBytes(raw)
			if err != nil || !trialLedgerContains(ledger, prior) {
				return ErrLegacyNormalize
			}
			evidence[secret.SecretID] = secret
		}
	}
	id := legacyTrialEvidencePrefix + rawSHA
	if _, exists := evidence[id]; !exists {
		scope := controlplane.SecretScope{OwnerType: legacyTrialEvidenceOwner, OwnerID: "s1:promo-v1:" + rawSHA, Field: "source_json", Kind: "legacy-promo-v1"}
		envelope, err := box.Seal(scope, input.RawJSON)
		if err != nil {
			return ErrLegacyNormalize
		}
		evidence[id] = LegacyEncryptedSecret{SecretID: id, OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: rawSHA}
	}
	delete(snapshot.SourceHashes, "legacy:trials:present-unconverted")
	snapshot.SourceHashes[legacyTrialConvertedSource] = rawSHA
	snapshot.LegacyTrialSaltSHA256 = saltSHA
	snapshot.Trials = nativeTrialRows(ledger, saltSHA)
	for _, secret := range evidence {
		snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, secret)
	}
	return nil
}

func trialSourceSalt(input *LegacyTrialSource) []byte {
	if input == nil {
		return nil
	}
	return input.Salt
}
