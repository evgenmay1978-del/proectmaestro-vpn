package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"sort"
	"strings"
)

const lifecycleJournalSchemaVersion = 2

type LifecycleSigner struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

type LifecycleTrust map[string]ed25519.PublicKey

type lifecycleJournal struct {
	Revision uint64                  `json:"revision"`
	Active   []lifecycleActiveEntry  `json:"active"`
	Entries  []lifecycleJournalEntry `json:"entries"`
}

type lifecycleActiveEntry struct {
	TransportProfileID string `json:"transport_profile_id"`
	ReleaseID          string `json:"release_id"`
}

type lifecycleJournalEntry struct {
	ReleaseID            string `json:"release_id"`
	ManifestSHA256       string `json:"manifest_sha256"`
	TransportProfileID   string `json:"transport_profile_id"`
	Generation           uint64 `json:"generation"`
	State                State  `json:"state"`
	WasPublished         bool   `json:"was_published"`
	PredecessorReleaseID string `json:"predecessor_release_id"`
}

type lifecycleSignaturePayload struct {
	SchemaVersion int              `json:"schema_version"`
	KeyID         string           `json:"key_id"`
	Journal       lifecycleJournal `json:"journal"`
}

type signedLifecycleJournal struct {
	SchemaVersion int              `json:"schema_version"`
	KeyID         string           `json:"key_id"`
	Journal       lifecycleJournal `json:"journal"`
	Signature     string           `json:"signature"`
}

func (c Catalog) Snapshot(signer LifecycleSigner) ([]byte, error) {
	if err := validateCatalog(c); err != nil {
		return nil, err
	}
	if err := validateLifecycleSigner(signer); err != nil {
		return nil, err
	}
	journal := lifecycleJournal{
		Revision: c.revision,
		Active:   catalogActiveEntries(c),
		Entries:  make([]lifecycleJournalEntry, 0, len(c.releases)),
	}
	ids := make([]string, 0, len(c.releases))
	for id := range c.releases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		value := c.releases[id]
		predecessorID, wasPublished := c.predecessors[id]
		journal.Entries = append(journal.Entries, lifecycleJournalEntry{
			ReleaseID: id, ManifestSHA256: value.manifestSHA256,
			TransportProfileID: value.manifest.TransportProfileID, Generation: value.manifest.Generation,
			State: value.state, WasPublished: wasPublished, PredecessorReleaseID: predecessorID,
		})
	}
	payload := lifecycleSignaturePayload{SchemaVersion: lifecycleJournalSchemaVersion, KeyID: signer.KeyID, Journal: journal}
	canonicalPayload, err := marshalCanonical(payload)
	if err != nil {
		return nil, invalid("journal_encode")
	}
	signed := signedLifecycleJournal{
		SchemaVersion: lifecycleJournalSchemaVersion,
		KeyID:         signer.KeyID,
		Journal:       journal,
		Signature:     hex.EncodeToString(ed25519.Sign(signer.PrivateKey, canonicalPayload)),
	}
	canonical, err := marshalCanonical(signed)
	if err != nil || len(canonical) > maxManifestBytes {
		return nil, invalid("journal_encode")
	}
	return canonical, nil
}

func (c Catalog) Restore(raw []byte, releases []Release, trusted LifecycleTrust, minimumRevision uint64) (Catalog, error) {
	if minimumRevision == 0 || len(raw) == 0 || len(raw) > maxManifestBytes {
		return Catalog{}, invalid("journal_policy_invalid")
	}
	var signed signedLifecycleJournal
	if err := decodeCanonicalJSON(raw, &signed); err != nil || signed.SchemaVersion != lifecycleJournalSchemaVersion || !validID(signed.KeyID) {
		return Catalog{}, invalid("journal_invalid")
	}
	if signed.Journal.Active == nil || signed.Journal.Entries == nil {
		return Catalog{}, invalid("journal_shape_invalid")
	}
	publicKey, trustedKey := trusted[signed.KeyID]
	if !trustedKey || len(publicKey) != ed25519.PublicKeySize {
		return Catalog{}, invalid("journal_key_untrusted")
	}
	signature, err := hex.DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || signed.Signature != strings.ToLower(signed.Signature) {
		return Catalog{}, invalid("journal_signature_invalid")
	}
	payload := lifecycleSignaturePayload{SchemaVersion: signed.SchemaVersion, KeyID: signed.KeyID, Journal: signed.Journal}
	canonicalPayload, err := marshalCanonical(payload)
	if err != nil || !ed25519.Verify(publicKey, canonicalPayload, signature) {
		return Catalog{}, invalid("journal_signature_invalid")
	}
	if signed.Journal.Revision < minimumRevision || signed.Journal.Revision == 0 {
		return Catalog{}, invalid("journal_revision_stale")
	}
	available, err := validateRestoreReleases(releases)
	if err != nil {
		return Catalog{}, err
	}
	restored := Catalog{
		releases:     make(map[string]Release, len(signed.Journal.Entries)),
		predecessors: make(map[string]string),
		revision:     signed.Journal.Revision,
	}
	lastID := ""
	pairs := make(map[string]struct{}, len(signed.Journal.Entries))
	digests := make(map[string]struct{}, len(signed.Journal.Entries))
	for _, entry := range signed.Journal.Entries {
		pair := entry.TransportProfileID + "\x00" + generationKey(entry.Generation)
		if entry.ReleaseID <= lastID || !validID(entry.ReleaseID) || !validID(entry.TransportProfileID) ||
			entry.Generation == 0 || !validSHA256(entry.ManifestSHA256) || (!entry.WasPublished && entry.PredecessorReleaseID != "") {
			return Catalog{}, invalid("journal_entry_invalid")
		}
		if _, exists := pairs[pair]; exists {
			return Catalog{}, invalid("journal_generation_duplicate")
		}
		pairs[pair] = struct{}{}
		if _, exists := digests[entry.ManifestSHA256]; exists {
			return Catalog{}, invalid("journal_manifest_duplicate")
		}
		digests[entry.ManifestSHA256] = struct{}{}
		base, exists := available[entry.ReleaseID]
		if !exists || !equalDigest(base.manifestSHA256, entry.ManifestSHA256) ||
			base.manifest.TransportProfileID != entry.TransportProfileID || base.manifest.Generation != entry.Generation {
			return Catalog{}, invalid("journal_binding_mismatch")
		}
		stateful, err := base.setState(entry.State)
		if err != nil {
			return Catalog{}, err
		}
		restored.releases[entry.ReleaseID] = stateful
		if entry.WasPublished {
			restored.predecessors[entry.ReleaseID] = entry.PredecessorReleaseID
		}
		lastID = entry.ReleaseID
	}
	if len(restored.releases) != len(available) {
		return Catalog{}, invalid("journal_release_set_mismatch")
	}
	if err := validateCatalog(restored); err != nil {
		return Catalog{}, err
	}
	if !equalActiveEntries(signed.Journal.Active, catalogActiveEntries(restored)) {
		return Catalog{}, invalid("journal_active_mismatch")
	}
	return restored.clone(), nil
}

func validateLifecycleSigner(signer LifecycleSigner) error {
	if !validID(signer.KeyID) || len(signer.PrivateKey) != ed25519.PrivateKeySize {
		return invalid("journal_signer_invalid")
	}
	derived := ed25519.NewKeyFromSeed(signer.PrivateKey.Seed())
	if !bytes.Equal(derived, signer.PrivateKey) {
		return invalid("journal_signer_invalid")
	}
	return nil
}

func validateRestoreReleases(releases []Release) (map[string]Release, error) {
	available := make(map[string]Release, len(releases))
	pairs := make(map[string]struct{}, len(releases))
	digests := make(map[string]struct{}, len(releases))
	for _, value := range releases {
		id := value.manifest.ReleaseID
		if !validID(id) || validateManifest(value.manifest) != nil ||
			value.transport.ID() != id || value.transport.TransportProfileID() != value.manifest.TransportProfileID ||
			value.transport.CompatibilityPresetID() != value.manifest.CompatibilityPresetID {
			return nil, invalid("restore_release_invalid")
		}
		canonical, err := marshalCanonical(value.manifest)
		if err != nil || !bytes.Equal(canonical, value.canonical) || !equalDigest(digestBytes(canonical), value.manifestSHA256) {
			return nil, invalid("restore_release_binding_invalid")
		}
		if _, exists := available[id]; exists {
			return nil, invalid("restore_release_duplicate")
		}
		pair := value.manifest.TransportProfileID + "\x00" + generationKey(value.manifest.Generation)
		if _, exists := pairs[pair]; exists {
			return nil, invalid("restore_generation_duplicate")
		}
		pairs[pair] = struct{}{}
		if _, exists := digests[value.manifestSHA256]; exists {
			return nil, invalid("restore_manifest_duplicate")
		}
		digests[value.manifestSHA256] = struct{}{}
		available[id] = cloneRelease(value)
	}
	return available, nil
}

func catalogActiveEntries(c Catalog) []lifecycleActiveEntry {
	active := make([]lifecycleActiveEntry, 0)
	for id, value := range c.releases {
		if value.state == Published {
			active = append(active, lifecycleActiveEntry{TransportProfileID: value.manifest.TransportProfileID, ReleaseID: id})
		}
	}
	sort.Slice(active, func(left, right int) bool {
		return active[left].TransportProfileID < active[right].TransportProfileID
	})
	return active
}

func equalActiveEntries(actual, expected []lifecycleActiveEntry) bool {
	if len(actual) != len(expected) {
		return false
	}
	lastProfile := ""
	for index := range actual {
		if !validID(actual[index].TransportProfileID) || !validID(actual[index].ReleaseID) ||
			actual[index].TransportProfileID <= lastProfile || actual[index] != expected[index] {
			return false
		}
		lastProfile = actual[index].TransportProfileID
	}
	return true
}
