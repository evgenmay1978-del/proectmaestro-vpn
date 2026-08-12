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

type CustomerTombstoneCommand struct {
	TombstoneID string
	CustomerID  string
	Generation  int64
	Reason      string
}

type PermanentRetirementCommand struct {
	NodeID              string
	ServiceName         string
	ExpectedIncarnation int64
	Actor               string
	Reason              string
}

// CreateCustomerTombstone freezes the immutable required-target set. Current
// availability is intentionally absent from the selection predicate.
func (s *Service) CreateCustomerTombstone(ctx context.Context, command CustomerTombstoneCommand) (int64, error) {
	if s == nil || s.store == nil || strings.TrimSpace(command.TombstoneID) == "" ||
		strings.TrimSpace(command.CustomerID) == "" || command.Generation <= 0 ||
		strings.TrimSpace(command.Reason) == "" {
		return 0, errors.New("controlplane: invalid customer tombstone")
	}
	revokeEnvelope, err := s.store.secrets.Seal(
		SecretScope{OwnerType: "customer", OwnerID: command.CustomerID, Field: "desired-revoke", Kind: "tombstone"},
		[]byte(`{"tombstone":true}`),
	)
	if err != nil {
		return 0, errors.New("controlplane: encrypt customer tombstone")
	}
	revokePayload, err := json.Marshal(revokeEnvelope)
	if err != nil {
		return 0, errors.New("controlplane: encode customer tombstone")
	}
	revokeSum := sha256.Sum256(revokePayload)
	revokeSHA256 := hex.EncodeToString(revokeSum[:])
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE customers SET status='disabled',generation=?
WHERE customer_id=? AND generation<? RETURNING generation`, Args: []any{
			command.Generation, command.CustomerID, command.Generation,
		}},
		rqlite.Statement{SQL: `INSERT INTO tombstones(
tombstone_id,customer_id,generation,reason,created_at_unix)
SELECT ?,?,?,?,unixepoch()
FROM customers WHERE customer_id=? AND generation=?
ON CONFLICT(customer_id,generation) DO NOTHING`, Args: []any{
			command.TombstoneID, command.CustomerID, command.Generation,
			command.Reason, command.CustomerID, command.Generation,
		}},
		rqlite.Statement{SQL: `INSERT INTO tombstone_targets(
tombstone_id,node_id,service_name,status,applied_at_unix)
SELECT ?,ns.node_id,ns.service_name,'pending',NULL
FROM node_services ns
WHERE ns.desired_target=1 AND ns.retired=0
  AND EXISTS(SELECT 1 FROM tombstones t WHERE t.tombstone_id=?
      AND t.customer_id=? AND t.generation=?)
ON CONFLICT(tombstone_id,node_id,service_name) DO NOTHING`, Args: []any{
			command.TombstoneID, command.TombstoneID, command.CustomerID, command.Generation,
		}},
	)
		rqlite.Statement{SQL: `UPDATE desired_node_state
SET generation=?,desired_envelope=?,desired_sha256=?,status='pending',
    updated_at_unix=unixepoch(),tombstone=1,operation_id=?
WHERE customer_id=? AND generation<?
  AND EXISTS(SELECT 1 FROM tombstone_targets tt
      WHERE tt.tombstone_id=? AND tt.node_id=desired_node_state.node_id
        AND tt.service_name=desired_node_state.service_name)`, Args: []any{
			command.Generation, revokePayload, revokeSHA256, command.TombstoneID,
			command.CustomerID, command.Generation, command.TombstoneID,
		}},
		rqlite.Statement{SQL: `INSERT INTO desired_node_state(
customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,
updated_at_unix,tombstone,operation_id)
SELECT ?,tt.node_id,tt.service_name,?,?,?,'pending',unixepoch(),1,?
FROM tombstone_targets tt
WHERE tt.tombstone_id=? AND NOT EXISTS(SELECT 1 FROM desired_node_state d
    WHERE d.customer_id=? AND d.node_id=tt.node_id AND d.service_name=tt.service_name)
ON CONFLICT(customer_id,node_id,service_name) DO NOTHING`, Args: []any{
			command.CustomerID, command.Generation, revokePayload, revokeSHA256,
			command.TombstoneID, command.TombstoneID, command.CustomerID,
		}},
		rqlite.Statement{SQL: `INSERT INTO outbox_events(
event_id,aggregate_type,aggregate_id,generation,event_type,payload_envelope,payload_sha256,
status,available_at_unix,attempts,created_at_unix,node_id,service_name,operation_id,event_kind)
SELECT 'tombstone:'||?||':'||d.node_id||':'||d.service_name,
       'desired_node_state',d.customer_id||':'||d.node_id||':'||d.service_name,
       d.generation,'customer_tombstone',d.desired_envelope,d.desired_sha256,
       'pending',unixepoch(),0,unixepoch(),d.node_id,d.service_name,?,'customer_tombstone'
FROM desired_node_state d JOIN tombstone_targets tt
  ON tt.tombstone_id=? AND tt.node_id=d.node_id AND tt.service_name=d.service_name
WHERE d.customer_id=? AND d.generation=? AND d.tombstone=1 AND d.operation_id=?
ON CONFLICT DO NOTHING`, Args: []any{
			command.TombstoneID, command.TombstoneID, command.TombstoneID,
			command.CustomerID, command.Generation, command.TombstoneID,
		}},
	)
	if err != nil || len(results) != 6 {
		return 0, errors.New("controlplane: create customer tombstone unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return 0, ErrConflict
	}
	generation, ok := rowInt64(row, "generation")
	targets := results[2].RowsAffected
	propagated := results[3].RowsAffected + results[4].RowsAffected
	if !ok || generation != command.Generation || targets <= 0 || propagated != targets || results[5].RowsAffected != targets {
		return 0, ErrConflict
	}
	return targets, nil
}

// PermanentlyRetireNodeService is the only Task 10 path that removes a frozen
// tombstone target. The exact fenced incarnation CAS and audit insert share
// the same transaction with target removal.
func (s *Service) PermanentlyRetireNodeService(ctx context.Context, command PermanentRetirementCommand) (int64, error) {
	if s == nil || s.store == nil || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.ServiceName) == "" || command.ExpectedIncarnation <= 0 ||
		strings.TrimSpace(command.Actor) == "" || strings.TrimSpace(command.Reason) == "" {
		return 0, errors.New("controlplane: invalid permanent retirement")
	}
	now := s.clock.Now().Unix()
	auditEventID := auditID("permanent-retirement", command.NodeID+":"+command.ServiceName+":"+command.Reason, command.ExpectedIncarnation, now)
	actorHMAC := s.store.secrets.LookupHMAC("audit-actor", []byte(command.Actor))
	resourceHMAC := s.store.secrets.LookupHMAC("audit-resource", []byte(command.NodeID+":"+command.ServiceName))
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE node_services
SET desired_target=0,apply_enabled=0,retired=1,updated_at_unix=unixepoch()
WHERE node_id=? AND service_name=? AND fenced=1 AND retired=0
  AND EXISTS(SELECT 1 FROM nodes n WHERE n.node_id=node_services.node_id
      AND n.node_incarnation=?)
RETURNING retired`, Args: []any{
			command.NodeID, command.ServiceName, command.ExpectedIncarnation,
		}},
		rqlite.Statement{SQL: `INSERT INTO audit_events(
event_id,actor_hmac,action,resource_type,resource_id_hmac,created_at_unix)
SELECT ?,?,'node_service.permanent_retire','node_service',?,unixepoch()
WHERE EXISTS(SELECT 1 FROM node_services
    WHERE node_id=? AND service_name=? AND retired=1)
ON CONFLICT(event_id) DO NOTHING`, Args: []any{
			auditEventID, actorHMAC, resourceHMAC, command.NodeID, command.ServiceName,
		}},
		rqlite.Statement{SQL: `DELETE FROM tombstone_targets
WHERE EXISTS(SELECT 1 FROM audit_events WHERE event_id=?)
  AND node_id=? AND service_name=?`, Args: []any{
			auditEventID, command.NodeID, command.ServiceName,
		}},
	)
	if err != nil || len(results) != 3 {
		return 0, errors.New("controlplane: permanent retirement unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return 0, ErrConflict
	}
	retired, ok := rowInt64(row, "retired")
	if !ok || retired != 1 {
		return 0, ErrConflict
	}
	return results[2].RowsAffected, nil
}
