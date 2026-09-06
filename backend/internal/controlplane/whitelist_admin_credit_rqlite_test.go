//go:build rqlite_integration

package controlplane

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

func newWhiteListAdminRQLite(t *testing.T) (rqlite.RQLite, *Service, string, string, int64) {
	t.Helper()
	db := task7DB(t)
	now := task7Now(t, db)
	customer := "customer_" + task7Name(t, "admin-credit")
	task7SeedCanonicalFixtureCustomer(t, db, task7FixtureSecretBox(t), customer, "active", now+86400, 7)
	service := whiteListBalanceRQLiteService(t, db, now, "admin")
	entitlement, err := service.EnsureWhiteListEntitlement(task7Context(t), customer)
	if err != nil {
		t.Fatal(err)
	}
	return db, service, customer, entitlement.EntitlementID(), now
}

func TestWhiteListAdminEnableZeroThenArbitraryGBAndDisablePreserveOrdinaryRQLite(t *testing.T) {
	db, service, customer, entitlement, now := newWhiteListAdminRQLite(t)
	before := whiteListOrdinarySnapshot(t, db, customer)
	enable := SetWhiteListPublicationCommand{EntitlementID: entitlement, Enabled: true, IdempotencyKey: "enable-zero", Actor: "owner", Channel: "panel"}
	if _, err := service.SetWhiteListPublication(task7Context(t), enable); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.WhiteListBalanceSnapshot(task7Context(t), now, entitlement)
	if err != nil || snapshot.AvailableBytes != 0 || snapshot.UsableBytes != 0 || snapshot.PeriodEndsUnix != before.ExpiresAtUnix {
		t.Fatalf("zero enable changed allowance/access: %#v %v", snapshot, err)
	}
	period := task7Row(t, db, rqlite.Statement{SQL: `SELECT period.access_order_id,source.customer_id,source.customer_generation,source.customer_expires_at_unix FROM whitelist_billing_periods AS period JOIN whitelist_customer_access_sources AS source ON source.source_id=period.customer_access_source_id WHERE period.entitlement_id=?`, Args: []any{entitlement}})
	generation, ok := rowInt64(period, "customer_generation")
	if period["access_order_id"] != nil || period["customer_id"] != customer || !ok || generation != 7 {
		t.Fatalf("not actual customer authority: %#v", period)
	}
	meter, err := db.QueryLinearizable(task7Context(t), whiteListMeteringPeriodRead(entitlement, now))
	if err != nil || len(meter) != 1 || len(meter[0].Rows) != 1 {
		t.Fatalf("legitimate admin period absent from metering: %v", err)
	}
	command := CreditWhiteListManualGBCommand{EntitlementID: entitlement, GB: 137, IdempotencyKey: "arbitrary-137", Actor: "owner"}
	result, err := service.CreditWhiteListManualGB(task7Context(t), command)
	if err != nil || result.Bytes != 137*whitelistbalance.GBDecimal {
		t.Fatalf("credit: %#v %v", result, err)
	}
	disabled, err := service.SetWhiteListPublication(task7Context(t), SetWhiteListPublicationCommand{EntitlementID: entitlement, Enabled: false, IdempotencyKey: "disable", Actor: "owner", Channel: "panel"})
	if err != nil || disabled.Enabled {
		t.Fatalf("disable: %#v %v", disabled, err)
	}
	snapshot, err = service.WhiteListBalanceSnapshot(task7Context(t), now, entitlement)
	if err != nil || snapshot.AvailableBytes != result.Bytes || snapshot.Projection.FreshThroughUnix != 0 {
		t.Fatalf("disable lost GB or fabricated meter freshness: %#v %v", snapshot, err)
	}
	enable.IdempotencyKey = "enable-again"
	if _, err := service.SetWhiteListPublication(task7Context(t), enable); err != nil {
		t.Fatal(err)
	}
	expiredService := whiteListBalanceRQLiteService(t, db, now+86401, "expired")
	enable.IdempotencyKey = "expired-enable"
	if _, err := expiredService.SetWhiteListPublication(task7Context(t), enable); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired primary enabled: %v", err)
	}
	expired, err := expiredService.WhiteListBalanceSnapshot(task7Context(t), now+86401, entitlement)
	if err != nil || expired.AvailableBytes != result.Bytes || expired.UsableBytes != 0 {
		t.Fatalf("expiry lost stored GB: %#v %v", expired, err)
	}
	assertWhiteListOrdinaryUnchanged(t, db, customer, before)
	if whiteListCount(t, db, `SELECT COUNT(*) AS n FROM whitelist_manual_credits WHERE entitlement_id=?`, entitlement) != 1 {
		t.Fatal("wrong manual ledger count")
	}
	for _, statement := range []rqlite.Statement{
		{SQL: `UPDATE whitelist_manual_credits SET bytes=1000000000 WHERE entitlement_id=?`, Args: []any{entitlement}},
		{SQL: `DELETE FROM whitelist_manual_credits WHERE entitlement_id=?`, Args: []any{entitlement}},
		{SQL: `UPDATE whitelist_customer_access_sources SET customer_expires_at_unix=customer_expires_at_unix+1 WHERE entitlement_id=?`, Args: []any{entitlement}},
	} {
		if _, err := db.Request(task7Context(t), rqlite.Linearizable, true, statement); err == nil {
			t.Fatal("immutable administrative evidence mutated")
		}
	}
}

func TestWhiteListManualCreditWithoutEnableUnknownCommitExactlyOnceRQLite(t *testing.T) {
	db, _, customer, entitlement, now := newWhiteListAdminRQLite(t)
	before := whiteListOrdinarySnapshot(t, db, customer)
	faulty := &task7WriteFault{delegate: db, failAt: 1}
	service := whiteListBalanceRQLiteService(t, faulty, now, "unknown")
	command := CreditWhiteListManualGBCommand{EntitlementID: entitlement, GB: 3, IdempotencyKey: "unknown-credit", Actor: "owner"}
	first, err := service.CreditWhiteListManualGB(task7Context(t), command)
	if err != nil {
		t.Fatal(err)
	}
	restarted := whiteListBalanceRQLiteService(t, db, now+1, "restart")
	replay, err := restarted.CreditWhiteListManualGB(task7Context(t), command)
	if err != nil || replay != first {
		t.Fatalf("unknown replay mismatch: %#v %#v %v", first, replay, err)
	}
	command.GB = 4
	if _, err := restarted.CreditWhiteListManualGB(task7Context(t), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry accepted: %v", err)
	}
	projection := whiteListProjection(t, db, entitlement)
	publication, err := restarted.WhiteListPublicationState(task7Context(t), entitlement)
	if err != nil || publication.Enabled || projection.PurchasedRemaining != 3000000000 || projection.CurrentPeriodID != "" || projection.Version != 1 {
		t.Fatalf("credit implicitly enabled/created period: %#v %#v %v", projection, publication, err)
	}
	if whiteListCount(t, db, `SELECT COUNT(*) AS n FROM whitelist_manual_credits WHERE entitlement_id=?`, entitlement) != 1 {
		t.Fatal("replay credited twice")
	}
	assertWhiteListOrdinaryUnchanged(t, db, customer, before)
}

func TestWhiteListManualConcurrentCreditsRetrySameKeysRQLite(t *testing.T) {
	db, _, _, entitlement, now := newWhiteListAdminRQLite(t)
	commands := []CreditWhiteListManualGBCommand{{EntitlementID: entitlement, GB: 2, IdempotencyKey: "parallel-a", Actor: "owner"}, {EntitlementID: entitlement, GB: 5, IdempotencyKey: "parallel-b", Actor: "owner"}}
	services := []*Service{whiteListBalanceRQLiteService(t, db, now, "parallel-a"), whiteListBalanceRQLiteService(t, db, now, "parallel-b")}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = services[i].CreditWhiteListManualGB(context.Background(), commands[i])
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
		if _, err := services[i].CreditWhiteListManualGB(task7Context(t), commands[i]); err != nil {
			t.Fatalf("same-key retry: %v", err)
		}
	}
	projection := whiteListProjection(t, db, entitlement)
	if projection.PurchasedRemaining != 7000000000 || projection.Version != 2 || whiteListCount(t, db, `SELECT COUNT(*) AS n FROM whitelist_manual_credits WHERE entitlement_id=?`, entitlement) != 2 {
		t.Fatalf("lost/duplicate credit: %#v", projection)
	}
}
