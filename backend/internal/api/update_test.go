package api

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUpdateManifestFor verifies the staged-rollout routing: a device behind a mandatory
// waypoint is stepped UP to it before any newer build; once on/past the waypoint it gets the
// latest (may skip versions). Guards the AWG-engine 107 checkpoint against regression.
func TestUpdateManifestFor(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, code int) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"version_code":`+itoa(code)+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := updateWaypoints
	updateWaypoints = []int{107}
	defer func() { updateWaypoints = old }()

	// Scenario A: a newer build (108) exists beyond the waypoint.
	write("update.json", 108)
	write("update-107.json", 107)
	for _, c := range []struct {
		v    int
		want string
	}{
		{106, "update-107.json"}, // behind 107 → must step to 107 first (never jump to 108)
		{105, "update-107.json"}, // also behind → 107
		{107, "update.json"},     // on the waypoint → latest (108)
		{108, "update.json"},     // already latest
		{0, "update.json"},       // non-app client (no SFA UA) → latest
	} {
		if got := updateManifestFor(dir, c.v); got != c.want {
			t.Errorf("scenarioA v=%d: got %s, want %s", c.v, got, c.want)
		}
	}

	// Scenario B: the waypoint IS the latest (current real state, 107 = newest). Everyone
	// gets update.json directly — no funnel, since there is nothing newer to gate.
	write("update.json", 107)
	for _, v := range []int{100, 106, 107} {
		if got := updateManifestFor(dir, v); got != "update.json" {
			t.Errorf("scenarioB v=%d: got %s, want update.json", v, got)
		}
	}

	// Scenario C: missing frozen waypoint manifest → fall back to latest (never 404 the app).
	_ = os.Remove(filepath.Join(dir, "update-107.json"))
	write("update.json", 108)
	if got := updateManifestFor(dir, 106); got != "update.json" {
		t.Errorf("scenarioC fallback: got %s, want update.json", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
