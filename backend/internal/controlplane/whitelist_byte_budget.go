package controlplane

import (
	"context"
	"strconv"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const whiteListByteAllocationKeySQL = `entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=?`

// AuthorizeWhiteListByteBudgetAdmission reserves existing prepaid bytes. The
// chunk is refill granularity, never a measured throughput or a usage debit.
// Every origin, exit and retained process lifetime shares the same SQL budget.
func (s *Service) AuthorizeWhiteListByteBudgetAdmission(ctx context.Context, entitlementID, exitID string, chunkBytes int64) error {
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil || chunkBytes <= 0 || chunkBytes > 9223372036854775806 {
		return ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return err
	}
	period, _, _, err := s.whiteListAdmissionBaseFromState(ctx, entitlementID, exitID, state)
	if err != nil {
		return err
	}
	origins, err := s.whiteListObservedOriginsFromState(ctx, state)
	if err != nil || len(origins) == 0 {
		return ErrUnavailable
	}
	now := s.clock.Now().Unix()
	statements := make([]rqlite.Statement, 0, len(origins)*2)
	for _, origin := range origins {
		if origin.desired.ExitID != exitID {
			return ErrUnavailable
		}
		if err := s.whiteListByteAllocationNewLifetime(ctx, entitlementID, exitID, origin); err != nil {
			return err
		}
		key := []any{entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID}
		args := append(append([]any{}, key...), origin.receipt.ActionKey, period, now, now, origin.origin.OriginID, origin.hash)
		args = append(args, key...)
		statements = append(statements, rqlite.Statement{SQL: `INSERT INTO whitelist_byte_allocations
(entitlement_id,exit_id,origin_id,xray_process_boot_id,admitted_action_key,billing_period_id,admitted_at_unix,updated_at_unix)
SELECT ?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM whitelist_metering_origin_observations WHERE origin_id=? AND observation_sha256=?)
AND NOT EXISTS(SELECT 1 FROM whitelist_byte_allocations WHERE ` + whiteListByteAllocationKeySQL + `)`, Args: args})
	}
	for index, origin := range origins {
		// Transactional reads see earlier reservations in this same batch. Divide
		// the remaining free balance over the remaining origins; no stale Go-side
		// balance calculation can allocate the same byte twice.
		statements = append(statements, rqlite.Statement{SQL: `UPDATE whitelist_byte_allocations
SET cumulative_byte_ceiling=cumulative_byte_ceiling+COALESCE((
SELECT MAX(0,MIN(?-budget.outstanding_bytes,9223372036854775806-budget.cumulative_byte_ceiling,
 (projection.purchased_remaining_bytes+projection.included_remaining_bytes-COALESCE((
 SELECT SUM(allocation.outstanding_bytes) FROM whitelist_byte_allocation_balances AS allocation
 WHERE allocation.entitlement_id=budget.entitlement_id),0))/?))
FROM whitelist_byte_allocation_balances AS budget
JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=budget.entitlement_id
JOIN whitelist_entitlement_identities AS identity ON identity.entitlement_id=budget.entitlement_id
JOIN customers AS customer ON customer.customer_id=identity.customer_id
WHERE budget.entitlement_id=whitelist_byte_allocations.entitlement_id
AND budget.exit_id=whitelist_byte_allocations.exit_id AND budget.origin_id=whitelist_byte_allocations.origin_id
AND budget.xray_process_boot_id=whitelist_byte_allocations.xray_process_boot_id
AND projection.pending=0 AND projection.uncovered_bytes=0 AND customer.status='active' AND customer.expires_at_unix>?
),0),updated_at_unix=?
WHERE ` + whiteListByteAllocationKeySQL + ` AND billing_period_id=?
AND EXISTS(SELECT 1 FROM whitelist_metering_origin_observations WHERE origin_id=? AND observation_sha256=?)`, Args: []any{
			chunkBytes, len(origins) - index, now, now, entitlementID, exitID, origin.origin.OriginID,
			origin.receipt.XrayProcessBootID, period, origin.origin.OriginID, origin.hash,
		}})
	}
	statements = append(statements, backupRPODirtyGenerationStatement(now))
	// Unknown commit responses are resolved by reading the durable absolute
	// ceilings. A retry may refill only genuinely unreserved account bytes.
	_, _ = s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	for _, origin := range origins {
		row, err := s.whiteListByteAllocationRow(ctx, entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID)
		boundPeriod, _ := rowString(row, "billing_period_id")
		outstanding, _ := rowInt64(row, "outstanding_bytes")
		if err != nil || boundPeriod != period || outstanding <= 0 {
			return ErrUnavailable
		}
	}
	return nil
}

// Payload bytes may be represented as base64 TEXT by the database transport.
// Decode the immutable desired history before authorizing a first zero-based
// allocation. Existing allocations retain their counters and skip this check.
func (s *Service) whiteListByteAllocationNewLifetime(ctx context.Context, entitlementID, exitID string, origin whiteListObservedOrigin) error {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT desired.*
FROM whitelist_sidecar_desired AS desired
LEFT JOIN whitelist_sidecar_receipts AS receipt ON receipt.action_key=desired.action_key
WHERE desired.origin_id=? AND (receipt.action_key IS NULL OR receipt.xray_process_boot_id=?)
AND NOT EXISTS(SELECT 1 FROM whitelist_byte_allocations WHERE ` + whiteListByteAllocationKeySQL + `)
ORDER BY desired.desired_generation`, Args: []any{
		origin.origin.OriginID, origin.receipt.XrayProcessBootID,
		entitlementID, exitID, origin.origin.OriginID, origin.receipt.XrayProcessBootID,
	}})
	if err != nil || len(results) != 1 {
		return ErrUnavailable
	}
	email := whiteListManagedEmail(entitlementID, exitID)
	for _, row := range results[0].Rows {
		desired, err := whiteListRuntimeDesiredFromRow(row)
		if err != nil || whiteListContainsUser(desired.ManagedUsers, email) {
			return ErrUnavailable
		}
	}
	return nil
}

func (s *Service) whiteListByteAllocationRow(ctx context.Context, entitlementID, exitID, originID, bootID string) (map[string]any, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT * FROM whitelist_byte_allocation_balances WHERE ` + whiteListByteAllocationKeySQL,
		Args: []any{entitlementID, exitID, originID, bootID}})
	if err != nil || len(results) != 1 || len(results[0].Rows) != 1 {
		return nil, ErrUnavailable
	}
	return results[0].Rows[0], nil
}

// CompleteWhiteListByteBudgetFinal runs only after actual immutable usage has
// been applied. A normal fence retires its known unused reservation; loss of a
// process or a missing final proof never releases an unknown tail.
func (s *Service) CompleteWhiteListByteBudgetFinal(ctx context.Context, authorization WhiteListFinalReceiptAuthorization) error {
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil || !authorization.Verified() {
		return ErrUnavailable
	}
	final := authorization.Receipt()
	if final.Control.Schema != 3 {
		return nil
	}
	if final.Receipt.CumulativeBytes == nil || *final.Receipt.CumulativeBytes < 0 {
		return ErrUnavailable
	}
	cumulative := *final.Receipt.CumulativeBytes
	actual := int64(0)
	if !authorization.Unused() {
		if final.Receipt.Uplink == nil || final.Receipt.Downlink == nil || *final.Receipt.Uplink < 0 ||
			*final.Receipt.Downlink < 0 || *final.Receipt.Uplink > 9223372036854775806-*final.Receipt.Downlink {
			return ErrUnavailable
		}
		actual = *final.Receipt.Uplink + *final.Receipt.Downlink
	}
	if actual > cumulative || cumulative > 9223372036854775806 {
		return ErrUnavailable
	}
	entitlementID := authorization.Route().Entitlement.EntitlementID()
	exitID := authorization.Route().ExitID
	generation := strconv.FormatUint(final.Control.Generation, 10)
	row, err := s.whiteListByteAllocationRow(ctx, entitlementID, exitID, final.OriginID, final.Control.BootID)
	if err != nil {
		return err
	}
	settled, settledOK := rowInt64(row, "settled_bytes")
	ceiling, ceilingOK := rowInt64(row, "cumulative_byte_ceiling")
	previousGeneration, _ := rowString(row, "last_fenced_generation")
	previous, parseErr := strconv.ParseUint(previousGeneration, 10, 64)
	if !settledOK || !ceilingOK || parseErr != nil || settled < actual {
		return ErrUnavailable
	}
	if previous >= final.Control.Generation {
		// An ACK may have been lost after this fence was retired and fresh quota
		// issued. Replaying that final must not shrink the new grant.
		if previous == final.Control.Generation && (row["last_fenced_receipt_id"] != final.ReceiptID || row["last_fenced_proof_sha256"] != final.ProofSHA256) {
			return ErrConflict
		}
		return nil
	}
	if settled != actual || cumulative > ceiling {
		return ErrUnavailable
	}
	_, _ = s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `UPDATE whitelist_byte_allocations
SET cumulative_byte_ceiling=?,retired_unbilled_bytes=?,last_fenced_generation=?,last_fenced_receipt_id=?,last_fenced_proof_sha256=?,updated_at_unix=?
WHERE ` + whiteListByteAllocationKeySQL + ` AND last_fenced_generation=? AND cumulative_byte_ceiling=?
AND EXISTS(SELECT 1 FROM whitelist_byte_allocation_balances AS budget WHERE budget.entitlement_id=whitelist_byte_allocations.entitlement_id
AND budget.exit_id=whitelist_byte_allocations.exit_id AND budget.origin_id=whitelist_byte_allocations.origin_id
AND budget.xray_process_boot_id=whitelist_byte_allocations.xray_process_boot_id AND budget.settled_bytes=?)`, Args: []any{
		cumulative, cumulative - actual, generation, final.ReceiptID, final.ProofSHA256, s.clock.Now().Unix(),
		entitlementID, exitID, final.OriginID, final.Control.BootID, previousGeneration, ceiling, actual,
	}}, backupRPODirtyGenerationStatement(s.clock.Now().Unix()))
	row, err = s.whiteListByteAllocationRow(ctx, entitlementID, exitID, final.OriginID, final.Control.BootID)
	if err != nil || row["last_fenced_generation"] != generation || row["last_fenced_receipt_id"] != final.ReceiptID || row["last_fenced_proof_sha256"] != final.ProofSHA256 {
		return ErrUnavailable
	}
	return nil
}
