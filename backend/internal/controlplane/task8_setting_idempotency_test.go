package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestTask8SettingMutationIsIdempotentAndPublishesOLCRTC(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{resultsScript(rqlite.Result{})},
		requests: []scriptedResult{resultsScript(
			rqlite.Result{},
			rqlite.Result{Rows: []map[string]any{{"generation": int64(4)}}},
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
			rqlite.Result{}, rqlite.Result{},
			rqlite.Result{Rows: []map[string]any{{
				"request_hash": "8f8bc4d8545eca430bf6ac1a6508ad2a5d72c0cf25dca9b10d916ae847c9e31e", "status": "applied", "response_json": `{"generation":4}`,
			}}},
		)}}
	service, _ := testService(t, db)
	result, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: 3, PublicValueJSON: `{"room":"room-1"}`,
		Actor: "owner", CommandType: "setting.olcrtc.room", IdempotencyKey: "idem-room-1",
		TargetMembers: []string{"alice"},
	})
	if err != nil {
		t.Fatalf("UpdateSetting: %v", err)
	}
	if result.Generation != 4 {
		t.Fatalf("generation = %d, want 4", result.Generation)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("setting mutation calls = %#v, want one transaction", db.requestCalls)
	}
	joined := strings.ToLower(joinedRequestSQL(db))
	for _, fragment := range []string{
		"idempotency_requests", "request_hash", "response_json", "status='applied'",
		"desired_node_state", "outbox_events", "s3-olcrtc",
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("setting transaction missing %q: %s", fragment, joined)
		}
	}
	if !statementsHaveArg(db.requestCalls[0].statements, "idem-room-1") {
		t.Fatal("setting transaction omitted the idempotency key")
	}
}

func TestAssignWBRoomUsesCanonicalOLCRTCTransaction(t *testing.T) {
	update := SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: 3,
		PublicValueJSON: `{"provider":"wbstream","room":"room-1"}`,
		Members:         []string{"alice"}, Actor: "panel",
		CommandType: "setting.olcrtc.wbroom", IdempotencyKey: "wb-idempotency-1",
		TargetMembers: []string{"alice"},
	}
	requestHash, err := settingRequestHash(update)
	if err != nil {
		t.Fatalf("setting request hash: %v", err)
	}
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(
				rqlite.Result{Rows: []map[string]any{{"public_value_json": `{}`, "generation": int64(3)}}},
				rqlite.Result{}, rqlite.Result{},
			),
			resultsScript(rqlite.Result{}),
		},
		requests: []scriptedResult{resultsScript(
			rqlite.Result{},
			rqlite.Result{Rows: []map[string]any{{"generation": int64(4)}}},
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
			rqlite.Result{}, rqlite.Result{},
			rqlite.Result{Rows: []map[string]any{{
				"request_hash": requestHash, "status": "applied", "response_json": `{"generation":4}`,
			}}},
		)},
	}
	service, _ := testService(t, db)
	if err := service.AssignWBRoom(context.Background(), "alice", "room-1", "wb-idempotency-1"); err != nil {
		t.Fatalf("AssignWBRoom: %v", err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("room assignment calls = %#v, want one transaction", db.requestCalls)
	}
	joined := strings.ToLower(joinedRequestSQL(db))
	for _, fragment := range []string{"cluster_settings", "idempotency_requests", "desired_node_state", "outbox_events", "s3-olcrtc"} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("room assignment transaction missing %q: %s", fragment, joined)
		}
	}
	if !statementsHaveArg(db.requestCalls[0].statements, "wb-idempotency-1") {
		t.Fatal("room assignment transaction omitted idempotency key")
	}
}

func TestTask8SettingReplayReturnsSavedResponseWithoutMutation(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(map[string]any{
		"request_hash": expectedSettingRequestHash(t, SettingUpdate{
			Key: "vkturn", ExpectedGeneration: 5, PublicValueJSON: `{"enabled":true}`,
			Actor: "owner", CommandType: "setting.vkturn.enable", IdempotencyKey: "idem-vk-1",
		}),
		"status": "applied", "response_json": `{"generation":6}`,
	})}}
	service, _ := testService(t, db)
	result, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: "vkturn", ExpectedGeneration: 5, PublicValueJSON: `{"enabled":true}`,
		Actor: "owner", CommandType: "setting.vkturn.enable", IdempotencyKey: "idem-vk-1",
	})
	if err != nil {
		t.Fatalf("UpdateSetting replay: %v", err)
	}
	if result.Generation != 6 || len(db.requestCalls) != 0 {
		t.Fatalf("replay result/calls = %#v/%d, want saved generation and no mutation", result, len(db.requestCalls))
	}
}

func TestTask8SettingReusedKeyWithDifferentHashConflicts(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(map[string]any{
		"request_hash": "different", "status": "applied", "response_json": `{"generation":6}`,
	})}}
	service, _ := testService(t, db)
	_, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: "vkturn", ExpectedGeneration: 5, PublicValueJSON: `{"enabled":true}`,
		Actor: "owner", CommandType: "setting.vkturn.enable", IdempotencyKey: "idem-vk-1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateSetting error = %v, want ErrConflict", err)
	}
}

func statementsHaveArg(statements []rqlite.Statement, want string) bool {
	for _, statement := range statements {
		for _, arg := range statement.Args {
			if value, ok := arg.(string); ok && value == want {
				return true
			}
		}
	}
	return false
}
