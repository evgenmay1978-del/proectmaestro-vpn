//go:build linux

package release_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func taskETrust(t *testing.T, seedByte byte) (release.EvidenceTrust, ed25519.PrivateKey) {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seedByte}, ed25519.SeedSize))
	trust, err := release.NewEvidenceTrust([]release.EvidenceTrustKey{{
		KeyID:        "task-a-test-key",
		PublicKey:    privateKey.Public().(ed25519.PublicKey),
		SourceOrigin: "https://evidence.example.invalid",
	}})
	if err != nil {
		t.Fatalf("NewEvidenceTrust: %v", err)
	}
	return trust, privateKey
}

func taskECandidate(t *testing.T, id string, generation uint64, seedByte byte) (release.CandidateSpec, release.Release, release.EvidenceTrust) {
	t.Helper()
	spec, _, _ := taskASpec(t, id)
	trust, privateKey := taskETrust(t, seedByte)
	spec.Generation = generation
	spec.EvidenceTrust = trust
	evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
	if err != nil {
		t.Fatalf("BuildValidationEvidence: %v", err)
	}
	spec.ValidationEvidence = evidence
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate(%s): %v", id, err)
	}
	return spec, candidate, trust
}

func taskERotatedConfig(root string, active release.EvidenceTrust, historical ...release.EvidenceTrust) release.ActivationStoreConfig {
	config := taskCConfig(root, active, nil)
	config.HistoricalEvidenceTrusts = historical
	return config
}

func TestTaskEActivationTrustRotationPublishesRollsBackAndRecovers(t *testing.T) {
	root := taskCRoot(t)
	oldSpec, oldCandidate, oldTrust := taskECandidate(t, "release-e-old", 1, 0x61)
	oldStore, err := release.NewActivationStore(taskCConfig(root, oldTrust, nil))
	if err != nil {
		t.Fatalf("NewActivationStore(old): %v", err)
	}
	oldStaging := taskCWriteSealed(t, root, "staging-e-old", oldSpec, oldCandidate)
	publishedOld, err := oldStore.Publish(release.NewCatalog(), oldCandidate, oldStaging)
	if err != nil {
		t.Fatalf("Publish(old): %v", err)
	}

	newSpec, newCandidate, newTrust := taskECandidate(t, "release-e-new", 2, 0x62)
	rotatedStore, err := release.NewActivationStore(taskERotatedConfig(root, newTrust, oldTrust))
	if err != nil {
		t.Fatalf("NewActivationStore(rotated): %v", err)
	}
	newStaging := taskCWriteSealed(t, root, "staging-e-new", newSpec, newCandidate)
	publishedNew, err := rotatedStore.Publish(publishedOld, newCandidate, newStaging)
	if err != nil {
		t.Fatalf("Publish(new after rotation): %v", err)
	}

	historicalSpec, historicalCandidate, _ := taskECandidate(t, "release-e-historical-new", 3, 0x61)
	historicalStaging := taskCWriteSealed(t, root, "staging-e-historical-new", historicalSpec, historicalCandidate)
	if _, err := rotatedStore.Publish(publishedNew, historicalCandidate, historicalStaging); err == nil {
		t.Fatal("historical trust admitted a new candidate")
	} else {
		taskCRequireReason(t, err, "evidence_trust_mismatch")
	}

	rolledBack, selectedID, err := rotatedStore.Rollback(publishedNew, newCandidate.Manifest().ReleaseID)
	if err != nil {
		t.Fatalf("Rollback after trust rotation: %v", err)
	}
	if selectedID != oldCandidate.Manifest().ReleaseID {
		t.Fatalf("rollback selected %q, want %q", selectedID, oldCandidate.Manifest().ReleaseID)
	}
	available := []release.Release{oldCandidate, newCandidate}
	taskCAssertSteady(t, root, rolledBack, available)

	fresh, err := release.NewActivationStore(taskERotatedConfig(root, newTrust, oldTrust))
	if err != nil {
		t.Fatalf("NewActivationStore(fresh rotated): %v", err)
	}
	recovered, err := fresh.Recover(available)
	if err != nil {
		t.Fatalf("Recover historical current after rotation: %v", err)
	}
	taskCAssertSteady(t, root, recovered, available)
}

func TestTaskEActivationTrustRotationRecoversPendingTransactions(t *testing.T) {
	t.Run("publish admitted before rotation", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, oldTrust := taskECandidate(t, "release-e-pending-publish", 1, 0x63)
		config := taskCConfig(root, oldTrust, func(phase release.ActivationPhase) error {
			if phase == release.ActivationAfterIntent {
				return errors.New("stop after intent")
			}
			return nil
		})
		store, err := release.NewActivationStore(config)
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-e-pending-publish", spec, candidate)
		if _, err := store.Publish(release.NewCatalog(), candidate, staging); err == nil {
			t.Fatal("publish was not interrupted")
		} else {
			taskCRequireReason(t, err, "activation_interrupted")
		}
		newTrust, _ := taskETrust(t, 0x64)
		fresh, err := release.NewActivationStore(taskERotatedConfig(root, newTrust, oldTrust))
		if err != nil {
			t.Fatalf("NewActivationStore(rotated): %v", err)
		}
		recovered, err := fresh.Recover([]release.Release{candidate})
		if err != nil {
			t.Fatalf("Recover pending publish after rotation: %v", err)
		}
		taskCAssertSteady(t, root, recovered, []release.Release{candidate})
	})

	t.Run("rollback targets historical trust", func(t *testing.T) {
		root := taskCRoot(t)
		oldSpec, oldCandidate, oldTrust := taskECandidate(t, "release-e-pending-old", 1, 0x65)
		oldStore, err := release.NewActivationStore(taskCConfig(root, oldTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore(old): %v", err)
		}
		oldStaging := taskCWriteSealed(t, root, "staging-e-pending-old", oldSpec, oldCandidate)
		publishedOld, err := oldStore.Publish(release.NewCatalog(), oldCandidate, oldStaging)
		if err != nil {
			t.Fatalf("Publish(old): %v", err)
		}

		newSpec, newCandidate, newTrust := taskECandidate(t, "release-e-pending-new", 2, 0x66)
		rotatedStore, err := release.NewActivationStore(taskERotatedConfig(root, newTrust, oldTrust))
		if err != nil {
			t.Fatalf("NewActivationStore(rotated): %v", err)
		}
		newStaging := taskCWriteSealed(t, root, "staging-e-pending-new", newSpec, newCandidate)
		publishedNew, err := rotatedStore.Publish(publishedOld, newCandidate, newStaging)
		if err != nil {
			t.Fatalf("Publish(new): %v", err)
		}

		crashConfig := taskERotatedConfig(root, newTrust, oldTrust)
		crashConfig.PhaseHook = func(phase release.ActivationPhase) error {
			if phase == release.ActivationAfterIntent {
				return errors.New("stop after intent")
			}
			return nil
		}
		crashStore, err := release.NewActivationStore(crashConfig)
		if err != nil {
			t.Fatalf("NewActivationStore(crash): %v", err)
		}
		if _, _, err := crashStore.Rollback(publishedNew, newCandidate.Manifest().ReleaseID); err == nil {
			t.Fatal("rollback was not interrupted")
		} else {
			taskCRequireReason(t, err, "activation_interrupted")
		}

		fresh, err := release.NewActivationStore(taskERotatedConfig(root, newTrust, oldTrust))
		if err != nil {
			t.Fatalf("NewActivationStore(fresh): %v", err)
		}
		available := []release.Release{oldCandidate, newCandidate}
		recovered, err := fresh.Recover(available)
		if err != nil {
			t.Fatalf("Recover pending rollback after rotation: %v", err)
		}
		current, ok := recovered.CurrentForProfile("profile-main")
		if !ok || current.Manifest().ReleaseID != oldCandidate.Manifest().ReleaseID {
			t.Fatalf("recovered rollback current=%q ok=%v", current.Manifest().ReleaseID, ok)
		}
		taskCAssertSteady(t, root, recovered, available)
	})
}

func TestTaskEActivationTrustRegistryFailsClosedAndFreezesBundles(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		root := taskCRoot(t)
		trust, _ := taskETrust(t, 0x67)
		config := taskERotatedConfig(root, trust, trust)
		if _, err := release.NewActivationStore(config); err == nil {
			t.Fatal("duplicate active and historical trust accepted")
		} else {
			taskCRequireReason(t, err, "evidence_trust_registry_duplicate")
		}
		if _, err := os.Lstat(filepath.Join(root, "journal.json")); !os.IsNotExist(err) {
			t.Fatalf("invalid registry initialized store: %v", err)
		}
	})

	t.Run("missing historical and caller mutation", func(t *testing.T) {
		root := taskCRoot(t)
		oldSpec, oldCandidate, oldTrust := taskECandidate(t, "release-e-freeze-old", 1, 0x68)
		oldStore, err := release.NewActivationStore(taskCConfig(root, oldTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore(old): %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-e-freeze-old", oldSpec, oldCandidate)
		published, err := oldStore.Publish(release.NewCatalog(), oldCandidate, staging)
		if err != nil {
			t.Fatalf("Publish(old): %v", err)
		}
		journalBefore, err := os.ReadFile(filepath.Join(root, "journal.json"))
		if err != nil {
			t.Fatalf("read journal: %v", err)
		}
		currentBefore, err := os.Readlink(filepath.Join(root, "current"))
		if err != nil {
			t.Fatalf("read current: %v", err)
		}

		newTrust, _ := taskETrust(t, 0x69)
		missing, err := release.NewActivationStore(taskCConfig(root, newTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore(missing historical): %v", err)
		}
		transientPath := filepath.Join(root, "journal.new")
		if err := os.WriteFile(transientPath, nil, 0o600); err != nil {
			t.Fatalf("write valid transient: %v", err)
		}
		if _, err := missing.Recover([]release.Release{oldCandidate}); err == nil {
			t.Fatal("steady recovery accepted missing historical trust")
		} else {
			taskCRequireReason(t, err, "evidence_trust_mismatch")
		}
		journalAfter, _ := os.ReadFile(filepath.Join(root, "journal.json"))
		currentAfter, _ := os.Readlink(filepath.Join(root, "current"))
		if !bytes.Equal(journalBefore, journalAfter) || currentBefore != currentAfter {
			t.Fatal("missing historical trust mutated durable activation state")
		}
		if _, err := os.Lstat(transientPath); err != nil {
			t.Fatalf("missing historical trust removed recovery transient: %v", err)
		}

		config := taskERotatedConfig(root, newTrust, oldTrust)
		frozen, err := release.NewActivationStore(config)
		if err != nil {
			t.Fatalf("NewActivationStore(frozen): %v", err)
		}
		config.HistoricalEvidenceTrusts[0].Keys[0].PublicKey[0] ^= 0xff
		recovered, err := frozen.Recover([]release.Release{oldCandidate})
		if err != nil {
			t.Fatalf("caller mutation changed frozen registry: %v", err)
		}
		if recovered.Revision() != published.Revision() {
			t.Fatalf("recovered revision=%d want=%d", recovered.Revision(), published.Revision())
		}
	})
}
