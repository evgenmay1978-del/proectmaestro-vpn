-- MaestroVPN HA schema v1. Every statement is split only at the marker below.

-- maestro:statement
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY CHECK(version > 0),
    checksum TEXT NOT NULL CHECK(length(checksum) = 64),
    applied_at_unix INTEGER NOT NULL CHECK(applied_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE customers (
    customer_id TEXT PRIMARY KEY,
    display_login TEXT NOT NULL,
    login_key_hmac TEXT NOT NULL UNIQUE CHECK(length(login_key_hmac) = 64),
    status TEXT NOT NULL CHECK(status IN ('active','suspended','expired','deleted')),
    expires_at_unix INTEGER NOT NULL CHECK(expires_at_unix >= 0),
    generation INTEGER NOT NULL CHECK(generation >= 0),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix)
)

-- maestro:statement
CREATE TABLE credentials (
    credential_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    protocol TEXT NOT NULL,
    secret_envelope BLOB NOT NULL,
    secret_sha256 TEXT NOT NULL CHECK(length(secret_sha256) = 64),
    generation INTEGER NOT NULL CHECK(generation >= 0),
    enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix),
    UNIQUE(customer_id, protocol, generation)
)

-- maestro:statement
CREATE TABLE subscription_tokens (
    token_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    token_hmac TEXT NOT NULL UNIQUE CHECK(length(token_hmac) = 64),
    token_envelope BLOB NOT NULL,
    token_sha256 TEXT NOT NULL CHECK(length(token_sha256) = 64),
    generation INTEGER NOT NULL CHECK(generation >= 0),
    revoked INTEGER NOT NULL CHECK(revoked IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    revoked_at_unix INTEGER,
    CHECK(revoked_at_unix IS NULL OR revoked_at_unix >= created_at_unix)
)

-- maestro:statement
CREATE TABLE devices (
    device_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    device_key_hmac TEXT NOT NULL CHECK(length(device_key_hmac) = 64),
    platform TEXT NOT NULL,
    last_seen_at_unix INTEGER,
    revoked INTEGER NOT NULL CHECK(revoked IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(customer_id, device_key_hmac)
)

-- maestro:statement
CREATE TABLE tariff_versions (
    tariff_version_id TEXT PRIMARY KEY,
    tariff_code TEXT NOT NULL,
    duration_days INTEGER NOT NULL CHECK(duration_days > 0),
    amount_minor INTEGER NOT NULL CHECK(amount_minor > 0),
    currency TEXT NOT NULL CHECK(currency = 'RUB'),
    active INTEGER NOT NULL CHECK(active IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(tariff_code, created_at_unix)
)

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
    payment_state TEXT NOT NULL CHECK(payment_state IN ('pending','claimed','confirmed','rejected')),
    provisioning_state TEXT NOT NULL CHECK(provisioning_state IN ('pending','applying','applied','failed')),
    decision TEXT CHECK(decision IN ('confirmed','rejected','expired','cancelled')),
    confirmed_at_unix INTEGER,
    result_expires_at_unix INTEGER,
    result_generation INTEGER,
    operation_id TEXT NOT NULL UNIQUE,
    CHECK((decision = 'confirmed' AND confirmed_at_unix IS NOT NULL) OR decision IS NULL OR decision <> 'confirmed'),
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
CREATE TABLE trial_redemptions (
    redemption_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE RESTRICT,
    trial_code_hmac TEXT NOT NULL UNIQUE CHECK(length(trial_code_hmac) = 64),
    device_key_hmac TEXT NOT NULL CHECK(length(device_key_hmac) = 64),
    redeemed_at_unix INTEGER NOT NULL CHECK(redeemed_at_unix >= 0),
    duration_days INTEGER NOT NULL CHECK(duration_days > 0)
)

-- maestro:statement
CREATE TABLE idempotency_requests (
    scope TEXT NOT NULL,
    command_type TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL CHECK(length(request_hash) = 64),
    resource_id TEXT NOT NULL,
    decision TEXT NOT NULL,
    operation_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK(status IN ('applying','applied')),
    response_json TEXT,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    applied_at_unix INTEGER,
    PRIMARY KEY(scope, command_type, idempotency_key),
    CHECK((status = 'applied' AND response_json IS NOT NULL AND applied_at_unix IS NOT NULL) OR status = 'applying')
)

-- maestro:statement
CREATE TABLE nodes (
    node_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    is_voter INTEGER NOT NULL CHECK(is_voter IN (0,1)),
    enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE node_services (
    node_id TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    desired_target INTEGER NOT NULL CHECK(desired_target IN (0,1)),
    apply_enabled INTEGER NOT NULL CHECK(apply_enabled IN (0,1)),
    fenced INTEGER NOT NULL CHECK(fenced IN (0,1)),
    retired INTEGER NOT NULL CHECK(retired IN (0,1)),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0),
    PRIMARY KEY(node_id, service_name)
)

-- maestro:statement
CREATE TABLE desired_node_state (
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    desired_envelope BLOB NOT NULL,
    desired_sha256 TEXT NOT NULL CHECK(length(desired_sha256) = 64),
    status TEXT NOT NULL CHECK(status IN ('pending','applying','applied','failed')),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0),
    PRIMARY KEY(customer_id, node_id, service_name),
    FOREIGN KEY(node_id, service_name) REFERENCES node_services(node_id, service_name) ON DELETE CASCADE
)

-- maestro:statement
CREATE TABLE outbox_events (
    event_id TEXT PRIMARY KEY,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    event_type TEXT NOT NULL,
    payload_envelope BLOB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256) = 64),
    status TEXT NOT NULL CHECK(status IN ('pending','processing','applied','failed')),
    available_at_unix INTEGER NOT NULL CHECK(available_at_unix >= 0),
    attempts INTEGER NOT NULL CHECK(attempts >= 0),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(aggregate_type, aggregate_id, generation, event_type)
)

-- maestro:statement
CREATE TABLE node_leases (
    node_id TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    holder_id TEXT NOT NULL,
    lease_token TEXT NOT NULL UNIQUE,
    acquired_at_unix INTEGER NOT NULL CHECK(acquired_at_unix >= 0),
    expires_at_unix INTEGER NOT NULL CHECK(expires_at_unix > acquired_at_unix),
    PRIMARY KEY(node_id, service_name),
    FOREIGN KEY(node_id, service_name) REFERENCES node_services(node_id, service_name) ON DELETE CASCADE
)

-- maestro:statement
CREATE TABLE cluster_job_leases (
    job_name TEXT PRIMARY KEY,
    holder_id TEXT NOT NULL,
    lease_token TEXT NOT NULL UNIQUE,
    acquired_at_unix INTEGER NOT NULL CHECK(acquired_at_unix >= 0),
    expires_at_unix INTEGER NOT NULL CHECK(expires_at_unix > acquired_at_unix)
)

-- maestro:statement
CREATE TABLE node_apply_receipts (
    receipt_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    desired_sha256 TEXT NOT NULL CHECK(length(desired_sha256) = 64),
    status TEXT NOT NULL CHECK(status IN ('pending','applying','applied','failed')),
    observed_sha256 TEXT CHECK(observed_sha256 IS NULL OR length(observed_sha256) = 64),
    error_code TEXT,
    applied_at_unix INTEGER,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(customer_id, node_id, service_name, generation),
    FOREIGN KEY(node_id, service_name) REFERENCES node_services(node_id, service_name) ON DELETE CASCADE
)

-- maestro:statement
CREATE TABLE tombstones (
    tombstone_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    reason TEXT NOT NULL,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(customer_id, generation)
)

-- maestro:statement
CREATE TABLE tombstone_targets (
    tombstone_id TEXT NOT NULL REFERENCES tombstones(tombstone_id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','applying','applied','failed')),
    applied_at_unix INTEGER,
    PRIMARY KEY(tombstone_id, node_id, service_name),
    FOREIGN KEY(node_id, service_name) REFERENCES node_services(node_id, service_name) ON DELETE CASCADE
)

-- maestro:statement
CREATE TABLE telegram_bot_routes (
    bot_identity_hmac TEXT PRIMARY KEY CHECK(length(bot_identity_hmac) = 64),
    token_fingerprint_hmac TEXT NOT NULL UNIQUE CHECK(length(token_fingerprint_hmac) = 64),
    credential_version INTEGER NOT NULL CHECK(credential_version > 0),
    schema_fingerprint TEXT NOT NULL CHECK(length(schema_fingerprint) > 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE telegram_pollers (
    bot_identity_hmac TEXT PRIMARY KEY NOT NULL
        CHECK(length(bot_identity_hmac) = 64)
        REFERENCES telegram_bot_routes(bot_identity_hmac) ON DELETE RESTRICT,
    node_id TEXT REFERENCES nodes(node_id) ON DELETE RESTRICT,
    lease_token TEXT UNIQUE,
    offset_value INTEGER NOT NULL CHECK(offset_value >= 0),
    lease_fence INTEGER NOT NULL CHECK(lease_fence >= 0),
    lease_expires_at_unix INTEGER NOT NULL CHECK(lease_expires_at_unix >= 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0),
    CHECK(
        (node_id IS NULL AND lease_token IS NULL AND lease_expires_at_unix = 0) OR
        (node_id IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at_unix > 0)
    )
)

-- maestro:statement
CREATE TABLE telegram_imported_callbacks (
    callback_hmac TEXT PRIMARY KEY NOT NULL CHECK(length(callback_hmac) = 64),
    bot_identity_hmac TEXT NOT NULL CHECK(length(bot_identity_hmac) = 64)
        REFERENCES telegram_bot_routes(bot_identity_hmac) ON DELETE RESTRICT,
    order_id TEXT NOT NULL CHECK(length(order_id) > 0),
    action TEXT NOT NULL CHECK(length(action) > 0),
    state TEXT NOT NULL CHECK(state IN ('pending','in_flight')),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE telegram_inbox (
    bot_id TEXT NOT NULL,
    update_id INTEGER NOT NULL,
    update_hmac TEXT NOT NULL CHECK(length(update_hmac) = 64),
    payload_envelope BLOB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256) = 64),
    status TEXT NOT NULL CHECK(status IN ('pending','processing','applied','rejected')),
    received_at_unix INTEGER NOT NULL CHECK(received_at_unix >= 0),
    processed_at_unix INTEGER,
    PRIMARY KEY(bot_id, update_id),
    UNIQUE(bot_id, update_hmac)
)

-- maestro:statement
CREATE TABLE telegram_callbacks (
    callback_hmac TEXT PRIMARY KEY CHECK(length(callback_hmac) = 64),
    bot_id TEXT NOT NULL,
    update_id INTEGER NOT NULL,
    decision TEXT,
    status TEXT NOT NULL CHECK(status IN ('pending','processing','applied','rejected')),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    processed_at_unix INTEGER,
    FOREIGN KEY(bot_id, update_id) REFERENCES telegram_inbox(bot_id, update_id) ON DELETE CASCADE
)

-- maestro:statement
CREATE TABLE telegram_delivery_outbox (
    delivery_id TEXT PRIMARY KEY,
    bot_id TEXT NOT NULL,
    chat_key_hmac TEXT NOT NULL CHECK(length(chat_key_hmac) = 64),
    payload_envelope BLOB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256) = 64),
    dedupe_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','processing','applied','failed')),
    attempts INTEGER NOT NULL CHECK(attempts >= 0),
    available_at_unix INTEGER NOT NULL CHECK(available_at_unix >= 0),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(bot_id, dedupe_key)
)

-- maestro:statement
CREATE TABLE telegram_bindings (
    binding_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
    bot_id TEXT NOT NULL,
    telegram_user_hmac TEXT NOT NULL CHECK(length(telegram_user_hmac) = 64),
    chat_key_hmac TEXT NOT NULL CHECK(length(chat_key_hmac) = 64),
    active INTEGER NOT NULL CHECK(active IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    UNIQUE(bot_id, telegram_user_hmac)
)

-- maestro:statement
CREATE TABLE external_actions (
    action_id TEXT PRIMARY KEY,
    action_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_envelope BLOB NOT NULL,
    request_sha256 TEXT NOT NULL CHECK(length(request_sha256) = 64),
    status TEXT NOT NULL CHECK(status IN ('pending','applying','applied','unknown','failed')),
    attempts INTEGER NOT NULL CHECK(attempts >= 0),
    response_envelope BLOB,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix),
    UNIQUE(action_type, idempotency_key)
)

-- maestro:statement
CREATE TABLE operations (
    operation_id TEXT PRIMARY KEY,
    operation_type TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','applying','applied','failed')),
    requested_by_hmac TEXT CHECK(requested_by_hmac IS NULL OR length(requested_by_hmac) = 64),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix)
)

-- maestro:statement
CREATE TABLE operation_batches (
    batch_id TEXT NOT NULL,
    operation_id TEXT NOT NULL REFERENCES operations(operation_id) ON DELETE CASCADE,
    sequence_no INTEGER NOT NULL CHECK(sequence_no >= 0),
    status TEXT NOT NULL CHECK(status IN ('pending','applying','applied','failed')),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    PRIMARY KEY(batch_id, operation_id),
    UNIQUE(batch_id, sequence_no)
)

-- maestro:statement
CREATE TABLE cluster_settings (
    setting_key TEXT PRIMARY KEY,
    public_value_json TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE setting_members (
    setting_key TEXT NOT NULL REFERENCES cluster_settings(setting_key) ON DELETE CASCADE,
    member_key TEXT NOT NULL,
    member_value_json TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    PRIMARY KEY(setting_key, member_key)
)

-- maestro:statement
CREATE TABLE setting_secrets (
    setting_key TEXT PRIMARY KEY REFERENCES cluster_settings(setting_key) ON DELETE CASCADE,
    secret_envelope BLOB NOT NULL,
    secret_sha256 TEXT NOT NULL CHECK(length(secret_sha256) = 64),
    key_version INTEGER NOT NULL CHECK(key_version > 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE principals (
    principal_id TEXT PRIMARY KEY,
    login_key_hmac TEXT NOT NULL UNIQUE CHECK(length(login_key_hmac) = 64),
    status TEXT NOT NULL CHECK(status IN ('active','disabled')),
    revocation_epoch INTEGER NOT NULL DEFAULT 0 CHECK(revocation_epoch >= 0),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE principal_roles (
    principal_id TEXT NOT NULL REFERENCES principals(principal_id) ON DELETE CASCADE,
    role_name TEXT NOT NULL,
    granted_at_unix INTEGER NOT NULL CHECK(granted_at_unix >= 0),
    PRIMARY KEY(principal_id, role_name)
)

-- maestro:statement
CREATE TABLE principal_credentials (
    credential_id TEXT PRIMARY KEY,
    principal_id TEXT NOT NULL REFERENCES principals(principal_id) ON DELETE CASCADE,
    credential_type TEXT NOT NULL CHECK(credential_type IN ('password','webauthn','recovery')),
    verifier_envelope BLOB NOT NULL,
    verifier_sha256 TEXT NOT NULL CHECK(length(verifier_sha256) = 64),
    active INTEGER NOT NULL CHECK(active IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE web_sessions (
    session_hmac TEXT PRIMARY KEY CHECK(length(session_hmac) = 64),
    csrf_hmac TEXT NOT NULL CHECK(length(csrf_hmac) = 64),
    principal_id TEXT NOT NULL REFERENCES principals(principal_id) ON DELETE CASCADE,
    revocation_epoch INTEGER NOT NULL CHECK(revocation_epoch >= 0),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    expires_at_unix INTEGER NOT NULL CHECK(expires_at_unix > created_at_unix),
    revoked_at_unix INTEGER
)

-- maestro:statement
CREATE TABLE rate_limit_buckets (
    bucket_scope TEXT NOT NULL,
    bucket_key_hmac TEXT NOT NULL CHECK(length(bucket_key_hmac) = 64),
    window_started_at_unix INTEGER NOT NULL CHECK(window_started_at_unix >= 0),
    count_value INTEGER NOT NULL CHECK(count_value >= 0),
    blocked_until_unix INTEGER,
    PRIMARY KEY(bucket_scope, bucket_key_hmac)
)

-- maestro:statement
CREATE TABLE import_runs (
    import_run_id TEXT PRIMARY KEY,
    snapshot_kind TEXT NOT NULL CHECK(snapshot_kind IN ('full','delta')),
    source_sha256 TEXT NOT NULL CHECK(length(source_sha256) = 64),
    plan_sha256 TEXT NOT NULL CHECK(length(plan_sha256) = 64),
    parent_source_sha256 TEXT CHECK(parent_source_sha256 IS NULL OR length(parent_source_sha256) = 64),
    target_sha256 TEXT CHECK(target_sha256 IS NULL OR length(target_sha256) = 64),
    batch_count INTEGER NOT NULL CHECK(batch_count >= 0),
    status TEXT NOT NULL CHECK(status IN ('applying','applied','failed')),
    started_at_unix INTEGER NOT NULL CHECK(started_at_unix >= 0),
    completed_at_unix INTEGER,
    UNIQUE(source_sha256, plan_sha256),
    CHECK((snapshot_kind = 'full' AND parent_source_sha256 IS NULL) OR
          (snapshot_kind = 'delta' AND parent_source_sha256 IS NOT NULL)),
    CHECK((status = 'applied' AND target_sha256 IS NOT NULL AND completed_at_unix IS NOT NULL) OR
          (status <> 'applied' AND completed_at_unix IS NULL)),
    CHECK(completed_at_unix IS NULL OR completed_at_unix >= started_at_unix)
)

-- maestro:statement
CREATE TABLE import_batches (
    import_run_id TEXT NOT NULL REFERENCES import_runs(import_run_id) ON DELETE CASCADE,
    batch_index INTEGER NOT NULL CHECK(batch_index >= 0),
    batch_digest TEXT NOT NULL CHECK(length(batch_digest) = 64),
    row_count INTEGER NOT NULL CHECK(row_count >= 0),
    status TEXT NOT NULL CHECK(status IN ('applying','applied','failed')),
    applied_at_unix INTEGER,
    PRIMARY KEY(import_run_id, batch_index),
    CHECK((status = 'applied' AND applied_at_unix IS NOT NULL) OR
          (status <> 'applied' AND applied_at_unix IS NULL))
)

-- maestro:statement
CREATE TABLE backup_watermarks (
    backup_id TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL REFERENCES schema_migrations(version) ON DELETE RESTRICT,
    backup_sha256 TEXT NOT NULL UNIQUE CHECK(length(backup_sha256) = 64),
    destination TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('pending','applied','verified','failed')),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    verified_at_unix INTEGER
)

-- maestro:statement
CREATE TABLE audit_events (
    event_id TEXT PRIMARY KEY,
    actor_hmac TEXT NOT NULL CHECK(length(actor_hmac) = 64),
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id_hmac TEXT NOT NULL CHECK(length(resource_id_hmac) = 64),
    details_envelope BLOB,
    details_sha256 TEXT CHECK(details_sha256 IS NULL OR length(details_sha256) = 64),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0)
)

-- maestro:statement
CREATE TABLE health_write_canary (
    node_id TEXT PRIMARY KEY REFERENCES nodes(node_id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK(generation >= 0),
    nonce_hmac TEXT NOT NULL CHECK(length(nonce_hmac) = 64),
    written_at_unix INTEGER NOT NULL CHECK(written_at_unix >= 0),
    observed_at_unix INTEGER NOT NULL CHECK(observed_at_unix >= written_at_unix)
)

-- maestro:statement
CREATE TRIGGER telegram_bot_routes_monotonic_version
BEFORE UPDATE ON telegram_bot_routes
WHEN NEW.credential_version < OLD.credential_version OR (
    NEW.credential_version = OLD.credential_version AND (
        NEW.token_fingerprint_hmac <> OLD.token_fingerprint_hmac OR
        NEW.schema_fingerprint <> OLD.schema_fingerprint
    )
)
BEGIN
    SELECT RAISE(ABORT, 'telegram bot credential route conflict');
END

-- maestro:statement
CREATE TRIGGER telegram_pollers_monotonic_state
BEFORE UPDATE OF offset_value, lease_fence ON telegram_pollers
WHEN NEW.offset_value < OLD.offset_value OR NEW.lease_fence < OLD.lease_fence
BEGIN
    SELECT RAISE(ABORT, 'telegram poll state rollback');
END

-- maestro:statement
CREATE TRIGGER telegram_imported_callbacks_transition
BEFORE UPDATE ON telegram_imported_callbacks
WHEN NEW.callback_hmac <> OLD.callback_hmac OR
     NEW.bot_identity_hmac <> OLD.bot_identity_hmac OR
     NEW.order_id <> OLD.order_id OR
     NEW.action <> OLD.action OR
     NOT (
         NEW.state = OLD.state OR
         (OLD.state = 'pending' AND NEW.state = 'in_flight')
     )
BEGIN
    SELECT RAISE(ABORT, 'invalid imported callback transition');
END

-- maestro:statement
CREATE TRIGGER tariff_versions_no_update
BEFORE UPDATE ON tariff_versions
BEGIN
    SELECT RAISE(ABORT, 'tariff versions are immutable');
END

-- maestro:statement
CREATE TRIGGER tariff_versions_no_delete
BEFORE DELETE ON tariff_versions
BEGIN
    SELECT RAISE(ABORT, 'tariff versions are immutable');
END

-- maestro:statement
CREATE TRIGGER audit_events_no_update
BEFORE UPDATE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are append only');
END

-- maestro:statement
CREATE TRIGGER audit_events_no_delete
BEFORE DELETE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are append only');
END

-- maestro:statement
CREATE TRIGGER orders_immutable_terms
BEFORE UPDATE OF payment_code, tariff_version_id, amount_minor, currency, duration_days, created_at_unix, expires_at_unix,
    buyer_scope, buyer_key_hmac ON orders
BEGIN
    SELECT RAISE(ABORT, 'order terms are immutable');
END

-- maestro:statement
CREATE TRIGGER orders_payment_transition
BEFORE UPDATE OF payment_state ON orders
WHEN NOT (
    NEW.payment_state = OLD.payment_state OR
    (OLD.payment_state = 'pending' AND NEW.payment_state IN ('claimed','confirmed','rejected')) OR
    (OLD.payment_state = 'claimed' AND NEW.payment_state IN ('confirmed','rejected'))
)
BEGIN
    SELECT RAISE(ABORT, 'invalid payment transition');
END

-- maestro:statement
CREATE TRIGGER orders_provisioning_transition
BEFORE UPDATE OF provisioning_state ON orders
WHEN NOT (
    NEW.provisioning_state = OLD.provisioning_state OR
    (OLD.provisioning_state = 'pending' AND NEW.provisioning_state IN ('applying','failed')) OR
    (OLD.provisioning_state = 'applying' AND NEW.provisioning_state IN ('applied','failed')) OR
    (OLD.provisioning_state = 'failed' AND NEW.provisioning_state = 'applying')
)
BEGIN
    SELECT RAISE(ABORT, 'invalid provisioning transition');
END

-- maestro:statement
CREATE TRIGGER orders_decision_consistency
BEFORE UPDATE OF decision ON orders
WHEN NEW.decision IS NOT NULL AND (
    OLD.decision IS NOT NULL OR
    (NEW.decision = 'confirmed' AND NEW.payment_state <> 'confirmed')
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
      AND decision IS NULL
      AND expires_at_unix > NEW.created_at_unix
)
BEGIN
    SELECT RAISE(ABORT, 'active order guard requires a live undecided order');
END

-- maestro:statement
CREATE TRIGGER terminal_order_releases_guards
AFTER UPDATE OF decision ON orders
WHEN OLD.decision IS NULL AND NEW.decision IS NOT NULL
BEGIN
    DELETE FROM active_order_guards WHERE order_id = NEW.order_id;
END

-- maestro:statement
CREATE TRIGGER payments_match_order
BEFORE INSERT ON payments
WHEN NOT EXISTS (
    SELECT 1 FROM orders
    WHERE order_id = NEW.order_id
      AND decision = 'confirmed'
      AND payment_state = 'confirmed'
      AND amount_minor = NEW.amount_minor
      AND currency = NEW.currency
)
BEGIN
    SELECT RAISE(ABORT, 'payment must match a confirmed order');
END

-- maestro:statement
CREATE TRIGGER idempotency_applied_resource
BEFORE INSERT ON idempotency_requests
WHEN NEW.status = 'applied' AND (
    NEW.response_json IS NULL OR
    (NEW.decision = 'payment_confirmed' AND NOT EXISTS (
        SELECT 1 FROM payments WHERE payment_id = NEW.resource_id
    )) OR
    (NEW.decision = 'customer_desired' AND NOT EXISTS (
        SELECT 1 FROM customers WHERE customer_id = NEW.resource_id
    )) OR
    (NEW.decision = 'outbox_applied' AND NOT EXISTS (
        SELECT 1 FROM outbox_events WHERE event_id = NEW.resource_id
    ))
)
BEGIN
    SELECT RAISE(ABORT, 'applied idempotency result is not durable');
END

-- maestro:statement
INSERT INTO tariff_versions(
    tariff_version_id, tariff_code, duration_days, amount_minor, currency, active, created_at_unix
) VALUES
    ('tariff_1m_v1','1m',30,40000,'RUB',1,0),
    ('tariff_2m_v1','2m',60,80000,'RUB',1,0),
    ('tariff_3m_v1','3m',90,120000,'RUB',1,0),
    ('tariff_6m_v1','6m',180,240000,'RUB',1,0),
    ('tariff_12m_v1','12m',365,480000,'RUB',1,0)

-- maestro:statement
INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES
    ('S1','S1',0,1,0),
    ('S2','S2',1,1,0),
    ('S3','S3',1,1,0),
    ('S4','S4',1,1,0)

-- maestro:statement
INSERT INTO node_services(
    node_id,service_name,desired_target,apply_enabled,fenced,retired,updated_at_unix
) VALUES
    ('S1','maestro-core',1,0,1,0,0),
    ('S2','maestro-core',1,1,0,0,0),
    ('S3','maestro-core',1,1,0,0,0),
    ('S4','maestro-core',1,1,0,0,0)
