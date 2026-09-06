package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func exactNormalizeFixture(t *testing.T) ([]byte, LegacyXUICapture, LegacyNormalizeOptions, *controlplane.SecretBox) {
	t.Helper()
	raw, capture, options, box := legacyNormalizeFixture(t)
	first, err := DecodeLegacyCustomers(raw)
	if err != nil {
		t.Fatal("invalid synthetic source")
	}
	second, err := DecodeLegacyCustomers(raw)
	if err != nil {
		t.Fatal("invalid synthetic source")
	}
	second[0].Login = strings.ToLower(first[0].Login)
	second[0].SubToken += "-second"
	second[0].VLESS.UUID = "86513836-47ba-49c5-8adb-00d24dd255ed"
	second[0].VLESS3.UUID, second[0].VLESS4.UUID = second[0].VLESS.UUID, second[0].VLESS.UUID
	second[0].Hy2.User, second[0].Hy2.Pass = second[0].Login, second[0].Hy2.Pass+"-second"
	second[0].Naive.Username, second[0].Naive.Password = "original-second-naive-name", second[0].Naive.Password+"-second"
	second[0].AnyTLS.Password += "-second"
	second[0].Expires = second[0].Expires.Add(time.Hour)
	second[0].Disabled = true
	rows := []legacystore.Customer{first[0], second[0]}
	raw = marshalNormalizeFixture(t, rows)
	capture.CustomersSHA256 = sha256Hex(raw)
	capture.Bindings = nil
	for index, customer := range rows {
		for _, nodeID := range []string{"S1", "S3", "S4"} {
			binding := legacyVLESSNodes(customer)[nodeID]
			capture.Bindings = append(capture.Bindings, LegacyNodeCapture{Login: customer.Login, NodeID: nodeID,
				Server: binding.server, UUID: binding.uuid, SubID: "synthetic-" + string(rune('a'+index)) + "-" + nodeID})
		}
	}
	return raw, capture, options, box
}

func exactIdentitySecrets(snapshot Snapshot) map[string]LegacyEncryptedSecret {
	secrets := make(map[string]LegacyEncryptedSecret, len(snapshot.EncryptedSecrets))
	for _, secret := range snapshot.EncryptedSecrets {
		secrets[secret.SecretID] = secret
	}
	return secrets
}

func resealExactIdentity(t *testing.T, snapshot *Snapshot, index int, identity ProductionCustomerIdentity, box *controlplane.SecretBox) {
	t.Helper()
	row := snapshot.Customers[index]
	plain := marshalNormalizeFixture(t, identity)
	defer zeroBytes(plain)
	scope := controlplane.SecretScope{OwnerType: "customer", OwnerID: row.SourceKey, Field: "identity", Kind: "customer-identity"}
	envelope, err := box.Seal(scope, plain)
	if err != nil {
		t.Fatal("cannot seal synthetic identity")
	}
	secret := LegacyEncryptedSecret{SecretID: row.IdentitySecretRef, OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID,
		Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha256Hex(plain)}
	for i := range snapshot.EncryptedSecrets {
		if snapshot.EncryptedSecrets[i].SecretID == row.IdentitySecretRef {
			snapshot.EncryptedSecrets[i] = secret
			return
		}
	}
	t.Fatal("synthetic identity reference missing")
}

func TestExactLegacyIdentitiesPreserveSeparateProductionCredentials(t *testing.T) {
	raw, capture, options, box := exactNormalizeFixture(t)
	customers, err := DecodeLegacyCustomers(raw)
	if err != nil || len(customers) != 2 {
		t.Fatal("distinct original case-sensitive accounts rejected")
	}
	snapshot := normalizeFixture(t, raw, capture, options, box)
	plan, report := Plan(snapshot, options.PlanOptions)
	if len(report.Blockers) != 0 || len(plan.Customers) != 2 || plan.Customers[0].InternalID == plan.Customers[1].InternalID {
		t.Fatal("separate original accounts merged in the real import plan")
	}
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal("actual production identity validation rejected exact accounts")
	}
	store, err := NewProductionRQLiteApplyStore(&applyStoreRQLite{}, time.Now, protection, nil)
	if err != nil {
		t.Fatal("actual production adapter rejected exact accounts")
	}
	originals := map[string]legacystore.Customer{}
	for _, customer := range customers {
		originals[customer.Login] = customer
	}
	secrets := exactIdentitySecrets(snapshot)
	for index, customer := range plan.Customers {
		original := originals[customer.DisplayLogin]
		canonical, _ := controlplane.CanonicalLoginKey(original.Login)
		canonicalHMAC := box.LookupHMAC("customer-login", []byte(canonical))
		exactHMAC := box.LookupHMAC(controlplane.LegacyExactCustomerLoginHMACDomain, []byte(original.Login))
		if customer.SourceKey != controlplane.LegacyExactCustomerSourcePrefix+canonicalHMAC+":"+exactHMAC || customer.LoginKeyHMAC != exactHMAC ||
			customer.InternalID != deterministicID(options.PlanOptions.Namespace, "customer", customer.SourceKey) {
			t.Fatal("source tuple, lookup key, or deterministic mapping lost exact identity")
		}
		identity, err := openProductionIdentity(box, customer.SourceKey, secrets[customer.IdentitySecretRef])
		if err != nil || !reflect.DeepEqual(identity.Customer, original) || len(identity.NodeSubIDs) != 3 {
			t.Fatal("protected original login, credentials, device times, expiry or node identities changed")
		}
		for _, binding := range capture.Bindings {
			if binding.Login == original.Login && identity.NodeSubIDs[binding.NodeID] != binding.SubID {
				t.Fatal("per-node original subscription identity changed")
			}
		}
		token, credentials, err := store.productionCustomerValues(customer, secrets[customer.IdentitySecretRef])
		if err != nil || len(credentials) != 4 {
			t.Fatal("production credential derivation failed")
		}
		values := map[string]productionSealedValue{"token": token, "vless": credentials["vless"], "naive": credentials["naive"]}
		for name, value := range values {
			encoded, err := base64.StdEncoding.DecodeString(value.envelope)
			var envelope controlplane.Envelope
			if err != nil || json.Unmarshal(encoded, &envelope) != nil {
				t.Fatal("invalid actual reader envelope")
			}
			field, kind, expected := "credential", name, original.VLESS.UUID
			if name == "token" {
				field, kind, expected = "token", "subscription", original.SubToken
			}
			if name == "naive" {
				kind = "naive-identity-v1"
			}
			scope := controlplane.SecretScope{OwnerType: "customer", OwnerID: customer.InternalID, Field: field, Kind: kind}
			plain, err := box.Open(scope, envelope)
			if err != nil {
				t.Fatal("production AAD or key binding rejected preserved credentials")
			}
			if name == "naive" {
				var named struct {
					Username string `json:"username"`
					Password string `json:"password"`
				}
				if json.Unmarshal(plain, &named) != nil || named.Username != original.Naive.Username || named.Password != original.Naive.Password {
					t.Fatal("original Naive username or password changed")
				}
			} else if string(plain) != expected {
				t.Fatal("original ordinary token or UUID changed")
			}
			zeroBytes(plain)
			scope.OwnerID = plan.Customers[1-index].InternalID
			if _, err := box.Open(scope, envelope); err == nil {
				t.Fatal("case-colliding account authenticated another customer's credential")
			}
		}
	}
	customers[1].Expires = customers[1].Expires.Add(time.Nanosecond)
	deltaRaw := marshalNormalizeFixture(t, customers)
	capture.CustomersSHA256 = sha256Hex(deltaRaw)
	capture.CapturedAt, capture.CompletedAt = capture.CapturedAt.Add(time.Second), capture.CompletedAt.Add(time.Second)
	options.Now, options.Parent = options.Now.Add(time.Second), &snapshot
	delta := normalizeFixture(t, deltaRaw, capture, options, box)
	deltaOptions := options.PlanOptions
	deltaOptions.ParentSnapshot, deltaOptions.AppliedParentDigest = &snapshot, digestSnapshot(snapshot)
	deltaPlan, deltaReport := Plan(delta, deltaOptions)
	if len(deltaReport.Blockers) != 0 || len(deltaPlan.Customers) != 2 {
		t.Fatal("case-colliding cumulative delta lost an original account")
	}
	deltaSecrets := exactIdentitySecrets(delta)
	for index, customer := range deltaPlan.Customers {
		prior := plan.Customers[index]
		expected := originals[customer.DisplayLogin]
		expectedGeneration := prior.Generation
		if customer.DisplayLogin == customers[1].Login {
			expected, expectedGeneration = customers[1], prior.Generation+1
		}
		identity, err := openProductionIdentity(box, customer.SourceKey, deltaSecrets[customer.IdentitySecretRef])
		if err != nil || customer.SourceKey != prior.SourceKey || customer.InternalID != prior.InternalID || customer.LoginKeyHMAC != prior.LoginKeyHMAC ||
			customer.Generation != expectedGeneration || !reflect.DeepEqual(identity.Customer, expected) {
			t.Fatal("case-colliding delta changed the other identity or replaced its original mapping")
		}
	}
}

func TestExactLegacyIdentityRejectsAuthenticatedSourceTupleTampering(t *testing.T) {
	for _, name := range []string{"canonical-hmac", "exact-hmac", "stored-login-hmac", "malformed-source", "wrong-original-case", "wrong-key"} {
		t.Run(name, func(t *testing.T) {
			raw, capture, options, box := exactNormalizeFixture(t)
			snapshot := normalizeFixture(t, raw, capture, options, box)
			row := &snapshot.Customers[0]
			identity, err := openProductionIdentity(box, row.SourceKey, exactIdentitySecrets(snapshot)[row.IdentitySecretRef])
			if err != nil {
				t.Fatal("synthetic original identity unavailable")
			}
			canonical, exact, _ := controlplane.ParseLegacyExactCustomerSource(row.SourceKey)
			switch name {
			case "canonical-hmac":
				canonical = strings.Repeat("a", 64)
			case "exact-hmac":
				exact = strings.Repeat("b", 64)
			case "stored-login-hmac":
				row.LoginKeyHMAC = canonical
			case "wrong-original-case":
				identity.Customer.Login = strings.ToUpper(identity.Customer.Login)
				row.Login = identity.Customer.Login
			}
			row.SourceKey = controlplane.LegacyExactCustomerSourcePrefix + canonical + ":" + exact
			if name == "malformed-source" {
				row.SourceKey += ":extra"
			}
			// Reseal under the edited source's actual AAD: metadata rejection must
			// come from recomputing the authenticated original identity, not an
			// incidental old-envelope AAD failure.
			resealExactIdentity(t, &snapshot, 0, identity, box)
			if name == "wrong-key" {
				box, err = controlplane.NewSecretBox(7, map[int][]byte{7: bytes.Repeat([]byte{0x44}, 32)}, bytes.Repeat([]byte{0x22}, 32))
				if err != nil {
					t.Fatal("synthetic key unavailable")
				}
			}
			if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box); err == nil || err.Error() != errInvalidProductionIdentity.Error() {
				t.Fatal("tampered exact identity accepted or private input exposed")
			}
		})
	}
}

func TestExactLegacyDeltaRetainsParentIdentityModesAndRevisions(t *testing.T) {
	for _, mode := range []string{"canonical", "exact"} {
		t.Run(mode, func(t *testing.T) {
			raw, capture, options, box := legacyNormalizeFixture(t)
			parent := normalizeFixture(t, raw, capture, options, box)
			if mode == "canonical" {
				row := &parent.Customers[0]
				identity, err := openProductionIdentity(box, row.SourceKey, parent.EncryptedSecrets[0])
				if err != nil {
					t.Fatal("synthetic parent unavailable")
				}
				canonical, _ := controlplane.CanonicalLoginKey(row.Login)
				row.LoginKeyHMAC = box.LookupHMAC("customer-login", []byte(canonical))
				row.SourceKey = "s1:customer:" + row.LoginKeyHMAC
				resealExactIdentity(t, &parent, 0, identity, box)
			}
			options.Parent = &parent
			capture.CapturedAt, capture.CompletedAt = capture.CapturedAt.Add(time.Second), capture.CompletedAt.Add(time.Second)
			options.Now = options.Now.Add(time.Second)
			unchanged := normalizeFixture(t, raw, capture, options, box)
			prior, next := parent.Customers[0], unchanged.Customers[0]
			if !reflect.DeepEqual(prior, next) || unchanged.EncryptedSecrets[0].SHA256 != parent.EncryptedSecrets[0].SHA256 ||
				unchanged.EncryptedSecrets[0].CiphertextB64 == parent.EncryptedSecrets[0].CiphertextB64 {
				t.Fatal("unchanged parent identity mode or revision changed with a fresh nonce")
			}
			customers, _ := DecodeLegacyCustomers(raw)
			customers[0].Expires = customers[0].Expires.Add(time.Nanosecond)
			changedRaw := marshalNormalizeFixture(t, customers)
			capture.CustomersSHA256 = sha256Hex(changedRaw)
			changed := normalizeFixture(t, changedRaw, capture, options, box)
			next = changed.Customers[0]
			identity, err := openProductionIdentity(box, next.SourceKey, changed.EncryptedSecrets[0])
			if err != nil || next.SourceKey != prior.SourceKey || next.LoginKeyHMAC != prior.LoginKeyHMAC || next.IdentitySecretRef != prior.IdentitySecretRef ||
				next.Generation != prior.Generation+1 || !reflect.DeepEqual(identity.Customer, customers[0]) {
				t.Fatal("changed delta replaced parent mapping or lost exact original values")
			}
			// A caller may not supply a newly sealed alternate mode for the same
			// authenticated parent login, even with a higher revision.
			alternate := changed
			alternate.Customers = append([]LegacyCustomer(nil), changed.Customers...)
			alternate.EncryptedSecrets = append([]LegacyEncryptedSecret(nil), changed.EncryptedSecrets...)
			row := &alternate.Customers[0]
			canonical, _ := controlplane.CanonicalLoginKey(row.Login)
			canonicalHMAC := box.LookupHMAC("customer-login", []byte(canonical))
			if mode == "canonical" {
				row.LoginKeyHMAC = box.LookupHMAC(controlplane.LegacyExactCustomerLoginHMACDomain, []byte(row.Login))
				row.SourceKey = controlplane.LegacyExactCustomerSourcePrefix + canonicalHMAC + ":" + row.LoginKeyHMAC
			} else {
				row.LoginKeyHMAC, row.SourceKey = canonicalHMAC, "s1:customer:"+canonicalHMAC
			}
			resealExactIdentity(t, &alternate, 0, identity, box)
			if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(alternate, &parent), box); err == nil {
				t.Fatal("delta silently changed authenticated parent identity mode")
			}
		})
	}
}

func TestExactLegacyPlanRejectsDuplicateAndMixedLoginFamilies(t *testing.T) {
	raw, capture, options, box := exactNormalizeFixture(t)
	snapshot := normalizeFixture(t, raw, capture, options, box)
	for _, name := range []string{"duplicate-original", "mixed-family", "malformed-exact", "wrong-stored-hmac"} {
		t.Run(name, func(t *testing.T) {
			bad := snapshot
			bad.Customers = append([]LegacyCustomer(nil), snapshot.Customers...)
			switch name {
			case "duplicate-original":
				bad.Customers = append(bad.Customers, bad.Customers[1])
			case "mixed-family":
				canonical, _, _ := controlplane.ParseLegacyExactCustomerSource(bad.Customers[0].SourceKey)
				bad.Customers[0].SourceKey, bad.Customers[0].LoginKeyHMAC = "s1:customer:"+canonical, canonical
			case "malformed-exact":
				bad.Customers[0].SourceKey += ":extra"
			case "wrong-stored-hmac":
				bad.Customers[0].LoginKeyHMAC = strings.Repeat("c", 64)
			}
			if _, report := Plan(bad, options.PlanOptions); len(report.Blockers) == 0 {
				t.Fatal("unsafe exact login family reached an executable plan")
			}
		})
	}
}
