package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type storedSettingResponse struct {
	Generation int64 `json:"generation"`
}

func settingRequestHash(update SettingUpdate) (string, error) {
	members := append([]string(nil), update.Members...)
	targets := append([]string(nil), update.TargetMembers...)
	sort.Strings(members)
	sort.Strings(targets)
	payload, err := json.Marshal(struct {
		Key                string            `json:"key"`
		ExpectedGeneration int64             `json:"expected_generation"`
		PublicValueJSON    string            `json:"public_value_json"`
		Members            []string          `json:"members,omitempty"`
		TargetMembers      []string          `json:"target_members,omitempty"`
		TargetPayloads     map[string]string `json:"target_payloads,omitempty"`
		CommandType        string            `json:"command_type"`
		RequestFingerprint string            `json:"request_fingerprint,omitempty"`
	}{
		update.Key, update.ExpectedGeneration, update.PublicValueJSON, members, targets, update.TargetPayloads,
		update.CommandType, update.RequestFingerprint,
	})
	if err != nil {
		return "", errors.New("controlplane: encode setting request")
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) resolveSettingMutation(
	ctx context.Context, update SettingUpdate, requestHash string,
) (SettingResult, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,status,response_json FROM idempotency_requests
WHERE scope='setting' AND command_type=? AND idempotency_key=?`, Args: []any{
		update.CommandType, update.IdempotencyKey,
	}})
	if err != nil {
		return SettingResult{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return SettingResult{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || storedHash != requestHash {
		return SettingResult{}, true, ErrConflict
	}
	if !statusOK || status != "applied" || !responseOK {
		return SettingResult{}, true, ErrUnavailable
	}
	var response storedSettingResponse
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil || response.Generation <= 0 {
		return SettingResult{}, true, ErrUnavailable
	}
	return SettingResult{Generation: response.Generation}, true, nil
}

func (s *Store) updateSettingIdempotent(
	ctx context.Context, update SettingUpdate, mutationToken, requestHash string,
) (SettingResult, error) {
	if !validSettingUpdate(update) || strings.TrimSpace(update.CommandType) == "" ||
		strings.TrimSpace(update.IdempotencyKey) == "" || mutationToken == "" || requestHash == "" {
		return SettingResult{}, errors.New("controlplane: invalid idempotent setting update")
	}
	now := s.clock.Now().Unix()
	next := update.ExpectedGeneration + 1
	responseBytes, err := json.Marshal(storedSettingResponse{Generation: next})
	if err != nil {
		return SettingResult{}, errors.New("controlplane: encode setting response")
	}
	guard := `EXISTS(SELECT 1 FROM idempotency_requests WHERE scope='setting' AND command_type=?
AND idempotency_key=? AND request_hash=? AND operation_id=? AND status='applying')`
	guardArgs := []any{update.CommandType, update.IdempotencyKey, requestHash, mutationToken}
	settingGuard := `EXISTS(SELECT 1 FROM cluster_settings WHERE setting_key=? AND generation=? AND last_mutation_token=?)`
	settingGuardArgs := []any{update.Key, next, mutationToken}
	statements := []rqlite.Statement{{
		SQL: `INSERT OR IGNORE INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,
resource_id,decision,operation_id,status,created_at_unix)
VALUES('setting',?,?,?,?, 'accepted',?,'applying',?)`, Args: []any{
			update.CommandType, update.IdempotencyKey, requestHash, update.Key, mutationToken, now,
		},
	}, {
		SQL: `INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix,last_mutation_token)
SELECT ?,?,?,?,? WHERE ` + guard + ` AND COALESCE((SELECT generation FROM cluster_settings WHERE setting_key=?),0)=?
ON CONFLICT(setting_key) DO UPDATE SET public_value_json=excluded.public_value_json,
generation=excluded.generation,updated_at_unix=excluded.updated_at_unix,last_mutation_token=excluded.last_mutation_token
WHERE cluster_settings.generation=? RETURNING generation`, Args: append([]any{
			update.Key, update.PublicValueJSON, next, now, mutationToken,
		}, append(guardArgs, update.Key, update.ExpectedGeneration, update.ExpectedGeneration)...),
	}, backupRPOSettingDirtyGenerationStatement(now, update.Key, next, mutationToken), {
		SQL:  `DELETE FROM setting_members WHERE setting_key=? AND ` + settingGuard + ` AND ` + guard,
		Args: append([]any{update.Key}, append(settingGuardArgs, guardArgs...)...),
	}}
	if len(update.Members) > 0 {
		for _, member := range update.Members {
			canonical, canonicalErr := CanonicalLoginKey(member)
			if canonicalErr != nil {
				return SettingResult{}, errors.New("controlplane: invalid setting member")
			}
			memberHMAC := s.secrets.LookupHMAC("setting-member:"+update.Key, []byte(canonical))
			statements = append(statements, rqlite.Statement{
				SQL: `INSERT INTO setting_members(setting_key,member_key,member_value_json,generation)
SELECT ?,?,'{"enabled":true}',? WHERE ` + settingGuard + ` AND ` + guard + `
ON CONFLICT(setting_key,member_key) DO UPDATE SET member_value_json=excluded.member_value_json,generation=excluded.generation`,
				Args: append([]any{update.Key, memberHMAC, next}, append(settingGuardArgs, guardArgs...)...),
			})
		}
	}
	if update.Secret != nil {
		envelopeBytes, marshalErr := json.Marshal(update.Secret)
		if marshalErr != nil {
			return SettingResult{}, errors.New("controlplane: encode protected envelope")
		}
		digest := sha256.Sum256(envelopeBytes)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO setting_secrets(setting_key,secret_envelope,secret_sha256,key_version,updated_at_unix)
SELECT ?,?,?,?,? WHERE ` + settingGuard + ` AND ` + guard + `
ON CONFLICT(setting_key) DO UPDATE SET secret_envelope=excluded.secret_envelope,
secret_sha256=excluded.secret_sha256,key_version=excluded.key_version,updated_at_unix=excluded.updated_at_unix`,
			Args: append([]any{update.Key, envelopeBytes, hex.EncodeToString(digest[:]), update.Secret.KeyVersion, now}, append(settingGuardArgs, guardArgs...)...),
		})
	} else {
		statements = append(statements, rqlite.Statement{
			SQL:  `DELETE FROM setting_secrets WHERE setting_key=? AND ` + settingGuard + ` AND ` + guard,
			Args: append([]any{update.Key}, append(settingGuardArgs, guardArgs...)...),
		})
	}
	actorHMAC := s.secrets.LookupHMAC("audit-actor", []byte(update.Actor))
	resourceHMAC := s.secrets.LookupHMAC("audit-resource", []byte(update.Key))
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,?, 'cluster_setting',?,? WHERE ` + settingGuard + ` AND ` + guard + ` ON CONFLICT(event_id) DO NOTHING`,
		Args: append([]any{auditID("setting", update.Key, next, now), actorHMAC, update.CommandType, resourceHMAC, now}, append(settingGuardArgs, guardArgs...)...),
	})
	if update.Key == "olcrtc" && len(update.TargetMembers) > 0 {
		for _, member := range update.TargetMembers {
			canonical, canonicalErr := CanonicalLoginKey(member)
			if canonicalErr != nil {
				return SettingResult{}, errors.New("controlplane: invalid olcrtc target")
			}
			desiredJSON, ok := update.TargetPayloads[canonical]
			if !ok {
				return SettingResult{}, errors.New("controlplane: missing olcrtc target payload")
			}
			payload, sealErr := s.secrets.Seal(SecretScope{
				OwnerType: "setting", OwnerID: update.Key, Field: "desired", Kind: "s3-olcrtc",
			}, []byte(desiredJSON))
			if sealErr != nil {
				return SettingResult{}, sealErr
			}
			envelopeBytes, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				return SettingResult{}, errors.New("controlplane: encode olcrtc desired state")
			}
			digest := sha256.Sum256(envelopeBytes)
			loginHMAC := s.secrets.LookupHMAC("customer-login", []byte(canonical))
			statements = append(statements, rqlite.Statement{
				SQL: `INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,
desired_sha256,status,updated_at_unix,tombstone,operation_id)
SELECT c.customer_id,ns.node_id,'s3-olcrtc',?,?,?,'pending',?,0,? FROM customers c
JOIN node_services ns ON ns.service_name='s3-olcrtc' AND ns.desired_target=1 AND ns.apply_enabled=1 AND ns.fenced=0 AND ns.retired=0
WHERE c.login_key_hmac=? AND ` + settingGuard + ` AND ` + guard + `
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET generation=excluded.generation,
desired_envelope=excluded.desired_envelope,desired_sha256=excluded.desired_sha256,status='pending',
updated_at_unix=excluded.updated_at_unix,tombstone=0,operation_id=excluded.operation_id`,
				Args: append([]any{next, envelopeBytes, hex.EncodeToString(digest[:]), now, mutationToken, loginHMAC}, append(settingGuardArgs, guardArgs...)...),
			})
		}
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT OR IGNORE INTO outbox_events(event_id,aggregate_type,aggregate_id,generation,event_type,
payload_envelope,payload_sha256,status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind)
SELECT ? || ':' || dns.customer_id || ':' || dns.node_id,'setting',dns.customer_id || ':' || dns.node_id,
dns.generation,?,dns.desired_envelope,dns.desired_sha256,'pending',?,0,?,dns.node_id,'s3-olcrtc',?,'desired_state'
FROM desired_node_state dns WHERE dns.operation_id=? AND dns.service_name='s3-olcrtc' AND ` + settingGuard + ` AND ` + guard,
			Args: append([]any{mutationToken, update.CommandType, now, now, mutationToken, mutationToken}, append(settingGuardArgs, guardArgs...)...),
		})
	}
	statements = append(statements,
		rqlite.Statement{SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope='setting' AND command_type=? AND idempotency_key=? AND request_hash=? AND operation_id=?
AND status='applying' AND ` + settingGuard, Args: append([]any{
			string(responseBytes), now, update.CommandType, update.IdempotencyKey, requestHash, mutationToken,
		}, settingGuardArgs...)},
		rqlite.Statement{SQL: `DELETE FROM idempotency_requests WHERE scope='setting' AND command_type=?
AND idempotency_key=? AND request_hash=? AND operation_id=? AND status='applying'`, Args: guardArgs},
		rqlite.Statement{SQL: `SELECT request_hash,status,response_json FROM idempotency_requests
WHERE scope='setting' AND command_type=? AND idempotency_key=?`, Args: []any{update.CommandType, update.IdempotencyKey}},
	)
	results, err := s.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return SettingResult{}, ErrUnavailable
	}
	return settingMutationResponse(results, requestHash)
}

func settingMutationResponse(results []rqlite.Result, requestHash string) (SettingResult, error) {
	for i := len(results) - 1; i >= 0; i-- {
		for _, row := range results[i].Rows {
			responseJSON, responseOK := rowString(row, "response_json")
			storedHash, hashOK := rowString(row, "request_hash")
			status, statusOK := rowString(row, "status")
			if !responseOK {
				continue
			}
			if !hashOK || storedHash != requestHash {
				return SettingResult{}, ErrConflict
			}
			if !statusOK || status != "applied" {
				return SettingResult{}, ErrUnavailable
			}
			var response storedSettingResponse
			if err := json.Unmarshal([]byte(responseJSON), &response); err != nil || response.Generation <= 0 {
				return SettingResult{}, ErrUnavailable
			}
			return SettingResult{Generation: response.Generation}, nil
		}
	}
	return SettingResult{}, ErrConflict
}
