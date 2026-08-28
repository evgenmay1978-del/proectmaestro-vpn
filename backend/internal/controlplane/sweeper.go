package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	expiryLeaseTTL     = 90 * time.Second
	expiryLeaseRenewal = 30 * time.Second
	expiryScanInterval = 60 * time.Second
	expiryBatchLimit   = 100
)

type expiryCustomer struct {
	CustomerID        string
	PriorGeneration   int64
	ExpiredGeneration int64
}

type expiryDesiredTarget struct {
	CustomerID    string
	Generation    int64
	NodeID        string
	ServiceName   string
	EnvelopeBytes []byte
	Digest        string
	EventID       string
}

func (s *Service) RunExpirySweep(ctx context.Context, command ExpirySweepCommand) (ExpirySweepResult, error) {
	if strings.TrimSpace(command.WorkerID) == "" {
		return ExpirySweepResult{}, errors.New("controlplane: expiry worker is required")
	}
	lease, err := s.acquireExpiryLease(ctx, command.WorkerID)
	if err != nil {
		return ExpirySweepResult{}, err
	}
	if err := s.ExpireDueOrders(ctx, lease); err != nil {
		return ExpirySweepResult{}, err
	}
	result, err := s.ExpireDueCustomers(ctx, lease)
	if err != nil {
		return ExpirySweepResult{}, err
	}
	result.LeaseFence = lease.LeaseFence
	return result, nil
}

func (s *Service) acquireExpiryLease(ctx context.Context, workerID string) (ExpiryLease, error) {
	leaseToken, err := s.ids.NewID("expiry-lease")
	if err != nil {
		return ExpiryLease{}, errors.New("controlplane: generate expiry lease")
	}
	results, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{
		SQL: `INSERT INTO cluster_job_leases(job_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,lease_fence)
SELECT 'expiry-sweeper',?,?,unixepoch(),unixepoch()+?,1 FROM cluster_restore_state
WHERE singleton_id=1 AND activated=1
ON CONFLICT(job_name) DO UPDATE SET
holder_id=excluded.holder_id,lease_token=excluded.lease_token,acquired_at_unix=excluded.acquired_at_unix,
expires_at_unix=excluded.expires_at_unix,
lease_fence=cluster_job_leases.lease_fence+1
WHERE cluster_job_leases.holder_id=excluded.holder_id OR cluster_job_leases.expires_at_unix<=unixepoch()
RETURNING lease_fence`,
		Args: []any{workerID, leaseToken, int64(expiryLeaseTTL / time.Second)},
	})
	if requestErr == nil {
		row, ok := firstRow(results)
		if ok {
			fence, fenceOK := rowInt64(row, "lease_fence")
			if fenceOK && fence > 0 {
				return ExpiryLease{WorkerID: workerID, LeaseFence: fence}, nil
			}
		}
		active, activeErr := s.readExpiryLease(ctx)
		if activeErr == nil && active.WorkerID != "" {
			return ExpiryLease{}, ErrLeaseHeld
		}
		return ExpiryLease{}, ErrUnavailable
	}
	resolved, resolveErr := s.readExpiryLeaseToken(ctx, workerID, leaseToken)
	if resolveErr == nil && resolved.LeaseFence > 0 {
		return resolved, nil
	}
	active, activeErr := s.readExpiryLease(ctx)
	if activeErr == nil && active.WorkerID != "" && active.WorkerID != workerID {
		return ExpiryLease{}, ErrLeaseHeld
	}
	return ExpiryLease{}, ErrUnavailable
}

func (s *Service) ExpireDueOrders(ctx context.Context, lease ExpiryLease) error {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT order_id FROM orders WHERE payment_state IN ('created','payment_claimed')
AND expires_at_unix<=unixepoch() ORDER BY expires_at_unix,order_id LIMIT ?`,
		Args: []any{expiryBatchLimit},
	})
	if err != nil || len(results) != 1 {
		return ErrUnavailable
	}
	for _, row := range results[0].Rows {
		orderID, ok := rowString(row, "order_id")
		if !ok {
			return ErrUnavailable
		}
		if err := s.expireDueOrderFenced(ctx, lease, orderID); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) expireDueOrderFenced(ctx context.Context, lease ExpiryLease, orderID string) error {
	order, err := s.queryOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if order.ExpiresAtUnix > order.DBNow || (order.View.PaymentState != PaymentPending && order.View.PaymentState != PaymentClaimed) {
		return nil
	}
	operationID, err := s.ids.NewID("expiry-order")
	if err != nil {
		return errors.New("controlplane: generate order expiry operation")
	}
	now := s.clock.Now().Unix()
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO operations(operation_id,operation_type,resource_type,resource_id,status,
requested_by_hmac,created_at_unix,updated_at_unix,lease_holder_id,lease_fence)
VALUES(?,'expiry-orders','order',?,'applying',?,unixepoch(),unixepoch(),?,?)`,
		Args: []any{operationID, orderID, s.auditActor(lease.WorkerID), lease.WorkerID, lease.LeaseFence},
	}}
	if order.View.PaymentState == PaymentPending {
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE orders SET payment_state='expired',provisioning_state='none',decision='expired',operation_id=?
WHERE order_id=? AND payment_state='created' AND decision IS NULL AND expires_at_unix<=unixepoch()`,
			Args: []any{operationID, orderID},
		}, backupRPODirtyGenerationStatement(now), rqlite.Statement{
			SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,'order.expire','order',?,unixepoch() WHERE EXISTS(
SELECT 1 FROM orders WHERE order_id=? AND operation_id=? AND payment_state='expired')`,
			Args: []any{auditID("order-sweep-expire", orderID, lease.LeaseFence, now), s.auditActor(lease.WorkerID),
				s.auditResource(orderID), orderID, operationID},
		})
	} else {
		delivery, deliveryErr := s.telegramDelivery("stale-owner-claim:"+orderID, orderID, "stale_owner_claim", order.View.OriginBotID, order.OriginChatHMAC)
		if deliveryErr != nil {
			return deliveryErr
		}
		if delivery != nil {
			statements = append(statements, *delivery, backupRPODirtyGenerationStatement(now))
		}
	}
	statements = append(statements, rqlite.Statement{
		SQL: `UPDATE operations SET status='applied',updated_at_unix=unixepoch()
WHERE operation_id=? AND status='applying'`, Args: []any{operationID},
	})
	_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
	if requestErr == nil {
		return nil
	}
	if applied, resolveErr := s.expiryOperationApplied(ctx, operationID); resolveErr == nil && applied {
		return nil
	}
	if lost, leaseErr := s.expiryLeaseLost(ctx, lease); leaseErr == nil && lost {
		return ErrLeaseLost
	}
	if isUnknownWrite(requestErr) {
		return ErrUnavailable
	}
	return orderDecisionConflict(requestErr)
}

func expiryCustomerOperationID(customerID string, generation int64) string {
	digest := sha256.Sum256([]byte("expiry-customers\x00" + customerID + "\x00" + strconv.FormatInt(generation, 10)))
	return "expiry_customer_" + hex.EncodeToString(digest[:16])
}

func (s *Service) ExpireDueCustomers(ctx context.Context, lease ExpiryLease) (ExpirySweepResult, error) {
	results, err := s.store.db.QueryLinearizable(ctx,
		rqlite.Statement{SQL: `SELECT customer_id,generation FROM customers
WHERE status='active' AND expires_at_unix<=unixepoch() ORDER BY expires_at_unix,customer_id LIMIT ?`, Args: []any{expiryBatchLimit}},
		rqlite.Statement{SQL: `SELECT node_id,service_name FROM node_services
WHERE desired_target=1 AND retired=0 ORDER BY node_id,service_name`},
	)
	if err != nil || len(results) != 2 {
		return ExpirySweepResult{}, ErrUnavailable
	}
	if len(results[0].Rows) == 0 {
		return ExpirySweepResult{LeaseFence: lease.LeaseFence}, nil
	}
	if len(results[1].Rows) == 0 {
		return ExpirySweepResult{}, ErrUnavailable
	}
	customers := make([]expiryCustomer, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		customerID, idOK := rowString(row, "customer_id")
		generation, generationOK := rowInt64(row, "generation")
		if !idOK || !generationOK {
			return ExpirySweepResult{}, ErrUnavailable
		}
		customers = append(customers, expiryCustomer{CustomerID: customerID, PriorGeneration: generation, ExpiredGeneration: generation + 1})
	}
	sweepResult := ExpirySweepResult{LeaseFence: lease.LeaseFence}
	for _, customer := range customers {
		operationID := expiryCustomerOperationID(customer.CustomerID, customer.ExpiredGeneration)
		targets, targetErr := s.expiryTargets(results[1].Rows, []expiryCustomer{customer}, operationID)
		if targetErr != nil {
			return sweepResult, targetErr
		}
		now := s.clock.Now().Unix()
		statements := []rqlite.Statement{{
			SQL: `INSERT INTO operations(operation_id,operation_type,resource_type,resource_id,status,
requested_by_hmac,created_at_unix,updated_at_unix,lease_holder_id,lease_fence)
VALUES(?,'expiry-customers','customer',?,'applying',?,unixepoch(),unixepoch(),?,?)`,
			Args: []any{operationID, customer.CustomerID, s.auditActor(lease.WorkerID), lease.WorkerID, lease.LeaseFence},
		}}
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE customers SET status='expired',generation=?,updated_at_unix=unixepoch()
WHERE customer_id=? AND status='active' AND generation=? AND expires_at_unix<=unixepoch()`,
			Args: []any{customer.ExpiredGeneration, customer.CustomerID, customer.PriorGeneration},
		}, rqlite.Statement{
			SQL: `INSERT INTO operation_batches(batch_id,operation_id,sequence_no,status,created_at_unix)
SELECT ?,?,?,'applying',unixepoch() WHERE changes()=1`,
			Args: []any{customer.CustomerID, operationID, customer.ExpiredGeneration},
		}, backupRPODirtyGenerationStatement(now))
		for _, target := range targets {
			statements = append(statements, rqlite.Statement{
				SQL: `INSERT INTO desired_node_state(customer_id,node_id,service_name,generation,desired_envelope,
desired_sha256,status,updated_at_unix,tombstone,operation_id)
SELECT ?,?,?,?,?,?,'pending',unixepoch(),1,? WHERE EXISTS(
SELECT 1 FROM operation_batches WHERE batch_id=? AND operation_id=? AND sequence_no=? AND status='applying')
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET generation=excluded.generation,
desired_envelope=excluded.desired_envelope,desired_sha256=excluded.desired_sha256,status='pending',
updated_at_unix=excluded.updated_at_unix,tombstone=1,operation_id=excluded.operation_id
WHERE desired_node_state.generation<excluded.generation`,
				Args: []any{customer.CustomerID, target.NodeID, target.ServiceName, customer.ExpiredGeneration,
					target.EnvelopeBytes, target.Digest, operationID, customer.CustomerID, operationID, customer.ExpiredGeneration},
			}, rqlite.Statement{
				SQL: `INSERT INTO outbox_events(event_id,aggregate_type,aggregate_id,generation,event_type,
payload_envelope,payload_sha256,status,available_at_unix,attempts,created_at_unix,
node_id,service_name,operation_id,event_kind)
SELECT ?,'customer',?,?,'customer.revoke',?,?,'pending',unixepoch(),0,unixepoch(),?,?,?,'revoke'
WHERE EXISTS(SELECT 1 FROM desired_node_state WHERE customer_id=? AND node_id=? AND service_name=?
AND generation=? AND operation_id=? AND tombstone=1)`,
				Args: []any{target.EventID, customer.CustomerID + ":" + target.NodeID + ":" + target.ServiceName,
					customer.ExpiredGeneration, target.EnvelopeBytes, target.Digest, target.NodeID, target.ServiceName,
					operationID, customer.CustomerID, target.NodeID, target.ServiceName, customer.ExpiredGeneration, operationID},
			}, rqlite.Statement{
				SQL: `INSERT INTO audit_events(event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,'customer.expire','customer',?,unixepoch() WHERE EXISTS(
SELECT 1 FROM operation_batches WHERE batch_id=? AND operation_id=? AND sequence_no=?)
ON CONFLICT(event_id) DO NOTHING`,
				Args: []any{auditID("customer-expire", customer.CustomerID, customer.ExpiredGeneration, now),
					s.auditActor(lease.WorkerID), s.auditResource(customer.CustomerID), customer.CustomerID,
					operationID, customer.ExpiredGeneration},
			})
		}
		statements = append(statements, rqlite.Statement{
			SQL: `UPDATE operation_batches SET status='applied'
WHERE operation_id=? AND status='applying'`, Args: []any{operationID},
		}, rqlite.Statement{
			SQL: `UPDATE operations SET status='applied',updated_at_unix=unixepoch()
WHERE operation_id=? AND status='applying'`, Args: []any{operationID},
		})
		_, requestErr := s.store.db.Request(ctx, rqlite.Linearizable, true, statements...)
		count, resolveErr := s.expiryOperationCount(ctx, operationID)
		if requestErr == nil {
			if resolveErr != nil {
				return sweepResult, resolveErr
			}
		} else if resolveErr != nil || count == 0 {
			if lost, leaseErr := s.expiryLeaseLost(ctx, lease); leaseErr == nil && lost {
				return sweepResult, ErrLeaseLost
			}
			if isUnknownWrite(requestErr) {
				return sweepResult, ErrUnavailable
			}
			return sweepResult, orderDecisionConflict(requestErr)
		}
		if count > 0 {
			sweepResult.CustomersExpired += count
			if sweepResult.OperationID == "" {
				sweepResult.OperationID = operationID
			}
		}
	}
	return sweepResult, nil
}

func (s *Service) expiryTargets(rows []map[string]any, customers []expiryCustomer, operationID string) ([]expiryDesiredTarget, error) {
	targets := make([]expiryDesiredTarget, 0, len(rows)*len(customers))
	for _, customer := range customers {
		for _, row := range rows {
			nodeID, nodeOK := rowString(row, "node_id")
			serviceName, serviceOK := rowString(row, "service_name")
			if !nodeOK || !serviceOK {
				return nil, ErrUnavailable
			}
			envelope, digest, err := s.store.secrets.SealDesiredPayload(DesiredPayloadScope{
				NodeID: nodeID, ServiceID: serviceName, CustomerID: customer.CustomerID,
				Generation: customer.ExpiredGeneration, OperationID: operationID,
				PayloadKind: "customer-revoke", Tombstone: true,
			}, nil)
			if err != nil {
				return nil, err
			}
			envelopeBytes, err := json.Marshal(envelope)
			if err != nil {
				return nil, errors.New("controlplane: encode revoke envelope")
			}
			eventID, err := s.ids.NewID("outbox")
			if err != nil {
				return nil, errors.New("controlplane: generate revoke event")
			}
			targets = append(targets, expiryDesiredTarget{
				CustomerID: customer.CustomerID, Generation: customer.ExpiredGeneration,
				NodeID: nodeID, ServiceName: serviceName, EnvelopeBytes: envelopeBytes, Digest: digest, EventID: eventID,
			})
		}
	}
	return targets, nil
}

func (s *Service) readExpiryLease(ctx context.Context) (ExpiryLease, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT holder_id,lease_fence FROM cluster_job_leases
WHERE job_name='expiry-sweeper' AND expires_at_unix>unixepoch()`,
	})
	if err != nil {
		return ExpiryLease{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return ExpiryLease{}, ErrNotFound
	}
	workerID, workerOK := rowString(row, "holder_id")
	fence, fenceOK := rowInt64(row, "lease_fence")
	if !workerOK || !fenceOK {
		return ExpiryLease{}, ErrUnavailable
	}
	return ExpiryLease{WorkerID: workerID, LeaseFence: fence}, nil
}

func (s *Service) readExpiryLeaseToken(ctx context.Context, workerID, token string) (ExpiryLease, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT holder_id,lease_fence FROM cluster_job_leases
WHERE job_name='expiry-sweeper' AND holder_id=? AND lease_token=? AND expires_at_unix>unixepoch()`,
		Args: []any{workerID, token},
	})
	if err != nil {
		return ExpiryLease{}, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return ExpiryLease{}, ErrNotFound
	}
	fence, fenceOK := rowInt64(row, "lease_fence")
	if !fenceOK {
		return ExpiryLease{}, ErrUnavailable
	}
	return ExpiryLease{WorkerID: workerID, LeaseFence: fence}, nil
}

func (s *Service) expiryLeaseLost(ctx context.Context, expected ExpiryLease) (bool, error) {
	active, err := s.readExpiryLease(ctx)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return active.WorkerID != expected.WorkerID || active.LeaseFence != expected.LeaseFence, nil
}

func (s *Service) expiryOperationApplied(ctx context.Context, operationID string) (bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT status FROM operations WHERE operation_id=?`, Args: []any{operationID},
	})
	if err != nil {
		return false, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return false, nil
	}
	status, statusOK := rowString(row, "status")
	return statusOK && status == "applied", nil
}

func (s *Service) expiryOperationCount(ctx context.Context, operationID string) (int64, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT COUNT(*) AS n FROM operation_batches b JOIN operations o ON o.operation_id=b.operation_id
WHERE b.operation_id=? AND b.status='applied' AND o.status='applied'`, Args: []any{operationID},
	})
	if err != nil {
		return 0, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return 0, ErrUnavailable
	}
	count, ok := rowInt64(row, "n")
	if !ok {
		return 0, ErrUnavailable
	}
	return count, nil
}
