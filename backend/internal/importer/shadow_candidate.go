package importer

import (
	"context"
	"encoding/json"
)

type ShadowProjection struct {
	SourceDigest      string
	TargetDigest      string
	RunApplied        bool
	BatchCount        int64
	AppliedBatchCount int64
	Customers         []ShadowProjectionCustomer
	Orders            []ShadowProjectionOrder
	Settings          []ShadowProjectionSetting
	Principals        []ShadowProjectionPrincipal
}

type ShadowProjectionCustomer struct {
	InternalID        string
	LoginKeyHMAC      string
	Status            string
	ExpiresAtUnix     int64
	Generation        int64
	CredentialEnabled bool
	TokenRevoked      bool
	Nodes             []string
	ProtocolTags      []string
}

type ShadowProjectionOrder struct {
	InternalID          string
	PaymentState        string
	ProvisioningState   string
	ResultExpiresAtUnix int64
}

type ShadowProjectionSetting struct {
	Key              string
	PublicValueJSON  json.RawMessage
	Generation       int64
	SecretSHA256     string
	SecretKeyVersion int
}

type ShadowProjectionPrincipal struct {
	InternalID         string
	LoginKeyHMAC       string
	Status             string
	Roles              []string
	VerifierSHA256     string
	VerifierKeyVersion int
	CredentialActive   bool
}

type ShadowCandidateSource interface {
	ReadShadowProjection(context.Context, string) (ShadowProjection, error)
}

func ShadowFromCandidate(
	ctx context.Context,
	source ShadowCandidateSource,
	expectedSourceDigest string,
	shapes ShadowURLShapes,
) (ShadowExport, error) {
	if source == nil || !validShadowHex64(expectedSourceDigest) || !validShadowURLShapes(shapes) {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	projection, err := source.ReadShadowProjection(ctx, expectedSourceDigest)
	if err != nil {
		return ShadowExport{}, ErrShadowExportUnavailable
	}
	if projection.SourceDigest != expectedSourceDigest || !validShadowHex64(projection.TargetDigest) ||
		!projection.RunApplied || projection.BatchCount < 0 || projection.AppliedBatchCount != projection.BatchCount {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	settingsFingerprint, ota, err := shadowCandidateSettings(projection.Settings)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	principalsFingerprint, err := shadowCandidatePrincipals(projection.Principals)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	export := ShadowExport{
		SchemaVersion: 1, SettingsFingerprint: settingsFingerprint,
		PrincipalsFingerprint: principalsFingerprint, OTA: ota,
		Customers: make([]ShadowCustomer, 0, len(projection.Customers)),
		Orders:    make([]ShadowOrder, 0, len(projection.Orders)),
	}
	customerIDs := make(map[string]struct{}, len(projection.Customers))
	identities := make(map[string]struct{}, len(projection.Customers))
	for _, customer := range projection.Customers {
		if customer.InternalID == "" || !validShadowHex64(customer.LoginKeyHMAC) || customer.Status != "active" ||
			customer.ExpiresAtUnix < 0 || customer.Generation < 0 || !customer.CredentialEnabled || customer.TokenRevoked {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		if _, exists := customerIDs[customer.InternalID]; exists {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		if _, exists := identities[customer.LoginKeyHMAC]; exists {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		customerIDs[customer.InternalID] = struct{}{}
		identities[customer.LoginKeyHMAC] = struct{}{}
		nodes, nodeErr := canonicalShadowSet(customer.Nodes)
		protocols, protocolErr := canonicalShadowSet(customer.ProtocolTags)
		if nodeErr != nil || protocolErr != nil {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		export.Customers = append(export.Customers, ShadowCustomer{
			IdentityHMAC: customer.LoginKeyHMAC, ExpiresAtUnix: customer.ExpiresAtUnix,
			Generation: customer.Generation, Nodes: nodes, ProtocolTags: protocols,
			MaestroURLShape: shapes.Maestro, KaringURLShape: shapes.Karing,
		})
	}
	orderIDs := make(map[string]struct{}, len(projection.Orders))
	for _, order := range projection.Orders {
		paymentState := order.PaymentState
		switch paymentState {
		case "created":
			paymentState = "pending"
		case "payment_claimed":
			paymentState = "claimed"
		}
		state := paymentState + ":" + order.ProvisioningState
		if !validShadowHex64(order.InternalID) || !validShadowOrderState(state) || order.ResultExpiresAtUnix < 0 {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		if _, exists := orderIDs[order.InternalID]; exists {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		orderIDs[order.InternalID] = struct{}{}
		export.Orders = append(export.Orders, ShadowOrder{
			IdentityDigest: order.InternalID, State: state,
			ResultExpiresAtUnix: order.ResultExpiresAtUnix,
		})
	}
	canonical, err := canonicalShadowExport(export)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	return canonical, nil
}

func shadowCandidateSettings(values []ShadowProjectionSetting) (string, ShadowOTA, error) {
	rows := make([]shadowSettingFingerprintRow, 0, len(values))
	keys := make(map[string]struct{}, len(values))
	otaCount := 0
	var ota ShadowOTA
	for _, setting := range values {
		if setting.Key == "" || setting.Generation < 0 {
			return "", ShadowOTA{}, ErrShadowExportInvalid
		}
		if _, exists := keys[setting.Key]; exists {
			return "", ShadowOTA{}, ErrShadowExportInvalid
		}
		keys[setting.Key] = struct{}{}
		publicValue, err := canonicalPublicJSON(setting.PublicValueJSON)
		if err != nil {
			return "", ShadowOTA{}, ErrShadowExportInvalid
		}
		row := shadowSettingFingerprintRow{Key: setting.Key, PublicValueJSON: publicValue, Generation: setting.Generation}
		if setting.SecretSHA256 != "" || setting.SecretKeyVersion != 0 {
			if !validShadowHex64(setting.SecretSHA256) || setting.SecretKeyVersion <= 0 {
				return "", ShadowOTA{}, ErrShadowExportInvalid
			}
			row.SecretSHA256, row.SecretKeyVersion = setting.SecretSHA256, setting.SecretKeyVersion
		}
		rows = append(rows, row)
		if setting.Key == "ota" {
			if row.SecretSHA256 != "" {
				return "", ShadowOTA{}, ErrShadowExportInvalid
			}
			otaCount++
			ota, err = parseShadowOTA(publicValue)
			if err != nil {
				return "", ShadowOTA{}, ErrShadowExportInvalid
			}
		}
	}
	if otaCount != 1 {
		return "", ShadowOTA{}, ErrShadowExportInvalid
	}
	sortShadowSettingRows(rows)
	fingerprint, err := shadowFingerprint(rows)
	if err != nil {
		return "", ShadowOTA{}, ErrShadowExportInvalid
	}
	return fingerprint, ota, nil
}

func shadowCandidatePrincipals(values []ShadowProjectionPrincipal) (string, error) {
	rows := make([]shadowPrincipalFingerprintRow, 0, len(values))
	identities := make(map[string]struct{}, len(values))
	for _, principal := range values {
		if !validShadowHex64(principal.InternalID) || !validShadowHex64(principal.LoginKeyHMAC) ||
			principal.Status == "" || !principal.CredentialActive || !validShadowHex64(principal.VerifierSHA256) ||
			principal.VerifierKeyVersion <= 0 {
			return "", ErrShadowExportInvalid
		}
		if _, exists := identities[principal.InternalID]; exists {
			return "", ErrShadowExportInvalid
		}
		identities[principal.InternalID] = struct{}{}
		roles, err := canonicalShadowSet(principal.Roles)
		if err != nil {
			return "", ErrShadowExportInvalid
		}
		rows = append(rows, shadowPrincipalFingerprintRow{
			InternalID: principal.InternalID, LoginKeyHMAC: principal.LoginKeyHMAC,
			Status: principal.Status, Roles: roles, VerifierSHA256: principal.VerifierSHA256,
			VerifierKeyVersion: principal.VerifierKeyVersion,
		})
	}
	sortShadowPrincipalRows(rows)
	return shadowFingerprint(rows)
}
