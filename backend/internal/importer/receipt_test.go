package importer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func receiptFixture(t *testing.T) (AppliedRunEvidence, controlplane.SchemaIdentity, ed25519.PrivateKey) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return AppliedRunEvidence{
			RunID:              "synthetic-run-1",
			SnapshotKind:       "full",
			SourceDigest:       strings.Repeat("1", 64),
			PlanDigest:         strings.Repeat("2", 64),
			TargetDigest:       strings.Repeat("3", 64),
			BatchCount:         2,
			BatchReceiptDigest: strings.Repeat("4", 64),
			CompletedAtUnix:    2_000_000,
		},
		controlplane.SchemaIdentity{Version: 1, Checksum: strings.Repeat("5", 64)},
		privateKey
}

func TestSignAndVerifyImportReceiptCanonicalRoundTrip(t *testing.T) {
	evidence, schema, privateKey := receiptFixture(t)
	receipt, encoded, err := SignImportReceipt(
		evidence,
		schema,
		strings.Repeat("6", 64),
		privateKey,
	)
	if err != nil {
		t.Fatalf("SignImportReceipt: %v", err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	verified, err := VerifyImportReceipt(encoded, publicKey)
	if err != nil {
		t.Fatalf("VerifyImportReceipt: %v", err)
	}
	if verified != receipt {
		t.Fatalf("verified receipt = %#v, want %#v", verified, receipt)
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(encoded, canonical) {
		t.Fatalf("receipt is not canonical: %q, %v", encoded, err)
	}
}

func TestReceiptSignatureRejectsChangedRunSchemaOrTarget(t *testing.T) {
	evidence, schema, privateKey := receiptFixture(t)
	_, encoded, err := SignImportReceipt(
		evidence,
		schema,
		strings.Repeat("6", 64),
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	var receipt ImportReceipt
	if err := json.Unmarshal(encoded, &receipt); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*ImportReceipt){
		func(value *ImportReceipt) { value.RunID = "changed-run" },
		func(value *ImportReceipt) { value.ControlSchemaChecksum = strings.Repeat("7", 64) },
		func(value *ImportReceipt) { value.TargetDigest = strings.Repeat("8", 64) },
		func(value *ImportReceipt) { value.TargetConfigDigest = strings.Repeat("9", 64) },
	}
	for index, mutate := range mutations {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			changed := receipt
			mutate(&changed)
			tampered, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyImportReceipt(tampered, publicKey); err == nil {
				t.Fatal("changed signed receipt was accepted")
			}
		})
	}
}

func TestReceiptJSONContainsNoBusinessRowsOrSecrets(t *testing.T) {
	evidence, schema, privateKey := receiptFixture(t)
	_, encoded, err := SignImportReceipt(
		evidence,
		schema,
		strings.Repeat("6", 64),
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"customer", "login", "token", "uuid", "sub_id", "ciphertext", "nonce", "secret",
	} {
		if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
			t.Fatalf("receipt leaked business/secret field %q: %s", forbidden, encoded)
		}
	}
}
