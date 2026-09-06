package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func runtimeDeltaFixture(t *testing.T, fixture runtimeDomainFixture, parent Snapshot, advanceCustomer bool) ([]byte, LegacyXUICapture, LegacyNormalizeOptions) {
	t.Helper()
	raw := append([]byte(nil), fixture.rawCustomers...)
	if advanceCustomer {
		var customers []legacystore.Customer
		if json.Unmarshal(raw, &customers) != nil {
			t.Fatal("synthetic customers")
		}
		customers[0].Expires = customers[0].Expires.Add(time.Hour)
		raw = marshalNormalizeFixture(t, customers)
	}
	capture := fixture.capture
	capture.CapturedAt = capture.CapturedAt.Add(10 * time.Second)
	capture.CompletedAt = capture.CompletedAt.Add(10 * time.Second)
	capture.CustomersSHA256 = sha256Hex(raw)
	options := fixture.normalizeOptions
	options.Parent = &parent
	options.Now = fixture.options.Now.Add(10 * time.Second)
	options.Sources = map[string]LegacySourcePresence{}
	for key, value := range fixture.normalizeOptions.Sources {
		options.Sources[key] = value
	}
	capsule := fixture.capsule
	capsule.CapturedAt = capsule.CapturedAt.Add(10 * time.Second)
	capsule.CompletedAt = capsule.CompletedAt.Add(10 * time.Second)
	capsule.ProcessBefore.CustomersSHA256, capsule.ProcessAfter.CustomersSHA256 = sha256Hex(raw), sha256Hex(raw)
	capsuleRaw := marshalNormalizeFixture(t, capsule)
	for _, key := range []string{"settings", "principals"} {
		options.Sources[key] = LegacySourcePresence{State: "present", SHA256: sha256Hex(capsuleRaw)}
	}
	absence := LegacyRuntimeOTAAbsence{SchemaVersion: 1, ObservedAt: options.Now, Process: capsule.ProcessBefore, Directory: "/var/lib/maestro/update", State: "absent", RuntimeCapsuleSHA256: sha256Hex(capsuleRaw)}
	options.RuntimeSource = &LegacyRuntimeSource{RawCapsule: capsuleRaw, RawOTAAbsence: marshalNormalizeFixture(t, absence)}
	return raw, capture, options
}

func completeNativeRuntimeFixture(t *testing.T) runtimeDomainFixture {
	t.Helper()
	fixture := newRuntimeDomainFixture(t)
	customers, err := DecodeLegacyCustomers(fixture.rawCustomers)
	if err != nil {
		t.Fatal(err)
	}
	orders := marshalNormalizeFixture(t, []legacyorder.Order{{ID: "native-complete-order", Tariff: "1m", Days: 30, Rub: 300, Code: "native-complete", Login: customers[0].Login, Status: "pending", CreatedAt: fixture.options.Now.Add(-time.Hour)}})
	trials := []byte(`{"redeemed_anchors":{},"redeemed_drm":{},"audit":[]}`)
	fixture.normalizeOptions.OrderSource = &LegacyOrderSource{RawJSON: orders}
	fixture.normalizeOptions.TrialSource = &LegacyTrialSource{RawJSON: trials, Salt: []byte("synthetic-native-complete-trial-salt")}
	fixture.normalizeOptions.Sources["orders"] = LegacySourcePresence{State: "present", SHA256: sha256Hex(orders)}
	fixture.normalizeOptions.Sources["trials"] = LegacySourcePresence{State: "present", SHA256: sha256Hex(trials)}
	fixture.normalizeOptions.CompleteNativeImport = true
	return fixture
}

func TestNativeCompleteImportRequiresAllAuthenticatedDomainsAndExplicitParentMode(t *testing.T) {
	fixture := completeNativeRuntimeFixture(t)
	parent := runtimeDomainNormalizedSnapshot(t, fixture)
	if _, preparation := parent.SourceHashes["scope:"+LegacyCustomerPreparationScope]; preparation || !nativeImportSourcesComplete(parent.SourceHashes) {
		t.Fatal("complete source retained apply prohibition")
	}
	raw, capture, options := runtimeDeltaFixture(t, fixture, parent, true)
	delta := normalizeFixture(t, raw, capture, options, fixture.box)
	if _, preparation := delta.SourceHashes["scope:"+LegacyCustomerPreparationScope]; preparation {
		t.Fatal("complete delta reverted to preparation")
	}
	options.CompleteNativeImport = false
	if _, err := NormalizeLegacyCustomers(raw, capture, fixture.box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
		t.Fatal("parent completion mode changed implicitly")
	}
	preparationFixture := fixture
	preparationFixture.normalizeOptions.CompleteNativeImport = false
	prepared := runtimeDomainNormalizedSnapshot(t, preparationFixture)
	if _, exists := prepared.SourceHashes["scope:"+LegacyCustomerPreparationScope]; !exists {
		t.Fatal("default preparation mode changed")
	}
	raw, capture, options = runtimeDeltaFixture(t, fixture, prepared, true)
	if _, err := NormalizeLegacyCustomers(raw, capture, fixture.box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
		t.Fatal("prepared parent silently upgraded")
	}
	for _, mode := range []string{"orders", "trials", "runtime", "ota"} {
		t.Run(mode, func(t *testing.T) {
			raw, capture, options := runtimeDeltaFixture(t, fixture, parent, false)
			switch mode {
			case "orders":
				options.OrderSource = nil
			case "trials":
				options.TrialSource = nil
			case "runtime":
				options.RuntimeSource = nil
			case "ota":
				options.RuntimeSource.RawOTAAbsence = nil
			}
			if _, err := NormalizeLegacyCustomers(raw, capture, fixture.box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
				t.Fatal("incomplete converter set lifted preparation gate")
			}
		})
	}
	for _, key := range []string{"legacy:settings:present-unconverted", "legacy:other:present-unconverted", "unknown-source"} {
		hashes := map[string]string{}
		for k, v := range parent.SourceHashes {
			hashes[k] = v
		}
		hashes[key] = strings.Repeat("a", 64)
		if nativeImportSourcesComplete(hashes) {
			t.Fatal("unsupported source reported complete")
		}
	}
}

func TestNativeRuntimeDeltaRetainsSourceAndAdvancesCustomer(t *testing.T) {
	fixture := newRuntimeDomainFixture(t)
	parent := runtimeDomainNormalizedSnapshot(t, fixture)
	raw, capture, options := runtimeDeltaFixture(t, fixture, parent, true)
	delta := normalizeFixture(t, raw, capture, options, fixture.box)
	if !reflect.DeepEqual(parent.Settings, delta.Settings) || !reflect.DeepEqual(parent.Principals, delta.Principals) {
		t.Fatal("delta rewrote initial runtime settings, membership or principal")
	}
	if reflect.DeepEqual(parent.Customers, delta.Customers) || delta.SourceHashes["customers"] == parent.SourceHashes["customers"] {
		t.Fatal("honest customer expiry delta was lost")
	}
	if delta.SourceHashes["legacy:settings:runtime-converted-v1"] != parent.SourceHashes["legacy:settings:runtime-converted-v1"] || delta.SourceHashes["legacy:ota:absent-v1"] != parent.SourceHashes["legacy:ota:absent-v1"] {
		t.Fatal("initial source identity changed")
	}
	proof, err := validateNativeRuntimeProof(ProtectionFromSnapshot(delta, &parent), fixture.box)
	if err != nil {
		t.Fatal(err)
	}
	oldProof, err := validateNativeRuntimeProof(ProtectionFromSnapshot(parent), fixture.box)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := runtimeSecretMap(delta.EncryptedSecrets)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range parent.EncryptedSecrets {
		if _, retained := oldProof[secret.SecretID]; retained && secrets[secret.SecretID] != secret {
			t.Fatal("delta resealed authenticated history")
		}
	}
	if len(proof) != len(oldProof)+2 {
		t.Fatal("fresh runtime and OTA evidence are not separately retained")
	}
	for _, check := range []struct {
		id  string
		raw []byte
	}{
		{"legacy-runtime-source-v1:" + sha256Hex(options.RuntimeSource.RawCapsule), options.RuntimeSource.RawCapsule},
		{"legacy-runtime-ota-source-v1:" + sha256Hex(options.RuntimeSource.RawOTAAbsence), options.RuntimeSource.RawOTAAbsence},
	} {
		plain, err := openRuntimeSource(fixture.box, secrets[check.id])
		if err != nil || !bytes.Equal(plain, check.raw) {
			t.Fatal("delta lost exact capture bytes")
		}
		zeroBytes(plain)
	}
	if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(delta, &parent), fixture.box); err != nil {
		t.Fatal(err)
	}
	planOptions := options.PlanOptions
	planOptions.ParentSnapshot, planOptions.AppliedParentDigest = &parent, delta.ParentSourceDigest
	if _, report := Plan(delta, planOptions); len(report.Blockers) != 0 {
		t.Fatal("historical member SHA blocked current stable identity")
	}
	// An authenticated capture is not a renewable lease: archival validation
	// remains valid after its explicit CLI freshness window has elapsed.
	delta.CapturedAt = delta.CapturedAt.Add(24 * time.Hour)
	if _, err := ValidateSnapshotProtection(ProtectionFromSnapshot(delta, &parent), fixture.box, bytes.Repeat([]byte{0x22}, 32), nil); err != nil {
		t.Fatal("persistent proof acquired an arbitrary expiry")
	}
}

func TestNativeRuntimeDeltaReusesUnchangedCaptureBytes(t *testing.T) {
	fixture := newRuntimeDomainFixture(t)
	parent := runtimeDomainNormalizedSnapshot(t, fixture)
	raw, capture, options := runtimeDeltaFixture(t, fixture, parent, false)
	// A later XUI/customer census may legitimately share the same still-fresh
	// process-bound runtime capture. Reuse the original source, including nonce.
	options.RuntimeSource.RawCapsule = marshalNormalizeFixture(t, fixture.capsule)
	options.RuntimeSource.RawOTAAbsence = marshalNormalizeFixture(t, LegacyRuntimeOTAAbsence{SchemaVersion: 1, ObservedAt: fixture.options.Now, Process: fixture.capsule.ProcessBefore, Directory: "/var/lib/maestro/update", State: "absent", RuntimeCapsuleSHA256: sha256Hex(options.RuntimeSource.RawCapsule)})
	for _, key := range []string{"settings", "principals"} {
		options.Sources[key] = LegacySourcePresence{State: "present", SHA256: sha256Hex(options.RuntimeSource.RawCapsule)}
	}
	delta := normalizeFixture(t, raw, capture, options, fixture.box)
	before, _ := validateNativeRuntimeProof(ProtectionFromSnapshot(parent), fixture.box)
	after, err := validateNativeRuntimeProof(ProtectionFromSnapshot(delta, &parent), fixture.box)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("same source was resealed or duplicated")
	}
}

func TestNativeRuntimeDeltaRejectsSourceLossAndTargetSubstitution(t *testing.T) {
	fixture := newRuntimeDomainFixture(t)
	parent := runtimeDomainNormalizedSnapshot(t, fixture)
	for _, mode := range []string{"missing-capture", "missing-ota", "changed-file", "changed-env", "changed-executable", "future", "stale", "wrong-customer-sha", "wrong-inventory", "new-ota-directory"} {
		t.Run(mode, func(t *testing.T) {
			raw, capture, options := runtimeDeltaFixture(t, fixture, parent, true)
			var capsule LegacyRuntimeCapsule
			if runtimeDomainDecode(options.RuntimeSource.RawCapsule, &capsule) != nil {
				t.Fatal("capsule")
			}
			switch mode {
			case "missing-capture":
				options.RuntimeSource = nil
			case "missing-ota":
				options.RuntimeSource.RawOTAAbsence = nil
			case "changed-file":
				file := capsule.Files["wb_token"]
				changed := []byte("different token")
				file.RawBase64 = base64.StdEncoding.EncodeToString(changed)
				file.SHA256 = sha256Hex(changed)
				capsule.Files["wb_token"] = file
			case "changed-env":
				capsule.Environment["MAESTRO_PANEL_PASSWORD_HASH"] = "different-env"
			case "changed-executable":
				capsule.ProcessBefore.ExecutableSHA256 = strings.Repeat("e", 64)
				capsule.ProcessAfter = capsule.ProcessBefore
			case "future":
				capsule.CompletedAt = options.Now.Add(time.Second)
			case "stale":
				options.MaxCaptureAge = time.Nanosecond
			case "wrong-customer-sha":
				capsule.ProcessBefore.CustomersSHA256 = strings.Repeat("e", 64)
				capsule.ProcessAfter = capsule.ProcessBefore
			case "wrong-inventory":
				options.Sources["settings"] = LegacySourcePresence{State: "absent"}
			case "new-ota-directory":
				var evidence LegacyRuntimeOTAAbsence
				if runtimeDomainDecode(options.RuntimeSource.RawOTAAbsence, &evidence) != nil {
					t.Fatal("OTA")
				}
				evidence.Directory = "/other/update"
				options.RuntimeSource.RawOTAAbsence = marshalNormalizeFixture(t, evidence)
			}
			if mode == "changed-file" || mode == "changed-env" || mode == "changed-executable" || mode == "future" || mode == "wrong-customer-sha" {
				options.RuntimeSource.RawCapsule = marshalNormalizeFixture(t, capsule)
				for _, key := range []string{"settings", "principals"} {
					options.Sources[key] = LegacySourcePresence{State: "present", SHA256: sha256Hex(options.RuntimeSource.RawCapsule)}
				}
				options.RuntimeSource.RawOTAAbsence = marshalNormalizeFixture(t, LegacyRuntimeOTAAbsence{SchemaVersion: 1, ObservedAt: options.Now, Process: capsule.ProcessBefore, Directory: "/var/lib/maestro/update", State: "absent", RuntimeCapsuleSHA256: sha256Hex(options.RuntimeSource.RawCapsule)})
			}
			if _, err := NormalizeLegacyCustomers(raw, capture, fixture.box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
				t.Fatal("runtime delta accepted changed or missing source")
			}
		})
	}
	raw, capture, options := runtimeDeltaFixture(t, fixture, parent, true)
	delta := normalizeFixture(t, raw, capture, options, fixture.box)
	for _, mode := range []string{"parent-missing", "marker-missing", "old-cipher-changed", "historical-member-changed", "archive-extra"} {
		t.Run(mode, func(t *testing.T) {
			var changed Snapshot
			if json.Unmarshal(marshalNormalizeFixture(t, delta), &changed) != nil {
				t.Fatal("clone")
			}
			proof := ProtectionFromSnapshot(changed, &parent)
			switch mode {
			case "parent-missing":
				proof.Parent = nil
			case "marker-missing":
				delete(proof.SourceHashes, "legacy:settings:runtime-converted-v1")
			case "old-cipher-changed":
				for i := range proof.EncryptedSecrets {
					if proof.EncryptedSecrets[i].SecretID == parent.Settings[0].SecretRef {
						proof.EncryptedSecrets[i].CiphertextB64 = base64.StdEncoding.EncodeToString([]byte("corrupt"))
					}
				}
			case "historical-member-changed":
				proof.Settings[0].Members[0].CustomerSHA256 = strings.Repeat("e", 64)
			case "archive-extra":
				secret := proof.EncryptedSecrets[0]
				secret.SecretID = "legacy-runtime-source-v1:" + strings.Repeat("f", 64)
				proof.EncryptedSecrets = append(proof.EncryptedSecrets, secret)
			}
			if _, err := ValidateProductionCustomerIdentities(proof, fixture.box); err == nil {
				t.Fatal("unbound runtime delta accepted")
			}
		})
	}
}
