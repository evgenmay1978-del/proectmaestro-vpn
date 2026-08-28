package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

type ServiceBusinessConfig struct {
	SubBaseURL string
	SBPPhone   string
	PayURL     string
	TrialDays  int
}

// ServiceBusiness is the only Business implementation used by the rqlite
// runtime. Every mutation delegates to a canonical controlplane.Service command.
type ServiceBusiness struct {
	service *controlplane.Service
	cfg     ServiceBusinessConfig
}

func NewServiceBusiness(service *controlplane.Service, cfg ServiceBusinessConfig) *ServiceBusiness {
	if cfg.TrialDays <= 0 {
		cfg.TrialDays = 2
	}
	return &ServiceBusiness{service: service, cfg: cfg}
}

type serviceBusinessError struct {
	err    error
	status int
}

func (e serviceBusinessError) Error() string   { return e.err.Error() }
func (e serviceBusinessError) Unwrap() error   { return e.err }
func (e serviceBusinessError) HTTPStatus() int { return e.status }

func businessError(err error) error {
	if err == nil {
		return nil
	}
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, controlplane.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, controlplane.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, controlplane.ErrDeviceLimit), errors.Is(err, controlplane.ErrConflict), errors.Is(err, controlplane.ErrLeaseHeld), errors.Is(err, controlplane.ErrLeaseLost):
		status = http.StatusConflict
	}
	return serviceBusinessError{err: err, status: status}
}

func (b *ServiceBusiness) available() error {
	if b == nil || b.service == nil {
		return serviceBusinessError{err: controlplane.ErrUnavailable, status: http.StatusServiceUnavailable}
	}
	return nil
}

func (b *ServiceBusiness) CreateSession(ctx context.Context, command CreateSessionCommand) (SessionView, error) {
	if err := b.available(); err != nil {
		return SessionView{}, err
	}
	session, err := b.service.AuthenticatePassword(ctx, command.Password)
	if err != nil {
		return SessionView{}, businessError(err)
	}
	return SessionView{Token: session.Cookie.Value, CSRF: session.CSRFToken}, nil
}

func (b *ServiceBusiness) Authorize(ctx context.Context, command AuthorizeCommand) (PrincipalView, error) {
	if err := b.available(); err != nil {
		return PrincipalView{}, err
	}
	permission := controlplane.PermissionCustomerRead
	switch strings.ToLower(strings.TrimSpace(command.Permission)) {
	case "write", "settings.critical":
		permission = controlplane.PermissionCriticalSettings
	case "customer.provision":
		permission = controlplane.PermissionProvision
	case "payment.decide":
		permission = controlplane.PermissionPaymentDecide
	}
	authorization, err := b.service.Authorize(ctx, command.Session, command.CSRF, permission)
	if err != nil {
		return PrincipalView{}, businessError(err)
	}
	return PrincipalView{ID: authorization.PrincipalID, Permissions: []string{string(permission)}}, nil
}

func (b *ServiceBusiness) RevokeSessions(ctx context.Context, command RevokeSessionsCommand) error {
	if err := b.available(); err != nil {
		return err
	}
	return businessError(b.service.RevokeSessions(ctx, command.PrincipalID, command.Actor))
}

func (b *ServiceBusiness) ChangePrincipalPassword(ctx context.Context, command ChangePasswordCommand) error {
	if err := b.available(); err != nil {
		return err
	}
	return businessError(b.service.ChangePrincipalPassword(ctx, command.PrincipalID, command.Current, command.New, command.IdempotencyKey))
}

func (b *ServiceBusiness) CustomerByToken(ctx context.Context, token string) (CustomerView, error) {
	if err := b.available(); err != nil {
		return CustomerView{}, err
	}
	customer, err := b.service.BusinessCustomerByToken(ctx, token)
	if err != nil {
		return CustomerView{}, businessError(err)
	}
	return b.customerView(customer), nil
}

func (b *ServiceBusiness) CustomerByLogin(ctx context.Context, login string) (CustomerView, error) {
	if err := b.available(); err != nil {
		return CustomerView{}, err
	}
	customer, err := b.service.BusinessCustomerByLogin(ctx, login)
	if err != nil {
		return CustomerView{}, businessError(err)
	}
	return b.customerView(customer), nil
}

func (b *ServiceBusiness) ListCustomers(ctx context.Context, filter CustomerFilter) ([]CustomerView, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	customers, err := b.service.ListBusinessCustomers(ctx)
	if err != nil {
		return nil, businessError(err)
	}
	views := make([]CustomerView, 0, len(customers))
	for _, customer := range customers {
		view := b.customerView(customer)
		if filter.Active != nil && view.Active != *filter.Active {
			continue
		}
		views = append(views, view)
	}
	return views, nil
}

func (b *ServiceBusiness) CustomerStats(ctx context.Context) (CustomerStatsView, error) {
	customers, err := b.ListCustomers(ctx, CustomerFilter{})
	if err != nil {
		return CustomerStatsView{}, err
	}
	stats := CustomerStatsView{Total: len(customers)}
	for _, customer := range customers {
		switch {
		case customer.Disabled:
			stats.Disabled++
		case customer.Active:
			stats.Active++
		default:
			stats.Expired++
		}
	}
	return stats, nil
}

func (b *ServiceBusiness) CustomerUsage(ctx context.Context, login string) (CustomerUsageView, error) {
	if err := b.available(); err != nil {
		return CustomerUsageView{}, err
	}
	bytes, err := b.service.BusinessCustomerUsage(ctx, login)
	if err != nil {
		return CustomerUsageView{}, businessError(err)
	}
	return CustomerUsageView{Login: login, Bytes: bytes}, nil
}

func (b *ServiceBusiness) Tariffs(ctx context.Context) ([]TariffView, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	tariffs, err := b.service.Tariffs(ctx)
	if err != nil {
		return nil, businessError(err)
	}
	views := make([]TariffView, 0, len(tariffs))
	for _, tariff := range tariffs {
		views = append(views, TariffView{ID: tariff.Code, Days: tariff.DurationDays, RUB: int(tariff.AmountMinor / 100)})
	}
	return views, nil
}

func (b *ServiceBusiness) ApprovedOTA(ctx context.Context) (OTAManifestView, error) {
	if err := b.available(); err != nil {
		return OTAManifestView{}, err
	}
	approval, err := b.service.ApprovedOTA(ctx)
	if err != nil {
		return OTAManifestView{}, businessError(err)
	}
	return OTAManifestView{Version: approval.VersionName, URL: "/update/" + approval.VersionName + ".apk", SHA256: approval.SHA256}, nil
}

func (b *ServiceBusiness) CreateOrder(ctx context.Context, command CreateOrderCommand) (OrderView, error) {
	if err := b.available(); err != nil {
		return OrderView{}, err
	}
	tariffs, err := b.service.Tariffs(ctx)
	if err != nil {
		return OrderView{}, businessError(err)
	}
	var version string
	for _, tariff := range tariffs {
		if tariff.Code == command.Tariff || tariff.VersionID == command.Tariff {
			version = tariff.VersionID
			break
		}
	}
	if version == "" {
		return OrderView{}, businessError(controlplane.ErrNotFound)
	}
	created, err := b.service.CreateOrder(ctx, controlplane.CreateOrderCommand{
		TariffVersionID: version, BuyerScope: "legacy-http", BuyerIdentity: command.IdempotencyKey,
		Actor: "legacy-http", Channel: "legacy-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return OrderView{}, businessError(err)
	}
	order, err := b.service.BusinessOrderByID(ctx, created.OrderID)
	if err != nil {
		return OrderView{}, businessError(err)
	}
	return b.orderView(ctx, order), nil
}

func (b *ServiceBusiness) OrderByID(ctx context.Context, orderID string) (OrderView, error) {
	if err := b.available(); err != nil {
		return OrderView{}, err
	}
	order, err := b.service.BusinessOrderByID(ctx, orderID)
	if err != nil {
		return OrderView{}, businessError(err)
	}
	return b.orderView(ctx, order), nil
}

func (b *ServiceBusiness) ListOrders(ctx context.Context, filter OrderFilter) ([]OrderView, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	orders, err := b.service.ListBusinessOrders(ctx, filter.Status)
	if err != nil {
		return nil, businessError(err)
	}
	views := make([]OrderView, 0, len(orders))
	for _, order := range orders {
		views = append(views, b.orderView(ctx, order))
	}
	return views, nil
}

func (b *ServiceBusiness) MarkPaymentClaimed(ctx context.Context, command ClaimPaymentCommand) (OrderView, error) {
	if err := b.available(); err != nil {
		return OrderView{}, err
	}
	result, err := b.service.MarkPaymentClaimed(ctx, controlplane.ClaimPaymentCommand{
		OrderID: command.OrderID, Actor: "legacy-http", Channel: "legacy-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return OrderView{}, businessError(err)
	}
	order, err := b.service.BusinessOrderByID(ctx, result.OrderID)
	if err != nil {
		return OrderView{}, businessError(err)
	}
	return b.orderView(ctx, order), nil
}

func (b *ServiceBusiness) ConfirmPayment(ctx context.Context, command ConfirmPaymentCommand) (ConfirmPaymentResult, error) {
	if err := b.available(); err != nil {
		return ConfirmPaymentResult{}, err
	}
	confirmed, err := b.service.ConfirmPayment(ctx, controlplane.ConfirmPaymentCommand{
		OrderID: command.OrderID, IdempotencyKey: command.IdempotencyKey,
		PaymentReference: command.IdempotencyKey, Provider: "manual-sbp", Actor: command.Actor,
		Channel: "legacy-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return ConfirmPaymentResult{}, businessError(err)
	}
	order, err := b.service.BusinessOrderByID(ctx, confirmed.OrderID)
	if err != nil {
		return ConfirmPaymentResult{}, businessError(err)
	}
	customer, err := b.service.BusinessCustomerByID(ctx, order.CustomerID)
	if err != nil {
		return ConfirmPaymentResult{}, businessError(err)
	}
	return ConfirmPaymentResult{
		Order: b.orderView(ctx, order), Customer: b.customerView(customer),
		Operation: OperationView{ID: confirmed.OperationID, State: "pending"},
	}, nil
}

func (b *ServiceBusiness) CancelOrder(ctx context.Context, command CancelOrderCommand) (OrderView, error) {
	if err := b.available(); err != nil {
		return OrderView{}, err
	}
	result, err := b.service.CancelOrder(ctx, controlplane.CancelOrderCommand{
		OrderID: command.OrderID, IdempotencyKey: command.IdempotencyKey, Actor: command.Actor, Channel: "legacy-http",
	})
	if err != nil {
		return OrderView{}, businessError(err)
	}
	order, err := b.service.BusinessOrderByID(ctx, result.OrderID)
	if err != nil {
		return OrderView{}, businessError(err)
	}
	return b.orderView(ctx, order), nil
}

func (b *ServiceBusiness) SubscriptionSnapshot(ctx context.Context, token string) (SubscriptionSnapshot, error) {
	if err := b.available(); err != nil {
		return SubscriptionSnapshot{}, err
	}
	customer, document, err := b.service.BusinessSubscriptionDocument(ctx, token)
	if err != nil {
		return SubscriptionSnapshot{}, businessError(err)
	}
	return SubscriptionSnapshot{Customer: b.customerView(customer), Document: document}, nil
}

func (b *ServiceBusiness) TouchDevice(ctx context.Context, command TouchDeviceCommand) (DeviceDecision, error) {
	if err := b.available(); err != nil {
		return DeviceDecision{}, err
	}
	customer, err := b.service.BusinessCustomerByLogin(ctx, command.Login)
	if err != nil {
		return DeviceDecision{}, businessError(err)
	}
	_, err = b.service.ClaimDevice(ctx, customer.ID, command.DeviceID, "maestro", 5)
	if err != nil {
		return DeviceDecision{Allowed: false}, businessError(err)
	}
	return DeviceDecision{Allowed: true}, nil
}

func (b *ServiceBusiness) RedeemTrial(ctx context.Context, command RedeemTrialCommand) (CustomerView, error) {
	if err := b.available(); err != nil {
		return CustomerView{}, err
	}
	customer, err := b.service.RedeemTrial(ctx, controlplane.RedeemTrialCommand{
		Login: command.Login, Anchor: command.Anchor, DRMIdentity: command.DRMIdentity,
		Days: b.cfg.TrialDays, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return CustomerView{}, businessError(err)
	}
	return b.customerView(controlplane.BusinessCustomer{Customer: customer, Login: command.Login}), nil
}

func (b *ServiceBusiness) ProvisionCustomer(ctx context.Context, command ProvisionCustomerCommand) (CustomerView, error) {
	return b.customerMutation(ctx, command.Login, func() (controlplane.Customer, error) {
		return b.service.ProvisionCustomer(ctx, controlplane.ProvisionCustomerCommand{Login: command.Login, Days: command.Days, IdempotencyKey: command.IdempotencyKey})
	})
}

func (b *ServiceBusiness) ExtendCustomer(ctx context.Context, command ExtendCustomerCommand) (CustomerView, error) {
	return b.customerMutation(ctx, command.Login, func() (controlplane.Customer, error) {
		return b.service.ExtendCustomer(ctx, controlplane.ExtendCustomerCommand{Login: command.Login, Days: command.Days, IdempotencyKey: command.IdempotencyKey})
	})
}

func (b *ServiceBusiness) RenewCustomer(ctx context.Context, command RenewCustomerCommand) (CustomerView, error) {
	return b.customerMutation(ctx, command.Login, func() (controlplane.Customer, error) {
		return b.service.RenewCustomer(ctx, controlplane.RenewCustomerCommand{Login: command.Login, Days: command.Days, IdempotencyKey: command.IdempotencyKey})
	})
}

func (b *ServiceBusiness) SetCustomerExpiry(ctx context.Context, command SetExpiryCommand) (CustomerView, error) {
	return b.customerMutation(ctx, command.Login, func() (controlplane.Customer, error) {
		return b.service.SetCustomerExpiry(ctx, controlplane.SetExpiryCommand{Login: command.Login, ExpiresAt: command.Expires, IdempotencyKey: command.IdempotencyKey})
	})
}

func (b *ServiceBusiness) DisableCustomer(ctx context.Context, command CustomerStateCommand) (CustomerView, error) {
	return b.customerMutation(ctx, command.Login, func() (controlplane.Customer, error) {
		return b.service.DisableCustomer(ctx, controlplane.CustomerStateCommand{Login: command.Login, IdempotencyKey: command.IdempotencyKey})
	})
}

func (b *ServiceBusiness) EnableCustomer(ctx context.Context, command CustomerStateCommand) (CustomerView, error) {
	return b.customerMutation(ctx, command.Login, func() (controlplane.Customer, error) {
		return b.service.EnableCustomer(ctx, controlplane.CustomerStateCommand{Login: command.Login, IdempotencyKey: command.IdempotencyKey})
	})
}

func (b *ServiceBusiness) DeleteCustomer(ctx context.Context, command DeleteCustomerCommand) error {
	if err := b.available(); err != nil {
		return err
	}
	return businessError(b.service.DeleteCustomer(ctx, controlplane.DeleteCustomerCommand{Login: command.Login, IdempotencyKey: command.IdempotencyKey}))
}

func (b *ServiceBusiness) RunExpirySweep(ctx context.Context, command ExpirySweepCommand) (OperationView, error) {
	if err := b.available(); err != nil {
		return OperationView{}, err
	}
	result, err := b.service.RunExpirySweep(ctx, controlplane.ExpirySweepCommand{WorkerID: command.IdempotencyKey})
	if err != nil {
		return OperationView{}, businessError(err)
	}
	return OperationView{ID: result.OperationID, State: "applied", Count: int(result.CustomersExpired)}, nil
}

func (b *ServiceBusiness) ResetDevices(ctx context.Context, command ResetDevicesCommand) error {
	if err := b.available(); err != nil {
		return err
	}
	return businessError(b.service.ResetDevices(ctx, controlplane.ResetDevicesCommand{Login: command.Login, IdempotencyKey: command.IdempotencyKey}))
}

func (b *ServiceBusiness) ReconcileServices(ctx context.Context, command ReconcileServicesCommand) (OperationView, error) {
	if err := b.available(); err != nil {
		return OperationView{}, err
	}
	if _, err := b.service.BusinessCustomerByLogin(ctx, command.Login); err != nil {
		return OperationView{}, businessError(err)
	}
	count, err := b.service.ReconcileBusinessService(ctx, command.Service)
	if err != nil {
		return OperationView{}, businessError(err)
	}
	return OperationView{ID: command.IdempotencyKey, State: "applied", Count: count}, nil
}

func (b *ServiceBusiness) ReadSetting(ctx context.Context, key string) (SettingView, error) {
	if err := b.available(); err != nil {
		return SettingView{}, err
	}
	setting, err := b.service.ReadBusinessSetting(ctx, key)
	if err != nil {
		return SettingView{}, businessError(err)
	}
	return SettingView{Key: key, Version: setting.Generation, Value: setting.PublicValueJSON}, nil
}

func (b *ServiceBusiness) UpdateSetting(ctx context.Context, command UpdateSettingCommand) (SettingView, error) {
	if err := b.available(); err != nil {
		return SettingView{}, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return SettingView{}, businessError(controlplane.ErrForbidden)
	}
	result, err := b.service.UpdateSetting(ctx, controlplane.SettingUpdate{
		Key: command.Key, ExpectedGeneration: command.ExpectedVersion, PublicValueJSON: string(command.Value), Actor: "panel",
		CommandType: "setting." + command.Key + ".update", IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return SettingView{}, businessError(err)
	}
	return SettingView{Key: command.Key, Version: result.Generation, Value: command.Value}, nil
}

func (b *ServiceBusiness) OLCRTCState(ctx context.Context) (OLCRTCView, error) {
	if err := b.available(); err != nil {
		return OLCRTCView{}, err
	}
	setting, err := b.service.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil {
		return OLCRTCView{}, businessError(err)
	}
	var state struct {
		Room     string `json:"room"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(setting.PublicValueJSON, &state); err != nil {
		return OLCRTCView{}, businessError(controlplane.ErrUnavailable)
	}
	logins := make([]string, 0, len(setting.Members))
	for login := range setting.Members {
		logins = append(logins, login)
	}
	sort.Strings(logins)
	return OLCRTCView{Room: state.Room, Provider: state.Provider, Logins: logins}, nil
}

func (b *ServiceBusiness) SetOLCRTCRoom(ctx context.Context, command SetOLCRTCRoomCommand) (SettingView, error) {
	if err := b.available(); err != nil {
		return SettingView{}, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return SettingView{}, businessError(controlplane.ErrForbidden)
	}
	setting, err := b.service.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil && !errors.Is(err, controlplane.ErrNotFound) {
		return SettingView{}, businessError(err)
	}
	members := settingMemberKeys(setting.Members)
	if command.Login != "" && !containsString(members, command.Login) {
		members = append(members, command.Login)
	}
	value, _ := json.Marshal(map[string]string{"room": command.Room, "provider": command.Provider})
	result, err := b.service.UpdateSetting(ctx, controlplane.SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: command.ExpectedVersion, PublicValueJSON: string(value), Members: members, Actor: "panel",
		CommandType: "setting.olcrtc.room", IdempotencyKey: command.IdempotencyKey,
		TargetMembers: append([]string(nil), members...),
	})
	if err != nil {
		return SettingView{}, businessError(err)
	}
	return SettingView{Key: "olcrtc", Version: result.Generation, Value: value}, nil
}

func (b *ServiceBusiness) SetOLCRTCGrant(ctx context.Context, command SetOLCRTCGrantCommand) (SettingView, error) {
	if err := b.available(); err != nil {
		return SettingView{}, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return SettingView{}, businessError(controlplane.ErrForbidden)
	}
	setting, err := b.service.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil {
		return SettingView{}, businessError(err)
	}
	members := settingMemberKeys(setting.Members)
	if command.Enabled && !containsString(members, command.Login) {
		members = append(members, command.Login)
	}
	if !command.Enabled {
		members = removeString(members, command.Login)
	}
	result, err := b.service.UpdateSetting(ctx, controlplane.SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: command.ExpectedVersion, PublicValueJSON: string(setting.PublicValueJSON), Members: members, Actor: "panel",
		CommandType: "setting.olcrtc.grant", IdempotencyKey: command.IdempotencyKey,
		TargetMembers: []string{command.Login},
	})
	if err != nil {
		return SettingView{}, businessError(err)
	}
	return SettingView{Key: "olcrtc", Version: result.Generation, Value: setting.PublicValueJSON}, nil
}

func (b *ServiceBusiness) WBTokenStatus(ctx context.Context) (SecretStatusView, error) {
	if err := b.available(); err != nil {
		return SecretStatusView{}, err
	}
	setting, err := b.service.ReadBusinessSetting(ctx, "wbstream")
	if errors.Is(err, controlplane.ErrNotFound) {
		return SecretStatusView{}, nil
	}
	if err != nil {
		return SecretStatusView{}, businessError(err)
	}
	return SecretStatusView{Configured: setting.SecretConfigured}, nil
}

func (b *ServiceBusiness) SetWBToken(ctx context.Context, command SetSecretCommand) error {
	if err := b.available(); err != nil {
		return err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return businessError(controlplane.ErrForbidden)
	}
	_, err := b.service.UpdateSecretSettingIdempotent(ctx, "wbstream", command.Secret, "panel", command.ExpectedVersion, "setting.wbstream.token", command.IdempotencyKey)
	return businessError(err)
}

func (b *ServiceBusiness) RequestWBRoom(ctx context.Context, command RequestWBRoomCommand) (ExternalActionView, error) {
	if err := b.available(); err != nil {
		return ExternalActionView{}, err
	}
	request, _ := json.Marshal(map[string]string{"login": command.Login})
	action, err := b.service.PrepareExternalAction(ctx, controlplane.ExternalActionCommand{
		Type: "wb.room", ResourceID: command.Login, ActionKey: command.ActionKey, Request: request,
	})
	if err != nil {
		return ExternalActionView{}, businessError(err)
	}
	view := ExternalActionView{ID: action.ID, State: action.State}
	if len(action.Response) > 0 {
		var response struct {
			Room string `json:"room"`
		}
		_ = json.Unmarshal(action.Response, &response)
		view.Room = response.Room
	}
	return view, nil
}

func (b *ServiceBusiness) VKTurnState(ctx context.Context) (VKTurnView, error) {
	if err := b.available(); err != nil {
		return VKTurnView{}, err
	}
	setting, err := b.service.ReadBusinessSetting(ctx, "vkturn")
	if errors.Is(err, controlplane.ErrNotFound) {
		return VKTurnView{}, nil
	}
	if err != nil {
		return VKTurnView{}, businessError(err)
	}
	var view VKTurnView
	if err := json.Unmarshal(setting.PublicValueJSON, &view); err != nil {
		return VKTurnView{}, businessError(controlplane.ErrUnavailable)
	}
	return view, nil
}

func (b *ServiceBusiness) UpdateVKTurn(ctx context.Context, command UpdateVKTurnCommand) (SettingView, error) {
	return b.UpdateSetting(ctx, UpdateSettingCommand{Key: "vkturn", ExpectedVersion: command.ExpectedVersion, Value: command.Value, IdempotencyKey: command.IdempotencyKey})
}

func (b *ServiceBusiness) SetVKTurnEnabled(ctx context.Context, command SetVKTurnEnabledCommand) (SettingView, error) {
	if err := b.available(); err != nil {
		return SettingView{}, err
	}
	setting, err := b.service.ReadBusinessSetting(ctx, "vkturn")
	if errors.Is(err, controlplane.ErrNotFound) {
		setting = controlplane.BusinessSetting{PublicValueJSON: json.RawMessage(`{}`)}
	} else if err != nil {
		return SettingView{}, businessError(err)
	}
	var value map[string]any
	if err := json.Unmarshal(setting.PublicValueJSON, &value); err != nil {
		return SettingView{}, businessError(controlplane.ErrUnavailable)
	}
	value["enabled"] = command.Enabled
	raw, _ := json.Marshal(value)
	return b.UpdateSetting(ctx, UpdateSettingCommand{Key: "vkturn", ExpectedVersion: command.ExpectedVersion, Value: raw, IdempotencyKey: command.IdempotencyKey})
}

func (b *ServiceBusiness) ClusterStatus(ctx context.Context) (ClusterStatusView, error) {
	if err := b.available(); err != nil {
		return ClusterStatusView{}, err
	}
	ready, quorum, err := b.service.BusinessClusterStatus(ctx)
	if err != nil {
		return ClusterStatusView{}, businessError(err)
	}
	return ClusterStatusView{Ready: ready, Quorum: quorum}, nil
}

func (b *ServiceBusiness) RecentAudit(ctx context.Context, filter AuditFilter) ([]AuditView, error) {
	if err := b.available(); err != nil {
		return nil, err
	}
	events, err := b.service.RecentBusinessAudit(ctx, filter.Limit)
	if err != nil {
		return nil, businessError(err)
	}
	views := make([]AuditView, 0, len(events))
	for _, event := range events {
		views = append(views, AuditView{ID: event.ID, Actor: "redacted", Action: event.Action, CreatedAt: event.CreatedAt})
	}
	return views, nil
}

func (b *ServiceBusiness) MigrateServiceEndpoint(ctx context.Context, command MigrateEndpointCommand) (OperationView, error) {
	if err := b.available(); err != nil {
		return OperationView{}, err
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return OperationView{}, businessError(controlplane.ErrForbidden)
	}
	count, err := b.service.MigrateBusinessServiceEndpointIdempotent(ctx, command.Service, command.Endpoint, "panel", command.IdempotencyKey)
	if err != nil {
		return OperationView{}, businessError(err)
	}
	return OperationView{ID: command.IdempotencyKey, State: "applied", Count: count}, nil
}

func (b *ServiceBusiness) customerMutation(ctx context.Context, login string, mutate func() (controlplane.Customer, error)) (CustomerView, error) {
	if err := b.available(); err != nil {
		return CustomerView{}, err
	}
	customer, err := mutate()
	if err != nil {
		return CustomerView{}, businessError(err)
	}
	return b.customerView(controlplane.BusinessCustomer{Customer: customer, Login: login}), nil
}

func (b *ServiceBusiness) customerView(customer controlplane.BusinessCustomer) CustomerView {
	protocols := make([]string, 0, len(customer.Access.Credentials))
	for protocol := range customer.Access.Credentials {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	expires := time.Unix(customer.ExpiresAtUnix, 0).UTC()
	active := customer.Status == "active" && expires.After(time.Now())
	return CustomerView{
		Login: customer.Login, SubURL: b.subscriptionURL(customer.Access.SubscriptionToken), Expires: expires,
		Active: active, Protocols: protocols, Generation: customer.Generation,
		Disabled: customer.Status == "suspended" || customer.Status == "deleted",
	}
}

func (b *ServiceBusiness) subscriptionURL(token string) string {
	if token == "" {
		return ""
	}
	return strings.TrimRight(b.cfg.SubBaseURL, "/") + "/sub/" + token
}

func (b *ServiceBusiness) orderView(ctx context.Context, order controlplane.BusinessOrder) OrderView {
	view := OrderView{
		OrderID: order.OrderID, Code: order.Code, RUB: int(order.AmountMinor / 100), Days: order.DurationDays,
		Tariff: order.TariffVersionID, SBPPhone: b.cfg.SBPPhone, PayURL: b.cfg.PayURL,
		Status: string(order.PaymentState), PaymentState: string(order.PaymentState),
		ProvisioningState: order.ProvisioningState, ResultGeneration: order.ResultGeneration,
	}
	if visibility, err := b.service.LegacyOrderVisibility(ctx, order.OrderID); err == nil && visibility == "paid" && order.CustomerID != "" {
		customer, customerErr := b.service.BusinessCustomerByID(ctx, order.CustomerID)
		if customerErr == nil {
			view.SubURL = b.subscriptionURL(customer.Access.SubscriptionToken)
		}
	}
	return view
}

func settingMemberKeys(members map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func removeString(values []string, value string) []string {
	result := values[:0]
	for _, candidate := range values {
		if candidate != value {
			result = append(result, candidate)
		}
	}
	return result
}
