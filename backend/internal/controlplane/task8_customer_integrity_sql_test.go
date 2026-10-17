package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// This adapter executes the service's actual SQL and migrations. Only the
// transport is replaced; transaction rollback, constraints and reads are real.
type customerIntegritySQLite struct {
	python        string
	path          string
	beforeRequest func()
}

func newCustomerIntegritySQLite(t *testing.T) (*customerIntegritySQLite, *Service) {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for customer integrity tests")
	}
	db := &customerIntegritySQLite{python: python, path: filepath.Join(t.TempDir(), "integrity.sqlite")}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var statements []rqlite.Statement
	for _, migration := range migrations {
		statements = append(statements, migration.Statements...)
	}
	db.must(t, statements...)
	service, _ := testService(t, db)
	return db, service
}

func (db *customerIntegritySQLite) Request(ctx context.Context, level rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if level != rqlite.Linearizable || !transaction {
		return nil, errors.New("customer mutation requires one linearizable transaction")
	}
	if hook := db.beforeRequest; hook != nil {
		db.beforeRequest = nil
		hook()
	}
	return db.execute(ctx, transaction, statements...)
}

func (db *customerIntegritySQLite) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (db *customerIntegritySQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (db *customerIntegritySQLite) Backup(context.Context, io.Writer) error {
	return errors.New("backup is outside the customer integrity fixture")
}

func (db *customerIntegritySQLite) execute(ctx context.Context, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	payloadStatements := make([]sqliteStatementPayload, len(statements))
	for i, statement := range statements {
		args := make([]any, len(statement.Args))
		for j, arg := range statement.Args {
			if blob, ok := arg.([]byte); ok {
				args[j] = map[string]string{"blob": base64.StdEncoding.EncodeToString(blob)}
			} else {
				args[j] = arg
			}
		}
		payloadStatements[i] = sqliteStatementPayload{SQL: statement.SQL, Args: args}
	}
	input, err := json.Marshal(map[string]any{"transaction": transaction, "statements": payloadStatements})
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, db.python, "-c", customerIntegritySQLiteProgram, db.path)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, errors.New("customer integrity SQLite process failed")
	}
	var response struct {
		Results []rqlite.Result `json:"results"`
		Error   string          `json:"error"`
		Index   int             `json:"index"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, errors.New("customer integrity SQLite returned invalid JSON")
	}
	if response.Error != "" {
		return nil, &rqlite.StatementError{Index: response.Index, Message: response.Error}
	}
	return response.Results, nil
}

func (db *customerIntegritySQLite) must(t *testing.T, statements ...rqlite.Statement) []rqlite.Result {
	t.Helper()
	results, err := db.execute(context.Background(), true, statements...)
	if err != nil {
		t.Fatalf("SQLite fixture: %v", err)
	}
	return results
}

func (db *customerIntegritySQLite) snapshot(t *testing.T) []rqlite.Result {
	t.Helper()
	var statements []rqlite.Statement
	for _, table := range []string{"customers", "subscription_tokens", "credentials", "devices", "trial_redemptions", "imported_trial_identities", "imported_secrets", "desired_node_state", "outbox_events", "idempotency_requests"} {
		statements = append(statements, rqlite.Statement{SQL: "SELECT * FROM " + table + " ORDER BY rowid"})
	}
	return db.must(t, statements...)
}

func seedIntegrityCustomer(t *testing.T, service *Service) Customer {
	t.Helper()
	customer, err := service.ProvisionCustomer(context.Background(), ProvisionCustomerCommand{Login: "Existing", Days: 30, IdempotencyKey: "seed-existing"})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	return customer
}

func TestTask8ExistingMutationsKeepAbsoluteAccessSQLite(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name, status string
		expires      int64
		tombstone    bool
		call         func(*Service) (Customer, error)
	}{
		{"extend", "active", 4764800, false, func(s *Service) (Customer, error) {
			return s.ExtendCustomer(ctx, ExtendCustomerCommand{Login: "Existing", Days: 2, IdempotencyKey: "mutation"})
		}},
		{"renew", "active", 4764800, false, func(s *Service) (Customer, error) {
			return s.RenewCustomer(ctx, RenewCustomerCommand{Login: "Existing", Days: 2, IdempotencyKey: "mutation"})
		}},
		{"set-expiry", "active", 3000000, false, func(s *Service) (Customer, error) {
			return s.SetCustomerExpiry(ctx, SetExpiryCommand{Login: "Existing", ExpiresAt: time.Unix(3000000, 0), IdempotencyKey: "mutation"})
		}},
		{"disable", "suspended", 4592000, true, func(s *Service) (Customer, error) {
			return s.DisableCustomer(ctx, CustomerStateCommand{Login: "Existing", IdempotencyKey: "mutation"})
		}},
		{"enable", "active", 4592000, false, func(s *Service) (Customer, error) {
			return s.EnableCustomer(ctx, CustomerStateCommand{Login: "Existing", IdempotencyKey: "mutation"})
		}},
		{"trial", "active", 2172800, false, func(s *Service) (Customer, error) {
			return s.RedeemTrial(ctx, RedeemTrialCommand{Login: "Existing", Anchor: "fresh-anchor", DRMIdentity: "fresh-device", Days: 2, IdempotencyKey: "mutation"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			seed := seedIntegrityCustomer(t, service)
			first, err := test.call(service)
			if err != nil {
				t.Fatalf("mutation: %v", err)
			}
			if first.ID != seed.ID || first.Generation != 2 || first.Status != test.status || first.ExpiresAtUnix != test.expires || !reflect.DeepEqual(first.Access, seed.Access) {
				t.Error("first response omitted canonical access or absolute customer state")
			}
			beforeReplay := db.snapshot(t)
			replayed, err := test.call(service)
			if err != nil || !reflect.DeepEqual(first, replayed) {
				t.Error("first response and idempotent replay differ")
			}
			if !reflect.DeepEqual(beforeReplay, db.snapshot(t)) {
				t.Error("replay changed durable state")
			}
			assertIntegrityDesired(t, db, service, seed, test.status, test.expires, 2, test.tombstone)
		})
	}
}

func TestTask8ConcurrentSameIdempotencyReturnsCommittedAccessSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	command := ProvisionCustomerCommand{Login: "Concurrent", Days: 30, IdempotencyKey: "same-command"}
	var winner Customer
	var winnerState []rqlite.Result
	db.beforeRequest = func() {
		var err error
		winner, err = service.ProvisionCustomer(ctx, command)
		if err != nil {
			t.Fatalf("concurrent winner: %v", err)
		}
		winnerState = db.snapshot(t)
	}
	candidate, err := service.ProvisionCustomer(ctx, command)
	if err != nil {
		t.Fatalf("concurrent replay: %v", err)
	}
	if winner.ID == "" || candidate.ID != winner.ID {
		t.Fatal("concurrent replay did not resolve the committed customer")
	}
	if !reflect.DeepEqual(candidate, winner) {
		t.Error("concurrent replay returned uncommitted access")
	}
	if !reflect.DeepEqual(winnerState, db.snapshot(t)) {
		t.Error("concurrent replay changed durable state")
	}
	replayed, err := service.ProvisionCustomer(ctx, command)
	if err != nil || !reflect.DeepEqual(replayed, winner) {
		t.Error("later replay differs from the committed response")
	}
}

func assertIntegrityDesired(t *testing.T, db *customerIntegritySQLite, service *Service, customer Customer, status string, expires, generation int64, tombstone bool) {
	t.Helper()
	results := db.must(t,
		rqlite.Statement{SQL: `SELECT node_id,service_name,generation,desired_envelope,desired_sha256 FROM desired_node_state WHERE customer_id=?`, Args: []any{customer.ID}},
		rqlite.Statement{SQL: `SELECT operation_id FROM idempotency_requests WHERE resource_id=? AND idempotency_key='mutation'`, Args: []any{customer.ID}},
		rqlite.Statement{SQL: `SELECT status,expires_at_unix,generation FROM customers WHERE customer_id=?`, Args: []any{customer.ID}},
	)
	if len(results[0].Rows) == 0 || len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
		t.Fatal("missing latest desired/customer/idempotency state")
	}
	stored := results[2].Rows[0]
	actualGeneration, _ := rowInt64(stored, "generation")
	actualExpiry, _ := rowInt64(stored, "expires_at_unix")
	if stored["status"] != status || actualGeneration != generation || actualExpiry != expires {
		t.Error("committed customer state differs from absolute mutation result")
	}
	operation, _ := rowString(results[1].Rows[0], "operation_id")
	kind := "customer-active"
	if status != "active" {
		kind = "customer-revoked"
	}
	for _, row := range results[0].Rows {
		node, _ := rowString(row, "node_id")
		serviceName, _ := rowString(row, "service_name")
		digest, _ := rowString(row, "desired_sha256")
		encoded, _ := rowString(row, "desired_envelope")
		blob, err := base64.StdEncoding.DecodeString(encoded)
		var envelope Envelope
		if err != nil || json.Unmarshal(blob, &envelope) != nil {
			t.Fatal("invalid desired envelope")
		}
		document, err := service.store.secrets.OpenDesiredPayload(DesiredPayloadScope{NodeID: node, ServiceID: serviceName, CustomerID: customer.ID, Generation: generation, OperationID: operation, PayloadKind: kind, Tombstone: tombstone}, envelope, digest)
		if err != nil {
			t.Fatalf("open latest desired envelope: %v", err)
		}
		if tombstone {
			if string(document.Body) != `{"tombstone":true}` {
				t.Error("revocation lost its tombstone")
			}
			continue
		}
		var body struct {
			Status  string         `json:"status"`
			Expires int64          `json:"expires_at_unix"`
			Access  CustomerAccess `json:"access"`
		}
		if json.Unmarshal(document.Body, &body) != nil || body.Status != status || body.Expires != expires || !reflect.DeepEqual(body.Access, customer.Access) {
			t.Error("latest absolute desired envelope omitted canonical access or state")
		}
	}
}

func TestTask8ResetDevicesPreservesInactiveStatusSQLite(t *testing.T) {
	for _, status := range []string{"suspended", "deleted"} {
		t.Run(status, func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			customer := seedIntegrityCustomer(t, service)
			if status == "suspended" {
				if _, err := service.DisableCustomer(context.Background(), CustomerStateCommand{Login: "Existing", IdempotencyKey: "state"}); err != nil {
					t.Fatal(err)
				}
			} else if err := service.DeleteCustomer(context.Background(), DeleteCustomerCommand{Login: "Existing", IdempotencyKey: "state"}); err != nil {
				t.Fatal(err)
			}
			db.must(t, rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix) VALUES ('device',?,?,'mobile',1,0,1)`, Args: []any{customer.ID, strings.Repeat("a", 64)}})
			command := ResetDevicesCommand{Login: "Existing", IdempotencyKey: "mutation"}
			if err := service.ResetDevices(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			rows := db.must(t, rqlite.Statement{SQL: `SELECT revoked FROM devices WHERE customer_id=?`, Args: []any{customer.ID}})[0].Rows
			if len(rows) != 1 {
				t.Fatal("device admission row disappeared")
			}
			revoked, _ := rowInt64(rows[0], "revoked")
			if revoked != 1 {
				t.Error("reset did not clear device admission")
			}
			assertIntegrityDesired(t, db, service, customer, status, 4592000, 3, false)
			before := db.snapshot(t)
			if err := service.ResetDevices(context.Background(), command); err != nil || !reflect.DeepEqual(before, db.snapshot(t)) {
				t.Error("reset replay changed state")
			}
		})
	}
}

const customerIntegritySQLiteProgram = `
import base64, json, sqlite3, sys
payload = json.load(sys.stdin)
connection = sqlite3.connect(sys.argv[1], isolation_level=None)
connection.row_factory = sqlite3.Row
connection.create_function("unixepoch", 0, lambda: 2000000)
connection.execute("PRAGMA foreign_keys=ON")
results = []
index = -1
def value(item):
    return base64.b64encode(item).decode() if isinstance(item, bytes) else item
try:
    if payload["transaction"]:
        connection.execute("BEGIN IMMEDIATE")
    for index, statement in enumerate(payload["statements"]):
        args = [base64.b64decode(arg["blob"]) if isinstance(arg, dict) and "blob" in arg else arg for arg in statement.get("args") or []]
        cursor = connection.execute(statement["sql"], args)
        rows = [{key: value(row[key]) for key in row.keys()} for row in cursor.fetchall()]
        results.append({"Rows": rows, "RowsAffected": max(cursor.rowcount, 0)})
    if payload["transaction"]:
        connection.commit()
    print(json.dumps({"results": results}))
except sqlite3.Error as error:
    if payload["transaction"]:
        connection.rollback()
    print(json.dumps({"error": str(error), "index": index}))
finally:
    connection.close()
`
