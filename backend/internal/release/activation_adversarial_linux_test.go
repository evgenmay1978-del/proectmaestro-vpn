//go:build linux

package release_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func TestTaskCActivationRejectsStagingFinalNameCollision(t *testing.T) {
	root := taskCRoot(t)
	spec, candidate, evidenceTrust := taskCCandidate(t, "staging-c-same", 1)
	store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
	if err != nil {
		t.Fatalf("NewActivationStore: %v", err)
	}
	staging := taskCWriteSealed(t, root, "staging-c-same", spec, candidate)
	_, err = store.Publish(release.NewCatalog(), candidate, staging)
	taskCRequireReason(t, err, "activation_staging_invalid")
	if _, statErr := os.Lstat(filepath.Join(root, "transaction.json")); !os.IsNotExist(statErr) {
		t.Fatalf("unrecoverable transaction was written: %v", statErr)
	}
	if _, statErr := os.Lstat(staging); statErr != nil {
		t.Fatalf("rejected staging tree changed: %v", statErr)
	}
}

func TestTaskCActivationRecoverRejectsImpossibleDurableOrdering(t *testing.T) {
	root := taskCRoot(t)
	spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-order", 1)
	hook := func(phase release.ActivationPhase) error {
		if phase == release.ActivationAfterJournal {
			return errors.New("stop after journal")
		}
		return nil
	}
	store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
	if err != nil {
		t.Fatalf("NewActivationStore: %v", err)
	}
	staging := taskCWriteSealed(t, root, "staging-c-order", spec, candidate)
	_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
	taskCRequireReason(t, publishErr, "activation_interrupted")
	if err := os.Remove(filepath.Join(root, "current")); err != nil {
		t.Fatalf("remove current: %v", err)
	}
	fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	_, err = fresh.Recover([]release.Release{candidate})
	taskCRequireReason(t, err, "activation_current_mismatch")
	if _, statErr := os.Lstat(filepath.Join(root, "transaction.json")); statErr != nil {
		t.Fatalf("invalid durable state cleared its transaction: %v", statErr)
	}
}

func TestTaskCActivationRecoverRepairsOwnedPartialTemps(t *testing.T) {
	root := taskCRoot(t)
	spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-temps", 1)
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
	staging := taskCWriteSealed(t, root, "staging-c-temps", spec, candidate)
	_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
	taskCRequireReason(t, publishErr, "activation_interrupted")
	for _, name := range []string{"transaction.new", "journal.new"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatalf("write partial %s: %v", name, err)
		}
	}
	if err := os.Symlink("releases/release-c-temps", filepath.Join(root, "current.new")); err != nil {
		t.Fatalf("write partial current: %v", err)
	}
	fresh, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil))
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	recovered, err := fresh.Recover([]release.Release{candidate})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	taskCAssertSteady(t, root, recovered, []release.Release{candidate})
}

func TestTaskCActivationRecoverUsesSignedAdmissionTime(t *testing.T) {
	root := taskCRoot(t)
	spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-admission", 1)
	admissionTime := time.Now().UTC().Add(23 * time.Hour)
	hook := func(phase release.ActivationPhase) error {
		if phase == release.ActivationAfterIntent {
			return errors.New("stop after intent")
		}
		return nil
	}
	config := taskCConfig(root, evidenceTrust, hook)
	config.Now = func() time.Time { return admissionTime }
	store, err := release.NewActivationStore(config)
	if err != nil {
		t.Fatalf("NewActivationStore: %v", err)
	}
	staging := taskCWriteSealed(t, root, "staging-c-admission", spec, candidate)
	_, publishErr := store.Publish(release.NewCatalog(), candidate, staging)
	taskCRequireReason(t, publishErr, "activation_interrupted")
	recoveryConfig := taskCConfig(root, evidenceTrust, nil)
	recoveryConfig.Now = func() time.Time { return admissionTime.Add(72 * time.Hour) }
	fresh, err := release.NewActivationStore(recoveryConfig)
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	recovered, err := fresh.Recover([]release.Release{candidate})
	if err != nil {
		t.Fatalf("Recover with signed admission time: %v", err)
	}
	taskCAssertSteady(t, root, recovered, []release.Release{candidate})
}

func TestTaskCActivationRejectsUntrustedLockWithoutMutatingIt(t *testing.T) {
	root := taskCRoot(t)
	_, _, evidenceTrust := taskCCandidate(t, "release-c-lock", 1)
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Chmod(victim, 0o644); err != nil {
		t.Fatalf("chmod victim: %v", err)
	}
	if err := os.Link(victim, filepath.Join(root, "activation.lock")); err != nil {
		t.Fatalf("hardlink lock: %v", err)
	}
	if _, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, nil)); err == nil {
		t.Fatal("hardlinked activation lock accepted")
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatalf("stat victim: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("untrusted lock target mode mutated to %o", info.Mode().Perm())
	}
}

func TestTaskCActivationRevalidatesStagingAfterBeforeIntentHook(t *testing.T) {
	root := taskCRoot(t)
	spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-presync", 1)
	var staging string
	hook := func(phase release.ActivationPhase) error {
		if phase != release.ActivationBeforeIntent {
			return nil
		}
		return os.Chmod(filepath.Join(staging, "config.json"), 0o600)
	}
	store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
	if err != nil {
		t.Fatalf("NewActivationStore: %v", err)
	}
	staging = taskCWriteSealed(t, root, "staging-c-presync", spec, candidate)
	_, err = store.Publish(release.NewCatalog(), candidate, staging)
	taskCRequireReason(t, err, "promotion_sync_failed")
	if _, statErr := os.Lstat(filepath.Join(root, "transaction.json")); !os.IsNotExist(statErr) {
		t.Fatalf("transaction was persisted before staging revalidation: %v", statErr)
	}
}

func TestTaskCActivationUsesRetainedRootAcrossAncestorSwap(t *testing.T) {
	sandbox := taskCRoot(t)
	root := filepath.Join(sandbox, "activation")
	retained := filepath.Join(sandbox, "activation-retained")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("mkdir activation root: %v", err)
	}
	spec, candidate, evidenceTrust := taskCCandidate(t, "release-c-anchored", 1)
	swapped := false
	hook := func(phase release.ActivationPhase) error {
		if phase != release.ActivationBeforePromotion {
			return nil
		}
		if err := os.Rename(root, retained); err != nil {
			return err
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			return err
		}
		swapped = true
		return nil
	}
	store, err := release.NewActivationStore(taskCConfig(root, evidenceTrust, hook))
	if err != nil {
		t.Fatalf("NewActivationStore: %v", err)
	}
	staging := taskCWriteSealed(t, root, "staging-c-anchored", spec, candidate)
	published, err := store.Publish(release.NewCatalog(), candidate, staging)
	if err != nil {
		t.Fatalf("Publish across ancestor swap: %v", err)
	}
	if !swapped {
		t.Fatal("ancestor swap hook was not reached")
	}
	taskCAssertSteady(t, retained, published, []release.Release{candidate})
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read replacement root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("replacement root received split transaction state: %v", entries)
	}
}
