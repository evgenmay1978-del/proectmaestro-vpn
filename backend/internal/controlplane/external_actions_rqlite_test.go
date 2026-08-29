package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRQLiteExternalActionStartCommitsFenceBeforePost(t *testing.T) {
	db := &recordingRQLite{requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		args := statements[0].Args
		return []rqlite.Result{{RowsAffected: 1}, {Rows: []map[string]any{{
			"action_id":           "action-1",
			"action_type":         args[4],
			"resource_id":         args[6],
			"idempotency_key":     args[5],
			"request_sha256":      args[7],
			"status":              "applying",
			"response_envelope":   nil,
			"replaces_action_id":  nil,
			"replaces_action_key": nil,
			"attempt_worker_id":   args[1],
			"attempt_lease_token": args[2],
			"attempt_lease_fence": args[3],
		}}}}, nil
	}}
	service, _ := testService(t, db)
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatalf("NewRQLiteExternalActions: %v", err)
	}
	result, err := store.StartAttempt(context.Background(), ExternalActionCommand{
		Type: "wb.create-room", ResourceID: "alice", ActionKey: "key-1", Request: []byte(`{"login":"alice"}`),
		WorkerID: "panel-a", LeaseToken: "lease-1", LeaseFence: 7,
	})
	if err != nil || result.State != "attempt_started" {
		t.Fatalf("StartAttempt = %#v, err=%v", result, err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("request calls = %#v, want one fenced transaction", db.requestCalls)
	}
	sql := strings.ToLower(joinedRequestSQL(db))
	for _, fragment := range []string{
		"cluster_job_leases", "lease_token", "expires_at_unix>unixepoch()", "status='applying'",
		"attempt_worker_id", "attempt_lease_token", "attempt_lease_fence", "request_sha256",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("fenced start SQL missing %q: %s", fragment, sql)
		}
	}
}
