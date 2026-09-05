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
	"strings"
	"sync"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
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
	db           rqlite.RQLite
	commercialMu sync.Mutex
}

// CommercialProducerCursor binds one Xray process/reset epoch to the next
// sequence after the latest durably accepted sample for one route identity.
type CommercialProducerCursor struct {
	// Source is the route-bound identity that must be passed unchanged to ApplyCommercialOrdered.
	Source             CommercialMeterSource
	MeterEpoch         string
	NextSampleSequence uint64
}

// CommercialDebiter applies one accepted immutable commercial interval to the
// prepaid balance. Implementations must be idempotent and persist the shared
// CommercialDebitReceiptKey in idempotency_requests before returning nil.
type CommercialDebiter interface {
	DebitCommercialInterval(context.Context, whitelistmetering.CommercialDebit) error
}

// NewDurableStore constructs the shadow-only metering adapter.
func NewDurableStore(db rqlite.RQLite) (*DurableStore, error) {
	if db == nil {
		return nil, ErrInvalidInput
	}
	return &DurableStore{db: db}, nil
}

// EnsureCommercialProducerCursor binds a physical counter source to one route,
// creates or resolves its immutable meter epoch, and resumes accepted ordering.
// Callers must pass the physical source, not a previously returned cursor Source.
func (store *DurableStore) EnsureCommercialProducerCursor(
	ctx context.Context,
	physicalSource CommercialMeterSource,
	createdAtUnix int64,
) (CommercialProducerCursor, error) {
	if store == nil || store.db == nil || ctx == nil ||
		!validCommercialProducerSource(physicalSource) || createdAtUnix < 0 || createdAtUnix >= math.MaxInt64 {
		return CommercialProducerCursor{}, ErrInvalidInput
	}
	source := bindCommercialProducerSource(physicalSource)
	epochDigest := sha256.Sum256([]byte(
		"maestro-whitelist-meter-epoch-v1\x00" + source.OriginID + "\x00" +
			source.CounterSourceID + "\x00" + source.XrayProcessBootID + "\x00" +
			uintText(source.ResetSequence),
	))
	candidateEpoch := "wl-meter-" + hex.EncodeToString(epochDigest[:])
	insert := rqlite.Statement{
		SQL: `INSERT OR IGNORE INTO whitelist_meter_epochs(
meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence,created_at_unix)
VALUES(?,?,?,?,?,?)`,
		Args: []any{
			candidateEpoch, source.OriginID, source.CounterSourceID,
			source.XrayProcessBootID, source.ResetSequence, createdAtUnix,
		},
	}
	reads := commercialProducerCursorReads(source)
	results, requestErr := store.db.Request(
		ctx, rqlite.Linearizable, true, append([]rqlite.Statement{insert}, reads...)...,
	)
	if requestErr == nil {
		if len(results) != 3 {
			return CommercialProducerCursor{}, ErrDurableStateInvalid
		}
		return commercialProducerCursorFromResults(results[1:], source)
	}
	results, err := store.db.QueryLinearizable(ctx, reads...)
	if err != nil {
		return CommercialProducerCursor{}, fmt.Errorf("shadowbilling: resolve commercial producer cursor: %w", requestErr)
	}
	cursor, err := commercialProducerCursorFromResults(results, source)
	if err != nil {
		return CommercialProducerCursor{}, fmt.Errorf("shadowbilling: resolve commercial producer cursor: %w", requestErr)
	}
	return cursor, nil
}

// PendingCommercialDebitEntitlementIDs enumerates committed intervals whose
// exact balance receipt is still absent, including users no longer desired.
func (store *DurableStore) PendingCommercialDebitEntitlementIDs(ctx context.Context) ([]string, error) {
	if store == nil || store.db == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT DISTINCT outbox.entitlement_id AS entitlement_id
FROM whitelist_commercial_debit_outbox AS outbox
LEFT JOIN idempotency_requests AS receipt
  ON receipt.scope=?
 AND receipt.command_type=?
 AND receipt.idempotency_key=outbox.receipt_key
 AND receipt.request_hash=outbox.request_hash
 AND receipt.resource_id=outbox.entitlement_id
 AND receipt.status='applied'
WHERE receipt.idempotency_key IS NULL
ORDER BY outbox.entitlement_id`,
		Args: []any{
			whitelistmetering.CommercialDebitReceiptScope,
			whitelistmetering.CommercialDebitReceiptCommand,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("shadowbilling: read pending commercial debit entitlements: %w", err)
	}
	if len(results) != 1 {
		return nil, ErrDurableStateInvalid
	}
	entitlementIDs := make([]string, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		entitlementID, parseErr := rowString(row, "entitlement_id")
		if parseErr != nil || !exactCommercialIdentifier(entitlementID) ||
			!strings.HasPrefix(entitlementID, "wl-ent-") {
			return nil, ErrDurableStateInvalid
		}
		entitlementIDs = append(entitlementIDs, entitlementID)
	}
	return entitlementIDs, nil
}

func commercialProducerCursorReads(source CommercialMeterSource) []rqlite.Statement {
	args := []any{
		source.OriginID, source.CounterSourceID, source.XrayProcessBootID, source.ResetSequence,
	}
	return []rqlite.Statement{
		{
			SQL: `SELECT meter_epoch,origin_id,counter_source_id,xray_process_boot_id,reset_sequence
FROM whitelist_meter_epochs
WHERE origin_id=? AND counter_source_id=? AND xray_process_boot_id=? AND reset_sequence=?`,
			Args: args,
		},
		{
			SQL: `SELECT source.sample_sequence AS sample_sequence
FROM whitelist_commercial_metering_sources AS source
JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=source.meter_epoch
WHERE epoch.origin_id=? AND epoch.counter_source_id=? AND epoch.xray_process_boot_id=?
  AND epoch.reset_sequence=? AND source.route_xray_identity=? AND source.counter_generation='1'
ORDER BY length(source.sample_sequence) DESC,source.sample_sequence DESC
LIMIT 1`,
			Args: append(append([]any(nil), args...), source.RouteXrayIdentity),
		},
	}
}

func commercialProducerCursorFromResults(
	results []rqlite.Result,
	source CommercialMeterSource,
) (CommercialProducerCursor, error) {
	if len(results) != 2 || len(results[0].Rows) != 1 || len(results[1].Rows) > 1 {
		return CommercialProducerCursor{}, ErrDurableStateInvalid
	}
	row := results[0].Rows[0]
	meterEpoch, meterEpochErr := rowString(row, "meter_epoch")
	originID, originErr := rowString(row, "origin_id")
	counterSourceID, counterSourceErr := rowString(row, "counter_source_id")
	processBootID, processBootErr := rowString(row, "xray_process_boot_id")
	resetSequence, resetErr := rowUint(row, "reset_sequence")
	if meterEpochErr != nil || originErr != nil || counterSourceErr != nil || processBootErr != nil ||
		resetErr != nil || !exactCommercialIdentifier(meterEpoch) || originID != source.OriginID ||
		counterSourceID != source.CounterSourceID || processBootID != source.XrayProcessBootID ||
		resetSequence != source.ResetSequence {
		return CommercialProducerCursor{}, ErrDurableStateInvalid
	}
	next := uint64(1)
	if len(results[1].Rows) == 1 {
		accepted, parseErr := rowUint(results[1].Rows[0], "sample_sequence")
		if parseErr != nil || accepted == math.MaxUint64 {
			return CommercialProducerCursor{}, ErrDurableStateInvalid
		}
		next = accepted + 1
	}
	return CommercialProducerCursor{Source: source, MeterEpoch: meterEpoch, NextSampleSequence: next}, nil
}

func bindCommercialProducerSource(source CommercialMeterSource) CommercialMeterSource {
	digest := sha256.Sum256([]byte(
		"maestro-whitelist-counter-source-v1\x00" + source.CounterSourceID + "\x00" + source.RouteXrayIdentity,
	))
	source.CounterSourceID = "wl-counter-" + hex.EncodeToString(digest[:])
	return source
}

func validCommercialProducerSource(source CommercialMeterSource) bool {
	return exactCommercialIdentifier(source.OriginID) &&
		exactCommercialIdentifier(source.ExitID) &&
		exactCommercialIdentifier(source.CounterSourceID) &&
		!strings.HasPrefix(source.CounterSourceID, "wl-counter-") &&
		exactCommercialIdentifier(source.XrayProcessBootID) &&
		exactCommercialIdentifier(source.RouteXrayIdentity) &&
		source.ResetSequence < uint64(math.MaxInt64) &&
		strings.Count(source.RouteXrayIdentity, ":") == 2 &&
		strings.HasPrefix(source.RouteXrayIdentity, "wl:wl-ent-") &&
		strings.HasSuffix(source.RouteXrayIdentity, ":"+source.ExitID)
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
	result, err := store.applyOrdered(ctx, event, policy, nil, false)
	if err != nil {
		return DurableResult{}, err
	}
	return result, nil
}

// ApplyCommercialOrdered atomically persists the v10 event and its v12 source
// binding, then applies accepted intervals through the durable balance receipt.
// Previously committed pending intervals are drained before a newer sample can
// advance the counter.
func (store *DurableStore) ApplyCommercialOrdered(
	ctx context.Context,
	event CommercialOrderedUsageEvent,
	policy Policy,
	debiter CommercialDebiter,
) (DurableResult, error) {
	return store.applyCommercialOrdered(ctx, event, policy, debiter, false)
}

func (store *DurableStore) applyCommercialOrdered(
	ctx context.Context,
	event CommercialOrderedUsageEvent,
	policy Policy,
	debiter CommercialDebiter,
	firstCumulative bool,
) (DurableResult, error) {
	return store.applyCommercialFinalOrdered(ctx, event, policy, debiter, firstCumulative, nil)
}

// A final source proof can only enter through the authenticated final-receipt
// adapter. Its source/sequence binding is committed with the metering event,
// never reserved independently of the transaction which accepts that sequence.
func (store *DurableStore) applyCommercialFinalOrdered(
	ctx context.Context,
	event CommercialOrderedUsageEvent,
	policy Policy,
	debiter CommercialDebiter,
	firstCumulative bool,
	finalProof *commercialFinalProof,
) (DurableResult, error) {
	if store == nil || store.db == nil || ctx == nil || debiter == nil {
		return DurableResult{}, ErrInvalidInput
	}
	binding, err := BindCommercialMeteringSource(event, policy)
	if err != nil {
		return DurableResult{}, err
	}
	store.commercialMu.Lock()
	defer store.commercialMu.Unlock()
	if err := store.verifyCommercialEpoch(ctx, binding); err != nil {
		return DurableResult{}, err
	}
	if err := store.drainCommercialDebitsLocked(
		ctx, binding.EntitlementID, debiter,
	); err != nil {
		return DurableResult{}, err
	}
	result, err := store.applyOrderedWithFinal(ctx, event.OrderedUsageEvent, policy, &binding, firstCumulative, finalProof)
	if err != nil {
		if !errors.Is(err, ErrEventIDConflict) {
			lateDiagnostic := result.Decision.Diagnostic == DiagnosticLateSample
			conflict, resolveErr := store.resolveCommercialSourceConflict(ctx, binding, lateDiagnostic, finalProof)
			if resolveErr == nil && conflict {
				return DurableResult{}, &EventIDConflictError{EventID: binding.EventID}
			}
		}
		return DurableResult{}, err
	}
	if err := store.verifyCommercialSource(ctx, binding); err != nil {
		return DurableResult{}, err
	}
	if finalProof != nil {
		if err := verifyCommercialFinalAcceptance(ctx, store.db, binding, finalProof); err != nil {
			return DurableResult{}, err
		}
	}
	if err := store.drainCommercialDebitsLocked(ctx, binding.EntitlementID, debiter); err != nil {
		return DurableResult{}, fmt.Errorf("shadowbilling: debit commercial interval: %w", err)
	}
	return result, nil
}

// DrainCommercialDebits applies every committed interval that lacks the
// durable balance receipt. It is safe to call at startup and before every new
// sample; callbacks are at-least-once while balance mutation is exactly-once.
func (store *DurableStore) DrainCommercialDebits(
	ctx context.Context,
	entitlementID string,
	debiter CommercialDebiter,
) error {
	if store == nil || store.db == nil || ctx == nil || debiter == nil ||
		!exactCommercialIdentifier(entitlementID) {
		return ErrInvalidInput
	}
	store.commercialMu.Lock()
	defer store.commercialMu.Unlock()
	return store.drainCommercialDebitsLocked(ctx, entitlementID, debiter)
}

func (store *DurableStore) drainCommercialDebitsLocked(
	ctx context.Context,
	entitlementID string,
	debiter CommercialDebiter,
) error {
	pending, err := store.pendingCommercialDebits(ctx, entitlementID)
	if err != nil {
		return err
	}
	for _, debit := range pending {
		if err := store.ensureCommercialDebit(ctx, debit, debiter); err != nil {
			return fmt.Errorf("shadowbilling: drain commercial debit: %w", err)
		}
	}
	return nil
}

func (store *DurableStore) ensureCommercialDebit(
	ctx context.Context,
	debit whitelistmetering.CommercialDebit,
	debiter CommercialDebiter,
) error {
	applied, err := store.commercialDebitReceiptApplied(ctx, debit)
	if err != nil || applied {
		return err
	}
	if err := debiter.DebitCommercialInterval(ctx, debit); err != nil {
		return err
	}
	applied, err = store.commercialDebitReceiptApplied(ctx, debit)
	if err != nil {
		return err
	}
	if !applied {
		return ErrDurableStateInvalid
	}
	return nil
}

func (store *DurableStore) applyOrdered(
	ctx context.Context,
	event OrderedUsageEvent,
	policy Policy,
	commercialSource *CommercialSourceBinding,
	firstCumulative bool,
) (DurableResult, error) {
	return store.applyOrderedWithFinal(ctx, event, policy, commercialSource, firstCumulative, nil)
}

func (store *DurableStore) applyOrderedWithFinal(
	ctx context.Context,
	event OrderedUsageEvent,
	policy Policy,
	commercialSource *CommercialSourceBinding,
	firstCumulative bool,
	finalProof *commercialFinalProof,
) (DurableResult, error) {
	if store == nil || store.db == nil || ctx == nil || ((firstCumulative || finalProof != nil) && commercialSource == nil) {
		return DurableResult{}, ErrInvalidInput
	}
	var finalStatements []rqlite.Statement
	if finalProof != nil {
		if finalProof.event.OrderedUsageEvent != event || finalProof.firstCumulative != firstCumulative {
			return DurableResult{}, ErrInvalidInput
		}
		var err error
		finalStatements, err = commercialFinalAcceptanceStatements(*commercialSource, finalProof)
		if err != nil || len(finalStatements) == 0 {
			return DurableResult{}, ErrInvalidInput
		}
	}
	applySample := ApplyOrdered
	if firstCumulative {
		applySample = applyFirstCumulative
	}
	if _, _, err := applySample(NewState(), event, policy); err != nil {
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
	eventVersion := 1
	if firstCumulative {
		// Preserve generic v1 hashes; a baseline cannot replay as a full debit.
		eventVersion = 2
	}
	payloadSHA256, err := canonicalSHA256(canonicalEvent{
		Version: eventVersion, EventID: event.EventID, InstanceID: event.InstanceID,
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

	next, decision, err := applySample(state, event, policy)
	if err != nil {
		return DurableResult{}, err
	}
	if finalProof != nil && decision.Diagnostic != "" {
		// A baseline or a late-sample diagnostic is not settlement of a final
		// counter pair. Keep its proof pending instead of ACKing discarded bytes.
		return DurableResult{}, ErrInvalidInput
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

	statements, err := durableStatements(
		event, canonical, policySHA256, payloadSHA256, loaded, next, result,
		string(resultJSON), commercialSource, finalProof,
	)
	if err != nil {
		return DurableResult{}, err
	}
	if firstCumulative {
		statements = append([]rqlite.Statement{commercialFirstCumulativeGuard(*commercialSource)}, statements...)
	}
	statements = append(finalStatements, statements...)
	if _, err := store.db.Request(ctx, rqlite.Linearizable, true, statements...); err != nil {
		if replay, resolvedErr := store.resolveWrite(ctx, event.EventID, payloadSHA256); resolvedErr == nil {
			return replay, nil
		} else if errors.Is(resolvedErr, ErrEventIDConflict) {
			return DurableResult{}, resolvedErr
		}
		return result, fmt.Errorf("shadowbilling: persist durable metering transaction: %w", err)
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

func durableStatements(
	event OrderedUsageEvent,
	policy canonicalPolicy,
	policySHA256, payloadSHA256 string,
	loaded durableRead,
	next State,
	result DurableResult,
	resultJSON string,
	commercialSource *CommercialSourceBinding,
	finalProof *commercialFinalProof,
) ([]rqlite.Statement, error) {
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
	lateDiagnostic := result.Decision.Diagnostic == DiagnosticLateSample
	var commercialDebit *whitelistmetering.CommercialDebit
	commercialReceiptKey := ""
	commercialRequestHash := ""
	if commercialSource != nil && hasInterval {
		debit := commercialDebitFromBinding(*commercialSource)
		var err error
		commercialReceiptKey, err = whitelistmetering.CommercialDebitReceiptKey(
			debit.MeterEpoch, debit.IntervalID,
		)
		if err != nil {
			return nil, ErrDurableStateInvalid
		}
		commercialRequestHash, err = whitelistmetering.CommercialDebitReceiptHash(debit)
		if err != nil {
			return nil, ErrDurableStateInvalid
		}
		commercialDebit = &debit
	}
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
	if commercialSource != nil {
		source := *commercialSource
		// applyOrderedWithFinal prepends the authenticated proof/acceptance
		// guards in this transaction. Only that path may accept an older
		// timestamp after another Origin in the same period; same-Origin
		// ordering, pending debits and the current paid-period CAS stay intact.
		statements = append(statements,
			rqlite.Statement{
				SQL: `SELECT CASE WHEN
NOT EXISTS(
    SELECT 1 FROM whitelist_commercial_metering_sources
    WHERE meter_epoch=? AND (route_xray_identity<>? OR exit_id<>?)
)
AND (
    (
        ?=1
        AND EXISTS(
            SELECT 1 FROM whitelist_commercial_metering_sources
            WHERE entitlement_id=? AND sampled_at_unix>=?
        )
    )
    OR (
        ?=0
        AND
        NOT EXISTS(
            SELECT 1 FROM whitelist_commercial_metering_sources
            WHERE entitlement_id=? AND sampled_at_unix>?
              AND (?=0 OR origin_id=? OR billing_period_id<>?)
        )
        AND NOT EXISTS(
            SELECT 1
            FROM whitelist_commercial_debit_outbox AS pending
            WHERE pending.entitlement_id=?
              AND NOT EXISTS(
                SELECT 1 FROM idempotency_requests AS receipt
                WHERE receipt.scope=? AND receipt.command_type=?
                  AND receipt.idempotency_key=pending.receipt_key
                  AND receipt.request_hash=pending.request_hash
                  AND receipt.resource_id=pending.entitlement_id
                  AND receipt.status='applied'
            )
        )
        AND NOT EXISTS(
            SELECT 1 FROM whitelist_balance_projections AS balance
            WHERE balance.entitlement_id=?
              AND COALESCE(balance.current_period_id,'')<>?
        )
    )
)
THEN 1 ELSE abs(-9223372036854775808) END AS commercial_source_guard`,
				Args: []any{
					source.MeterEpoch, source.RouteXrayIdentity, source.ExitID,
					boolInt(lateDiagnostic), source.EntitlementID, source.SampledAtUnix,
					boolInt(lateDiagnostic),
					source.EntitlementID, source.SampledAtUnix,
					boolInt(finalProof != nil), source.OriginID, source.BillingPeriodID,
					source.EntitlementID,
					whitelistmetering.CommercialDebitReceiptScope,
					whitelistmetering.CommercialDebitReceiptCommand,
					source.EntitlementID, source.BillingPeriodID,
				},
			},
			rqlite.Statement{
				SQL: `INSERT INTO whitelist_commercial_metering_sources(
event_id,entitlement_id,billing_period_id,origin_id,exit_id,meter_epoch,
route_xray_identity,counter_generation,sample_sequence,basis,sampled_at_unix,source_sha256)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
				Args: []any{
					source.EventID, source.EntitlementID, source.BillingPeriodID,
					source.OriginID, source.ExitID, source.MeterEpoch, source.RouteXrayIdentity,
					uintText(source.CounterGeneration), uintText(source.SampleSequence),
					string(source.Basis), source.SampledAtUnix, source.SourceSHA256,
				},
			},
		)
	}
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
	if commercialDebit != nil {
		debit := *commercialDebit
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_commercial_debit_outbox(
event_id,entitlement_id,billing_period_id,meter_epoch,basis,interval_end_unix,
source_sha256,receipt_key,request_hash,created_at_unix)
VALUES(?,?,?,?,?,?,?,?,?,unixepoch())`,
			Args: []any{
				debit.IntervalID, debit.EntitlementID, debit.BillingPeriodID,
				debit.MeterEpoch, debit.Basis, debit.IntervalEndUnix,
				debit.SourceSHA256, commercialReceiptKey, commercialRequestHash,
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

const commercialSourceSelectSQL = `SELECT
source.event_id AS event_id,
policy.account_id AS account_id,
source.entitlement_id AS entitlement_id,
policy.transport_id AS transport_id,
source.billing_period_id AS billing_period_id,
source.origin_id AS origin_id,
source.exit_id AS exit_id,
epoch.counter_source_id AS counter_source_id,
epoch.xray_process_boot_id AS xray_process_boot_id,
epoch.reset_sequence AS reset_sequence,
source.meter_epoch AS meter_epoch,
event.xray_identity AS base_xray_identity,
source.route_xray_identity AS route_xray_identity,
source.basis AS basis,
source.counter_generation AS counter_generation,
source.sample_sequence AS sample_sequence,
event.uplink_bytes AS uplink_bytes,
event.downlink_bytes AS downlink_bytes,
source.sampled_at_unix AS sampled_at_unix,
source.source_sha256 AS source_sha256,
policy.basis AS policy_basis,
policy.included_bytes AS policy_included_bytes,
outbox.receipt_key AS debit_receipt_key,
outbox.request_hash AS debit_request_hash
FROM whitelist_commercial_metering_sources AS source
JOIN whitelist_metering_events AS event ON event.event_id=source.event_id
JOIN whitelist_metering_periods AS policy
  ON policy.entitlement_id=source.entitlement_id
 AND policy.billing_period_id=source.billing_period_id
JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=source.meter_epoch
LEFT JOIN whitelist_metering_intervals AS interval ON interval.event_id=source.event_id
LEFT JOIN whitelist_commercial_debit_outbox AS outbox ON outbox.event_id=source.event_id`

func (store *DurableStore) verifyCommercialEpoch(
	ctx context.Context,
	want CommercialSourceBinding,
) error {
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT origin_id,counter_source_id,xray_process_boot_id,reset_sequence
FROM whitelist_meter_epochs WHERE meter_epoch=?`,
		Args: []any{want.MeterEpoch},
	})
	if err != nil {
		return fmt.Errorf("shadowbilling: verify commercial meter epoch: %w", err)
	}
	if len(results) != 1 || len(results[0].Rows) != 1 {
		return ErrDurableStateInvalid
	}
	row := results[0].Rows[0]
	originID, err := rowString(row, "origin_id")
	if err != nil {
		return err
	}
	counterSourceID, err := rowString(row, "counter_source_id")
	if err != nil {
		return err
	}
	processBootID, err := rowString(row, "xray_process_boot_id")
	if err != nil {
		return err
	}
	resetSequence, err := rowUint(row, "reset_sequence")
	if err != nil {
		return err
	}
	if originID != want.OriginID || counterSourceID != want.CounterSourceID ||
		processBootID != want.XrayProcessBootID || resetSequence != want.ResetSequence {
		return &EventIDConflictError{EventID: want.EventID}
	}
	return nil
}

func (store *DurableStore) verifyCommercialSource(
	ctx context.Context,
	want CommercialSourceBinding,
) error {
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  commercialSourceSelectSQL + ` WHERE source.event_id=?`,
		Args: []any{want.EventID},
	})
	if err != nil {
		return fmt.Errorf("shadowbilling: verify commercial source: %w", err)
	}
	bindings, err := commercialBindingsFromResults(results)
	if err != nil || len(bindings) != 1 {
		return ErrDurableStateInvalid
	}
	if bindings[0] != want {
		return &EventIDConflictError{EventID: want.EventID}
	}
	return nil
}

func (store *DurableStore) pendingCommercialDebits(
	ctx context.Context,
	entitlementID string,
) ([]whitelistmetering.CommercialDebit, error) {
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: commercialSourceSelectSQL + `
LEFT JOIN idempotency_requests AS receipt
  ON receipt.scope=?
 AND receipt.command_type=?
 AND receipt.idempotency_key=outbox.receipt_key
 AND receipt.request_hash=outbox.request_hash
 AND receipt.resource_id=outbox.entitlement_id
 AND receipt.status='applied'
WHERE outbox.event_id IS NOT NULL
  AND source.entitlement_id=?
  AND receipt.idempotency_key IS NULL
ORDER BY source.sampled_at_unix,source.event_id`,
		Args: []any{
			whitelistmetering.CommercialDebitReceiptScope,
			whitelistmetering.CommercialDebitReceiptCommand,
			entitlementID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("shadowbilling: read pending commercial debits: %w", err)
	}
	if len(results) != 1 {
		return nil, ErrDurableStateInvalid
	}
	pending := make([]whitelistmetering.CommercialDebit, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		binding, parseErr := commercialBindingFromRow(row)
		if parseErr != nil {
			return nil, parseErr
		}
		debit := commercialDebitFromBinding(binding)
		key, keyErr := whitelistmetering.CommercialDebitReceiptKey(
			debit.MeterEpoch, debit.IntervalID,
		)
		hash, hashErr := whitelistmetering.CommercialDebitReceiptHash(debit)
		storedKey, storedKeyErr := rowString(row, "debit_receipt_key")
		storedHash, storedHashErr := rowString(row, "debit_request_hash")
		if keyErr != nil || hashErr != nil || storedKeyErr != nil || storedHashErr != nil ||
			storedKey != key || storedHash != hash {
			return nil, ErrDurableStateInvalid
		}
		pending = append(pending, debit)
	}
	return pending, nil
}

func commercialBindingsFromResults(results []rqlite.Result) ([]CommercialSourceBinding, error) {
	if len(results) != 1 {
		return nil, ErrDurableStateInvalid
	}
	bindings := make([]CommercialSourceBinding, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		binding, err := commercialBindingFromRow(row)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func commercialBindingFromRow(row map[string]any) (CommercialSourceBinding, error) {
	stringsByKey := make(map[string]string, 15)
	for _, key := range []string{
		"event_id", "account_id", "entitlement_id", "transport_id", "billing_period_id",
		"origin_id", "exit_id", "counter_source_id", "xray_process_boot_id", "meter_epoch",
		"base_xray_identity", "route_xray_identity", "basis", "source_sha256", "policy_basis",
	} {
		value, err := rowString(row, key)
		if err != nil {
			return CommercialSourceBinding{}, err
		}
		stringsByKey[key] = value
	}
	resetSequence, err := rowUint(row, "reset_sequence")
	if err != nil {
		return CommercialSourceBinding{}, err
	}
	generation, err := rowUint(row, "counter_generation")
	if err != nil || generation != 1 {
		return CommercialSourceBinding{}, ErrDurableStateInvalid
	}
	sequence, err := rowUint(row, "sample_sequence")
	if err != nil {
		return CommercialSourceBinding{}, err
	}
	uplink, err := rowUint(row, "uplink_bytes")
	if err != nil {
		return CommercialSourceBinding{}, err
	}
	downlink, err := rowUint(row, "downlink_bytes")
	if err != nil {
		return CommercialSourceBinding{}, err
	}
	sampledAt, err := rowUint(row, "sampled_at_unix")
	if err != nil || sampledAt > math.MaxInt64 {
		return CommercialSourceBinding{}, ErrDurableStateInvalid
	}
	policyIncluded, err := rowUint(row, "policy_included_bytes")
	if err != nil || policyIncluded != 0 || stringsByKey["policy_basis"] != stringsByKey["basis"] ||
		stringsByKey["policy_basis"] != string(BasisUplinkPlusDownlink) {
		return CommercialSourceBinding{}, ErrDurableStateInvalid
	}
	binding := CommercialSourceBinding{
		EventID: stringsByKey["event_id"], AccountID: stringsByKey["account_id"],
		EntitlementID: stringsByKey["entitlement_id"], TransportID: stringsByKey["transport_id"],
		BillingPeriodID: stringsByKey["billing_period_id"], OriginID: stringsByKey["origin_id"],
		ExitID: stringsByKey["exit_id"], CounterSourceID: stringsByKey["counter_source_id"],
		XrayProcessBootID: stringsByKey["xray_process_boot_id"], ResetSequence: resetSequence,
		MeterEpoch: stringsByKey["meter_epoch"], BaseXrayIdentity: stringsByKey["base_xray_identity"],
		RouteXrayIdentity: stringsByKey["route_xray_identity"], Basis: TrafficBasis(stringsByKey["basis"]),
		CounterGeneration: generation, SampleSequence: sequence, UplinkBytes: uplink,
		DownlinkBytes: downlink, SampledAtUnix: int64(sampledAt),
		SourceSHA256: stringsByKey["source_sha256"],
	}
	if !exactCommercialIdentifier(binding.EventID) {
		return CommercialSourceBinding{}, ErrDurableStateInvalid
	}
	recomputed, err := whitelistmetering.SourceSHA256(whitelistmetering.SourceDigestInput{
		AccountID: binding.AccountID, EntitlementID: binding.EntitlementID,
		TransportID: binding.TransportID, BillingPeriodID: binding.BillingPeriodID,
		Basis: string(binding.Basis), BaseXrayIdentity: binding.BaseXrayIdentity,
		RouteXrayIdentity: binding.RouteXrayIdentity, OriginID: binding.OriginID,
		ExitID: binding.ExitID, CounterSourceID: binding.CounterSourceID,
		XrayProcessBootID: binding.XrayProcessBootID, ResetSequence: binding.ResetSequence,
		MeterEpoch: binding.MeterEpoch, CounterGeneration: binding.CounterGeneration,
		SampleSequence: binding.SampleSequence, UplinkBytes: binding.UplinkBytes,
		DownlinkBytes: binding.DownlinkBytes, SampledAtUnix: binding.SampledAtUnix,
	})
	if err != nil || recomputed != binding.SourceSHA256 {
		return CommercialSourceBinding{}, ErrDurableStateInvalid
	}
	return binding, nil
}

func commercialDebitFromBinding(binding CommercialSourceBinding) whitelistmetering.CommercialDebit {
	return whitelistmetering.CommercialDebit{
		EntitlementID: binding.EntitlementID, BillingPeriodID: binding.BillingPeriodID,
		MeterEpoch: binding.MeterEpoch, IntervalID: binding.EventID,
		Basis: string(binding.Basis), IntervalEndUnix: binding.SampledAtUnix,
		SourceSHA256: binding.SourceSHA256,
	}
}

func (store *DurableStore) commercialDebitReceiptApplied(
	ctx context.Context,
	debit whitelistmetering.CommercialDebit,
) (bool, error) {
	key, err := whitelistmetering.CommercialDebitReceiptKey(debit.MeterEpoch, debit.IntervalID)
	if err != nil {
		return false, ErrDurableStateInvalid
	}
	requestHash, err := whitelistmetering.CommercialDebitReceiptHash(debit)
	if err != nil {
		return false, ErrDurableStateInvalid
	}
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT request_hash,resource_id,status FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`,
		Args: []any{
			whitelistmetering.CommercialDebitReceiptScope,
			whitelistmetering.CommercialDebitReceiptCommand,
			key,
		},
	})
	if err != nil {
		return false, fmt.Errorf("shadowbilling: verify commercial debit receipt: %w", err)
	}
	if len(results) != 1 || len(results[0].Rows) > 1 {
		return false, ErrDurableStateInvalid
	}
	if len(results[0].Rows) == 0 {
		return false, nil
	}
	storedHash, err := rowString(results[0].Rows[0], "request_hash")
	if err != nil {
		return false, err
	}
	resourceID, err := rowString(results[0].Rows[0], "resource_id")
	if err != nil {
		return false, err
	}
	status, err := rowString(results[0].Rows[0], "status")
	if err != nil || storedHash != requestHash || resourceID != debit.EntitlementID ||
		(status != "applying" && status != "applied") {
		return false, ErrDurableStateInvalid
	}
	return status == "applied", nil
}

func (store *DurableStore) resolveCommercialSourceConflict(
	ctx context.Context,
	want CommercialSourceBinding,
	lateDiagnostic bool,
	finalProof *commercialFinalProof,
) (bool, error) {
	results, err := store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT
EXISTS(
    SELECT 1 FROM whitelist_commercial_metering_sources
    WHERE event_id<>? AND (
        source_sha256=? OR (
            meter_epoch=? AND route_xray_identity=?
            AND counter_generation=? AND sample_sequence=?
        )
    )
) AS physical_sample_conflict,
EXISTS(
    SELECT 1 FROM whitelist_commercial_metering_sources
    WHERE entitlement_id=? AND sampled_at_unix>?
      AND (?=0 OR origin_id=? OR billing_period_id<>?)
) AS sampled_at_conflict,
NOT EXISTS(
    SELECT 1 FROM whitelist_commercial_metering_sources
    WHERE entitlement_id=? AND sampled_at_unix>=?
) AS late_future_timestamp_conflict,
EXISTS(
    SELECT 1 FROM whitelist_commercial_metering_sources
    WHERE meter_epoch=? AND (route_xray_identity<>? OR exit_id<>?)
) AS epoch_route_conflict,
EXISTS(
    SELECT 1 FROM whitelist_balance_projections
    WHERE entitlement_id=? AND COALESCE(current_period_id,'')<>?
) AS balance_period_conflict`,
		Args: []any{
			want.EventID, want.SourceSHA256, want.MeterEpoch, want.RouteXrayIdentity,
			uintText(want.CounterGeneration), uintText(want.SampleSequence),
			want.EntitlementID, want.SampledAtUnix,
			boolInt(finalProof != nil), want.OriginID, want.BillingPeriodID,
			want.EntitlementID, want.SampledAtUnix,
			want.MeterEpoch, want.RouteXrayIdentity, want.ExitID,
			want.EntitlementID, want.BillingPeriodID,
		},
	})
	if err != nil {
		return false, fmt.Errorf("shadowbilling: resolve commercial source conflict: %w", err)
	}
	if len(results) != 1 || len(results[0].Rows) != 1 {
		return false, ErrDurableStateInvalid
	}
	row := results[0].Rows[0]
	physical, err := rowUint(row, "physical_sample_conflict")
	if err != nil || physical > 1 {
		return false, ErrDurableStateInvalid
	}
	sampledAt, err := rowUint(row, "sampled_at_conflict")
	if err != nil || sampledAt > 1 {
		return false, ErrDurableStateInvalid
	}
	lateFutureTimestamp, err := rowUint(row, "late_future_timestamp_conflict")
	if err != nil || lateFutureTimestamp > 1 {
		return false, ErrDurableStateInvalid
	}
	epochRoute, err := rowUint(row, "epoch_route_conflict")
	if err != nil || epochRoute > 1 {
		return false, ErrDurableStateInvalid
	}
	balancePeriod, err := rowUint(row, "balance_period_conflict")
	if err != nil || balancePeriod > 1 {
		return false, ErrDurableStateInvalid
	}
	return physical == 1 || epochRoute == 1 ||
		(lateDiagnostic && lateFutureTimestamp == 1) ||
		(!lateDiagnostic && (sampledAt == 1 || balancePeriod == 1)), nil
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
