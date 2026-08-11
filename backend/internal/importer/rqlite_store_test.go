package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type recordedApplyRequest struct {
	level       rqlite.Consistency
	transaction bool
	statements  []rqlite.Statement
}

type applyStoreRQLite struct {
	queryResponses [][]rqlite.Result
	queryHandler   func([]rqlite.Statement) ([]rqlite.Result, error)
	queryErrors    []error
	queryCalls     int
	requestError   error
	queries        [][]rqlite.Statement
	requests       []recordedApplyRequest
}

func (db *applyStoreRQLite) Request(
	_ context.Context,
	level rqlite.Consistency,
	transaction bool,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	copyStatements := append([]rqlite.Statement(nil), statements...)
	db.requests = append(db.requests, recordedApplyRequest{
		level: level, transaction: transaction, statements: copyStatements,
	})
	if db.requestError != nil {
		return nil, db.requestError
	}
	results := make([]rqlite.Result, len(statements))
	for index := range results {
		results[index].RowsAffected = 1
	}
	return results, nil
}

func (db *applyStoreRQLite) QueryLinearizable(
	_ context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	copyStatements := append([]rqlite.Statement(nil), statements...)
	db.queries = append(db.queries, copyStatements)
	index := db.queryCalls
	db.queryCalls++
	if db.queryHandler != nil {
		return db.queryHandler(copyStatements)
	}
	if index < len(db.queryErrors) && db.queryErrors[index] != nil {
		return nil, db.queryErrors[index]
	}
	if index >= len(db.queryResponses) {
		return []rqlite.Result{{}}, nil
	}
	return db.queryResponses[index], nil
}

func (db *applyStoreRQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.QueryLinearizable(ctx, statements...)
}

func (*applyStoreRQLite) Backup(context.Context, io.Writer) error { return nil }

func canonicalCustomerOrderBatch(t *testing.T) ApplyBatch {
	t.Helper()
	plan, report := Plan(decodeFixture(t, "orders-pending-credited.json"), testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	selected := make([]ApplyOperation, 0, 2)
	for _, operation := range operations {
		if operation.Entity == "customer" ||
			(operation.Entity == "order" && operation.Key == "legacy-order-credited") {
			selected = append(selected, operation)
		}
	}
	if len(selected) != 2 {
		t.Fatalf("selected operations = %#v", selected)
	}
	return ApplyBatch{
		RunID: "import-run-1", PlanDigest: plan.PlanDigest, Index: 0,
		Digest: digestBatch(selected), Operations: selected,
	}
}

func receiptQueryResult(batch ApplyBatch) []rqlite.Result {
	return []rqlite.Result{{Rows: []map[string]any{{
		"batch_index": int64(batch.Index), "batch_digest": batch.Digest, "status": "applied",
	}}}}
}

func TestRQLiteApplyStoreCommitsCanonicalRowsAndReceiptAtomically(t *testing.T) {
	batch := canonicalCustomerOrderBatch(t)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	receipt, err := store.CommitBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("CommitBatch: %v", err)
	}
	if receipt.Index != batch.Index || receipt.Digest != batch.Digest || receipt.AlreadyApplied {
		t.Fatalf("receipt = %#v", receipt)
	}
	if len(db.requests) != 1 {
		t.Fatalf("write request count = %d, want 1", len(db.requests))
	}
	request := db.requests[0]
	if request.level != rqlite.Linearizable || !request.transaction {
		t.Fatalf("write request is not one linearizable transaction: %#v", request)
	}
	var sqlText strings.Builder
	for _, statement := range request.statements {
		sqlText.WriteString(statement.SQL)
		sqlText.WriteByte('\n')
	}
	gotSQL := strings.ToLower(sqlText.String())
	for _, required := range []string{
		"insert into import_batches", "insert into customers", "insert into credentials",
		"insert into subscription_tokens", "insert into orders", "update import_batches",
	} {
		if !strings.Contains(gotSQL, required) {
			t.Fatalf("transaction is missing %q:\n%s", required, gotSQL)
		}
	}
	for _, forbidden := range []string{"canonical_import_rows", "canonical_json"} {
		if strings.Contains(gotSQL, forbidden) {
			t.Fatalf("transaction used generic staging %q:\n%s", forbidden, gotSQL)
		}
	}
}

func TestRQLiteApplyStoreResolvesUnknownOutcomeByReceiptWithoutRetry(t *testing.T) {
	batch := canonicalCustomerOrderBatch(t)
	db := &applyStoreRQLite{
		queryResponses: [][]rqlite.Result{{{Rows: nil}}, receiptQueryResult(batch)},
		requestError: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic disconnect"),
		},
	}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	receipt, err := store.CommitBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("CommitBatch unknown outcome: %v", err)
	}
	if !receipt.AlreadyApplied || receipt.Digest != batch.Digest {
		t.Fatalf("resolved receipt = %#v", receipt)
	}
	if len(db.requests) != 1 {
		t.Fatalf("unknown write was retried %d times", len(db.requests))
	}
}

func TestRQLiteApplyStoreRejectsChangedDigestWithoutWrite(t *testing.T) {
	batch := canonicalCustomerOrderBatch(t)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{
		Rows: []map[string]any{{"batch_index": int64(0), "batch_digest": strings.Repeat("f", 64), "status": "applied"}},
	}}}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	_, err = store.CommitBatch(context.Background(), batch)
	if !errors.Is(err, ErrRunDigestMismatch) {
		t.Fatalf("changed digest error = %v, want %v", err, ErrRunDigestMismatch)
	}
	if len(db.requests) != 0 {
		t.Fatalf("changed digest issued %d writes", len(db.requests))
	}
}

func TestRQLiteApplyStoreBeginResumeBindsRunAndReceipts(t *testing.T) {
	run := ApplyRun{
		RunID: "run-lifecycle", SnapshotKind: "delta",
		SourceDigest: strings.Repeat("a", 64), PlanDigest: strings.Repeat("b", 64),
		ParentDigest: strings.Repeat("c", 64), BatchCount: 2,
	}
	db := &applyStoreRQLite{queryHandler: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) != 2 {
			t.Fatalf("BeginOrResume query statement count = %d, want 2", len(statements))
		}
		return []rqlite.Result{
			{Rows: []map[string]any{{
				"import_run_id": run.RunID, "snapshot_kind": run.SnapshotKind,
				"source_sha256": run.SourceDigest, "plan_sha256": run.PlanDigest,
				"parent_source_sha256": run.ParentDigest, "target_sha256": nil,
				"batch_count": int64(run.BatchCount), "status": "applying",
			}}},
			{Rows: []map[string]any{{
				"batch_index": int64(0), "batch_digest": strings.Repeat("d", 64), "status": "applied",
			}}},
		}, nil
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	progress, err := store.BeginOrResume(context.Background(), run)
	if err != nil {
		t.Fatalf("BeginOrResume: %v", err)
	}
	if !progress.New || progress.Completed || progress.TargetDigest != "" ||
		progress.AppliedBatchDigests[0] != strings.Repeat("d", 64) {
		t.Fatalf("run progress = %#v", progress)
	}
	if len(db.requests) != 1 || !db.requests[0].transaction ||
		!strings.Contains(strings.ToLower(db.requests[0].statements[0].SQL), "insert into import_runs") {
		t.Fatalf("run creation request = %#v", db.requests)
	}
}

func inspectTargetFixture(t *testing.T, expiresAt int64) TargetState {
	t.Helper()
	db := &applyStoreRQLite{queryHandler: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		results := make([]rqlite.Result, len(statements))
		for index, statement := range statements {
			sqlText := strings.ToLower(statement.SQL)
			switch {
			case strings.Contains(sqlText, "from customers"):
				results[index].Rows = []map[string]any{{
					"customer_id": "customer-1", "display_login": "CaseSensitiveUser",
					"login_key_hmac": strings.Repeat("e", 64), "status": "active",
					"expires_at_unix": expiresAt, "generation": int64(7),
					"created_at_unix": int64(100), "updated_at_unix": int64(100),
				}}
			case strings.Contains(sqlText, "from import_runs"):
				results[index].Rows = []map[string]any{{
					"source_sha256": strings.Repeat("f", 64),
				}}
			}
		}
		return results, nil
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	state, err := store.InspectTarget(context.Background())
	if err != nil {
		t.Fatalf("InspectTarget: %v", err)
	}
	if state.Empty || state.BusinessDigest == "" || state.AppliedSourceDigest != strings.Repeat("f", 64) {
		t.Fatalf("target state = %#v", state)
	}
	if len(db.queries) != 1 || len(db.queries[0]) < 5 {
		t.Fatalf("business digest query coverage = %#v", db.queries)
	}
	return state
}

func TestRQLiteApplyStoreRecomputesBusinessDigestFromCanonicalRows(t *testing.T) {
	first := inspectTargetFixture(t, 2_100_000)
	second := inspectTargetFixture(t, 2_100_000)
	changed := inspectTargetFixture(t, 2_100_001)
	if first.BusinessDigest != second.BusinessDigest {
		t.Fatalf("same canonical rows produced unstable digests: %s != %s", first.BusinessDigest, second.BusinessDigest)
	}
	if first.BusinessDigest == changed.BusinessDigest {
		t.Fatal("changed customer expiry did not change business digest")
	}
}

func TestRQLiteApplyStoreCompleteRequiresEveryAppliedReceipt(t *testing.T) {
	completion := ApplyCompletion{
		RunID: "run-complete", SourceDigest: strings.Repeat("a", 64),
		PlanDigest: strings.Repeat("b", 64), TargetDigest: strings.Repeat("c", 64),
	}
	db := &applyStoreRQLite{queryHandler: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) != 1 {
			t.Fatalf("Complete verify statement count = %d", len(statements))
		}
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
	if len(db.requests) != 1 || len(db.requests[0].statements) != 1 {
		t.Fatalf("completion requests = %#v", db.requests)
	}
	statement := db.requests[0].statements[0]
	sqlText := strings.ToLower(statement.SQL)
	if !strings.Contains(sqlText, "count(*)") || !strings.Contains(sqlText, "status='applied'") {
		t.Fatalf("completion is not receipt-gated: %s", statement.SQL)
	}
}

func TestApplyRowIntAcceptsRQLiteIntegerWireString(t *testing.T) {
	got, ok := applyRowInt("42")
	if !ok || got != 42 {
		t.Fatalf("applyRowInt wire integer = %d,%t", got, ok)
	}
	if _, ok := applyRowInt("4.2"); ok {
		t.Fatal("applyRowInt accepted non-integer wire value")
	}
}

func TestRQLiteApplyStoreCommitsBotCredentialRouteAsTypedRow(t *testing.T) {
	plan, report := Plan(decodeFixture(t, "bot-bindings-v1.json"), testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	if len(operations) != 1 || operations[0].Entity != "bot_binding" {
		t.Fatalf("bot route operations = %#v", operations)
	}
	batch := ApplyBatch{
		RunID: "import-run-bot-route", PlanDigest: plan.PlanDigest, Index: 0,
		Digest: digestBatch(operations), Operations: operations,
	}
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch bot route: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("bot route request count = %d", len(db.requests))
	}
	found := false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if strings.Contains(sqlText, "canonical_import_rows") || strings.Contains(sqlText, "canonical_json") {
			t.Fatalf("bot route used generic staging: %s", statement.SQL)
		}
		if !strings.Contains(sqlText, "insert into telegram_bot_routes") {
			continue
		}
		found = true
		wantArgs := []any{
			strings.Repeat("1", 64), strings.Repeat("2", 64), 1, "bot-schema-v1", int64(1_500_000),
			batch.RunID, batch.Index, batch.Digest,
		}
		if fmt.Sprint(statement.Args) != fmt.Sprint(wantArgs) {
			t.Fatalf("bot route args = %v, want %v", statement.Args, wantArgs)
		}
	}
	if !found {
		t.Fatal("transaction has no typed telegram_bot_routes write")
	}
}
