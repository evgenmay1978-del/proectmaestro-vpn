package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type snapshotProtectionFixture struct {
	snapshot   Snapshot
	box        *controlplane.SecretBox
	hmacKey    []byte
	trialSalt  []byte
	plaintexts [][]byte
}

func newSnapshotProtectionFixture(t *testing.T) snapshotProtectionFixture {
	t.Helper()
	encryptionKey := bytes.Repeat([]byte{0x11}, 32)
	hmacKey := bytes.Repeat([]byte{0x22}, 32)
	trialSalt := []byte("synthetic-trial-salt-with-exact-bytes")
	box, err := controlplane.NewSecretBox(7, map[int][]byte{7: encryptionKey}, hmacKey)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	scopes := []controlplane.SecretScope{
		{OwnerType: "customer", OwnerID: "customer-alpha", Field: "identity", Kind: "subscription"},
		{OwnerType: "principal", OwnerID: "admin-alpha", Field: "credential", Kind: "password-hash"},
	}
	plaintexts := [][]byte{
		[]byte("synthetic-customer-secret"),
		[]byte("synthetic-principal-secret"),
	}
	secrets := make([]LegacyEncryptedSecret, 0, len(scopes))
	for index, scope := range scopes {
		envelope, err := box.Seal(scope, plaintexts[index])
		if err != nil {
			t.Fatalf("Seal(%d): %v", index, err)
		}
		secrets = append(secrets, LegacyEncryptedSecret{
			SecretID:       []string{"secret-customer-alpha", "secret-admin-alpha"}[index],
			OwnerType:      scope.OwnerType,
			OwnerSourceKey: scope.OwnerID,
			Field:          scope.Field,
			Kind:           scope.Kind,
			KeyVersion:     envelope.KeyVersion,
			NonceB64:       base64.StdEncoding.EncodeToString(envelope.Nonce),
			CiphertextB64:  base64.StdEncoding.EncodeToString(envelope.Ciphertext),
			SHA256:         sha256Hex(plaintexts[index]),
		})
	}
	return snapshotProtectionFixture{
		snapshot: Snapshot{
			FormatVersion:           2,
			SnapshotKind:            "full",
			ClusterHMACKeySHA256:    sha256Hex(hmacKey),
			LegacyTrialSaltSHA256:   sha256Hex(trialSalt),
			Trials:                  []LegacyTrial{{SourceKey: "trial-alpha"}},
			EncryptedSecrets:        secrets,
		},
		box:        box,
		hmacKey:    hmacKey,
		trialSalt:  trialSalt,
		plaintexts: plaintexts,
	}
}

func TestValidateSnapshotProtectionAuthenticatesEveryEnvelope(t *testing.T) {
	fixture := newSnapshotProtectionFixture(t)
	protection := ProtectionFromSnapshot(fixture.snapshot)
	if _, err := ValidateSnapshotProtection(protection, fixture.box, fixture.hmacKey, fixture.trialSalt); err != nil {
		t.Fatalf("ValidateSnapshotProtection: %v", err)
	}

	tampered := ProtectionFromSnapshot(fixture.snapshot)
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(tampered.EncryptedSecrets[1].CiphertextB64)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 0x01
	tampered.EncryptedSecrets[1].CiphertextB64 = base64.StdEncoding.EncodeToString(ciphertext)
	if _, err := ValidateSnapshotProtection(tampered, fixture.box, fixture.hmacKey, fixture.trialSalt); err == nil {
		t.Fatal("tampered second envelope authenticated")
	}
}

func TestValidateSnapshotProtectionRejectsWrongOwnerScope(t *testing.T) {
	fixture := newSnapshotProtectionFixture(t)
	protection := ProtectionFromSnapshot(fixture.snapshot)
	protection.EncryptedSecrets[0].OwnerSourceKey = "moved-customer"
	_, err := ValidateSnapshotProtection(protection, fixture.box, fixture.hmacKey, fixture.trialSalt)
	if err == nil {
		t.Fatal("wrong owner scope authenticated")
	}
	for _, forbidden := range fixture.plaintexts {
		if strings.Contains(err.Error(), string(forbidden)) {
			t.Fatalf("error leaked plaintext: %q", err)
		}
	}
}

func TestValidateSnapshotProtectionRejectsWrongHMACKeyBeforeStore(t *testing.T) {
	fixture := newSnapshotProtectionFixture(t)
	wrong := bytes.Repeat([]byte{0x33}, 32)
	if _, err := ValidateSnapshotProtection(
		ProtectionFromSnapshot(fixture.snapshot), fixture.box, wrong, fixture.trialSalt,
	); err == nil {
		t.Fatal("wrong cluster HMAC key was accepted")
	}
}

func TestValidateSnapshotProtectionRejectsTrialSaltNewlineDrift(t *testing.T) {
	fixture := newSnapshotProtectionFixture(t)
	drifted := append(append([]byte(nil), fixture.trialSalt...), '\n')
	if _, err := ValidateSnapshotProtection(
		ProtectionFromSnapshot(fixture.snapshot), fixture.box, fixture.hmacKey, drifted,
	); err == nil {
		t.Fatal("trial salt newline drift was accepted")
	}
}

func TestValidateSnapshotProtectionRequiresNoSaltWhenTrialsAreAbsent(t *testing.T) {
	fixture := newSnapshotProtectionFixture(t)
	fixture.snapshot.Trials = nil
	fixture.snapshot.LegacyTrialSaltSHA256 = ""
	protection := ProtectionFromSnapshot(fixture.snapshot)
	got, err := ValidateSnapshotProtection(protection, fixture.box, fixture.hmacKey, nil)
	if err != nil || got != nil {
		t.Fatalf("trial-free validation = %#v, %v", got, err)
	}
	if _, err := ValidateSnapshotProtection(protection, fixture.box, fixture.hmacKey, []byte("unexpected")); err == nil {
		t.Fatal("trial-free snapshot accepted a salt")
	}
}

func TestValidateSnapshotProtectionReturnsSealedTrialSalt(t *testing.T) {
	fixture := newSnapshotProtectionFixture(t)
	got, err := ValidateSnapshotProtection(
		ProtectionFromSnapshot(fixture.snapshot), fixture.box, fixture.hmacKey, fixture.trialSalt,
	)
	if err != nil {
		t.Fatalf("ValidateSnapshotProtection: %v", err)
	}
	if got == nil || got.KeyVersion != 7 || got.SaltSHA256 != sha256Hex(fixture.trialSalt) {
		t.Fatalf("trial protection = %#v", got)
	}
	var encoded struct {
		KeyVersion    int    `json:"key_version"`
		NonceB64      string `json:"nonce_b64"`
		CiphertextB64 string `json:"ciphertext_b64"`
	}
	if err := json.Unmarshal([]byte(got.EncryptedSaltEnvelope), &encoded); err != nil {
		t.Fatalf("decode returned envelope: %v", err)
	}
	canonical, err := json.Marshal(encoded)
	if err != nil || string(canonical) != got.EncryptedSaltEnvelope {
		t.Fatalf("returned envelope is not canonical: %q, %v", got.EncryptedSaltEnvelope, err)
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(encoded.NonceB64)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(encoded.CiphertextB64)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.box.Open(controlplane.SecretScope{
		OwnerType: "trial_lookup",
		OwnerID:   "legacy",
		Field:     "salt",
		Kind:      "hmac-key",
	}, controlplane.Envelope{
		KeyVersion: encoded.KeyVersion,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	})
	if err != nil || !bytes.Equal(plaintext, fixture.trialSalt) {
		t.Fatalf("open returned trial salt = %q, %v", plaintext, err)
	}
}
