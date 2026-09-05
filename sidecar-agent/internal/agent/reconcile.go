package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
)

type Handler interface {
	ListUsers(context.Context, string) ([]string, error)
	ManagedUserAccountMatches(context.Context, string, string) (bool, error)
	AddUser(context.Context, string, string) error
	RemoveUser(context.Context, string, string) error
}

type ReadinessPreflight interface {
	Validate(context.Context, string, string, string, string) error
}

type ReconcilerConfig struct {
	Handler             Handler
	Store               *FileStore
	InboundTag          string
	ReleaseID           string
	ConfigDigest        string
	ProcessBootID       func() (string, error)
	Preflight           ReadinessPreflight
	Now                 func() time.Time
	ReceiptTTL          time.Duration
	ManagedLeaseEnabled bool
	LeaseClock          func() (string, int64, error)
}

type Reconciler struct {
	handler             Handler
	store               *FileStore
	inboundTag          string
	releaseID           string
	configDigest        string
	processBootID       func() (string, error)
	preflight           ReadinessPreflight
	now                 func() time.Time
	receiptTTL          time.Duration
	mutex               sync.Mutex
	managedLeaseEnabled bool
	leaseClock          func() (string, int64, error)
}

func NewReconciler(config ReconcilerConfig) (*Reconciler, error) {
	if config.Handler == nil || config.Store == nil || config.InboundTag != DefaultInboundTag ||
		!safeIdentifier(config.ReleaseID) || !validDigest(config.ConfigDigest) || config.ProcessBootID == nil || config.Preflight == nil {
		return nil, errors.New("sidecar agent: invalid reconciler configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReceiptTTL == 0 {
		config.ReceiptTTL = DefaultReceiptTTL
	}
	if config.ReceiptTTL != DefaultReceiptTTL {
		return nil, errors.New("sidecar agent: receipt TTL must be 30 seconds")
	}
	if config.ManagedLeaseEnabled {
		if _, ok := config.Handler.(managedRuntimeController); !ok {
			return nil, ErrLeaseUnavailable
		}
		if config.LeaseClock == nil {
			config.LeaseClock = runtimefence.ReadLeaseClock
		}
	}
	return &Reconciler{
		handler: config.Handler, store: config.Store, inboundTag: config.InboundTag,
		releaseID: config.ReleaseID, configDigest: config.ConfigDigest,
		processBootID: config.ProcessBootID, preflight: config.Preflight, now: config.Now, receiptTTL: config.ReceiptTTL,
		managedLeaseEnabled: config.ManagedLeaseEnabled, leaseClock: config.LeaseClock,
	}, nil
}

func (reconciler *Reconciler) Apply(ctx context.Context, desired Desired) (Receipt, error) {
	if reconciler == nil {
		return Receipt{}, errors.New("sidecar agent: reconciler unavailable")
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	return reconciler.applyLocked(ctx, desired)
}

func (reconciler *Reconciler) Refresh(ctx context.Context) (Receipt, error) {
	if reconciler == nil {
		return Receipt{}, errors.New("sidecar agent: reconciler unavailable")
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	desired, err := reconciler.store.LoadDesired()
	if err != nil {
		return Receipt{}, err
	}
	return reconciler.applyLocked(ctx, desired)
}

func (reconciler *Reconciler) Recover(ctx context.Context) (Receipt, error) {
	return reconciler.Refresh(ctx)
}

// LookupReceipt reads one durable receipt without reconciling or changing Xray.
func (reconciler *Reconciler) LookupReceipt(ctx context.Context, actionKey string) (Receipt, error) {
	if reconciler == nil || ctx == nil || actionKey == "" || strings.TrimSpace(actionKey) != actionKey ||
		strings.ContainsAny(actionKey, "\x00\r\n\t") {
		return Receipt{}, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	receipt, err := reconciler.store.LoadReceipt(actionKey)
	if err != nil {
		return Receipt{}, err
	}
	bootID, err := reconciler.processBootID()
	if err != nil || receipt.ActionKey != actionKey || receipt.XrayProcessBootID != bootID ||
		!receipt.ReadyAt(reconciler.now()) {
		return Receipt{}, ErrNotFound
	}
	return receipt, nil
}

func (reconciler *Reconciler) applyLocked(ctx context.Context, desired Desired) (Receipt, error) {
	if ctx == nil {
		return Receipt{}, ErrInvalidDesired
	}
	if err := desired.validate(); err != nil || desired.ActionKey() == "" {
		return Receipt{}, ErrInvalidDesired
	}
	if desired.ReleaseID != reconciler.releaseID {
		return Receipt{}, errors.New("sidecar agent: release mismatch")
	}
	if desired.ConfigDigest != reconciler.configDigest {
		return Receipt{}, errors.New("sidecar agent: config digest mismatch")
	}
	if reconciler.managedLeaseEnabled {
		if len(desired.ManagedUsers) > maxLeaseUsers {
			return Receipt{}, ErrLeaseCapacity
		}
		if err := reconciler.settleLeasePendingForReconcileLocked(ctx); err != nil {
			return Receipt{}, err
		}
	}
	stored, err := reconciler.store.LoadDesired()
	switch {
	case err == nil && desired.Generation < stored.Generation:
		return Receipt{}, ErrStaleGeneration
	case err == nil && desired.Generation == stored.Generation && desired.DesiredSHA256() != stored.DesiredSHA256():
		return Receipt{}, ErrConflict
	case err != nil && !errors.Is(err, ErrNotFound):
		return Receipt{}, err
	}
	if !reconciler.managedLeaseEnabled {
		if err := reconciler.store.SaveDesired(desired); err != nil {
			return Receipt{}, err
		}
	}
	bootID, err := reconciler.processBootID()
	if err != nil || !safeIdentifier(bootID) {
		return Receipt{}, errors.New("sidecar agent: Xray process identity unavailable")
	}
	if err := reconciler.store.InvalidateReceiptsExceptBoot(bootID); err != nil {
		return Receipt{}, err
	}
	if err := reconciler.preflight.Validate(ctx, reconciler.releaseID, reconciler.configDigest, bootID, desired.ExitID); err != nil {
		return Receipt{}, errors.New("sidecar agent: relay readiness preflight failed")
	}
	var convergeErr error
	if reconciler.managedLeaseEnabled {
		convergeErr = reconciler.convergeCommercial(ctx, desired, stored)
	} else {
		convergeErr = reconciler.converge(ctx, desired)
	}
	if convergeErr != nil {
		return Receipt{}, convergeErr
	}
	if err := reconciler.preflight.Validate(ctx, reconciler.releaseID, reconciler.configDigest, bootID, desired.ExitID); err != nil {
		return Receipt{}, errors.New("sidecar agent: final relay readiness preflight failed")
	}
	receipt, err := receiptFor(desired, bootID, reconciler.now(), reconciler.receiptTTL)
	if err != nil {
		return Receipt{}, err
	}
	if err := reconciler.store.SaveReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (reconciler *Reconciler) converge(ctx context.Context, desired Desired) error {
	current, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
	if err != nil {
		return errors.New("sidecar agent: list Xray inbound users")
	}
	if err := verifyStaticUsers(current, desired.StaticUsers); err != nil {
		return err
	}
	currentManaged := managedSet(current)
	desiredManaged := stringSet(desired.ManagedUsers)
	replacements := make([]string, 0)
	for email := range desiredManaged {
		if _, present := currentManaged[email]; !present {
			continue
		}
		matches, err := reconciler.handler.ManagedUserAccountMatches(ctx, reconciler.inboundTag, email)
		if err != nil {
			return errors.New("sidecar agent: verify managed Xray account")
		}
		if !matches {
			replacements = append(replacements, email)
		}
	}
	sort.Strings(replacements)
	for _, email := range sortedDifference(desiredManaged, currentManaged) {
		if !managedEmailForExit(email, desired.ExitID) {
			return ErrInvalidDesired
		}
		if err := reconciler.handler.AddUser(ctx, reconciler.inboundTag, email); err != nil {
			return errors.New("sidecar agent: add managed Xray user")
		}
	}
	afterAdds, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
	if err != nil {
		return errors.New("sidecar agent: verify managed Xray additions")
	}
	afterAddManaged := managedSet(afterAdds)
	for email := range desiredManaged {
		if _, ok := afterAddManaged[email]; !ok {
			return fmt.Errorf("sidecar agent: managed Xray addition not visible")
		}
	}
	for _, email := range replacements {
		if err := reconciler.handler.RemoveUser(ctx, reconciler.inboundTag, email); err != nil {
			return errors.New("sidecar agent: remove stale managed Xray account")
		}
		if err := reconciler.handler.AddUser(ctx, reconciler.inboundTag, email); err != nil {
			return errors.New("sidecar agent: replace managed Xray account")
		}
	}
	for _, email := range sortedDifference(afterAddManaged, desiredManaged) {
		if !strings.HasPrefix(email, ManagedPrefix) {
			return errors.New("sidecar agent: refusing non-managed Xray removal")
		}
		if err := reconciler.handler.RemoveUser(ctx, reconciler.inboundTag, email); err != nil {
			return errors.New("sidecar agent: remove managed Xray user")
		}
	}
	final, err := reconciler.handler.ListUsers(ctx, reconciler.inboundTag)
	if err != nil {
		return errors.New("sidecar agent: verify exact managed Xray set")
	}
	if err := verifyStaticUsers(final, desired.StaticUsers); err != nil {
		return err
	}
	if !setsEqual(managedSet(final), desiredManaged) {
		return errors.New("sidecar agent: exact managed Xray convergence failed")
	}
	for email := range desiredManaged {
		matches, err := reconciler.handler.ManagedUserAccountMatches(ctx, reconciler.inboundTag, email)
		if err != nil || !matches {
			return errors.New("sidecar agent: exact managed Xray account convergence failed")
		}
	}
	return nil
}

func verifyStaticUsers(current, expected []string) error {
	currentSet := stringSet(current)
	for _, email := range expected {
		if _, ok := currentSet[email]; !ok {
			return errors.New("sidecar agent: expected static Xray user is missing")
		}
	}
	return nil
}

func managedSet(values []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, value := range values {
		if strings.HasPrefix(value, ManagedPrefix) {
			result[value] = struct{}{}
		}
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func sortedDifference(left, right map[string]struct{}) []string {
	result := make([]string, 0)
	for value := range left {
		if _, ok := right[value]; !ok {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func setsEqual(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
}
