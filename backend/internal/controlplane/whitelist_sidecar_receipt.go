package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type WhiteListSidecarReceipt struct {
	ActionKey            string    `json:"action_key"`
	OriginID             string    `json:"origin_id"`
	ReleaseID            string    `json:"release_id"`
	XrayProcessBootID    string    `json:"xray_process_boot_id"`
	ConfigDigest         string    `json:"config_digest"`
	DesiredGeneration    int64     `json:"desired_generation"`
	ManagedUserSetDigest string    `json:"managed_user_set_digest"`
	AppliedAt            time.Time `json:"applied_at"`
	ExpiresAt            time.Time `json:"expires_at"`
}

func ValidateWhiteListSidecarReceipt(
	desired WhiteListSidecarDesired, currentBootID string, receipt WhiteListSidecarReceipt, now time.Time,
) error {
	if currentBootID == "" || receipt.ActionKey != desired.Action.ActionKey ||
		receipt.OriginID != desired.OriginID || receipt.ReleaseID != desired.ReleaseID ||
		receipt.XrayProcessBootID != currentBootID || receipt.ConfigDigest != desired.ConfigDigest ||
		receipt.DesiredGeneration != desired.Generation ||
		receipt.ManagedUserSetDigest != desired.ManagedUserSetDigest ||
		receipt.AppliedAt.IsZero() || receipt.ExpiresAt.IsZero() || receipt.AppliedAt.After(now) ||
		!receipt.ExpiresAt.After(now) || !receipt.ExpiresAt.After(receipt.AppliedAt) {
		return errors.New("controlplane: white-list sidecar receipt does not match desired state")
	}
	return nil
}

func ReplayWhiteListSidecarReceipt(
	existing, replay WhiteListSidecarReceipt,
) (WhiteListSidecarReceipt, error) {
	if !whiteListSidecarReceiptPersistedEqual(existing, replay) {
		return WhiteListSidecarReceipt{}, ErrConflict
	}
	return existing, nil
}

// EvaluateWhiteListSidecarReadiness derives the complete active Origin set and
// each Origin's latest desired generation from one linearizable durable read.
func (s *Service) EvaluateWhiteListSidecarReadiness(
	ctx context.Context, bootIDs map[string]string, receipts []WhiteListSidecarReceipt, exitID string,
) (bool, error) {
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil {
		return false, ErrUnavailable
	}
	if exitID == "" {
		return false, nil
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT
origin.origin_id,origin.node_id AS current_node_id,origin.release_id AS current_release_id,
origin.profile_id AS current_profile_id,origin.preset_id AS current_preset_id,
origin.config_digest AS current_config_digest,desired.node_id AS desired_node_id,
desired.release_id AS desired_release_id,desired.profile_id AS desired_profile_id,
desired.preset_id AS desired_preset_id,desired.exit_id AS desired_exit_id,
desired.config_digest AS desired_config_digest,desired.desired_generation,
desired.managed_user_set_digest,desired.desired_sha256,desired.payload_json,
desired.action_type,desired.action_key,exit.healthy AS exit_healthy
FROM whitelist_sidecar_origins AS origin
LEFT JOIN whitelist_sidecar_desired AS desired
  ON desired.origin_id=origin.origin_id
 AND desired.desired_generation=(
  SELECT MAX(candidate.desired_generation)
  FROM whitelist_sidecar_desired AS candidate
  WHERE candidate.origin_id=origin.origin_id
 )
LEFT JOIN whitelist_sidecar_exits AS exit
  ON exit.exit_id=desired.exit_id AND exit.exit_id=?
WHERE origin.active=1
ORDER BY origin.origin_id`, Args: []any{exitID}})
	if err != nil {
		return false, ErrUnavailable
	}
	desired, complete := whiteListSidecarCurrentDesired(results, exitID)
	if !complete {
		return false, nil
	}
	return evaluateWhiteListSidecarReceipts(desired, bootIDs, receipts, s.clock.Now())
}

func whiteListSidecarCurrentDesired(results []rqlite.Result, expectedExitID string) ([]WhiteListSidecarDesired, bool) {
	if len(results) != 1 || len(results[0].Rows) == 0 {
		return nil, false
	}
	desiredRows := make([]WhiteListSidecarDesired, 0, len(results[0].Rows))
	seen := make(map[string]struct{}, len(results[0].Rows))
	for _, row := range results[0].Rows {
		originID, originOK := rowString(row, "origin_id")
		currentNodeID, currentNodeOK := rowString(row, "current_node_id")
		currentReleaseID, currentReleaseOK := rowString(row, "current_release_id")
		currentProfileID, currentProfileOK := rowString(row, "current_profile_id")
		currentPresetID, currentPresetOK := rowString(row, "current_preset_id")
		currentConfigDigest, currentConfigOK := rowString(row, "current_config_digest")
		nodeID, nodeOK := rowString(row, "desired_node_id")
		releaseID, releaseOK := rowString(row, "desired_release_id")
		profileID, profileOK := rowString(row, "desired_profile_id")
		presetID, presetOK := rowString(row, "desired_preset_id")
		exitID, exitOK := rowString(row, "desired_exit_id")
		configDigest, configOK := rowString(row, "desired_config_digest")
		managedDigest, managedOK := rowString(row, "managed_user_set_digest")
		desiredSHA, desiredOK := rowString(row, "desired_sha256")
		actionType, typeOK := rowString(row, "action_type")
		actionKey, actionOK := rowString(row, "action_key")
		generation, generationOK := rowInt64(row, "desired_generation")
		healthy, healthyOK := rowInt64(row, "exit_healthy")
		payloadJSON, payloadOK := whiteListRowBytes(row, "payload_json")
		var payload whiteListSidecarPayload
		payloadDecoded := payloadOK && json.Unmarshal(payloadJSON, &payload) == nil
		requiresHealthyExit := payloadDecoded && len(payload.ManagedUsers) > 0
		desired := WhiteListSidecarDesired{
			OriginID: originID, NodeID: nodeID, ReleaseID: releaseID, ProfileID: profileID,
			PresetID: presetID, ExitID: exitID, Generation: generation, ConfigDigest: configDigest,
			ManagedUserSetDigest: managedDigest, DesiredSHA256: desiredSHA,
			StaticUsers: append([]string(nil), payload.StaticUsers...), ManagedUsers: append([]string(nil), payload.ManagedUsers...),
			PayloadJSON: append([]byte(nil), payloadJSON...),
			Action:      ExternalActionCommand{Type: actionType, ResourceID: originID, ActionKey: actionKey, Request: append([]byte(nil), payloadJSON...)},
		}
		_, duplicate := seen[originID]
		if !originOK || !currentNodeOK || !currentReleaseOK || !currentProfileOK || !currentPresetOK ||
			!currentConfigOK || !nodeOK || !releaseOK || !profileOK || !presetOK || !exitOK ||
			!configOK || !managedOK || !desiredOK || !typeOK || !actionOK || !generationOK ||
			!payloadDecoded || (requiresHealthyExit && (!healthyOK || healthy != 1)) || duplicate || exitID != expectedExitID ||
			currentNodeID != nodeID || currentReleaseID != releaseID || currentProfileID != profileID ||
			currentPresetID != presetID || currentConfigDigest != configDigest ||
			validateWhiteListSidecarDesired(desired) != nil {
			return nil, false
		}
		seen[originID] = struct{}{}
		desiredRows = append(desiredRows, desired)
	}
	return desiredRows, true
}

func evaluateWhiteListSidecarReceipts(
	desired []WhiteListSidecarDesired, bootIDs map[string]string,
	receipts []WhiteListSidecarReceipt, now time.Time,
) (bool, error) {
	expectedActions := make(map[string]struct{}, len(desired))
	for _, desired := range desired {
		expectedActions[desired.Action.ActionKey] = struct{}{}
	}
	byAction := make(map[string]WhiteListSidecarReceipt, len(desired))
	for _, receipt := range receipts {
		if _, expected := expectedActions[receipt.ActionKey]; !expected {
			continue
		}
		if existing, ok := byAction[receipt.ActionKey]; ok {
			if _, err := ReplayWhiteListSidecarReceipt(existing, receipt); err != nil {
				return false, err
			}
			continue
		}
		byAction[receipt.ActionKey] = receipt
	}
	for _, target := range desired {
		receipt, ok := byAction[target.Action.ActionKey]
		if !ok {
			return false, nil
		}
		if err := ValidateWhiteListSidecarReceipt(target, bootIDs[target.OriginID], receipt, now); err != nil {
			return false, nil
		}
	}
	return true, nil
}

// RecordWhiteListSidecarReceipt durably accepts only an exact receipt replay.
func (s *Service) RecordWhiteListSidecarReceipt(
	ctx context.Context, desired WhiteListSidecarDesired, currentBootID string,
	receipt WhiteListSidecarReceipt,
) (WhiteListSidecarReceipt, error) {
	receipt = normalizeWhiteListSidecarReceipt(receipt)
	now := s.clock.Now()
	if err := ValidateWhiteListSidecarReceipt(desired, currentBootID, receipt, now); err != nil {
		return WhiteListSidecarReceipt{}, err
	}
	insert := rqlite.Statement{SQL: `INSERT OR IGNORE INTO whitelist_sidecar_receipts(
action_key,origin_id,release_id,xray_process_boot_id,config_digest,desired_generation,
managed_user_set_digest,applied_at_unix,expires_at_unix,created_at_unix) VALUES(?,?,?,?,?,?,?,?,?,?)`, Args: []any{
		receipt.ActionKey, receipt.OriginID, receipt.ReleaseID, receipt.XrayProcessBootID,
		receipt.ConfigDigest, receipt.DesiredGeneration, receipt.ManagedUserSetDigest,
		receipt.AppliedAt.Unix(), receipt.ExpiresAt.Unix(), now.Unix(),
	}}
	read := whiteListSidecarReceiptRead(receipt.ActionKey)
	_, _ = s.store.db.Request(ctx, rqlite.Linearizable, true, insert)
	results, err := s.store.db.QueryLinearizable(ctx, read)
	if err != nil {
		return WhiteListSidecarReceipt{}, ErrUnavailable
	}
	stored, err := whiteListSidecarReceiptFromResults(results)
	if err != nil {
		return WhiteListSidecarReceipt{}, err
	}
	return ReplayWhiteListSidecarReceipt(stored, receipt)
}

func whiteListSidecarReceiptPersistedEqual(left, right WhiteListSidecarReceipt) bool {
	return left.ActionKey == right.ActionKey && left.OriginID == right.OriginID &&
		left.ReleaseID == right.ReleaseID && left.XrayProcessBootID == right.XrayProcessBootID &&
		left.ConfigDigest == right.ConfigDigest && left.DesiredGeneration == right.DesiredGeneration &&
		left.ManagedUserSetDigest == right.ManagedUserSetDigest &&
		left.AppliedAt.Unix() == right.AppliedAt.Unix() && left.ExpiresAt.Unix() == right.ExpiresAt.Unix()
}

func normalizeWhiteListSidecarReceipt(receipt WhiteListSidecarReceipt) WhiteListSidecarReceipt {
	if !receipt.AppliedAt.IsZero() {
		receipt.AppliedAt = time.Unix(receipt.AppliedAt.Unix(), 0)
	}
	if !receipt.ExpiresAt.IsZero() {
		receipt.ExpiresAt = time.Unix(receipt.ExpiresAt.Unix(), 0)
	}
	return receipt
}

func cloneWhiteListSidecarDesired(desired WhiteListSidecarDesired) WhiteListSidecarDesired {
	desired.StaticUsers = append([]string(nil), desired.StaticUsers...)
	desired.ManagedUsers = append([]string(nil), desired.ManagedUsers...)
	desired.PayloadJSON = append([]byte(nil), desired.PayloadJSON...)
	desired.Action.Request = append([]byte(nil), desired.Action.Request...)
	return desired
}

// ResolveWhiteListSidecarUnknown resolves an ambiguous provider outcome only
// by reading the durable receipt bound to the same immutable action key. It
// performs no write and therefore cannot resend the sidecar action.
func (s *Service) ResolveWhiteListSidecarUnknown(
	ctx context.Context, desired WhiteListSidecarDesired, currentBootID string,
) (WhiteListSidecarReceipt, error) {
	if s == nil || s.store == nil || s.clock == nil || desired.Action.ActionKey == "" {
		return WhiteListSidecarReceipt{}, errors.New("controlplane: white-list sidecar receipt service is unavailable")
	}
	results, err := s.store.db.QueryLinearizable(ctx, whiteListSidecarReceiptRead(desired.Action.ActionKey))
	if err != nil {
		return WhiteListSidecarReceipt{}, ErrUnavailable
	}
	receipt, err := whiteListSidecarReceiptFromResults(results)
	if err != nil {
		return WhiteListSidecarReceipt{}, err
	}
	if err := ValidateWhiteListSidecarReceipt(desired, currentBootID, receipt, s.clock.Now()); err != nil {
		return WhiteListSidecarReceipt{}, err
	}
	return receipt, nil
}

func whiteListSidecarReceiptRead(actionKey string) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT action_key,origin_id,release_id,xray_process_boot_id,config_digest,
desired_generation,managed_user_set_digest,applied_at_unix,expires_at_unix
FROM whitelist_sidecar_receipts WHERE action_key=?`, Args: []any{actionKey}}
}

func whiteListSidecarReceiptFromResults(results []rqlite.Result) (WhiteListSidecarReceipt, error) {
	row, ok := firstRow(results)
	if !ok {
		return WhiteListSidecarReceipt{}, ErrUnavailable
	}
	actionKey, actionOK := rowString(row, "action_key")
	originID, originOK := rowString(row, "origin_id")
	releaseID, releaseOK := rowString(row, "release_id")
	bootID, bootOK := rowString(row, "xray_process_boot_id")
	configDigest, configOK := rowString(row, "config_digest")
	managedDigest, managedOK := rowString(row, "managed_user_set_digest")
	generation, generationOK := rowInt64(row, "desired_generation")
	appliedAt, appliedOK := rowInt64(row, "applied_at_unix")
	expiresAt, expiresOK := rowInt64(row, "expires_at_unix")
	if !actionOK || !originOK || !releaseOK || !bootOK || !configOK || !managedOK ||
		!generationOK || !appliedOK || !expiresOK {
		return WhiteListSidecarReceipt{}, ErrUnavailable
	}
	return WhiteListSidecarReceipt{
		ActionKey: actionKey, OriginID: originID, ReleaseID: releaseID, XrayProcessBootID: bootID,
		ConfigDigest: configDigest, DesiredGeneration: generation, ManagedUserSetDigest: managedDigest,
		AppliedAt: time.Unix(appliedAt, 0), ExpiresAt: time.Unix(expiresAt, 0),
	}, nil
}
