package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type RestoreState struct {
	ClusterID                string
	RestoreEpoch             int64
	RestoredFromBackupSHA256 string
	Activated                bool
	CreatedAtUnix            int64
	ActivatedAtUnix          int64
}

type RestoreEpochStore struct {
	db  rqlite.RQLite
	now func() time.Time
}

func NewRestoreEpochStore(db rqlite.RQLite) *RestoreEpochStore {
	return &RestoreEpochStore{db: db, now: time.Now}
}

func (s *RestoreEpochStore) Current(ctx context.Context) (RestoreState, error) {
	if s == nil || s.db == nil {
		return RestoreState{}, errors.New("controlplane: restore database is required")
	}
	results, err := s.db.QueryLinearizable(ctx, restoreStateSelect())
	if err != nil {
		return RestoreState{}, errors.New("controlplane: read restore state")
	}
	return exactRestoreState(results)
}

func (s *RestoreEpochStore) AdvanceAfterRestore(ctx context.Context, expected int64, digest string) (RestoreState, error) {
	if s == nil || s.db == nil || expected <= 0 || expected == math.MaxInt64 || !canonicalRestoreHex(digest) {
		return RestoreState{}, errors.New("controlplane: restore transition is invalid")
	}
	next, now := expected+1, s.now().Unix()
	gate := `EXISTS (SELECT 1 FROM cluster_restore_state
		WHERE singleton_id = 1 AND restore_epoch = ? AND activated = 0
		AND restored_from_backup_sha256 = ?)`
	statements := []rqlite.Statement{
		{SQL: `UPDATE cluster_restore_state
			SET restore_epoch = ?, restored_from_backup_sha256 = ?, activated = 0,
				created_at_unix = ?, activated_at_unix = NULL
			WHERE singleton_id = 1 AND restore_epoch = ? AND activated = 1
			  AND EXISTS(SELECT 1 FROM backup_rpo_state
			      WHERE singleton_id=1 AND restore_epoch=?)
			RETURNING cluster_id,restore_epoch,restored_from_backup_sha256,
				activated,created_at_unix,activated_at_unix`,
			Args: []any{next, digest, now, expected, expected}},
		{SQL: `UPDATE backup_rpo_attempts
			SET phase='superseded',failure_code='restore-epoch',updated_at_unix=?
			WHERE restore_epoch=? AND phase IN ('pending','applying','applied','unknown')
			  AND ` + gate, Args: []any{now, expected, next, digest}},
		{SQL: `UPDATE backup_rpo_state
			SET restore_epoch=?,dirty_generation=1,verified_generation=0,
				verified_backup_id=NULL,verified_object_key=NULL,
				verified_object_sha256=NULL,verified_object_version=NULL,
				verified_size_bytes=NULL,verified_manifest_version=NULL,
				verified_at_unix=NULL,last_attempt_sequence=0,
				phase='dirty',updated_at_unix=?
			WHERE singleton_id=1 AND restore_epoch=? AND ` + gate + `
			RETURNING restore_epoch,dirty_generation,verified_generation,
				last_attempt_sequence,phase`, Args: []any{next, now, expected, next, digest}},
		{SQL: "DELETE FROM node_leases WHERE " + gate, Args: []any{next, digest}},
		{SQL: "DELETE FROM cluster_job_leases WHERE " + gate, Args: []any{next, digest}},
		{SQL: `UPDATE telegram_pollers
			SET node_id=NULL, lease_token=NULL, lease_expires_at_unix=0,
				lease_fence=lease_fence+1, updated_at_unix=?
			WHERE ` + gate + ` AND (node_id IS NOT NULL OR lease_token IS NOT NULL
				OR lease_expires_at_unix<>0)`, Args: []any{now, next, digest}},
		{SQL: `INSERT INTO backup_rpo_state(
			singleton_id,restore_epoch,dirty_generation,verified_generation,
			last_attempt_sequence,phase,updated_at_unix)
			SELECT 0,1,1,0,0,'dirty',1
			WHERE NOT EXISTS(
				SELECT 1 FROM cluster_restore_state c
				JOIN backup_rpo_state b ON b.singleton_id=1
				WHERE c.singleton_id=1 AND c.restore_epoch=? AND c.activated=0
				  AND c.restored_from_backup_sha256=?
				  AND b.restore_epoch=? AND b.dirty_generation=1
				  AND b.verified_generation=0 AND b.last_attempt_sequence=0
				  AND b.phase='dirty' AND b.verified_backup_id IS NULL
				  AND b.verified_object_key IS NULL AND b.verified_object_sha256 IS NULL
				  AND b.verified_object_version IS NULL AND b.verified_size_bytes IS NULL
				  AND b.verified_manifest_version IS NULL AND b.verified_at_unix IS NULL
				  AND NOT EXISTS(SELECT 1 FROM backup_rpo_attempts
				      WHERE restore_epoch=? AND phase IN ('pending','applying','applied','unknown'))
				  AND NOT EXISTS(SELECT 1 FROM node_leases)
				  AND NOT EXISTS(SELECT 1 FROM cluster_job_leases)
				  AND NOT EXISTS(SELECT 1 FROM telegram_pollers
				      WHERE node_id IS NOT NULL OR lease_token IS NOT NULL OR lease_expires_at_unix<>0)
			)`, Args: []any{next, digest, next, expected}},
	}
	results, err := s.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		if unknownRestoreOutcome(err) {
			state, readErr := s.advancedPostcondition(ctx, expected, next, digest)
			if readErr == nil {
				return state, nil
			}
			return RestoreState{}, errors.New("controlplane: restore epoch outcome is unresolved")
		}
		return RestoreState{}, errors.New("controlplane: advance restore epoch")
	}
	if len(results) != 7 || results[6].RowsAffected != 0 || len(results[6].Rows) != 0 {
		return RestoreState{}, errors.New("controlplane: restore transition result count is invalid")
	}
	state, err := exactRestoreState(results[:1])
	if err != nil || state.RestoreEpoch != next || state.Activated ||
		state.RestoredFromBackupSHA256 != digest {
		return RestoreState{}, errors.New("controlplane: restore epoch compare-and-swap failed")
	}
	backup, ok := firstRow(results[2:3])
	backupEpoch, epochOK := rowInt64(backup, "restore_epoch")
	dirtyGeneration, dirtyOK := rowInt64(backup, "dirty_generation")
	verifiedGeneration, verifiedOK := rowInt64(backup, "verified_generation")
	lastAttempt, attemptOK := rowInt64(backup, "last_attempt_sequence")
	phase, phaseOK := rowString(backup, "phase")
	if !ok || !epochOK || !dirtyOK || !verifiedOK || !attemptOK || !phaseOK ||
		backupEpoch != next || dirtyGeneration != 1 || verifiedGeneration != 0 ||
		lastAttempt != 0 || phase != BackupRPOPhaseDirty {
		return RestoreState{}, errors.New("controlplane: backup RPO restore handoff failed")
	}
	return state, nil
}

func (s *RestoreEpochStore) Activate(ctx context.Context, epoch int64) (RestoreState, error) {
	if s == nil || s.db == nil || epoch <= 0 {
		return RestoreState{}, errors.New("controlplane: restore activation is invalid")
	}
	results, err := s.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `UPDATE cluster_restore_state
			SET activated = 1, activated_at_unix = ?
			WHERE singleton_id = 1 AND restore_epoch = ? AND activated = 0
			RETURNING cluster_id,restore_epoch,restored_from_backup_sha256,
				activated,created_at_unix,activated_at_unix`,
		Args: []any{s.now().Unix(), epoch},
	})
	if err == nil {
		state, parseErr := exactRestoreState(results)
		if parseErr == nil && state.RestoreEpoch == epoch && state.Activated {
			return state, nil
		}
		if len(results) != 1 || len(results[0].Rows) != 0 {
			return RestoreState{}, errors.New("controlplane: restore activation compare-and-swap failed")
		}
	} else if !unknownRestoreOutcome(err) {
		return RestoreState{}, errors.New("controlplane: activate restore epoch")
	}
	state, readErr := s.Current(ctx)
	if readErr == nil && state.RestoreEpoch == epoch && state.Activated {
		return state, nil
	}
	return RestoreState{}, errors.New("controlplane: restore activation outcome is unresolved")
}

func restoreStateSelect() rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT cluster_id,restore_epoch,restored_from_backup_sha256,
		activated,created_at_unix,activated_at_unix
		FROM cluster_restore_state WHERE singleton_id = 1`}
}

func (s *RestoreEpochStore) advancedPostcondition(
	ctx context.Context,
	expected int64,
	next int64,
	digest string,
) (RestoreState, error) {
	results, err := s.db.QueryLinearizable(ctx, restoreAdvanceEvidenceSelect(expected, next, digest))
	if err != nil {
		return RestoreState{}, errors.New("controlplane: read restore handoff evidence")
	}
	state, err := exactRestoreState(results)
	if err != nil || state.RestoreEpoch != next || state.Activated ||
		state.RestoredFromBackupSHA256 != digest {
		return RestoreState{}, errors.New("controlplane: restore handoff evidence is invalid")
	}
	row := results[0].Rows[0]
	backupEpoch, epochOK := rowInt64(row, "backup_restore_epoch")
	dirtyGeneration, dirtyOK := rowInt64(row, "dirty_generation")
	verifiedGeneration, verifiedOK := rowInt64(row, "verified_generation")
	lastAttempt, attemptOK := rowInt64(row, "last_attempt_sequence")
	phase, phaseOK := rowString(row, "phase")
	activeAttempts, activeOK := rowInt64(row, "active_attempts")
	nodeLeases, nodeOK := rowInt64(row, "node_leases")
	jobLeases, jobOK := rowInt64(row, "job_leases")
	leasedPollers, pollerOK := rowInt64(row, "leased_pollers")
	if !epochOK || !dirtyOK || !verifiedOK || !attemptOK || !phaseOK ||
		!activeOK || !nodeOK || !jobOK || !pollerOK || backupEpoch != next ||
		dirtyGeneration != 1 || verifiedGeneration != 0 || lastAttempt != 0 ||
		phase != BackupRPOPhaseDirty || activeAttempts != 0 || nodeLeases != 0 ||
		jobLeases != 0 || leasedPollers != 0 {
		return RestoreState{}, errors.New("controlplane: restore handoff evidence is incomplete")
	}
	return state, nil
}

func restoreAdvanceEvidenceSelect(expected int64, next int64, digest string) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT
		c.cluster_id,c.restore_epoch,c.restored_from_backup_sha256,
		c.activated,c.created_at_unix,c.activated_at_unix,
		b.restore_epoch AS backup_restore_epoch,b.dirty_generation,
		b.verified_generation,b.last_attempt_sequence,b.phase,
		(SELECT COUNT(*) FROM backup_rpo_attempts
		 WHERE restore_epoch=? AND phase IN ('pending','applying','applied','unknown')) AS active_attempts,
		(SELECT COUNT(*) FROM node_leases) AS node_leases,
		(SELECT COUNT(*) FROM cluster_job_leases) AS job_leases,
		(SELECT COUNT(*) FROM telegram_pollers
		 WHERE node_id IS NOT NULL OR lease_token IS NOT NULL OR lease_expires_at_unix<>0) AS leased_pollers
		FROM cluster_restore_state c
		JOIN backup_rpo_state b ON b.singleton_id=1
		WHERE c.singleton_id=1 AND c.restore_epoch=? AND c.activated=0
		  AND c.restored_from_backup_sha256=?
		  AND b.restore_epoch=? AND b.dirty_generation=1
		  AND b.verified_generation=0 AND b.last_attempt_sequence=0 AND b.phase='dirty'
		  AND b.verified_backup_id IS NULL AND b.verified_object_key IS NULL
		  AND b.verified_object_sha256 IS NULL AND b.verified_object_version IS NULL
		  AND b.verified_size_bytes IS NULL AND b.verified_manifest_version IS NULL
		  AND b.verified_at_unix IS NULL
		  AND NOT EXISTS(SELECT 1 FROM backup_rpo_attempts
		      WHERE restore_epoch=? AND phase IN ('pending','applying','applied','unknown'))
		  AND NOT EXISTS(SELECT 1 FROM node_leases)
		  AND NOT EXISTS(SELECT 1 FROM cluster_job_leases)
		  AND NOT EXISTS(SELECT 1 FROM telegram_pollers
		      WHERE node_id IS NOT NULL OR lease_token IS NOT NULL OR lease_expires_at_unix<>0)`,
		Args: []any{expected, next, digest, next, expected}}
}

func exactRestoreState(results []rqlite.Result) (RestoreState, error) {
	if len(results) != 1 || len(results[0].Rows) != 1 {
		return RestoreState{}, errors.New("controlplane: restore state cardinality is invalid")
	}
	row := results[0].Rows[0]
	cluster, ok := row["cluster_id"].(string)
	if !ok || !canonicalRestoreHex(cluster) {
		return RestoreState{}, errors.New("controlplane: restore cluster identity is invalid")
	}
	epoch, ok := restoreInteger(row["restore_epoch"])
	if !ok || epoch <= 0 {
		return RestoreState{}, errors.New("controlplane: restore epoch is invalid")
	}
	activeValue, ok := restoreInteger(row["activated"])
	if !ok || (activeValue != 0 && activeValue != 1) {
		return RestoreState{}, errors.New("controlplane: restore activation is invalid")
	}
	created, ok := restoreInteger(row["created_at_unix"])
	if !ok || created < 0 {
		return RestoreState{}, errors.New("controlplane: restore creation time is invalid")
	}
	digest := ""
	if raw := row["restored_from_backup_sha256"]; raw != nil {
		digest, ok = raw.(string)
		if !ok || !canonicalRestoreHex(digest) {
			return RestoreState{}, errors.New("controlplane: restore backup digest is invalid")
		}
	}
	activatedAt := int64(0)
	if raw := row["activated_at_unix"]; raw != nil {
		activatedAt, ok = restoreInteger(raw)
		if !ok || activatedAt < created {
			return RestoreState{}, errors.New("controlplane: restore activation time is invalid")
		}
	}
	active := activeValue == 1
	if active != (activatedAt != 0) {
		return RestoreState{}, errors.New("controlplane: restore activation fields are inconsistent")
	}
	return RestoreState{ClusterID: cluster, RestoreEpoch: epoch, RestoredFromBackupSHA256: digest,
		Activated: active, CreatedAtUnix: created, ActivatedAtUnix: activatedAt}, nil
}

func restoreInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func canonicalRestoreHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && fmt.Sprintf("%x", decoded) == value
}

func unknownRestoreOutcome(err error) bool {
	var transportErr *rqlite.TransportError
	return errors.As(err, &transportErr) && transportErr.UnknownOutcome
}
