//go:build rqlite_integration

package rqliteintegrationlock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const JobName = "maestrovpn-backup-rpo-integration-v1"

const databaseUnixSecondReserve = time.Second

var ErrLeaseLost = errors.New("rqlite integration lease ownership lost")

var errRenewOutcomeUnresolved = errors.New("rqlite integration lease renewal outcome unresolved")

const acquireSQL = `INSERT INTO cluster_job_leases(
job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix
) VALUES(?,?,?,unixepoch(),unixepoch()+?)
ON CONFLICT(job_name) DO UPDATE SET
holder_id=excluded.holder_id,
lease_token=excluded.lease_token,
acquired_at_unix=excluded.acquired_at_unix,
expires_at_unix=excluded.expires_at_unix
WHERE cluster_job_leases.expires_at_unix<=unixepoch()
RETURNING job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
unixepoch() AS database_now_unix`

const currentSQL = `SELECT job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
unixepoch() AS database_now_unix
FROM cluster_job_leases
WHERE job_name=? AND holder_id=? AND lease_token=? AND expires_at_unix>unixepoch()`

const leaseAtExpirySQL = `SELECT job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
unixepoch() AS database_now_unix
FROM cluster_job_leases
WHERE job_name=? AND holder_id=? AND lease_token=? AND expires_at_unix=?
AND expires_at_unix>unixepoch()`

const renewSQL = `UPDATE cluster_job_leases
SET expires_at_unix=expires_at_unix+?
WHERE job_name=? AND holder_id=? AND lease_token=? AND expires_at_unix=?
AND expires_at_unix>unixepoch() AND expires_at_unix-unixepoch()<=?
RETURNING job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
unixepoch() AS database_now_unix`

const releaseSQL = `DELETE FROM cluster_job_leases
WHERE job_name=? AND holder_id=? AND lease_token=?
RETURNING job_name,holder_id,lease_token`

type Identity struct {
	JobName    string
	HolderID   string
	LeaseToken string
}

func (identity Identity) Valid() bool {
	return identity.JobName == JobName && strings.TrimSpace(identity.HolderID) == identity.HolderID &&
		identity.HolderID != "" && strings.TrimSpace(identity.LeaseToken) == identity.LeaseToken &&
		identity.LeaseToken != ""
}

type Options struct {
	HolderID     string
	LeaseToken   string
	TTL          time.Duration
	PollInterval time.Duration
}

type leaseState struct {
	acquiredAt  int64
	expiresAt   int64
	databaseNow int64
	observedAt  time.Time
	safeUntil   time.Time
}

type Lease struct {
	db                 rqlite.RQLite
	identity           Identity
	ttlSeconds         int64
	renewWindowSeconds int64
	mu                 sync.RWMutex
	state              leaseState
}

func Acquire(ctx context.Context, db rqlite.RQLite, options Options) (*Lease, error) {
	if db == nil || strings.TrimSpace(options.HolderID) != options.HolderID || options.HolderID == "" {
		return nil, errors.New("invalid rqlite integration lease options")
	}
	ttlSeconds := int64(math.Ceil(options.TTL.Seconds()))
	if ttlSeconds < 2 || options.PollInterval <= 0 {
		return nil, errors.New("invalid rqlite integration lease timing")
	}
	token := options.LeaseToken
	if token == "" {
		var err error
		token, err = newLeaseToken()
		if err != nil {
			return nil, err
		}
	}
	identity := Identity{JobName: JobName, HolderID: options.HolderID, LeaseToken: token}
	if !identity.Valid() {
		return nil, errors.New("invalid rqlite integration lease identity")
	}
	lease := &Lease{
		db: db, identity: identity, ttlSeconds: ttlSeconds,
		renewWindowSeconds: (ttlSeconds*2 + 2) / 3,
	}
	var lastResolveErr error
	for {
		startedAt := time.Now()
		results, err := db.Request(ctx, rqlite.Linearizable, true, acquireStatement(identity, ttlSeconds))
		if err == nil {
			state, owned, parseErr := returnedLease(results, identity)
			if parseErr != nil {
				return nil, parseErr
			}
			if owned {
				lease.recordState(state, startedAt)
				return lease, nil
			}
		} else {
			var transportErr *rqlite.TransportError
			if !errors.As(err, &transportErr) || !transportErr.UnknownOutcome {
				return nil, fmt.Errorf("acquire rqlite integration lease: %w", err)
			}
			state, owned, resolveErr := lease.current(ctx)
			if resolveErr == nil && owned {
				lease.recordState(state, state.observedAt)
				return lease, nil
			}
			lastResolveErr = resolveErr
		}
		timer := time.NewTimer(options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastResolveErr != nil {
				return nil, fmt.Errorf("wait for rqlite integration lease after resolution failure: %v: %w", lastResolveErr, ctx.Err())
			}
			return nil, fmt.Errorf("wait for rqlite integration lease: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (lease *Lease) Identity() Identity { return lease.identity }

func (lease *Lease) Renew(ctx context.Context) error {
	before, ok := lease.snapshot()
	if !ok {
		return ErrLeaseLost
	}
	startedAt := time.Now()
	results, err := lease.db.Request(
		ctx, rqlite.Linearizable, true,
		renewStatement(lease.identity, lease.ttlSeconds, before.expiresAt, lease.renewWindowSeconds),
	)
	if err == nil {
		state, owned, parseErr := returnedLease(results, lease.identity)
		if parseErr != nil {
			return parseErr
		}
		if owned {
			if err := lease.validateRenewedState(before, state); err != nil {
				return err
			}
			lease.recordState(state, startedAt)
			return nil
		}
		return lease.resolveKnownNoChange(ctx, before)
	}
	var transportErr *rqlite.TransportError
	if !errors.As(err, &transportErr) || !transportErr.UnknownOutcome {
		return fmt.Errorf("renew rqlite integration lease: %w", err)
	}
	return lease.resolveUnknownRenewal(ctx, before)
}

func (lease *Lease) Release(ctx context.Context) error {
	results, err := lease.db.Request(ctx, rqlite.Linearizable, true, releaseStatement(lease.identity))
	if err != nil {
		var transportErr *rqlite.TransportError
		if !errors.As(err, &transportErr) || !transportErr.UnknownOutcome {
			return fmt.Errorf("release rqlite integration lease: %w", err)
		}
		_, owned, resolveErr := lease.current(ctx)
		if resolveErr != nil {
			return fmt.Errorf("resolve rqlite integration lease release: %w", resolveErr)
		}
		if !owned {
			return nil
		}
		return errors.New("release rqlite integration lease: outcome remains unknown")
	}
	if len(results) != 1 || len(results[0].Rows) != 1 || !rowHasIdentity(results[0].Rows[0], lease.identity) {
		return ErrLeaseLost
	}
	return nil
}

func (lease *Lease) resolveKnownNoChange(ctx context.Context, before leaseState) error {
	state, owned, err := lease.atExpiry(ctx, before.expiresAt)
	if err != nil {
		return fmt.Errorf("prove rqlite integration lease after no-op renewal: %w", err)
	}
	if owned {
		lease.recordState(state, state.observedAt)
		return nil
	}
	expectedExpiry := before.expiresAt + lease.ttlSeconds
	state, owned, err = lease.atExpiry(ctx, expectedExpiry)
	if err != nil {
		return fmt.Errorf("prove rqlite integration lease after renewal: %w", err)
	}
	if !owned {
		return ErrLeaseLost
	}
	if err := lease.validateRenewedState(before, state); err != nil {
		return err
	}
	lease.recordState(state, state.observedAt)
	return nil
}

func (lease *Lease) resolveUnknownRenewal(ctx context.Context, before leaseState) error {
	expectedExpiry := before.expiresAt + lease.ttlSeconds
	state, owned, err := lease.atExpiry(ctx, expectedExpiry)
	if err != nil {
		return fmt.Errorf("resolve rqlite integration lease renewal at expected expiry: %w", err)
	}
	if owned {
		if err := lease.validateRenewedState(before, state); err != nil {
			return err
		}
		lease.recordState(state, state.observedAt)
		return nil
	}
	state, owned, err = lease.atExpiry(ctx, before.expiresAt)
	if err != nil {
		return fmt.Errorf("resolve rqlite integration lease renewal at previous expiry: %w", err)
	}
	if !owned {
		return ErrLeaseLost
	}
	lease.recordState(state, state.observedAt)
	return errRenewOutcomeUnresolved
}

func (lease *Lease) validateRenewedState(before, after leaseState) error {
	if after.acquiredAt != before.acquiredAt || after.expiresAt != before.expiresAt+lease.ttlSeconds {
		return errors.New("rqlite integration lease returned malformed renewal transition")
	}
	return nil
}

func (lease *Lease) current(ctx context.Context) (leaseState, bool, error) {
	startedAt := time.Now()
	results, err := lease.db.QueryLinearizable(ctx, currentStatement(lease.identity))
	if err != nil {
		return leaseState{}, false, err
	}
	state, owned, err := returnedLease(results, lease.identity)
	if owned {
		state.observedAt = startedAt
	}
	return state, owned, err
}

func (lease *Lease) atExpiry(ctx context.Context, expiresAt int64) (leaseState, bool, error) {
	startedAt := time.Now()
	results, err := lease.db.QueryLinearizable(ctx, leaseAtExpiryStatement(lease.identity, expiresAt))
	if err != nil {
		return leaseState{}, false, err
	}
	state, owned, err := returnedLease(results, lease.identity)
	if owned {
		state.observedAt = startedAt
	}
	return state, owned, err
}

func (lease *Lease) recordState(state leaseState, observedAt time.Time) {
	state.observedAt = observedAt
	remaining := time.Duration(state.expiresAt-state.databaseNow)*time.Second - databaseUnixSecondReserve
	if remaining < 0 {
		remaining = 0
	}
	state.safeUntil = observedAt.Add(remaining)

	lease.mu.Lock()
	if !lease.state.safeUntil.IsZero() {
		maxFromPrevious := lease.state.safeUntil.Add(
			time.Duration(state.expiresAt-lease.state.expiresAt) * time.Second,
		)
		if state.safeUntil.After(maxFromPrevious) {
			state.safeUntil = maxFromPrevious
		}
	}
	lease.state = state
	lease.mu.Unlock()
}

func (lease *Lease) snapshot() (leaseState, bool) {
	lease.mu.RLock()
	defer lease.mu.RUnlock()
	return lease.state, !lease.state.safeUntil.IsZero()
}

func (lease *Lease) safetyDeadline(margin time.Duration) (time.Time, bool) {
	state, ok := lease.snapshot()
	if !ok || state.expiresAt <= state.databaseNow {
		return time.Time{}, false
	}
	return state.safeUntil.Add(-margin), true
}

func acquireStatement(identity Identity, ttlSeconds int64) rqlite.Statement {
	return rqlite.Statement{SQL: acquireSQL, Args: []any{
		identity.JobName, identity.HolderID, identity.LeaseToken, ttlSeconds,
	}}
}

func currentStatement(identity Identity) rqlite.Statement {
	return rqlite.Statement{SQL: currentSQL, Args: []any{
		identity.JobName, identity.HolderID, identity.LeaseToken,
	}}
}

func leaseAtExpiryStatement(identity Identity, expiresAt int64) rqlite.Statement {
	return rqlite.Statement{SQL: leaseAtExpirySQL, Args: []any{
		identity.JobName, identity.HolderID, identity.LeaseToken, expiresAt,
	}}
}

func renewStatement(identity Identity, ttlSeconds, expectedExpiry, renewWindowSeconds int64) rqlite.Statement {
	return rqlite.Statement{SQL: renewSQL, Args: []any{
		ttlSeconds, identity.JobName, identity.HolderID, identity.LeaseToken,
		expectedExpiry, renewWindowSeconds,
	}}
}

func releaseStatement(identity Identity) rqlite.Statement {
	return rqlite.Statement{SQL: releaseSQL, Args: []any{
		identity.JobName, identity.HolderID, identity.LeaseToken,
	}}
}

func returnedLease(results []rqlite.Result, identity Identity) (leaseState, bool, error) {
	if len(results) != 1 {
		return leaseState{}, false, errors.New("rqlite integration lease returned malformed result count")
	}
	if len(results[0].Rows) == 0 {
		return leaseState{}, false, nil
	}
	if len(results[0].Rows) != 1 {
		return leaseState{}, false, errors.New("rqlite integration lease returned malformed row count")
	}
	row := results[0].Rows[0]
	acquiredAt, acquiredOK := integer(row["acquired_at_unix"])
	expiresAt, expiresOK := integer(row["expires_at_unix"])
	databaseNow, nowOK := integer(row["database_now_unix"])
	if !rowHasIdentity(row, identity) || !acquiredOK || !expiresOK || !nowOK ||
		acquiredAt < 0 || expiresAt <= acquiredAt || expiresAt <= databaseNow {
		return leaseState{}, false, errors.New("rqlite integration lease returned malformed ownership row")
	}
	return leaseState{acquiredAt: acquiredAt, expiresAt: expiresAt, databaseNow: databaseNow}, true, nil
}

func rowHasIdentity(row map[string]any, identity Identity) bool {
	jobName, jobOK := row["job_name"].(string)
	holderID, holderOK := row["holder_id"].(string)
	leaseToken, tokenOK := row["lease_token"].(string)
	return jobOK && holderOK && tokenOK && jobName == identity.JobName &&
		holderID == identity.HolderID && leaseToken == identity.LeaseToken
}

func integer(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if math.Trunc(typed) == typed && typed >= math.MinInt64 && typed <= math.MaxInt64 {
			return int64(typed), true
		}
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	}
	return 0, false
}

func newLeaseToken() (string, error) {
	random := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		return "", fmt.Errorf("generate rqlite integration lease token: %w", err)
	}
	return hex.EncodeToString(random), nil
}

type Selection struct {
	Package       string
	RunPattern    string
	DRProofPhase  string
	DRFencePhase  string
	DRQuorumPhase string
}

func IsExactSequentialDRSelection(selection Selection) bool {
	if selection.DRProofPhase != "restored" {
		return false
	}
	switch {
	case selection.Package == "importer" &&
		selection.RunPattern == "^TestVerifyRestoredBusinessParity$":
		return selection.DRFencePhase == "" && selection.DRQuorumPhase == ""
	case selection.Package == "controlplane" &&
		selection.RunPattern == "^TestAdvanceRestoredEpochAndFence$":
		return (selection.DRFencePhase == "advance" || selection.DRFencePhase == "activate") &&
			selection.DRQuorumPhase == ""
	case selection.Package == "controlplane" &&
		selection.RunPattern == "^TestRestoredQuorumBoundaries$":
		return selection.DRFencePhase == "" &&
			(selection.DRQuorumPhase == "one-loss" || selection.DRQuorumPhase == "two-loss")
	default:
		return false
	}
}

func IsExactNonLiveSelection(packageName, runPattern string) bool {
	if packageName != "controlplane" && packageName != "importer" {
		return false
	}
	if runPattern == "^$" {
		return true
	}
	return packageName == "importer" &&
		runPattern == "^TestTask6IntegrationCleanupRestoresOnlyExactOwnedPostconditionSQLite$"
}

func DefaultEndpoints() []string {
	return []string{"http://127.0.0.1:4401", "http://127.0.0.1:4403", "http://127.0.0.1:4405"}
}

type runTiming struct {
	TTL                time.Duration
	PollInterval       time.Duration
	ProbeTimeout       time.Duration
	MigrateTimeout     time.Duration
	AcquireTimeout     time.Duration
	RenewInterval      time.Duration
	RenewRetryInterval time.Duration
	RenewTimeout       time.Duration
	SafetyMargin       time.Duration
	ReleaseTimeout     time.Duration
}

func defaultRunTiming() runTiming {
	return runTiming{
		TTL: 30 * time.Second, PollInterval: 100 * time.Millisecond,
		ProbeTimeout: 2 * time.Second, MigrateTimeout: 45 * time.Second,
		AcquireTimeout: 5 * time.Minute, RenewInterval: 10 * time.Second,
		RenewRetryInterval: 250 * time.Millisecond, RenewTimeout: 5 * time.Second,
		SafetyMargin: 2 * time.Second, ReleaseTimeout: 10 * time.Second,
	}
}

func (timing runTiming) valid() bool {
	return timing.TTL >= 2*time.Second && timing.PollInterval > 0 && timing.ProbeTimeout > 0 &&
		timing.MigrateTimeout > 0 && timing.AcquireTimeout > 0 && timing.RenewInterval > 0 &&
		timing.RenewRetryInterval > 0 && timing.RenewTimeout > 0 && timing.SafetyMargin >= 0 &&
		timing.ReleaseTimeout > 0 && timing.SafetyMargin+databaseUnixSecondReserve < timing.TTL
}

type RunOptions struct {
	HolderID  string
	Migrate   func(context.Context, rqlite.RQLite) error
	Run       func(Identity) int
	Endpoints []string

	timing   runTiming
	failStop func(error)
}

func Run(options RunOptions) int {
	if options.Run == nil || options.Migrate == nil ||
		strings.TrimSpace(options.HolderID) != options.HolderID || options.HolderID == "" {
		fmt.Fprintln(os.Stderr, "invalid rqlite integration TestMain options")
		return 2
	}
	if len(options.Endpoints) == 0 {
		return options.Run(Identity{})
	}
	timing := options.timing
	if timing == (runTiming{}) {
		timing = defaultRunTiming()
	}
	if !timing.valid() {
		fmt.Fprintln(os.Stderr, "invalid rqlite integration TestMain timing")
		return 2
	}
	db, err := rqlite.New(rqlite.Config{Endpoints: options.Endpoints, Timeout: 10 * time.Second})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create rqlite integration coordinator: %v\n", err)
		return 2
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), timing.ProbeTimeout)
	probe, probeErr := db.QueryLinearizable(probeCtx, rqlite.Statement{SQL: "SELECT 1 AS ready"})
	probeCancel()
	if probeErr != nil {
		fmt.Fprintf(os.Stderr, "probe rqlite integration coordinator: %v\n", probeErr)
		return 2
	}
	if len(probe) != 1 || len(probe[0].Rows) != 1 {
		fmt.Fprintln(os.Stderr, "probe rqlite integration coordinator returned malformed result")
		return 2
	}
	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), timing.MigrateTimeout)
	err = options.Migrate(migrateCtx, db)
	migrateCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate before rqlite integration coordination: %v\n", err)
		return 2
	}
	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), timing.AcquireTimeout)
	lease, err := Acquire(acquireCtx, db, Options{
		HolderID: options.HolderID, TTL: timing.TTL, PollInterval: timing.PollInterval,
	})
	acquireCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "acquire rqlite integration coordinator: %v\n", err)
		return 2
	}
	failStop := options.failStop
	if failStop == nil {
		failStop = failStopProcess
	}
	renewCtx, stopRenew := context.WithCancel(context.Background())
	renewDone := make(chan error, 1)
	go renewUntilCanceled(renewCtx, lease, timing, failStop, renewDone)
	exitCode := options.Run(lease.Identity())
	stopRenew()
	renewErr := <-renewDone
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), timing.ReleaseTimeout)
	releaseErr := lease.Release(releaseCtx)
	releaseCancel()
	if renewErr != nil {
		fmt.Fprintf(os.Stderr, "renew rqlite integration coordinator: %v\n", renewErr)
	}
	if releaseErr != nil {
		fmt.Fprintf(os.Stderr, "release rqlite integration coordinator: %v\n", releaseErr)
	}
	if exitCode == 0 && (renewErr != nil || releaseErr != nil) {
		return 2
	}
	return exitCode
}

func renewUntilCanceled(
	ctx context.Context,
	lease *Lease,
	timing runTiming,
	failStop func(error),
	done chan<- error,
) {
	delay := timing.RenewInterval
	var lastRenewErr error
	for {
		deadline, ok := lease.safetyDeadline(timing.SafetyMargin)
		if !ok {
			failRenewal(renewalFailure(ErrLeaseLost, lastRenewErr), failStop, done)
			return
		}
		untilDeadline := time.Until(deadline)
		if untilDeadline <= 0 {
			failRenewal(renewalFailure(errors.New("rqlite integration lease renewal safety deadline reached"), lastRenewErr), failStop, done)
			return
		}
		wait := delay
		if wait > untilDeadline {
			wait = untilDeadline
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			done <- nil
			return
		case <-timer.C:
		}
		now := time.Now()
		if !deadline.After(now) {
			failRenewal(renewalFailure(errors.New("rqlite integration lease renewal safety deadline reached"), lastRenewErr), failStop, done)
			return
		}
		renewDeadline := deadline
		if timeoutDeadline := now.Add(timing.RenewTimeout); timeoutDeadline.Before(renewDeadline) {
			renewDeadline = timeoutDeadline
		}
		if !renewDeadline.After(time.Now()) {
			failRenewal(renewalFailure(errors.New("rqlite integration lease renewal safety deadline reached"), lastRenewErr), failStop, done)
			return
		}
		previousRenewErr := lastRenewErr
		renewCtx, cancel := context.WithDeadline(ctx, renewDeadline)
		err := lease.Renew(renewCtx)
		cancel()
		if ctx.Err() != nil {
			done <- nil
			return
		}
		if err != nil {
			lastRenewErr = err
		}
		postDeadline, safe := lease.safetyDeadline(timing.SafetyMargin)
		if !safe || !time.Now().Before(postDeadline) {
			diagnosticRenewErr := lastRenewErr
			if err != nil && previousRenewErr != nil && err.Error() != previousRenewErr.Error() {
				diagnosticRenewErr = fmt.Errorf("%v; previous renewal error: %w", err, previousRenewErr)
			}
			failRenewal(renewalFailure(errors.New("rqlite integration lease renewal safety deadline reached after renewal attempt"), diagnosticRenewErr), failStop, done)
			return
		}
		if err == nil {
			lastRenewErr = nil
			delay = timing.RenewInterval
			continue
		}
		if errors.Is(err, ErrLeaseLost) {
			failRenewal(err, failStop, done)
			return
		}
		delay = timing.RenewRetryInterval
	}
}

func renewalFailure(err, lastRenewErr error) error {
	if lastRenewErr == nil {
		return err
	}
	return fmt.Errorf("%v; last renewal error: %w", err, lastRenewErr)
}

func failRenewal(err error, failStop func(error), done chan<- error) {
	wrapped := fmt.Errorf("rqlite integration coordination cannot safely retain ownership: %w", err)
	failStop(wrapped)
	done <- wrapped
}

func failStopProcess(err error) {
	fmt.Fprintf(os.Stderr, "rqlite integration coordination fail-stop: %v\n", err)
	os.Exit(2)
}
