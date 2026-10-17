package controlplane

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestWhiteListRouteCredentialIsImmutableAndScopeBound(t *testing.T) {
	box, err := NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewWhiteListRouteCredential(box, "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "exit-nl", []byte("synthetic-route-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if credential.ManagedEmail != "wl:wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:exit-nl" {
		t.Fatalf("managed email = %q", credential.ManagedEmail)
	}
	opened, err := box.Open(WhiteListRouteCredentialScope(credential.EntitlementID, credential.ExitID), credential.Payload)
	if err != nil || string(opened) != "synthetic-route-secret" {
		t.Fatalf("open route credential: %q, %v", opened, err)
	}
	if _, err := box.Open(WhiteListRouteCredentialScope(credential.EntitlementID, "exit-de"), credential.Payload); err == nil {
		t.Fatal("credential opened for a different exit")
	}
	if strings.Contains(credential.CanonicalIdentity(), "synthetic-route-secret") {
		t.Fatal("canonical identity contains raw credential")
	}
}

func TestStoreWhiteListRouteCredentialAuthenticatesEnvelopeBeforeWrite(t *testing.T) {
	box, err := NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := NewWhiteListRouteCredential(box, "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "exit-nl", []byte("synthetic-route-secret"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		payload Envelope
	}{
		{name: "zero key", payload: Envelope{}},
		{name: "empty nonce", payload: Envelope{KeyVersion: 1, Ciphertext: valid.Payload.Ciphertext}},
		{name: "plaintext ciphertext", payload: Envelope{KeyVersion: 1, Nonce: valid.Payload.Nonce, Ciphertext: []byte("synthetic-route-secret")}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{}
			service := &Service{store: &Store{db: db, secrets: box}, clock: fixedClock{value: testTime()}}
			credential := valid
			credential.Payload = test.payload
			if err := service.StoreWhiteListRouteCredential(context.Background(), credential); err == nil {
				t.Fatal("invalid protected envelope accepted")
			}
			if len(db.requestCalls) != 0 {
				t.Fatalf("invalid protected envelope caused %d writes", len(db.requestCalls))
			}
		})
	}
}
