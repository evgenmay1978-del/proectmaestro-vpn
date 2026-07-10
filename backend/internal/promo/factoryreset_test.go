package promo

import (
	"path/filepath"
	"testing"
)

// A factory reset changes the Android SSAID but NOT the hardware Widevine id → the same physical
// device must NOT be able to start a second trial.
func TestFactoryResetBlockedByDRM(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "promo.json"), "salt")
	if err != nil {
		t.Fatal(err)
	}
	drm := "a1b2c3d4e5f60718293a4b5c6d7e8f90" // 32 hex — a real Widevine id shape
	a1 := "ssaid-BEFORE|" + drm + "|BoxModelX"
	a2 := "ssaid-AFTER|" + drm + "|BoxModelX" // same device, post factory reset (SSAID rolled)

	if got := s.AnchorLoginMulti(a1); got != "" {
		t.Fatalf("fresh device should have no prior trial, got %q", got)
	}
	if err := s.SetAnchorMulti(a1, "trial-vasya", "vasya", "10.0.0/24"); err != nil {
		t.Fatal(err)
	}
	// same composite → matched
	if got := s.AnchorLoginMulti(a1); got != "trial-vasya" {
		t.Fatalf("reinstall (same anchor) must return existing, got %q", got)
	}
	// factory reset (SSAID changed, DRM same) → STILL matched via DRM
	if got := s.AnchorLoginMulti(a2); got != "trial-vasya" {
		t.Fatalf("factory reset must be blocked by DRM, got %q (want trial-vasya)", got)
	}
	// a genuinely different device (different DRM) → allowed
	other := "ssaid-Z|ffffffffffffffffffffffffffffffff|BoxModelX"
	if got := s.AnchorLoginMulti(other); got != "" {
		t.Fatalf("distinct device must be allowed, got %q", got)
	}
	// no-DRM L3 box (empty/short DRM) must NOT bucket distinct devices together
	l3a := "ssaidA||BoxL3"
	l3b := "ssaidB||BoxL3"
	if err := s.SetAnchorMulti(l3a, "trial-a", "a", "10.0.0/24"); err != nil {
		t.Fatal(err)
	}
	if got := s.AnchorLoginMulti(l3b); got != "" {
		t.Fatalf("distinct no-DRM boxes must NOT collide, got %q", got)
	}
	// persistence: reopen and re-check the DRM match survives a restart
	s2, err := Open(s.path, "salt")
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.AnchorLoginMulti(a2); got != "trial-vasya" {
		t.Fatalf("DRM match must survive restart, got %q", got)
	}
}
