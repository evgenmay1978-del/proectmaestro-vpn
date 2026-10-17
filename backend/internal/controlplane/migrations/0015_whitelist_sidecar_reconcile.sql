-- MaestroVPN HA immutable commercial white-list sidecar reconciliation schema v15.
-- Migrations v1-v14 remain byte-for-byte immutable.

-- maestro:statement
CREATE TABLE whitelist_sidecar_exits (
    exit_id TEXT PRIMARY KEY NOT NULL,
    country_code TEXT NOT NULL,
    country_label TEXT NOT NULL,
    healthy INTEGER NOT NULL CHECK(typeof(healthy) = 'integer' AND healthy IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    CHECK(exit_id <> '' AND country_code <> '' AND country_label <> '')
)

-- maestro:statement
CREATE TABLE whitelist_sidecar_origins (
    origin_id TEXT PRIMARY KEY NOT NULL,
    node_id TEXT NOT NULL UNIQUE,
    release_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    preset_id TEXT NOT NULL,
    config_digest TEXT NOT NULL CHECK(length(config_digest) = 64 AND config_digest NOT GLOB '*[^0-9a-f]*'),
    active INTEGER NOT NULL CHECK(typeof(active) = 'integer' AND active IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(node_id) REFERENCES nodes(node_id) ON DELETE RESTRICT,
    CHECK(origin_id <> '' AND release_id <> '' AND profile_id <> '' AND preset_id <> '')
)

-- maestro:statement
CREATE TABLE whitelist_route_credentials (
    entitlement_id TEXT NOT NULL,
    exit_id TEXT NOT NULL,
    managed_email TEXT NOT NULL UNIQUE,
    credential_envelope BLOB NOT NULL CHECK(length(credential_envelope) > 0),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    PRIMARY KEY(entitlement_id, exit_id),
    UNIQUE(entitlement_id, exit_id),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(exit_id) REFERENCES whitelist_sidecar_exits(exit_id) ON DELETE RESTRICT,
    CHECK(managed_email = 'wl:' || entitlement_id || ':' || exit_id)
)

-- maestro:statement
CREATE TRIGGER whitelist_route_credentials_immutable_update
BEFORE UPDATE ON whitelist_route_credentials
BEGIN
    SELECT RAISE(ABORT, 'white-list route credential is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_route_credentials_immutable_delete
BEFORE DELETE ON whitelist_route_credentials
BEGIN
    SELECT RAISE(ABORT, 'white-list route credential is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_sidecar_desired (
    origin_id TEXT NOT NULL,
    desired_generation INTEGER NOT NULL CHECK(typeof(desired_generation) = 'integer' AND desired_generation BETWEEN 1 AND 9223372036854775806),
    node_id TEXT NOT NULL,
    release_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    preset_id TEXT NOT NULL,
    exit_id TEXT NOT NULL,
    config_digest TEXT NOT NULL CHECK(length(config_digest) = 64 AND config_digest NOT GLOB '*[^0-9a-f]*'),
    managed_user_set_digest TEXT NOT NULL CHECK(length(managed_user_set_digest) = 64 AND managed_user_set_digest NOT GLOB '*[^0-9a-f]*'),
    desired_sha256 TEXT NOT NULL CHECK(length(desired_sha256) = 64 AND desired_sha256 NOT GLOB '*[^0-9a-f]*'),
    payload_json BLOB NOT NULL CHECK(length(payload_json) > 0),
    action_type TEXT NOT NULL CHECK(action_type = 'whitelist_sidecar_apply'),
    action_key TEXT NOT NULL UNIQUE,
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    PRIMARY KEY(origin_id, desired_generation),
    UNIQUE(origin_id, desired_generation),
    FOREIGN KEY(origin_id) REFERENCES whitelist_sidecar_origins(origin_id) ON DELETE RESTRICT,
    FOREIGN KEY(node_id) REFERENCES nodes(node_id) ON DELETE RESTRICT,
    FOREIGN KEY(exit_id) REFERENCES whitelist_sidecar_exits(exit_id) ON DELETE RESTRICT,
    FOREIGN KEY(action_type, action_key) REFERENCES external_actions(action_type, idempotency_key) ON DELETE RESTRICT,
    CHECK(action_key = node_id || ':' || desired_generation || ':' || desired_sha256)
)

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_desired_monotonic_generation
BEFORE INSERT ON whitelist_sidecar_desired
WHEN (
    NOT EXISTS (SELECT 1 FROM whitelist_sidecar_desired WHERE origin_id = NEW.origin_id)
    AND NEW.desired_generation <> 1
) OR (
    EXISTS (SELECT 1 FROM whitelist_sidecar_desired WHERE origin_id = NEW.origin_id)
    AND NEW.desired_generation <> (
        SELECT MAX(desired_generation) + 1 FROM whitelist_sidecar_desired WHERE origin_id = NEW.origin_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar desired generation must increment by one');
END

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_desired_origin_binding
BEFORE INSERT ON whitelist_sidecar_desired
WHEN NOT EXISTS (
    SELECT 1 FROM whitelist_sidecar_origins AS origin
    WHERE origin.origin_id = NEW.origin_id
      AND origin.node_id = NEW.node_id
      AND origin.release_id = NEW.release_id
      AND origin.profile_id = NEW.profile_id
      AND origin.preset_id = NEW.preset_id
      AND origin.config_digest = NEW.config_digest
      AND origin.active = 1
)
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar desired origin binding mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_desired_immutable_update
BEFORE UPDATE ON whitelist_sidecar_desired
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar desired state is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_desired_immutable_delete
BEFORE DELETE ON whitelist_sidecar_desired
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar desired state is immutable');
END

-- maestro:statement
CREATE INDEX idx_whitelist_sidecar_desired_origin_generation
ON whitelist_sidecar_desired(origin_id, desired_generation DESC)

-- maestro:statement
CREATE TABLE whitelist_sidecar_receipts (
    action_key TEXT PRIMARY KEY NOT NULL,
    origin_id TEXT NOT NULL,
    release_id TEXT NOT NULL,
    xray_process_boot_id TEXT NOT NULL,
    config_digest TEXT NOT NULL CHECK(length(config_digest) = 64 AND config_digest NOT GLOB '*[^0-9a-f]*'),
    desired_generation INTEGER NOT NULL CHECK(typeof(desired_generation) = 'integer' AND desired_generation BETWEEN 1 AND 9223372036854775806),
    managed_user_set_digest TEXT NOT NULL CHECK(length(managed_user_set_digest) = 64 AND managed_user_set_digest NOT GLOB '*[^0-9a-f]*'),
    applied_at_unix INTEGER NOT NULL CHECK(typeof(applied_at_unix) = 'integer' AND applied_at_unix BETWEEN 0 AND 9223372036854775806),
    expires_at_unix INTEGER NOT NULL CHECK(typeof(expires_at_unix) = 'integer' AND expires_at_unix BETWEEN 1 AND 9223372036854775806),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(action_key) REFERENCES whitelist_sidecar_desired(action_key) ON DELETE RESTRICT,
    FOREIGN KEY(origin_id, desired_generation) REFERENCES whitelist_sidecar_desired(origin_id, desired_generation) ON DELETE RESTRICT,
    CHECK(action_key <> '' AND release_id <> '' AND xray_process_boot_id <> '' AND expires_at_unix > applied_at_unix)
)

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_receipts_exact_desired
BEFORE INSERT ON whitelist_sidecar_receipts
WHEN NOT EXISTS (
    SELECT 1 FROM whitelist_sidecar_desired AS desired
    WHERE desired.action_key = NEW.action_key
      AND desired.origin_id = NEW.origin_id
      AND desired.release_id = NEW.release_id
      AND desired.config_digest = NEW.config_digest
      AND desired.desired_generation = NEW.desired_generation
      AND desired.managed_user_set_digest = NEW.managed_user_set_digest
)
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar receipt desired binding mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_receipts_immutable_update
BEFORE UPDATE ON whitelist_sidecar_receipts
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar receipt is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_sidecar_receipts_immutable_delete
BEFORE DELETE ON whitelist_sidecar_receipts
BEGIN
    SELECT RAISE(ABORT, 'white-list sidecar receipt is immutable');
END
