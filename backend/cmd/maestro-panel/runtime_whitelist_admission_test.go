package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

func TestRuntimeWhiteListAdmissionRunsAfterAllOriginsAndBeforeReconcile(t *testing.T) {
	for _, failLastOrigin := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "last-origin-unavailable"}[failLastOrigin], func(t *testing.T) {
			now := time.Unix(2_000_000, 0).UTC()
			control := &runtimeWhiteListMeteringControl{candidates: []controlplane.WhiteListMeteringAdmissionCandidate{
				{EntitlementID: "paid-new-account", ExitID: "exit-nl"},
				{EntitlementID: "unmeasured-exit-account", ExitID: "exit-other"},
			}}
			senders := make(map[string]controlplane.ExternalActionSender)
			for _, nodeID := range []string{"s4", "s2"} {
				receipt := controlplane.WhiteListSidecarReceipt{ActionKey: nodeID + ":empty", OriginID: "origin-" + nodeID,
					XrayProcessBootID: "boot-" + nodeID, AppliedAt: now, ExpiresAt: now.Add(time.Minute)}
				control.plan.Origins = append(control.plan.Origins, controlplane.WhiteListMeteringOrigin{
					Origin:  controlplane.WhiteListOrigin{NodeID: nodeID, OriginID: receipt.OriginID},
					Desired: controlplane.WhiteListSidecarDesired{Action: controlplane.ExternalActionCommand{ActionKey: receipt.ActionKey}}, Receipt: receipt,
				})
				sender := &runtimeWhiteListMeteringSender{snapshot: sidecaragentclient.UsageSnapshot{
					Receipt: sidecaragentclient.Receipt{ActionKey: receipt.ActionKey, OriginID: receipt.OriginID,
						XrayProcessBootID: receipt.XrayProcessBootID, AppliedAt: now, ExpiresAt: receipt.ExpiresAt}, SampledAt: now,
				}}
				if failLastOrigin && nodeID == "s2" {
					sender.lookupErr = errors.New("origin unavailable")
				}
				senders[nodeID] = sender
			}
			providerCalls, admissions := 0, 0
			reserve := controlplane.WhiteListAdmissionReserve{MeasuredP999BytesPerSecond: 3_000_000, MeasuredAtUnix: now.Unix(), ValidUntilUnix: now.Unix() + 20}
			control.admissionCall = func(ctx context.Context, entitlementID, exitID string, got controlplane.WhiteListAdmissionReserve) error {
				if len(control.observations) != 2 || control.reconciles != 0 || len(control.plan.Routes) != 0 ||
					entitlementID != "paid-new-account" || exitID != "exit-nl" || got != reserve || ctx.Err() != nil {
					t.Fatal("admission must use the exact measured reserve after every observation, even before Routes exist")
				}
				admissions++
				return nil
			}
			collector := &runtimeWhiteListMeteringCollector{control: control, store: &runtimeWhiteListMeteringStoreFake{},
				workerID: "worker", senders: senders,
				reserves: func(context.Context) (map[string]controlplane.WhiteListAdmissionReserve, error) {
					providerCalls++
					return map[string]controlplane.WhiteListAdmissionReserve{"exit-nl": reserve}, nil
				},
			}
			err := collector.runPass(context.Background())
			if failLastOrigin {
				if err == nil || providerCalls != 0 || admissions != 0 {
					t.Fatal("partial Origin coverage must not invoke provider or admit an account")
				}
			} else if err != nil || providerCalls != 1 || admissions != 1 {
				t.Fatalf("first-use caller did not authorize exactly the measured exit: provider=%d admissions=%d error=%v", providerCalls, admissions, err)
			}
			if control.reconciles != 1 {
				t.Fatal("both admission success and observation failure must reconcile")
			}
		})
	}
}

func TestRuntimeWhiteListUnavailableReserveStillReconciles(t *testing.T) {
	control := &runtimeWhiteListMeteringControl{}
	collector := &runtimeWhiteListMeteringCollector{control: control, store: &runtimeWhiteListMeteringStoreFake{}, workerID: "worker",
		senders: map[string]controlplane.ExternalActionSender{"s4": &runtimeWhiteListMeteringSender{}},
		reserves: func(context.Context) (map[string]controlplane.WhiteListAdmissionReserve, error) {
			return nil, errRuntimeWhiteListReserveUnavailable
		},
	}
	if err := collector.runPass(context.Background()); !errors.Is(err, errRuntimeWhiteListMeteringUnavailable) || control.reconciles != 1 {
		t.Fatal("invalid report must retain fail-closed reconciliation")
	}
	collector.reserves = nil
	if err := collector.runPass(context.Background()); err != nil || control.reconciles != 2 {
		t.Fatal("no configured report must leave accounting/reconciliation running without admission")
	}
}
