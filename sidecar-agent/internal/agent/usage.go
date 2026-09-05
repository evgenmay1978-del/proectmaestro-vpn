package agent

import (
	"context"
	"errors"
	"strings"
	"time"
)

type UserUsage struct {
	Email         string `json:"email"`
	UplinkBytes   uint64 `json:"uplink_bytes"`
	DownlinkBytes uint64 `json:"downlink_bytes"`
}

type UsageSnapshot struct {
	Receipt          Receipt     `json:"receipt"`
	SampledAt        time.Time   `json:"sampled_at"`
	Users            []UserUsage `json:"users"`
	UnavailableUsers []string    `json:"unavailable_users"`
}

// Missing map entries mean an incomplete counter pair, never a zero sample.
type managedCounterReader interface {
	ManagedUserCounters(context.Context, []string) (map[string][2]uint64, error)
}

type runtimeBindingReader interface {
	ValidateRuntimeBinding(string, string) error
}

// Usage reads counters for the currently applied desired set without refreshing
// its receipt, reconciling users, creating counters, or probing relay traffic.
func (reconciler *Reconciler) Usage(ctx context.Context, actionKey string) (UsageSnapshot, error) {
	if reconciler == nil || ctx == nil || actionKey == "" || strings.TrimSpace(actionKey) != actionKey ||
		strings.ContainsAny(actionKey, "\x00\r\n\t") {
		return UsageSnapshot{}, ErrNotFound
	}
	reconciler.mutex.Lock()
	defer reconciler.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		return UsageSnapshot{}, err
	}
	desired, err := reconciler.store.LoadDesired()
	if err != nil {
		return UsageSnapshot{}, err
	}
	if desired.ActionKey() != actionKey {
		return UsageSnapshot{}, ErrNotFound
	}
	receipt, err := reconciler.store.LoadReceipt(actionKey)
	if err != nil {
		return UsageSnapshot{}, err
	}
	counters, countersOK := reconciler.handler.(managedCounterReader)
	binding, bindingOK := reconciler.preflight.(runtimeBindingReader)
	if !countersOK || !bindingOK {
		return UsageSnapshot{}, errors.New("sidecar agent: usage reader unavailable")
	}
	validateBinding := func() error {
		bootID, err := reconciler.processBootID()
		if err != nil || !safeIdentifier(bootID) || receipt.XrayProcessBootID != bootID ||
			desired.ReleaseID != reconciler.releaseID || desired.ConfigDigest != reconciler.configDigest ||
			receipt.ActionKey != actionKey || receipt.OriginID != desired.OriginID ||
			receipt.ReleaseID != desired.ReleaseID || receipt.ConfigDigest != desired.ConfigDigest ||
			receipt.DesiredGeneration != desired.Generation || receipt.ManagedUserSetDigest != desired.ManagedUserSetDigest ||
			!receipt.ReadyAt(reconciler.now()) {
			return errors.New("sidecar agent: usage runtime binding unavailable")
		}
		if err := binding.ValidateRuntimeBinding(reconciler.releaseID, reconciler.configDigest); err != nil {
			return errors.New("sidecar agent: usage config binding unavailable")
		}
		return ctx.Err()
	}
	if err := validateBinding(); err != nil {
		return UsageSnapshot{}, err
	}
	values, err := counters.ManagedUserCounters(ctx, append([]string(nil), desired.ManagedUsers...))
	if err != nil {
		return UsageSnapshot{}, errors.New("sidecar agent: usage counters unavailable")
	}
	sampledAt := reconciler.now().UTC()
	if err := validateBinding(); err != nil {
		return UsageSnapshot{}, err
	}
	result := UsageSnapshot{
		Receipt: receipt, SampledAt: sampledAt,
		Users: make([]UserUsage, 0, len(desired.ManagedUsers)), UnavailableUsers: make([]string, 0),
	}
	for _, email := range desired.ManagedUsers {
		value, ok := values[email]
		if !ok {
			result.UnavailableUsers = append(result.UnavailableUsers, email)
			continue
		}
		result.Users = append(result.Users, UserUsage{Email: email, UplinkBytes: value[0], DownlinkBytes: value[1]})
	}
	if len(result.Users) != len(values) {
		return UsageSnapshot{}, errors.New("sidecar agent: usage counters escaped managed set")
	}
	return result, nil
}
