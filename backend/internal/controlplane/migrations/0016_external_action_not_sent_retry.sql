-- MaestroVPN HA definite-not-sent external-action retry schema v16.
-- Migrations v1-v15 remain byte-for-byte immutable.

-- maestro:statement
DROP TRIGGER external_actions_attempt_owner_set_once

-- maestro:statement
CREATE TRIGGER external_actions_attempt_owner_set_once
BEFORE UPDATE OF attempt_worker_id,attempt_lease_token,attempt_lease_fence ON external_actions
WHEN NOT (
    (
        OLD.status = 'pending' AND NEW.status = 'applying' AND
        OLD.attempt_worker_id IS NULL AND OLD.attempt_lease_token IS NULL AND OLD.attempt_lease_fence IS NULL AND
        NEW.attempt_worker_id IS NOT NULL AND length(NEW.attempt_worker_id) > 0 AND
        NEW.attempt_lease_token IS NOT NULL AND length(NEW.attempt_lease_token) > 0 AND
        NEW.attempt_lease_fence IS NOT NULL AND NEW.attempt_lease_fence > 0
    ) OR (
        OLD.status = 'applying' AND NEW.status = 'pending' AND
        OLD.attempt_worker_id IS NOT NULL AND length(OLD.attempt_worker_id) > 0 AND
        OLD.attempt_lease_token IS NOT NULL AND length(OLD.attempt_lease_token) > 0 AND
        OLD.attempt_lease_fence IS NOT NULL AND OLD.attempt_lease_fence > 0 AND
        NEW.attempt_worker_id IS NULL AND NEW.attempt_lease_token IS NULL AND NEW.attempt_lease_fence IS NULL
    )
)
BEGIN
    SELECT RAISE(ABORT, 'external action attempt owner is immutable');
END
