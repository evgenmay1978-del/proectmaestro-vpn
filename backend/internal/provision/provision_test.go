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
}

func (f *fakeXUI) Login() error                         { f.logins++; return nil }
func (f *fakeXUI) AddClient(int, xui.VLESSClient) error { f.adds++; return nil }
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
	lastHy2   []server2.Hy2User
	lastMieru []server2.MieruUser
	lastNaive []server2.NaiveUser
}

func (f *fakeS2) SyncHy2Users(u []server2.Hy2User) error       { f.lastHy2 = u; return nil }
func (f *fakeS2) SyncMieruUsers(u []server2.MieruUser) error   { f.lastMieru = u; return nil }
func (f *fakeS2) SyncNaiveUsers(u []server2.NaiveUser) error   { f.lastNaive = u; return nil }
func (f *fakeS2) ReadNaiveUser(string) (string, bool, error)   { return "", false, nil }
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
