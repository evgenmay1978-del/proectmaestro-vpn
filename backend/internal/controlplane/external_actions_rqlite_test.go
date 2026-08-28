package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRQLiteExternalActionStartCommitsFenceBeforePost(t *testing.T) {
	db := &recordingRQLite{requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		return []rqlite.Result{{}, {Rows: []map[string]any{{"action_id": "action-1", "status": "applying"}}}}, nil
	}}
	service, _ := testService(t, db)
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatalf("NewRQLiteExternalActions: %v", err)
	}
	result, err := store.StartAttempt(context.Background(), ExternalActionCommand{
		Type: "wb.create-room", ActionKey: "key-1", WorkerID: "panel-a", LeaseToken: "lease-1",
	})
	if err != nil || result.State != "attempt_started" {
		t.Fatalf("StartAttempt = %#v, err=%v", result, err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("request calls = %#v, want one fenced transaction", db.requestCalls)
	}
	sql := strings.ToLower(joinedRequestSQL(db))
	for _, fragment := range []string{"cluster_job_leases", "lease_token", "expires_at_unix>unixepoch()", "status='applying'"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("fenced start SQL missing %q: %s", fragment, sql)
		}
	}
}
