package provision

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/server2"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

type fakeXUI struct {
	logins, adds, updates int
	getSub                string // if set, GetClient returns a client carrying this subId
	getExpiry             int64  // if set, GetClient returns this expiryTime (millis) — for reconcile
	lastSub               string // last subId passed to UpdateClient (assert subId preservation)
	lastAddLimitIP        int    // last limitIp passed to AddClient (assert the device cap)
}

func (f *fakeXUI) Login() error { f.logins++; return nil }
func (f *fakeXUI) AddClient(_ int, c xui.VLESSClient) error {
	f.adds++
	f.lastAddLimitIP = c.LimitIP
	return nil
}
func (f *fakeXUI) UpdateClient(_ int, _ string, c xui.VLESSClient) error {
	f.updates++
	f.lastSub = c.SubID
	return nil
}
func (f *fakeXUI) GetClient(string) (*xui.ExistingClient, error) {
	if f.getSub == "" && f.getExpiry == 0 {
		return nil, nil
	}
	return &xui.ExistingClient{UUID: "u", Email: "e", SubID: f.getSub, ExpiryTime: f.getExpiry}, nil
}

type fakeS2 struct {
	lastHy2     []server2.Hy2User
	lastMieru   []server2.MieruUser
	lastNaive   []server2.NaiveUser
	lastAnyTLS  []server2.AnyTLSUser
	anytlsSyncs int
	naiveUser   string // if set, ReadNaiveUser returns this password (customer exists on naive)
}

func (f *fakeS2) SyncHy2Users(u []server2.Hy2User) error     { f.lastHy2 = u; return nil }
func (f *fakeS2) SyncMieruUsers(u []server2.MieruUser) error { f.lastMieru = u; return nil }
func (f *fakeS2) SyncNaiveUsers(u []server2.NaiveUser) error { f.lastNaive = u; return nil }
func (f *fakeS2) SyncAnyTLSUsers(u []server2.AnyTLSUser) error {
	f.lastAnyTLS = u
	f.anytlsSyncs++
	return nil
}
func (f *fakeS2) ReadNaiveUser(string) (string, bool, error) {
	return f.naiveUser, f.naiveUser != "", nil
}
func (f *fakeS2) ReadProxyExpiry(string) (string, bool, error) { return "", false, nil }

func newProv(t *testing.T) (*Provisioner, *fakeXUI, *fakeS2, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	fx, fh := &fakeXUI{}, &fakeS2{}
	cfg := Config{
		VLESS: VLESSTmpl{InboundID: 2, Server: "wapmixx.ru", Port: 443, Flow: "xtls-rprx-vision", SNI: "yahoo.com", PublicKey: "pk", ShortID: "ab"},
		Hy2:   Hy2Tmpl{Server: "wapmix.duckdns.org", Port: 8443, SNI: "wapmix.duckdns.org", Insecure: true},
		Naive: NaiveTmpl{Server: "wapmixx.ru", Port: 443, SNI: "naive.example"},
		Mita:  MitaTmpl{Server: "85.137.166.237", Port: 2027, Transport: "TCP", HelperSOCKS: 18667},
	}
	return New(st, fx, fh, cfg), fx, fh, st
}

// TestExtendPreservesBotSubId: a renewal must NOT overwrite the 3x-ui client's existing
// subId (a bot-sold client's :2096 sub id) with the panel SubToken — that would break
// the customer's imported bot subscription. The fix reads + re-sends the existing subId.
func TestExtendPreservesBotSubId(t *testing.T) {
	p, fx, _, _ := newProv(t)
	if _, err := p.Provision("bob", 30*24*time.Hour); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// 3x-ui now reports a bot-minted subId for this client; a renewal must keep it.
	fx.getSub = "botsub88"
	fx.lastSub = ""
	if _, err := p.Extend("bob", 30*24*time.Hour); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if fx.lastSub != "botsub88" {
		t.Fatalf("Extend overwrote the 3x-ui subId: sent %q, want preserved botsub88", fx.lastSub)
	}
}

// TestReconcilePullsLater3xuiExpiry: the store must PULL a later 3x-ui expiry (a renewal
// that happened in the 3x-ui panel) and must NEVER reduce the store from an earlier one.
func TestReconcilePullsLater3xuiExpiry(t *testing.T) {
	p, fx, _, st := newProv(t)
	if _, err := p.Provision("carol", 10*24*time.Hour); err != nil {
		t.Fatalf("provision: %v", err)
	}
	before, _ := st.ByLogin("carol")
	// 3x-ui now reports a LATER expiry (the owner extended in the panel) → pull it.
	later := time.Now().Add(60 * 24 * time.Hour)
	fx.getExpiry = later.UnixMilli()
	p.ReconcileExpiries()
	after, _ := st.ByLogin("carol")
	if !after.Expires.After(before.Expires.Add(40 * 24 * time.Hour)) {
		t.Fatalf("reconcile did not pull the later 3x-ui expiry: before %v after %v", before.Expires, after.Expires)
	}
	// An EARLIER 3x-ui date must NOT reduce the store (advance-only).
	fx.getExpiry = time.Now().Add(5 * 24 * time.Hour).UnixMilli()
	p.ReconcileExpiries()
	after2, _ := st.ByLogin("carol")
	if after2.Expires.Before(after.Expires) {
		t.Fatalf("reconcile reduced the expiry: was %v now %v", after.Expires, after2.Expires)
	}
}

// TestActivateExistingSetsLimitIP: when ActivateExisting CREATES a fresh VLESS client for a
// customer who exists only on the naive panel (no 3x-ui client yet), it must apply the
// 5-device cap. This closes the audit gap where app-claimed existing customers were created
// uncapped on VLESS.
func TestActivateExistingSetsLimitIP(t *testing.T) {
	p, fx, fh, _ := newProv(t)
	fh.naiveUser = "secretpw" // exists on naive only → ActivateExisting proceeds + creates VLESS
	if _, err := p.ActivateExisting("frank"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if fx.adds != 1 {
		t.Fatalf("expected one VLESS create, adds=%d", fx.adds)
	}
	if fx.lastAddLimitIP != DeviceLimit {
		t.Fatalf("ActivateExisting created VLESS with limitIp=%d, want %d", fx.lastAddLimitIP, DeviceLimit)
	}
}

// TestActivateExistingExemptUnlimited: the owner admin logins stay uncapped (limitIp=0) on
// the ActivateExisting create path too.
func TestActivateExistingExemptUnlimited(t *testing.T) {
	p, fx, fh, _ := newProv(t)
	fh.naiveUser = "pw"
	if _, err := p.ActivateExisting("wapmix"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if fx.lastAddLimitIP != 0 {
		t.Fatalf("wapmix VLESS limitIp=%d, want 0 (unlimited)", fx.lastAddLimitIP)
	}
}

// TestProvisionCapsDevices: a normal Provision applies limitIp=5; the owner admins get 0.
func TestProvisionCapsDevices(t *testing.T) {
	p, fx, _, _ := newProv(t)
	if _, err := p.Provision("normaluser", 30*24*time.Hour); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if fx.lastAddLimitIP != DeviceLimit {
		t.Fatalf("Provision limitIp=%d, want %d", fx.lastAddLimitIP, DeviceLimit)
	}
	if p.DeviceLimitFor("normaluser") != DeviceLimit {
		t.Fatalf("DeviceLimitFor(normal)=%d, want %d", p.DeviceLimitFor("normaluser"), DeviceLimit)
	}
	if p.DeviceLimitFor("WAPMIXX") != 0 {
		t.Fatalf("DeviceLimitFor(WAPMIXX)=%d, want 0 (case-insensitive exempt)", p.DeviceLimitFor("WAPMIXX"))
	}
}

func TestProvisionAllProtocols(t *testing.T) {
	p, fx, fh, st := newProv(t)
	cust, err := p.Provision("alice", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if cust.SubToken == "" || cust.VLESS == nil || cust.Hy2 == nil || cust.Naive == nil || cust.Mieru == nil {
		t.Fatalf("incomplete customer: %+v", cust)
	}
	if cust.Naive.Username != "mtv_alice" {
		t.Fatalf("naive username = %q, want mtv_alice", cust.Naive.Username)
	}
	if fx.logins != 1 || fx.adds != 1 {
		t.Fatalf("xui calls: logins=%d adds=%d", fx.logins, fx.adds)
	}
	if len(fh.lastNaive) != 1 || fh.lastNaive[0].User != "mtv_alice" {
		t.Fatalf("naive sync users = %+v, want [mtv_alice]", fh.lastNaive)
	}
	if len(fh.lastHy2) != 1 || fh.lastHy2[0].User != "alice" {
		t.Fatalf("hy2 sync users = %+v, want [alice]", fh.lastHy2)
	}
	if len(fh.lastMieru) != 1 || fh.lastMieru[0].User != "alice" {
		t.Fatalf("mieru sync users = %+v, want [alice]", fh.lastMieru)
	}
	if _, err := st.ByToken(cust.SubToken); err != nil {
		t.Fatalf("not stored: %v", err)
	}
}

func TestProvisionThenExpiredDroppedFromSync(t *testing.T) {
	p, _, fh, _ := newProv(t)
	if _, err := p.Provision("bob", -time.Hour); err != nil { // already expired
		t.Fatalf("Provision: %v", err)
	}
	// an expired customer must NOT be in any synced server-2 user set
	if len(fh.lastHy2) != 0 {
		t.Fatalf("expired customer leaked into hy2 sync: %+v", fh.lastHy2)
	}
	if len(fh.lastMieru) != 0 {
		t.Fatalf("expired customer leaked into mieru sync: %+v", fh.lastMieru)
	}
}

func TestExtendRenewsEverywhere(t *testing.T) {
	p, fx, fh, _ := newProv(t)
	if _, err := p.Provision("carol", -time.Hour); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	cust, err := p.Extend("carol", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if !cust.Active() {
		t.Fatal("not active after extend")
	}
	if fx.updates != 1 {
		t.Fatalf("xui updates = %d, want 1", fx.updates)
	}
	// now active → present in both hy2 and mieru syncs
	if len(fh.lastHy2) != 1 {
		t.Fatalf("renewed customer not in hy2 sync: %+v", fh.lastHy2)
	}
	if len(fh.lastMieru) != 1 {
		t.Fatalf("renewed customer not in mieru sync: %+v", fh.lastMieru)
	}
}

// TestBackfillAnyTLS: enabling AnyTLS after customers already exist gives them the 5th
// protocol by re-syncing ONLY the server-2 AnyTLS server (one batch), and is idempotent.
func TestBackfillAnyTLS(t *testing.T) {
	p, _, fh, st := newProv(t)
	if _, err := p.Provision("alice", 30*24*time.Hour); err != nil {
		t.Fatalf("provision alice: %v", err)
	}
	if _, err := p.Provision("bob", 30*24*time.Hour); err != nil {
		t.Fatalf("provision bob: %v", err)
	}
	// AnyTLS not wired yet → no-op.
	if n, err := p.BackfillAnyTLS(); err != nil || n != 0 {
		t.Fatalf("backfill before enable: n=%d err=%v, want 0,nil", n, err)
	}
	// Enable AnyTLS (server 2), then backfill the existing customers.
	p.cfg.AnyTLS = AnyTLSTmpl{Server: "wapmix.duckdns.org", Port: 8443, SNI: "wapmix.duckdns.org", Insecure: true}
	n, err := p.BackfillAnyTLS()
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 2 {
		t.Fatalf("backfilled %d, want 2", n)
	}
	if fh.anytlsSyncs != 1 {
		t.Fatalf("anytls synced %d times, want exactly 1", fh.anytlsSyncs)
	}
	if len(fh.lastAnyTLS) != 2 {
		t.Fatalf("anytls user set = %d, want 2", len(fh.lastAnyTLS))
	}
	for _, login := range []string{"alice", "bob"} {
		c, _ := st.ByLogin(login)
		if c.AnyTLS == nil || c.AnyTLS.Password == "" {
			t.Fatalf("%s has no AnyTLS creds after backfill", login)
		}
	}
	// Idempotent: nothing left to add, no extra sync.
	if n2, err := p.BackfillAnyTLS(); err != nil || n2 != 0 {
		t.Fatalf("second backfill n=%d err=%v, want 0,nil", n2, err)
	}
	if fh.anytlsSyncs != 1 {
		t.Fatalf("idempotent backfill re-synced: syncs=%d, want 1", fh.anytlsSyncs)
	}
}

// TestMigrateAnyTLSEndpoint: the S1:8444 → S2:8443 cutover repoints every customer's AnyTLS
// creds to the configured endpoint WITHOUT changing the password, re-syncs once, and is
// idempotent (a second run is a no-op).
func TestMigrateAnyTLSEndpoint(t *testing.T) {
	p, _, fh, st := newProv(t)
	// Provision two customers with AnyTLS on the OLD endpoint (server 1 :8444).
	p.cfg.AnyTLS = AnyTLSTmpl{Server: "wapmixx.ru", Port: 8444, SNI: "wapmixx.ru", Insecure: true}
	for _, login := range []string{"alice", "bob"} {
		if _, err := p.Provision(login, 30*24*time.Hour); err != nil {
			t.Fatalf("provision %s: %v", login, err)
		}
	}
	// Capture each customer's AnyTLS password — migration must preserve it.
	want := map[string]string{}
	for _, login := range []string{"alice", "bob"} {
		c, _ := st.ByLogin(login)
		if c.AnyTLS == nil || c.AnyTLS.Port != 8444 {
			t.Fatalf("%s not on old endpoint before migrate: %+v", login, c.AnyTLS)
		}
		want[login] = c.AnyTLS.Password
	}
	syncsBefore := fh.anytlsSyncs
	// Point config at server 2 and migrate.
	p.cfg.AnyTLS = AnyTLSTmpl{Server: "wapmix.duckdns.org", Port: 8443, SNI: "wapmix.duckdns.org", Insecure: true}
	n, err := p.MigrateAnyTLSEndpoint()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 2 {
		t.Fatalf("migrated %d, want 2", n)
	}
	if fh.anytlsSyncs != syncsBefore+1 {
		t.Fatalf("anytls synced %d times, want exactly one more than %d", fh.anytlsSyncs, syncsBefore)
	}
	if len(fh.lastAnyTLS) != 2 {
		t.Fatalf("anytls user set pushed to S2 = %d, want 2", len(fh.lastAnyTLS))
	}
	for _, login := range []string{"alice", "bob"} {
		c, _ := st.ByLogin(login)
		if c.AnyTLS.Server != "wapmix.duckdns.org" || c.AnyTLS.Port != 8443 || c.AnyTLS.SNI != "wapmix.duckdns.org" {
			t.Fatalf("%s not repointed to S2: %+v", login, c.AnyTLS)
		}
		if c.AnyTLS.Password != want[login] {
			t.Fatalf("%s AnyTLS password changed during migrate (must be preserved)", login)
		}
	}
	// Idempotent: everyone already on the target endpoint → no rewrite, no extra sync.
	n2, err := p.MigrateAnyTLSEndpoint()
	if err != nil || n2 != 0 {
		t.Fatalf("second migrate n=%d err=%v, want 0,nil", n2, err)
	}
	if fh.anytlsSyncs != syncsBefore+1 {
		t.Fatalf("idempotent migrate re-synced: syncs=%d", fh.anytlsSyncs)
	}
}
