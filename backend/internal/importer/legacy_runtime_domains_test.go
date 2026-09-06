package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"golang.org/x/crypto/bcrypt"
)

type runtimeDomainFixture struct {
	rawCustomers     []byte
	snapshot         Snapshot
	capsule          LegacyRuntimeCapsule
	box              *controlplane.SecretBox
	options          LegacyRuntimeDomainOptions
	verifier         []byte
	capture          LegacyXUICapture
	normalizeOptions LegacyNormalizeOptions
}

func newRuntimeDomainFixture(t *testing.T, moments ...time.Time) runtimeDomainFixture {
	t.Helper()
	raw, capture, normalizeOptions, box := legacyNormalizeFixture(t)
	normalizeOptions.Sources["settings"] = LegacySourcePresence{State: "absent"}
	if len(moments) == 1 {
		capture.CapturedAt = moments[0].Add(-5 * time.Second)
		capture.CompletedAt = moments[0].Add(-3 * time.Second)
		normalizeOptions.Now = moments[0]
	}
	base, err := DecodeLegacyCustomers(raw)
	if err != nil {
		t.Fatal(err)
	}
	var customers []legacystore.Customer
	capture.Bindings = nil
	for index, login := range vkturnconf.AllowedLogins() {
		var customer legacystore.Customer
		if json.Unmarshal(marshalNormalizeFixture(t, base[0]), &customer) != nil {
			t.Fatal("fixture clone")
		}
		customer.Login, customer.SubToken = login, "synthetic-runtime-sub-"+login
		if len(moments) == 1 {
			customer.Expires = moments[0].Add(24 * time.Hour)
			customer.Devices = map[string]time.Time{"synthetic-device": moments[0]}
		}
		if customer.Hy2 != nil {
			customer.Hy2.User = login
		}
		uuid := fmt.Sprintf("123e4567-e89b-42d3-a456-%012d", index+1)
		customer.VLESS.UUID = uuid
		if customer.VLESS3 != nil {
			customer.VLESS3.UUID = uuid
		}
		if customer.VLESS4 != nil {
			customer.VLESS4.UUID = uuid
		}
		customers = append(customers, customer)
		for nodeID, binding := range legacyVLESSNodes(customer) {
			capture.Bindings = append(capture.Bindings, LegacyNodeCapture{Login: login, NodeID: nodeID, Server: binding.server, UUID: binding.uuid, SubID: "synthetic-runtime-sub-id-" + login + "-" + nodeID})
		}
	}
	raw = marshalNormalizeFixture(t, customers)
	capture.CustomersSHA256 = sha256Hex(raw)
	snapshot := normalizeFixture(t, raw, capture, normalizeOptions, box)
	verifier, err := bcrypt.GenerateFromPassword([]byte("synthetic-existing-file-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	process := LegacyRuntimeProcess{PID: 123, StartTicks: 456, ExecutableSHA256: strings.Repeat("a", 64), EnvironmentSHA256: strings.Repeat("b", 64), CustomersSHA256: sha256Hex(raw)}
	capsule := LegacyRuntimeCapsule{SchemaVersion: 1, CapturedAt: time.Unix(1_000_003, 0).UTC(), CompletedAt: time.Unix(1_000_004, 0).UTC(), ProcessBefore: process, ProcessAfter: process, CustomerCount: 3,
		Environment: map[string]string{"MAESTRO_OLC_FILE": "", "MAESTRO_OLC_LOGINS": "ignored-env-login", "MAESTRO_VKTURN_FILE": "/var/lib/maestro/vkturn.json", "MAESTRO_PANEL_PATH": "/synthetic-panel/", "MAESTRO_PANEL_PASSWORD_HASH": "ignored-env-bootstrap-hash", "MAESTRO_PANEL_PW_FILE": "", "MAESTRO_OLC_WB_TOKEN_FILE": ""}, Files: map[string]LegacyRuntimeFile{}}
	if len(moments) == 1 {
		capsule.CapturedAt = moments[0].Add(-2 * time.Second)
		capsule.CompletedAt = moments[0].Add(-time.Second)
	}
	olc := olcconf.Config{Enabled: true, Provider: "telemost", Transport: "vp8channel", Room: "synthetic-global-room", Key: strings.Repeat("1", 64), Logins: vkturnconf.AllowedLogins(), Rooms: map[string]olcconf.RoomKey{
		"wapmix":  {Room: "synthetic-first-room", Key: strings.Repeat("2", 64), Provider: "telemost"},
		"wapmixx": {Room: "synthetic-second-room", Key: strings.Repeat("3", 64), Provider: "wbstream"},
	}}
	vk := vkturnconf.Config{Enabled: true, MinVersionCode: 200, Server: "synthetic-wdtt.example:443", VKHashes: []string{"synthetic-vk-hash"}, Clients: map[string]vkturnconf.Client{}}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	for _, login := range vkturnconf.AllowedLogins() {
		vk.Clients[login] = vkturnconf.Client{Password: "synthetic-password-" + login, WG: subgen.VKTurnCreds{PrivateKey: key, PeerPublicKey: key, LocalAddress: "10.80.0.2/32"}}
	}
	for _, file := range []struct {
		key, path string
		raw       []byte
	}{
		{"olcrtc", "/var/lib/maestro/olcrtc.json", marshalNormalizeFixture(t, olc)},
		{"vkturn", "/var/lib/maestro/vkturn.json", marshalNormalizeFixture(t, vk)},
		{"panel_password", "/var/lib/maestro/panel-pw.hash", append(append([]byte(" \n"), verifier...), '\n')},
		{"wb_token", "/var/lib/maestro/wb.token", []byte(" synthetic-existing-wb-token\n")},
	} {
		capsule.Files[file.key] = LegacyRuntimeFile{Path: file.path, State: "present", RawBase64: base64.StdEncoding.EncodeToString(file.raw), SHA256: sha256Hex(file.raw)}
	}
	now := time.Unix(1_000_005, 0).UTC()
	if len(moments) == 1 {
		now = moments[0]
	}
	return runtimeDomainFixture{raw, snapshot, capsule, box, LegacyRuntimeDomainOptions{Now: now, MaxCaptureAge: time.Minute, ExpectedProcess: process}, verifier, capture, normalizeOptions}
}

func TestNativeRuntimeDomainsPreserveEffectiveCredentialsMembersAndRawEvidence(t *testing.T) {
	fixture := newRuntimeDomainFixture(t)
	raw := marshalNormalizeFixture(t, fixture.capsule)
	domains, err := NormalizeLegacyRuntimeDomains(raw, fixture.rawCustomers, fixture.snapshot, fixture.box, fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ComposeLegacyRuntimeDomains(fixture.snapshot, domains)
	if err != nil || len(snapshot.Settings) != 3 || len(snapshot.Principals) != 1 {
		t.Fatal("domains not composed")
	}
	if snapshot.Principals[0].Status != "active" || len(snapshot.Principals[0].Roles) != 1 || snapshot.Principals[0].Roles[0] != "owner" {
		t.Fatal("legacy password-only authority changed")
	}
	var olcPublic struct {
		Rooms map[string]map[string]string `json:"rooms"`
	}
	if json.Unmarshal(snapshot.Settings[0].PublicValueJSON, &olcPublic) != nil || len(olcPublic.Rooms) != 3 || olcPublic.Rooms["wapmix2"]["room"] != "synthetic-global-room" || olcPublic.Rooms["wapmixx"]["provider"] != "wbstream" {
		t.Fatal("effective room/provider fallback changed")
	}
	for _, secret := range domains.secrets {
		scope := controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}
		nonce, _ := decodeCanonicalBase64(secret.NonceB64)
		cipher, _ := decodeCanonicalBase64(secret.CiphertextB64)
		plain, err := fixture.box.Open(scope, controlplane.Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: cipher})
		if err != nil {
			t.Fatal(err)
		}
		switch secret.OwnerType {
		case "principal":
			if !bytes.Equal(plain, fixture.verifier) || bcrypt.CompareHashAndPassword(plain, []byte("synthetic-existing-file-password")) != nil {
				t.Fatal("effective file bcrypt changed or env incorrectly won")
			}
		case "legacy_runtime_source":
			if !bytes.Equal(plain, raw) {
				t.Fatal("raw capsule was not preserved byte-exact")
			}
		case "setting":
			if secret.OwnerSourceKey == "wbstream" {
				if string(plain) != "synthetic-existing-wb-token" {
					t.Fatal("WB runtime token changed")
				}
				break
			}
			var doc controlplane.LegacyRuntimeSettingDocument
			if json.Unmarshal(plain, &doc) != nil || len(doc.Members) != 3 {
				t.Fatal("allowlist members omitted")
			}
			for _, member := range doc.Members {
				if member.MemberHMAC != fixture.box.LookupHMAC("setting-member:"+doc.SettingKey, []byte(member.Login)) {
					t.Fatal("membership identity changed")
				}
			}
		}
		zeroBytes(plain)
	}
	changed := fixture.snapshot
	changed.SourceHashes = map[string]string{"customers": strings.Repeat("c", 64)}
	if _, err := ComposeLegacyRuntimeDomains(changed, domains); err == nil {
		t.Fatal("producer proof reused for a different source")
	}
}

func TestNativeRuntimeDomainsRejectDriftLossySourceAndUnknownMember(t *testing.T) {
	for _, mode := range []string{"process", "customer-source", "stale", "path", "file-sha", "unknown-member", "duplicate-member"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newRuntimeDomainFixture(t)
			switch mode {
			case "process":
				fixture.capsule.ProcessAfter.StartTicks++
			case "customer-source":
				fixture.capsule.ProcessBefore.CustomersSHA256 = strings.Repeat("c", 64)
			case "stale":
				fixture.options.Now = fixture.options.Now.Add(2 * time.Minute)
			case "path":
				file := fixture.capsule.Files["panel_password"]
				file.Path = "/other/hash"
				fixture.capsule.Files["panel_password"] = file
			case "file-sha":
				file := fixture.capsule.Files["wb_token"]
				file.SHA256 = strings.Repeat("c", 64)
				fixture.capsule.Files["wb_token"] = file
			default:
				file := fixture.capsule.Files["olcrtc"]
				raw, _ := decodeCanonicalBase64(file.RawBase64)
				var config olcconf.Config
				if json.Unmarshal(raw, &config) != nil {
					t.Fatal("fixture config")
				}
				if mode == "unknown-member" {
					config.Logins[0] = "WAPMIX"
				} else {
					config.Logins = append(config.Logins, config.Logins[0])
				}
				raw = marshalNormalizeFixture(t, config)
				file.RawBase64, file.SHA256 = base64.StdEncoding.EncodeToString(raw), sha256Hex(raw)
				fixture.capsule.Files["olcrtc"] = file
			}
			if _, err := NormalizeLegacyRuntimeDomains(marshalNormalizeFixture(t, fixture.capsule), fixture.rawCustomers, fixture.snapshot, fixture.box, fixture.options); err == nil {
				t.Fatal("inconsistent source accepted")
			}
		})
	}
}

func runtimeDomainNormalizedSnapshot(t *testing.T, fixture runtimeDomainFixture) Snapshot {
	t.Helper()
	raw := marshalNormalizeFixture(t, fixture.capsule)
	absence := LegacyRuntimeOTAAbsence{SchemaVersion: 1, ObservedAt: fixture.options.Now, Process: fixture.capsule.ProcessBefore, Directory: "/var/lib/maestro/update", State: "absent", RuntimeCapsuleSHA256: sha256Hex(raw)}
	options := fixture.normalizeOptions
	options.Now = fixture.options.Now
	options.Sources = map[string]LegacySourcePresence{}
	for key, value := range fixture.normalizeOptions.Sources {
		options.Sources[key] = value
	}
	for _, key := range []string{"settings", "principals"} {
		options.Sources[key] = LegacySourcePresence{State: "present", SHA256: sha256Hex(raw)}
	}
	options.RuntimeSource = &LegacyRuntimeSource{RawCapsule: raw, RawOTAAbsence: marshalNormalizeFixture(t, absence)}
	snapshot, err := NormalizeLegacyCustomers(fixture.rawCustomers, fixture.capture, fixture.box, bytes.Repeat([]byte{0x22}, 32), options)
	if err != nil {
		t.Fatal("real runtime normalization path rejected exact protected source")
	}
	return snapshot
}

func TestNativeRuntimeNormalizerBindsSourceProofAndAbsentOTA(t *testing.T) {
	fixture := newRuntimeDomainFixture(t)
	snapshot := runtimeDomainNormalizedSnapshot(t, fixture)
	if len(snapshot.Settings) != 4 || len(snapshot.Principals) != 1 {
		t.Fatal("runtime domains missing from actual normalizer")
	}
	if _, err := ValidateSnapshotProtection(ProtectionFromSnapshot(snapshot), fixture.box, bytes.Repeat([]byte{0x22}, 32), nil); err != nil {
		t.Fatal(err)
	}
	plan, report := Plan(snapshot, fixture.normalizeOptions.PlanOptions)
	if len(report.Blockers) != 0 {
		t.Fatal("native runtime plan blocked")
	}
	export, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil || export.OTA.State != "absent" || export.OTA.SourceSHA256 != snapshot.SourceHashes["legacy:ota:absent-v1"] {
		t.Fatal("genuine absent OTA could not be represented")
	}
	raw, err := json.Marshal(export.OTA)
	if err != nil || bytes.Contains(raw, []byte("version_code")) || bytes.Contains(raw, []byte("apk_size")) {
		t.Fatal("absent OTA fabricated APK metadata")
	}
	for _, mode := range []string{"member-target", "member-omission", "source-marker", "bcrypt-ref", "ota-proof", "secret-aad"} {
		t.Run(mode, func(t *testing.T) {
			var changed Snapshot
			if json.Unmarshal(marshalNormalizeFixture(t, snapshot), &changed) != nil {
				t.Fatal("clone")
			}
			switch mode {
			case "member-target":
				changed.Settings[0].Members[0].CustomerID = strings.Repeat("f", 64)
			case "member-omission":
				changed.Settings[0].Members = changed.Settings[0].Members[1:]
			case "source-marker":
				delete(changed.SourceHashes, "legacy:settings:runtime-converted-v1")
			case "bcrypt-ref":
				changed.Principals[0].CredentialSecretRef = "other-source"
			case "ota-proof":
				changed.SourceHashes["legacy:ota:absent-v1"] = strings.Repeat("f", 64)
			case "secret-aad":
				for index := range changed.EncryptedSecrets {
					if changed.EncryptedSecrets[index].OwnerType == "legacy_runtime_source" {
						changed.EncryptedSecrets[index].OwnerSourceKey = strings.Repeat("f", 64)
					}
				}
			}
			if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(changed), fixture.box); err == nil {
				t.Fatal("unbound native import accepted")
			}
		})
	}
	// Protected copies must not let a caller mutate the validated snapshot via a
	// shared member backing array.
	proof := ProtectionFromSnapshot(snapshot)
	proof.Settings[0].Members[0].Login = "changed"
	if snapshot.Settings[0].Members[0].Login == "changed" {
		t.Fatal("member slice aliases source snapshot")
	}
}

func TestNativeRuntimeExplicitCaptureAgeAndDurableArchive(t *testing.T) {
	fixture := newRuntimeDomainFixture(t)
	fixture.options.Now = fixture.options.Now.Add(2 * time.Hour)
	fixture.options.MaxCaptureAge = 3 * time.Hour
	if _, err := NormalizeLegacyRuntimeDomains(marshalNormalizeFixture(t, fixture.capsule), fixture.rawCustomers, fixture.snapshot, fixture.box, fixture.options); err != nil {
		t.Fatal("explicit source age was replaced with an invented upper cap")
	}
	fixture.options.MaxCaptureAge = time.Minute
	if _, err := NormalizeLegacyRuntimeDomains(marshalNormalizeFixture(t, fixture.capsule), fixture.rawCustomers, fixture.snapshot, fixture.box, fixture.options); err == nil {
		t.Fatal("explicit source age was ignored")
	}
	fixture = newRuntimeDomainFixture(t)
	snapshot := runtimeDomainNormalizedSnapshot(t, fixture)
	snapshot.CapturedAt = snapshot.CapturedAt.Add(24 * time.Hour)
	if _, err := ValidateSnapshotProtection(ProtectionFromSnapshot(snapshot), fixture.box, bytes.Repeat([]byte{0x22}, 32), nil); err != nil {
		t.Fatal("durable authenticated source archive was given a relative lifetime")
	}
}
