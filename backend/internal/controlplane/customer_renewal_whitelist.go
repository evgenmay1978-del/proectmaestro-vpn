package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

const (
	whiteListCustomerRenewalCommand = "whitelist_admin_access"
	whiteListCustomerRenewalScope   = "whitelist-customer-renewal"
	whiteListCustomerRenewalKind    = "customer-renewal-access"
)

type customerRenewalWhiteListResult struct {
	OperationID             string `json:"operation_id"`
	PeriodID                string `json:"period_id"`
	Bytes                   int64  `json:"bytes"`
	PurchasedRemainingBytes int64  `json:"purchased_remaining_bytes"`
	ProjectionVersion       int64  `json:"projection_version"`
}

// reconcileCustomerWhiteListRenewal keeps an already purchased CDN wallet
// attached to the ordinary access expiry. It never creates an order or grants
// bytes; accounts without an existing CDN period are left untouched.
func (s *Service) reconcileCustomerWhiteListRenewal(
	ctx context.Context,
	customer Customer,
	requestKey string,
) error {
	if s == nil || s.store == nil || customer.ID == "" || customer.Status != "active" ||
		customer.Generation <= 0 || customer.ExpiresAtUnix <= 0 || requestKey == "" {
		return ErrUnavailable
	}
	entitlement, err := s.WhiteListEntitlementByAccountID(ctx, customer.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	entitlementID := entitlement.EntitlementID()
	nowUnix := s.clock.Now().Unix()
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, entitlementID)
	if err != nil {
		return err
	}
	if loaded.CommercialPending || loaded.RenewalPending ||
		(loaded.State.Projection != nil && loaded.State.Projection.Pending) {
		return ErrUnavailable
	}
	if loaded.State.Projection == nil || len(loaded.State.Periods) == 0 {
		return nil
	}
	latest := loaded.State.Periods[len(loaded.State.Periods)-1]
	generation := strconv.FormatInt(customer.Generation, 10)
	sourceID := whiteListSourceKey(whiteListCustomerRenewalKind, entitlementID, customer.ID, generation, requestKey)
	periodID := whiteListSourceKey(whiteListCustomerRenewalKind+"-period", sourceID)
	operationID := whiteListSourceKey(whiteListCustomerRenewalKind+"-operation", sourceID)
	idempotencyKey := whiteListSourceKey(whiteListCustomerRenewalKind+"-request", sourceID)
	if latest.ID == periodID {
		if latest.CustomerAccessSourceID != sourceID || latest.AccessOrderID != "" ||
			latest.IncludedGrantBytes != 0 || latest.EndsAtUnix != customer.ExpiresAtUnix {
			return ErrConflict
		}
		hash, hashErr := customerRenewalWhiteListHash(entitlementID, customer, requestKey, latest)
		if hashErr != nil {
			return ErrUnavailable
		}
		_, found, resolveErr := s.resolveCustomerRenewalWhiteList(
			ctx, entitlementID, idempotencyKey, hash, operationID, periodID,
		)
		if found || resolveErr != nil {
			return resolveErr
		}
		return ErrUnavailable
	}
	if latest.EndsAtUnix >= customer.ExpiresAtUnix {
		return nil
	}
	if latest.EndsAtUnix <= 0 || latest.Ordinal >= whitelistbalance.MaxExclusive-1 {
		return ErrConflict
	}
	period := whitelistbalance.Period{
		ID:                     periodID,
		Ordinal:                latest.Ordinal + 1,
		StartsAtUnix:           latest.EndsAtUnix,
		EndsAtUnix:             customer.ExpiresAtUnix,
		IncludedGrantBytes:     0,
		CustomerAccessSourceID: sourceID,
	}
	if !validWhiteListCustomerRenewalPeriod(period) {
		return ErrConflict
	}
	hash, err := customerRenewalWhiteListHash(entitlementID, customer, requestKey, period)
	if err != nil {
		return ErrUnavailable
	}
	if _, found, resolveErr := s.resolveCustomerRenewalWhiteList(
		ctx, entitlementID, idempotencyKey, hash, operationID, periodID,
	); found || resolveErr != nil {
		return resolveErr
	}
	transition, err := whitelistbalance.SchedulePeriod(loaded.State, whitelistbalance.SchedulePeriodRequest{
		OperationID: operationID,
		NowUnix:     nowUnix,
		Period:      period,
	}, nil)
	if err != nil {
		return mapWhiteListBalanceError(err)
	}
	return s.persistCustomerRenewalWhiteList(
		ctx, nowUnix, customer, entitlementID, idempotencyKey,
		hash, operationID, sourceID, period, loaded.State.Projection, transition,
	)
}

func validWhiteListCustomerRenewalPeriod(period whitelistbalance.Period) bool {
	return validWhiteListID(period.ID) && period.AccessOrderID == "" &&
		validWhiteListID(period.CustomerAccessSourceID) && period.IncludedGrantBytes == 0 &&
		period.Ordinal >= 0 && period.Ordinal < whitelistbalance.MaxExclusive &&
		validWhiteListTimestamp(period.StartsAtUnix) && validWhiteListTimestamp(period.EndsAtUnix) &&
		period.EndsAtUnix > period.StartsAtUnix
}

func customerRenewalWhiteListHash(
	entitlementID string,
	customer Customer,
	requestKey string,
	period whitelistbalance.Period,
) (string, error) {
	return whiteListCanonicalHash(struct {
		Version       int    `json:"version"`
		CommandType   string `json:"command_type"`
		EntitlementID string `json:"entitlement_id"`
		CustomerID    string `json:"customer_id"`
		RequestKey    string `json:"request_key"`
		Generation    int64  `json:"generation"`
		PeriodID      string `json:"period_id"`
		SourceID      string `json:"source_id"`
		Ordinal       int64  `json:"ordinal"`
		StartsAt      int64  `json:"starts_at_unix"`
		EndsAt        int64  `json:"ends_at_unix"`
	}{1, whiteListCustomerRenewalCommand, entitlementID, customer.ID, requestKey,
		customer.Generation, period.ID, period.CustomerAccessSourceID, period.Ordinal,
		period.StartsAtUnix, period.EndsAtUnix})
}

func (s *Service) resolveCustomerRenewalWhiteList(
	ctx context.Context,
	entitlementID, idempotencyKey, requestHash, operationID, periodID string,
) (customerRenewalWhiteListResult, bool, error) {
	var result customerRenewalWhiteListResult
	rows, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,operation_id,status,response_json
FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`, Args: []any{
		whiteListCustomerRenewalScope + ":" + entitlementID,
		whiteListCustomerRenewalCommand,
		idempotencyKey,
	}})
	if err != nil || len(rows) != 1 {
		return result, false, ErrUnavailable
	}
	row, ok := firstRow(rows)
	if !ok {
		return result, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	storedOperation, operationOK := rowString(row, "operation_id")
	status, statusOK := rowString(row, "status")
	if !hashOK || !resourceOK || !operationOK || storedHash != requestHash ||
		resourceID != entitlementID || storedOperation != operationID {
		return result, true, ErrConflict
	}
	if !statusOK || status != "applied" {
		return result, true, ErrUnavailable
	}
	body, bodyOK := rowString(row, "response_json")
	if !bodyOK || json.Unmarshal([]byte(body), &result) != nil ||
		result.OperationID != operationID || result.PeriodID != periodID || result.Bytes != 0 ||
		result.PurchasedRemainingBytes < 0 || result.ProjectionVersion <= 0 {
		return result, true, ErrUnavailable
	}
	return result, true, nil
}

func (s *Service) persistCustomerRenewalWhiteList(
	ctx context.Context,
	nowUnix int64,
	customer Customer,
	entitlementID, idempotencyKey, requestHash, operationID, sourceID string,
	period whitelistbalance.Period,
	previous *whitelistbalance.BalanceProjection,
	transition whitelistbalance.Transition,
) error {
	if previous == nil || transition.Record.ID != operationID || !validWhiteListTimestamp(nowUnix) {
		return ErrUnavailable
	}
	next := transition.Result.Projection
	if next.EntitlementID != entitlementID || next.Version <= previous.Version ||
		next.PurchasedRemainingBytes != previous.PurchasedRemainingBytes {
		return ErrConflict
	}
	scope := whiteListCustomerRenewalScope + ":" + entitlementID
	response, err := json.Marshal(customerRenewalWhiteListResult{
		OperationID: operationID, PeriodID: period.ID, Bytes: 0,
		PurchasedRemainingBytes: next.PurchasedRemainingBytes, ProjectionVersion: next.Version,
	})
	if err != nil {
		return ErrUnavailable
	}
	idempotencyGuard := `EXISTS(SELECT 1 FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND resource_id=? AND operation_id=? AND status='applying')`
	idempotencyArgs := []any{scope, whiteListCustomerRenewalCommand, idempotencyKey, requestHash, entitlementID, operationID}
	baseGuard := idempotencyGuard + `
AND EXISTS(SELECT 1 FROM whitelist_entitlement_identities AS identity
JOIN customers AS current_customer ON current_customer.customer_id=identity.customer_id
WHERE identity.entitlement_id=? AND identity.customer_id=? AND current_customer.status='active'
AND current_customer.generation=? AND current_customer.expires_at_unix=?)
AND EXISTS(SELECT 1 FROM whitelist_billing_periods AS previous_period
WHERE previous_period.entitlement_id=? AND previous_period.period_id=?
AND previous_period.period_ordinal=? AND previous_period.ends_at_unix=?)
AND EXISTS(SELECT 1 FROM whitelist_balance_projections AS previous_projection
WHERE previous_projection.entitlement_id=? AND previous_projection.version=?
AND COALESCE(previous_projection.current_period_id,'')=?
AND previous_projection.included_remaining_bytes=?
AND previous_projection.purchased_remaining_bytes=?
AND previous_projection.lifetime_consumed_bytes=?
AND previous_projection.uncovered_bytes=?
AND previous_projection.pending=?
AND previous_projection.fresh_through_unix=?)
AND NOT EXISTS(SELECT 1 FROM whitelist_renewal_intents AS renewal_intent
WHERE renewal_intent.entitlement_id=? AND renewal_intent.status='pending')
AND NOT EXISTS(SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
WHERE debit_outbox.entitlement_id=? AND NOT EXISTS(
SELECT 1 FROM idempotency_requests AS debit_receipt
WHERE debit_receipt.scope='whitelist-balance' AND debit_receipt.command_type='apply-usage'
AND debit_receipt.idempotency_key=debit_outbox.receipt_key
AND debit_receipt.request_hash=debit_outbox.request_hash
AND debit_receipt.resource_id=debit_outbox.entitlement_id
AND debit_receipt.status='applied'))`
	baseArgs := append(append([]any(nil), idempotencyArgs...),
		entitlementID, customer.ID, customer.Generation, customer.ExpiresAtUnix,
		entitlementID, transition.State.Periods[len(transition.State.Periods)-2].ID,
		transition.State.Periods[len(transition.State.Periods)-2].Ordinal,
		period.StartsAtUnix,
		entitlementID, previous.Version, previous.CurrentPeriodID,
		previous.IncludedRemainingBytes, previous.PurchasedRemainingBytes,
		previous.LifetimeConsumedBytes, previous.UncoveredBytes, whiteListBoolInt(previous.Pending),
		previous.FreshThroughUnix, entitlementID, entitlementID)
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix)
VALUES(?,?,?,?,?,'admin_balance',?,'applying',NULL,?,NULL)`,
		Args: []any{scope, whiteListCustomerRenewalCommand, idempotencyKey, requestHash, entitlementID, operationID, nowUnix},
	}, {
		SQL: `INSERT INTO whitelist_customer_access_sources(
source_id,entitlement_id,customer_id,customer_generation,customer_expires_at_unix,
captured_at_unix,operation_id,request_hash,actor_hmac)
SELECT ?,?,?,?,?,?,?,?,? WHERE ` + baseGuard,
		Args: append([]any{sourceID, entitlementID, customer.ID, customer.Generation,
			customer.ExpiresAtUnix, period.StartsAtUnix, operationID, requestHash,
			s.auditActor("customer-renewal")}, baseArgs...),
	}}
	statements = append(statements, rqlite.Statement{
		SQL: `UPDATE idempotency_requests SET status='customer-renewal-source-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND status='applying' AND changes()<>1`,
		Args: idempotencyArgs[:3],
	})
	periodGuard := baseGuard + `
AND EXISTS(SELECT 1 FROM whitelist_customer_access_sources AS source
WHERE source.source_id=? AND source.entitlement_id=? AND source.operation_id=?
AND source.customer_generation=? AND source.customer_expires_at_unix=?
AND source.captured_at_unix=? AND source.request_hash=?)
AND NOT EXISTS(SELECT 1 FROM whitelist_billing_periods AS new_period
WHERE new_period.period_id=? OR (new_period.entitlement_id=? AND new_period.period_ordinal=?))`
	periodArgs := append(append([]any(nil), baseArgs...), sourceID, entitlementID, operationID,
		customer.Generation, customer.ExpiresAtUnix, period.StartsAtUnix, requestHash,
		period.ID, entitlementID, period.Ordinal)
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix,customer_access_source_id)
SELECT ?,?,?,?,?,0,NULL,?,? WHERE ` + periodGuard,
		Args: append([]any{period.ID, entitlementID, period.Ordinal, period.StartsAtUnix,
			period.EndsAtUnix, nowUnix, sourceID}, periodArgs...),
	})
	statements = append(statements, rqlite.Statement{
		SQL: `UPDATE idempotency_requests SET status='customer-renewal-period-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND status='applying' AND changes()<>1`,
		Args: idempotencyArgs[:3],
	})
	writeGuard := baseGuard + `
AND EXISTS(SELECT 1 FROM whitelist_customer_access_sources AS source
WHERE source.source_id=? AND source.entitlement_id=? AND source.operation_id=?
AND source.customer_generation=? AND source.customer_expires_at_unix=?
AND source.captured_at_unix=? AND source.request_hash=?)
AND EXISTS(SELECT 1 FROM whitelist_billing_periods AS new_period
WHERE new_period.period_id=? AND new_period.entitlement_id=?
AND new_period.period_ordinal=? AND new_period.starts_at_unix=?
AND new_period.ends_at_unix=? AND new_period.included_grant_bytes=0
AND new_period.customer_access_source_id=?)`
	writeGuardArgs := append(append([]any(nil), baseArgs...), sourceID, entitlementID, operationID,
		customer.Generation, customer.ExpiresAtUnix, period.StartsAtUnix,
		requestHash, period.ID, entitlementID, period.Ordinal, period.StartsAtUnix,
		period.EndsAtUnix, sourceID)
	entries := make([]persistedWhiteListEntry, 0, len(transition.Journal))
	for _, intent := range transition.Journal {
		if intent.Kind != whitelistbalance.EntryAdjustment || intent.PurchasedDeltaBytes != 0 ||
			intent.ConsumedDeltaBytes != 0 || intent.UncoveredDeltaBytes != 0 || intent.IncludedDeltaBytes >= 0 {
			return ErrUnavailable
		}
		entryID, idErr := s.ids.NewID("whitelist-entry")
		if idErr != nil {
			return ErrUnavailable
		}
		entries = append(entries, persistedWhiteListEntry{ID: entryID, Intent: intent})
		metadataSHA256, digestErr := whiteListCanonicalHash(intent)
		if digestErr != nil {
			return ErrUnavailable
		}
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_balance_entries(
entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
idempotency_key,metadata_sha256,created_at_unix)
SELECT ?,?,?,?,?,?,?,?,?,?,?,?,? WHERE ` + writeGuard,
			Args: append([]any{entryID, entitlementID, intent.PeriodID, string(intent.Kind),
				intent.IncludedDeltaBytes, intent.PurchasedDeltaBytes, intent.ConsumedDeltaBytes,
				intent.UncoveredDeltaBytes, nil, nil, whiteListJournalKey(entitlementID, intent),
				metadataSHA256, nowUnix}, writeGuardArgs...),
		})
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE idempotency_requests SET status='customer-renewal-entry-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND status='applying' AND changes()<>1`,
			Args: idempotencyArgs[:3],
		})
	}
	projectionGuard := idempotencyGuard + `
AND EXISTS(SELECT 1 FROM whitelist_entitlement_identities AS identity
JOIN customers AS current_customer ON current_customer.customer_id=identity.customer_id
WHERE identity.entitlement_id=? AND identity.customer_id=? AND current_customer.status='active'
AND current_customer.generation=? AND current_customer.expires_at_unix=?)
AND EXISTS(SELECT 1 FROM whitelist_customer_access_sources AS source
WHERE source.source_id=? AND source.entitlement_id=? AND source.operation_id=?
AND source.customer_generation=? AND source.customer_expires_at_unix=?
AND source.captured_at_unix=? AND source.request_hash=?)
AND EXISTS(SELECT 1 FROM whitelist_billing_periods AS new_period
WHERE new_period.period_id=? AND new_period.entitlement_id=?
AND new_period.period_ordinal=? AND new_period.starts_at_unix=?
AND new_period.ends_at_unix=? AND new_period.included_grant_bytes=0
AND new_period.customer_access_source_id=?)
AND NOT EXISTS(SELECT 1 FROM whitelist_renewal_intents AS renewal_intent
WHERE renewal_intent.entitlement_id=? AND renewal_intent.status='pending')
AND NOT EXISTS(SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
WHERE debit_outbox.entitlement_id=? AND NOT EXISTS(
SELECT 1 FROM idempotency_requests AS debit_receipt
WHERE debit_receipt.scope='whitelist-balance' AND debit_receipt.command_type='apply-usage'
AND debit_receipt.idempotency_key=debit_outbox.receipt_key
AND debit_receipt.request_hash=debit_outbox.request_hash
AND debit_receipt.resource_id=debit_outbox.entitlement_id
AND debit_receipt.status='applied'))`
	projectionArgs := append(append([]any(nil), idempotencyArgs...),
		entitlementID, customer.ID, customer.Generation, customer.ExpiresAtUnix,
		sourceID, entitlementID, operationID, customer.Generation, customer.ExpiresAtUnix,
		period.StartsAtUnix, requestHash, period.ID, entitlementID, period.Ordinal,
		period.StartsAtUnix, period.EndsAtUnix, sourceID, entitlementID, entitlementID)
	projectionIndex, changed, err := appendWhiteListProjectionCAS(
		&statements, previous, next, nowUnix, projectionGuard, projectionArgs,
	)
	if err != nil || !changed {
		return ErrUnavailable
	}
	statements = append(statements, backupRPODirtyGenerationStatement(nowUnix))
	statements = append(statements, rqlite.Statement{
		SQL: `UPDATE idempotency_requests SET status='customer-renewal-backup-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND status='applying' AND changes()<>1`,
		Args: idempotencyArgs[:3],
	})
	if projectionIndex+1 != len(statements)-2 {
		return ErrUnavailable
	}
	finalize := rqlite.Statement{
		SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND resource_id=? AND operation_id=? AND status='applying'
AND EXISTS(SELECT 1 FROM whitelist_customer_access_sources
WHERE source_id=? AND entitlement_id=? AND customer_id=? AND customer_generation=?
AND customer_expires_at_unix=? AND captured_at_unix=? AND operation_id=? AND request_hash=?)
AND EXISTS(SELECT 1 FROM whitelist_billing_periods
WHERE period_id=? AND entitlement_id=? AND period_ordinal=? AND starts_at_unix=?
AND ends_at_unix=? AND included_grant_bytes=0 AND customer_access_source_id=?)
AND EXISTS(SELECT 1 FROM whitelist_balance_projections
WHERE entitlement_id=? AND version=? AND purchased_remaining_bytes=?
AND COALESCE(current_period_id,'')=? AND included_remaining_bytes=?
AND lifetime_consumed_bytes=? AND uncovered_bytes=? AND pending=? AND fresh_through_unix=?)`,
		Args: append(append([]any{string(response), nowUnix}, idempotencyArgs...),
			sourceID, entitlementID, customer.ID, customer.Generation,
			customer.ExpiresAtUnix, period.StartsAtUnix, operationID, requestHash,
			period.ID, entitlementID, period.Ordinal, period.StartsAtUnix, period.EndsAtUnix, sourceID,
			next.EntitlementID, next.Version, next.PurchasedRemainingBytes, next.CurrentPeriodID,
			next.IncludedRemainingBytes, next.LifetimeConsumedBytes, next.UncoveredBytes,
			whiteListBoolInt(next.Pending), next.FreshThroughUnix),
	}
	statements = append(statements, finalize, rqlite.Statement{
		SQL: `UPDATE idempotency_requests SET status='customer-renewal-finalize-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND status='applying' AND changes()<>1`,
		Args: idempotencyArgs[:3],
	}, rqlite.Statement{SQL: `SELECT request_hash,resource_id,operation_id,status,response_json
FROM idempotency_requests WHERE scope=? AND command_type=? AND idempotency_key=?`, Args: idempotencyArgs[:3]})
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr != nil {
		if _, found, resolveErr := s.resolveCustomerRenewalWhiteList(ctx, entitlementID, idempotencyKey, requestHash, operationID, period.ID); found || resolveErr != nil {
			return resolveErr
		}
		return ErrUnavailable
	}
	if _, found, resolveErr := s.resolveCustomerRenewalWhiteList(ctx, entitlementID, idempotencyKey, requestHash, operationID, period.ID); found || resolveErr != nil {
		return resolveErr
	}
	return ErrUnavailable
}
