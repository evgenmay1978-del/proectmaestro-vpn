-- MaestroVPN authenticated collector health and first-use admission schema v17.
-- Health/presence metadata is not a usage sample, checkpoint, or balance watermark.

-- maestro:statement
CREATE TABLE whitelist_metering_origin_observations (
    origin_id TEXT PRIMARY KEY NOT NULL,
    action_key TEXT NOT NULL,
    sampled_at_unix INTEGER NOT NULL CHECK(typeof(sampled_at_unix) = 'integer' AND sampled_at_unix BETWEEN 1 AND 9223372036854775806),
    checked_at_unix INTEGER NOT NULL CHECK(typeof(checked_at_unix) = 'integer' AND checked_at_unix BETWEEN sampled_at_unix AND 9223372036854775806),
    available_users_json BLOB NOT NULL CHECK(length(available_users_json) > 0),
    unavailable_users_json BLOB NOT NULL CHECK(length(unavailable_users_json) > 0),
    observation_sha256 TEXT NOT NULL CHECK(length(observation_sha256) = 64 AND observation_sha256 NOT GLOB '*[^0-9a-f]*'),
    FOREIGN KEY(origin_id) REFERENCES whitelist_sidecar_origins(origin_id) ON DELETE RESTRICT,
    FOREIGN KEY(action_key) REFERENCES whitelist_sidecar_receipts(action_key) ON DELETE RESTRICT
)

-- maestro:statement
CREATE TRIGGER whitelist_metering_origin_observations_exact_insert
BEFORE INSERT ON whitelist_metering_origin_observations
WHEN NOT EXISTS (
    SELECT 1 FROM whitelist_sidecar_receipts AS receipt
    JOIN whitelist_sidecar_desired AS desired ON desired.action_key=receipt.action_key
    JOIN whitelist_sidecar_origins AS origin ON origin.origin_id=desired.origin_id
    WHERE receipt.action_key=NEW.action_key AND receipt.origin_id=NEW.origin_id
      AND origin.active=1 AND origin.node_id=desired.node_id
      AND origin.release_id=desired.release_id AND origin.profile_id=desired.profile_id
      AND origin.preset_id=desired.preset_id AND origin.config_digest=desired.config_digest
      AND desired.desired_generation=(SELECT MAX(current.desired_generation) FROM whitelist_sidecar_desired AS current WHERE current.origin_id=NEW.origin_id)
      AND receipt.applied_at_unix<=NEW.sampled_at_unix AND NEW.checked_at_unix<receipt.expires_at_unix
)
BEGIN
    SELECT RAISE(ABORT, 'white-list observation receipt is not current');
END

-- maestro:statement
CREATE TRIGGER whitelist_metering_origin_observations_exact_update
BEFORE UPDATE ON whitelist_metering_origin_observations
WHEN NEW.origin_id<>OLD.origin_id OR NOT EXISTS (
    SELECT 1 FROM whitelist_sidecar_receipts AS receipt
    JOIN whitelist_sidecar_desired AS desired ON desired.action_key=receipt.action_key
    JOIN whitelist_sidecar_origins AS origin ON origin.origin_id=desired.origin_id
    WHERE receipt.action_key=NEW.action_key AND receipt.origin_id=NEW.origin_id
      AND origin.active=1 AND origin.node_id=desired.node_id
      AND origin.release_id=desired.release_id AND origin.profile_id=desired.profile_id
      AND origin.preset_id=desired.preset_id AND origin.config_digest=desired.config_digest
      AND desired.desired_generation=(SELECT MAX(current.desired_generation) FROM whitelist_sidecar_desired AS current WHERE current.origin_id=NEW.origin_id)
      AND receipt.applied_at_unix<=NEW.sampled_at_unix AND NEW.checked_at_unix<receipt.expires_at_unix
)
BEGIN
    SELECT RAISE(ABORT, 'white-list observation receipt is not current');
END

-- maestro:statement
CREATE TRIGGER whitelist_metering_origin_observations_monotonic
BEFORE UPDATE ON whitelist_metering_origin_observations
WHEN NEW.sampled_at_unix<OLD.sampled_at_unix OR NEW.checked_at_unix<OLD.checked_at_unix
 OR (NEW.sampled_at_unix=OLD.sampled_at_unix AND NEW.action_key=OLD.action_key AND NEW.observation_sha256<>OLD.observation_sha256)
BEGIN
    SELECT RAISE(ABORT, 'white-list observation is out of order');
END

-- maestro:statement
CREATE TABLE whitelist_first_use_admissions (
    entitlement_id TEXT NOT NULL,
    exit_id TEXT NOT NULL,
    origin_id TEXT NOT NULL,
    xray_process_boot_id TEXT NOT NULL CHECK(xray_process_boot_id <> ''),
    admitted_action_key TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    admitted_at_unix INTEGER NOT NULL CHECK(typeof(admitted_at_unix) = 'integer' AND admitted_at_unix BETWEEN 1 AND 9223372036854775806),
    zero_start_authorized INTEGER NOT NULL CHECK(typeof(zero_start_authorized) = 'integer' AND zero_start_authorized IN (0,1)),
    first_observed_at_unix INTEGER NOT NULL CHECK(typeof(first_observed_at_unix) = 'integer' AND first_observed_at_unix BETWEEN 0 AND 9223372036854775806),
    reserve_bytes INTEGER NOT NULL CHECK(typeof(reserve_bytes) = 'integer' AND reserve_bytes BETWEEN 10000000 AND 9223372036854775806),
    reserve_measured_at_unix INTEGER NOT NULL CHECK(typeof(reserve_measured_at_unix) = 'integer' AND reserve_measured_at_unix BETWEEN 1 AND 9223372036854775806),
    reserve_until_unix INTEGER NOT NULL CHECK(typeof(reserve_until_unix) = 'integer' AND reserve_until_unix BETWEEN 1 AND 9223372036854775806 AND reserve_until_unix>reserve_measured_at_unix),
    PRIMARY KEY(entitlement_id,exit_id,origin_id,xray_process_boot_id),
    FOREIGN KEY(entitlement_id,exit_id) REFERENCES whitelist_route_credentials(entitlement_id,exit_id) ON DELETE RESTRICT,
    FOREIGN KEY(origin_id) REFERENCES whitelist_sidecar_origins(origin_id) ON DELETE RESTRICT,
    FOREIGN KEY(admitted_action_key) REFERENCES whitelist_sidecar_receipts(action_key) ON DELETE RESTRICT,
    FOREIGN KEY(billing_period_id) REFERENCES whitelist_billing_periods(period_id) ON DELETE RESTRICT,
    CHECK(zero_start_authorized=1 OR first_observed_at_unix>0)
)

-- maestro:statement
CREATE TRIGGER whitelist_first_use_admissions_exact_insert
BEFORE INSERT ON whitelist_first_use_admissions
WHEN NOT EXISTS (
    SELECT 1 FROM whitelist_metering_origin_observations AS observation
    JOIN whitelist_sidecar_receipts AS receipt ON receipt.action_key=observation.action_key
    JOIN whitelist_billing_periods AS period ON period.period_id=NEW.billing_period_id
    WHERE observation.origin_id=NEW.origin_id AND receipt.action_key=NEW.admitted_action_key
      AND receipt.xray_process_boot_id=NEW.xray_process_boot_id
      AND period.entitlement_id=NEW.entitlement_id
      AND period.starts_at_unix<=NEW.admitted_at_unix AND NEW.admitted_at_unix<period.ends_at_unix
) OR (NEW.zero_start_authorized=1 AND (
    EXISTS (
        SELECT 1 FROM whitelist_sidecar_desired AS desired
        LEFT JOIN whitelist_sidecar_receipts AS receipt ON receipt.action_key=desired.action_key
        WHERE desired.origin_id=NEW.origin_id
          AND instr(CAST(desired.payload_json AS TEXT),'"wl:' || NEW.entitlement_id || ':' || NEW.exit_id || '"')>0
          AND (receipt.action_key IS NULL OR receipt.xray_process_boot_id=NEW.xray_process_boot_id)
    ) OR EXISTS (
        SELECT 1 FROM whitelist_commercial_metering_sources AS source
        JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=source.meter_epoch
        WHERE source.entitlement_id=NEW.entitlement_id AND source.exit_id=NEW.exit_id
          AND source.origin_id=NEW.origin_id AND epoch.xray_process_boot_id=NEW.xray_process_boot_id
    )
))
BEGIN
    SELECT RAISE(ABORT, 'white-list first-use admission lacks a new bound counter lifetime');
END

-- maestro:statement
CREATE TRIGGER whitelist_first_use_admissions_immutable_binding
BEFORE UPDATE ON whitelist_first_use_admissions
WHEN NEW.entitlement_id<>OLD.entitlement_id OR NEW.exit_id<>OLD.exit_id OR NEW.origin_id<>OLD.origin_id
 OR NEW.xray_process_boot_id<>OLD.xray_process_boot_id OR NEW.admitted_action_key<>OLD.admitted_action_key
 OR NEW.billing_period_id<>OLD.billing_period_id OR NEW.admitted_at_unix<>OLD.admitted_at_unix
 OR NEW.zero_start_authorized<>OLD.zero_start_authorized
BEGIN
    SELECT RAISE(ABORT, 'white-list first-use admission binding is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_first_use_admissions_monotonic
BEFORE UPDATE ON whitelist_first_use_admissions
WHEN (OLD.first_observed_at_unix>0 AND NEW.first_observed_at_unix<>OLD.first_observed_at_unix)
 OR NEW.reserve_measured_at_unix<OLD.reserve_measured_at_unix
BEGIN
    SELECT RAISE(ABORT, 'white-list first-use observation cannot be reopened');
END

-- maestro:statement
CREATE TRIGGER whitelist_first_use_admissions_immutable_delete
BEFORE DELETE ON whitelist_first_use_admissions
BEGIN
    SELECT RAISE(ABORT, 'white-list first-use admission is immutable');
END
