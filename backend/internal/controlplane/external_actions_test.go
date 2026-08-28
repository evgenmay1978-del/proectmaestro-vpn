package controlplane

import (
	"context"
	"errors"
	"testing"
)

type memoryExternalActions struct {
	states     map[string]ExternalActionResult
	staleLease bool
}

func newMemoryExternalActions() *memoryExternalActions {
	return &memoryExternalActions{states: make(map[string]ExternalActionResult)}
}

func (m *memoryExternalActions) Prepare(_ context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	if result, ok := m.states[command.ActionKey]; ok {
		return result, nil
	}
	result := ExternalActionResult{ID: command.ActionKey, State: "pending"}
	m.states[command.ActionKey] = result
	return result, nil
}

func (m *memoryExternalActions) StartAttempt(_ context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	if m.staleLease {
		return ExternalActionResult{}, ErrLeaseLost
	}
	result := m.states[command.ActionKey]
	result.State = "attempt_started"
	m.states[command.ActionKey] = result
	return result, nil
}

func (m *memoryExternalActions) Finish(_ context.Context, command ExternalActionCommand, response []byte) (ExternalActionResult, error) {
	result := m.states[command.ActionKey]
	result.State = "succeeded"
	result.Response = append([]byte(nil), response...)
	m.states[command.ActionKey] = result
	return result, nil
}

func (m *memoryExternalActions) MarkUnknown(_ context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	result := m.states[command.ActionKey]
	result.State = "unknown"
	m.states[command.ActionKey] = result
	return result, nil
}

type countingExternalSender struct{ posts int }

func (s *countingExternalSender) Post(context.Context, []byte) ([]byte, error) {
	s.posts++
	return []byte(`{"room":"wb-1"}`), nil
}

func TestExternalActionCrashBoundariesMemoryModel(t *testing.T) {
	for _, point := range []ExternalActionCrashPoint{
		CrashBeforeAttemptMarker,
		CrashAfterAttemptMarker,
		CrashAfterProviderPost,
		CrashBeforeResultCommit,
	} {
		t.Run(string(point), func(t *testing.T) {
			store := newMemoryExternalActions()
			sender := &countingExternalSender{}
			executor := NewExternalActionExecutor(store, sender)
			command := ExternalActionCommand{Type: "wb.create-room", ResourceID: "alice", ActionKey: "action-1", WorkerID: "panel-a", LeaseToken: "lease-1", Request: []byte(`{"login":"alice"}`)}
			crashed := false
			hook := func(actual ExternalActionCrashPoint) error {
				if !crashed && actual == point {
					crashed = true
					return errors.New("crash")
				}
				return nil
			}
			_, _ = executor.Execute(context.Background(), command, hook)
			_, _ = executor.Execute(context.Background(), command, nil)
			if sender.posts > 1 {
				t.Fatalf("provider POSTs = %d after %s, want at most one", sender.posts, point)
			}
		})
	}
}

func TestExternalActionStaleLeaseMemoryModel(t *testing.T) {
	store := newMemoryExternalActions()
	store.staleLease = true
	sender := &countingExternalSender{}
	executor := NewExternalActionExecutor(store, sender)
	_, err := executor.Execute(context.Background(), ExternalActionCommand{
		Type: "wb.create-room", ResourceID: "alice", ActionKey: "action-1", WorkerID: "stale", LeaseToken: "old", Request: []byte(`{}`),
	}, nil)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Execute error = %v, want ErrLeaseLost", err)
	}
	if sender.posts != 0 {
		t.Fatalf("stale lease sent %d provider POSTs", sender.posts)
	}
}

func TestExternalActionReplacementMemoryModel(t *testing.T) {
	store := newMemoryExternalActions()
	sender := &countingExternalSender{}
	executor := NewExternalActionExecutor(store, sender)
	first := ExternalActionCommand{Type: "wb.create-room", ResourceID: "alice", ActionKey: "action-1", WorkerID: "panel-a", LeaseToken: "lease-1", Request: []byte(`{}`)}
	if _, err := executor.Execute(context.Background(), first, nil); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	replacement := first
	replacement.ActionKey = "action-2"
	replacement.ReplacesActionKey = first.ActionKey
	if _, err := executor.Execute(context.Background(), replacement, nil); err != nil {
		t.Fatalf("replacement Execute: %v", err)
	}
	if sender.posts != 2 {
		t.Fatalf("provider POSTs = %d, want one for each distinct action key", sender.posts)
	}
}
