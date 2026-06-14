// Package api serves the customer-facing subscription endpoint: the app polls
// GET /sub/<token> and receives its current sing-box configuration. Because the
// config is generated live from the store, a key rotation (or a renewal) is
// picked up on the next poll with no action by the customer.
package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/order"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// Provisioner is the subset of the provision package the admin API drives.
type Provisioner interface {
	Provision(login string, dur time.Duration) (*store.Customer, error)
	Extend(login string, dur time.Duration) (*store.Customer, error)
}

// Config tunes the api server.
type Config struct {
	AdminToken string // bearer token guarding /admin/*; empty disables admin API
	SubBaseURL string // public base for building sub URLs, e.g. https://wapmixx.ru:8910
	SBPPhone   string // СБП phone shown to the customer for in-app purchase
}

// Server wires the HTTP handlers to the store and (optionally) the provisioner.
type Server struct {
	st     *store.Store
	prov   Provisioner
	orders *order.Store
	cfg    Config
}

// New returns an api server. prov/orders may be nil to disable those routes.
func New(st *store.Store, prov Provisioner, orders *order.Store, cfg Config) *Server {
	return &Server{st: st, prov: prov, orders: orders, cfg: cfg}
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
	if s.orders != nil {
		mux.HandleFunc("/order/tariffs", s.handleTariffs)
		mux.HandleFunc("/order", s.handleOrderCreate)
		mux.HandleFunc("/order/", s.handleOrderGet)
	}
	if s.prov != nil && s.cfg.AdminToken != "" {
		mux.HandleFunc("/admin/provision", s.adminAuth(s.handleProvision))
		mux.HandleFunc("/admin/extend", s.adminAuth(s.handleExtend))
		mux.HandleFunc("/admin/customer", s.adminAuth(s.handleCustomer))
		if s.orders != nil {
			mux.HandleFunc("/admin/order/confirm", s.adminAuth(s.handleOrderConfirm))
		}
	}
	return mux
}

func (s *Server) handleSub(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.URL.Path, "/sub/")
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
	if !c.Active() {
		http.Error(w, "subscription expired", http.StatusPaymentRequired)
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
	c, err := s.st.ByLogin(code)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "unknown code", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.respCustomer(w, c)
}
