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
}

// Hy2Syncer regenerates the server-2 Hysteria user set.
type Hy2Syncer interface {
	SyncHy2Users(users []server2.Hy2User) error
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

// Config holds the per-server templates.
type Config struct {
	VLESS VLESSTmpl
	Hy2   Hy2Tmpl
}

// Provisioner orchestrates the store + the per-server clients.
type Provisioner struct {
	st  *store.Store
	xui VLESSClienter
	s2  Hy2Syncer
	cfg Config
}

// New builds a provisioner.
func New(st *store.Store, x VLESSClienter, s2 Hy2Syncer, cfg Config) *Provisioner {
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
