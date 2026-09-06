package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

// Actual migrations, constraints and sealed access are used here. The synthetic
// source row digest represents the binding written by the validated importer.
func seedExactLoginCustomer(t *testing.T, db *customerIntegritySQLite, service *Service, login string) Customer {
	t.Helper()
	canonical, err := CanonicalLoginKey(login)
	if err != nil {
		t.Fatal(err)
	}
	box := service.store.secrets
	canonicalHMAC := box.LookupHMAC("customer-login", []byte(canonical))
	exactHMAC := box.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte(login))
	source := LegacyExactCustomerSourcePrefix + canonicalHMAC + ":" + exactHMAC
	mapped := sha256.Sum256([]byte("maestro-legacy-v1\x00customer\x00" + source))
	customer := Customer{ID: hex.EncodeToString(mapped[:]), Status: "active", ExpiresAtUnix: 3_000_000, Generation: 1}
	access, err := service.mintCustomerAccess(customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	customer.Access = access.Access
	now := service.clock.Now().Unix()
	digest := sha256.Sum256([]byte("fixture-authenticated-customer-row:" + login))
	statements := []rqlite.Statement{{
		SQL:  `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) VALUES(?,?,?,'active',?,1,?,?)`,
		Args: []any{customer.ID, login, exactHMAC, customer.ExpiresAtUnix, now, now},
	}, {
		SQL:  `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('customer',?,?,?,'active',?)`,
		Args: []any{source, customer.ID, hex.EncodeToString(digest[:]), now},
	}}
	statements = append(statements, access.statements(customer, now, "1=1", nil, box)...)
	db.must(t, statements...)
	return customer
}

func TestExactLoginSourceParser(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	source := LegacyExactCustomerSourcePrefix + a + ":" + b
	if gotA, gotB, ok := ParseLegacyExactCustomerSource(source); !ok || gotA != a || gotB != b {
		t.Fatal("valid source rejected")
	}
	for _, bad := range []string{source + ":", strings.Replace(source, a, strings.ToUpper(a), 1), LegacyExactCustomerSourcePrefix + a, "s1:customer:" + a, LegacyExactCustomerSourcePrefix + a + ":" + strings.Repeat("g", 64)} {
		if _, _, ok := ParseLegacyExactCustomerSource(bad); ok {
			t.Fatal("invalid source accepted")
		}
	}
}

func TestExactLoginCollisionPreservesLookupAccessAndMutationScopesSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	upper := seedExactLoginCustomer(t, db, service, "Alice")
	lower := seedExactLoginCustomer(t, db, service, "alice")
	if upper.ID == lower.ID || upper.Access.SubscriptionToken == lower.Access.SubscriptionToken || upper.Access.Credentials["vless"] == lower.Access.Credentials["vless"] {
		t.Fatal("fixture identities collapsed")
	}
	var identities []CustomerLoginIdentity
	for index, raw := range []string{"Alice", "alice"} {
		want := []Customer{upper, lower}[index]
		identity, err := service.ResolveCustomerLogin(ctx, raw)
		if err != nil || !identity.Exists() || !identity.ExactLegacy() || identity.Login() != raw || identity.CustomerID() != want.ID {
			t.Fatalf("exact resolution: %v", err)
		}
		identities = append(identities, identity)
		view, err := service.BusinessCustomerByLogin(ctx, raw)
		if err != nil || view.ID != want.ID || view.Login != raw || !reflect.DeepEqual(view.Access, want.Access) {
			t.Fatalf("exact business lookup: %v", err)
		}
		byToken, err := service.CustomerByToken(ctx, want.Access.SubscriptionToken)
		if err != nil || byToken.ID != want.ID {
			t.Fatalf("token identity changed: %v", err)
		}
		command := ExtendCustomerCommand{Login: raw, Days: 2, IdempotencyKey: "same-request-key"}
		first, err := service.ExtendCustomer(ctx, command)
		if err != nil || first.ID != want.ID || first.Generation != 2 || first.ExpiresAtUnix != want.ExpiresAtUnix+2*86400 || !reflect.DeepEqual(first.Access, want.Access) {
			t.Fatalf("separate mutation: %v", err)
		}
		replayed, err := service.ExtendCustomer(ctx, command)
		if err != nil || !reflect.DeepEqual(first, replayed) {
			t.Fatalf("exact retry: %v", err)
		}
	}
	if identities[0].LookupHMAC() == identities[1].LookupHMAC() || identities[0].SettingMemberHMAC("olcrtc") == identities[1].SettingMemberHMAC("olcrtc") || identities[0].PurchaseIdentityHMAC() == identities[1].PurchaseIdentityHMAC() {
		t.Fatal("identity-derived scope collapsed")
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT scope FROM idempotency_requests WHERE command_type='customer.extend' ORDER BY scope`})
	if len(rows[0].Rows) != 2 {
		t.Fatal("expected two independent idempotency receipts")
	}
	before := db.snapshot(t)
	for _, unknown := range []string{"ALICE", " Alice ", "aLiCe"} {
		if _, err := service.CustomerByLogin(ctx, unknown); !errors.Is(err, ErrConflict) {
			t.Fatalf("unknown family casing: %v", err)
		}
		if _, err := service.ProvisionCustomer(ctx, ProvisionCustomerCommand{Login: unknown, Days: 1, IdempotencyKey: "must-not-create"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("family create: %v", err)
		}
		if _, err := service.PurchaseOrderLoginIdentityHMAC(ctx, unknown); !errors.Is(err, ErrConflict) {
			t.Fatalf("unknown purchase identity: %v", err)
		}
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("unknown casing changed durable state")
	}
}

func TestExactLoginAllAdministrativeMutationsKeepSiblingSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	upper := seedExactLoginCustomer(t, db, service, "Alice")
	lower := seedExactLoginCustomer(t, db, service, "alice")
	calls := []func() error{
		func() error {
			_, err := service.RenewCustomer(ctx, RenewCustomerCommand{Login: "Alice", Days: 1, IdempotencyKey: "renew"})
			return err
		},
		func() error {
			_, err := service.SetCustomerExpiry(ctx, SetExpiryCommand{Login: "Alice", ExpiresAt: time.Unix(4_000_000, 0), IdempotencyKey: "expiry"})
			return err
		},
		func() error {
			_, err := service.DisableCustomer(ctx, CustomerStateCommand{Login: "Alice", IdempotencyKey: "disable"})
			return err
		},
		func() error {
			_, err := service.EnableCustomer(ctx, CustomerStateCommand{Login: "Alice", IdempotencyKey: "enable"})
			return err
		},
		func() error {
			return service.ResetDevices(ctx, ResetDevicesCommand{Login: "Alice", IdempotencyKey: "reset"})
		},
		func() error {
			return service.DeleteCustomer(ctx, DeleteCustomerCommand{Login: "Alice", IdempotencyKey: "delete"})
		},
	}
	for _, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("exact admin mutation: %v", err)
		}
		access, err := service.customerAccess(ctx, upper.ID)
		if err != nil || !reflect.DeepEqual(access, upper.Access) {
			t.Fatalf("exact access changed: %v", err)
		}
		unchanged, err := service.BusinessCustomerByLogin(ctx, "alice")
		if err != nil || unchanged.Customer.ID != lower.ID || unchanged.Generation != lower.Generation || unchanged.ExpiresAtUnix != lower.ExpiresAtUnix || !reflect.DeepEqual(unchanged.Access, lower.Access) {
			t.Fatalf("sibling changed: %v", err)
		}
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT display_login FROM customers WHERE customer_id=?`, Args: []any{upper.ID}})
	if rows[0].Rows[0]["display_login"] != "Alice" {
		t.Fatal("original login changed")
	}
}

func TestExactLoginProofDriftAndMissingBindingFailClosedSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	upper := seedExactLoginCustomer(t, db, service, "Alice")
	before := db.snapshot(t)
	db.beforeRequest = func() {
		db.must(t, rqlite.Statement{SQL: `UPDATE imported_entity_state SET canonical_sha256=? WHERE entity_kind='customer' AND target_id=?`, Args: []any{strings.Repeat("e", 64), upper.ID}})
	}
	if _, err := service.ExtendCustomer(ctx, ExtendCustomerCommand{Login: "Alice", Days: 1, IdempotencyKey: "proof-race"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("proof drift: %v", err)
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("proof race changed business state")
	}
	// A row at the exact HMAC without its imported state cannot become ordinary.
	box := service.store.secrets
	missingKey := box.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte("Missing"))
	db.must(t, rqlite.Statement{SQL: `INSERT INTO customers(customer_id,display_login,login_key_hmac,status,expires_at_unix,generation,created_at_unix,updated_at_unix) VALUES('missing-proof','Missing',?,'active',3000000,1,2000000,2000000)`, Args: []any{missingKey}})
	if _, err := service.ResolveCustomerLogin(ctx, "Missing"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unbound exact identity: %v", err)
	}
	missingCanonical := box.LookupHMAC("customer-login", []byte("missing"))
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('customer',?,'missing-proof',?,'active',2000000)`, Args: []any{LegacyExactCustomerSourcePrefix + missingCanonical + ":" + missingKey, strings.Repeat("a", 64)}})
	if _, err := service.ResolveCustomerLogin(ctx, "Missing"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("altered mapped target accepted: %v", err)
	}
	db.must(t, rqlite.Statement{SQL: `UPDATE imported_entity_state SET lifecycle='deleted' WHERE entity_kind='customer' AND target_id=?`, Args: []any{upper.ID}})
	if _, err := service.EnableCustomer(ctx, CustomerStateCommand{Login: "Alice", IdempotencyKey: "no-resurrection"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted import enabled: %v", err)
	}
	if _, err := service.ProvisionCustomer(ctx, ProvisionCustomerCommand{Login: "ALICE", Days: 1, IdempotencyKey: "no-family-reuse"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted family reused: %v", err)
	}
}

func TestExactLoginImportTombstoneAllowsOnlySavedMutationReplaySQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedExactLoginCustomer(t, db, service, "Alice")
	command := ExtendCustomerCommand{Login: "Alice", Days: 1, IdempotencyKey: "already-applied"}
	first, err := service.ExtendCustomer(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	db.must(t, rqlite.Statement{SQL: `UPDATE imported_entity_state SET lifecycle='deleted' WHERE entity_kind='customer' AND target_id=?`, Args: []any{customer.ID}})
	before := db.snapshot(t)
	if replay, err := service.ExtendCustomer(ctx, command); err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("saved tombstone retry: %v", err)
	}
	command.IdempotencyKey = "new-operation"
	if _, err := service.ExtendCustomer(ctx, command); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tombstone new mutation: %v", err)
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) {
		t.Fatal("tombstone retry mutated state")
	}
}

func TestCanonicalLoginCompatibilityAndRevokedTokenSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	customer := seedIntegrityCustomer(t, service)
	canonicalHMAC := service.store.secrets.LookupHMAC("customer-login", []byte("existing"))
	db.must(t, rqlite.Statement{SQL: `INSERT INTO imported_entity_state(entity_kind,source_key,target_id,canonical_sha256,lifecycle,updated_at_unix) VALUES('customer',?,?,?,'active',2000000)`, Args: []any{"s1:customer:" + canonicalHMAC, customer.ID, strings.Repeat("c", 64)}})
	identity, err := service.ResolveCustomerLogin(ctx, " EXISTING ")
	if err != nil || identity.ExactLegacy() || identity.Login() != "existing" || identity.CustomerID() != customer.ID {
		t.Fatalf("canonical compatibility: %v", err)
	}
	oldHash, _ := service.PurchaseOrderIdentityHMAC(PurchaseOrderIdentityLogin, " EXISTING ")
	newHash, err := service.PurchaseOrderLoginIdentityHMAC(ctx, " EXISTING ")
	if err != nil || oldHash != newHash {
		t.Fatal("canonical purchase fingerprint changed")
	}
	db.must(t, rqlite.Statement{SQL: `UPDATE subscription_tokens SET revoked=1 WHERE customer_id=?`, Args: []any{customer.ID}})
	if _, err := service.CustomerByLogin(ctx, "Existing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token login: %v", err)
	}
	identity, err = service.ResolveCustomerLogin(ctx, "existing")
	if err != nil || !identity.Exists() {
		t.Fatalf("mutation identity incorrectly requires token: %v", err)
	}
	seedExactLoginCustomer(t, db, service, "EXISTING")
	if _, err := service.ResolveCustomerLogin(ctx, "Existing"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mixed canonical/exact family accepted: %v", err)
	}
}

func TestExactLoginPurchaseProofAndReplaySQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	ctx := context.Background()
	upper := seedExactLoginCustomer(t, db, service, "Alice")
	lower := seedExactLoginCustomer(t, db, service, "alice")
	upperIdentity, err := service.ResolveCustomerLogin(ctx, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	command := PurchaseOrderCommand{TariffVersionID: "tariff_1m_v1", CustomerID: upper.ID, IdempotencyKey: "purchase-exact", IdentitySource: PurchaseOrderIdentityLogin, IdentityHMAC: upperIdentity.PurchaseIdentityHMAC(), LoginIdentity: &upperIdentity, Actor: "fixture", Channel: "fixture"}
	first, err := service.CreatePurchaseOrder(ctx, command)
	if err != nil {
		t.Fatalf("exact purchase: %v", err)
	}
	if replay, err := service.CreatePurchaseOrder(ctx, command); err != nil || replay.OrderID != first.OrderID {
		t.Fatalf("exact purchase retry: %v", err)
	}
	lowerIdentity, err := service.ResolveCustomerLogin(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	command.CustomerID, command.IdentityHMAC, command.LoginIdentity = lower.ID, lowerIdentity.PurchaseIdentityHMAC(), &lowerIdentity
	if _, err := service.CreatePurchaseOrder(ctx, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("sibling replay must conflict: %v", err)
	}
	command.IdempotencyKey = "purchase-lower"
	second, err := service.CreatePurchaseOrder(ctx, command)
	if err != nil || second.OrderID == first.OrderID {
		t.Fatalf("separate purchase: %v", err)
	}
	orders := db.must(t, rqlite.Statement{SQL: `SELECT customer_id FROM orders ORDER BY customer_id`})
	if len(orders[0].Rows) != 2 || orders[0].Rows[0]["customer_id"] == orders[0].Rows[1]["customer_id"] {
		t.Fatal("purchase targets collapsed")
	}
	before := db.snapshot(t)
	command.IdempotencyKey = "purchase-proof-race"
	db.beforeRequest = func() {
		db.must(t, rqlite.Statement{SQL: `UPDATE imported_entity_state SET canonical_sha256=? WHERE entity_kind='customer' AND target_id=?`, Args: []any{strings.Repeat("d", 64), lower.ID}})
	}
	if _, err := service.CreatePurchaseOrder(ctx, command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("purchase proof drift: %v", err)
	}
	if !reflect.DeepEqual(before, db.snapshot(t)) || !reflect.DeepEqual(orders, db.must(t, rqlite.Statement{SQL: `SELECT customer_id FROM orders ORDER BY customer_id`})) {
		t.Fatal("purchase proof race changed durable state")
	}
	command.LoginIdentity = nil
	command.IdempotencyKey = "naked-exact-target"
	if _, err := service.CreatePurchaseOrder(ctx, command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("exact purchase without proof: %v", err)
	}
}
