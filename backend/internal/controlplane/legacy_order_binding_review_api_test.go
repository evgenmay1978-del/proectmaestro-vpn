package controlplane_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestLegacyOrderExplicitKeyPreservesExactBytesSQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	body := `{"tariff":"1m"}`

	status, plainBody, plain := f6ShipPostOrder(t, fixture.handler, body, "key")
	if status != http.StatusOK || plain.OrderID == "" {
		t.Fatalf("plain key status=%d body=%s", status, plainBody)
	}
	status, spacedBody, spaced := f6ShipPostOrder(t, fixture.handler, body, " key ")
	if status != http.StatusOK || spaced.OrderID == "" || spaced.OrderID == plain.OrderID {
		t.Fatalf("spaced key status=%d order=%q plain=%q body=%s, want distinct exact binding",
			status, spaced.OrderID, plain.OrderID, spacedBody)
	}

	for _, replay := range []struct {
		key  string
		want f6OrderResponse
		body []byte
	}{
		{key: "key", want: plain, body: plainBody},
		{key: " key ", want: spaced, body: spacedBody},
	} {
		status, replayBody, got := f6ShipPostOrder(t, fixture.handler, body, replay.key)
		if status != http.StatusOK || got.OrderID != replay.want.OrderID || !bytes.Equal(replayBody, replay.body) {
			t.Fatalf("key %q replay status=%d order=%q body=%s, want exact order=%q body=%s",
				replay.key, status, got.OrderID, replayBody, replay.want.OrderID, replay.body)
		}
	}

	rows := fixture.db.must(t, rqlite.Statement{SQL: `
SELECT idempotency_key,resource_id FROM idempotency_requests
WHERE scope='legacy-order' AND command_type='create' AND idempotency_key IN (?,?)`, Args: []any{"key", " key "}})
	if len(rows) != 1 || len(rows[0].Rows) != 2 {
		t.Fatalf("exact key rows=%#v, want two durable raw keys", rows)
	}
	wantOrders := map[string]string{"key": plain.OrderID, " key ": spaced.OrderID}
	for _, row := range rows[0].Rows {
		key := fmt.Sprint(row["idempotency_key"])
		if fmt.Sprint(row["resource_id"]) != wantOrders[key] {
			t.Fatalf("exact key row=%#v want=%#v", row, wantOrders)
		}
		delete(wantOrders, key)
	}
	if len(wantOrders) != 0 {
		t.Fatalf("missing exact key rows=%#v", wantOrders)
	}

	beforeWhitespace := f6ShipCountsNow(t, fixture.db)
	_, _, whitespaceFirst := f6ShipPostOrder(t, fixture.handler, body, "   ")
	_, _, whitespaceSecond := f6ShipPostOrder(t, fixture.handler, body, "   ")
	if whitespaceFirst.OrderID == "" || whitespaceSecond.OrderID == "" || whitespaceFirst.OrderID == whitespaceSecond.OrderID {
		t.Fatalf("whitespace-only key orders first=%+v second=%+v, want keyless distinct intents", whitespaceFirst, whitespaceSecond)
	}
	if after := f6ShipCountsNow(t, fixture.db); after.Idempotency != beforeWhitespace.Idempotency {
		t.Fatalf("whitespace-only key wrote idempotency row before=%+v after=%+v", beforeWhitespace, after)
	}
}

func TestLegacyOrderExplicitKeyBindsSuppliedIdentityFingerprintSQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	existing := fixture.seedCustomer(t, "Identity-Owner", "active")

	unknownToken := "synthetic-unknown-token-a"
	unknownBody := fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, unknownToken)
	status, firstBody, first := f6ShipPostOrder(t, fixture.handler, unknownBody, "unknown-identity-key")
	if status != http.StatusOK || first.OrderID == "" {
		t.Fatalf("unknown identity first status=%d body=%s", status, firstBody)
	}
	status, normalizedBody, normalized := f6ShipPostOrder(t, fixture.handler,
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, "  "+unknownToken+"  "), "unknown-identity-key")
	if status != http.StatusOK || normalized.OrderID != first.OrderID || !bytes.Equal(normalizedBody, firstBody) {
		t.Fatalf("normalized unknown identity replay status=%d order=%q body=%s, want exact %q/%s",
			status, normalized.OrderID, normalizedBody, first.OrderID, firstBody)
	}
	f6AssertSavedIdentityBinding(t, fixture, "unknown-identity-key", "sub_token", unknownToken)

	beforeConflict := f6ShipCountsNow(t, fixture.db)
	for _, conflictBody := range []string{
		`{"tariff":"1m","sub_token":"synthetic-unknown-token-b"}`,
		`{"tariff":"1m","login":"synthetic-unknown-login"}`,
		`{"tariff":"1m"}`,
	} {
		status, _, _ = f6ShipPostOrder(t, fixture.handler, conflictBody, "unknown-identity-key")
		if status != http.StatusConflict {
			t.Fatalf("identity conflict body=%s status=%d, want 409", conflictBody, status)
		}
	}
	if after := f6ShipCountsNow(t, fixture.db); after != beforeConflict {
		t.Fatalf("identity conflicts mutated rows before=%+v after=%+v", beforeConflict, after)
	}

	knownBody := fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, existing.Access.SubscriptionToken)
	status, knownFirstBody, knownFirst := f6ShipPostOrder(t, fixture.handler, knownBody, "known-identity-key")
	if status != http.StatusOK || knownFirst.OrderID == "" {
		t.Fatalf("known identity first status=%d body=%s", status, knownFirstBody)
	}
	f6AssertSavedIdentityBinding(t, fixture, "known-identity-key", "sub_token", existing.Access.SubscriptionToken)
	status, _, _ = f6ShipPostOrder(t, fixture.handler,
		`{"tariff":"1m","login":"IDENTITY-OWNER"}`, "known-identity-key")
	if status != http.StatusConflict {
		t.Fatalf("same customer through different supplied source status=%d, want 409", status)
	}
	f6ShipAssertIdempotencyHasNoPlaintext(t, fixture.db, "known-identity-key",
		append([]string{"Identity-Owner", existing.Access.SubscriptionToken}, f6ShipCredentialValues(existing.Access.Credentials)...))
}

func TestLegacyOrderAppliedReplayRejectsConsistentlySwappedSavedResponseSQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	existing := fixture.seedCustomer(t, "Corruption-Owner", "active")

	originalBody := `{"tariff":"1m","sub_token":"synthetic-corruption-token"}`
	status, _, original := f6ShipPostOrder(t, fixture.handler, originalBody, "corruption-original-key")
	if status != http.StatusOK || original.OrderID == "" {
		t.Fatalf("original order status=%d order=%q", status, original.OrderID)
	}
	status, _, replacement := f6ShipPostOrder(t, fixture.handler,
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, existing.Access.SubscriptionToken), "corruption-replacement-key")
	if status != http.StatusOK || replacement.OrderID == "" || replacement.OrderID == original.OrderID {
		t.Fatalf("replacement order status=%d original=%q replacement=%q", status, original.OrderID, replacement.OrderID)
	}

	replacementRows := fixture.db.must(t, rqlite.Statement{SQL: `
SELECT resource_id,decision,operation_id,response_json FROM idempotency_requests
WHERE scope='legacy-order' AND command_type='create' AND idempotency_key='corruption-replacement-key'`})
	if len(replacementRows) != 1 || len(replacementRows[0].Rows) != 1 {
		t.Fatalf("replacement binding rows=%#v", replacementRows)
	}
	replacementRow := replacementRows[0].Rows[0]
	fixture.db.must(t,
		rqlite.Statement{SQL: `DELETE FROM idempotency_requests
WHERE scope='legacy-order' AND command_type='create' AND idempotency_key='corruption-replacement-key'`},
		rqlite.Statement{SQL: `UPDATE idempotency_requests
SET resource_id=?,decision=?,operation_id=?,response_json=?
WHERE scope='legacy-order' AND command_type='create' AND idempotency_key='corruption-original-key'`, Args: []any{
			replacementRow["resource_id"], replacementRow["decision"], replacementRow["operation_id"], replacementRow["response_json"],
		}},
	)

	beforeReplay := f6ShipCountsNow(t, fixture.db)
	status, _, replay := f6ShipPostOrder(t, fixture.handler, originalBody, "corruption-original-key")
	if status == http.StatusOK {
		t.Fatalf("corrupted durable response replayed replacement order=%q, want fail-closed 503/409", replay.OrderID)
	}
	if status != http.StatusServiceUnavailable && status != http.StatusConflict {
		t.Fatalf("corrupted durable response status=%d, want 503/409", status)
	}
	if after := f6ShipCountsNow(t, fixture.db); after != beforeReplay {
		t.Fatalf("corrupted replay mutated rows before=%+v after=%+v", beforeReplay, after)
	}
}

func TestLegacyOrderSuspendedIdentityIsAdminBanSQLite(t *testing.T) {
	fixture := newF6ShipFixture(t, nil, time.Unix(2_000_000, 0).UTC())
	suspended := fixture.seedCustomer(t, "Admin-Suspended", "suspended")
	for _, body := range []string{
		fmt.Sprintf(`{"tariff":"1m","sub_token":%q}`, suspended.Access.SubscriptionToken),
		`{"tariff":"1m","login":"Admin-Suspended"}`,
	} {
		before := f6ShipCountsNow(t, fixture.db)
		status, _, _ := f6ShipPostOrder(t, fixture.handler, body, "suspended-admin-ban")
		if status != http.StatusForbidden {
			t.Fatalf("admin suspension body=%s status=%d, want fail-fast 403", body, status)
		}
		if after := f6ShipCountsNow(t, fixture.db); after != before {
			t.Fatalf("admin suspension mutated rows before=%+v after=%+v", before, after)
		}
	}
}

func f6AssertSavedIdentityBinding(t *testing.T, fixture f6ShipFixture, key, source, normalized string) {
	t.Helper()
	rows := fixture.db.must(t, rqlite.Statement{SQL: `
SELECT response_json FROM idempotency_requests
WHERE scope='legacy-order' AND command_type='create' AND idempotency_key=?`, Args: []any{key}})
	if len(rows) != 1 || len(rows[0].Rows) != 1 {
		t.Fatalf("saved identity rows=%#v", rows)
	}
	encoded := fmt.Sprint(rows[0].Rows[0]["response_json"])
	var saved struct {
		IdentitySource string `json:"identity_source"`
		IdentityHMAC   string `json:"identity_hmac"`
	}
	if json.Unmarshal([]byte(encoded), &saved) != nil {
		t.Fatalf("decode saved identity response=%q", encoded)
	}
	wantHMAC := f6ExpectedIdentityHMAC(source, normalized)
	if saved.IdentitySource != source || saved.IdentityHMAC != wantHMAC {
		t.Fatalf("saved identity source=%q hmac=%q, want %q/%q", saved.IdentitySource, saved.IdentityHMAC, source, wantHMAC)
	}
	if strings.Contains(encoded, normalized) {
		t.Fatalf("saved response leaked normalized identity %q in %s", normalized, encoded)
	}
}

func f6ExpectedIdentityHMAC(source, normalized string) string {
	mac := hmac.New(sha256.New, bytes.Repeat([]byte{0x62}, 32))
	_, _ = mac.Write([]byte("legacy-order-identity:" + source + "\x00"))
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}
