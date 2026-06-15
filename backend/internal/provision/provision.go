// Package provision is the orchestration glue: one call provisions (or renews) a
// customer across every server — a VLESS client in 3x-ui (server 1) and a
// Hysteria2 user on server 2 — records it in the store, and re-syncs the
// server-2 Hysteria user set so only active customers can connect.
//
// (Naive and Mieru provisioning plug in here the same way once their server-side
// management is wired.)
package provision

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/server2"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

// VLESSClienter is the slice of the 3x-ui client used here (so it can be faked).
type VLESSClienter interface {
	Login() error
	AddClient(inboundID int, c xui.VLESSClient) error
	UpdateClient(inboundID int, uuid string, c xui.VLESSClient) error
	GetClient(email string) (*xui.ExistingClient, error)
}

// Server2 is the slice of the server-2 client provision needs (so it can be
// faked): the Hysteria + Mieru user-set syncs and the Naive user CRUD.
type Server2 interface {
	SyncHy2Users(users []server2.Hy2User) error
	SyncMieruUsers(users []server2.MieruUser) error
	AddNaiveUser(login, pass string, realExpiry time.Time) error
	SetNaiveExpiry(login string, realExpiry time.Time) error
	DelNaiveUser(login string) error
}

// VLESSTmpl is the server-side VLESS/Reality inbound facts shared by all clients.
type VLESSTmpl struct {
	InboundID   int
	Server      string
	Port        int
	SNI         string
	PublicKey   string
	ShortID     string
	Flow        string
	Fingerprint string
}

// Hy2Tmpl is the server-2 Hysteria listener facts shared by all clients.
type Hy2Tmpl struct {
	Server   string
	Port     int
	SNI      string
	Insecure bool
}

// NaiveTmpl is the server-2 Naive (Caddy forward_proxy) listener the app dials
// via sing-box's native `naive` outbound.
type NaiveTmpl struct {
	Server string // host the app connects to (caddy), e.g. the naive domain
	Port   int    // 443
	SNI    string
}

// MitaTmpl is the server-2 Mieru (mita) listener + the local SOCKS port the
// app's bundled mieru helper exposes for sing-box to dial.
type MitaTmpl struct {
	Server      string
	Port        int    // 2027
	Transport   string // "TCP"
	HelperSOCKS int    // 127.0.0.1:<port> the app's mieru helper listens on
}

// Config holds the per-server templates. Naive/Mita are optional: if their
// Server is empty, that protocol is simply not provisioned.
type Config struct {
	VLESS VLESSTmpl
	Hy2   Hy2Tmpl
	Naive NaiveTmpl
	Mita  MitaTmpl
}

// Provisioner orchestrates the store + the per-server clients.
type Provisioner struct {
	st  *store.Store
	xui VLESSClienter
	s2  Server2
	cfg Config
}

// New builds a provisioner.
func New(st *store.Store, x VLESSClienter, s2 Server2, cfg Config) *Provisioner {
	return &Provisioner{st: st, xui: x, s2: s2, cfg: cfg}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// uuid4 returns a random RFC-4122 v4 UUID (good enough for VLESS client ids).
func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Provision creates (or replaces) a customer with all protocols, active for dur.
// Returns the stored customer, whose SubToken forms the app subscription URL.
func (p *Provisioner) Provision(login string, dur time.Duration) (*store.Customer, error) {
	if login == "" {
		return nil, fmt.Errorf("provision: empty login")
	}
	expires := time.Now().Add(dur)
	uuid := uuid4()
	subTok := randHex(16)
	hy2Pass := randHex(16)

	// Server 1: VLESS client in 3x-ui.
	if err := p.xui.Login(); err != nil {
		return nil, fmt.Errorf("provision: xui login: %w", err)
	}
	vc := xui.VLESSClient{
		ID: uuid, Email: login, Flow: p.cfg.VLESS.Flow, Enable: true,
		SubID: subTok, ExpiryTime: expires.UnixMilli(),
	}
	if err := p.xui.AddClient(p.cfg.VLESS.InboundID, vc); err != nil {
		return nil, fmt.Errorf("provision: xui addClient: %w", err)
	}

	cust := &store.Customer{
		Login: login, SubToken: subTok, Expires: expires,
		VLESS: &subgen.VLESSCreds{
			Server: p.cfg.VLESS.Server, Port: p.cfg.VLESS.Port, UUID: uuid,
			Flow: p.cfg.VLESS.Flow, SNI: p.cfg.VLESS.SNI,
			PublicKey: p.cfg.VLESS.PublicKey, ShortID: p.cfg.VLESS.ShortID,
			Fingerprint: p.cfg.VLESS.Fingerprint,
		},
		Hy2: &subgen.Hy2Creds{
			Server: p.cfg.Hy2.Server, Port: p.cfg.Hy2.Port, User: login, Pass: hy2Pass,
			SNI: p.cfg.Hy2.SNI, Insecure: p.cfg.Hy2.Insecure,
		},
	}
	if err := p.st.Put(cust); err != nil {
		return nil, fmt.Errorf("provision: store: %w", err)
	}

	// Server 2: re-sync the Hysteria user set to include this customer.
	if err := p.syncHy2(); err != nil {
		return nil, fmt.Errorf("provision: hy2 sync: %w", err)
	}

	// Extra protocols — best-effort: never fail the provision (VLESS+Hy2 are
	// already up) if Naive's panel or the Mieru daemon isn't reachable yet.
	p.addNaive(cust, login)
	p.addMieru(cust, login)
	if err := p.st.Put(cust); err != nil {
		return nil, fmt.Errorf("provision: store extras: %w", err)
	}
	if cust.Mieru != nil {
		if err := p.syncMieru(); err != nil {
			log.Printf("provision: mieru sync for %q skipped: %v", login, err)
			cust.Mieru = nil
			_ = p.st.Put(cust)
		}
	}
	return cust, nil
}

// ActivateExisting activates an EXISTING panel customer by their login: it looks
// the login up in 3x-ui and, if found, registers a subscription mirroring their
// VLESS access + existing expiry (creates NO new panel client). Lets customers
// already in the panels just type their login to use the app.
func (p *Provisioner) ActivateExisting(login string) (*store.Customer, error) {
	if login == "" {
		return nil, fmt.Errorf("provision: empty login")
	}
	if err := p.xui.Login(); err != nil {
		return nil, fmt.Errorf("provision: xui login: %w", err)
	}
	ex, err := p.xui.GetClient(login)
	if err != nil {
		return nil, fmt.Errorf("provision: lookup %q: %w", login, err)
	}
	if ex == nil || ex.UUID == "" {
		return nil, fmt.Errorf("provision: login %q not found in panels", login)
	}
	expires := time.Now().Add(365 * 24 * time.Hour)
	if ex.ExpiryTime > 0 {
		expires = time.UnixMilli(ex.ExpiryTime)
	}
	cust := &store.Customer{
		Login: login, SubToken: randHex(16), Expires: expires,
		VLESS: &subgen.VLESSCreds{
			Server: p.cfg.VLESS.Server, Port: p.cfg.VLESS.Port, UUID: ex.UUID,
			Flow: p.cfg.VLESS.Flow, SNI: p.cfg.VLESS.SNI,
			PublicKey: p.cfg.VLESS.PublicKey, ShortID: p.cfg.VLESS.ShortID,
			Fingerprint: p.cfg.VLESS.Fingerprint,
		},
	}
	if err := p.st.Put(cust); err != nil {
		return nil, fmt.Errorf("provision: store: %w", err)
	}
	return cust, nil
}

// Extend renews a customer across all servers.
func (p *Provisioner) Extend(login string, dur time.Duration) (*store.Customer, error) {
	cust, err := p.st.Extend(login, dur)
	if err != nil {
		return nil, fmt.Errorf("provision: extend store: %w", err)
	}
	if cust.VLESS != nil {
		if err := p.xui.Login(); err != nil {
			return nil, fmt.Errorf("provision: xui login: %w", err)
		}
		vc := xui.VLESSClient{
			ID: cust.VLESS.UUID, Email: cust.Login, Flow: cust.VLESS.Flow, Enable: true,
			SubID: cust.SubToken, ExpiryTime: cust.Expires.UnixMilli(),
		}
		if err := p.xui.UpdateClient(p.cfg.VLESS.InboundID, cust.VLESS.UUID, vc); err != nil {
			return nil, fmt.Errorf("provision: xui updateClient: %w", err)
		}
	}
	if err := p.syncHy2(); err != nil {
		return nil, fmt.Errorf("provision: hy2 sync: %w", err)
	}
	if cust.Naive != nil {
		if err := p.s2.SetNaiveExpiry(cust.Login, cust.Expires); err != nil {
			log.Printf("provision: extend naive %q: %v", cust.Login, err)
		}
	}
	if cust.Mieru != nil {
		if err := p.syncMieru(); err != nil {
			log.Printf("provision: extend mieru %q: %v", cust.Login, err)
		}
	}
	return cust, nil
}

// syncHy2 regenerates server-2's Hysteria user set from all ACTIVE customers, so
// an expired or disabled customer is dropped and can no longer connect.
func (p *Provisioner) syncHy2() error {
	var users []server2.Hy2User
	for _, c := range p.st.List() {
		if c.Active() && c.Hy2 != nil {
			users = append(users, server2.Hy2User{User: c.Hy2.User, Pass: c.Hy2.Pass})
		}
	}
	return p.s2.SyncHy2Users(users)
}

// addNaive creates a Naive user on server 2 and records native-outbound creds.
// No-op if Naive isn't configured; on panel failure it logs and leaves the
// customer without Naive (so the sub never advertises a protocol they can't use).
func (p *Provisioner) addNaive(cust *store.Customer, login string) {
	if p.cfg.Naive.Server == "" {
		return
	}
	pass := randHex(16)
	if err := p.s2.AddNaiveUser(login, pass, cust.Expires); err != nil {
		log.Printf("provision: naive for %q skipped: %v", login, err)
		return
	}
	cust.Naive = &subgen.NaiveCreds{
		Server: p.cfg.Naive.Server, Port: p.cfg.Naive.Port,
		Username: server2.NaivePrefix + login, Password: pass, SNI: p.cfg.Naive.SNI,
	}
}

// addMieru records Mieru creds; the actual server-side user set is pushed by
// syncMieru (called after the customer is stored). No-op if Mieru isn't configured.
func (p *Provisioner) addMieru(cust *store.Customer, login string) {
	if p.cfg.Mita.Server == "" {
		return
	}
	cust.Mieru = &subgen.MieruCreds{
		Server: p.cfg.Mita.Server, Port: p.cfg.Mita.Port,
		Username: login, Password: randHex(16),
		Transport: fallbackStr(p.cfg.Mita.Transport, "TCP"), HelperSOCKS: p.cfg.Mita.HelperSOCKS,
	}
}

// syncMieru regenerates server-2's mita user set from all ACTIVE customers.
func (p *Provisioner) syncMieru() error {
	var users []server2.MieruUser
	for _, c := range p.st.List() {
		if c.Active() && c.Mieru != nil {
			users = append(users, server2.MieruUser{User: c.Mieru.Username, Pass: c.Mieru.Password})
		}
	}
	return p.s2.SyncMieruUsers(users)
}

func fallbackStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
