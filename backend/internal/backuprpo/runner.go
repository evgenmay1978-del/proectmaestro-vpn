package backuprpo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const (
	FailureInvalidState         = "backup-worker:invalid-state"
	FailureInvalidTransition    = "backup-worker:invalid-transition"
	FailureStaleLease           = "backup-worker:stale-lease"
	FailureVerificationMismatch = "backup-worker:verification-mismatch"
	FailureUnsafeRuntime        = "backup-worker:unsafe-runtime"
	FailureUnsafeCutover        = "backup-worker:unsafe-cutover"
)

var (
	ErrInvalidRunner = errors.New(FailureInvalidState)
	ErrUnsafeRuntime = errors.New(FailureUnsafeRuntime)
	ErrBackupSource  = errors.New(FailureInvalidTransition)
	ErrCommandFailed = errors.New(FailureUnsafeRuntime)
)

type ResultCode string

const (
	ResultNoop                  ResultCode = "ok-noop"
	ResultVerified              ResultCode = "ok-verified"
	ResultCapabilityUnavailable ResultCode = FailureInvalidState
	ResultInvalidTransition     ResultCode = FailureInvalidTransition
	ResultTransitionLimit       ResultCode = FailureInvalidTransition
	ResultDeadline              ResultCode = FailureInvalidTransition
	ResultStaleLease            ResultCode = FailureStaleLease
	ResultVerificationMismatch  ResultCode = FailureVerificationMismatch
	ResultUnsafeRuntime         ResultCode = FailureUnsafeRuntime
)

type Result struct {
	Code        ResultCode
	Transitions int
}

type ContractMatrix struct {
	Phases       []string          `json:"phases"`
	FailureCodes []string          `json:"failure_codes"`
	Transitions  map[string]string `json:"transitions"`
}

func PublicContract() ContractMatrix {
	return ContractMatrix{
		Phases: []string{
			controlplane.BackupRPOAttemptPending,
			controlplane.BackupRPOAttemptApplying,
			controlplane.BackupRPOAttemptApplied,
			controlplane.BackupRPOAttemptUnknown,
		},
		FailureCodes: []string{
			FailureInvalidState,
			FailureInvalidTransition,
			FailureStaleLease,
			FailureVerificationMismatch,
			FailureUnsafeRuntime,
			FailureUnsafeCutover,
		},
		Transitions: map[string]string{
			"no-attempt-clean":       "wait",
			"no-attempt-dirty":       "create",
			"pending":                "upload",
			"applying":               "verify",
			"applied":                "verify",
			"unknown":                "verify",
			"newer-fence":            "supersede",
			"capability-unavailable": "blocked",
			"lease-unavailable":      "blocked",
		},
	}
}

type CycleStore interface {
	Current(context.Context) (controlplane.BackupRPOState, error)
	CurrentCycle(context.Context) (controlplane.BackupRPOCycle, error)
	AcquireLease(context.Context, controlplane.BackupRPOLeaseRequest) (controlplane.BackupRPOLease, error)
	RenewLease(context.Context, controlplane.BackupRPOLeaseRequest) (controlplane.BackupRPOLease, error)
	RegisterAttempt(context.Context, controlplane.BackupRPOAttemptIdentity) (controlplane.BackupRPOAttempt, error)
	MarkUploadStarted(context.Context, controlplane.BackupRPOAttemptIdentity) (controlplane.BackupRPOAttempt, error)
	RecordUploadOutcome(context.Context, controlplane.BackupRPOUploadOutcome) (controlplane.BackupRPOAttempt, error)
	AcknowledgeVerified(context.Context, controlplane.BackupRPOVerification) (controlplane.BackupRPOAttempt, error)
	SupersedeStaleAttempt(context.Context, controlplane.BackupRPOSupersedeRequest) (controlplane.BackupRPOAttempt, error)
}

type Bundle interface {
	io.ReadSeeker
	io.Closer
}

type BundleRequest struct {
	RestoreEpoch       int64
	CapturedGeneration int64
	AttemptSequence    int64
	BackupID           string
	ObjectKey          string
	LeaseFence         int64
}

type BundleFactory interface {
	Create(context.Context, BundleRequest) (Bundle, error)
	OpenExisting(context.Context, BundleRequest) (Bundle, error)
}

type IdentitySource interface {
	NewID() (string, error)
}

type CapabilityGate interface {
	Check(context.Context) error
}

type RunnerConfig struct {
	HolderID                 string
	Prefix                   string
	LeaseTTL                 time.Duration
	CapabilityTTL            time.Duration
	Deadline                 time.Duration
	MaxTransitions           int
	CapabilityGeneration     int64
	CapabilityEvidenceSHA256 string
	CapabilityIssuedAtUnix   int64
	CapabilityExpiresAtUnix  int64
	MaxBundleBytes           int64
}

type Runner struct {
	Store        CycleStore
	Objects      ObjectStore
	Bundles      BundleFactory
	Capabilities CapabilityGate
	IDs          IdentitySource
	Now          func() time.Time
	Config       RunnerConfig
}

func (runner *Runner) Run(parent context.Context) Result {
	if !validRunner(runner) {
		return Result{Code: ResultCapabilityUnavailable}
	}
	ctx, cancel := context.WithTimeout(parent, runner.Config.Deadline)
	defer cancel()

	now := runner.Now().Unix()
	capability, ok := runner.capabilityAt(now)
	if !ok {
		return Result{Code: ResultCapabilityUnavailable}
	}
	if err := runner.Capabilities.Check(ctx); err != nil {
		return Result{Code: resultForContext(ctx, ResultCapabilityUnavailable)}
	}
	state, err := runner.Store.Current(ctx)
	if err != nil {
		return Result{Code: resultForContext(ctx, ResultCapabilityUnavailable)}
	}
	if _, ok := runner.capabilityAt(state.DatabaseNowUnix); !ok {
		return Result{Code: ResultCapabilityUnavailable}
	}
	leaseToken := ""
	if state.Lease != nil && state.Lease.Live {
		leaseToken = state.Lease.LeaseToken
	} else {
		leaseToken, err = runner.IDs.NewID()
	}
	if err != nil || !validRuntimeIdentity(leaseToken) {
		return Result{Code: ResultCapabilityUnavailable}
	}
	lease, leaseRequest, code := runner.obtainLease(ctx, state, leaseToken, capability)
	if code != "" {
		return Result{Code: code}
	}

	var pinned Bundle
	var pinnedBackupID string
	closePinned := func() {
		if pinned != nil {
			_ = pinned.Close()
			pinned = nil
			pinnedBackupID = ""
		}
	}
	defer closePinned()

	for transition := 1; transition <= runner.Config.MaxTransitions; transition++ {
		if ctx.Err() != nil {
			return Result{Code: ResultDeadline, Transitions: transition - 1}
		}
		cycle, cycleErr := runner.Store.CurrentCycle(ctx)
		if cycleErr != nil {
			return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
		}
		if cycle.State.RestoreEpoch != lease.RestoreEpoch {
			return Result{Code: ResultStaleLease, Transitions: transition}
		}
		if lease.ExpiresAtUnix <= cycle.State.DatabaseNowUnix ||
			lease.Capability.ExpiresAtUnix <= cycle.State.DatabaseNowUnix {
			return Result{Code: ResultStaleLease, Transitions: transition}
		}
		if cycle.State.DatabaseNowUnix+5 >= lease.ExpiresAtUnix {
			leaseRequest.ExpectedFence = lease.LeaseFence
			leaseRequest.Capability = lease.Capability
			renewed, renewErr := runner.Store.RenewLease(ctx, leaseRequest)
			if renewErr != nil {
				return Result{Code: resultForContext(ctx, ResultStaleLease), Transitions: transition}
			}
			lease = renewed
		}

		attempt := cycle.ActiveAttempt
		if attempt == nil {
			if cycle.State.DirtyGeneration <= cycle.State.VerifiedGeneration {
				return Result{Code: ResultNoop, Transitions: transition}
			}
			if cycle.State.Phase != controlplane.BackupRPOPhaseDirty ||
				cycle.State.DirtyGeneration <= 0 ||
				cycle.State.LastAttemptSequence == math.MaxInt64 {
				return Result{Code: ResultInvalidTransition, Transitions: transition}
			}
			backupID, idErr := runner.IDs.NewID()
			if idErr != nil || !canonicalLowerHex(backupID, 32) {
				return Result{Code: ResultInvalidTransition, Transitions: transition}
			}
			sequence := cycle.State.LastAttemptSequence + 1
			key, keyErr := BuildObjectKeyWithPrefix(
				runner.Config.Prefix,
				cycle.State.DirtyGeneration,
				sequence,
				backupID,
			)
			if keyErr != nil {
				return Result{Code: ResultInvalidTransition, Transitions: transition}
			}
			request := BundleRequest{
				RestoreEpoch: cycle.State.RestoreEpoch, CapturedGeneration: cycle.State.DirtyGeneration,
				AttemptSequence: sequence, BackupID: backupID, ObjectKey: key,
				LeaseFence: lease.LeaseFence,
			}
			closePinned()
			pinned, err = runner.Bundles.Create(ctx, request)
			if err != nil {
				return Result{Code: resultForBundleError(ctx, err), Transitions: transition}
			}
			pinnedBackupID = backupID
			digest, size, inspectErr := inspectBundle(pinned, runner.Config.MaxBundleBytes)
			if inspectErr != nil {
				return Result{Code: resultForBundleError(ctx, inspectErr), Transitions: transition}
			}
			identity := controlplane.BackupRPOAttemptIdentity{
				HolderID: runner.Config.HolderID, LeaseToken: lease.LeaseToken,
				RestoreEpoch: cycle.State.RestoreEpoch, LeaseFence: lease.LeaseFence,
				Capability: lease.Capability, CapturedGeneration: cycle.State.DirtyGeneration,
				AttemptSequence: sequence, BackupID: backupID, ObjectKey: key,
				ObjectSHA256: digest, ObjectSizeBytes: size, ManifestVersion: 2,
				AdapterContractVersion: controlplane.BackupRPOAdapterYandexS3V1,
			}
			if _, registerErr := runner.Store.RegisterAttempt(ctx, identity); registerErr != nil {
				return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
			}
			continue
		}

		identity := attempt.Identity
		if identity.RestoreEpoch != lease.RestoreEpoch {
			return Result{Code: ResultStaleLease, Transitions: transition}
		}
		if identity.LeaseFence < lease.LeaseFence {
			closePinned()
			if _, supersedeErr := runner.Store.SupersedeStaleAttempt(ctx, controlplane.BackupRPOSupersedeRequest{
				Identity: identity, CurrentLease: leaseRequestFor(lease, runner.Config.LeaseTTL),
			}); supersedeErr != nil {
				return Result{Code: resultForContext(ctx, ResultStaleLease), Transitions: transition}
			}
			continue
		}
		if !attemptBoundToLease(*attempt, lease) {
			return Result{Code: ResultStaleLease, Transitions: transition}
		}

		switch attempt.Phase {
		case controlplane.BackupRPOAttemptPending:
			request := bundleRequestFromIdentity(identity)
			if pinned == nil || pinnedBackupID != identity.BackupID {
				closePinned()
				pinned, err = runner.Bundles.OpenExisting(ctx, request)
				if err != nil {
					return Result{Code: resultForBundleError(ctx, err), Transitions: transition}
				}
				pinnedBackupID = identity.BackupID
			}
			digest, size, inspectErr := inspectBundle(pinned, runner.Config.MaxBundleBytes)
			if inspectErr != nil || digest != identity.ObjectSHA256 || size != identity.ObjectSizeBytes {
				return Result{Code: ResultUnsafeRuntime, Transitions: transition}
			}
			if _, startErr := runner.Store.MarkUploadStarted(ctx, identity); startErr != nil {
				return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
			}
			if _, seekErr := pinned.Seek(0, io.SeekStart); seekErr != nil {
				return Result{Code: ResultUnsafeRuntime, Transitions: transition}
			}
			version, putErr := runner.Objects.PutImmutable(ctx, PutRequest{
				Key: identity.ObjectKey, Body: pinned, Metadata: metadataFromIdentity(identity),
			})
			if putErr != nil {
				_, outcomeErr := runner.Store.RecordUploadOutcome(ctx, controlplane.BackupRPOUploadOutcome{
					Identity: identity, Unknown: true,
				})
				closePinned()
				if outcomeErr != nil {
					return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
				}
				if errors.Is(putErr, ErrPutOutcomeUnknown) {
					continue
				}
				return Result{Code: classifyObjectFailure(putErr), Transitions: transition}
			}
			controlVersion, versionErr := controlplane.NewBackupRPOVersionID(version.String())
			if versionErr != nil {
				closePinned()
				return Result{Code: ResultVerificationMismatch, Transitions: transition}
			}
			if _, outcomeErr := runner.Store.RecordUploadOutcome(ctx, controlplane.BackupRPOUploadOutcome{
				Identity: identity, VersionID: controlVersion,
			}); outcomeErr != nil {
				closePinned()
				return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
			}
			closePinned()
			continue

		case controlplane.BackupRPOAttemptApplying, controlplane.BackupRPOAttemptUnknown:
			closePinned()
			version, reconcileErr := runner.Objects.ReconcileUnknownPut(ctx, ReconcileRequest{
				Key: identity.ObjectKey, Metadata: metadataFromIdentity(identity),
			})
			if reconcileErr != nil {
				return Result{Code: classifyObjectFailure(reconcileErr), Transitions: transition}
			}
			controlVersion, versionErr := controlplane.NewBackupRPOVersionID(version.String())
			if versionErr != nil {
				return Result{Code: ResultVerificationMismatch, Transitions: transition}
			}
			if _, outcomeErr := runner.Store.RecordUploadOutcome(ctx, controlplane.BackupRPOUploadOutcome{
				Identity: identity, VersionID: controlVersion,
			}); outcomeErr != nil {
				return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
			}
			continue

		case controlplane.BackupRPOAttemptApplied:
			closePinned()
			version, versionErr := NewVersionID(attempt.ObjectVersion)
			if versionErr != nil {
				return Result{Code: ResultVerificationMismatch, Transitions: transition}
			}
			readback, readErr := runner.Objects.GetExact(ctx, ExactObjectRequest{
				Key: identity.ObjectKey, VersionID: version, Metadata: metadataFromIdentity(identity),
			})
			if readErr != nil {
				return Result{Code: classifyObjectFailure(readErr), Transitions: transition}
			}
			if !validRunnerReadback(readback, version, identity) {
				return Result{Code: ResultVerificationMismatch, Transitions: transition}
			}
			controlVersion, controlVersionErr := controlplane.NewBackupRPOVersionID(version.String())
			if controlVersionErr != nil {
				return Result{Code: ResultVerificationMismatch, Transitions: transition}
			}
			proof := controlplane.BackupRPOVerification{
				Identity: identity, VersionID: controlVersion, FullReadback: true,
				ReadbackSHA256: readback.SHA256, ReadbackSizeBytes: readback.SizeBytes,
				ManifestAuthenticated: readback.ManifestAuthenticated, ManifestVersion: identity.ManifestVersion,
				ManifestBackupID: identity.BackupID, ManifestCapturedGeneration: identity.CapturedGeneration,
				ManifestRestoreEpoch: identity.RestoreEpoch, ManifestObjectKey: identity.ObjectKey,
				ManifestObjectSHA256: identity.ObjectSHA256, ManifestObjectSizeBytes: identity.ObjectSizeBytes,
			}
			if _, acknowledgeErr := runner.Store.AcknowledgeVerified(ctx, proof); acknowledgeErr != nil {
				return Result{Code: resultForContext(ctx, ResultInvalidTransition), Transitions: transition}
			}
			return Result{Code: ResultVerified, Transitions: transition}

		case controlplane.BackupRPOAttemptVerified,
			controlplane.BackupRPOAttemptSuperseded,
			controlplane.BackupRPOAttemptFailed:
			closePinned()
			return Result{Code: ResultNoop, Transitions: transition}

		default:
			return Result{Code: ResultInvalidTransition, Transitions: transition}
		}
	}
	return Result{Code: ResultTransitionLimit, Transitions: runner.Config.MaxTransitions}
}

func (runner *Runner) obtainLease(
	ctx context.Context,
	state controlplane.BackupRPOState,
	token string,
	capability controlplane.BackupRPOCapability,
) (controlplane.BackupRPOLease, controlplane.BackupRPOLeaseRequest, ResultCode) {
	if state.Lease != nil && state.Lease.Live {
		if state.Lease.HolderID != runner.Config.HolderID ||
			state.Lease.LeaseToken != token ||
			state.Lease.RestoreEpoch != state.RestoreEpoch ||
			state.Lease.Capability != capability {
			return controlplane.BackupRPOLease{}, controlplane.BackupRPOLeaseRequest{}, ResultStaleLease
		}
		request := leaseRequestFor(*state.Lease, runner.Config.LeaseTTL)
		lease, err := runner.Store.RenewLease(ctx, request)
		if err != nil {
			return controlplane.BackupRPOLease{}, controlplane.BackupRPOLeaseRequest{}, resultForContext(ctx, ResultStaleLease)
		}
		return lease, request, ""
	}
	expectedFence := int64(0)
	if state.Lease != nil {
		expectedFence = state.Lease.LeaseFence
	}
	request := controlplane.BackupRPOLeaseRequest{
		HolderID: runner.Config.HolderID, LeaseToken: token,
		RestoreEpoch: state.RestoreEpoch, ExpectedFence: expectedFence,
		TTLSeconds: int64(runner.Config.LeaseTTL / time.Second), Capability: capability,
	}
	lease, err := runner.Store.AcquireLease(ctx, request)
	if err != nil {
		return controlplane.BackupRPOLease{}, controlplane.BackupRPOLeaseRequest{}, resultForContext(ctx, ResultStaleLease)
	}
	request.ExpectedFence = lease.LeaseFence
	return lease, request, ""
}

func leaseRequestFor(lease controlplane.BackupRPOLease, ttl time.Duration) controlplane.BackupRPOLeaseRequest {
	return controlplane.BackupRPOLeaseRequest{
		HolderID: lease.HolderID, LeaseToken: lease.LeaseToken,
		RestoreEpoch: lease.RestoreEpoch, ExpectedFence: lease.LeaseFence,
		TTLSeconds: int64(ttl / time.Second), Capability: lease.Capability,
	}
}

func attemptBoundToLease(attempt controlplane.BackupRPOAttempt, lease controlplane.BackupRPOLease) bool {
	identity := attempt.Identity
	return identity.RestoreEpoch == lease.RestoreEpoch &&
		identity.LeaseFence == lease.LeaseFence &&
		identity.HolderID == lease.HolderID &&
		identity.LeaseToken == lease.LeaseToken &&
		identity.Capability == lease.Capability
}

func bundleRequestFromIdentity(identity controlplane.BackupRPOAttemptIdentity) BundleRequest {
	return BundleRequest{
		RestoreEpoch: identity.RestoreEpoch, CapturedGeneration: identity.CapturedGeneration,
		AttemptSequence: identity.AttemptSequence, BackupID: identity.BackupID,
		ObjectKey: identity.ObjectKey, LeaseFence: identity.LeaseFence,
	}
}

func metadataFromIdentity(identity controlplane.BackupRPOAttemptIdentity) ObjectMetadata {
	return ObjectMetadata{
		SHA256: identity.ObjectSHA256, SizeBytes: identity.ObjectSizeBytes,
		CapturedGeneration: identity.CapturedGeneration, RestoreEpoch: identity.RestoreEpoch,
		AttemptSequence: identity.AttemptSequence, BackupID: identity.BackupID,
		ManifestVersion: identity.ManifestVersion, LeaseFence: identity.LeaseFence,
	}
}

func inspectBundle(bundle Bundle, maximum int64) (string, int64, error) {
	if bundle == nil || maximum <= 0 || maximum > MaxObjectBytes {
		return "", 0, ErrUnsafeRuntime
	}
	if _, err := bundle.Seek(0, io.SeekStart); err != nil {
		return "", 0, ErrUnsafeRuntime
	}
	hasher := sha256.New()
	count, err := io.Copy(hasher, io.LimitReader(bundle, maximum+1))
	if err != nil || count <= 0 || count > maximum {
		return "", 0, ErrUnsafeRuntime
	}
	if _, err := bundle.Seek(0, io.SeekStart); err != nil {
		return "", 0, ErrUnsafeRuntime
	}
	return hex.EncodeToString(hasher.Sum(nil)), count, nil
}

func validRunnerReadback(readback Readback, version VersionID, identity controlplane.BackupRPOAttemptIdentity) bool {
	return readback.VersionID.String() == version.String() &&
		readback.SHA256 == identity.ObjectSHA256 &&
		readback.SizeBytes == identity.ObjectSizeBytes &&
		readback.RestoreEpoch == identity.RestoreEpoch &&
		readback.ManifestAuthenticated
}

func validRunner(runner *Runner) bool {
	if runner == nil || runner.Store == nil || runner.Objects == nil || runner.Capabilities == nil ||
		runner.Bundles == nil || runner.IDs == nil || runner.Now == nil {
		return false
	}
	config := runner.Config
	if !validRuntimeIdentity(config.HolderID) ||
		config.LeaseTTL < time.Second || config.LeaseTTL > time.Hour ||
		config.CapabilityTTL < config.LeaseTTL || config.CapabilityTTL > time.Hour ||
		config.Deadline < time.Second || config.Deadline > time.Hour ||
		config.MaxTransitions < 1 || config.MaxTransitions > 64 ||
		config.CapabilityGeneration <= 0 ||
		!canonicalLowerHex(config.CapabilityEvidenceSHA256, 64) ||
		config.CapabilityIssuedAtUnix <= 0 ||
		config.CapabilityExpiresAtUnix <= config.CapabilityIssuedAtUnix ||
		config.MaxBundleBytes <= 0 || config.MaxBundleBytes > MaxObjectBytes ||
		config.LeaseTTL%time.Second != 0 ||
		config.CapabilityTTL%time.Second != 0 {
		return false
	}
	_, err := BuildObjectKeyWithPrefix(config.Prefix, 1, 1, strings.Repeat("a", 32))
	return err == nil
}

func (runner *Runner) capabilityAt(now int64) (controlplane.BackupRPOCapability, bool) {
	config := runner.Config
	leaseSeconds := int64(config.LeaseTTL / time.Second)
	capabilitySeconds := int64(config.CapabilityTTL / time.Second)
	if now <= 0 || config.CapabilityIssuedAtUnix > now ||
		config.CapabilityExpiresAtUnix-config.CapabilityIssuedAtUnix > capabilitySeconds ||
		config.CapabilityExpiresAtUnix-now < leaseSeconds {
		return controlplane.BackupRPOCapability{}, false
	}
	return controlplane.BackupRPOCapability{
		Generation:     config.CapabilityGeneration,
		EvidenceSHA256: config.CapabilityEvidenceSHA256,
		ExpiresAtUnix:  config.CapabilityExpiresAtUnix,
	}, true
}

func validRuntimeIdentity(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func classifyObjectFailure(err error) ResultCode {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ResultDeadline
	case errors.Is(err, ErrVersioningRequired), errors.Is(err, ErrInvalidConfig):
		return ResultCapabilityUnavailable
	case errors.Is(err, ErrInvalidRequest):
		return ResultInvalidTransition
	case errors.Is(err, ErrUnsafeRuntime), errors.Is(err, ErrCommandFailed):
		return ResultUnsafeRuntime
	default:
		return ResultVerificationMismatch
	}
}

func resultForBundleError(ctx context.Context, err error) ResultCode {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ResultDeadline
	}
	return ResultUnsafeRuntime
}

func resultForContext(ctx context.Context, fallback ResultCode) ResultCode {
	if ctx.Err() != nil {
		return ResultDeadline
	}
	return fallback
}
