//go:build rqlite_integration

package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type drBackupRPOState struct {
	RestoreEpoch        int64
	DirtyGeneration     int64
	VerifiedGeneration  int64
	LastAttemptSequence int64
	Phase               string
}

func readDRBackupRPOState(t *testing.T, ctx context.Context, db rqlite.RQLite) drBackupRPOState {
	t.Helper()
	results, err := db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
		SELECT restore_epoch,dirty_generation,verified_generation,last_attempt_sequence,phase
		FROM backup_rpo_state WHERE singleton_id=1`})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("read backup RPO state: %#v, %v", results, err)
	}
	row := results[0].Rows[0]
	state := drBackupRPOState{}
	var ok bool
	state.RestoreEpoch, ok = rowInt64(row, "restore_epoch")
	if !ok {
		t.Fatalf("backup restore epoch is malformed: %#v", row)
	}
	state.DirtyGeneration, _ = rowInt64(row, "dirty_generation")
	state.VerifiedGeneration, _ = rowInt64(row, "verified_generation")
	state.LastAttemptSequence, _ = rowInt64(row, "last_attempt_sequence")
	state.Phase, ok = rowString(row, "phase")
	if !ok {
		t.Fatalf("backup phase is malformed: %#v", row)
	}
	return state
}

func TestAdvanceRestoredEpochAndFence(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "restored" {
		t.Skip("dedicated restored epoch proof is disabled")
	}
	phase := os.Getenv("MAESTRO_DR_FENCE_PHASE")
	if phase != "advance" && phase != "activate" {
		t.Fatal("MAESTRO_DR_FENCE_PHASE must be advance or activate")
	}
	metadata := readEpochDRMetadata(t)
	db := restoredEpochRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	store := NewRestoreEpochStore(db)
	if phase == "advance" {
		before, err := store.Current(ctx)
		if err != nil || !before.Activated || before.RestoreEpoch != metadata.SourceEpoch {
			t.Fatalf("restored initial epoch mismatch: %#v, %v", before, err)
		}
		upgrade, err := db.QueryStrong(ctx, rqlite.Statement{
			SQL: "SELECT version FROM schema_migrations WHERE version IN (4,5) ORDER BY version",
		})
		if err != nil || metadata.SchemaVersion != SchemaVersion ||
			len(upgrade) != 1 || len(upgrade[0].Rows) != 2 ||
			!drIntegerEquals(upgrade[0].Rows[0]["version"], 4) ||
			!drIntegerEquals(upgrade[0].Rows[1]["version"], 5) {
			t.Fatalf("restored v4-to-v5 migration evidence mismatch: %#v, metadata=%#v, error=%v", upgrade, metadata, err)
		}
		backupBefore := readDRBackupRPOState(t, ctx, db)
		if backupBefore.RestoreEpoch != before.RestoreEpoch {
			t.Fatalf("restored backup singleton epoch=%d, want %d", backupBefore.RestoreEpoch, before.RestoreEpoch)
		}
		topology, err := db.QueryStrong(ctx, rqlite.Statement{
			SQL: "SELECT node_id,service_name FROM node_services ORDER BY node_id,service_name LIMIT 1",
		})
		if err != nil || len(topology) != 1 || len(topology[0].Rows) != 1 {
			t.Fatalf("restored topology unavailable: %#v, %v", topology, err)
		}
		nodeID, nodeOK := topology[0].Rows[0]["node_id"].(string)
		service, serviceOK := topology[0].Rows[0]["service_name"].(string)
		if !nodeOK || !serviceOK || nodeID == "" || service == "" {
			t.Fatal("restored topology is invalid")
		}
		bot := strings.Repeat("d", 64)
		_, err = db.Request(ctx, rqlite.Linearizable, true,
			rqlite.Statement{SQL: `INSERT INTO backup_rpo_attempts(
				restore_epoch,attempt_sequence,phase,backup_id,captured_generation,
				object_key,object_sha256,object_version,object_size_bytes,manifest_version,
				adapter_contract_version,capability_generation,capability_evidence_sha256,
				capability_expires_at_unix,lease_holder_id,lease_token,lease_fence,
				failure_code,created_at_unix,updated_at_unix)
			SELECT restore_epoch,9000001,'pending',?,dirty_generation,
				?,?,NULL,1,2,'yandex-s3-v1',1,?,9999999999,
				'task6-holder','task6-token',1,NULL,100,100
			FROM backup_rpo_state WHERE singleton_id=1`, Args: []any{
				strings.Repeat("a", 32), "task6/restore.db", strings.Repeat("b", 64), strings.Repeat("c", 64),
			}},
			rqlite.Statement{SQL: `UPDATE backup_rpo_state
				SET last_attempt_sequence=9000001,updated_at_unix=updated_at_unix+1
				WHERE singleton_id=1`},
			rqlite.Statement{SQL: `INSERT INTO telegram_bot_routes(
				bot_identity_hmac,token_fingerprint_hmac,credential_version,schema_fingerprint,updated_at_unix
				) VALUES(?,?,?,?,?)`, Args: []any{bot, strings.Repeat("e", 64), 1, "dr-fence-v1", 100}},
			rqlite.Statement{SQL: `INSERT INTO telegram_pollers(
				bot_identity_hmac,node_id,lease_token,offset_value,lease_fence,
				lease_expires_at_unix,updated_at_unix) VALUES(?,?,?,?,?,?,?)`,
				Args: []any{bot, nodeID, "old-poller-token", 42, 11, 999999, 100}},
			rqlite.Statement{SQL: `INSERT INTO node_leases(
				node_id,service_name,holder_id,lease_token,acquired_at_unix,expires_at_unix
				) VALUES(?,?,?,?,?,?)`,
				Args: []any{nodeID, service, "old-holder", "old-node-token", 100, 999999}},
			rqlite.Statement{SQL: `INSERT INTO cluster_job_leases(
				job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix
				) VALUES(?,?,?,?,?)`,
				Args: []any{"dr-old-job", "old-holder", "old-job-token", 100, 999999}},
		)
		if err != nil {
			t.Fatalf("seed old leases: %v", err)
		}
		committedUnknown := &committedUnknownRQLite{delegate: db}
		after, err := NewRestoreEpochStore(committedUnknown).AdvanceAfterRestore(
			ctx, before.RestoreEpoch, metadata.BackupSHA256,
		)
		if committedUnknown.requestCalls.Load() != 1 || committedUnknown.linearCalls.Load() != 1 {
			t.Fatalf("restore ambiguity calls=%d/%d, want 1/1", committedUnknown.requestCalls.Load(), committedUnknown.linearCalls.Load())
		}
		if err != nil || after.RestoreEpoch != before.RestoreEpoch+1 || after.Activated ||
			after.RestoredFromBackupSHA256 != metadata.BackupSHA256 {
			t.Fatalf("advance restored epoch: %#v, %v", after, err)
		}
		invalidated, err := db.QueryStrong(ctx,
			rqlite.Statement{SQL: "SELECT COUNT(*) AS count FROM node_leases WHERE lease_token=?",
				Args: []any{"old-node-token"}},
			rqlite.Statement{SQL: "SELECT COUNT(*) AS count FROM cluster_job_leases WHERE lease_token=?",
				Args: []any{"old-job-token"}},
			rqlite.Statement{SQL: `SELECT node_id,lease_token,offset_value,lease_fence,lease_expires_at_unix
				FROM telegram_pollers WHERE bot_identity_hmac=?`, Args: []any{bot}},
			rqlite.Statement{SQL: `SELECT restore_epoch,dirty_generation,verified_generation,last_attempt_sequence,phase,
				verified_backup_id,verified_object_key,verified_object_sha256,verified_object_version,
				verified_size_bytes,verified_manifest_version,verified_at_unix
				FROM backup_rpo_state WHERE singleton_id=1`},
			rqlite.Statement{SQL: `SELECT phase,failure_code FROM backup_rpo_attempts
				WHERE restore_epoch=? AND attempt_sequence=9000001`, Args: []any{before.RestoreEpoch}},
		)
		if err != nil || len(invalidated) != 5 ||
			len(invalidated[0].Rows) != 1 || len(invalidated[1].Rows) != 1 ||
			len(invalidated[2].Rows) != 1 || len(invalidated[3].Rows) != 1 ||
			len(invalidated[4].Rows) != 1 ||
			!drIntegerEquals(invalidated[0].Rows[0]["count"], 0) ||
			!drIntegerEquals(invalidated[1].Rows[0]["count"], 0) ||
			len(invalidated[2].Rows) != 1 ||
			invalidated[2].Rows[0]["node_id"] != nil ||
			invalidated[2].Rows[0]["lease_token"] != nil ||
			!drIntegerEquals(invalidated[2].Rows[0]["offset_value"], 42) ||
			!drIntegerEquals(invalidated[2].Rows[0]["lease_fence"], 12) ||
			!drIntegerEquals(invalidated[2].Rows[0]["lease_expires_at_unix"], 0) {
			t.Fatalf("old leases were not invalidated: %#v, %v", invalidated, err)
		}
		backup := invalidated[3].Rows[0]
		if !drIntegerEquals(backup["restore_epoch"], after.RestoreEpoch) ||
			!drIntegerEquals(backup["dirty_generation"], 1) ||
			!drIntegerEquals(backup["verified_generation"], 0) ||
			!drIntegerEquals(backup["last_attempt_sequence"], 0) || backup["phase"] != "dirty" {
			t.Fatalf("backup restore handoff mismatch: %#v", backup)
		}
		for _, field := range []string{
			"verified_backup_id", "verified_object_key", "verified_object_sha256", "verified_object_version",
			"verified_size_bytes", "verified_manifest_version", "verified_at_unix",
		} {
			if backup[field] != nil {
				t.Fatalf("backup restore handoff retained %s: %#v", field, backup)
			}
		}
		if len(invalidated[4].Rows) != 1 || invalidated[4].Rows[0]["phase"] != "superseded" ||
			invalidated[4].Rows[0]["failure_code"] != "restore-epoch" {
			t.Fatalf("active backup attempt was not superseded: %#v", invalidated[4])
		}
		if affected := drEpochMutation(t, ctx, db, after.RestoreEpoch); affected != 0 {
			t.Fatalf("inactive epoch accepted mutation: %d", affected)
		}
		writeAdvancedEpoch(t, before.RestoreEpoch, after.RestoreEpoch, metadata.BackupSHA256)
		return
	}

	checkpoint := readAdvancedEpoch(t)
	state, err := store.Current(ctx)
	if err != nil || state.Activated || state.RestoreEpoch != checkpoint.RestoredEpoch ||
		state.RestoredFromBackupSHA256 != checkpoint.BackupSHA256 {
		t.Fatalf("restart did not preserve inactive epoch: %#v, %v", state, err)
	}
	if affected := drEpochMutation(t, ctx, db, checkpoint.RestoredEpoch); affected != 0 {
		t.Fatalf("inactive epoch accepted mutation after restart: %d", affected)
	}
	backupBeforeActivation := readDRBackupRPOState(t, ctx, db)
	active, err := store.Activate(ctx, checkpoint.RestoredEpoch)
	if err != nil || !active.Activated || active.RestoreEpoch != checkpoint.RestoredEpoch {
		t.Fatalf("activate restored epoch: %#v, %v", active, err)
	}
	if backupAfterActivation := readDRBackupRPOState(t, ctx, db); backupAfterActivation != backupBeforeActivation {
		t.Fatalf("activation dirtied backup state: before=%#v after=%#v", backupBeforeActivation, backupAfterActivation)
	}
	if affected := drEpochMutation(t, ctx, db, checkpoint.SourceEpoch); affected != 0 {
		t.Fatalf("old epoch accepted mutation: %d", affected)
	}
	if affected := drEpochMutation(t, ctx, db, checkpoint.RestoredEpoch); affected != 1 {
		t.Fatalf("current epoch first mutation affected %d rows", affected)
	}
	if affected := drEpochMutation(t, ctx, db, checkpoint.RestoredEpoch); affected != 0 {
		t.Fatalf("current epoch replay affected %d rows", affected)
	}
	final, err := db.QueryStrong(ctx, rqlite.Statement{
		SQL: "SELECT offset_value,lease_fence FROM telegram_pollers WHERE bot_identity_hmac=?",
		Args: []any{strings.Repeat("d", 64)},
	})
	if err != nil || len(final) != 1 || len(final[0].Rows) != 1 ||
		!drIntegerEquals(final[0].Rows[0]["offset_value"], 43) ||
		!drIntegerEquals(final[0].Rows[0]["lease_fence"], 13) {
		t.Fatalf("current epoch exact-once state mismatch: %#v, %v", final, err)
	}
}

func TestRestoredQuorumBoundaries(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "restored" {
		t.Skip("dedicated restored quorum proof is disabled")
	}
	phase := os.Getenv("MAESTRO_DR_QUORUM_PHASE")
	if phase != "one-loss" && phase != "two-loss" {
		t.Fatal("MAESTRO_DR_QUORUM_PHASE must be one-loss or two-loss")
	}
	checkpoint := readAdvancedEpoch(t)
	endpoints := []string{"https://127.0.0.1:4401", "https://127.0.0.1:4403"}
	if phase == "two-loss" {
		endpoints = endpoints[:1]
	}
	db := restoredEpochRQLiteEndpoints(t, endpoints)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	bot := strings.Repeat("d", 64)
	const updateSQL = `UPDATE telegram_pollers
		SET offset_value=?,lease_fence=?,updated_at_unix=?
		WHERE bot_identity_hmac=? AND lease_fence=?
		AND EXISTS(SELECT 1 FROM cluster_restore_state
			WHERE singleton_id=1 AND restore_epoch=? AND activated=1)`
	if phase == "one-loss" {
		state, err := NewRestoreEpochStore(db).Current(ctx)
		if err != nil || !state.Activated || state.RestoreEpoch != checkpoint.RestoredEpoch {
			t.Fatalf("one voter loss lost active restore state: %#v, %v", state, err)
		}
		before, err := db.QueryStrong(ctx, rqlite.Statement{
			SQL:  "SELECT offset_value,lease_fence FROM telegram_pollers WHERE bot_identity_hmac=?",
			Args: []any{bot},
		})
		if err != nil || len(before) != 1 || len(before[0].Rows) != 1 ||
			!drIntegerEquals(before[0].Rows[0]["offset_value"], 43) ||
			!drIntegerEquals(before[0].Rows[0]["lease_fence"], 13) {
			t.Fatalf("one voter loss strong state mismatch: %#v, %v", before, err)
		}
		write, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
			SQL: updateSQL,
			Args: []any{
				44, 14, 102, bot, 13, checkpoint.RestoredEpoch,
			},
		})
		if err != nil || len(write) != 1 || write[0].RowsAffected != 1 {
			t.Fatalf("one voter loss rejected exact-once write: %#v, %v", write, err)
		}
		replay, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
			SQL: updateSQL,
			Args: []any{
				44, 14, 102, bot, 13, checkpoint.RestoredEpoch,
			},
		})
		if err != nil || len(replay) != 1 || replay[0].RowsAffected != 0 {
			t.Fatalf("one voter loss replay was not idempotent: %#v, %v", replay, err)
		}
		after, err := db.QueryStrong(ctx, rqlite.Statement{
			SQL:  "SELECT offset_value,lease_fence FROM telegram_pollers WHERE bot_identity_hmac=?",
			Args: []any{bot},
		})
		if err != nil || len(after) != 1 || len(after[0].Rows) != 1 ||
			!drIntegerEquals(after[0].Rows[0]["offset_value"], 44) ||
			!drIntegerEquals(after[0].Rows[0]["lease_fence"], 14) {
			t.Fatalf("one voter loss post-write state mismatch: %#v, %v", after, err)
		}
		return
	}

	write, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: updateSQL,
		Args: []any{
			45, 15, 103, bot, 14, checkpoint.RestoredEpoch,
		},
	})
	if err == nil {
		t.Fatalf("two voter loss accepted linearizable write: %#v", write)
	}
	if ctx.Err() != nil {
		t.Fatalf("two voter loss did not reject within bounded client timeout: %v", ctx.Err())
	}
}

func drEpochMutation(t *testing.T, ctx context.Context, db rqlite.RQLite, epoch int64) int64 {
	t.Helper()
	results, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `UPDATE telegram_pollers SET offset_value=43,lease_fence=13,updated_at_unix=101
			WHERE bot_identity_hmac=? AND lease_fence=12
			AND EXISTS(SELECT 1 FROM cluster_restore_state
				WHERE singleton_id=1 AND restore_epoch=? AND activated=1)`,
		Args: []any{strings.Repeat("d", 64), epoch},
	})
	if err != nil || len(results) != 1 {
		t.Fatalf("synthetic epoch mutation: %#v, %v", results, err)
	}
	return results[0].RowsAffected
}

type advancedEpochCheckpoint struct {
	FormatVersion int    `json:"format_version"`
	SourceEpoch   int64  `json:"source_epoch"`
	RestoredEpoch int64  `json:"restored_epoch"`
	BackupSHA256  string `json:"backup_sha256"`
}

func advancedEpochPath(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	if err != nil || !filepath.IsAbs(base) {
		t.Fatal("RUNNER_TEMP is unavailable")
	}
	return filepath.Join(base, "dr-advanced-epoch.json")
}

func writeAdvancedEpoch(t *testing.T, source, restored int64, backup string) {
	t.Helper()
	path := advancedEpochPath(t)
	encoded, err := json.Marshal(advancedEpochCheckpoint{
		FormatVersion: 1, SourceEpoch: source, RestoredEpoch: restored, BackupSHA256: backup,
	})
	if err != nil {
		t.Fatal("encode advanced epoch checkpoint")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal("create advanced epoch checkpoint")
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		t.Fatal("write advanced epoch checkpoint")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal("sync advanced epoch checkpoint")
	}
	if err := file.Close(); err != nil {
		t.Fatal("close advanced epoch checkpoint")
	}
}

func readAdvancedEpoch(t *testing.T) advancedEpochCheckpoint {
	t.Helper()
	path := advancedEpochPath(t)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatal("advanced epoch checkpoint is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("open advanced epoch checkpoint")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4096))
	decoder.DisallowUnknownFields()
	var checkpoint advancedEpochCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		t.Fatal("decode advanced epoch checkpoint")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF ||
		checkpoint.FormatVersion != 1 || checkpoint.SourceEpoch <= 0 ||
		checkpoint.RestoredEpoch != checkpoint.SourceEpoch+1 ||
		!canonicalRestoreHex(checkpoint.BackupSHA256) {
		t.Fatal("advanced epoch checkpoint is invalid")
	}
	return checkpoint
}

type epochDRMetadata struct {
	FormatVersion  int    `json:"format_version"`
	SourceEpoch    int64  `json:"source_epoch"`
	SchemaVersion  int    `json:"schema_version"`
	SchemaChecksum string `json:"schema_checksum"`
	RunID          string `json:"run_id"`
	SourceDigest   string `json:"source_digest"`
	PlanDigest     string `json:"plan_digest"`
	TargetDigest   string `json:"target_digest"`
	BatchCount     int    `json:"batch_count"`
	BatchReceiptDigest string `json:"batch_receipt_digest"`
	ReceiptSHA256  string `json:"receipt_sha256"`
	ShadowSHA256   string `json:"shadow_sha256"`
	BackupSHA256   string `json:"backup_sha256"`
}

func readEpochDRMetadata(t *testing.T) epochDRMetadata {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	path := os.Getenv("MAESTRO_DR_METADATA")
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
	info, statErr := os.Lstat(path)
	if err != nil || parentErr != nil || statErr != nil || parent != base ||
		filepath.Base(path) != "dr-metadata.json" || !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 {
		t.Fatal("DR metadata file is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("open DR metadata")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var metadata epochDRMetadata
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal("decode DR metadata")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("DR metadata has trailing content")
	}
	if metadata.FormatVersion != 1 || metadata.SourceEpoch <= 0 ||
		!canonicalRestoreHex(metadata.BackupSHA256) {
		t.Fatal("DR metadata values are invalid")
	}
	return metadata
}

func restoredEpochRQLite(t *testing.T) *rqlite.Client {
	t.Helper()
	return restoredEpochRQLiteEndpoints(t, []string{
		"https://127.0.0.1:4401", "https://127.0.0.1:4403", "https://127.0.0.1:4405",
	})
}

func restoredEpochRQLiteEndpoints(t *testing.T, endpoints []string) *rqlite.Client {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	if err != nil {
		t.Fatal("resolve runner temp")
	}
	rootBytes, err := os.ReadFile(filepath.Join(base, "maestro-rqlite-ci-root"))
	if err != nil {
		t.Fatal("read cluster marker")
	}
	rootLine := strings.TrimSuffix(string(rootBytes), "\n")
	root, err := filepath.EvalSymlinks(rootLine)
	if err != nil || root != rootLine || filepath.Dir(root) != base {
		t.Fatal("unsafe restored cluster root")
	}
	db, err := rqlite.New(rqlite.Config{
		Endpoints: endpoints,
		CAFile: filepath.Join(root, "tls", "ca.crt"),
		CertFile: filepath.Join(root, "tls", "client.crt"),
		KeyFile: filepath.Join(root, "tls", "client.key"),
		Timeout: 10 * time.Second, MaxResponseBytes: 8 << 20, MaxBackupBytes: 4 << 30,
	})
	if err != nil {
		t.Fatalf("new restored rqlite client: %v", err)
	}
	return db
}

func drIntegerEquals(value any, want int64) bool {
	got, ok := restoreInteger(value)
	return ok && got == want
}
