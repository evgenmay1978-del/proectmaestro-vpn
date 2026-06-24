// Package provision is the orchestration glue: one call provisions (or renews) a
// customer across every server — a VLESS client in 3x-ui (server 1) and a
// Hysteria2 user on server 2 — records it in the store, and re-syncs the
// server-2 Hysteria user set so only active customers can connect.
package provision

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
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

// NodeClienter is a 3x-ui panel client for the 3rd node (S3), which serves BOTH a
// VLESS-Reality and a Trojan inbound — so it needs the VLESS client ops PLUS the
// password-based Trojan client ops. The real *xui.Client satisfies it.
type NodeClienter interface {
	VLESSClienter
	AddTrojanClient(inboundID int, c xui.TrojanClient) error
	UpdateTrojanClient(inboundID int, email string, c xui.TrojanClient) error
}

// Server2 is the slice of the server-2 client provision needs (so it can be
// faked): the Hysteria/Naive user-set syncs plus a lookup of an existing
// naive credential (for activating customers already on the naive panel).
type Server2 interface {
	SyncHy2Users(users []server2.Hy2User) error
	SyncNaiveUsers(users []server2.NaiveUser) error
	SyncAnyTLSUsers(users []server2.AnyTLSUser) error
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

// AnyTLSTmpl is the server-2 AnyTLS listener facts shared by all clients. AnyTLS is a
// NATIVE sing-box outbound (no on-device helper). Optional: empty Server = not provisioned.
type AnyTLSTmpl struct {
	Server   string
	Port     int
	SNI      string
	Insecure bool // self-signed cert → true
}

// TrojanTmpl is the 3rd-node (S3) Trojan inbound facts shared by all clients.
// Optional: empty Server = not provisioned. Self-signed cert → Insecure=true.
type TrojanTmpl struct {
	InboundID int
	Server    string
	Port      int
	SNI       string
	Insecure  bool
}

// Config holds the per-server templates. Naive/AnyTLS/VLESS3/Trojan are optional:
// if their Server is empty, that protocol is simply not provisioned.
type Config struct {
	VLESS  VLESSTmpl
	Hy2    Hy2Tmpl
	Naive  NaiveTmpl
	AnyTLS AnyTLSTmpl
	VLESS3 VLESSTmpl  // VLESS-Reality on the 3rd node (S3); empty Server = off
	Trojan TrojanTmpl // Trojan on the 3rd node (S3); empty Server = off
}

// Provisioner orchestrates the store + the per-server clients.
type Provisioner struct {
	// mu serializes Provision/Extend/ActivateExisting so a double-tapped owner
	// confirm (or two concurrent /claim hits for the same login) can't run two
	// provisions for the same customer at once (TOCTOU double-provision).
	mu   sync.Mutex
	st   *store.Store
	xui  VLESSClienter
	s2   Server2
	xui3 NodeClienter // 3rd node (S3) 3x-ui panel; nil = S3 not configured
	cfg  Config
}

// New builds a provisioner.
func New(st *store.Store, x VLESSClienter, s2 Server2, cfg Config) *Provisioner {
	return &Provisioner{st: st, xui: x, s2: s2, cfg: cfg}
}

// SetS3Node wires the 3rd-node (S3) 3x-ui client. Called from main.go when S3 is
// configured; left nil otherwise so all S3 code paths are no-ops. Kept separate from
// New() so existing callers/tests don't change.
func (p *Provisioner) SetS3Node(x NodeClienter) { p.xui3 = x }

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

// DeviceLimit caps simultaneous devices/IPs per login (3x-ui limitIp).
const DeviceLimit = 5

// unlimitedLogins are exempt from the device cap (the owner's admin logins — same person,
// one Telegram; all unlimited on devices AND days).
var unlimitedLogins = map[string]bool{"wapmix": true, "wapmixx": true, "wapmix2": true}

// deviceLimitOverrides raises (or lowers) the cap for specific logins — e.g. a customer
// with more household devices. (For UNLIMITED use unlimitedLogins, not 0 here.)
var deviceLimitOverrides = map[string]int{"strogino": 8}

// deviceLimit returns the per-login limitIp (0 = unlimited).
func deviceLimit(login string) int {
	l := strings.ToLower(login)
	if unlimitedLogins[l] {
		return 0
	}
	if n, ok := deviceLimitOverrides[l]; ok {
		return n
	}
	return DeviceLimit
}

// DeviceLimitFor exposes the per-login device cap (0 = unlimited, for the owner's admin
// logins) so the subscription endpoint can enforce the SAME cap + wapmix/wapmixx exemption
// that 3x-ui's limitIp uses — one source of truth, no drift between the two layers.
func (p *Provisioner) DeviceLimitFor(login string) int { return deviceLimit(login) }

// Returns the stored customer, whose SubToken forms the app subscription URL.
func (p *Provisioner) Provision(login string, dur time.Duration) (*store.Customer, error) {
	if !ValidLogin(login) {
		return nil, fmt.Errorf("provision: invalid login")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	expires := time.Now().Add(dur)
	// Idempotent: on a retry (e.g. re-confirm after a transient hy2-sync failure)
	// reuse the prior record's secrets so we REFRESH the same customer instead of
	// orphaning the old SubToken or duplicating the 3x-ui client.
	uuid := uuid4()
	subTok := randHex(16)
	hy2Pass := randHex(16)
	if prev, perr := p.st.ByLogin(login); perr == nil && prev != nil {
		if prev.SubToken != "" {
			subTok = prev.SubToken
		}
		if prev.VLESS != nil && prev.VLESS.UUID != "" {
			uuid = prev.VLESS.UUID
		}
		if prev.Hy2 != nil && prev.Hy2.Pass != "" {
			hy2Pass = prev.Hy2.Pass
		}
	}

	// Server 1: VLESS client in 3x-ui.
	if err := p.xui.Login(); err != nil {
		return nil, fmt.Errorf("provision: xui login: %w", err)
	}
	vc := xui.VLESSClient{
		ID: uuid, Email: login, Flow: p.cfg.VLESS.Flow, Enable: true,
		SubID: subTok, ExpiryTime: expires.UnixMilli(), LimitIP: deviceLimit(login),
	}
	// Update an already-present client, else add — so a retry never fails on a
	// duplicate-email AddClient (which previously wedged the order permanently).
	if ex, _ := p.xui.GetClient(login); ex != nil {
		// Preserve the existing 3x-ui subId. For a bot-sold client this is the :2096
		// sub id every customer already imported into Karing — overwriting it with the
		// panel SubToken would break their bot subscription. (App-only clients keep the
		// same value either way.)
		if ex.SubID != "" {
			vc.SubID = ex.SubID
		}
		if err := p.xui.UpdateClient(p.cfg.VLESS.InboundID, login, vc); err != nil {
			return nil, fmt.Errorf("provision: xui updateClient: %w", err)
		}
	} else if err := p.xui.AddClient(p.cfg.VLESS.InboundID, vc); err != nil {
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
	// already up) if Naive's panel isn't reachable yet.
	p.addNaive(cust, login)
	p.addAnyTLS(cust, login)
	p.provisionS3(cust, login) // 3rd node (S3): VLESS-Reality + Trojan, best-effort
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
	if cust.AnyTLS != nil {
		if err := p.syncAnyTLS(); err != nil {
			log.Printf("provision: anytls sync for %q skipped: %v", login, err)
			cust.AnyTLS = nil
			_ = p.st.Put(cust)
		}
	}
	return cust, nil
}

// ActivateExisting activates an EXISTING customer (in 3x-ui and/or on the naive
// panel) by their login and gives them ALL protocols: it reuses their existing
// VLESS / Naive credential where present and creates the rest (Hy2, plus
// VLESS or Naive if they lacked one), at their existing 3x-ui expiry (or 30 days
// if only on the naive panel). So a customer already paying just types their login
// and gets the full multi-protocol app.
func (p *Provisioner) ActivateExisting(login string) (*store.Customer, error) {
	if !ValidLogin(login) {
		return nil, fmt.Errorf("provision: invalid login")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
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
		vc := xui.VLESSClient{ID: uuid, Email: login, Flow: p.cfg.VLESS.Flow, Enable: true, SubID: subTok, ExpiryTime: expires.UnixMilli(), LimitIP: deviceLimit(login)}
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

	// AnyTLS (native sing-box outbound, server 1).
	p.addAnyTLS(cust, login)
	p.provisionS3(cust, login) // 3rd node (S3): VLESS-Reality + Trojan, best-effort

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
	if cust.AnyTLS != nil {
		if err := p.syncAnyTLS(); err != nil {
			log.Printf("activate: anytls sync %q: %v", login, err)
			cust.AnyTLS = nil
			_ = p.st.Put(cust)
		}
	}
	return cust, nil
}

// Extend renews a customer across all servers.
func (p *Provisioner) Extend(login string, dur time.Duration) (*store.Customer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cust, err := p.st.Extend(login, dur)
	if err != nil {
		return nil, fmt.Errorf("provision: extend store: %w", err)
	}
	if err := p.fanOutExpiry(cust); err != nil {
		return nil, err
	}
	return cust, nil
}

// SetExpiry mirrors an ABSOLUTE expiry into the store and fans it out to all protocols
// (no day-stacking, unlike Extend). Used by /admin/set-expiry so a channel that OWNS the
// date elsewhere (the s2 naive bot) can sync the SAME date here without double-counting.
func (p *Provisioner) SetExpiry(login string, t time.Time) (*store.Customer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cust, err := p.st.SetExpiry(login, t)
	if err != nil {
		return nil, fmt.Errorf("provision: set expiry store: %w", err)
	}
	if err := p.fanOutExpiry(cust); err != nil {
		return nil, err
	}
	return cust, nil
}

// fanOutExpiry pushes the customer's current store expiry to 3x-ui (VLESS date, preserving
// the bot subId) and re-syncs Hy2 / Naive(mtv_ only) membership. The caller holds
// p.mu. It deliberately does NOT touch a raw (non-mtv_) naive user — the s2 bot owns that.
func (p *Provisioner) fanOutExpiry(cust *store.Customer) error {
	if cust.VLESS != nil {
		if err := p.xui.Login(); err != nil {
			return fmt.Errorf("provision: xui login: %w", err)
		}
		// Preserve the existing 3x-ui subId (a bot-sold client's :2096 sub id) instead of
		// overwriting it with the panel SubToken — a renewal must NOT break the customer's
		// imported bot subscription. Falls back to SubToken if the client has none.
		subID := cust.SubToken
		if ex, _ := p.xui.GetClient(cust.Login); ex != nil && ex.SubID != "" {
			subID = ex.SubID
		}
		vc := xui.VLESSClient{
			ID: cust.VLESS.UUID, Email: cust.Login, Flow: cust.VLESS.Flow, Enable: true,
			SubID: subID, ExpiryTime: cust.Expires.UnixMilli(), LimitIP: deviceLimit(cust.Login),
		}
		if err := p.xui.UpdateClient(p.cfg.VLESS.InboundID, cust.VLESS.UUID, vc); err != nil {
			return fmt.Errorf("provision: xui updateClient: %w", err)
		}
	}
	if err := p.syncHy2(); err != nil {
		return fmt.Errorf("provision: hy2 sync: %w", err)
	}
	if cust.Naive != nil && strings.HasPrefix(cust.Naive.Username, server2.NaivePrefix) {
		if err := p.syncNaive(); err != nil {
			log.Printf("provision: sync naive %q: %v", cust.Login, err)
		}
	}
	if cust.AnyTLS != nil {
		if err := p.syncAnyTLS(); err != nil {
			log.Printf("provision: sync anytls %q: %v", cust.Login, err)
		}
	}
	// 3rd node (S3): push the new expiry to the VLESS/Trojan clients (and add them if a
	// renewal is the first time this customer touches S3). Persist any newly-added creds.
	p.provisionS3(cust, cust.Login)
	if cust.VLESS3 != nil || cust.Trojan != nil {
		_ = p.st.Put(cust)
	}
	return nil
}

// ReconcileExpiries mirrors each unified customer's authoritative expiry from whichever
// panel OWNS it — 3x-ui (the VLESS client's expiryTime) and/or the s2 naive panel (the
// raw bot user's bot_minimal.db date) — into the store, ADVANCE-ONLY: it takes the LATEST
// of {store, 3x-ui, naive} and never reduces an app/panel renewal. A customer created in
// the maestro panel is already authoritative in the store, so pulling just confirms it.
// So no matter which of the 3 panels a client originated in or was last renewed through,
// the app's days + the customer's cross-protocol expiry stay in sync. Runs at startup +
// every 15 min over the existing SSH / API; per-customer errors are logged and skipped.
func (p *Provisioner) ReconcileExpiries() {
	for _, c := range p.st.List() {
		latest := c.Expires
		// 3x-ui (VLESS): pull the client's expiryTime (0 = never → skip; never reduce).
		if c.VLESS != nil {
			if ex, err := p.xui.GetClient(c.Login); err == nil && ex != nil && ex.ExpiryTime > 0 {
				if t := time.UnixMilli(ex.ExpiryTime); t.After(latest) {
					latest = t
				}
			}
		}
		// s2 naive panel (only RAW, non-mtv_ users — the s2 bot owns those dates).
		if c.Naive != nil && c.Naive.Username != "" && !strings.HasPrefix(c.Naive.Username, server2.NaivePrefix) {
			if raw, ok, err := p.s2.ReadProxyExpiry(c.Naive.Username); err == nil && ok {
				if t, perr := parseProxyExpiry(raw); perr == nil && t.After(latest) {
					latest = t
				}
			}
		}
		if latest.After(c.Expires) {
			if _, err := p.SetExpiry(c.Login, latest); err != nil {
				log.Printf("reconcile %q: %v", c.Login, err)
			} else {
				log.Printf("reconcile %q: store expiry advanced to %s (pulled from a panel)", c.Login, latest.UTC().Format(time.RFC3339))
			}
		}
	}
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

// addAnyTLS records AnyTLS creds (a NATIVE sing-box outbound — no on-device helper).
// No-op if AnyTLS isn't configured. The client authenticates by password, so it's unique.
func (p *Provisioner) addAnyTLS(cust *store.Customer, login string) {
	if p.cfg.AnyTLS.Server == "" {
		return
	}
	cust.AnyTLS = &subgen.AnyTLSCreds{
		Server: p.cfg.AnyTLS.Server, Port: p.cfg.AnyTLS.Port,
		Password: randHex(16), SNI: p.cfg.AnyTLS.SNI, Insecure: p.cfg.AnyTLS.Insecure,
	}
}

// syncAnyTLS regenerates server-2's standalone sing-box AnyTLS user set from all ACTIVE
// customers, so an expired/disabled customer can no longer connect. No-op if AnyTLS off.
func (p *Provisioner) syncAnyTLS() error {
	if p.cfg.AnyTLS.Server == "" {
		return nil
	}
	var users []server2.AnyTLSUser
	for _, c := range p.st.List() {
		if c.Active() && c.AnyTLS != nil {
			users = append(users, server2.AnyTLSUser{Name: c.Login, Pass: c.AnyTLS.Password})
		}
	}
	return p.s2.SyncAnyTLSUsers(users)
}

// BackfillAnyTLS gives AnyTLS to every stored customer that lacks it WITHOUT touching any
// other protocol: it only re-syncs the server-2 AnyTLS server (sing-box-anytls), never
// hy2/naive, so existing customers' live connections are never disturbed. No-op when
// AnyTLS isn't configured. Idempotent — re-running only adds it to anyone still missing it.
// Returns how many customers were backfilled. Run once after enabling AnyTLS so the existing
// customer base gets the 5th protocol with zero blip to the other four.
func (p *Provisioner) BackfillAnyTLS() (int, error) {
	if p.cfg.AnyTLS.Server == "" {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.st.List() {
		if c.AnyTLS != nil {
			continue
		}
		p.addAnyTLS(c, c.Login)
		if c.AnyTLS == nil {
			continue
		}
		if err := p.st.Put(c); err != nil {
			return n, fmt.Errorf("provision: backfill anytls store %q: %w", c.Login, err)
		}
		n++
	}
	if n > 0 {
		if err := p.syncAnyTLS(); err != nil {
			return n, fmt.Errorf("provision: backfill anytls sync: %w", err)
		}
	}
	return n, nil
}

// trojanEmail derives the S3 Trojan client's panel email. 3x-ui emails are unique across
// the whole panel, so the Trojan client can't reuse the VLESS client's email (= login).
func trojanEmail(login string) string { return login + "-tr" }

// provisionS3 best-effort adds/updates the customer on the 3rd node (S3): VLESS-Reality
// (reusing their VLESS uuid for one identity) + Trojan (password). On success it sets
// cust.VLESS3 / cust.Trojan. It NEVER fails the caller — S3 is additive; if the node is
// unreachable the other servers still stand and the customer is just missing S3 until the
// next provision/backfill. Caller holds p.mu.
func (p *Provisioner) provisionS3(cust *store.Customer, login string) {
	if p.xui3 == nil || p.cfg.VLESS3.Server == "" {
		return
	}
	if err := p.xui3.Login(); err != nil {
		log.Printf("provision: s3 login %q: %v", login, err)
		return
	}
	// VLESS-Reality on S3 — reuse the customer's VLESS uuid (one client identity).
	uuid := ""
	switch {
	case cust.VLESS != nil && cust.VLESS.UUID != "":
		uuid = cust.VLESS.UUID
	case cust.VLESS3 != nil && cust.VLESS3.UUID != "":
		uuid = cust.VLESS3.UUID
	default:
		uuid = uuid4()
	}
	vc := xui.VLESSClient{
		ID: uuid, Email: login, Flow: p.cfg.VLESS3.Flow, Enable: true,
		SubID: cust.SubToken, ExpiryTime: cust.Expires.UnixMilli(), LimitIP: deviceLimit(login),
	}
	var verr error
	if ex, _ := p.xui3.GetClient(login); ex != nil {
		if ex.SubID != "" {
			vc.SubID = ex.SubID
		}
		verr = p.xui3.UpdateClient(p.cfg.VLESS3.InboundID, login, vc)
	} else {
		verr = p.xui3.AddClient(p.cfg.VLESS3.InboundID, vc)
	}
	if verr != nil {
		log.Printf("provision: s3 vless %q: %v", login, verr)
	} else {
		cust.VLESS3 = &subgen.VLESSCreds{
			Server: p.cfg.VLESS3.Server, Port: p.cfg.VLESS3.Port, UUID: uuid,
			Flow: p.cfg.VLESS3.Flow, SNI: p.cfg.VLESS3.SNI,
			PublicKey: p.cfg.VLESS3.PublicKey, ShortID: p.cfg.VLESS3.ShortID, Fingerprint: p.cfg.VLESS3.Fingerprint,
		}
	}
	// Trojan on S3 — distinct panel email, password auth.
	if p.cfg.Trojan.Server != "" {
		pass := randHex(16)
		if cust.Trojan != nil && cust.Trojan.Password != "" {
			pass = cust.Trojan.Password
		}
		email := trojanEmail(login)
		// Distinct subId: 3x-ui requires a globally-unique subId per panel, so the Trojan
		// client can't reuse the VLESS client's subId (= SubToken). The value is irrelevant to
		// our app sub (built from stored creds, not 3x-ui's :2096 sub) — it just must be unique.
		tc := xui.TrojanClient{
			Password: pass, Email: email, Enable: true,
			SubID: cust.SubToken + "-tr", ExpiryTime: cust.Expires.UnixMilli(), LimitIP: deviceLimit(login),
		}
		var terr error
		if ex, _ := p.xui3.GetClient(email); ex != nil {
			terr = p.xui3.UpdateTrojanClient(p.cfg.Trojan.InboundID, email, tc)
		} else {
			terr = p.xui3.AddTrojanClient(p.cfg.Trojan.InboundID, tc)
		}
		if terr != nil {
			log.Printf("provision: s3 trojan %q: %v", login, terr)
		} else {
			cust.Trojan = &subgen.TrojanCreds{
				Server: p.cfg.Trojan.Server, Port: p.cfg.Trojan.Port,
				Password: pass, SNI: p.cfg.Trojan.SNI, Insecure: p.cfg.Trojan.Insecure,
			}
		}
	}
}

// BackfillS3 gives the 3rd node (S3 VLESS-Reality + Trojan) to every stored customer that
// lacks it, WITHOUT touching any other server/protocol — it only adds clients to S3's panel,
// so existing customers' live S1/S2 connections are never disturbed. Best-effort per customer,
// idempotent. Returns how many customers gained at least one S3 protocol. Run once after S3 is
// wired so the existing base gets the new server + Trojan with zero blip.
func (p *Provisioner) BackfillS3() (int, error) {
	if p.xui3 == nil || p.cfg.VLESS3.Server == "" {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.st.List() {
		needV := c.VLESS3 == nil
		needT := p.cfg.Trojan.Server != "" && c.Trojan == nil
		if !needV && !needT {
			continue
		}
		hadV, hadT := c.VLESS3 != nil, c.Trojan != nil
		p.provisionS3(c, c.Login)
		if (c.VLESS3 != nil && !hadV) || (c.Trojan != nil && !hadT) {
			if err := p.st.Put(c); err != nil {
				return n, fmt.Errorf("provision: backfill s3 store %q: %w", c.Login, err)
			}
			n++
		}
	}
	return n, nil
}

// MigrateAnyTLSEndpoint repoints every stored customer's AnyTLS credential to the CURRENT
// configured endpoint (p.cfg.AnyTLS) WITHOUT changing their password — this is the
// S1:8444 → S2:8443 cutover. It rewrites only Server/Port/SNI/Insecure on customers still
// pointing elsewhere, persists, then re-syncs the AnyTLS server ONCE. Idempotent (a customer
// already on the target endpoint is skipped) and never touches any other protocol. Returns
// how many customers were repointed.
func (p *Provisioner) MigrateAnyTLSEndpoint() (int, error) {
	if p.cfg.AnyTLS.Server == "" {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.st.List() {
		if c.AnyTLS == nil {
			continue
		}
		if c.AnyTLS.Server == p.cfg.AnyTLS.Server && c.AnyTLS.Port == p.cfg.AnyTLS.Port {
			continue // already on the target endpoint
		}
		c.AnyTLS.Server = p.cfg.AnyTLS.Server
		c.AnyTLS.Port = p.cfg.AnyTLS.Port
		c.AnyTLS.SNI = p.cfg.AnyTLS.SNI
		c.AnyTLS.Insecure = p.cfg.AnyTLS.Insecure
		if err := p.st.Put(c); err != nil {
			return n, fmt.Errorf("provision: migrate anytls store %q: %w", c.Login, err)
		}
		n++
	}
	if n > 0 {
		if err := p.syncAnyTLS(); err != nil {
			return n, fmt.Errorf("provision: migrate anytls sync: %w", err)
		}
	}
	return n, nil
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
