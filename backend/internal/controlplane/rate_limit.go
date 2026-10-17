package controlplane

import (
	"context"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	rateLimitBucketCapacity            = 65_536
	rateLimitGarbageCollectionBatch    = 256
	rateLimitExpiredRetentionInSeconds = 60
)

type RateLimitDecision struct {
	Allowed           bool
	RetryAfterSeconds int
}

// ConsumeRateLimit atomically consumes one fixed-window allowance in rqlite.
// SQLite's clock is authoritative for every node. Expired buckets are removed
// in bounded batches and cardinality is capped before accepting a new key.
// Only an HMAC of the actor/IP key is persisted.
func (s *Service) ConsumeRateLimit(
	ctx context.Context,
	scope, key string,
	limit int,
	window, block time.Duration,
) (RateLimitDecision, error) {
	scope = strings.TrimSpace(scope)
	key = strings.TrimSpace(key)
	if scope == "" || len(scope) > 64 || key == "" || len(key) > 512 || limit < 1 || limit > 100_000 ||
		window < time.Second || window > 24*time.Hour || block < time.Second || block > 24*time.Hour {
		return RateLimitDecision{}, ErrForbidden
	}

	windowSeconds := int64(window / time.Second)
	blockSeconds := int64(block / time.Second)
	keyHMAC := s.store.secrets.LookupHMAC("rate-limit-bucket", []byte(scope+"\x00"+key))
	results, err := s.store.db.Request(
		ctx,
		rqlite.Linearizable,
		true,
		rqlite.Statement{
			SQL: `DELETE FROM rate_limit_buckets
WHERE rowid IN (
  SELECT rowid FROM rate_limit_buckets
  WHERE expires_at_unix>0 AND expires_at_unix<=unixepoch()-?
  ORDER BY expires_at_unix,bucket_scope,bucket_key_hmac
  LIMIT ?
)`,
			Args: []any{rateLimitExpiredRetentionInSeconds, rateLimitGarbageCollectionBatch},
		},
		rqlite.Statement{
			SQL: `INSERT INTO rate_limit_buckets(
bucket_scope,bucket_key_hmac,window_started_at_unix,count_value,blocked_until_unix,expires_at_unix
)
SELECT ?,?,unixepoch(),1,NULL,unixepoch()+?
WHERE EXISTS(
  SELECT 1 FROM rate_limit_buckets WHERE bucket_scope=? AND bucket_key_hmac=?
) OR (SELECT COUNT(*) FROM rate_limit_buckets)<?
ON CONFLICT(bucket_scope,bucket_key_hmac) DO UPDATE SET
window_started_at_unix=CASE
  WHEN rate_limit_buckets.blocked_until_unix IS NOT NULL AND rate_limit_buckets.blocked_until_unix>unixepoch() THEN rate_limit_buckets.window_started_at_unix
  WHEN rate_limit_buckets.window_started_at_unix<=unixepoch()-? THEN unixepoch()
  ELSE rate_limit_buckets.window_started_at_unix END,
count_value=CASE
  WHEN rate_limit_buckets.blocked_until_unix IS NOT NULL AND rate_limit_buckets.blocked_until_unix>unixepoch() THEN rate_limit_buckets.count_value
  WHEN rate_limit_buckets.window_started_at_unix<=unixepoch()-? THEN 1
  WHEN rate_limit_buckets.count_value<? THEN rate_limit_buckets.count_value+1
  ELSE rate_limit_buckets.count_value END,
blocked_until_unix=CASE
  WHEN rate_limit_buckets.blocked_until_unix IS NOT NULL AND rate_limit_buckets.blocked_until_unix>unixepoch() THEN rate_limit_buckets.blocked_until_unix
  WHEN rate_limit_buckets.window_started_at_unix<=unixepoch()-? THEN NULL
  WHEN rate_limit_buckets.count_value>=? THEN unixepoch()+?
  ELSE NULL END,
expires_at_unix=CASE
  WHEN rate_limit_buckets.blocked_until_unix IS NOT NULL AND rate_limit_buckets.blocked_until_unix>unixepoch() THEN rate_limit_buckets.expires_at_unix
  WHEN rate_limit_buckets.window_started_at_unix<=unixepoch()-? THEN unixepoch()+?
  WHEN rate_limit_buckets.count_value>=? THEN MAX(rate_limit_buckets.window_started_at_unix+?,unixepoch()+?)
  ELSE rate_limit_buckets.expires_at_unix END
RETURNING count_value,blocked_until_unix,unixepoch() AS now_unix`,
			Args: []any{
				scope, keyHMAC, windowSeconds,
				scope, keyHMAC, rateLimitBucketCapacity,
				windowSeconds,
				windowSeconds, limit,
				windowSeconds, limit, blockSeconds,
				windowSeconds, windowSeconds, limit, windowSeconds, blockSeconds,
			},
		},
	)
	if err != nil || len(results) != 2 || len(results[1].Rows) != 1 {
		return RateLimitDecision{}, ErrUnavailable
	}
	row := results[1].Rows[0]
	count, countOK := rowInt64(row, "count_value")
	now, nowOK := rowInt64(row, "now_unix")
	if !countOK || count < 1 || !nowOK || now < 1 {
		return RateLimitDecision{}, ErrUnavailable
	}
	blockedAt := int64(0)
	if raw, present := row["blocked_until_unix"]; present && raw != nil {
		var blockedOK bool
		blockedAt, blockedOK = rowInt64(row, "blocked_until_unix")
		if !blockedOK {
			return RateLimitDecision{}, ErrUnavailable
		}
	}
	if blockedAt > now {
		retry := blockedAt - now
		if retry < 1 {
			retry = 1
		}
		return RateLimitDecision{Allowed: false, RetryAfterSeconds: int(retry)}, nil
	}
	return RateLimitDecision{Allowed: true}, nil
}
