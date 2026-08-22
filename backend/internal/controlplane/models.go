package controlplane

import (
	"errors"
	"net/http"
	"time"
)

var (
	ErrNotFound    = errors.New("controlplane: not found")
	ErrConflict    = errors.New("controlplane: conflict")
	ErrForbidden   = errors.New("controlplane: forbidden")
	ErrDeviceLimit = errors.New("controlplane: device limit reached")
)

type Customer struct {
	ID            string
	Status        string
	ExpiresAtUnix int64
	Generation    int64
}

type Tariff struct {
	VersionID    string
	Code         string
	DurationDays int
	AmountMinor  int64
	Currency     string
}

type SettingUpdate struct {
	Key                string
	ExpectedGeneration int64
	PublicValueJSON    string
	Members            []string
	Secret             *Envelope
	Actor              string
}

type SettingResult struct {
	Generation int64
}

type DeviceClaim struct {
	DeviceID string
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
