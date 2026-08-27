//go:build rqlite_integration

package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

type task7IDs struct {
	prefix string
	next   atomic.Uint64
}

func (ids *task7IDs) NewID(prefix string) (string, error) {
	return fmt.Sprintf("%s_%s_%016x", prefix, ids.prefix, ids.next.Add(1)), nil
}

type task7WriteFault struct {
	delegate rqlite.RQLite
	failAt   int
	calls    int
	mu       sync.Mutex
}

func (db *task7WriteFault) Request(ctx context.Context, level rqlite.Consistency, tx bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	db.mu.Lock()
	db.calls++
	call := db.calls
	db.mu.Unlock()
	results, err := db.delegate.Request(ctx, level, tx, statements...)
	if err == nil && call == db.failAt {
		return nil, &rqlite.TransportError{Operation: "request response", UnknownOutcome: true, Err: io.ErrUnexpectedEOF}
	}
	return results, err
}

func (db *task7WriteFault) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.delegate.QueryLinearizable(ctx, statements...)
}
func (db *task7WriteFault) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.delegate.QueryStrong(ctx, statements...)
}
func (db *task7WriteFault) Backup(ctx context.Context, writer io.Writer) error {
	return db.delegate.Backup(ctx, writer)
}

type task7NoQuorum struct{ delegate rqlite.RQLite }

func (db task7NoQuorum) Request(context.Context, rqlite.Consistency, bool, ...rqlite.Statement) ([]rqlite.Result, error) {
	return nil, &rqlite.TransportError{Operation: "request", StatusCode: 503, UnknownOutcome: true}
}
func (db task7NoQuorum) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.delegate.QueryLinearizable(ctx, statements...)
}
func (db task7NoQuorum) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.delegate.QueryStrong(ctx, statements...)
}
func (db task7NoQuorum) Backup(ctx context.Context, writer io.Writer) error {
	return db.delegate.Backup(ctx, writer)
}

type task7ConfirmLoserGate struct {
	rqlite.RQLite
	reached chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (db *task7ConfirmLoserGate) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	results, err := db.RQLite.QueryLinearizable(ctx, statements...)
	if err != nil || len(statements) != 1 ||
		!strings.Contains(strings.ToLower(statements[0].SQL), "from idempotency_requests") {
		return results, err
	}
	blocked := false
	db.once.Do(func() {
		blocked = true
		close(db.reached)
	})
	if blocked {
		select {
		case <-db.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return results, nil
}

func task7Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func task7DB(t *testing.T) rqlite.RQLite {
	t.Helper()
	db := mustIntegrationRQLite(t)
	if err := NewMigrator(db).Apply(task7Context(t)); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	fixture := []rqlite.Statement{{SQL: `DELETE FROM cluster_job_leases WHERE job_name='expiry-sweeper'`}, {SQL: `INSERT INTO tariff_versions(
tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix)
VALUES('tariff_1m_v1','one-month',30,40000,'RUB',1,1)
ON CONFLICT(tariff_version_id) DO NOTHING`}}
	for index, nodeID := range []string{"S1", "S2", "S3", "S4"} {
		enabled, applyEnabled, fenced := 1, 1, 0
		if nodeID == "S1" {
			enabled, applyEnabled, fenced = 0, 0, 1
		}
		fixture = append(fixture,
			rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix)
VALUES(?,?,0,?,1) ON CONFLICT(node_id) DO UPDATE SET enabled=excluded.enabled`, Args: []any{nodeID, "task7-" + nodeID, enabled}},
			rqlite.Statement{SQL: `INSERT INTO node_services(
node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix)
VALUES(?,'maestro-core',1,?,?,0,?)
ON CONFLICT(node_id,service_name) DO UPDATE SET
desired_target=1,apply_enabled=excluded.apply_enabled,fenced=excluded.fenced,retired=0,updated_at_unix=excluded.updated_at_unix`, Args: []any{nodeID, applyEnabled, fenced, index + 1}},
		)
	}
	task7Request(t, db, fixture...)
	return db
}

func task7Name(t *testing.T, suffix string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + "\x00" + suffix))
	return strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_")) + "_" + hex.EncodeToString(sum[:6])
}

func task7Service(t *testing.T, db rqlite.RQLite) *Service {
	t.Helper()
	box, err := NewSecretBox(1, map[int][]byte{1: bytes.Repeat([]byte{0x71}, 32)}, bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	clock := fixedClock{value: time.Unix(2_100_000_000, 0)}
	store, err := NewStore(db, box, clock)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	service, err := NewService(store, &task7IDs{prefix: task7Name(t, "ids")}, clock)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return service
}

func task7Request(t *testing.T, db rqlite.RQLite, statements ...rqlite.Statement) []rqlite.Result {
	t.Helper()
	results, err := db.Request(task7Context(t), rqlite.Linearizable, true, statements...)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return results
}

func task7Row(t *testing.T, db rqlite.RQLite, statement rqlite.Statement) map[string]any {
	t.Helper()
	results, err := db.QueryLinearizable(task7Context(t), statement)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	row, ok := firstRow(results)
	if !ok {
		t.Fatalf("query returned no row: %s", statement.SQL)
	}
	return row
}

func task7Int(t *testing.T, db rqlite.RQLite, sql string, args ...any) int64 {
	t.Helper()
	row := task7Row(t, db, rqlite.Statement{SQL: sql, Args: args})
	value, ok := rowInt64(row, "n")
	if !ok {
		t.Fatalf("invalid integer row: %#v", row)
	}
	return value
}

type task7Counts struct {
	Customers int64
	Orders    int64
	Payments  int64
	Outbox    int64
}

func task7Snapshot(t *testing.T, db rqlite.RQLite) task7Counts {
	t.Helper()
	return task7Counts{
		Customers: task7Int(t, db, "SELECT COUNT(*) AS n FROM customers"),
		Orders:    task7Int(t, db, "SELECT COUNT(*) AS n FROM orders"),
		Payments:  task7Int(t, db, "SELECT COUNT(*) AS n FROM payments"),
		Outbox:    task7Int(t, db, "SELECT COUNT(*) AS n FROM outbox_events"),
	}
}

func task7Now(t *testing.T, db rqlite.RQLite) int64 {
	t.Helper()
	return task7Int(t, db, "SELECT unixepoch() AS n")
}

func task7SeedCustomer(t *testing.T, db rqlite.RQLite, status string, expiry, generation int64) string {
	t.Helper()
	id := "customer_" + task7Name(t, fmt.Sprintf("customer-%d", generation))
	login := sha256.Sum256([]byte(id))
	now := task7Now(t, db)
	task7Request(t, db, rqlite.Statement{SQL: `INSERT INTO customers(
customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
VALUES(?,?,?,?,?,?,?,?)`, Args: []any{id, id, hex.EncodeToString(login[:]), status, expiry, generation, now - 100, now}})
	return id
}

func task7Create(t *testing.T, service *Service, customerID, scope, buyer, bot string) OrderView {
	t.Helper()
	order, err := service.CreateOrder(task7Context(t), CreateOrderCommand{
		TariffVersionID: "tariff_1m_v1", CustomerID: customerID, BuyerScope: scope,
		BuyerIdentity: buyer, OriginBotID: bot, ChatIdentity: "chat-" + buyer,
		Actor: "buyer", Channel: "telegram", SourceEventID: "create-" + buyer,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return order
}

func task7Claim(t *testing.T, service *Service, orderID string) OrderView {
	t.Helper()
	order, err := service.MarkPaymentClaimed(task7Context(t), ClaimPaymentCommand{
		OrderID: orderID, Actor: "buyer", Channel: "telegram", SourceEventID: "claim-" + orderID,
	})
	if err != nil {
		t.Fatalf("MarkPaymentClaimed: %v", err)
	}
	return order
}

func task7Confirm(orderID, key, receipt string) ConfirmPaymentCommand {
	return ConfirmPaymentCommand{
		OrderID: orderID, IdempotencyKey: key, PaymentReference: receipt,
		Provider: "manual", TariffVersionID: "tariff_1m_v1", Actor: "owner",
		Channel: "web", SourceEventID: "confirm-" + orderID,
	}
}

func task7SeedDueOrder(t *testing.T, db rqlite.RQLite, customerID, state, bot string) string {
	t.Helper()
	orderID := "ord_" + task7Name(t, state+bot)
	buyer := sha256.Sum256([]byte(orderID))
	now := task7Now(t, db)
	task7Request(t, db,
		rqlite.Statement{SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,amount_minor,currency,duration_days,
created_at_unix,expires_at_unix,payment_state,provisioning_state,decision,operation_id,origin_bot_id,origin_chat_key_hmac)
VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,?,?,?,'none',NULL,?,?,?)`, Args: []any{
			orderID, "code_" + task7Name(t, state), "telegram_user", hex.EncodeToString(buyer[:]), customerID,
			now - 86401, now - 1, state, "op_" + task7Name(t, state), bot, hex.EncodeToString(buyer[:]),
		}},
		rqlite.Statement{SQL: `INSERT INTO active_order_guards(buyer_scope,buyer_key_hmac,order_id,created_at_unix)
SELECT buyer_scope,buyer_key_hmac,order_id,created_at_unix FROM orders WHERE order_id=?`, Args: []any{orderID}},
	)
	return orderID
}

func task7Activate(t *testing.T, db rqlite.RQLite) {
	t.Helper()
	task7Request(t, db, rqlite.Statement{SQL: `UPDATE cluster_restore_state SET activated=1 WHERE singleton_id=1`})
}

func TestHundredConcurrentSameConfirmCreditsOnce(t *testing.T) {
	db := task7DB(t)
	now := task7Now(t, db)
	customerID := task7SeedCustomer(t, db, "active", now+600, 7)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "70001", "bot-a")
	task7Claim(t, service, order.OrderID)
	command := task7Confirm(order.OrderID, "confirm-hundred", "receipt-hundred")
	results := make(chan ConfirmPaymentResult, 100)
	errorsCh := make(chan error, 100)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < 100; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			attempt := command
			attempt.Actor = fmt.Sprintf("owner-%d", index)
			attempt.SourceEventID = fmt.Sprintf("callback-%d", index)
			result, err := service.ConfirmPayment(task7Context(t), attempt)
			results <- result
			errorsCh <- err
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	var first ConfirmPaymentResult
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("duplicate confirm: %v", err)
		}
	}
	for result := range results {
		if first.OperationID == "" {
			first = result
		} else if result != first {
			t.Fatalf("saved results differ: %#v vs %#v", result, first)
		}
	}
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM payments WHERE order_id=?", order.OrderID) != 1 {
		t.Fatal("payment count is not one")
	}
	row := task7Row(t, db, rqlite.Statement{SQL: "SELECT expires_at_unix,generation FROM customers WHERE customer_id=?", Args: []any{customerID}})
	expires, _ := rowInt64(row, "expires_at_unix")
	generation, _ := rowInt64(row, "generation")
	if expires != now+600+30*86400 || generation != 8 {
		t.Fatalf("customer expiry/generation=(%d,%d)", expires, generation)
	}
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM outbox_events WHERE operation_id=?", first.OperationID) != 4 {
		t.Fatal("expected one outbox row for each S1-S4 target")
	}
	if task7Int(t, db, `SELECT COUNT(DISTINCT node_id || ':' || service_name) AS n
FROM outbox_events WHERE operation_id=?`, first.OperationID) != 4 ||
		task7Int(t, db, `SELECT COUNT(*) AS n FROM outbox_events
WHERE operation_id=? AND node_id='S1' AND service_name='maestro-core'`, first.OperationID) != 1 {
		t.Fatal("outbox is not unique per target or omitted fenced/down S1")
	}
}

func TestConcurrentConfirmLoserReresolvesSavedWinner(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+600, 71)
	winner := task7Service(t, db)
	order := task7Create(t, winner, customerID, "telegram_user", "concurrent-loser", "bot-a")
	task7Claim(t, winner, order.OrderID)
	reached := make(chan struct{})
	release := make(chan struct{})
	loser := task7Service(t, &task7ConfirmLoserGate{RQLite: db, reached: reached, release: release})
	command := task7Confirm(order.OrderID, "concurrent-loser", "receipt-concurrent-loser")
	loserResults := make(chan ConfirmPaymentResult, 1)
	loserErrors := make(chan error, 1)
	go func() {
		result, err := loser.ConfirmPayment(task7Context(t), command)
		loserResults <- result
		loserErrors <- err
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("loser did not finish its empty idempotency lookup")
	}
	winnerResult, err := winner.ConfirmPayment(task7Context(t), command)
	close(release)
	if err != nil {
		t.Fatalf("winner confirm: %v", err)
	}
	select {
	case loserErr := <-loserErrors:
		loserResult := <-loserResults
		if loserErr != nil || loserResult != winnerResult {
			t.Fatalf("loser result=%#v err=%v, want saved %#v", loserResult, loserErr, winnerResult)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loser did not re-resolve the committed winner")
	}
}

func TestSameKeyDifferentHashReturnsConflict(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+600, 1)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "70002", "bot-a")
	task7Claim(t, service, order.OrderID)
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(order.OrderID, "same-key", "receipt-a")); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	before := task7Snapshot(t, db)
	_, err := service.ConfirmPayment(task7Context(t), task7Confirm(order.OrderID, "same-key", "receipt-b"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("different hash error=%v", err)
	}
	if after := task7Snapshot(t, db); after != before {
		t.Fatalf("conflicting hash changed counts: before=%#v after=%#v", before, after)
	}
}

func TestLostResponseAfterCommitReturnsSavedResultAfterRestart(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+600, 2)
	baseService := task7Service(t, db)
	order := task7Create(t, baseService, customerID, "telegram_user", "70003", "bot-a")
	task7Claim(t, baseService, order.OrderID)
	fault := &task7WriteFault{delegate: db, failAt: 1}
	service := task7Service(t, fault)
	command := task7Confirm(order.OrderID, "lost-response", "receipt-lost")
	first, err := service.ConfirmPayment(task7Context(t), command)
	if err != nil {
		t.Fatalf("unknown outcome was not resolved: %v", err)
	}
	restarted := task7Service(t, db)
	second, err := restarted.ConfirmPayment(task7Context(t), command)
	if err != nil || second != first {
		t.Fatalf("restart result=%#v err=%v, want %#v", second, err, first)
	}
}

func TestTwoDifferentPaidOrdersBothExtendExpiry(t *testing.T) {
	db := task7DB(t)
	now := task7Now(t, db)
	customerID := task7SeedCustomer(t, db, "active", now+100, 3)
	service := task7Service(t, db)
	first := task7Create(t, service, customerID, "anonymous", "", "")
	second := task7Create(t, service, customerID, "anonymous", "", "")
	task7Claim(t, service, first.OrderID)
	task7Claim(t, service, second.OrderID)
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(first.OrderID, "confirm-first", "receipt-first")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(second.OrderID, "confirm-second", "receipt-second")); err != nil {
		t.Fatalf("second: %v", err)
	}
	row := task7Row(t, db, rqlite.Statement{SQL: "SELECT expires_at_unix,generation FROM customers WHERE customer_id=?", Args: []any{customerID}})
	expires, _ := rowInt64(row, "expires_at_unix")
	generation, _ := rowInt64(row, "generation")
	if expires != now+100+60*86400 || generation != 5 {
		t.Fatalf("expiry/generation=(%d,%d)", expires, generation)
	}
}

func TestConfirmVersusCancelHasOneTerminalWinner(t *testing.T) {
	db := task7DB(t)
	now := task7Now(t, db)
	customerID := task7SeedCustomer(t, db, "active", now+100, 4)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "70004", "bot-a")
	task7Claim(t, service, order.OrderID)
	start := make(chan struct{})
	var confirmErr, cancelErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, confirmErr = service.ConfirmPayment(task7Context(t), task7Confirm(order.OrderID, "race-confirm", "receipt-race"))
	}()
	go func() {
		defer wait.Done()
		<-start
		_, cancelErr = service.CancelOrder(task7Context(t), CancelOrderCommand{OrderID: order.OrderID, IdempotencyKey: "race-cancel", Actor: "owner", Channel: "web"})
	}()
	close(start)
	wait.Wait()
	if (confirmErr == nil) == (cancelErr == nil) {
		t.Fatalf("confirmErr=%v cancelErr=%v", confirmErr, cancelErr)
	}
	if confirmErr != nil && !errors.Is(confirmErr, ErrConflict) || cancelErr != nil && !errors.Is(cancelErr, ErrConflict) {
		t.Fatalf("loser error is not conflict: confirmErr=%v cancelErr=%v", confirmErr, cancelErr)
	}
	row := task7Row(t, db, rqlite.Statement{SQL: "SELECT payment_state FROM orders WHERE order_id=?", Args: []any{order.OrderID}})
	state, _ := rowString(row, "payment_state")
	payments := task7Int(t, db, "SELECT COUNT(*) AS n FROM payments WHERE order_id=?", order.OrderID)
	if (state == "confirmed" && payments != 1) || (state == "canceled" && payments != 0) {
		t.Fatalf("state=%q payments=%d", state, payments)
	}
}

func TestReceiptReferenceCannotCreditSiblingOrder(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 5)
	service := task7Service(t, db)
	first := task7Create(t, service, customerID, "anonymous", "", "")
	second := task7Create(t, service, customerID, "anonymous", "", "")
	task7Claim(t, service, first.OrderID)
	task7Claim(t, service, second.OrderID)
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(first.OrderID, "receipt-first-key", "one-bank-receipt")); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	before := task7Snapshot(t, db)
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(second.OrderID, "receipt-second-key", "one-bank-receipt")); !errors.Is(err, ErrConflict) {
		t.Fatalf("sibling receipt error=%v", err)
	}
	if after := task7Snapshot(t, db); after != before {
		t.Fatalf("duplicate receipt changed counts: before=%#v after=%#v", before, after)
	}
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM payments WHERE receipt_ref=?", "one-bank-receipt") != 1 {
		t.Fatal("receipt credited more than once")
	}
}

func TestBotActiveOrderGuardReturnsExistingOrder(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 6)
	service := task7Service(t, db)
	first := task7Create(t, service, customerID, "telegram_user", "70005", "bot-a")
	second := task7Create(t, service, customerID, "telegram_user", "70005", "bot-a")
	if second.OrderID != first.OrderID || task7Int(t, db, "SELECT COUNT(*) AS n FROM orders WHERE customer_id=?", customerID) != 1 {
		t.Fatalf("orders=(%q,%q)", first.OrderID, second.OrderID)
	}
}

func TestDuplicatePaidClaimCreatesOneOwnerEvent(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 7)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "70006", "bot-a")
	var wait sync.WaitGroup
	for i := 0; i < 50; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := service.MarkPaymentClaimed(task7Context(t), ClaimPaymentCommand{OrderID: order.OrderID, Actor: "buyer", Channel: "telegram"}); err != nil {
				t.Errorf("claim: %v", err)
			}
		}()
	}
	wait.Wait()
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM telegram_delivery_outbox WHERE dedupe_key=?", "owner-claim:"+order.OrderID) != 1 {
		t.Fatal("owner event count is not one")
	}
}

func TestNoQuorumMutationReturnsUnavailableWithoutPendingWrite(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 8)
	service := task7Service(t, task7NoQuorum{delegate: db})
	before := task7Snapshot(t, db)
	_, err := service.CreateOrder(task7Context(t), CreateOrderCommand{TariffVersionID: "tariff_1m_v1", CustomerID: customerID, BuyerScope: "telegram_user", BuyerIdentity: "70007", OriginBotID: "bot-a", ChatIdentity: "chat"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM orders WHERE customer_id=?", customerID) != 0 {
		t.Fatal("no-quorum create left a pending write")
	}
	if after := task7Snapshot(t, db); after != before {
		t.Fatalf("no-quorum mutation changed counts: before=%#v after=%#v", before, after)
	}
}

func TestExpiredCustomerRenewsFromConfirmedAt(t *testing.T) {
	db := task7DB(t)
	now := task7Now(t, db)
	customerID := task7SeedCustomer(t, db, "expired", now-10, 9)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "70008", "bot-a")
	task7Claim(t, service, order.OrderID)
	result, err := service.ConfirmPayment(task7Context(t), task7Confirm(order.OrderID, "renew-expired", "receipt-expired"))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if result.ExpiresAtUnix != now+30*86400 {
		t.Fatalf("expiry=%d want %d", result.ExpiresAtUnix, now+30*86400)
	}
}

func TestCallerCannotOverrideTariffSnapshot(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 10)
	service := task7Service(t, db)
	order, err := service.CreateOrder(task7Context(t), CreateOrderCommand{TariffVersionID: "tariff_1m_v1", CustomerID: customerID, BuyerScope: "anonymous", AmountMinor: 1, Currency: "USD", DurationSeconds: 1})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if order.AmountMinor != 40000 || order.Currency != "RUB" || order.DurationSeconds != 30*86400 {
		t.Fatalf("caller overrode snapshot: %#v", order)
	}
}

func TestSameTelegramBuyerAcrossBothBotsSharesActiveGuard(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 11)
	service := task7Service(t, db)
	first := task7Create(t, service, customerID, "telegram_user", "70009", "bot-a")
	second := task7Create(t, service, customerID, "telegram_user", "70009", "bot-b")
	if first.OrderID != second.OrderID || second.OriginBotID != "bot-a" {
		t.Fatalf("cross-bot orders=%#v %#v", first, second)
	}
}

func TestOrderExpiresAfterTwentyFourHoursAndReleasesGuard(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 12)
	orderID := task7SeedDueOrder(t, db, customerID, "created", "bot-a")
	service := task7Service(t, db)
	if _, err := service.OrderByID(task7Context(t), orderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OrderByID error=%v", err)
	}
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM active_order_guards WHERE order_id=?", orderID) != 0 {
		t.Fatal("expired order retained guard")
	}
}

func TestPaymentClaimedDoesNotAutoExpireBeforeOwnerDecision(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 13)
	orderID := task7SeedDueOrder(t, db, customerID, "payment_claimed", "bot-a")
	service := task7Service(t, db)
	if _, err := service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-claimed"}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	order, err := service.OrderByID(task7Context(t), orderID)
	if err != nil || order.PaymentState != PaymentClaimed {
		t.Fatalf("order=%#v err=%v", order, err)
	}
	if task7Int(t, db, "SELECT COUNT(*) AS n FROM telegram_delivery_outbox WHERE dedupe_key=?", "stale-owner-claim:"+orderID) != 1 {
		t.Fatal("stale claim alert count is not one")
	}
}

func TestExpireVersusConfirmHasOneTerminalWinner(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 14)
	orderID := task7SeedDueOrder(t, db, customerID, "created", "bot-a")
	service := task7Service(t, db)
	start := make(chan struct{})
	var confirmErr, sweepErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		if _, confirmErr = service.MarkPaymentClaimed(task7Context(t), ClaimPaymentCommand{OrderID: orderID, Actor: "buyer", Channel: "telegram", SourceEventID: "expire-race-claim"}); confirmErr != nil {
			return
		}
		_, confirmErr = service.ConfirmPayment(task7Context(t), task7Confirm(orderID, "expire-confirm", "receipt-expire"))
	}()
	go func() {
		defer wait.Done()
		<-start
		_, sweepErr = service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-expire-race"})
	}()
	close(start)
	wait.Wait()
	if sweepErr != nil && !errors.Is(sweepErr, ErrLeaseHeld) {
		t.Fatalf("confirmErr=%v sweepErr=%v", confirmErr, sweepErr)
	}
	row := task7Row(t, db, rqlite.Statement{SQL: "SELECT payment_state FROM orders WHERE order_id=?", Args: []any{orderID}})
	state, _ := rowString(row, "payment_state")
	payments := task7Int(t, db, "SELECT COUNT(*) AS n FROM payments WHERE order_id=?", orderID)
	if (state == "confirmed" && (confirmErr != nil || payments != 1)) ||
		(state == "expired" && (confirmErr == nil || payments != 0)) ||
		(state != "confirmed" && state != "expired") {
		t.Fatalf("state=%q payments=%d confirmErr=%v", state, payments, confirmErr)
	}
}

func TestCustomerExpirySweepRevokesEveryServiceOnce(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)-1, 20)
	service := task7Service(t, db)
	result, err := service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-customer"})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.CustomersExpired != 1 || task7Int(t, db, "SELECT COUNT(*) AS n FROM desired_node_state WHERE customer_id=? AND generation=21 AND tombstone=1", customerID) != 4 || task7Int(t, db, "SELECT COUNT(*) AS n FROM outbox_events WHERE operation_id=?", result.OperationID) != 4 {
		t.Fatalf("result=%#v", result)
	}
	if task7Int(t, db, "SELECT generation AS n FROM customers WHERE customer_id=?", customerID) != 21 {
		t.Fatal("customer generation did not increment once")
	}
}

func TestExpirySweepVersusRenewLatestGenerationWins(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)-1, 30)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "70010", "bot-a")
	task7Claim(t, service, order.OrderID)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, _ = service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-renew-race"})
	}()
	go func() {
		defer wait.Done()
		<-start
		if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(order.OrderID, "renew-race", "receipt-renew-race")); err != nil {
			t.Errorf("confirm: %v", err)
		}
	}()
	close(start)
	wait.Wait()
	row := task7Row(t, db, rqlite.Statement{SQL: "SELECT status,generation FROM customers WHERE customer_id=?", Args: []any{customerID}})
	status, _ := rowString(row, "status")
	generation, _ := rowInt64(row, "generation")
	if status != "active" || generation < 31 || task7Int(t, db, "SELECT COUNT(*) AS n FROM desired_node_state WHERE customer_id=? AND generation=?", customerID, generation) != 4 {
		t.Fatalf("latest state status=%q generation=%d", status, generation)
	}
}

func TestSweeperLeaseHasOneActiveHolder(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	task7Request(t, db, rqlite.Statement{SQL: "DELETE FROM cluster_job_leases WHERE job_name='expiry-sweeper'"})
	service := task7Service(t, db)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, worker := range []string{"worker-one", "worker-two"} {
		go func(worker string) {
			<-start
			_, err := service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: worker})
			errs <- err
		}(worker)
	}
	close(start)
	first, second := <-errs, <-errs
	if (first == nil) == (second == nil) {
		t.Fatalf("lease results=%v,%v", first, second)
	}
	if first != nil && !errors.Is(first, ErrLeaseHeld) || second != nil && !errors.Is(second, ErrLeaseHeld) {
		t.Fatalf("lease errors=%v,%v", first, second)
	}
}

func TestStaleSweeperFenceCannotExpireAfterLeaseHandoff(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	task7Request(t, db, rqlite.Statement{SQL: "DELETE FROM cluster_job_leases WHERE job_name='expiry-sweeper'"})
	service := task7Service(t, db)
	first, err := service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "stale-worker"})
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}
	task7Request(t, db, rqlite.Statement{SQL: `UPDATE cluster_job_leases
SET acquired_at_unix=unixepoch()-2,expires_at_unix=unixepoch()-1
WHERE job_name='expiry-sweeper'`})
	second, err := service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "new-worker"})
	if err != nil || second.LeaseFence <= first.LeaseFence {
		t.Fatalf("handoff=%#v err=%v first=%#v", second, err, first)
	}
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)-1, 40)
	before := task7Snapshot(t, db)
	_, err = service.ExpireDueCustomers(task7Context(t), ExpiryLease{WorkerID: "stale-worker", LeaseFence: first.LeaseFence})
	if !errors.Is(err, ErrLeaseLost) || task7Int(t, db, "SELECT generation AS n FROM customers WHERE customer_id=?", customerID) != 40 {
		t.Fatalf("stale fence error=%v", err)
	}
	if after := task7Snapshot(t, db); after != before {
		t.Fatalf("stale fence changed counts: before=%#v after=%#v", before, after)
	}
}

func TestSweeperNoQuorumDoesNoSideEffect(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)-1, 50)
	service := task7Service(t, task7NoQuorum{delegate: db})
	before := task7Snapshot(t, db)
	_, err := service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-no-quorum"})
	if !errors.Is(err, ErrUnavailable) || task7Int(t, db, "SELECT generation AS n FROM customers WHERE customer_id=?", customerID) != 50 || task7Int(t, db, "SELECT COUNT(*) AS n FROM desired_node_state WHERE customer_id=?", customerID) != 0 {
		t.Fatalf("error=%v", err)
	}
	if after := task7Snapshot(t, db); after != before {
		t.Fatalf("no-quorum sweep changed counts: before=%#v after=%#v", before, after)
	}
}

func TestSweeperCrashAfterCommitDoesNotIncrementAgain(t *testing.T) {
	db := task7DB(t)
	task7Activate(t, db)
	task7Request(t, db, rqlite.Statement{SQL: "DELETE FROM cluster_job_leases WHERE job_name='expiry-sweeper'"})
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)-1, 60)
	fault := &task7WriteFault{delegate: db, failAt: 2}
	service := task7Service(t, fault)
	_, _ = service.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-crash"})
	restarted := task7Service(t, db)
	if _, err := restarted.RunExpirySweep(task7Context(t), ExpirySweepCommand{WorkerID: "worker-crash"}); err != nil {
		t.Fatalf("restart sweep: %v", err)
	}
	if task7Int(t, db, "SELECT generation AS n FROM customers WHERE customer_id=?", customerID) != 61 || task7Int(t, db, "SELECT COUNT(*) AS n FROM outbox_events WHERE aggregate_id LIKE ?", customerID+":%") != 4 {
		t.Fatal("crash replay incremented or emitted again")
	}
}

func TestSavedIdempotencyResponseContainsNoPrivateMaterial(t *testing.T) {
	db := task7DB(t)
	customerID := task7SeedCustomer(t, db, "active", task7Now(t, db)+100, 70)
	service := task7Service(t, db)
	order := task7Create(t, service, customerID, "telegram_user", "private-user-marker", "bot-a")
	task7Claim(t, service, order.OrderID)
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(order.OrderID, "saved-redacted", "receipt-private")); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	row := task7Row(t, db, rqlite.Statement{SQL: "SELECT response_json FROM idempotency_requests WHERE idempotency_key=?", Args: []any{"saved-redacted"}})
	saved, _ := rowString(row, "response_json")
	for _, forbidden := range []string{"private-user-marker", "sub-token-private", "https://private.invalid/sub", "protocol-password"} {
		if strings.Contains(saved, forbidden) {
			t.Fatalf("saved response contains private material %q: %s", forbidden, saved)
		}
	}
}
