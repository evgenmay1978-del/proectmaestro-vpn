package importer

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
)

func nativeOrderFixture(t *testing.T) ([]byte, LegacyXUICapture, LegacyNormalizeOptions, *controlplane.SecretBox, []legacyorder.Order) {
	t.Helper()
	raw, capture, options, box := legacyNormalizeFixture(t)
	customers, err := DecodeLegacyCustomers(raw)
	if err != nil {
		t.Fatal(err)
	}
	orders := []legacyorder.Order{
		{ID: "ord_paid", Tariff: "1m", Days: 30, Rub: 300, Code: "Mpaid", Login: customers[0].Login, Status: "paid", SubToken: customers[0].SubToken, CreatedAt: time.Unix(800000, 123456789).UTC()},
		{ID: "ord_orphan", Tariff: "1m", Days: 30, Rub: 400, Code: "Morphan", Login: "MissingOriginalCustomer", Status: "paid", SubToken: "retained-orphan-token", CreatedAt: time.Unix(800010, 987654321).UTC()},
		{ID: "ord_pending", Tariff: "2m", Days: 60, Rub: 800, Code: "Mpending", Login: customers[0].Login, Status: "pending", CreatedAt: time.Unix(800020, 111222333).UTC()},
	}
	orderRaw, _ := json.MarshalIndent(orders, "", " ")
	options.Sources["orders"] = LegacySourcePresence{State: "present", SHA256: sha256Hex(orderRaw)}
	options.OrderSource = &LegacyOrderSource{RawJSON: orderRaw}
	return raw, capture, options, box, orders
}

func TestNativeOrdersRetainAllRawHistoryWithoutInventedDurableGrants(t *testing.T) {
	raw, capture, options, box, orders := nativeOrderFixture(t)
	snapshot := normalizeFixture(t, raw, capture, options, box)
	if len(snapshot.Orders) != 0 || snapshot.SourceHashes[legacyOrdersConvertedSource] != sha256Hex(options.OrderSource.RawJSON) || snapshot.SourceHashes["legacy:orders:present-unconverted"] != "" {
		t.Fatal("raw history was represented as a current grant")
	}
	proof, err := validateNativeLegacyOrderProof(ProtectionFromSnapshot(snapshot), box)
	if err != nil || len(proof.aliases) != 3 {
		t.Fatal("native order proof incomplete")
	}
	if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box); err != nil {
		t.Fatal(err)
	}
	for _, order := range orders {
		key := box.LookupHMAC(controlplane.LegacyOrderLookupDomain, []byte(order.ID))
		alias := proof.aliases[key]
		plain, err := openLegacyOrderSecret(box, proof.secrets[alias.SecretID])
		if err != nil {
			t.Fatal(err)
		}
		var record controlplane.LegacyOrderRecord
		err = decodeCanonicalOperation(plain, &record)
		zeroBytes(plain)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := controlplane.ValidateLegacyOrderRecord(box, record)
		if err != nil || decoded.Order != order || decoded.CreditedPresent || alias.Historical != (order.Status == "paid") || record.Revision != 1 {
			t.Fatal("original order fields or credited presence changed")
		}
		if (order.ID == "ord_orphan") != (record.CustomerID == "") {
			t.Fatal("orphan customer was fabricated or exact binding lost")
		}
	}
	source := proof.secrets[controlplane.LegacyOrderSourceID(sha256Hex(options.OrderSource.RawJSON))]
	decoded, err := openLegacyOrderSecret(box, source)
	if err != nil || !bytes.Equal(decoded, options.OrderSource.RawJSON) {
		t.Fatal("exact raw source bytes were lost")
	}
	zeroBytes(decoded)
	encoded, _ := json.Marshal(snapshot)
	for _, secret := range []string{orders[0].SubToken, orders[1].SubToken, orders[0].ID} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("protected order data leaked")
		}
	}
}

func TestNativeOrdersFinalDeltaRetainsPriorCiphertextAndAdvancesOnlyChangedOrder(t *testing.T) {
	raw, capture, options, box, orders := nativeOrderFixture(t)
	parent := normalizeFixture(t, raw, capture, options, box)
	old, err := validateNativeLegacyOrderProof(ProtectionFromSnapshot(parent), box)
	if err != nil {
		t.Fatal(err)
	}
	orders[2].Status, orders[2].Credited, orders[2].SubToken = "paid", true, orders[0].SubToken
	nextRaw, _ := json.MarshalIndent(orders, "", " ")
	options.OrderSource = &LegacyOrderSource{RawJSON: nextRaw}
	options.Sources["orders"] = LegacySourcePresence{State: "present", SHA256: sha256Hex(nextRaw)}
	options.Parent = &parent
	options.Now = options.Now.Add(10 * time.Second)
	capture.CapturedAt = capture.CapturedAt.Add(10 * time.Second)
	capture.CompletedAt = capture.CompletedAt.Add(10 * time.Second)
	delta := normalizeFixture(t, raw, capture, options, box)
	proof, err := validateNativeLegacyOrderProof(ProtectionFromSnapshot(delta, &parent), box)
	if err != nil {
		t.Fatal(err)
	}
	for id, secret := range old.secrets {
		if proof.secrets[id] != secret {
			t.Fatal("final delta rewrote immutable source evidence")
		}
	}
	owners := map[string]string{}
	for id, secret := range proof.secrets {
		owner := secret.OwnerType + "\x00" + secret.OwnerSourceKey + "\x00" + secret.Field
		if previous, exists := owners[owner]; exists && previous != id {
			t.Fatal("delta archive collides with immutable SQL owner identity")
		}
		owners[owner] = id
		if secret.Kind == controlplane.LegacyOrderRecordKind && secret.Field != controlplane.LegacyOrderRecordScope(secret.OwnerSourceKey, secret.SHA256).Field {
			t.Fatal("new source record is not content-addressed")
		}
	}
	for _, order := range orders {
		key := box.LookupHMAC(controlplane.LegacyOrderLookupDomain, []byte(order.ID))
		want := int64(1)
		if order.ID == "ord_pending" {
			want = 2
		}
		if proof.aliases[key].Revision != want {
			t.Fatal("order source revision did not match actual change")
		}
	}
	if !reflect.DeepEqual(parent.Customers, delta.Customers) {
		t.Fatal("order history changed customer expiry or desired revision")
	}
	orders[0].Rub = 1
	badRaw, _ := json.Marshal(orders)
	options.OrderSource = &LegacyOrderSource{RawJSON: badRaw}
	options.Sources["orders"] = LegacySourcePresence{State: "present", SHA256: sha256Hex(badRaw)}
	if _, err := NormalizeLegacyCustomers(raw, capture, box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
		t.Fatal("historical financial terms changed")
	}
}

func TestNativeOrdersProofRejectsWrongSourceAADAndForgedGrant(t *testing.T) {
	raw, capture, options, box, _ := nativeOrderFixture(t)
	snapshot := normalizeFixture(t, raw, capture, options, box)
	for _, which := range []string{"source", "aad", "grant", "alias"} {
		t.Run(which, func(t *testing.T) {
			protection := ProtectionFromSnapshot(snapshot)
			switch which {
			case "source":
				protection.SourceHashes[legacyOrdersConvertedSource] = strings.Repeat("f", 64)
			case "aad":
				for i := range protection.EncryptedSecrets {
					if protection.EncryptedSecrets[i].Kind == controlplane.LegacyOrderRecordKind {
						protection.EncryptedSecrets[i].Field = "other"
						break
					}
				}
			case "grant":
				protection.Orders = []LegacyOrder{{SourceKey: "forged-current-order"}}
			case "alias":
				for key := range protection.SourceHashes {
					if strings.HasPrefix(key, legacyOrderAliasPrefix) {
						delete(protection.SourceHashes, key)
						break
					}
				}
			}
			if _, err := ValidateProductionCustomerIdentities(protection, box); err == nil {
				t.Fatal("invalid source escaped pre-apply validation")
			}
		})
	}
}

func TestNativeOrderCustomerBindingUsesOriginalCaseAndExactToken(t *testing.T) {
	_, _, _, box, _ := nativeOrderFixture(t)
	customers := map[string]LegacyCustomer{}
	for _, login := range []string{"OrderCase", "ordercase"} {
		canonical, _ := controlplane.CanonicalLoginKey(login)
		exact := box.LookupHMAC(controlplane.LegacyExactCustomerLoginHMACDomain, []byte(login))
		customers[login] = LegacyCustomer{SourceKey: controlplane.LegacyExactCustomerSourcePrefix + box.LookupHMAC("customer-login", []byte(canonical)) + ":" + exact, Login: login, LoginKeyHMAC: exact, TokenHMAC: box.LookupHMAC("subscription-token", []byte("token-"+login))}
	}
	order := legacyorder.Order{ID: "ord_exact_case", Tariff: "1m", Days: 30, Rub: 400, Code: "Mexact", Login: "OrderCase", Status: "paid", SubToken: "token-OrderCase", CreatedAt: time.Unix(1000000, 0).UTC()}
	raw, _ := json.Marshal([]legacyorder.Order{order})
	rows, err := controlplane.DecodeLegacyOrderSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	record, err := legacyOrderRecordFor(rows[0], customers, sha256Hex(raw), sha256Hex([]byte("customers")), box)
	if err != nil || record.CustomerSourceKey != customers["OrderCase"].SourceKey || record.CustomerID == deterministicID("maestro-legacy-v1", "customer", customers["ordercase"].SourceKey) {
		t.Fatal("order bound to case-colliding sibling")
	}
	order.SubToken = "token-ordercase"
	raw, _ = json.Marshal([]legacyorder.Order{order})
	rows, err = controlplane.DecodeLegacyOrderSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyOrderRecordFor(rows[0], customers, sha256Hex(raw), sha256Hex([]byte("customers")), box); err == nil {
		t.Fatal("sibling token authorized order binding")
	}
}
