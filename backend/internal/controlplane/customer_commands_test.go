package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestCustomerWriteCommandsUseOneCanonicalTransaction(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Service) error
	}{
		{"provision", func(ctx context.Context, service *Service) error {
			_, err := service.ProvisionCustomer(ctx, ProvisionCustomerCommand{Login: "Alice", Days: 30, IdempotencyKey: "provision-1"})
			return err
		}},
		{"extend", func(ctx context.Context, service *Service) error {
			_, err := service.ExtendCustomer(ctx, ExtendCustomerCommand{Login: "Alice", Days: 7, IdempotencyKey: "extend-1"})
			return err
		}},
		{"renew", func(ctx context.Context, service *Service) error {
			_, err := service.RenewCustomer(ctx, RenewCustomerCommand{Login: "Alice", Days: 30, IdempotencyKey: "renew-1"})
			return err
		}},
		{"set-expiry", func(ctx context.Context, service *Service) error {
			_, err := service.SetCustomerExpiry(ctx, SetExpiryCommand{Login: "Alice", ExpiresAt: time.Unix(3_000_000, 0), IdempotencyKey: "expiry-1"})
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{requestFn: canonicalCustomerResult}
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
			db := &recordingRQLite{requestFn: canonicalCustomerResult}
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

func canonicalCustomerResult(_ []rqlite.Statement) ([]rqlite.Result, error) {
	return []rqlite.Result{{Rows: []map[string]any{{
		"customer_id": "customer_1", "display_login": "Alice", "status": "active",
		"expires_at_unix": int64(3_000_000), "generation": int64(2),
	}}}}, nil
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
