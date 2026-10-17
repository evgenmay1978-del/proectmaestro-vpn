package whitelistready_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/shadowbilling"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/testsupport/whitelistfixture"
	v1 "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistapi/v1"
)

// These tests compose production-domain values using synthetic fixtures only.
// Revocation orchestration, cache invalidation and any balance adapter remain
// NOT_RUN; this suite is FIXTURE_REPLAY evidence and cannot upgrade NO_GO.

func integrationCredential() controlplane.WhiteListCredential {
	return controlplane.WhiteListCredential{
		ClientID:                 "11111111-1111-4111-8111-111111111111",
		ClientEncryption:         "mlkem768x25519plus.native.0rtt.test-client-material",
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:b150c646913ddf355a539ca3ae147919cbbae7141c3783d7860cfbbb9062424a",
	}
}

func integrationProfile() controlplane.TransportProfile {
	return controlplane.TransportProfile{
		ID:                    "profile-a",
		PublicHost:            "cdn.example.invalid",
		SecretPath:            "/static/test/segment.ts/opaque",
		OriginRouteID:         "origin-route-a",
		CompatibilityPresetID: "preset-a",
	}
}

func integrationPreset() controlplane.CompatibilityPreset {
	return controlplane.CompatibilityPreset{
		ID: "preset-a", Version: 1, Kind: "MAESTRO_ADVANCED", ProtectionLevel: "advanced",
		Capabilities: []string{"vless-encryption", "xhttp-get-body"},
		CoreRange:    "xray>=26.7.28", ClientRanges: []string{"maestrovpn>=154"}, FixtureRefs: []string{"fixture-a"},
		Protocol: "vless", Network: "xhttp", Port: 443, TLS: true,
		Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ALPN: []string{"h2"}, Fingerprint: "firefox", ExtraJSON: `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id"}`, LabelPrefix: "БС/Yandex", DomainFallback: true,
	}
}

func integrationRelease(t *testing.T, releaseID string, candidates []controlplane.EdgeCandidate) controlplane.TransportRelease {
	t.Helper()
	edges := make([]controlplane.ApprovedEdge, 0, len(candidates))
	for index, candidate := range candidates {
		edge, err := candidate.Approve(time.Unix(int64(index+1), 0).UTC(), "evidence-"+candidate.ID)
		if err != nil {
			t.Fatalf("Approve(%q): %v", candidate.ID, err)
		}
		edges = append(edges, edge)
	}
	release, err := controlplane.NewTransportRelease(controlplane.TransportReleaseSpec{
		ID:            releaseID,
		Profile:       integrationProfile(),
		Preset:        integrationPreset(),
		State:         controlplane.TransportReleasePublished,
		ApprovedEdges: edges,
	})
	if err != nil {
		t.Fatalf("NewTransportRelease(%q): %v", releaseID, err)
	}
	return release
}

func integrationEntitlement(t *testing.T) controlplane.WhiteListEntitlement {
	t.Helper()
	return whitelistfixture.MustPersisted(t, "account-alpha")
}

func integrationActivate(t *testing.T, entitlement controlplane.WhiteListEntitlement, releaseID string) controlplane.WhiteListEntitlement {
	t.Helper()
	active, err := entitlement.Activate("profile-a", "preset-a", releaseID, integrationCredential())
	if err != nil {
		t.Fatalf("Activate(%q): %v", releaseID, err)
	}
	return active
}

func nodeAddresses(result subgen.WhiteListSubscriptionResult) []string {
	addresses := make([]string, 0, len(result.WhiteListNodes))
	for _, node := range result.WhiteListNodes {
		addresses = append(addresses, node.Address)
	}
	return addresses
}

func TestIntegrationFixtureCompositionSubscriptionLifecycleAndEdgeRotation(t *testing.T) {
	releaseA := integrationRelease(t, "release-a", []controlplane.EdgeCandidate{
		{ID: "edge-b", TransportProfileID: "profile-a", Address: "1.1.1.11"},
		{ID: "edge-a", TransportProfileID: "profile-a", Address: "8.8.8.12"},
	})
	disabled := integrationEntitlement(t)
	stableEntitlementID := disabled.EntitlementID()
	if stableEntitlementID == "" {
		t.Fatal("disabled entitlement has no stable server-side id")
	}
	if identity, ok := disabled.XrayIdentity(); ok || identity != "" {
		t.Fatalf("disabled entitlement exposed Xray identity=%q ok=%v", identity, ok)
	}
	ordinary := subgen.OrdinarySubscription{
		AccountID: "account-alpha",
		Identity:  "ordinary-subscription-alpha",
		Output:    "vmess://ordinary\r\nvless://ordinary\r\n",
	}

	off := subgen.RenderWhiteListSubscription(ordinary, disabled, releaseA)
	if off.Ordinary != ordinary || len(off.WhiteListNodes) != 0 || off.Diagnostic != nil {
		t.Fatalf("OFF changed ordinary subscription: %#v", off)
	}

	active := integrationActivate(t, disabled, "release-a")
	wantXrayIdentity := "wl:" + stableEntitlementID
	if identity, ok := active.XrayIdentity(); !ok || identity != wantXrayIdentity || active.EntitlementID() != stableEntitlementID {
		t.Fatalf("activation identity binding: entitlement=%q xray=%q ok=%v", active.EntitlementID(), identity, ok)
	}
	activeResult := subgen.RenderWhiteListSubscription(ordinary, active, releaseA)
	if activeResult.Ordinary != ordinary || activeResult.Diagnostic != nil {
		t.Fatalf("ACTIVE changed ordinary subscription: %#v", activeResult)
	}
	if got, want := nodeAddresses(activeResult), []string{"8.8.8.12", "1.1.1.11", "cdn.example.invalid"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("release-a addresses=%v, want %v", got, want)
	}
	activeJSON, err := json.Marshal(activeResult)
	if err != nil {
		t.Fatalf("marshal ACTIVE: %v", err)
	}

	// DISABLED/EXPIRED are lifecycle rendering checks, not evidence that a
	// production revocation workflow exists.
	for _, state := range []controlplane.EntitlementState{
		controlplane.EntitlementDisabled,
		controlplane.EntitlementSuspended,
		controlplane.EntitlementExpired,
	} {
		nonActive, err := active.WithState(state)
		if err != nil {
			t.Fatalf("WithState(%q): %v", state, err)
		}
		result := subgen.RenderWhiteListSubscription(ordinary, nonActive, releaseA)
		if result.Ordinary != ordinary || len(result.WhiteListNodes) != 0 || result.Diagnostic != nil {
			t.Fatalf("state %q changed ordinary path: %#v", state, result)
		}
		if nonActive.EntitlementID() != stableEntitlementID {
			t.Fatalf("state %q changed entitlement id", state)
		}
	}

	mismatch := integrationActivate(t, active, "release-other")
	mismatchResult := subgen.RenderWhiteListSubscription(ordinary, mismatch, releaseA)
	if mismatchResult.Ordinary != ordinary || len(mismatchResult.WhiteListNodes) != 0 || mismatchResult.Diagnostic == nil || mismatchResult.Diagnostic.Code != subgen.DiagnosticReleaseMismatch {
		t.Fatalf("release mismatch did not fail only additive nodes: %#v", mismatchResult)
	}

	// Deterministic rerendering is not evidence of cache invalidation.
	suspended, err := active.WithState(controlplane.EntitlementSuspended)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	resumed, err := suspended.WithState(controlplane.EntitlementActive)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumedJSON, err := json.Marshal(subgen.RenderWhiteListSubscription(ordinary, resumed, releaseA))
	if err != nil {
		t.Fatalf("marshal resumed: %v", err)
	}
	if string(resumedJSON) != string(activeJSON) {
		t.Fatalf("suspend/resume changed deterministic rendering:\nactive=%s\nresumed=%s", activeJSON, resumedJSON)
	}

	releaseB := integrationRelease(t, "release-b", []controlplane.EdgeCandidate{
		{ID: "edge-c", TransportProfileID: "profile-a", Address: "9.9.9.9"},
	})
	rotated := integrationActivate(t, suspended, "release-b")
	rotatedResult := subgen.RenderWhiteListSubscription(ordinary, rotated, releaseB)
	if got, want := nodeAddresses(rotatedResult), []string{"9.9.9.9", "cdn.example.invalid"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("release-b addresses=%v, want only new edge plus fallback %v", got, want)
	}
	if rotatedResult.Ordinary != ordinary || rotatedResult.Diagnostic != nil || rotated.EntitlementID() != stableEntitlementID {
		t.Fatalf("rotation changed ordinary or entitlement identity: result=%#v entitlement=%q", rotatedResult, rotated.EntitlementID())
	}
	if identity, ok := rotated.XrayIdentity(); !ok || identity != wantXrayIdentity {
		t.Fatalf("rotation changed Xray identity=%q ok=%v", identity, ok)
	}
}

func integrationPolicy(t *testing.T, entitlement controlplane.WhiteListEntitlement) shadowbilling.Policy {
	t.Helper()
	policy, err := shadowbilling.NewPolicy(entitlement, shadowbilling.PolicySpec{
		BillingPeriodID: "period-1",
		Unit:            shadowbilling.UnitGBDecimal, Basis: shadowbilling.BasisUplinkPlusDownlink, IncludedBytes: 100,
		SoftLimitBytes: 150, HardLimitBytes: 200, GraceBytes: 10,
		Prices: shadowbilling.PriceOptions{Global: &shadowbilling.Price{Mode: shadowbilling.PricePaid, Currency: "RUB", MinorUnitsPerUnit: 25000}},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func integrationEvent(id, epoch, identity string, up, down uint64) shadowbilling.UsageEvent {
	return shadowbilling.UsageEvent{
		EventID: id, InstanceID: "s2-xray-a", MeterEpoch: epoch, XrayIdentity: identity,
		UplinkBytes: up, DownlinkBytes: down,
	}
}

func integrationOrderedEvent(id, epoch, identity string, generation, sequence, up, down uint64) shadowbilling.OrderedUsageEvent {
	return shadowbilling.OrderedUsageEvent{
		UsageEvent:        integrationEvent(id, epoch, identity, up, down),
		CounterGeneration: generation,
		SampleSequence:    sequence,
	}
}

func TestIntegrationFixtureCompositionOrderedMeteringIgnoresLateSample(t *testing.T) {
	active := integrationActivate(t, integrationEntitlement(t), "release-a")
	identity, ok := active.XrayIdentity()
	if !ok {
		t.Fatal("active entitlement has no Xray identity")
	}
	policy := integrationPolicy(t, active)
	policy.Basis = shadowbilling.BasisDownlinkOnly
	policy.IncludedBytes = 0
	policy.SoftLimitBytes = 0
	policy.HardLimitBytes = 0
	policy.GraceBytes = 0

	state, decision, err := shadowbilling.ApplyOrdered(shadowbilling.NewState(), integrationOrderedEvent("baseline", "epoch-ordered", identity, 1, 1, 0, 0), policy)
	if err != nil || decision.Diagnostic != shadowbilling.DiagnosticEpochStarted || decision.Interval != nil {
		t.Fatalf("baseline=%#v err=%v", decision, err)
	}
	state, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("accepted-100", "epoch-ordered", identity, 1, 3, 0, 100), policy)
	if err != nil || decision.Interval == nil || decision.Interval.BillableBytes != 100 {
		t.Fatalf("accepted 100=%#v err=%v", decision, err)
	}

	late := integrationOrderedEvent("late-50", "epoch-ordered", identity, 1, 2, 0, 50)
	beforeLateLedger := state.LedgerEntries()
	state, decision, err = shadowbilling.ApplyOrdered(state, late, policy)
	if err != nil || decision.Replay || decision.Diagnostic != shadowbilling.DiagnosticLateSample || decision.Interval != nil || decision.Ledger != nil || !reflect.DeepEqual(state.LedgerEntries(), beforeLateLedger) {
		t.Fatalf("late 50 was not ignored: ledger=%#v decision=%#v err=%v", state.LedgerEntries(), decision, err)
	}
	state, decision, err = shadowbilling.ApplyOrdered(state, late, policy)
	if err != nil || decision.Replay || decision.Diagnostic != shadowbilling.DiagnosticLateSample || decision.Interval != nil || decision.Ledger != nil || !reflect.DeepEqual(state.LedgerEntries(), beforeLateLedger) {
		t.Fatalf("late retry was recorded as replay or mutated ledger: ledger=%#v decision=%#v err=%v", state.LedgerEntries(), decision, err)
	}

	state, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("fresh-110", "epoch-ordered", identity, 1, 4, 0, 110), policy)
	if err != nil || decision.Interval == nil || decision.Interval.DownlinkBytes != 10 || decision.Interval.BillableBytes != 10 || len(state.LedgerEntries()) != 2 {
		t.Fatalf("fresh 110=%#v ledger=%d err=%v; want only 10 billed", decision, len(state.LedgerEntries()), err)
	}
	amount := decision.Ledger.CalculatedAmount
	if amount.Numerator != "1" || amount.Denominator != 4000 || amount.Currency != "RUB" {
		t.Fatalf("fresh amount=%#v, want exact 1/4000 RUB", amount)
	}
}

func TestIntegrationFixtureCompositionShadowMeteringKeysResetReplay(t *testing.T) {
	active := integrationActivate(t, integrationEntitlement(t), "release-a")
	identity, ok := active.XrayIdentity()
	if !ok {
		t.Fatal("active entitlement has no Xray identity")
	}
	policy := integrationPolicy(t, active)
	state := shadowbilling.NewState()
	wrong := integrationOrderedEvent("event-1", "epoch-1", "ordinary:existing-vpn", 1, 1, 10, 90)
	got, decision, err := shadowbilling.ApplyOrdered(state, wrong, policy)
	if !errors.Is(err, shadowbilling.ErrIdentityMismatch) || len(got.LedgerEntries()) != 0 || decision != (shadowbilling.Decision{}) {
		t.Fatalf("ordinary identity was not rejected atomically: state=%#v decision=%#v err=%v", got, decision, err)
	}

	state, decision, err = shadowbilling.ApplyOrdered(got, integrationOrderedEvent("event-1", "epoch-1", identity, 1, 1, 10, 90), policy)
	if err != nil || decision.Replay || decision.Interval != nil || decision.Diagnostic != shadowbilling.DiagnosticEpochStarted {
		t.Fatalf("baseline=%#v err=%v", decision, err)
	}
	state, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("event-2", "epoch-1", identity, 1, 2, 30, 180), policy)
	if err != nil || decision.Interval == nil || decision.Ledger == nil {
		t.Fatalf("positive delta=%#v err=%v", decision, err)
	}
	interval := decision.Interval
	if interval.UplinkBytes != 20 || interval.DownlinkBytes != 90 || interval.BillableBytes != 10 {
		t.Fatalf("positive interval=%#v", interval)
	}
	if gotKeys, wantKeys := []string{interval.AccountID, interval.EntitlementID, interval.TransportID, interval.BillingPeriodID, interval.InstanceID, interval.MeterEpoch, interval.XrayIdentity}, []string{"account-alpha", active.EntitlementID(), "profile-a", "period-1", "s2-xray-a", "epoch-1", identity}; !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("interval keys=%v, want %v", gotKeys, wantKeys)
	}
	amount := decision.Ledger.CalculatedAmount
	if amount.Numerator != "1" || amount.Denominator != 4000 || amount.Currency != "RUB" {
		t.Fatalf("exact amount=%#v, want 1/4000 RUB", amount)
	}

	state, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("event-2", "epoch-1", identity, 1, 2, 30, 180), policy)
	if err != nil || !decision.Replay || decision.Interval != nil || len(state.LedgerEntries()) != 1 {
		t.Fatalf("duplicate replay=%#v ledger=%d err=%v", decision, len(state.LedgerEntries()), err)
	}
	beforeRegressionLedger := state.LedgerEntries()
	got, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("event-3", "epoch-1", identity, 1, 3, 5, 8), policy)
	if !errors.Is(err, shadowbilling.ErrResetGenerationRequired) || decision != (shadowbilling.Decision{}) || !reflect.DeepEqual(got.LedgerEntries(), beforeRegressionLedger) {
		t.Fatalf("same-generation regression was not rejected atomically: ledger=%#v decision=%#v err=%v", got.LedgerEntries(), decision, err)
	}
	state, decision, err = shadowbilling.ApplyOrdered(got, integrationOrderedEvent("event-4", "epoch-1", identity, 2, 1, 5, 8), policy)
	if err != nil || decision.Interval != nil || decision.Diagnostic != shadowbilling.DiagnosticCounterReset || !reflect.DeepEqual(state.LedgerEntries(), beforeRegressionLedger) {
		t.Fatalf("generation reset did not rebaseline without billing: ledger=%#v decision=%#v err=%v", state.LedgerEntries(), decision, err)
	}
	state, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("event-5", "epoch-1", identity, 2, 2, 10, 18), policy)
	if err != nil || decision.Interval == nil || decision.Interval.UplinkBytes != 5 || decision.Interval.DownlinkBytes != 10 || decision.Interval.BillableBytes != 15 || len(state.LedgerEntries()) != 2 {
		t.Fatalf("post-reset interval=%#v ledger=%d err=%v", decision.Interval, len(state.LedgerEntries()), err)
	}
	state, decision, err = shadowbilling.ApplyOrdered(state, integrationOrderedEvent("event-6", "epoch-2", identity, 1, 1, 1, 2), policy)
	if err != nil || decision.Interval != nil || decision.Diagnostic != shadowbilling.DiagnosticEpochStarted || len(state.LedgerEntries()) != 2 {
		t.Fatalf("new epoch=%#v ledger=%d err=%v", decision, len(state.LedgerEntries()), err)
	}
}

func TestIntegrationFixtureCompositionPrivateFixtureValidation(t *testing.T) {
	active := integrationActivate(t, integrationEntitlement(t), "release-a")
	identity, ok := active.XrayIdentity()
	if !ok {
		t.Fatal("active entitlement has no Xray identity")
	}
	policy := integrationPolicy(t, active)
	policy.Basis = shadowbilling.BasisDownlinkOnly
	state, _, err := shadowbilling.Apply(shadowbilling.NewState(), integrationEvent("event-0", "epoch-1", identity, 0, 0), policy)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	state, decision, err := shadowbilling.Apply(state, integrationEvent("event-1", "epoch-1", identity, 0, 1000), policy)
	if err != nil || decision.Ledger == nil {
		t.Fatalf("ledger interval=%#v err=%v", decision, err)
	}
	entry := state.LedgerEntries()[0]
	now := time.Unix(1700000000, 0).UTC()
	amount := v1.ExactAmount{
		Numerator: entry.CalculatedAmount.Numerator, Denominator: entry.CalculatedAmount.Denominator, Currency: entry.CalculatedAmount.Currency,
	}
	lastSample := now
	// This is deliberately a fixture-only projection. Production still needs a
	// reviewed domain-to-v1 adapter before release readiness can advance.
	fixtures := v1.Fixtures{
		Entitlement: v1.Entitlement{
			ID: active.EntitlementID(), AccountID: active.AccountID(), State: v1.EntitlementActive,
			TransportProfileID: active.TransportProfileID(), CompatibilityPresetID: active.CompatibilityPresetID(),
			TransportReleaseID: active.TransportReleaseID(), BillingEnabled: true, UpdatedAt: now,
		},
		Health: v1.Health{
			AccountID: active.AccountID(), Status: v1.HealthHealthy, CollectorStatus: v1.HealthHealthy,
			XrayStatus: v1.HealthHealthy, DataPlaneReleaseID: active.TransportReleaseID(), Fresh: true,
			LastMeterSampleAt: &lastSample, ObservedAt: now,
		},
		Usage: v1.Usage{
			AccountID: active.AccountID(), EntitlementID: active.EntitlementID(), BillingPeriodID: policy.BillingPeriodID(),
			Unit: v1.UnitGBDecimal, Basis: v1.BasisDownlinkOnly, MeasuredBytes: 1000,
			IncludedBytes: policy.IncludedBytes, BillableBytes: entry.Interval.BillableBytes, RemainingIncludedBytes: 0,
			SoftLimitBytes: policy.SoftLimitBytes, HardLimitBytes: policy.HardLimitBytes, GraceBytes: policy.GraceBytes,
			AccruedAmount: amount, UpdatedAt: now,
		},
		Ledger: v1.Page[v1.LedgerEntry]{Items: []v1.LedgerEntry{{
			ID: "ledger-event-1", EventID: entry.EventID, AccountID: entry.Interval.AccountID,
			EntitlementID: entry.Interval.EntitlementID, BillingPeriodID: entry.Interval.BillingPeriodID,
			BillableBytes: entry.Interval.BillableBytes, Unit: v1.UnitGBDecimal, Basis: v1.BasisDownlinkOnly,
			PriceSource: v1.PriceGlobal, Amount: amount, OccurredAt: now,
		}}},
		Audit: v1.Page[v1.AuditRecord]{Items: []v1.AuditRecord{{
			ID: "audit-event-1", AccountID: active.AccountID(), ActorID: "task7-fixture",
			Action: "ENTITLEMENT_ENABLED", Reason: "offline integration fixture", OccurredAt: now,
			Changes: []v1.AuditChange{{Field: "state", OldValue: "DISABLED", NewValue: "ACTIVE"}},
		}}},
	}
	if err := fixtures.ValidateForAccount(active.AccountID()); err != nil {
		t.Fatalf("valid private fixture: %v", err)
	}
	fixtures.Ledger.Items[0].AccountID = "account-other"
	if err := fixtures.ValidateForAccount(active.AccountID()); err == nil {
		t.Fatal("cross-account nested ledger fixture was accepted")
	}
}
