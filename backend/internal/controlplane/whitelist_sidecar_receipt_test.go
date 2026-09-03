package controlplane

import (
	"testing"
	"time"
)

func TestWhiteListSidecarReceiptReplayAndReadinessAreExact(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	desired := WhiteListSidecarDesired{
		OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1",
		Generation: 7, ConfigDigest: testDigest("a"), ManagedUserSetDigest: testDigest("b"), DesiredSHA256: testDigest("c"),
	}
	desired.Action.ActionKey = "s2:7:" + desired.DesiredSHA256
	receipt := WhiteListSidecarReceipt{
		ActionKey: desired.Action.ActionKey, OriginID: desired.OriginID, ReleaseID: desired.ReleaseID,
		XrayProcessBootID: "boot-1", ConfigDigest: desired.ConfigDigest, DesiredGeneration: desired.Generation,
		ManagedUserSetDigest: desired.ManagedUserSetDigest, AppliedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
	}
	if err := ValidateWhiteListSidecarReceipt(desired, "boot-1", receipt, now); err != nil {
		t.Fatal(err)
	}
	if replay, err := ReplayWhiteListSidecarReceipt(receipt, receipt); err != nil || replay != receipt {
		t.Fatalf("exact replay = %#v, %v", replay, err)
	}
	ready, err := EvaluateWhiteListSidecarReadiness([]WhiteListSidecarDesired{desired}, map[string]string{"origin-s2": "boot-1"}, []WhiteListSidecarReceipt{receipt}, WhiteListExit{ExitID: "exit-nl", Healthy: true}, now)
	if err != nil || !ready {
		t.Fatalf("ready = %v, %v", ready, err)
	}
}

func TestWhiteListSidecarReceiptRejectsStaleReleaseAndActionMutation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	desired := WhiteListSidecarDesired{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", Generation: 7, ConfigDigest: testDigest("a"), ManagedUserSetDigest: testDigest("b"), DesiredSHA256: testDigest("c")}
	desired.Action.ActionKey = "s2:7:" + desired.DesiredSHA256
	base := WhiteListSidecarReceipt{ActionKey: desired.Action.ActionKey, OriginID: desired.OriginID, ReleaseID: desired.ReleaseID, XrayProcessBootID: "boot-1", ConfigDigest: desired.ConfigDigest, DesiredGeneration: 7, ManagedUserSetDigest: desired.ManagedUserSetDigest, AppliedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}
	cases := []struct {
		name string
		edit func(*WhiteListSidecarReceipt)
	}{
		{name: "stale", edit: func(value *WhiteListSidecarReceipt) { value.ExpiresAt = now }},
		{name: "release", edit: func(value *WhiteListSidecarReceipt) { value.ReleaseID = "release-2" }},
		{name: "action", edit: func(value *WhiteListSidecarReceipt) { value.ActionKey = "s2:8:" + desired.DesiredSHA256 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.edit(&changed)
			if err := ValidateWhiteListSidecarReceipt(desired, "boot-1", changed, now); err == nil {
				t.Fatal("mismatched receipt accepted")
			}
			if _, err := ReplayWhiteListSidecarReceipt(base, changed); err == nil {
				t.Fatal("mutated receipt replay accepted")
			}
		})
	}
}
