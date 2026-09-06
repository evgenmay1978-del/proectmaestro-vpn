//go:build rqlite_integration

package importer

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestNativeRuntimeFullApplyAuthenticatedReadersAndShadow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("real SQLite fixture requires Python")
	}
	db := &productionDomainSQLite{python: python, path: filepath.Join(t.TempDir(), "native-runtime.sqlite")}
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fixture := newRuntimeDomainFixture(t, now)
	snapshot := runtimeDomainNormalizedSnapshot(t, fixture)
	protection, err := ValidateProductionCustomerIdentities(ProtectionFromSnapshot(snapshot), fixture.box)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewProductionRQLiteApplyStore(db, func() time.Time { return now }, protection, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, report := Plan(snapshot, fixture.normalizeOptions.PlanOptions)
	if len(report.Blockers) != 0 {
		t.Fatal("native runtime plan blocked")
	}
	options := ApplyOptions{RunID: "native-runtime-real-boundary", BatchSize: 2}
	applied, err := Apply(ctx, store, plan, options)
	if err != nil {
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
	for _, key := range []string{"olcrtc", "vkturn"} {
		value, err := service.ReadLegacyRuntimeSetting(ctx, key)
		if err != nil || value.Generation() != 1 || len(value.Members()) != 3 {
			t.Fatal("real reader lost source members")
		}
	}
	if absent, err := service.ReadLegacyOTAAbsent(ctx); err != nil || !absent {
		t.Fatal("authenticated absent OTA was not preserved")
	}
	session, err := service.AuthenticatePassword(ctx, "synthetic-existing-file-password")
	if err != nil {
		t.Fatal("original panel password cannot authenticate")
	}
	if _, err := service.Authorize(ctx, session.Cookie.Value, session.CSRFToken, controlplane.PermissionCriticalSettings); err != nil {
		t.Fatal("original panel owner authority lost")
	}
	legacy, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := ShadowFromCandidate(ctx, store, plan.SourceDigest, validShadowShapes())
	if err != nil || !reflect.DeepEqual(legacy, candidate) {
		t.Fatal("full runtime shadow differs from authenticated source")
	}
	replay, err := Apply(ctx, store, plan, options)
	if err != nil || replay.TargetDigest != applied.TargetDigest {
		t.Fatal("exact retry changed native runtime target")
	}
	// Model an already committed setting winner before a still-pending import
	// operation. The actual CommitBatch transaction must keep every dependent
	// row and its receipt unchanged when its generation loses the CAS.
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE cluster_settings SET generation=2,public_value_json='{"winner":true}' WHERE setting_key='olcrtc'`}, rqlite.Statement{SQL: `UPDATE setting_members SET generation=2 WHERE setting_key='olcrtc'`}); err != nil {
		t.Fatal(err)
	}
	operations, err := planOperations(plan)
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
		t.Fatal("canonical OLC operation missing")
	}
	run := ApplyRun{RunID: "native-runtime-cas-loser", SnapshotKind: "full", SourceDigest: plan.SourceDigest, PlanDigest: strings.Repeat("f", 64), BatchCount: 1}
	if _, err := store.BeginOrResume(ctx, run); err != nil {
		t.Fatal(err)
	}
	batch := ApplyBatch{RunID: run.RunID, PlanDigest: run.PlanDigest, Index: 0, Operations: selected, Digest: digestBatch(selected)}
	reads := []rqlite.Statement{{SQL: `SELECT * FROM cluster_settings ORDER BY setting_key`}, {SQL: `SELECT * FROM setting_secrets ORDER BY setting_key`}, {SQL: `SELECT * FROM setting_members ORDER BY setting_key,member_key`}, {SQL: `SELECT * FROM imported_secrets ORDER BY secret_id`}, {SQL: `SELECT * FROM imported_entity_state ORDER BY entity_kind,source_key`}, {SQL: `SELECT * FROM import_batches WHERE import_run_id=?`, Args: []any{run.RunID}}}
	before, err := db.QueryLinearizable(ctx, reads...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitBatch(ctx, batch); err == nil {
		t.Fatal("stale native setting import overwrote committed winner")
	}
	after, err := db.QueryLinearizable(ctx, reads...)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed native setting CAS changed dependent rows or accepted a batch receipt")
	}
	// An actual persisted member omission must not degrade to the conventional
	// setting projection, even when transport data remains decryptable.
	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `DELETE FROM setting_members WHERE setting_key='olcrtc' AND member_key=(SELECT member_key FROM setting_members WHERE setting_key='olcrtc' LIMIT 1)`}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc"); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatal("tampered member set escaped authenticated reader")
	}
	changed, err := ShadowFromCandidate(ctx, store, plan.SourceDigest, validShadowShapes())
	if err == nil && reflect.DeepEqual(legacy, changed) {
		t.Fatal("shadow omitted persisted membership loss")
	}
}
