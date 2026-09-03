package controlplane

import (
	"bytes"
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
