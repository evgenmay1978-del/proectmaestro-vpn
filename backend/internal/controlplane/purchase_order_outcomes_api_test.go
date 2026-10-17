package controlplane_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestCreatePurchaseOrderWriteOutcomesSQLite(t *testing.T) {
	t.Run("committed-unknown-outcome-resolves-exact-generated-order", func(t *testing.T) {
		ctx := context.Background()
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
			t.Fatalf("apply real migrations: %v", err)
		}
		box := f6PurchaseSecretBox(t)
		clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
		outcomeDB := &f6PurchaseOutcomeDB{RQLite: db, commitThenUnknown: true}
		store, err := controlplane.NewStore(outcomeDB, box, clock)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		service, err := controlplane.NewService(store, &f6SequenceIDs{}, clock)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}

		order, err := service.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{
			TariffVersionID: "tariff_1m_v1", Actor: "legacy-http", Channel: "legacy-http",
		})
		if err != nil {
			t.Fatalf("CreatePurchaseOrder committed unknown outcome: %v", err)
		}
		if outcomeDB.generatedOrderID == "" || order.OrderID != outcomeDB.generatedOrderID {
			t.Fatalf("resolved order id=%q generated=%q, want exact generated id", order.OrderID, outcomeDB.generatedOrderID)
		}
		persisted, err := service.BusinessOrderByID(ctx, order.OrderID)
		if err != nil {
			t.Fatalf("BusinessOrderByID: %v", err)
		}
		customer, err := service.BusinessCustomerByID(ctx, persisted.CustomerID)
		if err != nil {
			t.Fatalf("BusinessCustomerByID: %v", err)
		}
		if persisted.TariffVersionID != "tariff_1m_v1" || persisted.PaymentState != controlplane.PaymentPending ||
			customer.Status != "expired" || customer.ExpiresAtUnix > clock.Now().Unix() ||
			customer.Access.SubscriptionToken == "" || len(customer.Access.Credentials) != 4 {
			t.Fatalf("resolved order=%+v customer=%+v, want exact inert purchase state", persisted, customer)
		}
		if outcomeDB.requests != 1 || !outcomeDB.transaction {
			t.Fatalf("request count=%d transaction=%v, want one atomic transactional write", outcomeDB.requests, outcomeDB.transaction)
		}
		rows := db.must(t, rqlite.Statement{SQL: `
SELECT CAST((SELECT count(*) FROM customers WHERE customer_id=?) AS TEXT) AS customers,
CAST((SELECT count(*) FROM orders WHERE order_id=? AND customer_id=?) AS TEXT) AS orders,
CAST((SELECT count(*) FROM desired_node_state WHERE customer_id=?) AS TEXT) AS desired,
CAST((SELECT count(*) FROM outbox_events WHERE aggregate_id LIKE ? || ':%') AS TEXT) AS outbox`,
			Args: []any{customer.ID, order.OrderID, customer.ID, customer.ID, customer.ID}})
		if len(rows) != 1 || len(rows[0].Rows) != 1 || rows[0].Rows[0]["customers"] != "1" ||
			rows[0].Rows[0]["orders"] != "1" || rows[0].Rows[0]["desired"] != "0" || rows[0].Rows[0]["outbox"] != "0" {
			t.Fatalf("committed unknown rows=%#v, want exact atomic inert customer+order only", rows)
		}
	})

	t.Run("committed-unknown-exact-read-failure-is-unavailable-without-write-retry", func(t *testing.T) {
		ctx := context.Background()
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
			t.Fatalf("apply real migrations: %v", err)
		}
		box := f6PurchaseSecretBox(t)
		clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
		outcomeDB := &f6PurchaseOutcomeDB{
			RQLite: db, commitThenUnknown: true, failExactResolution: true,
		}
		store, err := controlplane.NewStore(outcomeDB, box, clock)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		service, err := controlplane.NewService(store, &f6SequenceIDs{next: 200}, clock)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}

		order, createErr := service.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{
			TariffVersionID: "tariff_1m_v1", Actor: "legacy-http", Channel: "legacy-http",
		})
		if !errors.Is(createErr, controlplane.ErrUnavailable) {
			t.Fatalf("CreatePurchaseOrder order=%+v err=%v, want ErrUnavailable", order, createErr)
		}
		if outcomeDB.requests != 1 || outcomeDB.generatedOrderID == "" {
			t.Fatalf("requests=%d generated=%q, want one write and exact generated id", outcomeDB.requests, outcomeDB.generatedOrderID)
		}
		rows := db.must(t, rqlite.Statement{SQL: `
SELECT CAST((SELECT count(*) FROM orders WHERE order_id=?) AS TEXT) AS orders,
CAST((SELECT count(*) FROM customers c JOIN orders o ON o.customer_id=c.customer_id WHERE o.order_id=?) AS TEXT) AS customers`,
			Args: []any{outcomeDB.generatedOrderID, outcomeDB.generatedOrderID}})
		if len(rows) != 1 || len(rows[0].Rows) != 1 || rows[0].Rows[0]["orders"] != "1" || rows[0].Rows[0]["customers"] != "1" {
			t.Fatalf("committed rows=%#v, want committed exact customer+order despite unavailable resolution", rows)
		}
	})

	t.Run("definite-write-failure-never-reports-success", func(t *testing.T) {
		ctx := context.Background()
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
			t.Fatalf("apply real migrations: %v", err)
		}
		box := f6PurchaseSecretBox(t)
		clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
		outcomeDB := &f6PurchaseOutcomeDB{RQLite: db, failDefinite: true}
		store, err := controlplane.NewStore(outcomeDB, box, clock)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		service, err := controlplane.NewService(store, &f6SequenceIDs{next: 100}, clock)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}

		if order, createErr := service.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{
			TariffVersionID: "tariff_1m_v1", Actor: "legacy-http", Channel: "legacy-http",
		}); createErr == nil {
			t.Fatalf("definite write failure returned order=%+v", order)
		}
		rows := db.must(t, rqlite.Statement{SQL: `
SELECT CAST((SELECT count(*) FROM customers) AS TEXT) AS customers,
CAST((SELECT count(*) FROM orders) AS TEXT) AS orders`})
		if len(rows) != 1 || len(rows[0].Rows) != 1 || rows[0].Rows[0]["customers"] != "0" || rows[0].Rows[0]["orders"] != "0" {
			t.Fatalf("definite failure rows=%#v, want no customer or order", rows)
		}
	})

	t.Run("validation-tariff-terms-and-collision-rollback", func(t *testing.T) {
		ctx := context.Background()
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(ctx); err != nil {
			t.Fatalf("apply real migrations: %v", err)
		}
		box := f6PurchaseSecretBox(t)
		clock := s4CanaryClock{value: time.Unix(2_000_000, 0).UTC()}
		outcomeDB := &f6PurchaseOutcomeDB{RQLite: db}
		store, err := controlplane.NewStore(outcomeDB, box, clock)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		service, err := controlplane.NewService(store, &f6SequenceIDs{}, clock)
		if err != nil {
			t.Fatalf("NewService: %v", err)
		}
		if order, createErr := service.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{}); createErr == nil {
			t.Fatalf("empty purchase command returned order=%+v", order)
		}
		if outcomeDB.requests != 0 {
			t.Fatalf("invalid command made %d writes, want zero", outcomeDB.requests)
		}

		order, err := service.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{
			TariffVersionID: "tariff_1m_v1", Actor: "legacy-http", Channel: "legacy-http",
		})
		if err != nil {
			t.Fatalf("valid CreatePurchaseOrder: %v", err)
		}
		if order.AmountMinor != 40000 || order.Currency != "RUB" || order.DurationSeconds != 30*86400 {
			t.Fatalf("order terms=%+v, want immutable tariff_versions snapshot", order)
		}
		persisted, err := service.BusinessOrderByID(ctx, order.OrderID)
		if err != nil {
			t.Fatalf("BusinessOrderByID: %v", err)
		}
		customer, err := service.BusinessCustomerByID(ctx, persisted.CustomerID)
		if err != nil {
			t.Fatalf("BusinessCustomerByID: %v", err)
		}
		statementDump := fmt.Sprint(outcomeDB.lastStatements)
		secrets := []string{customer.Access.SubscriptionToken}
		for _, credential := range customer.Access.Credentials {
			secrets = append(secrets, credential)
		}
		for _, secret := range secrets {
			if secret != "" && strings.Contains(statementDump, secret) {
				t.Fatalf("atomic statement args leaked plaintext secret %q", secret)
			}
		}
		backupMarkers, auditWrites, idempotencyWrites := 0, 0, 0
		for _, statement := range outcomeDB.lastStatements {
			if strings.Contains(statement.SQL, "backup_rpo_state") {
				backupMarkers++
			}
			if strings.Contains(statement.SQL, "INSERT INTO audit_events") {
				auditWrites++
			}
			if strings.Contains(statement.SQL, "idempotency_requests") {
				idempotencyWrites++
			}
		}
		if backupMarkers != 1 || auditWrites != 2 || idempotencyWrites != 0 {
			t.Fatalf("atomic statements backup=%d audit=%d idempotency=%d, want 1/2/0", backupMarkers, auditWrites, idempotencyWrites)
		}

		if rejected, createErr := service.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{
			TariffVersionID: "missing-tariff", Actor: "legacy-http", Channel: "legacy-http",
		}); createErr == nil {
			t.Fatalf("missing tariff returned order=%+v", rejected)
		}
		counts := db.must(t, rqlite.Statement{SQL: `
SELECT CAST((SELECT count(*) FROM customers) AS TEXT) AS customers,
CAST((SELECT count(*) FROM orders) AS TEXT) AS orders`})
		if len(counts) != 1 || len(counts[0].Rows) != 1 || counts[0].Rows[0]["customers"] != "1" || counts[0].Rows[0]["orders"] != "1" {
			t.Fatalf("missing tariff counts=%#v, want atomic rollback", counts)
		}

		collisionDB := &f6PurchaseOutcomeDB{RQLite: db}
		collisionStore, err := controlplane.NewStore(collisionDB, box, clock)
		if err != nil {
			t.Fatalf("collision NewStore: %v", err)
		}
		collisionService, err := controlplane.NewService(collisionStore, &f6SequenceIDs{}, clock)
		if err != nil {
			t.Fatalf("collision NewService: %v", err)
		}
		if collision, collisionErr := collisionService.CreatePurchaseOrder(ctx, controlplane.PurchaseOrderCommand{
			TariffVersionID: "tariff_1m_v1", Actor: "legacy-http", Channel: "legacy-http",
		}); collisionErr == nil {
			t.Fatalf("identifier collision returned order=%+v", collision)
		}
		counts = db.must(t, rqlite.Statement{SQL: `
SELECT CAST((SELECT count(*) FROM customers) AS TEXT) AS customers,
CAST((SELECT count(*) FROM orders) AS TEXT) AS orders`})
		if len(counts) != 1 || len(counts[0].Rows) != 1 || counts[0].Rows[0]["customers"] != "1" || counts[0].Rows[0]["orders"] != "1" {
			t.Fatalf("collision counts=%#v, want original rows only", counts)
		}
	})
}

func f6PurchaseSecretBox(t *testing.T) *controlplane.SecretBox {
	t.Helper()
	box, err := controlplane.NewSecretBox(
		1,
		map[int][]byte{1: bytes.Repeat([]byte{0x61}, 32)},
		bytes.Repeat([]byte{0x62}, 32),
	)
	if err != nil {
		t.Fatalf("NewSecretBox: %v", err)
	}
	return box
}

type f6PurchaseOutcomeDB struct {
	rqlite.RQLite
	commitThenUnknown   bool
	failDefinite        bool
	failExactResolution bool
	requests            int
	transaction         bool
	generatedOrderID    string
	lastStatements      []rqlite.Statement
}

func (db *f6PurchaseOutcomeDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	if db.failExactResolution {
		for _, statement := range statements {
			if strings.Contains(statement.SQL, "FROM orders WHERE order_id=?") {
				return nil, errors.New("injected exact purchase order resolution failure")
			}
		}
	}
	return db.RQLite.QueryLinearizable(ctx, statements...)
}

func (db *f6PurchaseOutcomeDB) Request(
	ctx context.Context,
	consistency rqlite.Consistency,
	transaction bool,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	db.requests++
	db.transaction = transaction
	db.lastStatements = make([]rqlite.Statement, len(statements))
	for index, statement := range statements {
		db.lastStatements[index] = rqlite.Statement{SQL: statement.SQL, Args: append([]any(nil), statement.Args...)}
		if strings.Contains(statement.SQL, "INSERT INTO orders(") && len(statement.Args) > 0 {
			db.generatedOrderID, _ = statement.Args[0].(string)
		}
	}
	if db.failDefinite {
		return nil, errors.New("injected definite purchase order failure")
	}
	results, err := db.RQLite.Request(ctx, consistency, transaction, statements...)
	if err != nil {
		return nil, err
	}
	if db.commitThenUnknown {
		return nil, &rqlite.TransportError{
			Operation: "request", UnknownOutcome: true, Err: errors.New("injected committed ambiguity"),
		}
	}
	return results, nil
}
