package controlplane

import (
	"context"
	"strings"
	"testing"
)

func TestCustomerStateCommandsAreCanonicalAndDeleteIsLogical(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Service) error
	}{
		{"disable", func(ctx context.Context, service *Service) error {
			_, err := service.DisableCustomer(ctx, CustomerStateCommand{Login: "alice", IdempotencyKey: "disable-1"})
			return err
		}},
		{"enable", func(ctx context.Context, service *Service) error {
			_, err := service.EnableCustomer(ctx, CustomerStateCommand{Login: "alice", IdempotencyKey: "enable-1"})
			return err
		}},
		{"delete", func(ctx context.Context, service *Service) error {
			return service.DeleteCustomer(ctx, DeleteCustomerCommand{Login: "alice", IdempotencyKey: "delete-1"})
		}},
		{"reset-devices", func(ctx context.Context, service *Service) error {
			return service.ResetDevices(ctx, ResetDevicesCommand{Login: "alice", IdempotencyKey: "reset-1"})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := canonicalMutationDB(true)
			service, _ := testService(t, db)
			if err := test.call(context.Background(), service); err != nil {
				t.Fatalf("command: %v", err)
			}
			if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
				t.Fatalf("request calls = %#v, want one transaction", db.requestCalls)
			}
			sql := strings.ToLower(joinedRequestSQL(db))
			if strings.Contains(sql, "delete from customers") {
				t.Fatalf("%s hard-deletes customer identity: %s", test.name, sql)
			}
			if !strings.Contains(sql, "idempotency_requests") {
				t.Fatalf("%s is not protected by canonical idempotency: %s", test.name, sql)
			}
		})
	}
}
