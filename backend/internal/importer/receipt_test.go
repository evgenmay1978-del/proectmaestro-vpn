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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	// Check the public receipt schema, not arbitrary substrings in a valid
	// cryptographic signature. This full snapshot omits parent_source_sha256.
	allowed := []string{
		"schema_version", "run_id", "snapshot_kind", "source_sha256", "plan_sha256",
		"target_sha256", "batch_count", "batch_receipt_sha256", "control_schema_version",
		"control_schema_checksum", "target_config_sha256", "signer_key_id",
		"completed_at_unix", "signature_b64",
	}
	if len(fields) != len(allowed) {
		t.Fatalf("receipt contains %d fields, want exactly %d public fields", len(fields), len(allowed))
	}
	for _, name := range allowed {
		if _, ok := fields[name]; !ok {
			t.Fatalf("receipt is missing public field %q or contains a replacement business/secret field", name)
		}
	}
}
