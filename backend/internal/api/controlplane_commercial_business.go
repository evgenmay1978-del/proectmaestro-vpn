package api

import (
	"context"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

var _ CommercialBusiness = (*ServiceBusiness)(nil)

func (b *ServiceBusiness) CommercialCatalog(ctx context.Context) (CommercialCatalogView, error) {
	access, err := b.Tariffs(ctx)
	if err != nil {
		return CommercialCatalogView{}, err
	}
	if len(access) == 0 {
		return CommercialCatalogView{}, businessError(controlplane.ErrNotFound)
	}
	products, err := b.service.WhiteListProducts(ctx)
	if err != nil {
		return CommercialCatalogView{}, businessError(err)
	}
	views := make([]CommercialProductView, 0, len(products))
	for _, product := range products {
		views = append(views, commercialProductView(product))
	}
	return CommercialCatalogView{Access: access[0], Products: views}, nil
}

func (b *ServiceBusiness) CommercialOrderBinding(ctx context.Context, orderID string) (CommercialOrderBindingView, error) {
	if err := b.available(); err != nil {
		return CommercialOrderBindingView{}, err
	}
	order, err := b.service.BusinessOrderByID(ctx, orderID)
	if err != nil {
		return CommercialOrderBindingView{}, businessError(err)
	}
	products, err := b.service.WhiteListProducts(ctx)
	if err != nil {
		return CommercialOrderBindingView{}, businessError(err)
	}
	family := CommercialOrderFamilyAccess
	for _, product := range products {
		if product.ProductID == order.TariffVersionID {
			family = CommercialOrderFamilyWhiteListTopUp
			break
		}
	}
	return CommercialOrderBindingView{OrderID: order.OrderID, Family: family, AccountID: order.CustomerID}, nil
}

func (b *ServiceBusiness) CreateCommercialOrder(ctx context.Context, command CommercialOrderCommand) (CommercialOrderView, error) {
	if err := b.available(); err != nil {
		return CommercialOrderView{}, err
	}
	entitlement, err := b.service.EnsureWhiteListEntitlement(ctx, command.AccountID)
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	order, err := b.service.CreateWhiteListTopUpOrder(ctx, controlplane.CreateWhiteListTopUpOrderCommand{
		EntitlementID: entitlement.EntitlementID(), ProductID: command.ProductID,
		IdempotencyKey: command.IdempotencyKey, BuyerScope: "account", BuyerIdentity: command.AccountID,
		Actor: "public-http", Channel: "public-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	return commercialTopUpOrderView(command.AccountID, order), nil
}

func (b *ServiceBusiness) ClaimCommercialPayment(ctx context.Context, command CommercialClaimCommand) (CommercialOrderView, error) {
	binding, err := b.CommercialOrderBinding(ctx, command.OrderID)
	if err != nil {
		return CommercialOrderView{}, err
	}
	if binding.Family != CommercialOrderFamilyWhiteListTopUp || binding.AccountID != command.AccountID {
		return CommercialOrderView{}, businessError(controlplane.ErrForbidden)
	}
	order, err := b.service.ClaimWhiteListTopUpPayment(ctx, controlplane.ClaimWhiteListTopUpPaymentCommand{
		OrderID: command.OrderID, IdempotencyKey: command.IdempotencyKey,
		Actor: "public-http", Channel: "public-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	return commercialTopUpOrderView(command.AccountID, order), nil
}

func (b *ServiceBusiness) ConfirmCommercialOrder(ctx context.Context, command CommercialOrderDecisionCommand) (CommercialOrderView, error) {
	binding, err := b.CommercialOrderBinding(ctx, command.OrderID)
	if err != nil {
		return CommercialOrderView{}, err
	}
	if binding.Family != CommercialOrderFamilyWhiteListTopUp {
		return CommercialOrderView{}, businessError(controlplane.ErrConflict)
	}
	_, err = b.service.ConfirmWhiteListTopUpPayment(ctx, controlplane.ConfirmWhiteListTopUpPaymentCommand{
		OrderID: command.OrderID, IdempotencyKey: command.IdempotencyKey,
		PaymentReference: command.IdempotencyKey, Provider: "manual-sbp", Actor: command.Actor,
		Channel: "admin-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	return b.commercialOrderFromBusiness(ctx, binding.AccountID, command.OrderID)
}

func (b *ServiceBusiness) RejectCommercialOrder(ctx context.Context, command CommercialOrderDecisionCommand) (CommercialOrderView, error) {
	binding, err := b.CommercialOrderBinding(ctx, command.OrderID)
	if err != nil {
		return CommercialOrderView{}, err
	}
	if binding.Family != CommercialOrderFamilyWhiteListTopUp {
		return CommercialOrderView{}, businessError(controlplane.ErrConflict)
	}
	order, err := b.service.RejectWhiteListTopUpOrder(ctx, controlplane.RejectWhiteListTopUpOrderCommand{
		OrderID: command.OrderID, IdempotencyKey: command.IdempotencyKey,
		Actor: command.Actor, Channel: "admin-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	return commercialTopUpOrderView(binding.AccountID, order), nil
}

func (b *ServiceBusiness) WhiteListBalance(ctx context.Context, accountID string) (WhiteListBalanceView, error) {
	if err := b.available(); err != nil {
		return WhiteListBalanceView{}, err
	}
	entitlement, err := b.service.WhiteListEntitlementByAccountID(ctx, accountID)
	if err != nil {
		return WhiteListBalanceView{}, businessError(err)
	}
	now := b.requestNow()
	snapshot, err := b.service.WhiteListBalanceSnapshot(ctx, now.Unix(), entitlement.EntitlementID())
	if err != nil {
		return WhiteListBalanceView{}, businessError(err)
	}
	publication, err := b.service.WhiteListPublicationState(ctx, entitlement.EntitlementID())
	if err != nil {
		return WhiteListBalanceView{}, businessError(err)
	}
	primaryState := "expired"
	if snapshot.PrimaryActive {
		primaryState = "active"
	}
	verdict := WhiteListPublishable
	switch {
	case !publication.Enabled:
		verdict = WhiteListPublicationDisabled
	case !snapshot.PrimaryActive:
		verdict = WhiteListPrimaryExpired
	case snapshot.Projection.Pending:
		verdict = WhiteListProjectionPending
	case snapshot.Frozen:
		verdict = WhiteListProjectionStale
	case snapshot.AvailableBytes <= 0:
		verdict = WhiteListNoBalance
	}
	return WhiteListBalanceView{
		AccountID:               accountID,
		IncludedRemainingBytes:  snapshot.Projection.IncludedRemainingBytes,
		PurchasedRemainingBytes: snapshot.Projection.PurchasedRemainingBytes,
		AvailableBytes:          snapshot.AvailableBytes, PeriodEndsAtUnix: snapshot.PeriodEndsUnix,
		PrimaryAccessState: primaryState, PublicationVerdict: string(verdict),
	}, nil
}

func (b *ServiceBusiness) SetWhiteListPublication(ctx context.Context, command CommercialPublicationCommand) (CommercialPublicationView, error) {
	if err := b.available(); err != nil {
		return CommercialPublicationView{}, err
	}
	entitlement, err := b.service.WhiteListEntitlementByAccountID(ctx, command.AccountID)
	if err != nil {
		return CommercialPublicationView{}, businessError(err)
	}
	result, err := b.service.SetWhiteListPublication(ctx, controlplane.SetWhiteListPublicationCommand{
		EntitlementID: entitlement.EntitlementID(), Enabled: command.Enabled,
		IdempotencyKey: command.IdempotencyKey, Actor: command.Actor,
		Channel: "admin-http", SourceEventID: command.IdempotencyKey,
	})
	if err != nil {
		return CommercialPublicationView{}, businessError(err)
	}
	return CommercialPublicationView{
		AccountID: command.AccountID, Enabled: result.Enabled, Version: result.Version,
		OperationID: result.OperationID, AuditID: result.ControlID,
	}, nil
}

func (b *ServiceBusiness) SubscriptionDelivery(ctx context.Context, command CommercialDeliveryCommand) (CommercialDeliveryView, error) {
	if err := b.available(); err != nil {
		return CommercialDeliveryView{}, err
	}
	customer, err := b.service.BusinessCustomerByID(ctx, command.AccountID)
	if err != nil {
		return CommercialDeliveryView{}, businessError(err)
	}
	url := b.subscriptionURL(customer.Access.SubscriptionToken)
	if strings.TrimSpace(url) == "" {
		return CommercialDeliveryView{}, businessError(controlplane.ErrNotFound)
	}
	return CommercialDeliveryView{
		AccountID: command.AccountID, Client: command.Client, Format: "TYPED_DESCRIPTOR", URL: url,
	}, nil
}

func (b *ServiceBusiness) commercialOrderFromBusiness(ctx context.Context, accountID, orderID string) (CommercialOrderView, error) {
	order, err := b.service.BusinessOrderByID(ctx, orderID)
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	products, err := b.service.WhiteListProducts(ctx)
	if err != nil {
		return CommercialOrderView{}, businessError(err)
	}
	for _, product := range products {
		if product.ProductID == order.TariffVersionID {
			return CommercialOrderView{
				AccountID: accountID, OrderID: order.OrderID, PaymentCode: order.Code,
				ProductID: product.ProductID, AmountMinor: order.AmountMinor, Currency: order.Currency,
				Bytes: product.Bytes, PaymentState: string(order.PaymentState),
			}, nil
		}
	}
	return CommercialOrderView{}, businessError(controlplane.ErrConflict)
}

func commercialTopUpOrderView(accountID string, order controlplane.WhiteListTopUpOrder) CommercialOrderView {
	return CommercialOrderView{
		AccountID: accountID, OrderID: order.OrderID, PaymentCode: order.PaymentCode,
		ProductID: order.ProductID, AmountMinor: order.AmountMinor, Currency: order.Currency,
		Bytes: order.Bytes, PaymentState: string(order.PaymentState), ExpiresAtUnix: order.ExpiresAtUnix,
	}
}

func commercialProductView(product controlplane.WhiteListProduct) CommercialProductView {
	return CommercialProductView{
		ID: product.ProductID, Kind: product.Kind, AmountMinor: product.AmountMinor,
		Currency: product.Currency, Bytes: product.Bytes, Unit: product.Unit,
	}
}
