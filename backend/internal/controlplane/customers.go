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
	if customerID == "" || rawDeviceIdentity == "" || platform == "" || limit < 0 {
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
   OR ? = 0
   OR (SELECT COUNT(*) FROM devices WHERE customer_id = ? AND revoked = 0) < ?
ON CONFLICT(customer_id, device_key_hmac) DO UPDATE SET
platform = excluded.platform, last_seen_at_unix = excluded.last_seen_at_unix, revoked = 0
WHERE devices.platform IS NOT excluded.platform
	OR devices.last_seen_at_unix IS NOT excluded.last_seen_at_unix
	OR devices.revoked <> 0`,
		Args: []any{deviceID, customerID, deviceHMAC, platform, now, now, customerID, deviceHMAC, limit, customerID, limit},
	}, backupRPODirtyGenerationStatement(now), {
		SQL: `INSERT INTO audit_events(event_id, actor_hmac, action, resource_type, resource_id_hmac, created_at_unix)
SELECT ?, ?, 'device.claim', 'device', ?, ?
WHERE EXISTS (SELECT 1 FROM devices WHERE customer_id = ? AND device_key_hmac = ? AND revoked = 0)
ON CONFLICT(event_id) DO NOTHING`,
		Args: []any{auditID("device", deviceHMAC, 0, now), actorHMAC, resourceHMAC, now, customerID, deviceHMAC},
	}, {
		SQL: `SELECT device_id FROM devices
WHERE customer_id = ? AND device_key_hmac = ? AND revoked = 0
LIMIT 1`,
		Args: []any{customerID, deviceHMAC},
	}}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return DeviceClaim{}, ErrUnavailable
	}
	if len(results) != len(statements) {
		return DeviceClaim{}, ErrInvalidState
	}
	row, ok := firstRow(results[3:4])
	if !ok {
		return DeviceClaim{}, ErrDeviceLimit
	}
	committedID, ok := rowString(row, "device_id")
	if !ok {
		return DeviceClaim{}, ErrInvalidState
	}
	return DeviceClaim{DeviceID: committedID, AdmittedAtUnix: now}, nil
}

// ClaimSubscriptionDevice binds public subscription admission to the exact
// strong snapshot that authorized it. A changed status, expiry, token, access,
// or generation is a zero-write precondition failure rather than a claim.
func (s *Service) ClaimSubscriptionDevice(ctx context.Context, command SubscriptionDeviceClaimCommand) (DeviceClaim, error) {
	platform := strings.ToLower(strings.TrimSpace(command.Platform))
	if command.CustomerID == "" || len(command.TokenHMAC) != 64 || command.RawDeviceIdentity == "" || platform == "" ||
		command.Limit < 0 || command.ExpectedCustomerGeneration < 0 || command.ExpectedTokenGeneration <= 0 ||
		command.ExpectedExpiresAtUnix <= 0 || command.ExpectedRestoreEpoch <= 0 {
		return DeviceClaim{}, errors.New("controlplane: invalid subscription device claim")
	}
	deviceID, err := s.ids.NewID("device")
	if err != nil {
		return DeviceClaim{}, errors.New("controlplane: generate device identifier")
	}
	now := s.clock.Now().Unix()
	deviceHMAC := s.store.secrets.LookupHMAC("device-identity", []byte(command.RawDeviceIdentity))
	actorHMAC := s.store.secrets.LookupHMAC("audit-actor", []byte(command.CustomerID))
	resourceHMAC := s.store.secrets.LookupHMAC("audit-resource", []byte(command.CustomerID+"\x00"+deviceHMAC))
	requireCredentials := 0
	if command.RequireCredentials {
		requireCredentials = 1
	}
	eligibility := `EXISTS (
SELECT 1 FROM customers c
JOIN subscription_tokens st ON st.customer_id=c.customer_id
WHERE c.customer_id=? AND c.generation=? AND c.status='active'
AND c.expires_at_unix=? AND c.expires_at_unix>unixepoch()
AND st.token_hmac=? AND st.generation=? AND st.revoked=0
AND (?=0 OR EXISTS (SELECT 1 FROM credentials cr WHERE cr.customer_id=c.customer_id AND cr.enabled=1))
AND EXISTS (
  SELECT 1 FROM backup_rpo_state b
  JOIN cluster_restore_state r ON r.singleton_id=1 AND r.activated=1 AND r.restore_epoch=b.restore_epoch
  WHERE b.singleton_id=1 AND b.restore_epoch=?
))`
	eligibilityArgs := []any{
		command.CustomerID, command.ExpectedCustomerGeneration, command.ExpectedExpiresAtUnix,
		command.TokenHMAC, command.ExpectedTokenGeneration, requireCredentials, command.ExpectedRestoreEpoch,
	}
	claimArgs := []any{deviceID, command.CustomerID, deviceHMAC, platform, now, now}
	claimArgs = append(claimArgs, eligibilityArgs...)
	claimArgs = append(claimArgs, command.CustomerID, deviceHMAC, command.Limit, command.CustomerID, command.Limit)
	auditArgs := []any{auditID("device", deviceHMAC+"\x00"+deviceID, 0, now), actorHMAC, resourceHMAC, now}
	auditArgs = append(auditArgs, command.CustomerID, deviceHMAC)
	outcomeArgs := []any{command.CustomerID, deviceHMAC}
	outcomeArgs = append(outcomeArgs, eligibilityArgs...)
	outcomeArgs = append(outcomeArgs, eligibilityArgs...)
	outcomeArgs = append(outcomeArgs, command.Limit, command.CustomerID, command.Limit)
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix)
SELECT ?,?,?,?,?,0,? WHERE ` + eligibility + `
AND (EXISTS (SELECT 1 FROM devices WHERE customer_id=? AND device_key_hmac=? AND revoked=0)
  OR ?=0 OR (SELECT COUNT(*) FROM devices WHERE customer_id=? AND revoked=0)<?)
ON CONFLICT(customer_id,device_key_hmac) DO UPDATE SET
platform=excluded.platform,last_seen_at_unix=excluded.last_seen_at_unix,revoked=0
WHERE devices.platform IS NOT excluded.platform
OR devices.last_seen_at_unix IS NOT excluded.last_seen_at_unix
OR devices.revoked<>0
RETURNING device_id,unixepoch() AS admitted_at_unix`,
		Args: claimArgs,
	}, backupRPODirtyGenerationStatement(now), {
		SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,'device.claim','device',?,? WHERE changes()=1
AND EXISTS (SELECT 1 FROM devices WHERE customer_id=? AND device_key_hmac=? AND revoked=0)
RETURNING event_id`,
		Args: auditArgs,
	}, {
		SQL: `WITH admitted_device AS (
SELECT device_id FROM devices WHERE customer_id=? AND device_key_hmac=? AND revoked=0 LIMIT 1
)
SELECT CASE WHEN ` + eligibility + ` THEN 0 ELSE 1 END AS state_changed,
CASE WHEN EXISTS (SELECT 1 FROM admitted_device) THEN 1 ELSE 0 END AS admitted,
(SELECT device_id FROM admitted_device) AS device_id,
CASE WHEN ` + eligibility + ` AND ?>0
AND (SELECT COUNT(*) FROM devices WHERE customer_id=? AND revoked=0)>=? THEN 1 ELSE 0 END AS at_limit,
unixepoch() AS database_now_unix`,
		Args: outcomeArgs,
	}}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return DeviceClaim{}, ErrUnavailable
	}
	if len(results) != len(statements) {
		return DeviceClaim{}, ErrInvalidState
	}
	if len(results[0].Rows) > 1 || len(results[1].Rows) > 1 || len(results[2].Rows) > 1 || len(results[3].Rows) != 1 {
		return DeviceClaim{}, ErrInvalidState
	}
	if len(results[0].Rows) == 1 {
		if len(results[1].Rows) != 1 || len(results[2].Rows) != 1 {
			return DeviceClaim{}, ErrInvalidState
		}
		if dirtyGeneration, ok := rowInt64(results[1].Rows[0], "dirty_generation"); !ok || dirtyGeneration <= 0 {
			return DeviceClaim{}, ErrInvalidState
		}
		if eventID, ok := rowString(results[2].Rows[0], "event_id"); !ok || eventID == "" {
			return DeviceClaim{}, ErrInvalidState
		}
		mutation := results[0].Rows[0]
		committedID, committed := rowString(mutation, "device_id")
		admittedAt, admittedAtOK := rowInt64(mutation, "admitted_at_unix")
		if !committed || committedID == "" || !admittedAtOK || admittedAt <= 0 || admittedAt >= command.ExpectedExpiresAtUnix {
			return DeviceClaim{}, ErrInvalidState
		}
		return DeviceClaim{DeviceID: committedID, AdmittedAtUnix: admittedAt}, nil
	}
	if len(results[1].Rows) != 0 || len(results[2].Rows) != 0 {
		return DeviceClaim{}, ErrInvalidState
	}
	row := results[3].Rows[0]
	stateChanged, stateChangedOK := rowInt64(row, "state_changed")
	admitted, admittedOK := rowInt64(row, "admitted")
	atLimit, atLimitOK := rowInt64(row, "at_limit")
	databaseNow, databaseNowOK := rowInt64(row, "database_now_unix")
	if !stateChangedOK || (stateChanged != 0 && stateChanged != 1) ||
		!admittedOK || (admitted != 0 && admitted != 1) ||
		!atLimitOK || (atLimit != 0 && atLimit != 1) || !databaseNowOK || databaseNow <= 0 {
		return DeviceClaim{}, ErrInvalidState
	}
	if stateChanged == 0 && command.ExpectedExpiresAtUnix <= databaseNow {
		return DeviceClaim{}, ErrSubscriptionChanged
	}
	if stateChanged == 1 {
		return DeviceClaim{}, ErrSubscriptionChanged
	}
	if admitted == 1 {
		committedID, committed := rowString(row, "device_id")
		if !committed || committedID == "" {
			return DeviceClaim{}, ErrInvalidState
		}
		return DeviceClaim{DeviceID: committedID, AdmittedAtUnix: databaseNow}, nil
	}
	if atLimit == 1 {
		return DeviceClaim{}, ErrDeviceLimit
	}
	return DeviceClaim{}, ErrInvalidState
}
