-- Durable payload reservations. Issuance is not a balance debit.
-- A lost process retains its unknown reservation until an authenticated final fence.

-- maestro:statement
CREATE TABLE whitelist_byte_allocations (
    entitlement_id TEXT NOT NULL,
    exit_id TEXT NOT NULL,
    origin_id TEXT NOT NULL,
    xray_process_boot_id TEXT NOT NULL CHECK(xray_process_boot_id<>''),
    admitted_action_key TEXT NOT NULL,
    billing_period_id TEXT NOT NULL,
    admitted_at_unix INTEGER NOT NULL CHECK(typeof(admitted_at_unix)='integer' AND admitted_at_unix>0),
    first_observed_at_unix INTEGER NOT NULL DEFAULT 0 CHECK(typeof(first_observed_at_unix)='integer' AND first_observed_at_unix>=0),
    cumulative_byte_ceiling INTEGER NOT NULL DEFAULT 0 CHECK(typeof(cumulative_byte_ceiling)='integer' AND cumulative_byte_ceiling BETWEEN 0 AND 9223372036854775806),
    retired_unbilled_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(retired_unbilled_bytes)='integer' AND retired_unbilled_bytes BETWEEN 0 AND cumulative_byte_ceiling),
    last_fenced_generation TEXT NOT NULL DEFAULT '0' CHECK(last_fenced_generation<>'' AND last_fenced_generation NOT GLOB '*[^0-9]*' AND (last_fenced_generation='0' OR substr(last_fenced_generation,1,1)<>'0') AND (length(last_fenced_generation)<20 OR (length(last_fenced_generation)=20 AND last_fenced_generation<='18446744073709551615'))),
    last_fenced_receipt_id TEXT NOT NULL DEFAULT '',
    last_fenced_proof_sha256 TEXT NOT NULL DEFAULT '',
    updated_at_unix INTEGER NOT NULL CHECK(typeof(updated_at_unix)='integer' AND updated_at_unix>0),
    PRIMARY KEY(entitlement_id,exit_id,origin_id,xray_process_boot_id),
    FOREIGN KEY(entitlement_id,exit_id) REFERENCES whitelist_route_credentials(entitlement_id,exit_id) ON DELETE RESTRICT,
    FOREIGN KEY(origin_id) REFERENCES whitelist_sidecar_origins(origin_id) ON DELETE RESTRICT,
    FOREIGN KEY(admitted_action_key) REFERENCES whitelist_sidecar_receipts(action_key) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id,billing_period_id) REFERENCES whitelist_billing_periods(entitlement_id,period_id) ON DELETE RESTRICT
)

-- maestro:statement
CREATE VIEW whitelist_byte_allocation_balances AS
SELECT allocation.*,allocation.cumulative_byte_ceiling-allocation.settled_bytes-allocation.retired_unbilled_bytes AS outstanding_bytes
FROM (
    SELECT budget.*,COALESCE((
        SELECT SUM(entry.consumed_delta_bytes)
        FROM whitelist_usage_applications AS application
        JOIN whitelist_balance_entries AS entry ON entry.entry_id=application.entry_id
        JOIN whitelist_commercial_metering_sources AS source ON source.event_id=application.interval_id
        JOIN whitelist_meter_epochs AS epoch ON epoch.meter_epoch=source.meter_epoch
        WHERE source.entitlement_id=budget.entitlement_id AND source.exit_id=budget.exit_id
          AND source.origin_id=budget.origin_id AND epoch.xray_process_boot_id=budget.xray_process_boot_id
    ),0) AS settled_bytes
    FROM whitelist_byte_allocations AS budget
) AS allocation

-- maestro:statement
CREATE TRIGGER whitelist_byte_allocations_exact_insert
BEFORE INSERT ON whitelist_byte_allocations
WHEN NEW.cumulative_byte_ceiling<>0 OR NEW.retired_unbilled_bytes<>0 OR NEW.last_fenced_generation<>'0'
 OR NEW.first_observed_at_unix<>0 OR NEW.last_fenced_receipt_id<>'' OR NEW.last_fenced_proof_sha256<>''
 OR NOT EXISTS (
    SELECT 1 FROM whitelist_metering_origin_observations AS observation
    JOIN whitelist_sidecar_receipts AS receipt ON receipt.action_key=observation.action_key
    JOIN whitelist_billing_periods AS period ON period.period_id=NEW.billing_period_id
    WHERE observation.origin_id=NEW.origin_id AND receipt.action_key=NEW.admitted_action_key
      AND receipt.xray_process_boot_id=NEW.xray_process_boot_id
      AND period.entitlement_id=NEW.entitlement_id
      AND period.starts_at_unix<=NEW.admitted_at_unix AND NEW.admitted_at_unix<period.ends_at_unix
 ) OR EXISTS (SELECT 1 FROM whitelist_first_use_admissions WHERE entitlement_id=NEW.entitlement_id)
 OR EXISTS (
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
BEGIN
    SELECT RAISE(ABORT,'white-list byte allocation lacks a new bound counter lifetime');
END

-- maestro:statement
CREATE TRIGGER whitelist_byte_allocations_immutable_binding
BEFORE UPDATE ON whitelist_byte_allocations
WHEN NEW.entitlement_id<>OLD.entitlement_id OR NEW.exit_id<>OLD.exit_id OR NEW.origin_id<>OLD.origin_id
 OR NEW.xray_process_boot_id<>OLD.xray_process_boot_id OR NEW.admitted_action_key<>OLD.admitted_action_key
 OR NEW.billing_period_id<>OLD.billing_period_id OR NEW.admitted_at_unix<>OLD.admitted_at_unix
 OR (OLD.first_observed_at_unix>0 AND NEW.first_observed_at_unix<>OLD.first_observed_at_unix)
 OR length(NEW.last_fenced_generation)<length(OLD.last_fenced_generation)
 OR (length(NEW.last_fenced_generation)=length(OLD.last_fenced_generation) AND NEW.last_fenced_generation<OLD.last_fenced_generation)
 OR (NEW.last_fenced_generation=OLD.last_fenced_generation AND (
     NEW.cumulative_byte_ceiling<OLD.cumulative_byte_ceiling OR NEW.retired_unbilled_bytes<>OLD.retired_unbilled_bytes
     OR NEW.last_fenced_receipt_id<>OLD.last_fenced_receipt_id OR NEW.last_fenced_proof_sha256<>OLD.last_fenced_proof_sha256))
BEGIN
    SELECT RAISE(ABORT,'white-list byte allocation binding or fence regressed');
END

-- maestro:statement
CREATE TRIGGER whitelist_byte_allocations_global_budget
BEFORE UPDATE ON whitelist_byte_allocations
WHEN NOT EXISTS (
    SELECT 1 FROM whitelist_byte_allocation_balances AS current
    JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=current.entitlement_id
    WHERE current.entitlement_id=OLD.entitlement_id AND current.exit_id=OLD.exit_id
      AND current.origin_id=OLD.origin_id AND current.xray_process_boot_id=OLD.xray_process_boot_id
      AND NEW.cumulative_byte_ceiling-current.settled_bytes-NEW.retired_unbilled_bytes>=0
      AND NEW.cumulative_byte_ceiling-current.settled_bytes-NEW.retired_unbilled_bytes <=
          projection.purchased_remaining_bytes+projection.included_remaining_bytes-COALESCE((
              SELECT SUM(other.outstanding_bytes) FROM whitelist_byte_allocation_balances AS other
              WHERE other.entitlement_id=OLD.entitlement_id AND NOT (
                  other.exit_id=OLD.exit_id AND other.origin_id=OLD.origin_id AND other.xray_process_boot_id=OLD.xray_process_boot_id)
          ),0)
      AND NOT EXISTS (SELECT 1 FROM whitelist_byte_allocation_balances AS invalid WHERE invalid.entitlement_id=OLD.entitlement_id AND invalid.outstanding_bytes<0)
)
BEGIN
    SELECT RAISE(ABORT,'white-list byte allocations exceed the shared balance');
END

-- maestro:statement
CREATE TRIGGER whitelist_byte_allocations_immutable_delete
BEFORE DELETE ON whitelist_byte_allocations
BEGIN
    SELECT RAISE(ABORT,'white-list byte allocation cannot be deleted');
END

-- maestro:statement
CREATE TRIGGER whitelist_first_use_admissions_no_byte_mode
BEFORE INSERT ON whitelist_first_use_admissions
WHEN EXISTS(SELECT 1 FROM whitelist_byte_allocations WHERE entitlement_id=NEW.entitlement_id)
BEGIN
    SELECT RAISE(ABORT,'white-list byte allocation cannot downgrade to measured admission');
END
