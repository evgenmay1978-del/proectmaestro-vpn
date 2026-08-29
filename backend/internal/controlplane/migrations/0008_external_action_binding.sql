-- MaestroVPN HA external-action binding schema v8. Applied migrations v1-v7 remain immutable.

-- maestro:statement
ALTER TABLE external_actions ADD COLUMN replaces_action_id TEXT
    REFERENCES external_actions(action_id) ON DELETE RESTRICT

-- maestro:statement
CREATE UNIQUE INDEX external_actions_one_replacement
ON external_actions(replaces_action_id)
WHERE replaces_action_id IS NOT NULL

-- maestro:statement
CREATE TRIGGER external_actions_binding_immutable
BEFORE UPDATE OF action_id,action_type,resource_id,idempotency_key,request_envelope,request_sha256,replaces_action_id
ON external_actions
BEGIN
    SELECT RAISE(ABORT, 'external action binding is immutable');
END

-- maestro:statement
CREATE TRIGGER external_actions_replacement_valid_insert
BEFORE INSERT ON external_actions
WHEN NEW.replaces_action_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM external_actions predecessor
        WHERE predecessor.action_id = NEW.replaces_action_id
          AND predecessor.status = 'unknown'
          AND predecessor.action_type = NEW.action_type
          AND predecessor.resource_id = NEW.resource_id
          AND predecessor.request_sha256 = NEW.request_sha256
          AND predecessor.idempotency_key <> NEW.idempotency_key
    ) THEN RAISE(ABORT, 'invalid external action replacement') END;
END
