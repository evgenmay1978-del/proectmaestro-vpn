-- maestro:statement
ALTER TABLE rate_limit_buckets
ADD COLUMN expires_at_unix INTEGER NOT NULL DEFAULT 0 CHECK(expires_at_unix >= 0)

-- maestro:statement
UPDATE rate_limit_buckets
SET expires_at_unix = CASE
    WHEN blocked_until_unix IS NOT NULL AND blocked_until_unix > window_started_at_unix + 86400
        THEN blocked_until_unix
    ELSE window_started_at_unix + 86400
END
WHERE expires_at_unix = 0

-- maestro:statement
CREATE INDEX idx_rate_limit_buckets_expiry
ON rate_limit_buckets(expires_at_unix, bucket_scope, bucket_key_hmac)
