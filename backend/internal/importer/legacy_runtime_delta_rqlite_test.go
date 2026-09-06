//go:build rqlite_integration

package importer

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestNativeRuntimeDeltaPreservesCommittedTargetAndPasswordSQLite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("real SQLite requires Python")
	}
	db := &productionDomainSQLite{python: python, path: filepath.Join(t.TempDir(), "runtime-delta.sqlite")}
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fixture := newRuntimeDomainFixture(t, now)
	parent := runtimeDomainNormalizedSnapshot(t, fixture)
	proof, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(parent), fixture.box)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProductionRQLiteApplyStore(db, func() time.Time { return now }, proof, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, report := Plan(parent, fixture.normalizeOptions.PlanOptions)
	if len(report.Blockers) != 0 {
		t.Fatal("full plan")
	}
	if _, err := Apply(ctx, store, plan, ApplyOptions{RunID: "runtime-delta-parent", BatchSize: 2}); err != nil {
		t.Fatal(err)
	}
	clock := productionDomainReaderClock{now: now}
	reader, err := controlplane.NewStore(db, fixture.box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(reader, &productionDomainReaderIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	principalID := deterministicID("maestro-legacy-v1", "principal", parent.Principals[0].SourceKey)
	if err := service.ChangePrincipalPassword(ctx, principalID, "synthetic-existing-file-password", "synthetic-changed-target-password", "runtime-delta-password"); err != nil {
		t.Fatal("actual password mutation failed")
	}
	// An authenticated setting may have a newer committed generation even if
	// its content is unchanged. The delta must neither reject nor reset it.
	if _, err := db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE cluster_settings SET generation=2 WHERE setting_key='olcrtc'`},
		rqlite.Statement{SQL: `UPDATE setting_members SET generation=2 WHERE setting_key='olcrtc'`}); err != nil {
		t.Fatal(err)
	}
	reads := []rqlite.Statement{{SQL: `SELECT * FROM cluster_settings ORDER BY setting_key`}, {SQL: `SELECT * FROM setting_secrets ORDER BY setting_key`}, {SQL: `SELECT * FROM setting_members ORDER BY setting_key,member_key`}, {SQL: `SELECT * FROM principals ORDER BY principal_id`}, {SQL: `SELECT * FROM principal_roles ORDER BY principal_id,role_name`}, {SQL: `SELECT * FROM principal_credentials ORDER BY credential_id`}}
	before, err := db.QueryLinearizable(ctx, reads...)
	if err != nil {
		t.Fatal(err)
	}
	oldSources, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT secret_id,secret_envelope,secret_sha256 FROM imported_secrets WHERE secret_id LIKE 'runtime-%' OR secret_id LIKE 'legacy-runtime-%' ORDER BY secret_id`})
	if err != nil {
		t.Fatal(err)
	}
	raw, capture, options := runtimeDeltaFixture(t, fixture, parent, true)
	delta := normalizeFixture(t, raw, capture, options, fixture.box)
	deltaProof, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(delta, &parent), fixture.box)
	if err != nil {
		t.Fatal(err)
	}
	deltaStore, err := NewProductionRQLiteApplyStore(db, func() time.Time { return options.Now }, deltaProof, nil)
	if err != nil {
		t.Fatal(err)
	}
	planOptions := options.PlanOptions
	planOptions.ParentSnapshot, planOptions.AppliedParentDigest = &parent, delta.ParentSourceDigest
	deltaPlan, deltaReport := Plan(delta, planOptions)
	if len(deltaReport.Blockers) != 0 {
		t.Fatal("delta plan")
	}
	applyOptions := ApplyOptions{RunID: "runtime-delta-final", BatchSize: 2}
	result, err := Apply(ctx, deltaStore, deltaPlan, applyOptions)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := Apply(ctx, deltaStore, deltaPlan, applyOptions)
	if err != nil || replay.TargetDigest != result.TargetDigest {
		t.Fatal("exact delta retry changed its committed target")
	}
	after, err := db.QueryLinearizable(ctx, reads...)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("delta changed a committed setting/member/password/role")
	}
	for _, row := range oldSources[0].Rows {
		actual, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT secret_id,secret_envelope,secret_sha256 FROM imported_secrets WHERE secret_id=?`, Args: []any{row["secret_id"]}})
		if err != nil || len(actual) != 1 || len(actual[0].Rows) != 1 || !reflect.DeepEqual(row, actual[0].Rows[0]) {
			t.Fatal("old source archive was rewritten")
		}
	}
	if _, err := service.AuthenticatePassword(ctx, "synthetic-changed-target-password"); err != nil {
		t.Fatal("delta restored old source password")
	}
	for _, key := range []string{"olcrtc", "vkturn"} {
		value, err := service.ReadLegacyRuntimeSetting(ctx, key)
		if err != nil || len(value.Members()) != 3 {
			t.Fatal("customer expiry changed stable runtime membership")
		}
		if key == "olcrtc" && value.Generation() != 2 {
			t.Fatal("delta reset target generation")
		}
	}
	if result.TargetDigest == "" {
		t.Fatal("delta did not finish")
	}
	// A pending native operation cannot bless a missing initial source marker.
	operations, err := planOperations(deltaPlan)
	if err != nil {
		t.Fatal(err)
	}
	var selected []ApplyOperation
	for _, operation := range operations {
		if operation.Entity == "setting" && operation.Key == "olcrtc" {
			selected = append(selected, operation)
		}
	}
	if len(selected) != 1 {
		t.Fatal("setting operation missing")
	}
	batch := ApplyBatch{RunID: "runtime-delta-target-missing", Index: 0, PlanDigest: strings.Repeat("f", 64), Operations: selected, Digest: digestBatch(selected)}
	if _, err := deltaStore.BeginOrResume(ctx, ApplyRun{RunID: batch.RunID, SnapshotKind: "delta", SourceDigest: deltaPlan.SourceDigest, PlanDigest: batch.PlanDigest, ParentDigest: deltaPlan.ParentSourceDigest, BatchCount: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE imported_entity_state SET lifecycle='deleted' WHERE entity_kind='encrypted_secret' AND source_key=?`, Args: []any{parent.Settings[0].SecretRef}}); err != nil {
		t.Fatal(err)
	}
	before, err = db.QueryLinearizable(ctx, reads...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deltaStore.CommitBatch(ctx, batch); err == nil {
		t.Fatal("missing source proof accepted")
	}
	after, err = db.QueryLinearizable(ctx, reads...)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed preservation assertion mutated target")
	}
	accepted, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT COUNT(*) AS count FROM import_batches WHERE import_run_id=?`, Args: []any{batch.RunID}})
	if err != nil || len(accepted) != 1 || len(accepted[0].Rows) != 1 {
		t.Fatal("batch receipt query")
	}
	if count, ok := applyRowInt(accepted[0].Rows[0]["count"]); !ok || count != 0 {
		t.Fatal("failed preservation assertion accepted a batch")
	}
}
