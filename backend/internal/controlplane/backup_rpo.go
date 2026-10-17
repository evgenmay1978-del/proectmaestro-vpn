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
	BackupRPOJobName                     = "backup-rpo"
	BackupRPOPhaseDirty                  = "dirty"
	BackupRPOPhaseVerified               = "verified"
	BackupRPOMaxVerifiedAgeSeconds int64 = 60 * 60

	BackupRPOAdapterYandexS3V1 = "yandex-s3-v1"
	BackupRPOAttemptPending    = "pending"
	BackupRPOAttemptApplying   = "applying"
	BackupRPOAttemptApplied    = "applied"
	BackupRPOAttemptUnknown    = "unknown"
	BackupRPOAttemptVerified   = "verified"
	BackupRPOAttemptSuperseded = "superseded"
	BackupRPOAttemptFailed     = "failed"
	BackupRPOFailureStaleFence = "stale-fence"
)

var (
	errBackupRPOStateUnavailable         = errors.New("controlplane: backup RPO state unavailable")
	errBackupRPORequestInvalid           = errors.New("controlplane: backup RPO lease request is invalid")
	errBackupRPOLeaseUnavailable         = errors.New("controlplane: backup RPO lease unavailable")
	errBackupRPOOutcomeUnresolved        = errors.New("controlplane: backup RPO lease outcome is unresolved")
	errBackupRPOAttemptRequestInvalid    = errors.New("controlplane: backup RPO attempt request is invalid")
	errBackupRPOAttemptUnavailable       = errors.New("controlplane: backup RPO attempt unavailable")
	errBackupRPOAttemptOutcomeUnresolved = errors.New("controlplane: backup RPO attempt outcome is unresolved")
)

// backupRPODirtyGenerationStatement must be placed immediately after the
// authoritative business mutation in the same rqlite transaction. SQLite's
// changes() then proves that mutation changed durable state before the backup
// generation advances.
func backupRPODirtyGenerationStatement(updatedAtUnix int64) rqlite.Statement {
	return rqlite.Statement{
		SQL: `UPDATE backup_rpo_state AS b
SET dirty_generation = b.dirty_generation + 1,
phase = 'dirty',
	updated_at_unix = ?
WHERE b.singleton_id = 1
	AND changes() > 0
	AND EXISTS (
		SELECT 1 FROM cluster_restore_state AS cr
		WHERE cr.singleton_id = 1 AND cr.activated = 1
			AND cr.restore_epoch = b.restore_epoch
	)
RETURNING dirty_generation`,
		Args: []any{updatedAtUnix},
	}
}

// backupRPOSettingDirtyGenerationStatement keeps the immediate changes()
// proof and additionally binds the dirty marker to this setting request's CAS.
func backupRPOSettingDirtyGenerationStatement(updatedAtUnix int64, settingKey string, generation int64, mutationToken string) rqlite.Statement {
	return rqlite.Statement{
		SQL: `UPDATE backup_rpo_state AS b
SET dirty_generation = b.dirty_generation + 1,
phase = 'dirty',
	updated_at_unix = ?
WHERE b.singleton_id = 1
	AND changes() > 0
	AND EXISTS (
		SELECT 1 FROM cluster_settings AS cs
		WHERE cs.setting_key = ? AND cs.generation = ?
			AND cs.last_mutation_token = ?
	)
	AND EXISTS (
		SELECT 1 FROM cluster_restore_state AS cr
		WHERE cr.singleton_id = 1 AND cr.activated = 1
			AND cr.restore_epoch = b.restore_epoch
	)
RETURNING dirty_generation`,
		Args: []any{updatedAtUnix, settingKey, generation, mutationToken},
	}
}

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

type BackupRPOCycle struct {
	State         BackupRPOState
	ActiveAttempt *BackupRPOAttempt
}

type BackupRPOAttemptIdentity struct {
	HolderID               string
	LeaseToken             string
	RestoreEpoch           int64
	LeaseFence             int64
	Capability             BackupRPOCapability
	CapturedGeneration     int64
	AttemptSequence        int64
	BackupID               string
	ObjectKey              string
	ObjectSHA256           string
	ObjectSizeBytes        int64
	ManifestVersion        int64
	AdapterContractVersion string
}

type BackupRPOAttempt struct {
	Identity        BackupRPOAttemptIdentity
	Phase           string
	ObjectVersion   string
	FailureCode     string
	CreatedAtUnix   int64
	UpdatedAtUnix   int64
	DatabaseNowUnix int64
}

type BackupRPOVersionID struct {
	value string
}

func NewBackupRPOVersionID(value string) (BackupRPOVersionID, error) {
	if !validExactObjectVersion(value) {
		return BackupRPOVersionID{}, errBackupRPOAttemptRequestInvalid
	}
	return BackupRPOVersionID{value: value}, nil
}

func (version BackupRPOVersionID) String() string {
	return version.value
}

func (version BackupRPOVersionID) valid() bool {
	return validExactObjectVersion(version.value)
}

type BackupRPOUploadOutcome struct {
	Identity  BackupRPOAttemptIdentity
	VersionID BackupRPOVersionID
	Unknown   bool
}

type BackupRPOVerification struct {
	Identity                   BackupRPOAttemptIdentity
	VersionID                  BackupRPOVersionID
	FullReadback               bool
	ReadbackSHA256             string
	ReadbackSizeBytes          int64
	ManifestAuthenticated      bool
	ManifestVersion            int64
	ManifestBackupID           string
	ManifestCapturedGeneration int64
	ManifestRestoreEpoch       int64
	ManifestObjectKey          string
	ManifestObjectSHA256       string
	ManifestObjectSizeBytes    int64
}

type BackupRPOSupersedeRequest struct {
	Identity     BackupRPOAttemptIdentity
	CurrentLease BackupRPOLeaseRequest
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

func (s *BackupRPOStore) CurrentCycle(ctx context.Context) (BackupRPOCycle, error) {
	if s == nil || s.db == nil {
		return BackupRPOCycle{}, errBackupRPOStateUnavailable
	}
	results, err := s.db.QueryLinearizable(ctx, backupRPOCycleSelect())
	if err != nil {
		return BackupRPOCycle{}, errBackupRPOStateUnavailable
	}
	cycle, ok := parseBackupRPOCycle(results)
	if !ok {
		return BackupRPOCycle{}, errBackupRPOStateUnavailable
	}
	return cycle, nil
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

func (s *BackupRPOStore) RegisterAttempt(
	ctx context.Context,
	identity BackupRPOAttemptIdentity,
) (BackupRPOAttempt, error) {
	if s == nil || s.db == nil || !isValidBackupRPOAttemptIdentity(identity) {
		return BackupRPOAttempt{}, errBackupRPOAttemptRequestInvalid
	}
	state := rqlite.Statement{SQL: `UPDATE backup_rpo_state AS b
	SET last_attempt_sequence=?,updated_at_unix=unixepoch()
	WHERE b.singleton_id=1 AND b.restore_epoch=? AND b.dirty_generation=?
		AND b.last_attempt_sequence=?-1
		AND (
			(b.dirty_generation>b.verified_generation AND b.phase='dirty')
			OR (
				b.dirty_generation=b.verified_generation
				AND b.phase='verified'
				AND b.verified_backup_id IS NOT NULL
				AND b.verified_object_key IS NOT NULL
				AND b.verified_object_sha256 IS NOT NULL
				AND b.verified_object_version IS NOT NULL
				AND b.verified_size_bytes>0
				AND b.verified_manifest_version=2
				AND b.verified_at_unix>0
				AND b.verified_at_unix<=unixepoch()-?
			)
		)
		AND EXISTS (
			SELECT 1 FROM cluster_restore_state cr
			WHERE cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=b.restore_epoch
		)
		AND EXISTS (
			SELECT 1 FROM cluster_job_leases l
			WHERE l.job_name='backup-rpo' AND l.holder_id=? AND l.lease_token=?
				AND l.restore_epoch=? AND l.lease_fence=?
				AND l.capability_generation=? AND l.capability_evidence_sha256=?
				AND l.capability_expires_at_unix=?
				AND l.expires_at_unix>unixepoch()
				AND l.capability_expires_at_unix>unixepoch()
		)
		AND NOT EXISTS (
			SELECT 1 FROM backup_rpo_attempts active
			WHERE active.restore_epoch=b.restore_epoch
				AND active.phase IN ('pending','applying','applied','unknown')
		)
	RETURNING last_attempt_sequence`, Args: []any{
		identity.AttemptSequence, identity.RestoreEpoch, identity.CapturedGeneration,
		identity.AttemptSequence, BackupRPOMaxVerifiedAgeSeconds,
		identity.HolderID, identity.LeaseToken, identity.RestoreEpoch,
		identity.LeaseFence, identity.Capability.Generation,
		identity.Capability.EvidenceSHA256, identity.Capability.ExpiresAtUnix,
	}}
	insert := rqlite.Statement{SQL: `INSERT INTO backup_rpo_attempts(
		restore_epoch,attempt_sequence,phase,backup_id,captured_generation,
		object_key,object_sha256,object_version,object_size_bytes,
		manifest_version,adapter_contract_version,capability_generation,
		capability_evidence_sha256,capability_expires_at_unix,
		lease_holder_id,lease_token,lease_fence,failure_code,
		created_at_unix,updated_at_unix
	)
	SELECT ?,?,'pending',?,?,?, ?,NULL,?,?,?,?,?,?, ?,?,?,NULL,unixepoch(),unixepoch()
	FROM backup_rpo_state b
	WHERE b.singleton_id=1 AND b.restore_epoch=? AND b.dirty_generation=?
		AND b.last_attempt_sequence=? AND changes()=1
	RETURNING ` + backupRPOAttemptColumns, Args: []any{
		identity.RestoreEpoch, identity.AttemptSequence, identity.BackupID,
		identity.CapturedGeneration, identity.ObjectKey, identity.ObjectSHA256,
		identity.ObjectSizeBytes, identity.ManifestVersion, identity.AdapterContractVersion,
		identity.Capability.Generation, identity.Capability.EvidenceSHA256,
		identity.Capability.ExpiresAtUnix, identity.HolderID, identity.LeaseToken,
		identity.LeaseFence, identity.RestoreEpoch, identity.CapturedGeneration,
		identity.AttemptSequence,
	}}
	return s.mutateAttempt(ctx, identity, attemptExpectation{
		phase: BackupRPOAttemptPending, versionMode: attemptVersionMustBeNull, registered: true,
	}, state, insert)
}

func (s *BackupRPOStore) MarkUploadStarted(
	ctx context.Context,
	identity BackupRPOAttemptIdentity,
) (BackupRPOAttempt, error) {
	if s == nil || s.db == nil || !isValidBackupRPOAttemptIdentity(identity) {
		return BackupRPOAttempt{}, errBackupRPOAttemptRequestInvalid
	}
	statement := rqlite.Statement{SQL: `UPDATE backup_rpo_attempts AS a
	SET phase='applying',updated_at_unix=unixepoch()
	WHERE ` + backupRPOAttemptIdentityWhere("a") + ` AND a.phase='pending'
		` + backupRPOAttemptLiveLeaseWhere("a") + `
	RETURNING ` + backupRPOAttemptColumns, Args: backupRPOAttemptIdentityArgs(identity)}
	return s.mutateAttempt(ctx, identity, attemptExpectation{
		phase: BackupRPOAttemptApplying, versionMode: attemptVersionMustBeNull,
	}, statement)
}

func (s *BackupRPOStore) RecordUploadOutcome(
	ctx context.Context,
	outcome BackupRPOUploadOutcome,
) (BackupRPOAttempt, error) {
	version := outcome.VersionID.String()
	if s == nil || s.db == nil || !isValidBackupRPOAttemptIdentity(outcome.Identity) ||
		(outcome.Unknown && version != "") ||
		(!outcome.Unknown && !outcome.VersionID.valid()) {
		return BackupRPOAttempt{}, errBackupRPOAttemptRequestInvalid
	}
	phase := BackupRPOAttemptApplied
	versionMode := attemptVersionMustEqual
	versionSQL := "object_version=?"
	priorPhaseSQL := "a.phase IN ('applying','unknown')"
	args := append([]any{version}, backupRPOAttemptIdentityArgs(outcome.Identity)...)
	if outcome.Unknown {
		phase = BackupRPOAttemptUnknown
		versionMode = attemptVersionMustBeNull
		versionSQL = "object_version=NULL"
		priorPhaseSQL = "a.phase='applying'"
		args = backupRPOAttemptIdentityArgs(outcome.Identity)
		version = ""
	}
	statement := rqlite.Statement{SQL: `UPDATE backup_rpo_attempts AS a
	SET phase='` + phase + `',` + versionSQL + `,failure_code=NULL,updated_at_unix=unixepoch()
	WHERE ` + backupRPOAttemptIdentityWhere("a") + ` AND ` + priorPhaseSQL + `
		` + backupRPOAttemptLiveLeaseWhere("a") + `
	RETURNING ` + backupRPOAttemptColumns, Args: args}
	return s.mutateAttempt(ctx, outcome.Identity, attemptExpectation{
		phase: phase, versionMode: versionMode, objectVersion: version,
	}, statement)
}

func (s *BackupRPOStore) AcknowledgeVerified(
	ctx context.Context,
	proof BackupRPOVerification,
) (BackupRPOAttempt, error) {
	if s == nil || s.db == nil || !isValidBackupRPOVerification(proof) {
		return BackupRPOAttempt{}, errBackupRPOAttemptRequestInvalid
	}
	identity := proof.Identity
	version := proof.VersionID.String()
	stateArgs := []any{
		identity.CapturedGeneration, identity.BackupID, identity.ObjectKey,
		identity.ObjectSHA256, version, identity.ObjectSizeBytes,
		identity.ManifestVersion, identity.CapturedGeneration,
		identity.RestoreEpoch, identity.CapturedGeneration,
		identity.AttemptSequence, identity.CapturedGeneration,
		identity.CapturedGeneration, BackupRPOMaxVerifiedAgeSeconds,
	}
	stateArgs = append(stateArgs, backupRPOAttemptIdentityArgs(identity)...)
	stateArgs = append(stateArgs, version)
	state := rqlite.Statement{SQL: `UPDATE backup_rpo_state AS b
	SET verified_generation=?,verified_backup_id=?,verified_object_key=?,
		verified_object_sha256=?,verified_object_version=?,verified_size_bytes=?,
		verified_manifest_version=?,verified_at_unix=unixepoch(),
		phase=CASE WHEN dirty_generation=? THEN 'verified' ELSE 'dirty' END,
		updated_at_unix=unixepoch()
	WHERE b.singleton_id=1 AND b.restore_epoch=? AND b.dirty_generation>=?
		AND b.last_attempt_sequence=?
		AND (
			b.verified_generation<?
			OR (b.verified_generation=?
				AND b.verified_at_unix>0
				AND b.verified_at_unix<=unixepoch()-?)
		)
		AND EXISTS (
			SELECT 1 FROM backup_rpo_attempts a
			WHERE ` + backupRPOAttemptIdentityWhere("a") + `
				AND a.phase='applied' AND a.object_version=?
				` + backupRPOAttemptLiveLeaseWhere("a") + `
		)
	RETURNING verified_generation,dirty_generation,phase`, Args: stateArgs}
	attemptArgs := backupRPOAttemptIdentityArgs(identity)
	attemptArgs = append(attemptArgs, version)
	attempt := rqlite.Statement{SQL: `UPDATE backup_rpo_attempts AS a
	SET phase='verified',failure_code=NULL,updated_at_unix=unixepoch()
	WHERE ` + backupRPOAttemptIdentityWhere("a") + `
		AND a.phase='applied' AND a.object_version=? AND changes()=1
	RETURNING ` + backupRPOAttemptColumns, Args: attemptArgs}
	return s.mutateAttempt(ctx, identity, attemptExpectation{
		phase: BackupRPOAttemptVerified, versionMode: attemptVersionMustEqual,
		objectVersion: version, acknowledged: true,
	}, state, attempt)
}

func (s *BackupRPOStore) SupersedeStaleAttempt(
	ctx context.Context,
	request BackupRPOSupersedeRequest,
) (BackupRPOAttempt, error) {
	current := request.CurrentLease
	if s == nil || s.db == nil || !isValidBackupRPOAttemptIdentity(request.Identity) ||
		!validBackupRPORequest(current, true) || current.RestoreEpoch != request.Identity.RestoreEpoch ||
		current.ExpectedFence <= request.Identity.LeaseFence {
		return BackupRPOAttempt{}, errBackupRPOAttemptRequestInvalid
	}
	args := backupRPOAttemptIdentityArgs(request.Identity)
	args = append(args,
		current.HolderID, current.LeaseToken, current.RestoreEpoch, current.ExpectedFence,
		current.Capability.Generation, current.Capability.EvidenceSHA256,
		current.Capability.ExpiresAtUnix, request.Identity.LeaseFence,
	)
	statement := rqlite.Statement{SQL: `UPDATE backup_rpo_attempts AS a
	SET phase='superseded',failure_code='stale-fence',updated_at_unix=unixepoch()
	WHERE ` + backupRPOAttemptIdentityWhere("a") + `
		AND a.phase IN ('pending','applying','applied','unknown')
		AND EXISTS (
			SELECT 1 FROM cluster_job_leases l
			JOIN cluster_restore_state cr
				ON cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=l.restore_epoch
			JOIN backup_rpo_state b
				ON b.singleton_id=1 AND b.restore_epoch=l.restore_epoch
			WHERE l.job_name='backup-rpo' AND l.holder_id=? AND l.lease_token=?
				AND l.restore_epoch=? AND l.lease_fence=?
				AND l.capability_generation=? AND l.capability_evidence_sha256=?
				AND l.capability_expires_at_unix=? AND l.lease_fence>?
				AND l.lease_fence>a.lease_fence
				AND l.expires_at_unix>unixepoch()
				AND l.capability_expires_at_unix>unixepoch()
		)
	RETURNING ` + backupRPOAttemptColumns, Args: args}
	return s.mutateAttempt(ctx, request.Identity, attemptExpectation{
		phase: BackupRPOAttemptSuperseded, versionMode: attemptVersionPreserveAnyValid,
		failureCode: BackupRPOFailureStaleFence,
	}, statement)
}

type attemptVersionExpectation uint8

const (
	attemptVersionMustBeNull attemptVersionExpectation = iota + 1
	attemptVersionMustEqual
	attemptVersionPreserveAnyValid
)

type attemptExpectation struct {
	phase         string
	versionMode   attemptVersionExpectation
	objectVersion string
	failureCode   string
	registered    bool
	acknowledged  bool
}

func (s *BackupRPOStore) mutateAttempt(
	ctx context.Context,
	identity BackupRPOAttemptIdentity,
	expected attemptExpectation,
	statements ...rqlite.Statement,
) (BackupRPOAttempt, error) {
	results, err := s.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		if unknownBackupRPOOutcome(err) {
			return s.resolveAttemptOutcome(ctx, identity, expected)
		}
		return BackupRPOAttempt{}, errBackupRPOAttemptUnavailable
	}
	if len(results) != len(statements) || len(results) == 0 {
		return BackupRPOAttempt{}, errBackupRPOAttemptUnavailable
	}
	for _, result := range results {
		if len(result.Rows) == 0 {
			return BackupRPOAttempt{}, ErrConflict
		}
		if len(result.Rows) != 1 {
			return BackupRPOAttempt{}, errBackupRPOAttemptUnavailable
		}
	}
	attempt, ok := parseBackupRPOAttempt(results[len(results)-1].Rows[0])
	if !ok || !attemptMatchesExpectation(attempt, identity, expected) {
		return BackupRPOAttempt{}, errBackupRPOAttemptUnavailable
	}
	return attempt, nil
}

func (s *BackupRPOStore) resolveAttemptOutcome(
	ctx context.Context,
	identity BackupRPOAttemptIdentity,
	expected attemptExpectation,
) (BackupRPOAttempt, error) {
	args := backupRPOAttemptIdentityArgs(identity)
	args = append(args, expected.phase)
	versionPredicate := ""
	switch expected.versionMode {
	case attemptVersionMustBeNull:
		versionPredicate = " AND a.object_version IS NULL"
	case attemptVersionMustEqual:
		if expected.objectVersion == "" {
			return BackupRPOAttempt{}, errBackupRPOAttemptOutcomeUnresolved
		}
		versionPredicate = " AND a.object_version=?"
		args = append(args, expected.objectVersion)
	case attemptVersionPreserveAnyValid:
	default:
		return BackupRPOAttempt{}, errBackupRPOAttemptOutcomeUnresolved
	}
	failurePredicate := "a.failure_code IS NULL"
	if expected.failureCode != "" {
		failurePredicate = "a.failure_code=?"
		args = append(args, expected.failureCode)
	}
	statePredicate := ""
	if expected.registered {
		statePredicate = ` AND EXISTS (
			SELECT 1 FROM backup_rpo_state b
			WHERE b.singleton_id=1 AND b.restore_epoch=a.restore_epoch
				AND b.last_attempt_sequence>=a.attempt_sequence
		)`
	}
	ackPredicate := ""
	if expected.acknowledged {
		ackPredicate = ` AND EXISTS (
			SELECT 1 FROM backup_rpo_state b
			WHERE b.singleton_id=1 AND b.restore_epoch=a.restore_epoch
				AND b.verified_generation=a.captured_generation
				AND b.verified_backup_id=a.backup_id
				AND b.verified_object_key=a.object_key
				AND b.verified_object_sha256=a.object_sha256
				AND b.verified_object_version=a.object_version
				AND b.verified_size_bytes=a.object_size_bytes
				AND b.verified_manifest_version=a.manifest_version
		)`
	}
	results, err := s.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT ` +
		backupRPOAttemptColumns + ` FROM backup_rpo_attempts a
	WHERE ` + backupRPOAttemptIdentityWhere("a") + ` AND a.phase=?` + versionPredicate + `
		AND ` + failurePredicate + statePredicate + ackPredicate, Args: args})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return BackupRPOAttempt{}, errBackupRPOAttemptOutcomeUnresolved
	}
	attempt, ok := parseBackupRPOAttempt(results[0].Rows[0])
	if !ok || !attemptMatchesExpectation(attempt, identity, expected) {
		return BackupRPOAttempt{}, errBackupRPOAttemptOutcomeUnresolved
	}
	return attempt, nil
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

func backupRPOCycleSelect() rqlite.Statement {
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
		l.capability_expires_at_unix AS lease_capability_expires_at_unix,
		a.restore_epoch AS attempt_restore_epoch,a.attempt_sequence AS attempt_attempt_sequence,
		a.phase AS attempt_phase,a.backup_id AS attempt_backup_id,
		a.captured_generation AS attempt_captured_generation,a.object_key AS attempt_object_key,
		a.object_sha256 AS attempt_object_sha256,a.object_version AS attempt_object_version,
		a.object_size_bytes AS attempt_object_size_bytes,a.manifest_version AS attempt_manifest_version,
		a.adapter_contract_version AS attempt_adapter_contract_version,a.capability_generation AS attempt_capability_generation,
		a.capability_evidence_sha256 AS attempt_capability_evidence_sha256,a.capability_expires_at_unix AS attempt_capability_expires_at_unix,
		a.lease_holder_id AS attempt_lease_holder_id,a.lease_token AS attempt_lease_token,
		a.lease_fence AS attempt_lease_fence,a.failure_code AS attempt_failure_code,
		a.created_at_unix AS attempt_created_at_unix,a.updated_at_unix AS attempt_updated_at_unix
	FROM backup_rpo_state b
	JOIN cluster_restore_state cr
		ON cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=b.restore_epoch
	LEFT JOIN cluster_job_leases l ON l.job_name='backup-rpo'
	LEFT JOIN backup_rpo_attempts a
		ON a.restore_epoch=b.restore_epoch
		AND a.phase IN ('pending','applying','applied','unknown')
	WHERE b.singleton_id=1`}
}

var backupRPOCycleAttemptFields = []string{
	"restore_epoch", "attempt_sequence", "phase", "backup_id",
	"captured_generation", "object_key", "object_sha256", "object_version",
	"object_size_bytes", "manifest_version", "adapter_contract_version",
	"capability_generation", "capability_evidence_sha256", "capability_expires_at_unix",
	"lease_holder_id", "lease_token", "lease_fence", "failure_code",
	"created_at_unix", "updated_at_unix",
}

func parseBackupRPOCycle(results []rqlite.Result) (BackupRPOCycle, bool) {
	state, ok := parseBackupRPOState(results)
	if !ok {
		return BackupRPOCycle{}, false
	}
	row := results[0].Rows[0]
	attemptRow := make(map[string]any, len(backupRPOCycleAttemptFields)+1)
	allNil := true
	for _, field := range backupRPOCycleAttemptFields {
		value, exists := row["attempt_"+field]
		if !exists {
			return BackupRPOCycle{}, false
		}
		if value != nil {
			allNil = false
		}
		attemptRow[field] = value
	}
	cycle := BackupRPOCycle{State: state}
	if allNil {
		return cycle, true
	}
	attemptRow["database_now_unix"] = row["database_now_unix"]
	attempt, ok := parseBackupRPOAttempt(attemptRow)
	if !ok || attempt.FailureCode != "" ||
		attempt.Identity.RestoreEpoch != state.RestoreEpoch ||
		attempt.Identity.AttemptSequence != state.LastAttemptSequence ||
		attempt.Identity.CapturedGeneration < state.VerifiedGeneration ||
		attempt.Identity.CapturedGeneration > state.DirtyGeneration ||
		attempt.DatabaseNowUnix != state.DatabaseNowUnix {
		return BackupRPOCycle{}, false
	}
	if attempt.Identity.CapturedGeneration == state.VerifiedGeneration &&
		!backupRPOHourlyRefreshDue(state) {
		return BackupRPOCycle{}, false
	}
	switch attempt.Phase {
	case BackupRPOAttemptPending, BackupRPOAttemptApplying, BackupRPOAttemptApplied, BackupRPOAttemptUnknown:
	default:
		return BackupRPOCycle{}, false
	}
	cycle.ActiveAttempt = &attempt
	return cycle, true
}

func backupRPOHourlyRefreshDue(state BackupRPOState) bool {
	return state.Verified != nil &&
		state.Verified.VerifiedAtUnix > 0 &&
		state.Verified.VerifiedAtUnix <= state.DatabaseNowUnix &&
		state.DatabaseNowUnix-state.Verified.VerifiedAtUnix >= BackupRPOMaxVerifiedAgeSeconds
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
	if !ok || !validExactObjectVersion(objectVersion) {
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

const backupRPOAttemptColumns = `
	restore_epoch,attempt_sequence,phase,backup_id,captured_generation,
	object_key,object_sha256,object_version,object_size_bytes,
	manifest_version,adapter_contract_version,capability_generation,
	capability_evidence_sha256,capability_expires_at_unix,
	lease_holder_id,lease_token,lease_fence,failure_code,
	created_at_unix,updated_at_unix,unixepoch() AS database_now_unix`

func backupRPOAttemptIdentityWhere(alias string) string {
	return alias + `.restore_epoch=? AND ` + alias + `.attempt_sequence=?
		AND ` + alias + `.captured_generation=? AND ` + alias + `.backup_id=?
		AND ` + alias + `.object_key=? AND ` + alias + `.object_sha256=?
		AND ` + alias + `.object_size_bytes=? AND ` + alias + `.manifest_version=?
		AND ` + alias + `.adapter_contract_version=?
		AND ` + alias + `.capability_generation=?
		AND ` + alias + `.capability_evidence_sha256=?
		AND ` + alias + `.capability_expires_at_unix=?
		AND ` + alias + `.lease_holder_id=? AND ` + alias + `.lease_token=?
		AND ` + alias + `.lease_fence=?`
}

func backupRPOAttemptIdentityArgs(identity BackupRPOAttemptIdentity) []any {
	return []any{
		identity.RestoreEpoch, identity.AttemptSequence, identity.CapturedGeneration,
		identity.BackupID, identity.ObjectKey, identity.ObjectSHA256,
		identity.ObjectSizeBytes, identity.ManifestVersion, identity.AdapterContractVersion,
		identity.Capability.Generation, identity.Capability.EvidenceSHA256,
		identity.Capability.ExpiresAtUnix, identity.HolderID, identity.LeaseToken,
		identity.LeaseFence,
	}
}

func backupRPOAttemptLiveLeaseWhere(alias string) string {
	return `AND EXISTS (
		SELECT 1 FROM cluster_job_leases l
		JOIN cluster_restore_state cr
			ON cr.singleton_id=1 AND cr.activated=1 AND cr.restore_epoch=l.restore_epoch
		JOIN backup_rpo_state b
			ON b.singleton_id=1 AND b.restore_epoch=l.restore_epoch
		WHERE l.job_name='backup-rpo'
			AND l.holder_id=` + alias + `.lease_holder_id
			AND l.lease_token=` + alias + `.lease_token
			AND l.restore_epoch=` + alias + `.restore_epoch
			AND l.lease_fence=` + alias + `.lease_fence
			AND l.capability_generation=` + alias + `.capability_generation
			AND l.capability_evidence_sha256=` + alias + `.capability_evidence_sha256
			AND l.capability_expires_at_unix=` + alias + `.capability_expires_at_unix
			AND l.expires_at_unix>unixepoch()
			AND l.capability_expires_at_unix>unixepoch()
	)`
}

func validBackupRPOObjectKey(value string, capturedGeneration, attemptSequence int64, backupID string) bool {
	if len(value) == 0 || len(value) > 1024 || capturedGeneration <= 0 || attemptSequence <= 0 || !canonicalLowerHex(backupID, 32) {
		return false
	}
	tail := "g-" + strconv.FormatInt(capturedGeneration, 10) + "/a-" + strconv.FormatInt(attemptSequence, 10) + "-" + backupID + ".tar.gpg"
	if value == tail {
		return true
	}
	suffix := "/" + tail
	if !strings.HasSuffix(value, suffix) {
		return false
	}
	return validBackupRPOObjectPrefix(strings.TrimSuffix(value, suffix))
}

func validBackupRPOObjectPrefix(prefix string) bool {
	if prefix == "" || !backupRPOASCIIAlphaNumeric(prefix[0]) || prefix[len(prefix)-1] == '/' {
		return false
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	for _, character := range prefix {
		if character > 127 || (!backupRPOASCIIAlphaNumeric(byte(character)) && character != '.' && character != '_' && character != '-' && character != '/') {
			return false
		}
	}
	return true
}

func backupRPOASCIIAlphaNumeric(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')
}

func isValidBackupRPOAttemptIdentity(identity BackupRPOAttemptIdentity) bool {
	return identity.HolderID != "" && strings.TrimSpace(identity.HolderID) == identity.HolderID &&
		identity.LeaseToken != "" && strings.TrimSpace(identity.LeaseToken) == identity.LeaseToken &&
		identity.RestoreEpoch > 0 && identity.LeaseFence > 0 &&
		identity.Capability.Generation > 0 &&
		canonicalLowerHex(identity.Capability.EvidenceSHA256, 64) &&
		identity.Capability.ExpiresAtUnix > 0 && identity.CapturedGeneration > 0 &&
		identity.AttemptSequence > 0 && canonicalLowerHex(identity.BackupID, 32) &&
		validBackupRPOObjectKey(identity.ObjectKey, identity.CapturedGeneration, identity.AttemptSequence, identity.BackupID) &&
		canonicalLowerHex(identity.ObjectSHA256, 64) && identity.ObjectSizeBytes > 0 &&
		identity.ManifestVersion == 2 && identity.AdapterContractVersion == BackupRPOAdapterYandexS3V1
}

func validExactObjectVersion(value string) bool {
	if !validObjectVersion(value) || value[0] == '"' || value[len(value)-1] == '"' ||
		backupRPOLooksLikeMultipartETag(value) {
		return false
	}
	return !backupRPOFoldedHex(value, 32) || value == strings.ToLower(value)
}

func backupRPOLooksLikeMultipartETag(value string) bool {
	if len(value) <= 33 || value[32] != '-' || !backupRPOFoldedHex(value[:32], 32) {
		return false
	}
	for _, digit := range value[33:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func backupRPOFoldedHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func isValidBackupRPOVerification(proof BackupRPOVerification) bool {
	identity := proof.Identity
	return isValidBackupRPOAttemptIdentity(identity) && proof.VersionID.valid() &&
		proof.FullReadback && proof.ReadbackSHA256 == identity.ObjectSHA256 &&
		proof.ReadbackSizeBytes == identity.ObjectSizeBytes && proof.ManifestAuthenticated &&
		proof.ManifestVersion == 2 && proof.ManifestVersion == identity.ManifestVersion &&
		proof.ManifestBackupID == identity.BackupID &&
		proof.ManifestRestoreEpoch == identity.RestoreEpoch &&
		proof.ManifestCapturedGeneration == identity.CapturedGeneration &&
		proof.ManifestObjectKey == identity.ObjectKey &&
		proof.ManifestObjectSHA256 == identity.ObjectSHA256 &&
		proof.ManifestObjectSizeBytes == identity.ObjectSizeBytes
}

func parseBackupRPOAttempt(row map[string]any) (BackupRPOAttempt, bool) {
	restoreEpoch, ok := strictBackupRPOIntegerAt(row, "restore_epoch")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	attemptSequence, ok := strictBackupRPOIntegerAt(row, "attempt_sequence")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	capturedGeneration, ok := strictBackupRPOIntegerAt(row, "captured_generation")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	objectSize, ok := strictBackupRPOIntegerAt(row, "object_size_bytes")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	manifestVersion, ok := strictBackupRPOIntegerAt(row, "manifest_version")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	capabilityGeneration, ok := strictBackupRPOIntegerAt(row, "capability_generation")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	capabilityExpires, ok := strictBackupRPOIntegerAt(row, "capability_expires_at_unix")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	leaseFence, ok := strictBackupRPOIntegerAt(row, "lease_fence")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	createdAt, ok := strictBackupRPOIntegerAt(row, "created_at_unix")
	if !ok || createdAt <= 0 {
		return BackupRPOAttempt{}, false
	}
	updatedAt, ok := strictBackupRPOIntegerAt(row, "updated_at_unix")
	if !ok || updatedAt < createdAt {
		return BackupRPOAttempt{}, false
	}
	databaseNow, ok := strictBackupRPOIntegerAt(row, "database_now_unix")
	if !ok || databaseNow <= 0 {
		return BackupRPOAttempt{}, false
	}
	phase, ok := exactBackupRPOStringAt(row, "phase")
	if !ok || !validBackupRPOAttemptPhase(phase) {
		return BackupRPOAttempt{}, false
	}
	backupID, ok := exactBackupRPOStringAt(row, "backup_id")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	objectKey, ok := exactBackupRPOStringAt(row, "object_key")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	objectSHA, ok := exactBackupRPOStringAt(row, "object_sha256")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	adapter, ok := exactBackupRPOStringAt(row, "adapter_contract_version")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	capabilitySHA, ok := exactBackupRPOStringAt(row, "capability_evidence_sha256")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	holderID, ok := exactBackupRPOStringAt(row, "lease_holder_id")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	leaseToken, ok := exactBackupRPOStringAt(row, "lease_token")
	if !ok {
		return BackupRPOAttempt{}, false
	}
	objectVersion, ok := optionalBackupRPOStringAt(row, "object_version")
	if !ok || (objectVersion != "" && !validExactObjectVersion(objectVersion)) {
		return BackupRPOAttempt{}, false
	}
	failureCode, ok := optionalBackupRPOStringAt(row, "failure_code")
	if !ok || !validBackupRPOFailureCode(failureCode) {
		return BackupRPOAttempt{}, false
	}
	identity := BackupRPOAttemptIdentity{
		HolderID: holderID, LeaseToken: leaseToken, RestoreEpoch: restoreEpoch,
		LeaseFence: leaseFence, Capability: BackupRPOCapability{
			Generation: capabilityGeneration, EvidenceSHA256: capabilitySHA,
			ExpiresAtUnix: capabilityExpires,
		},
		CapturedGeneration: capturedGeneration, AttemptSequence: attemptSequence,
		BackupID: backupID, ObjectKey: objectKey, ObjectSHA256: objectSHA,
		ObjectSizeBytes: objectSize, ManifestVersion: manifestVersion,
		AdapterContractVersion: adapter,
	}
	if !isValidBackupRPOAttemptIdentity(identity) || !validBackupRPOAttemptLifecycle(phase, objectVersion) {
		return BackupRPOAttempt{}, false
	}
	return BackupRPOAttempt{
		Identity: identity, Phase: phase, ObjectVersion: objectVersion,
		FailureCode: failureCode, CreatedAtUnix: createdAt, UpdatedAtUnix: updatedAt,
		DatabaseNowUnix: databaseNow,
	}, true
}

func optionalBackupRPOStringAt(row map[string]any, key string) (string, bool) {
	value, exists := row[key]
	if !exists {
		return "", false
	}
	if value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func validBackupRPOAttemptPhase(phase string) bool {
	switch phase {
	case BackupRPOAttemptPending, BackupRPOAttemptApplying, BackupRPOAttemptApplied,
		BackupRPOAttemptUnknown, BackupRPOAttemptVerified, BackupRPOAttemptSuperseded,
		BackupRPOAttemptFailed:
		return true
	default:
		return false
	}
}

func validBackupRPOAttemptLifecycle(phase, objectVersion string) bool {
	switch phase {
	case BackupRPOAttemptPending, BackupRPOAttemptApplying, BackupRPOAttemptUnknown:
		return objectVersion == ""
	case BackupRPOAttemptApplied, BackupRPOAttemptVerified:
		return objectVersion != ""
	case BackupRPOAttemptSuperseded, BackupRPOAttemptFailed:
		return true
	default:
		return false
	}
}

func validBackupRPOFailureCode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func attemptMatchesExpectation(
	attempt BackupRPOAttempt,
	identity BackupRPOAttemptIdentity,
	expected attemptExpectation,
) bool {
	if attempt.Identity != identity || attempt.Phase != expected.phase ||
		attempt.FailureCode != expected.failureCode {
		return false
	}
	switch expected.versionMode {
	case attemptVersionMustBeNull:
		return attempt.ObjectVersion == ""
	case attemptVersionMustEqual:
		return expected.objectVersion != "" && attempt.ObjectVersion == expected.objectVersion
	case attemptVersionPreserveAnyValid:
		return attempt.ObjectVersion == "" || validExactObjectVersion(attempt.ObjectVersion)
	default:
		return false
	}
}

func unknownBackupRPOOutcome(err error) bool {
	var transportErr *rqlite.TransportError
	return errors.As(err, &transportErr) && transportErr.UnknownOutcome
}
