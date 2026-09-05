package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type usageHandler struct {
	*fakeHandler
	onRead func()
}

func (handler *usageHandler) ManagedUserCounters(_ context.Context, users []string) (map[string][2]uint64, error) {
	if !reflect.DeepEqual(users, []string{"wl:one:exit-s1", "wl:unused:exit-s1"}) {
		return nil, errors.New("counter request escaped current managed set")
	}
	if handler.onRead != nil {
		handler.onRead()
	}
	return map[string][2]uint64{"wl:one:exit-s1": {799, 3564}}, nil
}

type usagePreflight struct {
	fakeReadinessPreflight
	bindingError error
	bindingCalls int
}

func (preflight *usagePreflight) ValidateRuntimeBinding(releaseID, configDigest string) error {
	preflight.bindingCalls++
	if releaseID != "release-12" || configDigest != strings.Repeat("a", 64) {
		return errors.New("unexpected runtime binding")
	}
	return preflight.bindingError
}

func TestUsageSnapshotBindsCurrentGenerationWithoutChangingState(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	bootID := "boot-usage"
	handler := &usageHandler{fakeHandler: newFakeHandler("canary:fixed", "ordinary:fixed")}
	preflight := &usagePreflight{}
	reconciler, store := testReconcilerWithPreflight(t, handler, &now, &bootID, preflight)
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:one:exit-s1", "wl:unused:exit-s1")
	receipt, err := reconciler.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	operations := append([]string(nil), handler.operations...)
	fullPreflightCalls := preflight.calls
	got, err := reconciler.Usage(context.Background(), desired.ActionKey())
	if err != nil {
		t.Fatal(err)
	}
	if got.Receipt != receipt || !got.SampledAt.Equal(now) ||
		!reflect.DeepEqual(got.Users, []UserUsage{{Email: "wl:one:exit-s1", UplinkBytes: 799, DownlinkBytes: 3564}}) ||
		!reflect.DeepEqual(got.UnavailableUsers, []string{"wl:unused:exit-s1"}) {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
	if preflight.bindingCalls != 2 || preflight.calls != fullPreflightCalls {
		t.Fatal("usage did not check both bindings or invoked relay probes")
	}
	if _, err := reconciler.Usage(context.Background(), "node-s1:999:"+strings.Repeat("b", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-current generation accepted: %v", err)
	}
	for _, drift := range []string{"boot", "config"} {
		t.Run(drift, func(t *testing.T) {
			bootID = "boot-usage"
			preflight.bindingError = nil
			handler.onRead = func() {
				if drift == "boot" {
					bootID = "boot-restarted"
				} else {
					preflight.bindingError = errors.New("config changed during stats read")
				}
			}
			if _, err := reconciler.Usage(context.Background(), desired.ActionKey()); err == nil {
				t.Fatal("usage returned after runtime drift")
			}
		})
	}
	storedDesired, err := store.LoadDesired()
	if err != nil || storedDesired.DesiredSHA256() != desired.DesiredSHA256() {
		t.Fatalf("usage changed desired: %v", err)
	}
	storedReceipt, err := store.LoadReceipt(desired.ActionKey())
	if err != nil || storedReceipt != receipt || !reflect.DeepEqual(handler.operations, operations) {
		t.Fatalf("usage changed receipt or Xray users: %v", err)
	}
}

func TestUsageAcceptsCurrentActionKeysLongerThanIdentifiers(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	bootID := "boot-long-key"
	handler := &usageHandler{fakeHandler: newFakeHandler("canary:fixed", "ordinary:fixed")}
	reconciler, _ := testReconcilerWithPreflight(t, handler, &now, &bootID, &usagePreflight{})
	desired := testDesired(t, 1, "release-12", strings.Repeat("a", 64), "wl:one:exit-s1", "wl:unused:exit-s1")
	desired.NodeID = strings.Repeat("n", 190)
	raw, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}
	desired, err = ParseDesired(raw)
	if err != nil || len(desired.ActionKey()) != 257 {
		t.Fatalf("invalid long-action fixture: %v", err)
	}
	receipt, err := reconciler.Apply(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reconciler.LookupReceipt(context.Background(), desired.ActionKey()); err != nil || got != receipt {
		t.Fatalf("existing receipt lookup rejected valid action: %v", err)
	}
	got, err := reconciler.Usage(context.Background(), desired.ActionKey())
	if err != nil || got.Receipt != receipt {
		t.Fatalf("usage rejected current valid long action: %v", err)
	}
}
