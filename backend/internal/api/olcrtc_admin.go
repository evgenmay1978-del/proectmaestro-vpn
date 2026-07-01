package api

import (
	"net/http"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
)

// handleOlcrtc (admin) reads or replaces the GLOBAL olcRTC config (the single source of
// truth for room/key/provider/transport/enabled). GET returns it (for the S1 swap script +
// inspection); POST replaces it (key rotation, enable/disable). Behind adminAuth (bearer),
// so the key is no more exposed than any other admin data.
func (s *Server) handleOlcrtc(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.olc.Get())
	case http.MethodPost:
		var c olcconf.Config
		if !decodeJSON(w, r, &c) {
			return
		}
		if err := s.olc.Set(c); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, s.olc.Get())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleOlcrtcRoom (admin) swaps ONLY the carrier room URL — the common operation when a
// Yandex Telemost room expires. Keeps the key/provider/transport and auto-enables once
// room+key are both set. Clients pick up the new room on their next /info poll; the S1 swap
// script (ops/olcrtc-room.sh) also pushes it to the S3 srv, so one call updates both.
func (s *Server) handleOlcrtcRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Room string `json:"room"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Room == "" {
		http.Error(w, "room required", http.StatusBadRequest)
		return
	}
	if err := s.olc.SetRoom(req.Room); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.olc.Get())
}
