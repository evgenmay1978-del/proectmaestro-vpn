package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type durableExternalActionFixture struct {
	states     map[string]string
	allowStart bool
}

func newDurableExternalActionPersistence(t *testing.T, allowStart bool) (*RQLiteExternalActions, *durableExternalActionFixture) {
	t.Helper()
	fixture := &durableExternalActionFixture{states: make(map[string]string), allowStart: allowStart}
	db := &recordingRQLite{requestFn: func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) < 2 {
			return nil, errors.New("unexpected external action transaction")
		}
		sql := strings.ToLower(statements[0].SQL)
		if strings.Contains(sql, "insert or ignore into external_actions") {
			key, _ := statements[0].Args[3].(string)
			if _, ok := fixture.states[key]; !ok {
				fixture.states[key] = "pending"
			}
			return actionFixtureResults(key, fixture.states[key]), nil
		}
		if strings.Contains(sql, "status='applying'") {
			key, _ := statements[0].Args[2].(string)
			if fixture.allowStart && fixture.states[key] == "pending" {
				fixture.states[key] = "applying"
			}
			return actionFixtureResults(key, fixture.states[key]), nil
		}
		if strings.Contains(sql, "update external_actions set status=?") {
			status, _ := statements[0].Args[0].(string)
			key, _ := statements[0].Args[4].(string)
			if fixture.states[key] == "applying" {
				fixture.states[key] = status
			}
			return actionFixtureResults(key, fixture.states[key]), nil
		}
		return nil, errors.New("unexpected external action SQL")
	}}
	service, _ := testService(t, db)
	persistence, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatalf("NewRQLiteExternalActions: %v", err)
	}
	return persistence, fixture
}

func actionFixtureResults(key, state string) []rqlite.Result {
	return []rqlite.Result{{}, {Rows: []map[string]any{{"action_id": "action-" + key, "status": state}}}}
}

func TestExternalActionCrashBoundariesPostAtMostOnce(t *testing.T) {
	for _, test := range []struct {
		point     ExternalActionCrashPoint
		wantPosts int
		wantState string
	}{
		{CrashBeforeAttemptMarker, 1, "succeeded"},
		{CrashAfterAttemptMarker, 0, "unknown"},
		{CrashAfterProviderPost, 1, "unknown"},
		{CrashBeforeResultCommit, 1, "unknown"},
	} {
		t.Run(string(test.point), func(t *testing.T) {
			persistence, _ := newDurableExternalActionPersistence(t, true)
			sender := &countingExternalSender{}
			executor := NewExternalActionExecutor(persistence, sender)
			command := ExternalActionCommand{
				Type: "wb.create-room", ResourceID: "alice", ActionKey: "action-1",
				WorkerID: "panel-a", LeaseToken: "lease-1", LeaseFence: 7,
				Request: []byte(`{"login":"alice"}`),
			}
			crashed := false
			hook := func(actual ExternalActionCrashPoint) error {
				if !crashed && actual == test.point {
					crashed = true
					return errors.New("crash")
				}
				return nil
			}
			_, _ = executor.Execute(context.Background(), command, hook)
			result, err := executor.Execute(context.Background(), command, nil)
			if err != nil {
				t.Fatalf("takeover Execute: %v", err)
			}
			if result.State != test.wantState {
				t.Fatalf("takeover state = %q, want %q", result.State, test.wantState)
			}
			if sender.posts != test.wantPosts {
				t.Fatalf("provider POSTs = %d after %s, want %d", sender.posts, test.point, test.wantPosts)
			}
		})
	}
}

func TestExternalActionStaleLeaseCannotSend(t *testing.T) {
	persistence, _ := newDurableExternalActionPersistence(t, false)
	sender := &countingExternalSender{}
	executor := NewExternalActionExecutor(persistence, sender)
	_, err := executor.Execute(context.Background(), ExternalActionCommand{
		Type: "wb.create-room", ResourceID: "alice", ActionKey: "action-1",
		WorkerID: "stale", LeaseToken: "old", LeaseFence: 1, Request: []byte(`{}`),
	}, nil)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Execute error = %v, want ErrLeaseLost", err)
	}
	if sender.posts != 0 {
		t.Fatalf("stale lease sent %d provider POSTs", sender.posts)
	}
}

func TestExternalActionReplacementUsesNewKey(t *testing.T) {
	persistence, _ := newDurableExternalActionPersistence(t, true)
	sender := &countingExternalSender{}
	executor := NewExternalActionExecutor(persistence, sender)
	first := ExternalActionCommand{
		Type: "wb.create-room", ResourceID: "alice", ActionKey: "action-1",
		WorkerID: "panel-a", LeaseToken: "lease-1", LeaseFence: 1, Request: []byte(`{}`),
	}
	if _, err := executor.Execute(context.Background(), first, nil); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	replacement := first
	replacement.ActionKey = "action-2"
	replacement.ReplacesActionKey = first.ActionKey
	replacement.LeaseFence = 2
	if _, err := executor.Execute(context.Background(), replacement, nil); err != nil {
		t.Fatalf("replacement Execute: %v", err)
	}
	if sender.posts != 2 {
		t.Fatalf("provider POSTs = %d, want one for each distinct action key", sender.posts)
	}
}
