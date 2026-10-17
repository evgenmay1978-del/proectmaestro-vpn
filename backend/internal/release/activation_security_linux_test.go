//go:build linux

package release_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func TestTaskCActivationSyncsStagingBeforeIntent(t *testing.T) {
	t.Run("successful sync precedes intent boundary", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-sync-order", 1)
		syncSeen := false
		stopAtIntent := errors.New("stop at pre-intent boundary")
		restore := release.TaskCReplacePreIntentStagingSyncForTest(func(stagingName string) error {
			syncSeen = true
			if stagingName != "staging-c-sync-order" {
				t.Fatalf("pre-intent sync staging=%q", stagingName)
			}
			return nil
		})
		defer restore()

		hook := func(phase release.ActivationPhase) error {
			if phase != release.ActivationBeforeIntent {
				return nil
			}
			if !syncSeen {
				t.Fatal("intent boundary reached before staging tree and releases parent were synced")
			}
			return stopAtIntent
		}
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-sync-order", spec, candidate)

		_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
		taskCRequireReason(t, publishErr, "activation_interrupted")
		if !syncSeen {
			t.Fatal("publish reached the intent path without invoking pre-intent staging sync")
		}
		taskCSecurityAssertNoTransaction(t, root)
	})

	t.Run("sync failure cannot persist intent", func(t *testing.T) {
		root := taskCRoot(t)
		spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-sync-failure", 1)
		syncFailure := errors.New("injected staging fsync failure")
		syncSeen := false
		beforeIntentSeen := false
		restore := release.TaskCReplacePreIntentStagingSyncForTest(func(stagingName string) error {
			syncSeen = true
			if stagingName != "staging-c-sync-failure" {
				t.Fatalf("pre-intent sync staging=%q", stagingName)
			}
			return syncFailure
		})
		defer restore()

		hook := func(phase release.ActivationPhase) error {
			if phase == release.ActivationBeforeIntent {
				beforeIntentSeen = true
			}
			return nil
		}
		store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
		if err != nil {
			t.Fatalf("NewActivationStore: %v", err)
		}
		staging := taskCWriteSealed(t, root, "staging-c-sync-failure", spec, candidate)

		_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
		if publishErr == nil {
			t.Fatal("publish accepted failed pre-intent staging sync")
		}
		if !syncSeen {
			t.Fatal("pre-intent staging sync was not attempted")
		}
		if beforeIntentSeen {
			t.Fatal("intent boundary reached after failed staging sync")
		}
		taskCSecurityAssertNoTransaction(t, root)
		if _, err := os.Lstat(staging); err != nil {
			t.Fatalf("failed pre-intent sync changed staging tree: %v", err)
		}
	})
}

func TestTaskCActivationRejectsRootBelowReplaceableAncestor(t *testing.T) {
	ancestor := t.TempDir()
	if err := os.Chmod(ancestor, 0o777); err != nil {
		t.Fatalf("make swap-capable ancestor: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(ancestor, 0o700) })

	root := filepath.Join(ancestor, "activation")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("mkdir activation root: %v", err)
	}
	_, _, evidenceTrust := taskCCandidate(t, "release-c-unsafe-ancestor", 1)

	_, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
	if err == nil {
		t.Error("activation root below non-sticky writable ancestor was accepted")
	} else {
		taskCRequireReason(t, err, "activation_root_untrusted")
	}
	for _, name := range []string{"activation.lock", "releases", "journal.json"} {
		if _, statErr := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(statErr) {
			t.Errorf("rejected store created %s: %v", name, statErr)
		}
	}
}

func taskCSecurityAssertNoTransaction(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, "transaction.json")); !os.IsNotExist(err) {
		t.Fatalf("transaction persisted before staging durability: %v", err)
	}
}
