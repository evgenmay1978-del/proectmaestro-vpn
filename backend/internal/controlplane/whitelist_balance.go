package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

const (
	whiteListBalanceScope           = "whitelist-balance"
	whiteListSchedulePeriodCommand  = "schedule-period"
	whiteListCreditPurchasedCommand = "credit-purchased"
	whiteListApplyUsageCommand      = "apply-usage"
)

type ScheduleWhiteListPeriodCommand struct {
	EntitlementID string
	Period        whitelistbalance.Period
}

type CreditWhiteListPurchasedBytesCommand struct {
	EntitlementID string
	PeriodID      string
	SourceOrderID string
	Bytes         int64
}

type ApplyWhiteListUsageCommand struct {
	EntitlementID   string
	PeriodID        string
	MeterEpoch      string
	IntervalID      string
	Basis           string
	IntervalEndUnix int64
	SourceSHA256    string
}

type storedWhiteListBalanceResponse struct {
	OperationID string                           `json:"operation_id"`
	Result      whitelistbalance.OperationResult `json:"result"`
}

type loadedWhiteListBalance struct {
	State             whitelistbalance.State
	PrimaryActive     bool
	CommercialPending bool
}

type whiteListUsageInterval struct {
	BillableBytes int64
}

type whiteListMutation struct {
	CommandType     string
	IdempotencyKey  string
	RequestHash     string
	ScheduledPeriod *whitelistbalance.Period
	Usage           *ApplyWhiteListUsageCommand
	PrimaryActive   *bool
	SourceOrderID   string
	PeriodID        string
}

type persistedWhiteListEntry struct {
	ID     string
	Intent whitelistbalance.JournalIntent
}

func (s *Service) ScheduleWhiteListPeriod(
	ctx context.Context,
	nowUnix int64,
	command ScheduleWhiteListPeriodCommand,
) (whitelistbalance.OperationResult, error) {
	requestHash, err := whiteListScheduleRequestHash(command)
	if err != nil || !validWhiteListTimestamp(nowUnix) {
		return whitelistbalance.OperationResult{}, ErrConflict
	}
	idempotencyKey := whiteListSourceKey(whiteListSchedulePeriodCommand, command.Period.ID)
	if replay, found, resolveErr := s.resolveWhiteListBalanceRequest(
		ctx, whiteListSchedulePeriodCommand, idempotencyKey, requestHash,
		command.EntitlementID, command.Period.ID,
	); found || resolveErr != nil {
		return replay, resolveErr
	}
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, command.EntitlementID)
	if err != nil {
		return whitelistbalance.OperationResult{}, err
	}
	if loaded.CommercialPending {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	operationID, err := s.ids.NewID("whitelist-operation")
	if err != nil {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	transition, err := whitelistbalance.SchedulePeriod(loaded.State, whitelistbalance.SchedulePeriodRequest{
		OperationID: operationID,
		NowUnix:     nowUnix,
		Period:      command.Period,
	}, nil)
	if err != nil {
		return whitelistbalance.OperationResult{}, mapWhiteListBalanceError(err)
	}
	period := command.Period
	return s.persistWhiteListBalance(ctx, nowUnix, loaded.State, transition, whiteListMutation{
		CommandType: whiteListSchedulePeriodCommand, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, ScheduledPeriod: &period, PeriodID: command.Period.ID,
	})
}

func (s *Service) CreditWhiteListPurchasedBytes(
	ctx context.Context,
	nowUnix int64,
	command CreditWhiteListPurchasedBytesCommand,
) (whitelistbalance.OperationResult, error) {
	requestHash, err := whiteListCreditRequestHash(command)
	if err != nil || !validWhiteListTimestamp(nowUnix) {
		return whitelistbalance.OperationResult{}, ErrConflict
	}
	idempotencyKey := whiteListSourceKey(whiteListCreditPurchasedCommand, command.SourceOrderID)
	if replay, found, resolveErr := s.resolveWhiteListBalanceRequest(
		ctx, whiteListCreditPurchasedCommand, idempotencyKey, requestHash,
		command.EntitlementID, command.PeriodID,
	); found || resolveErr != nil {
		return replay, resolveErr
	}
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, command.EntitlementID)
	if err != nil {
		return whitelistbalance.OperationResult{}, err
	}
	if loaded.CommercialPending {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	operationID, err := s.ids.NewID("whitelist-operation")
	if err != nil {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	transition, err := whitelistbalance.CreditPurchased(loaded.State, whitelistbalance.CreditPurchasedRequest{
		OperationID:   operationID,
		PeriodID:      command.PeriodID,
		SourceOrderID: command.SourceOrderID,
		NowUnix:       nowUnix,
		Bytes:         command.Bytes,
		PrimaryActive: loaded.PrimaryActive,
	}, nil)
	if err != nil {
		return whitelistbalance.OperationResult{}, mapWhiteListBalanceError(err)
	}
	return s.persistWhiteListBalance(ctx, nowUnix, loaded.State, transition, whiteListMutation{
		CommandType: whiteListCreditPurchasedCommand, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, PrimaryActive: whiteListBoolPointer(true),
		SourceOrderID: command.SourceOrderID, PeriodID: command.PeriodID,
	})
}

func (s *Service) ApplyWhiteListUsage(
	ctx context.Context,
	nowUnix int64,
	command ApplyWhiteListUsageCommand,
) (whitelistbalance.OperationResult, error) {
	requestHash, err := whiteListUsageRequestHash(command)
	if err != nil || !validWhiteListTimestamp(nowUnix) {
		return whitelistbalance.OperationResult{}, ErrConflict
	}
	idempotencyKey, err := whitelistmetering.CommercialDebitReceiptKey(command.MeterEpoch, command.IntervalID)
	if err != nil {
		return whitelistbalance.OperationResult{}, ErrConflict
	}
	if replay, found, resolveErr := s.resolveWhiteListBalanceRequest(
		ctx, whiteListApplyUsageCommand, idempotencyKey, requestHash,
		command.EntitlementID, command.PeriodID,
	); found || resolveErr != nil {
		return replay, resolveErr
	}
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, command.EntitlementID)
	if err != nil {
		return whitelistbalance.OperationResult{}, err
	}
	interval, err := s.loadWhiteListUsageInterval(ctx, command)
	if err != nil {
		return whitelistbalance.OperationResult{}, err
	}
	operationID, err := s.ids.NewID("whitelist-operation")
	if err != nil {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	transition, err := whitelistbalance.ApplyUsage(loaded.State, whitelistbalance.ApplyUsageRequest{
		OperationID:      operationID,
		PeriodID:         command.PeriodID,
		MeterEpoch:       command.MeterEpoch,
		IntervalID:       command.IntervalID,
		AppliedAtUnix:    nowUnix,
		IntervalEndUnix:  command.IntervalEndUnix,
		FreshThroughUnix: command.IntervalEndUnix,
		Bytes:            interval.BillableBytes,
		PrimaryActive:    loaded.PrimaryActive,
	}, nil)
	if err != nil {
		return whitelistbalance.OperationResult{}, mapWhiteListBalanceError(err)
	}
	usage := command
	return s.persistWhiteListBalance(ctx, nowUnix, loaded.State, transition, whiteListMutation{
		CommandType: whiteListApplyUsageCommand, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, Usage: &usage,
		PrimaryActive: whiteListBoolPointer(loaded.PrimaryActive), PeriodID: command.PeriodID,
	})
}

func (s *Service) WhiteListBalanceSnapshot(
	ctx context.Context,
	nowUnix int64,
	entitlementID string,
) (whitelistbalance.BalanceSnapshot, error) {
	if !validWhiteListID(entitlementID) || !validWhiteListTimestamp(nowUnix) {
		return whitelistbalance.BalanceSnapshot{}, ErrConflict
	}
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, entitlementID)
	if err != nil {
		return whitelistbalance.BalanceSnapshot{}, err
	}
	advanced, err := whitelistbalance.AdvanceAt(loaded.State, nowUnix)
	if err != nil {
		return whitelistbalance.BalanceSnapshot{}, mapWhiteListBalanceError(err)
	}
	snapshot, err := whitelistbalance.Snapshot(advanced.State, loaded.PrimaryActive)
	if err != nil {
		return whitelistbalance.BalanceSnapshot{}, mapWhiteListBalanceError(err)
	}
	if loaded.State.Projection != nil {
		snapshot.Projection.Version = loaded.State.Projection.Version
	}
	return snapshot, nil
}

func whiteListScheduleRequestHash(command ScheduleWhiteListPeriodCommand) (string, error) {
	if !validWhiteListID(command.EntitlementID) || !validWhiteListPeriod(command.Period) {
		return "", errors.New("controlplane: invalid white-list period command")
	}
	return whiteListCanonicalHash(struct {
		Version       int    `json:"version"`
		CommandType   string `json:"command_type"`
		EntitlementID string `json:"entitlement_id"`
		PeriodID      string `json:"period_id"`
		Ordinal       int64  `json:"ordinal"`
		StartsAtUnix  int64  `json:"starts_at_unix"`
		EndsAtUnix    int64  `json:"ends_at_unix"`
		IncludedBytes int64  `json:"included_bytes"`
		AccessOrderID string `json:"access_order_id"`
	}{
		Version: 1, CommandType: whiteListSchedulePeriodCommand,
		EntitlementID: command.EntitlementID, PeriodID: command.Period.ID,
		Ordinal: command.Period.Ordinal, StartsAtUnix: command.Period.StartsAtUnix,
		EndsAtUnix: command.Period.EndsAtUnix, IncludedBytes: command.Period.IncludedGrantBytes,
		AccessOrderID: command.Period.AccessOrderID,
	})
}

func whiteListCreditRequestHash(command CreditWhiteListPurchasedBytesCommand) (string, error) {
	if !validWhiteListID(command.EntitlementID) || !validWhiteListID(command.PeriodID) ||
		!validWhiteListID(command.SourceOrderID) || command.Bytes <= 0 ||
		command.Bytes >= whitelistbalance.MaxExclusive {
		return "", errors.New("controlplane: invalid white-list credit command")
	}
	return whiteListCanonicalHash(struct {
		Version       int    `json:"version"`
		CommandType   string `json:"command_type"`
		EntitlementID string `json:"entitlement_id"`
		PeriodID      string `json:"period_id"`
		SourceOrderID string `json:"source_order_id"`
		Bytes         int64  `json:"bytes"`
	}{1, whiteListCreditPurchasedCommand, command.EntitlementID, command.PeriodID, command.SourceOrderID, command.Bytes})
}

func whiteListUsageRequestHash(command ApplyWhiteListUsageCommand) (string, error) {
	return whitelistmetering.CommercialDebitReceiptHash(whitelistmetering.CommercialDebit{
		EntitlementID: command.EntitlementID, BillingPeriodID: command.PeriodID,
		MeterEpoch: command.MeterEpoch, IntervalID: command.IntervalID,
		Basis: command.Basis, IntervalEndUnix: command.IntervalEndUnix,
		SourceSHA256: command.SourceSHA256,
	})
}

func whiteListCanonicalHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("controlplane: encode white-list balance request")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func whiteListSourceKey(commandType string, sourceIDs ...string) string {
	payload := whiteListBalanceScope + "\x00" + commandType + "\x00" + strings.Join(sourceIDs, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func (s *Service) resolveWhiteListBalanceRequest(
	ctx context.Context,
	commandType, idempotencyKey, requestHash, entitlementID, periodID string,
) (whitelistbalance.OperationResult, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,operation_id,status,response_json
FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`, Args: []any{
		whiteListBalanceScope, commandType, idempotencyKey,
	}})
	if err != nil {
		return whitelistbalance.OperationResult{}, false, ErrUnavailable
	}
	return parseWhiteListBalanceResult(results, requestHash, entitlementID, periodID)
}

func parseWhiteListBalanceResult(
	results []rqlite.Result,
	requestHash, entitlementID, periodID string,
) (whitelistbalance.OperationResult, bool, error) {
	row, ok := firstRow(results)
	if !ok {
		return whitelistbalance.OperationResult{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	operationID, operationOK := rowString(row, "operation_id")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || !resourceOK || storedHash != requestHash || resourceID != entitlementID {
		return whitelistbalance.OperationResult{}, true, ErrConflict
	}
	if !operationOK || !statusOK || status != "applied" || !responseOK {
		return whitelistbalance.OperationResult{}, true, ErrUnavailable
	}
	var stored storedWhiteListBalanceResponse
	if err := json.Unmarshal([]byte(responseJSON), &stored); err != nil ||
		stored.OperationID != operationID || !validStoredWhiteListResult(stored.Result, entitlementID, periodID) {
		return whitelistbalance.OperationResult{}, true, ErrUnavailable
	}
	return stored.Result, true, nil
}

func validStoredWhiteListResult(result whitelistbalance.OperationResult, entitlementID, periodID string) bool {
	projection := result.Projection
	if projection.EntitlementID != entitlementID || projection.Version <= 0 ||
		projection.Version >= whitelistbalance.MaxExclusive || result.PeriodID != periodID ||
		!validWhiteListID(result.PeriodID) {
		return false
	}
	for _, value := range []int64{
		projection.IncludedRemainingBytes, projection.PurchasedRemainingBytes,
		projection.LifetimeConsumedBytes, projection.UncoveredBytes,
		projection.FreshThroughUnix, result.Allocation.IncludedBytes,
		result.Allocation.PurchasedBytes, result.Allocation.UncoveredBytes,
	} {
		if value < 0 || value >= whitelistbalance.MaxExclusive {
			return false
		}
	}
	return projection.CurrentPeriodID == "" || validWhiteListID(projection.CurrentPeriodID)
}

func (s *Service) loadWhiteListBalance(
	ctx context.Context,
	nowUnix int64,
	entitlementID string,
) (loadedWhiteListBalance, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT entitlement.entitlement_id,
       customer.status AS customer_status,
       customer.expires_at_unix AS customer_expires_at_unix,
       period.period_id,
       period.period_ordinal,
       period.starts_at_unix,
       period.ends_at_unix,
       period.included_grant_bytes,
       period.access_order_id,
       COALESCE((
           SELECT SUM(entry.included_delta_bytes)
           FROM whitelist_balance_entries AS entry
           WHERE entry.entitlement_id=period.entitlement_id AND entry.period_id=period.period_id
       ),0) AS included_outstanding_bytes,
       projection.current_period_id,
       projection.included_remaining_bytes,
       projection.purchased_remaining_bytes,
       projection.lifetime_consumed_bytes,
       projection.uncovered_bytes,
       projection.version,
       projection.pending,
       projection.fresh_through_unix,
       EXISTS(
           SELECT 1
           FROM whitelist_commercial_debit_outbox AS debit_outbox
           WHERE debit_outbox.entitlement_id=entitlement.entitlement_id
             AND NOT EXISTS(
                 SELECT 1 FROM idempotency_requests AS debit_receipt
                 WHERE debit_receipt.scope='whitelist-balance'
                   AND debit_receipt.command_type='apply-usage'
                   AND debit_receipt.idempotency_key=debit_outbox.receipt_key
                   AND debit_receipt.request_hash=debit_outbox.request_hash
                   AND debit_receipt.resource_id=debit_outbox.entitlement_id
                   AND debit_receipt.status='applied'
             )
       ) AS commercial_debit_pending
FROM whitelist_entitlement_identities AS entitlement
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
LEFT JOIN whitelist_billing_periods AS period ON period.entitlement_id=entitlement.entitlement_id
LEFT JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=entitlement.entitlement_id
WHERE entitlement.entitlement_id=?
ORDER BY period.period_ordinal`, Args: []any{entitlementID}})
	if err != nil {
		return loadedWhiteListBalance{}, ErrUnavailable
	}
	if len(results) != 1 || len(results[0].Rows) == 0 {
		return loadedWhiteListBalance{}, ErrNotFound
	}
	state, err := whitelistbalance.NewState(entitlementID)
	if err != nil {
		return loadedWhiteListBalance{}, ErrConflict
	}
	loaded := loadedWhiteListBalance{State: state}
	projectionLoaded := false
	for index, row := range results[0].Rows {
		rowEntitlementID, entitlementOK := rowString(row, "entitlement_id")
		customerStatus, statusOK := rowString(row, "customer_status")
		customerExpires, expiresOK := rowInt64(row, "customer_expires_at_unix")
		if !entitlementOK || rowEntitlementID != entitlementID || !statusOK || !expiresOK {
			return loadedWhiteListBalance{}, ErrUnavailable
		}
		primaryActive := customerStatus == "active" && customerExpires > nowUnix
		commercialPending, commercialPendingOK := rowInt64(row, "commercial_debit_pending")
		if !commercialPendingOK || (commercialPending != 0 && commercialPending != 1) {
			return loadedWhiteListBalance{}, ErrUnavailable
		}
		if index == 0 {
			loaded.PrimaryActive = primaryActive
			loaded.CommercialPending = commercialPending == 1
		} else if loaded.PrimaryActive != primaryActive ||
			loaded.CommercialPending != (commercialPending == 1) {
			return loadedWhiteListBalance{}, ErrUnavailable
		}

		if periodID, hasPeriod := optionalRowString(row, "period_id"); hasPeriod && periodID != "" {
			ordinal, ordinalOK := rowInt64(row, "period_ordinal")
			startsAt, startsOK := rowInt64(row, "starts_at_unix")
			endsAt, endsOK := rowInt64(row, "ends_at_unix")
			includedGrant, grantOK := rowInt64(row, "included_grant_bytes")
			accessOrderID, orderOK := rowString(row, "access_order_id")
			outstanding, outstandingOK := rowInt64(row, "included_outstanding_bytes")
			if !ordinalOK || !startsOK || !endsOK || !grantOK || !orderOK || !outstandingOK {
				return loadedWhiteListBalance{}, ErrUnavailable
			}
			period := whitelistbalance.Period{
				ID: periodID, Ordinal: ordinal, StartsAtUnix: startsAt, EndsAtUnix: endsAt,
				IncludedGrantBytes: includedGrant, AccessOrderID: accessOrderID,
			}
			loaded.State.Periods = append(loaded.State.Periods, period)
			loaded.State.IncludedOutstandingBytes[periodID] = outstanding
		}

		version, hasProjection := rowInt64(row, "version")
		if !hasProjection {
			continue
		}
		currentPeriodID, currentOK := optionalRowString(row, "current_period_id")
		includedRemaining, includedOK := rowInt64(row, "included_remaining_bytes")
		purchasedRemaining, purchasedOK := rowInt64(row, "purchased_remaining_bytes")
		lifetimeConsumed, lifetimeOK := rowInt64(row, "lifetime_consumed_bytes")
		uncovered, uncoveredOK := rowInt64(row, "uncovered_bytes")
		pending, pendingOK := rowInt64(row, "pending")
		freshThrough, freshOK := rowInt64(row, "fresh_through_unix")
		if !currentOK || !includedOK || !purchasedOK || !lifetimeOK || !uncoveredOK ||
			!pendingOK || !freshOK || (pending != 0 && pending != 1) {
			return loadedWhiteListBalance{}, ErrUnavailable
		}
		candidate := whitelistbalance.BalanceProjection{
			EntitlementID: entitlementID, CurrentPeriodID: currentPeriodID,
			IncludedRemainingBytes: includedRemaining, PurchasedRemainingBytes: purchasedRemaining,
			LifetimeConsumedBytes: lifetimeConsumed, UncoveredBytes: uncovered,
			Version: version, Pending: pending == 1, FreshThroughUnix: freshThrough,
		}
		if !projectionLoaded {
			loaded.State.Projection = &candidate
			projectionLoaded = true
		} else if *loaded.State.Projection != candidate {
			return loadedWhiteListBalance{}, ErrUnavailable
		}
	}
	if _, err := whitelistbalance.Snapshot(loaded.State, loaded.PrimaryActive); err != nil {
		return loadedWhiteListBalance{}, ErrUnavailable
	}
	return loaded, nil
}

func (s *Service) loadWhiteListUsageInterval(
	ctx context.Context,
	command ApplyWhiteListUsageCommand,
) (whiteListUsageInterval, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT event.entitlement_id,
       event.billing_period_id AS period_id,
       event.meter_epoch,
       interval.event_id AS interval_id,
       interval.billable_bytes,
       source.basis,
       source.sampled_at_unix AS interval_end_unix,
       source.source_sha256,
       debit_outbox.receipt_key,
       debit_outbox.request_hash
FROM whitelist_metering_intervals AS interval
JOIN whitelist_metering_events AS event ON event.event_id=interval.event_id
JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=event.meter_epoch
JOIN whitelist_commercial_metering_sources AS source ON source.event_id=interval.event_id
JOIN whitelist_commercial_debit_outbox AS debit_outbox ON debit_outbox.event_id=interval.event_id
WHERE interval.event_id=? AND event.meter_epoch=?
  AND event.entitlement_id=? AND event.billing_period_id=?
  AND epoch.origin_id=event.instance_id
  AND source.entitlement_id=event.entitlement_id
  AND source.billing_period_id=event.billing_period_id
  AND source.meter_epoch=event.meter_epoch`, Args: []any{
		command.IntervalID, command.MeterEpoch, command.EntitlementID, command.PeriodID,
	}})
	if err != nil {
		return whiteListUsageInterval{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return whiteListUsageInterval{}, ErrNotFound
	}
	rowEntitlementID, entitlementOK := rowString(row, "entitlement_id")
	periodID, periodOK := rowString(row, "period_id")
	meterEpoch, epochOK := rowString(row, "meter_epoch")
	intervalID, intervalOK := rowString(row, "interval_id")
	billableText, billableOK := rowStringAllowEmpty(row, "billable_bytes")
	basis, basisOK := rowString(row, "basis")
	intervalEndUnix, intervalEndOK := rowInt64(row, "interval_end_unix")
	sourceSHA256, sourceSHAOK := rowString(row, "source_sha256")
	receiptKey, receiptKeyOK := rowString(row, "receipt_key")
	requestHash, requestHashOK := rowString(row, "request_hash")
	if !entitlementOK || !periodOK || !epochOK || !intervalOK || !billableOK ||
		!basisOK || !intervalEndOK || !sourceSHAOK || !receiptKeyOK || !requestHashOK ||
		rowEntitlementID != command.EntitlementID || periodID != command.PeriodID ||
		meterEpoch != command.MeterEpoch || intervalID != command.IntervalID ||
		basis != command.Basis || intervalEndUnix != command.IntervalEndUnix ||
		sourceSHA256 != command.SourceSHA256 {
		return whiteListUsageInterval{}, ErrUnavailable
	}
	wantReceiptKey, err := whitelistmetering.CommercialDebitReceiptKey(command.MeterEpoch, command.IntervalID)
	if err != nil {
		return whiteListUsageInterval{}, ErrConflict
	}
	wantRequestHash, err := whiteListUsageRequestHash(command)
	if err != nil {
		return whiteListUsageInterval{}, ErrConflict
	}
	if receiptKey != wantReceiptKey || requestHash != wantRequestHash {
		return whiteListUsageInterval{}, ErrUnavailable
	}
	billable, err := strconv.ParseInt(billableText, 10, 64)
	if err != nil || billable < 0 || billable >= whitelistbalance.MaxExclusive {
		return whiteListUsageInterval{}, ErrConflict
	}
	return whiteListUsageInterval{BillableBytes: billable}, nil
}

func (s *Service) persistWhiteListBalance(
	ctx context.Context,
	nowUnix int64,
	previous whitelistbalance.State,
	transition whitelistbalance.Transition,
	mutation whiteListMutation,
) (whitelistbalance.OperationResult, error) {
	operationID := transition.Record.ID
	result := transition.Result
	if !validWhiteListID(operationID) ||
		!validStoredWhiteListResult(result, previous.EntitlementID, mutation.PeriodID) {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	storedJSON, err := json.Marshal(storedWhiteListBalanceResponse{OperationID: operationID, Result: result})
	if err != nil {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	idempotencyGuard := `EXISTS(SELECT 1 FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying')`
	idempotencyGuardArgs := []any{
		whiteListBalanceScope, mutation.CommandType, mutation.IdempotencyKey,
		mutation.RequestHash, operationID,
	}
	mutationGuard := idempotencyGuard
	mutationGuardArgs := append([]any(nil), idempotencyGuardArgs...)
	if mutation.SourceOrderID != "" {
		mutationGuard += ` AND EXISTS(
SELECT 1 FROM orders AS source_order
JOIN whitelist_entitlement_identities AS entitlement
  ON entitlement.entitlement_id=? AND entitlement.customer_id=source_order.customer_id
WHERE source_order.order_id=? AND source_order.payment_state='confirmed'
AND source_order.decision='confirmed' AND source_order.confirmed_at_unix IS NOT NULL)`
		mutationGuardArgs = append(mutationGuardArgs, previous.EntitlementID, mutation.SourceOrderID)
	}
	if mutation.PrimaryActive != nil {
		activePredicate := `EXISTS(
SELECT 1 FROM whitelist_entitlement_identities AS entitlement
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
WHERE entitlement.entitlement_id=? AND customer.status='active' AND customer.expires_at_unix>?)`
		if !*mutation.PrimaryActive {
			activePredicate = "NOT " + activePredicate
		}
		mutationGuard += " AND " + activePredicate
		mutationGuardArgs = append(mutationGuardArgs, previous.EntitlementID, nowUnix)
	}
	if mutation.Usage == nil {
		mutationGuard += ` AND NOT EXISTS(
SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
WHERE debit_outbox.entitlement_id=?
  AND NOT EXISTS(
      SELECT 1 FROM idempotency_requests AS debit_receipt
      WHERE debit_receipt.scope=? AND debit_receipt.command_type=?
        AND debit_receipt.idempotency_key=debit_outbox.receipt_key
        AND debit_receipt.request_hash=debit_outbox.request_hash
        AND debit_receipt.resource_id=debit_outbox.entitlement_id
        AND debit_receipt.status='applied'
  ))`
		mutationGuardArgs = append(
			mutationGuardArgs,
			previous.EntitlementID,
			whitelistmetering.CommercialDebitReceiptScope,
			whitelistmetering.CommercialDebitReceiptCommand,
		)
	}
	statements := []rqlite.Statement{{
		SQL: `INSERT OR IGNORE INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,created_at_unix)
VALUES(?,?,?,?,?,?,?,'applying',?)`, Args: []any{
			whiteListBalanceScope, mutation.CommandType, mutation.IdempotencyKey,
			mutation.RequestHash, previous.EntitlementID, "accepted", operationID, nowUnix,
		},
	}}

	if mutation.ScheduledPeriod != nil {
		period := *mutation.ScheduledPeriod
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
SELECT ?,?,?,?,?,?,?,? WHERE ` + mutationGuard,
			Args: append([]any{
				period.ID, previous.EntitlementID, period.Ordinal, period.StartsAtUnix,
				period.EndsAtUnix, period.IncludedGrantBytes, period.AccessOrderID, nowUnix,
			}, mutationGuardArgs...),
		})
	}

	entries := make([]persistedWhiteListEntry, 0, len(transition.Journal))
	usageEntryID := ""
	for _, intent := range transition.Journal {
		entryID, idErr := s.ids.NewID("whitelist-entry")
		if idErr != nil {
			return whitelistbalance.OperationResult{}, ErrUnavailable
		}
		entry := persistedWhiteListEntry{ID: entryID, Intent: intent}
		entries = append(entries, entry)
		sourceOrderID, intervalID := nullableWhiteListSources(intent)
		metadataSHA256, digestErr := whiteListCanonicalHash(intent)
		if digestErr != nil {
			return whitelistbalance.OperationResult{}, ErrUnavailable
		}
		entryKey := whiteListJournalKey(previous.EntitlementID, intent)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
idempotency_key,metadata_sha256,created_at_unix)
SELECT ?,?,?,?,?,?,?,?,?,?,?,?,? WHERE ` + mutationGuard,
			Args: append([]any{
				entryID, previous.EntitlementID, intent.PeriodID, string(intent.Kind),
				intent.IncludedDeltaBytes, intent.PurchasedDeltaBytes,
				intent.ConsumedDeltaBytes, intent.UncoveredDeltaBytes,
				sourceOrderID, intervalID, entryKey, metadataSHA256, nowUnix,
			}, mutationGuardArgs...),
		})
		if mutation.Usage != nil && intent.IntervalID == mutation.Usage.IntervalID {
			usageEntryID = entryID
		}
	}

	usageApplicationID := ""
	if mutation.Usage != nil && usageEntryID != "" {
		usageApplicationID, err = s.ids.NewID("whitelist-application")
		if err != nil {
			return whitelistbalance.OperationResult{}, ErrUnavailable
		}
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_usage_applications(
application_id,entitlement_id,period_id,meter_epoch,interval_id,entry_id,applied_at_unix)
SELECT ?,?,?,?,?,?,? WHERE ` + mutationGuard,
			Args: append([]any{
				usageApplicationID, previous.EntitlementID, mutation.Usage.PeriodID,
				mutation.Usage.MeterEpoch, mutation.Usage.IntervalID, usageEntryID, nowUnix,
			}, mutationGuardArgs...),
		})
	}

	projectionIndex, projectionChanged, err := appendWhiteListProjectionCAS(
		&statements, previous.Projection, result.Projection, nowUnix, mutationGuard, mutationGuardArgs,
	)
	if err != nil {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}
	if !projectionChanged {
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE idempotency_requests SET decision=decision
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying' AND ` + mutationGuard + ` RETURNING operation_id`,
			Args: append(append([]any(nil), idempotencyGuardArgs...), mutationGuardArgs...),
		})
		projectionIndex = len(statements) - 1
	}
	statements = append(statements, backupRPODirtyGenerationStatement(nowUnix))
	statements = append(statements, rqlite.Statement{
		SQL: `UPDATE idempotency_requests SET status='whitelist-balance-backup-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying' AND changes()<>1`,
		Args: append([]any(nil), idempotencyGuardArgs...),
	})
	if projectionIndex+1 != len(statements)-2 {
		return whitelistbalance.OperationResult{}, ErrUnavailable
	}

	finalizeSQL := `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying'
AND EXISTS(SELECT 1 FROM whitelist_balance_projections
WHERE entitlement_id=? AND COALESCE(current_period_id,'')=?
AND included_remaining_bytes=? AND purchased_remaining_bytes=?
AND lifetime_consumed_bytes=? AND uncovered_bytes=? AND version=?
AND pending=? AND fresh_through_unix=?)`
	projection := result.Projection
	finalizeArgs := append([]any{string(storedJSON), nowUnix}, idempotencyGuardArgs...)
	finalizeArgs = append(finalizeArgs,
		projection.EntitlementID, projection.CurrentPeriodID,
		projection.IncludedRemainingBytes, projection.PurchasedRemainingBytes,
		projection.LifetimeConsumedBytes, projection.UncoveredBytes, projection.Version,
		whiteListBoolInt(projection.Pending), projection.FreshThroughUnix,
	)
	if mutation.ScheduledPeriod != nil {
		finalizeSQL += `
AND EXISTS(SELECT 1 FROM whitelist_billing_periods
WHERE period_id=? AND entitlement_id=?)`
		finalizeArgs = append(finalizeArgs, mutation.ScheduledPeriod.ID, previous.EntitlementID)
	}
	for _, entry := range entries {
		finalizeSQL += `
AND EXISTS(SELECT 1 FROM whitelist_balance_entries
WHERE entry_id=? AND entitlement_id=? AND period_id=?)`
		finalizeArgs = append(finalizeArgs, entry.ID, previous.EntitlementID, entry.Intent.PeriodID)
	}
	if usageApplicationID != "" {
		finalizeSQL += `
AND EXISTS(SELECT 1 FROM whitelist_usage_applications
WHERE application_id=? AND entitlement_id=? AND meter_epoch=? AND interval_id=? AND entry_id=?)`
		finalizeArgs = append(finalizeArgs, usageApplicationID, previous.EntitlementID,
			mutation.Usage.MeterEpoch, mutation.Usage.IntervalID, usageEntryID)
	}
	statements = append(statements,
		rqlite.Statement{SQL: finalizeSQL, Args: finalizeArgs},
		rqlite.Statement{
			SQL: `UPDATE idempotency_requests SET status='whitelist-balance-finalize-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying' AND changes()<>1`,
			Args: append([]any(nil), idempotencyGuardArgs...),
		},
		rqlite.Statement{
			SQL: `SELECT request_hash,resource_id,operation_id,status,response_json
FROM idempotency_requests WHERE scope=? AND command_type=? AND idempotency_key=?`,
			Args: []any{whiteListBalanceScope, mutation.CommandType, mutation.IdempotencyKey},
		},
	)

	results, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr != nil || len(results) != len(statements) {
		return s.resolveWhiteListBalanceAfterWrite(ctx, mutation, previous.EntitlementID)
	}
	returnResult, found, parseErr := parseWhiteListBalanceResult(
		results[len(results)-1:], mutation.RequestHash, previous.EntitlementID, mutation.PeriodID,
	)
	if found && parseErr == nil {
		return returnResult, parseErr
	}
	return s.resolveWhiteListBalanceAfterWrite(ctx, mutation, previous.EntitlementID)
}

func (s *Service) resolveWhiteListBalanceAfterWrite(
	ctx context.Context,
	mutation whiteListMutation,
	entitlementID string,
) (whitelistbalance.OperationResult, error) {
	resolved, found, err := s.resolveWhiteListBalanceRequest(
		ctx, mutation.CommandType, mutation.IdempotencyKey, mutation.RequestHash,
		entitlementID, mutation.PeriodID,
	)
	if found || err != nil {
		return resolved, err
	}
	return whitelistbalance.OperationResult{}, ErrUnavailable
}

func appendWhiteListProjectionCAS(
	statements *[]rqlite.Statement,
	previous *whitelistbalance.BalanceProjection,
	next whitelistbalance.BalanceProjection,
	nowUnix int64,
	guard string,
	guardArgs []any,
) (int, bool, error) {
	if previous == nil {
		if next.Version != 1 {
			return -1, false, errors.New("controlplane: invalid initial white-list projection")
		}
		*statements = append(*statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix)
SELECT ?,NULLIF(?,''),?,?,?,?,?,?,?,?
WHERE ` + guard + ` AND NOT EXISTS(
SELECT 1 FROM whitelist_balance_projections WHERE entitlement_id=?)
RETURNING version`,
			Args: append([]any{
				next.EntitlementID, next.CurrentPeriodID,
				next.IncludedRemainingBytes, next.PurchasedRemainingBytes,
				next.LifetimeConsumedBytes, next.UncoveredBytes,
				next.Version, whiteListBoolInt(next.Pending), next.FreshThroughUnix, nowUnix,
			}, append(guardArgs, next.EntitlementID)...),
		})
		return len(*statements) - 1, true, nil
	}
	if next.Version == previous.Version {
		if next != *previous {
			return -1, false, errors.New("controlplane: white-list projection changed without version")
		}
		return -1, false, nil
	}
	if previous.Version >= whitelistbalance.MaxExclusive-1 || next.Version != previous.Version+1 {
		return -1, false, errors.New("controlplane: invalid white-list projection version")
	}
	*statements = append(*statements, rqlite.Statement{
		SQL: `UPDATE whitelist_balance_projections SET
current_period_id=NULLIF(?,''),included_remaining_bytes=?,purchased_remaining_bytes=?,
lifetime_consumed_bytes=?,uncovered_bytes=?,version=?,pending=?,fresh_through_unix=?,updated_at_unix=?
WHERE entitlement_id=? AND version=? AND ` + guard + `
RETURNING version`,
		Args: append([]any{
			next.CurrentPeriodID, next.IncludedRemainingBytes, next.PurchasedRemainingBytes,
			next.LifetimeConsumedBytes, next.UncoveredBytes, next.Version,
			whiteListBoolInt(next.Pending), next.FreshThroughUnix, nowUnix,
			next.EntitlementID, previous.Version,
		}, guardArgs...),
	})
	return len(*statements) - 1, true, nil
}

func whiteListJournalKey(entitlementID string, intent whitelistbalance.JournalIntent) string {
	switch intent.Kind {
	case whitelistbalance.EntryIncludedGrant:
		return whiteListSourceKey("journal-included", entitlementID, intent.PeriodID, intent.SourceOrderID)
	case whitelistbalance.EntryPurchasedCredit:
		return whiteListSourceKey("journal-purchased", intent.SourceOrderID)
	case whitelistbalance.EntryConsumed, whitelistbalance.EntryUncovered:
		return whiteListSourceKey("journal-usage", intent.MeterEpoch, intent.IntervalID)
	case whitelistbalance.EntryAdjustment:
		return whiteListSourceKey("journal-adjustment", entitlementID, intent.PeriodID)
	default:
		return whiteListSourceKey("journal-invalid", entitlementID, intent.PeriodID)
	}
}

func nullableWhiteListSources(intent whitelistbalance.JournalIntent) (any, any) {
	var sourceOrderID any
	var intervalID any
	if intent.SourceOrderID != "" {
		sourceOrderID = intent.SourceOrderID
	}
	if intent.IntervalID != "" {
		intervalID = intent.IntervalID
	}
	return sourceOrderID, intervalID
}

func optionalRowString(row map[string]any, key string) (string, bool) {
	value, exists := row[key]
	if !exists || value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func rowStringAllowEmpty(row map[string]any, key string) (string, bool) {
	value, exists := row[key]
	if !exists {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func whiteListBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func whiteListBoolPointer(value bool) *bool {
	return &value
}

func validWhiteListPeriod(period whitelistbalance.Period) bool {
	return validWhiteListID(period.ID) && validWhiteListID(period.AccessOrderID) &&
		period.Ordinal >= 0 && period.Ordinal < whitelistbalance.MaxExclusive &&
		validWhiteListTimestamp(period.StartsAtUnix) && validWhiteListTimestamp(period.EndsAtUnix) &&
		period.EndsAtUnix > period.StartsAtUnix && period.IncludedGrantBytes >= 0 &&
		period.IncludedGrantBytes < whitelistbalance.MaxExclusive
}

func validWhiteListTimestamp(value int64) bool {
	return value >= 0 && value < whitelistbalance.MaxExclusive
}

func validWhiteListID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsRune(value, 0)
}

func mapWhiteListBalanceError(err error) error {
	for _, domainErr := range []error{
		whitelistbalance.ErrInvalid,
		whitelistbalance.ErrOperationConflict,
		whitelistbalance.ErrPeriodConflict,
		whitelistbalance.ErrNoActivePeriod,
		whitelistbalance.ErrOverflow,
		whitelistbalance.ErrPrimaryInactive,
	} {
		if errors.Is(err, domainErr) {
			return ErrConflict
		}
	}
	return ErrUnavailable
}
