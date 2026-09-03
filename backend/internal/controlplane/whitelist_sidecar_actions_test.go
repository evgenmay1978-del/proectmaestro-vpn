package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestExecuteWhiteListSidecarActionUsesExistingExecutorAndRecordsExactReceipt(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	desired := seedWhiteListSidecarActionFixture(t, db, "origin-s4", "s4", testDigest("a"))
	sender := &desiredReceiptSender{now: service.clock.Now(), bootID: "boot-s4"}

	first, err := service.ExecuteWhiteListSidecarAction(context.Background(), desired, "panel-a", sender)
	if err != nil {
		t.Fatalf("ExecuteWhiteListSidecarAction: %v", err)
	}
	second, err := service.ExecuteWhiteListSidecarAction(context.Background(), desired, "panel-a", sender)
	if err != nil {
		t.Fatalf("replay ExecuteWhiteListSidecarAction: %v", err)
	}
	if first != second || first.ActionKey != desired.Action.ActionKey || sender.posts != 1 {
		t.Fatalf("first=%#v second=%#v posts=%d", first, second, sender.posts)
	}
	rows := db.must(t, rqlite.Statement{SQL: `SELECT action_type,status FROM external_actions WHERE idempotency_key=?`, Args: []any{desired.Action.ActionKey}})[0].Rows
	if fmt.Sprint(rows) != `[map[action_type:whitelist_sidecar_apply status:applied]]` {
		t.Fatalf("external action=%#v", rows)
	}
	receiptRows := db.must(t, rqlite.Statement{SQL: `SELECT COUNT(*) AS n FROM whitelist_sidecar_receipts WHERE action_key=?`, Args: []any{desired.Action.ActionKey}})[0].Rows
	if fmt.Sprint(receiptRows) != `[map[n:1]]` {
		t.Fatalf("receipt rows=%#v", receiptRows)
	}
}

func TestExecuteWhiteListSidecarActionRejectsUnrelatedActionBeforeNetwork(t *testing.T) {
	desired := testWhiteListSidecarDesired(t)
	desired.Action.Type = "wb.room"
	sender := &desiredReceiptSender{now: testTime(), bootID: "boot-s2"}
	service := &Service{}
	if _, err := service.ExecuteWhiteListSidecarAction(context.Background(), desired, "panel-a", sender); !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v", err)
	}
	if sender.posts != 0 {
		t.Fatalf("unrelated action posts=%d", sender.posts)
	}
}

func TestReconcileWhiteListSidecarGenerationCoversEveryActiveOriginBeforeReady(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	origins := []WhiteListOrigin{
		{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true},
		{OriginID: "origin-s3", NodeID: "s3", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("b"), Active: true},
		{OriginID: "origin-s4", NodeID: "s4", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("c"), Active: false},
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, origins, exit)
	routes := []WhiteListManagedRoute{{EntitlementID: "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExitID: exit.ExitID}}
	senders := map[string]*desiredReceiptSender{
		"s2": {now: service.clock.Now(), bootID: "boot-s2"},
		"s3": {now: service.clock.Now(), bootID: "boot-s3"},
	}
	result, err := service.ReconcileWhiteListSidecarGeneration(
		context.Background(), nil, origins, routes, exit, "panel-a",
		func(nodeID string) (ExternalActionSender, bool) {
			sender, ok := senders[nodeID]
			return sender, ok
		},
	)
	if err != nil {
		t.Fatalf("ReconcileWhiteListSidecarGeneration: %v", err)
	}
	if !result.Ready || result.Generation != 1 || result.ReleaseID != "release-1" || len(result.Desired) != 2 || len(result.Receipts) != 2 || !result.FreshUntil.After(service.clock.Now()) {
		t.Fatalf("result=%#v", result)
	}
	if senders["s2"].posts != 1 || senders["s3"].posts != 1 {
		t.Fatalf("posts s2=%d s3=%d", senders["s2"].posts, senders["s3"].posts)
	}

	removal, err := service.ReconcileWhiteListSidecarGeneration(
		context.Background(), desiredByOrigin(result.Desired), origins, nil, exit, "panel-a",
		func(nodeID string) (ExternalActionSender, bool) {
			sender, ok := senders[nodeID]
			return sender, ok
		},
	)
	if err != nil || !removal.Ready || removal.Generation != 2 {
		t.Fatalf("removal=%#v err=%v", removal, err)
	}
	for _, desired := range removal.Desired {
		if len(desired.ManagedUsers) != 0 {
			t.Fatalf("removal managed users=%#v", desired.ManagedUsers)
		}
	}
	if senders["s2"].posts != 2 || senders["s3"].posts != 2 {
		t.Fatalf("removal posts s2=%d s3=%d", senders["s2"].posts, senders["s3"].posts)
	}
}

func TestReconcileWhiteListSidecarGenerationFailsClosedBeforeSendForUnhealthyExitOrMixedRelease(t *testing.T) {
	for name, mutate := range map[string]func([]WhiteListOrigin, *WhiteListExit){
		"unhealthy exit": func(_ []WhiteListOrigin, exit *WhiteListExit) { exit.Healthy = false },
		"mixed release":  func(origins []WhiteListOrigin, _ *WhiteListExit) { origins[1].ReleaseID = "release-2" },
	} {
		t.Run(name, func(t *testing.T) {
			origins := []WhiteListOrigin{
				{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true},
				{OriginID: "origin-s3", NodeID: "s3", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("b"), Active: true},
			}
			exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
			mutate(origins, &exit)
			sender := &desiredReceiptSender{now: testTime(), bootID: "boot"}
			service := &Service{}
			_, err := service.ReconcileWhiteListSidecarGeneration(context.Background(), nil, origins, nil, exit, "panel-a", func(string) (ExternalActionSender, bool) {
				return sender, true
			})
			if !errors.Is(err, ErrConflict) || sender.posts != 0 {
				t.Fatalf("error=%v posts=%d", err, sender.posts)
			}
		})
	}
}

type desiredReceiptSender struct {
	posts  int
	now    time.Time
	bootID string
}

func (sender *desiredReceiptSender) Post(_ context.Context, request []byte) ([]byte, error) {
	sender.posts++
	var payload whiteListSidecarPayload
	if err := json.Unmarshal(request, &payload); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(request)
	receipt := WhiteListSidecarReceipt{
		ActionKey: payload.NodeID + ":" + itoa64(payload.Generation) + ":" + hex.EncodeToString(digest[:]),
		OriginID:  payload.OriginID, ReleaseID: payload.ReleaseID, XrayProcessBootID: sender.bootID,
		ConfigDigest: payload.ConfigDigest, DesiredGeneration: payload.Generation,
		ManagedUserSetDigest: payload.ManagedUserSetDigest, AppliedAt: sender.now, ExpiresAt: sender.now.Add(30 * time.Second),
	}
	return json.Marshal(receipt)
}

func seedWhiteListSidecarActionFixture(t *testing.T, db *customerIntegritySQLite, originID, nodeID, digest string) WhiteListSidecarDesired {
	t.Helper()
	origins := []WhiteListOrigin{{
		OriginID: originID, NodeID: nodeID, ReleaseID: "release-1", ProfileID: "profile-1",
		PresetID: "preset-1", ConfigDigest: digest, Active: true,
	}}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	seedWhiteListSidecarInventory(t, db, origins, exit)
	desired, err := BuildWhiteListSidecarDesired(nil, origins,
		[]WhiteListManagedRoute{{EntitlementID: "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExitID: exit.ExitID}}, exit)
	if err != nil || len(desired) != 1 {
		t.Fatalf("BuildWhiteListSidecarDesired=%#v err=%v", desired, err)
	}
	return desired[0]
}

func seedWhiteListSidecarInventory(t *testing.T, db *customerIntegritySQLite, origins []WhiteListOrigin, exit WhiteListExit) {
	t.Helper()
	for _, origin := range origins {
		db.must(t, rqlite.Statement{SQL: `INSERT OR IGNORE INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES(?,?,0,1,1)`, Args: []any{origin.NodeID, origin.NodeID}})
	}
	db.must(t, rqlite.Statement{SQL: `INSERT OR IGNORE INTO whitelist_sidecar_exits(exit_id,country_code,country_label,healthy,created_at_unix) VALUES(?,?,?,?,1)`, Args: []any{exit.ExitID, exit.CountryCode, exit.CountryLabel, whiteListBoolInt(exit.Healthy)}})
	for _, origin := range origins {
		db.must(t, rqlite.Statement{SQL: `INSERT OR IGNORE INTO whitelist_sidecar_origins(origin_id,node_id,release_id,profile_id,preset_id,config_digest,active,created_at_unix) VALUES(?,?,?,?,?,?,?,1)`, Args: []any{
			origin.OriginID, origin.NodeID, origin.ReleaseID, origin.ProfileID, origin.PresetID,
			origin.ConfigDigest, whiteListBoolInt(origin.Active),
		}})
	}
}
