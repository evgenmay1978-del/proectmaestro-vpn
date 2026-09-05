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
	clockRows, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: "SELECT unixepoch() AS database_now_unix"})
	if err != nil || len(clockRows) != 1 || len(clockRows[0].Rows) != 1 {
		t.Fatal("production reader fixture database clock unavailable")
	}
	databaseNow, ok := applyRowInt(clockRows[0].Rows[0]["database_now_unix"])
	if !ok || databaseNow <= 0 {
		t.Fatal("production reader fixture database clock invalid")
	}
	clock := productionReaderClock{time.Unix(databaseNow, 0).UTC()}
	snapshot, identity, box := productionIdentityFixture(t)
	// The public reader enforces expiry against SQL unixepoch(), not the injected
	// service clock. Give this synthetic identity a live database-relative lease.
	identity.Customer.Expires = clock.Add(time.Hour)
	setProductionFixtureIdentity(t, &snapshot, identity, box)
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("synthetic production reader plan blocked")
	}
	const runID = "production-reader-roundtrip-v1"
	customerID := plan.Customers[0].InternalID
	// Imported identity and provenance rows are immutable. Like the package's
	// other canonical import seeds, this synthetic namespace remains until the
	// disposable CI cluster is torn down. Restore only shared backup-RPO state
	// through the existing lease- and receipt-bound cleanup CAS.
	backupCleanup := task6RegisterIntegrationBackupSeedCleanup(t, db)
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
