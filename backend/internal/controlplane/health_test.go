package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type stubSchemaVerifier struct{ err error }

func (s stubSchemaVerifier) Verify(context.Context) error { return s.err }

type stubDiskSignal struct{ writable bool }

func (s stubDiskSignal) Writable() bool { return s.writable }

func TestMissingReferencedSecretKeyVersionMakesReadinessRed(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"key_version": 99}}},
		rqlite.Result{Rows: []map[string]any{{"count_value": 5}}},
		rqlite.Result{Rows: []map[string]any{{"updated_at_unix": 1_999_999}}},
	)}}
	service, _ := testService(t, db)
	readiness := NewReadiness(ReadinessConfig{
		Store:            service.store,
		Schema:           stubSchemaVerifier{},
		Disk:             stubDiskSignal{writable: true},
		IDs:              &sequenceIDs{},
		NodeID:           "S2",
		RequiredSettings: []string{"ota"},
		MaxCommitAge:     time.Minute,
	})
	if err := readiness.Read(context.Background()); err == nil {
		t.Fatal("readiness accepted a referenced missing secret key version")
	}
}

func TestReadinessRejectsStaleSchemaSettingsAndDisk(t *testing.T) {
	service, _ := testService(t, &recordingRQLite{})
	tests := []struct {
		name   string
		schema error
		disk   bool
	}{
		{name: "schema", schema: errors.New("checksum mismatch"), disk: true},
		{name: "disk", disk: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readiness := NewReadiness(ReadinessConfig{
				Store:            service.store,
				Schema:           stubSchemaVerifier{err: tc.schema},
				Disk:             stubDiskSignal{writable: tc.disk},
				IDs:              &sequenceIDs{},
				NodeID:           "S2",
				RequiredSettings: []string{"ota"},
				MaxCommitAge:     time.Minute,
			})
			if tc.name == "schema" {
				if err := readiness.Read(context.Background()); err == nil {
					t.Fatal("Read accepted stale schema")
				}
			} else if err := readiness.Write(context.Background()); err == nil {
				t.Fatal("Write accepted non-writable disk signal")
			}
		})
	}
}

func TestReadinessWriteRejectsQuorumLossAndNonceMismatch(t *testing.T) {
	t.Run("quorum", func(t *testing.T) {
		db := &recordingRQLite{requests: []scriptedResult{{err: errors.New("no quorum")}}}
		service, _ := testService(t, db)
		readiness := NewReadiness(ReadinessConfig{
			Store: service.store, Schema: stubSchemaVerifier{},
			Disk: stubDiskSignal{writable: true}, IDs: &sequenceIDs{}, NodeID: "S2",
		})
		if err := readiness.Write(context.Background()); err == nil {
			t.Fatal("Write accepted quorum loss")
		}
	})
	t.Run("nonce", func(t *testing.T) {
		db := &recordingRQLite{
			requests: []scriptedResult{resultsScript(rqlite.Result{RowsAffected: 1})},
			linear: []scriptedResult{rowsScript(map[string]any{
				"nonce_hmac": "wrong",
			})},
		}
		service, _ := testService(t, db)
		readiness := NewReadiness(ReadinessConfig{
			Store: service.store, Schema: stubSchemaVerifier{},
			Disk: stubDiskSignal{writable: true}, IDs: &sequenceIDs{}, NodeID: "S2",
		})
		if err := readiness.Write(context.Background()); err == nil {
			t.Fatal("Write accepted mismatched committed nonce")
		}
	})
}

func TestReadinessSuccessfulCanaryDoesNotGrowRows(t *testing.T) {
	db := &recordingRQLite{}
	service, secrets := testService(t, db)
	ids := &sequenceIDs{}
	nonce, _ := ids.NewID("canary")
	wantHMAC := secrets.LookupHMAC("health-canary", []byte(nonce))
	ids.counter = 0
	db.requests = []scriptedResult{resultsScript(rqlite.Result{RowsAffected: 1})}
	db.linear = []scriptedResult{rowsScript(map[string]any{
		"nonce_hmac": wantHMAC,
	})}
	readiness := NewReadiness(ReadinessConfig{
		Store: service.store, Schema: stubSchemaVerifier{},
		Disk: stubDiskSignal{writable: true}, IDs: ids, NodeID: "S2",
	})
	if err := readiness.Write(context.Background()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(db.requestCalls) != 1 || len(db.requestCalls[0].statements) != 1 {
		t.Fatalf("canary write calls = %#v", db.requestCalls)
	}
	sql := db.requestCalls[0].statements[0].SQL
	if !containsAll(sql, "health_write_canary", "ON CONFLICT", "generation") {
		t.Fatalf("canary does not use bounded upsert: %s", sql)
	}
}

func containsAll(value string, values ...string) bool {
	for _, item := range values {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}
