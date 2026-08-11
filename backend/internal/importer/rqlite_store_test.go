package importer

import (
	"context"
	"errors"
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
	queryErrors    []error
	queryCalls     int
	requestError   error
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
	_ ...rqlite.Statement,
) ([]rqlite.Result, error) {
	index := db.queryCalls
	db.queryCalls++
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
