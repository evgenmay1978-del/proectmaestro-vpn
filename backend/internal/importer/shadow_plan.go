package importer

import (
	"bytes"
	"encoding/json"
	"io"
)

type shadowSettingFingerprintRow struct {
	Key              string          `json:"key"`
	PublicValueJSON  json.RawMessage `json:"public_value_json"`
	Generation       int64           `json:"generation"`
	SecretSHA256     string          `json:"secret_sha256,omitempty"`
	SecretKeyVersion int             `json:"secret_key_version,omitempty"`
}

type shadowPrincipalFingerprintRow struct {
	InternalID        string   `json:"internal_id"`
	LoginKeyHMAC      string   `json:"login_key_hmac"`
	Status            string   `json:"status"`
	Roles             []string `json:"roles"`
	VerifierSHA256    string   `json:"verifier_sha256"`
	VerifierKeyVersion int     `json:"verifier_key_version"`
}

type shadowOTAPublicValue struct {
	VersionCode *int64  `json:"versionCode"`
	VersionName *string `json:"versionName"`
	SHA256      *string `json:"sha256"`
	Size        *int64  `json:"size"`
}

func ShadowFromPlan(plan ImportPlan, shapes ShadowURLShapes) (ShadowExport, error) {
	if plan.SnapshotKind != "full" || len(plan.Blockers) != 0 || !validShadowURLShapes(shapes) ||
		!validShadowHex64(plan.SourceDigest) || !validShadowHex64(plan.PlanDigest) || Digest(plan) != plan.PlanDigest {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	secrets, err := shadowSecretIndex(plan.EncryptedSecrets)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	settingsFingerprint, ota, err := shadowSettings(plan.Settings, secrets)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	principalsFingerprint, err := shadowPrincipals(plan.Principals, secrets)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	export := ShadowExport{
		SchemaVersion:          1,
		SettingsFingerprint:   settingsFingerprint,
		PrincipalsFingerprint: principalsFingerprint,
		OTA:                    ota,
		Customers:              make([]ShadowCustomer, 0, len(plan.Customers)),
		Orders:                 make([]ShadowOrder, 0, len(plan.Orders)),
	}
	for _, customer := range plan.Customers {
		secret, exists := secrets[customer.IdentitySecretRef]
		if !exists || secret.OwnerType != "customer" || secret.OwnerSourceKey != customer.SourceKey {
			return ShadowExport{}, ErrShadowExportInvalid
		}
		export.Customers = append(export.Customers, ShadowCustomer{
			IdentityHMAC:    customer.LoginKeyHMAC,
			ExpiresAtUnix:   customer.ExpiresAtUnix,
			Generation:      customer.Generation,
			ProtocolTags:    append([]string(nil), customer.ProtocolTags...),
			Nodes:           append([]string(nil), customer.NodeIDs...),
			MaestroURLShape: shapes.Maestro,
			KaringURLShape:  shapes.Karing,
		})
	}
	for _, order := range plan.Orders {
		paymentState := order.PaymentState
		if paymentState == "created" {
			paymentState = "pending"
		} else if paymentState == "paid" {
			paymentState = "confirmed"
		}
		provisioningState := order.ProvisioningState
		if provisioningState == "paid" {
			provisioningState = "applied"
		}
		export.Orders = append(export.Orders, ShadowOrder{
			IdentityDigest:      order.InternalID,
			State:               paymentState + ":" + provisioningState,
			ResultExpiresAtUnix: order.ResultExpiresAtUnix,
		})
	}
	canonical, err := canonicalShadowExport(export)
	if err != nil {
		return ShadowExport{}, ErrShadowExportInvalid
	}
	return canonical, nil
}

func shadowSecretIndex(values []LegacyEncryptedSecret) (map[string]LegacyEncryptedSecret, error) {
	result := make(map[string]LegacyEncryptedSecret, len(values))
	for _, secret := range values {
		if secret.SecretID == "" || !validShadowHex64(secret.SHA256) || secret.KeyVersion <= 0 {
			return nil, ErrShadowExportInvalid
		}
		if _, exists := result[secret.SecretID]; exists {
			return nil, ErrShadowExportInvalid
		}
		result[secret.SecretID] = secret
	}
	return result, nil
}

func shadowSettings(
	settings []LegacySetting,
	secrets map[string]LegacyEncryptedSecret,
) (string, ShadowOTA, error) {
	rows := make([]shadowSettingFingerprintRow, 0, len(settings))
	keys := make(map[string]struct{}, len(settings))
	otaCount := 0
	var ota ShadowOTA
	for _, setting := range settings {
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
		row := shadowSettingFingerprintRow{
			Key: setting.Key, PublicValueJSON: publicValue, Generation: setting.Generation,
		}
		if setting.SecretRef != "" {
			secret, exists := secrets[setting.SecretRef]
			if !exists || secret.OwnerType != "setting" || secret.OwnerSourceKey != setting.Key {
				return "", ShadowOTA{}, ErrShadowExportInvalid
			}
			row.SecretSHA256 = secret.SHA256
			row.SecretKeyVersion = secret.KeyVersion
		}
		rows = append(rows, row)
		if setting.Key == "ota" {
			if setting.SecretRef != "" {
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

func shadowPrincipals(
	principals []PlannedPrincipal,
	secrets map[string]LegacyEncryptedSecret,
) (string, error) {
	rows := make([]shadowPrincipalFingerprintRow, 0, len(principals))
	identities := make(map[string]struct{}, len(principals))
	for _, principal := range principals {
		if !validShadowHex64(principal.InternalID) || !validShadowHex64(principal.LoginKeyHMAC) || principal.Status == "" {
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
		secret, exists := secrets[principal.CredentialSecretRef]
		if !exists || secret.OwnerType != "principal" || secret.OwnerSourceKey != principal.SourceKey {
			return "", ErrShadowExportInvalid
		}
		rows = append(rows, shadowPrincipalFingerprintRow{
			InternalID: principal.InternalID, LoginKeyHMAC: principal.LoginKeyHMAC,
			Status: principal.Status, Roles: roles, VerifierSHA256: secret.SHA256,
			VerifierKeyVersion: secret.KeyVersion,
		})
	}
	sortShadowPrincipalRows(rows)
	return shadowFingerprint(rows)
}

func canonicalPublicJSON(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrShadowExportInvalid
	}
	if err := requireShadowEOF(decoder); err != nil {
		return nil, ErrShadowExportInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrShadowExportInvalid
	}
	return encoded, nil
}

func parseShadowOTA(raw json.RawMessage) (ShadowOTA, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value shadowOTAPublicValue
	if err := decoder.Decode(&value); err != nil || requireShadowEOF(decoder) != nil ||
		value.VersionCode == nil || value.VersionName == nil || value.SHA256 == nil || value.Size == nil ||
		*value.VersionCode < 0 || *value.VersionName == "" || !validShadowHex64(*value.SHA256) || *value.Size < 0 {
		return ShadowOTA{}, ErrShadowExportInvalid
	}
	return ShadowOTA{
		VersionCode: *value.VersionCode, VersionName: *value.VersionName,
		APKSHA256: *value.SHA256, APKSize: *value.Size,
	}, nil
}

func requireShadowEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrShadowExportInvalid
	}
	return nil
}

func shadowFingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", ErrShadowExportInvalid
	}
	return sha256Hex(encoded), nil
}

func sortShadowSettingRows(values []shadowSettingFingerprintRow) {
	for index := 1; index < len(values); index++ {
		for cursor := index; cursor > 0 && values[cursor].Key < values[cursor-1].Key; cursor-- {
			values[cursor], values[cursor-1] = values[cursor-1], values[cursor]
		}
	}
}

func sortShadowPrincipalRows(values []shadowPrincipalFingerprintRow) {
	for index := 1; index < len(values); index++ {
		for cursor := index; cursor > 0 && values[cursor].InternalID < values[cursor-1].InternalID; cursor-- {
			values[cursor], values[cursor-1] = values[cursor-1], values[cursor]
		}
	}
}
