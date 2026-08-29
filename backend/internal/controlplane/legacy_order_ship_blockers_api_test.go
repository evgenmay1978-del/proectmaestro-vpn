package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestLegacyOrderExplicitIdempotencySQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	existing := fixture.seedCustomer(t, "Existing-Idem", "active")
	other := fixture.seedCustomer(t, "Other-Idem", "active")

	t.Run("first-time-replay-is-durable-after-finalization-and-conflicts-on-body", func(t *testing.T) {
		status, firstBody, first := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "first-time-key")
		if status != http.StatusOK || first.OrderID == "" {
			t.Fatalf("first status=%d body=%s", status, firstBody)
		}
		f6ShipAssertIdempotencyBinding(t, fixture.db, "first-time-key", first.OrderID, "new")

		status, replayBody, replay := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "first-time-key")
		if status != http.StatusOK || replay.OrderID != first.OrderID || !bytes.Equal(replayBody, firstBody) {
			t.Fatalf("replay status=%d order=%q body=%s, want exact %q body=%s", status, replay.OrderID, replayBody, first.OrderID, firstBody)
		}

		cancel := httptest.NewRequest(http.MethodPost, "/admin/order/cancel", strings.NewReader(fmt.Sprintf(`{"order_id":%q}`, first.OrderID)))
		cancel.Header.Set("Authorization", "Bearer admin-secret")
		cancel.Header.Set("Idempotency-Key", "cancel-first-time-idem")
		cancelResponse := httptest.NewRecorder()
		fixture.handler.ServeHTTP(cancelResponse, cancel)
		if cancelResponse.Code != http.StatusOK {
			t.Fatalf("cancel status=%d body=%s", cancelResponse.Code, cancelResponse.Body.Bytes())
		}
		status, finalizedBody, finalized := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "first-time-key")
		if status != http.StatusOK || finalized.OrderID != first.OrderID || !bytes.Equal(finalizedBody, firstBody) {
			t.Fatalf("post-final replay status=%d order=%q body=%s, want exact initial response", status, finalized.OrderID, finalizedBody)
		}

		beforeConflict := f6ShipCountsNow(t, fixture.db)
		status, _, _ = f6ShipPostOrder(t, fixture.handler, `{"tariff":"3m"}`, "first-time-key")
		if status != http.StatusConflict {
			t.Fatalf("same key different tariff status=%d, want 409", status)
		}
		status, _, _ = f6ShipPostOrder(t, fixture.handler,
			fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, existing.Access.SubscriptionToken), "first-time-key")
		if status != http.StatusConflict {
			t.Fatalf("same key different mode status=%d, want 409", status)
		}
		if after := f6ShipCountsNow(t, fixture.db); after != beforeConflict {
			t.Fatalf("conflict mutated durable rows before=%+v after=%+v", beforeConflict, after)
		}
	})

	t.Run("existing-replay-is-durable-and-binds-exact-customer", func(t *testing.T) {
		body := fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, existing.Access.SubscriptionToken)
		status, firstBody, first := f6ShipPostOrder(t, fixture.handler, body, "existing-key")
		if status != http.StatusOK || first.OrderID == "" {
			t.Fatalf("first existing status=%d body=%s", status, firstBody)
		}
		f6AssertOrderCustomer(t, fixture.db, first.OrderID, existing.ID)
		f6ShipAssertIdempotencyBinding(t, fixture.db, "existing-key", first.OrderID, "existing")
		f6ShipAssertIdempotencyHasNoPlaintext(t, fixture.db, "existing-key",
			append([]string{"Existing-Idem", existing.Access.SubscriptionToken},
				f6ShipCredentialValues(existing.Access.Credentials)...))

		status, replayBody, replay := f6ShipPostOrder(t, fixture.handler, body, "existing-key")
		if status != http.StatusOK || replay.OrderID != first.OrderID || !bytes.Equal(replayBody, firstBody) {
			t.Fatalf("existing replay status=%d order=%q body=%s, want exact initial response", status, replay.OrderID, replayBody)
		}
		beforeConflict := f6ShipCountsNow(t, fixture.db)
		status, _, _ = f6ShipPostOrder(t, fixture.handler,
			fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, other.Access.SubscriptionToken), "existing-key")
		if status != http.StatusConflict {
			t.Fatalf("same key different customer status=%d, want 409", status)
		}
		if after := f6ShipCountsNow(t, fixture.db); after != beforeConflict {
			t.Fatalf("existing conflict mutated durable rows before=%+v after=%+v", beforeConflict, after)
		}
	})

	t.Run("keyless-taps-stay-distinct-and-write-no-idempotency-row", func(t *testing.T) {
		before := f6ShipCountsNow(t, fixture.db)
		_, _, first := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "")
		_, _, second := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "")
		if first.OrderID == "" || second.OrderID == "" || first.OrderID == second.OrderID {
			t.Fatalf("keyless orders first=%+v second=%+v, want distinct", first, second)
		}
		after := f6ShipCountsNow(t, fixture.db)
		if after.Idempotency != before.Idempotency {
			t.Fatalf("keyless idempotency rows before=%s after=%s, want unchanged", before.Idempotency, after.Idempotency)
		}
	})
}

func TestLegacyOrderExplicitIdempotencyCommittedUnknownAndConcurrentLoserSQLite(t *testing.T) {
	t.Run("committed-unknown-replays-without-write-retry", func(t *testing.T) {
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		outcomeDB := &f6PurchaseOutcomeDB{RQLite: db, commitThenUnknown: true}
		fixture := newF6ShipFixtureOnAppliedDB(t, db, outcomeDB, time.Unix(2_000_000, 0).UTC(), &f6SequenceIDs{})
		status, firstBody, first := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "unknown-key")
		if status != http.StatusOK || first.OrderID == "" {
			t.Fatalf("committed unknown status=%d body=%s", status, firstBody)
		}
		status, replayBody, replay := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "unknown-key")
		if status != http.StatusOK || replay.OrderID != first.OrderID || !bytes.Equal(replayBody, firstBody) || outcomeDB.requests != 1 {
			t.Fatalf("unknown replay status=%d order=%q requests=%d body=%s, want exact one-write response", status, replay.OrderID, outcomeDB.requests, replayBody)
		}
	})

	t.Run("existing-committed-unknown-replays-without-write-retry", func(t *testing.T) {
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		seed := newF6ShipFixtureOnAppliedDB(t, db, db, time.Unix(2_000_000, 0).UTC(), &f6SequenceIDs{next: 10_000})
		existing := seed.seedCustomer(t, "Existing-Unknown", "active")
		outcomeDB := &f6PurchaseOutcomeDB{RQLite: db, commitThenUnknown: true}
		fixture := newF6ShipFixtureOnAppliedDB(t, db, outcomeDB, time.Unix(2_000_000, 0).UTC(), &f6SequenceIDs{next: 20_000})
		body := fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, existing.Access.SubscriptionToken)
		status, firstBody, first := f6ShipPostOrder(t, fixture.handler, body, "existing-unknown-key")
		if status != http.StatusOK || first.OrderID == "" {
			t.Fatalf("existing committed unknown status=%d body=%s", status, firstBody)
		}
		f6AssertOrderCustomer(t, db, first.OrderID, existing.ID)
		status, replayBody, replay := f6ShipPostOrder(t, fixture.handler, body, "existing-unknown-key")
		if status != http.StatusOK || replay.OrderID != first.OrderID || !bytes.Equal(replayBody, firstBody) || outcomeDB.requests != 1 {
			t.Fatalf("existing unknown replay status=%d order=%q requests=%d body=%s, want exact one-write response",
				status, replay.OrderID, outcomeDB.requests, replayBody)
		}
	})

	t.Run("committed-unknown-resolution-failure-is-503-with-one-write", func(t *testing.T) {
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		outcomeDB := &f6PurchaseOutcomeDB{RQLite: db, commitThenUnknown: true, failExactResolution: true}
		fixture := newF6ShipFixtureOnAppliedDB(t, db, outcomeDB, time.Unix(2_000_000, 0).UTC(), &f6SequenceIDs{next: 30_000})
		status, _, _ := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "unknown-read-key")
		if status != http.StatusServiceUnavailable || outcomeDB.requests != 1 {
			t.Fatalf("failed resolution status=%d requests=%d, want 503/one write", status, outcomeDB.requests)
		}
		outcomeDB.failExactResolution = false
		status, replayBody, replay := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "unknown-read-key")
		if status != http.StatusOK || replay.OrderID == "" || outcomeDB.requests != 1 {
			t.Fatalf("recovered resolution status=%d order=%q requests=%d body=%s, want durable replay without write",
				status, replay.OrderID, outcomeDB.requests, replayBody)
		}
	})

	t.Run("concurrent-first-time-loser-has-no-side-effects", func(t *testing.T) {
		db := newS4CanarySQLite(t)
		if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		raceDB := newF6OrderRaceDB(db)
		fixture := newF6ShipFixtureOnAppliedDB(t, db, raceDB, time.Unix(2_000_000, 0).UTC(), &f6RandomIDs{})
		before := f6ShipCountsNow(t, db)
		type result struct {
			status int
			body   []byte
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				status, body, _ := f6ShipPostOrderNoHelper(fixture.handler, `{"tariff":"1m"}`, "race-key")
				results <- result{status: status, body: body}
			}()
		}
		first, second := <-results, <-results
		if first.status != http.StatusOK || second.status != http.StatusOK || !bytes.Equal(first.body, second.body) {
			t.Fatalf("race results first=%d/%s second=%d/%s, want exact 200 replay", first.status, first.body, second.status, second.body)
		}
		after := f6ShipCountsNow(t, db)
		if f6ShipDelta(before.Customers, after.Customers) != 1 || f6ShipDelta(before.Orders, after.Orders) != 1 ||
			f6ShipDelta(before.Audits, after.Audits) != 2 || f6ShipDelta(before.Idempotency, after.Idempotency) != 1 ||
			f6ShipDelta(before.Dirty, after.Dirty) != 1 {
			t.Fatalf("race loser side effects before=%+v after=%+v, want one atomic winner", before, after)
		}
	})
}

func TestLegacyOrderIdentitySuspensionAndLoginCompatibilitySQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	tokenOwner := fixture.seedCustomer(t, "Token-Owner", "active")
	loginOwner := fixture.seedCustomer(t, "Login-Owner", "expired")
	suspended := fixture.seedCustomer(t, "Suspended-Owner", "suspended")

	status, _, knownLogin := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m","login":"Login-Owner"}`, "")
	if status != http.StatusOK {
		t.Fatalf("known login status=%d, want 200", status)
	}
	f6AssertOrderCustomer(t, fixture.db, knownLogin.OrderID, loginOwner.ID)

	status, _, unknownLogin := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m","login":"Unknown-Login"}`, "")
	if status != http.StatusOK {
		t.Fatalf("unknown login status=%d, want first-time 200", status)
	}
	if unknownLogin.OrderID == "" {
		t.Fatal("unknown login returned empty order")
	}

	status, _, priority := f6ShipPostOrder(t, fixture.handler,
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q,"login":"Login-Owner"}`, tokenOwner.Access.SubscriptionToken), "")
	if status != http.StatusOK {
		t.Fatalf("token priority status=%d, want 200", status)
	}
	f6AssertOrderCustomer(t, fixture.db, priority.OrderID, tokenOwner.ID)

	status, _, frozenFallback := f6ShipPostOrder(t, fixture.handler,
		`{"tariff":"1m","sub_token":"unknown-token","login":"Login-Owner"}`, "")
	if status != http.StatusOK || frozenFallback.OrderID == "" {
		t.Fatalf("unknown token frozen fallback status=%d order=%q, want first-time 200", status, frozenFallback.OrderID)
	}
	fallbackCustomer := f6ShipOrderCustomer(t, fixture.db, frozenFallback.OrderID)
	if fallbackCustomer == loginOwner.ID {
		t.Fatalf("unknown supplied token fell through to known login customer %q", fallbackCustomer)
	}

	for _, body := range []string{
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, suspended.Access.SubscriptionToken),
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q,"login":"Token-Owner"}`, suspended.Access.SubscriptionToken),
		`{"tariff":"1m","login":"Suspended-Owner"}`,
	} {
		before := f6ShipCountsNow(t, fixture.db)
		status, _, _ = f6ShipPostOrder(t, fixture.handler, body, "")
		if status == http.StatusOK {
			t.Fatalf("suspended body=%s status=200, want rejection", body)
		}
		if after := f6ShipCountsNow(t, fixture.db); after != before {
			t.Fatalf("suspended mutation before=%+v after=%+v", before, after)
		}
	}

	faultStore, err := controlplane.NewStore(f6QueryFailureDB{RQLite: fixture.db, fragment: "FROM customers c"}, fixture.box, fixture.clock)
	if err != nil {
		t.Fatalf("fault store: %v", err)
	}
	faultService, err := controlplane.NewService(faultStore, &f6SequenceIDs{next: 5_000}, fixture.clock)
	if err != nil {
		t.Fatalf("fault service: %v", err)
	}
	faultHandler := api.NewControlPlane(api.NewServiceBusiness(faultService, api.ServiceBusinessConfig{}), api.Config{}).Handler()
	beforeFailure := f6ShipCountsNow(t, fixture.db)
	for _, body := range []string{
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, tokenOwner.Access.SubscriptionToken),
		`{"tariff":"1m","login":"Token-Owner"}`,
	} {
		status, _, _ = f6ShipPostOrder(t, faultHandler, body, "")
		if status != http.StatusServiceUnavailable {
			t.Fatalf("lookup transport failure body=%s status=%d, want 503", body, status)
		}
		if after := f6ShipCountsNow(t, fixture.db); after != beforeFailure {
			t.Fatalf("lookup failure body=%s mutation before=%+v after=%+v", body, beforeFailure, after)
		}
	}
}

func TestPurchaseOrderDBClockAndMissingRestoreFailClosedSQLite(t *testing.T) {
	for _, test := range []struct {
		name  string
		clock time.Time
	}{
		{"app-clock-ahead", time.Unix(2_000_001, 0).UTC()},
		{"app-clock-behind", time.Unix(1_999_999, 0).UTC()},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newF6ShipFixture(t, nil, test.clock)
			status, body, order := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "")
			if status != http.StatusOK || order.OrderID == "" {
				t.Fatalf("status=%d body=%s, want 200", status, body)
			}
			rows := fixture.db.must(t, rqlite.Statement{SQL: `SELECT CAST(c.expires_at_unix AS TEXT) AS expiry
FROM customers c JOIN orders o ON o.customer_id=c.customer_id WHERE o.order_id=?`, Args: []any{order.OrderID}})
			if len(rows) != 1 || len(rows[0].Rows) != 1 || rows[0].Rows[0]["expiry"] != "2000000" {
				t.Fatalf("DB-authoritative expiry rows=%#v, want 2000000", rows)
			}
		})
	}

	for _, corrupt := range []struct {
		name string
		sql  string
	}{
		{name: "missing-restore-singleton-rolls-back-everything", sql: `DELETE FROM cluster_restore_state`},
		{name: "missing-backup-marker-rolls-back-everything", sql: `DELETE FROM backup_rpo_state`},
	} {
		t.Run(corrupt.name, func(t *testing.T) {
			fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
			fixture.db.must(t, rqlite.Statement{SQL: corrupt.sql})
			before := f6ShipCountsNow(t, fixture.db)
			status, _, _ := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "")
			if status == http.StatusOK {
				t.Fatalf("corrupted backup/restore invariant status=200, want failure")
			}
			if after := f6ShipCountsNow(t, fixture.db); after != before {
				t.Fatalf("corrupted backup/restore invariant left partial rows before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestPaidClaimIgnoresVisibilityFailureButGETDoesNotSQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	_, _, order := f6ShipPostOrder(t, fixture.handler, `{"tariff":"1m"}`, "")
	if order.OrderID == "" {
		t.Fatal("seed order is empty")
	}
	faultStore, err := controlplane.NewStore(f6QueryFailureDB{RQLite: fixture.db, fragment: "AS receipt_ready"}, fixture.box, fixture.clock)
	if err != nil {
		t.Fatalf("fault store: %v", err)
	}
	faultService, err := controlplane.NewService(faultStore, &f6SequenceIDs{next: 9_000}, fixture.clock)
	if err != nil {
		t.Fatalf("fault service: %v", err)
	}
	faultHandler := api.NewControlPlane(api.NewServiceBusiness(faultService, api.ServiceBusinessConfig{}), api.Config{}).Handler()
	claim := httptest.NewRequest(http.MethodPost, "/order/paid-claim", strings.NewReader(fmt.Sprintf(`{"order_id":%q}`, order.OrderID)))
	claimResponse := httptest.NewRecorder()
	faultHandler.ServeHTTP(claimResponse, claim)
	if claimResponse.Code != http.StatusOK || strings.TrimSpace(claimResponse.Body.String()) != `{"status":"awaiting_confirm"}` {
		t.Fatalf("claim status=%d body=%q, want exact 200 acknowledgement", claimResponse.Code, claimResponse.Body.String())
	}
	f6ReadOrder(t, faultHandler, order.OrderID, http.StatusServiceUnavailable)
}

type f6ShipFixture struct {
	db       *s4CanarySQLite
	box      *controlplane.SecretBox
	clock    s4CanaryClock
	service  *controlplane.Service
	business *api.ServiceBusiness
	handler  http.Handler
}

func newF6ShipFixture(t *testing.T, wrapped rqlite.RQLite, now time.Time) f6ShipFixture {
	t.Helper()
	db := newS4CanarySQLite(t)
	if err := controlplane.NewMigrator(db).Apply(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if wrapped == nil {
		wrapped = db
	}
	return newF6ShipFixtureOnAppliedDB(t, db, wrapped, now, &f6SequenceIDs{})
}

func newF6ShipFixtureOnAppliedDB(t *testing.T, db *s4CanarySQLite, wrapped rqlite.RQLite, now time.Time, ids controlplane.IDSource) f6ShipFixture {
	t.Helper()
	box := f6PurchaseSecretBox(t)
	clock := s4CanaryClock{value: now}
	store, err := controlplane.NewStore(wrapped, box, clock)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	service, err := controlplane.NewService(store, ids, clock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	business := api.NewServiceBusiness(service, api.ServiceBusinessConfig{SubBaseURL: "https://service.invalid"})
	return f6ShipFixture{
		db: db, box: box, clock: clock, service: service, business: business,
		handler: api.NewControlPlane(business, api.Config{AdminToken: "admin-secret"}).Handler(),
	}
}

func (fixture f6ShipFixture) seedCustomer(t *testing.T, login, status string) controlplane.Customer {
	t.Helper()
	customer, err := fixture.service.ProvisionCustomer(context.Background(), controlplane.ProvisionCustomerCommand{
		Login: login, Days: 30, IdempotencyKey: "seed-" + strings.ToLower(login),
	})
	if err != nil {
		t.Fatalf("seed %s: %v", login, err)
	}
	if status != "active" {
		fixture.db.must(t, rqlite.Statement{
			SQL: `UPDATE customers SET status=?,expires_at_unix=2000000 WHERE customer_id=?`, Args: []any{status, customer.ID},
		})
		customer.Status = status
		customer.ExpiresAtUnix = 2_000_000
	}
	return customer
}

type f6ShipCounts struct {
	Customers   string
	Orders      string
	Audits      string
	Idempotency string
	Dirty       string
}

func f6ShipCountsNow(t *testing.T, db *s4CanarySQLite) f6ShipCounts {
	t.Helper()
	results := db.must(t, rqlite.Statement{SQL: `
SELECT CAST((SELECT count(*) FROM customers) AS TEXT) AS customers,
CAST((SELECT count(*) FROM orders) AS TEXT) AS orders,
CAST((SELECT count(*) FROM audit_events) AS TEXT) AS audits,
CAST((SELECT count(*) FROM idempotency_requests WHERE scope='legacy-order' AND command_type='create') AS TEXT) AS idempotency,
CAST(COALESCE((SELECT dirty_generation FROM backup_rpo_state WHERE singleton_id=1),-1) AS TEXT) AS dirty`})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("count rows=%#v", results)
	}
	row := results[0].Rows[0]
	return f6ShipCounts{
		Customers: fmt.Sprint(row["customers"]), Orders: fmt.Sprint(row["orders"]), Audits: fmt.Sprint(row["audits"]),
		Idempotency: fmt.Sprint(row["idempotency"]), Dirty: fmt.Sprint(row["dirty"]),
	}
}

func f6ShipDelta(before, after string) int {
	var left, right int
	_, _ = fmt.Sscan(before, &left)
	_, _ = fmt.Sscan(after, &right)
	return right - left
}

func f6ShipPostOrder(t *testing.T, handler http.Handler, body, key string) (int, []byte, f6OrderResponse) {
	t.Helper()
	status, responseBody, order := f6ShipPostOrderNoHelper(handler, body, key)
	if status == http.StatusOK && order.OrderID == "" {
		t.Fatalf("decode successful order body=%s", responseBody)
	}
	return status, responseBody, order
}

func f6ShipPostOrderNoHelper(handler http.Handler, body, key string) (int, []byte, f6OrderResponse) {
	request := httptest.NewRequest(http.MethodPost, "/order", strings.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	responseBody := append([]byte(nil), response.Body.Bytes()...)
	var order f6OrderResponse
	_ = json.Unmarshal(responseBody, &order)
	return response.Code, responseBody, order
}

func f6ShipAssertIdempotencyBinding(t *testing.T, db *s4CanarySQLite, key, orderID, mode string) {
	t.Helper()
	results := db.must(t, rqlite.Statement{SQL: `
SELECT resource_id,decision,status,length(request_hash) AS hash_length,response_json
FROM idempotency_requests WHERE scope='legacy-order' AND command_type='create' AND idempotency_key=?`, Args: []any{key}})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("binding rows=%#v", results)
	}
	row := results[0].Rows[0]
	if row["resource_id"] != orderID || row["decision"] != mode || row["status"] != "applied" || fmt.Sprint(row["hash_length"]) != "64" {
		t.Fatalf("binding row=%#v, want order=%q mode=%q applied hash", row, orderID, mode)
	}
}

func f6ShipAssertIdempotencyHasNoPlaintext(t *testing.T, db *s4CanarySQLite, key string, forbidden []string) {
	t.Helper()
	results := db.must(t, rqlite.Statement{SQL: `
SELECT request_hash,resource_id,decision,operation_id,status,response_json
FROM idempotency_requests WHERE scope='legacy-order' AND command_type='create' AND idempotency_key=?`, Args: []any{key}})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("plaintext binding rows=%#v", results)
	}
	durable := fmt.Sprint(results[0].Rows[0])
	for _, value := range forbidden {
		if value != "" && strings.Contains(durable, value) {
			t.Fatalf("idempotency binding leaked plaintext identity/access value %q in %s", value, durable)
		}
	}
}

func f6ShipCredentialValues(credentials map[string]string) []string {
	values := make([]string, 0, len(credentials))
	for _, value := range credentials {
		values = append(values, value)
	}
	return values
}

func f6ShipOrderCustomer(t *testing.T, db *s4CanarySQLite, orderID string) string {
	t.Helper()
	results := db.must(t, rqlite.Statement{SQL: `SELECT customer_id FROM orders WHERE order_id=?`, Args: []any{orderID}})
	if len(results) != 1 || len(results[0].Rows) != 1 {
		t.Fatalf("order customer rows=%#v", results)
	}
	return fmt.Sprint(results[0].Rows[0]["customer_id"])
}

type f6RandomIDs struct {
	mu   sync.Mutex
	next int
}

func (ids *f6RandomIDs) NewID(prefix string) (string, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.next++
	return fmt.Sprintf("%s-race-%d", prefix, ids.next), nil
}

type f6OrderRaceDB struct {
	rqlite.RQLite
	mu       sync.Mutex
	arrived  int
	release  chan struct{}
	released bool
}

func newF6OrderRaceDB(db rqlite.RQLite) *f6OrderRaceDB {
	return &f6OrderRaceDB{RQLite: db, release: make(chan struct{})}
}

func (db *f6OrderRaceDB) QueryLinearizable(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	for _, statement := range statements {
		if strings.Contains(statement.SQL, "FROM idempotency_requests") && strings.Contains(statement.SQL, "command_type='create'") {
			db.mu.Lock()
			if db.arrived < 2 {
				db.arrived++
				if db.arrived == 2 && !db.released {
					close(db.release)
					db.released = true
				}
				release := db.release
				db.mu.Unlock()
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return db.RQLite.QueryLinearizable(ctx, statements...)
			}
			db.mu.Unlock()
		}
	}
	return db.RQLite.QueryLinearizable(ctx, statements...)
}

func (db *f6OrderRaceDB) Request(ctx context.Context, consistency rqlite.Consistency, transaction bool, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.RQLite.Request(ctx, consistency, transaction, statements...)
}

func (db *f6OrderRaceDB) QueryStrong(ctx context.Context, statements ...rqlite.Statement) ([]rqlite.Result, error) {
	return db.RQLite.QueryStrong(ctx, statements...)
}

func (db *f6OrderRaceDB) Backup(ctx context.Context, destination io.Writer) error {
	return db.RQLite.Backup(ctx, destination)
}
