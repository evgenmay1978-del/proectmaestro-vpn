package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	task7PersistedEntitlementID  = "wl-ent-00000000000000000000000000000001"
	task7PersistedEntitlementID2 = "wl-ent-00000000000000000000000000000002"
)

func entitlementIdentityRows(accountID, entitlementID string) scriptedResult {
	return resultsScript(
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{Rows: []map[string]any{{"customer_id": accountID, "entitlement_id": entitlementID}}},
	)
}

func TestEnsureWhiteListEntitlementPersistsOneImmutableIdentityPerAccount(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{
		entitlementIdentityRows("account-a", task7PersistedEntitlementID),
		entitlementIdentityRows("account-a", task7PersistedEntitlementID),
	}}
	service, _ := testService(t, db)

	first, err := service.EnsureWhiteListEntitlement(context.Background(), "account-a")
	if err != nil {
		t.Fatalf("first EnsureWhiteListEntitlement: %v", err)
	}
	second, err := service.EnsureWhiteListEntitlement(context.Background(), "account-a")
	if err != nil {
		t.Fatalf("second EnsureWhiteListEntitlement: %v", err)
	}
	if first.EntitlementID() != task7PersistedEntitlementID || second.EntitlementID() != first.EntitlementID() || first.AccountID() != "account-a" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if len(db.requestCalls) != 2 {
		t.Fatalf("request calls=%d, want 2", len(db.requestCalls))
	}
	for _, call := range db.requestCalls {
		if call.level != rqlite.Linearizable || !call.transaction || len(call.statements) != 2 {
			t.Fatalf("non-atomic ensure call: %#v", call)
		}
		if !strings.Contains(strings.ToLower(call.statements[0].SQL), "on conflict do nothing") ||
			!strings.Contains(strings.ToLower(call.statements[1].SQL), "where c.customer_id = ?") {
			t.Fatalf("unexpected ensure SQL: %#v", call.statements)
		}
	}
}

func TestEnsureWhiteListEntitlementRetriesOnlyOpaqueIDCollision(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{
		resultsScript(rqlite.Result{RowsAffected: 0}, rqlite.Result{Rows: []map[string]any{{"customer_id": "account-a", "entitlement_id": nil}}}),
		entitlementIdentityRows("account-a", task7PersistedEntitlementID2),
	}}
	service, _ := testService(t, db)
	entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), "account-a")
	if err != nil || entitlement.EntitlementID() != task7PersistedEntitlementID2 || len(db.requestCalls) != 2 {
		t.Fatalf("entitlement=%#v err=%v requests=%d", entitlement, err, len(db.requestCalls))
	}
}

func TestEnsureWhiteListEntitlementResolvesUnknownOutcomeByExactRead(t *testing.T) {
	db := &recordingRQLite{
		requests: []scriptedResult{{err: &rqlite.TransportError{Operation: "request", UnknownOutcome: true, Err: errors.New("synthetic ambiguity")}}},
		linear:   []scriptedResult{rowsScript(map[string]any{"customer_id": "account-a", "entitlement_id": task7PersistedEntitlementID})},
	}
	service, _ := testService(t, db)
	entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), "account-a")
	if err != nil || entitlement.EntitlementID() != task7PersistedEntitlementID || len(db.requestCalls) != 1 || len(db.linearCalls) != 1 {
		t.Fatalf("entitlement=%#v err=%v requests=%d reads=%d", entitlement, err, len(db.requestCalls), len(db.linearCalls))
	}
}

func TestWhiteListEntitlementLookupGetsAccountOnlyFromPersistedBinding(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(map[string]any{"customer_id": "account-b", "entitlement_id": task7PersistedEntitlementID}),
		rowsScript(),
	}}
	service, _ := testService(t, db)
	entitlement, err := service.WhiteListEntitlementByID(context.Background(), task7PersistedEntitlementID)
	if err != nil || entitlement.AccountID() != "account-b" || entitlement.EntitlementID() != task7PersistedEntitlementID {
		t.Fatalf("entitlement=%#v err=%v", entitlement, err)
	}
	_, err = service.WhiteListEntitlementByID(context.Background(), task7PersistedEntitlementID2)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown identity error=%v, want ErrNotFound", err)
	}
}

func TestWhiteListEntitlementPersistenceFailsClosedOnMissingOrMalformedRows(t *testing.T) {
	tests := []struct {
		name    string
		results scriptedResult
	}{
		{name: "missing customer", results: resultsScript(rqlite.Result{}, rqlite.Result{})},
		{name: "malformed id", results: resultsScript(rqlite.Result{}, rqlite.Result{Rows: []map[string]any{{"customer_id": "account-a", "entitlement_id": "bad"}}})},
		{name: "multiple rows", results: resultsScript(rqlite.Result{}, rqlite.Result{Rows: []map[string]any{
			{"customer_id": "account-a", "entitlement_id": task7PersistedEntitlementID},
			{"customer_id": "account-a", "entitlement_id": task7PersistedEntitlementID2},
		}})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{requests: []scriptedResult{test.results}}
			service, _ := testService(t, db)
			if _, err := service.EnsureWhiteListEntitlement(context.Background(), "account-a"); err == nil {
				t.Fatal("malformed persisted binding accepted")
			}
		})
	}
}

func TestWhiteListEntitlementMigrationFourIsAdditiveAndImmutable(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) < 4 || migrations[3].Version != 4 || migrations[3].Path != "migrations/0004_whitelist_entitlement_identity.sql" {
		t.Fatalf("schema=%d migrations=%#v", SchemaVersion, migrations)
	}
	sql := strings.ToLower(string(migrations[3].Data))
	for _, required := range []string{
		"create table whitelist_entitlement_identities",
		"entitlement_id text primary key",
		"customer_id text not null unique",
		"references customers(customer_id) on delete cascade",
		"before update on whitelist_entitlement_identities",
		"before delete on whitelist_entitlement_identities",
		"where customer_id = old.customer_id",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration v4 missing %q", required)
		}
	}
}

func TestWhiteListIdentityMigrationAllowsOnlyCustomerCascadeDeletion(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	sql := strings.ToLower(string(migrations[3].Data))
	for _, required := range []string{
		"references customers(customer_id) on delete cascade",
		"before delete on whitelist_entitlement_identities",
		"when exists",
		"from customers",
		"old.customer_id",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration v4 lacks purge-safe identity guard %q", required)
		}
	}
	if strings.Contains(sql, "on delete restrict") {
		t.Fatal("migration v4 blocks the audited customer purge path")
	}
}
