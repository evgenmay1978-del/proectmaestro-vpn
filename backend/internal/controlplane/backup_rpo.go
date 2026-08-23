package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	BackupRPOJobName       = "backup-rpo"
	BackupRPOPhaseDirty    = "dirty"
	BackupRPOPhaseVerified = "verified"
)

var (
	errBackupRPOStateUnavailable  = errors.New("controlplane: backup RPO state unavailable")
	errBackupRPORequestInvalid    = errors.New("controlplane: backup RPO lease request is invalid")
	errBackupRPOLeaseUnavailable  = errors.New("controlplane: backup RPO lease unavailable")
	errBackupRPOOutcomeUnresolved = errors.New("controlplane: backup RPO lease outcome is unresolved")
)

type BackupRPOCapability struct {
	Generation     int64
	EvidenceSHA256 string
	ExpiresAtUnix  int64
}

type BackupRPOLeaseRequest struct {
	HolderID      string
	LeaseToken    string
	RestoreEpoch  int64
	ExpectedFence int64
	TTLSeconds    int64
	Capability    BackupRPOCapability
}

type BackupRPOVerified struct {
	BackupID        string
	ObjectKey       string
	ObjectSHA256    string
	ObjectVersion   string
	SizeBytes       int64
	ManifestVersion int64
	VerifiedAtUnix  int64
}

type BackupRPOLease struct {
	JobName        string
	HolderID       string
	LeaseToken     string
	AcquiredAtUnix int64
	ExpiresAtUnix  int64
	RestoreEpoch   int64
	LeaseFence     int64
	Capability     BackupRPOCapability
	Live           bool
}

type BackupRPOState struct {
	RestoreEpoch        int64
	DirtyGeneration     int64
	VerifiedGeneration  int64
	LastAttemptSequence int64
	Phase               string
	UpdatedAtUnix       int64
	DatabaseNowUnix     int64
	Verified            *BackupRPOVerified
	Lease               *BackupRPOLease
}

type BackupRPOStore struct {
	db rqlite.RQLite
}

func NewBackupRPOStore(db rqlite.RQLite) *BackupRPOStore {
	return &BackupRPOStore{db: db}
}

func (s *BackupRPOStore) Current(ctx context.Context) (BackupRPOState, error) {
	if s == nil || s.db == nil {
		return BackupRPOState{}, errBackupRPOStateUnavailable
	}
	results, err := s.db.QueryLinearizable(ctx, backupRPOStateSelect())
	if err != nil {
		return BackupRPOState{}, errBackupRPOStateUnavailable
	}
	state, ok := parseBackupRPOState(results)
	if !ok {
		return BackupRPOState{}, errBackupRPOStateUnavailable
	}
	return state, nil
}

func (s *BackupRPOStore) AcquireLease(ctx context.Context, request BackupRPOLeaseRequest) (BackupRPOLease, error) {
	if s == nil || s.db == nil || !validBackupRPORequest(request, false) {
		return BackupRPOLease{}, errBackupRPORequestInvalid
	}
	statement := rqlite.Statement{SQL: `INSERT INTO cluster_job_leases(
		job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
		restore_epoch,lease_fence,capability_generation,
		capability_evidence_sha256,capability_expires_at_unix
	)
	SELECT 'backup-rpo',?,?,unixepoch(),unixepoch()+?,b.restore_epoch,?+1,?,?,?
	FROM backup_rpo_state b
	JOIN cluster_restore_state cr
		ON cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=b.restore_epoch
	WHERE b.singleton_id=1 AND b.restore_epoch=?
		AND ?>unixepoch() AND ?>=unixepoch()+?
		AND (?=0 OR EXISTS (
			SELECT 1 FROM cluster_job_leases existing
			WHERE existing.job_name='backup-rpo'
		))
	ON CONFLICT(job_name) DO UPDATE SET
		holder_id=excluded.holder_id,
		lease_token=excluded.lease_token,
		acquired_at_unix=excluded.acquired_at_unix,
		expires_at_unix=excluded.expires_at_unix,
		restore_epoch=excluded.restore_epoch,
		lease_fence=cluster_job_leases.lease_fence + 1,
		capability_generation=excluded.capability_generation,
		capability_evidence_sha256=excluded.capability_evidence_sha256,
		capability_expires_at_unix=excluded.capability_expires_at_unix
	WHERE cluster_job_leases.restore_epoch=?
		AND cluster_job_leases.lease_fence=?
		AND cluster_job_leases.expires_at_unix<=unixepoch()
	RETURNING job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
		restore_epoch,lease_fence,capability_generation,
		capability_evidence_sha256,capability_expires_at_unix,
		unixepoch() AS database_now_unix`, Args: []any{
		request.HolderID, request.LeaseToken, request.TTLSeconds,
		request.ExpectedFence, request.Capability.Generation,
		request.Capability.EvidenceSHA256, request.Capability.ExpiresAtUnix,
		request.RestoreEpoch, request.Capability.ExpiresAtUnix,
		request.Capability.ExpiresAtUnix, request.TTLSeconds,
		request.ExpectedFence, request.RestoreEpoch, request.ExpectedFence,
	}}
	return s.mutateLease(ctx, statement, request, request.ExpectedFence+1)
}

func (s *BackupRPOStore) RenewLease(ctx context.Context, request BackupRPOLeaseRequest) (BackupRPOLease, error) {
	if s == nil || s.db == nil || !validBackupRPORequest(request, true) {
		return BackupRPOLease{}, errBackupRPORequestInvalid
	}
	statement := rqlite.Statement{SQL: `UPDATE cluster_job_leases SET expires_at_unix=unixepoch()+? WHERE job_name='backup-rpo'
		AND holder_id=? AND lease_token=?
		AND restore_epoch=? AND lease_fence=?
		AND capability_generation=?
		AND capability_evidence_sha256=?
		AND capability_expires_at_unix=?
		AND expires_at_unix>unixepoch()
		AND capability_expires_at_unix>unixepoch()
		AND capability_expires_at_unix>=unixepoch()+?
		AND EXISTS (
			SELECT 1
			FROM cluster_restore_state cr
			JOIN backup_rpo_state b
				ON b.singleton_id=1 AND b.restore_epoch=cr.restore_epoch
			WHERE cr.singleton_id=1 AND cr.activated=1
				AND cr.restore_epoch=?
		)
	RETURNING job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
		restore_epoch,lease_fence,capability_generation,
		capability_evidence_sha256,capability_expires_at_unix,
		unixepoch() AS database_now_unix`, Args: []any{
		request.TTLSeconds, request.HolderID, request.LeaseToken,
		request.RestoreEpoch, request.ExpectedFence,
		request.Capability.Generation, request.Capability.EvidenceSHA256,
		request.Capability.ExpiresAtUnix, request.TTLSeconds,
		request.RestoreEpoch,
	}}
	return s.mutateLease(ctx, statement, request, request.ExpectedFence)
}

func (s *BackupRPOStore) mutateLease(
	ctx context.Context,
	statement rqlite.Statement,
	request BackupRPOLeaseRequest,
	expectedFence int64,
) (BackupRPOLease, error) {
	results, err := s.db.Request(ctx, rqlite.Linearizable, true, statement)
	if err != nil {
		if unknownBackupRPOOutcome(err) {
			return s.resolveLeaseOutcome(ctx, request, expectedFence)
		}
		return BackupRPOLease{}, errBackupRPOLeaseUnavailable
	}
	if len(results) != 1 {
		return BackupRPOLease{}, errBackupRPOLeaseUnavailable
	}
	if len(results[0].Rows) == 0 {
		return BackupRPOLease{}, ErrConflict
	}
	if len(results[0].Rows) != 1 {
		return BackupRPOLease{}, errBackupRPOLeaseUnavailable
	}
	lease, ok := parseBackupRPOLease(results[0].Rows[0], "")
	if !ok || !leaseMatchesRequest(lease, request, expectedFence) || !lease.Live {
		return BackupRPOLease{}, errBackupRPOLeaseUnavailable
	}
	return lease, nil
}

func (s *BackupRPOStore) resolveLeaseOutcome(
	ctx context.Context,
	request BackupRPOLeaseRequest,
	expectedFence int64,
) (BackupRPOLease, error) {
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT
		l.job_name,l.holder_id,l.lease_token,l.acquired_at_unix,l.expires_at_unix,
		l.restore_epoch,l.lease_fence,l.capability_generation,
		l.capability_evidence_sha256,l.capability_expires_at_unix,
		unixepoch() AS database_now_unix
	FROM cluster_job_leases l
	JOIN cluster_restore_state cr
		ON cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=l.restore_epoch
	JOIN backup_rpo_state b
		ON b.singleton_id=1 AND b.restore_epoch=l.restore_epoch
	WHERE l.job_name='backup-rpo'
		AND l.holder_id=? AND l.lease_token=?
		AND l.restore_epoch=? AND l.lease_fence=?
		AND l.capability_generation=?
		AND l.capability_evidence_sha256=?
		AND l.capability_expires_at_unix=?
		AND l.expires_at_unix>unixepoch()
		AND l.capability_expires_at_unix>unixepoch()`, Args: []any{
		request.HolderID, request.LeaseToken, request.RestoreEpoch, expectedFence,
		request.Capability.Generation, request.Capability.EvidenceSHA256,
		request.Capability.ExpiresAtUnix,
	}})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return BackupRPOLease{}, errBackupRPOOutcomeUnresolved
	}
	lease, ok := parseBackupRPOLease(results[0].Rows[0], "")
	if !ok || !lease.Live || !leaseMatchesRequest(lease, request, expectedFence) {
		return BackupRPOLease{}, errBackupRPOOutcomeUnresolved
	}
	return lease, nil
}

func backupRPOStateSelect() rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT
		b.restore_epoch,b.dirty_generation,b.verified_generation,
		b.verified_backup_id,b.verified_object_key,b.verified_object_sha256,
		b.verified_object_version,b.verified_size_bytes,b.verified_manifest_version,
		b.verified_at_unix,b.last_attempt_sequence,b.phase,b.updated_at_unix,
		unixepoch() AS database_now_unix,
		l.job_name AS lease_job_name,l.holder_id AS lease_holder_id,
		l.lease_token AS lease_token,l.acquired_at_unix AS lease_acquired_at_unix,
		l.expires_at_unix AS lease_expires_at_unix,l.restore_epoch AS lease_restore_epoch,
		l.lease_fence AS lease_fence,
		l.capability_generation AS lease_capability_generation,
		l.capability_evidence_sha256 AS lease_capability_evidence_sha256,
		l.capability_expires_at_unix AS lease_capability_expires_at_unix
	FROM backup_rpo_state b
	JOIN cluster_restore_state cr
		ON cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=b.restore_epoch
	LEFT JOIN cluster_job_leases l ON l.job_name='backup-rpo'
	WHERE b.singleton_id=1`}
}

func parseBackupRPOState(results []rqlite.Result) (BackupRPOState, bool) {
	if len(results) != 1 || len(results[0].Rows) != 1 {
		return BackupRPOState{}, false
	}
	row := results[0].Rows[0]
	restoreEpoch, ok := strictBackupRPOIntegerAt(row, "restore_epoch")
	if !ok || restoreEpoch <= 0 {
		return BackupRPOState{}, false
	}
	dirty, ok := strictBackupRPOIntegerAt(row, "dirty_generation")
	if !ok || dirty < 0 {
		return BackupRPOState{}, false
	}
	verifiedGeneration, ok := strictBackupRPOIntegerAt(row, "verified_generation")
	if !ok || verifiedGeneration < 0 || verifiedGeneration > dirty {
		return BackupRPOState{}, false
	}
	lastAttempt, ok := strictBackupRPOIntegerAt(row, "last_attempt_sequence")
	if !ok || lastAttempt < 0 {
		return BackupRPOState{}, false
	}
	updated, ok := strictBackupRPOIntegerAt(row, "updated_at_unix")
	if !ok || updated <= 0 {
		return BackupRPOState{}, false
	}
	databaseNow, ok := strictBackupRPOIntegerAt(row, "database_now_unix")
	if !ok || databaseNow <= 0 {
		return BackupRPOState{}, false
	}
	phase, ok := exactBackupRPOStringAt(row, "phase")
	if !ok || (dirty > verifiedGeneration && phase != BackupRPOPhaseDirty) ||
		(dirty == verifiedGeneration && phase != BackupRPOPhaseVerified) {
		return BackupRPOState{}, false
	}
	verified, ok := parseBackupRPOVerified(row, verifiedGeneration)
	if !ok {
		return BackupRPOState{}, false
	}
	lease, ok := parseOptionalBackupRPOLease(row)
	if !ok || (lease != nil && lease.RestoreEpoch != restoreEpoch) {
		return BackupRPOState{}, false
	}
	return BackupRPOState{
		RestoreEpoch: restoreEpoch, DirtyGeneration: dirty,
		VerifiedGeneration: verifiedGeneration, LastAttemptSequence: lastAttempt,
		Phase: phase, UpdatedAtUnix: updated, DatabaseNowUnix: databaseNow,
		Verified: verified, Lease: lease,
	}, true
}

func parseBackupRPOVerified(row map[string]any, generation int64) (*BackupRPOVerified, bool) {
	keys := []string{
		"verified_backup_id", "verified_object_key", "verified_object_sha256",
		"verified_object_version", "verified_size_bytes", "verified_manifest_version",
		"verified_at_unix",
	}
	if generation == 0 {
		for _, key := range keys {
			value, exists := row[key]
			if !exists || value != nil {
				return nil, false
			}
		}
		return nil, true
	}
	backupID, ok := exactBackupRPOStringAt(row, "verified_backup_id")
	if !ok || !canonicalLowerHex(backupID, 32) {
		return nil, false
	}
	objectKey, ok := exactBackupRPOStringAt(row, "verified_object_key")
	if !ok || objectKey == "" || strings.TrimSpace(objectKey) != objectKey {
		return nil, false
	}
	objectSHA, ok := exactBackupRPOStringAt(row, "verified_object_sha256")
	if !ok || !canonicalLowerHex(objectSHA, 64) {
		return nil, false
	}
	objectVersion, ok := exactBackupRPOStringAt(row, "verified_object_version")
	if !ok || !validObjectVersion(objectVersion) {
		return nil, false
	}
	size, ok := strictBackupRPOIntegerAt(row, "verified_size_bytes")
	if !ok || size <= 0 {
		return nil, false
	}
	manifest, ok := strictBackupRPOIntegerAt(row, "verified_manifest_version")
	if !ok || manifest != 2 {
		return nil, false
	}
	verifiedAt, ok := strictBackupRPOIntegerAt(row, "verified_at_unix")
	if !ok || verifiedAt <= 0 {
		return nil, false
	}
	return &BackupRPOVerified{
		BackupID: backupID, ObjectKey: objectKey, ObjectSHA256: objectSHA,
		ObjectVersion: objectVersion, SizeBytes: size,
		ManifestVersion: manifest, VerifiedAtUnix: verifiedAt,
	}, true
}

func parseOptionalBackupRPOLease(row map[string]any) (*BackupRPOLease, bool) {
	keys := []string{
		"lease_job_name", "lease_holder_id", "lease_token",
		"lease_acquired_at_unix", "lease_expires_at_unix", "lease_restore_epoch",
		"lease_fence", "lease_capability_generation",
		"lease_capability_evidence_sha256", "lease_capability_expires_at_unix",
	}
	allNil := true
	for _, key := range keys {
		value, exists := row[key]
		if !exists {
			return nil, false
		}
		if value != nil {
			allNil = false
		}
	}
	if allNil {
		return nil, true
	}
	lease, ok := parseBackupRPOLease(row, "lease_")
	if !ok {
		return nil, false
	}
	return &lease, true
}

func backupRPOLeaseIdentityKey(prefix, field string) string {
	if prefix == "lease_" {
		return field
	}
	return prefix + field
}

func parseBackupRPOLease(row map[string]any, prefix string) (BackupRPOLease, bool) {
	jobName, ok := exactBackupRPOStringAt(row, prefix+"job_name")
	if !ok || jobName != BackupRPOJobName {
		return BackupRPOLease{}, false
	}
	holderID, ok := exactBackupRPOStringAt(row, prefix+"holder_id")
	if !ok || holderID == "" || strings.TrimSpace(holderID) != holderID {
		return BackupRPOLease{}, false
	}
	leaseToken, ok := exactBackupRPOStringAt(row, backupRPOLeaseIdentityKey(prefix, "lease_token"))
	if !ok || leaseToken == "" || strings.TrimSpace(leaseToken) != leaseToken {
		return BackupRPOLease{}, false
	}
	acquired, ok := strictBackupRPOIntegerAt(row, prefix+"acquired_at_unix")
	if !ok || acquired <= 0 {
		return BackupRPOLease{}, false
	}
	expires, ok := strictBackupRPOIntegerAt(row, prefix+"expires_at_unix")
	if !ok || expires <= acquired {
		return BackupRPOLease{}, false
	}
	restoreEpoch, ok := strictBackupRPOIntegerAt(row, prefix+"restore_epoch")
	if !ok || restoreEpoch <= 0 {
		return BackupRPOLease{}, false
	}
	fence, ok := strictBackupRPOIntegerAt(row, backupRPOLeaseIdentityKey(prefix, "lease_fence"))
	if !ok || fence <= 0 {
		return BackupRPOLease{}, false
	}
	capabilityGeneration, ok := strictBackupRPOIntegerAt(row, prefix+"capability_generation")
	if !ok || capabilityGeneration <= 0 {
		return BackupRPOLease{}, false
	}
	capabilityEvidence, ok := exactBackupRPOStringAt(row, prefix+"capability_evidence_sha256")
	if !ok || !canonicalLowerHex(capabilityEvidence, 64) {
		return BackupRPOLease{}, false
	}
	capabilityExpires, ok := strictBackupRPOIntegerAt(row, prefix+"capability_expires_at_unix")
	if !ok || capabilityExpires < expires {
		return BackupRPOLease{}, false
	}
	databaseNow, ok := strictBackupRPOIntegerAt(row, "database_now_unix")
	if !ok || databaseNow <= 0 {
		return BackupRPOLease{}, false
	}
	return BackupRPOLease{
		JobName: jobName, HolderID: holderID, LeaseToken: leaseToken,
		AcquiredAtUnix: acquired, ExpiresAtUnix: expires, RestoreEpoch: restoreEpoch,
		LeaseFence: fence, Capability: BackupRPOCapability{
			Generation: capabilityGeneration, EvidenceSHA256: capabilityEvidence,
			ExpiresAtUnix: capabilityExpires,
		}, Live: expires > databaseNow && capabilityExpires > databaseNow,
	}, true
}

func validBackupRPORequest(request BackupRPOLeaseRequest, renewal bool) bool {
	if request.HolderID == "" || strings.TrimSpace(request.HolderID) != request.HolderID ||
		request.LeaseToken == "" || strings.TrimSpace(request.LeaseToken) != request.LeaseToken ||
		request.RestoreEpoch <= 0 || request.ExpectedFence < 0 ||
		request.ExpectedFence == math.MaxInt64 || request.TTLSeconds <= 0 ||
		request.Capability.Generation <= 0 ||
		!canonicalLowerHex(request.Capability.EvidenceSHA256, 64) ||
		request.Capability.ExpiresAtUnix <= 0 {
		return false
	}
	return !renewal || request.ExpectedFence > 0
}

func leaseMatchesRequest(lease BackupRPOLease, request BackupRPOLeaseRequest, fence int64) bool {
	return lease.JobName == BackupRPOJobName &&
		lease.HolderID == request.HolderID && lease.LeaseToken == request.LeaseToken &&
		lease.RestoreEpoch == request.RestoreEpoch && lease.LeaseFence == fence &&
		lease.Capability == request.Capability
}

func strictBackupRPOIntegerAt(row map[string]any, key string) (int64, bool) {
	value, exists := row[key]
	if !exists || value == nil {
		return 0, false
	}
	return strictBackupRPOInteger(value)
}

func strictBackupRPOInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case string:
		return canonicalBackupRPOInteger(typed)
	case json.Number:
		return canonicalBackupRPOInteger(string(typed))
	default:
		return 0, false
	}
}

func canonicalBackupRPOInteger(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value {
		return 0, false
	}
	return parsed, true
}

func exactBackupRPOStringAt(row map[string]any, key string) (string, bool) {
	value, exists := row[key]
	if !exists || value == nil {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func canonicalLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == length && hex.EncodeToString(decoded) == value
}

func validObjectVersion(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	switch strings.ToLower(value) {
	case "latest", "null", "none":
		return false
	default:
		return true
	}
}

func unknownBackupRPOOutcome(err error) bool {
	var transportErr *rqlite.TransportError
	return errors.As(err, &transportErr) && transportErr.UnknownOutcome
}
