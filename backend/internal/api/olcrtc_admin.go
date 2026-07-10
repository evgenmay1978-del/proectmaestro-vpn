package api

import (
	"net/http"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
)

// olcMaxCarrierReady gates the "max" (MAX messenger) carrier. It stays false until the olcrtc
// binary pinned by OLCRTC_REF ships a "max" auth provider — the current binary knows only
// jitsi/telemost/wbstream, so a "provider: max" YAML would be rejected and the exit would never
// join. The whole backend/app/ops plumbing for "max" is staged behind this flag; flip it to true
// in the SAME change that bumps OLCRTC_REF to a build with MAX support. See docs/olcrtc-max-carrier.md.
const olcMaxCarrierReady = false

// olcMaxNotReadyMsg is the operator-facing reason "max" is refused while the gate is closed.
const olcMaxNotReadyMsg = "carrier MAX ещё не поддерживается текущим бинарником olcrtc " +
	"(нужен OLCRTC_REF с провайдером max) — см. docs/olcrtc-max-carrier.md"

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

// handleOlcrtcRoom (admin) swaps a carrier room URL — the common operation when a Yandex
// Telemost room expires. With "login" set, it swaps that login's DEDICATED room (isolation);
// without it, the GLOBAL default room. An optional "key" sets a per-login shared key (leave
// empty to keep the existing one — a room-only swap must not wipe the key + desync the srv).
// Keeps provider/transport and auto-enables once any room+key exists. Clients pick up the new
// room on their next /info poll; the S1 swap script (ops/olcrtc-room.sh [login] <url>) also
// pushes it to that login's S3 srv, so one call updates both.
func (s *Server) handleOlcrtcRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Login    string `json:"login"` // empty → global room; else this login's dedicated room
		Room     string `json:"room"`
		Key      string `json:"key"`      // optional; empty keeps the existing key
		Provider string `json:"provider"` // optional; "wbstream"|"telemost"|"max"; empty keeps existing
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Room == "" {
		http.Error(w, "room required", http.StatusBadRequest)
		return
	}
	if req.Provider == "max" && !olcMaxCarrierReady {
		http.Error(w, olcMaxNotReadyMsg, http.StatusBadRequest)
		return
	}
	if err := s.olc.SetRoomProviderFor(req.Login, req.Room, req.Key, req.Provider); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.olc.Get())
}
