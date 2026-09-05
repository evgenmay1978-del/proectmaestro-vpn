package importer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func productionIdentityFixture(t *testing.T) (Snapshot, ProductionCustomerIdentity, *controlplane.SecretBox) {
	t.Helper()
	hmacKey := bytes.Repeat([]byte{0x22}, 32)
	box, err := controlplane.NewSecretBox(7, map[int][]byte{7: bytes.Repeat([]byte{0x11}, 32)}, hmacKey)
	if err != nil {
		t.Fatal(err)
	}
	const login = "ImportedReaderFixture"
	const uuid = "4d29cf7f-8581-4243-baba-d39eb481256c"
	identity := ProductionCustomerIdentity{SchemaVersion: 1, SubID: "synthetic-original-sub-id", Generation: 7,
		Customer: legacystore.Customer{Login: login, SubToken: "synthetic-original-sub-token", Expires: time.Unix(2_100_000, 0).UTC(),
			VLESS:  &subgen.VLESSCreds{Server: "s1.example.test", Port: 443, UUID: uuid, SNI: "example.test", Fingerprint: "chrome"},
			VLESS3: &subgen.VLESSCreds{Server: "s3.example.test", Port: 443, UUID: uuid, SNI: "example.test", Fingerprint: "chrome"},
			VLESS4: &subgen.VLESSCreds{Server: "s4.example.test", Port: 443, UUID: uuid, SNI: "example.test", Fingerprint: "chrome"},
			Hy2:    &subgen.Hy2Creds{Server: "hy.example.test", Port: 443, User: login, Pass: "synthetic-hy-password", SNI: "example.test"},
			Naive:  &subgen.NaiveCreds{Server: "naive.example.test", Port: 443, Username: login, Password: "synthetic-naive-password", SNI: "example.test"},
			AnyTLS: &subgen.AnyTLSCreds{Server: "tls.example.test", Port: 443, Password: "synthetic-anytls-password", SNI: "example.test"},
		}}
	snapshot := decodeFixture(t, "customers-valid.json")
	snapshot.ClusterHMACKeySHA256 = sha256Hex(hmacKey)
	snapshot.Customers[0].SourceKey = "s1:customer:production-reader-fixture"
	snapshot.Customers[0].IdentitySecretRef = "production-reader-identity-fixture"
	snapshot.Customers[0].ProtocolTags = []string{"vless", "hysteria2", "naive", "anytls"}
	setProductionFixtureIdentity(t, &snapshot, identity, box)
	return snapshot, identity, box
}

func setProductionFixtureIdentity(t *testing.T, snapshot *Snapshot, identity ProductionCustomerIdentity, box *controlplane.SecretBox) {
	t.Helper()
	row := &snapshot.Customers[0]
	row.Login = identity.Customer.Login
	row.ExpiresAtUnix = identity.Customer.Expires.Unix()
	row.Generation = identity.Generation
	row.Status = "active"
	if identity.Customer.Disabled {
		row.Status = "disabled"
	}
	canonical, err := controlplane.CanonicalLoginKey(row.Login)
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[string]string{"vless": identity.Customer.VLESS.UUID}
	if identity.Customer.Hy2 != nil {
		credentials["hysteria2"] = identity.Customer.Hy2.Pass
	}
	if identity.Customer.Naive != nil {
		credentials["naive"] = identity.Customer.Naive.Password
	}
	if identity.Customer.AnyTLS != nil {
		credentials["anytls"] = identity.Customer.AnyTLS.Password
	}
	fingerprint, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	row.LoginKeyHMAC = box.LookupHMAC("customer-login", []byte(canonical))
	row.UUIDHMAC = box.LookupHMAC("customer-uuid", []byte(identity.Customer.VLESS.UUID))
	row.SubIDHMAC = box.LookupHMAC("subscription-id", []byte(identity.SubID))
	row.TokenHMAC = box.LookupHMAC("subscription-token", []byte(identity.Customer.SubToken))
	row.CredentialFingerprintHMAC = box.LookupHMAC("customer-credentials", fingerprint)
	plaintext, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	scope := controlplane.SecretScope{OwnerType: "customer", OwnerID: row.SourceKey, Field: "identity", Kind: "customer-identity"}
	envelope, err := box.Seal(scope, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.EncryptedSecrets = []LegacyEncryptedSecret{{SecretID: row.IdentitySecretRef,
		OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind,
		KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha256Hex(plaintext)}}
}

func TestProductionIdentityDerivesExactReaderScopesAndRetainsOriginal(t *testing.T) {
	snapshot, identity, box := productionIdentityFixture(t)
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal(err)
	}
	db := &applyStoreRQLite{}
	store, err := NewProductionRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) }, protection, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("synthetic production plan blocked")
	}
	operations, err := planOperations(plan)
	if err != nil || len(operations) != 1 {
		t.Fatal("synthetic customer operation unavailable")
	}
	statements, err := store.customerStatements(ApplyBatch{}, operations[0])
	if err != nil {
		t.Fatal(err)
	}
	want, err := productionCredentials(identity)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	tokenFound, originalFound := false, false
	for _, statement := range statements {
		var encoded, field, kind string
		switch {
		case strings.Contains(statement.SQL, "INSERT INTO credentials("):
			kind = statement.Args[2].(string)
			encoded, field = statement.Args[3].(string), "credential"
		case strings.Contains(statement.SQL, "INSERT INTO subscription_tokens("):
			encoded, field, kind = statement.Args[3].(string), "token", "subscription"
		case strings.Contains(statement.SQL, "INSERT INTO imported_secrets("):
			var preserved LegacyEncryptedSecret
			if json.Unmarshal([]byte(statement.Args[6].(string)), &preserved) != nil || preserved != snapshot.EncryptedSecrets[0] {
				t.Fatal("original encrypted identity was not preserved")
			}
			originalFound = true
			continue
		default:
			continue
		}
		rawEnvelope, err := base64.StdEncoding.DecodeString(encoded)
		var envelope controlplane.Envelope
		if err != nil || json.Unmarshal(rawEnvelope, &envelope) != nil {
			t.Fatal("derived envelope is not in production reader format")
		}
		plain, err := box.Open(controlplane.SecretScope{OwnerType: "customer", OwnerID: plan.Customers[0].InternalID, Field: field, Kind: kind}, envelope)
		if err != nil {
			t.Fatal("derived envelope does not authenticate under production scope")
		}
		if field == "token" {
			tokenFound = string(plain) == identity.Customer.SubToken
		} else {
			got[kind] = string(plain)
		}
		if _, err := box.Open(controlplane.SecretScope{OwnerType: "customer", OwnerID: snapshot.Customers[0].SourceKey, Field: field, Kind: kind}, envelope); err == nil {
			t.Fatal("derived envelope accepted legacy source-key scope")
		}
	}
	if !reflect.DeepEqual(got, want) || !tokenFound || !originalFound {
		t.Fatal("derived ordinary access lost original values")
	}
	if len(db.requests) != 0 {
		t.Fatal("preparing protected customer values wrote to the database")
	}
	_, err = store.BeginOrResume(context.Background(), ApplyRun{SourceDigest: strings.Repeat("a", 64)})
	if err == nil || len(db.requests) != 0 {
		t.Fatal("a different snapshot reached apply writes")
	}
}

func TestProductionIdentityRejectsUnsupportedMappingBeforeStoreCreation(t *testing.T) {
	cases := map[string]func(*Snapshot, *ProductionCustomerIdentity){
		"distinct-vless3": func(_ *Snapshot, i *ProductionCustomerIdentity) { i.Customer.VLESS3.UUID = "different" },
		"distinct-vless4": func(_ *Snapshot, i *ProductionCustomerIdentity) { i.Customer.VLESS4.UUID = "different" },
		"devices": func(_ *Snapshot, i *ProductionCustomerIdentity) {
			i.Customer.Devices = map[string]time.Time{"synthetic-device": time.Unix(1, 0)}
		},
		"wireguard": func(_ *Snapshot, i *ProductionCustomerIdentity) {
			i.Customer.WG = &subgen.WGCreds{PrivateKey: "synthetic"}
		},
		"hy2-login":          func(_ *Snapshot, i *ProductionCustomerIdentity) { i.Customer.Hy2.User = "other" },
		"naive-login":        func(_ *Snapshot, i *ProductionCustomerIdentity) { i.Customer.Naive.Username = "other" },
		"missing-sub-id":     func(_ *Snapshot, i *ProductionCustomerIdentity) { i.SubID = "" },
		"missing-generation": func(_ *Snapshot, i *ProductionCustomerIdentity) { i.Generation = 0 },
		"missing-protocol":   func(s *Snapshot, _ *ProductionCustomerIdentity) { s.Customers[0].ProtocolTags = []string{"vless"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot, identity, box := productionIdentityFixture(t)
			mutate(&snapshot, &identity)
			setProductionFixtureIdentity(t, &snapshot, identity, box)
			if protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box); err == nil || protection != nil {
				t.Fatal("unsupported identity produced an enabled production adapter")
			}
		})
	}
	for _, field := range []string{"login", "token", "uuid", "sub-id", "fingerprint", "expiry", "generation", "status"} {
		t.Run("mismatch-"+field, func(t *testing.T) {
			snapshot, _, box := productionIdentityFixture(t)
			row := &snapshot.Customers[0]
			switch field {
			case "login":
				row.LoginKeyHMAC = strings.Repeat("a", 64)
			case "token":
				row.TokenHMAC = strings.Repeat("a", 64)
			case "uuid":
				row.UUIDHMAC = strings.Repeat("a", 64)
			case "sub-id":
				row.SubIDHMAC = strings.Repeat("a", 64)
			case "fingerprint":
				row.CredentialFingerprintHMAC = strings.Repeat("a", 64)
			case "expiry":
				row.ExpiresAtUnix++
			case "generation":
				row.Generation++
			case "status":
				row.Status = "disabled"
			}
			if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box); err == nil {
				t.Fatal("identity metadata mismatch accepted")
			}
		})
	}
}

func TestProductionIdentityRejectsOpaqueLegacyFixtureOnlyInProduction(t *testing.T) {
	snapshot, _, box := productionIdentityFixture(t)
	opaque := decodeFixture(t, "customers-valid.json")
	if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(opaque), box); err == nil {
		t.Fatal("opaque fixture accepted by production adapter")
	}
	protection := ProtectionFromSnapshot(snapshot)
	snapshot.Customers[0].ProtocolTags[0] = "changed"
	if protection.Customers[0].ProtocolTags[0] == "changed" {
		t.Fatal("snapshot protection shares mutable customer slices")
	}
}

func TestProductionIdentityTracksDerivedEnvelopeKeyVersions(t *testing.T) {
	snapshot, _, box := productionIdentityFixture(t)
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := controlplane.NewSecretBox(8, map[int][]byte{8: bytes.Repeat([]byte{0x33}, 32)}, bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatal(err)
	}
	seal := &ProductionCustomerProtection{box: rotated}
	value, err := seal.sealValue("synthetic-customer", "token", "subscription", "synthetic-token")
	if err != nil {
		t.Fatal(err)
	}
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: []map[string]any{{"key_version": int64(7)}}}},
		{{Rows: []map[string]any{{"envelope": value.envelope}}}},
	}}
	store, err := NewProductionRQLiteApplyStore(db, time.Now, protection, nil)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := store.ReadReferencedKeyVersions(context.Background())
	if err != nil || !reflect.DeepEqual(versions, []int{7, 8}) {
		t.Fatal("key readiness omitted either original or reader-derived envelope key")
	}
}
