package controlplane

import (
	"context"
	"strings"
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
	service := testWhiteListReadinessService(now, testWhiteListDurableCurrentRow(desired, true))
	ready, err := service.EvaluateWhiteListSidecarReadiness(context.Background(), map[string]string{"origin-s2": "boot-1"}, []WhiteListSidecarReceipt{receipt}, "exit-nl")
	if err != nil || !ready {
		t.Fatalf("ready = %v, %v", ready, err)
	}
}

func TestWhiteListSidecarReadinessRequiresCompleteCurrentStateAndExactExit(t *testing.T) {
	desired := testWhiteListSidecarDesired(t)
	cases := []struct {
		name string
		row  map[string]any
	}{
		{name: "missing desired", row: map[string]any{"origin_id": desired.OriginID, "current_node_id": desired.NodeID, "current_release_id": desired.ReleaseID, "current_profile_id": desired.ProfileID, "current_preset_id": desired.PresetID, "current_config_digest": desired.ConfigDigest}},
		{name: "stale origin config", row: func() map[string]any {
			row := testWhiteListDurableCurrentRow(desired, true)
			row["current_config_digest"] = testDigest("d")
			return row
		}()},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := testWhiteListReadinessService(testTime(), test.row)
			ready, err := service.EvaluateWhiteListSidecarReadiness(context.Background(), map[string]string{desired.OriginID: "boot-1"}, []WhiteListSidecarReceipt{testWhiteListSidecarReceipt(desired, testTime())}, desired.ExitID)
			if err != nil || ready {
				t.Fatalf("incomplete or stale current state readiness = %v, %v", ready, err)
			}
		})
	}
	receipt := testWhiteListSidecarReceipt(desired, testTime())
	service := testWhiteListReadinessService(testTime(), testWhiteListDurableCurrentRow(desired, true))
	ready, err := service.EvaluateWhiteListSidecarReadiness(context.Background(), map[string]string{desired.OriginID: "boot-1"}, []WhiteListSidecarReceipt{receipt}, "exit-de")
	if err != nil || ready {
		t.Fatalf("unrelated exit readiness = %v, %v", ready, err)
	}
}

func TestWhiteListSidecarReadinessSelectsCurrentReceiptAmongHistory(t *testing.T) {
	now := testTime()
	desired := testWhiteListSidecarDesired(t)
	current := testWhiteListSidecarReceipt(desired, now)
	historical := current
	historical.ActionKey = desired.NodeID + ":0:" + testDigest("f")
	historical.DesiredGeneration = 0
	service := testWhiteListReadinessService(now, testWhiteListDurableCurrentRow(desired, true))
	ready, err := service.EvaluateWhiteListSidecarReadiness(context.Background(), map[string]string{desired.OriginID: "boot-1"}, []WhiteListSidecarReceipt{historical, current}, desired.ExitID)
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

func TestWhiteListSidecarReadinessRejectsCallerSubsetAgainstDurableActiveOrigins(t *testing.T) {
	now := testTime()
	origins := []WhiteListOrigin{
		{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true},
		{OriginID: "origin-s3", NodeID: "s3", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("b"), Active: true},
	}
	desired, err := BuildWhiteListSidecarDesired(nil, origins, nil, WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(
		testWhiteListDurableCurrentRow(desired[0], true),
		testWhiteListDurableCurrentRow(desired[1], true),
	)}}
	service := &Service{store: &Store{db: db}, clock: fixedClock{value: now}}
	ready, err := service.EvaluateWhiteListSidecarReadiness(
		context.Background(),
		map[string]string{desired[0].OriginID: "boot-1"},
		[]WhiteListSidecarReceipt{testWhiteListSidecarReceipt(desired[0], now)},
		"exit-nl",
	)
	if err != nil || ready {
		t.Fatalf("incomplete caller subset readiness = %v, %v", ready, err)
	}
	if len(db.linearCalls) != 1 {
		t.Fatalf("durable readiness reads = %d, want 1", len(db.linearCalls))
	}
	query := db.linearCalls[0].statements[0]
	for _, required := range []string{"FROM whitelist_sidecar_origins AS origin", "LEFT JOIN whitelist_sidecar_desired AS desired", "MAX(candidate.desired_generation)", "WHERE origin.active=1"} {
		if !strings.Contains(query.SQL, required) {
			t.Fatalf("durable readiness query missing %q: %s", required, query.SQL)
		}
	}
	if len(query.Args) != 1 || query.Args[0] != "exit-nl" {
		t.Fatalf("durable readiness query args = %#v", query.Args)
	}
}

func testWhiteListDurableCurrentRow(desired WhiteListSidecarDesired, healthy bool) map[string]any {
	healthyValue := int64(0)
	if healthy {
		healthyValue = 1
	}
	return map[string]any{
		"origin_id": desired.OriginID, "current_node_id": desired.NodeID,
		"current_release_id": desired.ReleaseID, "current_profile_id": desired.ProfileID,
		"current_preset_id": desired.PresetID, "current_config_digest": desired.ConfigDigest,
		"desired_node_id": desired.NodeID, "desired_release_id": desired.ReleaseID,
		"desired_profile_id": desired.ProfileID, "desired_preset_id": desired.PresetID,
		"desired_exit_id": desired.ExitID, "desired_config_digest": desired.ConfigDigest,
		"desired_generation": desired.Generation, "managed_user_set_digest": desired.ManagedUserSetDigest,
		"desired_sha256": desired.DesiredSHA256, "payload_json": desired.PayloadJSON,
		"action_type": desired.Action.Type, "action_key": desired.Action.ActionKey,
		"exit_healthy": healthyValue,
	}
}

func testWhiteListReadinessService(now time.Time, rows ...map[string]any) *Service {
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(rows...)}}
	return &Service{store: &Store{db: db}, clock: fixedClock{value: now}}
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
