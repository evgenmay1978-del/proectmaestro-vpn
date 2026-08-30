package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const (
	defaultSubscriptionCacheTTL          = 60 * time.Minute
	maxSubscriptionCacheTokens           = 4096
	maxSubscriptionCacheVariantsPerToken = 32
	maxSubscriptionCacheEntries          = 8192
)

type subscriptionStateSource interface {
	BusinessSubscriptionLookupHMACs(string, string) (string, string, error)
	BusinessSubscriptionSnapshot(context.Context, string, string) (controlplane.BusinessSubscriptionSnapshot, error)
	ClaimSubscriptionDevice(context.Context, controlplane.SubscriptionDeviceClaimCommand) (controlplane.DeviceClaim, error)
}

type subscriptionCacheKey struct {
	TokenHMAC  string
	DeviceHMAC string
	Variant    string
}

type subscriptionCacheIdentity struct {
	RestoreEpoch       int64
	CustomerGeneration int64
	TokenGeneration    int64
	SettingsGeneration int64
	SchemaVersion      int64
}

type subscriptionCacheVersion struct {
	Identity   subscriptionCacheIdentity
	VerifiedAt time.Time
}

type cachedSubscription struct {
	Snapshot SubscriptionSnapshot
	Version  subscriptionCacheVersion
}

type subscriptionTokenCache struct {
	Customer CustomerView
	Version  subscriptionCacheVersion
}

type subscriptionCacheEpoch struct {
	version uint64
	refs    int
	devices map[string]*subscriptionCacheDeviceEpoch
}

type subscriptionCacheDeviceEpoch struct {
	version uint64
	refs    int
}

type subscriptionCacheFence struct {
	TokenVersion  uint64
	DeviceVersion uint64
	DeviceHMAC    string
}

type subscriptionCache struct {
	mu      sync.RWMutex
	entries map[subscriptionCacheKey]cachedSubscription
	tokens  map[string]subscriptionTokenCache
	epochs  map[string]*subscriptionCacheEpoch
}

func newSubscriptionCache() *subscriptionCache {
	return &subscriptionCache{
		entries: make(map[subscriptionCacheKey]cachedSubscription),
		tokens:  make(map[string]subscriptionTokenCache),
		epochs:  make(map[string]*subscriptionCacheEpoch),
	}
}

func (cache *subscriptionCache) begin(tokenHMAC, deviceHMAC string) subscriptionCacheFence {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state := cache.epochs[tokenHMAC]
	if state == nil {
		state = &subscriptionCacheEpoch{devices: make(map[string]*subscriptionCacheDeviceEpoch)}
		cache.epochs[tokenHMAC] = state
	}
	device := state.devices[deviceHMAC]
	if device == nil {
		device = &subscriptionCacheDeviceEpoch{}
		state.devices[deviceHMAC] = device
	}
	state.refs++
	device.refs++
	return subscriptionCacheFence{TokenVersion: state.version, DeviceVersion: device.version, DeviceHMAC: deviceHMAC}
}

func (cache *subscriptionCache) end(tokenHMAC, deviceHMAC string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state := cache.epochs[tokenHMAC]
	if state == nil || state.refs <= 0 {
		return
	}
	state.refs--
	if device := state.devices[deviceHMAC]; device != nil && device.refs > 0 {
		device.refs--
		if device.refs == 0 {
			delete(state.devices, deviceHMAC)
		}
	}
	cache.cleanupEpochLocked(tokenHMAC)
}

func (cache *subscriptionCache) cleanupEpochLocked(tokenHMAC string) {
	state := cache.epochs[tokenHMAC]
	if state != nil && state.refs == 0 {
		if _, cached := cache.tokens[tokenHMAC]; !cached {
			delete(cache.epochs, tokenHMAC)
		}
	}
}

func (cache *subscriptionCache) invalidate(tokenHMAC, deviceHMAC string) subscriptionCacheFence {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state := cache.epochs[tokenHMAC]
	if state == nil {
		state = &subscriptionCacheEpoch{devices: make(map[string]*subscriptionCacheDeviceEpoch)}
		cache.epochs[tokenHMAC] = state
	}
	state.version++
	cache.deleteTokenLocked(tokenHMAC)
	cache.cleanupEpochLocked(tokenHMAC)
	return cache.fenceLocked(state, deviceHMAC)
}

func (cache *subscriptionCache) invalidateDevice(tokenHMAC, deviceHMAC string, identity subscriptionCacheIdentity) subscriptionCacheFence {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state := cache.epochs[tokenHMAC]
	if state == nil {
		state = &subscriptionCacheEpoch{devices: make(map[string]*subscriptionCacheDeviceEpoch)}
		cache.epochs[tokenHMAC] = state
	}
	device := state.devices[deviceHMAC]
	if device == nil {
		device = &subscriptionCacheDeviceEpoch{}
		state.devices[deviceHMAC] = device
	}
	current, cached := cache.tokens[tokenHMAC]
	switch {
	case !cached:
		state.version++
		cache.deleteTokenLocked(tokenHMAC)
	case compareSubscriptionIdentity(identity, current.Version.Identity) == subscriptionIdentityEqual:
		device.version++
		for key := range cache.entries {
			if key.TokenHMAC == tokenHMAC && key.DeviceHMAC == deviceHMAC {
				delete(cache.entries, key)
			}
		}
	case compareSubscriptionIdentity(identity, current.Version.Identity) == subscriptionIdentityOlder:
		device.version++
		// A stale strong request may fence its own store, but must not delete a
		// newer cache identity installed by a concurrent request.
	default:
		state.version++
		cache.deleteTokenLocked(tokenHMAC)
	}
	cache.cleanupEpochLocked(tokenHMAC)
	return cache.fenceLocked(state, deviceHMAC)
}

func (cache *subscriptionCache) fenceLocked(state *subscriptionCacheEpoch, deviceHMAC string) subscriptionCacheFence {
	deviceVersion := uint64(0)
	if device := state.devices[deviceHMAC]; device != nil {
		deviceVersion = device.version
	}
	return subscriptionCacheFence{TokenVersion: state.version, DeviceVersion: deviceVersion, DeviceHMAC: deviceHMAC}
}

func (cache *subscriptionCache) deleteTokenLocked(tokenHMAC string) {
	delete(cache.tokens, tokenHMAC)
	for key := range cache.entries {
		if key.TokenHMAC == tokenHMAC {
			delete(cache.entries, key)
		}
	}
}

func (cache *subscriptionCache) store(
	tokenHMAC string,
	epoch subscriptionCacheFence,
	key subscriptionCacheKey,
	snapshot SubscriptionSnapshot,
	version subscriptionCacheVersion,
	_ time.Time,
	_ time.Duration,
) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state := cache.epochs[tokenHMAC]
	if state == nil || state.version != epoch.TokenVersion {
		return
	}
	device := state.devices[epoch.DeviceHMAC]
	current, exists := cache.tokens[tokenHMAC]
	if device == nil || device.version != epoch.DeviceVersion {
		if !exists || compareSubscriptionIdentity(version.Identity, current.Version.Identity) != subscriptionIdentityNewer {
			return
		}
	}
	if exists && current.Version.Identity != version.Identity {
		switch compareSubscriptionIdentity(version.Identity, current.Version.Identity) {
		case subscriptionIdentityNewer:
			state.version++
			for existing := range cache.entries {
				if existing.TokenHMAC == tokenHMAC {
					delete(cache.entries, existing)
				}
			}
		case subscriptionIdentityIncomparable:
			state.version++
			cache.deleteTokenLocked(tokenHMAC)
			cache.cleanupEpochLocked(tokenHMAC)
			return
		default:
			return
		}
	}
	if entry, ok := cache.entries[key]; ok {
		if entry.Version.Identity != version.Identity {
			switch compareSubscriptionIdentity(version.Identity, entry.Version.Identity) {
			case subscriptionIdentityNewer:
			case subscriptionIdentityIncomparable:
				state.version++
				cache.deleteTokenLocked(tokenHMAC)
				cache.cleanupEpochLocked(tokenHMAC)
				return
			default:
				return
			}
		}
		if entry.Version.Identity == version.Identity && version.VerifiedAt.Before(entry.Version.VerifiedAt) {
			return
		}
	}
	safeSnapshot := cloneSubscriptionSnapshot(snapshot)
	safeSnapshot.Customer.SubURL = ""
	metadata := safeSnapshot.Customer
	if !exists || current.Version.Identity != version.Identity || !version.VerifiedAt.Before(current.Version.VerifiedAt) {
		cache.tokens[tokenHMAC] = subscriptionTokenCache{Customer: metadata, Version: version}
	}
	cache.entries[key] = cachedSubscription{Snapshot: safeSnapshot, Version: version}
	cache.pruneTokenVariantsLocked(tokenHMAC)
	for len(cache.tokens) > maxSubscriptionCacheTokens {
		if !cache.evictOldestTokenLocked(tokenHMAC) {
			break
		}
	}
	for len(cache.entries) > maxSubscriptionCacheEntries {
		cache.evictOldestEntryLocked()
	}
}

type subscriptionIdentityOrder uint8

const (
	subscriptionIdentityOlder subscriptionIdentityOrder = iota
	subscriptionIdentityEqual
	subscriptionIdentityNewer
	subscriptionIdentityIncomparable
)

func compareSubscriptionIdentity(next, current subscriptionCacheIdentity) subscriptionIdentityOrder {
	if next.RestoreEpoch < current.RestoreEpoch {
		return subscriptionIdentityOlder
	}
	if next.RestoreEpoch > current.RestoreEpoch {
		return subscriptionIdentityNewer
	}
	if next == current {
		return subscriptionIdentityEqual
	}
	nextAtLeast := next.CustomerGeneration >= current.CustomerGeneration &&
		next.TokenGeneration >= current.TokenGeneration && next.SettingsGeneration >= current.SettingsGeneration &&
		next.SchemaVersion >= current.SchemaVersion
	currentAtLeast := current.CustomerGeneration >= next.CustomerGeneration &&
		current.TokenGeneration >= next.TokenGeneration && current.SettingsGeneration >= next.SettingsGeneration &&
		current.SchemaVersion >= next.SchemaVersion
	if nextAtLeast && !currentAtLeast {
		return subscriptionIdentityNewer
	}
	if currentAtLeast && !nextAtLeast {
		return subscriptionIdentityOlder
	}
	return subscriptionIdentityIncomparable
}

func (cache *subscriptionCache) pruneTokenVariantsLocked(tokenHMAC string) {
	for {
		count := 0
		var oldestKey subscriptionCacheKey
		var oldest time.Time
		found := false
		for key, entry := range cache.entries {
			if key.TokenHMAC != tokenHMAC {
				continue
			}
			count++
			if !found || entry.Version.VerifiedAt.Before(oldest) {
				oldestKey, oldest, found = key, entry.Version.VerifiedAt, true
			}
		}
		if count <= maxSubscriptionCacheVariantsPerToken || !found {
			return
		}
		delete(cache.entries, oldestKey)
	}
}

func (cache *subscriptionCache) fenceAndDeleteTokenLocked(tokenHMAC string) {
	state := cache.epochs[tokenHMAC]
	if state == nil {
		state = &subscriptionCacheEpoch{devices: make(map[string]*subscriptionCacheDeviceEpoch)}
		cache.epochs[tokenHMAC] = state
	}
	state.version++
	cache.deleteTokenLocked(tokenHMAC)
	cache.cleanupEpochLocked(tokenHMAC)
}

func (cache *subscriptionCache) evictOldestTokenLocked(protectedToken string) bool {
	var oldestToken string
	var oldest time.Time
	found := false
	for tokenHMAC, metadata := range cache.tokens {
		if tokenHMAC == protectedToken {
			continue
		}
		if !found || metadata.Version.VerifiedAt.Before(oldest) {
			oldestToken, oldest, found = tokenHMAC, metadata.Version.VerifiedAt, true
		}
	}
	if found {
		cache.fenceAndDeleteTokenLocked(oldestToken)
	}
	return found
}

func (cache *subscriptionCache) evictOldestEntryLocked() {
	var oldestKey subscriptionCacheKey
	var oldest time.Time
	found := false
	for key, entry := range cache.entries {
		if !found || entry.Version.VerifiedAt.Before(oldest) {
			oldestKey, oldest, found = key, entry.Version.VerifiedAt, true
		}
	}
	if found {
		delete(cache.entries, oldestKey)
	}
}

func (cache *subscriptionCache) load(
	tokenHMAC string,
	deviceHMAC string,
	variant string,
	now time.Time,
	ttl time.Duration,
	selectDevice func(CustomerView) (string, bool),
) (SubscriptionSnapshot, bool) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	metadata, ok := cache.tokens[tokenHMAC]
	if !ok || !validSubscriptionCacheVersion(metadata.Version, now, ttl) {
		return SubscriptionSnapshot{}, false
	}
	customer := metadata.Customer
	if !customer.Expires.After(now) {
		customer.Active = false
	}
	if !customer.Active {
		return SubscriptionSnapshot{Customer: cloneCustomerView(customer), AsOf: now, Cached: true}, true
	}
	wantedDevice, allowed := selectDevice(customer)
	if !allowed || (wantedDevice != "" && wantedDevice != deviceHMAC) {
		return SubscriptionSnapshot{}, false
	}
	entry, ok := cache.entries[subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: wantedDevice, Variant: variant}]
	if !ok || entry.Version.Identity != metadata.Version.Identity || !validSubscriptionCacheVersion(entry.Version, now, ttl) {
		return SubscriptionSnapshot{}, false
	}
	snapshot := cloneSubscriptionSnapshot(entry.Snapshot)
	snapshot.AsOf = now
	snapshot.Cached = true
	return snapshot, true
}

func validSubscriptionCacheVersion(version subscriptionCacheVersion, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 || version.VerifiedAt.IsZero() || version.VerifiedAt.After(now) {
		return false
	}
	return now.Sub(version.VerifiedAt) <= ttl
}

func cloneCustomerView(customer CustomerView) CustomerView {
	clone := customer
	clone.Protocols = append([]string(nil), customer.Protocols...)
	return clone
}

func cloneSubscriptionSnapshot(snapshot SubscriptionSnapshot) SubscriptionSnapshot {
	clone := snapshot
	clone.Customer = cloneCustomerView(snapshot.Customer)
	clone.Document = append(json.RawMessage(nil), snapshot.Document...)
	return clone
}

func subscriptionVariant(options subscriptionRenderOptions) string {
	payload, _ := json.Marshal(struct {
		Endpoint      subscriptionEndpointKind `json:"endpoint"`
		ClientRequest bool                     `json:"client_request"`
		UserAgent     string                   `json:"user_agent"`
		Links         bool                     `json:"links"`
		DNSFakeIPOff  bool                     `json:"dns_fake_ip_off"`
		AWGMinimum    int                      `json:"awg_minimum"`
	}{options.endpoint(), options.ClientRequest, options.UserAgent, options.Links, dnsFakeIPOff, awgMinVC})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (b *ServiceBusiness) subscriptionSnapshotWithState(ctx context.Context, token string, options subscriptionRenderOptions) (SubscriptionSnapshot, error) {
	tokenHMAC, deviceHMAC, err := b.subscriptionStates.BusinessSubscriptionLookupHMACs(token, options.DeviceID)
	if err != nil {
		return SubscriptionSnapshot{}, businessError(err)
	}
	epoch := b.subscriptionCache.begin(tokenHMAC, deviceHMAC)
	defer b.subscriptionCache.end(tokenHMAC, deviceHMAC)
	variant := subscriptionVariant(options)
	state, err := b.subscriptionStates.BusinessSubscriptionSnapshot(ctx, token, options.DeviceID)
	if err != nil {
		return b.subscriptionFallback(tokenHMAC, deviceHMAC, variant, options, err)
	}
	requestNow := b.requestNow()
	if !validBusinessSubscriptionState(state, tokenHMAC, deviceHMAC, requestNow) {
		b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
		return SubscriptionSnapshot{}, businessError(controlplane.ErrInvalidState)
	}
	now := time.Unix(state.DatabaseNowUnix, 0).UTC()
	identity := subscriptionCacheIdentity{
		RestoreEpoch:       state.RestoreEpoch,
		CustomerGeneration: state.Customer.Generation,
		TokenGeneration:    state.TokenGeneration,
		SettingsGeneration: state.SettingsGeneration,
		SchemaVersion:      state.SchemaVersion,
	}
	if len(state.Customer.Access.Credentials) == 0 {
		epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
	}
	limit := b.resolveDeviceLimit(state.Customer.Login)
	gateEnabled := options.EnforceDeviceLimit && limit >= 0
	active := state.Customer.Status == "active" && time.Unix(state.Customer.ExpiresAtUnix, 0).After(now)
	requireCredentials := options.endpoint() == subscriptionEndpointBase
	if active && requireCredentials && len(state.Customer.Access.Credentials) == 0 {
		return b.renderBusinessSubscriptionState(state, options, now)
	}
	admit := active && options.endpoint() != subscriptionEndpointInfo && gateEnabled && options.DeviceID != ""
	if admit {
		claimApplied := false
		var claim controlplane.DeviceClaim
		for attempt := 0; attempt < 2; attempt++ {
			var claimErr error
			claim, claimErr = b.subscriptionStates.ClaimSubscriptionDevice(ctx, controlplane.SubscriptionDeviceClaimCommand{
				CustomerID: state.Customer.ID, TokenHMAC: state.TokenKeyHMAC, RawDeviceIdentity: options.DeviceID,
				Platform: "maestro", Limit: limit, ExpectedCustomerGeneration: state.Customer.Generation,
				RequireCredentials: requireCredentials, ExpectedTokenGeneration: state.TokenGeneration,
				ExpectedExpiresAtUnix: state.Customer.ExpiresAtUnix, ExpectedRestoreEpoch: state.RestoreEpoch,
			})
			if claimErr == nil {
				if claim.DeviceID == "" || claim.AdmittedAtUnix <= 0 || claim.AdmittedAtUnix >= state.Customer.ExpiresAtUnix {
					epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return SubscriptionSnapshot{}, businessError(controlplane.ErrInvalidState)
				}
				claimApplied = true
				break
			}
			if errors.Is(claimErr, controlplane.ErrSubscriptionChanged) {
				if attempt > 0 {
					epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
				}
				changed, changeErr := b.subscriptionStates.BusinessSubscriptionSnapshot(ctx, token, options.DeviceID)
				if changeErr != nil {
					b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return SubscriptionSnapshot{}, businessError(changeErr)
				}
				changedVerifiedAt := b.requestNow()
				if !validBusinessSubscriptionState(changed, tokenHMAC, deviceHMAC, changedVerifiedAt) || changed.Customer.ID != state.Customer.ID {
					b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return SubscriptionSnapshot{}, businessError(controlplane.ErrInvalidState)
				}
				changedAt := time.Unix(changed.DatabaseNowUnix, 0).UTC()
				changedActive := changed.Customer.Status == "active" && time.Unix(changed.Customer.ExpiresAtUnix, 0).After(changedAt)
				if !changedActive || (requireCredentials && len(changed.Customer.Access.Credentials) == 0) {
					b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return b.renderBusinessSubscriptionState(changed, options, changedAt)
				}
				state = changed
				now = changedAt
				identity = subscriptionCacheIdentity{
					RestoreEpoch:       state.RestoreEpoch,
					CustomerGeneration: state.Customer.Generation,
					TokenGeneration:    state.TokenGeneration,
					SettingsGeneration: state.SettingsGeneration,
					SchemaVersion:      state.SchemaVersion,
				}
				limit = b.resolveDeviceLimit(state.Customer.Login)
				if limit < 0 {
					gateEnabled = false
					admit = false
					break
				}
				continue
			}
			if errors.Is(claimErr, controlplane.ErrDeviceLimit) {
				if state.DeviceCommitted {
					epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
				} else {
					epoch = b.subscriptionCache.invalidateDevice(tokenHMAC, deviceHMAC, identity)
				}
				return SubscriptionSnapshot{}, businessError(claimErr)
			}
			if errors.Is(claimErr, controlplane.ErrUnavailable) {
				if !state.DeviceCommitted {
					epoch = b.subscriptionCache.invalidateDevice(tokenHMAC, deviceHMAC, identity)
					return SubscriptionSnapshot{}, businessError(claimErr)
				}
				break
			} else {
				epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
				return SubscriptionSnapshot{}, businessError(claimErr)
			}
		}
		if claimApplied {
			admittedAt := time.Unix(claim.AdmittedAtUnix, 0).UTC()
			verified, verifyErr := b.subscriptionStates.BusinessSubscriptionSnapshot(ctx, token, options.DeviceID)
			if verifyErr != nil {
				if errors.Is(verifyErr, controlplane.ErrUnavailable) {
					if !state.DeviceCommitted {
						epoch = b.subscriptionCache.invalidateDevice(tokenHMAC, deviceHMAC, identity)
						return SubscriptionSnapshot{}, businessError(verifyErr)
					}
				} else {
					epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return SubscriptionSnapshot{}, businessError(verifyErr)
				}
			} else {
				verifiedAt := b.requestNow()
				if !validBusinessSubscriptionState(verified, tokenHMAC, deviceHMAC, verifiedAt) ||
					!sameSubscriptionAdmissionAuthorization(state, verified, requireCredentials) {
					b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
					return SubscriptionSnapshot{}, businessError(controlplane.ErrInvalidState)
				}
				state = verified
				now = admittedAt
				identity = subscriptionCacheIdentity{
					RestoreEpoch:       state.RestoreEpoch,
					CustomerGeneration: state.Customer.Generation,
					TokenGeneration:    state.TokenGeneration,
					SettingsGeneration: state.SettingsGeneration,
					SchemaVersion:      state.SchemaVersion,
				}
				if len(state.Customer.Access.Credentials) == 0 {
					epoch = b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
				}
			}
		}
	}
	snapshot, err := b.renderBusinessSubscriptionState(state, options, now)
	if err != nil {
		b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
		return SubscriptionSnapshot{}, err
	}
	active = snapshot.Customer.Active
	cacheAllowed := !active || options.endpoint() == subscriptionEndpointInfo || !gateEnabled || admit
	if cacheAllowed {
		cacheDeviceHMAC := ""
		if active && options.endpoint() != subscriptionEndpointInfo && gateEnabled {
			cacheDeviceHMAC = deviceHMAC
		}
		version := subscriptionCacheVersion{
			Identity:   identity,
			VerifiedAt: state.VerifiedAt,
		}
		b.subscriptionCache.store(tokenHMAC, epoch, subscriptionCacheKey{
			TokenHMAC: tokenHMAC, DeviceHMAC: cacheDeviceHMAC, Variant: variant,
		}, snapshot, version, now, b.subscriptionCacheTTL)
	}
	return snapshot, nil
}

func (b *ServiceBusiness) subscriptionFallback(tokenHMAC, deviceHMAC, variant string, options subscriptionRenderOptions, cause error) (SubscriptionSnapshot, error) {
	if errors.Is(cause, controlplane.ErrNotFound) {
		b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
		return SubscriptionSnapshot{}, businessError(controlplane.ErrNotFound)
	}
	if errors.Is(cause, controlplane.ErrInvalidState) {
		b.subscriptionCache.invalidate(tokenHMAC, deviceHMAC)
		return SubscriptionSnapshot{}, businessError(cause)
	}
	if !errors.Is(cause, controlplane.ErrUnavailable) {
		return SubscriptionSnapshot{}, businessError(cause)
	}
	now := b.requestNow()
	snapshot, ok := b.subscriptionCache.load(tokenHMAC, deviceHMAC, variant, now, b.subscriptionCacheTTL, func(customer CustomerView) (string, bool) {
		if options.endpoint() == subscriptionEndpointInfo || !options.EnforceDeviceLimit || b.resolveDeviceLimit(customer.Login) < 0 {
			return "", true
		}
		if options.DeviceID == "" || deviceHMAC == "" {
			return "", false
		}
		return deviceHMAC, true
	})
	if ok {
		return snapshot, nil
	}
	return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
}

func validBusinessSubscriptionState(state controlplane.BusinessSubscriptionSnapshot, tokenHMAC, deviceHMAC string, now time.Time) bool {
	return state.Customer.ID != "" && state.TokenKeyHMAC == tokenHMAC && state.DeviceKeyHMAC == deviceHMAC &&
		state.Customer.Generation >= 0 && state.TokenGeneration > 0 && state.TokenGeneration <= state.Customer.Generation &&
		state.SettingsGeneration >= 0 && state.SchemaVersion > 0 && state.RestoreEpoch > 0 && state.DatabaseNowUnix > 0 &&
		!state.VerifiedAt.IsZero() && !state.VerifiedAt.After(now)
}

func sameSubscriptionAdmissionAuthorization(expected, actual controlplane.BusinessSubscriptionSnapshot, requireCredentials bool) bool {
	return expected.Customer.ID == actual.Customer.ID &&
		expected.Customer.Generation == actual.Customer.Generation &&
		expected.Customer.ExpiresAtUnix == actual.Customer.ExpiresAtUnix &&
		expected.DeviceKeyHMAC == actual.DeviceKeyHMAC &&
		expected.TokenKeyHMAC == actual.TokenKeyHMAC &&
		expected.TokenGeneration == actual.TokenGeneration &&
		expected.RestoreEpoch == actual.RestoreEpoch &&
		actual.Customer.Status == "active" && actual.DeviceCommitted &&
		(!requireCredentials || len(actual.Customer.Access.Credentials) > 0)
}

func (b *ServiceBusiness) renderBusinessSubscriptionState(state controlplane.BusinessSubscriptionSnapshot, options subscriptionRenderOptions, now time.Time) (SubscriptionSnapshot, error) {
	snapshot := SubscriptionSnapshot{Customer: b.customerViewAt(state.Customer, now), AsOf: now}
	if !snapshot.Customer.Active || options.endpoint() == subscriptionEndpointInfo || options.endpoint() == subscriptionEndpointHelpers {
		return snapshot, nil
	}
	if len(state.Customer.Access.Credentials) == 0 {
		return SubscriptionSnapshot{}, businessError(controlplane.ErrForbidden)
	}
	document, contentType, err := renderControlPlaneSubscription(state.Customer, b.cfg.SubscriptionTopology, options)
	if err != nil {
		return SubscriptionSnapshot{}, businessError(controlplane.ErrUnavailable)
	}
	snapshot.Document, snapshot.ContentType = document, contentType
	return snapshot, nil
}
