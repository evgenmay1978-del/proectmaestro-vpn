package api

import "context"

type dispatchBusiness struct {
	Business
	calls []string
}

func (b *dispatchBusiness) called(name string) { b.calls = append(b.calls, name) }

func (b *dispatchBusiness) CreateSession(context.Context, CreateSessionCommand) (SessionView, error) {
	b.called("create_session")
	return SessionView{Token: "panel-session", CSRF: "panel-csrf"}, nil
}
func (b *dispatchBusiness) Authorize(context.Context, AuthorizeCommand) (PrincipalView, error) {
	b.called("authorize")
	return PrincipalView{ID: "owner", Permissions: []string{"admin"}}, nil
}
func (b *dispatchBusiness) RevokeSessions(context.Context, RevokeSessionsCommand) error {
	b.called("revoke_sessions")
	return nil
}
func (b *dispatchBusiness) ChangePrincipalPassword(context.Context, ChangePasswordCommand) error {
	b.called("change_password")
	return nil
}
func (b *dispatchBusiness) CustomerByLogin(context.Context, string) (CustomerView, error) {
	b.called("customer_by_login")
	return CustomerView{Login: "alice", Active: true}, nil
}
func (b *dispatchBusiness) ListCustomers(context.Context, CustomerFilter) ([]CustomerView, error) {
	b.called("list_customers")
	return []CustomerView{{Login: "alice", Active: true}}, nil
}
func (b *dispatchBusiness) CustomerStats(context.Context) (CustomerStatsView, error) {
	b.called("customer_stats")
	return CustomerStatsView{Total: 1, Active: 1}, nil
}
func (b *dispatchBusiness) CustomerUsage(context.Context, string) (CustomerUsageView, error) {
	b.called("customer_usage")
	return CustomerUsageView{Login: "alice"}, nil
}
func (b *dispatchBusiness) Tariffs(context.Context) ([]TariffView, error) {
	b.called("tariffs")
	return []TariffView{{ID: "month", Days: 30, RUB: 300}}, nil
}
func (b *dispatchBusiness) ApprovedOTA(context.Context) (OTAManifestView, error) {
	b.called("approved_ota")
	return OTAManifestView{Version: "1.0.157"}, nil
}
func (b *dispatchBusiness) CreateOrder(context.Context, CreateOrderCommand) (OrderView, error) {
	b.called("create_order")
	return OrderView{OrderID: "ord", Status: "pending"}, nil
}
func (b *dispatchBusiness) OrderByID(context.Context, string) (OrderView, error) {
	b.called("order_by_id")
	return OrderView{OrderID: "ord", Status: "pending"}, nil
}
func (b *dispatchBusiness) MarkPaymentClaimed(context.Context, ClaimPaymentCommand) (OrderView, error) {
	b.called("mark_payment_claimed")
	return OrderView{OrderID: "ord", Status: "awaiting_confirm"}, nil
}
func (b *dispatchBusiness) ConfirmPayment(context.Context, ConfirmPaymentCommand) (ConfirmPaymentResult, error) {
	b.called("confirm_payment")
	return ConfirmPaymentResult{}, nil
}
func (b *dispatchBusiness) CancelOrder(context.Context, CancelOrderCommand) (OrderView, error) {
	b.called("cancel_order")
	return OrderView{OrderID: "ord", Status: "canceled"}, nil
}
func (b *dispatchBusiness) SubscriptionSnapshot(context.Context, string) (SubscriptionSnapshot, error) {
	b.called("subscription_snapshot")
	return SubscriptionSnapshot{Customer: CustomerView{Login: "alice", Active: true}, Document: []byte(`{}`)}, nil
}
func (b *dispatchBusiness) TouchDevice(context.Context, TouchDeviceCommand) (DeviceDecision, error) {
	b.called("touch_device")
	return DeviceDecision{Allowed: true}, nil
}
func (b *dispatchBusiness) RedeemTrial(context.Context, RedeemTrialCommand) (CustomerView, error) {
	b.called("redeem_trial")
	return CustomerView{Login: "trial-alice", Active: true}, nil
}
func (b *dispatchBusiness) ProvisionCustomer(context.Context, ProvisionCustomerCommand) (CustomerView, error) {
	b.called("provision")
	return CustomerView{Login: "alice", Active: true}, nil
}
func (b *dispatchBusiness) ExtendCustomer(context.Context, ExtendCustomerCommand) (CustomerView, error) {
	b.called("extend")
	return CustomerView{Login: "alice", Active: true}, nil
}
func (b *dispatchBusiness) RenewCustomer(context.Context, RenewCustomerCommand) (CustomerView, error) {
	b.called("renew")
	return CustomerView{Login: "alice", Active: true}, nil
}
func (b *dispatchBusiness) SetCustomerExpiry(context.Context, SetExpiryCommand) (CustomerView, error) {
	b.called("set_expiry")
	return CustomerView{Login: "alice", Active: true}, nil
}
func (b *dispatchBusiness) DisableCustomer(context.Context, CustomerStateCommand) (CustomerView, error) {
	b.called("disable")
	return CustomerView{Login: "alice", Disabled: true}, nil
}
func (b *dispatchBusiness) EnableCustomer(context.Context, CustomerStateCommand) (CustomerView, error) {
	b.called("enable")
	return CustomerView{Login: "alice", Active: true}, nil
}
func (b *dispatchBusiness) DeleteCustomer(context.Context, DeleteCustomerCommand) error {
	b.called("delete")
	return nil
}
func (b *dispatchBusiness) RunExpirySweep(context.Context, ExpirySweepCommand) (OperationView, error) {
	b.called("expiry_sweep")
	return OperationView{ID: "sweep", State: "accepted"}, nil
}
func (b *dispatchBusiness) ResetDevices(context.Context, ResetDevicesCommand) error {
	b.called("reset_devices")
	return nil
}
func (b *dispatchBusiness) ReconcileServices(_ context.Context, command ReconcileServicesCommand) (OperationView, error) {
	b.called("reconcile:" + command.Service)
	return OperationView{ID: "reconcile", State: "accepted"}, nil
}
func (b *dispatchBusiness) OLCRTCState(context.Context) (OLCRTCView, error) {
	b.called("olcrtc_state")
	return OLCRTCView{}, nil
}
func (b *dispatchBusiness) SetOLCRTCRoom(context.Context, SetOLCRTCRoomCommand) (SettingView, error) {
	b.called("olcrtc_room")
	return SettingView{Key: "olcrtc"}, nil
}
func (b *dispatchBusiness) SetOLCRTCGrant(context.Context, SetOLCRTCGrantCommand) (SettingView, error) {
	b.called("olcrtc_grant")
	return SettingView{Key: "olcrtc"}, nil
}
func (b *dispatchBusiness) WBTokenStatus(context.Context) (SecretStatusView, error) {
	b.called("wbtoken_status")
	return SecretStatusView{Configured: true}, nil
}
func (b *dispatchBusiness) SetWBToken(context.Context, SetSecretCommand) error {
	b.called("set_wbtoken")
	return nil
}
func (b *dispatchBusiness) RequestWBRoom(context.Context, RequestWBRoomCommand) (ExternalActionView, error) {
	b.called("request_wbroom")
	return ExternalActionView{ID: "room", State: "pending"}, nil
}
func (b *dispatchBusiness) VKTurnState(context.Context) (VKTurnView, error) {
	b.called("vkturn_state")
	return VKTurnView{}, nil
}
func (b *dispatchBusiness) UpdateVKTurn(context.Context, UpdateVKTurnCommand) (SettingView, error) {
	b.called("update_vkturn")
	return SettingView{Key: "vkturn"}, nil
}
func (b *dispatchBusiness) SetVKTurnEnabled(context.Context, SetVKTurnEnabledCommand) (SettingView, error) {
	b.called("set_vkturn_enabled")
	return SettingView{Key: "vkturn"}, nil
}
func (b *dispatchBusiness) MigrateServiceEndpoint(context.Context, MigrateEndpointCommand) (OperationView, error) {
	b.called("migrate_endpoint")
	return OperationView{ID: "migrate", State: "accepted"}, nil
}

func containsDispatchCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}
