package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	legacyorder "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
)

const LegacyOrderLookupDomain = "legacy-order-raw-id-v1"
const LegacyOrderRecordKind = "legacy-order-record-v1"
const LegacyOrderSourceKind = "legacy-orders-source-v1"

// SourceRow is encoded as bytes so even whitespace, fractional timestamps and
// the distinction between absent credited and credited=false survive sealing.
type LegacyOrderRecord struct {
	SchemaVersion     int    `json:"schema_version"`
	OrderKeyHMAC      string `json:"order_key_hmac"`
	OrdersSHA256      string `json:"orders_sha256"`
	CustomersSHA256   string `json:"customers_sha256"`
	SourceRow         []byte `json:"source_row"`
	CustomerID        string `json:"customer_id,omitempty"`
	CustomerSourceKey string `json:"customer_source_key,omitempty"`
	CustomerLoginHMAC string `json:"customer_login_hmac,omitempty"`
	CustomerTokenHMAC string `json:"customer_token_hmac,omitempty"`
	Revision          int64  `json:"revision"`
}

type LegacyOrderSourceRow struct {
	Order           legacyorder.Order
	RawJSON         []byte
	CreditedPresent bool
}

func LegacyOrderRecordScope(key string) SecretScope {
	return SecretScope{OwnerType: "legacy_order", OwnerID: key, Field: "source_record", Kind: LegacyOrderRecordKind}
}
func LegacyOrderSourceScope(sha string) SecretScope {
	return SecretScope{OwnerType: "legacy_order_source", OwnerID: sha, Field: "source_json", Kind: LegacyOrderSourceKind}
}
func LegacyOrderRecordID(key, sha string) string {
	return LegacyOrderRecordKind + ":" + key + ":" + sha
}
func LegacyOrderSourceID(sha string) string { return LegacyOrderSourceKind + ":" + sha }
func LegacyOrderDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// The old order file is a strict array. No map insertion or case-folding may
// overwrite one public order identity with another during import.
func DecodeLegacyOrderSource(raw []byte) ([]LegacyOrderSourceRow, error) {
	if len(raw) == 0 || len(raw) > 32<<20 || !utf8.Valid(raw) || !legacyOrderUnicodeLossless(raw) {
		return nil, ErrConflict
	}
	var rows []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&rows) != nil || rows == nil || decoder.Decode(&struct{}{}) != io.EOF || len(rows) > 100000 {
		return nil, ErrConflict
	}
	result := make([]LegacyOrderSourceRow, 0, len(rows))
	ids, codes := map[string]bool{}, map[string]bool{}
	for _, rawRow := range rows {
		row, err := decodeLegacyOrderRow(rawRow)
		if err != nil || ids[row.Order.ID] || codes[row.Order.Code] {
			return nil, ErrConflict
		}
		ids[row.Order.ID], codes[row.Order.Code] = true, true
		result = append(result, row)
	}
	return result, nil
}

func decodeLegacyOrderRow(raw []byte) (LegacyOrderSourceRow, error) {
	invalid := func() (LegacyOrderSourceRow, error) { return LegacyOrderSourceRow{}, ErrConflict }
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || !legacyOrderUnicodeLossless(raw) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || fields[key] {
			return invalid()
		}
		switch key {
		case "id", "tariff", "days", "rub", "code", "login", "status", "sub_token", "created_at", "credited":
		default:
			return invalid()
		}
		fields[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF {
		return invalid()
	}
	for _, key := range []string{"id", "tariff", "days", "rub", "code", "login", "status", "sub_token", "created_at"} {
		if !fields[key] {
			return invalid()
		}
	}
	var order legacyorder.Order
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&order) != nil {
		return invalid()
	}
	for _, value := range []string{order.ID, order.Tariff, order.Code, order.Login} {
		if value == "" || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return invalid()
		}
	}
	if _, err := CanonicalLoginKey(order.Login); err != nil || order.Days <= 0 || int64(order.Days) > math.MaxInt64/86400 || order.Rub <= 0 || int64(order.Rub) > math.MaxInt64/100 ||
		(order.Status != "pending" && order.Status != "paid") || (order.Status == "paid" && order.SubToken == "") || len(order.SubToken) > 4096 || strings.ContainsRune(order.SubToken, 0) ||
		order.CreatedAt.IsZero() || order.CreatedAt.Unix() < 0 || order.CreatedAt.Unix() > 253402300799 {
		return invalid()
	}
	return LegacyOrderSourceRow{Order: order, RawJSON: append([]byte(nil), raw...), CreditedPresent: fields["credited"]}, nil
}

// encoding/json replaces unpaired surrogate escapes, losing source bytes.
// Literal U+FFFD and valid paired escapes remain legitimate source values.
func legacyOrderUnicodeLossless(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+5 > len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+7 > len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func ValidateLegacyOrderRecord(box *SecretBox, record LegacyOrderRecord) (LegacyOrderSourceRow, error) {
	row, err := decodeLegacyOrderRow(record.SourceRow)
	if err != nil || box == nil || record.SchemaVersion != 1 || record.Revision <= 0 || !legacyBindingSHA(record.OrdersSHA256) || !legacyBindingSHA(record.CustomersSHA256) ||
		record.OrderKeyHMAC != box.LookupHMAC(LegacyOrderLookupDomain, []byte(row.Order.ID)) {
		return LegacyOrderSourceRow{}, ErrConflict
	}
	if record.CustomerID == "" {
		if record.CustomerSourceKey != "" || record.CustomerLoginHMAC != "" || record.CustomerTokenHMAC != "" {
			return LegacyOrderSourceRow{}, ErrConflict
		}
	} else {
		if record.CustomerID != LegacyOrderDigest([]byte("maestro-legacy-v1\x00customer\x00"+record.CustomerSourceKey)) || !legacyBindingSHA(record.CustomerLoginHMAC) || !legacyBindingSHA(record.CustomerTokenHMAC) {
			return LegacyOrderSourceRow{}, ErrConflict
		}
		canonical, _ := CanonicalLoginKey(row.Order.Login)
		expected := box.LookupHMAC("customer-login", []byte(canonical))
		if strings.HasPrefix(record.CustomerSourceKey, LegacyExactCustomerSourcePrefix) {
			canonicalHMAC, exactHMAC, ok := ParseLegacyExactCustomerSource(record.CustomerSourceKey)
			if !ok || canonicalHMAC != expected || exactHMAC != box.LookupHMAC(LegacyExactCustomerLoginHMACDomain, []byte(row.Order.Login)) {
				return LegacyOrderSourceRow{}, ErrConflict
			}
			expected = exactHMAC
		}
		if record.CustomerLoginHMAC != expected || (row.Order.SubToken != "" && box.LookupHMAC("subscription-token", []byte(row.Order.SubToken)) != record.CustomerTokenHMAC) {
			return LegacyOrderSourceRow{}, ErrConflict
		}
	}
	return row, nil
}

func LegacyOrderTransitionAllowed(prior, next LegacyOrderSourceRow) bool {
	if prior.Order.Credited && !next.Order.Credited || prior.Order.Status == "paid" && next.Order.Status != "paid" || prior.Order.SubToken != "" && prior.Order.SubToken != next.Order.SubToken {
		return false
	}
	comparison := next.Order
	comparison.Status, comparison.Credited, comparison.SubToken = prior.Order.Status, prior.Order.Credited, prior.Order.SubToken
	before, _ := json.Marshal(prior.Order)
	after, _ := json.Marshal(comparison)
	return bytes.Equal(before, after)
}
