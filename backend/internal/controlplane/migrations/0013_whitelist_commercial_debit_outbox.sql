-- MaestroVPN HA commercial white-list debit outbox schema v13.
-- Migrations v1-v12 remain byte-for-byte immutable.

-- maestro:statement
CREATE TABLE whitelist_commercial_debit_outbox (
    event_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    meter_epoch TEXT NOT NULL,
    basis TEXT NOT NULL CHECK(basis = 'UPLINK_PLUS_DOWNLINK'),
    interval_end_unix INTEGER NOT NULL CHECK(
        typeof(interval_end_unix) = 'integer' AND
        interval_end_unix BETWEEN 1 AND 9223372036854775806
    ),
    source_sha256 TEXT NOT NULL CHECK(
        length(source_sha256) = 64 AND
        source_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    receipt_key TEXT NOT NULL UNIQUE CHECK(
        length(receipt_key) = 64 AND
        receipt_key NOT GLOB '*[^0-9a-f]*'
    ),
    request_hash TEXT NOT NULL CHECK(
        length(request_hash) = 64 AND
        request_hash NOT GLOB '*[^0-9a-f]*'
    ),
    created_at_unix INTEGER NOT NULL CHECK(
        typeof(created_at_unix) = 'integer' AND
        created_at_unix BETWEEN 0 AND 9223372036854775806
    ),
    FOREIGN KEY(event_id)
        REFERENCES whitelist_commercial_metering_sources(event_id)
        ON DELETE RESTRICT,
    CHECK(
        event_id <> '' AND entitlement_id <> '' AND
        billing_period_id <> '' AND meter_epoch <> ''
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_commercial_debit_outbox_exact_binding
BEFORE INSERT ON whitelist_commercial_debit_outbox
WHEN NOT EXISTS (
    SELECT 1
    FROM whitelist_commercial_metering_sources AS source
    JOIN whitelist_metering_events AS event
      ON event.event_id = source.event_id
    JOIN whitelist_metering_intervals AS interval
      ON interval.event_id = event.event_id
    JOIN whitelist_metering_periods AS policy
      ON policy.entitlement_id = event.entitlement_id
     AND policy.billing_period_id = event.billing_period_id
    WHERE source.event_id = NEW.event_id
      AND source.entitlement_id = NEW.entitlement_id
      AND source.billing_period_id = NEW.billing_period_id
      AND source.meter_epoch = NEW.meter_epoch
      AND source.basis = NEW.basis
      AND source.sampled_at_unix = NEW.interval_end_unix
      AND source.source_sha256 = NEW.source_sha256
      AND source.counter_generation = '1'
      AND event.entitlement_id = NEW.entitlement_id
      AND event.billing_period_id = NEW.billing_period_id
      AND event.meter_epoch = NEW.meter_epoch
      AND event.has_interval = 1
      AND policy.xray_identity = event.xray_identity
      AND policy.basis = NEW.basis
      AND policy.basis = 'UPLINK_PLUS_DOWNLINK'
      AND policy.included_bytes = '0'
)
BEGIN
    SELECT RAISE(
        ABORT,
        'white-list commercial debit outbox binding mismatch'
    );
END

-- maestro:statement
CREATE INDEX idx_whitelist_commercial_debit_outbox_order
ON whitelist_commercial_debit_outbox(entitlement_id, interval_end_unix, event_id)

-- maestro:statement
CREATE INDEX idx_whitelist_commercial_metering_sources_entitlement_time
ON whitelist_commercial_metering_sources(entitlement_id, sampled_at_unix, event_id)

-- maestro:statement
CREATE TRIGGER whitelist_commercial_debit_outbox_immutable_update
BEFORE UPDATE ON whitelist_commercial_debit_outbox
BEGIN
    SELECT RAISE(
        ABORT,
        'white-list commercial debit outbox is immutable'
    );
END

-- maestro:statement
CREATE TRIGGER whitelist_commercial_debit_outbox_immutable_delete
BEFORE DELETE ON whitelist_commercial_debit_outbox
BEGIN
    SELECT RAISE(
        ABORT,
        'white-list commercial debit outbox is immutable'
    );
END
