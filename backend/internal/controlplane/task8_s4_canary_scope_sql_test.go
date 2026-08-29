package controlplane_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestServiceBusinessS4CanaryReconcilesOnlySelectedCustomerSQL(t *testing.T) {
	ctx := context.Background()
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply real migrations: %v", err)
	}
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	service, err := controlplane.NewService(store, s4CanaryIDs{}, clock)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix)
VALUES ('S4','s4',1,1,0,0,2000000)`})
	for _, customer := range []struct {
		id, login, operation, envelope, digest string
	}{
		{"alice", "Alice", "alice-op", "alice-envelope", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"bob", "Bob", "bob-op", "bob-envelope", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	} {
		seedS4CanaryCustomer(t, db, box, customer.id, customer.login, customer.operation, customer.envelope, customer.digest)
	}

	// Mutant: replacing login-filtered reconciliation with ReconcileBusinessService
	// must enqueue Bob too and fail this persistence assertion.
	view, err := api.NewServiceBusiness(service, api.ServiceBusinessConfig{}).ReconcileServices(ctx, api.ReconcileServicesCommand{
		Logins: []string{"Alice"}, Service: "s4", IdempotencyKey: "s4-alice-canary",
	})
	if err != nil {
		t.Fatalf("reconcile Alice canary: %v", err)
	}
	if view.Count != 1 {
		t.Fatalf("reconciled nodes=%d, want 1", view.Count)
	}
	got := db.must(t, rqlite.Statement{SQL: `SELECT event_id,aggregate_id,node_id,service_name,operation_id,event_kind
FROM outbox_events WHERE node_id='S4' AND service_name='s4' ORDER BY event_id`})
	want := []map[string]any{{
		"event_id": "reconcile:alice-op:S4:s4:1", "aggregate_id": "alice:S4:s4",
		"node_id": "S4", "service_name": "s4", "operation_id": "alice-op", "event_kind": "customer_desired",
	}}
	if !reflect.DeepEqual(got[0].Rows, want) {
		t.Fatalf("S4 canary outbox rows=%#v, want %#v", got[0].Rows, want)
	}
}

func TestServiceBusinessSubscriptionStatusUsesMetadataBeforeDocumentAuthorizationSQLite(t *testing.T) {
	ctx := context.Background()
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("apply real migrations: %v", err)
	}
	box, err := controlplane.NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	clock := s4CanaryClock{value: now}
	store, err := controlplane.NewStore(db, box, clock)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	service, err := controlplane.NewService(store, s4CanaryIDs{}, clock)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix)
VALUES ('S4','s4',1,1,0,0,2000000)`})
	customers := []struct {
		id, status, digest string
		expires            time.Time
		wantActive         bool
		wantDays           int
	}{
		{"live", "active", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", now.Add(48 * time.Hour), true, 2},
		{"inactive", "suspended", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", now.Add(48 * time.Hour), false, 2},
		{"expired", "active", "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", now.Add(-time.Hour), false, 0},
	}
	for _, customer := range customers {
		seedS4CanaryCustomer(t, db, box, customer.id, customer.id, customer.id+"-operation", customer.id+"-envelope", customer.digest)
		db.must(t, rqlite.Statement{SQL: `UPDATE customers SET status=?,expires_at_unix=? WHERE customer_id=?`, Args: []any{customer.status, customer.expires.Unix(), customer.id}})
	}

	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{SubscriptionTopology: subgen.Customer{
		VLESS: &subgen.VLESSCreds{Server: "vless.example.test", Port: 443, SNI: "cdn.example.test", PublicKey: "public-key", ShortID: "0123456789abcdef", Flow: "xtls-rprx-vision", Fingerprint: "chrome"},
	}})
	handler := api.NewControlPlane(business, api.Config{}).Handler()
	for _, customer := range customers {
		t.Run(customer.id+"-info", func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/"+customer.id+"-token/info", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q, want %d", response.Code, response.Body.String(), http.StatusOK)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache control=%q, want no-store", response.Header().Get("Cache-Control"))
			}
			var info struct {
				Active   bool `json:"active"`
				DaysLeft int  `json:"days_left"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
				t.Fatalf("decode info: %v", err)
			}
			if info.Active != customer.wantActive || info.DaysLeft != customer.wantDays {
				t.Fatalf("info=%+v, want active=%v days_left=%d", info, customer.wantActive, customer.wantDays)
			}
		})
	}
	for _, customer := range customers[1:] {
		for _, suffix := range []string{"", "/helpers"} {
			t.Run(customer.id+suffix, func(t *testing.T) {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/"+customer.id+"-token"+suffix, nil))
				if response.Code != http.StatusPaymentRequired {
					t.Fatalf("status=%d body=%q, want %d", response.Code, response.Body.String(), http.StatusPaymentRequired)
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("cache control=%q, want no-store", response.Header().Get("Cache-Control"))
				}
			})
		}
	}
}

type s4CanaryClock struct{ value time.Time }

func (clock s4CanaryClock) Now() time.Time { return clock.value }

type s4CanaryIDs struct{}

func (s4CanaryIDs) NewID(prefix string) (string, error) { return prefix + "-id", nil }

func seedS4CanaryCustomer(t *testing.T, db *s4CanarySQLite, box *controlplane.SecretBox, id, login, operation, payload, digest string) {
	t.Helper()
	tokenEnvelope, err := box.Seal(controlplane.SecretScope{OwnerType: "customer", OwnerID: id, Field: "token", Kind: "subscription"}, []byte(id+"-token"))
	if err != nil {
		t.Fatalf("seal %s token: %v", id, err)
	}
	credentialEnvelope, err := box.Seal(controlplane.SecretScope{OwnerType: "customer", OwnerID: id, Field: "credential", Kind: "vless"}, []byte(id+"-vless"))
	if err != nil {
		t.Fatalf("seal %s credential: %v", id, err)
	}
	tokenBytes, err := json.Marshal(tokenEnvelope)
	if err != nil {
		t.Fatalf("marshal %s token: %v", id, err)
	}
	credentialBytes, err := json.Marshal(credentialEnvelope)
	if err != nil {
		t.Fatalf("marshal %s credential: %v", id, err)
	}
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES (?,?,?,?,?,?,2000000,2000000)`, Args: []any{id, login, box.LookupHMAC("customer-login", []byte(id)), "active", int64(2_100_000), int64(1)}},
		rqlite.Statement{SQL: `INSERT INTO subscription_tokens(token_id,customer_id,token_hmac,token_envelope,token_sha256,generation,revoked,created_at_unix)
VALUES (?,?,?,?,?,?,0,2000000)`, Args: []any{id + "-token", id, box.LookupHMAC("subscription-token", []byte(id+"-token")), tokenBytes, digest, int64(1)}},
		rqlite.Statement{SQL: `INSERT INTO credentials(credential_id,customer_id,protocol,secret_envelope,secret_sha256,generation,enabled,created_at_unix,updated_at_unix)
VALUES (?,?,?,?,?,?,1,2000000,2000000)`, Args: []any{id + "-credential", id, "vless", credentialBytes, digest, int64(1)}},
		rqlite.Statement{SQL: `INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,updated_at_unix,tombstone,operation_id)
VALUES (?,'S4','s4',1,?,?, 'pending',2000000,0,?)`, Args: []any{id, []byte(payload), digest, operation}},
	)
}

type s4CanarySQLite struct {
	python string
	path   string
}

func newS4CanarySQLite(t *testing.T) *s4CanarySQLite {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("Python SQLite is required for S4 canary scope test")
	}
	return &s4CanarySQLite{python: python, path: filepath.Join(t.TempDir(), "s4-canary.sqlite")}
}

func (db *s4CanarySQLite) Request(ctx context.Context, _ rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, transaction, statements...)
}

func (db *s4CanarySQLite) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (db *s4CanarySQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.execute(ctx, false, statements...)
}

func (db *s4CanarySQLite) Backup(context.Context, io.Writer) error {
	return errors.New("backup is outside the S4 canary scope fixture")
}

func (db *s4CanarySQLite) must(t *testing.T, statements ...rqlite.Statement) []rqlite.Result {
	t.Helper()
	results, err := db.execute(context.Background(), true, statements...)
	if err != nil {
		t.Fatalf("SQLite fixture: %v", err)
	}
	return results
}

func (db *s4CanarySQLite) execute(ctx context.Context, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	payload := make([]map[string]any, 0, len(statements))
	for _, statement := range statements {
		args := make([]any, len(statement.Args))
		for index, arg := range statement.Args {
			if blob, ok := arg.([]byte); ok {
				args[index] = map[string]string{"blob": base64.StdEncoding.EncodeToString(blob)}
			} else {
				args[index] = arg
			}
		}
		payload = append(payload, map[string]any{"sql": statement.SQL, "args": args})
	}
	input, err := json.Marshal(map[string]any{"transaction": transaction, "statements": payload})
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, db.python, "-c", s4CanarySQLiteProgram, db.path)
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("S4 canary SQLite process: %w", err)
	}
	var response struct {
		Results []rqlite.Result `json:"results"`
		Error   string          `json:"error"`
		Index   int             `json:"index"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("decode S4 canary SQLite response: %w", err)
	}
	if response.Error != "" {
		return nil, &rqlite.StatementError{Index: response.Index, Message: response.Error}
	}
	return response.Results, nil
}

const s4CanarySQLiteProgram = `
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
        results.append({"Rows": [{key: value(row[key]) for key in row.keys()} for row in cursor.fetchall()], "RowsAffected": max(cursor.rowcount, 0)})
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
