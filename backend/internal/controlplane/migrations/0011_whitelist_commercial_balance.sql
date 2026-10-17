-- MaestroVPN HA commercial white-list balance schema v11.
-- Migrations v1-v10 remain byte-for-byte immutable.

-- maestro:statement
CREATE TABLE whitelist_meter_epochs (
    meter_epoch TEXT PRIMARY KEY NOT NULL,
    origin_id TEXT NOT NULL,
    counter_source_id TEXT NOT NULL,
    xray_process_boot_id TEXT NOT NULL,
    reset_sequence INTEGER NOT NULL CHECK(typeof(reset_sequence) = 'integer' AND reset_sequence BETWEEN 0 AND 9223372036854775806),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    UNIQUE(origin_id, counter_source_id, xray_process_boot_id, reset_sequence),
    CHECK(
        meter_epoch <> '' AND origin_id <> '' AND counter_source_id <> '' AND
        xray_process_boot_id <> ''
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_meter_epochs_immutable_update
BEFORE UPDATE ON whitelist_meter_epochs
BEGIN
    SELECT RAISE(ABORT, 'white-list meter epoch is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_meter_epochs_immutable_delete
BEFORE DELETE ON whitelist_meter_epochs
BEGIN
    SELECT RAISE(ABORT, 'white-list meter epoch is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_billing_periods (
    period_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    period_ordinal INTEGER NOT NULL CHECK(typeof(period_ordinal) = 'integer' AND period_ordinal BETWEEN 0 AND 9223372036854775806),
    starts_at_unix INTEGER NOT NULL CHECK(typeof(starts_at_unix) = 'integer' AND starts_at_unix BETWEEN 0 AND 9223372036854775806),
    ends_at_unix INTEGER NOT NULL CHECK(typeof(ends_at_unix) = 'integer' AND ends_at_unix BETWEEN 0 AND 9223372036854775806 AND ends_at_unix > starts_at_unix),
    included_grant_bytes INTEGER NOT NULL CHECK(typeof(included_grant_bytes) = 'integer' AND included_grant_bytes BETWEEN 0 AND 9223372036854775806),
    access_order_id TEXT NOT NULL,
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    UNIQUE(access_order_id, period_ordinal),
    UNIQUE(entitlement_id, period_ordinal),
    UNIQUE(entitlement_id, period_id),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(access_order_id) REFERENCES orders(order_id) ON DELETE RESTRICT,
    CHECK(period_id <> '' AND access_order_id <> '' AND ends_at_unix > starts_at_unix)
)

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_order_matches_entitlement
BEFORE INSERT ON whitelist_billing_periods
WHEN NOT EXISTS (
    SELECT 1
    FROM orders AS source_order
    JOIN whitelist_entitlement_identities AS entitlement
      ON entitlement.entitlement_id = NEW.entitlement_id
    WHERE source_order.order_id = NEW.access_order_id
      AND source_order.customer_id = entitlement.customer_id
      AND source_order.payment_state = 'confirmed'
      AND source_order.decision = 'confirmed'
      AND source_order.confirmed_at_unix IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'white-list billing period order owner mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_immutable_update
BEFORE UPDATE ON whitelist_billing_periods
BEGIN
    SELECT RAISE(ABORT, 'white-list billing period is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_immutable_delete
BEFORE DELETE ON whitelist_billing_periods
BEGIN
    SELECT RAISE(ABORT, 'white-list billing period is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_balance_entries (
    entry_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    period_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('INCLUDED_GRANT','PURCHASED_CREDIT','CONSUMED','UNCOVERED','ADJUSTMENT')),
    included_delta_bytes INTEGER NOT NULL CHECK(typeof(included_delta_bytes) = 'integer' AND included_delta_bytes BETWEEN -9223372036854775806 AND 9223372036854775806),
    purchased_delta_bytes INTEGER NOT NULL CHECK(typeof(purchased_delta_bytes) = 'integer' AND purchased_delta_bytes BETWEEN -9223372036854775806 AND 9223372036854775806),
    consumed_delta_bytes INTEGER NOT NULL CHECK(typeof(consumed_delta_bytes) = 'integer' AND consumed_delta_bytes BETWEEN -9223372036854775806 AND 9223372036854775806),
    uncovered_delta_bytes INTEGER NOT NULL CHECK(typeof(uncovered_delta_bytes) = 'integer' AND uncovered_delta_bytes BETWEEN -9223372036854775806 AND 9223372036854775806),
    source_order_id TEXT,
    interval_id TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    metadata_sha256 TEXT NOT NULL CHECK(length(metadata_sha256) = 64 AND metadata_sha256 NOT GLOB '*[^0-9a-f]*'),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    UNIQUE(entry_id, entitlement_id, period_id, interval_id),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id, period_id)
        REFERENCES whitelist_billing_periods(entitlement_id, period_id) ON DELETE RESTRICT,
    FOREIGN KEY(source_order_id) REFERENCES orders(order_id) ON DELETE RESTRICT,
    FOREIGN KEY(interval_id) REFERENCES whitelist_metering_intervals(event_id) ON DELETE RESTRICT,
    CHECK(entry_id <> '' AND idempotency_key <> ''),
    CHECK(
        (source_order_id IS NOT NULL AND interval_id IS NULL) OR
        (source_order_id IS NULL AND interval_id IS NOT NULL)
    ),
    CHECK(
        (kind IN ('INCLUDED_GRANT','PURCHASED_CREDIT') AND source_order_id IS NOT NULL) OR
        (kind IN ('CONSUMED','UNCOVERED') AND interval_id IS NOT NULL) OR
        kind = 'ADJUSTMENT'
    ),
    CHECK(
        (kind = 'INCLUDED_GRANT' AND included_delta_bytes > 0 AND
            purchased_delta_bytes = 0 AND consumed_delta_bytes = 0 AND uncovered_delta_bytes = 0) OR
        (kind = 'PURCHASED_CREDIT' AND included_delta_bytes = 0 AND
            purchased_delta_bytes > 0 AND consumed_delta_bytes = 0 AND uncovered_delta_bytes = 0) OR
        (kind = 'CONSUMED' AND included_delta_bytes <= 0 AND purchased_delta_bytes <= 0 AND
            consumed_delta_bytes > 0 AND uncovered_delta_bytes >= 0 AND
            consumed_delta_bytes = -included_delta_bytes - purchased_delta_bytes + uncovered_delta_bytes) OR
        (kind = 'UNCOVERED' AND included_delta_bytes = 0 AND purchased_delta_bytes = 0 AND
            consumed_delta_bytes > 0 AND uncovered_delta_bytes = consumed_delta_bytes) OR
        (kind = 'ADJUSTMENT' AND
            (included_delta_bytes <> 0 OR purchased_delta_bytes <> 0 OR
             consumed_delta_bytes <> 0 OR uncovered_delta_bytes <> 0))
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_balance_entries_order_matches_entitlement
BEFORE INSERT ON whitelist_balance_entries
WHEN NEW.source_order_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM orders AS source_order
    JOIN whitelist_entitlement_identities AS entitlement
      ON entitlement.entitlement_id = NEW.entitlement_id
    WHERE source_order.order_id = NEW.source_order_id
      AND source_order.customer_id = entitlement.customer_id
      AND source_order.payment_state = 'confirmed'
      AND source_order.decision = 'confirmed'
      AND source_order.confirmed_at_unix IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'white-list balance entry order owner mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_balance_entries_included_matches_period_order
BEFORE INSERT ON whitelist_balance_entries
WHEN NEW.kind = 'INCLUDED_GRANT' AND NOT EXISTS (
    SELECT 1
    FROM whitelist_billing_periods AS period
    WHERE period.period_id = NEW.period_id
      AND period.entitlement_id = NEW.entitlement_id
      AND period.access_order_id = NEW.source_order_id
)
BEGIN
    SELECT RAISE(ABORT, 'white-list included grant order does not match billing period');
END

-- maestro:statement
CREATE TRIGGER whitelist_balance_entries_interval_matches_period
BEFORE INSERT ON whitelist_balance_entries
WHEN NEW.interval_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM whitelist_metering_intervals AS interval
    JOIN whitelist_metering_events AS event ON event.event_id = interval.event_id
    JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch = event.meter_epoch
    WHERE interval.event_id = NEW.interval_id
      AND event.entitlement_id = NEW.entitlement_id
      AND event.billing_period_id = NEW.period_id
      AND epoch.origin_id = event.instance_id
)
BEGIN
    SELECT RAISE(ABORT, 'white-list balance interval binding mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_balance_entries_immutable_update
BEFORE UPDATE ON whitelist_balance_entries
BEGIN
    SELECT RAISE(ABORT, 'white-list balance entry is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_balance_entries_immutable_delete
BEFORE DELETE ON whitelist_balance_entries
BEGIN
    SELECT RAISE(ABORT, 'white-list balance entry is immutable');
END

-- maestro:statement
CREATE UNIQUE INDEX whitelist_balance_entries_purchased_source_order_once
ON whitelist_balance_entries(source_order_id)
WHERE kind = 'PURCHASED_CREDIT'

-- maestro:statement
CREATE UNIQUE INDEX whitelist_balance_entries_included_source_period_once
ON whitelist_balance_entries(source_order_id, period_id)
WHERE kind = 'INCLUDED_GRANT'

-- maestro:statement
CREATE TABLE whitelist_balance_projections (
    entitlement_id TEXT PRIMARY KEY NOT NULL,
    current_period_id TEXT,
    included_remaining_bytes INTEGER NOT NULL CHECK(typeof(included_remaining_bytes) = 'integer' AND included_remaining_bytes BETWEEN 0 AND 9223372036854775806),
    purchased_remaining_bytes INTEGER NOT NULL CHECK(typeof(purchased_remaining_bytes) = 'integer' AND purchased_remaining_bytes BETWEEN 0 AND 9223372036854775806),
    lifetime_consumed_bytes INTEGER NOT NULL CHECK(typeof(lifetime_consumed_bytes) = 'integer' AND lifetime_consumed_bytes BETWEEN 0 AND 9223372036854775806),
    uncovered_bytes INTEGER NOT NULL CHECK(typeof(uncovered_bytes) = 'integer' AND uncovered_bytes BETWEEN 0 AND 9223372036854775806),
    version INTEGER NOT NULL CHECK(typeof(version) = 'integer' AND version BETWEEN 1 AND 9223372036854775806),
    pending INTEGER NOT NULL CHECK(typeof(pending) = 'integer' AND pending IN (0,1)),
    fresh_through_unix INTEGER NOT NULL CHECK(typeof(fresh_through_unix) = 'integer' AND fresh_through_unix BETWEEN 0 AND 9223372036854775806),
    updated_at_unix INTEGER NOT NULL CHECK(typeof(updated_at_unix) = 'integer' AND updated_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id, current_period_id)
        REFERENCES whitelist_billing_periods(entitlement_id, period_id) ON DELETE RESTRICT
)

-- maestro:statement
CREATE TRIGGER whitelist_balance_projections_initial_version
BEFORE INSERT ON whitelist_balance_projections
WHEN NEW.version <> 1
BEGIN
    SELECT RAISE(ABORT, 'white-list balance projection initial version must be one');
END

-- maestro:statement
CREATE TRIGGER whitelist_balance_projections_monotonic_version
BEFORE UPDATE ON whitelist_balance_projections
WHEN NEW.version <> OLD.version + 1
BEGIN
    SELECT RAISE(ABORT, 'white-list balance projection version must increment by one');
END

-- maestro:statement
CREATE TRIGGER whitelist_balance_projections_immutable_delete
BEFORE DELETE ON whitelist_balance_projections
BEGIN
    SELECT RAISE(ABORT, 'white-list balance projection cannot be deleted');
END

-- maestro:statement
CREATE TABLE whitelist_usage_applications (
    application_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    period_id TEXT NOT NULL,
    meter_epoch TEXT NOT NULL,
    interval_id TEXT NOT NULL,
    entry_id TEXT NOT NULL UNIQUE,
    applied_at_unix INTEGER NOT NULL CHECK(typeof(applied_at_unix) = 'integer' AND applied_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id, period_id)
        REFERENCES whitelist_billing_periods(entitlement_id, period_id) ON DELETE RESTRICT,
    FOREIGN KEY(meter_epoch) REFERENCES whitelist_meter_epochs(meter_epoch) ON DELETE RESTRICT,
    FOREIGN KEY(interval_id) REFERENCES whitelist_metering_intervals(event_id) ON DELETE RESTRICT,
    FOREIGN KEY(entry_id, entitlement_id, period_id, interval_id)
        REFERENCES whitelist_balance_entries(entry_id, entitlement_id, period_id, interval_id) ON DELETE RESTRICT,
    UNIQUE(meter_epoch, interval_id),
    UNIQUE(interval_id),
    CHECK(application_id <> '' AND meter_epoch <> '' AND interval_id <> '')
)

-- maestro:statement
CREATE TRIGGER whitelist_usage_applications_meter_epoch_matches_interval
BEFORE INSERT ON whitelist_usage_applications
WHEN NOT EXISTS (
    SELECT 1
    FROM whitelist_metering_events AS event
    JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch = event.meter_epoch
    WHERE event.event_id = NEW.interval_id
      AND event.meter_epoch = NEW.meter_epoch
      AND event.entitlement_id = NEW.entitlement_id
      AND event.billing_period_id = NEW.period_id
      AND epoch.origin_id = event.instance_id
)
BEGIN
    SELECT RAISE(ABORT, 'white-list usage application meter epoch mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_usage_applications_immutable_update
BEFORE UPDATE ON whitelist_usage_applications
BEGIN
    SELECT RAISE(ABORT, 'white-list usage application is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_usage_applications_immutable_delete
BEFORE DELETE ON whitelist_usage_applications
BEGIN
    SELECT RAISE(ABORT, 'white-list usage application is immutable');
END
