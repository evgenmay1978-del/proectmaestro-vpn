package shadowbilling

import (
	"strings"
	"unicode/utf8"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

// CommercialMeterSource identifies one exact Xray route counter. The legacy
// v10 event remains bound to the base wl:<entitlement> identity while this
// source retains the exit-specific route identity.
type CommercialMeterSource struct {
	OriginID          string
	ExitID            string
	CounterSourceID   string
	XrayProcessBootID string
	ResetSequence     uint64
	RouteXrayIdentity string
}

type CommercialOrderedUsageEvent struct {
	OrderedUsageEvent
	Source        CommercialMeterSource
	SampledAtUnix int64
}

type CommercialSourceBinding struct {
	EventID           string
	AccountID         string
	EntitlementID     string
	TransportID       string
	BillingPeriodID   string
	OriginID          string
	ExitID            string
	CounterSourceID   string
	XrayProcessBootID string
	ResetSequence     uint64
	MeterEpoch        string
	BaseXrayIdentity  string
	RouteXrayIdentity string
	Basis             TrafficBasis
	CounterGeneration uint64
	SampleSequence    uint64
	UplinkBytes       uint64
	DownlinkBytes     uint64
	SampledAtUnix     int64
	SourceSHA256      string
}

// BindCommercialMeteringSource validates and freezes the commercial source
// identity without writing metering, balance, publication, or ordinary access.
func BindCommercialMeteringSource(
	event CommercialOrderedUsageEvent,
	policy Policy,
) (CommercialSourceBinding, error) {
	if !policy.validBinding() || policy.Basis != BasisUplinkPlusDownlink ||
		!exactCommercialIdentifier(event.EventID) || event.CounterGeneration == 0 ||
		event.SampleSequence == 0 {
		return CommercialSourceBinding{}, ErrInvalidInput
	}
	if event.XrayIdentity != policy.expectedXrayIdentity {
		return CommercialSourceBinding{}, ErrIdentityMismatch
	}
	if event.InstanceID != event.Source.OriginID {
		return CommercialSourceBinding{}, ErrInvalidInput
	}

	digestInput := whitelistmetering.SourceDigestInput{
		AccountID: policy.AccountID(), EntitlementID: policy.EntitlementID(),
		TransportID: policy.TransportID(), BillingPeriodID: policy.BillingPeriodID(),
		Basis: string(policy.Basis), BaseXrayIdentity: event.XrayIdentity,
		RouteXrayIdentity: event.Source.RouteXrayIdentity,
		OriginID:          event.Source.OriginID, ExitID: event.Source.ExitID,
		CounterSourceID:   event.Source.CounterSourceID,
		XrayProcessBootID: event.Source.XrayProcessBootID,
		ResetSequence:     event.Source.ResetSequence, MeterEpoch: event.MeterEpoch,
		CounterGeneration: event.CounterGeneration, SampleSequence: event.SampleSequence,
		UplinkBytes: event.UplinkBytes, DownlinkBytes: event.DownlinkBytes,
		SampledAtUnix: event.SampledAtUnix,
	}
	digest, err := whitelistmetering.SourceSHA256(digestInput)
	if err != nil {
		return CommercialSourceBinding{}, ErrInvalidInput
	}
	return CommercialSourceBinding{
		EventID: event.EventID, AccountID: policy.AccountID(),
		EntitlementID: policy.EntitlementID(), TransportID: policy.TransportID(),
		BillingPeriodID: policy.BillingPeriodID(), OriginID: event.Source.OriginID,
		ExitID: event.Source.ExitID, CounterSourceID: event.Source.CounterSourceID,
		XrayProcessBootID: event.Source.XrayProcessBootID,
		ResetSequence:     event.Source.ResetSequence, MeterEpoch: event.MeterEpoch,
		BaseXrayIdentity:  event.XrayIdentity,
		RouteXrayIdentity: event.Source.RouteXrayIdentity, Basis: policy.Basis,
		CounterGeneration: event.CounterGeneration, SampleSequence: event.SampleSequence,
		UplinkBytes: event.UplinkBytes, DownlinkBytes: event.DownlinkBytes,
		SampledAtUnix: event.SampledAtUnix, SourceSHA256: digest,
	}, nil
}

func exactCommercialIdentifier(value string) bool {
	return value != "" && utf8.ValidString(value) &&
		value == strings.TrimSpace(value) && !strings.ContainsRune(value, '\x00')
}
