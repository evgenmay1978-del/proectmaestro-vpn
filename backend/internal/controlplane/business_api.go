package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"golang.org/x/crypto/bcrypt"
)

// BusinessCustomer is the decrypted application view used at the HTTP boundary.
// It never participates in durable command responses or SQL arguments.
type BusinessCustomer struct {
	Customer
	Login string
}

type BusinessOrder struct {
	OrderView
	Code              string
	TariffVersionID   string
	DurationDays      int
	ProvisioningState string
	ResultGeneration  int64
	CustomerID        string
}

type BusinessSetting struct {
	Key              string
	PublicValueJSON  json.RawMessage
	Generation       int64
	Members          map[string]json.RawMessage
	SecretConfigured bool
}

type BusinessAuditEvent struct {
	ID        string
	Action    string
	CreatedAt time.Time
}

func (s *Service) BusinessCustomerByLogin(ctx context.Context, login string) (BusinessCustomer, error) {
	customer, err := s.CustomerByLogin(ctx, login)
	if err != nil {
		return BusinessCustomer{}, err
	}
	return s.businessCustomer(ctx, customer)
}

func (s *Service) BusinessCustomerByToken(ctx context.Context, token string) (BusinessCustomer, error) {
	customer, err := s.CustomerByToken(ctx, token)
	if err != nil {
		return BusinessCustomer{}, err
	}
	return s.businessCustomer(ctx, customer)
}

func (s *Service) BusinessCustomerByID(ctx context.Context, customerID string) (BusinessCustomer, error) {
	if strings.TrimSpace(customerID) == "" {
		return BusinessCustomer{}, ErrNotFound
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT customer_id,display_login,status,expires_at_unix,generation
FROM customers WHERE customer_id=? AND status<>'deleted' LIMIT 1`,
		Args: []any{customerID},
	})
	if err != nil {
		return BusinessCustomer{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return BusinessCustomer{}, ErrNotFound
	}
	id, idOK := rowString(row, "customer_id")
	login, loginOK := rowString(row, "display_login")
	status, statusOK := rowString(row, "status")
	expires, expiresOK := rowInt64(row, "expires_at_unix")
	generation, generationOK := rowInt64(row, "generation")
	if !idOK || !loginOK || !statusOK || !expiresOK || !generationOK {
		return BusinessCustomer{}, ErrUnavailable
	}
	access, err := s.customerAccess(ctx, id)
	if err != nil {
		return BusinessCustomer{}, err
	}
	return BusinessCustomer{Customer: Customer{ID: id, Status: status, ExpiresAtUnix: expires, Generation: generation, Access: access}, Login: login}, nil
}

func (s *Service) businessCustomer(ctx context.Context, customer Customer) (BusinessCustomer, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT display_login FROM customers WHERE customer_id=? LIMIT 1`,
		Args: []any{customer.ID},
	})
	if err != nil {
		return BusinessCustomer{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return BusinessCustomer{}, ErrNotFound
	}
	login, ok := rowString(row, "display_login")
	if !ok {
		return BusinessCustomer{}, ErrUnavailable
	}
	access, err := s.customerAccess(ctx, customer.ID)
	if err != nil {
		return BusinessCustomer{}, err
	}
	customer.Access = access
	return BusinessCustomer{Customer: customer, Login: login}, nil
}

func (s *Service) ListBusinessCustomers(ctx context.Context) ([]BusinessCustomer, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT customer_id,display_login,status,expires_at_unix,generation
FROM customers WHERE status<>'deleted' ORDER BY display_login`})
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}
	customers := make([]BusinessCustomer, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		id, idOK := rowString(row, "customer_id")
		login, loginOK := rowString(row, "display_login")
		status, statusOK := rowString(row, "status")
		expires, expiresOK := rowInt64(row, "expires_at_unix")
		generation, generationOK := rowInt64(row, "generation")
		if !idOK || !loginOK || !statusOK || !expiresOK || !generationOK {
			return nil, ErrUnavailable
		}
		access, err := s.customerAccess(ctx, id)
		if err != nil {
			return nil, err
		}
		customers = append(customers, BusinessCustomer{Customer: Customer{
			ID: id, Status: status, ExpiresAtUnix: expires, Generation: generation, Access: access,
		}, Login: login})
	}
	return customers, nil
}

func (s *Service) BusinessCustomerUsage(ctx context.Context, login string) (int64, error) {
	if _, err := s.CustomerByLogin(ctx, login); err != nil {
		return 0, err
	}
	// The approved HA schema has no traffic counters. Zero is the canonical
	// compatibility value until a later schema slice introduces them.
	return 0, nil
}

func (s *Service) ReadBusinessSetting(ctx context.Context, key string) (BusinessSetting, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return BusinessSetting{}, ErrNotFound
	}
	results, err := s.store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT public_value_json,generation FROM cluster_settings WHERE setting_key=?`, Args: []any{key}},
		rqlite.Statement{SQL: `SELECT member_key,member_value_json FROM setting_members WHERE setting_key=? ORDER BY member_key`, Args: []any{key}},
		rqlite.Statement{SQL: `SELECT 1 AS configured FROM setting_secrets WHERE setting_key=? LIMIT 1`, Args: []any{key}},
	)
	if err != nil || len(results) != 3 {
		return BusinessSetting{}, ErrUnavailable
	}
	row, ok := firstRow(results[:1])
	if !ok {
		return BusinessSetting{}, ErrNotFound
	}
	value, valueOK := rowString(row, "public_value_json")
	generation, generationOK := rowInt64(row, "generation")
	if !valueOK || !generationOK || !json.Valid([]byte(value)) {
		return BusinessSetting{}, ErrUnavailable
	}
	setting := BusinessSetting{Key: key, PublicValueJSON: json.RawMessage(value), Generation: generation, Members: map[string]json.RawMessage{}}
	for _, member := range results[1].Rows {
		memberKey, keyOK := rowString(member, "member_key")
		memberValue, memberOK := rowString(member, "member_value_json")
		if !keyOK || !memberOK || !json.Valid([]byte(memberValue)) {
			return BusinessSetting{}, ErrUnavailable
		}
		setting.Members[memberKey] = json.RawMessage(memberValue)
	}
	setting.SecretConfigured = len(results[2].Rows) == 1
	return setting, nil
}

func (s *Service) UpdateSecretSetting(ctx context.Context, key, raw, actor string, expected int64) (SettingResult, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(raw) == "" {
		return SettingResult{}, errors.New("controlplane: invalid secret setting")
	}
	envelope, err := s.store.secrets.Seal(SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, []byte(raw))
	if err != nil {
		return SettingResult{}, err
	}
	return s.UpdateSetting(ctx, SettingUpdate{
		Key: key, ExpectedGeneration: expected, PublicValueJSON: `{}`, Secret: &envelope, Actor: actor,
	})
}

func (s *Service) UpdateSecretSettingIdempotent(
	ctx context.Context, key, raw, actor string, expected int64, commandType, idempotencyKey string,
) (SettingResult, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(raw) == "" ||
		strings.TrimSpace(commandType) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return SettingResult{}, errors.New("controlplane: invalid idempotent secret setting")
	}
	envelope, err := s.store.secrets.Seal(SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, []byte(raw))
	if err != nil {
		return SettingResult{}, err
	}
	return s.UpdateSetting(ctx, SettingUpdate{
		Key: key, ExpectedGeneration: expected, PublicValueJSON: `{}`, Secret: &envelope, Actor: actor,
		CommandType: commandType, IdempotencyKey: idempotencyKey,
		RequestFingerprint: s.store.secrets.LookupHMAC("setting-secret-request:"+key, []byte(raw)),
	})
}

func (s *Service) ListBusinessOrders(ctx context.Context, status string) ([]BusinessOrder, error) {
	statement := rqlite.Statement{SQL: `
SELECT o.order_id,o.payment_code,o.amount_minor,o.currency,o.duration_days,o.payment_state,
o.provisioning_state,o.result_generation,o.customer_id,o.tariff_version_id
FROM orders o WHERE (?='' OR o.payment_state=?) ORDER BY o.created_at_unix DESC`, Args: []any{status, status}}
	results, err := s.store.db.QueryLinearizable(ctx, statement)
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}
	orders := make([]BusinessOrder, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		order, ok := parseBusinessOrder(row)
		if !ok {
			return nil, ErrUnavailable
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func (s *Service) BusinessOrderByID(ctx context.Context, orderID string) (BusinessOrder, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT o.order_id,o.payment_code,o.amount_minor,o.currency,o.duration_days,o.payment_state,
o.provisioning_state,o.result_generation,o.customer_id,o.tariff_version_id
FROM orders o WHERE o.order_id=?`, Args: []any{orderID}})
	if err != nil {
		return BusinessOrder{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return BusinessOrder{}, ErrNotFound
	}
	order, ok := parseBusinessOrder(row)
	if !ok {
		return BusinessOrder{}, ErrUnavailable
	}
	return order, nil
}

func parseBusinessOrder(row map[string]any) (BusinessOrder, bool) {
	id, idOK := rowString(row, "order_id")
	code, codeOK := rowString(row, "payment_code")
	amount, amountOK := rowInt64(row, "amount_minor")
	currency, currencyOK := rowString(row, "currency")
	days, daysOK := rowInt64(row, "duration_days")
	payment, paymentOK := rowString(row, "payment_state")
	provisioning, provisioningOK := rowString(row, "provisioning_state")
	tariff, tariffOK := rowString(row, "tariff_version_id")
	if !idOK || !codeOK || !amountOK || !currencyOK || !daysOK || !paymentOK || !provisioningOK || !tariffOK {
		return BusinessOrder{}, false
	}
	resultGeneration, _ := rowInt64(row, "result_generation")
	customerID, _ := rowString(row, "customer_id")
	return BusinessOrder{
		OrderView: OrderView{OrderID: id, AmountMinor: amount, Currency: currency, DurationSeconds: days * 86400, PaymentState: PaymentState(payment)},
		Code:      code, TariffVersionID: tariff, DurationDays: int(days), ProvisioningState: provisioning,
		ResultGeneration: resultGeneration, CustomerID: customerID,
	}, true
}

func (s *Service) BusinessSubscriptionDocument(ctx context.Context, rawToken string) (BusinessCustomer, json.RawMessage, error) {
	customer, err := s.BusinessCustomerByToken(ctx, rawToken)
	if err != nil {
		return BusinessCustomer{}, nil, err
	}
	if customer.Status != "active" || customer.ExpiresAtUnix <= s.clock.Now().Unix() || len(customer.Access.Credentials) == 0 {
		return BusinessCustomer{}, nil, ErrForbidden
	}
	document, err := json.Marshal(map[string]any{
		"login": customer.Login, "expires_at_unix": customer.ExpiresAtUnix,
		"generation": customer.Generation, "credentials": customer.Access.Credentials,
	})
	if err != nil {
		return BusinessCustomer{}, nil, ErrUnavailable
	}
	return customer, document, nil
}

func (s *Service) ReconcileBusinessService(ctx context.Context, serviceName string) (int, error) {
	return s.reconcileBusinessService(ctx, serviceName, nil)
}

func (s *Service) ReconcileBusinessServiceForCustomerIDs(ctx context.Context, serviceName string, customerIDs []string) (int, error) {
	if len(customerIDs) == 0 {
		return 0, errors.New("controlplane: missing reconcile customers")
	}
	ids := make([]string, 0, len(customerIDs))
	for _, customerID := range customerIDs {
		customerID = strings.TrimSpace(customerID)
		if customerID == "" {
			return 0, errors.New("controlplane: invalid reconcile customer")
		}
		ids = append(ids, customerID)
	}
	return s.reconcileBusinessService(ctx, serviceName, ids)
}

func (s *Service) reconcileBusinessService(ctx context.Context, serviceName string, customerIDs []string) (int, error) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return 0, errors.New("controlplane: invalid service")
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT node_id FROM node_services
WHERE service_name=? AND desired_target=1 AND apply_enabled=1 AND fenced=0 AND retired=0
ORDER BY node_id`, Args: []any{serviceName}})
	if err != nil || len(results) != 1 {
		return 0, ErrUnavailable
	}
	count := 0
	for _, row := range results[0].Rows {
		nodeID, ok := rowString(row, "node_id")
		if !ok {
			return 0, ErrUnavailable
		}
		if len(customerIDs) == 0 {
			if _, err := s.ReconcileNode(ctx, ReconcileNodeCommand{NodeID: nodeID, ServiceName: serviceName}); err != nil {
				return count, err
			}
		} else {
			for _, customerID := range customerIDs {
				if _, err := s.ReconcileNode(ctx, ReconcileNodeCommand{NodeID: nodeID, ServiceName: serviceName, CustomerID: customerID}); err != nil {
					return count, err
				}
			}
		}
		count++
	}
	return count, nil
}

func (s *Service) MigrateBusinessServiceEndpoint(ctx context.Context, serviceName, endpoint, actor string) (int, error) {
	if strings.TrimSpace(serviceName) == "" || strings.TrimSpace(endpoint) == "" {
		return 0, errors.New("controlplane: invalid service endpoint")
	}
	key := "service_endpoint." + serviceName
	current, err := s.ReadBusinessSetting(ctx, key)
	if errors.Is(err, ErrNotFound) {
		current = BusinessSetting{Generation: 0}
	} else if err != nil {
		return 0, err
	}
	value, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	if _, err := s.UpdateSetting(ctx, SettingUpdate{Key: key, ExpectedGeneration: current.Generation, PublicValueJSON: string(value), Actor: actor}); err != nil {
		return 0, err
	}
	return s.ReconcileBusinessService(ctx, serviceName)
}

func (s *Service) MigrateBusinessServiceEndpointIdempotent(
	ctx context.Context, serviceName, endpoint, actor, idempotencyKey string,
) (int, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return 0, errors.New("controlplane: missing migration idempotency key")
	}
	key := "service_endpoint." + strings.TrimSpace(serviceName)
	current, err := s.ReadBusinessSetting(ctx, key)
	if errors.Is(err, ErrNotFound) {
		current = BusinessSetting{Generation: 0}
	} else if err != nil {
		return 0, err
	}
	value, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	if _, err := s.UpdateSetting(ctx, SettingUpdate{Key: key, ExpectedGeneration: current.Generation,
		PublicValueJSON: string(value), Actor: actor, CommandType: "setting.service_endpoint.migrate",
		IdempotencyKey: idempotencyKey}); err != nil {
		return 0, err
	}
	return s.ReconcileBusinessService(ctx, serviceName)
}

func (s *Service) PrepareExternalAction(ctx context.Context, command ExternalActionCommand) (ExternalActionResult, error) {
	store, err := NewRQLiteExternalActions(s)
	if err != nil {
		return ExternalActionResult{}, err
	}
	return store.Prepare(ctx, command)
}

func (s *Service) BusinessClusterStatus(ctx context.Context) (ready, quorum bool, err error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT COUNT(*) AS voters,
COALESCE(SUM(CASE WHEN enabled=1 THEN 1 ELSE 0 END),0) AS enabled
FROM nodes WHERE is_voter=1`})
	if err != nil {
		return false, false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return false, false, ErrUnavailable
	}
	voters, votersOK := rowInt64(row, "voters")
	enabled, enabledOK := rowInt64(row, "enabled")
	if !votersOK || !enabledOK || voters == 0 {
		return false, false, ErrUnavailable
	}
	quorum = enabled >= voters/2+1
	return quorum, quorum, nil
}

func (s *Service) RecentBusinessAudit(ctx context.Context, limit int) ([]BusinessAuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT event_id,action,created_at_unix FROM audit_events
ORDER BY created_at_unix DESC,event_id DESC LIMIT ?`, Args: []any{limit}})
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}
	events := make([]BusinessAuditEvent, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		id, idOK := rowString(row, "event_id")
		action, actionOK := rowString(row, "action")
		created, createdOK := rowInt64(row, "created_at_unix")
		if !idOK || !actionOK || !createdOK {
			return nil, ErrUnavailable
		}
		events = append(events, BusinessAuditEvent{ID: id, Action: action, CreatedAt: time.Unix(created, 0).UTC()})
	}
	return events, nil
}

func (s *Service) AuthenticatePassword(ctx context.Context, password string) (SessionResult, error) {
	if password == "" {
		return SessionResult{}, ErrForbidden
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT p.principal_id,pc.verifier_envelope
FROM principals p JOIN principal_credentials pc ON pc.principal_id=p.principal_id
JOIN principal_roles pr ON pr.principal_id=p.principal_id
WHERE p.status='active' AND pc.credential_type='password' AND pc.active=1
AND pr.role_name IN ('owner','admin') ORDER BY CASE pr.role_name WHEN 'owner' THEN 0 ELSE 1 END,p.principal_id LIMIT 1`})
	if err != nil {
		return SessionResult{}, ErrForbidden
	}
	row, ok := firstRow(results)
	if !ok {
		return SessionResult{}, ErrForbidden
	}
	principalID, idOK := rowString(row, "principal_id")
	if !idOK || s.verifyPrincipalPassword(row, principalID, password) != nil {
		return SessionResult{}, ErrForbidden
	}
	return s.CreateSession(ctx, principalID)
}

func (s *Service) verifyPrincipalPassword(row map[string]any, principalID, password string) error {
	encoded, ok := rowString(row, "verifier_envelope")
	if !ok {
		return ErrForbidden
	}
	bytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ErrForbidden
	}
	var envelope Envelope
	if err := json.Unmarshal(bytes, &envelope); err != nil {
		return ErrForbidden
	}
	verifier, err := s.store.secrets.Open(SecretScope{OwnerType: "principal", OwnerID: principalID, Field: "password", Kind: "bcrypt"}, envelope)
	if err != nil || bcrypt.CompareHashAndPassword(verifier, []byte(password)) != nil {
		return ErrForbidden
	}
	return nil
}

func (s *Service) ChangePrincipalPassword(ctx context.Context, principalID, current, next, idempotencyKey string) error {
	if principalID == "" || current == "" || len(next) < 12 || idempotencyKey == "" {
		return ErrForbidden
	}
	requestHash := s.store.secrets.LookupHMAC("password-change-request", []byte(principalID+"\x00"+current+"\x00"+next))
	replay, err := s.passwordChangeReplay(ctx, principalID, idempotencyKey, requestHash)
	if err != nil || replay {
		return err
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT verifier_envelope FROM principal_credentials
WHERE principal_id=? AND credential_type='password' AND active=1
ORDER BY created_at_unix DESC LIMIT 1`, Args: []any{principalID}})
	if err != nil {
		return ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok || s.verifyPrincipalPassword(row, principalID, current) != nil {
		return ErrForbidden
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return ErrUnavailable
	}
	envelope, err := s.store.secrets.Seal(SecretScope{OwnerType: "principal", OwnerID: principalID, Field: "password", Kind: "bcrypt"}, hash)
	if err != nil {
		return err
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return ErrUnavailable
	}
	digest := sha256.Sum256(envelopeBytes)
	credentialID, err := s.ids.NewID("principal-password")
	if err != nil {
		return ErrUnavailable
	}
	operationID, err := s.ids.NewID("password-change")
	if err != nil {
		return ErrUnavailable
	}
	now := s.clock.Now().Unix()
	guard := `EXISTS(SELECT 1 FROM idempotency_requests WHERE scope='principal' AND command_type='change_password' AND idempotency_key=? AND operation_id=? AND status='applying')`
	guardArgs := []any{idempotencyKey, operationID}
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,resource_id,decision,operation_id,status,response_json,created_at_unix,applied_at_unix)
VALUES('principal','change_password',?,?,?,'accepted',?,'applying',NULL,?,NULL)
ON CONFLICT(scope,command_type,idempotency_key) DO NOTHING RETURNING operation_id`,
		Args: []any{idempotencyKey, requestHash, principalID, operationID, now},
	}, {
		SQL:  `UPDATE principal_credentials SET active=0 WHERE principal_id=? AND credential_type='password' AND active=1 AND ` + guard,
		Args: append([]any{principalID}, guardArgs...),
	}, {
		SQL: `INSERT INTO principal_credentials(credential_id,principal_id,credential_type,verifier_envelope,verifier_sha256,active,created_at_unix)
SELECT ?,?,'password',?,?,1,? WHERE ` + guard,
		Args: append([]any{credentialID, principalID, envelopeBytes, hex.EncodeToString(digest[:]), now}, guardArgs...),
	}, {
		SQL:  `UPDATE principals SET revocation_epoch=revocation_epoch+1 WHERE principal_id=? AND ` + guard,
		Args: append([]any{principalID}, guardArgs...),
	}, {
		SQL:  `UPDATE web_sessions SET revoked_at_unix=? WHERE principal_id=? AND revoked_at_unix IS NULL AND ` + guard,
		Args: append([]any{now, principalID}, guardArgs...),
	}, backupRPODirtyGenerationStatement(now), {
		SQL: `UPDATE idempotency_requests SET status='applied',response_json='{}',applied_at_unix=?
WHERE scope='principal' AND command_type='change_password' AND idempotency_key=? AND operation_id=? AND status='applying' RETURNING operation_id`,
		Args: []any{now, idempotencyKey, operationID},
	}}
	requestResults, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return ErrUnavailable
	}
	if len(requestResults) != len(statements) || len(requestResults[len(requestResults)-1].Rows) != 1 {
		replay, resolveErr := s.passwordChangeReplay(ctx, principalID, idempotencyKey, requestHash)
		if resolveErr != nil || !replay {
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) passwordChangeReplay(ctx context.Context, principalID, key, requestHash string) (bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,status FROM idempotency_requests
WHERE scope='principal' AND command_type='change_password' AND idempotency_key=?`, Args: []any{key}})
	if err != nil {
		return false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return false, nil
	}
	storedHash, hashOK := rowString(row, "request_hash")
	resourceID, resourceOK := rowString(row, "resource_id")
	status, statusOK := rowString(row, "status")
	if !hashOK || !resourceOK || !statusOK || storedHash != requestHash || resourceID != principalID {
		return false, ErrConflict
	}
	if status != "applied" {
		return false, ErrUnavailable
	}
	return true, nil
}

func sortedCredentialProtocols(access CustomerAccess) []string {
	protocols := make([]string, 0, len(access.Credentials))
	for protocol := range access.Credentials {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	return protocols
}
