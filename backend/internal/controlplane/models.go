package controlplane

import (
	"errors"
	"net/http"
	"time"
)

var (
	ErrNotFound            = errors.New("controlplane: not found")
	ErrConflict            = errors.New("controlplane: conflict")
	ErrForbidden           = errors.New("controlplane: forbidden")
	ErrInvalidState        = errors.New("controlplane: invalid state")
	ErrDeviceLimit         = errors.New("controlplane: device limit reached")
	ErrSubscriptionChanged = errors.New("controlplane: subscription precondition changed")
	ErrUnavailable         = errors.New("controlplane: unavailable")
	ErrLeaseHeld           = errors.New("controlplane: expiry lease held")
	ErrLeaseLost           = errors.New("controlplane: expiry lease lost")
)

type Customer struct {
	ID            string
	Status        string
	ExpiresAtUnix int64
	Generation    int64
	Access        CustomerAccess
}

type Tariff struct {
	VersionID    string
	Code         string
	DurationDays int
	AmountMinor  int64
	Currency     string
}

// CreateOrderCommand contains caller identity and a tariff-version reference.
// Caller-supplied terms are compatibility inputs only; tariff_versions wins.
type CreateOrderCommand struct {
	TariffVersionID string
	CustomerID      string
	BuyerScope      string
	BuyerIdentity   string
	OriginBotID     string
	ChatIdentity    string
	Actor           string
	Channel         string
	SourceEventID   string
	AmountMinor     int64
	Currency        string
	DurationSeconds int64
}

type ClaimPaymentCommand struct {
	OrderID       string
	Actor         string
	Channel       string
	SourceEventID string
}

type ConfirmPaymentCommand struct {
	OrderID             string
	IdempotencyKey      string
	PaymentReference    string
	Provider            string
	TariffVersionID     string
	Actor               string
	Channel             string
	SourceEventID       string
	ProposedPaymentID   string
	ProposedOperationID string
	OccurredAt          time.Time
}

type CancelOrderCommand struct {
	OrderID        string
	IdempotencyKey string
	Actor          string
	Channel        string
}

type OrderView struct {
	OrderID         string
	AmountMinor     int64
	Currency        string
	DurationSeconds int64
	OriginBotID     string
	PaymentState    PaymentState
}

type ConfirmPaymentResult struct {
	OrderID       string `json:"order_id"`
	OperationID   string `json:"operation_id"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
	Generation    int64  `json:"generation"`
}

type ExpirySweepCommand struct {
	WorkerID string
}

type ExpiryLease struct {
	WorkerID   string
	LeaseFence int64
}

type ExpirySweepResult struct {
	CustomersExpired int64
	OperationID      string
	LeaseFence       int64
}

type SettingUpdate struct {
	Key                string
	ExpectedGeneration int64
	PublicValueJSON    string
	Members            []string
	Secret             *Envelope
	Actor              string
	CommandType        string
	IdempotencyKey     string
	RequestFingerprint string
	TargetMembers      []string
	TargetPayloads     map[string]string
}

type SettingResult struct {
	Generation int64
}

type DeviceClaim struct {
	DeviceID       string
	AdmittedAtUnix int64
}

type SubscriptionDeviceClaimCommand struct {
	CustomerID                 string
	TokenHMAC                  string
	RawDeviceIdentity          string
	Platform                   string
	Limit                      int
	RequireCredentials         bool
	ExpectedCustomerGeneration int64
	ExpectedTokenGeneration    int64
	ExpectedExpiresAtUnix      int64
	ExpectedRestoreEpoch       int64
}

type Permission string

const (
	PermissionCustomerRead     Permission = "customer.read"
	PermissionProvision        Permission = "customer.provision"
	PermissionPaymentDecide    Permission = "payment.decide"
	PermissionCriticalSettings Permission = "settings.critical"
)

type Authorization struct {
	PrincipalID string
	Role        string
}

type SessionResult struct {
	Cookie    http.Cookie
	CSRFToken string
	ExpiresAt time.Time
}

type OTAApproval struct {
	VersionCode     int64  `json:"versionCode"`
	VersionName     string `json:"versionName"`
	APKSize         int64  `json:"size"`
	SHA256          string `json:"sha256"`
	SourceReleaseID string `json:"sourceReleaseId"`
	Generation      int64  `json:"-"`
}
