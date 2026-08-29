package controlplane

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLegacyPublicIdempotencyKeyUsesClusterStableSecret(t *testing.T) {
	first := legacyPublicIdempotencyTestService(t, 0x62)
	second := legacyPublicIdempotencyTestService(t, 0x62)
	otherSecret := legacyPublicIdempotencyTestService(t, 0x63)

	base := mustLegacyPublicIdempotencyKey(t, first, "/trial", "trial-alice", "anchor-1", "device-1")
	if sameCluster := mustLegacyPublicIdempotencyKey(t, second, "/trial", "trial-alice", "anchor-1", "device-1"); sameCluster != base {
		t.Fatalf("same cluster secret derived %q, want %q", sameCluster, base)
	}
	if !strings.HasPrefix(base, "legacy-public-v1:") || len(base) != len("legacy-public-v1:")+64 {
		t.Fatalf("derived key=%q, want versioned HMAC identifier", base)
	}
	for _, raw := range []string{"trial-alice", "anchor-1", "device-1"} {
		if strings.Contains(base, raw) {
			t.Fatalf("derived key %q leaks raw identity %q", base, raw)
		}
	}

	mutations := map[string]string{
		"route":         mustLegacyPublicIdempotencyKey(t, first, "/claim", "trial-alice", "anchor-1", "device-1"),
		"identity":      mustLegacyPublicIdempotencyKey(t, first, "/trial", "trial-bob", "anchor-1", "device-1"),
		"secret":        mustLegacyPublicIdempotencyKey(t, otherSecret, "/trial", "trial-alice", "anchor-1", "device-1"),
		"value framing": mustLegacyPublicIdempotencyKey(t, first, "/trial", "trial-alice", "anchor-1device-1"),
	}
	seen := map[string]string{base: "base"}
	for name, key := range mutations {
		if prior, duplicate := seen[key]; duplicate {
			t.Fatalf("%s key collided with %s: %q", name, prior, key)
		}
		seen[key] = name
	}
}

func TestLegacyPublicIdempotencyKeyFailsClosedWithoutService(t *testing.T) {
	var service *Service
	if key, err := service.LegacyPublicIdempotencyKey("/claim", "alice", "device-1"); key != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil service key=%q err=%v, want empty ErrUnavailable", key, err)
	}
}

func legacyPublicIdempotencyTestService(t *testing.T, hmacByte byte) *Service {
	t.Helper()
	box, err := NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x61}, 32)}, bytes.Repeat([]byte{hmacByte}, 32))
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	clock := fixedClock{value: time.Unix(2_000_000, 0)}
	store, err := NewStore(&recordingRQLite{}, box, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := NewService(store, &sequenceIDs{}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func mustLegacyPublicIdempotencyKey(t *testing.T, service *Service, route string, values ...string) string {
	t.Helper()
	key, err := service.LegacyPublicIdempotencyKey(route, values...)
	if err != nil {
		t.Fatalf("LegacyPublicIdempotencyKey(%q): %v", route, err)
	}
	return key
}
