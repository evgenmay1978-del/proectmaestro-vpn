package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

func TestAppendWhiteListOrdinaryRenewalIntentDoesNotTouchBalance(t *testing.T) {
	statements := make([]rqlite.Statement, 0, 1)
	appendWhiteListOrdinaryRenewalIntent(&statements,
		orderRecord{
			CustomerID: "customer-1", DBNow: 2_000_000,
			CustomerExpiry: 2_086_400, CustomerGeneration: 7,
		},
		"ordinary-order-1", "ordinary-confirm-operation-1",
	)
	if len(statements) != 1 {
		t.Fatalf("renewal intent statements=%d, want 1", len(statements))
	}
	joined := strings.ToLower(statementsText(statements))
	for _, required := range []string{
		"insert into whitelist_renewal_intents", "target_generation",
		"whitelist_entitlement_identities", "whitelist_balance_projections",
		"source_order.result_generation", "not exists(select 1 from whitelist_topup_orders",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("renewal intent statement missing %q: %s", required, joined)
		}
	}
	for _, forbidden := range []string{
		"insert into whitelist_billing_periods", "whitelist_balance_entries",
		"update whitelist_balance_projections", "whitelist_publication_controls",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("ordinary confirm touched CDN balance through %q: %s", forbidden, joined)
		}
	}
}

func TestReconcileWhiteListRenewalIntentsDefersCommercialPending(t *testing.T) {
	period := whitelistbalance.Period{
		ID: "period-current", Ordinal: 0, StartsAtUnix: 1_900_000,
		EndsAtUnix: 2_050_000, AccessOrderID: "ordinary-order-current",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: "wl-ent-customer-1", CurrentPeriodID: period.ID,
		PurchasedRemainingBytes: 5_000_000_000, Version: 2,
	}
	pendingBalance := whiteListBalanceStateRow(
		"wl-ent-customer-1", "active", 2_086_400, period, projection, 0,
	)
	pendingBalance["commercial_debit_pending"] = int64(1)
	pendingBalance["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(map[string]any{
			"access_order_id": "ordinary-order-1", "entitlement_id": "wl-ent-customer-1",
			"operation_id": "ordinary-confirm-operation-1", "period_id": "renewal-period-1",
			"confirmed_at_unix": int64(2_000_000), "target_ends_at_unix": int64(2_086_400),
			"target_generation": int64(7),
		}),
		rowsScript(pendingBalance),
	}}
	service, _ := testService(t, db)
	applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8)
	if err != nil || applied != 0 {
		t.Fatalf("commercial-pending reconcile=(%d,%v), want (0,nil)", applied, err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatalf("commercial-pending reconcile wrote %#v", db.requestCalls)
	}
}

func TestReconcileWhiteListRenewalIntentAdvancesAtCurrentTime(t *testing.T) {
	intent := whiteListRenewalIntent{
		AccessOrderID: "ordinary-order-renewed", EntitlementID: "wl-ent-customer-1",
		OperationID: "ordinary-confirm-operation-renewed", ConfirmedAtUnix: 1_950_000,
		TargetEndsUnix: 2_086_400, TargetGeneration: 7,
	}
	periodID := whiteListSourceKey(whiteListOrdinaryRenewalPeriodSource, intent.AccessOrderID)
	oldPeriod := whitelistbalance.Period{
		ID: "period-expired", Ordinal: 0, StartsAtUnix: 1_900_000,
		EndsAtUnix: 1_990_000, AccessOrderID: "ordinary-order-old",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: intent.EntitlementID, CurrentPeriodID: oldPeriod.ID,
		PurchasedRemainingBytes: 5_000_000_000, Version: 2,
	}
	balanceRow := whiteListBalanceStateRow(
		intent.EntitlementID, "active", intent.TargetEndsUnix, oldPeriod, projection, 0,
	)
	balanceRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(whiteListRenewalIntentRow(intent, nil, "pending", nil)),
		rowsScript(balanceRow),
		rowsScript(whiteListRenewalIntentRow(intent, &periodID, "applied", whiteListInt64Pointer(3))),
	}}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		var periodInsert *rqlite.Statement
		joined := strings.ToLower(statementsText(statements))
		for index := range statements {
			if strings.Contains(strings.ToLower(statements[index].SQL), "insert into whitelist_billing_periods") {
				periodInsert = &statements[index]
				break
			}
		}
		if periodInsert == nil || len(periodInsert.Args) < 7 {
			t.Fatalf("renewal period insert missing: %s", joined)
		}
		if periodInsert.Args[3] != int64(1_990_000) || periodInsert.Args[6] != int64(2_000_000) {
			t.Fatalf("renewal period start/created=%#v, want start=1990000 created=2000000", periodInsert.Args)
		}
		if !strings.Contains(joined, "current_period_id") ||
			!whiteListBalanceStatementsHaveArgs(statements, periodID) {
			t.Fatalf("renewal did not advance projection at current time: %s %#v", joined, statements)
		}
		return make([]rqlite.Result, len(statements)), nil
	}
	service, _ := testService(t, db)
	applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8)
	if err != nil || applied != 1 {
		t.Fatalf("current-time reconcile=(%d,%v), want (1,nil)", applied, err)
	}
}

func TestReconcileWhiteListRenewalIntentPersistsDelayedIncludedAdjustment(t *testing.T) {
	intent := whiteListRenewalIntent{
		AccessOrderID: "ordinary-order-after-included", EntitlementID: "wl-ent-customer-1",
		OperationID: "ordinary-confirm-operation-after-included", ConfirmedAtUnix: 1_950_000,
		TargetEndsUnix: 2_086_400, TargetGeneration: 7,
	}
	periodID := whiteListSourceKey(whiteListOrdinaryRenewalPeriodSource, intent.AccessOrderID)
	oldPeriod := whitelistbalance.Period{
		ID: "period-expired-with-included", Ordinal: 0, StartsAtUnix: 1_900_000,
		EndsAtUnix: 1_990_000, IncludedGrantBytes: 100, AccessOrderID: "ordinary-order-old",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: intent.EntitlementID, CurrentPeriodID: oldPeriod.ID,
		IncludedRemainingBytes: 100, PurchasedRemainingBytes: 5_000_000_000, Version: 2,
	}
	balanceRow := whiteListBalanceStateRow(
		intent.EntitlementID, "active", intent.TargetEndsUnix, oldPeriod, projection, 100,
	)
	balanceRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(whiteListRenewalIntentRow(intent, nil, "pending", nil)),
		rowsScript(balanceRow),
		rowsScript(whiteListRenewalIntentRow(intent, &periodID, "applied", whiteListInt64Pointer(3))),
	}}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		var periodInsert, adjustmentInsert *rqlite.Statement
		for index := range statements {
			statement := &statements[index]
			sql := strings.ToLower(statement.SQL)
			switch {
			case strings.Contains(sql, "insert into whitelist_billing_periods"):
				periodInsert = statement
			case strings.Contains(sql, "insert into whitelist_balance_entries"):
				adjustmentInsert = statement
			}
		}
		if periodInsert == nil || len(periodInsert.Args) < 7 ||
			periodInsert.Args[3] != int64(1_990_000) || periodInsert.Args[4] != intent.TargetEndsUnix ||
			periodInsert.Args[6] != int64(2_000_000) {
			t.Fatalf("delayed renewal period insert=%#v", periodInsert)
		}
		if adjustmentInsert == nil || len(adjustmentInsert.Args) < 13 ||
			adjustmentInsert.Args[2] != oldPeriod.ID ||
			adjustmentInsert.Args[3] != string(whitelistbalance.EntryAdjustment) ||
			adjustmentInsert.Args[4] != int64(-100) || adjustmentInsert.Args[12] != int64(2_000_000) {
			t.Fatalf("delayed renewal adjustment insert=%#v", adjustmentInsert)
		}
		if !whiteListBalanceStatementsHaveArgs(statements, periodID) {
			t.Fatalf("delayed renewal did not advance to current period %q", periodID)
		}
		return make([]rqlite.Result, len(statements)), nil
	}
	service, _ := testService(t, db)
	applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8)
	if err != nil || applied != 1 {
		t.Fatalf("delayed included-balance reconcile=(%d,%v), want (1,nil)", applied, err)
	}
}

func TestReconcileWhiteListRenewalIntentPersistsPostExpiryIncludedAdjustment(t *testing.T) {
	intent := whiteListRenewalIntent{
		AccessOrderID: "ordinary-order-after-expiry", EntitlementID: "wl-ent-customer-1",
		OperationID: "ordinary-confirm-operation-after-expiry", ConfirmedAtUnix: 1_995_000,
		TargetEndsUnix: 2_086_400, TargetGeneration: 7,
	}
	periodID := whiteListSourceKey(whiteListOrdinaryRenewalPeriodSource, intent.AccessOrderID)
	oldPeriod := whitelistbalance.Period{
		ID: "period-expired-before-confirmation", Ordinal: 0, StartsAtUnix: 1_900_000,
		EndsAtUnix: 1_990_000, IncludedGrantBytes: 100, AccessOrderID: "ordinary-order-old",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: intent.EntitlementID, CurrentPeriodID: oldPeriod.ID,
		IncludedRemainingBytes: 100, PurchasedRemainingBytes: 5_000_000_000, Version: 2,
	}
	balanceRow := whiteListBalanceStateRow(
		intent.EntitlementID, "expired", intent.TargetEndsUnix, oldPeriod, projection, 100,
	)
	balanceRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(whiteListRenewalIntentRow(intent, nil, "pending", nil)),
		rowsScript(balanceRow),
		rowsScript(whiteListRenewalIntentRow(intent, &periodID, "applied", whiteListInt64Pointer(3))),
	}}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		var periodInsert, adjustmentInsert *rqlite.Statement
		for index := range statements {
			statement := &statements[index]
			sql := strings.ToLower(statement.SQL)
			switch {
			case strings.Contains(sql, "insert into whitelist_billing_periods"):
				periodInsert = statement
			case strings.Contains(sql, "insert into whitelist_balance_entries"):
				adjustmentInsert = statement
			}
		}
		if periodInsert == nil || len(periodInsert.Args) < 7 ||
			periodInsert.Args[3] != intent.ConfirmedAtUnix || periodInsert.Args[6] != int64(2_000_000) {
			t.Fatalf("post-expiry renewal period insert=%#v", periodInsert)
		}
		if adjustmentInsert == nil || len(adjustmentInsert.Args) < 13 ||
			adjustmentInsert.Args[2] != oldPeriod.ID ||
			adjustmentInsert.Args[3] != string(whitelistbalance.EntryAdjustment) ||
			adjustmentInsert.Args[4] != int64(-100) || adjustmentInsert.Args[12] != int64(2_000_000) {
			t.Fatalf("post-expiry adjustment insert=%#v", adjustmentInsert)
		}
		return make([]rqlite.Result, len(statements)), nil
	}
	service, _ := testService(t, db)
	applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8)
	if err != nil || applied != 1 {
		t.Fatalf("post-expiry reconcile=(%d,%v), want (1,nil)", applied, err)
	}
}

func TestReconcileWhiteListRenewalIntentDoesNotStarveAfterTargetExpiry(t *testing.T) {
	intent := whiteListRenewalIntent{
		AccessOrderID: "ordinary-order-overdue", EntitlementID: "wl-ent-customer-1",
		OperationID: "ordinary-confirm-operation-overdue", ConfirmedAtUnix: 1_950_000,
		TargetEndsUnix: 1_990_000, TargetGeneration: 7,
	}
	periodID := whiteListSourceKey(whiteListOrdinaryRenewalPeriodSource, intent.AccessOrderID)
	oldPeriod := whitelistbalance.Period{
		ID: "period-before-overdue", Ordinal: 0, StartsAtUnix: 1_900_000,
		EndsAtUnix: 1_960_000, IncludedGrantBytes: 100,
		AccessOrderID: "ordinary-order-before-overdue",
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: intent.EntitlementID, CurrentPeriodID: oldPeriod.ID,
		IncludedRemainingBytes: 100, PurchasedRemainingBytes: 5_000_000_000, Version: 2,
	}
	balanceRow := whiteListBalanceStateRow(
		intent.EntitlementID, "active", intent.TargetEndsUnix, oldPeriod, projection, 100,
	)
	balanceRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(whiteListRenewalIntentRow(intent, nil, "pending", nil)),
		rowsScript(balanceRow),
		rowsScript(whiteListRenewalIntentRow(intent, &periodID, "applied", whiteListInt64Pointer(3))),
	}}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		var adjustmentInsert *rqlite.Statement
		for index := range statements {
			statement := &statements[index]
			if strings.Contains(strings.ToLower(statement.SQL), "insert into whitelist_balance_entries") {
				adjustmentInsert = statement
			}
			if !strings.Contains(strings.ToLower(statement.SQL), "insert into whitelist_billing_periods") {
				continue
			}
			if len(statement.Args) < 7 || statement.Args[3] != int64(1_960_000) ||
				statement.Args[4] != intent.TargetEndsUnix || statement.Args[6] != int64(2_000_000) {
				t.Fatalf("overdue renewal period args=%#v", statement.Args)
			}
		}
		if adjustmentInsert == nil || len(adjustmentInsert.Args) < 13 ||
			adjustmentInsert.Args[2] != oldPeriod.ID ||
			adjustmentInsert.Args[3] != string(whitelistbalance.EntryAdjustment) ||
			adjustmentInsert.Args[4] != int64(-100) || adjustmentInsert.Args[12] != int64(2_000_000) {
			t.Fatalf("overdue renewal adjustment insert=%#v", adjustmentInsert)
		}
		return make([]rqlite.Result, len(statements)), nil
	}
	service, _ := testService(t, db)
	applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8)
	if err != nil || applied != 1 {
		t.Fatalf("overdue reconcile=(%d,%v), want (1,nil)", applied, err)
	}
}

func TestReconcileWhiteListRenewalIntentAcceptsExistingExactZeroGrantPeriod(t *testing.T) {
	intent := whiteListRenewalIntent{
		AccessOrderID: "ordinary-order-existing", EntitlementID: "wl-ent-customer-1",
		OperationID: "ordinary-confirm-operation-existing", ConfirmedAtUnix: 1_950_000,
		TargetEndsUnix: 2_086_400, TargetGeneration: 7,
	}
	periodID := "topup-created-period"
	period := whitelistbalance.Period{
		ID: periodID, Ordinal: 0, StartsAtUnix: 1_960_000, EndsAtUnix: intent.TargetEndsUnix,
		IncludedGrantBytes: 0, AccessOrderID: intent.AccessOrderID,
	}
	projection := whitelistbalance.BalanceProjection{
		EntitlementID: intent.EntitlementID, CurrentPeriodID: period.ID,
		PurchasedRemainingBytes: 5_000_000_000, Version: 4,
	}
	balanceRow := whiteListBalanceStateRow(
		intent.EntitlementID, "active", intent.TargetEndsUnix, period, projection, 0,
	)
	balanceRow["renewal_intent_pending"] = int64(1)
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(whiteListRenewalIntentRow(intent, nil, "pending", nil)),
		rowsScript(balanceRow),
		rowsScript(whiteListRenewalIntentRow(intent, &periodID, "applied", whiteListInt64Pointer(4))),
	}}
	db.requestFn = func(statements []rqlite.Statement) ([]rqlite.Result, error) {
		joined := strings.ToLower(statementsText(statements))
		if strings.Contains(joined, "insert into whitelist_billing_periods") ||
			strings.Contains(joined, "update whitelist_balance_projections") {
			t.Fatalf("existing exact period was rewritten: %s", joined)
		}
		for _, required := range []string{
			"update whitelist_renewal_intents", "period.included_grant_bytes=0",
			"period.ends_at_unix=intent.target_ends_at_unix", "backup_rpo_state",
		} {
			if !strings.Contains(strings.Join(strings.Fields(joined), ""), strings.Join(strings.Fields(required), "")) {
				t.Fatalf("existing-period finalize missing %q: %s", required, joined)
			}
		}
		return make([]rqlite.Result, len(statements)), nil
	}
	service, _ := testService(t, db)
	applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8)
	if err != nil || applied != 1 {
		t.Fatalf("existing-period reconcile=(%d,%v), want (1,nil)", applied, err)
	}
}

func whiteListRenewalIntentRow(
	intent whiteListRenewalIntent,
	periodID *string,
	status string,
	projectionVersion *int64,
) map[string]any {
	row := map[string]any{
		"access_order_id": intent.AccessOrderID, "entitlement_id": intent.EntitlementID,
		"operation_id": intent.OperationID, "period_id": nil,
		"confirmed_at_unix": intent.ConfirmedAtUnix, "target_ends_at_unix": intent.TargetEndsUnix,
		"target_generation": intent.TargetGeneration, "status": status, "projection_version": nil,
	}
	if periodID != nil {
		row["period_id"] = *periodID
	}
	if projectionVersion != nil {
		row["projection_version"] = *projectionVersion
	}
	return row
}

func whiteListInt64Pointer(value int64) *int64 { return &value }

func TestReconcileWhiteListRenewalIntentsSelectsEarliestGenerationPerEntitlement(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{rowsScript()}}
	service, _ := testService(t, db)
	if applied, err := service.ReconcileWhiteListRenewalIntents(context.Background(), 8); err != nil || applied != 0 {
		t.Fatalf("empty reconcile=(%d,%v)", applied, err)
	}
	if len(db.linearCalls) != 1 {
		t.Fatalf("pending intent reads=%d, want 1", len(db.linearCalls))
	}
	sql := strings.ToLower(db.linearCalls[0].statements[0].SQL)
	for _, required := range []string{
		"status='pending'", "not exists", "earlier.entitlement_id=intent.entitlement_id",
		"earlier.target_generation<intent.target_generation", "order by intent.confirmed_at_unix",
	} {
		if !strings.Contains(strings.Join(strings.Fields(sql), ""), strings.Join(strings.Fields(required), "")) {
			t.Fatalf("pending intent ordering missing %q: %s", required, sql)
		}
	}
}

func TestNewWhiteListOrdinaryRenewalPeriodIsZeroGrant(t *testing.T) {
	period, err := newWhiteListOrdinaryRenewalPeriod(
		nil, "period-renewal-1", "ordinary-order-1", 2_000_000, 2_086_400,
	)
	if err != nil {
		t.Fatalf("new first renewal period: %v", err)
	}
	if period.Ordinal != 0 || period.StartsAtUnix != 2_000_000 || period.EndsAtUnix != 2_086_400 ||
		period.IncludedGrantBytes != 0 || period.AccessOrderID != "ordinary-order-1" {
		t.Fatalf("first renewal period=%#v", period)
	}
}

func TestNewWhiteListOrdinaryRenewalPeriodQueuesAfterLatestPeriod(t *testing.T) {
	periods := []whitelistbalance.Period{{
		ID: "period-current", Ordinal: 0, StartsAtUnix: 1_900_000, EndsAtUnix: 2_050_000,
		IncludedGrantBytes: 0, AccessOrderID: "ordinary-order-current",
	}}
	period, err := newWhiteListOrdinaryRenewalPeriod(
		periods, "period-renewal-2", "ordinary-order-2", 2_000_000, 2_150_000,
	)
	if err != nil {
		t.Fatalf("new queued renewal period: %v", err)
	}
	if period.Ordinal != 1 || period.StartsAtUnix != periods[0].EndsAtUnix ||
		period.EndsAtUnix != 2_150_000 || period.IncludedGrantBytes != 0 {
		t.Fatalf("queued renewal period=%#v", period)
	}
}
