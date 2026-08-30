package controlplane

import (
	"context"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// BusinessSubscriptionSnapshot is one coherent strong-read view used by the
// public subscription boundary. Lookup identities are HMACs; raw request
// identities never become database arguments or cache keys.
type BusinessSubscriptionSnapshot struct {
	Customer           BusinessCustomer
	TokenKeyHMAC       string
	TokenGeneration    int64
	DeviceKeyHMAC      string
	DeviceCommitted    bool
	SettingsGeneration int64
	SchemaVersion      int64
	RestoreEpoch       int64
	DatabaseNowUnix    int64
	VerifiedAt         time.Time
}

func (s *Service) BusinessSubscriptionLookupHMACs(rawToken, rawDevice string) (string, string, error) {
	if s == nil || s.store == nil || rawToken == "" {
		return "", "", ErrNotFound
	}
	tokenHMAC := s.store.secrets.LookupHMAC("subscription-token", []byte(rawToken))
	deviceHMAC := ""
	if rawDevice != "" {
		deviceHMAC = s.store.secrets.LookupHMAC("device-identity", []byte(rawDevice))
	}
	return tokenHMAC, deviceHMAC, nil
}

func (s *Service) BusinessSubscriptionSnapshot(ctx context.Context, rawToken, rawDevice string) (BusinessSubscriptionSnapshot, error) {
	tokenHMAC, deviceHMAC, err := s.BusinessSubscriptionLookupHMACs(rawToken, rawDevice)
	if err != nil {
		return BusinessSubscriptionSnapshot{}, err
	}
	results, err := s.store.db.QueryStrong(ctx, rqlite.Statement{
		SQL: `SELECT c.customer_id,c.display_login,c.status,c.expires_at_unix,c.generation,
st.token_hmac,st.token_envelope,st.generation AS token_generation,
cr.protocol,cr.secret_envelope,cr.generation AS credential_generation,
EXISTS(SELECT 1 FROM devices d WHERE d.customer_id=c.customer_id AND d.device_key_hmac=? AND d.revoked=0) AS device_committed,
COALESCE((SELECT MAX(generation) FROM cluster_settings),0) AS settings_generation,
COALESCE((SELECT MAX(version) FROM schema_migrations),0) AS schema_version,
COALESCE((SELECT b.restore_epoch FROM backup_rpo_state b
JOIN cluster_restore_state r ON r.singleton_id=1 AND r.activated=1 AND r.restore_epoch=b.restore_epoch
WHERE b.singleton_id=1),0) AS restore_epoch,
unixepoch() AS database_now_unix
FROM subscription_tokens st
JOIN customers c ON c.customer_id=st.customer_id
LEFT JOIN credentials cr ON cr.customer_id=c.customer_id AND cr.enabled=1
AND cr.generation=(SELECT MAX(c2.generation) FROM credentials c2 WHERE c2.customer_id=c.customer_id AND c2.protocol=cr.protocol AND c2.enabled=1)
WHERE st.token_hmac=? AND st.revoked=0 AND c.status<>'deleted'
ORDER BY cr.protocol`,
		Args: []any{deviceHMAC, tokenHMAC},
	})
	if err != nil {
		return BusinessSubscriptionSnapshot{}, ErrUnavailable
	}
	if len(results) != 1 || len(results[0].Rows) == 0 {
		return BusinessSubscriptionSnapshot{}, ErrNotFound
	}
	rows := results[0].Rows
	first := rows[0]
	customerID, idOK := rowString(first, "customer_id")
	login, loginOK := rowString(first, "display_login")
	status, statusOK := rowString(first, "status")
	expires, expiresOK := rowInt64(first, "expires_at_unix")
	generation, generationOK := rowInt64(first, "generation")
	storedTokenHMAC, tokenHMACOK := rowString(first, "token_hmac")
	tokenEnvelope, tokenEnvelopeOK := rowString(first, "token_envelope")
	tokenGeneration, tokenGenerationOK := rowInt64(first, "token_generation")
	deviceCommitted, deviceOK := rowInt64(first, "device_committed")
	settingsGeneration, settingsOK := rowInt64(first, "settings_generation")
	schemaVersion, schemaOK := rowInt64(first, "schema_version")
	restoreEpoch, restoreOK := rowInt64(first, "restore_epoch")
	databaseNow, databaseNowOK := rowInt64(first, "database_now_unix")
	if !idOK || customerID == "" || !loginOK || !statusOK || !expiresOK || !generationOK || generation < 0 ||
		!tokenHMACOK || len(storedTokenHMAC) != 64 || storedTokenHMAC != tokenHMAC || len(tokenHMAC) != 64 ||
		(deviceHMAC != "" && len(deviceHMAC) != 64) || !tokenEnvelopeOK || !tokenGenerationOK || tokenGeneration <= 0 || tokenGeneration > generation ||
		!deviceOK || (deviceCommitted != 0 && deviceCommitted != 1) || !settingsOK || settingsGeneration < 0 ||
		!schemaOK || schemaVersion <= 0 || !restoreOK || restoreEpoch <= 0 || !databaseNowOK || databaseNow <= 0 {
		return BusinessSubscriptionSnapshot{}, ErrInvalidState
	}
	protocols := make(map[string]struct{}, len(rows))
	credentialRows := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		rowCustomerID, rowIDOK := rowString(row, "customer_id")
		rowLogin, rowLoginOK := rowString(row, "display_login")
		rowStatus, rowStatusOK := rowString(row, "status")
		rowExpires, rowExpiresOK := rowInt64(row, "expires_at_unix")
		rowGeneration, rowGenerationOK := rowInt64(row, "generation")
		rowTokenHMAC, rowTokenHMACOK := rowString(row, "token_hmac")
		rowTokenEnvelope, rowTokenEnvelopeOK := rowString(row, "token_envelope")
		rowTokenGeneration, rowTokenGenerationOK := rowInt64(row, "token_generation")
		rowDeviceCommitted, rowDeviceOK := rowInt64(row, "device_committed")
		rowSettings, rowSettingsOK := rowInt64(row, "settings_generation")
		rowSchema, rowSchemaOK := rowInt64(row, "schema_version")
		rowRestore, rowRestoreOK := rowInt64(row, "restore_epoch")
		rowDatabaseNow, rowDatabaseNowOK := rowInt64(row, "database_now_unix")
		if !rowIDOK || rowCustomerID != customerID || !rowLoginOK || rowLogin != login ||
			!rowStatusOK || rowStatus != status || !rowExpiresOK || rowExpires != expires ||
			!rowGenerationOK || rowGeneration != generation || !rowTokenHMACOK || rowTokenHMAC != tokenHMAC ||
			!rowTokenEnvelopeOK || rowTokenEnvelope != tokenEnvelope || !rowTokenGenerationOK || rowTokenGeneration != tokenGeneration ||
			!rowDeviceOK || rowDeviceCommitted != deviceCommitted || !rowSettingsOK || rowSettings != settingsGeneration ||
			!rowSchemaOK || rowSchema != schemaVersion || !rowRestoreOK || rowRestore != restoreEpoch ||
			!rowDatabaseNowOK || rowDatabaseNow != databaseNow {
			return BusinessSubscriptionSnapshot{}, ErrInvalidState
		}
		protocol, protocolOK := rowString(row, "protocol")
		if !protocolOK {
			if len(rows) != 1 || row["secret_envelope"] != nil || row["credential_generation"] != nil {
				return BusinessSubscriptionSnapshot{}, ErrInvalidState
			}
			continue
		}
		credentialGeneration, credentialGenerationOK := rowInt64(row, "credential_generation")
		if protocol == "" || !credentialGenerationOK || credentialGeneration <= 0 || credentialGeneration > generation {
			return BusinessSubscriptionSnapshot{}, ErrInvalidState
		}
		if _, envelopeOK := rowString(row, "secret_envelope"); !envelopeOK {
			return BusinessSubscriptionSnapshot{}, ErrInvalidState
		}
		if _, duplicate := protocols[protocol]; duplicate {
			return BusinessSubscriptionSnapshot{}, ErrInvalidState
		}
		protocols[protocol] = struct{}{}
		credentialRows = append(credentialRows, row)
	}
	rawStoredToken, err := s.openCustomerSecret(first, "token_envelope", customerID, "token", "subscription")
	if err != nil || rawStoredToken != rawToken {
		return BusinessSubscriptionSnapshot{}, ErrInvalidState
	}
	access := CustomerAccess{SubscriptionToken: rawStoredToken, Credentials: make(map[string]string, len(credentialRows))}
	for _, row := range credentialRows {
		protocol, _ := rowString(row, "protocol")
		raw, openErr := s.openCustomerSecret(row, "secret_envelope", customerID, "credential", protocol)
		if openErr != nil {
			return BusinessSubscriptionSnapshot{}, ErrInvalidState
		}
		access.Credentials[protocol] = raw
	}
	return BusinessSubscriptionSnapshot{
		Customer: BusinessCustomer{Customer: Customer{
			ID: customerID, Status: status, ExpiresAtUnix: expires, Generation: generation, Access: access,
		}, Login: login},
		TokenKeyHMAC: tokenHMAC, TokenGeneration: tokenGeneration,
		DeviceKeyHMAC: deviceHMAC, DeviceCommitted: deviceCommitted == 1,
		SettingsGeneration: settingsGeneration, SchemaVersion: schemaVersion, RestoreEpoch: restoreEpoch,
		DatabaseNowUnix: databaseNow, VerifiedAt: s.clock.Now().UTC(),
	}, nil
}
