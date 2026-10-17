// Package shadowbilling contains pure, shadow-only CDN metering rules. It has
// no balance, wallet, API, or ordinary-VPN model, so it cannot charge or alter
// ordinary access.
package shadowbilling

import (
	"errors"
	"math/big"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

var (
	ErrMissingPaidPrice        = errors.New("shadowbilling: paid price is required")
	ErrInvalidInput            = errors.New("shadowbilling: invalid input")
	ErrIdentityMismatch        = errors.New("shadowbilling: Xray identity mismatch")
	ErrEventIDConflict         = errors.New("shadowbilling: EventID conflicts with the recorded payload or context")
	ErrResetGenerationRequired = errors.New("shadowbilling: counter reset generation is required")
	ErrOrderingModeMismatch    = errors.New("shadowbilling: counter ordering mode mismatch")
)

type EventIDConflictError struct {
	EventID string
}

func (err *EventIDConflictError) Error() string { return ErrEventIDConflict.Error() }
func (err *EventIDConflictError) Unwrap() error { return ErrEventIDConflict }

type TrafficUnit string

const (
	UnitGBDecimal TrafficUnit = "GB_DECIMAL"
	UnitGiBBinary TrafficUnit = "GIB_BINARY"
)

func (u TrafficUnit) bytes() uint64 {
	if u == UnitGBDecimal {
		return 1000000000
	}
	if u == UnitGiBBinary {
		return 1073741824
	}
	return 0
}

type TrafficBasis string

const (
	BasisDownlinkOnly       TrafficBasis = "DOWNLINK_ONLY"
	BasisUplinkPlusDownlink TrafficBasis = "UPLINK_PLUS_DOWNLINK"
	BasisFree               TrafficBasis = "FREE"
)

type PriceMode string

const (
	PricePaid PriceMode = "PAID"
	PriceFree PriceMode = "FREE"
)

type PriceSource string

const (
	PriceIndividual PriceSource = "INDIVIDUAL"
	PriceTariff     PriceSource = "TARIFF"
	PriceProfile    PriceSource = "PROFILE"
	PriceGlobal     PriceSource = "GLOBAL"
)

type Price struct {
	Mode              PriceMode
	Currency          string
	MinorUnitsPerUnit uint64
}
type PriceOptions struct{ Individual, Tariff, Profile, Global *Price }
type ResolvedPrice struct {
	Source PriceSource
	Price  Price
}

func ResolvePrice(options PriceOptions) (ResolvedPrice, error) {
	for _, candidate := range []struct {
		source PriceSource
		price  *Price
	}{{PriceIndividual, options.Individual}, {PriceTariff, options.Tariff}, {PriceProfile, options.Profile}, {PriceGlobal, options.Global}} {
		if candidate.price == nil {
			continue
		}
		p := *candidate.price
		if p.Mode == PriceFree {
			return ResolvedPrice{candidate.source, p}, nil
		}
		if p.Mode != PricePaid || strings.TrimSpace(p.Currency) == "" || p.MinorUnitsPerUnit == 0 {
			return ResolvedPrice{}, ErrInvalidInput
		}
		return ResolvedPrice{candidate.source, p}, nil
	}
	return ResolvedPrice{}, ErrMissingPaidPrice
}

type PolicySpec struct {
	BillingPeriodID                                           string
	Unit                                                      TrafficUnit
	Basis                                                     TrafficBasis
	IncludedBytes, SoftLimitBytes, HardLimitBytes, GraceBytes uint64
	Prices                                                    PriceOptions
}

type Policy struct {
	accountID, entitlementID, transportID, billingPeriodID, expectedXrayIdentity string
	Unit                                                                         TrafficUnit
	Basis                                                                        TrafficBasis
	IncludedBytes, SoftLimitBytes, HardLimitBytes, GraceBytes                    uint64
	Prices                                                                       PriceOptions
}

// NewPolicy binds metering to identities issued by the white-list control
// plane. Callers cannot substitute an ordinary VPN identity.
func NewPolicy(entitlement controlplane.WhiteListEntitlement, spec PolicySpec) (Policy, error) {
	identity, ok := entitlement.XrayIdentity()
	if !ok {
		return Policy{}, ErrInvalidInput
	}
	policy := Policy{
		accountID: entitlement.AccountID(), entitlementID: entitlement.EntitlementID(),
		transportID: entitlement.TransportProfileID(), billingPeriodID: spec.BillingPeriodID,
		expectedXrayIdentity: identity,
		Unit:                 spec.Unit, Basis: spec.Basis,
		IncludedBytes: spec.IncludedBytes, SoftLimitBytes: spec.SoftLimitBytes,
		HardLimitBytes: spec.HardLimitBytes, GraceBytes: spec.GraceBytes,
		Prices: spec.Prices,
	}
	if !policy.validBinding() || policy.Unit.bytes() == 0 || (policy.Basis != BasisDownlinkOnly && policy.Basis != BasisUplinkPlusDownlink && policy.Basis != BasisFree) {
		return Policy{}, ErrInvalidInput
	}
	if _, err := ResolvePrice(policy.Prices); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (policy Policy) AccountID() string       { return policy.accountID }
func (policy Policy) EntitlementID() string   { return policy.entitlementID }
func (policy Policy) TransportID() string     { return policy.transportID }
func (policy Policy) BillingPeriodID() string { return policy.billingPeriodID }

func (policy Policy) validBinding() bool {
	for _, value := range []string{policy.accountID, policy.entitlementID, policy.transportID, policy.billingPeriodID, policy.expectedXrayIdentity} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || strings.IndexByte(value, 0) >= 0 {
			return false
		}
	}
	return strings.HasPrefix(policy.entitlementID, "wl-ent-") && policy.expectedXrayIdentity == "wl:"+policy.entitlementID
}

type TariffSnapshot struct {
	Unit                                                      TrafficUnit
	Basis                                                     TrafficBasis
	IncludedBytes, SoftLimitBytes, HardLimitBytes, GraceBytes uint64
	Price                                                     ResolvedPrice
}
type UsageEvent struct {
	EventID, InstanceID, MeterEpoch, XrayIdentity string
	UplinkBytes, DownlinkBytes                    uint64
}
type OrderedUsageEvent struct {
	UsageEvent
	CounterGeneration uint64
	SampleSequence    uint64
}
type ExactAmount struct {
	Numerator   string
	Denominator uint64
	Currency    string
}
type UsageInterval struct {
	EventID, AccountID, EntitlementID, TransportID, BillingPeriodID, InstanceID, MeterEpoch, XrayIdentity string
	UplinkBytes, DownlinkBytes, BillableBytes                                                             uint64
	Snapshot                                                                                              TariffSnapshot
}
type LedgerEntry struct {
	EventID          string
	Interval         UsageInterval
	Snapshot         TariffSnapshot
	CalculatedAmount ExactAmount
}
type Diagnostic string

const (
	DiagnosticEpochStarted    Diagnostic = "EPOCH_STARTED"
	DiagnosticCounterReset    Diagnostic = "COUNTER_RESET"
	DiagnosticLateSample      Diagnostic = "LATE_SAMPLE"
	DiagnosticOrderingStarted Diagnostic = "ORDERING_STARTED"
)

type SuspensionReason string

const SuspensionHardLimit SuspensionReason = "HARD_LIMIT"

type SuspensionRecommendation struct {
	Recommended   bool
	EntitlementID string
	Reason        SuspensionReason
}
type Decision struct {
	Replay           bool
	Diagnostic       Diagnostic
	Interval         *UsageInterval
	Ledger           *LedgerEntry
	SoftLimitReached bool
	Suspension       SuspensionRecommendation
}

type counter struct {
	up, down             uint64
	ordered              bool
	generation, sequence uint64
}

type sampleOrder struct {
	enabled              bool
	generation, sequence uint64
	firstCumulative      bool
}

type eventRecord struct {
	event                                                                        UsageEvent
	order                                                                        sampleOrder
	accountID, entitlementID, transportID, billingPeriodID, expectedXrayIdentity string
	tariff                                                                       TariffSnapshot
}

type meterKey struct {
	instanceID string
	epochID    string
	identity   string
}

type periodKey struct {
	entitlementID   string
	billingPeriodID string
}
type State struct {
	seen               map[string]eventRecord
	counters           map[meterKey]counter
	included, measured map[periodKey]uint64
	suspended          map[string]bool
	ledger             []LedgerEntry
}

func NewState() State {
	return State{seen: map[string]eventRecord{}, counters: map[meterKey]counter{}, included: map[periodKey]uint64{}, measured: map[periodKey]uint64{}, suspended: map[string]bool{}}
}
func (s State) LedgerEntries() []LedgerEntry               { return append([]LedgerEntry(nil), s.ledger...) }
func (s State) WhiteListSuspended(entitlement string) bool { return s.suspended[entitlement] }
func (s State) clone() State {
	n := NewState()
	for k, v := range s.seen {
		n.seen[k] = v
	}
	for k, v := range s.counters {
		n.counters[k] = v
	}
	for k, v := range s.included {
		n.included[k] = v
	}
	for k, v := range s.measured {
		n.measured[k] = v
	}
	for k, v := range s.suspended {
		n.suspended[k] = v
	}
	n.ledger = append(n.ledger, s.ledger...)
	return n
}

// Apply preserves the legacy inferred-reset behavior for existing callers.
func Apply(state State, event UsageEvent, policy Policy) (State, Decision, error) {
	return apply(state, event, policy, sampleOrder{})
}

// ApplyOrdered uses a producer-supplied monotonic generation and sequence.
// Late samples are ignored atomically; a lower counter is accepted only after
// an explicit generation increase.
func ApplyOrdered(state State, event OrderedUsageEvent, policy Policy) (State, Decision, error) {
	if event.CounterGeneration == 0 || event.SampleSequence == 0 {
		return state, Decision{}, ErrInvalidInput
	}
	return apply(state, event.UsageEvent, policy, sampleOrder{
		enabled: true, generation: event.CounterGeneration, sequence: event.SampleSequence,
	})
}

func apply(state State, event UsageEvent, policy Policy, order sampleOrder) (State, Decision, error) {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.InstanceID) == "" || strings.TrimSpace(event.MeterEpoch) == "" || strings.TrimSpace(event.XrayIdentity) == "" || !policy.validBinding() || policy.Unit.bytes() == 0 {
		return state, Decision{}, ErrInvalidInput
	}
	if event.XrayIdentity != policy.expectedXrayIdentity {
		return state, Decision{}, ErrIdentityMismatch
	}
	price, err := ResolvePrice(policy.Prices)
	if err != nil {
		return state, Decision{}, err
	}
	if policy.Basis != BasisDownlinkOnly && policy.Basis != BasisUplinkPlusDownlink && policy.Basis != BasisFree {
		return state, Decision{}, ErrInvalidInput
	}
	record := eventRecord{
		event:     event,
		order:     order,
		accountID: policy.accountID, entitlementID: policy.entitlementID,
		transportID: policy.transportID, billingPeriodID: policy.billingPeriodID,
		expectedXrayIdentity: policy.expectedXrayIdentity,
		tariff: TariffSnapshot{
			policy.Unit, policy.Basis, policy.IncludedBytes, policy.SoftLimitBytes,
			policy.HardLimitBytes, policy.GraceBytes, price,
		},
	}
	if recorded, ok := state.seen[event.EventID]; ok {
		if recorded == record {
			return state, Decision{Replay: true}, nil
		}
		return state, Decision{}, &EventIDConflictError{EventID: event.EventID}
	}

	key := meterKey{event.InstanceID, event.MeterEpoch, event.XrayIdentity}
	period := periodKey{policy.entitlementID, policy.billingPeriodID}
	old, hasCounter := state.counters[key]
	if order.firstCumulative && hasCounter {
		return state, Decision{}, ErrInvalidInput
	}
	diagnostic := Diagnostic("")
	switch {
	case !hasCounter:
		if !order.firstCumulative {
			diagnostic = DiagnosticEpochStarted
		}
	case order.enabled:
		switch {
		case old.ordered && (order.generation < old.generation || (order.generation == old.generation && order.sequence <= old.sequence)):
			return state, Decision{Diagnostic: DiagnosticLateSample}, nil
		case old.ordered && order.generation > old.generation:
			diagnostic = DiagnosticCounterReset
		case old.ordered && (event.UplinkBytes < old.up || event.DownlinkBytes < old.down):
			return state, Decision{}, ErrResetGenerationRequired
		case !old.ordered:
			diagnostic = DiagnosticOrderingStarted
		}
	case old.ordered:
		return state, Decision{}, ErrOrderingModeMismatch
	case event.UplinkBytes < old.up || event.DownlinkBytes < old.down:
		diagnostic = DiagnosticCounterReset
	}

	var up, down, measured uint64
	if (hasCounter || order.firstCumulative) && diagnostic == "" {
		up = event.UplinkBytes - old.up
		down = event.DownlinkBytes - old.down
		measured = down
		if policy.Basis == BasisUplinkPlusDownlink {
			var overflow bool
			measured, overflow = checkedAdd(up, measured)
			if overflow {
				return state, Decision{}, ErrInvalidInput
			}
		}
		if policy.Basis == BasisFree {
			measured = 0
		}
		if _, overflow := checkedAdd(state.measured[period], measured); overflow {
			return state, Decision{}, ErrInvalidInput
		}
	}

	next := state.clone()
	next.seen[event.EventID] = record
	next.counters[key] = counter{
		up: event.UplinkBytes, down: event.DownlinkBytes,
		ordered: order.enabled, generation: order.generation, sequence: order.sequence,
	}
	if diagnostic != "" {
		return next, Decision{Diagnostic: diagnostic}, nil
	}

	remaining, known := next.included[period]
	if !known {
		remaining = policy.IncludedBytes
	}
	used := measured
	if used > remaining {
		used -= remaining
		remaining = 0
	} else {
		remaining -= used
		used = 0
	}
	next.included[period] = remaining
	next.measured[period] += measured
	snap := record.tariff
	interval := UsageInterval{event.EventID, policy.accountID, policy.entitlementID, policy.transportID, policy.billingPeriodID, event.InstanceID, event.MeterEpoch, event.XrayIdentity, up, down, used, snap}
	entry := LedgerEntry{event.EventID, interval, snap, exactAmount(used, price, policy.Unit)}
	next.ledger = append(next.ledger, entry)
	d := Decision{Interval: &interval, Ledger: &entry, SoftLimitReached: policy.SoftLimitBytes > 0 && next.measured[period] >= policy.SoftLimitBytes}
	if policy.HardLimitBytes > 0 && next.measured[period] > policy.HardLimitBytes && next.measured[period]-policy.HardLimitBytes > policy.GraceBytes {
		next.suspended[policy.entitlementID] = true
		d.Suspension = SuspensionRecommendation{true, policy.entitlementID, SuspensionHardLimit}
	}
	return next, d, nil
}
func checkedAdd(left, right uint64) (uint64, bool) {
	if ^uint64(0)-left < right {
		return 0, true
	}
	return left + right, false
}

func exactAmount(bytes uint64, price ResolvedPrice, unit TrafficUnit) ExactAmount {
	if price.Price.Mode == PriceFree || bytes == 0 {
		return ExactAmount{Numerator: "0", Denominator: 1, Currency: price.Price.Currency}
	}
	n := new(big.Int).Mul(new(big.Int).SetUint64(bytes), new(big.Int).SetUint64(price.Price.MinorUnitsPerUnit))
	d := new(big.Int).SetUint64(unit.bytes())
	g := new(big.Int).GCD(nil, nil, n, d)
	n.Div(n, g)
	d.Div(d, g)
	return ExactAmount{n.String(), d.Uint64(), price.Price.Currency}
}
