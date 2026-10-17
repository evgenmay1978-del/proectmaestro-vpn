package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestExternalActionReplacementBindingAndReplaySQLite(t *testing.T) {
	db, service, store := f10ReplacementStore(t)
	sourceCommand := ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "replacement-source", Request: []byte(`{"login":"alice"}`),
	}
	source := f10PrepareExternalAction(t, store, sourceCommand)
	f10SetExternalActionState(t, db, source.ID, "unknown")
	otherCommand := sourceCommand
	otherCommand.ActionKey = "replacement-other-source"
	other := f10PrepareExternalAction(t, store, otherCommand)
	f10SetExternalActionState(t, db, other.ID, "unknown")

	replacementCommand := sourceCommand
	replacementCommand.ActionKey = "replacement-child"
	replacementCommand.ReplacesActionKey = sourceCommand.ActionKey
	child := f10PrepareExternalAction(t, store, replacementCommand)
	if child.ID == source.ID {
		t.Fatalf("replacement reused predecessor action ID %q", child.ID)
	}
	replayed := f10PrepareExternalAction(t, store, replacementCommand)
	if replayed.ID != child.ID || replayed.State != child.State {
		t.Fatalf("replacement replay=%#v, want %#v", replayed, child)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT replaces_action_id FROM external_actions WHERE action_id=?`, Args: []any{child.ID}})[0].Rows
	if len(rows) != 1 || rows[0]["replaces_action_id"] != source.ID {
		t.Fatalf("replacement relation=%#v, want predecessor %q", rows, source.ID)
	}

	responseEnvelope, err := service.store.secrets.Seal(SecretScope{
		OwnerType: "external-action", OwnerID: replacementCommand.ActionKey, Field: "response", Kind: replacementCommand.Type,
	}, []byte(`{"room":"replacement-room"}`))
	if err != nil {
		t.Fatalf("seal replacement response: %v", err)
	}
	responseBytes, _ := json.Marshal(responseEnvelope)
	db.must(t, rqlite.Statement{SQL: `UPDATE external_actions SET status='applied',response_envelope=? WHERE action_id=?`, Args: []any{responseBytes, child.ID}})
	spy := &f10CountingAEAD{AEAD: service.store.secrets.aeadByVersion[1]}
	service.store.secrets.aeadByVersion[1] = spy
	omitted := replacementCommand
	omitted.ReplacesActionKey = ""
	different := replacementCommand
	different.ReplacesActionKey = otherCommand.ActionKey
	for _, conflict := range []ExternalActionCommand{omitted, different} {
		if _, err := store.Prepare(context.Background(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("relation-conflicting replay err=%v, want ErrConflict", err)
		}
	}
	if spy.opens != 0 {
		t.Fatalf("relation conflict decrypted saved response %d times", spy.opens)
	}
}

func TestExternalActionReplacementRejectsInvalidPredecessorAndBindingSQLite(t *testing.T) {
	db, service, store := f10ReplacementStore(t)
	base := ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "reject-unknown-source", Request: []byte(`{"login":"alice"}`),
	}
	unknown := f10PrepareExternalAction(t, store, base)
	f10SetExternalActionState(t, db, unknown.ID, "unknown")
	appliedCommand := base
	appliedCommand.ActionKey = "reject-applied-source"
	applied := f10PrepareExternalAction(t, store, appliedCommand)
	f10SetExternalActionState(t, db, applied.ID, "applied")

	legacyRequest := []byte(`{"login":"legacy"}`)
	legacyDigest := sha256.Sum256(legacyRequest)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO external_actions(
action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,status,attempts,created_at_unix,updated_at_unix)
VALUES('legacy-unknown-action','wb.room','legacy', 'legacy-unknown-key',?,?,'unknown',1,1,1)`,
		Args: []any{[]byte("legacy-envelope"), hex.EncodeToString(legacyDigest[:])}})

	tests := []ExternalActionCommand{
		{Type: base.Type, ResourceID: base.ResourceID, ActionKey: base.ActionKey, ReplacesActionKey: base.ActionKey, Request: base.Request},
		{Type: base.Type, ResourceID: base.ResourceID, ActionKey: "reject-missing-child", ReplacesActionKey: "reject-missing-source", Request: base.Request},
		{Type: "wb.other-room", ResourceID: base.ResourceID, ActionKey: "reject-type-child", ReplacesActionKey: base.ActionKey, Request: base.Request},
		{Type: base.Type, ResourceID: base.ResourceID, ActionKey: "reject-applied-child", ReplacesActionKey: appliedCommand.ActionKey, Request: base.Request},
		{Type: base.Type, ResourceID: "bob", ActionKey: "reject-resource-child", ReplacesActionKey: base.ActionKey, Request: []byte(`{"login":"bob"}`)},
		{Type: base.Type, ResourceID: base.ResourceID, ActionKey: "reject-request-child", ReplacesActionKey: base.ActionKey, Request: []byte(`{"login":"alice","other":true}`)},
		{Type: base.Type, ResourceID: "legacy", ActionKey: "reject-legacy-child", ReplacesActionKey: "legacy-unknown-key", Request: legacyRequest},
	}
	for _, command := range tests {
		if _, err := store.Prepare(context.Background(), command); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid replacement %#v err=%v, want ErrConflict", command, err)
		}
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM external_actions
WHERE idempotency_key LIKE 'reject-%-child'`})[0].Rows
	count, _ := rowInt64(rows[0], "n")
	if count != 0 {
		t.Fatalf("invalid replacements left %d durable child rows", count)
	}
	_ = service
}

func TestExternalActionReplacementUniquenessAndWriteOutcomesSQLite(t *testing.T) {
	t.Run("same child key concurrent replay", func(t *testing.T) {
		db, service, store := f10ReplacementStore(t)
		sourceCommand := ExternalActionCommand{Type: "wb.room", ResourceID: "alice", ActionKey: "unique-source", Request: []byte(`{}`)}
		source := f10PrepareExternalAction(t, store, sourceCommand)
		f10SetExternalActionState(t, db, source.ID, "unknown")
		command := sourceCommand
		command.ActionKey, command.ReplacesActionKey = "unique-child", sourceCommand.ActionKey
		outcomes := f10ConcurrentExternalActionPrepare(t, db, service, command, command)
		for i, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("same-key concurrent outcome[%d] err=%v", i, outcome.err)
			}
		}
		if outcomes[0].result.ID == "" || outcomes[0].result.ID != outcomes[1].result.ID {
			t.Fatalf("same-key concurrent IDs=(%q,%q), want same durable winner", outcomes[0].result.ID, outcomes[1].result.ID)
		}
		rows := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM external_actions WHERE replaces_action_id=?`, Args: []any{source.ID}})[0].Rows
		count, _ := rowInt64(rows[0], "n")
		if count != 1 {
			t.Fatalf("replacement child count=%d, want 1", count)
		}
	})

	t.Run("distinct child keys concurrent conflict", func(t *testing.T) {
		db, service, store := f10ReplacementStore(t)
		sourceCommand := ExternalActionCommand{Type: "wb.room", ResourceID: "alice", ActionKey: "distinct-source", Request: []byte(`{}`)}
		source := f10PrepareExternalAction(t, store, sourceCommand)
		f10SetExternalActionState(t, db, source.ID, "unknown")
		first := sourceCommand
		first.ActionKey, first.ReplacesActionKey = "distinct-child-a", sourceCommand.ActionKey
		second := first
		second.ActionKey = "distinct-child-b"
		outcomes := f10ConcurrentExternalActionPrepare(t, db, service, first, second)
		var successes, conflicts int
		var winnerID string
		for i, outcome := range outcomes {
			switch {
			case outcome.err == nil:
				successes++
				winnerID = outcome.result.ID
			case errors.Is(outcome.err, ErrConflict):
				conflicts++
			default:
				t.Fatalf("distinct-key concurrent outcome[%d] err=%v", i, outcome.err)
			}
		}
		if successes != 1 || conflicts != 1 || winnerID == "" {
			t.Fatalf("distinct-key outcomes successes=%d conflicts=%d winner=%q, want 1/1/nonempty", successes, conflicts, winnerID)
		}
		rows := db.must(t, rqlite.Statement{SQL: `SELECT action_id FROM external_actions WHERE replaces_action_id=?`, Args: []any{source.ID}})[0].Rows
		if len(rows) != 1 || rows[0]["action_id"] != winnerID {
			t.Fatalf("durable replacement winner=%#v, want action_id %q", rows, winnerID)
		}
	})

	t.Run("committed unknown exact child read", func(t *testing.T) {
		db, service, store := f10ReplacementStore(t)
		sourceCommand := ExternalActionCommand{Type: "wb.room", ResourceID: "alice", ActionKey: "unknown-write-source", Request: []byte(`{}`)}
		source := f10PrepareExternalAction(t, store, sourceCommand)
		f10SetExternalActionState(t, db, source.ID, "unknown")
		wrapped := &f10CommittedUnknownDB{RQLite: db}
		wrappedStore := *service.store
		wrappedStore.db = wrapped
		wrappedService := *service
		wrappedService.store = &wrappedStore
		persistence, _ := NewRQLiteExternalActions(&wrappedService)
		command := sourceCommand
		command.ActionKey, command.ReplacesActionKey = "unknown-write-child", sourceCommand.ActionKey
		child, err := persistence.Prepare(context.Background(), command)
		if err != nil || child.State != "pending" || child.ID == "" {
			t.Fatalf("committed-unknown replacement=%#v err=%v", child, err)
		}
		if wrapped.requests != 1 || wrapped.linearReads != 1 {
			t.Fatalf("committed-unknown writes/reads=(%d,%d), want (1,1)", wrapped.requests, wrapped.linearReads)
		}
	})

	t.Run("ambiguous no child is unavailable", func(t *testing.T) {
		db, service, store := f10ReplacementStore(t)
		sourceCommand := ExternalActionCommand{Type: "wb.room", ResourceID: "alice", ActionKey: "no-child-source", Request: []byte(`{}`)}
		source := f10PrepareExternalAction(t, store, sourceCommand)
		f10SetExternalActionState(t, db, source.ID, "unknown")
		wrapped := &f10NoCommitExternalActionDB{RQLite: db}
		wrappedStore := *service.store
		wrappedStore.db = wrapped
		wrappedService := *service
		wrappedService.store = &wrappedStore
		persistence, _ := NewRQLiteExternalActions(&wrappedService)
		command := sourceCommand
		command.ActionKey, command.ReplacesActionKey = "no-child-replacement", sourceCommand.ActionKey
		if _, err := persistence.Prepare(context.Background(), command); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("ambiguous no-child err=%v, want ErrUnavailable", err)
		}
		if wrapped.requests != 1 || wrapped.linearReads != 1 {
			t.Fatalf("ambiguous no-child writes/reads=(%d,%d), want (1,1)", wrapped.requests, wrapped.linearReads)
		}
	})
}

func TestExternalActionOrdinaryInsertIDCollisionIsUnavailableSQLite(t *testing.T) {
	db, service, store := f10ReplacementStore(t)
	existingCommand := ExternalActionCommand{
		Type: "wb.room", ResourceID: "alice", ActionKey: "ordinary-existing", Request: []byte(`{}`),
	}
	existing := f10PrepareExternalAction(t, store, existingCommand)
	wrapper := &f10ExternalActionIDCollisionDB{RQLite: db, actionID: existing.ID}
	wrapperStore := *service.store
	wrapperStore.db = wrapper
	wrapperService := *service
	wrapperService.store = &wrapperStore
	persistence, err := NewRQLiteExternalActions(&wrapperService)
	if err != nil {
		t.Fatal(err)
	}
	command := existingCommand
	command.ActionKey = "ordinary-collision"
	if _, err := persistence.Prepare(context.Background(), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ordinary action-ID collision err=%v, want ErrUnavailable", err)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM external_actions WHERE idempotency_key=?`, Args: []any{command.ActionKey}})[0].Rows
	count, _ := rowInt64(rows[0], "n")
	if count != 0 {
		t.Fatalf("ordinary action-ID collision left %d durable rows", count)
	}
}

type f10ExternalActionPrepareOutcome struct {
	result ExternalActionResult
	err    error
}

type f10ExternalActionBarrierDB struct {
	rqlite.RQLite
	ready   chan<- struct{}
	release <-chan struct{}
}

func (db *f10ExternalActionBarrierDB) Request(ctx context.Context, level rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.ready <- struct{}{}
	<-db.release
	return db.RQLite.Request(ctx, level, transaction, statements...)
}

func f10ConcurrentExternalActionPrepare(t *testing.T, db rqlite.RQLite, service *Service, commands ...ExternalActionCommand) []f10ExternalActionPrepareOutcome {
	t.Helper()
	ready := make(chan struct{}, len(commands))
	release := make(chan struct{})
	wrapper := &f10ExternalActionBarrierDB{RQLite: db, ready: ready, release: release}
	wrapperStore := *service.store
	wrapperStore.db = wrapper
	wrapperService := *service
	wrapperService.store = &wrapperStore
	persistence, err := NewRQLiteExternalActions(&wrapperService)
	if err != nil {
		t.Fatal(err)
	}
	outcomeChannel := make(chan f10ExternalActionPrepareOutcome, len(commands))
	for _, command := range commands {
		command := command
		go func() {
			result, err := persistence.Prepare(context.Background(), command)
			outcomeChannel <- f10ExternalActionPrepareOutcome{result: result, err: err}
		}()
	}
	for range commands {
		<-ready
	}
	close(release)
	outcomes := make([]f10ExternalActionPrepareOutcome, 0, len(commands))
	for range commands {
		outcomes = append(outcomes, <-outcomeChannel)
	}
	return outcomes
}

type f10ExternalActionIDCollisionDB struct {
	rqlite.RQLite
	actionID string
}

func (db *f10ExternalActionIDCollisionDB) Request(ctx context.Context, level rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	cloned := make([]rqlite.Statement, len(statements))
	copy(cloned, statements)
	cloned[0].Args = append([]any(nil), statements[0].Args...)
	cloned[0].Args[0] = db.actionID
	return db.RQLite.Request(ctx, level, transaction, cloned...)
}

type f10NoCommitExternalActionDB struct {
	rqlite.RQLite
	requests, linearReads int
}

func (db *f10NoCommitExternalActionDB) Request(context.Context, rqlite.Consistency, bool, ...rqlite.Statement) ([]rqlite.Result, error) {
	db.requests++
	return nil, errors.New("synthetic ambiguous write without commit")
}

func (db *f10NoCommitExternalActionDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.linearReads++
	return db.RQLite.QueryLinearizable(ctx, statements...)
}

func f10ReplacementStore(t *testing.T) (*customerIntegritySQLite, *Service, *RQLiteExternalActions) {
	t.Helper()
	db, service := newCustomerIntegritySQLite(t)
	store, err := NewRQLiteExternalActions(service)
	if err != nil {
		t.Fatal(err)
	}
	return db, service, store
}

func f10PrepareExternalAction(t *testing.T, store *RQLiteExternalActions, command ExternalActionCommand) ExternalActionResult {
	t.Helper()
	result, err := store.Prepare(context.Background(), command)
	if err != nil {
		t.Fatalf("Prepare(%q): %v", command.ActionKey, err)
	}
	return result
}

func f10SetExternalActionState(t *testing.T, db *customerIntegritySQLite, actionID, state string) {
	t.Helper()
	db.must(t, rqlite.Statement{SQL: `UPDATE external_actions SET status=? WHERE action_id=?`, Args: []any{state, actionID}})
}
