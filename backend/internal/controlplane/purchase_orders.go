package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	purchaseOrderIdempotencyScope   = "legacy-order"
	purchaseOrderIdempotencyCommand = "create"

	PurchaseOrderIdentityNone     = "none"
	PurchaseOrderIdentitySubToken = "sub_token"
	PurchaseOrderIdentityLogin    = "login"
)

// PurchaseOrderCommand creates either a fresh inert purchase identity or an
// order for an existing customer. Commercial terms come only from tariff_versions.
type PurchaseOrderCommand struct {
	TariffVersionID string
	CustomerID      string
	IdempotencyKey  string
	IdentitySource  string
	IdentityHMAC    string
	Actor           string
	Channel         string
	SourceEventID   string
}

type purchaseOrderRequestIdentity struct {
	Mode            string `json:"mode"`
	CustomerID      string `json:"customer_id,omitempty"`
	TariffVersionID string `json:"tariff_version_id"`
	IdentitySource  string `json:"identity_source"`
	IdentityHMAC    string `json:"identity_hmac"`
}

type purchaseOrderSavedResponse struct {
	OrderID         string `json:"order_id"`
	CustomerID      string `json:"customer_id"`
	TariffVersionID string `json:"tariff_version_id"`
	Mode            string `json:"mode"`
	OperationID     string `json:"operation_id"`
	IdentitySource  string `json:"identity_source"`
	IdentityHMAC    string `json:"identity_hmac"`
}

// PurchaseOrderIdentityHMAC binds a supplied legacy identity without exposing
// its token or login outside the cluster SecretBox trust boundary.
func (s *Service) PurchaseOrderIdentityHMAC(source, supplied string) (string, error) {
	normalized, err := normalizePurchaseOrderIdentity(source, supplied)
	if err != nil {
		return "", err
	}
	return s.store.secrets.LookupHMAC("legacy-order-identity:"+source, []byte(normalized)), nil
}

func normalizePurchaseOrderIdentity(source, supplied string) (string, error) {
	switch source {
	case PurchaseOrderIdentityNone:
		if strings.TrimSpace(supplied) != "" {
			return "", errors.New("controlplane: invalid empty purchase identity")
		}
		return "", nil
	case PurchaseOrderIdentitySubToken:
		normalized := strings.TrimSpace(supplied)
		if normalized == "" {
			return "", errors.New("controlplane: invalid purchase token identity")
		}
		return normalized, nil
	case PurchaseOrderIdentityLogin:
		normalized := strings.ToLower(strings.TrimSpace(supplied))
		if normalized == "" {
			return "", errors.New("controlplane: invalid purchase login identity")
		}
		return normalized, nil
	default:
		return "", errors.New("controlplane: invalid purchase identity source")
	}
}

// CreatePurchaseOrder commits identity, sealed access (for a new customer),
// immutable tariff terms, order, audit, backup dirtiness, and optional durable
// idempotency binding in one rqlite transaction. It never retries a write.
func (s *Service) CreatePurchaseOrder(ctx context.Context, command PurchaseOrderCommand) (OrderView, error) {
	tariffVersionID := strings.TrimSpace(command.TariffVersionID)
	actor := strings.TrimSpace(command.Actor)
	channel := strings.TrimSpace(command.Channel)
	customerID := strings.TrimSpace(command.CustomerID)
	idempotencyKey := command.IdempotencyKey
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = ""
	}
	if tariffVersionID == "" || actor == "" || channel == "" {
		return OrderView{}, errors.New("controlplane: invalid purchase order")
	}
	identitySource := command.IdentitySource
	identityHMAC := command.IdentityHMAC
	if identitySource == "" && identityHMAC == "" {
		identitySource = PurchaseOrderIdentityNone
		identityHMAC, _ = s.PurchaseOrderIdentityHMAC(identitySource, "")
	}
	if !validPurchaseOrderIdentityBinding(identitySource, identityHMAC) {
		return OrderView{}, errors.New("controlplane: invalid purchase identity binding")
	}
	mode := "new"
	if customerID != "" {
		mode = "existing"
	}
	requestHash, err := purchaseOrderRequestHash(mode, customerID, tariffVersionID, identitySource, identityHMAC)
	if err != nil {
		return OrderView{}, err
	}
	if idempotencyKey != "" {
		if saved, found, resolveErr := s.resolvePurchaseOrderRequest(ctx, idempotencyKey, requestHash); found || resolveErr != nil {
			return saved, resolveErr
		}
	}

	var displayLogin string
	var access customerAccessMint
	if mode == "new" {
		customerID, err = s.ids.NewID("purchase-customer")
		if err != nil {
			return OrderView{}, errors.New("controlplane: generate purchase customer identifier")
		}
		displayLogin = customerID
		if _, err = CanonicalLoginKey(displayLogin); err != nil {
			return OrderView{}, errors.New("controlplane: generate purchase customer login")
		}
		access, err = s.mintCustomerAccess(customerID)
		if err != nil {
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

	now := s.clock.Now().Unix()
	orderAuditID := auditID("order-create", orderID, 0, now)
	orderAuditEnvelope, orderAuditDigest, err := s.orderAuditDetails(orderAuditID, orderAuditMetadata{
		Channel: channel, SourceEventID: strings.TrimSpace(command.SourceEventID),
	})
	if err != nil {
		return OrderView{}, err
	}
	saved := purchaseOrderSavedResponse{
		OrderID: orderID, CustomerID: customerID, TariffVersionID: tariffVersionID, Mode: mode,
		OperationID:    operationID,
		IdentitySource: identitySource, IdentityHMAC: identityHMAC,
	}
	savedJSON, err := json.Marshal(saved)
	if err != nil {
		return OrderView{}, errors.New("controlplane: encode purchase order response")
	}

	guard := "1=1"
	var guardArgs []any
	statements := make([]rqlite.Statement, 0, 16)
	if idempotencyKey != "" {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT OR IGNORE INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,
resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix)
VALUES(?,'create',?,?,?,?,?,'applying',NULL,unixepoch(),NULL)`,
			Args: []any{purchaseOrderIdempotencyScope, idempotencyKey, requestHash, orderID, mode, operationID},
		})
		guard = `EXISTS (SELECT 1 FROM idempotency_requests WHERE scope=? AND command_type='create'
AND idempotency_key=? AND request_hash=? AND operation_id=? AND status='applying')`
		guardArgs = []any{purchaseOrderIdempotencyScope, idempotencyKey, requestHash, operationID}
	}

	var customerAuditID string
	if mode == "new" {
		canonicalLogin, _ := CanonicalLoginKey(displayLogin)
		loginHMAC := s.store.secrets.LookupHMAC("customer-login", []byte(canonicalLogin))
		customerAuditID = auditID("purchase-customer-create", customerID, 1, now)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
SELECT ?,?,?,'expired',unixepoch(),1,unixepoch(),unixepoch() WHERE ` + guard,
			Args: append([]any{customerID, displayLogin, loginHMAC}, guardArgs...),
		})
		customer := Customer{ID: customerID, Status: "expired", Generation: 1, Access: access.Access}
		accessGuard := guard + ` AND EXISTS (SELECT 1 FROM customers
WHERE customer_id=? AND status='expired' AND expires_at_unix<=unixepoch() AND generation=1)`
		accessGuardArgs := append(append([]any(nil), guardArgs...), customerID)
		statements = append(statements, access.statements(customer, now, accessGuard, accessGuardArgs, s.store.secrets)...)
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,'customer.purchase_intent_create','customer',?,unixepoch() WHERE ` + guard + `
AND EXISTS (SELECT 1 FROM customers WHERE customer_id=? AND status='expired' AND generation=1)`,
			Args: append([]any{customerAuditID, s.auditActor(actor), s.auditResource(customerID)}, append(guardArgs, customerID)...),
		})
	}

	buyerScope := "legacy-http"
	if mode == "new" {
		buyerScope = "anonymous"
	}
	buyerHMAC := s.store.secrets.LookupHMAC("order-buyer:"+buyerScope, []byte(orderID))
	statements = append(statements,
		rqlite.Statement{
			SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,operation_id,origin_bot_id,origin_chat_key_hmac)
SELECT ?,?,?,?,c.customer_id,t.tariff_version_id,t.amount_minor,t.currency,t.duration_days,
unixepoch(),unixepoch()+86400,'created','none',NULL,?,'',NULL
FROM tariff_versions t JOIN customers c ON c.customer_id=?
WHERE t.tariff_version_id=? AND t.active=1 AND c.status IN ('active','expired') AND ` + guard,
			Args: append([]any{orderID, paymentCode, buyerScope, buyerHMAC, operationID, customerID, tariffVersionID}, guardArgs...),
		},
		rqlite.Statement{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,
details_envelope,details_sha256,created_at_unix)
SELECT ?,?,'order.create','order',?,?,?,unixepoch() WHERE ` + guard + `
AND EXISTS (SELECT 1 FROM orders WHERE order_id=? AND customer_id=? AND tariff_version_id=?)`,
			Args: append([]any{orderAuditID, s.auditActor(actor), s.auditResource(orderID), orderAuditEnvelope,
				orderAuditDigest}, append(guardArgs, orderID, customerID, tariffVersionID)...),
		},
	)

	if mode == "new" {
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE customers SET updated_at_unix=updated_at_unix
WHERE customer_id=? AND display_login=? AND status='expired' AND expires_at_unix<=unixepoch() AND generation=1
AND (SELECT count(*) FROM subscription_tokens WHERE customer_id=? AND revoked=0 AND generation=1)=1
AND (SELECT count(*) FROM credentials WHERE customer_id=? AND enabled=1 AND generation=1)=?
AND EXISTS (SELECT 1 FROM orders o JOIN tariff_versions t ON t.tariff_version_id=o.tariff_version_id
WHERE o.order_id=? AND o.customer_id=? AND o.tariff_version_id=?
AND o.amount_minor=t.amount_minor AND o.currency=t.currency AND o.duration_days=t.duration_days
AND o.payment_state='created' AND o.provisioning_state='none' AND o.decision IS NULL)
AND (SELECT count(*) FROM audit_events WHERE event_id IN (?,?))=2
AND NOT EXISTS (SELECT 1 FROM desired_node_state WHERE customer_id=?)
AND NOT EXISTS (SELECT 1 FROM outbox_events WHERE aggregate_id LIKE ? || ':%')
AND ` + guard,
			Args: append([]any{
				customerID, displayLogin, customerID, customerID, len(canonicalCustomerProtocols),
				orderID, customerID, tariffVersionID, customerAuditID, orderAuditID, customerID, customerID,
			}, guardArgs...),
		})
	} else {
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE customers SET updated_at_unix=updated_at_unix
WHERE customer_id=? AND status IN ('active','expired')
AND EXISTS (SELECT 1 FROM orders o JOIN tariff_versions t ON t.tariff_version_id=o.tariff_version_id
WHERE o.order_id=? AND o.customer_id=? AND o.tariff_version_id=?
AND o.amount_minor=t.amount_minor AND o.currency=t.currency AND o.duration_days=t.duration_days
AND o.payment_state='created' AND o.provisioning_state='none' AND o.decision IS NULL)
AND EXISTS (SELECT 1 FROM audit_events WHERE event_id=?) AND ` + guard,
			Args: append([]any{customerID, orderID, customerID, tariffVersionID, orderAuditID}, guardArgs...),
		})
	}
	statements = append(statements, backupRPODirtyGenerationStatement(now))
	if idempotencyKey == "" {
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE customers SET status='purchase-order-backup-rejected'
WHERE customer_id=? AND changes()<>1`, Args: []any{customerID},
		})
	} else {
		statements = append(statements,
			rqlite.Statement{
				SQL: `UPDATE idempotency_requests SET status='purchase-order-backup-rejected'
WHERE scope=? AND command_type='create' AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying' AND changes()<>1`,
				Args: guardArgs,
			},
			rqlite.Statement{
				SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=unixepoch()
WHERE scope=? AND command_type='create' AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying'
AND EXISTS (SELECT 1 FROM orders WHERE order_id=? AND customer_id=? AND tariff_version_id=?)`,
				Args: []any{string(savedJSON), purchaseOrderIdempotencyScope, idempotencyKey, requestHash,
					operationID, orderID, customerID, tariffVersionID},
			},
			rqlite.Statement{
				SQL: `UPDATE idempotency_requests SET status='purchase-order-finalize-rejected'
WHERE scope=? AND command_type='create' AND idempotency_key=? AND request_hash=?
AND operation_id=? AND status='applying' AND changes()<>1`,
				Args: guardArgs,
			},
		)
	}

	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if idempotencyKey != "" {
		resolved, found, resolveErr := s.resolvePurchaseOrderRequest(ctx, idempotencyKey, requestHash)
		if found || resolveErr != nil {
			return resolved, resolveErr
		}
		return OrderView{}, ErrUnavailable
	}
	if requestErr != nil && !isUnknownWrite(requestErr) {
		return OrderView{}, ErrUnavailable
	}
	return s.resolveInitialPurchaseOrder(ctx, saved)
}

func validPurchaseOrderIdentityBinding(source, fingerprint string) bool {
	if source != PurchaseOrderIdentityNone && source != PurchaseOrderIdentitySubToken && source != PurchaseOrderIdentityLogin {
		return false
	}
	if len(fingerprint) != sha256.Size*2 || fingerprint != strings.ToLower(fingerprint) {
		return false
	}
	decoded, err := hex.DecodeString(fingerprint)
	return err == nil && len(decoded) == sha256.Size
}

func purchaseOrderRequestHash(mode, customerID, tariffVersionID, identitySource, identityHMAC string) (string, error) {
	switch mode {
	case "new":
		customerID = ""
	case "existing":
		if customerID == "" {
			return "", errors.New("controlplane: invalid existing purchase identity")
		}
	default:
		return "", errors.New("controlplane: invalid purchase mode")
	}
	if tariffVersionID == "" || !validPurchaseOrderIdentityBinding(identitySource, identityHMAC) {
		return "", errors.New("controlplane: invalid purchase request identity")
	}
	canonical, err := json.Marshal(purchaseOrderRequestIdentity{
		Mode: mode, CustomerID: customerID, TariffVersionID: tariffVersionID,
		IdentitySource: identitySource, IdentityHMAC: identityHMAC,
	})
	if err != nil {
		return "", errors.New("controlplane: encode purchase order request")
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) resolvePurchaseOrderRequest(ctx context.Context, key, requestHash string) (OrderView, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT request_hash,resource_id,decision,operation_id,status,response_json
FROM idempotency_requests WHERE scope=? AND command_type='create' AND idempotency_key=?`,
		Args: []any{purchaseOrderIdempotencyScope, key},
	})
	if err != nil {
		return OrderView{}, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return OrderView{}, false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	mode, modeOK := rowString(row, "decision")
	operationID, operationOK := rowString(row, "operation_id")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || storedHash != requestHash {
		return OrderView{}, true, ErrConflict
	}
	if !resourceOK || !modeOK || !statusOK || status != "applied" || !responseOK {
		return OrderView{}, true, ErrUnavailable
	}
	var saved purchaseOrderSavedResponse
	if json.Unmarshal([]byte(responseJSON), &saved) != nil || saved.OrderID == "" || saved.CustomerID == "" ||
		saved.TariffVersionID == "" || saved.OperationID == "" || saved.OrderID != resourceID || saved.Mode != mode ||
		!operationOK || saved.OperationID != operationID {
		return OrderView{}, true, ErrUnavailable
	}
	savedRequestHash, savedHashErr := purchaseOrderRequestHash(saved.Mode, saved.CustomerID, saved.TariffVersionID,
		saved.IdentitySource, saved.IdentityHMAC)
	if savedHashErr != nil || savedRequestHash != storedHash {
		return OrderView{}, true, ErrUnavailable
	}
	order, queryErr := s.queryOrder(ctx, saved.OrderID)
	if queryErr != nil || order.CustomerID != saved.CustomerID || order.TariffVersionID != saved.TariffVersionID ||
		order.View.AmountMinor <= 0 || order.View.Currency != "RUB" || order.View.DurationSeconds <= 0 {
		return OrderView{}, true, ErrUnavailable
	}
	order.View.PaymentState = PaymentPending
	return order.View, true, nil
}

func (s *Service) resolveInitialPurchaseOrder(ctx context.Context, saved purchaseOrderSavedResponse) (OrderView, error) {
	order, err := s.queryOrder(ctx, saved.OrderID)
	if err != nil || order.CustomerID != saved.CustomerID || order.TariffVersionID != saved.TariffVersionID ||
		order.OperationID != saved.OperationID || order.View.PaymentState != PaymentPending || order.ExpiresAtUnix <= order.DBNow ||
		order.View.AmountMinor <= 0 || order.View.Currency != "RUB" || order.View.DurationSeconds <= 0 {
		return OrderView{}, ErrUnavailable
	}
	if saved.Mode != "new" {
		return order.View, nil
	}
	customer, err := s.BusinessCustomerByID(ctx, saved.CustomerID)
	if err != nil || customer.ID != saved.CustomerID || customer.Status != "expired" ||
		customer.ExpiresAtUnix > order.DBNow || customer.Generation != 1 || customer.Access.SubscriptionToken == "" ||
		len(customer.Access.Credentials) != len(canonicalCustomerProtocols) {
		return OrderView{}, ErrUnavailable
	}
	for _, protocol := range canonicalCustomerProtocols {
		if customer.Access.Credentials[protocol] == "" {
			return OrderView{}, ErrUnavailable
		}
	}
	return order.View, nil
}
