package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type sqliteBackupRPOFlowResult struct {
	Error              string `json:"error"`
	DirtyGeneration    int64  `json:"dirty_generation"`
	VerifiedGeneration int64  `json:"verified_generation"`
	Phase              string `json:"phase"`
	AuditCount         int64  `json:"audit_count"`
	EntityValue        int64  `json:"entity_value"`
	RelatedValue       int64  `json:"related_value"`
}

func TestCoreMutationFlowsHonorBackupRPOStateSQLite(t *testing.T) {
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

	for _, flow := range []string{"claim", "setting", "revoke", "whitelist", "desired"} {
		statements := captureBackupRPOFlowStatements(t, flow)
		for _, state := range []string{"dirty", "verified", "inactive", "mismatched", "noop"} {
			t.Run(flow+"/"+state, func(t *testing.T) {
				result := executeBackupRPOFlowSQLite(t, schema, statements, flow, state)
				if result.Error != "" {
					t.Fatalf("transaction rolled back: %s", result.Error)
				}

				wantDirty, wantVerified, wantPhase := int64(1), int64(1), "verified"
				if state == "dirty" {
					wantDirty, wantVerified, wantPhase = 2, 0, "dirty"
				} else if state == "verified" {
					wantDirty, wantVerified, wantPhase = 2, 1, "dirty"
				}
				if result.DirtyGeneration != wantDirty || result.VerifiedGeneration != wantVerified || result.Phase != wantPhase {
					t.Fatalf("backup state = (%d,%d,%q), want (%d,%d,%q)", result.DirtyGeneration, result.VerifiedGeneration, result.Phase, wantDirty, wantVerified, wantPhase)
				}

				if state == "noop" {
					wantEntity := map[string]int64{"claim": 3, "setting": 2, "revoke": -1, "whitelist": 1, "desired": 1}[flow]
					if result.AuditCount != 0 || result.EntityValue != wantEntity ||
						(flow == "desired" && result.RelatedValue != 1) {
						t.Fatalf("no-op state = (audit=%d,entity=%d), want (0,%d)", result.AuditCount, result.EntityValue, wantEntity)
					}
					return
				}

				wantAudit := int64(1)
				if flow == "whitelist" || flow == "desired" {
					wantAudit = 0
				}
				wantEntity := map[string]int64{"claim": 1, "setting": 2, "revoke": 1, "whitelist": 1, "desired": 1}[flow]
				if result.AuditCount != wantAudit || result.EntityValue != wantEntity {
					t.Fatalf("business state = (audit=%d,entity=%d), want (%d,%d)", result.AuditCount, result.EntityValue, wantAudit, wantEntity)
				}
				if flow == "desired" && result.RelatedValue != 1 {
					t.Fatalf("desired outbox count=%d, want 1", result.RelatedValue)
				}
			})
		}
	}
}

func captureBackupRPOFlowStatements(t *testing.T, flow string) []rqlite.Statement {
	t.Helper()
	var db *recordingRQLite
	switch flow {
	case "claim":
		db = &recordingRQLite{requests: []scriptedResult{resultsScript(
			rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1},
			rqlite.Result{Rows: []map[string]any{{"device_id": "device-1"}}},
		)}}
		service, _ := testService(t, db)
		if _, err := service.ClaimDevice(context.Background(), "customer-1", "task5-matrix-device", "android", 3); err != nil {
			t.Fatalf("capture ClaimDevice: %v", err)
		}
	case "setting":
		db = &recordingRQLite{requests: []scriptedResult{resultsScript(
			rqlite.Result{Rows: []map[string]any{{"generation": int64(2)}}},
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		)}}
		service, _ := testService(t, db)
		if _, err := service.UpdateSetting(context.Background(), SettingUpdate{
			Key: "task5-matrix-setting", ExpectedGeneration: 1,
			PublicValueJSON: `{"owner":"updated"}`, Actor: "task5-matrix-actor",
		}); err != nil {
			t.Fatalf("capture UpdateSetting: %v", err)
		}
	case "revoke":
		db = &recordingRQLite{requests: []scriptedResult{resultsScript(
			rqlite.Result{Rows: []map[string]any{{"revocation_epoch": int64(1)}}},
			rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1}, rqlite.Result{RowsAffected: 1},
		)}}
		service, _ := testService(t, db)
		if err := service.RevokeSessions(context.Background(), "principal-1", "task5-matrix-actor"); err != nil {
			t.Fatalf("capture RevokeSessions: %v", err)
		}
	case "whitelist":
		db = &recordingRQLite{requests: []scriptedResult{
			entitlementIdentityRows("account-a", task7PersistedEntitlementID, 1),
		}}
		service, _ := testService(t, db)
		if _, err := service.EnsureWhiteListEntitlement(context.Background(), "account-a"); err != nil {
			t.Fatalf("capture EnsureWhiteListEntitlement: %v", err)
		}
	case "desired":
		db = &recordingRQLite{requests: []scriptedResult{resultsScript(
			rqlite.Result{Rows: []map[string]any{{"generation": int64(5), "desired_sha256": testDesiredSHA}}},
			rqlite.Result{RowsAffected: 1}, rqlite.Result{},
			rqlite.Result{Rows: []map[string]any{task6DesiredEvidence()}},
		)}}
		service, _ := testService(t, db)
		if err := service.UpsertDesired(context.Background(), desiredFixture(5, testDesiredSHA)); err != nil {
			t.Fatalf("capture UpsertDesired: %v", err)
		}
	default:
		t.Fatalf("unknown flow %q", flow)
	}
	if len(db.requestCalls) != 1 {
		t.Fatalf("%s request calls = %d, want 1", flow, len(db.requestCalls))
	}
	return append([]rqlite.Statement(nil), db.requestCalls[0].statements...)
}

func executeBackupRPOFlowSQLite(
	t *testing.T,
	schema []sqliteStatementPayload,
	statements []rqlite.Statement,
	flow string,
	state string,
) sqliteBackupRPOFlowResult {
	t.Helper()
	transaction := make([]sqliteStatementPayload, len(statements))
	for index, statement := range statements {
		transaction[index] = sqliteStatementPayload{SQL: statement.SQL, Args: statement.Args}
	}
	payload, err := json.Marshal(map[string]any{
		"schema": schema, "transaction": transaction, "flow": flow, "state": state,
	})
	if err != nil {
		t.Fatalf("encode SQLite flow payload: %v", err)
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatalf("working python sqlite3 is required: %v", err)
	}
	command := exec.Command(python, "-c", sqliteBackupRPOFlowProgram)
	command.Stdin = bytes.NewReader(payload)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("execute SQLite flow proof: %v: %s", commandErr, output)
	}
	var result sqliteBackupRPOFlowResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode SQLite flow proof: %v: %s", err, output)
	}
	return result
}

const sqliteBackupRPOFlowProgram = `
import json
import sqlite3
import sys

payload = json.load(sys.stdin)
flow = payload["flow"]
state = payload["state"]
transaction = payload["transaction"]
connection = sqlite3.connect(":memory:", isolation_level=None)
connection.execute("PRAGMA foreign_keys=ON")
for statement in payload["schema"]:
    connection.execute(statement["sql"], statement.get("args") or []).fetchall()

def seed_customer(customer_id):
    connection.execute(
        "INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) "
        "VALUES (?,?,?,'active',100,1,1,1)",
        (customer_id, customer_id, "a" * 64),
    )

if flow == "claim":
    seed_customer("customer-1")
    if state == "noop":
        for index in range(3):
            connection.execute(
                "INSERT INTO devices(device_id,customer_id,device_key_hmac,platform,last_seen_at_unix,revoked,created_at_unix) "
                "VALUES (?,?,?,'android',1,0,1)",
                ("existing-device-" + str(index), "customer-1", format(index + 1, "064x")),
            )
elif flow == "setting":
    generation = 2 if state == "noop" else 1
    connection.execute(
        "INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix,last_mutation_token) "
        "VALUES (?,?,?,?,?)",
        ("task5-matrix-setting", '{"owner":"seed"}', generation, 1, "seed-token"),
    )
elif flow == "revoke":
    if state != "noop":
        connection.execute(
            "INSERT INTO principals(principal_id,login_key_hmac,status,revocation_epoch,created_at_unix) "
            "VALUES ('principal-1',?,'active',0,1)",
            ("b" * 64,),
        )
        connection.execute(
            "INSERT INTO web_sessions(session_hmac,csrf_hmac,principal_id,revocation_epoch,created_at_unix,expires_at_unix) "
            "VALUES (?,?,'principal-1',0,1,100)",
            ("c" * 64, "d" * 64),
        )
elif flow == "whitelist":
    seed_customer("account-a")
    if state == "noop":
        connection.execute(
            "INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES (?,?,1)",
            (transaction[0]["args"][0], "account-a"),
        )
elif flow == "desired":
    seed_customer("customer-1")
    connection.execute(
        "INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES (?,?,?,?,?)",
        ("s2", "S2", 1, 1, 1),
    )
    connection.execute(
        "INSERT INTO node_services(node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix) "
        "VALUES (?,?,?,?,?,?,?)",
        ("s2", "xui", 1, 1, 0, 0, 1),
    )
    if state == "noop":
        desired_args = transaction[0]["args"]
        outbox_args = transaction[1]["args"]
        connection.execute(
            "INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,"
            "status,updated_at_unix,tombstone,operation_id) VALUES (?,?,?,?,?,?,'pending',?,?,?)",
            desired_args[:9],
        )
        connection.execute(
            "INSERT INTO outbox_events(event_id,aggregate_type,aggregate_id,generation,event_type,payload_envelope,"
            "payload_sha256,status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind) "
            "VALUES (?,'desired_node_state',?,?,?,?,?,'pending',?,0,?,?,?,?,?)",
            outbox_args[:12],
        )

if state != "dirty":
    connection.execute(
        "UPDATE backup_rpo_state SET dirty_generation=1,verified_generation=1,"
        "verified_backup_id=?,verified_object_key=?,verified_object_sha256=?,verified_object_version=?,"
        "verified_size_bytes=1,verified_manifest_version=2,verified_at_unix=1,"
        "last_attempt_sequence=1,phase='verified',updated_at_unix=1 WHERE singleton_id=1",
        ("1" * 32, "task5/verified.db", "2" * 64, "version-1"),
    )
if state == "inactive":
    connection.execute("UPDATE cluster_restore_state SET activated=0,activated_at_unix=NULL WHERE singleton_id=1")
elif state == "mismatched":
    connection.execute("UPDATE backup_rpo_state SET restore_epoch=restore_epoch+1 WHERE singleton_id=1")

connection.execute("BEGIN IMMEDIATE")
failure = ""
try:
    for statement in transaction:
        connection.execute(statement["sql"], statement.get("args") or []).fetchall()
    connection.commit()
except Exception as error:
    connection.rollback()
    failure = str(error)

dirty, verified, phase = connection.execute(
    "SELECT dirty_generation,verified_generation,phase FROM backup_rpo_state WHERE singleton_id=1"
).fetchone()
audit_count = connection.execute("SELECT COUNT(*) FROM audit_events").fetchone()[0]
if flow == "claim":
    entity_value = connection.execute("SELECT COUNT(*) FROM devices WHERE customer_id='customer-1'").fetchone()[0]
elif flow == "setting":
    entity_value = connection.execute("SELECT generation FROM cluster_settings WHERE setting_key='task5-matrix-setting'").fetchone()[0]
elif flow == "revoke":
    row = connection.execute("SELECT revocation_epoch FROM principals WHERE principal_id='principal-1'").fetchone()
    entity_value = row[0] if row else -1
elif flow == "whitelist":
    entity_value = connection.execute("SELECT COUNT(*) FROM whitelist_entitlement_identities WHERE customer_id='account-a'").fetchone()[0]
    related_value = -1
else:
    entity_value = connection.execute("SELECT COUNT(*) FROM desired_node_state WHERE customer_id='customer-1'").fetchone()[0]
    related_value = connection.execute("SELECT COUNT(*) FROM outbox_events WHERE operation_id='operation-1'").fetchone()[0]
if flow != "desired":
    related_value = -1

json.dump(
    {
        "error": failure,
        "dirty_generation": dirty,
        "verified_generation": verified,
        "phase": phase,
        "audit_count": audit_count,
        "entity_value": entity_value,
        "related_value": related_value,
    },
    sys.stdout,
    sort_keys=True,
    separators=(",", ":"),
)
`
