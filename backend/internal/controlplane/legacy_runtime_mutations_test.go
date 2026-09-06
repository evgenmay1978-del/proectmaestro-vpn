package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func legacyRuntimeAbsentScript() scriptedResult {
	// Conventional fixtures have no native setting, members or source archive.
	return resultsScript(rqlite.Result{}, rqlite.Result{}, rqlite.Result{})
}

func seedRuntimeMutationOLC(t *testing.T) (*customerIntegritySQLite, *Service, LegacyRuntimeSetting, olcconf.Config) {
	t.Helper()
	db, service, config := seedRuntimeMutationOLCRows(t, false)
	value, err := service.ReadLegacyRuntimeSetting(context.Background(), "olcrtc")
	if err != nil {
		t.Fatal(err)
	}
	return db, service, value, config
}

func seedRuntimeMutationOLCRows(t *testing.T, corruptArchive bool) (*customerIntegritySQLite, *Service, olcconf.Config) {
	t.Helper()
	db, service := newCustomerIntegritySQLite(t)
	config := olcconf.Config{Enabled: true, Provider: "telemost", Transport: "vp8channel", Logins: []string{"Alice", "alice"}, Rooms: map[string]olcconf.RoomKey{
		"Alice": {Room: "https://telemost.yandex.ru/j/upper-fixture", Key: strings.Repeat("a", 64)},
		"alice": {Room: "https://telemost.yandex.ru/j/lower-fixture", Key: strings.Repeat("b", 64)},
	}}
	raw, _ := json.Marshal(config)
	doc := LegacyRuntimeSettingDocument{SchemaVersion: 1, SettingKey: "olcrtc", CapsuleSHA256: strings.Repeat("a", 64), CustomersSHA256: strings.Repeat("b", 64), ConfigJSON: raw, Members: []LegacyRuntimeMember{}}
	for _, login := range config.Logins {
		seedExactLoginCustomer(t, db, service, login)
		identity, err := service.ResolveCustomerLogin(context.Background(), login)
		if err != nil {
			t.Fatal(err)
		}
		doc.Members = append(doc.Members, LegacyRuntimeMember{Login: login, CustomerID: identity.CustomerID(), CustomerSourceKey: identity.source, CustomerSHA256: identity.digest, LoginHMAC: identity.LookupHMAC(), MemberHMAC: identity.SettingMemberHMAC("olcrtc")})
	}
	encoded, digest := runtimeDomainCipher(t, service.store.secrets, "olcrtc", doc)
	archiveEncoded := encoded
	if corruptArchive {
		// Corrupt only the source ciphertext before its immutable INSERT. The
		// current target remains valid; the archive metadata hashes this actual
		// corrupted envelope, so the reader must reject its failed AEAD proof.
		raw, err := base64.StdEncoding.DecodeString(encoded)
		var envelope Envelope
		if err != nil || json.Unmarshal(raw, &envelope) != nil || len(envelope.Ciphertext) == 0 {
			t.Fatal("invalid corruption fixture envelope")
		}
		envelope.Ciphertext[len(envelope.Ciphertext)-1] ^= 1
		raw, err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		archiveEncoded = base64.StdEncoding.EncodeToString(raw)
	}
	seedRuntimeDomainMarker(t, db, "olcrtc", doc.CapsuleSHA256, digest, archiveEncoded)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix) VALUES('olcrtc',?,1,1)`, Args: []any{string(legacyRuntimeOLCPublic(config))}},
		rqlite.Statement{SQL: `INSERT INTO setting_secrets(setting_key,secret_envelope,secret_sha256,key_version,updated_at_unix) VALUES('olcrtc',?,?,1,1)`, Args: []any{encoded, digest}},
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES('runtime-node','Runtime fixture',0,1,1)`},
		rqlite.Statement{SQL: `INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix) VALUES('runtime-node','s3-olcrtc',1,1,0,0,1)`})
	for _, member := range doc.Members {
		db.must(t, rqlite.Statement{SQL: `INSERT INTO setting_members(setting_key,member_key,member_value_json,generation) VALUES('olcrtc',?,'{"enabled":true}',1)`, Args: []any{member.MemberHMAC}})
	}
	return db, service, config
}

func TestLegacyRuntimeRoomGrantRestartPreservesArchiveAndKeysSQLite(t *testing.T) {
	db, service, current, original := seedRuntimeMutationOLC(t)
	ctx := context.Background()
	archive := db.must(t, rqlite.Statement{SQL: `SELECT * FROM imported_secrets ORDER BY secret_id`})
	result, _, err := service.SetLegacyOLCRoom(ctx, current, "Alice", "fixture-wb-room", "wbstream", 1, "room-edit", "setting.olcrtc.room")
	if err != nil || result.Generation != 2 {
		t.Fatalf("room edit: %v", err)
	}
	current, err = service.ReadLegacyRuntimeSetting(ctx, "olcrtc")
	if err != nil {
		t.Fatal(err)
	}
	var config olcconf.Config
	if json.Unmarshal(current.ConfigJSON(), &config) != nil || config.Rooms["Alice"].Key != original.Rooms["Alice"].Key || config.Rooms["alice"] != original.Rooms["alice"] {
		t.Fatal("room edit changed a key or sibling")
	}
	restarted, err := NewService(service.store, &sequenceIDs{}, service.clock)
	if err != nil {
		t.Fatal(err)
	}
	replay, _, err := restarted.SetLegacyOLCRoom(ctx, current, "Alice", "fixture-wb-room", "wbstream", 1, "room-edit", "setting.olcrtc.room")
	if err != nil || replay != result {
		t.Fatalf("restart replay: %v", err)
	}
	if _, _, err := restarted.SetLegacyOLCRoom(ctx, current, "Alice", "different-wb-room", "wbstream", 1, "room-edit", "setting.olcrtc.room"); !errors.Is(err, ErrConflict) {
		t.Fatal("same key accepted changed command")
	}
	if _, _, err := restarted.SetLegacyOLCGrant(ctx, current, "Alice", false, 2, "grant-off"); err != nil {
		t.Fatal(err)
	}
	current, err = restarted.ReadLegacyRuntimeSetting(ctx, "olcrtc")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(current.ConfigJSON(), &config) != nil || config.Allowed("Alice") || !config.Allowed("alice") || config.Rooms["Alice"].Key != original.Rooms["Alice"].Key {
		t.Fatal("grant revocation discarded credentials or affected sibling")
	}
	if _, _, err := restarted.SetLegacyOLCGrant(ctx, current, "Alice", true, 3, "grant-on"); err != nil {
		t.Fatal(err)
	}
	current, err = restarted.ReadLegacyRuntimeSetting(ctx, "olcrtc")
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(current.ConfigJSON(), &config) != nil || !config.Allowed("Alice") || config.Rooms["Alice"].Key != original.Rooms["Alice"].Key {
		t.Fatal("grant restore minted a key")
	}
	if !reflect.DeepEqual(archive, db.must(t, rqlite.Statement{SQL: `SELECT * FROM imported_secrets ORDER BY secret_id`})) {
		t.Fatal("runtime mutation rewrote imported archive")
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM outbox_events WHERE service_name='s3-olcrtc'`}, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM audit_events WHERE action LIKE 'setting.olcrtc.%'`})
	for _, result := range rows {
		if n, ok := rowInt64(result.Rows[0], "n"); !ok || n != 3 {
			t.Fatal("mutations did not publish exactly once")
		}
	}
}

func TestLegacyRuntimeStableCustomerRevisionAndCorruptSourceSQLite(t *testing.T) {
	db, service, current, _ := seedRuntimeMutationOLC(t)
	ctx := context.Background()
	id := current.Members()[0].CustomerID
	db.must(t, rqlite.Statement{SQL: `UPDATE customers SET expires_at_unix=expires_at_unix+86400,generation=generation+1 WHERE customer_id=?`, Args: []any{id}}, rqlite.Statement{SQL: `UPDATE imported_entity_state SET canonical_sha256=? WHERE entity_kind='customer' AND target_id=?`, Args: []any{strings.Repeat("f", 64), id}})
	if _, err := service.ReadLegacyRuntimeSetting(ctx, "olcrtc"); err != nil {
		t.Fatalf("ordinary expiry delta invalidated stable runtime identity: %v", err)
	}
	corruptDB, corruptService, _ := seedRuntimeMutationOLCRows(t, true)
	before := corruptDB.snapshot(t)
	if _, err := corruptService.ReadLegacyRuntimeSetting(ctx, "olcrtc"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupt source archive accepted")
	}
	if !reflect.DeepEqual(before, corruptDB.snapshot(t)) {
		t.Fatal("failed source authentication wrote state")
	}
}

func TestLegacyRuntimeMutationStaleGenerationCannotOverwriteSQLite(t *testing.T) {
	db, service, current, _ := seedRuntimeMutationOLC(t)
	ctx := context.Background()
	if _, _, err := service.SetLegacyOLCRoom(ctx, current, "Alice", "first-wb-room", "wbstream", 1, "first", "setting.olcrtc.room"); err != nil {
		t.Fatal(err)
	}
	before := db.snapshot(t)
	if _, _, err := service.SetLegacyOLCRoom(ctx, current, "Alice", "stale-wb-room", "wbstream", 1, "stale", "setting.olcrtc.room"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale mutation: %v", err)
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("stale mutation changed canonical state")
	}
}
