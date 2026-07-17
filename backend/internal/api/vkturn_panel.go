package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
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
	if req.VKHashes != nil {
		hashes := make([]string, 0, len(req.VKHashes))
		for _, h := range req.VKHashes {
			if h = strings.TrimSpace(h); h != "" {
				hashes = append(hashes, h)
			}
		}
		next.VKHashes = hashes
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
// bits (server, hashes, peer public key, tunnel address, enabled, min version)
// are returned so the form can be pre-filled. Mirrors wbTokenSet()'s "show
// presence, never the value" rule for the wbstream token.
func (s *Server) vkTurnRedacted() map[string]any {
	out := map[string]any{
		"configured":    false,
		"prov_ready":    s.wdtt != nil,
		"clients_limit": vkturnconf.MaxClients,
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
	out["vk_hashes"] = cfg.VKHashes
	out["clients"] = clients
	return out
}

// panelVKTurn is the panel's WDTT/VK-TURN editor. GET returns the redacted config;
// POST saves a full config (merged, validated, persisted atomically). Secret fields
// left blank on POST keep their existing value, so the operator can rotate a VK hash
// or flip the switch without re-typing every WG key ("leave blank to keep" — the same
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

// panelVKTurnClient adds or removes ONE login end-to-end (POST {login, action}).
// Add is the panel's "one click": generate the password + WireGuard identity,
// pre-provision the canary wdtt-server for every app install the customer store
// knows for that login (the password then binds to whichever phone connects
// first), restart the canary, and only after the server side is live publish the
// client half into vkturnconf — /sub must never serve creds the server does not
// know. Remove runs the other way (unpublish first, then tear down the server
// side) so the fail-open direction is always "login has no WDTT".
func (s *Server) panelVKTurnClient(w http.ResponseWriter, r *http.Request) {
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
	if s.wdtt == nil {
		http.Error(w, "WDTT provisioning not configured (MAESTRO_WDTT_CANARY_DIR unset)", http.StatusBadRequest)
		return
	}
	var req struct {
		Login  string `json:"login"`
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	login := strings.TrimSpace(req.Login)
	if login == "" {
		http.Error(w, "login is required", http.StatusBadRequest)
		return
	}
	cfg := s.vkTurnConfig()
	if cfg == nil {
		http.Error(w, "vkturn not configured yet — save the full config first", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "add":
		c, err := s.st.ByLogin(login)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "нет такого клиента", http.StatusNotFound)
			return
		}
		if err != nil {
			panelErrLog(w, http.StatusInternalServerError, "internal error", "vkturn client lookup", err)
			return
		}
		if !c.Active() {
			http.Error(w, "аккаунт истёк или отключён — продлите его сначала", http.StatusConflict)
			return
		}
		if _, exists := cfg.Clients[login]; exists {
			http.Error(w, "логин уже в списке WDTT", http.StatusConflict)
			return
		}
		if len(cfg.Clients) >= vkturnconf.MaxClients {
			http.Error(w, fmt.Sprintf("лимит WDTT-клиентов (%d) исчерпан", vkturnconf.MaxClients), http.StatusConflict)
			return
		}
		ids := make([]string, 0, len(c.Devices))
		for id := range c.Devices {
			if deviceIDRe.MatchString(id) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			http.Error(w, "у логина нет известных установок приложения — пусть клиент откроет приложение на телефоне, потом добавьте снова", http.StatusConflict)
			return
		}
		res, err := s.wdtt.AddClient(login, ids)
		if err != nil {
			http.Error(w, "провизия на wdtt-сервере не удалась: "+err.Error(), http.StatusBadGateway)
			return
		}
		err = s.vkturn.Update(func(cur *vkturnconf.Config) *vkturnconf.Config {
			if cur == nil {
				return nil
			}
			if cur.Clients == nil {
				cur.Clients = map[string]vkturnconf.Client{}
			}
			if _, raced := cur.Clients[login]; raced {
				return nil // lost a concurrent add — abort, rollback below removes OUR provision only
			}
			cur.Clients[login] = vkturnconf.Client{
				Password: res.Password,
				WG: subgen.VKTurnCreds{
					PrivateKey:    res.PrivateKey,
					PeerPublicKey: res.ServerPublicKey,
					LocalAddress:  res.LocalAddress,
				},
			}
			return cur
		})
		if err != nil {
			_ = s.wdtt.RemoveClient(res.Password, res.LocalAddress)
			http.Error(w, "сохранение конфига не удалось (сервер откатан): "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, s.vkTurnRedacted())
	case "remove":
		cl, exists := cfg.Clients[login]
		if !exists {
			http.Error(w, "логин не в списке WDTT", http.StatusNotFound)
			return
		}
		err := s.vkturn.Update(func(cur *vkturnconf.Config) *vkturnconf.Config {
			if cur == nil {
				return nil
			}
			delete(cur.Clients, login)
			return cur
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := s.vkTurnRedacted()
		if err := s.wdtt.RemoveClient(cl.Password, cl.WG.LocalAddress); err != nil {
			// The login is already unpublished from /sub (fail-safe direction); the
			// canary keeps a dead entry until the next successful add/remove.
			out["warning"] = "из /sub убран, но очистка wdtt-сервера не удалась: " + err.Error()
		}
		writeJSON(w, http.StatusOK, out)
	default:
		http.Error(w, "action must be add or remove", http.StatusBadRequest)
	}
}
