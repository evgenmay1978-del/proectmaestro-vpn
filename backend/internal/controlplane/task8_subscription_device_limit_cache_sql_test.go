package controlplane_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSubscriptionSixthDeviceDoesNotEvictAllowedCacheSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	allowed := f5WarmAllowedDevices(t, fixture)
	blocked := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "blocked-device-6", f5CacheUserAgent)
	if blocked.status != http.StatusForbidden {
		t.Fatalf("sixth device status=%d body=%q, want 403", blocked.status, blocked.body)
	}

	fixture.database.setUnavailable(true)
	f5RequireAllowedDevicesCached(t, fixture, allowed)
	blockedOutage := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "blocked-device-6", f5CacheUserAgent)
	if blockedOutage.status != http.StatusServiceUnavailable {
		t.Fatalf("sixth device during outage status=%d body=%q, want 503", blockedOutage.status, blockedOutage.body)
	}
}

func TestSubscriptionSixthDeviceWithNewerIdentityInvalidatesOldAllowedCacheSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	allowed := f5WarmAllowedDevices(t, fixture)
	fixture.sqlite.must(t, rqlite.Statement{
		SQL:  `UPDATE customers SET generation=generation+1 WHERE customer_id=?`,
		Args: []any{fixture.customerID},
	})
	blocked := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "new-generation-device-6", f5CacheUserAgent)
	if blocked.status != http.StatusForbidden {
		t.Fatalf("new-generation sixth device status=%d body=%q, want 403", blocked.status, blocked.body)
	}

	fixture.database.setUnavailable(true)
	for index, previous := range allowed {
		device := fmt.Sprintf("preserved-device-%d", index+1)
		got := f5SubscriptionGET(t, fixture.handler, fixture.path(""), device, f5CacheUserAgent)
		if got.status != http.StatusServiceUnavailable {
			t.Fatalf("old identity device %d survived newer strong state: status=%d old_body=%q body=%q", index+1, got.status, previous.body, got.body)
		}
	}
}

func TestSubscriptionSixthDeviceWithStaleIdentityPreservesNewerAllowedCacheSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	fixture.sqlite.must(t, rqlite.Statement{
		SQL:  `UPDATE customers SET generation=2 WHERE customer_id=?`,
		Args: []any{fixture.customerID},
	})
	allowed := f5WarmAllowedDevices(t, fixture)
	fixture.sqlite.must(t, rqlite.Statement{
		SQL:  `UPDATE customers SET generation=1 WHERE customer_id=?`,
		Args: []any{fixture.customerID},
	})
	blocked := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "stale-generation-device-6", f5CacheUserAgent)
	if blocked.status != http.StatusForbidden {
		t.Fatalf("stale-generation sixth device status=%d body=%q, want 403", blocked.status, blocked.body)
	}

	fixture.database.setUnavailable(true)
	f5RequireAllowedDevicesCached(t, fixture, allowed)
}

const f5CacheUserAgent = "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)"

func f5WarmAllowedDevices(t *testing.T, fixture *f5SubscriptionFixture) []f5HTTPResult {
	t.Helper()
	allowed := make([]f5HTTPResult, 5)
	for index := range allowed {
		device := fmt.Sprintf("preserved-device-%d", index+1)
		allowed[index] = f5SubscriptionGET(t, fixture.handler, fixture.path(""), device, f5CacheUserAgent)
		if allowed[index].status != http.StatusOK {
			t.Fatalf("warm device %d status=%d body=%q", index+1, allowed[index].status, allowed[index].body)
		}
	}
	return allowed
}

func f5RequireAllowedDevicesCached(t *testing.T, fixture *f5SubscriptionFixture, allowed []f5HTTPResult) {
	t.Helper()
	for index, want := range allowed {
		device := fmt.Sprintf("preserved-device-%d", index+1)
		got := f5SubscriptionGET(t, fixture.handler, fixture.path(""), device, f5CacheUserAgent)
		if got.status != http.StatusOK || got.contentType != want.contentType || !bytes.Equal(got.body, want.body) {
			t.Fatalf("allowed device %d after blocked probe: status=%d content_type=%q byte_equal=%v", index+1, got.status, got.contentType, bytes.Equal(got.body, want.body))
		}
	}
}
