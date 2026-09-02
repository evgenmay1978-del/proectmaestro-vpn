//go:build rqlite_integration

package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

type whiteListBalanceRQLiteFixture struct {
	DB                 rqlite.RQLite
	NowUnix            int64
	CustomerID         string
	OtherCustomerID    string
	EntitlementID      string
	PeriodID           string
	AccessOrderID      string
	PurchaseOrderID    string
	SecondPurchaseID   string
	UnconfirmedOrderID string
	ForeignOrderID     string
}

type whiteListOrdinaryState struct {
	Status             string
	ExpiresAtUnix      int64
	Generation         int64
	OrderCount         int64
	TokenCount         int64
	RevokedTokenCount  int64
	CredentialCount    int64
	EnabledCredentials int64
	EntitlementCount   int64
	DeviceCount        int64
	PaymentCount       int64
	DesiredStateCount  int64
	DesiredTagCount    int64
	OutboxCount        int64
}

type whiteListProjectionRow struct {
	CurrentPeriodID    string
	IncludedRemaining  int64
	PurchasedRemaining int64
	LifetimeConsumed   int64
	Uncovered          int64
	Version            int64
	Pending            int64
	FreshThroughUnix   int64
}

type whiteListBalanceStateReadBarrier struct {
	rqlite.RQLite
	reached chan struct{}
	release <-chan struct{}
	reads   atomic.Int32
	once    sync.Once
}

func (db *whiteListBalanceStateReadBarrier) QueryLinearizable(
	ctx context.Context,
	statements ...rqlite.Statement,
) ([]rqlite.Result, error) {
	results, err := db.RQLite.QueryLinearizable(ctx, statements...)
	if err != nil || len(statements) != 1 ||
		!strings.Contains(strings.ToLower(statements[0].SQL), "from whitelist_entitlement_identities as entitlement") {
		return results, err
	}
	if db.reads.Add(1) >= 2 {
		db.once.Do(func() { close(db.reached) })
	}
	select {
	case <-db.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return results, nil
}

func newWhiteListBalanceRQLiteFixture(t *testing.T) whiteListBalanceRQLiteFixture {
	t.Helper()
	db := task7DB(t)
	nowUnix := task7Now(t, db)
	customerID := "customer_" + task7Name(t, "whitelist-customer")
	otherCustomerID := "customer_" + task7Name(t, "whitelist-other-customer")
	task7SeedCanonicalFixtureCustomer(
		t, db, task7FixtureSecretBox(t), customerID, "active", nowUnix+90*86400, 7,
	)
	task7SeedCanonicalFixtureCustomer(
		t, db, task7FixtureSecretBox(t), otherCustomerID, "active", nowUnix+90*86400, 3,
	)

	fixture := whiteListBalanceRQLiteFixture{
		DB:                 db,
		NowUnix:            nowUnix,
		CustomerID:         customerID,
		OtherCustomerID:    otherCustomerID,
		EntitlementID:      whiteListEntitlementID(t.Name()),
		PeriodID:           "period_" + task7Name(t, "whitelist-period"),
		AccessOrderID:      "order_" + task7Name(t, "whitelist-access-order"),
		PurchaseOrderID:    "order_" + task7Name(t, "whitelist-purchase-order"),
		SecondPurchaseID:   "order_" + task7Name(t, "whitelist-purchase-order-2"),
		UnconfirmedOrderID: "order_" + task7Name(t, "whitelist-unconfirmed-order"),
		ForeignOrderID:     "order_" + task7Name(t, "whitelist-foreign-order"),
	}
	task7Request(t, db,
		rqlite.Statement{
			SQL:  "INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES(?,?,?)",
			Args: []any{fixture.EntitlementID, customerID, nowUnix - 100},
		},
		whiteListConfirmedOrderStatement(fixture.AccessOrderID, customerID, nowUnix),
		whiteListConfirmedOrderStatement(fixture.PurchaseOrderID, customerID, nowUnix),
		whiteListConfirmedOrderStatement(fixture.SecondPurchaseID, customerID, nowUnix),
		whiteListUnconfirmedOrderStatement(fixture.UnconfirmedOrderID, customerID, nowUnix),
		whiteListConfirmedOrderStatement(fixture.ForeignOrderID, otherCustomerID, nowUnix),
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
VALUES(?,?,0,?,?,0,?,?)`,
			Args: []any{
				fixture.PeriodID, fixture.EntitlementID, nowUnix - 1000,
				nowUnix + 86400, fixture.AccessOrderID, nowUnix - 100,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix)
VALUES(?,?,0,0,0,0,1,0,0,?)`,
			Args: []any{fixture.EntitlementID, fixture.PeriodID, nowUnix - 100},
		},
	)
	return fixture
}

func whiteListConfirmedOrderStatement(orderID, customerID string, nowUnix int64) rqlite.Statement {
	createdAt := nowUnix - 100
	return rqlite.Statement{
		SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'confirmed','applied','confirmed',?,?,1,?)`,
		Args: []any{
			orderID, whiteListPaymentCode(orderID), "whitelist-rqlite",
			whiteListDigest(orderID), customerID, createdAt, createdAt + 86400,
			nowUnix - 50, nowUnix + 90*86400, orderID + "-operation",
		},
	}
}

func whiteListUnconfirmedOrderStatement(orderID, customerID string, nowUnix int64) rqlite.Statement {
	createdAt := nowUnix - 100
	return rqlite.Statement{
		SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,operation_id)
VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'created','none',?)`,
		Args: []any{
			orderID, whiteListPaymentCode(orderID), "whitelist-rqlite",
			whiteListDigest(orderID), customerID, createdAt, createdAt + 86400,
			orderID + "-operation",
		},
	}
}

func whiteListPaymentCode(value string) string {
	digest := sha256.Sum256([]byte("payment-code:" + value))
	return strings.ToUpper(hex.EncodeToString(digest[:6]))
}

func whiteListDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func whiteListEntitlementID(value string) string {
	digest := sha256.Sum256([]byte("entitlement:" + value))
	return "wl-ent-" + hex.EncodeToString(digest[:16])
}

func whiteListBalanceRQLiteService(
	t *testing.T,
	db rqlite.RQLite,
	nowUnix int64,
	suffix string,
) *Service {
	t.Helper()
	clock := fixedClock{value: time.Unix(nowUnix, 0)}
	store, err := NewStore(db, task7FixtureSecretBox(t), clock)
	if err != nil {
		t.Fatalf("white-list balance store: %v", err)
	}
	service, err := NewService(store, &task7IDs{prefix: task7Name(t, suffix)}, clock)
	if err != nil {
		t.Fatalf("white-list balance service: %v", err)
	}
	return service
}

func whiteListOrdinarySnapshot(
	t *testing.T,
	db rqlite.RQLite,
	customerID string,
) whiteListOrdinaryState {
	t.Helper()
	row := task7Row(t, db, rqlite.Statement{
		SQL:  "SELECT status,expires_at_unix,generation FROM customers WHERE customer_id=?",
		Args: []any{customerID},
	})
	status, statusOK := rowString(row, "status")
	expiresAt, expiresOK := rowInt64(row, "expires_at_unix")
	generation, generationOK := rowInt64(row, "generation")
	if !statusOK || !expiresOK || !generationOK {
		t.Fatalf("invalid ordinary customer row: %#v", row)
	}
	return whiteListOrdinaryState{
		Status: status, ExpiresAtUnix: expiresAt, Generation: generation,
		OrderCount: task7Int(t, db,
			"SELECT COUNT(*) AS n FROM orders WHERE customer_id=?", customerID),
		TokenCount: task7Int(t, db,
			"SELECT COUNT(*) AS n FROM subscription_tokens WHERE customer_id=?", customerID),
		RevokedTokenCount: task7Int(t, db,
			"SELECT COALESCE(SUM(revoked),0) AS n FROM subscription_tokens WHERE customer_id=?", customerID),
		CredentialCount: task7Int(t, db,
			"SELECT COUNT(*) AS n FROM credentials WHERE customer_id=?", customerID),
		EnabledCredentials: task7Int(t, db,
			"SELECT COALESCE(SUM(enabled),0) AS n FROM credentials WHERE customer_id=?", customerID),
		EntitlementCount: task7Int(t, db, `SELECT COUNT(*) AS n
FROM whitelist_entitlement_identities WHERE customer_id=?`, customerID),
		DeviceCount: task7Int(t, db,
			"SELECT COUNT(*) AS n FROM devices WHERE customer_id=?", customerID),
		PaymentCount: task7Int(t, db, `SELECT COUNT(*) AS n
FROM payments AS payment
JOIN orders AS source_order ON source_order.order_id=payment.order_id
WHERE source_order.customer_id=?`, customerID),
		DesiredStateCount: task7Int(t, db,
			"SELECT COUNT(*) AS n FROM desired_node_state WHERE customer_id=?", customerID),
		DesiredTagCount: task7Int(t, db,
			"SELECT COUNT(*) AS n FROM desired_protocol_tags WHERE customer_id=?", customerID),
		OutboxCount: task7Int(t, db, `SELECT COUNT(*) AS n
FROM outbox_events
WHERE aggregate_id=?
   OR substr(aggregate_id,1,length(?)+1)=?||':'
   OR aggregate_id IN (
	SELECT order_id FROM orders WHERE customer_id=?
)`, customerID, customerID, customerID, customerID),
	}
}

func assertWhiteListOrdinaryUnchanged(
	t *testing.T,
	db rqlite.RQLite,
	customerID string,
	before whiteListOrdinaryState,
) {
	t.Helper()
	if after := whiteListOrdinarySnapshot(t, db, customerID); after != before {
		t.Fatalf("ordinary VPN state changed for %s: before=%#v after=%#v",
			customerID, before, after)
	}
}

func whiteListProjection(
	t *testing.T,
	db rqlite.RQLite,
	entitlementID string,
) whiteListProjectionRow {
	t.Helper()
	row := task7Row(t, db, rqlite.Statement{SQL: `SELECT
COALESCE(current_period_id,'') AS current_period_id,
included_remaining_bytes,purchased_remaining_bytes,lifetime_consumed_bytes,
uncovered_bytes,version,pending,fresh_through_unix
FROM whitelist_balance_projections WHERE entitlement_id=?`, Args: []any{entitlementID}})
	currentPeriodID, currentOK := rowString(row, "current_period_id")
	included, includedOK := rowInt64(row, "included_remaining_bytes")
	purchased, purchasedOK := rowInt64(row, "purchased_remaining_bytes")
	consumed, consumedOK := rowInt64(row, "lifetime_consumed_bytes")
	uncovered, uncoveredOK := rowInt64(row, "uncovered_bytes")
	version, versionOK := rowInt64(row, "version")
	pending, pendingOK := rowInt64(row, "pending")
	fresh, freshOK := rowInt64(row, "fresh_through_unix")
	if !currentOK || !includedOK || !purchasedOK || !consumedOK ||
		!uncoveredOK || !versionOK || !pendingOK || !freshOK {
		t.Fatalf("invalid white-list projection row: %#v", row)
	}
	return whiteListProjectionRow{
		CurrentPeriodID: currentPeriodID, IncludedRemaining: included,
		PurchasedRemaining: purchased, LifetimeConsumed: consumed,
		Uncovered: uncovered, Version: version, Pending: pending,
		FreshThroughUnix: fresh,
	}
}

func whiteListDirtyGeneration(t *testing.T, db rqlite.RQLite) int64 {
	t.Helper()
	return task7Int(t, db,
		"SELECT dirty_generation AS n FROM backup_rpo_state WHERE singleton_id=1")
}

func whiteListCount(
	t *testing.T,
	db rqlite.RQLite,
	sql string,
	args ...any,
) int64 {
	t.Helper()
	return task7Int(t, db, sql, args...)
}

func TestWhiteListBalanceRQLiteCreditRequiresConfirmedExactOrderAndIsExactlyOnce(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	service := whiteListBalanceRQLiteService(
		t, fixture.DB, fixture.NowUnix, "credit-guard",
	)
	beforeOrdinary := whiteListOrdinarySnapshot(t, fixture.DB, fixture.CustomerID)
	beforeForeignOrdinary := whiteListOrdinarySnapshot(t, fixture.DB, fixture.OtherCustomerID)
	beforeDirty := whiteListDirtyGeneration(t, fixture.DB)

	for _, invalidOrderID := range []string{
		fixture.UnconfirmedOrderID,
		fixture.ForeignOrderID,
	} {
		_, err := service.CreditWhiteListPurchasedBytes(
			task7Context(t),
			fixture.NowUnix,
			CreditWhiteListPurchasedBytesCommand{
				EntitlementID: fixture.EntitlementID,
				PeriodID:      fixture.PeriodID,
				SourceOrderID: invalidOrderID,
				Bytes:         999,
			},
		)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("credit with invalid source order %q error = %v, want ErrUnavailable", invalidOrderID, err)
		}
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM whitelist_balance_entries WHERE entitlement_id=?`, fixture.EntitlementID); got != 0 {
		t.Fatalf("invalid source order committed %d balance entries", got)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM idempotency_requests WHERE scope=? AND resource_id=?`,
		whiteListBalanceScope, fixture.EntitlementID); got != 0 {
		t.Fatalf("invalid source order committed %d idempotency rows", got)
	}
	if got := whiteListProjection(t, fixture.DB, fixture.EntitlementID); got != (whiteListProjectionRow{
		CurrentPeriodID: fixture.PeriodID,
		Version:         1,
	}) {
		t.Fatalf("projection changed after rejected source order: %#v", got)
	}
	if got := whiteListDirtyGeneration(t, fixture.DB); got != beforeDirty {
		t.Fatalf("dirty generation after rejected source order = %d, want %d", got, beforeDirty)
	}

	command := CreditWhiteListPurchasedBytesCommand{
		EntitlementID: fixture.EntitlementID,
		PeriodID:      fixture.PeriodID,
		SourceOrderID: fixture.PurchaseOrderID,
		Bytes:         1_000,
	}
	postCommitUnknown := &task7WriteFault{delegate: fixture.DB, failAt: 1}
	unknownService := whiteListBalanceRQLiteService(
		t, postCommitUnknown, fixture.NowUnix, "credit-unknown",
	)
	result, err := unknownService.CreditWhiteListPurchasedBytes(
		task7Context(t), fixture.NowUnix, command,
	)
	if err != nil {
		t.Fatalf("credit after committed unknown outcome: %v", err)
	}
	if result.PeriodID != fixture.PeriodID ||
		result.Projection.EntitlementID != fixture.EntitlementID ||
		result.Projection.PurchasedRemainingBytes != 1_000 ||
		result.Projection.Version != 2 {
		t.Fatalf("credit result = %#v", result)
	}

	replayService := whiteListBalanceRQLiteService(
		t, fixture.DB, fixture.NowUnix, "credit-replay",
	)
	replayed, err := replayService.CreditWhiteListPurchasedBytes(
		task7Context(t), fixture.NowUnix+1, command,
	)
	if err != nil || replayed != result {
		t.Fatalf("credit replay result=%#v err=%v, want %#v", replayed, err, result)
	}
	conflict := command
	conflict.Bytes++
	if _, err := replayService.CreditWhiteListPurchasedBytes(
		task7Context(t), fixture.NowUnix+2, conflict,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed source-order payload error = %v, want ErrConflict", err)
	}

	entry := task7Row(t, fixture.DB, rqlite.Statement{SQL: `SELECT
kind,purchased_delta_bytes,source_order_id
FROM whitelist_balance_entries WHERE entitlement_id=?`, Args: []any{fixture.EntitlementID}})
	kind, kindOK := rowString(entry, "kind")
	sourceOrderID, orderOK := rowString(entry, "source_order_id")
	purchasedDelta, deltaOK := rowInt64(entry, "purchased_delta_bytes")
	if !kindOK || !orderOK || !deltaOK || kind != "PURCHASED_CREDIT" ||
		sourceOrderID != fixture.PurchaseOrderID || purchasedDelta != 1_000 {
		t.Fatalf("purchased credit row = %#v", entry)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM whitelist_balance_entries WHERE entitlement_id=? AND kind='PURCHASED_CREDIT'`,
		fixture.EntitlementID); got != 1 {
		t.Fatalf("purchased credit entry count = %d, want 1", got)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM idempotency_requests
WHERE scope=? AND resource_id=? AND status='applied'`,
		whiteListBalanceScope, fixture.EntitlementID); got != 1 {
		t.Fatalf("applied balance idempotency count = %d, want 1", got)
	}
	if got := whiteListProjection(t, fixture.DB, fixture.EntitlementID); got != (whiteListProjectionRow{
		CurrentPeriodID:    fixture.PeriodID,
		PurchasedRemaining: 1_000,
		Version:            2,
	}) {
		t.Fatalf("durable projection = %#v", got)
	}
	if got := whiteListDirtyGeneration(t, fixture.DB); got != beforeDirty+1 {
		t.Fatalf("dirty generation after exactly-once credit = %d, want %d", got, beforeDirty+1)
	}
	assertWhiteListOrdinaryUnchanged(t, fixture.DB, fixture.CustomerID, beforeOrdinary)
	assertWhiteListOrdinaryUnchanged(t, fixture.DB, fixture.OtherCustomerID, beforeForeignOrdinary)
}

func whiteListSeedMeteredInterval(
	t *testing.T,
	fixture whiteListBalanceRQLiteFixture,
) (meterEpoch, intervalID string, intervalEndUnix int64, sourceSHA256 string) {
	t.Helper()
	originID := "origin_" + task7Name(t, "whitelist-origin")
	meterEpoch = "epoch_" + task7Name(t, "whitelist-meter-epoch")
	intervalID = "interval_" + task7Name(t, "whitelist-interval")
	intervalEndUnix = fixture.NowUnix - 10
	xrayIdentity := "wl:" + fixture.EntitlementID
	counterSourceID := originID + "-xray"
	processBootID := "boot-" + intervalID
	var err error
	sourceSHA256, err = whitelistmetering.SourceSHA256(whitelistmetering.SourceDigestInput{
		AccountID: fixture.CustomerID, EntitlementID: fixture.EntitlementID,
		TransportID: "yandex-cdn", BillingPeriodID: fixture.PeriodID,
		Basis: "UPLINK_PLUS_DOWNLINK", BaseXrayIdentity: xrayIdentity,
		RouteXrayIdentity: xrayIdentity + ":nl", OriginID: originID, ExitID: "nl",
		CounterSourceID: counterSourceID, XrayProcessBootID: processBootID,
		MeterEpoch: meterEpoch, CounterGeneration: 1, SampleSequence: 2,
		UplinkBytes: 23, DownlinkBytes: 37, SampledAtUnix: intervalEndUnix,
	})
	if err != nil {
		t.Fatalf("canonical commercial source digest: %v", err)
	}
	debit := whitelistmetering.CommercialDebit{
		EntitlementID: fixture.EntitlementID, BillingPeriodID: fixture.PeriodID,
		MeterEpoch: meterEpoch, IntervalID: intervalID, Basis: "UPLINK_PLUS_DOWNLINK",
		IntervalEndUnix: intervalEndUnix, SourceSHA256: sourceSHA256,
	}
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(meterEpoch, intervalID)
	if err != nil {
		t.Fatalf("commercial receipt key: %v", err)
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(debit)
	if err != nil {
		t.Fatalf("commercial request hash: %v", err)
	}
	task7Request(t, fixture.DB,
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_periods(
entitlement_id,billing_period_id,account_id,transport_id,xray_identity,unit,basis,
included_bytes,soft_limit_bytes,hard_limit_bytes,grace_bytes,price_mode,price_source,
currency,minor_units_per_unit,policy_sha256,created_at_unix)
VALUES(?,?,?,?,?,'GB_DECIMAL','UPLINK_PLUS_DOWNLINK','0','0','0','0',
'PAID','GLOBAL','RUB','100',?,?)`,
			Args: []any{
				fixture.EntitlementID, fixture.PeriodID, fixture.CustomerID,
				"yandex-cdn", xrayIdentity, whiteListDigest("policy:" + intervalID),
				fixture.NowUnix - 100,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_meter_epochs(
meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix)
VALUES(?,?,?,?,0,?)`,
			Args: []any{
				meterEpoch, originID, counterSourceID, processBootID,
				fixture.NowUnix - 100,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_events(
event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
diagnostic,has_interval,result_json,created_at_unix)
VALUES(?,?,?,?,?,?,'1','2','23','37',?,'',1,'{}',?)`,
			Args: []any{
				intervalID, fixture.EntitlementID, fixture.PeriodID, originID,
				meterEpoch, xrayIdentity, whiteListDigest("payload:" + intervalID),
				intervalEndUnix,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_intervals(
event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
amount_numerator,amount_denominator,currency,created_at_unix)
VALUES(?,'23','37','60','6000','1000000000','RUB',?)`,
			Args: []any{intervalID, intervalEndUnix},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_commercial_metering_sources(
event_id,entitlement_id,billing_period_id,origin_id,exit_id,meter_epoch,
route_xray_identity,counter_generation,sample_sequence,basis,sampled_at_unix,source_sha256)
VALUES(?,?,?,?,?,?,?,'1','2','UPLINK_PLUS_DOWNLINK',?,?)`,
			Args: []any{
				intervalID, fixture.EntitlementID, fixture.PeriodID, originID,
				"nl", meterEpoch, xrayIdentity + ":nl", intervalEndUnix, sourceSHA256,
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_commercial_debit_outbox(
event_id,entitlement_id,billing_period_id,meter_epoch,basis,interval_end_unix,
source_sha256,receipt_key,request_hash,created_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,?)`,
			Args: []any{
				intervalID, fixture.EntitlementID, fixture.PeriodID, meterEpoch,
				"UPLINK_PLUS_DOWNLINK", intervalEndUnix, sourceSHA256,
				receiptKey, requestHash, intervalEndUnix,
			},
		},
	)
	return meterEpoch, intervalID, intervalEndUnix, sourceSHA256
}

func TestWhiteListBalanceRQLiteUsageDebitsExactIntervalAndReplaysOnce(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	service := whiteListBalanceRQLiteService(
		t, fixture.DB, fixture.NowUnix, "usage-credit",
	)
	_, err := service.CreditWhiteListPurchasedBytes(
		task7Context(t),
		fixture.NowUnix,
		CreditWhiteListPurchasedBytesCommand{
			EntitlementID: fixture.EntitlementID,
			PeriodID:      fixture.PeriodID,
			SourceOrderID: fixture.PurchaseOrderID,
			Bytes:         100,
		},
	)
	if err != nil {
		t.Fatalf("seed purchased balance: %v", err)
	}
	meterEpoch, intervalID, intervalEndUnix, sourceSHA256 := whiteListSeedMeteredInterval(t, fixture)
	beforeOrdinary := whiteListOrdinarySnapshot(t, fixture.DB, fixture.CustomerID)
	beforeForeignOrdinary := whiteListOrdinarySnapshot(t, fixture.DB, fixture.OtherCustomerID)
	beforeDirty := whiteListDirtyGeneration(t, fixture.DB)

	command := ApplyWhiteListUsageCommand{
		EntitlementID:   fixture.EntitlementID,
		PeriodID:        fixture.PeriodID,
		MeterEpoch:      meterEpoch,
		IntervalID:      intervalID,
		Basis:           "UPLINK_PLUS_DOWNLINK",
		IntervalEndUnix: intervalEndUnix,
		SourceSHA256:    sourceSHA256,
	}
	result, err := service.ApplyWhiteListUsage(
		task7Context(t), fixture.NowUnix, command,
	)
	if err != nil {
		t.Fatalf("apply exact metered interval: %v", err)
	}
	if result.PeriodID != fixture.PeriodID ||
		result.Allocation.IncludedBytes != 0 ||
		result.Allocation.PurchasedBytes != 60 ||
		result.Allocation.UncoveredBytes != 0 ||
		result.Projection.PurchasedRemainingBytes != 40 ||
		result.Projection.LifetimeConsumedBytes != 60 ||
		result.Projection.Version != 3 ||
		result.Projection.FreshThroughUnix != intervalEndUnix {
		t.Fatalf("usage result = %#v", result)
	}

	replayService := whiteListBalanceRQLiteService(
		t, fixture.DB, fixture.NowUnix, "usage-replay",
	)
	replayed, err := replayService.ApplyWhiteListUsage(
		task7Context(t), fixture.NowUnix+1, command,
	)
	if err != nil || replayed != result {
		t.Fatalf("usage replay result=%#v err=%v, want %#v", replayed, err, result)
	}
	conflict := command
	conflict.IntervalEndUnix++
	if _, err := replayService.ApplyWhiteListUsage(
		task7Context(t), fixture.NowUnix+2, conflict,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed interval payload error = %v, want ErrConflict", err)
	}
	conflict = command
	conflict.SourceSHA256 = strings.Repeat("b", 64)
	if _, err := replayService.ApplyWhiteListUsage(
		task7Context(t), fixture.NowUnix+2, conflict,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed commercial source error = %v, want ErrConflict", err)
	}

	entry := task7Row(t, fixture.DB, rqlite.Statement{SQL: `SELECT
included_delta_bytes,purchased_delta_bytes,consumed_delta_bytes,
uncovered_delta_bytes,interval_id
FROM whitelist_balance_entries
WHERE entitlement_id=? AND kind='CONSUMED'`, Args: []any{fixture.EntitlementID}})
	includedDelta, includedOK := rowInt64(entry, "included_delta_bytes")
	purchasedDelta, purchasedOK := rowInt64(entry, "purchased_delta_bytes")
	consumedDelta, consumedOK := rowInt64(entry, "consumed_delta_bytes")
	uncoveredDelta, uncoveredOK := rowInt64(entry, "uncovered_delta_bytes")
	storedIntervalID, intervalOK := rowString(entry, "interval_id")
	if !includedOK || !purchasedOK || !consumedOK || !uncoveredOK || !intervalOK ||
		includedDelta != 0 || purchasedDelta != -60 || consumedDelta != 60 ||
		uncoveredDelta != 0 || storedIntervalID != intervalID {
		t.Fatalf("consumed entry = %#v", entry)
	}
	application := task7Row(t, fixture.DB, rqlite.Statement{SQL: `SELECT
meter_epoch,interval_id FROM whitelist_usage_applications
WHERE entitlement_id=?`, Args: []any{fixture.EntitlementID}})
	storedEpoch, epochOK := rowString(application, "meter_epoch")
	appliedIntervalID, appliedIntervalOK := rowString(application, "interval_id")
	if !epochOK || !appliedIntervalOK ||
		storedEpoch != meterEpoch || appliedIntervalID != intervalID {
		t.Fatalf("usage application = %#v", application)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM whitelist_usage_applications WHERE entitlement_id=?`, fixture.EntitlementID); got != 1 {
		t.Fatalf("usage application count = %d, want 1", got)
	}
	if got := whiteListProjection(t, fixture.DB, fixture.EntitlementID); got != (whiteListProjectionRow{
		CurrentPeriodID:    fixture.PeriodID,
		PurchasedRemaining: 40,
		LifetimeConsumed:   60,
		Version:            3,
		FreshThroughUnix:   intervalEndUnix,
	}) {
		t.Fatalf("durable usage projection = %#v", got)
	}
	if got := whiteListDirtyGeneration(t, fixture.DB); got != beforeDirty+1 {
		t.Fatalf("dirty generation after exactly-once usage = %d, want %d", got, beforeDirty+1)
	}
	assertWhiteListOrdinaryUnchanged(t, fixture.DB, fixture.CustomerID, beforeOrdinary)
	assertWhiteListOrdinaryUnchanged(t, fixture.DB, fixture.OtherCustomerID, beforeForeignOrdinary)
}

func TestWhiteListBalanceRQLiteConcurrentProjectionCASLoserRollsBackAndCanRetry(t *testing.T) {
	fixture := newWhiteListBalanceRQLiteFixture(t)
	beforeOrdinary := whiteListOrdinarySnapshot(t, fixture.DB, fixture.CustomerID)
	beforeForeignOrdinary := whiteListOrdinarySnapshot(t, fixture.DB, fixture.OtherCustomerID)
	beforeDirty := whiteListDirtyGeneration(t, fixture.DB)
	release := make(chan struct{})
	barrier := &whiteListBalanceStateReadBarrier{
		RQLite:  fixture.DB,
		reached: make(chan struct{}),
		release: release,
	}
	serviceA := whiteListBalanceRQLiteService(
		t, barrier, fixture.NowUnix, "cas-credit-a",
	)
	serviceB := whiteListBalanceRQLiteService(
		t, barrier, fixture.NowUnix, "cas-credit-b",
	)
	commandA := CreditWhiteListPurchasedBytesCommand{
		EntitlementID: fixture.EntitlementID,
		PeriodID:      fixture.PeriodID,
		SourceOrderID: fixture.PurchaseOrderID,
		Bytes:         100,
	}
	commandB := CreditWhiteListPurchasedBytesCommand{
		EntitlementID: fixture.EntitlementID,
		PeriodID:      fixture.PeriodID,
		SourceOrderID: fixture.SecondPurchaseID,
		Bytes:         100,
	}
	type outcome struct {
		command CreditWhiteListPurchasedBytesCommand
		result  whitelistbalance.OperationResult
		err     error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	outcomes := make(chan outcome, 2)
	go func() {
		result, err := serviceA.CreditWhiteListPurchasedBytes(
			ctx, fixture.NowUnix, commandA,
		)
		outcomes <- outcome{command: commandA, result: result, err: err}
	}()
	go func() {
		result, err := serviceB.CreditWhiteListPurchasedBytes(
			ctx, fixture.NowUnix, commandB,
		)
		outcomes <- outcome{command: commandB, result: result, err: err}
	}()
	select {
	case <-barrier.reached:
		close(release)
	case <-time.After(15 * time.Second):
		close(release)
		t.Fatal("concurrent balance reads did not reach the CAS barrier")
	}

	first := <-outcomes
	second := <-outcomes
	var winner, loser outcome
	switch {
	case first.err == nil && errors.Is(second.err, ErrUnavailable):
		winner, loser = first, second
	case second.err == nil && errors.Is(first.err, ErrUnavailable):
		winner, loser = second, first
	default:
		t.Fatalf("CAS outcomes = first(%#v,%v) second(%#v,%v), want one success and one ErrUnavailable",
			first.result, first.err, second.result, second.err)
	}
	if winner.result.Projection.PurchasedRemainingBytes != 100 ||
		winner.result.Projection.Version != 2 {
		t.Fatalf("winning CAS result = %#v", winner.result)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM whitelist_balance_entries
WHERE entitlement_id=? AND kind='PURCHASED_CREDIT'`, fixture.EntitlementID); got != 1 {
		t.Fatalf("purchased rows after CAS race = %d, want 1", got)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM idempotency_requests
WHERE scope=? AND resource_id=? AND status='applied'`,
		whiteListBalanceScope, fixture.EntitlementID); got != 1 {
		t.Fatalf("applied idempotency rows after CAS race = %d, want 1", got)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM idempotency_requests
WHERE scope=? AND resource_id=? AND status='applying'`,
		whiteListBalanceScope, fixture.EntitlementID); got != 0 {
		t.Fatalf("orphan applying idempotency rows after CAS race = %d", got)
	}
	if got := whiteListProjection(t, fixture.DB, fixture.EntitlementID); got != (whiteListProjectionRow{
		CurrentPeriodID:    fixture.PeriodID,
		PurchasedRemaining: 100,
		Version:            2,
	}) {
		t.Fatalf("projection after CAS race = %#v", got)
	}
	if got := whiteListDirtyGeneration(t, fixture.DB); got != beforeDirty+1 {
		t.Fatalf("dirty generation after CAS race = %d, want %d", got, beforeDirty+1)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM whitelist_balance_entries
WHERE source_order_id=? AND kind='PURCHASED_CREDIT'`, loser.command.SourceOrderID); got != 0 {
		t.Fatalf("losing source order committed %d credit rows", got)
	}

	retryService := whiteListBalanceRQLiteService(
		t, fixture.DB, fixture.NowUnix, "cas-credit-retry",
	)
	retried, err := retryService.CreditWhiteListPurchasedBytes(
		task7Context(t), fixture.NowUnix+1, loser.command,
	)
	if err != nil {
		t.Fatalf("retry losing credit after reload: %v", err)
	}
	if retried.Projection.PurchasedRemainingBytes != 200 ||
		retried.Projection.Version != 3 {
		t.Fatalf("retried losing credit result = %#v", retried)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM whitelist_balance_entries
WHERE entitlement_id=? AND kind='PURCHASED_CREDIT'
AND source_order_id IN (?,?)`,
		fixture.EntitlementID, commandA.SourceOrderID, commandB.SourceOrderID); got != 2 {
		t.Fatalf("purchased rows after loser retry = %d, want 2", got)
	}
	if got := whiteListCount(t, fixture.DB, `SELECT COUNT(*) AS n
FROM idempotency_requests
WHERE scope=? AND resource_id=? AND status='applied'`,
		whiteListBalanceScope, fixture.EntitlementID); got != 2 {
		t.Fatalf("applied idempotency rows after loser retry = %d, want 2", got)
	}
	if got := whiteListProjection(t, fixture.DB, fixture.EntitlementID); got != (whiteListProjectionRow{
		CurrentPeriodID:    fixture.PeriodID,
		PurchasedRemaining: 200,
		Version:            3,
	}) {
		t.Fatalf("projection after loser retry = %#v", got)
	}
	if got := whiteListDirtyGeneration(t, fixture.DB); got != beforeDirty+2 {
		t.Fatalf("dirty generation after loser retry = %d, want %d", got, beforeDirty+2)
	}
	assertWhiteListOrdinaryUnchanged(t, fixture.DB, fixture.CustomerID, beforeOrdinary)
	assertWhiteListOrdinaryUnchanged(t, fixture.DB, fixture.OtherCustomerID, beforeForeignOrdinary)
}
