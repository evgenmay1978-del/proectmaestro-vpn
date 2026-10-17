package controlplane

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type sqliteBackupRPOResult struct {
	Error              string `json:"error"`
	DirtyGeneration    int64  `json:"dirty_generation"`
	VerifiedGeneration int64  `json:"verified_generation"`
	Phase              string `json:"phase"`
}

func TestBackupRPODirtyGenerationStatementTransitionsVerifiedStateSQLite(t *testing.T) {
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
	transaction := []rqlite.Statement{
		{
			SQL:  "UPDATE cluster_settings SET public_value_json=?,generation=generation+1,updated_at_unix=? WHERE setting_key=?",
			Args: []any{`{"owner":"updated"}`, int64(123), "task5-verified-setting"},
		},
		backupRPODirtyGenerationStatement(123),
	}
	payloadTransaction := make([]sqliteStatementPayload, len(transaction))
	for index, statement := range transaction {
		payloadTransaction[index] = sqliteStatementPayload{SQL: statement.SQL, Args: statement.Args}
	}
	payload, err := json.Marshal(map[string]any{
		"schema":      schema,
		"transaction": payloadTransaction,
	})
	if err != nil {
		t.Fatalf("encode SQLite payload: %v", err)
	}

	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatalf("working python sqlite3 is required: %v", err)
	}
	command := exec.Command(python, "-c", sqliteBackupRPOActualSchemaProgram)
	command.Stdin = bytes.NewReader(payload)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("execute SQLite proof: %v: %s", commandErr, output)
	}
	var result sqliteBackupRPOResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode SQLite proof: %v: %s", err, output)
	}
	if result.Error != "" {
		t.Fatalf("verified-state mutation rolled back: %s", result.Error)
	}
	if result.DirtyGeneration != 2 || result.VerifiedGeneration != 1 || result.Phase != "dirty" {
		t.Fatalf("backup state = (%d,%d,%q), want (2,1,%q)", result.DirtyGeneration, result.VerifiedGeneration, result.Phase, "dirty")
	}
}

const sqliteBackupRPOActualSchemaProgram = `
import json
import sqlite3
import sys

payload = json.load(sys.stdin)
connection = sqlite3.connect(":memory:", isolation_level=None)
connection.execute("PRAGMA foreign_keys=ON")
for statement in payload["schema"]:
    connection.execute(statement["sql"], statement.get("args") or []).fetchall()

connection.execute(
    "INSERT INTO cluster_settings(setting_key,public_value_json,generation,updated_at_unix,last_mutation_token) "
    "VALUES (?,?,?,?,?)",
    ("task5-verified-setting", '{"owner":"seed"}', 1, 1, "seed-token"),
)
connection.execute(
    "UPDATE backup_rpo_state SET dirty_generation=1,verified_generation=1,"
    "verified_backup_id=?,verified_object_key=?,verified_object_sha256=?,verified_object_version=?,"
    "verified_size_bytes=1,verified_manifest_version=2,verified_at_unix=1,"
    "last_attempt_sequence=1,phase='verified',updated_at_unix=1 WHERE singleton_id=1",
    ("1" * 32, "task5/verified.db", "2" * 64, "version-1"),
)

connection.execute("BEGIN IMMEDIATE")
failure = ""
try:
    for statement in payload["transaction"]:
        connection.execute(statement["sql"], statement.get("args") or []).fetchall()
    connection.commit()
except Exception as error:
    connection.rollback()
    failure = str(error)

dirty, verified, phase = connection.execute(
    "SELECT dirty_generation,verified_generation,phase FROM backup_rpo_state WHERE singleton_id=1"
).fetchone()
json.dump(
    {
        "error": failure,
        "dirty_generation": dirty,
        "verified_generation": verified,
        "phase": phase,
    },
    sys.stdout,
    sort_keys=True,
    separators=(",", ":"),
)
`
