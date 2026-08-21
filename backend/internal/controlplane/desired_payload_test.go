package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestDesiredPayloadRoundTripBindsEnvelopeAndBodyDigests(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x11})
	scope := desiredPayloadTestScope()
	body := map[string]any{
		"endpoint": "https://example.invalid/subscription",
		"protocol": "vless",
	}

	envelope, envelopeSHA256, err := box.SealDesiredPayload(scope, body)
	if err != nil {
		t.Fatalf("SealDesiredPayload: %v", err)
	}
	if envelope.KeyVersion != 1 {
		t.Fatalf("envelope key version = %d, want 1", envelope.KeyVersion)
	}
	if len(envelopeSHA256) != sha256.Size*2 {
		t.Fatalf("envelope digest length = %d, want %d", len(envelopeSHA256), sha256.Size*2)
	}
	if _, err := hex.DecodeString(envelopeSHA256); err != nil {
		t.Fatalf("envelope digest is not hex: %v", err)
	}
	if want := desiredPayloadTestEnvelopeDigest(t, envelope); envelopeSHA256 != want {
		t.Fatalf("envelope digest = %s, want SHA-256 of exact canonical envelope %s", envelopeSHA256, want)
	}

	document, err := box.OpenDesiredPayload(scope, envelope, envelopeSHA256)
	if err != nil {
		t.Fatalf("OpenDesiredPayload: %v", err)
	}
	if document.Version != DesiredPayloadVersion || document.Kind != scope.PayloadKind {
		t.Fatalf("document = %#v, want version %d and kind %q", document, DesiredPayloadVersion, scope.PayloadKind)
	}
	if !json.Valid(document.Body) || document.BodySHA256 != desiredPayloadTestDigest(document.Body) {
		t.Fatalf("document body digest is not bound to canonical body: %#v", document)
	}
}

func TestDesiredPayloadAADRejectsEveryScopeMutation(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x12})
	scope := desiredPayloadTestScope()
	envelope, digest, err := box.SealDesiredPayload(scope, map[string]string{"endpoint": "https://example.invalid/subscription"})
	if err != nil {
		t.Fatalf("SealDesiredPayload: %v", err)
	}

	mutations := []struct {
		name  string
		scope DesiredPayloadScope
	}{
		{name: "node", scope: DesiredPayloadScope{NodeID: "node-b", ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, Tombstone: scope.Tombstone, PayloadKind: scope.PayloadKind}},
		{name: "service", scope: DesiredPayloadScope{NodeID: scope.NodeID, ServiceID: "service-b", CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, Tombstone: scope.Tombstone, PayloadKind: scope.PayloadKind}},
		{name: "customer", scope: DesiredPayloadScope{NodeID: scope.NodeID, ServiceID: scope.ServiceID, CustomerID: "customer-b", Generation: scope.Generation, OperationID: scope.OperationID, Tombstone: scope.Tombstone, PayloadKind: scope.PayloadKind}},
		{name: "generation", scope: DesiredPayloadScope{NodeID: scope.NodeID, ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation + 1, OperationID: scope.OperationID, Tombstone: scope.Tombstone, PayloadKind: scope.PayloadKind}},
		{name: "operation", scope: DesiredPayloadScope{NodeID: scope.NodeID, ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: "operation-b", Tombstone: scope.Tombstone, PayloadKind: scope.PayloadKind}},
		{name: "tombstone", scope: DesiredPayloadScope{NodeID: scope.NodeID, ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, Tombstone: true, PayloadKind: scope.PayloadKind}},
		{name: "payload kind", scope: DesiredPayloadScope{NodeID: scope.NodeID, ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, Tombstone: scope.Tombstone, PayloadKind: "hysteria2"}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := box.OpenDesiredPayload(mutation.scope, envelope, digest); err == nil {
				t.Fatal("OpenDesiredPayload authenticated a mutated destination scope")
			}
		})
	}
}

func TestDesiredPayloadRejectsUnknownVersionMalformedBodyOrDigest(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x13})
	scope := desiredPayloadTestScope()
	canonicalBody := json.RawMessage(`{"endpoint":"https://example.invalid/subscription"}`)
	valid := DesiredPayloadDocument{
		Version:    DesiredPayloadVersion,
		Kind:       scope.PayloadKind,
		Body:       canonicalBody,
		BodySHA256: desiredPayloadTestDigest(canonicalBody),
	}
	unknownVersion := valid
	unknownVersion.Version++
	badBodyDigest := valid
	badBodyDigest.BodySHA256 = strings.Repeat("0", sha256.Size*2)

	cases := []struct {
		name           string
		plaintext      []byte
		envelopeDigest string
	}{
		{name: "unknown document version", plaintext: desiredPayloadTestJSON(t, unknownVersion)},
		{name: "malformed document JSON", plaintext: []byte(`{"version":1`)},
		{name: "noncanonical body", plaintext: desiredPayloadTestJSON(t, DesiredPayloadDocument{Version: DesiredPayloadVersion, Kind: scope.PayloadKind, Body: json.RawMessage(`{"z":1,"a":2}`), BodySHA256: desiredPayloadTestDigest(json.RawMessage(`{"z":1,"a":2}`))})},
		{name: "body digest mismatch", plaintext: desiredPayloadTestJSON(t, badBodyDigest)},
		{name: "envelope digest mismatch", plaintext: desiredPayloadTestJSON(t, valid), envelopeDigest: strings.Repeat("f", sha256.Size*2)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			envelope := sealDesiredPayloadDocumentForTest(t, box, scope, testCase.plaintext)
			digest := testCase.envelopeDigest
			if digest == "" {
				digest = desiredPayloadTestEnvelopeDigest(t, envelope)
			}
			if _, err := box.OpenDesiredPayload(scope, envelope, digest); err == nil {
				t.Fatal("OpenDesiredPayload accepted an invalid desired payload document")
			}
		})
	}
	validEnvelope, validDigest, err := box.SealDesiredPayload(scope, map[string]string{"endpoint": "https://example.invalid/subscription"})
	if err != nil {
		t.Fatalf("SealDesiredPayload: %v", err)
	}
	validEnvelope.KeyVersion = 99
	if _, err := box.OpenDesiredPayload(scope, validEnvelope, validDigest); err == nil {
		t.Fatal("OpenDesiredPayload accepted an unknown envelope key version")
	}
}

func TestDesiredPayloadRotationReadsReferencedOldVersionAndSealsCurrent(t *testing.T) {
	keys := map[int]byte{1: 0x14, 2: 0x15}
	oldBox := newDesiredPayloadTestBox(t, 1, keys)
	scope := desiredPayloadTestScope()
	oldEnvelope, oldDigest, err := oldBox.SealDesiredPayload(scope, map[string]string{"endpoint": "https://example.invalid/old"})
	if err != nil {
		t.Fatalf("old SealDesiredPayload: %v", err)
	}
	rotated := newDesiredPayloadTestBox(t, 2, keys)
	if _, err := rotated.OpenDesiredPayload(scope, oldEnvelope, oldDigest); err != nil {
		t.Fatalf("rotated OpenDesiredPayload old envelope: %v", err)
	}
	newEnvelope, _, err := rotated.SealDesiredPayload(scope, map[string]string{"endpoint": "https://example.invalid/new"})
	if err != nil {
		t.Fatalf("rotated SealDesiredPayload: %v", err)
	}
	if newEnvelope.KeyVersion != 2 {
		t.Fatalf("new envelope key version = %d, want 2", newEnvelope.KeyVersion)
	}
}

func TestDesiredTombstoneContainsNoReusableCredential(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x16})
	scope := desiredPayloadTestScope()
	scope.Tombstone = true
	envelope, digest, err := box.SealDesiredPayload(scope, nil)
	if err != nil {
		t.Fatalf("SealDesiredPayload tombstone: %v", err)
	}
	document, err := box.OpenDesiredPayload(scope, envelope, digest)
	if err != nil {
		t.Fatalf("OpenDesiredPayload tombstone: %v", err)
	}
	if got, want := string(document.Body), `{"tombstone":true}`; got != want {
		t.Fatalf("tombstone body = %s, want %s", got, want)
	}
	if strings.Contains(string(document.Body), "credential") {
		t.Fatal("tombstone body contains reusable credential material")
	}
}

func TestDesiredPayloadCanonicalizesEquivalentJSONNumbers(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x17})
	scope := desiredPayloadTestScope()
	for _, body := range []json.RawMessage{json.RawMessage(`{"n":1}`), json.RawMessage(`{"n":1.0}`), json.RawMessage(`{"n":1e0}`)} {
		envelope, digest, err := box.SealDesiredPayload(scope, body)
		if err != nil {
			t.Fatalf("SealDesiredPayload(%s): %v", body, err)
		}
		document, err := box.OpenDesiredPayload(scope, envelope, digest)
		if err != nil {
			t.Fatalf("OpenDesiredPayload(%s): %v", body, err)
		}
		if got, want := string(document.Body), `{"n":1}`; got != want {
			t.Fatalf("canonical body = %s, want %s", got, want)
		}
	}
}

func TestDesiredPayloadAbsentBodyUsesEmptyDigest(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x18})
	scope := desiredPayloadTestScope()
	envelope, digest, err := box.SealDesiredPayload(scope, nil)
	if err != nil {
		t.Fatalf("SealDesiredPayload absent body: %v", err)
	}
	document, err := box.OpenDesiredPayload(scope, envelope, digest)
	if err != nil {
		t.Fatalf("OpenDesiredPayload absent body: %v", err)
	}
	if document.Body != nil || document.BodySHA256 != desiredPayloadTestDigest(nil) {
		t.Fatalf("absent body document = %#v, want omitted body with empty digest", document)
	}
}

func TestDesiredPayloadRejectsInvalidScopeAndDocumentFraming(t *testing.T) {
	box := newDesiredPayloadTestBox(t, 1, map[int]byte{1: 0x19})
	scope := desiredPayloadTestScope()
	for _, invalid := range []DesiredPayloadScope{
		{NodeID: "", ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, PayloadKind: scope.PayloadKind},
		{NodeID: strings.Repeat("n", maxSecretScopePart+1), ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, PayloadKind: scope.PayloadKind},
		{NodeID: string([]byte{0xff}), ServiceID: scope.ServiceID, CustomerID: scope.CustomerID, Generation: scope.Generation, OperationID: scope.OperationID, PayloadKind: scope.PayloadKind},
	} {
		if _, _, err := box.SealDesiredPayload(invalid, map[string]string{"endpoint": "https://example.invalid/subscription"}); err == nil {
			t.Fatal("SealDesiredPayload accepted an invalid scope")
		}
	}
	validBody := json.RawMessage(`{"endpoint":"https://example.invalid/subscription"}`)
	valid := DesiredPayloadDocument{Version: DesiredPayloadVersion, Kind: scope.PayloadKind, Body: validBody, BodySHA256: desiredPayloadTestDigest(validBody)}
	unknownField := append(desiredPayloadTestJSON(t, valid)[:len(desiredPayloadTestJSON(t, valid))-1], []byte(`,"unexpected":true}`)...)
	for _, plaintext := range [][]byte{unknownField, append(desiredPayloadTestJSON(t, valid), []byte(` {}`)...)} {
		envelope := sealDesiredPayloadDocumentForTest(t, box, scope, plaintext)
		if _, err := box.OpenDesiredPayload(scope, envelope, desiredPayloadTestEnvelopeDigest(t, envelope)); err == nil {
			t.Fatal("OpenDesiredPayload accepted unknown fields or trailing JSON")
		}
	}
	unknownKeyEnvelope, digest, err := box.SealDesiredPayload(scope, map[string]string{"endpoint": "https://example.invalid/subscription"})
	if err != nil {
		t.Fatalf("SealDesiredPayload: %v", err)
	}
	unknownKeyEnvelope.KeyVersion = 99
	digest = desiredPayloadTestEnvelopeDigest(t, unknownKeyEnvelope)
	if _, err := box.OpenDesiredPayload(scope, unknownKeyEnvelope, digest); err == nil {
		t.Fatal("OpenDesiredPayload accepted unknown key version after digest recomputation")
	}
}

func desiredPayloadTestScope() DesiredPayloadScope {
	return DesiredPayloadScope{
		NodeID:      "node-a",
		ServiceID:   "service-a",
		CustomerID:  "customer-a",
		Generation:  17,
		OperationID: "operation-a",
		PayloadKind: "vless",
	}
}

func newDesiredPayloadTestBox(t *testing.T, current int, keys map[int]byte) *SecretBox {
	t.Helper()
	encryptionKeys := make(map[int][]byte, len(keys))
	for version, key := range keys {
		encryptionKeys[version] = bytes.Repeat([]byte{key}, secretKeyBytes)
	}
	box, err := NewSecretBox(current, encryptionKeys, bytes.Repeat([]byte{0x42}, secretKeyBytes))
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	return box
}

func sealDesiredPayloadDocumentForTest(t *testing.T, box *SecretBox, scope DesiredPayloadScope, plaintext []byte) Envelope {
	t.Helper()
	aad, err := desiredPayloadAAD(box.current, scope)
	if err != nil {
		t.Fatalf("desiredPayloadAAD: %v", err)
	}
	aead := box.aeadByVersion[box.current]
	nonce := bytes.Repeat([]byte{0x77}, aead.NonceSize())
	return Envelope{KeyVersion: box.current, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, aad)}
}

func desiredPayloadTestJSON(t *testing.T, document DesiredPayloadDocument) []byte {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return encoded
}

func desiredPayloadTestDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func desiredPayloadTestEnvelopeDigest(t *testing.T, envelope Envelope) string {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal envelope: %v", err)
	}
	return desiredPayloadTestDigest(encoded)
}
