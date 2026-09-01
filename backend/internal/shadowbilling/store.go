package shadowbilling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

var (
	ErrDurableStateInvalid    = errors.New("shadowbilling: durable metering state is invalid")
	ErrPolicySnapshotConflict = errors.New("shadowbilling: policy snapshot conflicts with the recorded period")
)

// Projection is the durable, non-charging view of one white-list billing
// period. Suspension is a recommendation only; this package cannot mutate
// entitlement or customer access state.
type Projection struct {
	EntitlementID    string
	BillingPeriodID  string
	UsedBytes        uint64
	IncludedBytes    uint64
	RemainingBytes   uint64
	SoftLimitReached bool
	Suspension       SuspensionRecommendation
	Pending          bool
	Diagnostic       Diagnostic
	Version          uint64
}

// DurableResult is persisted verbatim for exact EventID replay.
type DurableResult struct {
	Decision   Decision
	Projection Projection
}

// DurableStore persists the existing pure ApplyOrdered state machine through
// the repository's single rqlite abstraction.
type DurableStore struct {
	db rqlite.RQLite
}

// NewDurableStore constructs the shadow-only metering adapter.
func NewDurableStore(db rqlite.RQLite) (*DurableStore, error) {
	if db == nil {
		return nil, ErrInvalidInput
	}
	return &DurableStore{db: db}, nil
}

type canonicalPolicy struct {
	Version           int          `json:"version"`
	AccountID         string       `json:"account_id"`
	EntitlementID     string       `json:"entitlement_id"`
	TransportID       string       `json:"transport_id"`
	BillingPeriodID   string       `json:"billing_period_id"`
	XrayIdentity      string       `json:"xray_identity"`
	Unit              TrafficUnit  `json:"unit"`
	Basis             TrafficBasis `json:"basis"`
	IncludedBytes     uint64       `json:"included_bytes"`
	SoftLimitBytes    uint64       `json:"soft_limit_bytes"`
	HardLimitBytes    uint64       `json:"hard_limit_bytes"`
	GraceBytes        uint64       `json:"grace_bytes"`
	PriceMode         PriceMode    `json:"price_mode"`
	PriceSource       PriceSource  `json:"price_source"`
	Currency          string       `json:"currency"`
	MinorUnitsPerUnit uint64       `json:"minor_units_per_unit"`
}

type canonicalEvent struct {
	Version           int             `json:"version"`
	EventID           string          `json:"event_id"`
	InstanceID        string          `json:"instance_id"`
	MeterEpoch        string          `json:"meter_epoch"`
	XrayIdentity      string          `json:"xray_identity"`
	UplinkBytes       uint64          `json:"uplink_bytes"`
	DownlinkBytes     uint64          `json:"downlink_bytes"`
	CounterGeneration uint64          `json:"counter_generation"`
	SampleSequence    uint64          `json:"sample_sequence"`
	Policy            canonicalPolicy `json:"policy"`
}

type durableRead struct {
	period          bool
	policySHA256    string
	projection      bool
	current         Projection
	checkpoints     map[meterKey]counter
	storedEvent     bool
	eventPayload    string
	eventResultJSON string
}

// ApplyOrdered reads a linearizable snapshot, delegates all metering rules to
// ApplyOrdered, and persists the event, checkpoint, interval, and projection
// in one transaction.
func (store *DurableStore) ApplyOrdered(ctx context.Context, event OrderedUsageEvent, policy Policy) (DurableResult, error) {
	if store == nil || store.db == nil || ctx == nil {
		return DurableResult{}, ErrInvalidInput
	}
	if _, _, err := ApplyOrdered(NewState(), event, policy); err != nil {
		return DurableResult{}, err
	}
	resolved, err := ResolvePrice(policy.Prices)
	if err != nil {
		return DurableResult{}, err
	}
	canonical := canonicalPolicy{
		Version: 1, AccountID: policy.accountID, EntitlementID: policy.entitlementID,
		TransportID: policy.transportID, BillingPeriodID: policy.billingPeriodID,
		XrayIdentity: policy.expectedXrayIdentity, Unit: policy.Unit, Basis: policy.Basis,
		IncludedBytes: policy.IncludedBytes, SoftLimitBytes: policy.SoftLimitBytes,
		HardLimitBytes: policy.HardLimitBytes, GraceBytes: policy.GraceBytes,
		PriceMode: resolved.Price.Mode, PriceSource: resolved.Source,
		Currency: resolved.Price.Currency, MinorUnitsPerUnit: resolved.Price.MinorUnitsPerUnit,
	}
	policySHA256, err := canonicalSHA256(canonical)
	if err != nil {
		return DurableResult{}, ErrDurableStateInvalid
	}
	payloadSHA256, err := canonicalSHA256(canonicalEvent{
		Version: 1, EventID: event.EventID, InstanceID: event.InstanceID,
		MeterEpoch: event.MeterEpoch, XrayIdentity: event.XrayIdentity,
		UplinkBytes: event.UplinkBytes, DownlinkBytes: event.DownlinkBytes,
		CounterGeneration: event.CounterGeneration, SampleSequence: event.SampleSequence,
		Policy: canonical,
	})
	if err != nil {
		return DurableResult{}, ErrDurableStateInvalid
	}

	loaded, err := store.read(ctx, event.EventID, policy)
	if err != nil {
		return DurableResult{}, err
	}
	if loaded.storedEvent {
		return storedReplay(event.EventID, payloadSHA256, loaded.eventPayload, loaded.eventResultJSON)
	}
	if loaded.period && loaded.policySHA256 != policySHA256 {
		return DurableResult{}, ErrPolicySnapshotConflict
	}
	if loaded.period != loaded.projection {
		return DurableResult{}, ErrDurableStateInvalid
	}
	if !loaded.period && len(loaded.checkpoints) != 0 {
		return DurableResult{}, ErrDurableStateInvalid
	}

	state := NewState()
	period := periodKey{policy.entitlementID, policy.billingPeriodID}
	if loaded.projection {
		state.included[period] = loaded.current.RemainingBytes
		state.measured[period] = loaded.current.UsedBytes
		if loaded.current.Suspension.Recommended {
			state.suspended[policy.entitlementID] = true
		}
	}
	for key, checkpoint := range loaded.checkpoints {
		state.counters[key] = checkpoint
	}

	next, decision, err := ApplyOrdered(state, event, policy)
	if err != nil {
		return DurableResult{}, err
	}
	remaining := policy.IncludedBytes
	if value, ok := next.included[period]; ok {
		remaining = value
	}
	used := next.measured[period]
	softLimitReached := decision.SoftLimitReached
	suspension := decision.Suspension
	if loaded.projection {
		softLimitReached = softLimitReached || loaded.current.SoftLimitReached
		if !suspension.Recommended && loaded.current.Suspension.Recommended {
			suspension = loaded.current.Suspension
		}
	}
	version := uint64(1)
	if loaded.projection {
		if loaded.current.Version >= uint64(math.MaxInt64) {
			return DurableResult{}, ErrDurableStateInvalid
		}
		version = loaded.current.Version + 1
	}
	result := DurableResult{
		Decision: decision,
		Projection: Projection{
			EntitlementID: policy.entitlementID, BillingPeriodID: policy.billingPeriodID,
			UsedBytes: used, IncludedBytes: policy.IncludedBytes, RemainingBytes: remaining,
			SoftLimitReached: softLimitReached, Suspension: suspension,
			Pending: decision.Diagnostic != "", Diagnostic: decision.Diagnostic, Version: version,
		},
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return DurableResult{}, ErrDurableStateInvalid
	}

	statements, err := durableStatements(event, canonical, policySHA256, payloadSHA256, loaded, next, result, string(resultJSON))
	if err != nil {
		return DurableResult{}, err
	}
	if _, err := store.db.Request(ctx, rqlite.Linearizable, true, statements...); err != nil {
		if replay, resolvedErr := store.resolveWrite(ctx, event.EventID, payloadSHA256); resolvedErr == nil {
			return replay, nil
		} else if errors.Is(resolvedErr, ErrEventIDConflict) {
			return DurableResult{}, resolvedErr
		}
		return DurableResult{}, fmt.Errorf("shadowbilling: persist durable metering transaction: %w", err)
	}
	return result, nil
}

func canonicalSHA256(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (store *DurableStore) read(ctx context.Context, eventID string, policy Policy) (durableRead, error) {
	periodArgs := []any{policy.entitlementID, policy.billingPeriodID}
	results, err := store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT payload_sha256,result_json FROM whitelist_metering_events WHERE event_id=?`, Args: []any{eventID}},
		rqlite.Statement{SQL: `SELECT policy_sha256 FROM whitelist_metering_periods WHERE entitlement_id=? AND billing_period_id=?`, Args: periodArgs},
		rqlite.Statement{SQL: `SELECT used_bytes,included_bytes,remaining_bytes,soft_limit_reached,hard_limit_recommended,suspension_reason,reconciliation_pending,reconciliation_diagnostic,version FROM whitelist_metering_projections WHERE entitlement_id=? AND billing_period_id=?`, Args: periodArgs},
		rqlite.Statement{SQL: `SELECT instance_id,meter_epoch,xray_identity,counter_generation,sample_sequence,uplink_bytes,downlink_bytes FROM whitelist_metering_checkpoints WHERE entitlement_id=? AND billing_period_id=?`, Args: periodArgs},
	)
	if err != nil {
		return durableRead{}, fmt.Errorf("shadowbilling: read durable metering state: %w", err)
	}
	if len(results) != 4 || len(results[0].Rows) > 1 || len(results[1].Rows) > 1 || len(results[2].Rows) > 1 {
		return durableRead{}, ErrDurableStateInvalid
	}
	loaded := durableRead{checkpoints: make(map[meterKey]counter)}
	if len(results[0].Rows) == 1 {
		loaded.storedEvent = true
		loaded.eventPayload, err = rowString(results[0].Rows[0], "payload_sha256")
		if err != nil {
			return durableRead{}, err
		}
		loaded.eventResultJSON, err = rowString(results[0].Rows[0], "result_json")
		if err != nil {
			return durableRead{}, err
		}
	}
	if len(results[1].Rows) == 1 {
		loaded.period = true
		loaded.policySHA256, err = rowString(results[1].Rows[0], "policy_sha256")
		if err != nil {
			return durableRead{}, err
		}
	}
	if len(results[2].Rows) == 1 {
		loaded.projection = true
		loaded.current, err = projectionFromRow(results[2].Rows[0], policy)
		if err != nil {
			return durableRead{}, err
		}
	}
	for _, row := range results[3].Rows {
		instanceID, stringErr := rowString(row, "instance_id")
		if stringErr != nil {
			return durableRead{}, stringErr
		}
		epoch, stringErr := rowString(row, "meter_epoch")
		if stringErr != nil {
			return durableRead{}, stringErr
		}
		identity, stringErr := rowString(row, "xray_identity")
		if stringErr != nil {
			return durableRead{}, stringErr
		}
		generation, parseErr := rowUint(row, "counter_generation")
		if parseErr != nil {
			return durableRead{}, parseErr
		}
		sequence, parseErr := rowUint(row, "sample_sequence")
		if parseErr != nil {
			return durableRead{}, parseErr
		}
		up, parseErr := rowUint(row, "uplink_bytes")
		if parseErr != nil {
			return durableRead{}, parseErr
		}
		down, parseErr := rowUint(row, "downlink_bytes")
		if parseErr != nil {
			return durableRead{}, parseErr
		}
		key := meterKey{instanceID, epoch, identity}
		if _, duplicate := loaded.checkpoints[key]; duplicate || generation == 0 || sequence == 0 {
			return durableRead{}, ErrDurableStateInvalid
		}
		loaded.checkpoints[key] = counter{up: up, down: down, ordered: true, generation: generation, sequence: sequence}
	}
	return loaded, nil
}

func projectionFromRow(row map[string]any, policy Policy) (Projection, error) {
	used, err := rowUint(row, "used_bytes")
	if err != nil {
		return Projection{}, err
	}
	included, err := rowUint(row, "included_bytes")
	if err != nil {
		return Projection{}, err
	}
	remaining, err := rowUint(row, "remaining_bytes")
	if err != nil || remaining > included || included != policy.IncludedBytes {
		return Projection{}, ErrDurableStateInvalid
	}
	soft, err := rowBool(row, "soft_limit_reached")
	if err != nil {
		return Projection{}, err
	}
	hard, err := rowBool(row, "hard_limit_recommended")
	if err != nil {
		return Projection{}, err
	}
	reason, err := rowString(row, "suspension_reason")
	if err != nil {
		return Projection{}, err
	}
	pending, err := rowBool(row, "reconciliation_pending")
	if err != nil {
		return Projection{}, err
	}
	diagnosticText, err := rowString(row, "reconciliation_diagnostic")
	if err != nil {
		return Projection{}, err
	}
	version, err := rowUint(row, "version")
	if err != nil || version == 0 || version > uint64(math.MaxInt64) {
		return Projection{}, ErrDurableStateInvalid
	}
	suspension := SuspensionRecommendation{}
	if hard {
		if reason != string(SuspensionHardLimit) {
			return Projection{}, ErrDurableStateInvalid
		}
		suspension = SuspensionRecommendation{Recommended: true, EntitlementID: policy.entitlementID, Reason: SuspensionHardLimit}
	} else if reason != "" {
		return Projection{}, ErrDurableStateInvalid
	}
	return Projection{
		EntitlementID: policy.entitlementID, BillingPeriodID: policy.billingPeriodID,
		UsedBytes: used, IncludedBytes: included, RemainingBytes: remaining,
		SoftLimitReached: soft, Suspension: suspension, Pending: pending,
		Diagnostic: Diagnostic(diagnosticText), Version: version,
	}, nil
}

func durableStatements(event OrderedUsageEvent, policy canonicalPolicy, policySHA256, payloadSHA256 string, loaded durableRead, next State, result DurableResult, resultJSON string) ([]rqlite.Statement, error) {
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO whitelist_metering_periods(
entitlement_id,billing_period_id,account_id,transport_id,xray_identity,unit,basis,
included_bytes,soft_limit_bytes,hard_limit_bytes,grace_bytes,price_mode,price_source,
currency,minor_units_per_unit,policy_sha256,created_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,unixepoch())
ON CONFLICT(entitlement_id,billing_period_id) DO NOTHING`,
		Args: []any{
			policy.EntitlementID, policy.BillingPeriodID, policy.AccountID, policy.TransportID,
			policy.XrayIdentity, string(policy.Unit), string(policy.Basis), uintText(policy.IncludedBytes),
			uintText(policy.SoftLimitBytes), uintText(policy.HardLimitBytes), uintText(policy.GraceBytes),
			string(policy.PriceMode), string(policy.PriceSource), policy.Currency,
			uintText(policy.MinorUnitsPerUnit), policySHA256,
		},
	}}
	if loaded.projection {
		statements = append(statements, rqlite.Statement{
			SQL: `SELECT CASE WHEN
(SELECT policy_sha256 FROM whitelist_metering_periods WHERE entitlement_id=? AND billing_period_id=?)=?
AND EXISTS(SELECT 1 FROM whitelist_metering_projections WHERE entitlement_id=? AND billing_period_id=? AND version=?)
THEN 1 ELSE abs(-9223372036854775808) END AS metering_state_guard`,
			Args: []any{policy.EntitlementID, policy.BillingPeriodID, policySHA256, policy.EntitlementID, policy.BillingPeriodID, loaded.current.Version},
		})
	} else {
		statements = append(statements, rqlite.Statement{
			SQL: `SELECT CASE WHEN
(SELECT policy_sha256 FROM whitelist_metering_periods WHERE entitlement_id=? AND billing_period_id=?)=?
AND NOT EXISTS(SELECT 1 FROM whitelist_metering_projections WHERE entitlement_id=? AND billing_period_id=?)
THEN 1 ELSE abs(-9223372036854775808) END AS metering_state_guard`,
			Args: []any{policy.EntitlementID, policy.BillingPeriodID, policySHA256, policy.EntitlementID, policy.BillingPeriodID},
		})
	}
	hasInterval := result.Decision.Interval != nil
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO whitelist_metering_events(
event_id,entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
counter_generation,sample_sequence,uplink_bytes,downlink_bytes,payload_sha256,
diagnostic,has_interval,result_json,created_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,unixepoch())`,
		Args: []any{
			event.EventID, policy.EntitlementID, policy.BillingPeriodID,
			event.InstanceID, event.MeterEpoch, event.XrayIdentity,
			uintText(event.CounterGeneration), uintText(event.SampleSequence),
			uintText(event.UplinkBytes), uintText(event.DownlinkBytes), payloadSHA256,
			string(result.Decision.Diagnostic), boolInt(hasInterval), resultJSON,
		},
	})
	key := meterKey{event.InstanceID, event.MeterEpoch, event.XrayIdentity}
	old, hadOld := loaded.checkpoints[key]
	checkpoint, hasCheckpoint := next.counters[key]
	if hasCheckpoint && (!hadOld || old != checkpoint) {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_checkpoints(
entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity,
counter_generation,sample_sequence,uplink_bytes,downlink_bytes,updated_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,unixepoch())
ON CONFLICT(entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity)
DO UPDATE SET counter_generation=excluded.counter_generation,sample_sequence=excluded.sample_sequence,
uplink_bytes=excluded.uplink_bytes,downlink_bytes=excluded.downlink_bytes,updated_at_unix=excluded.updated_at_unix`,
			Args: []any{
				policy.EntitlementID, policy.BillingPeriodID, event.InstanceID, event.MeterEpoch,
				event.XrayIdentity, uintText(checkpoint.generation), uintText(checkpoint.sequence),
				uintText(checkpoint.up), uintText(checkpoint.down),
			},
		})
	}
	if hasInterval {
		if result.Decision.Ledger == nil || result.Decision.Ledger.EventID != event.EventID {
			return nil, ErrDurableStateInvalid
		}
		interval := result.Decision.Interval
		amount := result.Decision.Ledger.CalculatedAmount
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_metering_intervals(
event_id,uplink_delta_bytes,downlink_delta_bytes,billable_bytes,
amount_numerator,amount_denominator,currency,created_at_unix)
VALUES(?,?,?,?,?,?,?,unixepoch())`,
			Args: []any{
				event.EventID, uintText(interval.UplinkBytes), uintText(interval.DownlinkBytes),
				uintText(interval.BillableBytes), amount.Numerator, uintText(amount.Denominator), amount.Currency,
			},
		})
	}
	suspensionReason := ""
	if result.Projection.Suspension.Recommended {
		suspensionReason = string(result.Projection.Suspension.Reason)
	}
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO whitelist_metering_projections(
entitlement_id,billing_period_id,used_bytes,included_bytes,remaining_bytes,
soft_limit_reached,hard_limit_recommended,suspension_reason,reconciliation_pending,
reconciliation_diagnostic,version,updated_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,?,?,unixepoch())
ON CONFLICT(entitlement_id,billing_period_id) DO UPDATE SET
used_bytes=excluded.used_bytes,included_bytes=excluded.included_bytes,
remaining_bytes=excluded.remaining_bytes,soft_limit_reached=excluded.soft_limit_reached,
hard_limit_recommended=excluded.hard_limit_recommended,suspension_reason=excluded.suspension_reason,
reconciliation_pending=excluded.reconciliation_pending,
reconciliation_diagnostic=excluded.reconciliation_diagnostic,
version=excluded.version,updated_at_unix=excluded.updated_at_unix`,
		Args: []any{
			policy.EntitlementID, policy.BillingPeriodID, uintText(result.Projection.UsedBytes),
			uintText(result.Projection.IncludedBytes), uintText(result.Projection.RemainingBytes),
			boolInt(result.Projection.SoftLimitReached), boolInt(result.Projection.Suspension.Recommended),
			suspensionReason, boolInt(result.Projection.Pending), string(result.Projection.Diagnostic),
			result.Projection.Version,
		},
	})
	return statements, nil
}

func (store *DurableStore) resolveWrite(ctx context.Context, eventID, payloadSHA256 string) (DurableResult, error) {
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT payload_sha256,result_json FROM whitelist_metering_events WHERE event_id=?`,
		Args: []any{eventID},
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) > 1 {
		return DurableResult{}, ErrDurableStateInvalid
	}
	if len(results[0].Rows) == 0 {
		return DurableResult{}, ErrDurableStateInvalid
	}
	storedPayload, err := rowString(results[0].Rows[0], "payload_sha256")
	if err != nil {
		return DurableResult{}, err
	}
	storedResult, err := rowString(results[0].Rows[0], "result_json")
	if err != nil {
		return DurableResult{}, err
	}
	return storedReplay(eventID, payloadSHA256, storedPayload, storedResult)
}

func storedReplay(eventID, payloadSHA256, storedPayload, storedResult string) (DurableResult, error) {
	if storedPayload != payloadSHA256 {
		return DurableResult{}, &EventIDConflictError{EventID: eventID}
	}
	var result DurableResult
	if err := json.Unmarshal([]byte(storedResult), &result); err != nil {
		return DurableResult{}, ErrDurableStateInvalid
	}
	return result, nil
}

func rowString(row map[string]any, key string) (string, error) {
	value, ok := row[key]
	if !ok {
		return "", ErrDurableStateInvalid
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", ErrDurableStateInvalid
	}
}

func rowUint(row map[string]any, key string) (uint64, error) {
	value, ok := row[key]
	if !ok {
		return 0, ErrDurableStateInvalid
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case []byte:
		text = string(typed)
	case json.Number:
		text = typed.String()
	case float64:
		if typed < 0 || typed != math.Trunc(typed) || typed > math.MaxInt64 {
			return 0, ErrDurableStateInvalid
		}
		text = strconv.FormatUint(uint64(typed), 10)
	case int:
		if typed < 0 {
			return 0, ErrDurableStateInvalid
		}
		text = strconv.FormatUint(uint64(typed), 10)
	case int64:
		if typed < 0 {
			return 0, ErrDurableStateInvalid
		}
		text = strconv.FormatUint(uint64(typed), 10)
	default:
		return 0, ErrDurableStateInvalid
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil || uintText(parsed) != text {
		return 0, ErrDurableStateInvalid
	}
	return parsed, nil
}

func rowBool(row map[string]any, key string) (bool, error) {
	value, err := rowUint(row, key)
	if err != nil || value > 1 {
		return false, ErrDurableStateInvalid
	}
	return value == 1, nil
}

func uintText(value uint64) string { return strconv.FormatUint(value, 10) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
