package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func exactSettingSnapshot(t *testing.T, db *customerIntegritySQLite) []rqlite.Result {
	t.Helper()
	return db.must(t,
		rqlite.Statement{SQL: `SELECT * FROM cluster_settings ORDER BY setting_key`},
		rqlite.Statement{SQL: `SELECT * FROM setting_members ORDER BY setting_key,member_key`},
		rqlite.Statement{SQL: `SELECT * FROM desired_node_state ORDER BY customer_id,node_id,service_name`},
		rqlite.Statement{SQL: `SELECT * FROM outbox_events ORDER BY event_id`},
		rqlite.Statement{SQL: `SELECT * FROM idempotency_requests ORDER BY scope,command_type,idempotency_key`},
	)
}

func TestExactLoginWBAndSettingTargetsRemainSeparateSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	upper := seedExactLoginCustomer(t, db, service, "Alice")
	lower := seedExactLoginCustomer(t, db, service, "alice")
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES('exact-setting-node','Exact setting fixture',0,1,1)`},
		rqlite.Statement{SQL: `INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix) VALUES('exact-setting-node','s3-olcrtc',1,1,0,0,1)`},
	)
	for index, login := range []string{"Alice", "alice"} {
		if err := service.AssignWBRoom(ctx, login, []string{"upper-room", "lower-room"}[index], "room-"+login); err != nil {
			t.Fatalf("assign exact room: %v", err)
		}
	}
	setting, err := service.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil || setting.Generation != 2 || len(setting.Members) != 2 {
		t.Fatalf("setting: %v", err)
	}
	var state olcrtcRoomAssignments
	if json.Unmarshal(setting.PublicValueJSON, &state) != nil || state.Rooms["Alice"].Room != "upper-room" || state.Rooms["alice"].Room != "lower-room" {
		t.Fatal("exact room keys collapsed")
	}
	for index, customer := range []Customer{upper, lower} {
		identity, err := service.ResolveCustomerLogin(ctx, []string{"Alice", "alice"}[index])
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := setting.Members[identity.SettingMemberHMAC("olcrtc")]; !ok {
			t.Fatal("exact member HMAC missing")
		}
		result := db.must(t, rqlite.Statement{SQL: `SELECT desired_envelope FROM desired_node_state WHERE customer_id=? AND service_name='s3-olcrtc'`, Args: []any{customer.ID}})
		if len(result[0].Rows) != 1 {
			t.Fatal("desired target missing")
		}
		encoded, _ := rowString(result[0].Rows[0], "desired_envelope")
		raw, decodeErr := base64.StdEncoding.DecodeString(encoded)
		var envelope Envelope
		if decodeErr != nil || json.Unmarshal(raw, &envelope) != nil {
			t.Fatal("invalid desired fixture envelope")
		}
		plain, err := service.store.secrets.Open(SecretScope{OwnerType: "setting", OwnerID: "olcrtc", Field: "desired", Kind: "s3-olcrtc"}, envelope)
		var room olcrtcRoomAssignment
		if err != nil || json.Unmarshal(plain, &room) != nil || room.Room != []string{"upper-room", "lower-room"}[index] {
			t.Fatal("desired room addressed the wrong exact customer")
		}
	}
	before := exactSettingSnapshot(t, db)
	if err := service.AssignWBRoom(ctx, "ALICE", "other-room", "unknown-case"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown casing: %v", err)
	}
	if err := service.AssignWBRoom(ctx, "Alice", "upper-room", "room-Alice"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, exactSettingSnapshot(t, db)) {
		t.Fatal("denial or exact room replay wrote state")
	}
}

func TestExactLoginSettingIdentityDriftRejectsWholeTransactionSQLite(t *testing.T) {
	for _, idempotent := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "idempotent"}[idempotent], func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			customer := seedExactLoginCustomer(t, db, service, "Alice")
			update := SettingUpdate{Key: "device_limit", PublicValueJSON: `{"limit":5}`, Members: []string{"Alice"}, Actor: "fixture"}
			if idempotent {
				update.CommandType, update.IdempotencyKey = "setting.device_limit", "exact-setting"
			}
			before := exactSettingSnapshot(t, db)
			db.beforeRequest = func() {
				db.must(t, rqlite.Statement{SQL: `UPDATE imported_entity_state SET canonical_sha256=? WHERE entity_kind='customer' AND target_id=?`, Args: []any{strings.Repeat("e", 64), customer.ID}})
			}
			if _, err := service.UpdateSetting(context.Background(), update); !errors.Is(err, ErrConflict) {
				t.Fatalf("identity drift: %v", err)
			}
			if !reflect.DeepEqual(before, exactSettingSnapshot(t, db)) {
				t.Fatal("identity CAS failure left partial setting effects")
			}
		})
	}
}

func TestOrdinarySettingStillUsesCanonicalMemberHashSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedIntegrityCustomer(t, service)
	if _, err := service.UpdateSetting(context.Background(), SettingUpdate{Key: "device_limit", PublicValueJSON: `{"limit":5}`, Members: []string{" ExIsTiNg "}, Actor: "fixture"}); err != nil {
		t.Fatal(err)
	}
	setting, err := service.ReadBusinessSetting(context.Background(), "device_limit")
	key := service.store.secrets.LookupHMAC("setting-member:device_limit", []byte("existing"))
	if err != nil || len(setting.Members) != 1 {
		t.Fatalf("ordinary setting: %v", err)
	}
	if _, ok := setting.Members[key]; !ok {
		t.Fatal("ordinary canonical membership changed")
	}
	before := exactSettingSnapshot(t, db)
	if _, err := service.UpdateSetting(context.Background(), SettingUpdate{Key: "olcrtc", PublicValueJSON: `{}`, TargetMembers: []string{"Existing", "existing"}, TargetPayloads: map[string]string{"Existing": `{}`, "existing": `{}`}, CommandType: "setting.olcrtc", IdempotencyKey: "duplicate-target"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate canonical target: %v", err)
	}
	if !reflect.DeepEqual(before, exactSettingSnapshot(t, db)) {
		t.Fatal("duplicate target wrote state")
	}
}

func TestExactLoginWBRejectsUnboundPersistedMemberSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	seedExactLoginCustomer(t, db, service, "Alice")
	if err := service.AssignWBRoom(context.Background(), "Alice", "first-room", "first-room"); err != nil {
		t.Fatal(err)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO setting_members(setting_key,member_key,member_value_json,generation) VALUES('olcrtc',?,'{"enabled":true}',1)`, Args: []any{strings.Repeat("f", 64)}})
	before := exactSettingSnapshot(t, db)
	if err := service.AssignWBRoom(context.Background(), "Alice", "second-room", "second-room"); !errors.Is(err, ErrConflict) {
		t.Fatalf("unbound persisted member: %v", err)
	}
	if !reflect.DeepEqual(before, exactSettingSnapshot(t, db)) {
		t.Fatal("unbound member was silently omitted")
	}
}
