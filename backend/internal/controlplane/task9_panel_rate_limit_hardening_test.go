package controlplane_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestPanelRateLimitUsesDatabaseTimeAcrossSkewedNodesSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	ctx := context.Background()
	for attempt := 0; attempt < 2; attempt++ {
		decision, err := fixture.service.ConsumeRateLimit(
			ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute,
		)
		if err != nil || !decision.Allowed {
			t.Fatalf("attempt %d decision=%+v err=%v, want allowed", attempt+1, decision, err)
		}
	}

	skewedClock := &f5MutableClock{value: fixture.startedAt.Add(365 * 24 * time.Hour)}
	store, err := controlplane.NewStore(fixture.database, fixture.box, skewedClock)
	if err != nil {
		t.Fatalf("new skewed store: %v", err)
	}
	peer, err := controlplane.NewService(store, &f5UniqueIDs{}, skewedClock)
	if err != nil {
		t.Fatalf("new skewed peer: %v", err)
	}
	blocked, err := peer.ConsumeRateLimit(
		ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute,
	)
	if err != nil || blocked.Allowed || blocked.RetryAfterSeconds < 1 {
		t.Fatalf("skewed peer decision=%+v err=%v, want cluster-wide denial", blocked, err)
	}
}

func TestPanelRateLimitGarbageCollectsExpiredBucketsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	fixture.sqlite.must(t, rqlite.Statement{SQL: "WITH RECURSIVE seq(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM seq WHERE value<300) INSERT INTO rate_limit_buckets(bucket_scope,bucket_key_hmac,window_started_at_unix,count_value,blocked_until_unix,expires_at_unix) SELECT 'expired',printf('%064x',value),unixepoch()-7200,1,NULL,unixepoch()-7200 FROM seq"})

	decision, err := fixture.service.ConsumeRateLimit(
		context.Background(), "panel.read.ip", "198.51.100.7", 2, time.Minute, 5*time.Minute,
	)
	if err != nil || !decision.Allowed {
		t.Fatalf("decision=%+v err=%v, want allowed after bounded GC", decision, err)
	}
	rows := fixture.sqlite.must(t, rqlite.Statement{SQL: "SELECT COUNT(*) AS bucket_count FROM rate_limit_buckets"})
	count, ok := task9RateLimitRowInt64(rows[0].Rows[0]["bucket_count"])
	if !ok || count > 50 {
		t.Fatalf("bucket_count=%v, want at most 50 after bounded expired-row GC", rows[0].Rows[0]["bucket_count"])
	}
}

func TestPanelRateLimitCapsBucketCardinalitySQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	fixture.sqlite.must(t, rqlite.Statement{SQL: "WITH RECURSIVE seq(value) AS (SELECT 1 UNION ALL SELECT value+1 FROM seq WHERE value<65536) INSERT INTO rate_limit_buckets(bucket_scope,bucket_key_hmac,window_started_at_unix,count_value,blocked_until_unix,expires_at_unix) SELECT 'capacity',printf('%064x',value),unixepoch(),1,NULL,unixepoch()+86400 FROM seq"})

	_, err := fixture.service.ConsumeRateLimit(
		context.Background(), "panel.read.ip", "192.0.2.55", 2, time.Minute, 5*time.Minute,
	)
	if !errors.Is(err, controlplane.ErrUnavailable) {
		t.Fatalf("capacity error=%v, want ErrUnavailable", err)
	}
}

func task9RateLimitRowInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), typed == float64(int64(typed))
	default:
		return 0, false
	}
}
