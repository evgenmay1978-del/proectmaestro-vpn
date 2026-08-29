package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestTask7CustomerFixtureCarriesEncryptedAccessIntoConfirmedDesiredStateSQLite(t *testing.T) {
	db, _ := newCustomerIntegritySQLite(t)
	task7FixtureRequest(t, db,
		rqlite.Statement{SQL: `UPDATE cluster_restore_state SET activated=1 WHERE singleton_id=1`},
		rqlite.Statement{SQL: `INSERT INTO tariff_versions(
tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix)
VALUES('task7-fixture-tariff','task7-fixture',30,40000,'RUB',1,1)`},
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix)
VALUES('task7-fixture-node','Task7 fixture node',0,1,1)`},
		rqlite.Statement{SQL: `INSERT INTO node_services(
node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix)
VALUES('task7-fixture-node','maestro-core',1,1,0,0,1)`},
	)

	box := task7FixtureSecretBox(t)
	clock := fixedClock{value: time.Unix(2_100_000_000, 0)}
	store, err := NewStore(db, box, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := NewService(store, &task7FixtureIDs{}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	const customerID = "task7-fixture-customer"
	wantAccess := task7SeedCanonicalFixtureCustomer(t, db, box, customerID, "active", 2_100_000_000, 4)

	order, err := service.CreateOrder(context.Background(), CreateOrderCommand{
		TariffVersionID: "task7-fixture-tariff", CustomerID: customerID,
		BuyerScope: "anonymous", Actor: "task7-fixture", Channel: "test",
		AmountMinor: 40000, Currency: "RUB", DurationSeconds: 30 * 86400,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if _, err := service.MarkPaymentClaimed(context.Background(), ClaimPaymentCommand{
		OrderID: order.OrderID, Actor: "task7-fixture", Channel: "test",
	}); err != nil {
		t.Fatalf("MarkPaymentClaimed: %v", err)
	}
	confirmed, err := service.ConfirmPayment(context.Background(), ConfirmPaymentCommand{
		OrderID: order.OrderID, IdempotencyKey: "task7-fixture-confirm",
		PaymentReference: "task7-fixture-receipt", Provider: "manual",
		TariffVersionID: "task7-fixture-tariff", Actor: "task7-fixture", Channel: "test",
	})
	if err != nil {
		t.Fatalf("ConfirmPayment with realistic fixture access: %v", err)
	}

	row := task7FixtureRow(t, db, rqlite.Statement{SQL: `SELECT node_id,service_name,generation,
operation_id,desired_envelope,desired_sha256,tombstone FROM desired_node_state WHERE customer_id=? AND node_id=? AND service_name=?`, Args: []any{customerID, "task7-fixture-node", "maestro-core"}})
	nodeID, _ := rowString(row, "node_id")
	serviceName, _ := rowString(row, "service_name")
	generation, _ := rowInt64(row, "generation")
	operationID, _ := rowString(row, "operation_id")
	digest, _ := rowString(row, "desired_sha256")
	tombstone, _ := rowInt64(row, "tombstone")
	encodedEnvelope, _ := rowString(row, "desired_envelope")
	envelopeBytes, err := base64.StdEncoding.DecodeString(encodedEnvelope)
	if err != nil {
		t.Fatalf("decode desired envelope: %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(envelopeBytes, &envelope); err != nil {
		t.Fatalf("unmarshal desired envelope: %v", err)
	}
	document, err := box.OpenDesiredPayload(DesiredPayloadScope{
		NodeID: nodeID, ServiceID: serviceName, CustomerID: customerID,
		Generation: generation, OperationID: operationID, Tombstone: tombstone != 0, PayloadKind: "customer-active",
	}, envelope, digest)
	if err != nil {
		t.Fatalf("OpenDesiredPayload: %v", err)
	}
	var body struct {
		Access CustomerAccess `json:"access"`
	}
	if err := json.Unmarshal(document.Body, &body); err != nil {
		t.Fatalf("decode desired body: %v", err)
	}
	if confirmed.Generation != generation || !reflect.DeepEqual(body.Access, wantAccess) {
		t.Fatalf("confirmed generation/access=(%d,%+v), desired=(%d,%+v)", confirmed.Generation, wantAccess, generation, body.Access)
	}
}

type task7FixtureIDs struct{ next int }

func (ids *task7FixtureIDs) NewID(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_task7_fixture_%d", prefix, ids.next), nil
}

func task7FixtureSecretBox(t *testing.T) *SecretBox {
	t.Helper()
	box, err := NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x71}, 32)}, bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatalf("task7 SecretBox: %v", err)
	}
	return box
}

func task7SeedCanonicalFixtureCustomer(
	t *testing.T, db rqlite.RQLite, box *SecretBox, customerID, status string, expiry, generation int64,
) CustomerAccess {
	t.Helper()
	loginDigest := sha256.Sum256([]byte(customerID))
	access := CustomerAccess{
		SubscriptionToken: customerID + "-subscription",
		Credentials:       make(map[string]string, len(canonicalCustomerProtocols)),
	}
	statements := []rqlite.Statement{{SQL: `INSERT INTO customers(
customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,?,?,?,1,1)`, Args: []any{
		customerID, customerID, hex.EncodeToString(loginDigest[:]), status, expiry, generation,
	}}}
	tokenEnvelope, tokenDigest := task7SealFixtureSecret(t, box, SecretScope{
		OwnerType: "customer", OwnerID: customerID, Field: "token", Kind: "subscription",
	}, access.SubscriptionToken)
	statements = append(statements, rqlite.Statement{SQL: `INSERT INTO subscription_tokens(
token_id,customer_id,token_hmac,token_envelope,token_sha256,generation,revoked,created_at_unix)
VALUES(?,?,?,?,?,?,0,1)`, Args: []any{
		customerID + "-token", customerID,
		box.LookupHMAC("subscription-token", []byte(access.SubscriptionToken)),
		tokenEnvelope, tokenDigest, generation,
	}})
	for _, protocol := range canonicalCustomerProtocols {
		raw := customerID + "-" + protocol + "-credential"
		access.Credentials[protocol] = raw
		envelope, digest := task7SealFixtureSecret(t, box, SecretScope{
			OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: protocol,
		}, raw)
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO credentials(
credential_id,customer_id,protocol,secret_envelope,secret_sha256,generation,enabled,created_at_unix,updated_at_unix)
VALUES(?,?,?,?,?,?,1,1,1)`, Args: []any{
			customerID + "-credential-" + protocol, customerID, protocol, envelope, digest, generation,
		}})
	}
	task7FixtureRequest(t, db, statements...)
	return access
}

func task7SealFixtureSecret(t *testing.T, box *SecretBox, scope SecretScope, raw string) ([]byte, string) {
	t.Helper()
	envelope, err := box.Seal(scope, []byte(raw))
	if err != nil {
		t.Fatalf("seal task7 fixture access: %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode task7 fixture access: %v", err)
	}
	digest := sha256.Sum256([]byte(raw))
	return encoded, hex.EncodeToString(digest[:])
}

func task7FixtureRequest(t *testing.T, db rqlite.RQLite, statements ...rqlite.Statement) []rqlite.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	results, err := db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		t.Fatalf("task7 fixture Request: %v", err)
	}
	return results
}

func task7FixtureRow(t *testing.T, db rqlite.RQLite, statement rqlite.Statement) map[string]any {
	t.Helper()
	results, err := db.QueryLinearizable(context.Background(), statement)
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("task7 fixture row results=%#v err=%v", results, err)
	}
	return results[0].Rows[0]
}
