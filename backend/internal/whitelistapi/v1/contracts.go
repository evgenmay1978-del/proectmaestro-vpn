// Package v1 defines the versioned, read-only wire contract between the
// isolated white-list control plane and Maestro Panel. It intentionally has no
// subscription, balance, wallet, or data-plane mutation methods.
package v1

import (
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Version  = "v1"
	BasePath = "/internal/white-list/v1"

	DefaultPageSize = 50
	MaxPageSize     = 100
)

var errInvalidContract = errors.New("whitelistapi/v1: invalid contract")

type EntitlementState string

const (
	EntitlementDisabled     EntitlementState = "DISABLED"
	EntitlementProvisioning EntitlementState = "PROVISIONING"
	EntitlementActive       EntitlementState = "ACTIVE"
	EntitlementGrace        EntitlementState = "GRACE"
	EntitlementSuspended    EntitlementState = "SUSPENDED"
	EntitlementError        EntitlementState = "ERROR"
	EntitlementExpired      EntitlementState = "EXPIRED"
)

type HealthStatus string

const (
	HealthUnknown     HealthStatus = "UNKNOWN"
	HealthHealthy     HealthStatus = "HEALTHY"
	HealthDegraded    HealthStatus = "DEGRADED"
	HealthUnavailable HealthStatus = "UNAVAILABLE"
)

type TrafficUnit string

const (
	UnitGBDecimal TrafficUnit = "GB_DECIMAL"
	UnitGiBBinary TrafficUnit = "GIB_BINARY"
)

type TrafficBasis string

const (
	BasisDownlinkOnly       TrafficBasis = "DOWNLINK_ONLY"
	BasisUplinkPlusDownlink TrafficBasis = "UPLINK_PLUS_DOWNLINK"
	BasisFree               TrafficBasis = "FREE"
)

type PriceSource string

const (
	PriceIndividual PriceSource = "INDIVIDUAL"
	PriceTariff     PriceSource = "TARIFF"
	PriceProfile    PriceSource = "PROFILE"
	PriceGlobal     PriceSource = "GLOBAL"
)

// Entitlement is the panel-safe projection. It deliberately excludes client
// UUIDs, encryption material, origin routes, subscription URIs and notes.
type Entitlement struct {
	ID                    string           `json:"id"`
	AccountID             string           `json:"account_id"`
	State                 EntitlementState `json:"state"`
	TransportProfileID    string           `json:"transport_profile_id,omitempty"`
	CompatibilityPresetID string           `json:"compatibility_preset_id,omitempty"`
	TransportReleaseID    string           `json:"transport_release_id,omitempty"`
	BillingEnabled        bool             `json:"billing_enabled"`
	SuspensionReason      string           `json:"suspension_reason,omitempty"`
	UpdatedAt             time.Time        `json:"updated_at"`
}

// Health contains bounded status and freshness facts, never process output,
// config, addresses, collector payloads or secrets.
type Health struct {
	AccountID          string       `json:"account_id"`
	Status             HealthStatus `json:"status"`
	CollectorStatus    HealthStatus `json:"collector_status"`
	XrayStatus         HealthStatus `json:"xray_status"`
	DataPlaneReleaseID string       `json:"data_plane_release_id,omitempty"`
	Fresh              bool         `json:"fresh"`
	LastMeterSampleAt  *time.Time   `json:"last_meter_sample_at,omitempty"`
	ObservedAt         time.Time    `json:"observed_at"`
}

// ExactAmount is an exact reduced rational number of minor currency units.
// A decimal string prevents floating-point financial values on the wire.
type ExactAmount struct {
	Numerator   string `json:"numerator"`
	Denominator uint64 `json:"denominator"`
	Currency    string `json:"currency"`
}

type Usage struct {
	AccountID              string       `json:"account_id"`
	EntitlementID          string       `json:"entitlement_id"`
	BillingPeriodID        string       `json:"billing_period_id"`
	Unit                   TrafficUnit  `json:"unit"`
	Basis                  TrafficBasis `json:"basis"`
	MeasuredBytes          uint64       `json:"measured_bytes"`
	IncludedBytes          uint64       `json:"included_bytes"`
	BillableBytes          uint64       `json:"billable_bytes"`
	RemainingIncludedBytes uint64       `json:"remaining_included_bytes"`
	SoftLimitBytes         uint64       `json:"soft_limit_bytes"`
	HardLimitBytes         uint64       `json:"hard_limit_bytes"`
	GraceBytes             uint64       `json:"grace_bytes"`
	AccruedAmount          ExactAmount  `json:"accrued_amount"`
	UpdatedAt              time.Time    `json:"updated_at"`
}

type LedgerEntry struct {
	ID              string       `json:"id"`
	EventID         string       `json:"event_id"`
	AccountID       string       `json:"account_id"`
	EntitlementID   string       `json:"entitlement_id"`
	BillingPeriodID string       `json:"billing_period_id"`
	BillableBytes   uint64       `json:"billable_bytes"`
	Unit            TrafficUnit  `json:"unit"`
	Basis           TrafficBasis `json:"basis"`
	PriceSource     PriceSource  `json:"price_source"`
	Amount          ExactAmount  `json:"amount"`
	OccurredAt      time.Time    `json:"occurred_at"`
}

type AuditChange struct {
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
}

type AuditRecord struct {
	ID         string        `json:"id"`
	AccountID  string        `json:"account_id"`
	ActorID    string        `json:"actor_id"`
	Action     string        `json:"action"`
	Reason     string        `json:"reason"`
	Changes    []AuditChange `json:"changes"`
	OccurredAt time.Time     `json:"occurred_at"`
}

type PageRequest struct {
	Limit  int
	Cursor string
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Fixtures is the complete panel projection an adapter may validate before it
// exposes the five independently paged/read resources.
type Fixtures struct {
	Entitlement Entitlement       `json:"entitlement"`
	Health      Health            `json:"health"`
	Usage       Usage             `json:"usage"`
	Ledger      Page[LedgerEntry] `json:"ledger"`
	Audit       Page[AuditRecord] `json:"audit"`
}

func (amount ExactAmount) Validate() error {
	if amount.Denominator == 0 || !validCurrency(amount.Currency) || amount.Numerator == "" ||
		(len(amount.Numerator) > 1 && amount.Numerator[0] == '0') {
		return errInvalidContract
	}
	numerator := new(big.Int)
	if _, ok := numerator.SetString(amount.Numerator, 10); !ok || numerator.Sign() < 0 || numerator.String() != amount.Numerator {
		return errInvalidContract
	}
	denominator := new(big.Int).SetUint64(amount.Denominator)
	if new(big.Int).GCD(nil, nil, numerator, denominator).Cmp(big.NewInt(1)) != 0 {
		return errInvalidContract
	}
	return nil
}

func (fixtures Fixtures) ValidateForAccount(accountID string) error {
	if !validOpaqueID(accountID) || fixtures.Entitlement.validateForAccount(accountID) != nil ||
		fixtures.Health.validateForAccount(accountID) != nil || fixtures.Usage.validateForAccount(accountID) != nil ||
		validateLedgerPage(fixtures.Ledger, accountID) != nil || validateAuditPage(fixtures.Audit, accountID) != nil {
		return errInvalidContract
	}
	return nil
}

func (value Entitlement) validateForAccount(accountID string) error {
	if !validOpaqueID(value.ID) || value.AccountID != accountID || !validEntitlementState(value.State) || !validTimestamp(value.UpdatedAt) {
		return errInvalidContract
	}
	for _, optionalID := range []string{value.TransportProfileID, value.CompatibilityPresetID, value.TransportReleaseID} {
		if optionalID != "" && !validOpaqueID(optionalID) {
			return errInvalidContract
		}
	}
	if value.State == EntitlementActive || value.State == EntitlementGrace || value.State == EntitlementSuspended {
		if value.TransportProfileID == "" || value.CompatibilityPresetID == "" || value.TransportReleaseID == "" {
			return errInvalidContract
		}
	}
	if value.State == EntitlementSuspended && !validText(value.SuspensionReason, false, 256) {
		return errInvalidContract
	}
	if value.SuspensionReason != "" && !validText(value.SuspensionReason, false, 256) {
		return errInvalidContract
	}
	return nil
}

func (value Health) validateForAccount(accountID string) error {
	if value.AccountID != accountID || !validHealth(value.Status) || !validHealth(value.CollectorStatus) ||
		!validHealth(value.XrayStatus) || !validTimestamp(value.ObservedAt) {
		return errInvalidContract
	}
	if value.DataPlaneReleaseID != "" && !validOpaqueID(value.DataPlaneReleaseID) {
		return errInvalidContract
	}
	if value.LastMeterSampleAt != nil && (!validTimestamp(*value.LastMeterSampleAt) || value.LastMeterSampleAt.After(value.ObservedAt)) {
		return errInvalidContract
	}
	return nil
}

func (value Usage) validateForAccount(accountID string) error {
	if value.AccountID != accountID || !validOpaqueID(value.EntitlementID) || !validOpaqueID(value.BillingPeriodID) ||
		!validUnit(value.Unit) || !validBasis(value.Basis) || !validTimestamp(value.UpdatedAt) || value.AccruedAmount.Validate() != nil ||
		value.BillableBytes > value.MeasuredBytes || value.RemainingIncludedBytes > value.IncludedBytes ||
		(value.SoftLimitBytes > 0 && value.HardLimitBytes > 0 && value.SoftLimitBytes > value.HardLimitBytes) ||
		(value.GraceBytes > 0 && value.HardLimitBytes == 0) {
		return errInvalidContract
	}
	return nil
}

func (value LedgerEntry) validateForAccount(accountID string) error {
	if !validOpaqueID(value.ID) || !validOpaqueID(value.EventID) || value.AccountID != accountID ||
		!validOpaqueID(value.EntitlementID) || !validOpaqueID(value.BillingPeriodID) || !validUnit(value.Unit) ||
		!validBasis(value.Basis) || !validPriceSource(value.PriceSource) || value.Amount.Validate() != nil || !validTimestamp(value.OccurredAt) {
		return errInvalidContract
	}
	return nil
}

func (value AuditRecord) validateForAccount(accountID string) error {
	if !validOpaqueID(value.ID) || value.AccountID != accountID || !validOpaqueID(value.ActorID) ||
		!validAuditAction(value.Action) || !validText(value.Reason, false, 512) || !validTimestamp(value.OccurredAt) ||
		len(value.Changes) == 0 || len(value.Changes) > 32 {
		return errInvalidContract
	}
	for _, change := range value.Changes {
		if !allowedAuditFields[change.Field] || !validText(change.OldValue, true, 256) ||
			!validText(change.NewValue, true, 256) || change.OldValue == change.NewValue {
			return errInvalidContract
		}
	}
	return nil
}

func validateLedgerPage(page Page[LedgerEntry], accountID string) error {
	if len(page.Items) > MaxPageSize || !validOptionalCursor(page.NextCursor) {
		return errInvalidContract
	}
	for _, item := range page.Items {
		if item.validateForAccount(accountID) != nil {
			return errInvalidContract
		}
	}
	return nil
}

func validateAuditPage(page Page[AuditRecord], accountID string) error {
	if len(page.Items) > MaxPageSize || !validOptionalCursor(page.NextCursor) {
		return errInvalidContract
	}
	for _, item := range page.Items {
		if item.validateForAccount(accountID) != nil {
			return errInvalidContract
		}
	}
	return nil
}

var allowedAuditFields = map[string]bool{
	"state": true, "transport_profile_id": true, "compatibility_preset_id": true,
	"transport_release_id": true, "billing_enabled": true, "tariff_id": true,
	"included_bytes": true, "soft_limit_bytes": true, "hard_limit_bytes": true,
	"grace_bytes": true, "suspension_reason": true,
}

func validEntitlementState(value EntitlementState) bool {
	switch value {
	case EntitlementDisabled, EntitlementProvisioning, EntitlementActive, EntitlementGrace, EntitlementSuspended, EntitlementError, EntitlementExpired:
		return true
	default:
		return false
	}
}

func validHealth(value HealthStatus) bool {
	return value == HealthUnknown || value == HealthHealthy || value == HealthDegraded || value == HealthUnavailable
}

func validUnit(value TrafficUnit) bool { return value == UnitGBDecimal || value == UnitGiBBinary }

func validBasis(value TrafficBasis) bool {
	return value == BasisDownlinkOnly || value == BasisUplinkPlusDownlink || value == BasisFree
}

func validPriceSource(value PriceSource) bool {
	return value == PriceIndividual || value == PriceTariff || value == PriceProfile || value == PriceGlobal
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validOpaqueID(value string) bool {
	if len(value) == 0 || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:-", char)) {
			return false
		}
	}
	return true
}

func validOptionalCursor(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 256 || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._~:-", char)) {
			return false
		}
	}
	return true
}

func validText(value string, allowEmpty bool, max int) bool {
	if !utf8.ValidString(value) || len(value) > max || value != strings.TrimSpace(value) || (!allowEmpty && value == "") {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validAuditAction(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}

func validTimestamp(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
