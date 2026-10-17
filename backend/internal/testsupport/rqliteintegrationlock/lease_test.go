//go:build rqlite_integration

package rqliteintegrationlock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type scriptedRQLite struct {
	requestResults [][]rqlite.Result
	requestErrors  []error
	queryResults   [][]rqlite.Result
	queryErrors    []error
	requests       []rqlite.Statement
	queries        []rqlite.Statement
}

func (db *scriptedRQLite) Request(_ context.Context, _ rqlite.Consistency, _ bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.requests = append(db.requests, statements...)
	index := len(db.requests) - 1
	return db.requestResults[index], db.requestErrors[index]
}

func (db *scriptedRQLite) QueryLinearizable(_ context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.queries = append(db.queries, statements...)
	index := len(db.queries) - 1
	return db.queryResults[index], db.queryErrors[index]
}

func (db *scriptedRQLite) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.QueryLinearizable(ctx, statements...)
}

func (db *scriptedRQLite) Backup(context.Context, io.Writer) error { return nil }

func TestSafetyDeadlineCannotExtendOnRepeatedSameExpiryProof(t *testing.T) {
	lease := &Lease{}
	observedAt := time.Unix(1_000, 0)
	state := leaseState{acquiredAt: 100, expiresAt: 103, databaseNow: 100}
	lease.recordState(state, observedAt)
	first, ok := lease.safetyDeadline(0)
	if !ok || !first.Equal(observedAt.Add(2*time.Second)) {
		t.Fatalf("initial safety deadline=(%s,%t), want one-second quantization reserve", first, ok)
	}

	lease.recordState(state, observedAt.Add(900*time.Millisecond))
	repeated, ok := lease.safetyDeadline(0)
	if !ok || repeated.After(first) {
		t.Fatalf("same-expiry proof extended safety deadline from %s to %s", first, repeated)
	}

	lease.recordState(leaseState{acquiredAt: 100, expiresAt: 106, databaseNow: 101}, observedAt.Add(time.Second))
	renewed, ok := lease.safetyDeadline(0)
	if !ok || !renewed.After(first) {
		t.Fatalf("later proven expiry did not advance safety deadline: first=%s renewed=%s", first, renewed)
	}
}
func TestLeaseLifecycleUsesExactIdentity(t *testing.T) {
	identity := Identity{JobName: JobName, HolderID: "importer-package", LeaseToken: "lease-token-a"}
	db := &scriptedRQLite{
		requestResults: [][]rqlite.Result{
			{{Rows: []map[string]any{leaseRow(identity, 100, 130, 100)}}},
			{{Rows: []map[string]any{leaseRow(identity, 100, 160, 110)}}},
			{{Rows: []map[string]any{{"job_name": identity.JobName, "holder_id": identity.HolderID, "lease_token": identity.LeaseToken}}}},
			{{}},
		},
		requestErrors: make([]error, 4),
	}
	lease, err := Acquire(context.Background(), db, Options{
		HolderID: identity.HolderID, LeaseToken: identity.LeaseToken,
		TTL: 30 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil || lease.Identity() != identity {
		t.Fatalf("acquire=(%#v,%v), want identity %#v", lease, err, identity)
	}
	if err := lease.Renew(context.Background()); err != nil {
		t.Fatalf("renew exact lease: %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("release exact lease: %v", err)
	}
	if err := lease.Release(context.Background()); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("second release error=%v, want ErrLeaseLost", err)
	}
	if len(db.requests) != 4 || db.requests[0].SQL != acquireSQL || db.requests[1].SQL != renewSQL || db.requests[2].SQL != releaseSQL {
		t.Fatalf("lease lifecycle SQL mismatch: %#v", db.requests)
	}
}

func TestAcquireResolvesCommittedUnknownByExactIdentity(t *testing.T) {
	identity := Identity{JobName: JobName, HolderID: "controlplane-package", LeaseToken: "lease-token-b"}
	db := &scriptedRQLite{
		requestResults: [][]rqlite.Result{nil},
		requestErrors:  []error{&rqlite.TransportError{Operation: "request response", UnknownOutcome: true, Err: context.DeadlineExceeded}},
		queryResults:   [][]rqlite.Result{{{Rows: []map[string]any{leaseRow(identity, 200, 230, 201)}}}},
		queryErrors:    []error{nil},
	}
	lease, err := Acquire(context.Background(), db, Options{
		HolderID: identity.HolderID, LeaseToken: identity.LeaseToken,
		TTL: 30 * time.Second, PollInterval: time.Millisecond,
	})
	if err != nil || lease.Identity() != identity {
		t.Fatalf("resolve committed acquire=(%#v,%v), want %#v", lease, err, identity)
	}
	if len(db.queries) != 1 || db.queries[0].SQL != currentSQL {
		t.Fatalf("committed-unknown resolver queries=%#v", db.queries)
	}
}

func TestGeneratedLeaseTokensAreProcessUnique(t *testing.T) {
	first, err := newLeaseToken()
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	second, err := newLeaseToken()
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatalf("generated tokens=(%q,%q)", first, second)
	}
}

func TestLeaseSQLRecoversExpiredOwnerAndIsClusterScopedSQLite(t *testing.T) {
	ownerA := Identity{JobName: JobName, HolderID: "package-a", LeaseToken: "token-a"}
	ownerB := Identity{JobName: JobName, HolderID: "package-b", LeaseToken: "token-b"}
	payload, err := json.Marshal(map[string]any{
		"acquire_a": acquireStatement(ownerA, 30), "acquire_b": acquireStatement(ownerB, 30),
		"release_a": releaseStatement(ownerA), "release_b": releaseStatement(ownerB),
	})
	if err != nil {
		t.Fatalf("encode lease SQLite payload: %v", err)
	}
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatalf("working python sqlite3 is required: %v", err)
	}
	command := exec.Command(python, "-c", leaseSQLiteProgram)
	command.Stdin = bytes.NewReader(payload)
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		t.Fatalf("execute lease SQLite proof: %v: %s", commandErr, output)
	}
	var result struct {
		FirstAcquire, ContendedAcquire, ExpiredTakeover int
		WrongRelease, ExactRelease, OtherCluster        int
		TakeoverHolder                                  string `json:"takeover_holder"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode lease SQLite proof: %v: %s", err, output)
	}
	if result.FirstAcquire != 1 || result.ContendedAcquire != 0 || result.ExpiredTakeover != 1 ||
		result.WrongRelease != 0 || result.ExactRelease != 1 || result.OtherCluster != 1 ||
		result.TakeoverHolder != ownerB.HolderID {
		t.Fatalf("lease SQLite lifecycle=%#v", result)
	}
}

func leaseRow(identity Identity, acquiredAt, expiresAt, databaseNow int64) map[string]any {
	return map[string]any{
		"job_name": identity.JobName, "holder_id": identity.HolderID, "lease_token": identity.LeaseToken,
		"acquired_at_unix": acquiredAt, "expires_at_unix": expiresAt, "database_now_unix": databaseNow,
	}
}

const leaseSQLiteProgram = `
import json
import sqlite3
import sys

payload = json.load(sys.stdin)
schema = """
CREATE TABLE cluster_job_leases(
    job_name TEXT PRIMARY KEY, holder_id TEXT NOT NULL, lease_token TEXT NOT NULL UNIQUE,
    acquired_at_unix INTEGER NOT NULL CHECK(acquired_at_unix >= 0),
    expires_at_unix INTEGER NOT NULL CHECK(expires_at_unix > acquired_at_unix),
    restore_epoch INTEGER NOT NULL DEFAULT 0 CHECK(restore_epoch >= 0),
    lease_fence INTEGER NOT NULL DEFAULT 0 CHECK(lease_fence >= 0),
    capability_generation INTEGER NOT NULL DEFAULT 0 CHECK(capability_generation >= 0),
    capability_evidence_sha256 TEXT,
    capability_expires_at_unix INTEGER NOT NULL DEFAULT 0 CHECK(capability_expires_at_unix >= 0)
)
"""
def connection_at(clock):
    connection = sqlite3.connect(":memory:", isolation_level=None)
    connection.create_function("unixepoch", 0, lambda: clock[0])
    connection.execute(schema)
    return connection
def execute(connection, statement):
    return connection.execute(statement["SQL"], statement.get("Args") or []).fetchall()

clock = [100]
first = connection_at(clock)
first_acquire = execute(first, payload["acquire_a"])
clock[0] = 101
contended_acquire = execute(first, payload["acquire_b"])
clock[0] = 131
expired_takeover = execute(first, payload["acquire_b"])
wrong_release = execute(first, payload["release_a"])
exact_release = execute(first, payload["release_b"])
other_clock = [101]
other = connection_at(other_clock)
other_cluster = execute(other, payload["acquire_a"])
print(json.dumps({
    "FirstAcquire": len(first_acquire), "ContendedAcquire": len(contended_acquire),
    "ExpiredTakeover": len(expired_takeover), "WrongRelease": len(wrong_release),
    "ExactRelease": len(exact_release), "OtherCluster": len(other_cluster),
    "takeover_holder": expired_takeover[0][1] if expired_takeover else "",
}))
`
