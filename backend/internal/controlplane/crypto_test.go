package controlplane

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestSecretBoxRoundTripScopeBindingAndRedactedErrors(t *testing.T) {
	encryptionKey := bytes.Repeat([]byte{0x11}, 32)
	hmacKey := bytes.Repeat([]byte{0x22}, 32)
	box, err := NewSecretBox(7, map[int][]byte{7: encryptionKey}, hmacKey)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	scope := SecretScope{
		OwnerType: "customer",
		OwnerID:   "customer-a",
		Field:     "credential",
		Kind:      "vless",
	}
	plaintext := []byte("super-secret-material")
	first, err := box.Seal(scope, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := box.Seal(scope, plaintext)
	if err != nil {
		t.Fatalf("Seal second: %v", err)
	}
	if first.KeyVersion != 7 || second.KeyVersion != 7 {
		t.Fatalf("new envelopes use versions %d and %d, want 7", first.KeyVersion, second.KeyVersion)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("two seals reused randomized AES-GCM material")
	}
	got, err := box.Open(scope, first)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open = %q, want original plaintext", got)
	}

	movedScopes := []SecretScope{
		{OwnerType: "principal", OwnerID: scope.OwnerID, Field: scope.Field, Kind: scope.Kind},
		{OwnerType: scope.OwnerType, OwnerID: "customer-b", Field: scope.Field, Kind: scope.Kind},
		{OwnerType: scope.OwnerType, OwnerID: scope.OwnerID, Field: "other-field", Kind: scope.Kind},
		{OwnerType: scope.OwnerType, OwnerID: scope.OwnerID, Field: scope.Field, Kind: "hysteria2"},
	}
	for _, moved := range movedScopes {
		if _, err := box.Open(moved, first); err == nil {
			t.Fatalf("moving envelope to scope %#v authenticated", moved)
		}
	}
	wrongBox, err := NewSecretBox(7, map[int][]byte{7: bytes.Repeat([]byte{0x33}, 32)}, hmacKey)
	if err != nil {
		t.Fatalf("NewSecretBox wrong key: %v", err)
	}
	_, openErr := wrongBox.Open(scope, first)
	if openErr == nil {
		t.Fatal("wrong encryption key authenticated")
	}
	rendered := openErr.Error()
	for _, forbidden := range []string{
		string(plaintext),
		hex.EncodeToString(first.Nonce),
		hex.EncodeToString(first.Ciphertext),
	} {
		if forbidden != "" && strings.Contains(rendered, forbidden) {
			t.Fatalf("error leaked secret material: %q", rendered)
		}
	}
}

func TestSecretBoxRotationAndReadiness(t *testing.T) {
	key1 := bytes.Repeat([]byte{0x41}, 32)
	key2 := bytes.Repeat([]byte{0x42}, 32)
	hmacKey := bytes.Repeat([]byte{0x43}, 32)
	scope := SecretScope{OwnerType: "customer", OwnerID: "c", Field: "token", Kind: "subscription"}

	oldBox, err := NewSecretBox(1, map[int][]byte{1: key1}, hmacKey)
	if err != nil {
		t.Fatalf("old NewSecretBox: %v", err)
	}
	oldEnvelope, err := oldBox.Seal(scope, []byte("old"))
	if err != nil {
		t.Fatalf("old Seal: %v", err)
	}

	rotated, err := NewSecretBox(2, map[int][]byte{1: key1, 2: key2}, hmacKey)
	if err != nil {
		t.Fatalf("rotated NewSecretBox: %v", err)
	}
	if err := rotated.ReadyForVersions(1, 2); err != nil {
		t.Fatalf("ReadyForVersions: %v", err)
	}
	if got, err := rotated.Open(scope, oldEnvelope); err != nil || string(got) != "old" {
		t.Fatalf("rotated Open old = %q, %v", got, err)
	}
	newEnvelope, err := rotated.Seal(scope, []byte("new"))
	if err != nil {
		t.Fatalf("rotated Seal: %v", err)
	}
	if newEnvelope.KeyVersion != 2 {
		t.Fatalf("new envelope version = %d, want 2", newEnvelope.KeyVersion)
	}

	missing, err := NewSecretBox(2, map[int][]byte{2: key2}, hmacKey)
	if err != nil {
		t.Fatalf("missing NewSecretBox: %v", err)
	}
	if err := missing.ReadyForVersions(1, 2); err == nil {
		t.Fatal("readiness accepted a referenced missing key version")
	}
	if _, err := missing.Open(scope, oldEnvelope); err == nil {
		t.Fatal("Open accepted an envelope with a missing key version")
	}
	if _, err := NewSecretBox(1, map[int][]byte{1: key1}, key1); err == nil {
		t.Fatal("constructor accepted identical encryption and HMAC keys")
	}
}

func TestLookupHMACAndCanonicalLoginKey(t *testing.T) {
	box, err := NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{0x51}, 32)},
		bytes.Repeat([]byte{0x52}, 32),
	)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	first := box.LookupHMAC("login", []byte("WapMix"))
	second := box.LookupHMAC("login", []byte("WapMix"))
	otherKind := box.LookupHMAC("telegram-user", []byte("WapMix"))
	if len(first) != 64 || first != second || first == otherKind {
		t.Fatalf("HMAC contract violated: len=%d deterministic=%v separated=%v", len(first), first == second, first != otherKind)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("HMAC is not lowercase hex: %q", first)
	}
	if got, err := CanonicalLoginKey("  WapMix  "); err != nil || got != "wapmix" {
		t.Fatalf("CanonicalLoginKey = %q, %v", got, err)
	}
	for _, invalid := range []string{"", "   ", "bad\nlogin", strings.Repeat("a", 129)} {
		if _, err := CanonicalLoginKey(invalid); err == nil {
			t.Fatalf("CanonicalLoginKey accepted %q", invalid)
		}
	}
}

func TestSecretBoxRebindPreservesAuthenticatedVersionAndChangesOnlyScope(t *testing.T) {
	keys := map[int][]byte{6: bytes.Repeat([]byte{0x61}, 32), 7: bytes.Repeat([]byte{0x72}, 32)}
	lookup := bytes.Repeat([]byte{0x83}, 32)
	old, err := NewSecretBox(6, keys, lookup)
	if err != nil {
		t.Fatal(err)
	}
	current, err := NewSecretBox(7, keys, lookup)
	if err != nil {
		t.Fatal(err)
	}
	source := SecretScope{OwnerType: "principal", OwnerID: "legacy-owner", Field: "verifier", Kind: "password-verifier"}
	target := SecretScope{OwnerType: "principal", OwnerID: "mapped-owner", Field: "password", Kind: "bcrypt"}
	plain := []byte("synthetic-original-verifier-bytes")
	original, err := old.Seal(source, plain)
	if err != nil {
		t.Fatal(err)
	}
	rebound, err := current.Rebind(source, target, original)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.KeyVersion != 6 || bytes.Equal(rebound.Nonce, original.Nonce) || bytes.Equal(rebound.Ciphertext, original.Ciphertext) {
		t.Fatal("rebind changed source version or reused encrypted material")
	}
	got, err := current.Open(target, rebound)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("rebind changed original plaintext")
	}
	if _, err := current.Open(source, rebound); err == nil {
		t.Fatal("rebound envelope retained old AAD")
	}
	if got, err := current.Open(source, original); err != nil || !bytes.Equal(got, plain) {
		t.Fatal("rebind mutated original envelope")
	}
	fresh, err := current.Seal(target, plain)
	if err != nil || fresh.KeyVersion != 7 {
		t.Fatal("ordinary Seal no longer uses current key")
	}
	wrong := source
	wrong.OwnerID += "-wrong"
	if _, err := current.Rebind(wrong, target, original); err == nil {
		t.Fatal("incorrect source scope authenticated")
	}
	for _, mutate := range []func(*Envelope){func(e *Envelope) { e.KeyVersion = 7 }, func(e *Envelope) { e.Nonce[0] ^= 1 }, func(e *Envelope) { e.Ciphertext[0] ^= 1 }} {
		changed := Envelope{KeyVersion: original.KeyVersion, Nonce: append([]byte(nil), original.Nonce...), Ciphertext: append([]byte(nil), original.Ciphertext...)}
		mutate(&changed)
		if _, err := current.Rebind(source, target, changed); err == nil {
			t.Fatal("tampered source envelope authenticated")
		}
	}
	missing, err := NewSecretBox(7, map[int][]byte{7: keys[7]}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Rebind(source, target, original); err == nil {
		t.Fatal("missing source key accepted")
	}
	if _, err := current.Rebind(source, SecretScope{}, original); err == nil {
		t.Fatal("invalid target scope accepted")
	}
	var absent *SecretBox
	if _, err := absent.Rebind(source, target, original); err == nil {
		t.Fatal("nil secret box accepted")
	}
}

func TestNewIDConcurrentUniqueness(t *testing.T) {
	const goroutines = 100
	const perGoroutine = 20

	ids := make(chan string, goroutines*perGoroutine)
	errs := make(chan error, goroutines*perGoroutine)
	var wg sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < perGoroutine; n++ {
				id, err := NewID("op")
				if err != nil {
					errs <- err
					continue
				}
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("NewID: %v", err)
	}

	seen := make(map[string]struct{}, goroutines*perGoroutine)
	for id := range ids {
		if !strings.HasPrefix(id, "op_") || len(id) != len("op_")+32 {
			t.Fatalf("invalid prefixed ID %q", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("generated %d IDs, want %d", len(seen), goroutines*perGoroutine)
	}
	if _, err := NewID("bad prefix"); err == nil {
		t.Fatal("NewID accepted an unsafe prefix")
	}
	if _, err := NewID(fmt.Sprintf("%032d", 1)); err == nil {
		t.Fatal("NewID accepted an overlong prefix")
	}
}
