package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type wbActionRunnerSpy struct {
	calls    int
	command  controlplane.ExternalActionCommand
	workerID string
	sender   controlplane.ExternalActionSender
	result   controlplane.ExternalActionResult
	err      error
}

func (s *wbActionRunnerSpy) ExecuteExternalAction(_ context.Context, command controlplane.ExternalActionCommand, workerID string, sender controlplane.ExternalActionSender) (controlplane.ExternalActionResult, error) {
	s.calls++
	s.command = command
	s.workerID = workerID
	s.sender = sender
	return s.result, s.err
}

type wbSenderStub struct{}

func (wbSenderStub) Post(context.Context, []byte) ([]byte, error) { return nil, nil }

type wbRoomAssignerSpy struct {
	calls, login, room, idempotency string
}

func (s *wbRoomAssignerSpy) AssignWBRoom(_ context.Context, login, room, idempotency string) error {
	s.calls = "called"
	s.login = login
	s.room = room
	s.idempotency = idempotency
	return nil
}

func TestServiceBusinessRequestWBRoomExecutesDurableProvider(t *testing.T) {
	runner := &wbActionRunnerSpy{result: controlplane.ExternalActionResult{
		ID: "wb-action-1", State: "succeeded", Response: []byte(`{"room":" room-1 "}`),
	}}
	sender := wbSenderStub{}
	assigner := &wbRoomAssignerSpy{}
	business := &ServiceBusiness{
		externalActions: runner,
		wbSender:        sender,
		workerID:        "panel-s2",
		wbRooms:         assigner,
	}
	view, err := business.RequestWBRoom(context.Background(), RequestWBRoomCommand{
		Login: " Alice ", ActionKey: "wb-action-key-1", ReplacesActionKey: "wb-action-key-0", IdempotencyKey: "wb-idempotency-1",
	})
	if err != nil {
		t.Fatalf("RequestWBRoom: %v", err)
	}
	if runner.calls != 1 || runner.workerID != "panel-s2" || runner.sender != sender {
		t.Fatalf("runner calls=%d worker=%q sender=%T", runner.calls, runner.workerID, runner.sender)
	}
	if runner.command.Type != "wb.room" || runner.command.ResourceID != "alice" || runner.command.ActionKey != "wb-action-key-1" || runner.command.ReplacesActionKey != "wb-action-key-0" {
		t.Fatalf("external action command = %#v", runner.command)
	}
	if runner.command.ReplayResourceID != " Alice " || string(runner.command.ReplayRequest) != `{"login":" Alice "}` {
		t.Fatalf("external action compatibility alias = (%q,%q)", runner.command.ReplayResourceID, runner.command.ReplayRequest)
	}
	var request map[string]string
	if err := json.Unmarshal(runner.command.Request, &request); err != nil || request["login"] != "alice" {
		t.Fatalf("provider request=%q err=%v", runner.command.Request, err)
	}
	if view.ID != "wb-action-1" || view.State != "succeeded" || view.Room != "room-1" {
		t.Fatalf("view = %#v", view)
	}
	if assigner.calls != "called" || assigner.login != "alice" || assigner.room != "room-1" || assigner.idempotency != "wb-idempotency-1" {
		t.Fatalf("room assignment = %#v", assigner)
	}
}

func TestServiceBusinessRequestWBRoomRejectsMalformedSucceededResponse(t *testing.T) {
	tests := []struct {
		name     string
		response []byte
	}{
		{name: "invalid json", response: []byte(`{`)},
		{name: "empty object", response: []byte(`{}`)},
		{name: "null", response: []byte(`null`)},
		{name: "wrong shape", response: []byte(`{"room":123}`)},
		{name: "blank room", response: []byte(`{"room":"   "}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &wbActionRunnerSpy{result: controlplane.ExternalActionResult{
				ID: "wb-action-1", State: "succeeded", Response: test.response,
			}}
			assigner := &wbRoomAssignerSpy{}
			business := &ServiceBusiness{
				externalActions: runner,
				wbSender:        wbSenderStub{},
				workerID:        "panel-s2",
				wbRooms:         assigner,
			}
			_, err := business.RequestWBRoom(context.Background(), RequestWBRoomCommand{
				Login: "alice", ActionKey: "wb-action-key-1", IdempotencyKey: "wb-idempotency-1",
			})
			if err == nil || !errors.Is(err, controlplane.ErrUnavailable) {
				t.Fatalf("RequestWBRoom error = %v, want ErrUnavailable", err)
			}
			if assigner.calls != "" {
				t.Fatalf("AssignWBRoom called for malformed response: %#v", assigner)
			}
		})
	}
}

func TestServiceBusinessRequestWBRoomDoesNotAssignNonSucceeded(t *testing.T) {
	for _, state := range []string{"pending", "unknown"} {
		t.Run(state, func(t *testing.T) {
			runner := &wbActionRunnerSpy{result: controlplane.ExternalActionResult{
				ID: "wb-action-1", State: state, Response: []byte(`{"room":"room-1"}`),
			}}
			assigner := &wbRoomAssignerSpy{}
			business := &ServiceBusiness{
				externalActions: runner,
				wbSender:        wbSenderStub{},
				workerID:        "panel-s2",
				wbRooms:         assigner,
			}
			view, err := business.RequestWBRoom(context.Background(), RequestWBRoomCommand{
				Login: "alice", ActionKey: "wb-action-key-1", IdempotencyKey: "wb-idempotency-1",
			})
			if err != nil {
				t.Fatalf("RequestWBRoom: %v", err)
			}
			if view.State != state || view.Room != "" {
				t.Fatalf("view = %#v", view)
			}
			if assigner.calls != "" {
				t.Fatalf("AssignWBRoom called for %s action: %#v", state, assigner)
			}
		})
	}
}
