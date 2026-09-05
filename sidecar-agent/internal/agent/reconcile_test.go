package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeHandler struct {
	users      map[string]struct{}
	operations []string
	failAdd    string
	failRemove string
	accountOK  map[string]bool
}

func newFakeHandler(users ...string) *fakeHandler {
	result := &fakeHandler{
		users:     make(map[string]struct{}, len(users)),
		accountOK: make(map[string]bool, len(users)),
	}
	for _, user := range users {
		result.users[user] = struct{}{}
		result.accountOK[user] = true
	}
	return result
}

func (handler *fakeHandler) ListUsers(context.Context, string) ([]string, error) {
	result := make([]string, 0, len(handler.users))
	for user := range handler.users {
		result = append(result, user)
	}
	return result, nil
}

func (handler *fakeHandler) AddUser(_ context.Context, _ string, email string) error {
	handler.operations = append(handler.operations, "add:"+email)
	if email == handler.failAdd {
		return errors.New("synthetic add failure")
	}
	handler.users[email] = struct{}{}
	handler.accountOK[email] = true
	return nil
}

func (handler *fakeHandler) RemoveUser(_ context.Context, _ string, email string) error {
	handler.operations = append(handler.operations, "remove:"+email)
	if email == handler.failRemove {
		return errors.New("synthetic remove failure")
	}
	delete(handler.users, email)
	delete(handler.accountOK, email)
	return nil
}

func (handler *fakeHandler) ManagedUserAccountMatches(_ context.Context, _ string, email string) (bool, error) {
	return handler.accountOK[email], nil
}

type fakeReadinessPreflight struct {
	err    error
	failAt int
	calls  int
}

func (preflight *fakeReadinessPreflight) Validate(_ context.Context, _, _, _, _ string) error {
	preflight.calls++
	if preflight.failAt == preflight.calls {
		return errors.New("synthetic relay preflight transition")
	}
	return preflight.err
}

func testDesired(t *testing.T, generation int64, releaseID, configDigest string, users ...string) Desired {
	t.Helper()
	managedDigest, err := ManagedUserSetDigest(users)
	if err != nil {
		t.Fatalf("ManagedUserSetDigest: %v", err)
	}
	raw, err := json.Marshal(struct {
		Version              int      `json:"version"`
		OriginID             string   `json:"origin_id"`
		NodeID               string   `json:"node_id"`
		ReleaseID            string   `json:"release_id"`
		ProfileID            string   `json:"profile_id"`
		PresetID             string   `json:"preset_id"`
		ExitID               string   `json:"exit_id"`
		Generation           int64    `json:"generation"`
		ConfigDigest         string   `json:"config_digest"`
		ManagedUserSetDigest string   `json:"managed_user_set_digest"`
		StaticUsers          []string `json:"static_users"`
		ManagedUsers         []string `json:"managed_users"`
	}{
		Version: 1, OriginID: "origin-s1", NodeID: "node-s1", ReleaseID: releaseID,
		ProfileID: "profile-xhttp", PresetID: "preset-packet-up", ExitID: "exit-s1",
		Generation: generation, ConfigDigest: configDigest, ManagedUserSetDigest: managedDigest,
		StaticUsers: []string{"canary:fixed", "ordinary:fixed"}, ManagedUsers: users,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	desired, err := ParseDesired(raw)
	if err != nil {
		t.Fatalf("ParseDesired: %v", err)
	}
	return desired
}

func testReconciler(t *testing.T, handler Handler, clock *time.Time, bootID *string) (*Reconciler, *FileStore) {
	return testReconcilerWithPreflight(t, handler, clock, bootID, &fakeReadinessPreflight{})
}

func TestCommercialLeaseModeRequiresExplicitRuntimeCapability(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	boot := "legacy-upstream-boot"
	r, store := testReconciler(t, newFakeHandler("ordinary:fixed", "canary:fixed"), &now, &boot)
	if _, err := r.LeaseReceipts(context.Background()); !errors.Is(err, ErrLeaseUnavailable) {
		t.Fatal("legacy runtime exposed lease capability")
	}
	if _, err := NewReconciler(ReconcilerConfig{Handler: newFakeHandler(), Store: store, InboundTag: DefaultInboundTag, ReleaseID: "release-12", ConfigDigest: strings.Repeat("a", 64),
		ProcessBootID: func() (string, error) { return boot, nil }, Preflight: &fakeReadinessPreflight{}, ManagedLeaseEnabled: true}); !errors.Is(err, ErrLeaseUnavailable) {
		t.Fatal("commercial mode accepted upstream without runtime fence capability")
	}
}

func testReconcilerWithPreflight(t *testing.T, handler Handler, clock *time.Time, bootID *string, preflight ReadinessPreflight) (*Reconciler, *FileStore) {
	t.Helper()
	store, err := NewFileStore(t.TempDir(), 4)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	reconciler, err := NewReconciler(ReconcilerConfig{
		Handler: handler, Store: store, InboundTag: "maestro-cdn-in",
		ReleaseID: "release-12", ConfigDigest: strings.Repeat("a", 64),
		Preflight:     preflight,
		ProcessBootID: func() (string, error) { return *bootID, nil },
		Now:           func() time.Time { return *clock }, ReceiptTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	return reconciler, store
}

func TestReconcileReplacesWrongManagedVLESSAccountBeforeReadiness(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	bootID := "boot-account"
	email := "wl:account:exit-s1"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed", email)
	handler.accountOK[email] = false
	reconciler, store := testReconciler(t, handler, &now, &bootID)
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), email)

	receipt, err := reconciler.Apply(context.Background(), desired)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{"remove:" + email, "add:" + email}; !reflect.DeepEqual(handler.operations, want) {
		t.Fatalf("wrong-account replacement operations = %#v, want %#v", handler.operations, want)
	}
	if !handler.accountOK[email] || receipt.ActionKey != desired.ActionKey() {
		t.Fatalf("readiness emitted without exact managed account: receipt=%#v account_ok=%v", receipt, handler.accountOK[email])
	}
	if _, err := store.LoadReceipt(desired.ActionKey()); err != nil {
		t.Fatalf("LoadReceipt: %v", err)
	}
}

func TestReconcileRequiresCurrentRelayPreflightBeforeMutationOrReceipt(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	bootID := "boot-preflight"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed")
	preflight := &fakeReadinessPreflight{err: errors.New("synthetic stale relay preflight")}
	reconciler, store := testReconcilerWithPreflight(t, handler, &now, &bootID, preflight)
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:new:exit-s1")

	if _, err := reconciler.Apply(context.Background(), desired); err == nil {
		t.Fatal("desired accepted without current relay preflight")
	}
	if preflight.calls != 1 || len(handler.operations) != 0 {
		t.Fatalf("preflight calls=%d operations=%#v", preflight.calls, handler.operations)
	}
	if _, err := store.LoadReceipt(desired.ActionKey()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt persisted after preflight failure: %v", err)
	}
}

func TestReconcileRechecksRelayPreflightAfterConvergenceBeforeReceipt(t *testing.T) {
	now := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	bootID := "boot-preflight-transition"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed")
	preflight := &fakeReadinessPreflight{failAt: 2}
	reconciler, store := testReconcilerWithPreflight(t, handler, &now, &bootID, preflight)
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:new:exit-s1")

	if _, err := reconciler.Apply(context.Background(), desired); err == nil {
		t.Fatal("receipt emitted after relay preflight changed during convergence")
	}
	if preflight.calls != 2 || !reflect.DeepEqual(handler.operations, []string{"add:wl:new:exit-s1"}) {
		t.Fatalf("preflight calls=%d operations=%#v", preflight.calls, handler.operations)
	}
	if _, err := store.LoadReceipt(desired.ActionKey()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt persisted after final preflight failure: %v", err)
	}
}

func TestReconcileConvergesExactManagedSetAddsBeforeRemovalsAndPreservesStaticUsers(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	bootID := "boot-a"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed", "wl:old:exit-s1", "wl:keep:exit-s1")
	reconciler, _ := testReconciler(t, handler, &now, &bootID)
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:add:exit-s1", "wl:keep:exit-s1")

	receipt, err := reconciler.Apply(context.Background(), desired)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	wantOperations := []string{"add:wl:add:exit-s1", "remove:wl:old:exit-s1"}
	if !reflect.DeepEqual(handler.operations, wantOperations) {
		t.Fatalf("operations = %#v, want %#v", handler.operations, wantOperations)
	}
	for _, preserved := range []string{"ordinary:fixed", "canary:fixed"} {
		if _, ok := handler.users[preserved]; !ok {
			t.Fatalf("non-managed user %q was changed", preserved)
		}
	}
	if receipt.ActionKey != desired.ActionKey() || receipt.XrayProcessBootID != bootID || !receipt.ReadyAt(now) {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestReconcileDuplicateStaleAndPartialFailureAreFailClosed(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	bootID := "boot-a"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed", "wl:old:exit-s1")
	reconciler, store := testReconciler(t, handler, &now, &bootID)
	desired := testDesired(t, 2, "release-12", strings.Repeat("a", 64), "wl:new:exit-s1")
	if _, err := reconciler.Apply(context.Background(), desired); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	handler.operations = nil
	now = now.Add(10 * time.Second)
	second, err := reconciler.Apply(context.Background(), desired)
	if err != nil || len(handler.operations) != 0 || !second.ReadyAt(now) {
		t.Fatalf("duplicate Apply receipt=%#v operations=%#v err=%v", second, handler.operations, err)
	}
	stale := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:stale:exit-s1")
	if _, err := reconciler.Apply(context.Background(), stale); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale Apply error = %v", err)
	}

	failing := newFakeHandler("ordinary:fixed", "canary:fixed", "wl:remove-only-after-add:exit-s1")
	failing.failAdd = "wl:missing:exit-s1"
	reconciler, store = testReconciler(t, failing, &now, &bootID)
	partial := testDesired(t, 3, "release-12", strings.Repeat("a", 64), "wl:missing:exit-s1")
	if _, err := reconciler.Apply(context.Background(), partial); err == nil {
		t.Fatal("partial HandlerService failure accepted")
	}
	if !reflect.DeepEqual(failing.operations, []string{"add:wl:missing:exit-s1"}) {
		t.Fatalf("removal ran after failed add: %#v", failing.operations)
	}
	if _, err := store.LoadReceipt(partial.ActionKey()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt persisted after partial failure: %v", err)
	}
}

func TestReconcileRejectsReleaseAndConfigMismatchWithoutHandlerMutation(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	bootID := "boot-a"
	for name, desired := range map[string]Desired{
		"release": testDesired(t, 1, "release-other", strings.Repeat("a", 64), "wl:new:exit-s1"),
		"config":  testDesired(t, 1, "release-12", strings.Repeat("b", 64), "wl:new:exit-s1"),
	} {
		t.Run(name, func(t *testing.T) {
			handler := newFakeHandler("ordinary:fixed")
			reconciler, _ := testReconciler(t, handler, &now, &bootID)
			if _, err := reconciler.Apply(context.Background(), desired); err == nil {
				t.Fatal("mismatched desired accepted")
			}
			if len(handler.operations) != 0 {
				t.Fatalf("handler mutated: %#v", handler.operations)
			}
		})
	}
}

func TestParseDesiredAcceptsTask11HexDigestCase(t *testing.T) {
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:new:exit-s1")
	raw := bytes.Replace(
		desired.CanonicalJSON(),
		[]byte(`"config_digest":"`+strings.Repeat("a", 64)+`"`),
		[]byte(`"config_digest":"`+strings.Repeat("A", 64)+`"`),
		1,
	)
	if _, err := ParseDesired(raw); err != nil {
		t.Fatalf("Task 11-compatible uppercase digest rejected: %v", err)
	}
}

func TestReconcileRejectsDesiredMutatedAfterCanonicalParsing(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	bootID := "boot-a"
	handler := newFakeHandler("ordinary:fixed", "canary:fixed")
	reconciler, _ := testReconciler(t, handler, &now, &bootID)
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:new:exit-s1")
	desired.ProfileID = "profile-tampered"
	if _, err := reconciler.Apply(context.Background(), desired); !errors.Is(err, ErrInvalidDesired) {
		t.Fatalf("mutated desired error = %v", err)
	}
	if len(handler.operations) != 0 {
		t.Fatalf("mutated desired reached HandlerService: %#v", handler.operations)
	}
}
