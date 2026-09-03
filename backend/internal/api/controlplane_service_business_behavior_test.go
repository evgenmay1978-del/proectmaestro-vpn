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

func TestSubscriptionDeliveryUsesTask9ClientContract(t *testing.T) {
	for _, test := range []struct {
		client string
		format string
	}{
		{client: "incy", format: "INCY_ONE_TAP"},
		{client: "happ", format: "COPY_HTTPS_URL_AND_QR"},
	} {
		delivery, err := subscriptionDeliveryForClient(test.client, "https://sub.example/token")
		if err != nil {
			t.Fatalf("delivery for %s: %v", test.client, err)
		}
		if delivery.Format != test.format || delivery.URL != "https://sub.example/token" {
			t.Fatalf("delivery for %s = %#v", test.client, delivery)
		}
	}
}

func TestNextOLCRTCRoomSettingKeepsAliceAndBobIsolated(t *testing.T) {
	alice, err := nextOLCRTCRoomSetting(nil, " Alice ", "room-alice", "manual")
	if err != nil {
		t.Fatalf("Alice mutation: %v", err)
	}
	if alice.Login != "alice" || len(alice.Members) != 1 || alice.Members[0] != "alice" {
		t.Fatalf("Alice routing = %#v", alice)
	}
	var aliceTarget map[string]string
	if err := json.Unmarshal(alice.TargetValue, &aliceTarget); err != nil {
		t.Fatalf("Alice target JSON: %v", err)
	}
	if len(aliceTarget) != 2 || aliceTarget["room"] != "room-alice" || aliceTarget["provider"] != "manual" {
		t.Fatalf("Alice target = %#v", aliceTarget)
	}

	bob, err := nextOLCRTCRoomSetting(alice.Value, "Bob", "room-bob", "wbstream")
	if err != nil {
		t.Fatalf("Bob mutation: %v", err)
	}
	if bob.Login != "bob" || len(bob.Members) != 2 || bob.Members[0] != "alice" || bob.Members[1] != "bob" {
		t.Fatalf("Bob routing = %#v", bob)
	}
	type room struct {
		Room     string
		Provider string
	}
	var state struct {
		Rooms map[string]room
	}
	if err := json.Unmarshal(bob.Value, &state); err != nil {
		t.Fatalf("combined room JSON: %v", err)
	}
	if len(state.Rooms) != 2 || state.Rooms["alice"].Room != "room-alice" || state.Rooms["bob"].Room != "room-bob" {
		t.Fatalf("combined rooms = %#v", state.Rooms)
	}
	var bobTarget map[string]string
	if err := json.Unmarshal(bob.TargetValue, &bobTarget); err != nil {
		t.Fatalf("Bob target JSON: %v", err)
	}
	if len(bobTarget) != 2 || bobTarget["room"] != "room-bob" || bobTarget["provider"] != "wbstream" {
		t.Fatalf("Bob target = %#v", bobTarget)
	}

	view, err := olcrtcViewFromValue(bob.Value)
	if err != nil {
		t.Fatalf("OLCRTC view: %v", err)
	}
	if len(view.Rooms) != 2 || view.Rooms["alice"].Room != "room-alice" || view.Rooms["bob"].Room != "room-bob" {
		t.Fatalf("OLCRTC view rooms = %#v", view.Rooms)
	}
	if len(view.Logins) != 2 || view.Logins[0] != "alice" || view.Logins[1] != "bob" {
		t.Fatalf("OLCRTC view logins = %#v", view.Logins)
	}
	if view.Room != "" || view.Provider != "" {
		t.Fatalf("multi-room view exposed ambiguous global room: %#v", view)
	}

	type grantView struct {
		Enabled  bool
		Room     string
		Provider string
	}
	aliceGrantLogin, aliceGrantValue, err := olcrtcGrantTargetValue(bob.Value, "Alice", true)
	if err != nil {
		t.Fatalf("Alice grant: %v", err)
	}
	var aliceGrant grantView
	if err := json.Unmarshal(aliceGrantValue, &aliceGrant); err != nil {
		t.Fatalf("Alice grant JSON: %v", err)
	}
	if aliceGrantLogin != "alice" || !aliceGrant.Enabled || aliceGrant.Room != "room-alice" || aliceGrant.Provider != "manual" {
		t.Fatalf("Alice grant payload = login:%q value:%#v", aliceGrantLogin, aliceGrant)
	}
	bobGrantLogin, bobGrantValue, err := olcrtcGrantTargetValue(bob.Value, "Bob", false)
	if err != nil {
		t.Fatalf("Bob grant: %v", err)
	}
	var bobGrant grantView
	if err := json.Unmarshal(bobGrantValue, &bobGrant); err != nil {
		t.Fatalf("Bob grant JSON: %v", err)
	}
	if bobGrantLogin != "bob" || bobGrant.Enabled || bobGrant.Room != "room-bob" || bobGrant.Provider != "wbstream" {
		t.Fatalf("Bob grant payload = login:%q value:%#v", bobGrantLogin, bobGrant)
	}

	for _, invalid := range []struct {
		name, room, provider string
	}{
		{name: "empty-room", room: " ", provider: "manual"},
		{name: "empty-provider", room: "room-alice", provider: " "},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			if _, err := nextOLCRTCRoomSetting(nil, "alice", invalid.room, invalid.provider); err == nil {
				t.Fatal("invalid room assignment was accepted")
			}
		})
	}
	if _, _, err := olcrtcGrantTargetValue(nil, "alice", true); err == nil {
		t.Fatal("roomless enabled grant was accepted")
	}
	login, disabledValue, err := olcrtcGrantTargetValue(nil, "alice", false)
	if err != nil {
		t.Fatalf("roomless disabled grant: %v", err)
	}
	var disabled grantView
	if err := json.Unmarshal(disabledValue, &disabled); err != nil {
		t.Fatalf("roomless disabled grant JSON: %v", err)
	}
	if login != "alice" || disabled.Enabled {
		t.Fatalf("roomless disabled grant = login:%q value:%#v", login, disabled)
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
