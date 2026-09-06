package controlplane

import "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"

// The absence lookup is a LEFT JOIN: a missing imported binding still produces
// exactly one row. Each caller explicitly queues this response and its binding.
func legacyXUIAbsentScript(customerID, nodeID string) scriptedResult {
	return scriptedResult{
		results: []rqlite.Result{{Rows: []map[string]any{{"secret_id": nil, "source_key": nil}}}},
		expectedQuery: &rqlite.Statement{
			SQL: `SELECT i.secret_id,i.owner_type,i.owner_source_key,i.field,i.kind,i.key_version,
CAST(i.secret_envelope AS TEXT) AS secret_envelope,i.secret_sha256,
e.source_key,e.target_id,e.canonical_sha256,e.lifecycle
FROM (SELECT ? AS expected_id) expected
LEFT JOIN imported_secrets i ON i.secret_id=expected.expected_id OR (i.owner_type=? AND i.owner_source_key=?)
LEFT JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=expected.expected_id LIMIT 2`,
			Args: []any{LegacyXUIAbsenceID(customerID, nodeID), LegacyXUIAbsenceOwner, LegacyXUIAbsenceOwnerKey(customerID, nodeID)},
		},
	}
}

// Reconciliation searches only present quarantine bindings; its missing case
// is an empty set, unlike the single expected-id row above.
func legacyXUIReconcileEmptyScript(command ReconcileNodeCommand) scriptedResult {
	return scriptedResult{
		results: []rqlite.Result{{}},
		expectedQuery: &rqlite.Statement{
			SQL: `SELECT d.customer_id,d.generation,d.operation_id,d.tombstone,d.desired_sha256,CAST(d.desired_envelope AS TEXT) AS desired_envelope
FROM desired_node_state d WHERE d.node_id=? AND d.service_name=? AND d.operation_id IS NOT NULL
AND (?='' OR d.customer_id=?) AND (EXISTS(SELECT 1 FROM imported_secrets i WHERE i.secret_id='legacy-xui-absence-v1:'||d.customer_id||':'||d.node_id OR (i.owner_type=? AND i.owner_source_key=d.customer_id||':'||d.node_id||':xui')) OR EXISTS(SELECT 1 FROM imported_entity_state e WHERE e.entity_kind='encrypted_secret' AND e.source_key='legacy-xui-absence-v1:'||d.customer_id||':'||d.node_id)) LIMIT 4097`,
			Args: []any{command.NodeID, command.ServiceName, command.CustomerID, command.CustomerID, LegacyXUIAbsenceOwner},
		},
	}
}
