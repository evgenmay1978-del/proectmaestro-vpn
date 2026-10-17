package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func TestScheduleWhiteListPeriodCreatesZeroGrantWithoutImplicitCredit(t *testing.T) {
	const entitlementID = "entitlement-1"
	expected := whitelistbalance.OperationResult{
		PeriodID: "period-0",
		Projection: whitelistbalance.BalanceProjection{
			EntitlementID:   entitlementID,
			CurrentPeriodID: "period-0",
			Version:         1,
		},
	}
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(),
		rowsScript(map[string]any{
			"entitlement_id": entitlementID, "customer_status": "active",
			"customer_expires_at_unix": int64(3_000),
			"commercial_debit_pending": int64(0),
			"renewal_intent_pending":   int64(0),
		}),
	}}
	db.requestFn = successfulWhiteListBalanceRequest(t, expected)
	service, _ := testService(t, db)

	result, err := service.ScheduleWhiteListPeriod(context.Background(), 1_000, ScheduleWhiteListPeriodCommand{
		EntitlementID: entitlementID,
		Period: whitelistbalance.Period{
			ID: "period-0", Ordinal: 0, StartsAtUnix: 900, EndsAtUnix: 2_000,
			IncludedGrantBytes: 0, AccessOrderID: "ordinary-order-0",
		},
	})
	if err != nil {
		t.Fatalf("ScheduleWhiteListPeriod: %v", err)
	}
	if result != expected {
		t.Fatalf("schedule result = %#v, want %#v", result, expected)
	}
	statements := assertWhiteListBalanceWrite(t, db,
		"idempotency_requests", "whitelist_billing_periods", "whitelist_balance_projections",
		"whitelist_commercial_debit_outbox", "whitelist_renewal_intents",
	)
	assertWhiteListProjectionCAS(t, statements, expected.Projection)
	assertStoredWhiteListBalanceResult(t, statements, expected)
	for _, statement := range statements {
		if strings.Contains(statement.SQL, "'INCLUDED_GRANT'") ||
			statementHasStringArg(statement, string(whitelistbalance.EntryIncludedGrant)) {
			t.Fatalf("zero-grant period created included credit: %#v", statement)
		}
	}
}

func TestCreditWhiteListPurchasedBytesUsesExactPeriodAndSourceOrder(t *testing.T) {
	const entitlementID = "entitlement-1"
	expected := whitelistbalance.OperationResult{
		PeriodID: "period-0",
		Projection: whitelistbalance.BalanceProjection{
			EntitlementID: entitlementID, CurrentPeriodID: "period-0",
			PurchasedRemainingBytes: whitelistbalance.GBDecimal, Version: 2,
		},
	}
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(),
		rowsScript(whiteListBalanceStateRow(
			entitlementID, "active", 3_000,
			whitelistbalance.Period{
				ID: "period-0", Ordinal: 0, StartsAtUnix: 900, EndsAtUnix: 2_000,
				AccessOrderID: "ordinary-order-0",
			},
			whitelistbalance.BalanceProjection{
				EntitlementID: entitlementID, CurrentPeriodID: "period-0", Version: 1,
			},
			0,
		)),
	}}
	db.requestFn = successfulWhiteListBalanceRequest(t, expected)
	service, _ := testService(t, db)

	result, err := service.CreditWhiteListPurchasedBytes(context.Background(), 1_000, CreditWhiteListPurchasedBytesCommand{
		EntitlementID: entitlementID,
		PeriodID:      "period-0",
		SourceOrderID: "gb-order-1",
		Bytes:         whitelistbalance.GBDecimal,
	})
	if err != nil {
		t.Fatalf("CreditWhiteListPurchasedBytes: %v", err)
	}
	if result != expected {
		t.Fatalf("credit result = %#v, want %#v", result, expected)
	}
	statements := assertWhiteListBalanceWrite(t, db,
		"whitelist_balance_entries", "whitelist_balance_projections",
		"payment_state='confirmed'", "customer.status='active'", "customer.expires_at_unix>?",
		"whitelist_commercial_debit_outbox", "whitelist_renewal_intents",
	)
	assertWhiteListProjectionCAS(t, statements, expected.Projection)
	assertStoredWhiteListBalanceResult(t, statements, expected)
	if !whiteListBalanceStatementsHaveArgs(statements,
		string(whitelistbalance.EntryPurchasedCredit), "period-0", "gb-order-1",
	) {
		t.Fatalf("purchase credit lacks exact source binding: %#v", statements)
	}
}

func TestApplyWhiteListUsageDebitsOldPeriodBeforeBoundaryRollover(t *testing.T) {
	const entitlementID = "entitlement-1"
	const sourceSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	period0 := whitelistbalance.Period{
		ID: "period-0", Ordinal: 0, StartsAtUnix: 1_000, EndsAtUnix: 2_000,
		IncludedGrantBytes: 100, AccessOrderID: "ordinary-order-0",
	}
	period1 := whitelistbalance.Period{
		ID: "period-1", Ordinal: 1, StartsAtUnix: 2_000, EndsAtUnix: 3_000,
		AccessOrderID: "ordinary-order-1",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: entitlementID, CurrentPeriodID: "period-0",
		IncludedRemainingBytes: 100, PurchasedRemainingBytes: 50,
		Version: 2, FreshThroughUnix: 1_900,
	}
	expected := whitelistbalance.OperationResult{
		PeriodID: "period-0",
		Allocation: whitelistbalance.UsageAllocation{
			IncludedBytes: 100, PurchasedBytes: 50, UncoveredBytes: 30,
		},
		Projection: whitelistbalance.BalanceProjection{
			EntitlementID: entitlementID, CurrentPeriodID: "period-1",
			LifetimeConsumedBytes: 180, UncoveredBytes: 30,
			Version: 3, FreshThroughUnix: 2_000,
		},
	}
	usageCommand := ApplyWhiteListUsageCommand{
		EntitlementID: entitlementID, PeriodID: "period-0",
		MeterEpoch: "meter-epoch-1", IntervalID: "interval-1",
		Basis: "UPLINK_PLUS_DOWNLINK", IntervalEndUnix: 2_000, SourceSHA256: sourceSHA256,
	}
	receiptKey, err := whitelistmetering.CommercialDebitReceiptKey(usageCommand.MeterEpoch, usageCommand.IntervalID)
	if err != nil {
		t.Fatalf("commercial receipt key: %v", err)
	}
	requestHash, err := whiteListUsageRequestHash(usageCommand)
	if err != nil {
		t.Fatalf("commercial request hash: %v", err)
	}
	period0Row := whiteListBalanceStateRow(entitlementID, "active", 3_000, period0, projection, 100)
	period1Row := whiteListBalanceStateRow(entitlementID, "active", 3_000, period1, projection, 0)
	period0Row["commercial_debit_pending"] = int64(1)
	period1Row["commercial_debit_pending"] = int64(1)
	period0Row["renewal_intent_pending"] = int64(1)
	period1Row["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(),
		rowsScript(period0Row, period1Row),
		rowsScript(map[string]any{
			"entitlement_id":    entitlementID,
			"period_id":         "period-0",
			"meter_epoch":       "meter-epoch-1",
			"interval_id":       "interval-1",
			"billable_bytes":    "180",
			"basis":             "UPLINK_PLUS_DOWNLINK",
			"interval_end_unix": int64(2_000),
			"source_sha256":     sourceSHA256,
			"receipt_key":       receiptKey,
			"request_hash":      requestHash,
		}),
	}}
	db.requestFn = successfulWhiteListBalanceRequest(t, expected)
	service, _ := testService(t, db)

	result, err := service.ApplyWhiteListUsage(context.Background(), 2_001, usageCommand)
	if err != nil {
		t.Fatalf("ApplyWhiteListUsage: %v", err)
	}
	if result != expected {
		t.Fatalf("usage result = %#v, want %#v", result, expected)
	}
	statements := assertWhiteListBalanceWrite(t, db,
		"whitelist_balance_entries", "whitelist_usage_applications", "whitelist_balance_projections",
		"customer.status='active'", "customer.expires_at_unix>?",
	)
	if strings.Contains(strings.ToLower(statementsText(statements)), "whitelist_renewal_intents") {
		t.Fatalf("commercial debit was blocked behind renewal intent: %s", statementsText(statements))
	}
	assertWhiteListProjectionCAS(t, statements, expected.Projection)
	assertStoredWhiteListBalanceResult(t, statements, expected)
	if !whiteListBalanceStatementsHaveArgs(statements,
		string(whitelistbalance.EntryConsumed), "meter-epoch-1", "interval-1", "period-0",
	) {
		t.Fatalf("usage debit lacks exact immutable source binding: %#v", statements)
	}
}

func TestWhiteListBalanceBlocksNonUsageMutationWhileCommercialDebitPending(t *testing.T) {
	const entitlementID = "entitlement-pending"
	period := whitelistbalance.Period{
		ID: "period-0", Ordinal: 0, StartsAtUnix: 900, EndsAtUnix: 2_000,
		AccessOrderID: "ordinary-order-0",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: entitlementID, CurrentPeriodID: period.ID, Version: 1,
	}
	pendingRow := whiteListBalanceStateRow(
		entitlementID, "active", 3_000, period, projection, 0,
	)
	pendingRow["commercial_debit_pending"] = int64(1)

	t.Run("schedule", func(t *testing.T) {
		db := &recordingRQLite{linear: []scriptedResult{rowsScript(), rowsScript(pendingRow)}}
		service, _ := testService(t, db)
		_, err := service.ScheduleWhiteListPeriod(context.Background(), 1_000, ScheduleWhiteListPeriodCommand{
			EntitlementID: entitlementID,
			Period: whitelistbalance.Period{
				ID: "period-1", Ordinal: 1, StartsAtUnix: 2_000, EndsAtUnix: 3_000,
				AccessOrderID: "ordinary-order-1",
			},
		})
		if !errors.Is(err, ErrUnavailable) || len(db.requestCalls) != 0 {
			t.Fatalf("schedule pending debit error=%v requests=%d", err, len(db.requestCalls))
		}
	})

	t.Run("credit", func(t *testing.T) {
		db := &recordingRQLite{linear: []scriptedResult{rowsScript(), rowsScript(pendingRow)}}
		service, _ := testService(t, db)
		_, err := service.CreditWhiteListPurchasedBytes(context.Background(), 1_000, CreditWhiteListPurchasedBytesCommand{
			EntitlementID: entitlementID, PeriodID: period.ID,
			SourceOrderID: "gb-order-pending", Bytes: 1_000,
		})
		if !errors.Is(err, ErrUnavailable) || len(db.requestCalls) != 0 {
			t.Fatalf("credit pending debit error=%v requests=%d", err, len(db.requestCalls))
		}
	})
}

func TestWhiteListBalanceBlocksNonUsageMutationWhileRenewalIntentPending(t *testing.T) {
	const entitlementID = "entitlement-renewal-pending"
	period := whitelistbalance.Period{
		ID: "period-0", Ordinal: 0, StartsAtUnix: 900, EndsAtUnix: 2_000,
		AccessOrderID: "ordinary-order-0",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: entitlementID, CurrentPeriodID: period.ID, Version: 1,
	}
	pendingRow := whiteListBalanceStateRow(
		entitlementID, "active", 3_000, period, projection, 0,
	)
	pendingRow["renewal_intent_pending"] = int64(1)

	tests := []struct {
		name string
		run  func(*Service) error
	}{
		{
			name: "schedule",
			run: func(service *Service) error {
				_, err := service.ScheduleWhiteListPeriod(context.Background(), 1_000, ScheduleWhiteListPeriodCommand{
					EntitlementID: entitlementID,
					Period: whitelistbalance.Period{
						ID: "period-1", Ordinal: 1, StartsAtUnix: 2_000, EndsAtUnix: 3_000,
						AccessOrderID: "ordinary-order-1",
					},
				})
				return err
			},
		},
		{
			name: "credit",
			run: func(service *Service) error {
				_, err := service.CreditWhiteListPurchasedBytes(context.Background(), 1_000, CreditWhiteListPurchasedBytesCommand{
					EntitlementID: entitlementID, PeriodID: period.ID,
					SourceOrderID: "gb-order-renewal-pending", Bytes: 1_000,
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingRQLite{linear: []scriptedResult{rowsScript(), rowsScript(pendingRow)}}
			service, _ := testService(t, db)
			err := test.run(service)
			if !errors.Is(err, ErrUnavailable) || len(db.requestCalls) != 0 {
				t.Fatalf("renewal-pending mutation error=%v requests=%d", err, len(db.requestCalls))
			}
		})
	}
}

func TestWhiteListUsageReceiptKeyKeepsLegacyIdempotencyIdentity(t *testing.T) {
	const meterEpoch = "meter-epoch-legacy-key"
	const intervalID = "interval-legacy-key"
	got, err := whitelistmetering.CommercialDebitReceiptKey(meterEpoch, intervalID)
	if err != nil {
		t.Fatalf("CommercialDebitReceiptKey: %v", err)
	}
	want := whiteListSourceKey(whiteListApplyUsageCommand, meterEpoch, intervalID)
	if got != want {
		t.Fatalf("commercial receipt key=%q, want legacy key %q", got, want)
	}
}

func TestWhiteListBalanceSnapshotVirtuallyAdvancesWithoutWrite(t *testing.T) {
	const entitlementID = "entitlement-1"
	period0 := whitelistbalance.Period{
		ID: "period-0", Ordinal: 0, StartsAtUnix: 1_000, EndsAtUnix: 2_000,
		IncludedGrantBytes: 100, AccessOrderID: "ordinary-order-0",
	}
	period1 := whitelistbalance.Period{
		ID: "period-1", Ordinal: 1, StartsAtUnix: 2_000, EndsAtUnix: 3_000,
		AccessOrderID: "ordinary-order-1",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: entitlementID, CurrentPeriodID: "period-0",
		IncludedRemainingBytes: 100, PurchasedRemainingBytes: 50,
		Version: 2, FreshThroughUnix: 1_900,
	}
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(
		whiteListBalanceStateRow(entitlementID, "active", 3_000, period0, projection, 100),
		whiteListBalanceStateRow(entitlementID, "active", 3_000, period1, projection, 0),
	)}}
	service, _ := testService(t, db)

	snapshot, err := service.WhiteListBalanceSnapshot(context.Background(), 2_001, entitlementID)
	if err != nil {
		t.Fatalf("WhiteListBalanceSnapshot: %v", err)
	}
	if snapshot.Projection.CurrentPeriodID != "period-1" ||
		snapshot.Projection.IncludedRemainingBytes != 0 ||
		snapshot.Projection.PurchasedRemainingBytes != 50 ||
		snapshot.Projection.Version != 2 ||
		snapshot.AvailableBytes != 50 || snapshot.UsableBytes != 50 ||
		!snapshot.PrimaryActive || snapshot.Frozen {
		t.Fatalf("virtual snapshot = %#v", snapshot)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("snapshot opened a write transaction: %#v", db.requestCalls)
	}
}

func TestCreditWhiteListPurchasedBytesReplaysStoredResultWithoutWrite(t *testing.T) {
	command := CreditWhiteListPurchasedBytesCommand{
		EntitlementID: "entitlement-1",
		PeriodID:      "period-0",
		SourceOrderID: "gb-order-1",
		Bytes:         whitelistbalance.GBDecimal,
	}
	requestHash, err := whiteListCreditRequestHash(command)
	if err != nil {
		t.Fatalf("whiteListCreditRequestHash: %v", err)
	}
	expected := whitelistbalance.OperationResult{
		PeriodID: "period-0",
		Projection: whitelistbalance.BalanceProjection{
			EntitlementID: command.EntitlementID, CurrentPeriodID: command.PeriodID,
			PurchasedRemainingBytes: command.Bytes, Version: 2,
		},
	}
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(
		whiteListAppliedRow(t, requestHash, command.EntitlementID, "stored-operation-1", expected),
	)}}
	service, _ := testService(t, db)

	result, err := service.CreditWhiteListPurchasedBytes(context.Background(), 2_500, command)
	if err != nil {
		t.Fatalf("replay credit: %v", err)
	}
	if result != expected {
		t.Fatalf("replay result = %#v, want %#v", result, expected)
	}
	if len(db.requestCalls) != 0 || len(db.linearCalls) != 1 {
		t.Fatalf("replay touched write/state paths: linear=%d request=%d", len(db.linearCalls), len(db.requestCalls))
	}

	conflicting := command
	conflicting.Bytes++
	db.linear = append(db.linear, rowsScript(
		whiteListAppliedRow(t, requestHash, command.EntitlementID, "stored-operation-1", expected),
	))
	if _, err := service.CreditWhiteListPurchasedBytes(context.Background(), 2_600, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed replay error = %v, want ErrConflict", err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("conflicting replay opened a write: %#v", db.requestCalls)
	}
}

func TestCreditWhiteListPurchasedBytesResolvesUnknownOutcomeWithoutRetry(t *testing.T) {
	command := CreditWhiteListPurchasedBytesCommand{
		EntitlementID: "entitlement-1",
		PeriodID:      "period-0",
		SourceOrderID: "gb-order-1",
		Bytes:         whitelistbalance.GBDecimal,
	}
	requestHash, err := whiteListCreditRequestHash(command)
	if err != nil {
		t.Fatalf("whiteListCreditRequestHash: %v", err)
	}
	expected := whitelistbalance.OperationResult{
		PeriodID: "period-0",
		Projection: whitelistbalance.BalanceProjection{
			EntitlementID: command.EntitlementID, CurrentPeriodID: command.PeriodID,
			PurchasedRemainingBytes: command.Bytes, Version: 2,
		},
	}
	stateRow := whiteListBalanceStateRow(
		command.EntitlementID, "active", 3_000,
		whitelistbalance.Period{
			ID: command.PeriodID, Ordinal: 0, StartsAtUnix: 900, EndsAtUnix: 2_000,
			AccessOrderID: "ordinary-order-0",
		},
		whitelistbalance.BalanceProjection{
			EntitlementID: command.EntitlementID, CurrentPeriodID: command.PeriodID, Version: 1,
		},
		0,
	)
	operationID := "whitelist-operation_00000000000000000000000000000001"
	db := &recordingRQLite{
		linear: []scriptedResult{
			rowsScript(), rowsScript(stateRow),
			rowsScript(whiteListAppliedRow(t, requestHash, command.EntitlementID, operationID, expected)),
		},
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request response", UnknownOutcome: true, Err: errors.New("lost response"),
		}}},
	}
	service, _ := testService(t, db)

	result, err := service.CreditWhiteListPurchasedBytes(context.Background(), 1_000, command)
	if err != nil {
		t.Fatalf("resolved unknown outcome: %v", err)
	}
	if result != expected {
		t.Fatalf("resolved result = %#v, want %#v", result, expected)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 3 {
		t.Fatalf("unknown outcome was retried or not resolved exactly once: linear=%d request=%d",
			len(db.linearCalls), len(db.requestCalls))
	}

	db = &recordingRQLite{
		linear: []scriptedResult{
			rowsScript(), rowsScript(stateRow),
			rowsScript(whiteListAppliedRow(t, requestHash, command.EntitlementID, operationID, expected)),
		},
		requests: []scriptedResult{resultsScript(rqlite.Result{})},
	}
	service, _ = testService(t, db)
	if result, err := service.CreditWhiteListPurchasedBytes(context.Background(), 1_000, command); err != nil || result != expected {
		t.Fatalf("ambiguous successful response was not resolved: result=%#v err=%v", result, err)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 3 {
		t.Fatalf("ambiguous success was retried or not resolved exactly once: linear=%d request=%d",
			len(db.linearCalls), len(db.requestCalls))
	}

	db = &recordingRQLite{
		linear: []scriptedResult{rowsScript(), rowsScript(stateRow), rowsScript()},
		requests: []scriptedResult{{err: &rqlite.TransportError{
			Operation: "request response", UnknownOutcome: true, Err: errors.New("lost response"),
		}}},
	}
	service, _ = testService(t, db)
	if _, err := service.CreditWhiteListPurchasedBytes(context.Background(), 1_000, command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing durable outcome error = %v, want ErrUnavailable", err)
	}
	if len(db.requestCalls) != 1 || len(db.linearCalls) != 3 {
		t.Fatalf("missing durable outcome was blindly retried: linear=%d request=%d",
			len(db.linearCalls), len(db.requestCalls))
	}
}

func successfulWhiteListBalanceRequest(
	t *testing.T,
	expected whitelistbalance.OperationResult,
) func([]rqlite.Statement) ([]rqlite.Result, error) {
	t.Helper()
	return func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		if len(statements) < 2 {
			t.Fatalf("white-list balance transaction too short: %#v", statements)
		}
		capture := statements[0]
		if !strings.Contains(strings.ToLower(capture.SQL), "insert or ignore into idempotency_requests") ||
			len(capture.Args) != 8 {
			t.Fatalf("invalid idempotency capture: %#v", capture)
		}
		requestHash, hashOK := capture.Args[3].(string)
		resourceID, resourceOK := capture.Args[4].(string)
		operationID, operationOK := capture.Args[6].(string)
		if !hashOK || !resourceOK || !operationOK || resourceID != expected.Projection.EntitlementID {
			t.Fatalf("invalid idempotency identity: %#v", capture.Args)
		}
		encoded, err := json.Marshal(struct {
			OperationID string                           `json:"operation_id"`
			Result      whitelistbalance.OperationResult `json:"result"`
		}{OperationID: operationID, Result: expected})
		if err != nil {
			t.Fatalf("encode stored response: %v", err)
		}
		results := make([]rqlite.Result, len(statements))
		results[len(results)-1].Rows = []map[string]any{{
			"request_hash":  requestHash,
			"resource_id":   resourceID,
			"operation_id":  operationID,
			"status":        "applied",
			"response_json": string(encoded),
		}}
		return results, nil
	}
}

func whiteListBalanceStateRow(
	entitlementID, customerStatus string,
	customerExpiresAtUnix int64,
	period whitelistbalance.Period,
	projection whitelistbalance.BalanceProjection,
	includedOutstandingBytes int64,
) map[string]any {
	pending := int64(0)
	if projection.Pending {
		pending = 1
	}
	return map[string]any{
		"entitlement_id":             entitlementID,
		"customer_status":            customerStatus,
		"customer_expires_at_unix":   customerExpiresAtUnix,
		"period_id":                  period.ID,
		"period_ordinal":             period.Ordinal,
		"starts_at_unix":             period.StartsAtUnix,
		"ends_at_unix":               period.EndsAtUnix,
		"included_grant_bytes":       period.IncludedGrantBytes,
		"access_order_id":            period.AccessOrderID,
		"included_outstanding_bytes": includedOutstandingBytes,
		"current_period_id":          projection.CurrentPeriodID,
		"included_remaining_bytes":   projection.IncludedRemainingBytes,
		"purchased_remaining_bytes":  projection.PurchasedRemainingBytes,
		"lifetime_consumed_bytes":    projection.LifetimeConsumedBytes,
		"uncovered_bytes":            projection.UncoveredBytes,
		"version":                    projection.Version,
		"pending":                    pending,
		"fresh_through_unix":         projection.FreshThroughUnix,
		"commercial_debit_pending":   int64(0),
		"renewal_intent_pending":     int64(0),
	}
}

func whiteListAppliedRow(
	t *testing.T,
	requestHash, resourceID, operationID string,
	result whitelistbalance.OperationResult,
) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(struct {
		OperationID string                           `json:"operation_id"`
		Result      whitelistbalance.OperationResult `json:"result"`
	}{OperationID: operationID, Result: result})
	if err != nil {
		t.Fatalf("encode applied row: %v", err)
	}
	return map[string]any{
		"request_hash": requestHash, "resource_id": resourceID,
		"operation_id": operationID, "status": "applied", "response_json": string(encoded),
	}
}

func assertWhiteListBalanceWrite(
	t *testing.T,
	db *recordingRQLite,
	required ...string,
) []rqlite.Statement {
	t.Helper()
	if len(db.requestCalls) != 1 || db.requestCalls[0].level != rqlite.Linearizable ||
		!db.requestCalls[0].transaction {
		t.Fatalf("white-list balance write is not one linearizable transaction: %#v", db.requestCalls)
	}
	statements := db.requestCalls[0].statements
	joined := strings.ToLower(statementsText(statements))
	for _, fragment := range required {
		if !strings.Contains(joined, strings.ToLower(fragment)) {
			t.Fatalf("transaction lacks %q: %s", fragment, joined)
		}
	}
	for _, forbidden := range []string{"publication", "visibility", "customer_publication"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("balance transaction changed %s: %s", forbidden, joined)
		}
	}
	projectionIndex := -1
	for index, statement := range statements {
		sql := strings.ToLower(statement.SQL)
		if strings.Contains(sql, "insert into whitelist_balance_projections") ||
			strings.Contains(sql, "update whitelist_balance_projections") {
			projectionIndex = index
			break
		}
	}
	if projectionIndex < 0 {
		t.Fatalf("transaction lacks projection CAS: %s", joined)
	}
	assertBackupDirtyImmediatelyAfter(t, statements, projectionIndex)
	projectionSQL := strings.ToLower(statements[projectionIndex].SQL)
	if !strings.Contains(projectionSQL, "returning") {
		t.Fatalf("projection mutation does not prove CAS result: %s", statements[projectionIndex].SQL)
	}
	return statements
}

func assertStoredWhiteListBalanceResult(
	t *testing.T,
	statements []rqlite.Statement,
	want whitelistbalance.OperationResult,
) {
	t.Helper()
	for _, statement := range statements {
		if !strings.Contains(strings.ToLower(statement.SQL), "update idempotency_requests set status='applied'") {
			continue
		}
		for _, arg := range statement.Args {
			raw, ok := arg.(string)
			if !ok || !strings.HasPrefix(raw, "{") {
				continue
			}
			var stored struct {
				Result whitelistbalance.OperationResult `json:"result"`
			}
			if json.Unmarshal([]byte(raw), &stored) == nil && stored.Result == want {
				return
			}
		}
	}
	t.Fatalf("transaction does not finalize the computed result: %#v", statements)
}

func assertWhiteListProjectionCAS(
	t *testing.T,
	statements []rqlite.Statement,
	want whitelistbalance.BalanceProjection,
) {
	t.Helper()
	for _, statement := range statements {
		sql := strings.ToLower(statement.SQL)
		if !strings.Contains(sql, "whitelist_balance_projections") {
			continue
		}
		if want.Version == 1 {
			if strings.Contains(sql, "insert into whitelist_balance_projections") &&
				strings.Contains(sql, "not exists") && statementHasInt64Arg(statement, 1) {
				return
			}
			continue
		}
		if strings.Contains(sql, "update whitelist_balance_projections") &&
			strings.Contains(sql, "where") && strings.Contains(sql, "version=?") &&
			statementHasInt64Arg(statement, want.Version-1) && statementHasInt64Arg(statement, want.Version) {
			return
		}
	}
	t.Fatalf("projection does not use exact version CAS for %#v: %#v", want, statements)
}

func statementHasInt64Arg(statement rqlite.Statement, want int64) bool {
	for _, arg := range statement.Args {
		switch value := arg.(type) {
		case int:
			if int64(value) == want {
				return true
			}
		case int64:
			if value == want {
				return true
			}
		}
	}
	return false
}

func whiteListBalanceStatementsHaveArgs(statements []rqlite.Statement, required ...string) bool {
	for _, want := range required {
		found := false
		for _, statement := range statements {
			if statementHasStringArg(statement, want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
