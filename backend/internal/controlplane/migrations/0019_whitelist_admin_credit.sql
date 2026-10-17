-- Free administrative CDN credit never creates an order or payment.
-- The existing migrator executes this entire FK-enabled migration atomically.
-- maestro:statement
PRAGMA defer_foreign_keys=ON

-- maestro:statement
CREATE TABLE whitelist_customer_access_sources (
 source_id TEXT PRIMARY KEY NOT NULL CHECK(source_id<>''),
 entitlement_id TEXT NOT NULL REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
 customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE RESTRICT,
 customer_generation INTEGER NOT NULL CHECK(typeof(customer_generation)='integer' AND customer_generation>=0),
 customer_expires_at_unix INTEGER NOT NULL CHECK(typeof(customer_expires_at_unix)='integer' AND customer_expires_at_unix BETWEEN 1 AND 9223372036854775806),
 captured_at_unix INTEGER NOT NULL CHECK(typeof(captured_at_unix)='integer' AND captured_at_unix>=0 AND captured_at_unix<customer_expires_at_unix),
 operation_id TEXT NOT NULL CHECK(operation_id<>''),
 request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 actor_hmac TEXT NOT NULL CHECK(actor_hmac<>''),
 UNIQUE(entitlement_id,source_id)
)

-- maestro:statement
CREATE TRIGGER whitelist_customer_access_sources_authority
BEFORE INSERT ON whitelist_customer_access_sources
WHEN NOT EXISTS(
 SELECT 1 FROM customers AS customer
 JOIN whitelist_entitlement_identities AS identity ON identity.customer_id=customer.customer_id
 WHERE identity.entitlement_id=NEW.entitlement_id AND customer.customer_id=NEW.customer_id
 AND customer.status='active' AND customer.generation=NEW.customer_generation
 AND customer.expires_at_unix=NEW.customer_expires_at_unix AND customer.expires_at_unix>NEW.captured_at_unix
 AND EXISTS(SELECT 1 FROM idempotency_requests AS request WHERE request.command_type='whitelist_admin_access'
 AND request.resource_id=NEW.entitlement_id AND request.operation_id=NEW.operation_id AND request.request_hash=NEW.request_hash AND request.status='applying')
)
BEGIN SELECT RAISE(ABORT,'invalid customer access authority'); END

-- maestro:statement
CREATE TRIGGER whitelist_customer_access_sources_immutable_update
BEFORE UPDATE ON whitelist_customer_access_sources
BEGIN SELECT RAISE(ABORT,'customer access authority is immutable'); END

-- maestro:statement
CREATE TRIGGER whitelist_customer_access_sources_immutable_delete
BEFORE DELETE ON whitelist_customer_access_sources
BEGIN SELECT RAISE(ABORT,'customer access authority is immutable'); END

-- maestro:statement
CREATE TABLE whitelist_periods_v19_copy AS SELECT * FROM whitelist_billing_periods

-- maestro:statement
DROP TABLE whitelist_billing_periods

-- maestro:statement
CREATE TABLE whitelist_billing_periods (
 period_id TEXT PRIMARY KEY NOT NULL,
 entitlement_id TEXT NOT NULL,
 period_ordinal INTEGER NOT NULL CHECK(typeof(period_ordinal)='integer' AND period_ordinal BETWEEN 0 AND 9223372036854775806),
 starts_at_unix INTEGER NOT NULL CHECK(typeof(starts_at_unix)='integer' AND starts_at_unix BETWEEN 0 AND 9223372036854775806),
 ends_at_unix INTEGER NOT NULL CHECK(typeof(ends_at_unix)='integer' AND ends_at_unix BETWEEN 0 AND 9223372036854775806 AND ends_at_unix>starts_at_unix),
 included_grant_bytes INTEGER NOT NULL CHECK(typeof(included_grant_bytes)='integer' AND included_grant_bytes BETWEEN 0 AND 9223372036854775806),
 access_order_id TEXT,
 created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix)='integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
 customer_access_source_id TEXT,
 UNIQUE(access_order_id,period_ordinal),
 UNIQUE(entitlement_id,period_ordinal),
 UNIQUE(entitlement_id,period_id),
 UNIQUE(customer_access_source_id),
 FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
 FOREIGN KEY(access_order_id) REFERENCES orders(order_id) ON DELETE RESTRICT,
 FOREIGN KEY(entitlement_id,customer_access_source_id) REFERENCES whitelist_customer_access_sources(entitlement_id,source_id) ON DELETE RESTRICT,
 CHECK(period_id<>'' AND (
 (access_order_id IS NOT NULL AND access_order_id<>'' AND customer_access_source_id IS NULL)
 OR (access_order_id IS NULL AND customer_access_source_id IS NOT NULL AND customer_access_source_id<>'' AND included_grant_bytes=0)))
)

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_order_matches_entitlement
BEFORE INSERT ON whitelist_billing_periods
WHEN NEW.access_order_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM orders AS source_order
 JOIN whitelist_entitlement_identities AS entitlement ON entitlement.entitlement_id=NEW.entitlement_id
 WHERE source_order.order_id=NEW.access_order_id AND source_order.customer_id=entitlement.customer_id
 AND source_order.payment_state='confirmed' AND source_order.decision='confirmed' AND source_order.confirmed_at_unix IS NOT NULL
)
BEGIN SELECT RAISE(ABORT,'white-list billing period order owner mismatch'); END

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_customer_access_matches
BEFORE INSERT ON whitelist_billing_periods
WHEN NEW.customer_access_source_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM whitelist_customer_access_sources AS source
 JOIN whitelist_entitlement_identities AS identity ON identity.entitlement_id=source.entitlement_id
 WHERE source.source_id=NEW.customer_access_source_id AND source.entitlement_id=NEW.entitlement_id
 AND source.customer_id=identity.customer_id AND NEW.starts_at_unix=source.captured_at_unix
 AND NEW.ends_at_unix=source.customer_expires_at_unix AND NEW.included_grant_bytes=0
)
BEGIN SELECT RAISE(ABORT,'white-list billing period customer access mismatch'); END

-- maestro:statement
INSERT INTO whitelist_billing_periods(period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,created_at_unix)
SELECT period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,created_at_unix FROM whitelist_periods_v19_copy

-- maestro:statement
DROP TABLE whitelist_periods_v19_copy

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_immutable_update
BEFORE UPDATE ON whitelist_billing_periods
BEGIN SELECT RAISE(ABORT,'white-list billing period is immutable'); END

-- maestro:statement
CREATE TRIGGER whitelist_billing_periods_immutable_delete
BEFORE DELETE ON whitelist_billing_periods
BEGIN SELECT RAISE(ABORT,'white-list billing period is immutable'); END

-- maestro:statement
CREATE TABLE whitelist_manual_credits (
 credit_id TEXT PRIMARY KEY NOT NULL CHECK(credit_id<>''),
 entitlement_id TEXT NOT NULL REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
 bytes INTEGER NOT NULL CHECK(typeof(bytes)='integer' AND bytes BETWEEN 1000000000 AND 9223372036854775806 AND bytes%1000000000=0),
 purchased_after_bytes INTEGER NOT NULL CHECK(typeof(purchased_after_bytes)='integer' AND purchased_after_bytes BETWEEN 1000000000 AND 9223372036854775806 AND purchased_after_bytes>=bytes),
 projection_version INTEGER NOT NULL CHECK(typeof(projection_version)='integer' AND projection_version>0),
 operation_id TEXT NOT NULL UNIQUE CHECK(operation_id<>''),
 request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 actor_hmac TEXT NOT NULL CHECK(actor_hmac<>''),
 created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix)='integer' AND created_at_unix>=0),
 UNIQUE(entitlement_id,projection_version)
)

-- maestro:statement
CREATE TRIGGER whitelist_manual_credits_immutable_update
BEFORE UPDATE ON whitelist_manual_credits
BEGIN SELECT RAISE(ABORT,'manual credit is immutable'); END

-- maestro:statement
CREATE TRIGGER whitelist_manual_credits_exact_binding
BEFORE INSERT ON whitelist_manual_credits
WHEN NOT EXISTS(
 SELECT 1 FROM whitelist_balance_projections AS projection JOIN idempotency_requests AS request ON request.resource_id=projection.entitlement_id
 WHERE projection.entitlement_id=NEW.entitlement_id AND projection.version=NEW.projection_version
 AND projection.purchased_remaining_bytes=NEW.purchased_after_bytes
 AND request.command_type='whitelist_manual_credit' AND request.operation_id=NEW.operation_id
 AND request.request_hash=NEW.request_hash AND request.status='applying'
)
BEGIN SELECT RAISE(ABORT,'manual credit projection proof mismatch'); END

-- maestro:statement
CREATE TRIGGER whitelist_admin_idempotency_applied_guard
BEFORE UPDATE OF status ON idempotency_requests
WHEN NEW.status='applied' AND (
 (NEW.command_type='whitelist_manual_credit' AND NOT EXISTS(
 SELECT 1 FROM whitelist_manual_credits AS credit JOIN whitelist_balance_projections AS projection ON projection.entitlement_id=credit.entitlement_id
 WHERE credit.entitlement_id=NEW.resource_id AND credit.operation_id=NEW.operation_id AND credit.request_hash=NEW.request_hash
 AND credit.credit_id=json_extract(NEW.response_json,'$.credit_id') AND credit.bytes=json_extract(NEW.response_json,'$.bytes')
 AND projection.version=credit.projection_version AND projection.version=json_extract(NEW.response_json,'$.projection_version')
 AND projection.purchased_remaining_bytes=credit.purchased_after_bytes AND credit.purchased_after_bytes=json_extract(NEW.response_json,'$.purchased_remaining_bytes')))
 OR (NEW.command_type='whitelist_admin_access' AND NOT EXISTS(
 SELECT 1 FROM whitelist_balance_projections AS projection JOIN whitelist_billing_periods AS period ON period.period_id=projection.current_period_id AND period.entitlement_id=projection.entitlement_id
 WHERE projection.entitlement_id=NEW.resource_id AND projection.version=json_extract(NEW.response_json,'$.projection_version')
 AND projection.purchased_remaining_bytes=json_extract(NEW.response_json,'$.purchased_remaining_bytes') AND json_extract(NEW.response_json,'$.bytes')=0))
)
BEGIN SELECT RAISE(ABORT,'administrative CDN balance proof incomplete'); END

-- maestro:statement
CREATE TRIGGER whitelist_manual_credits_immutable_delete
BEFORE DELETE ON whitelist_manual_credits
BEGIN SELECT RAISE(ABORT,'manual credit is immutable'); END

-- Abort the same transaction if restoring the parent lost any child binding.
-- maestro:statement
CREATE TABLE whitelist_v19_integrity_assert (valid INTEGER NOT NULL CHECK(valid=1))

-- maestro:statement
INSERT INTO whitelist_v19_integrity_assert(valid) SELECT 0 FROM pragma_foreign_key_check LIMIT 1

-- maestro:statement
DROP TABLE whitelist_v19_integrity_assert
