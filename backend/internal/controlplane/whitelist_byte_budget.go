package controlplane

import (
	"context"
	"strconv"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const whiteListByteAllocationKeySQL = `entitlement_id=? AND exit_id=? AND origin_id=? AND xray_process_boot_id=?`

func whiteListPerRouteByteBudget(totalBytes int64) (int64, bool) {
	if totalBytes <= 0 || totalBytes%whiteListCommercialExitCount != 0 {
		return 0, false
	}
	perRoute := totalBytes / whiteListCommercialExitCount
	return perRoute, perRoute > 0
}

func whiteListMeteringAdmissionCandidateExitSets(candidates []WhiteListMeteringAdmissionCandidate) (map[string]whiteListMeteringExitSet, bool) {
	sets := make(map[string]whiteListMeteringExitSet)
	for _, candidate := range candidates {
		if !validEntitlementID(candidate.EntitlementID) || !routeCredentialExit(candidate.ExitID) {
			return nil, false
		}
		if sets[candidate.EntitlementID] == nil {
			sets[candidate.EntitlementID] = whiteListMeteringExitSet{}
		}
		if _, duplicate := sets[candidate.EntitlementID][candidate.ExitID]; duplicate {
			return nil, false
		}
		sets[candidate.EntitlementID][candidate.ExitID] = struct{}{}
	}
	for _, exits := range sets {
		if len(exits) != whiteListCommercialExitCount {
			return nil, false
		}
	}
	return sets, len(sets) > 0
}

// Filter before taking the fresh counter snapshot. A funded allocation needs
// no refill transaction until half its chunk is consumed. This grants nothing:
// every use lease still rechecks actual settlement, balance, boot and freshness.
func (s *Service) WhiteListByteBudgetRefillCandidates(ctx context.Context, plan WhiteListMeteringPlan, candidates []WhiteListMeteringAdmissionCandidate, chunkBytes int64) ([]WhiteListMeteringAdmissionCandidate, error) {
	routeChunkBytes, chunkOK := whiteListPerRouteByteBudget(chunkBytes)
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil || !chunkOK || len(plan.Origins) == 0 {
		return nil, ErrUnavailable
	}
	if len(candidates) == 0 {
		return candidates, nil
	}
	candidateSets, candidatesOK := whiteListMeteringAdmissionCandidateExitSets(candidates)
	if !candidatesOK {
		return nil, ErrUnavailable
	}
	minimum := routeChunkBytes / 2
	if minimum == 0 {
		minimum = 1
	}
	statements := make([]rqlite.Statement, 0, len(candidates))
	for _, candidate := range candidates {
		exits, exists := candidateSets[candidate.EntitlementID]
		if !exists {
			return nil, ErrUnavailable
		}
		if _, exists = exits[candidate.ExitID]; !exists {
			return nil, ErrUnavailable
		}
		now := s.clock.Now().Unix()
		args := []any{candidate.EntitlementID, candidate.ExitID, minimum, now, now}
		bindings := make([]string, 0, len(plan.Origins))
		for _, origin := range plan.Origins {
			bindings = append(bindings, "(allocation.origin_id=? AND allocation.xray_process_boot_id=?)")
			args = append(args, origin.Origin.OriginID, origin.Receipt.XrayProcessBootID)
		}
		statements = append(statements, rqlite.Statement{SQL: `SELECT COUNT(*) AS funded_origins
FROM whitelist_byte_allocation_balances AS allocation
JOIN whitelist_billing_periods AS period ON period.period_id=allocation.billing_period_id AND period.entitlement_id=allocation.entitlement_id
WHERE allocation.entitlement_id=? AND allocation.exit_id=? AND allocation.outstanding_bytes>=?
AND period.starts_at_unix<=? AND ?<period.ends_at_unix AND (` + strings.Join(bindings, " OR ") + `)`, Args: args})
	}
	results, err := s.store.db.QueryLinearizable(ctx, statements...)
	if err != nil || len(results) != len(candidates) {
		return nil, ErrUnavailable
	}
	needed := make([]WhiteListMeteringAdmissionCandidate, 0, len(candidates))
	for index, candidate := range candidates {
		row, ok := firstRow(results[index : index+1])
		funded, valid := rowInt64(row, "funded_origins")
		if !ok || !valid {
			return nil, ErrUnavailable
		}
		if funded != int64(len(plan.Origins)) {
			needed = append(needed, candidate)
		}
	}
	return needed, nil
}

// AuthorizeWhiteListByteBudgetAdmission reserves existing prepaid bytes. The
// chunk is refill granularity, never a measured throughput or a usage debit.
// Every origin, exit and retained process lifetime shares the same SQL budget.
func (s *Service) AuthorizeWhiteListByteBudgetAdmission(ctx context.Context, entitlementID, exitID string, chunkBytes int64) error {
	routeChunkBytes, chunkOK := whiteListPerRouteByteBudget(chunkBytes)
	if s == nil || s.store == nil || s.store.db == nil || s.clock == nil || ctx == nil || !chunkOK || routeChunkBytes > 9223372036854775806 || !routeCredentialExit(exitID) {
		return ErrUnavailable
	}
	state, err := s.loadWhiteListSidecarRuntimeState(ctx)
	if err != nil {
		return err
	}
	if _, exitsReady := whiteListRequiredRuntimeExits(state.exits); !exitsReady ||
		!whiteListRuntimeCredentialUsable(state.credentials[entitlementID], state.exits) {
		return ErrUnavailable
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
			routeChunkBytes, len(origins) - index, now, now, entitlementID, exitID, origin.origin.OriginID,
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
// An unknown action superseded on a predecessor boot cannot reach this boot:
// the agent durably rejects generations older than its acknowledged desired.
// Preserve all old allocations and their unknown outstanding reservations.
func (s *Service) whiteListByteAllocationNewLifetime(ctx context.Context, entitlementID, exitID string, origin whiteListObservedOrigin) error {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT desired.*
FROM whitelist_sidecar_desired AS desired
LEFT JOIN whitelist_sidecar_receipts AS receipt ON receipt.action_key=desired.action_key
WHERE desired.origin_id=? AND (receipt.xray_process_boot_id=? OR (
receipt.action_key IS NULL AND NOT EXISTS (
 SELECT 1 FROM whitelist_sidecar_receipts AS superseding
 WHERE superseding.origin_id=desired.origin_id
 AND superseding.desired_generation>desired.desired_generation
 AND superseding.desired_generation<? AND superseding.xray_process_boot_id<>?
 AND superseding.applied_at_unix<?
)))
AND NOT EXISTS(SELECT 1 FROM whitelist_byte_allocations WHERE ` + whiteListByteAllocationKeySQL + `)
ORDER BY desired.desired_generation`, Args: []any{
		origin.origin.OriginID, origin.receipt.XrayProcessBootID,
		origin.receipt.DesiredGeneration, origin.receipt.XrayProcessBootID, origin.receipt.AppliedAt.Unix(),
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
