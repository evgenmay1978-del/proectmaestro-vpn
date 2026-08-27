-- MaestroVPN HA exactly-once order, payment, and expiry schema v7. Applied migrations v1-v6 remain immutable.

-- maestro:statement
DROP TRIGGER orders_immutable_terms

-- maestro:statement
DROP TRIGGER orders_payment_transition

-- maestro:statement
DROP TRIGGER orders_provisioning_transition

-- maestro:statement
DROP TRIGGER orders_decision_consistency

-- maestro:statement
DROP TRIGGER active_order_guards_live_order

-- maestro:statement
DROP TRIGGER terminal_order_releases_guards

-- maestro:statement
DROP TRIGGER payments_match_order

-- maestro:statement
ALTER TABLE active_order_guards RENAME TO active_order_guards_v1

-- maestro:statement
ALTER TABLE payments RENAME TO payments_v1

-- maestro:statement
ALTER TABLE orders RENAME TO orders_v1

-- maestro:statement
CREATE TABLE orders (
    order_id TEXT PRIMARY KEY,
    payment_code TEXT NOT NULL UNIQUE,
    buyer_scope TEXT NOT NULL,
    buyer_key_hmac TEXT NOT NULL CHECK(length(buyer_key_hmac) = 64),
    customer_id TEXT REFERENCES customers(customer_id) ON DELETE RESTRICT,
    tariff_version_id TEXT NOT NULL REFERENCES tariff_versions(tariff_version_id) ON DELETE RESTRICT,
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL CHECK(currency = 'RUB'),
    duration_days INTEGER NOT NULL CHECK(duration_days > 0),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    expires_at_unix INTEGER NOT NULL CHECK(expires_at_unix = created_at_unix + 86400),
    payment_state TEXT NOT NULL CHECK(payment_state IN ('created','payment_claimed','confirmed','canceled','expired')),
    provisioning_state TEXT NOT NULL CHECK(provisioning_state IN ('none','pending','ready','degraded','failed')),
    decision TEXT CHECK(decision IN ('confirmed','rejected','expired','cancelled')),
    confirmed_at_unix INTEGER,
    result_expires_at_unix INTEGER,
    result_generation INTEGER,
    operation_id TEXT NOT NULL UNIQUE,
    origin_bot_id TEXT NOT NULL DEFAULT '',
    origin_chat_key_hmac TEXT CHECK(origin_chat_key_hmac IS NULL OR length(origin_chat_key_hmac) = 64),
    CHECK((payment_state = 'confirmed' AND decision = 'confirmed' AND confirmed_at_unix IS NOT NULL) OR payment_state <> 'confirmed'),
    CHECK((payment_state = 'canceled' AND decision = 'cancelled') OR payment_state <> 'canceled'),
    CHECK((payment_state = 'expired' AND decision = 'expired') OR payment_state <> 'expired'),
    CHECK(result_generation IS NULL OR result_generation >= 0),
    CHECK(result_expires_at_unix IS NULL OR result_expires_at_unix >= created_at_unix)
)

-- maestro:statement
CREATE TABLE active_order_guards (
    buyer_scope TEXT NOT NULL,
    buyer_key_hmac TEXT NOT NULL CHECK(length(buyer_key_hmac) = 64),
    order_id TEXT NOT NULL REFERENCES orders(order_id) ON DELETE CASCADE,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    PRIMARY KEY(buyer_scope, buyer_key_hmac),
    UNIQUE(order_id, buyer_scope)
)

-- maestro:statement
CREATE TABLE payments (
    payment_id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL UNIQUE REFERENCES orders(order_id) ON DELETE RESTRICT,
    provider TEXT NOT NULL,
    provider_event_id TEXT,
    receipt_ref TEXT,
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL CHECK(currency = 'RUB'),
    confirmed_at_unix INTEGER NOT NULL CHECK(confirmed_at_unix >= 0),
    UNIQUE(provider, provider_event_id),
    UNIQUE(receipt_ref)
)

-- maestro:statement
INSERT INTO orders(
    order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
    amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
    provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,
    result_generation,operation_id,origin_bot_id,origin_chat_key_hmac
)
SELECT
    order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
    amount_minor,currency,duration_days,created_at_unix,expires_at_unix,
    CASE
        WHEN payment_state = 'pending' THEN 'created'
        WHEN payment_state = 'claimed' THEN 'payment_claimed'
        WHEN payment_state = 'confirmed' THEN 'confirmed'
        WHEN decision = 'expired' THEN 'expired'
        ELSE 'canceled'
    END,
    CASE
        WHEN payment_state <> 'confirmed' THEN 'none'
        WHEN provisioning_state = 'applied' THEN 'ready'
        WHEN provisioning_state = 'failed' THEN 'failed'
        ELSE 'pending'
    END,
    CASE
        WHEN payment_state = 'confirmed' THEN 'confirmed'
        WHEN payment_state = 'rejected' AND decision = 'expired' THEN 'expired'
        WHEN payment_state = 'rejected' THEN 'cancelled'
        ELSE NULL
    END,
    confirmed_at_unix,result_expires_at_unix,result_generation,operation_id,'',NULL
FROM orders_v1

-- maestro:statement
INSERT INTO payments(
    payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix
)
SELECT payment_id,order_id,provider,provider_event_id,receipt_ref,amount_minor,currency,confirmed_at_unix
FROM payments_v1

-- maestro:statement
INSERT INTO active_order_guards(buyer_scope,buyer_key_hmac,order_id,created_at_unix)
SELECT g.buyer_scope,g.buyer_key_hmac,g.order_id,g.created_at_unix
FROM active_order_guards_v1 g
JOIN orders o ON o.order_id = g.order_id
WHERE o.payment_state IN ('created','payment_claimed')

-- maestro:statement
DROP TABLE active_order_guards_v1

-- maestro:statement
DROP TABLE payments_v1

-- maestro:statement
DROP TABLE orders_v1

-- maestro:statement
ALTER TABLE operations ADD COLUMN lease_holder_id TEXT

-- maestro:statement
ALTER TABLE operations ADD COLUMN lease_fence INTEGER NOT NULL DEFAULT 0 CHECK(lease_fence >= 0)

-- maestro:statement
CREATE TRIGGER orders_immutable_terms
BEFORE UPDATE OF payment_code, tariff_version_id, amount_minor, currency, duration_days,
    created_at_unix, expires_at_unix, buyer_scope, buyer_key_hmac, customer_id,
    origin_bot_id, origin_chat_key_hmac ON orders
BEGIN
    SELECT RAISE(ABORT, 'order terms are immutable');
END

-- maestro:statement
CREATE TRIGGER orders_payment_transition
BEFORE UPDATE OF payment_state ON orders
WHEN NOT (
    NEW.payment_state = OLD.payment_state OR
    (OLD.payment_state = 'created' AND NEW.payment_state IN ('payment_claimed','confirmed','canceled','expired')) OR
    (OLD.payment_state = 'payment_claimed' AND NEW.payment_state IN ('confirmed','canceled'))
)
BEGIN
    SELECT RAISE(ABORT, 'invalid payment transition');
END

-- maestro:statement
CREATE TRIGGER orders_provisioning_transition
BEFORE UPDATE OF provisioning_state ON orders
WHEN NOT (
    NEW.provisioning_state = OLD.provisioning_state OR
    (OLD.provisioning_state = 'none' AND NEW.provisioning_state = 'pending') OR
    (OLD.provisioning_state = 'pending' AND NEW.provisioning_state IN ('ready','degraded','failed')) OR
    (OLD.provisioning_state IN ('degraded','failed') AND NEW.provisioning_state = 'pending')
)
BEGIN
    SELECT RAISE(ABORT, 'invalid provisioning transition');
END

-- maestro:statement
CREATE TRIGGER orders_decision_consistency
BEFORE UPDATE OF decision ON orders
WHEN NEW.decision IS NOT NULL AND (
    OLD.decision IS NOT NULL OR
    (NEW.decision = 'confirmed' AND NEW.payment_state <> 'confirmed') OR
    (NEW.decision = 'cancelled' AND NEW.payment_state <> 'canceled') OR
    (NEW.decision = 'expired' AND NEW.payment_state <> 'expired')
)
BEGIN
    SELECT RAISE(ABORT, 'invalid order decision');
END

-- maestro:statement
CREATE TRIGGER active_order_guards_live_order
BEFORE INSERT ON active_order_guards
WHEN NOT EXISTS (
    SELECT 1 FROM orders
    WHERE order_id = NEW.order_id
      AND buyer_scope = NEW.buyer_scope
      AND buyer_key_hmac = NEW.buyer_key_hmac
      AND (
          payment_state = 'payment_claimed' OR
          (payment_state = 'created' AND expires_at_unix > NEW.created_at_unix)
      )
)
BEGIN
    SELECT RAISE(ABORT, 'active order guard requires a live nonterminal order');
END

-- maestro:statement
CREATE TRIGGER terminal_order_releases_guards
AFTER UPDATE OF payment_state ON orders
WHEN OLD.payment_state IN ('created','payment_claimed') AND NEW.payment_state IN ('confirmed','canceled','expired')
BEGIN
    DELETE FROM active_order_guards WHERE order_id = NEW.order_id;
END

-- maestro:statement
CREATE TRIGGER payments_match_order
BEFORE INSERT ON payments
WHEN (
    NEW.receipt_ref IS NULL AND NOT EXISTS (
        SELECT 1 FROM orders
        WHERE order_id = NEW.order_id AND payment_state = 'confirmed'
          AND decision = 'confirmed' AND amount_minor = NEW.amount_minor AND currency = NEW.currency
    )
) OR (
    NEW.receipt_ref IS NOT NULL AND (
        NOT EXISTS (
            SELECT 1 FROM orders
            WHERE order_id = NEW.order_id AND payment_state = 'payment_claimed'
              AND decision IS NULL AND amount_minor = NEW.amount_minor AND currency = NEW.currency
        ) OR NOT EXISTS (
            SELECT 1 FROM idempotency_requests
            WHERE command_type = 'confirm' AND resource_id = NEW.order_id
              AND decision = 'payment_confirmed' AND status = 'applying'
        )
    )
)
BEGIN
    SELECT RAISE(ABORT, 'payment must follow the winning claimed-order decision');
END

-- maestro:statement
CREATE TRIGGER task7_idempotency_claim_guard
BEFORE INSERT ON idempotency_requests
WHEN (
    NEW.command_type = 'confirm' AND (
        NEW.decision <> 'payment_confirmed' OR NOT EXISTS (
            SELECT 1 FROM orders
            WHERE order_id = NEW.resource_id AND payment_state = 'payment_claimed' AND decision IS NULL
        )
    )
) OR (
    NEW.command_type = 'cancel' AND (
        NEW.decision <> 'order_canceled' OR NOT EXISTS (
            SELECT 1 FROM orders
            WHERE order_id = NEW.resource_id AND payment_state IN ('created','payment_claimed') AND decision IS NULL
        )
    )
)
BEGIN
    SELECT RAISE(ABORT, 'idempotency claim requires a live order decision');
END

-- maestro:statement
CREATE TRIGGER task7_idempotency_applied_guard
BEFORE UPDATE OF status ON idempotency_requests
WHEN NEW.status = 'applied' AND (
    NEW.response_json IS NULL OR
    (NEW.command_type = 'confirm' AND (
        NOT EXISTS (
            SELECT 1 FROM orders o
            JOIN customers c ON c.customer_id = o.customer_id
            WHERE o.order_id = NEW.resource_id AND o.payment_state = 'confirmed'
              AND o.decision = 'confirmed' AND o.operation_id = NEW.operation_id
              AND c.status = 'active'
              AND c.generation = CAST(json_extract(NEW.response_json,'$.generation') AS INTEGER)
              AND c.expires_at_unix = CAST(json_extract(NEW.response_json,'$.expires_at_unix') AS INTEGER)
        ) OR
        (SELECT COUNT(*) FROM payments WHERE order_id = NEW.resource_id) <> 1 OR
        EXISTS (
            SELECT 1 FROM node_services ns
            JOIN orders o ON o.order_id = NEW.resource_id
            WHERE ns.desired_target = 1 AND ns.retired = 0
              AND NOT EXISTS (
                  SELECT 1 FROM desired_node_state d
                  WHERE d.customer_id = o.customer_id AND d.node_id = ns.node_id
                    AND d.service_name = ns.service_name
                    AND d.generation = CAST(json_extract(NEW.response_json,'$.generation') AS INTEGER)
                    AND d.operation_id = NEW.operation_id AND d.tombstone = 0
              )
        ) OR EXISTS (
            SELECT 1 FROM node_services ns
            JOIN orders o ON o.order_id = NEW.resource_id
            WHERE ns.desired_target = 1 AND ns.retired = 0
              AND NOT EXISTS (
                  SELECT 1 FROM outbox_events e
                  WHERE e.operation_id = NEW.operation_id AND e.node_id = ns.node_id
                    AND e.service_name = ns.service_name AND e.event_kind = 'apply'
                    AND e.generation = CAST(json_extract(NEW.response_json,'$.generation') AS INTEGER)
                    AND e.aggregate_id = o.customer_id || ':' || ns.node_id || ':' || ns.service_name
              )
        )
    )) OR
    (NEW.command_type = 'cancel' AND (
        NOT EXISTS (
            SELECT 1 FROM orders
            WHERE order_id = NEW.resource_id AND payment_state = 'canceled'
              AND decision = 'cancelled' AND operation_id = NEW.operation_id
        ) OR EXISTS (SELECT 1 FROM payments WHERE order_id = NEW.resource_id)
          OR EXISTS (SELECT 1 FROM active_order_guards WHERE order_id = NEW.resource_id)
    ))
)
BEGIN
    SELECT RAISE(ABORT, 'applied Task 7 result is incomplete');
END

-- maestro:statement
CREATE TRIGGER task7_expiry_operation_fence
BEFORE INSERT ON operations
WHEN NEW.operation_type IN ('expiry-customers','expiry-orders') AND (
    NEW.lease_holder_id IS NULL OR NEW.lease_fence <= 0 OR
    NOT EXISTS (
        SELECT 1 FROM cluster_restore_state
        WHERE singleton_id = 1 AND activated = 1
    ) OR NOT EXISTS (
        SELECT 1 FROM cluster_job_leases
        WHERE job_name = 'expiry-sweeper' AND holder_id = NEW.lease_holder_id
          AND lease_fence = NEW.lease_fence AND expires_at_unix > unixepoch()
    )
)
BEGIN
    SELECT RAISE(ABORT, 'expiry sweeper lease lost');
END

-- maestro:statement
CREATE TRIGGER task7_expiry_operation_applied_guard
BEFORE UPDATE OF status ON operations
WHEN NEW.operation_type = 'expiry-customers' AND NEW.status = 'applied' AND (
    NOT EXISTS (
        SELECT 1 FROM operation_batches
        WHERE operation_id = NEW.operation_id AND status = 'applied'
    ) OR EXISTS (
        SELECT 1 FROM operation_batches b
        WHERE b.operation_id = NEW.operation_id AND (
            b.status <> 'applied' OR NOT EXISTS (
                SELECT 1 FROM customers c
                WHERE c.customer_id = b.batch_id AND c.status = 'expired'
                  AND c.generation = b.sequence_no
            ) OR EXISTS (
                SELECT 1 FROM node_services ns
                WHERE ns.desired_target = 1 AND ns.retired = 0
                  AND NOT EXISTS (
                      SELECT 1 FROM desired_node_state d
                      WHERE d.customer_id = b.batch_id AND d.node_id = ns.node_id
                        AND d.service_name = ns.service_name AND d.generation = b.sequence_no
                        AND d.operation_id = NEW.operation_id AND d.tombstone = 1
                  )
            ) OR EXISTS (
                SELECT 1 FROM node_services ns
                WHERE ns.desired_target = 1 AND ns.retired = 0
                  AND NOT EXISTS (
                      SELECT 1 FROM outbox_events e
                      WHERE e.operation_id = NEW.operation_id AND e.node_id = ns.node_id
                        AND e.service_name = ns.service_name AND e.event_kind = 'revoke'
                        AND e.generation = b.sequence_no
                        AND e.aggregate_id = b.batch_id || ':' || ns.node_id || ':' || ns.service_name
                  )
            )
        )
    )
)
BEGIN
    SELECT RAISE(ABORT, 'applied expiry result is incomplete');
END
