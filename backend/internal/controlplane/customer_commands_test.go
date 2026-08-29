package controlplane

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestCustomerWriteCommandsUseOneCanonicalTransaction(t *testing.T) {
	tests := []struct {
		name   string
		exists bool
		call   func(context.Context, *Service) error
	}{
		{"provision", false, func(ctx context.Context, service *Service) error {
			_, err := service.ProvisionCustomer(ctx, ProvisionCustomerCommand{Login: "Alice", Days: 30, IdempotencyKey: "provision-1"})
			return err
		}},
		{"extend", true, func(ctx context.Context, service *Service) error {
			_, err := service.ExtendCustomer(ctx, ExtendCustomerCommand{Login: "Alice", Days: 7, IdempotencyKey: "extend-1"})
			return err
		}},
		{"renew", true, func(ctx context.Context, service *Service) error {
			_, err := service.RenewCustomer(ctx, RenewCustomerCommand{Login: "Alice", Days: 30, IdempotencyKey: "renew-1"})
			return err
		}},
		{"set-expiry", true, func(ctx context.Context, service *Service) error {
			_, err := service.SetCustomerExpiry(ctx, SetExpiryCommand{Login: "Alice", ExpiresAt: time.Unix(3_000_000, 0), IdempotencyKey: "expiry-1"})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := canonicalMutationDB(test.exists)
			service, _ := testService(t, db)
			if err := test.call(context.Background(), service); err != nil {
				t.Fatalf("command: %v", err)
			}
			assertCanonicalCustomerTransaction(t, db, test.name)
		})
	}
}

func TestExtendAndRenewUseDistinctExpiryRules(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(context.Context, *Service) error
		want string
	}{
		{"extend", func(ctx context.Context, service *Service) error {
			_, err := service.ExtendCustomer(ctx, ExtendCustomerCommand{Login: "alice", Days: 7, IdempotencyKey: "extend-rule"})
			return err
		}, "expires_at_unix +"},
		{"renew", func(ctx context.Context, service *Service) error {
			_, err := service.RenewCustomer(ctx, RenewCustomerCommand{Login: "alice", Days: 7, IdempotencyKey: "renew-rule"})
			return err
		}, "max(expires_at_unix"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := canonicalMutationDB(true)
			service, _ := testService(t, db)
			if err := test.call(context.Background(), service); err != nil {
				t.Fatalf("command: %v", err)
			}
			if sql := joinedRequestSQL(db); !strings.Contains(strings.ToLower(sql), test.want) {
				t.Fatalf("SQL does not preserve %s rule: %s", test.name, sql)
			}
		})
	}
}

func canonicalMutationDB(existing bool) *recordingRQLite {
	linear := []scriptedResult{rowsScript()}
	if existing {
		linear = append(linear, rowsScript(map[string]any{
			"customer_id": "customer_1", "status": "active",
			"expires_at_unix": int64(2_500_000), "generation": int64(1),
		}))
		linear = append(linear, rowsScript(canonicalMutationAccessRows()...))
	} else {
		linear = append(linear, rowsScript())
	}
	linear = append(linear, rowsScript(map[string]any{"node_id": "s1", "service_name": "x-ui"}))
	return &recordingRQLite{linear: linear, requestFn: canonicalCustomerResult}
}

func canonicalCustomerResult(statements []rqlite.Statement) ([]rqlite.Result, error) {
	for _, statement := range statements {
		if !strings.Contains(statement.SQL, "UPDATE idempotency_requests SET status='applied'") || len(statement.Args) < 6 {
			continue
		}
		responseJSON, responseOK := statement.Args[0].(string)
		requestHash, hashOK := statement.Args[5].(string)
		if !responseOK || !hashOK {
			continue
		}
		return []rqlite.Result{{Rows: []map[string]any{{
			"request_hash": requestHash, "status": "applied", "response_json": responseJSON,
		}}}}, nil
	}
	return nil, errors.New("canonical fixture missing applied idempotency result")
}

func assertCanonicalCustomerTransaction(t *testing.T, db *recordingRQLite, command string) {
	t.Helper()
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("%s request calls = %#v, want one transaction", command, db.requestCalls)
	}
	sql := strings.ToLower(joinedRequestSQL(db))
	for _, table := range []string{"idempotency_requests", "customers", "desired_node_state", "outbox_events"} {
		if !strings.Contains(sql, table) {
			t.Fatalf("%s transaction does not touch %s: %s", command, table, sql)
		}
	}
}

func joinedRequestSQL(db *recordingRQLite) string {
	var statements []string
	for _, call := range db.requestCalls {
		for _, statement := range call.statements {
			statements = append(statements, statement.SQL)
		}
	}
	return strings.Join(statements, "\n")
}

func canonicalMutationAccessRows() []map[string]any {
	secrets, err := NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x61}, 32)}, bytes.Repeat([]byte{0x62}, 32))
	if err != nil {
		panic(err)
	}
	seal := func(field, kind, raw string) string {
		envelope, err := secrets.Seal(SecretScope{OwnerType: "customer", OwnerID: "customer_1", Field: field, Kind: kind}, []byte(raw))
		if err != nil {
			panic(err)
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			panic(err)
		}
		return base64.StdEncoding.EncodeToString(encoded)
	}
	return []map[string]any{{
		"token_envelope": seal("token", "subscription", "fixture-subscription"),
		"protocol":       "vless", "secret_envelope": seal("credential", "vless", "fixture-vless"),
	}}
}
