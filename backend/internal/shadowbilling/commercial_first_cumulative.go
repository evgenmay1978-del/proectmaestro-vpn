package shadowbilling

import (
	"context"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// ApplyCommercialFirstCumulative accounts the entire first real counter reading
// for an immutable, zero-start-authorized admission. It uses the same durable
// event/checkpoint/interval/outbox transaction and balance receipt as normal
// commercial deltas. An absent counter must never be passed as a zero sample.
// Generic ApplyCommercialOrdered retains its EPOCH_STARTED baseline semantics.
func (store *DurableStore) ApplyCommercialFirstCumulative(
	ctx context.Context,
	event CommercialOrderedUsageEvent,
	policy Policy,
	debiter CommercialDebiter,
) (DurableResult, error) {
	price, err := ResolvePrice(policy.Prices)
	if err != nil || event.CounterGeneration != 1 || event.SampleSequence != 1 || event.Source.ResetSequence != 0 ||
		policy.Unit != UnitGBDecimal || policy.Basis != BasisUplinkPlusDownlink ||
		policy.IncludedBytes != 0 || policy.SoftLimitBytes != 0 || policy.HardLimitBytes != 0 || policy.GraceBytes != 0 ||
		price.Source != PriceGlobal || price.Price.Mode != PriceFree || price.Price.Currency != "" || price.Price.MinorUnitsPerUnit != 0 {
		return DurableResult{}, ErrInvalidInput
	}
	return store.applyCommercialOrdered(ctx, event, policy, debiter, true)
}

// This arithmetic mode is private: only the durable adapter may authorize its
// transaction. The zero start is admission provenance, never a persisted sample.
func applyFirstCumulative(state State, event OrderedUsageEvent, policy Policy) (State, Decision, error) {
	if event.CounterGeneration != 1 || event.SampleSequence != 1 {
		return state, Decision{}, ErrInvalidInput
	}
	return apply(state, event.UsageEvent, policy, sampleOrder{
		enabled: true, generation: 1, sequence: 1, firstCumulative: true,
	})
}

// The guard and metering writes share one transaction. It does not refresh the
// admission, health, or balance. An accepted event replay bypasses this current
// poll guard so its existing outbox can still settle after health becomes stale.
// Admission and sample must belong to the same paid period: no first-use reset
// or approximate allocation is authorized by a topup, restart, or period change.
func commercialFirstCumulativeGuard(source CommercialSourceBinding) rqlite.Statement {
	return rqlite.Statement{SQL: `SELECT CASE WHEN (EXISTS (
SELECT 1 FROM whitelist_first_use_admissions AS admission
JOIN whitelist_billing_periods AS period ON period.period_id=admission.billing_period_id
 AND period.entitlement_id=admission.entitlement_id
JOIN whitelist_metering_origin_observations AS observation ON observation.origin_id=admission.origin_id
JOIN whitelist_sidecar_receipts AS receipt ON receipt.action_key=observation.action_key
 AND receipt.origin_id=admission.origin_id AND receipt.xray_process_boot_id=admission.xray_process_boot_id
JOIN whitelist_sidecar_desired AS desired ON desired.action_key=receipt.action_key
JOIN whitelist_sidecar_origins AS origin ON origin.origin_id=desired.origin_id
WHERE admission.entitlement_id=? AND admission.exit_id=? AND admission.origin_id=?
 AND admission.xray_process_boot_id=? AND admission.billing_period_id=? AND admission.zero_start_authorized=1
 AND admission.first_observed_at_unix>0 AND admission.admitted_at_unix<=admission.first_observed_at_unix
 AND admission.first_observed_at_unix<=? AND period.starts_at_unix<=admission.admitted_at_unix AND ?<period.ends_at_unix
 AND observation.sampled_at_unix=? AND observation.checked_at_unix>=observation.sampled_at_unix
 AND observation.checked_at_unix-observation.sampled_at_unix<5
 AND receipt.applied_at_unix<=observation.sampled_at_unix AND observation.checked_at_unix<receipt.expires_at_unix
 AND desired.exit_id=admission.exit_id AND desired.origin_id=admission.origin_id
 AND origin.active=1 AND origin.node_id=desired.node_id AND origin.release_id=desired.release_id
 AND origin.profile_id=desired.profile_id AND origin.preset_id=desired.preset_id AND origin.config_digest=desired.config_digest
 AND desired.desired_generation=(SELECT MAX(current.desired_generation) FROM whitelist_sidecar_desired AS current WHERE current.origin_id=origin.origin_id)
 AND instr(CAST(desired.payload_json AS TEXT),'"' || ? || '"')>0
 AND instr(CAST(observation.available_users_json AS TEXT),'"' || ? || '"')>0
 AND instr(CAST(observation.unavailable_users_json AS TEXT),'"' || ? || '"')=0
)
OR EXISTS (
 SELECT 1 FROM idempotency_requests AS accepted
 JOIN idempotency_requests AS proof ON proof.scope='whitelist-final-proof'
  AND proof.command_type='accept-agent-fence' AND proof.idempotency_key=accepted.idempotency_key
  AND proof.resource_id=accepted.resource_id AND proof.status='applied'
 JOIN whitelist_first_use_admissions AS admission ON admission.entitlement_id=accepted.resource_id
 JOIN whitelist_billing_periods AS period ON period.period_id=admission.billing_period_id AND period.entitlement_id=admission.entitlement_id
 WHERE accepted.scope='whitelist-final-metering' AND accepted.command_type='accept-final-source'
  AND accepted.operation_id=? AND accepted.request_hash=? AND accepted.resource_id=? AND accepted.status='applied'
  AND admission.exit_id=? AND admission.origin_id=? AND admission.xray_process_boot_id=?
  AND admission.billing_period_id=? AND admission.zero_start_authorized=1
  AND period.starts_at_unix<=admission.admitted_at_unix AND admission.admitted_at_unix<=? AND ?<period.ends_at_unix
))
AND NOT EXISTS (
 SELECT 1 FROM whitelist_commercial_metering_sources AS previous
 JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=previous.meter_epoch
 WHERE previous.entitlement_id=? AND previous.exit_id=? AND previous.origin_id=? AND epoch.xray_process_boot_id=?
)
AND NOT EXISTS (
 SELECT 1 FROM whitelist_metering_checkpoints WHERE entitlement_id=? AND instance_id=? AND meter_epoch=? AND xray_identity=?
)
THEN 1 ELSE abs(-9223372036854775808) END AS first_cumulative_admission_guard`, Args: []any{
		source.EntitlementID, source.ExitID, source.OriginID, source.XrayProcessBootID, source.BillingPeriodID,
		source.SampledAtUnix, source.SampledAtUnix, source.SampledAtUnix,
		source.RouteXrayIdentity, source.RouteXrayIdentity, source.RouteXrayIdentity,
		source.EventID, source.SourceSHA256, source.EntitlementID, source.ExitID, source.OriginID,
		source.XrayProcessBootID, source.BillingPeriodID, source.SampledAtUnix, source.SampledAtUnix,
		source.EntitlementID, source.ExitID, source.OriginID, source.XrayProcessBootID,
		source.EntitlementID, source.OriginID, source.MeterEpoch, source.BaseXrayIdentity,
	}}
}
