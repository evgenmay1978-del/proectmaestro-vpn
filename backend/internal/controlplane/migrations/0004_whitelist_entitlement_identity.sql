-- MaestroVPN HA immutable white-list entitlement identity v4.
-- Applied migrations v1-v3 remain byte-for-byte immutable.

-- maestro:statement
CREATE TABLE whitelist_entitlement_identities (
    entitlement_id TEXT PRIMARY KEY NOT NULL CHECK(
        length(entitlement_id) = 39 AND
        substr(entitlement_id, 1, 7) = 'wl-ent-' AND
        substr(entitlement_id, 8) = lower(substr(entitlement_id, 8)) AND
        substr(entitlement_id, 8) NOT GLOB '*[^0-9a-f]*'
    ),
    customer_id TEXT NOT NULL UNIQUE
        REFERENCES customers(customer_id) ON DELETE CASCADE,
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0)
);

-- maestro:statement
CREATE TRIGGER whitelist_entitlement_identity_no_update
BEFORE UPDATE ON whitelist_entitlement_identities
BEGIN
    SELECT RAISE(ABORT, 'whitelist entitlement identity is immutable');
END;

-- maestro:statement
CREATE TRIGGER whitelist_entitlement_identity_no_delete
BEFORE DELETE ON whitelist_entitlement_identities
WHEN EXISTS (
    SELECT 1 FROM customers
    WHERE customer_id = OLD.customer_id
)
BEGIN
    SELECT RAISE(ABORT, 'whitelist entitlement identity is immutable');
END;
