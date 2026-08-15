package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnprobe"
)

// vkTurnClientReq is one login's editable fields in a panel save. Pointer fields
// distinguish "absent" (keep current) from "present"; a blank secret string also
// means "keep current" (the browser never receives the secret to echo back).
type vkTurnClientReq struct {
	Password *string `json:"password"`
	WG       struct {
		PrivateKey    *string `json:"private_key"`
		PeerPublicKey *string `json:"peer_public_key"`
		LocalAddress  *string `json:"local_address"`
	} `json:"wg"`
}

// vkTurnSaveReq is the panel's WDTT save payload. `enabled` is intentionally NOT in
// the full-save form (the master switch is a separate endpoint), so a save never
// flips the transport on/off by accident.
type vkTurnSaveReq struct {
	Enabled        *bool                      `json:"enabled"`
	MinVersionCode *int                       `json:"min_version_code"`
	Server         *string                    `json:"server"`
	VKHashes       []string                   `json:"vk_hashes"`
	Clients        map[string]vkTurnClientReq `json:"clients"`
}

// applyVKTurnEdit merges req onto cur (a clone, or nil for first-time setup),
// treating an absent OR blank field as "keep the current value". It does NOT
// validate — Store.Update validates the result atomically before persisting. Pure
// (no I/O), so the merge semantics are unit-tested directly.
func applyVKTurnEdit(cur *vkturnconf.Config, req vkTurnSaveReq) *vkturnconf.Config {
	next := cur
	if next == nil {
		next = &vkturnconf.Config{}
	}
	if next.Clients == nil {
		next.Clients = map[string]vkturnconf.Client{}
	}
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	if req.MinVersionCode != nil {
		next.MinVersionCode = *req.MinVersionCode
	}
	if req.Server != nil {
		next.Server = strings.TrimSpace(*req.Server)
	}
	for login, rc := range req.Clients {
		cl := next.Clients[login]
		if rc.Password != nil && strings.TrimSpace(*rc.Password) != "" {
			cl.Password = strings.TrimSpace(*rc.Password)
		}
		if rc.WG.PrivateKey != nil && strings.TrimSpace(*rc.WG.PrivateKey) != "" {
			cl.WG.PrivateKey = strings.TrimSpace(*rc.WG.PrivateKey)
		}
		if rc.WG.PeerPublicKey != nil && strings.TrimSpace(*rc.WG.PeerPublicKey) != "" {
			cl.WG.PeerPublicKey = strings.TrimSpace(*rc.WG.PeerPublicKey)
		}
		if rc.WG.LocalAddress != nil && strings.TrimSpace(*rc.WG.LocalAddress) != "" {
			cl.WG.LocalAddress = strings.TrimSpace(*rc.WG.LocalAddress)
		}
		next.Clients[login] = cl
	}
	return next
}

// vkTurnRedacted renders the WDTT config for the panel WITHOUT the secrets. WG
// private keys and per-login passwords are never sent to the browser — only a
// "*_set" boolean so the operator sees whether a value is present. The public
// bits (server, peer public key, tunnel address, enabled, min version) are
// returned so the form can be pre-filled. Room values are write-only: the
// browser receives only counts and fixed probe status/code. Mirrors wbTokenSet()'s "show
// presence, never the value" rule for the wbstream token.
func (s *Server) vkTurnRedacted() map[string]any {
	out := map[string]any{
		"logins":     vkturnconf.AllowedLogins(),
		"configured": false,
	}
	cfg := s.vkTurnConfig()
	if cfg == nil {
		return out
	}
	clients := map[string]any{}
	for login, cl := range cfg.Clients {
		clients[login] = map[string]any{
			"password_set": cl.Password != "",
			"wg": map[string]any{
				"peer_public_key": cl.WG.PeerPublicKey,
				"local_address":   cl.WG.LocalAddress,
				"private_key_set": cl.WG.PrivateKey != "",
			},
		}
	}
	out["configured"] = true
	out["enabled"] = cfg.Enabled
	out["min_version_code"] = cfg.MinVersionCode
	out["server"] = cfg.Server
	out["probe_status"] = string(cfg.ProbeStatus)
	out["active_count"] = len(cfg.VKHashes)
	out["candidate_count"] = len(cfg.CandidateVKHashes)
	out["last_known_good_count"] = len(cfg.LastKnownGoodVKHashes)
	if !cfg.ProbeStartedAt.IsZero() {
		out["probe_started_at"] = cfg.ProbeStartedAt.UTC().Format(time.RFC3339)
	}
	if !cfg.ProbeCheckedAt.IsZero() {
		out["probe_checked_at"] = cfg.ProbeCheckedAt.UTC().Format(time.RFC3339)
	}
	if cfg.ProbeErrorCode != "" {
		out["probe_error_code"] = cfg.ProbeErrorCode
	}
	out["clients"] = clients
	return out
}

// panelVKTurn is the panel's WDTT/VK-TURN editor. GET returns the redacted config;
// POST saves the non-room config (merged, validated, persisted atomically). Secret fields
// left blank on POST keep their existing value, so the operator can change public
// settings without re-typing every WG key ("leave blank to keep" — the same
// UX as the wbstream token).
func (s *Server) panelVKTurn(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.panelGuard(w, r, false) {
			return
		}
		writeJSON(w, http.StatusOK, s.vkTurnRedacted())
	case http.MethodPost:
		if !s.panelGuard(w, r, true) {
			return
		}
		if s.vkturn == nil {
			http.Error(w, "vkturn storage not configured (MAESTRO_VKTURN_FILE unset)", http.StatusBadRequest)
			return
		}
		var req vkTurnSaveReq
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.VKHashes != nil {
			http.Error(w, "room changes must use the verified candidate endpoint", http.StatusBadRequest)
			return
		}
		if req.MinVersionCode != nil && *req.MinVersionCode >= 90000 {
			http.Error(w, "min_version_code must use the production APK versionCode (for example 156); test-build codes 90xxx are not allowed", http.StatusBadRequest)
			return
		}
		// Update does the read-merge-validate-persist-swap atomically under one lock
		// (fail-closed: a bad edit is rejected and the previous config keeps serving).
		if err := s.vkturn.Update(func(cur *vkturnconf.Config) *vkturnconf.Config {
			return applyVKTurnEdit(cur, req)
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, s.vkTurnRedacted())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type vkTurnCandidateReq struct {
	VKHashes []string `json:"vk_hashes"`
}

func normalizeVKTurnCandidates(inputs []string) []string {
	hashes := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if hash := normalizeVKCallInput(input); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	return hashes
}

// panelVKTurnCandidate stages a write-only room list, runs the pinned provider
// probe, and promotes it only on a fixed validated success result. Every failure
// leaves the current active/LKG room list serving installed clients.
func (s *Server) panelVKTurnCandidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.panelGuard(w, r, true) {
		return
	}
	if s.vkturn == nil {
		http.Error(w, "vkturn storage not configured (MAESTRO_VKTURN_FILE unset)", http.StatusBadRequest)
		return
	}
	var req vkTurnCandidateReq
	if !decodeJSON(w, r, &req) {
		return
	}
	hashes := normalizeVKTurnCandidates(req.VKHashes)
	generation, err := s.vkturn.StageCandidate(hashes, time.Now().UTC())
	if err != nil {
		if cfg := s.vkturn.Get(); cfg != nil && cfg.ProbeStatus == vkturnconf.ProbeStatusChecking {
			http.Error(w, "candidate probe already running", http.StatusConflict)
			return
		}
		http.Error(w, "invalid candidate", http.StatusBadRequest)
		return
	}

	result := vkturnprobe.Result{Stage: "STARTING", Code: "PROBE_UNAVAILABLE"}
	if s.vkprober != nil {
		result = s.vkprober.Probe(r.Context(), hashes)
	}
	if safe, ok := vkturnprobe.Sanitize(result); ok {
		result = safe
	} else {
		result = vkturnprobe.Result{Stage: "STARTING", Code: "PROBE_OUTPUT_INVALID"}
	}
	checkedAt := time.Now().UTC()
	if result.OK {
		if err := s.vkturn.PromoteCandidate(generation, checkedAt); err != nil {
			panelErrLog(w, http.StatusInternalServerError, "candidate state update failed", "promote vkturn candidate", err)
			return
		}
	} else if err := s.vkturn.RejectCandidate(generation, result.Code, checkedAt); err != nil {
		panelErrLog(w, http.StatusInternalServerError, "candidate state update failed", "reject vkturn candidate", err)
		return
	}
	out := s.vkTurnRedacted()
	out["probe_stage"] = result.Stage
	writeJSON(w, http.StatusOK, out)
}

// panelVKTurnEnabled is the quick master switch (POST {enabled}). The config must
// already be fully set (Validate rejects a partial one) before it can be enabled.
func (s *Server) panelVKTurnEnabled(w http.ResponseWriter, r *http.Request) {
	if !s.panelGuard(w, r, true) {
		return
	}
	if s.vkturn == nil {
		http.Error(w, "vkturn storage not configured (MAESTRO_VKTURN_FILE unset)", http.StatusBadRequest)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if s.vkTurnConfig() == nil {
		http.Error(w, "vkturn not configured yet — save the full config first", http.StatusBadRequest)
		return
	}
	err := s.vkturn.Update(func(cur *vkturnconf.Config) *vkturnconf.Config {
		if cur == nil {
			return nil
		}
		cur.Enabled = req.Enabled
		return cur
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": req.Enabled})
}
