package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// Break caught: filtering targets by current availability would forget a
// fenced/down node and permit credentials to disappear before its later catch-up.
func TestDeleteKeepsTombstoneUntilEveryRequiredServiceAcknowledges(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"generation": int64(9)}}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 4},
		rqlite.Result{RowsAffected: 4},
		rqlite.Result{RowsAffected: 4},
	)}}
	service, _ := testService(t, db)

	targets, err := service.CreateCustomerTombstone(context.Background(), CustomerTombstoneCommand{
		TombstoneID: "tombstone-1", CustomerID: "customer-1",
		Generation: 9, Reason: "owner-delete",
	})
	if err != nil || targets != 4 {
		t.Fatalf("CreateCustomerTombstone targets=%d err=%v", targets, err)
	}
	if len(db.requestCalls) != 1 || !db.requestCalls[0].transaction {
		t.Fatalf("tombstone calls=%#v", db.requestCalls)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{
		"insert into tombstones", "insert into tombstone_targets",
		"desired_target=1", "retired=0",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("tombstone transaction lacks %q: %s", required, sql)
		}
	}
	for _, required := range []string{"update desired_node_state", "insert into outbox_events"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("tombstone transaction lacks revoke propagation %q: %s", required, sql)
		}
	}
	if strings.Contains(sql, "apply_enabled=1") || strings.Contains(sql, "fenced=0") || strings.Contains(sql, "enabled=1") {
		t.Fatalf("unavailable required target was filtered out: %s", sql)
	}
}

// Break caught: a plain topology edit could otherwise erase a required target
// and unblock tombstone purge without an owner CAS and append-only audit event.
func TestPermanentRetirementRequiresAuditedOwnerCAS(t *testing.T) {
	db := &recordingRQLite{requests: []scriptedResult{resultsScript(
		rqlite.Result{Rows: []map[string]any{{"retired": int64(1)}}},
		rqlite.Result{RowsAffected: 1},
		rqlite.Result{RowsAffected: 3},
	)}}
	service, _ := testService(t, db)

	removed, err := service.PermanentlyRetireNodeService(context.Background(), PermanentRetirementCommand{
		NodeID: "s1", ServiceName: "xui", ExpectedIncarnation: 4,
		Actor: "owner", Reason: "provider-retired",
	})
	if err != nil || removed != 3 {
		t.Fatalf("PermanentlyRetireNodeService removed=%d err=%v", removed, err)
	}
	sql := strings.ToLower(statementsText(db.requestCalls[0].statements))
	for _, required := range []string{
		"node_incarnation=?", "fenced=1", "retired=0",
		"insert into audit_events", "delete from tombstone_targets",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("retirement transaction lacks %q: %s", required, sql)
		}
	}
	if strings.Contains(sql, "delete from tombstone_targets where node_id=?") {
		t.Fatalf("target removal is not gated by an audit receipt: %s", sql)
	}
}
