package controlplane

import (
	"strings"
	"testing"
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

func TestWhiteListSidecarDesiredStatementsBindExternalAction(t *testing.T) {
	desired := WhiteListSidecarDesired{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ExitID: "exit-nl", Generation: 3, ConfigDigest: testDigest("a"), ManagedUserSetDigest: testDigest("b"), DesiredSHA256: testDigest("c"), PayloadJSON: []byte(`{"version":1}`)}
	desired.Action = ExternalActionCommand{Type: "whitelist_sidecar_apply", ResourceID: desired.OriginID, ActionKey: "s2:3:" + desired.DesiredSHA256, Request: desired.PayloadJSON}
	statements, err := whiteListSidecarDesiredStatements(desired, 1_800_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 || !strings.Contains(statements[0].SQL, "SELECT action_id FROM external_actions") || !strings.Contains(statements[1].SQL, "INSERT OR IGNORE INTO whitelist_sidecar_desired") {
		t.Fatalf("statements = %#v", statements)
	}
}
