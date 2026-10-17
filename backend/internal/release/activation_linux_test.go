//go:build linux

package release_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func taskCLifecycle() (release.LifecycleSigner, release.LifecycleTrust) {
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x52}, ed25519.SeedSize))
	return release.LifecycleSigner{KeyID: "task-c-lifecycle", PrivateKey: privateKey}, release.LifecycleTrust{
		"task-c-lifecycle": append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...),
	}
}

func taskCConfig(root string, evidenceTrust release.EvidenceTrust, hook func(release.ActivationPhase) error) release.ActivationStoreConfig {
	signer, trust := taskCLifecycle()
	return release.ActivationStoreConfig{
		Root: root, StoreID: "task-c-store", TransportProfileID: "profile-main",
		TrustedOwnerUID: uint32(os.Geteuid()), EvidenceTrust: evidenceTrust,
		LifecycleSigner: signer, LifecycleTrust: trust, MinimumRevision: 1,
		Now: func() time.Time { return time.Now().UTC() }, PhaseHook: hook,
	}
}

func taskCRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil {
				if info.IsDir() {
					_ = os.Chmod(path, 0o700)
				} else if info.Mode()&os.ModeSymlink == 0 {
					_ = os.Chmod(path, 0o600)
				}
			}
			return nil
		})
	})
	return root
}

func taskCCandidate(t *testing.T, id string, generation uint64) (release.CandidateSpec, release.Release, release.EvidenceTrust) {
	t.Helper()
	spec := candidateSpec(t, id, generation)
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate(%s): %v", id, err)
	}
	return spec, candidate, spec.EvidenceTrust
}

func taskCWriteSealed(t *testing.T, root, stagingID string, spec release.CandidateSpec, candidate release.Release) string {
	t.Helper()
	if !strings.HasPrefix(stagingID, "staging-") {
		t.Fatalf("unsafe test staging id %q", stagingID)
	}
	dir := filepath.Join(root, "releases", stagingID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), candidate.CanonicalManifest(), 0o400); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for name, data := range artifactBytes(spec) {
		mode := os.FileMode(0o400)
		if name == "xray" {
			mode = 0o500
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("seal staging: %v", err)
	}
	return dir
}

func taskCInventoryForCatalog(catalog release.Catalog, available []release.Release) []release.Release {
	selected := make([]release.Release, 0, len(available))
	for _, candidate := range available {
		if _, ok := catalog.Get(candidate.Manifest().ReleaseID); ok {
			selected = append(selected, candidate)
		}
	}
	return selected
}

func taskCAssertSteady(t *testing.T, root string, expected release.Catalog, available []release.Release) {
	t.Helper()
	journal, err := os.ReadFile(filepath.Join(root, "journal.json"))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	lifecycleSigner, lifecycleTrust := taskCLifecycle()
	expectedJournal, err := expected.Snapshot(lifecycleSigner)
	if err != nil {
		t.Fatalf("Snapshot expected journal: %v", err)
	}
	if !bytes.Equal(journal, expectedJournal) {
		t.Fatal("journal bytes do not match the exact expected catalog snapshot")
	}
	restored, err := release.NewCatalog().Restore(journal, taskCInventoryForCatalog(expected, available), lifecycleTrust, expected.Revision())
	if err != nil {
		t.Fatalf("Restore journal: %v", err)
	}
	if restored.Revision() != expected.Revision() {
		t.Fatalf("restored revision=%d want=%d", restored.Revision(), expected.Revision())
	}
	expectedCurrent, expectedOK := expected.CurrentForProfile("profile-main")
	restoredCurrent, restoredOK := restored.CurrentForProfile("profile-main")
	if restoredOK != expectedOK || (expectedOK && restoredCurrent.Manifest().ReleaseID != expectedCurrent.Manifest().ReleaseID) {
		t.Fatalf("restored current mismatch ok=%v/%v", restoredOK, expectedOK)
	}
	currentPath := filepath.Join(root, "current")
	if !expectedOK {
		if _, err := os.Lstat(currentPath); !os.IsNotExist(err) {
			t.Fatalf("unexpected current pointer: %v", err)
		}
	} else {
		target, err := os.Readlink(currentPath)
		if err != nil {
			t.Fatalf("read current: %v", err)
		}
		want := filepath.ToSlash(filepath.Join("releases", expectedCurrent.Manifest().ReleaseID))
		if target != want || filepath.IsAbs(target) {
			t.Fatalf("current=%q want relative %q", target, want)
		}
	}
	for _, name := range []string{"transaction.json", "transaction.new", "current.new", "journal.new"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("transient %s remains: %v", name, err)
		}
	}
}

func TestTaskCActivationPublishRollbackAlignment(t *testing.T) {
	root := taskCRoot(t)
	specOne, candidateOne, evidenceTrust := taskCCandidate(t, "release-c-one", 1)
	store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
	if err != nil {
		t.Fatalf("NewActivationStore: %v", err)
	}
	stagingOne := taskCWriteSealed(t, root, "staging-c-one", specOne, candidateOne)
	publishedOne, err := store.Publish(release.NewCatalog(), candidateOne, stagingOne)
	if err != nil {
		t.Fatalf("Publish one: %v", err)
	}
	taskCAssertSteady(t, root, publishedOne, []release.Release{candidateOne})

	specTwo, candidateTwo, _ := taskCCandidate(t, "release-c-two", 2)
	stagingTwo := taskCWriteSealed(t, root, "staging-c-two", specTwo, candidateTwo)
	publishedTwo, err := store.Publish(publishedOne, candidateTwo, stagingTwo)
	if err != nil {
		t.Fatalf("Publish two: %v", err)
	}
	available := []release.Release{candidateOne, candidateTwo}
	taskCAssertSteady(t, root, publishedTwo, available)

	rolledBack, selectedID, err := store.Rollback(publishedTwo, "release-c-two")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if selectedID != "release-c-one" {
		t.Fatalf("rollback selected %q", selectedID)
	}
	taskCAssertSteady(t, root, rolledBack, available)

	fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	recovered, err := fresh.Recover(available)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	taskCAssertSteady(t, root, recovered, available)
}

func TestTaskCActivationRejectsManifestParentAndRaceDrift(t *testing.T) {
	t.Run("different valid release", func(t *testing.T) {
		root := taskCRoot(t)
		_, requested, evidenceTrust := taskCCandidate(t, "release-c-requested", 1)
		otherSpec, other, _ := taskCCandidate(t, "release-c-other", 2)
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-other", otherSpec, other)
		if _, err := store.Publish(release.NewCatalog(), requested, staging); err == nil {
			t.Fatal("different valid sealed release accepted for requested candidate")
		}
	})

	t.Run("writable parent", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-parent", 1)
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-parent", spec, candidate)
		if err := os.Chmod(filepath.Join(root, "releases"), 0o770); err != nil {
			t.Fatalf("chmod releases: %v", err)
		}
		if _, err := store.Publish(release.NewCatalog(), candidate, staging); err == nil {
			t.Fatal("group-writable release parent accepted")
		}
	})

	t.Run("staging replacement", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-swap", 1)
		staging := filepath.Join(root, "releases", "staging-c-swap")
		backup := filepath.Join(root, "releases", "backup-c-swap")
		hook := func(phase release.ActivationPhase) error {
			if phase != release.ActivationBeforePromotion {
				return nil
			}
			if err := os.Rename(staging, backup); err != nil {
				return err
			}
			return os.Mkdir(staging, 0o500)
		}
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		taskCWriteSealed(t, root, "staging-c-swap", spec, candidate)
		if _, err := store.Publish(release.NewCatalog(), candidate, staging); err == nil {
			t.Fatal("post-validation staging replacement accepted")
		}
		if _, err := os.Lstat(filepath.Join(root, "releases", "release-c-swap")); !os.IsNotExist(err) {
			t.Fatalf("replacement promoted: %v", err)
		}
	})

	t.Run("racing destination", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-race", 1)
		target := filepath.Join(root, "releases", "release-c-race")
		hook := func(phase release.ActivationPhase) error {
			if phase != release.ActivationBeforePromotion {
				return nil
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(target, "marker"), []byte("preserve"), 0o600)
		}
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-race", spec, candidate)
		if _, err := store.Publish(release.NewCatalog(), candidate, staging); err == nil {
			t.Fatal("racing destination was overwritten")
		}
		marker, err := os.ReadFile(filepath.Join(target, "marker"))
		if err != nil || string(marker) != "preserve" {
			t.Fatalf("destination marker changed: %q %v", marker, err)
		}
	})
}

func TestTaskCActivationRecoverCrashMatrix(t *testing.T) {
	phases := []release.ActivationPhase{
		release.ActivationBeforeIntent,
		release.ActivationAfterIntent,
		release.ActivationBeforePromotion,
		release.ActivationAfterPromotion,
		release.ActivationBeforeCurrent,
		release.ActivationAfterCurrent,
		release.ActivationBeforeJournal,
		release.ActivationAfterJournal,
		release.ActivationBeforeCleanup,
		release.ActivationAfterCleanup,
	}
	for _, failPhase := range phases {
		failPhase := failPhase
		t.Run(string(failPhase), func(t *testing.T) {
			root := taskCRoot(t)
			spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-crash", 1)
			triggered := false
			hook := func(phase release.ActivationPhase) error {
				if phase == failPhase {
					triggered = true
					return errors.New("injected crash")
				}
				return nil
			}
			store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
			if err != nil {
				t.Fatalf("NewActivationStore: %v", err)
			}
			staging := taskCWriteSealed(t, root, "staging-c-crash", spec, candidate)
			base := release.NewCatalog()
			withCandidate, err := base.AddCandidate(candidate)
			if err != nil {
				t.Fatalf("AddCandidate: %v", err)
			}
			planned, err := withCandidate.Publish(candidate.Manifest().ReleaseID)
			if err != nil {
				t.Fatalf("plan Publish: %v", err)
			}
			_, publishErr := store.Publish(base, candidate, staging)
			if publishErr == nil || !triggered {
				t.Fatalf("injected phase %q did not interrupt: %v", failPhase, publishErr)
			}
			taskCRequireReason(t, publishErr, "activation_interrupted")

			fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
			if err != nil {
				t.Fatalf("fresh store: %v", err)
			}
			recovered, err := fresh.Recover([]release.Release{candidate})
			if err != nil {
				t.Fatalf("Recover(%s): %v", failPhase, err)
			}
			expected := planned
			if failPhase == release.ActivationBeforeIntent {
				expected = base
			}
			if recovered.Revision() != expected.Revision() {
				t.Fatalf("Recover(%s) revision=%d want=%d", failPhase, recovered.Revision(), expected.Revision())
			}
			taskCAssertSteady(t, root, expected, []release.Release{candidate})
			second, err := fresh.Recover([]release.Release{candidate})
			if err != nil || second.Revision() != expected.Revision() {
				t.Fatalf("idempotent Recover(%s): revision=%d err=%v", failPhase, second.Revision(), err)
			}
		})
	}
}

func TestTaskCActivationRecoverRejectsTamperingAndEscapingCurrent(t *testing.T) {
	t.Run("tampered intent", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-tamper", 1)
		hook := func(phase release.ActivationPhase) error {
			if phase == release.ActivationAfterIntent {
				return errors.New("stop after intent")
			}
			return nil
		}
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-tamper", spec, candidate)
		if _, err := store.Publish(release.NewCatalog(), candidate, staging); err == nil {
			t.Fatal("after-intent crash did not interrupt")
		}
		intentPath := filepath.Join(root, "transaction.json")
		raw, err := os.ReadFile(intentPath)
		if err != nil {
			t.Fatalf("read intent: %v", err)
		}
		index := bytes.Index(raw, []byte(`"signature":"`))
		if index < 0 {
			t.Fatal("intent signature missing")
		}
		index += len(`"signature":"`)
		if raw[index] == '0' {
			raw[index] = '1'
		} else {
			raw[index] = '0'
		}
		if err := os.WriteFile(intentPath, raw, 0o600); err != nil {
			t.Fatalf("tamper intent: %v", err)
		}
		fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("fresh store: %v", err)
		}
		if _, err := fresh.Recover([]release.Release{candidate}); err == nil {
			t.Fatal("tampered signed intent recovered")
		}
		if _, err := os.Lstat(intentPath); err != nil {
			t.Fatalf("tampered intent was cleared: %v", err)
		}
	})

	t.Run("escaping current", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-escape", 1)
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-escape", spec, candidate)
		if _, err := store.Publish(release.NewCatalog(), candidate, staging); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		currentPath := filepath.Join(root, "current")
		if err := os.Remove(currentPath); err != nil {
			t.Fatalf("remove current: %v", err)
		}
		if err := os.Symlink("../escape", currentPath); err != nil {
			t.Fatalf("write escaping current: %v", err)
		}
		fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("fresh store: %v", err)
		}
		if _, err := fresh.Recover([]release.Release{candidate}); err == nil {
			t.Fatal("escaping current target accepted")
		}
		target, err := os.Readlink(currentPath)
		if err != nil || target != "../escape" {
			t.Fatalf("escaping current unexpectedly rewritten: %q %v", target, err)
		}
	})
}
