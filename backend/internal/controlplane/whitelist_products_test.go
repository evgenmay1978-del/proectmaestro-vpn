package controlplane

import (
	"context"
	"reflect"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestWhiteListProductsReturnsExactCommercialCatalog(t *testing.T) {
	rows := []map[string]any{
		{"product_id": "wl-gb-5-v1", "kind": "WHITELIST_BYTES", "amount_minor": int64(10000), "currency": "RUB", "bytes": int64(5000000000), "unit": "GB_DECIMAL"},
		{"product_id": "wl-gb-20-v1", "kind": "WHITELIST_BYTES", "amount_minor": int64(30000), "currency": "RUB", "bytes": int64(20000000000), "unit": "GB_DECIMAL"},
		{"product_id": "wl-gb-50-v1", "kind": "WHITELIST_BYTES", "amount_minor": int64(60000), "currency": "RUB", "bytes": int64(50000000000), "unit": "GB_DECIMAL"},
		{"product_id": "wl-gb-100-v1", "kind": "WHITELIST_BYTES", "amount_minor": int64(100000), "currency": "RUB", "bytes": int64(100000000000), "unit": "GB_DECIMAL"},
	}
	db := &recordingRQLite{linear: []scriptedResult{resultsScript(rqlite.Result{Rows: rows})}}
	service, _ := testService(t, db)

	got, err := service.WhiteListProducts(context.Background())
	if err != nil {
		t.Fatalf("WhiteListProducts: %v", err)
	}
	want := []WhiteListProduct{
		{ProductID: "wl-gb-5-v1", Kind: "WHITELIST_BYTES", AmountMinor: 10000, Currency: "RUB", Bytes: 5000000000, Unit: "GB_DECIMAL"},
		{ProductID: "wl-gb-20-v1", Kind: "WHITELIST_BYTES", AmountMinor: 30000, Currency: "RUB", Bytes: 20000000000, Unit: "GB_DECIMAL"},
		{ProductID: "wl-gb-50-v1", Kind: "WHITELIST_BYTES", AmountMinor: 60000, Currency: "RUB", Bytes: 50000000000, Unit: "GB_DECIMAL"},
		{ProductID: "wl-gb-100-v1", Kind: "WHITELIST_BYTES", AmountMinor: 100000, Currency: "RUB", Bytes: 100000000000, Unit: "GB_DECIMAL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog=%#v, want %#v", got, want)
	}
	if len(db.linearCalls) != 1 || len(db.linearCalls[0].statements) != 1 {
		t.Fatalf("catalog reads=%#v", db.linearCalls)
	}
}

func TestWhiteListProductsRejectsMalformedCatalogRow(t *testing.T) {
	rows := []map[string]any{{
		"product_id": "wl-gb-bad", "kind": "WHITELIST_BYTES", "amount_minor": int64(1),
		"currency": "USD", "bytes": int64(1), "unit": "GB_DECIMAL",
	}}
	db := &recordingRQLite{linear: []scriptedResult{
		resultsScript(rqlite.Result{Rows: rows}),
	}}
	service, _ := testService(t, db)
	if _, err := service.WhiteListProducts(context.Background()); err == nil {
		t.Fatal("malformed catalog row was accepted")
	}
}
