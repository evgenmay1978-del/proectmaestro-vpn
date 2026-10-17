package main

import (
	"context"
	"errors"
	"testing"
)

func TestBuildRuntimeDefaultsToLegacyWithoutConstructingRQLite(t *testing.T) {
	legacyRuntime := &panelRuntime{}
	legacyCalls := 0
	rqliteCalls := 0

	got, err := buildRuntime(context.Background(), "", runtimeFactories{
		legacy: func(context.Context) (*panelRuntime, error) {
			legacyCalls++
			return legacyRuntime, nil
		},
		rqlite: func(context.Context) (*panelRuntime, error) {
			rqliteCalls++
			return &panelRuntime{}, nil
		},
	})
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	if got != legacyRuntime {
		t.Fatal("buildRuntime did not return the legacy runtime")
	}
	if legacyCalls != 1 || rqliteCalls != 0 {
		t.Fatalf("factory calls legacy=%d rqlite=%d, want 1/0", legacyCalls, rqliteCalls)
	}
}

func TestBuildRuntimeConstructsOnlyRQLite(t *testing.T) {
	rqliteRuntime := &panelRuntime{}
	legacyCalls := 0
	rqliteCalls := 0

	got, err := buildRuntime(context.Background(), "rqlite", runtimeFactories{
		legacy: func(context.Context) (*panelRuntime, error) {
			legacyCalls++
			return &panelRuntime{}, nil
		},
		rqlite: func(context.Context) (*panelRuntime, error) {
			rqliteCalls++
			return rqliteRuntime, nil
		},
	})
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	if got != rqliteRuntime {
		t.Fatal("buildRuntime did not return the rqlite runtime")
	}
	if legacyCalls != 0 || rqliteCalls != 1 {
		t.Fatalf("factory calls legacy=%d rqlite=%d, want 0/1", legacyCalls, rqliteCalls)
	}
}

func TestBuildRuntimeRejectsUnknownModeBeforeConstruction(t *testing.T) {
	calls := 0
	factory := func(context.Context) (*panelRuntime, error) {
		calls++
		return &panelRuntime{}, nil
	}

	if _, err := buildRuntime(context.Background(), "dual", runtimeFactories{
		legacy: factory,
		rqlite: factory,
	}); err == nil {
		t.Fatal("buildRuntime accepted an unknown control-plane mode")
	}
	if calls != 0 {
		t.Fatalf("unknown mode constructed %d runtime(s), want 0", calls)
	}
}

func TestBuildRuntimeDoesNotFallbackAfterRQLiteFailure(t *testing.T) {
	wantErr := errors.New("rqlite unavailable")
	legacyCalls := 0

	got, err := buildRuntime(context.Background(), "rqlite", runtimeFactories{
		legacy: func(context.Context) (*panelRuntime, error) {
			legacyCalls++
			return &panelRuntime{}, nil
		},
		rqlite: func(context.Context) (*panelRuntime, error) {
			return nil, wantErr
		},
	})
	if got != nil {
		t.Fatalf("buildRuntime returned a runtime after rqlite failure: %#v", got)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("buildRuntime error=%v, want %v", err, wantErr)
	}
	if legacyCalls != 0 {
		t.Fatalf("rqlite failure opened legacy %d time(s), want 0", legacyCalls)
	}
}
