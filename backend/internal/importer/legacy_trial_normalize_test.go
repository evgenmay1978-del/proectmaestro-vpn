package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func nativeTrialSnapshotFixture(t *testing.T, raw []byte, parent *Snapshot) (Snapshot, TrialImportProtection) {
	t.Helper()
	base := validatedTrialProtectionFixture(t, 1)
	snapshot := Snapshot{FormatVersion: 2, SnapshotKind: "full", CapturedAt: time.Unix(1_600_000, 0).UTC(), ClusterHMACKeySHA256: sha256Hex(bytes.Repeat([]byte{0x73}, 32)), SourceHashes: map[string]string{"legacy:trials:present-unconverted": sha256Hex(raw)}}
	if parent != nil {
		snapshot.SnapshotKind = "delta"
		snapshot.ParentSourceDigest = digestSnapshot(*parent)
		snapshot.CapturedAt = parent.CapturedAt.Add(time.Second)
	}
	salt := []byte("synthetic-restart-legacy-trial-salt")
	if err := normalizeLegacyTrialSource(&snapshot, &LegacyTrialSource{RawJSON: raw, Salt: salt}, base.box, parent); err != nil {
		t.Fatal(err)
	}
	protection, err := ValidateSnapshotProtection(ProtectionFromSnapshot(snapshot, parent), base.box, bytes.Repeat([]byte{0x73}, 32), salt)
	if err != nil || protection == nil {
		t.Fatalf("native source protection: %v", err)
	}
	return snapshot, *protection
}

func TestNativeTrialProducerPreservesBothMapsAndExactRawAudit(t *testing.T) {
	hash := strings.Repeat("a", 64)
	raw := []byte("{\n  \"redeemed_anchors\": {\"" + hash + "\":\"OriginalLogin\"}, \"redeemed_drm\": {\"" + hash + "\":\"OtherLogin\"},\n \"audit\": [{\"at\":\"2026-01-01T00:00:00Z\",\"login\":\"OriginalLogin\",\"ip_net\":\"synthetic-private-audit\",\"future_evidence\":7}]\n}\n")
	snapshot, protection := nativeTrialSnapshotFixture(t, raw, nil)
	if len(snapshot.Trials) != 2 || snapshot.Trials[0].IdentityKind != "legacy-anchor-v1" || snapshot.Trials[1].IdentityKind != "legacy-drm-v1" {
		t.Fatal("anchor and DRM namespaces collapsed")
	}
	for _, row := range snapshot.Trials {
		if row.LegacyAnchorHMAC != hash || row.CurrentHMAC != "" || !row.Used || row.ExpiresAtUnix != 0 {
			t.Fatal("producer invented a current identity or expiry")
		}
	}
	opened, err := openTrialEvidence(protection.box, snapshot.EncryptedSecrets[0])
	if err != nil || !bytes.Equal(opened, raw) {
		t.Fatal("audit/source bytes changed")
	}
	encoded, _ := json.Marshal(snapshot)
	for _, plain := range []string{"OriginalLogin", "OtherLogin", "synthetic-private-audit"} {
		if bytes.Contains(encoded, []byte(plain)) {
			t.Fatal("snapshot exposed source identity or audit")
		}
	}
	if snapshot.SourceHashes[legacyTrialConvertedSource] != sha256Hex(raw) || snapshot.SourceHashes["legacy:trials:present-unconverted"] != "" {
		t.Fatal("conversion provenance missing")
	}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("native snapshot plan blocked: %v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil || len(operations) != 3 {
		t.Fatal("real planner omitted used rows or source evidence")
	}
}

func TestNativeTrialProducerEmptyLedgerStillCarriesSaltAndEvidence(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{}`), []byte(`{"redeemed_anchors":{},"redeemed_drm":{},"audit":[]}`), []byte(`{"redeemed_anchors":null,"audit":null}`)} {
		snapshot, protection := nativeTrialSnapshotFixture(t, raw, nil)
		if len(snapshot.Trials) != 0 || len(snapshot.EncryptedSecrets) != 1 || !ProtectionFromSnapshot(snapshot).HasTrials || protection.ledger == nil {
			t.Fatal("empty present source disappeared")
		}
		plan, report := Plan(snapshot, testPlanOptions())
		if len(report.Blockers) != 0 {
			t.Fatalf("empty source plan: %v", report.Blockers)
		}
		operations, err := planOperations(plan)
		if err != nil || len(operations) != 1 || operations[0].Entity != "encrypted_secret" {
			t.Fatal("empty source lost its apply operation")
		}
		store := &RQLiteApplyStore{trialProtection: &protection, now: func() time.Time { return time.Unix(1_600_000, 0) }}
		statements, err := store.encryptedSecretStatements(ApplyBatch{RunID: "fixture", PlanDigest: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64)}, operations[0])
		if err != nil || len(statements) != 3 || statements[0].Args[0] != legacyTrialSaltSecretID {
			t.Fatal("empty evidence did not include protected salt in its transaction")
		}
		store.trialProtection = nil
		if _, err := store.encryptedSecretStatements(ApplyBatch{}, operations[0]); err == nil {
			t.Fatal("unverified evidence authorized salt persistence")
		}
	}
}

func TestNativeTrialProducerDeltaRetainsUsedRowsAndPriorCiphertext(t *testing.T) {
	hashA, hashB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	parent, _ := nativeTrialSnapshotFixture(t, []byte(`{"redeemed_anchors":{"`+hashA+`":"ExactLogin"},"audit":[{"note":"first"}]}`), nil)
	delta, proof := nativeTrialSnapshotFixture(t, []byte(`{"redeemed_anchors":{"`+hashA+`":"ExactLogin"},"redeemed_drm":{"`+hashB+`":"SecondLogin"},"audit":[{"note":"next"}]}`), &parent)
	if len(delta.Trials) != 2 || len(delta.EncryptedSecrets) != 2 {
		t.Fatal("delta omitted used identity or historical raw source")
	}
	prior := parent.EncryptedSecrets[0]
	if proof.ledger.evidence[prior.SecretID] != prior {
		t.Fatal("delta resealed immutable prior evidence")
	}
	base := validatedTrialProtectionFixture(t, 1)
	for _, raw := range [][]byte{[]byte(`{}`), []byte(`{"redeemed_anchors":{"` + hashA + `":"changed-login"}}`)} {
		snapshot := Snapshot{SourceHashes: map[string]string{"legacy:trials:present-unconverted": sha256Hex(raw)}}
		if normalizeLegacyTrialSource(&snapshot, &LegacyTrialSource{RawJSON: raw, Salt: []byte("synthetic-restart-legacy-trial-salt")}, base.box, &parent) == nil {
			t.Fatal("delta lost or rebound an already-used hash")
		}
	}
	plan, report := Plan(delta, PlanOptions{Namespace: "maestro-legacy-v1", ParentSnapshot: &parent, AppliedParentDigest: digestSnapshot(parent)})
	if len(report.Blockers) != 0 || plan.ParentSourceDigest != digestSnapshot(parent) {
		t.Fatal("actual planner rejected bound cumulative delta")
	}
}

func TestNativeTrialProducerRejectsLossyJSONAndScopeDowngrade(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, raw := range []string{"", "null", `[]`, `{"unknown":true}`, `{"redeemed_anchors":{},"redeemed_anchors":{}}`, `{"Redeemed_anchors":{}}`, `{"redeemed_anchors":{"` + hash + `":"one","` + hash + `":"two"}}`, `{"redeemed_drm":{"bad":"login"}}`, `{"redeemed_drm":{"` + hash + `":""}}`, `{"audit":[null]}`, `{"audit":[{"note":"\ud800"}]}`} {
		if _, err := decodeNativeTrialLedger([]byte(raw)); err == nil {
			t.Fatal("lossy native source accepted")
		}
	}
	parent, _ := nativeTrialSnapshotFixture(t, []byte(`{}`), nil)
	if normalizeLegacyTrialSource(&Snapshot{}, nil, nil, &parent) == nil {
		t.Fatal("converted parent was silently downgraded")
	}
}

func TestNativeTrialComposerKeepsCustomerPreparationGate(t *testing.T) {
	raw, capture, options, box := legacyNormalizeFixture(t)
	ledger := []byte(`{"redeemed_anchors":{},"redeemed_drm":{},"audit":[{"note":"retained"}]}`)
	options.Sources["trials"] = LegacySourcePresence{State: "present", SHA256: sha256Hex(ledger)}
	options.TrialSource = &LegacyTrialSource{RawJSON: ledger, Salt: []byte("actual-salt-with-newline\n")}
	snapshot := normalizeFixture(t, raw, capture, options, box)
	if snapshot.SourceHashes["scope:"+LegacyCustomerPreparationScope] == "" || snapshot.SourceHashes["legacy:orders:present-unconverted"] == "" || !ProtectionFromSnapshot(snapshot).HasTrials {
		t.Fatal("trial composition changed scope or omitted empty salt")
	}
	parent := snapshot
	options.Parent = &parent
	capture.CapturedAt = capture.CapturedAt.Add(time.Second)
	capture.CompletedAt = capture.CompletedAt.Add(time.Second)
	options.Now = options.Now.Add(time.Second)
	delta := normalizeFixture(t, raw, capture, options, box)
	if delta.ParentSourceDigest != digestSnapshot(parent) {
		t.Fatal("composed parent source binding changed")
	}
	for _, secret := range parent.EncryptedSecrets {
		if reservedTrialEvidence(secret) {
			found := false
			for _, next := range delta.EncryptedSecrets {
				found = found || next == secret
			}
			if !found {
				t.Fatal("unchanged raw source was resealed")
			}
		}
	}
}

func TestNativeTrialProofRejectsForgedOperationBeforeWrites(t *testing.T) {
	snapshot, protection := nativeTrialSnapshotFixture(t, []byte(`{}`), nil)
	secret := snapshot.EncryptedSecrets[0]
	secret.CiphertextB64 = secret.CiphertextB64 + "AAAA"
	raw, _ := json.Marshal(secret)
	operation := ApplyOperation{Entity: "encrypted_secret", Key: secret.SecretID, CanonicalJSON: raw}
	store := &RQLiteApplyStore{trialProtection: &protection}
	if _, err := store.withDurableTrialProtection(context.Background(), ApplyBatch{Operations: []ApplyOperation{operation}}); err == nil {
		t.Fatal("forged evidence reached database boundary")
	}
	if _, err := store.BeginOrResume(context.Background(), ApplyRun{SourceDigest: strings.Repeat("f", 64)}); err == nil {
		t.Fatal("proof was reusable for a different source run")
	}
	clone := ProtectionFromSnapshot(snapshot)
	clone.SourceHashes[legacyTrialConvertedSource] = strings.Repeat("e", 64)
	if reflect.DeepEqual(clone.SourceHashes, snapshot.SourceHashes) {
		t.Fatal("protection aliases source hash map")
	}
}

func TestNativeTrialEvidenceAndSaltRejectLogicalDeletionBeforeWrites(t *testing.T) {
	snapshot, protection := nativeTrialSnapshotFixture(t, []byte(`{}`), nil)
	for _, id := range []string{snapshot.EncryptedSecrets[0].SecretID, legacyTrialSaltSecretID} {
		for _, validated := range []bool{false, true} {
			deletion := PlannedDelete{Entity: "encrypted_secret", SourceKey: id, TargetID: id, ExpectedPriorDigest: strings.Repeat("a", 64)}
			raw, err := json.Marshal(deletion)
			if err != nil {
				t.Fatal(err)
			}
			operation := ApplyOperation{Entity: "encrypted_secret", Key: id, Tombstone: true, CanonicalJSON: raw}
			batch := ApplyBatch{RunID: "native-trial-delete", PlanDigest: strings.Repeat("b", 64), Operations: []ApplyOperation{operation}}
			batch.Digest = digestBatch(batch.Operations)
			db := &applyStoreRQLite{}
			store := &RQLiteApplyStore{db: db, now: func() time.Time { return time.Unix(1_600_000, 0) }}
			if validated {
				store.trialProtection = &protection
			}
			if _, err := store.CommitBatch(context.Background(), batch); err == nil {
				t.Fatal("native trial evidence or salt accepted a logical deletion")
			}
			if len(db.requests) != 0 || db.queryCalls != 1 {
				t.Fatal("reserved deletion crossed the receipt-only preflight boundary")
			}
			if statements, err := store.encryptedSecretDeleteStatements(batch, operation); err == nil || len(statements) != 0 {
				t.Fatal("direct delete builder produced SQL for reserved trial material")
			}
		}
	}
}
