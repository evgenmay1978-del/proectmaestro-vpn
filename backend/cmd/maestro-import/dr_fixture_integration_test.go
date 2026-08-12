//go:build rqlite_integration

package main

import (
	"os"
	"testing"
)

func TestPrepareSyntheticDRSource(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "source" {
		t.Skip("dedicated DR source proof is disabled")
	}
	t.Fatal("synthetic DR source proof wiring is absent")
}
