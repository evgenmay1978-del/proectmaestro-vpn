package whitelistmetering

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const (
	// CommercialDebitReceiptScope and CommercialDebitReceiptCommand identify
	// the durable control-plane receipt written after one balance debit.
	CommercialDebitReceiptScope   = "whitelist-balance"
	CommercialDebitReceiptCommand = "apply-usage"
)

// CommercialDebit is the dependency-neutral handoff from accepted Xray
// metering to the prepaid balance service. The balance service reloads the
// immutable interval and source row; callers cannot supply billable bytes.
type CommercialDebit struct {
	EntitlementID   string
	BillingPeriodID string
	MeterEpoch      string
	IntervalID      string
	Basis           string
	IntervalEndUnix int64
	SourceSHA256    string
}

// CommercialDebitReceiptKey returns the stable exactly-once key shared by the
// metering drain and the balance service. Existing receipt keys keep the same
// byte representation as the original control-plane implementation.
func CommercialDebitReceiptKey(meterEpoch, intervalID string) (string, error) {
	if !exactIdentifier(meterEpoch) || !exactIdentifier(intervalID) {
		return "", ErrInvalidSource
	}
	payload := CommercialDebitReceiptScope + "\x00" + CommercialDebitReceiptCommand +
		"\x00" + meterEpoch + "\x00" + intervalID
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:]), nil
}

// CommercialDebitReceiptHash returns the canonical v2 balance request hash.
// The outbox and control-plane service must agree on these exact bytes before
// an applied idempotency row can acknowledge a debit.
func CommercialDebitReceiptHash(debit CommercialDebit) (string, error) {
	if !exactIdentifier(debit.EntitlementID) || !exactIdentifier(debit.BillingPeriodID) ||
		!exactIdentifier(debit.MeterEpoch) || !exactIdentifier(debit.IntervalID) ||
		debit.Basis != "UPLINK_PLUS_DOWNLINK" || debit.IntervalEndUnix <= 0 ||
		debit.IntervalEndUnix > maxSQLiteInteger || !validReceiptSHA256(debit.SourceSHA256) {
		return "", ErrInvalidSource
	}
	payload, err := json.Marshal(struct {
		Version         int    `json:"version"`
		CommandType     string `json:"command_type"`
		EntitlementID   string `json:"entitlement_id"`
		PeriodID        string `json:"period_id"`
		MeterEpoch      string `json:"meter_epoch"`
		IntervalID      string `json:"interval_id"`
		Basis           string `json:"basis"`
		IntervalEndUnix int64  `json:"interval_end_unix"`
		SourceSHA256    string `json:"source_sha256"`
	}{
		2, CommercialDebitReceiptCommand, debit.EntitlementID, debit.BillingPeriodID,
		debit.MeterEpoch, debit.IntervalID, debit.Basis, debit.IntervalEndUnix,
		debit.SourceSHA256,
	})
	if err != nil {
		return "", ErrInvalidSource
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validReceiptSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
