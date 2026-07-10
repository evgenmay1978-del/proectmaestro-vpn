package api

// panel.go — the password-protected web admin panel (the "полноценная веб-панель"). It is a
// SEPARATE auth surface from the localhost-only bearer /admin/* API: the panel is meant to be
// reachable through the public nginx front, so it is guarded by a bcrypt password + a random
// session cookie (HttpOnly/Secure/SameSite=Strict) + a per-session CSRF token on writes + a
// login rate-limiter. It reuses the same store/provisioner/olcconf the admin API uses. Served
// under cfg.PanelPath (a secret, non-obvious prefix set in env), which nginx proxies through.
//
// Enabled only when BOTH PanelPath and PanelPasswordHash are set (else the routes aren't
// registered at all — the panel is off and invisible).

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	panelCookie     = "mp_session"
	panelSessionTTL = 12 * time.Hour
	panelLockFails  = 8                // failed logins (per IP) before a cooldown
	panelLockFor    = 15 * time.Minute // cooldown once locked
	panelFailWindow = 15 * time.Minute // rolling window the fail count decays over
	panelFailsCap   = 4096             // hard cap on distinct fail buckets (anti memory-DoS)
)

type panelSess struct {
	csrf   string
	expiry time.Time
}
type panelFails struct {
	n     int
	first time.Time
	until time.Time // locked until (zero = not locked)
}

// panelState holds the panel's in-memory session + rate-limit tables plus the ACTIVE password
// hash (mutable at runtime via the change-password endpoint, persisted to pwFile). In-memory
// session/rate tables are fine: one operator, and a restart just forces a re-login.
type panelState struct {
	mu     sync.Mutex
	sess   map[string]panelSess
	fails  map[string]*panelFails
	pwHash string // active bcrypt hash (from pwFile if set, else the env bootstrap hash)
	pwFile string // where a changed password is persisted
}

func newPanelState() *panelState {
	return &panelState{sess: map[string]panelSess{}, fails: map[string]*panelFails{}}
}

// panelClientIP returns the TRUE peer IP for rate-limiting. It trusts ONLY X-Real-IP, which the
// nginx panel location sets to $remote_addr (the real TCP peer) and which nginx OVERWRITES
// regardless of any client-sent value — so, unlike X-Forwarded-For (which nginx APPENDS to a
// client-controlled chain), it cannot be spoofed to forge another IP into the limiter bucket.
func panelClientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func panelToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sortedLogins returns a sorted copy of the olcRTC allowlist (for the panel's olcRTC view).
func sortedLogins(logins []string) []string {
	out := append([]string(nil), logins...)
	sort.Strings(out)
	return out
}

// deviceLimitFor returns the per-login device cap (0 = unlimited / unknown), guarding a nil prov.
func (s *Server) deviceLimitFor(login string) int {
	if s.prov == nil {
		return 0
	}
	return s.prov.DeviceLimitFor(login)
}

// (clientIP is defined in trial.go — it reads X-Forwarded-For, which the nginx panel location sets.)

// locked reports whether this IP is in login cooldown (and prunes stale windows).
func (ps *panelState) locked(ip string, now time.Time) bool {
	f := ps.fails[ip]
	if f == nil {
		return false
	}
	if !f.until.IsZero() && now.Before(f.until) {
		return true
	}
	if now.Sub(f.first) > panelFailWindow {
		delete(ps.fails, ip) // window elapsed, forget
	}
	return false
}

func (ps *panelState) recordFail(ip string, now time.Time) {
	f := ps.fails[ip]
	if f == nil || now.Sub(f.first) > panelFailWindow {
		// New bucket. If the table is somehow full (e.g. a distributed flood), sweep stale
		// entries first; if it's still at the cap, skip recording — never grow unbounded.
		if len(ps.fails) >= panelFailsCap {
			ps.sweep(now)
			if len(ps.fails) >= panelFailsCap {
				return
			}
		}
		f = &panelFails{first: now}
		ps.fails[ip] = f
	}
	f.n++
	if f.n >= panelLockFails {
		f.until = now.Add(panelLockFor)
	}
}

// sweep drops expired sessions and elapsed/unlocked fail buckets. Called periodically by the
// janitor and opportunistically when the fails table hits its cap. Caller need NOT hold the
// lock only when invoked from the janitor (which locks); recordFail already holds it.
func (ps *panelState) sweep(now time.Time) {
	for id, s := range ps.sess {
		if now.After(s.expiry) {
			delete(ps.sess, id)
		}
	}
	for ip, f := range ps.fails {
		if (f.until.IsZero() || now.After(f.until)) && now.Sub(f.first) > panelFailWindow {
			delete(ps.fails, ip)
		}
	}
}

// startJanitor periodically sweeps expired sessions + stale fail buckets so neither map grows
// without bound (defence in depth beyond the recordFail cap).
func (ps *panelState) startJanitor() {
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for range t.C {
			ps.mu.Lock()
			ps.sweep(time.Now())
			ps.mu.Unlock()
		}
	}()
}

// newSession mints a session + csrf token and returns (sessionID, csrf).
func (ps *panelState) newSession(now time.Time) (string, string) {
	id, csrf := panelToken(), panelToken()
	ps.sess[id] = panelSess{csrf: csrf, expiry: now.Add(panelSessionTTL)}
	return id, csrf
}

// validSession returns the session (and true) if the request carries a live session cookie.
func (ps *panelState) validSession(r *http.Request, now time.Time) (panelSess, bool) {
	c, err := r.Cookie(panelCookie)
	if err != nil || c.Value == "" {
		return panelSess{}, false
	}
	s, ok := ps.sess[c.Value]
	if !ok || now.After(s.expiry) {
		if ok {
			delete(ps.sess, c.Value)
		}
		return panelSess{}, false
	}
	return s, true
}

// registerPanel wires the panel routes under cfg.PanelPath (which must start and end with '/').
func (s *Server) registerPanel(mux *http.ServeMux) {
	p := s.cfg.PanelPath
	if p == "" || s.cfg.PanelPasswordHash == "" {
		return // panel disabled
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	s.panel = newPanelState()
	// Active password: a runtime-changed password persists to PanelPWFile and takes precedence
	// over the env bootstrap hash; if the file is absent/unreadable, fall back to the env hash.
	s.panel.pwFile = s.cfg.PanelPWFile
	s.panel.pwHash = s.cfg.PanelPasswordHash
	if s.cfg.PanelPWFile != "" {
		if b, err := os.ReadFile(s.cfg.PanelPWFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			s.panel.pwHash = strings.TrimSpace(string(b))
		}
	}
	s.panel.startJanitor()
	mux.HandleFunc(p, s.panelApp)                             // GET the SPA shell
	mux.HandleFunc(p+"api/login", s.panelLogin)               // POST {password}
	mux.HandleFunc(p+"api/logout", s.panelLogout)             // POST
	mux.HandleFunc(p+"api/me", s.panelMe)                     // GET
	mux.HandleFunc(p+"api/password", s.panelPassword)         // POST {current,new}
	mux.HandleFunc(p+"api/customers", s.panelCustomers)       // GET
	mux.HandleFunc(p+"api/customer", s.panelCustomer)         // GET ?login=
	mux.HandleFunc(p+"api/stats", s.panelStats)               // GET
	mux.HandleFunc(p+"api/action", s.panelActionH)            // POST
	mux.HandleFunc(p+"api/olcrtc", s.panelOlcrtc)             // GET
	mux.HandleFunc(p+"api/olcrtc/room", s.panelOlcRoom)       // POST {login,room,provider}
	mux.HandleFunc(p+"api/olcrtc/login", s.panelOlcLogin)     // POST {login,action:add|remove}
	mux.HandleFunc(p+"api/olcrtc/wbtoken", s.panelOlcWBToken) // POST {token} — set wbstream account token
	mux.HandleFunc(p+"api/olcrtc/wbroom", s.panelOlcWBRoom)   // POST {login} — create+assign a fresh wbstream room
}

// panelErrLog returns a GENERIC message to the browser (an internet-facing surface) while
// logging the real error server-side, so provisioner/xui/SSH error strings can't leak internal
// hosts, ports, or paths to a client.
func panelErrLog(w http.ResponseWriter, code int, public, context string, err error) {
	log.Printf("panel: %s: %v", context, err)
	http.Error(w, public, code)
}

// panelApp serves the SPA shell (no data, so no auth needed to GET it; the JS calls /api/me).
func (s *Server) panelApp(w http.ResponseWriter, r *http.Request) {
	// Only the exact panel root serves the app; unknown sub-paths under it 404 (avoid catch-all).
	if strings.TrimSuffix(r.URL.Path, "/") != strings.TrimSuffix(s.cfg.PanelPath, "/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte(panelHTML))
}

func (s *Server) panelLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	ip := panelClientIP(r) // the UNSPOOFABLE peer IP (X-Real-IP), never the client-set XFF chain
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.panel.mu.Lock()
	if s.panel.locked(ip, now) {
		s.panel.mu.Unlock()
		http.Error(w, "too many attempts — try later", http.StatusTooManyRequests)
		return
	}
	hash := s.panel.pwHash
	s.panel.mu.Unlock()

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		s.panel.mu.Lock()
		s.panel.recordFail(ip, now)
		s.panel.mu.Unlock()
		http.Error(w, "wrong password", http.StatusUnauthorized)
		return
	}
	s.panel.mu.Lock()
	delete(s.panel.fails, ip)
	id, csrf := s.panel.newSession(now)
	s.panel.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name: panelCookie, Value: id, Path: s.cfg.PanelPath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		Expires: now.Add(panelSessionTTL),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "csrf": csrf})
}

func (s *Server) panelLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(panelCookie); err == nil {
		s.panel.mu.Lock()
		delete(s.panel.sess, c.Value)
		s.panel.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: panelCookie, Value: "", Path: s.cfg.PanelPath, MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) panelMe(w http.ResponseWriter, r *http.Request) {
	s.panel.mu.Lock()
	sess, ok := s.panel.validSession(r, time.Now())
	s.panel.mu.Unlock()
	// Return the session's CSRF token so a page that reloads (or returns to a still-valid session
	// WITHOUT a fresh login) can recover it — otherwise every write (change-password, room assign,
	// customer actions) would fail with "bad csrf". Safe: only the authenticated same-origin session
	// (which sent the HttpOnly cookie) can read this response; SameSite=Strict blocks cross-origin.
	out := map[string]any{"logged_in": ok}
	if ok {
		out["csrf"] = sess.csrf
	}
	writeJSON(w, http.StatusOK, out)
}

// panelPassword changes the panel login password at runtime. Requires a live session + CSRF +
// the CURRENT password (so a hijacked-but-idle session alone can't lock the owner out). The new
// bcrypt hash is persisted to pwFile (survives restart) and swapped in-memory. Other sessions
// are invalidated (only the caller's stays valid).
func (s *Server) panelPassword(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.New) < 8 {
		http.Error(w, "new password too short (min 8)", http.StatusBadRequest)
		return
	}
	s.panel.mu.Lock()
	cur := s.panel.pwHash
	s.panel.mu.Unlock()
	if bcrypt.CompareHashAndPassword([]byte(cur), []byte(req.Current)) != nil {
		// 403, NOT 401: the session IS valid (the operator is logged in), only the typed CURRENT
		// password is wrong. A 401 here makes the SPA's api() treat it as an expired session and
		// bounce to the login screen — so a mistyped current password looked like "can't change it".
		http.Error(w, "current password is wrong", http.StatusForbidden)
		return
	}
	nh, err := bcrypt.GenerateFromPassword([]byte(req.New), 12)
	if err != nil {
		panelErrLog(w, http.StatusInternalServerError, "could not set password", "bcrypt", err)
		return
	}
	if s.panel.pwFile != "" {
		// Atomic write (temp + rename) with an explicit, non-aliased byte slice so the persisted
		// hash can never be a partial/garbled value.
		tmp := s.panel.pwFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(string(nh)+"\n"), 0o600); err != nil {
			panelErrLog(w, http.StatusInternalServerError, "could not persist password", "write pwfile", err)
			return
		}
		if err := os.Rename(tmp, s.panel.pwFile); err != nil {
			panelErrLog(w, http.StatusInternalServerError, "could not persist password", "rename pwfile", err)
			return
		}
	}
	// Keep the caller's session; drop all others so a changed password takes effect everywhere.
	keep, _ := r.Cookie(panelCookie)
	s.panel.mu.Lock()
	s.panel.pwHash = string(nh)
	for id := range s.panel.sess {
		if keep == nil || id != keep.Value {
			delete(s.panel.sess, id)
		}
	}
	s.panel.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// panelGuard authorizes a panel API request. GET needs only a live session; state-changing
// methods additionally require the per-session CSRF token in X-CSRF (defence in depth on top
// of the SameSite=Strict cookie). Returns false (and writes the error) when unauthorized.
func (s *Server) panelGuard(w http.ResponseWriter, r *http.Request, write bool) bool {
	s.panel.mu.Lock()
	sess, ok := s.panel.validSession(r, time.Now())
	s.panel.mu.Unlock()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if write {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return false
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF")), []byte(sess.csrf)) != 1 {
			http.Error(w, "bad csrf", http.StatusForbidden)
			return false
		}
	}
	return true
}

type panelCustomerRow struct {
	Login     string    `json:"login"`
	Expires   time.Time `json:"expires"`
	DaysLeft  int       `json:"days_left"`
	Active    bool      `json:"active"`
	Disabled  bool      `json:"disabled"`
	Devices   int       `json:"devices"`
	Protocols []string  `json:"protocols"`
	SubURL    string    `json:"sub_url"`
	// LastSeen is the most recent time any of the customer's devices polled /sub (a proxy for
	// activity — the nodes don't expose realtime "online"). Zero when never seen.
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

func (s *Server) rowFor(c *store.Customer) panelCustomerRow {
	d := time.Until(c.Expires)
	days := int(d / (24 * time.Hour))
	if d > 0 && d%(24*time.Hour) != 0 {
		days++
	}
	if days < 0 {
		days = 0
	}
	protos := []string{}
	if c.VLESS != nil {
		protos = append(protos, "vless")
	}
	if c.Hy2 != nil {
		protos = append(protos, "hysteria2")
	}
	if c.Naive != nil {
		protos = append(protos, "naive")
	}
	if c.AnyTLS != nil {
		protos = append(protos, "anytls")
	}
	if c.VLESS3 != nil {
		protos = append(protos, "vless-s3")
	}
	if c.WG != nil {
		protos = append(protos, "awg")
	}
	var lastSeen *time.Time
	for _, t := range c.Devices {
		if lastSeen == nil || t.After(*lastSeen) {
			tt := t
			lastSeen = &tt
		}
	}
	return panelCustomerRow{
		Login: c.Login, Expires: c.Expires, DaysLeft: days,
		Active: c.Active(), Disabled: c.Disabled, Devices: len(c.Devices),
		Protocols: protos, SubURL: s.cfg.SubBaseURL + "/sub/" + c.SubToken,
		LastSeen: lastSeen,
	}
}

func (s *Server) panelCustomers(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, false) {
		return
	}
	list := s.st.List()
	rows := make([]panelCustomerRow, 0, len(list))
	for _, c := range list {
		rows = append(rows, s.rowFor(c))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Expires.Before(rows[j].Expires) })
	writeJSON(w, http.StatusOK, map[string]any{"customers": rows})
}

func (s *Server) panelCustomer(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, false) {
		return
	}
	c, err := s.st.ByLogin(r.URL.Query().Get("login"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such customer", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	row := s.rowFor(c)
	devs := map[string]string{}
	for id, t := range c.Devices {
		devs[id] = t.Format(time.RFC3339)
	}
	var traffic int64
	if s.prov != nil {
		traffic = s.prov.TrafficFor(c.Login) // VLESS total bytes (best-effort; 0 if node unreachable)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"customer": row, "device_ids": devs,
		"device_limit": s.deviceLimitFor(c.Login), "traffic_bytes": traffic,
	})
}

func (s *Server) panelStats(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, false) {
		return
	}
	list := s.st.List()
	now := time.Now()
	var active, expired, soon, disabled, devices int
	for _, c := range list {
		devices += len(c.Devices)
		if c.Disabled {
			disabled++
		}
		if c.Active() {
			active++
			if c.Expires.Before(now.Add(7 * 24 * time.Hour)) {
				soon++
			}
		} else {
			expired++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": len(list), "active": active, "expired": expired,
		"expiring_7d": soon, "disabled": disabled, "devices": devices,
	})
}

// panelActionH dispatches a state-changing action. All go through the provisioner/store so the
// same fan-out + persistence the bearer admin API uses is applied.
func (s *Server) panelActionH(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	var req struct {
		Action  string `json:"action"` // extend|renew|set_expiry|reset_devices|disable|enable|provision|delete|delete_expired
		Login   string `json:"login"`
		Days    int    `json:"days"`
		Expires string `json:"expires"` // RFC3339 for set_expiry
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// delete_expired operates over the whole store (no single login) — handle it up front.
	if req.Action == "delete_expired" {
		n := 0
		for _, c := range s.st.List() {
			if !c.Active() && !c.Disabled { // expired (not merely disabled)
				if err := s.prov.DeleteCustomer(c.Login); err != nil {
					log.Printf("panel: delete_expired %q: %v", c.Login, err)
					continue
				}
				n++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
		return
	}
	if req.Login == "" {
		http.Error(w, "login required", http.StatusBadRequest)
		return
	}
	if req.Action == "delete" {
		if err := s.prov.DeleteCustomer(req.Login); err != nil {
			panelErrLog(w, http.StatusBadGateway, "delete failed", "delete "+req.Login, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": req.Login})
		return
	}
	var (
		c   *store.Customer
		err error
	)
	switch req.Action {
	case "provision":
		if req.Days <= 0 {
			http.Error(w, "positive days required", http.StatusBadRequest)
			return
		}
		c, err = s.prov.Provision(req.Login, time.Duration(req.Days)*24*time.Hour)
	case "extend", "renew":
		if req.Days <= 0 {
			http.Error(w, "positive days required", http.StatusBadRequest)
			return
		}
		if _, e := s.st.ByLogin(req.Login); errors.Is(e, store.ErrNotFound) && req.Action == "renew" {
			if _, ae := s.prov.ActivateExisting(req.Login); ae != nil {
				panelErrLog(w, http.StatusNotFound, "no such customer", "activate "+req.Login, ae)
				return
			}
		}
		c, err = s.prov.Extend(req.Login, time.Duration(req.Days)*24*time.Hour)
	case "set_expiry":
		t, pe := time.Parse(time.RFC3339, req.Expires)
		if pe != nil {
			http.Error(w, "expires must be RFC3339", http.StatusBadRequest)
			return
		}
		if _, e := s.st.ByLogin(req.Login); errors.Is(e, store.ErrNotFound) {
			if _, ae := s.prov.ActivateExisting(req.Login); ae != nil {
				panelErrLog(w, http.StatusNotFound, "no such customer", "activate "+req.Login, ae)
				return
			}
		}
		c, err = s.prov.SetExpiry(req.Login, t)
	case "reset_devices":
		c, err = s.st.ResetDevices(req.Login)
	case "disable":
		c, err = s.st.SetDisabled(req.Login, true)
	case "enable":
		c, err = s.st.SetDisabled(req.Login, false)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such customer", http.StatusNotFound)
		return
	}
	if err != nil {
		// Generic message to the browser; the real (node/SSH) error is logged server-side so it
		// can't leak internal hosts/ports/paths over the public panel.
		panelErrLog(w, http.StatusBadGateway, "action failed on a node", req.Action+" "+req.Login, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "customer": s.rowFor(c)})
}

func (s *Server) panelOlcrtc(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, false) {
		return
	}
	if s.olc == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	cfg := s.olc.Get()
	health := s.readOlcHealth() // per-login S3 exit liveness (from the health probe); may be empty
	rooms := map[string]any{}
	for lg, rk := range cfg.Rooms {
		rooms[lg] = map[string]any{"room": rk.Room, "has_key": rk.Key != "", "provider": cfg.ProviderFor(lg)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": cfg.Enabled, "provider": cfg.Provider, "transport": cfg.Transport,
		"global_room": cfg.Room, "rooms": rooms, "logins": sortedLogins(cfg.Logins),
		"health": health.Exits, "health_checked": health.Checked,
		"wb_token_set": s.wbTokenSet(), // wbstream account token present on this node?
	})
}

// olcHealth mirrors /var/lib/maestro/olcrtc-health.json (written by the S3 exit-liveness probe).
type olcHealth struct {
	Checked int64                     `json:"checked"`
	Exits   map[string]map[string]any `json:"exits"`
}

// readOlcHealth loads the latest exit-liveness snapshot; a missing/unreadable file yields a zero
// value (no health shown) rather than a false "healthy".
func (s *Server) readOlcHealth() olcHealth {
	var h olcHealth
	if s.cfg.OlcHealthFile == "" {
		return h
	}
	if b, err := os.ReadFile(s.cfg.OlcHealthFile); err == nil {
		_ = json.Unmarshal(b, &h)
	}
	return h
}

func (s *Server) panelOlcRoom(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	if s.olc == nil {
		http.Error(w, "olcRTC disabled", http.StatusBadRequest)
		return
	}
	var req struct {
		Login    string `json:"login"`
		Room     string `json:"room"`
		Provider string `json:"provider"` // "wbstream" | "max" | "telemost"/"" (default)
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Validate the room per carrier + the (optional) login token, so a typo or junk value can't
	// poison the persisted config / the S3 srv. wbstream joins by a BARE room id (UUID); every
	// other carrier uses an http(s) URL.
	if req.Provider == "wbstream" {
		if !olcRoomIDRe.MatchString(req.Room) {
			http.Error(w, "wbstream room must be a bare id (letters/digits/._~-, 8-128 chars)", http.StatusBadRequest)
			return
		}
	} else if !strings.HasPrefix(req.Room, "https://") && !strings.HasPrefix(req.Room, "http://") {
		http.Error(w, "room must be an http(s) URL", http.StatusBadRequest)
		return
	}
	if req.Provider != "" && req.Provider != "wbstream" && req.Provider != "telemost" && req.Provider != "max" {
		http.Error(w, "provider must be wbstream, telemost or max", http.StatusBadRequest)
		return
	}
	// MAX is staged but not yet functional (the olcrtc binary at OLCRTC_REF has no "max" provider).
	// Refuse to assign it so an operator can't silently break a login. See docs/olcrtc-max-carrier.md.
	if req.Provider == "max" && !olcMaxCarrierReady {
		http.Error(w, olcMaxNotReadyMsg, http.StatusBadRequest)
		return
	}
	if req.Login != "" && !claimCodeRe.MatchString(req.Login) {
		http.Error(w, "invalid login", http.StatusBadRequest)
		return
	}
	// Run the SAME script the Telegram bridge uses so a UI room-swap does the FULL work: mint the
	// login's key + write the panel config + bring up (or restart) that login's S3 exit. Falls
	// back to a config-only write if the script isn't configured (no S3 exit, matches the old
	// behaviour). Inputs are already validated (URL + login charset) so there's no shell injection.
	if s.cfg.OlcrtcRoomScript == "" {
		if err := s.olc.SetRoomProviderFor(req.Login, req.Room, "", req.Provider); err != nil {
			panelErrLog(w, http.StatusInternalServerError, "could not set room", "olcrtc room", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	argv := []string{s.cfg.OlcrtcRoomScript}
	if req.Login != "" {
		argv = append(argv, req.Login)
	}
	argv = append(argv, req.Room)
	if req.Provider != "" {
		argv = append(argv, req.Provider) // ops script: <login> <room> [provider]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/sh", argv...).CombinedOutput()
	if err != nil {
		panelErrLog(w, http.StatusBadGateway, "room assign failed on S3 — try again", "olcrtc room script", errors.New(strings.TrimSpace(string(out))))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// panelOlcLogin adds or removes a login from the olcRTC ALLOWLIST (who gets olcRTC creds) — the
// "add a client to olcRTC" control. Adding only grants access (the login then gets the GLOBAL
// room until a dedicated room is assigned via panelOlcRoom). Removing also drops any dedicated
// room. It does NOT tear down an S3 exit — harmless, since an unlisted login gets no creds.
func (s *Server) panelOlcLogin(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	if s.olc == nil {
		http.Error(w, "olcRTC disabled", http.StatusBadRequest)
		return
	}
	var req struct {
		Login  string `json:"login"`
		Action string `json:"action"` // add | remove
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !claimCodeRe.MatchString(req.Login) {
		http.Error(w, "invalid login", http.StatusBadRequest)
		return
	}
	var err error
	switch req.Action {
	case "add":
		err = s.olc.AddLogin(req.Login)
	case "remove":
		err = s.olc.RemoveLogin(req.Login)
	default:
		http.Error(w, "action must be add or remove", http.StatusBadRequest)
		return
	}
	if err != nil {
		panelErrLog(w, http.StatusInternalServerError, "could not update allowlist", "olcrtc login", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// wbToken returns the wbstream ACCOUNT token stored on this node (trimmed), or "" if unset. It is
// the WB session JWT the panel uses to create rooms; the ops room script injects it into each
// wbstream login's S3 srv config so the exit joins AS the account (keeping the room alive).
func (s *Server) wbToken() string {
	if s.cfg.OlcWBTokenFile == "" {
		return ""
	}
	b, err := os.ReadFile(s.cfg.OlcWBTokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// wbTokenSet reports whether a wbstream account token is present (shown in the panel; never the value).
func (s *Server) wbTokenSet() bool { return s.wbToken() != "" }

// panelOlcWBToken sets/updates the wbstream ACCOUNT token (a WB session JWT). Stored root-only on
// this node (0600); the ops room script injects it into each wbstream login's S3 srv config. The
// value is NEVER echoed back. POST {token}.
func (s *Server) panelOlcWBToken(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	if s.cfg.OlcWBTokenFile == "" {
		http.Error(w, "wbtoken storage not configured", http.StatusBadRequest)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tok := strings.TrimSpace(req.Token)
	// A WB token is a JWT (three dot-separated base64url segments); reject junk before storing.
	if strings.Count(tok, ".") != 2 || len(tok) < 40 || strings.ContainsAny(tok, " \t\r\n\"'") {
		http.Error(w, "token must be a bare JWT (header.payload.signature)", http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(s.cfg.OlcWBTokenFile, []byte(tok+"\n"), 0o600); err != nil {
		panelErrLog(w, http.StatusInternalServerError, "could not store token", "wbtoken write", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// createWBStreamRoom mints a FRESH wbstream room using the stored account token and returns its
// bare room id. WB — unlike Yandex Telemost — HAS a room-creation API, so the panel can regenerate
// a room in one click (the "как в яндексе" ask). Bounded + LimitReader so a slow/huge reply can't hang.
func (s *Server) createWBStreamRoom(ctx context.Context) (string, error) {
	tok := s.wbToken()
	if tok == "" {
		return "", errors.New("no wbstream token set")
	}
	body := strings.NewReader(`{"roomType":"ROOM_TYPE_ALL_ON_SCREEN","roomPrivacy":"ROOM_PRIVACY_FREE"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://stream.wb.ru/api-room/api/v2/room", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wbstream create room: HTTP %d", resp.StatusCode)
	}
	var out struct {
		RoomID string `json:"roomId"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.RoomID == "" {
		return "", errors.New("wbstream create room: no roomId in reply")
	}
	return out.RoomID, nil
}

// panelOlcWBRoom creates a fresh wbstream room via the account token and assigns it to a login —
// the one-click "новая комната" (like a new Yandex meeting). POST {login}. Runs the SAME room
// script as a manual set (mint key + panel config + bring up the login's S3 wbstream exit).
func (s *Server) panelOlcWBRoom(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	if s.olc == nil {
		http.Error(w, "olcRTC disabled", http.StatusBadRequest)
		return
	}
	var req struct {
		Login string `json:"login"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !claimCodeRe.MatchString(req.Login) {
		http.Error(w, "invalid login", http.StatusBadRequest)
		return
	}
	cctx, ccancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer ccancel()
	roomID, err := s.createWBStreamRoom(cctx)
	if err != nil {
		panelErrLog(w, http.StatusBadGateway, "could not create a wbstream room — check the token", "wbstream create", err)
		return
	}
	if s.cfg.OlcrtcRoomScript == "" {
		if err := s.olc.SetRoomProviderFor(req.Login, roomID, "", "wbstream"); err != nil {
			panelErrLog(w, http.StatusInternalServerError, "could not set room", "wbstream assign", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "room": roomID})
		return
	}
	sctx, scancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer scancel()
	out, err := exec.CommandContext(sctx, "/bin/sh", s.cfg.OlcrtcRoomScript, req.Login, roomID, "wbstream").CombinedOutput()
	if err != nil {
		panelErrLog(w, http.StatusBadGateway, "room assign failed on S3 — try again", "wbstream room script", errors.New(strings.TrimSpace(string(out))))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "room": roomID})
}
