package controlplane

import (
	"context"
	"errors"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type WhiteListSidecarReceipt struct {
	ActionKey            string
	OriginID             string
	ReleaseID            string
	XrayProcessBootID    string
	ConfigDigest         string
	DesiredGeneration    int64
	ManagedUserSetDigest string
	AppliedAt            time.Time
	ExpiresAt            time.Time
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
	if existing != replay {
		return WhiteListSidecarReceipt{}, ErrConflict
	}
	return existing, nil
}

func EvaluateWhiteListSidecarReadiness(
	desired []WhiteListSidecarDesired, bootIDs map[string]string, receipts []WhiteListSidecarReceipt,
	exit WhiteListExit, now time.Time,
) (bool, error) {
	if !exit.Healthy || len(desired) == 0 {
		return false, nil
	}
	byOrigin := make(map[string]WhiteListSidecarReceipt, len(receipts))
	for _, receipt := range receipts {
		if existing, ok := byOrigin[receipt.OriginID]; ok {
			if _, err := ReplayWhiteListSidecarReceipt(existing, receipt); err != nil {
				return false, err
			}
			continue
		}
		byOrigin[receipt.OriginID] = receipt
	}
	for _, target := range desired {
		receipt, ok := byOrigin[target.OriginID]
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
