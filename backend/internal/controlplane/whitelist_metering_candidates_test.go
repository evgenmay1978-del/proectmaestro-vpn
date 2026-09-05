package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestWhiteListMeteringAdmissionCandidatesPrecedeDesiredAndFilterClosedPairs(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	for _, exitID := range []string{"exit-z", "exit-a"} {
		seedWhiteListSidecarInventory(t, db, nil, WhiteListExit{
			ExitID: exitID, CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true,
		})
	}
	_, first := seedWhiteListAdmissionCandidate(t, db, service, "CandidateA", []string{"exit-z", "exit-a"})
	secondCustomer, second := seedWhiteListAdmissionCandidate(t, db, service, "CandidateB", []string{"exit-a"})
	want := []WhiteListMeteringAdmissionCandidate{
		{EntitlementID: first, ExitID: "exit-z"},
		{EntitlementID: first, ExitID: "exit-a"},
		{EntitlementID: second, ExitID: "exit-a"},
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].EntitlementID == want[j].EntitlementID {
			return want[i].ExitID < want[j].ExitID
		}
		return want[i].EntitlementID < want[j].EntitlementID
	})
	assertCandidates := func(expected []WhiteListMeteringAdmissionCandidate) {
		t.Helper()
		got, err := service.WhiteListMeteringAdmissionCandidates(ctx)
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("candidates=%#v error=%v, want %#v", got, err, expected)
		}
	}
	assertCandidates(want)
	// No desired generation or admission is needed for discovery, and discovery
	// itself must not create either or change the paid balance projection.
	rows := db.must(t, rqlite.Statement{SQL: `SELECT
(SELECT COUNT(*) FROM whitelist_sidecar_desired) AS desired,
(SELECT COUNT(*) FROM whitelist_first_use_admissions) AS admissions,
(SELECT COUNT(*) FROM whitelist_balance_projections
 WHERE version<>1 OR purchased_remaining_bytes<>1000000000) AS changed_balances`})[0].Rows
	for _, name := range []string{"desired", "admissions", "changed_balances"} {
		if count, ok := rowInt64(rows[0], name); !ok || count != 0 {
			t.Fatalf("discovery mutated %s: %#v", name, rows)
		}
	}
	// CandidateB has no exit-z credential: it was already excluded above.
	db.must(t, rqlite.Statement{SQL: `UPDATE whitelist_sidecar_exits SET healthy=0 WHERE exit_id='exit-z'`})
	filtered := make([]WhiteListMeteringAdmissionCandidate, 0, 2)
	for _, candidate := range want {
		if candidate.ExitID == "exit-a" {
			filtered = append(filtered, candidate)
		}
	}
	assertCandidates(filtered)
	db.must(t, rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix)
VALUES(?,?,3,0,'ADMIN_DISABLE',NULL,?,?,?)`, Args: []any{
		"candidate-disable:" + first, first, "candidate-disable-operation:" + first,
		testDigest("d"), service.clock.Now().Unix(),
	}})
	assertCandidates([]WhiteListMeteringAdmissionCandidate{{EntitlementID: second, ExitID: "exit-a"}})
	db.must(t, rqlite.Statement{SQL: `UPDATE customers SET expires_at_unix=? WHERE customer_id=?`,
		Args: []any{service.clock.Now().Unix(), secondCustomer}})
	assertCandidates([]WhiteListMeteringAdmissionCandidate{})
}

func TestWhiteListMeteringAdmissionCandidatesRejectUnavailableOrCancelledContext(t *testing.T) {
	_, service := newCustomerIntegritySQLite(t)
	if got, err := service.WhiteListMeteringAdmissionCandidates(nil); !errors.Is(err, ErrUnavailable) || got != nil {
		t.Fatalf("nil context candidates=%#v error=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := service.WhiteListMeteringAdmissionCandidates(ctx); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancelled context candidates=%#v error=%v", got, err)
	}
}

// Uses the existing real-storage admission fixture contract: confirmed ordinary
// order, zero-grant period, purchased projection and explicit admin enable.
func seedWhiteListAdmissionCandidate(
	t *testing.T, db *customerIntegritySQLite, service *Service, label string, exitIDs []string,
) (string, string) {
	t.Helper()
	ctx := context.Background()
	now := service.clock.Now().Unix()
	customer, err := service.ProvisionCustomer(ctx, ProvisionCustomerCommand{
		Login: label, Days: 30, IdempotencyKey: "candidate-" + label,
	})
	if err != nil {
		t.Fatal(err)
	}
	entitlement, err := service.EnsureWhiteListEntitlement(ctx, customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	entitlementID := entitlement.EntitlementID()
	orderID, periodID := "candidate-order-"+label, "candidate-period-"+label
	orderDigest := sha256.Sum256([]byte(orderID))
	db.must(t,
		// This fixture runs without rqlite_integration: do not call helpers
		// declared only in the separately tagged integration test files.
		rqlite.Statement{SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,?, 'candidate-test',?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'confirmed','applied','confirmed',?,?,1,?)`,
			Args: []any{orderID, fmt.Sprintf("%X", orderDigest[:6]), fmt.Sprintf("%x", orderDigest[:]), customer.ID,
				now - 100, now - 100 + 86400, now - 50, now + 90*86400, orderID + "-operation"}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix) VALUES(?,?,0,?,?,0,?,?)`,
			Args: []any{periodID, entitlementID, now - 100, now + 86400, orderID, now}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(
entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,
lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix)
VALUES(?,?,0,1000000000,0,0,1,0,0,?)`, Args: []any{entitlementID, periodID, now}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_publication_controls(
control_id,entitlement_id,version,enabled,source,source_topup_order_id,
operation_id,request_hash,created_at_unix) VALUES(?,?,2,1,'ADMIN_ENABLE',NULL,?,?,?)`,
			Args: []any{"candidate-enable:" + entitlementID, entitlementID,
				"candidate-enable-operation:" + entitlementID, testDigest("b"), now}},
	)
	material, err := json.Marshal(WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/candidate",
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, exitID := range exitIDs {
		credential, err := NewWhiteListRouteCredential(service.store.secrets, entitlementID, exitID, material)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
			t.Fatal(err)
		}
	}
	return customer.ID, entitlementID
}
