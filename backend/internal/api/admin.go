package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
)

// adminAuth guards a handler with a constant-time bearer-token check.
func (s *Server) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	want := []byte("Bearer " + s.cfg.AdminToken)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type provisionReq struct {
	Login string `json:"login"`
	Days  int    `json:"days"`
}

type customerResp struct {
	Login     string    `json:"login"`
	SubURL    string    `json:"sub_url"`
	Expires   time.Time `json:"expires"`
	Active    bool      `json:"active"`
	Protocols []string  `json:"protocols"`
}

func (s *Server) respCustomer(w http.ResponseWriter, c *store.Customer) {
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
	if c.Mieru != nil {
		protos = append(protos, "mieru")
	}
	writeJSON(w, http.StatusOK, customerResp{
		Login:     c.Login,
		SubURL:    s.cfg.SubBaseURL + "/sub/" + c.SubToken,
		Expires:   c.Expires,
		Active:    c.Active(),
		Protocols: protos,
	})
}

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	var req provisionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Login == "" || req.Days <= 0 {
		http.Error(w, "login and positive days required", http.StatusBadRequest)
		return
	}
	c, err := s.prov.Provision(req.Login, time.Duration(req.Days)*24*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.respCustomer(w, c)
}

func (s *Server) handleExtend(w http.ResponseWriter, r *http.Request) {
	var req provisionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Login == "" || req.Days <= 0 {
		http.Error(w, "login and positive days required", http.StatusBadRequest)
		return
	}
	c, err := s.prov.Extend(req.Login, time.Duration(req.Days)*24*time.Hour)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such customer", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.respCustomer(w, c)
}

// handleSetExpiry (admin): MIRROR an absolute expiry set by a channel that owns the date
// (the s2 naive bot) into the unified account — sets the store date (no stacking) + fans
// it out to the customer's other protocols (VLESS date, Hy2/Mieru membership), without
// touching the raw naive the s2 bot owns. Backfills via ActivateExisting if not in store.
func (s *Server) handleSetExpiry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Login   string `json:"login"`
		Expires string `json:"expires"` // RFC3339
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	t, perr := time.Parse(time.RFC3339, req.Expires)
	if req.Login == "" || perr != nil {
		http.Error(w, "login and RFC3339 expires required", http.StatusBadRequest)
		return
	}
	if _, err := s.st.ByLogin(req.Login); errors.Is(err, store.ErrNotFound) {
		if _, aerr := s.prov.ActivateExisting(req.Login); aerr != nil {
			http.Error(w, aerr.Error(), http.StatusNotFound)
			return
		}
	}
	c, err := s.prov.SetExpiry(req.Login, t)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such customer", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.respCustomer(w, c)
}

// handleRenew (admin): the SINGLE cross-channel renewal entrypoint for the "unified
// account". Renews a customer by login across ALL 4 protocols regardless of which
// channel sold/confirmed the payment (app, s1 3x-ui bot, s2 naive bot — all call this
// with login = the shared key: 3x-ui email = naive username). If the login is already
// a panel customer → Extend; if it's a bot-only client not yet in the store →
// ActivateExisting (backfill from 3x-ui/naive at their current expiry) then Extend, so
// their remaining time is kept and the renewal days are stacked on top.
func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	var req provisionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Login == "" || req.Days <= 0 {
		http.Error(w, "login and positive days required", http.StatusBadRequest)
		return
	}
	if _, err := s.st.ByLogin(req.Login); errors.Is(err, store.ErrNotFound) {
		// Not in the panel store yet (a bot-only client) → backfill from the bot panels.
		if _, aerr := s.prov.ActivateExisting(req.Login); aerr != nil {
			http.Error(w, aerr.Error(), http.StatusNotFound)
			return
		}
	}
	c, err := s.prov.Extend(req.Login, time.Duration(req.Days)*24*time.Hour)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such customer", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.respCustomer(w, c)
}

func (s *Server) handleCustomer(w http.ResponseWriter, r *http.Request) {
	login := r.URL.Query().Get("login")
	c, err := s.st.ByLogin(login)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such customer", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.respCustomer(w, c)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
