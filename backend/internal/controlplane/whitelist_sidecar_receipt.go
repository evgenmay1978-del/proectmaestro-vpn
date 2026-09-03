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
	if !whiteListSidecarReceiptPersistedEqual(existing, replay) {
		return WhiteListSidecarReceipt{}, ErrConflict
	}
	return existing, nil
}

// WhiteListSidecarCurrentState is constructed only from the complete active
// Origin inventory and the latest linearizable durable desired row for each.
// Its fields stay private so readiness cannot consume an unchecked slice.
type WhiteListSidecarCurrentState struct {
	desired []WhiteListSidecarDesired
	exitID  string
}

func NewWhiteListSidecarCurrentState(
	origins []WhiteListOrigin, latestDesired []WhiteListSidecarDesired,
	latestDurableGeneration map[string]int64,
) (WhiteListSidecarCurrentState, error) {
	active := make(map[string]WhiteListOrigin, len(origins))
	for _, origin := range origins {
		if !origin.Active {
			continue
		}
		if origin.OriginID == "" || origin.NodeID == "" || origin.ReleaseID == "" ||
			origin.ProfileID == "" || origin.PresetID == "" || !whiteListDigestValid(origin.ConfigDigest) {
			return WhiteListSidecarCurrentState{}, errors.New("controlplane: invalid current white-list origin")
		}
		if _, exists := active[origin.OriginID]; exists {
			return WhiteListSidecarCurrentState{}, ErrConflict
		}
		active[origin.OriginID] = origin
	}
	if len(active) == 0 || len(latestDesired) != len(active) || len(latestDurableGeneration) != len(active) {
		return WhiteListSidecarCurrentState{}, errors.New("controlplane: incomplete current white-list state")
	}
	state := WhiteListSidecarCurrentState{desired: make([]WhiteListSidecarDesired, 0, len(latestDesired))}
	seen := make(map[string]struct{}, len(latestDesired))
	for _, desired := range latestDesired {
		origin, ok := active[desired.OriginID]
		if !ok {
			return WhiteListSidecarCurrentState{}, errors.New("controlplane: desired row is not for an active Origin")
		}
		if _, duplicate := seen[desired.OriginID]; duplicate {
			return WhiteListSidecarCurrentState{}, ErrConflict
		}
		latestGeneration, generationOK := latestDurableGeneration[desired.OriginID]
		if err := validateWhiteListSidecarDesired(desired); err != nil || !generationOK ||
			desired.Generation != latestGeneration ||
			desired.NodeID != origin.NodeID || desired.ReleaseID != origin.ReleaseID ||
			desired.ProfileID != origin.ProfileID || desired.PresetID != origin.PresetID ||
			desired.ConfigDigest != origin.ConfigDigest ||
			!whiteListStringsEqual(desired.StaticUsers, whiteListSortedUnique(origin.StaticUsers)) {
			return WhiteListSidecarCurrentState{}, errors.New("controlplane: stale current white-list state")
		}
		if state.exitID == "" {
			state.exitID = desired.ExitID
		} else if state.exitID != desired.ExitID {
			return WhiteListSidecarCurrentState{}, errors.New("controlplane: mixed current white-list exits")
		}
		seen[desired.OriginID] = struct{}{}
		state.desired = append(state.desired, cloneWhiteListSidecarDesired(desired))
	}
	return state, nil
}

func EvaluateWhiteListSidecarReadiness(
	state WhiteListSidecarCurrentState, bootIDs map[string]string, receipts []WhiteListSidecarReceipt,
	exit WhiteListExit, now time.Time,
) (bool, error) {
	if !exit.Healthy || exit.ExitID == "" || state.exitID != exit.ExitID || len(state.desired) == 0 {
		return false, nil
	}
	expectedActions := make(map[string]struct{}, len(state.desired))
	for _, desired := range state.desired {
		expectedActions[desired.Action.ActionKey] = struct{}{}
	}
	byAction := make(map[string]WhiteListSidecarReceipt, len(state.desired))
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
	for _, target := range state.desired {
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
