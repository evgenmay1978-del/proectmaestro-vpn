package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type runtimeCertificateFixture struct {
	caFile   string
	certFile string
	keyFile  string
	now      time.Time
}

func newRuntimeCertificateFixture(t *testing.T) runtimeCertificateFixture {
	t.Helper()
	now := time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC)
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1001),
		Subject:               pkix.Name{CommonName: "Maestro synthetic import CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	clientPublic, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1002),
		Subject:      pkix.Name{CommonName: "Maestro synthetic import client"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, clientPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	fixture := runtimeCertificateFixture{
		caFile:   filepath.Join(directory, "ca.pem"),
		certFile: filepath.Join(directory, "client.pem"),
		keyFile:  filepath.Join(directory, "client-key.pem"),
		now:      now,
	}
	if err := os.WriteFile(fixture.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func targetConfigJSON(certs runtimeCertificateFixture) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"voters": []any{
			map[string]any{"node_id": "S3", "url": "https://s3.synthetic.invalid:4001"},
			map[string]any{"node_id": "S2", "url": "https://s2.synthetic.invalid:4001"},
			map[string]any{"node_id": "S4", "url": "https://s4.synthetic.invalid:4001"},
		},
		"ca_file":         certs.caFile,
		"cert_file":       certs.certFile,
		"key_file":        certs.keyFile,
		"timeout_seconds": 10,
	}
}

func writeRuntimeJSON(t *testing.T, name string, value any, mode os.FileMode) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, encoded, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadTargetConfigRequiresExactS2S3S4HTTPSMTLS(t *testing.T) {
	certs := newRuntimeCertificateFixture(t)
	validPath := writeRuntimeJSON(t, "target.json", targetConfigJSON(certs), 0o600)
	got, digest, err := loadTargetConfig(validPath, certs.now)
	if err != nil {
		t.Fatalf("loadTargetConfig(valid): %v", err)
	}
	if len(got.Voters) != 3 || got.Voters[0].NodeID != "S2" ||
		got.Voters[1].NodeID != "S3" || got.Voters[2].NodeID != "S4" ||
		len(digest) != 64 {
		t.Fatalf("normalized target = %#v digest=%q", got, digest)
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing voter", func(value map[string]any) {
			value["voters"] = value["voters"].([]any)[:2]
		}},
		{"foreign node", func(value map[string]any) {
			value["voters"].([]any)[0].(map[string]any)["node_id"] = "S1"
		}},
		{"http origin", func(value map[string]any) {
			value["voters"].([]any)[0].(map[string]any)["url"] = "http://s3.synthetic.invalid:4001"
		}},
		{"duplicate origin", func(value map[string]any) {
			value["voters"].([]any)[0].(map[string]any)["url"] = "https://s2.synthetic.invalid:4001"
		}},
		{"userinfo", func(value map[string]any) {
			value["voters"].([]any)[0].(map[string]any)["url"] = "https://user@s3.synthetic.invalid:4001"
		}},
		{"path and query", func(value map[string]any) {
			value["voters"].([]any)[0].(map[string]any)["url"] = "https://s3.synthetic.invalid:4001/db?x=1"
		}},
		{"missing client key", func(value map[string]any) {
			value["key_file"] = ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := targetConfigJSON(certs)
			tc.mutate(value)
			path := writeRuntimeJSON(t, "target.json", value, 0o600)
			if _, _, err := loadTargetConfig(path, certs.now); err == nil {
				t.Fatal("invalid target configuration was accepted")
			}
		})
	}
}

func TestLoadTargetConfigRejectsUnknownFieldsAndBroadPrivateFiles(t *testing.T) {
	certs := newRuntimeCertificateFixture(t)
	unknown := targetConfigJSON(certs)
	unknown["unexpected"] = true
	if _, _, err := loadTargetConfig(
		writeRuntimeJSON(t, "unknown.json", unknown, 0o600), certs.now,
	); err == nil {
		t.Fatal("unknown target field was accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	broadConfig := writeRuntimeJSON(t, "broad-target.json", targetConfigJSON(certs), 0o644)
	if _, _, err := loadTargetConfig(broadConfig, certs.now); err == nil {
		t.Fatal("broad target config mode was accepted")
	}
	strictConfig := writeRuntimeJSON(t, "strict-target.json", targetConfigJSON(certs), 0o600)
	if err := os.Chmod(certs.keyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTargetConfig(strictConfig, certs.now); err == nil {
		t.Fatal("broad client private-key mode was accepted")
	}
}

func validKeyBundleJSON() map[string]any {
	return map[string]any{
		"schema_version":      1,
		"current_key_version": 1,
		"encryption_keys": []any{
			map[string]any{
				"version": 1,
				"key_b64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 32)),
			},
		},
		"hmac_key_b64": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)),
	}
}

func TestLoadKeyBundleRejectsDuplicateMissingOrEqualKeys(t *testing.T) {
	validPath := writeRuntimeJSON(t, "keys.json", validKeyBundleJSON(), 0o600)
	got, err := loadKeyBundle(validPath)
	if err != nil || got.CurrentKeyVersion != 1 || len(got.EncryptionKeys) != 1 ||
		len(got.HMACKey) != 32 {
		t.Fatalf("loadKeyBundle(valid) = %#v, %v", got, err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"duplicate version", func(value map[string]any) {
			value["encryption_keys"] = append(value["encryption_keys"].([]any), value["encryption_keys"].([]any)[0])
		}},
		{"missing current version", func(value map[string]any) {
			value["current_key_version"] = 2
		}},
		{"equal encryption and hmac", func(value map[string]any) {
			value["hmac_key_b64"] = value["encryption_keys"].([]any)[0].(map[string]any)["key_b64"]
		}},
		{"noncanonical base64", func(value map[string]any) {
			value["hmac_key_b64"] = "YQ"
		}},
		{"31 byte key", func(value map[string]any) {
			value["encryption_keys"].([]any)[0].(map[string]any)["key_b64"] =
				base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, 31))
		}},
		{"unknown field", func(value map[string]any) {
			value["unexpected"] = true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := validKeyBundleJSON()
			tc.mutate(value)
			if _, err := loadKeyBundle(writeRuntimeJSON(t, "keys.json", value, 0o600)); err == nil {
				t.Fatal("invalid key bundle was accepted")
			}
		})
	}
}

func TestLoadReceiptSigningKeyRequiresCanonicalEd25519Seed(t *testing.T) {
	seed := bytes.Repeat([]byte{0x61}, ed25519.SeedSize)
	path := writeRuntimeJSON(t, "receipt-key.json", map[string]any{
		"schema_version": 1,
		"seed_b64":       base64.StdEncoding.EncodeToString(seed),
	}, 0o600)
	privateKey, keyID, err := loadReceiptSigningKey(path)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		t.Fatalf("loadReceiptSigningKey = %d bytes, %q, %v", len(privateKey), keyID, err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(publicKey)
	if keyID != hex.EncodeToString(sum[:]) {
		t.Fatalf("signer key id = %q", keyID)
	}
	for _, invalid := range []map[string]any{
		{"schema_version": 1, "seed_b64": "YQ"},
		{"schema_version": 2, "seed_b64": base64.StdEncoding.EncodeToString(seed)},
		{"schema_version": 1, "seed_b64": base64.StdEncoding.EncodeToString(seed), "extra": true},
	} {
		if _, _, err := loadReceiptSigningKey(
			writeRuntimeJSON(t, "invalid-receipt-key.json", invalid, 0o600),
		); err == nil {
			t.Fatal("invalid receipt signing key was accepted")
		}
	}
}

type runtimeFiles struct {
	targetConfig string
	keyBundle    string
	signingKey   string
	hmacKey      []byte
	now          time.Time
}

func newRuntimeFiles(t *testing.T) runtimeFiles {
	t.Helper()
	certs := newRuntimeCertificateFixture(t)
	bundle := validKeyBundleJSON()
	seed := bytes.Repeat([]byte{0x61}, ed25519.SeedSize)
	return runtimeFiles{
		targetConfig: writeRuntimeJSON(t, "target.json", targetConfigJSON(certs), 0o600),
		keyBundle:    writeRuntimeJSON(t, "keys.json", bundle, 0o600),
		signingKey: writeRuntimeJSON(t, "receipt-key.json", map[string]any{
			"schema_version": 1,
			"seed_b64":       base64.StdEncoding.EncodeToString(seed),
		}, 0o600),
		hmacKey: bytes.Repeat([]byte{0x51}, 32),
		now:     certs.now,
	}
}

type runtimeRQLite struct {
	linearResults []rqlite.Result
	requestCalls  int
	linearCalls   int
}

func (f *runtimeRQLite) Request(
	context.Context, rqlite.Consistency, bool, ...rqlite.Statement,
) ([]rqlite.Result, error) {
	f.requestCalls++
	return nil, errors.New("unexpected runtime mutation")
}

func (f *runtimeRQLite) QueryLinearizable(
	_ context.Context, statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	f.linearCalls++
	if len(statements) == 1 && strings.Contains(statements[0].SQL, "SELECT secret_envelope AS envelope") {
		return []rqlite.Result{{}}, nil
	}
	return f.linearResults, nil
}

func (f *runtimeRQLite) QueryStrong(
	context.Context, ...rqlite.Statement,
) ([]rqlite.Result, error) {
	return nil, errors.New("unexpected direct strong query")
}

func (f *runtimeRQLite) Backup(context.Context, io.Writer) error {
	return errors.New("unexpected backup")
}

type runtimeSchemaVerifier struct {
	calls int
}

func (v *runtimeSchemaVerifier) VerifyIdentity(context.Context) (controlplane.SchemaIdentity, error) {
	v.calls++
	return controlplane.SchemaIdentity{Version: 1, Checksum: strings.Repeat("a", 64)}, nil
}

func runtimeConfig(files runtimeFiles, protection importer.SnapshotProtection) applyRuntimeConfig {
	return applyRuntimeConfig{
		TargetConfigFile:   files.targetConfig,
		KeyBundleFile:      files.keyBundle,
		ReceiptSigningFile: files.signingKey,
		Protection:         protection,
	}
}

func runtimeDependencies(
	files runtimeFiles,
	db rqlite.RQLite,
	verifier *runtimeSchemaVerifier,
	newCalls *int,
) productionRuntimeDependencies {
	return productionRuntimeDependencies{
		now: func() time.Time { return files.now },
		newRQLite: func(rqlite.Config) (rqlite.RQLite, error) {
			*newCalls++
			return db, nil
		},
		newVerifier: func(rqlite.RQLite) schemaIdentityVerifier {
			return verifier
		},
	}
}

func TestProductionFactoryValidatesLocalProtectionBeforeNewRQLite(t *testing.T) {
	files := newRuntimeFiles(t)
	newCalls := 0
	verifier := &runtimeSchemaVerifier{}
	db := &runtimeRQLite{}
	protection := importer.SnapshotProtection{
		ClusterHMACKeySHA256: strings.Repeat("f", 64),
	}
	_, err := buildProductionApplyRuntime(
		context.Background(),
		runtimeConfig(files, protection),
		runtimeDependencies(files, db, verifier, &newCalls),
	)
	if err == nil {
		t.Fatal("factory accepted mismatched local protection")
	}
	if newCalls != 0 || verifier.calls != 0 || db.requestCalls != 0 {
		t.Fatalf("network boundary crossed: new=%d verify=%d request=%d", newCalls, verifier.calls, db.requestCalls)
	}
}

func TestProductionFactoryCallsVerifyIdentityButNeverApply(t *testing.T) {
	files := newRuntimeFiles(t)
	newCalls := 0
	verifier := &runtimeSchemaVerifier{}
	db := &runtimeRQLite{linearResults: []rqlite.Result{{
		Rows: []map[string]any{{"key_version": int64(1)}},
	}}}
	protection := importer.ProtectionFromSnapshot(importer.Snapshot{
		ClusterHMACKeySHA256: hexSHA256(files.hmacKey),
	})
	got, err := buildProductionApplyRuntime(
		context.Background(),
		runtimeConfig(files, protection),
		runtimeDependencies(files, db, verifier, &newCalls),
	)
	if err != nil {
		t.Fatalf("buildProductionApplyRuntime: %v", err)
	}
	if got == nil || got.Store == nil || verifier.calls != 1 || newCalls != 1 ||
		db.linearCalls != 2 || db.requestCalls != 0 {
		t.Fatalf("runtime=%#v new=%d verify=%d linear=%d request=%d",
			got, newCalls, verifier.calls, db.linearCalls, db.requestCalls)
	}
}

func TestProductionFactoryRejectsMissingTargetKeyVersionBeforeMutation(t *testing.T) {
	files := newRuntimeFiles(t)
	newCalls := 0
	verifier := &runtimeSchemaVerifier{}
	db := &runtimeRQLite{linearResults: []rqlite.Result{{
		Rows: []map[string]any{{"key_version": int64(2)}},
	}}}
	protection := importer.ProtectionFromSnapshot(importer.Snapshot{
		ClusterHMACKeySHA256: hexSHA256(files.hmacKey),
	})
	if _, err := buildProductionApplyRuntime(
		context.Background(),
		runtimeConfig(files, protection),
		runtimeDependencies(files, db, verifier, &newCalls),
	); err == nil {
		t.Fatal("factory accepted a missing target key version")
	}
	if db.requestCalls != 0 {
		t.Fatalf("missing target key performed %d mutation(s)", db.requestCalls)
	}
}

func protectRuntimeCustomerIdentity(t *testing.T, snapshot *importer.Snapshot, box *controlplane.SecretBox, identity importer.ProductionCustomerIdentity) {
	t.Helper()
	row := &snapshot.Customers[0]
	canonical, err := controlplane.CanonicalLoginKey(identity.Customer.Login)
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
	snapshot.EncryptedSecrets = []importer.LegacyEncryptedSecret{{SecretID: row.IdentitySecretRef,
		OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind,
		KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: hexSHA256(plaintext)}}
}

func TestProductionFactoryRejectsUnsupportedTypedIdentityBeforeNetwork(t *testing.T) {
	files := newRuntimeFiles(t)
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x41}, 32)}, files.hmacKey)
	if err != nil {
		t.Fatal(err)
	}
	identity := importer.ProductionCustomerIdentity{SchemaVersion: 1, SubID: "synthetic-sub-id", Generation: 1,
		Customer: legacystore.Customer{Login: "SyntheticFactoryCustomer", SubToken: "synthetic-token", Expires: time.Unix(2_100_000_000, 0),
			VLESS: &subgen.VLESSCreds{UUID: "4d29cf7f-8581-4243-baba-d39eb481256c"},
			WG:    &subgen.WGCreds{PrivateKey: "PRIVATE-WG-MARKER"}}}
	snapshot := importer.Snapshot{FormatVersion: 2, SnapshotKind: "full", CapturedAt: time.Unix(2_000_000_000, 0).UTC(), ClusterHMACKeySHA256: hexSHA256(files.hmacKey),
		Customers: []importer.LegacyCustomer{{SourceKey: "factory-customer", IdentitySecretRef: "factory-customer-secret",
			Login: identity.Customer.Login, Generation: identity.Generation, ExpiresAtUnix: identity.Customer.Expires.Unix(),
			Status: "active", ProtocolTags: []string{"vless"}, NodeIDs: []string{"S1"}}}}
	protectRuntimeCustomerIdentity(t, &snapshot, box, identity)
	newCalls := 0
	db, verifier := &runtimeRQLite{}, &runtimeSchemaVerifier{}
	_, err = buildProductionApplyRuntime(context.Background(), runtimeConfig(files, importer.ProtectionFromSnapshot(snapshot)),
		runtimeDependencies(files, db, verifier, &newCalls))
	if err == nil || strings.Contains(err.Error(), "PRIVATE-WG-MARKER") || newCalls != 0 || verifier.calls != 0 || db.requestCalls != 0 {
		t.Fatal("unsupported typed legacy identity crossed the production pre-apply boundary")
	}
}

func TestProductionFactoryErrorTextIsSecretFree(t *testing.T) {
	files := newRuntimeFiles(t)
	marker := "PRIVATE-RUNTIME-MARKER"
	protection := importer.SnapshotProtection{
		ClusterHMACKeySHA256: hexSHA256(files.hmacKey),
		EncryptedSecrets: []importer.LegacyEncryptedSecret{{
			SecretID:  "synthetic-secret",
			OwnerType: "customer", OwnerSourceKey: "synthetic-owner",
			Field: "identity", Kind: "subscription", KeyVersion: 1,
			NonceB64:      base64.StdEncoding.EncodeToString([]byte(marker)),
			CiphertextB64: base64.StdEncoding.EncodeToString([]byte(marker)),
			SHA256:        strings.Repeat("a", 64),
		}},
	}
	newCalls := 0
	verifier := &runtimeSchemaVerifier{}
	db := &runtimeRQLite{}
	_, err := buildProductionApplyRuntime(
		context.Background(),
		runtimeConfig(files, protection),
		runtimeDependencies(files, db, verifier, &newCalls),
	)
	if err == nil {
		t.Fatal("invalid secret envelope was accepted")
	}
	for _, forbidden := range []string{marker, files.targetConfig, files.keyBundle, files.signingKey} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error leaked private material: %q", err)
		}
	}
	if newCalls != 0 {
		t.Fatalf("secret validation opened network %d time(s)", newCalls)
	}
}

func hexSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
