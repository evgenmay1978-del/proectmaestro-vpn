package controlplane

import (
	"encoding/json"
	"testing"
)

type retainingDesiredPayloadAEAD struct {
	plaintext []byte
}

func (a *retainingDesiredPayloadAEAD) NonceSize() int { return 12 }
func (a *retainingDesiredPayloadAEAD) Overhead() int  { return 16 }
func (a *retainingDesiredPayloadAEAD) Seal(_, _, _, _ []byte) []byte {
	panic("unexpected Seal")
}
func (a *retainingDesiredPayloadAEAD) Open(_, _, _, _ []byte) ([]byte, error) {
	return a.plaintext, nil
}

func TestOpenDesiredPayloadWipesTemporaryPlaintextBuffer(t *testing.T) {
	scope := desiredPayloadTestScope()
	body := json.RawMessage(`{"credential":"synthetic-retained-marker"}`)
	document := DesiredPayloadDocument{
		Version: DesiredPayloadVersion, Kind: scope.PayloadKind,
		Body: body, BodySHA256: desiredPayloadTestDigest(body),
	}
	plaintext := desiredPayloadTestJSON(t, document)
	retained := append([]byte(nil), plaintext...)
	aead := &retainingDesiredPayloadAEAD{plaintext: retained}
	envelope := Envelope{
		KeyVersion: 1,
		Nonce:      make([]byte, aead.NonceSize()),
		Ciphertext: make([]byte, aead.Overhead()),
	}

	opened, err := openDesiredPayloadDocument(aead, scope, envelope)
	if err != nil {
		t.Fatalf("openDesiredPayloadDocument: %v", err)
	}
	if string(opened.Body) != string(body) {
		t.Fatalf("opened body=%s, want %s", opened.Body, body)
	}
	for _, value := range retained {
		if value != 0 {
			t.Fatalf("temporary decrypted document retained plaintext: %q", string(retained))
		}
	}
}
