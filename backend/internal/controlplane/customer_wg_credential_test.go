package controlplane

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestWGCredentialRetainsTupleAndCannotChangeOwnerOrType(t *testing.T) {
	service, box := testService(t, &recordingRQLite{})
	identity := &subgen.WGCreds{Server: "wg.example.test", Port: 443, PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), LocalAddress: "10.10.8.2/32"}
	raw, err := EncodeWGCredentialIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := SealWGCredentialIdentity(box, "fixture-wg-customer", raw)
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"secret_envelope": base64.StdEncoding.EncodeToString(encoded)}
	actual, username, err := service.openCustomerCredential(row, "fixture-wg-customer", "awg")
	decoded, decodeErr := DecodeWGCredentialIdentity(actual)
	if err != nil || decodeErr != nil || username != "" || actual != raw || !reflect.DeepEqual(decoded, identity) {
		t.Fatal("complete WG tuple was changed")
	}
	for _, variant := range []string{"wrong-owner", "wrong-protocol", "missing-marker", "bad-version", "null-marker", "damaged-ciphertext"} {
		t.Run(variant, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if json.Unmarshal(encoded, &fields) != nil {
				t.Fatal("fixture")
			}
			owner, protocol := "fixture-wg-customer", "awg"
			switch variant {
			case "wrong-owner":
				owner = "other-customer"
			case "wrong-protocol":
				protocol = "naive"
			case "missing-marker":
				delete(fields, "credential_identity_version")
			case "bad-version":
				fields["credential_identity_version"] = json.RawMessage("2")
			case "null-marker":
				fields["credential_identity_version"] = json.RawMessage("null")
			case "damaged-ciphertext":
				fields["Ciphertext"] = json.RawMessage(`"AAAA"`)
			}
			changed, _ := json.Marshal(fields)
			value, name, err := service.openCustomerCredential(map[string]any{"secret_envelope": base64.StdEncoding.EncodeToString(changed)}, owner, protocol)
			if err == nil || value != "" || name != "" {
				t.Fatal("invalid WG envelope exposed a credential")
			}
		})
	}
	for _, mutate := range []func(*subgen.WGCreds){func(c *subgen.WGCreds) { c.PrivateKey = "short" }, func(c *subgen.WGCreds) { c.Port = 0 }, func(c *subgen.WGCreds) { c.LocalAddress = "missing-prefix" }, func(c *subgen.WGCreds) { c.Server = "" }} {
		changed := *identity
		mutate(&changed)
		if _, err := EncodeWGCredentialIdentity(&changed); err == nil {
			t.Fatal("incomplete WG credential accepted")
		}
	}
}
