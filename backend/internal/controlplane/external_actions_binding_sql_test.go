package controlplane

import (
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestExternalActionMigrationV8PreservesLegacyRowsAndEnforcesReplacementBindingSQLite(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if SchemaVersion != 8 || len(migrations) != 8 || migrations[7].Version != 8 {
		t.Fatalf("migration chain version/count/tail=(%d,%d,%d), want (8,8,8)", SchemaVersion, len(migrations), migrations[len(migrations)-1].Version)
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for external action migration tests")
	}
	db := &customerIntegritySQLite{python: python, path: filepath.Join(t.TempDir(), "external-actions-v8.sqlite")}
	var prefix []rqlite.Statement
	for _, migration := range migrations[:7] {
		prefix = append(prefix, migration.Statements...)
	}
	db.must(t, prefix...)
	legacyHash := strings.Repeat("a", 64)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,
status,attempts,created_at_unix,updated_at_unix)
VALUES('legacy-action','wb.room','legacy-login','legacy-key',? ,?,'unknown',1,1,1)`,
		Args: []any{[]byte("legacy-request-envelope"), legacyHash}})
	db.must(t, migrations[7].Statements...)

	results := db.must(t,
		rqlite.Statement{SQL: `SELECT action_type,resource_id,idempotency_key,request_sha256,replaces_action_id,
attempt_worker_id,attempt_lease_token,attempt_lease_fence
FROM external_actions WHERE action_id='legacy-action'`},
		rqlite.Statement{SQL: `SELECT "table" AS target_table,"from" AS source_column,"to" AS target_column
FROM pragma_foreign_key_list('external_actions') WHERE "from"='replaces_action_id'`},
		rqlite.Statement{SQL: `SELECT type,name FROM sqlite_master WHERE name IN (
'external_actions_one_replacement','external_actions_binding_immutable','external_actions_replacement_valid_insert') ORDER BY type,name`},
	)
	if len(results[0].Rows) != 1 {
		t.Fatalf("legacy row results=%#v", results[0].Rows)
	}
	legacy := results[0].Rows[0]
	if legacy["action_type"] != "wb.room" || legacy["resource_id"] != "legacy-login" ||
		legacy["idempotency_key"] != "legacy-key" || legacy["request_sha256"] != legacyHash || legacy["replaces_action_id"] != nil ||
		legacy["attempt_worker_id"] != nil || legacy["attempt_lease_token"] != nil || legacy["attempt_lease_fence"] != nil {
		t.Fatalf("legacy external action was not preserved exactly: %#v", legacy)
	}
	if got := fmt.Sprint(results[1].Rows); got != `[map[source_column:replaces_action_id target_column:action_id target_table:external_actions]]` {
		t.Fatalf("replacement foreign key=%s", got)
	}
	if got := fmt.Sprint(results[2].Rows); got != `[map[name:external_actions_one_replacement type:index] map[name:external_actions_binding_immutable type:trigger] map[name:external_actions_replacement_valid_insert type:trigger]]` {
		t.Fatalf("replacement schema objects=%s", got)
	}
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions SET action_id='changed-legacy-action'
WHERE action_id='legacy-action'`})

	for _, test := range []struct {
		name, actionType, resource, requestHash string
	}{
		{name: "type", actionType: "wb.other", resource: "legacy-login", requestHash: legacyHash},
		{name: "resource", actionType: "wb.room", resource: "other-login", requestHash: legacyHash},
		{name: "request", actionType: "wb.room", resource: "legacy-login", requestHash: strings.Repeat("b", 64)},
	} {
		t.Run("reject mismatched "+test.name, func(t *testing.T) {
			f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,
status,attempts,created_at_unix,updated_at_unix,replaces_action_id)
VALUES(?,?,?,?,?,?,'pending',0,1,1,'legacy-action')`, Args: []any{
				"invalid-" + test.name, test.actionType, test.resource, "invalid-key-" + test.name,
				[]byte("invalid-envelope"), test.requestHash,
			}})
		})
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,
status,attempts,created_at_unix,updated_at_unix,replaces_action_id)
VALUES('replacement-action','wb.room','legacy-login','replacement-key',?,?,'pending',0,1,1,'legacy-action')`,
		Args: []any{[]byte("replacement-envelope"), legacyHash}})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,
status,attempts,created_at_unix,updated_at_unix,replaces_action_id)
VALUES('second-replacement','wb.room','legacy-login','second-replacement-key',?,?,'pending',0,1,1,'legacy-action')`,
		Args: []any{[]byte("second-envelope"), legacyHash}})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions SET resource_id='changed'
WHERE action_id='replacement-action'`})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `UPDATE external_actions SET replaces_action_id=NULL
WHERE action_id='replacement-action'`})
	f10RequireSQLiteFailure(t, db, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,
status,attempts,created_at_unix,updated_at_unix,replaces_action_id)
VALUES('missing-parent-child','wb.room','legacy-login','missing-parent-key',?,?,'pending',0,1,1,'missing-parent')`,
		Args: []any{[]byte("missing-parent-envelope"), legacyHash}})
}

func TestExternalActionPrepareBindingIsolationBeforeResponseDecryptSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	box := service.store.secrets
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatalf("NewRQLiteExternalActions: %v", err)
	}
	alice := ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "shared-action-key", Request: []byte(`{"login":"alice"}`),
	}
	first, err := store.Prepare(context.Background(), alice)
	if err != nil || first.State != "pending" {
		t.Fatalf("prepare alice result=%#v err=%v", first, err)
	}
	responseEnvelope, err := box.Seal(SecretScope{
		OwnerType: "external-action", OwnerID: alice.ActionKey, Field: "response", Kind: alice.Type,
	}, []byte(`{"room":"alice-room"}`))
	if err != nil {
		t.Fatalf("seal response: %v", err)
	}
	responseBytes, err := json.Marshal(responseEnvelope)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	db.must(t, rqlite.Statement{SQL: `UPDATE external_actions SET status='applied',response_envelope=? WHERE action_id=?`,
		Args: []any{responseBytes, first.ID}})
	spy := &f10CountingAEAD{AEAD: box.aeadByVersion[1]}
	box.aeadByVersion[1] = spy

	for _, conflict := range []ExternalActionCommand{
		{Type: alice.Type, ResourceID: "bob", ActionKey: alice.ActionKey, Request: []byte(`{"login":"bob"}`)},
		{Type: alice.Type, ResourceID: alice.ResourceID, ActionKey: alice.ActionKey, Request: []byte(`{"login":"alice","mode":"other"}`)},
	} {
		if _, err := store.Prepare(context.Background(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflicting prepare err=%v, want ErrConflict", err)
		}
	}
	if spy.opens != 0 {
		t.Fatalf("binding conflict decrypted saved response %d times", spy.opens)
	}
	replay, err := store.Prepare(context.Background(), alice)
	if err != nil || replay.ID != first.ID || replay.State != "succeeded" || string(replay.Response) != `{"room":"alice-room"}` {
		t.Fatalf("exact replay=%#v err=%v", replay, err)
	}
	if spy.opens != 1 {
		t.Fatalf("exact replay decrypt count=%d, want 1", spy.opens)
	}

	rows := db.must(t, rqlite.Statement{SQL: `SELECT resource_id,request_sha256 FROM external_actions WHERE action_id=?`, Args: []any{first.ID}})[0].Rows
	if len(rows) != 1 {
		t.Fatalf("external action rows=%#v", rows)
	}
	plainRequestDigest := sha256.Sum256(alice.Request)
	wantResource := box.LookupHMAC("external-action-resource:"+alice.Type, []byte(alice.ResourceID))
	wantRequest := box.LookupHMAC("external-action-request:"+alice.Type, alice.Request)
	if rows[0]["resource_id"] != wantResource || rows[0]["request_sha256"] != wantRequest ||
		rows[0]["resource_id"] == alice.ResourceID || rows[0]["request_sha256"] == hex.EncodeToString(plainRequestDigest[:]) {
		t.Fatalf("external action durable binding leaks or mismatches identity: %#v", rows[0])
	}
}

func TestExternalActionPrepareReplaysLegacyAppliedRowSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	box := service.store.secrets
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	command := ExternalActionCommand{Type: "wb.room", ResourceID: "legacy-login", ActionKey: "legacy-action-key", Request: []byte(`{"login":"legacy-login"}`)}
	requestEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: command.ActionKey, Field: "request", Kind: command.Type}, command.Request)
	responseEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: command.ActionKey, Field: "response", Kind: command.Type}, []byte(`{"room":"legacy-room"}`))
	requestBytes, _ := json.Marshal(requestEnvelope)
	responseBytes, _ := json.Marshal(responseEnvelope)
	requestDigest := sha256.Sum256(command.Request)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,
response_envelope,created_at_unix,updated_at_unix)
VALUES('legacy-applied','wb.room','legacy-login','legacy-action-key',?,?,'applied',1,?,1,1)`, Args: []any{
		requestBytes, hex.EncodeToString(requestDigest[:]), responseBytes,
	}})
	replay, err := store.Prepare(context.Background(), command)
	if err != nil || replay.ID != "legacy-applied" || replay.State != "succeeded" || string(replay.Response) != `{"room":"legacy-room"}` {
		t.Fatalf("legacy replay=%#v err=%v", replay, err)
	}
}

func TestExternalActionPrepareCommittedUnknownResolvesExactWinnerSQLite(t *testing.T) {
	db, _ := newCustomerIntegritySQLite(t)
	wrapped := &f10CommittedUnknownDB{RQLite: db}
	service, _ := testService(t, wrapped)
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	command := ExternalActionCommand{Type: "wb.room", ResourceID: "alice", ActionKey: "committed-unknown-key", Request: []byte(`{"login":"alice"}`)}
	result, err := store.Prepare(context.Background(), command)
	if err != nil || result.State != "pending" || result.ID == "" {
		t.Fatalf("committed-unknown prepare=%#v err=%v", result, err)
	}
	if wrapped.requests != 1 || wrapped.linearReads != 1 {
		t.Fatalf("write/read attempts=(%d,%d), want (1,1)", wrapped.requests, wrapped.linearReads)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM external_actions WHERE action_type=? AND idempotency_key=?`,
		Args: []any{command.Type, command.ActionKey}})[0].Rows
	count, ok := rowInt64(rows[0], "n")
	if !ok || count != 1 {
		t.Fatalf("committed winner count=%#v", rows)
	}
}

type f10CountingAEAD struct {
	cipher.AEAD
	opens int
}

func (a *f10CountingAEAD) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	a.opens++
	return a.AEAD.Open(dst, nonce, ciphertext, additionalData)
}

type f10CommittedUnknownDB struct {
	rqlite.RQLite
	requests, linearReads int
}

func (db *f10CommittedUnknownDB) Request(ctx context.Context, level rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.requests++
	if _, err := db.RQLite.Request(ctx, level, transaction, statements...); err != nil {
		return nil, err
	}
	return nil, errors.New("synthetic committed-unknown external action write")
}

func (db *f10CommittedUnknownDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.linearReads++
	return db.RQLite.QueryLinearizable(ctx, statements...)
}

func f10RequireSQLiteFailure(t *testing.T, db *customerIntegritySQLite, statement rqlite.Statement) {
	t.Helper()
	if _, err := db.execute(context.Background(), true, statement); err == nil {
		t.Fatalf("SQLite accepted invalid external action statement: %s", statement.SQL)
	}
}
