package controlplane

import (
	"testing"
	"time"
)

func TestWhiteListSidecarReceiptReplayAndReadinessAreExact(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	desired := testWhiteListSidecarDesired(t)
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
	state, err := NewWhiteListSidecarCurrentState(
		[]WhiteListOrigin{{OriginID: desired.OriginID, NodeID: desired.NodeID, ReleaseID: desired.ReleaseID, ProfileID: desired.ProfileID, PresetID: desired.PresetID, ConfigDigest: desired.ConfigDigest, Active: true}},
		[]WhiteListSidecarDesired{desired},
	)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := EvaluateWhiteListSidecarReadiness(state, map[string]string{"origin-s2": "boot-1"}, []WhiteListSidecarReceipt{receipt}, WhiteListExit{ExitID: "exit-nl", Healthy: true}, now)
	if err != nil || !ready {
		t.Fatalf("ready = %v, %v", ready, err)
	}
}

func TestWhiteListSidecarReadinessRequiresCompleteCurrentStateAndExactExit(t *testing.T) {
	desired := testWhiteListSidecarDesired(t)
	origin := WhiteListOrigin{OriginID: desired.OriginID, NodeID: desired.NodeID, ReleaseID: desired.ReleaseID, ProfileID: desired.ProfileID, PresetID: desired.PresetID, ConfigDigest: desired.ConfigDigest, Active: true}
	cases := []struct {
		name     string
		origins  []WhiteListOrigin
		desireds []WhiteListSidecarDesired
	}{
		{name: "missing active origin", origins: []WhiteListOrigin{origin, {OriginID: "origin-s3", NodeID: "s3", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("d"), Active: true}}, desireds: []WhiteListSidecarDesired{desired}},
		{name: "stale generation", origins: []WhiteListOrigin{origin}, desireds: []WhiteListSidecarDesired{func() WhiteListSidecarDesired { stale := desired; stale.Generation--; return stale }()}},
		{name: "stale origin config", origins: []WhiteListOrigin{func() WhiteListOrigin { changed := origin; changed.ConfigDigest = testDigest("d"); return changed }()}, desireds: []WhiteListSidecarDesired{desired}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewWhiteListSidecarCurrentState(test.origins, test.desireds); err == nil {
				t.Fatal("incomplete or stale current state accepted")
			}
		})
	}
	state, err := NewWhiteListSidecarCurrentState([]WhiteListOrigin{origin}, []WhiteListSidecarDesired{desired})
	if err != nil {
		t.Fatal(err)
	}
	receipt := testWhiteListSidecarReceipt(desired, testTime())
	ready, err := EvaluateWhiteListSidecarReadiness(state, map[string]string{desired.OriginID: "boot-1"}, []WhiteListSidecarReceipt{receipt}, WhiteListExit{ExitID: "exit-de", Healthy: true}, testTime())
	if err != nil || ready {
		t.Fatalf("unrelated exit readiness = %v, %v", ready, err)
	}
}

func TestWhiteListSidecarReadinessSelectsCurrentReceiptAmongHistory(t *testing.T) {
	now := testTime()
	desired := testWhiteListSidecarDesired(t)
	origin := WhiteListOrigin{OriginID: desired.OriginID, NodeID: desired.NodeID, ReleaseID: desired.ReleaseID, ProfileID: desired.ProfileID, PresetID: desired.PresetID, ConfigDigest: desired.ConfigDigest, Active: true}
	state, err := NewWhiteListSidecarCurrentState([]WhiteListOrigin{origin}, []WhiteListSidecarDesired{desired})
	if err != nil {
		t.Fatal(err)
	}
	current := testWhiteListSidecarReceipt(desired, now)
	historical := current
	historical.ActionKey = desired.NodeID + ":0:" + testDigest("f")
	historical.DesiredGeneration = 0
	ready, err := EvaluateWhiteListSidecarReadiness(state, map[string]string{desired.OriginID: "boot-1"}, []WhiteListSidecarReceipt{historical, current}, WhiteListExit{ExitID: desired.ExitID, Healthy: true}, now)
	if err != nil || !ready {
		t.Fatalf("readiness with history = %v, %v", ready, err)
	}
}

func TestWhiteListSidecarReceiptReplayNormalizesPersistedPrecision(t *testing.T) {
	desired := testWhiteListSidecarDesired(t)
	now := testTime()
	receipt := testWhiteListSidecarReceipt(desired, now)
	receipt.AppliedAt = receipt.AppliedAt.Add(123456789)
	receipt.ExpiresAt = receipt.ExpiresAt.Add(987654321)
	persisted := receipt
	persisted.AppliedAt = time.Unix(receipt.AppliedAt.Unix(), 0)
	persisted.ExpiresAt = time.Unix(receipt.ExpiresAt.Unix(), 0)
	replayed, err := ReplayWhiteListSidecarReceipt(persisted, receipt)
	if err != nil || replayed != persisted {
		t.Fatalf("normalized replay = %#v, %v", replayed, err)
	}
}

func testWhiteListSidecarReceipt(desired WhiteListSidecarDesired, now time.Time) WhiteListSidecarReceipt {
	return WhiteListSidecarReceipt{ActionKey: desired.Action.ActionKey, OriginID: desired.OriginID, ReleaseID: desired.ReleaseID, XrayProcessBootID: "boot-1", ConfigDigest: desired.ConfigDigest, DesiredGeneration: desired.Generation, ManagedUserSetDigest: desired.ManagedUserSetDigest, AppliedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}
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
