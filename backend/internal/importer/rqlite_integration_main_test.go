//go:build rqlite_integration

package importer

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/rqliteintegrationlock"
)

type task6BackupRPOLockIdentity = rqliteintegrationlock.Identity

var task6BackupRPOIntegrationLeaseIdentity task6BackupRPOLockIdentity

func TestMain(m *testing.M) {
	runPattern := rqliteIntegrationRunPattern()
	selection := rqliteintegrationlock.Selection{
		Package: "importer", RunPattern: runPattern,
		DRProofPhase:  os.Getenv("MAESTRO_DR_PROOF_PHASE"),
		DRFencePhase:  os.Getenv("MAESTRO_DR_FENCE_PHASE"),
		DRQuorumPhase: os.Getenv("MAESTRO_DR_QUORUM_PHASE"),
	}
	if rqliteintegrationlock.IsExactSequentialDRSelection(selection) {
		os.Exit(m.Run())
	}
	endpoints := rqliteintegrationlock.DefaultEndpoints()
	if rqliteintegrationlock.IsExactNonLiveSelection("importer", runPattern) {
		endpoints = nil
	}
	exitCode := rqliteintegrationlock.Run(rqliteintegrationlock.RunOptions{
		HolderID: "importer-package",
		Migrate: func(ctx context.Context, db rqlite.RQLite) error {
			return controlplane.NewMigrator(db).Apply(ctx)
		},
		Run: func(identity rqliteintegrationlock.Identity) int {
			task6BackupRPOIntegrationLeaseIdentity = identity
			return m.Run()
		},
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
