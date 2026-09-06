package controlplane_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
)

// Only the rqlite HTTP transport is substituted. All setting SQL, constraints,
// API command composition, encryption and subsequent production reads execute.
func TestLegacyRuntimeAPICommandsPreserveProtectedConfigSQLite(t *testing.T) {
	ctx := context.Background()
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatal(err)
	}
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x41}, 32)}, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	clock := s4CanaryClock{value: time.Unix(2_000_000, 0)}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(store, &f5UniqueIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	logins := vkturnconf.AllowedLogins()
	members := make([]controlplane.LegacyRuntimeMember, 0, len(logins))
	for _, login := range logins {
		canonical, err := controlplane.CanonicalLoginKey(login)
		if err != nil {
			t.Fatal(err)
		}
		exact := box.LookupHMAC(controlplane.LegacyExactCustomerLoginHMACDomain, []byte(login))
		source := controlplane.LegacyExactCustomerSourcePrefix + box.LookupHMAC("customer-login", []byte(canonical)) + ":" + exact
		sum := sha256.Sum256([]byte("maestro-legacy-v1\x00customer\x00" + source))
		id := hex.EncodeToString(sum[:])
		digest := sha256.Sum256([]byte("runtime-api-source-row:" + login))
		rowSHA := hex.EncodeToString(digest[:])
		db.must(t, rqlite.Statement{SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) VALUES(?,?,?,'active',3000000,1,2000000,2000000)`, Args: []any{id, login, exact}},
			rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('customer',?,?,?,'active',2000000)`, Args: []any{source, id, rowSHA}})
		members = append(members, controlplane.LegacyRuntimeMember{Login: login, CustomerSourceKey: source, CustomerID: id, CustomerSHA256: rowSHA, LoginHMAC: exact})
	}
	olc := olcconf.Config{Enabled: true, Provider: "telemost", Transport: "vp8channel", Logins: append([]string(nil), logins...), Rooms: map[string]olcconf.RoomKey{}}
	vk := vkturnconf.Config{Enabled: true, MinVersionCode: 107, Server: "turn.example.test:443", VKHashes: []string{"fixture-vk-hash"}, Clients: map[string]vkturnconf.Client{}}
	for _, login := range logins {
		olc.Rooms[login] = olcconf.RoomKey{Room: "https://telemost.yandex.ru/j/fixture-" + login, Key: strings.Repeat("a", 64)}
		vk.Clients[login] = vkturnconf.Client{Password: "fixture-password-" + login, WG: subgen.VKTurnCreds{PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), LocalAddress: "10.8.0.2/32"}}
	}
	for key, config := range map[string]any{"olcrtc": olc, "vkturn": vk} {
		raw, _ := json.Marshal(config)
		doc := controlplane.LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: key, CapsuleSHA256: strings.Repeat("a", 64), CustomersSHA256: strings.Repeat("b", 64), ConfigJSON: raw, Members: append([]controlplane.LegacyRuntimeMember(nil), members...)}
		for index := range doc.Members {
			doc.Members[index].MemberHMAC = box.LookupHMAC("setting-member:"+key, []byte(doc.Members[index].Login))
		}
		seedRuntimeAPIArchive(t, db, box, doc)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES('runtime-api','Runtime API fixture',0,1,2000000)`},
		rqlite.Statement{SQL: `INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix) VALUES('runtime-api','s3-olcrtc',1,1,0,0,2000000)`})
	archive := db.must(t, rqlite.Statement{SQL: `SELECT * FROM imported_secrets ORDER BY secret_id`})
	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{Now: clock.Now})
	beforeGeneric := db.must(t, rqlite.Statement{SQL: `SELECT * FROM cluster_settings WHERE setting_key='olcrtc'`}, rqlite.Statement{SQL: `SELECT * FROM setting_secrets WHERE setting_key='olcrtc'`})
	if _, err := business.UpdateSetting(ctx, api.UpdateSettingCommand{Key: "olcrtc", Value: json.RawMessage(`{"enabled":false}`), ExpectedVersion: 1, IdempotencyKey: "generic-native-olc"}); err == nil {
		t.Fatal("generic native OLC bypass accepted")
	}
	if !reflect.DeepEqual(beforeGeneric, db.must(t, rqlite.Statement{SQL: `SELECT * FROM cluster_settings WHERE setting_key='olcrtc'`}, rqlite.Statement{SQL: `SELECT * FROM setting_secrets WHERE setting_key='olcrtc'`})) {
		t.Fatal("generic native OLC bypass changed protected state")
	}
	room, err := business.SetOLCRTCRoom(ctx, api.SetOLCRTCRoomCommand{Login: "wapmix", Room: "updated-wb-room", Provider: "wbstream", ExpectedVersion: 1, IdempotencyKey: "runtime-room"})
	if err != nil || room.Version != 2 {
		t.Fatalf("room command: %v", err)
	}
	if _, err := business.SetOLCRTCGrant(ctx, api.SetOLCRTCGrantCommand{Login: "wapmix", Enabled: false, ExpectedVersion: 2, IdempotencyKey: "runtime-off"}); err != nil {
		t.Fatal(err)
	}
	view, err := business.OLCRTCState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range view.Logins {
		if login == "wapmix" {
			t.Fatal("revoked runtime member still visible")
		}
	}
	if _, err := business.SetOLCRTCGrant(ctx, api.SetOLCRTCGrantCommand{Login: "wapmix", Enabled: true, ExpectedVersion: 3, IdempotencyKey: "runtime-on"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AssignWBRoom(ctx, "wapmix", "provider-wb-room", "runtime-wb"); err != nil {
		t.Fatal(err)
	}
	if err := service.AssignWBRoom(ctx, "wapmix", "provider-wb-room", "runtime-wb"); err != nil {
		t.Fatal(err)
	}
	current, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc")
	if err != nil || current.Generation() != 5 {
		t.Fatalf("WB assignment/retry: %v", err)
	}
	var nextOLC olcconf.Config
	if json.Unmarshal(current.ConfigJSON(), &nextOLC) != nil || nextOLC.Rooms["wapmix"].Room != "provider-wb-room" || nextOLC.Rooms["wapmix"].Key != olc.Rooms["wapmix"].Key || nextOLC.Rooms["wapmixx"] != olc.Rooms["wapmixx"] {
		t.Fatal("runtime room mutation changed protected keys/sibling")
	}

	edit := api.UpdateVKTurnCommand{Value: json.RawMessage(`{"server":"changed.example.test:443","clients":{"wapmix":{"password":"","wg":{"private_key":""}}}}`), IdempotencyKey: "runtime-vk-edit"}
	updated, err := business.UpdateSetting(ctx, api.UpdateSettingCommand{Key: "vkturn", Value: edit.Value, IdempotencyKey: edit.IdempotencyKey})
	if err != nil || updated.Version != 2 {
		t.Fatalf("VK edit: %v", err)
	}
	restarted, err := controlplane.NewService(store, &f5UniqueIDs{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := api.NewServiceBusiness(restarted, api.ServiceBusinessConfig{}).UpdateVKTurn(ctx, edit)
	if err != nil || !reflect.DeepEqual(replayed, updated) {
		t.Fatalf("VK restart retry: %v", err)
	}
	if _, err := business.SetVKTurnEnabled(ctx, api.SetVKTurnEnabledCommand{Enabled: false, ExpectedVersion: 2, IdempotencyKey: "runtime-vk-off"}); err != nil {
		t.Fatal(err)
	}
	current, err = service.ReadLegacyRuntimeSetting(ctx, "vkturn")
	if err != nil || current.Generation() != 3 {
		t.Fatalf("VK read: %v", err)
	}
	var nextVK vkturnconf.Config
	if json.Unmarshal(current.ConfigJSON(), &nextVK) != nil || nextVK.Validate() != nil || nextVK.Enabled || nextVK.Server != "changed.example.test:443" || !reflect.DeepEqual(nextVK.Clients, vk.Clients) || !reflect.DeepEqual(nextVK.VKHashes, vk.VKHashes) {
		t.Fatal("VK edit/toggle lost credentials or transport fields")
	}
	for _, response := range []api.SettingView{room, updated, replayed} {
		if strings.Contains(string(response.Value), strings.Repeat("a", 64)) || strings.Contains(string(response.Value), "fixture-password") || strings.Contains(string(response.Value), "private_key") {
			t.Fatal("runtime mutation response exposed protected material")
		}
	}
	if !reflect.DeepEqual(archive, db.must(t, rqlite.Statement{SQL: `SELECT * FROM imported_secrets ORDER BY secret_id`})) {
		t.Fatal("runtime mutation replaced original archive")
	}
	counts := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM outbox_events WHERE service_name='s3-olcrtc'`}, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM audit_events WHERE action LIKE 'setting.olcrtc.%' OR action LIKE 'setting.vkturn.%'`})
	for index, want := range []float64{4, 6} {
		if counts[index].Rows[0]["n"] != want {
			t.Fatal("runtime mutation/retry was not recorded exactly once")
		}
	}
	// An immutable native marker with a missing current secret must never become
	// an ordinary room update that silently creates a replacement setting.
	db.must(t, rqlite.Statement{SQL: `DELETE FROM setting_secrets WHERE setting_key='olcrtc'`})
	if _, err := business.SetOLCRTCRoom(ctx, api.SetOLCRTCRoomCommand{Login: "wapmix", Room: "must-not-write", Provider: "wbstream", ExpectedVersion: 5, IdempotencyKey: "broken-native"}); err == nil {
		t.Fatal("broken native state fell back to ordinary mutation")
	}
	if err := service.AssignWBRoom(ctx, "wapmix", "must-not-write", "broken-native-wb"); err == nil {
		t.Fatal("broken native WB state fell back to ordinary mutation")
	}
	if !reflect.DeepEqual(counts, db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM outbox_events WHERE service_name='s3-olcrtc'`}, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM audit_events WHERE action LIKE 'setting.olcrtc.%' OR action LIKE 'setting.vkturn.%'`})) {
		t.Fatal("rejected runtime mutation wrote state")
	}
}

func seedRuntimeAPIArchive(t *testing.T, db *s4CanarySQLite, box *controlplane.SecretBox, doc controlplane.LegacyRuntimeSettingDocument) {
	t.Helper()
	key := doc.SettingKey
	plain, _ := json.Marshal(doc)
	envelope, err := box.Seal(controlplane.SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, plain)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(envelope)
	sum := sha256.Sum256(plain)
	digest := hex.EncodeToString(sum[:])
	id := "runtime-setting-v1:" + key + ":" + doc.CapsuleSHA256
	source, _ := json.Marshal(map[string]any{"secret_id": id, "owner_type": "setting", "owner_source_key": key, "field": "secret", "kind": key, "key_version": envelope.KeyVersion, "nonce_b64": base64.StdEncoding.EncodeToString(envelope.Nonce), "ciphertext_b64": base64.StdEncoding.EncodeToString(envelope.Ciphertext), "sha256": digest})
	sourceSum := sha256.Sum256(source)
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix) VALUES(?,'{}',1,2000000)`, Args: []any{key}},
		rqlite.Statement{SQL: `INSERT INTO setting_secrets(setting_key,secret_envelope,secret_sha256,key_version,updated_at_unix) VALUES(?,?,?,1,2000000)`, Args: []any{key, base64.StdEncoding.EncodeToString(raw), digest}},
		rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix) VALUES(?,'setting',?,'secret',?,1,?,?,2000000)`, Args: []any{id, key, key, string(source), digest}},
		rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('encrypted_secret',?,?,?,'active',2000000)`, Args: []any{id, id, hex.EncodeToString(sourceSum[:])}},
	)
	for _, member := range doc.Members {
		db.must(t, rqlite.Statement{SQL: `INSERT INTO setting_members(setting_key,member_key,member_value_json,generation) VALUES(?,?,'{"enabled":true}',1)`, Args: []any{key, member.MemberHMAC}})
	}
}
