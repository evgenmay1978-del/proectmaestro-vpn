package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// PanelAuth is the session boundary shared by the legacy-compatible HA panel.
type PanelAuth interface {
	CreateSession(context.Context, CreateSessionCommand) (SessionView, error)
	Authorize(context.Context, AuthorizeCommand) (PrincipalView, error)
	RevokeSessions(context.Context, RevokeSessionsCommand) error
	ChangePrincipalPassword(context.Context, ChangePasswordCommand) error
}

// Business is the complete application port used by the rqlite HTTP adapter.
// Implementations own canonical transactions; handlers never open legacy stores
// or invoke provisioners, shells, or SSH.
type Business interface {
	PanelAuth
	ConsumeRateLimit(context.Context, RateLimitCommand) (RateLimitView, error)
	CustomerByToken(context.Context, string) (CustomerView, error)
	CustomerByLogin(context.Context, string) (CustomerView, error)
	ListCustomers(context.Context, CustomerFilter) ([]CustomerView, error)
	CustomerStats(context.Context) (CustomerStatsView, error)
	CustomerUsage(context.Context, string) (CustomerUsageView, error)
	Tariffs(context.Context) ([]TariffView, error)
	ApprovedOTA(context.Context) (OTAManifestView, error)
	CreateOrder(context.Context, CreateOrderCommand) (OrderView, error)
	OrderByID(context.Context, string) (OrderView, error)
	ListOrders(context.Context, OrderFilter) ([]OrderView, error)
	MarkPaymentClaimed(context.Context, ClaimPaymentCommand) (OrderView, error)
	ConfirmPayment(context.Context, ConfirmPaymentCommand) (ConfirmPaymentResult, error)
	CancelOrder(context.Context, CancelOrderCommand) (OrderView, error)
	SubscriptionSnapshot(context.Context, string) (SubscriptionSnapshot, error)
	TouchDevice(context.Context, TouchDeviceCommand) (DeviceDecision, error)
	RedeemTrial(context.Context, RedeemTrialCommand) (CustomerView, error)
	ProvisionCustomer(context.Context, ProvisionCustomerCommand) (CustomerView, error)
	ExtendCustomer(context.Context, ExtendCustomerCommand) (CustomerView, error)
	RenewCustomer(context.Context, RenewCustomerCommand) (CustomerView, error)
	SetCustomerExpiry(context.Context, SetExpiryCommand) (CustomerView, error)
	DisableCustomer(context.Context, CustomerStateCommand) (CustomerView, error)
	EnableCustomer(context.Context, CustomerStateCommand) (CustomerView, error)
	DeleteCustomer(context.Context, DeleteCustomerCommand) error
	RunExpirySweep(context.Context, ExpirySweepCommand) (OperationView, error)
	ResetDevices(context.Context, ResetDevicesCommand) error
	ReconcileServices(context.Context, ReconcileServicesCommand) (OperationView, error)
	ReadSetting(context.Context, string) (SettingView, error)
	UpdateSetting(context.Context, UpdateSettingCommand) (SettingView, error)
	OLCRTCState(context.Context) (OLCRTCView, error)
	SetOLCRTCRoom(context.Context, SetOLCRTCRoomCommand) (SettingView, error)
	SetOLCRTCGrant(context.Context, SetOLCRTCGrantCommand) (SettingView, error)
	WBTokenStatus(context.Context) (SecretStatusView, error)
	SetWBToken(context.Context, SetSecretCommand) error
	RequestWBRoom(context.Context, RequestWBRoomCommand) (ExternalActionView, error)
	VKTurnState(context.Context) (VKTurnView, error)
	UpdateVKTurn(context.Context, UpdateVKTurnCommand) (SettingView, error)
	SetVKTurnEnabled(context.Context, SetVKTurnEnabledCommand) (SettingView, error)
	ClusterStatus(context.Context) (ClusterStatusView, error)
	RecentAudit(context.Context, AuditFilter) ([]AuditView, error)
	MigrateServiceEndpoint(context.Context, MigrateEndpointCommand) (OperationView, error)
}

type CreateSessionCommand struct {
	Password string
}

type SessionView struct {
	Token string `json:"token"`
	CSRF  string `json:"csrf"`
}

type AuthorizeCommand struct {
	Session    string
	CSRF       string
	Permission string
}

type PrincipalView struct {
	ID          string   `json:"id"`
	Permissions []string `json:"permissions,omitempty"`
}

type RevokeSessionsCommand struct {
	PrincipalID string
	Actor       string
}

type ChangePasswordCommand struct {
	PrincipalID    string
	Current        string
	New            string
	IdempotencyKey string
}

type RateLimitCommand struct {
	Scope  string
	Key    string
	Limit  int
	Window time.Duration
	Block  time.Duration
}

type RateLimitView struct {
	Allowed           bool
	RetryAfterSeconds int
}

type CustomerView struct {
	CustomerID string     `json:"-"`
	Login      string     `json:"login"`
	SubURL     string     `json:"sub_url"`
	Expires    time.Time  `json:"expires"`
	DaysLeft   int        `json:"days_left"`
	Active     bool       `json:"active"`
	Disabled   bool       `json:"disabled,omitempty"`
	Devices    int        `json:"devices"`
	Protocols  []string   `json:"protocols"`
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	Generation int64      `json:"generation,omitempty"`
}

type CustomerFilter struct {
	Active          *bool
	Limit           int
	AfterLogin      string
	AfterCustomerID string
}

type CustomerStatsView struct {
	Total      int `json:"total"`
	Active     int `json:"active"`
	Expired    int `json:"expired"`
	Expiring7D int `json:"expiring_7d"`
	Disabled   int `json:"disabled"`
	Devices    int `json:"devices"`
}

type CustomerUsageView struct {
	Login       string            `json:"login"`
	Bytes       int64             `json:"bytes"`
	DeviceIDs   map[string]string `json:"-"`
	DeviceLimit int               `json:"-"`
}

type TariffView struct {
	ID   string `json:"id"`
	Days int    `json:"days"`
	RUB  int    `json:"rub"`
}

type OTAManifestView struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

type CreateOrderCommand struct {
	Tariff         string
	SubToken       string
	Login          string
	IdempotencyKey string
}

type OrderView struct {
	CreatedAtUnix     int64  `json:"-"`
	OrderID           string `json:"order_id"`
	Code              string `json:"code"`
	RUB               int    `json:"rub"`
	Days              int    `json:"days,omitempty"`
	Tariff            string `json:"tariff,omitempty"`
	SBPPhone          string `json:"sbp_phone,omitempty"`
	PayURL            string `json:"pay_url,omitempty"`
	Status            string `json:"status"`
	SubURL            string `json:"sub_url,omitempty"`
	PaymentState      string `json:"payment_state,omitempty"`
	ProvisioningState string `json:"provisioning_state,omitempty"`
	ResultGeneration  int64  `json:"result_generation,omitempty"`
}

type OrderFilter struct {
	Status             string
	Limit              int
	AfterCreatedAtUnix int64
	AfterOrderID       string
}

type ClaimPaymentCommand struct {
	OrderID        string
	IdempotencyKey string
}

type ConfirmPaymentCommand struct {
	OrderID        string
	Actor          string
	IdempotencyKey string
}

type ConfirmPaymentResult struct {
	Order     OrderView     `json:"order"`
	Customer  CustomerView  `json:"customer"`
	Operation OperationView `json:"operation"`
}

type CancelOrderCommand struct {
	OrderID        string
	Actor          string
	IdempotencyKey string
}

type SubscriptionSnapshot struct {
	ContentType string          `json:"-"`
	Customer    CustomerView    `json:"customer"`
	Document    json.RawMessage `json:"document"`
	Cached      bool            `json:"cached,omitempty"`
	AsOf        time.Time       `json:"-"`
}

type TouchDeviceCommand struct {
	Login          string
	DeviceID       string
	IdempotencyKey string
}

type DeviceDecision struct {
	Allowed bool `json:"allowed"`
	Cached  bool `json:"cached,omitempty"`
}

type RedeemTrialCommand struct {
	Login          string
	Anchor         string
	DRMIdentity    string
	IdempotencyKey string
}

type ProvisionCustomerCommand struct {
	Login          string
	Days           int
	IdempotencyKey string
}

type ExtendCustomerCommand struct {
	Login          string
	Days           int
	IdempotencyKey string
}

type RenewCustomerCommand struct {
	Login          string
	Days           int
	IdempotencyKey string
}

type SetExpiryCommand struct {
	Login          string
	Expires        time.Time
	IdempotencyKey string
}

type CustomerStateCommand struct {
	Login          string
	IdempotencyKey string
}

type DeleteCustomerCommand struct {
	Login          string
	IdempotencyKey string
}

type ExpirySweepCommand struct {
	IdempotencyKey string
}

type OperationView struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Count int    `json:"count,omitempty"`
}

type ResetDevicesCommand struct {
	Login          string
	IdempotencyKey string
}

type ReconcileServicesCommand struct {
	Login          string
	Logins         []string
	Service        string
	IdempotencyKey string
}

type SettingView struct {
	Key     string          `json:"key"`
	Version int64           `json:"version"`
	Value   json.RawMessage `json:"value,omitempty"`
}

type UpdateSettingCommand struct {
	Key             string
	ExpectedVersion int64
	Value           json.RawMessage
	IdempotencyKey  string
}

type OLCRTCRoomView struct {
	Room     string `json:"room"`
	Provider string `json:"provider"`
}

type OLCRTCView struct {
	Room     string                    `json:"room"`
	Provider string                    `json:"provider"`
	Logins   []string                  `json:"logins"`
	Rooms    map[string]OLCRTCRoomView `json:"rooms,omitempty"`
}

type SetOLCRTCRoomCommand struct {
	Login           string
	Room            string
	Provider        string
	ExpectedVersion int64
	IdempotencyKey  string
}

type SetOLCRTCGrantCommand struct {
	Login           string
	Enabled         bool
	ExpectedVersion int64
	IdempotencyKey  string
}

type SecretStatusView struct {
	Configured bool `json:"configured"`
}

type SetSecretCommand struct {
	Secret          string
	ExpectedVersion int64
	IdempotencyKey  string
}

type RequestWBRoomCommand struct {
	Login             string
	ActionKey         string
	ReplacesActionKey string
	IdempotencyKey    string
}

type ExternalActionView struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Room  string `json:"room,omitempty"`
}

type VKTurnView struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server,omitempty"`
}

type UpdateVKTurnCommand struct {
	Value           json.RawMessage
	ExpectedVersion int64
	IdempotencyKey  string
}

type SetVKTurnEnabledCommand struct {
	Enabled         bool
	ExpectedVersion int64
	IdempotencyKey  string
}

type ClusterStatusView struct {
	Ready          bool                  `json:"ready"`
	Quorum         bool                  `json:"quorum"`
	ReadReady      bool                  `json:"read_ready"`
	WriteReadiness string                `json:"write_readiness"`
	DataComplete   bool                  `json:"data_complete"`
	Replication    ReplicationStatusView `json:"replication"`
	Nodes          NodeStatusView        `json:"nodes"`
	Apply          ApplyStatusView       `json:"apply"`
	Outbox         OutboxStatusView      `json:"outbox"`
	Telegram       TelegramStatusView    `json:"telegram"`
	DNSTLS         ProbeStatusView       `json:"dns_tls"`
	Backup         BackupStatusView      `json:"backup"`
	Restore        RestoreStatusView     `json:"restore"`
	Failures       []FailureSummaryView  `json:"failures"`
}

type ReplicationStatusView struct {
	State          string `json:"state"`
	DataComplete   bool   `json:"data_complete"`
	LeaderID       string `json:"leader_id"`
	ReachableNodes int64  `json:"reachable_nodes"`
	MaxLagEntries  int64  `json:"max_lag_entries"`
}

type NodeStatusView struct {
	Voters               int64 `json:"voters"`
	EnabledVoters        int64 `json:"enabled_voters"`
	ActiveServiceTargets int64 `json:"active_service_targets"`
	FencedServiceTargets int64 `json:"fenced_service_targets"`
	StaleReceipts        int64 `json:"stale_receipts"`
}

type ApplyStatusView struct {
	Pending          int64 `json:"pending"`
	Failed           int64 `json:"failed"`
	FailedReceipts   int64 `json:"failed_receipts"`
	MaxGenerationLag int64 `json:"max_generation_lag"`
}

type OutboxStatusView struct {
	Pending                 int64 `json:"pending"`
	Failed                  int64 `json:"failed"`
	OldestPendingAgeSeconds int64 `json:"oldest_pending_age_seconds"`
}

type TelegramStatusView struct {
	Routes         int64 `json:"routes"`
	ActivePollers  int64 `json:"active_pollers"`
	InboxRejected  int64 `json:"inbox_rejected"`
	DeliveryFailed int64 `json:"delivery_failed"`
	DataComplete   bool  `json:"data_complete"`
}

type ProbeStatusView struct {
	State        string `json:"state"`
	Targets      int64  `json:"targets"`
	DataComplete bool   `json:"data_complete"`
}

type BackupStatusView struct {
	State              string `json:"state"`
	DirtyGeneration    int64  `json:"dirty_generation"`
	VerifiedGeneration int64  `json:"verified_generation"`
	GenerationGap      int64  `json:"generation_gap"`
}

type RestoreStatusView struct {
	State        string `json:"state"`
	Epoch        int64  `json:"epoch"`
	DataComplete bool   `json:"data_complete"`
}

type FailureSummaryView struct {
	Component string `json:"component"`
	Count     int64  `json:"count"`
}

type AuditFilter struct {
	Limit              int
	AfterCreatedAtUnix int64
	AfterID            string
}

type AuditView struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
}

type MigrateEndpointCommand struct {
	Service        string
	Endpoint       string
	IdempotencyKey string
}

// ControlPlaneServer is the HA HTTP adapter. It intentionally has no legacy
// store or Provisioner fields.
type ControlPlaneServer struct {
	business       Business
	cfg            Config
	panelCursorKey [32]byte
}

// NewControlPlane builds the rqlite-only HTTP adapter.
func NewControlPlane(business Business, cfg Config) *ControlPlaneServer {
	cursorKey := sha256.Sum256([]byte("maestrovpn-panel-cursor-v1\x00" + cfg.PanelPath + "\x00" + cfg.PanelPasswordHash))
	return &ControlPlaneServer{business: business, cfg: cfg, panelCursorKey: cursorKey}
}

func (s *ControlPlaneServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok " + BuildCommit))
	})
	mux.HandleFunc("/sub/", s.handleControlPlaneSub)
	mux.HandleFunc("/claim", s.handleControlPlaneClaim)
	mux.HandleFunc("/order/tariffs", s.handleControlPlaneTariffs)
	mux.HandleFunc("/order/paid-claim", s.handleControlPlanePaymentClaim)
	mux.HandleFunc("/order", s.handleControlPlaneCreateOrder)
	mux.HandleFunc("/order/", s.handleControlPlaneOrder)
	mux.HandleFunc("/trial", s.handleControlPlaneTrial)
	if s.cfg.UpdateDir != "" {
		mux.HandleFunc("/update/", s.handleControlPlaneOTA)
	}
	if s.cfg.ReportDir != "" {
		mux.HandleFunc("/report", s.handleControlPlaneReport)
	}

	if s.cfg.AdminToken != "" {
		mux.HandleFunc("/admin/provision", s.controlPlaneAdmin(s.handleControlPlaneProvision))
		mux.HandleFunc("/admin/extend", s.controlPlaneAdmin(s.handleControlPlaneExtend))
		mux.HandleFunc("/admin/renew", s.controlPlaneAdmin(s.handleControlPlaneRenew))
		mux.HandleFunc("/admin/set-expiry", s.controlPlaneAdmin(s.handleControlPlaneSetExpiry))
		mux.HandleFunc("/admin/reset-devices", s.controlPlaneAdmin(s.handleControlPlaneResetDevices))
		mux.HandleFunc("/admin/customer", s.controlPlaneAdmin(s.handleControlPlaneCustomer))
		mux.HandleFunc("/admin/backfill-anytls", s.controlPlaneAdmin(s.controlPlaneReconcile("anytls")))
		mux.HandleFunc("/admin/backfill-s3", s.controlPlaneAdmin(s.controlPlaneReconcile("s3")))
		mux.HandleFunc("/admin/backfill-s4", s.controlPlaneAdmin(s.handleControlPlaneBackfillS4))
		mux.HandleFunc("/admin/bulk-import", s.controlPlaneAdmin(s.handleControlPlaneBulkImport))
		mux.HandleFunc("/admin/migrate-anytls-s2", s.controlPlaneAdmin(s.handleControlPlaneMigrate))
		mux.HandleFunc("/admin/order/confirm", s.controlPlaneAdmin(s.handleControlPlaneConfirmOrder))
		mux.HandleFunc("/admin/order/cancel", s.controlPlaneAdmin(s.handleControlPlaneCancelOrder))
		mux.HandleFunc("/admin/olcrtc", s.controlPlaneAdmin(s.handleControlPlaneOLCRTC))
		mux.HandleFunc("/admin/olcrtc/room", s.controlPlaneAdmin(s.handleControlPlaneOLCRTCRoom))
	}
	s.registerControlPlanePanel(mux)
	return mux
}

func (s *ControlPlaneServer) controlPlaneAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || subtle.ConstantTimeCompare(
			[]byte(strings.TrimPrefix(header, prefix)), []byte(s.cfg.AdminToken),
		) != 1 {
			writeControlPlaneJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *ControlPlaneServer) handleControlPlaneExtend(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login string `json:"login"`
		Days  int    `json:"days"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.ExtendCustomer(r.Context(), ExtendCustomerCommand{
		Login: request.Login, Days: request.Days, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneRenew(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Login string `json:"login"`
		Days  int    `json:"days"`
	}
	if !decodeControlPlaneMutation(w, r, &request) {
		return
	}
	view, err := s.business.RenewCustomer(r.Context(), RenewCustomerCommand{
		Login: request.Login, Days: request.Days, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeControlPlaneJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
		return
	}
	writeControlPlaneJSON(w, http.StatusOK, view)
}

func (s *ControlPlaneServer) handleControlPlaneBulkImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeControlPlaneJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeControlPlaneJSON(w, http.StatusGone, map[string]string{
		"error": "bulk import is offline-only in rqlite mode",
	})
}

func decodeControlPlaneMutation(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeControlPlaneJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return false
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeControlPlaneJSON(w, http.StatusPreconditionRequired, map[string]string{"error": "Idempotency-Key required"})
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeControlPlaneJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}

func (s *ControlPlaneServer) controlPlaneNotFound(w http.ResponseWriter, _ *http.Request) {
	writeControlPlaneJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func controlPlanePanelActions() []string {
	return []string{
		"provision", "extend", "renew", "set_expiry", "reset_devices",
		"disable", "enable", "delete", "delete_expired",
	}
}

func writeControlPlaneJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
