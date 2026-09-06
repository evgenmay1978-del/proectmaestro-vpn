-- Raw legacy history is distinct from orders that authorize a new grant.
-- The source envelope is immutable; only a parent-bound import may advance
-- an unaccepted alias. An accepted alias can never point at another order.

-- maestro:statement
CREATE TABLE imported_legacy_order_aliases (
    order_key_hmac TEXT PRIMARY KEY NOT NULL CHECK(length(order_key_hmac)=64 AND order_key_hmac NOT GLOB '*[^0-9a-f]*'),
    record_secret_id TEXT NOT NULL REFERENCES imported_secrets(secret_id) ON DELETE RESTRICT,
    record_sha256 TEXT NOT NULL CHECK(length(record_sha256)=64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    source_sha256 TEXT NOT NULL CHECK(length(source_sha256)=64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'),
    source_revision INTEGER NOT NULL CHECK(typeof(source_revision)='integer' AND source_revision>0),
    customer_id TEXT REFERENCES customers(customer_id) ON DELETE RESTRICT,
    historical_grant INTEGER NOT NULL CHECK(historical_grant IN (0,1)),
    accepted_order_id TEXT UNIQUE REFERENCES orders(order_id) ON DELETE RESTRICT,
    accepted_at_unix INTEGER,
    cancelled_at_unix INTEGER CHECK(cancelled_at_unix IS NULL OR (typeof(cancelled_at_unix)='integer' AND cancelled_at_unix>=0 AND historical_grant=0)),
    imported_at_unix INTEGER NOT NULL CHECK(typeof(imported_at_unix)='integer' AND imported_at_unix>=0),
    CHECK((accepted_order_id IS NULL AND accepted_at_unix IS NULL) OR
          (accepted_order_id IS NOT NULL AND typeof(accepted_at_unix)='integer' AND accepted_at_unix>=0 AND historical_grant=0 AND customer_id IS NOT NULL))
)

-- maestro:statement
CREATE TRIGGER imported_legacy_order_aliases_source_binding_insert
BEFORE INSERT ON imported_legacy_order_aliases
WHEN NOT EXISTS(SELECT 1 FROM imported_secrets i JOIN imported_entity_state e
 ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id
 WHERE i.secret_id=NEW.record_secret_id AND i.owner_type='legacy_order'
 AND i.owner_source_key=NEW.order_key_hmac AND i.field='source_record' AND i.kind='legacy-order-record-v1'
 AND i.secret_sha256=NEW.record_sha256 AND e.target_id=i.secret_id AND e.lifecycle='active')
BEGIN
 SELECT RAISE(ABORT,'legacy order record binding missing');
END

-- maestro:statement
CREATE TRIGGER imported_legacy_order_aliases_transition
BEFORE UPDATE ON imported_legacy_order_aliases
WHEN NEW.order_key_hmac<>OLD.order_key_hmac OR NEW.imported_at_unix<>OLD.imported_at_unix
 OR NEW.customer_id IS NOT OLD.customer_id
 OR (OLD.cancelled_at_unix IS NOT NULL AND (NEW.cancelled_at_unix IS NOT OLD.cancelled_at_unix OR NEW.record_secret_id<>OLD.record_secret_id OR NEW.record_sha256<>OLD.record_sha256 OR NEW.source_sha256<>OLD.source_sha256 OR NEW.source_revision<>OLD.source_revision OR NEW.historical_grant<>OLD.historical_grant OR NEW.accepted_order_id IS NOT OLD.accepted_order_id))
 OR (OLD.historical_grant=1 AND NEW.historical_grant<>1)
 OR (OLD.accepted_order_id IS NOT NULL AND (NEW.accepted_order_id IS NOT OLD.accepted_order_id OR NEW.accepted_at_unix IS NOT OLD.accepted_at_unix OR NEW.record_secret_id<>OLD.record_secret_id OR NEW.record_sha256<>OLD.record_sha256 OR NEW.source_sha256<>OLD.source_sha256 OR NEW.source_revision<>OLD.source_revision OR NEW.historical_grant<>OLD.historical_grant))
 OR (NEW.record_secret_id<>OLD.record_secret_id AND (OLD.accepted_order_id IS NOT NULL OR NEW.source_revision<=OLD.source_revision))
 OR (NEW.accepted_order_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM orders o WHERE o.order_id=NEW.accepted_order_id AND o.customer_id=NEW.customer_id AND o.buyer_scope='legacy-import' AND o.buyer_key_hmac=NEW.order_key_hmac AND o.created_at_unix=NEW.accepted_at_unix))
 OR NOT EXISTS(SELECT 1 FROM imported_secrets i JOIN imported_entity_state e ON e.entity_kind='encrypted_secret' AND e.source_key=i.secret_id WHERE i.secret_id=NEW.record_secret_id AND i.owner_type='legacy_order' AND i.owner_source_key=NEW.order_key_hmac AND i.field='source_record' AND i.kind='legacy-order-record-v1' AND i.secret_sha256=NEW.record_sha256 AND e.target_id=i.secret_id AND e.lifecycle='active')
BEGIN
 SELECT RAISE(ABORT,'legacy order alias transition rejected');
END

-- maestro:statement
CREATE TRIGGER imported_legacy_order_aliases_no_delete
BEFORE DELETE ON imported_legacy_order_aliases
BEGIN
 SELECT RAISE(ABORT,'legacy order history is retained');
END
