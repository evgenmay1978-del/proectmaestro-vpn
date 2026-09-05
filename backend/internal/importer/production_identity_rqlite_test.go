//go:build rqlite_integration

package importer

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type productionReaderClock struct{ time.Time }

func (clock productionReaderClock) Now() time.Time { return clock.Time }

type productionReaderNoMint struct{}

func (productionReaderNoMint) NewID(string) (string, error) {
	return "", errors.New("imported ordinary reader must not mint replacement identities")
}

// Uses the exact production-mode constructor selected by maestro-import's
// factory, actual rqlite SQL, and the exported production subscription adapter.
func TestProductionImportOrdinarySubscriptionRoundTripRQLite(t *testing.T) {
	db, err := rqlite.New(rqlite.Config{Endpoints: []string{
		"http://127.0.0.1:4401", "http://127.0.0.1:4403", "http://127.0.0.1:4405"}, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, identity, box := productionIdentityFixture(t)
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("synthetic production reader plan blocked")
	}
	const runID = "production-reader-roundtrip-v1"
	customerID, sourceKey, secretID := plan.Customers[0].InternalID, snapshot.Customers[0].SourceKey, snapshot.EncryptedSecrets[0].SecretID
	// The package's integration lease serializes this exact fixture cleanup.
	// Register before backup cleanup so its receipt remains available to that CAS.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := db.Request(cleanupCtx, rqlite.Linearizable, true,
			rqlite.Statement{SQL: "DELETE FROM desired_protocol_tags WHERE customer_id=?", Args: []any{customerID}},
			rqlite.Statement{SQL: "DELETE FROM desired_node_state WHERE customer_id=?", Args: []any{customerID}},
			rqlite.Statement{SQL: "DELETE FROM credentials WHERE customer_id=?", Args: []any{customerID}},
			rqlite.Statement{SQL: "DELETE FROM subscription_tokens WHERE customer_id=?", Args: []any{customerID}},
			rqlite.Statement{SQL: "DELETE FROM imported_secrets WHERE secret_id=?", Args: []any{secretID}},
			rqlite.Statement{SQL: "DELETE FROM imported_entity_state WHERE (entity_kind='customer' AND source_key=?) OR (entity_kind='encrypted_secret' AND source_key=?)", Args: []any{sourceKey, secretID}},
			rqlite.Statement{SQL: "DELETE FROM customers WHERE customer_id=?", Args: []any{customerID}},
			rqlite.Statement{SQL: "DELETE FROM import_batches WHERE import_run_id=?", Args: []any{runID}},
			rqlite.Statement{SQL: "DELETE FROM import_runs WHERE import_run_id=?", Args: []any{runID}},
		)
		if err != nil {
			t.Error("synthetic production reader fixture cleanup failed")
		}
	})
	backupCleanup := task6RegisterIntegrationBackupSeedCleanup(t, db)
	clock := productionReaderClock{time.Unix(1_500_000, 0)}
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), box)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProductionRQLiteApplyStore(db, clock.Now, protection, nil)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatal(err)
	}
	batch := ApplyBatch{RunID: runID, PlanDigest: plan.PlanDigest, Index: 0, Digest: digestBatch(operations), Operations: operations}
	backupCleanup.Expect(task6BackupRPOCleanupExpectation{DirtyGenerationDelta: 2, UpdatedAtUnix: clock.Unix(),
		Receipt: task6ImportRunReceipt{RunID: runID, SourceDigest: plan.SourceDigest, PlanDigest: plan.PlanDigest, Status: "applied"}})
	if _, err := store.BeginOrResume(ctx, ApplyRun{RunID: runID, SnapshotKind: "full", SourceDigest: plan.SourceDigest, PlanDigest: plan.PlanDigest, BatchCount: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	target, err := store.InspectTarget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, ApplyCompletion{RunID: runID, SourceDigest: plan.SourceDigest, PlanDigest: plan.PlanDigest, TargetDigest: target.BusinessDigest}); err != nil {
		t.Fatal(err)
	}
	readerStore, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(readerStore, productionReaderNoMint{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := service.CustomerByToken(ctx, identity.Customer.SubToken)
	if err != nil || customer.ID != customerID || customer.Generation != identity.Generation || customer.ExpiresAtUnix != identity.Customer.Expires.Unix() {
		t.Fatal("actual CustomerByToken lost the imported ordinary identity")
	}
	access, err := service.BusinessCustomerByToken(ctx, identity.Customer.SubToken)
	wantCredentials, credentialErr := productionCredentials(identity)
	if err != nil || credentialErr != nil || access.Access.SubscriptionToken != identity.Customer.SubToken || !reflect.DeepEqual(access.Access.Credentials, wantCredentials) {
		t.Fatal("actual production access reader failed imported credential roundtrip")
	}
	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{Now: clock.Now, SubscriptionTopology: identity.Customer.ToSubgen()})
	result, err := business.SubscriptionSnapshot(ctx, identity.Customer.SubToken)
	if err != nil || !result.Customer.Active || len(result.Document) == 0 {
		t.Fatal("actual production subscription snapshot rejected imported credentials")
	}
	expected, err := subgen.GenerateSingbox(identity.Customer.ToSubgen())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Document, expected) {
		t.Fatal("ordinary subscription changed legacy S1/S3/S4 UUIDs, endpoints or passwords")
	}
}
