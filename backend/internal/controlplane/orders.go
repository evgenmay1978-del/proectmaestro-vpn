package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	orderUnclaimedTTL    = 24 * time.Hour
	orderDecisionRetries = 3
)

type canonicalPaymentDecision struct {
	Decision         string `json:"decision"`
	OrderID          string `json:"order_id"`
	PaymentReference string `json:"payment_reference"`
	TariffVersion    string `json:"tariff_version"`
}

type orderRecord struct {
	View                OrderView
	CustomerID          string
	TariffVersionID     string
	OperationID         string
	ExpiresAtUnix       int64
	CustomerPriorExpiry int64
	CustomerExpiry      int64
	CustomerGeneration  int64
	DBNow               int64
	OriginChatHMAC      string
}

type desiredTarget struct {
	NodeID        string
	ServiceName   string
	EnvelopeBytes []byte
	Digest        string
	EventID       string
}

type cancelSavedResult struct {
	OrderID     string `json:"order_id"`
	OperationID string `json:"operation_id"`
}

type orderAuditMetadata struct {
	Channel             string `json:"channel,omitempty"`
	SourceEventID       string `json:"source_event_id,omitempty"`
	ResultHash          string `json:"result_hash,omitempty"`
	ProposedPaymentID   string `json:"proposed_payment_id,omitempty"`
	ProposedOperationID string `json:"proposed_operation_id,omitempty"`
	OccurredAtUnix      int64  `json:"occurred_at_unix,omitempty"`
}

func canonicalPaymentDecisionHash(command ConfirmPaymentCommand, decision string) (string, error) {
	return canonicalPaymentHashValues(decision, command.OrderID, command.PaymentReference, command.TariffVersionID)
}

func canonicalPaymentHashValues(decision, orderID, paymentReference, tariffVersion string) (string, error) {
	if strings.TrimSpace(decision) == "" || strings.TrimSpace(orderID) == "" || strings.TrimSpace(tariffVersion) == "" {
		return "", errors.New("controlplane: invalid payment decision")
	}
	canonical, err := json.Marshal(canonicalPaymentDecision{
		Decision: decision, OrderID: orderID, PaymentReference: paymentReference, TariffVersion: tariffVersion,
	})
	if err != nil {
		return "", errors.New("controlplane: encode payment decision")
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) CreateOrder(ctx context.Context, command CreateOrderCommand) (OrderView, error) {
	if strings.TrimSpace(command.TariffVersionID) == "" || strings.TrimSpace(command.CustomerID) == "" || strings.TrimSpace(command.BuyerScope) == "" {
		return OrderView{}, errors.New("controlplane: invalid order")
	}
	stableBuyer := command.BuyerScope != "anonymous" && strings.TrimSpace(command.BuyerIdentity) != ""
	buyerMaterial := command.BuyerIdentity
	if stableBuyer {
		buyerHMAC := s.store.secrets.LookupHMAC("order-buyer:"+command.BuyerScope, []byte(buyerMaterial))
		if guarded, err := s.orderByGuard(ctx, command.BuyerScope, buyerHMAC); err == nil {
			if guarded.View.PaymentState == PaymentPending && guarded.ExpiresAtUnix <= guarded.DBNow {
				if expireErr := s.expireCreatedOrder(ctx, guarded.View.OrderID); expireErr != nil && !errors.Is(expireErr, ErrConflict) {
					return OrderView{}, expireErr
				}
			} else {
				return guarded.View, nil
			}
		} else if !errors.Is(err, ErrNotFound) {
			return OrderView{}, err
		}
	}

	orderID, err := s.ids.NewID("order")
	if err != nil {
		return OrderView{}, errors.New("controlplane: generate order ID")
	}
	operationID, err := s.ids.NewID("order-create")
	if err != nil {
		return OrderView{}, errors.New("controlplane: generate order operation")
	}
	paymentCode, err := s.ids.NewID("payment-code")
	if err != nil {
		return OrderView{}, errors.New("controlplane: generate payment code")
	}
	if !stableBuyer {
		buyerMaterial = orderID
	}
	buyerHMAC := s.store.secrets.LookupHMAC("order-buyer:"+command.BuyerScope, []byte(buyerMaterial))
	var chatHMAC any
	if command.ChatIdentity != "" {
		chatHMAC = s.store.secrets.LookupHMAC("order-origin-chat", []byte(command.ChatIdentity))
	}
	now := s.clock.Now().Unix()
	auditEventID := auditID("order-create", orderID, 0, now)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID,
	})
	if err != nil {
		return OrderView{}, err
	}
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,operation_id,origin_bot_id,origin_chat_key_hmac)
SELECT ?,?,?,?,c.customer_id,t.tariff_version_id,t.amount_minor,t.currency,t.duration_days,
unixepoch(),unixepoch()+86400,'created','none',NULL,?,?,?
FROM tariff_versions t JOIN customers c ON c.customer_id=?
WHERE t.tariff_version_id=? AND t.active=1 AND c.status<>'deleted'`,
		Args: []any{orderID, paymentCode, command.BuyerScope, buyerHMAC, operationID, command.OriginBotID, chatHMAC, command.CustomerID, command.TariffVersionID},
	}, backupRPODirtyGenerationStatement(now)}
	if stableBuyer {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO active_order_guards(buyer_scope,buyer_key_hmac,order_id,created_at_unix)
SELECT buyer_scope,buyer_key_hmac,order_id,created_at_unix FROM orders WHERE order_id=?`,
			Args: []any{orderID},
		})
	}
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'order.create','order',?,?,?,unixepoch() WHERE EXISTS(SELECT 1 FROM orders WHERE order_id=?)`,
		Args: []any{auditEventID, s.auditActor(command.Actor), s.auditResource(orderID), auditEnvelope, auditDigest, orderID},
	})
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr != nil {
		if stableBuyer {
			if guarded, lookupErr := s.orderByGuard(ctx, command.BuyerScope, buyerHMAC); lookupErr == nil {
				return guarded.View, nil
			}
		}
		if created, lookupErr := s.queryOrder(ctx, orderID); lookupErr == nil {
			return created.View, nil
		}
		if isUnknownWrite(requestErr) {
			return OrderView{}, ErrUnavailable
		}
		return OrderView{}, ErrConflict
	}
	created, err := s.queryOrder(ctx, orderID)
	if err != nil {
		return OrderView{}, ErrUnavailable
	}
	return created.View, nil
}

func (s *Service) MarkPaymentClaimed(ctx context.Context, command ClaimPaymentCommand) (OrderView, error) {
	if strings.TrimSpace(command.OrderID) == "" {
		return OrderView{}, errors.New("controlplane: invalid order")
	}
	if err := s.expireCreatedOrder(ctx, command.OrderID); err != nil && !errors.Is(err, ErrConflict) {
		return OrderView{}, err
	}
	order, err := s.queryOrder(ctx, command.OrderID)
	if err != nil {
		return OrderView{}, err
	}
	if order.View.PaymentState == PaymentExpired || order.View.PaymentState == PaymentCanceled {
		return OrderView{}, ErrNotFound
	}
	if order.View.PaymentState == PaymentConfirmed {
		return OrderView{}, ErrConflict
	}
	claimOperationID, err := s.ids.NewID("order-claim")
	if err != nil {
		return OrderView{}, errors.New("controlplane: generate claim operation")
	}
	delivery, deliveryErr := s.telegramDelivery("owner-claim:"+command.OrderID, command.OrderID, "owner_payment_claim", order.View.OriginBotID, order.OriginChatHMAC)
	if deliveryErr != nil {
		return OrderView{}, deliveryErr
	}
	now := s.clock.Now().Unix()
	auditEventID := auditID("order-claim", command.OrderID, 0, now)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, SourceEventID: command.SourceEventID,
	})
	if err != nil {
		return OrderView{}, err
	}
	statements := []rqlite.Statement{{
		SQL: `UPDATE orders SET payment_state='payment_claimed',operation_id=?
WHERE order_id=? AND payment_state='created' AND decision IS NULL AND expires_at_unix>unixepoch()
RETURNING order_id`, Args: []any{claimOperationID, command.OrderID},
	}, backupRPODirtyGenerationStatement(now)}
	if delivery != nil {
		statements = append(statements, *delivery)
	}
	statements = append(statements, rqlite.Statement{
		SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'order.payment_claimed','order',?,?,?,unixepoch()
WHERE EXISTS(SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='payment_claimed')
ON CONFLICT(event_id) DO NOTHING`,
		Args: []any{auditEventID, s.auditActor(command.Actor), s.auditResource(command.OrderID), auditEnvelope,
			auditDigest, command.OrderID, claimOperationID},
	})
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	claimed, lookupErr := s.queryOrder(ctx, command.OrderID)
	if lookupErr == nil && claimed.View.PaymentState == PaymentClaimed {
		return claimed.View, nil
	}
	if requestErr != nil && isUnknownWrite(requestErr) {
		return OrderView{}, ErrUnavailable
	}
	if lookupErr != nil {
		return OrderView{}, lookupErr
	}
	return OrderView{}, ErrConflict
}

func (s *Service) ConfirmPayment(ctx context.Context, command ConfirmPaymentCommand) (ConfirmPaymentResult, error) {
	if strings.TrimSpace(command.OrderID) == "" || strings.TrimSpace(command.IdempotencyKey) == "" ||
		strings.TrimSpace(command.PaymentReference) == "" || strings.TrimSpace(command.TariffVersionID) == "" {
		return ConfirmPaymentResult{}, errors.New("controlplane: invalid payment confirmation")
	}
	requestHash, err := canonicalPaymentDecisionHash(command, "confirm")
	if err != nil {
		return ConfirmPaymentResult{}, err
	}
	if saved, found, resolveErr := s.resolveConfirm(ctx, command.OrderID, command.IdempotencyKey, requestHash); found || resolveErr != nil {
		return saved, resolveErr
	}
	if err := s.expireCreatedOrder(ctx, command.OrderID); err != nil && !errors.Is(err, ErrConflict) {
		return ConfirmPaymentResult{}, err
	}
	var lastStatementErr *rqlite.StatementError
	for attempt := 0; attempt < orderDecisionRetries; attempt++ {
		prepared, prepErr := s.prepareConfirm(ctx, command)
		if prepErr != nil {
			if saved, found, resolveErr := s.resolveConfirm(ctx, command.OrderID, command.IdempotencyKey, requestHash); found || resolveErr != nil {
				return saved, resolveErr
			}
			return ConfirmPaymentResult{}, prepErr
		}
		paymentID, idErr := s.ids.NewID("payment")
		if idErr != nil {
			return ConfirmPaymentResult{}, errors.New("controlplane: generate payment ID")
		}
		operationID, idErr := s.ids.NewID("payment-confirm")
		if idErr != nil {
			return ConfirmPaymentResult{}, errors.New("controlplane: generate payment operation")
		}
		targets, targetErr := s.confirmTargets(ctx, prepared, operationID)
		if targetErr != nil {
			return ConfirmPaymentResult{}, targetErr
		}
		result := ConfirmPaymentResult{
			OrderID: command.OrderID, OperationID: operationID,
			ExpiresAtUnix: prepared.CustomerExpiry, Generation: prepared.CustomerGeneration,
		}
		responseJSON, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return ConfirmPaymentResult{}, errors.New("controlplane: encode payment result")
		}
		provider := strings.TrimSpace(command.Provider)
		if provider == "" {
			provider = "manual"
		}
		now := s.clock.Now().Unix()
		auditEventID := auditID("payment-confirm", command.OrderID, result.Generation, now)
		occurredAtUnix := int64(0)
		if !command.OccurredAt.IsZero() {
			occurredAtUnix = command.OccurredAt.Unix()
		}
		auditEnvelope, auditDigest, auditErr := s.orderAuditDetails(auditEventID, orderAuditMetadata{
			Channel: command.Channel, SourceEventID: command.SourceEventID, ResultHash: requestHash,
			ProposedPaymentID: command.ProposedPaymentID, ProposedOperationID: command.ProposedOperationID,
			OccurredAtUnix: occurredAtUnix,
		})
		if auditErr != nil {
			return ConfirmPaymentResult{}, auditErr
		}
		statements := []rqlite.Statement{{
			SQL: `INSERT INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,
resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix)
VALUES(?,'confirm',?,?,?,'payment_confirmed',?,'applying',NULL,unixepoch(),NULL)`,
			Args: []any{"order:" + command.OrderID, command.IdempotencyKey, requestHash, command.OrderID, operationID},
		}, {
			SQL: `INSERT INTO payments(payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix)
SELECT ?,o.order_id,?,NULL,?,o.amount_minor,o.currency,? FROM orders o
WHERE o.order_id=? AND o.tariff_version_id=? AND o.payment_state='payment_claimed' AND o.decision IS NULL`,
			Args: []any{paymentID, provider, command.PaymentReference, prepared.DBNow, command.OrderID, command.TariffVersionID},
		}, {
			SQL: `UPDATE orders SET payment_state='confirmed',provisioning_state='pending',decision='confirmed',
confirmed_at_unix=?,result_expires_at_unix=?,result_generation=?,operation_id=?
WHERE order_id=? AND tariff_version_id=? AND payment_state='payment_claimed' AND decision IS NULL
AND EXISTS(SELECT 1 FROM idempotency_requests WHERE scope=? AND command_type='confirm'
AND idempotency_key=? AND request_hash=? AND operation_id=? AND status='applying')`,
			Args: []any{prepared.DBNow, result.ExpiresAtUnix, result.Generation, operationID, command.OrderID, command.TariffVersionID,
				"order:" + command.OrderID, command.IdempotencyKey, requestHash, operationID},
		}, {
			SQL: `UPDATE customers SET status='active',expires_at_unix=?,generation=?,updated_at_unix=unixepoch()
WHERE customer_id=? AND generation=? AND expires_at_unix=? AND status IN ('active','expired')
AND EXISTS(SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='confirmed')
RETURNING generation`,
			Args: []any{result.ExpiresAtUnix, result.Generation, prepared.CustomerID,
				prepared.CustomerGeneration - 1, prepared.CustomerPriorExpiry, command.OrderID, operationID},
		}}
		statements = append(statements, backupRPODirtyGenerationStatement(now))
		appendWhiteListOrdinaryRenewalIntent(&statements, prepared, command.OrderID, operationID)
		for _, target := range targets {
			statements = append(statements, rqlite.Statement{
				SQL: `INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,
desired_sha256,status,updated_at_unix,tombstone,operation_id)
SELECT ?,?,?,?,?,?,'pending',unixepoch(),0,? WHERE EXISTS(
SELECT 1 FROM orders o JOIN customers c ON c.customer_id=o.customer_id
WHERE o.order_id=? AND o.operation_id=? AND c.generation=? AND c.status='active')
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET generation=excluded.generation,
desired_envelope=excluded.desired_envelope,desired_sha256=excluded.desired_sha256,status='pending',
updated_at_unix=excluded.updated_at_unix,tombstone=0,operation_id=excluded.operation_id
WHERE desired_node_state.generation<excluded.generation`,
				Args: []any{prepared.CustomerID, target.NodeID, target.ServiceName, result.Generation,
					target.EnvelopeBytes, target.Digest, operationID, command.OrderID, operationID, result.Generation},
			}, rqlite.Statement{
				SQL: `INSERT INTO outbox_events(event_id,aggregate_type,aggregate_id,generation,event_type,
payload_envelope,payload_sha256,status,available_at_unix,attempts,created_at_unix,
node_id,service_name,operation_id,event_kind)
SELECT ?,'customer',?,?, 'customer.desired',?,?,'pending',unixepoch(),0,unixepoch(),?,?,?,'apply'
WHERE EXISTS(SELECT 1 FROM desired_node_state WHERE customer_id=? AND node_id=? AND service_name=?
AND generation=? AND operation_id=? AND tombstone=0)`,
				Args: []any{target.EventID, prepared.CustomerID + ":" + target.NodeID + ":" + target.ServiceName,
					result.Generation, target.EnvelopeBytes, target.Digest, target.NodeID, target.ServiceName, operationID,
					prepared.CustomerID, target.NodeID, target.ServiceName, result.Generation, operationID},
			})
		}
		statements = append(statements,
			rqlite.Statement{SQL: `DELETE FROM active_order_guards WHERE order_id=?`, Args: []any{command.OrderID}},
			rqlite.Statement{
				SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'payment.confirm','order',?,?,?,unixepoch() WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='confirmed')`,
				Args: []any{auditEventID, s.auditActor(command.Actor), s.auditResource(command.OrderID),
					auditEnvelope, auditDigest, command.OrderID, operationID},
			},
			rqlite.Statement{
				SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=unixepoch()
WHERE scope=? AND command_type='confirm' AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying'`,
				Args: []any{string(responseJSON), "order:" + command.OrderID, command.IdempotencyKey, requestHash, operationID},
			},
		)
		_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
		if requestErr == nil {
			return result, nil
		}
		if saved, found, resolveErr := s.resolveConfirm(ctx, command.OrderID, command.IdempotencyKey, requestHash); found || resolveErr != nil {
			return saved, resolveErr
		}
		if isUnknownWrite(requestErr) {
			return ConfirmPaymentResult{}, ErrUnavailable
		}
		var statementErr *rqlite.StatementError
		if !errors.As(requestErr, &statementErr) {
			return ConfirmPaymentResult{}, ErrUnavailable
		}
		lastStatementErr = statementErr
	}
	return ConfirmPaymentResult{}, orderDecisionConflict(lastStatementErr)
}

func (s *Service) CancelOrder(ctx context.Context, command CancelOrderCommand) (OrderView, error) {
	if strings.TrimSpace(command.OrderID) == "" || strings.TrimSpace(command.IdempotencyKey) == "" {
		return OrderView{}, errors.New("controlplane: invalid cancellation")
	}
	order, err := s.queryOrder(ctx, command.OrderID)
	if err != nil {
		return OrderView{}, err
	}
	requestHash, err := canonicalPaymentHashValues("cancel", command.OrderID, "", order.TariffVersionID)
	if err != nil {
		return OrderView{}, err
	}
	if saved, found, resolveErr := s.resolveCancel(ctx, command.OrderID, command.IdempotencyKey, requestHash); found || resolveErr != nil {
		return saved, resolveErr
	}
	operationID, err := s.ids.NewID("order-cancel")
	if err != nil {
		return OrderView{}, errors.New("controlplane: generate cancellation operation")
	}
	saved := cancelSavedResult{OrderID: command.OrderID, OperationID: operationID}
	responseJSON, _ := json.Marshal(saved)
	now := s.clock.Now().Unix()
	auditEventID := auditID("order-cancel", command.OrderID, 0, now)
	auditEnvelope, auditDigest, err := s.orderAuditDetails(auditEventID, orderAuditMetadata{
		Channel: command.Channel, ResultHash: requestHash,
	})
	if err != nil {
		return OrderView{}, err
	}
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,
resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix)
VALUES(?,'cancel',?,?,?,'order_canceled',?,'applying',NULL,unixepoch(),NULL)`,
		Args: []any{"order:" + command.OrderID, command.IdempotencyKey, requestHash, command.OrderID, operationID},
	}, {
		SQL: `UPDATE orders SET payment_state='canceled',provisioning_state='none',decision='cancelled',operation_id=?
WHERE order_id=? AND payment_state IN ('created','payment_claimed') AND decision IS NULL
AND EXISTS(SELECT 1 FROM idempotency_requests WHERE scope=? AND command_type='cancel'
AND idempotency_key=? AND request_hash=? AND operation_id=? AND status='applying')`,
		Args: []any{operationID, command.OrderID, "order:" + command.OrderID, command.IdempotencyKey, requestHash, operationID},
	}, backupRPODirtyGenerationStatement(now), {
		SQL: `DELETE FROM active_order_guards WHERE order_id=?`, Args: []any{command.OrderID},
	}, {
		SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'order.cancel','order',?,?,?,unixepoch() WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='canceled')`,
		Args: []any{auditEventID, s.auditActor(command.Actor), s.auditResource(command.OrderID), auditEnvelope,
			auditDigest, command.OrderID, operationID},
	}, {
		SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=unixepoch()
WHERE scope=? AND command_type='cancel' AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying'`,
		Args: []any{string(responseJSON), "order:" + command.OrderID, command.IdempotencyKey, requestHash, operationID},
	}}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return OrderView{OrderID: command.OrderID, PaymentState: PaymentCanceled}, nil
	}
	if resolved, found, resolveErr := s.resolveCancel(ctx, command.OrderID, command.IdempotencyKey, requestHash); found || resolveErr != nil {
		return resolved, resolveErr
	}
	if isUnknownWrite(requestErr) {
		return OrderView{}, ErrUnavailable
	}
	return OrderView{}, ErrConflict
}

func (s *Service) OrderByID(ctx context.Context, orderID string) (OrderView, error) {
	if strings.TrimSpace(orderID) == "" {
		return OrderView{}, ErrNotFound
	}
	if err := s.expireCreatedOrder(ctx, orderID); err != nil && !errors.Is(err, ErrConflict) {
		return OrderView{}, err
	}
	order, err := s.queryOrder(ctx, orderID)
	if err != nil {
		return OrderView{}, err
	}
	if order.View.PaymentState == PaymentCanceled || order.View.PaymentState == PaymentExpired {
		return OrderView{}, ErrNotFound
	}
	return order.View, nil
}

func (s *Service) expireCreatedOrder(ctx context.Context, orderID string) error {
	order, err := s.queryOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if order.View.PaymentState != PaymentPending || order.ExpiresAtUnix > order.DBNow {
		return nil
	}
	operationID, err := s.ids.NewID("order-expire")
	if err != nil {
		return errors.New("controlplane: generate order expiry operation")
	}
	now := s.clock.Now().Unix()
	statements := []rqlite.Statement{{
		SQL: `UPDATE orders SET payment_state='expired',provisioning_state='none',decision='expired',operation_id=?
WHERE order_id=? AND payment_state='created' AND decision IS NULL AND expires_at_unix<=unixepoch()`,
		Args: []any{operationID, orderID},
	}, backupRPODirtyGenerationStatement(now), {
		SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,'order.expire','order',?,unixepoch() WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='expired')`,
		Args: []any{auditID("order-expire", orderID, 0, now), s.auditActor("expiry-lazy"), s.auditResource(orderID), orderID, operationID},
	}}
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return nil
	}
	resolved, lookupErr := s.queryOrder(ctx, orderID)
	if lookupErr == nil && resolved.View.PaymentState == PaymentExpired {
		return nil
	}
	if isUnknownWrite(requestErr) {
		return ErrUnavailable
	}
	return ErrConflict
}

func (s *Service) prepareConfirm(ctx context.Context, command ConfirmPaymentCommand) (orderRecord, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT o.order_id,o.customer_id,o.tariff_version_id,o.amount_minor,o.currency,o.duration_days,
o.expires_at_unix,o.payment_state,o.origin_bot_id,o.origin_chat_key_hmac,
c.expires_at_unix AS customer_expires_at_unix,c.generation AS customer_generation,unixepoch() AS db_now
FROM orders o JOIN customers c ON c.customer_id=o.customer_id
WHERE o.order_id=? AND o.tariff_version_id=? AND c.status IN ('active','expired')`,
		Args: []any{command.OrderID, command.TariffVersionID},
	})
	if err != nil {
		return orderRecord{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return orderRecord{}, ErrConflict
	}
	prepared, ok := orderRecordFromRow(row)
	if !ok || prepared.View.PaymentState != PaymentClaimed {
		return orderRecord{}, ErrConflict
	}
	priorExpiry, okExpiry := rowInt64(row, "customer_expires_at_unix")
	priorGeneration, okGeneration := rowInt64(row, "customer_generation")
	if !okExpiry || !okGeneration {
		return orderRecord{}, ErrUnavailable
	}
	prepared.CustomerPriorExpiry = priorExpiry
	prepared.CustomerGeneration = priorGeneration + 1
	base := priorExpiry
	if base < prepared.DBNow {
		base = prepared.DBNow
	}
	prepared.CustomerExpiry = base + prepared.View.DurationSeconds
	return prepared, nil
}

func (s *Service) confirmTargets(ctx context.Context, prepared orderRecord, operationID string) ([]desiredTarget, error) {
	access, err := s.customerAccess(ctx, prepared.CustomerID)
	if err != nil {
		return nil, err
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT node_id,service_name FROM node_services
WHERE desired_target=1 AND retired=0 ORDER BY node_id,service_name`,
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) == 0 {
		return nil, ErrUnavailable
	}
	payload := map[string]any{"expires_at_unix": prepared.CustomerExpiry, "status": "active"}
	if canonical := accessPayload(access); canonical != nil {
		payload["access"] = canonical
	}
	targets := make([]desiredTarget, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		nodeID, nodeOK := rowString(row, "node_id")
		serviceName, serviceOK := rowString(row, "service_name")
		if !nodeOK || !serviceOK {
			return nil, ErrUnavailable
		}
		envelope, digest, sealErr := s.store.secrets.SealDesiredPayload(DesiredPayloadScope{
			NodeID: nodeID, ServiceID: serviceName, CustomerID: prepared.CustomerID,
			Generation: prepared.CustomerGeneration, OperationID: operationID,
			PayloadKind: "customer-active", Tombstone: false,
		}, payload)
		if sealErr != nil {
			return nil, sealErr
		}
		envelopeBytes, marshalErr := json.Marshal(envelope)
		if marshalErr != nil {
			return nil, errors.New("controlplane: encode desired envelope")
		}
		eventID, idErr := s.ids.NewID("outbox")
		if idErr != nil {
			return nil, errors.New("controlplane: generate outbox event")
		}
		targets = append(targets, desiredTarget{NodeID: nodeID, ServiceName: serviceName, EnvelopeBytes: envelopeBytes, Digest: digest, EventID: eventID})
	}
	return targets, nil
}

func (s *Service) resolveConfirm(ctx context.Context, orderID, key, requestHash string) (ConfirmPaymentResult, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT request_hash,status,response_json FROM idempotency_requests
WHERE scope=? AND command_type='confirm' AND idempotency_key=?`,
		Args: []any{"order:" + orderID, key},
	})
	if err != nil {
		return ConfirmPaymentResult{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return ConfirmPaymentResult{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	if !hashOK || storedHash != requestHash {
		return ConfirmPaymentResult{}, true, ErrConflict
	}
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !statusOK || status != "applied" || !responseOK {
		return ConfirmPaymentResult{}, true, ErrUnavailable
	}
	var result ConfirmPaymentResult
	if json.Unmarshal([]byte(responseJSON), &result) != nil || result.OrderID != orderID || result.OperationID == "" || result.Generation <= 0 || result.ExpiresAtUnix <= 0 {
		return ConfirmPaymentResult{}, true, ErrUnavailable
	}
	return result, true, nil
}

func (s *Service) resolveCancel(ctx context.Context, orderID, key, requestHash string) (OrderView, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT request_hash,status,response_json FROM idempotency_requests
WHERE scope=? AND command_type='cancel' AND idempotency_key=?`,
		Args: []any{"order:" + orderID, key},
	})
	if err != nil {
		return OrderView{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return OrderView{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	if !hashOK || storedHash != requestHash {
		return OrderView{}, true, ErrConflict
	}
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !statusOK || status != "applied" || !responseOK {
		return OrderView{}, true, ErrUnavailable
	}
	var saved cancelSavedResult
	if json.Unmarshal([]byte(responseJSON), &saved) != nil || saved.OrderID != orderID || saved.OperationID == "" {
		return OrderView{}, true, ErrUnavailable
	}
	return OrderView{OrderID: orderID, PaymentState: PaymentCanceled}, true, nil
}

func (s *Service) queryOrder(ctx context.Context, orderID string) (orderRecord, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT order_id,customer_id,tariff_version_id,amount_minor,currency,duration_days,
expires_at_unix,payment_state,operation_id,origin_bot_id,origin_chat_key_hmac,unixepoch() AS db_now
FROM orders WHERE order_id=?`, Args: []any{orderID},
	})
	if err != nil {
		return orderRecord{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return orderRecord{}, ErrNotFound
	}
	order, ok := orderRecordFromRow(row)
	if !ok {
		return orderRecord{}, ErrUnavailable
	}
	return order, nil
}

func (s *Service) orderByGuard(ctx context.Context, scope, buyerHMAC string) (orderRecord, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT o.order_id,o.customer_id,o.tariff_version_id,o.amount_minor,o.currency,o.duration_days,
o.expires_at_unix,o.payment_state,o.origin_bot_id,o.origin_chat_key_hmac,unixepoch() AS db_now
FROM active_order_guards g JOIN orders o ON o.order_id=g.order_id
WHERE g.buyer_scope=? AND g.buyer_key_hmac=? AND o.payment_state IN ('created','payment_claimed')`,
		Args: []any{scope, buyerHMAC},
	})
	if err != nil {
		return orderRecord{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return orderRecord{}, ErrNotFound
	}
	order, ok := orderRecordFromRow(row)
	if !ok {
		return orderRecord{}, ErrUnavailable
	}
	return order, nil
}

func orderRecordFromRow(row map[string]any) (orderRecord, bool) {
	orderID, idOK := rowString(row, "order_id")
	customerID, customerOK := rowString(row, "customer_id")
	tariffVersion, tariffOK := rowString(row, "tariff_version_id")
	amount, amountOK := rowInt64(row, "amount_minor")
	currency, currencyOK := rowString(row, "currency")
	durationDays, durationOK := rowInt64(row, "duration_days")
	expires, expiresOK := rowInt64(row, "expires_at_unix")
	paymentState, stateOK := rowString(row, "payment_state")
	dbNow, nowOK := rowInt64(row, "db_now")
	if !idOK || !customerOK || !tariffOK || !amountOK || !currencyOK || !durationOK || !expiresOK || !stateOK || !nowOK || durationDays <= 0 {
		return orderRecord{}, false
	}
	originBot, _ := optionalString(row, "origin_bot_id")
	operationID, _ := optionalString(row, "operation_id")
	originChat, _ := optionalString(row, "origin_chat_key_hmac")
	return orderRecord{
		View: OrderView{OrderID: orderID, AmountMinor: amount, Currency: currency,
			DurationSeconds: durationDays * 86400, OriginBotID: originBot, PaymentState: PaymentState(paymentState)},
		CustomerID: customerID, TariffVersionID: tariffVersion, ExpiresAtUnix: expires,
		OperationID: operationID, DBNow: dbNow, OriginChatHMAC: originChat,
	}, true
}

func optionalString(row map[string]any, key string) (string, bool) {
	value, ok := row[key]
	if !ok || value == nil {
		return "", false
	}
	result, ok := value.(string)
	return result, ok
}

func (s *Service) telegramDelivery(dedupeKey, orderID, kind, botID, chatHMAC string) (*rqlite.Statement, error) {
	if botID == "" || chatHMAC == "" {
		return nil, nil
	}
	payloadValues := map[string]string{"event": kind, "order_id": orderID}
	if kind == "owner_whitelist_topup_payment_claim" {
		confirmCallback := "mwcf:" + orderID
		rejectCallback := "mwrj:" + orderID
		if len(confirmCallback) > 64 || len(rejectCallback) > 64 {
			return nil, errors.New("controlplane: Telegram callback exceeds limit")
		}
		payloadValues["confirm_callback_data"] = confirmCallback
		payloadValues["reject_callback_data"] = rejectCallback
	}
	payload, err := json.Marshal(payloadValues)
	if err != nil {
		return nil, errors.New("controlplane: encode Telegram delivery")
	}
	envelope, err := s.store.secrets.Seal(SecretScope{
		OwnerType: "telegram-delivery", OwnerID: dedupeKey, Field: "payload", Kind: "owner-order-event",
	}, payload)
	if err != nil {
		return nil, errors.New("controlplane: protect Telegram delivery")
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, errors.New("controlplane: encode Telegram envelope")
	}
	digest := sha256.Sum256(envelopeBytes)
	deliveryID, err := s.ids.NewID("telegram-delivery")
	if err != nil {
		return nil, errors.New("controlplane: generate Telegram delivery")
	}
	return &rqlite.Statement{
		SQL: `INSERT INTO telegram_delivery_outbox(delivery_id,bot_id,chat_key_hmac,payload_envelope,
payload_sha256,dedupe_key,status,attempts,available_at_unix,created_at_unix)
VALUES(?,?,?,?,?,?,'pending',0,unixepoch(),unixepoch()) ON CONFLICT(bot_id,dedupe_key) DO NOTHING`,
		Args: []any{deliveryID, botID, chatHMAC, envelopeBytes, hex.EncodeToString(digest[:]), dedupeKey},
	}, nil
}

func (s *Service) auditActor(actor string) string {
	return s.store.secrets.LookupHMAC("audit-actor", []byte(actor))
}

func (s *Service) auditResource(resource string) string {
	return s.store.secrets.LookupHMAC("audit-resource", []byte(resource))
}

func (s *Service) orderAuditDetails(eventID string, metadata orderAuditMetadata) ([]byte, string, error) {
	plaintext, err := json.Marshal(metadata)
	if err != nil {
		return nil, "", errors.New("controlplane: encode order audit metadata")
	}
	envelope, err := s.store.secrets.Seal(SecretScope{
		OwnerType: "audit-event", OwnerID: eventID, Field: "details", Kind: "order-metadata",
	}, plaintext)
	if err != nil {
		return nil, "", errors.New("controlplane: protect order audit metadata")
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return nil, "", errors.New("controlplane: encode order audit envelope")
	}
	digest := sha256.Sum256(envelopeBytes)
	return envelopeBytes, hex.EncodeToString(digest[:]), nil
}

func orderDecisionConflict(err error) error {
	var statementErr *rqlite.StatementError
	if errors.As(err, &statementErr) {
		return errors.Join(ErrConflict, statementErr)
	}
	return ErrConflict
}

func isUnknownWrite(err error) bool {
	var transportErr *rqlite.TransportError
	return errors.As(err, &transportErr) && transportErr.UnknownOutcome
}
