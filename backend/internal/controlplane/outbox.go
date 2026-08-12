package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const nodeLeaseTTLSeconds int64 = 90

// DesiredState is one absolute encrypted service target. A generation is
// immutable: retries may repeat its hash, but may never replace it.
type DesiredState struct {
	CustomerID   string
	NodeID       string
	ServiceName  string
	OperationID  string
	EventKind    string
	Generation   int64
	Payload      Envelope
	PayloadSHA256 string
	Tombstone    bool
}

type LeaseRequest struct {
	NodeID      string
	ServiceName string
	HolderID    string
}

type NodeLease struct {
	NodeID          string
	ServiceName     string
	HolderID        string
	ClusterEpoch    int64
	NodeIncarnation int64
	LeaseFence      int64
	ExpiresAtUnix   int64
}

type ApplyReceipt struct {
	ReceiptID       string
	CustomerID      string
	NodeID          string
	ServiceName     string
	OperationID     string
	HolderID        string
	Generation      int64
	ClusterEpoch    int64
	NodeIncarnation int64
	LeaseFence      int64
	DesiredSHA256   string
	ObservedSHA256  string
}

type ReconcileNodeCommand struct {
	NodeID      string
	ServiceName string
}

type TombstonePurgeCommand struct {
	TombstoneID string
	CustomerID  string
}

func (s *Service) UpsertDesired(ctx context.Context, desired DesiredState) error {
	if s == nil || s.store == nil || strings.TrimSpace(desired.CustomerID) == "" ||
		strings.TrimSpace(desired.NodeID) == "" || strings.TrimSpace(desired.ServiceName) == "" ||
		strings.TrimSpace(desired.OperationID) == "" || strings.TrimSpace(desired.EventKind) == "" ||
		desired.Generation <= 0 || !canonicalRestoreHex(desired.PayloadSHA256) ||
		desired.Payload.KeyVersion <= 0 || len(desired.Payload.Nonce) == 0 || len(desired.Payload.Ciphertext) == 0 {
		return errors.New("controlplane: invalid desired state")
	}
	payload, err := json.Marshal(desired.Payload)
	if err != nil {
		return errors.New("controlplane: encode desired state")
	}
	payloadDigest := sha256.Sum256(payload)
	if hex.EncodeToString(payloadDigest[:]) != desired.PayloadSHA256 {
		return errors.New("controlplane: desired payload hash mismatch")
	}
	eventID, err := s.ids.NewID("event")
	if err != nil {
		return errors.New("controlplane: generate outbox event")
	}
	tombstone := 0
	if desired.Tombstone {
		tombstone = 1
	}
	now := s.clock.Now().Unix()
	aggregateID := desired.CustomerID + ":" + desired.NodeID + ":" + desired.ServiceName
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT INTO desired_node_state(
customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,updated_at_unix,tombstone,operation_id)
SELECT ?,?,?,?,?,?,'pending',?,?,?
FROM node_services
WHERE node_id=? AND service_name=? AND desired_target=1 AND retired=0
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET
generation=CASE WHEN excluded.generation>desired_node_state.generation THEN excluded.generation ELSE desired_node_state.generation END,
desired_envelope=CASE WHEN excluded.generation>desired_node_state.generation THEN excluded.desired_envelope ELSE desired_node_state.desired_envelope END,
desired_sha256=CASE WHEN excluded.generation>desired_node_state.generation THEN excluded.desired_sha256 ELSE desired_node_state.desired_sha256 END,
status=CASE WHEN excluded.generation>desired_node_state.generation THEN 'pending' ELSE desired_node_state.status END,
updated_at_unix=CASE WHEN excluded.generation>desired_node_state.generation THEN excluded.updated_at_unix ELSE desired_node_state.updated_at_unix END,
tombstone=CASE WHEN excluded.generation>desired_node_state.generation THEN excluded.tombstone ELSE desired_node_state.tombstone END,
operation_id=CASE WHEN excluded.generation>desired_node_state.generation THEN excluded.operation_id ELSE desired_node_state.operation_id END
WHERE excluded.generation > desired_node_state.generation OR
      (excluded.generation = desired_node_state.generation AND
       excluded.desired_sha256 = desired_node_state.desired_sha256)
RETURNING generation,desired_sha256`, Args: []any{
			desired.CustomerID, desired.NodeID, desired.ServiceName, desired.Generation,
			payload, desired.PayloadSHA256, now, tombstone, desired.OperationID,
			desired.NodeID, desired.ServiceName,
		}},
		rqlite.Statement{SQL: `INSERT OR IGNORE INTO outbox_events(
event_id,aggregate_type,aggregate_id,generation,event_type,payload_envelope,payload_sha256,
status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind)
SELECT ?,'desired_node_state',?,?,?,?,?,'pending',?,0,?,?,?,?,?
FROM desired_node_state
WHERE customer_id=? AND node_id=? AND service_name=? AND generation=? AND desired_sha256=?`, Args: []any{
			eventID, aggregateID, desired.Generation, desired.EventKind, payload,
			desired.PayloadSHA256, now, now, desired.NodeID, desired.ServiceName,
			desired.OperationID, desired.EventKind, desired.CustomerID, desired.NodeID,
			desired.ServiceName, desired.Generation, desired.PayloadSHA256,
		}},
	)
	if err != nil {
		return errors.New("controlplane: desired state unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return ErrConflict
	}
	generation, generationOK := rowInt64(row, "generation")
	digest, digestOK := rowString(row, "desired_sha256")
	if !generationOK || !digestOK || generation != desired.Generation || digest != desired.PayloadSHA256 {
		return ErrConflict
	}
	return nil
}

func (s *Service) AcquireNodeLease(ctx context.Context, request LeaseRequest) (NodeLease, error) {
	if s == nil || s.store == nil || strings.TrimSpace(request.NodeID) == "" ||
		strings.TrimSpace(request.ServiceName) == "" || strings.TrimSpace(request.HolderID) == "" {
		return NodeLease{}, errors.New("controlplane: invalid lease request")
	}
	token, err := s.ids.NewID("lease")
	if err != nil {
		return NodeLease{}, errors.New("controlplane: generate lease token")
	}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
INSERT INTO node_leases(
node_id,service_name,holder_id,lease_token,acquired_at_unix,expires_at_unix,
cluster_epoch,node_incarnation,lease_fence)
SELECT ns.node_id,ns.service_name,?,?,unixepoch(),unixepoch()+?,
       cr.restore_epoch,n.node_incarnation,1
FROM node_services ns
JOIN nodes n ON n.node_id=ns.node_id
JOIN cluster_restore_state cr ON cr.singleton_id=1 AND cr.activated=1
WHERE ns.node_id=? AND ns.service_name=? AND ns.desired_target=1
  AND ns.apply_enabled=1 AND ns.fenced=0 AND ns.retired=0
  AND n.enabled=1 AND n.node_incarnation>0
ON CONFLICT(node_id,service_name) DO UPDATE SET
holder_id=excluded.holder_id,lease_token=excluded.lease_token,
acquired_at_unix=unixepoch(),expires_at_unix=unixepoch()+?,
cluster_epoch=excluded.cluster_epoch,node_incarnation=excluded.node_incarnation,
lease_fence=CASE WHEN node_leases.holder_id=excluded.holder_id
                 THEN node_leases.lease_fence ELSE node_leases.lease_fence + 1 END
WHERE node_leases.holder_id=excluded.holder_id OR node_leases.expires_at_unix<=unixepoch()
RETURNING node_id,service_name,holder_id,cluster_epoch,node_incarnation,lease_fence,expires_at_unix`, Args: []any{
		request.HolderID, token, nodeLeaseTTLSeconds, request.NodeID, request.ServiceName, nodeLeaseTTLSeconds,
	}})
	if err != nil {
		return NodeLease{}, errors.New("controlplane: node lease unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return NodeLease{}, ErrConflict
	}
	lease := NodeLease{}
	lease.NodeID, _ = rowString(row, "node_id")
	lease.ServiceName, _ = rowString(row, "service_name")
	lease.HolderID, _ = rowString(row, "holder_id")
	lease.ClusterEpoch, _ = rowInt64(row, "cluster_epoch")
	lease.NodeIncarnation, _ = rowInt64(row, "node_incarnation")
	lease.LeaseFence, _ = rowInt64(row, "lease_fence")
	lease.ExpiresAtUnix, _ = rowInt64(row, "expires_at_unix")
	if lease.NodeID != request.NodeID || lease.ServiceName != request.ServiceName ||
		lease.HolderID != request.HolderID || lease.ClusterEpoch <= 0 ||
		lease.NodeIncarnation <= 0 || lease.LeaseFence <= 0 || lease.ExpiresAtUnix <= 0 {
		return NodeLease{}, ErrConflict
	}
	return lease, nil
}

func (s *Service) RecordApplyReceipt(ctx context.Context, receipt ApplyReceipt) error {
	if s == nil || s.store == nil || strings.TrimSpace(receipt.ReceiptID) == "" ||
		strings.TrimSpace(receipt.CustomerID) == "" || strings.TrimSpace(receipt.NodeID) == "" ||
		strings.TrimSpace(receipt.ServiceName) == "" || strings.TrimSpace(receipt.OperationID) == "" ||
		strings.TrimSpace(receipt.HolderID) == "" || receipt.Generation <= 0 || receipt.ClusterEpoch <= 0 ||
		receipt.NodeIncarnation <= 0 || receipt.LeaseFence <= 0 ||
		!canonicalRestoreHex(receipt.DesiredSHA256) || !canonicalRestoreHex(receipt.ObservedSHA256) {
		return errors.New("controlplane: invalid apply receipt")
	}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `INSERT INTO node_apply_receipts(
receipt_id,customer_id,node_id,service_name,generation,desired_sha256,status,
observed_sha256,error_code,applied_at_unix,created_at_unix,
cluster_epoch,node_incarnation,lease_fence,operation_id)
SELECT ?,?,?,?,?,?,'applied',?,NULL,unixepoch(),unixepoch(),?,?,?,?
FROM node_leases nl
JOIN nodes n ON n.node_id=nl.node_id
JOIN cluster_restore_state cr ON cr.singleton_id=1 AND cr.activated=1
JOIN desired_node_state dns ON dns.customer_id=? AND dns.node_id=nl.node_id
 AND dns.service_name=nl.service_name
WHERE nl.node_id=? AND nl.service_name=? AND nl.holder_id=?
  AND nl.expires_at_unix > unixepoch()
  AND cr.restore_epoch=? AND nl.cluster_epoch=?
  AND n.node_incarnation=? AND nl.node_incarnation=?
  AND nl.lease_fence=? AND dns.generation=? AND dns.desired_sha256=?
  AND dns.operation_id=?
ON CONFLICT(customer_id,node_id,service_name,generation) DO NOTHING
RETURNING receipt_id`, Args: []any{
			receipt.ReceiptID, receipt.CustomerID, receipt.NodeID, receipt.ServiceName,
			receipt.Generation, receipt.DesiredSHA256, receipt.ObservedSHA256,
			receipt.ClusterEpoch, receipt.NodeIncarnation, receipt.LeaseFence, receipt.OperationID,
			receipt.CustomerID, receipt.NodeID, receipt.ServiceName, receipt.HolderID,
			receipt.ClusterEpoch, receipt.ClusterEpoch, receipt.NodeIncarnation,
			receipt.NodeIncarnation, receipt.LeaseFence, receipt.Generation,
			receipt.DesiredSHA256, receipt.OperationID,
		}},
		rqlite.Statement{SQL: `UPDATE desired_node_state SET status='applied',updated_at_unix=unixepoch()
WHERE customer_id=? AND node_id=? AND service_name=? AND generation=? AND desired_sha256=?
AND EXISTS(SELECT 1 FROM node_apply_receipts WHERE receipt_id=?)`, Args: []any{
			receipt.CustomerID, receipt.NodeID, receipt.ServiceName, receipt.Generation,
			receipt.DesiredSHA256, receipt.ReceiptID,
		}},
		rqlite.Statement{SQL: `UPDATE outbox_events SET status='applied'
WHERE operation_id=? AND node_id=? AND service_name=? AND generation=?
AND EXISTS(SELECT 1 FROM node_apply_receipts WHERE receipt_id=?)`, Args: []any{
			receipt.OperationID, receipt.NodeID, receipt.ServiceName, receipt.Generation, receipt.ReceiptID,
		}},
		rqlite.Statement{SQL: `UPDATE tombstone_targets SET status='applied',applied_at_unix=unixepoch()
WHERE node_id=? AND service_name=? AND status<>'applied'
  AND EXISTS(SELECT 1 FROM desired_node_state d
      JOIN tombstones t ON t.customer_id=d.customer_id AND t.generation=d.generation
      JOIN node_apply_receipts r ON r.receipt_id=? AND r.customer_id=d.customer_id
      WHERE d.customer_id=? AND d.node_id=? AND d.service_name=? AND d.tombstone=1
        AND tombstone_targets.tombstone_id=t.tombstone_id)`, Args: []any{
			receipt.NodeID, receipt.ServiceName, receipt.ReceiptID, receipt.CustomerID, receipt.NodeID, receipt.ServiceName,
		}},
	)
	if err != nil {
		exact, readErr := s.applyReceiptRecordedExactly(ctx, receipt)
		if readErr == nil && exact {
			return nil
		}
		return errors.New("controlplane: apply receipt unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		exact, readErr := s.applyReceiptRecordedExactly(ctx, receipt)
		if readErr == nil && exact {
			return nil
		}
		return ErrConflict
	}
	storedID, ok := rowString(row, "receipt_id")
	if !ok || storedID != receipt.ReceiptID {
		return ErrConflict
	}
	return nil

func (s *Service) applyReceiptRecordedExactly(ctx context.Context, receipt ApplyReceipt) (bool, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `SELECT
receipt_id,customer_id,node_id,service_name,operation_id,generation,
cluster_epoch,node_incarnation,lease_fence,desired_sha256,observed_sha256
FROM node_apply_receipts
WHERE customer_id=? AND node_id=? AND service_name=? AND generation=?`, Args: []any{
		receipt.CustomerID, receipt.NodeID, receipt.ServiceName, receipt.Generation,
	}})
	if err != nil {
		return false, err
	}
	row, ok := firstRow(results)
	if !ok {
		return false, nil
	}
	stringsMatch := map[string]string{
		"receipt_id": receipt.ReceiptID, "customer_id": receipt.CustomerID,
		"node_id": receipt.NodeID, "service_name": receipt.ServiceName,
		"operation_id": receipt.OperationID, "desired_sha256": receipt.DesiredSHA256,
		"observed_sha256": receipt.ObservedSHA256,
	}
	for key, want := range stringsMatch {
		got, valid := rowString(row, key)
		if !valid || got != want {
			return false, nil
		}
	}
	integers := map[string]int64{
		"generation": receipt.Generation, "cluster_epoch": receipt.ClusterEpoch,
		"node_incarnation": receipt.NodeIncarnation, "lease_fence": receipt.LeaseFence,
	}
	for key, want := range integers {
		got, valid := rowInt64(row, key)
		if !valid || got != want {
			return false, nil
		}
	}
	return true, nil
}
}

func (s *Service) ReconcileNode(ctx context.Context, command ReconcileNodeCommand) (int64, error) {
	if s == nil || s.store == nil || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.ServiceName) == "" {
		return 0, errors.New("controlplane: invalid reconcile command")
	}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
INSERT INTO outbox_events(
event_id,aggregate_type,aggregate_id,generation,event_type,payload_envelope,payload_sha256,
status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind)
SELECT 'reconcile:'||d.operation_id||':'||d.node_id||':'||d.service_name||':'||d.generation,
       'desired_node_state',d.customer_id||':'||d.node_id||':'||d.service_name,
       d.generation,'customer_desired',d.desired_envelope,d.desired_sha256,
       'pending',unixepoch(),0,unixepoch(),d.node_id,d.service_name,d.operation_id,'customer_desired'
FROM desired_node_state d
JOIN node_services ns ON ns.node_id=d.node_id AND ns.service_name=d.service_name
WHERE d.node_id=? AND d.service_name=? AND d.operation_id IS NOT NULL
  AND ns.desired_target=1 AND ns.retired=0
  AND NOT EXISTS(SELECT 1 FROM outbox_events o
      WHERE o.operation_id=d.operation_id AND o.node_id=d.node_id
        AND o.service_name=d.service_name AND o.generation=d.generation
        AND o.event_kind='customer_desired')
ON CONFLICT DO NOTHING`, Args: []any{command.NodeID, command.ServiceName}})
	if err != nil || len(results) != 1 {
		return 0, errors.New("controlplane: reconcile unavailable")
	}
	return results[0].RowsAffected, nil
}

func (s *Service) PurgeTombstone(ctx context.Context, command TombstonePurgeCommand) error {
	if s == nil || s.store == nil || strings.TrimSpace(command.TombstoneID) == "" ||
		strings.TrimSpace(command.CustomerID) == "" {
		return errors.New("controlplane: invalid tombstone purge")
	}
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true, rqlite.Statement{SQL: `
DELETE FROM customers
WHERE customer_id=?
  AND EXISTS(SELECT 1 FROM tombstones t
      JOIN tombstone_targets tt ON tt.tombstone_id=t.tombstone_id
      WHERE t.tombstone_id=? AND t.customer_id=customers.customer_id
      GROUP BY t.tombstone_id HAVING MAX(tt.applied_at_unix) <= unixepoch()-7776000)
  AND NOT EXISTS(SELECT 1 FROM tombstone_targets tt
      WHERE tt.tombstone_id=? AND tt.status <> 'applied')
  AND NOT EXISTS(
      SELECT 1 FROM node_services ns
      WHERE ns.desired_target = 1 AND ns.retired = 0
        AND NOT EXISTS(SELECT 1 FROM tombstone_targets tt
            WHERE tt.tombstone_id=? AND tt.node_id=ns.node_id
              AND tt.service_name=ns.service_name AND tt.status='applied'))`, Args: []any{
		command.CustomerID, command.TombstoneID, command.TombstoneID, command.TombstoneID,
	}})
	if err != nil || len(results) != 1 {
		return errors.New("controlplane: tombstone purge unavailable")
	}
	if results[0].RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (d DesiredState) String() string {
	return fmt.Sprintf("desired node=%s service=%s generation=%d", d.NodeID, d.ServiceName, d.Generation)
}
