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
	ExpiresAt      time.Time
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

type ResetDevicesCommand struct {
	Login          string
	IdempotencyKey string
}

type RedeemTrialCommand struct {
	Login          string
	Anchor         string
	DRMIdentity    string
	Days           int
	IdempotencyKey string
}

type customerMutation struct {
	commandType   string
	login         string
	idempotency   string
	days          int
	expiresAt     int64
	status        string
	allowCreate   bool
	requireNew    bool
	resetDevices  bool
	tombstone     bool
	trialAnchor   string
	trialDevice   string
	trialIdentity *trialMutationIdentity
}

type storedCustomerResponse struct {
	CustomerID    string `json:"customer_id"`
	Status        string `json:"status"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
	Generation    int64  `json:"generation"`
}

func (s *Service) ProvisionCustomer(ctx context.Context, command ProvisionCustomerCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.provision", login: command.Login, idempotency: command.IdempotencyKey,
		days: command.Days, status: "active", allowCreate: true, requireNew: true,
	})
}

func (s *Service) ExtendCustomer(ctx context.Context, command ExtendCustomerCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.extend", login: command.Login, idempotency: command.IdempotencyKey,
		days: command.Days, status: "active",
	})
}

func (s *Service) RenewCustomer(ctx context.Context, command RenewCustomerCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.renew", login: command.Login, idempotency: command.IdempotencyKey,
		days: command.Days, status: "active",
	})
}

func (s *Service) SetCustomerExpiry(ctx context.Context, command SetExpiryCommand) (Customer, error) {
	return s.mutateCustomer(ctx, customerMutation{
		commandType: "customer.set-expiry", login: command.Login, idempotency: command.IdempotencyKey,
		expiresAt: command.ExpiresAt.Unix(), status: "active",
	})
}

func (s *Service) mutateCustomer(ctx context.Context, mutation customerMutation) (Customer, error) {
	canonical, err := CanonicalLoginKey(mutation.login)
	if err != nil || strings.TrimSpace(mutation.idempotency) == "" {
		return Customer{}, errors.New("controlplane: invalid customer command")
	}
	if mutation.days < 0 || (mutation.days == 0 && mutation.expiresAt == 0 && mutation.status == "active" && (mutation.commandType != "customer.enable" && mutation.commandType != "customer.reset-devices")) {
		return Customer{}, errors.New("controlplane: invalid customer duration")
	}
	requestHash, err := customerMutationHash(mutation, canonical)
	if err != nil {
		return Customer{}, err
	}
	scope := "customer:" + s.store.secrets.LookupHMAC("customer-login", []byte(canonical))
	if saved, ok, resolveErr := s.resolveCustomerMutation(ctx, scope, mutation, requestHash); resolveErr != nil || ok {
		return saved, resolveErr
	}

	current, exists, err := s.customerForMutation(ctx, canonical)
	if err != nil {
		return Customer{}, err
	}
	if mutation.requireNew && exists {
		return Customer{}, ErrConflict
	}
	if !exists && !mutation.allowCreate {
		return Customer{}, ErrNotFound
	}
	if exists {
		current.Access, err = s.customerAccess(ctx, current.ID)
		if err != nil {
			return Customer{}, err
		}
	}
	if !exists {
		current.ID, err = s.ids.NewID("customer")
		if err != nil {
			return Customer{}, errors.New("controlplane: generate customer identifier")
		}
		current.Status = "active"
	}

	next := current
	next.Generation = current.Generation + 1
	if mutation.status != "" {
		next.Status = mutation.status
	}
	now := s.clock.Now().Unix()
	switch mutation.commandType {
	case "customer.provision", "trial.redeem":
		next.ExpiresAtUnix = now + int64(mutation.days)*86400
	case "customer.extend":
		next.ExpiresAtUnix = current.ExpiresAtUnix + int64(mutation.days)*86400
	case "customer.renew":
		base := current.ExpiresAtUnix
		if base < now {
			base = now
		}
		next.ExpiresAtUnix = base + int64(mutation.days)*86400
	case "customer.set-expiry":
		next.ExpiresAtUnix = mutation.expiresAt
	default:
		next.ExpiresAtUnix = current.ExpiresAtUnix
	}
	if next.ExpiresAtUnix < 0 {
		return Customer{}, errors.New("controlplane: invalid customer expiry")
	}

	var access customerAccessMint
	if !exists {
		access, err = s.mintCustomerAccess(next.ID)
		if err != nil {
			return Customer{}, err
		}
		next.Access = access.Access
	}

	operationID, err := s.ids.NewID("operation")
	if err != nil {
		return Customer{}, errors.New("controlplane: generate operation identifier")
	}
	targets, err := s.customerMutationTargets(ctx, next, operationID, mutation.tombstone)
	if err != nil {
		return Customer{}, err
	}
	if mutation.trialAnchor != "" {
		mutation.trialIdentity, err = s.trialIdentityForMutation(ctx, mutation.trialAnchor, mutation.trialDevice)
		if err != nil {
			return Customer{}, err
		}
	}
	statements, err := s.customerMutationStatements(scope, canonical, mutation, requestHash, current, next, exists, operationID, targets, access, now)
	if err != nil {
		return Customer{}, err
	}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if err != nil {
		return Customer{}, ErrUnavailable
	}
	result, err := customerMutationResponse(results, requestHash)
	if err == nil {
		result.Access = next.Access
	}
	return result, err
}

func customerMutationHash(mutation customerMutation, canonical string) (string, error) {
	payload, err := json.Marshal(struct {
		CommandType string `json:"command_type"`
		Login       string `json:"login"`
		Days        int    `json:"days,omitempty"`
		ExpiresAt   int64  `json:"expires_at_unix,omitempty"`
		Status      string `json:"status"`
		Reset       bool   `json:"reset_devices,omitempty"`
		Anchor      string `json:"anchor,omitempty"`
		Device      string `json:"device,omitempty"`
	}{mutation.commandType, canonical, mutation.days, mutation.expiresAt, mutation.status, mutation.resetDevices, mutation.trialAnchor, mutation.trialDevice})
	if err != nil {
		return "", errors.New("controlplane: encode customer command")
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) resolveCustomerMutation(ctx context.Context, scope string, mutation customerMutation, requestHash string) (Customer, bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT request_hash,status,response_json FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`,
		Args: []any{scope, mutation.commandType, mutation.idempotency},
	})
	if err != nil {
		return Customer{}, false, ErrUnavailable
	}
	if len(results) == 0 || len(results[0].Rows) == 0 {
		return Customer{}, false, nil
	}
	row := results[0].Rows[0]
	storedHash, hashOK := rowString(row, "request_hash")
	status, statusOK := rowString(row, "status")
	responseJSON, responseOK := rowString(row, "response_json")
	if !hashOK || storedHash != requestHash {
		return Customer{}, true, ErrConflict
	}
	if !statusOK || status != "applied" || !responseOK {
		return Customer{}, true, ErrUnavailable
	}
	customer, err := decodeStoredCustomer(responseJSON)
	if err != nil {
		return Customer{}, true, err
	}
	access, err := s.customerAccess(ctx, customer.ID)
	if err != nil {
		return Customer{}, true, err
	}
	customer.Access = access
	return customer, true, nil
}

func (s *Service) customerForMutation(ctx context.Context, canonical string) (Customer, bool, error) {
	lookup := s.store.secrets.LookupHMAC("customer-login", []byte(canonical))
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL:  `SELECT customer_id,status,expires_at_unix,generation FROM customers WHERE login_key_hmac=?`,
		Args: []any{lookup},
	})
	if err != nil {
		return Customer{}, false, ErrUnavailable
	}
	if len(results) == 0 || len(results[0].Rows) == 0 {
		return Customer{}, false, nil
	}
	row := results[0].Rows[0]
	id, idOK := rowString(row, "customer_id")
	status, statusOK := rowString(row, "status")
	expires, expiresOK := rowInt64(row, "expires_at_unix")
	generation, generationOK := rowInt64(row, "generation")
	if !idOK || !statusOK || !expiresOK || !generationOK {
		return Customer{}, false, ErrUnavailable
	}
	return Customer{ID: id, Status: status, ExpiresAtUnix: expires, Generation: generation}, true, nil
}

func (s *Service) customerMutationTargets(ctx context.Context, customer Customer, operationID string, tombstone bool) ([]desiredTarget, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT node_id,service_name FROM node_services
WHERE desired_target=1 AND retired=0 ORDER BY node_id,service_name`,
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) == 0 {
		return nil, ErrUnavailable
	}
	targets := make([]desiredTarget, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		nodeID, nodeOK := rowString(row, "node_id")
		serviceName, serviceOK := rowString(row, "service_name")
		if !nodeOK || !serviceOK {
			return nil, ErrUnavailable
		}
		kind := "customer-active"
		if tombstone || customer.Status != "active" {
			kind = "customer-revoked"
		}
		payload := map[string]any{"expires_at_unix": customer.ExpiresAtUnix, "status": customer.Status}
		if access := accessPayload(customer.Access); access != nil {
			payload["access"] = access
		}
		envelope, digest, sealErr := s.store.secrets.SealDesiredPayload(DesiredPayloadScope{
			NodeID: nodeID, ServiceID: serviceName, CustomerID: customer.ID, Generation: customer.Generation,
			OperationID: operationID, PayloadKind: kind, Tombstone: tombstone,
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

func (s *Service) customerMutationStatements(
	scope, canonical string,
	mutation customerMutation,
	requestHash string,
	current, next Customer,
	exists bool,
	operationID string,
	targets []desiredTarget,
	access customerAccessMint,
	now int64,
) ([]rqlite.Statement, error) {
	loginHMAC := s.store.secrets.LookupHMAC("customer-login", []byte(canonical))
	claim := rqlite.Statement{
		SQL: `INSERT OR IGNORE INTO idempotency_requests(scope,command_type,idempotency_key,request_hash,
resource_id,decision,operation_id,status,created_at_unix)
SELECT ?,?,?,?,?,?,?,'applying',?`,
		Args: []any{scope, mutation.commandType, mutation.idempotency, requestHash, next.ID, next.Status, operationID, now},
	}
	if mutation.trialIdentity != nil {
		eligible, args := mutation.trialIdentity.eligibility()
		claim.SQL += " WHERE " + eligible
		claim.Args = append(claim.Args, args...)
	}
	statements := []rqlite.Statement{claim}
	guard := `EXISTS (SELECT 1 FROM idempotency_requests WHERE scope=? AND command_type=?
AND idempotency_key=? AND request_hash=? AND status='applying')`
	guardArgs := []any{scope, mutation.commandType, mutation.idempotency, requestHash}
	if exists {
		var sql string
		var args []any
		switch mutation.commandType {
		case "customer.extend":
			sql = `UPDATE customers SET display_login=?,status=?,expires_at_unix=expires_at_unix + ?,generation=?,updated_at_unix=?
WHERE customer_id=? AND generation=? AND `
			args = []any{mutation.login, next.Status, int64(mutation.days) * 86400, next.Generation, now, current.ID, current.Generation}
		case "customer.renew":
			sql = `UPDATE customers SET display_login=?,status=?,expires_at_unix=max(expires_at_unix, ?) + ?,generation=?,updated_at_unix=?
WHERE customer_id=? AND generation=? AND `
			args = []any{mutation.login, next.Status, now, int64(mutation.days) * 86400, next.Generation, now, current.ID, current.Generation}
		default:
			sql = `UPDATE customers SET display_login=?,status=?,expires_at_unix=?,generation=?,updated_at_unix=?
WHERE customer_id=? AND generation=? AND `
			args = []any{mutation.login, next.Status, next.ExpiresAtUnix, next.Generation, now, current.ID, current.Generation}
		}
		statements = append(statements, rqlite.Statement{
			SQL:  sql + guard,
			Args: append(args, guardArgs...),
		})
	} else {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix)
SELECT ?,?,?,?,?,?,?,? WHERE ` + guard,
			Args: append([]any{next.ID, mutation.login, loginHMAC, next.Status, next.ExpiresAtUnix, next.Generation, now, now}, guardArgs...),
		})
		statements = append(statements, access.statements(next, now, guard, guardArgs, s.store.secrets)...)
	}
	if mutation.trialAnchor != "" {
		redemptionID, err := s.ids.NewID("trial")
		if err != nil {
			return nil, errors.New("controlplane: generate trial redemption")
		}
		if mutation.trialIdentity == nil {
			return nil, ErrUnavailable
		}
		identity := mutation.trialIdentity
		deviceHMAC := identity.deviceHMAC
		if deviceHMAC == "" {
			// A missing DRM identity must not put all clients into one bucket.
			deviceHMAC = identity.anchorHMAC
		}
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO trial_redemptions(redemption_id,customer_id,trial_code_hmac,device_key_hmac,redeemed_at_unix,duration_days)
SELECT ?,?,?,?,?,? WHERE ` + guard,
			Args: append([]any{redemptionID, next.ID, identity.anchorHMAC, deviceHMAC, now, mutation.days}, guardArgs...),
		}, rqlite.Statement{
			// The status CHECK aborts the whole transaction if a claimed trial
			// did not insert exactly one redemption (including RAISE(IGNORE)).
			SQL: `UPDATE idempotency_requests SET status='trial-redemption-rejected'
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=?
AND status='applying' AND changes()<>1`,
			Args: guardArgs,
		})
	}
	if mutation.resetDevices {
		statements = append(statements, rqlite.Statement{
			SQL:  `UPDATE devices SET revoked=1 WHERE customer_id=? AND ` + guard,
			Args: append([]any{next.ID}, guardArgs...),
		})
	}
	for _, target := range targets {
		statements = append(statements,
			rqlite.Statement{SQL: `INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,
desired_envelope,desired_sha256,status,updated_at_unix)
SELECT ?,?,?,?,?,?,'pending',? WHERE ` + guard + ` AND EXISTS
(SELECT 1 FROM customers WHERE customer_id=? AND generation=?)
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET generation=excluded.generation,
desired_envelope=excluded.desired_envelope,desired_sha256=excluded.desired_sha256,status='pending',updated_at_unix=excluded.updated_at_unix
WHERE excluded.generation >= desired_node_state.generation`, Args: append([]any{
				next.ID, target.NodeID, target.ServiceName, next.Generation, target.EnvelopeBytes, target.Digest, now,
			}, append(guardArgs, next.ID, next.Generation)...)},
			rqlite.Statement{SQL: `INSERT OR IGNORE INTO outbox_events(event_id,aggregate_type,aggregate_id,generation,event_type,
payload_envelope,payload_sha256,status,available_at_unix,attempts,created_at_unix)
SELECT ?,'customer',?,?,?, ?,?,'pending',?,0,? WHERE ` + guard + ` AND EXISTS
(SELECT 1 FROM customers WHERE customer_id=? AND generation=?)`, Args: append([]any{
				target.EventID, next.ID, next.Generation, mutation.commandType, target.EnvelopeBytes, target.Digest, now, now,
			}, append(guardArgs, next.ID, next.Generation)...)},
		)
	}
	responseBytes, err := json.Marshal(storedCustomerResponse{
		CustomerID: next.ID, Status: next.Status, ExpiresAtUnix: next.ExpiresAtUnix, Generation: next.Generation,
	})
	if err != nil {
		return nil, errors.New("controlplane: encode customer response")
	}
	statements = append(statements,
		rqlite.Statement{SQL: `UPDATE idempotency_requests SET status='applied',response_json=?,applied_at_unix=?
WHERE scope=? AND command_type=? AND idempotency_key=? AND request_hash=? AND status='applying'
AND EXISTS (SELECT 1 FROM customers WHERE customer_id=? AND generation=?)`, Args: []any{
			string(responseBytes), now, scope, mutation.commandType, mutation.idempotency, requestHash, next.ID, next.Generation,
		}},
		rqlite.Statement{SQL: `DELETE FROM idempotency_requests WHERE scope=? AND command_type=? AND idempotency_key=?
AND request_hash=? AND status='applying'`, Args: guardArgs},
		rqlite.Statement{SQL: `SELECT request_hash,status,response_json FROM idempotency_requests
WHERE scope=? AND command_type=? AND idempotency_key=?`, Args: []any{scope, mutation.commandType, mutation.idempotency}},
	)
	return statements, nil
}

func customerMutationResponse(results []rqlite.Result, requestHash string) (Customer, error) {
	for i := len(results) - 1; i >= 0; i-- {
		for _, row := range results[i].Rows {
			if responseJSON, ok := rowString(row, "response_json"); ok {
				storedHash, hashOK := rowString(row, "request_hash")
				status, statusOK := rowString(row, "status")
				if !hashOK || storedHash != requestHash {
					return Customer{}, ErrConflict
				}
				if !statusOK || status != "applied" {
					return Customer{}, ErrUnavailable
				}
				return decodeStoredCustomer(responseJSON)
			}
			id, idOK := rowString(row, "customer_id")
			status, statusOK := rowString(row, "status")
			expires, expiresOK := rowInt64(row, "expires_at_unix")
			generation, generationOK := rowInt64(row, "generation")
			if idOK && statusOK && expiresOK && generationOK {
				return Customer{ID: id, Status: status, ExpiresAtUnix: expires, Generation: generation}, nil
			}
		}
	}
	return Customer{}, ErrConflict
}

func decodeStoredCustomer(responseJSON string) (Customer, error) {
	var response storedCustomerResponse
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil || response.CustomerID == "" {
		return Customer{}, ErrUnavailable
	}
	return Customer{ID: response.CustomerID, Status: response.Status, ExpiresAtUnix: response.ExpiresAtUnix, Generation: response.Generation}, nil
}
