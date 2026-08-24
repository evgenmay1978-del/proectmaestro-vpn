package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type sqliteSettingClock struct {
	values []time.Time
	next   int
}

func (clock *sqliteSettingClock) Now() time.Time {
	if clock.next >= len(clock.values) {
		panic("sqlite setting test clock exhausted")
	}
	value := clock.values[clock.next]
	clock.next++
	return value
}

type sqliteStatementPayload struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args"`
}

func TestSettingFailedCASAtCommittedNextGenerationCannotMutateWinnerSQLite(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{
		resultsScript(
			rqlite.Result{Rows: []map[string]any{{"generation": int64(2)}}},
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		),
		resultsScript(
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
			rqlite.Result{}, rqlite.Result{}, rqlite.Result{},
		),
	}}
	clock := &sqliteSettingClock{values: []time.Time{
		time.Unix(2_000_001, 0),
		time.Unix(2_000_002, 0),
	}}
	secrets, err := NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{0x61}, 32)},
		bytes.Repeat([]byte{0x62}, 32),
	)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	store, err := NewStore(db, secrets, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := NewService(store, &sequenceIDs{}, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	const settingKey = "task5-sqlite-setting"
	winnerSecret, err := secrets.Seal(
		SecretScope{OwnerType: "setting", OwnerID: settingKey, Field: "token", Kind: "test-secret"},
		[]byte("winner-secret"),
	)
	if err != nil {
		t.Fatalf("Seal winner: %v", err)
	}
	loserSecret, err := secrets.Seal(
		SecretScope{OwnerType: "setting", OwnerID: settingKey, Field: "token", Kind: "test-secret"},
		[]byte("loser-secret"),
	)
	if err != nil {
		t.Fatalf("Seal loser: %v", err)
	}

	winner, err := service.UpdateSetting(context.Background(), SettingUpdate{
		Key: settingKey, ExpectedGeneration: 1, PublicValueJSON: `{"owner":"winner"}`,
		Members: []string{"winner-member"}, Secret: &winnerSecret, Actor: "winner-actor",
	})
	if err != nil || winner.Generation != 2 {
		t.Fatalf("winner result=%#v error=%v", winner, err)
	}
	_, err = service.UpdateSetting(context.Background(), SettingUpdate{
		Key: settingKey, ExpectedGeneration: 1, PublicValueJSON: `{"owner":"loser"}`,
		Members: []string{"loser-member"}, Secret: &loserSecret, Actor: "loser-actor",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("loser error=%v, want ErrConflict", err)
	}
	if len(db.requestCalls) != 2 {
		t.Fatalf("request calls=%d, want 2", len(db.requestCalls))
	}

	snapshots := executeSettingTransactionsSQLite(
		t,
		db.requestCalls[0].statements,
		db.requestCalls[1].statements,
	)
	if len(snapshots) != 2 {
		t.Fatalf("snapshots=%d, want 2", len(snapshots))
	}
	if !reflect.DeepEqual(snapshots[0]["dirty"], snapshots[1]["dirty"]) {
		t.Fatalf("failed CAS changed dirty generation: winner=%v loser=%v", snapshots[0]["dirty"], snapshots[1]["dirty"])
	}
	if !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		t.Fatalf("failed CAS mutated winner state or created loser audit:\nwinner=%v\nafter loser=%v", snapshots[0], snapshots[1])
	}
}

func TestSettingMutationTokenMigrationSixIsAdditive(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}

	if SchemaVersion != 6 {
		t.Fatalf("SchemaVersion = %d, want 6", SchemaVersion)
	}
	if len(migrations) != 6 {
		t.Fatalf("migration count = %d, want 6", len(migrations))
	}

	migration := migrations[5]
	if migration.Version != 6 || migration.Path != "migrations/0006_setting_mutation_token.sql" {
		t.Fatalf("migration 6 identity = (%d, %q)", migration.Version, migration.Path)
	}

	normalizedSQL := strings.Join(strings.Fields(strings.ToLower(string(migration.Data))), " ")
	for _, required := range []string{
		"alter table cluster_settings",
		"add column last_mutation_token text not null default ''",
	} {
		if !strings.Contains(normalizedSQL, required) {
			t.Fatalf("migration 6 missing %q: %s", required, normalizedSQL)
		}
	}
}

func executeSettingTransactionsSQLite(t *testing.T, transactions ...[]rqlite.Statement) []map[string]any {
	t.Helper()
	python := ""
	for _, candidate := range []string{"python", "python3"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if err := exec.Command(path, "-c", "import sqlite3").Run(); err == nil {
			python = path
			break
		}
	}
	if python == "" {
		t.Fatal("working python sqlite3 is required for the setting CAS regression")
	}

	payloadTransactions := make([][]sqliteStatementPayload, len(transactions))
	for transactionIndex, statements := range transactions {
		payloadTransactions[transactionIndex] = make([]sqliteStatementPayload, len(statements))
		for statementIndex, statement := range statements {
			payloadTransactions[transactionIndex][statementIndex] = sqliteStatementPayload{
				SQL: statement.SQL, Args: statement.Args,
			}
		}
	}
	payload, err := json.Marshal(map[string]any{"transactions": payloadTransactions})
	if err != nil {
		t.Fatalf("encode SQLite payload: %v", err)
	}
	command := exec.Command(python, "-c", sqliteSettingCASProgram)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute SQLite regression: %v: %s", err, output)
	}
	var snapshots []map[string]any
	if err := json.Unmarshal(output, &snapshots); err != nil {
		t.Fatalf("decode SQLite snapshots: %v: %s", err, output)
	}
	return snapshots
}

const sqliteSettingCASProgram = `
import json
import sqlite3
import sys

payload = json.load(sys.stdin)
connection = sqlite3.connect(":memory:", isolation_level=None)
connection.execute("PRAGMA foreign_keys=ON")
connection.executescript("""
CREATE TABLE cluster_restore_state (
    singleton_id INTEGER PRIMARY KEY,
    restore_epoch INTEGER NOT NULL,
    activated INTEGER NOT NULL
);
CREATE TABLE backup_rpo_state (
    singleton_id INTEGER PRIMARY KEY,
    restore_epoch INTEGER NOT NULL,
    dirty_generation INTEGER NOT NULL,
    verified_generation INTEGER NOT NULL,
    phase TEXT NOT NULL CHECK(phase IN ('dirty','verified')),
    updated_at_unix INTEGER NOT NULL,
    CHECK(
        (dirty_generation > verified_generation AND phase = 'dirty') OR
        (dirty_generation = verified_generation AND phase = 'verified')
    )
);
CREATE TABLE cluster_settings (
    setting_key TEXT PRIMARY KEY,
    public_value_json TEXT NOT NULL,
    generation INTEGER NOT NULL,
    updated_at_unix INTEGER NOT NULL,
    last_mutation_token TEXT NOT NULL DEFAULT ''
);
CREATE TABLE setting_members (
    setting_key TEXT NOT NULL,
    member_key TEXT NOT NULL,
    member_value_json TEXT NOT NULL,
    generation INTEGER NOT NULL,
    PRIMARY KEY(setting_key, member_key)
);
CREATE TABLE setting_secrets (
    setting_key TEXT PRIMARY KEY,
    secret_envelope BLOB NOT NULL,
    secret_sha256 TEXT NOT NULL,
    key_version INTEGER NOT NULL,
    updated_at_unix INTEGER NOT NULL
);
CREATE TABLE audit_events (
    event_id TEXT PRIMARY KEY,
    actor_hmac TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id_hmac TEXT NOT NULL,
    created_at_unix INTEGER NOT NULL
);
INSERT INTO cluster_restore_state(singleton_id, restore_epoch, activated) VALUES (1, 1, 1);
INSERT INTO backup_rpo_state(singleton_id, restore_epoch, dirty_generation, verified_generation, phase, updated_at_unix) VALUES (1, 1, 10, 0, 'dirty', 1);
INSERT INTO cluster_settings(setting_key, public_value_json, generation, updated_at_unix, last_mutation_token)
VALUES ('task5-sqlite-setting', '{"owner":"seed"}', 1, 1, 'seed-token');
""")

def snapshot():
    return {
        "setting": connection.execute(
            "SELECT setting_key, public_value_json, generation, updated_at_unix, last_mutation_token "
            "FROM cluster_settings WHERE setting_key='task5-sqlite-setting'"
        ).fetchone(),
        "members": connection.execute(
            "SELECT member_key, member_value_json, generation FROM setting_members "
            "WHERE setting_key='task5-sqlite-setting' ORDER BY member_key"
        ).fetchall(),
        "secret": connection.execute(
            "SELECT secret_envelope, secret_sha256, key_version, updated_at_unix FROM setting_secrets "
            "WHERE setting_key='task5-sqlite-setting'"
        ).fetchone(),
        "audits": connection.execute(
            "SELECT event_id, actor_hmac, action, resource_id_hmac, created_at_unix FROM audit_events "
            "ORDER BY event_id"
        ).fetchall(),
        "dirty": connection.execute(
            "SELECT dirty_generation, updated_at_unix FROM backup_rpo_state WHERE singleton_id=1"
        ).fetchone(),
    }

snapshots = []
for transaction in payload["transactions"]:
    connection.execute("BEGIN IMMEDIATE")
    try:
        for statement in transaction:
            cursor = connection.execute(statement["sql"], statement.get("args") or [])
            cursor.fetchall()
        connection.commit()
    except Exception:
        connection.rollback()
        raise
    snapshots.append(snapshot())

json.dump(snapshots, sys.stdout, sort_keys=True, separators=(",", ":"))
`
