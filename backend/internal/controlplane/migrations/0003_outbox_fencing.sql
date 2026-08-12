-- MaestroVPN HA durable outbox fencing v3. Applied migrations v1/v2 stay immutable.

-- maestro:statement
ALTER TABLE nodes ADD COLUMN node_incarnation INTEGER NOT NULL DEFAULT 0
    CHECK(node_incarnation >= 0)

-- maestro:statement
ALTER TABLE desired_node_state ADD COLUMN tombstone INTEGER NOT NULL DEFAULT 0
    CHECK(tombstone IN (0,1))

-- maestro:statement
ALTER TABLE desired_node_state ADD COLUMN operation_id TEXT

-- maestro:statement
ALTER TABLE outbox_events ADD COLUMN node_id TEXT

-- maestro:statement
ALTER TABLE outbox_events ADD COLUMN service_name TEXT

-- maestro:statement
ALTER TABLE outbox_events ADD COLUMN operation_id TEXT

-- maestro:statement
ALTER TABLE outbox_events ADD COLUMN event_kind TEXT

-- maestro:statement
CREATE UNIQUE INDEX outbox_operation_target_generation
ON outbox_events(operation_id,node_id,service_name,event_kind,generation)
WHERE operation_id IS NOT NULL AND node_id IS NOT NULL
  AND service_name IS NOT NULL AND event_kind IS NOT NULL

-- maestro:statement
ALTER TABLE node_leases ADD COLUMN cluster_epoch INTEGER NOT NULL DEFAULT 0
    CHECK(cluster_epoch >= 0)

-- maestro:statement
ALTER TABLE node_leases ADD COLUMN node_incarnation INTEGER NOT NULL DEFAULT 0
    CHECK(node_incarnation >= 0)

-- maestro:statement
ALTER TABLE node_leases ADD COLUMN lease_fence INTEGER NOT NULL DEFAULT 0
    CHECK(lease_fence >= 0)

-- maestro:statement
ALTER TABLE node_apply_receipts ADD COLUMN cluster_epoch INTEGER NOT NULL DEFAULT 0
    CHECK(cluster_epoch >= 0)

-- maestro:statement
ALTER TABLE node_apply_receipts ADD COLUMN node_incarnation INTEGER NOT NULL DEFAULT 0
    CHECK(node_incarnation >= 0)

-- maestro:statement
ALTER TABLE node_apply_receipts ADD COLUMN lease_fence INTEGER NOT NULL DEFAULT 0
    CHECK(lease_fence >= 0)

-- maestro:statement
ALTER TABLE node_apply_receipts ADD COLUMN operation_id TEXT
