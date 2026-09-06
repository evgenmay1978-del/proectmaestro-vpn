package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"golang.org/x/crypto/bcrypt"
)

func TestNormalizeCLICompleteNativeImportPublishesApplyEligibleAuthenticatedSource(t *testing.T) {
	fixture := newNormalizeCLIFixture(t)
	directory := filepath.Dir(fixture.output)
	raw, err := os.ReadFile(fixture.customers)
	if err != nil {
		t.Fatal(err)
	}
	var original []legacystore.Customer
	if json.Unmarshal(raw, &original) != nil || len(original) != 1 {
		t.Fatal("customer fixture")
	}
	customerRaw, err := json.Marshal(original[0])
	if err != nil {
		t.Fatal(err)
	}
	var customers []legacystore.Customer
	now := time.Now().UTC()
	capture := importer.LegacyXUICapture{SchemaVersion: 1, CapturedAt: now.Add(-3 * time.Second), CompletedAt: now.Add(-2 * time.Second)}
	for i, login := range vkturnconf.AllowedLogins() {
		var customer legacystore.Customer
		if json.Unmarshal(customerRaw, &customer) != nil {
			t.Fatal("customer clone")
		}
		customer.Login, customer.SubToken = login, "complete-sub-"+login
		customer.VLESS.UUID = fmt.Sprintf("123e4567-e89b-42d3-a456-%012d", i+1)
		customer.Naive.Username = "complete-naive-" + login
		customers = append(customers, customer)
		capture.Bindings = append(capture.Bindings, importer.LegacyNodeCapture{Login: login, NodeID: "S1", Server: customer.VLESS.Server, UUID: customer.VLESS.UUID, SubID: "complete-existing-subid-" + login})
	}
	raw = writeNormalizeFixtureJSON(t, fixture.customers, customers)
	capture.CustomersSHA256 = runtimeSHA256Hex(raw)
	writeNormalizeFixtureJSON(t, fixture.capture, capture)
	verifier, err := bcrypt.GenerateFromPassword([]byte("synthetic-complete-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	process := importer.LegacyRuntimeProcess{PID: 123, StartTicks: 456, ExecutableSHA256: strings.Repeat("a", 64), EnvironmentSHA256: strings.Repeat("b", 64), CustomersSHA256: runtimeSHA256Hex(raw)}
	capsule := importer.LegacyRuntimeCapsule{SchemaVersion: 1, CapturedAt: now.Add(-time.Second), CompletedAt: now, ProcessBefore: process, ProcessAfter: process, CustomerCount: len(customers), Environment: map[string]string{"MAESTRO_OLC_FILE": "", "MAESTRO_OLC_LOGINS": "", "MAESTRO_VKTURN_FILE": "/var/lib/maestro/vkturn.json", "MAESTRO_PANEL_PATH": "/synthetic-panel/", "MAESTRO_PANEL_PASSWORD_HASH": "file-wins", "MAESTRO_PANEL_PW_FILE": "", "MAESTRO_OLC_WB_TOKEN_FILE": ""}, Files: map[string]importer.LegacyRuntimeFile{}}
	olc := olcconf.Config{Enabled: true, Provider: "telemost", Transport: "vp8channel", Room: "synthetic-room", Key: strings.Repeat("1", 64), Logins: vkturnconf.AllowedLogins()}
	vk := vkturnconf.Config{Enabled: true, MinVersionCode: 200, Server: "synthetic-wdtt.example:443", VKHashes: []string{"synthetic-vk-hash"}, Clients: map[string]vkturnconf.Client{}}
	wgKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	for _, login := range vkturnconf.AllowedLogins() {
		vk.Clients[login] = vkturnconf.Client{Password: "synthetic-password-" + login, WG: subgen.VKTurnCreds{PrivateKey: wgKey, PeerPublicKey: wgKey, LocalAddress: "10.80.0.2/32"}}
	}
	olcRaw, _ := json.Marshal(olc)
	vkRaw, _ := json.Marshal(vk)
	for _, file := range []struct {
		key, path string
		raw       []byte
	}{{"olcrtc", "/var/lib/maestro/olcrtc.json", olcRaw}, {"vkturn", "/var/lib/maestro/vkturn.json", vkRaw}, {"panel_password", "/var/lib/maestro/panel-pw.hash", verifier}, {"wb_token", "/var/lib/maestro/wb.token", []byte("synthetic-wb-token")}} {
		capsule.Files[file.key] = importer.LegacyRuntimeFile{Path: file.path, State: "present", RawBase64: base64.StdEncoding.EncodeToString(file.raw), SHA256: runtimeSHA256Hex(file.raw)}
	}
	capsulePath := filepath.Join(directory, "runtime-capsule.json")
	capsuleRaw := writeNormalizeFixtureJSON(t, capsulePath, capsule)
	otaPath := filepath.Join(directory, "ota-source.json")
	writeNormalizeFixtureJSON(t, otaPath, importer.LegacyRuntimeOTAAbsence{SchemaVersion: 1, ObservedAt: now, Process: process, Directory: "/var/lib/maestro/update", State: "absent", RuntimeCapsuleSHA256: runtimeSHA256Hex(capsuleRaw)})
	for _, domain := range []string{"settings", "principals"} {
		fixture.sources[domain] = normalizeSourceInput{State: "present", Path: capsulePath}
	}
	trialsPath := fixture.sources["trials"].Path
	fixture.sources["trials"] = normalizeSourceInput{State: "present", Path: trialsPath}
	if os.WriteFile(trialsPath, []byte(`{"redeemed_anchors":{},"redeemed_drm":{},"audit":[]}`), 0o600) != nil || os.WriteFile(fixture.sources["orders"].Path, []byte(`[]`), 0o600) != nil {
		t.Fatal("native ledger sources")
	}
	saltPath := filepath.Join(directory, "legacy-trial-salt")
	if os.WriteFile(saltPath, []byte("synthetic-complete-trial-salt"), 0o600) != nil {
		t.Fatal("salt")
	}
	writeNormalizeFixtureJSON(t, fixture.inventory, normalizeInventory{SchemaVersion: 1, Scope: importer.LegacyCustomerPreparationScope, Sources: fixture.sources, ProtocolBindings: []importer.LegacyProtocolBinding{{Protocol: "naive", Server: customers[0].Naive.Server, NodeID: "S1"}}})
	args := append(append([]string(nil), fixture.args...), "--convert-legacy-orders", "--legacy-trial-salt-file", saltPath, "--legacy-runtime-capsule", capsulePath, "--legacy-ota-absence", otaPath, "--complete-native-import")
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, nil); code != exitClean {
		t.Fatalf("complete normalization failed: %d", code)
	}
	encoded, err := os.ReadFile(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := importer.DecodeSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, preparation := snapshot.SourceHashes["scope:"+importer.LegacyCustomerPreparationScope]; preparation {
		t.Fatal("real CLI still forbids apply")
	}
	if !strings.Contains(stdout.String(), "apply_eligible=true") || strings.Contains(stdout.String(), "conversion not performed") || !strings.Contains(stdout.String(), "cutover_ready=false") {
		t.Fatal("CLI misreported native conversion or live readiness")
	}
	_, report := importer.Plan(snapshot, defaultPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("complete snapshot planner blocked")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--snapshot", fixture.output, "--mode", "apply", "--report", filepath.Join(directory, "apply-report.json")}, &stdout, &stderr, nil); code != exitInputSystem || !strings.Contains(stderr.String(), "expected plan digest does not match") {
		t.Fatal("apply failed at obsolete preparation gate")
	}
	for _, mode := range []string{"missing-all-converters", "absent-runtime-file"} {
		t.Run(mode, func(t *testing.T) {
			other := newNormalizeCLIFixture(t)
			invalid := append(append([]string(nil), other.args...), "--complete-native-import")
			if mode == "absent-runtime-file" {
				invalid = append(invalid, "--convert-legacy-orders", "--legacy-trial-salt-file", saltPath, "--legacy-runtime-capsule", filepath.Join(directory, "missing-capsule"), "--legacy-ota-absence", otaPath)
			}
			var out, errOut bytes.Buffer
			if code := run(invalid, &out, &errOut, nil); code != exitInputSystem {
				t.Fatal("incomplete native source published")
			}
			if _, err := os.Lstat(other.output); !os.IsNotExist(err) {
				t.Fatal("failed completion wrote output")
			}
		})
	}
}

func TestNormalizeCLINativeTrialSourceRetainsEmptyLedgerAndExactSalt(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(map[bool]string{true: "empty", false: "used"}[empty], func(t *testing.T) {
			fixture := newNormalizeCLIFixture(t)
			data, err := os.ReadFile(fixture.inventory)
			if err != nil {
				t.Fatal(err)
			}
			var inventory normalizeInventory
			if json.Unmarshal(data, &inventory) != nil {
				t.Fatal("inventory fixture")
			}
			input := inventory.Sources["trials"]
			input.State = "present"
			inventory.Sources["trials"] = input
			hash := strings.Repeat("a", 64)
			raw := []byte("{\n \"redeemed_anchors\":{},\"redeemed_drm\":{},\"audit\":[{\"note\":\"private-trial-audit\"}]\n}\n")
			if !empty {
				raw = []byte(`{"redeemed_anchors":{"` + hash + `":"private-trial-login"},"redeemed_drm":{"` + hash + `":"private-trial-login"},"audit":[]}`)
			}
			if os.WriteFile(input.Path, raw, 0o600) != nil {
				t.Fatal("trial source fixture")
			}
			writeNormalizeFixtureJSON(t, fixture.inventory, inventory)
			salt := []byte("exact protected trial salt\n")
			saltPath := filepath.Join(filepath.Dir(fixture.output), "trial-salt")
			if os.WriteFile(saltPath, salt, 0o600) != nil {
				t.Fatal("salt fixture")
			}
			args := append(append([]string(nil), fixture.args...), "--legacy-trial-salt-file", saltPath)
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr, nil); code != exitClean {
				t.Fatalf("native normalize exit %d", code)
			}
			encoded, err := os.ReadFile(fixture.output)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := importer.DecodeSnapshot(encoded)
			if err != nil || snapshot.LegacyTrialSaltSHA256 != runtimeSHA256Hex(salt) || !importer.ProtectionFromSnapshot(snapshot).HasTrials {
				t.Fatal("empty ledger/salt binding lost")
			}
			wantRows := 2
			if empty {
				wantRows = 0
			}
			if len(snapshot.Trials) != wantRows || !strings.Contains(stdout.String(), "conversion performed") || !strings.Contains(stdout.String(), "cutover_ready=false") {
				t.Fatal("conversion scope misreported")
			}
			keys, err := loadKeyBundle(fixture.key)
			if err != nil {
				t.Fatal(err)
			}
			defer keys.zero()
			box, err := controlplane.NewSecretBox(keys.CurrentKeyVersion, keys.EncryptionKeys, keys.HMACKey)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, secret := range snapshot.EncryptedSecrets {
				if secret.OwnerType != "trial_evidence" {
					continue
				}
				nonce, _ := base64.StdEncoding.DecodeString(secret.NonceB64)
				cipher, _ := base64.StdEncoding.DecodeString(secret.CiphertextB64)
				plain, err := box.Open(controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
				if err != nil || !bytes.Equal(plain, raw) {
					t.Fatal("CLI evidence changed raw source bytes")
				}
				found = true
			}
			if !found {
				t.Fatal("raw evidence missing")
			}
			for _, secret := range []string{string(salt), "private-trial-audit", "private-trial-login"} {
				if strings.Contains(stdout.String()+stderr.String()+string(encoded), secret) {
					t.Fatal("raw trial data leaked")
				}
			}
			stdout.Reset()
			stderr.Reset()
			if code := run(args, &stdout, &stderr, nil); code != exitInputSystem {
				t.Fatal("existing protected output overwritten")
			}
			after, _ := os.ReadFile(fixture.output)
			if !bytes.Equal(after, encoded) {
				t.Fatal("failed repeat changed snapshot")
			}
		})
	}
}

func TestNormalizeCLIRejectsSaltWithoutPresentTrialSource(t *testing.T) {
	fixture := newNormalizeCLIFixture(t)
	saltPath := filepath.Join(filepath.Dir(fixture.output), "trial-salt")
	if os.WriteFile(saltPath, []byte("salt"), 0o600) != nil {
		t.Fatal("salt fixture")
	}
	var stdout, stderr bytes.Buffer
	if code := run(append(fixture.args, "--legacy-trial-salt-file", saltPath), &stdout, &stderr, nil); code != exitInputSystem {
		t.Fatal("absent trial source accepted salt")
	}
	if _, err := os.Lstat(fixture.output); !os.IsNotExist(err) {
		t.Fatal("invalid source published output")
	}
}

type normalizeCLIFixture struct {
	args                                       []string
	customers, capture, inventory, key, output string
	sources                                    map[string]normalizeSourceInput
}

func writeNormalizeFixtureJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil || os.WriteFile(path, raw, 0o600) != nil {
		t.Fatal("cannot write synthetic protected fixture")
	}
	return raw
}

func newNormalizeCLIFixture(t *testing.T) normalizeCLIFixture {
	t.Helper()
	directory := t.TempDir()
	fixture := normalizeCLIFixture{customers: filepath.Join(directory, "customers.json"), capture: filepath.Join(directory, "capture.json"),
		inventory: filepath.Join(directory, "inventory.json"), key: filepath.Join(directory, "key.json"), output: filepath.Join(directory, "snapshot.json"), sources: map[string]normalizeSourceInput{}}
	for _, domain := range []string{"orders", "trials", "settings", "principals"} {
		fixture.sources[domain] = normalizeSourceInput{State: "absent", Path: filepath.Join(directory, domain+".json")}
	}
	fixture.sources["orders"] = normalizeSourceInput{State: "present", Path: fixture.sources["orders"].Path}
	writeNormalizeFixtureJSON(t, fixture.sources["orders"].Path, []map[string]string{{"id": "synthetic-order-must-not-be-dropped"}})
	customer := legacystore.Customer{Login: "SyntheticNormalizeLogin", SubToken: "synthetic-private-subscription-token", Expires: time.Now().UTC().Add(time.Hour),
		VLESS:   &subgen.VLESSCreds{Server: "s1.example.test", Port: 443, UUID: "42a633eb-52e6-4ec7-b373-7a4a88b1c19b"},
		Naive:   &subgen.NaiveCreds{Server: "naive.example.test", Port: 443, Username: "mtv_SyntheticNormalizeLogin", Password: "synthetic-private-naive-password"},
		Devices: map[string]time.Time{"synthetic-private-device": time.Now().UTC()}}
	raw := writeNormalizeFixtureJSON(t, fixture.customers, []legacystore.Customer{customer})
	now := time.Now().UTC()
	writeNormalizeFixtureJSON(t, fixture.capture, importer.LegacyXUICapture{SchemaVersion: 1, CapturedAt: now.Add(-time.Second), CompletedAt: now,
		CustomersSHA256: runtimeSHA256Hex(raw), Bindings: []importer.LegacyNodeCapture{{Login: customer.Login, NodeID: "S1", Server: customer.VLESS.Server, UUID: customer.VLESS.UUID, SubID: "synthetic-existing-bot-sub-id"}}})
	writeNormalizeFixtureJSON(t, fixture.inventory, normalizeInventory{SchemaVersion: 1, Scope: importer.LegacyCustomerPreparationScope, Sources: fixture.sources,
		ProtocolBindings: []importer.LegacyProtocolBinding{{Protocol: "naive", Server: customer.Naive.Server, NodeID: "S1"}}})
	writeNormalizeFixtureJSON(t, fixture.key, map[string]any{"schema_version": 1, "current_key_version": 7,
		"encryption_keys": []map[string]any{{"version": 7, "key_b64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))}},
		"hmac_key_b64":    base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))})
	fixture.args = []string{"normalize", "--customers", fixture.customers, "--xui-capture", fixture.capture, "--inventory", fixture.inventory,
		"--key-file", fixture.key, "--output", fixture.output, "--max-capture-age", "1h"}
	return fixture
}

func TestNormalizeCLIProducesProtectedPreparationAndRejectsApply(t *testing.T) {
	fixture := newNormalizeCLIFixture(t)
	var stdout, stderr bytes.Buffer
	factoryCalls := 0
	factory := func(context.Context, applyRuntimeConfig) (*applyRuntime, error) {
		factoryCalls++
		t.Fatal("preparation reached production runtime factory")
		return nil, nil
	}
	if code := run(fixture.args, &stdout, &stderr, factory); code != exitClean || factoryCalls != 0 ||
		!strings.Contains(stdout.String(), "cutover_ready=false") || !strings.Contains(stdout.String(), "orders=present") {
		t.Fatal("preparation command did not preserve its explicit scope")
	}
	for _, secret := range []string{"synthetic-private-subscription-token", "synthetic-private-naive-password", "synthetic-private-device", "SyntheticNormalizeLogin"} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatal("diagnostic leaked customer identity")
		}
	}
	data, err := os.ReadFile(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := importer.DecodeSnapshot(data)
	if err != nil || snapshot.SourceHashes["legacy:orders:present-unconverted"] == "" || snapshot.SourceHashes["source_inventory"] == "" {
		t.Fatal("scope evidence missing from actual Snapshot output")
	}
	info, err := os.Stat(fixture.output)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("normalization output permissions are not private")
	}
	applyReport := filepath.Join(t.TempDir(), "apply-report.json")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--snapshot", fixture.output, "--mode", "apply", "--report", applyReport}, &stdout, &stderr, factory); code != exitInputSystem || factoryCalls != 0 ||
		!strings.Contains(stderr.String(), "not a complete cutover input") {
		t.Fatal("incomplete preparation was allowed to reach apply")
	}
	if _, err := os.Lstat(applyReport); !os.IsNotExist(err) {
		t.Fatal("apply rejection performed a report mutation")
	}
	after, err := os.ReadFile(fixture.output)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("apply rejection modified the protected snapshot")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--snapshot", fixture.output, "--mode", "dry-run", "--report", filepath.Join(t.TempDir(), "dry-report.json")}, &stdout, &stderr, factory); code != exitClean || factoryCalls != 0 {
		t.Fatal("preparation dry-run is not reviewable")
	}
}

func TestNormalizeCLIRejectsInvalidOrChangedInputsWithoutOutput(t *testing.T) {
	for _, name := range []string{"missing-customers", "source-drift", "undeclared-presence", "missing-inventory-domain", "wrong-scope", "stale-capture", "output-input", "output-absent-source", "existing-output"} {
		t.Run(name, func(t *testing.T) {
			fixture := newNormalizeCLIFixture(t)
			switch name {
			case "missing-customers":
				if os.Remove(fixture.customers) != nil {
					t.Fatal("fixture removal failed")
				}
			case "source-drift":
				data, err := os.ReadFile(fixture.customers)
				if err != nil {
					t.Fatal(err)
				}
				if os.WriteFile(fixture.customers, append(data, ' '), 0o600) != nil {
					t.Fatal("fixture write failed")
				}
			case "undeclared-presence":
				writeNormalizeFixtureJSON(t, fixture.sources["trials"].Path, map[string]bool{"used": true})
			case "missing-inventory-domain", "wrong-scope":
				data, err := os.ReadFile(fixture.inventory)
				if err != nil {
					t.Fatal(err)
				}
				var inventory normalizeInventory
				if json.Unmarshal(data, &inventory) != nil {
					t.Fatal("fixture decode failed")
				}
				if name == "wrong-scope" {
					inventory.Scope = "full-cutover"
				} else {
					delete(inventory.Sources, "principals")
				}
				writeNormalizeFixtureJSON(t, fixture.inventory, inventory)
			case "stale-capture":
				data, err := os.ReadFile(fixture.capture)
				if err != nil {
					t.Fatal(err)
				}
				var capture importer.LegacyXUICapture
				if json.Unmarshal(data, &capture) != nil {
					t.Fatal("fixture decode failed")
				}
				capture.CapturedAt = capture.CapturedAt.Add(-2 * time.Hour)
				writeNormalizeFixtureJSON(t, fixture.capture, capture)
			case "output-input":
				fixture.args[10] = fixture.customers
			case "output-absent-source":
				fixture.args[10] = fixture.sources["trials"].Path
			case "existing-output":
				writeNormalizeFixtureJSON(t, fixture.output, map[string]bool{"must_remain": true})
			}
			var before []byte
			if name == "existing-output" {
				before, _ = os.ReadFile(fixture.output)
			}
			if name == "output-input" {
				before, _ = os.ReadFile(fixture.customers)
			}
			var stdout, stderr bytes.Buffer
			if code := run(fixture.args, &stdout, &stderr, nil); code != exitInputSystem {
				t.Fatal("invalid preparation input accepted")
			}
			if name == "existing-output" {
				after, _ := os.ReadFile(fixture.output)
				if !bytes.Equal(before, after) {
					t.Fatal("existing output overwritten")
				}
			} else if _, err := os.Lstat(fixture.output); !os.IsNotExist(err) {
				t.Fatal("failed normalization published output")
			}
			if name == "output-input" {
				after, _ := os.ReadFile(fixture.customers)
				if !bytes.Equal(before, after) {
					t.Fatal("source was replaced by output")
				}
			}
			if name == "output-absent-source" {
				if _, err := os.Lstat(fixture.sources["trials"].Path); !os.IsNotExist(err) {
					t.Fatal("output created an absent source")
				}
			}
			if strings.Contains(stderr.String(), fixture.customers) || strings.Contains(stderr.String(), "synthetic-private") {
				t.Fatal("input leaked through failure diagnostics")
			}
		})
	}
}

func TestNormalizeSourceRecheckAndNoReplacePublication(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "protected.json")
	original := []byte(`{"synthetic":"original"}`)
	if os.WriteFile(path, original, 0o600) != nil {
		t.Fatal("fixture write failed")
	}
	stamps := []normalizeFileStamp{{path: path, sha: runtimeSHA256Hex(original)}}
	if verifyNormalizeFiles(stamps) != nil {
		t.Fatal("unchanged source rejected")
	}
	if os.WriteFile(path, []byte(`{"synthetic":"changed"}`), 0o600) != nil {
		t.Fatal("fixture change failed")
	}
	if verifyNormalizeFiles(stamps) == nil {
		t.Fatal("changed source not detected")
	}
	before, _ := os.ReadFile(path)
	if writeNormalizeOutput(path, []byte(`{"replacement":true}`)) == nil {
		t.Fatal("existing source overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed publication changed source")
	}
	if writeNormalizeOutput(filepath.Join(directory, "new.json"), original) != nil {
		t.Fatal("new protected output publication failed")
	}
}
