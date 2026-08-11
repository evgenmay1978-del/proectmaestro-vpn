package controlplane

import (
	"context"
	"errors"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func (s *Service) ClaimDevice(
	ctx context.Context,
	customerID string,
	rawDeviceIdentity string,
	platform string,
	limit int,
) (DeviceClaim, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if customerID == "" || rawDeviceIdentity == "" || platform == "" || limit <= 0 {
		return DeviceClaim{}, errors.New("controlplane: invalid device claim")
	}
	deviceID, err := s.ids.NewID("device")
	if err != nil {
		return DeviceClaim{}, errors.New("controlplane: generate device identifier")
	}
	now := s.clock.Now().Unix()
	deviceHMAC := s.store.secrets.LookupHMAC("device-identity", []byte(rawDeviceIdentity))
	actorHMAC := s.store.secrets.LookupHMAC("audit-actor", []byte(customerID))
	resourceHMAC := s.store.secrets.LookupHMAC("audit-resource", []byte(customerID+"\x00"+deviceHMAC))
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO devices(device_id, customer_id, device_key_hmac, platform, last_seen_at_unix, revoked, created_at_unix)
SELECT ?, ?, ?, ?, ?, 0, ?
WHERE EXISTS (SELECT 1 FROM devices WHERE customer_id = ? AND device_key_hmac = ? AND revoked = 0)
   OR (SELECT COUNT(*) FROM devices WHERE customer_id = ? AND revoked = 0) < ?
ON CONFLICT(customer_id, device_key_hmac) DO UPDATE SET
platform = excluded.platform, last_seen_at_unix = excluded.last_seen_at_unix, revoked = 0
RETURNING device_id`,
		Args: []any{deviceID, customerID, deviceHMAC, platform, now, now, customerID, deviceHMAC, customerID, limit},
	}, {
		SQL: `INSERT INTO audit_events(event_id, actor_hmac, action, resource_type, resource_id_hmac, created_at_unix)
SELECT ?, ?, 'device.claim', 'device', ?, ?
WHERE EXISTS (SELECT 1 FROM devices WHERE customer_id = ? AND device_key_hmac = ? AND revoked = 0)`,
		Args: []any{auditID("device", deviceHMAC, 0, now), actorHMAC, resourceHMAC, now, customerID, deviceHMAC},
	}}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return DeviceClaim{}, errors.New("controlplane: device claim unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return DeviceClaim{}, ErrDeviceLimit
	}
	committedID, ok := rowString(row, "device_id")
	if !ok {
		return DeviceClaim{}, errors.New("controlplane: invalid device claim result")
	}
	return DeviceClaim{DeviceID: committedID}, nil
}
