package controlplane

import (
	"strings"
	"testing"
)

// Break caught: adding runtime fencing while rewriting an applied migration,
// or omitting the durable epoch/incarnation/fence fields, would make restored
// clusters accept stale apply commands after restart.
func TestOutboxFencingIsAdditiveMigrationThree(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) < 3 {
		t.Fatalf("migration count=%d, want at least 3", len(migrations))
	}
	if migrations[0].Path != "migrations/0001_control_plane.sql" ||
		migrations[1].Path != "migrations/0002_restore_epoch.sql" ||
		migrations[2].Path != "migrations/0003_outbox_fencing.sql" {
		t.Fatalf("migration paths=%v", []string{
			migrations[0].Path, migrations[1].Path, migrations[2].Path,
		})
	}

	sql := strings.ToLower(string(migrations[2].Data))
	for _, required := range []string{
		"cluster_epoch", "node_incarnation", "lease_fence",
		"operation_id", "event_kind", "tombstone",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 3 lacks %q", required)
		}
	}
	if strings.Contains(sql, "drop table") {
		t.Fatal("migration 3 is destructive")
	}
}
