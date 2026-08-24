//go:build rqlite_integration

package controlplane

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/rqliteintegrationlock"
)

func TestMain(m *testing.M) {
	runPattern := rqliteIntegrationRunPattern()
	selection := rqliteintegrationlock.Selection{
		Package: "controlplane", RunPattern: runPattern,
		DRProofPhase:  os.Getenv("MAESTRO_DR_PROOF_PHASE"),
		DRFencePhase:  os.Getenv("MAESTRO_DR_FENCE_PHASE"),
		DRQuorumPhase: os.Getenv("MAESTRO_DR_QUORUM_PHASE"),
	}
	if rqliteintegrationlock.IsExactSequentialDRSelection(selection) {
		os.Exit(m.Run())
	}
	endpoints := rqliteintegrationlock.DefaultEndpoints()
	if rqliteintegrationlock.IsExactNonLiveSelection("controlplane", runPattern) {
		endpoints = nil
	}
	exitCode := rqliteintegrationlock.Run(rqliteintegrationlock.RunOptions{
		HolderID: "controlplane-package",
		Migrate: func(ctx context.Context, db rqlite.RQLite) error {
			return NewMigrator(db).Apply(ctx)
		},
		Run:       func(rqliteintegrationlock.Identity) int { return m.Run() },
		Endpoints: endpoints,
	})
	os.Exit(exitCode)
}

func rqliteIntegrationRunPattern() string {
	if !flag.Parsed() {
		flag.Parse()
	}
	if testRun := flag.Lookup("test.run"); testRun != nil {
		return testRun.Value.String()
	}
	return ""
}
