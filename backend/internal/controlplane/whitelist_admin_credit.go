package controlplane

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

// A retry bound to different input may refer to an earlier applied operation.
// Keep it distinguishable from a definitive rejection of a new operation.
var errWhiteListRetryConflict = fmt.Errorf("%w: retry identity differs", ErrConflict)

type CreditWhiteListManualGBCommand struct {
	EntitlementID  string
	GB             int64
	IdempotencyKey string
	Actor          string
}

type WhiteListManualCreditResult struct {
	OperationID             string `json:"operation_id"`
	CreditID                string `json:"credit_id"`
	Bytes                   int64  `json:"bytes"`
	PurchasedRemainingBytes int64  `json:"purchased_remaining_bytes"`
	ProjectionVersion       int64  `json:"projection_version"`
}

// Manual GB shares the non-expiring purchased bucket, but its immutable source
// is this administrative ledger. It never manufactures an order/payment.
func (s *Service) CreditWhiteListManualGB(ctx context.Context, command CreditWhiteListManualGBCommand) (WhiteListManualCreditResult, error) {
	if command.GB < 1 || command.GB > (whitelistbalance.MaxExclusive-1)/whitelistbalance.GBDecimal {
		return WhiteListManualCreditResult{}, ErrConflict
	}
	return s.writeWhiteListAdministrativeBalance(ctx, command, false)
}

// A period records existing ordinary access, without extending it. Creating a
// subsequent period after actual metering still requires the terminal settlement
// path; this method never relabels old usage or assumes a boundary counter.
func (s *Service) ensureWhiteListAdminAccessPeriod(ctx context.Context, command SetWhiteListPublicationCommand) error {
	_, err := s.writeWhiteListAdministrativeBalance(ctx, CreditWhiteListManualGBCommand{
		EntitlementID: command.EntitlementID, IdempotencyKey: command.IdempotencyKey, Actor: command.Actor,
	}, true)
	return err
}

func (s *Service) writeWhiteListAdministrativeBalance(ctx context.Context, command CreditWhiteListManualGBCommand, accessPeriod bool) (WhiteListManualCreditResult, error) {
	var empty WhiteListManualCreditResult
	if !validWhiteListID(command.EntitlementID) || !validWhiteListID(command.IdempotencyKey) || !validWhiteListID(command.Actor) {
		return empty, ErrConflict
	}
	kind := "whitelist_manual_credit"
	if accessPeriod {
		kind = "whitelist_admin_access"
	}
	scope := kind + ":" + command.EntitlementID
	hash, err := whiteListCanonicalHash(struct {
		Version       int
		EntitlementID string
		GB            int64
		Actor         string
		Kind          string
	}{1, command.EntitlementID, command.GB, s.auditActor(command.Actor), kind})
	if err != nil {
		return empty, ErrUnavailable
	}
	if saved, found, err := s.resolveWhiteListAdministrativeBalance(ctx, scope, kind, command.IdempotencyKey, hash); found || err != nil {
		return saved, err
	}
	now := s.clock.Now().Unix()
	loaded, err := s.loadWhiteListBalance(ctx, now, command.EntitlementID)
	if err != nil {
		return empty, err
	}
	if loaded.CommercialPending || loaded.RenewalPending || (loaded.State.Projection != nil && loaded.State.Projection.Pending) {
		return empty, ErrConflict
	}
	next := whitelistbalance.BalanceProjection{EntitlementID: command.EntitlementID, Version: 1}
	if loaded.State.Projection != nil {
		next = *loaded.State.Projection
		if next.Version >= whitelistbalance.MaxExclusive-1 {
			return empty, ErrConflict
		}
		next.Version++
	}
	amount := command.GB * whitelistbalance.GBDecimal
	if !accessPeriod && (amount <= 0 || next.PurchasedRemainingBytes >= whitelistbalance.MaxExclusive-amount || next.IncludedRemainingBytes >= whitelistbalance.MaxExclusive-amount-next.PurchasedRemainingBytes) {
		return empty, ErrConflict
	}
	next.PurchasedRemainingBytes += amount
	var period *whitelistbalance.Period
	if accessPeriod {
		if !loaded.PrimaryActive {
			return empty, ErrConflict
		}
		for _, p := range loaded.State.Periods {
			if p.StartsAtUnix <= now && now < p.EndsAtUnix {
				if period != nil {
					return empty, ErrConflict
				}
				candidate := p
				period = &candidate
			}
		}
		if period != nil {
			if next.CurrentPeriodID == period.ID {
				return empty, nil
			}
			if next.IncludedRemainingBytes != 0 {
				return empty, ErrConflict
			}
			next.CurrentPeriodID = period.ID
		}
		if period == nil && next.IncludedRemainingBytes != 0 {
			return empty, ErrConflict
		}
		if period == nil {
			for _, existing := range loaded.State.Periods {
				if existing.EndsAtUnix > now {
					return empty, ErrConflict
				}
			}
		}
	}
	operation, err := s.ids.NewID(kind)
	if err != nil {
		return empty, ErrUnavailable
	}
	resource := whiteListSourceKey(kind, command.EntitlementID, command.IdempotencyKey)
	result := WhiteListManualCreditResult{OperationID: operation, CreditID: resource, Bytes: amount, PurchasedRemainingBytes: next.PurchasedRemainingBytes, ProjectionVersion: next.Version}
	statements := []rqlite.Statement{{SQL: `INSERT INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix)
VALUES(?,?,?,?,?,'admin_balance',?,'applying',NULL,?,NULL)`, Args: []any{scope, kind, command.IdempotencyKey, hash, command.EntitlementID, operation, now}}}
	if accessPeriod && period == nil {
		sourceID := whiteListSourceKey("admin-access", command.EntitlementID, command.IdempotencyKey)
		next.CurrentPeriodID = whiteListSourceKey("admin-period", sourceID)
		// No active or future period may race in; an admitted source may not be silently
		// rolled over. The immutable proof captures the actual current customer row.
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO whitelist_customer_access_sources(source_id,entitlement_id,customer_id,customer_generation,customer_expires_at_unix,captured_at_unix,operation_id,request_hash,actor_hmac)
SELECT ?,identity.entitlement_id,customer.customer_id,customer.generation,customer.expires_at_unix,?,?,?,?
FROM whitelist_entitlement_identities AS identity JOIN customers AS customer ON customer.customer_id=identity.customer_id
WHERE identity.entitlement_id=? AND customer.status='active' AND customer.expires_at_unix>?
AND NOT EXISTS(SELECT 1 FROM whitelist_billing_periods WHERE entitlement_id=identity.entitlement_id AND ends_at_unix>?)
AND NOT EXISTS(SELECT 1 FROM whitelist_first_use_admissions WHERE entitlement_id=identity.entitlement_id)`, Args: []any{sourceID, now, operation, hash, s.auditActor(command.Actor), command.EntitlementID, now, now}},
			rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,created_at_unix,customer_access_source_id)
SELECT ?,source.entitlement_id,COALESCE((SELECT MAX(period_ordinal)+1 FROM whitelist_billing_periods WHERE entitlement_id=source.entitlement_id),0),source.captured_at_unix,source.customer_expires_at_unix,0,NULL,?,source.source_id
FROM whitelist_customer_access_sources AS source WHERE source.source_id=? AND source.operation_id=?`, Args: []any{next.CurrentPeriodID, now, sourceID, operation}})
	}
	guard := `EXISTS(SELECT 1 FROM whitelist_entitlement_identities AS identity JOIN customers AS customer ON customer.customer_id=identity.customer_id WHERE identity.entitlement_id=? AND customer.status<>'deleted')`
	args := []any{command.EntitlementID}
	if accessPeriod {
		guard += ` AND EXISTS(SELECT 1 FROM whitelist_billing_periods AS period JOIN whitelist_entitlement_identities AS identity ON identity.entitlement_id=period.entitlement_id JOIN customers AS customer ON customer.customer_id=identity.customer_id WHERE period.entitlement_id=? AND period.period_id=? AND period.starts_at_unix<=? AND period.ends_at_unix>? AND customer.status='active' AND customer.expires_at_unix>?)`
		args = append(args, command.EntitlementID, next.CurrentPeriodID, now, now, now)
	}
	// The projection version CAS serializes credits and concurrent usage. Freshness
	// is copied unchanged: an admin write is not a meter observation.
	if _, changed, err := appendWhiteListProjectionCAS(&statements, loaded.State.Projection, next, now, guard, args); err != nil || !changed {
		return empty, ErrUnavailable
	}
	statements = append(statements, rqlite.Statement{SQL: `UPDATE idempotency_requests SET status='admin-projection-rejected' WHERE scope=? AND command_type=? AND idempotency_key=? AND status='applying' AND changes()<>1`, Args: []any{scope, kind, command.IdempotencyKey}})
	if !accessPeriod {
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO whitelist_manual_credits(credit_id,entitlement_id,bytes,purchased_after_bytes,projection_version,operation_id,request_hash,actor_hmac,created_at_unix) VALUES(?,?,?,?,?,?,?,?,?)`, Args: []any{resource, command.EntitlementID, amount, next.PurchasedRemainingBytes, next.Version, operation, hash, s.auditActor(command.Actor), now}})
	}
	auditEventID := auditID(kind, command.EntitlementID, next.Version, now)
	envelope, digest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{Channel: "panel-admin", SourceEventID: command.IdempotencyKey, ResultHash: hash})
	if err != nil {
		return empty, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return empty, ErrUnavailable
	}
	statements = append(statements, backupRPODirtyGenerationStatement(now), rqlite.Statement{SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,details_envelope,details_sha256,created_at_unix) VALUES(?,?,?,'whitelist-entitlement',?,?,?,?)`, Args: []any{auditEventID, s.auditActor(command.Actor), kind, s.auditResource(command.EntitlementID), envelope, digest, now}}, rqlite.Statement{SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=? WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=? AND operation_id=? AND status='applying'`, Args: []any{string(body), now, scope, kind, command.IdempotencyKey, hash, operation}})
	_, writeErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if writeErr == nil {
		return result, nil
	}
	if saved, found, err := s.resolveWhiteListAdministrativeBalance(ctx, scope, kind, command.IdempotencyKey, hash); found || err != nil {
		return saved, err
	}
	if isUnknownWrite(writeErr) {
		return empty, ErrUnavailable
	}
	return empty, ErrConflict
}

func (s *Service) resolveWhiteListAdministrativeBalance(ctx context.Context, scope, kind, key, hash string) (WhiteListManualCreditResult, bool, error) {
	var result WhiteListManualCreditResult
	rows, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT request_hash,status,response_json FROM idempotency_requests WHERE scope=? AND command_type=? AND idempotency_key=?`, Args: []any{scope, kind, key}})
	if err != nil || len(rows) != 1 {
		return result, false, ErrUnavailable
	}
	if len(rows[0].Rows) == 0 {
		return result, false, nil
	}
	if len(rows[0].Rows) != 1 {
		return result, false, ErrUnavailable
	}
	row := rows[0].Rows[0]
	saved, ok := rowString(row, "request_hash")
	if !ok || saved != hash {
		return result, true, errWhiteListRetryConflict
	}
	status, ok := rowString(row, "status")
	if !ok || status != "applied" {
		return result, true, ErrUnavailable
	}
	body, ok := rowString(row, "response_json")
	if !ok || json.Unmarshal([]byte(body), &result) != nil || result.OperationID == "" || result.ProjectionVersion <= 0 {
		return result, true, ErrUnavailable
	}
	return result, true, nil
}

// Fixed SQL fragment used by live admission and historical final accounting.
// The latter must retain old immutable authority even if ordinary access expired.
const whiteListPeriodAuthoritySQL = `(EXISTS(
SELECT 1 FROM orders AS access_order JOIN whitelist_entitlement_identities AS owner ON owner.entitlement_id=period.entitlement_id
WHERE access_order.order_id=period.access_order_id AND access_order.customer_id=owner.customer_id
AND access_order.payment_state='confirmed' AND access_order.decision='confirmed' AND access_order.confirmed_at_unix IS NOT NULL)
OR EXISTS(SELECT 1 FROM whitelist_customer_access_sources AS access_source JOIN whitelist_entitlement_identities AS owner ON owner.entitlement_id=period.entitlement_id
WHERE access_source.source_id=period.customer_access_source_id AND access_source.entitlement_id=period.entitlement_id AND access_source.customer_id=owner.customer_id
AND access_source.captured_at_unix=period.starts_at_unix AND access_source.customer_expires_at_unix=period.ends_at_unix AND period.included_grant_bytes=0))`
