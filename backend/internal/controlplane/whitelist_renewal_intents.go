package controlplane

import (
	"context"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

const whiteListOrdinaryRenewalPeriodSource = "ordinary-renewal-period"

type whiteListRenewalIntent struct {
	AccessOrderID    string
	EntitlementID    string
	OperationID      string
	ConfirmedAtUnix  int64
	TargetEndsUnix   int64
	TargetGeneration int64
}

func appendWhiteListOrdinaryRenewalIntent(
	statements *[]rqlite.Statement,
	prepared orderRecord,
	orderID string,
	operationID string,
) {
	*statements = append(*statements, rqlite.Statement{
		SQL: `INSERT INTO whitelist_renewal_intents(
access_order_id,entitlement_id,operation_id,period_id,target_generation,
confirmed_at_unix,target_ends_at_unix,status,projection_version,created_at_unix,applied_at_unix)
SELECT source_order.order_id,entitlement.entitlement_id,?,NULL,
source_order.result_generation,source_order.confirmed_at_unix,
source_order.result_expires_at_unix,'pending',NULL,?,NULL
FROM orders AS source_order
JOIN customers AS customer ON customer.customer_id=source_order.customer_id
JOIN whitelist_entitlement_identities AS entitlement ON entitlement.customer_id=customer.customer_id
JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=entitlement.entitlement_id
JOIN idempotency_requests AS request ON request.resource_id=source_order.order_id
WHERE source_order.order_id=? AND source_order.operation_id=?
AND source_order.payment_state='confirmed' AND source_order.decision='confirmed'
AND source_order.result_generation=? AND source_order.result_expires_at_unix=?
AND source_order.confirmed_at_unix=?
AND customer.customer_id=? AND customer.generation=source_order.result_generation
AND customer.expires_at_unix=source_order.result_expires_at_unix
AND request.scope=? AND request.command_type='confirm'
AND request.operation_id=? AND request.status='applying'
AND NOT EXISTS(SELECT 1 FROM whitelist_topup_orders WHERE order_id=source_order.order_id)
ON CONFLICT DO NOTHING`,
		Args: []any{
			operationID, prepared.DBNow, orderID, operationID,
			prepared.CustomerGeneration, prepared.CustomerExpiry, prepared.DBNow,
			prepared.CustomerID, "order:" + orderID, operationID,
		},
	})
}

// ReconcileWhiteListRenewalIntents applies at most one ordered pending renewal
// per entitlement. It never changes ordinary payment state and never grants
// bytes or publishes the CDN profile.
func (s *Service) ReconcileWhiteListRenewalIntents(ctx context.Context, limit int) (int64, error) {
	if s == nil || s.store == nil || s.store.db == nil || limit <= 0 || limit > 64 {
		return 0, ErrConflict
	}
	intents, err := s.pendingWhiteListRenewalIntents(ctx, limit)
	if err != nil {
		return 0, err
	}
	var applied int64
	var firstErr error
	for _, intent := range intents {
		changed, reconcileErr := s.reconcileWhiteListRenewalIntent(ctx, intent)
		if reconcileErr != nil {
			if firstErr == nil {
				firstErr = reconcileErr
			}
			continue
		}
		if changed {
			applied++
		}
	}
	return applied, firstErr
}

func (s *Service) pendingWhiteListRenewalIntents(
	ctx context.Context,
	limit int,
) ([]whiteListRenewalIntent, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT intent.access_order_id,intent.entitlement_id,intent.operation_id,
intent.confirmed_at_unix,intent.target_ends_at_unix,intent.target_generation
FROM whitelist_renewal_intents AS intent
WHERE intent.status='pending'
AND NOT EXISTS(
    SELECT 1 FROM whitelist_renewal_intents AS earlier
    WHERE earlier.entitlement_id=intent.entitlement_id
      AND earlier.status='pending'
      AND earlier.target_generation<intent.target_generation
)
ORDER BY intent.confirmed_at_unix,intent.entitlement_id,intent.target_generation,intent.access_order_id
LIMIT ?`,
		Args: []any{limit},
	})
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}
	intents := make([]whiteListRenewalIntent, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		intent, ok := parseWhiteListRenewalIntent(row)
		if !ok {
			return nil, ErrUnavailable
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

func parseWhiteListRenewalIntent(row map[string]any) (whiteListRenewalIntent, bool) {
	accessOrderID, orderOK := rowString(row, "access_order_id")
	entitlementID, entitlementOK := rowString(row, "entitlement_id")
	operationID, operationOK := rowString(row, "operation_id")
	confirmedAtUnix, confirmedOK := rowInt64(row, "confirmed_at_unix")
	targetEndsUnix, targetOK := rowInt64(row, "target_ends_at_unix")
	targetGeneration, generationOK := rowInt64(row, "target_generation")
	intent := whiteListRenewalIntent{
		AccessOrderID: accessOrderID, EntitlementID: entitlementID, OperationID: operationID,
		ConfirmedAtUnix: confirmedAtUnix, TargetEndsUnix: targetEndsUnix,
		TargetGeneration: targetGeneration,
	}
	ok := orderOK && entitlementOK && operationOK && confirmedOK && targetOK && generationOK &&
		validWhiteListID(accessOrderID) && validWhiteListID(entitlementID) && validWhiteListID(operationID) &&
		validWhiteListTimestamp(confirmedAtUnix) && validWhiteListTimestamp(targetEndsUnix) &&
		targetEndsUnix > confirmedAtUnix && targetGeneration > 0 && targetGeneration < whitelistbalance.MaxExclusive
	return intent, ok
}

func (s *Service) reconcileWhiteListRenewalIntent(
	ctx context.Context,
	intent whiteListRenewalIntent,
) (bool, error) {
	nowUnix := s.clock.Now().Unix()
	if !validWhiteListTimestamp(nowUnix) {
		return false, ErrUnavailable
	}
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, intent.EntitlementID)
	if err != nil {
		return false, err
	}
	if loaded.CommercialPending {
		return false, nil
	}
	if loaded.State.Projection == nil {
		return false, ErrUnavailable
	}
	if existing, found, existingErr := existingWhiteListRenewalPeriod(loaded.State.Periods, intent); existingErr != nil {
		return false, existingErr
	} else if found {
		return s.persistExistingWhiteListRenewalIntent(
			ctx, intent, existing, loaded.State.Projection.Version, nowUnix,
		)
	}
	periodID := whiteListSourceKey(whiteListOrdinaryRenewalPeriodSource, intent.AccessOrderID)
	period, err := newWhiteListOrdinaryRenewalPeriod(
		loaded.State.Periods, periodID, intent.AccessOrderID,
		intent.ConfirmedAtUnix, intent.TargetEndsUnix,
	)
	if err != nil {
		return false, err
	}
	transitionNow := nowUnix
	advanceAfterSchedule := false
	if transitionNow >= intent.TargetEndsUnix {
		transitionNow = intent.ConfirmedAtUnix
		advanceAfterSchedule = true
	}
	transition, err := whitelistbalance.SchedulePeriod(
		loaded.State,
		whitelistbalance.SchedulePeriodRequest{
			OperationID: intent.OperationID, NowUnix: transitionNow, Period: period,
		},
		nil,
	)
	if err != nil {
		return false, mapWhiteListBalanceError(err)
	}
	if advanceAfterSchedule {
		advanced, advanceErr := whitelistbalance.AdvanceAt(transition.State, nowUnix)
		if advanceErr != nil {
			return false, mapWhiteListBalanceError(advanceErr)
		}
		finalProjection := advanced.Result.Projection
		finalProjection.Version = transition.Result.Projection.Version
		transition.Result.Projection = finalProjection
		transition.Journal = append(transition.Journal, advanced.Journal...)
	}
	return s.persistWhiteListRenewalIntent(
		ctx, intent, loaded.State.Projection, transition, period, nowUnix,
	)
}

func existingWhiteListRenewalPeriod(
	periods []whitelistbalance.Period,
	intent whiteListRenewalIntent,
) (whitelistbalance.Period, bool, error) {
	for _, period := range periods {
		if period.AccessOrderID != intent.AccessOrderID {
			continue
		}
		if period.IncludedGrantBytes != 0 || period.EndsAtUnix != intent.TargetEndsUnix {
			return whitelistbalance.Period{}, false, ErrConflict
		}
		return period, true, nil
	}
	return whitelistbalance.Period{}, false, nil
}

func (s *Service) persistExistingWhiteListRenewalIntent(
	ctx context.Context,
	intent whiteListRenewalIntent,
	period whitelistbalance.Period,
	projectionVersion int64,
	nowUnix int64,
) (bool, error) {
	if projectionVersion <= 0 || !validWhiteListTimestamp(nowUnix) {
		return false, ErrUnavailable
	}
	statements := []rqlite.Statement{
		{
			SQL: `UPDATE whitelist_renewal_intents AS intent
SET status='applied',period_id=?,projection_version=?,applied_at_unix=?
WHERE intent.access_order_id=? AND intent.entitlement_id=? AND intent.operation_id=?
AND intent.target_generation=? AND intent.confirmed_at_unix=? AND intent.target_ends_at_unix=?
AND intent.status='pending' AND intent.period_id IS NULL
AND NOT EXISTS(SELECT 1 FROM whitelist_renewal_intents AS earlier
    WHERE earlier.entitlement_id=intent.entitlement_id AND earlier.status='pending'
      AND earlier.target_generation<intent.target_generation)
AND NOT EXISTS(SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
    WHERE debit_outbox.entitlement_id=intent.entitlement_id AND NOT EXISTS(
        SELECT 1 FROM idempotency_requests AS debit_receipt
        WHERE debit_receipt.scope='whitelist-balance'
          AND debit_receipt.command_type='apply-usage'
          AND debit_receipt.idempotency_key=debit_outbox.receipt_key
          AND debit_receipt.request_hash=debit_outbox.request_hash
          AND debit_receipt.resource_id=debit_outbox.entitlement_id
          AND debit_receipt.status='applied'))
AND EXISTS(SELECT 1 FROM whitelist_billing_periods AS period
    JOIN whitelist_balance_projections AS projection
      ON projection.entitlement_id=period.entitlement_id
    WHERE period.period_id=? AND period.entitlement_id=intent.entitlement_id
      AND period.access_order_id=intent.access_order_id
      AND period.included_grant_bytes=0
      AND period.ends_at_unix=intent.target_ends_at_unix
      AND projection.version=?)
RETURNING status`,
			Args: []any{
				period.ID, projectionVersion, nowUnix,
				intent.AccessOrderID, intent.EntitlementID, intent.OperationID,
				intent.TargetGeneration, intent.ConfirmedAtUnix, intent.TargetEndsUnix,
				period.ID, projectionVersion,
			},
		},
		backupRPODirtyGenerationStatement(nowUnix),
		{
			SQL: `UPDATE whitelist_renewal_intents SET status='backup_rejected'
WHERE access_order_id=? AND status='applied' AND changes()<>1`,
			Args: []any{intent.AccessOrderID},
		},
	}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr != nil {
		if applied, resolveErr := s.resolveWhiteListRenewalIntent(ctx, intent); applied || resolveErr != nil {
			return applied, resolveErr
		}
		return false, ErrUnavailable
	}
	if applied, resolveErr := s.resolveWhiteListRenewalIntent(ctx, intent); applied || resolveErr != nil {
		return applied, resolveErr
	}
	return false, ErrUnavailable
}

func (s *Service) persistWhiteListRenewalIntent(
	ctx context.Context,
	intent whiteListRenewalIntent,
	previous *whitelistbalance.BalanceProjection,
	transition whitelistbalance.Transition,
	period whitelistbalance.Period,
	nowUnix int64,
) (bool, error) {
	next := transition.Result.Projection
	guard := `EXISTS(SELECT 1 FROM whitelist_renewal_intents AS intent
WHERE intent.access_order_id=? AND intent.entitlement_id=? AND intent.operation_id=?
AND intent.target_generation=? AND intent.confirmed_at_unix=? AND intent.target_ends_at_unix=?
AND intent.status='pending' AND intent.period_id IS NULL
AND NOT EXISTS(SELECT 1 FROM whitelist_renewal_intents AS earlier
    WHERE earlier.entitlement_id=intent.entitlement_id AND earlier.status='pending'
      AND earlier.target_generation<intent.target_generation))
AND NOT EXISTS(SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
WHERE debit_outbox.entitlement_id=? AND NOT EXISTS(
    SELECT 1 FROM idempotency_requests AS debit_receipt
    WHERE debit_receipt.scope='whitelist-balance'
      AND debit_receipt.command_type='apply-usage'
      AND debit_receipt.idempotency_key=debit_outbox.receipt_key
      AND debit_receipt.request_hash=debit_outbox.request_hash
      AND debit_receipt.resource_id=debit_outbox.entitlement_id
      AND debit_receipt.status='applied'))`
	guardArgs := []any{
		intent.AccessOrderID, intent.EntitlementID, intent.OperationID,
		intent.TargetGeneration, intent.ConfirmedAtUnix, intent.TargetEndsUnix,
		intent.EntitlementID,
	}
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
SELECT ?,?,?,?,?,0,?,? WHERE ` + guard + `
AND NOT EXISTS(SELECT 1 FROM whitelist_billing_periods WHERE access_order_id=?)`,
		Args: append([]any{
			period.ID, intent.EntitlementID, period.Ordinal, period.StartsAtUnix,
			period.EndsAtUnix, intent.AccessOrderID, nowUnix,
		}, append(append([]any(nil), guardArgs...), intent.AccessOrderID)...),
	}}
	writeGuard := `changes()=1 AND ` + guard + `
AND EXISTS(SELECT 1 FROM whitelist_billing_periods
WHERE period_id=? AND entitlement_id=? AND access_order_id=?
AND period_ordinal=? AND starts_at_unix=? AND ends_at_unix=? AND included_grant_bytes=0)`
	writeGuardArgs := append(append([]any(nil), guardArgs...),
		period.ID, intent.EntitlementID, intent.AccessOrderID,
		period.Ordinal, period.StartsAtUnix, period.EndsAtUnix,
	)
	entries := make([]persistedWhiteListEntry, 0, len(transition.Journal))
	for _, journalIntent := range transition.Journal {
		if journalIntent.Kind != whitelistbalance.EntryAdjustment ||
			journalIntent.IncludedDeltaBytes >= 0 ||
			journalIntent.PurchasedDeltaBytes != 0 ||
			journalIntent.ConsumedDeltaBytes != 0 ||
			journalIntent.UncoveredDeltaBytes != 0 {
			return false, ErrUnavailable
		}
		entryID, idErr := s.ids.NewID("whitelist-entry")
		if idErr != nil {
			return false, ErrUnavailable
		}
		entry := persistedWhiteListEntry{ID: entryID, Intent: journalIntent}
		entries = append(entries, entry)
		sourceOrderID, intervalID := nullableWhiteListSources(journalIntent)
		metadataSHA256, digestErr := whiteListCanonicalHash(journalIntent)
		if digestErr != nil {
			return false, ErrUnavailable
		}
		entryKey := whiteListJournalKey(intent.EntitlementID, journalIntent)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
idempotency_key,metadata_sha256,created_at_unix)
SELECT ?,?,?,?,?,?,?,?,?,?,?,?,? WHERE ` + writeGuard,
			Args: append([]any{
				entryID, intent.EntitlementID, journalIntent.PeriodID, string(journalIntent.Kind),
				journalIntent.IncludedDeltaBytes, journalIntent.PurchasedDeltaBytes,
				journalIntent.ConsumedDeltaBytes, journalIntent.UncoveredDeltaBytes,
				sourceOrderID, intervalID, entryKey, metadataSHA256, nowUnix,
			}, writeGuardArgs...),
		})
	}
	_, changed, err := appendWhiteListProjectionCAS(
		&statements, previous, next, nowUnix,
		writeGuard, writeGuardArgs,
	)
	if err != nil || !changed {
		return false, ErrUnavailable
	}
	finalizeSQL := `UPDATE whitelist_renewal_intents
SET status='applied',period_id=?,projection_version=?,applied_at_unix=?
WHERE access_order_id=? AND entitlement_id=? AND operation_id=?
AND target_generation=? AND confirmed_at_unix=? AND target_ends_at_unix=?
AND status='pending' AND period_id IS NULL
AND EXISTS(SELECT 1 FROM whitelist_billing_periods
    WHERE period_id=? AND entitlement_id=? AND access_order_id=?
      AND period_ordinal=? AND starts_at_unix=? AND ends_at_unix=?
      AND included_grant_bytes=0)
AND EXISTS(SELECT 1 FROM whitelist_balance_projections
    WHERE entitlement_id=? AND version=?)`
	finalizeArgs := []any{
		period.ID, next.Version, nowUnix,
		intent.AccessOrderID, intent.EntitlementID, intent.OperationID,
		intent.TargetGeneration, intent.ConfirmedAtUnix, intent.TargetEndsUnix,
		period.ID, intent.EntitlementID, intent.AccessOrderID,
		period.Ordinal, period.StartsAtUnix, period.EndsAtUnix,
		intent.EntitlementID, next.Version,
	}
	for _, entry := range entries {
		finalizeSQL += `
AND EXISTS(SELECT 1 FROM whitelist_balance_entries
    WHERE entry_id=? AND entitlement_id=? AND period_id=?)`
		finalizeArgs = append(finalizeArgs, entry.ID, intent.EntitlementID, entry.Intent.PeriodID)
	}
	finalizeSQL += ` RETURNING status`
	statements = append(statements,
		backupRPODirtyGenerationStatement(nowUnix),
		rqlite.Statement{
			SQL: `UPDATE whitelist_renewal_intents SET status='projection_rejected'
WHERE access_order_id=? AND status='pending' AND changes()<>1`,
			Args: []any{intent.AccessOrderID},
		},
		rqlite.Statement{SQL: finalizeSQL, Args: finalizeArgs},
		rqlite.Statement{
			SQL: `UPDATE whitelist_renewal_intents SET status='finalize_rejected'
WHERE access_order_id=? AND status='pending' AND changes()<>1`,
			Args: []any{intent.AccessOrderID},
		},
	)
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr != nil {
		if applied, resolveErr := s.resolveWhiteListRenewalIntent(ctx, intent); applied || resolveErr != nil {
			return applied, resolveErr
		}
		return false, ErrUnavailable
	}
	if applied, resolveErr := s.resolveWhiteListRenewalIntent(ctx, intent); applied || resolveErr != nil {
		return applied, resolveErr
	}
	return false, ErrUnavailable
}

func (s *Service) resolveWhiteListRenewalIntent(
	ctx context.Context,
	intent whiteListRenewalIntent,
) (bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT access_order_id,entitlement_id,operation_id,period_id,
target_generation,confirmed_at_unix,target_ends_at_unix,status,projection_version
FROM whitelist_renewal_intents WHERE access_order_id=?`,
		Args: []any{intent.AccessOrderID},
	})
	if err != nil || len(results) != 1 {
		return false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return false, nil
	}
	stored, parsed := parseWhiteListRenewalIntent(row)
	status, statusOK := rowString(row, "status")
	periodID, periodOK := optionalRowString(row, "period_id")
	projectionVersion, versionOK := optionalRowInt64(row, "projection_version")
	if !parsed || !statusOK || !periodOK || !versionOK || stored != intent {
		return false, ErrUnavailable
	}
	switch status {
	case "pending":
		if periodID != "" || projectionVersion != nil {
			return false, ErrUnavailable
		}
		return false, nil
	case "applied":
		if !validWhiteListID(periodID) || projectionVersion == nil || *projectionVersion <= 0 {
			return false, ErrUnavailable
		}
		return true, nil
	default:
		return false, ErrUnavailable
	}
}

func optionalRowInt64(row map[string]any, key string) (*int64, bool) {
	value, exists := row[key]
	if !exists {
		return nil, false
	}
	if value == nil {
		return nil, true
	}
	parsed, ok := rowInt64(row, key)
	if !ok {
		return nil, false
	}
	return &parsed, true
}
