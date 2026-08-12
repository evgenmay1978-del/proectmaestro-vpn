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

func TestAdvanceRestoredEpochAndFence(t *testing.T) {
	if os.Getenv("MAESTRO_DR_PROOF_PHASE") != "restored" {
		t.Skip("dedicated restored epoch proof is disabled")
	}
	metadata := readEpochDRMetadata(t)
	db := restoredEpochRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	store := NewRestoreEpochStore(db)
	before, err := store.Current(ctx)
	if err != nil || !before.Activated || before.RestoreEpoch != metadata.SourceEpoch {
		t.Fatalf("restored initial epoch mismatch: %#v, %v", before, err)
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

	after, err := store.AdvanceAfterRestore(ctx, before.RestoreEpoch, metadata.BackupSHA256)
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
	)
	if err != nil || len(invalidated) != 3 ||
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

	mutation := func(epoch int64) int64 {
		t.Helper()
		results, requestErr := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
			SQL: `UPDATE telegram_pollers SET offset_value=43,lease_fence=13,updated_at_unix=101
				WHERE bot_identity_hmac=? AND lease_fence=12
				AND EXISTS(SELECT 1 FROM cluster_restore_state
					WHERE singleton_id=1 AND restore_epoch=? AND activated=1)`,
			Args: []any{bot, epoch},
		})
		if requestErr != nil || len(results) != 1 {
			t.Fatalf("synthetic epoch mutation: %#v, %v", results, requestErr)
		}
		return results[0].RowsAffected
	}
	if affected := mutation(after.RestoreEpoch); affected != 0 {
		t.Fatalf("inactive epoch accepted mutation: %d", affected)
	}
	active, err := store.Activate(ctx, after.RestoreEpoch)
	if err != nil || !active.Activated || active.RestoreEpoch != after.RestoreEpoch {
		t.Fatalf("activate restored epoch: %#v, %v", active, err)
	}
	if affected := mutation(before.RestoreEpoch); affected != 0 {
		t.Fatalf("old epoch accepted mutation: %d", affected)
	}
	if affected := mutation(after.RestoreEpoch); affected != 1 {
		t.Fatalf("current epoch first mutation affected %d rows", affected)
	}
	if affected := mutation(after.RestoreEpoch); affected != 0 {
		t.Fatalf("current epoch replay affected %d rows", affected)
	}
	final, err := db.QueryStrong(ctx, rqlite.Statement{
		SQL: "SELECT offset_value,lease_fence FROM telegram_pollers WHERE bot_identity_hmac=?",
		Args: []any{bot},
	})
	if err != nil || len(final) != 1 || len(final[0].Rows) != 1 ||
		!drIntegerEquals(final[0].Rows[0]["offset_value"], 43) ||
		!drIntegerEquals(final[0].Rows[0]["lease_fence"], 13) {
		t.Fatalf("current epoch exact-once state mismatch: %#v, %v", final, err)
	}
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
		Endpoints: []string{
			"https://127.0.0.1:4401", "https://127.0.0.1:4403", "https://127.0.0.1:4405",
		},
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
