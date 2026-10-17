package release_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

type lifecycleJournalFixture struct {
	Revision uint64                   `json:"revision"`
	Active   []lifecycleActiveFixture `json:"active"`
	Entries  []lifecycleEntryFixture  `json:"entries"`
}

type lifecycleActiveFixture struct {
	TransportProfileID string `json:"transport_profile_id"`
	ReleaseID          string `json:"release_id"`
}

type lifecycleEntryFixture struct {
	ReleaseID            string        `json:"release_id"`
	ManifestSHA256       string        `json:"manifest_sha256"`
	TransportProfileID   string        `json:"transport_profile_id"`
	Generation           uint64        `json:"generation"`
	State                release.State `json:"state"`
	WasPublished         bool          `json:"was_published"`
	PredecessorReleaseID string        `json:"predecessor_release_id"`
}

type signedJournalFixture struct {
	SchemaVersion int                     `json:"schema_version"`
	KeyID         string                  `json:"key_id"`
	Journal       lifecycleJournalFixture `json:"journal"`
	Signature     string                  `json:"signature"`
}

type signaturePayloadFixture struct {
	SchemaVersion int                     `json:"schema_version"`
	KeyID         string                  `json:"key_id"`
	Journal       lifecycleJournalFixture `json:"journal"`
}

func lifecycleCredentials(t *testing.T) (release.LifecycleSigner, release.LifecycleTrust, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte("task-b-lifecycle-signer"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	return release.LifecycleSigner{KeyID: "lifecycle-main", PrivateKey: privateKey},
		release.LifecycleTrust{"lifecycle-main": publicKey}, privateKey
}

func candidateWithoutApprovedEdges(t *testing.T, id, profileID string, generation uint64) release.Release {
	t.Helper()
	spec, _, privateKey := taskASpec(t, id)
	spec.Generation = generation
	base := transportForProfile(t, id, profileID)
	transport, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID: id, Profile: base.Profile(), Preset: base.Preset(),
		State: controlplane.TransportReleaseCandidate,
	})
	if err != nil {
		t.Fatalf("NewTransportRelease without approved edges: %v", err)
	}
	spec.Transport = transport
	evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
	if err != nil {
		t.Fatalf("BuildValidationEvidence without approved edges: %v", err)
	}
	spec.ValidationEvidence = evidence
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate without approved edges: %v", err)
	}
	return candidate
}

func mustAddCandidate(t *testing.T, catalog release.Catalog, candidate release.Release) release.Catalog {
	t.Helper()
	result, err := catalog.AddCandidate(candidate)
	if err != nil {
		t.Fatalf("AddCandidate(%s): %v", candidate.Manifest().ReleaseID, err)
	}
	return result
}

func mustPublish(t *testing.T, catalog release.Catalog, releaseID string) release.Catalog {
	t.Helper()
	result, err := catalog.Publish(releaseID)
	if err != nil {
		t.Fatalf("Publish(%s): %v", releaseID, err)
	}
	return result
}

func exactHistoryCatalog(t *testing.T) (release.Catalog, []release.Release) {
	t.Helper()
	releases := []release.Release{
		mustCandidate(t, "history-r1", 1),
		mustCandidate(t, "history-r2", 2),
		mustCandidate(t, "history-r3", 3),
		mustCandidate(t, "history-r4", 4),
	}
	catalog := release.NewCatalog()
	for index := 0; index < 3; index++ {
		catalog = mustAddCandidate(t, catalog, releases[index])
		catalog = mustPublish(t, catalog, releases[index].Manifest().ReleaseID)
	}
	var err error
	catalog, selected, err := catalog.Rollback("history-r3")
	if err != nil || selected != "history-r2" {
		t.Fatalf("Rollback(history-r3) selected=%q err=%v", selected, err)
	}
	catalog = mustAddCandidate(t, catalog, releases[3])
	catalog = mustPublish(t, catalog, "history-r4")
	return catalog, releases
}

func TestCatalogRollbackUsesExactPublicationPredecessorBeforeAndAfterRestore(t *testing.T) {
	catalog, releases := exactHistoryCatalog(t)
	rolled, selected, err := catalog.Rollback("history-r4")
	if err != nil {
		t.Fatalf("Rollback(history-r4): %v", err)
	}
	current, ok := rolled.CurrentForProfile("profile-main")
	if !ok || selected != "history-r2" || current.Manifest().ReleaseID != "history-r2" {
		t.Fatalf("exact rollback selected=%q current=%q exists=%t", selected, current.Manifest().ReleaseID, ok)
	}

	signer, trust, _ := lifecycleCredentials(t)
	snapshot, err := catalog.Snapshot(signer)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	restored, err := release.NewCatalog().Restore(snapshot, releases, trust, catalog.Revision())
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Revision() != catalog.Revision() {
		t.Fatalf("restored revision=%d want=%d", restored.Revision(), catalog.Revision())
	}
	restored, selected, err = restored.Rollback("history-r4")
	if err != nil {
		t.Fatalf("restored Rollback(history-r4): %v", err)
	}
	current, ok = restored.CurrentForProfile("profile-main")
	if !ok || selected != "history-r2" || current.Manifest().ReleaseID != "history-r2" {
		t.Fatalf("restored exact rollback selected=%q current=%q exists=%t", selected, current.Manifest().ReleaseID, ok)
	}
}

func TestCatalogCurrentIsFalseWhenPublishedProfilesAreAmbiguous(t *testing.T) {
	a := candidateForProfile(t, "current-a", "profile-a", 1)
	b := candidateForProfile(t, "current-b", "profile-b", 1)
	catalog := mustPublish(t, mustAddCandidate(t, release.NewCatalog(), a), "current-a")
	catalog = mustPublish(t, mustAddCandidate(t, catalog, b), "current-b")
	if _, ok := catalog.Current(); ok {
		t.Fatal("global Current selected lexically from multiple published profiles")
	}
	for profileID, releaseID := range map[string]string{"profile-a": "current-a", "profile-b": "current-b"} {
		current, ok := catalog.CurrentForProfile(profileID)
		if !ok || current.Manifest().ReleaseID != releaseID {
			t.Fatalf("CurrentForProfile(%s)=%q exists=%t", profileID, current.Manifest().ReleaseID, ok)
		}
	}
}

func TestCatalogSnapshotIsCanonicalAuthenticatedAndDeterministic(t *testing.T) {
	catalog, _ := exactHistoryCatalog(t)
	signer, _, _ := lifecycleCredentials(t)
	first, err := catalog.Snapshot(signer)
	if err != nil {
		t.Fatalf("Snapshot first: %v", err)
	}
	second, err := catalog.Snapshot(signer)
	if err != nil {
		t.Fatalf("Snapshot second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical catalog and signer produced different signed journal bytes")
	}
	var journal signedJournalFixture
	if err := json.Unmarshal(first, &journal); err != nil {
		t.Fatalf("Unmarshal snapshot: %v", err)
	}
	if journal.SchemaVersion != 2 || journal.Journal.Revision != catalog.Revision() || len(journal.Signature) != ed25519.SignatureSize*2 {
		t.Fatalf("signed journal header=%#v", journal)
	}
	if _, err := catalog.Snapshot(release.LifecycleSigner{}); err == nil {
		t.Fatal("snapshot accepted an absent lifecycle signer")
	}
}

func TestCatalogRestoreRejectsNullJournalCollections(t *testing.T) {
	signer, trust, privateKey := lifecycleCredentials(t)
	snapshot, err := release.NewCatalog().Snapshot(signer)
	if err != nil {
		t.Fatalf("Snapshot empty catalog: %v", err)
	}
	for name, mutate := range map[string]func(*signedJournalFixture){
		"null active":  func(value *signedJournalFixture) { value.Journal.Active = nil },
		"null entries": func(value *signedJournalFixture) { value.Journal.Entries = nil },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := resignJournal(t, snapshot, privateKey, mutate)
			if _, err := release.NewCatalog().Restore(tampered, nil, trust, 1); err == nil {
				t.Fatal("signed journal with null collection accepted")
			}
		})
	}
}

func restoreFixture(t *testing.T) (release.Catalog, []release.Release, []byte, release.LifecycleTrust, ed25519.PrivateKey) {
	t.Helper()
	releases := []release.Release{
		candidateForProfile(t, "restore-r1", "profile-main", 1),
		candidateForProfile(t, "restore-r2", "profile-main", 2),
		candidateForProfile(t, "restore-r3", "profile-main", 3),
		candidateForProfile(t, "restore-b1", "profile-b", 1),
	}
	catalog := release.NewCatalog()
	for index := 0; index < 3; index++ {
		catalog = mustAddCandidate(t, catalog, releases[index])
		catalog = mustPublish(t, catalog, releases[index].Manifest().ReleaseID)
	}
	catalog = mustAddCandidate(t, catalog, releases[3])
	catalog = mustPublish(t, catalog, "restore-b1")
	signer, trust, privateKey := lifecycleCredentials(t)
	snapshot, err := catalog.Snapshot(signer)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return catalog, releases, snapshot, trust, privateKey
}

func resignJournal(t *testing.T, raw []byte, privateKey ed25519.PrivateKey, mutate func(*signedJournalFixture)) []byte {
	t.Helper()
	var signed signedJournalFixture
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatalf("Unmarshal signed journal: %v", err)
	}
	mutate(&signed)
	payload, err := json.Marshal(signaturePayloadFixture{
		SchemaVersion: signed.SchemaVersion,
		KeyID:         signed.KeyID,
		Journal:       signed.Journal,
	})
	if err != nil {
		t.Fatalf("Marshal signature payload: %v", err)
	}
	signed.Signature = hex.EncodeToString(ed25519.Sign(privateKey, payload))
	result, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("Marshal signed journal: %v", err)
	}
	return result
}

func entryByID(t *testing.T, signed *signedJournalFixture, releaseID string) *lifecycleEntryFixture {
	t.Helper()
	for index := range signed.Journal.Entries {
		if signed.Journal.Entries[index].ReleaseID == releaseID {
			return &signed.Journal.Entries[index]
		}
	}
	t.Fatalf("journal entry %s missing", releaseID)
	return nil
}

func TestCatalogRestoreRejectsAdversarialJournalAndGraphState(t *testing.T) {
	catalog, releases, snapshot, trust, privateKey := restoreFixture(t)
	valid, err := release.NewCatalog().Restore(snapshot, releases, trust, catalog.Revision())
	if err != nil {
		t.Fatalf("valid Restore: %v", err)
	}
	if current, ok := valid.CurrentForProfile("profile-main"); !ok || current.Manifest().ReleaseID != "restore-r3" {
		t.Fatalf("valid restored current=%q exists=%t", current.Manifest().ReleaseID, ok)
	}

	cases := map[string]func(*signedJournalFixture){
		"duplicate journal release": func(value *signedJournalFixture) {
			value.Journal.Entries = append(value.Journal.Entries, value.Journal.Entries[0])
		},
		"missing journal release": func(value *signedJournalFixture) {
			value.Journal.Entries = value.Journal.Entries[:len(value.Journal.Entries)-1]
		},
		"duplicate profile generation": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r2").Generation = entryByID(t, value, "restore-r1").Generation
		},
		"duplicate manifest digest": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r2").ManifestSHA256 = entryByID(t, value, "restore-r1").ManifestSHA256
		},
		"impossible revision": func(value *signedJournalFixture) {
			value.Journal.Revision = 1
		},
		"multiple published in profile": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r2").State = release.Published
		},
		"active map mismatch": func(value *signedJournalFixture) {
			for index := range value.Journal.Active {
				if value.Journal.Active[index].TransportProfileID == "profile-main" {
					value.Journal.Active[index].ReleaseID = "restore-r2"
				}
			}
		},
		"duplicate active profile": func(value *signedJournalFixture) {
			value.Journal.Active = append(value.Journal.Active, value.Journal.Active[0])
		},
		"missing active profile": func(value *signedJournalFixture) {
			value.Journal.Active = value.Journal.Active[1:]
		},
		"predecessor missing": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").PredecessorReleaseID = "missing-release"
		},
		"non-root predecessor omitted": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").PredecessorReleaseID = ""
		},
		"published history without active": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").State = release.Retired
			for index := range value.Journal.Active {
				if value.Journal.Active[index].TransportProfileID == "profile-main" {
					value.Journal.Active = append(value.Journal.Active[:index], value.Journal.Active[index+1:]...)
					break
				}
			}
		},
		"predecessor same release": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").PredecessorReleaseID = "restore-r3"
		},
		"predecessor cross profile": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-b1").PredecessorReleaseID = "restore-r1"
		},
		"predecessor not lower generation": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r2").PredecessorReleaseID = "restore-r3"
		},
		"predecessor cycle": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r1").PredecessorReleaseID = "restore-r2"
			entryByID(t, value, "restore-r2").PredecessorReleaseID = "restore-r1"
		},
		"release manifest binding": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").ManifestSHA256 = strings.Repeat("f", 64)
		},
		"release profile binding": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").TransportProfileID = "profile-b"
		},
		"release generation binding": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").Generation = 30
		},
		"invalid lifecycle state": func(value *signedJournalFixture) {
			entryByID(t, value, "restore-r3").State = release.State("BROKEN")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tampered := resignJournal(t, snapshot, privateKey, mutate)
			if _, err := release.NewCatalog().Restore(tampered, releases, trust, 1); err == nil {
				t.Fatal("adversarial signed journal accepted")
			}
		})
	}
}

func TestCatalogRestoreRejectsDuplicateInputsReplayAndSignatureFailures(t *testing.T) {
	catalog, releases, snapshot, trust, privateKey := restoreFixture(t)
	duplicateID := append(append([]release.Release(nil), releases...), releases[0])
	duplicateGeneration := append(append([]release.Release(nil), releases...), candidateForProfile(t, "restore-r1-copy", "profile-main", 1))
	for name, inputs := range map[string][]release.Release{
		"duplicate input release id":         duplicateID,
		"duplicate input profile generation": duplicateGeneration,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := release.NewCatalog().Restore(snapshot, inputs, trust, 1); err == nil {
				t.Fatal("duplicate restore input accepted")
			}
		})
	}
	unpublishable := candidateWithoutApprovedEdges(t, "restore-r1", "profile-main", 1)
	unpublishableInputs := append([]release.Release(nil), releases...)
	unpublishableInputs[0] = unpublishable
	unpublishableJournal := resignJournal(t, snapshot, privateKey, func(value *signedJournalFixture) {
		entryByID(t, value, "restore-r1").ManifestSHA256 = unpublishable.ManifestSHA256()
	})
	if _, err := release.NewCatalog().Restore(unpublishableJournal, unpublishableInputs, trust, 1); err == nil {
		t.Fatal("retired release that was never publishable accepted")
	}
	if _, err := release.NewCatalog().Restore(snapshot, releases, trust, 0); err == nil {
		t.Fatal("zero minimum revision policy accepted")
	}
	if _, err := release.NewCatalog().Restore(snapshot, releases, trust, catalog.Revision()+1); err == nil {
		t.Fatal("stale/replayed revision accepted")
	}
	unknownKey := resignJournal(t, snapshot, privateKey, func(value *signedJournalFixture) {
		value.KeyID = "unknown-lifecycle-key"
	})
	if _, err := release.NewCatalog().Restore(unknownKey, releases, trust, 1); err == nil {
		t.Fatal("unknown lifecycle key accepted")
	}
	var signed signedJournalFixture
	if err := json.Unmarshal(snapshot, &signed); err != nil {
		t.Fatalf("Unmarshal snapshot: %v", err)
	}
	bareJournal, err := json.Marshal(signed.Journal)
	if err != nil {
		t.Fatalf("Marshal bare journal: %v", err)
	}
	var malformedSigned signedJournalFixture
	if err := json.Unmarshal(snapshot, &malformedSigned); err != nil {
		t.Fatalf("Unmarshal malformed signature fixture: %v", err)
	}
	malformedSigned.Signature = "zz"
	malformed, err := json.Marshal(malformedSigned)
	if err != nil {
		t.Fatalf("Marshal malformed signature fixture: %v", err)
	}
	wrong := resignJournal(t, snapshot, privateKey, func(value *signedJournalFixture) {})
	var wrongSigned signedJournalFixture
	if err := json.Unmarshal(wrong, &wrongSigned); err != nil {
		t.Fatalf("Unmarshal wrong signature fixture: %v", err)
	}
	wrongSigned.Signature = strings.Repeat("0", ed25519.SignatureSize*2)
	wrong, err = json.Marshal(wrongSigned)
	if err != nil {
		t.Fatalf("Marshal wrong signature fixture: %v", err)
	}
	for name, raw := range map[string][]byte{
		"unsigned raw journal": bareJournal,
		"malformed signature":  malformed,
		"wrong signature":      wrong,
		"trailing JSON":        append(append([]byte(nil), snapshot...), '\n'),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := release.NewCatalog().Restore(raw, releases, trust, 1); err == nil {
				t.Fatal("unauthenticated or noncanonical journal accepted")
			}
		})
	}
}
