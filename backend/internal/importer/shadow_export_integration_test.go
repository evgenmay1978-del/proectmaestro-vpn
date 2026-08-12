//go:build rqlite_integration

package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRQLiteShadowExportParity(t *testing.T) {
	if os.Getenv("MAESTRO_SHADOW_EXPORT_PROOF") != "1" {
		t.Skip("dedicated clean-cluster shadow proof is disabled")
	}
	db, err := rqlite.New(rqlite.Config{
		Endpoints: []string{"http://127.0.0.1:4401", "http://127.0.0.1:4403", "http://127.0.0.1:4405"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("rqlite.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	seed := []rqlite.Statement{{
		SQL: "INSERT INTO tariff_versions(tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix) VALUES(?,?,?,?,?,?,?)",
		Args: []any{"tariff_1m_v1", "one-month", 30, 40000, "RUB", 1, 1},
	}}
	for _, nodeID := range []string{"S1", "S2", "S3", "S4"} {
		seed = append(seed,
			rqlite.Statement{SQL: "INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES(?,?,?,?,?)", Args: []any{nodeID, nodeID, 1, 1, 1}},
			rqlite.Statement{SQL: "INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix) VALUES(?,?,?,?,?,?,?)", Args: []any{nodeID, "maestro-core", 1, 1, 0, 0, 1}},
		)
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true, seed...); err != nil {
		t.Fatalf("seed projection dependencies: %v", err)
	}

	privateURLMarker := "https://private-shadow-source.invalid"
	snapshot := decodeFixture(t, "orders-pending-credited.json")
	security := decodeFixture(t, "settings-principals-v1.json")
	security.Settings[1].PublicValueJSON = json.RawMessage(`{"enabled":true,"endpoint":"` + privateURLMarker + `"}`)
	snapshot.SourceHashes["security"] = security.SourceHashes["settings"]
	snapshot.Settings = security.Settings
	snapshot.Principals = security.Principals
	snapshot.EncryptedSecrets = append(snapshot.EncryptedSecrets, security.EncryptedSecrets...)
	plan, report := Plan(snapshot, testPlanOptions())
	if len(report.Blockers) != 0 {
		t.Fatalf("shadow parity plan blockers: %#v", report.Blockers)
	}
	legacy, err := ShadowFromPlan(plan, validShadowShapes())
	if err != nil {
		t.Fatalf("ShadowFromPlan: %v", err)
	}
	store, err := NewRQLiteApplyStore(db, func() time.Time { return time.Unix(1_800_000, 0) })
	if err != nil {
		t.Fatalf("NewRQLiteApplyStore: %v", err)
	}
	if _, err := Apply(ctx, store, plan, ApplyOptions{RunID: "shadow-export-proof-v1", BatchSize: 8}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	candidate, err := ShadowFromCandidate(ctx, store, plan.SourceDigest, validShadowShapes())
	if err != nil {
		t.Fatalf("ShadowFromCandidate: %v", err)
	}

	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "legacy.json")
	candidatePath := filepath.Join(directory, "candidate.json")
	saltPath := filepath.Join(directory, "salt")
	if err := WriteShadowExport(legacyPath, legacy); err != nil {
		t.Fatalf("WriteShadowExport legacy: %v", err)
	}
	if err := WriteShadowExport(candidatePath, candidate); err != nil {
		t.Fatalf("WriteShadowExport candidate: %v", err)
	}
	if err := os.WriteFile(saltPath, []byte("synthetic-shadow-proof-salt"), 0o600); err != nil {
		t.Fatalf("write verifier salt: %v", err)
	}
	verifier := filepath.Join("..", "..", "..", "ops", "ha", "shadow-verify.sh")
	command := exec.CommandContext(ctx, "bash", verifier, "--legacy", legacyPath, "--candidate", candidatePath, "--salt-file", saltPath)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("shadow verifier failed: %v: %s", commandErr, output)
	}
	wantOutput := []byte("{\"differences\":[],\"status\":\"match\"}\n")
	if !bytes.Equal(output, wantOutput) {
		t.Fatalf("shadow verifier output = %q", output)
	}
	legacyBytes, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("read legacy export: %v", err)
	}
	candidateBytes, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatalf("read candidate export: %v", err)
	}
	combined := bytes.Join([][]byte{legacyBytes, candidateBytes, output}, []byte("\n"))
	for _, forbidden := range []string{
		"OrderOwner", "MCRD1", "AAECAwQFBgcICQoL", "AQIDBAUGBwgJCgsM",
		"c3ludGhldGljLW9yZGVyLW93bmVy", "c3ludGhldGljLXBhbmVsLXZlcmlmaWVy",
		"c3ludGhldGljLWJvdC10b2tlbg==", privateURLMarker,
	} {
		if strings.Contains(string(combined), forbidden) {
			t.Fatalf("shadow proof leaked protected marker")
		}
	}
}
