// Package shadowbilling contains pure, shadow-only CDN metering rules. It has
// no balance, wallet, API, or ordinary-VPN model, so it cannot charge or alter
// ordinary access.
package shadowbilling

import (
	"errors"
	"math/big"
	"strings"
)

var (
	ErrMissingPaidPrice = errors.New("shadowbilling: paid price is required")
	ErrInvalidInput     = errors.New("shadowbilling: invalid input")
)

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

type Policy struct {
	AccountID, EntitlementID, TransportID, BillingPeriodID    string
	Unit                                                      TrafficUnit
	Basis                                                     TrafficBasis
	IncludedBytes, SoftLimitBytes, HardLimitBytes, GraceBytes uint64
	Prices                                                    PriceOptions
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
	DiagnosticEpochStarted Diagnostic = "EPOCH_STARTED"
	DiagnosticCounterReset Diagnostic = "COUNTER_RESET"
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

type counter struct{ up, down uint64 }

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
	seen               map[string]bool
	counters           map[meterKey]counter
	included, measured map[periodKey]uint64
	suspended          map[string]bool
	ledger             []LedgerEntry
}

func NewState() State {
	return State{seen: map[string]bool{}, counters: map[meterKey]counter{}, included: map[periodKey]uint64{}, measured: map[periodKey]uint64{}, suspended: map[string]bool{}}
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

func Apply(state State, event UsageEvent, policy Policy) (State, Decision, error) {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.InstanceID) == "" || strings.TrimSpace(event.MeterEpoch) == "" || strings.TrimSpace(event.XrayIdentity) == "" || strings.TrimSpace(policy.AccountID) == "" || strings.TrimSpace(policy.EntitlementID) == "" || strings.TrimSpace(policy.TransportID) == "" || strings.TrimSpace(policy.BillingPeriodID) == "" || policy.Unit.bytes() == 0 {
		return state, Decision{}, ErrInvalidInput
	}
	price, err := ResolvePrice(policy.Prices)
	if err != nil {
		return state, Decision{}, err
	}
	if policy.Basis != BasisDownlinkOnly && policy.Basis != BasisUplinkPlusDownlink && policy.Basis != BasisFree {
		return state, Decision{}, ErrInvalidInput
	}
	key := meterKey{event.InstanceID, event.MeterEpoch, event.XrayIdentity}
	periodKey := periodKey{policy.EntitlementID, policy.BillingPeriodID}
	if old, ok := state.counters[key]; ok && event.UplinkBytes >= old.up && event.DownlinkBytes >= old.down {
		measured := event.DownlinkBytes - old.down
		if policy.Basis == BasisUplinkPlusDownlink {
			var overflow bool
			measured, overflow = checkedAdd(event.UplinkBytes-old.up, measured)
			if overflow {
				return state, Decision{}, ErrInvalidInput
			}
		}
		if policy.Basis == BasisFree {
			measured = 0
		}
		if _, overflow := checkedAdd(state.measured[periodKey], measured); overflow {
			return state, Decision{}, ErrInvalidInput
		}
	}
	next := state.clone()
	if next.seen[event.EventID] {
		return next, Decision{Replay: true}, nil
	}
	next.seen[event.EventID] = true
	old, ok := next.counters[key]
	next.counters[key] = counter{event.UplinkBytes, event.DownlinkBytes}
	if !ok {
		return next, Decision{Diagnostic: DiagnosticEpochStarted}, nil
	}
	if event.UplinkBytes < old.up || event.DownlinkBytes < old.down {
		return next, Decision{Diagnostic: DiagnosticCounterReset}, nil
	}
	up, down := event.UplinkBytes-old.up, event.DownlinkBytes-old.down
	measured := down
	if policy.Basis == BasisUplinkPlusDownlink {
		measured = up + down
	}
	if policy.Basis == BasisFree {
		measured = 0
	}
	remaining, known := next.included[periodKey]
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
	next.included[periodKey] = remaining
	next.measured[periodKey] += measured
	snap := TariffSnapshot{policy.Unit, policy.Basis, policy.IncludedBytes, policy.SoftLimitBytes, policy.HardLimitBytes, policy.GraceBytes, price}
	interval := UsageInterval{event.EventID, policy.AccountID, policy.EntitlementID, policy.TransportID, policy.BillingPeriodID, event.InstanceID, event.MeterEpoch, event.XrayIdentity, up, down, used, snap}
	entry := LedgerEntry{event.EventID, interval, snap, exactAmount(used, price, policy.Unit)}
	next.ledger = append(next.ledger, entry)
	d := Decision{Interval: &interval, Ledger: &entry, SoftLimitReached: policy.SoftLimitBytes > 0 && next.measured[periodKey] >= policy.SoftLimitBytes}
	if policy.HardLimitBytes > 0 && next.measured[periodKey] > policy.HardLimitBytes && next.measured[periodKey]-policy.HardLimitBytes > policy.GraceBytes {
		next.suspended[policy.EntitlementID] = true
		d.Suspension = SuspensionRecommendation{true, policy.EntitlementID, SuspensionHardLimit}
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
