package provision

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/server2"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

type fakeXUI struct {
	logins, adds, updates, dels int
	getSub                      string // if set, GetClient returns a client carrying this subId
	getExpiry                   int64  // if set, GetClient returns this expiryTime (millis) — for reconcile
	lastSub                     string // last subId passed to UpdateClient (assert subId preservation)
	lastAddLimitIP              int    // last limitIp passed to AddClient (assert the device cap)
	traffic                     int64  // UsedTraffic returns this
	updateErr                   error  // if set, UpdateClient fails — simulates x-ui being down mid-fan-out
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
	return f.updateErr
}
func (f *fakeXUI) GetClient(string) (*xui.ExistingClient, error) {
	if f.getSub == "" && f.getExpiry == 0 {
		return nil, nil
	}
	return &xui.ExistingClient{UUID: "u", Email: "e", SubID: f.getSub, ExpiryTime: f.getExpiry}, nil
}
func (f *fakeXUI) DelClient(string) error            { f.dels++; return nil }
func (f *fakeXUI) UsedTraffic(string) (int64, error) { return f.traffic, nil }

type fakeS2 struct {
	lastHy2     []server2.Hy2User
	lastNaive   []server2.NaiveUser
	lastAnyTLS  []server2.AnyTLSUser
	anytlsSyncs int
	hy2Syncs    int
	naiveSyncs  int
	naiveUser   string // if set, ReadNaiveUser returns this password (customer exists on naive)
}

func (f *fakeS2) SyncHy2Users(u []server2.Hy2User) error {
	f.lastHy2 = u
	f.hy2Syncs++
	return nil
}

func (f *fakeS2) SyncNaiveUsers(u []server2.NaiveUser) error {
	f.lastNaive = u
	f.naiveSyncs++
	return nil
}
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
	if cust.SubToken == "" || cust.VLESS == nil || cust.Hy2 == nil || cust.Naive == nil {
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
	// now active → present in the hy2 sync
	if len(fh.lastHy2) != 1 {
		t.Fatalf("renewed customer not in hy2 sync: %+v", fh.lastHy2)
	}
}

// TestExtendReturnsCustomerWhenFanOutFails закрывает ВТОРУЮ половину фикса двойного начисления.
// Первую (ветку Credited в обработчике) проверяет TestOrderConfirmRetryDoesNotDoubleCredit, но он
// работает на подставном провижинере — и остаётся зелёным, даже если здесь вернуть `nil, err`.
// Это ровно тот случай «молчаливый сторож хуже отсутствующего»: реальный Extend складывает дни в
// хранилище ДО раскатки, поэтому при падении раскатки он ОБЯЗАН вернуть клиента вместе с ошибкой —
// иначе обработчик не пометит заказ начисленным и повтор подтверждения начислит дни второй раз.
// Проверено падением: с `return nil, err` в Extend этот тест краснеет.
func TestExtendReturnsCustomerWhenFanOutFails(t *testing.T) {
	p, fx, _, st := newProv(t)
	if _, err := p.Provision("dave", 30*24*time.Hour); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	before, err := st.ByLogin("dave")
	if err != nil {
		t.Fatalf("ByLogin: %v", err)
	}
	wasExpires := before.Expires

	fx.updateErr = errors.New("x-ui недоступен")
	cust, err := p.Extend("dave", 30*24*time.Hour)

	if err == nil {
		t.Fatal("ожидалась ошибка раскатки, её нет")
	}
	if cust == nil {
		t.Fatal("Extend вернул nil вместе с ошибкой: вызывающий не узнает, что дни УЖЕ начислены → повтор подтверждения начислит второй раз")
	}
	after, err := st.ByLogin("dave")
	if err != nil {
		t.Fatalf("ByLogin после Extend: %v", err)
	}
	if !after.Expires.After(wasExpires) {
		t.Fatalf("дни не легли в хранилище (было %v, стало %v) — тест проверяет не тот сценарий", wasExpires, after.Expires)
	}
	if !cust.Expires.Equal(after.Expires) {
		t.Fatalf("вернулся клиент с датой %v, в хранилище %v — вызывающий увидит не то состояние", cust.Expires, after.Expires)
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

// TestDeleteCustomerRevokesOnServer2 guards the hole found on 2026-07-28: deleting a customer
// wiped them from S1/S3 VLESS and from the store, but left working hy2/naive/anytls creds on S2 —
// free access the panel could no longer revoke, because it had already forgotten the login.
// (Live case: `serbia` kept working creds in hysteria/anytls/Caddyfile and had to be cleaned by
// hand.) The old code relied on ReconcileExpiries to rebuild the S2 sets, but that function only
// pulls expiry dates — it never touches a login that is already gone from the store.
func TestDeleteCustomerRevokesOnServer2(t *testing.T) {
	p, _, fh, _ := newProv(t)
	for _, l := range []string{"alice", "bob"} {
		if _, err := p.Provision(l, 30*24*time.Hour); err != nil {
			t.Fatalf("provision %s: %v", l, err)
		}
	}
	if err := p.DeleteCustomer("alice"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, u := range fh.lastHy2 {
		if u.User == "alice" {
			t.Fatalf("hy2 still carries the deleted login: %+v", fh.lastHy2)
		}
	}
	if len(fh.lastHy2) != 1 || fh.lastHy2[0].User != "bob" {
		t.Fatalf("hy2 set should be exactly the remaining customer, got %+v", fh.lastHy2)
	}
	for _, u := range fh.lastNaive {
		if strings.Contains(u.User, "alice") {
			t.Fatalf("naive still carries the deleted login: %+v", fh.lastNaive)
		}
	}
}

// TestDeleteExpiredSyncsServer2Once: hy2 and anytls apply a new user set with `systemctl restart`,
// which drops every live connection on that protocol. So a bulk purge must sync ONCE at the end,
// not once per login — otherwise purging 14 expired accounts restarts hysteria 14 times in a row
// and every paying customer on it sees the connection drop each time.
func TestDeleteExpiredSyncsServer2Once(t *testing.T) {
	p, _, fh, st := newProv(t)
	for _, l := range []string{"gone1", "gone2", "gone3", "keeper"} {
		if _, err := p.Provision(l, 30*24*time.Hour); err != nil {
			t.Fatalf("provision %s: %v", l, err)
		}
	}
	// Expire three of them directly in the store (SetExpiry would fan out and sync, which is
	// exactly what we're counting).
	for _, l := range []string{"gone1", "gone2", "gone3"} {
		c, err := st.ByLogin(l)
		if err != nil {
			t.Fatalf("lookup %s: %v", l, err)
		}
		c.Expires = time.Now().Add(-time.Hour)
		if err := st.Put(c); err != nil {
			t.Fatalf("put %s: %v", l, err)
		}
	}
	fh.hy2Syncs, fh.naiveSyncs = 0, 0

	n, err := p.DeleteExpired()
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 3 {
		t.Fatalf("deleted %d, want 3", n)
	}
	if fh.hy2Syncs != 1 {
		t.Fatalf("hy2 synced %d times, want exactly 1 (one restart, not one per login)", fh.hy2Syncs)
	}
	if fh.naiveSyncs != 1 {
		t.Fatalf("naive synced %d times, want exactly 1", fh.naiveSyncs)
	}
	// And the surviving customer must still be there afterwards.
	if len(fh.lastHy2) != 1 || fh.lastHy2[0].User != "keeper" {
		t.Fatalf("hy2 set after purge = %+v, want only keeper", fh.lastHy2)
	}
	if _, err := st.ByLogin("keeper"); err != nil {
		t.Fatalf("keeper was purged: %v", err)
	}
}
