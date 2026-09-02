package whitelistbalance

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	GBDecimal    int64 = 1_000_000_000
	MaxExclusive int64 = 1<<63 - 1
)

var (
	ErrInvalid           = errors.New("whitelist balance: invalid input")
	ErrOperationConflict = errors.New("whitelist balance: operation conflict")
	ErrPeriodConflict    = errors.New("whitelist balance: period conflict")
	ErrNoActivePeriod    = errors.New("whitelist balance: no active period")
	ErrOverflow          = errors.New("whitelist balance: overflow")
	ErrPrimaryInactive   = errors.New("whitelist balance: primary access inactive")
)

type OperationKind string

const (
	OperationSchedulePeriod  OperationKind = "SCHEDULE_PERIOD"
	OperationCreditPurchased OperationKind = "CREDIT_PURCHASED"
	OperationApplyUsage      OperationKind = "APPLY_USAGE"
)

type EntryKind string

const (
	EntryIncludedGrant   EntryKind = "INCLUDED_GRANT"
	EntryPurchasedCredit EntryKind = "PURCHASED_CREDIT"
	EntryConsumed        EntryKind = "CONSUMED"
	EntryUncovered       EntryKind = "UNCOVERED"
	EntryAdjustment      EntryKind = "ADJUSTMENT"
)

type Period struct {
	ID                 string
	Ordinal            int64
	StartsAtUnix       int64
	EndsAtUnix         int64
	IncludedGrantBytes int64
	AccessOrderID      string
}

type BalanceProjection struct {
	EntitlementID           string
	CurrentPeriodID         string
	IncludedRemainingBytes  int64
	PurchasedRemainingBytes int64
	LifetimeConsumedBytes   int64
	UncoveredBytes          int64
	Version                 int64
	Pending                 bool
	FreshThroughUnix        int64
}

type State struct {
	EntitlementID            string
	Periods                  []Period
	Projection               *BalanceProjection
	IncludedOutstandingBytes map[string]int64
}

type UsageAllocation struct {
	IncludedBytes  int64
	PurchasedBytes int64
	UncoveredBytes int64
}

type JournalIntent struct {
	Kind                EntryKind
	PeriodID            string
	SourceOrderID       string
	MeterEpoch          string
	IntervalID          string
	IncludedDeltaBytes  int64
	PurchasedDeltaBytes int64
	ConsumedDeltaBytes  int64
	UncoveredDeltaBytes int64
}

type OperationResult struct {
	Projection BalanceProjection
	PeriodID   string
	Allocation UsageAllocation
}

type OperationRecord struct {
	ID            string
	EntitlementID string
	Kind          OperationKind
	RequestSHA256 [32]byte
	Result        OperationResult
}

type Transition struct {
	State    State
	Result   OperationResult
	Record   OperationRecord
	Journal  []JournalIntent
	Replayed bool
}

type SchedulePeriodRequest struct {
	OperationID string
	NowUnix     int64
	Period      Period
}

type CreditPurchasedRequest struct {
	OperationID   string
	PeriodID      string
	SourceOrderID string
	NowUnix       int64
	Bytes         int64
	PrimaryActive bool
}

type ApplyUsageRequest struct {
	OperationID      string
	PeriodID         string
	MeterEpoch       string
	IntervalID       string
	AppliedAtUnix    int64
	IntervalEndUnix  int64
	FreshThroughUnix int64
	Bytes            int64
	PrimaryActive    bool
}

type BalanceSnapshot struct {
	Projection       BalanceProjection
	PeriodStartsUnix int64
	PeriodEndsUnix   int64
	AvailableBytes   int64
	UsableBytes      int64
	PrimaryActive    bool
	Frozen           bool
}

func NewState(entitlementID string) (State, error) {
	if !validID(entitlementID) {
		return State{}, ErrInvalid
	}
	return State{
		EntitlementID:            entitlementID,
		IncludedOutstandingBytes: map[string]int64{},
	}, nil
}

func SchedulePeriod(state State, request SchedulePeriodRequest, replay *OperationRecord) (Transition, error) {
	digest, err := requestDigest(OperationSchedulePeriod, state.EntitlementID, request)
	if err != nil {
		return Transition{}, err
	}
	if replay != nil {
		return replayTransition(state, request.OperationID, OperationSchedulePeriod, digest, replay)
	}
	if !validID(request.OperationID) {
		return Transition{}, ErrInvalid
	}
	if err := validateTimestamp(request.NowUnix); err != nil {
		return Transition{}, err
	}
	if err := validatePeriod(request.Period); err != nil {
		return Transition{}, err
	}
	if request.NowUnix >= request.Period.EndsAtUnix {
		return Transition{}, ErrPeriodConflict
	}
	if err := validateState(state); err != nil {
		return Transition{}, err
	}

	next := cloneState(state)
	journal, _, err := advanceCore(&next, request.NowUnix)
	if err != nil {
		return Transition{}, err
	}
	if err := validateNextPeriod(next.Periods, request.Period); err != nil {
		return Transition{}, err
	}
	if len(next.Periods) == 0 && !periodActive(request.Period, request.NowUnix) {
		return Transition{}, ErrNoActivePeriod
	}

	next.Periods = append(next.Periods, request.Period)
	next.IncludedOutstandingBytes[request.Period.ID] = request.Period.IncludedGrantBytes
	if next.Projection == nil {
		next.Projection = &BalanceProjection{
			EntitlementID:          next.EntitlementID,
			CurrentPeriodID:        request.Period.ID,
			IncludedRemainingBytes: request.Period.IncludedGrantBytes,
			Version:                1,
		}
	} else {
		if next.Projection.CurrentPeriodID == "" && periodActive(request.Period, request.NowUnix) {
			next.Projection.CurrentPeriodID = request.Period.ID
			next.Projection.IncludedRemainingBytes = request.Period.IncludedGrantBytes
		}
		if err := bumpVersion(next.Projection); err != nil {
			return Transition{}, err
		}
	}
	if request.Period.IncludedGrantBytes > 0 {
		journal = append(journal, JournalIntent{
			Kind:               EntryIncludedGrant,
			PeriodID:           request.Period.ID,
			SourceOrderID:      request.Period.AccessOrderID,
			IncludedDeltaBytes: request.Period.IncludedGrantBytes,
		})
	}
	if err := validateState(next); err != nil {
		return Transition{}, err
	}

	result := resultFor(next, request.Period.ID, UsageAllocation{})
	return newOperationTransition(next, result, request.OperationID, OperationSchedulePeriod, digest, journal), nil
}

func CreditPurchased(state State, request CreditPurchasedRequest, replay *OperationRecord) (Transition, error) {
	digest, err := requestDigest(OperationCreditPurchased, state.EntitlementID, request)
	if err != nil {
		return Transition{}, err
	}
	if replay != nil {
		return replayTransition(state, request.OperationID, OperationCreditPurchased, digest, replay)
	}
	if !validID(request.OperationID) || !validID(request.PeriodID) || !validID(request.SourceOrderID) {
		return Transition{}, ErrInvalid
	}
	if err := validateTimestamp(request.NowUnix); err != nil {
		return Transition{}, err
	}
	if err := validatePositive(request.Bytes); err != nil {
		return Transition{}, err
	}
	if !request.PrimaryActive {
		return Transition{}, ErrPrimaryInactive
	}
	if err := validateState(state); err != nil {
		return Transition{}, err
	}

	next := cloneState(state)
	journal, _, err := advanceCore(&next, request.NowUnix)
	if err != nil {
		return Transition{}, err
	}
	if next.Projection == nil {
		return Transition{}, ErrNoActivePeriod
	}
	if next.Projection.CurrentPeriodID != request.PeriodID {
		return Transition{}, ErrPeriodConflict
	}
	if _, ok := periodByID(next.Periods, request.PeriodID); !ok {
		return Transition{}, ErrPeriodConflict
	}
	purchased, err := checkedAdd(next.Projection.PurchasedRemainingBytes, request.Bytes)
	if err != nil {
		return Transition{}, err
	}
	next.Projection.PurchasedRemainingBytes = purchased
	if err := bumpVersion(next.Projection); err != nil {
		return Transition{}, err
	}
	journal = append(journal, JournalIntent{
		Kind:                EntryPurchasedCredit,
		PeriodID:            request.PeriodID,
		SourceOrderID:       request.SourceOrderID,
		PurchasedDeltaBytes: request.Bytes,
	})
	if err := validateState(next); err != nil {
		return Transition{}, err
	}

	result := resultFor(next, request.PeriodID, UsageAllocation{})
	return newOperationTransition(next, result, request.OperationID, OperationCreditPurchased, digest, journal), nil
}

func ApplyUsage(state State, request ApplyUsageRequest, replay *OperationRecord) (Transition, error) {
	digest, err := requestDigest(OperationApplyUsage, state.EntitlementID, request)
	if err != nil {
		return Transition{}, err
	}
	if replay != nil {
		return replayTransition(state, request.OperationID, OperationApplyUsage, digest, replay)
	}
	if !validID(request.OperationID) || !validID(request.PeriodID) || !validID(request.MeterEpoch) || !validID(request.IntervalID) {
		return Transition{}, ErrInvalid
	}
	for _, timestamp := range []int64{request.AppliedAtUnix, request.IntervalEndUnix, request.FreshThroughUnix} {
		if err := validateTimestamp(timestamp); err != nil {
			return Transition{}, err
		}
	}
	if err := validateNonNegative(request.Bytes); err != nil {
		return Transition{}, err
	}
	if request.AppliedAtUnix < request.IntervalEndUnix || request.FreshThroughUnix < request.IntervalEndUnix || request.FreshThroughUnix > request.AppliedAtUnix {
		return Transition{}, ErrInvalid
	}
	if err := validateState(state); err != nil {
		return Transition{}, err
	}

	next := cloneState(state)
	if next.Projection == nil {
		return Transition{}, ErrNoActivePeriod
	}
	if next.Projection.CurrentPeriodID != request.PeriodID {
		return Transition{}, ErrPeriodConflict
	}
	period, ok := periodByID(next.Periods, request.PeriodID)
	if !ok || request.IntervalEndUnix < period.StartsAtUnix || request.IntervalEndUnix > period.EndsAtUnix {
		return Transition{}, ErrPeriodConflict
	}
	if request.FreshThroughUnix > period.EndsAtUnix {
		return Transition{}, ErrPeriodConflict
	}
	if request.FreshThroughUnix < next.Projection.FreshThroughUnix {
		return Transition{}, ErrPeriodConflict
	}

	allocation := UsageAllocation{}
	journal := make([]JournalIntent, 0, 4)
	changed := false
	if request.Bytes > 0 {
		if request.PrimaryActive {
			allocation.IncludedBytes = min64(request.Bytes, next.Projection.IncludedRemainingBytes)
			remaining := request.Bytes - allocation.IncludedBytes
			allocation.PurchasedBytes = min64(remaining, next.Projection.PurchasedRemainingBytes)
			allocation.UncoveredBytes = remaining - allocation.PurchasedBytes
			next.Projection.IncludedRemainingBytes -= allocation.IncludedBytes
			next.IncludedOutstandingBytes[request.PeriodID] -= allocation.IncludedBytes
			next.Projection.PurchasedRemainingBytes -= allocation.PurchasedBytes
		} else {
			allocation.UncoveredBytes = request.Bytes
		}

		next.Projection.LifetimeConsumedBytes, err = checkedAdd(next.Projection.LifetimeConsumedBytes, request.Bytes)
		if err != nil {
			return Transition{}, err
		}
		next.Projection.UncoveredBytes, err = checkedAdd(next.Projection.UncoveredBytes, allocation.UncoveredBytes)
		if err != nil {
			return Transition{}, err
		}
		intent := JournalIntent{
			PeriodID:            request.PeriodID,
			MeterEpoch:          request.MeterEpoch,
			IntervalID:          request.IntervalID,
			IncludedDeltaBytes:  -allocation.IncludedBytes,
			PurchasedDeltaBytes: -allocation.PurchasedBytes,
			ConsumedDeltaBytes:  request.Bytes,
			UncoveredDeltaBytes: allocation.UncoveredBytes,
		}
		if allocation.IncludedBytes == 0 && allocation.PurchasedBytes == 0 {
			intent.Kind = EntryUncovered
		} else {
			intent.Kind = EntryConsumed
		}
		journal = append(journal, intent)
		changed = true
	}
	if request.FreshThroughUnix > next.Projection.FreshThroughUnix {
		next.Projection.FreshThroughUnix = request.FreshThroughUnix
		changed = true
	}
	rolloverJournal, rolloverChanged, err := advanceCore(&next, request.AppliedAtUnix)
	if err != nil {
		return Transition{}, err
	}
	journal = append(journal, rolloverJournal...)
	changed = changed || rolloverChanged
	if changed {
		if err := bumpVersion(next.Projection); err != nil {
			return Transition{}, err
		}
	}
	if err := validateState(next); err != nil {
		return Transition{}, err
	}

	result := resultFor(next, request.PeriodID, allocation)
	return newOperationTransition(next, result, request.OperationID, OperationApplyUsage, digest, journal), nil
}

func AdvanceAt(state State, nowUnix int64) (Transition, error) {
	if err := validateTimestamp(nowUnix); err != nil {
		return Transition{}, err
	}
	if err := validateState(state); err != nil {
		return Transition{}, err
	}
	next := cloneState(state)
	journal, changed, err := advanceCore(&next, nowUnix)
	if err != nil {
		return Transition{}, err
	}
	if changed && next.Projection != nil {
		if err := bumpVersion(next.Projection); err != nil {
			return Transition{}, err
		}
	}
	if err := validateState(next); err != nil {
		return Transition{}, err
	}
	periodID := ""
	if next.Projection != nil {
		periodID = next.Projection.CurrentPeriodID
	}
	return Transition{
		State:   next,
		Result:  resultFor(next, periodID, UsageAllocation{}),
		Journal: cloneJournal(journal),
	}, nil
}

func Snapshot(state State, primaryActive bool) (BalanceSnapshot, error) {
	if err := validateState(state); err != nil {
		return BalanceSnapshot{}, err
	}
	snapshot := BalanceSnapshot{PrimaryActive: primaryActive, Frozen: !primaryActive}
	if state.Projection == nil {
		return snapshot, nil
	}
	snapshot.Projection = *state.Projection
	available, err := checkedAdd(state.Projection.IncludedRemainingBytes, state.Projection.PurchasedRemainingBytes)
	if err != nil {
		return BalanceSnapshot{}, err
	}
	snapshot.AvailableBytes = available
	if primaryActive && state.Projection.CurrentPeriodID != "" {
		snapshot.UsableBytes = available
	}
	if period, ok := periodByID(state.Periods, state.Projection.CurrentPeriodID); ok {
		snapshot.PeriodStartsUnix = period.StartsAtUnix
		snapshot.PeriodEndsUnix = period.EndsAtUnix
	}
	return snapshot, nil
}

func advanceCore(state *State, nowUnix int64) ([]JournalIntent, bool, error) {
	if err := validateTimestamp(nowUnix); err != nil {
		return nil, false, err
	}
	if state.Projection == nil {
		return nil, false, nil
	}
	journal := make([]JournalIntent, 0, len(state.Periods))
	changed := false
	for _, period := range state.Periods {
		outstanding := state.IncludedOutstandingBytes[period.ID]
		if period.EndsAtUnix <= nowUnix && outstanding > 0 {
			journal = append(journal, JournalIntent{
				Kind:               EntryAdjustment,
				PeriodID:           period.ID,
				SourceOrderID:      period.AccessOrderID,
				IncludedDeltaBytes: -outstanding,
			})
			state.IncludedOutstandingBytes[period.ID] = 0
			changed = true
		}
	}

	currentID := ""
	included := int64(0)
	for _, period := range state.Periods {
		if periodActive(period, nowUnix) {
			currentID = period.ID
			included = state.IncludedOutstandingBytes[period.ID]
			break
		}
	}
	if state.Projection.CurrentPeriodID != currentID || state.Projection.IncludedRemainingBytes != included {
		state.Projection.CurrentPeriodID = currentID
		state.Projection.IncludedRemainingBytes = included
		changed = true
	}
	return journal, changed, nil
}

func validateNextPeriod(periods []Period, candidate Period) error {
	for _, period := range periods {
		if period.ID == candidate.ID {
			return ErrPeriodConflict
		}
	}
	if len(periods) == 0 {
		return nil
	}
	last := periods[len(periods)-1]
	expectedOrdinal, err := checkedAdd(last.Ordinal, 1)
	if err != nil {
		return err
	}
	if candidate.Ordinal != expectedOrdinal || candidate.StartsAtUnix < last.EndsAtUnix {
		return ErrPeriodConflict
	}
	return nil
}

func validateState(state State) error {
	if !validID(state.EntitlementID) {
		return ErrInvalid
	}
	periodIDs := make(map[string]Period, len(state.Periods))
	for index, period := range state.Periods {
		if err := validatePeriod(period); err != nil {
			return err
		}
		if _, exists := periodIDs[period.ID]; exists {
			return ErrPeriodConflict
		}
		periodIDs[period.ID] = period
		if index > 0 {
			previous := state.Periods[index-1]
			expectedOrdinal, err := checkedAdd(previous.Ordinal, 1)
			if err != nil {
				return err
			}
			if period.Ordinal != expectedOrdinal || period.StartsAtUnix < previous.EndsAtUnix {
				return ErrPeriodConflict
			}
		}
	}
	for periodID, outstanding := range state.IncludedOutstandingBytes {
		period, ok := periodIDs[periodID]
		if !ok {
			return ErrInvalid
		}
		if err := validateNonNegative(outstanding); err != nil {
			return err
		}
		if outstanding > period.IncludedGrantBytes {
			return ErrInvalid
		}
	}
	for _, period := range state.Periods {
		if _, ok := state.IncludedOutstandingBytes[period.ID]; !ok {
			return ErrInvalid
		}
	}
	if state.Projection == nil {
		if len(state.Periods) != 0 {
			return ErrInvalid
		}
		return nil
	}
	projection := state.Projection
	if projection.EntitlementID != state.EntitlementID || projection.Version <= 0 {
		return ErrInvalid
	}
	for _, value := range []int64{
		projection.IncludedRemainingBytes,
		projection.PurchasedRemainingBytes,
		projection.LifetimeConsumedBytes,
		projection.UncoveredBytes,
		projection.Version,
		projection.FreshThroughUnix,
	} {
		if err := validateNonNegative(value); err != nil {
			return err
		}
	}
	if projection.CurrentPeriodID == "" {
		if projection.IncludedRemainingBytes != 0 {
			return ErrInvalid
		}
	} else {
		if _, ok := periodIDs[projection.CurrentPeriodID]; !ok {
			return ErrInvalid
		}
		if state.IncludedOutstandingBytes[projection.CurrentPeriodID] != projection.IncludedRemainingBytes {
			return ErrInvalid
		}
	}
	for _, period := range state.Periods {
		if _, err := checkedAdd(projection.PurchasedRemainingBytes, state.IncludedOutstandingBytes[period.ID]); err != nil {
			return err
		}
	}
	_, err := checkedAdd(projection.IncludedRemainingBytes, projection.PurchasedRemainingBytes)
	return err
}

func validatePeriod(period Period) error {
	if !validID(period.ID) || !validID(period.AccessOrderID) {
		return ErrInvalid
	}
	for _, value := range []int64{period.Ordinal, period.StartsAtUnix, period.EndsAtUnix, period.IncludedGrantBytes} {
		if err := validateNonNegative(value); err != nil {
			return err
		}
	}
	if period.EndsAtUnix <= period.StartsAtUnix {
		return ErrInvalid
	}
	return nil
}

func validateTimestamp(value int64) error {
	return validateNonNegative(value)
}

func validatePositive(value int64) error {
	if value <= 0 {
		return ErrInvalid
	}
	return validateNonNegative(value)
}

func validateNonNegative(value int64) error {
	if value < 0 {
		return ErrInvalid
	}
	if value >= MaxExclusive {
		return ErrOverflow
	}
	return nil
}

func checkedAdd(left, right int64) (int64, error) {
	if err := validateNonNegative(left); err != nil {
		return 0, err
	}
	if err := validateNonNegative(right); err != nil {
		return 0, err
	}
	if right >= MaxExclusive-left {
		return 0, ErrOverflow
	}
	return left + right, nil
}

func bumpVersion(projection *BalanceProjection) error {
	version, err := checkedAdd(projection.Version, 1)
	if err != nil {
		return err
	}
	projection.Version = version
	return nil
}

func periodActive(period Period, nowUnix int64) bool {
	return period.StartsAtUnix <= nowUnix && nowUnix < period.EndsAtUnix
}

func periodByID(periods []Period, periodID string) (Period, bool) {
	for _, period := range periods {
		if period.ID == periodID {
			return period, true
		}
	}
	return Period{}, false
}

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsRune(value, 0)
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func requestDigest(kind OperationKind, entitlementID string, request any) ([32]byte, error) {
	if !validID(entitlementID) {
		return [32]byte{}, ErrInvalid
	}
	payload := struct {
		Version       int           `json:"version"`
		Kind          OperationKind `json:"kind"`
		EntitlementID string        `json:"entitlement_id"`
		Request       any           `json:"request"`
	}{
		Version:       1,
		Kind:          kind,
		EntitlementID: entitlementID,
		Request:       request,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: canonical request", ErrInvalid)
	}
	return sha256.Sum256(encoded), nil
}

func replayTransition(state State, operationID string, kind OperationKind, digest [32]byte, replay *OperationRecord) (Transition, error) {
	if replay.ID != operationID || replay.EntitlementID != state.EntitlementID || replay.Kind != kind || replay.RequestSHA256 != digest {
		return Transition{}, ErrOperationConflict
	}
	return Transition{
		State:    cloneState(state),
		Result:   replay.Result,
		Record:   *replay,
		Replayed: true,
	}, nil
}

func newOperationTransition(state State, result OperationResult, operationID string, kind OperationKind, digest [32]byte, journal []JournalIntent) Transition {
	record := OperationRecord{
		ID:            operationID,
		EntitlementID: state.EntitlementID,
		Kind:          kind,
		RequestSHA256: digest,
		Result:        result,
	}
	return Transition{
		State:   cloneState(state),
		Result:  result,
		Record:  record,
		Journal: cloneJournal(journal),
	}
}

func resultFor(state State, periodID string, allocation UsageAllocation) OperationResult {
	result := OperationResult{PeriodID: periodID, Allocation: allocation}
	if state.Projection != nil {
		result.Projection = *state.Projection
	}
	return result
}

func cloneState(state State) State {
	cloned := state
	cloned.Periods = append([]Period(nil), state.Periods...)
	if state.Projection != nil {
		projection := *state.Projection
		cloned.Projection = &projection
	}
	cloned.IncludedOutstandingBytes = make(map[string]int64, len(state.IncludedOutstandingBytes))
	for periodID, outstanding := range state.IncludedOutstandingBytes {
		cloned.IncludedOutstandingBytes[periodID] = outstanding
	}
	return cloned
}

func cloneJournal(journal []JournalIntent) []JournalIntent {
	return append([]JournalIntent(nil), journal...)
}
