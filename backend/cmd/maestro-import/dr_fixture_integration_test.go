//go:build rqlite_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type drSourceMetadata struct {
	FormatVersion      int    `json:"format_version"`
	SourceEpoch        int64  `json:"source_epoch"`
	SchemaVersion      int    `json:"schema_version"`
	SchemaChecksum     string `json:"schema_checksum"`
	RunID              string `json:"run_id"`
	SourceDigest       string `json:"source_digest"`
	PlanDigest         string `json:"plan_digest"`
	TargetDigest       string `json:"target_digest"`
	BatchCount         int    `json:"batch_count"`
	BatchReceiptDigest string `json:"batch_receipt_digest"`
	ReceiptSHA256      string `json:"receipt_sha256"`
	ShadowSHA256       string `json:"shadow_sha256"`
}

type drMigrationPrefixItem struct {
	version    int
	checksum   string
	statements []rqlite.Statement
}

func preparePopulatedExactDRV4(t *testing.T, ctx context.Context, db rqlite.RQLite) {
	t.Helper()
	exists, err := db.QueryStrong(ctx, rqlite.Statement{SQL: `SELECT name FROM sqlite_master
		WHERE type='table' AND name='schema_migrations'`})
	if err != nil || len(exists) != 1 || len(exists[0].Rows) != 0 {
		t.Fatalf("DR source cluster is not empty: %#v, %v", exists, err)
	}
	for _, migration := range loadExactDRV4Prefix(t) {
		statements := append([]rqlite.Statement(nil), migration.statements...)
		statements = append(statements, rqlite.Statement{
			SQL:  "INSERT INTO schema_migrations(version,checksum,applied_at_unix) VALUES(?,?,?)",
			Args: []any{migration.version, migration.checksum, migration.version},
		})
		if _, err := db.Request(ctx, rqlite.Linearizable, true, statements...); err != nil {
			t.Fatalf("apply exact DR migration v%d: %v", migration.version, err)
		}
	}
	if _, err := db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT INTO cluster_job_leases(
			job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix)
			VALUES('dr-v4-preserved-job','dr-v4-holder','dr-v4-lease',4,4000)`},
		rqlite.Statement{SQL: `INSERT INTO cluster_settings(
			setting_key,public_value_json,generation,updated_at_unix)
			VALUES('dr-v4-preserved-setting','{"source":"v4"}',4,4)`},
	); err != nil {
		t.Fatalf("populate exact DR v4 prefix: %v", err)
	}
	pre, err := db.QueryStrong(ctx,
		rqlite.Statement{SQL: "SELECT version FROM schema_migrations ORDER BY version"},
		rqlite.Statement{SQL: `SELECT name FROM sqlite_master
			WHERE type='table' AND name IN ('backup_rpo_state','backup_rpo_attempts') ORDER BY name`},
		rqlite.Statement{SQL: "PRAGMA table_info(cluster_job_leases)"},
	)
	if err != nil || len(pre) != 3 || len(pre[0].Rows) != 4 || len(pre[1].Rows) != 0 || len(pre[2].Rows) != 5 {
		t.Fatalf("exact populated v4 precondition mismatch: %#v, %v", pre, err)
	}
	for index, row := range pre[0].Rows {
		if fmt.Sprint(row["version"]) != fmt.Sprint(index+1) {
			t.Fatalf("v4 migration journal[%d]=%#v", index, row)
		}
	}
}

func loadExactDRV4Prefix(t *testing.T) []drMigrationPrefixItem {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate DR fixture source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "internal", "controlplane", "migrations"))
	files := []string{
		"0001_control_plane.sql", "0002_restore_epoch.sql",
		"0003_outbox_fencing.sql", "0004_whitelist_entitlement_identity.sql",
	}
	items := make([]drMigrationPrefixItem, 0, len(files))
	for index, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read exact DR migration %s: %v", name, err)
		}
		statements := make([]rqlite.Statement, 0)
		for _, part := range strings.Split(string(data), "-- maestro:statement") {
			sql := strings.TrimSpace(part)
			if sql == "" || strings.HasPrefix(sql, "-- MaestroVPN HA") {
				continue
			}
			statements = append(statements, rqlite.Statement{SQL: sql})
		}
		if len(statements) == 0 {
			t.Fatalf("exact DR migration %s has no statements", name)
		}
		items = append(items, drMigrationPrefixItem{
			version: index + 1, checksum: sha256HexDR(data), statements: statements,
		})
	}
	return items
}

func verifyDRV4UpgradePreservedAndSeeded(t *testing.T, ctx context.Context, db rqlite.RQLite) {
	t.Helper()
	proof, err := db.QueryStrong(ctx,
		rqlite.Statement{SQL: "SELECT version FROM schema_migrations ORDER BY version"},
		rqlite.Statement{SQL: `SELECT holder_id,lease_token,restore_epoch,lease_fence,
			capability_generation,capability_evidence_sha256,capability_expires_at_unix
			FROM cluster_job_leases WHERE job_name='dr-v4-preserved-job'`},
		rqlite.Statement{SQL: `SELECT b.restore_epoch,c.restore_epoch AS cluster_epoch,
			b.dirty_generation,b.verified_generation,b.last_attempt_sequence,b.phase,
			b.verified_backup_id,b.verified_object_key,b.verified_object_sha256,
			b.verified_object_version,b.verified_size_bytes,b.verified_manifest_version,b.verified_at_unix
			FROM backup_rpo_state b JOIN cluster_restore_state c ON c.singleton_id=1
			WHERE b.singleton_id=1`},
		rqlite.Statement{SQL: "SELECT COUNT(*) AS count FROM backup_rpo_attempts"},
		rqlite.Statement{SQL: `SELECT last_mutation_token FROM cluster_settings
			WHERE setting_key='dr-v4-preserved-setting'`},
	)
	if err != nil || len(proof) != 5 || len(proof[0].Rows) != controlplane.SchemaVersion ||
		len(proof[1].Rows) != 1 || len(proof[2].Rows) != 1 || len(proof[3].Rows) != 1 ||
		len(proof[4].Rows) != 1 {
		t.Fatalf("v4 upgrade proof shape mismatch: %#v, %v", proof, err)
	}
	for index, row := range proof[0].Rows {
		if fmt.Sprint(row["version"]) != fmt.Sprint(index+1) {
			t.Fatalf("upgraded migration journal[%d]=%#v", index, row)
		}
	}
	lease, backup := proof[1].Rows[0], proof[2].Rows[0]
	if lease["holder_id"] != "dr-v4-holder" || lease["lease_token"] != "dr-v4-lease" ||
		fmt.Sprint(lease["restore_epoch"]) != "0" || fmt.Sprint(lease["lease_fence"]) != "0" ||
		fmt.Sprint(lease["capability_generation"]) != "0" || lease["capability_evidence_sha256"] != nil ||
		fmt.Sprint(lease["capability_expires_at_unix"]) != "0" {
		t.Fatalf("v5 lease defaults/preservation mismatch: %#v", lease)
	}
	if fmt.Sprint(backup["restore_epoch"]) != fmt.Sprint(backup["cluster_epoch"]) ||
		fmt.Sprint(backup["dirty_generation"]) != "1" || fmt.Sprint(backup["verified_generation"]) != "0" ||
		fmt.Sprint(backup["last_attempt_sequence"]) != "0" || backup["phase"] != "dirty" {
		t.Fatalf("v5 backup singleton seed mismatch: %#v", backup)
	}
	for _, field := range []string{
		"verified_backup_id", "verified_object_key", "verified_object_sha256", "verified_object_version",
		"verified_size_bytes", "verified_manifest_version", "verified_at_unix",
	} {
		if backup[field] != nil {
			t.Fatalf("v5 backup singleton retained %s: %#v", field, backup)
		}
	}
	if fmt.Sprint(proof[3].Rows[0]["count"]) != "0" || proof[4].Rows[0]["last_mutation_token"] != "" {
		t.Fatalf("v5/v6 seed defaults mismatch: attempts=%#v setting=%#v", proof[3].Rows[0], proof[4].Rows[0])
	}
}

func clearDRV4MigrationProof(t *testing.T, ctx context.Context, db rqlite.RQLite) {
	t.Helper()
	results, err := db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `DELETE FROM cluster_job_leases
			WHERE job_name='dr-v4-preserved-job' AND holder_id='dr-v4-holder'
			AND lease_token='dr-v4-lease' AND acquired_at_unix=4 AND expires_at_unix=4000
			AND restore_epoch=0 AND lease_fence=0 AND capability_generation=0
			AND capability_evidence_sha256 IS NULL AND capability_expires_at_unix=0`},
		rqlite.Statement{SQL: `DELETE FROM cluster_settings
			WHERE setting_key='dr-v4-preserved-setting' AND public_value_json='{"source":"v4"}'
			AND generation=4 AND updated_at_unix=4 AND last_mutation_token=''`},
	)
	if err != nil {
		t.Fatalf("clear v4 migration proof: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("v4 migration proof cleanup result count=%d", len(results))
	}
	for index, result := range results {
		if result.RowsAffected != 1 {
			t.Fatalf(
				"v4 migration proof cleanup[%d] affected=%d, want 1",
				index, result.RowsAffected,
			)
		}
	}
}

func TestPrepareSyntheticDRSource(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "source" {
		t.Skip("dedicated DR source proof is disabled")
	}
	binary := os.Getenv("MAESTRO_IMPORT_BINARY")
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatal("exact importer binary is unavailable")
	}
	metadataPath := safeDRMetadataOutput(t)
	root := productionMTLSRoot(t)
	db := productionMTLSDB(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	migrator := controlplane.NewMigrator(db)
	preparePopulatedExactDRV4(t, ctx, db)
	if err := migrator.Apply(ctx); err != nil {
		t.Fatalf("apply source schema: %v", err)
	}
	verifyDRV4UpgradePreservedAndSeeded(t, ctx, db)
	identity, err := migrator.VerifyIdentity(ctx)
	if err != nil {
		t.Fatalf("verify source schema: %v", err)
	}
	upgrade, err := db.QueryStrong(ctx, rqlite.Statement{
		SQL: "SELECT version FROM schema_migrations WHERE version IN (4,5) ORDER BY version",
	})
	if err != nil || identity.Version != controlplane.SchemaVersion ||
		len(upgrade) != 1 || len(upgrade[0].Rows) != 2 ||
		fmt.Sprint(upgrade[0].Rows[0]["version"]) != "4" ||
		fmt.Sprint(upgrade[0].Rows[1]["version"]) != "5" {
		t.Fatalf("three-voter v4-to-v5 migration evidence mismatch: identity=%#v journal=%#v error=%v", identity, upgrade, err)
	}
	store, err := importer.NewRQLiteApplyStore(db, time.Now)
	if err != nil {
		t.Fatalf("new source store: %v", err)
	}
	populatedTarget, err := store.InspectTarget(ctx)
	if err != nil || populatedTarget.Empty {
		t.Fatalf("populated v4 upgrade was not visible to importer: %#v, %v", populatedTarget, err)
	}
	clearDRV4MigrationProof(t, ctx, db)
	emptyTarget, err := store.InspectTarget(ctx)
	if err != nil || !emptyTarget.Empty || emptyTarget.AppliedSourceDigest != "" {
		t.Fatalf(
			"v4 migration proof cleanup did not restore a fresh import target: %#v, %v",
			emptyTarget, err,
		)
	}
	files := prepareProductionProofFiles(t, root)
	const runID = "dr-source-proof-v1"
	output, code := runProductionBinary(ctx, binary, productionApplyArgs(
		files, files.target, files.keys, files.salt, files.report, files.receipt, runID,
	))
	if code != exitClean {
		t.Fatalf("source apply exit=%d output=%q", code, output)
	}
	receiptBytes := mustReadProofFile(t, files.receipt)
	receipt, err := importer.VerifyImportReceipt(receiptBytes, files.publicKey)
	if err != nil {
		t.Fatalf("verify source receipt: %v", err)
	}
	evidence, err := store.ReadAppliedRunEvidence(ctx, runID)
	if err != nil {
		t.Fatalf("read source evidence: %v", err)
	}
	target, err := store.InspectTarget(ctx)
	if err != nil {
		t.Fatalf("inspect source target: %v", err)
	}
	if target.BusinessDigest != receipt.TargetDigest || evidence.TargetDigest != receipt.TargetDigest ||
		evidence.BatchReceiptDigest != receipt.BatchReceiptDigest || evidence.BatchCount != receipt.BatchCount {
		t.Fatal("source receipt, evidence and business digest differ")
	}
	shapes := importer.ShadowURLShapes{
		Maestro: "maestro://import/{opaque-token}",
		Karing:  "https://proof.invalid/sub/{opaque-token}",
	}
	shadow, err := importer.ShadowFromCandidate(ctx, store, receipt.SourceDigest, shapes)
	if err != nil {
		t.Fatalf("source shadow: %v", err)
	}
	shadowBytes, err := importer.EncodeShadowExport(shadow)
	if err != nil {
		t.Fatalf("encode source shadow: %v", err)
	}
	state, err := controlplane.NewRestoreEpochStore(db).Current(ctx)
	if err != nil || !state.Activated || state.RestoreEpoch <= 0 {
		t.Fatalf("source restore state: %#v, %v", state, err)
	}
	metadata := drSourceMetadata{
		FormatVersion: 1, SourceEpoch: state.RestoreEpoch,
		SchemaVersion: identity.Version, SchemaChecksum: identity.Checksum,
		RunID: runID, SourceDigest: receipt.SourceDigest, PlanDigest: receipt.PlanDigest,
		TargetDigest: receipt.TargetDigest, BatchCount: receipt.BatchCount,
		BatchReceiptDigest: receipt.BatchReceiptDigest,
		ReceiptSHA256:      sha256HexDR(receiptBytes), ShadowSHA256: sha256HexDR(shadowBytes),
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(productionIdentityMarker)) ||
		bytes.Contains(encoded, []byte(productionTrialMarker)) {
		t.Fatal("redacted DR metadata contains a raw marker")
	}
	file, err := os.OpenFile(metadataPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create DR metadata: %v", err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		t.Fatalf("write DR metadata: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync DR metadata: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close DR metadata: %v", err)
	}
}

func safeDRMetadataOutput(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	if err != nil || !filepath.IsAbs(base) {
		t.Fatal("RUNNER_TEMP is unavailable")
	}
	path := os.Getenv("MAESTRO_DR_METADATA")
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || parent != base || filepath.Base(path) != "dr-source-metadata.json" {
		t.Fatal("DR metadata path is outside the runner root")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("DR metadata output already exists or is unsafe")
	}
	return path
}

func sha256HexDR(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
