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
}

func (f *fakeXUI) Login() error                                    { f.logins++; return nil }
func (f *fakeXUI) AddClient(int, xui.VLESSClient) error            { f.adds++; return nil }
func (f *fakeXUI) UpdateClient(int, string, xui.VLESSClient) error { f.updates++; return nil }
func (f *fakeXUI) GetClient(string) (*xui.ExistingClient, error)   { return nil, nil }

type fakeHy2 struct{ lastUsers []server2.Hy2User }

func (f *fakeHy2) SyncHy2Users(u []server2.Hy2User) error { f.lastUsers = u; return nil }

func newProv(t *testing.T) (*Provisioner, *fakeXUI, *fakeHy2, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	fx, fh := &fakeXUI{}, &fakeHy2{}
	cfg := Config{
		VLESS: VLESSTmpl{InboundID: 2, Server: "wapmixx.ru", Port: 443, Flow: "xtls-rprx-vision", SNI: "yahoo.com", PublicKey: "pk", ShortID: "ab"},
		Hy2:   Hy2Tmpl{Server: "wapmix.duckdns.org", Port: 8443, SNI: "wapmix.duckdns.org", Insecure: true},
	}
	return New(st, fx, fh, cfg), fx, fh, st
}

func TestProvisionAllProtocols(t *testing.T) {
	p, fx, fh, st := newProv(t)
	cust, err := p.Provision("alice", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if cust.SubToken == "" || cust.VLESS == nil || cust.Hy2 == nil {
		t.Fatalf("incomplete customer: %+v", cust)
	}
	if fx.logins != 1 || fx.adds != 1 {
		t.Fatalf("xui calls: logins=%d adds=%d", fx.logins, fx.adds)
	}
	if len(fh.lastUsers) != 1 || fh.lastUsers[0].User != "alice" {
		t.Fatalf("hy2 sync users = %+v, want [alice]", fh.lastUsers)
	}
	// persisted + servable by token
	if _, err := st.ByToken(cust.SubToken); err != nil {
		t.Fatalf("not stored: %v", err)
	}
}

func TestProvisionThenExpiredDroppedFromHy2(t *testing.T) {
	p, _, fh, _ := newProv(t)
	if _, err := p.Provision("bob", -time.Hour); err != nil { // already expired
		t.Fatalf("Provision: %v", err)
	}
	// expired customer must NOT be in the synced hy2 set
	if len(fh.lastUsers) != 0 {
		t.Fatalf("expired customer leaked into hy2 sync: %+v", fh.lastUsers)
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
	// now active → present in hy2 sync
	if len(fh.lastUsers) != 1 {
		t.Fatalf("renewed customer not in hy2 sync: %+v", fh.lastUsers)
	}
}
