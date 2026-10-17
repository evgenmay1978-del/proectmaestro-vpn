package importer

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	legacystore "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

func legacyAbsentFixture(capture LegacyXUICapture) controlplane.LegacyXUIAbsenceEvidence {
	return controlplane.LegacyXUIAbsenceEvidence{SchemaVersion: 1, Method: "xui-sqlite-live-fd-wal-snapshot-v1", ObservedAt: capture.CompletedAt,
		CustomersSHA256: capture.CustomersSHA256, DatabaseSnapshotSHA256: strings.Repeat("a", 64), DatabaseDevice: 1, DatabaseInode: 2, XUIPID: 3, XUIStartTicks: 4,
		LiveDatabaseFDMatched: true, InboundsLoginAbsent: true, InboundsUUIDAbsent: true, ClientsLoginAbsent: true, ClientsUUIDAbsent: true}
}

func TestLegacyAbsentNodePreservesOrdinaryIdentityAndOtherSubIDs(t *testing.T) {
	raw, capture, options, box := legacyNormalizeFixture(t)
	original, err := DecodeLegacyCustomers(raw)
	if err != nil {
		t.Fatal(err)
	}
	evidence := legacyAbsentFixture(capture)
	capture.Bindings[0].SubID = ""
	capture.Bindings[0].ObservedAbsent = &evidence
	snapshot := normalizeFixture(t, raw, capture, options, box)
	row := snapshot.Customers[0]
	if row.UUIDHMAC == "" || row.SubIDHMAC != "" || row.SubIDHMAC == box.LookupHMAC("subscription-id", nil) {
		t.Fatal("missing SubID was fabricated or UUID lost")
	}
	identity, err := openProductionIdentity(box, row.SourceKey, snapshot.EncryptedSecrets[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(identity.Customer, original[0]) || identity.SubID != "" || len(identity.NodeSubIDs) != 2 || identity.NodeSubIDs["S3"] != capture.Bindings[1].SubID || identity.NodeSubIDs["S4"] != capture.Bindings[2].SubID {
		t.Fatal("ordinary identity or other node binding changed")
	}
	if len(snapshot.EncryptedSecrets) != 2 || len(identity.ObservedAbsent) != 1 {
		t.Fatal("absence proof was not retained")
	}
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil || len(protection.nodeAbsences) != 1 {
		t.Fatal("actual production validation rejected absence")
	}
	plan, report := Plan(snapshot, options.PlanOptions)
	if len(report.Blockers) != 0 || len(plan.Customers[0].NodeIDs) != 4 {
		t.Fatal("absent XUI binding removed a whole node")
	}
	store := &RQLiteApplyStore{customerProtection: protection, now: func() time.Time { return options.Now }}
	statements, err := store.productionNodeAbsenceStatements(ApplyBatch{RunID: "absent", PlanDigest: strings.Repeat("b", 64), Digest: strings.Repeat("c", 64)}, plan.Customers[0])
	if err != nil || len(statements) != 2 || !strings.Contains(statements[0].SQL, "INSERT INTO imported_secrets") {
		t.Fatal("customer transaction omitted immutable proof")
	}
	before, _ := productionCredentials(ProductionCustomerIdentity{Customer: original[0]})
	after, _ := productionCredentials(identity)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ordinary protocol credentials changed")
	}
}

func TestLegacyAbsentNodeRejectsHTTPNegativeAndIncompleteOrMismatchedProof(t *testing.T) {
	for _, name := range []string{"http-negative", "source", "future", "old", "before-capture", "fd", "inbounds-login", "inbounds-uuid", "clients-login", "clients-uuid", "subid", "wrong-node", "wrong-uuid"} {
		t.Run(name, func(t *testing.T) {
			raw, capture, options, box := legacyNormalizeFixture(t)
			evidence := legacyAbsentFixture(capture)
			capture.Bindings[0].SubID = ""
			capture.Bindings[0].ObservedAbsent = &evidence
			switch name {
			case "http-negative":
				capture.Bindings[0].ObservedAbsent = nil
			case "source":
				evidence.CustomersSHA256 = strings.Repeat("f", 64)
			case "future":
				evidence.ObservedAt = options.Now.Add(time.Second)
			case "old":
				evidence.ObservedAt = options.Now.Add(-2 * time.Minute)
			case "before-capture":
				evidence.ObservedAt = capture.CapturedAt.Add(-time.Nanosecond)
			case "fd":
				evidence.LiveDatabaseFDMatched = false
			case "inbounds-login":
				evidence.InboundsLoginAbsent = false
			case "inbounds-uuid":
				evidence.InboundsUUIDAbsent = false
			case "clients-login":
				evidence.ClientsLoginAbsent = false
			case "clients-uuid":
				evidence.ClientsUUIDAbsent = false
			case "subid":
				capture.Bindings[0].SubID = "invented"
			case "wrong-node":
				capture.Bindings[0].NodeID = "S2"
			case "wrong-uuid":
				capture.Bindings[0].UUID = "not-source-uuid"
			}
			if _, err := NormalizeLegacyCustomers(raw, capture, box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
				t.Fatal("unsupported absence became authority")
			}
		})
	}
}

func TestLegacyAbsentNodeDeltaRetainsImmutableObservationWithoutRearming(t *testing.T) {
	raw, capture, options, box := legacyNormalizeFixture(t)
	evidence := legacyAbsentFixture(capture)
	capture.Bindings[0].SubID = ""
	capture.Bindings[0].ObservedAbsent = &evidence
	parent := normalizeFixture(t, raw, capture, options, box)
	originalProof := parent.EncryptedSecrets[1]
	customers, _ := DecodeLegacyCustomers(raw)
	customers[0].Expires = customers[0].Expires.Add(time.Hour)
	raw = marshalNormalizeFixture(t, []legacystore.Customer{customers[0]})
	capture.CapturedAt = capture.CapturedAt.Add(10 * time.Second)
	capture.CompletedAt = capture.CompletedAt.Add(10 * time.Second)
	capture.CustomersSHA256 = sha256Hex(raw)
	nextEvidence := legacyAbsentFixture(capture)
	capture.Bindings[0].ObservedAbsent = &nextEvidence
	options.Now = options.Now.Add(10 * time.Second)
	options.Parent = &parent
	delta := normalizeFixture(t, raw, capture, options, box)
	if delta.EncryptedSecrets[1] != originalProof || delta.Customers[0].Generation != parent.Customers[0].Generation+1 {
		t.Fatal("delta resealed proof or lost customer revision")
	}
	if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(delta, &parent), box); err != nil {
		t.Fatal(err)
	}
	capture.Bindings[0].ObservedAbsent = nil
	capture.Bindings[0].SubID = "new-panel-user"
	if _, err := NormalizeLegacyCustomers(raw, capture, box, bytes.Repeat([]byte{0x22}, 32), options); err == nil {
		t.Fatal("new capture silently rearmed a quarantined binding")
	}
	forged := delta
	forged.EncryptedSecrets = append([]LegacyEncryptedSecret(nil), delta.EncryptedSecrets...)
	forged.EncryptedSecrets[1].Field = "other"
	if _, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(forged, &parent), box); err == nil {
		t.Fatal("wrong-AAD evidence accepted")
	}
	encoded, _ := json.Marshal(delta)
	if bytes.Contains(encoded, []byte(customers[0].SubToken)) {
		t.Fatal("source token leaked")
	}
}
