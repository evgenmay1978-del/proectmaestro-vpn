package vkturnprov

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
)

const testKeyB64 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes

// seedCanaryDir writes a passwords.json + wg-keys.dat shaped exactly like the
// pinned wdtt-server's on-disk state, including server-owned fields the panel
// must never touch.
func seedCanaryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db := map[string]any{
		"main_password": "master-secret",
		"admin_id":      "42",
		"bot_token":     "bot-secret",
		"passwords": map[string]any{
			"ExistingPass1234": map[string]any{"device_id": "phone-1", "expires_at": 0},
		},
		"devices": map[string]any{
			"phone-1": map[string]any{"device_id": "phone-1", "ip": "10.66.66.2", "priv_key": testKeyB64, "pub_key": testKeyB64},
		},
	}
	b, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "passwords.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	keys := strings.Repeat(testKeyB64+"\n", 4)
	if err := os.WriteFile(filepath.Join(dir, "wg-keys.dat"), []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeDocker returns a Runner that records calls and reports a healthy container
// on the expected relay IP.
func fakeDocker(calls *[]string) Runner {
	return func(name string, args ...string) (string, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if len(args) > 0 && args[0] == "inspect" {
			return "true 172.17.0.2\n", nil
		}
		return "", nil
	}
}

func openTestProv(t *testing.T, dir string, calls *[]string) *Provisioner {
	t.Helper()
	p := Open(dir, "", "")
	if p == nil {
		t.Fatal("Open returned nil for a non-empty dir")
	}
	p.SetExecHooks(fakeDocker(calls), func(time.Duration) {})
	return p
}

func loadJSON(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "passwords.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAddClientProvisionsEveryDeviceAndRestarts(t *testing.T) {
	dir := seedCanaryDir(t)
	var calls []string
	p := openTestProv(t, dir, &calls)

	res, err := p.AddClient("newuser", []string{"dev-a", "dev-b", "dev-a", " "})
	if err != nil {
		t.Fatal(err)
	}
	// Returned identity is complete and self-consistent.
	if len(res.Password) != 16 || strings.ContainsAny(res.Password, "0O1lI") {
		t.Fatalf("password %q not in the upstream alphabet/length", res.Password)
	}
	if res.LocalAddress != "10.66.66.3/32" {
		t.Fatalf("expected next free IP 10.66.66.3/32, got %s", res.LocalAddress)
	}
	if res.ServerPublicKey != testKeyB64 {
		t.Fatalf("server pub not read from wg-keys.dat: %s", res.ServerPublicKey)
	}
	priv, err := base64.StdEncoding.DecodeString(res.PrivateKey)
	if err != nil || len(priv) != 32 {
		t.Fatalf("bad private key: %v", err)
	}

	raw := loadJSON(t, dir)
	// Server-owned fields preserved byte-for-byte semantics.
	var mp, bt string
	_ = json.Unmarshal(raw["main_password"], &mp)
	_ = json.Unmarshal(raw["bot_token"], &bt)
	if mp != "master-secret" || bt != "bot-secret" {
		t.Fatalf("server-owned fields were mangled: %s %s", mp, bt)
	}
	var passwords map[string]*passwordEntry
	if err := json.Unmarshal(raw["passwords"], &passwords); err != nil {
		t.Fatal(err)
	}
	entry, ok := passwords[res.Password]
	if !ok || entry.DeviceID != "" {
		t.Fatalf("new password must exist UNBOUND, got %+v (ok=%v)", entry, ok)
	}
	if _, kept := passwords["ExistingPass1234"]; !kept {
		t.Fatal("pre-existing password entry lost")
	}
	var devices map[string]*clientDevice
	if err := json.Unmarshal(raw["devices"], &devices); err != nil {
		t.Fatal(err)
	}
	// Both installs share ONE identity so whichever phone connects first just works;
	// the device pubkey must be the real curve25519 derivation of the returned priv.
	wantPub, _ := curve25519.X25519(priv, curve25519.Basepoint)
	for _, id := range []string{"dev-a", "dev-b"} {
		d := devices[id]
		if d == nil || d.IP != "10.66.66.3" || d.PrivKey != res.PrivateKey {
			t.Fatalf("device %s not provisioned with the shared identity: %+v", id, d)
		}
		if d.PubKey != base64.StdEncoding.EncodeToString(wantPub) {
			t.Fatalf("device %s pub_key is not derived from priv_key", id)
		}
	}
	if devices["phone-1"] == nil {
		t.Fatal("pre-existing device lost")
	}
	// Wrap keys only load at startup → the container must have been restarted, then verified.
	joined := strings.Join(calls, "; ")
	if !strings.Contains(joined, "docker restart -t 5 "+DefaultContainer) || !strings.Contains(joined, "docker inspect") {
		t.Fatalf("expected docker restart+inspect, got: %s", joined)
	}
}

func TestAddClientRejectsCollisionsAndEmpty(t *testing.T) {
	dir := seedCanaryDir(t)
	var calls []string
	p := openTestProv(t, dir, &calls)

	if _, err := p.AddClient("x", nil); err == nil {
		t.Fatal("no device ids must be rejected")
	}
	// phone-1 already belongs to the seeded login → adding another login that
	// claims the same physical device must fail loudly, not cross the identities.
	if _, err := p.AddClient("x", []string{"phone-1"}); err == nil || !strings.Contains(err.Error(), "phone-1") {
		t.Fatalf("expected collision error naming the device, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("failed adds must not restart the container: %v", calls)
	}
}

func TestRemoveClientDeletesPasswordAndDevicesByIP(t *testing.T) {
	dir := seedCanaryDir(t)
	var calls []string
	p := openTestProv(t, dir, &calls)

	res, err := p.AddClient("newuser", []string{"dev-a", "dev-b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveClient(res.Password, res.LocalAddress); err != nil {
		t.Fatal(err)
	}
	raw := loadJSON(t, dir)
	var passwords map[string]*passwordEntry
	_ = json.Unmarshal(raw["passwords"], &passwords)
	if _, still := passwords[res.Password]; still {
		t.Fatal("password not removed")
	}
	if _, kept := passwords["ExistingPass1234"]; !kept {
		t.Fatal("unrelated password removed")
	}
	var devices map[string]*clientDevice
	_ = json.Unmarshal(raw["devices"], &devices)
	if devices["dev-a"] != nil || devices["dev-b"] != nil {
		t.Fatal("devices of the removed client survive")
	}
	if devices["phone-1"] == nil {
		t.Fatal("unrelated device removed")
	}
	// Removing again is a no-op (idempotent) and must not bounce the container.
	n := len(calls)
	if err := p.RemoveClient(res.Password, res.LocalAddress); err != nil {
		t.Fatal(err)
	}
	if len(calls) != n {
		t.Fatal("idempotent remove restarted the container")
	}
}

func TestAddClientRollsBackWhenRestartFails(t *testing.T) {
	dir := seedCanaryDir(t)
	orig, err := os.ReadFile(filepath.Join(dir, "passwords.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := Open(dir, "", "")
	restarts := 0
	p.SetExecHooks(func(name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "restart" {
			restarts++
			if restarts == 1 {
				return "", fmt.Errorf("docker daemon exploded")
			}
			return "", nil
		}
		return "false ", nil // inspect: never healthy on the failing path
	}, func(time.Duration) {})

	if _, err := p.AddClient("newuser", []string{"dev-a"}); err == nil {
		t.Fatal("restart failure must fail the add")
	}
	now, err := os.ReadFile(filepath.Join(dir, "passwords.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(now) != string(orig) {
		t.Fatal("passwords.json not rolled back after failed restart")
	}
	if restarts < 2 {
		t.Fatalf("rollback must restart the container onto the restored config, restarts=%d", restarts)
	}
}

func TestOpenDisabledOnEmptyDir(t *testing.T) {
	if Open("", "c", "ip") != nil {
		t.Fatal("empty dir must disable the provisioner")
	}
}
