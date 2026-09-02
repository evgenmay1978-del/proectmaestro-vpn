package shadowbilling

import (
	"errors"
	"math"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func validCommercialOrderedEvent(policy Policy) CommercialOrderedUsageEvent {
	ordered := orderedEvent(policy, "commercial-event-1", 4, 5, 456)
	ordered.UplinkBytes = 123
	return CommercialOrderedUsageEvent{
		OrderedUsageEvent: ordered,
		Source: CommercialMeterSource{
			OriginID:          ordered.InstanceID,
			ExitID:            "exit-nl",
			CounterSourceID:   "xray-api:origin-s2:exit-nl",
			XrayProcessBootID: "boot-a",
			ResetSequence:     3,
			RouteXrayIdentity: ordered.XrayIdentity + ":exit-nl",
		},
		SampledAtUnix: 2_100_000_000,
	}
}

func TestBindCommercialMeteringSourceReturnsExactImmutableBinding(t *testing.T) {
	policy := paidPolicy()
	event := validCommercialOrderedEvent(policy)
	binding, err := BindCommercialMeteringSource(event, policy)
	if err != nil {
		t.Fatalf("BindCommercialMeteringSource: %v", err)
	}
	if binding.EventID != event.EventID || binding.AccountID != policy.AccountID() ||
		binding.EntitlementID != policy.EntitlementID() || binding.TransportID != policy.TransportID() ||
		binding.BillingPeriodID != policy.BillingPeriodID() || binding.OriginID != event.Source.OriginID ||
		binding.ExitID != event.Source.ExitID || binding.MeterEpoch != event.MeterEpoch ||
		binding.CounterSourceID != event.Source.CounterSourceID ||
		binding.XrayProcessBootID != event.Source.XrayProcessBootID ||
		binding.ResetSequence != event.Source.ResetSequence ||
		binding.BaseXrayIdentity != event.XrayIdentity || binding.RouteXrayIdentity != event.Source.RouteXrayIdentity ||
		binding.Basis != BasisUplinkPlusDownlink || binding.SampledAtUnix != event.SampledAtUnix ||
		binding.CounterGeneration != event.CounterGeneration || binding.SampleSequence != event.SampleSequence ||
		binding.UplinkBytes != event.UplinkBytes || binding.DownlinkBytes != event.DownlinkBytes ||
		len(binding.SourceSHA256) != 64 {
		t.Fatalf("binding=%#v", binding)
	}
	recomputed, err := whitelistmetering.SourceSHA256(whitelistmetering.SourceDigestInput{
		AccountID: binding.AccountID, EntitlementID: binding.EntitlementID,
		TransportID: binding.TransportID, BillingPeriodID: binding.BillingPeriodID,
		Basis: string(binding.Basis), BaseXrayIdentity: binding.BaseXrayIdentity,
		RouteXrayIdentity: binding.RouteXrayIdentity, OriginID: binding.OriginID,
		ExitID: binding.ExitID, CounterSourceID: binding.CounterSourceID,
		XrayProcessBootID: binding.XrayProcessBootID, ResetSequence: binding.ResetSequence,
		MeterEpoch: binding.MeterEpoch, CounterGeneration: binding.CounterGeneration,
		SampleSequence: binding.SampleSequence, UplinkBytes: binding.UplinkBytes,
		DownlinkBytes: binding.DownlinkBytes, SampledAtUnix: binding.SampledAtUnix,
	})
	if err != nil || recomputed != binding.SourceSHA256 {
		t.Fatalf("binding digest=%q recomputed=%q err=%v", binding.SourceSHA256, recomputed, err)
	}

	changedID := event
	changedID.EventID = "commercial-event-retried-under-another-id"
	rebound, err := BindCommercialMeteringSource(changedID, policy)
	if err != nil {
		t.Fatalf("BindCommercialMeteringSource changed EventID: %v", err)
	}
	if rebound.EventID == binding.EventID || rebound.SourceSHA256 != binding.SourceSHA256 {
		t.Fatalf("EventID must be stored but excluded from physical source digest: before=%#v after=%#v", binding, rebound)
	}
}

func TestBindCommercialMeteringSourceRejectsUnsafeBindings(t *testing.T) {
	policy := paidPolicy()
	base := validCommercialOrderedEvent(policy)
	tests := []struct {
		name    string
		policy  func(Policy) Policy
		mutate  func(*CommercialOrderedUsageEvent)
		wantErr error
	}{
		{name: "non commercial basis", policy: func(v Policy) Policy { v.Basis = BasisDownlinkOnly; return v }, wantErr: ErrInvalidInput},
		{name: "ordinary identity", mutate: func(v *CommercialOrderedUsageEvent) { v.XrayIdentity = "ordinary:customer" }, wantErr: ErrIdentityMismatch},
		{name: "origin mismatch", mutate: func(v *CommercialOrderedUsageEvent) { v.Source.OriginID = "origin-s3" }, wantErr: ErrInvalidInput},
		{name: "route mismatch", mutate: func(v *CommercialOrderedUsageEvent) { v.Source.RouteXrayIdentity += "-other" }, wantErr: ErrInvalidInput},
		{name: "unsafe exit", mutate: func(v *CommercialOrderedUsageEvent) { v.Source.ExitID = "exit:other" }, wantErr: ErrInvalidInput},
		{name: "empty counter source", mutate: func(v *CommercialOrderedUsageEvent) { v.Source.CounterSourceID = "" }, wantErr: ErrInvalidInput},
		{name: "zero sample time", mutate: func(v *CommercialOrderedUsageEvent) { v.SampledAtUnix = 0 }, wantErr: ErrInvalidInput},
		{name: "max sample time", mutate: func(v *CommercialOrderedUsageEvent) { v.SampledAtUnix = math.MaxInt64 }, wantErr: ErrInvalidInput},
		{name: "max reset sequence", mutate: func(v *CommercialOrderedUsageEvent) { v.Source.ResetSequence = uint64(math.MaxInt64) }, wantErr: ErrInvalidInput},
		{name: "zero generation", mutate: func(v *CommercialOrderedUsageEvent) { v.CounterGeneration = 0 }, wantErr: ErrInvalidInput},
		{name: "zero sequence", mutate: func(v *CommercialOrderedUsageEvent) { v.SampleSequence = 0 }, wantErr: ErrInvalidInput},
		{name: "invalid utf8 event", mutate: func(v *CommercialOrderedUsageEvent) { v.EventID = string([]byte{0xff}) }, wantErr: ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := base
			candidatePolicy := policy
			if test.policy != nil {
				candidatePolicy = test.policy(candidatePolicy)
			}
			if test.mutate != nil {
				test.mutate(&event)
			}
			if _, err := BindCommercialMeteringSource(event, candidatePolicy); !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want %v", err, test.wantErr)
			}
		})
	}
}
