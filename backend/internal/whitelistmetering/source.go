// Package whitelistmetering defines dependency-neutral canonical bindings for
// commercial white-list traffic samples. It contains no database or runtime
// publication behavior.
package whitelistmetering

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const maxSQLiteInteger int64 = 9223372036854775806

var ErrInvalidSource = errors.New("whitelistmetering: invalid commercial source")

// SourceDigestInput is the immutable physical counter sample plus its resolved
// commercial binding. Event IDs, billing periods, account metadata, tariff
// context, and processing timestamps are excluded from the digest so the same
// physical sample cannot be accepted again under another event or period.
type SourceDigestInput struct {
	AccountID         string
	EntitlementID     string
	TransportID       string
	BillingPeriodID   string
	Basis             string
	BaseXrayIdentity  string
	RouteXrayIdentity string
	OriginID          string
	ExitID            string
	CounterSourceID   string
	XrayProcessBootID string
	ResetSequence     uint64
	MeterEpoch        string
	CounterGeneration uint64
	SampleSequence    uint64
	UplinkBytes       uint64
	DownlinkBytes     uint64
	SampledAtUnix     int64
}

type canonicalSourceV1 struct {
	Version           int    `json:"version"`
	EntitlementID     string `json:"entitlement_id"`
	BaseXrayIdentity  string `json:"base_xray_identity"`
	RouteXrayIdentity string `json:"route_xray_identity"`
	OriginID          string `json:"origin_id"`
	ExitID            string `json:"exit_id"`
	CounterSourceID   string `json:"counter_source_id"`
	XrayProcessBootID string `json:"xray_process_boot_id"`
	ResetSequence     uint64 `json:"reset_sequence"`
	MeterEpoch        string `json:"meter_epoch"`
	CounterGeneration uint64 `json:"counter_generation"`
	SampleSequence    uint64 `json:"sample_sequence"`
	UplinkBytes       uint64 `json:"uplink_bytes"`
	DownlinkBytes     uint64 `json:"downlink_bytes"`
	SampledAtUnix     int64  `json:"sampled_at_unix"`
}

// SourceSHA256 returns the canonical v1 digest for one exact physical sample.
func SourceSHA256(input SourceDigestInput) (string, error) {
	if !validSource(input) {
		return "", ErrInvalidSource
	}
	payload, err := json.Marshal(canonicalSourceV1{
		Version: 1, EntitlementID: input.EntitlementID,
		BaseXrayIdentity:  input.BaseXrayIdentity,
		RouteXrayIdentity: input.RouteXrayIdentity, OriginID: input.OriginID,
		ExitID: input.ExitID, CounterSourceID: input.CounterSourceID,
		XrayProcessBootID: input.XrayProcessBootID, ResetSequence: input.ResetSequence,
		MeterEpoch: input.MeterEpoch, CounterGeneration: input.CounterGeneration,
		SampleSequence: input.SampleSequence, UplinkBytes: input.UplinkBytes,
		DownlinkBytes: input.DownlinkBytes, SampledAtUnix: input.SampledAtUnix,
	})
	if err != nil {
		return "", ErrInvalidSource
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validSource(input SourceDigestInput) bool {
	for _, value := range []string{
		input.AccountID, input.EntitlementID, input.TransportID,
		input.BillingPeriodID, input.BaseXrayIdentity, input.RouteXrayIdentity,
		input.OriginID, input.ExitID, input.CounterSourceID,
		input.XrayProcessBootID, input.MeterEpoch,
	} {
		if !exactIdentifier(value) {
			return false
		}
	}
	if !strings.HasPrefix(input.EntitlementID, "wl-ent-") ||
		input.Basis != "UPLINK_PLUS_DOWNLINK" ||
		input.BaseXrayIdentity != "wl:"+input.EntitlementID ||
		input.RouteXrayIdentity != input.BaseXrayIdentity+":"+input.ExitID ||
		!safeExitID(input.ExitID) {
		return false
	}
	return input.ResetSequence <= uint64(maxSQLiteInteger) &&
		input.CounterGeneration > 0 && input.SampleSequence > 0 &&
		input.SampledAtUnix > 0 && input.SampledAtUnix <= maxSQLiteInteger
}

func exactIdentifier(value string) bool {
	return value != "" && utf8.ValidString(value) &&
		value == strings.TrimSpace(value) && !strings.ContainsRune(value, '\x00')
}

func safeExitID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
