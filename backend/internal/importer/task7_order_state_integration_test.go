//go:build rqlite_integration

package importer

import (
	"context"
	"fmt"
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

	_, err = db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT INTO customers(
customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,'active',2100000000,4,1,1)
ON CONFLICT(customer_id) DO UPDATE SET status='active',expires_at_unix=2100000000,generation=4`,
			Args: []any{customerID, "task7-imported-order-customer", strings.Repeat("7", 64)}},
		rqlite.Statement{SQL: `INSERT INTO tariff_versions(
tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix)
VALUES(?, ?,30,40000,'RUB',1,1)
ON CONFLICT(tariff_version_id) DO UPDATE SET active=1`, Args: []any{tariffVersion, "task7-imported-order-tariff"}},
	)
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

	encryptionKey := make([]byte, 32)
	hmacKey := make([]byte, 32)
	for index := range encryptionKey {
		encryptionKey[index] = 0x31
		hmacKey[index] = 0x32
	}
	secrets, err := controlplane.NewSecretBox(1, map[int][]byte{1: encryptionKey}, hmacKey)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	clock := task7ImporterClock{value: time.Unix(1_800_000_200, 0)}
	controlStore, err := controlplane.NewStore(db, secrets, clock)
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
	})
}
