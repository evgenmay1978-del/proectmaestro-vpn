-- MaestroVPN HA commercial white-list metering source schema v12.
-- Migrations v1-v11 remain byte-for-byte immutable.

-- maestro:statement
CREATE TABLE whitelist_commercial_metering_sources (
    event_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    origin_id TEXT NOT NULL,
    exit_id TEXT NOT NULL,
    meter_epoch TEXT NOT NULL,
    route_xray_identity TEXT NOT NULL,
	    counter_generation TEXT NOT NULL CHECK(
	        counter_generation <> '0' AND counter_generation <> '' AND
	        counter_generation NOT GLOB '*[^0-9]*' AND
	        substr(counter_generation,1,1) <> '0' AND
	        (length(counter_generation) < 20 OR
	         (length(counter_generation) = 20 AND counter_generation <= '18446744073709551615'))
	    ),
	    sample_sequence TEXT NOT NULL CHECK(
	        sample_sequence <> '0' AND sample_sequence <> '' AND
	        sample_sequence NOT GLOB '*[^0-9]*' AND
	        substr(sample_sequence,1,1) <> '0' AND
	        (length(sample_sequence) < 20 OR
	         (length(sample_sequence) = 20 AND sample_sequence <= '18446744073709551615'))
	    ),
    basis TEXT NOT NULL CHECK(basis = 'UPLINK_PLUS_DOWNLINK'),
    sampled_at_unix INTEGER NOT NULL CHECK(
        typeof(sampled_at_unix) = 'integer' AND
        sampled_at_unix BETWEEN 1 AND 9223372036854775806
    ),
    source_sha256 TEXT NOT NULL UNIQUE CHECK(
        length(source_sha256) = 64 AND
        source_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
	    UNIQUE(meter_epoch, route_xray_identity, counter_generation, sample_sequence),

    FOREIGN KEY(event_id)
        REFERENCES whitelist_metering_events(event_id) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id,billing_period_id)
        REFERENCES whitelist_metering_periods(entitlement_id,billing_period_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id,billing_period_id)
        REFERENCES whitelist_billing_periods(entitlement_id,period_id)
        ON DELETE RESTRICT,
    FOREIGN KEY(meter_epoch)
        REFERENCES whitelist_meter_epochs(meter_epoch) ON DELETE RESTRICT,

    CHECK(
        event_id <> '' AND entitlement_id <> '' AND
        billing_period_id <> '' AND origin_id <> '' AND meter_epoch <> ''
    ),
    CHECK(
        length(exit_id) BETWEEN 1 AND 128 AND
        exit_id NOT GLOB '*[^0-9A-Za-z._-]*'
    ),
    CHECK(
        route_xray_identity =
        'wl:' || entitlement_id || ':' || exit_id
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_commercial_metering_sources_exact_binding
BEFORE INSERT ON whitelist_commercial_metering_sources
WHEN NOT EXISTS (
    SELECT 1
    FROM whitelist_metering_events AS event
    JOIN whitelist_metering_periods AS policy
      ON policy.entitlement_id = event.entitlement_id
     AND policy.billing_period_id = event.billing_period_id
    JOIN whitelist_billing_periods AS commercial_period
      ON commercial_period.entitlement_id = event.entitlement_id
     AND commercial_period.period_id = event.billing_period_id
    JOIN whitelist_meter_epochs AS epoch
      ON epoch.meter_epoch = event.meter_epoch
    WHERE event.event_id = NEW.event_id
      AND event.entitlement_id = NEW.entitlement_id
      AND event.billing_period_id = NEW.billing_period_id
      AND event.meter_epoch = NEW.meter_epoch
      AND event.instance_id = NEW.origin_id
      AND event.xray_identity = 'wl:' || NEW.entitlement_id
	      AND event.counter_generation = NEW.counter_generation
	      AND event.sample_sequence = NEW.sample_sequence
      AND policy.xray_identity = event.xray_identity
      AND policy.basis = NEW.basis
      AND epoch.origin_id = NEW.origin_id
      AND NEW.sampled_at_unix BETWEEN
          commercial_period.starts_at_unix AND commercial_period.ends_at_unix
)
BEGIN
    SELECT RAISE(
        ABORT,
        'white-list commercial metering source binding mismatch'
    );
END

-- maestro:statement
CREATE TRIGGER whitelist_commercial_metering_sources_immutable_update
BEFORE UPDATE ON whitelist_commercial_metering_sources
BEGIN
    SELECT RAISE(
        ABORT,
        'white-list commercial metering source is immutable'
    );
END

-- maestro:statement
CREATE TRIGGER whitelist_commercial_metering_sources_immutable_delete
BEFORE DELETE ON whitelist_commercial_metering_sources
BEGIN
    SELECT RAISE(
        ABORT,
        'white-list commercial metering source is immutable'
    );
END
