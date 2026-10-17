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
	requireExactV9MigrationChain(t, migrations)
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
		ReplayResourceID: " Alice ", ReplayRequest: []byte(`{"login":" Alice "}`),
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
	aliasResource := box.LookupHMAC("external-action-resource:"+alice.Type, []byte(alice.ReplayResourceID))
	aliasRequest := box.LookupHMAC("external-action-request:"+alice.Type, alice.ReplayRequest)
	if rows[0]["resource_id"] != wantResource || rows[0]["request_sha256"] != wantRequest ||
		rows[0]["resource_id"] == alice.ResourceID || rows[0]["request_sha256"] == hex.EncodeToString(plainRequestDigest[:]) ||
		rows[0]["resource_id"] == aliasResource || rows[0]["request_sha256"] == aliasRequest {
		t.Fatalf("external action durable binding leaks or mismatches identity: %#v", rows[0])
	}
}

func TestExternalActionPrepareReplaysNonCanonicalLegacyBindingsSQLite(t *testing.T) {
	for _, generation := range []string{"raw-sha256", "raw-hmac"} {
		t.Run(generation, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			box := service.store.secrets
			store, err := NewRQLiteExternalActions(service)
			if err != nil {
				t.Fatal(err)
			}
			command := ExternalActionCommand{
				Type: "wb.room", ResourceID: "alice", ActionKey: "noncanonical-legacy-key", Request: []byte(`{"login":"alice"}`),
				ReplayResourceID: " Alice ", ReplayRequest: []byte(`{"login":" Alice "}`),
			}
			requestEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: command.ActionKey, Field: "request", Kind: command.Type}, command.ReplayRequest)
			responseEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: command.ActionKey, Field: "response", Kind: command.Type}, []byte(`{"room":"legacy-room"}`))
			requestBytes, _ := json.Marshal(requestEnvelope)
			responseBytes, _ := json.Marshal(responseEnvelope)
			storedResource := command.ReplayResourceID
			digest := sha256.Sum256(command.ReplayRequest)
			storedRequestHash := hex.EncodeToString(digest[:])
			if generation == "raw-hmac" {
				storedResource = box.LookupHMAC("external-action-resource:"+command.Type, []byte(command.ReplayResourceID))
				storedRequestHash = box.LookupHMAC("external-action-request:"+command.Type, command.ReplayRequest)
			}
			db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,
response_envelope,created_at_unix,updated_at_unix)
VALUES('noncanonical-legacy-applied','wb.room',?,'noncanonical-legacy-key',?,?,'applied',1,?,1,1)`, Args: []any{
				storedResource, requestBytes, storedRequestHash, responseBytes,
			}})
			replay, err := store.Prepare(context.Background(), command)
			if err != nil || replay.ID != "noncanonical-legacy-applied" || replay.State != "succeeded" || string(replay.Response) != `{"room":"legacy-room"}` {
				t.Fatalf("legacy %s replay=%#v err=%v", generation, replay, err)
			}
		})
	}
}

func TestExternalActionPrepareRejectsMismatchedCompatibilityAliasBeforeDecryptSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	box := service.store.secrets
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	actionKey := "mismatched-alias-key"
	legacyRequest := []byte(`{"login":" Alice "}`)
	requestEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: actionKey, Field: "request", Kind: "wb.room"}, legacyRequest)
	responseEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: actionKey, Field: "response", Kind: "wb.room"}, []byte(`{"room":"alice-room"}`))
	requestBytes, _ := json.Marshal(requestEnvelope)
	responseBytes, _ := json.Marshal(responseEnvelope)
	digest := sha256.Sum256(legacyRequest)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,
response_envelope,created_at_unix,updated_at_unix)
VALUES('mismatched-alias-applied','wb.room',' Alice ',?,?,?,'applied',1,?,1,1)`, Args: []any{
		actionKey, requestBytes, hex.EncodeToString(digest[:]), responseBytes,
	}})
	spy := &f10CountingAEAD{AEAD: box.aeadByVersion[1]}
	box.aeadByVersion[1] = spy
	_, err = store.Prepare(context.Background(), ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: actionKey, Request: []byte(`{"login":"alice"}`),
		ReplayResourceID: " Bob ", ReplayRequest: []byte(`{"login":" Bob "}`),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched alias err=%v, want ErrConflict", err)
	}
	if spy.opens != 0 {
		t.Fatalf("mismatched alias decrypted saved response %d times", spy.opens)
	}
}

func TestExternalActionExecutorRejectsAliasOnlyPendingBeforeProviderSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	box := service.store.secrets
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	command := ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "alias-pending-key", Request: []byte(`{"login":"alice"}`),
		ReplayResourceID: " Alice ", ReplayRequest: []byte(`{"login":" Alice "}`),
		WorkerID: "panel-s2", LeaseToken: "lease-token", LeaseFence: 1,
	}
	requestEnvelope, _ := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: command.ActionKey, Field: "request", Kind: command.Type}, command.ReplayRequest)
	requestBytes, _ := json.Marshal(requestEnvelope)
	digest := sha256.Sum256(command.ReplayRequest)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,
created_at_unix,updated_at_unix)
VALUES('alias-pending','wb.room',' Alice ','alias-pending-key',?,?,'pending',0,1,1)`, Args: []any{
		requestBytes, hex.EncodeToString(digest[:]),
	}})
	sender := &countingExternalSender{}
	_, err = NewExternalActionExecutor(store, sender).Execute(context.Background(), command, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("alias-only pending err=%v, want ErrConflict", err)
	}
	if sender.posts != 0 {
		t.Fatalf("alias-only pending sent %d provider POSTs", sender.posts)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT status,attempts FROM external_actions WHERE action_id='alias-pending'`})[0].Rows
	if len(rows) != 1 || rows[0]["status"] != "pending" {
		t.Fatalf("alias-only pending row mutated: %#v", rows)
	}
}

func TestExternalActionAliasMutationSafetySQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	box := service.store.secrets
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	sealRequest := func(t *testing.T, command ExternalActionCommand) ([]byte, string) {
		t.Helper()
		envelope, err := box.Seal(SecretScope{OwnerType: "external-action", OwnerID: command.ActionKey, Field: "request", Kind: command.Type}, command.ReplayRequest)
		if err != nil {
			t.Fatalf("seal request: %v", err)
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		digest := sha256.Sum256(command.ReplayRequest)
		return encoded, hex.EncodeToString(digest[:])
	}

	t.Run("start rejects alias-only pending", func(t *testing.T) {
		command := ExternalActionCommand{
			Type: "wb.room", ResourceID: "alice", ActionKey: "alias-start-key", Request: []byte(`{"login":"alice"}`),
			ReplayResourceID: " Alice ", ReplayRequest: []byte(`{"login":" Alice "}`),
			WorkerID: "worker-a", LeaseToken: "lease-a", LeaseFence: 1,
		}
		requestBytes, requestHash := sealRequest(t, command)
		db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,created_at_unix,updated_at_unix)
VALUES('alias-start','wb.room',' Alice ','alias-start-key',?,?,'pending',0,1,1)`, Args: []any{requestBytes, requestHash}})
		f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
		if _, err := store.StartAttempt(context.Background(), command); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("alias-only StartAttempt err=%v, want ErrLeaseLost", err)
		}
		row := f10AttemptRow(t, db, "alias-start")
		if row["status"] != "pending" || f10AttemptRowInt(t, row, "attempts") != 0 {
			t.Fatalf("alias-only StartAttempt mutated row=%#v", row)
		}
	})

	t.Run("finish rejects but mark unknown accepts alias applying", func(t *testing.T) {
		command := ExternalActionCommand{
			Type: "wb.room", ResourceID: "alice", ActionKey: "alias-applying-key", Request: []byte(`{"login":"alice"}`),
			ReplayResourceID: " Alice ", ReplayRequest: []byte(`{"login":" Alice "}`),
			WorkerID: "worker-b", LeaseToken: "lease-b", LeaseFence: 2,
		}
		requestBytes, requestHash := sealRequest(t, command)
		db.must(t,
			rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,created_at_unix,updated_at_unix)
VALUES('alias-applying','wb.room',' Alice ','alias-applying-key',?,?,'pending',0,1,1)`, Args: []any{requestBytes, requestHash}},
			rqlite.Statement{SQL: `UPDATE external_actions
SET status='applying',attempts=1,updated_at_unix=2,attempt_worker_id=?,attempt_lease_token=?,attempt_lease_fence=?
WHERE action_id='alias-applying' AND status='pending'`, Args: []any{command.WorkerID, command.LeaseToken, command.LeaseFence}},
		)
		f10SetExternalActionLease(t, db, command, command.WorkerID, command.LeaseToken, command.LeaseFence)
		if _, err := store.Finish(context.Background(), command, []byte(`{"room":"unsafe"}`)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("alias-only Finish err=%v, want ErrLeaseLost", err)
		}
		row := f10AttemptRow(t, db, "alias-applying")
		if row["status"] != "applying" || row["response_envelope"] != nil {
			t.Fatalf("alias-only Finish mutated row=%#v", row)
		}
		result, err := store.MarkUnknown(context.Background(), command)
		if err != nil || result.State != "unknown" {
			t.Fatalf("alias-only MarkUnknown=%#v err=%v", result, err)
		}
		row = f10AttemptRow(t, db, "alias-applying")
		if row["status"] != "unknown" || row["response_envelope"] != nil {
			t.Fatalf("alias-only MarkUnknown row=%#v", row)
		}
	})
}

func TestExternalActionPrepareRejectsPartialCompatibilityAliasSQLite(t *testing.T) {
	_, service := newCustomerIntegritySQLite(t)
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	base := ExternalActionCommand{Type: "wb.room", ResourceID: "alice", ActionKey: "partial-alias", Request: []byte(`{"login":"alice"}`)}
	resourceOnly := base
	resourceOnly.ReplayResourceID = " Alice "
	requestOnly := base
	requestOnly.ReplayRequest = []byte(`{"login":" Alice "}`)
	for _, command := range []ExternalActionCommand{resourceOnly, requestOnly} {
		if _, err := store.Prepare(context.Background(), command); err == nil {
			t.Fatalf("partial alias unexpectedly accepted: %#v", command)
		}
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
