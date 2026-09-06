package controlplane

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistbalance"
)

func TestWhiteListManualCreditRejectsInvalidWholeGBBeforeDatabase(t *testing.T) {
	db := &recordingRQLite{}
	service, _ := testService(t, db)
	for _, gb := range []int64{-1, 0, 9223372037, whitelistbalance.MaxExclusive} {
		_, err := service.CreditWhiteListManualGB(context.Background(), CreditWhiteListManualGBCommand{EntitlementID: "entitlement-1", IdempotencyKey: "invalid", Actor: "owner", GB: gb})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("GB=%d: %v", gb, err)
		}
	}
	if len(db.linearCalls) != 0 || len(db.requestCalls) != 0 {
		t.Fatal("invalid amount reached database")
	}
}

func TestWhiteListAdministrativeRetryMismatchIsNotDefinitiveRejection(t *testing.T) {
	db := &recordingRQLite{linear: []scriptedResult{
		rowsScript(map[string]any{"request_hash": "previous-request", "status": "applied"}),
		rowsScript(map[string]any{"request_hash": "previous-request", "resource_id": "entitlement", "status": "applied"}),
	}}
	service, _ := testService(t, db)
	_, found, err := service.resolveWhiteListAdministrativeBalance(context.Background(), "scope", "whitelist_manual_credit", "key", "new-request")
	if !found || !errors.Is(err, ErrConflict) || err.Error() == ErrConflict.Error() {
		t.Fatalf("manual retry conflict lost uncertainty: %v", err)
	}
	_, found, err = service.resolveWhiteListPublication(context.Background(), "scope", "key", "new-request", "entitlement")
	if !found || !errors.Is(err, ErrConflict) || err.Error() == ErrConflict.Error() {
		t.Fatalf("publication retry conflict lost uncertainty: %v", err)
	}
	if len(db.requestCalls) != 0 {
		t.Fatal("retry conflict performed mutation")
	}
}

func requireV19MigrationRequest(t *testing.T, call recordedCall, item migration) {
	t.Helper()
	if !call.transaction || call.level != rqlite.Linearizable || len(call.statements) != len(item.Statements)+1 {
		t.Fatal("v19 must be one complete transaction")
	}
	for i, statement := range item.Statements {
		if call.statements[i].SQL != statement.SQL {
			t.Fatalf("v19 statement %d changed", i)
		}
	}
	last := call.statements[len(call.statements)-1]
	if len(last.Args) != 3 || fmt.Sprint(last.Args[0]) != "19" || fmt.Sprint(last.Args[1]) != item.Checksum {
		t.Fatal("v19 receipt mismatch")
	}
}

func TestWhiteListAdminMigrationRetainsOrderAuthorityAndEnablesOnlyProvenCustomerSource(t *testing.T) {
	items, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(items[18].Data))
	for _, required := range []string{"pragma defer_foreign_keys=on", "whitelist_periods_v19_copy", "source_order.payment_state='confirmed'", "source_order.decision='confirmed'", "customer.generation=new.customer_generation", "customer.expires_at_unix=new.customer_expires_at_unix", "whitelist_manual_credits_immutable_update", "whitelist_manual_credits_immutable_delete", "pragma_foreign_key_check"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("missing %q", required)
		}
	}
	if strings.Contains(sql, "foreign_keys=off") || strings.Contains(sql, "writable_schema") {
		t.Fatal("migration disables integrity")
	}
}

func TestWhiteListAdminMigrationRestoresPopulatedPeriodAndChildrenSQLite(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal(err)
	}
	db := &customerIntegritySQLite{python: python, path: filepath.Join(t.TempDir(), "v18.sqlite")}
	items, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var prefix []rqlite.Statement
	for _, item := range items[:18] {
		prefix = append(prefix, item.Statements...)
	}
	db.must(t, prefix...)
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) VALUES('admin-migrate-customer','Example',?,'active',3000000,1,1,1)`, Args: []any{strings.Repeat("a", 64)}},
		rqlite.Statement{SQL: `INSERT INTO tariff_versions(tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix) VALUES('admin-migrate-tariff','test',30,1,'RUB',1,1)`},
		rqlite.Statement{SQL: `INSERT INTO orders(order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,provisioning_state,decision,confirmed_at_unix,operation_id) VALUES('admin-migrate-order','ADMINMIG','test',?,'admin-migrate-customer','admin-migrate-tariff',1,'RUB',30,1,3000000,'confirmed','applied','confirmed',1,'admin-migrate-operation')`, Args: []any{strings.Repeat("b", 64)}},
		rqlite.Statement{SQL: `INSERT INTO whitelist_entitlement_identities(entitlement_id,customer_id,created_at_unix) VALUES('wl-ent-00000000000000000000000000000019','admin-migrate-customer',1)`},
		rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,created_at_unix) VALUES('admin-migrate-period','wl-ent-00000000000000000000000000000019',0,1,3000000,0,'admin-migrate-order',1)`},
		rqlite.Statement{SQL: `INSERT INTO whitelist_balance_projections(entitlement_id,current_period_id,included_remaining_bytes,purchased_remaining_bytes,lifetime_consumed_bytes,uncovered_bytes,version,pending,fresh_through_unix,updated_at_unix) VALUES('wl-ent-00000000000000000000000000000019','admin-migrate-period',0,17,0,0,1,0,0,1)`})
	db.must(t, items[18].Statements...)
	rows, err := db.QueryLinearizable(context.Background(), rqlite.Statement{SQL: `SELECT p.access_order_id,b.purchased_remaining_bytes,p.customer_access_source_id FROM whitelist_billing_periods p JOIN whitelist_balance_projections b ON b.current_period_id=p.period_id`}, rqlite.Statement{SQL: `PRAGMA foreign_key_check`})
	if err != nil || len(rows) != 2 || len(rows[0].Rows) != 1 || len(rows[1].Rows) != 0 {
		t.Fatalf("lost parent/child or FK integrity: %v %#v", err, rows)
	}
	row := rows[0].Rows[0]
	amount, ok := rowInt64(row, "purchased_remaining_bytes")
	if row["access_order_id"] != "admin-migrate-order" || row["customer_access_source_id"] != nil || !ok || amount != 17 {
		t.Fatalf("old row changed: %#v", row)
	}
	for _, sql := range []string{`DELETE FROM whitelist_billing_periods WHERE period_id='admin-migrate-period'`, `UPDATE whitelist_billing_periods SET ends_at_unix=4000000 WHERE period_id='admin-migrate-period'`} {
		if _, err := db.Request(context.Background(), rqlite.Linearizable, true, rqlite.Statement{SQL: sql}); err == nil {
			t.Fatal("period became mutable")
		}
	}
}

func TestWhiteListManualCreditActualSQLCASRollbackAndOverflow(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	customer := seedIntegrityCustomer(t, service)
	entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := CreditWhiteListManualGBCommand{EntitlementID: entitlement.EntitlementID(), GB: 3, IdempotencyKey: "first", Actor: "owner"}
	competing := first
	competing.GB = 2
	competing.IdempotencyKey = "competing"
	db.beforeRequest = func() {
		if _, err := service.CreditWhiteListManualGB(context.Background(), competing); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreditWhiteListManualGB(context.Background(), first); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale projection did not roll back: %v", err)
	}
	if _, err := service.CreditWhiteListManualGB(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.WhiteListBalanceSnapshot(context.Background(), service.clock.Now().Unix(), entitlement.EntitlementID())
	if err != nil || snapshot.AvailableBytes != 5*whitelistbalance.GBDecimal || snapshot.Projection.Version != 2 {
		t.Fatalf("lost or duplicated credit: %#v %v", snapshot, err)
	}
	overflow := first
	overflow.GB = 9223372036
	overflow.IdempotencyKey = "overflow"
	if _, err := service.CreditWhiteListManualGB(context.Background(), overflow); !errors.Is(err, ErrConflict) {
		t.Fatalf("overflow: %v", err)
	}
	rows, err := db.QueryLinearizable(context.Background(), rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM whitelist_manual_credits WHERE entitlement_id=?`, Args: []any{entitlement.EntitlementID()}}, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM idempotency_requests WHERE command_type='whitelist_manual_credit' AND resource_id=?`, Args: []any{entitlement.EntitlementID()}})
	if err != nil || len(rows) != 2 {
		t.Fatal(err)
	}
	for _, result := range rows {
		count, ok := rowInt64(result.Rows[0], "n")
		if !ok || count != 2 {
			t.Fatal("rollback left separate credit/request residue")
		}
	}
}

func TestWhiteListAdminEnablePreservesFuturePeriodBeforeAndDuringWriteSQLite(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(fmt.Sprint("race-", race), func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			customer := seedIntegrityCustomer(t, service)
			entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), customer.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.CreditWhiteListManualGB(context.Background(), CreditWhiteListManualGBCommand{EntitlementID: entitlement.EntitlementID(), GB: 3, IdempotencyKey: "retain-credit", Actor: "owner"}); err != nil {
				t.Fatal(err)
			}
			now := service.clock.Now().Unix()
			db.must(t,
				rqlite.Statement{SQL: `INSERT INTO tariff_versions(tariff_version_id,tariff_code,duration_days,amount_minor,currency,active,created_at_unix) VALUES('future-tariff','future',30,1,'RUB',1,1)`},
				rqlite.Statement{SQL: `INSERT INTO orders(order_id,payment_code,buyer_scope,buyer_key_hmac,customer_id,tariff_version_id,amount_minor,currency,duration_days,created_at_unix,expires_at_unix,payment_state,provisioning_state,decision,confirmed_at_unix,operation_id) VALUES('future-order','FUTURE','test',?,?,'future-tariff',1,'RUB',30,1,3000000,'confirmed','applied','confirmed',1,'future-operation')`, Args: []any{strings.Repeat("b", 64), customer.ID}})
			insertFuture := func() {
				db.must(t, rqlite.Statement{SQL: `INSERT INTO whitelist_billing_periods(period_id,entitlement_id,period_ordinal,starts_at_unix,ends_at_unix,included_grant_bytes,access_order_id,created_at_unix) VALUES('future-period',?,0,?,?,0,'future-order',?)`, Args: []any{entitlement.EntitlementID(), now + 100, now + 1000, now}})
			}
			if race {
				db.beforeRequest = insertFuture
			} else {
				insertFuture()
			}
			_, err = service.SetWhiteListPublication(context.Background(), SetWhiteListPublicationCommand{EntitlementID: entitlement.EntitlementID(), Enabled: true, IdempotencyKey: "enable", Actor: "owner"})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("future period not rejected: %v", err)
			}
			snapshot, err := service.WhiteListBalanceSnapshot(context.Background(), now, entitlement.EntitlementID())
			if err != nil || snapshot.AvailableBytes != 3*whitelistbalance.GBDecimal || snapshot.Projection.Version != 1 {
				t.Fatalf("future-period denial corrupted state: %#v %v", snapshot, err)
			}
			rows, err := db.QueryLinearizable(context.Background(),
				rqlite.Statement{SQL: `SELECT period_id FROM whitelist_billing_periods WHERE entitlement_id=?`, Args: []any{entitlement.EntitlementID()}},
				rqlite.Statement{SQL: `SELECT source_id FROM whitelist_customer_access_sources WHERE entitlement_id=?`, Args: []any{entitlement.EntitlementID()}},
				rqlite.Statement{SQL: `SELECT idempotency_key FROM idempotency_requests WHERE resource_id=? AND command_type IN ('whitelist_admin_access','whitelist_publication_set')`, Args: []any{entitlement.EntitlementID()}})
			if err != nil || len(rows) != 3 || len(rows[0].Rows) != 1 || rows[0].Rows[0]["period_id"] != "future-period" || len(rows[1].Rows) != 0 || len(rows[2].Rows) != 0 {
				t.Fatal("future-period guard left partial authority/receipt")
			}
		})
	}
}
