package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"golang.org/x/crypto/bcrypt"
)

func productionDomainsFixture(t *testing.T) (Snapshot, *controlplane.SecretBox, []byte, []byte, string) {
	t.Helper()
	keys := map[int][]byte{6: bytes.Repeat([]byte{0x36}, 32), 7: bytes.Repeat([]byte{0x47}, 32)}
	lookup := bytes.Repeat([]byte{0x58}, 32)
	old, err := controlplane.NewSecretBox(6, keys, lookup)
	if err != nil {
		t.Fatal(err)
	}
	box, err := controlplane.NewSecretBox(7, keys, lookup)
	if err != nil {
		t.Fatal(err)
	}
	password := "synthetic-original-panel-password"
	verifier, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	settingRaw := []byte(`{"shared_key":"synthetic-retained-key","enabled":false}`)
	snapshot := Snapshot{FormatVersion: 2, ClusterHMACKeySHA256: sha256Hex(lookup), SnapshotKind: "full", CapturedAt: time.Unix(2_000_000, 0).UTC(), SourceHashes: map[string]string{"synthetic-domains": sha256Hex([]byte("domains-v1"))},
		Settings:   []LegacySetting{{Key: "production-domain-roundtrip", PublicValueJSON: json.RawMessage(`{"enabled":false}`), Generation: 3, SecretRef: "setting-domain-original"}, {Key: "production-public-only", PublicValueJSON: json.RawMessage(`{"enabled":true}`), Generation: 1}},
		Principals: []LegacyPrincipal{{SourceKey: "legacy-domain-owner", LoginKeyHMAC: box.LookupHMAC("principal-login", []byte("domain-owner")), Status: "active", Roles: []string{"owner"}, CredentialSecretRef: "principal-domain-original"}}}
	for _, value := range []struct {
		id    string
		scope controlplane.SecretScope
		raw   []byte
	}{
		{"setting-domain-original", controlplane.SecretScope{OwnerType: "setting", OwnerID: snapshot.Settings[0].Key, Field: "secret", Kind: snapshot.Settings[0].Key}, settingRaw},
		{"principal-domain-original", controlplane.SecretScope{OwnerType: "principal", OwnerID: snapshot.Principals[0].SourceKey, Field: "verifier", Kind: "password-verifier"}, verifier},
	} {
		envelope, err := old.Seal(value.scope, value.raw)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, LegacyEncryptedSecret{SecretID: value.id, OwnerType: value.scope.OwnerType, OwnerSourceKey: value.scope.OwnerID, Field: value.scope.Field, Kind: value.scope.Kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha256Hex(value.raw)})
	}
	return snapshot, box, settingRaw, verifier, password
}

func TestProductionDomainsResealExactBytesIntoRuntimeScopes(t *testing.T) {
	snapshot, box, settingRaw, verifier, _ := productionDomainsFixture(t)
	protected := ProtectionFromSnapshot(snapshot)
	validated, err := ValidateProductionCustomerIdentities(protected, box)
	if err != nil {
		t.Fatal(err)
	}
	db := &applyStoreRQLite{}
	store, err := NewProductionRQLiteApplyStore(db, func() time.Time { return snapshot.CapturedAt }, validated, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("valid protected domain plan blocked")
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatal(err)
	}
	seenSetting, seenPrincipal := false, false
	for _, operation := range operations {
		var statementsErr error
		if operation.Entity == "setting" {
			statements, err := store.settingStatements(ApplyBatch{}, operation)
			statementsErr = err
			for _, statement := range statements {
				if strings.Contains(statement.SQL, "INSERT INTO setting_secrets(") {
					if statement.Args[3] != 6 {
						t.Fatal("rebind changed the authenticated source key version")
					}
					assertProductionDomainEnvelope(t, box, statement.Args[1].(string), controlplane.SecretScope{OwnerType: "setting", OwnerID: snapshot.Settings[0].Key, Field: "secret", Kind: snapshot.Settings[0].Key}, settingRaw)
					seenSetting = true
				}
			}
		} else if operation.Entity == "principal" {
			statements, err := store.principalStatements(ApplyBatch{}, operation)
			statementsErr = err
			for _, statement := range statements {
				if strings.Contains(statement.SQL, "INSERT INTO principal_credentials(") {
					assertProductionDomainEnvelope(t, box, statement.Args[2].(string), controlplane.SecretScope{OwnerType: "principal", OwnerID: plan.Principals[0].InternalID, Field: "password", Kind: "bcrypt"}, verifier)
					seenPrincipal = true
				}
			}
		}
		if statementsErr != nil {
			t.Fatal(statementsErr)
		}
	}
	if !seenSetting || !seenPrincipal || len(db.requests) != 0 {
		t.Fatal("domain adaptation failed or touched database during preparation")
	}
	// Validated metadata does not retain mutable caller-owned slices.
	snapshot.Settings[0].PublicValueJSON[0] = 'x'
	snapshot.Principals[0].Roles[0] = "admin"
	if !json.Valid(protected.Settings[0].PublicValueJSON) || protected.Principals[0].Roles[0] != "owner" {
		t.Fatal("snapshot metadata aliases source")
	}
	protected.Settings[0].Generation++
	protected.Principals[0].Roles[0] = "admin"
	if validated.settingRows[plan.Settings[0].Key] != canonicalLegacyDigest(plan.Settings[0]) {
		t.Fatal("validated setting metadata changed through caller alias")
	}
	if _, err := store.productionPrincipalValue(plan.Principals[0], snapshot.EncryptedSecrets[1]); err != nil {
		t.Fatal("validated principal metadata changed through caller alias")
	}
}

func TestProductionDomainProtectionClonesParentMetadata(t *testing.T) {
	parent, box, _, _, _ := productionDomainsFixture(t)
	child, _, _, _, _ := productionDomainsFixture(t)
	child.SnapshotKind = "delta"
	child.ParentSourceDigest = digestSnapshot(parent)
	child.CapturedAt = parent.CapturedAt.Add(time.Second)
	protected := ProtectionFromSnapshot(child, &parent)
	parent.Settings[0].PublicValueJSON[0] = 'x'
	parent.Principals[0].Roles[0] = "admin"
	child.Settings[0].PublicValueJSON[0] = 'x'
	child.Principals[0].Roles[0] = "admin"
	if protected.Parent == nil || !json.Valid(protected.Parent.Settings[0].PublicValueJSON) || protected.Parent.Principals[0].Roles[0] != "owner" || !json.Valid(protected.Settings[0].PublicValueJSON) || protected.Principals[0].Roles[0] != "owner" {
		t.Fatal("current or parent domain metadata aliases raw snapshot")
	}
	if _, err := ValidateProductionCustomerIdentities(protected, box); err != nil {
		t.Fatal("source mutation changed cloned protection")
	}
	protected.Parent.Principals[0].Roles = []string{"owner", "owner"}
	if _, err := ValidateProductionCustomerIdentities(protected, box); err == nil {
		t.Fatal("invalid parent domain evidence was ignored")
	}
}

func TestProductionDomainsAuthenticateDeclaredSourceScopesAndRejectNonBcrypt(t *testing.T) {
	for _, name := range []string{"fixture-setting-alias", "typed-principal", "non-bcrypt"} {
		t.Run(name, func(t *testing.T) {
			snapshot, box, settingRaw, verifier, _ := productionDomainsFixture(t)
			index, raw := 1, verifier
			if name == "fixture-setting-alias" {
				index, raw = 0, settingRaw
				snapshot.Settings[0].Key = "telegram"
			}
			secret := &snapshot.EncryptedSecrets[index]
			if name == "fixture-setting-alias" {
				secret.OwnerSourceKey = "telegram"
				secret.Field = "token"
				secret.Kind = "bot-token"
			} else {
				secret.Field = "password"
				secret.Kind = "bcrypt"
			}
			if name == "non-bcrypt" {
				raw = []byte("authenticated-but-not-a-password-verifier")
			}
			envelope, err := box.Seal(controlplane.SecretScope{OwnerType: secret.OwnerType, OwnerID: secret.OwnerSourceKey, Field: secret.Field, Kind: secret.Kind}, raw)
			if err != nil {
				t.Fatal(err)
			}
			secret.KeyVersion = envelope.KeyVersion
			secret.NonceB64 = base64.StdEncoding.EncodeToString(envelope.Nonce)
			secret.CiphertextB64 = base64.StdEncoding.EncodeToString(envelope.Ciphertext)
			secret.SHA256 = sha256Hex(raw)
			_, err = ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
			if name == "non-bcrypt" {
				if err == nil {
					t.Fatal("authenticated non-bcrypt credential was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal("declared source scope rejected valid authenticated ciphertext")
			}
		})
	}
}

func assertProductionDomainEnvelope(t *testing.T, box *controlplane.SecretBox, encoded string, scope controlplane.SecretScope, want []byte) {
	t.Helper()
	raw, ok := decodeCanonicalBase64(encoded)
	var envelope controlplane.Envelope
	if !ok || json.Unmarshal(raw, &envelope) != nil || envelope.KeyVersion != 6 {
		t.Fatal("wrong runtime envelope encoding/key version")
	}
	plain, err := box.Open(scope, envelope)
	if err != nil || !bytes.Equal(plain, want) {
		t.Fatal("original domain plaintext changed")
	}
	zeroBytes(plain)
	wrong := scope
	wrong.OwnerID += "-wrong"
	if plain, err := box.Open(wrong, envelope); err == nil {
		zeroBytes(plain)
		t.Fatal("runtime envelope not bound to mapped owner")
	}
	if bytes.Contains([]byte(encoded), want) {
		t.Fatal("plaintext leaked into SQL envelope")
	}
}

func TestProductionDomainsRejectInvalidEvidenceBeforeStoreConstruction(t *testing.T) {
	cases := map[string]func(*Snapshot){
		"missing-setting-row":   func(s *Snapshot) { s.Settings = nil },
		"missing-principal-row": func(s *Snapshot) { s.Principals = nil },
		"missing-secret":        func(s *Snapshot) { s.EncryptedSecrets = s.EncryptedSecrets[:1] },
		"wrong-owner":           func(s *Snapshot) { s.EncryptedSecrets[1].OwnerSourceKey = "another-owner" },
		"wrong-kind":            func(s *Snapshot) { s.EncryptedSecrets[1].Kind = "unrelated" },
		"wrong-field":           func(s *Snapshot) { s.EncryptedSecrets[0].Field = "unrelated" },
		"wrong-version":         func(s *Snapshot) { s.EncryptedSecrets[0].KeyVersion = 99 },
		"wrong-sha":             func(s *Snapshot) { s.EncryptedSecrets[0].SHA256 = strings.Repeat("f", 64) },
		"wrong-ciphertext": func(s *Snapshot) {
			s.EncryptedSecrets[1].CiphertextB64 = base64.StdEncoding.EncodeToString([]byte("invalid"))
		},
		"cross-owner-reference": func(s *Snapshot) { s.Principals[0].CredentialSecretRef = s.Settings[0].SecretRef },
		"duplicate-role":        func(s *Snapshot) { s.Principals[0].Roles = []string{"owner", "owner"} },
		"unknown-status":        func(s *Snapshot) { s.Principals[0].Status = "enabled" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot, box, _, _, _ := productionDomainsFixture(t)
			change(&snapshot)
			if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box); err == nil {
				t.Fatal("invalid evidence authorized production store")
			}
		})
	}
	if _, err := NewProductionRQLiteApplyStore(&applyStoreRQLite{}, time.Now, &ProductionCustomerProtection{sourceDigest: strings.Repeat("a", 64)}, nil); err == nil {
		t.Fatal("missing validation enabled production adapter")
	}
}

func TestProductionDomainsRejectChangedApplyRowsAndKeepFixtureConstructor(t *testing.T) {
	snapshot, box, _, _, _ := productionDomainsFixture(t)
	validated, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal(err)
	}
	db := &applyStoreRQLite{}
	store, err := NewProductionRQLiteApplyStore(db, time.Now, validated, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := Plan(snapshot, testPlanOptions())
	setting := snapshot.Settings[0]
	setting.Generation++
	if _, err := store.productionSettingValue(setting, &snapshot.EncryptedSecrets[0]); err == nil {
		t.Fatal("changed setting metadata accepted")
	}
	principal := plan.Principals[0]
	principal.InternalID = strings.Repeat("a", 64)
	if _, err := store.productionPrincipalValue(principal, snapshot.EncryptedSecrets[1]); err == nil {
		t.Fatal("altered canonical target accepted")
	}
	principal = plan.Principals[0]
	principal.Roles = []string{"admin"}
	if _, err := store.productionPrincipalValue(principal, snapshot.EncryptedSecrets[1]); err == nil {
		t.Fatal("changed principal permission accepted")
	}
	secret := snapshot.EncryptedSecrets[1]
	secret.NonceB64 = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, 12))
	if _, err := store.productionPrincipalValue(plan.Principals[0], secret); err == nil {
		t.Fatal("changed encrypted source accepted")
	}
	if len(db.requests) != 0 {
		t.Fatal("invalid rows reached database")
	}
	fixture, err := NewRQLiteApplyStore(db, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if operation.Entity == "principal" {
			statements, err := fixture.principalStatements(ApplyBatch{}, operation)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range statements {
				if strings.Contains(statement.SQL, "INSERT INTO principal_credentials(") {
					var original LegacyEncryptedSecret
					if json.Unmarshal([]byte(statement.Args[2].(string)), &original) != nil || original != snapshot.EncryptedSecrets[1] {
						t.Fatal("fixture-only envelope behavior changed")
					}
				}
			}
		}
	}
}

func TestProductionDomainStoredEnvelopeRequiresActualScopeDigestAndVersion(t *testing.T) {
	snapshot, box, _, _, _ := productionDomainsFixture(t)
	validated, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProductionRQLiteApplyStore(&applyStoreRQLite{}, time.Now, validated, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.productionSettingValue(snapshot.Settings[0], &snapshot.EncryptedSecrets[0])
	if err != nil {
		t.Fatal(err)
	}
	scope := controlplane.SecretScope{OwnerType: "setting", OwnerID: snapshot.Settings[0].Key, Field: "secret", Kind: snapshot.Settings[0].Key}
	version, err := store.productionDomainEnvelopeVersion(value.envelope, scope, value.digest, 6)
	if err != nil || version != 6 {
		t.Fatal("valid stored evidence rejected")
	}
	for _, test := range []struct {
		encoded, digest string
		scope           controlplane.SecretScope
		version         int
	}{
		{value.envelope, value.digest, scope, 7},
		{value.envelope, strings.Repeat("f", 64), scope, 6},
		{value.envelope, value.digest, controlplane.SecretScope{OwnerType: "setting", OwnerID: "another-setting", Field: "secret", Kind: scope.Kind}, 6},
		{"{}", value.digest, scope, 6},
	} {
		if _, err := store.productionDomainEnvelopeVersion(test.encoded, test.scope, test.digest, test.version); err == nil {
			t.Fatal("invalid durable envelope evidence accepted")
		}
	}
}
