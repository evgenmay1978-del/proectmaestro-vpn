package importer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestTask6CommitBatchBumpsAfterNewAppliedReceipt(t *testing.T) {
	batch := canonicalCustomerOrderBatch(t)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{Rows: nil}}, receiptQueryResult(batch)}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch: %v", err)
	}
	statements := db.requests[0].statements
	if len(statements) < 2 || !strings.Contains(strings.ToLower(statements[len(statements)-2].SQL), "update import_batches") ||
		!strings.Contains(strings.ToLower(statements[len(statements)-1].SQL), "update backup_rpo_state") {
		t.Fatalf("batch dirty ordering=%#v", statements)
	}
}

func TestTask6CompleteBumpsOnlyNewlyAppliedRun(t *testing.T) {
	completion := ApplyCompletion{
		RunID: "task6-run-complete", SourceDigest: strings.Repeat("a", 64),
		PlanDigest: strings.Repeat("b", 64), TargetDigest: strings.Repeat("c", 64),
	}
	db := &applyStoreRQLite{queryHandler: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		return []rqlite.Result{{Rows: []map[string]any{{
			"source_sha256": completion.SourceDigest, "plan_sha256": completion.PlanDigest,
			"target_sha256": completion.TargetDigest, "status": "applied",
		}}}}, nil
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if err := store.Complete(context.Background(), completion); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	statements := db.requests[0].statements
	if len(statements) != 2 || !strings.Contains(strings.ToLower(statements[0].SQL), "update import_runs") ||
		!strings.Contains(strings.ToLower(statements[1].SQL), "update backup_rpo_state") {
		t.Fatalf("completion dirty ordering=%#v", statements)
	}
}

func TestTask6BeginOrResumeRemainsBackupNeutral(t *testing.T) {
	run := ApplyRun{
		RunID: "task6-begin", SnapshotKind: "full", SourceDigest: strings.Repeat("d", 64),
		PlanDigest: strings.Repeat("e", 64), BatchCount: 0,
	}
	db := &applyStoreRQLite{queryHandler: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		return []rqlite.Result{
			{Rows: []map[string]any{{
				"import_run_id": run.RunID, "snapshot_kind": run.SnapshotKind,
				"source_sha256": run.SourceDigest, "plan_sha256": run.PlanDigest,
				"parent_source_sha256": nil, "target_sha256": nil,
				"batch_count": int64(0), "status": "applying",
			}}},
			{Rows: nil},
		}, nil
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.BeginOrResume(context.Background(), run); err != nil {
		t.Fatalf("BeginOrResume: %v", err)
	}
	if sql := strings.ToLower(db.requests[0].statements[0].SQL); strings.Contains(sql, "backup_rpo_state") {
		t.Fatalf("BeginOrResume dirtied backup state: %s", sql)
	}
}
