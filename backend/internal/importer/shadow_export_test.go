package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func validShadowShapes() ShadowURLShapes {
	return ShadowURLShapes{
		Maestro: "maestro://subscription/{opaque-token}",
		Karing:   "https://example.invalid/karing/{opaque-token}",
	}
}

func fullShadowPlan(t *testing.T) ImportPlan {
	t.Helper()
	snapshot := decodeFixture(t, "orders-pending-credited.json")
	security := decodeFixture(t, "settings-principals-v1.json")
	security.Settings[1].PublicValueJSON = json.RawMessage(`{"enabled":true,"mode":"strict"}`)
	snapshot.SourceHashes["security"] = security.SourceHashes["settings"]
	snapshot.Settings = security.Settings
	snapshot.Principals = security.Principals
	snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, security.EncryptedSecrets...)
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("full shadow plan blockers: %#v", report.Blockers)
	}
	return plan
}

func reverseShadowPlanInput(plan *ImportPlan) {
	reverseShadowValues(plan.Customers)
	reverseShadowValues(plan.Orders)
	reverseShadowValues(plan.Settings)
	reverseShadowValues(plan.Principals)
	reverseShadowValues(plan.EncryptedSecrets)
	for index := range plan.Customers {
		reverseShadowValues(plan.Customers[index].ProtocolTags)
		reverseShadowValues(plan.Customers[index].NodeIDs)
	}
	for index := range plan.Principals {
		reverseShadowValues(plan.Principals[index].Roles)
	}
	for index := range plan.Settings {
		if plan.Settings[index].Key == "telegram" {
			plan.Settings[index].PublicValueJSON = json.RawMessage(`{"mode":"strict","enabled":true}`)
		}
	}
	plan.PlanDigest = Digest(*plan)
}

func reverseShadowValues[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func TestShadowFromPlanIsByteStableAndSecretFree(t *testing.T) {
	plan := fullShadowPlan(t)
	first, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	reverseShadowPlanInput(&plan)
	second, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	a, err := EncodeShadowExport(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeShadowExport(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("shadow encoding is order-dependent:\n%s\n%s", a, b)
	}
	for _, forbidden := range []string{
		"OrderOwner", "CaseSensitiveUser", "ciphertext_b64", "nonce_b64",
		"c3ludGhldGljLW9yZGVyLW93bmVy", "private-token", "MCRD-RQLITE-E2E",
	} {
		if bytes.Contains(a, []byte(forbidden)) {
			t.Fatalf("export leaked forbidden marker %q", forbidden)
		}
	}
	if len(first.Customers) != 1 || len(first.Orders) != 2 {
		t.Fatalf("shadow counts = customers:%d orders:%d", len(first.Customers), len(first.Orders))
	}
	customer := first.Customers[0]
	if customer.IdentityHMAC != plan.Customers[0].LoginKeyHMAC || customer.ExpiresAtUnix != 2_200_000 ||
		customer.Generation != 9 || strings.Join(customer.ProtocolTags, ",") != "anytls,hysteria2,naive,vless" ||
		strings.Join(customer.Nodes, ",") != "S1,S2,S3,S4" {
		t.Fatalf("shadow customer = %#v", customer)
	}
	if first.OTA.VersionCode != 154 || first.OTA.VersionName != "1.5.4" || first.OTA.APKSize != 12_345_678 ||
		first.OTA.APKSHA256 != strings.Repeat("5", 64) {
		t.Fatalf("shadow OTA = %#v", first.OTA)
	}
	if len(first.SettingsFingerprint) != 64 || len(first.PrincipalsFingerprint) != 64 {
		t.Fatalf("shadow fingerprints = %q / %q", first.SettingsFingerprint, first.PrincipalsFingerprint)
	}
}

func TestShadowFromPlanRejectsIncompleteOrAmbiguousInput(t *testing.T) {
	privateMarker := "private-shadow-marker"
	tests := []struct {
		name   string
		mutate func(*ImportPlan, *ShadowURLShapes)
	}{
		{"duplicate identity", func(plan *ImportPlan, _ *ShadowURLShapes) {
			duplicate := plan.Customers[0]
			duplicate.InternalID = strings.Repeat("a", 64)
			duplicate.SourceKey = "duplicate-customer"
			plan.Customers = append(plan.Customers, duplicate)
		}},
		{"malformed identity hmac", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.Customers[0].LoginKeyHMAC = privateMarker
		}},
		{"missing protocols", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.Customers[0].ProtocolTags = nil
		}},
		{"missing nodes", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.Customers[0].NodeIDs = nil
		}},
		{"invalid maestro shape", func(_ *ImportPlan, shapes *ShadowURLShapes) {
			shapes.Maestro = "maestro://subscription/" + privateMarker
		}},
		{"invalid karing shape", func(_ *ImportPlan, shapes *ShadowURLShapes) {
			shapes.Karing = "http://" + privateMarker + "/{opaque-token}"
		}},
		{"missing ota", func(plan *ImportPlan, _ *ShadowURLShapes) {
			filtered := plan.Settings[:0]
			for _, setting := range plan.Settings {
				if setting.Key != "ota" {
					filtered = append(filtered, setting)
				}
			}
			plan.Settings = filtered
		}},
		{"ambiguous ota", func(plan *ImportPlan, _ *ShadowURLShapes) {
			for _, setting := range plan.Settings {
				if setting.Key == "ota" {
					plan.Settings = append(plan.Settings, setting)
					return
				}
			}
		}},
		{"malformed ota", func(plan *ImportPlan, _ *ShadowURLShapes) {
			for index := range plan.Settings {
				if plan.Settings[index].Key == "ota" {
					plan.Settings[index].PublicValueJSON = json.RawMessage(`{"versionCode":154,"private":"` + privateMarker + `"}`)
				}
			}
		}},
		{"malformed setting secret binding", func(plan *ImportPlan, _ *ShadowURLShapes) {
			for index := range plan.Settings {
				if plan.Settings[index].Key == "telegram" {
					plan.Settings[index].SecretRef = "secret-order-owner"
				}
			}
		}},
		{"malformed principal secret binding", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.Principals[0].CredentialSecretRef = "secret-bot-token"
		}},
		{"non full plan", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.SnapshotKind = "delta"
		}},
		{"plan blockers", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.Blockers = []Blocker{{Code: "synthetic_blocker", SourceKey: privateMarker}}
		}},
		{"plan digest mismatch", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.PlanDigest = strings.Repeat("f", 64)
		}},
		{"invalid order state", func(plan *ImportPlan, _ *ShadowURLShapes) {
			plan.Orders[0].PaymentState = privateMarker
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := fullShadowPlan(t)
			shapes := validShadowShapes()
			tc.mutate(&plan, &shapes)
			if tc.name != "plan digest mismatch" && tc.name != "plan blockers" {
				plan.PlanDigest = Digest(plan)
			}
			_, err := ShadowFromPlan(plan, shapes)
			if !errors.Is(err, ErrShadowExportInvalid) {
				t.Fatalf("error = %v, want shadow export invalid", err)
			}
			if err != nil && strings.Contains(err.Error(), privateMarker) {
				t.Fatalf("error leaked private input: %v", err)
			}
		})
	}
}

func TestShadowFingerprintsBindCanonicalPublicAndProtectedMetadataOnly(t *testing.T) {
	plan := fullShadowPlan(t)
	baseline, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	changedPublic := plan
	changedPublic.Settings = append([]LegacySetting(nil), plan.Settings...)
	for index := range changedPublic.Settings {
		if changedPublic.Settings[index].Key == "telegram" {
			changedPublic.Settings[index].PublicValueJSON = json.RawMessage(`{"enabled":false,"mode":"strict"}`)
		}
	}
	changedPublic.PlanDigest = Digest(changedPublic)
	publicExport, err := ShadowFromPlan(changedPublic, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	if baseline.SettingsFingerprint == publicExport.SettingsFingerprint {
		t.Fatal("public setting change did not change fingerprint")
	}

	changedCiphertext := plan
	changedCiphertext.EncryptedSecrets = append([]LegacyEncryptedSecret(nil), plan.EncryptedSecrets...)
	changedCiphertext.EncryptedSecrets[0].CiphertextB64 = "private-token"
	changedCiphertext.PlanDigest = Digest(changedCiphertext)
	ciphertextExport, err := ShadowFromPlan(changedCiphertext, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	if baseline.SettingsFingerprint != ciphertextExport.SettingsFingerprint ||
		baseline.PrincipalsFingerprint != ciphertextExport.PrincipalsFingerprint {
		t.Fatal("ciphertext bytes changed public fingerprints")
	}
}

func TestEncodeShadowExportRevalidatesModel(t *testing.T) {
	export, err := ShadowFromPlan(fullShadowPlan(t), validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	export.SchemaVersion = 2
	if _, err := EncodeShadowExport(export); !errors.Is(err, ErrShadowExportInvalid) {
		t.Fatalf("EncodeShadowExport error = %v", err)
	}
}

func TestWriteShadowExportCreatesProtectedAtomicFileAndRefusesOverwrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("protected file mode contract is authoritative on Linux GitHub")
	}
	export, err := ShadowFromPlan(fullShadowPlan(t), validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "legacy-shadow.json")
	if err := WriteShadowExport(path, export); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("shadow file mode = %v", info.Mode())
	}
	want, err := EncodeShadowExport(export)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("written bytes differ:\n%s\n%s", got, want)
	}
	var decoded ShadowExport
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("written shadow JSON: %v", err)
	}
	if err := WriteShadowExport(path, export); !errors.Is(err, ErrShadowExportUnavailable) {
		t.Fatalf("overwrite error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, got) {
		t.Fatalf("existing export changed: %v", err)
	}
	temps, err := filepath.Glob(filepath.Join(directory, ".shadow-export-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary exports remain: %v / %v", temps, err)
	}
}
