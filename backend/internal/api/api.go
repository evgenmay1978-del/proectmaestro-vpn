// Package api serves the customer-facing subscription endpoint: the app polls
// GET /sub/<token> and receives its current sing-box configuration. Because the
// config is generated live from the store, a key rotation (or a renewal) is
// picked up on the next poll with no action by the customer.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/promo"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/telegram"
)

// claimCodeRe bounds a /claim code (a login) to a safe charset before it can
// reach the unauthenticated → server-2 SSH path (mirrors provision.ValidLogin).
var claimCodeRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// Provisioner is the subset of the provision package the admin API drives.
type Provisioner interface {
	Provision(login string, dur time.Duration) (*store.Customer, error)
	Extend(login string, dur time.Duration) (*store.Customer, error)
	SetExpiry(login string, t time.Time) (*store.Customer, error)
	ActivateExisting(login string) (*store.Customer, error)
	// BackfillAnyTLS gives AnyTLS to existing customers that lack it, re-syncing only the
	// server-2 AnyTLS server (no hy2/naive restart). Returns the count backfilled.
	BackfillAnyTLS() (int, error)
	// BackfillS3 gives the 3rd node (S3 VLESS-Reality) to existing customers that
	// lack it, adding only S3 panel clients (no other server touched). Returns the count.
	BackfillS3() (int, error)
	// BulkActivateExisting imports many existing panel logins into the unified store with a
	// SINGLE server-2 sync at the end (no per-customer hy2/anytls restart). Returns
	// (imported, failed-logins, err).
	BulkActivateExisting(logins []string) (int, []string, error)
	// MigrateAnyTLSEndpoint repoints existing customers' AnyTLS creds to the configured
	// endpoint (the S1:8444 → S2:8443 cutover), password-preserving. Returns the count moved.
	MigrateAnyTLSEndpoint() (int, error)
	// DeviceLimitFor returns the per-login device cap (0 = unlimited) so the sub
	// endpoint enforces the same cap + exemption as 3x-ui's limitIp.
	DeviceLimitFor(login string) int
	// DeleteCustomer removes a customer from the store and deletes their VLESS clients on the
	// nodes (best-effort). Used by the web panel to purge inactive users.
	DeleteCustomer(login string) error
	// TrafficFor returns the customer's total used traffic in bytes (VLESS; 0 on error).
	TrafficFor(login string) int64
}

// deviceIDRe bounds the app's per-install device id (a random UUID the app generates and
// stores locally). Anything outside this charset is treated as "no device id" → no cap
// enforcement, so a malformed value can never block a customer or poison the device set.
var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Config tunes the api server.
type Config struct {
	AdminToken string // bearer token guarding /admin/*; empty disables admin API
	// PanelPath + PanelPasswordHash enable the password-protected WEB admin panel (served under
	// PanelPath, a secret non-obvious prefix the public nginx front proxies). Both must be set
	// or the panel is off. PanelPasswordHash is the BOOTSTRAP bcrypt hash (never the plaintext);
	// PanelPWFile, if set, holds a runtime-changed hash that overrides it and persists.
	PanelPath         string
	PanelPasswordHash string
	PanelPWFile       string
	// OlcrtcRoomScript is the path to ops/olcrtc-room.sh (the SAME one the Telegram bridge runs):
	// the panel execs it so an olcRTC room assigned from the UI does the FULL work (mint key +
	// panel config + bring up the per-login S3 exit), not just a config-only write. Empty →
	// the panel falls back to a config-only room set (no S3 exit).
	OlcrtcRoomScript string
	SubBaseURL       string // public base for building sub URLs, e.g. https://wapmixx.ru:8910
	SBPPhone         string // СБП phone shown to the customer for in-app purchase
	PayURL           string // pay link (T-Bank «Сбор денег» / СБП) shown as a scannable QR — cross-bank, no acquiring; empty → fall back to the phone QR
	TGBotToken       string // bot token for owner payment notifications (send-only, no poll)
	TGAdminID        string // owner's Telegram chat id
	UpdateDir        string // dir holding the panel-hosted OTA channel (update.json + *.apk); empty disables /update/
	ReportDir        string // dir for the fleet crash/diagnostic log (JSON-lines per day); empty disables /report
	// EnforceDeviceLimit gates the per-account 5-device cap at /sub + /claim. A kill
	// switch (env MAESTRO_DEVICE_LIMIT=off) so the cap can be disabled live without a
	// redeploy if it ever misbehaves in prod.
	EnforceDeviceLimit bool
	// TrialDays is the length of the in-app free trial (POST /trial); default 2.
	TrialDays int
	// TrialIPQuota caps trials started per /24 subnet per 24h (soft, lenient — RU CGNAT);
	// 0 disables the velocity check.
	TrialIPQuota int
	// OLC is the global olcRTC config (room/key/etc.), hot-swappable at runtime. nil → olcRTC
	// disabled entirely. Emitted in /sub + /info only for the MAESTRO_OLC_LOGINS allowlist.
	OLC *olcconf.Store
}

// Server wires the HTTP handlers to the store and (optionally) the provisioner.
type Server struct {
	st       *store.Store
	prov     Provisioner
	orders   *order.Store
	promos   *promo.Store
	trialVel *trialVelocity
	tg       *telegram.Client
	olc      *olcconf.Store
	panel    *panelState // web admin panel session/rate-limit state (nil until registerPanel)
	cfg      Config
}

// New returns an api server. prov/orders/promos may be nil to disable those routes.
func New(st *store.Store, prov Provisioner, orders *order.Store, promos *promo.Store, cfg Config) *Server {
	return &Server{
		st: st, prov: prov, orders: orders, promos: promos,
		trialVel: newTrialVelocity(cfg.TrialIPQuota, 24*time.Hour),
		tg:       telegram.New(cfg.TGBotToken), olc: cfg.OLC, cfg: cfg,
	}
}

// Handler returns the configured http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/sub/", s.handleSub)
	mux.HandleFunc("/claim", s.handleClaim)
	if s.cfg.UpdateDir != "" {
		// Panel-hosted OTA update channel — the RU-reachable update source the app
		// prefers over GitHub (throttled from Russian ISPs). Same host:port the
		// device already polls for /sub every 15 min.
		mux.HandleFunc("/update/", s.handleUpdate)
	}
	if s.cfg.ReportDir != "" {
		// Public crash/diagnostic sink — devices POST here after a local crash/OOM so
		// the fleet's real failures land on S1 (read-only intake; never blocks anything).
		mux.HandleFunc("/report", s.handleReport)
	}
	if s.orders != nil {
		mux.HandleFunc("/order/tariffs", s.handleTariffs)
		mux.HandleFunc("/order/paid-claim", s.handleOrderPaidClaim)
		mux.HandleFunc("/order", s.handleOrderCreate)
		mux.HandleFunc("/order/", s.handleOrderGet)
	}
	// In-app free trial: needs the ledger (promos) + the provisioner to grant the account.
	if s.promos != nil && s.prov != nil {
		mux.HandleFunc("/trial", s.handleTrial)
	}
	if s.prov != nil && s.cfg.AdminToken != "" {
		mux.HandleFunc("/admin/provision", s.adminAuth(s.handleProvision))
		mux.HandleFunc("/admin/extend", s.adminAuth(s.handleExtend))
		mux.HandleFunc("/admin/renew", s.adminAuth(s.handleRenew))
		mux.HandleFunc("/admin/set-expiry", s.adminAuth(s.handleSetExpiry))
		mux.HandleFunc("/admin/reset-devices", s.adminAuth(s.handleResetDevices))
		mux.HandleFunc("/admin/customer", s.adminAuth(s.handleCustomer))
		mux.HandleFunc("/admin/backfill-anytls", s.adminAuth(s.handleBackfillAnyTLS))
		mux.HandleFunc("/admin/backfill-s3", s.adminAuth(s.handleBackfillS3))
		mux.HandleFunc("/admin/bulk-import", s.adminAuth(s.handleBulkImport))
		mux.HandleFunc("/admin/migrate-anytls-s2", s.adminAuth(s.handleMigrateAnyTLSS2))
		if s.orders != nil {
			mux.HandleFunc("/admin/order/confirm", s.adminAuth(s.handleOrderConfirm))
			mux.HandleFunc("/admin/order/cancel", s.adminAuth(s.handleOrderCancel))
		}
	}
	// olcRTC admin (independent of the provisioner): swap the carrier room when it expires,
	// or read the live config (the S1 swap script + inspection). GET reads, POST sets.
	if s.olc != nil && s.cfg.AdminToken != "" {
		mux.HandleFunc("/admin/olcrtc", s.adminAuth(s.handleOlcrtc))
		mux.HandleFunc("/admin/olcrtc/room", s.adminAuth(s.handleOlcrtcRoom))
	}
	// Web admin panel (password + session) — a public, browser-facing surface, separate from the
	// localhost-only bearer /admin/*. Registered only when configured (PanelPath + password hash).
	s.registerPanel(mux)
	return mux
}

// awgMinVC is the minimum app versionCode that may receive an "awg" endpoint in its /sub
// (= the version that ships the with_awg libbox). Default 999999 = OFF: until
// MAESTRO_AWG_MIN_VC is set to the shipped AWG version, NO client qualifies, so an awg
// endpoint never reaches a libbox that would HARD-FAIL the whole config on it.
var awgMinVC = func() int {
	if n, err := strconv.Atoi(os.Getenv("MAESTRO_AWG_MIN_VC")); err == nil && n > 0 {
		return n
	}
	return 999999
}()

// ParseOlcLogins parses a comma-separated MAESTRO_OLC_LOGINS value into the olcRTC allowlist.
// Used ONCE at startup to seed the mutable allowlist held in olcconf (the panel then manages it
// at runtime). Defaults to {wapmix} when unset. WHO gets olcRTC now lives in olcconf.Config.Logins
// (hot-reloadable), not this static value.
func ParseOlcLogins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		raw = "wapmix"
	}
	var out []string
	for _, l := range strings.Split(raw, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// appVersionCode parses the SFA app versionCode from the User-Agent, e.g.
// "SFA/1.0.106 (106; sing-box 1.14.0-alpha.31; language ru_RU)" → 106. Returns 0 for any
// non-SFA client (Karing, v2rayN, curl…) so they never qualify for the awg endpoint.
func appVersionCode(ua string) int {
	if !strings.HasPrefix(ua, "SFA/") {
		return 0
	}
	i := strings.IndexByte(ua, '(')
	if i < 0 {
		return 0
	}
	rest := ua[i+1:]
	j := strings.IndexByte(rest, ';')
	if j < 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(rest[:j]))
	return n
}

func (s *Server) handleSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/sub/")
	tok := rest
	wantHelpers := strings.HasSuffix(rest, "/helpers")
	wantInfo := strings.HasSuffix(rest, "/info")
	switch {
	case wantHelpers:
		tok = strings.TrimSuffix(rest, "/helpers")
	case wantInfo:
		tok = strings.TrimSuffix(rest, "/info")
	}
	// Tolerate a TV-app 1.0.74 bug: the stored sub URL's ?device=<id> query got
	// concatenated with the /helpers (or /info) suffix — "/sub/<tok>?device=<id>/helpers"
	// — so the suffix landed in the query and the PATH lost it. Recover the intent so
	// already-installed 1.0.74 devices keep working. The mangled device value fails
	// deviceIDRe → not counted.
	if !wantHelpers && !wantInfo {
		if dev := r.URL.Query().Get("device"); strings.HasSuffix(dev, "/helpers") {
			wantHelpers = true
		} else if strings.HasSuffix(dev, "/info") {
			wantInfo = true
		}
	}
	if tok == "" || strings.Contains(tok, "/") {
		http.NotFound(w, r)
		return
	}
	c, err := s.st.ByToken(tok)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Account info (login + days left) is served even when EXPIRED, so the app can
	// always show "Аккаунт: <login> · осталось N дней" (N=0 once expired).
	if wantInfo {
		s.writeSubInfo(w, c)
		return
	}
	if !c.Active() {
		http.Error(w, "subscription expired", http.StatusPaymentRequired)
		return
	}
	// Per-account device cap (covers all 4 protocols at this chokepoint). Only the
	// app sends a device id; Karing/bot subs send none and pass through untouched.
	if !s.deviceAllowed(w, c.Login, deviceID(r)) {
		return
	}
	if wantHelpers {
		s.writeHelpers(w, c)
		return
	}
	// ⛔ AWG version-gate: an "awg" endpoint only parses on the with_awg libbox (app
	// versionCode >= awgMinVC). The PLAIN/older libbox HARD-FAILS the ENTIRE config on an
	// awg endpoint → strip WG for any client older than that (or non-SFA, e.g. Karing/curl,
	// which return 0) so it NEVER reaches a device that can't handle it.
	sc := c.ToSubgen()
	if sc.WG != nil && appVersionCode(r.UserAgent()) < awgMinVC {
		sc.WG = nil
	}
	// olcRTC creds-gate: emit the olcRTC socks outbound ONLY for the owner's logins, and ONLY
	// when the GLOBAL olcRTC config is enabled+configured (olcconf — single source of truth,
	// hot-swappable). The WebRTC params (room/key) ride over /info, same gate; no creds = no
	// tunnel even if the UI is bypassed. sc.OLC starts nil (no per-customer creds), so a
	// non-allowlisted login can never get it.
	if oc := s.olcConfig(); oc.Enabled && oc.Allowed(c.Login) {
		if room, key, ok := oc.RoomFor(c.Login); ok {
			sc.OLC = &subgen.OLCRTCCreds{Provider: oc.Provider, Room: room, Key: key, Transport: oc.Transport}
		}
	}
	// Universal share-links subscription for cross-platform clients (Karing on
	// iPhone, v2rayN, NekoBox, Shadowrocket…), requested via ?app=karing /
	// ?format=links.
	if q := r.URL.Query(); q.Get("app") == "karing" || q.Get("format") == "links" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(subgen.ShareLinks(sc)))
		return
	}
	cfg, err := subgen.GenerateSingbox(sc)
	if err != nil {
		http.Error(w, "config error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(cfg)
}

// writeHelpers serves GET /sub/<token>/helpers. Every protocol is a native sing-box
// outbound now, so no on-device helper creds are needed and this always returns {}.
// Kept as a 200-{} stub so older installed apps that still poll the path don't 404.
func (s *Server) writeHelpers(w http.ResponseWriter, c *store.Customer) {
	out := map[string]any{}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// writeSubInfo serves GET /sub/<token>/info — the customer's login + days remaining, so
// the app can show "Аккаунт: <login> · осталось N дней". Served even when expired (then
// active=false, days_left=0). The token is the per-customer secret, so no extra auth.
func (s *Server) writeSubInfo(w http.ResponseWriter, c *store.Customer) {
	d := time.Until(c.Expires)
	daysLeft := int(d / (24 * time.Hour))
	if d > 0 && d%(24*time.Hour) != 0 {
		daysLeft++ // count any partial day as a remaining day
	}
	if daysLeft < 0 {
		daysLeft = 0
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{
		"login":     c.Login,
		"expires":   c.Expires,
		"days_left": daysLeft,
		"active":    c.Active(),
	}
	// olcRTC WebRTC params (provider/room/key/transport) — NOT sing-box fields, so they can't
	// ride in /sub; the app writes them into the child's client.yaml. From the GLOBAL olcconf
	// (hot-swappable room), gated to the owner's logins (same set as the /sub creds-gate). The
	// token is the per-customer secret, so the key is no more exposed than the rest of /info.
	if oc := s.olcConfig(); oc.Enabled && oc.Allowed(c.Login) {
		if room, key, ok := oc.RoomFor(c.Login); ok {
			out["olcrtc"] = map[string]any{
				"provider":  oc.Provider,
				"room":      room,
				"key":       key,
				"transport": oc.Transport,
			}
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// olcConfig returns the live global olcRTC config, or a zero (not-Ready) config when olcRTC
// is disabled (s.olc == nil) — so callers can always call .Ready() without a nil check.
func (s *Server) olcConfig() olcconf.Config {
	if s.olc == nil {
		return olcconf.Config{}
	}
	return s.olc.Get()
}

// deviceID extracts the app's per-install device id from a request — query param
// ?device=<id> (used on /sub polls) or the X-Device-Id header. Empty when the caller
// isn't our app (e.g. a Karing client polling the share-links sub), which disables the
// cap for that request.
func deviceID(r *http.Request) string {
	if d := r.URL.Query().Get("device"); d != "" {
		return d
	}
	return r.Header.Get("X-Device-Id")
}

// deviceAllowed enforces the per-account device cap. It returns true (allow) when the cap
// is off, the provisioner is absent, no/invalid device id is sent, the device is already
// known, or the account is below its cap (recording the new device). It returns false and
// writes a 403 only when a NEW device would exceed the cap. wapmix/wapmixx get cap 0
// (unlimited) via the provisioner, so they are never blocked.
func (s *Server) deviceAllowed(w http.ResponseWriter, login, dev string) bool {
	if !s.cfg.EnforceDeviceLimit || s.prov == nil || dev == "" || !deviceIDRe.MatchString(dev) {
		return true
	}
	allowed, _, err := s.st.TouchDeviceByLogin(login, dev, s.prov.DeviceLimitFor(login))
	if err == nil && !allowed {
		http.Error(w, "device limit reached", http.StatusForbidden)
		return false
	}
	return true
}

// updateWaypoints are MANDATORY intermediate versions every device must install IN ORDER
// before being offered anything newer (env MAESTRO_UPDATE_WAYPOINTS, comma-separated
// versionCodes; empty = no waypoints = always latest). Used to force the AWG-engine
// introduction (107) as a checkpoint: a device on <107 is offered 1.0.107 first (never a
// later build that carries awg creds), then may skip versions freely once it's on ≥107.
var updateWaypoints = func() []int {
	var ws []int
	for _, p := range strings.Split(os.Getenv("MAESTRO_UPDATE_WAYPOINTS"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 {
			ws = append(ws, n)
		}
	}
	sort.Ints(ws)
	return ws
}()

// manifestCode reads version_code from a manifest file (0 on error/missing).
func manifestCode(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var m struct {
		VersionCode int `json:"version_code"`
	}
	_ = json.Unmarshal(b, &m)
	return m.VersionCode
}

// updateManifestFor picks the manifest filename to serve a device on versionCode v: the
// smallest waypoint strictly between v and the latest version (so the device steps UP to it
// next), else "update.json" (the latest). Falls back to latest if the frozen waypoint manifest
// is missing. Non-app clients (v<=0) and the no-waypoint config always get the latest.
func updateManifestFor(dir string, v int) string {
	if v <= 0 || len(updateWaypoints) == 0 {
		return "update.json"
	}
	latest := manifestCode(filepath.Join(dir, "update.json"))
	for _, w := range updateWaypoints { // sorted ascending → first match is the smallest
		if v < w && w < latest {
			f := "update-" + strconv.Itoa(w) + ".json"
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				return f
			}
		}
	}
	return "update.json"
}

// handleUpdate serves the panel-hosted OTA channel: GET /update/update.json (the
// manifest the app reads to learn the latest version_code/version_name/apk_url/
// sha256) and GET /update/<file>.apk (the APK bytes, with Range/resume support for
// the ~140MB file). Read-only static serving from cfg.UpdateDir; only bare
// .json/.apk filenames are allowed (no path traversal). This is the update source
// the app prefers because the device reaches wapmixx.ru but not GitHub from RU.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.UpdateDir == "" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/update/")
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	// Staged rollout: route the manifest by the requesting app's versionCode so a device
	// behind a mandatory waypoint (e.g. the AWG-engine 107) is stepped UP to it before any
	// newer build. updateManifestFor returns a safe "update[-N].json" name (no traversal).
	if name == "update.json" {
		name = updateManifestFor(s.cfg.UpdateDir, appVersionCode(r.UserAgent()))
	}
	switch {
	case strings.HasSuffix(name, ".json"):
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
	case strings.HasSuffix(name, ".apk"):
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Header().Set("Cache-Control", "public, max-age=86400")
	default:
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.UpdateDir, name))
}

// handleClaim is the public install-time exchange. The customer enters the short
// code the owner gave them (their login) and the app receives its subscription
// URL to register as an auto-updating remote profile — so no key is ever typed or
// moved by hand on the TV. The login is the claim secret (owner should issue
// non-trivial codes); the sub token inside the returned URL stays the real
// per-customer secret. Returns the customer even if expired so the app can show
// "renew" — the /sub poll itself enforces expiry with 402.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Code   string `json:"code"`
		Device string `json:"device"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}
	dev := strings.TrimSpace(req.Device)
	if dev == "" {
		dev = deviceID(r)
	}
	// A claim code is a login (3x-ui email / naive username). Reject anything
	// outside a safe charset at the door — this code flows unauthenticated into
	// server-2 SSH commands, so quotes/spaces/; must never get past here.
	if !claimCodeRe.MatchString(code) {
		http.Error(w, "invalid code", http.StatusBadRequest)
		return
	}
	c, err := s.st.ByLogin(code)
	if errors.Is(err, store.ErrNotFound) {
		// Fallback: a customer who already exists in the panels (3x-ui/naive)
		// activating by their login — look them up and mirror their access.
		if s.prov != nil {
			if ec, aerr := s.prov.ActivateExisting(code); aerr == nil {
				if !s.deviceAllowed(w, ec.Login, dev) {
					return
				}
				s.respCustomer(w, ec)
				return
			}
		}
		http.Error(w, "unknown code", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !s.deviceAllowed(w, c.Login, dev) {
		return
	}
	s.respCustomer(w, c)
}
