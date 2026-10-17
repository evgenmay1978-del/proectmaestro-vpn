package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestNamedNaiveCredentialAuthenticatesAndCannotFallBack(t *testing.T) {
	service, box := testService(t, &recordingRQLite{})
	const customerID = "named-naive-customer"
	const username = "mtv_OriginalLogin"
	const password = "synthetic-original-password"
	encoded, _, err := SealNaiveCredentialIdentity(box, customerID, username, password)
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"secret_envelope": base64.StdEncoding.EncodeToString(encoded)}
	gotPassword, gotUsername, err := service.openCustomerCredential(row, customerID, "naive")
	if err != nil || gotPassword != password || gotUsername != username {
		t.Fatal("named Naive identity did not round trip")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil {
		t.Fatal("invalid synthetic credential")
	}
	for _, variant := range []string{"missing-marker", "bad-version", "null-marker", "wrong-protocol", "wrong-owner", "damaged-ciphertext"} {
		t.Run(variant, func(t *testing.T) {
			changed := make(map[string]json.RawMessage, len(fields))
			for key, value := range fields {
				changed[key] = append(json.RawMessage(nil), value...)
			}
			owner, protocol := customerID, "naive"
			switch variant {
			case "missing-marker":
				delete(changed, "credential_identity_version")
			case "bad-version":
				changed["credential_identity_version"] = json.RawMessage("2")
			case "null-marker":
				changed["credential_identity_version"] = json.RawMessage("null")
			case "wrong-protocol":
				protocol = "vless"
			case "wrong-owner":
				owner = "other-customer"
			case "damaged-ciphertext":
				changed["Ciphertext"] = json.RawMessage(`"AAAA"`)
			}
			raw, _ := json.Marshal(changed)
			p, u, err := service.openCustomerCredential(map[string]any{"secret_envelope": base64.StdEncoding.EncodeToString(raw)}, owner, protocol)
			if err == nil || p != "" || u != "" {
				t.Fatal("invalid typed identity exposed a scalar fallback")
			}
		})
	}
	// An existing scalar that happens to look like JSON remains an opaque
	// password: marker selection never guesses based on decrypted plaintext.
	const scalar = `{"username":"opaque","password":"scalar"}`
	legacy, err := box.Seal(SecretScope{OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: "naive"}, []byte(scalar))
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, _ := json.Marshal(legacy)
	p, u, err := service.openCustomerCredential(map[string]any{"secret_envelope": base64.StdEncoding.EncodeToString(legacyJSON)}, customerID, "naive")
	if err != nil || p != scalar || u != "" {
		t.Fatal("existing scalar credential semantics changed")
	}
	payload := accessPayload(CustomerAccess{SubscriptionToken: "synthetic-token", Credentials: map[string]string{"naive": password}, CredentialUsernames: map[string]string{"naive": username}})
	if !reflect.DeepEqual(payload["credential_usernames"], map[string]string{"naive": username}) {
		t.Fatal("renew/provision payload lost the original username")
	}
}
