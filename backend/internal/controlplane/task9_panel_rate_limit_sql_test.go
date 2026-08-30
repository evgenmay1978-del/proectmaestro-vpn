package controlplane_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestPanelRateLimitIsBoundedHashedAndClusterWideSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, func(string) int { return 2 })
	ctx := context.Background()

	first, err := fixture.service.ConsumeRateLimit(ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute)
	if err != nil || !first.Allowed {
		t.Fatalf("first decision=%+v err=%v, want allowed", first, err)
	}
	second, err := fixture.service.ConsumeRateLimit(ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute)
	if err != nil || !second.Allowed {
		t.Fatalf("second decision=%+v err=%v, want allowed", second, err)
	}
	blocked, err := fixture.service.ConsumeRateLimit(ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute)
	if err != nil || blocked.Allowed || blocked.RetryAfterSeconds < 1 {
		t.Fatalf("blocked decision=%+v err=%v, want bounded denial", blocked, err)
	}

	store, err := controlplane.NewStore(fixture.database, fixture.box, fixture.clock)
	if err != nil {
		t.Fatalf("new shared store: %v", err)
	}
	peer, err := controlplane.NewService(store, &f5UniqueIDs{}, fixture.clock)
	if err != nil {
		t.Fatalf("new peer service: %v", err)
	}
	peerBlocked, err := peer.ConsumeRateLimit(ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute)
	if err != nil || peerBlocked.Allowed {
		t.Fatalf("peer decision=%+v err=%v, want cluster-wide denial", peerBlocked, err)
	}

	rows := fixture.sqlite.must(t, rqlite.Statement{SQL: `SELECT bucket_scope,bucket_key_hmac,count_value FROM rate_limit_buckets`})
	if len(rows) != 1 || len(rows[0].Rows) != 1 {
		t.Fatalf("rate-limit rows=%#v", rows)
	}
	key, _ := rows[0].Rows[0]["bucket_key_hmac"].(string)
	if len(key) != 64 || strings.Contains(key, "203.0.113.8") {
		t.Fatalf("bucket key=%q, want a 64-character HMAC without raw IP", key)
	}

	fixture.sqlite.must(t, rqlite.Statement{SQL: "UPDATE rate_limit_buckets SET window_started_at_unix=unixepoch()-120,blocked_until_unix=NULL,expires_at_unix=unixepoch()-60"})
	reset, err := peer.ConsumeRateLimit(ctx, "panel.read.ip", "203.0.113.8", 2, time.Minute, 5*time.Minute)
	if err != nil || !reset.Allowed {
		t.Fatalf("post-window decision=%+v err=%v, want reset allowance", reset, err)
	}
}
