-- MaestroVPN HA durable, shadow-only white-list metering schema v10.

-- maestro:statement
CREATE TABLE whitelist_metering_periods (
    entitlement_id TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    transport_id TEXT NOT NULL,
    xray_identity TEXT NOT NULL,
    unit TEXT NOT NULL CHECK(unit IN ('GB_DECIMAL','GIB_BINARY')),
    basis TEXT NOT NULL CHECK(basis IN ('DOWNLINK_ONLY','UPLINK_PLUS_DOWNLINK','FREE')),
    included_bytes TEXT NOT NULL CHECK(
        included_bytes = '0' OR (
            included_bytes <> '' AND included_bytes NOT GLOB '*[^0-9]*' AND
            substr(included_bytes,1,1) <> '0' AND
            (length(included_bytes) < 20 OR (length(included_bytes) = 20 AND included_bytes <= '18446744073709551615'))
        )
    ),
    soft_limit_bytes TEXT NOT NULL CHECK(
        soft_limit_bytes = '0' OR (
            soft_limit_bytes <> '' AND soft_limit_bytes NOT GLOB '*[^0-9]*' AND
            substr(soft_limit_bytes,1,1) <> '0' AND
            (length(soft_limit_bytes) < 20 OR (length(soft_limit_bytes) = 20 AND soft_limit_bytes <= '18446744073709551615'))
        )
    ),
    hard_limit_bytes TEXT NOT NULL CHECK(
        hard_limit_bytes = '0' OR (
            hard_limit_bytes <> '' AND hard_limit_bytes NOT GLOB '*[^0-9]*' AND
            substr(hard_limit_bytes,1,1) <> '0' AND
            (length(hard_limit_bytes) < 20 OR (length(hard_limit_bytes) = 20 AND hard_limit_bytes <= '18446744073709551615'))
        )
    ),
    grace_bytes TEXT NOT NULL CHECK(
        grace_bytes = '0' OR (
            grace_bytes <> '' AND grace_bytes NOT GLOB '*[^0-9]*' AND
            substr(grace_bytes,1,1) <> '0' AND
            (length(grace_bytes) < 20 OR (length(grace_bytes) = 20 AND grace_bytes <= '18446744073709551615'))
        )
    ),
    price_mode TEXT NOT NULL CHECK(price_mode IN ('PAID','FREE')),
    price_source TEXT NOT NULL CHECK(price_source IN ('INDIVIDUAL','TARIFF','PROFILE','GLOBAL')),
    currency TEXT NOT NULL,
    minor_units_per_unit TEXT NOT NULL CHECK(
        minor_units_per_unit = '0' OR (
            minor_units_per_unit <> '' AND minor_units_per_unit NOT GLOB '*[^0-9]*' AND
            substr(minor_units_per_unit,1,1) <> '0' AND
            (length(minor_units_per_unit) < 20 OR (length(minor_units_per_unit) = 20 AND minor_units_per_unit <= '18446744073709551615'))
        )
    ),
    policy_sha256 TEXT NOT NULL CHECK(length(policy_sha256) = 64 AND policy_sha256 NOT GLOB '*[^0-9a-f]*'),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    PRIMARY KEY(entitlement_id,billing_period_id),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    CHECK(account_id <> '' AND billing_period_id <> '' AND transport_id <> ''),
    CHECK(xray_identity = 'wl:' || entitlement_id),
    CHECK(
        (price_mode = 'FREE' AND minor_units_per_unit = '0') OR
        (price_mode = 'PAID' AND currency <> '' AND minor_units_per_unit <> '0')
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_metering_periods_immutable_update
BEFORE UPDATE ON whitelist_metering_periods
BEGIN
    SELECT RAISE(ABORT, 'white-list metering period is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_metering_periods_immutable_delete
BEFORE DELETE ON whitelist_metering_periods
BEGIN
    SELECT RAISE(ABORT, 'white-list metering period is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_metering_checkpoints (
    entitlement_id TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    meter_epoch TEXT NOT NULL,
    xray_identity TEXT NOT NULL,
    counter_generation TEXT NOT NULL,
    sample_sequence TEXT NOT NULL,
    uplink_bytes TEXT NOT NULL,
    downlink_bytes TEXT NOT NULL,
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0),
    PRIMARY KEY(entitlement_id,billing_period_id,instance_id,meter_epoch,xray_identity),
    FOREIGN KEY(entitlement_id,billing_period_id)
        REFERENCES whitelist_metering_periods(entitlement_id,billing_period_id) ON DELETE RESTRICT,
    CHECK(instance_id <> '' AND meter_epoch <> '' AND xray_identity = 'wl:' || entitlement_id),
    CHECK(counter_generation <> '0' AND counter_generation <> '' AND counter_generation NOT GLOB '*[^0-9]*' AND substr(counter_generation,1,1) <> '0' AND (length(counter_generation) < 20 OR (length(counter_generation) = 20 AND counter_generation <= '18446744073709551615'))),
    CHECK(sample_sequence <> '0' AND sample_sequence <> '' AND sample_sequence NOT GLOB '*[^0-9]*' AND substr(sample_sequence,1,1) <> '0' AND (length(sample_sequence) < 20 OR (length(sample_sequence) = 20 AND sample_sequence <= '18446744073709551615'))),
    CHECK(uplink_bytes = '0' OR (uplink_bytes <> '' AND uplink_bytes NOT GLOB '*[^0-9]*' AND substr(uplink_bytes,1,1) <> '0' AND (length(uplink_bytes) < 20 OR (length(uplink_bytes) = 20 AND uplink_bytes <= '18446744073709551615')))),
    CHECK(downlink_bytes = '0' OR (downlink_bytes <> '' AND downlink_bytes NOT GLOB '*[^0-9]*' AND substr(downlink_bytes,1,1) <> '0' AND (length(downlink_bytes) < 20 OR (length(downlink_bytes) = 20 AND downlink_bytes <= '18446744073709551615'))))
)

-- maestro:statement
CREATE TABLE whitelist_metering_events (
    event_id TEXT PRIMARY KEY,
    entitlement_id TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    meter_epoch TEXT NOT NULL,
    xray_identity TEXT NOT NULL,
    counter_generation TEXT NOT NULL,
    sample_sequence TEXT NOT NULL,
    uplink_bytes TEXT NOT NULL,
    downlink_bytes TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256) = 64 AND payload_sha256 NOT GLOB '*[^0-9a-f]*'),
    diagnostic TEXT NOT NULL CHECK(diagnostic IN ('','EPOCH_STARTED','COUNTER_RESET','LATE_SAMPLE','ORDERING_STARTED')),
    has_interval INTEGER NOT NULL CHECK(has_interval IN (0,1)),
    result_json TEXT NOT NULL CHECK(json_valid(result_json)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    FOREIGN KEY(entitlement_id,billing_period_id)
        REFERENCES whitelist_metering_periods(entitlement_id,billing_period_id) ON DELETE RESTRICT,
    CHECK(event_id <> '' AND instance_id <> '' AND meter_epoch <> ''),
    CHECK(xray_identity = 'wl:' || entitlement_id),
    CHECK(counter_generation <> '0' AND counter_generation <> '' AND counter_generation NOT GLOB '*[^0-9]*' AND substr(counter_generation,1,1) <> '0' AND (length(counter_generation) < 20 OR (length(counter_generation) = 20 AND counter_generation <= '18446744073709551615'))),
    CHECK(sample_sequence <> '0' AND sample_sequence <> '' AND sample_sequence NOT GLOB '*[^0-9]*' AND substr(sample_sequence,1,1) <> '0' AND (length(sample_sequence) < 20 OR (length(sample_sequence) = 20 AND sample_sequence <= '18446744073709551615'))),
    CHECK(uplink_bytes = '0' OR (uplink_bytes <> '' AND uplink_bytes NOT GLOB '*[^0-9]*' AND substr(uplink_bytes,1,1) <> '0' AND (length(uplink_bytes) < 20 OR (length(uplink_bytes) = 20 AND uplink_bytes <= '18446744073709551615')))),
    CHECK(downlink_bytes = '0' OR (downlink_bytes <> '' AND downlink_bytes NOT GLOB '*[^0-9]*' AND substr(downlink_bytes,1,1) <> '0' AND (length(downlink_bytes) < 20 OR (length(downlink_bytes) = 20 AND downlink_bytes <= '18446744073709551615'))))
)

-- maestro:statement
CREATE TRIGGER whitelist_metering_events_immutable_update
BEFORE UPDATE ON whitelist_metering_events
BEGIN
    SELECT RAISE(ABORT, 'white-list metering event is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_metering_events_immutable_delete
BEFORE DELETE ON whitelist_metering_events
BEGIN
    SELECT RAISE(ABORT, 'white-list metering event is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_metering_intervals (
    event_id TEXT PRIMARY KEY REFERENCES whitelist_metering_events(event_id) ON DELETE RESTRICT,
    uplink_delta_bytes TEXT NOT NULL,
    downlink_delta_bytes TEXT NOT NULL,
    billable_bytes TEXT NOT NULL,
    amount_numerator TEXT NOT NULL CHECK(amount_numerator <> '' AND amount_numerator NOT GLOB '*[^0-9]*' AND (amount_numerator = '0' OR substr(amount_numerator,1,1) <> '0')),
    amount_denominator TEXT NOT NULL CHECK(amount_denominator <> '0' AND amount_denominator <> '' AND amount_denominator NOT GLOB '*[^0-9]*' AND substr(amount_denominator,1,1) <> '0'),
    currency TEXT NOT NULL,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    CHECK(uplink_delta_bytes = '0' OR (uplink_delta_bytes <> '' AND uplink_delta_bytes NOT GLOB '*[^0-9]*' AND substr(uplink_delta_bytes,1,1) <> '0' AND (length(uplink_delta_bytes) < 20 OR (length(uplink_delta_bytes) = 20 AND uplink_delta_bytes <= '18446744073709551615')))),
    CHECK(downlink_delta_bytes = '0' OR (downlink_delta_bytes <> '' AND downlink_delta_bytes NOT GLOB '*[^0-9]*' AND substr(downlink_delta_bytes,1,1) <> '0' AND (length(downlink_delta_bytes) < 20 OR (length(downlink_delta_bytes) = 20 AND downlink_delta_bytes <= '18446744073709551615')))),
    CHECK(billable_bytes = '0' OR (billable_bytes <> '' AND billable_bytes NOT GLOB '*[^0-9]*' AND substr(billable_bytes,1,1) <> '0' AND (length(billable_bytes) < 20 OR (length(billable_bytes) = 20 AND billable_bytes <= '18446744073709551615'))))
)

-- maestro:statement
CREATE TRIGGER whitelist_metering_intervals_immutable_update
BEFORE UPDATE ON whitelist_metering_intervals
BEGIN
    SELECT RAISE(ABORT, 'white-list metering interval is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_metering_intervals_immutable_delete
BEFORE DELETE ON whitelist_metering_intervals
BEGIN
    SELECT RAISE(ABORT, 'white-list metering interval is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_metering_projections (
    entitlement_id TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    used_bytes TEXT NOT NULL,
    included_bytes TEXT NOT NULL,
    remaining_bytes TEXT NOT NULL,
    soft_limit_reached INTEGER NOT NULL CHECK(soft_limit_reached IN (0,1)),
    hard_limit_recommended INTEGER NOT NULL CHECK(hard_limit_recommended IN (0,1)),
    suspension_reason TEXT NOT NULL CHECK(suspension_reason IN ('','HARD_LIMIT')),
    reconciliation_pending INTEGER NOT NULL CHECK(reconciliation_pending IN (0,1)),
    reconciliation_diagnostic TEXT NOT NULL CHECK(reconciliation_diagnostic IN ('','EPOCH_STARTED','COUNTER_RESET','LATE_SAMPLE','ORDERING_STARTED')),
    version INTEGER NOT NULL CHECK(version > 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0),
    PRIMARY KEY(entitlement_id,billing_period_id),
    FOREIGN KEY(entitlement_id,billing_period_id)
        REFERENCES whitelist_metering_periods(entitlement_id,billing_period_id) ON DELETE RESTRICT,
    CHECK((hard_limit_recommended = 0 AND suspension_reason = '') OR (hard_limit_recommended = 1 AND suspension_reason = 'HARD_LIMIT')),
    CHECK(used_bytes = '0' OR (used_bytes <> '' AND used_bytes NOT GLOB '*[^0-9]*' AND substr(used_bytes,1,1) <> '0' AND (length(used_bytes) < 20 OR (length(used_bytes) = 20 AND used_bytes <= '18446744073709551615')))),
    CHECK(included_bytes = '0' OR (included_bytes <> '' AND included_bytes NOT GLOB '*[^0-9]*' AND substr(included_bytes,1,1) <> '0' AND (length(included_bytes) < 20 OR (length(included_bytes) = 20 AND included_bytes <= '18446744073709551615')))),
    CHECK(remaining_bytes = '0' OR (remaining_bytes <> '' AND remaining_bytes NOT GLOB '*[^0-9]*' AND substr(remaining_bytes,1,1) <> '0' AND (length(remaining_bytes) < 20 OR (length(remaining_bytes) = 20 AND remaining_bytes <= '18446744073709551615'))))
)
