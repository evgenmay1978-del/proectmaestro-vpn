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
	"regexp"
	"strings"
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
// faked): the Hysteria/Mieru/Naive user-set syncs plus a lookup of an existing
// naive credential (for activating customers already on the naive panel).
type Server2 interface {
	SyncHy2Users(users []server2.Hy2User) error
	SyncMieruUsers(users []server2.MieruUser) error
	SyncNaiveUsers(users []server2.NaiveUser) error
	ReadNaiveUser(username string) (string, bool, error)
	ReadProxyExpiry(proxyUser string) (string, bool, error)
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
// loginRe bounds a login/claim-code to a safe charset (alphanumerics + . _ @ -).
// Logins flow UNAUTHENTICATED from POST /claim into server-2 SSH commands
// (ReadNaiveUser/ReadProxyExpiry, Caddyfile/Hysteria sync), so anything able to
// break a shell/awk/sql quote — quotes, spaces, ;, $, backtick, newlines — must
// be rejected at the door. This is the hard stop against command injection.
var loginRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// ValidLogin reports whether a login/claim-code is safe to provision with.
func ValidLogin(login string) bool { return loginRe.MatchString(login) }

// Returns the stored customer, whose SubToken forms the app subscription URL.
func (p *Provisioner) Provision(login string, dur time.Duration) (*store.Customer, error) {
	if !ValidLogin(login) {
		return nil, fmt.Errorf("provision: invalid login")
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
	if cust.Naive != nil {
		if err := p.syncNaive(); err != nil {
			log.Printf("provision: naive sync for %q skipped: %v", login, err)
			cust.Naive = nil
			_ = p.st.Put(cust)
		}
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

// ActivateExisting activates an EXISTING customer (in 3x-ui and/or on the naive
// panel) by their login and gives them ALL protocols: it reuses their existing
// VLESS / Naive credential where present and creates the rest (Hy2, Mieru, plus
// VLESS or Naive if they lacked one), at their existing 3x-ui expiry (or 30 days
// if only on the naive panel). So a customer already paying just types their login
// and gets the full multi-protocol app.
func (p *Provisioner) ActivateExisting(login string) (*store.Customer, error) {
	if !ValidLogin(login) {
		return nil, fmt.Errorf("provision: invalid login")
	}
	if err := p.xui.Login(); err != nil {
		return nil, fmt.Errorf("provision: xui login: %w", err)
	}
	ex, _ := p.xui.GetClient(login) // 3x-ui client (nil if absent)

	// Existing naive customer = their raw Caddy basic_auth (not an mtv_ one).
	naivePass, naiveFound := "", false
	if p.cfg.Naive.Server != "" {
		if pw, ok, rerr := p.s2.ReadNaiveUser(login); rerr == nil {
			naivePass, naiveFound = pw, ok
		}
	}
	if (ex == nil || ex.UUID == "") && !naiveFound {
		return nil, fmt.Errorf("provision: login %q not found in panels", login)
	}

	expires := time.Now().Add(30 * 24 * time.Hour)
	if ex != nil && ex.ExpiryTime > 0 {
		expires = time.UnixMilli(ex.ExpiryTime)
	} else if naiveFound {
		// Carry over the real end date from the server-2 naive bot DB (the panel /
		// Caddyfile don't store it) so a naive customer keeps their actual sub.
		if raw, ok, _ := p.s2.ReadProxyExpiry(login); ok {
			if t, perr := parseProxyExpiry(raw); perr == nil {
				expires = t
			}
		}
	}
	subTok := randHex(16)
	cust := &store.Customer{Login: login, SubToken: subTok, Expires: expires}

	// VLESS: reuse their 3x-ui client, else create one so they get VLESS too.
	uuid := ""
	if ex != nil && ex.UUID != "" {
		uuid = ex.UUID
	} else {
		uuid = uuid4()
		vc := xui.VLESSClient{ID: uuid, Email: login, Flow: p.cfg.VLESS.Flow, Enable: true, SubID: subTok, ExpiryTime: expires.UnixMilli()}
		if err := p.xui.AddClient(p.cfg.VLESS.InboundID, vc); err != nil {
			log.Printf("activate: vless create for %q: %v", login, err)
			uuid = ""
		}
	}
	if uuid != "" {
		cust.VLESS = &subgen.VLESSCreds{
			Server: p.cfg.VLESS.Server, Port: p.cfg.VLESS.Port, UUID: uuid,
			Flow: p.cfg.VLESS.Flow, SNI: p.cfg.VLESS.SNI,
			PublicKey: p.cfg.VLESS.PublicKey, ShortID: p.cfg.VLESS.ShortID, Fingerprint: p.cfg.VLESS.Fingerprint,
		}
	}

	// Hy2: always create.
	cust.Hy2 = &subgen.Hy2Creds{
		Server: p.cfg.Hy2.Server, Port: p.cfg.Hy2.Port, User: login, Pass: randHex(16),
		SNI: p.cfg.Hy2.SNI, Insecure: p.cfg.Hy2.Insecure,
	}

	// Naive: reuse their existing Caddy credential, else create an mtv_ one.
	if naiveFound {
		cust.Naive = &subgen.NaiveCreds{
			Server: p.cfg.Naive.Server, Port: p.cfg.Naive.Port,
			Username: login, Password: naivePass, SNI: p.cfg.Naive.SNI,
		}
	} else {
		p.addNaive(cust, login)
	}

	// Mieru.
	p.addMieru(cust, login)

	if err := p.st.Put(cust); err != nil {
		return nil, fmt.Errorf("provision: store: %w", err)
	}
	// Push the freshly created server-2 user sets (best-effort).
	if err := p.syncHy2(); err != nil {
		log.Printf("activate: hy2 sync %q: %v", login, err)
	}
	if cust.Naive != nil && strings.HasPrefix(cust.Naive.Username, server2.NaivePrefix) {
		if err := p.syncNaive(); err != nil {
			log.Printf("activate: naive sync %q: %v", login, err)
		}
	}
	if cust.Mieru != nil {
		if err := p.syncMieru(); err != nil {
			log.Printf("activate: mieru sync %q: %v", login, err)
			cust.Mieru = nil
			_ = p.st.Put(cust)
		}
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
	if cust.Naive != nil && strings.HasPrefix(cust.Naive.Username, server2.NaivePrefix) {
		if err := p.syncNaive(); err != nil {
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

// addNaive records app-managed Naive creds (mtv_-prefixed Caddy basic_auth);
// syncNaive pushes the full app set to server 2. No-op if Naive isn't configured.
func (p *Provisioner) addNaive(cust *store.Customer, login string) {
	if p.cfg.Naive.Server == "" {
		return
	}
	cust.Naive = &subgen.NaiveCreds{
		Server: p.cfg.Naive.Server, Port: p.cfg.Naive.Port,
		Username: server2.NaivePrefix + login, Password: randHex(16), SNI: p.cfg.Naive.SNI,
	}
}

// syncNaive regenerates server-2's app-managed (mtv_) naive user set in the
// Caddyfile MTV block. Customers reusing their OWN pre-existing Caddy credential
// are skipped — they already live in the Caddyfile, outside the MTV block.
func (p *Provisioner) syncNaive() error {
	var users []server2.NaiveUser
	for _, c := range p.st.List() {
		if c.Active() && c.Naive != nil && strings.HasPrefix(c.Naive.Username, server2.NaivePrefix) {
			users = append(users, server2.NaiveUser{User: c.Naive.Username, Pass: c.Naive.Password})
		}
	}
	return p.s2.SyncNaiveUsers(users)
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

// parseProxyExpiry parses the ISO end date stored by the server-2 naive bot
// (RFC3339, or fractional seconds with no zone = treated as UTC).
func parseProxyExpiry(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("provision: unparseable proxy expiry %q", s)
}
