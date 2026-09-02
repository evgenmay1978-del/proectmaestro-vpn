-- MaestroVPN HA immutable white-list GB products, top-up orders, and publication controls schema v14.
-- Migrations v1-v13 remain byte-for-byte immutable.

-- maestro:statement
INSERT INTO tariff_versions(
    tariff_version_id, tariff_code, duration_days, amount_minor, currency, active, created_at_unix
) VALUES
    ('wl-gb-5-v1','wl-gb-5',1,10000,'RUB',0,0),
    ('wl-gb-20-v1','wl-gb-20',1,30000,'RUB',0,0),
    ('wl-gb-50-v1','wl-gb-50',1,60000,'RUB',0,0),
    ('wl-gb-100-v1','wl-gb-100',1,100000,'RUB',0,0)

-- maestro:statement
CREATE TABLE whitelist_gb_products (
    product_id TEXT PRIMARY KEY NOT NULL,
    bytes INTEGER NOT NULL CHECK(typeof(bytes) = 'integer' AND bytes BETWEEN 1 AND 9223372036854775806),
    unit TEXT NOT NULL CHECK(unit = 'GB_DECIMAL'),
    kind TEXT NOT NULL CHECK(kind = 'WHITELIST_BYTES'),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(product_id) REFERENCES tariff_versions(tariff_version_id) ON DELETE RESTRICT,
    CHECK(product_id <> '')
)

-- maestro:statement
CREATE TRIGGER whitelist_gb_products_hidden_tariff
BEFORE INSERT ON whitelist_gb_products
WHEN NOT EXISTS (
    SELECT 1 FROM tariff_versions AS tariff
    WHERE tariff.tariff_version_id = NEW.product_id
      AND tariff.active = 0
      AND tariff.currency = 'RUB'
)
BEGIN
    SELECT RAISE(ABORT, 'white-list GB product requires a hidden RUB tariff');
END

-- maestro:statement
CREATE TRIGGER whitelist_gb_products_immutable_update
BEFORE UPDATE ON whitelist_gb_products
BEGIN
    SELECT RAISE(ABORT, 'white-list GB product is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_gb_products_immutable_delete
BEFORE DELETE ON whitelist_gb_products
BEGIN
    SELECT RAISE(ABORT, 'white-list GB product is immutable');
END

-- maestro:statement
INSERT INTO whitelist_gb_products(product_id,bytes,unit,kind,created_at_unix) VALUES
    ('wl-gb-5-v1',5000000000,'GB_DECIMAL','WHITELIST_BYTES',0),
    ('wl-gb-20-v1',20000000000,'GB_DECIMAL','WHITELIST_BYTES',0),
    ('wl-gb-50-v1',50000000000,'GB_DECIMAL','WHITELIST_BYTES',0),
    ('wl-gb-100-v1',100000000000,'GB_DECIMAL','WHITELIST_BYTES',0)

-- maestro:statement
CREATE TABLE whitelist_topup_orders (
    order_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    product_id TEXT NOT NULL,
    creation_request_hash TEXT NOT NULL CHECK(length(creation_request_hash) = 64 AND creation_request_hash NOT GLOB '*[^0-9a-f]*'),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(order_id) REFERENCES orders(order_id) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(product_id) REFERENCES whitelist_gb_products(product_id) ON DELETE RESTRICT,
    CHECK(order_id <> '' AND entitlement_id <> '' AND product_id <> '')
)

-- maestro:statement
CREATE TRIGGER whitelist_topup_orders_exact_terms
BEFORE INSERT ON whitelist_topup_orders
WHEN NOT EXISTS (
    SELECT 1
    FROM orders AS source_order
    JOIN whitelist_entitlement_identities AS entitlement
      ON entitlement.entitlement_id = NEW.entitlement_id
    JOIN whitelist_gb_products AS product
      ON product.product_id = NEW.product_id
    JOIN tariff_versions AS tariff
      ON tariff.tariff_version_id = product.product_id
    WHERE source_order.order_id = NEW.order_id
      AND source_order.customer_id = entitlement.customer_id
      AND source_order.tariff_version_id = product.product_id
      AND source_order.amount_minor = tariff.amount_minor
      AND source_order.currency = tariff.currency
      AND source_order.duration_days = tariff.duration_days
      AND source_order.payment_state = 'created'
      AND source_order.provisioning_state = 'none'
      AND source_order.decision IS NULL
      AND source_order.confirmed_at_unix IS NULL
      AND source_order.result_expires_at_unix IS NULL
      AND source_order.result_generation IS NULL
      AND source_order.created_at_unix = NEW.created_at_unix
)
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up order terms do not match anchor order');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_orders_immutable_update
BEFORE UPDATE ON whitelist_topup_orders
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up order is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_orders_immutable_delete
BEFORE DELETE ON whitelist_topup_orders
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up order is immutable');
END

-- maestro:statement
CREATE INDEX idx_whitelist_topup_orders_entitlement_time
ON whitelist_topup_orders(entitlement_id, created_at_unix, order_id)

-- maestro:statement
CREATE TABLE whitelist_topup_payment_claims (
    order_id TEXT PRIMARY KEY NOT NULL,
    operation_id TEXT NOT NULL UNIQUE,
    request_hash TEXT NOT NULL CHECK(length(request_hash) = 64 AND request_hash NOT GLOB '*[^0-9a-f]*'),
    claimed_at_unix INTEGER NOT NULL CHECK(typeof(claimed_at_unix) = 'integer' AND claimed_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(order_id) REFERENCES whitelist_topup_orders(order_id) ON DELETE RESTRICT,
    CHECK(order_id <> '' AND operation_id <> '')
)

-- maestro:statement
CREATE TRIGGER whitelist_topup_payment_claims_live_order
BEFORE INSERT ON whitelist_topup_payment_claims
WHEN NOT EXISTS (
    SELECT 1 FROM orders
    WHERE order_id = NEW.order_id
      AND payment_state = 'created'
      AND decision IS NULL
      AND expires_at_unix > NEW.claimed_at_unix
)
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up claim requires a live order');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_payment_claims_immutable_update
BEFORE UPDATE ON whitelist_topup_payment_claims
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up payment claim is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_payment_claims_immutable_delete
BEFORE DELETE ON whitelist_topup_payment_claims
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up payment claim is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_publication_controls (
    control_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(typeof(version) = 'integer' AND version BETWEEN 1 AND 9223372036854775806),
    enabled INTEGER NOT NULL CHECK(typeof(enabled) = 'integer' AND enabled IN (0,1)),
    source TEXT NOT NULL CHECK(source IN ('DEFAULT_OFF','CONFIRMED_GB_PURCHASE','ADMIN_ENABLE','ADMIN_DISABLE')),
    source_topup_order_id TEXT,
    operation_id TEXT UNIQUE,
    request_hash TEXT CHECK(request_hash IS NULL OR (length(request_hash) = 64 AND request_hash NOT GLOB '*[^0-9a-f]*')),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    UNIQUE(entitlement_id, version),
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE CASCADE,
    FOREIGN KEY(source_topup_order_id) REFERENCES whitelist_topup_orders(order_id) ON DELETE RESTRICT,
    CHECK(control_id <> ''),
    CHECK(
        (source = 'DEFAULT_OFF' AND enabled = 0 AND source_topup_order_id IS NULL AND operation_id IS NULL AND request_hash IS NULL) OR
        (source = 'CONFIRMED_GB_PURCHASE' AND enabled = 1 AND source_topup_order_id IS NOT NULL AND operation_id IS NOT NULL AND request_hash IS NOT NULL) OR
        (source = 'ADMIN_ENABLE' AND enabled = 1 AND source_topup_order_id IS NULL AND operation_id IS NOT NULL AND request_hash IS NOT NULL) OR
        (source = 'ADMIN_DISABLE' AND enabled = 0 AND source_topup_order_id IS NULL AND operation_id IS NOT NULL AND request_hash IS NOT NULL)
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_publication_controls_monotonic_version
BEFORE INSERT ON whitelist_publication_controls
WHEN (
    NOT EXISTS (
        SELECT 1 FROM whitelist_publication_controls
        WHERE entitlement_id = NEW.entitlement_id
    ) AND NEW.version <> 1
) OR (
    EXISTS (
        SELECT 1 FROM whitelist_publication_controls
        WHERE entitlement_id = NEW.entitlement_id
    ) AND NEW.version <> (
        SELECT MAX(version) + 1 FROM whitelist_publication_controls
        WHERE entitlement_id = NEW.entitlement_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'white-list publication control version must increment by one');
END

-- maestro:statement
CREATE TRIGGER whitelist_publication_controls_purchase_owner
BEFORE INSERT ON whitelist_publication_controls
WHEN NEW.source = 'CONFIRMED_GB_PURCHASE' AND NOT EXISTS (
    SELECT 1
    FROM whitelist_topup_orders AS topup
    JOIN orders AS source_order ON source_order.order_id = topup.order_id
    JOIN payments AS payment ON payment.order_id = topup.order_id
    JOIN whitelist_balance_entries AS entry ON entry.source_order_id = topup.order_id
    JOIN idempotency_requests AS request ON request.resource_id = topup.order_id
    WHERE topup.order_id = NEW.source_topup_order_id
      AND topup.entitlement_id = NEW.entitlement_id
      AND source_order.payment_state = 'confirmed'
      AND source_order.decision = 'confirmed'
      AND source_order.operation_id = NEW.operation_id
      AND payment.provider_event_id IS NOT NULL
      AND payment.amount_minor = source_order.amount_minor
      AND payment.currency = source_order.currency
      AND entry.entitlement_id = topup.entitlement_id
      AND entry.kind = 'PURCHASED_CREDIT'
      AND request.command_type = 'whitelist_topup_confirm'
      AND request.request_hash = NEW.request_hash
      AND request.operation_id = NEW.operation_id
      AND request.status = 'applying'
)
BEGIN
    SELECT RAISE(ABORT, 'white-list publication purchase owner mismatch');
END

-- maestro:statement
CREATE TRIGGER whitelist_publication_controls_immutable_update
BEFORE UPDATE ON whitelist_publication_controls
BEGIN
    SELECT RAISE(ABORT, 'white-list publication control is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_publication_controls_immutable_delete
BEFORE DELETE ON whitelist_publication_controls
WHEN EXISTS (
    SELECT 1 FROM whitelist_entitlement_identities
    WHERE entitlement_id = OLD.entitlement_id
)
BEGIN
    SELECT RAISE(ABORT, 'white-list publication control is immutable');
END

-- maestro:statement
CREATE UNIQUE INDEX whitelist_publication_controls_purchase_once
ON whitelist_publication_controls(source_topup_order_id)
WHERE source = 'CONFIRMED_GB_PURCHASE'

-- maestro:statement
-- whitelist_publication_controls_default_existing
INSERT INTO whitelist_publication_controls(
    control_id,entitlement_id,version,enabled,source,source_topup_order_id,
    operation_id,request_hash,created_at_unix
)
SELECT 'wlpub-default:' || entitlement_id,entitlement_id,1,0,'DEFAULT_OFF',NULL,NULL,NULL,
       CAST(strftime('%s','now') AS INTEGER)
FROM whitelist_entitlement_identities

-- maestro:statement
CREATE TRIGGER whitelist_publication_controls_default_new
AFTER INSERT ON whitelist_entitlement_identities
BEGIN
    INSERT INTO whitelist_publication_controls(
        control_id,entitlement_id,version,enabled,source,source_topup_order_id,
        operation_id,request_hash,created_at_unix
    ) VALUES(
        'wlpub-default:' || NEW.entitlement_id,NEW.entitlement_id,1,0,'DEFAULT_OFF',
        NULL,NULL,NULL,NEW.created_at_unix
    );
END

-- maestro:statement
CREATE TABLE whitelist_topup_results (
    order_id TEXT PRIMARY KEY NOT NULL,
    decision TEXT NOT NULL CHECK(decision IN ('CONFIRMED','REJECTED')),
    operation_id TEXT NOT NULL UNIQUE,
    request_hash TEXT NOT NULL CHECK(length(request_hash) = 64 AND request_hash NOT GLOB '*[^0-9a-f]*'),
    payment_reference_hmac TEXT UNIQUE CHECK(payment_reference_hmac IS NULL OR (length(payment_reference_hmac) = 64 AND payment_reference_hmac NOT GLOB '*[^0-9a-f]*')),
    payment_id TEXT UNIQUE,
    period_id TEXT,
    balance_entry_id TEXT UNIQUE,
    control_id TEXT UNIQUE,
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    FOREIGN KEY(order_id) REFERENCES whitelist_topup_orders(order_id) ON DELETE RESTRICT,
    FOREIGN KEY(payment_id) REFERENCES payments(payment_id) ON DELETE RESTRICT,
    FOREIGN KEY(period_id) REFERENCES whitelist_billing_periods(period_id) ON DELETE RESTRICT,
    FOREIGN KEY(balance_entry_id) REFERENCES whitelist_balance_entries(entry_id) ON DELETE RESTRICT,
    FOREIGN KEY(control_id) REFERENCES whitelist_publication_controls(control_id) ON DELETE RESTRICT,
    CHECK(order_id <> '' AND operation_id <> ''),
    CHECK(
        (decision = 'CONFIRMED' AND payment_reference_hmac IS NOT NULL AND payment_id IS NOT NULL AND
            period_id IS NOT NULL AND balance_entry_id IS NOT NULL AND control_id IS NOT NULL) OR
        (decision = 'REJECTED' AND payment_reference_hmac IS NULL AND payment_id IS NULL AND
            period_id IS NULL AND balance_entry_id IS NULL AND control_id IS NULL)
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_topup_results_exact_binding
BEFORE INSERT ON whitelist_topup_results
WHEN (
    NEW.decision = 'CONFIRMED' AND NOT EXISTS (
        SELECT 1
        FROM whitelist_topup_orders AS topup
        JOIN orders AS source_order ON source_order.order_id = topup.order_id
        JOIN whitelist_gb_products AS product ON product.product_id = topup.product_id
        JOIN payments AS payment ON payment.payment_id = NEW.payment_id
        JOIN whitelist_billing_periods AS period ON period.period_id = NEW.period_id
        JOIN whitelist_balance_entries AS entry ON entry.entry_id = NEW.balance_entry_id
        JOIN whitelist_publication_controls AS control ON control.control_id = NEW.control_id
        WHERE topup.order_id = NEW.order_id
          AND source_order.payment_state = 'confirmed'
          AND source_order.decision = 'confirmed'
          AND source_order.operation_id = NEW.operation_id
          AND payment.order_id = topup.order_id
          AND payment.provider_event_id = NEW.payment_reference_hmac
          AND payment.amount_minor = source_order.amount_minor
          AND payment.currency = source_order.currency
          AND period.entitlement_id = topup.entitlement_id
          AND period.included_grant_bytes = 0
          AND NOT EXISTS (
              SELECT 1 FROM whitelist_topup_orders AS period_topup
              WHERE period_topup.order_id = period.access_order_id
          )
          AND entry.entitlement_id = topup.entitlement_id
          AND entry.period_id = period.period_id
          AND entry.kind = 'PURCHASED_CREDIT'
          AND entry.source_order_id = topup.order_id
          AND entry.purchased_delta_bytes = product.bytes
          AND entry.included_delta_bytes = 0
          AND entry.consumed_delta_bytes = 0
          AND entry.uncovered_delta_bytes = 0
          AND control.entitlement_id = topup.entitlement_id
          AND control.enabled = 1
          AND control.source = 'CONFIRMED_GB_PURCHASE'
          AND control.source_topup_order_id = topup.order_id
          AND control.operation_id = NEW.operation_id
          AND control.request_hash = NEW.request_hash
    )
) OR (
    NEW.decision = 'REJECTED' AND (
        NOT EXISTS (
            SELECT 1 FROM orders
            WHERE order_id = NEW.order_id
              AND payment_state = 'canceled'
              AND decision = 'cancelled'
              AND operation_id = NEW.operation_id
        ) OR EXISTS(SELECT 1 FROM payments WHERE order_id = NEW.order_id)
          OR EXISTS(SELECT 1 FROM whitelist_balance_entries WHERE source_order_id = NEW.order_id)
          OR EXISTS(SELECT 1 FROM whitelist_publication_controls WHERE source_topup_order_id = NEW.order_id)
    )
)
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up result is incomplete');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_results_immutable_update
BEFORE UPDATE ON whitelist_topup_results
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up result is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_results_immutable_delete
BEFORE DELETE ON whitelist_topup_results
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up result is immutable');
END

-- maestro:statement
CREATE TABLE whitelist_renewal_intents (
    access_order_id TEXT PRIMARY KEY NOT NULL,
    entitlement_id TEXT NOT NULL,
    operation_id TEXT NOT NULL UNIQUE,
    period_id TEXT UNIQUE,
    target_generation INTEGER NOT NULL CHECK(typeof(target_generation) = 'integer' AND target_generation BETWEEN 1 AND 9223372036854775806),
    confirmed_at_unix INTEGER NOT NULL CHECK(typeof(confirmed_at_unix) = 'integer' AND confirmed_at_unix BETWEEN 0 AND 9223372036854775806),
    target_ends_at_unix INTEGER NOT NULL CHECK(typeof(target_ends_at_unix) = 'integer' AND target_ends_at_unix BETWEEN 1 AND 9223372036854775806),
    status TEXT NOT NULL CHECK(status IN ('pending','applied')),
    projection_version INTEGER CHECK(projection_version IS NULL OR (typeof(projection_version) = 'integer' AND projection_version BETWEEN 1 AND 9223372036854775806)),
    created_at_unix INTEGER NOT NULL CHECK(typeof(created_at_unix) = 'integer' AND created_at_unix BETWEEN 0 AND 9223372036854775806),
    applied_at_unix INTEGER CHECK(applied_at_unix IS NULL OR (typeof(applied_at_unix) = 'integer' AND applied_at_unix BETWEEN 0 AND 9223372036854775806)),
    UNIQUE(entitlement_id, target_generation),
    FOREIGN KEY(access_order_id) REFERENCES orders(order_id) ON DELETE RESTRICT,
    FOREIGN KEY(entitlement_id) REFERENCES whitelist_entitlement_identities(entitlement_id) ON DELETE RESTRICT,
    FOREIGN KEY(period_id) REFERENCES whitelist_billing_periods(period_id) ON DELETE RESTRICT,
    CHECK(access_order_id <> '' AND entitlement_id <> '' AND operation_id <> ''),
    CHECK(period_id IS NULL OR period_id <> ''),
    CHECK(target_ends_at_unix > confirmed_at_unix),
    CHECK(
        (status = 'pending' AND period_id IS NULL AND projection_version IS NULL AND applied_at_unix IS NULL) OR
        (status = 'applied' AND period_id IS NOT NULL AND projection_version IS NOT NULL AND applied_at_unix IS NOT NULL)
    )
)

-- maestro:statement
CREATE TRIGGER whitelist_renewal_intents_exact_binding
BEFORE INSERT ON whitelist_renewal_intents
WHEN NEW.status <> 'pending'
  OR NEW.projection_version IS NOT NULL
  OR NEW.period_id IS NOT NULL
  OR NEW.applied_at_unix IS NOT NULL
  OR NOT EXISTS (
      SELECT 1
      FROM orders AS source_order
      JOIN customers AS customer ON customer.customer_id = source_order.customer_id
      JOIN whitelist_entitlement_identities AS entitlement
        ON entitlement.customer_id = customer.customer_id
      JOIN whitelist_balance_projections AS projection
        ON projection.entitlement_id = entitlement.entitlement_id
      JOIN idempotency_requests AS request
        ON request.resource_id = source_order.order_id
      WHERE source_order.order_id = NEW.access_order_id
        AND source_order.payment_state = 'confirmed'
        AND source_order.decision = 'confirmed'
        AND source_order.operation_id = NEW.operation_id
        AND source_order.confirmed_at_unix = NEW.confirmed_at_unix
        AND source_order.result_generation = NEW.target_generation
        AND source_order.result_expires_at_unix = NEW.target_ends_at_unix
        AND customer.generation = NEW.target_generation
        AND customer.expires_at_unix = NEW.target_ends_at_unix
        AND entitlement.entitlement_id = NEW.entitlement_id
        AND request.scope = 'order:' || source_order.order_id
        AND request.command_type = 'confirm'
        AND request.operation_id = NEW.operation_id
        AND request.status = 'applying'
        AND NOT EXISTS (
            SELECT 1 FROM whitelist_topup_orders
            WHERE order_id = source_order.order_id
        )
  )
BEGIN
    SELECT RAISE(ABORT, 'white-list renewal intent does not match ordinary renewal');
END

-- maestro:statement
CREATE TRIGGER whitelist_renewal_intents_applied_binding
BEFORE UPDATE OF status ON whitelist_renewal_intents
WHEN NEW.status = 'applied' AND NOT EXISTS (
    SELECT 1
    FROM whitelist_billing_periods AS period
    JOIN whitelist_balance_projections AS projection
      ON projection.entitlement_id = period.entitlement_id
    JOIN orders AS source_order ON source_order.order_id = period.access_order_id
    WHERE period.period_id = NEW.period_id
      AND period.entitlement_id = NEW.entitlement_id
      AND period.access_order_id = NEW.access_order_id
      AND period.included_grant_bytes = 0
      AND period.ends_at_unix = NEW.target_ends_at_unix
      AND projection.version = NEW.projection_version
      AND source_order.operation_id = NEW.operation_id
      AND source_order.result_generation = NEW.target_generation
      AND source_order.confirmed_at_unix = NEW.confirmed_at_unix
      AND source_order.result_expires_at_unix = NEW.target_ends_at_unix
)
BEGIN
    SELECT RAISE(ABORT, 'white-list renewal intent applied result is incomplete');
END

-- maestro:statement
CREATE TRIGGER whitelist_renewal_intents_immutable_update
BEFORE UPDATE ON whitelist_renewal_intents
WHEN NOT (
    OLD.status = 'pending' AND NEW.status = 'applied'
    AND NEW.access_order_id = OLD.access_order_id
    AND NEW.entitlement_id = OLD.entitlement_id
    AND NEW.operation_id = OLD.operation_id
    AND OLD.period_id IS NULL
    AND NEW.period_id IS NOT NULL
    AND NEW.target_generation = OLD.target_generation
    AND NEW.confirmed_at_unix = OLD.confirmed_at_unix
    AND NEW.target_ends_at_unix = OLD.target_ends_at_unix
    AND NEW.created_at_unix = OLD.created_at_unix
    AND OLD.projection_version IS NULL
    AND NEW.projection_version IS NOT NULL
    AND OLD.applied_at_unix IS NULL
    AND NEW.applied_at_unix IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'white-list renewal intent is immutable');
END

-- maestro:statement
CREATE TRIGGER whitelist_renewal_intents_immutable_delete
BEFORE DELETE ON whitelist_renewal_intents
BEGIN
    SELECT RAISE(ABORT, 'white-list renewal intent is immutable');
END

-- maestro:statement
CREATE INDEX idx_whitelist_renewal_intents_pending_order
ON whitelist_renewal_intents(status, entitlement_id, target_generation, confirmed_at_unix, access_order_id)

-- maestro:statement
CREATE TRIGGER whitelist_topup_orders_block_legacy_decision
BEFORE INSERT ON idempotency_requests
WHEN NEW.command_type IN ('confirm','cancel') AND EXISTS (
    SELECT 1 FROM whitelist_topup_orders
    WHERE order_id = NEW.resource_id
)
BEGIN
    SELECT RAISE(ABORT, 'white-list top-up requires the dedicated decision path');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_orders_payment_transition
BEFORE UPDATE OF payment_state ON orders
WHEN EXISTS (
    SELECT 1 FROM whitelist_topup_orders
    WHERE order_id = OLD.order_id
) AND NOT (
    NEW.payment_state = OLD.payment_state OR
    (OLD.payment_state = 'created' AND NEW.payment_state = 'payment_claimed' AND EXISTS (
        SELECT 1 FROM whitelist_topup_payment_claims
        WHERE order_id = OLD.order_id
    )) OR
    (OLD.payment_state IN ('created','payment_claimed') AND NEW.payment_state = 'confirmed' AND EXISTS (
        SELECT 1 FROM idempotency_requests
        WHERE command_type = 'whitelist_topup_confirm'
          AND resource_id = OLD.order_id
          AND operation_id = NEW.operation_id
          AND status = 'applying'
    )) OR
    (OLD.payment_state IN ('created','payment_claimed') AND NEW.payment_state = 'canceled' AND EXISTS (
        SELECT 1 FROM idempotency_requests
        WHERE command_type = 'whitelist_topup_reject'
          AND resource_id = OLD.order_id
          AND operation_id = NEW.operation_id
          AND status = 'applying'
    )) OR
    (OLD.payment_state = 'created' AND NEW.payment_state = 'expired')
)
BEGIN
    SELECT RAISE(ABORT, 'invalid white-list top-up payment transition');
END

-- maestro:statement
CREATE TRIGGER whitelist_topup_idempotency_applied_guard
BEFORE UPDATE OF status ON idempotency_requests
WHEN NEW.status = 'applied' AND (
    (NEW.command_type = 'whitelist_topup_create' AND NOT EXISTS (
        SELECT 1
        FROM whitelist_topup_orders AS topup
        JOIN orders AS source_order ON source_order.order_id = topup.order_id
        WHERE topup.order_id = NEW.resource_id
          AND topup.creation_request_hash = NEW.request_hash
          AND source_order.operation_id = NEW.operation_id
          AND json_extract(NEW.response_json,'$.order_id') = topup.order_id
    )) OR
    (NEW.command_type = 'whitelist_topup_claim' AND NOT EXISTS (
        SELECT 1
        FROM whitelist_topup_payment_claims AS claim
        JOIN orders AS source_order ON source_order.order_id = claim.order_id
        WHERE claim.order_id = NEW.resource_id
          AND claim.operation_id = NEW.operation_id
          AND claim.request_hash = NEW.request_hash
          AND source_order.payment_state = 'payment_claimed'
          AND json_extract(NEW.response_json,'$.order_id') = claim.order_id
    )) OR
    (NEW.command_type = 'whitelist_topup_confirm' AND NOT EXISTS (
        SELECT 1
        FROM whitelist_topup_results AS result
        JOIN whitelist_topup_orders AS topup ON topup.order_id = result.order_id
        JOIN whitelist_balance_projections AS projection
          ON projection.entitlement_id = topup.entitlement_id
        WHERE result.order_id = NEW.resource_id
          AND result.decision = 'CONFIRMED'
          AND result.operation_id = NEW.operation_id
          AND result.request_hash = NEW.request_hash
          AND projection.current_period_id = result.period_id
          AND projection.pending = 0
          AND json_extract(NEW.response_json,'$.order_id') = result.order_id
          AND json_extract(NEW.response_json,'$.balance_entry_id') = result.balance_entry_id
          AND json_extract(NEW.response_json,'$.control_id') = result.control_id
    )) OR
    (NEW.command_type = 'whitelist_topup_reject' AND NOT EXISTS (
        SELECT 1
        FROM whitelist_topup_results AS result
        WHERE result.order_id = NEW.resource_id
          AND result.decision = 'REJECTED'
          AND result.operation_id = NEW.operation_id
          AND result.request_hash = NEW.request_hash
          AND json_extract(NEW.response_json,'$.order_id') = result.order_id
    )) OR
    (NEW.command_type = 'whitelist_publication_set' AND NOT EXISTS (
        SELECT 1
        FROM whitelist_publication_controls AS control
        WHERE control.entitlement_id = NEW.resource_id
          AND control.operation_id = NEW.operation_id
          AND control.request_hash = NEW.request_hash
          AND json_extract(NEW.response_json,'$.control_id') = control.control_id
          AND json_extract(NEW.response_json,'$.version') = control.version
          AND json_extract(NEW.response_json,'$.enabled') = control.enabled
    ))
)
BEGIN
    SELECT RAISE(ABORT, 'applied white-list top-up result is incomplete');
END
