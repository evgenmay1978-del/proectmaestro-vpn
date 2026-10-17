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
	now := s.clock.Now().Unix()
	results, err := s.store.db.Request(ctx, rqlite.Linearizable, true,
		rqlite.Statement{SQL: `UPDATE customers SET status='deleted',generation=?
WHERE customer_id=? AND generation<?
  AND NOT EXISTS(SELECT 1 FROM tombstones
      WHERE tombstone_id=? OR (customer_id=? AND generation=?))
RETURNING generation`, Args: []any{
			command.Generation, command.CustomerID, command.Generation,
			command.TombstoneID, command.CustomerID, command.Generation,
		}},
		rqlite.Statement{SQL: `INSERT INTO tombstones(
tombstone_id,customer_id,generation,reason,created_at_unix)
SELECT ?,?,?,?,unixepoch()
FROM customers WHERE customer_id=? AND generation=? AND changes()=1
ON CONFLICT DO NOTHING`, Args: []any{
			command.TombstoneID, command.CustomerID, command.Generation,
			command.Reason, command.CustomerID, command.Generation,
		}},
		rqlite.Statement{SQL: `INSERT INTO tombstone_targets(
tombstone_id,node_id,service_name,status,applied_at_unix)
SELECT ?,ns.node_id,ns.service_name,'pending',NULL
FROM node_services ns
WHERE ns.desired_target=1 AND ns.retired=0
  AND changes()=1
ON CONFLICT(tombstone_id,node_id,service_name) DO NOTHING`, Args: []any{
			command.TombstoneID,
		}},
		rqlite.Statement{SQL: `INSERT INTO desired_node_state(
customer_id,node_id,service_name,generation,desired_envelope,desired_sha256,status,
updated_at_unix,tombstone,operation_id)
SELECT ?,tt.node_id,tt.service_name,?,?,?,'pending',unixepoch(),1,?
FROM tombstone_targets tt
WHERE tt.tombstone_id=? AND changes()>0
ON CONFLICT(customer_id,node_id,service_name) DO UPDATE SET
    generation=excluded.generation,
    desired_envelope=excluded.desired_envelope,
    desired_sha256=excluded.desired_sha256,
    status='pending',
    updated_at_unix=excluded.updated_at_unix,
    tombstone=1,
    operation_id=excluded.operation_id
WHERE excluded.generation>desired_node_state.generation`, Args: []any{
			command.CustomerID, command.Generation, revokePayload, revokeSHA256,
			command.TombstoneID, command.TombstoneID,
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
WHERE changes()>0 AND d.customer_id=? AND d.generation=?
  AND d.tombstone=1 AND d.operation_id=?`, Args: []any{
			command.TombstoneID, command.TombstoneID, command.TombstoneID,
			command.CustomerID, command.Generation, command.TombstoneID,
		}},
		backupRPODirtyGenerationStatement(now),
		rqlite.Statement{SQL: `INSERT INTO backup_rpo_state(
singleton_id,restore_epoch,dirty_generation,verified_generation,
last_attempt_sequence,phase,updated_at_unix)
SELECT 0,1,1,0,0,'dirty',1
WHERE NOT EXISTS(
    SELECT 1 FROM tombstones t
    JOIN customers c ON c.customer_id=t.customer_id
    WHERE t.tombstone_id=? AND t.customer_id=? AND t.generation=? AND t.reason=?
      AND c.status='deleted' AND c.generation=?
      AND EXISTS(SELECT 1 FROM tombstone_targets tt
          WHERE tt.tombstone_id=t.tombstone_id)
      AND NOT EXISTS(
          SELECT 1 FROM tombstone_targets tt
          WHERE tt.tombstone_id=t.tombstone_id
            AND NOT EXISTS(
                SELECT 1 FROM desired_node_state d
                JOIN outbox_events o
                  ON o.event_id='tombstone:'||t.tombstone_id||':'||tt.node_id||':'||tt.service_name
                 AND o.aggregate_type='desired_node_state'
                 AND o.aggregate_id=d.customer_id||':'||d.node_id||':'||d.service_name
                 AND o.generation=d.generation
                 AND o.event_type='customer_tombstone'
                 AND o.payload_envelope=d.desired_envelope
                 AND o.payload_sha256=d.desired_sha256
                 AND o.node_id=d.node_id AND o.service_name=d.service_name
                 AND o.operation_id=t.tombstone_id AND o.event_kind='customer_tombstone'
                WHERE d.customer_id=t.customer_id
                  AND d.node_id=tt.node_id AND d.service_name=tt.service_name
                  AND d.generation=t.generation AND d.tombstone=1
                  AND d.operation_id=t.tombstone_id
            )
      )
)`, Args: []any{
			command.TombstoneID, command.CustomerID, command.Generation,
			command.Reason, command.Generation,
		}},
		rqlite.Statement{SQL: `SELECT t.generation,t.reason,c.status AS customer_status,
       COUNT(tt.node_id) AS target_count
FROM tombstones t
JOIN customers c ON c.customer_id=t.customer_id
LEFT JOIN tombstone_targets tt ON tt.tombstone_id=t.tombstone_id
WHERE t.tombstone_id=? AND t.customer_id=? AND t.generation=? AND t.reason=?
  AND c.status='deleted' AND c.generation=?
GROUP BY t.generation,t.reason,c.status`, Args: []any{
			command.TombstoneID, command.CustomerID, command.Generation,
			command.Reason, command.Generation,
		}},
	)
	if err != nil || len(results) != 8 {
		return 0, errors.New("controlplane: create customer tombstone unavailable")
	}
	if results[6].RowsAffected != 0 || len(results[6].Rows) != 0 {
		return 0, ErrConflict
	}
	evidence, evidenceOK := firstRow(results[7:8])
	evidenceGeneration, generationOK := rowInt64(evidence, "generation")
	evidenceReason, reasonOK := rowString(evidence, "reason")
	evidenceStatus, statusOK := rowString(evidence, "customer_status")
	targets, targetsOK := rowInt64(evidence, "target_count")
	if !evidenceOK || !generationOK || !reasonOK || !statusOK || !targetsOK ||
		evidenceGeneration != command.Generation || evidenceReason != command.Reason ||
		evidenceStatus != "deleted" || targets <= 0 {
		return 0, ErrConflict
	}
	mutation, mutated := firstRow(results[:1])
	if !mutated {
		for _, index := range []int{1, 2, 3, 4, 5, 6} {
			if results[index].RowsAffected != 0 || len(results[index].Rows) != 0 {
				return 0, ErrConflict
			}
		}
		return targets, nil
	}
	generation, ok := rowInt64(mutation, "generation")
	if !ok || generation != command.Generation ||
		results[1].RowsAffected != 1 || results[2].RowsAffected != targets ||
		results[3].RowsAffected != targets || results[4].RowsAffected != targets {
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
WHERE changes()=1 AND EXISTS(SELECT 1 FROM node_services ns
    JOIN nodes n ON n.node_id=ns.node_id
    WHERE ns.node_id=? AND ns.service_name=? AND ns.retired=1
      AND n.node_incarnation=?)`, Args: []any{
			auditEventID, actorHMAC, resourceHMAC, command.NodeID, command.ServiceName,
			command.ExpectedIncarnation,
		}},
		backupRPODirtyGenerationStatement(now),
		rqlite.Statement{SQL: `DELETE FROM tombstone_targets
WHERE EXISTS(SELECT 1 FROM audit_events WHERE event_id=?)
  AND node_id=? AND service_name=?`, Args: []any{
			auditEventID, command.NodeID, command.ServiceName,
		}},
	)
	if err != nil || len(results) != 4 {
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
	return results[3].RowsAffected, nil
}
