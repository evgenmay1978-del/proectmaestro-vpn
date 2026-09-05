package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func TestMigrationWhiteListSidecarIsExactV15Upgrade(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 18 || len(migrations) != 18 {
		t.Fatalf("schema chain = version %d/%d, want exact v18", SchemaVersion, len(migrations))
	}
	if migrations[13].Path != "migrations/0014_whitelist_topup_orders.sql" {
		t.Fatalf("v14 moved: %#v", migrations[13])
	}
	v15 := migrations[14]
	if v15.Path != "migrations/0015_whitelist_sidecar_reconcile.sql" {
		t.Fatalf("v15 migration = %#v", v15)
	}
	sql := strings.Join(strings.Fields(strings.ToLower(string(v15.Data))), " ")
	for _, required := range []string{
		"create table whitelist_route_credentials",
		"unique(entitlement_id, exit_id)",
		"create table whitelist_sidecar_desired",
		"unique(origin_id, desired_generation)",
		"create table whitelist_sidecar_receipts",
		"xray_process_boot_id text not null",
		"managed_user_set_digest text not null",
		"foreign key(action_type, action_key) references external_actions(action_type, idempotency_key)",
		"before update on whitelist_route_credentials",
		"before update on whitelist_sidecar_receipts",
		"before insert on whitelist_sidecar_desired",
		"select max(desired_generation) + 1",
		"desired generation must increment by one",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("v15 missing %q", required)
		}
	}
	if strings.Contains(sql, "drop table") || strings.Contains(sql, "alter table") {
		t.Fatal("v15 must preserve the immutable v1-v14 prefix")
	}
}

func TestResolveWhiteListSidecarUnknownReadsExactReceiptWithoutWrite(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	desired := WhiteListSidecarDesired{
		OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", Generation: 7,
		ConfigDigest: testDigest("a"), ManagedUserSetDigest: testDigest("b"), DesiredSHA256: testDigest("c"),
	}
	desired.Action.ActionKey = "s2:7:" + desired.DesiredSHA256
	receipt := WhiteListSidecarReceipt{
		ActionKey: desired.Action.ActionKey, OriginID: desired.OriginID, ReleaseID: desired.ReleaseID,
		XrayProcessBootID: "boot-1", ConfigDigest: desired.ConfigDigest,
		DesiredGeneration: desired.Generation, ManagedUserSetDigest: desired.ManagedUserSetDigest,
		AppliedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute),
	}
	db := &recordingRQLite{linear: []scriptedResult{rowsScript(map[string]any{
		"action_key": receipt.ActionKey, "origin_id": receipt.OriginID, "release_id": receipt.ReleaseID,
		"xray_process_boot_id": receipt.XrayProcessBootID, "config_digest": receipt.ConfigDigest,
		"desired_generation": receipt.DesiredGeneration, "managed_user_set_digest": receipt.ManagedUserSetDigest,
		"applied_at_unix": receipt.AppliedAt.Unix(), "expires_at_unix": receipt.ExpiresAt.Unix(),
	})}}
	service := &Service{store: &Store{db: db}, clock: fixedClock{value: now}}
	resolved, err := service.ResolveWhiteListSidecarUnknown(context.Background(), desired, "boot-1")
	if err != nil || resolved != receipt {
		t.Fatalf("resolved = %#v, %v", resolved, err)
	}
	if len(db.requestCalls) != 0 || len(db.linearCalls) != 1 {
		t.Fatalf("unknown resolution writes/reads = %d/%d, want 0/1", len(db.requestCalls), len(db.linearCalls))
	}
	if !strings.Contains(db.linearCalls[0].statements[0].SQL, "WHERE action_key=?") ||
		len(db.linearCalls[0].statements[0].Args) != 1 || db.linearCalls[0].statements[0].Args[0] != desired.Action.ActionKey {
		t.Fatalf("unknown resolution query = %#v", db.linearCalls[0].statements[0])
	}
}

func TestWhiteListSidecarDesiredStatementsBindExternalAction(t *testing.T) {
	desired := testWhiteListSidecarDesired(t)
	statements, err := whiteListSidecarDesiredStatements(desired, 1_800_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 || !strings.Contains(statements[0].SQL, "SELECT action_id FROM external_actions") || !strings.Contains(statements[1].SQL, "INSERT OR IGNORE INTO whitelist_sidecar_desired") {
		t.Fatalf("statements = %#v", statements)
	}
}

func TestWhiteListSidecarDispatchRequiresDurableDesiredBinding(t *testing.T) {
	guard := externalActionDesiredBindingGuard(ExternalActionCommand{Type: whiteListSidecarApplyAction})
	for _, required := range []string{"EXISTS", "whitelist_sidecar_desired", "desired.action_type=action.action_type", "desired.action_key=action.idempotency_key"} {
		if !strings.Contains(guard, required) {
			t.Fatalf("dispatch guard %q missing %q", guard, required)
		}
	}
	if guard := externalActionDesiredBindingGuard(ExternalActionCommand{Type: "unrelated"}); guard != "" {
		t.Fatalf("unrelated action guard = %q", guard)
	}
}

func TestWhiteListSidecarReadinessDerivesCompleteCurrentStateFromSQLite(t *testing.T) {
	db, service := newCustomerIntegritySQLite(t)
	now := service.clock.Now()
	origins := []WhiteListOrigin{
		{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true, StaticUsers: []string{"static-s2@example.invalid"}},
		{OriginID: "origin-s3", NodeID: "s3", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("b"), Active: true, StaticUsers: []string{"static-s3@example.invalid"}},
	}
	routes := []WhiteListManagedRoute{{EntitlementID: "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExitID: "exit-nl"}}
	db.must(t,
		rqlite.Statement{SQL: `INSERT INTO nodes(node_id,display_name,is_voter,enabled,created_at_unix) VALUES('s2','s2',0,1,1),('s3','s3',0,1,1),('s4','s4',0,1,1)`},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_exits(exit_id,country_code,country_label,healthy,created_at_unix) VALUES('exit-nl','NL','Netherlands',1,1)`},
		rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_origins(origin_id,node_id,release_id,profile_id,preset_id,config_digest,active,created_at_unix) VALUES(?,?,?,?,?,?,1,1),(?,?,?,?,?,?,1,1)`, Args: []any{origins[0].OriginID, origins[0].NodeID, origins[0].ReleaseID, origins[0].ProfileID, origins[0].PresetID, origins[0].ConfigDigest, origins[1].OriginID, origins[1].NodeID, origins[1].ReleaseID, origins[1].ProfileID, origins[1].PresetID, origins[1].ConfigDigest}},
	)
	desired, err := BuildWhiteListSidecarDesired(nil, origins, routes, WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range desired {
		if _, err := service.PersistWhiteListSidecarDesired(context.Background(), target); err != nil {
			t.Fatalf("persist desired %s: %v", target.OriginID, err)
		}
	}
	bootIDs := map[string]string{"origin-s2": "boot-s2", "origin-s3": "boot-s3"}
	receipts := []WhiteListSidecarReceipt{testWhiteListSidecarReceipt(desired[0], now), testWhiteListSidecarReceipt(desired[1], now)}
	receipts[0].XrayProcessBootID = bootIDs[desired[0].OriginID]
	receipts[1].XrayProcessBootID = bootIDs[desired[1].OriginID]
	ready, err := service.EvaluateWhiteListSidecarReadiness(context.Background(), bootIDs, receipts, "exit-nl")
	if err != nil || !ready {
		t.Fatalf("complete durable readiness = %v, %v", ready, err)
	}
	db.must(t, rqlite.Statement{SQL: `INSERT INTO whitelist_sidecar_origins(origin_id,node_id,release_id,profile_id,preset_id,config_digest,active,created_at_unix) VALUES('origin-s4','s4','release-1','profile-1','preset-1',?,1,1)`, Args: []any{testDigest("c")}})
	ready, err = service.EvaluateWhiteListSidecarReadiness(context.Background(), bootIDs, receipts, "exit-nl")
	if err != nil || ready {
		t.Fatalf("missing durable desired readiness = %v, %v", ready, err)
	}
}
