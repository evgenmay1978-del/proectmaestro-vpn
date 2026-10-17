-- Legacy ledgers retain only keyed hashes, not the original trial identities.
-- Every row is permanent evidence of use; no current HMAC or expiry is invented.

-- maestro:statement
CREATE TABLE imported_legacy_trial_uses (
    source_key TEXT PRIMARY KEY NOT NULL CHECK(length(source_key) > 0),
    hash_kind TEXT NOT NULL CHECK(hash_kind IN ('anchor','drm')),
    legacy_hmac TEXT NOT NULL CHECK(length(legacy_hmac) = 64 AND legacy_hmac NOT GLOB '*[^0-9a-f]*'),
    lookup_secret_id TEXT NOT NULL REFERENCES imported_secrets(secret_id) ON DELETE RESTRICT,
    imported_at_unix INTEGER NOT NULL CHECK(typeof(imported_at_unix) = 'integer' AND imported_at_unix >= 0),
    UNIQUE(lookup_secret_id,hash_kind,legacy_hmac)
)

-- maestro:statement
CREATE TRIGGER imported_legacy_trial_uses_exact_insert
BEFORE INSERT ON imported_legacy_trial_uses
WHEN NOT EXISTS (
    SELECT 1 FROM imported_secrets
    WHERE secret_id=NEW.lookup_secret_id AND owner_type='trial_lookup'
      AND owner_source_key='legacy' AND field='salt' AND kind='hmac-key'
) OR EXISTS (
    SELECT 1 FROM imported_trial_identities WHERE source_key=NEW.source_key
) OR EXISTS (
    SELECT 1 FROM imported_legacy_trial_uses AS prior
    WHERE (prior.source_key=NEW.source_key AND (
        prior.hash_kind<>NEW.hash_kind OR prior.legacy_hmac<>NEW.legacy_hmac OR
        prior.lookup_secret_id<>NEW.lookup_secret_id OR prior.imported_at_unix<>NEW.imported_at_unix
    )) OR (prior.lookup_secret_id=NEW.lookup_secret_id AND prior.hash_kind=NEW.hash_kind
        AND prior.legacy_hmac=NEW.legacy_hmac AND prior.source_key<>NEW.source_key)
)
BEGIN
    SELECT RAISE(ABORT, 'invalid imported legacy trial use');
END

-- maestro:statement
CREATE TRIGGER imported_legacy_trial_uses_immutable
BEFORE UPDATE ON imported_legacy_trial_uses
WHEN NEW.source_key<>OLD.source_key OR NEW.hash_kind<>OLD.hash_kind OR
     NEW.legacy_hmac<>OLD.legacy_hmac OR NEW.lookup_secret_id<>OLD.lookup_secret_id OR
     NEW.imported_at_unix<>OLD.imported_at_unix
BEGIN
    SELECT RAISE(ABORT, 'imported legacy trial uses are immutable');
END

-- maestro:statement
CREATE TRIGGER imported_legacy_trial_uses_no_delete
BEFORE DELETE ON imported_legacy_trial_uses
BEGIN
    SELECT RAISE(ABORT, 'imported legacy trial uses are immutable');
END

-- maestro:statement
CREATE TRIGGER imported_trial_identities_no_legacy_only_source
BEFORE INSERT ON imported_trial_identities
WHEN EXISTS (SELECT 1 FROM imported_legacy_trial_uses WHERE source_key=NEW.source_key)
BEGIN
    SELECT RAISE(ABORT, 'imported trial source already has a legacy-only identity');
END
