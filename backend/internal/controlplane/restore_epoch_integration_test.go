//go:build rqlite_integration

package controlplane

import (
	"os"
	"testing"
)

func TestAdvanceRestoredEpochAndFence(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "restored" {
		t.Skip("dedicated restored epoch proof is disabled")
	}
	t.Fatal("restored epoch fencing proof wiring is absent")
}
