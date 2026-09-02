//go:build rqlite_integration

package controlplane

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestWhiteListTopUpSchemaRejectsPublicationForUnpaidOrder(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	orderID := "order_" + task7Name(t, "unpaid-whitelist-topup")
	operationID := "operation_" + task7Name(t, "unpaid-whitelist-topup")
	createdAt := fixture.NowUnix
	requestHash := whiteListDigest("unpaid-whitelist-topup-request")

	task7Request(t, fixture.DB,
		rqlite.Statement{
			SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,operation_id)
VALUES(?,?,?,?,?,'wl-gb-5-v1',10000,'RUB',1,?,?,'created','none',NULL,?)`,
			Args: []any{
				orderID, whiteListPaymentCode(orderID), "whitelist-topup",
				whiteListDigest(orderID), fixture.CustomerID, createdAt, createdAt + 86400, operationID,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_topup_orders(
order_id,entitlement_id,product_id,creation_request_hash,created_at_unix)
VALUES(?,?,?,?,?)`,
			Args: []any{orderID, fixture.EntitlementID, "wl-gb-5-v1", requestHash, createdAt},
		},
	)

	mustRequestFail(t, task7Context(t), fixture.DB, rqlite.Statement{
		SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix)
VALUES(?,?,2,1,'CONFIRMED_GB_PURCHASE',?,?,?,?)`,
		Args: []any{
			"control_" + task7Name(t, "unpaid-whitelist-topup"), fixture.EntitlementID,
			orderID, operationID, requestHash, createdAt,
		},
	})
}

func TestWhiteListTopUpConfirmationCommitsOnceAndReplaysAfterUnknownOutcome(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	service := whiteListBalanceRQLiteService(t, fixture.DB, fixture.NowUnix, "topup-create")
	created, err := service.CreateWhiteListTopUpOrder(task7Context(t), CreateWhiteListTopUpOrderCommand{
		EntitlementID: fixture.EntitlementID, ProductID: "wl-gb-5-v1",
		IdempotencyKey: "create-topup-once", BuyerScope: "telegram", BuyerIdentity: fixture.CustomerID,
		Actor: "customer", Channel: "telegram",
	})
	if err != nil {
		t.Fatalf("create top-up: %v", err)
	}
	if _, err = service.ClaimWhiteListTopUpPayment(task7Context(t), ClaimWhiteListTopUpPaymentCommand{
		OrderID: created.OrderID, IdempotencyKey: "claim-topup-once", Actor: "customer", Channel: "telegram",
	}); err != nil {
		t.Fatalf("claim top-up: %v", err)
	}

	ambiguousDB := &committedUnknownRQLite{delegate: fixture.DB}
	ambiguousService := whiteListBalanceRQLiteService(t, ambiguousDB, fixture.NowUnix, "topup-confirm")
	command := ConfirmWhiteListTopUpPaymentCommand{
		OrderID: created.OrderID, IdempotencyKey: "confirm-topup-once",
		PaymentReference: "manual-bank-reference", Provider: "manual", Actor: "owner", Channel: "panel",
	}
	confirmed, err := ambiguousService.ConfirmWhiteListTopUpPayment(task7Context(t), command)
	if err != nil {
		t.Fatalf("confirm committed unknown top-up: %v", err)
	}
	if ambiguousDB.requestCalls.Load() != 1 {
		t.Fatalf("confirmation writes=%d, want one", ambiguousDB.requestCalls.Load())
	}

	restarted := whiteListBalanceRQLiteService(t, fixture.DB, fixture.NowUnix, "topup-restart")
	replayed, err := restarted.ConfirmWhiteListTopUpPayment(task7Context(t), command)
	if err != nil {
		t.Fatalf("replay confirmed top-up: %v", err)
	}
	if replayed != confirmed {
		t.Fatalf("replay=%#v, want %#v", replayed, confirmed)
	}
	for name, count := range map[string]int64{
		"payment": task7Int(t, fixture.DB,
			"SELECT COUNT(*) AS n FROM payments WHERE order_id=?", created.OrderID),
		"credit": task7Int(t, fixture.DB,
			"SELECT COUNT(*) AS n FROM whitelist_balance_entries WHERE source_order_id=? AND kind='PURCHASED_CREDIT'", created.OrderID),
		"result": task7Int(t, fixture.DB,
			"SELECT COUNT(*) AS n FROM whitelist_topup_results WHERE order_id=? AND decision='CONFIRMED'", created.OrderID),
		"publication": task7Int(t, fixture.DB,
			"SELECT COUNT(*) AS n FROM whitelist_publication_controls WHERE source_topup_order_id=?", created.OrderID),
	} {
		if count != 1 {
			t.Fatalf("%s rows=%d, want one", name, count)
		}
	}
	projection := task7Row(t, fixture.DB, rqlite.Statement{
		SQL:  "SELECT purchased_remaining_bytes,version FROM whitelist_balance_projections WHERE entitlement_id=?",
		Args: []any{fixture.EntitlementID},
	})
	remaining, remainingOK := rowInt64(projection, "purchased_remaining_bytes")
	version, versionOK := rowInt64(projection, "version")
	if !remainingOK || !versionOK || remaining != 5000000000 || version != confirmed.BalanceVersion {
		t.Fatalf("projection=%#v confirmation=%#v", projection, confirmed)
	}
}

func TestWhiteListTopUpCannotUseLegacyPaymentConfirmation(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	service := whiteListBalanceRQLiteService(t, fixture.DB, fixture.NowUnix, "topup-legacy-block")
	created, err := service.CreateWhiteListTopUpOrder(task7Context(t), CreateWhiteListTopUpOrderCommand{
		EntitlementID: fixture.EntitlementID, ProductID: "wl-gb-5-v1",
		IdempotencyKey: "create-topup-legacy", BuyerScope: "telegram", BuyerIdentity: fixture.CustomerID,
	})
	if err != nil {
		t.Fatalf("create top-up: %v", err)
	}
	if _, err = service.ClaimWhiteListTopUpPayment(task7Context(t), ClaimWhiteListTopUpPaymentCommand{
		OrderID: created.OrderID, IdempotencyKey: "claim-topup-legacy",
	}); err != nil {
		t.Fatalf("claim top-up: %v", err)
	}
	if _, err = service.ConfirmPayment(task7Context(t), ConfirmPaymentCommand{
		OrderID: created.OrderID, TariffVersionID: "wl-gb-5-v1",
		PaymentReference: "legacy-payment-reference", IdempotencyKey: "legacy-confirm-topup",
	}); err == nil {
		t.Fatal("legacy confirmation accepted a white-list top-up")
	}
	if payments := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM payments WHERE order_id=?", created.OrderID); payments != 0 {
		t.Fatalf("legacy confirmation persisted %d payment(s)", payments)
	}
	if credits := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_balance_entries WHERE source_order_id=?", created.OrderID); credits != 0 {
		t.Fatalf("legacy confirmation persisted %d credit(s)", credits)
	}
}

func TestPendingOrdinaryRenewalSerializesWhiteListTopUpConfirmation(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	boundaryNow := fixture.NowUnix + 86400
	service := whiteListBalanceRQLiteService(t, fixture.DB, boundaryNow, "renewal-topup-gate")
	topup, err := service.CreateWhiteListTopUpOrder(task7Context(t), CreateWhiteListTopUpOrderCommand{
		EntitlementID: fixture.EntitlementID, ProductID: "wl-gb-5-v1",
		IdempotencyKey: "create-topup-before-renewal", BuyerScope: "telegram",
		BuyerIdentity: fixture.CustomerID, Actor: "customer", Channel: "telegram",
	})
	if err != nil {
		t.Fatalf("create top-up: %v", err)
	}
	if _, err = service.ClaimWhiteListTopUpPayment(task7Context(t), ClaimWhiteListTopUpPaymentCommand{
		OrderID: topup.OrderID, IdempotencyKey: "claim-topup-before-renewal",
		Actor: "customer", Channel: "telegram",
	}); err != nil {
		t.Fatalf("claim top-up: %v", err)
	}
	renewal := task7Create(t, service, fixture.CustomerID, "telegram_user", "renewal-before-topup", "bot-a")
	task7Claim(t, service, renewal.OrderID)
	command := ConfirmWhiteListTopUpPaymentCommand{
		OrderID: topup.OrderID, IdempotencyKey: "confirm-topup-after-renewal",
		PaymentReference: "manual-topup-after-renewal", Provider: "manual",
		Actor: "owner", Channel: "panel",
	}
	interleavedDB := &afterFirstBalanceReadRQLite{
		RQLite: fixture.DB,
		afterRead: func() error {
			_, confirmErr := service.ConfirmPayment(task7Context(t), task7Confirm(
				renewal.OrderID, "renewal-before-topup-confirm", "renewal-before-topup-receipt",
			))
			return confirmErr
		},
	}
	interleavedService := whiteListBalanceRQLiteService(
		t, interleavedDB, boundaryNow, "renewal-topup-interleaving",
	)
	if _, err = interleavedService.ConfirmWhiteListTopUpPayment(task7Context(t), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("top-up while renewal pending error=%v, want ErrUnavailable", err)
	}
	if interleavedDB.afterReadCalls != 1 {
		t.Fatalf("interleaving callback calls=%d, want one", interleavedDB.afterReadCalls)
	}
	if pending := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_renewal_intents WHERE access_order_id=? AND status='pending'", renewal.OrderID); pending != 1 {
		t.Fatalf("interleaved pending renewal intents=%d, want one", pending)
	}
	if payments := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM payments WHERE order_id=?", topup.OrderID); payments != 0 {
		t.Fatalf("pending renewal allowed %d top-up payment(s)", payments)
	}
	if credits := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_balance_entries WHERE source_order_id=?", topup.OrderID); credits != 0 {
		t.Fatalf("pending renewal allowed %d top-up credit(s)", credits)
	}
	if results := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_topup_results WHERE order_id=?", topup.OrderID); results != 0 {
		t.Fatalf("pending renewal allowed %d top-up result(s)", results)
	}
	if controls := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_publication_controls WHERE source_topup_order_id=?", topup.OrderID); controls != 0 {
		t.Fatalf("pending renewal allowed %d publication control(s)", controls)
	}
	if periods := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_billing_periods WHERE access_order_id=?", renewal.OrderID); periods != 0 {
		t.Fatalf("ordinary renewal synchronously created %d period(s)", periods)
	}
	stateRow := task7Row(t, fixture.DB, rqlite.Statement{
		SQL: "SELECT payment_state FROM orders WHERE order_id=?", Args: []any{topup.OrderID},
	})
	state, stateOK := rowString(stateRow, "payment_state")
	if !stateOK || state != string(PaymentClaimed) {
		t.Fatalf("pending renewal changed top-up state=%q row=%#v", state, stateRow)
	}
	if applied, reconcileErr := service.ReconcileWhiteListRenewalIntents(task7Context(t), 8); reconcileErr != nil || applied != 1 {
		t.Fatalf("renewal reconcile=(%d,%v), want (1,nil)", applied, reconcileErr)
	}
	confirmed, err := service.ConfirmWhiteListTopUpPayment(task7Context(t), command)
	if err != nil {
		t.Fatalf("confirm top-up after renewal reconcile: %v", err)
	}
	if confirmed.PurchasedBytes != 5000000000 {
		t.Fatalf("confirmed purchased bytes=%d", confirmed.PurchasedBytes)
	}
	if credits := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_balance_entries WHERE source_order_id=? AND kind='PURCHASED_CREDIT'", topup.OrderID); credits != 1 {
		t.Fatalf("post-reconcile top-up credits=%d, want one", credits)
	}
}

type afterFirstBalanceReadRQLite struct {
	rqlite.RQLite
	once           sync.Once
	afterRead      func() error
	afterReadErr   error
	afterReadCalls int
}

func (db *afterFirstBalanceReadRQLite) QueryLinearizable(
	ctx context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	results, err := db.RQLite.QueryLinearizable(ctx, statements...)
	if err != nil || len(statements) != 1 {
		return results, err
	}
	sql := strings.ToLower(statements[0].SQL)
	if !strings.Contains(sql, "from whitelist_entitlement_identities as entitlement") ||
		!strings.Contains(sql, "renewal_intent_pending") {
		return results, nil
	}
	db.once.Do(func() {
		db.afterReadCalls++
		if db.afterRead != nil {
			db.afterReadErr = db.afterRead()
		}
	})
	if db.afterReadErr != nil {
		return nil, db.afterReadErr
	}
	return results, nil
}

func TestOrdinaryRenewalsQueueZeroGrantWhiteListPeriodsWithoutPublishing(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	service := whiteListBalanceRQLiteService(t, fixture.DB, fixture.NowUnix, "ordinary-renewal")
	firstOrder := task7Create(t, service, fixture.CustomerID, "telegram_user", "renewal-one", "bot-a")
	task7Claim(t, service, firstOrder.OrderID)
	dirtyBeforeFirstConfirm := task7Int(t, fixture.DB,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1")
	first, err := service.ConfirmPayment(task7Context(t), task7Confirm(
		firstOrder.OrderID, "ordinary-renewal-confirm-one", "ordinary-renewal-receipt-one",
	))
	if err != nil {
		t.Fatalf("confirm first ordinary renewal: %v", err)
	}
	if dirty := task7Int(t, fixture.DB,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1"); dirty != dirtyBeforeFirstConfirm+1 {
		t.Fatalf("first ordinary renewal dirty generation=%d, want %d", dirty, dirtyBeforeFirstConfirm+1)
	}
	secondOrder := task7Create(t, service, fixture.CustomerID, "telegram_user", "renewal-two", "bot-a")
	task7Claim(t, service, secondOrder.OrderID)
	dirtyBeforeSecondConfirm := task7Int(t, fixture.DB,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1")
	second, err := service.ConfirmPayment(task7Context(t), task7Confirm(
		secondOrder.OrderID, "ordinary-renewal-confirm-two", "ordinary-renewal-receipt-two",
	))
	if err != nil {
		t.Fatalf("confirm second ordinary renewal: %v", err)
	}
	if dirty := task7Int(t, fixture.DB,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1"); dirty != dirtyBeforeSecondConfirm+1 {
		t.Fatalf("second ordinary renewal dirty generation=%d, want %d", dirty, dirtyBeforeSecondConfirm+1)
	}
	if pending := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_renewal_intents WHERE entitlement_id=? AND status='pending'", fixture.EntitlementID); pending != 2 {
		t.Fatalf("pending renewal intents=%d, want 2", pending)
	}
	if periods := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_billing_periods WHERE entitlement_id=?", fixture.EntitlementID); periods != 1 {
		t.Fatalf("ordinary confirm synchronously changed billing periods=%d", periods)
	}
	mustRequestFail(t, task7Context(t), fixture.DB, rqlite.Statement{
		SQL: `UPDATE whitelist_renewal_intents
SET status='applied',period_id=?,projection_version=1,applied_at_unix=?
WHERE access_order_id=?`,
		Args: []any{fixture.PeriodID, fixture.NowUnix, firstOrder.OrderID},
	})
	dirtyBeforeReconcile := task7Int(t, fixture.DB,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1")
	ambiguousDB := &committedUnknownRQLite{delegate: fixture.DB}
	ambiguousService := whiteListBalanceRQLiteService(t, ambiguousDB, fixture.NowUnix, "ordinary-renewal-unknown")
	applied, err := ambiguousService.ReconcileWhiteListRenewalIntents(task7Context(t), 8)
	if err != nil || applied != 1 {
		t.Fatalf("committed-unknown reconcile=(%d,%v), want (1,nil)", applied, err)
	}
	applied, err = service.ReconcileWhiteListRenewalIntents(task7Context(t), 8)
	if err != nil || applied != 1 {
		t.Fatalf("second ordered reconcile=(%d,%v), want (1,nil)", applied, err)
	}
	if replayed, replayErr := service.ReconcileWhiteListRenewalIntents(task7Context(t), 8); replayErr != nil || replayed != 0 {
		t.Fatalf("empty replay reconcile=(%d,%v), want (0,nil)", replayed, replayErr)
	}
	if completed := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_renewal_intents WHERE entitlement_id=? AND status='applied'", fixture.EntitlementID); completed != 2 {
		t.Fatalf("applied renewal intents=%d, want 2", completed)
	}
	if dirty := task7Int(t, fixture.DB,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1"); dirty != dirtyBeforeReconcile+2 {
		t.Fatalf("renewal intents dirty generation=%d, want %d", dirty, dirtyBeforeReconcile+2)
	}

	if periods := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_billing_periods WHERE entitlement_id=?", fixture.EntitlementID); periods != 3 {
		t.Fatalf("billing periods=%d, want initial plus two renewals", periods)
	}
	firstPeriod := task7Row(t, fixture.DB, rqlite.Statement{
		SQL: `SELECT period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id
FROM whitelist_billing_periods WHERE entitlement_id=? AND access_order_id=?`,
		Args: []any{fixture.EntitlementID, firstOrder.OrderID},
	})
	secondPeriod := task7Row(t, fixture.DB, rqlite.Statement{
		SQL: `SELECT period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id
FROM whitelist_billing_periods WHERE entitlement_id=? AND access_order_id=?`,
		Args: []any{fixture.EntitlementID, secondOrder.OrderID},
	})
	firstStart, _ := rowInt64(firstPeriod, "starts_at_unix")
	firstEnd, _ := rowInt64(firstPeriod, "ends_at_unix")
	firstGrant, _ := rowInt64(firstPeriod, "included_grant_bytes")
	secondStart, _ := rowInt64(secondPeriod, "starts_at_unix")
	secondEnd, _ := rowInt64(secondPeriod, "ends_at_unix")
	secondGrant, _ := rowInt64(secondPeriod, "included_grant_bytes")
	if firstStart != fixture.NowUnix+86400 || firstEnd != first.ExpiresAtUnix || firstGrant != 0 ||
		secondStart != firstEnd || secondEnd != second.ExpiresAtUnix || secondGrant != 0 {
		t.Fatalf("renewal periods first=%#v second=%#v", firstPeriod, secondPeriod)
	}
	if entries := task7Int(t, fixture.DB,
		"SELECT COUNT(*) AS n FROM whitelist_balance_entries WHERE entitlement_id=?", fixture.EntitlementID); entries != 0 {
		t.Fatalf("ordinary renewals created %d balance entries", entries)
	}
	publication := task7Row(t, fixture.DB, rqlite.Statement{
		SQL: `SELECT COUNT(*) AS n,MAX(version) AS version,MAX(enabled) AS enabled
FROM whitelist_publication_controls WHERE entitlement_id=?`,
		Args: []any{fixture.EntitlementID},
	})
	count, _ := rowInt64(publication, "n")
	version, _ := rowInt64(publication, "version")
	enabled, _ := rowInt64(publication, "enabled")
	if count != 1 || version != 1 || enabled != 0 {
		t.Fatalf("ordinary renewal changed publication=%#v", publication)
	}
}

func TestOrdinaryRenewalWithoutWhiteListBalanceDoesNotCreateIntent(t *testing.T) {
	db := task7DB(t)
	nowUnix := task7Now(t, db)
	customerID := "customer_" + task7Name(t, "ordinary-no-whitelist")
	task7SeedCanonicalFixtureCustomer(
		t, db, task7FixtureSecretBox(t), customerID, "active", nowUnix+90*86400, 7,
	)
	service := whiteListBalanceRQLiteService(t, db, nowUnix, "ordinary-no-whitelist")
	order := task7Create(t, service, customerID, "telegram_user", "renewal-no-whitelist", "bot-a")
	task7Claim(t, service, order.OrderID)
	dirtyBefore := task7Int(t, db,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1")
	if _, err := service.ConfirmPayment(task7Context(t), task7Confirm(
		order.OrderID, "ordinary-no-whitelist-confirm", "ordinary-no-whitelist-receipt",
	)); err != nil {
		t.Fatalf("ordinary confirm without white-list balance: %v", err)
	}
	if intents := task7Int(t, db,
		"SELECT COUNT(*) AS n FROM whitelist_renewal_intents WHERE access_order_id=?", order.OrderID); intents != 0 {
		t.Fatalf("ordinary no-white-list renewal created %d intent(s)", intents)
	}
	if payments := task7Int(t, db,
		"SELECT COUNT(*) AS n FROM payments WHERE order_id=?", order.OrderID); payments != 1 {
		t.Fatalf("ordinary no-white-list renewal payments=%d, want 1", payments)
	}
	if dirty := task7Int(t, db,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1"); dirty != dirtyBefore+1 {
		t.Fatalf("ordinary no-white-list dirty generation=%d, want %d", dirty, dirtyBefore+1)
	}
}
