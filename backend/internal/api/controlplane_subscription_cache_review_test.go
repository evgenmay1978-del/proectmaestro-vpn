package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestSubscriptionCacheRejectsStaleClockIdentityDowngrade(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("a", 64)
	key := subscriptionCacheKey{TokenHMAC: tokenHMAC, Variant: "base"}
	epoch := cache.begin(tokenHMAC, "")
	defer cache.end(tokenHMAC, "")
	verifiedAt := time.Date(2035, time.January, 2, 3, 4, 5, 500, time.UTC)
	ttl := time.Hour

	cache.store(tokenHMAC, epoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: verifiedAt.Add(time.Hour), Generation: 2},
		Document: json.RawMessage(`{"generation":2}`),
	}, subscriptionCacheVersion{
		Identity:   subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 2, TokenGeneration: 1, SettingsGeneration: 2, SchemaVersion: 8},
		VerifiedAt: verifiedAt,
	}, verifiedAt, ttl)

	cache.store(tokenHMAC, epoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: verifiedAt.Add(time.Hour), Generation: 1},
		Document: json.RawMessage(`{"generation":1}`),
	}, subscriptionCacheVersion{
		Identity:   subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8},
		VerifiedAt: verifiedAt.Add(-time.Second),
	}, verifiedAt.Add(-time.Second), ttl)

	got, ok := cache.load(tokenHMAC, "", "base", verifiedAt, ttl, func(CustomerView) (string, bool) { return "", true })
	if !ok {
		t.Fatal("newer cache entry disappeared after stale store")
	}
	if got.Customer.Generation != 2 || string(got.Document) != `{"generation":2}` {
		t.Fatalf("stale store replaced newer identity: generation=%d document=%s", got.Customer.Generation, got.Document)
	}
}

func TestSubscriptionStrongMetadataWithoutCredentialsClearsSecretVariants(t *testing.T) {
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	source := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
	business := newSubscriptionReviewBusiness(&now, source)
	base := subscriptionReviewOptions(subscriptionEndpointBase)
	info := subscriptionReviewOptions(subscriptionEndpointInfo)

	warm, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", base)
	if err != nil || len(warm.Document) == 0 {
		t.Fatalf("warm secret variant: document=%q err=%v", warm.Document, err)
	}

	now = now.Add(time.Second)
	source.state = subscriptionReviewState(now, 1, true, "")
	source.reset()
	metadata, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", info)
	if err != nil || len(metadata.Customer.Protocols) != 0 {
		t.Fatalf("healthy credential-less metadata: protocols=%v err=%v", metadata.Customer.Protocols, err)
	}

	source.snapshotErr = controlplane.ErrUnavailable
	cachedMetadata, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", info)
	if err != nil || !cachedMetadata.Cached || len(cachedMetadata.Customer.Protocols) != 0 {
		t.Fatalf("credential-less metadata cache: cached=%v protocols=%v err=%v", cachedMetadata.Cached, cachedMetadata.Customer.Protocols, err)
	}
	if stale, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", base); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("old secret variant survived credential removal: cached=%v document=%q err=%v", stale.Cached, stale.Document, err)
	}
}

func TestSubscriptionMidRequestOutageNeverUsesHistoricalCache(t *testing.T) {
	for _, failure := range []string{"claim", "verify"} {
		for _, committed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/committed=%t", failure, committed), func(t *testing.T) {
				now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
				source := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
				business := newSubscriptionReviewBusiness(&now, source)
				options := subscriptionReviewOptions(subscriptionEndpointBase)

				warm, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
				if err != nil || len(warm.Document) == 0 {
					t.Fatalf("warm historical cache: document=%q err=%v", warm.Document, err)
				}

				now = now.Add(time.Second)
				source.state = subscriptionReviewState(now, 2, committed, "22222222-2222-4222-8222-222222222222")
				source.reset()
				if failure == "claim" {
					source.claimErr = controlplane.ErrUnavailable
				} else {
					source.verifyErr = controlplane.ErrUnavailable
				}

				got, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
				if !committed {
					if !errors.Is(err, controlplane.ErrUnavailable) {
						t.Fatalf("uncommitted device reused history: cached=%v generation=%d err=%v", got.Cached, got.Customer.Generation, err)
					}
				} else if err != nil || got.Cached || got.Customer.Generation != 2 {
					t.Fatalf("committed device did not use coherent current state: cached=%v generation=%d err=%v", got.Cached, got.Customer.Generation, err)
				}

				source.reset()
				source.snapshotErr = controlplane.ErrUnavailable
				cached, cachedErr := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
				if committed {
					if cachedErr != nil || !cached.Cached || cached.Customer.Generation != 2 {
						t.Fatalf("fresh coherent LKG was not cached: cached=%v generation=%d err=%v", cached.Cached, cached.Customer.Generation, cachedErr)
					}
				} else if !errors.Is(cachedErr, controlplane.ErrUnavailable) {
					t.Fatalf("uncommitted device cache resurrected: cached=%v generation=%d err=%v", cached.Cached, cached.Customer.Generation, cachedErr)
				}
			})
		}
	}
}

func TestSubscriptionCommittedClaimUnavailableDoesNotRetryUnknownWrite(t *testing.T) {
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	source := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
	business := newSubscriptionReviewBusiness(&now, source)
	source.claimErr = controlplane.ErrUnavailable

	got, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", subscriptionReviewOptions(subscriptionEndpointBase))
	if err != nil || got.Cached || got.Customer.Generation != 1 {
		t.Fatalf("committed current state: cached=%v generation=%d err=%v", got.Cached, got.Customer.Generation, err)
	}
	if source.claimCalls != 1 {
		t.Fatalf("ambiguous claim write retried: calls=%d", source.claimCalls)
	}
}

func TestSubscriptionRepeatedClaimDriftInvalidatesWholeTokenCache(t *testing.T) {
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	allowedDeviceHMAC := strings.Repeat("c", 64)
	blockedDeviceHMAC := strings.Repeat("d", 64)
	source := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
	source.deviceHMAC = allowedDeviceHMAC
	source.state.DeviceKeyHMAC = allowedDeviceHMAC
	business := newSubscriptionReviewBusiness(&now, source)
	options := subscriptionReviewOptions(subscriptionEndpointBase)
	if _, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options); err != nil {
		t.Fatalf("warm allowed-device cache: %v", err)
	}

	now = now.Add(time.Second)
	source.reset()
	source.deviceHMAC = blockedDeviceHMAC
	source.state = subscriptionReviewState(now, 1, false, "11111111-1111-4111-8111-111111111111")
	source.state.DeviceKeyHMAC = blockedDeviceHMAC
	source.claimErrs = []error{controlplane.ErrSubscriptionChanged, controlplane.ErrSubscriptionChanged}
	if _, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("repeated drift result: %v", err)
	}
	if source.claimCalls != 2 {
		t.Fatalf("bounded conflict retry calls=%d, want 2", source.claimCalls)
	}

	source.reset()
	source.deviceHMAC = allowedDeviceHMAC
	source.snapshotErr = controlplane.ErrUnavailable
	if cached, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options); !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("old allowed-device cache survived repeated drift: cached=%v generation=%d err=%v", cached.Cached, cached.Customer.Generation, err)
	}
}

func TestSubscriptionPostClaimRendersAtAuthorizedAdmissionTime(t *testing.T) {
	admittedAt := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	expiresAt := admittedAt.Add(time.Second)
	now := expiresAt
	source := newSubscriptionReviewSource(now, 1, false, "11111111-1111-4111-8111-111111111111")
	source.state.DatabaseNowUnix = admittedAt.Unix()
	source.state.Customer.ExpiresAtUnix = expiresAt.Unix()
	source.claimAtUnix = admittedAt.Unix()
	verified := source.state
	verified.DeviceCommitted = true
	verified.DatabaseNowUnix = expiresAt.Unix()
	source.verifyState = &verified
	business := newSubscriptionReviewBusiness(&now, source)
	options := subscriptionReviewOptions(subscriptionEndpointBase)

	got, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
	if err != nil || !got.Customer.Active || len(got.Document) == 0 || !got.AsOf.Equal(admittedAt) {
		t.Fatalf("post-claim boundary response: active=%v document=%q as_of=%s err=%v", got.Customer.Active, got.Document, got.AsOf, err)
	}
	if source.claimCalls != 1 {
		t.Fatalf("post-claim boundary claim calls=%d, want 1", source.claimCalls)
	}

	source.reset()
	source.state = verified
	next, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
	if err != nil || next.Customer.Active || len(next.Document) != 0 {
		t.Fatalf("next request did not observe DB expiry: active=%v document=%q err=%v", next.Customer.Active, next.Document, err)
	}
	if source.claimCalls != 0 {
		t.Fatalf("expired next request claimed device: calls=%d", source.claimCalls)
	}
}

func TestSubscriptionPostClaimAuthorizationDriftFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*controlplane.BusinessSubscriptionSnapshot)
	}{
		{name: "customer-id", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.Customer.ID = "other-customer" }},
		{name: "customer-generation", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.Customer.Generation++ }},
		{name: "status", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.Customer.Status = "suspended" }},
		{name: "expiry", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.Customer.ExpiresAtUnix++ }},
		{name: "token-hmac", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.TokenKeyHMAC = strings.Repeat("c", 64) }},
		{name: "token-generation", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.TokenGeneration++ }},
		{name: "restore-epoch", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.RestoreEpoch++ }},
		{name: "credential-removal", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) {
			state.Customer.Access.Credentials = map[string]string{}
		}},
		{name: "device-not-committed", mutate: func(state *controlplane.BusinessSubscriptionSnapshot) { state.DeviceCommitted = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
			source := newSubscriptionReviewSource(now, 1, false, "11111111-1111-4111-8111-111111111111")
			source.claimAtUnix = now.Unix()
			verified := source.state
			verified.DeviceCommitted = true
			test.mutate(&verified)
			source.verifyState = &verified
			business := newSubscriptionReviewBusiness(&now, source)

			got, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", subscriptionReviewOptions(subscriptionEndpointBase))
			if !errors.Is(err, controlplane.ErrInvalidState) {
				t.Fatalf("post-claim drift returned cached=%v document=%q err=%v, want ErrInvalidState", got.Cached, got.Document, err)
			}
			if source.claimCalls != 1 {
				t.Fatalf("post-claim drift claim calls=%d, want 1", source.claimCalls)
			}
		})
	}
}

type subscriptionReviewSource struct {
	tokenHMAC    string
	deviceHMAC   string
	state        controlplane.BusinessSubscriptionSnapshot
	verifyState  *controlplane.BusinessSubscriptionSnapshot
	snapshotErr  error
	verifyErr    error
	claimErr     error
	claimErrs    []error
	claimAtUnix  int64
	claimCalls   int
	snapshotCall int
}

func newSubscriptionReviewSource(now time.Time, generation int64, committed bool, credential string) *subscriptionReviewSource {
	return &subscriptionReviewSource{
		tokenHMAC:  strings.Repeat("a", 64),
		deviceHMAC: strings.Repeat("b", 64),
		state:      subscriptionReviewState(now, generation, committed, credential),
	}
}

func subscriptionReviewState(now time.Time, generation int64, committed bool, credential string) controlplane.BusinessSubscriptionSnapshot {
	credentials := map[string]string{}
	if credential != "" {
		credentials["vless"] = credential
	}
	return controlplane.BusinessSubscriptionSnapshot{
		Customer: controlplane.BusinessCustomer{
			Customer: controlplane.Customer{
				ID: "review-customer", Status: "active", ExpiresAtUnix: now.Add(24 * time.Hour).Unix(), Generation: generation,
				Access: controlplane.CustomerAccess{SubscriptionToken: "review-token", Credentials: credentials},
			},
			Login: "review-user",
		},
		TokenKeyHMAC: strings.Repeat("a", 64), TokenGeneration: 1,
		DeviceKeyHMAC: strings.Repeat("b", 64), DeviceCommitted: committed,
		SettingsGeneration: 1, SchemaVersion: 8, RestoreEpoch: 1, DatabaseNowUnix: now.Unix(), VerifiedAt: now,
	}
}

func (source *subscriptionReviewSource) reset() {
	source.snapshotCall = 0
	source.snapshotErr = nil
	source.verifyErr = nil
	source.verifyState = nil
	source.claimErr = nil
	source.claimErrs = nil
	source.claimCalls = 0
}

func (source *subscriptionReviewSource) BusinessCustomerByToken(context.Context, string) (controlplane.BusinessCustomer, error) {
	return source.state.Customer, nil
}

func (source *subscriptionReviewSource) BusinessSubscriptionLookupHMACs(string, string) (string, string, error) {
	return source.tokenHMAC, source.deviceHMAC, nil
}

func (source *subscriptionReviewSource) BusinessSubscriptionSnapshot(context.Context, string, string) (controlplane.BusinessSubscriptionSnapshot, error) {
	source.snapshotCall++
	if source.snapshotErr != nil {
		return controlplane.BusinessSubscriptionSnapshot{}, source.snapshotErr
	}
	if source.snapshotCall > 1 && source.verifyErr != nil {
		return controlplane.BusinessSubscriptionSnapshot{}, source.verifyErr
	}
	if source.snapshotCall > 1 && source.verifyState != nil {
		return *source.verifyState, nil
	}
	return source.state, nil
}

func (source *subscriptionReviewSource) ClaimSubscriptionDevice(context.Context, controlplane.SubscriptionDeviceClaimCommand) (controlplane.DeviceClaim, error) {
	source.claimCalls++
	if source.claimCalls <= len(source.claimErrs) {
		if err := source.claimErrs[source.claimCalls-1]; err != nil {
			return controlplane.DeviceClaim{}, err
		}
	}
	if source.claimErr != nil {
		return controlplane.DeviceClaim{}, source.claimErr
	}
	admittedAtUnix := source.claimAtUnix
	if admittedAtUnix <= 0 {
		admittedAtUnix = source.state.DatabaseNowUnix
	}
	return controlplane.DeviceClaim{DeviceID: "review-device", AdmittedAtUnix: admittedAtUnix}, nil
}

func newSubscriptionReviewBusiness(now *time.Time, source *subscriptionReviewSource) *ServiceBusiness {
	business := NewServiceBusiness(nil, ServiceBusinessConfig{
		Now: func() time.Time { return *now }, DeviceLimitFor: func(string) int { return 5 }, SubscriptionCacheTTL: time.Hour,
		SubscriptionTopology: subgen.Customer{VLESS: &subgen.VLESSCreds{
			Server: "review.example.test", Port: 443, SNI: "review.example.test", PublicKey: "review-public-key",
			ShortID: "0123456789abcdef", Flow: "xtls-rprx-vision", Fingerprint: "chrome",
		}},
	})
	business.subscriptions = source
	business.subscriptionStates = source
	return business
}

func subscriptionReviewOptions(endpoint subscriptionEndpointKind) subscriptionRenderOptions {
	return subscriptionRenderOptions{
		ClientRequest: true, UserAgent: "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)",
		Endpoint: endpoint, DeviceID: "review-device", EnforceDeviceLimit: true,
	}
}

func TestSubscriptionCacheDeviceFenceIsIdentityAware(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("c", 64)
	allowedDeviceHMAC := strings.Repeat("d", 64)
	blockedDeviceHMAC := strings.Repeat("e", 64)
	verifiedAt := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	ttl := time.Hour
	identity1 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	identity2 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 2, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	snapshot := SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: verifiedAt.Add(time.Hour), Generation: 1},
		Document: json.RawMessage(`{"allowed":true}`),
	}
	allowedKey := subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: allowedDeviceHMAC, Variant: "base"}
	loadAllowed := func() bool {
		_, ok := cache.load(tokenHMAC, allowedDeviceHMAC, "base", verifiedAt, ttl, func(CustomerView) (string, bool) {
			return allowedDeviceHMAC, true
		})
		return ok
	}

	allowedEpoch := cache.begin(tokenHMAC, allowedDeviceHMAC)
	cache.store(tokenHMAC, allowedEpoch, allowedKey, snapshot, subscriptionCacheVersion{
		Identity: identity1, VerifiedAt: verifiedAt,
	}, verifiedAt, ttl)
	cache.end(tokenHMAC, allowedDeviceHMAC)

	staleBlockedEpoch := cache.begin(tokenHMAC, blockedDeviceHMAC)
	currentEpoch := cache.invalidateDevice(tokenHMAC, blockedDeviceHMAC, identity1)
	if currentEpoch == staleBlockedEpoch {
		t.Fatal("device fence did not advance token epoch")
	}
	cache.store(tokenHMAC, staleBlockedEpoch, subscriptionCacheKey{
		TokenHMAC: tokenHMAC, DeviceHMAC: blockedDeviceHMAC, Variant: "base",
	}, snapshot, subscriptionCacheVersion{Identity: identity1, VerifiedAt: verifiedAt}, verifiedAt, ttl)
	cache.end(tokenHMAC, blockedDeviceHMAC)
	if !loadAllowed() {
		t.Fatal("exact-identity device fence deleted an allowed entry")
	}
	if _, ok := cache.load(tokenHMAC, blockedDeviceHMAC, "base", verifiedAt, ttl, func(CustomerView) (string, bool) {
		return blockedDeviceHMAC, true
	}); ok {
		t.Fatal("stale blocked-device store resurrected after the fence")
	}

	cache.invalidateDevice(tokenHMAC, blockedDeviceHMAC, identity2)
	if loadAllowed() {
		t.Fatal("newer strong identity preserved stale allowed entries")
	}

	newerEpoch := cache.begin(tokenHMAC, allowedDeviceHMAC)
	snapshot.Customer.Generation = 2
	cache.store(tokenHMAC, newerEpoch, allowedKey, snapshot, subscriptionCacheVersion{
		Identity: identity2, VerifiedAt: verifiedAt,
	}, verifiedAt, ttl)
	cache.end(tokenHMAC, allowedDeviceHMAC)
	cache.invalidateDevice(tokenHMAC, blockedDeviceHMAC, identity1)
	if !loadAllowed() {
		t.Fatal("stale strong identity deleted newer allowed entries")
	}
}

func TestSubscriptionDeviceFenceDoesNotSuppressAuthoritativeNewerStore(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("f", 64)
	allowedDeviceHMAC := strings.Repeat("1", 64)
	blockedDeviceHMAC := strings.Repeat("2", 64)
	verifiedAt := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	ttl := time.Hour
	identity1 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	identity2 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 2, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	key := subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: allowedDeviceHMAC, Variant: "base"}

	warmEpoch := cache.begin(tokenHMAC, allowedDeviceHMAC)
	cache.store(tokenHMAC, warmEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: verifiedAt.Add(time.Hour), Generation: 1},
		Document: json.RawMessage(`{"generation":1}`),
	}, subscriptionCacheVersion{Identity: identity1, VerifiedAt: verifiedAt}, verifiedAt, ttl)
	cache.end(tokenHMAC, allowedDeviceHMAC)

	authoritativeEpoch := cache.begin(tokenHMAC, allowedDeviceHMAC)
	blockedEpoch := cache.begin(tokenHMAC, blockedDeviceHMAC)
	cache.invalidateDevice(tokenHMAC, blockedDeviceHMAC, identity1)
	cache.end(tokenHMAC, blockedDeviceHMAC)
	cache.store(tokenHMAC, authoritativeEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: verifiedAt.Add(time.Hour), Generation: 2},
		Document: json.RawMessage(`{"generation":2}`),
	}, subscriptionCacheVersion{Identity: identity2, VerifiedAt: verifiedAt.Add(time.Second)}, verifiedAt.Add(time.Second), ttl)
	cache.end(tokenHMAC, allowedDeviceHMAC)
	_ = blockedEpoch

	got, ok := cache.load(tokenHMAC, allowedDeviceHMAC, "base", verifiedAt.Add(time.Second), ttl, func(CustomerView) (string, bool) {
		return allowedDeviceHMAC, true
	})
	if !ok || got.Customer.Generation != 2 || string(got.Document) != `{"generation":2}` {
		t.Fatalf("device fence suppressed authoritative store: ok=%v generation=%d document=%s", ok, got.Customer.Generation, got.Document)
	}
}

func TestSubscriptionSameDeviceFenceAllowsOnlyAuthoritativeNewerStore(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("3", 64)
	deviceHMAC := strings.Repeat("4", 64)
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	ttl := time.Hour
	identity1 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	identity2 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 2, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	key := subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: deviceHMAC, Variant: "base"}
	warmEpoch := cache.begin(tokenHMAC, deviceHMAC)
	cache.store(tokenHMAC, warmEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: 1}, Document: json.RawMessage(`{"generation":1}`),
	}, subscriptionCacheVersion{Identity: identity1, VerifiedAt: now}, now, ttl)
	cache.end(tokenHMAC, deviceHMAC)

	authoritativeEpoch := cache.begin(tokenHMAC, deviceHMAC)
	blockedEpoch := cache.begin(tokenHMAC, deviceHMAC)
	cache.invalidateDevice(tokenHMAC, deviceHMAC, identity1)
	cache.end(tokenHMAC, deviceHMAC)
	cache.store(tokenHMAC, blockedEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: 1}, Document: json.RawMessage(`{"stale":true}`),
	}, subscriptionCacheVersion{Identity: identity1, VerifiedAt: now}, now, ttl)
	cache.store(tokenHMAC, authoritativeEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: 2}, Document: json.RawMessage(`{"generation":2}`),
	}, subscriptionCacheVersion{Identity: identity2, VerifiedAt: now.Add(time.Second)}, now.Add(time.Second), ttl)
	cache.end(tokenHMAC, deviceHMAC)

	got, ok := cache.load(tokenHMAC, deviceHMAC, "base", now.Add(time.Second), ttl, func(CustomerView) (string, bool) {
		return deviceHMAC, true
	})
	if !ok || got.Customer.Generation != 2 || string(got.Document) != `{"generation":2}` {
		t.Fatalf("same-device fence result: ok=%v generation=%d document=%s", ok, got.Customer.Generation, got.Document)
	}
}

func TestSubscriptionHardInvalidationCannotBeOverridden(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("5", 64)
	deviceHMAC := strings.Repeat("6", 64)
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	ttl := time.Hour
	identity1 := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	identity2 := subscriptionCacheIdentity{RestoreEpoch: 2, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	key := subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: deviceHMAC, Variant: "base"}
	warmEpoch := cache.begin(tokenHMAC, deviceHMAC)
	cache.store(tokenHMAC, warmEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: 1}, Document: json.RawMessage(`{"generation":1}`),
	}, subscriptionCacheVersion{Identity: identity1, VerifiedAt: now}, now, ttl)
	cache.end(tokenHMAC, deviceHMAC)

	staleEpoch := cache.begin(tokenHMAC, deviceHMAC)
	cache.invalidate(tokenHMAC, deviceHMAC)
	cache.store(tokenHMAC, staleEpoch, key, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: 1}, Document: json.RawMessage(`{"restore_epoch":2}`),
	}, subscriptionCacheVersion{Identity: identity2, VerifiedAt: now.Add(time.Second)}, now.Add(time.Second), ttl)
	cache.end(tokenHMAC, deviceHMAC)
	if _, ok := cache.load(tokenHMAC, deviceHMAC, "base", now.Add(time.Second), ttl, func(CustomerView) (string, bool) {
		return deviceHMAC, true
	}); ok {
		t.Fatal("hard invalidation was overridden by an in-flight store")
	}
}

func TestSubscriptionCacheRestoreEpochOrdering(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("7", 64)
	deviceHMAC := strings.Repeat("8", 64)
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	ttl := time.Hour
	key := subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: deviceHMAC, Variant: "base"}
	store := func(identity subscriptionCacheIdentity, generation int64, document string, at time.Time) {
		epoch := cache.begin(tokenHMAC, deviceHMAC)
		cache.store(tokenHMAC, epoch, key, SubscriptionSnapshot{
			Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: generation}, Document: json.RawMessage(document),
		}, subscriptionCacheVersion{Identity: identity, VerifiedAt: at}, at, ttl)
		cache.end(tokenHMAC, deviceHMAC)
	}
	load := func(at time.Time) (SubscriptionSnapshot, bool) {
		return cache.load(tokenHMAC, deviceHMAC, "base", at, ttl, func(CustomerView) (string, bool) { return deviceHMAC, true })
	}

	store(subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 5, TokenGeneration: 5, SettingsGeneration: 5, SchemaVersion: 8}, 5, `{"epoch":1}`, now)
	store(subscriptionCacheIdentity{RestoreEpoch: 2, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}, 1, `{"epoch":2}`, now.Add(time.Second))
	if got, ok := load(now.Add(time.Second)); !ok || string(got.Document) != `{"epoch":2}` {
		t.Fatalf("higher restore epoch did not replace rollback cache: ok=%v document=%s", ok, got.Document)
	}
	store(subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 9, TokenGeneration: 9, SettingsGeneration: 9, SchemaVersion: 9}, 9, `{"stale":true}`, now.Add(2*time.Second))
	if got, ok := load(now.Add(2 * time.Second)); !ok || string(got.Document) != `{"epoch":2}` {
		t.Fatalf("lower restore epoch replaced newer cache: ok=%v document=%s", ok, got.Document)
	}
	store(subscriptionCacheIdentity{RestoreEpoch: 2, CustomerGeneration: 2, TokenGeneration: 1, SettingsGeneration: 0, SchemaVersion: 8}, 2, `{"incomparable":true}`, now.Add(3*time.Second))
	if _, ok := load(now.Add(3 * time.Second)); ok {
		t.Fatal("same-epoch incomparable identity did not fail closed")
	}
}

func TestSubscriptionDeviceFenceStateIsReleased(t *testing.T) {
	cache := newSubscriptionCache()
	tokenHMAC := strings.Repeat("9", 64)
	identity := subscriptionCacheIdentity{RestoreEpoch: 1, CustomerGeneration: 1, TokenGeneration: 1, SettingsGeneration: 1, SchemaVersion: 8}
	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	epoch := cache.begin(tokenHMAC, "cached-device")
	cache.store(tokenHMAC, epoch, subscriptionCacheKey{TokenHMAC: tokenHMAC, DeviceHMAC: "cached-device", Variant: "base"}, SubscriptionSnapshot{
		Customer: CustomerView{Active: true, Expires: now.Add(time.Hour), Generation: 1}, Document: json.RawMessage(`{"cached":true}`),
	}, subscriptionCacheVersion{Identity: identity, VerifiedAt: now}, now, time.Hour)
	cache.end(tokenHMAC, "cached-device")
	for index := 0; index < 100; index++ {
		device := fmt.Sprintf("attacker-%d", index)
		cache.begin(tokenHMAC, device)
		cache.end(tokenHMAC, device)
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if state := cache.epochs[tokenHMAC]; state == nil || len(state.devices) != 0 {
		t.Fatalf("released device fences retained: state=%#v", state)
	}
}
