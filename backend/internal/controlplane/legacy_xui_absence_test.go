package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func seedLegacyXUIAbsence(t *testing.T, db *customerIntegritySQLite, service *Service, customer Customer) LegacyXUIAbsentBinding {
	t.Helper()
	source := LegacyExactCustomerSourcePrefix + service.store.secrets.LookupHMAC("customer-login", []byte("absentbinding")) + ":" + service.store.secrets.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte("AbsentBinding"))
	record := LegacyXUIAbsentBinding{SchemaVersion: 1, CustomerID: customer.ID, CustomerSourceKey: source, NodeID: "S1", Server: "s1.example.test", LoginKeyHMAC: service.store.secrets.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte("AbsentBinding")), UUIDHMAC: service.store.secrets.LookupHMAC("customer-uuid", []byte("source-uuid")), Observation: LegacyXUIAbsenceEvidence{SchemaVersion: 1, Method: "xui-sqlite-live-fd-wal-snapshot-v1", ObservedAt: time.Unix(1_000_000, 0).UTC(), CustomersSHA256: strings.Repeat("c", 64), DatabaseSnapshotSHA256: strings.Repeat("d", 64), DatabaseDevice: 1, DatabaseInode: 2, XUIPID: 3, XUIStartTicks: 4, LiveDatabaseFDMatched: true, InboundsLoginAbsent: true, InboundsUUIDAbsent: true, ClientsLoginAbsent: true, ClientsUUIDAbsent: true}}
	raw, _ := json.Marshal(record)
	id := LegacyXUIAbsenceID(customer.ID, "S1")
	owner := LegacyXUIAbsenceOwnerKey(customer.ID, "S1")
	envelope, err := service.store.secrets.Seal(SecretScope{OwnerType: LegacyXUIAbsenceOwner, OwnerID: owner, Field: "observed_absent", Kind: LegacyXUIAbsenceKind}, raw)
	if err != nil {
		t.Fatal(err)
	}
	secret := legacyNodeSecret{SecretID: id, OwnerType: LegacyXUIAbsenceOwner, OwnerSourceKey: owner, Field: "observed_absent", Kind: LegacyXUIAbsenceKind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: legacyNodeDigest(raw)}
	encoded, _ := json.Marshal(secret)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix) VALUES(?,?,?,?,?,?,?,?,?)`, Args: []any{id, secret.OwnerType, owner, secret.Field, secret.Kind, secret.KeyVersion, string(encoded), secret.SHA256, 1_000_000}},
		rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('encrypted_secret',?,?,?,'active',?)`, Args: []any{id, id, legacyNodeDigest(encoded), 1_000_000}})
	return record
}

func absentDesired(t *testing.T, service *Service, customerID, node, kind string, generation int64, body any) DesiredState {
	t.Helper()
	desired := DesiredState{CustomerID: customerID, NodeID: node, ServiceName: "maestro-core", OperationID: "absent-operation-" + customerID + "-" + node, EventKind: "customer_desired", Generation: generation}
	var err error
	desired.Payload, desired.PayloadSHA256, err = service.store.secrets.SealDesiredPayload(DesiredPayloadScope{CustomerID: customerID, NodeID: node, ServiceID: desired.ServiceName, OperationID: desired.OperationID, Generation: generation, PayloadKind: kind}, body)
	if err != nil {
		t.Fatal(err)
	}
	return desired
}

func seedAbsentDesired(t *testing.T, db *customerIntegritySQLite, desired DesiredState) {
	t.Helper()
	envelope, _ := json.Marshal(desired.Payload)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,updated_at_unix,tombstone,operation_id) VALUES(?,?,?,?,?,?,'pending',1000000,0,?)`, Args: []any{desired.CustomerID, desired.NodeID, desired.ServiceName, desired.Generation, string(envelope), desired.PayloadSHA256, desired.OperationID}})
}

func TestLegacyXUIAbsenceScopesRuntimeToOneBindingSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedExactLoginCustomer(t, db, service, "AbsentBinding")
	other := seedExactLoginCustomer(t, db, service, "PresentBinding")
	record := seedLegacyXUIAbsence(t, db, service, customer)
	slot, err := service.legacyXUIAbsence(ctx, customer.ID, "S1")
	if err != nil || !slot.present || slot.record != record {
		t.Fatalf("authenticated absence: %v", err)
	}
	before := db.snapshot(t)
	typed := absentDesired(t, service, customer.ID, "S1", LegacyXUIPayloadKind, 2, map[string]any{"login": "AbsentBinding", "uuid": "source-uuid", "sub_id": "must-not-mint", "absolute_expiry_unix": 3_000_000})
	if err := service.UpsertDesired(ctx, typed); !errors.Is(err, ErrForbidden) {
		t.Fatalf("typed XUI upsert: %v", err)
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("denied XUI changed durable state")
	}
	payload := map[string]any{"status": "active", "expires_at_unix": 3_000_000}
	if err := service.appendLegacyXUIAbsence(ctx, customer.ID, "S1", payload); err != nil {
		t.Fatal(err)
	}
	generic := absentDesired(t, service, customer.ID, "S1", "customer-active", 2, payload)
	if err := service.UpsertDesired(ctx, generic); err != nil {
		t.Fatalf("other protocols were blocked: %v", err)
	}
	if err := service.appendLegacyXUIAbsence(ctx, customer.ID, "S3", payload); err != nil {
		t.Fatal(err)
	}
	if _, leaked := payload[LegacyXUIAbsenceMarker]; leaked {
		t.Fatal("S1 quarantine leaked to S3")
	}
	present := absentDesired(t, service, other.ID, "S1", LegacyXUIPayloadKind, 1, map[string]any{"login": "PresentBinding", "uuid": "present-uuid", "sub_id": "existing", "absolute_expiry_unix": 3_000_000})
	if err := service.UpsertDesired(ctx, present); err != nil {
		t.Fatalf("other customer blocked: %v", err)
	}
}

func TestLegacyXUIAbsenceRejectsOldTypedReconcileAndReceiptSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedExactLoginCustomer(t, db, service, "AbsentBinding")
	other := seedExactLoginCustomer(t, db, service, "PresentBinding")
	seedLegacyXUIAbsence(t, db, service, customer)
	typed := absentDesired(t, service, customer.ID, "S1", LegacyXUIPayloadKind, 1, map[string]any{"login": "AbsentBinding", "uuid": "source-uuid", "sub_id": "invented", "absolute_expiry_unix": 3_000_000})
	present := absentDesired(t, service, other.ID, "S1", LegacyXUIPayloadKind, 1, map[string]any{"login": "PresentBinding", "uuid": "present-uuid", "sub_id": "existing", "absolute_expiry_unix": 3_000_000})
	seedAbsentDesired(t, db, typed)
	seedAbsentDesired(t, db, present)
	count, err := service.ReconcileNode(ctx, ReconcileNodeCommand{NodeID: "S1", ServiceName: "maestro-core"})
	if err != nil || count != 1 {
		t.Fatalf("reconcile must preserve only unaffected work: count=%d err=%v", count, err)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT aggregate_id FROM outbox_events`})
	if len(rows[0].Rows) != 1 || rows[0].Rows[0]["aggregate_id"] != other.ID+":S1:maestro-core" {
		t.Fatal("quarantined XUI entered reconcile outbox")
	}
	before := db.snapshot(t)
	err = service.RecordApplyReceipt(ctx, ApplyReceipt{ReceiptID: "forbidden-receipt", CustomerID: customer.ID, NodeID: "S1", ServiceName: "maestro-core", OperationID: typed.OperationID, HolderID: "agent", Generation: 1, ClusterEpoch: 1, NodeIncarnation: 1, LeaseFence: 1, DesiredSHA256: typed.PayloadSHA256, ObservedSHA256: strings.Repeat("e", 64)})
	if !errors.Is(err, ErrForbidden) || !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("quarantined binding gained a receipt")
	}
}

func TestLegacyXUIAbsencePreservesAuthenticatedOtherProtocolKindsSQLite(t *testing.T) {
	for _, kind := range []string{"hysteria2", "anytls"} {
		t.Run(kind, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			ctx := context.Background()
			customer := seedExactLoginCustomer(t, db, service, "AbsentBinding")
			seedLegacyXUIAbsence(t, db, service, customer)
			desired := absentDesired(t, service, customer.ID, "S1", kind, 2, map[string]any{"password": "original-other-protocol"})
			scope := DesiredPayloadScope{CustomerID: customer.ID, NodeID: "S1", ServiceID: desired.ServiceName, OperationID: desired.OperationID, Generation: desired.Generation, PayloadKind: kind}
			if _, err := service.store.secrets.OpenDesiredPayload(scope, desired.Payload, desired.PayloadSHA256); err != nil {
				t.Fatal("valid other-protocol fixture did not authenticate")
			}
			scope.PayloadKind = LegacyXUIPayloadKind
			if _, err := service.store.secrets.OpenDesiredPayload(scope, desired.Payload, desired.PayloadSHA256); err == nil {
				t.Fatal("payload kind was not cryptographically bound")
			}
			if err := service.UpsertDesired(ctx, desired); err != nil {
				t.Fatalf("other protocol upsert blocked: %v", err)
			}
			if count, err := service.ReconcileNode(ctx, ReconcileNodeCommand{NodeID: "S1", ServiceName: desired.ServiceName}); err != nil || count != 1 {
				t.Fatalf("other protocol reconcile blocked: count=%d err=%v", count, err)
			}
			unmarked := absentDesired(t, service, customer.ID, "S1", "customer-active", 3, map[string]any{"status": "active"})
			if err := service.UpsertDesired(ctx, unmarked); !errors.Is(err, ErrForbidden) {
				t.Fatal("generic payload dropped immutable marker")
			}
		})
	}
}

func TestLegacyXUIAbsenceSQLGuardClosesImportRaceSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedExactLoginCustomer(t, db, service, "AbsentBinding")
	desired := absentDesired(t, service, customer.ID, "S1", LegacyXUIPayloadKind, 1, map[string]any{"login": "AbsentBinding", "uuid": "source-uuid", "sub_id": "invented", "absolute_expiry_unix": 3_000_000})
	db.beforeRequest = func() { seedLegacyXUIAbsence(t, db, service, customer) }
	if err := service.UpsertDesired(ctx, desired); err == nil {
		t.Fatal("read-before-import race authorized XUI")
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT customer_id FROM desired_node_state`}, rqlite.Statement{SQL: `SELECT event_id FROM outbox_events`})
	if len(rows[0].Rows) != 0 || len(rows[1].Rows) != 0 {
		t.Fatal("race published XUI work")
	}
}
