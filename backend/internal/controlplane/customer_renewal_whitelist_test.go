package controlplane

import (
	"context"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestRenewCustomerExtendsExistingWhiteListPeriodAndPreservesPurchasedBytesSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	customer := seedIntegrityCustomer(t, service)
	entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), customer.ID)
	if err != nil {
		t.Fatalf("ensure entitlement: %v", err)
	}
	if _, err := service.SetWhiteListPublication(context.Background(), SetWhiteListPublicationCommand{
		EntitlementID: entitlement.EntitlementID(), Enabled: true, IdempotencyKey: "renewal-enable", Actor: "owner",
	}); err != nil {
		t.Fatalf("enable white-list: %v", err)
	}
	credit, err := service.CreditWhiteListManualGB(context.Background(), CreditWhiteListManualGBCommand{
		EntitlementID: entitlement.EntitlementID(), GB: 2, IdempotencyKey: "renewal-credit", Actor: "owner",
	})
	if err != nil {
		t.Fatalf("credit white-list: %v", err)
	}
	ordersBefore := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM orders`})
	intentsBefore := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM whitelist_renewal_intents`})
	renewed, err := service.RenewCustomer(context.Background(), RenewCustomerCommand{
		Login: "Existing", Days: 2, IdempotencyKey: "renewal-bridge",
	})
	if err != nil {
		t.Fatalf("renew customer: %v", err)
	}
	if renewed.ExpiresAtUnix != customer.ExpiresAtUnix+2*86400 {
		t.Fatalf("renewed expiry=%d, want %d", renewed.ExpiresAtUnix, customer.ExpiresAtUnix+2*86400)
	}
	periods := db.must(t, rqlite.Statement{SQL: `SELECT period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,customer_access_source_id FROM whitelist_billing_periods WHERE entitlement_id=? ORDER BY period_ordinal`, Args: []any{entitlement.EntitlementID()}})
	if len(periods) != 1 || len(periods[0].Rows) != 2 {
		t.Fatalf("billing periods=%#v, want initial plus renewal bridge", periods)
	}
	bridge := periods[0].Rows[1]
	starts, startsOK := rowInt64(bridge, "starts_at_unix")
	ends, endsOK := rowInt64(bridge, "ends_at_unix")
	grant, grantOK := rowInt64(bridge, "included_grant_bytes")
	_, sourceOK := rowString(bridge, "customer_access_source_id")
	if !startsOK || !endsOK || !grantOK || !sourceOK || starts != customer.ExpiresAtUnix || ends != renewed.ExpiresAtUnix ||
		grant != 0 || bridge["access_order_id"] != nil {
		t.Fatalf("renewal bridge period=%#v", bridge)
	}
	projection := db.must(t, rqlite.Statement{SQL: `SELECT current_period_id,purchased_remaining_bytes,version FROM whitelist_balance_projections WHERE entitlement_id=?`, Args: []any{entitlement.EntitlementID()}})
	var purchased int64
	purchasedOK := false
	if len(projection) == 1 && len(projection[0].Rows) == 1 {
		purchased, purchasedOK = rowInt64(projection[0].Rows[0], "purchased_remaining_bytes")
	}
	if !purchasedOK || purchased != credit.PurchasedRemainingBytes {
		t.Fatalf("renewal changed purchased balance: %#v, credit=%#v", projection, credit)
	}
	ordersAfter := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM orders`})
	intentsAfter := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM whitelist_renewal_intents`})
	if len(ordersBefore) != 1 || len(ordersBefore[0].Rows) != 1 || len(ordersAfter) != 1 || len(ordersAfter[0].Rows) != 1 ||
		len(intentsBefore) != 1 || len(intentsBefore[0].Rows) != 1 || len(intentsAfter) != 1 || len(intentsAfter[0].Rows) != 1 {
		t.Fatalf("order/intents count query malformed")
	}
	beforeOrders, beforeOrdersOK := rowInt64(ordersBefore[0].Rows[0], "n")
	afterOrders, afterOrdersOK := rowInt64(ordersAfter[0].Rows[0], "n")
	beforeIntents, beforeIntentsOK := rowInt64(intentsBefore[0].Rows[0], "n")
	afterIntents, afterIntentsOK := rowInt64(intentsAfter[0].Rows[0], "n")
	if !beforeOrdersOK || !afterOrdersOK || !beforeIntentsOK || !afterIntentsOK || beforeOrders != afterOrders || beforeIntents != afterIntents {
		t.Fatalf("renewal created native order/intent: before orders=%d intents=%d, after orders=%d intents=%d", beforeOrders, beforeIntents, afterOrders, afterIntents)
	}

	replay, err := service.RenewCustomer(context.Background(), RenewCustomerCommand{
		Login: "Existing", Days: 2, IdempotencyKey: "renewal-bridge",
	})
	if err != nil || replay.ExpiresAtUnix != renewed.ExpiresAtUnix {
		t.Fatalf("renewal replay=%#v, err=%v", replay, err)
	}
	periods = db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM whitelist_billing_periods WHERE entitlement_id=?`, Args: []any{entitlement.EntitlementID()}})
	var periodCount int64
	periodCountOK := false
	if len(periods) == 1 && len(periods[0].Rows) == 1 {
		periodCount, periodCountOK = rowInt64(periods[0].Rows[0], "n")
	}
	if !periodCountOK || periodCount != 2 {
		t.Fatalf("renewal replay duplicated period: %#v", periods)
	}
}
