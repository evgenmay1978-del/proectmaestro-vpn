package api

import (
	"net/http"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/store"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
)

// VKTurnConfig remains an alias for API callers; the authoritative type and
// validation live in vkturnconf.
type VKTurnConfig = vkturnconf.Config

// vkTurnEligible is the single gate shared by the /sub endpoint family. A
// caller must explicitly identify as mobile; TV, missing/unknown platforms,
// non-SFA callers, and old app versions fail closed.
func (s *Server) vkTurnEligible(r *http.Request, c *store.Customer) bool {
	_, ok := s.vkTurnClient(r, c)
	return ok
}

// vkTurnClient is the single gate used by both /sub and /info. It returns the
// secrets only after every account/platform/version check has passed.
func (s *Server) vkTurnClient(r *http.Request, c *store.Customer) (vkturnconf.Client, bool) {
	cfg := s.cfg.VKTurn
	if cfg == nil || !cfg.Enabled || cfg.MinVersionCode <= 0 || c == nil || !c.Active() {
		return vkturnconf.Client{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("platform")), "mobile") {
		return vkturnconf.Client{}, false
	}
	client, ok := cfg.ClientFor(c.Login)
	if !ok || appVersionCode(r.UserAgent()) < cfg.MinVersionCode {
		return vkturnconf.Client{}, false
	}
	return client, true
}
