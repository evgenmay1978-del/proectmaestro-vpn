package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestServiceBusinessFailsClosedAcrossEveryBusinessFamilyWithoutService(t *testing.T) {
	business := NewServiceBusiness(nil, ServiceBusinessConfig{SubBaseURL: "https://sub.example"})
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"auth", func() error {
			_, err := business.CreateSession(ctx, CreateSessionCommand{Password: "secret"})
			return err
		}},
		{"customer-read", func() error { _, err := business.CustomerByLogin(ctx, "alice"); return err }},
		{"customer-list", func() error { _, err := business.ListCustomers(ctx, CustomerFilter{}); return err }},
		{"order", func() error {
			_, err := business.CreateOrder(ctx, CreateOrderCommand{Tariff: "1m", IdempotencyKey: "k"})
			return err
		}},
		{"subscription", func() error { _, err := business.SubscriptionSnapshot(ctx, "token"); return err }},
		{"device", func() error {
			_, err := business.TouchDevice(ctx, TouchDeviceCommand{Login: "alice", DeviceID: "device", IdempotencyKey: "k"})
			return err
		}},
		{"customer-write", func() error {
			_, err := business.ProvisionCustomer(ctx, ProvisionCustomerCommand{Login: "alice", Days: 30, IdempotencyKey: "k"})
			return err
		}},
		{"sweep", func() error {
			_, err := business.RunExpirySweep(ctx, ExpirySweepCommand{IdempotencyKey: "k"})
			return err
		}},
		{"reconcile", func() error {
			_, err := business.ReconcileServices(ctx, ReconcileServicesCommand{Login: "alice", Service: "s3", IdempotencyKey: "k"})
			return err
		}},
		{"settings", func() error {
			_, err := business.UpdateSetting(ctx, UpdateSettingCommand{Key: "vkturn", Value: json.RawMessage(`{}`), IdempotencyKey: "k"})
			return err
		}},
		{"olcrtc", func() error { _, err := business.OLCRTCState(ctx); return err }},
		{"external-action", func() error {
			_, err := business.RequestWBRoom(ctx, RequestWBRoomCommand{Login: "alice", ActionKey: "action", IdempotencyKey: "k"})
			return err
		}},
		{"vkturn", func() error { _, err := business.VKTurnState(ctx); return err }},
		{"cluster", func() error { _, err := business.ClusterStatus(ctx); return err }},
		{"audit", func() error { _, err := business.RecentAudit(ctx, AuditFilter{Limit: 10}); return err }},
		{"migrate", func() error {
			_, err := business.MigrateServiceEndpoint(ctx, MigrateEndpointCommand{Service: "anytls", Endpoint: "new", IdempotencyKey: "k"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if err == nil {
				t.Fatal("missing Service did not fail closed")
			}
			status, ok := err.(interface{ HTTPStatus() int })
			if !ok || status.HTTPStatus() != 503 {
				t.Fatalf("error=%v status=%v; want typed 503", err, status)
			}
		})
	}
}

func TestServiceBusinessCustomerViewDoesNotExposeCredentialValues(t *testing.T) {
	business := NewServiceBusiness(nil, ServiceBusinessConfig{SubBaseURL: "https://sub.example"})
	view := business.customerView(controlplane.BusinessCustomer{
		Customer: controlplane.Customer{
			Status: "active", ExpiresAtUnix: time.Now().Add(time.Hour).Unix(), Generation: 7,
			Access: controlplane.CustomerAccess{SubscriptionToken: "subscription-secret", Credentials: map[string]string{"vless": "credential-secret"}},
		},
		Login: "alice",
	})
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || containsAny(string(raw), "credential-secret") {
		t.Fatalf("customer JSON leaked credential value: %s", raw)
	}
	if view.SubURL != "https://sub.example/sub/subscription-secret" {
		t.Fatalf("sub URL=%q", view.SubURL)
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && len(value) >= len(needle) {
			for i := 0; i+len(needle) <= len(value); i++ {
				if value[i:i+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}
