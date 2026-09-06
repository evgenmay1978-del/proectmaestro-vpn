package controlplane

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
)

func testLegacyRawOrder(id, login, token, status string) legacyorder.Order {
	return legacyorder.Order{ID: id, Tariff: "1m", Days: 30, Rub: 300, Code: "code-" + id, Login: login, SubToken: token, Status: status, CreatedAt: time.Unix(1_000_000, 123456789).UTC()}
}

func TestLegacyOrderSourcePreservesRawHistoryAndRejectsLoss(t *testing.T) {
	order := testLegacyRawOrder("ord_exact", "ExactLogin", "source-token", "paid")
	raw, _ := json.Marshal([]legacyorder.Order{order})
	rows, err := DecodeLegacyOrderSource(raw)
	if err != nil || len(rows) != 1 || rows[0].CreditedPresent || rows[0].Order != order || !bytes.Equal(rows[0].RawJSON, raw[1:len(raw)-1]) {
		t.Fatal("legacy record lost exact identity, timestamp or absent credited")
	}
	explicit := bytes.Replace(raw, []byte(`"status":"paid"`), []byte(`"status":"paid","credited":false`), 1)
	explicitRows, err := DecodeLegacyOrderSource(explicit)
	if err != nil || !explicitRows[0].CreditedPresent || explicitRows[0].Order.Credited {
		t.Fatal("credited presence was collapsed")
	}
	repeated, _ := json.Marshal([]legacyorder.Order{order, order})
	for _, bad := range [][]byte{nil, []byte("null"), repeated, bytes.Replace(raw, []byte(`"id":`), []byte(`"ID":"hidden","id":`), 1), bytes.Replace(raw, []byte(`"id":`), []byte(`"id":"hidden","id":`), 1), bytes.Replace(raw, []byte("source-token"), []byte(`\ud800`), 1), bytes.Replace(raw, []byte("source-token"), []byte{0xff}, 1)} {
		if _, err := DecodeLegacyOrderSource(bad); err == nil || strings.Contains(err.Error(), "source-token") || strings.Contains(err.Error(), "ExactLogin") {
			t.Fatal("lossy source accepted or secret exposed")
		}
	}
	valid := bytes.Replace(raw, []byte("source-token"), []byte(`valid-\ud83d\ude00-�`), 1)
	if _, err := DecodeLegacyOrderSource(valid); err != nil {
		t.Fatal("valid Unicode source rejected")
	}
}

func TestLegacyOrderTransitionNeverRewritesTermsOrReopensGrant(t *testing.T) {
	pending := testLegacyRawOrder("ord_exact", "ExactLogin", "", "pending")
	row := func(order legacyorder.Order) LegacyOrderSourceRow {
		raw, _ := json.Marshal(order)
		r, err := decodeLegacyOrderRow(raw)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	paid := pending
	paid.Status, paid.SubToken, paid.Credited = "paid", "source-token", true
	if !LegacyOrderTransitionAllowed(row(pending), row(paid)) {
		t.Fatal("real payment completion rejected")
	}
	if LegacyOrderTransitionAllowed(row(paid), row(pending)) {
		t.Fatal("credited order was reopened")
	}
	for _, change := range []func(*legacyorder.Order){func(o *legacyorder.Order) { o.Rub = 400 }, func(o *legacyorder.Order) { o.Days = 60 }, func(o *legacyorder.Order) { o.Code = "replacement" }, func(o *legacyorder.Order) { o.CreatedAt = o.CreatedAt.Add(time.Nanosecond) }, func(o *legacyorder.Order) { o.Login = "Sibling" }} {
		altered := pending
		change(&altered)
		if LegacyOrderTransitionAllowed(row(pending), row(altered)) {
			t.Fatal("original order terms changed")
		}
	}
}
