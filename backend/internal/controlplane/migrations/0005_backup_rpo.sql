-- MaestroVPN HA durable backup RPO schema v5. Applied migrations v1-v4 remain byte-for-byte immutable.

-- maestro:statement
ALTER TABLE cluster_job_leases ADD COLUMN restore_epoch INTEGER NOT NULL DEFAULT 0
    CHECK(restore_epoch >= 0)

-- maestro:statement
ALTER TABLE cluster_job_leases ADD COLUMN lease_fence INTEGER NOT NULL DEFAULT 0
    CHECK(lease_fence >= 0)

-- maestro:statement
ALTER TABLE cluster_job_leases ADD COLUMN capability_generation INTEGER NOT NULL DEFAULT 0
    CHECK(capability_generation >= 0)

-- maestro:statement
ALTER TABLE cluster_job_leases ADD COLUMN capability_evidence_sha256 TEXT CHECK(
    capability_evidence_sha256 IS NULL OR (
        length(capability_evidence_sha256) = 64 AND
        capability_evidence_sha256 = lower(capability_evidence_sha256) AND
        capability_evidence_sha256 NOT GLOB '*[^0-9a-f]*'
    )
)

-- maestro:statement
ALTER TABLE cluster_job_leases ADD COLUMN capability_expires_at_unix INTEGER NOT NULL DEFAULT 0
    CHECK(
        capability_expires_at_unix >= 0 AND
        (
            job_name <> 'backup-rpo' OR (
                restore_epoch > 0 AND
                lease_fence > 0 AND
                capability_generation > 0 AND
                capability_evidence_sha256 IS NOT NULL AND
                capability_expires_at_unix > acquired_at_unix AND
                expires_at_unix <= capability_expires_at_unix
            )
        )
    )

-- maestro:statement
CREATE TABLE backup_rpo_state (
    singleton_id INTEGER PRIMARY KEY CHECK(singleton_id = 1),
    restore_epoch INTEGER NOT NULL CHECK(restore_epoch > 0),
    dirty_generation INTEGER NOT NULL CHECK(dirty_generation >= 0),
    verified_generation INTEGER NOT NULL CHECK(
        verified_generation >= 0 AND verified_generation <= dirty_generation
    ),
    verified_backup_id TEXT,
    verified_object_key TEXT,
    verified_object_sha256 TEXT,
    verified_object_version TEXT,
    verified_size_bytes INTEGER,
    verified_manifest_version INTEGER,
    verified_at_unix INTEGER,
    last_attempt_sequence INTEGER NOT NULL CHECK(last_attempt_sequence >= 0),
    phase TEXT NOT NULL CHECK(phase IN ('dirty','verified')),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix > 0),
    CHECK(
        (dirty_generation > verified_generation AND phase = 'dirty') OR
        (dirty_generation = verified_generation AND phase = 'verified')
    ),
    CHECK(
        (
            verified_generation = 0 AND
            verified_backup_id IS NULL AND
            verified_object_key IS NULL AND
            verified_object_sha256 IS NULL AND
            verified_object_version IS NULL AND
            verified_size_bytes IS NULL AND
            verified_manifest_version IS NULL AND
            verified_at_unix IS NULL
        ) OR (
            verified_generation > 0 AND
            last_attempt_sequence > 0 AND
            verified_backup_id IS NOT NULL AND
            length(verified_backup_id) = 32 AND
            verified_backup_id = lower(verified_backup_id) AND
            verified_backup_id NOT GLOB '*[^0-9a-f]*' AND
            verified_object_key IS NOT NULL AND
            length(verified_object_key) > 0 AND
            verified_object_key = trim(verified_object_key) AND
            verified_object_sha256 IS NOT NULL AND
            length(verified_object_sha256) = 64 AND
            verified_object_sha256 = lower(verified_object_sha256) AND
            verified_object_sha256 NOT GLOB '*[^0-9a-f]*' AND
            verified_object_version IS NOT NULL AND
            length(verified_object_version) > 0 AND
            verified_object_version = trim(verified_object_version) AND
            lower(verified_object_version) NOT IN ('latest','null','none') AND
            verified_size_bytes > 0 AND
            verified_manifest_version = 2 AND
            verified_at_unix > 0
        )
    )
)

-- maestro:statement
CREATE TABLE backup_rpo_attempts (
    restore_epoch INTEGER NOT NULL CHECK(restore_epoch > 0),
    attempt_sequence INTEGER NOT NULL CHECK(attempt_sequence > 0),
    phase TEXT NOT NULL CHECK(
        phase IN ('pending','applying','applied','unknown','verified','superseded','failed')
    ),
    backup_id TEXT NOT NULL CHECK(
        length(backup_id) = 32 AND
        backup_id = lower(backup_id) AND
        backup_id NOT GLOB '*[^0-9a-f]*'
    ),
    captured_generation INTEGER NOT NULL CHECK(captured_generation > 0),
    object_key TEXT NOT NULL CHECK(length(object_key) > 0 AND object_key = trim(object_key)),
    object_sha256 TEXT NOT NULL CHECK(
        length(object_sha256) = 64 AND
        object_sha256 = lower(object_sha256) AND
        object_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    object_version TEXT CHECK(
        object_version IS NULL OR (
            length(object_version) > 0 AND
            object_version = trim(object_version) AND
            lower(object_version) NOT IN ('latest','null','none')
        )
    ),
    object_size_bytes INTEGER NOT NULL CHECK(object_size_bytes > 0),
    manifest_version INTEGER NOT NULL CHECK(manifest_version = 2),
    adapter_contract_version TEXT NOT NULL CHECK(adapter_contract_version = 'yandex-s3-v1'),
    capability_generation INTEGER NOT NULL CHECK(capability_generation > 0),
    capability_evidence_sha256 TEXT NOT NULL CHECK(
        length(capability_evidence_sha256) = 64 AND
        capability_evidence_sha256 = lower(capability_evidence_sha256) AND
        capability_evidence_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    capability_expires_at_unix INTEGER NOT NULL CHECK(capability_expires_at_unix > 0),
    lease_holder_id TEXT NOT NULL CHECK(length(lease_holder_id) > 0),
    lease_token TEXT NOT NULL CHECK(length(lease_token) > 0),
    lease_fence INTEGER NOT NULL CHECK(lease_fence > 0),
    failure_code TEXT CHECK(
        failure_code IS NULL OR (
            length(failure_code) BETWEEN 1 AND 64 AND
            failure_code = lower(failure_code) AND
            failure_code NOT GLOB '*[^a-z0-9_-]*'
        )
    ),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix > 0),
    updated_at_unix INTEGER NOT NULL CHECK(updated_at_unix >= created_at_unix),
    CHECK(
        (phase IN ('pending','applying','unknown') AND object_version IS NULL) OR
        (phase IN ('applied','verified') AND object_version IS NOT NULL) OR
        phase IN ('superseded','failed')
    ),
    UNIQUE(restore_epoch, attempt_sequence)
)

-- maestro:statement
CREATE TRIGGER backup_rpo_attempts_no_delete
BEFORE DELETE ON backup_rpo_attempts
BEGIN
    SELECT RAISE(ABORT, 'backup RPO attempts are append-only');
END

-- maestro:statement
INSERT INTO backup_rpo_state(
    singleton_id,restore_epoch,dirty_generation,verified_generation,
    last_attempt_sequence,phase,updated_at_unix
)
SELECT
    1,restore_epoch,1,0,0,'dirty',CAST(strftime('%s','now') AS INTEGER)
FROM cluster_restore_state
WHERE singleton_id = 1