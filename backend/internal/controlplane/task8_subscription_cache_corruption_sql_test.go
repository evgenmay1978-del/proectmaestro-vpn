package controlplane_test

import (
	"net/http"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSubscriptionCorruptStrongStateNeverFallsBackToCachedSecretsSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	warm := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "corrupt-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if warm.status != http.StatusOK {
		t.Fatalf("warm status=%d body=%q", warm.status, warm.body)
	}
	fixture.sqlite.must(t, rqlite.Statement{
		SQL:  `UPDATE credentials SET secret_envelope=x'00' WHERE customer_id=? AND protocol='vless'`,
		Args: []any{fixture.customerID},
	})

	corrupt := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "corrupt-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if corrupt.status != http.StatusServiceUnavailable {
		t.Fatalf("corrupt strong state status=%d body=%q, want 503 without cache fallback", corrupt.status, corrupt.body)
	}
	if got := fixture.deviceCount(t); got != 1 {
		t.Fatalf("corrupt strong state changed device count to %d, want 1", got)
	}

	fixture.database.setUnavailable(true)
	stale := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "corrupt-device", "SFA/1.0.157 (157; sing-box 1.14; language ru_RU)")
	if stale.status != http.StatusServiceUnavailable {
		t.Fatalf("corrupt cache survived later outage: status=%d body=%q, want 503", stale.status, stale.body)
	}
}
