package controlplane_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestSubscriptionAdmissionRechecksAuthorizationBeforeDeviceWriteSQLite(t *testing.T) {
	tests := []struct {
		name       string
		suffix     string
		wantStatus int
		mutate     func(*testing.T, *f5SubscriptionFixture)
	}{
		{
			name: "expiry", wantStatus: http.StatusPaymentRequired,
			mutate: func(t *testing.T, fixture *f5SubscriptionFixture) {
				databaseNow := f5DatabaseNow(t, fixture)
				fixture.sqlite.must(t, rqlite.Statement{
					SQL:  `UPDATE customers SET expires_at_unix=? WHERE customer_id=?`,
					Args: []any{databaseNow - 1, fixture.customerID},
				})
			},
		},
		{
			name: "suspension", wantStatus: http.StatusPaymentRequired,
			mutate: func(t *testing.T, fixture *f5SubscriptionFixture) {
				fixture.sqlite.must(t, rqlite.Statement{
					SQL: `UPDATE customers SET status='suspended' WHERE customer_id=?`, Args: []any{fixture.customerID},
				})
			},
		},
		{
			name: "token-revocation", wantStatus: http.StatusNotFound,
			mutate: func(t *testing.T, fixture *f5SubscriptionFixture) {
				fixture.sqlite.must(t, rqlite.Statement{
					SQL:  `UPDATE subscription_tokens SET revoked=1,revoked_at_unix=? WHERE customer_id=? AND revoked=0`,
					Args: []any{fixture.startedAt.Unix(), fixture.customerID},
				})
			},
		},
		{
			name: "base-credential-deletion", wantStatus: http.StatusForbidden,
			mutate: func(t *testing.T, fixture *f5SubscriptionFixture) {
				fixture.sqlite.must(t, rqlite.Statement{SQL: `DELETE FROM credentials WHERE customer_id=?`, Args: []any{fixture.customerID}})
			},
		},
		{
			name: "links-credential-deletion", suffix: "?format=links", wantStatus: http.StatusForbidden,
			mutate: func(t *testing.T, fixture *f5SubscriptionFixture) {
				fixture.sqlite.must(t, rqlite.Statement{SQL: `DELETE FROM credentials WHERE customer_id=?`, Args: []any{fixture.customerID}})
			},
		},
		{
			name: "request-unavailable", wantStatus: http.StatusServiceUnavailable,
			mutate: func(_ *testing.T, fixture *f5SubscriptionFixture) {
				fixture.database.setUnavailable(true)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newF5SubscriptionFixture(t, nil)
			before := f5ReadAdmissionDurableState(t, fixture)
			fixture.database.beforeRequest = func() { test.mutate(t, fixture) }

			got := f5SubscriptionGET(t, fixture.handler, fixture.path(test.suffix), "race-device", f5CacheUserAgent)
			if got.status != test.wantStatus {
				t.Fatalf("status=%d body=%q, want %d", got.status, got.body, test.wantStatus)
			}
			after := f5ReadAdmissionDurableState(t, fixture)
			if after != before {
				t.Fatalf("authorization race wrote durable device state: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestSubscriptionAdmissionCustomerGenerationCASIsZeroWriteSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	command := f5SubscriptionClaimCommand(t, fixture, "generation-race-device", true, 5)
	before := f5ReadAdmissionDurableState(t, fixture)
	fixture.sqlite.must(t, rqlite.Statement{
		SQL: `UPDATE customers SET generation=generation+1 WHERE customer_id=?`, Args: []any{fixture.customerID},
	})

	if _, err := fixture.service.ClaimSubscriptionDevice(t.Context(), command); !errors.Is(err, controlplane.ErrSubscriptionChanged) {
		t.Fatalf("customer generation race error=%v, want ErrSubscriptionChanged", err)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after != before {
		t.Fatalf("customer generation race wrote durable device state: before=%+v after=%+v", before, after)
	}
}

func TestSubscriptionAdmissionWritesOneDeviceAuditAndDirtyMarkerSQLite(t *testing.T) {
	fixture := newF5SubscriptionFixture(t, nil)
	before := f5ReadAdmissionDurableState(t, fixture)

	got := f5SubscriptionGET(t, fixture.handler, fixture.path(""), "success-device", f5CacheUserAgent)
	if got.status != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", got.status, got.body)
	}
	after := f5ReadAdmissionDurableState(t, fixture)
	if after.Devices != before.Devices+1 || after.Audits != before.Audits+1 || after.DirtyGeneration != before.DirtyGeneration+1 {
		t.Fatalf("successful admission durable delta: before=%+v after=%+v", before, after)
	}
}

type f5AdmissionDurableState struct {
	Devices         int64
	Audits          int64
	DirtyGeneration int64
}

func f5ReadAdmissionDurableState(t *testing.T, fixture *f5SubscriptionFixture) f5AdmissionDurableState {
	t.Helper()
	results := fixture.sqlite.must(t, rqlite.Statement{
		SQL: `SELECT
(SELECT COUNT(*) FROM devices WHERE customer_id=?) AS devices,
(SELECT COUNT(*) FROM audit_events WHERE action='device.claim') AS audits,
(SELECT dirty_generation FROM backup_rpo_state WHERE singleton_id=1) AS dirty_generation`,
		Args: []any{fixture.customerID},
	})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("durable admission state rows=%#v", results)
	}
	row := results[0].Rows[0]
	devices, devicesOK := f5Int64(row["devices"])
	audits, auditsOK := f5Int64(row["audits"])
	dirty, dirtyOK := f5Int64(row["dirty_generation"])
	if !devicesOK || !auditsOK || !dirtyOK {
		t.Fatalf("durable admission state values=%#v", row)
	}
	return f5AdmissionDurableState{Devices: devices, Audits: audits, DirtyGeneration: dirty}
}
