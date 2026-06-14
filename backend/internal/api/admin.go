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
	Login   string    `json:"login"`
	SubURL  string    `json:"sub_url"`
	Expires time.Time `json:"expires"`
	Active  bool      `json:"active"`
}

func (s *Server) respCustomer(w http.ResponseWriter, c *store.Customer) {
	writeJSON(w, http.StatusOK, customerResp{
		Login:   c.Login,
		SubURL:  s.cfg.SubBaseURL + "/sub/" + c.SubToken,
		Expires: c.Expires,
		Active:  c.Active(),
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
