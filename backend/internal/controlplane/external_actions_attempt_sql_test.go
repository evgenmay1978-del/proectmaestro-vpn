package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestExternalActionAttemptOwnerSchemaSQLite(t *testing.T) {
	db, _, store := f10ReplacementStore(t)
	command := f10AttemptCommand("schema-owner")
	action := f10PrepareExternalAction(t, store, command)

	columns := db.must(t, rqlite.Statement{SQL: `SELECT name FROM pragma_table_info('external_actions')
WHERE name IN ('attempt_worker_id','attempt_lease_token','attempt_lease_fence') ORDER BY name`})[0].Rows
	if len(columns) != 3 {
		t.Fatalf("attempt owner columns=%#v, want all three", columns)
	}
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,
created_at_unix,updated_at_unix,attempt_worker_id,attempt_lease_token,attempt_lease_fence)
SELECT 'owner-insert-action',action_type,resource_id,'owner-insert-key',request_envelope,request_sha256,
'pending',0,created_at_unix,updated_at_unix,'worker-a','token-a',1
FROM external_actions WHERE action_id=?`, Args: []any{action.ID}})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions SET attempt_worker_id='worker-a'
WHERE action_id=?`, Args: []any{action.ID}})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions
SET attempt_worker_id='worker-a',attempt_lease_token='token-a',attempt_lease_fence=0
WHERE action_id=?`, Args: []any{action.ID}})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions
SET attempt_worker_id='worker-a',attempt_lease_token='token-a',attempt_lease_fence=1
WHERE action_id=?`, Args: []any{action.ID}})
	db.must(t, rqlite.Statement{SQL: `UPDATE external_actions
SET status='applying',attempt_worker_id='worker-a',attempt_lease_token='token-a',attempt_lease_fence=1
WHERE action_id=?`, Args: []any{action.ID}})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions
SET attempt_worker_id='worker-b',attempt_lease_token='token-b',attempt_lease_fence=2
WHERE action_id=?`, Args: []any{action.ID}})
}

func TestExternalActionAttemptExactOwnerStartAndFinishSQLite(t *testing.T) {
	db, _, store := f10ReplacementStore(t)
	command := f10AttemptCommand("owner-success")
	action := f10PrepareExternalAction(t, store, command)
	f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)

	started, err := store.StartAttempt(context.Background(), command)
	if err != nil || started.ID != action.ID || started.State != "attempt_started" {
		t.Fatalf("StartAttempt=%#v err=%v", started, err)
	}
	row := f10AttemptRow(t, db, action.ID)
	if row["status"] != "applying" || f10AttemptRowInt(t, row, "attempts") != 1 ||
		row["attempt_worker_id"] != command.WorkerID || row["attempt_lease_token"] != command.LeaseToken ||
		f10AttemptRowInt(t, row, "attempt_lease_fence") != command.LeaseFence {
		t.Fatalf("durable attempt owner=%#v", row)
	}

	response := []byte(`{"room":"owner-room"}`)
	finished, err := store.Finish(context.Background(), command, response)
	if err != nil || finished.ID != action.ID || finished.State != "succeeded" || string(finished.Response) != string(response) {
		t.Fatalf("Finish=%#v err=%v", finished, err)
	}
	row = f10AttemptRow(t, db, action.ID)
	if row["status"] != "applied" || row["response_envelope"] == nil {
		t.Fatalf("finished durable row=%#v", row)
	}
}

func TestExternalActionAttemptLeaseHandoffFencesOldOwnerSQLite(t *testing.T) {
	db, _, store := f10ReplacementStore(t)
	command := f10AttemptCommand("handoff")
	action := f10PrepareExternalAction(t, store, command)
	f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
	if _, err := store.StartAttempt(context.Background(), command); err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}

	takeover := command
	takeover.WorkerID, takeover.LeaseToken, takeover.LeaseFence = "worker-b", "lease-b", command.LeaseFence+1
	f10SetExternalActionLease(t, db, takeover, takeover.WorkerID, takeover.LeaseToken, takeover.LeaseFence)
	if result, err := store.Finish(context.Background(), command, []byte(`{"room":"stale"}`)); !errors.Is(err, ErrLeaseLost) || len(result.Response) != 0 {
		t.Fatalf("old Finish=%#v err=%v, want empty ErrLeaseLost", result, err)
	}
	if _, err := store.MarkUnknown(context.Background(), command); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old MarkUnknown err=%v, want ErrLeaseLost", err)
	}
	row := f10AttemptRow(t, db, action.ID)
	if row["status"] != "applying" || row["response_envelope"] != nil {
		t.Fatalf("stale owner mutated action: %#v", row)
	}

	unknown, err := store.MarkUnknown(context.Background(), takeover)
	if err != nil || unknown.State != "unknown" || unknown.ID != action.ID {
		t.Fatalf("takeover MarkUnknown=%#v err=%v", unknown, err)
	}
	if result, err := store.Finish(context.Background(), command, []byte(`{"room":"after-takeover"}`)); !errors.Is(err, ErrLeaseLost) || len(result.Response) != 0 {
		t.Fatalf("old Finish after takeover=%#v err=%v", result, err)
	}
	row = f10AttemptRow(t, db, action.ID)
	if row["status"] != "unknown" || row["response_envelope"] != nil {
		t.Fatalf("post-takeover durable row=%#v", row)
	}
}

func TestExternalActionAttemptExpiredLeaseFencesTerminalMutationsSQLite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RQLiteExternalActions, ExternalActionCommand) (ExternalActionResult, error)
	}{
		{name: "finish", mutate: func(store *RQLiteExternalActions, command ExternalActionCommand) (ExternalActionResult, error) {
			return store.Finish(context.Background(), command, []byte(`{"room":"expired"}`))
		}},
		{name: "mark unknown", mutate: func(store *RQLiteExternalActions, command ExternalActionCommand) (ExternalActionResult, error) {
			return store.MarkUnknown(context.Background(), command)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, _, store := f10ReplacementStore(t)
			command := f10AttemptCommand("expired-" + test.name)
			action := f10PrepareExternalAction(t, store, command)
			f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
			if _, err := store.StartAttempt(context.Background(), command); err != nil {
				t.Fatalf("StartAttempt: %v", err)
			}
			db.must(t, rqlite.Statement{SQL: `UPDATE cluster_job_leases SET expires_at_unix=unixepoch()
WHERE job_name=?`, Args: []any{"external-action:" + command.Type}})

			result, err := test.mutate(store, command)
			if !errors.Is(err, ErrLeaseLost) || len(result.Response) != 0 {
				t.Fatalf("expired %s=%#v err=%v, want empty ErrLeaseLost", test.name, result, err)
			}
			row := f10AttemptRow(t, db, action.ID)
			if row["status"] != "applying" || row["response_envelope"] != nil ||
				row["attempt_worker_id"] != command.WorkerID || row["attempt_lease_token"] != command.LeaseToken ||
				f10AttemptRowInt(t, row, "attempt_lease_fence") != command.LeaseFence {
				t.Fatalf("expired %s mutated durable action: %#v", test.name, row)
			}
		})
	}
}

func TestExternalActionLegacyApplyingTakeoverMarksUnknownSQLite(t *testing.T) {
	db, _, store := f10ReplacementStore(t)
	command := f10AttemptCommand("legacy-applying")
	action := f10PrepareExternalAction(t, store, command)
	db.must(t, rqlite.Statement{SQL: `UPDATE external_actions SET status='applying',attempts=1 WHERE action_id=?`, Args: []any{action.ID}})
	f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
	result, err := store.MarkUnknown(context.Background(), command)
	if err != nil || result.State != "unknown" || result.ID != action.ID {
		t.Fatalf("legacy MarkUnknown=%#v err=%v", result, err)
	}
	row := f10AttemptRow(t, db, action.ID)
	if row["status"] != "unknown" || row["attempt_worker_id"] != nil || row["attempt_lease_token"] != nil || row["attempt_lease_fence"] != nil {
		t.Fatalf("legacy takeover row=%#v", row)
	}
}

func TestExternalActionAttemptMutationsRejectBindingBeforeMutationSQLite(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		db, _, store := f10ReplacementStore(t)
		command := f10AttemptCommand("binding-start")
		action := f10PrepareExternalAction(t, store, command)
		f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
		conflict := command
		conflict.ResourceID = "bob"
		if _, err := store.StartAttempt(context.Background(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("binding-conflicting StartAttempt err=%v, want ErrConflict", err)
		}
		if row := f10AttemptRow(t, db, action.ID); row["status"] != "pending" || f10AttemptRowInt(t, row, "attempts") != 0 {
			t.Fatalf("binding-conflicting start mutated row=%#v", row)
		}
	})

	t.Run("finish and mark unknown", func(t *testing.T) {
		db, _, store := f10ReplacementStore(t)
		command := f10AttemptCommand("binding-terminal")
		action := f10PrepareExternalAction(t, store, command)
		f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
		if _, err := store.StartAttempt(context.Background(), command); err != nil {
			t.Fatalf("StartAttempt: %v", err)
		}
		conflict := command
		conflict.Request = []byte(`{"different":true}`)
		if result, err := store.Finish(context.Background(), conflict, []byte(`{"room":"wrong"}`)); !errors.Is(err, ErrConflict) || len(result.Response) != 0 {
			t.Fatalf("binding-conflicting Finish=%#v err=%v", result, err)
		}
		conflict = command
		conflict.ReplacesActionKey = "not-the-relation"
		if _, err := store.MarkUnknown(context.Background(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("relation-conflicting MarkUnknown err=%v", err)
		}
		if row := f10AttemptRow(t, db, action.ID); row["status"] != "applying" || row["response_envelope"] != nil {
			t.Fatalf("binding-conflicting terminal mutation changed row=%#v", row)
		}
	})
}

func TestExternalActionAmbiguousStartIsUnavailableAndTakeoverSafeSQLite(t *testing.T) {
	db, service, store := f10ReplacementStore(t)
	command := f10AttemptCommand("ambiguous-start")
	action := f10PrepareExternalAction(t, store, command)
	f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)

	wrapper := &f10CommittedUnknownDB{RQLite: db}
	wrapperStore := *service.store
	wrapperStore.db = wrapper
	wrapperService := *service
	wrapperService.store = &wrapperStore
	persistence, err := NewRQLiteExternalActions(&wrapperService)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.StartAttempt(context.Background(), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ambiguous StartAttempt err=%v, want ErrUnavailable", err)
	}
	if wrapper.requests != 1 || wrapper.linearReads != 0 {
		t.Fatalf("ambiguous StartAttempt writes/reads=(%d,%d), want (1,0)", wrapper.requests, wrapper.linearReads)
	}
	prepared, err := store.Prepare(context.Background(), command)
	if err != nil || prepared.ID != action.ID || prepared.State != "attempt_started" {
		t.Fatalf("Prepare after ambiguous start=%#v err=%v", prepared, err)
	}
	row := f10AttemptRow(t, db, action.ID)
	if row["attempt_worker_id"] != command.WorkerID || row["attempt_lease_token"] != command.LeaseToken || f10AttemptRowInt(t, row, "attempt_lease_fence") != command.LeaseFence {
		t.Fatalf("ambiguous start owner=%#v", row)
	}
	takeover := command
	takeover.WorkerID, takeover.LeaseToken, takeover.LeaseFence = "worker-new", "lease-new", command.LeaseFence+1
	f10SetExternalActionLease(t, db, takeover, takeover.WorkerID, takeover.LeaseToken, takeover.LeaseFence)
	if result, err := store.MarkUnknown(context.Background(), takeover); err != nil || result.State != "unknown" {
		t.Fatalf("takeover after ambiguous start=%#v err=%v", result, err)
	}
}

func f10AttemptCommand(suffix string) ExternalActionCommand {
	return ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "attempt-" + suffix,
		WorkerID: "worker-a", LeaseToken: "lease-a-" + suffix, LeaseFence: 1,
		Request: []byte(`{"login":"alice"}`),
	}
}

func f10SetExternalActionLease(t *testing.T, db *customerIntegritySQLite, command ExternalActionCommand, workerID, leaseToken string, leaseFence int64) {
	t.Helper()
	jobName := "external-action:" + command.Type
	db.must(t, rqlite.Statement{SQL: `INSERT INTO cluster_job_leases(
job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,lease_fence)
VALUES(?,?,?,1999900,2000100,?)
ON CONFLICT(job_name) DO UPDATE SET holder_id=excluded.holder_id,lease_token=excluded.lease_token,
acquired_at_unix=excluded.acquired_at_unix,expires_at_unix=excluded.expires_at_unix,lease_fence=excluded.lease_fence`, Args: []any{
		jobName, workerID, leaseToken, leaseFence,
	}})
}

func f10AttemptRow(t *testing.T, db *customerIntegritySQLite, actionID string) map[string]any {
	t.Helper()
	rows := db.must(t, rqlite.Statement{SQL: `SELECT status,attempts,response_envelope,
attempt_worker_id,attempt_lease_token,attempt_lease_fence FROM external_actions WHERE action_id=?`, Args: []any{actionID}})[0].Rows
	if len(rows) != 1 {
		t.Fatalf("attempt row=%#v", rows)
	}
	return rows[0]
}

func f10AttemptRowInt(t *testing.T, row map[string]any, key string) int64 {
	t.Helper()
	value, ok := rowInt64(row, key)
	if !ok {
		t.Fatalf("attempt row has invalid %s: %#v", key, row)
	}
	return value
}
