package controlplane_test

import (
	"net/http"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSubscriptionInfoUsesStrongMetadataWithoutCredentialsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	fixture.sqlite.must(t, rqlite.Statement{
		SQL:  `DELETE FROM credentials WHERE customer_id=?`,
		Args: []any{fixture.customerID},
	})

	info := f5SubscriptionGET(t, fixture.handler, fixture.path("/info"), "", "curl/8")
	if info.status != http.StatusOK {
		t.Fatalf("metadata-only info status=%d body=%q, want 200", info.status, info.body)
	}
	base := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "metadata-device", "curl/8")
	if base.status != http.StatusForbidden {
		t.Fatalf("metadata-only active base status=%d body=%q, want 403", base.status, base.body)
	}
	if got := fixture.deviceCount(t); got != 0 {
		t.Fatalf("metadata-only requests committed %d devices, want 0", got)
	}
}
