package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func legacyNormalizeFixture(t *testing.T) ([]byte, LegacyXUICapture, LegacyNormalizeOptions, *controlplane.SecretBox) {
	t.Helper()
	_, identity, box := productionIdentityFixture(t)
	identity.Customer.Expires = time.Unix(2_100_000, 123456789).UTC()
	identity.Customer.Devices = map[string]time.Time{"synthetic-device": time.Unix(900_000, 987654321).UTC()}
	raw := marshalNormalizeFixture(t, []legacystore.Customer{identity.Customer})
	capture := LegacyXUICapture{SchemaVersion: 1, CapturedAt: time.Unix(1_000_000, 0).UTC(), CompletedAt: time.Unix(1_000_002, 0).UTC(), CustomersSHA256: sha256Hex(raw)}
	for _, nodeID := range []string{"S1", "S3", "S4"} {
		binding := legacyVLESSNodes(identity.Customer)[nodeID]
		capture.Bindings = append(capture.Bindings, LegacyNodeCapture{Login: identity.Customer.Login, NodeID: nodeID,
			Server: binding.server, UUID: binding.uuid, SubID: "synthetic-bot-sub-id-" + nodeID})
	}
	options := LegacyNormalizeOptions{Now: time.Unix(1_000_003, 0).UTC(), MaxCaptureAge: time.Minute,
		Sources: map[string]LegacySourcePresence{"orders": {State: "present", SHA256: sha256Hex([]byte("synthetic orders"))},
			"trials": {State: "absent"}, "settings": {State: "present", SHA256: sha256Hex([]byte("synthetic settings"))}, "principals": {State: "absent"}},
		PlanOptions: PlanOptions{Namespace: "maestro-legacy-v1", SupportedProtocolTags: []string{"vless", "hysteria2", "naive", "anytls"}, SupportedNodeIDs: []string{"S1", "S2", "S3", "S4"}},
	}
	for protocol, server := range legacyOtherServers(identity.Customer) {
		options.ProtocolBindings = append(options.ProtocolBindings, LegacyProtocolBinding{Protocol: protocol, Server: server, NodeID: "S2"})
	}
	return raw, capture, options, box
}

func marshalNormalizeFixture(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal("synthetic fixture cannot be encoded")
	}
	return raw
}

func normalizeFixture(t *testing.T, raw []byte, capture LegacyXUICapture, options LegacyNormalizeOptions, box *controlplane.SecretBox) Snapshot {
	t.Helper()
	snapshot, err := NormalizeLegacyCustomers(raw, capture, box, bytes.Repeat([]byte{0x22}, 32), options)
	if err != nil {
		t.Fatal("synthetic customer normalization failed")
	}
	return snapshot
}

func TestDecodeLegacyCustomersRejectsLossyInput(t *testing.T) {
	raw, _, _, _ := legacyNormalizeFixture(t)
	customers, err := DecodeLegacyCustomers(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"canonical-login", "token", "uuid"} {
		t.Run(name, func(t *testing.T) {
			other := customers[0]
			vless := *other.VLESS
			other.VLESS, other.VLESS3, other.VLESS4 = &vless, nil, nil
			other.Login, other.SubToken, other.VLESS.UUID = "DifferentSyntheticLogin", "different-synthetic-token", "different-synthetic-uuid"
			if name == "canonical-login" {
				other.Login = strings.ToLower(customers[0].Login)
			}
			if name == "token" {
				other.SubToken = customers[0].SubToken
			}
			if name == "uuid" {
				other.VLESS.UUID = customers[0].VLESS.UUID
			}
			other.Hy2 = nil
			_, err := DecodeLegacyCustomers(marshalNormalizeFixture(t, []legacystore.Customer{customers[0], other}))
			if err == nil {
				t.Fatal("ambiguous source identity accepted")
			}
		})
	}
	for name, input := range map[string][]byte{
		"empty": nil, "null": []byte("null"), "null-row": []byte("[null]"),
		"trailing":              append(append([]byte(nil), raw...), []byte(" []")...),
		"repeated-field":        bytes.Replace(raw, []byte(`"login":`), []byte(`"login":"synthetic-hidden","login":`), 1),
		"case-field-alias":      bytes.Replace(raw, []byte(`"login":`), []byte(`"LOGIN":"synthetic-hidden","login":`), 1),
		"case-credential-alias": bytes.Replace(raw, []byte(`"UUID":`), []byte(`"uuid":"synthetic-hidden","UUID":`), 1),
		"unknown-field":         bytes.Replace(raw, []byte(`"login":`), []byte(`"unrecognized":true,"login":`), 1),
		"repeated-device":       bytes.Replace(raw, []byte(`"synthetic-device":`), []byte(`"synthetic-device":"2000-01-01T00:00:00Z","synthetic-device":`), 1),
		"invalid-utf8":          bytes.Replace(raw, []byte("synthetic-device"), []byte{'d', 0xff}, 1),
		"unpaired-surrogate":    bytes.Replace(raw, []byte("synthetic-device"), []byte(`device\ud800`), 1),
		"low-surrogate":         bytes.Replace(raw, []byte("synthetic-device"), []byte(`device\udc00`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeLegacyCustomers(input); err == nil || err.Error() != ErrLegacyNormalize.Error() {
				t.Fatal("lossy source accepted or input leaked in error")
			}
		})
	}
}

func TestDecodeLegacyCustomersPreservesValidUnicodeAndCaseSensitiveDevices(t *testing.T) {
	raw, _, _, _ := legacyNormalizeFixture(t)
	customers, _ := DecodeLegacyCustomers(raw)
	customers[0].Devices["Synthetic-Device"] = customers[0].Devices["synthetic-device"]
	customers[0].Devices["literal-�-𝄞"] = customers[0].Devices["synthetic-device"]
	raw = marshalNormalizeFixture(t, customers)
	raw = bytes.Replace(raw, []byte("𝄞"), []byte(`\ud834\udd1e`), 1)
	decoded, err := DecodeLegacyCustomers(raw)
	if err != nil || !reflect.DeepEqual(decoded, customers) {
		t.Fatal("valid Unicode or distinct device identities were changed")
	}
}

func TestLegacyNormalizePreservesOrdinaryIdentityAndCaptureScope(t *testing.T) {
	raw, capture, options, box := legacyNormalizeFixture(t)
	snapshot := normalizeFixture(t, raw, capture, options, box)
	customers, _ := DecodeLegacyCustomers(raw)
	identity, err := openProductionIdentity(box, snapshot.Customers[0].SourceKey, snapshot.EncryptedSecrets[0])
	if err != nil || !reflect.DeepEqual(identity.Customer, customers[0]) || identity.Generation != 1 ||
		identity.SubID != "synthetic-bot-sub-id-S1" || identity.NodeSubIDs["S3"] != "synthetic-bot-sub-id-S3" || identity.NodeSubIDs["S4"] != "synthetic-bot-sub-id-S4" {
		t.Fatal("normalization replaced a credential, device, timestamp, or per-node SubID")
	}
	if snapshot.Customers[0].ExpiresAtUnix != customers[0].Expires.Unix() || snapshot.CapturedAt != capture.CapturedAt ||
		snapshot.SourceHashes["customers"] != sha256Hex(raw) || snapshot.SourceHashes["legacy:orders:present-unconverted"] == "" ||
		snapshot.SourceHashes["scope:"+LegacyCustomerPreparationScope] == "" || len(snapshot.Orders) != 0 {
		t.Fatal("capture provenance or explicit preparation scope was lost")
	}
	if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyNormalizePreservesObservedWGAndNonVLESSVariants(t *testing.T) {
	for _, variant := range []string{"wg", "no-vless"} {
		t.Run(variant, func(t *testing.T) {
			raw, capture, options, box := legacyNormalizeFixture(t)
			customers, err := DecodeLegacyCustomers(raw)
			if err != nil {
				t.Fatal(err)
			}
			customer := &customers[0]
			if variant == "wg" {
				customer.WG = &subgen.WGCreds{Server: "wg.example.test", Port: 443, PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), LocalAddress: "10.10.8.2/32"}
				options.ProtocolBindings = append(options.ProtocolBindings, LegacyProtocolBinding{Protocol: "awg", Server: customer.WG.Server, NodeID: "S3"})
				options.PlanOptions.SupportedProtocolTags = append(options.PlanOptions.SupportedProtocolTags, "awg")
			} else {
				customer.VLESS, customer.VLESS3, customer.VLESS4 = nil, nil, nil
				capture.Bindings = nil
			}
			raw = marshalNormalizeFixture(t, customers)
			capture.CustomersSHA256 = sha256Hex(raw)
			snapshot := normalizeFixture(t, raw, capture, options, box)
			identity, err := openProductionIdentity(box, snapshot.Customers[0].SourceKey, snapshot.EncryptedSecrets[0])
			if err != nil || !reflect.DeepEqual(identity.Customer, *customer) {
				t.Fatal("observed source variant lost original credentials")
			}
			if variant == "no-vless" && (identity.SubID != "" || len(identity.NodeSubIDs) != 0 || snapshot.Customers[0].UUIDHMAC != "" || snapshot.Customers[0].SubIDHMAC != "") {
				t.Fatal("absent VLESS identity was fabricated")
			}
			options.Parent = &snapshot
			capture.CapturedAt = capture.CompletedAt.Add(time.Nanosecond)
			capture.CompletedAt = capture.CapturedAt
			options.Now = capture.CompletedAt
			delta := normalizeFixture(t, raw, capture, options, box)
			if delta.Customers[0].Generation != 1 {
				t.Fatal("unchanged variant changed logical revision")
			}
			if variant == "no-vless" {
				customer.VLESS3 = &subgen.VLESSCreds{UUID: "orphan"}
				if _, err := DecodeLegacyCustomers(marshalNormalizeFixture(t, customers)); err == nil {
					t.Fatal("orphan secondary VLESS accepted")
				}
			}
		})
	}
}

func TestLegacyNormalizeRejectsStaleMismatchedCapture(t *testing.T) {
	for _, name := range []string{"raw-drift", "stale", "future", "time-order", "uuid", "server", "missing-node", "extra-node", "sub-id", "source-inventory", "topology"} {
		t.Run(name, func(t *testing.T) {
			raw, capture, options, box := legacyNormalizeFixture(t)
			switch name {
			case "raw-drift":
				raw = append(raw, ' ')
			case "stale":
				options.Now = capture.CapturedAt.Add(options.MaxCaptureAge + time.Nanosecond)
			case "future":
				capture.CompletedAt = options.Now.Add(time.Nanosecond)
			case "time-order":
				capture.CompletedAt = capture.CapturedAt.Add(-time.Nanosecond)
			case "uuid":
				capture.Bindings[0].UUID = "wrong-synthetic-uuid"
			case "server":
				capture.Bindings[0].Server = "wrong.example.test"
			case "missing-node":
				capture.Bindings = capture.Bindings[:2]
			case "extra-node":
				capture.Bindings = append(capture.Bindings, capture.Bindings[0])
			case "sub-id":
				capture.Bindings[0].SubID = ""
			case "source-inventory":
				delete(options.Sources, "orders")
			case "topology":
				options.ProtocolBindings = nil
			}
			if _, err := NormalizeLegacyCustomers(raw, capture, box, bytes.Repeat([]byte{0x22}, 32), options); err == nil || err.Error() != ErrLegacyNormalize.Error() {
				t.Fatal("inconsistent capture accepted or detail leaked")
			}
		})
	}
}

func TestLegacyNormalizeDeltaUsesAuthenticatedContentRevision(t *testing.T) {
	raw, capture, options, box := legacyNormalizeFixture(t)
	parent := normalizeFixture(t, raw, capture, options, box)
	options.Parent = &parent
	capture.CapturedAt, capture.CompletedAt = capture.CapturedAt.Add(time.Second), capture.CompletedAt.Add(time.Second)
	options.Now = options.Now.Add(time.Second)
	unchanged := normalizeFixture(t, raw, capture, options, box)
	if unchanged.SnapshotKind != "delta" || unchanged.ParentSourceDigest != digestSnapshot(parent) || unchanged.Customers[0].Generation != 1 ||
		unchanged.EncryptedSecrets[0].SHA256 != parent.EncryptedSecrets[0].SHA256 || unchanged.EncryptedSecrets[0].CiphertextB64 == parent.EncryptedSecrets[0].CiphertextB64 {
		t.Fatal("unchanged content revision depended on capture time or randomized encryption")
	}
	for _, name := range []string{"expiry", "naive-username", "device", "node-sub-id", "disabled"} {
		t.Run(name, func(t *testing.T) {
			changedCapture := capture
			changedCapture.Bindings = append([]LegacyNodeCapture(nil), capture.Bindings...)
			customers, _ := DecodeLegacyCustomers(raw)
			switch name {
			case "expiry":
				customers[0].Expires = customers[0].Expires.Add(time.Nanosecond)
			case "naive-username":
				customers[0].Naive.Username += "-retained"
			case "device":
				customers[0].Devices["synthetic-added-device"] = time.Unix(999_999, 999).UTC()
			case "node-sub-id":
				changedCapture.Bindings[1].SubID += "-changed"
			case "disabled":
				customers[0].Disabled = true
			}
			changedRaw := marshalNormalizeFixture(t, customers)
			changedCapture.CustomersSHA256 = sha256Hex(changedRaw)
			changed := normalizeFixture(t, changedRaw, changedCapture, options, box)
			identity, err := openProductionIdentity(box, changed.Customers[0].SourceKey, changed.EncryptedSecrets[0])
			if err != nil || changed.Customers[0].Generation != 2 || identity.Generation != 2 || !reflect.DeepEqual(identity.Customer, customers[0]) {
				t.Fatal("authenticated content change lost identity or reused its prior revision")
			}
		})
	}
}

func TestLegacyNormalizeRejectsParentTamperRemovalAndOverflow(t *testing.T) {
	for _, name := range []string{"ciphertext", "row-generation", "removal", "overflow"} {
		t.Run(name, func(t *testing.T) {
			raw, capture, options, box := legacyNormalizeFixture(t)
			parent := normalizeFixture(t, raw, capture, options, box)
			if name == "ciphertext" {
				parent.EncryptedSecrets[0].CiphertextB64 = strings.Repeat("A", len(parent.EncryptedSecrets[0].CiphertextB64))
			}
			if name == "row-generation" {
				parent.Customers[0].Generation++
			}
			if name == "overflow" {
				identity, err := openProductionIdentity(box, parent.Customers[0].SourceKey, parent.EncryptedSecrets[0])
				if err != nil {
					t.Fatal(err)
				}
				identity.Generation = math.MaxInt64
				setProductionFixtureIdentity(t, &parent, identity, box)
				customers, _ := DecodeLegacyCustomers(raw)
				customers[0].Expires = customers[0].Expires.Add(time.Second)
				raw = marshalNormalizeFixture(t, customers)
			}
			if name == "removal" {
				raw, capture.Bindings = []byte("[]"), nil
			}
			capture.CustomersSHA256 = sha256Hex(raw)
			capture.CapturedAt, capture.CompletedAt = capture.CapturedAt.Add(time.Second), capture.CompletedAt.Add(time.Second)
			options.Now, options.Parent = options.Now.Add(time.Second), &parent
			if _, err := NormalizeLegacyCustomers(raw, capture, box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
				t.Fatal("invalid parent transition accepted")
			}
		})
	}
}
