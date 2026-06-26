// Package api serves the customer-facing subscription endpoint: the app polls
// GET /sub/<token> and receives its current sing-box configuration. Because the
// config is generated live from the store, a key rotation (or a renewal) is
// picked up on the next poll with no action by the customer.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
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
	// BackfillS3 gives the 3rd node (S3 VLESS-Reality + Trojan) to existing customers that
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
}

// deviceIDRe bounds the app's per-install device id (a random UUID the app generates and
// stores locally). Anything outside this charset is treated as "no device id" → no cap
// enforcement, so a malformed value can never block a customer or poison the device set.
var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Config tunes the api server.
type Config struct {
	AdminToken string // bearer token guarding /admin/*; empty disables admin API
	SubBaseURL string // public base for building sub URLs, e.g. https://wapmixx.ru:8910
	SBPPhone   string // СБП phone shown to the customer for in-app purchase
	PayURL     string // pay link (T-Bank «Сбор денег» / СБП) shown as a scannable QR — cross-bank, no acquiring; empty → fall back to the phone QR
	TGBotToken string // bot token for owner payment notifications (send-only, no poll)
	TGAdminID  string // owner's Telegram chat id
	UpdateDir  string // dir holding the panel-hosted OTA channel (update.json + *.apk); empty disables /update/
	ReportDir  string // dir for the fleet crash/diagnostic log (JSON-lines per day); empty disables /report
	// EnforceDeviceLimit gates the per-account 5-device cap at /sub + /claim. A kill
	// switch (env MAESTRO_DEVICE_LIMIT=off) so the cap can be disabled live without a
	// redeploy if it ever misbehaves in prod.
	EnforceDeviceLimit bool
}

// Server wires the HTTP handlers to the store and (optionally) the provisioner.
type Server struct {
	st     *store.Store
	prov   Provisioner
	orders *order.Store
	tg     *telegram.Client
	cfg    Config
}

// New returns an api server. prov/orders may be nil to disable those routes.
func New(st *store.Store, prov Provisioner, orders *order.Store, cfg Config) *Server {
	return &Server{st: st, prov: prov, orders: orders, tg: telegram.New(cfg.TGBotToken), cfg: cfg}
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
	return mux
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
	// Universal share-links subscription for cross-platform clients (Karing on
	// iPhone, v2rayN, NekoBox, Shadowrocket…), requested via ?app=karing /
	// ?format=links.
	if q := r.URL.Query(); q.Get("app") == "karing" || q.Get("format") == "links" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(subgen.ShareLinks(c.ToSubgen())))
		return
	}
	cfg, err := subgen.GenerateSingbox(c.ToSubgen())
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"login":     c.Login,
		"expires":   c.Expires,
		"days_left": daysLeft,
		"active":    c.Active(),
	})
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
