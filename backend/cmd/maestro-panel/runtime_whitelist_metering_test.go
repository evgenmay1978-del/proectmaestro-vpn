package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/shadowbilling"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/whitelistmetering"
)

func TestRuntimeWhiteListMeteringRecoversPendingUsesExactCursorAndSkipsUnavailable(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	entitlement := runtimeWhiteListMeteringEntitlement(t, now)
	entitlementID := entitlement.EntitlementID()
	managedEmail := "wl:" + entitlementID + ":exit-nl"
	receipt := controlplane.WhiteListSidecarReceipt{
		ActionKey: "s4:1:action", OriginID: "origin-s4", ReleaseID: "release-1",
		XrayProcessBootID: "boot-s4", ConfigDigest: "config-digest", DesiredGeneration: 1,
		ManagedUserSetDigest: "managed-digest", AppliedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
	control := &runtimeWhiteListMeteringControl{
		plan: controlplane.WhiteListMeteringPlan{
			Origins: []controlplane.WhiteListMeteringOrigin{{
				Origin: controlplane.WhiteListOrigin{OriginID: "origin-s4", NodeID: "s4"},
				Desired: controlplane.WhiteListSidecarDesired{
					Action: controlplane.ExternalActionCommand{ActionKey: receipt.ActionKey},
				},
				Receipt: receipt,
			}},
			Routes: []controlplane.WhiteListMeteringRoute{{
				ManagedEmail: managedEmail, ExitID: "exit-nl", Entitlement: entitlement,
				Policy: controlplane.WhiteListMeteringPolicy{
					BillingPeriodID: "period-1", PeriodStartsAtUnix: now.Add(-time.Hour).Unix(),
					PeriodEndsAtUnix: now.Add(time.Hour).Unix(), Unit: "GB_DECIMAL",
					Basis: "UPLINK_PLUS_DOWNLINK", PriceMode: "FREE", PriceSource: "GLOBAL",
				},
			}},
		},
	}
	sender := &runtimeWhiteListMeteringSender{snapshot: sidecaragentclient.UsageSnapshot{
		Receipt: sidecaragentclient.Receipt{
			ActionKey: receipt.ActionKey, OriginID: receipt.OriginID, ReleaseID: receipt.ReleaseID,
			XrayProcessBootID: receipt.XrayProcessBootID, ConfigDigest: receipt.ConfigDigest,
			DesiredGeneration: receipt.DesiredGeneration, ManagedUserSetDigest: receipt.ManagedUserSetDigest,
			AppliedAt: receipt.AppliedAt, ExpiresAt: receipt.ExpiresAt,
		},
		SampledAt: now.Add(time.Second),
		Users: []sidecaragentclient.UsageUser{{
			Email: managedEmail, UplinkBytes: 11, DownlinkBytes: 17,
		}},
		UnavailableUsers: []string{},
	}}
	store := &runtimeWhiteListMeteringStoreFake{
		pending: []string{entitlementID},
		cursor: shadowbilling.CommercialProducerCursor{
			Source: shadowbilling.CommercialMeterSource{
				OriginID: "origin-s4", ExitID: "exit-nl", CounterSourceID: "wl-counter-bound",
				XrayProcessBootID: receipt.XrayProcessBootID, RouteXrayIdentity: managedEmail,
			},
			MeterEpoch: "wl-meter-epoch", NextSampleSequence: 7,
		},
	}
	collector := &runtimeWhiteListMeteringCollector{
		control: control, store: store, workerID: "worker-1",
		senders: map[string]controlplane.ExternalActionSender{"s4": sender},
	}
	if err := collector.runPass(context.Background()); err != nil {
		t.Fatalf("runPass: %v", err)
	}
	if store.pendingReads != 1 || len(store.drains) != 2 ||
		store.drains[0] != entitlementID || store.drains[1] != entitlementID {
		t.Fatalf("recovery reads=%d drains=%#v", store.pendingReads, store.drains)
	}
	if len(store.sources) != 1 || store.sources[0].CounterSourceID != "xray-api:origin-s4:exit-nl" ||
		store.sources[0].RouteXrayIdentity != managedEmail || store.sources[0].XrayProcessBootID != receipt.XrayProcessBootID {
		t.Fatalf("physical sources = %#v", store.sources)
	}
	if len(store.events) != 1 || store.events[0].Source != store.cursor.Source ||
		store.events[0].EventID != runtimeWhiteListMeteringEventID(store.cursor.MeterEpoch, managedEmail, 7) ||
		store.events[0].CounterGeneration != 1 || store.events[0].SampleSequence != 7 ||
		store.events[0].UplinkBytes != 11 || store.events[0].DownlinkBytes != 17 {
		t.Fatalf("events = %#v", store.events)
	}
	if len(store.policies) != 1 || store.policies[0].EntitlementID() != entitlementID ||
		store.policies[0].BillingPeriodID() != "period-1" || control.reconciles != 1 {
		t.Fatalf("policies=%#v reconciles=%d", store.policies, control.reconciles)
	}

	sender.snapshot.Users = []sidecaragentclient.UsageUser{}
	sender.snapshot.UnavailableUsers = []string{managedEmail}
	if err := collector.runPass(context.Background()); err != nil {
		t.Fatalf("unavailable runPass: %v", err)
	}
	if store.pendingReads != 1 || len(store.sources) != 1 || len(store.events) != 1 ||
		len(store.drains) != 2 || control.reconciles != 1 {
		t.Fatalf("unavailable user produced metering: reads=%d sources=%d events=%d drains=%d reconciles=%d",
			store.pendingReads, len(store.sources), len(store.events), len(store.drains), control.reconciles)
	}
}

func TestRuntimeWhiteListMeteringReconcilesAfterLaterOriginFails(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	entitlement := runtimeWhiteListMeteringEntitlement(t, now)
	entitlementID := entitlement.EntitlementID()
	managedEmail := "wl:" + entitlementID + ":exit-nl"
	firstReceipt := controlplane.WhiteListSidecarReceipt{
		ActionKey: "s4:1:first", OriginID: "origin-s4", ReleaseID: "release-1",
		XrayProcessBootID: "boot-s4", ConfigDigest: "config-s4", DesiredGeneration: 1,
		ManagedUserSetDigest: "managed-digest", AppliedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
	secondReceipt := firstReceipt
	secondReceipt.ActionKey = "s2:1:second"
	secondReceipt.OriginID = "origin-s2"
	secondReceipt.XrayProcessBootID = "boot-s2"
	secondReceipt.ConfigDigest = "config-s2"
	control := &runtimeWhiteListMeteringControl{plan: controlplane.WhiteListMeteringPlan{
		Origins: []controlplane.WhiteListMeteringOrigin{
			{
				Origin: controlplane.WhiteListOrigin{OriginID: firstReceipt.OriginID, NodeID: "s4"},
				Desired: controlplane.WhiteListSidecarDesired{
					Action: controlplane.ExternalActionCommand{ActionKey: firstReceipt.ActionKey},
				},
				Receipt: firstReceipt,
			},
			{
				Origin: controlplane.WhiteListOrigin{OriginID: secondReceipt.OriginID, NodeID: "s2"},
				Desired: controlplane.WhiteListSidecarDesired{
					Action: controlplane.ExternalActionCommand{ActionKey: secondReceipt.ActionKey},
				},
				Receipt: secondReceipt,
			},
		},
		Routes: []controlplane.WhiteListMeteringRoute{{
			ManagedEmail: managedEmail, ExitID: "exit-nl", Entitlement: entitlement,
			Policy: controlplane.WhiteListMeteringPolicy{
				BillingPeriodID: "period-1", PeriodStartsAtUnix: now.Add(-time.Hour).Unix(),
				PeriodEndsAtUnix: now.Add(time.Hour).Unix(), Unit: "GB_DECIMAL",
				Basis: "UPLINK_PLUS_DOWNLINK", PriceMode: "FREE", PriceSource: "GLOBAL",
			},
		}},
	}}
	firstSender := &runtimeWhiteListMeteringSender{snapshot: sidecaragentclient.UsageSnapshot{
		Receipt: sidecaragentclient.Receipt{
			ActionKey: firstReceipt.ActionKey, OriginID: firstReceipt.OriginID, ReleaseID: firstReceipt.ReleaseID,
			XrayProcessBootID: firstReceipt.XrayProcessBootID, ConfigDigest: firstReceipt.ConfigDigest,
			DesiredGeneration: firstReceipt.DesiredGeneration, ManagedUserSetDigest: firstReceipt.ManagedUserSetDigest,
			AppliedAt: firstReceipt.AppliedAt, ExpiresAt: firstReceipt.ExpiresAt,
		},
		SampledAt:        now.Add(time.Second),
		Users:            []sidecaragentclient.UsageUser{{Email: managedEmail, UplinkBytes: 1, DownlinkBytes: 2}},
		UnavailableUsers: []string{},
	}}
	store := &runtimeWhiteListMeteringStoreFake{cursor: shadowbilling.CommercialProducerCursor{
		Source: shadowbilling.CommercialMeterSource{
			OriginID: firstReceipt.OriginID, ExitID: "exit-nl", CounterSourceID: "wl-counter-bound",
			XrayProcessBootID: firstReceipt.XrayProcessBootID, RouteXrayIdentity: managedEmail,
		},
		MeterEpoch: "wl-meter-epoch", NextSampleSequence: 1,
	}}
	collector := &runtimeWhiteListMeteringCollector{
		control: control, store: store, workerID: "worker-1", startupRecovered: true,
		senders: map[string]controlplane.ExternalActionSender{
			"s4": firstSender,
			"s2": &runtimeWhiteListMeteringSender{lookupErr: errors.New("later origin unavailable")},
		},
	}
	if err := collector.runPass(context.Background()); !errors.Is(err, errRuntimeWhiteListMeteringUnavailable) {
		t.Fatalf("runPass error = %v, want unavailable", err)
	}
	if len(store.events) != 1 || len(store.drains) != 1 || control.reconciles != 1 || collector.reconcileNeeded {
		t.Fatalf("events=%d drains=%d reconciles=%d pending=%v",
			len(store.events), len(store.drains), control.reconciles, collector.reconcileNeeded)
	}
}

func runtimeWhiteListMeteringEntitlement(t *testing.T, now time.Time) controlplane.WhiteListEntitlement {
	t.Helper()
	database := newPublicationSQLite(t)
	if err := controlplane.NewMigrator(database).Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := controlplane.NewSecretBox(
		1, map[int][]byte{1: bytes.Repeat([]byte{0x31}, 32)}, bytes.Repeat([]byte{0x42}, 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := controlplane.NewStore(database, box, runtimeTestClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	service, err := controlplane.NewService(store, cryptoRuntimeIDs{}, runtimeTestClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	customer, err := service.ProvisionCustomer(context.Background(), controlplane.ProvisionCustomerCommand{
		Login: "Metering", Days: 30, IdempotencyKey: "runtime-metering",
	})
	if err != nil {
		t.Fatal(err)
	}
	entitlement, err := service.EnsureWhiteListEntitlement(context.Background(), customer.ID)
	if err != nil {
		t.Fatal(err)
	}
	encryption := "mlkem768x25519plus.native.0rtt.runtimeMeteringClient"
	proof := sha256.Sum256([]byte("maestrovpn:vlessenc-client:v1\x00CLIENT\x00" + encryption))
	entitlement, err = entitlement.Activate("profile-1", "preset-1", "release-1", controlplane.WhiteListCredential{
		ClientID: "11111111-1111-4111-8111-111111111111", ClientEncryption: encryption,
		ClientEncryptionRole:     "CLIENT",
		ClientEncryptionProofRef: "xray-vlessenc-client-v1:sha256:" + hex.EncodeToString(proof[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	return entitlement
}

type runtimeWhiteListMeteringSender struct {
	snapshot  sidecaragentclient.UsageSnapshot
	lookupErr error
}

func (*runtimeWhiteListMeteringSender) Post(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("unexpected sidecar mutation")
}

func (sender *runtimeWhiteListMeteringSender) LookupUsage(context.Context, string) (sidecaragentclient.UsageSnapshot, error) {
	return sender.snapshot, sender.lookupErr
}

type runtimeWhiteListMeteringControl struct {
	plan       controlplane.WhiteListMeteringPlan
	reconciles int
}

func (control *runtimeWhiteListMeteringControl) WhiteListMeteringPlan(context.Context) (controlplane.WhiteListMeteringPlan, error) {
	return control.plan, nil
}

func (*runtimeWhiteListMeteringControl) DebitCommercialInterval(context.Context, whitelistmetering.CommercialDebit) error {
	return nil
}

func (control *runtimeWhiteListMeteringControl) ReconcileWhiteListSidecarIntents(
	_ context.Context,
	_ string,
	resolve func(string) (controlplane.ExternalActionSender, bool),
) error {
	if sender, ok := resolve("s4"); !ok || sender == nil {
		return errors.New("missing test sender")
	}
	control.reconciles++
	return nil
}

type runtimeWhiteListMeteringStoreFake struct {
	pending      []string
	cursor       shadowbilling.CommercialProducerCursor
	pendingReads int
	drains       []string
	sources      []shadowbilling.CommercialMeterSource
	events       []shadowbilling.CommercialOrderedUsageEvent
	policies     []shadowbilling.Policy
}

func (store *runtimeWhiteListMeteringStoreFake) PendingCommercialDebitEntitlementIDs(context.Context) ([]string, error) {
	store.pendingReads++
	return append([]string(nil), store.pending...), nil
}

func (store *runtimeWhiteListMeteringStoreFake) DrainCommercialDebits(
	_ context.Context,
	entitlementID string,
	_ shadowbilling.CommercialDebiter,
) error {
	store.drains = append(store.drains, entitlementID)
	return nil
}

func (store *runtimeWhiteListMeteringStoreFake) EnsureCommercialProducerCursor(
	_ context.Context,
	source shadowbilling.CommercialMeterSource,
	_ int64,
) (shadowbilling.CommercialProducerCursor, error) {
	store.sources = append(store.sources, source)
	return store.cursor, nil
}

func (store *runtimeWhiteListMeteringStoreFake) ApplyCommercialOrdered(
	_ context.Context,
	event shadowbilling.CommercialOrderedUsageEvent,
	policy shadowbilling.Policy,
	_ shadowbilling.CommercialDebiter,
) (shadowbilling.DurableResult, error) {
	store.events = append(store.events, event)
	store.policies = append(store.policies, policy)
	return shadowbilling.DurableResult{Decision: shadowbilling.Decision{
		Interval: &shadowbilling.UsageInterval{},
	}}, nil
}
