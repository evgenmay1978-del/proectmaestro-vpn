//go:build rqlite_integration

package controlplane

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSchemaSeedsImmutableTariffsAndFourNodes(t *testing.T) {
	ctx, db := mustAppliedSchema(t)

	tariffs := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT tariff_version_id, duration_days, amount_minor, currency
		FROM tariff_versions
		WHERE tariff_version_id IN ('tariff_1m_v1','tariff_2m_v1')
		ORDER BY tariff_version_id
	`})
	if got := fmt.Sprint(tariffs.Rows); got != `[map[amount_minor:40000 currency:RUB duration_days:30 tariff_version_id:tariff_1m_v1] map[amount_minor:80000 currency:RUB duration_days:60 tariff_version_id:tariff_2m_v1]]` {
		t.Fatalf("tariff seeds = %s", got)
	}

	nodes := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT node_id, is_voter FROM nodes ORDER BY node_id
	`})
	if got := fmt.Sprint(nodes.Rows); got != `[map[is_voter:0 node_id:S1] map[is_voter:1 node_id:S2] map[is_voter:1 node_id:S3] map[is_voter:1 node_id:S4]]` {
		t.Fatalf("node seeds = %s", got)
	}

	s1 := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT desired_target, apply_enabled, fenced, retired
		FROM node_services WHERE node_id='S1' AND service_name='maestro-core'
	`})
	if got := fmt.Sprint(s1.Rows); got != `[map[apply_enabled:0 desired_target:1 fenced:1 retired:0]]` {
		t.Fatalf("S1 service seed = %s", got)
	}

	if _, err := db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL:  "UPDATE tariff_versions SET amount_minor=? WHERE tariff_version_id=?",
		Args: []any{1, "tariff_1m_v1"},
	}); err == nil {
		t.Fatal("immutable tariff version accepted an update")
	}
}

func TestConstraintPaymentsAndIdempotencyFailClosed(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: `
			INSERT INTO customers(
				customer_id,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix
			) VALUES(?,?,?,?,?,?,?)
		`, Args: []any{"constraint-customer", repeatHex("a"), "active", 200000, 1, 100000, 100000}},
	)

	insertOrder := func(orderID string, created int64) {
		t.Helper()
		mustRequest(t, ctx, db, rqlite.Statement{SQL: `
			INSERT INTO orders(
				order_id,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
				amount_minor,currency,duration_days,created_at_unix,expires_at_unix,
				payment_state,provisioning_state,operation_id
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, Args: []any{
			orderID, "telegram", repeatHex("b"), "constraint-customer", "tariff_1m_v1",
			40000, "RUB", 30, created, created + 86400,
			string(PaymentPending), string(ProvisioningPending), "operation-" + orderID,
		}})
	}
	insertOrder("constraint-order-1", 100001)
	insertOrder("constraint-order-2", 100002)
	insertOrder("constraint-order-3", 100003)

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO payments(payment_id,order_id,provider,amount_minor,currency,confirmed_at_unix)
		VALUES(?,?,?,?,?,?)
	`, Args: []any{"bad-amount", "constraint-order-1", "manual", 0, "RUB", 100010}})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO payments(payment_id,order_id,provider,amount_minor,currency,confirmed_at_unix)
		VALUES(?,?,?,?,?,?)
	`, Args: []any{"bad-currency", "constraint-order-1", "manual", 40000, "USD", 100010}})

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: "UPDATE orders SET payment_state='confirmed', decision='confirmed', confirmed_at_unix=? WHERE order_id=?", Args: []any{100010, "constraint-order-1"}},
		rqlite.Statement{SQL: `
			INSERT INTO payments(payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
			VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"payment-1", "constraint-order-1", "manual", nil, nil, 40000, "RUB", 100010}},
		rqlite.Statement{SQL: "UPDATE orders SET payment_state='confirmed', decision='confirmed', confirmed_at_unix=? WHERE order_id=?", Args: []any{100011, "constraint-order-2"}},
		rqlite.Statement{SQL: `
			INSERT INTO payments(payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
			VALUES(?,?,?,?,?,?,?,?)
		`, Args: []any{"payment-2", "constraint-order-2", "manual", "provider-event", nil, 40000, "RUB", 100011}},
	)

	mustRequest(t, ctx, db,
		rqlite.Statement{SQL: "UPDATE orders SET payment_state='confirmed', decision='confirmed', confirmed_at_unix=? WHERE order_id=?", Args: []any{100012, "constraint-order-3"}},
	)
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO payments(payment_id,order_id,provider,provider_event_id,amount_minor,currency,confirmed_at_unix)
		VALUES(?,?,?,?,?,?,?)
	`, Args: []any{"payment-3", "constraint-order-3", "manual", "provider-event", 40000, "RUB", 100012}})

	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO idempotency_requests(
			scope,command_type,idempotency_key,request_hash,resource_id,decision,
			operation_id,status,response_json,created_at_unix
		) VALUES(?,?,?,?,?,?,?,?,?,?)
	`, Args: []any{"owner", "confirm", "key-1", "short", "payment-1", "payment_confirmed", "idem-op-1", "applied", `{}`, 100020}})
}

func TestConstraintAuditIsAppendOnlyAndForeignKeysDeclareDeleteActions(t *testing.T) {
	ctx, db := mustAppliedSchema(t)
	mustRequest(t, ctx, db, rqlite.Statement{SQL: `
		INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
		VALUES(?,?,?,?,?,?)
	`, Args: []any{"audit-1", repeatHex("c"), "test", "schema", repeatHex("d"), 100030}})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: "UPDATE audit_events SET action='changed' WHERE event_id='audit-1'"})
	mustRequestFail(t, ctx, db, rqlite.Statement{SQL: "DELETE FROM audit_events WHERE event_id='audit-1'"})

	tables := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: `
		SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name
	`})
	for _, row := range tables.Rows {
		name, ok := row["name"].(string)
		if !ok {
			t.Fatalf("malformed table row: %#v", row)
		}
		foreignKeys := mustStrongQuery(t, ctx, db, rqlite.Statement{SQL: fmt.Sprintf("PRAGMA foreign_key_list(%q)", name)})
		for _, fk := range foreignKeys.Rows {
			if fk["on_delete"] == "NO ACTION" {
				t.Fatalf("table %s has FK without explicit ON DELETE: %#v", name, fk)
			}
		}
	}
}

func mustAppliedSchema(t *testing.T) (context.Context, rqlite.RQLite) {
	t.Helper()
	db := mustIntegrationRQLite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := NewMigrator(db).Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return ctx, db
}

func mustStrongQuery(t *testing.T, ctx context.Context, db rqlite.RQLite, statement rqlite.Statement) rqlite.Result {
	t.Helper()
	results, err := db.QueryStrong(ctx, statement)
	if err != nil {
		t.Fatalf("QueryStrong: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("QueryStrong returned %d results, want 1", len(results))
	}
	return results[0]
}

func mustRequest(t *testing.T, ctx context.Context, db rqlite.RQLite, statements ...rqlite.Statement) {
	t.Helper()
	if _, err := db.Request(ctx, rqlite.Linearizable, true, statements...); err != nil {
		t.Fatalf("Request: %v", err)
	}
}

func mustRequestFail(t *testing.T, ctx context.Context, db rqlite.RQLite, statement rqlite.Statement) {
	t.Helper()
	if _, err := db.Request(ctx, rqlite.Linearizable, true, statement); err == nil {
		t.Fatalf("Request unexpectedly accepted %s", statement.SQL)
	}
}

func repeatHex(value string) string {
	return strings.Repeat(value, 64)
}
