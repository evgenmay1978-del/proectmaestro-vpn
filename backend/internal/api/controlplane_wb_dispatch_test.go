package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type wbActionRunnerSpy struct {
	calls    int
	command  controlplane.ExternalActionCommand
	workerID string
	sender   controlplane.ExternalActionSender
}

func (s *wbActionRunnerSpy) ExecuteExternalAction(_ context.Context, command controlplane.ExternalActionCommand, workerID string, sender controlplane.ExternalActionSender) (controlplane.ExternalActionResult, error) {
	s.calls++
	s.command = command
	s.workerID = workerID
	s.sender = sender
	return controlplane.ExternalActionResult{ID: "wb-action-1", State: "succeeded", Response: []byte(`{"room":"room-1"}`)}, nil
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
	runner := &wbActionRunnerSpy{}
	sender := wbSenderStub{}
	assigner := &wbRoomAssignerSpy{}
	business := &ServiceBusiness{
		externalActions: runner,
		wbSender:        sender,
		workerID:        "panel-s2",
		wbRooms:         assigner,
	}
	view, err := business.RequestWBRoom(context.Background(), RequestWBRoomCommand{
		Login: "alice", ActionKey: "wb-action-key-1", IdempotencyKey: "wb-idempotency-1",
	})
	if err != nil {
		t.Fatalf("RequestWBRoom: %v", err)
	}
	if runner.calls != 1 || runner.workerID != "panel-s2" || runner.sender != sender {
		t.Fatalf("runner calls=%d worker=%q sender=%T", runner.calls, runner.workerID, runner.sender)
	}
	if runner.command.Type != "wb.room" || runner.command.ResourceID != "alice" || runner.command.ActionKey != "wb-action-key-1" {
		t.Fatalf("external action command = %#v", runner.command)
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
