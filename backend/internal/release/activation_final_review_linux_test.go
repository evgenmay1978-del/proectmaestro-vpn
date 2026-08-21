//go:build linux

package release_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func taskCFinalReviewPhases() []release.ActivationPhase {
	return []release.ActivationPhase{
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
}

func TestTaskCActivationRecoverReplacementPublishCrashMatrix(t *testing.T) {
	for _, failPhase := range taskCFinalReviewPhases() {
		failPhase := failPhase
		t.Run(string(failPhase), func(t *testing.T) {
			root := taskCRoot(t)
			specOne, candidateOne, evidenceTrust := taskCCandidate(t, "release-c-replace-one", 1)
			bootstrap, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
			if err != nil {
				t.Fatalf("NewActivationStore bootstrap: %v", err)
			}
			stagingOne := taskCWriteSealed(t, root, "staging-c-replace-one", specOne, candidateOne)
			base, err := bootstrap.Publish(release.NewCatalog(), candidateOne, stagingOne)
			if err != nil {
				t.Fatalf("Publish bootstrap: %v", err)
			}

			specTwo, candidateTwo, _ := taskCCandidate(t, "release-c-replace-two", 2)
			stagingTwo := taskCWriteSealed(t, root, "staging-c-replace-two", specTwo, candidateTwo)
			withCandidate, err := base.AddCandidate(candidateTwo)
			if err != nil {
				t.Fatalf("AddCandidate replacement: %v", err)
			}
			planned, err := withCandidate.Publish(candidateTwo.Manifest().ReleaseID)
			if err != nil {
				t.Fatalf("plan replacement Publish: %v", err)
			}

			triggered := false
			hook := func(phase release.ActivationPhase) error {
				if phase == failPhase {
					triggered = true
					return errors.New("injected replacement publish crash")
				}
				return nil
			}
			store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
			if err != nil {
				t.Fatalf("NewActivationStore crash store: %v", err)
			}
			_, publishErr := store.Publish(base, candidateTwo, stagingTwo)
			if publishErr == nil || !triggered {
				t.Fatalf("injected replacement phase %q did not interrupt: %v", failPhase, publishErr)
			}
			taskCRequireReason(t, publishErr, "activation_interrupted")

			available := []release.Release{candidateOne, candidateTwo}
			fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
			if err != nil {
				t.Fatalf("NewActivationStore recovery: %v", err)
			}
			recovered, err := fresh.Recover(available)
			if err != nil {
				t.Fatalf("Recover replacement (%s): %v", failPhase, err)
			}
			expected := planned
			if failPhase == release.ActivationBeforeIntent {
				expected = base
			}
			if recovered.Revision() != expected.Revision() {
				t.Fatalf(
					"Recover replacement (%s) revision=%d want=%d",
					failPhase, recovered.Revision(), expected.Revision(),
				)
			}
			taskCAssertSteady(t, root, expected, available)

			second, err := fresh.Recover(available)
			if err != nil || second.Revision() != expected.Revision() {
				t.Fatalf(
					"idempotent replacement Recover(%s): revision=%d err=%v",
					failPhase, second.Revision(), err,
				)
			}
			taskCAssertSteady(t, root, expected, available)
		})
	}
}

func TestTaskCActivationRecoverRollbackCrashMatrix(t *testing.T) {
	for _, failPhase := range taskCFinalReviewPhases() {
		failPhase := failPhase
		t.Run(string(failPhase), func(t *testing.T) {
			root := taskCRoot(t)
			specOne, candidateOne, evidenceTrust := taskCCandidate(t, "release-c-rollback-one", 1)
			bootstrap, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
			if err != nil {
				t.Fatalf("NewActivationStore bootstrap: %v", err)
			}
			stagingOne := taskCWriteSealed(t, root, "staging-c-rollback-one", specOne, candidateOne)
			publishedOne, err := bootstrap.Publish(release.NewCatalog(), candidateOne, stagingOne)
			if err != nil {
				t.Fatalf("Publish one: %v", err)
			}
			specTwo, candidateTwo, _ := taskCCandidate(t, "release-c-rollback-two", 2)
			stagingTwo := taskCWriteSealed(t, root, "staging-c-rollback-two", specTwo, candidateTwo)
			base, err := bootstrap.Publish(publishedOne, candidateTwo, stagingTwo)
			if err != nil {
				t.Fatalf("Publish two: %v", err)
			}

			planned, selectedID, err := base.Rollback(candidateTwo.Manifest().ReleaseID)
			if err != nil {
				t.Fatalf("plan Rollback: %v", err)
			}
			if selectedID != candidateOne.Manifest().ReleaseID {
				t.Fatalf("planned rollback selected %q", selectedID)
			}
			triggered := false
			hook := func(phase release.ActivationPhase) error {
				if phase == failPhase {
					triggered = true
					return errors.New("injected rollback crash")
				}
				return nil
			}
			store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
			if err != nil {
				t.Fatalf("NewActivationStore crash store: %v", err)
			}
			_, _, rollbackErr := store.Rollback(base, candidateTwo.Manifest().ReleaseID)
			if rollbackErr == nil || !triggered {
				t.Fatalf("injected rollback phase %q did not interrupt: %v", failPhase, rollbackErr)
			}
			taskCRequireReason(t, rollbackErr, "activation_interrupted")

			available := []release.Release{candidateOne, candidateTwo}
			fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
			if err != nil {
				t.Fatalf("NewActivationStore recovery: %v", err)
			}
			recovered, err := fresh.Recover(available)
			if err != nil {
				t.Fatalf("Recover rollback (%s): %v", failPhase, err)
			}
			expected := planned
			if failPhase == release.ActivationBeforeIntent {
				expected = base
			}
			if recovered.Revision() != expected.Revision() {
				t.Fatalf(
					"Recover rollback (%s) revision=%d want=%d",
					failPhase, recovered.Revision(), expected.Revision(),
				)
			}
			taskCAssertSteady(t, root, expected, available)

			second, err := fresh.Recover(available)
			if err != nil || second.Revision() != expected.Revision() {
				t.Fatalf(
					"idempotent rollback Recover(%s): revision=%d err=%v",
					failPhase, second.Revision(), err,
				)
			}
			taskCAssertSteady(t, root, expected, available)
		})
	}
}

func TestTaskCActivationRejectsPrevalidationTrustAnchorReplacement(t *testing.T) {
	t.Run("activation root inode", func(t *testing.T) {
		container := taskCRoot(t)
		root := filepath.Join(container, "activation")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatalf("mkdir activation root: %v", err)
		}
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-root-inode", 1)
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		journal, err := os.ReadFile(filepath.Join(root, "journal.json"))
		if err != nil {
			t.Fatalf("read initial journal: %v", err)
		}

		originalRoot := filepath.Join(container, "activation-before-swap")
		if err := os.Rename(root, originalRoot); err != nil {
			t.Fatalf("replace activation root: %v", err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatalf("mkdir replacement root: %v", err)
		}
		if err := os.Mkdir(filepath.Join(root, "releases"), 0o700); err != nil {
			t.Fatalf("mkdir replacement releases: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "journal.json"), journal, 0o600); err != nil {
			t.Fatalf("write replacement journal: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-root-inode", spec, candidate)

		_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
		if publishErr == nil {
			t.Error("pre-validation activation-root inode replacement accepted")
		}
		taskCAssertReleaseNotPromoted(
			t,
			candidate.Manifest().ReleaseID,
			filepath.Join(root, "releases"),
			filepath.Join(originalRoot, "releases"),
		)
		taskCAssertSteady(t, root, release.NewCatalog(), []release.Release{candidate})
	})

	t.Run("releases parent inode", func(t *testing.T) {
		container := taskCRoot(t)
		root := filepath.Join(container, "activation")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatalf("mkdir activation root: %v", err)
		}
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-parent-inode", 1)
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}

		releasesPath := filepath.Join(root, "releases")
		originalReleases := filepath.Join(container, "releases-before-swap")
		if err := os.Rename(releasesPath, originalReleases); err != nil {
			t.Fatalf("replace releases parent: %v", err)
		}
		if err := os.Mkdir(releasesPath, 0o700); err != nil {
			t.Fatalf("mkdir replacement releases: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-parent-inode", spec, candidate)

		_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
		if publishErr == nil {
			t.Error("pre-validation releases-parent inode replacement accepted")
		}
		taskCAssertReleaseNotPromoted(
			t,
			candidate.Manifest().ReleaseID,
			releasesPath,
			originalReleases,
		)
		taskCAssertSteady(t, root, release.NewCatalog(), []release.Release{candidate})
	})
}

func taskCAssertReleaseNotPromoted(t *testing.T, releaseID string, releaseParents ...string) {
	t.Helper()
	for _, parent := range releaseParents {
		path := filepath.Join(parent, releaseID)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("release promoted at %s: %v", path, err)
		}
	}
}
