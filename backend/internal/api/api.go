// Package api serves the customer-facing subscription endpoint: the app polls
// GET /sub/<token> and receives its current sing-box configuration. Because the
// config is generated live from the store, a key rotation (or a renewal) is
// picked up on the next poll with no action by the customer.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
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
}

// Config tunes the api server.
type Config struct {
	AdminToken string // bearer token guarding /admin/*; empty disables admin API
	SubBaseURL string // public base for building sub URLs, e.g. https://wapmixx.ru:8910
	SBPPhone   string // СБП phone shown to the customer for in-app purchase
	TGBotToken string // bot token for owner payment notifications (send-only, no poll)
	TGAdminID  string // owner's Telegram chat id
	UpdateDir  string // dir holding the panel-hosted OTA channel (update.json + *.apk); empty disables /update/
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
	// Diagnostic sink: the on-device Mieru helper POSTs its run output/status here
	// so the actual failure is visible server-side (read via journalctl). No auth —
	// it only writes to the panel log, capped, token charset-checked.
	mux.HandleFunc("/mierulog/", s.handleMieruLog)
	if s.cfg.UpdateDir != "" {
		// Panel-hosted OTA update channel — the RU-reachable update source the app
		// prefers over GitHub (throttled from Russian ISPs). Same host:port the
		// device already polls for /sub every 15 min.
		mux.HandleFunc("/update/", s.handleUpdate)
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
		mux.HandleFunc("/admin/customer", s.adminAuth(s.handleCustomer))
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
	if wantHelpers {
		s.writeHelpers(w, c)
		return
	}
	// Universal share-links subscription for cross-platform clients (Karing on
	// iPhone, v2rayN, NekoBox, Shadowrocket…), requested via ?app=karing /
	// ?format=links. Mieru is excluded (Android-app-only — needs the local helper).
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

// writeHelpers serves GET /sub/<token>/helpers — the server creds for protocols
// the app runs as a bundled local SOCKS helper (only Mieru: sing-box dials the
// helper on 127.0.0.1:<socks>, the helper authenticates to mita with these). VLESS
// / Hy2 / Naive are native sing-box outbounds and need nothing here, so the object
// is empty when the customer has no helper protocols.
func (s *Server) writeHelpers(w http.ResponseWriter, c *store.Customer) {
	out := map[string]any{}
	if c.Mieru != nil {
		out["mieru"] = map[string]any{
			"server":    c.Mieru.Server,
			"port":      c.Mieru.Port,
			"username":  c.Mieru.Username,
			"password":  c.Mieru.Password,
			"transport": c.Mieru.Transport,
			"socks":     c.Mieru.HelperSOCKS,
		}
	}
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

// handleMieruLog records the on-device Mieru helper's run output so the actual
// failure (exec error, bad config, auth/connection error from mita) is visible in
// the panel log without device access. Diagnostic only — writes nothing but a log
// line; body capped at 8KB; token must match the safe charset.
func (s *Server) handleMieruLog(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.URL.Path, "/mierulog/")
	if !claimCodeRe.MatchString(tok) {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	log.Printf("mierulog [%s]: %s", tok, strings.TrimSpace(string(body)))
	w.WriteHeader(http.StatusNoContent)
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
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
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
	s.respCustomer(w, c)
}
