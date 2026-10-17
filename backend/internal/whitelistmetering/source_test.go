package whitelistmetering

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

func validSourceDigestInput() SourceDigestInput {
	return SourceDigestInput{
		AccountID:         "account-a",
		EntitlementID:     "wl-ent-11111111111111111111111111111111",
		TransportID:       "yandex-cdn",
		BillingPeriodID:   "period-1",
		Basis:             "UPLINK_PLUS_DOWNLINK",
		BaseXrayIdentity:  "wl:wl-ent-11111111111111111111111111111111",
		RouteXrayIdentity: "wl:wl-ent-11111111111111111111111111111111:exit-nl",
		OriginID:          "origin-s2",
		ExitID:            "exit-nl",
		CounterSourceID:   "xray-api:origin-s2:exit-nl",
		XrayProcessBootID: "boot-a",
		ResetSequence:     3,
		MeterEpoch:        "origin-s2-exit-nl-boot-a-reset-3",
		CounterGeneration: 4,
		SampleSequence:    5,
		UplinkBytes:       123,
		DownlinkBytes:     456,
		SampledAtUnix:     2_100_000_000,
	}
}

func TestSourceSHA256UsesFrozenCanonicalV1Payload(t *testing.T) {
	input := validSourceDigestInput()
	got, err := SourceSHA256(input)
	if err != nil {
		t.Fatalf("SourceSHA256: %v", err)
	}
	canonical := `{"version":1,"entitlement_id":"wl-ent-11111111111111111111111111111111","base_xray_identity":"wl:wl-ent-11111111111111111111111111111111","route_xray_identity":"wl:wl-ent-11111111111111111111111111111111:exit-nl","origin_id":"origin-s2","exit_id":"exit-nl","counter_source_id":"xray-api:origin-s2:exit-nl","xray_process_boot_id":"boot-a","reset_sequence":3,"meter_epoch":"origin-s2-exit-nl-boot-a-reset-3","counter_generation":4,"sample_sequence":5,"uplink_bytes":123,"downlink_bytes":456,"sampled_at_unix":2100000000}`
	wantBytes := sha256.Sum256([]byte(canonical))
	want := hex.EncodeToString(wantBytes[:])
	if got != want {
		t.Fatalf("digest=%q, want frozen canonical digest %q", got, want)
	}
	if len(got) != 64 || got != strings.ToLower(got) {
		t.Fatalf("digest is not canonical lowercase SHA-256: %q", got)
	}
	again, err := SourceSHA256(input)
	if err != nil || again != got {
		t.Fatalf("repeat digest=%q err=%v, want %q", again, err, got)
	}
}

func TestSourceSHA256RejectsCrossPeriodReplayWithSamePhysicalDigest(t *testing.T) {
	first := validSourceDigestInput()
	firstDigest, err := SourceSHA256(first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.AccountID = "account-renamed"
	second.TransportID = "yandex-cdn-v2"
	second.BillingPeriodID = "period-2"
	secondDigest, err := SourceSHA256(second)
	if err != nil {
		t.Fatal(err)
	}
	if secondDigest != firstDigest {
		t.Fatalf("commercial context changed physical digest: first=%q second=%q", firstDigest, secondDigest)
	}
}

func TestSourceSHA256BindsPhysicalSampleFields(t *testing.T) {
	base := validSourceDigestInput()
	want, err := SourceSHA256(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*SourceDigestInput){
		"origin":             func(v *SourceDigestInput) { v.OriginID = "origin-s3" },
		"counter source":     func(v *SourceDigestInput) { v.CounterSourceID += "-other" },
		"process boot":       func(v *SourceDigestInput) { v.XrayProcessBootID += "-other" },
		"reset sequence":     func(v *SourceDigestInput) { v.ResetSequence++ },
		"meter epoch":        func(v *SourceDigestInput) { v.MeterEpoch += "-other" },
		"counter generation": func(v *SourceDigestInput) { v.CounterGeneration++ },
		"sample sequence":    func(v *SourceDigestInput) { v.SampleSequence++ },
		"uplink":             func(v *SourceDigestInput) { v.UplinkBytes++ },
		"downlink":           func(v *SourceDigestInput) { v.DownlinkBytes++ },
		"sample time":        func(v *SourceDigestInput) { v.SampledAtUnix++ },
		"exit and route": func(v *SourceDigestInput) {
			v.ExitID = "exit-ru"
			v.RouteXrayIdentity = v.BaseXrayIdentity + ":" + v.ExitID
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			got, err := SourceSHA256(changed)
			if err != nil {
				t.Fatalf("SourceSHA256: %v", err)
			}
			if got == want {
				t.Fatalf("physical/source mutation did not change digest %q", got)
			}
		})
	}
}

func TestSourceSHA256RejectsNonCommercialOrAmbiguousBinding(t *testing.T) {
	base := validSourceDigestInput()
	cases := map[string]func(*SourceDigestInput){
		"wrong basis":         func(v *SourceDigestInput) { v.Basis = "DOWNLINK_ONLY" },
		"wrong base":          func(v *SourceDigestInput) { v.BaseXrayIdentity = "ordinary:customer" },
		"wrong route":         func(v *SourceDigestInput) { v.RouteXrayIdentity += "-other" },
		"unsafe exit":         func(v *SourceDigestInput) { v.ExitID = "exit:other" },
		"blank source":        func(v *SourceDigestInput) { v.CounterSourceID = "" },
		"trimmed source":      func(v *SourceDigestInput) { v.OriginID = " origin-s2" },
		"nul source":          func(v *SourceDigestInput) { v.XrayProcessBootID = "boot\x00a" },
		"invalid utf8 source": func(v *SourceDigestInput) { v.CounterSourceID = string([]byte{0xff}) },
		"zero generation":     func(v *SourceDigestInput) { v.CounterGeneration = 0 },
		"zero sequence":       func(v *SourceDigestInput) { v.SampleSequence = 0 },
		"zero sample time":    func(v *SourceDigestInput) { v.SampledAtUnix = 0 },
		"max sample time":     func(v *SourceDigestInput) { v.SampledAtUnix = math.MaxInt64 },
		"max reset sequence":  func(v *SourceDigestInput) { v.ResetSequence = uint64(math.MaxInt64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if digest, err := SourceSHA256(changed); err == nil {
				t.Fatalf("accepted invalid binding with digest %q", digest)
			}
		})
	}
}
