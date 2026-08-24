//go:build rqlite_integration

package importer

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type task6CleanupSQLiteResult struct {
	OwnedRows     int             `json:"owned_rows"`
	OwnedRestored bool            `json:"owned_restored"`
	Blocked       map[string]bool `json:"blocked"`
}

func TestTask6IntegrationCleanupRestoresOnlyExactOwnedPostconditionSQLite(t *testing.T) {
	baseline := task6BackupRPOState{
		RestoreEpoch: 1, DirtyGeneration: 5, VerifiedGeneration: 5,
		VerifiedBackupID: strings.Repeat("a", 32), VerifiedObjectKey: "task6/verified.db",
		VerifiedObjectSHA256: strings.Repeat("b", 64), VerifiedObjectVersion: "version-5",
		VerifiedSizeBytes: int64(4096), VerifiedManifestVersion: int64(2), VerifiedAtUnix: int64(900),
		LastAttemptSequence: 7, Phase: "verified", UpdatedAtUnix: 1000,
	}
	expectation := task6BackupRPOCleanupExpectation{
		DirtyGenerationDelta: 2, UpdatedAtUnix: 1_500_000,
		Receipt: task6ImportRunReceipt{
			RunID: "task6-owned-run", SourceDigest: strings.Repeat("c", 64),
			PlanDigest: strings.Repeat("d", 64), Status: "applied",
		},
	}
	owner := task6BackupRPOLockIdentity{
		JobName: "maestrovpn-backup-rpo-integration-v1", HolderID: "importer-package",
		LeaseToken: strings.Repeat("e", 64),
	}
	statement := task6IntegrationBackupCleanupStatement(baseline, expectation, owner)
	result := executeTask6CleanupSQLite(t, statement, baseline, expectation, owner)
	if result.OwnedRows != 1 || !result.OwnedRestored {
		t.Fatalf("owned cleanup=(rows=%d restored=%v), want (1,true)", result.OwnedRows, result.OwnedRestored)
	}
	for _, scenario := range []string{
		"concurrent_dirty", "verified", "unexpected_attempt", "wrong_lease", "missing_receipt",
	} {
		if !result.Blocked[scenario] {
			t.Fatalf("cleanup overwrote or accepted non-owned scenario %q: %#v", scenario, result.Blocked)
		}
	}
}

func executeTask6CleanupSQLite(
	t *testing.T,
	statement rqlite.Statement,
	baseline task6BackupRPOState,
	expectation task6BackupRPOCleanupExpectation,
	owner task6BackupRPOLockIdentity,
) task6CleanupSQLiteResult {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "controlplane", "migrations", "*.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find current migrations: %v, paths=%v", err, paths)
	}
	sort.Strings(paths)
	schema := make([]string, 0)
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		parts := strings.Split(string(content), "-- maestro:statement")
		for _, part := range parts[1:] {
			if sql := strings.TrimSpace(part); sql != "" {
				schema = append(schema, sql)
			}
		}
	}
	payload, err := json.Marshal(map[string]any{
		"schema": schema, "cleanup": statement, "baseline": baseline,
		"expectation": expectation, "owner": owner,
	})
	if err != nil {
		t.Fatalf("encode cleanup SQLite payload: %v", err)
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatalf("working python sqlite3 is required: %v", err)
	}
	command := exec.Command(python, "-c", task6CleanupSQLiteProgram)
	command.Stdin = bytes.NewReader(payload)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("execute cleanup SQLite proof: %v: %s", commandErr, output)
	}
	var result task6CleanupSQLiteResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode cleanup SQLite proof: %v: %s", err, output)
	}
	return result
}

const task6CleanupSQLiteProgram = `
import json
import sqlite3
import sys

payload = json.load(sys.stdin)
now = [2000000]
connection = sqlite3.connect(":memory:", isolation_level=None)
connection.create_function("unixepoch", 0, lambda: now[0])
connection.execute("PRAGMA foreign_keys=ON")
for statement in payload["schema"]:
    connection.execute(statement).fetchall()

baseline = payload["baseline"]
expectation = payload["expectation"]
owner = payload["owner"]
receipt = expectation["Receipt"]
columns = (
    "restore_epoch", "dirty_generation", "verified_generation", "verified_backup_id",
    "verified_object_key", "verified_object_sha256", "verified_object_version",
    "verified_size_bytes", "verified_manifest_version", "verified_at_unix",
    "last_attempt_sequence", "phase", "updated_at_unix",
)
field_names = (
    "RestoreEpoch", "DirtyGeneration", "VerifiedGeneration", "VerifiedBackupID",
    "VerifiedObjectKey", "VerifiedObjectSHA256", "VerifiedObjectVersion",
    "VerifiedSizeBytes", "VerifiedManifestVersion", "VerifiedAtUnix",
    "LastAttemptSequence", "Phase", "UpdatedAtUnix",
)

def values(state):
    return tuple(state[name] for name in field_names)

baseline_values = values(baseline)
expected = dict(baseline)
expected["DirtyGeneration"] += expectation["DirtyGenerationDelta"]
expected["Phase"] = "dirty"
expected["UpdatedAtUnix"] = expectation["UpdatedAtUnix"]
expected_values = values(expected)

def set_state(state_values):
    connection.execute(
        "UPDATE backup_rpo_state SET " + ",".join(name + "=?" for name in columns) +
        " WHERE singleton_id=1", state_values,
    )

def current_state():
    return connection.execute(
        "SELECT " + ",".join(columns) + " FROM backup_rpo_state WHERE singleton_id=1"
    ).fetchone()

def reset_owned():
    set_state(expected_values)
    connection.execute("DELETE FROM cluster_job_leases WHERE job_name=?", (owner["JobName"],))
    connection.execute(
        "INSERT INTO cluster_job_leases(job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix) "
        "VALUES(?,?,?,1999990,2000300)",
        (owner["JobName"], owner["HolderID"], owner["LeaseToken"]),
    )
    connection.execute("DELETE FROM import_runs WHERE import_run_id=?", (receipt["RunID"],))
    connection.execute(
        "INSERT INTO import_runs(import_run_id,snapshot_kind,source_sha256,plan_sha256,parent_source_sha256,"
        "target_sha256,batch_count,status,started_at_unix,completed_at_unix) "
        "VALUES(?,?,?,?,NULL,?,1,?,1499990,1500000)",
        (receipt["RunID"], "full", receipt["SourceDigest"], receipt["PlanDigest"], "f" * 64, receipt["Status"]),
    )

def cleanup_rows():
    return connection.execute(payload["cleanup"]["SQL"], payload["cleanup"].get("Args") or []).fetchall()

reset_owned()
owned_rows = cleanup_rows()
owned_restored = current_state() == baseline_values
blocked = {}
for scenario in ("concurrent_dirty", "verified", "unexpected_attempt", "wrong_lease", "missing_receipt"):
    reset_owned()
    if scenario == "concurrent_dirty":
        state = list(expected_values); state[1] += 1; state[12] += 1; set_state(tuple(state))
    elif scenario == "verified":
        state = list(expected_values); state[2] = state[1]; state[11] = "verified"; state[12] += 1; set_state(tuple(state))
    elif scenario == "unexpected_attempt":
        state = list(expected_values); state[10] += 1; set_state(tuple(state))
    elif scenario == "wrong_lease":
        connection.execute("UPDATE cluster_job_leases SET lease_token=? WHERE job_name=?", ("0" * 64, owner["JobName"]))
    elif scenario == "missing_receipt":
        connection.execute("DELETE FROM import_runs WHERE import_run_id=?", (receipt["RunID"],))
    before = current_state()
    rows = cleanup_rows()
    blocked[scenario] = len(rows) == 0 and current_state() == before

print(json.dumps({"owned_rows": len(owned_rows), "owned_restored": owned_restored, "blocked": blocked}))
`
