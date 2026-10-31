package aivpn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// testKeyPSK builds a valid key whose PSK is 32 copies of b, so two devices can be
// told apart by the credential itself and not only by the client name.
func testKeyPSK(t *testing.T, b byte) string {
	t.Helper()
	key, err := Build(Key{
		Server:    testServer,
		ServerKey: b64(32, 1),
		PSK:       b64(32, b),
		ClientIP:  testClientIP,
		Net: &NetConfig{
			ClientIP:      testClientIP,
			ServerVPNIP:   testServerIP,
			PrefixLen:     24,
			MTU:           1280,
			MDHLen:        20,
			KeepaliveSecs: 8,
		},
	})
	if err != nil {
		t.Fatalf("build test key: %v", err)
	}
	return key
}

// TestClientNameIsStableAndBounded: the per-device client name is login + -- +
// sha256(device)[:10], deterministic across calls and distinct per device, and it
// never exceeds the management API bound even for a maximal login.
func TestClientNameIsStableAndBounded(t *testing.T) {
	const dev = "0f8fad5bd9cb469fa16570867728950e"
	name := clientName("alice", dev)
	sum := sha256.Sum256([]byte(dev))
	want := "alice--" + hex.EncodeToString(sum[:])[:10]
	if name != want {
		t.Fatalf("clientName = %q, want %q", name, want)
	}
	if name != clientName("alice", dev) {
		t.Fatal("clientName is not deterministic")
	}
	if len(name) > maxClientName {
		t.Fatalf("name %q exceeds %d chars", name, maxClientName)
	}
	if name == clientName("alice", "another-device") {
		t.Fatal("two devices got one client name")
	}
	if name == clientName("bob", dev) {
		t.Fatal("two logins got one client name")
	}

	// A 64-char login must lose its tail, never the digest.
	long := strings.Repeat("a", 64)
	longName := clientName(long, dev)
	if len(longName) > maxClientName {
		t.Fatalf("long-login name %q exceeds %d chars", longName, maxClientName)
	}
	if !strings.HasSuffix(longName, want[len(want)-(2+10):]) {
		t.Fatalf("truncation dropped the device digest: %q", longName)
	}
	if longName == clientName(long, "another-device") {
		t.Fatal("truncated names collide across devices")
	}
}

// TestServerKeyForDeviceIsolatesClients: two devices get two AIVPN clients and two
// keys via the management API, a repeated device is served from the cache (no
// second POST), the empty-device path still uses the login-named legacy client,
// and Invalidate(login) drops every device of that login only.
func TestServerKeyForDeviceIsolatesClients(t *testing.T) {
	const devA, devB = "dev-aaaaaaaaaaaaaaaa", "dev-bbbbbbbbbbbbbbbb"
	keyA, keyB, legacy := testKeyPSK(t, 0x11), testKeyPSK(t, 0x22), testKeyPSK(t, 0x33)
	nameA, nameB := clientName("alice", devA), clientName("alice", devB)
	stub := &stubRest{key: legacy, keyByName: map[string]string{nameA: keyA, nameB: keyB}}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	ctx := context.Background()

	gotA, err := s.KeyForDevice(ctx, "alice", devA)
	if err != nil || gotA != keyA {
		t.Fatalf("KeyForDevice(devA) = %q, %v; want keyA", gotA, err)
	}
	gotB, err := s.KeyForDevice(ctx, "alice", devB)
	if err != nil || gotB != keyB {
		t.Fatalf("KeyForDevice(devB) = %q, %v; want keyB", gotB, err)
	}
	if gotA == gotB {
		t.Fatal("two devices share one credential")
	}
	if names := stub.createdNames(); len(names) != 2 || names[0] != nameA || names[1] != nameB {
		t.Fatalf("created clients = %v, want [%q %q]", names, nameA, nameB)
	}
	if _, post, key := stub.hits(); post != 2 || key != 2 {
		t.Fatalf("two devices: post=%d key=%d, want 2/2", post, key)
	}

	// Same device again: cache hit, no new management round trip.
	if again, err := s.KeyForDevice(ctx, "alice", devA); err != nil || again != keyA {
		t.Fatalf("cached KeyForDevice = %q, %v", again, err)
	}
	if _, post, key := stub.hits(); post != 2 || key != 2 {
		t.Fatalf("after cached repeat: post=%d key=%d, want 2/2", post, key)
	}

	// Legacy path: the client is named after the login, with its own cache slot.
	if got, err := s.Key(ctx, "alice"); err != nil || got != legacy {
		t.Fatalf("legacy Key = %q, %v; want the legacy key", got, err)
	}
	if names := stub.createdNames(); len(names) != 3 || names[2] != "alice" {
		t.Fatalf("created clients = %v, want a third legacy client named alice", names)
	}

	// A second login for the Invalidate scope check.
	if _, err := s.Key(ctx, "bob"); err != nil {
		t.Fatalf("Key(bob): %v", err)
	}

	// Invalidate drops every device of alice: the next call refetches, but finds
	// the existing client (no new POST) and leaves bob's cached key alone.
	s.Invalidate("alice")
	if _, err := s.KeyForDevice(ctx, "alice", devA); err != nil {
		t.Fatalf("KeyForDevice after Invalidate: %v", err)
	}
	if _, err := s.Key(ctx, "bob"); err != nil {
		t.Fatalf("Key(bob) after Invalidate(alice): %v", err)
	}
	if _, post, key := stub.hits(); post != 4 || key != 5 {
		t.Fatalf("after invalidate: post=%d key=%d, want 4/5 (bob must stay cached)", post, key)
	}
}

// TestServerKeyForDeviceEmptyIsLegacy: an empty device is exactly the pre-device
// behaviour — the login-named client and its cache slot.
func TestServerKeyForDeviceEmptyIsLegacy(t *testing.T) {
	key := testKeyPSK(t, 0x33)
	stub := &stubRest{key: key}
	s := newTestServer(t, restConfig(startStub(t, stub)))
	ctx := context.Background()

	if got, err := s.KeyForDevice(ctx, "alice", ""); err != nil || got != key {
		t.Fatalf("KeyForDevice(empty) = %q, %v", got, err)
	}
	if got, err := s.Key(ctx, "alice"); err != nil || got != key {
		t.Fatalf("Key = %q, %v", got, err)
	}
	// Empty device and Key share one client and one cache entry.
	if names := stub.createdNames(); len(names) != 1 || names[0] != "alice" {
		t.Fatalf("created clients = %v, want one client named alice", names)
	}
	if _, post, keyHits := stub.hits(); post != 1 || keyHits != 1 {
		t.Fatalf("empty device: post=%d key=%d, want 1/1", post, keyHits)
	}

	// The whitelist gate holds for the device path too, and creates nothing.
	if _, err := s.KeyForDevice(ctx, "mallory", "dev-1"); !errors.Is(err, ErrNoClient) {
		t.Fatalf("non-whitelisted login err = %v, want ErrNoClient", err)
	}
	if names := stub.createdNames(); len(names) != 1 {
		t.Fatalf("a non-whitelisted login created a client: %v", names)
	}
}
