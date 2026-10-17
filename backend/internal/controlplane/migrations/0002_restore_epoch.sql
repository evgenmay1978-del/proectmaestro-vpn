-- MaestroVPN HA restore epoch v2. Every statement is split only at the marker below.

-- maestro:statement
CREATE TABLE cluster_restore_state (
    singleton_id INTEGER PRIMARY KEY CHECK(singleton_id = 1),
    cluster_id TEXT NOT NULL CHECK(
        length(cluster_id) = 64 AND cluster_id = lower(cluster_id)
    ),
    restore_epoch INTEGER NOT NULL CHECK(restore_epoch > 0),
    restored_from_backup_sha256 TEXT CHECK(
        restored_from_backup_sha256 IS NULL OR
        (length(restored_from_backup_sha256) = 64 AND restored_from_backup_sha256 = lower(restored_from_backup_sha256))
    ),
    activated INTEGER NOT NULL CHECK(activated IN (0,1)),
    created_at_unix INTEGER NOT NULL CHECK(created_at_unix >= 0),
    activated_at_unix INTEGER CHECK(
        activated_at_unix IS NULL OR activated_at_unix >= created_at_unix
    ),
    CHECK(
        (activated = 0 AND activated_at_unix IS NULL) OR
        (activated = 1 AND activated_at_unix IS NOT NULL)
    )
)

-- maestro:statement
INSERT INTO cluster_restore_state(
    singleton_id,cluster_id,restore_epoch,restored_from_backup_sha256,
    activated,created_at_unix,activated_at_unix
)
VALUES(
    1,lower(hex(randomblob(32))),1,NULL,1,
    CAST(strftime('%s','now') AS INTEGER),
    CAST(strftime('%s','now') AS INTEGER)
)
