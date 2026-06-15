package server2

import (
	"os"
	"strings"
	"testing"
)

func TestRenderHy2(t *testing.T) {
	c := New(Config{Hy2Port: 8443})
	out, err := c.renderHy2([]Hy2User{{User: "alice", Pass: "p1"}, {User: "bob", Pass: "p2"}})
	if err != nil {
		t.Fatalf("renderHy2: %v", err)
	}
	for _, want := range []string{
		"listen: :8443",
		"type: userpass",
		"    alice: p1",
		"    bob: p2",
		"/etc/hysteria/server.crt",
		"masquerade:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered config missing %q\n---\n%s", want, out)
		}
	}
}

// TestSyncHy2Live hits the real server 2. Gated behind MAESTRO_S2_PASS so CI and
// casual `go test` skip it. Run manually:
//
//	MAESTRO_S2_PASS=… go test ./internal/server2 -run Live -v
func TestSyncHy2Live(t *testing.T) {
	pass := os.Getenv("MAESTRO_S2_PASS")
	if pass == "" {
		t.Skip("set MAESTRO_S2_PASS to run the live server-2 sync test")
	}
	c := New(Config{Host: "85.137.166.237", User: "root", Password: pass, Hy2Port: 8443})

	before, err := c.HealthCheck()
	if err != nil {
		t.Fatalf("health before: %v", err)
	}
	t.Logf("server2 before: %s", before)
	if !strings.Contains(before, "caddy=active") {
		t.Fatalf("refusing to proceed — caddy (naive) not active: %s", before)
	}

	users := []Hy2User{{User: "maestro_app", Pass: os.Getenv("MAESTRO_HY2_PASS")}}
	if users[0].Pass == "" {
		users[0].Pass = "testpass1234567890"
	}
	if err := c.SyncHy2Users(users); err != nil {
		t.Fatalf("SyncHy2Users: %v", err)
	}

	after, err := c.HealthCheck()
	if err != nil {
		t.Fatalf("health after: %v", err)
	}
	t.Logf("server2 after:  %s", after)
	if !strings.Contains(after, "caddy=active") || !strings.Contains(after, "hysteria=active") {
		t.Fatalf("a service is down after sync: %s", after)
	}
}

// TestNaiveLive edits the real server-2 Caddyfile (the MTV-managed block only).
// Gated behind MAESTRO_S2_PASS. Run:
//
//	MAESTRO_S2_PASS=… go test ./internal/server2 -run NaiveLive -v
func TestNaiveLive(t *testing.T) {
	pass := os.Getenv("MAESTRO_S2_PASS")
	if pass == "" {
		t.Skip("set MAESTRO_S2_PASS to run the live naive sync test")
	}
	c := New(Config{Host: "85.137.166.237", User: "root", Password: pass})

	before, err := c.HealthCheck()
	if err != nil {
		t.Fatalf("health before: %v", err)
	}
	if !strings.Contains(before, "caddy=active") {
		t.Fatalf("refusing to proceed — caddy not active: %s", before)
	}

	// Add an app (mtv_) user, confirm caddy survives, then clear the app set.
	if err := c.SyncNaiveUsers([]NaiveUser{{User: NaivePrefix + "livetest", Pass: "testpass1234567890"}}); err != nil {
		t.Fatalf("SyncNaiveUsers add: %v", err)
	}
	after, err := c.HealthCheck()
	if err != nil || !strings.Contains(after, "caddy=active") {
		t.Fatalf("caddy down after add: %s (%v)", after, err)
	}
	if err := c.SyncNaiveUsers(nil); err != nil {
		t.Fatalf("SyncNaiveUsers clear: %v", err)
	}
	final, _ := c.HealthCheck()
	if !strings.Contains(final, "caddy=active") {
		t.Fatalf("caddy down after clear: %s", final)
	}
}
