package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMigrationWhiteListSidecarIsExactV15Upgrade(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if SchemaVersion != 15 || len(migrations) != 15 {
		t.Fatalf("schema chain = version %d/%d, want exact v15", SchemaVersion, len(migrations))
	}
	if migrations[13].Path != "migrations/0014_whitelist_topup_orders.sql" {
		t.Fatalf("v14 moved: %#v", migrations[13])
	}
	latest := migrations[14]
	if latest.Path != "migrations/0015_whitelist_sidecar_reconcile.sql" {
		t.Fatalf("latest migration = %#v", latest)
	}
	sql := strings.Join(strings.Fields(strings.ToLower(string(latest.Data))), " ")
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
