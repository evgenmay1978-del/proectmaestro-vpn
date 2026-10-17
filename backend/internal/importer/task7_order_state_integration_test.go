//go:build rqlite_integration

package importer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type task7ImporterIDs struct{ next int }

func (ids *task7ImporterIDs) NewID(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_importer_%d", prefix, ids.next), nil
}

type task7ImporterClock struct{ value time.Time }

func (clock task7ImporterClock) Now() time.Time { return clock.value }

func TestImportedNonterminalOrdersRemainActionable(t *testing.T) {
	db, err := rqlite.New(rqlite.Config{
		Endpoints: []string{"http://127.0.0.1:4401", "http://127.0.0.1:4403", "http://127.0.0.1:4405"},
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("rqlite.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	const (
		runID          = "task7-imported-order-states-v1"
		sourceDigest   = "7777777777777777777777777777777777777777777777777777777777777777"
		planDigest     = "8888888888888888888888888888888888888888888888888888888888888888"
		customerID     = "task7-imported-order-customer"
		tariffVersion  = "task7-imported-order-tariff-v1"
		createdOrderID = "task7-imported-created-order"
		claimedOrderID = "task7-imported-claimed-order"
	)
	secretBox := task7ImporterSecretBox(t)
	wantAccess, accessStatements := task7ImporterAccessFixture(t, secretBox, customerID, 4)

	backupCleanup := task6RegisterIntegrationBackupSeedCleanup(t, db)
	t.Cleanup(func() {
		current, readErr := task6ReadIntegrationBackupState(context.Background(), db)
		if readErr != nil {
			t.Errorf("read backup state for cleanup: %v", readErr)
			return
		}
		if current == backupCleanup.baseline {
			return
		}
		backupCleanup.Expect(task6BackupRPOCleanupExpectation{
			DirtyGenerationDelta: current.DirtyGeneration - backupCleanup.baseline.DirtyGeneration,
			UpdatedAtUnix:        current.UpdatedAtUnix,
			Receipt: task6ImportRunReceipt{
				RunID: runID, SourceDigest: sourceDigest, PlanDigest: planDigest, Status: "applying",
			},
		})
	})

	seedStatements := []rqlite.Statement{
		rqlite.Statement{SQL: `INSERT INTO customers(
customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,'active',2100000000,4,1,1)
ON CONFLICT(customer_id) DO UPDATE SET status='active',expires_at_unix=2100000000,generation=4`,
			Args: []any{customerID, "task7-imported-order-customer", strings.Repeat("7", 64)}},
		rqlite.Statement{SQL: `INSERT INTO tariff_versions(
tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix)
VALUES(?, ?,30,40000,'RUB',1,1)
ON CONFLICT(tariff_version_id) DO UPDATE SET active=1`, Args: []any{tariffVersion, "task7-imported-order-tariff"}},
	}
	seedStatements = append(seedStatements, accessStatements...)
	_, err = db.Request(ctx, rqlite.Linearizable, true, seedStatements...)
	if err != nil {
		t.Fatalf("seed import dependencies: %v", err)
	}

	orders := []PlannedOrder{
		{
			InternalID: createdOrderID, SourceKey: createdOrderID, CustomerInternalID: customerID,
			BuyerScope: "anonymous", BuyerKeyHMAC: strings.Repeat("8", 64), TariffVersionID: tariffVersion,
			AmountMinor: 40000, Currency: "RUB", DurationDays: 30, PaymentCode: "TASK7-CREATED",
			CreatedAtUnix: 2100000000, ExpiresAtUnix: 2100086400,
			PaymentState: "created", ProvisioningState: "pending", ImportState: "created",
		},
		{
			InternalID: claimedOrderID, SourceKey: claimedOrderID, CustomerInternalID: customerID,
			BuyerScope: "anonymous", BuyerKeyHMAC: strings.Repeat("9", 64), TariffVersionID: tariffVersion,
			AmountMinor: 40000, Currency: "RUB", DurationDays: 30, PaymentCode: "TASK7-CLAIMED",
			CreatedAtUnix: 2100000000, ExpiresAtUnix: 2100086400,
			PaymentState: "claimed", ProvisioningState: "pending", ImportState: "claimed",
		},
	}
	operations, err := planOperations(ImportPlan{Orders: orders})
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	batch := ApplyBatch{RunID: runID, PlanDigest: planDigest, Index: 0, Operations: operations}
	batch.Digest = digestBatch(batch.Operations)
	importStore, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_800_000_100, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := importStore.BeginOrResume(ctx, ApplyRun{
		RunID: runID, SnapshotKind: "full", SourceDigest: sourceDigest, PlanDigest: planDigest, BatchCount: 1,
	}); err != nil {
		t.Fatalf("BeginOrResume: %v", err)
	}
	if _, err := importStore.CommitBatch(ctx, batch); err != nil {
		t.Fatalf("CommitBatch: %v", err)
	}

	clock := task7ImporterClock{value: time.Unix(1_800_000_200, 0)}
	controlStore, err := controlplane.NewStore(db, secretBox, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := controlplane.NewService(controlStore, &task7ImporterIDs{}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	t.Run("created can be claimed", func(t *testing.T) {
		claimed, claimErr := service.MarkPaymentClaimed(ctx, controlplane.ClaimPaymentCommand{
			OrderID: createdOrderID, Actor: "task7-review", Channel: "import-regression",
		})
		if claimErr != nil {
			t.Fatalf("claim imported created order: %v", claimErr)
		}
		if claimed.PaymentState != controlplane.PaymentClaimed {
			t.Fatalf("claimed state=%q, want %q", claimed.PaymentState, controlplane.PaymentClaimed)
		}
	})

	t.Run("claimed can be confirmed", func(t *testing.T) {
		confirmed, confirmErr := service.ConfirmPayment(ctx, controlplane.ConfirmPaymentCommand{
			OrderID: claimedOrderID, IdempotencyKey: "task7-imported-claim-confirm",
			PaymentReference: "task7-imported-claim-receipt", TariffVersionID: tariffVersion,
			Actor: "task7-review", Channel: "import-regression",
		})
		if confirmErr != nil {
			t.Fatalf("confirm imported claimed order: %v", confirmErr)
		}
		if confirmed.OrderID != claimedOrderID || confirmed.Generation != 5 {
			t.Fatalf("confirmation result=%#v", confirmed)
		}
		task7AssertImportedDesiredAccess(t, ctx, db, secretBox, customerID, confirmed.Generation, wantAccess)
	})
}

func task7ImporterSecretBox(t *testing.T) *controlplane.SecretBox {
	t.Helper()
	encryptionKey := make([]byte, 32)
	hmacKey := make([]byte, 32)
	for index := range encryptionKey {
		encryptionKey[index] = 0x31
		hmacKey[index] = 0x32
	}
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: encryptionKey}, hmacKey)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	return box
}

func task7ImporterAccessFixture(
	t *testing.T, box *controlplane.SecretBox, customerID string, generation int64,
) (controlplane.CustomerAccess, []rqlite.Statement) {
	t.Helper()
	access := controlplane.CustomerAccess{
		SubscriptionToken: customerID + "-subscription",
		Credentials:       make(map[string]string, 4),
	}
	tokenEnvelope, tokenDigest := task7ImporterSealAccess(t, box, controlplane.SecretScope{
		OwnerType: "customer", OwnerID: customerID, Field: "token", Kind: "subscription",
	}, access.SubscriptionToken)
	statements := []rqlite.Statement{{SQL: `INSERT INTO subscription_tokens(
token_id,customer_id,token_hmac,token_envelope,token_sha256,generation,revoked,created_at_unix)
VALUES(?,?,?,?,?,?,0,1)
ON CONFLICT(token_id) DO UPDATE SET
customer_id=excluded.customer_id,token_hmac=excluded.token_hmac,token_envelope=excluded.token_envelope,
token_sha256=excluded.token_sha256,generation=excluded.generation,revoked=0`, Args: []any{
		customerID + "-token", customerID,
		box.LookupHMAC("subscription-token", []byte(access.SubscriptionToken)),
		tokenEnvelope, tokenDigest, generation,
	}}}
	for _, protocol := range []string{"anytls", "hysteria2", "naive", "vless"} {
		raw := customerID + "-" + protocol + "-credential"
		access.Credentials[protocol] = raw
		envelope, digest := task7ImporterSealAccess(t, box, controlplane.SecretScope{
			OwnerType: "customer", OwnerID: customerID, Field: "credential", Kind: protocol,
		}, raw)
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO credentials(
credential_id,customer_id,protocol,secret_envelope,secret_sha256,generation,enabled,created_at_unix,updated_at_unix)
VALUES(?,?,?,?,?,?,1,1,1)
ON CONFLICT(credential_id) DO UPDATE SET
customer_id=excluded.customer_id,protocol=excluded.protocol,secret_envelope=excluded.secret_envelope,
secret_sha256=excluded.secret_sha256,generation=excluded.generation,enabled=1,updated_at_unix=1`, Args: []any{
			customerID + "-credential-" + protocol, customerID, protocol, envelope, digest, generation,
		}})
	}
	return access, statements
}

func task7ImporterSealAccess(
	t *testing.T, box *controlplane.SecretBox, scope controlplane.SecretScope, raw string,
) ([]byte, string) {
	t.Helper()
	envelope, err := box.Seal(scope, []byte(raw))
	if err != nil {
		t.Fatalf("seal imported access fixture: %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode imported access fixture: %v", err)
	}
	digest := sha256.Sum256([]byte(raw))
	return encoded, hex.EncodeToString(digest[:])
}

func task7AssertImportedDesiredAccess(
	t *testing.T,
	ctx context.Context,
	db rqlite.RQLite,
	box *controlplane.SecretBox,
	customerID string,
	wantGeneration int64,
	wantAccess controlplane.CustomerAccess,
) {
	t.Helper()
	results, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT
node_id,service_name,CAST(generation AS TEXT) AS generation,operation_id,
desired_envelope,desired_sha256,CAST(tombstone AS TEXT) AS tombstone
FROM desired_node_state
WHERE customer_id=? ORDER BY node_id,service_name LIMIT 1`,
		Args: []any{customerID}})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("read imported desired state: results=%#v err=%v", results, err)
	}
	row := results[0].Rows[0]
	nodeID := task7ImporterRowString(t, row, "node_id")
	serviceName := task7ImporterRowString(t, row, "service_name")
	generation, err := strconv.ParseInt(task7ImporterRowString(t, row, "generation"), 10, 64)
	if err != nil {
		t.Fatalf("parse imported desired generation: %v", err)
	}
	tombstone := task7ImporterRowString(t, row, "tombstone") == "1"
	envelopeBytes, err := base64.StdEncoding.DecodeString(task7ImporterRowString(t, row, "desired_envelope"))
	if err != nil {
		t.Fatalf("decode imported desired envelope: %v", err)
	}
	var envelope controlplane.Envelope
	if err := json.Unmarshal(envelopeBytes, &envelope); err != nil {
		t.Fatalf("unmarshal imported desired envelope: %v", err)
	}
	document, err := box.OpenDesiredPayload(controlplane.DesiredPayloadScope{
		NodeID: nodeID, ServiceID: serviceName, CustomerID: customerID,
		Generation: generation, OperationID: task7ImporterRowString(t, row, "operation_id"),
		Tombstone: tombstone, PayloadKind: "customer-active",
	}, envelope, task7ImporterRowString(t, row, "desired_sha256"))
	if err != nil {
		t.Fatalf("open imported desired payload: %v", err)
	}
	var body struct {
		Access controlplane.CustomerAccess `json:"access"`
	}
	if err := json.Unmarshal(document.Body, &body); err != nil {
		t.Fatalf("decode imported desired payload: %v", err)
	}
	if generation != wantGeneration || !reflect.DeepEqual(body.Access, wantAccess) {
		t.Fatalf("imported desired generation/access=(%d,%+v), want=(%d,%+v)",
			generation, body.Access, wantGeneration, wantAccess)
	}
}

func task7ImporterRowString(t *testing.T, row map[string]any, key string) string {
	t.Helper()
	value, ok := row[key].(string)
	if !ok || value == "" {
		t.Fatalf("imported desired row[%q]=%#v, want non-empty string", key, row[key])
	}
	return value
}
