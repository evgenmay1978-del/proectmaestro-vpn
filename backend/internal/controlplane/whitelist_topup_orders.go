package controlplane

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

const (
	whiteListTopUpCreateCommand    = "whitelist_topup_create"
	whiteListTopUpClaimCommand     = "whitelist_topup_claim"
	whiteListTopUpConfirmCommand   = "whitelist_topup_confirm"
	whiteListTopUpRejectCommand    = "whitelist_topup_reject"
	whiteListPublicationSetCommand = "whitelist_publication_set"
)

type CreateWhiteListTopUpOrderCommand struct {
	EntitlementID  string
	ProductID      string
	IdempotencyKey string
	BuyerScope     string
	BuyerIdentity  string
	OriginBotID    string
	ChatIdentity   string
	Actor          string
	Channel        string
	SourceEventID  string
}

type WhiteListTopUpOrder struct {
	OrderID       string       `json:"order_id"`
	PaymentCode   string       `json:"payment_code"`
	EntitlementID string       `json:"entitlement_id"`
	ProductID     string       `json:"product_id"`
	AmountMinor   int64        `json:"amount_minor"`
	Currency      string       `json:"currency"`
	Bytes         int64        `json:"bytes"`
	PaymentState  PaymentState `json:"payment_state"`
	ExpiresAtUnix int64        `json:"expires_at_unix"`
}

type ClaimWhiteListTopUpPaymentCommand struct {
	OrderID        string
	IdempotencyKey string
	Actor          string
	Channel        string
	SourceEventID  string
}

type RejectWhiteListTopUpOrderCommand struct {
	OrderID        string
	IdempotencyKey string
	Actor          string
	Channel        string
	SourceEventID  string
}

type ConfirmWhiteListTopUpPaymentCommand struct {
	OrderID          string
	IdempotencyKey   string
	PaymentReference string
	Provider         string
	Actor            string
	Channel          string
	SourceEventID    string
}

type ConfirmWhiteListTopUpPaymentResult struct {
	OrderID                 string `json:"order_id"`
	OperationID             string `json:"operation_id"`
	PaymentID               string `json:"payment_id"`
	PeriodID                string `json:"period_id"`
	BalanceEntryID          string `json:"balance_entry_id"`
	ControlID               string `json:"control_id"`
	PurchasedBytes          int64  `json:"purchased_bytes"`
	PurchasedRemainingBytes int64  `json:"purchased_remaining_bytes"`
	BalanceVersion          int64  `json:"balance_version"`
	PublicationEnabled      bool   `json:"publication_enabled"`
}

type SetWhiteListPublicationCommand struct {
	EntitlementID  string
	Enabled        bool
	IdempotencyKey string
	Actor          string
	Channel        string
	SourceEventID  string
}

type WhiteListPublicationResult struct {
	EntitlementID string `json:"entitlement_id"`
	ControlID     string `json:"control_id"`
	OperationID   string `json:"operation_id,omitempty"`
	Version       int64  `json:"version"`
	Enabled       bool   `json:"enabled"`
	Source        string `json:"source"`
}

type whiteListTopUpPrepared struct {
	CustomerID      string
	CustomerStatus  string
	CustomerExpires int64
	Product         WhiteListProduct
}

type whiteListTopUpRecord struct {
	View           WhiteListTopUpOrder
	OriginBotID    string
	OriginChatHMAC string
}

func (s *Service) CreateWhiteListTopUpOrder(
	ctx context.Context,
	command CreateWhiteListTopUpOrderCommand,
) (WhiteListTopUpOrder, error) {
	if s == nil || s.store == nil || s.store.db == nil ||
		!validWhiteListID(command.EntitlementID) || !validWhiteListID(command.ProductID) ||
		!validWhiteListID(command.IdempotencyKey) || strings.TrimSpace(command.BuyerScope) == "" {
		return WhiteListTopUpOrder{}, ErrConflict
	}
	buyerMaterial := strings.TrimSpace(command.BuyerIdentity)
	if buyerMaterial == "" {
		buyerMaterial = command.IdempotencyKey
	}
	buyerHMAC := s.store.secrets.LookupHMAC("order-buyer:"+command.BuyerScope, []byte(buyerMaterial))
	requestHash, err := whiteListCanonicalHash(struct {
		Version       int    `json:"version"`
		CommandType   string `json:"command_type"`
		EntitlementID string `json:"entitlement_id"`
		ProductID     string `json:"product_id"`
		BuyerScope    string `json:"buyer_scope"`
		BuyerKeyHMAC  string `json:"buyer_key_hmac"`
	}{1, whiteListTopUpCreateCommand, command.EntitlementID, command.ProductID, command.BuyerScope, buyerHMAC})
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	if saved, found, resolveErr := s.resolveWhiteListTopUpOrder(
		ctx, whiteListTopUpScope(command.EntitlementID), whiteListTopUpCreateCommand,
		command.IdempotencyKey, requestHash,
	); found || resolveErr != nil {
		return saved, resolveErr
	}

	nowUnix := s.clock.Now().Unix()
	prepared, err := s.prepareWhiteListTopUp(ctx, nowUnix, command.EntitlementID, command.ProductID)
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	orderID, err := s.ids.NewID("whitelist-topup-order")
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	operationID, err := s.ids.NewID("whitelist-topup-create")
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	paymentCode, err := s.ids.NewID("payment-code")
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	expiresAtUnix := nowUnix + 86400
	result := WhiteListTopUpOrder{
		OrderID: orderID, PaymentCode: paymentCode, EntitlementID: command.EntitlementID,
		ProductID: command.ProductID, AmountMinor: prepared.Product.AmountMinor,
		Currency: prepared.Product.Currency, Bytes: prepared.Product.Bytes,
		PaymentState: PaymentPending, ExpiresAtUnix: expiresAtUnix,
	}
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	var chatHMAC any
	if command.ChatIdentity != "" {
		chatHMAC = s.store.secrets.LookupHMAC("order-origin-chat", []byte(command.ChatIdentity))
	}
	auditEventID := auditID("whitelist-topup-create", orderID, 0, nowUnix)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID,
	})
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	scope := whiteListTopUpScope(command.EntitlementID)
	statements := []rqlite.Statement{
		{
			SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,
response_json,created_at_unix,applied_at_unix)
VALUES(?,'whitelist_topup_create',?,?,?,'order_created',?,'applying',NULL,?,NULL)`,
			Args: []any{scope, command.IdempotencyKey, requestHash, orderID, operationID, nowUnix},
		},
		{
			SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,operation_id,origin_bot_id,origin_chat_key_hmac)
SELECT ?,?,?,?,?,t.tariff_version_id,t.amount_minor,t.currency,t.duration_days,?,?,'created',
'none',NULL,?,?,?
FROM whitelist_entitlement_identities AS entitlement
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
JOIN whitelist_gb_products AS product ON product.product_id=?
JOIN tariff_versions AS t ON t.tariff_version_id=product.product_id
WHERE entitlement.entitlement_id=? AND entitlement.customer_id=?
  AND customer.status='active' AND customer.expires_at_unix>?`,
			Args: []any{
				orderID, paymentCode, command.BuyerScope, buyerHMAC, prepared.CustomerID,
				nowUnix, expiresAtUnix, operationID, command.OriginBotID, chatHMAC,
				command.ProductID, command.EntitlementID, prepared.CustomerID, nowUnix,
			},
		},
		{
			SQL: `INSERT INTO whitelist_topup_orders(
order_id,entitlement_id,product_id,creation_request_hash,created_at_unix)
SELECT ?,?,?,?,? WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='created')`,
			Args: []any{orderID, command.EntitlementID, command.ProductID, requestHash, nowUnix, orderID, operationID},
		},
		backupRPODirtyGenerationStatement(nowUnix),
		{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'whitelist.topup.create','order',?,?,?,? WHERE EXISTS(
SELECT 1 FROM whitelist_topup_orders WHERE order_id=?)`,
			Args: []any{
				auditEventID, s.auditActor(command.Actor), s.auditResource(orderID), auditEnvelope,
				auditDigest, nowUnix, orderID,
			},
		},
		{
			SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type='whitelist_topup_create' AND idempotency_key=?
  AND request_hash=? AND operation_id=? AND status='applying'`,
			Args: []any{string(responseJSON), nowUnix, scope, command.IdempotencyKey, requestHash, operationID},
		},
	}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return result, nil
	}
	if saved, found, resolveErr := s.resolveWhiteListTopUpOrder(
		ctx, scope, whiteListTopUpCreateCommand, command.IdempotencyKey, requestHash,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	if isUnknownWrite(requestErr) {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	return WhiteListTopUpOrder{}, ErrConflict
}

func (s *Service) ClaimWhiteListTopUpPayment(
	ctx context.Context,
	command ClaimWhiteListTopUpPaymentCommand,
) (WhiteListTopUpOrder, error) {
	if !validWhiteListID(command.OrderID) || !validWhiteListID(command.IdempotencyKey) {
		return WhiteListTopUpOrder{}, ErrConflict
	}
	requestHash, err := whiteListTopUpDecisionHash(whiteListTopUpClaimCommand, command.OrderID)
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	scope := "order:" + command.OrderID
	if saved, found, resolveErr := s.resolveWhiteListTopUpOrder(
		ctx, scope, whiteListTopUpClaimCommand, command.IdempotencyKey, requestHash,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	record, err := s.queryWhiteListTopUpOrder(ctx, command.OrderID)
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	if record.View.PaymentState == PaymentClaimed {
		return record.View, nil
	}
	if record.View.PaymentState != PaymentPending || record.View.ExpiresAtUnix <= s.clock.Now().Unix() {
		return WhiteListTopUpOrder{}, ErrConflict
	}
	operationID, err := s.ids.NewID("whitelist-topup-claim")
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	result := record.View
	result.PaymentState = PaymentClaimed
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	nowUnix := s.clock.Now().Unix()
	auditEventID := auditID("whitelist-topup-claim", command.OrderID, 0, nowUnix)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID, ResultHash: requestHash,
	})
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	delivery, err := s.telegramDelivery(
		"owner-whitelist-topup-claim:"+command.OrderID, command.OrderID,
		"owner_whitelist_topup_payment_claim", record.OriginBotID, record.OriginChatHMAC,
	)
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	statements := []rqlite.Statement{
		{
			SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,
response_json,created_at_unix,applied_at_unix)
VALUES(?,'whitelist_topup_claim',?,?,?,'payment_claimed',?,'applying',NULL,?,NULL)`,
			Args: []any{scope, command.IdempotencyKey, requestHash, command.OrderID, operationID, nowUnix},
		},
		{
			SQL: `INSERT INTO whitelist_topup_payment_claims(
order_id,operation_id,request_hash,claimed_at_unix)
SELECT ?,?,?,? WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND payment_state='created' AND decision IS NULL
AND expires_at_unix>?)`,
			Args: []any{command.OrderID, operationID, requestHash, nowUnix, command.OrderID, nowUnix},
		},
		{
			SQL: `UPDATE orders SET payment_state='payment_claimed',operation_id=?
WHERE order_id=? AND payment_state='created' AND decision IS NULL AND expires_at_unix>?
AND EXISTS(SELECT 1 FROM whitelist_topup_payment_claims
WHERE order_id=? AND operation_id=?)`,
			Args: []any{operationID, command.OrderID, nowUnix, command.OrderID, operationID},
		},
		backupRPODirtyGenerationStatement(nowUnix),
	}
	if delivery != nil {
		statements = append(statements, *delivery)
	}
	statements = append(statements,
		rqlite.Statement{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'whitelist.topup.payment_claimed','order',?,?,?,? WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='payment_claimed')`,
			Args: []any{
				auditEventID, s.auditActor(command.Actor), s.auditResource(command.OrderID), auditEnvelope,
				auditDigest, nowUnix, command.OrderID, operationID,
			},
		},
		rqlite.Statement{
			SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type='whitelist_topup_claim' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying'`,
			Args: []any{string(responseJSON), nowUnix, scope, command.IdempotencyKey, requestHash, operationID},
		},
	)
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return result, nil
	}
	if saved, found, resolveErr := s.resolveWhiteListTopUpOrder(
		ctx, scope, whiteListTopUpClaimCommand, command.IdempotencyKey, requestHash,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	if refreshed, lookupErr := s.queryWhiteListTopUpOrder(ctx, command.OrderID); lookupErr == nil && refreshed.View.PaymentState == PaymentClaimed {
		return refreshed.View, nil
	}
	if isUnknownWrite(requestErr) {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	return WhiteListTopUpOrder{}, ErrConflict
}

func (s *Service) RejectWhiteListTopUpOrder(
	ctx context.Context,
	command RejectWhiteListTopUpOrderCommand,
) (WhiteListTopUpOrder, error) {
	if !validWhiteListID(command.OrderID) || !validWhiteListID(command.IdempotencyKey) {
		return WhiteListTopUpOrder{}, ErrConflict
	}
	requestHash, err := whiteListTopUpDecisionHash(whiteListTopUpRejectCommand, command.OrderID)
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	scope := "order:" + command.OrderID
	if saved, found, resolveErr := s.resolveWhiteListTopUpOrder(
		ctx, scope, whiteListTopUpRejectCommand, command.IdempotencyKey, requestHash,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	record, err := s.queryWhiteListTopUpOrder(ctx, command.OrderID)
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	if record.View.PaymentState == PaymentCanceled {
		return record.View, nil
	}
	if record.View.PaymentState != PaymentPending && record.View.PaymentState != PaymentClaimed {
		return WhiteListTopUpOrder{}, ErrConflict
	}
	operationID, err := s.ids.NewID("whitelist-topup-reject")
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	result := record.View
	result.PaymentState = PaymentCanceled
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	nowUnix := s.clock.Now().Unix()
	auditEventID := auditID("whitelist-topup-reject", command.OrderID, 0, nowUnix)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID, ResultHash: requestHash,
	})
	if err != nil {
		return WhiteListTopUpOrder{}, err
	}
	statements := []rqlite.Statement{
		{
			SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,
response_json,created_at_unix,applied_at_unix)
VALUES(?,'whitelist_topup_reject',?,?,?,'order_rejected',?,'applying',NULL,?,NULL)`,
			Args: []any{scope, command.IdempotencyKey, requestHash, command.OrderID, operationID, nowUnix},
		},
		{
			SQL: `UPDATE orders SET payment_state='canceled',provisioning_state='none',
decision='cancelled',operation_id=?
WHERE order_id=? AND payment_state IN ('created','payment_claimed') AND decision IS NULL
AND EXISTS(SELECT 1 FROM idempotency_requests WHERE scope=?
AND command_type='whitelist_topup_reject' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying')`,
			Args: []any{operationID, command.OrderID, scope, command.IdempotencyKey, requestHash, operationID},
		},
		backupRPODirtyGenerationStatement(nowUnix),
		{
			SQL: `INSERT INTO whitelist_topup_results(
order_id,decision,operation_id,request_hash,payment_reference_hmac,payment_id,
period_id,balance_entry_id,control_id,created_at_unix)
SELECT ?,'REJECTED',?,?,NULL,NULL,NULL,NULL,NULL,? WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='canceled')`,
			Args: []any{command.OrderID, operationID, requestHash, nowUnix, command.OrderID, operationID},
		},
		{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'whitelist.topup.reject','order',?,?,?,? WHERE EXISTS(
SELECT 1 FROM whitelist_topup_results WHERE order_id=? AND operation_id=? AND decision='REJECTED')`,
			Args: []any{
				auditEventID, s.auditActor(command.Actor), s.auditResource(command.OrderID), auditEnvelope,
				auditDigest, nowUnix, command.OrderID, operationID,
			},
		},
		{
			SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type='whitelist_topup_reject' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying'`,
			Args: []any{string(responseJSON), nowUnix, scope, command.IdempotencyKey, requestHash, operationID},
		},
	}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return result, nil
	}
	if saved, found, resolveErr := s.resolveWhiteListTopUpOrder(
		ctx, scope, whiteListTopUpRejectCommand, command.IdempotencyKey, requestHash,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	if isUnknownWrite(requestErr) {
		return WhiteListTopUpOrder{}, ErrUnavailable
	}
	return WhiteListTopUpOrder{}, ErrConflict
}

func (s *Service) ConfirmWhiteListTopUpPayment(
	ctx context.Context,
	command ConfirmWhiteListTopUpPaymentCommand,
) (ConfirmWhiteListTopUpPaymentResult, error) {
	if !validWhiteListID(command.OrderID) || !validWhiteListID(command.IdempotencyKey) ||
		strings.TrimSpace(command.PaymentReference) == "" {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
	}
	provider := strings.TrimSpace(command.Provider)
	if provider == "" {
		provider = "manual"
	}
	paymentReferenceHMAC := s.store.secrets.LookupHMAC(
		"whitelist-topup-payment-reference", []byte(command.PaymentReference),
	)
	requestHash, err := whiteListCanonicalHash(struct {
		Version              int    `json:"version"`
		CommandType          string `json:"command_type"`
		OrderID              string `json:"order_id"`
		Provider             string `json:"provider"`
		PaymentReferenceHMAC string `json:"payment_reference_hmac"`
	}{1, whiteListTopUpConfirmCommand, command.OrderID, provider, paymentReferenceHMAC})
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	scope := "order:" + command.OrderID
	if saved, found, resolveErr := s.resolveWhiteListTopUpConfirmation(
		ctx, scope, command.IdempotencyKey, requestHash, command.OrderID,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	record, err := s.queryWhiteListTopUpOrder(ctx, command.OrderID)
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, err
	}
	if record.View.PaymentState != PaymentClaimed {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
	}
	nowUnix := s.clock.Now().Unix()
	loaded, err := s.loadWhiteListBalance(ctx, nowUnix, record.View.EntitlementID)
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, err
	}
	if loaded.RenewalPending {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	if !loaded.PrimaryActive || loaded.CommercialPending {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
	}
	creditState := loaded.State
	var scheduledPeriod *whitelistbalance.Transition
	period, ok := activeWhiteListTopUpPeriod(loaded.State.Periods, nowUnix)
	if !ok {
		accessOrderID, periodEndsAtUnix, accessErr := s.whiteListTopUpAccessOrder(
			ctx, record.View.EntitlementID, nowUnix,
		)
		if accessErr != nil {
			return ConfirmWhiteListTopUpPaymentResult{}, accessErr
		}
		periodID, idErr := s.ids.NewID("whitelist-period")
		if idErr != nil {
			return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
		}
		periodOperationID, idErr := s.ids.NewID("whitelist-topup-period")
		if idErr != nil {
			return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
		}
		period, err = newWhiteListTopUpPeriod(
			loaded.State.Periods, periodID, accessOrderID, nowUnix, periodEndsAtUnix,
		)
		if err != nil {
			return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
		}
		scheduled, scheduleErr := whitelistbalance.SchedulePeriod(
			loaded.State,
			whitelistbalance.SchedulePeriodRequest{
				OperationID: periodOperationID, NowUnix: nowUnix, Period: period,
			},
			nil,
		)
		if scheduleErr != nil || len(scheduled.Journal) != 0 || scheduled.Result.Projection.Pending {
			return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
		}
		scheduledPeriod = &scheduled
		creditState = scheduled.State
	}
	operationID, err := s.ids.NewID("whitelist-topup-confirm")
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	paymentID, err := s.ids.NewID("whitelist-topup-payment")
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	balanceEntryID, err := s.ids.NewID("whitelist-entry")
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	controlID, err := s.ids.NewID("whitelist-publication-control")
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	transition, err := whitelistbalance.CreditPurchased(creditState, whitelistbalance.CreditPurchasedRequest{
		OperationID: operationID, PeriodID: period.ID, SourceOrderID: command.OrderID,
		NowUnix: nowUnix, Bytes: record.View.Bytes, PrimaryActive: loaded.PrimaryActive,
	}, nil)
	if err != nil || len(transition.Journal) != 1 ||
		transition.Journal[0].Kind != whitelistbalance.EntryPurchasedCredit ||
		transition.Result.Projection.Pending {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
	}
	result := ConfirmWhiteListTopUpPaymentResult{
		OrderID: command.OrderID, OperationID: operationID, PaymentID: paymentID,
		PeriodID: period.ID, BalanceEntryID: balanceEntryID, ControlID: controlID,
		PurchasedBytes:          record.View.Bytes,
		PurchasedRemainingBytes: transition.Result.Projection.PurchasedRemainingBytes,
		BalanceVersion:          transition.Result.Projection.Version, PublicationEnabled: true,
	}
	responseJSON, err := json.Marshal(result)
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	intent := transition.Journal[0]
	metadataSHA256, err := whiteListCanonicalHash(intent)
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	entryKey := whiteListJournalKey(record.View.EntitlementID, intent)
	auditEventID := auditID("whitelist-topup-confirm", command.OrderID, result.BalanceVersion, nowUnix)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID, ResultHash: requestHash,
	})
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, err
	}
	guard := `EXISTS(SELECT 1 FROM idempotency_requests AS request
WHERE request.scope=? AND request.command_type='whitelist_topup_confirm'
AND request.idempotency_key=? AND request.request_hash=? AND request.resource_id=?
AND request.operation_id=? AND request.status='applying')
AND EXISTS(SELECT 1 FROM whitelist_topup_orders AS topup
JOIN orders AS source_order ON source_order.order_id=topup.order_id
JOIN whitelist_entitlement_identities AS entitlement ON entitlement.entitlement_id=topup.entitlement_id
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
WHERE topup.order_id=? AND topup.entitlement_id=?
AND source_order.payment_state='confirmed' AND source_order.decision='confirmed'
AND source_order.operation_id=? AND customer.status='active' AND customer.expires_at_unix>?)
AND NOT EXISTS(SELECT 1 FROM whitelist_renewal_intents AS renewal_intent
WHERE renewal_intent.entitlement_id=? AND renewal_intent.status='pending')
AND NOT EXISTS(SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
WHERE debit_outbox.entitlement_id=? AND NOT EXISTS(
SELECT 1 FROM idempotency_requests AS debit_receipt
WHERE debit_receipt.scope=? AND debit_receipt.command_type=?
AND debit_receipt.idempotency_key=debit_outbox.receipt_key
AND debit_receipt.request_hash=debit_outbox.request_hash
AND debit_receipt.resource_id=debit_outbox.entitlement_id
AND debit_receipt.status='applied'))`
	guardArgs := []any{
		scope, command.IdempotencyKey, requestHash, command.OrderID, operationID,
		command.OrderID, record.View.EntitlementID, operationID, nowUnix,
		record.View.EntitlementID,
		record.View.EntitlementID, whitelistmetering.CommercialDebitReceiptScope,
		whitelistmetering.CommercialDebitReceiptCommand,
	}
	statements := []rqlite.Statement{
		{
			SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,
response_json,created_at_unix,applied_at_unix)
VALUES(?,'whitelist_topup_confirm',?,?,?,'payment_confirmed',?,'applying',NULL,?,NULL)`,
			Args: []any{scope, command.IdempotencyKey, requestHash, command.OrderID, operationID, nowUnix},
		},
		{
			SQL: `UPDATE orders SET payment_state='confirmed',
decision='confirmed',confirmed_at_unix=?,operation_id=?
WHERE order_id=? AND payment_state='payment_claimed' AND decision IS NULL
AND EXISTS(SELECT 1 FROM idempotency_requests WHERE scope=?
AND command_type='whitelist_topup_confirm' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying')`,
			Args: []any{
				nowUnix, operationID, command.OrderID, scope, command.IdempotencyKey,
				requestHash, operationID,
			},
		},
		{
			SQL: `INSERT INTO payments(
payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
SELECT ?,source_order.order_id,?,?,NULL,source_order.amount_minor,source_order.currency,?
FROM orders AS source_order WHERE source_order.order_id=?
AND source_order.payment_state='confirmed' AND source_order.decision='confirmed'
AND source_order.operation_id=?`,
			Args: []any{paymentID, provider, paymentReferenceHMAC, nowUnix, command.OrderID, operationID},
		},
	}
	if scheduledPeriod != nil {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix)
SELECT ?,?,?,?,?,0,?,? WHERE ` + guard,
			Args: append([]any{
				period.ID, record.View.EntitlementID, period.Ordinal, period.StartsAtUnix,
				period.EndsAtUnix, period.AccessOrderID, nowUnix,
			}, guardArgs...),
		})
	}
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO whitelist_balance_entries(
entry_id,entitlement_id,period_id,kind,included_delta_bytes,purchased_delta_bytes,
consumed_delta_bytes,uncovered_delta_bytes,source_order_id,interval_id,
idempotency_key,metadata_sha256,created_at_unix)
SELECT ?,?,?, 'PURCHASED_CREDIT',0,?,0,0,?,NULL,?,?,? WHERE ` + guard,
		Args: append([]any{
			balanceEntryID, record.View.EntitlementID, period.ID, record.View.Bytes,
			command.OrderID, entryKey, metadataSHA256, nowUnix,
		}, guardArgs...),
	})
	projectionBeforeCredit := loaded.State.Projection
	if scheduledPeriod != nil {
		if err := appendWhiteListTopUpProjectionCASProof(
			&statements, loaded.State.Projection, scheduledPeriod.Result.Projection,
			nowUnix, guard, guardArgs, scope, command.IdempotencyKey, requestHash, operationID,
		); err != nil {
			return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
		}
		projectionBeforeCredit = scheduledPeriod.State.Projection
	}
	if err := appendWhiteListTopUpProjectionCASProof(
		&statements, projectionBeforeCredit, transition.Result.Projection,
		nowUnix, guard, guardArgs, scope, command.IdempotencyKey, requestHash, operationID,
	); err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	statements = append(statements,
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix)
SELECT ?,?,(SELECT COALESCE(MAX(version),0)+1 FROM whitelist_publication_controls
WHERE entitlement_id=?),1,'CONFIRMED_GB_PURCHASE',?,?,?,? WHERE ` + guard,
			Args: append([]any{
				controlID, record.View.EntitlementID, record.View.EntitlementID,
				command.OrderID, operationID, requestHash, nowUnix,
			}, guardArgs...),
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_topup_results(
order_id,decision,operation_id,request_hash,payment_reference_hmac,payment_id,
period_id,balance_entry_id,control_id,created_at_unix)
SELECT ?,'CONFIRMED',?,?,?,?,?,?,?,? WHERE ` + guard,
			Args: append([]any{
				command.OrderID, operationID, requestHash, paymentReferenceHMAC, paymentID,
				period.ID, balanceEntryID, controlID, nowUnix,
			}, guardArgs...),
		},
		rqlite.Statement{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'whitelist.topup.confirm','order',?,?,?,? WHERE EXISTS(
SELECT 1 FROM whitelist_topup_results WHERE order_id=? AND operation_id=? AND decision='CONFIRMED')`,
			Args: []any{
				auditEventID, s.auditActor(command.Actor), s.auditResource(command.OrderID), auditEnvelope,
				auditDigest, nowUnix, command.OrderID, operationID,
			},
		},
		rqlite.Statement{
			SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type='whitelist_topup_confirm' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying'`,
			Args: []any{string(responseJSON), nowUnix, scope, command.IdempotencyKey, requestHash, operationID},
		},
	)
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return result, nil
	}
	if saved, found, resolveErr := s.resolveWhiteListTopUpConfirmation(
		ctx, scope, command.IdempotencyKey, requestHash, command.OrderID,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	if reloaded, reloadErr := s.loadWhiteListBalance(
		ctx, nowUnix, record.View.EntitlementID,
	); reloadErr == nil && reloaded.RenewalPending {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	if isUnknownWrite(requestErr) {
		return ConfirmWhiteListTopUpPaymentResult{}, ErrUnavailable
	}
	return ConfirmWhiteListTopUpPaymentResult{}, ErrConflict
}

func (s *Service) SetWhiteListPublication(
	ctx context.Context,
	command SetWhiteListPublicationCommand,
) (WhiteListPublicationResult, error) {
	if !validWhiteListID(command.EntitlementID) || !validWhiteListID(command.IdempotencyKey) {
		return WhiteListPublicationResult{}, ErrConflict
	}
	requestHash, err := whiteListCanonicalHash(struct {
		Version       int    `json:"version"`
		CommandType   string `json:"command_type"`
		EntitlementID string `json:"entitlement_id"`
		Enabled       bool   `json:"enabled"`
	}{1, whiteListPublicationSetCommand, command.EntitlementID, command.Enabled})
	if err != nil {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	scope := "whitelist-publication:" + command.EntitlementID
	if saved, found, resolveErr := s.resolveWhiteListPublication(
		ctx, scope, command.IdempotencyKey, requestHash, command.EntitlementID,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	current, err := s.currentWhiteListPublication(ctx, command.EntitlementID)
	if err != nil {
		return WhiteListPublicationResult{}, err
	}
	nowUnix := s.clock.Now().Unix()
	if command.Enabled {
		loaded, loadErr := s.loadWhiteListBalance(ctx, nowUnix, command.EntitlementID)
		if loadErr != nil {
			return WhiteListPublicationResult{}, loadErr
		}
		snapshot, snapshotErr := whitelistbalance.Snapshot(loaded.State, loaded.PrimaryActive)
		if snapshotErr != nil || !loaded.PrimaryActive || loaded.CommercialPending ||
			snapshot.Frozen || snapshot.UsableBytes <= 0 || snapshot.Projection.Pending {
			return WhiteListPublicationResult{}, ErrConflict
		}
	}
	operationID, err := s.ids.NewID("whitelist-publication-set")
	if err != nil {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	controlID, err := s.ids.NewID("whitelist-publication-control")
	if err != nil {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	source := "ADMIN_DISABLE"
	if command.Enabled {
		source = "ADMIN_ENABLE"
	}
	result := WhiteListPublicationResult{
		EntitlementID: command.EntitlementID, ControlID: controlID, OperationID: operationID,
		Version: current.Version + 1, Enabled: command.Enabled, Source: source,
	}
	responseJSON, err := json.Marshal(result)
	if err != nil || result.Version <= current.Version {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	auditEventID := auditID("whitelist-publication-set", command.EntitlementID, result.Version, nowUnix)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID, ResultHash: requestHash,
	})
	if err != nil {
		return WhiteListPublicationResult{}, err
	}
	enableGuard := ""
	enableGuardArgs := []any(nil)
	if command.Enabled {
		enableGuard = ` AND EXISTS(
SELECT 1 FROM whitelist_entitlement_identities AS entitlement
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=entitlement.entitlement_id
JOIN whitelist_billing_periods AS period ON period.period_id=projection.current_period_id
WHERE entitlement.entitlement_id=? AND customer.status='active' AND customer.expires_at_unix>?
AND period.starts_at_unix<=? AND period.ends_at_unix>?
AND projection.pending=0
AND projection.included_remaining_bytes+projection.purchased_remaining_bytes>0
AND NOT EXISTS(SELECT 1 FROM whitelist_commercial_debit_outbox AS debit_outbox
WHERE debit_outbox.entitlement_id=entitlement.entitlement_id AND NOT EXISTS(
SELECT 1 FROM idempotency_requests AS debit_receipt
WHERE debit_receipt.scope=? AND debit_receipt.command_type=?
AND debit_receipt.idempotency_key=debit_outbox.receipt_key
AND debit_receipt.request_hash=debit_outbox.request_hash
AND debit_receipt.resource_id=debit_outbox.entitlement_id
AND debit_receipt.status='applied')))`
		enableGuardArgs = []any{
			command.EntitlementID, nowUnix, nowUnix, nowUnix,
			whitelistmetering.CommercialDebitReceiptScope, whitelistmetering.CommercialDebitReceiptCommand,
		}
	}
	statements := []rqlite.Statement{
		{
			SQL: `INSERT INTO idempotency_requests(
scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,
response_json,created_at_unix,applied_at_unix)
VALUES(?,'whitelist_publication_set',?,?,?,'publication_set',?,'applying',NULL,?,NULL)`,
			Args: []any{scope, command.IdempotencyKey, requestHash, command.EntitlementID, operationID, nowUnix},
		},
		{
			SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix)
SELECT ?,?,?,?,?,NULL,?,?,? WHERE
(SELECT MAX(version) FROM whitelist_publication_controls WHERE entitlement_id=?)=?` + enableGuard,
			Args: append([]any{
				controlID, command.EntitlementID, result.Version, whiteListBoolInt(command.Enabled),
				source, operationID, requestHash, nowUnix, command.EntitlementID, current.Version,
			}, enableGuardArgs...),
		},
		backupRPODirtyGenerationStatement(nowUnix),
		{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'whitelist.publication.set','whitelist-entitlement',?,?,?,? WHERE EXISTS(
SELECT 1 FROM whitelist_publication_controls WHERE control_id=? AND operation_id=?)`,
			Args: []any{
				auditEventID, s.auditActor(command.Actor), s.auditResource(command.EntitlementID),
				auditEnvelope, auditDigest, nowUnix, controlID, operationID,
			},
		},
		{
			SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type='whitelist_publication_set' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying'`,
			Args: []any{string(responseJSON), nowUnix, scope, command.IdempotencyKey, requestHash, operationID},
		},
	}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return result, nil
	}
	if saved, found, resolveErr := s.resolveWhiteListPublication(
		ctx, scope, command.IdempotencyKey, requestHash, command.EntitlementID,
	); found || resolveErr != nil {
		return saved, resolveErr
	}
	if isUnknownWrite(requestErr) {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	return WhiteListPublicationResult{}, ErrConflict
}

func appendWhiteListTopUpProjectionCASProof(
	statements *[]rqlite.Statement,
	previous *whitelistbalance.BalanceProjection,
	next whitelistbalance.BalanceProjection,
	nowUnix int64,
	guard string,
	guardArgs []any,
	scope string,
	idempotencyKey string,
	requestHash string,
	operationID string,
) error {
	_, changed, err := appendWhiteListProjectionCAS(
		statements, previous, next, nowUnix, guard, guardArgs,
	)
	if err != nil || !changed {
		return ErrUnavailable
	}
	*statements = append(*statements,
		backupRPODirtyGenerationStatement(nowUnix),
		rqlite.Statement{
			SQL: `UPDATE idempotency_requests SET status='whitelist-topup-projection-rejected'
WHERE scope=? AND command_type='whitelist_topup_confirm' AND idempotency_key=?
AND request_hash=? AND operation_id=? AND status='applying' AND changes()<>1`,
			Args: []any{scope, idempotencyKey, requestHash, operationID},
		},
	)
	return nil
}

func newWhiteListOrdinaryRenewalPeriod(
	periods []whitelistbalance.Period,
	periodID string,
	orderID string,
	nowUnix int64,
	endsAtUnix int64,
) (whitelistbalance.Period, error) {
	startsAtUnix := nowUnix
	ordinal := int64(0)
	if len(periods) != 0 {
		last := periods[len(periods)-1]
		if last.Ordinal >= whitelistbalance.MaxExclusive-1 {
			return whitelistbalance.Period{}, ErrConflict
		}
		ordinal = last.Ordinal + 1
		if last.EndsAtUnix > startsAtUnix {
			startsAtUnix = last.EndsAtUnix
		}
	}
	period := whitelistbalance.Period{
		ID: periodID, Ordinal: ordinal, StartsAtUnix: startsAtUnix, EndsAtUnix: endsAtUnix,
		IncludedGrantBytes: 0, AccessOrderID: orderID,
	}
	if !validWhiteListPeriod(period) {
		return whitelistbalance.Period{}, ErrConflict
	}
	return period, nil
}

func (s *Service) prepareWhiteListTopUp(
	ctx context.Context,
	nowUnix int64,
	entitlementID string,
	productID string,
) (whiteListTopUpPrepared, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT entitlement.entitlement_id,entitlement.customer_id,
customer.status AS customer_status,customer.expires_at_unix AS customer_expires_at_unix,
product.product_id,product.kind,tariff.amount_minor,tariff.currency,product.bytes,product.unit
FROM whitelist_entitlement_identities AS entitlement
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
JOIN whitelist_gb_products AS product ON product.product_id=?
JOIN tariff_versions AS tariff ON tariff.tariff_version_id=product.product_id
WHERE entitlement.entitlement_id=?`, Args: []any{productID, entitlementID}})
	if err != nil {
		return whiteListTopUpPrepared{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return whiteListTopUpPrepared{}, ErrNotFound
	}
	storedEntitlementID, entitlementOK := rowString(row, "entitlement_id")
	customerID, customerOK := rowString(row, "customer_id")
	status, statusOK := rowString(row, "customer_status")
	expires, expiresOK := rowInt64(row, "customer_expires_at_unix")
	product, productOK := parseWhiteListProduct(row)
	if !entitlementOK || !customerOK || !statusOK || !expiresOK || !productOK ||
		storedEntitlementID != entitlementID || product.ProductID != productID {
		return whiteListTopUpPrepared{}, ErrConflict
	}
	if status != "active" || expires <= nowUnix {
		return whiteListTopUpPrepared{}, ErrConflict
	}
	return whiteListTopUpPrepared{
		CustomerID: customerID, CustomerStatus: status, CustomerExpires: expires, Product: product,
	}, nil
}

func (s *Service) queryWhiteListTopUpOrder(ctx context.Context, orderID string) (whiteListTopUpRecord, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT source_order.order_id,source_order.payment_code,topup.entitlement_id,topup.product_id,
source_order.amount_minor,source_order.currency,product.bytes,source_order.payment_state,
source_order.expires_at_unix,source_order.origin_bot_id,source_order.origin_chat_key_hmac,
product.kind,product.unit
FROM whitelist_topup_orders AS topup
JOIN orders AS source_order ON source_order.order_id=topup.order_id
JOIN whitelist_gb_products AS product ON product.product_id=topup.product_id
WHERE topup.order_id=?`, Args: []any{orderID}})
	if err != nil {
		return whiteListTopUpRecord{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return whiteListTopUpRecord{}, ErrNotFound
	}
	storedOrderID, orderOK := rowString(row, "order_id")
	paymentCode, paymentCodeOK := rowString(row, "payment_code")
	entitlementID, entitlementOK := rowString(row, "entitlement_id")
	productID, productIDOK := rowString(row, "product_id")
	amountMinor, amountOK := rowInt64(row, "amount_minor")
	currency, currencyOK := rowString(row, "currency")
	bytes, bytesOK := rowInt64(row, "bytes")
	paymentState, stateOK := rowString(row, "payment_state")
	expiresAtUnix, expiresOK := rowInt64(row, "expires_at_unix")
	originBotID, originBotOK := optionalRowString(row, "origin_bot_id")
	originChatHMAC, originChatOK := optionalRowString(row, "origin_chat_key_hmac")
	kind, kindOK := rowString(row, "kind")
	unit, unitOK := rowString(row, "unit")
	view := WhiteListTopUpOrder{
		OrderID: storedOrderID, PaymentCode: paymentCode, EntitlementID: entitlementID,
		ProductID: productID, AmountMinor: amountMinor, Currency: currency, Bytes: bytes,
		PaymentState: PaymentState(paymentState), ExpiresAtUnix: expiresAtUnix,
	}
	if !orderOK || !paymentCodeOK || !entitlementOK || !productIDOK || !amountOK ||
		!currencyOK || !bytesOK || !stateOK || !expiresOK || !originBotOK || !originChatOK ||
		!kindOK || kind != whiteListProductKind || !unitOK || unit != whiteListProductUnit ||
		storedOrderID != orderID || !validWhiteListTopUpOrder(view) {
		return whiteListTopUpRecord{}, ErrUnavailable
	}
	return whiteListTopUpRecord{View: view, OriginBotID: originBotID, OriginChatHMAC: originChatHMAC}, nil
}

func whiteListTopUpDecisionHash(commandType string, orderID string) (string, error) {
	if !validWhiteListID(commandType) || !validWhiteListID(orderID) {
		return "", ErrConflict
	}
	return whiteListCanonicalHash(struct {
		Version     int    `json:"version"`
		CommandType string `json:"command_type"`
		OrderID     string `json:"order_id"`
	}{1, commandType, orderID})
}

func activeWhiteListTopUpPeriod(periods []whitelistbalance.Period, nowUnix int64) (whitelistbalance.Period, bool) {
	var active whitelistbalance.Period
	found := false
	for _, period := range periods {
		if period.StartsAtUnix <= nowUnix && nowUnix < period.EndsAtUnix {
			if found {
				return whitelistbalance.Period{}, false
			}
			active = period
			found = true
		}
	}
	return active, found
}

func (s *Service) whiteListTopUpAccessOrder(
	ctx context.Context,
	entitlementID string,
	nowUnix int64,
) (string, int64, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT source_order.order_id AS access_order_id,
customer.expires_at_unix AS period_ends_at_unix
FROM whitelist_entitlement_identities AS entitlement
JOIN customers AS customer ON customer.customer_id=entitlement.customer_id
JOIN orders AS source_order ON source_order.customer_id=entitlement.customer_id
WHERE entitlement.entitlement_id=? AND customer.status='active'
AND customer.expires_at_unix>? AND source_order.payment_state='confirmed'
AND source_order.decision='confirmed' AND source_order.confirmed_at_unix IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM whitelist_topup_orders AS topup
WHERE topup.order_id=source_order.order_id)
ORDER BY source_order.confirmed_at_unix DESC,source_order.order_id DESC LIMIT 1`, Args: []any{
		entitlementID, nowUnix,
	}})
	if err != nil {
		return "", 0, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return "", 0, ErrConflict
	}
	orderID, orderOK := rowString(row, "access_order_id")
	endsAtUnix, endsOK := rowInt64(row, "period_ends_at_unix")
	if !orderOK || !endsOK || !validWhiteListID(orderID) ||
		!validWhiteListTimestamp(endsAtUnix) || endsAtUnix <= nowUnix {
		return "", 0, ErrUnavailable
	}
	return orderID, endsAtUnix, nil
}

func newWhiteListTopUpPeriod(
	periods []whitelistbalance.Period,
	periodID string,
	accessOrderID string,
	nowUnix int64,
	endsAtUnix int64,
) (whitelistbalance.Period, error) {
	if !validWhiteListID(periodID) || !validWhiteListID(accessOrderID) ||
		!validWhiteListTimestamp(nowUnix) || !validWhiteListTimestamp(endsAtUnix) ||
		endsAtUnix <= nowUnix {
		return whitelistbalance.Period{}, ErrConflict
	}
	nextOrdinal := int64(0)
	for _, period := range periods {
		if period.EndsAtUnix > nowUnix {
			return whitelistbalance.Period{}, ErrConflict
		}
		if period.Ordinal >= nextOrdinal {
			if period.Ordinal >= whitelistbalance.MaxExclusive-1 {
				return whitelistbalance.Period{}, ErrConflict
			}
			nextOrdinal = period.Ordinal + 1
		}
	}
	period := whitelistbalance.Period{
		ID: periodID, Ordinal: nextOrdinal, StartsAtUnix: nowUnix, EndsAtUnix: endsAtUnix,
		IncludedGrantBytes: 0, AccessOrderID: accessOrderID,
	}
	if !validWhiteListPeriod(period) {
		return whitelistbalance.Period{}, ErrConflict
	}
	return period, nil
}

func (s *Service) resolveWhiteListTopUpConfirmation(
	ctx context.Context,
	scope string,
	idempotencyKey string,
	requestHash string,
	orderID string,
) (ConfirmWhiteListTopUpPaymentResult, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,operation_id,status,response_json
FROM idempotency_requests
WHERE scope=? AND command_type='whitelist_topup_confirm' AND idempotency_key=?`, Args: []any{
		scope, idempotencyKey,
	}})
	if err != nil {
		return ConfirmWhiteListTopUpPaymentResult{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return ConfirmWhiteListTopUpPaymentResult{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	operationID, operationOK := rowString(row, "operation_id")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || !resourceOK || storedHash != requestHash || resourceID != orderID {
		return ConfirmWhiteListTopUpPaymentResult{}, true, ErrConflict
	}
	if !operationOK || !statusOK || status != "applied" || !responseOK {
		return ConfirmWhiteListTopUpPaymentResult{}, true, ErrUnavailable
	}
	var result ConfirmWhiteListTopUpPaymentResult
	if json.Unmarshal([]byte(responseJSON), &result) != nil ||
		result.OrderID != orderID || result.OperationID != operationID ||
		!validWhiteListID(result.PaymentID) || !validWhiteListID(result.PeriodID) ||
		!validWhiteListID(result.BalanceEntryID) || !validWhiteListID(result.ControlID) ||
		result.PurchasedBytes <= 0 || result.PurchasedRemainingBytes < result.PurchasedBytes ||
		result.BalanceVersion <= 0 || !result.PublicationEnabled {
		return ConfirmWhiteListTopUpPaymentResult{}, true, ErrUnavailable
	}
	return result, true, nil
}

func (s *Service) currentWhiteListPublication(
	ctx context.Context,
	entitlementID string,
) (WhiteListPublicationResult, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT control_id,entitlement_id,version,enabled,source
FROM whitelist_publication_controls WHERE entitlement_id=?
ORDER BY version DESC LIMIT 1`, Args: []any{entitlementID}})
	if err != nil {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return WhiteListPublicationResult{}, ErrNotFound
	}
	controlID, controlOK := rowString(row, "control_id")
	storedEntitlementID, entitlementOK := rowString(row, "entitlement_id")
	version, versionOK := rowInt64(row, "version")
	enabled, enabledOK := rowInt64(row, "enabled")
	source, sourceOK := rowString(row, "source")
	if !controlOK || !entitlementOK || !versionOK || !enabledOK || !sourceOK ||
		storedEntitlementID != entitlementID || version <= 0 || (enabled != 0 && enabled != 1) ||
		!validWhiteListPublicationSource(source) {
		return WhiteListPublicationResult{}, ErrUnavailable
	}
	return WhiteListPublicationResult{
		EntitlementID: entitlementID, ControlID: controlID, Version: version,
		Enabled: enabled == 1, Source: source,
	}, nil
}

// WhiteListPublicationState returns the latest immutable publication control
// without changing balance, access, or publication state.
func (s *Service) WhiteListPublicationState(
	ctx context.Context,
	entitlementID string,
) (WhiteListPublicationResult, error) {
	if s == nil || s.store == nil || !validWhiteListID(entitlementID) {
		return WhiteListPublicationResult{}, ErrNotFound
	}
	return s.currentWhiteListPublication(ctx, entitlementID)
}

func (s *Service) resolveWhiteListPublication(
	ctx context.Context,
	scope string,
	idempotencyKey string,
	requestHash string,
	entitlementID string,
) (WhiteListPublicationResult, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,operation_id,status,response_json
FROM idempotency_requests
WHERE scope=? AND command_type='whitelist_publication_set' AND idempotency_key=?`, Args: []any{
		scope, idempotencyKey,
	}})
	if err != nil {
		return WhiteListPublicationResult{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return WhiteListPublicationResult{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	operationID, operationOK := rowString(row, "operation_id")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || !resourceOK || storedHash != requestHash || resourceID != entitlementID {
		return WhiteListPublicationResult{}, true, ErrConflict
	}
	if !operationOK || !statusOK || status != "applied" || !responseOK {
		return WhiteListPublicationResult{}, true, ErrUnavailable
	}
	var result WhiteListPublicationResult
	if json.Unmarshal([]byte(responseJSON), &result) != nil ||
		result.EntitlementID != entitlementID || result.OperationID != operationID ||
		!validWhiteListID(result.ControlID) || result.Version <= 1 ||
		!validWhiteListPublicationSource(result.Source) {
		return WhiteListPublicationResult{}, true, ErrUnavailable
	}
	return result, true, nil
}

func validWhiteListPublicationSource(source string) bool {
	switch source {
	case "DEFAULT_OFF", "CONFIRMED_GB_PURCHASE", "ADMIN_ENABLE", "ADMIN_DISABLE":
		return true
	default:
		return false
	}
}

func (s *Service) resolveWhiteListTopUpOrder(
	ctx context.Context,
	scope string,
	commandType string,
	idempotencyKey string,
	requestHash string,
) (WhiteListTopUpOrder, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,status,response_json FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`, Args: []any{
		scope, commandType, idempotencyKey,
	}})
	if err != nil {
		return WhiteListTopUpOrder{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return WhiteListTopUpOrder{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || !resourceOK || storedHash != requestHash {
		return WhiteListTopUpOrder{}, true, ErrConflict
	}
	if !statusOK || status != "applied" || !responseOK {
		return WhiteListTopUpOrder{}, true, ErrUnavailable
	}
	var result WhiteListTopUpOrder
	if json.Unmarshal([]byte(responseJSON), &result) != nil ||
		!validWhiteListTopUpOrder(result) || result.OrderID != resourceID {
		return WhiteListTopUpOrder{}, true, ErrUnavailable
	}
	return result, true, nil
}

func validWhiteListTopUpOrder(order WhiteListTopUpOrder) bool {
	return validWhiteListID(order.OrderID) && validWhiteListID(order.PaymentCode) &&
		validWhiteListID(order.EntitlementID) && validWhiteListID(order.ProductID) &&
		order.AmountMinor > 0 && order.Currency == whiteListProductCurrency && order.Bytes > 0 &&
		(order.PaymentState == PaymentPending || order.PaymentState == PaymentClaimed ||
			order.PaymentState == PaymentConfirmed || order.PaymentState == PaymentCanceled) &&
		validWhiteListTimestamp(order.ExpiresAtUnix)
}

func whiteListTopUpScope(entitlementID string) string {
	return "whitelist-topup:" + entitlementID
}
