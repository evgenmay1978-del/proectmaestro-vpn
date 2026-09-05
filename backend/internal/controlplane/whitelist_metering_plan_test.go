package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestWhiteListMeteringPlanBindsDesiredReceiptRouteAndCurrentPrepaidPeriod(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	now := service.clock.Now()
	customer := seedIntegrityCustomer(t, service)
	entitlement, err := service.EnsureWhiteListEntitlement(ctx, customer.ID)
	if err != nil {
		t.Fatalf("EnsureWhiteListEntitlement: %v", err)
	}
	entitlementID := entitlement.EntitlementID()
	origin := WhiteListOrigin{
		OriginID: "origin-s4", NodeID: "s4", ReleaseID: "release-1",
		ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"),
		Active: true,
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, []WhiteListOrigin{origin}, exit)
	materialBytes, err := json.Marshal(WhiteListClientMaterial{
		PublicHost: "cdn.example.invalid", SecretPath: "/static/main/video/segment.ts/metering-plan",
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewWhiteListRouteCredential(service.store.secrets, entitlementID, exit.ExitID, materialBytes)
	if err != nil {
		t.Fatalf("NewWhiteListRouteCredential: %v", err)
	}
	if err := service.StoreWhiteListRouteCredential(ctx, credential); err != nil {
		t.Fatalf("StoreWhiteListRouteCredential: %v", err)
	}
	desired, err := BuildWhiteListSidecarDesired(nil, []WhiteListOrigin{origin}, []WhiteListManagedRoute{{
		EntitlementID: entitlementID, ExitID: exit.ExitID,
	}}, exit)
	if err != nil || len(desired) != 1 {
		t.Fatalf("BuildWhiteListSidecarDesired=%#v err=%v", desired, err)
	}
	receipt, err := service.ExecuteWhiteListSidecarAction(ctx, desired[0], "worker-s4", &desiredReceiptSender{
		now: now, bootID: "boot-s4",
	})
	if err != nil {
		t.Fatalf("ExecuteWhiteListSidecarAction: %v", err)
	}
	if _, err := service.WhiteListMeteringPlan(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("plan without current paid period error = %v, want unavailable", err)
	}

	orderID, periodID := "order-metering-plan", "period-metering-plan"
	db.must(t,
		rqlite.Statement{
			SQL: `INSERT INTO orders(
order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,
amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,
provisioning_state,decision,confirmed_at_unix,result_expires_at_unix,result_generation,operation_id)
VALUES(?,?,?,?,?,'tariff_1m_v1',40000,'RUB',30,?,?,'confirmed','applied','confirmed',?,?,1,?)`,
			Args: []any{
				orderID, "A1B2C3D4E5F6", "whitelist-metering-plan",
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				customer.ID, now.Unix() - 100, now.Unix() - 100 + 86400,
				now.Unix() - 50, now.Unix() + 90*86400, orderID + "-operation",
			},
		},
		rqlite.Statement{
			SQL: `INSERT INTO whitelist_billing_periods(
period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,
included_grant_bytes,access_order_id,created_at_unix) VALUES(?,?,0,?,?,0,?,?)`,
			Args: []any{periodID, entitlementID, now.Unix() - 100, now.Unix() + 86400, orderID, now.Unix()},
		},
	)
	plan, err := service.WhiteListMeteringPlan(ctx)
	if err != nil {
		t.Fatalf("WhiteListMeteringPlan: %v", err)
	}
	if len(plan.Origins) != 1 || plan.Origins[0].Origin.OriginID != origin.OriginID ||
		plan.Origins[0].Origin.NodeID != origin.NodeID ||
		plan.Origins[0].Origin.ConfigDigest != origin.ConfigDigest ||
		plan.Origins[0].Desired.Action.ActionKey != desired[0].Action.ActionKey ||
		plan.Origins[0].Receipt != receipt {
		t.Fatalf("origin binding = %#v", plan.Origins)
	}
	if len(plan.Routes) != 1 {
		t.Fatalf("routes = %#v", plan.Routes)
	}
	if pending := plan.Origins[0].PendingFirstCumulativeUsers; len(pending) != 1 ||
		pending[0] != whiteListManagedEmail(entitlementID, exit.ExitID) {
		t.Fatalf("unproven initial cumulative was offered to the baseline adapter: %#v", pending)
	}
	route := plan.Routes[0]
	xrayIdentity, xrayOK := route.Entitlement.XrayIdentity()
	if route.ManagedEmail != whiteListManagedEmail(entitlementID, exit.ExitID) || route.ExitID != exit.ExitID ||
		route.Entitlement.EntitlementID() != entitlementID || route.Entitlement.AccountID() != customer.ID ||
		route.Entitlement.TransportProfileID() != origin.ProfileID ||
		route.Entitlement.CompatibilityPresetID() != origin.PresetID ||
		route.Entitlement.TransportReleaseID() != origin.ReleaseID || !route.Entitlement.Active() ||
		!xrayOK || xrayIdentity != "wl:"+entitlementID {
		t.Fatalf("route entitlement = %#v identity=%q ok=%v", route, xrayIdentity, xrayOK)
	}
	wantPolicy := WhiteListMeteringPolicy{
		BillingPeriodID: periodID, PeriodStartsAtUnix: now.Unix() - 100, PeriodEndsAtUnix: now.Unix() + 86400,
		Unit: whiteListMeteringUnitGBDecimal, Basis: whiteListMeteringBasisUplinkPlusDownlink,
		PriceMode: whiteListMeteringPriceFree, PriceSource: whiteListMeteringPriceGlobal,
	}
	if route.Policy != wantPolicy {
		t.Fatalf("policy = %#v, want %#v", route.Policy, wantPolicy)
	}
}
