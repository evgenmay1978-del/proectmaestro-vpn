//go:build rqlite_integration

package importer

import (
	"bytes"
	"context"
	"encoding/json"
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
	clock := productionReaderClock{time.Unix(databaseNow+2, 0).UTC()}
	snapshot, identity, box := productionIdentityFixture(t)
	const originalDevice = "synthetic-original-device"
	const removedDevice = "synthetic-removed-device"
	snapshot.CapturedAt = time.Unix(databaseNow, 0).UTC()
	identity.Customer.Expires = clock.Add(time.Hour)
	identity.Customer.Devices = map[string]time.Time{originalDevice: clock.Add(-time.Hour), removedDevice: clock.Add(-2 * time.Hour)}
	setProductionFixtureIdentity(t, &snapshot, identity, box)
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatal("synthetic production reader plan blocked")
	}
	const runID = "production-reader-roundtrip-v2"
	customerID := plan.Customers[0].InternalID
	// Imported identity and provenance rows are immutable. Like the package's
	// other canonical import seeds, this synthetic namespace remains until the
	// disposable CI cluster is torn down. Restore only shared backup-RPO state
	// through the existing lease- and receipt-bound cleanup CAS.
	backupCleanup := task6RegisterIntegrationBackupSeedCleanup(t, db)
	delta, deltaIdentity := productionDeltaFixture(t, snapshot, identity, box)
	delta.CapturedAt = time.Unix(databaseNow+1, 0).UTC()
	deltaIdentity.Customer.Devices = map[string]time.Time{originalDevice: identity.Customer.Devices[originalDevice]}
	setProductionFixtureIdentity(t, &delta, deltaIdentity, box)
	deltaOptions := testPlanOptions()
	deltaOptions.ParentSnapshot = &snapshot
	deltaOptions.AppliedParentDigest = plan.SourceDigest
	deltaPlan, deltaReport := Plan(delta, deltaOptions)
	if len(deltaReport.Blockers) != 0 {
		t.Fatal("production final delta plan blocked")
	}
	backupCleanup.Expect(task6BackupRPOCleanupExpectation{DirtyGenerationDelta: 4, UpdatedAtUnix: clock.Unix(),
		Receipt: task6ImportRunReceipt{RunID: runID + "-delta", SourceDigest: deltaPlan.SourceDigest, PlanDigest: deltaPlan.PlanDigest, Status: "applied"}})
	apply := func(source Snapshot, parent *Snapshot, p ImportPlan, id string) *RQLiteApplyStore {
		t.Helper()
		store, batch := productionReaderBatch(t, db, clock, source, parent, p, id, box)
		if _, err := store.BeginOrResume(ctx, ApplyRun{RunID: id, SnapshotKind: source.SnapshotKind, ParentDigest: source.ParentSourceDigest, SourceDigest: p.SourceDigest, PlanDigest: p.PlanDigest, BatchCount: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		target, err := store.InspectTarget(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Complete(ctx, ApplyCompletion{RunID: id, SourceDigest: p.SourceDigest, PlanDigest: p.PlanDigest, TargetDigest: target.BusinessDigest}); err != nil {
			t.Fatal(err)
		}
		return store
	}
	apply(snapshot, nil, plan, runID)
	readerStore, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(readerStore, productionReaderNoMint{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	assertOrdinary := func(want ProductionCustomerIdentity) {
		t.Helper()
		customer, err := service.CustomerByToken(ctx, want.Customer.SubToken)
		if err != nil || customer.ID != customerID || customer.Generation != want.Generation || customer.ExpiresAtUnix != want.Customer.Expires.Unix() {
			t.Fatal("actual CustomerByToken lost imported ordinary identity")
		}
		access, err := service.BusinessCustomerByToken(ctx, want.Customer.SubToken)
		wantCredentials, credentialErr := productionCredentials(want)
		if err != nil || credentialErr != nil || access.Login != want.Customer.Login || access.Access.SubscriptionToken != want.Customer.SubToken || !reflect.DeepEqual(access.Access.Credentials, wantCredentials) || access.Access.CredentialUsernames["naive"] != want.Customer.Naive.Username {
			t.Fatal("actual production access reader lost original credentials or username")
		}
		state, err := service.BusinessSubscriptionSnapshot(ctx, want.Customer.SubToken, originalDevice)
		if err != nil || !state.DeviceCommitted || state.DeviceKeyHMAC != box.LookupHMAC("device-identity", []byte(originalDevice)) || state.Customer.Access.CredentialUsernames["naive"] != want.Customer.Naive.Username {
			t.Fatal("strong production reader lost imported device or username")
		}
		business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{Now: clock.Now, SubscriptionTopology: want.Customer.ToSubgen()})
		result, err := business.SubscriptionSnapshot(ctx, want.Customer.SubToken)
		if err != nil || !result.Customer.Active || len(result.Document) == 0 {
			t.Fatal("actual production subscription rejected imported credentials")
		}
		expected, err := subgen.GenerateSingbox(want.Customer.ToSubgen())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(result.Document, expected) {
			t.Fatal("ordinary subscription changed legacy UUIDs, endpoints, username or passwords")
		}
	}
	assertOrdinary(identity)
	originalHMAC := box.LookupHMAC("device-identity", []byte(originalDevice))
	beforeDevice, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT device_id FROM devices WHERE customer_id=? AND device_key_hmac=?`, Args: []any{customerID, originalHMAC}})
	if err != nil || len(beforeDevice) != 1 || len(beforeDevice[0].Rows) != 1 {
		t.Fatal("imported original device missing")
	}
	originalID := beforeDevice[0].Rows[0]["device_id"]
	// Claims after the source capture neither lose their slot nor have their
	// more recent last-seen timestamp rolled back by the final delta.
	_, err = db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE devices SET last_seen_at_unix=? WHERE customer_id=? AND device_key_hmac=?`, Args: []any{clock.Unix(), customerID, originalHMAC}},
		rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix) VALUES (?,?,?,'maestro',?,0,?)`, Args: []any{"production-concurrent-device", customerID, box.LookupHMAC("device-identity", []byte("synthetic-concurrent-device")), clock.Unix(), clock.Unix()}},
		rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix) VALUES (?,?,?,'maestro',?,0,?)`, Args: []any{"production-target-only-device", customerID, box.LookupHMAC("device-identity", []byte("synthetic-target-only-device")), clock.Add(-30 * time.Minute).Unix(), clock.Add(-30 * time.Minute).Unix()}})
	if err != nil {
		t.Fatal(err)
	}
	apply(delta, &snapshot, deltaPlan, runID+"-delta")
	assertOrdinary(deltaIdentity)
	devices, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT device_id,device_key_hmac,last_seen_at_unix,revoked FROM devices WHERE customer_id=? ORDER BY device_id`, Args: []any{customerID}})
	if err != nil || len(devices) != 1 || len(devices[0].Rows) != 4 {
		t.Fatal("final delta changed device identity set")
	}
	for _, row := range devices[0].Rows {
		seen, _ := applyRowInt(row["last_seen_at_unix"])
		revoked, _ := applyRowInt(row["revoked"])
		switch row["device_key_hmac"] {
		case originalHMAC:
			if row["device_id"] != originalID || seen != clock.Unix() || revoked != 0 {
				t.Fatal("delta replaced original device or rolled back last-seen")
			}
		case box.LookupHMAC("device-identity", []byte(removedDevice)):
			if revoked != 1 {
				t.Fatal("delta retained a device removed before capture")
			}
		case box.LookupHMAC("device-identity", []byte("synthetic-target-only-device")):
			if seen != clock.Add(-30*time.Minute).Unix() || revoked != 0 {
				t.Fatal("delta revoked a target-only device absent from legacy parent")
			}
		default:
			if seen != clock.Unix() || revoked != 0 {
				t.Fatal("delta revoked a concurrent device claim")
			}
		}
	}
	retained, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT desired_envelope FROM desired_node_state WHERE customer_id=? AND service_name='maestro-core'`, Args: []any{customerID}})
	if err != nil || len(retained) != 1 || len(retained[0].Rows) == 0 {
		t.Fatal("latest protected identity missing")
	}
	for _, row := range retained[0].Rows {
		var secret LegacyEncryptedSecret
		encoded, ok := row["desired_envelope"].(string)
		if !ok || json.Unmarshal([]byte(encoded), &secret) != nil || secret != delta.EncryptedSecrets[0] {
			t.Fatal("mutable identity did not retain final delta")
		}
		preserved, err := openProductionIdentity(box, delta.Customers[0].SourceKey, secret)
		if err != nil || preserved.Generation != deltaIdentity.Generation || !preserved.Customer.Expires.Equal(deltaIdentity.Customer.Expires) || !reflect.DeepEqual(preserved.Customer.Devices, deltaIdentity.Customer.Devices) {
			t.Fatal("retained protected identity lost original fields")
		}
	}
	// A concurrent mutation can reach the very same revision the importer wants.
	// The CAS must compare with the authenticated parent, not merely reject >.
	stale, staleIdentity := productionDeltaFixture(t, delta, deltaIdentity, box)
	staleOptions := testPlanOptions()
	staleOptions.ParentSnapshot = &delta
	staleOptions.AppliedParentDigest = deltaPlan.SourceDigest
	stalePlan, staleReport := Plan(stale, staleOptions)
	if len(staleReport.Blockers) != 0 {
		t.Fatal("stale CAS fixture plan blocked")
	}
	staleStore, staleBatch := productionReaderBatch(t, db, clock, stale, &delta, stalePlan, runID+"-stale", box)
	if _, err := staleStore.BeginOrResume(ctx, ApplyRun{RunID: runID + "-stale", SnapshotKind: "delta", ParentDigest: deltaPlan.SourceDigest, SourceDigest: stalePlan.SourceDigest, PlanDigest: stalePlan.PlanDigest, BatchCount: 1}); err != nil {
		t.Fatal(err)
	}
	winner := deltaIdentity
	winner.Generation = staleIdentity.Generation
	winner.Customer.Expires = clock.Add(4 * time.Hour)
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE customers SET generation=?,expires_at_unix=? WHERE customer_id=?`, Args: []any{winner.Generation, winner.Customer.Expires.Unix(), customerID}}); err != nil {
		t.Fatal(err)
	}
	before := productionReaderDurableRows(t, ctx, db, customerID)
	if _, err := staleStore.CommitBatch(ctx, staleBatch); err == nil {
		t.Fatal("equal-next-revision conflict overwrote concurrent target state")
	}
	after := productionReaderDurableRows(t, ctx, db, customerID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed final-delta CAS partially changed customer, access, devices or provenance")
	}
	assertOrdinary(winner)
}

func productionDeltaFixture(t *testing.T, parent Snapshot, identity ProductionCustomerIdentity, box *controlplane.SecretBox) (Snapshot, ProductionCustomerIdentity) {
	t.Helper()
	delta := parent
	delta.Customers = append([]LegacyCustomer(nil), parent.Customers...)
	delta.EncryptedSecrets = nil
	delta.SnapshotKind = "delta"
	delta.ParentSourceDigest = digestSnapshot(parent)
	delta.CapturedAt = parent.CapturedAt.Add(time.Second)
	identity.Generation++
	identity.Customer.Expires = identity.Customer.Expires.Add(time.Hour)
	setProductionFixtureIdentity(t, &delta, identity, box)
	return delta, identity
}

func productionReaderBatch(t *testing.T, db rqlite.RQLite, clock productionReaderClock, source Snapshot, parent *Snapshot, plan ImportPlan, runID string, box *controlplane.SecretBox) (*RQLiteApplyStore, ApplyBatch) {
	t.Helper()
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(source, parent), box)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProductionRQLiteApplyStore(db, clock.Now, protection, nil)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := planOperations(plan)
	if err != nil || len(operations) == 0 {
		t.Fatal("production reader operations unavailable")
	}
	return store, ApplyBatch{RunID: runID, PlanDigest: plan.PlanDigest, Index: 0, Digest: digestBatch(operations), Operations: operations}
}

func productionReaderDurableRows(t *testing.T, ctx context.Context, db rqlite.RQLite, customerID string) []rqlite.Result {
	t.Helper()
	rows, err := db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT * FROM customers WHERE customer_id=?`, Args: []any{customerID}},
		rqlite.Statement{SQL: `SELECT * FROM subscription_tokens WHERE customer_id=? ORDER BY token_id`, Args: []any{customerID}},
		rqlite.Statement{SQL: `SELECT * FROM credentials WHERE customer_id=? ORDER BY credential_id`, Args: []any{customerID}},
		rqlite.Statement{SQL: `SELECT * FROM devices WHERE customer_id=? ORDER BY device_id`, Args: []any{customerID}},
		rqlite.Statement{SQL: `SELECT * FROM desired_node_state WHERE customer_id=? ORDER BY node_id`, Args: []any{customerID}},
		rqlite.Statement{SQL: `SELECT * FROM imported_entity_state ORDER BY entity_kind,source_key`},
		rqlite.Statement{SQL: `SELECT * FROM import_batches ORDER BY import_run_id,batch_index`})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
