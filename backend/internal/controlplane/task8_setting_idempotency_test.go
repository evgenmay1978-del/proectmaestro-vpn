package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestValidSettingUpdateRequiresExactCanonicalOLCRTCTargetPayloads(t *testing.T) {
	valid := SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: 1, PublicValueJSON: "{}",
		TargetMembers: []string{"alice"}, TargetPayloads: map[string]string{"alice": "{\"room\":\"room-alice\"}"},
	}
	if !validSettingUpdate(valid) {
		t.Fatal("exact canonical OLCRTC target payload set was rejected")
	}
	tests := []struct {
		name   string
		update SettingUpdate
	}{
		{name: "missing", update: SettingUpdate{
			Key: "olcrtc", ExpectedGeneration: 1, PublicValueJSON: "{}",
			TargetMembers: []string{"alice"},
		}},
		{name: "extra", update: SettingUpdate{
			Key: "olcrtc", ExpectedGeneration: 1, PublicValueJSON: "{}",
			TargetMembers:  []string{"alice"},
			TargetPayloads: map[string]string{"alice": "{}", "bob": "{}"},
		}},
		{name: "non-canonical-key", update: SettingUpdate{
			Key: "olcrtc", ExpectedGeneration: 1, PublicValueJSON: "{}",
			TargetMembers: []string{"alice"}, TargetPayloads: map[string]string{"Alice": "{}"},
		}},
		{name: "duplicate-canonical-target", update: SettingUpdate{
			Key: "olcrtc", ExpectedGeneration: 1, PublicValueJSON: "{}",
			TargetMembers: []string{"alice", "Alice"}, TargetPayloads: map[string]string{"alice": "{}"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if validSettingUpdate(test.update) {
				t.Fatalf("invalid OLCRTC target payload set accepted: %#v", test.update)
			}
		})
	}
}

func TestTask8SettingMutationIsIdempotentAndPublishesOLCRTC(t *testing.T) {
	db := &recordingRQLite{
		linear: []scriptedResult{resultsScript(rqlite.Result{})},
	}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) == 0 || len(statements[0].Args) < 3 {
			return nil, errors.New("test: missing setting request hash")
		}
		requestHash, ok := statements[0].Args[2].(string)
		if !ok || requestHash == "" {
			return nil, errors.New("test: missing setting request hash")
		}
		results := make([]rqlite.Result, len(statements))
		results[len(results)-1] = rqlite.Result{Rows: []map[string]any{{
			"request_hash": requestHash, "status": "applied", "response_json": "{\"generation\":4}",
		}}}
		return results, nil
	}
	service, _ := testService(t, db)
	result, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: 3, PublicValueJSON: `{"room":"room-1"}`,
		Actor: "owner", CommandType: "setting.olcrtc.room", IdempotencyKey: "idem-room-1",
		TargetMembers: []string{"alice"}, TargetPayloads: map[string]string{"alice": `{"room":"room-1"}`},
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
	db := &recordingRQLite{
		linear: []scriptedResult{
			resultsScript(
				rqlite.Result{Rows: []map[string]any{{"public_value_json": `{}`, "generation": int64(3)}}},
				rqlite.Result{}, rqlite.Result{},
			),
			resultsScript(rqlite.Result{}),
		},
	}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) == 0 || len(statements[0].Args) < 3 {
			return nil, errors.New("test: missing setting request hash")
		}
		requestHash, ok := statements[0].Args[2].(string)
		if !ok || requestHash == "" {
			return nil, errors.New("test: missing setting request hash")
		}
		results := make([]rqlite.Result, len(statements))
		results[len(results)-1] = rqlite.Result{Rows: []map[string]any{{
			"request_hash": requestHash, "status": "applied", "response_json": `{"generation":4}`,
		}}}
		return results, nil
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

func TestAssignWBRoomAliceThenBobKeepsRoomsIsolated(t *testing.T) {
	const aliceState = `{"rooms":{"Alice":{"provider":"wbstream","room":"room-alice"}}}`
	db := &recordingRQLite{linear: []scriptedResult{
		resultsScript(rqlite.Result{}, rqlite.Result{}, rqlite.Result{}),
		resultsScript(rqlite.Result{}),
		resultsScript(
			rqlite.Result{Rows: []map[string]any{{"public_value_json": aliceState, "generation": int64(1)}}},
			rqlite.Result{Rows: []map[string]any{{"member_key": "alice", "member_value_json": `{"enabled":true}`}}},
			rqlite.Result{},
		),
		resultsScript(rqlite.Result{}),
	}}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) == 0 || len(statements[0].Args) < 3 {
			return nil, errors.New("test: missing final setting response query")
		}
		requestHash, ok := statements[0].Args[2].(string)
		if !ok || requestHash == "" {
			return nil, errors.New("test: missing setting request hash")
		}
		results := make([]rqlite.Result, len(statements))
		results[len(results)-1] = rqlite.Result{Rows: []map[string]any{{
			"request_hash": requestHash, "status": "applied", "response_json": `{"generation":1}`,
		}}}
		return results, nil
	}
	service, secrets := testService(t, db)
	for _, assignment := range []struct{ login, room, key string }{
		{login: "alice", room: "room-alice", key: "wb-room-alice"},
		{login: "bob", room: "room-bob", key: "wb-room-bob"},
	} {
		if err := service.AssignWBRoom(context.Background(), assignment.login, assignment.room, assignment.key); err != nil {
			t.Fatalf("AssignWBRoom(%s): %v", assignment.login, err)
		}
	}
	if len(db.requestCalls) != 2 {
		t.Fatalf("room assignment transactions = %d, want 2", len(db.requestCalls))
	}
	type roomView struct {
		Provider string `json:"provider"`
		Room     string `json:"room"`
	}
	type stateView struct {
		Rooms map[string]roomView `json:"rooms"`
	}
	for index, call := range db.requestCalls {
		var publicValue string
		desired := make([]rqlite.Statement, 0, 2)
		for _, statement := range call.statements {
			lowerSQL := strings.ToLower(statement.SQL)
			if strings.Contains(lowerSQL, "insert into cluster_settings") && len(statement.Args) > 1 {
				publicValue, _ = statement.Args[1].(string)
			}
			if strings.Contains(lowerSQL, "insert into desired_node_state") {
				desired = append(desired, statement)
			}
		}
		var state stateView
		if err := json.Unmarshal([]byte(publicValue), &state); err != nil {
			t.Fatalf("transaction %d state %q: %v", index, publicValue, err)
		}
		if state.Rooms["alice"].Room != "room-alice" || state.Rooms["alice"].Provider != "wbstream" {
			t.Fatalf("transaction %d lost Alice room: %#v", index, state.Rooms)
		}
		if index == 0 && len(state.Rooms) != 1 {
			t.Fatalf("Alice state contains another room: %#v", state.Rooms)
		}
		if index == 1 && (len(state.Rooms) != 2 || state.Rooms["bob"].Room != "room-bob" || state.Rooms["bob"].Provider != "wbstream") {
			t.Fatalf("Bob state overwrote or lost a room: %#v", state.Rooms)
		}
		if len(desired) != 1 || len(desired[0].Args) < 6 {
			t.Fatalf("transaction %d desired writes = %d, want one isolated target", index, len(desired))
		}
		login := []string{"alice", "bob"}[index]
		wantTarget := secrets.LookupHMAC("customer-login", []byte(login))
		if gotTarget, _ := desired[0].Args[5].(string); gotTarget != wantTarget {
			t.Fatalf("transaction %d target = %q, want only %s", index, gotTarget, login)
		}
		encodedEnvelope, ok := desired[0].Args[1].([]byte)
		if !ok {
			t.Fatalf("transaction %d envelope type = %T", index, desired[0].Args[1])
		}
		var envelope Envelope
		if err := json.Unmarshal(encodedEnvelope, &envelope); err != nil {
			t.Fatalf("transaction %d envelope JSON: %v", index, err)
		}
		plaintext, err := secrets.Open(SecretScope{
			OwnerType: "setting", OwnerID: "olcrtc", Field: "desired", Kind: "s3-olcrtc",
		}, envelope)
		if err != nil {
			t.Fatalf("transaction %d open desired envelope: %v", index, err)
		}
		var isolated roomView
		if err := json.Unmarshal(plaintext, &isolated); err != nil {
			t.Fatalf("transaction %d desired plaintext %q: %v", index, plaintext, err)
		}
		wantRoom := []string{"room-alice", "room-bob"}[index]
		otherRoom := []string{"room-bob", "room-alice"}[index]
		if isolated.Room != wantRoom || isolated.Provider != "wbstream" {
			t.Fatalf("transaction %d desired plaintext = %#v, want only %s", index, isolated, wantRoom)
		}
		if strings.Contains(string(plaintext), otherRoom) {
			t.Fatalf("transaction %d desired plaintext leaked %s: %s", index, otherRoom, plaintext)
		}
	}
}

func TestAssignWBRoomRejectsAmbiguousLegacyGlobalRoom(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"public_value_json": `{"room":"legacy-room","provider":"wbstream"}`, "generation": int64(1)}}},
		rqlite.Result{Rows: []map[string]any{
			{"member_key": "member-hmac-1", "member_value_json": `{"enabled":true}`},
			{"member_key": "member-hmac-2", "member_value_json": `{"enabled":true}`},
		}},
		rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	err := service.AssignWBRoom(context.Background(), "alice", "room-alice", "legacy-ambiguous")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous legacy assignment error = %v, want ErrConflict", err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("ambiguous legacy assignment mutated state: %#v", db.requestCalls)
	}
}

func TestAssignWBRoomMigratesSingleMatchingLegacyRoom(t *testing.T) {
	db := &recordingRQLite{}
	service, secrets := testService(t, db)
	memberHMAC := secrets.LookupHMAC("setting-member:olcrtc", []byte("alice"))
	db.linear = []scriptedResult{
		resultsScript(
			rqlite.Result{Rows: []map[string]any{{"public_value_json": `{"room":"legacy-room","provider":"wbstream"}`, "generation": int64(1)}}},
			rqlite.Result{Rows: []map[string]any{{"member_key": memberHMAC, "member_value_json": `{"enabled":true}`}}},
			rqlite.Result{},
		),
		resultsScript(rqlite.Result{}),
	}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) == 0 || len(statements[0].Args) < 3 {
			return nil, errors.New("test: missing setting request hash")
		}
		requestHash, ok := statements[0].Args[2].(string)
		if !ok || requestHash == "" {
			return nil, errors.New("test: missing setting request hash")
		}
		results := make([]rqlite.Result, len(statements))
		results[len(results)-1] = rqlite.Result{Rows: []map[string]any{{
			"request_hash": requestHash, "status": "applied", "response_json": `{"generation":2}`,
		}}}
		return results, nil
	}
	if err := service.AssignWBRoom(context.Background(), "Alice", "room-alice", "legacy-single"); err != nil {
		t.Fatalf("single legacy migration: %v", err)
	}
	var publicValue string
	for _, statement := range db.requestCalls[0].statements {
		if strings.Contains(strings.ToLower(statement.SQL), "insert into cluster_settings") && len(statement.Args) > 1 {
			publicValue, _ = statement.Args[1].(string)
		}
	}
	var state struct {
		Rooms map[string]struct {
			Room     string
			Provider string
		}
	}
	if err := json.Unmarshal([]byte(publicValue), &state); err != nil {
		t.Fatalf("migrated state %q: %v", publicValue, err)
	}
	if len(state.Rooms) != 1 || state.Rooms["alice"].Room != "room-alice" || state.Rooms["alice"].Provider != "wbstream" {
		t.Fatalf("migrated state = %#v", state.Rooms)
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
