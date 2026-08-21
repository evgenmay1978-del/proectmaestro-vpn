package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	activationTransactionSchemaVersion = 1
	activationTransactionPurpose       = "maestro-release-activation-v1"
	maxActivationTransactionBytes      = 3 * maxManifestBytes
)

var activationSyncStagingBeforeIntent = func(root *activationLockedRoot, stagingName string) error {
	return root.activationSyncSealedRelease(stagingName)
}

type ActivationPhase string

const (
	ActivationBeforeIntent    ActivationPhase = "before_intent"
	ActivationAfterIntent     ActivationPhase = "after_intent"
	ActivationBeforePromotion ActivationPhase = "before_promotion"
	ActivationAfterPromotion  ActivationPhase = "after_promotion"
	ActivationBeforeCurrent   ActivationPhase = "before_current"
	ActivationAfterCurrent    ActivationPhase = "after_current"
	ActivationBeforeJournal   ActivationPhase = "before_journal"
	ActivationAfterJournal    ActivationPhase = "after_journal"
	ActivationBeforeCleanup   ActivationPhase = "before_cleanup"
	ActivationAfterCleanup    ActivationPhase = "after_cleanup"
)

type ActivationStoreConfig struct {
	Root               string
	StoreID            string
	TransportProfileID string
	TrustedOwnerUID    uint32
	EvidenceTrust      EvidenceTrust
	LifecycleSigner    LifecycleSigner
	LifecycleTrust     LifecycleTrust
	MinimumRevision    uint64
	Now                func() time.Time
	PhaseHook          func(ActivationPhase) error
}

type activationFilesystemAnchor struct {
	root     filesystemIdentity
	releases filesystemIdentity
}

type ActivationStore struct {
	root               string
	storeID            string
	transportProfileID string
	trustedOwnerUID    uint32
	evidenceTrust      EvidenceTrust
	lifecycleSigner    LifecycleSigner
	lifecycleTrust     LifecycleTrust
	minimumRevision    uint64
	now                func() time.Time
	phaseHook          func(ActivationPhase) error
	anchor             activationFilesystemAnchor
}

type activationIntent struct {
	Operation            string `json:"operation"`
	TransportProfileID   string `json:"transport_profile_id"`
	BaseRevision         uint64 `json:"base_revision"`
	PlannedRevision      uint64 `json:"planned_revision"`
	BaseJournal          []byte `json:"base_journal"`
	BaseJournalSHA256    string `json:"base_journal_sha256"`
	PlannedJournal       []byte `json:"planned_journal"`
	PlannedJournalSHA256 string `json:"planned_journal_sha256"`
	FromReleaseID        string `json:"from_release_id"`
	ToReleaseID          string `json:"to_release_id"`
	FromTarget           string `json:"from_target"`
	ToTarget             string `json:"to_target"`
	ManifestSHA256       string `json:"manifest_sha256"`
	EvidenceTrustSHA256  string `json:"evidence_trust_sha256"`
	StagingName          string `json:"staging_name"`
	AdmissionTime        string `json:"admission_time"`
}

type activationSignaturePayload struct {
	SchemaVersion int              `json:"schema_version"`
	Purpose       string           `json:"purpose"`
	StoreID       string           `json:"store_id"`
	KeyID         string           `json:"key_id"`
	Intent        activationIntent `json:"intent"`
}

type signedActivationTransaction struct {
	SchemaVersion int              `json:"schema_version"`
	Purpose       string           `json:"purpose"`
	StoreID       string           `json:"store_id"`
	KeyID         string           `json:"key_id"`
	Intent        activationIntent `json:"intent"`
	Signature     string           `json:"signature"`
}

func NewActivationStore(config ActivationStoreConfig) (*ActivationStore, error) {
	if !activationPlatformSupported() {
		return nil, invalid("unsupported_platform")
	}
	store, err := freezeActivationStore(config)
	if err != nil {
		return nil, err
	}
	initialJournal, err := NewCatalog().Snapshot(store.lifecycleSigner)
	if err != nil {
		return nil, err
	}
	err = activationWithRootLock(store.root, store.trustedOwnerUID, nil, func(root *activationLockedRoot) error {
		store.anchor = root.activationAnchor()
		journal, exists, err := root.activationReadRegular("journal.json", maxManifestBytes)
		if err != nil {
			return err
		}
		if exists {
			if len(journal) == 0 {
				return invalid("activation_journal_invalid")
			}
			return nil
		}
		if store.minimumRevision != NewCatalog().Revision() {
			return invalid("journal_revision_stale")
		}
		transactionExists, err := root.activationEntryExists("transaction.json")
		if err != nil {
			return err
		}
		_, currentExists, err := root.activationReadCurrent()
		if err != nil {
			return err
		}
		if transactionExists || currentExists {
			return invalid("activation_state_mismatch")
		}
		if err := root.activationRepairTemps(nil); err != nil {
			return err
		}
		if err := activationRequireNoTemps(root); err != nil {
			return err
		}
		return root.activationWriteNoReplace("journal.new", "journal.json", initialJournal)
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}

func freezeActivationStore(config ActivationStoreConfig) (*ActivationStore, error) {
	if strings.TrimSpace(config.Root) == "" || !filepath.IsAbs(config.Root) ||
		filepath.Clean(config.Root) != config.Root || filepath.Dir(config.Root) == config.Root ||
		strings.IndexByte(config.Root, 0) >= 0 || !validID(config.StoreID) ||
		!validID(config.TransportProfileID) || config.MinimumRevision == 0 ||
		config.Now == nil {
		return nil, invalid("activation_config_invalid")
	}
	if err := config.EvidenceTrust.validate(); err != nil {
		return nil, err
	}
	if err := validateLifecycleSigner(config.LifecycleSigner); err != nil {
		return nil, err
	}
	trusted := make(LifecycleTrust, len(config.LifecycleTrust))
	for keyID, publicKey := range config.LifecycleTrust {
		if !validID(keyID) || len(publicKey) != ed25519.PublicKeySize {
			return nil, invalid("journal_trust_invalid")
		}
		trusted[keyID] = append(ed25519.PublicKey(nil), publicKey...)
	}
	signerPublic, ok := trusted[config.LifecycleSigner.KeyID]
	expectedPublic := config.LifecycleSigner.PrivateKey.Public().(ed25519.PublicKey)
	if !ok || !bytes.Equal(signerPublic, expectedPublic) {
		return nil, invalid("journal_signer_untrusted")
	}
	return &ActivationStore{
		root:               config.Root,
		storeID:            config.StoreID,
		transportProfileID: config.TransportProfileID,
		trustedOwnerUID:    config.TrustedOwnerUID,
		evidenceTrust: EvidenceTrust{
			SchemaVersion: config.EvidenceTrust.SchemaVersion,
			Keys:          cloneTrustKeys(config.EvidenceTrust.Keys),
		},
		lifecycleSigner: LifecycleSigner{
			KeyID:      config.LifecycleSigner.KeyID,
			PrivateKey: append(ed25519.PrivateKey(nil), config.LifecycleSigner.PrivateKey...),
		},
		lifecycleTrust:  trusted,
		minimumRevision: config.MinimumRevision,
		now:             config.Now,
		phaseHook:       config.PhaseHook,
	}, nil
}

func (store *ActivationStore) Publish(base Catalog, candidate Release, stagingDir string) (Catalog, error) {
	if !activationPlatformSupported() {
		return Catalog{}, invalid("unsupported_platform")
	}
	if store == nil {
		return Catalog{}, invalid("activation_store_invalid")
	}
	var result Catalog
	err := activationWithRootLock(store.root, store.trustedOwnerUID, &store.anchor, func(root *activationLockedRoot) error {
		planned, err := store.publishLocked(root, base, candidate, stagingDir)
		if err != nil {
			return err
		}
		result = planned
		return nil
	})
	if err != nil {
		return Catalog{}, err
	}
	return result, nil
}

func (store *ActivationStore) publishLocked(root *activationLockedRoot, base Catalog, candidate Release, stagingDir string) (Catalog, error) {
	if err := validateCatalog(base); err != nil {
		return Catalog{}, err
	}
	manifest := candidate.Manifest()
	if candidate.State() != Candidate || manifest.TransportProfileID != store.transportProfileID {
		return Catalog{}, invalid("activation_candidate_mismatch")
	}
	stagingName, err := store.activationStagingName(stagingDir)
	if err != nil {
		return Catalog{}, err
	}
	if stagingName == manifest.ReleaseID {
		return Catalog{}, invalid("activation_staging_invalid")
	}
	baseInventory := activationCatalogReleases(base)
	baseJournal, err := store.activationRequireBase(root, base, baseInventory)
	if err != nil {
		return Catalog{}, err
	}
	now, err := store.activationNow()
	if err != nil {
		return Catalog{}, err
	}
	identity, err := root.activationInspectSealedRelease(stagingName, store.evidenceTrust, &now)
	if err != nil {
		return Catalog{}, err
	}
	if err := activationMatchSealed(identity, candidate); err != nil {
		return Catalog{}, err
	}
	withCandidate, err := base.AddCandidate(candidate)
	if err != nil {
		return Catalog{}, err
	}
	planned, err := withCandidate.Publish(manifest.ReleaseID)
	if err != nil {
		return Catalog{}, err
	}
	plannedCurrent, ok := planned.CurrentForProfile(store.transportProfileID)
	if !ok || plannedCurrent.Manifest().ReleaseID != manifest.ReleaseID {
		return Catalog{}, invalid("activation_transition_invalid")
	}
	plannedJournal, err := planned.Snapshot(store.lifecycleSigner)
	if err != nil {
		return Catalog{}, err
	}
	fromID, fromTarget := activationCatalogCurrent(base, store.transportProfileID)
	toTarget := activationTarget(manifest.ReleaseID)
	intent := activationIntent{
		Operation: "publish", TransportProfileID: store.transportProfileID,
		BaseRevision: base.Revision(), PlannedRevision: planned.Revision(),
		BaseJournal: append([]byte(nil), baseJournal...), BaseJournalSHA256: digestBytes(baseJournal),
		PlannedJournal: append([]byte(nil), plannedJournal...), PlannedJournalSHA256: digestBytes(plannedJournal),
		FromReleaseID: fromID, ToReleaseID: manifest.ReleaseID,
		FromTarget: fromTarget, ToTarget: toTarget,
		ManifestSHA256: candidate.ManifestSHA256(), EvidenceTrustSHA256: manifest.EvidenceTrustSHA256,
		StagingName:   stagingName,
		AdmissionTime: now.Format(time.RFC3339Nano),
	}
	intentRaw, err := store.activationSignIntent(intent)
	if err != nil {
		return Catalog{}, err
	}
	if err := activationSyncStagingBeforeIntent(root, stagingName); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationBeforeIntent); err != nil {
		return Catalog{}, err
	}
	identity, err = root.activationPrepareSealedRelease(stagingName, store.evidenceTrust, &now)
	if err != nil {
		return Catalog{}, err
	}
	if err := activationMatchSealed(identity, candidate); err != nil {
		return Catalog{}, err
	}
	if err := root.activationWriteNoReplace("transaction.new", "transaction.json", intentRaw); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationAfterIntent); err != nil {
		return Catalog{}, err
	}
	promoted, err := root.activationPromoteSealedRelease(
		stagingName, manifest.ReleaseID, store.evidenceTrust, now,
		func() error { return store.activationCallPhase(ActivationBeforePromotion) },
	)
	if err != nil {
		return Catalog{}, err
	}
	if err := activationMatchSealed(promoted, candidate); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationAfterPromotion); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationBeforeCurrent); err != nil {
		return Catalog{}, err
	}
	if err := root.activationSwapCurrent(fromTarget, fromID != "", toTarget); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationAfterCurrent); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationBeforeJournal); err != nil {
		return Catalog{}, err
	}
	if err := root.activationWriteReplaceExpected("journal.new", "journal.json", baseJournal, plannedJournal); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationAfterJournal); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationBeforeCleanup); err != nil {
		return Catalog{}, err
	}
	inventory := append(baseInventory, cloneRelease(candidate))
	if err := store.activationVerifyFinal(root, planned, inventory, plannedJournal); err != nil {
		return Catalog{}, err
	}
	if err := root.activationRemoveExact("transaction.json", intentRaw); err != nil {
		return Catalog{}, err
	}
	if err := store.activationCallPhase(ActivationAfterCleanup); err != nil {
		return Catalog{}, err
	}
	if err := store.activationVerifySteady(root, planned, inventory, plannedJournal); err != nil {
		return Catalog{}, err
	}
	return planned, nil
}

func (store *ActivationStore) Rollback(base Catalog, currentID string) (Catalog, string, error) {
	if !activationPlatformSupported() {
		return Catalog{}, "", invalid("unsupported_platform")
	}
	if store == nil {
		return Catalog{}, "", invalid("activation_store_invalid")
	}
	var result Catalog
	var selected string
	err := activationWithRootLock(store.root, store.trustedOwnerUID, &store.anchor, func(root *activationLockedRoot) error {
		planned, selectedID, err := store.rollbackLocked(root, base, currentID)
		if err != nil {
			return err
		}
		result, selected = planned, selectedID
		return nil
	})
	if err != nil {
		return Catalog{}, "", err
	}
	return result, selected, nil
}

func (store *ActivationStore) rollbackLocked(root *activationLockedRoot, base Catalog, currentID string) (Catalog, string, error) {
	if err := validateCatalog(base); err != nil {
		return Catalog{}, "", err
	}
	current, ok := base.CurrentForProfile(store.transportProfileID)
	if !ok || current.Manifest().ReleaseID != currentID {
		return Catalog{}, "", invalid("activation_current_mismatch")
	}
	inventory := activationCatalogReleases(base)
	baseJournal, err := store.activationRequireBase(root, base, inventory)
	if err != nil {
		return Catalog{}, "", err
	}
	planned, selectedID, err := base.Rollback(currentID)
	if err != nil {
		return Catalog{}, "", err
	}
	selected, ok := base.Get(selectedID)
	if !ok || selected.Manifest().TransportProfileID != store.transportProfileID {
		return Catalog{}, "", invalid("activation_transition_invalid")
	}
	identity, err := root.activationInspectSealedRelease(selectedID, store.evidenceTrust, nil)
	if err != nil {
		return Catalog{}, "", err
	}
	if err := activationMatchSealed(identity, selected); err != nil {
		return Catalog{}, "", err
	}
	plannedJournal, err := planned.Snapshot(store.lifecycleSigner)
	if err != nil {
		return Catalog{}, "", err
	}
	now, err := store.activationNow()
	if err != nil {
		return Catalog{}, "", err
	}
	fromTarget, toTarget := activationTarget(currentID), activationTarget(selectedID)
	intent := activationIntent{
		Operation: "rollback", TransportProfileID: store.transportProfileID,
		BaseRevision: base.Revision(), PlannedRevision: planned.Revision(),
		BaseJournal: append([]byte(nil), baseJournal...), BaseJournalSHA256: digestBytes(baseJournal),
		PlannedJournal: append([]byte(nil), plannedJournal...), PlannedJournalSHA256: digestBytes(plannedJournal),
		FromReleaseID: currentID, ToReleaseID: selectedID,
		FromTarget: fromTarget, ToTarget: toTarget,
		ManifestSHA256: selected.ManifestSHA256(), EvidenceTrustSHA256: selected.Manifest().EvidenceTrustSHA256,
		StagingName:   "",
		AdmissionTime: now.Format(time.RFC3339Nano),
	}
	intentRaw, err := store.activationSignIntent(intent)
	if err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationBeforeIntent); err != nil {
		return Catalog{}, "", err
	}
	identity, err = root.activationPrepareSealedRelease(selectedID, store.evidenceTrust, nil)
	if err != nil {
		return Catalog{}, "", err
	}
	if err := activationMatchSealed(identity, selected); err != nil {
		return Catalog{}, "", err
	}
	if err := root.activationWriteNoReplace("transaction.new", "transaction.json", intentRaw); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationAfterIntent); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationBeforePromotion); err != nil {
		return Catalog{}, "", err
	}
	identity, err = root.activationInspectSealedRelease(selectedID, store.evidenceTrust, nil)
	if err != nil {
		return Catalog{}, "", err
	}
	if err := activationMatchSealed(identity, selected); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationAfterPromotion); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationBeforeCurrent); err != nil {
		return Catalog{}, "", err
	}
	if err := root.activationSwapCurrent(fromTarget, true, toTarget); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationAfterCurrent); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationBeforeJournal); err != nil {
		return Catalog{}, "", err
	}
	if err := root.activationWriteReplaceExpected("journal.new", "journal.json", baseJournal, plannedJournal); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationAfterJournal); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationBeforeCleanup); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationVerifyFinal(root, planned, inventory, plannedJournal); err != nil {
		return Catalog{}, "", err
	}
	if err := root.activationRemoveExact("transaction.json", intentRaw); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationCallPhase(ActivationAfterCleanup); err != nil {
		return Catalog{}, "", err
	}
	if err := store.activationVerifySteady(root, planned, inventory, plannedJournal); err != nil {
		return Catalog{}, "", err
	}
	return planned, selectedID, nil
}

func (store *ActivationStore) Recover(available []Release) (Catalog, error) {
	if !activationPlatformSupported() {
		return Catalog{}, invalid("unsupported_platform")
	}
	if store == nil {
		return Catalog{}, invalid("activation_store_invalid")
	}
	if _, err := validateRestoreReleases(available); err != nil {
		return Catalog{}, err
	}
	var result Catalog
	err := activationWithRootLock(store.root, store.trustedOwnerUID, &store.anchor, func(root *activationLockedRoot) error {
		recovered, err := store.recoverLocked(root, available)
		if err != nil {
			return err
		}
		result = recovered
		return nil
	})
	if err != nil {
		return Catalog{}, err
	}
	return result, nil
}

func (store *ActivationStore) recoverLocked(root *activationLockedRoot, available []Release) (Catalog, error) {
	transactionRaw, transactionExists, err := root.activationReadRegular("transaction.json", maxActivationTransactionBytes)
	if err != nil {
		return Catalog{}, err
	}
	if !transactionExists {
		if err := root.activationRepairTemps(nil); err != nil {
			return Catalog{}, err
		}
		if err := activationRequireNoTemps(root); err != nil {
			return Catalog{}, err
		}
		journal, exists, err := root.activationReadRegular("journal.json", maxManifestBytes)
		if err != nil {
			return Catalog{}, err
		}
		if !exists {
			return Catalog{}, invalid("activation_journal_missing")
		}
		catalog, err := activationRestoreJournal(journal, available, store.lifecycleTrust, store.minimumRevision)
		if err != nil {
			return Catalog{}, err
		}
		if err := store.activationValidatePointer(root, catalog); err != nil {
			return Catalog{}, err
		}
		return catalog, nil
	}
	intent, _, planned, err := store.activationVerifyIntent(transactionRaw, available)
	if err != nil {
		return Catalog{}, err
	}
	if err := root.activationRepairTemps(&intent); err != nil {
		return Catalog{}, err
	}
	if err := activationRequireNoTemps(root); err != nil {
		return Catalog{}, err
	}
	journal, exists, err := root.activationReadRegular("journal.json", maxManifestBytes)
	if err != nil {
		return Catalog{}, err
	}
	journalIsBase := bytes.Equal(journal, intent.BaseJournal)
	journalIsPlanned := bytes.Equal(journal, intent.PlannedJournal)
	if !exists || (!journalIsBase && !journalIsPlanned) {
		return Catalog{}, invalid("activation_journal_mismatch")
	}
	actualTarget, currentExists, err := root.activationReadCurrent()
	if err != nil {
		return Catalog{}, err
	}
	if currentExists {
		if _, err := activationReleaseIDFromTarget(actualTarget); err != nil {
			return Catalog{}, err
		}
	}
	fromExists := intent.FromReleaseID != ""
	currentIsFrom := activationPointerEquals(actualTarget, currentExists, intent.FromTarget, fromExists)
	currentIsTo := activationPointerEquals(actualTarget, currentExists, intent.ToTarget, true)
	if (!currentIsFrom && !currentIsTo) || (journalIsPlanned && !currentIsTo) {
		return Catalog{}, invalid("activation_current_mismatch")
	}
	target, ok := planned.Get(intent.ToReleaseID)
	if !ok {
		return Catalog{}, invalid("activation_transition_invalid")
	}
	if intent.Operation == "publish" {
		stagingExists, err := root.activationReleaseDirExists(intent.StagingName)
		if err != nil {
			return Catalog{}, err
		}
		targetExists, err := root.activationReleaseDirExists(intent.ToReleaseID)
		if err != nil {
			return Catalog{}, err
		}
		if stagingExists == targetExists {
			return Catalog{}, invalid("activation_release_state_invalid")
		}
		admissionTime, err := time.Parse(time.RFC3339Nano, intent.AdmissionTime)
		if err != nil {
			return Catalog{}, invalid("activation_transaction_binding_mismatch")
		}
		if stagingExists {
			identity, err := root.activationPromoteSealedRelease(
				intent.StagingName, intent.ToReleaseID, store.evidenceTrust, admissionTime, nil,
			)
			if err != nil {
				return Catalog{}, err
			}
			if err := activationMatchSealed(identity, target); err != nil {
				return Catalog{}, err
			}
		} else {
			identity, err := root.activationPrepareSealedRelease(intent.ToReleaseID, store.evidenceTrust, &admissionTime)
			if err != nil {
				return Catalog{}, err
			}
			if err := activationMatchSealed(identity, target); err != nil {
				return Catalog{}, err
			}
		}
	} else {
		targetExists, err := root.activationReleaseDirExists(intent.ToReleaseID)
		if err != nil {
			return Catalog{}, err
		}
		if !targetExists {
			return Catalog{}, invalid("activation_release_state_invalid")
		}
		identity, err := root.activationPrepareSealedRelease(intent.ToReleaseID, store.evidenceTrust, nil)
		if err != nil {
			return Catalog{}, err
		}
		if err := activationMatchSealed(identity, target); err != nil {
			return Catalog{}, err
		}
	}
	actualTarget, currentExists, err = root.activationReadCurrent()
	if err != nil {
		return Catalog{}, err
	}
	if activationPointerEquals(actualTarget, currentExists, intent.FromTarget, fromExists) {
		if err := root.activationSwapCurrent(intent.FromTarget, fromExists, intent.ToTarget); err != nil {
			return Catalog{}, err
		}
	} else if !activationPointerEquals(actualTarget, currentExists, intent.ToTarget, true) {
		return Catalog{}, invalid("activation_current_mismatch")
	}
	journal, exists, err = root.activationReadRegular("journal.json", maxManifestBytes)
	if err != nil {
		return Catalog{}, err
	}
	if !exists {
		return Catalog{}, invalid("activation_journal_missing")
	}
	if bytes.Equal(journal, intent.BaseJournal) {
		if err := root.activationWriteReplaceExpected("journal.new", "journal.json", intent.BaseJournal, intent.PlannedJournal); err != nil {
			return Catalog{}, err
		}
	} else if !bytes.Equal(journal, intent.PlannedJournal) {
		return Catalog{}, invalid("activation_journal_mismatch")
	}
	if err := store.activationVerifyFinal(root, planned, available, intent.PlannedJournal); err != nil {
		return Catalog{}, err
	}
	if err := root.activationRemoveExact("transaction.json", transactionRaw); err != nil {
		return Catalog{}, err
	}
	if err := store.activationVerifySteady(root, planned, available, intent.PlannedJournal); err != nil {
		return Catalog{}, err
	}
	return planned, nil
}

func (store *ActivationStore) activationRequireBase(root *activationLockedRoot, expected Catalog, available []Release) ([]byte, error) {
	if err := activationRequireNoTemps(root); err != nil {
		return nil, err
	}
	_, pending, err := root.activationReadRegular("transaction.json", maxActivationTransactionBytes)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, invalid("activation_transaction_pending")
	}
	journal, exists, err := root.activationReadRegular("journal.json", maxManifestBytes)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, invalid("activation_journal_missing")
	}
	restored, err := activationRestoreJournal(journal, available, store.lifecycleTrust, store.minimumRevision)
	if err != nil {
		return nil, err
	}
	if !activationCatalogEqual(restored, expected) {
		return nil, invalid("activation_catalog_mismatch")
	}
	if err := store.activationValidatePointer(root, restored); err != nil {
		return nil, err
	}
	return journal, nil
}

func (store *ActivationStore) activationVerifyFinal(root *activationLockedRoot, expected Catalog, available []Release, expectedJournal []byte) error {
	journal, exists, err := root.activationReadRegular("journal.json", maxManifestBytes)
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(journal, expectedJournal) {
		return invalid("activation_journal_mismatch")
	}
	restored, err := activationRestoreJournal(journal, available, store.lifecycleTrust, store.minimumRevision)
	if err != nil {
		return err
	}
	if !activationCatalogEqual(restored, expected) {
		return invalid("activation_catalog_mismatch")
	}
	return store.activationValidatePointer(root, restored)
}

func (store *ActivationStore) activationVerifySteady(root *activationLockedRoot, expected Catalog, available []Release, expectedJournal []byte) error {
	if err := store.activationVerifyFinal(root, expected, available, expectedJournal); err != nil {
		return err
	}
	transactionExists, err := root.activationEntryExists("transaction.json")
	if err != nil {
		return err
	}
	if transactionExists {
		return invalid("activation_transaction_pending")
	}
	return activationRequireNoTemps(root)
}

func (store *ActivationStore) activationValidatePointer(root *activationLockedRoot, catalog Catalog) error {
	expectedID, expectedTarget := activationCatalogCurrent(catalog, store.transportProfileID)
	actualTarget, exists, err := root.activationReadCurrent()
	if err != nil {
		return err
	}
	if expectedID == "" {
		if exists {
			return invalid("activation_current_mismatch")
		}
		return nil
	}
	if !exists {
		return invalid("activation_current_mismatch")
	}
	actualID, err := activationReleaseIDFromTarget(actualTarget)
	if err != nil || actualTarget != expectedTarget || actualID != expectedID {
		return invalid("activation_current_mismatch")
	}
	expected, ok := catalog.Get(expectedID)
	if !ok {
		return invalid("activation_current_mismatch")
	}
	identity, err := root.activationInspectSealedRelease(expectedID, store.evidenceTrust, nil)
	if err != nil {
		return err
	}
	return activationMatchSealed(identity, expected)
}

func (store *ActivationStore) activationSignIntent(intent activationIntent) ([]byte, error) {
	payload := activationSignaturePayload{
		SchemaVersion: activationTransactionSchemaVersion,
		Purpose:       activationTransactionPurpose, StoreID: store.storeID,
		KeyID: store.lifecycleSigner.KeyID, Intent: intent,
	}
	canonicalPayload, err := marshalCanonical(payload)
	if err != nil {
		return nil, invalid("activation_transaction_encode")
	}
	signed := signedActivationTransaction{
		SchemaVersion: payload.SchemaVersion, Purpose: payload.Purpose,
		StoreID: payload.StoreID, KeyID: payload.KeyID, Intent: payload.Intent,
		Signature: hex.EncodeToString(ed25519.Sign(store.lifecycleSigner.PrivateKey, canonicalPayload)),
	}
	raw, err := marshalCanonical(signed)
	if err != nil || len(raw) == 0 || len(raw) > maxActivationTransactionBytes {
		return nil, invalid("activation_transaction_encode")
	}
	return raw, nil
}

func (store *ActivationStore) activationVerifyIntent(raw []byte, available []Release) (activationIntent, Catalog, Catalog, error) {
	if len(raw) == 0 || len(raw) > maxActivationTransactionBytes {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_invalid")
	}
	var signed signedActivationTransaction
	if err := decodeCanonicalJSON(raw, &signed); err != nil ||
		signed.SchemaVersion != activationTransactionSchemaVersion ||
		signed.Purpose != activationTransactionPurpose || signed.StoreID != store.storeID ||
		!validID(signed.KeyID) {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_invalid")
	}
	publicKey, ok := store.lifecycleTrust[signed.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_key_untrusted")
	}
	signature, err := hex.DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		signed.Signature != strings.ToLower(signed.Signature) {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_signature_invalid")
	}
	payload := activationSignaturePayload{
		SchemaVersion: signed.SchemaVersion, Purpose: signed.Purpose,
		StoreID: signed.StoreID, KeyID: signed.KeyID, Intent: signed.Intent,
	}
	canonicalPayload, err := marshalCanonical(payload)
	if err != nil || !ed25519.Verify(publicKey, canonicalPayload, signature) {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_signature_invalid")
	}
	intent := signed.Intent
	evidenceTrustSHA256, trustErr := store.evidenceTrust.SHA256()
	admissionTime, timeErr := time.Parse(time.RFC3339Nano, intent.AdmissionTime)
	if trustErr != nil || intent.TransportProfileID != store.transportProfileID ||
		(intent.Operation != "publish" && intent.Operation != "rollback") ||
		intent.BaseRevision == 0 || intent.PlannedRevision <= intent.BaseRevision ||
		len(intent.BaseJournal) == 0 || len(intent.BaseJournal) > maxManifestBytes ||
		len(intent.PlannedJournal) == 0 || len(intent.PlannedJournal) > maxManifestBytes ||
		!equalDigest(intent.BaseJournalSHA256, digestBytes(intent.BaseJournal)) ||
		!equalDigest(intent.PlannedJournalSHA256, digestBytes(intent.PlannedJournal)) ||
		!validID(intent.ToReleaseID) || !validSHA256(intent.ManifestSHA256) ||
		!validSHA256(intent.EvidenceTrustSHA256) ||
		!equalDigest(intent.EvidenceTrustSHA256, evidenceTrustSHA256) ||
		intent.ToTarget != activationTarget(intent.ToReleaseID) ||
		timeErr != nil || admissionTime.Location() != time.UTC ||
		admissionTime.Format(time.RFC3339Nano) != intent.AdmissionTime {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_binding_mismatch")
	}
	if intent.FromReleaseID == "" {
		if intent.FromTarget != "" {
			return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_binding_mismatch")
		}
	} else if !validID(intent.FromReleaseID) || intent.FromTarget != activationTarget(intent.FromReleaseID) {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_binding_mismatch")
	}
	if intent.Operation == "publish" {
		if !activationSafeStagingName(intent.StagingName) || intent.StagingName == intent.ToReleaseID ||
			intent.ToReleaseID == intent.FromReleaseID {
			return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_binding_mismatch")
		}
	} else if intent.StagingName != "" || intent.FromReleaseID == "" || intent.ToReleaseID == intent.FromReleaseID {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_binding_mismatch")
	}
	base, err := activationRestoreJournal(intent.BaseJournal, available, store.lifecycleTrust, 1)
	if err != nil {
		return activationIntent{}, Catalog{}, Catalog{}, err
	}
	planned, err := activationRestoreJournal(intent.PlannedJournal, available, store.lifecycleTrust, store.minimumRevision)
	if err != nil {
		return activationIntent{}, Catalog{}, Catalog{}, err
	}
	if base.Revision() != intent.BaseRevision || planned.Revision() != intent.PlannedRevision {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transaction_binding_mismatch")
	}
	fromID, fromTarget := activationCatalogCurrent(base, store.transportProfileID)
	if fromID != intent.FromReleaseID || fromTarget != intent.FromTarget {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transition_invalid")
	}
	var recomputed Catalog
	if intent.Operation == "publish" {
		candidate, ok := activationAvailableRelease(available, intent.ToReleaseID)
		if !ok || candidate.State() != Candidate ||
			candidate.Manifest().TransportProfileID != store.transportProfileID ||
			!equalDigest(candidate.ManifestSHA256(), intent.ManifestSHA256) ||
			!equalDigest(candidate.Manifest().EvidenceTrustSHA256, intent.EvidenceTrustSHA256) {
			return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_candidate_mismatch")
		}
		withCandidate, err := base.AddCandidate(candidate)
		if err != nil {
			return activationIntent{}, Catalog{}, Catalog{}, err
		}
		recomputed, err = withCandidate.Publish(intent.ToReleaseID)
		if err != nil {
			return activationIntent{}, Catalog{}, Catalog{}, err
		}
	} else {
		selectedID := ""
		recomputed, selectedID, err = base.Rollback(intent.FromReleaseID)
		if err != nil {
			return activationIntent{}, Catalog{}, Catalog{}, err
		}
		if selectedID != intent.ToReleaseID {
			return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transition_invalid")
		}
		selected, ok := base.Get(selectedID)
		if !ok || !equalDigest(selected.ManifestSHA256(), intent.ManifestSHA256) ||
			!equalDigest(selected.Manifest().EvidenceTrustSHA256, intent.EvidenceTrustSHA256) {
			return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transition_invalid")
		}
	}
	if !activationCatalogEqual(recomputed, planned) {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transition_invalid")
	}
	toID, toTarget := activationCatalogCurrent(planned, store.transportProfileID)
	if toID != intent.ToReleaseID || toTarget != intent.ToTarget {
		return activationIntent{}, Catalog{}, Catalog{}, invalid("activation_transition_invalid")
	}
	return intent, base, planned, nil
}

func activationRestoreJournal(raw []byte, available []Release, trust LifecycleTrust, minimumRevision uint64) (Catalog, error) {
	var signed signedLifecycleJournal
	if err := decodeCanonicalJSON(raw, &signed); err != nil {
		return Catalog{}, invalid("journal_invalid")
	}
	index, err := validateRestoreReleases(available)
	if err != nil {
		return Catalog{}, err
	}
	selected := make([]Release, 0, len(signed.Journal.Entries))
	for _, entry := range signed.Journal.Entries {
		value, exists := index[entry.ReleaseID]
		if !exists {
			return Catalog{}, invalid("journal_binding_mismatch")
		}
		selected = append(selected, value)
	}
	return NewCatalog().Restore(raw, selected, trust, minimumRevision)
}

func activationCatalogEqual(left, right Catalog) bool {
	if validateCatalog(left) != nil || validateCatalog(right) != nil ||
		left.revision != right.revision || len(left.releases) != len(right.releases) ||
		len(left.predecessors) != len(right.predecessors) {
		return false
	}
	for id, leftRelease := range left.releases {
		rightRelease, ok := right.releases[id]
		if !ok || leftRelease.state != rightRelease.state ||
			!equalDigest(leftRelease.manifestSHA256, rightRelease.manifestSHA256) {
			return false
		}
	}
	for id, predecessor := range left.predecessors {
		if right.predecessors[id] != predecessor {
			return false
		}
	}
	return true
}

func activationCatalogReleases(catalog Catalog) []Release {
	values := make([]Release, 0, len(catalog.releases))
	for _, value := range catalog.releases {
		values = append(values, cloneRelease(value))
	}
	return values
}

func activationAvailableRelease(available []Release, releaseID string) (Release, bool) {
	for _, value := range available {
		if value.Manifest().ReleaseID == releaseID {
			return cloneRelease(value), true
		}
	}
	return Release{}, false
}

func activationCatalogCurrent(catalog Catalog, profileID string) (string, string) {
	current, ok := catalog.CurrentForProfile(profileID)
	if !ok {
		return "", ""
	}
	id := current.Manifest().ReleaseID
	return id, activationTarget(id)
}

func activationTarget(releaseID string) string {
	return "releases/" + releaseID
}

func activationReleaseIDFromTarget(target string) (string, error) {
	if target == "" || path.IsAbs(target) || path.Clean(target) != target ||
		strings.Contains(target, "\\") || strings.Count(target, "/") != 1 ||
		!strings.HasPrefix(target, "releases/") {
		return "", invalid("activation_current_invalid")
	}
	releaseID := strings.TrimPrefix(target, "releases/")
	if !validID(releaseID) || activationTarget(releaseID) != target {
		return "", invalid("activation_current_invalid")
	}
	return releaseID, nil
}

func activationPointerEquals(actual string, actualExists bool, expected string, expectedExists bool) bool {
	return actualExists == expectedExists && (!actualExists || actual == expected)
}

func (store *ActivationStore) activationStagingName(stagingDir string) (string, error) {
	if strings.TrimSpace(stagingDir) == "" || !filepath.IsAbs(stagingDir) ||
		filepath.Clean(stagingDir) != stagingDir {
		return "", invalid("activation_staging_invalid")
	}
	releasesRoot := filepath.Join(store.root, "releases")
	relative, err := filepath.Rel(releasesRoot, stagingDir)
	if err != nil || relative != filepath.Base(relative) || !activationSafeStagingName(relative) ||
		filepath.Join(releasesRoot, relative) != stagingDir {
		return "", invalid("activation_staging_invalid")
	}
	return relative, nil
}

func activationSafeStagingName(value string) bool {
	return validID(value) && strings.HasPrefix(value, "staging-") &&
		value == filepath.Base(value) && !strings.ContainsAny(value, "/\\")
}

func activationMatchSealed(identity sealedReleaseIdentity, expected Release) error {
	manifest := expected.Manifest()
	if identity.Manifest.ReleaseID != manifest.ReleaseID ||
		identity.Manifest.TransportProfileID != manifest.TransportProfileID ||
		!equalDigest(identity.ManifestSHA256, expected.ManifestSHA256()) {
		return invalid("activation_release_binding_mismatch")
	}
	return nil
}

func activationRequireNoTemps(root *activationLockedRoot) error {
	for _, name := range []string{"transaction.new", "current.new", "journal.new"} {
		exists, err := root.activationEntryExists(name)
		if err != nil {
			return err
		}
		if exists {
			return invalid("activation_transient_present")
		}
	}
	return nil
}

func (store *ActivationStore) activationNow() (time.Time, error) {
	value := store.now()
	if value.IsZero() {
		return time.Time{}, invalid("activation_time_invalid")
	}
	return value.UTC(), nil
}

func (store *ActivationStore) activationCallPhase(phase ActivationPhase) error {
	if store.phaseHook == nil {
		return nil
	}
	if err := store.phaseHook(phase); err != nil {
		return invalid("activation_interrupted")
	}
	return nil
}
