package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
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

func customerOnlyBatch(t *testing.T, fixtureName, sourceKey string) (ApplyBatch, customerApplyPayload) {
	t.Helper()
	plan, report := Plan(decodeFixture(t, fixtureName), testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var selected ApplyOperation
	for _, operation := range operations {
		if operation.Entity == "customer" && operation.Key == sourceKey {
			selected = operation
			break
		}
	}
	if selected.Entity == "" {
		t.Fatalf("customer operation %q not found", sourceKey)
	}
	var payload customerApplyPayload
	if err := decodeCanonicalOperation(selected.CanonicalJSON, &payload); err != nil {
		t.Fatalf("decode customer operation: %v", err)
	}
	batch := ApplyBatch{RunID: "import-topology-run", PlanDigest: plan.PlanDigest, Index: 0, Operations: []ApplyOperation{selected}}
	batch.Digest = digestBatch(batch.Operations)
	return batch, payload
}

func canonicalDeleteBatch(t *testing.T, entity string) (ApplyBatch, PlannedDelete) {
	t.Helper()
	base := decodeFixture(t, "full-then-delta/base-full.json")
	basePlan := plannedFixtureFromSnapshot(t, base, testPlanOptions())
	delta := preparedDelta(t, base, basePlan)
	options := testPlanOptions()
	options.ParentSnapshot = &base
	options.AppliedParentDigest = basePlan.SourceDigest
	plan, report := Plan(delta, options)
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected delete blockers: %#v", report.Blockers)
	}
	deletions := append(append([]PlannedDelete(nil), plan.Deletes...), plan.CascadeDeletes...)
	var want PlannedDelete
	for _, deletion := range deletions {
		if deletion.Entity == entity {
			want = deletion
			break
		}
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	selected := make([]ApplyOperation, 0, 1)
	for _, operation := range operations {
		if operation.Tombstone && operation.Entity == entity {
			selected = append(selected, operation)
		}
	}
	if want.Entity == "" || len(selected) != 1 {
		t.Fatalf("typed %s delete = %#v / %#v", entity, want, selected)
	}
	batch := ApplyBatch{RunID: "import-delete-" + entity, PlanDigest: plan.PlanDigest, Index: 0, Operations: selected}
	batch.Digest = digestBatch(batch.Operations)
	return batch, want
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

func requireTopologyBatchGate(t *testing.T, statement rqlite.Statement, batch ApplyBatch) {
	t.Helper()
	if !strings.Contains(statement.SQL, batchWriteGate) {
		t.Fatalf("topology statement is not batch-gated: %s", statement.SQL)
	}
	if len(statement.Args) < 3 {
		t.Fatalf("topology statement gate args are missing: %#v", statement)
	}
	got := statement.Args[len(statement.Args)-3:]
	want := batchGateArgs(batch)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("topology gate args = %v, want %v", got, want)
	}
}

func TestRQLiteApplyStoreWritesEveryExplicitCustomerTopologyTupleAtomically(t *testing.T) {
	batch, payload := customerOnlyBatch(t, "orders-pending-credited.json", "s1:customer:order-owner")
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{Rows: nil}}, receiptQueryResult(batch)}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch topology: %v", err)
	}
	if len(db.requests) != 1 || !db.requests[0].transaction || db.requests[0].level != rqlite.Linearizable {
		t.Fatalf("topology write was not one linearizable transaction: %#v", db.requests)
	}

	wantNodes := make(map[string]struct{}, len(payload.Customer.NodeIDs))
	wantTuples := make(map[string]struct{}, len(payload.Customer.NodeIDs)*len(payload.Customer.ProtocolTags))
	for _, nodeID := range payload.Customer.NodeIDs {
		wantNodes[nodeID] = struct{}{}
		for _, protocolTag := range payload.Customer.ProtocolTags {
			wantTuples[nodeID+"\x00"+protocolTag] = struct{}{}
		}
	}
	protocolDeletes := 0
	nodeDeletes := 0
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		switch {
		case strings.Contains(sqlText, "delete from desired_protocol_tags"):
			requireTopologyBatchGate(t, statement, batch)
			protocolDeletes++
		case strings.Contains(sqlText, "delete from desired_node_state"):
			requireTopologyBatchGate(t, statement, batch)
			nodeDeletes++
		case strings.Contains(sqlText, "insert into desired_node_state"):
			requireTopologyBatchGate(t, statement, batch)
			if len(statement.Args) < 8 || statement.Args[0] != payload.Customer.InternalID || statement.Args[2] != "maestro-core" ||
				statement.Args[3] != payload.Customer.Generation || statement.Args[5] != payload.IdentitySecret.SHA256 || statement.Args[6] != "pending" {
				t.Fatalf("desired node args = %#v", statement.Args)
			}
			nodeID, _ := statement.Args[1].(string)
			if _, exists := wantNodes[nodeID]; !exists {
				t.Fatalf("unexpected desired node %q", nodeID)
			}
			delete(wantNodes, nodeID)
		case strings.Contains(sqlText, "insert into desired_protocol_tags"):
			requireTopologyBatchGate(t, statement, batch)
			if len(statement.Args) < 4 || statement.Args[0] != payload.Customer.InternalID || statement.Args[2] != "maestro-core" {
				t.Fatalf("desired protocol args = %#v", statement.Args)
			}
			nodeID, _ := statement.Args[1].(string)
			protocolTag, _ := statement.Args[3].(string)
			key := nodeID + "\x00" + protocolTag
			if _, exists := wantTuples[key]; !exists {
				t.Fatalf("unexpected desired protocol tuple %q", key)
			}
			delete(wantTuples, key)
		}
	}
	if protocolDeletes != 1 || nodeDeletes != 1 || len(wantNodes) != 0 || len(wantTuples) != 0 {
		t.Fatalf("topology coverage deletes=%d/%d missing nodes=%v tuples=%v", protocolDeletes, nodeDeletes, wantNodes, wantTuples)
	}
}

func TestRQLiteApplyStoreNarrowsCustomerTopologyAsExactSet(t *testing.T) {
	batch, payload := customerOnlyBatch(t, "full-then-delta/final-full.json", "customer-alpha")
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{Rows: nil}}, receiptQueryResult(batch)}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch narrowed topology: %v", err)
	}
	wantProtocolArgs := []any{payload.Customer.InternalID, "maestro-core", "vless"}
	wantNodeArgs := []any{payload.Customer.InternalID, "maestro-core", "S2", "S3", "S4"}
	protocolDelete := false
	nodeDelete := false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if !strings.Contains(sqlText, "delete from desired_protocol_tags") && !strings.Contains(sqlText, "delete from desired_node_state") {
			continue
		}
		requireTopologyBatchGate(t, statement, batch)
		if !strings.Contains(sqlText, "not in") {
			t.Fatalf("topology delete is not exact-set narrowing: %s", statement.SQL)
		}
		setArgs := statement.Args[:len(statement.Args)-3]
		if strings.Contains(sqlText, "desired_protocol_tags") {
			protocolDelete = true
			if fmt.Sprint(setArgs) != fmt.Sprint(wantProtocolArgs) {
				t.Fatalf("protocol narrowing args = %v, want %v", setArgs, wantProtocolArgs)
			}
		} else {
			nodeDelete = true
			if fmt.Sprint(setArgs) != fmt.Sprint(wantNodeArgs) {
				t.Fatalf("node narrowing args = %v, want %v", setArgs, wantNodeArgs)
			}
		}
	}
	if !protocolDelete || !nodeDelete {
		t.Fatalf("exact-set topology deletes missing: protocol=%v node=%v", protocolDelete, nodeDelete)
	}
}

func TestRQLiteApplyStoreWritesActiveCustomerAndConsumedSecretRegistry(t *testing.T) {
	batch := canonicalCustomerOrderBatch(t)
	var payload customerApplyPayload
	for _, operation := range batch.Operations {
		if operation.Entity == "customer" {
			if err := decodeCanonicalOperation(operation.CanonicalJSON, &payload); err != nil {
				t.Fatalf("decode customer operation: %v", err)
			}
		}
	}
	if payload.Customer.InternalID == "" || payload.IdentitySecret.SecretID == "" {
		t.Fatalf("customer payload = %#v", payload)
	}
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}}, receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch customer registry: %v", err)
	}
	want := map[string][]any{
		"customer": {
			"customer", payload.Customer.SourceKey, payload.Customer.InternalID,
			plannedCustomerSourceDigest(payload.Customer), "active", int64(1_500_000),
			batch.RunID, batch.Index, batch.Digest,
		},
		"encrypted_secret": {
			"encrypted_secret", payload.IdentitySecret.SecretID, payload.IdentitySecret.SecretID,
			canonicalLegacyDigest(payload.IdentitySecret), "active", int64(1_500_000),
			batch.RunID, batch.Index, batch.Digest,
		},
	}
	for _, statement := range db.requests[0].statements {
		if !strings.Contains(strings.ToLower(statement.SQL), "insert into imported_entity_state") || len(statement.Args) == 0 {
			continue
		}
		kind, _ := statement.Args[0].(string)
		expected, exists := want[kind]
		if !exists {
			continue
		}
		if fmt.Sprint(statement.Args) != fmt.Sprint(expected) {
			t.Fatalf("%s registry args = %v, want %v", kind, statement.Args, expected)
		}
		delete(want, kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing active registry writes = %#v", want)
	}
}

func TestRQLiteApplyStoreBuildsAtomicCustomerDeleteTransaction(t *testing.T) {
	batch, deletion := canonicalDeleteBatch(t, "customer")
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{Rows: nil}}, receiptQueryResult(batch)}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch customer delete: %v", err)
	}
	if len(db.requests) != 1 || !db.requests[0].transaction || db.requests[0].level != rqlite.Linearizable {
		t.Fatalf("customer delete request = %#v", db.requests)
	}
	fragments := []string{
		"insert into import_batches", "update imported_entity_state", "update customers set",
		"update credentials set", "update subscription_tokens set", "insert into tombstones",
		"insert into tombstone_targets", "insert into import_delete_receipts", "update import_batches",
	}
	next := 0
	for _, statement := range db.requests[0].statements {
		if next < len(fragments) && strings.Contains(strings.ToLower(statement.SQL), fragments[next]) {
			next++
		}
	}
	if next != len(fragments) {
		t.Fatalf("customer delete transaction stopped before %q: %#v", fragments[next], db.requests[0].statements)
	}
	wantIdentity := []string{deletion.SourceKey, deletion.TargetID, deletion.ExpectedPriorDigest, deletion.TombstoneID}
	joined := fmt.Sprint(db.requests[0].statements)
	for _, value := range wantIdentity {
		if value == "" || !strings.Contains(joined, value) {
			t.Fatalf("customer delete transaction omitted proof value %q: %#v", value, deletion)
		}
	}
}

func TestRQLiteApplyStoreBuildsLogicalEncryptedSecretDelete(t *testing.T) {
	batch, deletion := canonicalDeleteBatch(t, "encrypted_secret")
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{Rows: nil}}, receiptQueryResult(batch)}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch encrypted-secret delete: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("encrypted-secret delete requests = %#v", db.requests)
	}
	gotSQL := strings.ToLower(fmt.Sprint(db.requests[0].statements))
	for _, required := range []string{"update imported_entity_state", "insert into import_delete_receipts"} {
		if !strings.Contains(gotSQL, required) {
			t.Fatalf("encrypted-secret delete omitted %q: %s", required, gotSQL)
		}
	}
	if strings.Contains(gotSQL, "delete from imported_secrets") || deletion.TombstoneID != "" {
		t.Fatalf("encrypted-secret delete removed protected material: %#v / %s", deletion, gotSQL)
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

func TestBusinessDigestUsesLogicalImportedEntityProjection(t *testing.T) {
	queries := make(map[string]string, len(businessDigestQueries))
	for _, query := range businessDigestQueries {
		queries[query.name] = strings.ToLower(query.sql)
	}
	if _, exists := queries["tombstones"]; exists {
		t.Fatal("operational tombstones remained in canonical business digest")
	}
	wantFragments := map[string][]string{
		"customers":             {"from customers c", "not exists", "imported_entity_state", "entity_kind='customer'", "target_id=c.customer_id", "lifecycle='deleted'"},
		"credentials":           {"from credentials c", "not exists", "imported_entity_state", "entity_kind='customer'", "target_id=c.customer_id", "lifecycle='deleted'"},
		"subscription_tokens":   {"from subscription_tokens t", "not exists", "imported_entity_state", "entity_kind='customer'", "target_id=t.customer_id", "lifecycle='deleted'"},
		"desired_node_state":    {"from desired_node_state d", "not exists", "imported_entity_state", "entity_kind='customer'", "target_id=d.customer_id", "lifecycle='deleted'"},
		"desired_protocol_tags": {"from desired_protocol_tags p", "not exists", "imported_entity_state", "entity_kind='customer'", "target_id=p.customer_id", "lifecycle='deleted'"},
		"imported_secrets":      {"from imported_secrets i", "not exists", "imported_entity_state", "entity_kind='encrypted_secret'", "target_id=i.secret_id", "lifecycle='deleted'"},
	}
	for table, fragments := range wantFragments {
		sqlText, exists := queries[table]
		if !exists {
			t.Fatalf("business digest query missing %s", table)
		}
		for _, fragment := range fragments {
			if !strings.Contains(sqlText, fragment) {
				t.Fatalf("%s digest query missing %q: %s", table, fragment, sqlText)
			}
		}
	}

	logical := inspectTargetFixture(t, 2_100_000)
	fresh := inspectTargetFixture(t, 2_100_000)
	if logical.BusinessDigest != fresh.BusinessDigest {
		t.Fatalf("logical delete digest=%s fresh digest=%s", logical.BusinessDigest, fresh.BusinessDigest)
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
	if len(db.requests) != 1 || len(db.requests[0].statements) != 2 {
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

func TestRQLiteApplyStoreCommitsBotPollStateAsTypedRow(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	snapshot.BotPollStates = []LegacyBotPollState{{
		BotIdentityHMAC:             snapshot.BotBindings[0].BotIdentityHMAC,
		CurrentTokenFingerprintHMAC: snapshot.BotBindings[0].TokenFingerprintHMAC,
		CredentialVersion:           snapshot.BotBindings[0].CredentialVersion,
		NextUpdateID:                42,
		CapturedFence:               11,
	}}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var pollOperation ApplyOperation
	for _, operation := range operations {
		if operation.Entity == "bot_poll_state" {
			pollOperation = operation
			break
		}
	}
	if pollOperation.Entity == "" {
		t.Fatalf("bot poll state operations = %#v", operations)
	}
	batch := ApplyBatch{
		RunID: "import-run-bot-poll-state", PlanDigest: plan.PlanDigest, Index: 0,
		Operations: []ApplyOperation{pollOperation},
	}
	batch.Digest = digestBatch(batch.Operations)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch bot poll state: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("bot poll state request count = %d", len(db.requests))
	}
	found := false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if strings.Contains(sqlText, "canonical_import_rows") || strings.Contains(sqlText, "canonical_json") {
			t.Fatalf("bot poll state used generic staging: %s", statement.SQL)
		}
		if !strings.Contains(sqlText, "insert into telegram_pollers") {
			continue
		}
		found = true
		if !strings.Contains(sqlText, "telegram_bot_routes") {
			t.Fatalf("bot poll state did not validate credential route: %s", statement.SQL)
		}
		wantArgs := []any{
			snapshot.BotBindings[0].BotIdentityHMAC, snapshot.BotBindings[0].TokenFingerprintHMAC,
			1, snapshot.BotBindings[0].BotIdentityHMAC, int64(42), int64(11), int64(1_500_000),
			batch.RunID, batch.Index, batch.Digest,
		}
		if fmt.Sprint(statement.Args) != fmt.Sprint(wantArgs) {
			t.Fatalf("bot poll state args = %v, want %v", statement.Args, wantArgs)
		}
	}
	if !found {
		t.Fatal("transaction has no typed telegram_pollers write")
	}
}

func TestRQLiteApplyStoreCommitsPendingCallbackAsTypedRow(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	snapshot.PendingCallbacks = []LegacyCallback{{
		BotIdentityHMAC:      snapshot.BotBindings[0].BotIdentityHMAC,
		TokenFingerprintHMAC: snapshot.BotBindings[0].TokenFingerprintHMAC,
		CredentialVersion:    snapshot.BotBindings[0].CredentialVersion,
		CallbackHMAC:         strings.Repeat("4", 64),
		OrderID:              "legacy-order-callback",
		Action:               "confirm",
		State:                "pending",
	}}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var callbackOperation ApplyOperation
	for _, operation := range operations {
		if operation.Entity == "pending_callback" {
			callbackOperation = operation
			break
		}
	}
	if callbackOperation.Entity == "" {
		t.Fatalf("pending callback operations = %#v", operations)
	}
	batch := ApplyBatch{
		RunID: "import-run-pending-callback", PlanDigest: plan.PlanDigest, Index: 0,
		Operations: []ApplyOperation{callbackOperation},
	}
	batch.Digest = digestBatch(batch.Operations)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch pending callback: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("pending callback request count = %d", len(db.requests))
	}
	found := false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if strings.Contains(sqlText, "canonical_import_rows") || strings.Contains(sqlText, "canonical_json") {
			t.Fatalf("pending callback used generic staging: %s", statement.SQL)
		}
		if !strings.Contains(sqlText, "insert into telegram_imported_callbacks") {
			continue
		}
		found = true
		if !strings.Contains(sqlText, "telegram_bot_routes") {
			t.Fatalf("pending callback did not validate credential route: %s", statement.SQL)
		}
		wantArgs := []any{
			snapshot.PendingCallbacks[0].CallbackHMAC,
			snapshot.BotBindings[0].BotIdentityHMAC, snapshot.BotBindings[0].TokenFingerprintHMAC,
			1, snapshot.BotBindings[0].BotIdentityHMAC, "legacy-order-callback", "confirm", "pending",
			int64(1_500_000), batch.RunID, batch.Index, batch.Digest,
		}
		if fmt.Sprint(statement.Args) != fmt.Sprint(wantArgs) {
			t.Fatalf("pending callback args = %v, want %v", statement.Args, wantArgs)
		}
	}
	if !found {
		t.Fatal("transaction has no typed telegram_imported_callbacks write")
	}
}

func TestRQLiteApplyStoreCommitsBotCredentialRotationAsTypedRow(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	oldFingerprint := snapshot.BotBindings[0].TokenFingerprintHMAC
	snapshot.BotBindings[0].TokenFingerprintHMAC = strings.Repeat("3", 64)
	snapshot.BotBindings[0].CredentialVersion = 2
	snapshot.BotCredentialRotations = []LegacyBotCredentialRotation{{
		BotIdentityHMAC:         snapshot.BotBindings[0].BotIdentityHMAC,
		OldTokenFingerprintHMAC: oldFingerprint,
		NewTokenFingerprintHMAC: snapshot.BotBindings[0].TokenFingerprintHMAC,
		OldCredentialVersion:    1,
		NewCredentialVersion:    2,
		AuditDigest:             strings.Repeat("4", 64),
	}}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var rotationOperation ApplyOperation
	for _, operation := range operations {
		if operation.Entity == "bot_credential_rotation" {
			rotationOperation = operation
			break
		}
	}
	if rotationOperation.Entity == "" {
		t.Fatalf("bot rotation operations = %#v", operations)
	}
	batch := ApplyBatch{
		RunID: "import-run-bot-rotation", PlanDigest: plan.PlanDigest, Index: 0,
		Operations: []ApplyOperation{rotationOperation},
	}
	batch.Digest = digestBatch(batch.Operations)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch bot rotation: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("bot rotation request count = %d", len(db.requests))
	}
	found := false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if strings.Contains(sqlText, "canonical_import_rows") || strings.Contains(sqlText, "canonical_json") {
			t.Fatalf("bot rotation used generic staging: %s", statement.SQL)
		}
		if !strings.Contains(sqlText, "insert into telegram_bot_credential_rotations") {
			continue
		}
		found = true
		wantArgs := []any{
			snapshot.BotCredentialRotations[0].AuditDigest,
			snapshot.BotBindings[0].BotIdentityHMAC, oldFingerprint,
			snapshot.BotBindings[0].TokenFingerprintHMAC, 1, 2, int64(1_500_000),
			batch.RunID, batch.Index, batch.Digest,
		}
		if fmt.Sprint(statement.Args) != fmt.Sprint(wantArgs) {
			t.Fatalf("bot rotation args = %v, want %v", statement.Args, wantArgs)
		}
	}
	if !found {
		t.Fatal("transaction has no typed telegram_bot_credential_rotations write")
	}
}

func TestRQLiteApplyStoreCommitsStandaloneEncryptedSecretAsTypedRow(t *testing.T) {
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	secret := standaloneEncryptedSecret()
	snapshot.EncryptedSecrets = []LegacyEncryptedSecret{secret}
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	var secretOperation ApplyOperation
	for _, operation := range operations {
		if operation.Entity == "encrypted_secret" {
			secretOperation = operation
			break
		}
	}
	if secretOperation.Entity == "" {
		t.Fatalf("standalone secret operations = %#v", operations)
	}
	batch := ApplyBatch{
		RunID: "import-run-standalone-secret", PlanDigest: plan.PlanDigest, Index: 0,
		Operations: []ApplyOperation{secretOperation},
	}
	batch.Digest = digestBatch(batch.Operations)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch standalone secret: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("standalone secret request count = %d", len(db.requests))
	}
	foundSecret, foundState := false, false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if strings.Contains(sqlText, "canonical_import_rows") || strings.Contains(sqlText, "canonical_json") {
			t.Fatalf("standalone secret used generic staging: %s", statement.SQL)
		}
		switch {
		case strings.Contains(sqlText, "insert into imported_secrets"):
			foundSecret = true
			wantArgs := []any{
				secret.SecretID, secret.OwnerType, secret.OwnerSourceKey, secret.Field, secret.Kind,
				secret.KeyVersion, string(secretOperation.CanonicalJSON), secret.SHA256, int64(1_500_000),
				batch.RunID, batch.Index, batch.Digest,
			}
			if fmt.Sprint(statement.Args) != fmt.Sprint(wantArgs) {
				t.Fatalf("standalone secret args = %v, want %v", statement.Args, wantArgs)
			}
		case strings.Contains(sqlText, "insert into imported_entity_state"):
			foundState = true
			wantArgs := []any{
				"encrypted_secret", secret.SecretID, secret.SecretID, canonicalLegacyDigest(secret),
				"active", int64(1_500_000), batch.RunID, batch.Index, batch.Digest,
			}
			if fmt.Sprint(statement.Args) != fmt.Sprint(wantArgs) {
				t.Fatalf("standalone secret registry args = %v, want %v", statement.Args, wantArgs)
			}
		}
	}
	if !foundSecret || !foundState {
		t.Fatalf("typed standalone secret writes = secret:%t state:%t", foundSecret, foundState)
	}
}

func TestRQLiteApplyStoreRequiresProtectedTrialSalt(t *testing.T) {
	batch := canonicalTrialBatch(t)
	db := &applyStoreRQLite{}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_500_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err == nil ||
		!strings.Contains(err.Error(), "protected legacy trial salt is required") {
		t.Fatalf("CommitBatch trial without protected salt error = %v", err)
	}
	if len(db.requests) != 0 {
		t.Fatalf("trial without protected salt wrote %d requests", len(db.requests))
	}
}

func TestRQLiteApplyStoreRejectsPlaintextTrialProtection(t *testing.T) {
	db := &applyStoreRQLite{}
	_, err := NewRQLiteApplyStoreWithTrialProtection(
		db,
		func() time.Time { return time.Unix(1_500_000, 0) },
		TrialImportProtection{
			KeyVersion: 1, EncryptedSaltEnvelope: "plaintext-salt",
			SaltSHA256: strings.Repeat("8", 64),
		},
	)
	if err == nil {
		t.Fatal("plaintext legacy trial salt was accepted as an encrypted envelope")
	}
}

func TestRQLiteApplyStoreCommitsTrialWithProtectedSaltAsTypedRows(t *testing.T) {
	batch := canonicalTrialBatch(t)
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{
		{{Rows: nil}},
		receiptQueryResult(batch),
	}}
	protection := protectedTrialImportFixture()
	store, err := NewRQLiteApplyStoreWithTrialProtection(
		db, func() time.Time { return time.Unix(1_500_000, 0) }, protection,
	)
	if err != nil {
		t.Fatalf("NewRQLiteApplyStoreWithTrialProtection: %v", err)
	}
	if _, err := store.CommitBatch(context.Background(), batch); err != nil {
		t.Fatalf("CommitBatch trial: %v", err)
	}
	if len(db.requests) != 1 {
		t.Fatalf("trial request count = %d", len(db.requests))
	}

	wantSaltArgs := []any{
		"legacy-trial-salt-v1", "trial_lookup", "legacy", "salt", "hmac-key",
		1, protection.EncryptedSaltEnvelope, protection.SaltSHA256, int64(1_500_000),
		batch.RunID, batch.Index, batch.Digest,
	}
	trial := legacyTrialFixture()
	wantTrialArgs := []any{
		trial.SourceKey, trial.LegacyAnchorHMAC, trial.CurrentHMAC, 0,
		trial.ExpiresAtUnix, "legacy-trial-salt-v1", int64(1_500_000),
		batch.RunID, batch.Index, batch.Digest,
	}
	foundSalt, foundTrial := false, false
	for _, statement := range db.requests[0].statements {
		sqlText := strings.ToLower(statement.SQL)
		if strings.Contains(sqlText, "canonical_import_rows") || strings.Contains(sqlText, "canonical_json") {
			t.Fatalf("trial used generic staging: %s", statement.SQL)
		}
		switch {
		case strings.Contains(sqlText, "insert into imported_secrets"):
			foundSalt = true
			if fmt.Sprint(statement.Args) != fmt.Sprint(wantSaltArgs) {
				t.Fatalf("protected trial salt args = %v, want %v", statement.Args, wantSaltArgs)
			}
		case strings.Contains(sqlText, "insert into imported_trial_identities"):
			foundTrial = true
			if fmt.Sprint(statement.Args) != fmt.Sprint(wantTrialArgs) {
				t.Fatalf("trial identity args = %v, want %v", statement.Args, wantTrialArgs)
			}
		}
	}
	if !foundSalt || !foundTrial {
		t.Fatalf("typed trial writes = salt:%t trial:%t", foundSalt, foundTrial)
	}
}

func canonicalTrialBatch(t *testing.T) ApplyBatch {
	t.Helper()
	snapshot := decodeFixture(t, "bot-bindings-v1.json")
	snapshot.Trials = []LegacyTrial{legacyTrialFixture()}
	snapshot.LegacyTrialSaltSHA256 = protectedTrialImportFixture().SaltSHA256
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %#v", report.Blockers)
	}
	operations, err := planOperations(plan)
	if err != nil {
		t.Fatalf("planOperations: %v", err)
	}
	for _, operation := range operations {
		if operation.Entity == "trial" {
			batch := ApplyBatch{
				RunID: "import-run-protected-trial", PlanDigest: plan.PlanDigest, Index: 0,
				Operations: []ApplyOperation{operation},
			}
			batch.Digest = digestBatch(batch.Operations)
			return batch
		}
	}
	t.Fatal("canonical trial operation is missing")
	return ApplyBatch{}
}

func legacyTrialFixture() LegacyTrial {
	return LegacyTrial{
		SourceKey:        "legacy-trial-owner",
		LegacyAnchorHMAC: strings.Repeat("2", 64),
		CurrentHMAC:      strings.Repeat("3", 64),
		Used:             false,
		ExpiresAtUnix:    2_000_000,
	}
}

func protectedTrialImportFixture() TrialImportProtection {
	return TrialImportProtection{
		KeyVersion:            1,
		EncryptedSaltEnvelope: `{"key_version":1,"nonce_b64":"AAECAwQFBgcICQoL","ciphertext_b64":"cHJvdGVjdGVkLXRyaWFsLXNhbHQ="}`,
		SaltSHA256:            strings.Repeat("8", 64),
	}
}

func standaloneEncryptedSecret() LegacyEncryptedSecret {
	return LegacyEncryptedSecret{
		SecretID:       "secret-standalone-wb",
		OwnerType:      "legacy_service",
		OwnerSourceKey: "s3-wb",
		Field:          "token",
		Kind:           "bearer",
		KeyVersion:     1,
		NonceB64:       "AAECAwQFBgcICQoL",
		CiphertextB64:  "c3ludGhldGljLWVuY3J5cHRlZA==",
		SHA256:         strings.Repeat("a", 64),
	}
}

func TestReadReferencedKeyVersionsUsesOneLinearizableRead(t *testing.T) {
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{
		Rows: []map[string]any{
			{"key_version": int64(3)},
			{"key_version": int64(1)},
			{"key_version": int64(3)},
			{"key_version": int64(2)},
		},
	}}}}
	store, err := NewRQLiteApplyStore(db, time.Now)
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	got, err := store.ReadReferencedKeyVersions(context.Background())
	if err != nil {
		t.Fatalf("ReadReferencedKeyVersions: %v", err)
	}
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("key versions = %v, want %v", got, want)
	}
	if db.queryCalls != 1 || len(db.queries) != 1 || len(db.queries[0]) != 1 {
		t.Fatalf("linearizable calls=%d queries=%#v", db.queryCalls, db.queries)
	}
	if len(db.requests) != 0 {
		t.Fatalf("key-version preflight performed %d mutation(s)", len(db.requests))
	}
}

func TestReadReferencedKeyVersionsRejectsMalformedRowsWithoutMutation(t *testing.T) {
	for _, value := range []any{int64(0), int64(-1), "1", nil} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{{
				Rows: []map[string]any{{"key_version": value}},
			}}}}
			store, err := NewRQLiteApplyStore(db, time.Now)
			if err != nil {
				t.Fatalf("NewRQLiteApplyStore: %v", err)
			}
			if _, err := store.ReadReferencedKeyVersions(context.Background()); err == nil {
				t.Fatal("malformed key-version row was accepted")
			}
			if len(db.requests) != 0 {
				t.Fatalf("malformed preflight performed %d mutation(s)", len(db.requests))
			}
		})
	}
}

func TestRQLiteApplyStoreAcceptsRotatedProtectedTrialSaltVersion(t *testing.T) {
	protection := protectedTrialImportFixture()
	protection.KeyVersion = 7
	protection.EncryptedSaltEnvelope = strings.Replace(
		protection.EncryptedSaltEnvelope,
		`"key_version":1`,
		`"key_version":7`,
		1,
	)
	if _, err := NewRQLiteApplyStoreWithTrialProtection(
		&applyStoreRQLite{},
		time.Now,
		protection,
	); err != nil {
		t.Fatalf("rotated protected trial salt was rejected: %v", err)
	}
}

func TestReadAppliedRunEvidenceRequiresOneCompletedExactRun(t *testing.T) {
	db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{
		{
			Rows: []map[string]any{{
				"import_run_id":        "synthetic-run",
				"snapshot_kind":        "full",
				"source_sha256":        strings.Repeat("1", 64),
				"plan_sha256":          strings.Repeat("2", 64),
				"parent_source_sha256": nil,
				"target_sha256":        strings.Repeat("3", 64),
				"batch_count":          int64(2),
				"status":               "applied",
				"completed_at_unix":    int64(2_000_000),
			}},
		},
		{
			Rows: []map[string]any{
				{"batch_index": int64(0), "batch_digest": strings.Repeat("a", 64), "status": "applied"},
				{"batch_index": int64(1), "batch_digest": strings.Repeat("b", 64), "status": "applied"},
			},
		},
	}}}
	store, err := NewRQLiteApplyStore(db, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadAppliedRunEvidence(context.Background(), "synthetic-run")
	if err != nil {
		t.Fatalf("ReadAppliedRunEvidence: %v", err)
	}
	if got.RunID != "synthetic-run" || got.BatchCount != 2 ||
		len(got.BatchReceiptDigest) != 64 || got.CompletedAtUnix != 2_000_000 {
		t.Fatalf("evidence=%#v", got)
	}
	if db.queryCalls != 1 || len(db.queries) != 1 || len(db.queries[0]) != 2 {
		t.Fatalf("evidence query calls=%d queries=%#v", db.queryCalls, db.queries)
	}
	if len(db.requests) != 0 {
		t.Fatalf("evidence read performed %d mutation(s)", len(db.requests))
	}
}

func TestReadAppliedRunEvidenceRejectsMissingExtraOrMismatchedBatches(t *testing.T) {
	runRow := map[string]any{
		"import_run_id":        "synthetic-run",
		"snapshot_kind":        "delta",
		"source_sha256":        strings.Repeat("1", 64),
		"plan_sha256":          strings.Repeat("2", 64),
		"parent_source_sha256": strings.Repeat("4", 64),
		"target_sha256":        strings.Repeat("3", 64),
		"batch_count":          int64(2),
		"status":               "applied",
		"completed_at_unix":    int64(2_000_000),
	}
	cases := []struct {
		name    string
		batches []map[string]any
	}{
		{"missing", []map[string]any{
			{"batch_index": int64(0), "batch_digest": strings.Repeat("a", 64), "status": "applied"},
		}},
		{"extra", []map[string]any{
			{"batch_index": int64(0), "batch_digest": strings.Repeat("a", 64), "status": "applied"},
			{"batch_index": int64(1), "batch_digest": strings.Repeat("b", 64), "status": "applied"},
			{"batch_index": int64(2), "batch_digest": strings.Repeat("c", 64), "status": "applied"},
		}},
		{"gap", []map[string]any{
			{"batch_index": int64(0), "batch_digest": strings.Repeat("a", 64), "status": "applied"},
			{"batch_index": int64(2), "batch_digest": strings.Repeat("b", 64), "status": "applied"},
		}},
		{"non-applied", []map[string]any{
			{"batch_index": int64(0), "batch_digest": strings.Repeat("a", 64), "status": "applied"},
			{"batch_index": int64(1), "batch_digest": strings.Repeat("b", 64), "status": "applying"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &applyStoreRQLite{queryResponses: [][]rqlite.Result{{
				{Rows: []map[string]any{runRow}},
				{Rows: tc.batches},
			}}}
			store, err := NewRQLiteApplyStore(db, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReadAppliedRunEvidence(context.Background(), "synthetic-run"); err == nil {
				t.Fatal("invalid applied-run evidence was accepted")
			}
			if len(db.requests) != 0 {
				t.Fatalf("invalid evidence read performed %d mutation(s)", len(db.requests))
			}
		})
	}
}
