package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func task6ReviewRestoreHandoffRow(epoch int64, backupSHA string) map[string]any {
	row := restoreStateRow(epoch, false, backupSHA)
	row["backup_restore_epoch"] = epoch
	row["dirty_generation"] = int64(1)
	row["verified_generation"] = int64(0)
	row["last_attempt_sequence"] = int64(0)
	row["phase"] = "dirty"
	row["active_attempts"] = int64(0)
	row["node_leases"] = int64(0)
	row["job_leases"] = int64(0)
	row["leased_pollers"] = int64(0)
	return row
}

// Break caught: checking only cluster_restore_state can accept a committed
// cluster epoch whose backup singleton and lease handoff never completed.
func TestAdvanceAfterRestoreRejectsUnknownOutcomeWithPartialPostcondition(t *testing.T) {
	backupSHA := strings.Repeat("d", 64)
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic ambiguity"),
		}}},
		linear: []scriptedResult{rowsScript(restoreStateRow(10, false, backupSHA))},
	}
	if _, err := NewRestoreEpochStore(db).AdvanceAfterRestore(context.Background(), 9, backupSHA); err == nil {
		t.Fatal("partial committed-unknown postcondition was accepted")
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
		t.Fatalf("requests=%d linear=%d, want 1/1", len(db.requestCalls), len(db.linearCalls))
	}
}

func TestAdvanceAfterRestoreResolvesUnknownOutcomeByFullPostcondition(t *testing.T) {
	backupSHA := strings.Repeat("e", 64)
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic committed ambiguity"),
		}}},
		linear: []scriptedResult{rowsScript(task6ReviewRestoreHandoffRow(11, backupSHA))},
	}
	state, err := NewRestoreEpochStore(db).AdvanceAfterRestore(context.Background(), 10, backupSHA)
	if err != nil || state.RestoreEpoch != 11 || state.Activated {
		t.Fatalf("AdvanceAfterRestore state=%#v error=%v", state, err)
	}
	if len(db.linearCalls) != 1 || len(db.linearCalls[0].statements) != 1 {
		t.Fatalf("linear calls=%#v, want one full-postcondition read", db.linearCalls)
	}
	sql := strings.ToLower(db.linearCalls[0].statements[0].SQL)
	for _, required := range []string{
		"backup_rpo_state", "verified_backup_id is null", "backup_rpo_attempts",
		"node_leases", "cluster_job_leases", "telegram_pollers",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("committed-unknown resolver lacks %q: %s", required, sql)
		}
	}
}
