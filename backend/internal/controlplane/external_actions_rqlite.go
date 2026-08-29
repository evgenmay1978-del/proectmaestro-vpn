package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type RQLiteExternalActions struct{ service *Service }
type externalActionBinding struct {
	resourceKeyHMAC     string
	requestHMAC         string
	legacyRequestSHA256 string
}

func NewRQLiteExternalActions(service *Service) (*RQLiteExternalActions, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("controlplane: external action service is unavailable")
	}
	return &RQLiteExternalActions{service: service}, nil
}

func (s *RQLiteExternalActions) Prepare(ctx context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	if command.Type == "" || command.ResourceID == "" || command.ActionKey == "" {
		return ExternalActionResult{}, errors.New("controlplane: invalid external action")
	}
	if command.ReplacesActionKey != "" && command.ReplacesActionKey == command.ActionKey {
		return ExternalActionResult{}, ErrConflict
	}
	binding, err := s.binding(command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	actionID, err := s.service.ids.NewID("external_action")
	if err != nil {
		return ExternalActionResult{}, errors.New("controlplane: generate external action identifier")
	}
	envelope, err := s.service.store.secrets.Seal(SecretScope{
		OwnerType: "external-action", OwnerID: command.ActionKey, Field: "request", Kind: command.Type,
	}, command.Request)
	if err != nil {
		return ExternalActionResult{}, err
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return ExternalActionResult{}, errors.New("controlplane: encode external action request")
	}
	now := s.service.clock.Now().Unix()
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		externalActionPrepareInsert(command, binding, actionID, envelopeBytes, now),
		externalActionPrepareRead(command),
	)
	ambiguous := err != nil
	if ambiguous {
		results, err = s.service.store.db.QueryLinearizable(ctx, externalActionPrepareRead(command))
		if err != nil {
			return ExternalActionResult{}, ErrUnavailable
		}
	}
	result, err := externalActionBoundResult(results, command, binding)
	if errors.Is(err, ErrNotFound) {
		if ambiguous {
			return ExternalActionResult{}, ErrUnavailable
		}
		if command.ReplacesActionKey != "" {
			return ExternalActionResult{}, ErrConflict
		}
		return ExternalActionResult{}, ErrUnavailable
	}
	if err != nil {
		return ExternalActionResult{}, err
	}
	switch result.State {
	case "applying":
		result.State = "attempt_started"
	case "applied":
		result.State = "succeeded"
	}
	if result.State == "succeeded" {
		result.Response, err = s.openResponse(results, command)
	}
	return result, err
}

func externalActionPrepareRead(command ExternalActionCommand) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT action.action_id,action.action_type,action.resource_id,
action.idempotency_key,action.request_sha256,action.status,action.response_envelope,
action.replaces_action_id,predecessor.idempotency_key AS replaces_action_key,
action.attempt_worker_id,action.attempt_lease_token,action.attempt_lease_fence
FROM external_actions action
LEFT JOIN external_actions predecessor ON predecessor.action_id=action.replaces_action_id
WHERE action.action_type=? AND action.idempotency_key=?`, Args: []any{command.Type, command.ActionKey}}
}

func externalActionPrepareInsert(
	command ExternalActionCommand, binding externalActionBinding, actionID string, envelope []byte, now int64,
) rqlite.Statement {
	if command.ReplacesActionKey == "" {
		return rqlite.Statement{SQL: `INSERT OR IGNORE INTO external_actions(action_id,action_type,resource_id,idempotency_key,
request_envelope,request_sha256,status,attempts,created_at_unix,updated_at_unix)
VALUES(?,?,?,?,?,?,'pending',0,?,?)`, Args: []any{
			actionID, command.Type, binding.resourceKeyHMAC, command.ActionKey, envelope, binding.requestHMAC, now, now,
		}}
	}
	return rqlite.Statement{SQL: `INSERT OR IGNORE INTO external_actions(action_id,action_type,resource_id,idempotency_key,
request_envelope,request_sha256,status,attempts,created_at_unix,updated_at_unix,replaces_action_id)
SELECT ?,?,?,?,?,?,'pending',0,?,?,predecessor.action_id FROM external_actions predecessor
WHERE predecessor.action_type=? AND predecessor.idempotency_key=? AND predecessor.status='unknown'
AND predecessor.resource_id=? AND predecessor.request_sha256=? AND predecessor.idempotency_key<>?`, Args: []any{
		actionID, command.Type, binding.resourceKeyHMAC, command.ActionKey, envelope, binding.requestHMAC, now, now,
		command.Type, command.ReplacesActionKey, binding.resourceKeyHMAC, binding.requestHMAC, command.ActionKey,
	}}
}

func (s *RQLiteExternalActions) StartAttempt(ctx context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	binding, err := s.attemptBinding(command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	jobName := "external-action:" + command.Type
	now := s.service.clock.Now().Unix()
	predicate, predicateArgs := externalActionMutationPredicate(command, binding)
	args := []any{now, command.WorkerID, command.LeaseToken, command.LeaseFence}
	args = append(args, predicateArgs...)
	args = append(args, jobName, command.WorkerID, command.LeaseToken, command.LeaseFence)
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE external_actions AS action
SET status='applying',attempts=attempts+1,updated_at_unix=?,
attempt_worker_id=?,attempt_lease_token=?,attempt_lease_fence=?
WHERE ` + predicate + ` AND action.status='pending'
AND action.attempt_worker_id IS NULL AND action.attempt_lease_token IS NULL AND action.attempt_lease_fence IS NULL
AND EXISTS (
 SELECT 1 FROM cluster_job_leases lease
 WHERE lease.job_name=? AND lease.holder_id=? AND lease.lease_token=? AND lease.lease_fence=?
 AND lease.expires_at_unix>unixepoch()
)`, Args: args},
		externalActionPrepareRead(command),
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	result, err := externalActionMutationResult(results, command, binding, "applying", true)
	if err != nil {
		return ExternalActionResult{}, err
	}
	result.State = "attempt_started"
	return result, nil
}

func (s *RQLiteExternalActions) Finish(ctx context.Context, command ExternalActionCommand, response []byte) (ExternalActionResult, error) {
	binding, err := s.attemptBinding(command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	envelope, err := s.service.store.secrets.Seal(SecretScope{
		OwnerType: "external-action", OwnerID: command.ActionKey, Field: "response", Kind: command.Type,
	}, response)
	if err != nil {
		return ExternalActionResult{}, err
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return ExternalActionResult{}, errors.New("controlplane: encode external action response")
	}
	result, err := s.finish(ctx, command, binding, envelopeBytes)
	if err != nil {
		return ExternalActionResult{}, err
	}
	result.Response = append([]byte(nil), response...)
	return result, nil
}

func (s *RQLiteExternalActions) finish(
	ctx context.Context, command ExternalActionCommand, binding externalActionBinding, response []byte,
) (ExternalActionResult, error) {
	jobName := "external-action:" + command.Type
	now := s.service.clock.Now().Unix()
	predicate, predicateArgs := externalActionMutationPredicate(command, binding)
	args := []any{response, now}
	args = append(args, predicateArgs...)
	args = append(args,
		command.WorkerID, command.LeaseToken, command.LeaseFence,
		jobName, command.WorkerID, command.LeaseToken, command.LeaseFence,
	)
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE external_actions AS action
SET status='applied',response_envelope=?,updated_at_unix=?
WHERE ` + predicate + ` AND action.status='applying'
AND action.attempt_worker_id=? AND action.attempt_lease_token=? AND action.attempt_lease_fence=?
AND EXISTS (
 SELECT 1 FROM cluster_job_leases lease
 WHERE lease.job_name=? AND lease.holder_id=? AND lease.lease_token=? AND lease.lease_fence=?
 AND lease.expires_at_unix>unixepoch()
)`, Args: args},
		externalActionPrepareRead(command),
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	result, err := externalActionMutationResult(results, command, binding, "applied", true)
	if err != nil {
		return ExternalActionResult{}, err
	}
	result.State = "succeeded"
	return result, nil
}

func (s *RQLiteExternalActions) MarkUnknown(ctx context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	binding, err := s.attemptBinding(command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	jobName := "external-action:" + command.Type
	now := s.service.clock.Now().Unix()
	predicate, predicateArgs := externalActionMutationPredicate(command, binding)
	args := []any{now}
	args = append(args, predicateArgs...)
	args = append(args,
		jobName, command.WorkerID, command.LeaseToken, command.LeaseFence,
		command.WorkerID, command.LeaseToken, command.LeaseFence, command.LeaseFence,
	)
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE external_actions AS action SET status='unknown',response_envelope=NULL,updated_at_unix=?
WHERE ` + predicate + ` AND action.status='applying'
AND EXISTS (
 SELECT 1 FROM cluster_job_leases lease
 WHERE lease.job_name=? AND lease.holder_id=? AND lease.lease_token=? AND lease.lease_fence=?
 AND lease.expires_at_unix>unixepoch()
)
AND (
 (action.attempt_worker_id IS NULL AND action.attempt_lease_token IS NULL AND action.attempt_lease_fence IS NULL) OR
 (action.attempt_worker_id=? AND action.attempt_lease_token=? AND action.attempt_lease_fence=?) OR
 action.attempt_lease_fence<?
)`, Args: args},
		externalActionPrepareRead(command),
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	return externalActionMutationResult(results, command, binding, "unknown", false)
}

func (s *RQLiteExternalActions) attemptBinding(command ExternalActionCommand) (externalActionBinding, error) {
	if command.Type == "" || command.ResourceID == "" || command.ActionKey == "" ||
		command.WorkerID == "" || command.LeaseToken == "" || command.LeaseFence <= 0 {
		return externalActionBinding{}, errors.New("controlplane: invalid external action")
	}
	return s.binding(command)
}

func externalActionMutationPredicate(command ExternalActionCommand, binding externalActionBinding) (string, []any) {
	return `action.action_type=? AND action.idempotency_key=?
AND ((action.resource_id=? AND action.request_sha256=?) OR (action.resource_id=? AND action.request_sha256=?))
AND ((?='' AND action.replaces_action_id IS NULL) OR
 (?<>'' AND EXISTS (
  SELECT 1 FROM external_actions predecessor
  WHERE predecessor.action_id=action.replaces_action_id AND predecessor.idempotency_key=?
 )))`, []any{
			command.Type, command.ActionKey,
			binding.resourceKeyHMAC, binding.requestHMAC, command.ResourceID, binding.legacyRequestSHA256,
			command.ReplacesActionKey, command.ReplacesActionKey, command.ReplacesActionKey,
		}
}

func externalActionMutationResult(
	results []rqlite.Result, command ExternalActionCommand, binding externalActionBinding,
	wantState string, requireExactOwner bool,
) (ExternalActionResult, error) {
	result, err := externalActionBoundResult(results, command, binding)
	if errors.Is(err, ErrNotFound) {
		return ExternalActionResult{}, ErrConflict
	}
	if err != nil {
		return ExternalActionResult{}, err
	}
	if len(results) < 2 || results[0].RowsAffected != 1 {
		return ExternalActionResult{}, ErrLeaseLost
	}
	legacyOwner, ownerMatches, err := externalActionAttemptOwner(results, command)
	if err != nil {
		return ExternalActionResult{}, err
	}
	if requireExactOwner && (legacyOwner || !ownerMatches) {
		return ExternalActionResult{}, ErrUnavailable
	}
	if result.State != wantState {
		return ExternalActionResult{}, ErrUnavailable
	}
	return result, nil
}

func externalActionAttemptOwner(results []rqlite.Result, command ExternalActionCommand) (legacy, matches bool, err error) {
	for i := len(results) - 1; i >= 0; i-- {
		if len(results[i].Rows) == 0 {
			continue
		}
		row := results[i].Rows[0]
		worker, workerPresent := row["attempt_worker_id"]
		token, tokenPresent := row["attempt_lease_token"]
		fenceValue, fencePresent := row["attempt_lease_fence"]
		if !workerPresent || !tokenPresent || !fencePresent {
			return false, false, ErrUnavailable
		}
		if worker == nil && token == nil && fenceValue == nil {
			return true, false, nil
		}
		if worker == nil || token == nil || fenceValue == nil {
			return false, false, ErrUnavailable
		}
		workerID, workerOK := worker.(string)
		leaseToken, tokenOK := token.(string)
		leaseFence, fenceOK := rowInt64(row, "attempt_lease_fence")
		if !workerOK || !tokenOK || !fenceOK || workerID == "" || leaseToken == "" || leaseFence <= 0 {
			return false, false, ErrUnavailable
		}
		return false, workerID == command.WorkerID && leaseToken == command.LeaseToken && leaseFence == command.LeaseFence, nil
	}
	return false, false, ErrNotFound
}

func (s *RQLiteExternalActions) binding(command ExternalActionCommand) (externalActionBinding, error) {
	legacyDigest := sha256.Sum256(command.Request)
	binding := externalActionBinding{
		resourceKeyHMAC: s.service.store.secrets.LookupHMAC(
			"external-action-resource:"+command.Type, []byte(command.ResourceID),
		),
		requestHMAC: s.service.store.secrets.LookupHMAC(
			"external-action-request:"+command.Type, command.Request,
		),
		legacyRequestSHA256: hex.EncodeToString(legacyDigest[:]),
	}
	if len(binding.resourceKeyHMAC) != 64 || len(binding.requestHMAC) != 64 {
		return externalActionBinding{}, ErrUnavailable
	}
	return binding, nil
}

func externalActionBoundResult(
	results []rqlite.Result, command ExternalActionCommand, binding externalActionBinding,
) (ExternalActionResult, error) {
	for i := len(results) - 1; i >= 0; i-- {
		if len(results[i].Rows) == 0 {
			continue
		}
		row := results[i].Rows[0]
		id, idOK := rowString(row, "action_id")
		actionType, typeOK := rowString(row, "action_type")
		resourceID, resourceOK := rowString(row, "resource_id")
		actionKey, keyOK := rowString(row, "idempotency_key")
		requestHash, requestOK := rowString(row, "request_sha256")
		status, statusOK := rowString(row, "status")
		if !idOK || !typeOK || !resourceOK || !keyOK || !requestOK || !statusOK {
			return ExternalActionResult{}, ErrUnavailable
		}
		if actionType != command.Type || actionKey != command.ActionKey {
			return ExternalActionResult{}, ErrConflict
		}
		if err := externalActionRelationMatches(row, command); err != nil {
			return ExternalActionResult{}, err
		}
		current := resourceID == binding.resourceKeyHMAC && requestHash == binding.requestHMAC
		legacy := resourceID == command.ResourceID && requestHash == binding.legacyRequestSHA256
		if !current && !legacy {
			return ExternalActionResult{}, ErrConflict
		}
		return ExternalActionResult{ID: id, State: status}, nil
	}
	return ExternalActionResult{}, ErrNotFound
}

func externalActionRelationMatches(row map[string]any, command ExternalActionCommand) error {
	relationID, idPresent := row["replaces_action_id"]
	relationKey, keyPresent := row["replaces_action_key"]
	if !idPresent || !keyPresent {
		return ErrUnavailable
	}
	if relationID == nil && relationKey == nil {
		if command.ReplacesActionKey == "" {
			return nil
		}
		return ErrConflict
	}
	if relationID == nil || relationKey == nil {
		return ErrUnavailable
	}
	id, idOK := relationID.(string)
	key, keyOK := relationKey.(string)
	if !idOK || !keyOK || id == "" || key == "" {
		return ErrUnavailable
	}
	if command.ReplacesActionKey == "" || key != command.ReplacesActionKey {
		return ErrConflict
	}
	return nil
}

func externalActionResult(results []rqlite.Result) (ExternalActionResult, error) {
	for i := len(results) - 1; i >= 0; i-- {
		if len(results[i].Rows) == 0 {
			continue
		}
		row := results[i].Rows[0]
		id, idOK := rowString(row, "action_id")
		status, statusOK := rowString(row, "status")
		if idOK && statusOK {
			return ExternalActionResult{ID: id, State: status}, nil
		}
	}
	return ExternalActionResult{}, ErrNotFound
}

func (s *RQLiteExternalActions) openResponse(results []rqlite.Result, command ExternalActionCommand) ([]byte, error) {
	for i := len(results) - 1; i >= 0; i-- {
		for _, row := range results[i].Rows {
			value, ok := row["response_envelope"]
			if !ok || value == nil {
				continue
			}
			var encoded []byte
			switch actual := value.(type) {
			case string:
				decoded, err := base64.StdEncoding.DecodeString(actual)
				if err != nil {
					return nil, ErrUnavailable
				}
				encoded = decoded
			case []byte:
				encoded = append([]byte(nil), actual...)
			default:
				return nil, ErrUnavailable
			}
			var envelope Envelope
			if err := json.Unmarshal(encoded, &envelope); err != nil {
				return nil, ErrUnavailable
			}
			plaintext, err := s.service.store.secrets.Open(SecretScope{
				OwnerType: "external-action", OwnerID: command.ActionKey, Field: "response", Kind: command.Type,
			}, envelope)
			if err != nil {
				return nil, ErrUnavailable
			}
			return plaintext, nil
		}
	}
	return nil, ErrUnavailable
}
