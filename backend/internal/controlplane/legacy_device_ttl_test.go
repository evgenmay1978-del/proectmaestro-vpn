package controlplane_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestLegacyDeviceTTLIsConsistentAcrossAdmissionAndReadersSQLite(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		name := "login"
		if subscription {
			name = "subscription"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newF5SubscriptionFixture(t, func(string) int { return 1 })
			ctx := context.Background()
			const previous = "original-device-key"
			const next = "new-device-key"
			oldHMAC := fixture.box.LookupHMAC("device-identity", []byte(previous))
			fixture.sqlite.must(t, rqlite.Statement{SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix) VALUES (?,?,?,'maestro',?,0,?)`, Args: []any{"original-device-row", fixture.customerID, oldHMAC, fixture.startedAt.Add(-60 * 24 * time.Hour).Unix(), fixture.startedAt.Add(-60 * 24 * time.Hour).Unix()}})
			claim := func(raw string) (controlplane.DeviceClaim, error) {
				if subscription {
					return fixture.service.ClaimSubscriptionDevice(ctx, f5SubscriptionClaimCommand(t, fixture, raw, true, 1))
				}
				return fixture.service.ClaimDevice(ctx, fixture.customerID, raw, "maestro", 1)
			}
			assertReaders := func(expected int, committed bool) {
				t.Helper()
				customer, err := fixture.service.BusinessCustomerByToken(ctx, fixture.token)
				if err != nil || customer.DeviceCount != expected {
					t.Fatal("account device count disagrees with legacy TTL")
				}
				devices, err := fixture.service.BusinessCustomerDevices(ctx, customer.Login)
				if err != nil || len(devices) != expected {
					t.Fatal("panel device list disagrees with legacy TTL")
				}
				state, err := fixture.service.BusinessSubscriptionSnapshot(ctx, fixture.token, previous)
				if err != nil || state.DeviceCommitted != committed {
					t.Fatal("subscription admission evidence disagrees with legacy TTL")
				}
			}
			assertReaders(1, true)
			if _, err := claim(next); !errors.Is(err, controlplane.ErrDeviceLimit) {
				t.Fatal("exactly sixty-day-old device stopped occupying its slot early")
			}
			fixture.clock.set(fixture.startedAt.Add(time.Second))
			assertReaders(0, false)
			first, err := claim(next)
			if err != nil || first.DeviceID == "" {
				t.Fatal("expired device permanently occupied the login slot")
			}
			if _, err := claim(previous); !errors.Is(err, controlplane.ErrDeviceLimit) {
				t.Fatal("expired device bypassed the new device limit")
			}
			again, err := claim(next)
			if err != nil || again.DeviceID != first.DeviceID {
				t.Fatal("existing device claim replaced its stored identity")
			}
		})
	}
}
