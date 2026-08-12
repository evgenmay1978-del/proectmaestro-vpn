package controlplane

import (
	"context"
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
	if err != nil || len(results) != 3 {
		return 0, errors.New("controlplane: create customer tombstone unavailable")
	}
	row, ok := firstRow(results)
	if !ok {
		return 0, ErrConflict
	}
	generation, ok := rowInt64(row, "generation")
	if !ok || generation != command.Generation || results[2].RowsAffected <= 0 {
		return 0, ErrConflict
	}
	return results[2].RowsAffected, nil
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
