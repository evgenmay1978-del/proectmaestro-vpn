package controlplane

import (
	"strings"
	"testing"
)

func TestMigrationWhiteListTopUpOrdersIsV14AndKeepsLegacyOrdersAsAnchor(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 18 || len(migrations) != 18 {
		t.Fatalf("schema chain = version %d/%d, want exact v18", SchemaVersion, len(migrations))
	}
	if migrations[12].Version != 13 || migrations[12].Path != "migrations/0013_whitelist_commercial_debit_outbox.sql" {
		t.Fatalf("immutable v13 migration moved: %#v", migrations[12])
	}
	v14 := migrations[13]
	if v14.Version != 14 || v14.Path != "migrations/0014_whitelist_topup_orders.sql" {
		t.Fatalf("immutable v14 migration moved: %#v", v14)
	}

	sql := strings.ToLower(string(v14.Data))
	for _, required := range []string{
		"create table whitelist_gb_products",
		"product_id text primary key not null",
		"bytes integer not null",
		"unit text not null check(unit = 'gb_decimal')",
		"kind text not null check(kind = 'whitelist_bytes')",
		"foreign key(product_id) references tariff_versions",
		"create table whitelist_topup_orders",
		"foreign key(order_id) references orders",
		"foreign key(entitlement_id) references whitelist_entitlement_identities",
		"foreign key(product_id) references whitelist_gb_products",
		"create table whitelist_topup_payment_claims",
		"create table whitelist_publication_controls",
		"create table whitelist_renewal_intents",
		"target_generation integer not null",
		"confirmed_at_unix integer not null",
		"target_ends_at_unix integer not null",
		"whitelist_renewal_intents_exact_binding",
		"whitelist_renewal_intents_applied_binding",
		"whitelist_renewal_intents_immutable_update",
		"whitelist_renewal_intents_immutable_delete",
		"source text not null check(source in ('default_off','confirmed_gb_purchase','admin_enable','admin_disable'))",
		"unique(entitlement_id, version)",
		"create table whitelist_topup_results",
		"payment_reference_hmac text unique",
		"foreign key(period_id) references whitelist_billing_periods",
		"foreign key(balance_entry_id) references whitelist_balance_entries",
		"foreign key(control_id) references whitelist_publication_controls",
		"wl-gb-5-v1",
		"5000000000",
		"wl-gb-20-v1",
		"20000000000",
		"wl-gb-50-v1",
		"50000000000",
		"wl-gb-100-v1",
		"100000000000",
		"whitelist_topup_orders_block_legacy_decision",
		"new.command_type in ('confirm','cancel')",
		"whitelist_topup_orders_payment_transition",
		"new.command_type = 'whitelist_topup_confirm'",
		"new.command_type = 'whitelist_topup_reject'",
		"whitelist_publication_controls_default_existing",
		"whitelist_publication_controls_default_new",
		"whitelist_topup_idempotency_applied_guard",
		"before update on whitelist_gb_products",
		"before delete on whitelist_gb_products",
		"before update on whitelist_topup_orders",
		"before delete on whitelist_topup_orders",
		"before update on whitelist_topup_payment_claims",
		"before delete on whitelist_topup_payment_claims",
		"before update on whitelist_publication_controls",
		"before delete on whitelist_publication_controls",
		"before update on whitelist_topup_results",
		"before delete on whitelist_topup_results",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v14 missing %q", required)
		}
	}
	if strings.Contains(sql, "drop table") || strings.Contains(sql, "alter table") {
		t.Fatal("v14 must preserve existing tables")
	}
}

func TestMigrationWhiteListStoredTriggersAvoidRQLiteClockRewrite(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations[13].Statements {
		identity := schemaSQLIdentity(statement.SQL)
		if !strings.HasPrefix(identity, "create trigger ") {
			continue
		}
		if strings.Contains(identity, "'now'") || strings.Contains(identity, "unixepoch()") {
			t.Fatalf("stored v14 trigger contains a clock expression rewritten by rqlite: %s", identity)
		}
	}
	defaultTrigger := expectedWhiteListCommercialMeteringTriggerRows(t, migrations[11], migrations[12], migrations[13])
	for _, row := range defaultTrigger {
		if row["name"] == "whitelist_publication_controls_default_new" &&
			!strings.Contains(row["sql"].(string), "new.created_at_unix") {
			t.Fatal("new entitlement publication control does not inherit its durable creation time")
		}
	}
}

func TestMigrationWhiteListRenewalIntentRequiresExactOrdinaryOrderAndAppliedProjection(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.Join(strings.Fields(strings.ToLower(string(migrations[13].Data))), " ")
	for _, required := range []string{
		"foreign key(access_order_id) references orders",
		"foreign key(entitlement_id) references whitelist_entitlement_identities",
		"foreign key(period_id) references whitelist_billing_periods",
		"unique(entitlement_id, target_generation)",
		"source_order.result_generation = new.target_generation",
		"source_order.result_expires_at_unix = new.target_ends_at_unix",
		"not exists ( select 1 from whitelist_topup_orders",
		"period.included_grant_bytes = 0",
		"period.access_order_id = new.access_order_id",
		"projection.version = new.projection_version",
		"old.status = 'pending' and new.status = 'applied'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v14 renewal intent guard missing %q", required)
		}
	}
}

func TestMigrationWhiteListTopUpCatalogIsExactAndHidden(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	compact := strings.Join(strings.Fields(strings.ToLower(string(migrations[13].Data))), "")
	for _, exact := range []string{
		"('wl-gb-5-v1','wl-gb-5',1,10000,'rub',0,0)",
		"('wl-gb-20-v1','wl-gb-20',1,30000,'rub',0,0)",
		"('wl-gb-50-v1','wl-gb-50',1,60000,'rub',0,0)",
		"('wl-gb-100-v1','wl-gb-100',1,100000,'rub',0,0)",
		"('wl-gb-5-v1',5000000000,'gb_decimal','whitelist_bytes',0)",
		"('wl-gb-20-v1',20000000000,'gb_decimal','whitelist_bytes',0)",
		"('wl-gb-50-v1',50000000000,'gb_decimal','whitelist_bytes',0)",
		"('wl-gb-100-v1',100000000000,'gb_decimal','whitelist_bytes',0)",
	} {
		if !strings.Contains(compact, exact) {
			t.Fatalf("v14 catalog missing exact frozen tuple %q", exact)
		}
	}
}

func TestMigrationWhiteListPurchasePublicationRequiresCompletedTopUp(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var triggerSQL string
	for _, statement := range migrations[13].Statements {
		identity := schemaSQLIdentity(statement.SQL)
		if strings.HasPrefix(identity, "create trigger whitelist_publication_controls_purchase_owner ") {
			triggerSQL = identity
			break
		}
	}
	if triggerSQL == "" {
		t.Fatal("v14 purchase publication guard is missing")
	}
	for _, required := range []string{
		"join orders as source_order",
		"join payments as payment",
		"join whitelist_balance_entries as entry",
		"join idempotency_requests as request",
		"source_order.payment_state = 'confirmed'",
		"source_order.decision = 'confirmed'",
		"entry.kind = 'purchased_credit'",
		"request.command_type = 'whitelist_topup_confirm'",
		"request.status = 'applying'",
	} {
		if !strings.Contains(triggerSQL, required) {
			t.Fatalf("purchase publication guard missing %q", required)
		}
	}
}

func TestMigrationWhiteListPublicationSetAppliedGuardRequiresExactControl(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var triggerSQL string
	for _, statement := range migrations[13].Statements {
		identity := schemaSQLIdentity(statement.SQL)
		if strings.HasPrefix(identity, "create trigger whitelist_topup_idempotency_applied_guard ") {
			triggerSQL = identity
			break
		}
	}
	for _, required := range []string{
		"new.command_type = 'whitelist_publication_set'",
		"control.operation_id = new.operation_id",
		"control.request_hash = new.request_hash",
		"json_extract(new.response_json,'$.control_id') = control.control_id",
		"json_extract(new.response_json,'$.version') = control.version",
	} {
		if !strings.Contains(triggerSQL, required) {
			t.Fatalf("publication applied guard missing %q", required)
		}
	}
}
