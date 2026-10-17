package controlplane_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestKeylessExistingPurchaseCustomerDisappearsBeforeRequestSQLite(t *testing.T) {
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seed := newF6ShipFixtureOnAppliedDB(t, db, db, time.Unix(2_000_000, 0).UTC(), &f6SequenceIDs{})
	existing := seed.seedCustomer(t, "Keyless-Race-Owner", "active")

	gate := newF6KeylessPurchaseGateDB(db)
	fixture := newF6ShipFixtureOnAppliedDB(t, db, gate, time.Unix(2_000_000, 0).UTC(), &f6SequenceIDs{next: 30_000})
	result := make(chan int, 1)
	go func() {
		status, _, _ := f6ShipPostOrderNoHelper(fixture.handler,
			fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, existing.Access.SubscriptionToken), "")
		result <- status
	}()
	defer gate.releaseRequest()

	select {
	case <-gate.beforeRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("purchase request did not reach the post-identity gate")
	}
	db.must(t, rqlite.Statement{SQL: `DELETE FROM customers WHERE customer_id=?`, Args: []any{existing.ID}})
	missing := db.must(t, rqlite.Statement{SQL: `SELECT count(*) AS count FROM customers WHERE customer_id=?`, Args: []any{existing.ID}})
	if len(missing) != 1 || len(missing[0].Rows) != 1 || fmt.Sprint(missing[0].Rows[0]["count"]) != "0" {
		t.Fatalf("customer race delete did not commit: %#v", missing)
	}
	before := f6KeylessRaceStateNow(t, db)
	gate.releaseRequest()

	select {
	case status := <-result:
		if status == http.StatusOK {
			t.Fatal("missing-customer keyless renewal returned 200")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("purchase request did not finish after gate release")
	}
	if gate.requestCount() != 1 {
		t.Fatalf("purchase writes=%d, want exactly one non-retried write", gate.requestCount())
	}
	if after := f6KeylessRaceStateNow(t, db); after != before {
		t.Fatalf("missing-customer keyless renewal mutated durable state\nbefore=%+v\nafter=%+v", before, after)
	}
}

type f6KeylessPurchaseGateDB struct {
	rqlite.RQLite
	beforeRequest chan struct{}
	release       chan struct{}
	gateOnce      sync.Once
	releaseOnce   sync.Once
	mu            sync.Mutex
	requests      int
}

func newF6KeylessPurchaseGateDB(db rqlite.RQLite) *f6KeylessPurchaseGateDB {
	return &f6KeylessPurchaseGateDB{
		RQLite: db, beforeRequest: make(chan struct{}), release: make(chan struct{}),
	}
}

func (db *f6KeylessPurchaseGateDB) Request(
	ctx context.Context, consistency rqlite.Consistency, transaction bool, statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	isPurchase := false
	for _, statement := range statements {
		if strings.Contains(statement.SQL, "INSERT INTO orders(") {
			isPurchase = true
			break
		}
	}
	if isPurchase {
		db.mu.Lock()
		db.requests++
		db.mu.Unlock()
		db.gateOnce.Do(func() { close(db.beforeRequest) })
		select {
		case <-db.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return db.RQLite.Request(ctx, consistency, transaction, statements...)
}

func (db *f6KeylessPurchaseGateDB) releaseRequest() {
	db.releaseOnce.Do(func() { close(db.release) })
}

func (db *f6KeylessPurchaseGateDB) requestCount() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.requests
}

type f6KeylessRaceState struct {
	Customers, Tokens, Credentials, Orders, Audits, Idempotency string
	Desired, Outbox, Dirty, BackupPhase, BackupUpdated          string
}

func f6KeylessRaceStateNow(t *testing.T, db *s4CanarySQLite) f6KeylessRaceState {
	t.Helper()
	results := db.must(t, rqlite.Statement{SQL: `SELECT
CAST((SELECT count(*) FROM customers) AS TEXT) AS customers,
CAST((SELECT count(*) FROM subscription_tokens) AS TEXT) AS tokens,
CAST((SELECT count(*) FROM credentials) AS TEXT) AS credentials,
CAST((SELECT count(*) FROM orders) AS TEXT) AS orders,
CAST((SELECT count(*) FROM audit_events) AS TEXT) AS audits,
CAST((SELECT count(*) FROM idempotency_requests) AS TEXT) AS idempotency,
CAST((SELECT count(*) FROM desired_node_state) AS TEXT) AS desired,
CAST((SELECT count(*) FROM outbox_events) AS TEXT) AS outbox,
CAST(dirty_generation AS TEXT) AS dirty,phase,CAST(updated_at_unix AS TEXT) AS backup_updated
FROM backup_rpo_state WHERE singleton_id=1`})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("race state rows=%#v", results)
	}
	row := results[0].Rows[0]
	return f6KeylessRaceState{
		Customers: fmt.Sprint(row["customers"]), Tokens: fmt.Sprint(row["tokens"]),
		Credentials: fmt.Sprint(row["credentials"]), Orders: fmt.Sprint(row["orders"]),
		Audits: fmt.Sprint(row["audits"]), Idempotency: fmt.Sprint(row["idempotency"]),
		Desired: fmt.Sprint(row["desired"]), Outbox: fmt.Sprint(row["outbox"]), Dirty: fmt.Sprint(row["dirty"]),
		BackupPhase: fmt.Sprint(row["phase"]), BackupUpdated: fmt.Sprint(row["backup_updated"]),
	}
}
