package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const testRestoreClusterID = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func restoreStateRow(epoch int64, activated bool, backupSHA string) map[string]any {
	active := int64(0)
	var activatedAt any
	if activated {
		active = 1
		activatedAt = int64(2_000_000)
	}
	var restoredFrom any
	if backupSHA != "" {
		restoredFrom = backupSHA
	}
	return map[string]any{
		"cluster_id":                  testRestoreClusterID,
		"restore_epoch":               epoch,
		"restored_from_backup_sha256": restoredFrom,
		"activated":                   active,
		"created_at_unix":             int64(1_900_000),
		"activated_at_unix":           activatedAt,
	}
}

func TestAdvanceAfterRestoreRaisesEpochAndInvalidatesEveryLease(t *testing.T) {
	backupSHA := strings.Repeat("a", 64)
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{restoreStateRow(8, false, backupSHA)}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 1},
	)}}

	state, err := NewRestoreEpochStore(db).AdvanceAfterRestore(
		context.Background(),
		7,
		backupSHA,
	)
	if err != nil {
		t.Fatalf("AdvanceAfterRestore: %v", err)
	}
	if state.RestoreEpoch != 8 || state.Activated ||
		state.RestoredFromBackupSHA256 != backupSHA {
		t.Fatalf("state=%#v", state)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("request calls=%d, want 1", len(db.requestCalls))
	}
	call := db.requestCalls[0]
	if call.level != rqlite.Linearizable || !call.transaction ||
		len(call.statements) != 4 {
		t.Fatalf("request=%#v", call)
	}
	joined := strings.ToLower(statementsText(call.statements))
	for _, required := range []string{
		"update cluster_restore_state",
		"restore_epoch = ?",
		"activated = 0",
		"delete from node_leases",
		"delete from cluster_job_leases",
		"update telegram_pollers",
		"lease_token=null",
		"lease_fence=lease_fence+1",
		"exists",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("restore transaction lacks %q: %s", required, joined)
		}
	}
}

func TestAdvanceAfterRestoreRejectsStaleEpochWithoutUngatedStatements(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: nil},
		rqlite.Result{},
		rqlite.Result{},
		rqlite.Result{},
	)}}
	if _, err := NewRestoreEpochStore(db).AdvanceAfterRestore(
		context.Background(),
		6,
		strings.Repeat("b", 64),
	); err == nil {
		t.Fatal("stale restore epoch was accepted")
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("request calls=%d, want 1", len(db.requestCalls))
	}
	for index, statement := range db.requestCalls[0].statements[1:] {
		sql := strings.ToLower(statement.SQL)
		if !strings.Contains(sql, "exists") ||
			!strings.Contains(sql, "cluster_restore_state") ||
			!strings.Contains(sql, "restore_epoch") ||
			!strings.Contains(sql, "activated = 0") {
			t.Fatalf("dependent statement %d is not epoch gated: %s", index+1, statement.SQL)
		}
	}
}

func TestAdvanceAfterRestoreResolvesUnknownOutcomeByExactRead(t *testing.T) {
	backupSHA := strings.Repeat("d", 64)
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation:      "request",
			UnknownOutcome: true,
			Err:            errors.New("synthetic ambiguity"),
		}}},
		linear: []scriptedResult{rowsScript(restoreStateRow(10, false, backupSHA))},
	}
	state, err := NewRestoreEpochStore(db).AdvanceAfterRestore(
		context.Background(),
		9,
		backupSHA,
	)
	if err != nil {
		t.Fatalf("AdvanceAfterRestore unknown outcome: %v", err)
	}
	if state.RestoreEpoch != 10 || state.Activated {
		t.Fatalf("state=%#v", state)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
		t.Fatalf("requests=%d linear=%d, want 1/1", len(db.requestCalls), len(db.linearCalls))
	}
}

func TestActivateRequiresExactUnactivatedEpoch(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{restoreStateRow(12, true, strings.Repeat("e", 64))}},
	)}}
	state, err := NewRestoreEpochStore(db).Activate(context.Background(), 12)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if state.RestoreEpoch != 12 || !state.Activated {
		t.Fatalf("state=%#v", state)
	}
	sql := strings.ToLower(db.requestCalls[0].statements[0].SQL)
	for _, required := range []string{"restore_epoch = ?", "activated = 0", "activated = 1", "returning"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("activation lacks %q: %s", required, sql)
		}
	}

	stale := &recordingRQLite{requests: []scriptedResult{resultsScript(rqlite.Result{Rows: nil})}}
	if _, err := NewRestoreEpochStore(stale).Activate(context.Background(), 11); err == nil {
		t.Fatal("stale activation epoch was accepted")
	}
}

func TestCurrentRejectsMissingDuplicateOrMalformedRestoreState(t *testing.T) {
	cases := []struct {
		name string
		rows []map[string]any
	}{
		{name: "missing"},
		{name: "duplicate", rows: []map[string]any{
			restoreStateRow(1, true, ""),
			restoreStateRow(1, true, ""),
		}},
		{name: "zero epoch", rows: []map[string]any{restoreStateRow(0, true, "")}},
		{name: "bad cluster id", rows: []map[string]any{func() map[string]any {
			row := restoreStateRow(1, true, "")
			row["cluster_id"] = "not-canonical"
			return row
		}()}},
		{name: "bad activation", rows: []map[string]any{func() map[string]any {
			row := restoreStateRow(1, true, "")
			row["activated"] = int64(2)
			return row
		}()}},
		{name: "bad backup digest", rows: []map[string]any{
			restoreStateRow(2, false, "not-canonical"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &recordingRQLite{linear: []scriptedResult{rowsScript(tc.rows...)}}
			if _, err := NewRestoreEpochStore(db).Current(context.Background()); err == nil {
				t.Fatal("malformed restore state was accepted")
			}
			if len(db.requestCalls) != 0 {
				t.Fatalf("Current mutated database: %#v", db.requestCalls)
			}
		})
	}
}

func TestCurrentReturnsExactRestoreState(t *testing.T) {
	backupSHA := strings.Repeat("f", 64)
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(
		restoreStateRow(4, true, backupSHA),
	)}}
	state, err := NewRestoreEpochStore(db).Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got := fmt.Sprintf("%s/%d/%t/%s", state.ClusterID, state.RestoreEpoch, state.Activated, state.RestoredFromBackupSHA256); got != testRestoreClusterID+"/4/true/"+backupSHA {
		t.Fatalf("state=%s", got)
	}
}

func TestRestoreIntegerAcceptsExactJSONNumberFromRQLiteDecoder(t *testing.T) {
	got, ok := restoreInteger(json.Number("42"))
	if !ok || got != 42 {
		t.Fatalf("restoreInteger(json.Number(42)) = %d/%t", got, ok)
	}
	for _, invalid := range []json.Number{"", "1.0", "9e1"} {
		if _, ok := restoreInteger(invalid); ok {
			t.Fatalf("restoreInteger accepted non-integer json.Number %q", invalid)
		}
	}
}
