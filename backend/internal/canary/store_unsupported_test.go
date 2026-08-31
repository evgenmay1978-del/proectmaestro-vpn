//go:build !linux

package canary

import (
	"context"
	"testing"
)

func TestStoreUnsupportedPlatform(t *testing.T) {
	store, err := NewStore()
	if err == nil || err.Error() != "unsupported_platform" || store != nil {
		t.Fatalf("NewStore() = (%v, %v), want (nil, unsupported_platform)", store, err)
	}

	zero := new(Store)
	if _, err := zero.Prepare(context.Background(), Snapshot{}, nil, Artifacts{}, nil); err == nil || err.Error() != "unsupported_platform" {
		t.Fatalf("Prepare error = %v, want unsupported_platform", err)
	}
	if err := zero.Activate(context.Background(), "r-test", nil); err == nil || err.Error() != "unsupported_platform" {
		t.Fatalf("Activate error = %v, want unsupported_platform", err)
	}
	if err := zero.RollbackToAbsence(context.Background(), "r-test", nil, nil); err == nil || err.Error() != "unsupported_platform" {
		t.Fatalf("RollbackToAbsence error = %v, want unsupported_platform", err)
	}
}
