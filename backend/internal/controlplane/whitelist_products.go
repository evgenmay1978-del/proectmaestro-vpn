package controlplane

import (
	"context"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

const (
	whiteListProductKind     = "WHITELIST_BYTES"
	whiteListProductCurrency = "RUB"
	whiteListProductUnit     = "GB_DECIMAL"
)

type WhiteListProduct struct {
	ProductID   string
	Kind        string
	AmountMinor int64
	Currency    string
	Bytes       int64
	Unit        string
}

func (s *Service) WhiteListProducts(ctx context.Context) ([]WhiteListProduct, error) {
	if s == nil || s.store == nil || s.store.db == nil {
		return nil, ErrUnavailable
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{SQL: `
SELECT p.product_id,p.kind,t.amount_minor,t.currency,p.bytes,p.unit
FROM whitelist_gb_products AS p
JOIN tariff_versions AS t ON t.tariff_version_id=p.product_id
ORDER BY p.bytes,p.product_id`})
	if err != nil || len(results) != 1 {
		return nil, ErrUnavailable
	}

	products := make([]WhiteListProduct, 0, len(results[0].Rows))
	for _, row := range results[0].Rows {
		product, ok := parseWhiteListProduct(row)
		if !ok {
			return nil, ErrUnavailable
		}
		products = append(products, product)
	}
	return products, nil
}

func parseWhiteListProduct(row map[string]any) (WhiteListProduct, bool) {
	productID, productIDOK := rowString(row, "product_id")
	kind, kindOK := rowString(row, "kind")
	amountMinor, amountOK := rowInt64(row, "amount_minor")
	currency, currencyOK := rowString(row, "currency")
	bytes, bytesOK := rowInt64(row, "bytes")
	unit, unitOK := rowString(row, "unit")
	if !productIDOK || !kindOK || !amountOK || !currencyOK || !bytesOK || !unitOK ||
		kind != whiteListProductKind || currency != whiteListProductCurrency || unit != whiteListProductUnit ||
		amountMinor <= 0 || bytes <= 0 {
		return WhiteListProduct{}, false
	}
	return WhiteListProduct{
		ProductID: productID, Kind: kind, AmountMinor: amountMinor,
		Currency: currency, Bytes: bytes, Unit: unit,
	}, true
}
