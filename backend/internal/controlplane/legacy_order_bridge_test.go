package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func seedNativeLegacyOrder(t *testing.T, db *customerIntegritySQLite, service *Service, order legacyorder.Order, customer *Customer) {
	t.Helper()
	box := service.store.secrets
	row, _ := json.Marshal(order)
	source, _ := json.Marshal([]legacyorder.Order{order})
	record := LegacyOrderRecord{SchemaVersion: 1, OrderKeyHMAC: box.LookupHMAC(LegacyOrderLookupDomain, []byte(order.ID)), OrdersSHA256: LegacyOrderDigest(source), CustomersSHA256: LegacyOrderDigest([]byte("fixture source")), SourceRow: row, Revision: 1}
	var customerID any
	if customer != nil {
		canonical, _ := CanonicalLoginKey(order.Login)
		record.CustomerID = customer.ID
		record.CustomerLoginHMAC = box.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte(order.Login))
		record.CustomerSourceKey = LegacyExactCustomerSourcePrefix + box.LookupHMAC("customer-login", []byte(canonical)) + ":" + record.CustomerLoginHMAC
		record.CustomerTokenHMAC = box.LookupHMAC("subscription-token", []byte(customer.Access.SubscriptionToken))
		customerID = customer.ID
	}
	if _, err := ValidateLegacyOrderRecord(box, record); err != nil {
		t.Fatal("invalid source fixture")
	}
	plain, _ := json.Marshal(record)
	sha := LegacyOrderDigest(plain)
	scope := LegacyOrderRecordScope(record.OrderKeyHMAC)
	envelope, err := box.Seal(scope, plain)
	if err != nil {
		t.Fatal(err)
	}
	secret := legacyOrderSecret{SecretID: LegacyOrderRecordID(record.OrderKeyHMAC, sha), OwnerType: scope.OwnerType, OwnerSourceKey: scope.OwnerID, Field: scope.Field, Kind: scope.Kind, KeyVersion: envelope.KeyVersion, NonceB64: base64.StdEncoding.EncodeToString(envelope.Nonce), CiphertextB64: base64.StdEncoding.EncodeToString(envelope.Ciphertext), SHA256: sha}
	encoded, _ := json.Marshal(secret)
	historical := 0
	if order.Status == "paid" || order.Credited {
		historical = 1
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_secrets(secret_id,owner_type,owner_source_key,field,kind,key_version,secret_envelope,secret_sha256,imported_at_unix) VALUES(?,?,?,?,?,?,?,?,2000000)`, Args: []any{secret.SecretID, secret.OwnerType, secret.OwnerSourceKey, secret.Field, secret.Kind, secret.KeyVersion, string(encoded), sha}},
		rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('encrypted_secret',?,?,?,'active',2000000)`, Args: []any{secret.SecretID, secret.SecretID, LegacyOrderDigest(encoded)}},
		rqlite.Statement{SQL: `INSERT INTO imported_legacy_order_aliases(order_key_hmac,record_secret_id,record_sha256,source_sha256,source_revision,customer_id,historical_grant,imported_at_unix) VALUES(?,?,?,?,1,?,?,2000000)`, Args: []any{record.OrderKeyHMAC, secret.SecretID, sha, record.OrdersSHA256, customerID, historical}})
}

func TestLegacyPaidHistoryIncludingOrphanNeverGrantsSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedExactLoginCustomer(t, db, service, "OrderCustomer")
	present := testLegacyRawOrder("ord_history", "OrderCustomer", customer.Access.SubscriptionToken, "paid")
	orphan := testLegacyRawOrder("ord_orphan", "OriginalMissingCustomer", "retained-orphan-token", "paid")
	seedNativeLegacyOrder(t, db, service, present, &customer)
	seedNativeLegacyOrder(t, db, service, orphan, nil)
	before := db.snapshot(t)
	for _, order := range []legacyorder.Order{present, orphan} {
		view, err := service.LegacyOrderByID(ctx, order.ID)
		if err != nil || view.Order != order || !view.HistoricalGrant || view.InternalOrderID != "" {
			t.Fatal("raw paid history was not preserved")
		}
		confirmed, result, err := service.ConfirmLegacyOrder(ctx, order.ID, "owner")
		if err != nil || confirmed.Order != order || result != (ConfirmPaymentResult{}) {
			t.Fatal("historical confirmation invented a grant")
		}
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("paid history confirmation mutated the service")
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT order_id FROM orders`}, rqlite.Statement{SQL: `SELECT payment_id FROM payments`})
	if len(rows[0].Rows) != 0 || len(rows[1].Rows) != 0 {
		t.Fatal("historical orders became grant-capable")
	}
}

func TestLegacyPendingAcceptanceRestartsAndGrantsOnceWithOriginalIdentitySQLite(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "suspended"}[suspended], func(t *testing.T) {
			db, service := newCustomerIntegritySQLite(t)
			ctx := context.Background()
			customer := seedExactLoginCustomer(t, db, service, "OrderCustomer")
			if suspended {
				db.must(t, rqlite.Statement{SQL: `UPDATE customers SET status='suspended' WHERE customer_id=?`, Args: []any{customer.ID}}, rqlite.Statement{SQL: `UPDATE subscription_tokens SET revoked=1,revoked_at_unix=1900000 WHERE customer_id=?`, Args: []any{customer.ID}}, rqlite.Statement{SQL: `UPDATE credentials SET enabled=0 WHERE customer_id=?`, Args: []any{customer.ID}})
			}
			order := testLegacyRawOrder("ord_pending", "OrderCustomer", "", "pending")
			seedNativeLegacyOrder(t, db, service, order, &customer)
			view, err := service.LegacyOrderByID(ctx, order.ID)
			if err != nil || view.Order != order {
				t.Fatal("old pending order expired or changed")
			}
			slot, err := service.loadLegacyOrder(ctx, order.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.acceptLegacyOrder(ctx, slot, "owner"); err != nil {
				t.Fatal(err)
			}
			// A fresh service resumes acceptance already committed before a restart.
			restarted, err := NewService(service.store, &sequenceIDs{}, service.clock)
			if err != nil {
				t.Fatal(err)
			}
			_, result, err := restarted.ConfirmLegacyOrder(ctx, order.ID, "owner")
			if err != nil || result.ExpiresAtUnix != customer.ExpiresAtUnix+30*86400 || result.Generation != 2 {
				t.Fatalf("ordinary renewal failed: %v", err)
			}
			_, again, err := restarted.ConfirmLegacyOrder(ctx, order.ID, "owner")
			if err != nil || again != result {
				t.Fatal("repeat confirmation changed the grant")
			}
			access, err := restarted.customerAccess(ctx, customer.ID)
			if err != nil || !reflect.DeepEqual(access, customer.Access) {
				t.Fatal("renewal replaced original token or protocol credentials")
			}
			rows := db.must(t, rqlite.Statement{SQL: `SELECT o.order_id,o.created_at_unix,o.amount_minor,t.active,a.accepted_at_unix FROM orders o JOIN tariff_versions t ON t.tariff_version_id=o.tariff_version_id JOIN imported_legacy_order_aliases a ON a.accepted_order_id=o.order_id`}, rqlite.Statement{SQL: `SELECT payment_id FROM payments`})
			if len(rows[0].Rows) != 1 || len(rows[1].Rows) != 1 {
				t.Fatal("duplicate durable order or payment")
			}
			created, _ := rowInt64(rows[0].Rows[0], "created_at_unix")
			accepted, _ := rowInt64(rows[0].Rows[0], "accepted_at_unix")
			amount, _ := rowInt64(rows[0].Rows[0], "amount_minor")
			active, _ := rowInt64(rows[0].Rows[0], "active")
			if created != 2_000_000 || accepted != created || created == order.CreatedAt.Unix() || amount != 30000 || active != 0 {
				t.Fatal("source date or historical price was rewritten")
			}
			menu := db.must(t, rqlite.Statement{SQL: `SELECT amount_minor,active FROM tariff_versions WHERE tariff_version_id='tariff_1m_v1'`})
			menuAmount, _ := rowInt64(menu[0].Rows[0], "amount_minor")
			menuActive, _ := rowInt64(menu[0].Rows[0], "active")
			if menuAmount != 40000 || menuActive != 1 {
				t.Fatal("historical order changed current default tariff")
			}
		})
	}
}

func TestLegacyPendingCancellationWinsAcceptanceRaceSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedExactLoginCustomer(t, db, service, "OrderCustomer")
	order := testLegacyRawOrder("ord_race", "OrderCustomer", "", "pending")
	seedNativeLegacyOrder(t, db, service, order, &customer)
	slot, err := service.loadLegacyOrder(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	db.beforeRequest = func() {
		db.must(t, rqlite.Statement{SQL: `UPDATE imported_legacy_order_aliases SET cancelled_at_unix=2000000 WHERE order_key_hmac=?`, Args: []any{slot.record.OrderKeyHMAC}})
	}
	if err := service.acceptLegacyOrder(ctx, slot, "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled order accepted: %v", err)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT order_id FROM orders`}, rqlite.Statement{SQL: `SELECT payment_id FROM payments`})
	if len(rows[0].Rows) != 0 || len(rows[1].Rows) != 0 {
		t.Fatal("losing decision created a grant")
	}
}
