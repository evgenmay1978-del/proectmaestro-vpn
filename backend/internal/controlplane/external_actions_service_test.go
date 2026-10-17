package controlplane

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestServiceExecuteExternalActionAcquiresFencePostsOnceAndReplaysResponse(t *testing.T) {
	var state string
	var responseEnvelope any
	var binding serviceActionBinding
	var attemptWorkerID, attemptLeaseToken string
	var attemptLeaseFence int64
	db := &recordingRQLite{requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) == 0 {
			t.Fatal("empty transaction")
		}
		sql := strings.ToLower(statements[0].SQL)
		switch {
		case strings.Contains(sql, "insert into cluster_job_leases"):
			leaseToken, _ := statements[0].Args[2].(string)
			return []rqlite.Result{{}, {Rows: []map[string]any{{
				"holder_id": "panel-a", "lease_token": leaseToken, "lease_fence": int64(7), "expires_at_unix": int64(2_000_030),
			}}}}, nil
		case strings.Contains(sql, "insert or ignore into external_actions"):
			affected := int64(0)
			if state == "" {
				state = "pending"
				affected = 1
				binding = serviceActionBinding{
					actionType:  fmt.Sprint(statements[0].Args[1]),
					resourceID:  fmt.Sprint(statements[0].Args[2]),
					actionKey:   fmt.Sprint(statements[0].Args[3]),
					requestHash: fmt.Sprint(statements[0].Args[5]),
				}
			}
			return serviceActionResults(state, responseEnvelope, binding, attemptWorkerID, attemptLeaseToken, attemptLeaseFence, affected), nil
		case strings.Contains(sql, "set status='applying'"):
			affected := int64(0)
			if state == "pending" {
				state = "applying"
				affected = 1
				attemptWorkerID, _ = statements[0].Args[1].(string)
				attemptLeaseToken, _ = statements[0].Args[2].(string)
				attemptLeaseFence, _ = statements[0].Args[3].(int64)
			}
			return serviceActionResults(state, responseEnvelope, binding, attemptWorkerID, attemptLeaseToken, attemptLeaseFence, affected), nil
		case strings.Contains(sql, "set status='applied'"):
			affected := int64(0)
			if state == "applying" {
				state = "applied"
				affected = 1
				switch encoded := statements[0].Args[0].(type) {
				case []byte:
					responseEnvelope = base64.StdEncoding.EncodeToString(encoded)
				}
			}
			return serviceActionResults(state, responseEnvelope, binding, attemptWorkerID, attemptLeaseToken, attemptLeaseFence, affected), nil
		default:
			t.Fatalf("unexpected SQL: %s", statements[0].SQL)
			return nil, nil
		}
	}}
	service, _ := testService(t, db)
	sender := &countingExternalSender{}
	command := ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "wb-room-alice-1",
		Request: []byte(`{"login":"alice"}`),
	}

	first, err := service.ExecuteExternalAction(context.Background(), command, "panel-a", sender)
	if err != nil {
		t.Fatalf("first ExecuteExternalAction: %v", err)
	}
	if first.State != "succeeded" || string(first.Response) != `{"room":"wb-1"}` {
		t.Fatalf("first result = %#v", first)
	}
	second, err := service.ExecuteExternalAction(context.Background(), command, "panel-a", sender)
	if err != nil {
		t.Fatalf("replay ExecuteExternalAction: %v", err)
	}
	if second.ID != first.ID || second.State != "succeeded" || string(second.Response) != string(first.Response) {
		t.Fatalf("replay result = %#v, want %#v", second, first)
	}
	if sender.posts != 1 {
		t.Fatalf("provider POSTs = %d, want exactly one across replay", sender.posts)
	}
	joined := strings.ToLower(joinedRequestSQL(db))
	for _, required := range []string{
		"insert into cluster_job_leases", "lease_fence",
		"insert or ignore into external_actions", "set status='applying'", "set status='applied'",
		"attempt_worker_id", "attempt_lease_token", "attempt_lease_fence",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("durable external action SQL missing %q: %s", required, joined)
		}
	}
	jobNamePresent := false
	for _, call := range db.requestCalls {
		for _, statement := range call.statements {
			jobNamePresent = jobNamePresent || containsStatementArg(statement, "external-action:wb.room")
		}
	}
	if !jobNamePresent {
		t.Fatal("durable external-action:wb.room lease key missing")
	}
	if strings.Contains(joined, "alice") || strings.Contains(joined, "wb-1") {
		t.Fatalf("external action SQL leaked plaintext request/response: %s", joined)
	}
}

func containsStatementArg(statement rqlite.Statement, want string) bool {
	for _, arg := range statement.Args {
		if actual, ok := arg.(string); ok && actual == want {
			return true
		}
	}
	return false
}

type serviceActionBinding struct {
	actionType, resourceID, actionKey, requestHash string
}

func serviceActionResults(
	state string, response any, binding serviceActionBinding,
	workerID, leaseToken string, leaseFence, rowsAffected int64,
) []rqlite.Result {
	row := map[string]any{
		"action_id":           "external_action_1",
		"action_type":         binding.actionType,
		"resource_id":         binding.resourceID,
		"idempotency_key":     binding.actionKey,
		"request_sha256":      binding.requestHash,
		"status":              state,
		"response_envelope":   response,
		"replaces_action_id":  nil,
		"replaces_action_key": nil,
		"attempt_worker_id":   nil,
		"attempt_lease_token": nil,
		"attempt_lease_fence": nil,
	}
	if workerID != "" {
		row["attempt_worker_id"] = workerID
		row["attempt_lease_token"] = leaseToken
		row["attempt_lease_fence"] = leaseFence
	}
	return []rqlite.Result{{RowsAffected: rowsAffected}, {Rows: []map[string]any{row}}}
}
