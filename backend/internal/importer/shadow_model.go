package importer

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

var (
	ErrShadowExportInvalid     = errors.New("shadow export invalid")
	ErrShadowExportUnavailable = errors.New("shadow export unavailable")
)

type ShadowURLShapes struct {
	Maestro string
	Karing  string
}

type ShadowExport struct {
	SchemaVersion          int              `json:"schema_version"`
	Customers              []ShadowCustomer `json:"customers"`
	Orders                 []ShadowOrder    `json:"orders"`
	SettingsFingerprint   string           `json:"settings_fingerprint"`
	PrincipalsFingerprint string           `json:"principals_fingerprint"`
	OTA                    ShadowOTA        `json:"ota_manifest"`
}

type ShadowCustomer struct {
	IdentityHMAC    string   `json:"identity_hmac"`
	ExpiresAtUnix   int64    `json:"expires_at_unix"`
	Generation      int64    `json:"generation"`
	ProtocolTags    []string `json:"protocol_tags"`
	Nodes           []string `json:"nodes"`
	MaestroURLShape string   `json:"maestro_url_shape"`
	KaringURLShape  string   `json:"karing_url_shape"`
}

type ShadowOrder struct {
	IdentityDigest      string `json:"order_hmac"`
	State               string `json:"state"`
	ResultExpiresAtUnix int64  `json:"result_expires_at_unix"`
}

type ShadowOTA struct {
	VersionCode int64  `json:"version_code"`
	VersionName string `json:"version_name"`
	APKSHA256   string `json:"apk_sha256"`
	APKSize     int64  `json:"apk_size"`
}

func EncodeShadowExport(value ShadowExport) ([]byte, error) {
	canonical, err := canonicalShadowExport(value)
	if err != nil {
		return nil, ErrShadowExportInvalid
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, ErrShadowExportInvalid
	}
	return encoded, nil
}

func canonicalShadowExport(value ShadowExport) (ShadowExport, error) {
	if value.SchemaVersion != 1 || !validShadowHex64(value.SettingsFingerprint) ||
		!validShadowHex64(value.PrincipalsFingerprint) || value.OTA.VersionCode < 0 ||
		value.OTA.VersionName == "" || !validShadowHex64(value.OTA.APKSHA256) || value.OTA.APKSize < 0 {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	canonical := ShadowExport{
		SchemaVersion:          value.SchemaVersion,
		SettingsFingerprint:   value.SettingsFingerprint,
		PrincipalsFingerprint: value.PrincipalsFingerprint,
		OTA:                    value.OTA,
		Customers:              make([]ShadowCustomer, len(value.Customers)),
		Orders:                 make([]ShadowOrder, len(value.Orders)),
	}
	identities := make(map[string]struct{}, len(value.Customers))
	for index, customer := range value.Customers {
		if !validShadowHex64(customer.IdentityHMAC) || customer.ExpiresAtUnix < 0 || customer.Generation < 0 ||
			!validShadowShapes(ShadowURLShapes{Maestro: customer.MaestroURLShape, Karing: customer.KaringURLShape}) {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		if _, exists := identities[customer.IdentityHMAC]; exists {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		identities[customer.IdentityHMAC] = struct{}{}
		protocolTags, err := canonicalShadowSet(customer.ProtocolTags)
		if err != nil {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		nodes, err := canonicalShadowSet(customer.Nodes)
		if err != nil {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		customer.ProtocolTags = protocolTags
		customer.Nodes = nodes
		canonical.Customers[index] = customer
	}
	orders := make(map[string]struct{}, len(value.Orders))
	for index, order := range value.Orders {
		if !validShadowHex64(order.IdentityDigest) || !validShadowOrderState(order.State) || order.ResultExpiresAtUnix < 0 {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		if _, exists := orders[order.IdentityDigest]; exists {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		orders[order.IdentityDigest] = struct{}{}
		canonical.Orders[index] = order
	}
	sortShadowCustomers(canonical.Customers)
	sortShadowOrders(canonical.Orders)
	return canonical, nil
}

func canonicalShadowSet(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, ErrShadowExportInvalid
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if value == "" || (index > 0 && value == result[index-1]) {
			return nil, ErrShadowExportInvalid
		}
	}
	return result, nil
}

func validShadowShapes(shapes ShadowURLShapes) bool {
	return validShadowShape(shapes.Maestro, "maestro://") && validShadowShape(shapes.Karing, "https://")
}

func validShadowShape(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && strings.Count(value, "{opaque-token}") == 1 &&
		!strings.ContainsAny(value, "\r\n\t ")
}

func validShadowOrderState(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	payment := map[string]struct{}{"pending": {}, "claimed": {}, "confirmed": {}, "rejected": {}}
	provisioning := map[string]struct{}{"pending": {}, "applying": {}, "applied": {}, "failed": {}}
	_, paymentOK := payment[parts[0]]
	_, provisioningOK := provisioning[parts[1]]
	return paymentOK && provisioningOK
}

func validShadowHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func sortShadowCustomers(values []ShadowCustomer) {
	sort.Slice(values, func(left, right int) bool {
		return values[left].IdentityHMAC < values[right].IdentityHMAC
	})
}

func sortShadowOrders(values []ShadowOrder) {
	sort.Slice(values, func(left, right int) bool {
		return values[left].IdentityDigest < values[right].IdentityDigest
	})
}
