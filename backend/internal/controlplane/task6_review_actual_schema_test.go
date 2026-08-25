package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type task6ReviewSQLiteResult struct {
	FirstError          string `json:"first_error"`
	ReplayError         string `json:"replay_error"`
	RestoreError        string `json:"restore_error"`
	MutationError       string `json:"mutation_error"`
	CustomerStatus      string `json:"customer_status"`
	CustomerGeneration  int64  `json:"customer_generation"`
	FirstTargetCount    int64  `json:"first_target_count"`
	FrozenTargetCount   int64  `json:"frozen_target_count"`
	LateTargetCount     int64  `json:"late_target_count"`
	DesiredCount        int64  `json:"desired_count"`
	OutboxCount         int64  `json:"outbox_count"`
	DirtyGeneration     int64  `json:"dirty_generation"`
	TombstoneCount      int64  `json:"tombstone_count"`
	ExistingGeneration  int64  `json:"existing_generation"`
	ReplayChanges       int64  `json:"replay_changes"`
	ClusterRestoreEpoch int64  `json:"cluster_restore_epoch"`
	ClusterActivated    int64  `json:"cluster_activated"`
	BackupRestoreEpoch  int64  `json:"backup_restore_epoch"`
	AttemptPhase        string `json:"attempt_phase"`
	NodeLeaseCount      int64  `json:"node_lease_count"`
	JobLeaseCount       int64  `json:"job_lease_count"`
	PollerLeaseToken    string `json:"poller_lease_token"`
	ContaminatedDirty   int64  `json:"contaminated_dirty_generation"`
	ContaminatedSeeds   int64  `json:"contaminated_seed_count"`
	FreshRestoreEpoch   int64  `json:"fresh_restore_epoch"`
	FreshDirty          int64  `json:"fresh_dirty_generation"`
	FreshVerified       int64  `json:"fresh_verified_generation"`
	FreshLastAttempt    int64  `json:"fresh_last_attempt_sequence"`
	FreshPhase          string `json:"fresh_phase"`
	FreshSeeds          int64  `json:"fresh_seed_count"`
}

// Break caught: a schema-invalid customer state or a replay gate backed by the
// durable tombstone would either roll back the first delete or expand its frozen
// target set when topology changes later.
func TestCreateCustomerTombstoneDeletedStateAndReplayAreExactSQLite(t *testing.T) {
	result := executeTask6ReviewSQLite(t, "tombstone", captureTask6ReviewTombstoneStatements(t))
	if result.FirstError != "" || result.ReplayError != "" {
		t.Fatalf("tombstone transaction errors: first=%q replay=%q", result.FirstError, result.ReplayError)
	}
	if result.CustomerStatus != "deleted" || result.CustomerGeneration != 9 {
		t.Fatalf("customer state=(%q,%d), want (deleted,9)", result.CustomerStatus, result.CustomerGeneration)
	}
	if result.FirstTargetCount != 4 || result.FrozenTargetCount != 4 || result.LateTargetCount != 0 {
		t.Fatalf("target counts=(first=%d frozen=%d late=%d), want (4,4,0)", result.FirstTargetCount, result.FrozenTargetCount, result.LateTargetCount)
	}
	if result.DesiredCount != 4 || result.OutboxCount != 4 {
		t.Fatalf("propagation counts=(desired=%d outbox=%d), want (4,4)", result.DesiredCount, result.OutboxCount)
	}
	if result.DirtyGeneration != 2 || result.ReplayChanges != 0 {
		t.Fatalf("replay state=(dirty=%d changes=%d), want (2,0)", result.DirtyGeneration, result.ReplayChanges)
	}
}

// Break caught: the customer/tombstone prefix must not commit when the frozen
// target set is empty, even though Go detects the zero-target conflict later.
func TestCreateCustomerTombstoneZeroEligibleTargetsRollsBackSQLite(t *testing.T) {
	result := executeTask6ReviewSQLite(t, "tombstone_zero_targets", captureTask6ReviewTombstoneStatements(t))
	if result.FirstError == "" {
		t.Fatal("zero-target tombstone committed instead of aborting the transaction")
	}
	if result.CustomerStatus != "active" || result.CustomerGeneration != 1 || result.TombstoneCount != 0 {
		t.Fatalf("rollback customer/tombstone=(%q,%d,%d), want (active,1,0)", result.CustomerStatus, result.CustomerGeneration, result.TombstoneCount)
	}
	if result.DesiredCount != 0 || result.OutboxCount != 0 || result.DirtyGeneration != 1 {
		t.Fatalf("rollback propagation=(desired=%d outbox=%d dirty=%d), want (0,0,1)", result.DesiredCount, result.OutboxCount, result.DirtyGeneration)
	}
}

// Break caught: an older tombstone generation must not partially commit around
// a target whose desired generation is already newer than the command.
func TestCreateCustomerTombstoneDesiredGenerationConflictRollsBackSQLite(t *testing.T) {
	result := executeTask6ReviewSQLite(t, "tombstone_desired_conflict", captureTask6ReviewTombstoneStatements(t))
	if result.FirstError == "" {
		t.Fatal("desired-generation conflict committed instead of aborting the transaction")
	}
	if result.CustomerStatus != "active" || result.CustomerGeneration != 1 || result.TombstoneCount != 0 {
		t.Fatalf("rollback customer/tombstone=(%q,%d,%d), want (active,1,0)", result.CustomerStatus, result.CustomerGeneration, result.TombstoneCount)
	}
	if result.ExistingGeneration != 10 {
		t.Fatalf("pre-existing desired generation=%d, want 10", result.ExistingGeneration)
	}
	if result.DesiredCount != 0 || result.OutboxCount != 0 || result.DirtyGeneration != 1 {
		t.Fatalf("rollback propagation=(desired=%d outbox=%d dirty=%d), want (0,0,1)", result.DesiredCount, result.OutboxCount, result.DirtyGeneration)
	}
}

// Break caught: advancing the cluster row before proving the backup singleton
// epoch lets a mismatched handoff commit while invalidating attempts and leases.
func TestAdvanceAfterRestoreBackupEpochMismatchRollsBackSQLite(t *testing.T) {
	result := executeTask6ReviewSQLite(t, "restore_mismatch", captureTask6ReviewRestoreStatements(t))
	if result.RestoreError == "" {
		t.Fatal("backup epoch mismatch committed instead of rolling back")
	}
	if result.ClusterRestoreEpoch != 1 || result.ClusterActivated != 1 || result.BackupRestoreEpoch != 2 {
		t.Fatalf("restore rows=(cluster=%d active=%d backup=%d), want (1,1,2)", result.ClusterRestoreEpoch, result.ClusterActivated, result.BackupRestoreEpoch)
	}
	if result.AttemptPhase != "pending" || result.NodeLeaseCount != 1 || result.JobLeaseCount != 1 || result.PollerLeaseToken != "old-poller-token" {
		t.Fatalf("rollback evidence=(attempt=%q node=%d job=%d poller=%q)", result.AttemptPhase, result.NodeLeaseCount, result.JobLeaseCount, result.PollerLeaseToken)
	}
}

// Break caught: the live schema package first purges a tombstone, which is a
// durable business mutation and therefore advances backup_rpo_state from the
// migration seed. Seed verification must execute the real migrations in a
// separate fresh database instead of depending on shared test order.
func TestBackupRPOMigrationSeedProofIsFreshAfterPurgeMutationSQLite(t *testing.T) {
	result := executeTask6ReviewSQLite(t, "purge_seed_order", captureTask6ReviewPurgeStatements(t))
	if result.MutationError != "" {
		t.Fatalf("purge mutation failed: %s", result.MutationError)
	}
	if result.ContaminatedDirty != 2 || result.ContaminatedSeeds != 0 {
		t.Fatalf("contaminated state=(dirty=%d seeds=%d), want (2,0)", result.ContaminatedDirty, result.ContaminatedSeeds)
	}
	if result.FreshRestoreEpoch != 1 || result.FreshDirty != 1 || result.FreshVerified != 0 ||
		result.FreshLastAttempt != 0 || result.FreshPhase != "dirty" || result.FreshSeeds != 1 {
		t.Fatalf("fresh seed=(epoch=%d dirty=%d verified=%d last=%d phase=%q count=%d), want (1,1,0,0,dirty,1)",
			result.FreshRestoreEpoch, result.FreshDirty, result.FreshVerified,
			result.FreshLastAttempt, result.FreshPhase, result.FreshSeeds)
	}
}

func captureTask6ReviewTombstoneStatements(t *testing.T) []rqlite.Statement {
	t.Helper()
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	service, _ := testService(t, db)
	_, _ = service.CreateCustomerTombstone(context.Background(), CustomerTombstoneCommand{
		TombstoneID: "tombstone-review", CustomerID: "customer-review",
		Generation: 9, Reason: "owner-delete",
	})
	if len(db.requestCalls) != 1 {
		t.Fatalf("captured tombstone requests=%d, want 1", len(db.requestCalls))
	}
	return append([]rqlite.Statement(nil), db.requestCalls[0].statements...)
}

func captureTask6ReviewRestoreStatements(t *testing.T) []rqlite.Statement {
	t.Helper()
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
	)}}
	_, _ = NewRestoreEpochStore(db).AdvanceAfterRestore(context.Background(), 1, strings.Repeat("f", 64))
	if len(db.requestCalls) != 1 {
		t.Fatalf("captured restore requests=%d, want 1", len(db.requestCalls))
	}
	return append([]rqlite.Statement(nil), db.requestCalls[0].statements...)
}

func captureTask6ReviewPurgeStatements(t *testing.T) []rqlite.Statement {
	t.Helper()
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{Rows: []map[string]any{{"dirty_generation": int64(2)}}},
	)}}
	service, _ := testService(t, db)
	if err := service.PurgeTombstone(context.Background(), TombstonePurgeCommand{
		TombstoneID: "whitelist-purge-tombstone", CustomerID: "whitelist-purge-customer",
	}); err != nil {
		t.Fatalf("capture PurgeTombstone: %v", err)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("captured purge requests=%d, want 1", len(db.requestCalls))
	}
	return append([]rqlite.Statement(nil), db.requestCalls[0].statements...)
}

func executeTask6ReviewSQLite(t *testing.T, mode string, statements []rqlite.Statement) task6ReviewSQLiteResult {
	t.Helper()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	schema := make([]sqliteStatementPayload, 0)
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			schema = append(schema, sqliteStatementPayload{SQL: statement.SQL, Args: statement.Args})
		}
	}
	transaction := make([]sqliteStatementPayload, len(statements))
	for index, statement := range statements {
		transaction[index] = sqliteStatementPayload{SQL: statement.SQL, Args: statement.Args}
	}
	payload, err := json.Marshal(map[string]any{"mode": mode, "schema": schema, "transaction": transaction})
	if err != nil {
		t.Fatalf("encode review SQLite payload: %v", err)
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatalf("working python sqlite3 is required: %v", err)
	}
	command := exec.Command(python, "-c", task6ReviewSQLiteProgram)
	command.Stdin = bytes.NewReader(payload)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("execute review SQLite proof: %v: %s", commandErr, output)
	}
	var result task6ReviewSQLiteResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode review SQLite proof: %v: %s", err, output)
	}
	return result
}

const task6ReviewSQLiteProgram = `
import copy
import json
import sqlite3
import sys

payload = json.load(sys.stdin)
connection = sqlite3.connect(":memory:", isolation_level=None)
connection.create_function("unixepoch", 0, lambda: 2000000)
connection.execute("PRAGMA foreign_keys=ON")
for statement in payload["schema"]:
    connection.execute(statement["sql"], statement.get("args") or []).fetchall()

def run_transaction(statements=None):
    connection.execute("BEGIN IMMEDIATE")
    try:
        for statement in statements or payload["transaction"]:
            connection.execute(statement["sql"], statement.get("args") or []).fetchall()
        connection.commit()
        return ""
    except Exception as error:
        connection.rollback()
        return str(error)

def backup_seed_rows(database):
    return database.execute(
        "SELECT backup.restore_epoch,backup.dirty_generation,backup.verified_generation,"
        "backup.last_attempt_sequence,backup.phase FROM backup_rpo_state AS backup "
        "JOIN cluster_restore_state AS restore ON restore.restore_epoch=backup.restore_epoch "
        "WHERE backup.singleton_id=1 AND backup.dirty_generation=1 "
        "AND backup.verified_generation=0 AND backup.last_attempt_sequence=0 "
        "AND backup.phase='dirty' AND backup.verified_backup_id IS NULL "
        "AND backup.verified_object_key IS NULL AND backup.verified_object_sha256 IS NULL "
        "AND backup.verified_object_version IS NULL AND backup.verified_size_bytes IS NULL "
        "AND backup.verified_manifest_version IS NULL AND backup.verified_at_unix IS NULL"
    ).fetchall()

def fresh_database():
    fresh = sqlite3.connect(":memory:", isolation_level=None)
    fresh.create_function("unixepoch", 0, lambda: 2000000)
    fresh.execute("PRAGMA foreign_keys=ON")
    for statement in payload["schema"]:
        fresh.execute(statement["sql"], statement.get("args") or []).fetchall()
    return fresh

if payload["mode"] == "tombstone":
    connection.execute(
        "INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) "
        "VALUES ('customer-review','customer-review',?,'active',100,1,1,1)",
        ("9" * 64,),
    )
    first_error = run_transaction()
    first_targets = connection.execute(
        "SELECT COUNT(*) FROM tombstone_targets WHERE tombstone_id='tombstone-review'"
    ).fetchone()[0]
    connection.execute(
        "INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES ('late-node','late-node',0,1,2)"
    )
    connection.execute(
        "INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix) "
        "VALUES ('late-node','maestro-core',1,1,0,0,2)"
    )
    replay_before = connection.total_changes
    replay_transaction = copy.deepcopy(payload["transaction"])
    replay_transaction[3]["args"][2] = "fresh-replay-envelope"
    replay_transaction[3]["args"][3] = "7" * 64
    abort_args = replay_transaction[6].get("args") or []
    if len(abort_args) > 5:
        abort_args[-2] = "fresh-replay-envelope"
        abort_args[-1] = "7" * 64
    replay_error = run_transaction(replay_transaction)
    replay_changes = connection.total_changes - replay_before
    customer = connection.execute(
        "SELECT status,generation FROM customers WHERE customer_id='customer-review'"
    ).fetchone()
    result = {
        "first_error": first_error,
        "replay_error": replay_error,
        "customer_status": customer[0],
        "customer_generation": customer[1],
        "first_target_count": first_targets,
        "frozen_target_count": connection.execute("SELECT COUNT(*) FROM tombstone_targets WHERE tombstone_id='tombstone-review'").fetchone()[0],
        "late_target_count": connection.execute("SELECT COUNT(*) FROM tombstone_targets WHERE tombstone_id='tombstone-review' AND node_id='late-node'").fetchone()[0],
        "desired_count": connection.execute("SELECT COUNT(*) FROM desired_node_state WHERE operation_id='tombstone-review'").fetchone()[0],
        "outbox_count": connection.execute("SELECT COUNT(*) FROM outbox_events WHERE operation_id='tombstone-review'").fetchone()[0],
        "dirty_generation": connection.execute("SELECT dirty_generation FROM backup_rpo_state WHERE singleton_id=1").fetchone()[0],
        "replay_changes": replay_changes,
    }
elif payload["mode"] in ("tombstone_zero_targets", "tombstone_desired_conflict"):
    connection.execute(
        "INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) "
        "VALUES ('customer-review','customer-review',?,'active',100,1,1,1)",
        ("9" * 64,),
    )
    if payload["mode"] == "tombstone_zero_targets":
        connection.execute(
            "UPDATE node_services SET desired_target=0,apply_enabled=0 WHERE desired_target=1"
        )
    else:
        connection.execute(
            "INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,"
            "updated_at_unix,tombstone,operation_id) VALUES ('customer-review','S1','maestro-core',10,?,?,'pending',1,0,?)",
            (b"pre-existing", "8" * 64, "pre-existing-operation"),
        )
    first_error = run_transaction()
    customer = connection.execute(
        "SELECT status,generation FROM customers WHERE customer_id='customer-review'"
    ).fetchone()
    existing = connection.execute(
        "SELECT generation FROM desired_node_state WHERE customer_id='customer-review' AND node_id='S1' AND service_name='maestro-core'"
    ).fetchone()
    result = {
        "first_error": first_error,
        "customer_status": customer[0],
        "customer_generation": customer[1],
        "tombstone_count": connection.execute("SELECT COUNT(*) FROM tombstones WHERE tombstone_id='tombstone-review'").fetchone()[0],
        "desired_count": connection.execute("SELECT COUNT(*) FROM desired_node_state WHERE operation_id='tombstone-review'").fetchone()[0],
        "outbox_count": connection.execute("SELECT COUNT(*) FROM outbox_events WHERE operation_id='tombstone-review'").fetchone()[0],
        "dirty_generation": connection.execute("SELECT dirty_generation FROM backup_rpo_state WHERE singleton_id=1").fetchone()[0],
        "existing_generation": existing[0] if existing else 0,
    }
elif payload["mode"] in ("fresh_backup_rpo_seed", "purge_seed_order"):
    mutation_error = ""
    if payload["mode"] == "purge_seed_order":
        connection.create_function("unixepoch", 0, lambda: 10000000)
        connection.execute(
            "INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) "
            "VALUES ('whitelist-purge-customer','WhitelistPurge',?,'active',2100000,1,1000000,1000000)",
            ("cd" * 32,),
        )
        connection.execute(
            "INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) "
            "VALUES ('wl-ent-00000000000000000000000000000001','whitelist-purge-customer',1000000)"
        )
        connection.execute(
            "INSERT INTO tombstones(tombstone_id,customer_id,generation,reason,created_at_unix) "
            "VALUES ('whitelist-purge-tombstone','whitelist-purge-customer',1,'test-purge',1)"
        )
        connection.execute(
            "INSERT INTO tombstone_targets(tombstone_id,node_id,service_name,status,applied_at_unix) "
            "SELECT 'whitelist-purge-tombstone',node_id,service_name,'applied',1 FROM node_services "
            "WHERE desired_target=1 AND retired=0"
        )
        mutation_error = run_transaction()
    contaminated_dirty = connection.execute(
        "SELECT dirty_generation FROM backup_rpo_state WHERE singleton_id=1"
    ).fetchone()[0]
    contaminated_seeds = len(backup_seed_rows(connection))
    fresh = fresh_database()
    fresh_rows = backup_seed_rows(fresh)
    fresh_state = fresh.execute(
        "SELECT restore_epoch,dirty_generation,verified_generation,last_attempt_sequence,phase "
        "FROM backup_rpo_state WHERE singleton_id=1"
    ).fetchone()
    result = {
        "mutation_error": mutation_error,
        "contaminated_dirty_generation": contaminated_dirty,
        "contaminated_seed_count": contaminated_seeds,
        "fresh_restore_epoch": fresh_state[0],
        "fresh_dirty_generation": fresh_state[1],
        "fresh_verified_generation": fresh_state[2],
        "fresh_last_attempt_sequence": fresh_state[3],
        "fresh_phase": fresh_state[4],
        "fresh_seed_count": len(fresh_rows),
    }
else:
    connection.execute(
        "INSERT INTO backup_rpo_attempts(restore_epoch,attempt_sequence,phase,backup_id,captured_generation,object_key,"
        "object_sha256,object_version,object_size_bytes,manifest_version,adapter_contract_version,capability_generation,"
        "capability_evidence_sha256,capability_expires_at_unix,lease_holder_id,lease_token,lease_fence,failure_code,"
        "created_at_unix,updated_at_unix) VALUES (1,1,'pending',?,1,'restore/review.db',?,NULL,1,2,'yandex-s3-v1',1,"
        "?,9999999999,'holder','attempt-token',1,NULL,1,1)",
        ("a" * 32, "b" * 64, "c" * 64),
    )
    connection.execute("UPDATE backup_rpo_state SET restore_epoch=2,last_attempt_sequence=1 WHERE singleton_id=1")
    connection.execute(
        "INSERT INTO node_leases(node_id,service_name,holder_id,lease_token,acquired_at_unix,expires_at_unix) "
        "VALUES ('S1','maestro-core','holder','old-node-token',1,9999999999)"
    )
    connection.execute(
        "INSERT INTO cluster_job_leases(job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix) "
        "VALUES ('restore-review','holder','old-job-token',1,9999999999)"
    )
    connection.execute(
        "INSERT INTO telegram_bot_routes(bot_identity_hmac,token_fingerprint_hmac,credential_version,schema_fingerprint,updated_at_unix) "
        "VALUES (?,?,1,'restore-review',1)",
        ("d" * 64, "e" * 64),
    )
    connection.execute(
        "INSERT INTO telegram_pollers(bot_identity_hmac,node_id,lease_token,offset_value,lease_fence,lease_expires_at_unix,updated_at_unix) "
        "VALUES (?,'S1','old-poller-token',1,1,9999999999,1)",
        ("d" * 64,),
    )
    restore_error = run_transaction()
    cluster = connection.execute("SELECT restore_epoch,activated FROM cluster_restore_state WHERE singleton_id=1").fetchone()
    result = {
        "restore_error": restore_error,
        "cluster_restore_epoch": cluster[0],
        "cluster_activated": cluster[1],
        "backup_restore_epoch": connection.execute("SELECT restore_epoch FROM backup_rpo_state WHERE singleton_id=1").fetchone()[0],
        "attempt_phase": connection.execute("SELECT phase FROM backup_rpo_attempts WHERE restore_epoch=1 AND attempt_sequence=1").fetchone()[0],
        "node_lease_count": connection.execute("SELECT COUNT(*) FROM node_leases WHERE lease_token='old-node-token'").fetchone()[0],
        "job_lease_count": connection.execute("SELECT COUNT(*) FROM cluster_job_leases WHERE lease_token='old-job-token'").fetchone()[0],
        "poller_lease_token": connection.execute("SELECT lease_token FROM telegram_pollers WHERE bot_identity_hmac=?", ("d" * 64,)).fetchone()[0],
    }

json.dump(result, sys.stdout, sort_keys=True, separators=(",", ":"))
`
