package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type RQLiteExternalActions struct{ service *Service }

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
	digest := sha256.Sum256(command.Request)
	now := s.service.clock.Now().Unix()
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT OR IGNORE INTO external_actions(action_id,action_type,resource_id,idempotency_key,
request_envelope,request_sha256,status,attempts,created_at_unix,updated_at_unix)
VALUES(?,?,?,?,?,?,'pending',0,?,?)`, Args: []any{
			actionID, command.Type, command.ResourceID, command.ActionKey, envelopeBytes, hex.EncodeToString(digest[:]), now, now,
		}},
		rqlite.Statement{SQL: `SELECT action_id,status,response_envelope FROM external_actions
WHERE action_type=? AND idempotency_key=?`, Args: []any{command.Type, command.ActionKey}},
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	return externalActionResult(results)
}

func (s *RQLiteExternalActions) StartAttempt(ctx context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	jobName := "external-action:" + command.Type
	now := s.service.clock.Now().Unix()
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE external_actions SET status='applying',attempts=attempts+1,updated_at_unix=?
WHERE action_type=? AND idempotency_key=? AND status='pending' AND EXISTS (
 SELECT 1 FROM cluster_job_leases WHERE job_name=? AND holder_id=? AND lease_token=? AND expires_at_unix>unixepoch()
)`, Args: []any{now, command.Type, command.ActionKey, jobName, command.WorkerID, command.LeaseToken}},
		rqlite.Statement{SQL: `SELECT action_id,status,response_envelope FROM external_actions
WHERE action_type=? AND idempotency_key=?`, Args: []any{command.Type, command.ActionKey}},
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	result, err := externalActionResult(results)
	if err != nil {
		return ExternalActionResult{}, err
	}
	if result.State != "applying" {
		return ExternalActionResult{}, ErrLeaseLost
	}
	result.State = "attempt_started"
	return result, nil
}

func (s *RQLiteExternalActions) Finish(ctx context.Context, command ExternalActionCommand, response []byte) (ExternalActionResult, error) {
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
	return s.transition(ctx, command, "applied", envelopeBytes)
}

func (s *RQLiteExternalActions) MarkUnknown(ctx context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	return s.transition(ctx, command, "unknown", nil)
}

func (s *RQLiteExternalActions) transition(ctx context.Context, command ExternalActionCommand, status string, response []byte) (ExternalActionResult, error) {
	now := s.service.clock.Now().Unix()
	results, err := s.service.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE external_actions SET status=?,response_envelope=?,updated_at_unix=?
WHERE action_type=? AND idempotency_key=? AND status='applying'`, Args: []any{
			status, response, now, command.Type, command.ActionKey,
		}},
		rqlite.Statement{SQL: `SELECT action_id,status,response_envelope FROM external_actions
WHERE action_type=? AND idempotency_key=?`, Args: []any{command.Type, command.ActionKey}},
	)
	if err != nil {
		return ExternalActionResult{}, ErrUnavailable
	}
	result, err := externalActionResult(results)
	if err != nil {
		return ExternalActionResult{}, err
	}
	switch result.State {
	case "applied":
		result.State = "succeeded"
	case "unknown":
	default:
		return ExternalActionResult{}, ErrConflict
	}
	result.Response = append([]byte(nil), response...)
	return result, nil
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
